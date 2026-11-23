package icscf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	domain = "ims.mnc001.mcc001.3gppnetwork.org"
	impi   = "001010000000001@" + domain
	impu   = "sip:001010000000001@" + domain
	scscf  = "sip:scscf." + domain + ":5060"
	// telURI is in the implicit registration set of impu.
	telURI = "tel:+12025550101"
)

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newICSCF is New with a registry of the I-CSCF's own, which fails only
// when that registry already holds the metrics of an I-CSCF.
func newICSCF(t *testing.T, cfg Config, h HSS, node *diameter.Node, locator Locator) *ICSCF {
	t.Helper()
	i, err := New(cfg, h, node, locator, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return i
}

type hssFunc func(*diam.Message) (*diam.Message, error)

func (f hssFunc) Request(_ context.Context, m *diam.Message) (*diam.Message, error) { return f(m) }

func stubHSS(caps cx.ServerCapabilities) hssFunc { return stubHSSWith(caps, nil) }

// stubHSSWith is stubHSS with criteria in every user profile.
func stubHSSWith(caps cx.ServerCapabilities, criteria []ifc.InitialFilterCriteria) hssFunc {
	store, err := hss.NewMemoryStore(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, Password: "x"})
	if err != nil {
		panic(err)
	}
	h, err := hss.New(hss.Config{Realm: domain, VisitedNetworks: []string{domain}, ServerCapabilities: caps,
		ServiceProfiles: map[string][]ifc.InitialFilterCriteria{"default": criteria}, DefaultServiceProfile: "default"}, store,
		diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet()), quiet())
	if err != nil {
		panic(err)
	}
	return func(m *diam.Message) (*diam.Message, error) {
		b, _ := m.Serialize()
		req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
		if err != nil {
			return nil, err
		}
		return h.Handle(req), nil
	}
}

type locatorFunc func(host string, port int) ([]sip.Destination, error)

func (f locatorFunc) Locate(_ context.Context, host string, port int, _ sip.Transport) ([]sip.Destination, error) {
	return f(host, port)
}

// fast makes Timer F fire after 640 ms instead of 32 s.
var fast = sip.Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}

func newStack(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	return newStackWith(t, fast, onRequest)
}

func newStackWith(t *testing.T, timers sip.Timers, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), Timers: timers, OnRequest: onRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// setup starts an I-CSCF in front of a fake S-CSCF that answers 401 and
// records what it received.
func setup(t *testing.T, h HSS) (*sip.Stack, netip.AddrPort, chan *sip.Message) {
	t.Helper()
	received := make(chan *sip.Message, 1)
	ue, icscf := setupWith(t, h, func(st *sip.ServerTransaction) {
		received <- st.Request
		st.Respond(sip.NewResponse(st.Request, 401, sip.NewTag()))
	})
	return ue, icscf, received
}

func setupWith(t *testing.T, h HSS, scscfBehaviour func(*sip.ServerTransaction)) (*sip.Stack, netip.AddrPort) {
	t.Helper()
	return setupBehind(t, h, nil, scscfBehaviour)
}

// setupBehind is setupWith with first, the targets that DNS gives for
// the S-CSCF before the fake one.
func setupBehind(t *testing.T, h HSS, first []sip.Destination, scscfBehaviour func(*sip.ServerTransaction)) (*sip.Stack, netip.AddrPort) {
	t.Helper()
	_, ue, icscf := start(t, Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1, 2}}}},
		h, first, scscfBehaviour)
	return ue, icscf
}

// start runs an I-CSCF with cfg in front of a fake S-CSCF that
// scscfBehaviour serves, which DNS gives after first. It returns the
// I-CSCF, the UE's stack and the address of the I-CSCF.
func start(t *testing.T, cfg Config, h HSS, first []sip.Destination, scscfBehaviour func(*sip.ServerTransaction)) (*ICSCF, *sip.Stack, netip.AddrPort) {
	t.Helper()
	s := newStack(t, scscfBehaviour)
	locate := locatorFunc(func(host string, port int) ([]sip.Destination, error) {
		if host != "scscf."+domain || port != 5060 {
			return nil, errors.New("unexpected lookup")
		}
		return append(slices.Clone(first), sip.Destination{Transport: sip.UDP, Addr: s.LocalAddr()}), nil
	})
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i := newICSCF(t, cfg, h, node, locate)
	stack := newStack(t, i.OnRequest)
	// The UE keeps the default timers, so that it outlasts the I-CSCF's
	// Timer F and sees the I-CSCF's answer.
	return i, newStackWith(t, sip.DefaultTimers(), func(*sip.ServerTransaction) {}), stack.LocalAddr()
}

