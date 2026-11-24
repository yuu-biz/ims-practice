package oamstore_test

import (
	"context"
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

// pausing is a kvstore.Store that stops the first read, or the first
// compare and swap, of a watch state until the test lets it go on, as
// when the process of an instance stops for a while after it has taken
// a lease.
type pausing struct {
	kvstore.Store
	// atSwap stops the compare and swap rather than the read.
	atSwap  bool
	once    sync.Once
	reached chan struct{}
	resume  chan struct{}
}

// pause stops the call on key once, and gives the context that the call
// goes on with: the clock of a process that is stopped does not run
// either, so the time limit of the request starts when it goes on.
func (p *pausing) pause(ctx context.Context, key string, swap bool) (context.Context, context.CancelFunc) {
	paused := false
	if strings.Contains(key, "watch-state:") && swap == p.atSwap {
		p.once.Do(func() {
			close(p.reached)
			<-p.resume
			paused = true
		})
	}
	if !paused {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), time.Second)
}

func (p *pausing) Get(ctx context.Context, key string) (kvstore.Item, error) {
	ctx, cancel := p.pause(ctx, key, false)
	defer cancel()
	return p.Store.Get(ctx, key)
}

func (p *pausing) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	ctx, cancel := p.pause(ctx, key, true)
	defer cancel()
	return p.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

// TestAHolderThatStoppedPastItsLeaseDoesNotTakeTheWatchBack: instance a
// takes the lease of a node, then stops before it raises the generation
// of the state, for longer than a lease: before it reads the state, or
// after it has read it and renewed its lease. Instance b takes the lease
// and raises the generation. When a goes on, it finds that its lease is
// no longer its own, and does not take the watch: b keeps it, and b's
// writes go on.
func TestAHolderThatStoppedPastItsLeaseDoesNotTakeTheWatchBack(t *testing.T) {
	for _, backend := range storetest.Backends() {
		for _, atSwap := range []bool{false, true} {
			name := backend.Name + "/before the read"
			if atSwap {
				name = backend.Name + "/before the write"
			}
			t.Run(name, func(t *testing.T) { aStoppedHolder(t, backend, atSwap) })
		}
	}
}

func aStoppedHolder(t *testing.T, backend storetest.Backend, atSwap bool) {
	ctx := context.Background()
	kv := backend.New(t)
	cfg := kvjson.Config{Prefix: storetest.Prefix()}
	paused := &pausing{Store: kv, atSwap: atSwap, reached: make(chan struct{}), resume: make(chan struct{})}
	a := oamstore.NewWithLease(paused, cfg, lease)
	b := oamstore.NewWithLease(kv, cfg, lease)
	resume := sync.OnceFunc(func() { close(paused.resume) })
	t.Cleanup(resume)

	type result struct {
		w   oam.AlarmWatch
		ok  bool
		err error
	}
	done := make(chan result, 1)
	go func() {
		w, ok, err := a.Watch(ctx, "scscf", "a")
		done <- result{w, ok, err}
	}()
	select {
	case <-paused.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("a did not reach the call that it stops at within 10 s")
	}
	// a's lease runs out while it is stopped, and b takes the
	// watch.
	waitOut()
	held, ok, err := b.Watch(ctx, "scscf", "b")
	if err != nil || !ok {
		t.Fatalf("b's Watch while a is stopped: %v, %v", ok, err)
	}
	resume()
	var got result
	select {
	case got = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a's Watch did not return within 10 s of going on")
	}
	if got.err != nil || got.ok {
		t.Errorf("a's Watch after its lease ran out = %v, %v; want no watch", got.ok, got.err)
	}
	st := held.State()
	st.JournalID, st.LastNumber = "journal-1", 3
	if err := held.Save(ctx, st); err != nil {
		t.Errorf("b's Save after a went on: %v", err)
	}
	if err := held.Renew(ctx); err != nil {
		t.Errorf("b's Renew after a went on: %v", err)
	}
	if stored, err := b.AlarmWatchState(ctx, "scscf"); err != nil || stored != st {
		t.Errorf("the state is %+v, %v; want b's %+v", stored, err, st)
	}
}
