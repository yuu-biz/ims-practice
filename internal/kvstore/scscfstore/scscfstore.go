// Package scscfstore is a scscf.Store on a kvstore.Store, so that the
// registrar state of the S-CSCF lives outside the node and several
// S-CSCF instances share it.
//
// The items and their keys, after the prefix of the Config and
// "scscf:", with the private identity in lower case as scscf.MemoryStore
// compares it:
//
//	challenge:<private identity>            Challenge, time to live 2 x reg-await-auth
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
// The four timers of the S-CSCF are time indexes (see Timer). An item
// whose end needs an action (a SAR, a NOTIFY) keeps no time to live of
// its own: the instance that claims its timer reads it, acts, and
// removes it. The items that only have to go away have one.
package scscfstore

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Config is the configuration of the Store.
type Config struct {
	kvjson.Config
	// RegAwaitAuth is the reg-await-auth timer, as scscf.Config has it;
	// zero means ims.RegAwaitAuth. The Store schedules the timer when a
	// challenge is put.
	RegAwaitAuth time.Duration
}

// Store is a scscf.Store on a kvstore.Store.
type Store struct {
	c            *kvjson.Client
	regAwaitAuth time.Duration
}

var _ scscf.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg Config) *Store {
	if cfg.RegAwaitAuth == 0 {
		cfg.RegAwaitAuth = ims.RegAwaitAuth
	}
	return &Store{c: kvjson.New(kv, cfg.Config, "scscf"), regAwaitAuth: cfg.RegAwaitAuth}
}

// Timer names a time index of the Store: one of the timers of the
// S-CSCF that scscf runs with time.AfterFunc in its MemoryStore.
type Timer string

const (
	// RegAwaitAuthTimer is reg-await-auth (TS 24.229 5.4.1.2.1, Table
	// 7.7.1). A member is ChallengeMember: the private identity and the
	// nonce, so that the timer of an answered challenge never ends a
	// later one. PutChallenge schedules it.
	RegAwaitAuthTimer Timer = "reg-await-auth"
	// RegistrationTimer is the expiry of a registration (TS 24.229
	// 5.4.1.2.2A step 10, 5.4.2.1.2). A member is the private identity
	// in lower case; PutRegistration schedules it at Expires.
	RegistrationTimer Timer = "registration-expiry"
	// SubscriptionTimer is the expiry of a subscription to the reg event
	// package (RFC 6665 4.2.2). A member is SubscriptionMember;
	// PutSubscription schedules it at Expires.
	SubscriptionTimer Timer = "subscription-expiry"
	// UnregisteredProfileTimer is the end of an unregistered profile
	// (scscf.UnregisteredProfileLifetime). A member is the private
	// identity in lower case; PutUnregisteredProfile schedules it.
	UnregisteredProfileTimer Timer = "unregistered-profile-expiry"
)

// expiryGrace is how long an index item outlives the Expires of the item
// it points to. The item itself stays until the instance that claims
// its timer has acted, which takes a lease and a Cx round trip (5 s)
// or a few if that instance
// stops; the index item has to find it until then. After that it goes
// by itself, so that an index item that a stopped instance left
// behind does not stay for ever.
const expiryGrace = 10 * time.Minute

// ClaimDue claims at most limit members of timer that are due at now,
// each for lease (kvstore.Store.ClaimDue). The caller acts on each
// claim as the timer of scscf would, and then calls Done.
//
// A claim does not prove that the item has expired: an instance may
// have refreshed it after it was due, or removed it, and the Store does
// not take a removed item out of the index (see DeleteRegistration).
// So the caller reads the item and checks its Expires first, as the
// timer functions of scscf already do (registrationExpired compares
// Expires, regAwaitAuthExpired the nonce). Of two instances that act on
// the same item, the user lock and those checks let one through; the
// lease makes it rare.
func (s *Store) ClaimDue(ctx context.Context, timer Timer, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	return s.c.ClaimDue(ctx, string(timer), now, lease, limit)
}

