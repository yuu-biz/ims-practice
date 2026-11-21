package sdp

import (
	"net/netip"
	"testing"
)

// answerEVS parses offer, answers it as this project's UE does with EVS
// and returns the accepted stream.
func answerEVS(t *testing.T, offer string) Media {
	t.Helper()
	o, err := Parse([]byte(offer))
	if err != nil {
		t.Fatal(err)
	}
	a, err := AudioAnswerEVS(o, "9", ueAddr, VoNRCodecs(UEEVS.Configuration), UEEVS)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Parse(a.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	return *again.AudioMedia()
}

// TestAnswerEVSBetweenUEs is the answer of one UE of this project to
// the offer of another: EVS, started in AMR-WB IO by evs-mode-switch=1,
// with the telephone-event of 16 kHz (TS 26.114 G.3).
func TestAnswerEVSBetweenUEs(t *testing.T) {
	offer, err := AudioOffer("7", netip.MustParseAddrPort("172.28.0.100:40002"), VoNRCodecs(UEEVS.Configuration))
	if err != nil {
		t.Fatal(err)
	}
	o, _ := Parse(offer.Bytes())
	a, err := AudioAnswerEVS(o, "9", ueAddr, VoNRCodecs(UEEVS.Configuration), UEEVS)
	if err != nil {
		t.Fatal(err)
	}
	want := "v=0\r\n" +
		"o=- 9 1 IN IP4 172.28.0.101\r\n" +
		"s=-\r\n" +
		"c=IN IP4 172.28.0.101\r\n" +
		"t=0 0\r\n" +
		"m=audio 40000 RTP/AVP 102 100\r\n" +
		"b=AS:42\r\n" +
		"b=RS:0\r\n" +
		"b=RR:4000\r\n" +
		"a=rtpmap:102 EVS/16000/1\r\n" +
		"a=fmtp:102 br=5.9-24.4; bw=nb-swb; evs-mode-switch=1; max-red=0\r\n" +
		"a=rtpmap:100 telephone-event/16000\r\n" +
		"a=fmtp:100 0-15\r\n" +
		"a=ptime:20\r\n" +
		"a=maxptime:240\r\n" +
		"a=sendrecv\r\n"
	if got := string(a.Bytes()); got != want {
		t.Errorf("answer:\n%s\nwant:\n%s", got, want)
	}
}

// evsTerminalOffer is shaped like the offer of TS 26.114 A.14.1.1:
// EVS with every bit rate and bandwidth, then AMR-WB and AMR, and an
// EVS payload type of its own given as fmtp.
func evsTerminalOffer(rtpmap, fmtp string) string {
	return "v=0\r\no=- 1 1 IN IP4 192.0.2.50\r\ns=-\r\nc=IN IP4 192.0.2.50\r\nt=0 0\r\n" +
		"m=audio 49152 RTP/AVP 97 98 99 100\r\nb=AS:145\r\nb=RS:0\r\nb=RR:2000\r\n" +
		"a=rtpmap:97 " + rtpmap + "\r\na=fmtp:97 " + fmtp + "\r\n" +
		"a=rtpmap:98 AMR-WB/16000/1\r\na=fmtp:98 mode-change-capability=2; max-red=220\r\n" +
		"a=rtpmap:99 AMR/8000/1\r\na=fmtp:99 mode-change-capability=2; max-red=220\r\n" +
		"a=rtpmap:100 telephone-event/16000\r\na=fmtp:100 0-15\r\n" +
		"a=ptime:20\r\na=maxptime:240\r\n"
}

func TestAnswerEVSTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, rtpmap, fmtp, want string
		as                       int
	}{
		{"every bit rate, answered with A2", "EVS/16000/1", "max-red=220",
			"97 EVS/16000/1 br=5.9-24.4; bw=nb-swb; evs-mode-switch=1; max-red=0 | 100 telephone-event/16000 0-15", 42},
		{"B1, answered with its mode-set", "EVS/16000", "br=9.6-13.2; bw=swb; max-red=220",
			"97 EVS/16000 br=9.6-13.2; bw=swb; evs-mode-switch=1; mode-set=0,1,2; max-red=0 | 100 telephone-event/16000 0-15", 30},
		// Rejected EVS payload types leave AMR-WB, the next in the
		// offerer's order.
		{"64 kbit/s only (TS 26.114 A.14.2.1)", "EVS/16000/1", "br=64; bw=wb; max-red=220",
			"98 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15", 41},
		{"EVS Primary fixed at the start", "EVS/16000/1", "evs-mode-switch=0",
			"98 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15", 41},
		{"dual-mono", "EVS/16000/2", "br=16.4; bw=swb; max-red=220",
			"98 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15", 41},
		{"parameters that are not valid", "EVS/16000/1", "br=48; bw=nb",
			"98 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15", 41},
	} {
		m := answerEVS(t, evsTerminalOffer(tc.rtpmap, tc.fmtp))
		if got := rtpmaps(m); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
		if as, _ := m.Bandwidth("AS"); as != tc.as {
			t.Errorf("%s: b=AS:%d, want %d", tc.name, as, tc.as)
		}
	}
}

// TestAudioAnswerEVSWithoutEVS is AudioAnswer when this side has no
// EVS: the same answers, byte for byte.
func TestAudioAnswerEVSWithoutEVS(t *testing.T) {
	for i, offer := range guardOffers {
		o, err := Parse([]byte(offer))
		if err != nil {
			t.Fatal(err)
		}
		a, err := AudioAnswerEVS(o, "9", ueAddr, VoLTECodecs(), UEEVS)
		if err != nil || string(a.Bytes()) != guardAnswers[i] {
			t.Errorf("offer %d answered differently from AudioAnswer, %v:\n%s", i, err, a.Bytes())
		}
	}
}

// TestAnswerEVSToVoLTEOffer is a VoNR UE answering a VoLTE UE, which
// offers no EVS: AMR-WB, as AudioAnswer chooses.
func TestAnswerEVSToVoLTEOffer(t *testing.T) {
	m := answerEVS(t, guardOffers[0])
	if got, want := rtpmaps(m), "96 AMR-WB/16000/1 mode-change-capability=2; max-red=0 | 100 telephone-event/16000 0-15"; got != want {
		t.Errorf("answer %q, want %q", got, want)
	}
}
