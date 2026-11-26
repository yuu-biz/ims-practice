package ue_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ue"
)

// activeCall is a call between two UEs through the whole IMS that stays
// up until the test hangs up, with the session of each side.
type activeCall struct {
	caller, callee               *party
	callerSession, calleeSession *ue.Session
	hangUp                       func()
	called, answered             <-chan error
}

func startActiveCall(t *testing.T, calls ue.CallConfig, speech ue.Speech) *activeCall {
	t.Helper()
	addr := network(t, time.Second)
	c := &activeCall{
		caller: newPartyWith(t, addr, impi, impu, password, calls),
		callee: newPartyWith(t, addr, impi2, impu2, password2, calls),
	}
	var mu sync.Mutex
	got := func(dst **ue.Session) func(*ue.Session) {
		return func(s *ue.Session) { mu.Lock(); *dst = s; mu.Unlock() }
	}
	ctx, hangUp := context.WithCancel(context.Background())
	c.hangUp = hangUp
	answered := make(chan error, 1)
	go func() {
		ic := <-c.callee.calls
		answered <- c.callee.ue.Answer(ctx, ic, c.callee.media, speech, ue.AnswerOptions{PickUp: time.After(0), Session: got(&c.calleeSession)})
	}()
	called := make(chan error, 1)
	go func() {
		called <- c.caller.ue.Call(ctx, c.caller.ua, c.caller.reg, impu2, c.caller.media, speech,
			ue.CallOptions{StayUp: true, Session: got(&c.callerSession)})
	}()
	c.called, c.answered = called, answered
	c.caller.wire.find(t, "> ACK")
	waitFor(t, "media both ways", func() bool {
		return c.caller.media.Stats().PacketsReceived > 2 && c.callee.media.Stats().PacketsReceived > 2
	})
	mu.Lock()
	defer mu.Unlock()
	return c
}

func (c *activeCall) end(t *testing.T) {
	t.Helper()
	c.hangUp()
	wait(t, "the call", c.called)
	wait(t, "the answer", c.answered)
}

// still checks that neither side sends RTP. Each side's RTP clock moves
// on every packet time, whether a packet goes out or not. Two packet
// times after the flow changed, a packet that was going out as it
// changed has been counted; in the two after that, nothing goes out.
func (c *activeCall) still(t *testing.T, why string) {
	t.Helper()
	sides := []*ue.Media{c.caller.media, c.callee.media}
	for _, m := range sides {
		packetTimes(t, m, 2)
	}
	sent := []int{sides[0].Stats().PacketsSent, sides[1].Stats().PacketsSent}
	for _, m := range sides {
		packetTimes(t, m, 2)
	}
	if a, b := sides[0].Stats().PacketsSent-sent[0], sides[1].Stats().PacketsSent-sent[1]; a != 0 || b != 0 {
		t.Errorf("%s: RTP still flows: the caller sent %d packets more, the callee %d", why, a, b)
	}
}

func (c *activeCall) flowing(t *testing.T, why string) {
	t.Helper()
	a, b := c.caller.media.Stats().PacketsReceived, c.callee.media.Stats().PacketsReceived
	if !came(watch, func() bool {
		return c.caller.media.Stats().PacketsReceived > a+2 && c.callee.media.Stats().PacketsReceived > b+2
	}) {
		t.Errorf("%s: no RTP either way within %v", why, watch)
	}
}

// TestHoldAndResume is RFC 3264 8.4 with IR.92 2.3.10: the holder offers
// sendonly in a re-INVITE, the held party answers recvonly, and no
// media flows either way until the holder resumes with sendrecv. Either
// side may hold.
func TestHoldAndResume(t *testing.T) {
	c := startActiveCall(t, ue.CallConfig{}, ue.Speech{PCM: ue.Tone(440, 10*time.Second)})
	defer c.end(t)
	for _, side := range []struct {
		name         string
		holder, held *ue.Session
		heldParty    *party
	}{
		{"caller holds", c.callerSession, c.calleeSession, c.callee},
		{"callee holds", c.calleeSession, c.callerSession, c.caller},
	} {
		if err := side.holder.Hold(); err != nil {
			t.Fatalf("%s: %v", side.name, err)
		}
		hs, ps := side.holder.Status(), side.held.Status()
		if !hs.Held || hs.Direction != "sendonly" || hs.PeerDirection != "recvonly" || ps.Direction != "recvonly" || ps.Held {
			t.Errorf("%s: holder %+v, held %+v", side.name, hs, ps)
		}
		c.still(t, side.name)
		if err := side.holder.Resume(); err != nil {
			t.Fatalf("%s, resuming: %v", side.name, err)
		}
		if hs := side.holder.Status(); hs.Held || hs.Direction != "sendrecv" || hs.PeerDirection != "sendrecv" {
			t.Errorf("%s, resumed: holder %+v", side.name, hs)
		}
		c.flowing(t, side.name+": media again after the resume")
	}
	reinvites := 0
	for _, l := range c.callee.wire.labels() {
		if l == "< INVITE" {
			reinvites++
		}
	}
	if reinvites != 3 {
		t.Errorf("the callee got %d INVITEs, want the call and the hold and resume of the caller", reinvites)
	}
}

// TestDTMFBothWays is TS 26.114 Annex G and IR.92 3.3 in a VoLTE call:
// telephone-event of 16 kHz with AMR-WB, each digit once at the other
// side.
func TestDTMFBothWays(t *testing.T) {
	c := startActiveCall(t, ue.CallConfig{Codecs: voLTECodecs(t)}, ue.ToneSpeech(10*time.Second))
	defer c.end(t)
	if codec := c.callerSession.Status().Codec; codec == nil || codec.Name != "AMR-WB" || codec.TelephoneEvent != 100 {
		t.Fatalf("codec %+v, want AMR-WB with telephone-event 100", codec)
	}
	if err := c.callerSession.SendDTMF("123#"); err != nil {
		t.Fatal(err)
	}
	if err := c.calleeSession.SendDTMF("*0"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the digits", func() bool {
		return c.calleeSession.Status().DTMFReceived == "123#" && c.callerSession.Status().DTMFReceived == "*0"
	})
	if s := c.callerSession.Status(); s.DTMFSent != "123#" {
		t.Errorf("caller sent %q", s.DTMFSent)
	}
	if err := c.callerSession.SendDTMF("Z"); err == nil || !strings.Contains(err.Error(), "DTMF") {
		t.Errorf("a letter: %v", err)
	}
}
