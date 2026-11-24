// Package ipsmgwstore is an ipsmgw.Store on a kvstore.Store, so that
// whichever IP-SM-GW instance a delivery report or a NOTIFY reaches
// finds what another instance stored.
//
// The items and their keys, after the prefix of the Config and
// "ipsmgw:":
//
//	message-reference:<recipient>      messageReferences, time to live until its Expires
//	delivery:<Call-ID>                 PendingDelivery, time to live until its Expires
//	reg-subscription:<Call-ID>         RegSubscription, time to live until its Expires
//	reg-subscription-of:<identity>     index: the Call-ID of the subscription
//	reg-subscription-imsi:<IMSI>       index: the Call-ID of the subscription
//
// The timer of TR1N is a time index, named ipsmgw.ReportTimeout.
package ipsmgwstore

import (
	"context"
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Store is an ipsmgw.Store on a kvstore.Store. The IP-SM-GW writes every
// expiry into what it stores, as a time, and the Store takes the times
// to live and the due times of the timer from there.
type Store struct {
	c *kvjson.Client
}

var _ ipsmgw.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "ipsmgw")}
}

// ClaimDue claims at most limit deliveries whose TR1N has run out at now
// (kvstore.Store.ClaimDue).
//
// A member that names no delivery, which an instance that stopped within
// PutDelivery leaves, comes back as an expiry whose TakeDelivery finds
// nothing; its Done takes the member out of the index.
func (s *Store) ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]ipsmgw.Expiry, error) {
	claims, err := s.c.ClaimDue(ctx, ipsmgw.ReportTimeout, now, lease, limit)
	if err != nil {
		return nil, err
	}
	expiries := make([]ipsmgw.Expiry, 0, len(claims))
	for _, c := range claims {
		expiries = append(expiries, ipsmgw.Expiry{CallID: c.Member, Until: c.Until})
	}
	return expiries, nil
}

// Done takes the delivery of a claimed expiry out of the index. A claim
// that has ended, which kvstore.Done reports with ErrClaimLost, leaves
// the index as it is: the delivery has been taken with its member, or the
// claim ran out and another instance has it now.
func (s *Store) Done(ctx context.Context, e ipsmgw.Expiry) error {
	err := s.c.Done(ctx, kvstore.Claim{Index: s.c.Index(ipsmgw.ReportTimeout), Member: e.CallID, Until: e.Until})
	if errors.Is(err, kvstore.ErrClaimLost) {
		return nil
	}
	return err
}

// NextMessageReference counts with compare and swap, so that two
// instances that send to one recipient at once never give the same
// RP-MR. After 255 comes 0, as in ipsmgw.MemoryStore. The counter is
// that of the public identity, so that two ways of writing one recipient
// share it.
func (s *Store) NextMessageReference(ctx context.Context, recipient string, expires time.Time) (uint8, error) {
	var mr uint8
	now := s.c.Now()
	if _, err := kvjson.ReadModifyWrite(ctx, s.c, s.c.Key("message-reference", ims.PublicIdentityKey(recipient)),
		func(r messageReferences) time.Duration { return s.c.TTLUntil(r.Expires) },
		func(r *messageReferences) {
			// A missing counter, or one past its Expires that the store
			// still keeps by its own clock, starts at 0.
			if !now.Before(r.Expires) {
				r.Next = 0
			}
			mr = r.Next
			r.Next++
			r.Expires = expires
		}); err != nil {
		return 0, err
	}
	return mr, nil
}

// messageReferences is the item of the counter of a recipient.
type messageReferences struct {
	// Next is the RP-MR of the next RP-DATA.
	Next uint8
	// Expires is when the counter goes, as the last NextMessageReference
	// gave it.
	Expires time.Time
}

// PutDelivery schedules TR1N, then writes the delivery. An instance that
// stops in between, or whose write fails, leaves a member whose claimer
// finds no delivery.
func (s *Store) PutDelivery(ctx context.Context, d ipsmgw.PendingDelivery) error {
	if err := s.c.WriteDue(ctx, ipsmgw.ReportTimeout, d.CallID, d.ReportDeadline); err != nil {
		return err
	}
	return kvjson.Write(ctx, s.c, s.c.Key("delivery", d.CallID), d, s.c.TTLUntil(d.Expires))
}

func (s *Store) Delivery(ctx context.Context, callID string) (ipsmgw.PendingDelivery, bool, error) {
	var d ipsmgw.PendingDelivery
	_, err := kvjson.Read(ctx, s.c, s.c.Key("delivery", callID), &d)
	return live(d, d.Expires, s.c.Now(), err)
}

// TakeDelivery takes the delivery (kvjson.ReadAndRemove) and its TR1N.
// A Call-ID is never used again, so the expiry can go. One that the
// store fails to remove finds no delivery when it is claimed, as after
// a stop within PutDelivery.
func (s *Store) TakeDelivery(ctx context.Context, callID string) (ipsmgw.PendingDelivery, bool, error) {
	d, err := kvjson.ReadAndRemove[ipsmgw.PendingDelivery](ctx, s.c, s.c.Key("delivery", callID))
	if err == nil {
		s.c.RemoveDue(ctx, ipsmgw.ReportTimeout, callID)
	}
	return live(d, d.Expires, s.c.Now(), err)
}

