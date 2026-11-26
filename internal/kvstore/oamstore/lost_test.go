package oamstore_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/oamstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// losingAnswers is a kvstore.Store that loses the answer of the next
// compare and swap of a key of one kind: the store makes the write, and
// the caller gets the failure of a request that ran out of time.
type losingAnswers struct {
	kvstore.Store

	mu   sync.Mutex
	kind string
}

// loseNext loses the answer of the next compare and swap of a key of
// kind, such as "watch-lease".
func (l *losingAnswers) loseNext(kind string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kind = kind
}

func (l *losingAnswers) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	newVersion, err := l.Store.CompareAndSwap(ctx, key, version, value, ttl)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil && l.kind != "" && strings.Contains(key, ":"+l.kind+":") {
		l.kind = ""
		return "", fmt.Errorf("the answer of the write was lost: %w", context.DeadlineExceeded)
	}
	return newVersion, err
}

// TestAWatchWhoseAnswerWasLostGoesOn: a renewal of the lease, or a save
// of the state, reaches the store, but its answer does not reach the
// instance. The instance still holds the watch: it goes on renewing and
// saving, another instance cannot take it, and its release gives it up
// at once.
func TestAWatchWhoseAnswerWasLostGoesOn(t *testing.T) {
	for _, backend := range storetest.Backends() {
		for _, kind := range []string{"watch-lease", "watch-state"} {
			t.Run(backend.Name+"/"+kind, func(t *testing.T) {
				ctx := context.Background()
				kv := backend.New(t)
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				lossy := &losingAnswers{Store: kv}
				a := oamstore.NewWithLease(lossy, cfg, lease)
				b := oamstore.NewWithLease(kv, cfg, lease)
				w, ok, err := a.Watch(ctx, "scscf", "a")
				if err != nil || !ok {
					t.Fatalf("Watch: %v, %v", ok, err)
				}
				st := w.State()
				st.JournalID, st.LastNumber = "journal-1", 1

				lossy.loseNext(kind)
				if kind == "watch-lease" {
					err = w.Renew(ctx)
				} else {
					err = w.Save(ctx, st)
				}
				if err == nil || errors.Is(err, oam.ErrWatchLost) {
					t.Fatalf("the write whose answer was lost: %v, want the failure of the store", err)
				}

				if err := w.Renew(ctx); err != nil {
					t.Errorf("Renew after the lost answer: %v", err)
				}
				st.LastNumber = 2
				if err := w.Save(ctx, st); err != nil {
					t.Errorf("Save after the lost answer: %v", err)
				}
				if got, err := b.AlarmWatchState(ctx, "scscf"); err != nil || got != st {
					t.Errorf("the state is %+v, %v; want %+v", got, err, st)
				}
				if _, ok, err := b.Watch(ctx, "scscf", "b"); err != nil || ok {
					t.Errorf("another instance's Watch of the watch held: %v, %v", ok, err)
				}
				if err := w.Release(ctx); err != nil {
					t.Errorf("Release: %v", err)
				}
				if _, ok, err := b.Watch(ctx, "scscf", "b"); err != nil || !ok {
					t.Errorf("another instance's Watch after the release: %v, %v", ok, err)
				}
			})
		}
	}
}

// TestAReleaseAfterALostRenewalAnswerGivesTheWatchUp: the answer of a
// renewal is lost, and the watcher gives the watch up before it renews
// again. Another instance can take the watch at once, and need not wait
// for the lease to run out.
func TestAReleaseAfterALostRenewalAnswerGivesTheWatchUp(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			lossy := &losingAnswers{Store: kv}
			a := oamstore.NewWithLease(lossy, cfg, lease)
			b := oamstore.NewWithLease(kv, cfg, lease)
			w, ok, err := a.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch: %v, %v", ok, err)
			}
			lossy.loseNext("watch-lease")
			if err := w.Renew(ctx); err == nil {
				t.Fatal("the renewal whose answer was lost succeeded")
			}
			if err := w.Release(ctx); err != nil {
				t.Errorf("Release: %v", err)
			}
			if _, ok, err := b.Watch(ctx, "scscf", "b"); err != nil || !ok {
				t.Errorf("another instance's Watch after the release: %v, %v", ok, err)
			}
		})
	}
}

// TestALostAnswerDoesNotKeepAWatchThatAnotherTook: after the answer of
// a renewal is lost, the lease runs out and another instance takes the
// watch. The earlier holder finds that the watch is no longer its own.
func TestALostAnswerDoesNotKeepAWatchThatAnotherTook(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			lossy := &losingAnswers{Store: kv}
			a := oamstore.NewWithLease(lossy, cfg, lease)
			b := oamstore.NewWithLease(kv, cfg, lease)
			old, ok, err := a.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch: %v, %v", ok, err)
			}
			lossy.loseNext("watch-lease")
			if err := old.Renew(ctx); err == nil {
				t.Fatal("the renewal whose answer was lost succeeded")
			}
			waitOut()
			next, ok, err := b.Watch(ctx, "scscf", "b")
			if err != nil || !ok {
				t.Fatalf("Watch after the lease ran out: %v, %v", ok, err)
			}
			if err := old.Renew(ctx); !errors.Is(err, oam.ErrWatchLost) {
				t.Errorf("Renew by the earlier holder: %v, want ErrWatchLost", err)
			}
			st := old.State()
			st.LastNumber = 9
			if err := old.Save(ctx, st); !errors.Is(err, oam.ErrWatchLost) {
				t.Errorf("Save by the earlier holder: %v, want ErrWatchLost", err)
			}
			if err := old.Release(ctx); err != nil {
				t.Errorf("Release by the earlier holder: %v", err)
			}
			if err := next.Renew(ctx); err != nil {
				t.Errorf("Renew by the next holder: %v", err)
			}
		})
	}
}

