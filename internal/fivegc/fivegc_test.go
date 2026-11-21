package fivegc_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
)

const (
	imsi1 = "001010000000001"
	imsi2 = "001010000000002"
	dnn   = "ims"
)

var (
	ue1Addr   = netip.MustParseAddr("172.28.0.110")
	pcscfIPv4 = netip.MustParseAddr("172.28.0.40")
	pcscfIPv6 = netip.MustParseAddr("2001:db8::40")
	cell      = fivegc.SystemInformation{
		TrackingAreaIdentity: fivegc.TrackingAreaIdentity{PLMN: fivegc.PLMNIdentity{MCC: "001", MNC: "01"}, TAC: 1},
		NRCellIdentity:       0x000000101,
	}
	stubConf = fivegc.Config{
		DNN:            dnn,
		PCSCFAddresses: []netip.Addr{pcscfIPv4, pcscfIPv6},
		Subscribers:    []fivegc.Subscriber{{IMSI: imsi1, Address: ue1Addr}, {IMSI: imsi2, Address: netip.MustParseAddr("172.28.0.111")}},
		Cell:           cell,
	}
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

// network is the stub with its UE API on an httptest server.
type network struct {
	t    *testing.T
	stub *fivegc.FiveGC
	api  string
}

func newNetwork(t *testing.T) *network {
	t.Helper()
	stub := fivegc.New(stubConf, fivegc.NewMemoryStore(), quiet())
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)
	return &network{t: t, stub: stub, api: api.URL}
}

// ue is a UE with its client and its open event stream.
type ue struct {
	t      *testing.T
	client *fivegc.Client
	events <-chan fivegc.NetworkMessage
}

func (n *network) ue(imsi string) *ue {
	n.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	n.t.Cleanup(cancel)
	c := fivegc.NewClient(n.api, imsi)
	events, err := c.Events(ctx)
	if err != nil {
		n.t.Fatal(err)
	}
	return &ue{t: n.t, client: c, events: events}
}

func (u *ue) register() fivegc.RegistrationAccept {
	u.t.Helper()
	accept, err := u.client.Register(u.t.Context(), fivegc.RegistrationRequest{FiveGSRegistrationType: fivegc.InitialRegistration})
	if err != nil {
		u.t.Fatal(err)
	}
	return accept
}

func establishment(pduSessionType fivegc.PDUSessionType) fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest] {
	m := fivegc.NewULNASTransport(1, fivegc.PDUSessionEstablishmentRequest{
		PDUSessionID: 1, PTI: 1, PDUSessionType: pduSessionType, SSCMode: fivegc.SSCMode1,
		ExtendedProtocolConfigurationOptions: fivegc.PCSCFAddressRequest(),
	})
	m.RequestType, m.DNN = fivegc.InitialRequest, dnn
	return m
}

func (u *ue) establish() fivegc.PDUSessionEstablishmentAccept {
	u.t.Helper()
	accept, err := u.client.EstablishPDUSession(u.t.Context(), establishment(fivegc.IPv4))
	if err != nil {
		u.t.Fatal(err)
	}
	return accept.PayloadContainer
}

// next waits for the next message of the network.
func (u *ue) next() fivegc.NetworkMessage {
	u.t.Helper()
	select {
	case m, ok := <-u.events:
		if !ok {
			u.t.Fatal("the event stream closed")
		}
		return m
	case <-time.After(5 * time.Second):
		u.t.Fatal("no message from the network")
	}
	return fivegc.NetworkMessage{}
}

func (u *ue) nothingMore() {
	u.t.Helper()
	select {
	case m := <-u.events:
		u.t.Fatalf("unexpected %s", m.Type)
	case <-time.After(100 * time.Millisecond):
	}
}

func statusCode(err error) int {
	var apiErr *fivegc.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode
	}
	return 0
}