func register(t *testing.T, ue *sip.Stack, icscf netip.AddrPort, to string) *sip.Message {
	t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "69")
	m.Add("From", "<"+to+">;tag="+sip.NewTag())
	m.Add("To", "<"+to+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:ue@192.0.2.10>;expires=600000")
	m.Add("P-Visited-Network-ID", domain)
	ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: icscf})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	return final
}

func TestFirstRegistrationChoosesAnSCSCFByCapability(t *testing.T) {
	ue, icscf, received := setup(t, stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}))
	if r := register(t, ue, icscf, impu); r.StatusCode != 401 {
		t.Fatalf("got %d, want the S-CSCF's 401", r.StatusCode)
	}
	got := <-received
	if got.RequestURI != scscf {
		t.Errorf("Request-URI = %q, want %q", got.RequestURI, scscf)
	}
	if got.Get("Max-Forwards") != "68" {
		t.Errorf("Max-Forwards = %q, want 68", got.Get("Max-Forwards"))
	}
}

func TestNoSCSCFWithTheMandatoryCapabilities(t *testing.T) {
	ue, icscf, _ := setup(t, stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{9}}))
	if r := register(t, ue, icscf, impu); r.StatusCode != 600 {
		t.Fatalf("got %d, want 600", r.StatusCode)
	}
}

func TestUnknownUserIsForbidden(t *testing.T) {
	ue, icscf, _ := setup(t, stubHSS(cx.ServerCapabilities{}))
	if r := register(t, ue, icscf, "sip:nobody@"+domain); r.StatusCode != 403 {
		t.Fatalf("got %d, want 403", r.StatusCode)
	}
}

func TestUnreachableHSSGives480(t *testing.T) {
	down := hssFunc(func(*diam.Message) (*diam.Message, error) { return nil, diameter.ErrPeerUnavailable })
	ue, icscf, _ := setup(t, down)
	if r := register(t, ue, icscf, impu); r.StatusCode != 480 {
		t.Fatalf("got %d, want 480", r.StatusCode)
	}
}

// RFC 3263 4.3: when the first target that DNS gives for the S-CSCF
// does not answer, the REGISTER goes to the next one, in a new client
// transaction.
func TestREGISTERGoesToTheNextSCSCFTargetWhenTheFirstIsSilent(t *testing.T) {
	silent := newStack(t, func(*sip.ServerTransaction) {})
	received := make(chan *sip.Message, 1)
	ue, icscf := setupBehind(t, stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}),
		[]sip.Destination{{Transport: sip.UDP, Addr: silent.LocalAddr()}}, func(st *sip.ServerTransaction) {
			received <- st.Request
			st.Respond(sip.NewResponse(st.Request, 401, sip.NewTag()))
		})
	if r := register(t, ue, icscf, impu); r.StatusCode != 401 {
		t.Fatalf("got %d, want the 401 of the second target", r.StatusCode)
	}
	select {
	case got := <-received:
		if got.RequestURI != scscf {
			t.Errorf("Request-URI = %q, want %q", got.RequestURI, scscf)
		}
	default:
		t.Fatal("the second target got no REGISTER")
	}
}

func TestSilentSCSCFGives504(t *testing.T) {
	ue, icscf := setupWith(t, stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}), func(*sip.ServerTransaction) {})
	if r := register(t, ue, icscf, impu); r.StatusCode != 504 {
		t.Fatalf("got %d, want 504", r.StatusCode)
	}
}

