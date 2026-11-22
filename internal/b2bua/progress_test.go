package b2bua_test

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The role sends the caller a 1xx of its own: after it has relayed the
// callee's reliable 180, a 183 that carries the session of an
// announcement of its own, in answer to an INVITE that had no offer.
// The caller gets it reliably, as every response of the B2BUA's early
// dialog goes when the INVITE offered 100rel (RFC 3262 3), and answers
// the offer in its PRACK (RFC 3262 5). The Call answers that PRACK with
// 200 itself and none of it reaches the callee, which waits for no
// PRACK of its own (RFC 3262 4): the PRACK that went on as the PRACK of
// the callee's 180 was the one for the copy of that 180.
func TestTheRolesOwnProvisionalResponse(t *testing.T) {
	calleeWire := newWire()
	h := &hooks{}
	role := make(chan *b2bua.Call, 1)
	h.own = func(c *b2bua.Call) { role <- c }
	calls := make(chan *sip.IncomingCall, 1)
	callee := startUA(t, "callee", timers, calleeWire, func(in *sip.IncomingCall) {
		calls <- in
	})
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, nil)
	inv.Body = sip.Body{}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}

	in := within(t, "INVITE at the callee", calls)
	ringing := make(chan error, 1)
	go func() { ringing <- in.Progress(180, sip.Body{}) }()
	if prov := within(t, "180 at the caller", out.Responses()); prov.StatusCode != 180 {
		t.Fatalf("the caller got %v, want the callee's 180", prov)
	}
	if err := within(t, "PRACK of the callee's 180", ringing); err != nil {
		t.Fatalf("the callee's 180 got no PRACK: %v", err)
	}

	own := make(chan error, 1)
	c := within(t, "the call of the role", role)
	go func() { own <- c.Progress(183, answer) }()
	announced := within(t, "183 at the caller", out.Responses())
	if announced.StatusCode != 183 || !announced.Requires(sip.Option100rel) || string(announced.Body) != string(answer.Content) {
		t.Fatalf("the caller got %v, want the role's reliable 183 with its own session description", announced)
	}
	if resp, err := out.PRACK(announced, offer); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the PRACK with the answer to the role's offer got %v: %v", resp, err)
	}
	if err := within(t, "the end of the role's 183", own); err != nil {
		t.Fatalf("the role's 183 got no PRACK: %v", err)
	}
	if got := calleeWire.bodies(sip.PRACK); len(got) != 1 || got[0] != "" {
		t.Errorf("the callee received PRACKs with %q, want only the one of its 180, without a body", got)
	}
	if calleeWire.has(sip.UPDATE) {
		t.Errorf("the callee received %v, want no UPDATE with the answer to the role's own offer", calleeWire.requests())
	}
	out.Cancel()
	select {
	case <-in.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the callee's call did not end")
	}
}
