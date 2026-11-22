package scscf

import (
	"context"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file run the S-CSCF with application servers (TS
// 24.229 5.4.1.7, 5.4.3.2 and 5.4.3.3). The user has a SIP URI and a
// tel URI, and the application servers are fake ones on the loopback.

const (
	telURI = "tel:+12025550101"
	msisdn = "12025550101"
	asHost = "ipsmgw." + domain
	asURI  = "sip:" + asHost + ":5060"
)

// registerCriterion sends every REGISTER of the user to the server,
// with the user's MSISDN as ServiceInfo.
func registerCriterion(priority int, server string, d ifc.DefaultHandling) ifc.InitialFilterCriteria {
	return ifc.InitialFilterCriteria{
		Priority:          priority,
		TriggerPoint:      &ifc.TriggerPoint{SPT: []ifc.SPT{{Group: []int{0}, Method: "REGISTER"}}},
		ApplicationServer: ifc.ApplicationServer{ServerName: server, DefaultHandling: d, ServiceInfo: hss.MSISDNPlaceholder},
	}
}

// smsCriterion sends the SMS that the user sends to the server.
func smsCriterion(priority int, server string, d ifc.DefaultHandling) ifc.InitialFilterCriteria {
	originating := ifc.OriginatingRegistered
	return ifc.InitialFilterCriteria{
		Priority: priority,
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "MESSAGE"},
			{Group: []int{1}, SIPHeader: &ifc.SIPHeader{Header: "^Content-Type$", Content: `^application/vnd\.3gpp\.sms$`}},
			{Group: []int{2}, SessionCase: &originating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: server, DefaultHandling: d},
	}
}

// serviceHarness is an S-CSCF whose HSS gives the user criteria, with
// next hops for the I-CSCF (the home domain) and the P-CSCF, and
// whatever other hosts the test names.
type serviceHarness struct {
	*harness
	icscf, pcscf <-chan *sip.Message
}

func newServiceHarness(t *testing.T, criteria []ifc.InitialFilterCriteria, hosts map[string]netip.AddrPort) *serviceHarness {
	t.Helper()
	return newServiceHarnessWith(t, Config{}, criteria, hosts)
}

// newServiceHarnessWith is newServiceHarness with the S-CSCF configured
// as cfg says. ENUM knows the number of subscriber 2.
func newServiceHarnessWith(t *testing.T, cfg Config, criteria []ifc.InitialFilterCriteria, hosts map[string]netip.AddrPort) *serviceHarness {
	t.Helper()
	icscfStack, icscfGot := nextHop(t)
	pcscfStack, pcscfGot := nextHop(t)
	resolver := fakeResolver{
		hosts: map[string]netip.AddrPort{domain: icscfStack.LocalAddr(), "pcscf." + domain: pcscfStack.LocalAddr()},
		enum:  map[string]string{"+12025550102": "sip:001010000000002@" + domain},
	}
	for host, addr := range hosts {
		resolver.hosts[host] = addr
	}
	h := newHarnessWithHSS(t, cfg, resolver, hss.Config{InitialFilterCriteria: criteria},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, telURI}, Password: password})
	return &serviceHarness{harness: h, icscf: icscfGot, pcscf: pcscfGot}
}

// TestRegistrationKeepsTheInitialFilterCriteria keeps the state of the
// profile outside the node: the criteria of the SAA are kept in the Store with the
// registration, not only in this S-CSCF's memory.
func TestRegistrationKeepsTheInitialFilterCriteria(t *testing.T) {
	criteria := []ifc.InitialFilterCriteria{smsCriterion(1, asURI, ifc.SessionContinued), registerCriterion(0, asURI, ifc.SessionContinued)}
	h := newServiceHarness(t, criteria, nil)
	h.registerUser(sip.NewCallID())
	reg, ok, _ := h.store.Registration(context.Background(), impi)
	if !ok {
		t.Fatal("no registration in the store")
	}
	want := []ifc.InitialFilterCriteria{criteria[0], criteria[1]}
	want[1].ApplicationServer.ServiceInfo = msisdn
	if !reflect.DeepEqual(reg.InitialFilterCriteria, want) {
		t.Errorf("stored criteria\n%+v\nwant\n%+v", reg.InitialFilterCriteria, want)
	}
}

