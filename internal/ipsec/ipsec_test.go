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

// setup is the commands of Setup, as a shell would need them.
func setup(t *testing.T, a Agreement, end End) []string {
	t.Helper()
	cmds, err := a.Setup(end)
	if err != nil {
		t.Fatalf("Setup at the %s: %v", end, err)
	}
	return written(cmds)
}

func written(cmds []Command) []string {
	out := make([]string, len(cmds))
	for i, c := range cmds {
		out[i] = c.String()
	}
	return out
}

// with are the commands that start with prefix.
func with(cmds []string, prefix string) []string {
	var out []string
	for _, c := range cmds {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func holds(cmds []string, want string) bool {
	for _, c := range cmds {
		if c == want {
			return true
		}
	}
	return false
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

// TestSetupRefusesAnotherProtocolOrMode: TS 33.203 6 allows no protocol
// but ESP, and 7.1 asks for transport mode, so a mechanism that names
// anything else is not set up as though it had asked for ESP.
func TestSetupRefusesAnotherProtocolOrMode(t *testing.T) {
	for _, c := range []struct {
		what  string
		named func(*ims.IPsec3GPP)
	}{
		{"prot=ah", func(m *ims.IPsec3GPP) { m.Protocol = "ah" }},
		{"mod=tun", func(m *ims.IPsec3GPP) { m.Mode = "tun" }},
		{"mod=UDP-enc-tun", func(m *ims.IPsec3GPP) { m.Mode = "UDP-enc-tun" }},
	} {
		a := agreement(t)
		c.named(&a.UEMechanism)
		if _, err := a.Setup(UEEnd); err == nil {
			t.Errorf("Setup accepted %s", c.what)
		}
	}
	// The values of this project's own mechanisms, which Annex H leaves
	// out because they are the defaults.
	a := agreement(t)
	a.UEMechanism.Protocol, a.UEMechanism.Mode = "esp", "trans"
	if _, err := a.Setup(UEEnd); err != nil {
		t.Errorf("Setup refused prot=esp with mod=trans: %v", err)
	}
}

// TestEveryCommandNamesTheXfrmObject: a command is a whole "ip" command
// without the program, so it names the object xfrm; without it ip
// answers that the object "state" is unknown.
func TestEveryCommandNamesTheXfrmObject(t *testing.T) {
	a := agreement(t)
	batches := [][]string{setup(t, a, UEEnd), setup(t, a, PCSCFEnd),
		written(a.Teardown(UEEnd)), written(a.TeardownStates())}
	for _, cmds := range batches {
		for _, c := range cmds {
			if !strings.HasPrefix(c, "ip xfrm state ") && !strings.HasPrefix(c, "ip xfrm policy ") {
				t.Errorf("a command names no xfrm object: %q", c)
			}
		}
	}
}

// TestSetupAtEachEnd: both ends set up the same four associations and
// the same keys, and each one's policies are outbound where it sends
// and inbound where it receives.
func TestSetupAtEachEnd(t *testing.T) {
	a := agreement(t)
	ue, pcscf := setup(t, a, UEEnd), setup(t, a, PCSCFEnd)
	got, want := with(ue, "ip xfrm state "), with(pcscf, "ip xfrm state ")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("the ends set up different security associations:\n%s\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, want := range []string{
		// The UE sends its requests from its protected client port.
		"ip xfrm policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto udp sport 5063 dport 5064 dir out",
		"ip xfrm policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto tcp sport 5063 dport 5064 dir out",
		// And receives the requests of the P-CSCF on its protected
		// server port.
		"ip xfrm policy update src 172.28.0.20/32 dst 172.28.0.120/32 proto udp sport 5065 dport 5062 dir in",
	} {
		if len(with(ue, want)) != 1 {
			t.Errorf("the UE sets up no policy %q in:\n%s", want, strings.Join(ue, "\n"))
		}
	}
	for _, want := range []string{
		"ip xfrm policy update src 172.28.0.20/32 dst 172.28.0.120/32 proto udp sport 5065 dport 5062 dir out",
		"ip xfrm policy update src 172.28.0.120/32 dst 172.28.0.20/32 proto udp sport 5063 dport 5064 dir in",
	} {
		if len(with(pcscf, want)) != 1 {
			t.Errorf("the P-CSCF sets up no policy %q in:\n%s", want, strings.Join(pcscf, "\n"))
		}
	}
}

// TestSetupState: a state is added, not updated, since the kernel
// cannot update an SPI it does not hold; it carries the SPI, the
// integrity algorithm with IK_ESP and its 128-bit integrity check
// value, null encryption with an empty key, transport mode and the
// anti-replay window of TS 33.203 6.3. It carries no lifetime: TS
// 33.203 7.1 NOTE 4 leaves that to the SIP application. A shell needs
// the name of an algorithm and the empty key quoted.
func TestSetupState(t *testing.T) {
	cmds := setup(t, agreement(t), UEEnd)
	const want = "ip xfrm state add src 172.28.0.120 dst 172.28.0.20 proto esp spi 0x138a reqid 5002" +
		" mode transport replay-window 32 auth-trunc 'hmac(sha256)'" +
		" 0x9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e 128 enc 'ecb(cipher_null)' ''"
	if !holds(cmds, want) {
		t.Errorf("Setup has no command\n%s\nin:\n%s", want, strings.Join(cmds, "\n"))
	}
	// Every policy points at its own association, so that the kernel
	// protects a packet with the association of its ports.
	for _, reqid := range []string{"reqid 5002 ", "reqid 4001 ", "reqid 4002 ", "reqid 5001 "} {
		if n := strings.Count(strings.Join(cmds, "\n"), reqid); n != 3 {
			t.Errorf("%q is in %d commands, want the state and its two policies", reqid, n)
		}
	}
	// An add of a state the kernel holds already fails, so each add
	// follows the delete of its own SPI.
	for i, c := range with(cmds, "ip xfrm state ") {
		if i%2 == 0 && !strings.HasPrefix(c, "ip xfrm state deleteall ") {
			t.Errorf("the state command before an add is %q", c)
		}
	}
}

// TestNothingCarriesALifetime: the kernel is given no lifetime, for a
// state or for a policy. TS 33.203 7.1 NOTE 4 leaves the lifetime to
// the SIP application, and a lifetime in the kernel would outlast a
// registration or fall short of one: the kernel counts a hard lifetime
// from the time a state was added, and an update of a state replaces
// its limits and not that time, so a refreshed registration would keep
// the lifetime of the authentication that set its associations up.
func TestNothingCarriesALifetime(t *testing.T) {
	a := agreement(t)
	for _, cmds := range [][]string{setup(t, a, UEEnd), written(a.Teardown(PCSCFEnd)), written(a.TeardownStates())} {
		for _, c := range cmds {
			if strings.Contains(c, "limit") {
				t.Errorf("a command gives the kernel a lifetime: %q", c)
			}
		}
	}
}

// TestTeardownStatesLeavesThePolicies: the set that another one of the
// same ports replaces gives up its states, and leaves its policies to
// be written over, so that the protected ports are covered throughout
// (TS 33.203 7.4.2a).
func TestTeardownStatesLeavesThePolicies(t *testing.T) {
	cmds := written(agreement(t).TeardownStates())
	if n := len(cmds); n != 4 {
		t.Errorf("TeardownStates runs %d commands, want the four states:\n%s", n, strings.Join(cmds, "\n"))
	}
	if n := len(with(cmds, "ip xfrm policy ")); n != 0 {
		t.Errorf("TeardownStates touches %d policies, want none:\n%s", n, strings.Join(cmds, "\n"))
	}
}

// TestRedactTakesTheKeyOut: a command carries IK_ESP, and the error of
// one that failed is logged.
func TestRedactTakesTheKeyOut(t *testing.T) {
	cmds := setup(t, agreement(t), UEEnd)
	state := with(cmds, "ip xfrm state add ")[0]
	const key = "0x9e98fd635ce304d820de064bea152b645b2fa171ba99416e3a3e90f5f9687c9e"
	if !strings.Contains(state, key) {
		t.Fatalf("the command carries no key, so this test checks nothing: %q", state)
	}
	text := redact(state + ": exit status 2: Error: Requested type not found.")
	if strings.Contains(text, key) || strings.Contains(text, key[2:10]) {
		t.Errorf("the key is still in %q", text)
	}
	for _, want := range []string{"ip xfrm state add", "spi 0x138a", "Requested type not found"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q is gone from %q", want, text)
		}
	}
}

// TestTeardownMatchesSetup: a teardown deletes every policy that the
// setup added, by the same selector, and each state by its own SPI, so
// that it leaves the associations of another registration of the same
// UE alone. Every command of it succeeds with nothing to delete.
func TestTeardownMatchesSetup(t *testing.T) {
	a := agreement(t)
	teardown := written(a.Teardown(PCSCFEnd))
	for _, c := range with(setup(t, a, PCSCFEnd), "ip xfrm policy update ") {
		selector, _, _ := strings.Cut(strings.TrimPrefix(c, "ip xfrm policy update "), " tmpl ")
		if !holds(teardown, "ip xfrm policy deleteall "+selector) {
			t.Errorf("Teardown deletes no policy %q in:\n%s", selector, strings.Join(teardown, "\n"))
		}
	}
	for _, want := range []string{
		"ip xfrm state deleteall src 172.28.0.120 dst 172.28.0.20 proto esp spi 0x138a",
		"ip xfrm state deleteall src 172.28.0.20 dst 172.28.0.120 proto esp spi 0xfa1",
		"ip xfrm state deleteall src 172.28.0.20 dst 172.28.0.120 proto esp spi 0xfa2",
		"ip xfrm state deleteall src 172.28.0.120 dst 172.28.0.20 proto esp spi 0x1389",
	} {
		if !holds(teardown, want) {
			t.Errorf("Teardown has no %q in:\n%s", want, strings.Join(teardown, "\n"))
		}
	}
	if n := len(with(teardown, "ip xfrm state ")); n != 4 {
		t.Errorf("Teardown deletes %d states, want the four of the agreement", n)
	}
}
