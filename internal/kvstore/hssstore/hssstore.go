// Package hssstore is an hss.Store on a kvstore.Store, so that several
// HSS instances share the subscribers, and SQN_HE and the repository
// data change by one atomic step whichever instance changes them.
//
// The items and their keys, after the prefix of the Config and "hss:":
//
//	subscriber:<private identity>    the Subscriber and its repository data
//	public:<public identity>         index: the private identity of the subscription
//	settings:                        the hss.StoredSettings of the instances
//
// and these sorted indexes of private identities, which are read one
// page at a time:
//
//	subscribers                      every subscriber, for List
//	served:<profile source>          those whose user profile an S-CSCF
//	                                 has, one index per source of the
//	                                 service profile name, for
//	                                 ListServed
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
// for one that is there already, or that the Add of another instance
// holds; WaitForAdd tells the two apart.
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

// servedIndex is the sorted index of the private identities of the
// subscribers whose user profile an S-CSCF has and whose service
// profile name comes from source. One index per source keeps the key of
// a subscriber the same when a setting names another profile, and lets
// a change of one setting read its own subscribers alone
// (hss.Store.ListServed).
//
// Nothing fills these indexes for the subscribers of a store that was
// written before them: a store holds no version of its own, and an
// instance that started would have to read every subscriber to fill
// them, which is work that grows with the network and which no
// operation here does. Those subscribers enter the index when their
// state or their source next changes, and until then a change of a
// setting pushes them no profile; they get it at the next registration.
func servedIndex(source hss.ProfileSource) string { return "served:" + string(source) }

// The writes that change what a subscriber's item says of the index
// put k in the index of its new source before they write the item
// (addServed), and take it out of the others after (dropServed). An
// instance that stops anywhere in between leaves k in an index too
// many, which costs a push of the profile the S-CSCF has already
// (pushProfile reads the subscriber before it sends), and never an item
// that names a source whose index lacks k, which would miss the push of
// a changed setting. The latter could not be put right by the request
// being sent again: a PUT that finds the subscription as the body has
// it answers unchanged and writes nothing.

// addServed puts k in the index of source; the empty source asks for
// none.
func (s *Store) addServed(ctx context.Context, k string, source hss.ProfileSource) error {
	if source == "" {
		return nil
	}
	return s.c.AddToIndex(ctx, servedIndex(source), k)
}

// setServed makes the indexes of the served subscribers say what the
// subscriber of the private key k says: k stands in the index of
// source, and in none of the others; the empty source leaves it in
// none at all.
func (s *Store) setServed(ctx context.Context, k string, source hss.ProfileSource) error {
	if err := s.addServed(ctx, k, source); err != nil {
		return err
	}
	return s.dropServed(ctx, k, source)
}

// dropServed takes k out of the index of each source but source.
func (s *Store) dropServed(ctx context.Context, k string, source hss.ProfileSource) error {
	for _, other := range hss.ProfileSources {
		if other == source {
			continue
		}
		if err := s.c.RemoveFromIndex(ctx, servedIndex(other), k); err != nil {
			return err
		}
	}
	return nil
}

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

