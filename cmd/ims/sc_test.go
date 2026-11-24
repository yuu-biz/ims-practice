package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// scTimers are the Diameter timers of the tests of the Service Centre
// out of service: a short Tc, so that a serving node that the SC turned
// away connects again soon after the unlock.
var scTimers = diameter.Timers{Watchdog: 2 * time.Second, Reconnect: 100 * time.Millisecond, Answer: 2 * time.Second}

// scInstance is one node of the sc role as cmd/ims wires it: the SC on
// a Diameter node that listens, and the maintenance API of the node.
type scInstance struct {
	host   string
	server *diameter.Server
	oam    *maintenance.Client
}

// startSCInstance starts an instance of the SC named host, on store,
// with an HSS that knows every recipient and serves none of them.
func startSCInstance(t *testing.T, host string, store smsc.Store, hssAvailable func() bool) scInstance {
	t.Helper()
	const scNumber = "12025550100"
	node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain},
		[]uint32{sgd.ApplicationIDS6c, sgd.ApplicationIDSGd}, scTimers, quiet())
	centre := smsc.New(smsc.Config{Address: scNumber, HSSRealm: domain}, store, nil, absentRecipients{},
		smsc.SystemClock, quiet())
	t.Cleanup(centre.Wait)
	var scRole atomic.Pointer[smsc.SMSC]
	scRole.Store(centre)
	var noSCSCF atomic.Pointer[scscf.SCSCF]
	var noIPSMGW atomic.Pointer[ipsmgw.IPSMGW]
	var noPolicy atomic.Pointer[rx.Client]
	node.Handle(diameterHandler(node, nil, &noSCSCF, nil, &noIPSMGW, &scRole, &noPolicy, quiet()))
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "sc", Roles: []config.Role{config.RoleSC}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()}, SC: &config.SC{Address: scNumber}}
	roles := []managedRole{{function: scFunction(centre, server, hssAvailable, quiet()), manage: centre.Manage}}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return scInstance{host: host, server: server, oam: maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)}
}

// set makes the event that takes the SC of the instance to want, and
// returns its state after it.
func (i scInstance) set(t *testing.T, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := i.oam.SetAdministrativeState(t.Context(), string(config.RoleSC),
		maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// absentRecipients is an HSS that knows every recipient and has no node
// registered for any of them, so the SC takes every short message and
// holds it.
type absentRecipients struct{}

func (absentRecipients) SendRoutingInfoForSM(context.Context, sgd.SendRoutingInfoForSMRequest) (sgd.SendRoutingInfoForSMAnswer, error) {
	return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}, nil
}

func (absentRecipients) ReportSMDeliveryStatus(context.Context, sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error) {
	return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
}

// until waits up to five seconds for ok.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("waited in vain for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestMaintenanceAPIShutsTheServiceCentreDown: an IP-SM-GW holds a
// connection to each of two instances of the SC that share one store,
// and sends its OFRs to the realm. The operator shuts the first instance
// down; with nothing in its hands it is LOCKED at once, ends the
// connection with a DPR and accepts no new one, so the next OFR reaches
// the second instance. The unlock lets the IP-SM-GW back in after Tc.
func TestMaintenanceAPIShutsTheServiceCentreDown(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	store := smsc.NewMemoryStore()
	hssUp := atomic.Bool{}
	hssUp.Store(true)
	one := startSCInstance(t, "sc1."+domain, store, hssUp.Load)
	two := startSCInstance(t, "sc2."+domain, store, func() bool { return true })

	gw := diameter.NewNode(diameter.Identity{OriginHost: "ipsmgw." + domain, OriginRealm: domain},
		[]uint32{sgd.ApplicationIDSGd}, scTimers, quiet())
	peerOne := gw.Connect(diameter.PeerConfig{Address: one.server.Addr(), OriginHost: one.host})
	defer peerOne.Stop()
	peerTwo := gw.Connect(diameter.PeerConfig{Address: two.server.Addr(), OriginHost: two.host})
	defer peerTwo.Stop()
	group, err := diameter.NewPeerGroup(metrics.NewRegistry(), string(config.RoleIPSMGW), peerOne, peerTwo)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "both connections to open", func() bool { return peerOne.Available() && peerTwo.Available() })
	// submit sends an OFR to the realm of the SC and returns the instance
	// that took the short message.
	submit := func() string {
		t.Helper()
		ofr := sgd.MOForwardShortMessageRequest{Header: diameter.NewHeader(gw, "", domain), SCAddress: "12025550100",
			UserIdentifier: sgd.UserIdentifier{MSISDN: "12025550101"}, SMRPUI: submitTPDU(t)}.Message()
		answer, err := group.Request(t.Context(), ofr)
		if err != nil {
			t.Fatal(err)
		}
		if got := result(t, answer); got != diameter.DiameterSuccess {
			t.Fatalf("OFA with result %d, want the short message taken", got)
		}
		return answeringHost(t, answer)
	}

	ctx := t.Context()
	state, err := one.oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	functions := state.Functions
	if len(functions) != 1 || functions[0].Role != string(config.RoleSC) ||
		functions[0].OperationalState != maintenance.Enabled || functions[0].Users == nil {
		t.Errorf("functions %+v, want the SC ENABLED with a count of its users", functions)
	}
	hssUp.Store(false)
	if state, err := one.oam.State(ctx); err != nil || state.Functions[0].OperationalState != maintenance.Disabled {
		t.Errorf("the SC is %+v (%v) without an S6c peer, want DISABLED", state.Functions, err)
	}
	hssUp.Store(true)
	conf, err := one.oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"sc":{"address":"12025550100"}`; !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}

	if host := submit(); host != one.host {
		t.Fatalf("the OFR reached %s, want the first instance %s", host, one.host)
	}
	// The short message is held, and its first attempt ends at once, so
	// the instance has nothing in its hands.
	until(t, "the first instance to have nothing in hand", func() bool {
		state, err := one.oam.State(ctx)
		return err == nil && *state.Functions[0].Users == 0
	})
	if f := one.set(t, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v, want LOCKED with nothing in hand", f)
	}
	if peerOne.Available() {
		t.Error("the connection to the locked instance still takes requests")
	}
	if conn, err := net.DialTimeout("tcp", one.server.Addr(), 5*time.Second); err == nil {
		conn.Close()
		t.Error("the locked instance took a new connection")
	}
	if host := submit(); host != two.host {
		t.Errorf("the OFR reached %s while the first instance is locked, want the second %s", host, two.host)
	}

	one.set(t, maintenance.Unlocked)
	until(t, "the connection to the unlocked instance to open again", peerOne.Available)
	if host := submit(); host != one.host {
		t.Errorf("after the unlock the OFR reached %s, want the first instance %s", host, one.host)
	}
}

// answeringHost is the Origin-Host of answer.
func answeringHost(t *testing.T, answer *diam.Message) string {
	t.Helper()
	a, err := answer.FindAVP(avp.OriginHost, 0)
	if err != nil {
		t.Fatalf("the answer carries no Origin-Host: %v", err)
	}
	return string(a.Data.(datatype.DiameterIdentity))
}
