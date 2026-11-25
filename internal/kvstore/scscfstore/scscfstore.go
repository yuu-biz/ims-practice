// Package scscfstore is a scscf.Store on a kvstore.Store, so that the
// registrar state of the S-CSCF lives outside the node and several
// S-CSCF instances share it.
//
// The items and their keys, after the prefix of the Config and
// "scscf:", with the private identity in lower case as scscf.MemoryStore
// compares it:
//
//	challenge:<private identity>            Challenge, time to live until its KeepUntil
//	registration:<private identity>         Registration, no time to live
//	impu:<public identity>                  index: the private identity of the registration
//	subscription:<Call-ID>:<tags>           Subscription, no time to live
//	subscriptions-of:<private identity>     index: the subscriptions to that registration
//	original-dialog:<identifier>            OriginalDialog, time to live until its Expires
//	unregistered:<private identity>         UnregisteredProfile, no time to live
//	unregistered-impu:<public identity>     index: the private identity of the profile
//
// A public identity is in the form of ims.PublicIdentityKey, as
// scscf.MemoryStore compares it. Every lookup is a read of
// one or two keys; none walks the registrations, so that the time of a
// lookup does not grow with the number of users.
//
// The four expiries of the S-CSCF that need an action (scscf.ExpiryKind)
// are time indexes, each named after its kind. An item whose end needs
// an action (a SAR, a NOTIFY) keeps no time to live of its own: the
// instance that claims its expiry reads it, acts, and removes it. The
// items that only have to go away have one.
//
// Every operation returns the failure of the store, and a lookup does
// not take it for a missing item. A write returns it until the main item
// is written or removed. What fails after that is the removal of an
// index item, an entry of a set or an expiry that the main item no
// longer needs, which is what an instance that stops there leaves: a
// miss (kvjson.WriteIndex), or an expiry whose claimer finds nothing to
// do.
package scscfstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Store is a scscf.Store on a kvstore.Store. The S-CSCF writes every
// expiry into what it stores, as a time, and the Store takes the times
// to live and the due times of the expiries from there.
type Store struct {
	c *kvjson.Client
}

var _ scscf.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "scscf")}
}

// expiryGrace is how long an index item outlives the Expires of the item
// it points to. That item stays until the instance which claims its
// expiry has acted, which takes a lease and a Cx round trip, or several
// leases if that instance stops; until then the index item has to find
// it. Afterwards it goes by itself, so that an index item left behind by
// an instance that stopped does not stay for ever.
const expiryGrace = 10 * time.Minute

// ClaimDue claims at most limit members of the time index of kind that
// are due at now, each for lease (kvstore.Store.ClaimDue). Its name is
// the name of the kind.
//
// A member that does not name an item of its kind, which only a write of
// an older version of this package could leave, comes back as an expiry
// that names nothing: the caller finds no item, and its Done takes the
// member out of the index.
func (s *Store) ClaimDue(ctx context.Context, kind scscf.ExpiryKind, now time.Time, lease time.Duration, limit int) ([]scscf.Expiry, error) {
	claims, err := s.c.ClaimDue(ctx, string(kind), now, lease, limit)
	if err != nil {
		return nil, err
	}
	expiries := make([]scscf.Expiry, 0, len(claims))
	for _, c := range claims {
		expiries = append(expiries, expiryOf(kind, c))
	}
	return expiries, nil
}

// Done takes the member of a claimed expiry out of its index. A claim
// that has ended, which kvstore.Done reports with ErrClaimLost, leaves
// the index as it is: the item has been rescheduled, removed with its
// member (DeleteSubscription), or claimed anew by another instance.
func (s *Store) Done(ctx context.Context, e scscf.Expiry) error {
	err := s.c.Done(ctx, kvstore.Claim{Index: s.c.Index(string(e.Kind)), Member: memberOf(e), Until: e.Until})
	if errors.Is(err, kvstore.ErrClaimLost) {
		return nil
	}
	return err
}

