package b2bua_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file has the role give one leg up and send the INVITE on to
// another callee (b2bua.Call.Attempt again), as a service that diverts
// a call does, and end the early dialog of the caller with a 199 (RFC
// 6228 4). What the caller got in that early dialog is then stale, and
// what the caller acknowledged in it is acknowledged no further.

// twoLegs answers the INVITE of the first leg with a reliable 183 that
// carries the session description of its UAS, and refuses it with 486
// (Busy Here) once the test lets it; it answers the INVITE of the
// second leg with a 200.
//
// Without ringing the 200 of the second leg carries the session
// description of its UAS, as it does after an INVITE without an offer
// (RFC 3261 13.2.1). With ringing the callee of the first leg sends a
// second reliable response, its 180, and the second leg answers the
// offer of the INVITE in a reliable 183 of its own before its 200.
type twoLegs struct {
	t     *testing.T
	stack *sip.Stack
	legs  atomic.Int32
	// pracked gets the To tag of each PRACK that came.
	pracked chan string
	// next lets the callee send the response after it.
	next    chan struct{}
	ringing bool
}

func newTwoLegs(t *testing.T, ringing bool) *twoLegs {
	t.Helper()
	tl, err := sip.Listen("127.0.0.1:0", discard)
	if err != nil {
		t.Fatal(err)
	}
	f := &twoLegs{t: t, pracked: make(chan string, 4), next: make(chan struct{}), ringing: ringing}
	stack, err := sip.NewStack(tl, sip.Config{SentBy: tl.LocalAddr().String(), Timers: timers,
		OnRequest: f.onRequest, OnACK: func(*sip.Message) {}}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	f.stack = stack
	return f
}

// uri is the URI of the callee of leg i.
func (f *twoLegs) uri(i int) string {
	return fmt.Sprintf("sip:leg-%d@%s", i, f.stack.LocalAddr())
}

// response is the response of the callee of leg i to req, reliable with
// the RSeq rseq while it is provisional, with body when it carries one.
func (f *twoLegs) response(req *sip.Message, code, i, rseq int, body sip.Body) *sip.Message {
	resp := sip.NewResponse(req, code, fmt.Sprintf("leg-%d", i))
	for _, rr := range req.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", "<"+f.uri(i)+">")
	if code < 200 {
		resp.AddOption("Require", sip.Option100rel)
		resp.Add("RSeq", fmt.Sprint(rseq))
	}
	if len(body.Content) > 0 {
		resp.Set("Content-Type", body.Type)
		resp.Body = body.Content
	}
	return resp
}

func (f *twoLegs) onRequest(st *sip.ServerTransaction) {
	req := st.Request
	switch {
	case req.Method == sip.PRACK:
		f.pracked <- tagOf(f.t, req)
		st.Respond(sip.NewResponse(req, 200, ""))
	case req.Method != sip.INVITE:
		st.Respond(sip.NewResponse(req, 200, ""))
	case f.legs.Add(1) == 1:
		go func() {
			st.Respond(f.response(req, 183, 1, 1, branchSession(1)))
			if f.ringing {
				st.Respond(f.response(req, 180, 1, 2, sip.Body{}))
			}
			<-f.next
			st.Respond(f.response(req, 486, 1, 0, sip.Body{}))
		}()
	case !f.ringing:
		st.Respond(f.response(req, 200, 2, 0, branchSession(2)))
	default:
		go func() {
			st.Respond(f.response(req, 183, 2, 1, branchSession(2)))
			<-f.next
			st.Respond(f.response(req, 200, 2, 0, sip.Body{}))
		}()
	}
}

// endsTheEarlyDialog is what the role does before the new leg: it ends
// the early dialog of the caller with a 199 (RFC 6228 4).
func endsTheEarlyDialog(t *testing.T) func(*sip.IncomingCall, *b2bua.Call) {
	return func(in *sip.IncomingCall, _ *b2bua.Call) {
		if err := in.EarlyDialogTerminated(486); err != nil {
			t.Errorf("the 199 did not go: %v", err)
		}
	}
}

// After an INVITE without an offer, the UAS of the leg that the role
// gave up offered in its reliable 183, which the caller answered in its
// PRACK. The role ended that early dialog with a 199, so the offer of
// the INVITE is open again and the caller takes the session description
// of the new leg, which its 200 carries (RFC 3261 13.2.1, RFC 6228 4).
func TestSessionOfTheNewLegAfterAnINVITEWithoutAnOffer(t *testing.T) {
	f := newTwoLegs(t, false)
	b2b := newB2BUA(t, &hooks{next: f.uri(2), before: endsTheEarlyDialog(t)}, f.uri(1))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, nil)
	inv.Body = sip.Body{}
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Supported", Value: sip.Option100rel + ", " + sip.Option199})
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	first := within(t, "the 183 of the leg that the role gives up", out.Responses())
	if first.StatusCode != 183 || mediaOf(t, first.Body) != 41001 {
		t.Fatalf("the caller got %v, want the 183 with the offer of the first leg", first)
	}
	if resp, err := out.PRACK(first, callerSession(1)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the PRACK with the answer got %v: %v", resp, err)
	}
	f.next <- struct{}{}
	if ended := within(t, "the 199", out.Responses()); ended.StatusCode != 199 {
		t.Fatalf("the caller got %d, want the 199 that ends its early dialog", ended.StatusCode)
	}
	_, final := finalOf(t, out)
	if final.StatusCode != 200 {
		t.Fatalf("the call got %d, want the 200 of the new leg", final.StatusCode)
	}
	if len(final.Body) == 0 {
		t.Fatal("the 200 of the new leg brought the caller no session description")
	}
	if got := mediaOf(t, final.Body); got != 41002 {
		t.Errorf("the 200 points the caller's media at port %d, want 41002 of the new leg", got)
	}
	out.Bye()
}

