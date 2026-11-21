package sip

import (
	"errors"
	"net"
	"reflect"
	"strconv"
	"testing"
	"time"
)

var (
	offer2  = Body{Type: "application/sdp", Content: []byte("v=0\r\no=caller 1 2 IN IP4 127.0.0.1\r\n")}
	answer2 = Body{Type: "application/sdp", Content: []byte("v=0\r\no=callee 1 2 IN IP4 127.0.0.1\r\n")}
)

// TestUPDATEInAnEarlyDialog is the exchange that preconditions need
// (RFC 3312 with RFC 3311): the answer comes in a reliable 183, and
// while the INVITE is still pending the caller sends a second offer in
// an UPDATE, which the callee answers in the 200.
func TestUPDATEInAnEarlyDialog(t *testing.T) {
	updates := make(chan *InDialogRequest, 1)
	arrivedIn := make(chan DialogState, 1)
	updated := make(chan struct{})
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			updates <- r
			// Read before the answer: the 200 lets the callee go on to
			// accept the call.
			d, _ := r.Dialog().Dialog()
			arrivedIn <- d.State
			if _, err := r.Accept(Body{}); !errors.Is(err, ErrNoAnswer) {
				t.Errorf("Accept without a body for an UPDATE with an offer: %v, want ErrNoAnswer", err)
			}
			if _, err := r.Accept(answer2, HeaderField{Name: "Require", Value: OptionPrecondition}); err != nil {
				t.Error(err)
			}
			close(updated)
		})
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		<-updated
		c.Progress(180, Body{})
		c.Accept(Body{})
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	if progress := <-out.Responses(); progress.StatusCode != 183 {
		t.Fatalf("got %s, want 183", startLine(progress))
	}

	// The UPDATE follows the PRACK at once.
	resp, err := out.Update(offer2)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(resp.Body) != string(answer2.Content) || resp.Get("Require") != OptionPrecondition {
		t.Errorf("UPDATE got %s with body %q, want 200 with the second answer", startLine(resp), resp.Body)
	}
	// RFC 3311 5.1: UPDATE is a target refresh request, so the request
	// and its 2xx carry Contact.
	if got := resp.Get("Contact"); got != "<"+calleeUA.cfg.Contact+">" {
		t.Errorf("200 to the UPDATE has Contact %q, want the callee's", got)
	}
	r := <-updates
	if !r.Offer || string(r.Request.Body) != string(offer2.Content) || r.Request.Get("CSeq") != "3 UPDATE" ||
		r.Request.Get("Contact") != "<"+callerUA.cfg.Contact+">" {
		t.Errorf("callee's user got Offer %v and\n%s", r.Offer, r.Request)
	}
	if s := <-arrivedIn; s != DialogEarly {
		t.Errorf("the UPDATE came in a dialog in state %s, want Early", s)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{180, 200}) {
		t.Errorf("after the UPDATE the caller got %v, want 180 and 200", got)
	}
}

// rawCallee answers the INVITE of a UA from a raw socket and keeps the
// dialog as the callee has it.
type rawCallee struct {
	t      *testing.T
	peer   *net.UDPConn
	ua     *UA
	target string
	invite *Message
	dialog Dialog
}

// rawAnswer has a raw callee answer the call of callerUA with a 200 and
// read the ACK.
func rawAnswer(t *testing.T, callerUA *UA, inv Invitation) (*OutgoingCall, *rawCallee) {
	t.Helper()
	p := &rawCallee{t: t, peer: rawSocket(t), ua: callerUA}
	p.target = "sip:" + callee + "@" + addrOf(p.peer).String()
	inv.RequestURI, inv.From = p.target, caller
	if inv.Body.Content == nil {
		inv.Body = offer
	}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	p.invite = readDatagram(t, p.peer)
	ok := forked(p.invite, 200, "callee", p.target, 0)
	answer.addTo(ok)
	p.dialog, err = NewUASDialog(p.invite, ok)
	if err != nil {
		t.Fatal(err)
	}
	send(t, p.peer, ok, callerUA.Stack())
	if ack := readDatagram(t, p.peer); ack.Method != ACK {
		t.Fatalf("got %s, want ACK", startLine(ack))
	}
	responses(t, out)
	return out, p
}

