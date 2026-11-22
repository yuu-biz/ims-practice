package sdp

import (
	"net/netip"
	"slices"
	"testing"
)

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
