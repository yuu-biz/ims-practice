package b2bua_test

import (
	"fmt"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file has the INVITE of the B2BUA fork beyond it: two UASs answer
// it, each in an early dialog of its own, and the second one answers the
// call (RFC 3261 13.2.2.4). The caller has one early dialog with the
// B2BUA and keeps the session description of the first dialog (13.2.1),
// so it learns of the session of the dialog that answered from an offer
// of the Call's own.

// branchSession is the session description of branch i of the forked
// callee, whose audio stream has a port of its own.
func branchSession(i int) sip.Body { return branchVersion(i, 1, "") }

// branchVersion is the session description of branch i with a version of
// its own, and direction as the direction of its audio stream.
func branchVersion(i int, version uint64, direction string) sip.Body {
	s := fmt.Sprintf("v=0\r\no=branch-%d 700%d %d IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio %d RTP/AVP 0\r\n",
		i, i, version, 41000+i)
	if direction != "" {
		s += "a=" + direction + "\r\n"
	}
	return sip.Body{Type: "application/sdp", Content: []byte(s)}
}

// hold is the offer of a caller that puts the call on hold (RFC 3264
// 8.4), and heldAnswer the answer of the callee to it.
var (
	hold = sip.Body{Type: "application/sdp", Content: []byte(
		"v=0\r\no=caller 1352 2 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0\r\na=sendonly\r\n")}
	heldAnswer = branchVersion(2, 2, "recvonly")
)

// tagOf is the To tag of m.
func tagOf(t *testing.T, m *sip.Message) string {
	t.Helper()
	na, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		t.Fatalf("To of %s: %v", m.Get("CSeq"), err)
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// callerSession is the offer of the caller, and its answer to an offer
// of the B2BUA.
func callerSession(version uint64) sip.Body {
	return sip.Body{Type: "application/sdp", Content: []byte(fmt.Sprintf(
		"v=0\r\no=caller 1352 %d IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 40000 RTP/AVP 0\r\n",
		version))}
}

// mediaOf is the port of the audio stream of body.
func mediaOf(t *testing.T, body []byte) int {
	t.Helper()
	s, err := sdp.Parse(body)
	if err != nil {
		t.Fatalf("no session description in\n%s\n%v", body, err)
	}
	addr, _, err := s.Audio()
	if err != nil {
		t.Fatalf("no audio stream in\n%s\n%v", body, err)
	}
	return int(addr.Port())
}

// forkedCallee answers each INVITE with a reliable 183 of each of two
// UASs, each with its own To tag and session description, and then with
// the 200 of the second one; next lets each response after the first
// go.
type forkedCallee struct {
	t     *testing.T
	stack *sip.Stack
	next  chan struct{}
	// acked gets the body of the ACK of the 200.
	acked chan []byte
	// inDialog gets each request with a body that came within a dialog,
	// such as the caller's hold; byes gets each BYE; dialogs gets the
	// early dialog of each branch, in which the test sends requests of
	// its own.
	inDialog chan *sip.Message
	byes     chan *sip.Message
	dialogs  chan *sip.Dialog
	// offers has the 200 carry the offer of its UAS, as it does after an
	// INVITE without one (RFC 3261 13.2.1).
	offers bool
	// refuses, when it is not zero, is the status with which the dialog
	// that answered refuses an offer within the dialog.
	refuses int
}

func newForkedCallee(t *testing.T, offers bool) *forkedCallee {
	t.Helper()
	tl, err := sip.Listen("127.0.0.1:0", discard)
	if err != nil {
		t.Fatal(err)
	}
	f := &forkedCallee{t: t, next: make(chan struct{}), acked: make(chan []byte, 1),
		inDialog: make(chan *sip.Message, 4), byes: make(chan *sip.Message, 2),
		dialogs: make(chan *sip.Dialog, 2), offers: offers}
	stack, err := sip.NewStack(tl, sip.Config{SentBy: tl.LocalAddr().String(), Timers: timers,
		OnRequest: f.onRequest, OnACK: func(ack *sip.Message) { f.acked <- ack.Body }}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	f.stack = stack
	return f
}

func (f *forkedCallee) uri() string { return "sip:callee@" + f.stack.LocalAddr().String() }

// response is the response of branch i to req, with its session
// description when it carries one.
func (f *forkedCallee) response(req *sip.Message, code, i int, body sip.Body) *sip.Message {
	resp := sip.NewResponse(req, code, fmt.Sprintf("branch-%d", i))
	for _, rr := range req.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", fmt.Sprintf("<sip:branch-%d@%s>", i, f.stack.LocalAddr()))
	if code < 200 {
		resp.AddOption("Require", sip.Option100rel)
		resp.Add("RSeq", "1")
	}
	if len(body.Content) > 0 {
		resp.Set("Content-Type", body.Type)
		resp.Body = body.Content
	}
	return resp
}

func (f *forkedCallee) onRequest(st *sip.ServerTransaction) {
	req := st.Request
	switch {
	case req.Method == sip.BYE:
		f.byes <- req
		st.Respond(sip.NewResponse(req, 200, ""))
		return
	case req.Method != sip.INVITE && len(req.Body) == 0:
		// A PRACK of a reliable response, or a request that changes no
		// session.
		st.Respond(sip.NewResponse(req, 200, ""))
		return
	case req.Method != sip.INVITE || tagOf(f.t, req) != "":
		// A request with an offer within a dialog, such as the caller's
		// hold: branch 2 answers it, or refuses it.
		f.inDialog <- req
		if f.refuses != 0 {
			st.Respond(sip.NewResponse(req, f.refuses, "branch-2"))
			return
		}
		st.Respond(f.response(req, 200, 2, heldAnswer))
		return
	}
	go func() {
		st.Respond(f.answer(st, 183, 1, branchSession(1)))
		<-f.next
		st.Respond(f.answer(st, 183, 2, branchSession(2)))
		<-f.next
		body := sip.Body{}
		if f.offers {
			// The answer of branch 2 went in its 183; after an INVITE
			// without an offer its 200 carries its offer (RFC 3262 5).
			body = branchSession(2)
		}
		st.Respond(f.answer(st, 200, 2, body))
	}()
}

// answer is the response of branch i to the INVITE of st, and keeps the
// early dialog that a 183 creates, in which the test may send requests
// of the branch.
func (f *forkedCallee) answer(st *sip.ServerTransaction, code, i int, body sip.Body) *sip.Message {
	resp := f.response(st.Request, code, i, body)
	if code < 200 {
		if d, err := sip.NewUASDialog(st.Request, resp); err == nil {
			f.dialogs <- &d
		}
	}
	return resp
}

// send sends a request of method with body within d, the early dialog of
// a branch, to the B2BUA, and returns its final response.
func (f *forkedCallee) send(d *sip.Dialog, method sip.Method, body sip.Body) *sip.Message {
	f.t.Helper()
	req := d.NewRequest(method)
	if len(body.Content) > 0 {
		req.Set("Content-Type", body.Type)
		req.Body = body.Content
	}
	hop, err := sip.NextHop(req)
	if err != nil {
		f.t.Fatal(err)
	}
	addr, err := netip.ParseAddr(hop.Host)
	if err != nil {
		f.t.Fatal(err)
	}
	ct, err := f.stack.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: netip.AddrPortFrom(addr, uint16(hop.Port))})
	if err != nil {
		f.t.Fatal(err)
	}
	return finalTo(ct)
}

// answerOffers has the caller answer an offer within the dialog with
// its own session description, and passes the request on to got.
func answerOffers(got chan<- *sip.Message) func(*sip.InDialogRequest) {
	return func(r *sip.InDialogRequest) {
		got <- r.Request
		if !r.Offer {
			r.Accept(sip.Body{})
			return
		}
		r.Accept(callerSession(2))
	}
}

// The dialog that answers is not the one whose session description the
// caller got, so the Call offers the caller the session of the dialog
// that answered, once the caller has acknowledged the 200: in an UPDATE
// to a caller that lists UPDATE in the Allow of its INVITE (RFC 3311),
// and otherwise in a re-INVITE (RFC 3261 14.1). The offer goes on in the
// session that the caller has, that of the first dialog, with its
// version one up (RFC 3264 8).
func TestSessionOfTheEarlyDialogThatAnswered(t *testing.T) {
	for _, method := range []sip.Method{sip.UPDATE, sip.INVITE} {
		t.Run(string(method), func(t *testing.T) {
			f := newForkedCallee(t, false)
			b2b := newB2BUA(t, &hooks{}, f.uri())
			offers := make(chan *sip.Message, 2)
			caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
			inv := invitation(t, b2b, answerOffers(offers))
			inv.Body = callerSession(1)
			if method == sip.INVITE {
				inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Allow", Value: "INVITE, ACK, CANCEL, BYE, PRACK"})
			}
			out, err := caller.Invite(inv)
			if err != nil {
				t.Fatal(err)
			}
			first := within(t, "183 of the first dialog", out.Responses())
			if first.StatusCode != 183 || mediaOf(t, first.Body) != 41001 {
				t.Fatalf("the caller got %v, want the 183 with the session of the first dialog", first)
			}
			f.next <- struct{}{}
			if second := within(t, "183 of the second dialog", out.Responses()); len(second.Body) != 0 {
				t.Errorf("the caller got a second session description in its early dialog:\n%s", second.Body)
			}
			f.next <- struct{}{}
			if _, final := finalOf(t, out); final.StatusCode != 200 {
				t.Fatalf("the call got %d", final.StatusCode)
			}

			req := within(t, "the offer at the caller", offers)
			if req.Method != method {
				t.Errorf("the caller got the session of the dialog that answered in a %s, want a %s", req.Method, method)
			}
			if got := mediaOf(t, req.Body); got != 41002 {
				t.Errorf("the %s points the caller's media at port %d, want 41002 of the dialog that answered", req.Method, got)
			}
			had, err := sdp.Parse(first.Body)
			if err != nil {
				t.Fatal(err)
			}
			offer, err := sdp.Parse(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			want := had.Origin
			want.Version++
			if offer.Origin != want {
				t.Errorf("the %s has the o= line %+v, want %+v of the session that the caller has", req.Method, offer.Origin, want)
			}
			out.Bye()
		})
	}
}

// After an INVITE without an offer, the first dialog offers in its
// reliable 183, which the caller answers in its PRACK, and the dialog
// that answers offers in its 200 (RFC 3262 5, RFC 3261 13.2.1). The
// Call offers the caller the session of that dialog after the 200, and
// the caller's answer goes to it in the ACK (13.2.2.4).
func TestOfferOfTheEarlyDialogThatAnswered(t *testing.T) {
	f := newForkedCallee(t, true)
	b2b := newB2BUA(t, &hooks{}, f.uri())
	offers := make(chan *sip.Message, 2)
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, answerOffers(offers))
	inv.Body = sip.Body{}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	first := within(t, "183 of the first dialog", out.Responses())
	if first.StatusCode != 183 || mediaOf(t, first.Body) != 41001 {
		t.Fatalf("the caller got %v, want the 183 with the offer of the first dialog", first)
	}
	if resp, err := out.PRACK(first, callerSession(1)); err != nil || resp.StatusCode != 200 {
		t.Fatalf("the PRACK with the answer got %v: %v", resp, err)
	}
	f.next <- struct{}{}
	if second := within(t, "183 of the second dialog", out.Responses()); len(second.Body) != 0 {
		t.Errorf("the caller got a second session description in its early dialog:\n%s", second.Body)
	}
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	req := within(t, "the offer at the caller", offers)
	if got := mediaOf(t, req.Body); got != 41002 {
		t.Errorf("the %s points the caller's media at port %d, want 41002 of the dialog that answered", req.Method, got)
	}
	if got := mediaOf(t, within(t, "the ACK at the dialog that answered", f.acked)); got != 40000 {
		t.Errorf("the ACK of the 200 has the answer with port %d, want the caller's 40000", got)
	}
	out.Bye()
}

// An offer that moves the caller's media may cross one of the caller,
// which refuses it with 491 (Request Pending); the Call offers the same
// session again after a while (RFC 3261 14.1, RFC 3311 5.3).
func TestSessionOfTheEarlyDialogThatAnsweredAfterGlare(t *testing.T) {
	f := newForkedCallee(t, false)
	b2b := newB2BUA(t, &hooks{}, f.uri())
	offers := make(chan *sip.Message, 2)
	accept := answerOffers(offers)
	var refused atomic.Bool
	caller := newUA(t, "caller", func(in *sip.IncomingCall) { in.Reject(480) })
	inv := invitation(t, b2b, func(r *sip.InDialogRequest) {
		if refused.CompareAndSwap(false, true) {
			offers <- r.Request
			r.Reject(491)
			return
		}
		accept(r)
	})
	inv.Body = callerSession(1)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	within(t, "183 of the first dialog", out.Responses())
	f.next <- struct{}{}
	within(t, "183 of the second dialog", out.Responses())
	f.next <- struct{}{}
	if _, final := finalOf(t, out); final.StatusCode != 200 {
		t.Fatalf("the call got %d", final.StatusCode)
	}
	first := within(t, "the first offer at the caller", offers)
	again := within(t, "the second offer at the caller", offers)
	if again.Method != sip.UPDATE || string(again.Body) != string(first.Body) {
		t.Errorf("after the 491 the caller got a %s with\n%s\nwant the UPDATE with the session it refused\n%s",
			again.Method, again.Body, first.Body)
	}
	out.Bye()
}
