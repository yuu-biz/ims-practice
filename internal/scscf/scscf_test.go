package scscf

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	domain   = "ims.mnc001.mcc001.3gppnetwork.org"
	impi     = "001010000000001@" + domain
	impu     = "sip:001010000000001@" + domain
	password = "test-password-1"
)

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// watch is how long a test watches for something the S-CSCF must not
// do. Each test that watches this long has a twin that waits as long
// for what the S-CSCF must do, and sees it come.
const watch = time.Second

// came tells whether cond held within d.
func came(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}

// fakeHSS hands each request to the HSS stub through the wire format,
// and records the Server-Assignment-Type of every SAR.
type fakeHSS struct {
	hss *hss.HSS
	mu  sync.Mutex
	sar []cx.ServerAssignmentType
	// routing holds, per Cx request that arrived, the command and the
	// Destination-Host it named.
	routing []routedTo
	// stalled, when set, holds the requests of stalledUser until it
	// closes, or until the caller's context ends, as an HSS that does not
	// answer does. The S-CSCF reads it from its own goroutines, after a
	// message over a socket, which the race detector does not see as a
	// hand-over; hence mu.
	stalled     <-chan struct{}
	stalledUser string
}

func (f *fakeHSS) stall(user string, until <-chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stalled, f.stalledUser = until, user
}

func (f *fakeHSS) Request(ctx context.Context, m *diam.Message) (*diam.Message, error) {
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	user := ""
	if r, err := cx.ParseMultimediaAuthRequest(req); err == nil && req.Header.CommandCode == cx.CommandMultimediaAuth {
		user = r.UserName
		f.mu.Lock()
		f.routing = append(f.routing, routedTo{"MAR", r.Header.DestinationHost})
		f.mu.Unlock()
	}
	if r, err := cx.ParseServerAssignmentRequest(req); err == nil && req.Header.CommandCode == cx.CommandServerAssignment {
		user = r.UserName
		f.mu.Lock()
		f.sar = append(f.sar, r.ServerAssignmentType)
		f.routing = append(f.routing, routedTo{"SAR", r.Header.DestinationHost})
		f.mu.Unlock()
	}
	f.mu.Lock()
	stalled := f.stalled
	if user != f.stalledUser {
		stalled = nil
	}
	f.mu.Unlock()
	if stalled != nil {
		select {
		case <-stalled:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.hss.Handle(req), nil
}

func (f *fakeHSS) assignments() []cx.ServerAssignmentType {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cx.ServerAssignmentType(nil), f.sar...)
}

// routedTo is one Cx request as the HSS saw it: which command, and the
// Destination-Host it named, "" for a request that named none.
type routedTo struct {
	command         string
	destinationHost string
}

func (f *fakeHSS) requestRouting() []routedTo {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]routedTo(nil), f.routing...)
}

type harness struct {
	t     *testing.T
	hss   *fakeHSS
	scscf *sip.Stack
	role  *SCSCF
	store *MemoryStore
	// failing is the Store of the S-CSCF: store, with operations that a
	// test makes fail.
	failing *failingStore
	ue      *sip.Stack
	// contact is the Contact of the REGISTERs; empty means ueContact.
	contact string
	// contactParams follow the contact, such as ";+g.3gpp.smsip".
	contactParams string
	// extra are header fields that every REGISTER carries besides.
	extra []sip.HeaderField
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	return newHarnessWith(t, cfg, nil)
}

func newHarnessWith(t *testing.T, cfg Config, resolver Resolver) *harness {
	t.Helper()
	return newHarnessWithHSS(t, cfg, resolver, hss.Config{}, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password})
}

// hssProfile is an HSS configuration whose only service profile, the
// one that every subscription gets, has these criteria.
func hssProfile(criteria ...ifc.InitialFilterCriteria) hss.Config {
	return hss.Config{
		ServiceProfiles:       map[string]hss.ServiceProfile{"default": {Criteria: criteria}},
		DefaultServiceProfile: "default",
	}
}

