// Package smscstore is an smsc.Store on a kvstore.Store, so that the
// short messages an SC holds live outside its instances: any instance
// may take a message, and any instance may deliver it, and the messages
// of an instance that stops are delivered by another.
//
// The items and their keys, after the prefix of the Config and "smsc:":
//
//	queue:<destination>        queue, kept until the SC has emptied it
//	time-stamp:<destination>   timeStamp, a time to live of seconds
//
// The destinations that are due for a delivery attempt are a time index.
// Each operation touches the queue of one destination, or the front of
// that index; none walks the destinations. The queue of a destination is
// one item, so each operation reads and writes all of the messages that
// wait for it. That is what lets one compare and swap settle a lease, and
// it costs little at the scale this project is built for, where a queue
// holds a message or two; of several instances that write to the same
// destination at once, all but one lose the compare and swap and read
// the queue again.
//
// # Keeping the two items in step
//
// The entry in the time index and the queue are two items, and nothing
// in this project writes two items at once. The rule they are kept to is
// that the entry of a queue that holds messages is never missing, and
// never later than the time the queue says it is due. An entry that is
// too early costs one claim and is put right; a missing or late entry
// would leave the messages lying there, since nothing walks the queues.
// Three habits hold the rule:
//
//   - An operation that can bring a destination forward writes the entry
//     before it writes the queue. An entry that is too early, or that
//     names a queue which is not there, is harmless.
//   - After it has written the queue, an operation reconciles: it reads
//     the queue back, gives the entry the time that queue says, and reads
//     once more to see that the queue has not changed since. It never
//     takes the entry on trust, not even where it has just written the
//     same time itself, because another instance may have claimed the
//     entry, taken it out or written its own time in between.
//   - An entry is taken out only while it still holds the time this
//     operation put there (dropEntry), so that the entry another instance
//     has just written for a queue of its own stays. Nothing removes an
//     entry outright.
//
// Of the writes to the index, Enqueue's, Release's and Alert's go to a
// time at or before what the queue will say, and only the reconcile and
// the claim of ClaimDue ever move an entry later: the reconcile against a
// queue it has read and checked, and the claim by the length of one
// lease, which is the window in which another instance takes a stopped
// one's work over.
//
// Two states are left, and each needs a stop at an instant:
//
//   - An instance stops between the write of its reconcile and the read
//     that confirms it, while another has just brought the destination
//     forward. The entry is then later than the queue says, and the
//     messages wait until that time; they are not lost.
//   - An instance takes an entry out whose time happens to be the one
//     another instance has just written for a new queue, and stops before
//     it reads that queue back, which would have given the entry back.
//     The queue is then left with no entry. Removing an entry only while
//     the queue is absent would close this, but that is one operation
//     across two keys, which the store does not offer.
package smscstore

import (
	"context"
	"crypto/rand"
	"errors"
	"slices"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// dueDestinations is the name of the time index. A claim of kvstore puts
// the member at the end of its lease, which is where smsc.Store wants a
// leased destination to stand: another instance takes it over once the
// lease runs out.
const dueDestinations = "due"

// timeStampHorizon is how long the last TP-SCTS of a destination is
// kept. A stamp older than the current second cannot clash with one
// given from now on, since every stamp is at least the current second;
// the second on top of that leaves room for the clocks of the instances
// to differ. Two instances whose clocks are more than that apart can
// give one destination the same stamp twice, which smsc.MemoryStore,
// whose pruning works the same way, allows as well.
const timeStampHorizon = 2 * time.Second

// staleEntries bounds the index entries that one ClaimDue puts right
// before it gives up: each is a destination whose queue does not call it
// due, which the round puts right (see ClaimDue). Finding this many in a
// row means something other than a crash is at work, and the next
// ProcessDue of the SC looks again anyway.
const staleEntries = 8

// overtakenRounds bounds the rounds of a Release that other instances
// keep overtaking. Each lost round means an Enqueue came in between, so
// one or two are normal; this many means something else is wrong.
const overtakenRounds = 8

// reconcileRounds bounds the rounds of a reconcile whose queue other
// instances keep changing under it.
const reconcileRounds = 8

// errUnreconciled: the entry of a destination could not be brought into
// step with its queue. The SC takes it for what it is, a failure of the
// store, and tries the destination again.
var errUnreconciled = errors.New("smscstore: the due index could not be brought into step with the queue")

// Store is an smsc.Store on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ smsc.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "smsc")}
}

