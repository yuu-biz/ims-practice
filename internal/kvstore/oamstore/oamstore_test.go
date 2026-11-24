package oamstore_test

import (
	"context"
	"errors"
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

// lease is short, for the tests that wait for a lease to run out, and
// long enough that a loaded machine renews it on time: the tests renew
// four times a lease.
const lease = time.Second

// pair is two instances of the store on one kvstore, with short leases.
func pair(t *testing.T, kv kvstore.Store) (*oamstore.Store, *oamstore.Store) {
	cfg := kvjson.Config{Prefix: storetest.Prefix()}
	return oamstore.NewWithLease(kv, cfg, lease), oamstore.NewWithLease(kv, cfg, lease)
}

// waitOut sleeps past a lease, with a margin for the clock of a server
// and a loaded machine.
func waitOut() { time.Sleep(2 * lease) }

func TestAWatchThatIsNotRenewedRunsOut(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := pair(t, backend.New(t))
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
				time.Sleep(lease / 4)
				if err := old.Renew(ctx); err != nil {
					t.Fatalf("Renew: %v", err)
				}
			}
			if _, ok, _ := b.Watch(ctx, "scscf", "b"); ok {
				t.Fatal("another instance took a watch that is renewed")
			}
			// Not renewed, it runs out, and the next holder goes on from
			// the number that the first one saved.
			waitOut()
			next, ok, err := b.Watch(ctx, "scscf", "b")
			if err != nil || !ok {
				t.Fatalf("Watch after the lease ran out: %v, %v", ok, err)
			}
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
}

func TestPresenceCountsTheInstancesThatRun(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := pair(t, backend.New(t))
			present := func(s *oamstore.Store, instance string) int {
				t.Helper()
				n, err := s.Present(ctx, instance)
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			if n := present(a, "a"); n != 1 {
				t.Errorf("one instance counts %d", n)
			}
			if n := present(b, "b"); n != 2 {
				t.Errorf("two instances count %d", n)
			}
			// b stops saying it runs; a soon counts itself alone.
			for range 8 {
				time.Sleep(lease / 4)
				present(a, "a")
			}
			if n := present(a, "a"); n != 1 {
				t.Errorf("after b stopped, a counts %d", n)
			}
		})
	}
}
