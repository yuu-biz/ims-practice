// Package oamstore is what the OAM server keeps on a kvstore.Store, so
// that several OAM instances share it: the subscriptions of the OSSs to
// the alarm notifications (oam.NtfSubscriptionStore), which the OSS
// creates through one instance and another instance may send the
// notifications of.
//
// One item, after the prefix of the Config and "oam:":
//
//	ntf-subscriptions   oam.NtfSubscriptionSet, every subscription by its id, no time to live
//
// The subscriptions are one item, since their number is bounded
// (oam.Config.MaxNtfSubscriptions) and follows the number of OSSs: a
// change reads the set and writes it back with a compare and swap, so
// that the bound holds when two instances create subscriptions at once,
// and a read of every subscription is one read.
package oamstore

import (
	"context"
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// Store is the state of the OAM on a kvstore.Store.
type Store struct {
	c *kvjson.Client
}

var _ oam.NtfSubscriptionStore = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// their state.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	return &Store{c: kvjson.New(kv, cfg, "oam")}
}

func (s *Store) subscriptionsKey() string { return s.c.Key("ntf-subscriptions") }

// readSubscriptions reads the set and its version; an empty set and no
// version when there is no item yet.
func (s *Store) readSubscriptions(ctx context.Context) (oam.NtfSubscriptionSet, string, error) {
	set := oam.NtfSubscriptionSet{}
	version, err := kvjson.Read(ctx, s.c, s.subscriptionsKey(), &set)
	if errors.Is(err, kvstore.ErrNotFound) {
		return oam.NtfSubscriptionSet{}, "", nil
	}
	return set, version, err
}

// changeSubscriptions reads the set, lets change change it, and writes
// it back if no one else wrote it in between; otherwise it starts again
// from the new set (kvjson.Modify). Nothing is written when change
// fails. Before the first subscription there is no item, which Modify
// does not create, so the empty set is written first, only if there is
// still none.
func (s *Store) changeSubscriptions(ctx context.Context, change func(oam.NtfSubscriptionSet) error) error {
	modify := func() error {
		_, err := kvjson.Modify(ctx, s.c, s.subscriptionsKey(), func(oam.NtfSubscriptionSet) time.Duration { return 0 },
			func(set *oam.NtfSubscriptionSet) error {
				if *set == nil {
					*set = oam.NtfSubscriptionSet{}
				}
				return change(*set)
			})
		return err
	}
	err := modify()
	if !errors.Is(err, kvstore.ErrNotFound) {
		return err
	}
	_, err = kvjson.CompareAndSwap(ctx, s.c, s.subscriptionsKey(), "", oam.NtfSubscriptionSet{}, 0)
	if err != nil && !errors.Is(err, kvstore.ErrConflict) {
		return err
	}
	return modify()
}

func (s *Store) PutNtfSubscription(ctx context.Context, sub oam.NtfSubscriptionControl, max int) (created bool, err error) {
	err = s.changeSubscriptions(ctx, func(set oam.NtfSubscriptionSet) error {
		created, err = set.Put(sub, max)
		return err
	})
	return created, err
}

func (s *Store) NtfSubscription(ctx context.Context, id string) (oam.NtfSubscriptionControl, error) {
	set, _, err := s.readSubscriptions(ctx)
	if err != nil {
		return oam.NtfSubscriptionControl{}, err
	}
	return set.Get(id)
}

func (s *Store) DeleteNtfSubscription(ctx context.Context, id string) error {
	return s.changeSubscriptions(ctx, func(set oam.NtfSubscriptionSet) error { return set.Delete(id) })
}

func (s *Store) NtfSubscriptions(ctx context.Context) ([]oam.NtfSubscriptionControl, error) {
	set, _, err := s.readSubscriptions(ctx)
	if err != nil {
		return nil, err
	}
	return set.Sorted(), nil
}
