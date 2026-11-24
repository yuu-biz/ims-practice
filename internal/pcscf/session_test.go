package pcscf

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/openapitest"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

type sessionHarness struct {
	t     *testing.T
	relay *agw.Relay
	pcscf *sip.Stack
	ue    *sip.Stack
	store *MemoryStore
	p     *PCSCF
	scscf <-chan *sip.Message
	toUE  <-chan *sip.Message
	// metrics is where this P-CSCF counts, for a test that reads the
	// session measurements of a call.
	metrics *metrics.Registry
	// release lets the next hops send their final 486, after the test
	// has looked at the relay while the call is up.
	release chan struct{}
	// byeAnswers is the status code of the fake S-CSCF's answer to each
	// BYE.
	byeAnswers chan int
}

// newSessionHarness has a registered UE, a P-CSCF with a real media
// relay, and a fake S-CSCF that answers every INVITE with a 183 that
// carries SDP, then a 486, and each BYE as byeAnswers says.
func newSessionHarness(t *testing.T) *sessionHarness {
	t.Helper()
	minPort, maxPort := rtptest.PortRange(t, 20)
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	// A test may end while its call is still being torn down. What the
	// relay still holds then goes once the stacks are closed (cleanups
	// run last first), so that the next test finds the ports free.
	t.Cleanup(func() {
		for _, c := range relay.Contexts() {
			relay.Subtract(c.ID)
		}
	})
	api := httptest.NewServer(openapitest.Load(t, "../agw/openapi.yaml").Handler(t, agw.Handler(relay)))
	t.Cleanup(api.Close)
	h := newSessionHarnessWith(t, []MediaRelay{{Name: api.URL, Control: agw.NewClient(api.URL)}})
	h.relay = relay
	return h
}

// newSessionHarnessWith is newSessionHarness with the P-CSCF driving
// relays instead.
func newSessionHarnessWith(t *testing.T, relays []MediaRelay) *sessionHarness {
	t.Helper()
	h := &sessionHarness{t: t, store: NewMemoryStore(), metrics: metrics.NewRegistry(),
		release: make(chan struct{}), byeAnswers: make(chan int, 4)}

	scscfGot := make(chan *sip.Message, 4)
	h.scscf = scscfGot
	answer := sdp.Offer("9", netip.MustParseAddrPort("192.0.2.99:6000"))
	scscf := newStack(t, func(st *sip.ServerTransaction) {
		scscfGot <- st.Request
		if st.Request.Method == sip.BYE {
			st.Respond(sip.NewResponse(st.Request, <-h.byeAnswers, "callee"))
			return
		}
		ringing := sip.NewResponse(st.Request, 183, "callee")
		ringing.Body = answer.Bytes()
		st.Respond(ringing)
		<-h.release
		st.Respond(sip.NewResponse(st.Request, 486, "callee"))
	})
	toUE := make(chan *sip.Message, 4)
	h.toUE = toUE
	h.ue = newStack(t, func(st *sip.ServerTransaction) {
		toUE <- st.Request
		<-h.release
		st.Respond(sip.NewResponse(st.Request, 486, "ue"))
	})
	locate := locatorFunc(func(host string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: scscf.LocalAddr()}}
	})
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, Metrics: h.metrics},
		locate, h.store, relays, quiet())
	h.p = p
	h.pcscf = newPCSCFStack(t, p)
	p.SetStack(h.pcscf)
	p.store.PutAssociation(context.Background(), Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: h.ue.LocalAddr()},
		SentBy:           h.ue.LocalAddr().String(),
		Contact:          "sip:001010000000001@" + h.ue.LocalAddr().String(),
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu, "tel:+12025550101"},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	})
	return h
}

// send sends m through the P-CSCF and returns the responses as they
// come, closing the channel after the final one.
func (h *sessionHarness) send(from *sip.Stack, m *sip.Message) <-chan *sip.Message {
	h.t.Helper()
	ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: h.pcscf.LocalAddr()})
	if err != nil {
		h.t.Fatal(err)
	}
	return ct.Responses()
}