// newHarnessWithHSS is newHarnessWith with the HSS stub configured as
// hssCfg says, and holding subs. The harness registers the first one.
func newHarnessWithHSS(t *testing.T, cfg Config, resolver Resolver, hssCfg hss.Config, subs ...hss.Subscription) *harness {
	t.Helper()
	identity := diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}
	subscribers, err := hss.NewMemoryStore(subs...)
	if err != nil {
		t.Fatal(err)
	}
	hssCfg.Realm, hssCfg.VisitedNetworks = domain, []string{domain}
	h, err := hss.New(hssCfg, subscribers,
		diameter.NewNode(identity, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet()), quiet())
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeHSS{hss: h}
	node := diameter.NewNode(diameter.Identity{OriginHost: "scscf." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())

	cfg.HomeDomain = domain
	cfg.Host = "scscf." + domain
	cfg.Port = 5060
	cfg.ServerName = "sip:scscf." + domain + ":5060"
	if cfg.MinExpires == 0 {
		cfg.MinExpires = time.Second
	}
	if cfg.MaxExpires == 0 {
		cfg.MaxExpires = time.Hour
	}
	// The sweep comes round often, so that a test does not wait a second
	// for an expiry that is due.
	if cfg.SweepInterval == 0 {
		cfg.SweepInterval = 20 * time.Millisecond
	}
	store := NewMemoryStore()
	failing := &failingStore{MemoryStore: store}
	s, err := New(cfg, f, node, failing, resolver, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	stack := newStack(t, s.OnRequest)
	s.SetStack(stack)
	return &harness{t: t, hss: f, scscf: stack, role: s, store: store, failing: failing, ue: newStack(t, func(*sip.ServerTransaction) {})}
}

func newStack(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(), OnRequest: onRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// register sends one REGISTER and returns the final response.
func (h *harness) register(callID string, cseq int, expires int, authorization string) *sip.Message {
	h.t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", callID)
	m.Add("CSeq", strconv.Itoa(cseq)+" REGISTER")
	contact := h.contact
	if contact == "" {
		contact = ueContact
	}
	m.Add("Contact", "<"+contact+">"+h.contactParams+";expires="+strconv.Itoa(expires))
	m.Add("Path", "<sip:term@pcscf."+domain+":5060;lr>")
	for _, f := range h.extra {
		m.Add(f.Name, f.Value)
	}
	if authorization != "" {
		m.Add("Authorization", authorization)
	}
	ct, err := h.ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: h.scscf.LocalAddr()})
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

// answer builds the Authorization of the second REGISTER from a 401.
func answer(t *testing.T, resp *sip.Message, pass, integrity string) (string, sip.Credentials) {
	t.Helper()
	ch, err := sip.ParseChallenge(resp.Get("WWW-Authenticate"))
	if err != nil {
		t.Fatal(err)
	}
	c := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain,
		Algorithm: "MD5", CNonce: "0a4f113b", QOP: "auth", NC: "00000001"}
	c.Response, err = sip.DigestResponse(sip.HA1(impi, ch.Realm, pass), sip.REGISTER, c)
	if err != nil {
		t.Fatal(err)
	}
	c.AuthParams.Set("integrity-protected", `"`+integrity+`"`)
	return c.String(), c
}

// initial is the first REGISTER of TS 24.229 5.1.1.2.3: empty nonce and
// response, and no integrity-protected because the P-CSCF has no IP
// association yet.
var initial = `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response=""`

// The S-CSCF learns which HSS serves the user from the Origin-Host of
// the MAA and names it in the Destination-Host of the requests that
// follow, while the MAR of an initial registration names none and goes
// to the realm (TS 29.229 5.5, TS 29.228 Table 6.3.1 and Table
// 6.1.2.1). A farm of HSS instances therefore keeps a started
// registration at the instance that answered first, and only the
// requests without a Destination-Host can go to another instance.
func TestTheSARNamesTheHSSThatAnsweredTheMAR(t *testing.T) {
	h := newHarness(t, Config{})
	callID := sip.NewCallID()

	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
	if r := h.register(callID, 3, 0, dereg); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d, want 200", r.StatusCode)
	}
	want := []routedTo{
		{"MAR", ""},
		{"SAR", "hss." + domain},
		{"SAR", "hss." + domain},
	}
	if got := h.hss.requestRouting(); !slices.Equal(got, want) {
		t.Errorf("the Cx requests went to %+v, want %+v", got, want)
	}
}

