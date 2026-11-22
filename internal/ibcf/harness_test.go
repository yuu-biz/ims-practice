package ibcf_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file builds two IMS networks in one process, joined by their
// IBCFs, for the tests of network_test.go. Each network has a UE (the
// UA of package sip), a fake I-CSCF and a fake S-CSCF (small proxies on
// package sip), a fake TrGW and its IBCFs. Network A runs two IBCF
// instances with one Store. Each instance has a name of its own on
// each side, which its dialogs carry in Contact, and both share a pool
// name on each side, which the S-CSCF and the other network send the
// initial requests to; a test moves the pool name from one instance to
// the other.
//
// The networks have addresses of their own on the loopback interface,
// so that a test can tell them apart in what each one receives:
// 127.0.1.0/24 inside network A, 127.0.2.0/24 inside network B, and
// 127.0.3.0/24 between the two, where the IBCFs and TrGWs face each
// other (the II-NNI).

var testTimers = sip.Timers{T1: 20 * time.Millisecond, T2: 160 * time.Millisecond, T4: 100 * time.Millisecond}

// The test numbers are fictional (555-0100 to 555-0199), and the
// private identities are of the test PLMNs 001/01 and 001/02.
const (
	domainA = "ims.mnc001.mcc001.3gppnetwork.org"
	domainB = "ims.mnc002.mcc001.3gppnetwork.org"
	numberA = "+12025550123"
	numberB = "+12025550151"
)

// names is the DNS of the test: every host name of both networks, so
// that the IBCFs find each other and the nodes of their own network.
type names struct {
	mu    sync.Mutex
	hosts map[string]netip.AddrPort
}

func (n *names) set(host string, addr netip.AddrPort) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts[strings.ToLower(host)] = addr
}

