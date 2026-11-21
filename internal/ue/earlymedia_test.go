package ue_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// pcmuAnswer answers the offer of ic with PCMU at media, and points
// media at the caller.
func pcmuAnswer(t *testing.T, ic *sip.IncomingCall, media *ue.Media) sip.Body {
	t.Helper()
	offer, err := sdp.Parse(ic.Request.Body)
	if err != nil {
		t.Error(err)
		return sip.Body{}
	}
	pcmuOnly, _ := ue.CodecsNamed([]string{sdp.NamePCMU})
	answer, err := sdp.AudioAnswer(offer, "1", loopback(media.Port()), pcmuOnly)
	if err != nil {
		t.Error(err)
		return sip.Body{}
	}
	media.SetStream(ue.Stream{Remote: offer.Address(*offer.AudioMedia()), Codec: pcmu})
	return sip.Body{Type: "application/sdp", Content: answer.Bytes()}
}

// statusLog keeps the status of the caller's session after each change.
type statusLog struct {
	mu       sync.Mutex
	session  *ue.Session
	statuses []ue.SessionStatus
}

func (l *statusLog) options() ue.CallOptions {
	return ue.CallOptions{
		Session: func(s *ue.Session) { l.mu.Lock(); l.session = s; l.mu.Unlock() },
		Changed: func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.session != nil {
				l.statuses = append(l.statuses, l.session.Status())
			}
		},
	}
}

func (l *statusLog) ringback() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []bool
	for _, s := range l.statuses {
		out = append(out, s.LocalRingback)
	}
	return slices.Compact(out)
}

// TestEarlyMediaOnlyWhenAuthorized is RFC 5009 8 and TS 24.229 5.1.3.1:
// before the 200, the caller keeps the media of the callee only when
// P-Early-Media authorizes it.
func TestEarlyMediaOnlyWhenAuthorized(t *testing.T) {
	for _, tc := range []struct {
		name       string
		authorize  []sip.HeaderField
		wantPacket bool
	}{
		{"authorized", []sip.HeaderField{sip.PEarlyMedia{Directions: []string{sip.EarlyMediaSendonly}}.Header()}, true},
		{"not authorized", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u, ua, media, callee, _ := directCaller(t, func(ic *sip.IncomingCall, media *ue.Media) {
				if !ic.Request.Supports(sip.Option100rel) || ic.Request.Get("P-Early-Media") != "supported" {
					t.Errorf("INVITE Supported %q, P-Early-Media %q", ic.Request.GetAll("Supported"), ic.Request.Get("P-Early-Media"))
				}
				if err := ic.Progress(183, pcmuAnswer(t, ic, media), tc.authorize...); err != nil {
					return
				}
				// A ringback tone from the network, before the answer.
				media.SetFlow(ue.Flow{Send: true, Receive: true})
				media.Start(ue.Speech{PCM: ue.Tone(425, 200*time.Millisecond)})
				<-media.Played()
				time.Sleep(50 * time.Millisecond)
				media.SetFlow(ue.Flow{Receive: true})
				ic.Accept(sip.Body{})
			})
			var log statusLog
			if err := u.Call(context.Background(), ua, directRegistration, callee, media, ue.Speech{}, log.options()); err != nil {
				t.Fatal(err)
			}
			_, packets := media.Received()
			if got := packets > 0; got != tc.wantPacket {
				t.Errorf("the caller kept %d packets of early media", packets)
			}
			log.mu.Lock()
			shown := slices.ContainsFunc(log.statuses, func(s ue.SessionStatus) bool {
				return len(s.EarlyMedia) == 1 && !s.LocalRingback
			})
			log.mu.Unlock()
			if shown != tc.wantPacket {
				t.Errorf("a status before the answer showed the early media authorized: %v", shown)
			}
			// No 180 came, so without early media the call did not ring.
			want := ue.Ringback("")
			if tc.wantPacket {
				want = ue.RingbackNetwork
			}
			if last := log.statuses[len(log.statuses)-1]; last.Ringback != want {
				t.Errorf("after the answer the call rang %q, want %q", last.Ringback, want)
			}
		})
	}
}

// TestEarlyDialogTerminatedBy199 is RFC 6228 and TS 24.229 5.1.3.1: a
// callee that turns to the next UAS ends the first early dialog with a
// 199; the caller lets go of it, plays its own ringback again for the
// next one, and takes the answer of the dialog that answers.
func TestEarlyDialogTerminatedBy199(t *testing.T) {
	u, ua, media, callee, calleeMedia := directCaller(t, func(ic *sip.IncomingCall, media *ue.Media) {
		if !ic.Request.Supports(sip.Option199) {
			t.Error("the INVITE does not support 199 (IR.92 2.2.5)")
		}
		if err := ic.Progress(180, sip.Body{}); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
		if err := ic.EarlyDialogTerminated(480); err != nil {
			t.Error(err)
			return
		}
		time.Sleep(50 * time.Millisecond)
		if err := ic.Progress(180, sip.Body{}); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
		body := pcmuAnswer(t, ic, media)
		media.SetFlow(ue.Flow{Receive: true})
		ic.Accept(body)
	})
	var log statusLog
	if err := u.Call(context.Background(), ua, directRegistration, callee, media, ue.Speech{PCM: ue.Tone(440, 100*time.Millisecond)}, log.options()); err != nil {
		t.Fatal(err)
	}
	if got := log.ringback(); !slices.Equal(got, []bool{false, true, false, true, false}) {
		t.Errorf("local ringback went %v; want on for each early dialog with a 180, and off after the 199", got)
	}
	if _, n := calleeMedia.Received(); n != 5 {
		t.Errorf("the second dialog got %d packets, want the 5 the caller sent", n)
	}
}
