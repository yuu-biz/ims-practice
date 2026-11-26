package maintenance

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// AdministrativeState is the permission to use a function, which the
// operator sets (TS 28.622 5.3.1 has the values; its attribute
// definitions in 4.4.1 say the MnS consumer sets it). The events that
// change it and the meaning of each value come from ITU-T X.731 7.1.3
// and 8.1.1.3, on which TS 28.622 builds.
//
// SHUTTINGDOWN is the drain: the function serves the sessions it has,
// and takes no new ones, so that a node can be taken out of service
// without cutting the calls in progress.
type AdministrativeState string

const (
	// Unlocked: the function may serve its users (X.731 8.1.1.3).
	Unlocked AdministrativeState = "UNLOCKED"
	// ShuttingDown: the function may serve only the users it already
	// has (X.731 8.1.1.3).
	ShuttingDown AdministrativeState = "SHUTTINGDOWN"
	// Locked: the function may serve no one (X.731 8.1.1.3).
	Locked AdministrativeState = "LOCKED"
)

// OperationalState says whether a function can work (TS 28.622 5.3.3).
// The function itself sets it, and it is read-only to the operator
// (TS 28.622 4.4.1, operationalState).
type OperationalState string

const (
	Enabled  OperationalState = "ENABLED"
	Disabled OperationalState = "DISABLED"
)

// ErrInvalidTransition is the answer to an event that X.731 7.1.3 does
// not allow in the present administrative state: a shut down of a
// locked function.
var ErrInvalidTransition = errors.New("invalid administrative state transition")

