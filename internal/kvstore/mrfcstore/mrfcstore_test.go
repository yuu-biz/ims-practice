package mrfcstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mrfcstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.MRFC(t, func(*testing.T) (mrfc.Store, mrfc.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return mrfcstore.New(kv, cfg), mrfcstore.New(kv, cfg)
			})
		})
	}
}

// TestMemoryStore runs the suite on the Store of the mrfc package, which
// two instances share by sharing the one value. It lives here rather
// than in storetest's memory_test.go, so that the suite file of the
// role and the tests that run it stay together.
func TestMemoryStore(t *testing.T) {
	storetest.MRFC(t, func(*testing.T) (mrfc.Store, mrfc.Store) {
		s := mrfc.NewMemoryStore()
		return s, s
	})
}