// queue is the item of one destination: everything the SC keeps about
// the short messages that wait for it. One item per destination, changed
// by compare and swap, is what makes a lease and the attempts behind it
// hold across instances.
//
// A destination that the store does not hold reads as the zero value,
// whose Due is zero. Every queue the Store writes has a Due, so that is
// how Enqueue knows that a message is the first to its destination.
type queue struct {
	// Messages are the short messages and status reports that wait for
	// the destination, oldest first.
	Messages []smsc.Message
	// Due is when the destination is next due for an attempt, and, while
	// it is leased, the end of the lease. The entry in the time index
	// holds the same time, or an earlier one.
	Due time.Time
	// Token is the lease in force, or empty. A call of another lease than
	// this one is refused with smsc.ErrLeaseLost.
	Token string
	// Attempts is the number of attempts in a row that failed for a
	// reason that may pass, as the last Release recorded it.
	Attempts int
	// Alerted is the time of an Alert that came while the destination was
	// leased, or zero. Release takes it as the time of the next attempt
	// when it is the earlier one.
	Alerted time.Time
}

// timeStamp is the item of the last TP-SCTS given to a destination.
type timeStamp struct {
	Last time.Time
}

func (s *Store) key(destination string) string { return s.c.Key("queue", destination) }

// keepUntilEmpty is the time to live of a queue: none. A destination
// stays in the store until the SC has delivered or discarded every
// message of it and released it. A time to live could take a queue away
// before that, and with it the status reports that the SC owes the
// senders of the messages whose validity period ended (TS 23.040 6.1).
// A queue that an instance leaves behind is claimed and emptied by
// another, since its entry in the time index outlives the lease.
func keepUntilEmpty(queue) time.Duration { return 0 }

// Enqueue appends the message to the queue of its destination, and puts
// a destination that had none into the time index, due at now.
//
// The entry goes in before the queue: an instance that stops in between
// leaves an entry that names no queue, or one that is earlier than the
// queue it names, and ClaimDue puts either right. The other order would
// leave a message that nothing ever finds, since the instance that would
// have written the entry is the one that stopped.
//
// It can therefore fail after the message is in the store, when the
// reconcile that follows does not get through. The SC answers the UE
// with a failure, and a UE that sends the short message again has it
// delivered twice. That is what holding the message outside the
// instances costs, and TS 23.040 6.2 item 3 lets the SC leave such a
// repeat to the receiving UE, which this project does (see the
// SIMPLIFIED comment on smsc.MOForwardShortMessage).
func (s *Store) Enqueue(ctx context.Context, m smsc.Message, now time.Time) error {
	if err := s.c.WriteDue(ctx, dueDestinations, m.Destination, now); err != nil {
		return err
	}
	if _, err := kvjson.ReadModifyWrite(ctx, s.c, s.key(m.Destination), keepUntilEmpty, func(q *queue) {
		if q.Due.IsZero() {
			q.Due = now
		}
		q.Messages = append(q.Messages, m)
	}); err != nil {
		return err
	}
	// A destination that had a queue keeps its time, which is the time the
	// reconcile puts back in the entry.
	return s.reconcile(ctx, m.Destination, now)
}

