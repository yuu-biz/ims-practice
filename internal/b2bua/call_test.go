package b2bua_test

import (
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// A whole call through the Call: the callee's reliable 183 with the
// answer reaches the caller, whose PRACK goes back to it (RFC 3262 4);
// then the 200 and its ACK, a request of each side within the dialog,
// a re-INVITE without an offer, whose 2xx has the offer and its ACK the
// answer (RFC 3261 14.1), and the BYE with its Reason (RFC 3326 2).
// Each message reaches the other side with what the hooks gave for
// its way, and the role gets the dialogs after the answer and after
// each request within them.
func TestCallThroughTheB2BUA(t *testing.T) {
	h := &hooks{}
	fromCaller := make(chan *sip.InDialogRequest, 4)
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		in.Handle(func(r *sip.InDialogRequest) { fromCaller <- r })
		calls <- in
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	fromCallee := make(chan *sip.InDialogRequest, 4)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) { fromCallee <- r }))
	if err != nil {
		t.Fatal(err)
	}

	in := within(t, "INVITE at the callee", calls)
	if string(in.Request.Body) != string(offer.Content) {
		t.Errorf("the INVITE reached the callee with %q, want the offer", in.Request.Body)
	}
	progressed := make(chan error, 1)
	go func() { progressed <- in.Progress(183, answer) }()
	prov, ok := <-out.Responses()
	if !ok || prov.StatusCode != 183 || !prov.Requires(sip.Option100rel) || string(prov.Body) != string(answer.Content) {
		t.Fatalf("the caller got %v, want a reliable 183 with the answer", prov)
	}
	if got := prov.Get("Subject"); got != "toward the caller" {
		t.Errorf("the 183 has Subject %q, want the hooks' toward the caller", got)
	}
	if err := within(t, "PRACK at the callee", progressed); err != nil {
		t.Fatalf("the callee's 183 got no PRACK: %v", err)
	}
	go in.Accept(sip.Body{})
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}

	info := sip.Body{Type: "application/dtmf-relay", Content: []byte("Signal=5\r\nDuration=160\r\n")}
	sent := make(chan *sip.Message, 1)
	go func() {
		resp, _ := out.Send("INFO", info)
		sent <- resp
	}()
	r := within(t, "INFO at the callee", fromCaller)
	if r.Request.Method != "INFO" || string(r.Request.Body) != string(info.Content) || r.Request.Get("Subject") != "toward the callee" {
		t.Errorf("the callee got %s with %q and Subject %q, want the INFO toward the callee", r.Request.Method, r.Request.Body, r.Request.Get("Subject"))
	}
	r.Accept(sip.Body{})
	if resp := within(t, "200 to the INFO", sent); resp == nil || resp.StatusCode != 200 || resp.Get("Subject") != "toward the caller" {
		t.Errorf("the INFO got %v, want the callee's 200 toward the caller", resp)
	}

	reinvited := make(chan error, 1)
	go func() {
		resp, err := in.ReInvite(sip.Body{})
		if err == nil && (resp.StatusCode != 200 || string(resp.Body) != string(offer.Content)) {
			err = errors.New("the 2xx to the re-INVITE has no offer of the caller")
		}
		if err == nil {
			err = in.Ack(answer)
		}
		reinvited <- err
	}()
	r = within(t, "re-INVITE at the caller", fromCallee)
	if r.Request.Method != sip.INVITE || r.Offer || r.Request.Get("Subject") != "toward the caller" {
		t.Fatalf("the caller got %s with an offer %t and Subject %q, want the re-INVITE without one", r.Request.Method, r.Offer, r.Request.Get("Subject"))
	}
	ack, err := r.Accept(offer)
	if err != nil {
		t.Fatalf("no ACK for the 2xx with the offer: %v", err)
	}
	if string(ack.Body) != string(answer.Content) {
		t.Errorf("the caller's ACK has %q, want the callee's answer", ack.Body)
	}
	if err := within(t, "end of the re-INVITE", reinvited); err != nil {
		t.Fatal(err)
	}

	// Answered, then after the INFO and the re-INVITE. The core hands the
	// dialogs over after it has answered, so the last time may come just
	// after the caller has its ACK.
	if !until(5*time.Second, func() bool { return h.keptCount() >= 3 }) {
		t.Errorf("the role got the dialogs %d times within 5 s, want at least 3", h.keptCount())
	}
	reason := sip.NewReason(sip.ReasonSIP, 200, "Call completed elsewhere").Header()
	if err := out.Bye(reason); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	select {
	case <-in.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the callee's call did not end")
	}
	if got := sip.ReasonFields(in.EndedBy()); len(got) != 1 || got[0] != reason {
		t.Errorf("the callee's BYE has Reason %v, want %v", got, reason)
	}
}