// arrives waits for the request that the P-CSCF passes on to a next
// hop, which puts what it gets on at. When it does not come, the test
// fails with what the sender got instead, such as a response of the
// P-CSCF's own, rather than wait for ever. It leaves responses alone
// while it waits, for the test to read.
func arrives(t *testing.T, at, responses <-chan *sip.Message, hop string) *sip.Message {
	t.Helper()
	select {
	case m := <-at:
		return m
	case <-time.After(3 * time.Second):
	}
	var got []string
	for {
		select {
		case r, ok := <-responses:
			if ok {
				got = append(got, strconv.Itoa(r.StatusCode))
				continue
			}
		default:
		}
		break
	}
	t.Fatalf("the request did not reach the %s; the sender got %v", hop, got)
	return nil
}

func next(t *testing.T, responses <-chan *sip.Message) *sip.Message {
	t.Helper()
	select {
	case r, ok := <-responses:
		if !ok {
			t.Fatal("no more responses")
		}
		return r
	case <-time.After(3 * time.Second):
		t.Fatal("no response")
		return nil
	}
}

func invite(requestURI string, routes ...string) *sip.Message {
	m := &sip.Message{Method: sip.INVITE, RequestURI: requestURI}
	m.Add("Max-Forwards", "70")
	for _, r := range routes {
		m.Add("Route", r)
	}
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+requestURI+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 INVITE")
	m.Add("Contact", "<sip:001010000000001@127.0.0.1>")
	m.Add("Content-Type", "application/sdp")
	m.Body = sdp.Offer("1", netip.MustParseAddrPort("192.0.2.10:4000")).Bytes()
	return m
}

func audio(t *testing.T, body []byte) netip.AddrPort {
	t.Helper()
	s, err := sdp.Parse(body)
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := s.Audio()
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestOriginatingCallGoesThroughTheRelay(t *testing.T) {
	h := newSessionHarness(t)
	// The UE preloads only the P-CSCF; the P-CSCF puts the Service-Route
	// in (TS 24.229 5.2.6.3.3 step 2 ii b).
	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))

	got := arrives(t, h.scscf, responses, "S-CSCF")
	if got.Get("Route") != "<sip:orig@scscf."+domain+":5060;lr>" {
		t.Errorf("Route = %q, want the Service-Route", got.Get("Route"))
	}
	if got.Get("P-Asserted-Identity") != "<"+impu+">" {
		t.Errorf("P-Asserted-Identity = %q", got.Get("P-Asserted-Identity"))
	}
	if got.Get("Record-Route") != "<sip:pcscf."+domain+":5060;lr>" {
		t.Errorf("Record-Route = %q", got.Get("Record-Route"))
	}
	contexts := h.relay.Contexts()
	if len(contexts) != 1 {
		t.Fatalf("%d relay contexts, want 1", len(contexts))
	}
	c := contexts[0]
	if offer := audio(t, got.Body); offer != c.Terminations[agw.Core].Local {
		t.Errorf("offer towards the network names %v, want the core termination %v", offer, c.Terminations[agw.Core].Local)
	}
	if remote := c.Terminations[agw.Access].Remote; remote != netip.MustParseAddrPort("192.0.2.10:4000") {
		t.Errorf("access termination sends to %v, want the UE's SDP address", remote)
	}
	ringing := next(t, responses)
	if ringing.StatusCode != 183 {
		t.Fatalf("UE got %d, want 183", ringing.StatusCode)
	}
	if answer := audio(t, ringing.Body); answer != c.Terminations[agw.Access].Local {
		t.Errorf("answer to the UE names %v, want the access termination %v", answer, c.Terminations[agw.Access].Local)
	}
	if remote := h.relay.Contexts()[0].Terminations[agw.Core].Remote; remote != netip.MustParseAddrPort("192.0.2.99:6000") {
		t.Errorf("core termination sends to %v, want the callee's SDP address", remote)
	}
	// The 486 ends the call, and with it the relay context.
	close(h.release)
	if r := next(t, responses); r.StatusCode != 486 {
		t.Fatalf("UE got %d, want 486", r.StatusCode)
	}
	if !came(3*time.Second, func() bool { return len(h.relay.Contexts()) == 0 }) {
		t.Errorf("%d relay contexts left 3 s after the 486", len(h.relay.Contexts()))
	}
}