// ServiceCentreTimeStamp gives the destination the current second, or
// the second after the stamp it gave last, so that the time stamps of
// the messages to one MS differ (TS 23.040 6.2) whichever instance took
// them.
func (s *Store) ServiceCentreTimeStamp(ctx context.Context, destination string, now time.Time) (time.Time, error) {
	stamp, err := kvjson.ReadModifyWrite(ctx, s.c, s.c.Key("time-stamp", destination),
		func(v timeStamp) time.Duration { return s.c.TTLUntil(v.Last.Add(timeStampHorizon)) },
		func(v *timeStamp) {
			// The second is worked out anew in each round, since a round
			// that another instance overtook starts from its stamp.
			at := now.Truncate(time.Second)
			if !v.Last.IsZero() && !at.After(v.Last) {
				at = v.Last.Add(time.Second)
			}
			v.Last = at
		})
	if err != nil {
		return time.Time{}, err
	}
	return stamp.Last, nil
}

// ClaimDue leases the destination that has been due longest, if one is
// due at now. The time index hands each due destination to one instance
// at a time, and the lease in the queue is what the calls of that lease
// are then recognised by.
//
// An entry whose queue does not call the destination due is one that an
// instance left behind, or one that was written early on purpose: the
// queue is gone, or it is leased, or it is due later than the entry says.
// The reconcile puts the entry right, and the round after it looks for
// the next destination.
func (s *Store) ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (smsc.Lease, bool, error) {
	for range staleEntries {
		claims, err := s.c.ClaimDue(ctx, dueDestinations, now, lease, 1)
		if err != nil || len(claims) == 0 {
			return smsc.Lease{}, false, err
		}
		destination := claims[0].Member
		l, ok, err := s.take(ctx, destination, now, lease)
		if err != nil || ok {
			// The claim has put the entry at the end of the lease already,
			// which is where the queue of a leased destination says it
			// belongs.
			return l, ok, err
		}
		// The claim's own time is what the entry holds now, so the
		// reconcile may take it out, and only while nothing else has
		// written an entry since.
		if err := s.reconcile(ctx, destination, claims[0].Until); err != nil {
			return smsc.Lease{}, false, err
		}
	}
	return smsc.Lease{}, false, nil
}

// Claim leases destination if it is due at now. It asks the queue, not
// the index, which only hands out the destination that is due next.
//
// Nothing has moved the destination to the end of the lease in the
// index, as the claim of ClaimDue does, so the reconcile after the queue
// does it: an instance that stops in between leaves the entry where it
// was, at or before now, and an entry that is too early is put right by
// the first instance that claims it. The lease is this operation's own,
// so no other instance can take the queue away under it, and it puts
// nothing in the entry that it may take out again.
func (s *Store) Claim(ctx context.Context, destination string, now time.Time, lease time.Duration) (smsc.Lease, bool, error) {
	l, ok, err := s.take(ctx, destination, now, lease)
	if err != nil || !ok {
		return smsc.Lease{}, false, err
	}
	if err := s.reconcile(ctx, destination, time.Time{}); err != nil {
		return smsc.Lease{}, false, err
	}
	return l, true, nil
}

// errNotDue keeps take from writing a queue it may not lease. It never
// reaches a caller.
var errNotDue = errors.New("smscstore: the destination is not due")

// due reports whether a queue that is due at at has come due at now, to
// the millisecond that the time index counts in. A finer comparison
// would have ClaimDue hand out an entry, which holds the millisecond the
// queue is due in, that take then refuses, round after round, until it
// gives up; the SC would wait for its next sweep for a destination that
// is a fraction of a millisecond from due.
func due(at, now time.Time) bool {
	return !at.Truncate(time.Millisecond).After(now.Truncate(time.Millisecond))
}

