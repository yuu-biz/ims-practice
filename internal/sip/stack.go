package sip

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config is what a Stack needs besides its transport.
type Config struct {
	// SentBy is the host[:port] this node writes in the Via of every
	// request it sends (RFC 3261 18.1.1). A response whose top Via names
	// another sent-by is discarded (RFC 3261 18.1.2).
	SentBy string
	// Timers left at the zero value mean DefaultTimers.
	Timers Timers
	// OnRequest is called, in a goroutine of its own unless Ordered is
	// set, for each request that starts a new server transaction. It
	// must eventually call Respond.
	OnRequest func(*ServerTransaction)
	// OnACK is called, in a goroutine of its own, for each ACK that no
	// server transaction absorbs: in practice the ACK for a 2xx, which
	// has no transaction (RFC 3261 13.2.2.4, 17.1). A UAS stops
	// retransmitting its 2xx when it arrives; a proxy forwards it with
	// SendACK. When OnACK is nil, such ACKs are discarded.
	OnACK func(ack *Message)
	// OnCANCEL, when set, makes the node a proxy for a CANCEL that
	// matches no server transaction: the stack calls it, as it calls
	// OnACK, instead of answering 481 as a UAS does (RFC 3261 9.2). The
	// proxy has no knowledge of the request to cancel, so it forwards the
	// CANCEL statelessly with SendCANCEL (16.10). Such a CANCEL gets no
	// transaction and no 100 (Trying) (16.11), so a retransmission of it
	// comes here again. A UA leaves OnCANCEL nil.
	OnCANCEL func(cancel *Message)
	// Ordered has the stack call OnRequest and OnACK one after the
	// other, in the order the requests arrived, instead of each in a
	// goroutine of its own; with Admit, that order holds for the requests
	// of each Call-ID from a source. The callbacks then must return at once, and
	// start a goroutine themselves for whatever takes time. A UA sets
	// it: the requests within a dialog count on their order (RFC 3261
	// 12.2.2), and a peer may send two of them back to back.
	Ordered bool
	// TimerC bounds how long ServerTransaction.Proxy waits for the final
	// response to an INVITE (RFC 3261 16.6 step 11, 16.8). Zero means
	// DefaultTimerC.
	TimerC time.Duration
	// Screen, when set, is asked about each request that matches no
	// server transaction, CANCEL and ACK included, before the stack does
	// anything with it. When it returns false the stack drops the
	// request without a word: no transaction, no 100 (Trying), no
	// answer to a CANCEL, no call of OnRequest or OnACK. A border node
	// treats a request from a source that is no peer of it this way, so
	// that the source learns nothing about the node. A retransmission of
	// a dropped request is asked about again, since nothing of the first
	// copy is kept.
	//
	// A request that matches a server transaction is not asked about:
	// RFC 3261 17.2.3 makes it part of a transaction that Screen already
	// let in, namely a retransmission, or the ACK for a 300-699 response
	// to an INVITE. A CANCEL is a transaction of its own and never
	// matches the INVITE it cancels, so Screen is asked about it even
	// when it let that INVITE in.
	//
	// Over TCP the stack drops only the message and leaves the
	// connection, which closes once it has been idle long enough.
	//
	// The transport has already recorded the request for the Observer,
	// and written received and rport into its top Via. Screen is called
	// in the stack's own goroutine: it must return at once, must not
	// change req, and must not call the stack.
	Screen func(req *Message, source Destination) bool
	// Admit, when set, is asked about each request that Screen lets in,
	// before the stack does anything with it, as Screen is. It is asked
	// in a goroutine of its own, so it may wait, for instance for a
	// store on another machine, while the stack goes on with the
	// messages from other sources. When it returns true, the stack takes
	// the request up as if it had just arrived; when it returns false,
	// the stack drops the request as one that Screen refuses, but logs
	// nothing of it, since Admit knows why it refused and logs that.
	// Each copy of a request is asked about until one is let in, and a
	// copy let in after another has started the transaction counts as
	// its retransmission.
	//
	// Admit is asked about the requests with the same Call-ID from one
	// source, the same transport and address, one at a time and in the
	// order they arrived, and the stack takes them up in that order. A
	// CANCEL has the Call-ID of the INVITE it cancels (RFC 3261 9.1), so
	// it finds that INVITE (9.2), and the requests of a dialog have its
	// Call-ID (12), so with Ordered the TU sees them in the order they
	// arrived, the ACK for a 2xx before a BYE that follows it. While
	// Admit waits over one request, the later ones with its Call-ID from
	// its source wait too, so it should answer soon. Those with other
	// Call-IDs do not wait for it, so that the requests of many users
	// that a node of the network sends from one address do not queue up
	// behind one another. Admit may thus be called for several requests
	// at once, from one source or from several. At most maxAdmitQueue
	// requests from a source wait, whatever their Call-IDs; the stack
	// drops any more, sends nothing back and logs it. Admit must not
	// change req.
	Admit func(req *Message, source Destination) bool
}

// DefaultTimerC is Timer C unless Config says otherwise. RFC 3261 16.6
// step 11 only asks for more than 3 minutes.
const DefaultTimerC = 3*time.Minute + 30*time.Second

// ErrStackClosed is reported to client transactions still running when
// the stack closes, and by calls made after Close.
var ErrStackClosed = errors.New("sip: stack closed")

