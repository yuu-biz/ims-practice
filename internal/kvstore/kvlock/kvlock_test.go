package kvlock_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
)

const impi = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"

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

// harness is one store, as the instances of a role see it.
type harness struct {
	// kv reads the store directly.
	kv kvstore.Store
	// newLocker is one instance of the role: a Locker on a client of its
	// own, with the prefix of the test. newLockerOn is the same with the
	// client seen through wrap.
	newLocker   func(cfg kvlock.Config) *kvlock.Locker
	newLockerOn func(wrap func(kvstore.Store) kvstore.Store, cfg kvlock.Config) *kvlock.Locker
	// lease is the lease of the Lockers, and pass lets time go by for
	// the store.
	lease  time.Duration
	pass   func(d time.Duration)
	prefix string
}

// forEachStore runs test on a MemoryStore with a stopped clock and the
// default lease, and on Valkey with a short lease when
// KVSTORE_VALKEY_ADDR names a server, such as 127.0.0.1:6379.
func forEachStore(t *testing.T, test func(t *testing.T, h harness)) {
	t.Run("Memory", func(t *testing.T) {
		c := &clock{now: time.UnixMilli(1_790_000_000_000)}
		s := kvstore.NewMemoryStore(c.Now)
		on := func(wrap func(kvstore.Store) kvstore.Store, cfg kvlock.Config) *kvlock.Locker {
			return kvlock.New(wrap(s), cfg)
		}
		test(t, harness{kv: s, lease: 15 * time.Second, pass: c.Advance, newLockerOn: on,
			newLocker: func(cfg kvlock.Config) *kvlock.Locker { return on(same, cfg) }})
	})
	t.Run("Valkey", func(t *testing.T) {
		addr := os.Getenv("KVSTORE_VALKEY_ADDR")
		if addr == "" {
			t.Skip("KVSTORE_VALKEY_ADDR is not set")
		}
		const lease = 300 * time.Millisecond
		prefix := "kvlocktest:" + rand.Text() + ":"
		client := func() kvstore.Store {
			v := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
			t.Cleanup(v.Close)
			return v
		}
		on := func(wrap func(kvstore.Store) kvstore.Store, cfg kvlock.Config) *kvlock.Locker {
			cfg.Prefix = prefix + cfg.Prefix
			if cfg.Lease == 0 {
				cfg.Lease = lease
			}
			return kvlock.New(wrap(client()), cfg)
		}
		test(t, harness{kv: client(), lease: lease, pass: time.Sleep, prefix: prefix, newLockerOn: on,
			newLocker: func(cfg kvlock.Config) *kvlock.Locker { return on(same, cfg) }})
	})
}

func same(s kvstore.Store) kvstore.Store { return s }

// came tells whether cond held within d.
func came(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return true
}

// lockWithin tries to lock user for d, and returns the unlock function,
// or nil when the lock was not had in that time.
func lockWithin(t *testing.T, l *kvlock.Locker, user string, d time.Duration) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	unlock, err := l.Lock(ctx, user)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Lock = %v, want a lock or the end of the context", err)
		}
		return nil
	}
	return unlock
}

func TestOnlyOneInstanceHoldsTheLock(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		a, b := h.newLocker(kvlock.Config{Role: "scscf"}), h.newLocker(kvlock.Config{Role: "scscf"})
		unlock, err := a.Lock(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}
		if u := lockWithin(t, b, impi, 100*time.Millisecond); u != nil {
			t.Fatal("the second instance got a lock that the first holds")
		}
		// Another user, and the same user in another role, are not held up.
		for _, other := range []struct {
			l    *kvlock.Locker
			user string
		}{{b, "001010000000002@ims.mnc001.mcc001.3gppnetwork.org"}, {h.newLocker(kvlock.Config{Role: "hss"}), impi}} {
			u := lockWithin(t, other.l, other.user, time.Second)
			if u == nil {
				t.Fatalf("lock of %s held up", other.user)
			}
			u()
		}
		unlock()
		u := lockWithin(t, b, impi, time.Second)
		if u == nil {
			t.Fatal("the second instance did not get the lock after its release")
		}
		u()
	})
}

