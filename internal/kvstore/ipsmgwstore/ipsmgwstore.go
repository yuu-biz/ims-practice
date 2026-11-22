// Package ipsmgwstore is an ipsmgw.Store on a kvstore.Store, so that
// whichever IP-SM-GW instance a delivery report or a NOTIFY reaches
// finds what another instance stored.
//
// The items and their keys, after the prefix of the Config and
// "ipsmgw:":
//
//	message-reference:<recipient>      the next RP-MR, time to live 10 min
//	delivery:<Call-ID>                 PendingDelivery, time to live until its Expires
//	reg-subscription:<Call-ID>         RegSubscription, time to live until its Expires
//	reg-subscription-of:<identity>     index: the Call-ID of the subscription
//
// The timer of TR1N is a time index (ReportTimer).
package ipsmgwstore

import (
	"context"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Config is the configuration of the Store.
type Config struct {
	kvjson.Config
	// ReportTimeout is TR1N, as ipsmgw.Config has it; zero means the
	// default of ipsmgw, 64 s. PutDelivery schedules ReportTimer with it.
	ReportTimeout time.Duration
}

// defaultReportTimeout is that of ipsmgw (deliver.go), which does not
// export it.
const defaultReportTimeout = 2 * 32 * time.Second

// Store is an ipsmgw.Store on a kvstore.Store.
type Store struct {
	c             *kvjson.Client
	reportTimeout time.Duration
}

var _ ipsmgw.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg Config) *Store {
	if cfg.ReportTimeout == 0 {
		cfg.ReportTimeout = defaultReportTimeout
	}
	return &Store{c: kvjson.New(kv, cfg.Config, "ipsmgw"), reportTimeout: cfg.ReportTimeout}
}

// ReportTimer is the time index of TR1N (TS 24.011 6.2.2.2): a member is
// the Call-ID of a delivery, due ReportTimeout after PutDelivery.
// ipsmgw runs the timer in the process that sent the MESSAGE, and no
// one reports a delivery whose sender stopped (deliver.go). An
// instance that claims the member instead calls what that timer calls,
// which takes the delivery with TakeDelivery: only one of the timer,
// the delivery report and a failed MESSAGE gets it, so the SC hears
// the outcome once.
const ReportTimer = "report-timeout"

// ClaimDue claims at most limit deliveries whose TR1N has run out at
// now (kvstore.Store.ClaimDue).
func (s *Store) ClaimDue(ctx context.Context, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	return s.c.ClaimDue(ctx, ReportTimer, now, lease, limit)
}

// Done ends a claim of ClaimDue.
func (s *Store) Done(ctx context.Context, claim kvstore.Claim) error { return s.c.Done(ctx, claim) }

// messageReferenceLifetime is that of ipsmgw (store.go): the counter of
// a recipient outlives every delivery that could still wait for a
// report with one of its values.
const messageReferenceLifetime = 10 * time.Minute

// NextMessageReference counts with compare and swap, so that two
// instances that send to one recipient at once never give the same
// RP-MR. After 255 comes 0, as in ipsmgw.MemoryStore. The counter is
// that of the public identity, so that two ways of writing one recipient
// share it.
func (s *Store) NextMessageReference(recipient string) uint8 {
	// The item holds the next value; a missing one is 0.
	var mr uint8
	if _, err := kvjson.Upsert(s.c, s.c.Key("message-reference", ims.PublicIdentityKey(recipient)),
		func(uint8) time.Duration { return messageReferenceLifetime },
		func(next *uint8) { mr = *next; *next++ }); err != nil {
		return 0
	}
	return mr
}

// PutDelivery schedules TR1N, then writes the delivery. An instance that
// stops in between leaves a member whose claimer finds no delivery.
func (s *Store) PutDelivery(d ipsmgw.PendingDelivery) {
	s.c.Schedule(ReportTimer, d.CallID, s.c.Now().Add(s.reportTimeout))
	kvjson.Put(s.c, s.c.Key("delivery", d.CallID), d, s.c.TTLUntil(d.Expires))
}

func (s *Store) Delivery(callID string) (ipsmgw.PendingDelivery, bool) {
	var d ipsmgw.PendingDelivery
	if _, ok := kvjson.Get(s.c, s.c.Key("delivery", callID), &d); !ok || !s.c.Now().Before(d.Expires) {
		return ipsmgw.PendingDelivery{}, false
	}
	return d, true
}

// TakeDelivery takes the delivery (kvjson.Take) and its timer. A Call-ID
// is never used again, so the timer can go.
func (s *Store) TakeDelivery(callID string) (ipsmgw.PendingDelivery, bool) {
	d, ok := kvjson.Take[ipsmgw.PendingDelivery](s.c, s.c.Key("delivery", callID))
	if ok {
		s.c.Unschedule(ReportTimer, callID)
	}
	if !ok || !s.c.Now().Before(d.Expires) {
		return ipsmgw.PendingDelivery{}, false
	}
	return d, true
}

func (s *Store) subscriptionKey(callID string) string { return s.c.Key("reg-subscription", callID) }

func (s *Store) RegSubscription(callID string) (ipsmgw.RegSubscription, bool) {
	var sub ipsmgw.RegSubscription
	if _, ok := kvjson.Get(s.c, s.subscriptionKey(callID), &sub); !ok || !s.c.Now().Before(sub.Expires) {
		return ipsmgw.RegSubscription{}, false
	}
	return sub, true
}

func (s *Store) RegSubscriptionOf(publicID string) (ipsmgw.RegSubscription, bool) {
	k := ims.PublicIdentityKey(publicID)
	callID, _, ok := s.c.IndexTarget(s.c.Key("reg-subscription-of", k))
	if !ok {
		return ipsmgw.RegSubscription{}, false
	}
	sub, ok := s.RegSubscription(callID)
	if !ok || ims.PublicIdentityKey(sub.PublicIdentity) != k {
		return ipsmgw.RegSubscription{}, false
	}
	return sub, true
}

// PutRegSubscription points the index item of the public identity to
// the new subscription, writes it, and then removes the subscription
// that the index item named before, if it was another. Of two
// subscriptions to one identity that two instances put at once, the
// index item names the one written last, and RegSubscriptionOf finds
// only that one; the other stays until its Expires.
func (s *Store) PutRegSubscription(sub ipsmgw.RegSubscription) {
	callID, indexKey := sub.Dialog.ID.CallID, s.c.Key("reg-subscription-of", ims.PublicIdentityKey(sub.PublicIdentity))
	old, _, hadOld := s.c.IndexTarget(indexKey)
	ttl := s.c.TTLUntil(sub.Expires)
	s.c.PutIndex(indexKey, callID, ttl)
	kvjson.Put(s.c, s.subscriptionKey(callID), sub, ttl)
	if hadOld && old != callID {
		s.c.Delete(s.subscriptionKey(old))
	}
}

func (s *Store) DeleteRegSubscription(callID string) {
	var sub ipsmgw.RegSubscription
	_, ok := kvjson.Get(s.c, s.subscriptionKey(callID), &sub)
	s.c.Delete(s.subscriptionKey(callID))
	if ok {
		s.c.DeleteIndexIf(s.c.Key("reg-subscription-of", ims.PublicIdentityKey(sub.PublicIdentity)), callID)
	}
}
