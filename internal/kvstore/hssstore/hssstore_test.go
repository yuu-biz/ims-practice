package hssstore_test

import (
	"errors"
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

// TestAnAddThatStops stops the provisioning API of one instance after
// it has claimed the private identity and a public one. Until the
// provisional record runs out, the identities are taken; the index
// item it left is taken over by the next Add of another subscription.
func TestAnAddThatStops(t *testing.T) {
	kv := kvstore.NewMemoryStore(nil)
	s := hssstore.New(kv, kvjson.Config{})
	stopped := hssstore.New(storetest.StopAfter(kv, 2), kvjson.Config{})
	if err := stopped.Add(hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Password: "p"}); err == nil {
		t.Fatal("Add on a stopped instance succeeded")
	}
	if _, ok := s.Subscriber(impi); ok {
		t.Error("a provisional subscriber is visible")
	}
	if _, ok := s.SubscriberOf(impu); ok {
		t.Error("the index item of a provisional subscriber finds it")
	}
	// The provisional record still holds impu.
	other := hss.Subscription{PrivateIdentity: "001010000000002@ims.mnc001.mcc001.3gppnetwork.org", PublicIdentities: []string{impu}, Password: "p"}
	if err := s.Add(other); !errors.Is(err, hss.ErrIdentityTaken) {
		t.Errorf("Add while the stopped Add holds the identity = %v, want ErrIdentityTaken", err)
	}
	// Once the provisional record is gone, its index item names nothing.
	kv.Delete(t.Context(), "hss:subscriber:"+impi)
	if err := s.Add(other); err != nil {
		t.Fatalf("Add after the provisional record went: %v", err)
	}
	if got, ok := s.SubscriberOf(impu); !ok || got.PrivateIdentity != other.PrivateIdentity {
		t.Errorf("SubscriberOf = %+v, %v", got, ok)
	}
}
