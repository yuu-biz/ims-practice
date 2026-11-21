package agwstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore/agwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			// One prefix per case of the suite, so that the relays of a
			// case share the store and those of other cases do not.
			prefixes := map[*testing.T]string{}
			storetest.AGW(t, func(t *testing.T, relay string) agw.ContextStore {
				prefix, ok := prefixes[t]
				if !ok {
					prefix = storetest.Prefix()
					prefixes[t] = prefix
				}
				s, err := agwstore.New(kv, agwstore.Config{Config: kvjson.Config{Prefix: prefix}, Relay: relay})
				if err != nil {
					t.Fatal(err)
				}
				return s
			})
		})
	}
}

// TestMemoryContextStore runs the suite on the ContextStore of the main
// line. It lives here rather than in storetest's memory_test.go, to keep
// this unit to its own directory and suite file. A relay created again
// is the same value: a MemoryContextStore lives only as long as its
// process.
func TestMemoryContextStore(t *testing.T) {
	stores := map[*testing.T]map[string]*agw.MemoryContextStore{}
	storetest.AGW(t, func(t *testing.T, relay string) agw.ContextStore {
		if stores[t] == nil {
			stores[t] = map[string]*agw.MemoryContextStore{}
		}
		s, ok := stores[t][relay]
		if !ok {
			s = agw.NewMemoryContextStore()
			stores[t][relay] = s
		}
		return s
	})
}

func TestNewWithoutRelayName(t *testing.T) {
	if _, err := agwstore.New(nil, agwstore.Config{}); err == nil {
		t.Fatal("New without a relay name succeeded")
	}
}