func (n *names) Locate(_ context.Context, host string, port int, transport sip.Transport) ([]sip.Destination, error) {
	if transport == "" {
		transport = sip.UDP
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if port == 0 {
			port = 5060
		}
		return []sip.Destination{{Transport: transport, Addr: netip.AddrPortFrom(ip, uint16(port))}}, nil
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	addr, ok := n.hosts[strings.ToLower(host)]
	if !ok {
		return nil, fmt.Errorf("names: no address for %s", host)
	}
	return []sip.Destination{{Transport: transport, Addr: addr}}, nil
}

func (n *names) locate(u sip.URI) ([]sip.Destination, error) {
	return ims.Targets(context.Background(), n, u)
}

// capture is one message that a node of a network received.
type capture struct {
	node string
	sip.Capture
}

// recorder keeps what the nodes of one network receive.
type recorder struct {
	mu  sync.Mutex
	got []capture
}

func (r *recorder) observer(node string) sip.Observer {
	return func(c sip.Capture) {
		if c.Sent {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.got = append(r.got, capture{node: node, Capture: c})
	}
}

func (r *recorder) received() []capture {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capture(nil), r.got...)
}

// transaction identifies a request at the server that receives it: the
// branch and sent-by of the top Via and the method (RFC 3261 17.2.3). A
// request that comes again with the same three is a retransmission of
// the first, not a request of its own.
type transaction struct {
	branch, sentBy string
	method         sip.Method
}

// transactions are the requests that node received, one for each
// transaction, in the order in which the first copy of each arrived.
// Over UDP a client retransmits a request while it waits for the
// response (RFC 3261 17.1.1.2, 17.1.2.2), so the number of copies
// depends on how fast the other nodes answer; the number of
// transactions does not.
func (r *recorder) transactions(node string) []capture {
	var out []capture
	seen := map[transaction]bool{}
	for _, c := range r.received() {
		if c.node != node || !c.Message.IsRequest() {
			continue
		}
		v, _ := sip.ParseVia(c.Message.Get("Via"))
		key := transaction{branch: v.Branch(), sentBy: v.SentBy(), method: c.Message.Method}
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out
}

// requests are the requests of method that node received, one for each
// transaction.
func (r *recorder) requests(node string, method sip.Method) []*sip.Message {
	var out []*sip.Message
	for _, c := range r.transactions(node) {
		if c.Message.Method == method {
			out = append(out, c.Message)
		}
	}
	return out
}

// reachableStore is the Store of a network's IBCF: a MemoryStore that
// fails every operation while down is set, as a store outside the
// process does that the IBCF cannot reach.
type reachableStore struct {
	*ibcf.MemoryStore
	down atomic.Bool
}

var errUnreachable = errors.New("the store cannot be reached")

func (s *reachableStore) PutCall(ctx context.Context, r ibcf.CallRecord) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.MemoryStore.PutCall(ctx, r)
}

func (s *reachableStore) DeleteCall(ctx context.Context, instance, id string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.MemoryStore.DeleteCall(ctx, instance, id)
}

func (s *reachableStore) CallsOf(ctx context.Context, instance string) ([]ibcf.CallRecord, error) {
	if s.down.Load() {
		return nil, errUnreachable
	}
	return s.MemoryStore.CallsOf(ctx, instance)
}

// callsOf are the records of instance in the network's Store.
func (n *network) callsOf(t *testing.T, instance string) []ibcf.CallRecord {
	t.Helper()
	var store ibcf.Store = n.store.MemoryStore
	if n.kv != nil {
		store = ibcfstore.New(n.kv, kvjson.Config{})
	}
	records, err := store.CallsOf(context.Background(), instance)
	if err != nil {
		t.Fatal(err)
	}
	return records
}

// fakeTrGW is a TrGW that hands out addresses without relaying
// anything, and remembers what the IBCF told it. Its address in the
// home network is core, and towards the peers peer. With oneAddress it
// writes relayAddress for both terminations instead, as the media relay
// does, and the IBCF of its network is set up to show core and peer in
// its place.
type fakeTrGW struct {
	core, peer netip.Addr
	oneAddress bool

	mu       sync.Mutex
	next     int
	contexts map[string]*fakeContext
	reserved int
	// held are the remote addresses for which Modify waits until the
	// channel is closed: a test holds back what the IBCF does after it.
	held map[netip.AddrPort]chan struct{}
}

// relayAddress is the address that a fakeTrGW with one address writes.
var relayAddress = netip.MustParseAddr("127.0.0.1")

// fakeSides are the sides that the terminations of a TrGW context face,
// by the names that the IBCF gives them.
var fakeSides = map[agw.TerminationID]ibcf.Side{agw.Core: ibcf.CoreSide, agw.Access: ibcf.PeerSide}

// hold has Modify for remote wait until the returned function is
// called.
func (f *fakeTrGW) hold(remote netip.AddrPort) (release func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[netip.AddrPort]chan struct{}{}
	}
	ch := make(chan struct{})
	f.held[remote] = ch
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// fakeContext is a context of a fakeTrGW: local is what the SDP of each
// side is to show, and remote where each termination was told to send.
type fakeContext struct {
	local  ibcf.MediaContext
	remote map[ibcf.Side]netip.AddrPort
}

func newFakeTrGW(core, peer string, oneAddress bool) *fakeTrGW {
	return &fakeTrGW{core: netip.MustParseAddr(core), peer: netip.MustParseAddr(peer), oneAddress: oneAddress, contexts: map[string]*fakeContext{}}
}

func (f *fakeTrGW) Add(_ context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	f.reserved++
	port := uint16(30000 + 4*f.next)
	c := &fakeContext{
		local:  ibcf.MediaContext{ID: fmt.Sprintf("c%d", f.next), Core: netip.AddrPortFrom(f.core, port), Peer: netip.AddrPortFrom(f.peer, port+2)},
		remote: map[ibcf.Side]netip.AddrPort{},
	}
	out := agw.Context{ID: c.local.ID, Terminations: map[agw.TerminationID]*agw.Termination{}}
	for _, t := range terminations {
		side, ok := fakeSides[t.ID]
		if !ok {
			return agw.Context{}, fmt.Errorf("%w: termination %q", agw.ErrBadRequest, t.ID)
		}
		local := c.local.Core
		if side == ibcf.PeerSide {
			local = c.local.Peer
		}
		if f.oneAddress {
			local = netip.AddrPortFrom(relayAddress, local.Port())
		}
		out.Terminations[t.ID] = &agw.Termination{ID: t.ID, Local: local}
	}
	f.contexts[c.local.ID] = c
	return out, nil
}

func (f *fakeTrGW) Modify(_ context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	f.mu.Lock()
	held := f.held[change.Remote]
	f.mu.Unlock()
	if held != nil {
		<-held
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.contexts[id]
	if !ok {
		return agw.ErrNoContext
	}
	side, ok := fakeSides[termination]
	if !ok {
		return agw.ErrNoTermination
	}
	c.remote[side] = change.Remote
	return nil
}

func (f *fakeTrGW) Subtract(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.contexts[id]; !ok {
		return agw.ErrNoContext
	}
	delete(f.contexts, id)
	return nil
}

// open is the contexts that are not released, and how many were
// reserved in all.
func (f *fakeTrGW) open() (open []fakeContext, reserved int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.contexts {
		open = append(open, fakeContext{local: c.local, remote: clone(c.remote)})
	}
	return open, f.reserved
}

func clone[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// fakeCSCF is a proxy of the home network: an I-CSCF, which sends every
// initial request to the S-CSCF, or an S-CSCF, which sends a request
// for its own domain to the UE and any other through the IBCF, and
// asserts the caller on the way out.
type fakeCSCF struct {
	own   sip.URI
	names *names
	stack ims.StackRef
	// initial routes an initial request. It reports whether to
	// record-route, or a status code to answer with.
	initial func(out *sip.Message) (recordRoute bool, status int)
}

func (f *fakeCSCF) onRequest(st *sip.ServerTransaction) {
	out := st.Request.Clone()
	sip.DecrementMaxForwards(out)
	sip.PopOwnRoute(out, f.own)
	if !ims.IsInDialog(st.Request) {
		rr, status := f.initial(out)
		if status != 0 {
			st.Respond(sip.NewResponse(st.Request, status, sip.NewTag()))
			return
		}
		if rr && ims.CreatesDialog(out.Method) {
			sip.AddRecordRoute(out, f.own)
		}
	}
	u, err := sip.NextHop(out)
	var targets []sip.Destination
	if err == nil {
		targets, err = f.names.locate(u)
	}
	if err != nil {
		st.Respond(sip.NewResponse(st.Request, 404, sip.NewTag()))
		return
	}
	final, err := st.Proxy(out, targets...)
	ims.Finish(st, final, err)
}

func (f *fakeCSCF) onACK(ack *sip.Message) {
	out := ack.Clone()
	sip.PopOwnRoute(out, f.own)
	u, err := sip.NextHop(out)
	if err != nil {
		return
	}
	targets, err := f.names.locate(u)
	if stack, serr := f.stack.Get(); err == nil && serr == nil {
		stack.SendACK(out, targets...)
	}
}

// network is one of the two IMS networks.
type network struct {
	name     string
	plan     plan
	domain   string
	internal netip.Prefix
	number   string
	// The pool URIs of the IBCF, which every instance of the network
	// answers to.
	ibcfInternal, ibcfExternal sip.URI

	rec   *recorder
	names *names
	trgw  *fakeTrGW
	store *reachableStore
	// kv, when set, is the shared store of the network in place of
	// store: each process of an IBCF instance keeps its records there
	// through an ibcfstore.Store of its own, as the nodes of a network
	// on a shared store do.
	kv kvstore.Store
	// registries are the counters of each running IBCF instance, as
	// each process of an instance has its own.
	registries []*metrics.Registry
	peerOf     ibcf.Peer
	icscf      sip.URI
	ue         *sip.UA
	ueRoute    string
	contact    string
	media      netip.AddrPort
	calls      chan *sip.IncomingCall
	messages   chan *sip.Message

	mu sync.Mutex
	// messagesHeld, until it is closed, keeps the UE from answering
	// the MESSAGEs it gets.
	messagesHeld chan struct{}

	// instances are the IBCF instances, ibcfs the running IBCF of each.
	instances []ibcfInstance
	ibcfs     []*ibcf.IBCF
}

// ibcfInstance is where one IBCF instance listens, and its names.
type ibcfInstance struct {
	internal, external         netip.AddrPort
	internalHost, externalHost string
	// transports are its internal and its external transport, until it
	// starts.
	transports [2]*sip.TransportLayer
}

// use points the IBCF's pool names at instance i: the initial requests
// of either side go there from now on.
func (n *network) use(i int) {
	n.names.set(n.ibcfInternal.Host, n.instances[i].internal)
	n.names.set(n.ibcfExternal.Host, n.instances[i].external)
}

// plan is what tells the two networks apart.
type plan struct {
	name, domain, number, imsi string
	internal                   string   // the first three octets inside
	nniName                    string   // the IBCF's pool name at the II-NNI
	nni                        []string // the NNI address of each IBCF instance
	trgwPeer                   string
	// oneAddressTrGW has the TrGW write one address for both
	// terminations, as the media relay does.
	oneAddressTrGW bool
}

var (
	planA = plan{name: "a", domain: domainA, number: numberA, imsi: "001010000000001", internal: "127.0.1",
		nniName: "ibcf.nni.mnc001.example.net", nni: []string{"127.0.3.1", "127.0.3.2"}, trgwPeer: "127.0.3.50",
		oneAddressTrGW: true}
	planB = plan{name: "b", domain: domainB, number: numberB, imsi: "001020000000001", internal: "127.0.2",
		nniName: "ibcf.nni.mnc002.example.net", nni: []string{"127.0.3.11"}, trgwPeer: "127.0.3.60"}
)

// instanceHosts are the names of instance i of the IBCF of p, inside
// and at the II-NNI.
func (p plan) instanceHosts(i int) (internal, external string) {
	nni := strings.TrimPrefix(p.nniName, "ibcf.")
	return fmt.Sprintf("ibcf-%d.%s", i+1, p.domain), fmt.Sprintf("ibcf-%d.%s", i+1, nni)
}

// pair is the two networks, A and B.
type pair struct {
	a, b *network
}

// defaultProfile is the agreement of a network with the other one in a
// test that passes: untrusted, both transports, MESSAGE allowed, SIP
// URIs with user=phone, and every codec of the UEs but AMR, which the
// border filters out.
func defaultProfile(peer plan) interconnect.Profile {
	return interconnect.Profile{
		Name:         "peer-" + peer.name,
		Domain:       peer.domain,
		Transports:   []sip.Transport{sip.UDP, sip.TCP},
		Methods:      []sip.Method{sip.MESSAGE},
		NumberFormat: interconnect.NumberFormatSIP,
		Codecs:       []string{"AMR-WB", "PCMU", "telephone-event"},
	}
}

// newPair starts both networks. tweak may change network A's profile of
// B (ofB) and network B's profile of A (ofA) first.
func newPair(t *testing.T, tweak func(ofB, ofA *interconnect.Profile)) *pair {
	t.Helper()
	return newPairOn(t, tweak, false)
}

// newPairOnKV starts both networks, each with a shared store of its own
// (network.kv): a kvstore.MemoryStore, in place of the Valkey server of
// a network.
func newPairOnKV(t *testing.T) *pair {
	t.Helper()
	return newPairOn(t, nil, true)
}

func newPairOn(t *testing.T, tweak func(ofB, ofA *interconnect.Profile), onKV bool) *pair {
	t.Helper()
	dns := &names{hosts: map[string]netip.AddrPort{}}
	a, b := newNetwork(t, planA, dns), newNetwork(t, planB, dns)
	if onKV {
		a.kv, b.kv = kvstore.NewMemoryStore(nil), kvstore.NewMemoryStore(nil)
	}
	ofB, ofA := defaultProfile(planB), defaultProfile(planA)
	if tweak != nil {
		tweak(&ofB, &ofA)
	}
	a.peerOf, b.peerOf = b.peer(ofB), a.peer(ofA)
	a.startIBCFs(t)
	b.startIBCFs(t)
	a.use(0)
	b.use(0)
	return &pair{a: a, b: b}
}

var discard = slog.New(slog.NewTextHandler(debugOut(), nil))

func debugOut() io.Writer {
	if os.Getenv("IBCF_DEBUG") != "" {
		return os.Stderr
	}
	return io.Discard
}

// noLoopbackAddresses tells what a machine lacks when it cannot bind
// 127.0.x.y beyond 127.0.0.1. Linux has all of 127.0.0.0/8 on the
// loopback interface; macOS has 127.0.0.1 alone unless more addresses
// are added to lo0 as aliases.
const noLoopbackAddresses = "this machine has no loopback address beyond 127.0.0.1, " +
	"and the test gives each network and the link between them addresses of their own in 127.0.0.0/8"

// listen opens a transport on ip and port for node. A machine that
// cannot bind the loopback addresses beyond 127.0.0.1 fails the test:
// the two networks need addresses of their own, and a test that
// skipped there would pass without having run.
func listen(t *testing.T, ip string, port int, node string, rec *recorder) (*sip.TransportLayer, error) {
	t.Helper()
	tl, err := sip.ListenObserved(netip.AddrPortFrom(netip.MustParseAddr(ip), uint16(port)).String(), discard, rec.observer(node))
	if err != nil && port == 0 {
		t.Fatalf("cannot listen on %s: %s: %v", ip, noLoopbackAddresses, err)
	}
	return tl, err
}

func newStack(t *testing.T, tl *sip.TransportLayer, sentBy string, onRequest func(*sip.ServerTransaction), onACK func(*sip.Message)) *sip.Stack {
	t.Helper()
	stack, err := sip.NewStack(tl, sip.Config{SentBy: sentBy, Timers: testTimers, OnRequest: onRequest, OnACK: onACK}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	return stack
}

// newNetwork starts the UE and the fake CSCFs of a network, and opens
// the transports of its IBCF instances, whose ports the other network
// needs before the IBCFs can start.
func newNetwork(t *testing.T, p plan, dns *names) *network {
	t.Helper()
	n := &network{name: p.name, plan: p, domain: p.domain, number: p.number, rec: &recorder{}, names: dns,
		internal: netip.MustParsePrefix(p.internal + ".0/24"),
		trgw:     newFakeTrGW(p.internal+".50", p.trgwPeer, p.oneAddressTrGW), store: &reachableStore{MemoryStore: ibcf.NewMemoryStore()},
		calls: make(chan *sip.IncomingCall, 4), messages: make(chan *sip.Message, 4)}

	n.instances = n.openIBCFs(t, p)
	n.ibcfInternal = sip.URI{Scheme: "sip", Host: "ibcf." + p.domain, Port: int(n.instances[0].internal.Port())}
	n.ibcfExternal = sip.URI{Scheme: "sip", Host: p.nniName, Port: int(n.instances[0].external.Port())}

	icscf := n.startCSCF(t, "icscf", p.internal+".21")
	scscf := n.startCSCF(t, "scscf", p.internal+".20")
	n.icscf = icscf.own
	icscf.initial = func(out *sip.Message) (bool, int) {
		// TS 24.229 5.3.2.1 step 3: a Request-URI in the subdomain of the
		// conference URIs, public service identities, goes by DNS to the
		// AS that hosts it.
		if u, err := sip.ParseURI(out.RequestURI); err == nil && strings.EqualFold(u.Host, conferenceDomain(n.domain)) {
			return false, 0
		}
		// Otherwise on to the S-CSCF of the callee, without
		// record-routing.
		out.Prepend("Route", sip.NameAddr{URI: looseRoute(scscf.own)}.String())
		return false, 0
	}
	scscf.initial = func(out *sip.Message) (bool, int) {
		u, err := sip.ParseURI(out.RequestURI)
		if err == nil && strings.EqualFold(u.Host, n.domain) {
			// Terminating (TS 24.229 5.4.3.3): to the UE's contact.
			if number, _, _ := strings.Cut(u.User, ";"); number != n.number {
				return false, 404
			}
			out.RequestURI = n.contact
			return true, 0
		}
		// Originating (5.4.3.2): the network asserts the caller and
		// sends the request out through the IBCF (steps 11 and 12).
		out.Set("P-Asserted-Identity", "<sip:"+n.number+"@"+n.domain+";user=phone>")
		out.Set("P-Charging-Vector", "icid-value="+sip.NewCallID()+";orig-ioi="+n.domain)
		out.Set("P-Charging-Function-Addresses", "ccf="+p.internal+".90")
		out.Set("P-Access-Network-Info", "3GPP-E-UTRAN-FDD; utran-cell-id-3gpp=0010100010000001")
		out.Prepend("Route", sip.NameAddr{URI: looseRoute(n.ibcfInternal)}.String())
		return true, 0
	}
	n.ueRoute = sip.NameAddr{URI: looseRoute(scscf.own)}.String()

	tl, _ := listen(t, p.internal+".10", 0, "ue", n.rec)
	sentBy := tl.LocalAddr().String()
	n.contact = "sip:" + p.imsi + "@" + sentBy
	n.media = netip.AddrPortFrom(tl.LocalAddr().Addr(), 40000)
	n.startUE(t, tl)
	return n
}

// startUE starts the UE of the network on tl.
func (n *network) startUE(t *testing.T, tl *sip.TransportLayer) {
	t.Helper()
	ue, err := sip.NewUA(tl, sip.UAConfig{
		SentBy: tl.LocalAddr().String(), Timers: testTimers, Contact: n.contact,
		// The feature tag of MMTel, which the IBCFs carry to the other
		// side.
		ContactParams: sip.Params{{Name: "+g.3gpp.icsi-ref", Value: `"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`}},
		OnCall:        func(c *sip.IncomingCall) { n.calls <- c },
		OnMessage: func(st *sip.ServerTransaction) {
			n.messages <- st.Request
			n.mu.Lock()
			held := n.messagesHeld
			n.mu.Unlock()
			if held != nil {
				<-held
			}
			st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
		},
		Locate: n.names.locate,
	}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ue.Close)
	n.ue = ue
}

// holdMessages has the UE wait with its answer to a MESSAGE until the
// returned function is called.
func (n *network) holdMessages() (release func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch := make(chan struct{})
	n.messagesHeld = ch
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

// restartUE stops the UE and starts a new one at the same address,
// which knows none of the dialogs of the first one.
func (n *network) restartUE(t *testing.T) {
	t.Helper()
	addr := n.ue.Stack().LocalAddr()
	n.ue.Close()
	tl, err := listen(t, addr.Addr().String(), int(addr.Port()), "ue", n.rec)
	if err != nil {
		t.Fatalf("cannot listen again on %s: %v", addr, err)
	}
	n.startUE(t, tl)
}

func looseRoute(u sip.URI) string {
	r, _ := ims.LooseRoute(u.String())
	return r.String()
}

func (n *network) startCSCF(t *testing.T, role, ip string) *fakeCSCF {
	t.Helper()
	tl, _ := listen(t, ip, 0, role, n.rec)
	f := &fakeCSCF{own: sip.URI{Scheme: "sip", Host: role + "." + n.domain, Port: int(tl.LocalAddr().Port())}, names: n.names}
	n.names.set(f.own.Host, tl.LocalAddr())
	f.stack.Set(newStack(t, tl, f.own.Host+":"+fmt.Sprint(f.own.Port), f.onRequest, f.onACK))
	return f
}

// openIBCFs opens the two transports of each IBCF instance. The
// instances share the ports, on addresses of their own, so that one
// pool URI names any of them.
func (n *network) openIBCFs(t *testing.T, p plan) []ibcfInstance {
	t.Helper()
	for attempt := 0; ; attempt++ {
		var opened []*sip.TransportLayer
		var instances []ibcfInstance
		internalPort, externalPort := 0, 0
		var err error
		for i, nni := range p.nni {
			var in, ex *sip.TransportLayer
			in, err = listen(t, fmt.Sprintf("%s.%d", p.internal, i+1), internalPort, fmt.Sprintf("ibcf%d-internal", i+1), n.rec)
			if err != nil {
				break
			}
			opened = append(opened, in)
			ex, err = listen(t, nni, externalPort, fmt.Sprintf("ibcf%d-external", i+1), n.rec)
			if err != nil {
				break
			}
			opened = append(opened, ex)
			internalPort, externalPort = int(in.LocalAddr().Port()), int(ex.LocalAddr().Port())
			inHost, exHost := p.instanceHosts(i)
			n.names.set(inHost, in.LocalAddr())
			n.names.set(exHost, ex.LocalAddr())
			instances = append(instances, ibcfInstance{internal: in.LocalAddr(), external: ex.LocalAddr(),
				internalHost: inHost, externalHost: exHost, transports: [2]*sip.TransportLayer{in, ex}})
		}
		if err == nil {
			return instances
		}
		for _, tl := range opened {
			tl.Close()
		}
		if attempt == 4 {
			t.Fatalf("cannot open the IBCF ports: %v", err)
		}
	}
}

// peer is how the other network sees this one: its profile of this
// network, its entry point, the names of its IBCF instances, and the
// addresses its IBCFs send from.
func (n *network) peer(profile interconnect.Profile) ibcf.Peer {
	var sources []netip.Prefix
	for _, nni := range n.plan.nni {
		sources = append(sources, netip.PrefixFrom(netip.MustParseAddr(nni), 32))
	}
	var hosts []string
	for _, in := range n.instances {
		hosts = append(hosts, in.externalHost)
	}
	return ibcf.Peer{Profile: profile, EntryPoint: n.ibcfExternal.String(), Hosts: hosts, Sources: sources}
}

// startIBCFs starts the IBCF instances of the network on the transports
// that openIBCFs opened, with peerOf as the one peer.
func (n *network) startIBCFs(t *testing.T) {
	t.Helper()
	n.ibcfs = make([]*ibcf.IBCF, len(n.instances))
	n.registries = make([]*metrics.Registry, len(n.instances))
	for i := range n.instances {
		n.startIBCF(t, i)
	}
}

// startIBCF starts instance i on its transports.
func (n *network) startIBCF(t *testing.T, i int) {
	t.Helper()
	if err := n.listenIBCF(t, i); err != nil {
		t.Fatal(err)
	}
}

// listenIBCF makes instance i and has it listen on its transports. It
// returns the error of Listen, and leaves the transports open then.
func (n *network) listenIBCF(t *testing.T, i int) error {
	t.Helper()
	peers, err := ibcf.NewPeerTable(n.peerOf)
	if err != nil {
		t.Fatal(err)
	}
	icscf := n.icscf
	icscf.Port = int(n.mustLocate(t, icscf.Host).Port())
	in := n.instances[i]
	registry := metrics.NewRegistry()
	n.registries[i] = registry
	cfg := ibcf.Config{
		Internal:     sip.URI{Scheme: "sip", Host: in.internalHost, Port: int(in.internal.Port())},
		External:     sip.URI{Scheme: "sip", Host: in.externalHost, Port: int(in.external.Port())},
		InternalPool: n.ibcfInternal.Host, ExternalPool: n.ibcfExternal.Host,
		HomeDomain: n.domain, ICSCF: icscf, InternalTimers: testTimers, ExternalTimers: testTimers, Metrics: registry,
	}
	if n.trgw.oneAddress {
		// The SDP of each side shows the TrGW's address in that
		// network, not the one address that the TrGW wrote.
		cfg.TrGWCoreAddress, cfg.TrGWPeerAddress = n.trgw.core, n.trgw.peer
	}
	b, err := ibcf.New(cfg, peers, n.trgw, n.newStore(), n.names, discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Listen(in.transports[0], in.transports[1]); err != nil {
		return err
	}
	t.Cleanup(b.Close)
	n.ibcfs[i] = b
	return nil
}

// newStore is the Store of a new process of an IBCF instance: the one
// of the network, or a new adapter on the network's shared store.
func (n *network) newStore() ibcf.Store {
	if n.kv != nil {
		return ibcfstore.New(n.kv, kvjson.Config{})
	}
	return n.store
}

// restartIBCF stops instance i and starts a new process of it, with the
// same names and addresses and the same Store, or a new adapter on the
// same shared store, as a node that is recreated does. The counters of
// the new process start from zero.
func (n *network) restartIBCF(t *testing.T, i int) {
	t.Helper()
	n.stopIBCF(t, i)
	n.startIBCF(t, i)
}

// stopIBCF stops instance i, and opens its transports again for the
// next process of it. The transports of a process whose Listen failed
// are closed too.
func (n *network) stopIBCF(t *testing.T, i int) {
	t.Helper()
	n.ibcfs[i].Close()
	in := &n.instances[i]
	for _, tl := range in.transports {
		tl.Close()
	}
	for k, addr := range []netip.AddrPort{in.internal, in.external} {
		side := []string{"internal", "external"}[k]
		tl, err := listen(t, addr.Addr().String(), int(addr.Port()), fmt.Sprintf("ibcf%d-%s", i+1, side), n.rec)
		if err != nil {
			t.Fatalf("cannot listen again on %s: %v", addr, err)
		}
		in.transports[k] = tl
	}
}

func (n *network) mustLocate(t *testing.T, host string) netip.AddrPort {
	t.Helper()
	d, err := n.names.Locate(context.Background(), host, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	return d[0].Addr
}

// counter is the sum over the network's running IBCF instances of the
// sample of a counter whose name and labels are series, such as
// `ims_ibcf_session_attempts_total{peer="peer-b",direction="exit"}`; 0
// when there is none.
func (n *network) counter(t *testing.T, series string) float64 {
	t.Helper()
	var sum float64
	for _, reg := range n.registries {
		var text bytes.Buffer
		if err := reg.WriteText(&text); err != nil {
			t.Fatal(err)
		}
		s := bufio.NewScanner(&text)
		for s.Scan() {
			if value, ok := strings.CutPrefix(s.Text(), series+" "); ok {
				var v float64
				fmt.Sscan(value, &v)
				sum += v
			}
		}
	}
	return sum
}

// offer is an SDP offer of the network's UE with codecs.
func (n *network) offer(t *testing.T, codecs []sdp.Codec, version uint64, direction string) sip.Body {
	t.Helper()
	s, err := sdp.AudioOffer("1352", n.media, codecs)
	if err != nil {
		t.Fatal(err)
	}
	s.Origin.Version = version
	if direction != "" {
		s.Media[0].SetDirection(direction)
	}
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}
}

// answer is the UE's SDP answer to the offer in m.
func (n *network) answer(t *testing.T, m *sip.Message) sip.Body {
	t.Helper()
	body, err := answerTo(n, m)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// answerTo is network n's UE's answer to the offer in m, without a
// testing.T for the goroutines of the UA.
func answerTo(n *network, m *sip.Message) (sip.Body, error) {
	offer, err := sdp.Parse(m.Body)
	if err != nil {
		return sip.Body{}, err
	}
	s, err := sdp.AudioAnswer(offer, "2468", netip.AddrPortFrom(n.media.Addr(), 50000), sdp.VoLTECodecs())
	if err != nil {
		return sip.Body{}, err
	}
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}, nil
}

// answerInDialog has the UE answer a request within a dialog: an offer
// with its answer, anything else with 200.
func (n *network) answerInDialog(got chan<- *sip.Message) func(*sip.InDialogRequest) {
	return func(r *sip.InDialogRequest) {
		if got != nil {
			got <- r.Request
		}
		if !r.Offer {
			r.Accept(sip.Body{})
			return
		}
		answer, err := answerTo(n, r.Request)
		if err != nil {
			r.Reject(488)
			return
		}
		r.Accept(answer)
	}
}

// invite has the UE call requestURI, through its S-CSCF. The UE passes
// the requests within the dialog to inDialog, when it is not nil.
func (n *network) invite(t *testing.T, requestURI string, body sip.Body, inDialog chan<- *sip.Message, headers ...sip.HeaderField) *sip.OutgoingCall {
	t.Helper()
	c, err := n.ue.Invite(sip.Invitation{
		RequestURI: requestURI,
		From:       "sip:" + n.number + "@" + n.domain + ";user=phone",
		Route:      []string{n.ueRoute},
		Headers:    headers,
		Body:       body,
		OnRequest:  n.answerInDialog(inDialog),
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// message has the UE send a MESSAGE to requestURI, and returns the
// final response.
func (n *network) message(t *testing.T, requestURI, text string) *sip.Message {
	t.Helper()
	req := &sip.Message{Method: sip.MESSAGE, RequestURI: requestURI}
	req.Add("Route", n.ueRoute)
	req.Add("Max-Forwards", "70")
	req.Add("From", "<sip:"+n.number+"@"+n.domain+";user=phone>;tag="+sip.NewTag())
	req.Add("To", "<"+requestURI+">")
	req.Add("Call-ID", sip.NewCallID())
	req.Add("CSeq", "1 MESSAGE")
	req.Add("Content-Type", "text/plain")
	req.Body = []byte(text)
	return n.send(t, req)
}

// send has the UE send req, a request outside a dialog, to its next
// hop, and returns the final response.
func (n *network) send(t *testing.T, req *sip.Message) *sip.Message {
	t.Helper()
	u, _ := sip.NextHop(req)
	targets, err := n.names.locate(u)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := n.ue.Stack().SendRequest(req, targets...)
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for resp := range ct.Responses() {
		final = resp
	}
	if final == nil {
		t.Fatalf("%s got no final response: %v", req.Method, ct.Err())
	}
	return final
}

// sipURI is the URI of the network's UE as the other network calls it.
func (n *network) sipURI() string {
	return "sip:" + n.number + "@" + n.domain + ";user=phone"
}

// final waits for the final response to an INVITE.
func final(t *testing.T, c *sip.OutgoingCall) *sip.Message {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case resp, ok := <-c.Responses():
			if !ok {
				t.Fatalf("the INVITE got no final response: %v", c.Err())
			}
			if resp.StatusCode >= 200 {
				return resp
			}
		case <-timeout:
			t.Fatal("no final response to the INVITE")
		}
	}
}

// nextCall waits for the call that reaches the UE.
func (n *network) nextCall(t *testing.T) *sip.IncomingCall {
	t.Helper()
	select {
	case c := <-n.calls:
		return c
	case <-time.After(10 * time.Second):
		t.Fatalf("no call reached the UE of network %s", n.name)
	}
	return nil
}

// waitDone waits until a call has ended.
func waitDone(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

// waitReleased waits until the TrGWs of both networks have released
// every context, and reports how many each one reserved.
func (p *pair) waitReleased(t *testing.T) (reservedA, reservedB int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		openA, ra := p.a.trgw.open()
		openB, rb := p.b.trgw.open()
		if len(openA) == 0 && len(openB) == 0 {
			return ra, rb
		}
		if time.Now().After(deadline) {
			t.Fatalf("TrGW contexts left open: network A %d, network B %d", len(openA), len(openB))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// internals are the strings that name the inside of a network: its
// internal addresses, the names of its nodes, and its UE's private
// identity in the contact.
func internals(p plan) []string {
	s := []string{p.internal + ".", "scscf." + p.domain, "icscf." + p.domain, "ibcf." + p.domain, p.imsi}
	for i := range p.nni {
		in, _ := p.instanceHosts(i)
		s = append(s, in)
	}
	return s
}

// assertHidden checks that no message that network n received names
// the inside of the other network, and that none came from there.
func assertHidden(t *testing.T, n *network, other plan) {
	t.Helper()
	inside := netip.MustParsePrefix(other.internal + ".0/24")
	got := n.rec.received()
	if len(got) == 0 {
		t.Fatalf("network %s received nothing", n.name)
	}
	for _, c := range got {
		if inside.Contains(c.Source.Addr()) {
			t.Errorf("%s of network %s received a message from %s, inside network %s", c.node, n.name, c.Source, other.name)
		}
		for _, s := range internals(other) {
			if bytes.Contains(c.Wire, []byte(s)) {
				t.Errorf("%s of network %s received %q of network %s:\n%s", c.node, n.name, s, other.name, c.Wire)
			}
		}
	}
}

// dumpCaptures writes what network n received to the directory that
// IBCF_CAPTURE_DIR names, one file per message, for text2pcap and
// tshark.
func dumpCaptures(t *testing.T, n *network) {
	dir := os.Getenv("IBCF_CAPTURE_DIR")
	if dir == "" {
		return
	}
	for i, c := range n.rec.received() {
		name := fmt.Sprintf("%s_%03d_%s_%s_%s_%s.sip", n.name, i, c.node, c.Transport,
			strings.ReplaceAll(c.Source.String(), ":", "_"), strings.ReplaceAll(c.Destination.String(), ":", "_"))
		if err := os.WriteFile(filepath.Join(dir, name), c.Wire, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
