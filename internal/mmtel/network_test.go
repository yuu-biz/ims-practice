package mmtel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/ue"
	"github.com/yuu-biz/ims-practice/internal/xcap"
)

// This file runs calls through the whole IMS in one process: the HSS
// with the subscriptions, the default service documents and the filter
// criteria of deploy/compose/hss.yaml, the P-, I- and S-CSCF, and the
// MMTel AS set up as deploy/compose/mmtel.yaml says, with its XCAP
// server, all on loopback. The UEs register with the UE simulator and
// call on their own UAs.

var shTimers = diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}

// names stands in for DNS and ENUM.
type names struct {
	mu    sync.Mutex
	hosts map[string]netip.AddrPort
}

func (n *names) add(host string, addr netip.AddrPort) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.hosts[host] = addr
}

func (n *names) Locate(_ context.Context, host string, _ int, _ sip.Transport) ([]sip.Destination, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if a, ok := n.hosts[host]; ok {
		return []sip.Destination{{Transport: sip.UDP, Addr: a}}, nil
	}
	return nil, errors.New("no such host: " + host)
}

// ENUM turns +1 202 555 010x into the SIP URI of subscriber x, as the
// e164.arpa zone of deploy/compose does.
func (n *names) ENUM(_ context.Context, number, _ string) (string, error) {
	number = strings.NewReplacer("-", "", ".", "").Replace(number)
	if user, ok := strings.CutPrefix(number, "+120255501"); ok && len(user) == 2 {
		return "sip:0010100000000" + user + "@" + domain, nil
	}
	return "", sipdns.ErrNoENUMRecord
}

type network struct {
	t     *testing.T
	pcscf netip.AddrPort
	users *sh.Client
	ut    *httptest.Server
	cfg   config.Config
	// mrfp is the MRFP, when the network has the MRF, and mp the
	// requests that the MRFC sent to its control API.
	mrfp *agw.Relay
	mp   *mpRequests
	// mrfpPorts are the first and the last RTP port of the MRFP.
	mrfpPorts [2]uint16
	// provisioning is the provisioning API of the HSS.
	provisioning http.Handler
	// wire is every SIP message that a node or a UE received.
	wire *wire
}

// netOptions are the choices of startNetworkWith.
type netOptions struct {
	// afterFunc, when not nil, runs the no reply timers of the AS.
	afterFunc func(time.Duration, func()) func() bool
	// mrf starts an MRFC and an MRFP, which the AS uses; announcement is
	// the one the AS plays before it rejects a call.
	mrf          bool
	announcement string
}

// startNetwork starts the IMS without the MRF. afterFunc, when not nil,
// runs the no reply timers of the AS.
func startNetwork(t *testing.T, afterFunc func(time.Duration, func()) func() bool) *network {
	return startNetworkWith(t, netOptions{afterFunc: afterFunc})
}

// mrfAnnouncements are the announcements of deploy/announcements, which
// the MRFP of the network plays.
var mrfAnnouncements = map[string]string{
	"notice": "../../deploy/announcements/notice.wav",
	"cat1":   "../../deploy/announcements/cat1.wav",
	"cat2":   "../../deploy/announcements/cat2.wav",
}

// startMRF starts an MRFP, behind its HTTP API, and an MRFC that drives
// it, as host in dns, and returns the MRFP, the requests to its API and
// its range of RTP ports (rtptest.PortRange).
func startMRF(t *testing.T, dns *names, host string) (*agw.Relay, *mpRequests, [2]uint16) {
	t.Helper()
	clips, err := agw.LoadAnnouncements(mrfAnnouncements)
	if err != nil {
		t.Fatal(err)
	}
	minPort, maxPort := rtptest.PortRange(t, 40)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, clips, quiet())
	if err != nil {
		t.Fatal(err)
	}
	// A dialog that is still up when the test ends leaves its
	// termination behind, holding RTP ports of the range, once the API
	// that the MRFC would subtract it through has closed. Let go of them,
	// so that the next run of the test finds the range free.
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
	})
	mp := &mpRequests{}
	control := agw.Handler(relay)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mp.add(r.Method + " " + r.URL.Path + " " + string(body))
		r.Body = io.NopCloser(bytes.NewReader(body))
		control.ServeHTTP(w, r)
	}))
	t.Cleanup(api.Close)
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	notify := http.NewServeMux()
	notifyServer := httptest.NewServer(notify)
	t.Cleanup(notifyServer.Close)
	m, err := mrfc.New(mrfc.Config{Host: host, Port: int(tr.LocalAddr().Port()), NotifyURL: notifyServer.URL + "/notify"},
		[]mrfc.Processor{{Name: "mrfp1", Control: agw.NewClient(api.URL)}}, mrfc.NewMemoryStore(), dns, quiet())
	if err != nil {
		t.Fatal(err)
	}
	notify.Handle("/notify", m.NotifyHandler())
	if err := m.Listen(tr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	dns.add(host, tr.LocalAddr())
	return relay, mp, [2]uint16{minPort, maxPort}
}

