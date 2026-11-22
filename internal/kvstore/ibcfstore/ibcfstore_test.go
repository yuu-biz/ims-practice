package ibcfstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.IBCF(t, func(*testing.T) (ibcf.Store, ibcf.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return ibcfstore.New(kv, cfg), ibcfstore.New(kv, cfg)
			})
		})
	}
}

// TestMemoryStore runs the suite on the Store of the ibcf package, which
// two processes share by sharing the one value. It lives here rather
// than in storetest's memory_test.go, so that the suite file of the
// role and the tests that run it stay together.
func TestMemoryStore(t *testing.T) {
	storetest.IBCF(t, func(*testing.T) (ibcf.Store, ibcf.Store) {
		s := ibcf.NewMemoryStore()
		return s, s
	})
}
