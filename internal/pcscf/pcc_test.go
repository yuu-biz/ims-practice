package pcscf

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file run the P-CSCF as the AF of policy and charging
// control against the real parts: an rx.Client on a Diameter connection
// to the PCRF of the EPC stub, and a UE that attaches to the stub
// over its API. Each test reads like the ladder of the VoLTE guide: the
// SIP that the parties see, the Rx messages of the P-CSCF in order, and
// what the EPC stub tells the UE.

const pccIMSI = "001010000000001"

// The UE of the EPC stub has its own loopback address, as a UE in
// Compose has its own address on the bridge, so that the PCRF can bind
// the P-CSCF's Rx sessions to its PDN connection by Framed-IP-Address.
// The UE that bypasses the EPC has another one, which the stub does not
// know.
var (
	pccUEAddr     = netip.MustParseAddr("127.0.0.2")
	pccBypassAddr = netip.MustParseAddr("127.0.0.3")
)

// pcc is a P-CSCF with policy control over Rx and a media relay, the
// EPC stub with its PCRF, a home network that answers every REGISTER
// with 200, and far, a user agent that stands for the S-CSCF and the
// other end of every call.
type pcc struct {
	t     *testing.T
	p     *PCSCF
	store *MemoryStore
	relay *fakeRelay
	// pcscfAddr is where the parties send everything.
	pcscfAddr netip.AddrPort
	far       *pccParty
	// api is the EPC stub's API.
	api string
	// rx are the Rx messages of the P-CSCF's Diameter node, as it sent
	// or received them.
	rx chan diameter.Capture
}

// pccParty is a user agent and what reaches it: its calls, and every
// request, so that a test can read header fields such as Reason.
type pccParty struct {
	ua       *sip.UA
	contact  string
	calls    chan *sip.IncomingCall
	requests chan *sip.Message
}

// pccUE is a UE that has attached to the EPC stub and listens on its
// event stream.
type pccUE struct {
	*pccParty
	epc    *epc.Client
	events <-chan epc.NetworkMessage
}

var pccTimers = diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}

func newPCC(t *testing.T) *pcc {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	n := &pcc{t: t, store: NewMemoryStore(), relay: newFakeRelay(), rx: make(chan diameter.Capture, 64)}
	pcscfTransport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	n.pcscfAddr = pcscfTransport.LocalAddr()

	pcrfID := diameter.Identity{OriginHost: "pcrf." + domain, OriginRealm: domain}
	pcrfNode := diameter.NewNode(pcrfID, []uint32{diameter.ApplicationIDRx}, pccTimers, quiet())
	stub := epc.New(epc.Config{
		APN:            "ims",
		PCSCFAddresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		Subscribers:    []epc.Subscriber{{IMSI: pccIMSI, Address: pccUEAddr}},
		Cell: epc.SystemInformation{TrackingAreaIdentity: epc.TrackingAreaIdentity{
			PLMN: epc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1}, CellIdentity: 257},
	}, epc.NewMemoryStore(), pcrfNode, quiet())
	pcrfNode.Handle(stub.HandleRx)
	server, err := pcrfNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	stub.SetAFs(server)
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)
	n.api = api.URL

	home := newStack(t, func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
		resp.Add("P-Associated-URI", "<"+impu+">")
		resp.Add("Contact", st.Request.Get("Contact"))
		st.Respond(resp)
	})
	n.far = n.newParty(netip.MustParseAddr("127.0.0.1"), "far")
	locate := locatorFunc(func(host string) []sip.Destination {
		if host == domain {
			return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}}
		}
		return []sip.Destination{{Transport: sip.UDP, Addr: n.far.ua.Stack().LocalAddr()}}
	})
	n.p = New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, n.store, n.relay, quiet())

	// The P-CSCF's Diameter node, as cmd/ims makes it for a pcscf with a
	// PCRF: the client, made after its peer, takes the RARs and ASRs of
	// the PCRF. The node is observed from the start, as the connection's
	// goroutines read the observer; what the wait for the connection sent
	// is dropped.
	afNode := diameter.NewNode(diameter.Identity{OriginHost: "pcscf." + domain, OriginRealm: domain},
		[]uint32{diameter.ApplicationIDRx}, pccTimers, quiet())
	afNode.Observe(func(c diameter.Capture) {
		if c.Message.Header.ApplicationID != diameter.ApplicationIDRx {
			return
		}
		select {
		case n.rx <- c:
		default: // wantRx then reports the message it misses
		}
	})
	var client atomic.Pointer[rx.Client]
	afNode.Handle(func(m *diam.Message) *diam.Message { return client.Load().Handle(m) })
	peer := afNode.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: pcrfID.OriginHost})
	t.Cleanup(peer.Stop)
	client.Store(rx.NewClient(afNode, peer, domain, n.p.PolicyEvent, quiet()))
	n.p.SetPolicyControl(client.Load())
	waitForPeer(t, afNode, peer)
	for len(n.rx) > 0 {
		<-n.rx
	}

	stack, err := sip.NewStack(pcscfTransport, sip.Config{SentBy: n.pcscfAddr.String(), Screen: n.p.Screen, Admit: n.p.AdmitUnprotected, OnRequest: n.p.OnRequest, OnACK: n.p.OnACK}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	n.p.SetStack(stack)
	return n
}

