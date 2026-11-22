package epc_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

const (
	realm = "ims.mnc001.mcc001.3gppnetwork.org"
	imsi1 = "001010000000001"
	imsi2 = "001010000000002"
)

var (
	ue1Addr  = netip.MustParseAddr("172.28.0.110")
	pcscfIP  = netip.MustParseAddr("172.28.0.40")
	relayIP  = netip.MustParseAddr("172.28.0.50")
	pcrfID   = diameter.Identity{OriginHost: "pcrf." + realm, OriginRealm: realm}
	afID     = diameter.Identity{OriginHost: "pcscf." + realm, OriginRealm: realm}
	timers   = diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}
	cell     = epc.SystemInformation{TrackingAreaIdentity: epc.TrackingAreaIdentity{PLMN: epc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1}, CellIdentity: 0x0000101}
	stubConf = epc.Config{
		APN:            "ims",
		PCSCFAddresses: []netip.Addr{pcscfIP},
		Subscribers:    []epc.Subscriber{{IMSI: imsi1, Address: ue1Addr}, {IMSI: imsi2, Address: netip.MustParseAddr("172.28.0.111")}},
		Cell:           cell,
	}
)

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// network is the stub with its UE API on an httptest server and its
// PCRF on a Diameter connection from a fake AF.
type network struct {
	t   *testing.T
	api string
	af  *fakeAF
}

func newNetwork(t *testing.T) *network {
	t.Helper()
	node := diameter.NewNode(pcrfID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	stub := epc.New(stubConf, epc.NewMemoryStore(), node, quiet())
	node.Handle(stub.HandleRx)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	stub.SetAFs(server)
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)
	return &network{t: t, api: api.URL, af: newFakeAF(t, server.Addr())}
}

// fakeAF is a P-CSCF that sends what a test tells it to and keeps the
// RARs and ASRs of the PCRF.
type fakeAF struct {
	t        *testing.T
	node     *diameter.Node
	peer     *diameter.Peer
	requests chan *diam.Message
}

