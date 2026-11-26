package mmtel_test

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the AS while the operator has it out of service
// through the maintenance API of its node.

// asRole is the name of the MMTel AS in the configuration file, which
// is the path segment of its maintenance API.
const asRole = string(config.RoleMMTel)

// asRetryAfter is the Retry-After that the AS answers with its 503, in
// seconds.
const asRetryAfter = "30"

// available is the state of the Sh peers of an AS that reaches its HSS.
func available() bool { return true }

// manage gives as the maintenance API of a node, as the node of the AS
// does, and returns the node. hssAvailable is what the AS reports as
// the state of its Sh peers.
func manage(t *testing.T, as testAS, hssAvailable func() bool) *maintenance.Node {
	t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          asRole,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{as.Function(hssAvailable)},
		Configuration: func() any { return map[string]string{"node": asRole} },
	})
	if err != nil {
		t.Fatal(err)
	}
	as.Manage(node.ManagedFunction(asRole))
	return node
}

// asState is the state of the AS in the maintenance API of node.
func asState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	return node.State().Functions[0]
}

// setState makes the event that takes the AS to want, and returns its
// state after it.
func setState(t *testing.T, node *maintenance.Node, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := node.SetAdministrativeState(asRole, maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// A shut down AS refuses the INVITE of a new call with 503 and
// Retry-After, and sends nothing on: the S-CSCF then applies the
// default handling of the filter criterion (TS 24.229 5.4.3.2). With no
// call in progress it is LOCKED at once, and unlocked it serves calls
// again.
func TestAShutDownASRefusesANewCall(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	node := manage(t, as, available)
	reached := make(chan bool, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		reached <- true
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v, want LOCKED with no call in progress", f)
	}
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	refusal := got[len(got)-1]
	if refusal.StatusCode != 503 || refusal.Get("Retry-After") != asRetryAfter {
		t.Errorf("caller got %v with Retry-After %q, want 503 with %s", statuses(got), refusal.Get("Retry-After"), asRetryAfter)
	}
	if w := refusal.Get("Warning"); !strings.Contains(w, "Out of service") {
		t.Errorf("the 503 has Warning %q, want one that says the AS is out of service", w)
	}
	if len(reached) > 0 {
		t.Error("the callee got the INVITE of a refused call, want none sent on")
	}

	setState(t, node, maintenance.Unlocked)
	out, err = caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, out); got[len(got)-1].StatusCode != 200 {
		t.Errorf("the call after the unlock got %v, want 200", statuses(got))
	}
	receive(t, reached, "the INVITE at the callee after the unlock")
}

// A diversion of a call in progress comes back to the AS as the INVITE
// of the originating services after retargeting (orig-cdiv, TS 24.229
// 5.4.3.3 step 3). It carries on with a call that began before the shut
// down, so the shut down AS serves it rather than end that call.
func TestAShutDownASServesTheDiversionOfACallInProgress(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	node := manage(t, as, available)
	reached := make(chan bool, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		reached <- true
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })

	setState(t, node, maintenance.ShuttingDown)
	out, err := caller.Invite(viaAS(t, as, callee, "orig-cdiv", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, out); got[len(got)-1].StatusCode != 200 {
		t.Errorf("the diverted call got %v, want the 200 of the diverted-to user", statuses(got))
	}
	receive(t, reached, "the diverted INVITE at the diverted-to user")
}

// The users of the AS are the calls it is serving. A shut down waits
// for them: the AS stays SHUTTINGDOWN while a call is up, serves the
// requests within its dialogs, refuses the INVITE of a new call
// meanwhile, and becomes LOCKED once the call has ended. A refused call
// is no user of the AS and costs it no Sh request: the AS refuses the
// INVITE before it would read the served user's services.
func TestAShutDownWaitsForTheCallsOfTheAS(t *testing.T) {
	hss := &documents{docs: map[string]string{}}
	as := startAS(t, hss, mmtel.Config{})
	node := manage(t, as, available)
	calls := make(chan *sip.IncomingCall, 2)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) { r.Accept(held) })
		calls <- c
		c.Accept(answer)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, out); got[len(got)-1].StatusCode != 200 {
		t.Fatalf("the call got %v, want 200", statuses(got))
	}
	in := receive(t, calls, "the call at the callee")
	udrs := len(hss.askedFor())
	if udrs == 0 {
		t.Fatalf("the AS read no service data for the call it serves, want the Sh read of its initial INVITE")
	}

	if f := asState(t, node); f.Users == nil || *f.Users != 1 {
		t.Fatalf("the AS is %+v with a call up, want one user", f)
	}
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a call is up", f)
	}
	second, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, second); got[len(got)-1].StatusCode != 503 {
		t.Errorf("the new call got %v while the AS is shut down, want 503", statuses(got))
	}
	if len(calls) > 0 {
		t.Error("the callee got the INVITE of the refused call, want none sent on")
	}
	if f := asState(t, node); f.Users == nil || *f.Users != 1 {
		t.Errorf("the AS is %+v after the refusal, want the one user of the call it serves", f)
	}
	if got := hss.askedFor(); len(got) != udrs {
		t.Errorf("the AS read service data %d times, want the %d of the call it serves: the refused call asks the HSS for nothing (%v)",
			len(got), udrs, got)
	}
	// HOLD is a request within the dialog, which the AS relays while it
	// is shut down as it would have.
	if resp, err := out.ReInvite(hold); err != nil || resp.StatusCode != 200 {
		t.Errorf("the HOLD of the call got %v, %v; want 200", resp, err)
	}
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	receive(t, in.Done(), "the end of the callee's call")
	waitFor(t, func() bool { return asState(t, node).AdministrativeState == maintenance.Locked },
		"the AS to be LOCKED once its last call has ended")
}