// waitForPeer waits until the Diameter connection to the PCRF is open:
// the P-CSCF asks the PCRF once the UE has registered, and a request
// before that fails.
func waitForPeer(t *testing.T, node *diameter.Node, peer *diameter.Peer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		header := cx.NewHeader(node, "", domain)
		_, err := peer.Request(t.Context(), rx.SessionTerminationRequest{Header: header, TerminationCause: rx.DiameterLogout}.Message())
		if err == nil {
			return
		}
		if !errors.Is(err, diameter.ErrPeerUnavailable) || time.Now().After(deadline) {
			t.Fatalf("Rx never opened: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// newParty is a user agent at addr that sends everything to the
// P-CSCF.
func (n *pcc) newParty(addr netip.Addr, user string) *pccParty {
	n.t.Helper()
	p := &pccParty{calls: make(chan *sip.IncomingCall, 1), requests: make(chan *sip.Message, 32)}
	tr, err := sip.ListenObserved(netip.AddrPortFrom(addr, 0).String(), quiet(), func(c sip.Capture) {
		if c.Sent || c.Message.Method == "" {
			return
		}
		select {
		case p.requests <- c.Message:
		default: // receivedRequest then reports the request it misses
		}
	})
	if err != nil {
		n.t.Skipf("cannot listen on %v, which the test gives the UE: %v", addr, err)
	}
	p.contact = "sip:" + user + "@" + tr.LocalAddr().String()
	toPCSCF := func(sip.URI) (sip.Destination, error) {
		return sip.Destination{Transport: sip.UDP, Addr: n.pcscfAddr}, nil
	}
	p.ua, err = sip.NewUA(tr, sip.UAConfig{SentBy: tr.LocalAddr().String(), Contact: p.contact, Locate: toPCSCF,
		OnCall: func(ic *sip.IncomingCall) { p.calls <- ic }}, quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(p.ua.Close)
	return p
}

// attachedUE is a UE that opens its event stream and attaches to the
// EPC stub for the IMS APN, as TS 24.229 L.2.2.1 has a UE do before it
// registers.
func (n *pcc) attachedUE() *pccUE {
	n.t.Helper()
	u := &pccUE{pccParty: n.newParty(pccUEAddr, pccIMSI), epc: epc.NewClient(n.api, pccIMSI)}
	ctx, cancel := context.WithCancel(context.Background())
	n.t.Cleanup(cancel)
	events, err := u.epc.Events(ctx)
	if err != nil {
		n.t.Fatal(err)
	}
	u.events = events
	accept, err := u.epc.Attach(n.t.Context(), epc.AttachRequest{ESMMessageContainer: epc.PDNConnectivityRequest{
		ProcedureTransactionIdentity: 1, RequestType: epc.InitialRequest, PDNType: epc.IPv4, AccessPointName: "ims",
		ProtocolConfigurationOptions: epc.PCSCFIPv4AddressRequest(),
	}})
	if err != nil {
		n.t.Fatal(err)
	}
	if got := accept.ESMMessageContainer.PDNAddress.IPv4Address; got != pccUEAddr {
		n.t.Fatalf("the EPC gave the UE %v, want %v", got, pccUEAddr)
	}
	if err := u.epc.AttachComplete(n.t.Context(), epc.AttachComplete{ESMMessageContainer: epc.ActivateDefaultEPSBearerContextAccept{
		EPSBearerIdentity: accept.ESMMessageContainer.EPSBearerIdentity, ProcedureTransactionIdentity: 1}}); err != nil {
		n.t.Fatal(err)
	}
	return u
}

// register sends a REGISTER of the party's contact for expires seconds
// and waits for its 200.
func (n *pcc) register(p *pccParty, expires int) {
	n.t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<"+p.contact+">;expires="+strconv.Itoa(expires))
	m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
	ct, err := p.ua.Stack().SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: n.pcscfAddr})
	if err != nil {
		n.t.Fatal(err)
	}
	if resp := final(n.t, ct.Responses(), 3*time.Second); resp.StatusCode != 200 {
		n.t.Fatalf("REGISTER answered %d, want 200", resp.StatusCode)
	}
}

func (n *pcc) association(p *pccParty) Association {
	n.t.Helper()
	a, ok := n.store.Association(sip.Destination{Transport: sip.UDP, Addr: p.ua.Stack().LocalAddr()})
	if !ok {
		n.t.Fatal("the UE has no IP association")
	}
	return a
}

// signallingSession waits until the association of the party has the
// policy session of its signalling path: the P-CSCF puts it there once
// it has read the AAA, a moment after the test has seen the AAA.
func (n *pcc) signallingSession(p *pccParty) policy.SessionID {
	n.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if s := n.association(p).SignallingSession; s != "" {
			return s
		}
		if time.Now().After(deadline) {
			n.t.Fatal("the association has no signalling session")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// call has the UE call the far end, and returns the call on both sides.
func (n *pcc) call(u *pccParty, media netip.AddrPort) (*sip.OutgoingCall, *sip.IncomingCall) {
	n.t.Helper()
	out, err := u.ua.Invite(sip.Invitation{RequestURI: "sip:001010000000002@" + domain, From: impu,
		Route: []string{"<sip:pcscf." + domain + ":5060;lr>"}, Body: sdpBody(sdp.Offer("1", media))})
	if err != nil {
		n.t.Fatal(err)
	}
	return out, receiveCall(n.t, n.far.calls)
}

// rxName names a captured Rx message the way the ladder does, such as
// "AAR sent" or "ASR received".
func rxName(c diameter.Capture) string {
	names := map[uint32]string{rx.CommandAA: "AA", rx.CommandReAuth: "RA", rx.CommandSessionTermination: "ST", rx.CommandAbortSession: "AS"}
	name := names[c.Message.Header.CommandCode]
	if name == "" {
		name = "command " + strconv.Itoa(int(c.Message.Header.CommandCode)) + " "
	}
	if c.Message.Header.CommandFlags&diam.RequestFlag != 0 {
		name += "R"
	} else {
		name += "A"
	}
	if c.Sent {
		return name + " sent"
	}
	return name + " received"
}

// wantRx reads the next Rx messages of the P-CSCF, which must be want
// in this order, and returns them.
func (n *pcc) wantRx(want ...string) []*diam.Message {
	n.t.Helper()
	var got []*diam.Message
	for i, w := range want {
		select {
		case c := <-n.rx:
			if name := rxName(c); name != w {
				n.t.Fatalf("Rx message %d is %s, want %s", i+1, name, w)
			}
			got = append(got, c.Message)
		case <-time.After(5 * time.Second):
			n.t.Fatalf("no Rx message %d, want %s", i+1, w)
		}
	}
	return got
}

// noRx checks that the P-CSCF sends and receives nothing more over Rx.
func (n *pcc) noRx() {
	n.t.Helper()
	select {
	case c := <-n.rx:
		n.t.Fatalf("unexpected Rx message: %s", rxName(c))
	case <-time.After(300 * time.Millisecond):
	}
}

func parseAAR(t *testing.T, m *diam.Message) rx.AARequest {
	t.Helper()
	aar, err := rx.ParseAARequest(m)
	if err != nil {
		t.Fatal(err)
	}
	return aar
}

func wantResult(t *testing.T, what string, m *diam.Message, want rx.Result) {
	t.Helper()
	var got rx.Result
	var err error
	switch m.Header.CommandCode {
	case rx.CommandAA:
		var a rx.AAAnswer
		a, err = rx.ParseAAAnswer(m)
		got = a.Result
	case rx.CommandSessionTermination:
		var a rx.SessionTerminationAnswer
		a, err = rx.ParseSessionTerminationAnswer(m)
		got = a.Result
	case rx.CommandAbortSession:
		var a rx.AbortSessionAnswer
		a, err = rx.ParseAbortSessionAnswer(m)
		got = a.Result
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("%s with %v, want %v", what, got, want)
	}
}

// nextNetworkMessage is the next message of the EPC stub to the UE.
func (u *pccUE) nextNetworkMessage(t *testing.T) any {
	t.Helper()
	select {
	case m, ok := <-u.events:
		if !ok {
			t.Fatal("the event stream closed")
		}
		return m.Message
	case <-time.After(5 * time.Second):
		t.Fatal("no message from the EPC stub")
		return nil
	}
}

// voiceBearer waits for the dedicated bearer that the EPC stub sets up
// for the call, accepts it as the UE does, and returns its request.
func (u *pccUE) voiceBearer(t *testing.T) *epc.ActivateDedicatedEPSBearerContextRequest {
	t.Helper()
	req, ok := u.nextNetworkMessage(t).(*epc.ActivateDedicatedEPSBearerContextRequest)
	if !ok {
		t.Fatal("the UE got no ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST")
	}
	if err := u.epc.AcceptDedicatedBearer(t.Context(), epc.ActivateDedicatedEPSBearerContextAccept{
		EPSBearerIdentity: req.EPSBearerIdentity, ProcedureTransactionIdentity: req.ProcedureTransactionIdentity}); err != nil {
		t.Fatal(err)
	}
	return req
}

// rxSessions are the Rx sessions that the PCRF has bound to the UE's
// PDN connection, from the stub's API.
func (n *pcc) rxSessions() []string {
	n.t.Helper()
	resp, err := http.Get(n.api + "/ues/" + pccIMSI)
	if err != nil {
		n.t.Fatal(err)
	}
	defer resp.Body.Close()
	var s epc.Status
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		n.t.Fatal(err)
	}
	return s.RxSessions
}

// releaseBearer has the network release a bearer of the UE, through the
// API, as a learner does to see the call end.
func (n *pcc) releaseBearer(ebi uint8) {
	n.t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, n.api+"/network-initiated/ues/"+pccIMSI+"/bearers/"+strconv.Itoa(int(ebi)), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		n.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		n.t.Fatalf("releasing bearer %d: %d", ebi, resp.StatusCode)
	}
}

// receivedRequest waits for a request of method at the party.
func (p *pccParty) receivedRequest(t *testing.T, method sip.Method) *sip.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-p.requests:
			if m.Method == method {
				return m
			}
		case <-deadline:
			t.Fatalf("no %s reached %s", method, p.contact)
			return nil
		}
	}
}