// TestCallIsRefusedWhenTheRelayHasNoFreePort: another program holds the
// one port pair of the relay, so the relay answers no_port. The P-CSCF
// answers the UE 500 and passes nothing on. The test holds the ports
// itself; if some other program already does, the case is the same.
func TestCallIsRefusedWhenTheRelayHasNoFreePort(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 2)
	for _, port := range []uint16{minPort, maxPort} {
		if held, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port))); err == nil {
			t.Cleanup(func() { held.Close() })
		}
	}
	relay, err := agw.NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, nil, quiet())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(openapitest.Load(t, "../agw/openapi.yaml").Handler(t, agw.Handler(relay)))
	t.Cleanup(api.Close)
	h := newSessionHarnessWith(t, []MediaRelay{{Name: api.URL, Control: agw.NewClient(api.URL)}})
	close(h.release)
	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	if r := final(t, responses, 3*time.Second); r.StatusCode != 500 {
		t.Errorf("UE got %d, want 500", r.StatusCode)
	}
	select {
	case m := <-h.scscf:
		t.Errorf("the S-CSCF got %s, want nothing", m.Method)
	default:
	}
	if n := len(relay.Contexts()); n != 0 {
		t.Errorf("%d relay contexts, want none", n)
	}
}

func TestTerminatingCallGoesToWhereTheUERegisteredFrom(t *testing.T) {
	h := newSessionHarness(t)
	network := newStack(t, func(*sip.ServerTransaction) {})
	// The S-CSCF sends to the registered contact along the Path.
	m := invite("sip:001010000000001@"+h.ue.LocalAddr().String(), "<sip:term@pcscf."+domain+":5060;lr>")
	m.Add("P-Charging-Vector", "icid-value=network;orig-ioi="+domain)
	got := arrives(t, h.toUE, h.send(network, m), "UE")
	// TS 24.229 5.2.1: the charging information stays in the network.
	if pcv := got.Get("P-Charging-Vector"); pcv != "" {
		t.Errorf("P-Charging-Vector %q reached the UE", pcv)
	}
	c := h.relay.Contexts()
	if len(c) != 1 {
		t.Fatalf("%d relay contexts, want 1", len(c))
	}
	if offer := audio(t, got.Body); offer != c[0].Terminations[agw.Access].Local {
		t.Errorf("offer to the UE names %v, want the access termination", offer)
	}
	if got.Get("Route") != "" {
		t.Errorf("Route %q reached the UE", got.Get("Route"))
	}
	close(h.release)
}

// TestNotifyToTheUEIsRecordRouted is TS 24.229 5.2.6.4.5 item 4: the UE
// takes the route set of its subscription from the NOTIFY (RFC 6665
// 4.4.1), so the P-CSCF must be in its Record-Route. The NOTIFY still
// reaches a UE whose registration the P-CSCF thinks has just expired.
func TestNotifyToTheUEIsRecordRouted(t *testing.T) {
	h := newSessionHarness(t)
	contact := "sip:001010000000001@" + h.ue.LocalAddr().String()
	a, _, _ := h.store.AssociationOfContact(context.Background(), contact)
	a.Expires = time.Now().Add(-time.Second)
	h.p.store.PutAssociation(context.Background(), a)
	network := newStack(t, func(*sip.ServerTransaction) {})
	m := &sip.Message{Method: sip.NOTIFY, RequestURI: contact}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag=scscf")
	m.Add("To", "<"+impu+">;tag=ue")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 NOTIFY")
	m.Add("Contact", "<sip:scscf."+domain+":5060>")
	m.Add("Event", "reg")
	m.Add("Subscription-State", "terminated;reason=noresource")
	got := arrives(t, h.toUE, h.send(network, m), "UE")
	if got.Get("Record-Route") != "<sip:pcscf."+domain+":5060;lr>" {
		t.Errorf("Record-Route = %q, want the P-CSCF", got.Get("Record-Route"))
	}
	close(h.release)
}