// Stack is the transaction layer of RFC 3261 17 on top of a
// TransportLayer. One goroutine owns every transaction: it reads the
// transport, matches messages to transactions (17.1.3, 17.2.3), runs
// the state machines of transaction.go, and fires their timers.
//
// What the stack does with messages that match no transaction:
//   - A response to a CANCEL that SendCANCEL forwarded statelessly goes
//     on to the Via below this node's (RFC 3261 16.11). Any other
//     response is logged and discarded. RFC 6026 8.2 and 8.9 ask this of
//     every element but a stateless proxy, and a proxy built on this
//     stack keeps transaction state for every other request. A
//     retransmitted 2xx still reaches the TU while its INVITE client
//     transaction waits in Accepted.
//   - A request that Config.Screen or Config.Admit refuses is dropped.
//   - An ACK goes to Config.OnACK.
//   - A CANCEL goes to Config.OnCANCEL at a proxy, and gets 481 at a UA.
//   - Any other request starts a server transaction and goes to
//     Config.OnRequest.
//
// The stack answers a CANCEL that matches a server transaction of any
// method but CANCEL itself, with 200 (RFC 3261 9.2, 16.10). The TU of
// an INVITE that has no final response yet then sees Cancelled; the
// CANCEL changes nothing else.
type Stack struct {
	transport *TransportLayer
	cfg       Config
	sentBy    sentBy
	log       *slog.Logger

	inbox     chan stackInput
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup

	// toTU hands the calls of OnRequest and OnACK to the goroutine that
	// makes them in order (Config.Ordered).
	toTU chan func()

	// Only the loop goroutine touches the fields below.
	clients map[clientKey]*clientEntry
	servers map[serverKey]*serverEntry
	// cancellable are the server transactions in servers but those of
	// CANCEL, by what a CANCEL matches them on.
	cancellable map[cancelKey]*serverEntry
	timerSeq    uint64
	// admitting holds, for each source with a request in Config.Admit,
	// the requests from it that wait for Admit.
	admitting map[Destination]*admitQueue
	// forTU are the calls that wait for toTU. The loop never waits for
	// the TU, so they queue up here.
	forTU []func()
}

// sentBy is a Via sent-by in the form used to compare two of them: the
// host in lower case and the port with its default filled in.
type sentBy struct {
	host string
	port int
}

func sentByOf(v Via) sentBy {
	port := v.Port
	if port == 0 {
		port = defaultPort
	}
	return sentBy{host: strings.ToLower(v.Host), port: port}
}

// clientKey matches a response to a client transaction: the branch of
// the top Via and the CSeq method (RFC 3261 17.1.3).
type clientKey struct {
	branch string
	method Method
}

// serverKey matches a request to a server transaction: the branch and
// sent-by of the top Via and the method, where an ACK counts as the
// INVITE it acknowledges (RFC 3261 17.2.3).
type serverKey struct {
	branch string
	sentBy sentBy
	method Method
}

// cancelKey matches a CANCEL to the server transaction it cancels: the
// branch and sent-by of the top Via, whatever the method of the
// transaction but CANCEL (RFC 3261 9.2, 17.2.3).
type cancelKey struct {
	branch string
	sentBy sentBy
}

func (k serverKey) cancelKey() cancelKey { return cancelKey{branch: k.branch, sentBy: k.sentBy} }

// NewStack starts the transaction layer. The stack takes over the
// transport and closes it in Close.
func NewStack(transport *TransportLayer, cfg Config, log *slog.Logger) (*Stack, error) {
	if cfg.OnRequest == nil {
		return nil, errors.New("sip: Config.OnRequest is required")
	}
	host, port, err := splitHostPort(cfg.SentBy)
	if err != nil {
		return nil, fmt.Errorf("sip: Config.SentBy %q: %w", cfg.SentBy, err)
	}
	if cfg.Timers == (Timers{}) {
		cfg.Timers = DefaultTimers()
	}
	if cfg.Timers.T1 <= 0 || cfg.Timers.T2 < cfg.Timers.T1 || cfg.Timers.T4 <= 0 || cfg.Timers.TransactionTimeout < 0 {
		return nil, fmt.Errorf("sip: Config.Timers %+v: need 0 < T1 <= T2, T4 > 0 and TransactionTimeout >= 0", cfg.Timers)
	}
	if cfg.TimerC == 0 {
		cfg.TimerC = DefaultTimerC
	}
	s := &Stack{
		transport:   transport,
		cfg:         cfg,
		sentBy:      sentByOf(Via{Host: host, Port: port}),
		log:         log,
		inbox:       make(chan stackInput),
		toTU:        make(chan func()),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		clients:     map[clientKey]*clientEntry{},
		servers:     map[serverKey]*serverEntry{},
		cancellable: map[cancelKey]*serverEntry{},
		admitting:   map[Destination]*admitQueue{},
	}
	s.cfg.SentBy = joinHostPort(host, port)
	s.wg.Add(2)
	go s.read()
	go s.run()
	if cfg.Ordered {
		s.wg.Add(1)
		go s.callTU()
	}
	return s, nil
}

// LocalAddr is the address the stack's transport is bound to.
func (s *Stack) LocalAddr() netip.AddrPort { return s.transport.LocalAddr() }

// Close stops the stack and its transport. Client transactions still
// running end with ErrStackClosed. Close returns once the calls of
// Config.Admit under way have returned, so that the node may then close
// what Admit reads, and drops the requests that wait for Admit.
func (s *Stack) Close() {
	s.closeOnce.Do(func() {
		// The loop ends first, so that no transaction sees the closed
		// socket as a transport error. Closing the socket then ends the
		// reader.
		close(s.stop)
		<-s.done
		s.transport.Close()
	})
	s.wg.Wait()
}

// ClientTransaction is a request that the TU sends, as the TU sees it:
// the client transaction that takes it to its target, and, when the
// request fails there, the ones that take it on to the next targets
// (nexttarget.go). Responses delivers every provisional response and
// then the final one. It is closed after the final response, except
// after a 2xx to an INVITE: then it stays open until Timer M, 64*T1
// later, and delivers every further 2xx, which is either a
// retransmission that the TU acknowledges again or the answer of
// another branch of a fork (RFC 6026 7.2). It is also closed after a
// failure.
type ClientTransaction struct {
	// Request is the request as sent to the first target, with this
	// node's Via on top. The stack retransmits it, so it must not be
	// changed.
	Request *Message

	stack     *Stack
	responses chan *Message
	err       error

	mu sync.Mutex
	// attempt is the client transaction of the target that the request
	// went to last.
	attempt *attempt
	// cancelled is set by Cancel: the request goes to no other target.
	cancelled bool
}