// Clock is the time as the drain sees it. A test supplies one that it
// moves by hand.
type Clock interface {
	Now() time.Time
	// AfterFunc calls f in its own goroutine once d has passed, as
	// time.AfterFunc does.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a call that AfterFunc scheduled. *time.Timer is one.
type Timer interface {
	Stop() bool
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Function is what a role of the node supplies to the maintenance API:
// its name and the callbacks that give what only the role knows. Only
// Role is required.
type Function struct {
	// Role is the name of the role, as the configuration file has it,
	// such as "pcscf". It is a path segment of the API.
	Role string
	// OperationalState says whether the role can work now, for example
	// DISABLED while it has no Diameter peer. Without it the role is
	// ENABLED while the node runs.
	OperationalState func() OperationalState
	// Users counts the sessions or registrations that the role still
	// serves, the users of X.731 7.1.3. A SHUTTINGDOWN function becomes
	// LOCKED when it counts none. It counts those of this process only,
	// from a counter the role keeps, not by walking a store, which could
	// hold millions of users.
	// Without it the role has no users, and a shut down locks it at
	// once.
	Users func() int
	// AdministrativeStateChanged tells the role of each transition, in
	// order: to SHUTTINGDOWN or LOCKED it stops taking new registrations
	// and sessions, to LOCKED it may also end the ones it has, and to
	// UNLOCKED it takes new ones again. It may run in the goroutine of a
	// later transition, and must not wait for a session to end. Without
	// it the role reads ManagedFunction.AdministrativeState.
	AdministrativeStateChanged func(from, to AdministrativeState)
}

// ManagedFunction is the administrative state of one role of the node,
// the ManagedFunction of TS 28.622 4.3.4. The role reads the state with
// AdministrativeState before it takes a new registration or session, and
// tells of a session that ended with UserQuit. It is safe for
// concurrent use.
//
// The administrative state belongs to this process: another node of the
// same role has its own, and a restarted node starts UNLOCKED.
type ManagedFunction struct {
	role      string
	callbacks Function
	clock     Clock
	log       *slog.Logger

	// state is read without mu, so that the role can look at it on
	// every request.
	state atomic.Pointer[AdministrativeState]

	mu sync.Mutex
	// deadline is when a SHUTTINGDOWN function becomes LOCKED even
	// though it still has users; zero for no deadline.
	deadline time.Time
	timer    Timer
	// shutdowns counts the shut down events, so that the timer of an
	// earlier one that fires late changes nothing.
	shutdowns uint64
	// pending holds the transitions that the role has not been told of
	// yet, and dispatching says that a goroutine is telling it.
	pending     []transition
	dispatching bool
}

type transition struct{ from, to AdministrativeState }

func newManagedFunction(f Function, clock Clock, log *slog.Logger) *ManagedFunction {
	m := &ManagedFunction{
		role:      f.Role,
		callbacks: f,
		clock:     clock,
		log:       log.With("role", f.Role),
	}
	initial := Unlocked
	m.state.Store(&initial)
	return m
}

// Role is the name of the role, as the configuration file has it.
func (m *ManagedFunction) Role() string { return m.role }

// AdministrativeState is the present administrative state.
func (m *ManagedFunction) AdministrativeState() AdministrativeState { return *m.state.Load() }

// ShutdownDeadline is when a SHUTTINGDOWN function becomes LOCKED even
// with users left, or the zero time when there is no deadline or the
// function is not shutting down.
func (m *ManagedFunction) ShutdownDeadline() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.deadline
}

// Users is how many users the function has, as the role counts them,
// and false when the role counts none.
func (m *ManagedFunction) Users() (int, bool) {
	if m.callbacks.Users == nil {
		return 0, false
	}
	return m.callbacks.Users(), true
}

// ShutDown is the shut down event of X.731 7.1.3.3, allowed only on an
// UNLOCKED function. A function with users becomes SHUTTINGDOWN and
// keeps serving them; one with none becomes LOCKED at once. A timeout
// greater than zero bounds the shutting down: when it has passed, the
// function becomes LOCKED with the users it still has, which the role
// learns from AdministrativeStateChanged. Shutting down a function that
// is SHUTTINGDOWN already changes nothing, not even its deadline.
func (m *ManagedFunction) ShutDown(timeout time.Duration) error {
	if timeout < 0 {
		return fmt.Errorf("%w: negative timeout %s", ErrInvalidRequest, timeout)
	}
	// The users are counted before mu is taken, so that Users may take
	// the role's locks. A session that the role lets in between the
	// count and the transition is served as one that the deadline cut.
	// A user that quits in between is counted again below.
	users := 0
	if m.callbacks.Users != nil {
		users = m.callbacks.Users()
	}
	m.mu.Lock()
	switch m.AdministrativeState() {
	case ShuttingDown:
		m.mu.Unlock()
		return nil
	case Locked:
		m.mu.Unlock()
		return fmt.Errorf("%w: shut down applies only to an UNLOCKED function, and %s is LOCKED (X.731 7.1.3.3)", ErrInvalidTransition, m.role)
	}
	m.shutdowns++
	if users == 0 {
		m.enter(Locked)
		m.dispatchAndUnlock()
		return nil
	}
	m.enter(ShuttingDown)
	if timeout > 0 {
		m.deadline = m.clock.Now().Add(timeout)
		shutdown := m.shutdowns
		m.timer = m.clock.AfterFunc(timeout, func() { m.deadlinePassed(shutdown) })
	}
	m.dispatchAndUnlock()
	// The last user may have quit after the count and before the
	// function was SHUTTINGDOWN. Its UserQuit found the function
	// UNLOCKED and changed nothing, and no user is left to quit again,
	// so the user quit of X.731 7.1.3.4 is made here. A user that quits
	// from now on finds the function SHUTTINGDOWN.
	m.UserQuit()
	return nil
}

// Unlock is the unlock event of X.731 7.1.3.1: a LOCKED or SHUTTINGDOWN
// function becomes UNLOCKED and takes new users again. Unlocking an
// UNLOCKED function changes nothing.
func (m *ManagedFunction) Unlock() {
	m.mu.Lock()
	if m.AdministrativeState() != Unlocked {
		m.enter(Unlocked)
	}
	m.dispatchAndUnlock()
}

// Lock is the lock event of X.731 7.1.3.2: an UNLOCKED or SHUTTINGDOWN
// function becomes LOCKED at once, with the users it has. Locking a
// LOCKED function changes nothing.
func (m *ManagedFunction) Lock() {
	m.mu.Lock()
	if m.AdministrativeState() != Locked {
		m.enter(Locked)
	}
	m.dispatchAndUnlock()
}

// UserQuit is the user quit event of X.731 7.1.3.4: the role calls it
// when a session or registration of the function has ended. When the
// function is SHUTTINGDOWN and Users now counts none, it becomes
// LOCKED. In another state it changes nothing.
//
// UserQuit calls Users in the caller's goroutine, so the caller must not
// hold a lock that Users takes.
func (m *ManagedFunction) UserQuit() {
	if m.callbacks.Users == nil || m.AdministrativeState() != ShuttingDown {
		return
	}
	users := m.callbacks.Users()
	m.mu.Lock()
	if users == 0 && m.AdministrativeState() == ShuttingDown {
		m.enter(Locked)
	}
	m.dispatchAndUnlock()
}

// deadlinePassed makes a function that is still in the shutting down of
// the given number LOCKED. X.731 has no deadline: the operator would
// send a lock (7.1.3.2), which this does on the operator's behalf.
func (m *ManagedFunction) deadlinePassed(shutdown uint64) {
	m.mu.Lock()
	if m.shutdowns == shutdown && m.AdministrativeState() == ShuttingDown {
		m.log.Info("shutdown deadline passed", "deadline", m.deadline)
		m.enter(Locked)
	}
	m.dispatchAndUnlock()
}

// enter changes the state, with mu held, and queues the transition for
// the role.
func (m *ManagedFunction) enter(to AdministrativeState) {
	from := m.AdministrativeState()
	m.state.Store(&to)
	if to != ShuttingDown {
		if m.timer != nil {
			m.timer.Stop()
			m.timer = nil
		}
		m.deadline = time.Time{}
	}
	m.log.Info("administrative state changed", "from", string(from), "to", string(to))
	m.pending = append(m.pending, transition{from, to})
}

// dispatchAndUnlock tells the role of the queued transitions, in the
// order they were made, and releases mu. The callback runs without mu,
// so it may call UserQuit, AdministrativeState, or even Unlock; a
// transition it makes is told after it returns. Only one goroutine
// tells at a time: one that finds another telling leaves its transition
// in the queue for that one.
func (m *ManagedFunction) dispatchAndUnlock() {
	if m.callbacks.AdministrativeStateChanged == nil {
		m.pending = nil
	}
	if m.dispatching {
		m.mu.Unlock()
		return
	}
	m.dispatching = true
	for len(m.pending) > 0 {
		t := m.pending[0]
		m.pending = m.pending[1:]
		m.mu.Unlock()
		m.callbacks.AdministrativeStateChanged(t.from, t.to)
		m.mu.Lock()
	}
	m.dispatching = false
	m.mu.Unlock()
}
