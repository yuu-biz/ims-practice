package diameter

import (
	"slices"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"
)

// This file is the peer state machine of RFC 6733 5.6, merged with the
// watchdog algorithm of RFC 3539 Appendix A. Transition is a pure
// function: it never touches the network or a clock. The I/O loop in
// peer.go feeds it events and carries out the actions it returns.
//
// SIMPLIFIED: RFC 6733 5.6.4 (election). Only the configured initiator
// connects, so a node never holds an initiator and a responder
// connection to the same peer at once. The Wait-Conn-Ack/Elect and
// Wait-Returns states are therefore left out.

// Role tells which side opened the transport connection. RFC 6733 5.6
// writes it as the I- and R- prefixes.
type Role int

const (
	Initiator Role = iota
	Responder
)

// Phase is the state of the RFC 6733 5.6 table.
type Phase int

const (
	Closed Phase = iota
	WaitConnAck
	WaitICEA
	Open // I-Open or R-Open, depending on Role
	Closing
)

// WatchdogStatus is the watchdog state of RFC 3539 Appendix A while the
// connection is open. OKAY with Pending=FALSE and OKAY with
// Pending=TRUE are separate values here, so the pair cannot disagree.
//
// SIMPLIFIED: RFC 3539 Appendix A REOPEN. After a reconnect the RFC
// sends DWRs and waits for three DWAs before it puts the connection
// back into service. Here a reconnected peer goes straight to OKAY, the
// same as on first open. A request that such a peer leaves unanswered
// goes on to the next peer of its PeerGroup, so a peer that is not yet
// steady delays requests rather than losing them.
type WatchdogStatus int

const (
	WatchdogOkay    WatchdogStatus = iota // OKAY, no DWR outstanding
	WatchdogPending                       // OKAY, a DWR is outstanding
	WatchdogSuspect                       // SUSPECT, a DWR is outstanding
)

// State is everything the state machine remembers about one peer.
type State struct {
	Role     Role
	Phase    Phase
	Watchdog WatchdogStatus // meaningful only in Open
	// PeerHost is the peer's Origin-Host. The initiator knows it from
	// its configuration and checks it against the CEA. The responder
	// learns it from the CER.
	PeerHost string
	// Reconnect tells an initiator to wait Tc and connect again once it
	// reaches Closed (RFC 6733 2.1). A local Stop, or a DPR whose
	// Disconnect-Cause asks the peer not to reconnect, clears it.
	Reconnect bool
	// Applications are the Application-Ids this node supports. The peer
	// must advertise at least one of them (RFC 6733 5.3).
	Applications []uint32
}

// TakesRequests says whether a request may go out in s: the connection
// is open, and not SUSPECT (RFC 3539 3.4.1 [3]). Transition refuses one
// otherwise.
func (s State) TakesRequests() bool { return s.Phase == Open && s.Watchdog != WatchdogSuspect }

// NewInitiatorState is the state of a peer that this node connects to.
func NewInitiatorState(peerHost string, applications []uint32) State {
	return State{Role: Initiator, Phase: Closed, PeerHost: peerHost, Reconnect: true, Applications: applications}
}

// NewResponderState is the state of a peer that connected to this node
// and has not yet been sent a CEA.
func NewResponderState(applications []uint32) State {
	return State{Role: Responder, Phase: Closed, Applications: applications}
}

func (s State) String() string {
	switch s.Phase {
	case Closed:
		return "Closed"
	case WaitConnAck:
		return "Wait-Conn-Ack"
	case WaitICEA:
		return "Wait-I-CEA"
	case Open:
		if s.Role == Initiator {
			return "I-Open"
		}
		return "R-Open"
	case Closing:
		return "Closing"
	}
	return "unknown"
}

func (w WatchdogStatus) String() string {
	switch w {
	case WatchdogOkay:
		return "OKAY"
	case WatchdogPending:
		return "OKAY/Pending"
	case WatchdogSuspect:
		return "SUSPECT"
	}
	return "unknown"
}

// MessageIDs are the header fields an answer copies from its request
// (RFC 6733 3).
type MessageIDs struct {
	HopByHop uint32
	EndToEnd uint32
}

// Event is an input to the state machine. The names follow RFC 6733
// 5.6.2 without the I- and R- prefixes.
type Event interface{ eventName() string }

