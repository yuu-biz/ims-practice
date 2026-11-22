package pcscf

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// fakeHome plays the I-CSCF and the S-CSCF behind the P-CSCF: it
// answers every REGISTER and MESSAGE with 200, accepts an initial
// SUBSCRIBE and answers a refresh within its dialog. Every SUBSCRIBE it
// receives, and every subscription it accepts, it hands to the test.
type fakeHome struct {
	stack      *sip.Stack
	subscribes chan *sip.Message
	accepted   chan *sip.NotifierSubscription

	mu sync.Mutex
	// subs are the subscriptions the home has accepted, by the tag it
	// gave each one, so that a refresh finds the one it belongs to.
	subs map[string]*sip.NotifierSubscription
	// grant is the duration the home gives a subscription, in the Expires
	// of its 200. Zero gives the duration the SUBSCRIBE asked for, as the
	// S-CSCF of this project does.
	grant time.Duration
	// refuseRefresh, when set, is the status the home answers a SUBSCRIBE
	// within a dialog with, in place of the 200.
	refuseRefresh int
}

func newFakeHome(t *testing.T) *fakeHome {
	h := &fakeHome{subscribes: make(chan *sip.Message, 4), accepted: make(chan *sip.NotifierSubscription, 4),
		subs: map[string]*sip.NotifierSubscription{}}
	h.stack = newStack(t, func(st *sip.ServerTransaction) {
		req := st.Request
		switch req.Method {
		case sip.REGISTER:
			resp := sip.NewResponse(req, 200, sip.NewTag())
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", "<sip:ue@127.0.0.1>;expires=3600")
			st.Respond(resp)
		case sip.MESSAGE:
			st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
		case sip.SUBSCRIBE:
			h.subscribes <- req
			h.subscribe(t, st)
		}
	})
	return h
}

// subscribe accepts an initial SUBSCRIBE, or answers one that refreshes
// a subscription within its dialog (RFC 6665 4.2.1.4).
func (h *fakeHome) subscribe(t *testing.T, st *sip.ServerTransaction) {
	req := st.Request
	asked, err := sip.SubscribeExpires(req, time.Hour)
	if err != nil {
		t.Errorf("SubscribeExpires: %v", err)
		return
	}
	h.mu.Lock()
	expires, refuse := asked, h.refuseRefresh
	if h.grant != 0 {
		expires = h.grant
	}
	h.mu.Unlock()
	if tag := sip.ReceivedDialogID(req).LocalTag; tag != "" {
		if refuse != 0 {
			st.Respond(sip.NewResponse(req, refuse, sip.NewTag()))
			return
		}
		// The test sends the NOTIFYs of the subscription on a goroutine of
		// its own, so the lock is held while the dialog changes here.
		h.mu.Lock()
		sub := h.subs[tag]
		var ok *sip.Message
		if sub != nil {
			ok, err = sub.ReceiveSubscribe(req, expires, time.Now())
		}
		h.mu.Unlock()
		switch {
		case sub == nil:
			st.Respond(sip.NewResponse(req, 481, sip.NewTag()))
		case err != nil:
			t.Errorf("ReceiveSubscribe: %v", err)
		default:
			st.Respond(ok)
		}
		return
	}
	ok, sub, err := sip.AcceptSubscription(req, expires, "sip:scscf."+domain+":5060", time.Now())
	if err != nil {
		t.Errorf("AcceptSubscription: %v", err)
		return
	}
	h.mu.Lock()
	h.subs[sub.Dialog.ID.LocalTag] = &sub
	h.mu.Unlock()
	st.Respond(ok)
	h.accepted <- &sub
}

// terminate ends a subscription at the notifier, so that its next
// NOTIFY is the last one.
func (h *fakeHome) terminate(sub *sip.NotifierSubscription, reason string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	sub.Terminate(reason)
}

// notify sends a NOTIFY with doc to the P-CSCF and returns the answer.
func (h *fakeHome) notify(t *testing.T, sub *sip.NotifierSubscription, pcscf *sip.Stack, doc reginfo.Document) *sip.Message {
	return h.notifyState(t, sub, pcscf, doc, "")
}