var (
	pccUEMedia  = netip.MustParseAddrPort("127.0.0.2:4000")
	pccFarMedia = netip.MustParseAddrPort("192.0.2.99:6000")
)

// established is a call of an attached and registered UE that the far
// end has answered, with the voice bearer in place: the ladder of TS
// 29.214 A.4 and A.1 up to the 200.
func (n *pcc) established(u *pccUE) (*sip.OutgoingCall, *sip.IncomingCall, *epc.ActivateDedicatedEPSBearerContextRequest) {
	n.t.Helper()
	out, in, bearer := n.ringing(u)
	go in.Accept(answerTo(n.t, in.Request, pccFarMedia))
	answered(n.t, out, 200)
	// The 200 has the answer of the 183 again, which the PCRF has.
	n.noRx()
	return out, in, bearer
}

// ringing is a call of an attached and registered UE that the far end
// has answered with a 183 that carries its SDP answer.
func (n *pcc) ringing(u *pccUE) (*sip.OutgoingCall, *sip.IncomingCall, *epc.ActivateDedicatedEPSBearerContextRequest) {
	n.t.Helper()
	n.register(u.pccParty, 3600)
	n.wantRx("AAR sent", "AAA received")
	n.signallingSession(u.pccParty)
	out, in := n.call(u.pccParty, pccUEMedia)
	if err := in.Progress(183, answerTo(n.t, in.Request, pccFarMedia)); err != nil {
		n.t.Fatal(err)
	}
	n.wantRx("AAR sent", "AAA received")
	return out, in, u.voiceBearer(n.t)
}

