package h248

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultPort is the UDP port for the text encoding (H.248.1 (05/2002)
// clause 9, Annex D.1). A media gateway sends its first ServiceChange
// there when it knows no other port of its controller.
const DefaultPort = 2944

// Endpoint carries H.248 transactions over UDP for a media gateway or
// for its controller, with the application level framing of H.248.1
// (05/2002) Annex D.1, which the 2013 edition leaves as it was. Both
// sides send requests: the controller sends Add, Modify and Subtract,
// the media gateway sends Notify and ServiceChange. So one Endpoint is
// both the requester of D.1.3 and D.1.5, which repeats a request until
// an answer comes, and the responder of D.1.1, which carries out each
// request at most once and answers repetitions from the reply it keeps.
//
// The state of a transaction lives in the Endpoint only for the
// lifetime of the transaction and LONG-TIMER after it, keyed by the
// transaction ID. There is no lock for the whole Endpoint: two
// transactions never wait for each other, and no lock is held while a
// datagram is written or a Handler runs.
//
// SIMPLIFIED: TS 29.334 5.12 (Table 5.12.1), TS 29.238 5.12 and TS
// 29.333 5.12. Only UDP is offered, not SCTP, which the first two
// recommend and the third requires, with UDP as an option in all three.
// So the parts of H.248.4 (11/2000) that take the place of parts of
// Annex D.1 over SCTP are left out too: payload protocol identifier 7
// (clause 1), no TransactionResponseAck (clause 3) and no three-way
// handshake (clause 4.2). The Go standard library has no SCTP.
type Endpoint struct {
	conn    net.PacketConn
	mid     MId
	version int
	handler Handler
	timers  Timers
	maxSize int
	limit   int64
	clock   Clock
	log     *slog.Logger

	// ctx is cancelled by Close. The context of every Handler call
	// is derived from it.
	ctx    context.Context
	cancel context.CancelFunc

	// nextTransactionID is the last transaction ID handed out.
	nextTransactionID atomic.Uint32
	// outgoing holds the requests this Endpoint sent and that wait
	// for their reply, keyed by transaction ID.
	outgoing      sync.Map
	outgoingCount atomic.Int64
	// incoming holds the requests the peers sent, keyed by
	// incomingKey, from their arrival until LONG-TIMER after the
	// reply.
	incoming      sync.Map
	incomingCount atomic.Int64
}

// EndpointConfig sets up an Endpoint.
type EndpointConfig struct {
	// MId names this media gateway or controller in the header of
	// every message it sends. The profiles recommend a domain name
	// (TS 29.334 5.11).
	MId MId
	// Version is the protocol version in the header of the messages.
	// Zero means 2, the lowest version that the profiles allow (TS
	// 29.334 5.3, TS 29.238 5.3, TS 29.333 5.3). The messages of a
	// transaction whose ServiceChange carries a version are version 1
	// messages instead (see registrationVersion).
	Version int
	// Handler carries out the requests that peers send. Without one,
	// every request is answered with error 501 "Not Implemented".
	Handler Handler
	// Timers holds the timer values. Zero fields take the defaults.
	Timers Timers
	// MaxMessageSize is the largest message, in octets, that the
	// Endpoint sends. Annex D.1 warns that the MTU limits the size of
	// a message, and the profiles do not segment messages over UDP
	// (TS 29.334 5.12, Table 5.12.2; TS 29.238 5.12, Table 5.12.2; TS
	// 29.333 5.10, Table 5.10.2). Zero means 1452: a message of that
	// size and its IPv6 and UDP headers fit in an Ethernet frame of
	// 1500 octets, so no message depends on IP fragmentation.
	MaxMessageSize int
	// MaxTransactions bounds the number of transactions that the
	// Endpoint keeps at once, separately for the requests it sends
	// and for those it receives. Zero means 10000.
	MaxTransactions int
	// FirstTransactionID is the ID of the first request the Endpoint
	// sends. Zero picks one at random, so that after a restart the
	// IDs do not repeat those that a peer may still remember (H.248.1
	// Annex D.1.2.2). The tests set it to get IDs they can
	// predict.
	FirstTransactionID uint32
	// Clock defaults to SystemClock.
	Clock Clock
	// Logger receives a line for every message the Endpoint drops.
	// Nil discards them.
	Logger *slog.Logger
}

// Timers holds the timer values of H.248.1 (05/2002) Annex D.1. The
// 3GPP profiles leave the transaction timers to provisioning (TS 29.334
// 5.10, Table 5.10.7; TS 29.238 5.10, Table 5.10.7; TS 29.333 5.10,
// Table 5.10.6), and so does H.248.1: Annex D.1.3 purposely gives no
// value for the retransmission timers, and the 2013 edition gives
// "Provisioned" as the default of every property of the base root
// package (E.2.1.3 to E.2.1.8). So each value is a field here. A zero
// field takes the default given with it, which is an example or a
// suggestion of Annex D.1 where there is one, and a choice of this
// package where there is none.
//
// SIMPLIFIED: four of the values are also properties of the ROOT
// termination, which a controller may set with Modify (H.248.1
// (05/2002) Annex E.2.1). The Endpoint keeps the values it was made
// with; whoever handles such a Modify would have to make a new
// Endpoint.
type Timers struct {
	// InitialRetransmissionTimer is how long a request waits for an
	// answer before it is repeated the first time. Annex D.1.5 works
	// its example with 200 ms, which is the default.
	InitialRetransmissionTimer time.Duration
	// MaxRetransmissionTimer bounds the retransmission timer as it
	// doubles. Annex D.1.3 suggests 4 s, which is the default.
	MaxRetransmissionTimer time.Duration
	// MaxRetransmissions is the "disconnection threshold" of Annex
	// D.1.5: after this many repetitions without an answer, the peer
	// is taken to have failed. The annex puts the usual value
	// between 7 and 11. The default is 10.
	MaxRetransmissions int
	// TMax bounds the time from the first transmission of a request
	// to its last repetition (T-MAX, Annex D.1.5). Annex D.1.5 gives
	// no value; it calls a total wait of 30 s probably acceptable and
	// makes LONG-TIMER T-MAX plus the largest propagation delay. The
	// default of 25 s leaves 5 s of propagation delay within the
	// default LONG-TIMER.
	TMax time.Duration
	// LongTimer is how long a reply is kept to answer repetitions of
	// its request (LONG-TIMER, Annex D.1.1). It has to outlast every
	// repetition the peer may send, so it must be longer than TMax.
	// Annex D.1.1 suggests 30 s, which is the default.
	LongTimer time.Duration
	// ProvisionalResponseTimerValue is the longest time that this
	// Endpoint lets pass between the arrival of a request and a
	// TransactionPending for it while the Handler runs (Annex D.1.4).
	// The Pending is then repeated at the same interval, since clause
	// 8 has Pending notify the requester periodically. For a media
	// gateway it is the ROOT property MGProvisionalResponseTimerValue,
	// for a controller MGCProvisionalResponseTimerValue (Annex E.2.1).
	// H.248.1 gives no value: E.2.1 starts it at the normal execution
	// time plus the network delay, and lets it be lowered. The default
	// of 100 ms is half the default InitialRetransmissionTimer, so that
	// a request that takes long gets a Pending before the peer repeats
	// it.
	ProvisionalResponseTimerValue time.Duration
	// PeerProvisionalResponseTimerValue is the same value for the
	// peer. After a Pending, a request is repeated only when this time
	// plus InitialRetransmissionTimer, an allowance for the network,
	// has passed without another Pending or the reply (Annex D.1.4
	// asks for a different repetition timer). The default is 100 ms.
	PeerProvisionalResponseTimerValue time.Duration
	// PendingLimit is how many Pendings this Endpoint sends for one
	// request. When one more would be due, it stops the Handler, which
	// replies with error 506 "Number of TransactionPendings Exceeded".
	// For a media gateway it is the ROOT property
	// MGOriginatedPendingLimit, for a controller
	// MGCOriginatedPendingLimit. They came with version 2 of H.248.1
	// (H.248.1 (05/2002) 8.2.3 and E.2.1). No value is given. The
	// default is 20, which lets a Handler run for about 2 s with the
	// default ProvisionalResponseTimerValue.
	PendingLimit int
	// PeerPendingLimit is the same limit for the peer. A request that
	// gets more Pendings than this ends with ErrPendingLimitExceeded,
	// since the peer should have replied with error 506 by then. The
	// default is 20.
	PeerPendingLimit int
}

func (t Timers) withDefaults() Timers {
	if t.InitialRetransmissionTimer == 0 {
		t.InitialRetransmissionTimer = 200 * time.Millisecond
	}
	if t.MaxRetransmissionTimer == 0 {
		t.MaxRetransmissionTimer = 4 * time.Second
	}
	if t.MaxRetransmissions == 0 {
		t.MaxRetransmissions = 10
	}
	if t.TMax == 0 {
		t.TMax = 25 * time.Second
	}
	if t.LongTimer == 0 {
		t.LongTimer = 30 * time.Second
	}
	if t.ProvisionalResponseTimerValue == 0 {
		t.ProvisionalResponseTimerValue = 100 * time.Millisecond
	}
	if t.PeerProvisionalResponseTimerValue == 0 {
		t.PeerProvisionalResponseTimerValue = 100 * time.Millisecond
	}
	if t.PendingLimit == 0 {
		t.PendingLimit = 20
	}
	if t.PeerPendingLimit == 0 {
		t.PeerPendingLimit = 20
	}
	return t
}

// Handler carries out one TransactionRequest and returns its reply.
// The Endpoint fills in the TransactionID of the reply, and sets
// ImmAckRequired when it has sent a Pending (H.248.1 Annex D.1.4).
//
// The Endpoint calls the Handler in a goroutine of its own for each new
// transaction, and never twice for the same one (Annex D.1.1). While it
// runs, the Endpoint sends a TransactionPending every
// ProvisionalResponseTimerValue; a Handler that knows it will take long
// can send one at once with IncomingRequest.Pending.
//
// When PendingLimit Pendings have gone out, ctx is cancelled with the
// cause ErrPendingLimitReached (context.Cause). The Handler then stops
// and returns the replies so far, with error 506 for the command it
// was carrying out and nothing after it (H.248.1 (05/2002) 8.2.3), and
// the Endpoint sends that reply. ctx is also cancelled when the
// Endpoint is closed; the reply is then thrown away.
//
// A Handler carries out the commands one after the other and stops at
// the first that fails, unless that one is optional (H.248.1 (05/2002)
// clause 8). After each command it asks IncomingRequest.Fits whether
// the reply still fits in one message.
type Handler func(ctx context.Context, r *IncomingRequest) TransactionReply

// IncomingRequest is a TransactionRequest as a peer sent it.
type IncomingRequest struct {
	// From and Version come from the header of the message that
	// carried the request.
	From    MId
	Version int
	// Addr is the address the request came from.
	Addr net.Addr
	TransactionRequest

	endpoint    *Endpoint
	transaction *incomingTransaction
}

// Pending sends a TransactionPending for the request now, to tell the
// peer that the request takes long and is not to be repeated (H.248.1
// 8.2.3, Annex D.1.4).
func (r *IncomingRequest) Pending() {
	r.endpoint.sendPending(r.transaction)
}

// Fits reports whether reply, as the reply to this request so far,
// leaves room in one message for one more action reply that holds
// error 533 for one command. A Handler asks it after each command
// reply it adds. When a reply does not fit, the Handler puts error 533
// in its place and carries out nothing more, even after an optional
// command: H.248.8 (03/2013) 4.2.66 treats that command instance as
// failed and leaves the rest of the transaction undone. Asking with
// the room for the error kept free is what the implementers' note of
// that clause asks for.
//
// The room is that of the longest error reply a command can have, in
// an action of its own, so a reply may end with error 533 a little
// before the message is full.
func (r *IncomingRequest) Fits(reply TransactionReply) bool {
	probe := TransactionReply{
		TransactionID:  r.TransactionID,
		ImmAckRequired: true,
		Actions: append(slices.Clip(reply.Actions), ActionReply{
			ContextID: ContextChoose - 1,
			Commands: []CommandReply{{
				Command:       AuditCapability,
				TerminationID: TerminationID(strings.Repeat("x", 64)),
				Error:         &ErrorDescriptor{ErrorCode: 533},
			}},
		}),
	}
	message, err := r.endpoint.marshal(r.transaction.version, probe)
	// A reply that cannot be written is answered with error 500 by
	// reply; it is not for Fits to stop the Handler.
	return err != nil || len(message) <= r.endpoint.maxSize
}

var (
	// ErrNoReply means that a request was repeated as often and for
	// as long as the timers allow, and nothing came back. Annex
	// D.1.5 says the requester should then take the peer to have
	// failed: a media gateway starts its recovery and registers again
	// with ServiceChange (H.248.1 11.5).
	ErrNoReply = errors.New("h248: no answer to the request")
	// ErrTooManyTransactions means that the Endpoint already holds
	// MaxTransactions requests that wait for their reply.
	ErrTooManyTransactions = errors.New("h248: too many transactions at once")
	// ErrPendingLimitExceeded means that the peer sent more than
	// PeerPendingLimit Pendings for a request, which the requester
	// may take as a failed transaction (H.248.1 (05/2002) 8.2.3, Annex
	// E.2.1).
	ErrPendingLimitExceeded = errors.New("h248: the peer sent too many TransactionPendings")
	// ErrPendingLimitReached is the cause with which the context of a
	// Handler ends when the Endpoint has sent PendingLimit Pendings for
	// the request. See Handler.
	ErrPendingLimitReached = errors.New("h248: PendingLimit TransactionPendings have been sent")
	// ErrMessageTooLong means that the request does not fit in
	// MaxMessageSize octets. It was not sent.
	ErrMessageTooLong = errors.New("h248: the message is longer than MaxMessageSize")
)

// NewEndpoint makes an Endpoint that sends and receives on conn. Call
// Serve to start reading.
func NewEndpoint(conn net.PacketConn, config EndpointConfig) (*Endpoint, error) {
	e := &Endpoint{
		conn:    conn,
		mid:     config.MId,
		version: config.Version,
		handler: config.Handler,
		timers:  config.Timers.withDefaults(),
		maxSize: config.MaxMessageSize,
		limit:   int64(config.MaxTransactions),
		clock:   config.Clock,
		log:     config.Logger,
	}
	if e.version == 0 {
		e.version = 2
	}
	if e.maxSize == 0 {
		e.maxSize = 1452
	}
	if e.limit == 0 {
		e.limit = 10000
	}
	if e.clock == nil {
		e.clock = SystemClock{}
	}
	if e.log == nil {
		e.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if e.timers.LongTimer <= e.timers.TMax {
		return nil, fmt.Errorf("h248: LONG-TIMER %v must be longer than T-MAX %v", e.timers.LongTimer, e.timers.TMax)
	}
	// Writing a message checks the header once, so that a wrong MId
	// shows here and not with the first request.
	if _, err := e.marshal(e.version, TransactionPending{}); err != nil {
		return nil, err
	}
	first := config.FirstTransactionID
	for first == 0 {
		first = rand.Uint32()
	}
	e.nextTransactionID.Store(first - 1)
	e.ctx, e.cancel = context.WithCancel(context.Background())
	return e, nil
}

// Serve reads messages until the connection is closed. After Close it
// returns nil.
func (e *Endpoint) Serve() error {
	// A UDP datagram holds at most 65535 octets.
	buf := make([]byte, 65535)
	for {
		n, addr, err := e.conn.ReadFrom(buf)
		if err != nil {
			if e.ctx.Err() != nil {
				return nil
			}
			return err
		}
		e.receive(buf[:n], addr)
	}
}

// Close closes the connection. Requests that wait for their reply
// return net.ErrClosed.
func (e *Endpoint) Close() error {
	e.cancel()
	return e.conn.Close()
}

// receive handles one datagram. The transactions in a message are
// independent of each other (H.248.1 (05/2002) 8.3).
func (e *Endpoint) receive(b []byte, addr net.Addr) {
	m, unread := parseMessage(b)
	if unread != nil {
		e.log.Warn("h248: a message cannot be read in full", "from", addr, "error", unread.err)
	}
	switch {
	case unread != nil && !unread.header:
		// H.248.8 (03/2013) 4.2.1: the message does not follow the
		// grammar of a message.
		e.answerMessage(addr, 400)
		return
	case m.MessageError != nil:
		e.log.Warn("h248: the peer could not read a message", "from", addr, "mid", m.MId,
			"code", m.MessageError.ErrorCode, "text", m.MessageError.ErrorText)
		return
	case unread != nil && unread.next == tokError:
		// An error is never answered, so that two peers that cannot
		// read each other do not answer each other's errors for ever.
		return
	case m.Version < 1 || m.Version > e.version:
		// H.248.8 (03/2013) 4.2.5: the contents of a message whose
		// header names a version the receiver does not support are
		// disregarded.
		e.log.Warn("h248: refused a message of a version that is not supported", "from", addr, "mid", m.MId, "version", m.Version)
		e.answerMessage(addr, 406)
		return
	}
	n := countedTransactions(m)
	if unread != nil && unread.next == tokTransaction {
		n++
	}
	if n > maxTransactionsPerMessage {
		// H.248.8 (03/2013) 4.2.9: error 413 at the message level
		// says that nothing in the message was carried out. The
		// sender is to send the transactions again in smaller
		// messages.
		e.log.Warn("h248: refused a message with too many transactions", "from", addr, "mid", m.MId, "transactions", n)
		refusal := Message{Version: e.version, MId: e.mid, MessageError: &ErrorDescriptor{
			ErrorCode: 413, ErrorText: "Number of transactions in message exceeds maximum",
		}}
		if b, err := refusal.Marshal(); err == nil {
			e.write(b, addr)
		}
		return
	}
	// Corrigendum 1 to H.248.1 version 2 asks for the replies of a
	// message to be handled before its requests (clause 8).
	for _, t := range m.Transactions {
		if t, ok := t.(TransactionReply); ok {
			e.receiveReply(addr, t)
		}
	}
	for _, t := range m.Transactions {
		switch t := t.(type) {
		case TransactionRequest:
			e.receiveRequest(m, addr, t, nil)
		case TransactionPending:
			e.receivePending(t)
		case TransactionResponseAck:
			e.receiveResponseAck(m.MId, t)
		}
	}
	if unread != nil {
		e.receiveUnread(m, addr, unread)
	}
}

// maxTransactionsPerMessage is the most transaction requests, replies
// and response acknowledgements that a message may hold (TS 29.334
// 5.10, TS 29.238 5.10 and TS 29.333 5.10, Table 5.10.1 each).
// Pendings are not counted.
const maxTransactionsPerMessage = 10

func countedTransactions(m Message) int {
	n := 0
	for _, t := range m.Transactions {
		if _, ok := t.(TransactionPending); !ok {
			n++
		}
	}
	return n
}

// marshal writes one transaction as a message of its own, with version
// in the header. The profiles allow up to 10 transactions in a message,
// but expect one to be the usual case (TS 29.334 5.10, Table 5.10.1,
// and its note).
func (e *Endpoint) marshal(version int, t Transaction) ([]byte, error) {
	m := Message{Version: version, MId: e.mid, Transactions: []Transaction{t}}
	return m.Marshal()
}

// registrationVersion is the version in the header of the messages of
// a transaction that negotiates the version. H.248.1 (05/2002) 11.3,
// as Corrigendum 1 (03/2004) words it, has the ServiceChange with
// which a media gateway registers carry the version it supports, and
// the message with that command encoded as a version 1 message,
// whatever the version it offers. The 2013 edition says the same of
// the reply. H.Sup7 (05/2008) clause 8 shows every message with a
// ServiceChangeVersion, and the reply to it, as version 1, and the
// other ServiceChanges as version 2.
const registrationVersion = 1

// transactionVersion returns the version for the messages of a
// transaction whose request holds actions.
func (e *Endpoint) transactionVersion(actions []ActionRequest) int {
	for _, a := range actions {
		for _, c := range a.Commands {
			if c.Command == ServiceChange && c.ServiceChange != nil && c.ServiceChange.Version != 0 {
				return registrationVersion
			}
		}
	}
	return e.version
}

func (e *Endpoint) write(b []byte, addr net.Addr) {
	if _, err := e.conn.WriteTo(b, addr); err != nil {
		e.log.Warn("h248: could not send a message", "to", addr, "error", err)
	}
}
