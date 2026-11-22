package sip

import (
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
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
	// Ordered has the stack call OnRequest and OnACK one after the
	// other, in the order the requests arrived, instead of each in a
	// goroutine of its own. The callbacks then must return at once, and
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
	// the stack drops the request as one that Screen refuses. Each copy
	// of a request is asked about until one is let in, and a copy let in
	// after another has started the transaction counts as its
	// retransmission.
	//
	// Admit is asked about the requests from one source, the same
	// transport and address, one at a time and in the order they
	// arrived, and the stack takes them up in that order. A CANCEL thus
	// finds the INVITE it cancels (RFC 3261 9.2), and with Ordered the
	// TU sees the requests from a source in the order they arrived, the
	// ACK for a 2xx before a BYE that follows it. While Admit waits over
	// one request, the later ones from its source wait too, so it should
	// answer soon. Admit must not change req.
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
//   - A response is logged and discarded. RFC 6026 8.2 and 8.9 ask this
//     of every element but a stateless proxy, and a proxy built on this
//     stack keeps transaction state. A retransmitted 2xx still reaches
//     the TU while its INVITE client transaction waits in Accepted.
//   - A request that Config.Screen or Config.Admit refuses is dropped.
//   - An ACK goes to Config.OnACK.
//   - Any other request starts a server transaction and goes to
//     Config.OnRequest, except a CANCEL.
//
// The stack answers a CANCEL itself (RFC 3261 9.2): 200 when it matches
// an INVITE server transaction, whose TU then sees Cancelled, and 481
// otherwise.
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
	clients  map[clientKey]*clientEntry
	servers  map[serverKey]*serverEntry
	timerSeq uint64
	// admitting holds, for each source with a request in Config.Admit,
	// that request and then the requests from the source that came
	// after it.
	admitting map[Destination][]*Message
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
	if cfg.Timers.T1 <= 0 || cfg.Timers.T2 < cfg.Timers.T1 || cfg.Timers.T4 <= 0 {
		return nil, fmt.Errorf("sip: Config.Timers %+v: need 0 < T1 <= T2 and T4 > 0", cfg.Timers)
	}
	if cfg.TimerC == 0 {
		cfg.TimerC = DefaultTimerC
	}
	s := &Stack{
		transport: transport,
		cfg:       cfg,
		sentBy:    sentByOf(Via{Host: host, Port: port}),
		log:       log,
		inbox:     make(chan stackInput),
		toTU:      make(chan func()),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		clients:   map[clientKey]*clientEntry{},
		servers:   map[serverKey]*serverEntry{},
		admitting: map[Destination][]*Message{},
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
// running end with ErrStackClosed.
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

// ClientTransaction is a client transaction as the TU sees it.
// Responses delivers every provisional response and then the final
// one. It is closed after the final response, except after a 2xx to an
// INVITE: then it stays open until Timer M, 64*T1 later, and delivers
// every further 2xx, which is either a retransmission that the TU
// acknowledges again or the answer of another branch of a fork (RFC
// 6026 7.2). It is also closed after a failure.
type ClientTransaction struct {
	// Request is the request as sent, with this node's Via on top. The
	// stack retransmits it, so it must not be changed.
	Request *Message

	stack       *Stack
	destination Destination
	responses   chan *Message
	err         error
}

func (ct *ClientTransaction) Responses() <-chan *Message { return ct.responses }

// Cancel asks for the INVITE to be cancelled (RFC 3261 9.1). The stack
// sends the CANCEL, in a client transaction of its own, as soon as a
// provisional response has arrived, and not at all once a final
// response has. Either way the outcome is the INVITE's final response
// on Responses, normally 487 (Request Terminated).
//
// headers go into the CANCEL: a Reason that tells why (RFC 3326 2),
// which a proxy also copies from the CANCEL it received.
func (ct *ClientTransaction) Cancel(headers ...HeaderField) error {
	if ct.Request.Method != INVITE {
		// 9.1: a non-INVITE request is answered at once, so a CANCEL
		// would always race with the answer.
		return fmt.Errorf("sip: cannot cancel %s", ct.Request.Method)
	}
	return ct.stack.post(cancelInvite{ct: ct, headers: headers})
}

// Err is nil if a final response arrived. Otherwise it is ErrTimeout,
// ErrStackClosed, or the transport error. It is set once Responses is
// closed.
func (ct *ClientTransaction) Err() error { return ct.err }

// responseBuffer bounds the responses waiting for the TU. The stack
// never blocks on a slow TU: it keeps the last slot for the final
// response and drops other responses that do not fit.
const responseBuffer = 8

// SendRequest starts a client transaction, INVITE or non-INVITE, that
// sends req to destination. The stack puts its own Via, with a new
// branch, on top of a copy of req.
//
// ACK and CANCEL have no client transaction of their own here: an ACK
// for a 2xx goes out with SendACK, the transaction sends the ACK for
// any other final response, and ClientTransaction.Cancel sends CANCEL.
func (s *Stack) SendRequest(req *Message, destination Destination) (*ClientTransaction, error) {
	if req.Method == ACK || req.Method == CANCEL {
		return nil, fmt.Errorf("sip: SendRequest cannot send %s", req.Method)
	}
	out, destination, err := s.prepare(req, destination)
	if err != nil {
		return nil, err
	}
	ct := s.newClientTransaction(out, destination)
	if err := s.post(startClient{ct: ct}); err != nil {
		return nil, err
	}
	return ct, nil
}

func (s *Stack) newClientTransaction(req *Message, destination Destination) *ClientTransaction {
	return &ClientTransaction{Request: req, stack: s, destination: destination, responses: make(chan *Message, responseBuffer)}
}

// SendACK sends the ACK for a 2xx to destination. The UAC core passes
// that ACK straight to the transport, once for the 2xx and again for
// each retransmission of it (RFC 3261 13.2.2.4); a proxy forwards it
// the same way. The stack puts its own Via, with a new branch, on top
// of a copy of ack.
func (s *Stack) SendACK(ack *Message, destination Destination) error {
	if ack.Method != ACK {
		return fmt.Errorf("sip: SendACK cannot send %s", ack.Method)
	}
	out, destination, err := s.prepare(ack, destination)
	if err != nil {
		return err
	}
	return s.transport.SendRequest(out, destination)
}

// maxUDPRequestSize is the request size above which RFC 3261 18.1.1
// asks for a congestion-controlled transport when the path MTU is not
// known. No node here learns the path MTU, so the rule for a request
// within 200 bytes of it never applies.
const maxUDPRequestSize = 1300

// prepare settles the transport of a request this node sends, and
// returns a copy of req with this node's Via, which names that
// transport, on top. A request for UDP that is larger than 1300 bytes
// goes over TCP instead (RFC 3261 18.1.1). The TCP connection is opened
// here, in the caller's goroutine, so that the stack's loop does not
// wait for it when it sends.
func (s *Stack) prepare(req *Message, destination Destination) (*Message, Destination, error) {
	select {
	case <-s.done:
		return nil, Destination{}, ErrStackClosed
	default:
	}
	out := s.withOwnVia(req, destination.Transport)
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
		return s.withOwnVia(req, TCP), overTCP, nil
	case destination.Transport == TCP:
		if _, err := s.transport.connectTCP(destination.Addr); err != nil {
			return nil, Destination{}, err
		}
	}
	return out, destination, nil
}

// withOwnVia returns a copy of req with this node's Via on top, with a
// new branch (RFC 3261 8.1.1.7 and 16.6 step 8) and the transport the
// request goes over (18.1.1). The Via asks for rport (RFC 3581 3),
// which RFC 3581 leaves optional; asking costs nothing where there is
// no NAT.
func (s *Stack) withOwnVia(req *Message, transport Transport) *Message {
	host, port, _ := splitHostPort(s.cfg.SentBy)
	via := Via{Transport: string(transport), Host: host, Port: port, Params: Params{{Name: "branch", Value: NewBranch()}, {Name: "rport"}}}
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
	return st.stack.post(respond{entry: st.entry, response: resp.Clone()})
}

// stackInput is what the reader, the timers, and the TU hand to the
// loop.
type stackInput interface{}

type (
	receivedMessage struct {
		msg    *Message
		source Destination
	}
	// admitDecided is the answer of Config.Admit about the first
	// request in Stack.admitting of source.
	admitDecided struct {
		req      *Message
		source   Destination
		admitted bool
	}
	startClient  struct{ ct *ClientTransaction }
	cancelInvite struct {
		ct      *ClientTransaction
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
	ct       *ClientTransaction
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
	case startClient:
		s.startClient(in.ct)
	case cancelInvite:
		v, _ := ParseVia(in.ct.Request.Get("Via"))
		if e, ok := s.clients[clientKey{branch: v.Branch(), method: INVITE}]; ok && e.ct == in.ct {
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
		s.log.Info("discarded response that matches no client transaction",
			"status", resp.StatusCode, "branch", v.Branch(), "cseq", resp.Get("CSeq"))
		return
	}
	s.stepClient(e, ResponseReceived{Response: resp})
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
// transaction it cancels has the same branch and sent-by.
//
// SIMPLIFIED: RFC 3261 9.2 and 16.10. Only INVITE transactions are
// looked up. A CANCEL for another method SHOULD get 200 and change
// nothing; it gets 481 here, and clients SHOULD NOT send one (9.1). A
// stateful proxy MUST forward a CANCEL that matches nothing
// statelessly, in case it forwarded the INVITE statelessly; a proxy on
// this stack keeps state for every request, so it answers 481 too.
func (s *Stack) answerCancel(e *serverEntry) {
	cancel := e.st.Request
	target, ok := s.servers[serverKey{branch: e.key.branch, sentBy: e.key.sentBy, method: INVITE}]
	if !ok {
		s.stepServer(e, ResponseFromTU{Response: NewResponse(cancel, 481, NewTag())})
		return
	}
	invite := target.machine.(InviteServerState)
	if invite.State == Proceeding {
		select {
		case <-target.st.cancelled:
		default:
			target.st.cancel = cancel
			close(target.st.cancelled)
		}
	}
	s.stepServer(e, ResponseFromTU{Response: NewResponse(cancel, 200, toTag(invite.LastResponse))})
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

// admits tells whether req may go on at once. A request that
// Config.Screen lets in goes to Config.Admit, when there is one, and
// comes back to the loop once Admit has let it in.
func (s *Stack) admits(req *Message, source Destination) bool {
	if s.cfg.Screen != nil && !s.cfg.Screen(req, source) {
		s.logRefused(req, source)
		return false
	}
	if s.cfg.Admit == nil {
		return true
	}
	waiting := s.admitting[source]
	s.admitting[source] = append(waiting, req)
	if len(waiting) == 0 {
		s.askAdmit(req, source)
	}
	return false
}

// askAdmit asks Config.Admit about req off the loop.
func (s *Stack) askAdmit(req *Message, source Destination) {
	go func() {
		s.post(admitDecided{req: req, source: source, admitted: s.cfg.Admit(req, source)})
	}()
}

// receiveAdmitDecision takes up the request that Config.Admit has
// decided on, and then asks Admit about the next request from the same
// source.
func (s *Stack) receiveAdmitDecision(in admitDecided) {
	if in.admitted {
		s.receiveRequest(in.req, in.source, true)
	} else {
		s.logRefused(in.req, in.source)
	}
	waiting := s.admitting[in.source][1:]
	if len(waiting) == 0 {
		delete(s.admitting, in.source)
		return
	}
	s.admitting[in.source] = waiting
	s.askAdmit(waiting[0], in.source)
}

func (s *Stack) logRefused(req *Message, source Destination) {
	s.log.Info("dropped request that the screen refused", "method", string(req.Method), "src", source.String())
}

func (s *Stack) passACKToTU(ack *Message) {
	if s.cfg.OnACK == nil {
		v, _ := ParseVia(ack.Get("Via"))
		s.log.Info("discarded ACK that matches no INVITE transaction", "branch", v.Branch())
		return
	}
	s.passRequestToTU(func() { s.cfg.OnACK(ack) })
}

func (s *Stack) startClient(ct *ClientTransaction) {
	v, _ := ParseVia(ct.Request.Get("Via"))
	e := &clientEntry{
		key:    clientKey{branch: v.Branch(), method: ct.Request.Method},
		ct:     ct,
		timers: map[Timer]armedTimer{},
	}
	s.clients[e.key] = e
	var actions []TransactionAction
	if e.key.method == INVITE {
		e.machine, actions = StartInviteClient(s.cfg.Timers, ct.destination.Transport.reliable())
	} else {
		e.machine, actions = StartNonInviteClient(s.cfg.Timers, ct.destination.Transport.reliable())
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
			if err := s.transport.SendRequest(e.ct.Request, e.ct.destination); err != nil {
				sendErr = err
			}
		case SendACK:
			// RFC 3261 17.1.1.2: the same destination as the INVITE.
			ack := newMatchingRequest(e.ct.Request, ACK, a.Response.Get("To"))
			if err := s.transport.SendRequest(ack, e.ct.destination); err != nil {
				sendErr = err
			}
		case SendCancel:
			// RFC 3261 9.1: the same destination as the INVITE. Nobody
			// reads the CANCEL's own responses; the INVITE's final
			// response tells the TU how the cancel went.
			cancel := newMatchingRequest(e.ct.Request, CANCEL, e.ct.Request.Get("To"))
			addHeaders(cancel, e.cancelHeaders)
			s.startClient(s.newClientTransaction(cancel, e.ct.destination))
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
	final := resp.StatusCode >= 200
	room := cap(e.ct.responses) - len(e.ct.responses)
	if room == 0 || (!final && room == 1) {
		s.log.Warn("dropped response the TU has not read", "status", resp.StatusCode, "branch", e.key.branch)
		return
	}
	e.ct.responses <- resp
	if final && !(e.key.method == INVITE && resp.StatusCode < 300) {
		s.closeTU(e, nil)
	}
}

func (s *Stack) closeTU(e *clientEntry, err error) {
	if e.tuClosed {
		return
	}
	e.ct.err = err
	e.tuClosed = true
	close(e.ct.responses)
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
