package pcscf

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	testIK = "0123456789abcdef0123456789abcdef"
	testCK = "fedcba9876543210fedcba9876543210"
)

// akaHarness is a P-CSCF with a protected server port in front of a
// fake home network that challenges a REGISTER marked "no" with the
// 401 of TS 24.229 5.4.1.2.1A, and accepts one marked "yes".
type akaHarness struct {
	t             *testing.T
	p             *PCSCF
	store         *MemoryStore
	unprotected   *sip.Stack // the P-CSCF at 5060
	protected     *sip.Stack // the P-CSCF at port_ps
	ueUnprotected *sip.Stack
	ueProtected   *sip.Stack // the UE at port_us
	home          *sip.Stack // the I-CSCF and the S-CSCF
	received      chan *sip.Message
	// toUE are the requests that reach the UE's protected server port.
	toUE           chan *sip.Message
	securityClient string
	// noKeys leaves ik and ck out of the 401. The fake home network reads
	// it from a goroutine of its stack, after a message over a socket,
	// which the race detector does not see as a hand-over.
	noKeys atomic.Bool
	// failWith, when not zero, is the status with which the fake home
	// network answers a REGISTER marked "yes".
	failWith atomic.Int32
}

func newAKAHarness(t *testing.T, protectedPort bool) *akaHarness {
	t.Helper()
	return newAKAHarnessWith(t, protectedPort, quiet(), nil)
}

// newAKAHarnessWith is newAKAHarness with the P-CSCF logging to log. When
// wrap is not nil, the P-CSCF uses the Store that wrap makes of h.store.
func newAKAHarnessWith(t *testing.T, protectedPort bool, log *slog.Logger, wrap func(*MemoryStore) Store) *akaHarness {
	t.Helper()
	h := &akaHarness{t: t, received: make(chan *sip.Message, 10), toUE: make(chan *sip.Message, 10)}
	h.home = newStack(t, func(st *sip.ServerTransaction) {
		h.received <- st.Request
		req := st.Request
		if req.Method != sip.REGISTER {
			st.Respond(sip.NewResponse(req, 486, sip.NewTag()))
			return
		}
		c, _ := sip.ParseCredentials(req.Get("Authorization"))
		if c.IntegrityProtected() != sip.IntegrityProtectedYes {
			resp := sip.NewResponse(req, 401, sip.NewTag())
			ch := sip.Challenge{Realm: "scscf." + domain, Nonce: "bm9uY2U=", Algorithm: sip.AlgorithmAKAv1MD5, QOP: []string{"auth"}}
			if !h.noKeys.Load() {
				ch.SetKeys(testIK, testCK)
			}
			resp.Add("WWW-Authenticate", ch.String())
			st.Respond(resp)
			return
		}
		if status := h.failWith.Load(); status != 0 {
			st.Respond(sip.NewResponse(req, int(status), sip.NewTag()))
			return
		}
		expires := "3600"
		if strings.Contains(req.Get("Contact"), "expires=0") {
			expires = "0"
		}
		resp := sip.NewResponse(req, 200, sip.NewTag())
		resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
		resp.Add("P-Associated-URI", "<"+impu+">")
		resp.Add("Contact", "<sip:ue@127.0.0.1>;expires="+expires)
		st.Respond(resp)
	})
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: h.home.LocalAddr()}}
	})
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}
	if protectedPort {
		cfg.ProtectedServerPort = int(tr.LocalAddr().Port())
	}
	h.store = NewMemoryStore()
	var store Store = h.store
	if wrap != nil {
		store = wrap(h.store)
	}
	h.p = New(cfg, locate, store, nil, log)
	h.unprotected = newPCSCFStack(t, h.p)
	h.p.SetStack(h.unprotected)
	h.protected, err = sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), Screen: h.p.Screen, OnRequest: h.p.OnProtectedRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.protected.Close)
	h.p.SetProtectedStack(h.protected)
	h.ueUnprotected = newStack(t, func(*sip.ServerTransaction) {})
	h.ueProtected = newStack(t, func(st *sip.ServerTransaction) {
		h.toUE <- st.Request
		st.Respond(sip.NewResponse(st.Request, 486, sip.NewTag()))
	})
	h.securityClient = fmt.Sprintf("ipsec-3gpp;alg=hmac-sha2-256-128;ealg=null;spi-c=1111;spi-s=2222;port-c=50001;port-s=%d",
		h.ueProtected.LocalAddr().Port())
	return h
}

