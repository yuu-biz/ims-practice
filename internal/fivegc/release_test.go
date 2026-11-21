package fivegc_test

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/fivegc"
	"github.com/yuu-biz/ims-practice/internal/n5"
)

func request(t *testing.T, n *network, method, path string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, n.api+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// The whole VoNR flow in one process: the UE registers and sets up its
// IMS PDU session, the P-CSCF creates the contexts of the signalling
// path and of a call over N5 on h2c, the voice QoS flow appears, the
// network releases it, the P-CSCF is asked to delete the call's context
// and does. The context of the signalling path stays.
func TestVoNRCallLosesItsVoiceQoSFlow(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	if !u.register().FiveGSNetworkFeatureSupport.IMSVoPS3GPP {
		t.Fatal("no IMS voice over PS session")
	}
	accept := u.establish()
	if got := accept.ExtendedProtocolConfigurationOptions.PCSCFAddresses(); len(got) == 0 || got[0] != pcscfIPv4 {
		t.Fatalf("P-CSCF addresses = %v", got)
	}
	af := n.af()
	sig := af.create(af.signalling(accept.PDUAddress.IPv4Address.String()))
	call := af.create(af.call(n5.SuccessfulResourcesAllocation, n5.FailedResourcesAllocation))

	cmd := u.modificationCommand()
	qfi := cmd.AuthorizedQoSFlowDescriptions[0].QFI
	u.completeModification()
	if e := af.notification(); e.EvNotifs[0].Event != n5.SuccessfulResourcesAllocation || e.EvSubsUri != call.URI+n5.EventsSubscriptionPath {
		t.Fatalf("notification = %+v", e)
	}
	st, _ := n.stub.Status(imsi1)
	if f := st.PDUSession.QoSFlows; len(f) != 2 || f[1].QFI != qfi || f[1].FiveQI != fivegc.FiveQIConversationalVoice {
		t.Fatalf("QoS flows = %+v", f)
	}

	if code := request(t, n, http.MethodDelete, "/network-initiated/ues/"+imsi1+"/pdu-sessions/1/qos-flows/2"); code != http.StatusNoContent {
		t.Fatalf("release of the QoS flow = %d", code)
	}
	cmd = u.modificationCommand()
	if len(cmd.AuthorizedQoSFlowDescriptions) != 1 || cmd.AuthorizedQoSFlowDescriptions[0] != (fivegc.QoSFlowDescription{QFI: qfi, OperationCode: fivegc.DeleteExistingQoSFlowDescription}) {
		t.Errorf("command = %+v", cmd)
	}
	u.completeModification()
	if term := af.termination(); term != (n5.TerminationInfo{TermCause: n5.AllSDFDeactivation, ResUri: call.URI}) {
		t.Errorf("termination = %+v", term)
	}
	if _, err := af.client.Delete(t.Context(), call.URI, nil); err != nil {
		t.Fatalf("delete after the termination: %v", err)
	}
	af.quiet()
	u.nothingMore()

	st, _ = n.stub.Status(imsi1)
	if st.PDUSession.State != fivegc.PDUSessionActive || len(st.PDUSession.QoSFlows) != 1 ||
		len(st.PDUSession.AppSessions) != 1 || st.PDUSession.AppSessions[0].ResourceURI != sig.URI {
		t.Errorf("status after the release = %+v", st.PDUSession)
	}
}

// The network releases the PDU session: the UE hears PDU SESSION RELEASE
// COMMAND with no PTI, and every context is asked to go with
// PDU_SESSION_TERMINATION.
func TestNetworkReleasesThePDUSession(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	af := n.af()
	sig := af.create(af.signalling(ue1Addr.String()))

	if code := request(t, n, http.MethodDelete, "/network-initiated/ues/"+imsi1+"/pdu-sessions/1"); code != http.StatusNoContent {
		t.Fatalf("release of the PDU session = %d", code)
	}
	m := u.next()
	c, ok := m.Message.(*fivegc.DLNASTransport[fivegc.PDUSessionReleaseCommand])
	want := fivegc.PDUSessionReleaseCommand{PDUSessionID: 1, PTI: 0, FiveGSMCause: fivegc.RegularDeactivation}
	if m.Type != fivegc.TypePDUSessionReleaseCommand || !ok || !reflect.DeepEqual(c.PayloadContainer, want) {
		t.Fatalf("network message = %s %+v", m.Type, m.Message)
	}
	if err := u.client.CompletePDUSessionRelease(t.Context(), fivegc.NewULNASTransport(1, fivegc.PDUSessionReleaseComplete{PDUSessionID: 1})); err != nil {
		t.Fatal(err)
	}
	if term := af.termination(); term != (n5.TerminationInfo{TermCause: n5.PDUSessionTermination, ResUri: sig.URI}) {
		t.Errorf("termination = %+v", term)
	}
	if _, err := af.client.Delete(t.Context(), sig.URI, nil); err != nil {
		t.Errorf("delete after the termination: %v", err)
	}
}

func TestStatusAndNetworkInitiatedErrors(t *testing.T) {
	n := newNetwork(t)
	u := n.ue(imsi1)
	u.register()
	u.establish()
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodDelete, "/network-initiated/ues/" + imsi1 + "/pdu-sessions/1/qos-flows/1", http.StatusBadRequest},
		{http.MethodDelete, "/network-initiated/ues/" + imsi1 + "/pdu-sessions/1/qos-flows/2", http.StatusNotFound},
		{http.MethodDelete, "/network-initiated/ues/" + imsi1 + "/pdu-sessions/1/qos-flows/x", http.StatusBadRequest},
		{http.MethodDelete, "/network-initiated/ues/" + imsi1 + "/pdu-sessions/2", http.StatusConflict},
		{http.MethodDelete, "/network-initiated/ues/" + imsi2 + "/pdu-sessions/1", http.StatusConflict},
		{http.MethodGet, "/ues/" + imsi2, http.StatusConflict},
		{http.MethodGet, "/ues/001010000000099", http.StatusNotFound},
		{http.MethodPost, "/network-initiated/ues/" + imsi2 + "/deregistration", http.StatusConflict},
	} {
		if got := request(t, n, c.method, c.path); got != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}
	u.nothingMore()
}
