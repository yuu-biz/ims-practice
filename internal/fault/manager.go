package fault

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Executor does what a fault asks for on the container runtime. Apply
// and Clear may take seconds (docker stop waits for the node); Manager
// does not hold its lock while they run.
//
// Docker is the executor for Compose. Kubernetes needs
// another one: there the runtime is the API server, and a pod's
// namespace is reached through an ephemeral container.
type Executor interface {
	// Resolve names the one container that a target means, with the
	// same name for every way of writing the target: "pcscf" and
	// "pcscf:1" are one container when the service has one. Manager
	// tells by it whether two faults meet.
	Resolve(ctx context.Context, target string) (string, error)
	Apply(ctx context.Context, f Fault) error
	// Clear undoes Apply. A fault that is already gone, because the
	// target restarted with a new network namespace, counts as
	// cleared.
	Clear(ctx context.Context, f Fault) error
}

// Clock tells the time; tests replace it.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// ErrUnknownFault is the answer for an id that Manager does not hold.
var ErrUnknownFault = errors.New("no such fault")

// ErrConflict is the answer to a request that would meet a fault that
// is not over, or to a clear while the fault is being applied or
// cleared.
var ErrConflict = errors.New("conflict")

// ErrExecutor wraps what the executor said when it could not apply or
// clear a fault.
var ErrExecutor = errors.New("executor failed")

// endedRetention is how long an ended fault stays in the list, so that
// the Lab can still show what happened.
const endedRetention = time.Hour

// Manager holds the faults, applies and clears them through an
// Executor, and clears each one when its duration is up.
type Manager struct {
	exec  Executor
	clock Clock
	log   *slog.Logger

	mu     sync.Mutex
	faults map[string]*Fault
}

// Config is what NewManager needs. Clock and Logger may be nil.
type Config struct {
	Executor Executor
	Clock    Clock
	Logger   *slog.Logger
}

func NewManager(c Config) *Manager {
	m := &Manager{exec: c.Executor, clock: c.Clock, log: c.Logger, faults: map[string]*Fault{}}
	if m.clock == nil {
		m.clock = systemClock{}
	}
	if m.log == nil {
		m.log = slog.New(slog.DiscardHandler)
	}
	return m
}

// Inject applies a fault. When the executor fails, the fault stays in
// the list in state Failed, and the error wraps ErrExecutor.
func (m *Manager) Inject(ctx context.Context, r Request) (Fault, error) {
	if err := r.validate(); err != nil {
		return Fault{}, err
	}
	// Outside the lock: the executor asks the container runtime. A
	// target that names no container fails the fault like an Apply.
	container, err := m.exec.Resolve(ctx, r.Target)

	m.mu.Lock()
	now := m.clock.Now()
	f := &Fault{
		ID:        m.newID(),
		Request:   r,
		State:     Applying,
		CreatedAt: now,
		container: container,
	}
	if r.Kind.lasting() {
		f.ExpiresAt = now.Add(time.Duration(r.Duration))
	}
	if err == nil {
		for _, other := range m.faults {
			if slot := f.slot(); slot != "" && !other.State.ended() && other.slot() == slot {
				m.mu.Unlock()
				return Fault{}, fmt.Errorf("%w: fault %s (%s) on %s is not over", ErrConflict, other.ID, other.Kind, other.Target)
			}
		}
	}
	m.faults[f.ID] = f
	applying := *f
	m.mu.Unlock()

	if err == nil {
		args := []any{"id", f.ID, "target", r.Target, "kind", r.Kind, "event", "apply"}
		if r.Kind.lasting() {
			args = append(args, "expires_at", applying.ExpiresAt)
		}
		m.log.Info("fault", args...)
		err = m.exec.Apply(ctx, applying)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.end(f, Failed, "apply: "+err.Error())
		m.log.Warn("fault", "id", f.ID, "target", r.Target, "kind", r.Kind, "event", "apply failed", "error", err.Error())
		return *f, fmt.Errorf("%w: %v", ErrExecutor, err)
	}
	if !r.Kind.lasting() {
		// The node is already on its way back, or has been replaced:
		// nothing holds it, and nothing is left to clear.
		m.end(f, Done, "")
		return *f, nil
	}
	f.State = Active
	return *f, nil
}

// Clear ends a fault before its time. A fault that has ended already
// is returned as it is.
func (m *Manager) Clear(ctx context.Context, id string) (Fault, error) {
	return m.clear(ctx, id, Cleared)
}

func (m *Manager) clear(ctx context.Context, id string, end State) (Fault, error) {
	m.mu.Lock()
	f, ok := m.faults[id]
	if !ok {
		m.mu.Unlock()
		return Fault{}, fmt.Errorf("%w: %s", ErrUnknownFault, id)
	}
	switch f.State {
	case Applying, Clearing:
		state := f.State
		m.mu.Unlock()
		return Fault{}, fmt.Errorf("%w: fault %s is %s", ErrConflict, id, state)
	case Active:
	default:
		out := *f
		m.mu.Unlock()
		return out, nil
	}
	f.State = Clearing
	clearing := *f
	m.mu.Unlock()

	m.log.Info("fault", "id", id, "target", f.Target, "kind", f.Kind, "event", "clear", "reason", string(end))
	err := m.exec.Clear(ctx, clearing)

	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		// The fault may still be in place; Failed tells the learner to
		// look (deploy/chaos/fault.sh heal), and expiry does not try
		// again and again.
		m.end(f, Failed, "clear: "+err.Error())
		m.log.Warn("fault", "id", id, "target", f.Target, "kind", f.Kind, "event", "clear failed", "error", err.Error())
		return *f, fmt.Errorf("%w: %v", ErrExecutor, err)
	}
	m.end(f, end, "")
	return *f, nil
}

func (m *Manager) end(f *Fault, s State, reason string) {
	f.State = s
	f.Error = reason
	f.EndedAt = m.clock.Now()
}

// Get returns one fault.
func (m *Manager) Get(id string) (Fault, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.faults[id]
	if !ok {
		return Fault{}, fmt.Errorf("%w: %s", ErrUnknownFault, id)
	}
	return *f, nil
}

// List returns the faults, the oldest first.
func (m *Manager) List() []Fault {
	m.mu.Lock()
	out := make([]Fault, 0, len(m.faults))
	for _, f := range m.faults {
		out = append(out, *f)
	}
	m.mu.Unlock()
	slices.SortFunc(out, func(a, b Fault) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.ID, b.ID))
	})
	return out
}

// ExpireDue clears the active faults whose time is up, and forgets the
// ones that ended more than endedRetention ago.
func (m *Manager) ExpireDue(ctx context.Context) {
	now := m.clock.Now()
	var due []string
	m.mu.Lock()
	for id, f := range m.faults {
		switch {
		case f.State == Active && !now.Before(f.ExpiresAt):
			due = append(due, id)
		case f.State.ended() && now.Sub(f.EndedAt) > endedRetention:
			delete(m.faults, id)
		}
	}
	m.mu.Unlock()
	for _, id := range due {
		// A request may have cleared it since; clear then answers
		// with the ended fault and does nothing.
		m.clear(ctx, id, Expired)
	}
}

// Run calls ExpireDue every interval until ctx ends.
func (m *Manager) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.ExpireDue(ctx)
		}
	}
}

// newID returns 8 random hexadecimal digits. The id names the
// nftables table of the fault, so it holds only what a table name may.
func (m *Manager) newID() string {
	for {
		var b [4]byte
		rand.Read(b[:])
		id := hex.EncodeToString(b[:])
		if _, taken := m.faults[id]; !taken {
			return id
		}
	}
}
