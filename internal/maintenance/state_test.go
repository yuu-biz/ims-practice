package maintenance

import (
	"errors"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// drainFixture is a role with a session count that the test sets, and
// the transitions it has been told of.
type drainFixture struct {
	users atomic.Int64
	mu    sync.Mutex
	told  []transition
	clock *fakeClock
	fn    *ManagedFunction
}

func newDrainFixture(t *testing.T, users int) *drainFixture {
	t.Helper()
	d := &drainFixture{clock: newFakeClock()}
	d.users.Store(int64(users))
	d.fn = newManagedFunction(Function{
		Role:  "pcscf",
		Users: func() int { return int(d.users.Load()) },
		AdministrativeStateChanged: func(from, to AdministrativeState) {
			d.mu.Lock()
			d.told = append(d.told, transition{from, to})
			d.mu.Unlock()
		},
	}, d.clock, discardLog())
	return d
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func (d *drainFixture) transitions() []transition {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.told)
}

func (d *drainFixture) endSession() {
	d.users.Add(-1)
	d.fn.UserQuit()
}

func wantState(t *testing.T, fn *ManagedFunction, want AdministrativeState) {
	t.Helper()
	if got := fn.AdministrativeState(); got != want {
		t.Fatalf("administrative state = %s, want %s", got, want)
	}
}

func TestShutDownWaitsForTheLastUserToQuit(t *testing.T) {
	d := newDrainFixture(t, 2)
	wantState(t, d.fn, Unlocked)
	if err := d.fn.ShutDown(0); err != nil {
		t.Fatal(err)
	}
	wantState(t, d.fn, ShuttingDown)
	d.endSession()
	wantState(t, d.fn, ShuttingDown)
	d.endSession()
	wantState(t, d.fn, Locked)
	want := []transition{{Unlocked, ShuttingDown}, {ShuttingDown, Locked}}
	if got := d.transitions(); !slices.Equal(got, want) {
		t.Errorf("transitions = %v, want %v", got, want)
	}
}

// X.731 7.1.3.3: a shut down of a resource with no users locks it.
func TestShutDownWithoutUsersLocksAtOnce(t *testing.T) {
	d := newDrainFixture(t, 0)
	if err := d.fn.ShutDown(time.Minute); err != nil {
		t.Fatal(err)
	}
	wantState(t, d.fn, Locked)
	if got, want := d.transitions(), []transition{{Unlocked, Locked}}; !slices.Equal(got, want) {
		t.Errorf("transitions = %v, want %v", got, want)
	}
	if n := d.clock.pendingTimers(); n != 0 {
		t.Errorf("%d timers left", n)
	}
}

func TestShutDownOfARoleThatCountsNoUsersLocksAtOnce(t *testing.T) {
	fn := newManagedFunction(Function{Role: "icscf"}, newFakeClock(), discardLog())
	if err := fn.ShutDown(0); err != nil {
		t.Fatal(err)
	}
	wantState(t, fn, Locked)
	if _, counted := fn.Users(); counted {
		t.Error("Users counted for a role without a Users callback")
	}
}

func TestShutDownDeadlineLocksWithUsersLeft(t *testing.T) {
	d := newDrainFixture(t, 1)
	if err := d.fn.ShutDown(5 * time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, want := d.fn.ShutdownDeadline(), d.clock.Now().Add(5*time.Minute); !got.Equal(want) {
		t.Errorf("deadline = %v, want %v", got, want)
	}
	d.clock.Advance(4*time.Minute + 59*time.Second)
	wantState(t, d.fn, ShuttingDown)
	d.clock.Advance(time.Second)
	wantState(t, d.fn, Locked)
	if !d.fn.ShutdownDeadline().IsZero() {
		t.Error("deadline kept after LOCKED")
	}
	if users, _ := d.fn.Users(); users != 1 {
		t.Errorf("users = %d, want the 1 that the deadline cut", users)
	}
}

func TestUnlockCancelsTheShutdown(t *testing.T) {
	d := newDrainFixture(t, 1)
	if err := d.fn.ShutDown(time.Minute); err != nil {
		t.Fatal(err)
	}
	d.fn.Unlock()
	wantState(t, d.fn, Unlocked)
	if n := d.clock.pendingTimers(); n != 0 {
		t.Errorf("%d timers left after unlock", n)
	}
	d.clock.Advance(time.Hour)
	wantState(t, d.fn, Unlocked)
	// A user that quits while UNLOCKED changes nothing (X.731 7.1.3.4).
	d.endSession()
	wantState(t, d.fn, Unlocked)
	want := []transition{{Unlocked, ShuttingDown}, {ShuttingDown, Unlocked}}
	if got := d.transitions(); !slices.Equal(got, want) {
		t.Errorf("transitions = %v, want %v", got, want)
	}
}

// A timer of an earlier shut down that fires after a later one began,
// because Stop came too late, must not cut the later one short.
func TestALateTimerOfAnEarlierShutdownChangesNothing(t *testing.T) {
	d := newDrainFixture(t, 1)
	if err := d.fn.ShutDown(time.Minute); err != nil {
		t.Fatal(err)
	}
	d.fn.Unlock()
	if err := d.fn.ShutDown(time.Hour); err != nil {
		t.Fatal(err)
	}
	d.fn.deadlinePassed(1)
	wantState(t, d.fn, ShuttingDown)
	d.fn.deadlinePassed(2)
	wantState(t, d.fn, Locked)
}

func TestShutDownOfALockedFunctionIsRefused(t *testing.T) {
	d := newDrainFixture(t, 1)
	d.fn.Lock()
	if err := d.fn.ShutDown(0); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("ShutDown of LOCKED: %v, want ErrInvalidTransition", err)
	}
	wantState(t, d.fn, Locked)
}

func TestRepeatedEventsChangeNothing(t *testing.T) {
	d := newDrainFixture(t, 1)
	d.fn.Unlock()
	if err := d.fn.ShutDown(time.Minute); err != nil {
		t.Fatal(err)
	}
	deadline := d.fn.ShutdownDeadline()
	d.clock.Advance(30 * time.Second)
	if err := d.fn.ShutDown(time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := d.fn.ShutdownDeadline(); !got.Equal(deadline) {
		t.Errorf("second shut down moved the deadline to %v from %v", got, deadline)
	}
	d.fn.Lock()
	d.fn.Lock()
	want := []transition{{Unlocked, ShuttingDown}, {ShuttingDown, Locked}}
	if got := d.transitions(); !slices.Equal(got, want) {
		t.Errorf("transitions = %v, want %v", got, want)
	}
}

func TestLockAndUnlock(t *testing.T) {
	d := newDrainFixture(t, 3)
	d.fn.Lock()
	wantState(t, d.fn, Locked)
	d.fn.Unlock()
	wantState(t, d.fn, Unlocked)
	if err := d.fn.ShutDown(0); err != nil {
		t.Fatal(err)
	}
	d.fn.Lock()
	wantState(t, d.fn, Locked)
	want := []transition{{Unlocked, Locked}, {Locked, Unlocked}, {Unlocked, ShuttingDown}, {ShuttingDown, Locked}}
	if got := d.transitions(); !slices.Equal(got, want) {
		t.Errorf("transitions = %v, want %v", got, want)
	}
}

func TestShutDownRefusesANegativeTimeout(t *testing.T) {
	d := newDrainFixture(t, 1)
	if err := d.fn.ShutDown(-time.Second); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("ShutDown(-1s): %v, want ErrInvalidRequest", err)
	}
	wantState(t, d.fn, Unlocked)
}

// The callback runs without the lock of the function, so a role that
// ends its sessions when it is locked can report each one from there,
// and may even change the state again.
func TestTheCallbackMayCallBack(t *testing.T) {
	clock := newFakeClock()
	var users atomic.Int64
	users.Store(2)
	var told []transition
	var fn *ManagedFunction
	fn = newManagedFunction(Function{
		Role:  "scscf",
		Users: func() int { return int(users.Load()) },
		AdministrativeStateChanged: func(from, to AdministrativeState) {
			told = append(told, transition{from, to})
			if to == Locked && users.Load() > 0 {
				for users.Load() > 0 {
					users.Add(-1)
					fn.UserQuit()
				}
				fn.Unlock()
				if got := fn.AdministrativeState(); got != Unlocked {
					t.Errorf("state in the callback after Unlock = %s", got)
				}
			}
		},
	}, clock, discardLog())
	if err := fn.ShutDown(time.Second); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Second)
	want := []transition{{Unlocked, ShuttingDown}, {ShuttingDown, Locked}, {Locked, Unlocked}}
	if !slices.Equal(told, want) {
		t.Errorf("transitions = %v, want %v", told, want)
	}
	wantState(t, fn, Unlocked)
}

// Under -race: the role reads the state while operators and sessions
// change it. The role must be told of every transition in order, so
// that each one starts where the one before ended.
func TestConcurrentEventsAreToldInOrder(t *testing.T) {
	d := newDrainFixture(t, 0)
	d.users.Store(1000)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for j := range 200 {
				switch (i + j) % 4 {
				case 0:
					_ = d.fn.ShutDown(time.Duration(j) * time.Millisecond)
				case 1:
					d.fn.Unlock()
				case 2:
					d.fn.Lock()
				case 3:
					if d.users.Add(-1) >= 0 {
						d.fn.UserQuit()
					}
				}
				_ = d.fn.AdministrativeState()
				_ = d.fn.ShutdownDeadline()
			}
		})
	}
	wg.Go(func() {
		for range 100 {
			d.clock.Advance(time.Millisecond)
		}
	})
	wg.Wait()
	told := d.transitions()
	at := Unlocked
	for i, tr := range told {
		if tr.from != at || tr.from == tr.to {
			t.Fatalf("transition %d is %v after the state %s", i, tr, at)
		}
		at = tr.to
	}
	wantState(t, d.fn, at)
}
