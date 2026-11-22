// Package ibcfstore is an ibcf.Store on a kvstore.Store, so that an
// IBCF instance that starts again under the same name, on another
// process or host, finds the records of the calls that its earlier
// process left, and ends them.
//
// Each instance owns its records: no other instance reads or writes
// them. The items and their keys, after the prefix of the Config and
// "ibcf:":
//
//	call:<instance>:<ID>   the CallRecord, as JSON
//	calls-of:<instance>    index: the IDs of the calls of the instance
//
// An instance that starts reads its index and then each record in it,
// and never walks the keys of the store, which other instances and
// roles share. The index holds at most as many IDs as the instance has
// calls at once, so it does not grow with the number of users or of
// instances.
//
// Neither item has a time to live. A call lasts until its BYE, however
// long that is, as in ibcf.MemoryStore; the records of an instance that
// never starts again stay, as their TrGW contexts do.
package ibcfstore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Store is an ibcf.Store on a kvstore.Store.
//
// The calls of one instance write their records at once, and all of
// them change the index of the instance: it is changed by compare and
// swap, so that none of them loses another's entry.
type Store struct {
	c   *kvjson.Client
	log *slog.Logger
}

var _ ibcf.Store = (*Store)(nil)

// New returns a Store on kv. Instances with the same kv and Prefix share
// it, each under its own name.
func New(kv kvstore.Store, cfg kvjson.Config) *Store {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Store{c: kvjson.New(kv, cfg, "ibcf"), log: log}
}

func (s *Store) callKey(instance, id string) string { return s.c.Key("call", instance, id) }

func (s *Store) indexKey(instance string) string { return s.c.Key("calls-of", instance) }

// callsOf is the value of the index of an instance: the IDs of its
// calls, in order.
type callsOf struct {
	IDs []string
}

// maxAttempts bounds the rounds of a change of the index that other
// writers keep overtaking, as in kvjson.
const maxAttempts = 100

var errGaveUp = errors.New("ibcfstore: the index of the instance could not be changed")

// PutCall writes the ID of r in the index of its instance, and then r.
// The index comes first so that an instance that stops between the two
// writes leaves at worst an entry without a record, which CallsOf skips:
// never a record that CallsOf cannot find, whose TrGW context a later
// process would not release. The IBCF writes a record again after each
// request within its call; the index is then read but not written.
func (s *Store) PutCall(ctx context.Context, r ibcf.CallRecord) error {
	err := s.changeIndex(ctx, r.Instance, func(ix *callsOf) bool {
		i, found := slices.BinarySearch(ix.IDs, r.ID)
		if found {
			return false
		}
		ix.IDs = slices.Insert(ix.IDs, i, r.ID)
		return true
	})
	if err != nil {
		return fmt.Errorf("ibcfstore: put call %s of %s: %w", r.ID, r.Instance, err)
	}
	return kvjson.Write(ctx, s.c, s.callKey(r.Instance, r.ID), r, 0)
}

// DeleteCall removes the record, and then its entry in the index, in
// the opposite order to PutCall and for the same reason.
func (s *Store) DeleteCall(ctx context.Context, instance, id string) error {
	if err := s.c.Remove(ctx, s.callKey(instance, id)); err != nil {
		return err
	}
	if err := s.removeFromIndex(ctx, instance, []string{id}); err != nil {
		return fmt.Errorf("ibcfstore: delete call %s of %s: %w", id, instance, err)
	}
	return nil
}

// CallsOf reads the index of the instance, and then each record in it.
// A failure of the store is returned, not taken for an instance without
// calls: the IBCF would leave the calls of its earlier process up.
//
// An entry without a record was left by a process that stopped between
// the two writes of PutCall or of DeleteCall. The IBCF reads its records
// when it starts, before it writes any, and no other instance writes
// them, so no PutCall can be under way: CallsOf skips such an entry, and
// removes it from the index. A failed removal changes nothing that
// CallsOf returns, so it is only logged; the next CallsOf tries again.
func (s *Store) CallsOf(ctx context.Context, instance string) ([]ibcf.CallRecord, error) {
	var ix callsOf
	_, err := kvjson.Read(ctx, s.c, s.indexKey(instance), &ix)
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	records := make([]ibcf.CallRecord, 0, len(ix.IDs))
	var leftovers []string
	for _, id := range ix.IDs {
		var r ibcf.CallRecord
		_, err := kvjson.Read(ctx, s.c, s.callKey(instance, id), &r)
		switch {
		case errors.Is(err, kvstore.ErrNotFound):
			leftovers = append(leftovers, id)
		case err != nil:
			return nil, err
		default:
			records = append(records, r)
		}
	}
	if len(leftovers) > 0 {
		if err := s.removeFromIndex(ctx, instance, leftovers); err != nil {
			s.log.Warn("cannot remove index entries without a record", "key", s.indexKey(instance), "calls", leftovers, "error", err.Error())
		}
	}
	return records, nil
}

// removeFromIndex takes ids out of the index of the instance.
func (s *Store) removeFromIndex(ctx context.Context, instance string, ids []string) error {
	return s.changeIndex(ctx, instance, func(ix *callsOf) bool {
		n := len(ix.IDs)
		ix.IDs = slices.DeleteFunc(ix.IDs, func(id string) bool { return slices.Contains(ids, id) })
		return len(ix.IDs) < n
	})
}

// changeIndex reads the index of the instance, lets change change it,
// and writes it back if no one else wrote it in between; otherwise it
// starts again from the new value, so change may run more than once.
// change reports whether it changed anything; when it did not, nothing
// is written. An index left empty is removed, so that an instance
// without calls leaves no key behind.
func (s *Store) changeIndex(ctx context.Context, instance string, change func(*callsOf) bool) error {
	key := s.indexKey(instance)
	for range maxAttempts {
		var ix callsOf
		version, err := kvjson.Read(ctx, s.c, key, &ix)
		if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
			return err
		}
		if !change(&ix) {
			return nil
		}
		switch {
		case len(ix.IDs) > 0:
			_, err = kvjson.CompareAndSwap(ctx, s.c, key, version, ix, 0)
		case version != "":
			err = s.c.RemoveIf(ctx, key, version)
		default:
			return nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return err
		}
	}
	return errGaveUp
}
