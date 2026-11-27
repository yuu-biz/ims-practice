package sdp

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

// softphoneOffer looks like what a softphone sends: several codecs,
// telephone-event, and a media-level connection address.
const softphoneOffer = "v=0\r\n" +
	"o=alice 2890844526 2890844526 IN IP4 192.0.2.10\r\n" +
	"s=Talk\r\n" +
	"c=IN IP4 192.0.2.10\r\n" +
	"t=0 0\r\n" +
	"m=audio 49170 RTP/AVP 96 8 0 101\r\n" +
	"c=IN IP4 192.0.2.11\r\n" +
	"b=AS:80\r\n" +
	"b=RS:0\r\n" +
	"b=RR:4000\r\n" +
	"a=rtpmap:96 opus/48000/2\r\n" +
	"a=rtpmap:8 PCMA/8000\r\n" +
	"a=rtpmap:0 PCMU/8000\r\n" +
	"a=rtpmap:101 telephone-event/8000\r\n" +
	"a=sendrecv\r\n" +
	"m=video 51372 RTP/AVP 99\r\n" +
	"a=rtpmap:99 H264/90000\r\n"

func TestParse(t *testing.T) {
	s, err := Parse([]byte(softphoneOffer))
	if err != nil {
		t.Fatal(err)
	}
	if s.Origin.SessionID != "2890844526" || s.Name != "Talk" || len(s.Media) != 2 {
		t.Fatalf("parsed %+v", s)
	}
	addr, pt, err := s.Audio()
	if err != nil || addr != netip.MustParseAddrPort("192.0.2.11:49170") || pt != 96 {
		t.Errorf("Audio() = %v, %d, %v; want the media-level address", addr, pt, err)
	}
	if as, ok := s.Media[0].Bandwidth("AS"); !ok || as != 80 {
		t.Errorf("b=AS = %d, %v; want 80", as, ok)
	}
	if rr, _ := s.Media[0].Bandwidth("RR"); rr != 4000 {
		t.Errorf("b=RR = %d, want 4000 (bit/s, RFC 3556)", rr)
	}
	if v, _ := s.Media[0].Get("rtpmap"); v != "96 opus/48000/2" {
		t.Errorf("first rtpmap = %q", v)
	}
}

