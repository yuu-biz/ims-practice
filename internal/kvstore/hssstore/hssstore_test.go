package hssstore_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/hssstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.HSS(t, func(*testing.T) (hss.Store, hss.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return hssstore.New(kv, cfg), hssstore.New(kv, cfg)
			})
		})
	}
}

const (
	impi = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	impu = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	tel  = "tel:+12025550101"
)

// counting is a store that counts what List asks of it, to show what a
// page costs.
type counting struct {
	kvstore.Store
	ranges int // MembersAfter, the range lookup of the sorted index
	gets   int // Get, one per subscriber of the page
}

func (c *counting) Get(ctx context.Context, key string) (kvstore.Item, error) {
	c.gets++
	return c.Store.Get(ctx, key)
}

func (c *counting) MembersAfter(ctx context.Context, index, after string, limit int) ([]string, error) {
	c.ranges++
	return c.Store.MembersAfter(ctx, index, after, limit)
}

// TestAPageOfTheListingCostsTheSameHoweverManySubscribers: List reads
// one range of the sorted index and then the subscribers of that page,
// so a page of a store that holds ten subscribers costs what a page of
// a store that holds a hundred costs. A List that gathered every
// private identity to sort it would read more of the store as the
// network grew.
func TestAPageOfTheListingCostsTheSameHoweverManySubscribers(t *testing.T) {
	ctx := context.Background()
	const page = 5
	cost := func(subscribers int) (int, int) {
		kv := &counting{Store: kvstore.NewMemoryStore(nil)}
		s := hssstore.New(kv, kvjson.Config{})
		for i := range subscribers {
			id := fmt.Sprintf("0010100000%05d@ims.mnc001.mcc001.3gppnetwork.org", i)
			if err := s.Add(ctx, hss.Subscription{PrivateIdentity: id, PublicIdentities: []string{"sip:" + id}, Password: "p"}); err != nil {
				t.Fatal(err)
			}
		}
		kv.ranges, kv.gets = 0, 0
		subs, next, err := s.List(ctx, "", page)
		if err != nil || len(subs) != page || next == "" {
			t.Fatalf("List of %d subscribers = %d subscribers, next %q, %v", subscribers, len(subs), next, err)
		}
		return kv.ranges, kv.gets
	}
	ranges, gets := cost(10)
	if ranges != 1 || gets != page {
		t.Errorf("a page of %d cost %d range lookups and %d reads; want 1 and %d", page, ranges, gets, page)
	}
	if moreRanges, moreGets := cost(100); moreRanges != ranges || moreGets != gets {
		t.Errorf("a page of a store with 100 subscribers cost %d range lookups and %d reads, one with 10 cost %d and %d",
			moreRanges, moreGets, ranges, gets)
	}
}

// TestAnAddThatStops stops the provisioning API of one instance after
// it has claimed the private identity and a public one. Until the
// provisional record runs out, the identities are taken; the index
// item it left is taken over by the next Add of another subscription.
func TestAnAddThatStops(t *testing.T) {
	ctx := context.Background()
	kv := kvstore.NewMemoryStore(nil)
	s := hssstore.New(kv, kvjson.Config{})
	stopped := hssstore.New(storetest.StopAfter(kv, 2), kvjson.Config{})
	if err := stopped.Add(ctx, hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"}); err == nil || errors.Is(err, hss.ErrIdentityTaken) {
		t.Fatalf("Add on a stopped instance = %v, want the failure of the store", err)
	}
	if _, ok, err := s.Subscriber(ctx, impi); err != nil || ok {
		t.Errorf("a provisional subscriber is visible: %v, %v", ok, err)
	}
	if _, ok, err := s.SubscriberOf(ctx, impu); err != nil || ok {
		t.Errorf("the index item of a provisional subscriber finds it: %v, %v", ok, err)
	}
	// The provisional record still holds impu.
	other := hss.Subscription{PrivateIdentity: "001010000000002@ims.mnc001.mcc001.3gppnetwork.org", PublicIdentities: []string{impu}, Password: "p"}
	if err := s.Add(ctx, other); !errors.Is(err, hss.ErrIdentityTaken) {
		t.Errorf("Add while the stopped Add holds the identity = %v, want ErrIdentityTaken", err)
	}
	// Once the provisional record is gone, its index item names nothing.
	kv.Delete(t.Context(), "hss:subscriber:"+impi)
	if err := s.Add(ctx, other); err != nil {
		t.Fatalf("Add after the provisional record went: %v", err)
	}
	if got, ok, err := s.SubscriberOf(ctx, impu); err != nil || !ok || got.PrivateIdentity != other.PrivateIdentity {
		t.Errorf("SubscriberOf = %+v, %v, %v", got, ok, err)
	}
}
