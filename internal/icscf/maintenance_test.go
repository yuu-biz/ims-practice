package icscf

import (
	"log/slog"
	"net/netip"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// managed starts an I-CSCF in front of a fake S-CSCF that answers 200,
// gives it the maintenance API of a node, and returns that node, the
// UE's stack, the address of the I-CSCF and the registry it counts in.
// hssAvailable is what the I-CSCF reports as the state of its Cx peers.
func managed(t *testing.T, hssAvailable func() bool) (*maintenance.Node, *sip.Stack, netip.AddrPort, *metrics.Registry) {
	t.Helper()
	reg := metrics.NewRegistry()
	cfg := Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1, 2}}}, Metrics: reg}
	i, ue, icscf := start(t, cfg, registeredAtHSS(t), nil, func(st *sip.ServerTransaction) {
		st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
	})
	node, err := maintenance.New(maintenance.Config{
		Node:          "icscf",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{i.Function(hssAvailable)},
		Configuration: func() any { return map[string]string{"node": "icscf"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	i.Manage(node.ManagedFunction(role))
	return node, ue, icscf, reg
}

// functionState is the state of the I-CSCF in the maintenance API of
// node.
func functionState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	state, err := node.State()
	if err != nil {
		t.Fatal(err)
	}
	return state.Functions[0]
}

// A shut down I-CSCF refuses every request with 503 and Retry-After and
// forwards none of them: it holds nothing from one request to the next,
// so it has nothing to carry on with. With no transaction in progress
// it is LOCKED at once. Unlocked, it serves requests again.
func TestAShutDownICSCFRefusesEveryRequest(t *testing.T) {
	node, ue, icscf, reg := managed(t, func() bool { return true })
	if r := register(t, ue, icscf, impu); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want the 200 of the S-CSCF", r.StatusCode)
	}
	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED with no transaction in progress", f, err)
	}

	for name, r := range map[string]*sip.Message{
		"REGISTER": register(t, ue, icscf, impu),
		"INVITE":   invite(t, ue, icscf, impu),
	} {
		if r.StatusCode != 503 || r.Get("Retry-After") != strconv.Itoa(retryAfter) {
			t.Errorf("%s got %d with Retry-After %q, want 503 with %d", name, r.StatusCode, r.Get("Retry-After"), retryAfter)
		}
	}
	// The refused INVITE counts in no measurement: the next I-CSCF,
	// which serves it, counts it.
	if got := scraped(t, reg, terminatingAttempts); got != 0 {
		t.Errorf("%s = %v, want 0 for the refused INVITE", terminatingAttempts, got)
	}

	if _, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.Unlocked}); err != nil {
		t.Fatal(err)
	}
	if r := register(t, ue, icscf, impu); r.StatusCode != 200 {
		t.Errorf("REGISTER after the unlock got %d, want the 200 of the S-CSCF", r.StatusCode)
	}
}

// Without an instance of the HSS that takes its Cx requests the I-CSCF
// can serve nothing, since it asks the HSS for every REGISTER (UAR) and
// every initial request (LIR), so it is DISABLED.
func TestTheICSCFIsDisabledWithoutAnHSS(t *testing.T) {
	available := true
	node, _, _, _ := managed(t, func() bool { return available })
	if f := functionState(t, node); f.OperationalState != maintenance.Enabled {
		t.Errorf("the I-CSCF is %s with its HSS peer open, want ENABLED", f.OperationalState)
	}
	available = false
	if f := functionState(t, node); f.OperationalState != maintenance.Disabled {
		t.Errorf("the I-CSCF is %s without an HSS peer, want DISABLED", f.OperationalState)
	}
}

// The users of the I-CSCF are the transactions it is serving, which a
// shut down waits for: an instance with a request in progress is
// SHUTTINGDOWN until that request is answered.
func TestAShutDownWaitsForTheTransactionsInProgress(t *testing.T) {
	reg := metrics.NewRegistry()
	cfg := Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1, 2}}}, Metrics: reg}
	answer := make(chan struct{})
	received := make(chan struct{}, 1)
	i, ue, icscf := start(t, cfg, stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}), nil,
		func(st *sip.ServerTransaction) {
			received <- struct{}{}
			<-answer
			st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
		})
	node, err := maintenance.New(maintenance.Config{
		Node:          "icscf",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{i.Function(func() bool { return true })},
		Configuration: func() any { return map[string]string{"node": "icscf"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	i.Manage(node.ManagedFunction(role))

	done := make(chan *sip.Message, 1)
	go func() { done <- register(t, ue, icscf, impu) }()
	<-received
	if f := functionState(t, node); f.Users == nil || *f.Users != 1 {
		t.Fatalf("the I-CSCF is %+v while it serves a REGISTER, want one user", f)
	}
	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v (%v), want SHUTTINGDOWN while a REGISTER is in progress", f, err)
	}
	close(answer)
	if r := <-done; r.StatusCode != 200 {
		t.Errorf("the REGISTER in progress got %d, want the 200 of the S-CSCF", r.StatusCode)
	}
	// The 200 goes out before OnRequest returns and counts the
	// transaction out, so the state follows shortly after it.
	deadline := time.Now().Add(2 * time.Second)
	for f := functionState(t, node); f.AdministrativeState != maintenance.Locked; f = functionState(t, node) {
		if time.Now().After(deadline) {
			t.Fatalf("the I-CSCF is %+v once its last transaction has ended, want LOCKED", f)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
