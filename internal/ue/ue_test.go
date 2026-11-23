package ue_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

const (
	domain   = "ims.mnc001.mcc001.3gppnetwork.org"
	impi     = "001010000000001@" + domain
	impu     = "sip:001010000000001@" + domain
	password = "test-password-1"

	impi2     = "001010000000002@" + domain
	impu2     = "sip:001010000000002@" + domain
	password2 = "test-password-2"
)

// quiet discards logs; UE_TEST_LOG=1 shows them when a test needs
// looking into.
func quiet() *slog.Logger {
	if os.Getenv("UE_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

var timers = diameter.Timers{Watchdog: time.Minute, Reconnect: time.Second, Answer: 2 * time.Second}

func stackConfig(t *testing.T, cfg sip.Config) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	cfg.SentBy = tr.LocalAddr().String()
	s, err := sip.NewStack(tr, cfg, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func stack(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: onRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// names maps the host names of the test network to loopback stacks, in
// place of DNS. A name with a port, such as the P-CSCF's protected server
// port, is a name of its own; a lookup with a port it does not know
// falls back to the name alone.
// The nodes look names up from their own goroutines while the test is
// still adding names, and a SIP message over a socket is no
// synchronization the race detector sees; hence the lock.
type names struct {
	mu    sync.Mutex
	hosts map[string]netip.AddrPort
}

func (n *names) add(host string, addr netip.AddrPort) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts[host] = addr
}

func (n *names) Locate(_ context.Context, host string, port int, _ sip.Transport) ([]sip.Destination, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if a, ok := n.hosts[net.JoinHostPort(host, strconv.Itoa(port))]; ok {
		return []sip.Destination{{Transport: sip.UDP, Addr: a}}, nil
	}
	if a, ok := n.hosts[host]; ok {
		return []sip.Destination{{Transport: sip.UDP, Addr: a}}, nil
	}
	return nil, errors.New("no such host: " + host)
}

// regEventLog keeps the "reg event" lines that a node logs, so that a
// test can wait for one.
type regEventLog struct {
	mu    sync.Mutex
	lines []map[string]string
}

type regEventHandler struct {
	log   *regEventLog
	attrs []slog.Attr
}

func (h regEventHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h regEventHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "reg event" {
		return nil
	}
	line := map[string]string{}
	for _, a := range h.attrs {
		line[a.Key] = a.Value.String()
	}
	r.Attrs(func(a slog.Attr) bool {
		line[a.Key] = a.Value.String()
		return true
	})
	h.log.mu.Lock()
	h.log.lines = append(h.log.lines, line)
	h.log.mu.Unlock()
	return nil
}

func (h regEventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return regEventHandler{log: h.log, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h regEventHandler) WithGroup(string) slog.Handler { return h }

// waitFor waits until a line has every key and value of want.
func (l *regEventLog) waitFor(t *testing.T, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for _, line := range l.lines {
			match := true
			for k, v := range want {
				match = match && line[k] == v
			}
			if match {
				l.mu.Unlock()
				return
			}
		}
		l.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no reg event line with %v", want)
}

func (n *names) ENUM(context.Context, string, string) (string, error) {
	return "", sipdns.ErrNoENUMRecord
}

// network starts the HSS, the S-CSCF, the I-CSCF and the P-CSCF on
// loopback, joined by real Diameter connections, and returns the
// P-CSCF's address.
func network(t *testing.T, minExpires time.Duration) netip.AddrPort {
	t.Helper()
	return networkWith(t, minExpires, time.Hour).pcscf
}

// testNetwork is what networkWith started, for a test that looks inside.
type testNetwork struct {
	pcscf      netip.AddrPort
	pcscfStore *pcscf.MemoryStore
	pcscfLog   *regEventLog
	scscf      *scscf.SCSCF
	// dns names the hosts of the test network, for a test that adds
	// one, such as an application server.
	dns *names
	// provisioning is the HSS provisioning API.
	provisioning http.Handler
	// pcscfProtected is the P-CSCF's protected server port, where UEs
	// with IMS AKA send (TS 33.203 7.1).
	pcscfProtected netip.AddrPort
	// epc is the API of the EPC stub, "" without one.
	epc string
	// hss holds the instances of the HSS, in the order the CSCFs reach
	// them, for a test that stops one.
	hss []hssInstance
}

// hssInstance is one instance of the HSS of the test network.
type hssInstance struct {
	// host is the instance's Origin-Host. Each instance has its own,
	// since a responder refuses a second connection from one Origin-Host
	// (RFC 6733 5.6).
	host   string
	server *diameter.Server
}

// networkWith is network with the longest registration the S-CSCF
// grants.
func networkWith(t *testing.T, minExpires, maxExpires time.Duration) testNetwork {
	t.Helper()
	return startNetwork(t, networkConfig{minExpires: minExpires, maxExpires: maxExpires})
}

// networkConfig is what startNetwork varies.
type networkConfig struct {
	minExpires, maxExpires time.Duration
	// subscriptions are those of the HSS; nil means the two test users,
	// each with a SIP URI only.
	subscriptions []hss.Subscription
	// criteria are the initial filter criteria of every user.
	criteria []ifc.InitialFilterCriteria
	// epc starts the role epc, the EPC stub with its PCRF, which the
	// P-CSCF asks over Rx.
	epc bool
	// hssInstances is how many instances the HSS runs as, each with its
	// own Origin-Host and all of them on one shared store. Zero means
	// one instance, with the store in its own process.
	hssInstances int
	// metrics is the registry in which the P-CSCF and the S-CSCF count,
	// as the roles of one node do; nil gives each a registry of its own.
	metrics *metrics.Registry
}

// The UEs of the EPC stub have loopback addresses of their own, as UEs
// in Compose have their own addresses on the bridge, so that the PCRF
// binds the Rx sessions of the P-CSCF to their PDN connections by
// Framed-IP-Address (TS 29.213 5.2). UE 1 has 127.0.0.1: its VoLTE
// INVITE is too large for UDP and goes over TCP (RFC 3261 18.1.1), and a
// TCP connection on loopback comes from 127.0.0.1 whatever address the
// UE listens on, while the P-CSCF knows the UE by its address.
var epcAddress = map[string]netip.Addr{
	"001010000000001": netip.MustParseAddr("127.0.0.1"),
	"001010000000002": netip.MustParseAddr("127.0.0.3"),
}

// stubCell is the cell of the EPC stub, whose identity the UE writes
// into P-Access-Network-Info.
var stubCell = epc.SystemInformation{
	TrackingAreaIdentity: epc.TrackingAreaIdentity{PLMN: epc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 0x33C4},
	CellIdentity:         0x76B4321,
}

// startEPC starts the EPC stub with its PCRF, and gives p policy control
// over Rx against it, as cmd/ims does for a pcscf with a PCRF. It
// returns the stub's API.
func startEPC(t *testing.T, p *pcscf.PCSCF, pcscfAddr netip.Addr) string {
	t.Helper()
	pcrfID := diameter.Identity{OriginHost: "pcrf." + domain, OriginRealm: domain}
	pcrfNode := diameter.NewNode(pcrfID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	var subscribers []epc.Subscriber
	for imsi, addr := range epcAddress {
		subscribers = append(subscribers, epc.Subscriber{IMSI: imsi, Address: addr})
	}
	stub := epc.New(epc.Config{APN: ue.IMSAPN, PCSCFAddresses: []netip.Addr{pcscfAddr}, Subscribers: subscribers, Cell: stubCell},
		epc.NewMemoryStore(), pcrfNode, quiet())
	pcrfNode.Handle(stub.HandleRx)
	server, err := pcrfNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	stub.SetAFs(server)
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)

	afNode := diameter.NewNode(diameter.Identity{OriginHost: "pcscf." + domain, OriginRealm: domain},
		[]uint32{diameter.ApplicationIDRx}, timers, quiet())
	var client atomic.Pointer[rx.Client]
	afNode.Handle(func(m *diam.Message) *diam.Message { return client.Load().Handle(m) })
	peer := afNode.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: pcrfID.OriginHost})
	t.Cleanup(peer.Stop)
	client.Store(rx.NewClient(afNode, peer, domain, p.PolicyEvent, quiet()))
	p.SetPolicyControl(client.Load())
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := peer.Request(context.Background(), rx.SessionTerminationRequest{Header: diameter.NewHeader(afNode, "", domain),
			TerminationCause: rx.DiameterLogout}.Message())
		if err == nil {
			break
		}
		if !errors.Is(err, diameter.ErrPeerUnavailable) || time.Now().After(deadline) {
			t.Fatalf("Rx never opened: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return api.URL
}

func startNetwork(t *testing.T, cfg networkConfig) testNetwork {
	t.Helper()
	minExpires, maxExpires := cfg.minExpires, cfg.maxExpires
	if cfg.subscriptions == nil {
		cfg.subscriptions = []hss.Subscription{
			{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password},
			{PrivateIdentity: impi2, PublicIdentities: []string{impu2}, Password: password2},
		}
	}
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	hssRole, instances := startHSS(t, cfg)
	// Every instance of a server farm can answer, so the CSCFs take them
	// all as one group.
	connect := func(host string, handle diameter.Handler) (*diameter.Node, *diameter.PeerGroup) {
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, timers, quiet())
		node.Handle(handle)
		var peers []*diameter.Peer
		for _, instance := range instances {
			peer := node.Connect(diameter.PeerConfig{Address: instance.server.Addr(), OriginHost: instance.host})
			t.Cleanup(peer.Stop)
			peers = append(peers, peer)
		}
		return node, diameter.NewPeerGroup(peers...)
	}
	dns := &names{hosts: map[string]netip.AddrPort{}}

	// The S-CSCF takes the HSS's RTRs, but it is made after its Cx peer.
	var scscfRole atomic.Pointer[scscf.SCSCF]
	scscfNode, scscfPeer := connect("scscf."+domain, func(m *diam.Message) *diam.Message { return scscfRole.Load().Handle(m) })
	s, err := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", Host: "scscf." + domain, Port: 5060,
		HomeDomain: domain, MinExpires: minExpires, MaxExpires: maxExpires, Metrics: cfg.metrics}, scscfPeer, scscfNode, scscf.NewMemoryStore(), dns, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	scscfRole.Store(s)
	scscfStack := stackConfig(t, sip.Config{OnRequest: s.OnRequest, OnACK: s.OnACK, OnCANCEL: s.OnCANCEL})
	s.SetStack(scscfStack)
	dns.add("scscf."+domain, scscfStack.LocalAddr())

	icscfNode, icscfPeer := connect("icscf."+domain, nil)
	i := icscf.New(icscf.Config{HomeDomain: domain, SCSCFs: []icscf.SCSCF{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}},
		Host: "icscf." + domain, Port: 5060},
		icscfPeer, icscfNode, dns, quiet())
	icscfStack := stackConfig(t, sip.Config{OnRequest: i.OnRequest, OnCANCEL: i.OnCANCEL})
	i.SetStack(icscfStack)
	dns.add(domain, icscfStack.LocalAddr())

	pcscfStore, pcscfLog := pcscf.NewMemoryStore(), &regEventLog{}
	protectedTransport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	protectedPort := protectedTransport.LocalAddr().Port()
	p, err := pcscf.New(pcscf.Config{Host: "pcscf." + domain, Port: 5060, ProtectedServerPort: int(protectedPort), VisitedNetworkID: domain,
		Metrics: cfg.metrics},
		dns, pcscfStore, nil, slog.New(regEventHandler{log: pcscfLog}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	pcscfStack := stackConfig(t, sip.Config{Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest, OnACK: p.OnACK, OnCANCEL: p.OnCANCEL})
	var epcAPI string
	if cfg.epc {
		epcAPI = startEPC(t, p, pcscfStack.LocalAddr().Addr())
	}
	p.SetStack(pcscfStack)
	dns.add("pcscf."+domain, pcscfStack.LocalAddr())
	protectedStack, err := sip.NewStack(protectedTransport, sip.Config{SentBy: protectedTransport.LocalAddr().String(),
		Screen: p.Screen, OnRequest: p.OnProtectedRequest, OnACK: p.OnProtectedACK, OnCANCEL: p.OnProtectedCANCEL}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(protectedStack.Close)
	p.SetProtectedStack(protectedStack)
	dns.add(net.JoinHostPort("pcscf."+domain, strconv.Itoa(int(protectedPort))), protectedStack.LocalAddr())

	// The Cx connections open in the background; wait until a request
	// would get through on each of them, so that a test that stops one
	// instance of the HSS finds the connection to the next one open.
	deadline := time.Now().Add(5 * time.Second)
	for _, peer := range slices.Concat(scscfPeer.Peers(), icscfPeer.Peers()) {
		for {
			_, err := peer.Request(context.Background(), cx.UserAuthorizationRequest{
				Header: diameter.NewHeader(icscfNode, "", domain), UserName: impi, PublicIdentity: impu,
				VisitedNetworkIdentifier: domain}.Message())
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Cx never opened: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	return testNetwork{pcscf: pcscfStack.LocalAddr(), pcscfProtected: protectedStack.LocalAddr(), pcscfStore: pcscfStore, pcscfLog: pcscfLog, scscf: s, dns: dns,
		provisioning: hssRole.Provisioning(instances[0].server), epc: epcAPI, hss: instances}
}

// startHSS starts the instances of the HSS that cfg asks for and
// returns the first of them, whose provisioning API the test uses, and
// all of them. One instance keeps its subscribers in its own process,
// as a network with one HSS does; several share one store, which is
// what lets any of them answer for any subscriber (TS 29.228 6.4).
func startHSS(t *testing.T, cfg networkConfig) (*hss.HSS, []hssInstance) {
	t.Helper()
	instances := max(cfg.hssInstances, 1)
	store, locks := sharedSubscribers(t, cfg, instances)
	var first *hss.HSS
	var started []hssInstance
	for i := range instances {
		host := "hss." + domain
		if instances > 1 {
			host = fmt.Sprintf("hss-%d.%s", i+1, domain)
		}
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, timers, quiet())
		role := hss.New(hss.Config{
			Realm: domain, VisitedNetworks: []string{domain},
			ServerCapabilities:    cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
			InitialFilterCriteria: cfg.criteria,
			UserLocks:             locks,
		}, store, node, quiet())
		node.Handle(role.Handle)
		server, err := node.Listen("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(server.Stop)
		if first == nil {
			first = role
		}
		started = append(started, hssInstance{host: host, server: server})
	}
	return first, started
}

// sharedSubscribers is the store that the instances of the HSS share,
// with the subscriptions of cfg in it, and the lock per subscriber that
// goes with it. One instance alone keeps both in its own process.
func sharedSubscribers(t *testing.T, cfg networkConfig, instances int) (hss.Store, ims.UserLocker) {
	t.Helper()
	if instances == 1 {
		store, err := hss.NewMemoryStore(cfg.subscriptions...)
		if err != nil {
			t.Fatal(err)
		}
		return store, nil
	}
	kv := kvstore.NewMemoryStore(nil)
	store := hssstore.New(kv, kvjson.Config{})
	for _, sub := range cfg.subscriptions {
		if err := store.Add(t.Context(), sub); err != nil {
			t.Fatal(err)
		}
	}
	return store, kvlock.New(kv, kvlock.Config{Role: "hss", Log: quiet()})
}

func newUE(t *testing.T, pcscfAddr netip.AddrPort, pass string) *ue.UE {
	return newUEAsking(t, pcscfAddr, pass, 0)
}

func newUEAsking(t *testing.T, pcscfAddr netip.AddrPort, pass string, expires time.Duration) *ue.UE {
	s := stack(t, func(st *sip.ServerTransaction) {})
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: pass, HomeDomain: domain, PCSCF: pcscfAddr, Expires: expires}
	return ue.New(cfg, s, ue.NewContact(cfg, s.LocalAddr()), quiet())
}

func TestRegisterRefreshAndDeregisterThroughTheWholeIMS(t *testing.T) {
	addr := network(t, time.Second)
	u := newUE(t, addr, password)
	ctx := context.Background()

	reg, err := u.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reg.Expires != time.Hour || len(reg.ServiceRoute) != 1 || len(reg.PublicIdentities) != 1 || reg.PublicIdentities[0] != impu {
		t.Errorf("registration = %+v", reg)
	}
	if _, err := u.Register(ctx); err != nil {
		t.Fatalf("re-registration: %v", err)
	}
	if err := u.Deregister(ctx); err != nil {
		t.Fatalf("deregistration: %v", err)
	}
}

// TS 24.341 5.3.2.2: the UE registers the feature tag of SMS over IP,
// and the P-CSCF and the I-CSCF pass it on, so that the S-CSCF keeps it
// with the binding.
func TestTheFeatureTagsReachTheSCSCF(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	w := &wireLog{}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), w.observe)
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain, PCSCF: n.pcscf,
		Features: ue.DefaultFeatures}
	u := ue.New(cfg, s, ue.NewContact(cfg, s.LocalAddr()), quiet())
	if _, err := u.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The 200 lists the binding with the parameters the S-CSCF keeps
	// (TS 24.229 5.4.1.2.2F item f), and the expires it chose.
	binding := w.binding(t)
	kept := slices.DeleteFunc(slices.Clone(binding.Params), func(p sip.Param) bool { return p.Name == "expires" })
	if want := (sip.Params{{Name: "+g.3gpp.smsip"}}); !slices.Equal(kept, want) {
		t.Errorf("the S-CSCF keeps the contact parameters %v, want %v", kept, want)
	}
}

func TestWrongPasswordIsRefused(t *testing.T) {
	u := newUE(t, network(t, time.Second), "wrong-password")
	if _, err := u.Register(context.Background()); !errors.Is(err, ue.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// TestRegistrationWhileTheSCSCFIsShutDown: the operator has shut the
// S-CSCF down, so it answers the REGISTER 503 with Retry-After. The
// I-CSCF has no other S-CSCF to send it to, and answers 500 without
// Retry-After rather than pass the 503 on (RFC 3261 16.7 step 6), which
// the P-CSCF passes to the UE. The UE would read a 503 as said of its
// P-CSCF (TS 24.229 5.1.1.2.1).
func TestRegistrationWhileTheSCSCFIsShutDown(t *testing.T) {
	n := networkWith(t, time.Second, time.Hour)
	node, err := maintenance.New(maintenance.Config{
		Node:          "scscf",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{n.scscf.Function(func() bool { return true })},
		Configuration: func() any { return map[string]string{"node": "scscf"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	n.scscf.Manage(node.ManagedFunction("scscf"))
	if f, err := node.SetAdministrativeState("scscf", maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown}); err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED", f, err)
	}
	_, err = newUE(t, n.pcscf, password).Register(t.Context())
	var failure *ue.RegisterFailure
	if !errors.As(err, &failure) || failure.StatusCode != 500 || failure.RetryAfter != 0 {
		t.Errorf("Register = %v, want 500 without Retry-After", err)
	}
}

func TestTooBriefIntervalIsRaisedToMinExpires(t *testing.T) {
	u := newUEAsking(t, network(t, 2*time.Minute), password, 30*time.Second)
	reg, err := u.Register(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reg.Expires != 2*time.Minute {
		t.Errorf("registered for %v, want the 2 min of Min-Expires", reg.Expires)
	}
}

// TestRegistrationWithoutAnIdentityToUse is TS 24.229 5.1.1.2.1 item c:
// a 200 that lists no identity in P-Associated-URI leaves the UE with
// only the identity it registered, which is then barred.
func TestRegistrationWithoutAnIdentityToUse(t *testing.T) {
	pcscf := stack(t, func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Contact", st.Request.Get("Contact"))
		st.Respond(resp)
	})
	s := stack(t, func(st *sip.ServerTransaction) {})
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: pcscf.LocalAddr()}
	u := ue.New(cfg, s, ue.NewContact(cfg, s.LocalAddr()), quiet())
	if _, err := u.Register(t.Context()); err == nil || !strings.Contains(err.Error(), "P-Associated-URI") {
		t.Errorf("Register = %v, want an error about P-Associated-URI", err)
	}
}