// mpRequests are the requests of the control API of the MRFP, each as
// its method, path and body.
type mpRequests struct {
	mu   sync.Mutex
	list []string
}

func (m *mpRequests) add(r string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.list = append(m.list, r)
}

func (m *mpRequests) all() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.list)
}

// startNetworkWith starts the IMS.
func startNetworkWith(t *testing.T, o netOptions) *network {
	t.Helper()
	afterFunc := o.afterFunc
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	hssCfg, err := config.Load("../../deploy/compose/hss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	asCfg, err := config.Load("../../deploy/compose/mmtel.yaml")
	if err != nil {
		t.Fatal(err)
	}
	dns := &names{hosts: map[string]netip.AddrPort{}}
	w := &wire{}
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, shTimers, quiet())
	store, err := hss.NewMemoryStore(hssCfg.HSS.Subscriptions...)
	if err != nil {
		t.Fatal(err)
	}
	hssRole := hss.New(*hssCfg.HSS, store, hssNode, quiet())
	hssNode.Handle(hssRole.Handle)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	connect := func(host string, app uint32, handle diameter.Handler) (*diameter.Node, *diameter.Peer) {
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{app}, shTimers, quiet())
		node.Handle(handle)
		peer := node.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: "hss." + domain})
		t.Cleanup(peer.Stop)
		return node, peer
	}
	stackWith := func(node string, cfg sip.Config) *sip.Stack {
		tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), w.observer(node))
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
	stackOf := func(node string, onRequest func(*sip.ServerTransaction), onACK func(*sip.Message)) *sip.Stack {
		return stackWith(node, sip.Config{OnRequest: onRequest, OnACK: onACK})
	}

	var scscfRole atomic.Pointer[scscf.SCSCF]
	scscfNode, scscfPeer := connect("scscf."+domain, diameter.ApplicationIDCx, func(m *diam.Message) *diam.Message { return scscfRole.Load().Handle(m) })
	s := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", Host: "scscf." + domain, Port: 5060,
		HomeDomain: domain, MinExpires: time.Second, MaxExpires: time.Hour}, scscfPeer, scscfNode, scscf.NewMemoryStore(), dns, quiet())
	scscfRole.Store(s)
	scscfStack := stackOf("S-CSCF", s.OnRequest, s.OnACK)
	s.SetStack(scscfStack)
	dns.add("scscf."+domain, scscfStack.LocalAddr())

	icscfNode, icscfPeer := connect("icscf."+domain, diameter.ApplicationIDCx, nil)
	i := icscf.New(icscf.Config{HomeDomain: domain, SCSCFs: []icscf.SCSCF{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}},
		Host: "icscf." + domain, Port: 5060}, icscfPeer, icscfNode, dns, quiet())
	dns.add(domain, stackOf("I-CSCF", i.OnRequest, nil).LocalAddr())

	p := pcscf.New(pcscf.Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, dns, pcscf.NewMemoryStore(), nil, quiet())
	pcscfStack := stackWith("P-CSCF", sip.Config{Screen: p.Screen, OnRequest: p.OnRequest, OnACK: p.OnACK})
	p.SetStack(pcscfStack)
	dns.add("pcscf."+domain, pcscfStack.LocalAddr())

	var mrfp *agw.Relay
	var mp *mpRequests
	var mrfpPorts [2]uint16
	var mrfcURI string
	if o.mrf {
		mrfp, mp, mrfpPorts = startMRF(t, dns, "mrfc."+domain)
		mrfcURI = "sip:mrfc." + domain
	}

	asNode, asPeer := connect("mmtel."+domain, diameter.ApplicationIDSh, nil)
	users := sh.NewClient(asNode, asPeer, domain)
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), w.observer("MMTel AS"))
	if err != nil {
		t.Fatal(err)
	}
	asStore := mmtel.NewMemoryStore()
	as := mmtel.New(mmtel.Config{Host: asCfg.MMTel.SIP.Host, Port: int(tr.LocalAddr().Port()), HomeDomain: domain,
		HomeCountryCode: asCfg.MMTel.HomeCountryCode, MRFC: mrfcURI, Announcement: o.announcement, AfterFunc: afterFunc,
		ConferenceDomain: asCfg.MMTel.ConferenceDomain},
		users, asStore, asStore, dns, quiet())
	if err := as.Listen(tr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(as.Close)
	dns.add(asCfg.MMTel.SIP.Host, tr.LocalAddr())
	// The subdomain of the conference URIs leads to the AS, as its NAPTR
	// and SRV records do in deploy/compose/dns.
	dns.add(asCfg.MMTel.ConferenceDomain, tr.LocalAddr())
	ut := httptest.NewServer(xcap.NewHandler(users, asCfg.MMTel.XCAP.RootPath, asCfg.MMTel.XCAP.NonceKey, quiet()))
	t.Cleanup(ut.Close)

	// The Diameter connections open in the background.
	deadline := time.Now().Add(5 * time.Second)
	for _, peer := range []*diameter.Peer{scscfPeer, icscfPeer} {
		for {
			_, err := peer.Request(context.Background(), cx.UserAuthorizationRequest{
				Header: cx.NewHeader(icscfNode, "", domain), UserName: "001010000000001@" + domain,
				PublicIdentity: "sip:001010000000001@" + domain, VisitedNetworkIdentifier: domain}.Message())
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("Cx never opened: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for {
		_, _, err := users.RepositoryData(context.Background(), "sip:001010000000001@"+domain, simservs.ServiceIndication)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Sh never opened: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return &network{t: t, pcscf: pcscfStack.LocalAddr(), users: users, ut: ut, cfg: hssCfg, mrfp: mrfp, mp: mp, mrfpPorts: mrfpPorts,
		provisioning: hssRole.Provisioning(nil), wire: w}
}

// subscriber is a UE of the network: the UE simulator that registers,
// its UA for calls, and its client of Ut.
type subscriber struct {
	n        *network
	number   int
	impu     string
	tel      string
	ue       *ue.UE
	ua       *sip.UA
	media    *ue.Media
	reg      ue.Registration
	calls    chan *sip.IncomingCall
	services *ue.SupplementaryServices
}

// subscriber is subscriber number k of hss.yaml, which registers when
// register is true.
func (n *network) subscriber(k int, register bool) *subscriber {
	n.t.Helper()
	id := "00101000000000" + string(rune('0'+k))
	s := &subscriber{n: n, number: k, impu: "sip:" + id + "@" + domain, tel: "tel:+1202555010" + string(rune('0'+k)),
		calls: make(chan *sip.IncomingCall, 2)}
	password := "test-password-" + string(rune('0'+k))
	s.services = ue.NewSupplementaryServices(&xcap.Client{Root: n.ut.URL, XUI: s.impu, Username: id + "@" + domain, Password: password, UserAgent: "test"})
	if !register {
		return s
	}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), n.wire.observer("UE "+string(rune('0'+k))))
	if err != nil {
		n.t.Fatal(err)
	}
	addr := tr.LocalAddr()
	contact := ue.NewContact(s.impu, addr)
	// The UA takes the precondition mechanism, as the UE simulator's UA
	// does (ue.UAConfig), so that ue.Call and ue.Answer use it.
	s.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy: addr.String(), Contact: contact.URI(),
		OnCall: func(ic *sip.IncomingCall) { s.calls <- ic },
		Locate: func(sip.URI) (sip.Destination, error) { return sip.Destination{Transport: sip.UDP, Addr: n.pcscf}, nil },
	}, ue.CallConfig{}), quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(s.ua.Close)
	s.ue = ue.New(ue.Config{PrivateIdentity: id + "@" + domain, PublicIdentity: s.impu, Password: password, HomeDomain: domain,
		PCSCF: n.pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060"}, s.ua.Stack(), contact, quiet())
	if s.media, err = ue.ListenMedia("127.0.0.1:0"); err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(func() { s.media.Close() })
	if s.reg, err = s.ue.Register(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	return s
}

// set switches one of the subscriber's services on, with target, over
// Ut.
func (s *subscriber) set(service ue.Service, target string) {
	s.n.t.Helper()
	if _, err := s.services.SetService(context.Background(), service, true, target); err != nil {
		s.n.t.Fatalf("%s over Ut: %v", service, err)
	}
}

// invite calls target from the subscriber's UA along its registration,
// with headers, and returns the call.
func (s *subscriber) invite(target string, headers ...sip.HeaderField) *sip.OutgoingCall {
	s.n.t.Helper()
	offer := sdp.Offer("1", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), s.media.Port()))
	out, err := s.ua.Invite(sip.Invitation{RequestURI: target, From: s.impu, To: target,
		Route:   append([]string{"<sip:pcscf." + domain + ":5060;lr>"}, s.reg.ServiceRoute...),
		Headers: headers, Body: sip.Body{Type: "application/sdp", Content: offer.Bytes()}})
	if err != nil {
		s.n.t.Fatal(err)
	}
	s.n.t.Cleanup(func() { out.Bye() })
	return out
}

