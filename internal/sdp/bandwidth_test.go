package sdp

import (
	"fmt"
	"testing"
)

// TestASBandwidthTables checks every IPv4 entry of TS 26.114 V20.1.1
// Table 6.7 (AMR) and Table 6.8 (AMR-WB), ptime 20.
func TestASBandwidthTables(t *testing.T) {
	tables := []struct {
		name, oa string
		want     []int
	}{
		{NameAMR, "", []int{22, 22, 23, 24, 24, 25, 27, 29}},
		{NameAMR, "; octet-align=1", []int{22, 22, 23, 24, 25, 25, 28, 30}},
		{NameAMRWB, "", []int{24, 26, 30, 31, 33, 35, 37, 40, 41}},
		{NameAMRWB, "; octet-align=1", []int{24, 26, 30, 32, 33, 36, 37, 40, 41}},
	}
	for _, tb := range tables {
		rate := 8000
		if tb.name == NameAMRWB {
			rate = 16000
		}
		for mode, want := range tb.want {
			c := Codec{Name: tb.name, ClockRate: rate, Fmtp: fmt.Sprintf("mode-set=%d%s", mode, tb.oa)}
			if got, err := ASBandwidth(c, 20); err != nil || got != want {
				t.Errorf("%s mode %d%s: b=AS %d, %v; want %d", tb.name, mode, tb.oa, got, err, want)
			}
		}
	}
	// Without a mode-set the highest mode counts (TS 26.114 6.2.5.2).
	if got, _ := ASBandwidth(Codec{Name: NameAMRWB, ClockRate: 16000}, 20); got != 41 {
		t.Errorf("AMR-WB without mode-set: b=AS %d, want 41", got)
	}
	// G.711: 64 kbit/s of samples and 16 kbit/s of headers.
	if got, _ := ASBandwidth(Codec{Name: NamePCMU, ClockRate: 8000}, 20); got != 80 {
		t.Errorf("PCMU: b=AS %d, want 80", got)
	}
}

func TestRTCPBandwidthDefaults(t *testing.T) {
	m := Media{Bandwidths: []Bandwidth{{Type: "AS", Value: 80}}}
	// RFC 3556 3: 5% of 80 kbit/s, a quarter of it for senders.
	if rs, rr := (Session{}).RTCPBandwidth(m); rs != 1000 || rr != 3000 {
		t.Errorf("defaults = %d, %d; want 1000, 3000", rs, rr)
	}
	m.SetBandwidth("RR", 2500)
	if rs, rr := (Session{}).RTCPBandwidth(m); rs != 1500 || rr != 2500 {
		t.Errorf("with RR only = %d, %d; want 1500, 2500", rs, rr)
	}
	m.SetBandwidth("RS", 0)
	if rs, rr := (Session{}).RTCPBandwidth(m); rs != 0 || rr != 2500 {
		t.Errorf("explicit = %d, %d", rs, rr)
	}
}

// TestRTCPBandwidthPrecedence is RFC 3556 4: each of RS and RR is the
// media's explicit line, else the session's, else the default from b=AS
// of the media, else of the session.
func TestRTCPBandwidthPrecedence(t *testing.T) {
	const head = "v=0\r\no=- 1 1 IN IP4 192.0.2.30\r\ns=-\r\nc=IN IP4 192.0.2.30\r\n"
	for _, tc := range []struct {
		name, session, media string
		rs, rr               int
	}{
		{"session level only", "b=RS:500\r\nb=RR:2500\r\nt=0 0\r\n", "", 500, 2500},
		{"session RR only, no AS", "b=RR:4000\r\nt=0 0\r\n", "", 0, 4000},
		{"media overrides session", "b=RS:500\r\nb=RR:2500\r\nt=0 0\r\n", "b=RS:100\r\nb=RR:900\r\n", 100, 900},
		{"media RR with session RS", "b=RS:500\r\nt=0 0\r\n", "b=RR:900\r\n", 500, 900},
		{"both 0 at the media over the session", "b=RS:500\r\nb=RR:2500\r\nt=0 0\r\n", "b=RS:0\r\nb=RR:0\r\n", 0, 0},
		{"both 0 at the session", "b=RS:0\r\nb=RR:0\r\nt=0 0\r\n", "", 0, 0},
		{"session 0 over the default of the media AS", "b=RR:0\r\nt=0 0\r\n", "b=AS:80\r\n", 4000, 0},
		{"default from the AS of the media", "t=0 0\r\n", "b=AS:80\r\n", 1000, 3000},
		{"default from the AS of the session", "b=AS:80\r\nt=0 0\r\n", "", 1000, 3000},
		{"AS of the media over that of the session", "b=AS:80\r\nt=0 0\r\n", "b=AS:40\r\n", 500, 1500},
		{"nothing given", "t=0 0\r\n", "", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse([]byte(head + tc.session + "m=audio 5004 RTP/AVP 0\r\n" + tc.media))
			if err != nil {
				t.Fatal(err)
			}
			if rs, rr := s.RTCPBandwidth(s.Media[0]); rs != tc.rs || rr != tc.rr {
				t.Errorf("RS, RR = %d, %d; want %d, %d", rs, rr, tc.rs, tc.rr)
			}
		})
	}
}