// TestCallUnderPolicyControlOverRx is the normal ladder: the
// registration subscribes to the signalling path (TS 29.214 A.4, 4.4.5),
// the SDP answer has the PCRF authorise the media (A.1, 4.4.1) and the
// EPC stub set up the voice bearer, the BYE ends the Rx session (4.4.4)
// and with it the bearer, and the deregistration ends the subscription.
func TestCallUnderPolicyControlOverRx(t *testing.T) {
	n := newPCC(t)
	u := n.attachedUE()

	n.register(u.pccParty, 3600)
	msgs := n.wantRx("AAR sent", "AAA received")
	signalling := parseAAR(t, msgs[0])
	wantResult(t, "AAA of the signalling path", msgs[1], cx.Base(rx.DiameterSuccess))
	if signalling.FramedIPAddress != pccUEAddr ||
		!slices.Equal(signalling.SpecificActions, []rx.SpecificAction{rx.IndicationOfLossOfBearer, rx.IndicationOfReleaseOfBearer}) ||
		len(signalling.MediaComponents) != 1 || signalling.MediaComponents[0].Number != 0 {
		t.Errorf("AAR of the registration = %+v; want media component 0 for the UE's address with the loss and release of its bearer", signalling)
	}
	if got := n.signallingSession(u.pccParty); got != policy.SessionID(signalling.SessionID) {
		t.Errorf("signalling session of the association = %q, want the AAR's %q", got, signalling.SessionID)
	}

	out, in := n.call(u.pccParty, pccUEMedia)
	n.noRx() // an offer alone asks nothing of the PCRF
	if err := in.Progress(183, answerTo(t, in.Request, pccFarMedia)); err != nil {
		t.Fatal(err)
	}
	msgs = n.wantRx("AAR sent", "AAA received")
	media := parseAAR(t, msgs[0])
	wantResult(t, "AAA of the call", msgs[1], cx.Base(rx.DiameterSuccess))
	if media.FramedIPAddress != pccUEAddr || !slices.Contains(media.SpecificActions, rx.IndicationOfReleaseOfBearer) {
		t.Errorf("AAR of the call for %v with %v; want the UE's address and the release of the bearer", media.FramedIPAddress, media.SpecificActions)
	}
	if media.ServiceInfoStatus == nil || *media.ServiceInfoStatus != rx.FinalServiceInformation {
		t.Errorf("Service-Info-Status = %v, want FINAL_SERVICE_INFORMATION with the answer", media.ServiceInfoStatus)
	}
	if len(media.MediaComponents) != 1 || media.MediaComponents[0].Number != 1 || media.MediaComponents[0].Type != policy.Audio {
		t.Fatalf("media components = %+v, want the audio of the call", media.MediaComponents)
	}
	// The UE sends its media to the relay, so the uplink flow goes to
	// the address that the SDP to the UE names (TS 29.214 A.1).
	if flow := media.MediaComponents[0].SubComponents[0].Flows[0]; flow.Direction != policy.Uplink ||
		netip.AddrPortFrom(flow.Destination, flow.DestinationPort) != relayAccess {
		t.Errorf("first flow %+v, want the uplink to the relay's access termination %v", flow, relayAccess)
	}

	bearer := u.voiceBearer(t)
	if bearer.EPSQoS.QCI != 1 {
		t.Errorf("the voice bearer has QCI %d, want 1 (IR.92 4.4.2)", bearer.EPSQoS.QCI)
	}
	if !slices.ContainsFunc(bearer.TFT.PacketFilters, func(f epc.PacketFilter) bool {
		return f.Direction == epc.UplinkOnly && netip.AddrPortFrom(f.IPv4RemoteAddress, f.SingleRemotePort) == relayAccess
	}) {
		t.Errorf("packet filters %+v, want one for the uplink to the relay", bearer.TFT.PacketFilters)
	}
	if got, want := n.rxSessions(), []string{signalling.SessionID, media.SessionID}; !slices.Equal(got, want) {
		t.Errorf("Rx sessions at the PCRF = %v, want %v", got, want)
	}

	go in.Accept(answerTo(t, in.Request, pccFarMedia))
	answered(t, out, 200)
	n.noRx()

	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the far end's call", in.Done())
	msgs = n.wantRx("STR sent", "STA received")
	if rx.SessionID(msgs[0]) != media.SessionID {
		t.Errorf("STR for %q, want the session of the call %q", rx.SessionID(msgs[0]), media.SessionID)
	}
	wantResult(t, "STA", msgs[1], cx.Base(rx.DiameterSuccess))
	if m, ok := u.nextNetworkMessage(t).(*epc.DeactivateEPSBearerContextRequest); !ok || m.EPSBearerIdentity != bearer.EPSBearerIdentity {
		t.Errorf("the UE got %+v, want the voice bearer deactivated", m)
	}
	n.relay.waitHeld(t, 0, time.Second)

	n.register(u.pccParty, 0)
	msgs = n.wantRx("STR sent", "STA received")
	if rx.SessionID(msgs[0]) != signalling.SessionID {
		t.Errorf("STR for %q, want the session of the signalling path %q", rx.SessionID(msgs[0]), signalling.SessionID)
	}
	if got := n.rxSessions(); len(got) != 0 {
		t.Errorf("Rx sessions at the PCRF after the deregistration = %v, want none", got)
	}
}

