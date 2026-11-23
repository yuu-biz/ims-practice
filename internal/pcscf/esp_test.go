package pcscf

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsec"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// espHarness is a P-CSCF with IPsec ESP, whose kernel records the "ip
// xfrm" batches instead of running them.
type espHarness struct {
	p       *PCSCF
	batches []string
}

func newESPHarness(t *testing.T) *espHarness {
	t.Helper()
	h := &espHarness{}
	kernel := ipsec.NewKernel(quiet())
	kernel.Run = func(_ context.Context, force bool, batch string) error {
		prefix := "setup\n"
		if force {
			prefix = "teardown\n"
		}
		h.batches = append(h.batches, prefix+batch)
		return nil
	}
	cfg := Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, ProtectedServerPort: 5064,
		ESP: &ESP{Address: netip.MustParseAddr("127.0.0.1"), ProtectedClientPort: 5065, Kernel: kernel}}
	locate := locatorFunc(func(string) []sip.Destination { return nil })
	h.p = newPCSCF(t, cfg, locate, NewMemoryStore(), nil, quiet())
	return h
}

func (h *espHarness) only(t *testing.T) string {
	t.Helper()
	if len(h.batches) != 1 {
		t.Fatalf("the kernel ran %d batches, want one:\n%s", len(h.batches), strings.Join(h.batches, "\n"))
	}
	batch := h.batches[0]
	h.batches = nil
	return batch
}

// associations is a set of security associations as challenged would
// put one together, with the P-CSCF's configured protected client port.
func (h *espHarness) associations(spiUC, spiPC uint32) SecurityAssociations {
	ue := netip.MustParseAddrPort("127.0.0.1:5062")
	return SecurityAssociations{
		UE: ue, Temporary: true, PrivateIdentity: impi, Expires: time.Now().Add(time.Minute),
		UEMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: spiUC, SPIS: spiUC + 1, PortC: 50001, PortS: ue.Port()},
		PCSCFMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: spiPC, SPIS: spiPC + 1, PortC: h.p.protectedClientPort(), PortS: 5064},
	}
}

// TestESPSetUpWithTheChallenge: the new set of security associations
// goes into the kernel when the P-CSCF keeps it, which is before the
// 401 leaves, so that the REGISTER that answers the challenge arrives
// protected (TS 33.203 7.4.2a).
func TestESPSetUpWithTheChallenge(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	batch := h.only(t)
	if !strings.HasPrefix(batch, "setup\n") {
		t.Fatalf("the kernel ran:\n%s", batch)
	}
	// The four security associations of TS 33.203 7.1, between the
	// protected ports of the two ends.
	for _, want := range []string{
		"state update src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af ", // spi_ps, 2223
		"state update src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x457 ", // spi_uc, 1111
		"sport 50001 dport 5064 dir in",
		"sport 5065 dport 5062 dir out",
	} {
		if !strings.Contains(batch, want) {
			t.Errorf("the kernel got no %q in:\n%s", want, batch)
		}
	}
	if stored, ok, err := h.p.store.SecurityAssociations(ctx, sas.UE); err != nil || !ok {
		t.Fatalf("the SA table has no entry for the UE: %v, %v", ok, err)
	} else if stored.PCSCFMechanism.PortC != 5065 {
		t.Errorf("port_pc is %d, want the configured 5065", stored.PCSCFMechanism.PortC)
	}
}

// TestESPReplacedByANewAuthentication: a new authentication takes the
// security associations it replaces out of the kernel first, so that no
// message rides the old ones afterwards (TS 33.203 7.4.2a).
func TestESPReplacedByANewAuthentication(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	if err := h.p.putSecurityAssociations(ctx, h.associations(1111, 2222)); err != nil {
		t.Fatal(err)
	}
	h.batches = nil
	if err := h.p.putSecurityAssociations(ctx, h.associations(3333, 4444)); err != nil {
		t.Fatal(err)
	}
	if len(h.batches) != 2 {
		t.Fatalf("the kernel ran %d batches, want a teardown and a set-up:\n%s", len(h.batches), strings.Join(h.batches, "\n"))
	}
	if !strings.HasPrefix(h.batches[0], "teardown\n") || !strings.Contains(h.batches[0], "state deleteall src 127.0.0.1 dst 127.0.0.1 proto esp") {
		t.Errorf("the first batch is not the teardown of the old associations:\n%s", h.batches[0])
	}
	if !strings.Contains(h.batches[1], "spi 0x115c ") { // spi_pc of the new set, 4444
		t.Errorf("the second batch does not set the new associations up:\n%s", h.batches[1])
	}
}

// TestESPTornDownWithTheRegistration: deregistering takes the security
// associations out of the kernel as well as out of the SA table (TS
// 33.203 7.4).
func TestESPTornDownWithTheRegistration(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	h.batches = nil
	if err := h.p.deleteSecurityAssociations(ctx, sas.UE); err != nil {
		t.Fatal(err)
	}
	batch := h.only(t)
	if !strings.HasPrefix(batch, "teardown\n") {
		t.Errorf("the kernel ran:\n%s", batch)
	}
	for _, want := range []string{
		"state deleteall src 127.0.0.1 dst 127.0.0.1 proto esp",
		"policy delete src 127.0.0.1/32 dst 127.0.0.1/32 proto udp sport 50001 dport 5064 dir in",
	} {
		if !strings.Contains(batch, want) {
			t.Errorf("the teardown has no %q in:\n%s", want, batch)
		}
	}
	if _, ok, err := h.p.store.SecurityAssociations(ctx, sas.UE); err != nil || ok {
		t.Errorf("the SA table still has the entry: %v, %v", ok, err)
	}
}

// TestESPLeavesTheOtherSetAlone: the set that a contact was registered
// over goes with that contact, and another set at the same address
// stays (TS 33.203 7.4.2a).
func TestESPLeavesTheOtherSetAlone(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	h.batches = nil
	if err := h.p.deleteSecurityAssociationsOf(ctx, sas.UE, SPIs{}); err != nil {
		t.Fatal(err)
	}
	if len(h.batches) != 0 {
		t.Errorf("the kernel tore down the set of another contact:\n%s", strings.Join(h.batches, "\n"))
	}
	if err := h.p.deleteSecurityAssociationsOf(ctx, sas.UE, sas.SPIs()); err != nil {
		t.Fatal(err)
	}
	if batch := h.only(t); !strings.HasPrefix(batch, "teardown\n") {
		t.Errorf("the kernel ran:\n%s", batch)
	}
}
