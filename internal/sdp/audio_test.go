package sdp

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"
)

var ueAddr = netip.MustParseAddrPort("172.28.0.101:40000")

func TestAudioOffer(t *testing.T) {
	s, err := AudioOffer("7", netip.MustParseAddrPort("172.28.0.100:40002"), VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	want := "v=0\r\n" +
		"o=- 7 1 IN IP4 172.28.0.100\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.100\r\n" +
		"t=0 0\r\n" +
		"m=audio 40002 RTP/AVP 96 97 98 99 0 100 101\r\n" +
		"b=AS:80\r\n" + // PCMU needs the most (decision 7)
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:96 AMR-WB/16000/1\r\n" +
		"a=fmtp:96 mode-change-capability=2; max-red=0\r\n" +
		"a=rtpmap:97 AMR-WB/16000/1\r\n" +
		"a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1\r\n" +
		"a=rtpmap:98 AMR/8000/1\r\n" +
		"a=fmtp:98 mode-change-capability=2; max-red=0\r\n" +
		"a=rtpmap:99 AMR/8000/1\r\n" +
		"a=fmtp:99 mode-change-capability=2; max-red=0; octet-align=1\r\n" +
		"a=rtpmap:0 PCMU/8000\r\n" +
		"a=rtpmap:100 telephone-event/16000\r\n" +
		"a=fmtp:100 0-15\r\n" +
		"a=rtpmap:101 telephone-event/8000\r\n" +
		"a=fmtp:101 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n"
	if got := string(s.Bytes()); got != want {
		t.Errorf("offer:\n%s\nwant:\n%s", got, want)
	}
}

// answer parses offer, answers it with the VoLTE codecs and returns the
// accepted stream.
func answer(t *testing.T, offer string) (Session, Media) {
	t.Helper()
	o, err := Parse([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	a, err := AudioAnswer(o, "9", ueAddr, VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(a.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return again, *again.AudioMedia()
}

func rtpmaps(m Media) string {
	var out []string
	for _, c := range m.Codecs() {
		out = append(out, c.Rtpmap()+" "+c.Fmtp)
	}
	return strings.Join(out, " | ")
}

func TestAnswerToVoLTEOffer(t *testing.T) {
	offer, _ := AudioOffer("7", netip.MustParseAddrPort("172.28.0.100:40002"), VoLTECodecs())
	_, m := answer(t, string(offer.Bytes()))
	if got, want := rtpmaps(m), "96 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15"; got != want {
		t.Errorf("answer codecs %q, want %q", got, want)
	}
	if as, _ := m.Bandwidth("AS"); as != 41 {
		t.Errorf("b=AS:%d, want 41 for AMR-WB 23.85", as)
	}
	if v, _ := m.Get("maxptime"); v != "240" {
		t.Errorf("maxptime %q", v)
	}
}

// TestAnswerToMTSIOffer answers the offer of TS 26.114 A.1.1.2.1: the
// payload type numbers stay, and max-red is this side's own.
func TestAnswerToMTSIOffer(t *testing.T) {
	_, m := answer(t, mtsiOffer)
	if got, want := rtpmaps(m), "97 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 101 telephone-event/16000 0-15"; got != want {
		t.Errorf("answer codecs %q, want %q", got, want)
	}
}

// TestAnswerKeepsModeSetAndOctetAlign is RFC 4867 8.3.1: octet-align
// and mode-set go back as offered, and b=AS fits the highest mode of the
// mode-set (TS 26.114 6.2.5.2). The telephone-event follows the clock
// rate of the chosen codec (TS 26.114 G.3).
func TestAnswerKeepsModeSetAndOctetAlign(t *testing.T) {
	offer := "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=audio 5004 RTP/AVP 110 111 112\r\n" +
		"a=rtpmap:110 AMR/8000\r\na=fmtp:110 mode-set=0,2,4,7; octet-align=1; foo=bar\r\n" +
		"a=rtpmap:111 telephone-event/16000\r\n" +
		"a=rtpmap:112 telephone-event/8000\r\n"
	_, m := answer(t, offer)
	if got, want := rtpmaps(m), "110 AMR/8000 mode-set=0,2,4,7; mode-change-capability=2; max-red=0; octet-align=1 | 112 telephone-event/8000 0-15"; got != want {
		t.Errorf("answer codecs %q, want %q", got, want)
	}
	if as, _ := m.Bandwidth("AS"); as != 30 {
		t.Errorf("b=AS:%d, want 30 for octet-aligned AMR 12.2", as)
	}
}

func TestAnswerFollowsOfferersOrder(t *testing.T) {
	offer := "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=audio 5004 RTP/AVP 98 96\r\n" +
		"a=rtpmap:98 AMR/8000/1\r\na=rtpmap:96 AMR-WB/16000/1\r\n"
	_, m := answer(t, offer)
	if c := m.Codecs(); len(c) != 1 || c[0].Name != NameAMR {
		t.Errorf("answer %q; the offer prefers AMR", rtpmaps(m))
	}
}

// TestAnswerToSoftphone keeps the PCMU path: the softphone offers no
// AMR, so PCMU and its telephone-event are chosen, and the video stream
// is rejected.
func TestAnswerToSoftphone(t *testing.T) {
	a, m := answer(t, softphoneOffer)
	if got, want := rtpmaps(m), "0 PCMU/8000  | 101 telephone-event/8000 0-15"; got != want {
		t.Errorf("answer codecs %q, want %q", got, want)
	}
	if len(a.Media) != 2 || a.Media[1].Port != 0 {
		t.Errorf("video was not rejected: %+v", a.Media)
	}
	// The softphone offers b=RS:0, so the answer has RS 0 as well (IR.92 3.2.4).
	if rs, _ := m.Bandwidth("RS"); rs != 0 {
		t.Errorf("b=RS:%d, want 0", rs)
	}
	if as, _ := m.Bandwidth("AS"); as != 80 {
		t.Errorf("b=AS:%d, want 80", as)
	}
}

func TestAnswerRTCPOff(t *testing.T) {
	offer := "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=audio 5004 RTP/AVP 0\r\nb=RS:0\r\nb=RR:0\r\na=sendonly\r\n"
	_, m := answer(t, offer)
	if rs, rr := (Session{}).RTCPBandwidth(m); rs != 0 || rr != 0 {
		t.Errorf("RTCP bandwidth %d, %d; IR.92 3.2.4 answers zero with zero", rs, rr)
	}
	if _, ok := m.Get("recvonly"); !ok {
		t.Errorf("a sendonly offer is not answered recvonly: %+v", m.Attributes)
	}
}

func TestAnswerRefusesWhatItCannotPack(t *testing.T) {
	offer := "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=audio 5004 RTP/AVP 96 97\r\n" +
		"a=rtpmap:96 AMR-WB/16000\r\na=fmtp:96 octet-align=1; crc=1\r\n" +
		"a=rtpmap:97 opus/48000/2\r\n"
	o, _ := Parse([]byte(offer))
	if _, err := AudioAnswer(o, "9", ueAddr, VoLTECodecs()); !errors.Is(err, ErrNoCommonCodec) {
		t.Errorf("err = %v, want ErrNoCommonCodec", err)
	}
}

// TestVoNROfferHasNoEVS checks that the offer for NR access names only
// what the UE sends: AMR-WB, then AMR (TS 26.114 5.2.1.6), PCMU and
// telephone-event, and no EVS, for which the UE has no codec.
func TestVoNROfferHasNoEVS(t *testing.T) {
	o, err := AudioOffer("7", netip.MustParseAddrPort("172.28.0.100:40002"), VoNRCodecs())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(o.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range parsed.AudioMedia().Codecs() {
		names = append(names, c.Name)
	}
	want := []string{NameAMRWB, NameAMRWB, NameAMR, NameAMR, NamePCMU, NameTelephoneEvent, NameTelephoneEvent}
	if !slices.Equal(names, want) {
		t.Errorf("offered %q, want %q", names, want)
	}
}