// live is the answer to a read of an item v that goes at expires: none
// when the store has no item, or the item has passed its Expires by the
// clock of the Client while the store, by its own clock, still keeps
// it.
func live[T any](v T, expires, now time.Time, err error) (T, bool, error) {
	var zero T
	switch {
	case errors.Is(err, kvstore.ErrNotFound):
		return zero, false, nil
	case err != nil:
		return zero, false, err
	case !now.Before(expires):
		return zero, false, nil
	}
	return v, true, nil
}

func (s *Store) subscriptionKey(callID string) string { return s.c.Key("reg-subscription", callID) }

func (s *Store) subscriptionIndexKey(publicID string) string {
	return s.c.Key("reg-subscription-of", ims.PublicIdentityKey(publicID))
}

func (s *Store) subscriptionIMSIKey(imsi string) string {
	return s.c.Key("reg-subscription-imsi", imsi)
}

func (s *Store) RegSubscription(ctx context.Context, callID string) (ipsmgw.RegSubscription, bool, error) {
	var sub ipsmgw.RegSubscription
	_, err := kvjson.Read(ctx, s.c, s.subscriptionKey(callID), &sub)
	return live(sub, sub.Expires, s.c.Now(), err)
}

func (s *Store) RegSubscriptionOf(ctx context.Context, publicID string) (ipsmgw.RegSubscription, bool, error) {
	return s.subscriptionByIndex(ctx, s.subscriptionIndexKey(publicID), func(sub ipsmgw.RegSubscription) bool {
		return ims.PublicIdentityKey(sub.PublicIdentity) == ims.PublicIdentityKey(publicID)
	})
}

// RegSubscriptionOfIMSI finds the subscription through the index item of
// the IMSI. A subscription without an IMSI is in no such item, so an
// empty IMSI finds nothing.
func (s *Store) RegSubscriptionOfIMSI(ctx context.Context, imsi string) (ipsmgw.RegSubscription, bool, error) {
	if imsi == "" {
		return ipsmgw.RegSubscription{}, false, nil
	}
	return s.subscriptionByIndex(ctx, s.subscriptionIMSIKey(imsi), func(sub ipsmgw.RegSubscription) bool {
		return sub.IMSI == imsi
	})
}

// subscriptionByIndex reads the index item at key and then the
// subscription whose Call-ID it holds. matches says whether that
// subscription is still the one the index item is for: an item that an
// instance failed to remove, or that a new subscription has not yet
// taken over, names one that has moved on.
func (s *Store) subscriptionByIndex(ctx context.Context, key string, matches func(ipsmgw.RegSubscription) bool) (ipsmgw.RegSubscription, bool, error) {
	callID, _, err := s.c.ReadIndex(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return ipsmgw.RegSubscription{}, false, nil
	}
	if err != nil {
		return ipsmgw.RegSubscription{}, false, err
	}
	sub, ok, err := s.RegSubscription(ctx, callID)
	if err != nil || !ok || !matches(sub) {
		return ipsmgw.RegSubscription{}, false, err
	}
	return sub, true, nil
}

// PutRegSubscription points the index items of the public identity and
// of the IMSI to the new subscription, writes it, and then removes the
// subscription that the index item of the public identity named before,
// if it was another. Of two subscriptions to one identity that two
// instances put at once, the index item names the one written last, and
// RegSubscriptionOf finds only that one; the other stays until its
// Expires, as does an old one whose removal fails.
func (s *Store) PutRegSubscription(ctx context.Context, sub ipsmgw.RegSubscription) error {
	callID, indexKey := sub.Dialog.ID.CallID, s.subscriptionIndexKey(sub.PublicIdentity)
	old, _, err := s.c.ReadIndex(ctx, indexKey)
	hadOld := err == nil
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		return err
	}
	ttl := s.c.TTLUntil(sub.Expires)
	if err := s.c.WriteIndex(ctx, indexKey, callID, ttl); err != nil {
		return err
	}
	if sub.IMSI != "" {
		if err := s.c.WriteIndex(ctx, s.subscriptionIMSIKey(sub.IMSI), callID, ttl); err != nil {
			return err
		}
	}
	if err := kvjson.Write(ctx, s.c, s.subscriptionKey(callID), sub, ttl); err != nil {
		return err
	}
	if hadOld && old != callID {
		// The old subscription goes with both of its index items, except
		// the ones that now name this subscription, which RemoveIndexIf
		// leaves alone.
		return s.DeleteRegSubscription(ctx, old)
	}
	return nil
}

// DeleteRegSubscription removes the subscription, and then the index
// items of its public identity and of its IMSI if those still name it.
func (s *Store) DeleteRegSubscription(ctx context.Context, callID string) error {
	var sub ipsmgw.RegSubscription
	_, err := kvjson.Read(ctx, s.c, s.subscriptionKey(callID), &sub)
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.subscriptionKey(callID)); err != nil {
		return err
	}
	if sub.IMSI != "" {
		if err := s.c.RemoveIndexIf(ctx, s.subscriptionIMSIKey(sub.IMSI), callID); err != nil {
			return err
		}
	}
	return s.c.RemoveIndexIf(ctx, s.subscriptionIndexKey(sub.PublicIdentity), callID)
}
