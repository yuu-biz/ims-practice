// Package storetest holds the behavioural tests that every Store of a
// role must pass: the MemoryStore in the role's package, and the
// adapter on
// kvstore of internal/kvstore/<role>store, on a kvstore.MemoryStore and
// on Valkey. One suite for both is what shows that the adapter can take
// the place of the MemoryStore.
//
// Each suite takes a function that makes two instances of the Store that
// share their state: the same MemoryStore twice, or two adapters on one
// kvstore. What one writes, the other reads; and both may write the
// same item at once, as two nodes of a role do.
//
// The role packages test their MemoryStores through the role, in
// _test.go files that cannot be imported. The cases here are ported
// from those tests where they test the Store (internal/hss/store_test.go,
// internal/hss/aka_test.go), and written from the Store's documentation
// otherwise.
package storetest

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// Backend is a kvstore.Store to run an adapter's suite on.
type Backend struct {
	Name string
	// New returns the store. Two calls in one test may return the same
	// server; Prefix keeps the tests apart.
	New func(t *testing.T) kvstore.Store
}

// Backends are a kvstore.MemoryStore, and Valkey when
// KVSTORE_VALKEY_ADDR names a server, such as 127.0.0.1:6379.
func Backends() []Backend {
	backends := []Backend{{Name: "Memory", New: func(*testing.T) kvstore.Store { return kvstore.NewMemoryStore(nil) }}}
	if addr := os.Getenv("KVSTORE_VALKEY_ADDR"); addr != "" {
		backends = append(backends, Backend{Name: "Valkey", New: func(t *testing.T) kvstore.Store {
			v := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
			t.Cleanup(v.Close)
			return v
		}})
	}
	return backends
}

// Prefix is a prefix of keys of its own, for one test in a server that
// several tests share.
func Prefix() string { return "storetest:" + rand.Text() + ":" }

// short is the lifetime of an item that a test waits out. The suites
// run on real clocks, since the MemoryStores of the role packages read
// time.Now.
const short = 150 * time.Millisecond

// waitOut sleeps until an item of lifetime short has expired, with a
// margin for the clocks of a server.
func waitOut() { time.Sleep(short + 100*time.Millisecond) }

// sameJSON reports whether a and b encode alike. The adapters keep
// values as JSON, so a time comes back without its monotonic reading,
// which does not change what the value means.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	ja, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	jb, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(ja) == string(jb)
}

// parallel runs f n times at once and waits for all of them.
func parallel(n int, f func(i int)) {
	done := make(chan struct{})
	for i := range n {
		go func() {
			defer func() { done <- struct{}{} }()
			f(i)
		}()
	}
	for range n {
		<-done
	}
}

// run runs each case of a suite on a new pair of Stores.
func run[S any](t *testing.T, newPair func(t *testing.T) (S, S), cases []testCase[S]) {
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, b := newPair(t)
			c.run(t, a, b)
		})
	}
}

type testCase[S any] struct {
	name string
	run  func(t *testing.T, a, b S)
}