// TestMutualExclusion increments a counter under the lock from several
// goroutines of several instances. A read and a write that another
// holder interleaves would lose an increment.
func TestMutualExclusion(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		const instances, goroutines, increments = 4, 3, 20
		var counter, inside atomic.Int64
		var wg sync.WaitGroup
		for range instances {
			l := h.newLocker(kvlock.Config{Role: "scscf"})
			for range goroutines {
				wg.Go(func() {
					for range increments {
						unlock, err := l.Lock(context.Background(), impi)
						if err != nil {
							t.Error(err)
							return
						}
						if n := inside.Add(1); n != 1 {
							t.Errorf("%d holders at once", n)
						}
						v := counter.Load()
						runtime.Gosched()
						counter.Store(v + 1)
						inside.Add(-1)
						unlock()
					}
				})
			}
		}
		wg.Wait()
		if got, want := counter.Load(), int64(instances*goroutines*increments); got != want {
			t.Fatalf("counter = %d, want %d", got, want)
		}
	})
}

// TestStoppedHolder: an instance that stops holding a lock keeps the
// others waiting until its lease runs out, and its release, when it
// goes on after that, does not free the lock of the instance that took
// over.
func TestStoppedHolder(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		var log syncBuffer
		stopped := h.newLocker(kvlock.Config{Role: "scscf", Log: slog.New(slog.NewTextHandler(&log, nil))})
		next, third := h.newLocker(kvlock.Config{Role: "scscf"}), h.newLocker(kvlock.Config{Role: "scscf"})
		lateUnlock, err := stopped.Lock(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}

		got := make(chan func(), 1)
		go func() {
			unlock, err := next.Lock(context.Background(), impi)
			if err != nil {
				t.Error(err)
				close(got)
				return
			}
			got <- unlock
		}()
		h.pass(h.lease / 3)
		select {
		case <-got:
			t.Fatal("lock taken over before the lease ran out")
		case <-time.After(min(h.lease/6, 100*time.Millisecond)):
		}
		h.pass(h.lease - h.lease/3)
		var unlock func()
		select {
		case unlock = <-got:
		case <-time.After(2 * time.Second):
			t.Fatal("lock not taken over after the lease")
		}
		if unlock == nil {
			return
		}

		lateUnlock()
		if !strings.Contains(log.String(), "user lock released after its lease ran out") {
			t.Errorf("the late release logged %q", log.String())
		}
		if u := lockWithin(t, third, impi, 100*time.Millisecond); u != nil {
			t.Fatal("the late release freed the lock of the instance that took over")
		}
		unlock()
		if u := lockWithin(t, third, impi, time.Second); u == nil {
			t.Fatal("lock not had after its holder released it")
		} else {
			u()
		}
	})
}

func TestUnlockTwice(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		a, b := h.newLocker(kvlock.Config{Role: "scscf"}), h.newLocker(kvlock.Config{Role: "scscf"})
		unlockA, err := a.Lock(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}
		unlockA()
		unlockB := lockWithin(t, b, impi, time.Second)
		if unlockB == nil {
			t.Fatal("no lock after the release")
		}
		unlockA()
		if u := lockWithin(t, a, impi, 100*time.Millisecond); u != nil {
			t.Fatal("a second unlock freed the lock of the next holder")
		}
		unlockB()
	})
}

