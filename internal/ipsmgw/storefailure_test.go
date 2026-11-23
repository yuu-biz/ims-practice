package ipsmgw_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file tries the IP-SM-GW on a store outside the process that
// fails. The IP-SM-GW answers a request whose state it cannot read or
// write with 500 (RFC 3261 21.5.1), and tells the SC of a delivery that
// it cannot start.

// reachableStore is a store outside the process that the IP-SM-GW
// reaches until down is set, and that then fails every operation, as a
// store that the IP-SM-GW can no longer reach does.
type reachableStore struct {
	kvstore.Store
	down atomic.Bool
}

var errUnreachable = errors.New("the store cannot be reached")

func (s *reachableStore) Get(ctx context.Context, key string) (kvstore.Item, error) {
	if s.down.Load() {
		return kvstore.Item{}, errUnreachable
	}
	return s.Store.Get(ctx, key)
}

func (s *reachableStore) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	if s.down.Load() {
		return "", errUnreachable
	}
	return s.Store.Put(ctx, key, value, ttl)
}

func (s *reachableStore) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if s.down.Load() {
		return "", errUnreachable
	}
	return s.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (s *reachableStore) Delete(ctx context.Context, key string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.Delete(ctx, key)
}

func (s *reachableStore) CompareAndDelete(ctx context.Context, key, version string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.CompareAndDelete(ctx, key, version)
}

func (s *reachableStore) Schedule(ctx context.Context, index, member string, at time.Time) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.Schedule(ctx, index, member, at)
}

func (s *reachableStore) Unschedule(ctx context.Context, index, member string) error {
	if s.down.Load() {
		return errUnreachable
	}
	return s.Store.Unschedule(ctx, index, member)
}

// newHarnessOnAStore is newHarness for an IP-SM-GW whose state is in a
// store outside the process, which the test takes down when it wants.
func newHarnessOnAStore(t *testing.T) (*harness, *reachableStore) {
	t.Helper()
	kv := &reachableStore{Store: kvstore.NewMemoryStore(nil)}
	return newHarness(t, ipsmgwstore.New(kv, kvjson.Config{}), time.Minute, answerOK), kv
}

// TestDeliveryReportWhenTheStoreFails: the IP-SM-GW cannot tell whether
// a delivery report answers one of its deliveries, so it answers 500,
// not 488. The UE may send the report again, and once the store is back
// the report ends the delivery.
func TestDeliveryReportWhenTheStoreFails(t *testing.T) {
	h, kv := newHarnessOnAStore(t)
	mt, data := h.delivered("ref-1")
	report := sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference}

	kv.down.Store(true)
	if resp := h.send(deliveryReport(t, mt, report)); resp.StatusCode != 500 {
		t.Fatalf("delivery report with the store down answered %d, want 500", resp.StatusCode)
	}
	select {
	case r := <-h.sc.reports:
		t.Fatalf("SC heard %+v while the store was down", r)
	case <-time.After(50 * time.Millisecond):
	}

	kv.down.Store(false)
	if resp := h.send(deliveryReport(t, mt, report)); resp.StatusCode != 202 {
		t.Fatalf("delivery report sent again answered %d, want 202", resp.StatusCode)
	}
	if got := h.nextReport(); got != (reported{"ref-1", ipsmgw.Delivered}) {
		t.Errorf("SC heard %+v", got)
	}
}

// TestDeliveryWhenTheStoreFails: without a store, the IP-SM-GW could not
// match the delivery report with the delivery, so it sends no MESSAGE
// and tells the SC of a system failure at once.
func TestDeliveryWhenTheStoreFails(t *testing.T) {
	h, kv := newHarnessOnAStore(t)
	kv.down.Store(true)
	h.g.Deliver(ipsmgw.Delivery{Reference: "ref-1", Recipient: bobTel, TPDU: deliver})
	if got := h.nextReport(); got != (reported{"ref-1", ipsmgw.SystemFailure}) {
		t.Errorf("SC heard %+v", got)
	}
	select {
	case m := <-h.received:
		t.Errorf("%s reached the entry point", m.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNotifyWhenTheStoreFails: a NOTIFY whose subscription the IP-SM-GW
// cannot read gets 500, which does not end the subscription, and the
// HSS hears nothing. The next NOTIFY carries the full state again, and
// once the store is back the HSS hears of it.
func TestNotifyWhenTheStoreFails(t *testing.T) {
	h, kv := newHarnessOnAStore(t)
	if resp := h.thirdPartyRegister(3600); resp.StatusCode != 200 {
		t.Fatalf("third-party REGISTER answered %d", resp.StatusCode)
	}
	subscribe := h.subscribed()
	doc := regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")

	kv.down.Store(true)
	if resp := h.send(notify(subscribe, 1, "active;expires=3761", doc)); resp.StatusCode != 500 {
		t.Fatalf("NOTIFY with the store down answered %d, want 500", resp.StatusCode)
	}
	h.noUpdate()

	kv.down.Store(false)
	if resp := h.send(notify(subscribe, 2, "active;expires=3700", doc)); resp.StatusCode != 200 {
		t.Fatalf("next NOTIFY answered %d, want 200", resp.StatusCode)
	}
	if got := h.nextUpdate(); got != registered {
		t.Errorf("PUR = %+v, want %+v", got, registered)
	}
}

// reachableLocks is the lock per user in a store outside the process:
// it hands out locks until down is set, and then none, as the lock in a
// store that the IP-SM-GW can no longer reach does.
type reachableLocks struct {
	ims.UserLocks
	down atomic.Bool
}

func (l *reachableLocks) Lock(ctx context.Context, user string) (func(), error) {
	if l.down.Load() {
		return nil, errUnreachable
	}
	return l.UserLocks.Lock(ctx, user)
}

// TestNotifyWhenTheLockFails: a NOTIFY whose user the IP-SM-GW cannot
// lock gets 500 too. Another instance may be handling the same
// subscription, so this one does not read and write it without the
// lock, and the HSS hears nothing. Once the lock is there again, the
// next NOTIFY carries the full state and the HSS hears of it.
func TestNotifyWhenTheLockFails(t *testing.T) {
	locks := &reachableLocks{}
	h := newHarnessWith(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK, ipsmgw.Config{UserLocks: locks})
	if resp := h.thirdPartyRegister(3600); resp.StatusCode != 200 {
		t.Fatalf("third-party REGISTER answered %d", resp.StatusCode)
	}
	subscribe := h.subscribed()
	doc := regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")

	locks.down.Store(true)
	if resp := h.send(notify(subscribe, 1, "active;expires=3761", doc)); resp.StatusCode != 500 {
		t.Fatalf("NOTIFY whose user cannot be locked answered %d, want 500", resp.StatusCode)
	}
	h.noUpdate()

	locks.down.Store(false)
	if resp := h.send(notify(subscribe, 2, "active;expires=3700", doc)); resp.StatusCode != 200 {
		t.Fatalf("next NOTIFY answered %d, want 200", resp.StatusCode)
	}
	if got := h.nextUpdate(); got != registered {
		t.Errorf("PUR = %+v, want %+v", got, registered)
	}
}