// attempt is one client transaction of a ClientTransaction: the request
// with a Via of its own, sent to one target. RFC 3261 16.6 step 7 calls
// each target tried an attempt, and gives each a new client
// transaction.
type attempt struct {
	request     *Message
	destination Destination
	// responses and err are what the client transaction reports. err is
	// set before responses is closed.
	responses chan *Message
	err       error
}

func newAttempt(req *Message, destination Destination) *attempt {
	return &attempt{request: req, destination: destination, responses: make(chan *Message, responseBuffer)}
}

func (ct *ClientTransaction) Responses() <-chan *Message { return ct.responses }

// Cancel asks for the INVITE to be cancelled (RFC 3261 9.1). The stack
// sends the CANCEL, in a client transaction of its own, as soon as a
// provisional response has arrived, and not at all once a final
// response has. Either way the outcome is the INVITE's final response
// on Responses, normally 487 (Request Terminated). A cancelled INVITE
// goes to no further target.
//
// headers go into the CANCEL: a Reason that tells why (RFC 3326 2),
// which a proxy also copies from the CANCEL it received.
func (ct *ClientTransaction) Cancel(headers ...HeaderField) error {
	if ct.Request.Method != INVITE {
		// 9.1: a non-INVITE request is answered at once, so a CANCEL
		// would always race with the answer.
		return fmt.Errorf("sip: cannot cancel %s", ct.Request.Method)
	}
	ct.mu.Lock()
	ct.cancelled = true
	// RFC 3263 4: the CANCEL goes where the INVITE went.
	a := ct.attempt
	ct.mu.Unlock()
	return ct.stack.post(cancelInvite{a: a, headers: headers})
}

// Err is nil if a final response arrived. Otherwise it is ErrTimeout,
// ErrStackClosed, or the transport error. It is set once Responses is
// closed.
func (ct *ClientTransaction) Err() error { return ct.err }

// responseBuffer bounds the responses waiting for the TU. The stack
// never blocks on a slow TU: it keeps the last slot for the final
// response and drops other responses that do not fit.
const responseBuffer = 8

// queue puts resp on ch, unless ch is so full that resp does not fit
// (responseBuffer). Only one goroutine sends on ch.
func queue(ch chan<- *Message, resp *Message) bool {
	room := cap(ch) - len(ch)
	if room == 0 || (resp.StatusCode < 200 && room == 1) {
		return false
	}
	ch <- resp
	return true
}

// SendRequest starts a client transaction, INVITE or non-INVITE, that
// sends req to the first of targets: the transports and addresses that
// RFC 3263 gives for its next hop, best first. The stack puts its own
// Via, with a new branch, on top of a copy of req. When the request
// fails at a target, it goes on to the next one (follow).
//
// ACK and CANCEL have no client transaction of their own here: an ACK
// for a 2xx goes out with SendACK, the transaction sends the ACK for
// any other final response, and ClientTransaction.Cancel sends CANCEL.
func (s *Stack) SendRequest(req *Message, targets ...Destination) (*ClientTransaction, error) {
	if req.Method == ACK || req.Method == CANCEL {
		return nil, fmt.Errorf("sip: SendRequest cannot send %s", req.Method)
	}
	// The request goes out again to the next target as it is now.
	req = req.Clone()
	a, next, err := s.reach(req, targets)
	if err != nil {
		return nil, err
	}
	ct := &ClientTransaction{Request: a.request, stack: s, responses: make(chan *Message, responseBuffer), attempt: a}
	if err := s.post(sendRequest{ct: ct, req: req, next: next}); err != nil {
		return nil, err
	}
	return ct, nil
}

// SendACK sends the ACK for a 2xx to the first of targets. The UAC core
// passes that ACK straight to the transport, once for the 2xx and again
// for each retransmission of it (RFC 3261 13.2.2.4); a proxy forwards
// it the same way. The stack puts its own Via, with a new branch, on
// top of a copy of ack.
//
// The ACK for a 2xx is a transaction of its own, so it need not go to
// the server that took the INVITE (RFC 3263 4). Nothing answers it, so
// the only failure it can meet at a target is a transport error, and
// then it goes to the next target (4.3).
func (s *Stack) SendACK(ack *Message, targets ...Destination) error {
	if ack.Method != ACK {
		return fmt.Errorf("sip: SendACK cannot send %s", ack.Method)
	}
	return s.sendWithoutTransaction(ack, NewBranch(), targets)
}

// SendCANCEL forwards a CANCEL that matched no server transaction
// (Config.OnCANCEL) statelessly, as RFC 3261 16.10 asks of a proxy: to
// the first of targets that the stack can send it to, without a client
// transaction (16.11). The stack puts its own Via on top of a copy of
// cancel, with the branch that 16.11 recommends for a stateless proxy:
// a hash of the branch that cancel came with. Each retransmission thus
// goes out with the same branch, and so would the request it cancels,
// had this node forwarded that one statelessly too, since a CANCEL has
// the branch of the request it cancels (9.1). The responses to it go on
// to the Via below this node's (16.11).
func (s *Stack) SendCANCEL(cancel *Message, targets ...Destination) error {
	if cancel.Method != CANCEL {
		return fmt.Errorf("sip: SendCANCEL cannot send %s", cancel.Method)
	}
	v, err := ParseVia(cancel.Get("Via"))
	if err != nil {
		return err
	}
	return s.sendWithoutTransaction(cancel, statelessBranch(v), targets)
}

