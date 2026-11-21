package fivegc_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
	"github.com/yuu-biz/ims-practice/internal/n5"
)

// serveH2C serves h with cleartext HTTP/2 on a loopback port and
// returns its http URL.
func serveH2C(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := n5.NewServer("", h)
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return "http://" + ln.Addr().String()
}

// fakeAF is a P-CSCF that creates contexts with n5.Client over h2c and
// keeps the notifications of the PCF.
type fakeAF struct {
	t        *testing.T
	client   *n5.Client
	notifURI string
	notified chan n5.EventsNotification
	ended    chan n5.TerminationInfo
}

func (n *network) af() *fakeAF {
	n.t.Helper()
	af := &fakeAF{t: n.t, notified: make(chan n5.EventsNotification, 16), ended: make(chan n5.TerminationInfo, 16)}
	mux := http.NewServeMux()
	mux.Handle("/n5-callbacks/", n5.NewNotificationHandler(af))
	af.notifURI = serveH2C(n.t, mux) + "/n5-callbacks/ue-1"
	af.client = n5.NewClient(serveH2C(n.t, n.stub.N5Handler()))
	return af
}

func (af *fakeAF) Notify(ctx context.Context, e n5.EventsNotification) error {
	af.notified <- e
	return nil
}

func (af *fakeAF) Terminate(ctx context.Context, t n5.TerminationInfo) error {
	af.ended <- t
	return nil
}

func (af *fakeAF) notification() n5.EventsNotification {
	af.t.Helper()
	select {
	case e := <-af.notified:
		return e
	case <-time.After(5 * time.Second):
		af.t.Fatal("no notification from the PCF")
	}
	return n5.EventsNotification{}
}

func (af *fakeAF) termination() n5.TerminationInfo {
	af.t.Helper()
	select {
	case e := <-af.ended:
		return e
	case <-time.After(5 * time.Second):
		af.t.Fatal("no termination request from the PCF")
	}
	return n5.TerminationInfo{}
}