// notifyState is notify with subscriptionState in place of the one the
// notifier would write, for a NOTIFY that carries no expires (RFC 6665
// 4.1.3).
func (h *fakeHome) notifyState(t *testing.T, sub *sip.NotifierSubscription, pcscf *sip.Stack, doc reginfo.Document, subscriptionState string) *sip.Message {
	t.Helper()
	doc.State = reginfo.Full
	body, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	req := sub.NewNotify(sip.Body{Type: reginfo.ContentType, Content: body}, time.Now())
	h.mu.Unlock()
	if subscriptionState != "" {
		req.Set("Subscription-State", subscriptionState)
	}
	req.Add("P-Charging-Vector", "icid-value=x;orig-ioi=home.example.org")
	ct, err := h.stack.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no answer to the NOTIFY: %v", ct.Err())
	}
	return final
}

func registration(state, contactState reginfo.State, event reginfo.Event) reginfo.Document {
	return reginfo.Document{Registrations: []reginfo.Registration{{AOR: impu, ID: "reg1", State: state,
		Contacts: []reginfo.Contact{{ID: "c1", State: contactState, Event: event, URI: "sip:ue@127.0.0.1"}}}}}
}

func TestPCSCFSubscribesToTheRegEventAndEndsTheAssociation(t *testing.T) {
	grace := associationGrace
	associationGrace = 300 * time.Millisecond
	t.Cleanup(func() { associationGrace = grace })
	home := newFakeHome(t)
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain},
		locatorFunc(func(string) []sip.Destination {
			return []sip.Destination{{Transport: sip.UDP, Addr: home.stack.LocalAddr()}}
		}),
		NewMemoryStore(), nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	p.SetStack(pcscfStack)
	// Fast timers, so that a request that the P-CSCF discards times out
	// soon.
	ue := newStackWith(t, fast, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag())) })
	// send sends a MESSAGE from one stack to the P-CSCF, and returns its
	// final response, or nil when none comes.
	send := func(from *sip.Stack, m *sip.Message) *sip.Message {
		t.Helper()
		m.Add("Max-Forwards", "70")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 MESSAGE")
		ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		return final
	}
	// toUE is a request from the network to the UE's contact, and fromUE
	// one from the UE; each returns the status code of its answer, or 0
	// for none.
	status := func(r *sip.Message) int {
		if r == nil {
			return 0
		}
		return r.StatusCode
	}
	toUE := func() int {
		t.Helper()
		m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:ue@127.0.0.1"}
		m.Add("Route", "<sip:term@pcscf."+domain+":5060;lr>")
		m.Add("From", "<sip:001010000000002@"+domain+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		return status(send(home.stack, m))
	}
	fromUE := func() int {
		t.Helper()
		m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:001010000000002@" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<sip:001010000000002@"+domain+">")
		return status(send(ue, m))
	}

	register := func(cseq string) {
		t.Helper()
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "call-1")
		m.Add("CSeq", cseq+" REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>;expires=600000")
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
		ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		for range ct.Responses() {
		}
	}
	register("1")

	var subscribe *sip.Message
	select {
	case subscribe = <-home.subscribes:
	case <-time.After(5 * time.Second):
		t.Fatal("the P-CSCF did not subscribe")
	}
	// TS 24.229 5.2.3 items a to g.
	from, _ := sip.ParseNameAddr(subscribe.Get("From"))
	for name, got := range map[string]bool{
		"Request-URI":         subscribe.RequestURI == impu,
		"From":                from.URI == "sip:pcscf."+domain+":5060",
		"To":                  subscribe.Get("To") == "<"+impu+">",
		"Event":               subscribe.Get("Event") == "reg",
		"Expires":             subscribe.Get("Expires") == "3761",
		"P-Asserted-Identity": subscribe.Get("P-Asserted-Identity") == "<sip:term@pcscf."+domain+":5060;lr>",
		"P-Charging-Vector":   strings.HasPrefix(subscribe.Get("P-Charging-Vector"), "icid-value=") && strings.HasSuffix(subscribe.Get("P-Charging-Vector"), ";orig-ioi="+domain),
		"Contact":             subscribe.Get("Contact") == "<sip:pcscf."+domain+":5060>",
	} {
		if !got {
			t.Errorf("SUBSCRIBE %s wrong:\n%s", name, subscribe)
		}
	}
	sub := <-home.accepted

	resp := home.notify(t, sub, pcscfStack, registration(reginfo.Active, reginfo.Active, reginfo.Registered))
	if resp.StatusCode != 200 || !strings.Contains(resp.Get("P-Charging-Vector"), "orig-ioi=home.example.org;term-ioi="+domain) {
		t.Fatalf("NOTIFY answered %d with P-Charging-Vector %q", resp.StatusCode, resp.Get("P-Charging-Vector"))
	}

	// A refresh is no first registration: no second SUBSCRIBE. The
	// refresh of the subscription is on a timer of its own (TS 24.229
	// 5.2.3), not on the REGISTERs of the UE.
	register("2")
	select {
	case m := <-home.subscribes:
		t.Fatalf("SUBSCRIBE on a reregistration:\n%s", m)
	case <-time.After(200 * time.Millisecond):
	}
	if got := fromUE(); got != 200 {
		t.Fatalf("a request from the registered UE was answered %d, want the 200 of the home network", got)
	}
	if got := toUE(); got != 200 {
		t.Fatalf("a request to the registered UE was answered %d, want the UE's 200", got)
	}

	home.terminate(sub, sip.ReasonNoResource)
	if resp := home.notify(t, sub, pcscfStack, registration(reginfo.Terminated, reginfo.Terminated, reginfo.Expired)); resp.StatusCode != 200 {
		t.Fatalf("last NOTIFY answered %d", resp.StatusCode)
	}
	// 5.2.5.2 NOTE 2: the last NOTIFY must still reach the UE, so a
	// request to it goes on for the grace, and finds no registration
	// after. The UE's own requests are discarded at once (5.2.1).
	if got := toUE(); got != 200 {
		t.Errorf("a request to the UE within the grace was answered %d, want the UE's 200", got)
	}
	if got := fromUE(); got != 0 {
		t.Errorf("a request from the UE after the registration ended was answered %d, want nothing (TS 24.229 5.2.5.2)", got)
	}
	time.Sleep(associationGrace + 200*time.Millisecond)
	if got := toUE(); got != 480 {
		t.Errorf("a request to the UE after the grace was answered %d, want 480", got)
	}
}