// message is shaped like an SMS of TS 24.341 5.3.1.2: a MESSAGE whose
// body has the SMS content type. The bytes only stand in for an
// RP-DATA; the P-CSCF does not read them.
func message(requestURI string, routes ...string) *sip.Message {
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: requestURI}
	m.Add("Max-Forwards", "70")
	for _, r := range routes {
		m.Add("Route", r)
	}
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+requestURI+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("Content-Type", "application/vnd.3gpp.sms")
	m.Body = []byte{0x00, 0x01, 0x00, 0x07, 0x91, 0x21, 0x43, 0x65, 0x87, 0x09, 0xf1}
	return m
}

// TestMessageIsRelayedBothWaysWithoutRecordRoute is TS 24.229 5.2.6.3.7
// and 5.2.6.4.7: a MESSAGE is a request for a standalone transaction. It
// goes the way an INVITE goes, but no Record-Route is added, because it
// creates no dialog (RFC 3428 4).
func TestMessageIsRelayedBothWaysWithoutRecordRoute(t *testing.T) {
	h := newSessionHarnessWith(t, nil)
	close(h.release)

	fromUE := h.send(h.ue, message("sip:sc@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	got := arrives(t, h.scscf, fromUE, "S-CSCF")
	if got.Method != sip.MESSAGE || got.Get("Route") != "<sip:orig@scscf."+domain+":5060;lr>" {
		t.Errorf("S-CSCF got %s with Route %q, want MESSAGE along the Service-Route", got.Method, got.Get("Route"))
	}
	if got.Get("P-Asserted-Identity") != "<"+impu+">" {
		t.Errorf("P-Asserted-Identity = %q", got.Get("P-Asserted-Identity"))
	}
	if rr := got.Get("Record-Route"); rr != "" {
		t.Errorf("Record-Route %q in a MESSAGE", rr)
	}
	if got.Get("Content-Type") != "application/vnd.3gpp.sms" || len(got.Body) != 11 {
		t.Errorf("body %q of type %q, want the RP-DATA unchanged", got.Body, got.Get("Content-Type"))
	}
	if r := final(t, fromUE, 3*time.Second); r.StatusCode != 486 {
		t.Errorf("UE got %d, want the 486 of the S-CSCF", r.StatusCode)
	}

	network := newStack(t, func(*sip.ServerTransaction) {})
	toUE := h.send(network, message("sip:001010000000001@"+h.ue.LocalAddr().String(), "<sip:term@pcscf."+domain+":5060;lr>"))
	got = arrives(t, h.toUE, toUE, "UE")
	if got.Method != sip.MESSAGE || got.Get("Route") != "" {
		t.Errorf("UE got %s with Route %q, want MESSAGE with no Route", got.Method, got.Get("Route"))
	}
	if rr := got.Get("Record-Route"); rr != "" {
		t.Errorf("Record-Route %q in a MESSAGE", rr)
	}
	if r := final(t, toUE, 3*time.Second); r.StatusCode != 486 {
		t.Errorf("network got %d, want the 486 of the UE", r.StatusCode)
	}
}

// TestServiceUnavailableIsNotPassedOn: the next hop of a request
// through the P-CSCF answers 503 with Retry-After, and the P-CSCF has no
// other target for it. It answers 500 without Retry-After instead (RFC
// 3261 16.7 step 6), both ways. The UE would read a 503 as said of its
// P-CSCF (TS 24.229 5.1.2A.1.6).
func TestServiceUnavailableIsNotPassedOn(t *testing.T) {
	unavailable := func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 503, sip.NewTag())
		resp.Add("Retry-After", "30")
		st.Respond(resp)
	}
	scscf := newStack(t, unavailable)
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: scscf.LocalAddr()}}
	})
	store := NewMemoryStore()
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, store, nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	p.SetStack(pcscfStack)
	ue := newStack(t, unavailable)
	store.PutAssociation(context.Background(), Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()},
		SentBy:           ue.LocalAddr().String(),
		Contact:          "sip:001010000000001@" + ue.LocalAddr().String(),
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu},
		ServiceRoute:     []string{"<sip:orig@scscf." + domain + ":5060;lr>"},
		Expires:          time.Now().Add(time.Hour),
	})
	network := newStack(t, func(*sip.ServerTransaction) {})
	for _, tt := range []struct {
		name string
		from *sip.Stack
		m    *sip.Message
	}{
		{"MESSAGE from the UE", ue, message("sip:sc@"+domain, "<sip:pcscf."+domain+":5060;lr>")},
		{"INVITE from the UE", ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")},
		{"MESSAGE to the UE", network, message("sip:001010000000001@"+ue.LocalAddr().String(), "<sip:term@pcscf."+domain+":5060;lr>")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := tt.from.SendRequest(tt.m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
			if err != nil {
				t.Fatal(err)
			}
			r := final(t, ct.Responses(), 3*time.Second)
			if r.StatusCode != 500 || r.Get("Retry-After") != "" {
				t.Errorf("got %d with Retry-After %q, want 500 without one", r.StatusCode, r.Get("Retry-After"))
			}
		})
	}
}

