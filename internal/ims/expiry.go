package ims

import (
	"container/heap"
	"context"
	"log/slog"
	"sync"
	"time"
)

// This file holds how a role handles the expiries that need an action
// when their time comes: those whose item cannot simply go away by
// itself, because its end sends a request or a response. The S-CSCF's
// reg-await-auth and the end of a registration are two; an item that
// only has to be forgotten needs none of this, and the Store drops it at
// its time instead.
//
// A role keeps the items of one kind of expiry in a time index of its
// own, named after the kind, and the Store hands out each expiry that is
// due to one instance at a time (ClaimDue). Every instance runs one
// Sweep, and each round of it claims what is due. No expiry runs on a
// timer of the process, so an instance that is replaced takes none with
// it: the expiries of the user it was serving are in the store, where
// the next round of any instance finds them.
//
// An expiry is handled once. The instance that claims it reads the item
// under the lock of its user, checks that it has really expired, removes
// it, and then does what its end calls for. Should it stop half way, its
// claim runs out and another instance claims the item again; that one
// finds the item gone, or no longer expired, and leaves it alone.
//
// DueIndex is the same index in the memory of one process, for a Store
// that keeps the state of the role there.

// ExpiryBatch bounds how many expiries of one kind an instance claims in
// one round, so that it never holds more claims at once than it can
// handle within a lease. What is left over is due again at the next
// round.
const ExpiryBatch = 100

// Expiry is one expiry that an instance has claimed, as its role names
// one: which item is due, and until when this instance alone acts on it.
type Expiry interface {
	// ClaimedUntil is when the claim runs out, after which another
	// instance may claim the same item.
	ClaimedUntil() time.Time
	// LogFields name the expiry in the log of a failure: its kind and
	// the item it is of, as slog takes them.
	LogFields() []any
}

// Sweep is the sweep of one instance of a role. The role gives it the
// kinds of expiry it has and the three operations on one: claiming the
// expiries of a kind, handling one, and marking it done.
type Sweep[E Expiry] struct {
	// Kinds are the kinds of expiry that one round goes through, each
	// with a time index of its own.
	Kinds []string
	// Interval is how often this instance claims what is due, and so how
	// late an expiry may be handled.
	Interval time.Duration
	// Lease is how long a claimed expiry is this instance's alone. It is
	// longer than handling one takes, a round trip to another node
	// included.
	Lease time.Duration
	// Claim claims the expiries of kind that are due at now, at most
	// limit of them, each until now plus lease. The store of the role
	// gives them out, so that each of them goes to one instance at a
	// time.
	Claim func(ctx context.Context, kind string, now time.Time, lease time.Duration, limit int) ([]E, error)
	// Handle does what the expiry calls for. It returns the failure of
	// the store, or of a lock in it, that left the item as it was: no
	// Done then follows, and an instance claims the expiry again once
	// the claim has run out.
	Handle func(ctx context.Context, e E) error
	// Done takes the item of a handled expiry out of its index.
	Done func(ctx context.Context, e E) error
	Log  *slog.Logger
}

// Start runs the sweep of this instance and returns the function that
// stops it. That function ends the context of the handlers still at work
// and waits for them, so that nothing of this instance is left running
// when it returns. An expiry that this instance had claimed and not
// handled is handled by another instance once the claim runs out.
//
// The first round comes after one Interval, by when the role has its
// stack and its connections and can send what an expiry calls for.
func (s Sweep[E]) Start() (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	var running sync.WaitGroup
	running.Go(func() { s.run(ctx, &running) })
	return func() {
		cancel()
		running.Wait()
	}
}

// run claims what is due every Interval, until ctx ends.
func (s Sweep[E]) run(ctx context.Context, running *sync.WaitGroup) {
	tick := time.NewTicker(s.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.round(ctx, running)
	}
}

// round is one round: it claims the expiries of each kind that are due
// and hands each of them to a goroutine of its own, which marks it done
// itself. The round does not wait for those goroutines, and the next one
// comes on time: a handler waits for the lock of its user and for an
// answer from another node, and one user holds up neither another user
// nor the claiming. A handler keeps its claim while it works, so the
// rounds that follow do not claim the same expiry again.
func (s Sweep[E]) round(ctx context.Context, running *sync.WaitGroup) {
	for _, kind := range s.Kinds {
		due, err := s.Claim(ctx, kind, time.Now(), s.Lease, ExpiryBatch)
		if err != nil {
			s.Log.Warn("expiries not claimed: the store failed; the next sweep claims them again",
				"kind", kind, "error", err.Error())
			continue
		}
		for _, e := range due {
			running.Go(func() { s.handle(ctx, e) })
		}
	}
}