// holds reports whether ids, the public identities of a subscription,
// hold the one of key pk.
func holds(ids []string, pk string) bool {
	return slices.ContainsFunc(ids, func(id string) bool { return publicKey(id) == pk })
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
	if err != nil || !ok || !holds(r.PublicIdentities, publicKey(publicID)) {
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

// addPoll is how often WaitForAdd reads a provisional record again.
const addPoll = time.Second

// WaitForAdd waits until no Add holds the private identity, and reports
// whether a subscriber of it is stored then. An Add holds it with its
// provisional record from its first write to its last, and lets it go
// when it finishes or gives up; the Add of an instance that stopped lets
// it go when the record runs out with its time to live. WaitForAdd reads
// the record again every addPoll, for no longer than provisionalTime,
// the most that the record lasts, and reports no subscriber if it is
// still provisional then: that is the record of an Add that started
// since.
//
// An HSS that starts with the subscribers of its configuration calls it
// when the Add of one of them finds the private identity taken.
func (s *Store) WaitForAdd(ctx context.Context, privateID string) (bool, error) {
	longest := time.NewTimer(provisionalTime)
	defer longest.Stop()
	poll := time.NewTicker(addPoll)
	defer poll.Stop()
	for {
		var r record
		_, err := kvjson.Read(ctx, s.c, s.subscriberKey(privateID), &r)
		switch {
		case errors.Is(err, kvstore.ErrNotFound):
			return false, nil
		case err != nil:
			return false, err
		case !r.Provisional:
			return true, nil
		}
		select {
		case <-poll.C:
		case <-longest.C:
			return false, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
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
		if err == nil && holds(other.PublicIdentities, publicKey(publicID)) {
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
// the stored AKA credentials and SQN_HE, as hss.MemoryStore does. The
// subscriber goes in the index of the served subscribers that the state
// it writes asks for before the write, and out of the others after it
// (addServed). An item that is not there, or that an Add has not
// finished with, is not written, and the index entry that went in
// before is one too many, which nothing misses.
func (s *Store) Put(ctx context.Context, sub hss.Subscriber) error {
	k := privateKey(sub.PrivateIdentity)
	source := hss.ServedSource(sub)
	if err := s.addServed(ctx, k, source); err != nil {
		return err
	}
	_, err := kvjson.Modify(ctx, s.c, s.subscriberKey(k), noTTL, func(r *record) error {
		if r.Provisional {
			return errUnchanged
		}
		sub.AKA = r.AKA
		r.Subscriber = sub
		return nil
	})
	written, err := swapped(err)
	if err != nil || !written {
		return err
	}
	return s.dropServed(ctx, k, source)
}

// Replace writes the whole subscriber, then claims each public identity
// that the subscription did not hold, the way Add does, and then drops
// the index items of the identities it has let go. The repository data
// of the item stays as it is, and so does SQN_HE, which
// CompareAndSwapSQN alone changes.
//
// The item comes before the claims, because the item is what says which
// identities a subscription holds. claimPublic takes over an index item
// whose subscriber does not hold the identity, so of two Replaces
// claiming one identity, each would otherwise read the other's item from
// before its write, find the identity not held there, take the index
// item over, and leave both subscriptions holding it. Add is in no such
// race: its provisional record names the identities from its first
// write. The cost is that the item names an identity while the claim is
// under way, so another Replace that comes along in that window is told
// the identity is taken even when this one goes on to give it back.
//
// The write is a read, modify and write of the item, not a compare and
// swap against what the caller read: an update of SQN_HE or of the
// repository data runs without the subscriber's lock, so one that lands
// in between is to be kept, not to fail the Replace.
func (s *Store) Replace(ctx context.Context, sub hss.Subscriber) error {
	k := privateKey(sub.PrivateIdentity)
	stored, _, ok, err := s.record(ctx, k)
	if err != nil || !ok {
		return err
	}
	source := hss.ServedSource(sub)
	if err := s.addServed(ctx, k, source); err != nil {
		return err
	}
	written, err := s.writeSubscriber(ctx, k, sub)
	if err != nil || !written {
		// The store failed, the subscriber went, or an Add has not
		// finished with it.
		return err
	}
	for _, id := range sub.PublicIdentities {
		if holds(stored.PublicIdentities, publicKey(id)) {
			continue
		}
		if _, err := s.claimPublic(ctx, id, k); err != nil {
			// The identity is not this subscription's to take, so neither
			// is the item that says it holds it. The caller's context may
			// be what ended the Replace; the writes that put the
			// subscription back still get the Timeout of the Config.
			undo := context.WithoutCancel(ctx)
			if _, undoErr := s.writeSubscriber(undo, k, stored.Subscriber); undoErr != nil {
				return fmt.Errorf("hssstore: replace %s: %w: putting the subscription back: %w", sub.PrivateIdentity, err, undoErr)
			}
			for _, claimed := range sub.PublicIdentities {
				if !holds(stored.PublicIdentities, publicKey(claimed)) {
					s.c.RemoveIndexIf(undo, s.publicIndexKey(claimed), k)
				}
			}
			return err
		}
	}
	for _, id := range stored.PublicIdentities {
		if !holds(sub.PublicIdentities, publicKey(id)) {
			s.c.RemoveIndexIf(ctx, s.publicIndexKey(id), k)
		}
	}
	// The mark and the service profile name of the subscription are
	// written here too, so the source of its profile name may be another
	// one now. The old source's entry goes only for a Replace that stood:
	// the undo above puts back the subscription that entry is for.
	return s.dropServed(ctx, k, source)
}

// writeSubscriber puts sub in the item of the private key k, keeping the
// stored SQN_HE and the repository data of the item. It reports whether
// it wrote: an item that is gone, or that an Add has not finished with,
// takes nothing.
func (s *Store) writeSubscriber(ctx context.Context, k string, sub hss.Subscriber) (bool, error) {
	sub.RepositoryData = nil // the repository data is the item's own field
	_, err := kvjson.Modify(ctx, s.c, s.subscriberKey(k), noTTL, func(r *record) error {
		if r.Provisional {
			return errUnchanged
		}
		next := sub
		if next.AKA != nil && r.AKA != nil {
			credentials := *next.AKA
			credentials.SQN = r.AKA.SQN
			next.AKA = &credentials
		}
		r.Subscriber = next
		return nil
	})
	return swapped(err)
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

// AllRepositoryData reads the repository data of the subscriber's item,
// which holds every Service-Indication of the subscription: one read,
// however many subscriptions the store holds.
func (s *Store) AllRepositoryData(ctx context.Context, privateID string) ([]sh.TransparentData, error) {
	r, _, ok, err := s.record(ctx, privateID)
	if err != nil || !ok {
		return nil, err
	}
	data := make([]sh.TransparentData, 0, len(r.Repository))
	for _, d := range r.Repository {
		data = append(data, d)
	}
	slices.SortFunc(data, func(a, b sh.TransparentData) int { return strings.Compare(a.ServiceIndication, b.ServiceIndication) })
	return data, nil
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
// index items and its entries in the sorted indexes. A reader in between
// finds an index item without its subscriber, which is a miss; an Add
// of another subscription takes it over. An Add of the same private
// identity would take it over too, and then lose it and its entry in
// the sorted index to the rest of the Delete, which is why the caller
// of either holds the lock of the subscriber (hss.Store.Add).
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
		s.setServed(ctx, k, "")
		return r.Subscriber, true, nil
	}
	return hss.Subscriber{}, false, fmt.Errorf("hssstore: delete %s: changed by others %d times: %w", privateID, deleteAttempts, kvjson.ErrGaveUp)
}

// Counts keeps none of the numbers of TS 32.409 4.2.1, so it reports
// false and the HSS on this store serves no database usage
// measurement. The subscribers belong to the store, which every
// instance of the HSS shares, so the numbers are the store's to keep
// and no instance can work them out from what it did itself: one that
// starts and finds the subscribers of its configuration already there
// has added none of them. Keeping them here would mean a counter item
// per number, which every Add and Delete changes and which no step of
// this store repairs when an instance stops between the two writes.
func (s *Store) Counts(context.Context) (hss.Counts, bool, error) {
	return hss.Counts{}, false, nil
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

// settingsKey is the item of the settings that the instances share: one
// item, which every instance reads once per sweep, whatever the number
// of subscribers.
func (s *Store) settingsKey() string { return s.c.Key("settings") }

func (s *Store) Settings(ctx context.Context) (hss.StoredSettings, bool, error) {
	var stored hss.StoredSettings
	_, err := kvjson.Read(ctx, s.c, s.settingsKey(), &stored)
	if errors.Is(err, kvstore.ErrNotFound) {
		return hss.StoredSettings{}, false, nil
	}
	if err != nil {
		return hss.StoredSettings{}, false, err
	}
	return stored, true, nil
}

// CompareAndSwapSettings reads the item and writes next at the version
// read, if the item still holds old: a write of another instance in
// between changes the version, and the swap does not happen.
func (s *Store) CompareAndSwapSettings(ctx context.Context, old *hss.StoredSettings, next hss.StoredSettings) (bool, error) {
	var stored hss.StoredSettings
	version, err := kvjson.Read(ctx, s.c, s.settingsKey(), &stored)
	switch {
	case errors.Is(err, kvstore.ErrNotFound):
		if old != nil {
			return false, nil
		}
	case err != nil:
		return false, err
	case old == nil || stored != *old:
		return false, nil
	}
	_, err = kvjson.CompareAndSwap(ctx, s.c, s.settingsKey(), version, next, 0)
	if errors.Is(err, kvstore.ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

// ListServed reads one page of the sorted index of the served
// subscribers of source: one range lookup, however many subscribers the
// store holds. The subscribers themselves are not read here, as the
// caller reads each one when it pushes its profile
// (hss.Store.ListServed).
func (s *Store) ListServed(ctx context.Context, source hss.ProfileSource, after string, limit int) ([]string, string, error) {
	if limit < 1 || source == "" {
		return nil, "", nil
	}
	ids, err := s.c.MembersAfter(ctx, servedIndex(source), privateKey(after), limit)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(ids) == limit {
		next = ids[len(ids)-1]
	}
	return ids, next, nil
}