// testRelay is the name of the one media relay of a P-CSCF that only
// lists.
const testRelay = "http://agw.test:8080"

// only lists relay as the one media relay of a P-CSCF, or none when
// relay is nil.
func only(relay Relay) []MediaRelay {
	if relay == nil {
		return nil
	}
	return []MediaRelay{{Name: testRelay, Control: relay}}
}

// fakeRelay is a relay that stalls or fails where a test asks it to.
// It keeps the contexts added and not yet subtracted.
type fakeRelay struct {
	// stall, when not nil, has Add wait until its context ends; closing
	// stall lets go of Add when the context never does.
	stall <-chan struct{}
	// failModify makes every Modify fail.
	failModify bool
	// prefix starts the ID of each context, so that the contexts of two
	// relays have different IDs.
	prefix string

	mu   sync.Mutex
	next int
	// failAdd, when set, is what every Add returns.
	failAdd error
	// failSubtracts is how many Subtracts fail before one succeeds.
	failSubtracts int
	contexts      map[string]bool
	// remotes is where each termination was last told to send.
	remotes map[termination]netip.AddrPort
	// strays are the IDs that a Modify or a Subtract named, of contexts
	// that this relay does not hold.
	strays []string
}

type termination struct {
	context string
	side    agw.TerminationID
}

func newFakeRelay() *fakeRelay {
	return &fakeRelay{contexts: map[string]bool{}, remotes: map[termination]netip.AddrPort{}}
}

func (f *fakeRelay) remote(id string, side agw.TerminationID) netip.AddrPort {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.remotes[termination{id, side}]
}

func (f *fakeRelay) Add(ctx context.Context, _ ...agw.TerminationRequest) (agw.Context, error) {
	if f.stall != nil {
		select {
		case <-ctx.Done():
			return agw.Context{}, ctx.Err()
		case <-f.stall:
			return agw.Context{}, errors.New("the relay stalled")
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAdd != nil {
		return agw.Context{}, f.failAdd
	}
	f.next++
	id := f.prefix + strconv.Itoa(f.next)
	f.contexts[id] = true
	return agw.Context{ID: id, Terminations: map[agw.TerminationID]*agw.Termination{
		agw.Access: {Local: netip.MustParseAddrPort("127.0.0.1:42000")},
		agw.Core:   {Local: netip.MustParseAddrPort("127.0.0.1:42002")},
	}}, nil
}

func (f *fakeRelay) Modify(ctx context.Context, id string, side agw.TerminationID, change agw.TerminationRequest) error {
	if f.failModify {
		return errors.New("the relay refused the Modify")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.contexts[id] {
		f.strays = append(f.strays, id)
	}
	f.remotes[termination{id, side}] = change.Remote
	return nil
}

func (f *fakeRelay) Subtract(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSubtracts > 0 {
		f.failSubtracts--
		return errors.New("the relay is unreachable")
	}
	if !f.contexts[id] {
		f.strays = append(f.strays, id)
	}
	delete(f.contexts, id)
	return nil
}

// added is how many contexts the relay has added, and the ID of the
// last one.
func (f *fakeRelay) added() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.next, f.prefix + strconv.Itoa(f.next)
}

func (f *fakeRelay) noStrays(t *testing.T, name string) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.strays) > 0 {
		t.Errorf("%s got Modify or Subtract for the contexts %v, which it does not hold", name, f.strays)
	}
}

func (f *fakeRelay) held() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.contexts)
}

