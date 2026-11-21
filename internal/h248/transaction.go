package h248

import (
	"fmt"
	"strconv"
	"strings"
)

// Transaction is one of the four things a message body is made of:
// TransactionRequest, TransactionReply, TransactionPending or
// TransactionResponseAck (H.248.1 8).
type Transaction interface {
	write(w *writer)
}

// TransactionRequest asks the receiver to carry out commands. The
// commands are grouped into actions, one for each context.
type TransactionRequest struct {
	// TransactionID is chosen by the sender. The reply repeats it.
	TransactionID uint32
	Actions       []ActionRequest
}

// TransactionReply answers a TransactionRequest. Either Error says why
// nothing in the request could be carried out, or Actions holds one
// reply for each action.
type TransactionReply struct {
	TransactionID uint32
	// ImmAckRequired asks the receiver to confirm the reply at once
	// with a TransactionResponseAck. A sender sets it after it has
	// sent a TransactionPending (H.248.1 Annex D.1.4).
	ImmAckRequired bool
	Error          *ErrorDescriptor
	Actions        []ActionReply
}

// TransactionPending tells the requester that the transaction is still
// being worked on, so that it does not send the request again (H.248.1
// 8.2.3).
type TransactionPending struct {
	TransactionID uint32
}

// TransactionResponseAck confirms that replies have arrived, so that
// the other side can forget them. It is what makes at-most-once
// delivery work over UDP (H.248.1 Annex D.1.2.2).
type TransactionResponseAck struct {
	Acks []TransactionAck
}

// TransactionAck confirms one reply, or every reply from FirstAck to
// LastAck when LastAck is not zero.
type TransactionAck struct {
	FirstAck uint32
	LastAck  uint32
}

// ActionRequest holds the commands of a request that work on one
// context, and the attributes of the context that the request sets.
type ActionRequest struct {
	ContextID      ContextID
	ContextRequest *ContextRequest
	Commands       []CommandRequest
}

// ActionReply holds the replies to the commands of one action. When a
// command fails, the replies up to it are followed by Error, or Error
// stands alone.
type ActionReply struct {
	ContextID ContextID
	Commands  []CommandReply
	Error     *ErrorDescriptor
}

// ContextID names a context. Three values are set apart (H.248.1
// 8.1.2); they are the numbers that the binary encoding uses for them
// (H.248.1 Annex A.2).
type ContextID uint32

const (
	// ContextNull ("-") is where terminations are that belong to no
	// context. The ROOT termination is addressed in it.
	ContextNull ContextID = 0
	// ContextChoose ("$") asks the media gateway to make a context.
	ContextChoose ContextID = 0xFFFFFFFE
	// ContextAll ("*") stands for every context.
	ContextAll ContextID = 0xFFFFFFFF
)

// String returns the context ID as it is written in a message.
func (c ContextID) String() string {
	switch c {
	case ContextNull:
		return "-"
	case ContextChoose:
		return "$"
	case ContextAll:
		return "*"
	}
	return strconv.FormatUint(uint64(c), 10)
}

func (s *scanner) contextID() (ContextID, error) {
	s.skipLWSP()
	switch s.peek() {
	case '-':
		s.pos++
		return ContextNull, nil
	case '$':
		s.pos++
		return ContextChoose, nil
	case '*':
		s.pos++
		return ContextAll, nil
	}
	n, err := s.uint(32)
	if err != nil {
		return 0, err
	}
	switch id := ContextID(n); id {
	case ContextNull, ContextChoose, ContextAll:
		return 0, s.errorf("context ID %d is reserved", n)
	default:
		return id, nil
	}
}

// transactionID reads "= TransactionID".
func (s *scanner) transactionID() (uint32, error) {
	if err := s.expect('='); err != nil {
		return 0, err
	}
	n, err := s.uint(32)
	return uint32(n), err
}