// register sends a REGISTER of the UE, from its protected server port
// to the protected server port of the P-CSCF when protected is set, and
// returns the final response and the REGISTER the home network got, if
// any.
func (h *akaHarness) register(protected bool, response, expires string, extra ...sip.HeaderField) (*sip.Message, *sip.Message) {
	h.t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", "call-1")
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:ue@"+h.ueProtected.LocalAddr().String()+">;expires="+expires)
	m.Add("Supported", "path")
	m.Add("Require", sip.OptionSecAgree)
	m.Add("Proxy-Require", sip.OptionSecAgree)
	m.Add("Security-Client", h.securityClient)
	for _, f := range extra {
		m.Add(f.Name, f.Value)
	}
	m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="", uri="sip:`+domain+
		`", response="`+response+`", algorithm=AKAv1-MD5`)
	from, to := h.ueUnprotected, h.unprotected
	if protected {
		from, to = h.ueProtected, h.protected
	}
	final := h.send(from, to, m)
	deadline := time.After(100 * time.Millisecond)
	for {
		select {
		case got := <-h.received:
			// The P-CSCF's own reg event SUBSCRIBE may come between.
			if got.Method == sip.REGISTER {
				return final, got
			}
		case <-deadline:
			return final, nil
		}
	}
}

func (h *akaHarness) send(from, to *sip.Stack, m *sip.Message) *sip.Message {
	h.t.Helper()
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: to.LocalAddr()})
	if err != nil {
		h.t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		h.t.Fatalf("no final response: %v", ct.Err())
	}
	return final
}

// overTCP tells whether the UE sent m over TCP: the transport of the
// UE's Via, the last one of m as the home network got it.
func overTCP(m *sip.Message) bool {
	vias := m.GetAll("Via")
	if len(vias) == 0 {
		return false
	}
	via, err := sip.ParseVia(vias[len(vias)-1])
	return err == nil && via.Transport == string(sip.TCP)
}

// largerThanUDP makes a REGISTER larger than 1300 bytes, which the UE
// then sends over TCP (RFC 3261 18.1.1).
var largerThanUDP = sip.HeaderField{Name: "User-Agent", Value: strings.Repeat("x", 1300)}

// ueKey is the UE in the SA table: its address and protected server
// port.
func (h *akaHarness) ueKey() netip.AddrPort { return h.ueProtected.LocalAddr() }

// challenge runs the first REGISTER and returns the Security-Server of
// the 401, as the UE mirrors it in Security-Verify.
func (h *akaHarness) challenge() []sip.HeaderField {
	h.t.Helper()
	r, _ := h.register(false, "", "600000")
	if r.StatusCode != 401 {
		h.t.Fatalf("first REGISTER got %d, want 401", r.StatusCode)
	}
	var verify []sip.HeaderField
	for _, v := range r.GetAll("Security-Server") {
		verify = append(verify, sip.HeaderField{Name: "Security-Verify", Value: v})
	}
	return append(verify, sip.HeaderField{Name: "Security-Client", Value: h.securityClient})
}

