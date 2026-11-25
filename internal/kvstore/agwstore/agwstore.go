// Package agwstore is an agw.ContextStore on a kvstore.Store, so that a
// media relay (the IMS-AGW, or the MRFP) recreated at the same address
// finds its contexts and opens the same ports again, and the media goes
// on after a gap.
//
// Each relay instance owns its contexts: no other instance reads or
// writes them. The items and their keys, after the prefix of the Config
// and "agw:<relay>:", where <relay> is Config.Relay:
//
//	context:<context ID>   the SavedContext, as JSON
//
// and the sorted index of the IDs of the relay's contexts:
//
//	context-ids
//
// A context that the relay adds puts its ID in the sorted index, and one
// that it subtracts takes it out: one member each, whatever else the
// index holds. A Modify writes the context item again, and adds its
// member again, which changes nothing. None of them reads the list of
// the other contexts or waits for them to change it. A relay that restarts reads its index
// one page at a time, and then each context in it, and never walks the
// keys of the store, which other relays and roles share. The index
// holds at most as many IDs as the relay's port range has contexts, so
// it does not grow with the number of users.
//
// Neither the items nor the index has a time to live. A context lasts until the relay
// subtracts it, as in agw.MemoryContextStore; one whose call ended
// without a BYE that the P-CSCF saw stays, as its ports stay open in
// the relay.
package agwstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Config is what New takes.
type Config struct {
	// Prefix, Timeout and Log are as for every adapter. Nothing here has
	// an expiry, so Now is not used.
	kvjson.Config
	// Relay is the name of the relay instance, the same each time the
	// instance is created again: its host name in Compose or the name of
	// its pod. Two relays with different names on one store do not see
	// each other's contexts.
	Relay string
}

// Store is an agw.ContextStore on a kvstore.Store.
//
// For one context, the relay calls PutContext and DeleteContext one at
// a time, as it changes a context under the context's own lock. Calls
// for different contexts may overlap: each adds or removes only its own
// member of the index, which the store changes in one step, so that
// none of them loses another's entry or has to try again.
type Store struct {
	kv      kvstore.Store
	prefix  string // "<Prefix>agw:<relay>:"
	timeout time.Duration
	log     *slog.Logger
}

var _ agw.ContextStore = (*Store)(nil)

// New returns the Store of the relay cfg.Relay on kv. A Store created
// again with the same kv, Prefix and Relay finds what the earlier one
// saved.
func New(kv kvstore.Store, cfg Config) (*Store, error) {
	if cfg.Relay == "" {
		return nil, errors.New("agwstore: the relay needs a name")
	}
	s := &Store{
		kv: kv, prefix: cfg.Prefix + "agw:" + kvjson.Member(cfg.Relay) + ":",
		timeout: cfg.Timeout, log: cfg.Log,
	}
	if s.timeout == 0 {
		s.timeout = time.Second
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	return s, nil
}

func (s *Store) contextKey(id string) string { return s.prefix + "context:" + kvjson.Member(id) }

// index is the name of the sorted index of the IDs of the relay's
// contexts.
func (s *Store) index() string { return s.prefix + "context-ids" }

// page is how many IDs one read of the index returns.
const page = 100

// PutContext adds the ID of c to the index, and then writes c. The index
// comes first so that a relay that stops between the two writes leaves
// at worst an entry without a context, which Contexts skips: never a
// context that Contexts cannot find, whose ports a recreated relay would
// not open again. For a context that is there already, as after a
// Modify, adding its ID again changes nothing.
func (s *Store) PutContext(ctx context.Context, c agw.SavedContext) error {
	value, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("agwstore: put context %s: %w", c.ID, err)
	}
	if err := s.addToIndex(ctx, c.ID); err != nil {
		return fmt.Errorf("agwstore: put context %s: %w", c.ID, err)
	}
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if _, err := s.kv.Put(opCtx, s.contextKey(c.ID), value, 0); err != nil {
		return fmt.Errorf("agwstore: put context %s: %w", c.ID, err)
	}
	return nil
}

// DeleteContext removes the context, and then its entry in the index,
// in the opposite order to PutContext and for the same reason.
func (s *Store) DeleteContext(ctx context.Context, id string) error {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if err := s.kv.Delete(opCtx, s.contextKey(id)); err != nil {
		return fmt.Errorf("agwstore: delete context %s: %w", id, err)
	}
	if err := s.removeFromIndex(ctx, id); err != nil {
		return fmt.Errorf("agwstore: delete context %s: %w", id, err)
	}
	return nil
}

// Contexts reads the index one page at a time, and then each context in
// it, in the order of their IDs. A failure of the store is returned, not
// taken for a relay without contexts: a relay that restored only part of
// its contexts would close the media of the others without a word.
//
// An entry without a context was left by a relay that stopped between
// the two writes of a PutContext or of a DeleteContext. The relay reads
// its contexts when it starts, before it serves anything, and no other
// process writes them. This assumes one live process per relay name,
// which the deployment has to keep: nothing here enforces it, and the
// index is read before any port is opened. With a second process, a
// PutContext of the first could be under way, and its context would be
// left out of the index for good. Contexts skips such an entry, and
// removes it from the index once it has read every page, so that a
// store that fails then has given every context already. A failed
// removal changes nothing that Contexts returns, so it is only logged;
// the next Contexts tries again.
func (s *Store) Contexts(ctx context.Context) ([]agw.SavedContext, error) {
	var saved []agw.SavedContext
	var leftovers []string
	for after := ""; ; {
		ids, err := s.idsAfter(ctx, after)
		if err != nil {
			return nil, fmt.Errorf("agwstore: read the index: %w", err)
		}
		for _, id := range ids {
			c, ok, err := s.readContext(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("agwstore: read context %s: %w", id, err)
			}
			if ok {
				saved = append(saved, c)
			} else {
				leftovers = append(leftovers, id)
			}
		}
		if len(ids) < page {
			break
		}
		after = ids[len(ids)-1]
	}
	if len(leftovers) > 0 {
		s.repair(ctx, leftovers)
	}
	return saved, nil
}

// repair removes the leftover entries from the index.
func (s *Store) repair(ctx context.Context, leftovers []string) {
	for i, id := range leftovers {
		if err := s.removeFromIndex(ctx, id); err != nil {
			s.log.Warn("cannot remove index entries without a context", "index", s.index(), "contexts", leftovers[i:], "error", err.Error())
			return
		}
	}
	s.log.Info("removed index entries without a context", "index", s.index(), "contexts", leftovers)
}

func (s *Store) readContext(ctx context.Context, id string) (agw.SavedContext, bool, error) {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	item, err := s.kv.Get(opCtx, s.contextKey(id))
	if errors.Is(err, kvstore.ErrNotFound) {
		return agw.SavedContext{}, false, nil
	}
	if err != nil {
		return agw.SavedContext{}, false, err
	}
	var c agw.SavedContext
	if err := json.Unmarshal(item.Value, &c); err != nil {
		return agw.SavedContext{}, false, fmt.Errorf("decode: %w", err)
	}
	return c, true, nil
}

func (s *Store) addToIndex(ctx context.Context, id string) error {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.kv.AddToIndex(opCtx, s.index(), id)
}

func (s *Store) removeFromIndex(ctx context.Context, id string) error {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.kv.RemoveFromIndex(opCtx, s.index(), id)
}

// idsAfter reads the page of the index that follows after.
func (s *Store) idsAfter(ctx context.Context, after string) ([]string, error) {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	return s.kv.MembersAfter(opCtx, s.index(), after, page)
}