func TestRemovedByTheNetwork(t *testing.T) {
	active := reginfo.Registration{State: reginfo.Active}
	for _, tc := range []struct {
		r    reginfo.Registration
		c    reginfo.Contact
		want bool
	}{
		{reginfo.Registration{State: reginfo.Terminated}, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Expired}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Deactivated}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Rejected}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Unregistered}, true},
		// Another contact of the identity expired; this one stays.
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Expired}, false},
		{active, reginfo.Contact{State: reginfo.Active, Event: reginfo.Refreshed}, false},
	} {
		if got := removedByTheNetwork(tc.r, tc.c); got != tc.want {
			t.Errorf("%s registration, %s/%s contact: %v, want %v", tc.r.State, tc.c.State, tc.c.Event, got, tc.want)
		}
	}
}

// TestARecreatedPCSCFKeepsTheRegEventSubscription: the subscription of
// TS 24.229 5.2.3 is in the Store, not in the memory of the instance
// that made it. A P-CSCF made again at the same address therefore
// refreshes that subscription at the next REGISTER, within its dialog,
// and takes the NOTIFY that reports the end of the registration
// (5.2.5.2).
func TestARecreatedPCSCFKeepsTheRegEventSubscription(t *testing.T) {
	home := newFakeHome(t)
	store := NewMemoryStore()
	// Every instance answers at the same host and port: the URI in the
	// Contact of the SUBSCRIBE, where the NOTIFYs go.
	newInstance := func() (*PCSCF, *sip.Stack) {
		t.Helper()
		p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain,
			// The sweep comes round often, so that the test does not wait
			// a second for the refresh that falls due.
			SweepInterval: 20 * time.Millisecond},
			locatorFunc(func(string) []sip.Destination {
				return []sip.Destination{{Transport: sip.UDP, Addr: home.stack.LocalAddr()}}
			}),
			store, nil, quiet())
		t.Cleanup(p.Close)
		stack := newPCSCFStack(t, p)
		p.SetStack(stack)
		return p, stack
	}
	ue := newStackWith(t, fast, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag())) })
	register := func(to *sip.Stack, cseq string) {
		t.Helper()
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "call-1")
		m.Add("CSeq", cseq+" REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>;expires=600000")
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
		ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: to.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		for range ct.Responses() {
		}
	}
	nextSubscribe := func(what string) *sip.Message {
		t.Helper()
		select {
		case m := <-home.subscribes:
			return m
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s", what)
			return nil
		}
	}

	firstPCSCF, first := newInstance()
	register(first, "1")
	nextSubscribe("initial SUBSCRIBE")
	sub := <-home.accepted
	if resp := home.notify(t, sub, first, registration(reginfo.Active, reginfo.Active, reginfo.Registered)); resp.StatusCode != 200 {
		t.Fatalf("the first NOTIFY was answered %d", resp.StatusCode)
	}

	// The instance stops, and another takes its place at the same
	// address, with nothing of its own but what the Store holds.
	firstPCSCF.Close()
	first.Close()
	_, second := newInstance()

	// The new instance finds the subscription of the user and leaves it:
	// it starts no second one, and the refresh of the one there is is not
	// due yet.
	register(second, "2")
	select {
	case m := <-home.subscribes:
		t.Fatalf("the recreated instance sent a SUBSCRIBE:\n%s", m)
	case <-time.After(200 * time.Millisecond):
	}

	// The time of that refresh is in the Store, not in the process that
	// took the last NOTIFY: when it comes, the instance that is there now
	// sends the refresh, within the dialog that an earlier process
	// opened, and without waiting for a NOTIFY of its own.
	if _, ok, err := store.UpdateRegSubscription(context.Background(), impi, func(s *RegSubscription) bool {
		s.RefreshAt = time.Now()
		return true
	}); err != nil || !ok {
		t.Fatalf("the refresh of the subscription was not brought forward: %v, %v", ok, err)
	}
	select {
	case m := <-home.subscribes:
		id := sip.ReceivedDialogID(m)
		if id.CallID != sub.Dialog.ID.CallID || id.RemoteTag != sub.Dialog.ID.RemoteTag {
			t.Fatalf("the recreated instance sent no refresh of the subscription:\n%s", m)
		}
		if cseq, err := sip.ParseCSeq(m.Get("CSeq")); err != nil || cseq.Number != 2 {
			t.Errorf("the refresh has CSeq %q, want 2 SUBSCRIBE", m.Get("CSeq"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the NOTIFY started no refresh at the recreated instance")
	}

	// The S-CSCF reports that the registration has ended. The new
	// instance takes the NOTIFY of a subscription that it never sent a
	// SUBSCRIBE for, and ends the IP association of the UE.
	home.terminate(sub, sip.ReasonNoResource)
	if resp := home.notify(t, sub, second, registration(reginfo.Terminated, reginfo.Terminated, reginfo.Expired)); resp.StatusCode != 200 {
		t.Fatalf("the NOTIFY that ends the registration was answered %d, want 200", resp.StatusCode)
	}
	source := sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()}
	a, ok, err := store.Association(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || time.Now().Before(a.Expires) {
		t.Errorf("the IP association of the UE has not ended: %+v, %v", a, ok)
	}
}

// regEventFixture is one P-CSCF with one registered UE, for the tests of
// the timers of its subscription. refresh and notifyWait replace the
// timers of the P-CSCF, so that the tests do not wait out the real ones.
type regEventFixture struct {
	home  *fakeHome
	p     *PCSCF
	store *MemoryStore
	ue    *sip.Stack
	stack *sip.Stack
}

func newRegEventFixture(t *testing.T, refresh func(time.Duration) time.Duration, notifyWait time.Duration) *regEventFixture {
	t.Helper()
	f := &regEventFixture{home: newFakeHome(t), store: NewMemoryStore()}
	f.p = New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain,
		// The sweep comes round often, so that the tests do not wait a
		// second for each expiry they shorten.
		SweepInterval: 20 * time.Millisecond},
		locatorFunc(func(string) []sip.Destination {
			return []sip.Destination{{Transport: sip.UDP, Addr: f.home.stack.LocalAddr()}}
		}),
		f.store, nil, quiet())
	t.Cleanup(f.p.Close)
	if refresh != nil {
		f.p.regEventRefresh = refresh
	}
	if notifyWait != 0 {
		f.p.notifyWait = notifyWait
	}
	f.stack = newPCSCFStack(t, f.p)
	f.p.SetStack(f.stack)
	f.ue = newStackWith(t, fast, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag())) })
	return f
}

