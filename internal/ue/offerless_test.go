package ue_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// calledUE is a UE with no IMS in front of it that a bare UA calls.
type calledUE struct {
	ue      *ue.UE
	ua      *sip.UA
	media   *ue.Media
	contact ue.Contact
	calls   chan *sip.IncomingCall
	caller  *sip.UA
	// callerMedia is where the bare caller takes its RTP.
	callerMedia *ue.Media
}

func newCalledUE(t *testing.T, calls ue.CallConfig) *calledUE {
	t.Helper()
	c := &calledUE{calls: make(chan *sip.IncomingCall, 1)}
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	cfg := ue.Config{PrivateIdentity: impi2, PublicIdentity: impu2, Password: password2, HomeDomain: domain,
		PCSCF: addr, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
	c.contact = ue.NewContact(cfg, addr)
	var err error
	c.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy:  addr.String(),
		Contact: c.contact.URI(),
		OnCall:  func(ic *sip.IncomingCall) { c.calls <- ic },
	}, calls), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.ua.Close)
	c.ue = ue.New(cfg, c.ua.Stack(), c.contact, quiet())
	c.media = listenMedia(t)
	c.callerMedia = listenMedia(t)
	c.caller = newTestUA(t, "caller", func(ic *sip.IncomingCall) { ic.Reject(486) })
	return c
}

// answer has the UE answer the next call at once.
func (c *calledUE) answer(t *testing.T, speech ue.Speech) <-chan error {
	t.Helper()
	answered := make(chan error, 1)
	go func() {
		select {
		case ic := <-c.calls:
			answered <- c.ue.Answer(context.Background(), ic, c.media, speech, ue.AnswerOptions{PickUp: time.After(0)})
		case <-time.After(5 * time.Second):
			answered <- context.DeadlineExceeded
		}
	}()
	return answered
}

// answerFor is the bare caller's answer to the UE's offer: PCMU, which
// the UE offers too, at the caller's media.
func (c *calledUE) answerFor(t *testing.T, offer []byte) sip.Body {
	t.Helper()
	o, err := sdp.Parse(offer)
	if err != nil {
		t.Fatal(err)
	}
	pcmuOnly, _ := ue.CodecsNamed([]string{sdp.NamePCMU})
	a, err := sdp.AudioAnswer(o, "1", loopback(c.callerMedia.Port()), pcmuOnly)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.callerMedia.SetStream(ue.Stream{Remote: o.Address(*o.AudioMedia()), Codec: pcmu}); err != nil {
		t.Fatal(err)
	}
	c.callerMedia.SetFlow(ue.Flow{Receive: true})
	return sip.Body{Type: "application/sdp", Content: a.Bytes()}
}

// TestInviteWithoutSDPGetsTheOfferInTheReliable183 is IR.92 2.2.4 and
// RFC 3262 5: the callee offers in its first reliable response, and the
// caller answers in the PRACK.
func TestInviteWithoutSDPGetsTheOfferInTheReliable183(t *testing.T) {
	c := newCalledUE(t, ue.CallConfig{Codecs: voLTECodecs(t)})
	answered := c.answer(t, ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)})
	call, err := c.caller.Invite(sip.Invitation{RequestURI: c.contact.URI(), From: impu})
	if err != nil {
		t.Fatal(err)
	}
	var offered *sip.Message
	for resp := range call.Responses() {
		if resp.StatusCode == 183 {
			offered = resp
			if err := sdpHas(resp, "m=audio "+itoa(c.media.Port())+" RTP/AVP 96 97 98 99 0 100 101", "a=sendrecv"); err != nil {
				t.Error("183: ", err)
			}
			if prack, err := call.PRACK(resp, c.answerFor(t, resp.Body)); err != nil || prack.StatusCode != 200 {
				t.Fatalf("PRACK: %v %v", prack, err)
			}
		}
		if resp.StatusCode == 200 && len(resp.Body) > 0 {
			t.Error("the 200 repeats the offer of the 183")
		}
	}
	if offered == nil {
		t.Fatal("no 183 with the offer")
	}
	waitFor(t, "the callee's PCMU", func() bool { _, n := c.callerMedia.Received(); return n == 10 })
	call.Bye()
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
}

// TestInviteWithoutSDPOrReliableResponses has the offer in the 200 and
// the answer in the ACK (RFC 3261 13.2.1).
func TestInviteWithoutSDPOrReliableResponses(t *testing.T) {
	c := newCalledUE(t, ue.CallConfig{})
	answered := c.answer(t, ue.Speech{PCM: ue.Tone(440, 200*time.Millisecond)})
	call, err := c.caller.Invite(sip.Invitation{RequestURI: c.contact.URI(), From: impu,
		Headers: []sip.HeaderField{{Name: "Supported", Value: sip.OptionTimer}}})
	if err != nil {
		t.Fatal(err)
	}
	var ok *sip.Message
	for resp := range call.Responses() {
		if resp.StatusCode < 200 && len(resp.Body) > 0 {
			t.Errorf("an unreliable %d carries SDP", resp.StatusCode)
		}
		if resp.StatusCode == 200 {
			ok = resp
		}
	}
	if ok == nil || len(ok.Body) == 0 {
		t.Fatalf("200 %v without the offer", ok)
	}
	if err := call.Ack(c.answerFor(t, ok.Body)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the callee's PCMU", func() bool { _, n := c.callerMedia.Received(); return n == 10 })
	call.Bye()
	if err := wait(t, "the answer", answered); err != nil {
		t.Fatal(err)
	}
}
