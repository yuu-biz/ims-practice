package ipsec

import (
	"encoding/hex"
	"net/netip"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ims"
)

// agreement is the agreement of the scenarios: the UE and the P-CSCF of
// the Compose deployment, with CK and IK of test set 1 of TS 35.208.
func agreement(t *testing.T) Agreement {
	t.Helper()
	a := Agreement{
		UE:    netip.MustParseAddr("172.28.0.120"),
		PCSCF: netip.MustParseAddr("172.28.0.20"),
		UEMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: 4001, SPIS: 4002, PortC: 5063, PortS: 5062},
		PCSCFMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: 5001, SPIS: 5002, PortC: 5065, PortS: 5064},
	}
	copy(a.CK[:], decode(t, "b40ba9a3c58b2a05bbf0d987b21bf8cb"))
	copy(a.IK[:], decode(t, "f769bcd751044604127672711c6d3441"))
	return a
}

func decode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return b
}

// TestSecurityAssociationsAreTwoPairs: an agreement has the four
// unidirectional associations of TS 33.203 7.1 rule 1, each carrying
// packets from one protected port to the other port of its pair, with
// the SPI that the end it goes to chose for that port.
func TestSecurityAssociationsAreTwoPairs(t *testing.T) {
	sas := agreement(t).SecurityAssociations()
	want := []string{
		"172.28.0.120:5063 -> 172.28.0.20:5064 spi 5002",
		"172.28.0.20:5064 -> 172.28.0.120:5063 spi 4001",
		"172.28.0.20:5065 -> 172.28.0.120:5062 spi 4002",
		"172.28.0.120:5062 -> 172.28.0.20:5065 spi 5001",
	}
	if len(sas) != len(want) {
		t.Fatalf("got %d security associations, want %d", len(sas), len(want))
	}
	for i, sa := range sas {
		got := sa.Source.String() + " -> " + sa.Destination.String() + " spi " + reqid(sa.SPI)
		if got != want[i] {
			t.Errorf("security association %d is %s, want %s", i, got, want[i])
		}
	}
}

// TestIKESP: Annex I expands IK_ESP from CK_IM || IK_IM with the key
// derivation function of TS 33.220 Annex B.2.0. The expected value is
// HMAC-SHA-256 over FC || P0 || L0, computed outside this package.
func TestIKESP(t *testing.T) {
	a := agreement(t)
	key, err := IKESP(a.UEMechanism.Transform, a.IK, a.CK)
	if err != nil {
		t.Fatalf("IKESP: %v", err)
	}
	const want = "9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e"
	if got := hex.EncodeToString(key); got != want {
		t.Errorf("IK_ESP is %s, want %s", got, want)
	}
	if len(key) != 32 {
		t.Errorf("IK_ESP is %d bytes, want the 256 bits of the KDF output", len(key))
	}
}

// TestIKESPRefusesAnotherTransform: this package sets up only the
// transform that the security agreement offers.
func TestIKESPRefusesAnotherTransform(t *testing.T) {
	a := agreement(t)
	if _, err := IKESP(ims.Transform{Algorithm: "hmac-sha-1-96"}, a.IK, a.CK); err == nil {
		t.Error("IKESP accepted hmac-sha-1-96, which this package does not set up")
	}
}

// TestSetupAtEachEnd: both ends set up the same four associations and
// the same keys, and each one's policies are outbound where it sends
// and inbound where it receives.
func TestSetupAtEachEnd(t *testing.T) {
	a := agreement(t)
	ue, err := a.Setup(UEEnd)
	if err != nil {
		t.Fatalf("Setup at the UE: %v", err)
	}
	pcscf, err := a.Setup(PCSCFEnd)
	if err != nil {
		t.Fatalf("Setup at the P-CSCF: %v", err)
	}
	states := func(batch string) []string {
		var out []string
		for _, line := range strings.Split(strings.TrimSuffix(batch, "\n"), "\n") {
			if strings.HasPrefix(line, "state ") {
				out = append(out, line)
			}
		}
		return out
	}
	if got, want := states(ue), states(pcscf); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the ends set up different security associations:\n%s\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, want := range []string{
		// The UE sends its requests from its protected client port.
		"policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto udp sport 5063 dport 5064 dir out",
		"policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto tcp sport 5063 dport 5064 dir out",
		// And receives the requests of the P-CSCF on its protected
		// server port.
		"policy update src 172.28.0.20/32 dst 172.28.0.120/32 proto udp sport 5065 dport 5062 dir in",
	} {
		if !strings.Contains(ue, want) {
			t.Errorf("the UE sets up no policy %q in:\n%s", want, ue)
		}
	}
	for _, want := range []string{
		"policy update src 172.28.0.20/32 dst 172.28.0.120/32 proto udp sport 5065 dport 5062 dir out",
		"policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto udp sport 5063 dport 5064 dir in",
	} {
		if !strings.Contains(pcscf, want) {
			t.Errorf("the P-CSCF sets up no policy %q in:\n%s", want, pcscf)
		}
	}
}

// TestSetupState: a state carries the SPI, the integrity algorithm with
// IK_ESP and its 128-bit integrity check value, null encryption with an
// empty key, transport mode and the anti-replay window of TS 33.203 6.3.
func TestSetupState(t *testing.T) {
	batch, err := agreement(t).Setup(UEEnd)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	const want = "state update src 172.28.0.120 dst 172.28.0.20 proto esp spi 0x138a reqid 5002 mode transport" +
		" replay-window 32 auth-trunc hmac(sha256)" +
		" 0x9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e 128 enc ecb(cipher_null) 0x"
	if !strings.Contains(batch, want+"\n") {
		t.Errorf("Setup has no line\n%s\nin:\n%s", want, batch)
	}
	// Every policy points at its own association, so that the kernel
	// protects a packet with the association of its ports.
	for _, reqid := range []string{"reqid 5002 ", "reqid 4001 ", "reqid 4002 ", "reqid 5001 "} {
		if n := strings.Count(batch, reqid); n != 3 {
			t.Errorf("%q is in %d commands, want the state and its two policies", reqid, n)
		}
	}
}

// TestTeardownMatchesSetup: a teardown deletes every policy that the
// setup added, by the same selector, and the states of both directions.
func TestTeardownMatchesSetup(t *testing.T) {
	a := agreement(t)
	setup, err := a.Setup(PCSCFEnd)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	teardown := a.Teardown(PCSCFEnd)
	for _, line := range strings.Split(strings.TrimSuffix(setup, "\n"), "\n") {
		selector, ok := strings.CutPrefix(line, "policy update ")
		if !ok {
			continue
		}
		selector, _, _ = strings.Cut(selector, " tmpl ")
		if !strings.Contains(teardown, "policy delete "+selector+"\n") {
			t.Errorf("Teardown deletes no policy %q in:\n%s", selector, teardown)
		}
	}
	for _, want := range []string{
		"state deleteall src 172.28.0.120 dst 172.28.0.20 proto esp\n",
		"state deleteall src 172.28.0.20 dst 172.28.0.120 proto esp\n",
	} {
		if !strings.Contains(teardown, want) {
			t.Errorf("Teardown has no %q in:\n%s", want, teardown)
		}
	}
}