// The caller hangs up before the answer: the Call cancels the INVITE of
// the callee, with the Reason of the caller's CANCEL (RFC 3261 9.1, RFC
// 3326 2).
func TestCancelThroughTheB2BUA(t *testing.T) {
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		calls <- in
		in.Progress(180, sip.Body{})
	})
	b2b := newB2BUA(t, &hooks{}, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "INVITE at the callee", calls)
	if prov := within(t, "180 at the caller", out.Responses()); prov.StatusCode != 180 {
		t.Fatalf("the caller got %d, want 180", prov.StatusCode)
	}
	reason := sip.NewReason(sip.ReasonSIP, 600, "Busy Everywhere").Header()
	if err := out.Cancel(reason); err != nil {
		t.Fatal(err)
	}
	select {
	case <-in.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the callee's call did not end")
	}
	if !errors.Is(in.Err(), sip.ErrCancelled) {
		t.Errorf("the callee's call ended with %v, want a CANCEL", in.Err())
	}
	if got := sip.ReasonFields(in.EndedBy()); len(got) != 1 || got[0] != reason {
		t.Errorf("the callee's CANCEL has Reason %v, want %v", got, reason)
	}
}

// A request that the role refuses gets the role's response, and the
// other side never sees it.
func TestRoleRefusesARequest(t *testing.T) {
	h := &hooks{request: func(req *sip.Message, _ b2bua.Direction) *b2bua.Refusal {
		if req.Method == "INFO" {
			return &b2bua.Refusal{Status: 403, Headers: []sip.HeaderField{{Name: "Warning", Value: `399 b2bua "Not here"`}}}
		}
		return nil
	}}
	fromCaller := make(chan *sip.InDialogRequest, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) {
		in.Handle(func(r *sip.InDialogRequest) { fromCaller <- r })
		in.Accept(answer)
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	resp, err := out.Send("INFO", sip.Body{})
	if err != nil || resp.StatusCode != 403 || resp.Get("Warning") != `399 b2bua "Not here"` {
		t.Errorf("the INFO got %v %v, want the role's 403 with its Warning", resp, err)
	}
	select {
	case r := <-fromCaller:
		t.Errorf("the callee got the %s that the role refused", r.Request.Method)
	case <-time.After(5 * timers.T1):
	}
	out.Bye()
}

// A request of the callee that crosses its 2xx and the caller's ACK goes
// on to the caller in its early dialog, but the role gets the dialogs
// only once the caller has acknowledged the 2xx: until then the call may
// still fail (RFC 3261 13.3.1.4).
func TestDialogsKeptOnlyOnceTheCallerAcknowledged(t *testing.T) {
	h := &hooks{}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) { calls <- in })
	b2b := newB2BUA(t, h, contactOf(t, callee))
	infos := make(chan struct{}, 1)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	crossed := make(chan int, 1)
	h.answer = func() {
		in := <-calls
		go in.Send("INFO", sip.Body{})
		select {
		case <-infos:
		case <-time.After(5 * time.Second):
		}
		until(200*time.Millisecond, func() bool { return h.keptCount() > 0 })
		crossed <- h.keptCount()
	}
	out, err := caller.Invite(invitation(t, b2b, func(r *sip.InDialogRequest) {
		r.Accept(sip.Body{})
		infos <- struct{}{}
	}))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		in := <-calls
		calls <- in
		// The 180 makes the early dialog in which the INFO reaches the
		// caller before the 2xx.
		in.Progress(180, sip.Body{})
		in.Accept(answer)
	}()
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	if n := within(t, "the INFO that crosses the 2xx", crossed); n != 0 {
		t.Errorf("the role kept the dialogs %d times before the caller acknowledged the 2xx", n)
	}
	if !until(5*time.Second, func() bool { return h.keptCount() > 0 }) {
		t.Error("the role never kept the dialogs after the ACK")
	}
	out.Bye()
}
