package scstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sc"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.SC(t, func(*testing.T) (sc.Store, sc.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return scstore.New(kv, cfg), scstore.New(kv, cfg)
			})
		})
	}
}
