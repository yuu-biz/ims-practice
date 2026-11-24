package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/fault"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/icscf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
	"github.com/yuu-biz/ims-practice/internal/oam"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

const (
	domain = "ims.mnc001.mcc001.3gppnetwork.org"
	impi   = "001010000000001@" + domain
	impu   = "sip:001010000000001@" + domain
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func wire(t *testing.T, m *diam.Message) *diam.Message {
	t.Helper()
	b, err := m.Serialize()
	if err != nil {
		t.Fatal(err)
	}
	got, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// result reads Result-Code or Experimental-Result-Code, whichever the
// answer has.
func result(t *testing.T, m *diam.Message) uint32 {
	t.Helper()
	var raw struct {
		ResultCode         uint32 `avp:"Result-Code,omitempty"`
		ExperimentalResult struct {
			Code uint32 `avp:"Experimental-Result-Code"`
		} `avp:"Experimental-Result,omitempty"`
	}
	if err := m.Unmarshal(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.ResultCode != 0 {
		return raw.ResultCode
	}
	return raw.ExperimentalResult.Code
}

// TestCxHandlerSendsEachRequestToItsRole checks the dispatch of a node
// with the hss role, the scscf role or both: an RTR and a PPR go to the
// S-CSCF, and the rest to the HSS.
func TestCxHandlerSendsEachRequestToItsRole(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "ims." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	hssRole, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", HomeDomain: domain}, nil, node, scscf.NewMemoryStore(), nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)

	rtr := cx.RegistrationTerminationRequest{
		Header:               diameter.NewHeader(node, "ims."+domain, domain),
		UserName:             impi,
		DeregistrationReason: cx.DeregistrationReason{ReasonCode: cx.PermanentTermination},
	}.Message()
	ppr := cx.PushProfileRequest{
		Header:   diameter.NewHeader(node, "ims."+domain, domain),
		UserName: impi,
		UserData: cx.UserProfile{PrivateIdentity: impi, PublicIdentities: []string{impu}}.Marshal(),
	}.Message()
	uar := cx.UserAuthorizationRequest{
		Header: diameter.NewHeader(node, "", domain), UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain,
	}.Message()
	// The HSS does not take an RTR.
	unsupported := diameter.DiameterCommandUnsupported
	// The S-CSCF has no registration for the user.
	unknown := cx.DiameterErrorUserUnknown

	tests := []struct {
		name    string
		hss     *hss.HSS
		scscf   *scscf.SCSCF
		request *diam.Message
		want    uint32
	}{
		{"RTR on an S-CSCF node", nil, s, rtr, unknown},
		{"RTR on a node with both roles", hssRole, s, rtr, unknown},
		{"RTR on an HSS node", hssRole, nil, rtr, unsupported},
		{"PPR on an S-CSCF node", nil, s, ppr, unknown},
		{"PPR on a node with both roles", hssRole, s, ppr, unknown},
		{"PPR on an HSS node", hssRole, nil, ppr, unsupported},
		{"UAR on a node with both roles", hssRole, s, uar, cx.DiameterFirstRegistration},
		{"UAR on an S-CSCF node", nil, s, uar, unsupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var role atomic.Pointer[scscf.SCSCF]
			if tt.scscf != nil {
				role.Store(tt.scscf)
			}
			answer := cxHandler(node, tt.hss, &role)(wire(t, tt.request))
			if got := result(t, wire(t, answer)); got != tt.want {
				t.Errorf("result %d, want %d", got, tt.want)
			}
		})
	}
}

// A node advertises the applications of its roles once each, and none
// for roles without Diameter.
func TestDiameterApplicationsFollowTheRoles(t *testing.T) {
	cx := []uint32{diameter.ApplicationIDCx}
	cxAndSh := []uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}
	// The HSS adds S6c, over which the SMS central functions ask it for
	// routing information and it alerts them.
	hss := []uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh, sgd.ApplicationIDS6c}
	// The IP-SM-GW adds SGd, over which a Service Centre brings it a
	// short message for one of its users.
	ipsmgw := []uint32{diameter.ApplicationIDSh, sgd.ApplicationIDSGd}
	// The Service Centre speaks the two applications of TS 29.338 and
	// nothing else.
	sc := []uint32{sgd.ApplicationIDS6c, sgd.ApplicationIDSGd}
	rx := []uint32{diameter.ApplicationIDRx}
	tests := []struct {
		roles []config.Role
		want  []uint32
	}{
		{[]config.Role{config.RoleHSS}, hss},
		{[]config.Role{config.RoleICSCF, config.RoleSCSCF}, cx},
		{[]config.Role{config.RoleSCSCF, config.RoleHSS}, hss},
		{[]config.Role{config.RolePCSCF}, rx},
		{[]config.Role{config.RoleEPC}, rx},
		{[]config.Role{config.RoleAGW}, nil},
		{[]config.Role{config.RoleIPSMGW}, ipsmgw},
		{[]config.Role{config.RoleSC}, sc},
		{[]config.Role{config.RoleSCSCF, config.RoleIPSMGW}, append(cxAndSh, sgd.ApplicationIDSGd)},
		// A node with both ends of SGd advertises it once.
		{[]config.Role{config.RoleIPSMGW, config.RoleSC}, append(ipsmgw, sgd.ApplicationIDS6c)},
	}
	for _, tt := range tests {
		if got := diameterApplications(config.Config{Roles: tt.roles}); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("roles %v: applications %v, want %v", tt.roles, got, tt.want)
		}
	}
}

// Rx requests of an AF go to the PCRF of the epc role, and those of a
// PCRF to the P-CSCF's Rx client; a node without the role refuses them.
// Cx requests still reach the HSS.
func TestDiameterHandlerSendsRxToTheEPCRole(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "ims." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDRx}, diameter.Timers{}, quiet())
	epcRole := epc.New(epc.Config{APN: "ims"}, epc.NewMemoryStore(), node, quiet())
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	hssRole, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	aar := rx.AARequest{Header: diameter.NewHeader(node, "", domain), MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		FramedIPAddress: netip.MustParseAddr("172.28.0.110")}.Message()
	uar := cx.UserAuthorizationRequest{
		Header: diameter.NewHeader(node, "", domain), UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain,
	}.Message()
	asr := rx.AbortSessionRequest{Header: diameter.NewHeader(node, "ims."+domain, domain), AbortCause: rx.BearerReleased}.Message()
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	tfr := sgd.MTForwardShortMessageRequest{Header: diameter.NewHeader(node, "ims."+domain, domain),
		UserName: "001010000000001", SCAddress: "12025550100", SMRPUI: []byte{0x00}}.Message()
	events := make(chan policy.Event, 1)
	client := rx.NewClient(node, nil, domain, func(e policy.Event) { events <- e }, quiet())
	var noSCSCF atomic.Pointer[scscf.SCSCF]
	for _, tt := range []struct {
		name    string
		epc     *epc.EPC
		pcscf   *rx.Client
		request *diam.Message
		want    uint32
	}{
		// No UE is attached, so session binding fails.
		{"AAR on an epc node", epcRole, nil, aar, rx.IPCANSessionNotAvailable},
		{"AAR on a node without epc", nil, nil, aar, diameter.DiameterCommandUnsupported},
		{"UAR on a node with hss and epc", epcRole, nil, uar, cx.DiameterFirstRegistration},
		// What a PCRF sends is for the P-CSCF, and what an AF sends for
		// the PCRF, also on a node with both roles.
		{"ASR on a pcscf node", nil, client, asr, diameter.DiameterSuccess},
		{"ASR on a node with epc and pcscf", epcRole, client, asr, diameter.DiameterSuccess},
		{"AAR on a node with epc and pcscf", epcRole, client, aar, rx.IPCANSessionNotAvailable},
		{"ASR on a pcscf node without a PCRF", nil, nil, asr, diameter.DiameterCommandUnsupported},
		// A short message over SGd needs the ipsmgw role; this node has
		// none.
		{"TFR on a node without ipsmgw", epcRole, nil, tfr, diameter.DiameterCommandUnsupported},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var policyControl atomic.Pointer[rx.Client]
			if tt.pcscf != nil {
				policyControl.Store(tt.pcscf)
			}
			var noIPSMGW atomic.Pointer[ipsmgw.IPSMGW]
			var noSC atomic.Pointer[smsc.SMSC]
			answer := diameterHandler(node, hssRole, &noSCSCF, tt.epc, &noIPSMGW, &noSC, &policyControl, quiet())(wire(t, tt.request))
			if got := result(t, wire(t, answer)); got != tt.want {
				t.Errorf("result %d, want %d", got, tt.want)
			}
		})
	}
}

