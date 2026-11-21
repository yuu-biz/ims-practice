package h248

import (
	"net"
	"slices"
)

// unreadMessage is what could be read of a message that cannot be read
// in full.
type unreadMessage struct {
	err error
	// header tells that the version and the mId were read.
	header bool
	// next is the token that starts the transaction that could not be
	// read, or "" when no transaction token could be read there.
	next string
	// request is what was read of that transaction, when it is a
	// request whose TransactionID could be read.
	request *requestPart
}

// parseMessage reads a message as Parse does. For a message that cannot
// be read in full, it also returns what could be read: the header, the
// transactions before the one that is broken, and the part of that
// one.
func parseMessage(b []byte) (Message, *unreadMessage) {
	s := &scanner{b: b}
	m, err := s.message()
	if err == nil {
		return m, nil
	}
	return m, &unreadMessage{err: err, header: s.header, next: s.next, request: s.part}
}

// requestPlace is how far the scanner came in a transaction request.
type requestPlace int

const (
	// inTransaction: between the actions of the request, or before
	// the first.
	inTransaction requestPlace = iota
	// inContextID: after the Context token, before the ContextID was
	// read.
	inContextID
	// inAction: in an action, between its commands.
	inAction
	// inTerminationID: after the name of a command, before its
	// TerminationID was read.
	inTerminationID
	// inCommand: in a command, after its TerminationID.
	inCommand
)

// requestPart is what was read of a transaction request before a
// syntax error.
type requestPart struct {
	transactionID uint32
	// actions are the actions that were read in full.
	actions []ActionRequest
	// action is the action that was being read, with the context
	// properties and commands of it that were read in full. It is nil
	// between actions.
	action *ActionRequest
	at     requestPlace
}

// readInFull records a, the action being read, as far as it has been
// read in full.
func (p *requestPart) readInFull(a ActionRequest) {
	read := ActionRequest{ContextID: a.ContextID, Commands: slices.Clone(a.Commands)}
	if a.ContextRequest != nil {
		properties := *a.ContextRequest
		read.ContextRequest = &properties
	}
	p.action = &read
}

// unreadAnswer is the error with which the receiver answers the part of
// a request that it could not read (H.248.1 (05/2002) 8.2.2, which the
// 2013 edition words more precisely).
type unreadAnswer struct {
	// error is 403, 422 or 442 (H.248.8 (03/2013) 4.2.4, 4.2.11 and
	// 4.2.20).
	error *ErrorDescriptor
	// alone: the error is the whole reply, and nothing of the request
	// is carried out.
	alone bool
	// own: the error stands in an action reply of its own for
	// contextID, after the replies to the actions carried out.
	// Otherwise it ends the reply to the last action carried out.
	own       bool
	contextID ContextID
}

// answer returns the part of the request that is carried out, and how
// the rest is answered. 8.2.2 has the receiver carry out what it could
// make out, and answer, for the rest:
//
//   - 403 when the end of the transaction cannot be found, as the only
//     reply when no action could be read, and as the last action reply
//     otherwise;
//   - 422 when no action could be read because its ContextID could not
//     be, as the only reply; the 2002 edition puts it as an error in
//     processing a ContextID, the 2013 edition as a ContextID that
//     cannot be parsed;
//   - 422 when the end of an action cannot be found, after the replies
//     to its commands that could be read;
//   - 442 when the TerminationID or the end of a command cannot be
//     found, in the reply to the action of that command.
func (p *requestPart) answer() (TransactionRequest, unreadAnswer) {
	t := TransactionRequest{TransactionID: p.transactionID, Actions: slices.Clone(p.actions)}
	switch p.at {
	case inTransaction, inContextID:
		code := 403
		if p.at == inContextID && len(t.Actions) == 0 {
			code = 422
		}
		return t, unreadAnswer{error: syntaxError(code), alone: len(t.Actions) == 0}
	}
	code := 422
	if p.at == inTerminationID || p.at == inCommand {
		code = 442
	}
	a := unreadAnswer{error: syntaxError(code), own: true, contextID: p.action.ContextID}
	if len(p.action.Commands) > 0 || p.action.ContextRequest != nil {
		t.Actions = append(t.Actions, *p.action)
		a.own = false
	}
	return t, a
}

