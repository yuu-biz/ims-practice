package ipsmgwstore_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// The tests in this file run two IP-SM-GW instances on one store, as a
// network with two IP-SM-GW nodes does, and let the TR1N of a delivery
// run out. The SC hears the outcome once, from whichever instance claims
// the delivery, and a delivery that the instance which claimed it never
// ended is ended by another one once the claim runs out.
//
// The instances have no SIP stack: TR1N sends no request, it takes the
// delivery and tells the SC.

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// recordingSC is the SC of both instances: it records the outcome of
// every delivery it is told of.
type recordingSC struct {
	mu       sync.Mutex
	outcomes []ipsmgw.Outcome
}

func (c *recordingSC) Submit(context.Context, sms.Address, sms.Submit) (sms.SubmitReport, error) {
	return sms.SubmitReport{}, errors.New("no short message is submitted in this test")
}

func (c *recordingSC) Report(_ context.Context, _ string, outcome ipsmgw.Outcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.outcomes = append(c.outcomes, outcome)
}

func (c *recordingSC) reported() []ipsmgw.Outcome {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]ipsmgw.Outcome(nil), c.outcomes...)
}

// instances are IP-SM-GW instances that share one store, with the SC
// that they report to.
type instances struct {
	sc  *recordingSC
	kv  kvstore.Store
	cfg kvjson.Config
}

// startInstances runs n instances on one store, each with its own lock
// table, as nodes of one network have. Their locks are in the store they
// share. lease is how long each of them holds a claimed delivery.
func startInstances(t *testing.T, n int, lease time.Duration) *instances {
	t.Helper()
	return startInstancesOn(t, kvstore.NewMemoryStore(nil), n, lease)
}

// startInstancesOn is startInstances on a store of the caller's
// choosing, such as one that fails.
func startInstancesOn(t *testing.T, kv kvstore.Store, n int, lease time.Duration) *instances {
	t.Helper()
	in := &instances{sc: &recordingSC{}, kv: kv, cfg: kvjson.Config{Prefix: storetest.Prefix()}}
	for i := range n {
		holder := fmt.Sprintf("ipsmgw-%d.%s", i+1, domain)
		g := ipsmgw.New(ipsmgw.Config{
			URI: "sip:ipsmgw." + domain + ":5060", HomeDomain: domain, Number: "12025550199",
			ServiceCentre: sms.Address{Digits: "12025550100"},
			// The sweep comes round often, so that the test does not wait
			// a second for a delivery whose TR1N is up.
			SweepInterval: 20 * time.Millisecond, ExpiryLease: lease,
			UserLocks: kvlock.New(kv, kvlock.Config{Prefix: in.cfg.Prefix, Role: "ipsmgw", Holder: holder}),
		}, in.sc, in.store(), nil, nil, quiet())
		t.Cleanup(g.Close)
	}
	return in
}

// store is another Store on the same state, as each instance has.
func (in *instances) store() *ipsmgwstore.Store { return ipsmgwstore.New(in.kv, in.cfg) }

