package mmtel_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestFocusInviteWithReplacesThroughTheAS is TS 24.229 5.7.5.1 at the
// AS of a participant: a conference focus INVITEs the callee of a call
// that passes the AS, to take that call over. The Replaces names the
// caller's leg as the AS has it; the AS names its leg to the callee
// instead, so that the callee finds its own call. The Contact of the
// focus, with isfocus, reaches the callee as it was, and the AS stays
// on the route, so the callee's BYE of the new call comes back to the
// focus through the AS.
func TestFocusInviteWithReplacesThroughTheAS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	calls := make(chan *sip.IncomingCall, 2)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		calls <- c
		if c.Request.Get("Replaces") == "" {
			c.Accept(answer)
		}
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "term", offer))
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, out)
	old := receive(t, calls, "call at the callee")

	// The focus sits where the caller is: it names the caller's leg as
	// the AS, its UAS, has it (RFC 3891 3).
	callerDialog, _ := out.Dialog()
	replaces := sip.Replaces{CallID: callerDialog.ID.CallID, ToTag: callerDialog.ID.RemoteTag, FromTag: callerDialog.ID.LocalTag}
	// The AS keeps the record of the call once the caller's ACK came. No
	// message tells when, so the test waits for the record itself.
	waitFor(t, func() bool { _, ok := callOf(t, as, replaces.DialogID()); return ok }, "the record of the call")
	focus := newUA(t, "focus", func(c *sip.IncomingCall) { c.Reject(480) })
	contact := sip.NameAddr{URI: "sip:conf-1@" + contactOf(t, focus), Params: sip.Params{{Name: "isfocus"}}}
	inv := viaAS(t, as, callee, "term", offer)
	inv.Contact = contact
	inv.Headers = append(inv.Headers, replaces.Header(), sip.HeaderField{Name: "Referred-By", Value: "<sip:alice@" + domain + ">"})
	conf, err := focus.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	c := receive(t, calls, "INVITE with Replaces at the callee")
	if got := c.Request.Get("Contact"); got != contact.String() {
		t.Errorf("callee's INVITE has Contact %q, want the focus's %q", got, contact.String())
	}
	if rr := c.Request.GetAll("Record-Route"); len(rr) == 0 || rr[0] != "<sip:"+as.addr.String()+";lr>" {
		t.Errorf("callee's INVITE has Record-Route %q, want the AS on top", rr)
	}
	replaced, refusal := c.ReplacedDialog()
	if refusal != 0 || replaced != &old.CallDialog {
		t.Fatalf("callee's ReplacedDialog = %p, %d (Replaces %q); want its call with the AS", replaced, refusal, c.Request.Get("Replaces"))
	}
	if c.Request.Get("Referred-By") == "" {
		t.Error("the Referred-By of the focus's INVITE did not reach the callee")
	}
	c.Accept(answer)
	if got := statuses(readAll(t, conf)); len(got) != 1 || got[0] != 200 {
		t.Fatalf("focus got %v, want 200", got)
	}
	if err := old.Bye(); err != nil {
		t.Fatal(err)
	}
	receive(t, out.Done(), "end of the replaced call at the caller")
	if err := c.Bye(); err != nil {
		t.Fatalf("callee's BYE of the new call: %v", err)
	}
	receive(t, conf.Done(), "BYE at the focus")
}