func (af *fakeAF) quiet() {
	af.t.Helper()
	select {
	case e := <-af.notified:
		af.t.Fatalf("unexpected notification %+v", e)
	case e := <-af.ended:
		af.t.Fatalf("unexpected termination request %+v", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func (af *fakeAF) create(req n5.AppSessionContextReqData) n5.AppSession {
	af.t.Helper()
	s, err := af.client.Create(af.t.Context(), req)
	if err != nil {
		af.t.Fatal(err)
	}
	return s
}

func problemCause(err error) (int, string) {
	var p *n5.ProblemError
	if errors.As(err, &p) {
		return p.Status, p.Problem.Cause
	}
	return 0, ""
}

// signalling is the context of the signalling path status (TS 29.514
// 4.2.6.7).
func (af *fakeAF) signalling(ueIpv4 string) n5.AppSessionContextReqData {
	return n5.AppSessionContextReqData{
		EvSubsc: &n5.EventsSubscReqData{Events: []n5.AfEventSubscription{{Event: n5.FailedResourcesAllocation}}, NotifUri: af.notifURI},
		MedComponents: map[string]n5.MediaComponent{"0": {
			MedCompN:    0,
			MedSubComps: map[string]n5.MediaSubComponent{"0": {FNum: 0, FlowUsage: n5.FlowUsageAFSignalling}},
		}},
		NotifUri: af.notifURI,
		SuppFeat: n5.NewSupportedFeatures(n5.IMSSBI, n5.ProvAFsignalFlow, n5.PatchCorrection),
		UeIpv4:   ueIpv4,
	}
}

// call is the service information of an AMR-WB call of the UE at
// 172.28.0.110, port 49152, through the media relay at 172.28.0.50,
// port 40000, as the P-CSCF derives it from the SDP (TS 29.513 7.2.3).
func (af *fakeAF) call(events ...n5.AfEvent) n5.AppSessionContextReqData {
	var subsc []n5.AfEventSubscription
	for _, e := range events {
		subsc = append(subsc, n5.AfEventSubscription{Event: e})
	}
	return n5.AppSessionContextReqData{
		AfAppId: "urn:urn-7:3gpp-service.ims.icsi.mmtel",
		EvSubsc: &n5.EventsSubscReqData{Events: subsc, NotifUri: af.notifURI},
		MedComponents: map[string]n5.MediaComponent{"1": {
			MedCompN: 1, MedType: n5.MediaTypeAudio, FStatus: n5.FlowStatusEnabled,
			MarBwUl: "38 Kbps", MarBwDl: "38 Kbps", RsBw: "800 bps", RrBw: "600 bps",
			MedSubComps: map[string]n5.MediaSubComponent{
				"1": {FNum: 1, FDescs: []n5.FlowDescription{
					"permit in 17 from 172.28.0.110 49152 to 172.28.0.50 40000",
					"permit out 17 from 172.28.0.50 40000 to 172.28.0.110 49152",
				}},
				"2": {FNum: 2, FlowUsage: n5.FlowUsageRTCP, FDescs: []n5.FlowDescription{
					"permit in 17 from 172.28.0.110 49153 to 172.28.0.50 40001",
					"permit out 17 from 172.28.0.50 40001 to 172.28.0.110 49153",
				}},
			},
		}},
		ServInfStatus: n5.ServiceInfoStatusFinal,
		NotifUri:      af.notifURI,
		SuppFeat:      n5.NewSupportedFeatures(n5.IMSSBI, n5.PatchCorrection),
		UeIpv4:        ue1Addr.String(),
	}
}

// modificationCommand waits for the PDU SESSION MODIFICATION COMMAND that adds
// or removes the voice QoS flow.
func (u *ue) modificationCommand() fivegc.PDUSessionModificationCommand {
	u.t.Helper()
	m := u.next()
	c, ok := m.Message.(*fivegc.DLNASTransport[fivegc.PDUSessionModificationCommand])
	if m.Type != fivegc.TypePDUSessionModificationCommand || !ok {
		u.t.Fatalf("network message = %s %+v", m.Type, m.Message)
	}
	return c.PayloadContainer
}

func (u *ue) completeModification() {
	u.t.Helper()
	m := fivegc.NewULNASTransport(1, fivegc.PDUSessionModificationComplete{PDUSessionID: 1})
	if err := u.client.CompletePDUSessionModification(u.t.Context(), m); err != nil {
		u.t.Fatal(err)
	}
}

// The P-CSCF subscribes to the signalling path status of a registered
// UE. The PCF binds the context to the PDU session; its flows are on
// the default QoS flow, so the UE hears nothing.
func TestSignallingPathContextBindsToThePDUSession(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()

	s := af.create(af.signalling(ue1Addr.String()))
	if !strings.Contains(s.URI, n5.AppSessionsPath+"/") {
		t.Errorf("resource URI = %s", s.URI)
	}
	// TS 29.514 5.8: the PCF answers with the features both support.
	if got, want := s.Context.AscRespData.SuppFeat, n5.NewSupportedFeatures(n5.IMSSBI, n5.PatchCorrection); got != want {
		t.Errorf("supported features = %q, want %q", got, want)
	}
	u.nothingMore()
	st, _ := n.stub.Status(imsi1)
	if len(st.PDUSession.AppSessions) != 1 || len(st.PDUSession.QoSFlows) != 1 {
		t.Errorf("status = %+v", st.PDUSession)
	}
	if _, err := af.client.Delete(t.Context(), s.URI, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := af.client.Delete(t.Context(), s.URI, nil); !isCause(err, http.StatusNotFound, n5.CauseApplicationSessionContextNotFound) {
		t.Errorf("second delete: %v", err)
	}
	af.quiet()
}

func mustPrefix(s string) netip.Prefix { return netip.MustParsePrefix(s) }

func isCause(err error, status int, cause string) bool {
	s, c := problemCause(err)
	return s == status && c == cause
}

// TS 29.514 4.2.2.2: a UE with no PDU session, such as a softphone on
// the host, fails session binding.
func TestSessionBindingFailsWithoutAPDUSession(t *testing.T) {
	n := newNetwork(t)
	af := n.af()
	_, err := af.client.Create(t.Context(), af.signalling("172.28.0.111"))
	if !isCause(err, http.StatusInternalServerError, n5.CausePDUSessionNotAvailable) {
		t.Errorf("create: %v, want 500 PDU_SESSION_NOT_AVAILABLE", err)
	}
}

func TestRefusedServiceInformation(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()

	video := af.call()
	c := video.MedComponents["1"]
	c.MedType = n5.MediaTypeVideo
	video.MedComponents["1"] = c
	if _, err := af.client.Create(t.Context(), video); !isCause(err, http.StatusForbidden, n5.CauseRequestedServiceNotAuthorized) {
		t.Errorf("video: %v, want 403 REQUESTED_SERVICE_NOT_AUTHORIZED", err)
	}
	ranges := af.call()
	ranges.MedComponents["1"].MedSubComps["1"] = n5.MediaSubComponent{FNum: 1, FDescs: []n5.FlowDescription{"permit in 17 from any to any 5000-5010"}}
	if _, err := af.client.Create(t.Context(), ranges); !isCause(err, http.StatusBadRequest, n5.CauseFilterRestrictions) {
		t.Errorf("port range: %v, want 400 FILTER_RESTRICTIONS", err)
	}
	u.nothingMore()
}

// The PCF authorises the audio of a call with 5QI 1 (TS 29.513 Table
// 7.3.3-1), and the SMF adds the voice QoS flow with PDU SESSION
// MODIFICATION COMMAND. When the UE completes it, the AF hears that the
// resources are allocated (TS 29.514 4.2.5.8). When the AF deletes the
// context, the flow goes.
func TestCallAddsTheVoiceQoSFlow(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()

	s := af.create(af.call(n5.SuccessfulResourcesAllocation, n5.FailedResourcesAllocation))
	cmd := u.modificationCommand()
	// RTP 38 kbit/s and RTCP rs+rr 1.4 kbit/s each way, rounded up.
	wantFlow := fivegc.QoSFlowDescription{QFI: 2, OperationCode: fivegc.CreateNewQoSFlowDescription, FiveQI: fivegc.FiveQIConversationalVoice,
		GFBRUplink: 40, GFBRDownlink: 40, MFBRUplink: 40, MFBRDownlink: 40}
	if !reflect.DeepEqual(cmd.AuthorizedQoSFlowDescriptions, []fivegc.QoSFlowDescription{wantFlow}) {
		t.Errorf("QoS flow descriptions = %+v", cmd.AuthorizedQoSFlowDescriptions)
	}
	if len(cmd.AuthorizedQoSRules) != 1 {
		t.Fatalf("QoS rules = %+v", cmd.AuthorizedQoSRules)
	}
	rule := cmd.AuthorizedQoSRules[0]
	if rule.QoSRuleIdentifier != 2 || rule.RuleOperationCode != fivegc.CreateNewQoSRule || rule.DQR || rule.QFI != 2 || rule.QoSRulePrecedence != 1 {
		t.Errorf("QoS rule = %+v", rule)
	}
	wantFilters := []fivegc.PacketFilter{
		{Direction: fivegc.UplinkOnly, Identifier: 1, IPv4RemoteAddress: mustPrefix("172.28.0.50/32"), ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000},
		{Direction: fivegc.DownlinkOnly, Identifier: 2, IPv4RemoteAddress: mustPrefix("172.28.0.50/32"), ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49152, SingleRemotePort: 40000},
		{Direction: fivegc.UplinkOnly, Identifier: 3, IPv4RemoteAddress: mustPrefix("172.28.0.50/32"), ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49153, SingleRemotePort: 40001},
		{Direction: fivegc.DownlinkOnly, Identifier: 4, IPv4RemoteAddress: mustPrefix("172.28.0.50/32"), ProtocolIdentifierNextHeader: 17, SingleLocalPort: 49153, SingleRemotePort: 40001},
	}
	if !reflect.DeepEqual(rule.PacketFilters, wantFilters) {
		t.Errorf("packet filters =\n%+v\nwant\n%+v", rule.PacketFilters, wantFilters)
	}
	if st, _ := n.stub.Status(imsi1); st.PDUSession.State != fivegc.PDUSessionModificationPending || len(st.PDUSession.QoSFlows) != 2 {
		t.Errorf("status before the answer = %+v", st.PDUSession)
	}
	af.quiet()

	u.completeModification()
	e := af.notification()
	if e.EvSubsUri != s.URI+n5.EventsSubscriptionPath || len(e.EvNotifs) != 1 || e.EvNotifs[0].Event != n5.SuccessfulResourcesAllocation {
		t.Errorf("notification = %+v", e)
	}
	if st, _ := n.stub.Status(imsi1); st.PDUSession.State != fivegc.PDUSessionActive {
		t.Errorf("status after the answer = %+v", st.PDUSession)
	}

	if _, err := af.client.Delete(t.Context(), s.URI, nil); err != nil {
		t.Fatal(err)
	}
	cmd = u.modificationCommand()
	wantDelete := fivegc.PDUSessionModificationCommand{PDUSessionID: 1,
		AuthorizedQoSRules:            []fivegc.QoSRule{{QoSRuleIdentifier: 2, RuleOperationCode: fivegc.DeleteExistingQoSRule}},
		AuthorizedQoSFlowDescriptions: []fivegc.QoSFlowDescription{{QFI: 2, OperationCode: fivegc.DeleteExistingQoSFlowDescription}}}
	if !reflect.DeepEqual(cmd, wantDelete) {
		t.Errorf("command after the delete = %+v", cmd)
	}
	u.completeModification()
	if st, _ := n.stub.Status(imsi1); len(st.PDUSession.QoSFlows) != 1 || len(st.PDUSession.AppSessions) != 0 {
		t.Errorf("status after the delete = %+v", st.PDUSession)
	}
	af.quiet()
}

// A second call while the voice QoS flow is up rides on it: no new
// command, and the resources are there at once. An update of the first
// call changes the record of the flow only.
func TestSecondCallSharesTheVoiceQoSFlow(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()
	first := af.create(af.call(n5.SuccessfulResourcesAllocation))
	u.modificationCommand()
	u.completeModification()
	af.notification()

	second := af.call(n5.SuccessfulResourcesAllocation)
	second.MedComponents["1"].MedSubComps["1"] = n5.MediaSubComponent{FNum: 1, FDescs: []n5.FlowDescription{
		"permit in 17 from 172.28.0.110 49154 to 172.28.0.50 40002",
		"permit out 17 from 172.28.0.50 40002 to 172.28.0.110 49154",
	}}
	delete(second.MedComponents["1"].MedSubComps, "2")
	af.create(second)
	if e := af.notification(); e.EvNotifs[0].Event != n5.SuccessfulResourcesAllocation {
		t.Errorf("notification = %+v", e)
	}
	u.nothingMore()
	st, _ := n.stub.Status(imsi1)
	if f := st.PDUSession.QoSFlows[1]; len(f.PacketFilters) != 6 || f.MFBRUplink != 78 {
		t.Errorf("voice QoS flow with two calls = %+v", f)
	}

	update := n5.AppSessionContextUpdateData{MedComponents: map[string]n5.MediaComponentRm{"1": func() n5.MediaComponent {
		c := af.call().MedComponents["1"]
		c.MarBwUl, c.MarBwDl = "64 Kbps", "64 Kbps"
		return c
	}()}}
	if _, err := af.client.Update(t.Context(), first.URI, update); err != nil {
		t.Fatal(err)
	}
	st, _ = n.stub.Status(imsi1)
	if f := st.PDUSession.QoSFlows[1]; f.MFBRUplink != 104 {
		t.Errorf("voice QoS flow after the update = %+v", f)
	}
	u.nothingMore()
}

// The UE rejects the voice QoS flow. The AF hears that the resources
// could not be allocated, and, as none of its flows have any, is asked
// to delete the context (TS 29.514 4.2.5.8, 4.2.5.3).
func TestRejectedVoiceQoSFlowEndsTheContext(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()
	sig := af.create(af.signalling(ue1Addr.String()))
	s := af.create(af.call(n5.SuccessfulResourcesAllocation, n5.FailedResourcesAllocation))
	u.modificationCommand()

	reject := fivegc.NewULNASTransport(1, fivegc.PDUSessionModificationCommandReject{PDUSessionID: 1, FiveGSMCause: fivegc.InsufficientResources})
	if err := u.client.RejectPDUSessionModification(t.Context(), reject); err != nil {
		t.Fatal(err)
	}
	e := af.notification()
	if e.EvSubsUri != s.URI+n5.EventsSubscriptionPath || e.EvNotifs[0].Event != n5.FailedResourcesAllocation ||
		len(e.FailedResourcAllocReports) != 1 || e.FailedResourcAllocReports[0].McResourcStatus != n5.MediaComponentResourcesInactive {
		t.Errorf("notification = %+v", e)
	}
	if term := af.termination(); term != (n5.TerminationInfo{TermCause: n5.AllSDFDeactivation, ResUri: s.URI}) {
		t.Errorf("termination = %+v", term)
	}
	if _, err := af.client.Update(t.Context(), s.URI, n5.AppSessionContextUpdateData{}); !isCause(err, http.StatusNotFound, n5.CauseApplicationSessionContextNotFound) {
		t.Errorf("update after the termination: %v", err)
	}
	if _, err := af.client.Delete(t.Context(), s.URI, nil); err != nil {
		t.Errorf("delete after the termination: %v", err)
	}
	st, _ := n.stub.Status(imsi1)
	if len(st.PDUSession.QoSFlows) != 1 || len(st.PDUSession.AppSessions) != 1 || st.PDUSession.AppSessions[0].ResourceURI != sig.URI {
		t.Errorf("status = %+v", st.PDUSession)
	}
	af.quiet()
	u.nothingMore()
}

// A UE with no event stream cannot take the voice QoS flow, which fails
// as if the UE had rejected it.
func TestUnreachableUEFailsTheVoiceQoSFlow(t *testing.T) {
	n := newNetwork(t)
	c := fivegc.NewClient(n.api, imsi1)
	if _, err := c.Register(t.Context(), fivegc.RegistrationRequest{FiveGSRegistrationType: fivegc.InitialRegistration}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.EstablishPDUSession(t.Context(), establishment(fivegc.IPv4)); err != nil {
		t.Fatal(err)
	}
	af := n.af()
	s := af.create(af.call(n5.FailedResourcesAllocation))
	if e := af.notification(); e.EvNotifs[0].Event != n5.FailedResourcesAllocation {
		t.Errorf("notification = %+v", e)
	}
	if term := af.termination(); term.TermCause != n5.AllSDFDeactivation || term.ResUri != s.URI {
		t.Errorf("termination = %+v", term)
	}
}

// When the PDU session ends, every context bound to it is asked to go,
// with PDU_SESSION_TERMINATION (TS 29.514 4.2.5.3).
func TestPDUSessionReleaseTerminatesTheContexts(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()
	sig := af.create(af.signalling(ue1Addr.String()))
	call := af.create(af.call())
	u.modificationCommand()
	u.completeModification()

	if _, err := u.client.ReleasePDUSession(t.Context(), fivegc.NewULNASTransport(1, fivegc.PDUSessionReleaseRequest{PDUSessionID: 1, PTI: 5})); err != nil {
		t.Fatal(err)
	}
	got := map[string]n5.TerminationCause{}
	for range 2 {
		term := af.termination()
		got[term.ResUri] = term.TermCause
	}
	want := map[string]n5.TerminationCause{sig.URI: n5.PDUSessionTermination, call.URI: n5.PDUSessionTermination}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("terminations = %v, want %v", got, want)
	}
	for _, uri := range []string{sig.URI, call.URI} {
		if _, err := af.client.Delete(t.Context(), uri, nil); err != nil {
			t.Errorf("delete %s: %v", uri, err)
		}
	}
	af.quiet()
}