// TestUnavailableSCSCFGives500: the S-CSCF, a shut down one, answers 503
// with Retry-After, and DNS gives no other target for it. The 503 says
// that the S-CSCF takes no request, not that the I-CSCF takes none, so
// the I-CSCF answers 500 without Retry-After (RFC 3261 16.7 step 6),
// for a REGISTER as for a request to a callee.
func TestUnavailableSCSCFGives500(t *testing.T) {
	unavailable := func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 503, sip.NewTag())
		resp.Add("Retry-After", "30")
		st.Respond(resp)
	}
	for _, tt := range []struct {
		name string
		hss  hssFunc
		send func(*testing.T, *sip.Stack, netip.AddrPort, string) *sip.Message
	}{
		{"REGISTER", stubHSS(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}), register},
		{"INVITE", registeredAtHSS(t), invite},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ue, icscf := setupWith(t, tt.hss, unavailable)
			if r := tt.send(t, ue, icscf, impu); r.StatusCode != 500 || r.Get("Retry-After") != "" {
				t.Errorf("got %d with Retry-After %q, want 500 without one", r.StatusCode, r.Get("Retry-After"))
			}
		})
	}
}

func invite(t *testing.T, ue *sip.Stack, icscf netip.AddrPort, requestURI string) *sip.Message {
	t.Helper()
	m := &sip.Message{Method: sip.INVITE, RequestURI: requestURI}
	m.Add("Max-Forwards", "68")
	m.Add("From", "<sip:caller@"+domain+">;tag="+sip.NewTag())
	m.Add("To", "<"+requestURI+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 INVITE")
	m.Add("Contact", "<sip:caller@192.0.2.20>")
	return send(t, ue, icscf, m)
}

// send sends m to the I-CSCF and returns the final response.
func send(t *testing.T, ue *sip.Stack, icscf netip.AddrPort, m *sip.Message) *sip.Message {
	t.Helper()
	ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: icscf})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	return final
}

func TestCallToAnUnregisteredCalleeGets480(t *testing.T) {
	ue, icscf, _ := setup(t, stubHSS(cx.ServerCapabilities{}))
	if r := invite(t, ue, icscf, impu); r.StatusCode != 480 {
		t.Fatalf("got %d, want 480", r.StatusCode)
	}
}

func TestCallToAnUnknownCalleeGets404(t *testing.T) {
	ue, icscf, _ := setup(t, stubHSS(cx.ServerCapabilities{}))
	if r := invite(t, ue, icscf, "sip:nobody@"+domain); r.StatusCode != 404 {
		t.Fatalf("got %d, want 404", r.StatusCode)
	}
}