// fakeAS is an application server on the loopback. It records each
// request it gets, and answers with the status that answer picks.
func fakeAS(t *testing.T, answer func(*sip.Message) int) (*sip.Stack, <-chan *sip.Message) {
	t.Helper()
	got := make(chan *sip.Message, 8)
	s := newStack(t, func(st *sip.ServerTransaction) {
		got <- st.Request
		st.Respond(sip.NewResponse(st.Request, answer(st.Request), sip.NewTag()))
	})
	return s, got
}

func answers(code int) func(*sip.Message) int { return func(*sip.Message) int { return code } }

// TestThirdPartyRegister is TS 24.229 5.4.1.7 and 5.4.1.7A: after the
// registration, and again after the deregistration, the server of the
// matching criterion gets a REGISTER from the S-CSCF. The first carries
// the ServiceInfo, with the user's MSISDN in it.
func TestThirdPartyRegister(t *testing.T) {
	as, got := fakeAS(t, answers(200))
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{registerCriterion(0, asURI, ifc.SessionContinued)},
		map[string]netip.AddrPort{asHost: as.LocalAddr()})
	h.extra = []sip.HeaderField{
		{Name: "P-Visited-Network-ID", Value: domain},
		{Name: "P-Charging-Vector", Value: "icid-value=icid-1;orig-ioi=" + domain},
	}
	callID := sip.NewCallID()
	h.registerUser(callID)

	m := received(t, got)
	scscfURI := "<sip:scscf." + domain + ":5060>"
	for name, want := range map[string]string{
		"To":                   "<" + impu + ">",
		"Contact":              scscfURI,
		"Expires":              "3600",
		"P-Visited-Network-ID": domain,
		"P-Charging-Vector":    "icid-value=icid-1;orig-ioi=" + domain,
		"Content-Type":         ims.IMS3GPPContentType,
	} {
		if got := m.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if m.Method != sip.REGISTER || m.RequestURI != asURI {
		t.Errorf("got %s %s, want REGISTER %s", m.Method, m.RequestURI, asURI)
	}
	if from, err := sip.ParseNameAddr(m.Get("From")); err != nil || "<"+from.URI+">" != scscfURI {
		t.Errorf("From = %q, want the S-CSCF", m.Get("From"))
	}
	if info, err := ims.ParseServiceInfo(m.Body); err != nil || info != msisdn {
		t.Errorf("service-info = %q, %v; want %q", info, err, msisdn)
	}

	dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
	if r := h.register(callID, 3, 0, dereg); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d", r.StatusCode)
	}
	m = received(t, got)
	if m.Get("Expires") != "0" || len(m.Body) != 0 || m.Get("P-Charging-Vector") != "" {
		t.Errorf("third-party deregistration has Expires %q, body %q and P-Charging-Vector %q; want 0 and neither",
			m.Get("Expires"), m.Body, m.Get("P-Charging-Vector"))
	}
}

// TestThirdPartyRegisterThatFails is the default handling of TS 24.229
// 5.4.1.7: when the server answers 503, SESSION_CONTINUED leaves the
// user registered, and SESSION_TERMINATED deregisters the user, which
// tells the HSS and the servers.
func TestThirdPartyRegisterThatFails(t *testing.T) {
	for _, tt := range []struct {
		handling   ifc.DefaultHandling
		registered bool
	}{
		{ifc.SessionContinued, true},
		{ifc.SessionTerminated, false},
	} {
		t.Run(tt.handling.String(), func(t *testing.T) {
			as, got := fakeAS(t, answers(503))
			h := newServiceHarness(t, []ifc.InitialFilterCriteria{registerCriterion(0, asURI, tt.handling)},
				map[string]netip.AddrPort{asHost: as.LocalAddr()})
			h.registerUser(sip.NewCallID())
			received(t, got)
			want := []cx.ServerAssignmentType{cx.AssignRegistration}
			if !tt.registered {
				want = append(want, cx.AdministrativeDeregistration)
				if m := received(t, got); m.Get("Expires") != "0" {
					t.Errorf("second third-party REGISTER has Expires %q, want the deregistration", m.Get("Expires"))
				}
			}
			deadline := time.Now().Add(3 * time.Second)
			for !slices.Equal(h.hss.assignments(), want) && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			if got := h.hss.assignments(); !slices.Equal(got, want) {
				t.Errorf("SARs = %v, want %v", got, want)
			}
			// A call of the user goes on to the I-CSCF while the user is
			// registered, and gets 403 once it is not.
			status := 486
			if !tt.registered {
				status = 403
			}
			if r := h.invite("sip:001010000000002@"+domain, orig, impu); r.StatusCode != status {
				t.Errorf("INVITE of the user got %d, want %d", r.StatusCode, status)
			}
		})
	}
}