func TestContextEndsWhileWaiting(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		a, b := h.newLocker(kvlock.Config{Role: "hss"}), h.newLocker(kvlock.Config{Role: "hss"})
		unlock, err := a.Lock(context.Background(), impi)
		if err != nil {
			t.Fatal(err)
		}
		// One waiter tries the store from the other instance; the other
		// waits in the queue of the holder's own process.
		for _, l := range []*kvlock.Locker{b, a} {
			waiting := l.Users(impi) + 1
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				u, err := l.Lock(ctx, impi)
				if u != nil {
					// ims.UserLocker: on an error nothing is held and
					// unlock is nil.
					t.Error("unlock returned with the end of the context")
					u()
				}
				done <- err
			}()
			if !came(5*time.Second, func() bool { return l.Users(impi) >= waiting }) {
				t.Fatal("the waiter did not start to wait for the lock within 5 s")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Lock = %v, want context.Canceled", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Lock did not return when its context ended")
			}
		}
		unlock()
		if a.Queued() != 0 || b.Queued() != 0 {
			t.Fatalf("queues left: %d, %d", a.Queued(), b.Queued())
		}
		if u := lockWithin(t, a, impi, time.Second); u == nil {
			t.Fatal("no lock after the waiters gave up")
		} else {
			u()
		}
	})
}

// TestPrivateIdentityWithoutCase: the S-CSCF and the HSS lock a
// private identity, a NAI, which compares without case (ims.UserLocker).
func TestPrivateIdentityWithoutCase(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		a, b := h.newLocker(kvlock.Config{Role: "hss"}), h.newLocker(kvlock.Config{Role: "hss"})
		unlock, err := a.Lock(context.Background(), "001010000000001@IMS.MNC001.MCC001.3GPPNETWORK.ORG")
		if err != nil {
			t.Fatal(err)
		}
		if u := lockWithin(t, b, impi, 100*time.Millisecond); u != nil {
			t.Fatal("the same private identity in other case got a second lock")
		}
		if _, err := h.kv.Get(context.Background(), h.prefix+"hss:lock:"+impi); err != nil {
			t.Fatalf("lock item under the lower-case identity: %v", err)
		}
		unlock()
	})
}

// TestEndedContextIsNoLock: a context that has already ended gets no
// lock and a nil unlock, even when the lock is free.
func TestEndedContextIsNoLock(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		l := h.newLocker(kvlock.Config{Role: "scscf"})
		unlock, err := l.Lock(ctx, impi)
		if !errors.Is(err, context.Canceled) || unlock != nil {
			t.Fatalf("Lock = %v, %v; want no lock and context.Canceled", unlock != nil, err)
		}
		if l.Queued() != 0 {
			t.Fatal("queue left")
		}
	})
}

func TestKeyAndCase(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		a := h.newLocker(kvlock.Config{Prefix: "net-a:", Role: "ipsmgw", Holder: "ipsmgw-1.ims.example"})
		b := h.newLocker(kvlock.Config{Prefix: "net-a:", Role: "ipsmgw", Holder: "ipsmgw-2.ims.example"})
		unlock, err := a.Lock(context.Background(), "sip:+12025550101@IMS.example")
		if err != nil {
			t.Fatal(err)
		}
		item, err := h.kv.Get(context.Background(), h.prefix+"net-a:ipsmgw:lock:sip%3A+12025550101@ims.example")
		if err != nil || !strings.HasPrefix(string(item.Value), "ipsmgw-1.ims.example ") {
			t.Fatalf("lock item = %q, %v; want the holder's name and a token", item.Value, err)
		}
		if u := lockWithin(t, b, "SIP:+12025550101@ims.EXAMPLE", 100*time.Millisecond); u != nil {
			t.Fatal("the same identity in other case got a second lock")
		}
		unlock()
		if _, err := h.kv.Get(context.Background(), h.prefix+"net-a:ipsmgw:lock:sip%3A+12025550101@ims.example"); !errors.Is(err, kvstore.ErrNotFound) {
			t.Fatalf("lock item after the release: %v, want ErrNotFound", err)
		}
	})
}

// countingStore counts the tries to take a lock.
type countingStore struct {
	kvstore.Store
	swaps atomic.Int64
}

func (s *countingStore) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	s.swaps.Add(1)
	return s.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

