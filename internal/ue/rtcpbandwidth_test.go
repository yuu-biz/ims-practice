package ue_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TestCallerTakesTheRTCPBandwidthOfTheSessionLevel is RFC 3556 4: an
// answer that gives b=RR only before its first m= line holds for the
// audio stream as well. The UE then sends RTCP, and does not take the
// missing lines of the media section for b=RS:0 and b=RR:0, which would
// turn RTCP off (IR.92 3.2.4).
func TestCallerTakesTheRTCPBandwidthOfTheSessionLevel(t *testing.T) {
	offered := []sdp.Codec{{PayloadType: 0, Name: sdp.NamePCMU, ClockRate: 8000}}
	u, ua, media, callee, _ := directCaller(t, func(ic *sip.IncomingCall, calleeMedia *ue.Media) {
		offer, err := sdp.Parse(ic.Request.Body)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer, err := sdp.AudioAnswer(offer, "1", loopback(calleeMedia.Port()), offered)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer.Media[0].Bandwidths = nil
		answer.Lines = append(answer.Lines, "b=RR:4000")
		ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
	})
	ctx, hangUp := context.WithCancel(context.Background())
	called := make(chan error, 1)
	go func() {
		called <- u.Call(ctx, ua, directRegistration, callee, media, ue.ToneSpeech(10*time.Second),
			ue.CallOptions{StayUp: true, Codecs: offered})
	}()
	waitFor(t, "the UE to set up the stream of the call", func() bool {
		_, ok := media.Stream()
		return ok
	})
	if stream, _ := media.Stream(); stream.RS != 0 || stream.RR != 4000 {
		t.Errorf("RS, RR = %d, %d; want 0, 4000 from the b=RR:4000 of the session", stream.RS, stream.RR)
	}
	hangUp()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end")
	}
}
