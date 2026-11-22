package b2bua_test

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file has the role give one leg up and send the INVITE on to
// another callee (b2bua.Call.Attempt again), as a service that diverts
// a call does, and end the early dialog of the caller with a 199 (RFC
// 6228 4). What the caller got in that early dialog is then stale.

// twoLegs answers the INVITE of the first leg with a reliable 183 that
// carries the session description of its UAS, and refuses it with 486
// (Busy Here) once the test lets it; it answers the INVITE of the
// second leg with a 200.
//
// The 200 of the second leg carries the session description of its UAS,
// as it does after an INVITE without an offer (RFC 3261 13.2.1).
type twoLegs struct {
	t     *testing.T
	stack *sip.Stack
	legs  atomic.Int32
	// pracked gets the To tag of each PRACK that came.
	pracked chan string
	// next lets the callee send the response after it.
	next chan struct{}
}

func newTwoLegs(t *testing.T) *twoLegs {
	t.Helper()
	tl, err := sip.Listen("127.0.0.1:0", discard)
	if err != nil {
		t.Fatal(err)
	}
	f := &twoLegs{t: t, pracked: make(chan string, 4), next: make(chan struct{})}
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
			<-f.next
			st.Respond(f.response(req, 486, 1, 0, sip.Body{}))
		}()
	default:
		st.Respond(f.response(req, 200, 2, 0, branchSession(2)))
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
	f := newTwoLegs(t)
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