// statelessBranch is the branch of the Via that this node puts on a
// request it forwards statelessly, received being the top Via the
// request came with: a hash of its branch (RFC 3261 16.11). The stack
// takes only branches with the magic cookie (17.2.3), which are unique
// already, so the hash is unique too.
func statelessBranch(received Via) string {
	sum := sha256.Sum256([]byte(received.Branch()))
	return BranchMagicCookie + hex.EncodeToString(sum[:16])
}

// sendWithoutTransaction sends req, with this node's Via and branch on
// top, to the first of targets that takes it.
func (s *Stack) sendWithoutTransaction(req *Message, branch string, targets []Destination) error {
	err := errNoTarget
	for i, target := range targets {
		var out *Message
		var dst Destination
		if out, dst, err = s.prepare(req, branch, target); err == nil {
			if err = s.transport.SendRequest(out, dst); err == nil {
				return nil
			}
		}
		if errors.Is(err, ErrStackClosed) {
			return err
		}
		s.logNextTarget(req, target, err.Error(), targets[i+1:])
	}
	return err
}

// maxUDPRequestSize is the request size above which RFC 3261 18.1.1
// asks for a congestion-controlled transport when the path MTU is not
// known. No node here learns the path MTU, so the rule for a request
// within 200 bytes of it never applies.
const maxUDPRequestSize = 1300

// prepare settles the transport of a request this node sends, and
// returns a copy of req with this node's Via, which names that
// transport and has branch, on top. A request for UDP that is larger than 1300 bytes
// goes over TCP instead (RFC 3261 18.1.1). The TCP connection is opened
// here, in the caller's goroutine, so that the stack's loop does not
// wait for it when it sends.
func (s *Stack) prepare(req *Message, branch string, destination Destination) (*Message, Destination, error) {
	select {
	case <-s.done:
		return nil, Destination{}, ErrStackClosed
	default:
	}
	out := s.withOwnVia(req, branch, destination.Transport)
	switch size := len(out.Bytes()); {
	case destination.Transport == UDP && size > maxUDPRequestSize:
		overTCP := Destination{Transport: TCP, Addr: destination.Addr}
		if _, err := s.transport.connectTCP(overTCP.Addr); err != nil {
			// 18.1.1: when the connection is refused, the request SHOULD
			// go over UDP after all. Any other failure to connect, such
			// as a timeout, is treated the same way.
			s.log.Warn("request larger than 1300 bytes sent over UDP, since TCP failed",
				"method", string(req.Method), "bytes", size, "dst", overTCP.String(), "error", err.Error())
			return out, destination, nil
		}
		s.log.Info("request larger than 1300 bytes sent over TCP",
			"method", string(req.Method), "bytes", size, "dst", overTCP.String())
		return s.withOwnVia(req, branch, TCP), overTCP, nil
	case destination.Transport == TCP:
		if _, err := s.transport.connectTCP(destination.Addr); err != nil {
			return nil, Destination{}, err
		}
	}
	return out, destination, nil
}

// withOwnVia returns a copy of req with this node's Via on top, with
// branch (RFC 3261 8.1.1.7, 16.6 step 8 and 16.11) and the transport
// the request goes over (18.1.1). The Via asks for rport (RFC 3581 3),
// which RFC 3581 leaves optional; asking costs nothing where there is
// no NAT.
func (s *Stack) withOwnVia(req *Message, branch string, transport Transport) *Message {
	host, port, _ := splitHostPort(s.cfg.SentBy)
	via := Via{Transport: string(transport), Host: host, Port: port, Params: Params{{Name: "branch", Value: branch}, {Name: "rport"}}}
	req = req.Clone()
	req.Prepend("Via", via.String())
	return req
}

// ServerTransaction is a server transaction as the TU sees it.
type ServerTransaction struct {
	// Request is the request that created the transaction, with
	// received and rport already in its top Via.
	Request *Message
	// Source is where the request came from.
	Source Destination

	stack     *Stack
	entry     *serverEntry
	cancelled chan struct{}
	// cancel is the CANCEL, set before cancelled is closed.
	cancel *Message
	// final is the status code of the last final response passed to
	// Respond.
	final atomic.Int32
}

// Cancelled is closed when a CANCEL for this INVITE arrives before its
// final response. The stack has answered the CANCEL with 200 already. A
// UAS then answers the INVITE with 487 (Request Terminated) (RFC 3261
// 9.2), and a proxy cancels the branches it forwarded (16.10). For any
// other method it is never closed.
func (st *ServerTransaction) Cancelled() <-chan struct{} { return st.cancelled }

// CancelRequest returns the CANCEL, once Cancelled is closed. Its
// Reason tells why the caller gave up (RFC 3326 2).
func (st *ServerTransaction) CancelRequest() *Message { return st.cancel }

// Respond passes a response to the transaction, which sends it to the
// address in its top Via and resends it for every retransmitted
// request. NewResponse builds one. The stack keeps a copy, so resp may
// be changed afterwards.
//
// The transaction does not retransmit a 2xx to an INVITE. The TU calls
// Respond with it again, at intervals that start at T1 and double up to
// T2, until the ACK reaches Config.OnACK (RFC 6026 8.1, which replaces
// RFC 3261 13.3.1.4).
func (st *ServerTransaction) Respond(resp *Message) error {
	if resp.StatusCode >= 200 {
		st.final.Store(int32(resp.StatusCode))
	}
	return st.stack.post(respond{entry: st.entry, response: resp.Clone()})
}

// FinalStatus is the status code of the last final response that the TU
// passed to Respond, or 0 while it has passed none, so that a TU can
// count how its requests ended.
func (st *ServerTransaction) FinalStatus() int { return int(st.final.Load()) }

// stackInput is what the reader, the timers, and the TU hand to the
// loop.
type stackInput interface{}

