package sdp

import (
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/amr"
)

// mtsiOffer is the one-phase offer of TS 26.114 A.1.1.2.1 (Table
// A.1.2), without the SDPCapNeg lines, plus telephone-event.
const mtsiOffer = "v=0\r\n" +
	"o=- 1 1 IN IP4 192.0.2.20\r\n" +
	"s=-\r\n" +
	"c=IN IP4 192.0.2.20\r\n" +
	"t=0 0\r\n" +
	"m=audio 49152 RTP/AVP 97 98 99 100 101\r\n" +
	"a=rtpmap:97 AMR-WB/16000/1\r\n" +
	"a=fmtp:97 mode-change-capability=2; max-red=220\r\n" +
	"a=rtpmap:98 AMR-WB/16000/1\r\n" +
	"a=fmtp:98 mode-change-capability=2; max-red=220; octet-align=1\r\n" +
	"a=rtpmap:99 AMR/8000/1\r\n" +
	"a=fmtp:99 mode-change-capability=2; max-red=220\r\n" +
	"a=rtpmap:100 AMR/8000/1\r\n" +
	"a=fmtp:100 mode-change-capability=2; max-red=220; octet-align=1\r\n" +
	"a=rtpmap:101 telephone-event/16000\r\n" +
	"a=fmtp:101 0-15\r\n" +
	"a=ptime:20\r\n" +
	"a=maxptime:240\r\n"

func TestCodecs(t *testing.T) {
	s, err := Parse([]byte(mtsiOffer))
	if err != nil {
		t.Fatal(err)
	}
	cs := s.Media[0].Codecs()
	var got []string
	for _, c := range cs {
		got = append(got, c.Rtpmap())
	}
	want := []string{"97 AMR-WB/16000/1", "98 AMR-WB/16000/1", "99 AMR/8000/1", "100 AMR/8000/1", "101 telephone-event/16000"}
	if !slices.Equal(got, want) {
		t.Errorf("Codecs() = %q, want %q", got, want)
	}
	if v, ok := cs[1].Param("octet-align"); !ok || v != "1" {
		t.Errorf("octet-align of 98 = %q, %v", v, ok)
	}
	if v, _ := cs[0].Param("max-red"); v != "220" {
		t.Errorf("max-red of 97 = %q", v)
	}
	if cs[4].Fmtp != "0-15" {
		t.Errorf("fmtp of telephone-event = %q", cs[4].Fmtp)
	}

	// A static payload type needs no rtpmap (RFC 3551 Table 4).
	static, _ := Parse([]byte("v=0\r\no=- 1 1 IN IP4 192.0.2.1\r\ns=-\r\nm=audio 4000 RTP/AVP 0 8\r\n"))
	if c := static.Media[0].Codecs(); c[0].Name != NamePCMU || c[1].Name != "PCMA" || c[0].ClockRate != 8000 {
		t.Errorf("static payload types read as %+v", c)
	}
}

func TestSetCodecsKeepsOtherAttributes(t *testing.T) {
	s, _ := Parse([]byte(mtsiOffer))
	m := &s.Media[0]
	m.SetCodecs(m.Codecs())
	if string(s.Bytes()) != mtsiOffer {
		t.Errorf("SetCodecs(Codecs()) changed the description:\n%s", s.Bytes())
	}
	m.SetCodecs(m.Codecs()[2:3])
	out := string(s.Bytes())
	if !strings.Contains(out, "m=audio 49152 RTP/AVP 99\r\n") || strings.Contains(out, "AMR-WB") || !strings.Contains(out, "a=maxptime:240") {
		t.Errorf("SetCodecs(AMR only):\n%s", out)
	}
}

func TestAMRFormat(t *testing.T) {
	for _, tc := range []struct {
		codec Codec
		want  amr.Format
		ok    bool
	}{
		{Codec{Name: "AMR-WB", ClockRate: 16000, Fmtp: "mode-change-capability=2; max-red=0"}, amr.Format{Codec: amr.AMRWB}, true},
		{Codec{Name: "amr", ClockRate: 8000, Channels: 1, Fmtp: "octet-align=1"}, amr.Format{Codec: amr.AMR, OctetAligned: true}, true},
		{Codec{Name: "AMR", ClockRate: 8000, Fmtp: "octet-align=1; crc=1"}, amr.Format{}, false},
		{Codec{Name: "AMR", ClockRate: 8000, Fmtp: "octet-align=1; robust-sorting=1"}, amr.Format{}, false},
		{Codec{Name: "AMR", ClockRate: 8000, Fmtp: "octet-align=1; interleaving=4"}, amr.Format{}, false},
		{Codec{Name: "AMR", ClockRate: 8000, Channels: 2}, amr.Format{}, false},
		{Codec{Name: "AMR-WB", ClockRate: 8000}, amr.Format{}, false},
		{Codec{Name: "PCMU", ClockRate: 8000}, amr.Format{}, false},
	} {
		got, err := tc.codec.AMRFormat()
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%+v: AMRFormat() = %+v, %v", tc.codec, got, err)
		}
	}
	modes, err := Codec{Name: "AMR", ClockRate: 8000, Fmtp: "mode-set=0,2,4,7"}.ModeSet()
	if err != nil || !slices.Equal(modes, []amr.FrameType{0, 2, 4, 7}) {
		t.Errorf("ModeSet() = %v, %v", modes, err)
	}
	if _, err := (Codec{Name: "AMR", ClockRate: 8000, Fmtp: "mode-set=8"}).ModeSet(); err == nil {
		t.Error("mode-set=8 accepted for AMR; 8 is the SID, not a mode (RFC 4867 8.1)")
	}
}