// The UE registers, learns that IMS voice over PS sessions are
// supported, and sets up its PDU session to the IMS DNN with the QoS
// flow of the default QoS rule (5QI 5) and the P-CSCF addresses of both
// versions.
func TestRegistrationAndPDUSessionEstablishment(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)

	si, err := u.client.SystemInformation(t.Context())
	if err != nil || si != cell {
		t.Fatalf("system information = %+v, %v", si, err)
	}
	reg := u.register()
	if !reg.FiveGSNetworkFeatureSupport.IMSVoPS3GPP {
		t.Error("IMS voice over PS session is not supported")
	}
	if reg.FiveGSRegistrationResult.Value != fivegc.Access3GPP || len(reg.TAIList) != 1 || reg.TAIList[0] != cell.TrackingAreaIdentity {
		t.Errorf("registration accept = %+v", reg)
	}

	accept, err := u.client.EstablishPDUSession(t.Context(), establishment(fivegc.IPv4))
	if err != nil {
		t.Fatal(err)
	}
	if accept.PayloadContainerType != fivegc.N1SMInformation || accept.PDUSessionID != 1 {
		t.Errorf("DL NAS TRANSPORT = %+v", accept)
	}
	a := accept.PayloadContainer
	if a.PDUSessionID != 1 || a.PTI != 1 || a.SelectedPDUSessionType != fivegc.IPv4 || a.SelectedSSCMode != fivegc.SSCMode1 || a.DNN != dnn || a.FiveGSMCause != 0 {
		t.Errorf("accept = %+v", a)
	}
	if a.PDUAddress != (fivegc.PDUAddress{PDUSessionTypeValue: fivegc.IPv4, IPv4Address: ue1Addr}) {
		t.Errorf("PDU address = %+v", a.PDUAddress)
	}
	wantRule := fivegc.QoSRule{QoSRuleIdentifier: 1, RuleOperationCode: fivegc.CreateNewQoSRule, DQR: true, QoSRulePrecedence: 255, QFI: 1,
		PacketFilters: []fivegc.PacketFilter{{Direction: fivegc.Bidirectional, Identifier: 1, MatchAll: true}}}
	if !reflect.DeepEqual(a.AuthorizedQoSRules, []fivegc.QoSRule{wantRule}) {
		t.Errorf("QoS rules = %+v", a.AuthorizedQoSRules)
	}
	wantFlow := fivegc.QoSFlowDescription{QFI: 1, OperationCode: fivegc.CreateNewQoSFlowDescription, FiveQI: fivegc.FiveQIIMSSignalling}
	if !reflect.DeepEqual(a.AuthorizedQoSFlowDescriptions, []fivegc.QoSFlowDescription{wantFlow}) {
		t.Errorf("QoS flow descriptions = %+v", a.AuthorizedQoSFlowDescriptions)
	}
	if got := a.ExtendedProtocolConfigurationOptions.PCSCFAddresses(); !reflect.DeepEqual(got, []netip.Addr{pcscfIPv4, pcscfIPv6}) {
		t.Errorf("P-CSCF addresses = %v", got)
	}
	if c := a.ExtendedProtocolConfigurationOptions.Containers; len(c) == 0 || c[0].ID != fivegc.IMCNSubsystemSignalingFlag {
		t.Errorf("the IM CN Subsystem Signaling Flag is not echoed: %+v", c)
	}

	st, err := n.stub.Status(imsi1)
	if err != nil {
		t.Fatal(err)
	}
	if st.RegistrationState != fivegc.Registered || st.PDUSession == nil || st.PDUSession.State != fivegc.PDUSessionActive ||
		len(st.PDUSession.QoSFlows) != 1 || st.PDUSession.QoSFlows[0].FiveQI != fivegc.FiveQIIMSSignalling {
		t.Errorf("status = %+v", st)
	}
	u.nothingMore()
}

// A UE that asks only for IPv4 P-CSCFs gets only those.
func TestPCSCFAddressesFollowTheRequest(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	m := establishment(fivegc.IPv4)
	m.PayloadContainer.ExtendedProtocolConfigurationOptions = fivegc.ExtendedProtocolConfigurationOptions{
		Containers: []fivegc.Container{{ID: fivegc.PCSCFIPv4Address}}}
	accept, err := u.client.EstablishPDUSession(t.Context(), m)
	if err != nil {
		t.Fatal(err)
	}
	want := []fivegc.Container{{ID: fivegc.PCSCFIPv4Address, Contents: pcscfIPv4.String()}}
	if got := accept.PayloadContainer.ExtendedProtocolConfigurationOptions.Containers; !reflect.DeepEqual(got, want) {
		t.Errorf("extended PCO = %+v, want %+v", got, want)
	}
}

// TS 24.501 6.4.1.3: a UE that asks for IPv4v6 from a network limited
// to IPv4 gets IPv4 and #50.
func TestIPv4v6RequestGetsIPv4WithCause50(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	accept, err := u.client.EstablishPDUSession(t.Context(), establishment(fivegc.IPv4v6))
	if err != nil {
		t.Fatal(err)
	}
	if a := accept.PayloadContainer; a.SelectedPDUSessionType != fivegc.IPv4 || a.FiveGSMCause != fivegc.PDUSessionTypeIPv4OnlyAllowed {
		t.Errorf("accept = %+v", a)
	}
}

