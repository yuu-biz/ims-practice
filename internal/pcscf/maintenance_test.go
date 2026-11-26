package pcscf

import (
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// functionState is the state of the P-CSCF in the maintenance API of
// node.
func functionState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	return node.State().Functions[0]
}

// A shut down P-CSCF refuses the REGISTERs that would start or refresh a
// registration through it with 503 and a Retry-After longer than Timer
// F of the UE, and forwards none of them. It goes on serving the UE
// registered through it, whose request and deregistration pass, and
// becomes LOCKED once the Store has forgotten the IP association of
// that UE. Unlocked, it takes registrations again.
func TestAShutDownPCSCFRefusesOnlyTheRegistrations(t *testing.T) {
	grace := associationGrace
	associationGrace = 200 * time.Millisecond
	t.Cleanup(func() { associationGrace = grace })
	registrar := &digestRegistrar{}
	messages := make(chan *sip.Message, 1)
	home := newStack(t, func(st *sip.ServerTransaction) {
		if st.Request.Method == sip.MESSAGE {
			messages <- st.Request
			st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
			return
		}
		registrar.serve(st)
	})
	reg := metrics.NewRegistry()
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, Metrics: reg},
		locatorFunc(func(string) []sip.Destination { return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}} }),
		NewMemoryStore(), nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	p.SetStack(pcscfStack)
	node, err := maintenance.New(maintenance.Config{
		Node:          "pcscf",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{p.Function()},
		Configuration: func() any { return map[string]string{"node": "pcscf"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Manage(node.ManagedFunction(role))

	registered := newDigestUE(t, pcscfStack.LocalAddr())
	if r := registered.answer(registered.register("", "", "600000"), "right"); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	if f := functionState(t, node); f.OperationalState != maintenance.Enabled || f.Users == nil || *f.Users != 1 {
		t.Fatalf("the P-CSCF is %+v, want ENABLED with the one UE as its user", f)
	}
	f, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v (%v), want SHUTTINGDOWN while a UE is registered", f, err)
	}

	registrar.mu.Lock()
	registers := registrar.registers
	registrar.mu.Unlock()
	newcomer := newDigestUE(t, pcscfStack.LocalAddr())
	for name, r := range map[string]*sip.Message{
		"initial REGISTER": newcomer.register("", "", "600000"),
		"refresh":          registered.register("", "", "600000"),
	} {
		if r.StatusCode != 503 || r.Get("Retry-After") != strconv.Itoa(retryAfter) {
			t.Errorf("%s got %d with Retry-After %q, want 503 with %d", name, r.StatusCode, r.Get("Retry-After"), retryAfter)
		}
	}
	if retryAfter <= 128 {
		t.Errorf("Retry-After %d is not longer than Timer F of the UE, 128 s", retryAfter)
	}
	registrar.mu.Lock()
	if registrar.registers != registers {
		t.Error("a refused REGISTER reached the home network")
	}
	registrar.mu.Unlock()
	counted(t, reg, map[string]float64{
		`ims_registration_failures_total{role="pcscf",type="initial",status="503"}`: 1,
		`ims_registration_failures_total{role="pcscf",type="re",status="503"}`:      1,
	})

	m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:001010000000002@" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<sip:001010000000002@"+domain+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	if r := registered.send(m); r.StatusCode != 200 {
		t.Errorf("MESSAGE of the registered UE got %d, want the 200 of the home network", r.StatusCode)
	}
	<-messages
	if r := registered.register("", "", "0"); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d, want 200", r.StatusCode)
	}
	if !came(5*time.Second, func() bool { return functionState(t, node).AdministrativeState == maintenance.Locked }) {
		t.Fatalf("the P-CSCF is %+v after the association has gone, want LOCKED", functionState(t, node))
	}

	if _, err := node.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.Unlocked}); err != nil {
		t.Fatal(err)
	}
	if r := newcomer.register("", "", "600000"); r.StatusCode != 401 {
		t.Errorf("REGISTER after the unlock got %d, want the 401 of the home network", r.StatusCode)
	}
}
