package b2bua_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// answer2 is the answer of another early dialog of the callee's side
// than the one whose answer the caller got.
var answer2 = sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 2 1 IN IP4 127.0.0.1\r\n")}

// The callee's side ends the early dialog whose answer the caller got
// with a 199, and answers in a new one (RFC 6228 4). The caller has one
// early dialog with the B2BUA, which goes on, and the answer of the new
// dialog reaches it as it came: the Call takes out the session
// descriptions of the other dialogs only while the dialog the caller
// follows lasts. With preconditions the UAS of the new dialog alerts
// its user only once the caller has confirmed its resources, which
// needs that answer (RFC 3312 6).
func TestTheNextEarlyDialogAfterA199(t *testing.T) {
	h := &hooks{}
	calls := make(chan *sip.IncomingCall, 1)
	callee := newUA(t, "callee", func(in *sip.IncomingCall) { calls <- in })
	b2b := newB2BUA(t, h, contactOf(t, callee))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	out, err := caller.Invite(invitation(t, b2b, nil))
	if err != nil {
		t.Fatal(err)
	}
	in := within(t, "INVITE at the callee", calls)
	progressed := make(chan error, 1)
	go func() { progressed <- in.Progress(183, answer) }()
	if prov := within(t, "183 at the caller", out.Responses()); prov.StatusCode != 183 || string(prov.Body) != string(answer.Content) {
		t.Fatalf("the caller got %v, want the 183 with the answer of the first early dialog", prov)
	}
	if err := within(t, "PRACK of the first 183", progressed); err != nil {
		t.Fatalf("the first 183 got no PRACK: %v", err)
	}
	if err := in.EarlyDialogTerminated(480); err != nil {
		t.Fatalf("199: %v", err)
	}

	go func() { progressed <- in.Progress(183, answer2) }()
	next := within(t, "183 of the next early dialog at the caller", out.Responses())
	if next.StatusCode != 183 {
		t.Fatalf("the caller got %v, want the 183 of the next early dialog", next)
	}
	if string(next.Body) != string(answer2.Content) {
		t.Errorf("the caller got the 183 of the next early dialog with %q, want the answer %q of that dialog", next.Body, answer2.Content)
	}
	if err := within(t, "PRACK of the second 183", progressed); err != nil {
		t.Fatalf("the 183 of the next early dialog got no PRACK: %v", err)
	}
	out.Cancel()
}