// proxyAS is an application server that acts as a SIP proxy (TS 24.229
// 5.7.4): it removes its own Route and sends the request back to the
// S-CSCF that the next Route names, with the Request-URI changed to
// retarget when that is set.
type proxyAS struct {
	stack *sip.Stack
	got   chan *sip.Message
	// scscf is where the S-CSCF listens. The S-CSCF is made after the
	// server, and reads it from another goroutine, hence the atomic.
	scscf    atomic.Pointer[netip.AddrPort]
	retarget string
}

func newProxyAS(t *testing.T, host, retarget string) *proxyAS {
	t.Helper()
	p := &proxyAS{got: make(chan *sip.Message, 8), retarget: retarget}
	own, _ := ims.LooseRoute("sip:" + host + ":5060")
	p.stack = newStack(t, func(st *sip.ServerTransaction) {
		p.got <- st.Request
		out := st.Request.Clone()
		sip.DecrementMaxForwards(out)
		sip.PopOwnRoute(out, own)
		if p.retarget != "" {
			out.RequestURI = p.retarget
		}
		final, err := st.Proxy(out, sip.Destination{Transport: sip.UDP, Addr: *p.scscf.Load()})
		ims.Finish(st, final, err)
	})
	return p
}

func (p *proxyAS) serve(h *serviceHarness) {
	addr := h.scscf.LocalAddr()
	p.scscf.Store(&addr)
}

func none(t *testing.T, got <-chan *sip.Message, who string) {
	t.Helper()
	select {
	case m := <-got:
		t.Errorf("%s got %s %s", who, m.Method, m.RequestURI)
	case <-time.After(100 * time.Millisecond):
	}
}

// odiOf is the original dialog identifier in the second Route value,
// the S-CSCF's own, of a request that an application server got.
func odiOf(t *testing.T, m *sip.Message) string {
	t.Helper()
	routes := m.GetAll("Route")
	if len(routes) != 2 {
		t.Fatalf("Route = %q, want the server and the S-CSCF", routes)
	}
	na, _ := sip.ParseNameAddr(routes[1])
	u, err := sip.ParseURI(na.URI)
	if err != nil || !strings.EqualFold(u.Host, "scscf."+domain) {
		t.Fatalf("second Route %q is not the S-CSCF", routes[1])
	}
	odi, ok := u.Params.Get(odiParam)
	if !ok || odi == "" {
		t.Fatalf("second Route %q has no original dialog identifier", routes[1])
	}
	return odi
}

// TestOriginatingSMSGoesToTheApplicationServer is (b) of the SMS
// design: TS 24.229 5.4.3.2 step 5 sends the SMS of the user to the
// IP-SM-GW, which answers it as a terminating UA (5.7.2). The answer
// goes to the user, and nothing goes on to the destination. A call
// does not match the criterion.
func TestOriginatingSMSGoesToTheApplicationServer(t *testing.T) {
	as, got := fakeAS(t, answers(202))
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)},
		map[string]netip.AddrPort{asHost: as.LocalAddr()})
	h.registerUser(sip.NewCallID())

	if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 202 {
		t.Fatalf("SMS got %d, want the 202 of the application server", r.StatusCode)
	}
	m := received(t, got)
	if routes := m.GetAll("Route"); len(routes) != 2 || routes[0] != "<"+asURI+";lr>" {
		t.Errorf("Route = %q, want the server first", routes)
	}
	odiOf(t, m)
	if pai := m.GetAll("P-Asserted-Identity"); !slices.Equal(pai, []string{"<" + impu + ">", "<" + telURI + ">"}) {
		t.Errorf("P-Asserted-Identity = %q, want the SIP URI and the tel URI", pai)
	}
	if rr := m.Get("Record-Route"); rr != "" {
		t.Errorf("Record-Route %q in a MESSAGE", rr)
	}
	none(t, h.icscf, "the I-CSCF")

	if r := h.invite("sip:001010000000002@"+domain, orig, impu); r.StatusCode != 486 {
		t.Fatalf("call got %d, want the 486 of the I-CSCF", r.StatusCode)
	}
	received(t, h.icscf)
	none(t, got, "the application server")
}

