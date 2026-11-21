package sdp

import (
	"fmt"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// checkInitialOffer is what TS 26.114 V20.1.1 Table 6.2a and NG.114
// 3.2.2.3 ask of the EVS parameters of a terminal's initial offer.
func checkInitialOffer(p EVSParams) error {
	switch {
	case p.EVSModeSwitch.Present, p.DTX.Present, p.DTXRecv.Present:
		return fmt.Errorf("evs-mode-switch, dtx or dtx-recv in %q (Table 6.2a)", p)
	case p.ModeSet != nil, p.ModeChangePeriod.Present, p.ModeChangeNeighbor.Present:
		return fmt.Errorf("mode-set, mode-change-period or mode-change-neighbor in %q (Table 6.2a)", p)
	case !p.MaxRed.Present || p.MaxRed.Value > 220:
		return fmt.Errorf("max-red missing or above 220 in %q (Table 6.2a)", p)
	}
	only := EVSParams{BR: p.BR, BW: p.BW, MaxRed: p.MaxRed, ChAwRecv: p.ChAwRecv}
	if only.String() != p.String() {
		return fmt.Errorf("%q has more than br, bw, max-red and ch-aw-recv (NG.114 3.2.2.3)", p)
	}
	return nil
}

func TestEVSConfigurationOffers(t *testing.T) {
	for _, tc := range []struct {
		c    EVSConfiguration
		want string
	}{
		{EVSConfigA1, "br=5.9-13.2; bw=nb-swb; max-red=0"},
		{EVSConfigA2, "br=5.9-24.4; bw=nb-swb; max-red=0"},
		{EVSConfigB0, "br=13.2; bw=swb; max-red=0"},
		{EVSConfigB1, "br=9.6-13.2; bw=swb; max-red=0"},
		{EVSConfigB2, "br=9.6-24.4; bw=swb; max-red=0"},
	} {
		p := tc.c.OfferParams()
		if p.String() != tc.want {
			t.Errorf("%s offers %q, want %q", tc.c.Name, p, tc.want)
		}
		if err := checkInitialOffer(p); err != nil {
			t.Errorf("%s: %v", tc.c.Name, err)
		}
		if _, err := ParseEVSParams(p.String()); err != nil {
			t.Errorf("%s: %v", tc.c.Name, err)
		}
	}
}

// TestVoNRCodecs checks the order of TS 26.114 5.2.1.6: EVS, which
// offers super-wideband, first, then AMR-WB, then AMR; and that the
// VoLTE part is VoLTECodecs as it is.
func TestVoNRCodecs(t *testing.T) {
	cs := VoNRCodecs(EVSConfigA2)
	var names []string
	for _, c := range cs {
		names = append(names, c.Name)
	}
	want := []string{NameEVS, NameAMRWB, NameAMRWB, NameAMR, NameAMR, NamePCMU, NameTelephoneEvent, NameTelephoneEvent}
	if !slices.Equal(names, want) {
		t.Errorf("order %q, want %q", names, want)
	}
	if !slices.Equal(cs[1:], VoLTECodecs()) {
		t.Errorf("the VoLTE codecs changed: %+v", cs[1:])
	}
	if got := cs[0].Rtpmap(); got != "102 EVS/16000/1" {
		t.Errorf("rtpmap %q", got)
	}
	if _, err := cs[0].EVSFormat(); err != nil {
		t.Error(err)
	}
}

// TestUEToUE is a call between two UEs of this project: the answer
// starts both directions in EVS AMR-WB IO.
func TestUEToUE(t *testing.T) {
	offered, err := VoNRCodecs(UEEVS.Configuration)[0].EVSParams()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := AnswerEVSParams(offered, UEEVS)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := answer.String(), "br=5.9-24.4; bw=nb-swb; evs-mode-switch=1; max-red=0"; got != want {
		t.Errorf("answer %q, want %q", got, want)
	}
	// The offerer reads the start mode from the answer (TS 26.114 Table 6.3a).
	if answer.StartMode() != evs.AMRWBIO {
		t.Errorf("start mode %v", answer.StartMode())
	}
}