// waitHeld waits until the relay holds n contexts. The P-CSCF subtracts
// them after it has passed the response on.
func (f *fakeRelay) waitHeld(t *testing.T, n int, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		f.mu.Lock()
		held := len(f.contexts)
		f.mu.Unlock()
		if held == n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the relay holds %d contexts, want %d", held, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRelayThatStallsFailsTheCall(t *testing.T) {
	stall := make(chan struct{})
	t.Cleanup(func() { close(stall) })
	relay := newFakeRelay()
	relay.stall = stall
	h := newSessionHarnessWith(t, only(relay))
	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	if r := final(t, responses, agw.RequestTimeout+time.Second); r.StatusCode != 500 {
		t.Errorf("got %d, want 500", r.StatusCode)
	}
}

func TestRefusedModifySubtractsTheContext(t *testing.T) {
	relay := newFakeRelay()
	relay.failModify = true
	h := newSessionHarnessWith(t, only(relay))
	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	if r := final(t, responses, 3*time.Second); r.StatusCode != 500 {
		t.Errorf("got %d, want 500", r.StatusCode)
	}
	relay.waitHeld(t, 0, time.Second)
}

func TestSubtractThatFailsOnceIsTriedAgain(t *testing.T) {
	relay := newFakeRelay()
	relay.failSubtracts = 1
	h := newSessionHarnessWith(t, only(relay))
	responses := h.send(h.ue, invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>"))
	arrives(t, h.scscf, responses, "S-CSCF")
	close(h.release)
	if r := final(t, responses, 3*time.Second); r.StatusCode != 486 {
		t.Fatalf("got %d, want 486", r.StatusCode)
	}
	relay.waitHeld(t, 0, subtractRetryAfter+time.Second)
}

// TestBYEThatIsRejectedKeepsTheMedia is RFC 3261 12.2.2: a BYE out of
// order gets 500 and the call stays up, so its media must too.
func TestBYEThatIsRejectedKeepsTheMedia(t *testing.T) {
	relay := newFakeRelay()
	h := newSessionHarnessWith(t, only(relay))
	callID := sip.NewCallID()
	c, err := relay.Add(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.store.PutDialog(context.Background(), Dialog{Key: DialogKey{CallID: callID, UETag: "caller"}, Originating: true, Relay: &RelayContext{Relay: testRelay, ID: c.ID}})

	h.byeAnswers <- 500
	if r := final(t, h.send(h.ue, byeRequest(callID, 1)), 3*time.Second); r.StatusCode != 500 {
		t.Fatalf("got %d, want the callee's 500", r.StatusCode)
	}
	// TestBYEAnswered481EndsTheMedia watches as long, and sees the
	// context go.
	if came(watch, func() bool { return relay.held() != 1 }) {
		t.Fatalf("the relay holds %d contexts after the 500, want the call's 1", relay.held())
	}

	h.byeAnswers <- 200
	if r := final(t, h.send(h.ue, byeRequest(callID, 2)), 3*time.Second); r.StatusCode != 200 {
		t.Fatalf("got %d, want 200", r.StatusCode)
	}
	relay.waitHeld(t, 0, time.Second)
}

// TestBYEAnswered481EndsTheMedia is RFC 3261 15.1.1: after a 481 the
// caller considers the dialog ended, so no other BYE will come.
func TestBYEAnswered481EndsTheMedia(t *testing.T) {
	relay := newFakeRelay()
	h := newSessionHarnessWith(t, only(relay))
	callID := sip.NewCallID()
	c, err := relay.Add(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	h.store.PutDialog(context.Background(), Dialog{Key: DialogKey{CallID: callID, UETag: "caller"}, Originating: true, Relay: &RelayContext{Relay: testRelay, ID: c.ID}})
	h.byeAnswers <- 481
	if r := final(t, h.send(h.ue, byeRequest(callID, 1)), 3*time.Second); r.StatusCode != 481 {
		t.Fatalf("got %d, want 481", r.StatusCode)
	}
	if !came(watch, func() bool { return relay.held() == 0 }) {
		t.Errorf("the relay holds %d contexts %v after the 481, want 0", relay.held(), watch)
	}
}

// twoRelays is a session harness whose P-CSCF lists two media relays,
// and whose next hops turn every INVITE down right after its 183.
type twoRelays struct {
	*sessionHarness
	relays [2]*fakeRelay
	names  [2]string
}

func newTwoRelays(t *testing.T) *twoRelays {
	t.Helper()
	h := &twoRelays{relays: [2]*fakeRelay{newFakeRelay(), newFakeRelay()}, names: [2]string{"http://agw-1.test:8080", "http://agw-2.test:8080"}}
	h.relays[0].prefix, h.relays[1].prefix = "a", "b"
	h.sessionHarness = newSessionHarnessWith(t, []MediaRelay{{Name: h.names[0], Control: h.relays[0]}, {Name: h.names[1], Control: h.relays[1]}})
	close(h.release)
	return h
}

// call makes a call with callID, and returns the index of the relay that
// held its context. That relay got the answer of the 183, and neither
// relay holds the context once the 486 has ended the call.
func (h *twoRelays) call(callID string) int {
	h.t.Helper()
	var before [2]int
	for i, r := range h.relays {
		before[i], _ = r.added()
	}
	m := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	m.Set("Call-ID", callID)
	responses := h.send(h.ue, m)
	arrives(h.t, h.scscf, responses, "S-CSCF")
	if r := final(h.t, responses, 3*time.Second); r.StatusCode != 486 {
		h.t.Fatalf("got %d, want 486", r.StatusCode)
	}
	held := -1
	for i, r := range h.relays {
		n, id := r.added()
		if n == before[i] {
			continue
		}
		if held >= 0 || n != before[i]+1 {
			h.t.Fatalf("call %s added contexts in more than one relay", callID)
		}
		held = i
		if got, want := r.remote(id, agw.Core), netip.MustParseAddrPort("192.0.2.99:6000"); got != want {
			h.t.Errorf("call %s: the core termination of %s sends to %v, want the answer's %v", callID, h.names[i], got, want)
		}
	}
	if held < 0 {
		h.t.Fatalf("call %s added no context", callID)
	}
	for _, r := range h.relays {
		r.waitHeld(h.t, 0, time.Second)
	}
	return held
}

func (h *twoRelays) noStrays() {
	h.t.Helper()
	for i, r := range h.relays {
		r.noStrays(h.t, h.names[i])
	}
}

// TestCallsSpreadOverTheMediaRelays: the Call-ID of a call picks the
// relay of its context, so the calls spread over the relays of the
// list, and a call with the same Call-ID gets the same relay again.
func TestCallsSpreadOverTheMediaRelays(t *testing.T) {
	h := newTwoRelays(t)
	var callIDs []string
	for i := range 8 {
		callIDs = append(callIDs, "call-"+strconv.Itoa(i)+"@192.0.2.10")
	}
	picked := map[string]int{}
	var calls [2]int
	for _, callID := range callIDs {
		picked[callID] = h.call(callID)
		calls[picked[callID]]++
	}
	if calls[0] == 0 || calls[1] == 0 {
		t.Errorf("%d calls went to %s and %d to %s, want some on each", calls[0], h.names[0], calls[1], h.names[1])
	}
	for _, callID := range callIDs {
		if got := h.call(callID); got != picked[callID] {
			t.Errorf("call %s went to %s, and to %s the time before", callID, h.names[got], h.names[picked[callID]])
		}
	}
	h.noStrays()
}

// TestCallTakesTheNextMediaRelayWhenAddFails: when the relay that its
// Call-ID picks gives no context, as a relay with no port left answers
// 503, the call takes the next relay of the list.
func TestCallTakesTheNextMediaRelayWhenAddFails(t *testing.T) {
	h := newTwoRelays(t)
	callID := sip.NewCallID()
	first := h.call(callID)
	h.relays[first].mu.Lock()
	h.relays[first].failAdd = agw.ErrNoPort
	h.relays[first].mu.Unlock()
	if got := h.call(callID); got == first {
		t.Errorf("the call went to %s, whose Add fails", h.names[got])
	}
	h.noStrays()
}

// TestBYEReleasesTheContextInTheRelayThatHoldsIt: the dialog names the
// relay of its context, so that the context is subtracted there, and
// not in another relay of the list.
func TestBYEReleasesTheContextInTheRelayThatHoldsIt(t *testing.T) {
	h := newTwoRelays(t)
	for _, r := range h.relays {
		if _, err := r.Add(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	_, id := h.relays[1].added()
	callID := sip.NewCallID()
	h.store.PutDialog(context.Background(), Dialog{Key: DialogKey{CallID: callID, UETag: "caller"}, Originating: true,
		Relay: &RelayContext{Relay: h.names[1], ID: id}})
	h.byeAnswers <- 200
	if r := final(t, h.send(h.ue, byeRequest(callID, 1)), 3*time.Second); r.StatusCode != 200 {
		t.Fatalf("got %d, want 200", r.StatusCode)
	}
	h.relays[1].waitHeld(t, 0, time.Second)
	h.relays[0].waitHeld(t, 1, 0)
	h.noStrays()
}

// TestBYEReleasesAContextThatNamesNoRelay: a P-CSCF that knew one
// relay stored its dialogs without the relay's name. A P-CSCF with one
// relay in its list releases such a context in that relay.
func TestBYEReleasesAContextThatNamesNoRelay(t *testing.T) {
	relay := newFakeRelay()
	h := newSessionHarnessWith(t, []MediaRelay{{Name: "http://agw.test:8080", Control: relay}})
	close(h.release)
	if _, err := relay.Add(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, id := relay.added()
	callID := sip.NewCallID()
	h.store.PutDialog(context.Background(), Dialog{Key: DialogKey{CallID: callID, UETag: "caller"}, Originating: true,
		Relay: &RelayContext{ID: id}})
	h.byeAnswers <- 200
	if r := final(t, h.send(h.ue, byeRequest(callID, 1)), 3*time.Second); r.StatusCode != 200 {
		t.Fatalf("got %d, want 200", r.StatusCode)
	}
	relay.waitHeld(t, 0, time.Second)
}

// TestACKIsForwardedWithMaxForwardsLessOne is RFC 3261 16.6 step 3,
// which holds for an ACK as for any other request: an ACK whose
// Max-Forwards has reached 0 goes no further.
func TestACKIsForwardedWithMaxForwardsLessOne(t *testing.T) {
	acks := make(chan *sip.Message, 2)
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	next, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(),
		OnRequest: func(*sip.ServerTransaction) {}, OnACK: func(m *sip.Message) { acks <- m }}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: next.LocalAddr()}}
	})
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, NewMemoryStore(), nil, quiet())
	p.SetStack(newPCSCFStack(t, p))

	for _, maxForwards := range []string{"0", "70"} {
		ack := byeRequest(sip.NewCallID(), 1)
		ack.Method = sip.ACK
		ack.Set("CSeq", "1 ACK")
		ack.Set("Max-Forwards", maxForwards)
		p.OnACK(ack)
	}
	select {
	case got := <-acks:
		if got.Get("Max-Forwards") != "69" {
			t.Errorf("forwarded an ACK with Max-Forwards %q, want only the one with 69", got.Get("Max-Forwards"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the ACK with Max-Forwards 70 was not forwarded")
	}
	select {
	case got := <-acks:
		t.Errorf("forwarded a second ACK, with Max-Forwards %q", got.Get("Max-Forwards"))
	case <-time.After(200 * time.Millisecond):
	}
}

func byeRequest(callID string, cseq int) *sip.Message {
	m := &sip.Message{Method: sip.BYE, RequestURI: "sip:001010000000002@192.0.2.99"}
	m.Add("Route", "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag=caller")
	m.Add("To", "<sip:001010000000002@"+domain+">;tag=callee")
	m.Add("Call-ID", callID)
	m.Add("CSeq", strconv.Itoa(cseq)+" BYE")
	return m
}

// final skips the provisional responses and returns the final one.
func final(t *testing.T, responses <-chan *sip.Message, within time.Duration) *sip.Message {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case r, ok := <-responses:
			if !ok {
				t.Fatal("no final response")
			}
			if r.StatusCode >= 200 {
				return r
			}
		case <-deadline:
			t.Fatalf("no final response within %v", within)
			return nil
		}
	}
}