type (
	// Start asks an initiator to open the connection.
	Start struct{}
	// RcvConnAck reports that the TCP connection is up.
	RcvConnAck struct{}
	// RcvConnNack reports that the TCP connection could not be opened.
	RcvConnNack struct{}
	// RConnCER is a new incoming connection together with its first
	// message, a CER (RFC 6733 5.6.1).
	RConnCER struct {
		Request        MessageIDs
		OriginHost     string
		ApplicationIDs []uint32
	}
	RcvCEA struct {
		ResultCode     uint32
		OriginHost     string
		ApplicationIDs []uint32
	}
	RcvDWR struct{ Request MessageIDs }
	RcvDWA struct{}
	RcvDPR struct {
		Request MessageIDs
		Cause   DisconnectCause
	}
	RcvDPA struct{}
	// RcvMessage is any other message, such as a Cx request or answer.
	RcvMessage struct{ Message *diam.Message }
	// SendMessage asks to send an application message: a request from
	// the application, or the answer the handler made for a received
	// request.
	SendMessage struct{ Outgoing *outgoing }
	// PeerDisc reports that the peer closed the transport connection.
	PeerDisc struct{}
	// Timeout reports that the single timer set by SetTimer expired.
	Timeout struct{}
	// Stop asks to close the connection for good, e.g. on SIGTERM.
	Stop struct{}
)

func (Start) eventName() string       { return "Start" }
func (RcvConnAck) eventName() string  { return "Rcv-Conn-Ack" }
func (RcvConnNack) eventName() string { return "Rcv-Conn-Nack" }
func (RConnCER) eventName() string    { return "R-Conn-CER" }
func (RcvCEA) eventName() string      { return "Rcv-CEA" }
func (RcvDWR) eventName() string      { return "Rcv-DWR" }
func (RcvDWA) eventName() string      { return "Rcv-DWA" }
func (RcvDPR) eventName() string      { return "Rcv-DPR" }
func (RcvDPA) eventName() string      { return "Rcv-DPA" }
func (RcvMessage) eventName() string  { return "Rcv-Message" }
func (SendMessage) eventName() string { return "Send-Message" }
func (PeerDisc) eventName() string    { return "Peer-Disc" }
func (Timeout) eventName() string     { return "Timeout" }
func (Stop) eventName() string        { return "Stop" }

// Action is an output of the state machine for the I/O loop to carry out.
type Action interface{ isAction() }

// Timer names the duration that SetTimer starts. A peer has one timer;
// setting it replaces the previous one.
type Timer int

const (
	TimerWatchdog  Timer = iota // Tw, RFC 3539 3.4.1
	TimerAnswer                 // waiting for a CEA, a DPA, or a disconnect
	TimerReconnect              // Tc, RFC 6733 2.1
)

type (
	Connect struct{}
	SendCER struct{}
	SendCEA struct {
		Request    MessageIDs
		ResultCode uint32
	}
	SendDWR struct{}
	SendDWA struct{ Request MessageIDs }
	SendDPR struct{ Cause DisconnectCause }
	SendDPA struct{ Request MessageIDs }
	// Disconnect closes the transport connection. It covers the Disc,
	// Error and Cleanup actions of RFC 6733 5.6.3.
	Disconnect struct{}
	SetTimer   struct{ Timer Timer }
	// Process hands a received application message to the node: a
	// request to the handler, an answer to the request waiting for it.
	Process struct{ Message *diam.Message }
	// SndMessage writes an application message to the connection.
	SndMessage struct{ Outgoing *outgoing }
	// RefuseMessage tells the application that its message cannot be
	// sent now. RFC 6733 5.6 lists Send-Message only for the open
	// states, so the table has no name for this.
	RefuseMessage struct{ Outgoing *outgoing }
	// Finish ends the peer. Nothing more happens on it.
	Finish struct{}
)

func (Connect) isAction()       {}
func (SendCER) isAction()       {}
func (SendCEA) isAction()       {}
func (SendDWR) isAction()       {}
func (SendDWA) isAction()       {}
func (SendDPR) isAction()       {}
func (SendDPA) isAction()       {}
func (Disconnect) isAction()    {}
func (SetTimer) isAction()      {}
func (Process) isAction()       {}
func (SndMessage) isAction()    {}
func (RefuseMessage) isAction() {}
func (Finish) isAction()        {}