// TS 24.229 5.2.2.2 and TS 33.203 7.2: the Security-Client stays at the
// P-CSCF, the 401 loses ik and ck and gains Security-Server, and the
// answer that comes protected and repeats both lists goes on with
// integrity-protected "yes".
func TestIMSAKASecurityAgreement(t *testing.T) {
	logs := newLogRecords()
	h := newAKAHarnessWith(t, true, slog.New(logs), nil)
	r, first := h.register(false, "", "600000")
	if first == nil || r.StatusCode != 401 {
		t.Fatalf("first REGISTER got %d", r.StatusCode)
	}
	c, _ := sip.ParseCredentials(first.Get("Authorization"))
	if c.IntegrityProtected() != sip.IntegrityProtectedNo {
		t.Errorf("Authorization to the home network = %s, want integrity-protected no", first.Get("Authorization"))
	}
	// RFC 3329 2.3.1: the agreement ends at the P-CSCF.
	if first.Get("Security-Client") != "" || first.Requires(sip.OptionSecAgree) || first.Get("Proxy-Require") != "" {
		t.Errorf("forwarded Security-Client %q, Require %v, Proxy-Require %q", first.Get("Security-Client"), first.GetAll("Require"), first.Get("Proxy-Require"))
	}
	if strings.Contains(r.Get("WWW-Authenticate"), "ik=") || strings.Contains(r.Get("WWW-Authenticate"), "ck=") {
		t.Errorf("WWW-Authenticate to the UE = %s, want no keys", r.Get("WWW-Authenticate"))
	}
	server, err := ims.IPsec3GPPMechanisms(r, "Security-Server")
	if err != nil || len(server) != len(ims.SupportedTransforms) {
		t.Fatalf("Security-Server = %v (%v)", r.GetAll("Security-Server"), err)
	}
	ps := uint16(h.protected.LocalAddr().Port())
	for i, m := range server {
		if m.Transform != ims.SupportedTransforms[i] || m.PortS != ps || m.PortC == ps || m.SPIC == 1111 || m.SPIS == 2222 || m.SPIC == m.SPIS {
			t.Errorf("Security-Server %d = %+v", i, m)
		}
	}
	sas, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey())
	if !ok || !sas.Temporary || hex.EncodeToString(sas.IK[:]) != testIK || hex.EncodeToString(sas.CK[:]) != testCK ||
		sas.PCSCFMechanism.Algorithm != "hmac-sha2-256-128" || sas.UEMechanism.SPIS != 2222 || sas.PrivateIdentity != impi {
		t.Fatalf("SA table entry = %+v, %v", sas, ok)
	}

	var verify []sip.HeaderField
	for _, v := range r.GetAll("Security-Server") {
		verify = append(verify, sip.HeaderField{Name: "Security-Verify", Value: v})
	}
	r, second := h.register(true, "6629fae49393a05397450978507c4ef1", "600000", verify...)
	if second == nil || r.StatusCode != 200 {
		t.Fatalf("protected REGISTER got %d", r.StatusCode)
	}
	if c, _ := sip.ParseCredentials(second.Get("Authorization")); c.IntegrityProtected() != sip.IntegrityProtectedYes {
		t.Errorf("Authorization to the home network = %s, want integrity-protected yes", second.Get("Authorization"))
	}
	if second.Get("Security-Verify") != "" || second.Get("Security-Client") != "" {
		t.Errorf("forwarded Security-Verify %q, Security-Client %q", second.Get("Security-Verify"), second.Get("Security-Client"))
	}
	if got, want := second.Get("Path"), fmt.Sprintf("<sip:term@pcscf.%s:%d;lr>", domain, ps); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
	sas, ok, _ = h.store.SecurityAssociations(context.Background(), h.ueKey())
	if !ok || sas.Temporary || time.Until(sas.Expires) < time.Hour {
		t.Errorf("after the 200 the entry is %+v, %v; want it established past the registration", sas, ok)
	}
	if _, ok, _ := h.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: h.ueKey()}); !ok {
		t.Error("no association for the protected address of the UE")
	}

	// TS 24.229 5.2.6.3.3 and 5.2.6.3.4 item 4: an initial request over
	// the protected port is Record-Routed with that port.
	invite := &sip.Message{Method: sip.INVITE, RequestURI: "sip:001010000000002@" + domain}
	invite.Add("Max-Forwards", "70")
	invite.Add("Route", fmt.Sprintf("<sip:pcscf.%s:%d;lr>", domain, ps))
	invite.Add("Route", "<sip:orig@scscf."+domain+":5060;lr>")
	invite.Add("From", "<"+impu+">;tag="+sip.NewTag())
	invite.Add("To", "<sip:001010000000002@"+domain+">")
	invite.Add("Call-ID", sip.NewCallID())
	invite.Add("CSeq", "1 INVITE")
	invite.Add("Contact", "<sip:ue@"+h.ueProtected.LocalAddr().String()+">")
	if r := h.send(h.ueProtected, h.protected, invite); r.StatusCode != 486 {
		t.Fatalf("INVITE got %d, want the 486 of the home network", r.StatusCode)
	}
	// The P-CSCF's own reg event SUBSCRIBE may come first.
	forwarded := <-h.received
	for forwarded.Method != sip.INVITE {
		forwarded = <-h.received
	}
	if got, want := forwarded.Get("Record-Route"), fmt.Sprintf("<sip:pcscf.%s:%d;lr>", domain, ps); got != want {
		t.Errorf("Record-Route = %q, want %q", got, want)
	}
	if routes := forwarded.GetAll("Route"); len(routes) != 1 {
		t.Errorf("Route = %v, want the Service-Route only", routes)
	}

	// TS 24.229 5.2.6.4.3: an INVITE to the UE, which comes along the
	// Path, reaches it from the protected server port, with that port
	// in Via and Record-Route.
	terminating := &sip.Message{Method: sip.INVITE, RequestURI: "sip:ue@" + h.ueProtected.LocalAddr().String()}
	terminating.Add("Max-Forwards", "70")
	terminating.Add("Route", fmt.Sprintf("<sip:term@pcscf.%s:%d;lr>", domain, ps))
	terminating.Add("From", "<sip:001010000000002@"+domain+">;tag="+sip.NewTag())
	terminating.Add("To", "<"+impu+">")
	terminating.Add("Call-ID", sip.NewCallID())
	terminating.Add("CSeq", "1 INVITE")
	terminating.Add("Contact", "<sip:caller@"+h.home.LocalAddr().String()+">")
	if r := h.send(h.home, h.protected, terminating); r.StatusCode != 486 {
		t.Fatalf("terminating INVITE got %d, want the 486 of the UE", r.StatusCode)
	}
	atUE := <-h.toUE
	if via, _ := sip.ParseVia(atUE.Get("Via")); via.SentBy() != h.protected.LocalAddr().String() {
		t.Errorf("the INVITE reached the UE with Via %q, want the protected server port %s", atUE.Get("Via"), h.protected.LocalAddr())
	}
	if got, want := atUE.Get("Record-Route"), fmt.Sprintf("<sip:pcscf.%s:%d;lr>", domain, ps); got != want {
		t.Errorf("Record-Route at the UE = %q, want %q", got, want)
	}

	// TS 24.229 5.2.1 NOTE 8 and TS 33.203 7.1 item 3: the same INVITE on
	// the unprotected port is not protected, and the P-CSCF discards it.
	invite.Set("Call-ID", sip.NewCallID())
	unprotected, err := h.ueProtected.SendRequest(invite, sip.Destination{Transport: sip.UDP, Addr: h.unprotected.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	logs.waitFor(t, 1, discardedUnprotected)
	select {
	case r := <-unprotected.Responses():
		t.Errorf("unprotected INVITE got %d, want nothing", r.StatusCode)
	default:
	}

	// TS 33.203 7.4.2a: deregistration removes the entry.
	if r, _ := h.register(true, "6629fae49393a05397450978507c4ef1", "0", verify...); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d", r.StatusCode)
	}
	if _, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey()); ok {
		t.Error("the SA table still has the UE after its deregistration")
	}
}

