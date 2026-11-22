package scscf

import (
	"context"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
)

// This file holds the sweep of the S-CSCF: which expiries need an action
// when their time comes, and what each of them does. How a sweep claims
// and hands them out is in internal/ims (ims.Sweep).

// sweep starts the sweep of this instance and returns the function that
// stops it.
func (s *SCSCF) sweep() (stop func()) {
	kinds := make([]string, 0, len(ExpiryKinds))
	for _, kind := range ExpiryKinds {
		kinds = append(kinds, string(kind))
	}
	return ims.Sweep[Expiry]{
		Kinds: kinds, Interval: s.cfg.SweepInterval, Lease: s.cfg.ExpiryLease,
		Claim: func(ctx context.Context, kind string, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
			return s.store.ClaimDue(ctx, ExpiryKind(kind), now, lease, limit)
		},
		Handle: s.handleExpiry,
		Done:   s.store.Done,
		Log:    s.log,
	}.Start()
}

// handleExpiry does what one claimed expiry calls for. Each handler
// takes the lock of the user, reads the item again and checks that it
// has really expired, since the expiry may have come up while another
// instance was refreshing the item. It returns the failure of the store
// or of the lock that left the item as it was, so that an instance
// claims the expiry again once the claim has run out.
func (s *SCSCF) handleExpiry(ctx context.Context, e Expiry) error {
	switch e.Kind {
	case RegAwaitAuthExpiry:
		return s.regAwaitAuthExpired(ctx, e.PrivateIdentity, e.Nonce)
	case RegistrationExpiry:
		return s.registrationExpired(ctx, e.PrivateIdentity)
	case SubscriptionExpiry:
		return s.subscriptionExpired(ctx, e.Dialog)
	case UnregisteredProfileExpiry:
		return s.unregisteredProfileExpired(ctx, e.PrivateIdentity)
	}
	return nil
}