func TestPDUSessionEstablishmentErrors(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	if _, err := u.client.EstablishPDUSession(t.Context(), establishment(fivegc.IPv4)); statusCode(err) != http.StatusConflict {
		t.Errorf("before registration: %v, want 409", err)
	}
	u.register()
	for name, change := range map[string]func(*fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]){
		"another DNN": func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) { m.DNN = "internet" },
		"no DNN":      func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) { m.DNN = "" },
		"IPv6": func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) {
			m.PayloadContainer.PDUSessionType = fivegc.IPv6
		},
		"existing PDU session": func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) {
			m.RequestType = "existing PDU session"
		},
		"PDU session ID differs": func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) { m.PDUSessionID = 2 },
		"SSC mode 2": func(m *fivegc.ULNASTransport[fivegc.PDUSessionEstablishmentRequest]) {
			m.PayloadContainer.SSCMode = "SSC mode 2"
		},
	} {
		m := establishment(fivegc.IPv4)
		change(&m)
		if _, err := u.client.EstablishPDUSession(t.Context(), m); statusCode(err) != http.StatusBadRequest {
			t.Errorf("%s: %v, want 400", name, err)
		}
	}
	if _, err := fivegc.NewClient(n.api, "001010000000099").Register(t.Context(), fivegc.RegistrationRequest{FiveGSRegistrationType: fivegc.InitialRegistration}); statusCode(err) != http.StatusNotFound {
		t.Errorf("unknown IMSI: %v, want 404", err)
	}
}

// The UE releases its PDU session: the network answers with PDU SESSION
// RELEASE COMMAND carrying the PTI of the request (TS 24.501 6.3.3.2).
func TestUERequestedPDUSessionRelease(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	command, err := u.client.ReleasePDUSession(t.Context(), fivegc.NewULNASTransport(1, fivegc.PDUSessionReleaseRequest{PDUSessionID: 1, PTI: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if c := command.PayloadContainer; c.PDUSessionID != 1 || c.PTI != 2 || c.FiveGSMCause != fivegc.RegularDeactivation {
		t.Errorf("release command = %+v", c)
	}
	if err := u.client.CompletePDUSessionRelease(t.Context(), fivegc.NewULNASTransport(1, fivegc.PDUSessionReleaseComplete{PDUSessionID: 1, PTI: 2})); err != nil {
		t.Fatal(err)
	}
	if st, err := n.stub.Status(imsi1); err != nil || st.PDUSession != nil {
		t.Errorf("status = %+v, %v", st, err)
	}
	if _, err := u.client.ReleasePDUSession(t.Context(), fivegc.NewULNASTransport(1, fivegc.PDUSessionReleaseRequest{PDUSessionID: 1, PTI: 3})); statusCode(err) != http.StatusConflict {
		t.Errorf("second release: %v, want 409", err)
	}
}

// A UE that deregisters loses its PDU session with it (TS 24.501
// 5.5.2.2.3); one that switches off gets no DEREGISTRATION ACCEPT.
func TestUEInitiatedDeregistration(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	if err := u.client.Deregister(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if _, err := n.stub.Status(imsi1); !errors.Is(err, fivegc.ErrNotRegistered) {
		t.Errorf("status after deregistration: %v", err)
	}
	if err := u.client.Deregister(t.Context(), false); statusCode(err) != http.StatusConflict {
		t.Errorf("second deregistration: %v, want 409", err)
	}
	u.register()
	if err := u.client.Deregister(t.Context(), true); err != nil {
		t.Fatalf("switch off: %v", err)
	}
	u.nothingMore()
}

// The network deregisters the UE: DEREGISTRATION REQUEST goes to the
// UE on its event stream, which answers (TS 24.501 5.5.2.3).
func TestNetworkInitiatedDeregistration(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	resp, err := http.Post(n.api+"/network-initiated/ues/"+imsi1+"/deregistration", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("deregister = %d", resp.StatusCode)
	}
	m := u.next()
	req, ok := m.Message.(*fivegc.DeregistrationRequest)
	if m.Type != fivegc.TypeDeregistrationRequest || !ok || req.DeregistrationType.ReRegistrationRequired || req.DeregistrationType.AccessType != fivegc.Access3GPP {
		t.Fatalf("network message = %s %+v", m.Type, m.Message)
	}
	if err := u.client.AcceptDeregistration(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := n.stub.Status(imsi1); !errors.Is(err, fivegc.ErrNotRegistered) {
		t.Errorf("status after deregistration: %v", err)
	}
}

// TS 24.501 5.5.1.2.8 f: an initial registration from a registered UE
// deletes its old context, and the PDU session with it. TS 24.501
// 6.4.1.7 c: an initial request for the same PDU session ID replaces
// the old session.
func TestRegisteringAgainStartsAfresh(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	u.establish()
	u.register()
	if st, err := n.stub.Status(imsi1); err != nil || st.PDUSession != nil {
		t.Errorf("status after registering again = %+v, %v", st, err)
	}
}
