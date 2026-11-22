package b2bua_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The role sends no INVITE toward the callee (NoLeg) and gives the
// caller an early dialog of its own, in which it plays it an
// announcement before it rejects the INVITE. The requests of the caller
// within that dialog find no leg to go on in, and get the answers of a
// UAS that keeps the session it gave: 488 for a new offer, since the
// role has nothing else to offer; 200 for an UPDATE without one, which
// changes nothing; and 481 for any other request, which has neither a
// leg nor a dialog to go on in. None of them goes unanswered, and no
// 481 ends the early dialog along with the announcement (RFC 3261
// 12.2.1.2).
func TestRequestsOfTheCallerWithoutALeg(t *testing.T) {
	h := &hooks{noLeg: make(chan struct{})}
	role := make(chan *b2bua.Call, 1)
	h.own = func(c *b2bua.Call) { role <- c }
	b2b := newB2BUA(t, h, "sip:nobody@127.0.0.1:1")
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	announced := make(chan error, 1)
	c := within(t, "the call of the role", role)
	go func() { announced <- c.Progress(183, answer) }()
	if prov := within(t, "183 at the caller", out.Responses()); prov.StatusCode != 183 || string(prov.Body) != string(answer.Content) {
		t.Fatalf("the caller got %v, want the 183 with the session of the announcement", prov)
	}
	if err := within(t, "the end of the role's 183", announced); err != nil {
		t.Fatalf("the role's 183 got no PRACK: %v", err)
	}

	if resp, err := out.Update(offer); err != nil || resp.StatusCode != 488 {
		t.Errorf("the UPDATE with a new offer got %v: %v, want 488", resp, err)
	}
	if resp, err := out.Update(sip.Body{}); err != nil || resp.StatusCode != 200 {
		t.Errorf("the UPDATE without a body got %v: %v, want 200", resp, err)
	}
	if resp, err := out.Send("INFO", sip.Body{}); err != nil || resp.StatusCode != 481 {
		t.Errorf("the INFO got %v: %v, want 481", resp, err)
	}

	close(h.noLeg)
	if _, final := finalOf(t, out); final.StatusCode != 480 {
		t.Errorf("the call got %d, want the role's 480", final.StatusCode)
	}
}
