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
func (c *rawINVITE) answerFork(t *testing.T) (bye *sip.Message) {
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
	return got[sip.BYE]
}

// wantCallKept checks, for a while, that the call stays as its 200 left
// it: the Dialog is still call, the relay context sends to the callee of
// the call, and the PCRF has authorised the answer of that 200 alone and
// released nothing. The CSeqs of the parties may have grown, as a
// request of the other dialog counts in them (subsequent).
func (c *rawINVITE) wantCallKept(t *testing.T, call Dialog) {
	t.Helper()
	changed := func() bool {
		d, ok, _ := c.store.UpdateDialog(context.Background(), call.Key, func(*Dialog) {})
		call.Caller.CSeq, call.Callee.CSeq = max(call.Caller.CSeq, d.Caller.CSeq), max(call.Callee.CSeq, d.Callee.CSeq)
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

// inFork is a request within the other dialog, from the UE, or from the
// callee of that dialog to the UE when toUE is set.
func (c *rawINVITE) inFork(method sip.Method, toUE bool, cseq int) *sip.Message {
	ue, fork := c.st.Request.Get("From"), c.st.Request.Get("To")+";tag=fork"
	m := &sip.Message{Method: method, RequestURI: "sip:fork@" + c.far.LocalAddr().String()}
	if toUE {
		m.RequestURI, ue, fork = c.ueContact, fork, ue
	}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", ue)
	m.Add("To", fork)
	m.Add("Call-ID", c.st.Request.Get("Call-ID"))
	m.Add("CSeq", (sip.CSeq{Number: uint32(cseq), Method: method}).String())
	return m
}

// through sends m from the stack from to the P-CSCF and returns the
// final response that comes back.
func (c *rawINVITE) through(t *testing.T, from *sip.Stack, m *sip.Message) *sip.Message {
	t.Helper()
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: c.pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	return final(t, ct.Responses(), 3*time.Second)
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

// TestRequestOfAnotherForkLeavesTheCallAsItIs: once the caller has the
// 200 of the call, a request within the other dialog of the forked
// INVITE finds the Dialog of the call, as both have the tag of the
// caller. It changes nothing of the call. A BYE goes on and its answer,
// a 481 too, ends no more than that dialog (RFC 3261 15.1.1). An offer
// cannot go through the relay context of the call without moving its
// media, nor go on as it came past the relay, so the P-CSCF answers it
// 500 (RFC 3261 21.5.1).
func TestRequestOfAnotherForkLeavesTheCallAsItIs(t *testing.T) {
	t.Run("BYE from the UE in an early dialog", func(t *testing.T) {
		c := newRawINVITEWith(t, &fakePolicy{})
		ringing := c.fork(180)
		ringing.Remove("Content-Type")
		ringing.Body = nil
		c.st.Respond(ringing)
		call := c.established(t)

		// A caller may end an early dialog with a BYE (RFC 3261 15).
		if r := c.through(t, c.ue, c.inFork(sip.BYE, false, 2)); r.StatusCode != 200 {
			t.Fatalf("the BYE got %d, want the 200 of the far end", r.StatusCode)
		}
		c.wantCallKept(t, call)
	})
	t.Run("BYE to the UE, answered 481", func(t *testing.T) {
		c := newRawINVITEWith(t, &fakePolicy{})
		call := c.established(t)
		c.answerFork(t)

		// The callee of the other dialog hangs up as the P-CSCF ends that
		// dialog, and the UE has no such dialog (RFC 3261 12.2.2).
		c.ueAnswers.Store(481)
		if r := c.through(t, c.far, c.inFork(sip.BYE, true, 1)); r.StatusCode != 481 {
			t.Fatalf("the BYE got %d, want the 481 of the UE", r.StatusCode)
		}
		c.wantCallKept(t, call)
	})
	t.Run("UPDATE with an offer to the UE", func(t *testing.T) {
		c := newRawINVITEWith(t, &fakePolicy{})
		call := c.established(t)
		c.answerFork(t)

		c.ueAnswers.Store(481)
		update := c.inFork(sip.UPDATE, true, 1)
		update.Add("Contact", "<sip:fork@"+c.far.LocalAddr().String()+">")
		update.Add("Content-Type", "application/sdp")
		update.Body = sdp.Offer("8", moved).Bytes()
		if r := c.through(t, c.far, update); r.StatusCode != 500 {
			t.Errorf("the UPDATE got %d, want the 500 of the P-CSCF", r.StatusCode)
		}
		noRequest(t, "the UE", c.ueGot)
		c.wantCallKept(t, call)
	})
}

// TestTheBYEOfAnotherForkFollowsTheCallersRequestsInIt: the caller may
// send requests within the other dialog while it is early, after the
// 200 of the call, such as an INFO. The BYE with which the P-CSCF ends
// that dialog once its 200 comes takes a CSeq above them, or the callee
// of that dialog would answer it 500 and stay in the dialog (RFC 3261
// 12.2.2).
func TestTheBYEOfAnotherForkFollowsTheCallersRequestsInIt(t *testing.T) {
	c := newRawINVITEWith(t, &fakePolicy{})
	ringing := c.fork(180)
	ringing.Remove("Content-Type")
	ringing.Body = nil
	c.st.Respond(ringing)
	call := c.established(t)

	if r := c.through(t, c.ue, c.inFork("INFO", false, 7)); r.StatusCode != 200 {
		t.Fatalf("the INFO got %d, want the 200 of the far end", r.StatusCode)
	}
	if m := <-c.farGot; m.Method != "INFO" {
		t.Fatalf("the far end got %s, want the INFO", m.Method)
	}
	bye := c.answerFork(t)
	if bye == nil {
		t.Fatal("no BYE in the other dialog")
	}
	if cseq, err := sip.ParseCSeq(bye.Get("CSeq")); err != nil || cseq.Number <= 7 {
		t.Errorf("the BYE in the other dialog has CSeq %q, want a number above the 7 of the caller's INFO", bye.Get("CSeq"))
	}
	c.wantCallKept(t, call)
}