// TestWaitersOfOneInstanceDoNotTryTheStore: while a goroutine of the
// instance holds the lock, the others of the same instance wait in the
// process. Waiters of another instance try the store, and the same
// watch sees their tries come.
func TestWaitersOfOneInstanceDoNotTryTheStore(t *testing.T) {
	const watch = 500 * time.Millisecond
	for _, tc := range []struct {
		name         string
		sameInstance bool
	}{
		{"same instance", true},
		{"other instance", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &countingStore{Store: kvstore.NewMemoryStore(nil)}
			l := kvlock.New(s, kvlock.Config{Role: "scscf"})
			waiters := l
			if !tc.sameInstance {
				waiters = kvlock.New(s, kvlock.Config{Role: "scscf"})
			}
			unlock, err := l.Lock(context.Background(), impi)
			if err != nil {
				t.Fatal(err)
			}
			waiting := waiters.Users(impi) + 5
			var wg sync.WaitGroup
			for range 5 {
				wg.Go(func() {
					u, err := waiters.Lock(context.Background(), impi)
					if err != nil {
						t.Error(err)
						return
					}
					u()
				})
			}
			if !came(5*time.Second, func() bool { return waiters.Users(impi) >= waiting }) {
				t.Fatal("the 5 waiters did not start to wait for the lock within 5 s")
			}
			tried := came(watch, func() bool { return s.swaps.Load() > 1 })
			if tc.sameInstance && tried {
				t.Errorf("%d tries of the store while the lock was held in the process, want only the holder's", s.swaps.Load())
			}
			if !tc.sameInstance && !tried {
				t.Errorf("the waiters of another instance did not try the store within %v", watch)
			}
			unlock()
			wg.Wait()
			if n := s.swaps.Load(); tc.sameInstance && n != 6 {
				t.Errorf("%d tries of the store, want one for each holder", n)
			}
		})
	}
}

var errBroken = errors.New("store broken")

// brokenStore fails the operations of a lock.
type brokenStore struct {
	kvstore.Store
	swap, delete bool
}

func (s *brokenStore) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if s.swap {
		return "", errBroken
	}
	return s.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (s *brokenStore) CompareAndDelete(ctx context.Context, key, version string) error {
	if s.delete {
		return errBroken
	}
	return s.Store.CompareAndDelete(ctx, key, version)
}

// TestStoreFailureIsNoLock: when the store fails, Lock reports it at
// once and holds nothing, rather than waiting or going on unlocked.
func TestStoreFailureIsNoLock(t *testing.T) {
	l := kvlock.New(&brokenStore{Store: kvstore.NewMemoryStore(nil), swap: true}, kvlock.Config{Role: "scscf"})
	unlock, err := l.Lock(context.Background(), impi)
	if !errors.Is(err, errBroken) || unlock != nil {
		t.Fatalf("Lock = %v, %v; want no lock and the store's error", unlock != nil, err)
	}
	if l.Queued() != 0 {
		t.Fatal("queue left after a failure")
	}
}

