package pcscfstore_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file run two P-CSCF instances on one store, at one
// host and port as the nodes of the role answer at, and let an expiry of
// a reg event subscription run out. The expiry is handled once, by
// whichever instance claims it, and an expiry that the instance which
// claimed it never handled is handled by the other one once the claim
// runs out.

const (
	domain = "ims.mnc001.mcc001.3gppnetwork.org"
	impi   = "001010000000001@" + domain
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// toHome resolves every host to the S-CSCF of the home network, the only
// destination these tests send to.
type toHome struct{ addr netip.AddrPort }

func (r toHome) Locate(context.Context, string, int, sip.Transport) ([]sip.Destination, error) {
	return []sip.Destination{{Transport: sip.UDP, Addr: r.addr}}, nil
}

// instances are P-CSCF instances that share one store, with the S-CSCF
// that their SUBSCRIBEs go to.
type instances struct {
	kv  kvstore.Store
	cfg kvjson.Config
	// subscribes holds the SUBSCRIBEs that the S-CSCF was sent.
	subscribes chan *sip.Message
}

// startInstances runs n instances on one store, as nodes of one network
// have. lease is how long each of them holds a claimed expiry.
func startInstances(t *testing.T, n int, lease time.Duration) *instances {
	t.Helper()
	return startInstancesOn(t, kvstore.NewMemoryStore(nil), n, lease)
}

// startInstancesOn is startInstances on a store of the caller's
// choosing, such as one that fails.
func startInstancesOn(t *testing.T, kv kvstore.Store, n int, lease time.Duration) *instances {
	t.Helper()
	in := &instances{kv: kv, cfg: kvjson.Config{Prefix: storetest.Prefix()}, subscribes: make(chan *sip.Message, 8)}
	home := listen(t, func(st *sip.ServerTransaction) {
		if st.Request.Method == sip.SUBSCRIBE {
			in.subscribes <- st.Request
		}
		resp := sip.NewResponse(st.Request, 200, sip.NewTag())
		resp.Add("Expires", "3761")
		st.Respond(resp)
	})
	for i := range n {
		p := pcscf.New(pcscf.Config{
			Host: fmt.Sprintf("pcscf-%d.%s", i+1, domain), Port: 5060, VisitedNetworkID: domain,
			// The sweep comes round often, so that the test does not wait
			// a second for an expiry that is due.
			SweepInterval: 20 * time.Millisecond, ExpiryLease: lease,
		}, toHome{addr: home.LocalAddr()}, in.store(), nil, quiet())
		t.Cleanup(p.Close)
		p.SetStack(listen(t, p.OnRequest))
	}
	return in
}

// store is another Store on the same state, as each instance has.
func (in *instances) store() *pcscfstore.Store { return pcscfstore.New(in.kv, in.cfg) }

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

// subscription is a reg event subscription that a NOTIFY has confirmed,
// whose refresh fell due a second ago: what a user leaves behind when the
// instance that took that NOTIFY stops.
func subscriptionDueToRefresh() pcscf.RegSubscription {
	sub := regSubscription()
	sub.Dialog.RemoteTarget = "sip:scscf." + domain + ":5060"
	sub.Dialog.RouteSet = []string{"sip:scscf." + domain + ";lr"}
	sub.Requested, sub.Granted = 3761*time.Second, 3761*time.Second
	sub.RefreshAt = time.Now().Add(-time.Second)
	return sub
}

func create(t *testing.T, store *pcscfstore.Store, sub pcscf.RegSubscription) {
	t.Helper()
	if ok, err := store.CreateRegSubscription(context.Background(), sub); err != nil || !ok {
		t.Fatalf("CreateRegSubscription = %v, %v", ok, err)
	}
}

// createConfirmed puts sub in the store the way the P-CSCF does: the
// create leaves the subscription waiting for its first NOTIFY, and the
// update that follows is what that NOTIFY does, which is what sets the
// time of the next refresh.
func createConfirmed(t *testing.T, store *pcscfstore.Store, sub pcscf.RegSubscription) {
	t.Helper()
	waiting := sub
	waiting.RefreshAt, waiting.NotifyDeadline = time.Time{}, time.Now().Add(time.Minute)
	create(t, store, waiting)
	if _, ok, err := store.UpdateRegSubscription(context.Background(), sub.PrivateIdentity,
		func(s *pcscf.RegSubscription) bool {
			s.NotifyDeadline, s.RefreshAt = sub.NotifyDeadline, sub.RefreshAt
			return true
		}); err != nil || !ok {
		t.Fatalf("the NOTIFY of the subscription = %v, %v", ok, err)
	}
}

// watch is how long a test watches for a SUBSCRIBE that must not go
// out. Both instances sweep every 20 ms, so it leaves each of them
// several rounds in which it could repeat the work of the other.
// TestTwoInstancesRefreshASubscriptionOnce sees, within as long, the
// next refresh go out once it falls due.
const watch = 200 * time.Millisecond

// refreshedWithin tells whether the S-CSCF was sent a SUBSCRIBE within d.
func refreshedWithin(in *instances, d time.Duration) bool {
	select {
	case <-in.subscribes:
		return true
	case <-time.After(d):
		return false
	}
}

// waitForRefresh waits until the S-CSCF has been sent a SUBSCRIBE, and
// returns how long that took.
func waitForRefresh(t *testing.T, in *instances) time.Duration {
	t.Helper()
	began := time.Now()
	select {
	case <-in.subscribes:
		return time.Since(began)
	case <-time.After(3 * time.Second):
		t.Fatal("the S-CSCF was sent no refresh of the subscription whose time had come")
		return 0
	}
}

// TestTwoInstancesRefreshASubscriptionOnce: the refresh of a
// subscription falls due while two instances share the store. One
// SUBSCRIBE goes out, within the dialog, whichever instance claims the
// expiry (TS 24.229 5.2.3).
func TestTwoInstancesRefreshASubscriptionOnce(t *testing.T) {
	in := startInstances(t, 2, time.Minute)
	sub := subscriptionDueToRefresh()
	createConfirmed(t, in.store(), sub)
	waitForRefresh(t, in)
	if refreshedWithin(in, watch) {
		t.Error("a refresh beyond the first went out")
	}
	// The refresh that went out leaves no refresh due and waits for its
	// NOTIFY under Timer N.
	stored, ok, err := in.store().RegSubscription(context.Background(), impi)
	if err != nil || !ok {
		t.Fatalf("the subscription = %v, %v", ok, err)
	}
	if !stored.RefreshAt.IsZero() {
		t.Errorf("the refresh is still due at %v, want none until the NOTIFY", stored.RefreshAt)
	}
	if stored.NotifyDeadline.IsZero() {
		t.Error("the refresh started no Timer N")
	}
	if stored.Dialog.LocalSeq != sub.Dialog.LocalSeq+1 {
		t.Errorf("the CSeq of the dialog = %d, want one above the %d of the subscription",
			stored.Dialog.LocalSeq, sub.Dialog.LocalSeq)
	}

	// A NOTIFY that sets the next refresh in the past makes it due at
	// once, and the watch that saw no second refresh sees this one go
	// out.
	if _, ok, err := in.store().UpdateRegSubscription(context.Background(), impi, func(s *pcscf.RegSubscription) bool {
		s.NotifyDeadline, s.RefreshAt = time.Time{}, time.Now().Add(-time.Second)
		return true
	}); err != nil || !ok {
		t.Fatalf("the NOTIFY of the refresh = %v, %v", ok, err)
	}
	if !refreshedWithin(in, watch) {
		t.Errorf("no refresh went out within %v of the time it fell due", watch)
	}
}

// TestAnotherInstanceTakesOverARefreshThatWasNotSent: the instance that
// claimed the refresh stops before it has sent it, so it never marks the
// expiry done. Once its claim runs out the other instance claims the
// expiry and sends the refresh.
func TestAnotherInstanceTakesOverARefreshThatWasNotSent(t *testing.T) {
	const lease = 300 * time.Millisecond
	in := startInstances(t, 1, lease)
	// The store of the instance that stopped. It claims the expiry, as
	// its sweep would, and then neither refreshes nor calls Done.
	stopped := in.store()
	createConfirmed(t, stopped, subscriptionDueToRefresh())
	claimed, err := stopped.ClaimDue(context.Background(), pcscf.RegEventRefreshExpiry, time.Now(), lease, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("the instance that stops claimed %v, %v; want the refresh of the subscription", claimed, err)
	}

	took := waitForRefresh(t, in)
	if took < lease {
		t.Errorf("the other instance refreshed after %v, want not before the claim ran out at %v", took, lease)
	}
}

// TestARefreshThatTheStoreWouldNotTakeComesBack: the store fails while
// the instance is taking the CSeq of the refresh, so no SUBSCRIBE goes
// out and the instance marks nothing done. The expiry keeps its place in
// the index, and a round after the claim has run out sends the refresh.
func TestARefreshThatTheStoreWouldNotTakeComesBack(t *testing.T) {
	const lease = 300 * time.Millisecond
	kv := &failingUpdate{Store: kvstore.NewMemoryStore(nil)}
	in := startInstancesOn(t, kv, 1, lease)
	createConfirmed(t, in.store(), subscriptionDueToRefresh())
	// Armed only now: the write of the NOTIFY above is a change of the
	// subscription too, and the test is about the one the refresh makes.
	kv.armed.Store(true)

	took := waitForRefresh(t, in)
	if !kv.failed.Load() {
		t.Error("the store took the CSeq at the first try; the test proved nothing")
	}
	if took < lease {
		t.Errorf("the refresh went out after %v, want not before the claim it kept ran out at %v", took, lease)
	}
	if refreshedWithin(in, watch) {
		t.Error("a refresh beyond the first went out")
	}
}

// TestTimerNEndsTheSubscriptionOfAStoppedInstance: a SUBSCRIBE whose
// instance stopped leaves a subscription that no NOTIFY confirmed. An
// instance claims its Timer N and forgets the subscription, so that the
// user's next REGISTER subscribes again (RFC 6665 4.1.2.4).
func TestTimerNEndsTheSubscriptionOfAStoppedInstance(t *testing.T) {
	in := startInstances(t, 2, time.Minute)
	sub := regSubscription()
	sub.Dialog.ID.RemoteTag = ""
	sub.NotifyDeadline = time.Now().Add(-time.Second)
	create(t, in.store(), sub)

	ctx := context.Background()
	for range 150 {
		if _, ok, err := in.store().RegSubscription(ctx, impi); err == nil && !ok {
			if refreshedWithin(in, watch) {
				t.Error("Timer N sent a SUBSCRIBE, want none")
			}
			if got, err := in.store().ClaimDue(ctx, pcscf.RegEventNotifyExpiry, time.Now(), time.Minute, 10); err != nil || len(got) != 0 {
				t.Errorf("Timer N is still in the store: %+v, %v; want it marked done", got, err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Timer N did not end the subscription that got no NOTIFY")
}

// failingUpdate is a store that fails the first change of a
// subscription, as one that is briefly out of reach does, and works from
// then on.
type failingUpdate struct {
	kvstore.Store
	armed  atomic.Bool
	failed atomic.Bool
}

func (f *failingUpdate) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if f.armed.Load() && strings.Contains(key, ":reg-subscription:") && version != "" && f.failed.CompareAndSwap(false, true) {
		return "", errors.New("instances_test: the store is out of reach")
	}
	return f.Store.CompareAndSwap(ctx, key, version, value, ttl)
}
