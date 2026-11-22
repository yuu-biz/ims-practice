// Package hssstore is an hss.Store on a kvstore.Store, so that several
// HSS instances share the subscribers, and SQN_HE and the repository
// data change by one atomic step whichever instance changes them.
//
// The items and their keys, after the prefix of the Config and "hss:":
//
//	subscriber:<private identity>    the Subscriber and its repository data
//	public:<public identity>         index: the private identity of the subscription
//
// and one sorted index, "subscribers", of the private identities, which
// List reads one page at a time.
//
// The identities are in the form in which hss.MemoryStore compares them:
// a private identity in lower case, a public identity as hss's
// publicKey writes it (see publicKey). Every lookup of a Cx or Sh
// request reads one or two keys.
package hssstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// Store is an hss.Store on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ hss.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
//
// hss.NewMemoryStore takes the subscriptions of the configuration. A
// Store on kvstore keeps them across restarts, so an HSS that starts
// with it adds the configured ones with Add, and takes ErrIdentityTaken
// for one that is there already.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "hss")}
}

// record is the item of one subscriber.
type record struct {
	hss.Subscriber
	// Repository is the repository data by Service-Indication (TS 29.328
	// 7.4). It is part of the subscriber's item, so that Delete removes
	// it in the same step.
	Repository map[string]sh.TransparentData
	// Provisional is true while Add is under way: from the first write,
	// which claims the private identity, until the last, once every
	// public identity is claimed. A reader takes a provisional record as
	// a miss; a writer as taken.
	Provisional bool
}

// provisionalTime is the time to live of a provisional record: Add
// takes a few round trips to the store, and an Add that stops does not
// keep the identity for longer than this.
const provisionalTime = time.Minute

// subscriberIndex is the sorted index of the private identities, in the
// form that privateKey writes them, so that List reads them in order
// without gathering them all.
const subscriberIndex = "subscribers"

// privateKey is that of hss.MemoryStore (store.go), which does not
// export it: a NAI compares without case. A public identity is keyed by
// ims.PublicIdentityKey, as in hss.MemoryStore.
func privateKey(id string) string { return strings.ToLower(id) }

func publicKey(id string) string { return ims.PublicIdentityKey(id) }

func (s *Store) subscriberKey(privateID string) string {
	return s.c.Key("subscriber", privateKey(privateID))
}

func (s *Store) publicIndexKey(publicID string) string { return s.c.Key("public", publicKey(publicID)) }

func noTTL[T any](T) time.Duration { return 0 }

// record reads a record that Add has finished, and reports whether
// there is one.
func (s *Store) record(ctx context.Context, privateID string) (record, string, bool, error) {
	var r record
	version, err := kvjson.Read(ctx, s.c, s.subscriberKey(privateID), &r)
	if errors.Is(err, kvstore.ErrNotFound) {
		return record{}, "", false, nil
	}
	if err != nil {
		return record{}, "", false, err
	}
	if r.Provisional {
		return record{}, "", false, nil
	}
	return r, version, true, nil
}

// holds reports whether r holds the public identity of key pk.
func holds(r record, pk string) bool {
	return slices.ContainsFunc(r.PublicIdentities, func(id string) bool { return publicKey(id) == pk })
}

func (s *Store) Subscriber(ctx context.Context, privateID string) (hss.Subscriber, bool, error) {
	r, _, ok, err := s.record(ctx, privateID)
	return r.Subscriber, ok, err
}

// SubscriberOf reads the index item of the public identity, then the
// subscriber it names, which must hold the identity (kvjson.WriteIndex).
func (s *Store) SubscriberOf(ctx context.Context, publicID string) (hss.Subscriber, bool, error) {
	target, _, err := s.c.ReadIndex(ctx, s.publicIndexKey(publicID))
	if errors.Is(err, kvstore.ErrNotFound) {
		return hss.Subscriber{}, false, nil
	}
	if err != nil {
		return hss.Subscriber{}, false, err
	}
	r, _, ok, err := s.record(ctx, target)
	if err != nil || !ok || !holds(r, publicKey(publicID)) {
		return hss.Subscriber{}, false, err
	}
	return r.Subscriber, true, nil
}

