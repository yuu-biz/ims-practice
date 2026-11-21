package interconnect

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

func TestFilterCodecs(t *testing.T) {
	tests := []struct {
		name        string
		accepted    []string
		wantFormats []int
		wantRtpmaps []string
		wantErr     error
	}{
		{
			name:        "G.711 and DTMF at 8 kHz",
			accepted:    []string{"PCMU", "telephone-event/8000"},
			wantFormats: []int{0, 101},
			wantRtpmaps: []string{"0 PCMU/8000", "101 telephone-event/8000"},
		},
		{
			name:        "AMR-WB in both formats, names without case",
			accepted:    []string{"amr-wb", "telephone-event"},
			wantFormats: []int{96, 97, 100, 101},
			wantRtpmaps: []string{"96 AMR-WB/16000/1", "97 AMR-WB/16000/1", "100 telephone-event/16000", "101 telephone-event/8000"},
		},
		{
			name:     "nothing in common",
			accepted: []string{"EVS", "telephone-event"},
			wantErr:  ErrNoAcceptedCodec,
		},
		{
			name:        "a clock rate that does not match",
			accepted:    []string{"AMR/16000", "PCMU/8000"},
			wantFormats: []int{0},
			wantRtpmaps: []string{"0 PCMU/8000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := sdp.Parse([]byte(voLTEOffer))
			if err != nil {
				t.Fatal(err)
			}
			before := string(s.Bytes())
			err = FilterCodecs(&s, tt.accepted)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("FilterCodecs() = %v, want %v", err, tt.wantErr)
				}
				if string(s.Bytes()) != before {
					t.Error("a failed FilterCodecs changed the SDP")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			m := s.Media[0]
			if !slices.Equal(m.Formats, tt.wantFormats) {
				t.Errorf("formats = %v, want %v", m.Formats, tt.wantFormats)
			}
			var rtpmaps []string
			for _, a := range m.Attributes {
				if a.Name == "rtpmap" {
					rtpmaps = append(rtpmaps, a.Value)
				}
			}
			if !slices.Equal(rtpmaps, tt.wantRtpmaps) {
				t.Errorf("a=rtpmap = %q, want %q", rtpmaps, tt.wantRtpmaps)
			}
			for _, a := range m.Attributes {
				if a.Name == "fmtp" && !slices.ContainsFunc(tt.wantFormats, func(pt int) bool { return strings.HasPrefix(a.Value, strconv.Itoa(pt)+" ") }) {
					t.Errorf("a=fmtp:%s of a removed format is left", a.Value)
				}
			}
			for _, name := range []string{"ptime", "maxptime", "sendrecv"} {
				if _, ok := m.Get(name); !ok {
					t.Errorf("a=%s went too", name)
				}
			}
			if m.Port != 40000 || s.Connection.String() != "192.0.2.30" {
				t.Errorf("the address changed: %s port %d", s.Connection, m.Port)
			}
		})
	}
}

func TestFilterCodecsRejectsOneStream(t *testing.T) {
	const twoStreams = "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=audio 40000 RTP/AVP 0\r\na=rtpmap:0 PCMU/8000\r\n" +
		"m=video 40002 RTP/AVPF 102\r\na=rtpmap:102 H264/90000\r\na=rtcp-fb:102 nack\r\n" +
		"m=audio 0 RTP/AVP 8\r\n"
	s, err := sdp.Parse([]byte(twoStreams))
	if err != nil {
		t.Fatal(err)
	}
	if err := FilterCodecs(&s, []string{"PCMU"}); err != nil {
		t.Fatal(err)
	}
	if s.Media[0].Port != 40000 || s.Media[1].Port != 0 || !slices.Equal(s.Media[1].Formats, []int{102}) || s.Media[2].Port != 0 {
		t.Errorf("SDP:\n%s", s.Bytes())
	}
}

func TestFilterCodecsRemovesRtcpFb(t *testing.T) {
	const video = "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\nt=0 0\r\n" +
		"m=video 40002 RTP/AVPF 102 103\r\na=rtpmap:102 H264/90000\r\na=rtpmap:103 VP8/90000\r\na=rtcp-fb:102 nack\r\na=rtcp-fb:103 nack pli\r\n"
	s, err := sdp.Parse([]byte(video))
	if err != nil {
		t.Fatal(err)
	}
	if err := FilterCodecs(&s, []string{"H264"}); err != nil {
		t.Fatal(err)
	}
	if got := string(s.Bytes()); strings.Contains(got, "103") || !strings.Contains(got, "a=rtcp-fb:102 nack") {
		t.Errorf("SDP:\n%s", got)
	}
}

func TestFilterBody(t *testing.T) {
	m := parse(t, exitInvite)
	if err := FilterBody(m, []string{"PCMU", "telephone-event/8000"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(m.Body), "m=audio 40000 RTP/AVP 0 101\r\n") || strings.Contains(string(m.Body), "AMR") {
		t.Errorf("body:\n%s", m.Body)
	}

	m = parse(t, exitInvite)
	before := string(m.Body)
	if err := FilterBody(m, []string{"EVS"}); !errors.Is(err, ErrNoAcceptedCodec) {
		t.Errorf("FilterBody() = %v, want ErrNoAcceptedCodec", err)
	}
	if string(m.Body) != before {
		t.Error("a failed FilterBody changed the body")
	}

	m = parse(t, exitInvite)
	m.Set("Content-Type", "application/vnd.3gpp.sms")
	m.Body = []byte{0x00, 0x01}
	if err := FilterBody(m, []string{"PCMU"}); err != nil || string(m.Body) != "\x00\x01" {
		t.Errorf("FilterBody() = %v on a body that is not SDP", err)
	}

	m = parse(t, exitInvite)
	m.Body = []byte("not sdp")
	if err := FilterBody(m, []string{"PCMU"}); err == nil {
		t.Error("FilterBody() accepted a broken SDP body")
	}
}

func TestValidateCodecs(t *testing.T) {
	p := peerProfile()
	p.Codecs = []string{"PCMU", "telephone-event/8000"}
	if err := p.Validate(); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "PCMU/x", "telephone event", "AMR/0"} {
		p.Codecs = []string{bad}
		if err := p.Validate(); err == nil {
			t.Errorf("codec %q was accepted", bad)
		}
	}
}