// registeredAtHSS is the stub HSS after the callee has registered: the
// S-CSCF has sent its MAR and its SAR.
func registeredAtHSS(t *testing.T) hssFunc {
	t.Helper()
	h := stubHSS(cx.ServerCapabilities{})
	// Register the callee at the HSS: MAR then SAR from the S-CSCF.
	for _, m := range []*diam.Message{
		cx.MultimediaAuthRequest{Header: diameter.Header{SessionID: "s;1", OriginHost: "scscf." + domain, OriginRealm: domain, DestinationRealm: domain},
			UserName: impi, PublicIdentity: impu, SIPAuthenticationScheme: cx.SchemeSIPDigest, SIPNumberAuthItems: 1, ServerName: scscf}.Message(),
		cx.ServerAssignmentRequest{Header: diameter.Header{SessionID: "s;2", OriginHost: "scscf." + domain, OriginRealm: domain, DestinationRealm: domain},
			UserName: impi, PublicIdentities: []string{impu}, ServerName: scscf, ServerAssignmentType: cx.AssignRegistration}.Message(),
	} {
		if _, err := h(m); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

func TestCallToARegisteredCalleeGoesToItsSCSCF(t *testing.T) {
	ue, icscf, received := setup(t, registeredAtHSS(t))
	if r := invite(t, ue, icscf, impu); r.StatusCode != 401 {
		t.Fatalf("got %d, want the fake S-CSCF's answer", r.StatusCode)
	}
	got := <-received
	if got.Get("Route") != "<sip:scscf."+domain+":5060;lr>" || got.RequestURI != impu {
		t.Errorf("S-CSCF got Route %q and Request-URI %q", got.Get("Route"), got.RequestURI)
	}
}

// TestCallToANumberInASIPURIGoesOnAsATelURI: a peer network sends the
// number of the callee in a SIP URI with user=phone (TS 29.165 8.1).
// The I-CSCF turns it into a tel URI, with the parameters of the
// number, before it asks the HSS (TS 24.229 5.3.2.1 item 1 b), and the
// S-CSCF gets the tel URI.
func TestCallToANumberInASIPURIGoesOnAsATelURI(t *testing.T) {
	for uri, want := range map[string]string{
		"sip:+12025550101@" + domain + ";user=phone":      telURI,
		"sip:+12025550101;npdi@" + domain + ";user=phone": telURI + ";npdi",
		"sip:+12025550101@ibcf.example.net;user=PHONE;lr": telURI,
	} {
		t.Run(uri, func(t *testing.T) {
			ue, icscf, received := setup(t, registeredAtHSS(t))
			if r := invite(t, ue, icscf, uri); r.StatusCode != 401 {
				t.Fatalf("got %d, want the fake S-CSCF's answer", r.StatusCode)
			}
			if got := <-received; got.RequestURI != want {
				t.Errorf("Request-URI = %q, want %q", got.RequestURI, want)
			}
		})
	}
}

// TestNumbersThatStaySIPURIs: a SIP URI without user=phone, or one that
// is a GRUU (RFC 5627), keeps its form, which the HSS does not know.
func TestNumbersThatStaySIPURIs(t *testing.T) {
	for _, uri := range []string{
		"sip:+12025550101@" + domain,
		"sip:+12025550101@" + domain + ";user=phone;gr",
		"sip:12025550101@" + domain + ";user=phone",
	} {
		t.Run(uri, func(t *testing.T) {
			ue, icscf, _ := setup(t, registeredAtHSS(t))
			if r := invite(t, ue, icscf, uri); r.StatusCode != 404 {
				t.Fatalf("got %d, want 404 for an identity the HSS does not have", r.StatusCode)
			}
		})
	}
}

// TestMessageFromAnASGoesToTheCalleesSCSCF is a MESSAGE that an AS sends
// to a user as an originating UA (TS 24.229 5.7.3), such as the
// IP-SM-GW delivering an SMS, with the entry point of the network, the
// home domain, in Route. The I-CSCF takes that Route as its own and
// treats the MESSAGE like any initial request (5.3.2.1): the callee's
// S-CSCF alone is in Route, and the asserted identity and the body pass
// unchanged.
func TestMessageFromAnASGoesToTheCalleesSCSCF(t *testing.T) {
	ue, icscf, received := setup(t, registeredAtHSS(t))
	as := "sip:ipsmgw." + domain + ":5060"
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: impu}
	m.Add("Route", "<sip:"+domain+";lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+as+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", "<"+as+">")
	m.Add("Content-Type", "application/vnd.3gpp.sms")
	m.Body = []byte{0x01, 0x02}
	if r := send(t, ue, icscf, m); r.StatusCode != 401 {
		t.Fatalf("got %d, want the fake S-CSCF's answer", r.StatusCode)
	}
	got := <-received
	if routes := got.GetAll("Route"); got.Method != sip.MESSAGE || len(routes) != 1 || routes[0] != "<sip:scscf."+domain+":5060;lr>" || got.RequestURI != impu {
		t.Errorf("S-CSCF got %s with Route %q and Request-URI %q", got.Method, routes, got.RequestURI)
	}
	if got.Get("P-Asserted-Identity") != "<"+as+">" || got.Get("Record-Route") != "" || len(got.Body) != 2 {
		t.Errorf("P-Asserted-Identity %q, Record-Route %q, body %q", got.Get("P-Asserted-Identity"), got.Get("Record-Route"), got.Body)
	}
}

// unregisteredServices is a criterion of the unregistered state, which
// gives the callee services when not registered (TS 29.228 6.1.4.1 step
// 3).
func unregisteredServices() []ifc.InitialFilterCriteria {
	unregistered := ifc.TerminatingUnregistered
	return []ifc.InitialFilterCriteria{{
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, SessionCase: &unregistered}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:mmtel." + domain + ":5060"},
	}}
}