func newFakeAF(t *testing.T, pcrfAddr string) *fakeAF {
	af := &fakeAF{t: t, requests: make(chan *diam.Message, 16)}
	af.node = diameter.NewNode(afID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	af.node.Handle(func(req *diam.Message) *diam.Message {
		af.requests <- req
		if req.Header.CommandCode == rx.CommandAbortSession {
			return rx.AbortSessionAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(req, afID)
		}
		return rx.ReAuthAnswer{Result: diameter.Base(diameter.DiameterSuccess)}.Message(req, afID)
	})
	af.peer = af.node.Connect(diameter.PeerConfig{Address: pcrfAddr, OriginHost: pcrfID.OriginHost})
	t.Cleanup(af.peer.Stop)
	return af
}

// send sends a request, waiting for the connection to open first.
func (af *fakeAF) send(request *diam.Message) *diam.Message {
	af.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ans, err := af.peer.Request(af.t.Context(), request)
		if err == nil {
			return ans
		}
		if !errors.Is(err, diameter.ErrPeerUnavailable) || time.Now().After(deadline) {
			af.t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (af *fakeAF) header() diameter.Header { return diameter.NewHeader(af.node, "", realm) }

func (af *fakeAF) aar(r rx.AARequest) diameter.Result {
	af.t.Helper()
	aaa, err := rx.ParseAAAnswer(af.send(r.Message()))
	if err != nil {
		af.t.Fatal(err)
	}
	return aaa.Result
}

func (af *fakeAF) str(sessionID string) diameter.Result {
	af.t.Helper()
	h := af.header()
	h.SessionID = sessionID
	sta, err := rx.ParseSessionTerminationAnswer(af.send(rx.SessionTerminationRequest{Header: h, TerminationCause: rx.DiameterLogout}.Message()))
	if err != nil {
		af.t.Fatal(err)
	}
	return sta.Result
}

func (af *fakeAF) next() *diam.Message {
	af.t.Helper()
	select {
	case m := <-af.requests:
		return m
	case <-time.After(5 * time.Second):
		af.t.Fatal("no request from the PCRF")
		return nil
	}
}

func (af *fakeAF) none() {
	af.t.Helper()
	select {
	case m := <-af.requests:
		af.t.Fatalf("unexpected request %d from the PCRF", m.Header.CommandCode)
	case <-time.After(100 * time.Millisecond):
	}
}

// ue is a UE on the stub's API with its event stream open.
type ue struct {
	t      *testing.T
	client *epc.Client
	events <-chan epc.NetworkMessage
}

func (n *network) ue(imsi string) *ue {
	n.t.Helper()
	c := epc.NewClient(n.api, imsi)
	ctx, cancel := context.WithCancel(context.Background())
	n.t.Cleanup(cancel)
	events, err := c.Events(ctx)
	if err != nil {
		n.t.Fatal(err)
	}
	return &ue{t: n.t, client: c, events: events}
}

func (u *ue) attach() epc.AttachAccept {
	u.t.Helper()
	accept, err := u.client.Attach(u.t.Context(), epc.AttachRequest{ESMMessageContainer: epc.PDNConnectivityRequest{
		ProcedureTransactionIdentity: 1, RequestType: epc.InitialRequest, PDNType: epc.IPv4, AccessPointName: "ims",
		ProtocolConfigurationOptions: epc.PCSCFIPv4AddressRequest(),
	}})
	if err != nil {
		u.t.Fatal(err)
	}
	if err := u.client.AttachComplete(u.t.Context(), epc.AttachComplete{ESMMessageContainer: epc.ActivateDefaultEPSBearerContextAccept{
		EPSBearerIdentity: accept.ESMMessageContainer.EPSBearerIdentity, ProcedureTransactionIdentity: 1}}); err != nil {
		u.t.Fatal(err)
	}
	return accept
}

func (u *ue) next() epc.NetworkMessage {
	u.t.Helper()
	select {
	case m, ok := <-u.events:
		if !ok {
			u.t.Fatal("event stream closed")
		}
		return m
	case <-time.After(5 * time.Second):
		u.t.Fatal("no message from the network")
		return epc.NetworkMessage{}
	}
}

func sdpOf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n") }

// callComponents is the service information of a PCMU call of UE 1
// through the media relay, as a P-CSCF derives it.
func callComponents(t *testing.T, media string) []policy.MediaComponent {
	t.Helper()
	offer := sdpOf("v=0", "o=- 1 1 IN IP4 172.28.0.110", "s=-", "c=IN IP4 172.28.0.110", "t=0 0",
		"m="+media+" 49000 RTP/AVP 0", "b=AS:80", "a=rtpmap:0 PCMU/8000")
	answer := sdpOf("v=0", "o=- 2 1 IN IP4 172.28.0.50", "s=-", "c=IN IP4 172.28.0.50", "t=0 0",
		"m="+media+" 40000 RTP/AVP 0", "b=AS:80", "a=rtpmap:0 PCMU/8000")
	components, err := policy.MediaComponents(policy.OfferAnswer{Offer: offer, Answer: answer, UEOffered: true})
	if err != nil {
		t.Fatal(err)
	}
	return components
}

func (n *network) callAAR(subscribe ...rx.SpecificAction) rx.AARequest {
	return rx.AARequest{Header: n.af.header(), MediaComponents: callComponents(n.t, "audio"),
		SpecificActions: subscribe, FramedIPAddress: ue1Addr}
}

func (n *network) status(imsi string) (epc.Status, int) {
	n.t.Helper()
	resp, err := http.Get(n.api + "/ues/" + imsi)
	if err != nil {
		n.t.Fatal(err)
	}
	defer resp.Body.Close()
	var s epc.Status
	if resp.StatusCode == http.StatusOK {
		decodeJSON(n.t, resp, &s)
	}
	return s, resp.StatusCode
}

func (n *network) networkInitiated(method, path string) int {
	n.t.Helper()
	req, _ := http.NewRequest(method, n.api+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		n.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func decodeJSON(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestAttachGivesTheAddressAndTheP_CSCFs(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	accept := u.attach()
	esm := accept.ESMMessageContainer
	if esm.EPSBearerIdentity != 5 || esm.ProcedureTransactionIdentity != 1 || esm.EPSQoS.QCI != 5 || esm.AccessPointName != "ims" {
		t.Errorf("ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST = %+v", esm)
	}
	if esm.PDNAddress != (epc.PDNAddress{PDNTypeValue: epc.IPv4, IPv4Address: ue1Addr}) {
		t.Errorf("PDN address = %+v, want %v", esm.PDNAddress, ue1Addr)
	}
	if got := esm.ProtocolConfigurationOptions.PCSCFIPv4Addresses(); !reflect.DeepEqual(got, []netip.Addr{pcscfIP}) {
		t.Errorf("P-CSCF addresses = %v, want %v", got, pcscfIP)
	}
	if !reflect.DeepEqual(accept.TAIList, []epc.TrackingAreaIdentity{cell.TrackingAreaIdentity}) {
		t.Errorf("TAI list = %+v", accept.TAIList)
	}
	s, code := n.status(imsi1)
	if code != http.StatusOK || len(s.Bearers) != 1 || s.Bearers[0].State != epc.BearerActive || s.PDNAddress != ue1Addr {
		t.Errorf("status %d %+v, want the default bearer active", code, s)
	}
	si, err := u.client.SystemInformation(t.Context())
	if err != nil || si != cell {
		t.Errorf("system information = %+v, %v; want %+v", si, err, cell)
	}
}

func TestAttachRefusesWhatTheStubDoesNotServe(t *testing.T) {
	n := newNetwork(t)
	_, err := epc.NewClient(n.api, "001010000000099").Attach(t.Context(), epc.AttachRequest{})
	var apiErr *epc.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("unknown IMSI: %v, want 404", err)
	}
	_, err = epc.NewClient(n.api, imsi1).Attach(t.Context(), epc.AttachRequest{ESMMessageContainer: epc.PDNConnectivityRequest{
		RequestType: epc.InitialRequest, PDNType: epc.IPv4, AccessPointName: "internet"}})
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("other APN: %v, want 400", err)
	}
}

// An AF may name source ports too (TS 29.214 5.3.8). Seen from the UE,
// that is the local port of an uplink flow and the remote port of a
// downlink one.
func TestSourcePortsGoInTheTFT(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.attach()
	aar := n.callAAR()
	rtp := aar.MediaComponents[0].SubComponents[0].Flows
	rtp[0].SourcePort, rtp[1].SourcePort = 49000, 40000
	if r := n.af.aar(aar); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("AAA %v", r)
	}
	req, ok := u.next().Message.(*epc.ActivateDedicatedEPSBearerContextRequest)
	if !ok || len(req.TFT.PacketFilters) < 2 {
		t.Fatalf("UE got %+v, want ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST with the flows", req)
	}
	up, down := req.TFT.PacketFilters[0], req.TFT.PacketFilters[1]
	if up.SingleLocalPort != 49000 || up.SingleRemotePort != 40000 || down.SingleLocalPort != 49000 || down.SingleRemotePort != 40000 {
		t.Errorf("uplink %+v, downlink %+v; want local port 49000 and remote port 40000 in both", up, down)
	}
}

// A call's AAR makes the stub activate the voice bearer (QCI 1) with a
// TFT of the call's flows, and the PCRF reports the resources to an AF
// that asked once the UE accepts.
func TestTheMediaOfACallGetAVoiceBearer(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.attach()
	aar := n.callAAR(rx.IndicationOfReleaseOfBearer, rx.IndicationOfSuccessfulResourcesAllocation)
	if r := n.af.aar(aar); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("AAA %v", r)
	}
	m := u.next()
	req, ok := m.Message.(*epc.ActivateDedicatedEPSBearerContextRequest)
	if !ok {
		t.Fatalf("UE got %s, want ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST", m.Type)
	}
	// 80 kbit/s from b=AS each way, as both maximum and guaranteed rate.
	wantQoS := epc.EPSQoS{QCI: 1, MaximumBitRateForUplink: 80, MaximumBitRateForDownlink: 80, GuaranteedBitRateForUplink: 80, GuaranteedBitRateForDownlink: 80}
	if req.EPSBearerIdentity != 6 || req.LinkedEPSBearerIdentity != 5 || req.EPSQoS != wantQoS {
		t.Errorf("request = %+v", req)
	}
	wantFilters := []epc.PacketFilter{
		{Identifier: 1, Direction: epc.UplinkOnly, EvaluationPrecedence: 0, IPv4RemoteAddress: relayIP, ProtocolIdentifier: 17, SingleRemotePort: 40000},
		{Identifier: 2, Direction: epc.DownlinkOnly, EvaluationPrecedence: 1, IPv4RemoteAddress: relayIP, ProtocolIdentifier: 17, SingleLocalPort: 49000},
		{Identifier: 3, Direction: epc.UplinkOnly, EvaluationPrecedence: 2, IPv4RemoteAddress: relayIP, ProtocolIdentifier: 17, SingleRemotePort: 40001},
		{Identifier: 4, Direction: epc.DownlinkOnly, EvaluationPrecedence: 3, IPv4RemoteAddress: relayIP, ProtocolIdentifier: 17, SingleLocalPort: 49001},
	}
	if req.TFT.OperationCode != epc.CreateNewTFT || !reflect.DeepEqual(req.TFT.PacketFilters, wantFilters) {
		t.Errorf("TFT\n got %+v\nwant %+v", req.TFT, wantFilters)
	}
	n.af.none() // nothing until the UE accepts

	if err := u.client.AcceptDedicatedBearer(t.Context(), epc.ActivateDedicatedEPSBearerContextAccept{EPSBearerIdentity: 6}); err != nil {
		t.Fatal(err)
	}
	rar, err := rx.ParseReAuthRequest(n.af.next())
	if err != nil {
		t.Fatal(err)
	}
	want := rx.ReAuthRequest{
		Header:          diameter.Header{SessionID: aar.SessionID, OriginHost: pcrfID.OriginHost, OriginRealm: realm, DestinationHost: afID.OriginHost, DestinationRealm: realm},
		SpecificActions: []rx.SpecificAction{rx.IndicationOfSuccessfulResourcesAllocation},
		Flows:           []rx.Flows{{MediaComponentNumber: 1, FlowNumbers: []uint32{1, 2}}},
	}
	if !reflect.DeepEqual(rar, want) {
		t.Errorf("RAR\n got %+v\nwant %+v", rar, want)
	}
	s, _ := n.status(imsi1)
	if len(s.Bearers) != 2 || s.Bearers[1].State != epc.BearerActive || !reflect.DeepEqual(s.RxSessions, []string{aar.SessionID}) {
		t.Errorf("status %+v", s)
	}
}

func TestSTRReleasesTheVoiceBearer(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.attach()
	aar := n.callAAR()
	n.af.aar(aar)
	u.next() // the activation
	if r := n.af.str(aar.SessionID); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("STA %v", r)
	}
	m := u.next()
	req, ok := m.Message.(*epc.DeactivateEPSBearerContextRequest)
	if !ok || req.EPSBearerIdentity != 6 || req.ESMCause != epc.RegularDeactivation {
		t.Errorf("UE got %s %+v, want DEACTIVATE EPS BEARER CONTEXT REQUEST for 6 with #36", m.Type, m.Message)
	}
	if s, _ := n.status(imsi1); len(s.Bearers) != 1 || len(s.RxSessions) != 0 {
		t.Errorf("status %+v, want the default bearer only", s)
	}
	if r := n.af.str(aar.SessionID); r != diameter.Base(diameter.DiameterUnknownSessionID) {
		t.Errorf("second STA %v, want DIAMETER_UNKNOWN_SESSION_ID", r)
	}
}

// Deactivating the voice bearer from the API takes every flow
// of the call, so the AF that asked hears an ASR (TS 29.214 4.4.6.2);
// the subscription to the signalling path is not touched.
func TestReleasingTheVoiceBearerAbortsTheCall(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.attach()
	signalling := rx.AARequest{Header: n.af.header(), MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		SpecificActions: []rx.SpecificAction{rx.IndicationOfLossOfBearer, rx.IndicationOfReleaseOfBearer}, FramedIPAddress: ue1Addr}
	if r := n.af.aar(signalling); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("AAA of the signalling path %v", r)
	}
	call := n.callAAR(rx.IndicationOfReleaseOfBearer)
	n.af.aar(call)
	u.next()
	if err := u.client.AcceptDedicatedBearer(t.Context(), epc.ActivateDedicatedEPSBearerContextAccept{EPSBearerIdentity: 6}); err != nil {
		t.Fatal(err)
	}

	if code := n.networkInitiated(http.MethodDelete, "/network-initiated/ues/"+imsi1+"/bearers/6"); code != http.StatusNoContent {
		t.Fatalf("release: %d", code)
	}
	m := u.next()
	if req, ok := m.Message.(*epc.DeactivateEPSBearerContextRequest); !ok || req.EPSBearerIdentity != 6 || req.ESMCause != epc.InsufficientResources {
		t.Errorf("UE got %s %+v, want DEACTIVATE EPS BEARER CONTEXT REQUEST for 6 with #26", m.Type, m.Message)
	}
	asr, err := rx.ParseAbortSessionRequest(n.af.next())
	if err != nil {
		t.Fatal(err)
	}
	if asr.SessionID != call.SessionID || asr.AbortCause != rx.BearerReleased || asr.DestinationHost != afID.OriginHost {
		t.Errorf("ASR = %+v, want BEARER_RELEASED for the call", asr)
	}
	n.af.none()
	if r := n.af.str(call.SessionID); r != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("STA after the ASR %v", r)
	}
	if s, _ := n.status(imsi1); !reflect.DeepEqual(s.RxSessions, []string{signalling.SessionID}) || len(s.Bearers) != 1 {
		t.Errorf("status %+v, want the signalling session and the default bearer", s)
	}
	if code := n.networkInitiated(http.MethodDelete, "/network-initiated/ues/"+imsi1+"/bearers/6"); code != http.StatusNotFound {
		t.Errorf("releasing a released bearer: %d, want 404", code)
	}
}