// complete adds the error for the unread part to reply, the reply to
// t. When the reply shows that processing stopped before the end of t,
// it is left as it is: nothing is answered for what comes after a
// failed command (8.2.2).
func (a unreadAnswer) complete(t TransactionRequest, reply TransactionReply) TransactionReply {
	switch {
	case a.alone:
		return TransactionReply{Error: a.error}
	case len(t.Actions) == 0:
		return TransactionReply{Actions: []ActionReply{{ContextID: a.contextID, Error: a.error}}}
	case !carriedOutInFull(t, reply):
		return reply
	case a.own:
		reply.Actions = append(reply.Actions, ActionReply{ContextID: a.contextID, Error: a.error})
	default:
		reply.Actions[len(reply.Actions)-1].Error = a.error
	}
	return reply
}

// carriedOutInFull reports whether reply shows that every action of t
// was carried out to its end: it answers every action, without an
// error for an action or the transaction, and its last command reply
// has no error unless the last command is optional.
func carriedOutInFull(t TransactionRequest, reply TransactionReply) bool {
	if reply.Error != nil || len(reply.Actions) != len(t.Actions) {
		return false
	}
	last := reply.Actions[len(reply.Actions)-1]
	if last.Error != nil {
		return false
	}
	commands := t.Actions[len(t.Actions)-1].Commands
	if n := len(last.Commands); n > 0 && last.Commands[n-1].Error != nil {
		return len(commands) > 0 && commands[len(commands)-1].Optional
	}
	return true
}

// syntaxErrorNames are the names that H.248.8 (03/2013) gives the codes
// for syntax errors and for an unsupported version (4.2.1, 4.2.4,
// 4.2.5, 4.2.11, 4.2.20).
var syntaxErrorNames = map[int]string{
	400: "Syntax error in message",
	403: "Syntax error in TransactionRequest",
	406: "Version not supported",
	422: "Syntax error in action",
	442: "Syntax error in command",
}

func syntaxError(code int) *ErrorDescriptor {
	return &ErrorDescriptor{ErrorCode: code, ErrorText: syntaxErrorNames[code]}
}

// receiveUnread answers the transaction that could not be read in a
// message, after the transactions before it have been handled.
func (e *Endpoint) receiveUnread(m Message, addr net.Addr, u *unreadMessage) {
	switch {
	case u.next == tokTransaction && u.request != nil:
		t, answer := u.request.answer()
		e.receiveRequest(m, addr, t, &answer)
	case u.next == "" && len(m.Transactions) == 0:
		// H.248.8 (03/2013) 4.2.1: no transaction can be read.
		e.answerMessage(addr, 400)
	case u.next == tokTransaction || u.next == "":
		// H.248.1 (05/2002) 8.2.2: no legal transaction can be found,
		// so the reply has the null TransactionID, which is 0 (8.1.1).
		if b, err := e.marshal(e.version, TransactionReply{Error: syntaxError(403)}); err == nil {
			e.write(b, addr)
		}
	default:
		// A reply, a Pending or a TransactionResponseAck. 8.2.2 answers
		// requests only, and an error reply would read as the reply to
		// a request of the peer with that ID.
	}
}

// answerMessage answers a message with an error descriptor in place of
// its transactions, which tells the sender that nothing in the message
// was carried out.
func (e *Endpoint) answerMessage(addr net.Addr, code int) {
	answer := Message{Version: e.version, MId: e.mid, MessageError: syntaxError(code)}
	if b, err := answer.Marshal(); err == nil {
		e.write(b, addr)
	}
}
