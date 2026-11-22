package scscfstore_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/dict"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file run two S-CSCF instances on one store, as a
// network with two S-CSCF nodes does, and let an expiry run out. The
// expiry is handled once, by whichever instance claims it, and an expiry
// that the instance which claimed it never handled is handled by the
// other one once the claim runs out.

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

func init() {
	if err := diameter.LoadDictionary(); err != nil {
		panic(err)
	}
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// recordingHSS is the HSS of both instances: it answers every Cx request
// with success and counts the Server-Assignment-Types it was sent. Like
// the HSS of a network it is reached over the wire, so a request goes
// through the wire format.
type recordingHSS struct {
	identity diameter.Identity
	mu       sync.Mutex
	sar      []cx.ServerAssignmentType
	// held, when set, keeps the requests of heldUser waiting until it
	// closes or until the caller's context ends, as an HSS that does not
	// answer does.
	held     <-chan struct{}
	heldUser string
}

// hold leaves the requests of user unanswered until until closes.
func (h *recordingHSS) hold(user string, until <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held, h.heldUser = until, user
}

func (h *recordingHSS) Request(ctx context.Context, m *diam.Message) (*diam.Message, error) {
	b, err := m.Serialize()
	if err != nil {
		return nil, err
	}
	request, err := diam.ReadMessage(bytes.NewReader(b), dict.Default)
	if err != nil {
		return nil, err
	}
	user := ""
	if r, err := cx.ParseServerAssignmentRequest(request); err == nil && request.Header.CommandCode == cx.CommandServerAssignment {
		user = r.UserName
		h.mu.Lock()
		h.sar = append(h.sar, r.ServerAssignmentType)
		h.mu.Unlock()
	}
	h.mu.Lock()
	held := h.held
	if user != h.heldUser {
		held = nil
	}
	h.mu.Unlock()
	if held != nil {
		select {
		case <-held:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterSuccess)}.Message(request, h.identity), nil
}

func (h *recordingHSS) assignments() []cx.ServerAssignmentType {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]cx.ServerAssignmentType(nil), h.sar...)
}

// toSubscriber resolves every host to the subscriber of the reg event,
// the only destination these tests send to.
type toSubscriber struct{ addr netip.AddrPort }

func (r toSubscriber) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return []sip.Destination{{Transport: sip.UDP, Addr: r.addr}}, nil
}

func (r toSubscriber) ENUM(context.Context, string, string) (string, error) {
	return "", errors.New("no ENUM in this test")
}

// instances are S-CSCF instances that share one store, with the HSS that
// answers them and the subscriber that their NOTIFYs go to.
type instances struct {
	hss *recordingHSS
	kv  *kvstore.MemoryStore
	cfg kvjson.Config
	// notify holds the NOTIFYs that the subscriber was sent, and
	// subscriber is where it takes them.
	notify     chan *sip.Message
	subscriber netip.AddrPort
}

// startInstances runs n instances on one store, each with its own
// Diameter identity, SIP stack and lock table, as nodes of one network
// have. Their locks are in the store they share, so one user is handled
// by one of them at a time. lease is how long each of them holds a
// claimed expiry.
func startInstances(t *testing.T, n int, lease time.Duration) *instances {
	t.Helper()
	in := &instances{
		hss:    &recordingHSS{identity: diameter.Identity{OriginHost: "hss." + domain, OriginRealm: domain}},
		kv:     kvstore.NewMemoryStore(nil),
		cfg:    kvjson.Config{Prefix: storetest.Prefix()},
		notify: make(chan *sip.Message, 8),
	}
	in.subscriber = listen(t, func(st *sip.ServerTransaction) {
		in.notify <- st.Request
		st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
	}).LocalAddr()
	for i := range n {
		host := fmt.Sprintf("scscf-%d.%s", i+1, domain)
		node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: domain},
			[]uint32{diameter.ApplicationIDCx}, diameter.Timers{}, quiet())
		s := scscf.New(scscf.Config{
			ServerName: "sip:" + host + ":5060", Host: host, Port: 5060, HomeDomain: domain,
			// The sweep comes round often, so that the test does not wait
			// a second for an expiry that is due.
			SweepInterval: 20 * time.Millisecond, ExpiryLease: lease, UserLocks: in.locks(host),
		}, in.hss, node, in.store(), toSubscriber{addr: in.subscriber}, quiet())
		t.Cleanup(s.Close)
		s.SetStack(listen(t, s.OnRequest))
	}
	return in
}