// expiryOf reads back what memberOf wrote for an expiry of kind.
func expiryOf(kind scscf.ExpiryKind, c kvstore.Claim) scscf.Expiry {
	e := scscf.Expiry{Kind: kind, Until: c.Until}
	parts := kvjson.SplitMember(c.Member)
	switch {
	case kind == scscf.RegAwaitAuthExpiry && len(parts) == 2:
		e.PrivateIdentity, e.Nonce = parts[0], parts[1]
	case kind == scscf.SubscriptionExpiry && len(parts) == 3:
		e.Dialog = sip.DialogID{CallID: parts[0], LocalTag: parts[1], RemoteTag: parts[2]}
	case (kind == scscf.RegistrationExpiry || kind == scscf.UnregisteredProfileExpiry) && len(parts) == 1:
		e.PrivateIdentity = parts[0]
	}
	return e
}

// memberOf names the item of an expiry in its time index. A challenge is
// named by the private identity and the nonce, so that the expiry of an
// answered challenge never ends a later one; a subscription by its
// dialog; a registration and a profile by the private identity alone,
// which is the one item of its kind that the user has.
func memberOf(e scscf.Expiry) string {
	switch e.Kind {
	case scscf.RegAwaitAuthExpiry:
		return challengeMember(e.PrivateIdentity, e.Nonce)
	case scscf.SubscriptionExpiry:
		return subscriptionMember(e.Dialog)
	default:
		return userMember(e.PrivateIdentity)
	}
}

func challengeMember(privateID, nonce string) string {
	return kvjson.Member(privateKey(privateID), nonce)
}

func userMember(privateID string) string { return kvjson.Member(privateKey(privateID)) }

func subscriptionMember(id sip.DialogID) string {
	return kvjson.Member(id.CallID, id.LocalTag, id.RemoteTag)
}

// privateKey is the form in which scscf.MemoryStore compares a private
// identity.
func privateKey(privateID string) string { return strings.ToLower(privateID) }

// publicKeys are the keys of the public identities of a registration or
// a profile.
func publicKeys(ids []string) []string {
	var keys []string
	for _, id := range ids {
		keys = append(keys, ims.PublicIdentityKey(id))
	}
	return keys
}

// indexTTL is the time to live of an index item for an item that ends
// at expires.
func (s *Store) indexTTL(expires time.Time) time.Duration {
	if expires.IsZero() {
		return 0
	}
	return s.c.TTLUntil(expires.Add(expiryGrace))
}