func TestRegistrationWithSIPDigest(t *testing.T) {
	h := newHarness(t, Config{})
	callID := sip.NewCallID()

	r := h.register(callID, 1, 600000, initial)
	if r.StatusCode != 401 {
		t.Fatalf("first REGISTER got %d, want 401", r.StatusCode)
	}
	ch, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
	if err != nil || ch.Realm != domain || ch.Nonce == "" || len(ch.QOP) != 1 || ch.QOP[0] != "auth" {
		t.Fatalf("WWW-Authenticate = %q (%v)", r.Get("WWW-Authenticate"), err)
	}

	auth, creds := answer(t, r, password, "ip-assoc-pending")
	r = h.register(callID, 2, 600000, auth)
	if r.StatusCode != 200 {
		t.Fatalf("second REGISTER got %d, want 200", r.StatusCode)
	}
	if got := r.Get("Service-Route"); got != "<sip:orig@scscf."+domain+":5060;lr>" {
		t.Errorf("Service-Route = %q", got)
	}
	if got := r.Get("P-Associated-URI"); got != "<"+impu+">" {
		t.Errorf("P-Associated-URI = %q", got)
	}
	if got := r.Get("Path"); got != "<sip:term@pcscf."+domain+":5060;lr>" {
		t.Errorf("Path = %q", got)
	}
	// 600000 s is shortened to MaxExpires.
	if got := r.Get("Contact"); !strings.HasSuffix(got, ";expires=3600") {
		t.Errorf("Contact = %q, want expires=3600", got)
	}
	rspauth, _ := sip.DigestResponse(sip.HA1(impi, domain, password), "", creds)
	if got := r.Get("Authentication-Info"); !strings.Contains(got, `rspauth="`+rspauth+`"`) {
		t.Errorf("Authentication-Info = %q, want rspauth %s", got, rspauth)
	}

	// Deregistration over the existing IP association (TS 24.229
	// 5.4.1.4.1).
	dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
	r = h.register(callID, 3, 0, dereg)
	if r.StatusCode != 200 || !strings.HasSuffix(r.Get("Contact"), ";expires=0") {
		t.Fatalf("deregistration got %d %q", r.StatusCode, r.Get("Contact"))
	}
	want := []cx.ServerAssignmentType{cx.AssignRegistration, cx.UserDeregistration}
	if got := h.hss.assignments(); !slices.Equal(got, want) {
		t.Errorf("SARs = %v, want %v", got, want)
	}
}

func TestWrongPasswordIsForbidden(t *testing.T) {
	h := newHarness(t, Config{})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, "wrong-password", "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 403 {
		t.Fatalf("wrong password got %d, want 403", r.StatusCode)
	}
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AuthenticationFailure}) {
		t.Errorf("SARs = %v, want AUTHENTICATION_FAILURE", got)
	}
}

