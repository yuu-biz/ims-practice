package hss

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// scraped is the value of series, such as
// ims_hss_public_identities{format="tel"}, in what Prometheus would
// scrape from reg, and -1 when reg has no such series.
func scraped(t *testing.T, reg *metrics.Registry, series string) float64 {
	t.Helper()
	var text bytes.Buffer
	if err := reg.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(text.String(), "\n") {
		if v, ok := strings.CutPrefix(line, series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			return f
		}
	}
	return -1
}

// counting starts an HSS on a MemoryStore that holds the two
// subscribers of the tests, counting in a registry of the test, and
// returns the HSS and that registry.
func counting(t *testing.T) (*HSS, *metrics.Registry) {
	t.Helper()
	store, err := NewMemoryStore(
		Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, "tel:+12025550101"}, Password: "test-password-1"},
		Subscription{PrivateIdentity: otherImp, PublicIdentities: []string{"sip:001010000000002@" + realm}, Password: "test-password-2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h, err := New(Config{Realm: realm, VisitedNetworks: []string{realm},
		ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}}, Metrics: reg}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return h, reg
}

const (
	subscriptions     = `ims_hss_subscriptions`
	sipIdentities     = `ims_hss_public_identities{format="sip"}`
	telIdentities     = `ims_hss_public_identities{format="tel"}`
	registeredPublic  = `ims_hss_registered_public_identities`
	registeredPrivate = `ims_hss_registered_private_identities`
)

// want reads each series and reports the ones whose value is not the
// one wanted.
func want(t *testing.T, reg *metrics.Registry, when string, values map[string]float64) {
	t.Helper()
	for series, v := range values {
		if got := scraped(t, reg, series); got != v {
			t.Errorf("%s %s = %v, want %v", when, series, got, v)
		}
	}
}

// The gauges of TS 32.409 4.2.1.1 start at what the store holds, not
// at zero: the subscribers of the configuration are in it before the
// HSS counts. None of them is registered yet (4.2.1.2).
func TestTheGaugesStartAtWhatTheStoreHolds(t *testing.T) {
	_, reg := counting(t)
	want(t, reg, "at the start", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 0, registeredPrivate: 0,
	})
}

// A registration over Cx moves the public and the private identities of
// the subscription into the registered state (TS 32.409 4.2.1.2.1 and
// 4.2.1.2.3), and the deregistration moves them out again. The
// provisioned numbers do not move.
func TestARegistrationCountsTheIdentitiesOfTheSubscription(t *testing.T) {
	h, reg := counting(t)
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	want(t, reg, "while registered", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		// The SIP URI and the tel URI of the one subscriber.
		registeredPublic: 2, registeredPrivate: 1,
	})

	sar(t, h, scscf, cx.UserDeregistration)
	want(t, reg, "after the deregistration", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 0, registeredPrivate: 0,
	})
}

// The provisioning API moves the numbers of TS 32.409 4.2.1.1: a
// subscription that is added raises them, and one that is removed
// lowers them, with the registered identities it had.
func TestProvisioningMovesTheProvisionedNumbers(t *testing.T) {
	h, reg := counting(t)
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "001010000000003@` + realm + `",` +
		` "public_identities": ["sip:001010000000003@` + realm + `", "tel:+12025550103"], "password": "test-password-3"}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	want(t, reg, "after the subscription was added", map[string]float64{
		subscriptions: 3, sipIdentities: 3, telIdentities: 2,
	})

	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	if w := do(t, api, "DELETE", "/subscriptions/"+impi, ""); w.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	want(t, reg, "after the registered subscription was removed", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 0, registeredPrivate: 0,
	})
}

// uncounted is a store that keeps none of the numbers of TS 32.409
// 4.2.1, as the store on kvstore that several HSS instances share does.
type uncounted struct{ Store }

func (uncounted) Counts(context.Context) (Counts, bool, error) { return Counts{}, false, nil }