// store is another Store on the same state, as each instance has.
func (in *instances) store() *scscfstore.Store { return scscfstore.New(in.kv, in.cfg) }

// locks are the locks of the users in the store they share, as held by
// the instance named holder: the lock of a user is the same lock for
// every instance.
func (in *instances) locks(holder string) *kvlock.Locker {
	return kvlock.New(in.kv, kvlock.Config{Prefix: in.cfg.Prefix, Role: "scscf", Holder: holder})
}

func listen(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	transport, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	s, err := sip.NewStack(transport, sip.Config{SentBy: transport.LocalAddr().String(), OnRequest: onRequest}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// expire puts in store a registration whose time is up and a
// subscription to its reg event: what a user leaves behind when the UE
// stops refreshing.
func (in *instances) expire(t *testing.T, store *scscfstore.Store) {
	t.Helper()
	ctx := context.Background()
	reg := registration(time.Now().Add(-time.Second))
	reg.ContactID, reg.ContactEvent = "c1", reginfo.Registered
	must(t, store.PutRegistration(ctx, reg))

	var sub scscf.Subscription
	sub.PrivateIdentity, sub.ICID, sub.Version = impi, "icid-1", 1
	sub.Dialog = sip.Dialog{ID: sip.DialogID{CallID: "sub-1", LocalTag: "l1", RemoteTag: "r1"},
		State: sip.DialogConfirmed, LocalSeq: 1, RemoteSeq: 1, LocalURI: impu, RemoteURI: impu,
		RemoteTarget: "sip:ue1@" + in.subscriber.String()}
	sub.Event, sub.Contact = "reg", "sip:scscf."+domain
	sub.Expires, sub.State = time.Now().Add(time.Hour), sip.SubscriptionActive
	must(t, store.PutSubscription(ctx, sub))
}

// waitForSAR waits until the HSS has been sent a SAR, and returns how
// long that took.
func waitForSAR(t *testing.T, in *instances) time.Duration {
	t.Helper()
	began := time.Now()
	for range 300 {
		if len(in.hss.assignments()) > 0 {
			return time.Since(began)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the HSS was sent no SAR for the registration that expired")
	return 0
}

// TestTwoInstancesHandleARegistrationExpiryOnce: the registration of a
// user who stopped refreshing expires while two instances share the
// store. One SAR of TIMEOUT_DEREGISTRATION goes to the HSS and one
// NOTIFY to the subscriber, whichever instance claims the expiry (TS
// 29.229 6.3.15, TS 24.229 5.4.2.1.2).
func TestTwoInstancesHandleARegistrationExpiryOnce(t *testing.T) {
	in := startInstances(t, 2, time.Minute)
	in.expire(t, in.store())
	waitForSAR(t, in)
	// Both instances sweep every 20 ms, so this leaves each of them
	// several rounds in which it could repeat the work of the other.
	time.Sleep(200 * time.Millisecond)

	if got := in.hss.assignments(); len(got) != 1 || got[0] != cx.TimeoutDeregistration {
		t.Errorf("SARs = %v, want one TIMEOUT_DEREGISTRATION", got)
	}
	if got := len(in.notify); got != 1 {
		t.Errorf("%d NOTIFYs, want 1", got)
	}
	notify := <-in.notify
	doc, err := reginfo.Parse(notify.Body)
	if err != nil {
		t.Fatalf("the NOTIFY carries no reginfo document: %v", err)
	}
	// One registration element per public identity of the user.
	if len(doc.Registrations) != 2 {
		t.Fatalf("the NOTIFY holds %d registrations, want one per public identity", len(doc.Registrations))
	}
	for _, r := range doc.Registrations {
		if r.State != reginfo.Terminated || len(r.Contacts) != 1 || r.Contacts[0].Event != reginfo.Expired {
			t.Errorf("the NOTIFY says %+v, want it terminated and its contact expired", r)
		}
	}
	ctx := context.Background()
	if _, ok, err := in.store().Registration(ctx, impi); err != nil || ok {
		t.Errorf("the registration is still in the store: %v, %v", ok, err)
	}
	if subs, err := in.store().SubscriptionsOf(ctx, impi); err != nil || len(subs) != 0 {
		t.Errorf("subscriptions after the expiry = %v, %v; want none", subs, err)
	}
}

// TestAnotherInstanceTakesOverAnExpiryThatWasNotHandled: the instance
// that claimed an expiry stops before it has handled it, so it never
// marks it done. Once its claim runs out the other instance claims the
// expiry and handles it.
func TestAnotherInstanceTakesOverAnExpiryThatWasNotHandled(t *testing.T) {
	const lease = 300 * time.Millisecond
	in := startInstances(t, 1, lease)
	// The store of the instance that stopped. It claims the expiry, as
	// its sweep would, and then calls neither the handler nor Done.
	stopped := in.store()
	in.expire(t, stopped)
	claimed, err := stopped.ClaimDue(context.Background(), scscf.RegistrationExpiry, time.Now(), lease, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("the instance that stops claimed %v, %v; want the expiry of the registration", claimed, err)
	}

	took := waitForSAR(t, in)
	if took < lease {
		t.Errorf("the other instance handled the expiry after %v, want not before the claim ran out at %v", took, lease)
	}
	if got := in.hss.assignments(); len(got) != 1 || got[0] != cx.TimeoutDeregistration {
		t.Errorf("SARs = %v, want one TIMEOUT_DEREGISTRATION", got)
	}
}

// TestAnExpiryWhoseClaimRunsOutWhileItIsHandled: the HSS leaves the SAR
// of the expiry unanswered until the claim runs out, so the second
// instance claims the same expiry while the first is still at work. The
// user is deregistered once all the same: the first instance had already
// removed the registration, and the second finds nothing to do.
func TestAnExpiryWhoseClaimRunsOutWhileItIsHandled(t *testing.T) {
	const lease = 300 * time.Millisecond
	in := startInstances(t, 2, lease)
	answered := make(chan struct{})
	defer close(answered)
	in.hss.hold(impi, answered)
	in.expire(t, in.store())

	waitForSAR(t, in)
	// Long enough for the claim to run out and for the other instance to
	// claim the expiry and handle it, several rounds over.
	time.Sleep(4 * lease)
	if got := in.hss.assignments(); len(got) != 1 || got[0] != cx.TimeoutDeregistration {
		t.Errorf("SARs = %v, want one TIMEOUT_DEREGISTRATION", got)
	}
	if got := len(in.notify); got != 1 {
		t.Errorf("%d NOTIFYs, want 1", got)
	}
	ctx := context.Background()
	if _, ok, err := in.store().Registration(ctx, impi); err != nil || ok {
		t.Errorf("the registration is still in the store: %v, %v", ok, err)
	}
	if got, err := in.store().ClaimDue(ctx, scscf.RegistrationExpiry, time.Now(), lease, 10); err != nil || len(got) != 0 {
		t.Errorf("the expiry is still in the store: %+v, %v; want it marked done", got, err)
	}
}

// TestARegistrationRefreshedBeforeItsExpiryIsHandled: the UE refreshes
// while an instance holds the claim of the old expiry and waits for the
// lock of the user. That instance then reads the registration it is to
// end, finds the time it carries is no longer past, and leaves the user
// registered.
func TestARegistrationRefreshedBeforeItsExpiryIsHandled(t *testing.T) {
	const lease = 2 * time.Second
	in := startInstances(t, 1, lease)
	ctx := context.Background()
	// The test holds the lock of the user, as a third instance would
	// while it serves a REGISTER of that user.
	unlock, err := in.locks("test").Lock(ctx, impi)
	if err != nil {
		t.Fatal(err)
	}
	in.expire(t, in.store())
	// Several rounds, by when the instance has claimed the expiry and
	// waits for the lock.
	time.Sleep(100 * time.Millisecond)
	refreshed := registration(time.Now().Add(time.Hour))
	must(t, in.store().PutRegistration(ctx, refreshed))
	unlock()

	time.Sleep(200 * time.Millisecond)
	if got := in.hss.assignments(); len(got) != 0 {
		t.Errorf("SARs = %v, want none for a registration that was refreshed", got)
	}
	if got := len(in.notify); got != 0 {
		t.Errorf("%d NOTIFYs, want none", got)
	}
	if reg, ok, err := in.store().Registration(ctx, impi); err != nil || !ok || !reg.Expires.Equal(refreshed.Expires) {
		t.Errorf("the registration = %+v, %v, %v; want the refreshed one", reg, ok, err)
	}
}
