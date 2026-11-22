package scscfstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/scscf"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.SCSCF(t, func(*testing.T) (scscf.Store, scscf.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return scscfstore.New(kv, cfg), scscfstore.New(kv, cfg)
			})
		})
	}
}
