package hss_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// deletedBeforeTheSwap deletes the subscription just before the first
// swap of its repository data, as a DELETE from another client would.
type deletedBeforeTheSwap struct {
	hss.Store
	done bool
}

func (s *deletedBeforeTheSwap) CompareAndSwapRepositoryData(ctx context.Context, privateID string, old *sh.TransparentData, data sh.TransparentData) (bool, error) {
	if !s.done {
		s.done = true
		if _, _, err := s.Store.Delete(ctx, privateID); err != nil {
			return false, err
		}
	}
	return s.Store.CompareAndSwapRepositoryData(ctx, privateID, old, data)
}

// TestCATForASubscriptionDeletedMeanwhile: a PUT of the customized
// alerting tone whose subscription is deleted between its check and its
// write gets 404 instead of trying again for good.
func TestCATForASubscriptionDeletedMeanwhile(t *testing.T) {
	if err := diameter.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	node := diameter.NewNode(diameter.Identity{OriginHost: "hss." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx, diameter.ApplicationIDSh}, diameter.Timers{}, quiet())
	memory, err := hss.NewMemoryStore()
	if err != nil {
		t.Fatal(err)
	}
	store := &deletedBeforeTheSwap{Store: memory}
	h, err := hss.New(hss.Config{Realm: realm, VisitedNetworks: []string{realm}}, store, node, quiet())
	if err != nil {
		t.Fatal(err)
	}
	api := h.Provisioning(noSCSCFs{})
	body := `{"private_identity": "` + impi + `", "public_identities": ["` + impu + `"], "password": "test-password-1"}`
	if w := do(api, "POST", "/subscriptions", body); w.Code != http.StatusCreated {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	answered := make(chan int, 1)
	go func() {
		answered <- do(api, "PUT", "/subscriptions/"+impi+"/customized-alerting-tone", `{"tone": "cat1"}`).Code
	}()
	select {
	case code := <-answered:
		if code != http.StatusNotFound {
			t.Errorf("PUT of the tone of a subscription deleted meanwhile: %d, want 404", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer within 5 s to the PUT of the tone of a subscription deleted meanwhile")
	}
}