// expire puts in store a delivery whose TR1N is already up: what a short
// message leaves behind when no delivery report comes.
func (in *instances) expire(t *testing.T, store *ipsmgwstore.Store) ipsmgw.PendingDelivery {
	t.Helper()
	now := time.Now()
	d := ipsmgw.PendingDelivery{CallID: "msg-1", Reference: "ref-1", Recipient: recipient, MessageReference: 3,
		ReportDeadline: now.Add(-time.Second), Expires: now.Add(time.Hour)}
	if err := store.PutDelivery(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	return d
}

// waitForReport waits until the SC has been told an outcome, and returns
// how long that took.
func waitForReport(t *testing.T, in *instances) time.Duration {
	t.Helper()
	began := time.Now()
	for range 300 {
		if len(in.sc.reported()) > 0 {
			return time.Since(began)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the SC was told no outcome for the delivery whose TR1N ran out")
	return 0
}

// TestTwoInstancesEndADeliveryOnce: the TR1N of a delivery runs out
// while two instances share the store. The SC hears one system failure,
// whichever instance claims the delivery (TS 24.011 6.2.2.2).
func TestTwoInstancesEndADeliveryOnce(t *testing.T) {
	in := startInstances(t, 2, time.Minute)
	d := in.expire(t, in.store())
	waitForReport(t, in)
	// Both instances sweep every 20 ms, so this leaves each of them
	// several rounds in which it could repeat the work of the other.
	time.Sleep(200 * time.Millisecond)

	if got := in.sc.reported(); len(got) != 1 || got[0] != ipsmgw.SystemFailure {
		t.Errorf("the SC was told %v, want one system failure", got)
	}
	ctx := context.Background()
	if _, ok, err := in.store().Delivery(ctx, d.CallID); err != nil || ok {
		t.Errorf("the delivery is still in the store: %v, %v", ok, err)
	}
	if got, err := in.store().ClaimDue(ctx, time.Now(), time.Minute, 10); err != nil || len(got) != 0 {
		t.Errorf("TR1N is still in the store: %+v, %v; want it marked done", got, err)
	}
}

// TestAnotherInstanceEndsADeliveryThatWasNotEnded: the instance that
// claimed a TR1N stops before it has ended the delivery, so it never
// marks it done. Once its claim runs out the other instance claims the
// delivery and tells the SC.
func TestAnotherInstanceEndsADeliveryThatWasNotEnded(t *testing.T) {
	const lease = 300 * time.Millisecond
	in := startInstances(t, 1, lease)
	// The store of the instance that stopped. It claims the delivery, as
	// its sweep would, and then neither ends it nor calls Done.
	stopped := in.store()
	in.expire(t, stopped)
	claimed, err := stopped.ClaimDue(context.Background(), time.Now(), lease, 10)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("the instance that stops claimed %v, %v; want the TR1N of the delivery", claimed, err)
	}

	took := waitForReport(t, in)
	if took < lease {
		t.Errorf("the other instance ended the delivery after %v, want not before the claim ran out at %v", took, lease)
	}
	if got := in.sc.reported(); len(got) != 1 || got[0] != ipsmgw.SystemFailure {
		t.Errorf("the SC was told %v, want one system failure", got)
	}
}

// TestADeliveryThatTheStoreWouldNotGiveUpComesBack: the store fails
// while the instance is taking the delivery, so the instance marks
// nothing done and the delivery keeps its place in the index. A round
// after the claim has run out ends it, and the SC hears the outcome
// once.
func TestADeliveryThatTheStoreWouldNotGiveUpComesBack(t *testing.T) {
	const lease = 300 * time.Millisecond
	kv := &failingTake{Store: kvstore.NewMemoryStore(nil)}
	in := startInstancesOn(t, kv, 1, lease)
	in.expire(t, in.store())

	took := waitForReport(t, in)
	if !kv.failed.Load() {
		t.Error("the store gave up the delivery at the first try; the test proved nothing")
	}
	if took < lease {
		t.Errorf("the delivery ended after %v, want not before the claim it kept ran out at %v", took, lease)
	}
	time.Sleep(200 * time.Millisecond)
	if got := in.sc.reported(); len(got) != 1 || got[0] != ipsmgw.SystemFailure {
		t.Errorf("the SC was told %v, want one system failure", got)
	}
}

// failingTake is a store that fails the first removal of a delivery, as
// one that is briefly out of reach does, and works from then on.
type failingTake struct {
	kvstore.Store
	failed atomic.Bool
}

func (f *failingTake) CompareAndDelete(ctx context.Context, key, version string) error {
	if strings.Contains(key, ":delivery:") && f.failed.CompareAndSwap(false, true) {
		return errors.New("instances_test: the store is out of reach")
	}
	return f.Store.CompareAndDelete(ctx, key, version)
}
