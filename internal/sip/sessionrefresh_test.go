package sip

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// handTimers is a UAConfig.AfterFunc that starts no clock: the test
// fires a timer by its duration when it wants the time to have passed.
type handTimers struct {
	mu      sync.Mutex
	pending []*handTimer
}

type handTimer struct {
	d time.Duration
	f func()
}

func (h *handTimers) afterFunc(d time.Duration, f func()) func() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	timer := &handTimer{d: d, f: f}
	h.pending = append(h.pending, timer)
	return func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		i := slices.Index(h.pending, timer)
		if i < 0 {
			return false
		}
		h.pending = slices.Delete(h.pending, i, i+1)
		return true
	}
}

// durations are the durations of the timers that are running.
func (h *handTimers) durations() []time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	var ds []time.Duration
	for _, timer := range h.pending {
		ds = append(ds, timer.d)
	}
	return ds
}

// fire lets the running timer with duration d expire, and returns when
// its function has.
func (h *handTimers) fire(t *testing.T, d time.Duration) {
	t.Helper()
	h.mu.Lock()
	i := slices.IndexFunc(h.pending, func(timer *handTimer) bool { return timer.d == d })
	if i < 0 {
		h.mu.Unlock()
		t.Fatalf("no timer of %v is running", d)
	}
	timer := h.pending[i]
	h.pending = slices.Delete(h.pending, i, i+1)
	h.mu.Unlock()
	timer.f()
}

const (
	halfHour      = 30 * time.Minute
	refreshAfter  = 15 * time.Minute
	expireAfter   = halfHour - 32*time.Second
	sessionHeader = "Session-Expires"
)

// timedCall is an established call between two UAs whose session timers
// the test fires by hand.
type timedCall struct {
	established
	callerTimers, calleeTimers *handTimers
	ok                         *Message
}

