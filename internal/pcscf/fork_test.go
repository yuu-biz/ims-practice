package pcscf

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file fork the INVITE of the UE beyond the P-CSCF, as
// a proxy that forks it does (RFC 3261 16.6): its responses come in more
// than one dialog, each with a To tag of its own. The caller gets the 200
// of the dialog with the To tag callee, which is the call. The other
// dialog has the To tag fork, and its callee takes its media at
// forkMedia.

var forkMedia = netip.MustParseAddrPort("192.0.2.98:6000")

// fork is a response of the far end to the INVITE in the other dialog,
// with the answer of its callee.
func (c *rawINVITE) fork(code int) *sip.Message {
	resp := c.response(code)
	resp.Set("To", c.st.Request.Get("To")+";tag=fork")
	resp.Set("Contact", "<sip:fork@"+c.far.LocalAddr().String()+">")
	resp.Body = sdp.Offer("7", forkMedia).Bytes()
	return resp
}

// established has the far end answer the INVITE with the 200 of the
// call, and returns the Dialog that the P-CSCF keeps of the call once
// the caller has that 200.
func (c *rawINVITE) established(t *testing.T) Dialog {
	t.Helper()
	c.st.Respond(c.response(200))
	if r := final(t, c.ct.Responses(), 3*time.Second); r.StatusCode != 200 || tagOf(r.Get("To")) != "callee" {
		t.Fatalf("the caller got %d %s, want the 200 of the call", r.StatusCode, r.Get("To"))
	}
	key := DialogKey{CallID: c.st.Request.Get("Call-ID"), UETag: tagOf(c.st.Request.Get("From"))}
	d, ok, _ := c.store.UpdateDialog(context.Background(), key, func(*Dialog) {})
	if !ok {
		t.Fatal("no dialog after the 200")
	}
	return d
}

// answerFork has the far end answer the INVITE with the 200 of the
// other dialog too, and checks that the P-CSCF ends that dialog: its
// callee gets the ACK and the BYE of the P-CSCF, and the caller nothing
// of it.
func (c *rawINVITE) answerFork(t *testing.T) {
	t.Helper()
	c.st.Respond(c.fork(200))
	// The far end takes the ACK and the BYE on goroutines of their own,
	// so they reach farGot in either order.
	got := map[sip.Method]*sip.Message{}
	for deadline := time.After(3 * time.Second); len(got) < 2 && deadline != nil; {
		select {
		case m := <-c.farGot:
			got[m.Method] = m
		case <-deadline:
			deadline = nil
		}
	}
	for _, method := range []sip.Method{sip.ACK, sip.BYE} {
		if m := got[method]; m == nil || tagOf(m.Get("To")) != "fork" {
			t.Errorf("the far end got %v, want the %s of the P-CSCF in the other dialog", m, method)
		}
	}
	for deadline := time.After(watch); deadline != nil; {
		select {
		case r := <-c.ct.Responses():
			if tagOf(r.Get("To")) == "fork" {
				t.Errorf("the caller got %d of the other dialog, want nothing of it", r.StatusCode)
			}
		case <-deadline:
			deadline = nil
		}
	}
}

// wantCallKept checks, for a while, that the call stays as its 200 left
// it: the Dialog is still call, the relay context sends to the callee of
// the call, and the PCRF has authorised the answer of that 200 alone and
// released nothing.
func (c *rawINVITE) wantCallKept(t *testing.T, call Dialog) {
	t.Helper()
	changed := func() bool {
		d, ok, _ := c.store.UpdateDialog(context.Background(), call.Key, func(*Dialog) {})
		switch {
		case !ok:
			t.Error("the dialog of the call is gone")
		case !reflect.DeepEqual(d, call):
			t.Errorf("the dialog of the call is\n%+v\nwant it as its 200 left it\n%+v", d, call)
		case c.relay.held() != 1:
			t.Errorf("the relay holds %d contexts, want the one of the call", c.relay.held())
		case len(c.pcrf.releases()) != 0:
			t.Errorf("released the policy sessions %v, want none", c.pcrf.releases())
		default:
			return false
		}
		return true
	}
	for deadline := time.Now().Add(watch); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if changed() {
			break
		}
	}
	if got := c.relay.remote("1", agw.Core); got != coreMedia {
		t.Errorf("the core termination sends to %v, want the callee of the call at %v", got, coreMedia)
	}
	if n := len(c.pcrf.mediaRequests()); n != 1 {
		t.Errorf("%d requests to authorise media, want the one of the 200 of the call", n)
	}
}

// TestA2xxOfAnotherForkLeavesTheCallAsItIs: the caller has the 200 of
// the call, and the other dialog of the forked INVITE answers 200 too,
// with an answer that the relay would take. The one relay context and
// the one policy session of the call serve the dialog of the first 200,
// so the second cannot go through them without moving the media of the
// call. It goes no further: the P-CSCF acknowledges it in the caller's
// name and ends that dialog with a BYE, as for a 2xx whose answer the
// relay does not take. The relay still sends to the callee of the call,
// the PCRF has only its answer, and the Dialog still names that callee.
func TestA2xxOfAnotherForkLeavesTheCallAsItIs(t *testing.T) {
	c := newRawINVITEWith(t, &fakePolicy{})
	call := c.established(t)
	c.answerFork(t)
	c.wantCallKept(t, call)
}