// Transition returns the next state and the actions to carry out, in
// order. An event that has no row for the current state is ignored.
func Transition(s State, e Event) (State, []Action) {
	if send, ok := e.(SendMessage); ok && s.Phase != Open {
		return s, []Action{RefuseMessage{Outgoing: send.Outgoing}}
	}
	switch s.Phase {
	case Closed:
		return fromClosed(s, e)
	case WaitConnAck:
		return fromWaitConnAck(s, e)
	case WaitICEA:
		return fromWaitICEA(s, e)
	case Open:
		return fromOpen(s, e)
	case Closing:
		return fromClosing(s, e)
	}
	return s, nil
}

func fromClosed(s State, e Event) (State, []Action) {
	switch e := e.(type) {
	case Start, Timeout:
		// Timeout here is the Tc timer: try the connection again.
		if s.Role == Initiator {
			s.Phase = WaitConnAck
			return s, []Action{Connect{}}
		}
	case RConnCER:
		if s.Role != Responder {
			return s, nil
		}
		// Local policy accepts a CER from any Origin-Host, which RFC
		// 6733 5.3 allows. The peer entry lives as long as the
		// connection.
		s.PeerHost = e.OriginHost
		if !hasCommonApplication(s.Applications, e.ApplicationIDs) {
			// RFC 6733 5.3: answer DIAMETER_NO_COMMON_APPLICATION and
			// disconnect.
			s.Reconnect = false
			return s, []Action{
				SendCEA{Request: e.Request, ResultCode: DiameterNoCommonApplication},
				Disconnect{},
				Finish{},
			}
		}
		s.Phase = Open
		s.Watchdog = WatchdogOkay
		return s, []Action{
			SendCEA{Request: e.Request, ResultCode: DiameterSuccess},
			SetTimer{Timer: TimerWatchdog},
		}
	case Stop:
		s.Reconnect = false
		return s, []Action{Finish{}}
	}
	return s, nil
}

func fromWaitConnAck(s State, e Event) (State, []Action) {
	switch e.(type) {
	case RcvConnAck:
		s.Phase = WaitICEA
		return s, []Action{SendCER{}, SetTimer{Timer: TimerAnswer}}
	case RcvConnNack:
		return closed(s, nil)
	case Stop:
		s.Reconnect = false
		return closed(s, nil)
	}
	return s, nil
}

func fromWaitICEA(s State, e Event) (State, []Action) {
	switch e := e.(type) {
	case RcvCEA:
		if e.ResultCode != DiameterSuccess ||
			!strings.EqualFold(e.OriginHost, s.PeerHost) ||
			!hasCommonApplication(s.Applications, e.ApplicationIDs) {
			return closed(s, []Action{Disconnect{}})
		}
		s.Phase = Open
		s.Watchdog = WatchdogOkay
		return s, []Action{SetTimer{Timer: TimerWatchdog}}
	case RConnCER, RcvDWR, RcvDWA, RcvDPR, RcvDPA, RcvMessage:
		// I-Rcv-Non-CEA: anything but a CEA is an error here.
		return closed(s, []Action{Disconnect{}})
	case PeerDisc, Timeout:
		return closed(s, []Action{Disconnect{}})
	case Stop:
		s.Reconnect = false
		return closed(s, []Action{Disconnect{}})
	}
	return s, nil
}

