// Package ibcfstore is an ibcf.Store on a kvstore.Store, so that an
// IBCF instance that starts again under the same name, on another
// process or host, finds the records of the calls that its earlier
// process left, and ends them.
//
// Each instance owns its records: no other instance writes them. The
// items and their keys, after the prefix of the Config and "ibcf:":
//
//	call:<instance>:<ID>                       the CallRecord, as JSON
//	dialog:<Call-ID>:<local tag>:<remote tag>  index: <instance>:<ID> of the call that has the dialog
//
// and the sorted index of the IDs of the calls of each instance:
//
//	call-ids:<instance>
//
// A call that starts adds its ID to the sorted index, and one that ends
// takes it out: one member each, whatever else the index holds, so the
// calls of an instance neither read the list of its calls nor wait for
// each other to change it. An instance that starts reads its index one
// page at a time, and then each record in it, and never walks the keys
// of the store, which other instances and roles share. The index holds
// at most as many IDs as the instance has calls at once, so it does not
// grow with the number of users or of instances. Any instance reads one
// record by one of its dialogs, two keys, for the Replaces of an INVITE
// (ibcf.Store.CallOf).
//
// No item has a time to live. A call lasts until its BYE, however long
// that is, as in ibcf.MemoryStore; the records of an instance that
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
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Store is an ibcf.Store on a kvstore.Store.
//
// The calls of one instance write their records at once. Each adds or
// removes only its own member of the index of the instance, which the
// store changes in one step, so that none of them loses another's entry
// or has to try again.
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

// callIndex is the name of the sorted index of the IDs of the calls of
// instance.
func callIndex(instance string) string { return "call-ids:" + kvjson.Member(instance) }

func (s *Store) dialogKey(id sip.DialogID) string {
	return s.c.Key("dialog", id.CallID, id.LocalTag, id.RemoteTag)
}

// page is how many IDs one read of the index of an instance returns.
const page = 100

// PutCall adds the ID of r to the index of its instance, then writes r,
// and then the entry of each of its dialogs. The index comes first so
// that an instance that stops between the two writes leaves at worst an
// entry without a record, which CallsOf skips: never a record that
// CallsOf cannot find, whose TrGW context a later process would not
// release. The IBCF writes a record again after each request within its
// call; its ID is then in the index already, and adding it again
// changes nothing.
//
// The entries of the dialogs come after the record, unlike the other
// index items of the adapters (kvjson.WriteIndex), because the record
// is what DeleteCall reads to find them. An instance that stops in
// between leaves a record whose dialogs CallOf does not find yet, never
// an entry that no DeleteCall removes. The entries are written again
// with the record each time, so the next write that gets through puts
// them back.
func (s *Store) PutCall(ctx context.Context, r ibcf.CallRecord) error {
	if err := s.c.AddToIndex(ctx, callIndex(r.Instance), r.ID); err != nil {
		return fmt.Errorf("ibcfstore: put call %s of %s: %w", r.ID, r.Instance, err)
	}
	if err := kvjson.Write(ctx, s.c, s.callKey(r.Instance, r.ID), r, 0); err != nil {
		return err
	}
	for _, id := range r.DialogIDs() {
		if err := s.c.WriteIndex(ctx, s.dialogKey(id), kvjson.Member(r.Instance, r.ID), 0); err != nil {
			return fmt.Errorf("ibcfstore: put the dialog %s of call %s of %s: %w", id.CallID, r.ID, r.Instance, err)
		}
	}
	return nil
}

// DeleteCall reads the record for its dialogs, removes their entries,
// then the record, and then its entry in the index: the opposite order
// to PutCall, and for the same reasons. An instance that stops before
// the record is gone leaves it in the index, and the next process of the
// instance ends the call again and removes the rest.
func (s *Store) DeleteCall(ctx context.Context, instance, id string) error {
	var r ibcf.CallRecord
	if _, err := kvjson.Read(ctx, s.c, s.callKey(instance, id), &r); err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		return err
	}
	for _, d := range r.DialogIDs() {
		if err := s.c.RemoveIndexIf(ctx, s.dialogKey(d), kvjson.Member(instance, id)); err != nil {
			return fmt.Errorf("ibcfstore: delete the dialog %s of call %s of %s: %w", d.CallID, id, instance, err)
		}
	}
	if err := s.c.Remove(ctx, s.callKey(instance, id)); err != nil {
		return err
	}
	if err := s.c.RemoveFromIndex(ctx, callIndex(instance), id); err != nil {
		return fmt.Errorf("ibcfstore: delete call %s of %s: %w", id, instance, err)
	}
	return nil
}

// CallOf reads the entry of the dialog, and then the record that it
// names. An entry whose record is gone, or does not have the dialog, is
// a miss, as it is for kvjson.ReadIndex.
func (s *Store) CallOf(ctx context.Context, id sip.DialogID) (ibcf.CallRecord, bool, error) {
	target, _, err := s.c.ReadIndex(ctx, s.dialogKey(id))
	if errors.Is(err, kvstore.ErrNotFound) {
		return ibcf.CallRecord{}, false, nil
	}
	if err != nil {
		return ibcf.CallRecord{}, false, err
	}
	parts := kvjson.SplitMember(target)
	if len(parts) != 2 {
		return ibcf.CallRecord{}, false, nil
	}
	var r ibcf.CallRecord
	_, err = kvjson.Read(ctx, s.c, s.callKey(parts[0], parts[1]), &r)
	switch {
	case errors.Is(err, kvstore.ErrNotFound):
		return ibcf.CallRecord{}, false, nil
	case err != nil:
		return ibcf.CallRecord{}, false, err
	case !slices.Contains(r.DialogIDs(), id):
		return ibcf.CallRecord{}, false, nil
	}
	return r, true, nil
}

// CallsOf reads the index of the instance one page at a time, and the
// record of each ID in it. A failure of the store is returned, not taken
// for an instance without calls: the IBCF would leave the calls of its
// earlier process up.
//
// An entry without a record was left by a process that stopped between
// the two writes of PutCall or of DeleteCall. The IBCF reads its records
// when it starts, before it writes any, and no other instance writes
// them. This assumes one live process per instance name: with a second
// one, a PutCall of the old process could be under way, and its record
// would be left out of the index until that call is written again.
// CallsOf skips such an entry, and removes it from the index once it has
// read every page, so that a store that fails then has given every
// record already. A failed removal changes nothing that CallsOf returns,
// so it is only logged; the next CallsOf tries again.
func (s *Store) CallsOf(ctx context.Context, instance string) ([]ibcf.CallRecord, error) {
	index := callIndex(instance)
	var records []ibcf.CallRecord
	var leftovers []string
	for after := ""; ; {
		ids, err := s.c.MembersAfter(ctx, index, after, page)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
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
		if len(ids) < page {
			break
		}
		after = ids[len(ids)-1]
	}
	for i, id := range leftovers {
		if err := s.c.RemoveFromIndex(ctx, index, id); err != nil {
			s.log.Warn("cannot remove index entries without a record", "index", s.c.Index(index), "calls", leftovers[i:], "error", err.Error())
			break
		}
	}
	return records, nil
}