func TestUnreachableValkeyIsNoLock(t *testing.T) {
	// Nothing listens on port 1 of the loopback.
	v := kvstore.NewValkey(kvstore.ValkeyConfig{Address: "127.0.0.1:1", Timeout: 200 * time.Millisecond})
	defer v.Close()
	l := kvlock.New(v, kvlock.Config{Role: "scscf"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := l.Lock(ctx, impi)
	if err == nil || unlock != nil || ctx.Err() != nil {
		t.Fatalf("Lock = %v, %v; want no lock and the store's error before the context ends", unlock != nil, err)
	}
}

// TestFailedReleaseEndsWithTheLease: a release that the store refuses
// is logged, frees the queue of the process, and the lock ends with its
// lease.
func TestFailedReleaseEndsWithTheLease(t *testing.T) {
	c := &clock{now: time.UnixMilli(1_790_000_000_000)}
	s := &brokenStore{Store: kvstore.NewMemoryStore(c.Now), delete: true}
	var log syncBuffer
	l := kvlock.New(s, kvlock.Config{Role: "scscf", Log: slog.New(slog.NewTextHandler(&log, nil))})
	unlock, err := l.Lock(context.Background(), impi)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if !strings.Contains(log.String(), "user lock not released") || !strings.Contains(log.String(), errBroken.Error()) {
		t.Errorf("the failed release logged %q", log.String())
	}
	if l.Queued() != 0 {
		t.Fatal("queue left after the release")
	}
	// The default lease is ims.UserLockLease of the plan, 15 s.
	c.Advance(15*time.Second - time.Millisecond)
	if u := lockWithin(t, l, impi, 50*time.Millisecond); u != nil {
		t.Fatal("lock had again before its lease ran out")
	}
	c.Advance(time.Millisecond)
	if u := lockWithin(t, l, impi, time.Second); u == nil {
		t.Fatal("lock not had after its lease")
	}
}

// syncBuffer is a log that the goroutines of a test share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// losingAnswers is a store whose next take of a lock reaches the store
// but whose answer does not reach the instance, as when the answer
// comes after the timeout of the request.
// With then, the loss comes with the end of the context of the Lock:
// then ends it.
type losingAnswers struct {
	kvstore.Store
	lose atomic.Bool
	then func()
}

func (s *losingAnswers) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	v, err := s.Store.CompareAndSwap(ctx, key, version, value, ttl)
	if err == nil && s.lose.CompareAndSwap(true, false) {
		if s.then != nil {
			s.then()
		}
		return "", context.DeadlineExceeded
	}
	return v, err
}

// TestATakeWhoseAnswerWasLostHoldsTheLock: the write that takes a lock
// reaches the store, but its answer is lost. The instance holds the
// lock all the same, and its release frees it for the others at once,
// rather than leave a lock that no one holds to block the user on every
// instance until the lease runs out.
func TestATakeWhoseAnswerWasLostHoldsTheLock(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		lossy := &losingAnswers{}
		a := h.newLockerOn(func(s kvstore.Store) kvstore.Store { lossy.Store = s; return lossy }, kvlock.Config{Role: "scscf", Holder: "scscf-1"})
		b := h.newLocker(kvlock.Config{Role: "scscf", Holder: "scscf-2"})
		lossy.lose.Store(true)
		unlock, err := a.Lock(context.Background(), impi)
		if err != nil {
			t.Fatalf("Lock whose take lost its answer: %v", err)
		}
		if u := lockWithin(t, b, impi, 100*time.Millisecond); u != nil {
			u()
			t.Fatal("another instance got the lock that the take whose answer was lost holds")
		}
		unlock()
		if u := lockWithin(t, b, impi, time.Second); u == nil {
			t.Fatal("the lock was not free after its release")
		} else {
			u()
		}
	})
}

// TestATakeWhoseAnswerCameAfterTheEndOfItsContextFreesTheLock: the
// answer of the take is lost because the context of the Lock ended. The
// caller has given up, and the lock that the take wrote is freed at
// once for the other instances.
func TestATakeWhoseAnswerCameAfterTheEndOfItsContextFreesTheLock(t *testing.T) {
	forEachStore(t, func(t *testing.T, h harness) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lossy := &losingAnswers{then: cancel}
		a := h.newLockerOn(func(s kvstore.Store) kvstore.Store { lossy.Store = s; return lossy }, kvlock.Config{Role: "scscf", Holder: "scscf-1"})
		b := h.newLocker(kvlock.Config{Role: "scscf", Holder: "scscf-2"})
		lossy.lose.Store(true)
		if unlock, err := a.Lock(ctx, impi); !errors.Is(err, context.Canceled) || unlock != nil {
			t.Fatalf("Lock whose context ended with the answer of its take = %v, %v; want no lock and the end of the context", unlock != nil, err)
		}
		if u := lockWithin(t, b, impi, 100*time.Millisecond); u == nil {
			t.Fatal("the lock that the given up take wrote was not freed")
		} else {
			u()
		}
	})
}