type (
	receivedMessage struct {
		msg    *Message
		source Destination
	}
	// admitDecided is the answer of Config.Admit about req, the first
	// request with its Call-ID in the admitQueue of source.
	admitDecided struct {
		req      *Message
		source   Destination
		admitted bool
	}
	// sendRequest starts the first client transaction of ct, and the
	// goroutine that follows ct to its end (follow). req and next are
	// what follow needs to send the request on.
	sendRequest struct {
		ct   *ClientTransaction
		req  *Message
		next []Destination
	}
	// startClient starts a client transaction that follow sends the
	// request on in.
	startClient  struct{ a *attempt }
	cancelInvite struct {
		a       *attempt
		headers []HeaderField
	}
	respond struct {
		entry    *serverEntry
		response *Message
	}
	timerExpired struct {
		owner any // *clientEntry or *serverEntry
		timer Timer
		seq   uint64
	}
)

type armedTimer struct {
	timer *time.Timer
	seq   uint64
}

// stateMachine is the state of any of the four transaction state
// machines of transaction.go, so that the loop drives them all the same
// way.
type stateMachine interface {
	current() TransactionState
	next(TransactionEvent) (stateMachine, []TransactionAction)
}

func (s NonInviteClientState) current() TransactionState { return s.State }
func (s InviteClientState) current() TransactionState    { return s.State }
func (s NonInviteServerState) current() TransactionState { return s.State }
func (s InviteServerState) current() TransactionState    { return s.State }

func (s NonInviteClientState) next(e TransactionEvent) (stateMachine, []TransactionAction) {
	return NonInviteClientTransition(s, e)
}

func (s InviteClientState) next(e TransactionEvent) (stateMachine, []TransactionAction) {
	return InviteClientTransition(s, e)
}

func (s NonInviteServerState) next(e TransactionEvent) (stateMachine, []TransactionAction) {
	return NonInviteServerTransition(s, e)
}

func (s InviteServerState) next(e TransactionEvent) (stateMachine, []TransactionAction) {
	return InviteServerTransition(s, e)
}

type clientEntry struct {
	key      clientKey
	attempt  *attempt
	machine  stateMachine
	timers   map[Timer]armedTimer
	tuClosed bool
	// cancelHeaders are the header fields that the TU gave for the
	// CANCEL, which may go out later than the TU asked for it.
	cancelHeaders []HeaderField
}

type serverEntry struct {
	key     serverKey
	st      *ServerTransaction
	machine stateMachine
	timers  map[Timer]armedTimer
}

func (s *Stack) post(in stackInput) error {
	select {
	case s.inbox <- in:
		return nil
	case <-s.done:
		return ErrStackClosed
	}
}

func (s *Stack) read() {
	defer s.wg.Done()
	for {
		m, source, err := s.transport.Receive()
		if err != nil {
			select {
			case <-s.stop:
			default:
				s.log.Error("sip transport failed", "error", err.Error())
			}
			return
		}
		if s.post(receivedMessage{msg: m, source: source}) != nil {
			return
		}
	}
}

func (s *Stack) run() {
	defer s.wg.Done()
	defer close(s.done)
	for {
		// A nil channel is never ready, so the loop offers a call to the
		// TU only while one waits.
		var toTU chan<- func()
		var next func()
		if len(s.forTU) > 0 {
			toTU, next = s.toTU, s.forTU[0]
		}
		select {
		case <-s.stop:
			s.shutdown()
			return
		case in := <-s.inbox:
			s.handle(in)
		case toTU <- next:
			s.forTU = s.forTU[1:]
		}
	}
}

// callTU makes the calls of OnRequest and OnACK one after the other
// (Config.Ordered).
func (s *Stack) callTU() {
	defer s.wg.Done()
	for {
		select {
		case call := <-s.toTU:
			call()
		case <-s.done:
			return
		}
	}
}

// passRequestToTU calls the TU for a request or an ACK: in a goroutine
// of its own, or after the calls before it when the TU asked for the
// order of arrival (Config.Ordered).
func (s *Stack) passRequestToTU(call func()) {
	if s.cfg.Ordered {
		s.forTU = append(s.forTU, call)
		return
	}
	go call()
}

func (s *Stack) handle(in stackInput) {
	switch in := in.(type) {
	case receivedMessage:
		if in.msg.IsRequest() {
			s.receiveRequest(in.msg, in.source, false)
		} else {
			s.receiveResponse(in.msg)
		}
	case admitDecided:
		s.receiveAdmitDecision(in)
	case sendRequest:
		s.startClient(in.ct.attempt)
		// The loop holds the WaitGroup until it ends, so Close waits for
		// this goroutine too.
		s.wg.Add(1)
		go s.follow(in.ct, in.req, in.next)
	case startClient:
		s.startClient(in.a)
	case cancelInvite:
		v, _ := ParseVia(in.a.request.Get("Via"))
		if e, ok := s.clients[clientKey{branch: v.Branch(), method: INVITE}]; ok && e.attempt == in.a {
			e.cancelHeaders = in.headers
			s.stepClient(e, CancelFromTU{})
		}
	case respond:
		if s.servers[in.entry.key] != in.entry {
			s.log.Warn("discarded response for a terminated server transaction",
				"status", in.response.StatusCode, "branch", in.entry.key.branch)
			return
		}
		s.stepServer(in.entry, ResponseFromTU{Response: in.response})
	case timerExpired:
		switch e := in.owner.(type) {
		case *clientEntry:
			if s.clients[e.key] == e && e.timers[in.timer].seq == in.seq {
				s.stepClient(e, TimerFired{Timer: in.timer})
			}
		case *serverEntry:
			if s.servers[e.key] == e && e.timers[in.timer].seq == in.seq {
				s.stepServer(e, TimerFired{Timer: in.timer})
			}
		}
	}
}