// register sends a REGISTER of the UE through the P-CSCF and waits for
// its final response.
func (f *regEventFixture) register(t *testing.T, cseq string) {
	t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", "call-1")
	m.Add("CSeq", cseq+" REGISTER")
	m.Add("Contact", "<sip:ue@127.0.0.1>;expires=600000")
	m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
	ct, err := f.ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: f.stack.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	for range ct.Responses() {
	}
}

// awaitGranted waits until the Store holds granted as the duration the
// 200 to the SUBSCRIBE gave the subscription.
func (f *regEventFixture) awaitGranted(t *testing.T, granted time.Duration) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sub, ok, err := f.store.RegSubscription(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}
		if ok && sub.Granted == granted {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the 200 to the SUBSCRIBE granted %s, want %s", sub.Granted, granted)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// nextSubscribe is the next SUBSCRIBE the home receives, or a failure.
func (f *regEventFixture) nextSubscribe(t *testing.T, what string) *sip.Message {
	t.Helper()
	select {
	case m := <-f.home.subscribes:
		return m
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
		return nil
	}
}

// TestThePCSCFRefreshesItsRegEventSubscriptionWhenItFallsDue: the refresh
// of TS 24.229 5.2.3 falls due at the time counted from the duration the
// subscription has, which its NOTIFY gives, and not at a REGISTER of the
// UE. The instance that is running sends it within the dialog, straight
// to the S-CSCF.
func TestThePCSCFRefreshesItsRegEventSubscriptionWhenItFallsDue(t *testing.T) {
	counted := make(chan time.Duration, 4)
	f := newRegEventFixture(t, func(d time.Duration) time.Duration {
		counted <- d
		return 20 * time.Millisecond
	}, 0)

	f.register(t, "1")
	f.nextSubscribe(t, "initial SUBSCRIBE")
	sub := <-f.home.accepted
	// The NOTIFY says the subscription has 3761 s, and starts the timer.
	if resp := f.home.notify(t, sub, f.stack, registration(reginfo.Active, reginfo.Active, reginfo.Registered)); resp.StatusCode != 200 {
		t.Fatalf("the first NOTIFY was answered %d", resp.StatusCode)
	}
	select {
	case d := <-counted:
		if d.Round(time.Second) != 3761*time.Second {
			t.Errorf("the refresh was counted from %s, want the 3761 s of the subscription", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the NOTIFY started no timer for the refresh")
	}

	refresh := f.nextSubscribe(t, "refresh of the subscription")
	id := sip.ReceivedDialogID(refresh)
	if id.CallID != sub.Dialog.ID.CallID || id.LocalTag != sub.Dialog.ID.LocalTag || id.RemoteTag != sub.Dialog.ID.RemoteTag {
		t.Fatalf("the SUBSCRIBE is no refresh of the subscription:\n%s", refresh)
	}
	if cseq, err := sip.ParseCSeq(refresh.Get("CSeq")); err != nil || cseq.Number != 2 {
		t.Errorf("the refresh has CSeq %q, want 2 SUBSCRIBE", refresh.Get("CSeq"))
	}
	// RFC 6665 4.1.2.2: it goes to the remote target, the S-CSCF, not
	// through the I-CSCF, and asks for the same duration again.
	if refresh.RequestURI != "sip:scscf."+domain+":5060" || refresh.Get("Expires") != "3761" {
		t.Errorf("the refresh went to %q with Expires %q", refresh.RequestURI, refresh.Get("Expires"))
	}
	// The refresh is in the Store before it goes out, so that the NOTIFY
	// that answers it is in order at whichever instance takes it.
	stored, ok, err := f.store.RegSubscription(context.Background(), impi)
	if err != nil || !ok || stored.Dialog.LocalSeq != 2 {
		t.Errorf("the refreshed subscription in the Store = %+v, %v, %v", stored, ok, err)
	}
}

// TestTimerNEndsARegEventSubscriptionWithoutANotify: a SUBSCRIBE that no
// NOTIFY follows leaves no subscription (RFC 6665 4.1.2.1, 4.1.2.4). The
// P-CSCF forgets it when Timer N fires, and the user's next REGISTER
// subscribes again.
func TestTimerNEndsARegEventSubscriptionWithoutANotify(t *testing.T) {
	f := newRegEventFixture(t, nil, 50*time.Millisecond)

	f.register(t, "1")
	first := f.nextSubscribe(t, "initial SUBSCRIBE")
	// The home answers 200 and sends no NOTIFY. The subscription is in
	// the Store from before the SUBSCRIBE went out, waiting for its first
	// NOTIFY, so Timer N is what takes it away again.
	<-f.home.accepted
	waiting, ok, err := f.store.RegSubscription(context.Background(), impi)
	if err != nil || !ok || waiting.Dialog.ID.RemoteTag != "" {
		t.Fatalf("the subscription waiting for its first NOTIFY = %+v, %v, %v", waiting, ok, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		_, ok, err := f.store.RegSubscription(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Timer N did not end the subscription that got no NOTIFY")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The next REGISTER of the user subscribes again, with a Call-ID of
	// its own.
	f.register(t, "2")
	second := f.nextSubscribe(t, "SUBSCRIBE after Timer N")
	if second.Get("Call-ID") == first.Get("Call-ID") || sip.ReceivedDialogID(second).LocalTag != "" {
		t.Errorf("the SUBSCRIBE after Timer N is no new subscription:\n%s", second)
	}
}

// TestAnUnansweredRegEventRefreshDoesNotHoldTheSubscription: the expiry
// of a subscription is what the notifier gave, so a refresh must not
// move it before it is answered. A refresh that fails leaves the
// subscription to run out when the S-CSCF drops it, and the Store then
// forgets it so that the next REGISTER subscribes again.
func TestAnUnansweredRegEventRefreshDoesNotHoldTheSubscription(t *testing.T) {
	f := newRegEventFixture(t, func(time.Duration) time.Duration { return 500 * time.Millisecond }, 0)
	// The S-CSCF gives the subscription 2 s and turns the refresh down
	// with a 500, which does not end it (RFC 6665 4.1.2.2).
	f.home.mu.Lock()
	f.home.grant = 2 * time.Second
	f.home.refuseRefresh = 500
	f.home.mu.Unlock()

	f.register(t, "1")
	f.nextSubscribe(t, "initial SUBSCRIBE")
	sub := <-f.home.accepted
	started := time.Now()
	if resp := f.home.notify(t, sub, f.stack, registration(reginfo.Active, reginfo.Active, reginfo.Registered)); resp.StatusCode != 200 {
		t.Fatalf("the first NOTIFY was answered %d", resp.StatusCode)
	}
	f.nextSubscribe(t, "refresh of the subscription")

	// The refresh went out around 500 ms in and was turned down. The
	// subscription must still end 2 s after the NOTIFY gave it 2 s, not
	// 2 s after the refresh.
	stored, ok, err := f.store.RegSubscription(context.Background(), impi)
	if err != nil || !ok {
		t.Fatalf("the subscription after a refused refresh = %v, %v", ok, err)
	}
	if latest := started.Add(2*time.Second + 300*time.Millisecond); stored.Expires.After(latest) {
		t.Errorf("the refused refresh moved the expiry to %s, more than the %s the S-CSCF gave",
			stored.Expires.Sub(started), 2*time.Second)
	}
}

// TestARegEventNotifyWithoutExpiresTakesTheGrantedDuration: a NOTIFY
// that carries no expires stands for the duration the 2xx to the
// SUBSCRIBE granted (TS 24.229 5.1.1.3 item 2), and a refresh asks again
// for the duration the subscription was set up with, not for the time
// left.
func TestARegEventNotifyWithoutExpiresTakesTheGrantedDuration(t *testing.T) {
	f := newRegEventFixture(t, func(time.Duration) time.Duration { return 20 * time.Millisecond }, 0)
	// The P-CSCF asks for 3761 s; the S-CSCF grants 120.
	f.home.mu.Lock()
	f.home.grant = 120 * time.Second
	f.home.mu.Unlock()

	f.register(t, "1")
	if asked := f.nextSubscribe(t, "initial SUBSCRIBE").Get("Expires"); asked != "3761" {
		t.Fatalf("the SUBSCRIBE asked for %q", asked)
	}
	sub := <-f.home.accepted
	// The 200 to the SUBSCRIBE is what grants the duration, and the first
	// NOTIFY may overtake it (RFC 6665 4.1.2.4). This test is about the
	// NOTIFY that comes after it, so it waits for the grant; a NOTIFY that
	// arrives first takes the duration of the SUBSCRIBE instead.
	f.awaitGranted(t, 120*time.Second)
	started := time.Now()
	if resp := f.home.notifyState(t, sub, f.stack, registration(reginfo.Active, reginfo.Active, reginfo.Registered),
		"active"); resp.StatusCode != 200 {
		t.Fatalf("the NOTIFY without expires was answered %d", resp.StatusCode)
	}

	stored, ok, err := f.store.RegSubscription(context.Background(), impi)
	if err != nil || !ok {
		t.Fatalf("the subscription = %v, %v", ok, err)
	}
	if left := stored.Expires.Sub(started).Round(time.Second); left != 120*time.Second {
		t.Errorf("the NOTIFY without expires left the subscription %s, want the 120 s the 200 granted", left)
	}

	// RFC 6665 4.1.2.2: the refresh asks for the duration of the initial
	// SUBSCRIBE again.
	if asked := f.nextSubscribe(t, "refresh of the subscription").Get("Expires"); asked != "3761" {
		t.Errorf("the refresh asked for %q, want the 3761 of the initial SUBSCRIBE", asked)
	}
}

// TestARecreatedPCSCFReplacesASubscriptionThatGotNoNotify: a SUBSCRIBE
// whose process is gone leaves a subscription in the Store that no
// Timer N will end. Once Timer N has run out, the next REGISTER of the
// user takes it away and subscribes again, instead of leaving the user
// unwatched until the subscription runs out.
func TestARecreatedPCSCFReplacesASubscriptionThatGotNoNotify(t *testing.T) {
	f := newRegEventFixture(t, nil, 50*time.Millisecond)
	// What an instance that stopped between its SUBSCRIBE and the first
	// NOTIFY left behind: a dialog with no remote tag, sent long enough
	// ago that Timer N would have fired.
	stale := RegSubscription{PrivateIdentity: impi, PublicIdentity: impu,
		Dialog:         sip.Dialog{ID: sip.DialogID{CallID: "gone", LocalTag: "l"}, LocalSeq: 1},
		Requested:      3761 * time.Second,
		NotifyDeadline: time.Now().Add(-time.Minute),
		Expires:        time.Now().Add(time.Hour)}
	if ok, err := f.store.CreateRegSubscription(context.Background(), stale); err != nil || !ok {
		t.Fatalf("CreateRegSubscription = %v, %v", ok, err)
	}

	f.register(t, "1")
	m := f.nextSubscribe(t, "SUBSCRIBE for the user of the subscription that got no NOTIFY")
	if m.Get("Call-ID") == stale.Dialog.ID.CallID || sip.ReceivedDialogID(m).LocalTag != "" {
		t.Errorf("the SUBSCRIBE is no new subscription:\n%s", m)
	}
	stored, ok, err := f.store.RegSubscription(context.Background(), impi)
	if err != nil || !ok || stored.Dialog.ID.CallID != m.Get("Call-ID") {
		t.Errorf("the stored subscription = %+v, %v, %v; want the new one", stored, ok, err)
	}
}

// confirmOnRead is the Store of an instance whose Timer N comes up at
// the moment the NOTIFY it was waiting for reaches another instance: the
// read of the handler gives the subscription that still waits, and the
// NOTIFY lands before the handler can act on it.
type confirmOnRead struct {
	Store
	once      sync.Once
	confirmed atomic.Bool
}

func (s *confirmOnRead) RegSubscription(ctx context.Context, privateID string) (RegSubscription, bool, error) {
	sub, ok, err := s.Store.RegSubscription(ctx, privateID)
	if ok {
		s.once.Do(func() {
			_, written, err := s.Store.UpdateRegSubscription(ctx, privateID, func(current *RegSubscription) bool {
				current.Dialog.ID.RemoteTag = "s"
				current.NotifyDeadline = time.Time{}
				current.RefreshAt = time.Now().Add(time.Hour)
				return true
			})
			s.confirmed.Store(written && err == nil)
		})
	}
	return sub, ok, err
}

// TestTimerNKeepsASubscriptionThatANotifyConfirmed: the NOTIFY that
// Timer N waited for reaches another instance between the read of the
// handler and the removal. The subscription stays, since the removal
// names the dialog and the deadline that the handler read; without that,
// the user would be left unwatched although the S-CSCF still has the
// subscription (RFC 6665 4.1.2.4).
func TestTimerNKeepsASubscriptionThatANotifyConfirmed(t *testing.T) {
	store := NewMemoryStore()
	confirming := &confirmOnRead{Store: store}
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain,
		// The sweep of this instance must not claim the expiry: the test
		// hands it to the handler itself.
		SweepInterval: time.Hour},
		locatorFunc(func(string) []sip.Destination { return nil }), confirming, nil, quiet())
	t.Cleanup(p.Close)

	ctx := context.Background()
	waiting := RegSubscription{PrivateIdentity: impi, PublicIdentity: impu,
		Dialog:         sip.Dialog{ID: sip.DialogID{CallID: "sub-1", LocalTag: "l"}, LocalSeq: 1},
		Requested:      3761 * time.Second,
		NotifyDeadline: time.Now().Add(-time.Second),
		Expires:        time.Now().Add(time.Hour)}
	if ok, err := store.CreateRegSubscription(ctx, waiting); err != nil || !ok {
		t.Fatalf("CreateRegSubscription = %v, %v", ok, err)
	}

	if err := p.regEventNotifyWaitOver(ctx, impi); err != nil {
		t.Fatalf("Timer N: %v", err)
	}
	if !confirming.confirmed.Load() {
		t.Fatal("no NOTIFY came between the read and the removal; the test proved nothing")
	}
	sub, ok, err := store.RegSubscription(ctx, impi)
	if err != nil || !ok {
		t.Fatalf("the subscription that the NOTIFY confirmed is gone: %v, %v", ok, err)
	}
	if sub.Dialog.ID.RemoteTag == "" || !sub.NotifyDeadline.IsZero() {
		t.Errorf("the stored subscription = %+v; want the one the NOTIFY confirmed", sub)
	}
}
