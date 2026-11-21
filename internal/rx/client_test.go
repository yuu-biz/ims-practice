package rx_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
)

// These tests run the client against the PCRF of package epc over a
// Diameter connection, with a UE attached to the EPC stub.

const (
	realm = "ims.mnc001.mcc001.3gppnetwork.org"
	imsi  = "001010000000001"
)

var (
	ueAddr = netip.MustParseAddr("172.28.0.110")
	timers = diameter.Timers{Watchdog: time.Minute, Reconnect: 50 * time.Millisecond, Answer: 2 * time.Second}
)

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

type setup struct {
	t      *testing.T
	client *rx.Client
	events chan policy.Event
	api    string
	ue     <-chan epc.NetworkMessage
	ueAPI  *epc.Client
}

func newSetup(t *testing.T) *setup {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	pcrfID := diameter.Identity{OriginHost: "pcrf." + realm, OriginRealm: realm}
	pcrfNode := diameter.NewNode(pcrfID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	stub := epc.New(epc.Config{APN: "ims", Subscribers: []epc.Subscriber{{IMSI: imsi, Address: ueAddr}}}, epc.NewMemoryStore(), pcrfNode, quiet())
	pcrfNode.Handle(stub.HandleRx)
	server, err := pcrfNode.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	stub.SetAFs(server)
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)

	s := &setup{t: t, events: make(chan policy.Event, 8), api: api.URL}
	afNode := diameter.NewNode(diameter.Identity{OriginHost: "pcscf." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	var client *rx.Client
	afNode.Handle(func(m *diam.Message) *diam.Message { return client.Handle(m) })
	peer := afNode.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: pcrfID.OriginHost})
	t.Cleanup(peer.Stop)
	client = rx.NewClient(afNode, peer, realm, func(e policy.Event) { s.events <- e }, quiet())
	s.client = client
	waitOpen(t, peer)

	s.ueAPI = epc.NewClient(api.URL, imsi)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if s.ue, err = s.ueAPI.Events(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ueAPI.Attach(t.Context(), epc.AttachRequest{ESMMessageContainer: epc.PDNConnectivityRequest{
		RequestType: epc.InitialRequest, PDNType: epc.IPv4, AccessPointName: "ims"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

// waitOpen waits until the peer takes requests: an STR for no session
// gets its answer once the connection is open.
func waitOpen(t *testing.T, peer *diameter.Peer) {
	t.Helper()
	probe := rx.NewClient(diameter.NewNode(diameter.Identity{OriginHost: "probe", OriginRealm: realm}, nil, timers, quiet()), peer, realm, nil, quiet())
	deadline := time.Now().Add(5 * time.Second)
	for probe.Release(t.Context(), "probe;0;0") != nil {
		if time.Now().After(deadline) {
			t.Fatal("the connection to the PCRF did not open")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *setup) nextEvent() policy.Event {
	s.t.Helper()
	select {
	case e := <-s.events:
		return e
	case <-time.After(5 * time.Second):
		s.t.Fatal("no event from the PCRF")
		return policy.Event{}
	}
}

func (s *setup) nextUE() epc.NetworkMessage {
	s.t.Helper()
	select {
	case m := <-s.ue:
		return m
	case <-time.After(5 * time.Second):
		s.t.Fatal("no message to the UE")
		return epc.NetworkMessage{}
	}
}

func (s *setup) rxSessions() []string {
	s.t.Helper()
	resp, err := http.Get(s.api + "/ues/" + imsi)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	var st epc.Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		s.t.Fatal(err)
	}
	return st.RxSessions
}

func sdpOf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n") }

func call(media string) policy.OfferAnswer {
	return policy.OfferAnswer{
		Offer: sdpOf("v=0", "o=- 1 1 IN IP4 172.28.0.110", "s=-", "c=IN IP4 172.28.0.110", "t=0 0",
			"m="+media+" 49000 RTP/AVP 0", "b=AS:80", "a=rtpmap:0 PCMU/8000"),
		Answer: sdpOf("v=0", "o=- 2 1 IN IP4 172.28.0.50", "s=-", "c=IN IP4 172.28.0.50", "t=0 0",
			"m="+media+" 40000 RTP/AVP 0", "b=AS:80", "a=rtpmap:0 PCMU/8000"),
		UEOffered: true,
	}
}

func TestSignallingPathSubscriptionAndRelease(t *testing.T) {
	s := newSetup(t)
	id, err := s.client.SubscribeSignallingPath(t.Context(), ueAddr)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.rxSessions(); len(got) != 1 || got[0] != string(id) {
		t.Errorf("Rx sessions at the PCRF = %v, want [%s]", got, id)
	}
	if err := s.client.Release(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if got := s.rxSessions(); len(got) != 0 {
		t.Errorf("Rx sessions after the release = %v", got)
	}
	// Releasing again is not an error: the P-CSCF may release on every
	// BYE.
	if err := s.client.Release(t.Context(), id); err != nil {
		t.Errorf("second release: %v", err)
	}
}

func TestMediaOfACallFromSDPToTheBearerAndBack(t *testing.T) {
	s := newSetup(t)
	id, err := s.client.AuthorizeMedia(t.Context(), policy.MediaRequest{UE: ueAddr, SDP: call("audio"),
		ApplicationID: "urn:urn-7:3gpp-service.ims.icsi.mmtel",
		Subscribe:     []policy.EventKind{policy.ReleaseOfBearer, policy.SuccessfulResourcesAllocation}})
	if err != nil {
		t.Fatal(err)
	}
	m := s.nextUE()
	req, ok := m.Message.(*epc.ActivateDedicatedEPSBearerContextRequest)
	if !ok || req.EPSQoS.QCI != epc.QCIConversationalVoice {
		t.Fatalf("UE got %s %+v, want the voice bearer", m.Type, m.Message)
	}
	if err := s.ueAPI.AcceptDedicatedBearer(t.Context(), epc.ActivateDedicatedEPSBearerContextAccept{EPSBearerIdentity: req.EPSBearerIdentity}); err != nil {
		t.Fatal(err)
	}
	if e := s.nextEvent(); e != (policy.Event{Session: id, Kind: policy.SuccessfulResourcesAllocation}) {
		t.Errorf("event %+v, want the resources of %s", e, id)
	}
	// An UPDATE renegotiates the media on the same session (TS 29.214
	// 4.4.2).
	again, err := s.client.AuthorizeMedia(t.Context(), policy.MediaRequest{Session: id, UE: ueAddr, SDP: call("audio")})
	if err != nil || again != id {
		t.Errorf("update = %q, %v; want %q", again, err, id)
	}
	if err := s.client.Release(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if m := s.nextUE(); m.Type != epc.TypeDeactivateEPSBearerContextRequest {
		t.Errorf("UE got %s, want the deactivation of the voice bearer", m.Type)
	}
}

// When the network releases the voice bearer the PCRF aborts the
// session; the client answers, ends the session with an STR, and then
// tells the P-CSCF.
func TestReleasedBearerTerminatesTheSession(t *testing.T) {
	s := newSetup(t)
	id, err := s.client.AuthorizeMedia(t.Context(), policy.MediaRequest{UE: ueAddr, SDP: call("audio"),
		Subscribe: []policy.EventKind{policy.ReleaseOfBearer}})
	if err != nil {
		t.Fatal(err)
	}
	req := s.nextUE().Message.(*epc.ActivateDedicatedEPSBearerContextRequest)
	httpReq, _ := http.NewRequest(http.MethodDelete, s.api+"/network-initiated/ues/"+imsi+"/bearers/"+strconv.Itoa(int(req.EPSBearerIdentity)), nil)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("release: %v %v", resp, err)
	}
	resp.Body.Close()
	if e := s.nextEvent(); e != (policy.Event{Session: id, Kind: policy.SessionTerminated}) {
		t.Errorf("event %+v, want %s terminated", e, id)
	}
	if got := s.rxSessions(); len(got) != 0 {
		t.Errorf("Rx sessions after the ASR = %v, want none: the client sends the STR", got)
	}
}

func TestErrorsThePCSCFActsOn(t *testing.T) {
	s := newSetup(t)
	// A UE that does not come through the EPC, such as a softphone on the
	// host.
	if _, err := s.client.SubscribeSignallingPath(t.Context(), netip.MustParseAddr("172.28.0.1")); !errors.Is(err, policy.ErrIPCANSessionNotAvailable) {
		t.Errorf("unknown UE: %v, want ErrIPCANSessionNotAvailable", err)
	}
	if _, err := s.client.AuthorizeMedia(t.Context(), policy.MediaRequest{UE: ueAddr, SDP: call("video")}); !errors.Is(err, policy.ErrRequestedServiceNotAuthorized) {
		t.Errorf("video: %v, want ErrRequestedServiceNotAuthorized", err)
	}
}
