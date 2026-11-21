package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/epc"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/rx"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
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
// with the hss role, the scscf role or both: an RTR goes to the
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
	hssRole := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	s := scscf.New(scscf.Config{ServerName: "sip:scscf." + domain + ":5060", HomeDomain: domain}, nil, node, scscf.NewMemoryStore(), nil, quiet())

	rtr := cx.RegistrationTerminationRequest{
		Header:               cx.NewHeader(node, "ims."+domain, domain),
		UserName:             impi,
		DeregistrationReason: cx.DeregistrationReason{ReasonCode: cx.PermanentTermination},
	}.Message()
	uar := cx.UserAuthorizationRequest{
		Header: cx.NewHeader(node, "", domain), UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain,
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
	rx := []uint32{diameter.ApplicationIDRx}
	tests := []struct {
		roles []config.Role
		want  []uint32
	}{
		{[]config.Role{config.RoleHSS}, cxAndSh},
		{[]config.Role{config.RoleICSCF, config.RoleSCSCF}, cx},
		{[]config.Role{config.RoleSCSCF, config.RoleHSS}, cxAndSh},
		{[]config.Role{config.RolePCSCF}, rx},
		{[]config.Role{config.RoleEPC}, rx},
		{[]config.Role{config.RoleAGW}, nil},
		{[]config.Role{config.RoleIPSMGW}, []uint32{diameter.ApplicationIDSh}},
		{[]config.Role{config.RoleSCSCF, config.RoleIPSMGW}, cxAndSh},
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
	hssRole := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}}, store, node, quiet())
	aar := rx.AARequest{Header: cx.NewHeader(node, "", domain), MediaComponents: []policy.MediaComponent{policy.SignallingComponent()},
		FramedIPAddress: netip.MustParseAddr("172.28.0.110")}.Message()
	uar := cx.UserAuthorizationRequest{
		Header: cx.NewHeader(node, "", domain), UserName: impi, PublicIdentity: impu, VisitedNetworkIdentifier: domain,
	}.Message()
	asr := rx.AbortSessionRequest{Header: cx.NewHeader(node, "ims."+domain, domain), AbortCause: rx.BearerReleased}.Message()
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
		{"ASR on a pcscf node", nil, client, asr, rx.DiameterSuccess},
		{"ASR on a node with epc and pcscf", epcRole, client, asr, rx.DiameterSuccess},
		{"AAR on a node with epc and pcscf", epcRole, client, aar, rx.IPCANSessionNotAvailable},
		{"ASR on a pcscf node without a PCRF", nil, nil, asr, diameter.DiameterCommandUnsupported},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var policyControl atomic.Pointer[rx.Client]
			if tt.pcscf != nil {
				policyControl.Store(tt.pcscf)
			}
			answer := diameterHandler(node, hssRole, &noSCSCF, tt.epc, &policyControl)(wire(t, tt.request))
			if got := result(t, wire(t, answer)); got != tt.want {
				t.Errorf("result %d, want %d", got, tt.want)
			}
		})
	}
}

// The P-CSCF's PCRF is the peer that is not the HSS, and a P-CSCF
// without one has no policy control.
func TestPCRFPeer(t *testing.T) {
	hssPeer, pcrf := &diameter.Peer{}, &diameter.Peer{}
	for _, tt := range []struct {
		name  string
		roles []config.Role
		peers []*diameter.Peer
		want  *diameter.Peer
	}{
		{"pcscf with a PCRF", []config.Role{config.RolePCSCF}, []*diameter.Peer{pcrf}, pcrf},
		{"pcscf without Diameter", []config.Role{config.RolePCSCF}, nil, nil},
		{"pcscf and scscf with the HSS only", []config.Role{config.RolePCSCF, config.RoleSCSCF}, []*diameter.Peer{hssPeer}, nil},
		{"pcscf and scscf with both", []config.Role{config.RolePCSCF, config.RoleSCSCF}, []*diameter.Peer{hssPeer, pcrf}, pcrf},
		{"scscf alone", []config.Role{config.RoleSCSCF}, []*diameter.Peer{hssPeer}, nil},
	} {
		if got := pcrfPeer(config.Config{Roles: tt.roles}, tt.peers); got != tt.want {
			t.Errorf("%s: got peer %p, want %p", tt.name, got, tt.want)
		}
	}
}

// TestStartIBCFListensOnBothSides: the IBCF gets one transport on each
// side, the internal one first, and refuses a configuration without an
// I-CSCF.
func TestStartIBCFListensOnBothSides(t *testing.T) {
	c := config.IBCF{
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
	var started []config.SIP
	// The transports listen on any free port: the test only looks at
	// which side the IBCF asked for.
	listen := func(s config.SIP) (*sip.TransportLayer, error) {
		started = append(started, s)
		return sip.Listen("127.0.0.1:0", quiet())
	}
	b, err := startIBCF(domain, c, noNames{}, listen, quiet())
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if len(started) != 2 || started[0] != c.SIP || started[1] != c.ExternalSIP {
		t.Errorf("transports %+v, want the internal one, then the external one", started)
	}
	c.ICSCF = sip.URI{}
	started = nil
	if _, err := startIBCF(domain, c, noNames{}, listen, quiet()); err == nil {
		t.Error("an IBCF without an I-CSCF started")
	}
	if len(started) != 0 {
		t.Errorf("an IBCF that did not start opened %d transports", len(started))
	}
}

// noNames is a DNS that knows no name.
type noNames struct{}

func (noNames) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return nil, errors.New("no such name")
}