// Add claims the private identity with a provisional record, then each
// public identity with an index item that only one subscription can
// hold, then enters the private identity in the sorted index, and at
// last makes the record final. Every claim is a compare and swap, so of
// two Adds that share an identity, one fails with ErrIdentityTaken, on
// whichever instances they run.
//
// An index item that names a subscriber that does not hold the identity
// (one that is gone, or whose Add stopped) is taken over. One that
// names a provisional record that holds the identity is taken: that Add
// may still finish. If this Add fails, it removes what it wrote; if its
// instance stops, or the store fails again as it removes, the
// provisional record goes with its time to live, and its index items
// are taken over by the next Add.
func (s *Store) Add(ctx context.Context, sub hss.Subscription) error {
	k := privateKey(sub.PrivateIdentity)
	r := record{Repository: map[string]sh.TransparentData{}, Provisional: true}
	for _, d := range sub.RepositoryData {
		d.SequenceNumber = 0 // new data (TS 29.328 6.1.2.1)
		r.Repository[d.ServiceIndication] = d
	}
	sub.RepositoryData = nil
	r.Subscription = sub
	mainKey := s.subscriberKey(k)
	version, err := kvjson.CompareAndSwap(ctx, s.c, mainKey, "", r, provisionalTime)
	if errors.Is(err, kvstore.ErrConflict) {
		return fmt.Errorf("%w: %s", hss.ErrIdentityTaken, sub.PrivateIdentity)
	}
	if err != nil {
		return err
	}
	var claimed []claim
	undo := func() {
		// The caller's context may be what ended the Add; the removals
		// still get the Timeout of the Config.
		ctx := context.WithoutCancel(ctx)
		for _, c := range claimed {
			s.c.RemoveIf(ctx, c.key, c.version)
		}
		s.c.RemoveIf(ctx, mainKey, version)
	}
	for _, id := range sub.PublicIdentities {
		c, err := s.claimPublic(ctx, id, k)
		if err != nil {
			undo()
			return err
		}
		claimed = append(claimed, c)
	}
	if err := s.c.AddToIndex(ctx, subscriberIndex, k); err != nil {
		undo()
		return err
	}
	r.Provisional = false
	if _, err := kvjson.CompareAndSwap(ctx, s.c, mainKey, version, r, 0); err != nil {
		// The provisional record ran out, and another Add may hold the
		// identity now, or the store failed: this one has failed. Its
		// entry in the sorted index stays, for List to skip.
		undo()
		return fmt.Errorf("hssstore: add %s: %w", sub.PrivateIdentity, err)
	}
	return nil
}

// claim is an index item that Add wrote, and its version.
type claim struct{ key, version string }

// claimPublic makes the index item of publicID name the private key k,
// unless another subscription holds the identity.
func (s *Store) claimPublic(ctx context.Context, publicID, k string) (claim, error) {
	key := s.publicIndexKey(publicID)
	version := ""
	for range 10 {
		v, err := s.c.CompareAndSwapIndex(ctx, key, version, k, 0)
		if err == nil {
			return claim{key, v}, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return claim{}, err
		}
		target, current, err := s.c.ReadIndex(ctx, key)
		if errors.Is(err, kvstore.ErrNotFound) {
			version = ""
			continue
		}
		if err != nil {
			return claim{}, err
		}
		version = current
		if target == k {
			// Left by an earlier subscription of the same private
			// identity: the provisional record of this Add is the one at
			// that key now.
			continue
		}
		var other record
		_, err = kvjson.Read(ctx, s.c, s.subscriberKey(target), &other)
		if err == nil && holds(other, publicKey(publicID)) {
			return claim{}, fmt.Errorf("%w: %s", hss.ErrIdentityTaken, publicID)
		}
		if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
			return claim{}, err
		}
		// The subscriber it names does not hold the identity: take it.
	}
	return claim{}, fmt.Errorf("hssstore: claim %s: %w", publicID, kvjson.ErrGaveUp)
}

// errUnchanged makes Modify write nothing.
var errUnchanged = errors.New("hssstore: unchanged")

// Put writes back the registration state by compare and swap, keeping
// the stored AKA credentials and SQN_HE, as hss.MemoryStore does.
func (s *Store) Put(ctx context.Context, sub hss.Subscriber) error {
	_, err := kvjson.Modify(ctx, s.c, s.subscriberKey(sub.PrivateIdentity), noTTL, func(r *record) error {
		if r.Provisional {
			return errUnchanged
		}
		sub.AKA = r.AKA
		r.Subscriber = sub
		return nil
	})
	if errors.Is(err, kvstore.ErrNotFound) || errors.Is(err, errUnchanged) {
		return nil
	}
	return err
}