func fromOpen(s State, e Event) (State, []Action) {
	switch e := e.(type) {
	case RcvDWR:
		s.Watchdog = s.Watchdog.received(false)
		return s, []Action{SendDWA{Request: e.Request}, SetTimer{Timer: TimerWatchdog}}
	case RcvDWA:
		s.Watchdog = s.Watchdog.received(true)
		return s, []Action{SetTimer{Timer: TimerWatchdog}}
	case RcvMessage:
		// Any message counts as activity for the watchdog (RFC 3539
		// 3.4.1 [2]).
		s.Watchdog = s.Watchdog.received(false)
		return s, []Action{Process{Message: e.Message}, SetTimer{Timer: TimerWatchdog}}
	case RConnCER, RcvCEA, RcvDPA:
		// A second CER or an unexpected CEA or DPA has no meaning on an
		// open connection. It still proves the peer is alive.
		s.Watchdog = s.Watchdog.received(false)
		return s, []Action{SetTimer{Timer: TimerWatchdog}}
	case SendMessage:
		// RFC 3539 3.4.1 [3]: a SUSPECT connection takes no new
		// requests. Answers still go out, since the peer is waiting.
		if s.Watchdog == WatchdogSuspect && e.Outgoing.isRequest() {
			return s, []Action{RefuseMessage{Outgoing: e.Outgoing}}
		}
		return s, []Action{SndMessage{Outgoing: e.Outgoing}}
	case RcvDPR:
		s.Phase = Closing
		// RFC 6733 5.4.3: only REBOOTING invites the peer to reconnect.
		//
		// SIMPLIFIED: RFC 6733 5.4.3. A receiver of BUSY or
		// DO_NOT_WANT_TO_TALK_TO_YOU should not attempt to reconnect,
		// and the clause sets no time after which it may. Here the
		// initiator never connects to that peer again until its process
		// starts anew, however long the peer has been ready. The nodes
		// of this project send REBOOTING only, so the case comes up only
		// with a peer of another implementation.
		s.Reconnect = s.Reconnect && e.Cause == Rebooting
		return s, []Action{SendDPA{Request: e.Request}, SetTimer{Timer: TimerAnswer}}
	case Timeout:
		return watchdogExpired(s)
	case PeerDisc:
		return closed(s, []Action{Disconnect{}})
	case Stop:
		s.Phase = Closing
		s.Reconnect = false
		return s, []Action{SendDPR{Cause: Rebooting}, SetTimer{Timer: TimerAnswer}}
	}
	return s, nil
}

// watchdogExpired is OnTimerElapsed of RFC 3539 Appendix A for an open
// connection. The connection is closed only after Tw has expired twice
// with the DWR unanswered (RFC 3539 3.4.1 [4]).
func watchdogExpired(s State) (State, []Action) {
	switch s.Watchdog {
	case WatchdogOkay:
		s.Watchdog = WatchdogPending
		return s, []Action{SendDWR{}, SetTimer{Timer: TimerWatchdog}}
	case WatchdogPending:
		// SIMPLIFIED: RFC 3539 3.4.1 [3] failover. Pending requests
		// should move to an alternate peer here. SUSPECT refuses new
		// requests, but the pending ones stay and wait for their answer
		// or their timeout. A PeerGroup sends such a request to the next
		// peer once it has timed out, so it reaches an alternate later
		// than RFC 3539 asks, and never before its own wait is over.
		s.Watchdog = WatchdogSuspect
		return s, []Action{SetTimer{Timer: TimerWatchdog}}
	}
	return closed(s, []Action{Disconnect{}})
}

// received is OnReceive of RFC 3539 Appendix A: any message proves the
// peer is alive, and a DWA also answers the outstanding DWR.
func (w WatchdogStatus) received(isDWA bool) WatchdogStatus {
	if isDWA || w == WatchdogOkay {
		return WatchdogOkay
	}
	return WatchdogPending
}

func fromClosing(s State, e Event) (State, []Action) {
	switch e.(type) {
	case RcvDPA, PeerDisc, Timeout:
		// RFC 6733 5.4: the receiver of the DPA closes the transport.
		// The sender of the DPA waits for that close, bounded by the
		// timer.
		return closed(s, []Action{Disconnect{}})
	case Stop:
		s.Reconnect = false
	}
	return s, nil
}

// closed moves to Closed. An initiator that should reconnect starts Tc;
// every other peer finishes.
func closed(s State, actions []Action) (State, []Action) {
	s.Phase = Closed
	s.Watchdog = WatchdogOkay
	if s.Role == Initiator && s.Reconnect {
		return s, append(actions, SetTimer{Timer: TimerReconnect})
	}
	return s, append(actions, Finish{})
}

// hasCommonApplication reports whether the peer advertised one of the
// applications this node supports. RFC 6733 5.3: the Vendor-Id inside a
// Vendor-Specific-Application-Id is not used, and a relay counts as
// supporting every application (RFC 6733 2.4).
func hasCommonApplication(local, peerApplicationIDs []uint32) bool {
	if slices.Contains(peerApplicationIDs, applicationIDRelay) {
		return true
	}
	return slices.ContainsFunc(local, func(id uint32) bool { return slices.Contains(peerApplicationIDs, id) })
}