// Detaching the UE ends its IP-CAN session: the PCRF sends an ASR on
// every Rx session (TS 29.214 4.4.6.1), and the STRs that follow
// succeed.
func TestDetachAbortsEveryRxSession(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.attach()
	signalling := rx.AARequest{Header: n.af.header(), MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		SpecificActions: []rx.SpecificAction{rx.IndicationOfReleaseOfBearer}, FramedIPAddress: ue1Addr}
	n.af.aar(signalling)
	call := n.callAAR()
	n.af.aar(call)
	u.next()

	if code := n.networkInitiated(http.MethodPost, "/network-initiated/ues/"+imsi1+"/detach"); code != http.StatusNoContent {
		t.Fatalf("detach: %d", code)
	}
	if m := u.next(); m.Type != epc.TypeDetachRequest || m.Message.(*epc.DetachRequest).DetachType != epc.ReattachNotRequired {
		t.Errorf("UE got %s %+v, want DETACH REQUEST, re-attach not required", m.Type, m.Message)
	}
	aborted := map[string]bool{}
	for range 2 {
		asr, err := rx.ParseAbortSessionRequest(n.af.next())
		if err != nil {
			t.Fatal(err)
		}
		aborted[asr.SessionID] = true
	}
	if !aborted[signalling.SessionID] || !aborted[call.SessionID] {
		t.Errorf("ASRs for %v, want both sessions", aborted)
	}
	for _, id := range []string{signalling.SessionID, call.SessionID} {
		if r := n.af.str(id); r != diameter.Base(diameter.DiameterSuccess) {
			t.Errorf("STA after the ASR: %v", r)
		}
	}
	if _, code := n.status(imsi1); code != http.StatusConflict {
		t.Errorf("status after detach: %d, want 409", code)
	}
	if err := u.client.AcceptDetach(t.Context()); err != nil {
		t.Error(err)
	}
}