// TestRequestBackFromAProxyAS is (c): an application server that acts
// as a proxy sends the request back with the original dialog
// identifier, and the S-CSCF goes on with the next criterion, or, with
// none left, to the destination (TS 24.229 5.4.3.2 step 3).
func TestRequestBackFromAProxyAS(t *testing.T) {
	proxy := newProxyAS(t, "as1."+domain, "")
	last, lastGot := fakeAS(t, answers(202))
	criteria := []ifc.InitialFilterCriteria{
		smsCriterion(0, "sip:as1."+domain+":5060", ifc.SessionContinued),
		smsCriterion(1, "sip:as2."+domain+":5060", ifc.SessionContinued),
	}
	hosts := map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr(), "as2." + domain: last.LocalAddr()}

	t.Run("next criterion", func(t *testing.T) {
		h := newServiceHarness(t, criteria, hosts)
		proxy.serve(h)
		h.registerUser(sip.NewCallID())
		if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 202 {
			t.Fatalf("SMS got %d, want the 202 of the second server", r.StatusCode)
		}
		first, second := odiOf(t, received(t, proxy.got)), odiOf(t, received(t, lastGot))
		if first == second {
			t.Errorf("both servers got the original dialog identifier %s, want one each", first)
		}
		none(t, h.icscf, "the I-CSCF")
	})
	t.Run("destination", func(t *testing.T) {
		h := newServiceHarness(t, criteria[:1], hosts)
		proxy.serve(h)
		h.registerUser(sip.NewCallID())
		if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 486 {
			t.Fatalf("SMS got %d, want the 486 of the I-CSCF through the server", r.StatusCode)
		}
		received(t, proxy.got)
		if m := received(t, h.icscf); m.RequestURI != "sip:sc@"+domain || m.Get("Route") != "" {
			t.Errorf("I-CSCF got Request-URI %q and Route %q", m.RequestURI, m.Get("Route"))
		}
	})
}

// TestFailureAfterTheRequestCameBack: once the server has sent the
// request back, a 5xx that it passes on is the answer of the session.
// The S-CSCF gives it to the user instead of applying the default
// handling of the server's criterion (TS 24.229 5.4.3.2).
func TestFailureAfterTheRequestCameBack(t *testing.T) {
	proxy := newProxyAS(t, "as1."+domain, "")
	failing, failingGot := fakeAS(t, answers(503))
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{
		smsCriterion(0, "sip:as1."+domain+":5060", ifc.SessionContinued),
		smsCriterion(1, "sip:as2."+domain+":5060", ifc.SessionTerminated),
	}, map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr(), "as2." + domain: failing.LocalAddr()})
	proxy.serve(h)
	h.registerUser(sip.NewCallID())
	if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 503 {
		t.Fatalf("SMS got %d, want the 503 of the second server", r.StatusCode)
	}
	received(t, proxy.got)
	received(t, failingGot)
	none(t, failingGot, "the second server, a second time,")
	none(t, h.icscf, "the I-CSCF")
}

// TestDefaultHandling is (d): the server of the first criterion cannot
// be reached (DNS knows no address for it) or answers 503. With
// SESSION_CONTINUED the SMS goes on to the destination; with
// SESSION_TERMINATED the user gets the 503, or 504 when no answer came
// (TS 24.229 5.4.3.2).
func TestDefaultHandling(t *testing.T) {
	failing, _ := fakeAS(t, answers(503))
	for _, tt := range []struct {
		name     string
		host     string
		handling ifc.DefaultHandling
		want     int
	}{
		{"unreachable, continued", "absent." + domain, ifc.SessionContinued, 486},
		{"unreachable, terminated", "absent." + domain, ifc.SessionTerminated, 504},
		{"503, continued", "failing." + domain, ifc.SessionContinued, 486},
		{"503, terminated", "failing." + domain, ifc.SessionTerminated, 503},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, "sip:"+tt.host+":5060", tt.handling)},
				map[string]netip.AddrPort{"failing." + domain: failing.LocalAddr()})
			h.registerUser(sip.NewCallID())
			if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != tt.want {
				t.Fatalf("SMS got %d, want %d", r.StatusCode, tt.want)
			}
			if tt.want == 486 {
				received(t, h.icscf)
			} else {
				none(t, h.icscf, "the I-CSCF")
			}
		})
	}
}

