package scscf

import (
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// maintained gives the S-CSCF of h the maintenance API of a node, as
// cmd/ims does, and returns that API. hssAvailable tells whether the HSS
// takes requests.
func maintained(t *testing.T, h *harness, hssAvailable func() bool) *maintenance.Node {
	t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          "scscf",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{h.role.Function(hssAvailable)},
		Configuration: func() any { return map[string]string{"node": "scscf"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.role.Manage(node.ManagedFunction(role))
	return node
}

func functionState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	state, err := node.State()
	if err != nil {
		t.Fatal(err)
	}
	return state.Functions[0]
}

// The S-CSCF cannot work while no instance of the HSS takes requests.
func TestOperationalStateFollowsTheHSS(t *testing.T) {
	h := newHarness(t, Config{})
	var available atomic.Bool
	node := maintained(t, h, available.Load)
	if got := functionState(t, node).OperationalState; got != maintenance.Disabled {
		t.Errorf("without the HSS the S-CSCF is %s, want DISABLED", got)
	}
	available.Store(true)
	if got := functionState(t, node).OperationalState; got != maintenance.Enabled {
		t.Errorf("with the HSS the S-CSCF is %s, want ENABLED", got)
	}
}

// A shut down S-CSCF refuses what would start something new with 503 and
// Retry-After, and serves the requests within a dialog and those that an
// application server sends back. Unlocked, it takes new requests again.
func TestAShutDownSCSCFRefusesOnlyWhatIsNew(t *testing.T) {
	h, _, pcscf := registeredHarness(t)
	node := maintained(t, h, func() bool { return true })
	// No transaction is in progress, so the S-CSCF is LOCKED at once.
	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED", f, err)
	}

	for name, r := range map[string]*sip.Message{
		"REGISTER": h.register(sip.NewCallID(), 1, 600000, initial),
		"INVITE":   h.invite("tel:+1-202-555-0102", orig, impu),
	} {
		if r.StatusCode != 503 || r.Get("Retry-After") != "30" {
			t.Errorf("new %s got %d with Retry-After %q, want 503 with 30", name, r.StatusCode, r.Get("Retry-After"))
		}
	}

	bye := newRequest(sip.BYE, "sip:001010000000001@192.0.2.10:5060", "<sip:pcscf."+domain+":5060;lr>", "")
	bye.Set("To", bye.Get("To")+";tag=callee")
	if r := h.send(bye); r.StatusCode == 503 {
		t.Errorf("BYE within a dialog got 503")
	}
	if got := received(t, pcscf); got.Method != sip.BYE {
		t.Errorf("the P-CSCF got %s, want the BYE", got.Method)
	}
	// The S-CSCF looks up the original dialog identifier, as it does when
	// UNLOCKED; it gave out none, so the answer is 500.
	back := newRequest(sip.INVITE, "tel:+1-202-555-0102", "<sip:scscf."+domain+":5060;lr;odi=unknown>", impu)
	if r := h.send(back); r.StatusCode != 500 {
		t.Errorf("INVITE back from an application server got %d, want 500", r.StatusCode)
	}

	if _, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.Unlocked}); err != nil {
		t.Fatal(err)
	}
	if r := h.register(sip.NewCallID(), 1, 600000, initial); r.StatusCode != 401 {
		t.Errorf("REGISTER after the unlock got %d, want 401", r.StatusCode)
	}
}

// A shut down waits for the transactions in progress, and the S-CSCF is
// LOCKED once the last of them has ended.
func TestAShutDownWaitsForTheTransactionsInProgress(t *testing.T) {
	h := newHarness(t, Config{})
	node := maintained(t, h, func() bool { return true })
	stalled := make(chan struct{})
	h.hss.stall(impi, stalled)
	answered := make(chan int, 1)
	go func() { answered <- h.register(sip.NewCallID(), 1, 600000, initial).StatusCode }()
	deadline := time.Now().Add(3 * time.Second)
	for f := functionState(t, node); f.Users == nil || *f.Users != 1; f = functionState(t, node) {
		if time.Now().After(deadline) {
			close(stalled)
			t.Fatalf("users = %v while the MAR waits, want 1", f.Users)
		}
		time.Sleep(10 * time.Millisecond)
	}

	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.ShuttingDown {
		close(stalled)
		t.Fatalf("shut down with a REGISTER in progress gave %+v (%v), want SHUTTINGDOWN", f, err)
	}
	close(stalled)
	if code := <-answered; code != 401 {
		t.Errorf("the REGISTER in progress got %d, want its 401", code)
	}
	for functionState(t, node).AdministrativeState != maintenance.Locked {
		if time.Now().After(deadline.Add(3 * time.Second)) {
			t.Fatalf("still %s after the last transaction ended, want LOCKED", functionState(t, node).AdministrativeState)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