// read reads the item under key into v, and reports whether there is
// one.
func read[T any](ctx context.Context, c *kvjson.Client, key string, v *T) (bool, error) {
	_, err := kvjson.Read(ctx, c, key, v)
	if errors.Is(err, kvstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// indexTarget reads the index item key, and reports whether there is
// one.
func (s *Store) indexTarget(ctx context.Context, key string) (string, bool, error) {
	t, _, err := s.c.ReadIndex(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return "", false, nil
	}
	return t, err == nil, err
}

func (s *Store) challengeKey(privateID string) string {
	return s.c.Key("challenge", privateKey(privateID))
}

func (s *Store) Challenge(ctx context.Context, privateID string) (scscf.Challenge, bool, error) {
	var c scscf.Challenge
	ok, err := read(ctx, s.c, s.challengeKey(privateID), &c)
	return c, ok, err
}

// PutChallenge stores c and schedules its reg-await-auth. The expiry
// goes into the index first: an instance that stops before the challenge
// is written leaves a member that ends nothing, since no challenge has
// its nonce. The expiry of the challenge that c replaces goes once c is
// written, so that a pending challenge has one expiry and no other.
func (s *Store) PutChallenge(ctx context.Context, c scscf.Challenge) error {
	old, replaced, err := s.Challenge(ctx, c.PrivateIdentity)
	if err != nil {
		return err
	}
	if err := s.c.WriteDue(ctx, string(scscf.RegAwaitAuthExpiry), challengeMember(c.PrivateIdentity, c.Nonce), c.Expires); err != nil {
		return err
	}
	if err := kvjson.Write(ctx, s.c, s.challengeKey(c.PrivateIdentity), c, s.c.TTLUntil(c.KeepUntil)); err != nil {
		return err
	}
	if replaced && old.Nonce != c.Nonce {
		s.c.RemoveDue(ctx, string(scscf.RegAwaitAuthExpiry), challengeMember(old.PrivateIdentity, old.Nonce))
	}
	return nil
}

// DeleteChallenge removes the challenge and its expiry. Its member
// names the nonce, so a later challenge of the user keeps its own, and a
// member that stays, which only an instance that stopped half way
// leaves, ends nothing.
func (s *Store) DeleteChallenge(ctx context.Context, privateID string) error {
	c, ok, err := s.Challenge(ctx, privateID)
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.challengeKey(privateID)); err != nil {
		return err
	}
	if ok {
		s.c.RemoveDue(ctx, string(scscf.RegAwaitAuthExpiry), challengeMember(c.PrivateIdentity, c.Nonce))
	}
	return nil
}

func (s *Store) Registration(ctx context.Context, privateID string) (scscf.Registration, bool, error) {
	var r scscf.Registration
	ok, err := read(ctx, s.c, s.c.Key("registration", privateKey(privateID)), &r)
	return r, ok, err
}

// RegistrationOf reads the index item of the public identity, then the
// registration it names. A registration that does not hold the
// identity is a miss: the index item is one that a writer has not
// finished with, or one that a stopped instance left (kvjson.WriteIndex).
// So is one whose Expires has passed by the clock of the Store, as
// scscf.MemoryStore compares it.
func (s *Store) RegistrationOf(ctx context.Context, publicID string) (scscf.Registration, bool, error) {
	k := ims.PublicIdentityKey(publicID)
	target, ok, err := s.indexTarget(ctx, s.c.Key("impu", k))
	if err != nil || !ok {
		return scscf.Registration{}, false, err
	}
	r, ok, err := s.Registration(ctx, target)
	if err != nil || !ok || !s.c.Now().Before(r.Expires) || !slices.Contains(publicKeys(r.PublicIdentities), k) {
		return scscf.Registration{}, false, err
	}
	return r, true, nil
}

// PutRegistration writes, in this order: the index items of the public
// identities, the expiry, the registration, and then removes the index
// items of the identities that the registration no longer has. A reader
// in between, or after an instance stopped in between, finds either the
// old registration or the new one, and never one by an identity it
// does not hold. Two instances that write the same registration at once
// end with the one that wrote last, as with scscf.MemoryStore; an index
// item of the other is a miss.
func (s *Store) PutRegistration(ctx context.Context, r scscf.Registration) error {
	k := privateKey(r.PrivateIdentity)
	old, _, err := s.Registration(ctx, k)
	if err != nil {
		return err
	}
	keys := publicKeys(r.PublicIdentities)
	for _, pk := range keys {
		if err := s.c.WriteIndex(ctx, s.c.Key("impu", pk), k, s.indexTTL(r.Expires)); err != nil {
			return err
		}
	}
	if err := s.c.WriteDue(ctx, string(scscf.RegistrationExpiry), userMember(k), r.Expires); err != nil {
		return err
	}
	if err := kvjson.Write(ctx, s.c, s.c.Key("registration", k), r, 0); err != nil {
		return err
	}
	for _, pk := range publicKeys(old.PublicIdentities) {
		if !slices.Contains(keys, pk) {
			s.c.RemoveIndexIf(ctx, s.c.Key("impu", pk), k)
		}
	}
	return nil
}

// DeleteRegistration removes the registration first and its index items
// after, so that an index item never leads to a registration that is
// gone. It leaves the expiry: the member is the private identity, and a
// registration that another instance writes at the same moment would
// lose its own expiry with it. The claim of that member finds no
// registration, or one with a later Expires, and does nothing.
func (s *Store) DeleteRegistration(ctx context.Context, privateID string) error {
	k := privateKey(privateID)
	old, ok, err := s.Registration(ctx, k)
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.c.Key("registration", k)); err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, pk := range publicKeys(old.PublicIdentities) {
		s.c.RemoveIndexIf(ctx, s.c.Key("impu", pk), k)
	}
	return nil
}

func (s *Store) subscriptionKey(id sip.DialogID) string {
	return s.c.Key("subscription", id.CallID, id.LocalTag, id.RemoteTag)
}

func (s *Store) Subscription(ctx context.Context, id sip.DialogID) (scscf.Subscription, bool, error) {
	var sub scscf.Subscription
	ok, err := read(ctx, s.c, s.subscriptionKey(id), &sub)
	return sub, ok, err
}

