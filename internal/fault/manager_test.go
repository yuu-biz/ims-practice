package fault

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// fakeExecutor records what it is asked to do. applyErr and clearErr
// make it fail; block, when set, holds Apply until it is closed.
type fakeExecutor struct {
	mu       sync.Mutex
	calls    []string
	applyErr error
	clearErr error
	block    chan struct{}
	started  chan struct{}
}

func (e *fakeExecutor) Apply(ctx context.Context, f Fault) error {
	e.mu.Lock()
	e.calls = append(e.calls, "apply "+string(f.Kind)+" "+f.Target)
	block, started, err := e.block, e.started, e.applyErr
	e.mu.Unlock()
	if block != nil {
		close(started)
		<-block
	}
	return err
}

func (e *fakeExecutor) Clear(ctx context.Context, f Fault) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, "clear "+string(f.Kind)+" "+f.Target)
	return e.clearErr
}

func (e *fakeExecutor) Calls() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.calls...)
}

func newTestManager() (*Manager, *fakeExecutor, *fakeClock) {
	e := &fakeExecutor{}
	c := newFakeClock()
	return NewManager(Config{Executor: e, Clock: c}), e, c
}

func TestInjectAppliesAndExpires(t *testing.T) {
	m, e, c := newTestManager()
	ctx := context.Background()
	f, err := m.Inject(ctx, Request{Target: "pcscf", Kind: Loss, LossPercent: 20, Duration: Duration(30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if f.State != Active || !f.ExpiresAt.Equal(c.Now().Add(30*time.Second)) {
		t.Fatalf("fault = %+v, want active until 30 s from now", f)
	}

	c.Advance(29 * time.Second)
	m.ExpireDue(ctx)
	if got, _ := m.Get(f.ID); got.State != Active {
		t.Fatalf("state after 29 s = %s, want active", got.State)
	}
	c.Advance(time.Second)
	m.ExpireDue(ctx)
	got, _ := m.Get(f.ID)
	if got.State != Expired || !got.EndedAt.Equal(c.Now()) {
		t.Fatalf("after 30 s: %+v, want expired now", got)
	}
	want := []string{"apply loss pcscf", "clear loss pcscf"}
	if fmt.Sprint(e.Calls()) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", e.Calls(), want)
	}

	// An ended fault stays for the Lab, then goes.
	c.Advance(endedRetention)
	m.ExpireDue(ctx)
	if _, err := m.Get(f.ID); err != nil {
		t.Fatalf("fault gone %v after it ended: %v", endedRetention, err)
	}
	c.Advance(time.Second)
	m.ExpireDue(ctx)
	if _, err := m.Get(f.ID); !errors.Is(err, ErrUnknownFault) {
		t.Fatalf("Get after the retention: %v, want ErrUnknownFault", err)
	}
}

func TestDefaultDuration(t *testing.T) {
	m, _, c := newTestManager()
	f, err := m.Inject(context.Background(), Request{Target: "scscf", Kind: Pause})
	if err != nil {
		t.Fatal(err)
	}
	if time.Duration(f.Duration) != DefaultDuration || !f.ExpiresAt.Equal(c.Now().Add(DefaultDuration)) {
		t.Fatalf("duration %v, expires %v; want the default", time.Duration(f.Duration), f.ExpiresAt)
	}
}

func TestClearBeforeItsTime(t *testing.T) {
	m, e, c := newTestManager()
	ctx := context.Background()
	f, err := m.Inject(ctx, Request{Target: "hss", Kind: Stop})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	cleared, err := m.Clear(ctx, f.ID)
	if err != nil || cleared.State != Cleared {
		t.Fatalf("Clear = %+v, %v; want cleared", cleared, err)
	}
	// Clearing again, or the clock passing the expiry, does nothing
	// more.
	again, err := m.Clear(ctx, f.ID)
	if err != nil || again.State != Cleared {
		t.Fatalf("second Clear = %+v, %v", again, err)
	}
	c.Advance(DefaultDuration)
	m.ExpireDue(ctx)
	if n := len(e.Calls()); n != 2 {
		t.Fatalf("calls = %v, want one apply and one clear", e.Calls())
	}
	if _, err := m.Clear(ctx, "nope"); !errors.Is(err, ErrUnknownFault) {
		t.Fatalf("Clear of an unknown id: %v", err)
	}
}

// TestAFaultThatHappensOnceIsOverAtOnce: a crash and a recreate have
// nothing to undo, so the manager ends them as soon as the executor has
// done them. They hold no slot either: a node can be crashed while a
// loss sits on it, and twice in a row.
func TestAFaultThatHappensOnceIsOverAtOnce(t *testing.T) {
	m, e, c := newTestManager()
	ctx := context.Background()
	for _, kind := range []Kind{Crash, Recreate} {
		f, err := m.Inject(ctx, Request{Target: "scscf", Kind: kind})
		if err != nil {
			t.Fatalf("Inject %s: %v", kind, err)
		}
		if f.State != Done || !f.EndedAt.Equal(c.Now()) || !f.ExpiresAt.IsZero() || f.Duration != 0 {
			t.Errorf("%s = %+v, want done now and no duration", kind, f)
		}
		// Clearing it, and the sweep, find it over and do nothing more.
		if cleared, err := m.Clear(ctx, f.ID); err != nil || cleared.State != Done {
			t.Errorf("Clear of a %s = %+v, %v", kind, cleared, err)
		}
		c.Advance(DefaultDuration)
		m.ExpireDue(ctx)
	}
	if _, err := m.Inject(ctx, Request{Target: "scscf", Kind: Loss, LossPercent: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Inject(ctx, Request{Target: "scscf", Kind: Crash}); err != nil {
		t.Errorf("Inject a crash on a node that has a loss: %v", err)
	}
	want := []string{"apply crash scscf", "apply recreate scscf", "apply loss scscf", "apply crash scscf"}
	if fmt.Sprint(e.Calls()) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", e.Calls(), want)
	}
}

func TestConflicts(t *testing.T) {
	m, _, _ := newTestManager()
	ctx := context.Background()
	if _, err := m.Inject(ctx, Request{Target: "pcscf", Kind: Pause}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		r    Request
		want error
	}{
		// One node fault at a time on a target.
		{Request{Target: "pcscf", Kind: Stop}, ErrConflict},
		{Request{Target: "pcscf", Kind: Kill}, ErrConflict},
		// Network faults of different kinds live side by side.
		{Request{Target: "pcscf", Kind: Loss, LossPercent: 10}, nil},
		{Request{Target: "pcscf", Kind: Delay, Delay: Duration(100 * time.Millisecond)}, nil},
		{Request{Target: "pcscf", Kind: Partition, Peers: []string{"icscf"}}, nil},
		{Request{Target: "pcscf", Kind: Loss, LossPercent: 50}, ErrConflict},
		// Another target is free.
		{Request{Target: "scscf", Kind: Pause}, nil},
	} {
		_, err := m.Inject(ctx, tc.r)
		if !errors.Is(err, tc.want) && !(err == nil && tc.want == nil) {
			t.Errorf("Inject(%+v) = %v, want %v", tc.r, err, tc.want)
		}
	}
}

func TestAnEndedFaultFreesItsSlot(t *testing.T) {
	m, _, _ := newTestManager()
	ctx := context.Background()
	f, err := m.Inject(ctx, Request{Target: "pcscf", Kind: Stop})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if _, err := m.Clear(ctx, f.ID); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := m.Inject(ctx, Request{Target: "pcscf", Kind: Kill}); err != nil {
		t.Fatalf("Inject after the clear: %v", err)
	}
}

func TestApplyFailure(t *testing.T) {
	m, e, _ := newTestManager()
	e.applyErr = errors.New("Error: Specified qdisc kind is unknown.")
	f, err := m.Inject(context.Background(), Request{Target: "pcscf", Kind: Delay, Delay: Duration(200 * time.Millisecond)})
	if !errors.Is(err, ErrExecutor) {
		t.Fatalf("Inject error = %v, want ErrExecutor", err)
	}
	if f.ID == "" || f.State != Failed || f.Error != "apply: Error: Specified qdisc kind is unknown." {
		t.Fatalf("fault = %+v, want failed with the executor's words", f)
	}
	// The failed fault is listed, and does not hold the slot.
	if l := m.List(); len(l) != 1 || l[0].ID != f.ID {
		t.Fatalf("List = %+v", l)
	}
	e.applyErr = nil
	if _, err := m.Inject(context.Background(), Request{Target: "pcscf", Kind: Delay, Delay: Duration(time.Millisecond)}); err != nil {
		t.Fatalf("Inject after a failure: %v", err)
	}
}

func TestClearFailureIsNotRetried(t *testing.T) {
	m, e, c := newTestManager()
	ctx := context.Background()
	f, err := m.Inject(ctx, Request{Target: "pcscf", Kind: Pause, Duration: Duration(time.Minute)})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	e.clearErr = errors.New("container is not paused")
	c.Advance(time.Minute)
	m.ExpireDue(ctx)
	got, _ := m.Get(f.ID)
	if got.State != Failed || got.Error != "clear: container is not paused" {
		t.Fatalf("fault = %+v, want failed by the clear", got)
	}
	c.Advance(time.Minute)
	m.ExpireDue(ctx)
	if n := len(e.Calls()); n != 2 {
		t.Fatalf("calls = %v, want one clear only", e.Calls())
	}
}

func TestNoClearWhileApplying(t *testing.T) {
	e := &fakeExecutor{block: make(chan struct{}), started: make(chan struct{})}
	m := NewManager(Config{Executor: e, Clock: newFakeClock()})
	type result struct {
		fault Fault
		err   error
	}
	done := make(chan result)
	go func() {
		f, err := m.Inject(context.Background(), Request{Target: "scscf", Kind: Stop})
		done <- result{f, err}
	}()
	<-e.started
	l := m.List()
	if len(l) != 1 || l[0].State != Applying {
		t.Fatalf("List while applying = %+v", l)
	}
	if _, err := m.Clear(context.Background(), l[0].ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("Clear while applying: %v, want ErrConflict", err)
	}
	if _, err := m.Inject(context.Background(), Request{Target: "scscf", Kind: Pause}); !errors.Is(err, ErrConflict) {
		t.Fatalf("Inject in the same slot while applying: %v, want ErrConflict", err)
	}
	close(e.block)
	if r := <-done; r.err != nil || r.fault.State != Active {
		t.Fatalf("Inject = %+v, %v; want active", r.fault, r.err)
	}
}

func TestListIsOldestFirst(t *testing.T) {
	m, _, c := newTestManager()
	ctx := context.Background()
	var ids []string
	for _, target := range []string{"pcscf", "icscf", "scscf"} {
		f, err := m.Inject(ctx, Request{Target: target, Kind: Pause})
		if err != nil {
			t.Fatalf("Inject on %s: %v", target, err)
		}
		ids = append(ids, f.ID)
		c.Advance(time.Second)
	}
	l := m.List()
	for i, f := range l {
		if f.ID != ids[i] {
			t.Fatalf("List = %+v, want ids %v", l, ids)
		}
	}
}

func TestValidate(t *testing.T) {
	ms := func(n int) Duration { return Duration(time.Duration(n) * time.Millisecond) }
	for _, r := range []Request{
		{Target: "", Kind: Stop},
		{Target: "P-CSCF", Kind: Stop},
		{Target: "pcscf", Kind: "reboot"},
		{Target: "pcscf", Kind: Stop, LossPercent: 10},
		{Target: "pcscf", Kind: Delay},
		{Target: "pcscf", Kind: Delay, Delay: Duration(11 * time.Second)},
		{Target: "pcscf", Kind: Delay, Delay: ms(100), Jitter: ms(200)},
		{Target: "pcscf", Kind: Loss},
		{Target: "pcscf", Kind: Loss, LossPercent: 101},
		{Target: "pcscf", Kind: Loss, LossPercent: 10, Delay: ms(1)},
		{Target: "pcscf", Kind: Partition, Peers: []string{"pcscf"}},
		{Target: "pcscf", Kind: Partition, Peers: []string{"icscf", "icscf"}},
		{Target: "pcscf", Kind: Partition, Peers: []string{"fd00::1"}},
		{Target: "pcscf", Kind: Partition, Peers: []string{"I-CSCF"}},
		{Target: "pcscf", Kind: Pause, Peers: []string{"icscf"}},
		{Target: "pcscf", Kind: Pause, Duration: Duration(time.Millisecond)},
		{Target: "pcscf", Kind: Pause, Duration: Duration(2 * time.Hour)},
		{Target: "pcscf:0", Kind: Pause},
		{Target: "pcscf:a", Kind: Pause},
		{Target: "scscf-*", Kind: Kill},
		{Target: "ibcf-a", Kind: Kill, Network: "nni"},
		{Target: "ibcf-a", Kind: Partition, Network: "NNI"},
		{Target: "valkey", Kind: Partition, Peers: []string{"scscf-[12]"}},
		{Target: "valkey", Kind: Partition, Peers: []string{"pcscf:x"}},
		// A fault that happens once takes no duration.
		{Target: "scscf", Kind: Crash, Duration: Duration(time.Minute)},
		{Target: "scscf", Kind: Recreate, Duration: Duration(time.Minute)},
		{Target: "scscf", Kind: Crash, LossPercent: 10},
		{Target: "scscf", Kind: Recreate, Network: "nni"},
	} {
		if err := r.validate(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("validate(%+v) = %v, want ErrInvalidRequest", r, err)
		}
	}
	for _, r := range []Request{
		{Target: "pcscf", Kind: Delay, Delay: ms(200), Jitter: ms(20)},
		{Target: "pcscf", Kind: Loss, LossPercent: 100},
		{Target: "pcscf", Kind: Partition},
		{Target: "pcscf", Kind: Partition, Peers: []string{"icscf", "172.28.0.30"}},
		{Target: "phone1", Kind: Kill, Duration: Duration(time.Hour)},
		{Target: "scscf:2", Kind: Pause},
		{Target: "ibcf-a", Kind: Partition, Network: "nni"},
		{Target: "trgw-a", Kind: Loss, LossPercent: 10, Network: "nni"},
		{Target: "valkey", Kind: Partition, Peers: []string{"scscf-*", "pcscf:2"}},
		{Target: "scscf", Kind: Crash},
		{Target: "scscf-1", Kind: Recreate},
	} {
		if err := r.validate(); err != nil {
			t.Errorf("validate(%+v) = %v", r, err)
		}
	}
}
