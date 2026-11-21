package sdp

import (
	"errors"
	"testing"
)

// everything is an answerer that uses every EVS Primary bit rate and
// bandwidth.
var everything = EVSConfiguration{"all", allEVSBitRates, allEVSBandwidths}

// TestAnswerEVSParams checks the rules of TS 26.445 V19.1.0 A.3.3.1 and
// TS 26.114 V20.1.1 Tables 6.3 to 6.3c, and the selection among the
// NG.114 3.2.2.3 configurations, which taking the common part of the
// offer and of this side's configuration gives.
func TestAnswerEVSParams(t *testing.T) {
	for _, tc := range []struct {
		name  string
		offer string
		local EVSCapabilities
		want  string
	}{
		{"A2 to A2", "br=5.9-24.4; bw=nb-swb; max-red=0",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=5.9-24.4; bw=nb-swb; max-red=0"},
		{"A2 to A1 selects A1 with its mode-set", "br=5.9-24.4; bw=nb-swb; max-red=220",
			EVSCapabilities{Configuration: EVSConfigA1}, "br=5.9-13.2; bw=nb-swb; mode-set=0,1,2; max-red=0"},
		{"A2 to B2 selects B2", "br=5.9-24.4; bw=nb-swb; max-red=0",
			EVSCapabilities{Configuration: EVSConfigB2}, "br=9.6-24.4; bw=swb; max-red=0"},
		{"A1 to B2 selects B1", "br=5.9-13.2; bw=nb-swb; max-red=0",
			EVSCapabilities{Configuration: EVSConfigB2}, "br=9.6-13.2; bw=swb; mode-set=0,1,2; max-red=0"},
		{"a single bit rate comes back as offered", "br=13.2; bw=swb; max-red=0",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=13.2; bw=swb; mode-set=0,1,2; max-red=0"},
		{"an offer of everything gets this side's configuration", "max-red=220",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=5.9-24.4; bw=nb-swb; max-red=0"},
		{"the directions swap", "br-send=8-48; br-recv=32-48; bw-send=nb-swb; bw-recv=swb; max-red=220",
			EVSCapabilities{Configuration: everything}, "br-send=32-48; br-recv=8-48; bw-send=swb; bw-recv=nb-swb; max-red=0"},
		{"channels swap", "ch-send=1",
			EVSCapabilities{Configuration: everything}, "br=5.9-128; bw=nb-fb; ch-recv=1; max-red=0"},
		{"unchanged if present, left out if this side need not state them",
			"br=13.2; bw=swb; cmr=-1; dtx=0; dtx-recv=0; hf-only=1; ch-aw-recv=2; mode-change-capability=2; x-new=1",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=13.2; bw=swb; cmr=-1; dtx=0; mode-set=0,1,2; hf-only=1; max-red=0"},
		{"evs-mode-switch=0 stays", "br=5.9-24.4; bw=nb-swb; evs-mode-switch=0",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=5.9-24.4; bw=nb-swb; evs-mode-switch=0; max-red=0"},
		// The network-modified offer of TS 26.114 A.14.2.1.
		{"an offered mode-set stays", "br=5.9-24.4; bw=nb-wb; evs-mode-switch=1; mode-set=0,1,2; mode-change-capability=2; max-red=220",
			EVSCapabilities{Configuration: EVSConfigA2}, "br=5.9-24.4; bw=nb-wb; evs-mode-switch=1; mode-set=0,1,2; max-red=0"},
		{"AMR-WB IO only adds evs-mode-switch=1", "br=5.9-24.4; bw=nb-swb; max-red=0",
			EVSCapabilities{Configuration: EVSConfigA2, AMRWBIOOnly: true}, "br=5.9-24.4; bw=nb-swb; evs-mode-switch=1; max-red=0"},
		{"AMR-WB IO only keeps evs-mode-switch=1", "br=5.9-24.4; bw=nb-swb; evs-mode-switch=1",
			EVSCapabilities{Configuration: EVSConfigA1, AMRWBIOOnly: true}, "br=5.9-13.2; bw=nb-swb; evs-mode-switch=1; mode-set=0,1,2; max-red=0"},
	} {
		offer, err := ParseEVSParams(tc.offer)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		a, err := AnswerEVSParams(offer, tc.local)
		if err != nil || a.String() != tc.want {
			t.Errorf("%s:\n got %q, %v\nwant %q", tc.name, a.String(), err, tc.want)
		}
	}
}

func TestAnswerEVSParamsRefuses(t *testing.T) {
	narrow := EVSConfiguration{"narrow", EVSBitRateRange{EVS7200, EVS8000}, EVSBandwidthRange{EVSSWB, EVSSWB}}
	for _, tc := range []struct {
		name  string
		offer EVSParams
		local EVSCapabilities
	}{
		{"a single bit rate this side does not use",
			EVSParams{BR: Some(EVSBitRateRange{EVS32000, EVS32000})}, EVSCapabilities{Configuration: EVSConfigA2}},
		{"no bit rate in common",
			EVSParams{BR: Some(EVSBitRateRange{EVS5900, EVS8000})}, EVSCapabilities{Configuration: EVSConfigB2}},
		{"no bandwidth in common",
			EVSParams{BW: Some(EVSBandwidthRange{EVSFB, EVSFB})}, EVSCapabilities{Configuration: EVSConfigA2}},
		{"no bit rate in common for the receive direction of the offerer",
			EVSParams{BRRecv: Some(EVSBitRateRange{EVS32000, EVS48000})}, EVSCapabilities{Configuration: EVSConfigA2}},
		{"what is left has no combination in Table A.6",
			EVSParams{BR: Some(EVSBitRateRange{EVS7200, EVS9600}), BW: Some(EVSBandwidthRange{EVSNB, EVSSWB})}, EVSCapabilities{Configuration: narrow}},
		{"dual-mono",
			EVSParams{ChSend: Some(2), ChRecv: Some(2)}, EVSCapabilities{Configuration: everything}},
		{"an offer that is not valid",
			EVSParams{BR: Some(EVSBitRateRange{EVS32000, EVS32000}), BW: Some(EVSBandwidthRange{EVSNB, EVSNB})}, EVSCapabilities{Configuration: everything}},
		{"EVS Primary at the start, which AMR-WB IO only cannot send",
			EVSParams{EVSModeSwitch: Some(false)}, EVSCapabilities{Configuration: EVSConfigA2, AMRWBIOOnly: true}},
	} {
		a, err := AnswerEVSParams(tc.offer, tc.local)
		if !errors.Is(err, ErrEVSNotAnswerable) {
			t.Errorf("%s: answered %q, %v", tc.name, a.String(), err)
		}
	}
}