// subscriptionRef is one entry of the index of the subscriptions to a
// registration. Expires lets a writer drop the entries of subscriptions
// that ended long ago, which an instance that stopped between two writes
// left behind (see removeStale).
type subscriptionRef struct {
	ID      sip.DialogID
	Expires time.Time
}

// subscriptionsOf is the index item of the subscriptions to the
// registration of a private identity: a set, as one item, that writers
// change by compare and swap, so that two instances that add a
// subscription each keep both.
type subscriptionsOf struct {
	Subscriptions []subscriptionRef
}

// removeStale drops the entries whose subscription ended more than
// expiryGrace ago. Their subscription has been removed by now, by the
// instance that claimed its expiry; if it has not, SubscriptionsOf would
// still find it through a newer PutSubscription. An entry is never
// dropped because its subscription is missing: a writer of another
// instance may be about to write it.
func (set *subscriptionsOf) removeStale(now time.Time) {
	set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(r subscriptionRef) bool {
		return !r.Expires.IsZero() && r.Expires.Add(expiryGrace).Before(now)
	})
}

// setTTL lets the set go when the last of its subscriptions would.
func (s *Store) setTTL(set subscriptionsOf) time.Duration {
	if len(set.Subscriptions) == 0 {
		return time.Millisecond
	}
	var last time.Time
	for _, r := range set.Subscriptions {
		if r.Expires.IsZero() {
			return 0
		}
		if r.Expires.After(last) {
			last = r.Expires
		}
	}
	return s.indexTTL(last)
}

func (s *Store) subscriptionsOfKey(privateID string) string {
	return s.c.Key("subscriptions-of", privateKey(privateID))
}

// SubscriptionsOf reads the set of the private identity, and then each
// subscription in it. A subscription that is missing, or that reports
// another registration, is left out.
func (s *Store) SubscriptionsOf(ctx context.Context, privateID string) ([]scscf.Subscription, error) {
	var set subscriptionsOf
	if ok, err := read(ctx, s.c, s.subscriptionsOfKey(privateID), &set); err != nil || !ok {
		return nil, err
	}
	var subs []scscf.Subscription
	for _, r := range set.Subscriptions {
		sub, ok, err := s.Subscription(ctx, r.ID)
		if err != nil {
			return nil, err
		}
		if ok && privateKey(sub.PrivateIdentity) == privateKey(privateID) {
			subs = append(subs, sub)
		}
	}
	return subs, nil
}

// PutSubscription adds the subscription to the set of its registration,
// then schedules its expiry, then writes it. A reader in between finds
// an entry without a subscription, and leaves it out.
func (s *Store) PutSubscription(ctx context.Context, sub scscf.Subscription) error {
	id := sub.Dialog.ID
	if _, err := kvjson.ReadModifyWrite(ctx, s.c, s.subscriptionsOfKey(sub.PrivateIdentity), s.setTTL, func(set *subscriptionsOf) {
		set.removeStale(s.c.Now())
		set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(r subscriptionRef) bool { return r.ID == id })
		set.Subscriptions = append(set.Subscriptions, subscriptionRef{ID: id, Expires: sub.Expires})
	}); err != nil {
		return err
	}
	if err := s.c.WriteDue(ctx, string(scscf.SubscriptionExpiry), subscriptionMember(id), sub.Expires); err != nil {
		return err
	}
	return kvjson.Write(ctx, s.c, s.subscriptionKey(id), sub, 0)
}

// DeleteSubscription removes the subscription, then its expiry and its
// entry in the set. A dialog is never used again, so the expiry can go.
// An expiry or an entry that stays finds no subscription: the claimer of
// the expiry does nothing, and SubscriptionsOf leaves the entry out
// until removeStale drops it.
func (s *Store) DeleteSubscription(ctx context.Context, id sip.DialogID) error {
	sub, ok, err := s.Subscription(ctx, id)
	if err != nil {
		return err
	}
	if err := s.c.Remove(ctx, s.subscriptionKey(id)); err != nil {
		return err
	}
	s.c.RemoveDue(ctx, string(scscf.SubscriptionExpiry), subscriptionMember(id))
	if !ok {
		return nil
	}
	kvjson.Modify(ctx, s.c, s.subscriptionsOfKey(sub.PrivateIdentity), s.setTTL, func(set *subscriptionsOf) error {
		set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(r subscriptionRef) bool { return r.ID == id })
		return nil
	})
	return nil
}