// Each command of SGd and S6c reaches the role that owns its end of the
// reference point: the short message of a UE (OFR) and the alert (ALR)
// the Service Centre, the short message for a UE (TFR) the IP-SM-GW, and
// the routing info query (SRR) the HSS. A node without the role that a
// command belongs to says that it does not serve it.
func TestDiameterHandlerSendsTheSMSCommandsToTheirRole(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	const scNumber = "12025550100"
	node := diameter.NewNode(diameter.Identity{OriginHost: "sc." + domain, OriginRealm: domain},
		[]uint32{sgd.ApplicationIDS6c, sgd.ApplicationIDSGd}, diameter.Timers{}, quiet())
	// The SC answers out of its Store alone here: it refuses a short
	// message for a number the HSS does not answer about, and an alert
	// for a user it holds nothing for, without reaching either client.
	centre := smsc.New(smsc.Config{Address: scNumber, HSSRealm: domain}, smsc.NewMemoryStore(),
		nil, unreachableHSS{}, smsc.SystemClock, quiet())
	ofr := sgd.MOForwardShortMessageRequest{Header: diameter.NewHeader(node, "", domain), SCAddress: scNumber,
		UserIdentifier: sgd.UserIdentifier{MSISDN: "12025550101"},
		SMRPUI:         submitTPDU(t)}.Message()
	alr := sgd.AlertServiceCentreRequest{Header: diameter.NewHeader(node, "sc."+domain, domain), SCAddress: scNumber,
		UserIdentifier: sgd.UserIdentifier{MSISDN: "12025550102"}}.Message()
	tfr := sgd.MTForwardShortMessageRequest{Header: diameter.NewHeader(node, "ims."+domain, domain),
		UserName: "001010000000001", SCAddress: scNumber, SMRPUI: []byte{0x00}}.Message()
	srr := sgd.SendRoutingInfoForSMRequest{Header: diameter.NewHeader(node, "", domain),
		MSISDN: "12025550102", SCAddress: scNumber}.Message()
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	hssRole, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	var noSCSCF atomic.Pointer[scscf.SCSCF]
	for _, tt := range []struct {
		name    string
		sc      *smsc.SMSC
		hss     *hss.HSS
		request *diam.Message
		want    uint32
	}{
		// The HSS does not answer, so the SC cannot take the message.
		{"OFR on an sc node", centre, nil, ofr, sgd.DiameterErrorSMDeliveryFailure},
		{"OFR on a node without sc", nil, hssRole, ofr, diameter.DiameterCommandUnsupported},
		{"ALR on an sc node", centre, nil, alr, diameter.DiameterSuccess},
		{"ALR on a node without sc", nil, hssRole, alr, diameter.DiameterCommandUnsupported},
		// The TFR is the IP-SM-GW's end, which this node does not have
		// even with the sc role.
		{"TFR on an sc node", centre, nil, tfr, diameter.DiameterCommandUnsupported},
		// The SRR is for the HSS, also on a node that has both roles.
		{"SRR on an hss node", nil, hssRole, srr, sgd.DiameterErrorUserUnknown},
		{"SRR on a node with hss and sc", centre, hssRole, srr, sgd.DiameterErrorUserUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var scRole atomic.Pointer[smsc.SMSC]
			if tt.sc != nil {
				scRole.Store(tt.sc)
			}
			var noIPSMGW atomic.Pointer[ipsmgw.IPSMGW]
			var noPolicy atomic.Pointer[rx.Client]
			answer := diameterHandler(node, tt.hss, &noSCSCF, nil, &noIPSMGW, &scRole, &noPolicy, quiet())(wire(t, tt.request))
			if got := result(t, wire(t, answer)); got != tt.want {
				t.Errorf("result %d, want %d", got, tt.want)
			}
		})
	}
}

// unreachableHSS is an S6c client whose requests never arrive.
type unreachableHSS struct{}

func (unreachableHSS) SendRoutingInfoForSM(context.Context, sgd.SendRoutingInfoForSMRequest) (sgd.SendRoutingInfoForSMAnswer, error) {
	return sgd.SendRoutingInfoForSMAnswer{}, errors.New("the HSS is not reachable in this test")
}

func (unreachableHSS) ReportSMDeliveryStatus(context.Context, sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error) {
	return sgd.ReportSMDeliveryStatusAnswer{}, errors.New("the HSS is not reachable in this test")
}

// submitTPDU is an SMS-SUBMIT for a number in the test range.
func submitTPDU(t *testing.T) []byte {
	t.Helper()
	tpdu, err := sms.Submit{MessageReference: 1, DestinationAddress: sms.InternationalNumber("+12025550102")}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return tpdu
}

// The P-CSCF's PCRF is the last peer, after the instances of the HSS,
// and a P-CSCF without one has no policy control. Every peer before it
// is an instance of the HSS, so the roles that use Cx and Sh can send
// to any of them.
// Each role takes the peers that the diameter section says are for it:
// the roles that use Cx and Sh take every instance of the HSS, and the
// P-CSCF takes the PCRF. The kinds, not the places in the list, decide,
// so a node with two instances of the HSS and no PCRF leaves the
// P-CSCF without policy control instead of sending Rx to an HSS.
func TestPeersOfTheHSSAndThePCRF(t *testing.T) {
	hss1, hss2, pcrf, sc := &diameter.Peer{}, &diameter.Peer{}, &diameter.Peer{}, &diameter.Peer{}
	both := []config.Role{config.RolePCSCF, config.RoleSCSCF}
	for _, tt := range []struct {
		name  string
		roles []config.Role
		// kinds are the diameter section's entries, and peers the
		// connections in the same order.
		kinds []config.PeerKind
		peers []*diameter.Peer
		pcscf *diameter.Peer
		hss   []*diameter.Peer
		// sc are the connections to the Service Centre, which only the
		// ipsmgw role has.
		sc []*diameter.Peer
	}{
		{"pcscf with a PCRF", []config.Role{config.RolePCSCF},
			[]config.PeerKind{config.PeerPCRF}, []*diameter.Peer{pcrf}, pcrf, nil, nil},
		{"pcscf without Diameter", []config.Role{config.RolePCSCF}, nil, nil, nil, nil, nil},
		{"pcscf and scscf with the HSS only", both,
			[]config.PeerKind{config.PeerHSS}, []*diameter.Peer{hss1}, nil, []*diameter.Peer{hss1}, nil},
		{"pcscf and scscf with both", both,
			[]config.PeerKind{config.PeerHSS, config.PeerPCRF}, []*diameter.Peer{hss1, pcrf}, pcrf, []*diameter.Peer{hss1}, nil},
		{"pcscf and scscf with two instances of the HSS and a PCRF", both,
			[]config.PeerKind{config.PeerHSS, config.PeerHSS, config.PeerPCRF},
			[]*diameter.Peer{hss1, hss2, pcrf}, pcrf, []*diameter.Peer{hss1, hss2}, nil},
		{"pcscf and scscf with two instances of the HSS and no PCRF", both,
			[]config.PeerKind{config.PeerHSS, config.PeerHSS},
			[]*diameter.Peer{hss1, hss2}, nil, []*diameter.Peer{hss1, hss2}, nil},
		{"pcscf and scscf with the PCRF before the HSS", both,
			[]config.PeerKind{config.PeerPCRF, config.PeerHSS}, []*diameter.Peer{pcrf, hss1}, pcrf, []*diameter.Peer{hss1}, nil},
		{"scscf alone", []config.Role{config.RoleSCSCF},
			[]config.PeerKind{config.PeerHSS}, []*diameter.Peer{hss1}, nil, []*diameter.Peer{hss1}, nil},
		{"scscf with two instances of the HSS", []config.Role{config.RoleSCSCF},
			[]config.PeerKind{config.PeerHSS, config.PeerHSS}, []*diameter.Peer{hss1, hss2}, nil, []*diameter.Peer{hss1, hss2}, nil},
		// The Service Centre of an IP-SM-GW is a peer of its own kind,
		// and the HSS is still found beside it.
		{"ipsmgw with the HSS and the SC", []config.Role{config.RoleIPSMGW},
			[]config.PeerKind{config.PeerHSS, config.PeerSC}, []*diameter.Peer{hss1, sc}, nil, []*diameter.Peer{hss1},
			[]*diameter.Peer{sc}},
		{"ipsmgw with the HSS alone", []config.Role{config.RoleIPSMGW},
			[]config.PeerKind{config.PeerHSS}, []*diameter.Peer{hss1}, nil, []*diameter.Peer{hss1}, nil},
	} {
		cfg := config.Config{Roles: tt.roles}
		if tt.kinds != nil {
			cfg.Diameter = &config.Diameter{}
			for _, kind := range tt.kinds {
				cfg.Diameter.Peers = append(cfg.Diameter.Peers, config.Peer{Kind: kind})
			}
		}
		pcrf, err := pcrfPeers(cfg, tt.peers, metrics.NewRegistry())
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		var got []*diameter.Peer
		if pcrf != nil {
			got = pcrf.Peers()
		}
		var want []*diameter.Peer
		if tt.pcscf != nil {
			want = []*diameter.Peer{tt.pcscf}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: PCRF %v, want %v", tt.name, got, want)
		}
		group, err := hssPeers(cfg, tt.peers, metrics.NewRegistry(), config.RoleSCSCF)
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		if got := group.Peers(); !slices.Equal(got, tt.hss) {
			t.Errorf("%s: HSS %v, want %v", tt.name, got, tt.hss)
		}
		if got := peersOfKind(cfg, tt.peers, config.PeerSC); !slices.Equal(got, tt.sc) {
			t.Errorf("%s: SC %v, want %v", tt.name, got, tt.sc)
		}
	}
}