// answer has the subscriber's next call ring and be answered.
func (s *subscriber) answer() *sip.IncomingCall {
	s.n.t.Helper()
	ic := receive(s.n.t, s.calls, "call at subscriber "+string(rune('0'+s.number)))
	offer, err := sdp.Parse(ic.Request.Body)
	if err != nil {
		s.n.t.Fatal(err)
	}
	answer, err := sdp.Answer(offer, "2", netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), s.media.Port()))
	if err != nil {
		s.n.t.Fatal(err)
	}
	ic.Progress(180, sip.Body{})
	ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
	return ic
}

// TestPlainCallThroughTheAS: with the default service document, a call
// goes through the MMTel AS on both sides as it would without it, media
// and all.
func TestPlainCallThroughTheAS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	answered := make(chan error, 1)
	go func() {
		ic := <-callee.calls
		if !strings.Contains(ic.Request.Get("Contact"), "mmtel."+domain) {
			t.Errorf("callee's INVITE has Contact %q, want the MMTel AS's", ic.Request.Get("Contact"))
		}
		answered <- callee.ue.Answer(context.Background(), ic, callee.media, ue.Speech{PCM: ue.Tone(880, time.Second)}, ue.AnswerOptions{PickUp: time.After(100 * time.Millisecond)})
	}()
	if err := caller.ue.Call(context.Background(), caller.ua, caller.reg, callee.tel, caller.media, ue.Speech{PCM: ue.Tone(440, 500*time.Millisecond)}, ue.CallOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, answered, "end of the callee's call"); err != nil {
		t.Fatal(err)
	}
	got, packets := callee.media.Received()
	if packets != 25 || !bytes.Equal(got, caller.media.Sent()) {
		t.Errorf("callee got %d packets, %d bytes; want the 25 packets the caller sent", packets, len(got))
	}
}

