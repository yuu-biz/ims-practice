package sdp

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
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