func (s *Stack) receiveResponse(resp *Message) {
	v, err := ParseVia(resp.Get("Via"))
	if err != nil {
		s.log.Warn("discarded response without a usable Via", "error", err.Error())
		return
	}
	if sentByOf(v) != s.sentBy {
		s.log.Warn("discarded response for another sent-by", "sent_by", v.SentBy())
		return
	}
	cseq, err := ParseCSeq(resp.Get("CSeq"))
	if err != nil {
		s.log.Warn("discarded response without a usable CSeq", "error", err.Error())
		return
	}
	e, ok := s.clients[clientKey{branch: v.Branch(), method: cseq.Method}]
	if !ok {
		if s.relayStateless(resp, v) {
			return
		}
		s.log.Info("discarded response that matches no client transaction",
			"status", resp.StatusCode, "branch", v.Branch(), "cseq", resp.Get("CSeq"))
		return
	}
	s.stepClient(e, ResponseReceived{Response: resp})
}

// relayStateless is RFC 3261 16.11 for a response to a CANCEL that
// SendCANCEL forwarded: own, the top Via, has the branch that the Via
// below it gives, so this node put it there. The response goes on
// without own, to where the Via below says (18.2.2). It reports whether
// resp was such a response.
func (s *Stack) relayStateless(resp *Message, own Via) bool {
	// Only the CANCEL goes out statelessly, and the branch it carries is
	// one that anyone can compute from the Via below it. Any other
	// response is a stray one, which a stateful element discards (RFC
	// 6026 8.2) rather than send on to the address that its Via names.
	if cseq, err := ParseCSeq(resp.Get("CSeq")); err != nil || cseq.Method != CANCEL {
		return false
	}
	out := resp.Clone()
	out.RemoveFirst("Via")
	next, err := ParseVia(out.Get("Via"))
	if err != nil || own.Branch() != statelessBranch(next) {
		return false
	}
	if err := s.transport.SendResponse(out, connectionOf(next)); err != nil {
		s.log.Warn("cannot relay the response to a CANCEL forwarded statelessly",
			"status", resp.StatusCode, "branch", next.Branch(), "error", err.Error())
	}
	return true
}

// connectionOf is where a request with v as its top Via came from:
// received and rport, which this node wrote into v (RFC 3581 4), name
// the far end of its connection over TCP. The transport sends a response
// back on that connection while it is open (RFC 3261 18.2.2).
func connectionOf(v Via) Destination {
	d := Destination{Transport: Transport(v.Transport)}
	received, _ := v.Params.Get("received")
	rport, _ := v.Params.Get("rport")
	if addr, err := netip.ParseAddrPort(joinHostPort(received, 0) + ":" + rport); err == nil {
		d.Addr = addr
	}
	return d
}

func (s *Stack) receiveRequest(req *Message, source Destination, admitted bool) {
	// The transport has already parsed this Via.
	v, _ := ParseVia(req.Get("Via"))
	branch := v.Branch()
	if !strings.HasPrefix(branch, BranchMagicCookie) {
		// SIMPLIFIED: RFC 3261 17.2.3. Requests from RFC 2543 clients,
		// whose branch lacks the magic cookie, need matching on the
		// Request-URI, tags, Call-ID, CSeq and Via. Every client this
		// project talks to follows RFC 3261, so such requests are
		// dropped.
		s.log.Warn("discarded request whose branch lacks the magic cookie", "method", string(req.Method), "branch", branch)
		return
	}
	if req.Method == ACK {
		s.receiveACK(req, v, source, admitted)
		return
	}
	key := serverKey{branch: branch, sentBy: sentByOf(v), method: req.Method}
	if e, ok := s.servers[key]; ok {
		s.stepServer(e, RequestRetransmitted{})
		return
	}
	if !admitted && !s.admits(req, source) {
		return
	}
	if _, ok := s.cancellable[key.cancelKey()]; req.Method == CANCEL && !ok && s.cfg.OnCANCEL != nil {
		s.passRequestToTU(func() { s.cfg.OnCANCEL(req) })
		return
	}
	e := &serverEntry{key: key, timers: map[Timer]armedTimer{}}
	e.st = &ServerTransaction{Request: req, Source: source, stack: s, entry: e, cancelled: make(chan struct{})}
	var actions []TransactionAction
	if req.Method == INVITE {
		// A 100 (Trying) SHOULD NOT carry a To tag (RFC 3261 17.2.1).
		e.machine, actions = StartInviteServer(s.cfg.Timers, source.Transport.reliable(), NewResponse(req, 100, ""))
	} else {
		e.machine = StartNonInviteServer(s.cfg.Timers, source.Transport.reliable())
	}
	s.servers[key] = e
	if req.Method != CANCEL {
		s.cancellable[key.cancelKey()] = e
	}
	s.logState("server", req.Method, branch, "request", "", e.machine.current())
	s.carryOutServer(e, actions)
	if req.Method == CANCEL {
		s.answerCancel(e)
		return
	}
	s.passRequestToTU(func() { s.cfg.OnRequest(e.st) })
}

// answerCancel is RFC 3261 9.2. The CANCEL has a server transaction of
// its own, so that a retransmitted CANCEL gets the same answer. The
// transaction it cancels has the same branch and sent-by, and may be of
// any method but CANCEL. Only an INVITE that has no final response yet
// is cancelled; for a transaction of another method the CANCEL gets 200
// all the same, and changes nothing. A CANCEL that matches nothing gets
// 481 here, at a UA; at a proxy it goes to Config.OnCANCEL instead.
//
// SIMPLIFIED: RFC 3261 16.10. A proxy MUST cancel the pending client
// transactions of a request of any method when a CANCEL matches it. A
// proxy on this stack cancels only INVITEs, as clients SHOULD NOT cancel
// other requests (9.1).
func (s *Stack) answerCancel(e *serverEntry) {
	cancel := e.st.Request
	target, ok := s.cancellable[e.key.cancelKey()]
	if !ok {
		s.stepServer(e, ResponseFromTU{Response: NewResponse(cancel, 481, NewTag())})
		return
	}
	var last *Message
	switch m := target.machine.(type) {
	case InviteServerState:
		last = m.LastResponse
		if m.State == Proceeding {
			select {
			case <-target.st.cancelled:
			default:
				target.st.cancel = cancel
				close(target.st.cancelled)
			}
		}
	case NonInviteServerState:
		last = m.LastResponse
	}
	s.stepServer(e, ResponseFromTU{Response: NewResponse(cancel, 200, toTag(last))})
}