func establishTimed(t *testing.T, callerPolicy, calleePolicy SessionTimerPolicy) timedCall {
	t.Helper()
	tc := timedCall{callerTimers: &handTimers{}, calleeTimers: &handTimers{}}
	calls := make(chan *IncomingCall, 1)
	tc.calleeUA = newUAWith(t, UAConfig{Timers: testTimers, SessionTimer: calleePolicy, AfterFunc: tc.calleeTimers.afterFunc,
		OnCall: func(c *IncomingCall) {
			if _, err := c.Accept(answer); err != nil {
				t.Error(err)
			}
			calls <- c
		}}, callee)
	tc.callerUA = newUAWith(t, UAConfig{Timers: testTimers, SessionTimer: callerPolicy, AfterFunc: tc.callerTimers.afterFunc,
		OnCall: noCalls}, "001010000000001")
	out, err := tc.callerUA.Invite(invitation(tc.calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{200}) {
		t.Fatalf("caller got %v, want 200", codes(got))
	}
	tc.out, tc.in, tc.ok = out, <-calls, got[0]
	return tc
}

// TestSessionTimerRefreshesTheSession follows RFC 4028 through a call:
// the caller asks for a session timer in its INVITE, the callee makes
// the caller the refresher in the 2xx, the caller refreshes with an
// UPDATE at half the interval, and each 2xx starts the timers anew.
func TestSessionTimerRefreshesTheSession(t *testing.T) {
	updates := make(chan *Message, 1)
	tc := establishTimed(t, SessionTimerPolicy{Interval: halfHour}, SessionTimerPolicy{})

	// RFC 4028 7.1: timer in Supported, and the interval without a
	// refresher.
	if invite := tc.in.Request; !invite.Supports(OptionTimer) || invite.Get(sessionHeader) != "1800" {
		t.Errorf("INVITE has Supported %q and Session-Expires %q, want timer and 1800", invite.GetAll("Supported"), invite.Get(sessionHeader))
	}
	// RFC 4028 9: the UAS copies the interval, names the refresher, and
	// requires the extension so that the UAC reads the answer.
	if tc.ok.Get(sessionHeader) != "1800;refresher=uac" || !tc.ok.Requires(OptionTimer) {
		t.Errorf("200 has Session-Expires %q and Require %q, want 1800;refresher=uac and timer", tc.ok.Get(sessionHeader), tc.ok.GetAll("Require"))
	}
	// RFC 4028 7.2 and 10: the refresher refreshes at half the interval;
	// both sides give up 32 s before its end.
	if got := tc.callerTimers.durations(); !reflect.DeepEqual(got, []time.Duration{refreshAfter, expireAfter}) {
		t.Errorf("caller runs timers of %v, want the refresh and the expiration", got)
	}
	if got := tc.calleeTimers.durations(); !reflect.DeepEqual(got, []time.Duration{expireAfter}) {
		t.Errorf("callee runs timers of %v, want the expiration alone", got)
	}

	tc.in.Handle(func(r *InDialogRequest) {
		updates <- r.Request
		r.Accept(Body{})
	})
	tc.callerTimers.fire(t, refreshAfter)
	update := <-updates
	// RFC 4028 7.4: an UPDATE without a session description, with the
	// interval and the roles as they are.
	if update.Method != UPDATE || len(update.Body) != 0 || update.Get(sessionHeader) != "1800;refresher=uac" || !update.Supports(OptionTimer) {
		t.Errorf("refresh is\n%s\nwant an UPDATE without a body with Session-Expires 1800;refresher=uac", update)
	}
	// The 2xx started every timer anew: the fired refresh is back, and
	// nothing runs twice.
	if got := tc.callerTimers.durations(); !reflect.DeepEqual(got, []time.Duration{refreshAfter, expireAfter}) {
		t.Errorf("after the refresh the caller runs timers of %v, want one refresh and one expiration", got)
	}
	if got := tc.calleeTimers.durations(); !reflect.DeepEqual(got, []time.Duration{expireAfter}) {
		t.Errorf("after the refresh the callee runs timers of %v, want one expiration", got)
	}

	// RFC 4028 10: without another refresh, the callee ends the session
	// with a BYE.
	tc.calleeTimers.fire(t, expireAfter)
	waitDone(t, tc.out.Done(), "caller")
	waitDone(t, tc.in.Done(), "callee")
	if !errors.Is(tc.in.Err(), ErrSessionExpired) {
		t.Errorf("callee's call ended with %v, want ErrSessionExpired", tc.in.Err())
	}
	if bye := tc.out.EndedBy(); bye == nil || bye.Method != BYE {
		t.Errorf("caller's call was ended by %v, want the callee's BYE", bye)
	}
	if got := append(tc.callerTimers.durations(), tc.calleeTimers.durations()...); len(got) != 0 {
		t.Errorf("timers of %v still run after the call", got)
	}
}

// RFC 4028 9: when the caller asks for no session timer, the callee may
// ask for one in its 2xx.
func TestCalleeAsksForTheSessionTimer(t *testing.T) {
	tc := establishTimed(t, SessionTimerPolicy{}, SessionTimerPolicy{Interval: halfHour})
	if tc.in.Request.Get(sessionHeader) != "" {
		t.Errorf("INVITE has Session-Expires %q, want none", tc.in.Request.Get(sessionHeader))
	}
	if tc.ok.Get(sessionHeader) != "1800;refresher=uac" {
		t.Errorf("200 has Session-Expires %q, want 1800;refresher=uac", tc.ok.Get(sessionHeader))
	}
	if got := tc.callerTimers.durations(); !reflect.DeepEqual(got, []time.Duration{refreshAfter, expireAfter}) {
		t.Errorf("caller runs timers of %v, want the refresh and the expiration", got)
	}
}

// Without a session timer on either side, nothing runs.
func TestNoSessionTimerByDefault(t *testing.T) {
	tc := establishTimed(t, SessionTimerPolicy{}, SessionTimerPolicy{})
	if tc.ok.Get(sessionHeader) != "" || tc.ok.Requires(OptionTimer) {
		t.Errorf("200 has Session-Expires %q and Require %q, want neither", tc.ok.Get(sessionHeader), tc.ok.GetAll("Require"))
	}
	if got := append(tc.callerTimers.durations(), tc.calleeTimers.durations()...); len(got) != 0 {
		t.Errorf("timers of %v run for a session that does not expire", got)
	}
}

// RFC 4028 7.3 and 7.4: a 422 names the smallest interval that the UAS
// takes, and the UAC sends its INVITE again with that Min-SE, the same
// Call-ID and the next CSeq number. The user of the call sees only how
// the second INVITE ends.
func TestINVITEGoesOutAgainAfterA422(t *testing.T) {
	invites := make(chan *Message, 1)
	hour := time.Hour
	calleeUA := newUAWith(t, UAConfig{Timers: testTimers, SessionTimer: SessionTimerPolicy{MinSE: hour}, AfterFunc: (&handTimers{}).afterFunc,
		OnCall: func(c *IncomingCall) {
			invites <- c.Request
			c.Accept(answer)
		}}, callee)
	callerTimers := &handTimers{}
	callerUA := newUAWith(t, UAConfig{Timers: testTimers, SessionTimer: SessionTimerPolicy{Interval: halfHour}, AfterFunc: callerTimers.afterFunc,
		OnCall: noCalls}, "001010000000001")
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{200}) {
		t.Fatalf("caller got %v, want the 200 to the second INVITE alone", codes(got))
	}
	second := <-invites
	if second.Get("Call-ID") != out.Request.Get("Call-ID") || tagOf(second.Get("From")) != tagOf(out.Request.Get("From")) ||
		second.Get("CSeq") != "2 INVITE" || second.Get("Min-SE") != "3600" || second.Get(sessionHeader) != "3600" {
		t.Errorf("second INVITE has Call-ID %q, CSeq %q, Min-SE %q and Session-Expires %q; want the first one's Call-ID, 2 INVITE, 3600 and 3600",
			second.Get("Call-ID"), second.Get("CSeq"), second.Get("Min-SE"), second.Get(sessionHeader))
	}
	if got[0].Get(sessionHeader) != "3600;refresher=uac" {
		t.Errorf("200 has Session-Expires %q, want 3600;refresher=uac", got[0].Get(sessionHeader))
	}
	if d, _ := out.Dialog(); d.LocalSeq != 2 || d.State != DialogConfirmed {
		t.Errorf("caller's dialog has local seq %d in state %s, want 2 and Confirmed", d.LocalSeq, d.State)
	}
	if got := callerTimers.durations(); !reflect.DeepEqual(got, []time.Duration{hour / 2, hour - 32*time.Second}) {
		t.Errorf("caller runs timers of %v, want those of an hour's interval", got)
	}
	if err := out.Bye(); err != nil {
		t.Errorf("Bye: %v", err)
	}
}