// Without an instance of the HSS that takes its Sh requests the AS can
// serve no call: it reads the settings of the served user's services
// for every initial INVITE. So it reports itself DISABLED, without the
// operator having taken it out of service.
func TestTheASIsDisabledWithoutTheHSS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	reachable := true
	node := manage(t, as, func() bool { return reachable })
	if f := asState(t, node); f.OperationalState != maintenance.Enabled {
		t.Errorf("the AS is %s with its Sh peer open, want ENABLED", f.OperationalState)
	}
	reachable = false
	if f := asState(t, node); f.OperationalState != maintenance.Disabled {
		t.Errorf("the AS is %s without an Sh peer, want DISABLED", f.OperationalState)
	}
}

// factoryInvite is the INVITE to the conference factory URI as the
// originating filter criteria of its sender give it to the AS (TS
// 24.147 5.3.2.3.1); other stands in for the next hop of the Route.
func factoryInvite(t *testing.T, as testAS, other hop) sip.Invitation {
	t.Helper()
	inv := viaAS(t, as, other, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	return inv
}

// newConference has creator create a conference of as, and returns the
// creator's call with the focus and the conference URI with isfocus
// that the 200 gives as Contact.
func newConference(t *testing.T, as testAS, creator *sip.UA, other hop) (*sip.OutgoingCall, sip.NameAddr) {
	t.Helper()
	conf, err := creator.Invite(factoryInvite(t, as, other))
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, conf)
	ok := resps[len(resps)-1]
	focus, err := sip.ParseNameAddr(ok.Get("Contact"))
	if err != nil || ok.StatusCode != 200 {
		t.Fatalf("the creator got %v with Contact %q, want 200 with the conference URI", statuses(resps), ok.Get("Contact"))
	}
	return conf, focus
}

