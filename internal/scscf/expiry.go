package scscf

import (
	"context"
	"time"
)

// This file holds the sweep: how an instance of the S-CSCF handles the
// expiries that need an action when their time comes (ExpiryKind).
//
// Every instance runs one sweep, and each round of it claims the
// expiries that are due from the Store, which hands out each of them to
// one instance at a time. No expiry runs on a timer of the process, so
// an instance that is replaced takes none with it: the expiries of the
// user it was serving are in the store, where the next round of any
// instance finds them.
//
// An expiry is handled once. The instance that claims it reads the item
// under the lock of its user, checks that it has really expired, removes
// it, and then sends the SAR or the NOTIFY that its end calls for.
// Should it stop half way, its claim runs out and another instance
// claims the item again; that one finds the item gone, or no longer
// expired, and leaves it alone.

// expiryBatch bounds how many expiries of one kind an instance claims in
// one round, so that it never holds more claims at once than it can
// handle within a lease. What is left over is due again at the next
// round.
const expiryBatch = 100

// sweep claims what is due every SweepInterval, until ctx ends. The
// first round comes after one interval, by when the S-CSCF has its stack
// and can send the NOTIFYs.
func (s *SCSCF) sweep(ctx context.Context) {
	tick := time.NewTicker(s.cfg.SweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.claimDue(ctx)
	}
}

// claimDue is one round: it claims the expiries of each kind that are
// due and hands each of them to a goroutine of its own, which marks it
// done itself. The round does not wait for those goroutines, and the
// next one comes on time: a handler waits for the lock of its user and
// for a Cx round trip, and one user holds up neither another user nor
// the claiming. A handler keeps its claim while it works, so the rounds
// that follow do not claim the same expiry again.
func (s *SCSCF) claimDue(ctx context.Context) {
	for _, kind := range ExpiryKinds {
		due, err := s.store.ClaimDue(ctx, kind, time.Now(), s.cfg.ExpiryLease, expiryBatch)
		if err != nil {
			s.log.Warn("expiries not claimed: the store failed; the next sweep claims them again",
				"kind", string(kind), "error", err.Error())
			continue
		}
		for _, e := range due {
			s.sweeping.Go(func() { s.handleExpiry(ctx, e) })
		}
	}
}

// handleExpiry handles one claimed expiry and marks it done, which takes
// its item out of the index of its kind.
//
// The handler has until the claim runs out, and no longer: after that
// the item is there for any instance to claim, and a lock or a Cx answer
// that this one still waited for would have two of them handling the
// same expiry. A handler that could not do its work, because the store
// or the lock in it failed, leaves the item as it was and marks nothing
// done, so that an instance claims the expiry again once the claim has
// run out.
func (s *SCSCF) handleExpiry(parent context.Context, e Expiry) {
	ctx, cancel := context.WithDeadline(parent, e.Until)
	defer cancel()
	var err error
	switch e.Kind {
	case RegAwaitAuthExpiry:
		err = s.regAwaitAuthExpired(ctx, e.PrivateIdentity, e.Nonce)
	case RegistrationExpiry:
		err = s.registrationExpired(ctx, e.PrivateIdentity)
	case SubscriptionExpiry:
		err = s.subscriptionExpired(ctx, e.Dialog)
	case UnregisteredProfileExpiry:
		err = s.unregisteredProfileExpired(ctx, e.PrivateIdentity)
	}
	if err != nil {
		s.log.Warn("expiry not handled: an instance claims it again once the claim runs out", "kind", string(e.Kind),
			"private_identity", e.PrivateIdentity, "call_id", e.Dialog.CallID, "error", err.Error())
		return
	}
	if err := s.store.Done(ctx, e); err != nil {
		s.log.Warn("a handled expiry stays in the store: the store failed; an instance claims it again, and finds nothing left to do",
			"kind", string(e.Kind), "error", err.Error())
	}
}