// request sends a request within the dialog from the raw callee.
func (p *rawCallee) request(method Method, body Body, headers ...HeaderField) *Message {
	p.t.Helper()
	req := p.dialog.NewRequest(method)
	addHeaders(req, headers)
	body.addTo(req)
	via := Via{Transport: "UDP", Host: "127.0.0.1", Port: int(addrOf(p.peer).Port()), Params: Params{{Name: "branch", Value: NewBranch()}}}
	req.Prepend("Via", via.String())
	send(p.t, p.peer, req, p.ua.Stack())
	return req
}

// respond answers req, a request of the UA, from the raw callee.
func (p *rawCallee) respond(req *Message, code int, body Body, headers ...HeaderField) {
	p.t.Helper()
	resp := NewResponse(req, code, "")
	addHeaders(resp, headers)
	body.addTo(resp)
	send(p.t, p.peer, resp, p.ua.Stack())
}

// RFC 3311 5.2: an UPDATE whose offer crosses an offer of this UA gets
// 491, and an UPDATE that comes before the last one has its final
// response gets 500 with Retry-After.
func TestUPDATEGlare(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	release := make(chan struct{})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) {
		arrived <- r
		<-release
		r.Accept(answer2)
	}})

	type result struct {
		resp *Message
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := out.Update(offer2)
		done <- result{resp, err}
	}()
	own := readDatagram(t, p.peer)
	if own.Method != UPDATE || own.Get("CSeq") != "2 UPDATE" {
		t.Fatalf("got %s with CSeq %q, want UPDATE with 2 UPDATE", startLine(own), own.Get("CSeq"))
	}
	// While the UA's own offer is open, an offer of the peer is glare.
	p.request(UPDATE, offer2)
	if resp := readDatagram(t, p.peer); resp.StatusCode != 491 {
		t.Fatalf("crossing UPDATE got %s, want 491", startLine(resp))
	}
	// A second offer of this UA has to wait too (5.1).
	if _, err := out.Update(offer2); !errors.Is(err, ErrOfferPending) {
		t.Errorf("second Update with an offer: %v, want ErrOfferPending", err)
	}
	p.respond(own, 200, answer2)
	if r := <-done; r.err != nil || r.resp.StatusCode != 200 {
		t.Fatalf("Update: %v, %v; want 200", r.resp, r.err)
	}

	// The rejected UPDATE changed no state (RFC 3261 12.2.2), so the
	// peer's next one is in order with the next CSeq number, and its
	// offer is taken now.
	p.request(UPDATE, offer2)
	r := <-arrived
	if !r.Offer {
		t.Error("the UPDATE with a body is not marked as an offer")
	}
	p.request(UPDATE, Body{})
	busy := readDatagram(t, p.peer)
	seconds, err := strconv.Atoi(busy.Get("Retry-After"))
	if busy.StatusCode != 500 || err != nil || seconds < 0 || seconds > 10 {
		t.Fatalf("UPDATE before the answer to the last one got %s with Retry-After %q, want 500 with 0 to 10 seconds",
			startLine(busy), busy.Get("Retry-After"))
	}
	if _, err := out.Update(offer2); !errors.Is(err, ErrOfferUnanswered) {
		t.Errorf("Update with an offer while the peer's waits: %v, want ErrOfferUnanswered", err)
	}
	close(release)
	if ok := readDatagram(t, p.peer); ok.StatusCode != 200 || string(ok.Body) != string(answer2.Content) {
		t.Errorf("got %s with body %q, want the 200 with the answer", startLine(ok), ok.Body)
	}
}

