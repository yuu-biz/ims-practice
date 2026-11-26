package oamstore_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
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
			storetest.OAMAlarmWatches(t, func(*testing.T) (oam.AlarmWatchStore, oam.AlarmWatchStore) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return oamstore.New(kv, cfg), oamstore.New(kv, cfg)
			})
		})
	}
}

// lease is short, for the tests that wait for a lease to run out on
// the real clock, which the tests cannot move.
const lease = time.Second

// pair is two instances of the store on one kvstore, with short leases.
func pair(t *testing.T, kv kvstore.Store) (*oamstore.Store, *oamstore.Store) {
	cfg := kvjson.Config{Prefix: storetest.Prefix()}
	return oamstore.NewWithLease(kv, cfg, lease), oamstore.NewWithLease(kv, cfg, lease)
}

// waitOut sleeps past a lease, with a margin for the clock of a server
// and a loaded machine.
func waitOut() { time.Sleep(2 * lease) }

// clock is a clock that moves only when told.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// clockedStore is a kvstore with the clock that its times to live run
// on: now reads it, and pass lets time go by.
type clockedStore struct {
	kv   kvstore.Store
	now  func() time.Time
	pass func(time.Duration)
}

// forEachClockedStore runs test on a MemoryStore whose clock moves only
// when the test passes time, and on Valkey, on the real clock, when
// KVSTORE_VALKEY_ADDR names a server.
func forEachClockedStore(t *testing.T, test func(t *testing.T, s clockedStore)) {
	t.Run("Memory", func(t *testing.T) {
		c := &clock{now: time.UnixMilli(1_790_000_000_000)}
		test(t, clockedStore{kv: kvstore.NewMemoryStore(c.Now), now: c.Now, pass: c.Advance})
	})
	for _, backend := range storetest.Backends() {
		if backend.Name == "Memory" {
			continue
		}
		t.Run(backend.Name, func(t *testing.T) {
			test(t, clockedStore{kv: backend.New(t), now: time.Now, pass: time.Sleep})
		})
	}
}

// skipIfLapsed skips the case when a lease or more has gone by since a
// call that renewed the lease started. Until then the lease is surely
// held, whatever the delays of the calls. Only the real clock can go
// that far between two calls, on a machine that stalled, and the case
// then cannot tell whether the lease should have lasted.
func (s clockedStore) skipIfLapsed(t *testing.T, since time.Time) {
	t.Helper()
	if gone := s.now().Sub(since); gone >= lease {
		t.Skipf("%v went by since the lease was renewed, a lease or more: the machine stalled", gone)
	}
}

func TestAWatchThatIsNotRenewedRunsOut(t *testing.T) {
	ctx := context.Background()
	forEachClockedStore(t, func(t *testing.T, s clockedStore) {
		a, b := pair(t, s.kv)
		renewed := s.now()
		old, ok, err := a.Watch(ctx, "scscf", "a")
		if err != nil || !ok {
			t.Fatalf("Watch: %v, %v", ok, err)
		}
		st := old.State()
		st.JournalID, st.LastNumber = "journal-1", 5
		if err := old.Save(ctx, st); err != nil {
			t.Fatal(err)
		}
		// Renewed, the watch outlasts a lease.
		for range 6 {
			s.pass(lease / 4)
			at := s.now()
			if err := old.Renew(ctx); err != nil {
				s.skipIfLapsed(t, renewed)
				t.Fatalf("Renew: %v", err)
			}
			renewed = at
		}
		if _, ok, _ := b.Watch(ctx, "scscf", "b"); ok {
			s.skipIfLapsed(t, renewed)
			t.Fatal("another instance took a watch that is renewed")
		}
		// Not renewed, it runs out, and the next holder goes on from
		// the number that the first one saved.
		next := watchOnceFree(t, s, b, "scscf", "b")
		if got := next.State(); got.LastNumber != 5 || got.JournalID != "journal-1" || got.Generation != st.Generation+1 {
			t.Errorf("the next holder has %+v", got)
		}
		// The earlier holder can neither put the number back nor keep
		// the watch.
		st.LastNumber = 3
		if err := old.Save(ctx, st); !errors.Is(err, oam.ErrWatchLost) {
			t.Errorf("Save by the earlier holder: %v, want ErrWatchLost", err)
		}
		if err := old.Renew(ctx); !errors.Is(err, oam.ErrWatchLost) {
			t.Errorf("Renew by the earlier holder: %v, want ErrWatchLost", err)
		}
		if got, _ := a.AlarmWatchState(ctx, "scscf"); got.LastNumber != 5 {
			t.Errorf("the state is %+v", got)
		}
	})
}

// watchOnceFree takes the watch of node for instance as soon as the
// lease of its holder has run out, and fails when that takes ten leases.
func watchOnceFree(t *testing.T, s clockedStore, store *oamstore.Store, node, instance string) oam.AlarmWatch {
	t.Helper()
	start := s.now()
	for {
		s.pass(lease / 4)
		w, ok, err := store.Watch(context.Background(), node, instance)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return w
		}
		if gone := s.now().Sub(start); gone > 10*lease {
			t.Fatalf("the watch of %s is still held %v after it was last renewed, with a lease of %v", node, gone, lease)
		}
	}
}

// TestAPresenceOfAnEarlierVersionCounts: an instance of an earlier
// version, which wrote true as its presence, runs on the same store. It
// counts as an instance that reaches every node, and the presence of
// the others is read all the same.
func TestAPresenceOfAnEarlierVersionCounts(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			client := kvjson.New(kv, cfg, "oam")
			if err := kvjson.Write(ctx, client, client.Key("presence", "earlier"), true, lease); err != nil {
				t.Fatal(err)
			}
			if err := client.AddToIndex(ctx, "presence", "earlier"); err != nil {
				t.Fatal(err)
			}
			p, err := oamstore.NewWithLease(kv, cfg, lease).Present(ctx, "a", []string{"scscf"})
			if err != nil || p.Instances != 2 || !maps.Equal(p.Unreachable, map[string]int{"scscf": 1}) {
				t.Errorf("Present beside an earlier version: %+v, %v", p, err)
			}
		})
	}
}

func TestPresenceCountsTheInstancesThatRun(t *testing.T) {
	ctx := context.Background()
	forEachClockedStore(t, func(t *testing.T, s clockedStore) {
		a, b := pair(t, s.kv)
		present := func(s *oamstore.Store, instance string, unreachable ...string) oam.Presence {
			t.Helper()
			p, err := s.Present(ctx, instance, unreachable)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		if p := present(a, "a", "scscf"); p.Instances != 1 || !maps.Equal(p.Unreachable, map[string]int{"scscf": 1}) {
			t.Errorf("one instance reads %+v", p)
		}
		if p := present(b, "b", "scscf", "hss"); p.Instances != 2 || !maps.Equal(p.Unreachable, map[string]int{"scscf": 2, "hss": 1}) {
			t.Errorf("two instances read %+v", p)
		}
		// a reaches scscf again; b stops saying it runs, and a soon
		// reads itself alone.
		start := s.now()
		for {
			s.pass(lease / 4)
			p := present(a, "a")
			if p.Instances == 1 && len(p.Unreachable) == 0 {
				break
			}
			if gone := s.now().Sub(start); gone > 10*lease {
				t.Fatalf("%v after b stopped, with a lease of %v, a reads %+v", gone, lease, p)
			}
		}
	})
}
