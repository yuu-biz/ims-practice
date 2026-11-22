package ipsmgw_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// This file runs SMS over IP through the whole IMS in one process: the
// HSS with the subscriptions and filter criteria of
// deploy/compose/hss.yaml, the P-, I- and S-CSCF, and IP-SM-GWs set up
// as deploy/compose/ipsmgw.yaml says, all on loopback. The UEs are
// scripted here on the sip stack, as TS 24.341 5.3.1 and 5.3.2 have an
// SM-over-IP sender and receiver behave.

var cxTimers = diameter.Timers{Watchdog: time.Minute, Reconnect: time.Second, Answer: 2 * time.Second}

// names stands in for DNS. A test may point a name somewhere else while
// the nodes run, as DNS would spread requests over several IP-SM-GWs.
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

func (n *names) ENUM(context.Context, string, string) (string, error) {
	return "", sipdns.ErrNoENUMRecord
}

// heard is an outcome that the SC stub of one IP-SM-GW heard.
type heard struct {
	ipsmgw  int
	outcome ipsmgw.Outcome
}

// watchedSC tells the test which IP-SM-GW reported each outcome.
type watchedSC struct {
	*sc.ServiceCentre
	ipsmgw int
	heard  chan heard
}

func (w watchedSC) Report(ctx context.Context, reference string, outcome ipsmgw.Outcome) {
	w.heard <- heard{w.ipsmgw, outcome}
	w.ServiceCentre.Report(ctx, reference, outcome)
}

type network struct {
	t        *testing.T
	dns      *names
	hssStore *hss.MemoryStore
	pcscf    netip.AddrPort
	ipsmgws  []netip.AddrPort
	heard    chan heard
	gw       *config.IPSMGW
}