// TS 24.229 5.2.2.2 item 3 a and RFC 3329 2.3.1: a protected answer whose
// Security-Verify or Security-Client differs from what the P-CSCF sent
// and received gets 494, and does not reach the home network.
func TestIMSAKAChangedSecurityHeaders(t *testing.T) {
	for name, change := range map[string]func([]sip.HeaderField) []sip.HeaderField{
		"Security-Verify changed": func(f []sip.HeaderField) []sip.HeaderField {
			f[0].Value = strings.Replace(f[0].Value, "hmac-sha2-256-128", "hmac-sha-1-96", 1)
			return f
		},
		"Security-Verify left out": func(f []sip.HeaderField) []sip.HeaderField { return f[len(f)-1:] },
		"Security-Client changed": func(f []sip.HeaderField) []sip.HeaderField {
			f[len(f)-1].Value = strings.Replace(f[len(f)-1].Value, "spi-c=1111", "spi-c=1112", 1)
			return f
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAKAHarness(t, true)
			headers := change(h.challenge())
			client := headers[len(headers)-1].Value
			h.securityClient = client
			r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "600000", headers[:len(headers)-1]...)
			if r.StatusCode != 494 || got != nil {
				t.Errorf("got %d and the home network got %v; want 494 and nothing", r.StatusCode, got != nil)
			}
		})
	}
}

