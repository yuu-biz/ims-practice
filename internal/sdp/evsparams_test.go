package sdp

import (
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// TestEVSParamsRoundTrip reads and writes again a=fmtp values of the
// shapes TS 26.114 V20.1.1 A.14 uses in its EVS offers and answers. The
// order of String is chosen so that each comes back as it was.
func TestEVSParamsRoundTrip(t *testing.T) {
	for _, fmtp := range []string{
		"max-red=220", // A.14.1.1: all bit rates and bandwidths
		"br=5.9-24.4; bw=nb-swb; max-red=220",
		"br=64; bw=wb; mode-change-capability=2; max-red=220",
		"br=5.9-24.4; bw=nb-wb; evs-mode-switch=1; mode-set=0,1,2; mode-change-capability=2; max-red=220",
		"br=5.9-13.2; bw=nb; mode-set=0,1,2; max-red=220",
		"br=9.6-32; bw=wb; mode-set=0,1,2; hf-only=1; max-red=220",
		"br-send=8-48; br-recv=32-48; bw-send=nb-swb; bw-recv=swb; max-red=220",
		"br=16.4-48; bw=swb; cmr=-1; max-red=220",
		"br=13.2-32; ch-aw-recv=3; max-red=160",
		"br=16.4; bw=swb; ch-send=2; ch-recv=2; max-red=220",
		"br=5.9; bw=nb-wb; mode-set=0; max-red=220",
		"br=13.2; bw=nb; dtx=0; mode-set=0,1,2; max-red=220",
		"dtx-recv=0; mode-change-period=2; mode-change-neighbor=1",
		// Parameters A.3.1 does not define stay, after the others.
		"br=13.2; bw=swb; max-red=0; x-vendor=7; flag",
		"",
	} {
		p, err := ParseEVSParams(fmtp)
		if err != nil {
			t.Errorf("%q: %v", fmtp, err)
			continue
		}
		if got := p.String(); got != fmtp {
			t.Errorf("%q came back as %q", fmtp, got)
		}
	}
}

func TestParseEVSParams(t *testing.T) {
	p, err := ParseEVSParams(" BR=8.0-24.4 ;bw = nb-swb; mode-set=8,0 ; hf-only=1; evs-mode-switch=1")
	if err != nil {
		t.Fatal(err)
	}
	if p.BR != Some(EVSBitRateRange{EVS8000, EVS24400}) || p.BW != Some(EVSBandwidthRange{EVSNB, EVSSWB}) {
		t.Errorf("br, bw = %+v, %+v", p.BR, p.BW)
	}
	if !slices.Equal(p.ModeSet, []evs.FrameType{evs.AMRWBIO23850, evs.AMRWBIO6600}) {
		t.Errorf("mode-set = %v", p.ModeSet)
	}
	if !p.HFOnly.Value || p.StartMode() != evs.AMRWBIO {
		t.Errorf("hf-only %+v, start mode %v", p.HFOnly, p.StartMode())
	}
	if p.DTX.Present || p.CMR.Present {
		t.Errorf("absent parameters read as present: %+v", p)
	}
	if q, _ := ParseEVSParams("evs-mode-switch=0"); q.StartMode() != evs.Primary || !q.EVSModeSwitch.Present {
		t.Errorf("evs-mode-switch=0 read as %+v", q.EVSModeSwitch)
	}
}

// TestEVSParamsRefused checks the values and combinations TS 26.445
// V19.1.0 A.3.1 and A.3.3.1 (Table A.6) do not allow.
func TestEVSParamsRefused(t *testing.T) {
	for _, tc := range []struct{ fmtp, why string }{
		{"br=6", "not a bit rate of the set"},
		{"br=24.4-13.2", "br1 must be smaller than br2"},
		{"br=13.2-13.2", "br1 must be smaller than br2"},
		{"bw=wb-swb", "ranges start at nb"},
		{"bw=nb-nb", "not a value of the set"},
		{"evs-mode-switch=2", "0 or 1"},
		{"hf-only=yes", "0 or 1"},
		{"cmr=2", "-1, 0 or 1"},
		{"ch-aw-recv=4", "-1, 0, 2, 3, 5 or 7"},
		{"ch-send=0", "at least one channel"},
		{"mode-set=0,9", "EVS AMR-WB IO has modes 0 to 8"},
		{"mode-set=1,1", "a mode twice"},
		{"mode-change-capability=1", "2 is the only value for EVS AMR-WB IO"},
		{"mode-change-period=3", "1 or 2 (RFC 4867)"},
		{"max-red=-20", "0 to 65535 (RFC 4867)"},
		{"br=13.2; br=24.4", "a parameter twice"},
		{"br=13.2; br-send=24.4", "br-send must be identical to br"},
		{"bw=swb; bw-recv=nb-swb", "bw-recv must be identical to bw"},
		{"dtx=1; dtx-recv=0", "dtx-recv must be identical to dtx"},
		{"br=32; bw=nb", "Table A.6: no 32 kbit/s in narrowband"},
		{"br=5.9-8; bw=swb", "Table A.6: super-wideband starts at 9.6"},
		{"br-recv=128; bw-recv=nb", "Table A.6 for the receive direction"},
	} {
		if p, err := ParseEVSParams(tc.fmtp); err == nil {
			t.Errorf("%q accepted as %+v; %s", tc.fmtp, p, tc.why)
		}
	}
}

// TestEVSTableA6 checks highestUsable against TS 26.445 Table A.6.
func TestEVSTableA6(t *testing.T) {
	for _, tc := range []struct {
		br   EVSBitRateRange
		bw   EVSBandwidthRange
		want string // "" for none
	}{
		{allEVSBitRates, EVSBandwidthRange{EVSNB, EVSNB}, "24.4"},
		{allEVSBitRates, EVSBandwidthRange{EVSWB, EVSWB}, "128"},
		{EVSBitRateRange{EVS5900, EVS13200}, EVSBandwidthRange{EVSNB, EVSSWB}, "13.2"},
		{EVSBitRateRange{EVS5900, EVS8000}, EVSBandwidthRange{EVSSWB, EVSSWB}, ""},
		{EVSBitRateRange{EVS9600, EVS9600}, EVSBandwidthRange{EVSSWB, EVSSWB}, "9.6"},
		{EVSBitRateRange{EVS13200, EVS13200}, EVSBandwidthRange{EVSFB, EVSFB}, ""},
		{EVSBitRateRange{EVS16400, EVS16400}, EVSBandwidthRange{EVSFB, EVSFB}, "16.4"},
		{EVSBitRateRange{EVS5900, EVS32000}, EVSBandwidthRange{EVSNB, EVSNB}, "24.4"},
	} {
		got, ok := highestUsable(tc.br, tc.bw)
		if s := got.String(); (tc.want == "") == ok || (ok && s != tc.want) {
			t.Errorf("br=%s bw=%s: %s, %v; want %q", tc.br, tc.bw, s, ok, tc.want)
		}
	}
}

func TestEVSParamsKeepsUnknownParameters(t *testing.T) {
	p, err := ParseEVSParams("future=1; br=13.2")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.Other, []string{"future=1"}) || !strings.HasPrefix(p.String(), "br=13.2") {
		t.Errorf("other %q, written %q", p.Other, p.String())
	}
}