// toTag returns the To tag of resp, or a new tag when there is none
// yet. The 200 to a CANCEL SHOULD carry the To tag of the responses to
// the INVITE (RFC 3261 9.2), which only a response already sent shows.
func toTag(resp *Message) string {
	if resp != nil {
		if to, err := ParseNameAddr(resp.Get("To")); err == nil {
			if tag, ok := to.Params.Get("tag"); ok {
				return tag
			}
		}
	}
	return NewTag()
}

// receiveACK hands an ACK to the INVITE server transaction it matches.
// Only the ACK for a 300-699 response matches one, because it carries
// the INVITE's branch (RFC 3261 17.1.1.3). The ACK for a 2xx has a
// branch of its own and goes to the TU.
func (s *Stack) receiveACK(ack *Message, v Via, source Destination, admitted bool) {
	if e, ok := s.servers[serverKey{branch: v.Branch(), sentBy: sentByOf(v), method: INVITE}]; ok {
		s.stepServer(e, ACKReceived{ACK: ack})
		return
	}
	if !admitted && !s.admits(ack, source) {
		return
	}
	s.passACKToTU(ack)
}

// maxAdmitQueue bounds the requests from one source that wait for
// Config.Admit, whatever their Call-IDs, the ones it is deciding on
// included. A flood from one address thus piles up no more than this,
// and starts no more calls of Admit at once, while Admit waits for a
// slow store. A node of the network is one source for all of its
// requests, such as the NOTIFYs of the reg event for many UEs at once,
// so the bound leaves room for a burst of them; a request dropped over
// TCP is not sent again before its client transaction times out.
var maxAdmitQueue = 512

// admitQueue is what waits for Config.Admit from one source: the
// requests with each Call-ID in the order they arrived, Admit deciding
// on the first of each. A Call-ID leaves byCallID once Admit has
// decided on its last request, and the source leaves Stack.admitting
// with its last Call-ID, so neither map keeps what no longer waits.
type admitQueue struct {
	byCallID map[string][]*Message
	// waiting is how many requests byCallID holds in all, which
	// maxAdmitQueue bounds.
	waiting int
}

// admits tells whether req may go on at once. A request that
// Config.Screen lets in goes to Config.Admit, when there is one, and
// comes back to the loop once Admit has let it in.
func (s *Stack) admits(req *Message, source Destination) bool {
	if s.cfg.Screen != nil && !s.cfg.Screen(req, source) {
		s.log.Info("dropped request that the screen refused", "method", string(req.Method), "src", source.String())
		return false
	}
	if s.cfg.Admit == nil {
		return true
	}
	q := s.admitting[source]
	if q == nil {
		q = &admitQueue{byCallID: map[string][]*Message{}}
		s.admitting[source] = q
	}
	if q.waiting == maxAdmitQueue {
		s.log.Warn("dropped request since too many from its source wait for admission",
			"method", string(req.Method), "src", source.String())
		return false
	}
	q.waiting++
	callID := req.Get("Call-ID")
	earlier := q.byCallID[callID]
	q.byCallID[callID] = append(earlier, req)
	if len(earlier) == 0 {
		s.askAdmit(req, source)
	}
	return false
}

// askAdmit asks Config.Admit about req off the loop, in a goroutine
// that Close waits for.
func (s *Stack) askAdmit(req *Message, source Destination) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.post(admitDecided{req: req, source: source, admitted: s.cfg.Admit(req, source)})
	}()
}

// receiveAdmitDecision takes up the request that Config.Admit has
// decided on, and then asks Admit about the next request with the same
// Call-ID from the same source.
func (s *Stack) receiveAdmitDecision(in admitDecided) {
	// Read before receiveRequest hands the request to the TU, which
	// runs in a goroutine of its own.
	callID := in.req.Get("Call-ID")
	if in.admitted {
		s.receiveRequest(in.req, in.source, true)
	}
	q := s.admitting[in.source]
	q.waiting--
	if later := q.byCallID[callID][1:]; len(later) > 0 {
		q.byCallID[callID] = later
		s.askAdmit(later[0], in.source)
		return
	}
	delete(q.byCallID, callID)
	if q.waiting == 0 {
		delete(s.admitting, in.source)
	}
}

func (s *Stack) passACKToTU(ack *Message) {
	if s.cfg.OnACK == nil {
		v, _ := ParseVia(ack.Get("Via"))
		s.log.Info("discarded ACK that matches no INVITE transaction", "branch", v.Branch())
		return
	}
	s.passRequestToTU(func() { s.cfg.OnACK(ack) })
}

func (s *Stack) startClient(a *attempt) {
	v, _ := ParseVia(a.request.Get("Via"))
	e := &clientEntry{
		key:     clientKey{branch: v.Branch(), method: a.request.Method},
		attempt: a,
		timers:  map[Timer]armedTimer{},
	}
	s.clients[e.key] = e
	var actions []TransactionAction
	if e.key.method == INVITE {
		e.machine, actions = StartInviteClient(s.cfg.Timers, a.destination.Transport.reliable())
	} else {
		e.machine, actions = StartNonInviteClient(s.cfg.Timers, a.destination.Transport.reliable())
	}
	s.logState("client", e.key.method, e.key.branch, "request from TU", "", e.machine.current())
	s.carryOutClient(e, actions)
}

func (s *Stack) stepClient(e *clientEntry, ev TransactionEvent) {
	next, actions := e.machine.next(ev)
	if next.current() != e.machine.current() {
		s.logState("client", e.key.method, e.key.branch, ev.eventName(), e.machine.current().String(), next.current())
	}
	e.machine = next
	s.carryOutClient(e, actions)
}