// TestWithheldIdentityThroughTheIMS: a caller who withholds its
// identity for one call (TS 24.607 4.5.2.1). The callee sees an
// anonymous From, Privacy: id, and no P-Asserted-Identity, which the
// callee's S-CSCF keeps in the trust domain.
func TestWithheldIdentityThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	caller.invite(callee.tel, sip.HeaderField{Name: "Privacy", Value: "id"})
	ic := receive(t, callee.calls, "call at the callee")
	from, _ := sip.ParseNameAddr(ic.Request.Get("From"))
	if from.URI != "sip:anonymous@anonymous.invalid" || ic.Request.Get("Privacy") != "id" || ic.Request.Get("P-Asserted-Identity") != "" {
		t.Errorf("callee got From %q, Privacy %q, P-Asserted-Identity %q; want anonymous, id and none",
			ic.Request.Get("From"), ic.Request.Get("Privacy"), ic.Request.Get("P-Asserted-Identity"))
	}
	ic.Reject(486)
}

// TestConnectedIdentityThroughTheIMS: the caller learns who answered,
// as the callee's P-CSCF asserts it (TIP; TS 24.229 5.2.6.4.4 step 1).
// Once the callee restricts its identity by default with TIR, the
// callee's AS puts "id" in Privacy (TS 24.608 4.5.2.9), and the
// caller's P-CSCF keeps the identity in the trust domain (RFC 3325 7).
func TestConnectedIdentityThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	connected := func() *sip.Message {
		t.Helper()
		out := caller.invite(callee.tel)
		callee.answer()
		resps := readAll(t, out)
		if ok := resps[len(resps)-1]; ok.StatusCode == 200 {
			return ok
		}
		t.Fatalf("caller got %v, want the 200", statuses(resps))
		return nil
	}
	if ok := connected(); ok.Get("P-Asserted-Identity") != "<"+callee.impu+">" || ok.Get("Privacy") != "" {
		t.Errorf("caller got P-Asserted-Identity %q, Privacy %q; want <%s> and none", ok.Get("P-Asserted-Identity"), ok.Get("Privacy"), callee.impu)
	}

	ut := &xcap.Client{Root: n.ut.URL, XUI: callee.impu, Username: "001010000000002@" + domain, Password: "test-password-2", UserAgent: "test"}
	tir := []byte(`<default-behaviour xmlns="` + simservs.NamespaceSimservs + `">` + string(simservs.PresentationRestricted) + `</default-behaviour>`)
	if _, err := ut.Put(context.Background(), xcap.Selector{Path: "simservs/terminating-identity-presentation-restriction/default-behaviour"}, tir, ""); err != nil {
		t.Fatalf("TIR over Ut: %v", err)
	}
	if ok := connected(); ok.Get("P-Asserted-Identity") != "" || ok.Get("Privacy") != "id" {
		t.Errorf("with TIR the caller got P-Asserted-Identity %q, Privacy %q; want none and id", ok.Get("P-Asserted-Identity"), ok.Get("Privacy"))
	}
}

func TestBarringThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	callee.set(ue.BAIC, "")
	resps := readAll(t, caller.invite(callee.tel))
	if got := statuses(resps); !reflect.DeepEqual(got, []int{603}) {
		t.Errorf("caller got %v, want 603 from the callee's AS", got)
	}
	if len(callee.calls) != 0 {
		t.Error("the barred call reached the callee")
	}
}

// diverted checks that the call reached subscriber 3 as a diversion of
// subscriber 2 with cause, and that the caller was told.
func diverted(t *testing.T, resps []*sip.Message, ic *sip.IncomingCall, cause string) {
	t.Helper()
	if !slicesContain(statuses(resps), 181) || statuses(resps)[len(resps)-1] != 200 {
		t.Errorf("caller got %v, want a 181 and the 200", statuses(resps))
	}
	entries, err := sip.ParseHistoryInfo(ic.Request.GetAll("History-Info"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("diverted-to UE got History-Info %q", ic.Request.GetAll("History-Info"))
	}
	if !strings.HasPrefix(entries[0].Target(), "sip:001010000000002@") || !strings.Contains(entries[1].Target(), "+12025550103") ||
		!strings.HasSuffix(entries[1].Target(), ";cause="+cause) {
		t.Errorf("History-Info is %q, want subscriber 2 diverted to subscriber 3 with cause %s", ic.Request.GetAll("History-Info"), cause)
	}
}

func slicesContain(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestCFUSetOverUt is the MMTel AS end to end: the user sets
// CFU over Ut, the XCAP server writes it into the HSS over Sh, and the
// AS reads it there for the next call and diverts it.
func TestCFUSetOverUt(t *testing.T) {
	n := startNetwork(t, nil)
	caller, served, target := n.subscriber(1, true), n.subscriber(2, true), n.subscriber(3, true)
	served.set(ue.CFU, target.tel)
	out := caller.invite(served.tel)
	ic := target.answer()
	diverted(t, readAll(t, out), ic, "302")
	if len(served.calls) != 0 {
		t.Error("the diverted call reached the served user")
	}
}

func TestCFBThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, served, target := n.subscriber(1, true), n.subscriber(2, true), n.subscriber(3, true)
	served.set(ue.CFB, target.tel)
	out := caller.invite(served.tel)
	busy := receive(t, served.calls, "call at the served user")
	busy.Reject(486)
	ic := target.answer()
	diverted(t, readAll(t, out), ic, "486")
}

func TestCFNRThroughTheIMS(t *testing.T) {
	// The no reply timer fires at once; the test does not wait the 20 s.
	n := startNetwork(t, func(_ time.Duration, f func()) func() bool { return time.AfterFunc(10*time.Millisecond, f).Stop })
	caller, served, target := n.subscriber(1, true), n.subscriber(2, true), n.subscriber(3, true)
	served.set(ue.CFNR, target.tel)
	out := caller.invite(served.tel)
	ringing := receive(t, served.calls, "call at the served user")
	ringing.Progress(180, sip.Body{})
	ic := target.answer()
	diverted(t, readAll(t, out), ic, "408")
	// The AS cancels with Reason SIP cause 408 (TS 24.604 4.5.2.6.3), and
	// each CSCF on the way copies it into its own CANCEL (RFC 3326 2).
	receive(t, ringing.Done(), "the CANCEL at the served user")
	if !errors.Is(ringing.Err(), sip.ErrCancelled) {
		t.Fatalf("served user's call ended with %v, want the CANCEL", ringing.Err())
	}
	if rs := sip.Reasons(ringing.EndedBy()); len(rs) != 1 || rs[0].Protocol != sip.ReasonSIP || rs[0].Cause != 408 {
		t.Errorf("the CANCEL at the served user has Reason %q, want SIP cause 408", ringing.EndedBy().GetAll("Reason"))
	}
}

func TestCFNRcThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, served, target := n.subscriber(1, true), n.subscriber(2, true), n.subscriber(3, true)
	served.set(ue.CFNRc, target.tel)
	out := caller.invite(served.tel)
	receive(t, served.calls, "call at the served user").Reject(503)
	ic := target.answer()
	diverted(t, readAll(t, out), ic, "503")
}