// swapped is the result of a Modify that changes the item only if it is
// still as the caller read it: whether it did, and the failure of the
// store. An item that is gone, or that change found changed, is no
// failure.
func swapped(err error) (bool, error) {
	if errors.Is(err, kvstore.ErrNotFound) || errors.Is(err, errUnchanged) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) CompareAndSwapSQN(ctx context.Context, privateID string, old, new aka.SQN) (bool, error) {
	_, err := kvjson.Modify(ctx, s.c, s.subscriberKey(privateID), noTTL, func(r *record) error {
		if r.Provisional || r.AKA == nil || r.AKA.SQN != old {
			return errUnchanged
		}
		r.AKA.SQN = new
		return nil
	})
	return swapped(err)
}

func (s *Store) RepositoryData(ctx context.Context, privateID, serviceIndication string) (sh.TransparentData, bool, error) {
	r, _, ok, err := s.record(ctx, privateID)
	if err != nil || !ok {
		return sh.TransparentData{}, false, err
	}
	d, found := r.Repository[serviceIndication]
	return d, found, nil
}

// CompareAndSwapRepositoryData checks old against what is stored and
// writes data in one compare and swap of the subscriber's item, so that
// two instances that both read one Sequence Number cannot both write.
func (s *Store) CompareAndSwapRepositoryData(ctx context.Context, privateID string, old *sh.TransparentData, data sh.TransparentData) (bool, error) {
	_, err := kvjson.Modify(ctx, s.c, s.subscriberKey(privateID), noTTL, func(r *record) error {
		if r.Provisional {
			return errUnchanged
		}
		stored, ok := r.Repository[data.ServiceIndication]
		if ok != (old != nil) || ok && stored.SequenceNumber != old.SequenceNumber {
			return errUnchanged
		}
		if data.ServiceData == nil {
			delete(r.Repository, data.ServiceIndication)
			return nil
		}
		if r.Repository == nil {
			r.Repository = map[string]sh.TransparentData{}
		}
		r.Repository[data.ServiceIndication] = data
		return nil
	})
	return swapped(err)
}

// deleteAttempts bounds how often Delete reads the subscriber again
// when another instance changed it between the read and the removal.
const deleteAttempts = 10

// Delete removes the subscriber with a compare and delete, then its
// index items and its entry in the sorted index. A reader in between
// finds an index item without its subscriber, which is a miss; an Add
// takes it over.
//
// Once the subscriber's item is gone, so is the subscriber, and Delete
// returns it even when the store fails on what comes after: an entry in
// either index that stays is what a Delete that stops there leaves, and
// is a miss as well.
func (s *Store) Delete(ctx context.Context, privateID string) (hss.Subscriber, bool, error) {
	k := privateKey(privateID)
	for range deleteAttempts {
		r, version, ok, err := s.record(ctx, k)
		if err != nil || !ok {
			return hss.Subscriber{}, false, err
		}
		err = s.c.RemoveIf(ctx, s.subscriberKey(k), version)
		if errors.Is(err, kvstore.ErrConflict) {
			continue
		}
		if err != nil {
			return hss.Subscriber{}, false, err
		}
		for _, id := range r.PublicIdentities {
			s.c.RemoveIndexIf(ctx, s.publicIndexKey(id), k)
		}
		s.c.RemoveFromIndex(ctx, subscriberIndex, k)
		return r.Subscriber, true, nil
	}
	return hss.Subscriber{}, false, fmt.Errorf("hssstore: delete %s: changed by others %d times: %w", privateID, deleteAttempts, kvjson.ErrGaveUp)
}

// List reads one page of the sorted index of the private identities,
// and then the subscriber of each: a page of limit subscribers costs
// one range lookup and limit reads, however many subscribers the store
// holds.
func (s *Store) List(ctx context.Context, after string, limit int) ([]hss.Subscriber, string, error) {
	if limit < 1 {
		return nil, "", nil
	}
	ids, err := s.c.MembersAfter(ctx, subscriberIndex, privateKey(after), limit)
	if err != nil {
		return nil, "", err
	}
	subs := make([]hss.Subscriber, 0, len(ids))
	for _, id := range ids {
		// An entry whose subscriber is gone, or is still provisional, is
		// one that an Add or a Delete has not finished with.
		sub, ok, err := s.Subscriber(ctx, id)
		if err != nil {
			return nil, "", err
		}
		if ok {
			subs = append(subs, sub)
		}
	}
	next := ""
	if len(ids) == limit {
		next = ids[len(ids)-1]
	}
	return subs, next, nil
}