// TestAHolderDoesNotTakeAStateWrittenAfterTheStoreWasEmptied: the store
// is emptied while instance a holds a watch. Instance b takes the watch
// anew, and writes a state of the first generation, the one that a has.
// a's next save finds that state, which is not its own: the watch is
// lost, and b's state stays.
func TestAHolderDoesNotTakeAStateWrittenAfterTheStoreWasEmptied(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			a := oamstore.NewWithLease(kv, cfg, lease)
			b := oamstore.NewWithLease(kv, cfg, lease)
			old, ok, err := a.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch: %v, %v", ok, err)
			}
			client := kvjson.New(kv, cfg, "oam")
			for _, key := range []string{client.Key("watch-lease", "scscf"), client.Key("watch-state", "scscf")} {
				if err := client.Remove(ctx, key); err != nil {
					t.Fatal(err)
				}
			}
			next, ok, err := b.Watch(ctx, "scscf", "b")
			if err != nil || !ok {
				t.Fatalf("Watch after the store was emptied: %v, %v", ok, err)
			}
			if next.State().Generation != old.State().Generation {
				t.Fatalf("the generations %d and %d", old.State().Generation, next.State().Generation)
			}
			st := old.State()
			st.JournalID, st.LastNumber = "journal-1", 9
			if err := old.Save(ctx, st); !errors.Is(err, oam.ErrWatchLost) {
				t.Errorf("Save by the holder before the store was emptied: %v, want ErrWatchLost", err)
			}
			if got, err := b.AlarmWatchState(ctx, "scscf"); err != nil || got != next.State() {
				t.Errorf("the state is %+v, %v; want b's %+v", got, err, next.State())
			}
		})
	}
}

// TestAProcessOfTheSameNameDoesNotTakeTheLeaseOfAnother: two processes
// run with the same instance name. The first holds a watch, and stops
// for longer than its lease; the second takes the watch. When the first
// goes on, it finds that the lease is not its own, although it names
// its instance: its renewal loses the watch, and its release leaves the
// lease of the second.
func TestAProcessOfTheSameNameDoesNotTakeTheLeaseOfAnother(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			first := oamstore.NewWithLease(kv, cfg, lease)
			second := oamstore.NewWithLease(kv, cfg, lease)
			other := oamstore.NewWithLease(kv, cfg, lease)
			old, ok, err := first.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch: %v, %v", ok, err)
			}
			waitOut()
			next, ok, err := second.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch after the lease ran out: %v, %v", ok, err)
			}
			if err := old.Renew(ctx); !errors.Is(err, oam.ErrWatchLost) {
				t.Errorf("Renew by the first process: %v, want ErrWatchLost", err)
			}
			if err := old.Release(ctx); err != nil {
				t.Errorf("Release by the first process: %v", err)
			}
			if _, ok, err := other.Watch(ctx, "scscf", "b"); err != nil || ok {
				t.Errorf("another instance's Watch of the lease of the second process: %v, %v", ok, err)
			}
			if err := next.Renew(ctx); err != nil {
				t.Errorf("Renew by the second process: %v", err)
			}
		})
	}
}

// TestATakeWhoseAnswerWasLostTakesTheWatch: the write that takes the
// lease of a node reaches the store, but its answer does not reach the
// instance. The instance holds the watch all the same, rather than
// leave the node with a lease that no watcher renews until it runs
// out.
func TestATakeWhoseAnswerWasLostTakesTheWatch(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv := backend.New(t)
			cfg := kvjson.Config{Prefix: storetest.Prefix()}
			lossy := &losingAnswers{Store: kv}
			a := oamstore.NewWithLease(lossy, cfg, lease)
			b := oamstore.NewWithLease(kv, cfg, lease)
			lossy.loseNext("watch-lease")
			w, ok, err := a.Watch(ctx, "scscf", "a")
			if err != nil || !ok {
				t.Fatalf("Watch whose take lost its answer: %v, %v", ok, err)
			}
			if st := w.State(); st.Generation != 1 {
				t.Errorf("the state of the watch: %+v", st)
			}
			if err := w.Renew(ctx); err != nil {
				t.Errorf("Renew: %v", err)
			}
			if _, ok, err := b.Watch(ctx, "scscf", "b"); err != nil || ok {
				t.Errorf("another instance's Watch of the watch held: %v, %v", ok, err)
			}
		})
	}
}