func TestAARsThePCRFRefuses(t *testing.T) {
	n := newNetwork(t)
	n.ue(imsi1).attach()
	// No UE has this address: binding fails (TS 29.214 4.4.1). A softphone
	// on the host gets this, and its P-CSCF goes on without Rx.
	other := n.callAAR()
	other.FramedIPAddress = netip.MustParseAddr("172.28.0.1")
	if r := n.af.aar(other); r != diameter.Experimental(rx.IPCANSessionNotAvailable) {
		t.Errorf("unknown address: %v, want IP-CAN_SESSION_NOT_AVAILABLE", r)
	}
	video := rx.AARequest{Header: n.af.header(), MediaComponents: callComponents(t, "video"), FramedIPAddress: ue1Addr}
	if r := n.af.aar(video); r != diameter.Experimental(rx.RequestedServiceNotAuthorized) {
		t.Errorf("video: %v, want REQUESTED_SERVICE_NOT_AUTHORIZED", r)
	}
	// A packet filter of the stub's TFT names one remote host.
	masked := n.callAAR()
	masked.MediaComponents[0].SubComponents[0].Flows[0].Destination = netip.MustParsePrefix("172.28.0.0/24")
	if r := n.af.aar(masked); r != diameter.Experimental(rx.FilterRestrictions) {
		t.Errorf("masked address: %v, want FILTER_RESTRICTIONS", r)
	}
	if r := n.af.str(other.SessionID); r != diameter.Base(diameter.DiameterUnknownSessionID) {
		t.Errorf("STR of a refused session: %v, want DIAMETER_UNKNOWN_SESSION_ID", r)
	}
}

