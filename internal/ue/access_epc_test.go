package ue_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

const imsi1 = "001010000000001"

var (
	ue1Addr = netip.MustParseAddr("127.0.0.1")
	pcscfIP = netip.MustParseAddr("192.0.2.40")
)

// epcStub is the EPC stub of role epc on an httptest server, with a
// Diameter node in front of its PCRF that plays the P-CSCF, so that a
// test can have the network set up a voice bearer.
type epcStub struct {
	t   *testing.T
	api string
	af  *diameter.Node
	rx  *diameter.Peer
}

func newEPCStub(t *testing.T) *epcStub {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	pcrfID := diameter.Identity{OriginHost: "pcrf." + domain, OriginRealm: domain}
	node := diameter.NewNode(pcrfID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	stub := epc.New(epc.Config{
		APN:            ue.IMSAPN,
		PCSCFAddresses: []netip.Addr{pcscfIP},
		Subscribers:    []epc.Subscriber{{IMSI: imsi1, Address: ue1Addr}},
		Cell:           stubCell,
	}, epc.NewMemoryStore(), node, quiet())
	node.Handle(stub.HandleRx)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	stub.SetAFs(server)
	api := httptest.NewServer(stub.Handler())
	t.Cleanup(api.Close)

	afID := diameter.Identity{OriginHost: "pcscf." + domain, OriginRealm: domain}
	af := diameter.NewNode(afID, []uint32{diameter.ApplicationIDRx}, timers, quiet())
	af.Handle(func(req *diam.Message) *diam.Message {
		if req.Header.CommandCode == rx.CommandAbortSession {
			return rx.AbortSessionAnswer{Result: cx.Base(rx.DiameterSuccess)}.Message(req, afID)
		}
		return rx.ReAuthAnswer{Result: cx.Base(rx.DiameterSuccess)}.Message(req, afID)
	})
	peer := af.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: pcrfID.OriginHost})
	t.Cleanup(peer.Stop)
	return &epcStub{t: t, api: api.URL, af: af, rx: peer}
}

