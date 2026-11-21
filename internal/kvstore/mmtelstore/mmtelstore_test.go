package mmtelstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.MMTel(t, func(*testing.T) (mmtel.Store, mmtel.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return mmtelstore.New(kv, cfg), mmtelstore.New(kv, cfg)
			})
		})
	}
}