// The caller acknowledged two reliable responses of the leg that the
// role gave up, and the PRACK of the first was still on its way to that
// callee when the leg ended, so the second one never went on (RFC 3262
// 4): it gave up its place in the order of the caller's requests. That
// place is not the next PRACK's, so the PRACK of the reliable response
// of the new leg goes on as the PRACK of that response, and the callee
// of the new leg stops sending it again (RFC 3262 3).
func TestPRACKOfTheNewLegAfterOneThatDidNotGoOn(t *testing.T) {
	f := newTwoLegs(t, true)
	held := make(chan struct{})
	var first atomic.Bool
	h := &hooks{
		next:   f.uri(2),
		before: endsTheEarlyDialog(t),
		request: func(req *sip.Message, d b2bua.Direction) *b2bua.Refusal {
			if req.Method == sip.PRACK && d == b2bua.ToCallee && first.CompareAndSwap(false, true) {
				// The PRACK of the first reliable response stays in the
				// B2BUA until the leg it belongs to has ended.
				<-held
			}
			return nil
		},
	}
	b2b := newB2BUA(t, h, f.uri(1))
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, nil)
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Supported", Value: sip.Option100rel + ", " + sip.Option199})
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{183, 180} {
		got := within(t, fmt.Sprintf("the %d of the leg that the role gives up", want), out.Responses())
		if got.StatusCode != want {
			t.Fatalf("the caller got %d, want the %d of the first leg", got.StatusCode, want)
		}
	}
	// The leg ends while the PRACK of its 183 is still in the B2BUA, so
	// the PRACK of its 180 has nowhere to go.
	f.next <- struct{}{}
	go func() {
		until(5*time.Second, func() bool { return f.legs.Load() == 2 })
		close(held)
	}()
	// The PRACK that was held goes no further: the dialog it belongs to
	// has ended with its leg. The PRACK that the new leg waits for is
	// the one that reaches a callee.
	if tag := within(t, "the PRACK of the 183 of the new leg", f.pracked); tag != "leg-2" {
		t.Errorf("the PRACK went to the dialog with the tag %q, want leg-2 of the new leg", tag)
	}
	f.next <- struct{}{}
	_, final := finalOf(t, out)
	if final.StatusCode != 200 {
		t.Fatalf("the call got %d, want the 200 of the new leg", final.StatusCode)
	}
	out.Bye()
}
