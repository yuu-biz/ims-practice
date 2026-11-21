package ue

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Line is the UE's line, which holds its calls. By itself it holds one
// call: an INVITE that comes while the line holds one, or to a UE that
// takes no calls, gets 486 (Busy Here). A Phone lets it hold a second
// call, which waits while the first goes on (TS 24.615 4.5.5.3.2), or
// which the user makes after putting the first on hold; an INVITE that
// comes while the line holds two gets 486, the UDUB of TS 24.615.
//
// An INVITE with Replaces takes the place of a call on the line (RFC
// 3891 3), so it reaches the Phone whatever the line holds.
//
// "ue answer" takes one call and then stays busy until it exits; "ue
// register" and "ue call" take none. "ue serve" frees the line after
// each call, and puts its own calls on it too.
type Line struct {
	takesCalls bool
	calls      chan *sip.IncomingCall

	mu sync.Mutex
	// max is how many calls the line holds at once (takeWaitingCalls).
	max int
	// on are the calls on the line, oldest first.
	on []*lineCall
}

// Direction tells who made a call.
type Direction string

const (
	Outgoing Direction = "outgoing"
	Incoming Direction = "incoming"
)

// CallInfo is a call on the line, as a controller sees it.
type CallInfo struct {
	CallID    string    `json:"call_id"`
	Direction Direction `json:"direction"`
	// Peer is the callee's URI for an outgoing call, and for an incoming
	// one the caller's P-Asserted-Identity, or its From without one. For
	// a conference session it is the conference URI.
	Peer    string    `json:"peer"`
	State   CallState `json:"state"`
	Started time.Time `json:"started"`
	// Waiting tells an incoming call that rings while another call is
	// on the line (TS 24.615 4.5.5.3.2).
	Waiting bool `json:"waiting,omitempty"`
	// Session is the media side of the call: codec, preconditions,
	// hold and DTMF. It is null until the call has a session.
	Session *SessionStatus `json:"session"`
}

// lineCall is a call on the line, with what ends it.
type lineCall struct {
	info CallInfo
	// ic and pickUp are set for an incoming call. A send on pickUp, which
	// holds one, answers the call (AnswerOptions.PickUp).
	ic     *sip.IncomingCall
	pickUp chan time.Time
	// hangUp ends the context of the call: CANCEL or BYE from the caller,
	// BYE from the callee. It is nil until the call runs.
	hangUp context.CancelFunc
	// session is the session of the call, nil until it has one.
	session *Session
	// conference is set for a conference session (phone_conference.go).
	conference *Conference
	// peers are the identities of the peer that the UE knows: those its
	// network asserted, and the URI the UE called. A replacement of the
	// call must come from one of them (Replacement).
	peers []string
}

func NewLine(takesCalls bool) *Line {
	return &Line{takesCalls: takesCalls, max: 1, calls: make(chan *sip.IncomingCall, 2)}
}

// waitingCalls is how many calls a Phone's line holds: the call in
// progress and one more, waiting or held (TS 24.615, GSMA IR.92 2.3.4).
const waitingCalls = 2

// takeWaitingCalls lets the line hold a second call.
func (l *Line) takeWaitingCalls() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.max = waitingCalls
}

// Ring is the UAConfig.OnCall of the UE.
func (l *Line) Ring(ic *sip.IncomingCall) {
	if IsReplacement(ic) && l.takesCalls {
		l.calls <- ic
		return
	}
	peer := ic.Request.Get("P-Asserted-Identity")
	if peer == "" {
		peer = ic.Request.Get("From")
	}
	if na, err := sip.ParseNameAddr(peer); err == nil {
		peer = na.URI
	}
	c := &lineCall{
		info: CallInfo{CallID: ic.Request.Get("Call-ID"), Direction: Incoming, Peer: peer,
			State: CallRinging, Started: time.Now()},
		ic:     ic,
		pickUp: make(chan time.Time, 1),
		peers:  append(AssertedIdentities(ic.Request), peer),
	}
	if !l.takesCalls || !l.seize(c) {
		ic.Reject(486)
		return
	}
	l.calls <- ic
}

// Options is the UAConfig.OnOptions of the UE. An OPTIONS gets the
// status code that an INVITE would get now (RFC 3261 11.2): 486 (Busy
// Here) while the line is full or takes no calls. The UA lists the
// capabilities of the UE in the response itself, the ICSI of MMTel in
// its Contact among them (IR.92 2.2.9).
func (l *Line) Options(*sip.Message) (int, sip.Body) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.takesCalls || len(l.on) >= l.max {
		return 486, sip.Body{}
	}
	return 200, sip.Body{}
}