func TestAnswerOnAnotherCallIDIsForbidden(t *testing.T) {
	h := newHarness(t, Config{})
	r := h.register(sip.NewCallID(), 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(sip.NewCallID(), 2, 600000, auth); r.StatusCode != 403 {
		t.Fatalf("answer with a new Call-ID got %d, want 403", r.StatusCode)
	}
}

func TestRegisterWithoutAuthorizationIsChallenged(t *testing.T) {
	// A softphone sends no Authorization at first; the private identity
	// then comes from To (TS 24.229 5.4.1.1 item 3).
	h := newHarness(t, Config{})
	if r := h.register(sip.NewCallID(), 1, 3600, ""); r.StatusCode != 401 {
		t.Fatalf("REGISTER without Authorization got %d, want 401", r.StatusCode)
	}
}

func TestTooBriefIntervalGets423(t *testing.T) {
	h := newHarness(t, Config{MinExpires: time.Minute})
	r := h.register(sip.NewCallID(), 1, 10, initial)
	if r.StatusCode != 423 || r.Get("Min-Expires") != "60" {
		t.Fatalf("got %d Min-Expires %q, want 423 and 60", r.StatusCode, r.Get("Min-Expires"))
	}
}

func TestRegistrationExpires(t *testing.T) {
	h := newHarness(t, Config{MinExpires: time.Second, MaxExpires: time.Second})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("got %d", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.AssignRegistration, cx.TimeoutDeregistration)
}

// TestAnotherSCSCFTookTheUserOver: the user registered again at another
// S-CSCF, whose MAR took the user over at the HSS (TS 29.228 8.1.1),
// while this S-CSCF still has the registration. When the registration
// ends, by expiry or by the UE, the HSS answers the SAR with
// DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED and keeps the user at the
// other S-CSCF (8.1.2). The S-CSCF removes the registration all the
// same, and the UE gets its 200. A protected re-registration with IMS
// AKA needs no new authentication here (TS 24.229 5.4.1.2.2 step 1), so
// no MAR takes the user back and its SAR is refused too: the UE gets
// 500, and the S-CSCF keeps the registration as it was until it expires
// (8.1.1).
func TestAnotherSCSCFTookTheUserOver(t *testing.T) {
	const other = "sip:scscf2." + domain + ":5060"
	hdr := diameter.Header{SessionID: "scscf2;1;1", OriginHost: "scscf2." + domain, OriginRealm: domain, DestinationRealm: domain}
	toHSS := func(t *testing.T, f *fakeHSS, m *diam.Message) *diam.Message {
		t.Helper()
		b, err := m.Serialize()
		if err != nil {
			t.Fatal(err)
		}
		req, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
		if err != nil {
			t.Fatal(err)
		}
		return f.hss.Handle(req)
	}
	takeOver := func(t *testing.T, f *fakeHSS, scheme string) {
		t.Helper()
		maa, err := cx.ParseMultimediaAuthAnswer(toHSS(t, f, cx.MultimediaAuthRequest{Header: hdr, UserName: impi, PublicIdentity: impu,
			SIPAuthenticationScheme: scheme, SIPNumberAuthItems: 1, ServerName: other}.Message()))
		if err != nil || !maa.Result.Success() {
			t.Fatalf("MAA to the other S-CSCF = %+v (%v)", maa, err)
		}
		saa, err := cx.ParseServerAssignmentAnswer(toHSS(t, f, cx.ServerAssignmentRequest{Header: hdr, UserName: impi,
			PublicIdentities: []string{impu}, ServerName: other, ServerAssignmentType: cx.AssignRegistration}.Message()))
		if err != nil || !saa.Result.Success() {
			t.Fatalf("SAA to the other S-CSCF = %+v (%v)", saa, err)
		}
	}
	// calledAtTheOther fails the test unless the HSS still sends a call
	// to the user to the other S-CSCF.
	calledAtTheOther := func(t *testing.T, f *fakeHSS) {
		t.Helper()
		lia, err := cx.ParseLocationInfoAnswer(toHSS(t, f, cx.LocationInfoRequest{Header: hdr, PublicIdentity: impu}.Message()))
		if err != nil || !lia.Result.Success() || lia.ServerName != other {
			t.Errorf("LIA = %+v (%v), want DIAMETER_SUCCESS with %s", lia, err, other)
		}
	}
	registered := func(t *testing.T, h *harness) bool {
		t.Helper()
		_, ok, err := h.store.Registration(t.Context(), impi)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	register := func(t *testing.T, h *harness, callID string) {
		t.Helper()
		r := h.register(callID, 1, 600000, initial)
		auth, _ := answer(t, r, password, "ip-assoc-pending")
		if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
			t.Fatalf("registration got %d, want 200", r.StatusCode)
		}
	}

	t.Run("expiry", func(t *testing.T) {
		h := newHarness(t, Config{MinExpires: time.Second, MaxExpires: time.Second})
		register(t, h, sip.NewCallID())
		takeOver(t, h.hss, cx.SchemeUnknown)
		waitForAssignments(t, h.hss, cx.AssignRegistration, cx.TimeoutDeregistration)
		if registered(t, h) {
			t.Error("the registration stays after its expiry")
		}
		calledAtTheOther(t, h.hss)
	})
	t.Run("deregistration by the UE", func(t *testing.T) {
		h := newHarness(t, Config{})
		callID := sip.NewCallID()
		register(t, h, callID)
		takeOver(t, h.hss, cx.SchemeUnknown)
		dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
		if r := h.register(callID, 3, 0, dereg); r.StatusCode != 200 || !strings.HasSuffix(r.Get("Contact"), ";expires=0") {
			t.Fatalf("deregistration got %d %q, want 200 with expires=0", r.StatusCode, r.Get("Contact"))
		}
		waitForAssignments(t, h.hss, cx.AssignRegistration, cx.UserDeregistration)
		if registered(t, h) {
			t.Error("the registration stays after the deregistration")
		}
		calledAtTheOther(t, h.hss)
	})
	t.Run("protected re-registration with IMS AKA", func(t *testing.T) {
		h := newAKAHarness(t)
		callID := sip.NewCallID()
		ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
		res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
		if err != nil {
			t.Fatal(err)
		}
		creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
		if r := h.register(callID, 2, 600000, creds.String()); r.StatusCode != 200 {
			t.Fatalf("registration got %d, want 200", r.StatusCode)
		}
		before, _, err := h.store.Registration(t.Context(), impi)
		if err != nil {
			t.Fatal(err)
		}
		takeOver(t, h.hss, cx.SchemeDigestAKAv1MD5)
		if r := h.register(callID, 3, 600000, creds.String()); r.StatusCode != 500 {
			t.Fatalf("protected re-registration got %d, want 500", r.StatusCode)
		}
		waitForAssignments(t, h.hss, cx.AssignRegistration, cx.ReRegistration)
		if after, ok, err := h.store.Registration(t.Context(), impi); err != nil || !ok || !reflect.DeepEqual(after, before) {
			t.Errorf("the registration is %+v (%v, %v) after the refused re-registration, want it as it was, %+v", after, ok, err, before)
		}
		calledAtTheOther(t, h.hss)
	})
}

func TestRegAwaitAuthExpiry(t *testing.T) {
	h := newHarness(t, Config{RegAwaitAuth: 100 * time.Millisecond})
	h.register(sip.NewCallID(), 1, 600000, initial)
	waitForAssignments(t, h.hss, cx.AuthenticationTimeout)
}

// TestASlowExpiryHoldsUpNeitherAnotherNorClose: the HSS does not answer
// the SAR of one user's expiry, so that handler waits. The expiry of
// another user is handled all the same. Close lets the handler that
// waits finish, since the registration it ended has left the store and
// its SAR would go unsent otherwise, but waits for it no longer than its
// claim: after that the expiry is any instance's.
func TestASlowExpiryHoldsUpNeitherAnotherNorClose(t *testing.T) {
	const otherIMPI = "001010000000002@" + domain
	const lease = 2 * time.Second
	for _, tt := range []struct {
		name     string
		answered bool
	}{
		{"the HSS answers once Close has begun", true},
		{"the HSS never answers", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarnessWithHSS(t, Config{ExpiryLease: lease}, nil, hss.Config{},
				hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password})
			stalled := make(chan struct{})
			var once sync.Once
			answer := func() { once.Do(func() { close(stalled) }) }
			defer answer()
			h.hss.stall(impi, stalled)
			ctx := context.Background()
			// A registration of impi whose time is up: its handler sends the
			// SAR that the HSS leaves unanswered, and waits there.
			if err := h.store.PutRegistration(ctx, Registration{PrivateIdentity: impi, PublicIdentities: []string{impu},
				Contact: ueContact, Expires: time.Now().Add(-time.Second)}); err != nil {
				t.Fatal(err)
			}
			waitForAssignments(t, h.hss, cx.TimeoutDeregistration)

			// The rounds that follow go on claiming: the reg-await-auth of
			// another user, which runs out while that handler waits, is
			// handled.
			if err := h.store.PutChallenge(ctx, Challenge{PrivateIdentity: otherIMPI, PublicIdentity: impu, Nonce: "n1",
				Expires: time.Now().Add(-time.Second), KeepUntil: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			if !came(3*time.Second, func() bool { return slices.Contains(h.hss.assignments(), cx.AuthenticationTimeout) }) {
				t.Fatalf("SARs = %v, want AUTHENTICATION_TIMEOUT while the other expiry waits for the HSS", h.hss.assignments())
			}
			closed := make(chan struct{})
			go func() {
				h.role.Close()
				close(closed)
			}()
			select {
			case <-closed:
				t.Fatal("Close cut short the handler that waits for the HSS")
			case <-time.After(lease / 4):
			}
			if tt.answered {
				answer()
			}
			select {
			case <-closed:
			case <-time.After(lease + 3*time.Second):
				t.Errorf("Close waited past the claim of the handler that waits for the HSS")
			}
		})
	}
}

func TestFailedReauthenticationKeepsTheRegistrationExpiry(t *testing.T) {
	h := newHarness(t, Config{MinExpires: time.Second, MaxExpires: time.Second})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d", r.StatusCode)
	}
	// A refresh that fails authentication: the user stays registered
	// until the registration expires (TS 24.229 5.4.1.2.3B).
	r = h.register(callID, 3, 600000, initial)
	auth, _ = answer(t, r, "wrong-password", "ip-assoc-yes")
	if r := h.register(callID, 4, 600000, auth); r.StatusCode != 403 {
		t.Fatalf("wrong refresh got %d, want 403", r.StatusCode)
	}
	waitForAssignments(t, h.hss, cx.AssignRegistration, cx.AuthenticationFailure, cx.TimeoutDeregistration)
}