// A UE that rejects the voice bearer, or cannot be reached, leaves the
// call without resources; an AF that asked hears it (TS 29.214 4.4.1).
func TestFailedVoiceBearerIsReported(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stream bool
	}{{"rejected", true}, {"no event stream", false}} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			var u *ue
			if tc.stream {
				u = n.ue(imsi1)
			} else {
				u = &ue{t: t, client: epc.NewClient(n.api, imsi1)}
			}
			u.attach()
			aar := n.callAAR(rx.IndicationOfFailedResourcesAllocation)
			if r := n.af.aar(aar); r != diameter.Base(diameter.DiameterSuccess) {
				t.Fatalf("AAA %v", r)
			}
			if tc.stream {
				u.next()
				if err := u.client.RejectDedicatedBearer(t.Context(), epc.ActivateDedicatedEPSBearerContextReject{
					EPSBearerIdentity: 6, ESMCause: epc.InsufficientResources}); err != nil {
					t.Fatal(err)
				}
			}
			rar, err := rx.ParseReAuthRequest(n.af.next())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(rar.SpecificActions, []rx.SpecificAction{rx.IndicationOfFailedResourcesAllocation}) || len(rar.Flows) != 1 {
				t.Errorf("RAR = %+v, want INDICATION_OF_FAILED_RESOURCES_ALLOCATION with the flows", rar)
			}
			if s, _ := n.status(imsi1); len(s.Bearers) != 1 {
				t.Errorf("status %+v, want no voice bearer", s)
			}
		})
	}
}