// TestProtectedRegisterOverTCP is RFC 3261 18.1.1 for the answer to the
// challenge: over 1300 bytes, it goes over TCP, from a port of the
// moment. TCP and UDP share the security associations (TS 33.203 7.1),
// and the P-CSCF finds them from the sent-by of the Via, the UE's
// protected server port, as it finds the UE of any request over TCP. The
// IP association it then keeps is the one that the UE's later requests
// over UDP match.
func TestProtectedRegisterOverTCP(t *testing.T) {
	h := newAKAHarness(t, true)
	headers := h.challenge()
	headers = append(headers[:len(headers)-1], largerThanUDP)
	r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "600000", headers...)
	if r.StatusCode != 200 || got == nil {
		t.Fatalf("protected REGISTER over TCP got %d, want the 200 of the home network", r.StatusCode)
	}
	if !overTCP(got) {
		t.Fatalf("the REGISTER came with Via %v, want the UE's over TCP", got.GetAll("Via"))
	}
	if sas, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey()); !ok || sas.Temporary {
		t.Errorf("after the 200 the entry is %+v, %v; want it established", sas, ok)
	}
	if _, ok, _ := h.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: h.ueKey()}); !ok {
		t.Error("no association for the protected address of the UE")
	}
}

// registerOverUDP registers the UE with IMS AKA, with REGISTERs small
// enough for UDP, and returns the Security-Verify of its later
// REGISTERs.
func (h *akaHarness) registerOverUDP() []sip.HeaderField {
	h.t.Helper()
	headers := h.challenge()
	verify := headers[:len(headers)-1]
	if r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "600000", verify...); r.StatusCode != 200 || got == nil || overTCP(got) {
		h.t.Fatalf("protected REGISTER got %d, want the 200 of the home network over UDP", r.StatusCode)
	}
	return verify
}

// A deregistration larger than 1300 bytes goes over TCP although the UE
// registered over UDP. The P-CSCF finds the IP association from the
// sent-by of its Via, and the 200 ends it with the security
// associations (TS 24.229 5.2.2.3, TS 33.203 7.4.2a).
func TestDeregistrationOverTCP(t *testing.T) {
	h := newAKAHarness(t, true)
	verify := h.registerOverUDP()
	r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "0", append(verify, largerThanUDP)...)
	if r.StatusCode != 200 || got == nil || !overTCP(got) {
		t.Fatalf("deregistration got %d, want the 200 of the home network to a REGISTER over TCP", r.StatusCode)
	}
	if a, ok, _ := h.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: h.ueKey()}); ok && a.Expires.After(time.Now()) {
		t.Errorf("the IP association of the UE lasts until %v after its deregistration", a.Expires)
	}
	if _, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey()); ok {
		t.Error("the SA table still has the UE after its deregistration")
	}
}

