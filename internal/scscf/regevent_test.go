package scscf

import (
	"bytes"
	"context"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	pcscfURI  = "sip:pcscf." + domain + ":5060"
	pcscfPath = "<sip:term@pcscf." + domain + ":5060;lr>"
	ueContact = "sip:001010000000001@192.0.2.10:5060"
)

// subscriberHarness is a harness whose resolver sends the NOTIFYs for
// the P-CSCF to a UA that stands in for it. The UA's requests go
// straight to the S-CSCF.
type subscriberHarness struct {
	*harness
	ua *sip.UA
}

func newSubscriberHarness(t *testing.T, cfg Config) *subscriberHarness {
	t.Helper()
	return newSubscriberHarnessWithHSS(t, cfg, hss.Config{})
}

// newSubscriberHarnessWithHSS is newSubscriberHarness with the HSS stub
// configured as hssCfg says.
func newSubscriberHarnessWithHSS(t *testing.T, cfg Config, hssCfg hss.Config) *subscriberHarness {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithHSS(t, cfg, fakeResolver{hosts: map[string]netip.AddrPort{"pcscf." + domain: tr.LocalAddr()}}, hssCfg,
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: password})
	scscf := h.scscf.LocalAddr()
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy:  tr.LocalAddr().String(),
		Contact: pcscfURI,
		OnCall:  func(c *sip.IncomingCall) { c.Reject(480) },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: scscf}}, nil
		},
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	return &subscriberHarness{harness: h, ua: ua}
}

// registerUser registers the test user with the Path of the P-CSCF.
func (h *harness) registerUser(callID string) {
	h.t.Helper()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(h.t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		h.t.Fatalf("registration got %d", r.StatusCode)
	}
}

// subscribe subscribes as the P-CSCF does (TS 24.229 5.2.3), with
// assertedIdentity in P-Asserted-Identity.
func (h *subscriberHarness) subscribe(assertedIdentity, accept string) (*sip.Subscription, <-chan sip.Notification) {
	h.t.Helper()
	return h.subscribeFor(assertedIdentity, accept, 2*time.Hour, nil)
}