// subscribeToConference subscribes user's UA to the conference event
// package of the conference URI uri, outside the dialog of any
// conference session (GSMA IR.92 2.3.3), and sends each NOTIFY to
// notifications. A refusal is in the Err of the subscription it
// returns, not in an error here.
func subscribeToConference(t *testing.T, ua *sip.UA, user, uri string, notifications chan sip.Notification) *sip.Subscription {
	t.Helper()
	sub, err := ua.Subscribe(sip.SubscribeRequest{RequestURI: uri, From: "sip:" + user + "@" + domain,
		Event: confinfo.EventPackage, Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:" + user + "@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// A shut down AS refuses a SUBSCRIBE that would start a subscription to
// a conference that this instance does not host, as it refuses any
// other session of its own, and serves the one within the dialog of a
// subscription it has, which ends that subscription (RFC 6665 4.1.2.3).
// The conference it hosts goes on: it is the call of the user that
// created it, which the shut down waits for.
func TestAShutDownASRefusesANewSubscriptionToAnUnknownConference(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	node := manage(t, as, available)
	bob := newParticipant(t, as, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	_, focus := newConference(t, as, alice, bob)
	notifications := make(chan sip.Notification, 8)
	sub := subscribeToConference(t, alice, "alice", focus.URI, notifications)
	receive(t, notifications, "the first NOTIFY of the creator's subscription")

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while the conference is up", f)
	}
	// No conference of this instance has this URI, so the subscription
	// would be a session of the AS's own: the shut down AS answers the
	// 503 of its refusal, not the 404 of a conference URI that is not
	// allocated.
	refused := subscribeToConference(t, bob, "bob", "sip:conf-nosuchconference@"+conferenceDomain, make(chan sip.Notification, 8))
	receive(t, refused.Done(), "the end of the refused subscription")
	var no *sip.SubscribeRefused
	if err := refused.Err(); !errors.As(err, &no) || no.StatusCode != 503 {
		t.Errorf("the new subscription ended with %v, want a refusal with 503", err)
	}
	// The subscription that the AS has ends within its dialog, with the
	// NOTIFY that says terminated.
	if err := sub.Unsubscribe(); err != nil {
		t.Errorf("unsubscribing while the AS is shut down: %v", err)
	}
	receive(t, sub.Done(), "the end of the creator's subscription")
	if err := sub.Err(); err != nil {
		t.Errorf("the creator's subscription ended with %v, want the NOTIFY that says terminated", err)
	}
}

// A shut down AS goes on with a conference it hosts. That conference
// lives in this instance alone, since DNS leads its URI here and no
// other instance has its dialogs, so the AS serves the INVITE of a user
// that dials in (TS 24.147 5.3.2.4.1), the INVITE of the originating
// services that bring a user of this network to that URI, and the
// initial SUBSCRIBE that such a user sends to the conference event
// package. It takes nothing else new: the INVITE to the conference
// factory URI, which would create a conference of its own, gets the
// same 503 as a new call. Each INVITE that the AS took is a user of it,
// which the shut down waits for, and the conference is the call of the
// user that created it: it ends when that user leaves (5.3.2.7 item 3),
// and the AS is LOCKED once it has.
func TestAShutDownASGoesOnWithTheConferenceItHosts(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	node := manage(t, as, available)
	bob := newParticipant(t, as, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	conf, focus := newConference(t, as, alice, bob)

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while the conference is up", f)
	}
	joined, err := bob.Invite(joining("bob", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, joined); got[len(got)-1].StatusCode != 200 {
		t.Fatalf("the user that dialed in got %v while the AS is shut down, want 200", statuses(got))
	}
	if f := asState(t, node); f.Users == nil || *f.Users != 2 {
		t.Errorf("the AS is %+v, want the conference and the user that dialed into it as two users", f)
	}
	notifications := make(chan sip.Notification, 8)
	subscribeToConference(t, bob, "bob", focus.URI, notifications)
	awaitNotification(t, notifications, "of the subscription of the user that dialed in",
		func(_ sip.Notification, d confinfo.Document) bool { return len(d.Users.Users) == 2 })

	// A user of this network comes to the conference through its own
	// originating services first, which the filter criteria send to an
	// AS as for any call (TS 24.147 5.3.1.4.1). That INVITE names the
	// conference of this instance too, so the shut down AS relays it on
	// along the Route instead of refusing it, and the S-CSCF then finds
	// the focus by DNS.
	relayed := make(chan *sip.IncomingCall, 1)
	scscf := newUA(t, "scscf", func(c *sip.IncomingCall) {
		relayed <- c
		c.Reject(480)
	})
	inv := viaAS(t, as, scscf, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = focus.URI, focus.URI
	services, err := bob.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, services)); got[len(got)-1] != 480 {
		t.Errorf("the originating call to the conference URI got %v while the AS is shut down, want the 480 of the next hop", got)
	}
	receive(t, relayed, "the INVITE that the AS relayed towards the focus")

	another, err := bob.Invite(factoryInvite(t, as, bob))
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, another)
	refusal := resps[len(resps)-1]
	if refusal.StatusCode != 503 || refusal.Get("Retry-After") != asRetryAfter {
		t.Errorf("the INVITE to the conference factory URI got %v with Retry-After %q, want 503 with %s",
			statuses(resps), refusal.Get("Retry-After"), asRetryAfter)
	}

	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	receive(t, joined.Done(), "the BYE of the focus to the user that dialed in")
	waitFor(t, func() bool { return asState(t, node).AdministrativeState == maintenance.Locked },
		"the AS to be LOCKED once the conference it hosted has ended")
}

// utRequest is the GET of a UE that reads its own supplementary service
// settings over Ut (TS 24.623 5.3.1.2), as the XCAP server of the AS
// sees it.
func utRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet,
		"/xcap-root/simservs.ngn.etsi.org/users/sip:alice@"+domain+"/simservs.xml", nil)
}