func TestOldNonceWithTheRightPasswordIsStale(t *testing.T) {
	h := newHarness(t, Config{})
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	ch, _ := sip.ParseChallenge(r.Get("WWW-Authenticate"))
	ch.Nonce = "an-old-nonce"
	old := sip.NewResponse(r, 401, "")
	old.Set("WWW-Authenticate", ch.String())
	auth, _ := answer(t, old, password, "ip-assoc-pending")
	r = h.register(callID, 2, 600000, auth)
	again, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
	if r.StatusCode != 401 || err != nil || !again.Stale {
		t.Fatalf("got %d %q, want 401 with stale=true", r.StatusCode, r.Get("WWW-Authenticate"))
	}
}

func TestDeregisteringAnUnregisteredUserGets481(t *testing.T) {
	h := newHarness(t, Config{})
	if r := h.register(sip.NewCallID(), 1, 0, initial); r.StatusCode != 481 {
		t.Fatalf("got %d, want 481", r.StatusCode)
	}
}

// TestUsersDoNotWaitForEachOther: while the Cx round trip of
// one user's registration hangs, another user's REGISTER is answered.
func TestUsersDoNotWaitForEachOther(t *testing.T) {
	h := newHarness(t, Config{})
	stalled := make(chan struct{})
	h.hss.stall(impi, stalled)
	first := make(chan int, 1)
	go func() { first <- h.register(sip.NewCallID(), 1, 600000, initial).StatusCode }()
	if !came(3*time.Second, func() bool { return len(h.hss.requestRouting()) == 1 }) {
		close(stalled)
		t.Fatal("the first user's MAR did not reach the HSS within 3 s")
	}

	// The HSS knows no such user, and answers its MAR at once.
	other := strings.ReplaceAll(initial, impi, "001010000000009@"+domain)
	second := make(chan int, 1)
	go func() { second <- h.register(sip.NewCallID(), 1, 600000, other).StatusCode }()
	select {
	case code := <-second:
		if code != 403 {
			t.Errorf("the other user got %d, want 403", code)
		}
	case <-time.After(2 * time.Second):
		t.Error("the other user's REGISTER waited for the first user's MAR")
	}
	close(stalled)
	if code := <-first; code != 401 {
		t.Errorf("the first user got %d, want 401", code)
	}
}