// An HSS whose store keeps none of the numbers registers none of the
// gauges, so that a scrape shows no series rather than a zero for a
// store that holds subscribers.
func TestAnHSSOnASharedStoreServesNoDatabaseUsageMeasurement(t *testing.T) {
	reg := metrics.NewRegistry()
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	store, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{Realm: realm, VisitedNetworks: []string{realm}, Metrics: reg}, uncounted{store}, node, quiet()); err != nil {
		t.Fatal(err)
	}
	for _, series := range []string{subscriptions, sipIdentities, telIdentities, registeredPublic, registeredPrivate} {
		if got := scraped(t, reg, series); got != -1 {
			t.Errorf("%s = %v, want no such series", series, got)
		}
	}
}

// A provisioning request that the HSS refuses moves nothing: the
// subscription is already there, so the numbers of TS 32.409 4.2.1.1
// stay as they were.
func TestAnAddThatIsRefusedMovesNothing(t *testing.T) {
	h, reg := counting(t)
	api := h.Provisioning(&fakeSCSCFs{})
	body := `{"private_identity": "` + impi + `",` +
		` "public_identities": ["` + impu + `", "tel:+12025550101"], "password": "test-password-1"}`
	if w := do(t, api, "POST", "/subscriptions", body); w.Code != http.StatusConflict {
		t.Fatalf("POST of a subscription that is there: %d %s", w.Code, w.Body)
	}
	want(t, reg, "after the refused add", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 0, registeredPrivate: 0,
	})
}

// A SAR that leaves the registration state as it is moves nothing: a
// REGISTRATION for a user who is registered at the same S-CSCF
// already, and a NO_ASSIGNMENT, which only asks for the profile.
func TestASARThatChangesNothingMovesNothing(t *testing.T) {
	h, reg := counting(t)
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)
	registered := map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 2, registeredPrivate: 1,
	}
	want(t, reg, "while registered", registered)

	sar(t, h, scscf, cx.AssignRegistration)
	want(t, reg, "after a second REGISTRATION", registered)

	sar(t, h, scscf, cx.NoAssignment)
	want(t, reg, "after a NO_ASSIGNMENT", registered)
}

// An S-CSCF that takes a user on for an unregistered service does not
// register the user, so the numbers of TS 32.409 4.2.1.2 stay at zero
// (4.2.1.2.2 counts that state, and the HSS does not serve it).
func TestAnUnregisteredUserCountsAsNoRegistration(t *testing.T) {
	h, reg := counting(t)
	if a := sar(t, h, scscf, cx.UnregisteredUser); a.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("SAA for UNREGISTERED_USER = %+v", a.Result)
	}
	want(t, reg, "while the user is unregistered", map[string]float64{
		subscriptions: 2, sipIdentities: 2, telIdentities: 1,
		registeredPublic: 0, registeredPrivate: 0,
	})
}

// assign sends the SAR of one subscriber to the handler, as the S-CSCF
// of that user would. Several goroutines may call it: it reports a
// failure without ending the test from the goroutine that found it.
func assign(t *testing.T, h *HSS, private, public string, typ cx.ServerAssignmentType) {
	t.Helper()
	a, err := cx.ParseServerAssignmentAnswer(h.Handle(cx.ServerAssignmentRequest{Header: hdr,
		UserName: private, PublicIdentities: []string{public}, ServerName: scscf, ServerAssignmentType: typ}.Message()))
	if err != nil {
		t.Error(err)
		return
	}
	if a.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("SAA for %s, assignment type %v = %+v", private, typ, a.Result)
	}
}

