package pcscfstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/pcscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/pcscf"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.PCSCF(t, func(*testing.T) (pcscf.Store, pcscf.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return pcscfstore.New(kv, cfg), pcscfstore.New(kv, cfg)
			})
		})
	}
}