// TestCallToAnUnregisteredCalleeWithServices is TS 24.229 5.3.2.1 when
// the LIA has capabilities, not an S-CSCF: the I-CSCF picks the S-CSCF
// with them and sends the call there, or answers 480 when none has
// them.
func TestCallToAnUnregisteredCalleeWithServices(t *testing.T) {
	ue, icscf, received := setup(t, stubHSSWith(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}, unregisteredServices()))
	if r := invite(t, ue, icscf, impu); r.StatusCode != 401 {
		t.Fatalf("got %d, want the fake S-CSCF's answer", r.StatusCode)
	}
	if got := <-received; got.Get("Route") != "<sip:scscf."+domain+":5060;lr>" || got.RequestURI != impu {
		t.Errorf("S-CSCF got Route %q and Request-URI %q", got.Get("Route"), got.RequestURI)
	}

	ue, icscf, _ = setup(t, stubHSSWith(cx.ServerCapabilities{MandatoryCapabilities: []uint32{9}}, unregisteredServices()))
	if r := invite(t, ue, icscf, impu); r.StatusCode != 480 {
		t.Fatalf("without an S-CSCF that has the capabilities: got %d, want 480", r.StatusCode)
	}
}

// TestUseProxyFromTheSCSCF is the end of TS 24.229 5.3.2.1: the S-CSCF
// that the I-CSCF picked answers 305 with the S-CSCF that serves the
// callee (5.4.3.3), and the I-CSCF sends the call there instead.
func TestUseProxyFromTheSCSCF(t *testing.T) {
	served := make(chan *sip.Message, 1)
	other := newStack(t, func(st *sip.ServerTransaction) {
		served <- st.Request
		st.Respond(sip.NewResponse(st.Request, 486, sip.NewTag()))
	})
	first := newStack(t, func(st *sip.ServerTransaction) {
		r := sip.NewResponse(st.Request, 305, sip.NewTag())
		r.Add("Contact", "<sip:scscf2."+domain+":5060>")
		st.Respond(r)
	})
	locate := locatorFunc(func(host string, _ int) ([]sip.Destination, error) {
		switch host {
		case "scscf." + domain:
			return []sip.Destination{{Transport: sip.UDP, Addr: first.LocalAddr()}}, nil
		case "scscf2." + domain:
			return []sip.Destination{{Transport: sip.UDP, Addr: other.LocalAddr()}}, nil
		}
		return nil, errors.New("unexpected lookup")
	})
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i := newICSCF(t, Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1}}}},
		stubHSSWith(cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}, unregisteredServices()), node, locate)
	icscf := newStack(t, i.OnRequest)
	ue := newStackWith(t, sip.DefaultTimers(), func(*sip.ServerTransaction) {})

	if r := invite(t, ue, icscf.LocalAddr(), impu); r.StatusCode != 486 {
		t.Fatalf("got %d, want the 486 of the second S-CSCF", r.StatusCode)
	}
	if got := <-served; got.Get("Route") != "<sip:scscf2."+domain+":5060;lr>" {
		t.Errorf("second S-CSCF got Route %q, want itself alone", got.GetAll("Route"))
	}
}