// TestReleasedBearerEndsTheCall is TS 24.229 5.2.8.1.2: the network
// releases the voice bearer in the middle of the call, the PCRF aborts
// the Rx session of the call (TS 29.214 4.4.6.2), and the P-CSCF ends
// the call with a BYE to each party. The subscription to the
// signalling path stays.
func TestReleasedBearerEndsTheCall(t *testing.T) {
	n := newPCC(t)
	u := n.attachedUE()
	out, in, bearer := n.established(u)
	signalling := n.association(u.pccParty).SignallingSession

	n.releaseBearer(bearer.EPSBearerIdentity)
	if m, ok := u.nextNetworkMessage(t).(*epc.DeactivateEPSBearerContextRequest); !ok || m.ESMCause != epc.InsufficientResources {
		t.Errorf("the UE got %+v, want its bearer deactivated for insufficient resources", m)
	}
	msgs := n.wantRx("ASR received", "ASA sent", "STR sent", "STA received")
	asr, err := rx.ParseAbortSessionRequest(msgs[0])
	if err != nil {
		t.Fatal(err)
	}
	if asr.AbortCause != rx.BearerReleased {
		t.Errorf("Abort-Cause %v, want BEARER_RELEASED", asr.AbortCause)
	}
	wantResult(t, "ASA", msgs[1], cx.Base(rx.DiameterSuccess))
	if rx.SessionID(msgs[2]) != asr.SessionID {
		t.Errorf("STR for %q, want the aborted session %q", rx.SessionID(msgs[2]), asr.SessionID)
	}

	for _, party := range []struct {
		name string
		p    *pccParty
		done <-chan struct{}
	}{{"the UE", u.pccParty, out.Done()}, {"the far end", n.far, in.Done()}} {
		bye := party.p.receivedRequest(t, sip.BYE)
		if got, want := bye.Get("Reason"), `SIP;cause=503;text="Service Unavailable"`; got != want {
			t.Errorf("BYE to %s with Reason %q, want %q", party.name, got, want)
		}
		ended(t, party.name+"'s call", party.done)
	}
	n.relay.waitHeld(t, 0, time.Second)
	if got := n.rxSessions(); !slices.Equal(got, []string{string(signalling)}) {
		t.Errorf("Rx sessions at the PCRF = %v, want the signalling path's %q only", got, signalling)
	}
	n.noRx()
}