// utServer is the XCAP server of Ut that AS.Ut wraps: it answers 200 to
// every request, and counts them.
type utServer struct {
	served chan *http.Request
	// entered reports each request as the server takes it, and release
	// holds it there until the test closes the channel. A nil release
	// answers at once.
	entered chan struct{}
	release chan struct{}
}

func newUtServer() *utServer {
	return &utServer{served: make(chan *http.Request, 8), entered: make(chan struct{}, 8)}
}

// tookOne waits until the server has taken and answered one request.
func (u *utServer) tookOne(t *testing.T, what string) {
	t.Helper()
	receive(t, u.entered, what)
	receive(t, u.served, what)
}

func (u *utServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u.entered <- struct{}{}
	if u.release != nil {
		<-u.release
	}
	u.served <- r
	w.WriteHeader(http.StatusOK)
}

// Ut goes out of service with the AS: a request that arrives while the
// AS is not UNLOCKED is refused with 503 and Retry-After (RFC 9110
// 15.6.4, 10.2.3), and never reaches the XCAP server. The settings live
// in the HSS, which every instance reads, so a user that is refused
// here reaches the same documents through another instance.
func TestAShutDownASRefusesANewUtRequest(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	node := manage(t, as, available)
	server := newUtServer()
	ut := as.Ut(server)

	rec := httptest.NewRecorder()
	ut.ServeHTTP(rec, utRequest())
	if rec.Code != http.StatusOK {
		t.Fatalf("the Ut request of an unlocked AS got %d, want 200", rec.Code)
	}
	server.tookOne(t, "the Ut request at the XCAP server of an unlocked AS")

	for _, want := range []maintenance.AdministrativeState{maintenance.ShuttingDown, maintenance.Locked} {
		setState(t, node, want)
		rec := httptest.NewRecorder()
		ut.ServeHTTP(rec, utRequest())
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != asRetryAfter {
			t.Errorf("the Ut request of a %s AS got %d with Retry-After %q, want 503 with %s",
				want, rec.Code, rec.Header().Get("Retry-After"), asRetryAfter)
		}
		if len(server.entered) > 0 {
			t.Errorf("the XCAP server got the Ut request refused by a %s AS, want none passed on", want)
		}
		if f := asState(t, node); f.Users == nil || *f.Users != 0 {
			t.Errorf("the AS is %+v after the refusal, want no user of a refused request", f)
		}
	}

	setState(t, node, maintenance.Unlocked)
	rec = httptest.NewRecorder()
	ut.ServeHTTP(rec, utRequest())
	if rec.Code != http.StatusOK {
		t.Errorf("the Ut request after the unlock got %d, want 200", rec.Code)
	}
	server.tookOne(t, "the Ut request at the XCAP server after the unlock")
}

// A request of Ut that the XCAP server has begun to answer is a user of
// the AS, as a call is: it is answered, and the shut down waits for it.
func TestAShutDownWaitsForTheUtRequestsOfTheAS(t *testing.T) {
	as := startAS(t, noServices{}, mmtel.Config{})
	node := manage(t, as, available)
	server := newUtServer()
	server.release = make(chan struct{})
	ut := as.Ut(server)
	answered := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		ut.ServeHTTP(rec, utRequest())
		answered <- rec.Code
	}()
	receive(t, server.entered, "the Ut request at the XCAP server")

	if f := asState(t, node); f.Users == nil || *f.Users != 1 {
		t.Fatalf("the AS is %+v with a Ut request in progress, want one user", f)
	}
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a Ut request is in progress", f)
	}
	close(server.release)
	if code := receive(t, answered, "the answer to the Ut request"); code != http.StatusOK {
		t.Errorf("the Ut request in progress at the shut down got %d, want the 200 of the XCAP server", code)
	}
	waitFor(t, func() bool { return asState(t, node).AdministrativeState == maintenance.Locked },
		"the AS to be LOCKED once its last Ut request has been answered")
}