func (h *subscriberHarness) subscribeFor(assertedIdentity, accept string, expires time.Duration, refreshAfter func(time.Duration) time.Duration) (*sip.Subscription, <-chan sip.Notification) {
	h.t.Helper()
	got := make(chan sip.Notification, 8)
	sub, err := h.ua.Subscribe(sip.SubscribeRequest{
		RequestURI:   impu,
		From:         pcscfURI,
		Event:        "reg",
		Expires:      expires,
		RefreshAfter: refreshAfter,
		Accept:       accept,
		Headers: []sip.HeaderField{
			{Name: "P-Asserted-Identity", Value: assertedIdentity},
			{Name: "P-Charging-Vector", Value: "icid-value=icid-1;orig-ioi=" + domain},
		},
		OnNotify: func(n sip.Notification, _ *sip.Message) { got <- n },
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return sub, got
}

// nextNotify waits for a NOTIFY and parses its reginfo.
func nextNotify(t *testing.T, got <-chan sip.Notification) (sip.Notification, reginfo.Document) {
	t.Helper()
	select {
	case n := <-got:
		if n.Body.Type != reginfo.ContentType {
			t.Fatalf("NOTIFY body type %q", n.Body.Type)
		}
		doc, err := reginfo.Parse(n.Body.Content)
		if err != nil {
			t.Fatalf("NOTIFY body: %v\n%s", err, n.Body.Content)
		}
		return n, doc
	case <-time.After(5 * time.Second):
		t.Fatal("no NOTIFY")
		return sip.Notification{}, reginfo.Document{}
	}
}

// contactState checks the one registration and its contact.
func contactState(t *testing.T, doc reginfo.Document, version uint32, state reginfo.State, event reginfo.Event) {
	t.Helper()
	if doc.Version != version || doc.State != reginfo.Full || len(doc.Registrations) != 1 {
		t.Fatalf("reginfo version %d, state %s, %d registrations", doc.Version, doc.State, len(doc.Registrations))
	}
	r := doc.Registrations[0]
	if r.AOR != impu || r.State != state || len(r.Contacts) != 1 {
		t.Fatalf("registration %+v", r)
	}
	c := r.Contacts[0]
	if c.URI != ueContact || c.State != state || c.Event != event || c.CallID == "" || c.CSeq == 0 {
		t.Errorf("contact %+v, want %s and %s", c, state, event)
	}
}

func waitEnd(t *testing.T, sub *sip.Subscription) {
	t.Helper()
	select {
	case <-sub.Done():
		if err := sub.Err(); err != nil {
			t.Errorf("subscription ended with %v, want a NOTIFY that says terminated", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription never ended")
	}
}

func TestRegEventFollowsTheRegistration(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	callID := sip.NewCallID()
	h.registerUser(callID)
	sub, got := h.subscribe(pcscfPath, "")

	n, doc := nextNotify(t, got)
	if n.State.State != sip.SubscriptionActive || n.State.Expires != 2*time.Hour {
		t.Errorf("Subscription-State %+v, want active for the 2 h asked for", n.State)
	}
	contactState(t, doc, 0, reginfo.Active, reginfo.Registered)
	if c := doc.Registrations[0].Contacts[0]; c.Expires == 0 || c.Expires > 3600 {
		t.Errorf("contact expires %d", c.Expires)
	}
	if pcv := n.Request.Get("P-Charging-Vector"); pcv != "icid-value=icid-1;orig-ioi="+domain {
		t.Errorf("NOTIFY P-Charging-Vector %q", pcv)
	}

	r := h.register(callID, 3, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-yes")
	if r := h.register(callID, 4, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("re-registration got %d", r.StatusCode)
	}
	_, doc = nextNotify(t, got)
	contactState(t, doc, 1, reginfo.Active, reginfo.Refreshed)

	dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
	if r := h.register(callID, 5, 0, dereg); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d", r.StatusCode)
	}
	n, doc = nextNotify(t, got)
	contactState(t, doc, 2, reginfo.Terminated, reginfo.Unregistered)
	if n.State.State != sip.SubscriptionTerminated || n.State.Reason != sip.ReasonNoResource {
		t.Errorf("Subscription-State %+v, want terminated with noresource", n.State)
	}
	waitEnd(t, sub)
}

// TS 24.229 5.4.1.2.2F item f and 5.4.2.1.2: the parameters of the
// Contact, such as the feature tag of SMS over IP (TS 24.341 5.3.2.2),
// stay with the binding. The 200 OK lists them, and the reg event
// reports each as an <unknown-param>, q apart (RFC 3680 5.1).
func TestContactParametersStayWithTheBinding(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.contactParams = ";+g.3gpp.smsip;q=0.5"
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	r = h.register(callID, 2, 600000, auth)
	if want := "<" + ueContact + ">;+g.3gpp.smsip;q=0.5;expires=3600"; r.StatusCode != 200 || r.Get("Contact") != want {
		t.Fatalf("registration got %d with Contact %q, want %q", r.StatusCode, r.Get("Contact"), want)
	}
	_, got := h.subscribe(pcscfPath, "")
	_, doc := nextNotify(t, got)
	contactState(t, doc, 0, reginfo.Active, reginfo.Registered)
	c := doc.Registrations[0].Contacts[0]
	if want := []reginfo.UnknownParam{{Name: "+g.3gpp.smsip"}}; c.Q != "0.5" || !slices.Equal(c.UnknownParams, want) {
		t.Errorf("contact q %q, unknown-params %+v; want 0.5 and %+v", c.Q, c.UnknownParams, want)
	}
}

func TestRegEventSubscriptionsThatAreRefused(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	refused := func(name, asserted, accept string, want int) {
		t.Helper()
		sub, _ := h.subscribe(asserted, accept)
		select {
		case <-sub.Done():
			if err := sub.Err(); err == nil || !strings.Contains(err.Error(), strconv.Itoa(want)) {
				t.Errorf("%s: subscription ended with %v, want a %d", name, err, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the SUBSCRIBE was not refused", name)
		}
	}
	// Step 0: no binding yet.
	refused("not registered", pcscfPath, "", 480)
	h.registerUser(sip.NewCallID())
	// Step 1: neither the user nor the P-CSCF of its Path.
	refused("someone else", "<sip:someone-else@"+domain+">", "", 403)
	refused("another P-CSCF", "<sip:term@pcscf2."+domain+":5060;lr>", "", 403)
	// RFC 3680 4.5.
	refused("no reginfo", pcscfPath, "text/plain", 406)
	// The user itself may subscribe.
	_, got := h.subscribe("<"+impu+">", "application/reginfo+xml")
	if n, _ := nextNotify(t, got); n.State.State != sip.SubscriptionActive {
		t.Errorf("the user's own subscription: %+v", n.State)
	}
}

func TestRegEventReportsAnExpiredContact(t *testing.T) {
	h := newSubscriberHarness(t, Config{MinExpires: time.Second, MaxExpires: 2 * time.Second})
	h.registerUser(sip.NewCallID())
	sub, got := h.subscribe(pcscfPath, "")
	nextNotify(t, got)
	n, doc := nextNotify(t, got)
	contactState(t, doc, 1, reginfo.Terminated, reginfo.Expired)
	if n.State.State != sip.SubscriptionTerminated {
		t.Errorf("Subscription-State %+v", n.State)
	}
	waitEnd(t, sub)
}

func TestNetworkInitiatedDeregistration(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	sub, got := h.subscribe(pcscfPath, "")
	nextNotify(t, got)
	if err := h.role.Deregister(impi, reginfo.Expired); err == nil {
		t.Error("Deregister accepted the event expired")
	}
	if err := h.role.Deregister(impi, reginfo.Deactivated); err != nil {
		t.Fatal(err)
	}
	_, doc := nextNotify(t, got)
	contactState(t, doc, 1, reginfo.Terminated, reginfo.Deactivated)
	waitEnd(t, sub)
	want := []cx.ServerAssignmentType{cx.AssignRegistration, cx.AdministrativeDeregistration}
	if got := h.hss.assignments(); !slices.Equal(got, want) {
		t.Errorf("SARs = %v, want %v", got, want)
	}
}

// rtr sends the S-CSCF an RTR through the wire format, as the HSS
// would, and returns the RTA.
func (h *harness) rtr(user string, reason cx.ReasonCode, publicIDs ...string) *diam.Message {
	h.t.Helper()
	hssNode := diameter.NewNode(diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	m := cx.RegistrationTerminationRequest{
		Header:               cx.NewHeader(hssNode, "scscf."+domain, domain),
		UserName:             user,
		PublicIdentities:     publicIDs,
		DeregistrationReason: cx.DeregistrationReason{ReasonCode: reason},
	}.Message()
	return wire(h.t, h.role.Handle(wire(h.t, m)))
}

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

func rtaResult(t *testing.T, m *diam.Message) cx.Result {
	t.Helper()
	a, err := cx.ParseRegistrationTerminationAnswer(m)
	if err != nil {
		t.Fatal(err)
	}
	return a.Result
}

// TestRegistrationTerminationByTheHSS is TS 29.228 6.1.3.1 and TS
// 24.229 5.4.1.5: on an RTR with PERMANENT_TERMINATION the S-CSCF
// rejects the registration in the NOTIFY, answers DIAMETER_SUCCESS, and
// sends no SAR, since the HSS already deregistered the user (TS 29.228
// A.4.4.2).
func TestRegistrationTerminationByTheHSS(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	sub, got := h.subscribe(pcscfPath, "")
	nextNotify(t, got)

	if r := rtaResult(t, h.rtr(impi, cx.PermanentTermination)); r != cx.Base(cx.DiameterSuccess) {
		t.Errorf("RTA = %s, want DIAMETER_SUCCESS", r)
	}
	n, doc := nextNotify(t, got)
	contactState(t, doc, 1, reginfo.Terminated, reginfo.Rejected)
	if n.State.State != sip.SubscriptionTerminated {
		t.Errorf("Subscription-State %+v", n.State)
	}
	waitEnd(t, sub)
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AssignRegistration}) {
		t.Errorf("SARs = %v, want only the one of the registration", got)
	}

	// The user is gone: a second RTR is for a user the S-CSCF does not
	// know (TS 29.229 6.2.2.1), and so is a REGISTER to deregister.
	if r := rtaResult(t, h.rtr(impi, cx.PermanentTermination)); r != cx.Experimental(cx.DiameterErrorUserUnknown) {
		t.Errorf("second RTA = %s, want DIAMETER_ERROR_USER_UNKNOWN", r)
	}
	if r := h.register(sip.NewCallID(), 1, 0, initial); r.StatusCode != 481 {
		t.Errorf("deregistration after the RTR got %d, want 481", r.StatusCode)
	}
}

func TestRegistrationTerminationReasons(t *testing.T) {
	tests := []struct {
		name      string
		reason    cx.ReasonCode
		publicIDs []string
		result    cx.Result
		event     reginfo.Event // empty: the user stays registered
	}{
		{"server change asks the UE to register again", cx.ServerChange, nil, cx.Base(cx.DiameterSuccess), reginfo.Deactivated},
		{"a public identity of the user", cx.PermanentTermination, []string{impu}, cx.Base(cx.DiameterSuccess), reginfo.Rejected},
		{"a public identity of someone else", cx.PermanentTermination, []string{"sip:someone@" + domain}, cx.Experimental(cx.DiameterErrorUserUnknown), ""},
		{"a reason this S-CSCF does not act on", cx.NewServerAssigned, nil, cx.Base(cx.DiameterUnableToComply), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSubscriberHarness(t, Config{})
			h.registerUser(sip.NewCallID())
			_, got := h.subscribe(pcscfPath, "")
			nextNotify(t, got)
			if r := rtaResult(t, h.rtr(impi, tt.reason, tt.publicIDs...)); r != tt.result {
				t.Errorf("RTA = %s, want %s", r, tt.result)
			}
			if tt.event != "" {
				_, doc := nextNotify(t, got)
				contactState(t, doc, 1, reginfo.Terminated, tt.event)
			}
			// A REGISTER to deregister is challenged while the user is
			// registered, and gets 481 once it is not (5.4.1.4.1).
			want := 401
			if tt.event != "" {
				want = 481
			}
			if r := h.register(sip.NewCallID(), 1, 0, initial); r.StatusCode != want {
				t.Errorf("deregistration after the RTR got %d, want %d", r.StatusCode, want)
			}
		})
	}
}

func TestRegEventRefreshAndUnsubscribe(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	sub, got := h.subscribeFor(pcscfPath, "", 2*time.Hour, func(time.Duration) time.Duration { return 100 * time.Millisecond })
	nextNotify(t, got)
	// RFC 6665 4.2.1.2: a refresh gets a NOTIFY with the state at once.
	n, doc := nextNotify(t, got)
	if n.State.State != sip.SubscriptionActive {
		t.Errorf("NOTIFY after the refresh: %+v", n.State)
	}
	contactState(t, doc, 1, reginfo.Active, reginfo.Registered)
	if err := sub.Unsubscribe(); err != nil {
		t.Fatal(err)
	}
	// The refresh timer keeps running until the unsubscribe; skip any
	// NOTIFY it caused.
	for {
		n, _ = nextNotify(t, got)
		if n.State.State == sip.SubscriptionTerminated {
			break
		}
	}
	if n.State.Reason != sip.ReasonTimeout {
		t.Errorf("NOTIFY after the unsubscribe: %+v, want reason timeout (RFC 6665 4.2.1.4)", n.State)
	}
	waitEnd(t, sub)
	if r := h.subscribeWithin(n.Request, 3600); r.StatusCode != 481 {
		t.Errorf("SUBSCRIBE within the subscription after the unsubscribe got %d, want 481", r.StatusCode)
	}
}

// The NOTIFY that the S-CSCF sends at the expiry comes as the
// subscriber ends the subscription itself, and may get 481 from it, so
// the test reads the Store to see that the S-CSCF let the subscription
// go.
func TestRegEventSubscriptionThatIsNotRefreshedExpires(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	h.registerUser(sip.NewCallID())
	_, got := h.subscribeFor(pcscfPath, "", time.Second, nil)
	nextNotify(t, got)
	if subs, _ := h.role.store.SubscriptionsOf(context.Background(), impi); len(subs) != 1 {
		t.Fatal("the S-CSCF holds no subscription")
	}
	deadline := time.Now().Add(3 * time.Second)
	for subs, _ := h.role.store.SubscriptionsOf(context.Background(), impi); len(subs) != 0; subs, _ = h.role.store.SubscriptionsOf(context.Background(), impi) {
		if time.Now().After(deadline) {
			t.Fatal("the S-CSCF kept a subscription that expired")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The registration stays: a new subscription to it is active.
	_, got = h.subscribe(pcscfPath, "")
	if n, _ := nextNotify(t, got); n.State.State != sip.SubscriptionActive {
		t.Errorf("a new subscription after the expiry: %+v, want active", n.State)
	}
}

// A subscription that is past its expiry, which the sweep has not
// reached yet, gets no NOTIFY that says active: the next change of the
// registration ends it with reason timeout (RFC 6665 4.2.1.4), and the
// S-CSCF forgets it. No message moves an expiry into the past, so the
// test writes it in the Store, and leaves the sweep an interval long
// enough that it does not come in between.
func TestRegEventSubscriptionPastItsExpiryEndsAtTheNextNotify(t *testing.T) {
	h := newSubscriberHarness(t, Config{SweepInterval: time.Minute})
	h.registerUser(sip.NewCallID())
	sub, got := h.subscribe(pcscfPath, "")
	nextNotify(t, got)
	subs, _ := h.role.store.SubscriptionsOf(context.Background(), impi)
	if len(subs) != 1 {
		t.Fatalf("the S-CSCF holds %d subscriptions, want 1", len(subs))
	}
	stored := subs[0]
	stored.Expires = time.Now().Add(-time.Second)
	h.role.store.PutSubscription(context.Background(), stored)

	h.registerUser(sip.NewCallID())
	n, _ := nextNotify(t, got)
	if n.State.State != sip.SubscriptionTerminated || n.State.Reason != sip.ReasonTimeout {
		t.Errorf("NOTIFY after the expiry says %+v, want terminated with reason timeout", n.State)
	}
	waitEnd(t, sub)
	if r := h.subscribeWithin(n.Request, 3600); r.StatusCode != 481 {
		t.Errorf("SUBSCRIBE within the subscription after the expiry got %d, want 481", r.StatusCode)
	}
}

// subscribeWithin sends a SUBSCRIBE within the subscription of notify,
// a NOTIFY that the S-CSCF sent, asking for expires seconds, and
// returns the final response.
func (h *harness) subscribeWithin(notify *sip.Message, expires int) *sip.Message {
	h.t.Helper()
	contact, err := sip.ParseNameAddr(notify.Get("Contact"))
	if err != nil {
		h.t.Fatal(err)
	}
	cseq, _ := sip.ParseCSeq(notify.Get("CSeq"))
	m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: contact.URI}
	m.Add("Max-Forwards", "70")
	m.Add("From", notify.Get("To"))
	m.Add("To", notify.Get("From"))
	m.Add("Call-ID", notify.Get("Call-ID"))
	// Past any CSeq of the subscriber's own.
	m.Add("CSeq", strconv.Itoa(int(cseq.Number)+100)+" SUBSCRIBE")
	m.Add("Contact", "<"+pcscfURI+">")
	m.Add("Event", regEventPackage)
	m.Add("Expires", strconv.Itoa(expires))
	return h.send(m)
}

func TestRegEventReportsAReplacedContact(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	callID := sip.NewCallID()
	h.registerUser(callID)
	_, got := h.subscribe(pcscfPath, "")
	_, first := nextNotify(t, got)
	oldID := first.Registrations[0].Contacts[0].ID

	// The UE comes back on another port without deregistering.
	h.contact = "sip:001010000000001@192.0.2.10:5070"
	r := h.register(callID, 3, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 4, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration from the new contact got %d", r.StatusCode)
	}
	_, doc := nextNotify(t, got)
	contacts := doc.Registrations[0].Contacts
	if len(contacts) != 2 {
		t.Fatalf("contacts %+v, want the new one and the replaced one", contacts)
	}
	if c := contacts[0]; c.URI != h.contact || c.State != reginfo.Active || c.Event != reginfo.Registered || c.ID == oldID {
		t.Errorf("new contact %+v", c)
	}
	if c := contacts[1]; c.URI != ueContact || c.State != reginfo.Terminated || c.Event != reginfo.Unregistered || c.ID != oldID {
		t.Errorf("replaced contact %+v", c)
	}
}

func TestSubscribeInAnEndedSubscriptionGets481(t *testing.T) {
	h := newSubscriberHarness(t, Config{})
	m := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: "sip:scscf." + domain + ":5060"}
	m.Add("From", "<"+pcscfURI+">;tag=gone")
	m.Add("To", "<"+impu+">;tag=gone-too")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "2 SUBSCRIBE")
	m.Add("Contact", "<"+pcscfURI+">")
	m.Add("Event", "reg")
	m.Add("Expires", "600")
	ct, err := h.ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: h.scscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil || final.StatusCode != 481 {
		t.Fatalf("got %v, want 481 (RFC 3261 12.2.2)", final)
	}
}