// TestReleasedBearerBeforeTheAnswerCancelsTheCall is TS 24.229
// 5.2.8.1.1: the bearer goes while the call is still ringing, so the
// P-CSCF answers the UE 500 and cancels the INVITE towards the far end.
func TestReleasedBearerBeforeTheAnswerCancelsTheCall(t *testing.T) {
	n := newPCC(t)
	u := n.attachedUE()
	out, in, bearer := n.ringing(u)

	n.releaseBearer(bearer.EPSBearerIdentity)
	n.wantRx("ASR received", "ASA sent", "STR sent", "STA received")
	resp := answered(t, out, 500)
	if got, want := resp.Get("Reason"), `FAILURE_CAUSE;cause=1;text="Media bearer or QoS lost"`; got != want {
		t.Errorf("Reason of the 500 = %q, want %q", got, want)
	}
	if got, want := resp.Get("Response-Source"), "fe=<urn:3gpp:fe:p-cscf.orig>"; got != want {
		t.Errorf("Response-Source of the 500 = %q, want %q", got, want)
	}
	if got, want := n.far.receivedRequest(t, sip.CANCEL).Get("Reason"), `SIP;cause=503;text="Service Unavailable"`; got != want {
		t.Errorf("Reason of the CANCEL = %q, want %q", got, want)
	}
	ended(t, "the far end's call", in.Done())
	if in.Err() != sip.ErrCancelled {
		t.Errorf("the far end's call ended with %v, want a CANCEL", in.Err())
	}
	n.relay.waitHeld(t, 0, time.Second)
	n.noRx()
}