// authorizeCall sends the AAR with which a P-CSCF has the PCRF authorise
// the media of a call of UE 1 (TS 29.214 4.4.1); the stub then sets up
// the voice bearer.
func (s *epcStub) authorizeCall() {
	s.t.Helper()
	offer := "v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio 49000 RTP/AVP 0\r\nb=AS:80\r\na=rtpmap:0 PCMU/8000\r\n"
	answer := strings.ReplaceAll(offer, "49000", "40000")
	components, err := policy.MediaComponents(policy.OfferAnswer{Offer: []byte(offer), Answer: []byte(answer), UEOffered: true})
	if err != nil {
		s.t.Fatal(err)
	}
	aar := rx.AARequest{Header: cx.NewHeader(s.af, "", domain), MediaComponents: components, FramedIPAddress: ue1Addr,
		SpecificActions: []rx.SpecificAction{rx.IndicationOfReleaseOfBearer}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ans, err := s.rx.Request(context.Background(), aar.Message())
		if err == nil {
			aaa, err := rx.ParseAAAnswer(ans)
			if err != nil || aaa.Result != cx.Base(rx.DiameterSuccess) {
				s.t.Fatalf("AAA %+v, %v", aaa.Result, err)
			}
			return
		}
		if !errors.Is(err, diameter.ErrPeerUnavailable) || time.Now().After(deadline) {
			s.t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *epcStub) networkInitiated(method, path string) {
	s.t.Helper()
	req, _ := http.NewRequest(method, s.api+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		s.t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
	}
}

func (s *epcStub) bearers() []epc.BearerStatus {
	s.t.Helper()
	resp, err := http.Get(s.api + "/ues/" + imsi1)
	if err != nil {
		s.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var st epc.Status
	decodeBody(s.t, resp, &st)
	return st.Bearers
}

func nextAccessEvent(t *testing.T, events <-chan ue.AccessEvent) (ue.AccessEvent, bool) {
	t.Helper()
	select {
	case e, ok := <-events:
		return e, ok
	case <-time.After(5 * time.Second):
		t.Fatal("no event from the access network")
		return 0, false
	}
}

// TestAttachToTheEPCStub is TS 23.401 5.3.2.1 with P-CSCF discovery by
// the PCO (TS 24.229 L.2.2.1): the UE gets its address, the P-CSCF and
// the cell for P-Access-Network-Info; the voice bearer that the network
// sets up for a call is accepted and reported, and so is its release;
// a detach by the network ends the events.
func TestAttachToTheEPCStub(t *testing.T) {
	stub := newEPCStub(t)
	access := ue.NewEPCAccess(stub.api, imsi1, quiet())
	att, err := access.Attach(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := ue.Attachment{Address: ue1Addr, PCSCFs: []netip.Addr{pcscfIP},
		AccessNetworkInfo: "3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010133C476B4321"}
	if att.Address != want.Address || len(att.PCSCFs) != 1 || att.PCSCFs[0] != pcscfIP || att.AccessNetworkInfo != want.AccessNetworkInfo {
		t.Errorf("attachment %+v, want %+v", att, want)
	}

	stub.authorizeCall()
	if e, _ := nextAccessEvent(t, access.Events()); e != ue.MediaResourcesReserved {
		t.Fatalf("event %v, want %v", e, ue.MediaResourcesReserved)
	}
	b := stub.bearers()
	if len(b) != 2 || b[1].EPSQoS.QCI != epc.QCIConversationalVoice || b[1].State != epc.BearerActive {
		t.Errorf("bearers %+v, want the voice bearer active", b)
	}
	stub.networkInitiated(http.MethodDelete, "/network-initiated/ues/"+imsi1+"/bearers/6")
	if e, _ := nextAccessEvent(t, access.Events()); e != ue.MediaResourcesReleased {
		t.Fatalf("event %v, want %v", e, ue.MediaResourcesReleased)
	}
	stub.networkInitiated(http.MethodPost, "/network-initiated/ues/"+imsi1+"/detach")
	if e, _ := nextAccessEvent(t, access.Events()); e != ue.Detached {
		t.Fatalf("event %v, want %v", e, ue.Detached)
	}
	if _, ok := nextAccessEvent(t, access.Events()); ok {
		t.Error("events go on after the network detached the UE")
	}
}

// TestDetachFromTheEPCStub is the detach of the UE as it switches off:
// the network forgets the PDN connection, and no event reports a loss.
func TestDetachFromTheEPCStub(t *testing.T) {
	stub := newEPCStub(t)
	access := ue.NewEPCAccess(stub.api, imsi1, quiet())
	if _, err := access.Attach(t.Context()); err != nil {
		t.Fatal(err)
	}
	resources, detached := ue.FollowAccess(access)
	stub.authorizeCall()
	waitFor(t, "the resources reserved", func() bool { r, _ := resources.State(); return r })
	if err := access.Detach(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-detached:
	case <-time.After(5 * time.Second):
		t.Fatal("the events did not end after the detach")
	}
	if b := stub.bearers(); b != nil {
		t.Errorf("the network still has bearers %+v", b)
	}
}

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls ok until it holds, for what the UE does in goroutines of
// its own.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("never %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestIMSIOfThePrivateIdentity(t *testing.T) {
	if imsi, err := ue.IMSIOf(impi); err != nil || imsi != imsi1 {
		t.Errorf("IMSIOf(%q) = %q, %v", impi, imsi, err)
	}
	if _, err := ue.IMSIOf("alice@" + domain); err == nil {
		t.Error("an identity that is no IMSI was taken")
	}
}

// TS 24.229 5.1.2A.1.1 and 5.1.1.2.1: a UE that knows its cell writes it
// into its requests.
func TestRegisterCarriesTheAccessNetworkInfo(t *testing.T) {
	registers := make(chan *sip.Message, 2)
	pcscf := stack(t, func(st *sip.ServerTransaction) {
		registers <- st.Request
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Contact", st.Request.Get("Contact"))
		resp.Add("P-Associated-URI", "<"+impu+">")
		st.Respond(resp)
	})
	s := stack(t, func(st *sip.ServerTransaction) {})
	pani := ue.AccessNetworkInfo(stubCell)
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: pcscf.LocalAddr(), AccessNetworkInfo: pani}
	u := ue.New(cfg, s, ue.NewContact(cfg, s.LocalAddr()), quiet())
	if _, err := u.Register(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := (<-registers).Get("P-Access-Network-Info"); got != pani {
		t.Errorf("P-Access-Network-Info %q, want %q", got, pani)
	}
}
