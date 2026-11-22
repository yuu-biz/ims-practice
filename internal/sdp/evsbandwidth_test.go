package sdp

import "testing"

// TestEVSASBandwidthTables checks every bit rate of TS 26.114 V20.1.1
// Table Q.1 (ptime 20) and Table Q.3 (ptime 40), IPv4, and of Table 6.9
// with hf-only=1. mode-set=0 keeps EVS AMR-WB IO below each of them.
// Table 6.9 gives 24 for 7.2 kbit/s and Table Q.1 25: Q.1 pads the
// Header-Full payload off the Compact size of 8 kbit/s (Q.4 NOTE 3),
// which hf-only=1 does without (TS 26.445 A.3.1).
func TestEVSASBandwidthTables(t *testing.T) {
	rates := []string{"7.2", "8", "9.6", "13.2", "16.4", "24.4", "32", "48", "64", "96", "128"}
	for _, tb := range []struct {
		name   string
		extra  string
		ptime  int
		values []int
	}{
		{"Table Q.1", "", 20, []int{25, 25, 27, 30, 34, 42, 49, 65, 81, 113, 145}},
		{"Table Q.3", "", 40, []int{16, 17, 19, 22, 25, 33, 41, 57, 73, 105, 137}},
		{"Table 6.9", "; hf-only=1", 20, []int{24, 25, 27, 30, 34, 42, 49, 65, 81, 113, 145}},
	} {
		for i, want := range tb.values {
			c := Codec{Name: NameEVS, ClockRate: 16000, Fmtp: "br=" + rates[i] + "; bw=wb; mode-set=0" + tb.extra}
			if got, err := ASBandwidth(c, tb.ptime); err != nil || got != want {
				t.Errorf("%s, %s kbit/s: b=AS %d, %v; want %d", tb.name, rates[i], got, err, want)
			}
		}
	}
}

// TestEVSASBandwidthExamples checks the b=AS of the EVS offers and
// answers of TS 26.114 V20.1.1 A.14, IPv4.
func TestEVSASBandwidthExamples(t *testing.T) {
	for _, tc := range []struct {
		where string
		fmtp  string
		ptime int
		want  int
	}{
		{"A.14.1.1, 128 kbit/s", "max-red=220", 20, 145},
		{"A.14.1.2", "br=5.9-24.4; bw=nb-wb; max-red=200", 40, 33},
		{"A.14.1.3", "br=5.9-24.4; bw=nb-swb; max-red=220", 20, 42},
		{"A.14.2.1", "br=64; bw=wb; mode-change-capability=2; max-red=220", 20, 81},
		{"A.14.3.1, 13.2 or AMR-WB IO 12.65", "br=5.9-13.2; bw=nb; mode-set=0,1,2; max-red=220", 20, 30},
		{"A.14.3.2", "br=7.2-32; bw=nb-wb; max-red=220", 20, 49},
		{"A.14.3.3", "br=9.6-32; bw=wb; mode-set=0,1,2; hf-only=1; max-red=220", 20, 49},
		{"A.14.3.4, br-recv counts", "br-send=8-48; br-recv=32-48; bw-send=nb-swb; bw-recv=swb; max-red=220", 20, 65},
		{"A.14.3.5", "br=16.4-48; bw=swb; cmr=-1; max-red=220", 20, 65},
		{"A.14.3.6, four frames a packet", "br=13.2-32; ch-aw-recv=3; max-red=160", 80, 37},
		{"A.14.3.9, SC-VBR as 8 kbit/s", "br=5.9; bw=nb-wb; mode-set=0; max-red=220", 20, 25},
		{"A.14.4.1", "br=13.2; bw=nb; dtx=0; mode-set=0,1,2; max-red=220", 20, 30},
		{"A.14.4.2", "br=64; bw=wb; dtx=0; max-red=220", 20, 81},
		{"A.14.4.3", "br=96; bw=swb; dtx=0; max-red=220", 20, 113},
		// Without mode-set EVS AMR-WB IO may reach 23.85, which needs more.
		{"AMR-WB IO above EVS Primary", "br=5.9; bw=nb", 20, 41},
		// Without br, narrowband stops at 24.4 (TS 26.114 6.2.5.2).
		{"narrowband only", "bw=nb; mode-set=0", 20, 42},
	} {
		c := Codec{Name: NameEVS, ClockRate: 16000, Channels: 1, Fmtp: tc.fmtp}
		if got, err := ASBandwidth(c, tc.ptime); err != nil || got != tc.want {
			t.Errorf("%s: b=AS %d, %v; want %d", tc.where, got, err, tc.want)
		}
	}
	if _, err := ASBandwidth(Codec{Name: NameEVS, ClockRate: 16000, Fmtp: "br=128; bw=nb"}, 20); err == nil {
		t.Error("b=AS for a br and bw that Table A.6 does not allow")
	}
}