// Done ends a claim of ClaimDue once the caller has acted on it.
func (s *Store) Done(ctx context.Context, claim kvstore.Claim) error { return s.c.Done(ctx, claim) }

// ChallengeMember is the member of RegAwaitAuthTimer for a challenge.
func ChallengeMember(privateID, nonce string) string {
	return kvjson.Member(privateKey(privateID), nonce)
}

// ParseChallengeMember gives back the private identity, in lower case,
// and the nonce.
func ParseChallengeMember(member string) (privateID, nonce string) {
	parts := kvjson.SplitMember(member)
	if len(parts) != 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// SubscriptionMember is the member of SubscriptionTimer for a
// subscription.
func SubscriptionMember(id sip.DialogID) string {
	return kvjson.Member(id.CallID, id.LocalTag, id.RemoteTag)
}

// ParseSubscriptionMember gives back the dialog of a subscription.
func ParseSubscriptionMember(member string) (sip.DialogID, bool) {
	parts := kvjson.SplitMember(member)
	if len(parts) != 3 {
		return sip.DialogID{}, false
	}
	return sip.DialogID{CallID: parts[0], LocalTag: parts[1], RemoteTag: parts[2]}, true
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

func (s *Store) Challenge(privateID string) (scscf.Challenge, bool) {
	var c scscf.Challenge
	_, ok := kvjson.Get(s.c, s.c.Key("challenge", privateKey(privateID)), &c)
	return c, ok
}

// PutChallenge stores c and starts its reg-await-auth timer. The timer
// goes into the index first: an instance that stops before the challenge
// is written leaves a member that ends nothing, since no challenge has
// its nonce. The challenge itself goes after twice the timer: by then
// the timer has ended it, or the authentication has.
func (s *Store) PutChallenge(c scscf.Challenge) {
	s.c.Schedule(string(RegAwaitAuthTimer), ChallengeMember(c.PrivateIdentity, c.Nonce), s.c.Now().Add(s.regAwaitAuth))
	kvjson.Put(s.c, s.c.Key("challenge", privateKey(c.PrivateIdentity)), c, 2*s.regAwaitAuth)
}

// DeleteChallenge removes the challenge and its timer. Its member names
// the nonce, so a later challenge of the user keeps its own timer.
func (s *Store) DeleteChallenge(privateID string) {
	c, ok := s.Challenge(privateID)
	s.c.Delete(s.c.Key("challenge", privateKey(privateID)))
	if ok {
		s.c.Unschedule(string(RegAwaitAuthTimer), ChallengeMember(c.PrivateIdentity, c.Nonce))
	}
}

func (s *Store) Registration(privateID string) (scscf.Registration, bool) {
	var r scscf.Registration
	_, ok := kvjson.Get(s.c, s.c.Key("registration", privateKey(privateID)), &r)
	return r, ok
}

// RegistrationOf reads the index item of the public identity, then the
// registration it names. A registration that does not hold the
// identity is a miss: the index item is one that a writer has not
// finished with, or one that a stopped instance left (kvjson.PutIndex).
func (s *Store) RegistrationOf(publicID string) (scscf.Registration, bool) {
	k := ims.PublicIdentityKey(publicID)
	target, _, ok := s.c.IndexTarget(s.c.Key("impu", k))
	if !ok {
		return scscf.Registration{}, false
	}
	r, ok := s.Registration(target)
	if !ok || !slices.Contains(publicKeys(r.PublicIdentities), k) {
		return scscf.Registration{}, false
	}
	return r, true
}

// PutRegistration writes, in this order: the index items of the public
// identities, the timer, the registration, and then removes the index
// items of the identities that the registration no longer has. A reader
// in between, or after an instance stopped in between, finds either the
// old registration or the new one, and never one by an identity it
// does not hold. Two instances that write the same registration at once
// end with the one that wrote last, as with scscf.MemoryStore; an index
// item of the other is a miss.
func (s *Store) PutRegistration(r scscf.Registration) {
	k := privateKey(r.PrivateIdentity)
	old, _ := s.Registration(k)
	keys := publicKeys(r.PublicIdentities)
	for _, pk := range keys {
		s.c.PutIndex(s.c.Key("impu", pk), k, s.indexTTL(r.Expires))
	}
	s.c.Schedule(string(RegistrationTimer), k, r.Expires)
	kvjson.Put(s.c, s.c.Key("registration", k), r, 0)
	for _, pk := range publicKeys(old.PublicIdentities) {
		if !slices.Contains(keys, pk) {
			s.c.DeleteIndexIf(s.c.Key("impu", pk), k)
		}
	}
}

// DeleteRegistration removes the registration first and its index items
// after, so that an index item never leads to a registration that is
// gone. It leaves the timer: the member is the private identity, and a
// registration that another instance writes at the same moment would
// lose its own timer with it. The claim of that member finds no
// registration, or one with a later Expires, and does nothing.
func (s *Store) DeleteRegistration(privateID string) {
	k := privateKey(privateID)
	old, ok := s.Registration(k)
	s.c.Delete(s.c.Key("registration", k))
	if !ok {
		return
	}
	for _, pk := range publicKeys(old.PublicIdentities) {
		s.c.DeleteIndexIf(s.c.Key("impu", pk), k)
	}
}

func (s *Store) subscriptionKey(id sip.DialogID) string {
	return s.c.Key("subscription", id.CallID, id.LocalTag, id.RemoteTag)
}

func (s *Store) Subscription(id sip.DialogID) (scscf.Subscription, bool) {
	var sub scscf.Subscription
	_, ok := kvjson.Get(s.c, s.subscriptionKey(id), &sub)
	return sub, ok
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
// instance that claimed its timer; if it has not, SubscriptionsOf would
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

// SubscriptionsOf reads the set of the private identity, and then each
// subscription in it. A subscription that is missing, or that reports
// another registration, is left out.
func (s *Store) SubscriptionsOf(privateID string) []scscf.Subscription {
	k := privateKey(privateID)
	var set subscriptionsOf
	if _, ok := kvjson.Get(s.c, s.c.Key("subscriptions-of", k), &set); !ok {
		return nil
	}
	var subs []scscf.Subscription
	for _, r := range set.Subscriptions {
		if sub, ok := s.Subscription(r.ID); ok && privateKey(sub.PrivateIdentity) == k {
			subs = append(subs, sub)
		}
	}
	return subs
}

// PutSubscription adds the subscription to the set of its registration,
// then schedules its expiry, then writes it. A reader in between finds
// an entry without a subscription, and leaves it out.
func (s *Store) PutSubscription(sub scscf.Subscription) {
	id := sub.Dialog.ID
	setKey := s.c.Key("subscriptions-of", privateKey(sub.PrivateIdentity))
	if _, err := kvjson.Upsert(s.c, setKey, s.setTTL, func(set *subscriptionsOf) {
		set.removeStale(s.c.Now())
		set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(r subscriptionRef) bool { return r.ID == id })
		set.Subscriptions = append(set.Subscriptions, subscriptionRef{ID: id, Expires: sub.Expires})
	}); err != nil {
		return
	}
	s.c.Schedule(string(SubscriptionTimer), SubscriptionMember(id), sub.Expires)
	kvjson.Put(s.c, s.subscriptionKey(id), sub, 0)
}

// DeleteSubscription removes the subscription, then its entry in the set
// and its timer. A dialog is never used again, so the timer can go.
func (s *Store) DeleteSubscription(id sip.DialogID) {
	sub, ok := s.Subscription(id)
	s.c.Delete(s.subscriptionKey(id))
	s.c.Unschedule(string(SubscriptionTimer), SubscriptionMember(id))
	if !ok {
		return
	}
	setKey := s.c.Key("subscriptions-of", privateKey(sub.PrivateIdentity))
	kvjson.Update(s.c, setKey, s.setTTL, func(set *subscriptionsOf) error {
		set.Subscriptions = slices.DeleteFunc(set.Subscriptions, func(r subscriptionRef) bool { return r.ID == id })
		return nil
	})
}

// OriginalDialog returns the identifier until its Expires, by the clock
// of the Store, as scscf.MemoryStore does; the time to live removes it
// soon after.
func (s *Store) OriginalDialog(id string) (scscf.OriginalDialog, bool) {
	var d scscf.OriginalDialog
	if _, ok := kvjson.Get(s.c, s.c.Key("original-dialog", id), &d); !ok || !s.c.Now().Before(d.Expires) {
		return scscf.OriginalDialog{}, false
	}
	return d, true
}

func (s *Store) PutOriginalDialog(d scscf.OriginalDialog) {
	kvjson.Put(s.c, s.c.Key("original-dialog", d.ID), d, s.c.TTLUntil(d.Expires))
}

// UnregisteredProfile returns the profile of a private identity,
// whether or not it has expired. scscf.Store has no such lookup; the
// instance that claims UnregisteredProfileTimer needs it, since its
// member is the private identity, and the timer function of scscf
// takes the profile.
func (s *Store) UnregisteredProfile(privateID string) (scscf.UnregisteredProfile, bool) {
	var p scscf.UnregisteredProfile
	_, ok := kvjson.Get(s.c, s.c.Key("unregistered", privateKey(privateID)), &p)
	return p, ok
}

func (s *Store) UnregisteredProfileOf(publicID string) (scscf.UnregisteredProfile, bool) {
	k := ims.PublicIdentityKey(publicID)
	target, _, ok := s.c.IndexTarget(s.c.Key("unregistered-impu", k))
	if !ok {
		return scscf.UnregisteredProfile{}, false
	}
	p, ok := s.UnregisteredProfile(target)
	if !ok || !s.c.Now().Before(p.Expires) || !slices.Contains(publicKeys(p.PublicIdentities), k) {
		return scscf.UnregisteredProfile{}, false
	}
	return p, true
}

// PutUnregisteredProfile writes in the order of PutRegistration. The
// index items go at the Expires of the profile, after which
// UnregisteredProfileOf finds nothing anyway.
func (s *Store) PutUnregisteredProfile(p scscf.UnregisteredProfile) {
	k := privateKey(p.PrivateIdentity)
	old, _ := s.UnregisteredProfile(k)
	keys := publicKeys(p.PublicIdentities)
	for _, pk := range keys {
		s.c.PutIndex(s.c.Key("unregistered-impu", pk), k, s.c.TTLUntil(p.Expires))
	}
	s.c.Schedule(string(UnregisteredProfileTimer), k, p.Expires)
	kvjson.Put(s.c, s.c.Key("unregistered", k), p, 0)
	for _, pk := range publicKeys(old.PublicIdentities) {
		if !slices.Contains(keys, pk) {
			s.c.DeleteIndexIf(s.c.Key("unregistered-impu", pk), k)
		}
	}
}

// DeleteUnregisteredProfile removes the profile only if it still
// expires at expires and no one changed it since it was read. Of two
// instances whose timers end the same profile, one removes it and gets
// true; the other gets false and sends no SAR.
func (s *Store) DeleteUnregisteredProfile(privateID string, expires time.Time) bool {
	k := privateKey(privateID)
	key := s.c.Key("unregistered", k)
	for range 3 {
		var p scscf.UnregisteredProfile
		version, ok := kvjson.Get(s.c, key, &p)
		if !ok || !p.Expires.Equal(expires) {
			return false
		}
		if s.c.DeleteIf(key, version) {
			for _, pk := range publicKeys(p.PublicIdentities) {
				s.c.DeleteIndexIf(s.c.Key("unregistered-impu", pk), k)
			}
			return true
		}
		// Changed or removed since it was read: read it again.
	}
	return false
}