func (s *Stack) carryOutClient(e *clientEntry, actions []TransactionAction) {
	var sendErr error
	for _, a := range actions {
		switch a := a.(type) {
		case SendRequest:
			if err := s.transport.SendRequest(e.attempt.request, e.attempt.destination); err != nil {
				sendErr = err
			}
		case SendACK:
			// RFC 3261 17.1.1.2 and RFC 3263 4: the same destination as
			// the INVITE.
			ack := newMatchingRequest(e.attempt.request, ACK, a.Response.Get("To"))
			if err := s.transport.SendRequest(ack, e.attempt.destination); err != nil {
				sendErr = err
			}
		case SendCancel:
			// RFC 3261 9.1 and RFC 3263 4: the same destination as the
			// INVITE. Nobody reads the CANCEL's own responses; the
			// INVITE's final response tells the TU how the cancel went.
			cancel := newMatchingRequest(e.attempt.request, CANCEL, e.attempt.request.Get("To"))
			addHeaders(cancel, e.cancelHeaders)
			s.startClient(newAttempt(cancel, e.attempt.destination))
		case PassToTU:
			s.passToTU(e, a.Response)
		case InformTU:
			s.closeTU(e, a.Err)
		case StartTimer:
			s.startTimer(e, e.timers, a)
		}
	}
	if e.machine.current() == Terminated {
		stopTimers(e.timers)
		delete(s.clients, e.key)
		s.closeTU(e, nil)
		return
	}
	if sendErr != nil {
		s.stepClient(e, TransportError{Err: sendErr})
	}
}

func (s *Stack) passToTU(e *clientEntry, resp *Message) {
	if !queue(e.attempt.responses, resp) {
		s.log.Warn("dropped response the TU has not read", "status", resp.StatusCode, "branch", e.key.branch)
		return
	}
	if resp.StatusCode >= 200 && !(e.key.method == INVITE && resp.StatusCode < 300) {
		s.closeTU(e, nil)
	}
}

func (s *Stack) closeTU(e *clientEntry, err error) {
	if e.tuClosed {
		return
	}
	e.attempt.err = err
	e.tuClosed = true
	close(e.attempt.responses)
}

func (s *Stack) stepServer(e *serverEntry, ev TransactionEvent) {
	next, actions := e.machine.next(ev)
	if next.current() != e.machine.current() {
		s.logState("server", e.key.method, e.key.branch, ev.eventName(), e.machine.current().String(), next.current())
	}
	e.machine = next
	s.carryOutServer(e, actions)
}

func (s *Stack) carryOutServer(e *serverEntry, actions []TransactionAction) {
	var sendErr error
	for _, a := range actions {
		switch a := a.(type) {
		case SendResponse:
			if err := s.transport.SendResponse(a.Response, e.st.Source); err != nil {
				sendErr = err
			}
		case PassACKToTU:
			s.passACKToTU(a.ACK)
		case InformTU:
			// The server TU has already handed over its response, so
			// the failure can only be logged.
			s.log.Warn("server transaction failed", "branch", e.key.branch, "error", a.Err.Error())
		case StartTimer:
			s.startTimer(e, e.timers, a)
		}
	}
	if e.machine.current() == Terminated {
		stopTimers(e.timers)
		delete(s.servers, e.key)
		if s.cancellable[e.key.cancelKey()] == e {
			delete(s.cancellable, e.key.cancelKey())
		}
		return
	}
	if sendErr != nil {
		s.stepServer(e, TransportError{Err: sendErr})
	}
}

// newMatchingRequest builds, from an INVITE, a request that the next
// hop must match to the INVITE's server transaction: the ACK for a
// 300-699 response (RFC 3261 17.1.1.3) or a CANCEL (9.1). Both carry
// the INVITE's top Via, with its branch, as their only Via, and the
// INVITE's Request-URI, From, Call-ID and CSeq number. Route comes along
// for stateless proxies on the way. The To of an ACK is the To of the
// response it acknowledges.
func newMatchingRequest(invite *Message, method Method, to string) *Message {
	m := &Message{Method: method, RequestURI: invite.RequestURI}
	m.Add("Via", invite.Get("Via"))
	for _, route := range invite.GetAll("Route") {
		m.Add("Route", route)
	}
	m.Add("To", to)
	m.Add("From", invite.Get("From"))
	m.Add("Max-Forwards", "70")
	m.Add("Call-ID", invite.Get("Call-ID"))
	cseq, _ := ParseCSeq(invite.Get("CSeq"))
	m.Add("CSeq", CSeq{Number: cseq.Number, Method: method}.String())
	return m
}

// startTimer arms a timer for owner. The sequence number lets the loop
// ignore an expiry that was already on its way when the timer was
// replaced.
func (s *Stack) startTimer(owner any, timers map[Timer]armedTimer, a StartTimer) {
	if old, ok := timers[a.Timer]; ok {
		old.timer.Stop()
	}
	s.timerSeq++
	seq := s.timerSeq
	timers[a.Timer] = armedTimer{
		seq: seq,
		timer: time.AfterFunc(a.Duration, func() {
			s.post(timerExpired{owner: owner, timer: a.Timer, seq: seq})
		}),
	}
}

func stopTimers(timers map[Timer]armedTimer) {
	for _, t := range timers {
		t.timer.Stop()
	}
}

func (s *Stack) shutdown() {
	for _, e := range s.clients {
		stopTimers(e.timers)
		s.closeTU(e, ErrStackClosed)
	}
	for _, e := range s.servers {
		stopTimers(e.timers)
	}
}

func (s *Stack) logState(kind string, method Method, branch, event, from string, to TransactionState) {
	s.log.Info("sip transaction", "kind", kind, "method", string(method), "branch", branch,
		"event", event, "from", from, "to", to.String())
}