// handle handles one claimed expiry and marks it done, which takes its
// item out of the index of its kind.
//
// The handler has until the claim runs out, and no longer: after that
// the item is there for any instance to claim, and a lock or an answer
// that this one still waited for would have two of them handling the
// same expiry.
func (s Sweep[E]) handle(parent context.Context, e E) {
	ctx, cancel := context.WithDeadline(parent, e.ClaimedUntil())
	defer cancel()
	if err := s.Handle(ctx, e); err != nil {
		s.Log.Warn("expiry not handled: an instance claims it again once the claim runs out",
			append(e.LogFields(), "error", err.Error())...)
		return
	}
	if err := s.Done(ctx, e); err != nil {
		s.Log.Warn("a handled expiry stays in the store: the store failed; an instance claims it again, and finds nothing left to do",
			append(e.LogFields(), "error", err.Error())...)
	}
}

// Due is one member of a DueIndex that a caller has claimed.
type Due[M comparable] struct {
	Member M
	// Until is when the claim runs out. It also tells this claim from a
	// later one on the same member, so that the late Done of a caller
	// that was held up takes nothing away from the one that has the
	// member now.
	Until time.Time
}

// DueIndex is the time index of one kind of expiry in the memory of one
// process: the members ordered by the time they are due, earliest
// first. The ones that are due are at the front, so that finding them
// takes no walk over the users, as the ordered set of a store outside
// the process takes none. A member names the item of one expiry, as the
// member of a time index names it there.
//
// A DueIndex is not locked: the Store that holds it is, around the item
// and its index together.
type DueIndex[M comparable] struct {
	heap    dueHeap[M]
	entries map[M]*dueEntry[M]
}

func NewDueIndex[M comparable]() *DueIndex[M] {
	return &DueIndex[M]{entries: map[M]*dueEntry[M]{}}
}

type dueEntry[M comparable] struct {
	member M
	at     time.Time
	// position is the place in the heap (container/heap).
	position int
}

// Set puts member in the index, due at at. A member that is there moves,
// and a claim on it ends.
func (ix *DueIndex[M]) Set(member M, at time.Time) {
	if e, ok := ix.entries[member]; ok {
		e.at = at
		heap.Fix(&ix.heap, e.position)
		return
	}
	e := &dueEntry[M]{member: member, at: at}
	ix.entries[member] = e
	heap.Push(&ix.heap, e)
}

// Remove takes member out of the index, claimed or not.
func (ix *DueIndex[M]) Remove(member M) {
	if e, ok := ix.entries[member]; ok {
		heap.Remove(&ix.heap, e.position)
		delete(ix.entries, member)
	}
}

// ClaimDue claims at most limit members that are due at now, the ones
// due longest first, each until now plus lease. While a member is
// claimed it is not due, and no other ClaimDue gives it out; when its
// claim runs out without RemoveClaimed, it is due again, for whichever
// caller asks next.
func (ix *DueIndex[M]) ClaimDue(now time.Time, lease time.Duration, limit int) []Due[M] {
	until := now.Add(lease)
	var claimed []Due[M]
	// A claimed member moves to the end of its claim, after every member
	// that is due, so that the front holds the next one each time.
	for len(claimed) < limit {
		if len(ix.heap) == 0 || ix.heap[0].at.After(now) {
			break
		}
		member := ix.heap[0].member
		claimed = append(claimed, Due[M]{Member: member, Until: until})
		ix.Set(member, until)
	}
	return claimed
}

// RemoveClaimed removes member if its claim still ends at until. A claim
// that another caller has taken over, or whose member was rescheduled or
// removed, leaves the index as it is.
func (ix *DueIndex[M]) RemoveClaimed(member M, until time.Time) {
	if e, ok := ix.entries[member]; ok && e.at.Equal(until) {
		ix.Remove(member)
	}
}

type dueHeap[M comparable] []*dueEntry[M]

func (h dueHeap[M]) Len() int           { return len(h) }
func (h dueHeap[M]) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h dueHeap[M]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].position, h[j].position = i, j
}

func (h *dueHeap[M]) Push(x any) {
	e := x.(*dueEntry[M])
	e.position = len(*h)
	*h = append(*h, e)
}

func (h *dueHeap[M]) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return e
}