// TestCFNLThroughTheIMS: the served user is not registered, and its AS
// gets the call by the criteria of the unregistered state.
func TestCFNLThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, served, target := n.subscriber(1, true), n.subscriber(2, false), n.subscriber(3, true)
	served.set(ue.CFNL, target.tel)
	out := caller.invite(served.tel)
	ic := target.answer()
	diverted(t, readAll(t, out), ic, "404")
}

// TestUnregisteredWithoutDiversion: without CFNL the AS sends the call
// on unchanged, and the S-CSCF answers 480.
func TestUnregisteredWithoutDiversion(t *testing.T) {
	n := startNetwork(t, nil)
	caller := n.subscriber(1, true)
	if got := statuses(readAll(t, caller.invite("tel:+12025550102"))); !reflect.DeepEqual(got, []int{480}) {
		t.Errorf("caller got %v, want 480", got)
	}
}

// TestDiversionLoop: subscribers 2 and 3 forward to each other. The
// call goes round until it has had as many diversions as the operator
// allows (5 in hss.yaml), and is then rejected (TS 24.604 4.5.2.6.1).
func TestDiversionLoop(t *testing.T) {
	n := startNetwork(t, nil)
	caller, two, three := n.subscriber(1, true), n.subscriber(2, true), n.subscriber(3, true)
	two.set(ue.CFU, three.tel)
	three.set(ue.CFU, two.tel)
	resps := readAll(t, caller.invite(two.tel))
	final := resps[len(resps)-1]
	if final.StatusCode != 480 || !strings.Contains(final.Get("Warning"), "Too many diversions") {
		t.Errorf("caller got %v with Warning %q, want 480 with the Warning of the diversion limit", statuses(resps), final.Get("Warning"))
	}
	if len(two.calls)+len(three.calls) != 0 {
		t.Error("the looping call reached a UE")
	}
}

// TestHoldThroughTheIMS: the re-INVITE of a HOLD reaches the held UE
// through both ASs, and its answer comes back (TS 24.610 4.5.2.1).
func TestHoldThroughTheIMS(t *testing.T) {
	n := startNetwork(t, nil)
	caller, callee := n.subscriber(1, true), n.subscriber(2, true)
	out := caller.invite(callee.tel)
	ic := receive(t, callee.calls, "call at the callee")
	holds := make(chan *sip.Message, 1)
	ic.Handle(func(r *sip.InDialogRequest) {
		holds <- r.Request
		r.Accept(sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=- 2 2 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 4000 RTP/AVP 0\r\na=recvonly\r\n")})
	})
	ic.Accept(sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=- 2 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 4000 RTP/AVP 0\r\n")})
	readAll(t, out)
	hold := "v=0\r\no=- 1 2 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 4002 RTP/AVP 0\r\na=sendonly\r\n"
	resp, err := out.ReInvite(sip.Body{Type: "application/sdp", Content: []byte(hold)})
	if err != nil || resp.StatusCode != 200 || !strings.Contains(string(resp.Body), "a=recvonly") {
		t.Fatalf("re-INVITE got %v, %v; want the held side's 200 with recvonly", resp, err)
	}
	if r := receive(t, holds, "re-INVITE at the callee"); !strings.Contains(string(r.Body), "a=sendonly") {
		t.Errorf("callee got the re-INVITE body %q, want the sendonly offer", r.Body)
	}
}
