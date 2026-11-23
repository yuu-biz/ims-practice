package smscstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/smscstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.SMSC(t, func(*testing.T) (smsc.Store, smsc.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return smscstore.New(kv, cfg), smscstore.New(kv, cfg)
			})
		})
	}
}