// OriginalDialog returns the identifier until its Expires, by the clock
// of the Store, as scscf.MemoryStore does; the time to live removes it
// soon after.
func (s *Store) OriginalDialog(ctx context.Context, id string) (scscf.OriginalDialog, bool, error) {
	var d scscf.OriginalDialog
	ok, err := read(ctx, s.c, s.c.Key("original-dialog", id), &d)
	if err != nil || !ok || !s.c.Now().Before(d.Expires) {
		return scscf.OriginalDialog{}, false, err
	}
	return d, true, nil
}

func (s *Store) PutOriginalDialog(ctx context.Context, d scscf.OriginalDialog) error {
	return kvjson.Write(ctx, s.c, s.c.Key("original-dialog", d.ID), d, s.c.TTLUntil(d.Expires))
}

func (s *Store) unregisteredKey(privateID string) string {
	return s.c.Key("unregistered", privateKey(privateID))
}

// UnregisteredProfile returns the profile of a private identity,
// whether or not it has expired (scscf.Store).
func (s *Store) UnregisteredProfile(ctx context.Context, privateID string) (scscf.UnregisteredProfile, bool, error) {
	var p scscf.UnregisteredProfile
	ok, err := read(ctx, s.c, s.unregisteredKey(privateID), &p)
	return p, ok, err
}

func (s *Store) UnregisteredProfileOf(ctx context.Context, publicID string) (scscf.UnregisteredProfile, bool, error) {
	k := ims.PublicIdentityKey(publicID)
	target, ok, err := s.indexTarget(ctx, s.c.Key("unregistered-impu", k))
	if err != nil || !ok {
		return scscf.UnregisteredProfile{}, false, err
	}
	p, ok, err := s.UnregisteredProfile(ctx, target)
	if err != nil || !ok || !s.c.Now().Before(p.Expires) || !slices.Contains(publicKeys(p.PublicIdentities), k) {
		return scscf.UnregisteredProfile{}, false, err
	}
	return p, true, nil
}

// PutUnregisteredProfile writes in the order of PutRegistration. The
// index items go at the Expires of the profile, after which
// UnregisteredProfileOf finds nothing anyway.
func (s *Store) PutUnregisteredProfile(ctx context.Context, p scscf.UnregisteredProfile) error {
	k := privateKey(p.PrivateIdentity)
	old, _, err := s.UnregisteredProfile(ctx, k)
	if err != nil {
		return err
	}
	keys := publicKeys(p.PublicIdentities)
	for _, pk := range keys {
		if err := s.c.WriteIndex(ctx, s.c.Key("unregistered-impu", pk), k, s.c.TTLUntil(p.Expires)); err != nil {
			return err
		}
	}
	if err := s.c.WriteDue(ctx, string(scscf.UnregisteredProfileExpiry), userMember(k), p.Expires); err != nil {
		return err
	}
	if err := kvjson.Write(ctx, s.c, s.unregisteredKey(k), p, 0); err != nil {
		return err
	}
	for _, pk := range publicKeys(old.PublicIdentities) {
		if !slices.Contains(keys, pk) {
			s.c.RemoveIndexIf(ctx, s.c.Key("unregistered-impu", pk), k)
		}
	}
	return nil
}

// DeleteUnregisteredProfile removes the profile only if it still
// expires at expires and no one changed it since it was read. Of two
// instances whose expiries end the same profile, one removes it and gets
// true; the other gets false and sends no SAR.
func (s *Store) DeleteUnregisteredProfile(ctx context.Context, privateID string, expires time.Time) (bool, error) {
	k := privateKey(privateID)
	key := s.unregisteredKey(k)
	for range 3 {
		var p scscf.UnregisteredProfile
		version, err := kvjson.Read(ctx, s.c, key, &p)
		if errors.Is(err, kvstore.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !p.Expires.Equal(expires) {
			return false, nil
		}
		err = s.c.RemoveIf(ctx, key, version)
		if err == nil {
			for _, pk := range publicKeys(p.PublicIdentities) {
				s.c.RemoveIndexIf(ctx, s.c.Key("unregistered-impu", pk), k)
			}
			return true, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return false, err
		}
		// Changed or removed since it was read: read it again.
	}
	return false, nil
}