// take writes a new lease, which ends at now plus lease, into the queue
// of a destination that is due at now. Of several instances that take
// one destination at once, the compare and swap of the queue lets
// through one. Keeping the index in step is the caller's.
func (s *Store) take(ctx context.Context, destination string, now time.Time, lease time.Duration) (smsc.Lease, bool, error) {
	token, until := rand.Text(), now.Add(lease)
	q, err := kvjson.Modify(ctx, s.c, s.key(destination), keepUntilEmpty, func(q *queue) error {
		if !due(q.Due, now) {
			return errNotDue
		}
		q.Token, q.Alerted, q.Due = token, time.Time{}, until
		return nil
	})
	switch {
	case errors.Is(err, errNotDue), errors.Is(err, kvstore.ErrNotFound):
		return smsc.Lease{}, false, nil
	case err != nil:
		return smsc.Lease{}, false, err
	}
	return smsc.Lease{Destination: destination, Token: token, Attempts: q.Attempts}, true, nil
}

// reconcile gives the entry of destination the time its queue says, and
// takes the entry out when the store holds no queue. Every write to a
// queue is followed by one.
//
// The first round always writes the entry, even where the caller has
// just put the same time in it: between that write and the queue another
// instance may have claimed the entry, taken it out, or written a time
// of its own, and nothing the caller holds says which. What the entry
// holds is known only of a write this reconcile made itself after it had
// read the queue, and only while the queue has not changed since, which
// is what the round after it compares. A queue that did change is read
// again, and the round after that writes the time of the queue that is
// there then.
//
// mine is the time the entry holds if no other instance has touched it
// since this operation began, and only that time may be taken out
// (dropEntry); zero says the operation put nothing in the entry and may
// take nothing out.
func (s *Store) reconcile(ctx context.Context, destination string, mine time.Time) error {
	// written is the version of the queue whose Due this reconcile has
	// put in the entry.
	var written string
	for range reconcileRounds {
		var q queue
		version, err := kvjson.Read(ctx, s.c, s.key(destination), &q)
		switch {
		case errors.Is(err, kvstore.ErrNotFound):
			if err := s.dropEntry(ctx, destination, mine); err != nil {
				return err
			}
			// A queue that another instance wrote in between keeps the
			// entry that instance wrote for it, which dropEntry left alone.
			// One whose entry held the same time, and so was taken out, is
			// found here and given its entry back in the round after this.
			switch _, back := kvjson.Read(ctx, s.c, s.key(destination), &q); {
			case errors.Is(back, kvstore.ErrNotFound):
				return nil
			case back != nil:
				return back
			}
			continue
		case err != nil:
			return err
		}
		if version == written {
			return nil
		}
		if err := s.c.WriteDue(ctx, dueDestinations, destination, q.Due); err != nil {
			return err
		}
		mine, written = q.Due, version
	}
	return errUnreconciled
}

// dropEntry takes the index entry of destination out while it still
// holds at. kvstore.Done is that compare and remove: an entry that holds
// another time belongs to a queue another instance has written since,
// and Done leaves it alone and reports ErrClaimLost, which is no failure
// here.
//
// The compare is on the time alone, so an entry that holds the very time
// this operation put there, but belongs to a queue another instance has
// written since, is taken out. The reconcile reads the queue again right
// after, and the round after that gives such a queue its entry back; only
// an instance that stops in between leaves it without one.
//
// A zero at says this operation put nothing in the entry and may take
// nothing out.
func (s *Store) dropEntry(ctx context.Context, destination string, at time.Time) error {
	if at.IsZero() {
		return nil
	}
	err := s.c.Done(ctx, kvstore.Claim{Index: s.c.Index(dueDestinations), Member: destination, Until: at})
	if errors.Is(err, kvstore.ErrClaimLost) {
		return nil
	}
	return err
}

// Queue returns the messages of the leased destination, oldest first.
func (s *Store) Queue(ctx context.Context, l smsc.Lease) ([]smsc.Message, error) {
	var q queue
	switch _, err := kvjson.Read(ctx, s.c, s.key(l.Destination), &q); {
	case errors.Is(err, kvstore.ErrNotFound):
		return nil, smsc.ErrLeaseLost
	case err != nil:
		return nil, err
	}
	if q.Token != l.Token {
		return nil, smsc.ErrLeaseLost
	}
	return q.Messages, nil
}