func TestRoundTrip(t *testing.T) {
	s, err := Parse([]byte(softphoneOffer))
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(s.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if string(s.Bytes()) != softphoneOffer {
		t.Errorf("Bytes() is not the description that was parsed:\n%s", s.Bytes())
	}
	if string(again.Bytes()) != string(s.Bytes()) {
		t.Errorf("round trip changed the description:\n%s\nvs\n%s", s.Bytes(), again.Bytes())
	}
}

func TestAnswerPicksPCMUAndRejectsTheRest(t *testing.T) {
	offer, _ := Parse([]byte(softphoneOffer))
	answer, err := Answer(offer, "42", netip.MustParseAddrPort("172.28.0.101:40000"))
	if err != nil {
		t.Fatal(err)
	}
	// RFC 3264 6: one answer line per offered stream, in order.
	if len(answer.Media) != 2 {
		t.Fatalf("answer has %d media descriptions, want 2", len(answer.Media))
	}
	audio, video := answer.Media[0], answer.Media[1]
	if audio.Port != 40000 || len(audio.Formats) != 1 || audio.Formats[0] != PCMU {
		t.Errorf("audio answer = %+v", audio)
	}
	if video.Port != 0 {
		t.Errorf("video was not rejected: %+v", video)
	}
	if !strings.Contains(string(answer.Bytes()), "c=IN IP4 172.28.0.101\r\n") {
		t.Errorf("answer:\n%s", answer.Bytes())
	}
}

func TestAnswerDirection(t *testing.T) {
	offer := Offer("1", netip.MustParseAddrPort("192.0.2.1:4000"))
	offer.Media[0].Attributes[2] = Attribute{Name: "sendonly"}
	answer, err := Answer(offer, "2", netip.MustParseAddrPort("192.0.2.2:5000"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := answer.Media[0].Get("recvonly"); !ok {
		t.Errorf("sendonly offer answered %+v, want recvonly", answer.Media[0].Attributes)
	}
}

// TestAnswerDirectionAtSessionLevel is RFC 8866 6.7: a direction
// attribute at session level applies to a media description that has
// none of its own, and one in the media description wins.
func TestAnswerDirectionAtSessionLevel(t *testing.T) {
	for _, tc := range []struct {
		media string // the offered stream's own direction, if any
		want  string
	}{
		{media: "", want: "recvonly"},
		{media: "sendrecv", want: "sendrecv"},
	} {
		offer := Offer("1", netip.MustParseAddrPort("192.0.2.1:4000"))
		offer.Lines = append(offer.Lines, "a=sendonly")
		offer.Media[0].Attributes = offer.Media[0].Attributes[:2]
		if tc.media != "" {
			offer.Media[0].Attributes = append(offer.Media[0].Attributes, Attribute{Name: tc.media})
		}
		parsed, err := Parse(offer.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		answer, err := Answer(parsed, "2", netip.MustParseAddrPort("192.0.2.2:5000"))
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := answer.Media[0].Get(tc.want); !ok {
			t.Errorf("a=sendonly at session level, %q in the stream: answered %+v, want %s", tc.media, answer.Media[0].Attributes, tc.want)
		}
	}
}

func TestAnswerWithoutPCMU(t *testing.T) {
	offer, _ := Parse([]byte(strings.Replace(softphoneOffer, "96 8 0 101", "96 8 101", 1)))
	if _, err := Answer(offer, "2", netip.MustParseAddrPort("192.0.2.2:5000")); !errors.Is(err, ErrNoCommonCodec) {
		t.Errorf("err = %v, want ErrNoCommonCodec", err)
	}
}

func TestSetAudio(t *testing.T) {
	s, _ := Parse([]byte(softphoneOffer))
	relay := netip.MustParseAddrPort("172.28.0.50:30000")
	if err := s.SetAudio(relay); err != nil {
		t.Fatal(err)
	}
	if addr, _, _ := s.Audio(); addr != relay {
		t.Errorf("audio goes to %v, want %v", addr, relay)
	}
}

// TestSetAudioKeepsOtherStreams is RFC 8866 5.7: a stream without a
// c= line of its own uses the session-level one. The address of the
// session must stay while another stream uses it, so that only the
// stream that goes through the relay moves.
func TestSetAudioKeepsOtherStreams(t *testing.T) {
	const offer = "v=0\r\n" +
		"o=- 1 1 IN IP4 192.0.2.1\r\n" +
		"s=-\r\n" +
		"c=IN IP4 192.0.2.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 0\r\n" +
		"m=video 40002 RTP/AVP 96\r\n" +
		"a=rtpmap:96 H264/90000\r\n"
	relay := netip.MustParseAddrPort("172.28.0.50:30000")
	s, err := Parse([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAudio(relay); err != nil {
		t.Fatal(err)
	}
	// Read the result as a receiver of the SDP does.
	got, err := Parse(s.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if addr := got.Address(got.Media[0]); addr != relay {
		t.Errorf("audio goes to %v, want %v", addr, relay)
	}
	if want := netip.MustParseAddrPort("192.0.2.1:40002"); got.Address(got.Media[1]) != want {
		t.Errorf("video goes to %v, want %v", got.Address(got.Media[1]), want)
	}
}

// TestSetAudioWithOwnAddress is the same when the stream that goes
// through the relay has a c= line of its own and another one uses the
// session-level address.
func TestSetAudioWithOwnAddress(t *testing.T) {
	const offer = "v=0\r\n" +
		"o=- 1 1 IN IP4 192.0.2.1\r\n" +
		"s=-\r\n" +
		"c=IN IP4 192.0.2.1\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 0\r\n" +
		"c=IN IP4 192.0.2.9\r\n" +
		"m=video 40002 RTP/AVP 96\r\n"
	relay := netip.MustParseAddrPort("172.28.0.50:30000")
	s, _ := Parse([]byte(offer))
	if err := s.SetAudio(relay); err != nil {
		t.Fatal(err)
	}
	got, _ := Parse(s.Bytes())
	if addr := got.Address(got.Media[0]); addr != relay {
		t.Errorf("audio goes to %v, want %v", addr, relay)
	}
	if want := netip.MustParseAddrPort("192.0.2.1:40002"); got.Address(got.Media[1]) != want {
		t.Errorf("video goes to %v, want %v", got.Address(got.Media[1]), want)
	}
}

// TestHold is RFC 3264 8.4: the side that holds offers sendonly (or
// inactive when it was only receiving), the other side answers
// recvonly (or inactive), and the stream comes back with sendrecv.
func TestHold(t *testing.T) {
	for _, tc := range []struct{ before, offer, answer string }{
		{"sendrecv", "sendonly", "recvonly"},
		{"recvonly", "inactive", "inactive"},
	} {
		s := Offer("1", netip.MustParseAddrPort("192.0.2.1:40000"))
		m := &s.Media[0]
		m.SetDirection(tc.before)
		m.SetDirection(HoldDirection(s.Direction(*m)))
		if got := s.Direction(*m); got != tc.offer {
			t.Errorf("holding a %s stream offers %s, want %s", tc.before, got, tc.offer)
		}
		if n := strings.Count(string(s.Bytes()), "a="+tc.offer); n != 1 {
			t.Errorf("offer has %d a=%s lines:\n%s", n, tc.offer, s.Bytes())
		}
		if got := AnswerDirection(s.Direction(*m)); got != tc.answer {
			t.Errorf("answer to %s is %s, want %s", tc.offer, got, tc.answer)
		}
	}
}