// Registrations and deregistrations of different users at once leave
// the gauges on what the store holds. The scrape reads the numbers
// rather than a registration writing them, so no registration can
// leave a gauge holding the number that an earlier one read. Run with
// -race as well.
func TestTheGaugesFollowConcurrentRegistrations(t *testing.T) {
	const users = 16
	subs := make([]Subscription, users)
	for i := range subs {
		private := fmt.Sprintf("00101000000%04d@%s", i, realm)
		subs[i] = Subscription{PrivateIdentity: private, Password: "test-password",
			PublicIdentities: []string{"sip:" + private, fmt.Sprintf("tel:+1202555%04d", i)}}
	}
	store, err := NewMemoryStore(subs...)
	if err != nil {
		t.Fatal(err)
	}
	reg := metrics.NewRegistry()
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm}, []uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
	h, err := New(Config{Realm: realm, VisitedNetworks: []string{realm}, Metrics: reg}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}

	// Prometheus scrapes while the registrations go on.
	stop := make(chan struct{})
	var scraping, registering sync.WaitGroup
	scraping.Add(1)
	go func() {
		defer scraping.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := reg.WriteText(io.Discard); err != nil {
					t.Error(err)
					return
				}
			}
		}
	}()
	for i, sub := range subs {
		registering.Add(1)
		go func() {
			defer registering.Done()
			assign(t, h, sub.PrivateIdentity, sub.PublicIdentities[0], cx.AssignRegistration)
			// Half of them deregister again, so that the gauges end on
			// neither all the users nor none of them.
			if i%2 == 1 {
				assign(t, h, sub.PrivateIdentity, sub.PublicIdentities[0], cx.UserDeregistration)
			}
		}()
	}
	registering.Wait()
	close(stop)
	scraping.Wait()

	// Every subscriber has a SIP URI and a tel URI, and half of them
	// are left registered.
	expected := Counts{Subscriptions: users, SIPPublicIdentities: users, TelPublicIdentities: users,
		RegisteredPublicIdentities: users, RegisteredPrivateIdentities: users / 2}
	held, _, err := store.Counts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if held != expected {
		t.Fatalf("the store holds %+v, want %+v", held, expected)
	}
	want(t, reg, "after the registrations", map[string]float64{
		subscriptions: float64(held.Subscriptions),
		sipIdentities: float64(held.SIPPublicIdentities), telIdentities: float64(held.TelPublicIdentities),
		registeredPublic:  float64(held.RegisteredPublicIdentities),
		registeredPrivate: float64(held.RegisteredPrivateIdentities),
	})
}

// TS 32.409 4.2.12.1 counts every request the HSS receives and 4.2.12.2
// every answer it sends, under the other value of the direction label
// from the request that a role sends the HSS and the answer that comes
// back to it (internal/diameter). The Cx of one registration is a MAR
// and a SAR, each answered once.
func TestTheHSSCountsTheRequestsItAnswers(t *testing.T) {
	h, reg := counting(t)
	mar(t, h, scscf)
	sar(t, h, scscf, cx.AssignRegistration)

	want(t, reg, "after the Cx of one registration", map[string]float64{
		`ims_diameter_requests_total{role="hss",direction="received",command="MAR"}`:                      1,
		`ims_diameter_answers_total{role="hss",direction="sent",command="MAR",result="DIAMETER_SUCCESS"}`: 1,
		`ims_diameter_requests_total{role="hss",direction="received",command="SAR"}`:                      1,
		`ims_diameter_answers_total{role="hss",direction="sent",command="SAR",result="DIAMETER_SUCCESS"}`: 1,
	})
}

// The result of each answer is in the result label, so that the answers
// of one command divide into the requests of that command whatever the
// HSS answered. A UAR for a subscriber the HSS does not hold is
// answered DIAMETER_ERROR_USER_UNKNOWN (TS 29.229 6.2.2.1).
func TestTheHSSCountsTheResultOfTheAnswersItSends(t *testing.T) {
	h, reg := counting(t)
	uar(t, h, "001010000000009@"+realm, "sip:001010000000009@"+realm, cx.Registration)

	want(t, reg, "after a UAR for a user the HSS does not hold", map[string]float64{
		`ims_diameter_requests_total{role="hss",direction="received",command="UAR"}`:                                 1,
		`ims_diameter_answers_total{role="hss",direction="sent",command="UAR",result="DIAMETER_ERROR_USER_UNKNOWN"}`: 1,
	})
}