// Remove drops one message from the queue of the leased destination. The
// destination stays due when it was due, so the entry is left as it is.
func (s *Store) Remove(ctx context.Context, l smsc.Lease, id string) error {
	_, err := kvjson.Modify(ctx, s.c, s.key(l.Destination), keepUntilEmpty, func(q *queue) error {
		if q.Token != l.Token {
			return smsc.ErrLeaseLost
		}
		q.Messages = slices.DeleteFunc(q.Messages, func(m smsc.Message) bool { return m.ID == id })
		return nil
	})
	if errors.Is(err, kvstore.ErrNotFound) {
		return smsc.ErrLeaseLost
	}
	return err
}

// Release ends the lease: the destination becomes due at next, or at the
// time of an Alert that came during the lease if that is earlier, and a
// destination whose queue is empty is forgotten.
//
// An attempt that comes before the end of the lease brings the
// destination forward, so its entry goes in before the queue; one that
// comes later is left to the reconcile, and an instance that stops in
// between leaves the entry at the end of the lease, which is early.
// An Enqueue that came after the lease was taken leaves a queue of its
// own, which the round after it releases instead: a queue that has a
// message again is never forgotten, and the entry of its own that the
// Enqueue wrote is never taken out.
func (s *Store) Release(ctx context.Context, l smsc.Lease, next time.Time, attempts int) error {
	for range overtakenRounds {
		var q queue
		version, err := kvjson.Read(ctx, s.c, s.key(l.Destination), &q)
		switch {
		case errors.Is(err, kvstore.ErrNotFound):
			return smsc.ErrLeaseLost
		case err != nil:
			return err
		case q.Token != l.Token:
			return smsc.ErrLeaseLost
		}
		if len(q.Messages) == 0 {
			err := s.c.RemoveIf(ctx, s.key(l.Destination), version)
			if errors.Is(err, kvstore.ErrConflict) {
				continue
			}
			if err != nil {
				return err
			}
			return s.reconcile(ctx, l.Destination, q.Due)
		}
		if !q.Alerted.IsZero() && q.Alerted.Before(next) {
			next = q.Alerted
		}
		entry := q.Due
		if next.Before(entry) {
			if err := s.c.WriteDue(ctx, dueDestinations, l.Destination, next); err != nil {
				return err
			}
			entry = next
		}
		q.Token, q.Alerted, q.Attempts, q.Due = "", time.Time{}, attempts, next
		_, err = kvjson.CompareAndSwap(ctx, s.c, s.key(l.Destination), version, q, keepUntilEmpty(q))
		if errors.Is(err, kvstore.ErrConflict) {
			continue
		}
		if err != nil {
			return err
		}
		return s.reconcile(ctx, l.Destination, entry)
	}
	return kvjson.ErrGaveUp
}

// Alert makes a destination that has a queue due at now: the HSS says
// that the user can take short messages again (TS 29.338 5.2.2). One
// that comes while an instance holds the destination is kept in the
// queue, for the Release of that instance to take as the time of the
// next attempt.
//
// The entry goes in before the queue, since the alert brings the
// destination forward, and before it is known whether the destination
// has a queue at all: an entry that turns out to name none is taken out
// by the reconcile. An instance that stops in between leaves the entry
// early, and the alert itself unrecorded, which is what it reports to
// the HSS.
func (s *Store) Alert(ctx context.Context, destination string, now time.Time) (bool, error) {
	if err := s.c.WriteDue(ctx, dueDestinations, destination, now); err != nil {
		return false, err
	}
	_, err := kvjson.Modify(ctx, s.c, s.key(destination), keepUntilEmpty, func(q *queue) error {
		switch {
		case q.Token != "":
			q.Alerted = now
		case !due(q.Due, now):
			q.Due = now
		}
		return nil
	})
	held := err == nil
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		return false, err
	}
	if err := s.reconcile(ctx, destination, now); err != nil {
		return false, err
	}
	return held, nil
}
