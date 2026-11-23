package pcscf

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsec"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// espHarness is a P-CSCF with IPsec ESP, whose kernel records the
// commands instead of running them. A teardown runs on a timer of its
// own, so the lock stands between that goroutine and the test.
type espHarness struct {
	p  *PCSCF
	mu sync.Mutex
	// runs holds the commands of each run of the kernel, in order.
	runs [][]string
}

// teardownDelay is short enough for a test to wait for it and long
// enough to tell a teardown that waited from one that did not.
const teardownDelay = 50 * time.Millisecond

func newESPHarness(t *testing.T) *espHarness {
	t.Helper()
	h := &espHarness{}
	kernel := ipsec.NewKernel(quiet())
	kernel.Run = func(_ context.Context, cmds []ipsec.Command) error {
		run := make([]string, len(cmds))
		for i, c := range cmds {
			run[i] = c.String()
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		h.runs = append(h.runs, run)
		return nil
	}
	cfg := Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, ProtectedServerPort: 5064,
		ESP: &ESP{Address: netip.MustParseAddr("127.0.0.1"), ProtectedClientPort: 5065,
			Kernel: kernel, TeardownDelay: teardownDelay}}
	locate := locatorFunc(func(string) []sip.Destination { return nil })
	h.p = newPCSCF(t, cfg, locate, NewMemoryStore(), nil, quiet())
	return h
}

// run is the commands of one run of the kernel, and nil for a run that
// has not happened.
func (h *espHarness) run(i int) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if i >= len(h.runs) {
		return nil
	}
	return h.runs[i]
}

func (h *espHarness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.runs)
}

func (h *espHarness) forget() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = nil
}

// only is the one run of the kernel since the last forget, and fails
// when the kernel ran anything else.
func (h *espHarness) only(t *testing.T) []string {
	t.Helper()
	if h.count() != 1 {
		t.Fatalf("the kernel ran %d times, want once:\n%s", h.count(), h.printed())
	}
	run := h.run(0)
	h.forget()
	return run
}

func (h *espHarness) printed() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var b strings.Builder
	for i, run := range h.runs {
		fmt.Fprintf(&b, "run %d:\n", i+1)
		for _, c := range run {
			b.WriteString("  " + c + "\n")
		}
	}
	return b.String()
}

// ran waits for the kernel to have run count times, which a teardown
// does once its delay is over.
func (h *espHarness) ran(t *testing.T, count int) {
	t.Helper()
	for i := 0; i < 100 && h.count() < count; i++ {
		time.Sleep(teardownDelay / 5)
	}
	if h.count() != count {
		t.Fatalf("the kernel ran %d times, want %d:\n%s", h.count(), count, h.printed())
	}
}

func holds(t *testing.T, run []string, want string) {
	t.Helper()
	for _, c := range run {
		if strings.HasPrefix(c, want) {
			return
		}
	}
	t.Errorf("the kernel ran no command %q in:\n  %s", want, strings.Join(run, "\n  "))
}

// associations is a set of security associations as challenged would
// put one together, with the P-CSCF's configured protected client port.
func (h *espHarness) associations(spiUC, spiPC uint32) SecurityAssociations {
	ue := netip.MustParseAddrPort("127.0.0.1:5062")
	return SecurityAssociations{
		UE: ue, Temporary: true, PrivateIdentity: impi, Expires: time.Now().Add(ims.RegAwaitAuth),
		UEMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: spiUC, SPIS: spiUC + 1, PortC: 50001, PortS: ue.Port()},
		PCSCFMechanism: ims.IPsec3GPP{Transform: ims.SupportedTransforms[0],
			SPIC: spiPC, SPIS: spiPC + 1, PortC: h.p.protectedClientPort(), PortS: 5064},
	}
}

// TestESPSetUpWithTheChallenge: the new set of security associations
// goes into the kernel when the P-CSCF keeps it, which is before the
// 401 leaves, so that the REGISTER that answers the challenge arrives
// protected (TS 33.203 7.4.2a). The entry says which P-CSCF holds them.
func TestESPSetUpWithTheChallenge(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	run := h.only(t)
	// The four security associations of TS 33.203 7.1, between the
	// protected ports of the two ends, each added after the delete of
	// its own SPI.
	holds(t, run, "ip xfrm state deleteall src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af")
	holds(t, run, "ip xfrm state add src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af ") // spi_ps, 2223
	holds(t, run, "ip xfrm state add src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x457 ") // spi_uc, 1111
	holds(t, run, "ip xfrm policy update src 127.0.0.1/32 dst 127.0.0.1/32 proto udp sport 50001 dport 5064 dir in ")
	holds(t, run, "ip xfrm policy update src 127.0.0.1/32 dst 127.0.0.1/32 proto udp sport 5065 dport 5062 dir out ")
	// The lifetime of the new associations is the one of the
	// authentication, which the 200 (OK) raises.
	holds(t, run, "ip xfrm state add src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af reqid 2223 mode transport"+
		" replay-window 32 limit time-hard 240 ")
	stored, ok, err := h.p.store.SecurityAssociations(ctx, sas.UE)
	if err != nil || !ok {
		t.Fatalf("the SA table has no entry for the UE: %v, %v", ok, err)
	}
	if stored.PCSCFMechanism.PortC != 5065 {
		t.Errorf("port_pc is %d, want the configured 5065", stored.PCSCFMechanism.PortC)
	}
	if stored.PCSCF != netip.MustParseAddr("127.0.0.1") {
		t.Errorf("the entry names %s as the P-CSCF that holds the associations, want this one", stored.PCSCF)
	}
}