// RFC 4028 9 and 7.4: a caller that does not support the session timer
// leaves the refreshes to the callee, here on behalf of a proxy that
// asked for the timer. That caller allows no UPDATE, so the callee
// refreshes with a re-INVITE that offers its session description again.
func TestCalleeRefreshesWithAReINVITE(t *testing.T) {
	timers := &handTimers{}
	calleeUA := newUAWith(t, UAConfig{Timers: testTimers, AfterFunc: timers.afterFunc,
		OnCall: func(c *IncomingCall) { c.Accept(answer) }}, callee)
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add(sessionHeader, "1800")
	offer.addTo(invite)
	send(t, peer, invite, calleeUA.Stack())
	ok := readSkippingTrying(t, peer)
	if ok.StatusCode != 200 || ok.Get(sessionHeader) != "1800;refresher=uas" || ok.Requires(OptionTimer) {
		t.Fatalf("got %s with Session-Expires %q and Require %q, want 200 with 1800;refresher=uas and no Require",
			startLine(ok), ok.Get(sessionHeader), ok.GetAll("Require"))
	}
	send(t, peer, rawACK(peer, invite, ok), calleeUA.Stack())
	// The callee has to see the ACK before it may send an INVITE (RFC
	// 3261 14.1).
	deadline := time.Now().Add(2 * time.Second)
	for !reflect.DeepEqual(timers.durations(), []time.Duration{refreshAfter, expireAfter}) || !ackSeen(calleeUA, invite, ok) {
		if time.Now().After(deadline) {
			t.Fatalf("callee runs timers of %v, want the refresh and the expiration", timers.durations())
		}
		time.Sleep(5 * time.Millisecond)
	}

	done := make(chan struct{})
	go func() {
		timers.fire(t, refreshAfter)
		close(done)
	}()
	var refresh *Message
	for refresh == nil {
		if m := readDatagram(t, peer); m.Method == INVITE {
			refresh = m
		}
	}
	// RFC 4028 7.4: the sender of the refresh is its UAC, and stays the
	// refresher.
	if string(refresh.Body) != string(answer.Content) || refresh.Get(sessionHeader) != "1800;refresher=uac" {
		t.Errorf("refresh is\n%s\nwant a re-INVITE with the callee's session description and Session-Expires 1800;refresher=uac", refresh)
	}
	resp := NewResponse(refresh, 200, "")
	resp.Add("Contact", invite.Get("Contact"))
	resp.Add(sessionHeader, "1800;refresher=uac")
	offer.addTo(resp)
	send(t, peer, resp, calleeUA.Stack())
	if ack := readDatagram(t, peer); ack.Method != ACK {
		t.Fatalf("got %s, want the ACK for the refresh", startLine(ack))
	}
	<-done
	if got := timers.durations(); !reflect.DeepEqual(got, []time.Duration{refreshAfter, expireAfter}) {
		t.Errorf("after the refresh the callee runs timers of %v, want one refresh and one expiration", got)
	}
}