// TestUEThatBypassesTheEPCCallsWithoutRx: the PCRF knows no
// IP-CAN session for a UE that does not come through the EPC, such as a
// softphone on the host, so the P-CSCF serves its calls without Rx.
func TestUEThatBypassesTheEPCCallsWithoutRx(t *testing.T) {
	n := newPCC(t)
	softphone := n.newParty(pccBypassAddr, "softphone")

	n.register(softphone, 3600)
	msgs := n.wantRx("AAR sent", "AAA received")
	if aar := parseAAR(t, msgs[0]); aar.FramedIPAddress != pccBypassAddr {
		t.Errorf("AAR for %v, want the softphone's address %v", aar.FramedIPAddress, pccBypassAddr)
	}
	wantResult(t, "AAA", msgs[1], cx.Experimental(rx.IPCANSessionNotAvailable))
	if a := n.association(softphone); a.SignallingSession != "" {
		t.Errorf("signalling session %q, want none", a.SignallingSession)
	}

	out, in := n.call(softphone, netip.AddrPortFrom(pccBypassAddr, 4000))
	if err := in.Progress(183, answerTo(t, in.Request, pccFarMedia)); err != nil {
		t.Fatal(err)
	}
	go in.Accept(answerTo(t, in.Request, pccFarMedia))
	answered(t, out, 200)
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	ended(t, "the far end's call", in.Done())
	n.noRx()
}
