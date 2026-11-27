package sip

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// RFC 3262 5: an INVITE without an offer gets the offer in the first
// reliable provisional response, and the PRACK carries the answer. IR.92
// asks a UE to take such an INVITE.
func TestOfferInAReliable183AndAnswerInThePRACK(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			pracks <- r
			r.Accept(Body{})
		})
		// The callee's session description is the offer here.
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		ack, err := c.Accept(Body{})
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.Body = Body{}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	progress := <-out.Responses()
	if progress.StatusCode != 183 || string(progress.Body) != string(answer.Content) {
		t.Fatalf("got %s with body %q, want 183 with the callee's offer", startLine(progress), progress.Body)
	}
	// The UA leaves this PRACK to the user: it has to carry the answer.
	if _, err := out.PRACK(progress, Body{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("PRACK without a body: %v, want ErrNoAnswer", err)
	}
	resp, err := out.PRACK(progress, offer)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PRACK got %v, %v; want 200", resp, err)
	}
	if _, err := out.PRACK(progress, offer); err == nil {
		t.Error("a second PRACK for the same response went out")
	}
	r := <-pracks
	if r.Offer || r.Request.Method != PRACK || string(r.Request.Body) != string(offer.Content) {
		t.Errorf("callee's user got Offer %v and\n%s\nwant the PRACK with the answer", r.Offer, r.Request)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v, want 200", got)
	}
	// The exchange is complete, so the UA sends the ACK itself.
	if a := <-acks; a.err != nil || len(a.ack.Body) != 0 {
		t.Errorf("Accept returned %v, %v; want an ACK without a body", a.ack, a.err)
	}
}

// RFC 3261 13.2.1 and 13.2.2.4: an INVITE without an offer may get the
// offer in the 2xx; then the ACK carries the answer, and the user of
// the call sends it.
func TestOfferInThe2xxAndAnswerInTheACK(t *testing.T) {
	acks := make(chan accepted, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		ack, err := c.Accept(answer)
		acks <- accepted{ack, err}
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.Body = Body{}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{200}) || string(got[0].Body) != string(answer.Content) {
		t.Fatalf("caller got %v, want a 200 with the callee's offer", codes(got))
	}
	select {
	case a := <-acks:
		t.Fatalf("Accept returned %v, %v before the user sent the ACK", a.ack, a.err)
	default:
	}
	if err := out.Ack(Body{}); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("Ack without a body: %v, want ErrNoAnswer", err)
	}
	if err := out.Ack(offer); err != nil {
		t.Fatal(err)
	}
	a := <-acks
	if a.err != nil || string(a.ack.Body) != string(offer.Content) || a.ack.Get("Content-Type") != offer.Type {
		t.Errorf("Accept returned %v, %v; want the ACK with the answer", a.ack, a.err)
	}
	if err := out.Ack(offer); err == nil {
		t.Error("a second Ack succeeded")
	}
}

// RFC 3261 13.2.2.4: a UAC that ends the dialog instead of taking the
// offer of the 2xx puts a valid answer in the ACK, and sends the BYE
// after it. RFC 3264 6: with no stream accepted, the answer has one
// media description for each offered one, in order, with port 0.
func TestByeBeforeTheAnswerRejectsTheOfferInTheACK(t *testing.T) {
	const twoStreams = "v=0\r\n" +
		"o=callee 2890844526 2890844526 IN IP4 192.0.2.10\r\n" +
		"s=-\r\n" +
		"c=IN IP4 192.0.2.10\r\n" +
		"t=0 0\r\n" +
		"m=audio 49170 RTP/AVP 96 0\r\n" +
		"a=rtpmap:96 AMR/8000\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"m=video 51372 RTP/AVP 99\r\n" +
		"a=rtpmap:99 H264/90000\r\n"
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	p := &rawCallee{t: t, peer: rawSocket(t), ua: callerUA}
	p.target = "sip:" + callee + "@" + addrOf(p.peer).String()
	inv := invitation(callerUA)
	inv.RequestURI, inv.From, inv.Body = p.target, caller, Body{}
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	p.invite = readDatagram(t, p.peer)
	ok := forked(p.invite, 200, "callee", p.target, 0)
	Body{Type: "application/sdp", Content: []byte(twoStreams)}.addTo(ok)
	send(t, p.peer, ok, callerUA.Stack())
	responses(t, out)

	hungUp := make(chan error, 1)
	go func() { hungUp <- out.Bye() }()
	ack := readDatagram(t, p.peer)
	if ack.Method != ACK {
		t.Fatalf("got %s, want the ACK before the BYE", startLine(ack))
	}
	want := "v=0\r\n" +
		"o=- " // the session id follows, and is not the same twice
	if ct := ack.Get("Content-Type"); ct != "application/sdp" || !strings.HasPrefix(string(ack.Body), want) {
		t.Fatalf("the ACK has Content-Type %q and body\n%s\nwant an SDP answer", ct, ack.Body)
	}
	answer, err := sdp.Parse(ack.Body)
	if err != nil {
		t.Fatalf("the answer in the ACK does not parse: %v\n%s", err, ack.Body)
	}
	if len(answer.Media) != 2 ||
		answer.Media[0].Type != "audio" || answer.Media[0].Proto != "RTP/AVP" || answer.Media[0].Port != 0 ||
		answer.Media[1].Type != "video" || answer.Media[1].Proto != "RTP/AVP" || answer.Media[1].Port != 0 {
		t.Errorf("the ACK has the answer\n%s\nwant the audio and then the video stream, both with port 0", ack.Body)
	}
	if !answer.Connection.IsValid() || answer.Name == "" {
		t.Errorf("the answer has no c= or s= line:\n%s", ack.Body)
	}
	bye := readDatagram(t, p.peer)
	if bye.Method != BYE {
		t.Fatalf("got %s after the ACK, want the BYE", startLine(bye))
	}
	p.respond(bye, 200, Body{})
	if err := <-hungUp; err != nil {
		t.Fatalf("Bye: %v", err)
	}
}

// RFC 3262 5: after the answer in a reliable provisional response, the
// PRACK may carry a new offer, and the 2xx to the PRACK answers it. A
// B2BUA relays such a PRACK from one leg to the other.
func TestNewOfferInThePRACK(t *testing.T) {
	pracks := make(chan *InDialogRequest, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Handle(func(r *InDialogRequest) {
			pracks <- r
			r.Accept(answer2)
		})
		if err := c.Progress(183, answer); err != nil {
			t.Error(err)
		}
		c.Accept(Body{})
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	inv := invitation(calleeUA)
	inv.ManualPRACK = true
	out, err := callerUA.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	progress := <-out.Responses()
	resp, err := out.PRACK(progress, offer2)
	if err != nil || resp.StatusCode != 200 || string(resp.Body) != string(answer2.Content) {
		t.Fatalf("PRACK got %v, %v; want 200 with the second answer", resp, err)
	}
	if r := <-pracks; !r.Offer || string(r.Request.Body) != string(offer2.Content) {
		t.Errorf("callee's user got Offer %v and body %q, want the second offer", r.Offer, r.Request.Body)
	}
	if got := codes(responses(t, out)); !reflect.DeepEqual(got, []int{200}) {
		t.Errorf("caller got %v, want 200", got)
	}
}
