package oamstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/oamstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.OAMNtfSubscriptions(t, func(*testing.T) (oam.NtfSubscriptionStore, oam.NtfSubscriptionStore) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return oamstore.New(kv, cfg), oamstore.New(kv, cfg)
			})
		})
	}
}
