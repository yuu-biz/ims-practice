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
	if rs, rr := m.RTCPBandwidth(); rs != 1000 || rr != 3000 {
		t.Errorf("defaults = %d, %d; want 1000, 3000", rs, rr)
	}
	m.SetBandwidth("RR", 2500)
	if rs, rr := m.RTCPBandwidth(); rs != 1500 || rr != 2500 {
		t.Errorf("with RR only = %d, %d; want 1500, 2500", rs, rr)
	}
	m.SetBandwidth("RS", 0)
	if rs, rr := m.RTCPBandwidth(); rs != 0 || rr != 2500 {
		t.Errorf("explicit = %d, %d", rs, rr)
	}
}