func (s *scanner) transactionRequest() (Transaction, error) {
	var t TransactionRequest
	var err error
	if t.TransactionID, err = s.transactionID(); err != nil {
		return nil, err
	}
	s.part = &requestPart{transactionID: t.TransactionID}
	err = s.list(func() error {
		if err := s.expectToken(tokContext); err != nil {
			return err
		}
		s.reach(inContextID)
		a, err := s.actionRequest()
		if err != nil {
			return err
		}
		t.Actions = append(t.Actions, a)
		s.part.actions, s.part.action = t.Actions, nil
		s.reach(inTransaction)
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.part = nil
	return t, nil
}

func (t TransactionRequest) write(w *writer) {
	if len(t.Actions) == 0 {
		w.fail("transaction request %d has no action", t.TransactionID)
	}
	var items []func()
	for _, a := range t.Actions {
		items = append(items, func() { w.actionRequest(a) })
	}
	w.block(fmt.Sprintf("%s = %d", tokTransaction, t.TransactionID), items)
}

func (s *scanner) actionRequest() (ActionRequest, error) {
	var a ActionRequest
	var err error
	if err = s.expect('='); err != nil {
		return a, err
	}
	if a.ContextID, err = s.contextID(); err != nil {
		return a, err
	}
	s.part.action = &ActionRequest{ContextID: a.ContextID}
	s.reach(inAction)
	err = s.list(func() error {
		optional := s.eatPrefix("O-")
		wildcardResponse := s.eatPrefix("W-")
		name, err := s.token()
		if err != nil {
			return err
		}
		if !optional && !wildcardResponse {
			if isProperty, err := s.contextProperty(name, &a); isProperty {
				if err == nil {
					s.part.readInFull(a)
				}
				return err
			}
		}
		c, err := s.commandRequest(name)
		if err != nil {
			return err
		}
		c.Optional, c.WildcardResponse = optional, wildcardResponse
		a.Commands = append(a.Commands, c)
		s.part.readInFull(a)
		s.reach(inAction)
		return nil
	})
	return a, err
}

func (w *writer) actionRequest(a ActionRequest) {
	var items []func()
	if a.ContextRequest != nil {
		items = w.contextRequest(a.ContextRequest)
	}
	for _, c := range a.Commands {
		items = append(items, func() { w.commandRequest(c) })
	}
	if len(items) == 0 {
		w.fail("the action for context %s has no command", a.ContextID)
	}
	w.block(fmt.Sprintf("%s = %s", tokContext, a.ContextID), items)
}

func (s *scanner) transactionReply() (Transaction, error) {
	var t TransactionReply
	var err error
	if t.TransactionID, err = s.transactionID(); err != nil {
		return nil, err
	}
	first := true
	err = s.list(func() error {
		name, err := s.token()
		if err != nil {
			return err
		}
		isFirst := first
		first = false
		switch name {
		case tokImmAckRequired:
			if !isFirst {
				return s.errorf("%s comes first in a reply", tokImmAckRequired)
			}
			t.ImmAckRequired = true
			return nil
		case tokError:
			if t.Error != nil {
				return s.errorf("a transaction reply carries one error descriptor")
			}
			t.Error, err = s.errorDescriptor()
			return err
		case tokContext:
			a, err := s.actionReply()
			t.Actions = append(t.Actions, a)
			return err
		}
		return s.errorf("%s cannot stand in a transaction reply", name)
	})
	if err != nil {
		return nil, err
	}
	if (t.Error != nil) == (len(t.Actions) > 0) {
		return nil, s.errorf("a transaction reply holds either one error descriptor or action replies")
	}
	return t, nil
}

func (t TransactionReply) write(w *writer) {
	if (t.Error != nil) == (len(t.Actions) > 0) {
		w.fail("transaction reply %d holds either one error descriptor or action replies", t.TransactionID)
		return
	}
	var items []func()
	if t.ImmAckRequired {
		items = append(items, func() { w.item(tokImmAckRequired) })
	}
	if t.Error != nil {
		items = append(items, func() { w.errorDescriptor(t.Error) })
	}
	for _, a := range t.Actions {
		items = append(items, func() { w.actionReply(a) })
	}
	w.block(fmt.Sprintf("%s = %d", tokReply, t.TransactionID), items)
}

func (s *scanner) actionReply() (ActionReply, error) {
	var a ActionReply
	var err error
	if err = s.expect('='); err != nil {
		return a, err
	}
	if a.ContextID, err = s.contextID(); err != nil {
		return a, err
	}
	err = s.list(func() error {
		if a.Error != nil {
			return s.errorf("the error descriptor comes last in an action reply")
		}
		name, err := s.token()
		if err != nil {
			return err
		}
		if name == tokError {
			a.Error, err = s.errorDescriptor()
			return err
		}
		c, err := s.commandReply(name)
		a.Commands = append(a.Commands, c)
		return err
	})
	return a, err
}

func (w *writer) actionReply(a ActionReply) {
	if a.Error == nil && len(a.Commands) == 0 {
		w.fail("the action reply for context %s is empty", a.ContextID)
	}
	var items []func()
	for _, c := range a.Commands {
		items = append(items, func() { w.commandReply(c) })
	}
	if a.Error != nil {
		items = append(items, func() { w.errorDescriptor(a.Error) })
	}
	w.block(fmt.Sprintf("%s = %s", tokContext, a.ContextID), items)
}

func (s *scanner) transactionPending() (Transaction, error) {
	var t TransactionPending
	var err error
	if t.TransactionID, err = s.transactionID(); err != nil {
		return nil, err
	}
	if err := s.expect('{'); err != nil {
		return nil, err
	}
	return t, s.expect('}')
}

func (t TransactionPending) write(w *writer) {
	w.item(fmt.Sprintf("%s = %d { }", tokPending, t.TransactionID))
}

func (s *scanner) transactionResponseAck() (Transaction, error) {
	var t TransactionResponseAck
	err := s.list(func() error {
		first, err := s.uint(32)
		if err != nil {
			return err
		}
		ack := TransactionAck{FirstAck: uint32(first)}
		if s.eat('-') {
			last, err := s.uint(32)
			if err != nil {
				return err
			}
			if last < first {
				return s.errorf("the range %d-%d of acknowledged transactions runs backwards", first, last)
			}
			ack.LastAck = uint32(last)
		}
		t.Acks = append(t.Acks, ack)
		return nil
	})
	return t, err
}

func (t TransactionResponseAck) write(w *writer) {
	if len(t.Acks) == 0 {
		w.fail("a transaction response ack confirms no transaction")
	}
	var acks []string
	for _, ack := range t.Acks {
		switch {
		case ack.LastAck == 0:
			acks = append(acks, strconv.FormatUint(uint64(ack.FirstAck), 10))
		case ack.LastAck >= ack.FirstAck:
			acks = append(acks, fmt.Sprintf("%d-%d", ack.FirstAck, ack.LastAck))
		default:
			w.fail("the range %d-%d of acknowledged transactions runs backwards", ack.FirstAck, ack.LastAck)
		}
	}
	w.item(fmt.Sprintf("%s { %s }", tokResponseAck, strings.Join(acks, ", ")))
}

// expectToken takes the token want, in either form.
func (s *scanner) expectToken(want string) error {
	name, err := s.token()
	if err != nil {
		return err
	}
	if name != want {
		return s.errorf("expected %s, found %s", want, name)
	}
	return nil
}