// RFC 3311 5.1 and RFC 3261 12.2.2: an UPDATE refreshes the remote
// target. Without a body it leaves the session alone, and a call
// without a handler answers it by itself.
func TestUPDATERefreshesTheRemoteTarget(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, p := rawAnswer(t, callerUA, Invitation{})
	moved := "sip:moved@" + addrOf(p.peer).String()

	p.request(UPDATE, Body{}, HeaderField{Name: "Contact", Value: "<" + moved + ">"})
	if ok := readDatagram(t, p.peer); ok.StatusCode != 200 || ok.Get("Contact") != "<"+callerUA.cfg.Contact+">" {
		t.Fatalf("UPDATE got %s with Contact %q, want 200 with the UA's Contact", startLine(ok), ok.Get("Contact"))
	}
	if d, _ := out.Dialog(); d.RemoteTarget != moved {
		t.Errorf("remote target is %s, want %s", d.RemoteTarget, moved)
	}

	// The other direction (RFC 3261 12.2.1.2): the Contact of the 2xx.
	done := make(chan error, 1)
	go func() {
		_, err := out.Update(Body{})
		done <- err
	}()
	update := readDatagram(t, p.peer)
	if update.Method != UPDATE || update.RequestURI != moved || len(update.Body) != 0 {
		t.Fatalf("got %s %s, want an UPDATE without a body to the new target", startLine(update), update.RequestURI)
	}
	again := "sip:again@" + addrOf(p.peer).String()
	p.respond(update, 200, Body{}, HeaderField{Name: "Contact", Value: "<" + again + ">"})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	go func() { done <- out.Bye() }()
	bye := readDatagram(t, p.peer)
	if bye.Method != BYE || bye.RequestURI != again {
		t.Fatalf("got %s %s, want the BYE at the target of the 2xx", startLine(bye), bye.RequestURI)
	}
	p.respond(bye, 200, Body{})
	if err := <-done; err != nil {
		t.Errorf("Bye: %v", err)
	}
}

// RFC 3311 5.1: before the answer to the offer of the INVITE has come,
// the caller cannot offer again. An UPDATE without an offer may go.
func TestUPDATEOfferWaitsForTheAnswerOfTheINVITE(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	out, err := callerUA.Invite(Invitation{RequestURI: target, From: caller, Body: offer})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := out.Update(Body{}); err == nil {
		t.Error("Update before any dialog succeeded")
	}
	invite := readDatagram(t, peer)
	send(t, peer, forked(invite, 180, "callee", target, 0), callerUA.Stack())
	<-out.Responses()

	if _, err := out.Update(offer2); !errors.Is(err, ErrOfferPending) {
		t.Errorf("Update with an offer before the answer: %v, want ErrOfferPending", err)
	}
	expectSilence(t, peer, 3*testTimers.T1, "the refused UPDATE must not go out")

	done := make(chan *Message, 1)
	go func() {
		resp, _ := out.Update(Body{})
		done <- resp
	}()
	update := readDatagram(t, peer)
	if update.Method != UPDATE || tagOf(update.Get("To")) != "callee" {
		t.Fatalf("got %s to tag %q, want an UPDATE within the early dialog", startLine(update), tagOf(update.Get("To")))
	}
	send(t, peer, NewResponse(update, 200, ""), callerUA.Stack())
	select {
	case resp := <-done:
		if resp == nil || resp.StatusCode != 200 {
			t.Errorf("Update got %v, want 200", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Update did not return")
	}
}

// Without a handler nobody can answer an offer: the UPDATE gets 488 and
// the session stays as it was (RFC 3311 5.2, 5.3), so that the next
// offer is not held up by the refused one.
func TestUPDATEWithAnOfferNeedsAHandler(t *testing.T) {
	e := establish(t)
	for range 2 {
		resp, err := e.out.Update(offer2)
		if err != nil || resp.StatusCode != 488 {
			t.Fatalf("Update with an offer got %v, %v; want 488", resp, err)
		}
	}
	if resp, err := e.in.Update(Body{}); err != nil || resp.StatusCode != 200 {
		t.Errorf("Update without a body got %v, %v; want 200", resp, err)
	}
	select {
	case <-e.out.Done():
		t.Error("a refused UPDATE ended the call")
	default:
	}
}