// TestESPLifetimeFollowsTheRegistration: the 200 (OK) that establishes
// the associations, and every refresh after it, raises their lifetime
// in the kernel to the one of the registration (TS 33.203 7.4.2a), so
// that the kernel keeps nothing that the SA table has let go of.
func TestESPLifetimeFollowsTheRegistration(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	h.forget()
	established := sas
	established.Temporary = false
	established.PCSCF = netip.MustParseAddr("127.0.0.1")
	established.Expires = time.Now().Add(600*time.Second + securityAssociationMargin)
	if err := h.p.putSecurityAssociations(ctx, established); err != nil {
		t.Fatal(err)
	}
	run := h.only(t)
	if n := len(run); n != 4 {
		t.Errorf("the kernel ran %d commands, want an update of each association:\n  %s", n, strings.Join(run, "\n  "))
	}
	holds(t, run, "ip xfrm state update src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af reqid 2223 mode transport"+
		" replay-window 32 limit time-hard 630 ")
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
	h.forget()
	if err := h.p.putSecurityAssociations(ctx, h.associations(3333, 4444)); err != nil {
		t.Fatal(err)
	}
	h.ran(t, 2)
	// The teardown of the old associations comes first, and right away:
	// the challenge, not a response, is the message that follows.
	holds(t, h.run(0), "ip xfrm state deleteall src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af")
	holds(t, h.run(0), "ip xfrm policy deleteall src 127.0.0.1/32 dst 127.0.0.1/32 proto udp sport 50001 dport 5064 dir in")
	holds(t, h.run(1), "ip xfrm state add src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x115d ") // spi_ps of the new set, 4445
}

// TestESPTornDownAfterTheLastMessage: deregistering takes the security
// associations out of the kernel as well as out of the SA table (TS
// 33.203 7.4), but only once the 200 (OK) that answers the
// deregistration has had time to leave over them (7.4.2a).
func TestESPTornDownAfterTheLastMessage(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	sas := h.associations(1111, 2222)
	if err := h.p.putSecurityAssociations(ctx, sas); err != nil {
		t.Fatal(err)
	}
	h.forget()
	if err := h.p.deleteSecurityAssociations(ctx, sas.UE); err != nil {
		t.Fatal(err)
	}
	if h.count() != 0 {
		t.Errorf("the kernel let the associations go before the last message:\n%s", h.printed())
	}
	if _, ok, err := h.p.store.SecurityAssociations(ctx, sas.UE); err != nil || ok {
		t.Errorf("the SA table still has the entry: %v, %v", ok, err)
	}
	h.ran(t, 1)
	run := h.only(t)
	holds(t, run, "ip xfrm state deleteall src 127.0.0.1 dst 127.0.0.1 proto esp spi 0x8af")
	holds(t, run, "ip xfrm policy deleteall src 127.0.0.1/32 dst 127.0.0.1/32 proto udp sport 50001 dport 5064 dir in")
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
	h.forget()
	if err := h.p.deleteSecurityAssociationsOf(ctx, sas.UE, SPIs{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * teardownDelay)
	if h.count() != 0 {
		t.Errorf("the kernel tore down the set of another contact:\n%s", h.printed())
	}
	if err := h.p.deleteSecurityAssociationsOf(ctx, sas.UE, sas.SPIs()); err != nil {
		t.Fatal(err)
	}
	h.ran(t, 1)
	holds(t, h.only(t), "ip xfrm state deleteall ")
}

// TestESPOfAnotherInstanceIsLeftAlone: the SA table may be shared by
// several P-CSCFs, while the security associations of the kernel are
// each instance's own. An instance touches neither the kernel's for an
// entry that another instance set up.
func TestESPOfAnotherInstanceIsLeftAlone(t *testing.T) {
	h := newESPHarness(t)
	ctx := context.Background()
	elsewhere := h.associations(1111, 2222)
	elsewhere.Temporary = false
	elsewhere.PCSCF = netip.MustParseAddr("127.0.0.2")
	if err := h.p.store.PutSecurityAssociations(ctx, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := h.p.deleteSecurityAssociations(ctx, elsewhere.UE); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * teardownDelay)
	if h.count() != 0 {
		t.Errorf("the kernel was asked about the associations of another P-CSCF:\n%s", h.printed())
	}
	if _, ok, err := h.p.store.SecurityAssociations(ctx, elsewhere.UE); err != nil || ok {
		t.Errorf("the SA table still has the entry: %v, %v", ok, err)
	}
}