// TestMessageFromTheApplicationServerReachesTheUser is (e): the
// IP-SM-GW delivers an SMS as an originating UA (TS 24.229 5.7.3). It
// comes through the I-CSCF with the server as P-Asserted-Identity. The
// user's criteria hold no terminating one for MESSAGE (TS 23.218
// 5.2.3 NOTE 5), so it goes to the contact and not back to the server.
func TestMessageFromTheApplicationServerReachesTheUser(t *testing.T) {
	as, got := fakeAS(t, answers(202))
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{
		registerCriterion(0, asURI, ifc.SessionContinued),
		smsCriterion(1, asURI, ifc.SessionContinued),
	}, map[string]netip.AddrPort{asHost: as.LocalAddr()})
	h.registerUser(sip.NewCallID())
	received(t, got) // the third-party REGISTER

	m := sms(impu, "<sip:scscf."+domain+":5060;lr>", "")
	m.Set("From", "<"+asURI+">;tag="+sip.NewTag())
	m.Set("P-Asserted-Identity", "<"+asURI+">")
	if r := h.send(m); r.StatusCode != 486 {
		t.Fatalf("SMS to the user got %d, want the 486 of the P-CSCF", r.StatusCode)
	}
	toUE := received(t, h.pcscf)
	if toUE.RequestURI != ueContact || toUE.Get("P-Called-Party-ID") != "<"+impu+">" {
		t.Errorf("P-CSCF got Request-URI %q and P-Called-Party-ID %q", toUE.RequestURI, toUE.Get("P-Called-Party-ID"))
	}
	if pai := toUE.GetAll("P-Asserted-Identity"); !slices.Equal(pai, []string{"<" + asURI + ">"}) {
		t.Errorf("P-Asserted-Identity = %q, want the server alone", pai)
	}
	none(t, got, "the application server")
}

// TestTerminatingApplicationServer is TS 24.229 5.4.3.3 with a proxy
// server for calls to the user: the call comes back with the original
// dialog identifier and goes to the contact. When the server changed
// the Request-URI (step 3), the S-CSCF routes the call to the new
// target instead.
func TestTerminatingApplicationServer(t *testing.T) {
	terminating := ifc.TerminatingRegistered
	criterion := ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "INVITE"},
			{Group: []int{1}, SessionCase: &terminating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:as1." + domain + ":5060"},
	}
	for _, tt := range []struct {
		retarget string
		via      string
	}{
		{"", "P-CSCF"},
		// The alias of the user is still the user (step 3 c).
		{telURI, "P-CSCF"},
		{"sip:001010000000002@" + domain, "I-CSCF"},
	} {
		proxy := newProxyAS(t, "as1."+domain, tt.retarget)
		h := newServiceHarness(t, []ifc.InitialFilterCriteria{criterion}, map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr()})
		proxy.serve(h)
		h.registerUser(sip.NewCallID())
		if r := h.invite(impu, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 486 {
			t.Fatalf("retarget %q: call got %d, want 486", tt.retarget, r.StatusCode)
		}
		received(t, proxy.got)
		next := h.pcscf
		if tt.via == "I-CSCF" {
			next = h.icscf
		}
		m := received(t, next)
		if tt.via == "P-CSCF" && m.RequestURI != ueContact {
			t.Errorf("retarget %q: P-CSCF got Request-URI %q, want the contact", tt.retarget, m.RequestURI)
		}
		if tt.via == "I-CSCF" && m.RequestURI != tt.retarget {
			t.Errorf("retarget %q: I-CSCF got Request-URI %q", tt.retarget, m.RequestURI)
		}
	}
}