// TestPSISubdomainGoesToTheASByDNS is TS 24.229 5.3.2.1 step 3: a
// SUBSCRIBE from another network to a conference URI, whose host is a
// subdomain of public service identities, goes where DNS resolves that
// subdomain, the AS that hosts the conference. The I-CSCF asks the HSS
// nothing for it (TS 23.228 5.4.12.3 b).
func TestPSISubdomainGoesToTheASByDNS(t *testing.T) {
	const subdomain = "conf." + domain
	received := make(chan *sip.Message, 1)
	as := newStack(t, func(st *sip.ServerTransaction) {
		received <- st.Request
		st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
	})
	locate := locatorFunc(func(host string, port int) ([]sip.Destination, error) {
		if host != subdomain || port != 0 {
			return nil, errors.New("unexpected lookup of " + host)
		}
		return []sip.Destination{{Transport: sip.UDP, Addr: as.LocalAddr()}}, nil
	})
	asked := make(chan *diam.Message, 1)
	h := hssFunc(func(m *diam.Message) (*diam.Message, error) {
		asked <- m
		return nil, errors.New("the HSS is not to be asked")
	})
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i := newICSCF(t, Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf}}, Host: "icscf." + domain, Port: 5060,
		PSISubdomains: []string{subdomain}}, h, node, locate)
	icscf := newStack(t, i.OnRequest)
	ue := newStackWith(t, sip.DefaultTimers(), func(*sip.ServerTransaction) {})

	conference := "sip:conf-7f3a@" + subdomain
	m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: conference}
	m.Add("Route", "<sip:icscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "68")
	m.Add("From", "<sip:+12025550151@example.net>;tag="+sip.NewTag())
	m.Add("To", "<"+conference+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 SUBSCRIBE")
	m.Add("Event", "conference")
	m.Add("Contact", "<sip:ue@192.0.2.20>")
	if r := send(t, ue, icscf.LocalAddr(), m); r.StatusCode != 200 {
		t.Fatalf("got %d, want the AS's 200", r.StatusCode)
	}
	select {
	case got := <-received:
		if got.RequestURI != conference || got.Get("Route") != "" {
			t.Errorf("AS got Request-URI %q and Route %q, want the conference URI and no Route", got.RequestURI, got.Get("Route"))
		}
	default:
		t.Fatal("the AS got no SUBSCRIBE")
	}
	select {
	case m := <-asked:
		t.Errorf("the I-CSCF asked the HSS: command %d", m.Header.CommandCode)
	default:
	}
}

// TestCANCELThatMatchesNothingGoesOnAlongItsRoute is RFC 3261 16.10 at
// the I-CSCF: a CANCEL that matches no transaction goes on statelessly
// to the Route it has left once the I-CSCF has taken its own. One with no
// Route left goes nowhere: the INVITE it would cancel went where the HSS
// said, and the I-CSCF does not ask the HSS for a CANCEL.
func TestCANCELThatMatchesNothingGoesOnAlongItsRoute(t *testing.T) {
	cancels := make(chan *sip.Message, 2)
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	next, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(),
		OnRequest: func(*sip.ServerTransaction) {}, OnCANCEL: func(m *sip.Message) { cancels <- m }}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	// Every name leads to next, so that a CANCEL sent on by its
	// Request-URI would show up there.
	locate := locatorFunc(func(string, int) ([]sip.Destination, error) {
		return []sip.Destination{{Transport: sip.UDP, Addr: next.LocalAddr()}}, nil
	})
	asked := hssFunc(func(m *diam.Message) (*diam.Message, error) {
		t.Errorf("the I-CSCF asked the HSS: command %d", m.Header.CommandCode)
		return nil, errors.New("no HSS")
	})
	node := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	i := newICSCF(t, Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf}}, Host: "icscf." + domain, Port: 5060}, asked, node, locate)
	i.SetStack(newStack(t, i.OnRequest))
	cancel := func(routes ...string) *sip.Message {
		m := &sip.Message{Method: sip.CANCEL, RequestURI: impu}
		m.Add("Via", "SIP/2.0/UDP 192.0.2.20:5060;branch="+sip.NewBranch())
		for _, r := range routes {
			m.Add("Route", r)
		}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<sip:001010000000002@"+domain+">;tag=caller")
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 CANCEL")
		return m
	}
	own, onward := "<sip:icscf."+domain+":5060;lr>", "<sip:scscf."+domain+":5060;lr>"

	i.OnCANCEL(cancel(own, onward))
	select {
	case got := <-cancels:
		if routes := got.GetAll("Route"); len(routes) != 1 || routes[0] != onward {
			t.Errorf("forwarded CANCEL has Route %q, want only %q", routes, onward)
		}
		if got.Get("Max-Forwards") != "69" {
			t.Errorf("forwarded CANCEL has Max-Forwards %q, want 69", got.Get("Max-Forwards"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the CANCEL with a Route left was not forwarded")
	}

	i.OnCANCEL(cancel(own))
	select {
	case got := <-cancels:
		t.Errorf("forwarded a CANCEL with no Route left, to %s", got.RequestURI)
	case <-time.After(200 * time.Millisecond):
	}
}