// ackSeen reports whether the UA has taken the ACK for ok.
func ackSeen(ua *UA, invite, ok *Message) bool {
	d, found := ua.dialog(DialogID{CallID: invite.Get("Call-ID"), LocalTag: tagOf(ok.Get("To")), RemoteTag: tagOf(invite.Get("From"))})
	if !found {
		return false
	}
	d.c.mu.Lock()
	defer d.c.mu.Unlock()
	return !d.awaitsACK()
}

// RFC 4028 9: within the dialog too, a session interval below the UA's
// minimum gets 422 with Min-SE, and the session timer stays as it was.
func TestRefreshWithAnIntervalTooSmallGets422(t *testing.T) {
	tc := establishTimed(t, SessionTimerPolicy{Interval: halfHour}, SessionTimerPolicy{MinSE: 10 * time.Minute})
	resp, err := tc.out.Update(Body{})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("refresh with the current interval got %v, %v; want 200", resp, err)
	}
	// The caller's UA never asks for less than it has, so a raw UPDATE
	// plays a peer that does.
	d, _ := tc.out.Dialog()
	small := d.NewRequest(UPDATE)
	small.Set("CSeq", "9 UPDATE")
	small.Add("Supported", "timer")
	small.Add(sessionHeader, "300")
	if resp := sendInDialog(t, tc.established, small); resp.StatusCode != 422 || resp.Get("Min-SE") != "600" {
		t.Errorf("UPDATE with Session-Expires 300 got %s with Min-SE %q, want 422 with 600", startLine(resp), resp.Get("Min-SE"))
	}
	if got := tc.calleeTimers.durations(); !reflect.DeepEqual(got, []time.Duration{expireAfter}) {
		t.Errorf("after the 422 the callee runs timers of %v, want its expiration as before", got)
	}
}

// RFC 3261 14.1 and RFC 4028 10: a refresh that meets a 491 goes out
// again after the glare delay of the UA that owns the Call-ID.
func TestRefreshWaitsOutA491(t *testing.T) {
	tc := establishTimed(t, SessionTimerPolicy{Interval: halfHour}, SessionTimerPolicy{})
	seen := make(chan string, 2)
	var refused atomic.Bool
	tc.in.Handle(func(r *InDialogRequest) {
		seen <- r.Request.Get("CSeq")
		if refused.CompareAndSwap(false, true) {
			r.Reject(491)
			return
		}
		r.Accept(Body{})
	})
	tc.callerTimers.fire(t, refreshAfter)
	if cseq := <-seen; cseq != "2 UPDATE" {
		t.Fatalf("first refresh has CSeq %q, want 2 UPDATE", cseq)
	}
	var glare time.Duration
	for _, d := range tc.callerTimers.durations() {
		if d != expireAfter {
			glare = d
		}
	}
	if glare < 2100*time.Millisecond || glare > 4*time.Second {
		t.Fatalf("caller runs timers of %v, want the expiration and a retry after 2.1 to 4 s", tc.callerTimers.durations())
	}
	tc.callerTimers.fire(t, glare)
	if cseq := <-seen; cseq != "3 UPDATE" {
		t.Errorf("second refresh has CSeq %q, want 3 UPDATE", cseq)
	}
	if got := tc.callerTimers.durations(); !reflect.DeepEqual(got, []time.Duration{refreshAfter, expireAfter}) {
		t.Errorf("after the refresh the caller runs timers of %v, want one refresh and one expiration", got)
	}
}