// TestPServedUser is TS 24.229 5.4.3.2 step 5 b and 5.4.3.3 step 4 c: a
// request to an application server names the served user with the
// session case and the registration state (RFC 5502). The request that
// leaves for the destination or the UE has none (5.4.3.2 step 14A,
// 5.4.3.3 step 13A). A P-Served-User that came with an originating
// request names the served user (5.4.3.2 step 1 a), whatever
// P-Asserted-Identity says, and one for a user who is not registered
// here gets 403.
func TestPServedUser(t *testing.T) {
	terminating := ifc.TerminatingRegistered
	callCriterion := ifc.InitialFilterCriteria{
		Priority: 1,
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "INVITE"},
			{Group: []int{1}, SessionCase: &terminating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:as1." + domain + ":5060"},
	}
	proxy := newProxyAS(t, "as1."+domain, "")
	h := newServiceHarness(t, []ifc.InitialFilterCriteria{smsCriterion(0, "sip:as1."+domain+":5060", ifc.SessionContinued), callCriterion},
		map[string]netip.AddrPort{"as1." + domain: proxy.stack.LocalAddr()})
	proxy.serve(h)
	h.registerUser(sip.NewCallID())

	t.Run("originating", func(t *testing.T) {
		h.send(sms("sip:sc@"+domain, orig, impu))
		if got, want := received(t, proxy.got).GetAll("P-Served-User"), []string{"<" + impu + ">;sescase=orig;regstate=reg"}; !slices.Equal(got, want) {
			t.Errorf("the server got P-Served-User %q, want %q", got, want)
		}
		if got := received(t, h.icscf).Get("P-Served-User"); got != "" {
			t.Errorf("the I-CSCF got P-Served-User %q", got)
		}
	})
	t.Run("originating on behalf of the user", func(t *testing.T) {
		m := sms("sip:sc@"+domain, orig, "sip:announcer@"+domain)
		m.Add("P-Served-User", "<"+telURI+">")
		h.send(m)
		toAS := received(t, proxy.got)
		if got, want := toAS.GetAll("P-Served-User"), []string{"<" + telURI + ">;sescase=orig;regstate=reg"}; !slices.Equal(got, want) {
			t.Errorf("the server got P-Served-User %q, want %q", got, want)
		}
		if got, want := toAS.GetAll("P-Asserted-Identity"), []string{"<sip:announcer@" + domain + ">"}; !slices.Equal(got, want) {
			t.Errorf("the server got P-Asserted-Identity %q, want %q as it came", got, want)
		}
		received(t, h.icscf)
	})
	t.Run("on behalf of a user who is not registered", func(t *testing.T) {
		m := sms("sip:sc@"+domain, orig, impu)
		m.Add("P-Served-User", "<sip:someone-else@"+domain+">;sescase=orig;regstate=reg")
		if r := h.send(m); r.StatusCode != 403 {
			t.Errorf("answered %d, want 403", r.StatusCode)
		}
		none(t, proxy.got, "the application server")
	})
	t.Run("terminating", func(t *testing.T) {
		h.invite(telURI, "<sip:scscf."+domain+":5060;lr>", "")
		if got, want := received(t, proxy.got).GetAll("P-Served-User"), []string{"<" + telURI + ">;sescase=term;regstate=reg"}; !slices.Equal(got, want) {
			t.Errorf("the server got P-Served-User %q, want %q", got, want)
		}
		if got := received(t, h.pcscf).Get("P-Served-User"); got != "" {
			t.Errorf("the P-CSCF got P-Served-User %q", got)
		}
	})
}

// TestApplicationServerSubscribesToTheRegEvent is TS 24.229 5.4.2.1.1
// step 1: a server named in the user's criteria may subscribe to the
// user's registration, as the IP-SM-GW does after the third-party
// REGISTER (TS 23.204 6.1). Another server may not.
func TestApplicationServerSubscribesToTheRegEvent(t *testing.T) {
	h := newSubscriberHarnessWithHSS(t, Config{}, hss.Config{InitialFilterCriteria: []ifc.InitialFilterCriteria{
		registerCriterion(0, asURI, ifc.SessionContinued),
	}})
	h.registerUser(sip.NewCallID())
	_, got := h.subscribe("<"+asURI+">", "")
	if n, _ := nextNotify(t, got); n.State.State != sip.SubscriptionActive {
		t.Errorf("the server's subscription: %+v", n.State)
	}
	other, _ := h.subscribe("<sip:other-as."+domain+":5060>", "")
	select {
	case <-other.Done():
		if err := other.Err(); err == nil || !strings.Contains(err.Error(), "403") {
			t.Errorf("another server's subscription ended with %v, want a 403", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("another server's SUBSCRIBE was not refused")
	}
}