// TS 24.229 5.2.2.3: a 500 or a 504 to a REGISTER deletes the IP
// association of the UE, also when the REGISTER went over TCP because
// it is larger than 1300 bytes.
func TestNetworkFailureToARegisterOverTCP(t *testing.T) {
	for _, status := range []int{500, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			h := newAKAHarness(t, true)
			verify := h.registerOverUDP()
			h.failWith.Store(int32(status))
			r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "600000", append(verify, largerThanUDP)...)
			if r.StatusCode != status || got == nil || !overTCP(got) {
				t.Fatalf("REGISTER got %d, want the %d of the home network to a REGISTER over TCP", r.StatusCode, status)
			}
			if a, ok, _ := h.store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: h.ueKey()}); ok {
				t.Errorf("the IP association of the UE is still there after the %d: %+v", status, a)
			}
		})
	}
}

// TS 33.203 7.1 item 3: a REGISTER to the protected port from an
// address without security associations is refused.
func TestIMSAKAWithoutSecurityAssociations(t *testing.T) {
	h := newAKAHarness(t, true)
	if r, got := h.register(true, "6629fae49393a05397450978507c4ef1", "600000"); r.StatusCode != 403 || got != nil {
		t.Errorf("got %d, want 403 and nothing forwarded", r.StatusCode)
	}
}

// TS 24.229 5.2.2.2 item 2 of the 401: without ik and ck the 401 does
// not reach the UE.
func TestIMSAKAChallengeWithoutKeys(t *testing.T) {
	h := newAKAHarness(t, true)
	h.noKeys.Store(true)
	if r, _ := h.register(false, "", "600000"); r.StatusCode != 500 {
		t.Errorf("got %d, want 500", r.StatusCode)
	}
	if _, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey()); ok {
		t.Error("temporary associations without keys")
	}
}

// TS 33.203 7.3.1.2 and 7.3.1.3: the unprotected REGISTER with which the
// UE reports a challenge it found invalid ends the temporary
// associations.
func TestIMSAKAFailureReportEndsTheTemporaryAssociations(t *testing.T) {
	h := newAKAHarness(t, true)
	h.challenge()
	h.securityClient = strings.Replace(h.securityClient, "spi-c=1111;spi-s=2222", "spi-c=3333;spi-s=4444", 1)
	h.noKeys.Store(true) // the fake home network has no new challenge to give
	h.register(false, "", "600000")
	if sas, ok, _ := h.store.SecurityAssociations(context.Background(), h.ueKey()); ok {
		t.Errorf("SA table entry after the failure report: %+v", sas)
	}
}

// TS 33.203 7.3.2.1: a Security-Client with none of the P-CSCF's
// algorithms is refused, with the P-CSCF's list.
func TestIMSAKANoAlgorithmsInCommon(t *testing.T) {
	h := newAKAHarness(t, true)
	h.securityClient = strings.Replace(h.securityClient, "hmac-sha2-256-128", "hmac-sha-1-96", 1)
	r, got := h.register(false, "", "600000")
	if r.StatusCode != 494 || got != nil || !strings.Contains(r.Get("Security-Server"), "alg=hmac-sha2-256-128") {
		t.Errorf("got %d with Security-Server %q; want 494 with the P-CSCF's algorithms, and nothing forwarded", r.StatusCode, r.Get("Security-Server"))
	}
}

// RFC 3261 16.3 step 5: a P-CSCF without a protected server port does
// not support sec-agree.
func TestSecAgreeWithoutAProtectedPort(t *testing.T) {
	h := newAKAHarness(t, false)
	r, got := h.register(false, "", "600000")
	if r.StatusCode != 420 || !slices.Contains(r.GetAll("Unsupported"), sip.OptionSecAgree) || got != nil {
		t.Errorf("got %d with Unsupported %v; want 420 with sec-agree, and nothing forwarded", r.StatusCode, r.GetAll("Unsupported"))
	}
}
