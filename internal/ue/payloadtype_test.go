package ue_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// TestCallerSendsWithTheAnswerAndReceivesWithTheOffer is RFC 3264 5.1 and
// 6.1: the SDP of each side lists the payload types that side expects to
// receive. The UE offers AMR-WB as 96 and telephone-event as 100; the
// callee answers with the same formats numbered 110 and 111. The UE then
// sends 110 and 111, and takes in what the callee sends as 96 and 100,
// the numbers of its offer.
func TestCallerSendsWithTheAnswerAndReceivesWithTheOffer(t *testing.T) {
	offered := []sdp.Codec{sdp.VoLTECodecs()[0], sdp.VoLTECodecs()[5]}
	const speechPT, eventsPT = 110, 111
	u, ua, media, callee, calleeMedia := directCaller(t, func(ic *sip.IncomingCall, media *ue.Media) {
		offer, err := sdp.Parse(ic.Request.Body)
		if err != nil {
			ic.Reject(488)
			return
		}
		answer, err := sdp.AudioAnswer(offer, "1", loopback(media.Port()), offered)
		if err != nil {
			ic.Reject(488)
			return
		}
		m := answer.AudioMedia()
		answered := m.Codecs()
		if len(answered) != 2 {
			t.Errorf("the answer has %d formats, want the speech codec and telephone-event", len(answered))
			ic.Reject(488)
			return
		}
		sendSpeech, sendEvents := answered[0], answered[1]
		answered[0].PayloadType, answered[1].PayloadType = speechPT, eventsPT
		m.SetCodecs(answered)
		// The callee numbers the formats its own way, so it accepts what is
		// numbered 110 and 111 and sends what the offer numbered 96 and 100.
		if err := media.SetStream(ue.Stream{Remote: offer.Address(*offer.AudioMedia()),
			Codec: sendSpeech, Events: &sendEvents, ReceiveCodec: answered[0], ReceiveEvents: &answered[1]}); err != nil {
			t.Error(err)
		}
		media.SetFlow(ue.Flow{Send: true, Receive: true})
		media.Start(ue.ToneSpeech(10 * time.Second))
		ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
	})
	ctx, hangUp := context.WithCancel(context.Background())
	sessions := make(chan *ue.Session, 1)
	called := make(chan error, 1)
	go func() {
		called <- u.Call(ctx, ua, directRegistration, callee, media, ue.ToneSpeech(10*time.Second),
			ue.CallOptions{StayUp: true, Codecs: offered, Session: func(s *ue.Session) { sessions <- s }})
	}()
	waitFor(t, "the callee to take in the speech that the UE sends as 110", func() bool {
		return calleeMedia.Stats().PacketsReceived > 2
	})
	waitFor(t, "the UE to take in the speech that the callee sends as 96", func() bool {
		return media.Stats().PacketsReceived > 2
	})
	if stream, ok := media.Stream(); !ok || stream.Codec.PayloadType != speechPT || stream.ReceiveCodec.PayloadType != 96 ||
		stream.Events.PayloadType != eventsPT || stream.ReceiveEvents.PayloadType != 100 {
		t.Errorf("stream %+v, want to send 110 and 111 and to receive 96 and 100", stream)
	}
	session := <-sessions
	// The status gives the send format and, beside it, the receive format
	// that the received counts are made with.
	if st := session.Status(); st.Codec == nil || st.Codec.PayloadType != speechPT ||
		st.ReceiveCodec == nil || st.ReceiveCodec.PayloadType != 96 || st.ReceiveCodec.TelephoneEvent != 100 ||
		st.Media.PacketsReceived == 0 || st.Media.FramesReceived == 0 {
		t.Errorf("status codec %+v, receive codec %+v, media %+v, want to send 110, to receive 96 and 100, and counts of what was received",
			st.Codec, st.ReceiveCodec, st.Media)
	}
	if err := session.SendDTMF("5"); err != nil {
		t.Fatal(err)
	}
	if err := calleeMedia.SendDTMF("7"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the digits that each side sent with the other's number", func() bool {
		_, heardByCallee := calleeMedia.DTMF()
		_, heardByCaller := media.DTMF()
		return heardByCallee == "5" && heardByCaller == "7"
	})
	hangUp()
	select {
	case <-called:
	case <-time.After(5 * time.Second):
		t.Fatal("the call did not end")
	}
}