// TestStartIBCFListensOnBothSides: the IBCF gets one transport on each
// side, the internal one first, and refuses a configuration without an
// I-CSCF.
func TestStartIBCFListensOnBothSides(t *testing.T) {
	c := ibcfConfig()
	var started []config.SIP
	// The transports listen on any free port: the test only looks at
	// which side the IBCF asked for.
	listen := func(s config.SIP) (*sip.TransportLayer, error) {
		started = append(started, s)
		return sip.Listen("127.0.0.1:0", quiet())
	}
	b, err := startIBCF(domain, c, ibcf.NewMemoryStore(), noNames{}, metrics.NewRegistry(), listen, quiet())
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if len(started) != 2 || started[0] != c.SIP || started[1] != c.ExternalSIP {
		t.Errorf("transports %+v, want the internal one, then the external one", started)
	}
	c.ICSCF = sip.URI{}
	started = nil
	if _, err := startIBCF(domain, c, ibcf.NewMemoryStore(), noNames{}, metrics.NewRegistry(), listen, quiet()); err == nil {
		t.Error("an IBCF without an I-CSCF started")
	}
	if len(started) != 0 {
		t.Errorf("an IBCF that did not start opened %d transports", len(started))
	}
}

// TestMaintenanceListenerServesTheMetricsOfTheRoles: the IBCF counts in
// the registry of the node, which the maintenance listener serves at
// /metrics in the format that Prometheus scrapes. The IBCF is no role
// that the maintenance API serves, so a node of it alone has no API.
func TestMaintenanceListenerServesTheMetricsOfTheRoles(t *testing.T) {
	registry := metrics.NewRegistry()
	listen := func(config.SIP) (*sip.TransportLayer, error) { return sip.Listen("127.0.0.1:0", quiet()) }
	b, err := startIBCF(domain, ibcfConfig(), ibcf.NewMemoryStore(), noNames{}, registry, listen, quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop, err := startMaintenance(l, config.Config{Node: "ibcf"}, nil, registry, new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	resp, err := http.Get("http://" + l.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != metrics.ContentType {
		t.Errorf("GET /metrics = %s with Content-Type %q, want 200 with %q", resp.Status, resp.Header.Get("Content-Type"), metrics.ContentType)
	}
	if want := "# TYPE ims_ibcf_session_attempts_total counter\n"; !strings.Contains(string(body), want) {
		t.Errorf("GET /metrics has no %q:\n%s", want, body)
	}
	resp, err = http.Get("http://" + l.Addr().String() + maintenance.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET %s on a node without a role of the API = %s, want 404", maintenance.StatePath, resp.Status)
	}
}

// TestMaintenanceAPIShutsTheSCSCFDown: on the maintenance listener of a
// node with the scscf role, the API shows the S-CSCF and its
// configuration, and a shut down reaches the S-CSCF, which then refuses
// a new REGISTER.
func TestMaintenanceAPIShutsTheSCSCFDown(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "scscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	registry := metrics.NewRegistry()
	s, err := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", HomeDomain: domain, Metrics: registry},
		nil, node, scscf.NewMemoryStore(), nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	stack, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), OnRequest: s.OnRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	s.SetStack(stack)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "scscf", Roles: []config.Role{config.RoleSCSCF}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()},
		SCSCF: &config.SCSCF{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "scscf." + domain},
			MinExpires: time.Minute, MaxExpires: time.Hour}}
	// No instance of the HSS takes requests.
	roles := []managedRole{{function: s.Function(func() bool { return false }), manage: s.Manage}}
	stop, err := startMaintenance(l, cfg, roles, registry, new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Functions) != 1 || state.Functions[0].Role != string(config.RoleSCSCF) || state.Functions[0].OperationalState != maintenance.Disabled {
		t.Errorf("functions %+v, want the scscf alone, DISABLED without the HSS", state.Functions)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"scscf":{"sip":{"listen":"0.0.0.0:5060","host":"scscf.` + domain + `"},"min_expires":60,"max_expires":3600}`; !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	f, err := oam.SetAdministrativeState(ctx, string(config.RoleSCSCF), maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED", f, err)
	}

	ue, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sip.NewStack(ue, sip.Config{SentBy: ue.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	register := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	register.Add("Max-Forwards", "70")
	register.Add("From", "<"+impu+">;tag="+sip.NewTag())
	register.Add("To", "<"+impu+">")
	register.Add("Call-ID", sip.NewCallID())
	register.Add("CSeq", "1 REGISTER")
	ct, err := client.SendRequest(register, sip.Destination{Transport: sip.UDP, Addr: stack.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 503 || final.Get("Retry-After") == "" {
		t.Errorf("REGISTER to the shut down S-CSCF got %v, want 503 with Retry-After", final)
	}
}

// TestMaintenanceAPIShutsThePCSCFDown: a node with the pcscf and the
// scscf roles counts both in its one registry, and its maintenance API
// serves both. The configuration shows the section of the P-CSCF, and a
// shut down reaches the P-CSCF, which then answers a new REGISTER itself
// with 503 and Retry-After.
func TestMaintenanceAPIShutsThePCSCFDown(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	registry := metrics.NewRegistry()
	node := diameter.NewNode(diameter.Identity{OriginHost: "ims." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	s, err := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", HomeDomain: domain, Metrics: registry},
		nil, node, scscf.NewMemoryStore(), nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	p, err := pcscf.New(pcscf.Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, Metrics: registry},
		nil, pcscf.NewMemoryStore(), nil, quiet())
	if err != nil {
		t.Fatalf("the P-CSCF does not start beside the S-CSCF: %v", err)
	}
	t.Cleanup(p.Close)
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	stack, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), Screen: p.Screen,
		Admit: p.AdmitUnprotected, OnRequest: p.OnRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	p.SetStack(stack)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "ims", Roles: []config.Role{config.RolePCSCF, config.RoleSCSCF}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()},
		PCSCF: &config.PCSCF{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "pcscf." + domain},
			VisitedNetworkID: domain, ProtectedServerPort: 5064},
		SCSCF: &config.SCSCF{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "scscf." + domain},
			MinExpires: time.Minute, MaxExpires: time.Hour}}
	roles := []managedRole{
		{function: p.Function(), manage: p.Manage},
		{function: s.Function(func() bool { return true }), manage: s.Manage},
	}
	stop, err := startMaintenance(l, cfg, roles, registry, new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	resp, err := http.Get("http://" + l.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	for _, want := range []string{"# TYPE ims_registration_attempts_total counter\n", "# TYPE ims_registration_setup_seconds histogram\n",
		"# TYPE ims_session_attempts_total counter\n", "# TYPE ims_answered_sessions gauge\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("GET /metrics has no %q:\n%s", want, body)
		}
	}
	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var functions []string
	for _, f := range state.Functions {
		functions = append(functions, f.Role+" "+string(f.OperationalState))
	}
	if want := []string{"pcscf ENABLED", "scscf ENABLED"}; !slices.Equal(functions, want) {
		t.Errorf("functions %v, want %v", functions, want)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"pcscf":{"sip":{"listen":"0.0.0.0:5060","host":"pcscf.` + domain + `"},"visited_network_id":"` + domain + `","sec_agree":{"protected_server_port":5064}}`; !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	f, err := oam.SetAdministrativeState(ctx, string(config.RolePCSCF), maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED", f, err)
	}

	ue, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sip.NewStack(ue, sip.Config{SentBy: ue.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	register := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	register.Add("Max-Forwards", "70")
	register.Add("From", "<"+impu+">;tag="+sip.NewTag())
	register.Add("To", "<"+impu+">")
	register.Add("Call-ID", sip.NewCallID())
	register.Add("CSeq", "1 REGISTER")
	register.Add("Contact", "<sip:ue@"+ue.LocalAddr().String()+">")
	ct, err := client.SendRequest(register, sip.Destination{Transport: sip.UDP, Addr: stack.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 503 || final.Get("Retry-After") == "" {
		t.Errorf("REGISTER to the shut down P-CSCF got %v, want 503 with Retry-After", final)
	}
}

// TestMaintenanceAPIShutsTheICSCFDown: a node with the icscf role
// serves its function on the maintenance API and its session count at
// /metrics. The configuration shows the section of the I-CSCF, and a
// shut down reaches the I-CSCF, which then answers a new REGISTER with
// 503 and Retry-After instead of asking the HSS.
func TestMaintenanceAPIShutsTheICSCFDown(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	registry := metrics.NewRegistry()
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i, err := icscf.New(icscf.Config{HomeDomain: domain, Host: "icscf." + domain, Port: 5060,
		SCSCFs: []icscf.SCSCF{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}}, Metrics: registry},
		nil, node, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	stack, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), OnRequest: i.OnRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	i.SetStack(stack)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "icscf", Roles: []config.Role{config.RoleICSCF}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()},
		ICSCF: &config.ICSCF{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "icscf." + domain},
			SCSCFs: []config.SCSCFEntry{{Name: "sip:scscf." + domain + ":5060", Capabilities: []uint32{1}}}}}
	// No instance of the HSS takes the Cx requests of this I-CSCF, so it
	// is DISABLED.
	roles := []managedRole{{function: i.Function(func() bool { return false }), manage: i.Manage}}
	stop, err := startMaintenance(l, cfg, roles, registry, new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	resp, err := http.Get("http://" + l.Addr().String() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := "# TYPE ims_session_attempts_total counter\n"; !strings.Contains(string(body), want) {
		t.Errorf("GET /metrics has no %q:\n%s", want, body)
	}
	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Functions) != 1 || state.Functions[0].Role != string(config.RoleICSCF) ||
		state.Functions[0].OperationalState != maintenance.Disabled {
		t.Errorf("functions %+v, want the I-CSCF DISABLED without an HSS peer", state.Functions)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := `"icscf":{"sip":{"listen":"0.0.0.0:5060","host":"icscf.` + domain + `"},"scscfs":[{"name":"sip:scscf.` + domain + `:5060","capabilities":[1]}]}`
	if !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	f, err := oam.SetAdministrativeState(ctx, string(config.RoleICSCF), maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED with no transaction in progress", f, err)
	}

	ue, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sip.NewStack(ue, sip.Config{SentBy: ue.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	register := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	register.Add("Max-Forwards", "70")
	register.Add("From", "<"+impu+">;tag="+sip.NewTag())
	register.Add("To", "<"+impu+">")
	register.Add("Call-ID", sip.NewCallID())
	register.Add("CSeq", "1 REGISTER")
	register.Add("Contact", "<sip:ue@"+ue.LocalAddr().String()+">")
	ct, err := client.SendRequest(register, sip.Destination{Transport: sip.UDP, Addr: stack.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 503 || final.Get("Retry-After") == "" {
		t.Errorf("REGISTER to the shut down I-CSCF got %v, want 503 with Retry-After", final)
	}
}

// TestIBCFStoreIsTheSharedStoreOfTheNode: with a store section of kind
// valkey, the IBCF reads the records of its earlier process from that
// server when it starts, so a server that cannot be reached keeps it
// from starting. Without a store section the records are in the
// process, and it starts.
func TestIBCFStoreIsTheSharedStoreOfTheNode(t *testing.T) {
	listen := func(config.SIP) (*sip.TransportLayer, error) { return sip.Listen("127.0.0.1:0", quiet()) }
	for _, tt := range []struct {
		name   string
		store  *config.Store
		starts bool
	}{
		{"no store section", nil, true},
		{"memory", &config.Store{Kind: config.StoreMemory, Timeout: time.Second}, true},
		{"valkey that cannot be reached", unreachableStore(t), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stores, closeStores := newRoleStores(tt.store)
			defer closeStores()
			b, err := startIBCF(domain, ibcfConfig(), ibcfStore(stores, quiet()), noNames{}, metrics.NewRegistry(), listen, quiet())
			if err == nil {
				b.Close()
			}
			if started := err == nil; started != tt.starts {
				t.Errorf("the IBCF started: %v (error %v), want %v", started, err, tt.starts)
			}
		})
	}
}

// TestMediaNodeStoreIsTheSharedStoreOfTheNode: with a store section of
// kind valkey, a media relay reads the contexts of its earlier process
// from that server when it starts, so a server that cannot be reached
// keeps the node from starting. Without a store section the contexts
// are in the process, and it starts.
func TestMediaNodeStoreIsTheSharedStoreOfTheNode(t *testing.T) {
	for _, tt := range []struct {
		name   string
		store  *config.Store
		starts bool
	}{
		{"no store section", nil, true},
		{"memory", &config.Store{Kind: config.StoreMemory, Timeout: time.Second}, true},
		{"valkey that cannot be reached", unreachableStore(t), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stores, closeStores := newRoleStores(tt.store)
			defer closeStores()
			store, err := agwStore(stores, "agw-1", config.RoleAGW, quiet())
			if err != nil {
				t.Fatal(err)
			}
			minPort, maxPort := rtptest.PortRange(t, 4)
			relay := config.AGW{
				Control:    netip.MustParseAddrPort("127.0.0.1:0"),
				RTPAddress: netip.MustParseAddr("127.0.0.1"),
				MinPort:    minPort, MaxPort: maxPort,
			}
			_, stop, err := startMediaNode(relay, nil, store, quiet(), func() {})
			if err == nil {
				stop()
			}
			if started := err == nil; started != tt.starts {
				t.Errorf("the media node started: %v (error %v), want %v", started, err, tt.starts)
			}
		})
	}
}

// TestTheMediaRolesOfANodeKeepTheirContextsApart: a node that has both
// the agw and the mrfp role runs two relays, each with ports of its
// own, so each keeps its contexts under a name of its own. A relay that
// read the contexts of the other would open ports outside its range,
// and give those contexts up.
func TestTheMediaRolesOfANodeKeepTheirContextsApart(t *testing.T) {
	ctx := context.Background()
	stores := roleStores{kv: kvstore.NewMemoryStore(nil),
		section: config.Store{Kind: config.StoreValkey, Prefix: "ims:", Timeout: time.Second}}
	relay, err := agwStore(stores, "media-1", config.RoleAGW, quiet())
	if err != nil {
		t.Fatal(err)
	}
	processor, err := agwStore(stores, "media-1", config.RoleMRFP, quiet())
	if err != nil {
		t.Fatal(err)
	}
	call := agw.SavedContext{ID: "c1", Terminations: []agw.SavedTermination{
		{ID: agw.Access, LocalRTP: 40000, Mode: agw.SendReceive},
		{ID: agw.Core, LocalRTP: 40002, Mode: agw.SendReceive},
	}}
	if err := relay.PutContext(ctx, call); err != nil {
		t.Fatal(err)
	}
	got, err := processor.Contexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("the mrfp role of the node reads %+v, want none of the contexts of the agw role", got)
	}
	if stored, err := relay.Contexts(ctx); err != nil || len(stored) != 1 {
		t.Errorf("the agw role reads %+v, %v; want the context it stored", stored, err)
	}
}

// TestHSSStartsAgainOnItsStore: an HSS that starts again on the shared
// store keeps the subscribers stored there, and adds those of the
// configuration that are not. A subscriber of the configuration one of
// whose public identities another subscriber holds in the store keeps
// the HSS from starting, rather than being left out.
func TestHSSStartsAgainOnItsStore(t *testing.T) {
	ctx := context.Background()
	stores := roleStores{kv: kvstore.NewMemoryStore(nil), section: config.Store{Kind: config.StoreValkey, Timeout: time.Second}}
	alice := hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"}
	if _, err := hssStore(ctx, hss.Config{Subscriptions: []hss.Subscription{alice}}, stores, quiet()); err != nil {
		t.Fatal(err)
	}
	bob := hss.Subscription{PrivateIdentity: "bob@" + domain, PublicIdentities: []string{"sip:bob@" + domain}, Password: "q"}
	store, err := hssStore(ctx, hss.Config{Subscriptions: []hss.Subscription{alice, bob}}, stores, quiet())
	if err != nil {
		t.Fatalf("the HSS did not start again on its store: %v", err)
	}
	for _, id := range []string{alice.PrivateIdentity, bob.PrivateIdentity} {
		if _, ok, err := store.Subscriber(ctx, id); err != nil || !ok {
			t.Errorf("subscriber %s after the second start: %v, %v", id, ok, err)
		}
	}
	carol := hss.Subscription{PrivateIdentity: "carol@" + domain, PublicIdentities: []string{impu}, Password: "r"}
	if _, err := hssStore(ctx, hss.Config{Subscriptions: []hss.Subscription{carol}}, stores, quiet()); !errors.Is(err, hss.ErrIdentityTaken) {
		t.Errorf("an HSS whose configuration gives a stored public identity to another subscriber started: %v", err)
	}
}

// TestRoleStoresAreTheSharedStoreOfTheNode: with a store section of
// kind valkey, each role takes its state from that server, so its first
// operation on its Store fails when nothing listens there. Without a
// store section, and with kind memory, the role keeps its state in the
// process and the same operation works.
//
// The IBCF is not here: it reads its records when it starts, which
// TestIBCFStoreIsTheSharedStoreOfTheNode follows all the way to the
// node that does not start.
func TestRoleStoresAreTheSharedStoreOfTheNode(t *testing.T) {
	ctx := context.Background()
	roles := []struct {
		// name is the role, and the function within it when the role has
		// more than one Store.
		name string
		// read is one operation of the role on its Store, the kind it
		// does for the first request it serves.
		read func(roleStores) error
	}{
		{string(config.RoleSCSCF), func(s roleStores) error {
			_, _, err := scscfStore(s, quiet()).Registration(ctx, impi)
			return err
		}},
		{string(config.RoleHSS), func(s roleStores) error {
			subscriber := hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"}
			_, err := hssStore(ctx, hss.Config{Subscriptions: []hss.Subscription{subscriber}}, s, quiet())
			return err
		}},
		{string(config.RolePCSCF), func(s roleStores) error {
			ue := sip.Destination{Transport: sip.UDP, Addr: netip.MustParseAddrPort("172.28.0.100:5060")}
			_, _, err := pcscfStore(s, quiet()).Association(ctx, ue)
			return err
		}},
		{string(config.RoleMMTel), func(s roleStores) error {
			leg := sip.DialogID{CallID: "a-call-id", LocalTag: "local", RemoteTag: "remote"}
			_, _, err := mmtelStore(s, quiet()).CallOf(ctx, leg)
			return err
		}},
		{string(config.RoleMRFC), func(s roleStores) error {
			_, _, err := mrfcStore(s, quiet()).Conference(ctx, "conference-1")
			return err
		}},
		{string(config.RoleIPSMGW), func(s roleStores) error {
			_, _, err := ipsmgwStore(s, quiet()).Delivery(ctx, "a-call-id")
			return err
		}},
		{string(config.RoleIPSMGW) + " sc stub", func(s roleStores) error {
			_, _, err := scStore(s, quiet()).TakeStatusReportRequest(ctx, "1")
			return err
		}},
	}
	for _, r := range roles {
		for _, tt := range []struct {
			name  string
			store *config.Store
			works bool
		}{
			{"no store section", nil, true},
			{"memory", &config.Store{Kind: config.StoreMemory, Timeout: time.Second}, true},
			{"valkey that cannot be reached", unreachableStore(t), false},
		} {
			t.Run(r.name+" with "+tt.name, func(t *testing.T) {
				stores, closeStores := newRoleStores(tt.store)
				defer closeStores()
				err := r.read(stores)
				if worked := err == nil; worked != tt.works {
					t.Errorf("the operation on the Store of the %s worked: %v (error %v), want %v", r.name, worked, err, tt.works)
				}
			})
		}
	}
}

// unreachableStore is a store section of kind valkey whose server
// answers nothing: its address is a port that was free a moment ago, so
// nothing listens there.
func unreachableStore(t *testing.T) *config.Store {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return &config.Store{Kind: config.StoreValkey, Address: address, Timeout: time.Second}
}

// TestRoleLocksAreInTheSharedStoreOfTheNode: the roles that serialise
// what happens to one user take that lock in the store section's server,
// so that the instances of one role exclude each other over a user. The
// roles do not exclude each other, since each locks under its own name.
// Without a store section, and with kind memory, the role gets no
// locker and keeps the lock per user of its own process.
func TestRoleLocksAreInTheSharedStoreOfTheNode(t *testing.T) {
	ctx := context.Background()
	// The roles whose Config takes a lock per user.
	roles := []config.Role{config.RoleSCSCF, config.RoleHSS, config.RoleIPSMGW}

	for _, section := range []*config.Store{nil, {Kind: config.StoreMemory, Timeout: time.Second}} {
		stores, closeStores := newRoleStores(section)
		for _, role := range roles {
			if locks := stores.locks(role, quiet()); locks != nil {
				t.Errorf("the %s with the store section %+v got a locker, want the lock of its own process", role, section)
			}
		}
		closeStores()
	}

	unreachable, closeUnreachable := newRoleStores(unreachableStore(t))
	defer closeUnreachable()
	for _, role := range roles {
		if _, err := unreachable.locks(role, quiet()).Lock(ctx, impi); err == nil {
			t.Errorf("the %s took the lock of a user on a store that cannot be reached", role)
		}
	}

	// One node on a store it can reach, as newRoleStores opens it.
	shared := roleStores{kv: kvstore.NewMemoryStore(nil),
		section: config.Store{Kind: config.StoreValkey, Prefix: "ims:", Timeout: time.Second}}
	for _, role := range roles {
		unlock, err := shared.locks(role, quiet()).Lock(ctx, impi)
		if err != nil {
			t.Fatalf("the %s did not take the lock of a user while the other roles hold theirs: %v", role, err)
		}
		defer unlock()
	}
	// A second instance of one role waits for the lock that the first
	// holds, until the deadline of this attempt.
	waiting, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	if _, err := shared.locks(config.RoleSCSCF, quiet()).Lock(waiting, impi); err == nil {
		t.Error("a second S-CSCF on the same store took the lock of a user that the first holds")
	}
}

// ibcfConfig is the configuration of an IBCF with one peer.
func ibcfConfig() config.IBCF {
	return config.IBCF{
		SIP:         config.SIP{Listen: netip.MustParseAddrPort("127.0.0.1:5060"), Host: "ibcf." + domain},
		ExternalSIP: config.SIP{Listen: netip.MustParseAddrPort("127.0.0.2:5060"), Host: "ibcf.nni.mnc001.example.net"},
		ICSCF:       sip.URI{Scheme: "sip", Host: "icscf." + domain, Port: 5060},
		TrGW:        config.TrGW{Control: "http://trgw:8080", CoreAddress: netip.MustParseAddr("192.0.2.91"), PeerAddress: netip.MustParseAddr("198.51.100.11")},
		Peers: []ibcf.Peer{{
			Profile:    interconnect.Profile{Name: "peer-b", Domain: "ims.mnc002.mcc001.3gppnetwork.org", Transports: []sip.Transport{sip.UDP}},
			EntryPoint: "sip:ibcf.nni.mnc002.example.net:5060",
			Sources:    []netip.Prefix{netip.MustParsePrefix("198.51.100.20/32")},
		}},
	}
}

// noNames is a DNS that knows no name.
type noNames struct{}

func (noNames) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return nil, errors.New("no such name")
}

// TestFaultAgentServesTheAPIOfTheRole: a node whose only role is fault
// serves the fault API, and a fault it takes goes to the Docker Engine
// at the socket of its configuration. Nothing listens on that socket
// here, so the answer is the failure of the executor, which shows that
// the API reaches Docker and not something else.
func TestFaultAgentServesTheAPIOfTheRole(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := startFaultAgent(ctx, l, config.Fault{
		DockerSocket:   filepath.Join(t.TempDir(), "docker.sock"),
		ComposeProject: "lab",
		ToolsImage:     "lab/fault-tools:dev",
	}, quiet(), cancel)
	defer stop()

	api := "http://" + l.Addr().String()
	resp, err := http.Get(api + fault.FaultsPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("GET %s = %s %s, want 200 and no fault", fault.FaultsPath, resp.Status, body)
	}

	resp, err = http.Post(api+fault.FaultsPath, "application/json", strings.NewReader(`{"target":"pcscf","kind":"stop"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError || !strings.Contains(string(body), "docker engine") {
		t.Errorf("POST a fault without an Engine = %s %s, want 500 from the Engine", resp.Status, body)
	}
	var answer fault.ErrorBody
	if err := json.Unmarshal(body, &answer); err != nil || answer.Fault == nil || answer.Fault.State != fault.Failed {
		t.Errorf("the failed fault is not in the answer: %s", body)
	}
}

// TestOAMRoleServesItsAPIAndScreen: a node whose only role is oam
// serves the OAM API under /oam/v1/ and the OAM screen beside it, with
// the inventory of its configuration. With a data directory it keeps
// the backups and the history of the configuration changes it made;
// without one it refuses those operations.
func TestOAMRoleServesItsAPIAndScreen(t *testing.T) {
	// No node needs to answer here: the inventory is what the
	// configuration says, and the OAM asks a node only for an operation
	// on that node.
	c := config.OAM{
		DataDir: t.TempDir(),
		Nodes: []config.OAMNode{
			{Name: "pcscf-1", Roles: []string{"pcscf"}, Maintenance: "http://127.0.0.1:1/"},
		},
	}
	// serve mounts an OAM as the role does: the API under its prefix
	// and the screen beside it, on one listener.
	serve := func(c config.OAM) string {
		t.Helper()
		server, err := newOAM(c, quiet())
		if err != nil {
			t.Fatal(err)
		}
		mux := http.NewServeMux()
		mux.Handle(oam.PathPrefix, server.Handler())
		mux.Handle("/", oam.UIHandler())
		listener := httptest.NewServer(mux)
		t.Cleanup(listener.Close)
		return listener.URL
	}
	withData := serve(c)
	withoutData := serve(config.OAM{Nodes: c.Nodes})

	get := func(url string) (int, string) {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.StatusCode, string(body)
	}

	status, body := get(withData + oam.PathPrefix + "inventory")
	var inventory oam.Inventory
	if err := json.Unmarshal([]byte(body), &inventory); err != nil {
		t.Fatalf("the inventory is not JSON: %s", body)
	}
	want := []oam.Endpoint{{Name: "pcscf-1", Roles: []string{"pcscf"}, URL: "http://127.0.0.1:1/"}}
	if status != http.StatusOK || !reflect.DeepEqual(inventory.Nodes, want) {
		t.Errorf("the inventory is %d %+v, want 200 and %+v", status, inventory.Nodes, want)
	}

	if status, body = get(withData + oam.PathPrefix + "backups"); status != http.StatusOK ||
		!strings.Contains(body, `"backups"`) {
		t.Errorf("the backups of an OAM with a data directory are %d %s, want 200", status, body)
	}
	if status, body = get(withoutData + oam.PathPrefix + "backups"); status != http.StatusServiceUnavailable {
		t.Errorf("the backups of an OAM without a data directory are %d %s, want 503", status, body)
	}
	// The history file is made when the role starts, so that a change
	// is not the first thing to find out that the directory cannot be
	// written to.
	if _, err := os.Stat(filepath.Join(c.DataDir, "config-history.jsonl")); err != nil {
		t.Errorf("the history file: %v", err)
	}

	if status, body = get(withData + "/"); status != http.StatusOK || !strings.Contains(strings.ToLower(body), "<html") {
		t.Errorf("the OAM screen is %d %s, want 200 and a page", status, body)
	}
}

// TestMaintenanceAPIShutsTheMMTelASDown: on the maintenance listener of
// a node with the mmtel role, the API shows the AS and its
// configuration, with the key that signs the nonces of Ut redacted, and
// a shut down reaches the AS, which then refuses an INVITE outside a
// dialog.
func TestMaintenanceAPIShutsTheMMTelASDown(t *testing.T) {
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	store := mmtel.NewMemoryStore()
	as := mmtel.New(mmtel.Config{Host: "mmtel." + domain, Port: int(transport.LocalAddr().Port()), HomeDomain: domain},
		noRepositoryData{}, store, store, noNames{}, quiet())
	if err := as.Listen(transport); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(as.Close)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "mmtel", Roles: []config.Role{config.RoleMMTel}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()},
		MMTel: &config.MMTel{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "mmtel." + domain},
			HomeCountryCode: "1", ConferenceDomain: "conf." + domain,
			XCAP: config.XCAP{Listen: netip.MustParseAddrPort("0.0.0.0:80"), NonceKey: []byte("test-nonce-key")}}}
	// No instance of the HSS takes the Sh requests of this AS, so it is
	// DISABLED.
	roles := []managedRole{{function: as.Function(func() bool { return false }), manage: as.Manage}}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Functions) != 1 || state.Functions[0].Role != string(config.RoleMMTel) ||
		state.Functions[0].OperationalState != maintenance.Disabled {
		t.Errorf("functions %+v, want the MMTel AS DISABLED without an Sh peer", state.Functions)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := `"mmtel":{"sip":{"listen":"0.0.0.0:5060","host":"mmtel.` + domain +
		`"},"home_country_code":"1","xcap":{"listen":"0.0.0.0:80","nonce_key":"` + maintenance.RedactedValue +
		`"},"conference_domain":"conf.` + domain + `"}`
	if !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	f, err := oam.SetAdministrativeState(ctx, string(config.RoleMMTel), maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED with no call in progress", f, err)
	}

	caller, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	client, err := sip.NewStack(caller, sip.Config{SentBy: caller.LocalAddr().String(), OnRequest: func(*sip.ServerTransaction) {}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	invite := &sip.Message{Method: sip.INVITE, RequestURI: "sip:+12025550102@" + domain}
	invite.Add("Max-Forwards", "70")
	invite.Add("From", "<"+impu+">;tag="+sip.NewTag())
	invite.Add("To", "<sip:+12025550102@"+domain+">")
	invite.Add("Call-ID", sip.NewCallID())
	invite.Add("CSeq", "1 INVITE")
	invite.Add("Contact", "<sip:ue@"+caller.LocalAddr().String()+">")
	ct, err := client.SendRequest(invite, sip.Destination{Transport: sip.UDP, Addr: transport.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 503 || final.Get("Retry-After") == "" {
		t.Errorf("INVITE to the shut down MMTel AS got %v, want 503 with Retry-After", final)
	}
}

// noRepositoryData is an HSS that answers the AS no service document.
type noRepositoryData struct{}

func (noRepositoryData) RepositoryData(context.Context, string, string) (sh.TransparentData, bool, error) {
	return sh.TransparentData{}, false, nil
}

// TestMaintenanceAPIShutsTheMRFCDown: on the maintenance listener of a
// node with the mrfc role, the API shows the MRFC and its
// configuration, and a shut down reaches the MRFC, which then refuses
// the INVITE of an announcement with 503 and Retry-After. The MRFP of
// the configuration is never reached: the refusal comes before the MRFC
// would ask it for the announcement.
func TestMaintenanceAPIShutsTheMRFCDown(t *testing.T) {
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	mrfp := "http://mrfp." + domain + ":8080"
	client := agw.NewClient(mrfp)
	m, err := mrfc.New(mrfc.Config{Host: "mrfc." + domain, Port: int(transport.LocalAddr().Port()),
		NotifyURL: "http://mrfc." + domain + ":8080/notify"},
		[]mrfc.Processor{{Name: mrfp, Control: client, Announcements: client}}, mrfc.NewMemoryStore(), noNames{}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Listen(transport); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "mrfc", Roles: []config.Role{config.RoleMRFC}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()},
		MRFC: &config.MRFC{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "mrfc." + domain},
			Notify: netip.MustParseAddrPort("0.0.0.0:8080"), MRFPs: []string{mrfp}}}
	roles := []managedRole{{function: m.Function(), manage: m.Manage}}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The MRFC holds no association with an MRFP, so it is ENABLED while
	// the node runs, with no dialog of its own.
	functions := state.Functions
	if len(functions) != 1 || functions[0].Role != string(config.RoleMRFC) ||
		functions[0].OperationalState != maintenance.Enabled ||
		functions[0].Users == nil || *functions[0].Users != 0 {
		t.Errorf("functions %+v, want the MRFC ENABLED with no user", functions)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := `"mrfc":{"sip":{"listen":"0.0.0.0:5060","host":"mrfc.` + domain +
		`"},"notify":"0.0.0.0:8080","mrfp":["` + mrfp + `"]}`
	if !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	shut, err := oam.SetAdministrativeState(ctx, string(config.RoleMRFC),
		maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || shut.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED with no dialog in progress", shut, err)
	}

	caller, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	as, err := sip.NewUA(caller, sip.UAConfig{SentBy: caller.LocalAddr().String(),
		Contact: "sip:mmtel@" + caller.LocalAddr().String(),
		OnCall:  func(c *sip.IncomingCall) { c.Reject(480) },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: transport.LocalAddr()}}, nil
		}}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(as.Close)
	out, err := as.Invite(sip.Invitation{
		RequestURI: "sip:annc@mrfc." + domain + ";play=/provisioned/notice", From: "sip:mmtel@" + domain})
	if err != nil {
		t.Fatal(err)
	}
	var last *sip.Message
	for r := range out.Responses() {
		last = r
	}
	if last == nil || last.StatusCode != 503 || last.Get("Retry-After") == "" {
		t.Errorf("the INVITE to the shut down MRFC got %v, want 503 with Retry-After", last)
	}
}

// TestMaintenanceAPIShutsTheMediaRelaysDown: on the maintenance
// listener of a node with both media roles, the API shows a function
// for each of them under the name of the role, with its configuration
// and the contexts it holds, and a shut down reaches that relay alone.
// A relay with a context stays SHUTTINGDOWN until its controller
// subtracts it, and makes no new context meanwhile.
func TestMaintenanceAPIShutsTheMediaRelaysDown(t *testing.T) {
	agwPorts, agwMaxPort := rtptest.PortRange(t, 4)
	mrfpPorts, mrfpMaxPort := rtptest.PortRange(t, 4)
	cfg := config.Config{Node: "media-1", Roles: []config.Role{config.RoleAGW, config.RoleMRFP}, HomeDomain: domain,
		AGW: &config.AGW{Control: netip.MustParseAddrPort("127.0.0.1:0"), RTPAddress: netip.MustParseAddr("127.0.0.1"),
			MinPort: agwPorts, MaxPort: agwMaxPort},
		MRFP: &config.MRFP{AGW: config.AGW{Control: netip.MustParseAddrPort("127.0.0.1:0"),
			RTPAddress: netip.MustParseAddr("127.0.0.1"), MinPort: mrfpPorts, MaxPort: mrfpMaxPort},
			Announcements: map[string]string{"notice": "../../deploy/announcements/notice.wav"}}}

	announcements, err := agw.LoadAnnouncements(cfg.MRFP.Announcements)
	if err != nil {
		t.Fatal(err)
	}
	relay := startRelay(t, *cfg.AGW, nil)
	processor := startRelay(t, cfg.MRFP.AGW, announcements)
	roles := []managedRole{
		{function: relay.Function(string(config.RoleAGW)), manage: relay.Manage},
		{function: processor.Function(string(config.RoleMRFP)), manage: processor.Manage},
	}
	// One pass of a call through the P-CSCF, which the shut down below
	// has to wait for.
	if _, err := relay.Add(agw.TerminationRequest{ID: agw.Access}, agw.TerminationRequest{ID: agw.Core}); err != nil {
		t.Fatal(err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	cfg.Maintenance = &config.Maintenance{Listen: l.Addr().String()}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Each relay answers from its own sockets, so both are ENABLED while
	// the node runs. The agw role holds the one context of the test.
	functions := state.Functions
	if len(functions) != 2 ||
		functions[0].Role != string(config.RoleAGW) || functions[0].OperationalState != maintenance.Enabled ||
		functions[0].Users == nil || *functions[0].Users != 1 ||
		functions[1].Role != string(config.RoleMRFP) || functions[1].Users == nil || *functions[1].Users != 0 {
		t.Errorf("functions %+v, want the agw role with its one context and the mrfp role with none", functions)
	}
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"agw":{"control":"127.0.0.1:0","rtp_address":"127.0.0.1","rtp_ports":{"min":` + strconv.Itoa(int(agwPorts)),
		`"mrfp":{"control":"127.0.0.1:0","rtp_address":"127.0.0.1","rtp_ports":{"min":` + strconv.Itoa(int(mrfpPorts)),
		`"announcements":["notice"]}`,
	} {
		if !strings.Contains(string(conf.Configuration), want) {
			t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
		}
	}

	// The shut down of the agw role waits for its context, and the mrfp
	// role beside it is untouched.
	shut, err := oam.SetAdministrativeState(ctx, string(config.RoleAGW),
		maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || shut.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v (%v), want SHUTTINGDOWN with a context in use", shut, err)
	}
	state, err = oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Functions[1].AdministrativeState != maintenance.Unlocked {
		t.Errorf("the mrfp role is %s, want UNLOCKED after the shut down of the agw role beside it",
			state.Functions[1].AdministrativeState)
	}
}

// startRelay starts a media node as main starts one, with its contexts
// in the memory of this process, and subtracts the contexts that the
// test leaves, so that the next run finds its ports free.
func startRelay(t *testing.T, c config.AGW, announcements agw.Announcements) *agw.Relay {
	t.Helper()
	relay, stop, err := startMediaNode(c, announcements, nil, quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
		stop()
	})
	return relay
}

// hssMaintenanceNode starts an HSS on cfg with the maintenance listener
// of its node in front of it, wired as main wires them, and returns the
// client of that API and the HSS.
func hssMaintenanceNode(t *testing.T, cfg hss.Config) (*maintenance.Client, *hss.HSS) {
	t.Helper()
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain},
		[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h, err := hss.New(cfg, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	node.Handle(h.Handle)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	nodeConfig := config.Config{Node: "hss", Roles: []config.Role{config.RoleHSS}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()}, HSS: &cfg}
	roles := []managedRole{{function: h.Function(server), manage: h.Manage, settings: h.Settings()}}
	stop, err := startMaintenance(l, nodeConfig, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient), h
}

// TestMaintenanceAPIShowsTheProfilesAndSettingsOfTheHSS: the
// configuration that the maintenance API of an HSS node answers with
// names every service profile of the network with the features its
// criteria turn on, which is what an operator reads to choose a profile
// for a subscription, and lists the settings of the HSS that change
// while the node runs. It holds no subscriber: their credentials are
// secrets, and the provisioning API is where the subscribers are.
func TestMaintenanceAPIShowsTheProfilesAndSettingsOfTheHSS(t *testing.T) {
	oam, _ := hssMaintenanceNode(t, hss.Config{
		Realm: domain, VisitedNetworks: []string{domain}, ProvisioningListen: "0.0.0.0:8081",
		ServiceProfiles: map[string]hss.ServiceProfile{
			"default":     {Features: []string{"sms", "mmtel"}},
			"no-services": {},
		},
		DefaultServiceProfile:  "default",
		TestUserServiceProfile: "default",
	})
	conf, err := oam.Configuration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	want := `"hss":{"provisioning_listen":"0.0.0.0:8081","service_profiles":` +
		`{"default":{"features":["sms","mmtel"]},"no-services":{"features":[]}}}`
	if !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	if strings.Contains(string(conf.Configuration), impi) {
		t.Errorf("the configuration names a subscriber: %s", conf.Configuration)
	}
	// The admission policy and the two profile names are in the
	// changeable settings of the same answer, with the value in force,
	// and not in the hss section, so that one place has them.
	changeable := map[string]string{}
	for _, s := range conf.Changeable {
		changeable[s.Name] = string(s.Value)
	}
	for name, value := range map[string]string{
		"hss.admission":                 `"all"`,
		"hss.default_service_profile":   `"default"`,
		"hss.test_user_service_profile": `"default"`,
	} {
		if changeable[name] != value {
			t.Errorf("the changeable setting %s is %q, want %s", name, changeable[name], value)
		}
	}
}

// TestMaintenanceAPIShutsTheIPSMGWDown: on the maintenance listener of
// a node with the ipsmgw role, the API shows the IP-SM-GW and its
// configuration, and a shut down reaches the IP-SM-GW, which then
// refuses a short message from a UE with 503 and Retry-After. The SC
// beside it is handed nothing: the refusal comes before the IP-SM-GW
// would read the body.
func TestMaintenanceAPIShutsTheIPSMGWDown(t *testing.T) {
	gwCfg := &config.IPSMGW{SIP: config.SIP{Listen: netip.MustParseAddrPort("0.0.0.0:5060"), Host: "ipsmgw." + domain},
		Number: "12025550199", ServiceCentre: sms.InternationalNumber("+12025550100")}
	centre := &untouchedSC{}
	g := ipsmgw.New(ipsmgw.Config{URI: gwCfg.SIP.URI(), HomeDomain: domain, Number: gwCfg.Number,
		ServiceCentre: gwCfg.ServiceCentre}, centre, ipsmgw.NewMemoryStore(), noSMSRegistration{}, noNames{}, quiet())
	t.Cleanup(g.Close)
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	stack, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), OnRequest: g.OnRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stack.Close)
	g.SetStack(stack)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Node: "ipsmgw", Roles: []config.Role{config.RoleIPSMGW}, HomeDomain: domain,
		Maintenance: &config.Maintenance{Listen: l.Addr().String()}, IPSMGW: gwCfg}
	hssUp := true
	roles := []managedRole{{function: g.Function(func() bool { return hssUp }), manage: g.Manage}}
	stop, err := startMaintenance(l, cfg, roles, metrics.NewRegistry(), new(slog.LevelVar), quiet(), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	ctx := t.Context()
	oam := maintenance.NewClient("http://"+l.Addr().String(), http.DefaultClient)

	state, err := oam.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The IP-SM-GW is ENABLED while an instance of the HSS takes its Sh
	// requests, with nothing in its hands.
	functions := state.Functions
	if len(functions) != 1 || functions[0].Role != string(config.RoleIPSMGW) ||
		functions[0].OperationalState != maintenance.Enabled ||
		functions[0].Users == nil || *functions[0].Users != 0 {
		t.Errorf("functions %+v, want the IP-SM-GW ENABLED with no user", functions)
	}
	hssUp = false
	if state, err := oam.State(ctx); err != nil || state.Functions[0].OperationalState != maintenance.Disabled {
		t.Errorf("the IP-SM-GW is %+v (%v) without an Sh peer, want DISABLED", state.Functions, err)
	}
	hssUp = true
	conf, err := oam.Configuration(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := `"ipsmgw":{"sip":{"listen":"0.0.0.0:5060","host":"ipsmgw.` + domain +
		`"},"number":"12025550199","service_centre":"+12025550100"}`
	if !strings.Contains(string(conf.Configuration), want) {
		t.Errorf("configuration %s, want %s in it", conf.Configuration, want)
	}
	shut, err := oam.SetAdministrativeState(ctx, string(config.RoleIPSMGW),
		maintenance.AdministrativeStateRequest{AdministrativeState: maintenance.ShuttingDown})
	if err != nil || shut.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v (%v), want LOCKED with nothing in hand", shut, err)
	}

	resp := sendShortMessage(t, transport.LocalAddr())
	if resp == nil || resp.StatusCode != 503 || resp.Get("Retry-After") == "" {
		t.Errorf("the short message to the shut down IP-SM-GW got %v, want 503 with Retry-After", resp)
	}
	if centre.asked.Load() {
		t.Error("the SC was handed the short message of a refused MESSAGE")
	}
}

// sendShortMessage sends the MESSAGE of a short message from a UE to
// the IP-SM-GW at addr, as the S-CSCF does by the filter criteria, and
// returns the final response.
func sendShortMessage(t *testing.T, addr netip.AddrPort) *sip.Message {
	t.Helper()
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	from, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(),
		OnRequest: func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 405, sip.NewTag())) }}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(from.Close)
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: "tel:+12025550100"}
	m.Add("Max-Forwards", "69")
	m.Add("From", "<sip:+12025550101@"+domain+">;tag="+sip.NewTag())
	m.Add("To", "<tel:+12025550100>")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", "<tel:+12025550101>")
	m.Add("Content-Type", ipsmgw.ContentType)
	body, err := sms.RPDataMSToNetwork{MessageReference: 1, DestinationAddress: sms.InternationalNumber("+12025550100"),
		UserData: []byte{0x01, 0x00, 0x00, 0x00, 0x00}}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	m.Body = body
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	return final
}

// untouchedSC is an SC that records whether the IP-SM-GW handed it
// anything. A drained IP-SM-GW never does.
type untouchedSC struct{ asked atomic.Bool }

func (c *untouchedSC) Submit(context.Context, sms.Address, sms.Submit) (*sms.SubmitReport, error) {
	c.asked.Store(true)
	return &sms.SubmitReport{}, nil
}

func (c *untouchedSC) Report(context.Context, string, ipsmgw.Outcome) { c.asked.Store(true) }

// noSMSRegistration is an HSS that takes no Sh request, which a drained
// IP-SM-GW never attempts.
type noSMSRegistration struct{}

func (noSMSRegistration) IMSI(context.Context, string) (string, error) {
	return "", errors.New("no HSS in this test")
}

func (noSMSRegistration) UpdateSMSRegistrationInfo(context.Context, sh.UserIdentity, string) error {
	return errors.New("no HSS in this test")
}

func (noSMSRegistration) NotifyReadyForSM(context.Context, sh.UserIdentity, string, sh.ReadyForSM) error {
	return errors.New("no HSS in this test")
}

// The Service Centre ends when the node tells it to, even when no
// signal to stop came: the start of the node can fail after the SC has
// started, and the node then returns its error without waiting for a
// signal.
func TestTheServiceCentreEndsWhenTheNodeTellsIt(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "sc." + domain, OriginRealm: domain},
		[]uint32{sgd.ApplicationIDS6c, sgd.ApplicationIDSGd}, diameter.Timers{}, quiet())
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()
	hssGroup, err := diameter.NewPeerGroup(metrics.NewRegistry(), string(config.RoleSC))
	if err != nil {
		t.Fatal(err)
	}
	_, stop := startSC(context.Background(), config.SC{Address: "12025550100"}, domain, smsc.NewMemoryStore(),
		node, server, hssGroup, quiet())
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the SC did not end within 5 s of the node telling it to")
	}
}