func load(t *testing.T, name string) config.Config {
	t.Helper()
	cfg, err := config.Load("../../deploy/compose/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// startIMS starts the network with n IP-SM-GWs, which share one Store
// for the IP-SM-GW and one for the SC. DNS leads to the first.
func startIMS(t *testing.T, n int) *network {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	hssCfg, gwCfg := load(t, "hss.yaml"), load(t, "ipsmgw.yaml")
	net := &network{t: t, dns: &names{hosts: map[string]netip.AddrPort{}}, heard: make(chan heard, 20), gw: gwCfg.IPSMGW}

	hssID := diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}
	hssNode := diameter.NewNode(hssID, []uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, cxTimers, quiet())
	store, err := hss.NewMemoryStore(hssCfg.HSS.Subscriptions...)
	if err != nil {
		t.Fatal(err)
	}
	net.hssStore = store
	hssRole := hss.New(*hssCfg.HSS, store, hssNode, quiet())
	hssNode.Handle(hssRole.Handle)
	server, err := hssNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	connect := func(host string, app uint32, handle diameter.Handler) (*diameter.Node, *diameter.Peer) {
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain}, []uint32{app}, cxTimers, quiet())
		node.Handle(handle)
		peer := node.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: hssID.OriginHost})
		t.Cleanup(peer.Stop)
		return node, peer
	}

	var scscfRole atomic.Pointer[scscf.SCSCF]
	scscfNode, scscfPeer := connect("scscf."+domain, diameter.ApplicationIDCx, func(m *diam.Message) *diam.Message { return scscfRole.Load().Handle(m) })
	s, err := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", Host: "scscf." + domain, Port: 5060,
		HomeDomain: domain, MinExpires: time.Second, MaxExpires: time.Hour}, scscfPeer, scscfNode, scscf.NewMemoryStore(), net.dns, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	scscfRole.Store(s)
	scscfStack := newStack(t, sip.Timers{}, s.OnRequest)
	s.SetStack(scscfStack)
	net.dns.add("scscf."+domain, scscfStack.LocalAddr())

	icscfNode, icscfPeer := connect("icscf."+domain, diameter.ApplicationIDCx, nil)
	i := icscf.New(icscf.Config{HomeDomain: domain, SCSCFs: []icscf.SCSCF{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}},
		Host: "icscf." + domain, Port: 5060}, icscfPeer, icscfNode, net.dns, quiet())
	net.dns.add(domain, newStack(t, sip.Timers{}, i.OnRequest).LocalAddr())

	p := pcscf.New(pcscf.Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, net.dns, pcscf.NewMemoryStore(), nil, quiet())
	t.Cleanup(p.Close)
	pcscfStack := newStackConfig(t, sip.Config{Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest})
	p.SetStack(pcscfStack)
	net.pcscf = pcscfStack.LocalAddr()
	net.dns.add("pcscf."+domain, net.pcscf)

	gwStore, scStore := ipsmgw.NewMemoryStore(), sc.NewMemoryStore()
	var shPeers []*sh.Client
	for k := range n {
		node, peer := connect(fmt.Sprintf("ipsmgw%d.%s", k, domain), diameter.ApplicationIDSh, nil)
		users := sh.NewClient(node, peer, domain)
		shPeers = append(shPeers, users)
		centre := sc.New(users, scStore, quiet())
		g := ipsmgw.New(ipsmgw.Config{URI: gwCfg.IPSMGW.SIP.URI(), HomeDomain: domain, Number: gwCfg.IPSMGW.Number,
			ServiceCentre: gwCfg.IPSMGW.ServiceCentre}, watchedSC{centre, k, net.heard}, gwStore, users, net.dns, quiet())
		t.Cleanup(g.Close)
		centre.SetIPSMGW(g)
		stack := newStack(t, sip.Timers{}, g.OnRequest)
		g.SetStack(stack)
		net.ipsmgws = append(net.ipsmgws, stack.LocalAddr())
	}
	net.dns.add(gwCfg.IPSMGW.SIP.Host, net.ipsmgws[0])

	// The Diameter connections open in the background; wait until a
	// request would get through each.
	deadline := time.Now().Add(5 * time.Second)
	ready := func(request func() error) {
		for err := request(); err != nil; err = request() {
			if time.Now().After(deadline) {
				t.Fatalf("Diameter never opened: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for _, peer := range []*diameter.Peer{scscfPeer, icscfPeer} {
		ready(func() error {
			_, err := peer.Request(context.Background(), cx.UserAuthorizationRequest{
				Header: diameter.NewHeader(icscfNode, "", domain), UserName: "001010000000001@" + domain,
				PublicIdentity: alice, VisitedNetworkIdentifier: domain}.Message())
			return err
		})
	}
	for _, c := range shPeers {
		ready(func() error { _, err := c.PublicIdentities(context.Background(), "12025550101"); return err })
	}
	return net
}

// ipsmgwNumber is the IP-SM-GW that the HSS has for the user of a public
// identity (TS 29.328 7.6.16).
func (n *network) ipsmgwNumber(publicID string) string {
	u, ok, err := n.hssStore.SubscriberOf(context.Background(), publicID)
	if err != nil || !ok {
		n.t.Fatalf("no subscriber %s: %v", publicID, err)
	}
	return u.IPSMGWNumber
}

// waitForIPSMGWNumber waits until the PUR of the IP-SM-GW has reached
// the HSS.
func (n *network) waitForIPSMGWNumber(publicID, want string) {
	n.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for n.ipsmgwNumber(publicID) != want {
		if time.Now().After(deadline) {
			n.t.Fatalf("HSS has IP-SM-GW %q for %s, want %q", n.ipsmgwNumber(publicID), publicID, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (n *network) nextHeard() heard {
	n.t.Helper()
	select {
	case h := <-n.heard:
		return h
	case <-time.After(5 * time.Second):
		n.t.Fatal("no SC heard an outcome")
		return heard{}
	}
}

// phone is a scripted UE: an SM-over-IP sender and receiver (TS 24.341
// 5.3.1, 5.3.2). It answers each MESSAGE as answer says and hands it to
// the test.
type phone struct {
	t            *testing.T
	impi, impu   string
	password     string
	pcscf        netip.AddrPort
	stack        *sip.Stack
	callID       string
	cseq         int
	serviceRoute []string
	inbox        chan *sip.Message
	// waiting holds the MESSAGEs that came while the test waited for
	// another one.
	waiting []*sip.Message
	// features are the feature parameters of the registered Contact,
	// the one of TS 24.341 5.3.2.2 unless a test changes them.
	features string
	// contentTypeName is how the phone spells the name of Content-Type
	// in what it sends.
	contentTypeName string
}

// identities are the private identity and the public identity of
// subscriber k of deploy/compose/hss.yaml: the IMSI in the private one,
// the number in the public one.
func identities(k int) (impi, impu string) {
	return fmt.Sprintf("00101000000000%d@%s", k, domain), fmt.Sprintf("sip:+120255501%02d@%s", k, domain)
}

func (n *network) phone(subscriber int, answer int) *phone {
	n.t.Helper()
	impi, impu := identities(subscriber)
	p := &phone{t: n.t, impi: impi, impu: impu,
		password: "test-password-" + strconv.Itoa(subscriber), pcscf: n.pcscf, callID: sip.NewCallID(), inbox: make(chan *sip.Message, 10),
		features: ";+g.3gpp.smsip", contentTypeName: "Content-Type"}
	p.stack = newStack(n.t, sip.Timers{}, func(st *sip.ServerTransaction) {
		if answer != 0 {
			st.Respond(sip.NewResponse(st.Request, answer, sip.NewTag()))
		}
		p.inbox <- st.Request
	})
	return p
}

// register is the REGISTER of TS 24.229 5.1.1.2 with the feature tag of
// TS 24.341 5.3.2.2 in Contact, answering the Digest challenge.
func (p *phone) register(expires int) {
	p.t.Helper()
	creds := sip.Credentials{Username: p.impi, Realm: domain, URI: "sip:" + domain}
	resp := p.sendRegister(expires, creds.String())
	if resp.StatusCode == 401 {
		ch, err := sip.ParseChallenge(resp.Get("WWW-Authenticate"))
		if err != nil {
			p.t.Fatal(err)
		}
		creds.Realm, creds.Nonce, creds.Opaque = ch.Realm, ch.Nonce, ch.Opaque
		creds.Algorithm, creds.QOP, creds.CNonce, creds.NC = "MD5", "auth", rand.Text()[:16], "00000001"
		creds.Response, _ = sip.DigestResponse(sip.HA1(p.impi, ch.Realm, p.password), sip.REGISTER, creds)
		resp = p.sendRegister(expires, creds.String())
	}
	if resp.StatusCode != 200 {
		p.t.Fatalf("REGISTER answered %d", resp.StatusCode)
	}
	p.serviceRoute = resp.GetAll("Service-Route")
}

func (p *phone) sendRegister(expires int, authorization string) *sip.Message {
	p.cseq++
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+p.impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+p.impu+">")
	m.Add("Call-ID", p.callID)
	m.Add("CSeq", strconv.Itoa(p.cseq)+" REGISTER")
	m.Add("Contact", "<sip:"+p.stack.LocalAddr().String()+">"+p.features+";expires="+strconv.Itoa(expires))
	m.Add("Supported", "path")
	m.Add("Authorization", authorization)
	return sendTo(p.t, p.stack, p.pcscf, m)
}

// send sends an RP message in a MESSAGE to the URI to, along the route
// of the registration (TS 24.229 5.1.2A.1.1), and returns the request
// with the final response to it.
func (p *phone) send(to string, body []byte, inReplyTo string) (*sip.Message, *sip.Message) {
	p.t.Helper()
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: to}
	m.Add("Max-Forwards", "70")
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	for _, r := range p.serviceRoute {
		m.Add("Route", r)
	}
	m.Add("From", "<"+p.impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+to+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	if inReplyTo != "" {
		m.Add("In-Reply-To", inReplyTo)
	}
	m.Add(p.contentTypeName, ipsmgw.ContentType)
	m.Body = body
	return m, sendTo(p.t, p.stack, p.pcscf, m)
}

// submit sends a short message to the SC (TS 24.341 5.3.1.2), whose PSI
// is the tel URI of the SC address.
func (p *phone) submit(sc sms.Address, mr uint8, s sms.Submit) *sip.Message {
	p.t.Helper()
	mo, resp := p.send("tel:"+sc.String(), marshal(p.t, sms.RPDataMSToNetwork{MessageReference: mr, DestinationAddress: sc, UserData: marshal(p.t, s)}), "")
	if resp.StatusCode != 202 {
		p.t.Fatalf("short message answered %d, want 202", resp.StatusCode)
	}
	return mo
}

// next is the first MESSAGE for which want holds, of those that came
// while the test waited for another and those still to come. SIP puts
// no order on separate MESSAGE transactions (RFC 3428 7): the SC starts
// a delivery before the IP-SM-GW sends the submit report, so the status
// report of a short message may reach its sender first.
func (p *phone) next(what string, want func(*sip.Message) bool) *sip.Message {
	p.t.Helper()
	if i := slices.IndexFunc(p.waiting, want); i >= 0 {
		m := p.waiting[i]
		p.waiting = slices.Delete(p.waiting, i, i+1)
		return m
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case m := <-p.inbox:
			if want(m) {
				return m
			}
			p.waiting = append(p.waiting, m)
		case <-timeout:
			p.t.Fatalf("%s got no %s; other MESSAGEs: %d", p.impu, what, len(p.waiting))
			return nil
		}
	}
}

// submitReport is the submit report of mo (TS 24.341 5.3.1.2,
// 5.3.3.4.3): the MESSAGE in reply to it, forked to every contact.
func (p *phone) submitReport(mo *sip.Message) sms.RPMessage {
	p.t.Helper()
	m := p.next("submit report of "+mo.Get("Call-ID"), func(m *sip.Message) bool { return m.Get("In-Reply-To") == mo.Get("Call-ID") })
	if m.Get("Request-Disposition") != "fork" {
		p.t.Fatalf("submit report has Request-Disposition %q, want fork", m.Get("Request-Disposition"))
	}
	return parseRP(p.t, m)
}

// delivery is the next MESSAGE that carries RP-DATA from the SC, a
// short message or a status report (TS 24.341 5.3.2.3, 5.3.1.3): one
// that answers nothing the UE sent.
func (p *phone) delivery(scAddr sms.Address) (*sip.Message, sms.RPDataNetworkToMS, sms.TPDU) {
	p.t.Helper()
	m := p.next("short message", func(m *sip.Message) bool { return m.Get("In-Reply-To") == "" })
	checkHeaders(p.t, m, map[string]string{
		"P-Asserted-Identity": "<" + ownURI + ">",
		"Accept-Contact":      "*;+g.3gpp.smsip;require;explicit",
		"Request-Disposition": "no-fork",
		"Content-Type":        ipsmgw.ContentType,
	})
	data, ok := parseRP(p.t, m).(sms.RPDataNetworkToMS)
	if !ok || data.OriginatorAddress != scAddr {
		p.t.Fatalf("MESSAGE carries %+v, want RP-DATA from %v", parseRP(p.t, m), scAddr)
	}
	tpdu, err := sms.ParseTPDU(data)
	if err != nil {
		p.t.Fatal(err)
	}
	return m, data, tpdu
}

// report sends the delivery report of TS 24.341 5.3.2.4 for mt, to the
// IP-SM-GW in its P-Asserted-Identity, and checks the 202.
func (p *phone) report(mt *sip.Message, rp sms.RPMessage) {
	p.t.Helper()
	pai, err := sip.ParseNameAddr(mt.Get("P-Asserted-Identity"))
	if err != nil {
		p.t.Fatal(err)
	}
	if _, resp := p.send(pai.URI, marshal(p.t, rp), mt.Get("Call-ID")); resp.StatusCode != 202 {
		p.t.Fatalf("delivery report answered %d, want 202", resp.StatusCode)
	}
}

func ack(t *testing.T, mr uint8) sms.RPAck {
	return sms.RPAck{Direction: sms.MSToNetwork, MessageReference: mr, UserData: marshal(t, sms.DeliverReport{})}
}

func textSubmit(t *testing.T, mr uint8, to, text string, statusReport bool) sms.Submit {
	t.Helper()
	dcs, ud, err := sms.EncodeText(text)
	if err != nil {
		t.Fatal(err)
	}
	return sms.Submit{StatusReportRequest: statusReport, MessageReference: mr, DestinationAddress: sms.InternationalNumber(to),
		DataCodingScheme: dcs, UserData: ud}
}

// TestShortMessageThroughTheIMS is the whole of TS 24.341 B.3 to B.6:
// registration with the IP-SM-GW, a short message from Alice to Bob with
// its submit report, delivery and delivery report, the status report
// that Alice asked for, and deregistration.
func TestShortMessageThroughTheIMS(t *testing.T) {
	net := startIMS(t, 1)
	sc := net.gw.ServiceCentre
	alicePhone, bobPhone := net.phone(1, 200), net.phone(2, 200)
	alicePhone.register(3600)
	bobPhone.register(3600)
	net.waitForIPSMGWNumber(aliceTel, net.gw.Number)
	net.waitForIPSMGWNumber(bobTel, net.gw.Number)

	mo := alicePhone.submit(sc, 1, textSubmit(t, 42, "+12025550102", "Hello, Bob!", true))
	ack1, ok := alicePhone.submitReport(mo).(sms.RPAck)
	if !ok || ack1.Direction != sms.NetworkToMS || ack1.MessageReference != 1 {
		t.Fatalf("submit report is not an RP-ACK for RP-MR 1: %+v", ack1)
	}

	mt, data, tpdu := bobPhone.delivery(sc)
	deliver, ok := tpdu.(sms.Deliver)
	if !ok || deliver.OriginatingAddress != sms.InternationalNumber("+12025550101") || !deliver.StatusReportIndication {
		t.Fatalf("Bob got %+v, want an SMS-DELIVER from Alice with TP-SRI", tpdu)
	}
	if text, err := sms.DecodeText(deliver.DataCodingScheme, deliver.UserData); err != nil || text != "Hello, Bob!" {
		t.Errorf("text = %q, %v", text, err)
	}
	bobPhone.report(mt, ack(t, data.MessageReference))
	if got := net.nextHeard(); got.outcome != ipsmgw.Delivered {
		t.Errorf("SC heard %v", got.outcome)
	}

	sr, srData, tpdu := alicePhone.delivery(sc)
	status, ok := tpdu.(sms.StatusReport)
	if !ok || status.MessageReference != 42 || status.RecipientAddress != sms.InternationalNumber("+12025550102") || status.Status != sms.StatusReceivedBySME {
		t.Fatalf("Alice got %+v, want the status report of TP-MR 42", tpdu)
	}
	alicePhone.report(sr, ack(t, srData.MessageReference))
	if got := net.nextHeard(); got.outcome != ipsmgw.Delivered {
		t.Errorf("status report: SC heard %v", got.outcome)
	}

	alicePhone.register(0)
	net.waitForIPSMGWNumber(aliceTel, "")
}

// TestRegistrationWithoutTheFeatureTag is TS 24.341 5.3.3.2 through the
// whole IMS: the IP-SM-GW learns from the NOTIFYs of its subscription,
// not from the third-party REGISTER, whether a UE takes short messages.
// Alice first registers without "+g.3gpp.smsip", and the HSS gets no
// IP-SM-GW for her; when she registers again with it, the IP-SM-GW
// refreshes its subscription at her S-CSCF, and the HSS gets it.
func TestRegistrationWithoutTheFeatureTag(t *testing.T) {
	net := startIMS(t, 1)
	alicePhone := net.phone(1, 200)
	alicePhone.features = ""
	alicePhone.register(3600)
	// Long enough for the SUBSCRIBE, the NOTIFY and a PUR, had there
	// been one; the in-process network answers within milliseconds.
	time.Sleep(300 * time.Millisecond)
	if got := net.ipsmgwNumber(aliceTel); got != "" {
		t.Fatalf("HSS has IP-SM-GW %q for a UE without the feature tag", got)
	}
	alicePhone.features = ";+g.3gpp.smsip"
	alicePhone.register(3600)
	net.waitForIPSMGWNumber(aliceTel, net.gw.Number)
}

// TestJapaneseTextInUCS2 sends a text that the GSM 7 bit alphabet does
// not have (TS 23.038 4), without a status report: the next MESSAGE
// Alice gets is the submit report of her next short message.
func TestJapaneseTextInUCS2(t *testing.T) {
	net := startIMS(t, 1)
	sc := net.gw.ServiceCentre
	alicePhone, bobPhone := net.phone(1, 200), net.phone(2, 200)
	alicePhone.register(3600)
	bobPhone.register(3600)

	alicePhone.submitReport(alicePhone.submit(sc, 1, textSubmit(t, 1, "+12025550102", "こんにちは、世界", false)))
	mt, data, tpdu := bobPhone.delivery(sc)
	deliver := tpdu.(sms.Deliver)
	if deliver.DataCodingScheme != sms.DCSUCS2 || deliver.StatusReportIndication {
		t.Errorf("TP-DCS %#x, TP-SRI %v; want UCS2 without a status report", deliver.DataCodingScheme, deliver.StatusReportIndication)
	}
	if text, err := sms.DecodeText(deliver.DataCodingScheme, deliver.UserData); err != nil || text != "こんにちは、世界" {
		t.Errorf("text = %q, %v", text, err)
	}
	bobPhone.report(mt, ack(t, data.MessageReference))
	net.nextHeard()

	mo := alicePhone.submit(sc, 2, textSubmit(t, 2, "+12025550102", "again", false))
	if _, ok := alicePhone.submitReport(mo).(sms.RPAck); !ok {
		t.Error("second short message not accepted")
	}
}

// TestShortMessagesThatFail covers the errors of the IP-SM-GW and the SC
// through the IMS: what the sender gets in the submit report, and in the
// status report when the delivery fails.
func TestShortMessagesThatFail(t *testing.T) {
	net := startIMS(t, 1)
	sc := net.gw.ServiceCentre
	alicePhone, bobPhone := net.phone(1, 200), net.phone(2, 200)
	alicePhone.register(3600)
	bobPhone.register(3600)

	// RP-DATA that does not decode (TS 24.341 5.3.3.4.1 steps b and c):
	// RP-ERROR with cause 96 for its RP-MR.
	mo, resp := alicePhone.send("tel:"+sc.String(), []byte{0x00, 0x09, 0x07}, "")
	if resp.StatusCode != 202 {
		t.Fatalf("undecodable RP-DATA answered %d", resp.StatusCode)
	}
	if got, ok := alicePhone.submitReport(mo).(sms.RPError); !ok || got.MessageReference != 9 || got.Cause != sms.RPCauseInvalidMandatoryInformation {
		t.Errorf("undecodable RP-DATA answered with %+v", got)
	}

	// The memory of the UE is free again: RP-SMMA, answered with RP-ACK.
	mo, _ = alicePhone.send("tel:"+sc.String(), marshal(t, sms.RPSMMA{MessageReference: 2}), "")
	if got := alicePhone.submitReport(mo); !reflect.DeepEqual(got, sms.RPAck{Direction: sms.NetworkToMS, MessageReference: 2}) {
		t.Errorf("RP-SMMA answered with %+v, want RP-ACK", got)
	}

	// No user has the number: RP-Cause 1 with TP-FCS C3.
	mo = alicePhone.submit(sc, 3, textSubmit(t, 3, "+12025550188", "anyone?", true))
	refused, ok := alicePhone.submitReport(mo).(sms.RPError)
	report, err := sms.ParseTPDU(refused)
	if !ok || refused.Cause != sms.RPCauseUnassignedNumber || err != nil || report.(sms.SubmitReport).FailureCause != sms.FCSInvalidSMEAddress {
		t.Errorf("unknown number: %+v, %+v, %v", refused, report, err)
	}

	// Carol is a subscriber who has not registered: the I-CSCF answers
	// 480, which is Absent Subscriber SM, and the status report says the
	// SC has given up.
	mo = alicePhone.submit(sc, 4, textSubmit(t, 4, "+12025550103", "Carol?", true))
	if _, ok := alicePhone.submitReport(mo).(sms.RPAck); !ok {
		t.Fatal("short message for Carol not accepted")
	}
	if got := net.nextHeard(); got.outcome != ipsmgw.AbsentSubscriber {
		t.Errorf("Carol: SC heard %v, want absent subscriber", got.outcome)
	}
	sr, srData, tpdu := alicePhone.delivery(sc)
	if s := tpdu.(sms.StatusReport); s.MessageReference != 4 || s.Status != sms.StatusNoResponseFromSME {
		t.Errorf("status report for Carol: %+v", s)
	}
	alicePhone.report(sr, ack(t, srData.MessageReference))
	net.nextHeard()

	// Bob's memory is full: he answers with RP-ERROR, cause 22.
	alicePhone.submitReport(alicePhone.submit(sc, 5, textSubmit(t, 5, "+12025550102", "full?", true)))
	mt, data, _ := bobPhone.delivery(sc)
	bobPhone.report(mt, sms.RPError{Direction: sms.MSToNetwork, MessageReference: data.MessageReference, Cause: sms.RPCauseMemoryCapacityExceeded,
		UserData: marshal(t, sms.DeliverReport{FailureCause: sms.FCSMemoryCapacityExceeded})})
	if got := net.nextHeard(); got.outcome != ipsmgw.MemoryCapacityExceeded {
		t.Errorf("memory full: SC heard %v", got.outcome)
	}
	if _, _, tpdu := alicePhone.delivery(sc); tpdu.(sms.StatusReport).Status != sms.StatusNoResponseFromSME {
		t.Errorf("status report for a full memory: %+v", tpdu)
	}
}

// TestHeaderNameInLowerCase is a UE that spells Content-Type in lower
// case, which is the same header (RFC 3261 7.3.1): the originating
// filter criterion still sends its MESSAGEs to the IP-SM-GW (TS 29.228
// Annex F). A short message to the SC gets through. A MESSAGE to Bob
// that carries RP-DATA as the network sends it, with an SMS-DELIVER
// that names someone else as the sender, goes to the IP-SM-GW too,
// which refuses it, so Bob never sees it.
func TestHeaderNameInLowerCase(t *testing.T) {
	net := startIMS(t, 1)
	sc := net.gw.ServiceCentre
	alicePhone, bobPhone := net.phone(1, 200), net.phone(2, 200)
	alicePhone.contentTypeName = "content-type"
	alicePhone.register(3600)
	bobPhone.register(3600)

	mo := alicePhone.submit(sc, 1, textSubmit(t, 1, "+12025550102", "lower case", false))
	if _, ok := alicePhone.submitReport(mo).(sms.RPAck); !ok {
		t.Fatal("short message not accepted")
	}
	mt, data, _ := bobPhone.delivery(sc)
	bobPhone.report(mt, ack(t, data.MessageReference))
	net.nextHeard()

	dcs, ud, err := sms.EncodeText("from someone else")
	if err != nil {
		t.Fatal(err)
	}
	forged := marshal(t, sms.Deliver{OriginatingAddress: sms.InternationalNumber("+12025550199"), DataCodingScheme: dcs,
		ServiceCentreTimeStamp: time.Now(), UserData: ud})
	mo, resp := alicePhone.send(bobTel, marshal(t, sms.RPDataNetworkToMS{MessageReference: 2, OriginatorAddress: sc, UserData: forged}), "")
	if resp.StatusCode != 202 {
		t.Fatalf("forged short message answered %d, want 202 from the IP-SM-GW", resp.StatusCode)
	}
	if got, ok := alicePhone.submitReport(mo).(sms.RPError); !ok || got.Cause != sms.RPCauseMessageNotCompatibleWithState {
		t.Errorf("forged short message answered with %+v, want RP-ERROR with cause 98", got)
	}
	select {
	case m := <-bobPhone.inbox:
		t.Errorf("Bob got a MESSAGE from %s", m.Get("P-Asserted-Identity"))
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDeliveryReportThroughAnotherIPSMGW, through the IMS: the
// IP-SM-GW that sent the short message is not the one that DNS gives
// the S-CSCF for the delivery report, and the other one finishes the
// delivery and sends the status report.
func TestDeliveryReportThroughAnotherIPSMGW(t *testing.T) {
	net := startIMS(t, 2)
	sc := net.gw.ServiceCentre
	alicePhone, bobPhone := net.phone(1, 200), net.phone(2, 200)
	alicePhone.register(3600)
	bobPhone.register(3600)

	alicePhone.submitReport(alicePhone.submit(sc, 1, textSubmit(t, 7, "+12025550102", "Who answers?", true)))
	mt, data, _ := bobPhone.delivery(sc)
	net.dns.add(net.gw.SIP.Host, net.ipsmgws[1])
	bobPhone.report(mt, ack(t, data.MessageReference))
	if got := net.nextHeard(); got != (heard{1, ipsmgw.Delivered}) {
		t.Errorf("heard %+v, want the second IP-SM-GW to report the delivery", got)
	}
	if _, _, tpdu := alicePhone.delivery(sc); tpdu.(sms.StatusReport).MessageReference != 7 {
		t.Errorf("status report = %+v", tpdu)
	}
}

// simulator is the UE simulator of package ue on a UA of its own, with
// the SC address of its default configuration, +12025550100.
func (n *network) simulator(subscriber int) *ue.UE {
	n.t.Helper()
	impi, impu := identities(subscriber)
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	addr := tr.LocalAddr()
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: "test-password-" + strconv.Itoa(subscriber),
		HomeDomain: domain, PCSCF: n.pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Features: ue.DefaultFeatures,
		SCAddress: n.gw.ServiceCentre.String()}
	contact := ue.NewContact(cfg, addr)
	// The UA needs the UE's ReceiveSMS before the UE exists.
	var u atomic.Pointer[ue.UE]
	ua, err := sip.NewUA(tr, sip.UAConfig{SentBy: addr.String(), Contact: contact.URI(),
		OnCall:    func(c *sip.IncomingCall) { c.Reject(486) },
		OnMessage: func(st *sip.ServerTransaction) { u.Load().ReceiveSMS(st) },
	}, quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(ua.Close)
	u.Store(ue.New(cfg, ua.Stack(), contact, quiet()))
	if _, err := u.Load().Register(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	return u.Load()
}

// TestUESimulatorsThroughTheIPSMGW runs the UE simulator of package ue,
// not the scripted phones, against the IP-SM-GW and the SC stub: the
// two sides are written apart from each other, so this checks that they
// read TS 24.341 the same way.
func TestUESimulatorsThroughTheIPSMGW(t *testing.T) {
	net := startIMS(t, 1)
	a, b := net.simulator(1), net.simulator(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sent, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: "こんにちは、B", StatusReport: true})
	if err != nil {
		t.Fatal(err)
	}
	h, err := b.WaitSMS(ctx, func(h ue.SMSHistory) bool { return len(h.Received) == 1 })
	if err != nil {
		t.Fatalf("UE-B received %+v: %v", h.Received, err)
	}
	if r := h.Received[0]; r.Text != "こんにちは、B" || r.From != "+12025550101" {
		t.Errorf("UE-B received %q from %s", r.Text, r.From)
	}
	if got := net.nextHeard(); got.outcome != ipsmgw.Delivered {
		t.Errorf("SC heard %v, want the delivery report of UE-B", got.outcome)
	}
	h, err = a.WaitSMS(ctx, func(h ue.SMSHistory) bool {
		return len(h.Sent) == 1 && h.Sent[0].State == ue.SubmitAccepted && h.Sent[0].StatusReport != nil
	})
	if err != nil {
		t.Fatalf("UE-A sent %+v: %v", h.Sent, err)
	}
	if s := h.Sent[0]; s.CallID != sent.CallID || s.StatusReport.Status != sms.StatusReceivedBySME {
		t.Errorf("UE-A sent %+v, want it accepted and reported received", s)
	}
	// UE-A acknowledges the status report like any short message.
	if got := net.nextHeard(); got.outcome != ipsmgw.Delivered {
		t.Errorf("status report: SC heard %v", got.outcome)
	}

	// A number that no user has: the SC refuses it in the submit report.
	if _, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550188", Text: "anyone?"}); err != nil {
		t.Fatal(err)
	}
	h, err = a.WaitSMS(ctx, func(h ue.SMSHistory) bool { return len(h.Sent) == 2 && h.Sent[1].State != ue.Submitted })
	if err != nil {
		t.Fatalf("UE-A sent %+v: %v", h.Sent, err)
	}
	if s := h.Sent[1]; s.RPCause != sms.RPCauseUnassignedNumber || s.FailureCause != sms.FCSInvalidSMEAddress {
		t.Errorf("UE-A sent %+v, want RP-Cause 1 with TP-FCS C3", s)
	}
}