// Calls delivers each call the line takes, and each INVITE with
// Replaces. A call is on the line from the moment it is delivered.
func (l *Line) Calls() <-chan *sip.IncomingCall { return l.calls }

// seize puts c on the line, unless the line is full. An incoming call
// that comes while another is on the line waits.
func (l *Line) seize(c *lineCall) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.on) >= l.max {
		return false
	}
	c.info.Waiting = c.info.Direction == Incoming && len(l.on) > 0
	l.on = append(l.on, c)
	return true
}

// seizeBeyond puts c on the line whatever it holds: a call that takes
// the place of others, such as a conference session.
func (l *Line) seizeBeyond(c *lineCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = append(l.on, c)
}

// free takes c off the line.
func (l *Line) free(c *lineCall) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.on = slices.DeleteFunc(l.on, func(o *lineCall) bool { return o == c })
}

// current is the call in the foreground and a copy of its CallInfo: the
// newest answered call that is not on hold, or else the newest call. The
// call is nil when the line is free.
func (l *Line) current() (*lineCall, CallInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c := l.activeLocked(); c != nil {
		return c, c.info
	}
	if len(l.on) == 0 {
		return nil, CallInfo{}
	}
	c := l.on[len(l.on)-1]
	return c, c.info
}

// activeLocked is the newest answered call that is not on hold, nil
// when there is none. l.mu is held.
func (l *Line) activeLocked() *lineCall {
	for i := len(l.on) - 1; i >= 0; i-- {
		if c := l.on[i]; c.info.State == CallActive && !c.held() {
			return c
		}
	}
	return nil
}

// active is the answered call that is not on hold, nil when there is
// none.
func (l *Line) active() *lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.activeLocked()
}

// heldCall is the newest answered call on hold, nil when there is none.
func (l *Line) heldCall() *lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.on) - 1; i >= 0; i-- {
		if c := l.on[i]; c.info.State == CallActive && c.held() {
			return c
		}
	}
	return nil
}

// ringing is the newest incoming call that rings, nil when there is
// none.
func (l *Line) ringing() *lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.on) - 1; i >= 0; i-- {
		if c := l.on[i]; c.info.Direction == Incoming && c.info.State == CallRinging {
			return c
		}
	}
	return nil
}

// all are the calls on the line, oldest first.
func (l *Line) all() []*lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.on)
}

// byID is the call on the line with the Call-ID id, nil when there is
// none.
func (l *Line) byID(id string) *lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.on {
		if c.info.CallID == id {
			return c
		}
	}
	return nil
}

// of is the call on the line of the INVITE ic, nil when there is none.
func (l *Line) of(ic *sip.IncomingCall) *lineCall {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.on {
		if c.ic == ic {
			return c
		}
	}
	return nil
}

// infoOf is a copy of the CallInfo of c.
func (l *Line) infoOf(c *lineCall) CallInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return c.info
}

// held tells that the UE has put the call on hold. l.mu is held.
func (c *lineCall) held() bool {
	return c.session != nil && c.session.isHeld()
}

// isHeld tells that c is answered and on hold.
func (l *Line) isHeld(c *lineCall) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return c.info.State == CallActive && c.held()
}

// addPeers adds identities of the peer of c.
func (l *Line) addPeers(c *lineCall, ids ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.peers = append(c.peers, ids...)
}

// peersOf are the identities of the peer of c.
func (l *Line) peersOf(c *lineCall) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(c.peers)
}

// conferenceOf is the conference of the conference session c, nil for
// any other call.
func (l *Line) conferenceOf(c *lineCall) *Conference {
	l.mu.Lock()
	defer l.mu.Unlock()
	return c.conference
}

func (l *Line) setConference(c *lineCall, conf *Conference) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.conference = conf
}

// progress is the Progress of call c.
func (l *Line) progress(c *lineCall, state CallState, callID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.info.State, c.info.CallID = state, callID
	if state == CallActive {
		c.info.Waiting = false
	}
}

func (l *Line) setSession(c *lineCall, s *Session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.session = s
}

// session is the session of the call in the foreground, nil without
// one.
func (l *Line) session() *Session {
	c, _ := l.current()
	if c == nil {
		return nil
	}
	return l.sessionOf(c)
}

// sessionOf is the session of c, nil without one.
func (l *Line) sessionOf(c *lineCall) *Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return c.session
}

func (l *Line) setHangUp(c *lineCall, hangUp context.CancelFunc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c.hangUp = hangUp
}

// hangUp ends the context of call c, if it runs.
func (l *Line) hangUp(c *lineCall) {
	l.mu.Lock()
	hangUp := c.hangUp
	l.mu.Unlock()
	if hangUp != nil {
		hangUp()
	}
}
