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
//	contexts               index: the IDs of the relay's contexts
//
// A relay that restarts reads its index and then each context in it,
// and never walks the keys of the store, which other relays and roles
// share. The index holds at
// most as many IDs as the relay's port range has contexts, so it does
// not grow with the number of users.
//
// Neither item has a time to live. A context lasts until the relay
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
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Config is what New takes.
type Config struct {
	// Prefix, Timeout and Log are as for every adapter. Now is the clock
	// that dates the entries of the index (see repairGrace).
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
// for different contexts may overlap: the index is changed by compare
// and swap, so none of them loses another's entry.
type Store struct {
	kv      kvstore.Store
	prefix  string // "<Prefix>agw:<relay>:"
	timeout time.Duration
	log     *slog.Logger
	now     func() time.Time
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
		timeout: cfg.Timeout, log: cfg.Log, now: cfg.Now,
	}
	if s.timeout == 0 {
		s.timeout = time.Second
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s, nil
}

func (s *Store) contextKey(id string) string { return s.prefix + "context:" + kvjson.Member(id) }

func (s *Store) indexKey() string { return s.prefix + "contexts" }

// index is the value of the index item: the relay's contexts, in the
// order of their IDs.
type index struct {
	Contexts []indexEntry
}

type indexEntry struct {
	ID string
	// Saved is when the last PutContext of the context began. An entry
	// whose context item is missing, and that is older than
	// repairGrace, is left by a writer that stopped between its two
	// writes.
	Saved time.Time
}

// repairGrace is how long an entry without a context item is taken for
// a PutContext under way, between its write of the entry and its write
// of the item. After the entry comes only the write of the item,
// bounded by the Timeout of the Config (a second by default), so a
// minute is far longer than a PutContext that is running takes.
const repairGrace = time.Minute

// maxAttempts bounds the rounds of a change of the index that other
// writers keep overtaking, as in kvjson.
const maxAttempts = 100

var errGaveUp = errors.New("agwstore: the index of the relay could not be changed")

// PutContext writes the entry of c in the index, and then c. The index
// comes first so that a relay that stops between the two writes leaves
// at worst an entry without a context, which Contexts skips: never a
// context that Contexts cannot find, whose ports a recreated relay would
// not open again.
//
// The entry is written again on every PutContext, with a new Saved, even
// when it is there already. Contexts then never takes the entry of a
// context that is being saved for a leftover: a writer that pauses for
// longer than repairGrace between its two writes is the only way to
// lose a context.
func (s *Store) PutContext(ctx context.Context, c agw.SavedContext) error {
	saved := s.now()
	err := s.changeIndex(ctx, func(ix *index) bool {
		ix.Contexts = slices.DeleteFunc(ix.Contexts, func(e indexEntry) bool { return e.ID == c.ID })
		ix.Contexts = append(ix.Contexts, indexEntry{ID: c.ID, Saved: saved})
		slices.SortFunc(ix.Contexts, func(a, b indexEntry) int { return strings.Compare(a.ID, b.ID) })
		return true
	})
	if err != nil {
		return fmt.Errorf("agwstore: put context %s: %w", c.ID, err)
	}
	value, err := json.Marshal(c)
	if err != nil {
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
	err := s.changeIndex(ctx, func(ix *index) bool {
		n := len(ix.Contexts)
		ix.Contexts = slices.DeleteFunc(ix.Contexts, func(e indexEntry) bool { return e.ID == id })
		return len(ix.Contexts) < n
	})
	if err != nil {
		return fmt.Errorf("agwstore: delete context %s: %w", id, err)
	}
	return nil
}

// Contexts reads the index, and then each context in it, in the order of
// their IDs. An entry without a context is skipped; when it is older
// than repairGrace, Contexts also removes it from the index. A failure
// of the store is returned, not taken for a relay without contexts: a
// relay that restored only part of its contexts would close the media
// of the others without a word.
func (s *Store) Contexts(ctx context.Context) ([]agw.SavedContext, error) {
	ix, _, err := s.readIndex(ctx)
	if err != nil {
		return nil, fmt.Errorf("agwstore: read the index: %w", err)
	}
	saved := make([]agw.SavedContext, 0, len(ix.Contexts))
	var leftovers []indexEntry
	for _, e := range ix.Contexts {
		c, ok, err := s.readContext(ctx, e.ID)
		if err != nil {
			return nil, fmt.Errorf("agwstore: read context %s: %w", e.ID, err)
		}
		if ok {
			saved = append(saved, c)
		} else if s.now().Sub(e.Saved) > repairGrace {
			leftovers = append(leftovers, e)
		}
	}
	if len(leftovers) > 0 {
		s.repair(ctx, leftovers)
	}
	return saved, nil
}

// repair removes the leftover entries from the index. An entry that a
// PutContext has written again since it was read has a new Saved, and
// stays. A failed repair changes nothing that Contexts returns, so it
// is only logged; the next Contexts tries again.
func (s *Store) repair(ctx context.Context, leftovers []indexEntry) {
	err := s.changeIndex(ctx, func(ix *index) bool {
		n := len(ix.Contexts)
		ix.Contexts = slices.DeleteFunc(ix.Contexts, func(e indexEntry) bool {
			return slices.ContainsFunc(leftovers, func(l indexEntry) bool { return l.ID == e.ID && l.Saved.Equal(e.Saved) })
		})
		return len(ix.Contexts) < n
	})
	ids := make([]string, len(leftovers))
	for i, e := range leftovers {
		ids[i] = e.ID
	}
	if err != nil {
		s.log.Warn("cannot remove index entries without a context", "key", s.indexKey(), "contexts", ids, "error", err.Error())
		return
	}
	s.log.Info("removed index entries without a context", "key", s.indexKey(), "contexts", ids)
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

// readIndex returns the index and its version, "" when there is none.
func (s *Store) readIndex(ctx context.Context) (index, string, error) {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	item, err := s.kv.Get(opCtx, s.indexKey())
	if errors.Is(err, kvstore.ErrNotFound) {
		return index{}, "", nil
	}
	if err != nil {
		return index{}, "", err
	}
	var ix index
	if err := json.Unmarshal(item.Value, &ix); err != nil {
		return index{}, "", fmt.Errorf("decode: %w", err)
	}
	return ix, item.Version, nil
}

// changeIndex reads the index, lets change change it, and writes it back
// if no one else wrote it in between; otherwise it starts again from the
// new value, so change may run more than once. change reports whether it
// changed anything; when it did not, nothing is written. An index left
// empty is removed, so that a relay without contexts leaves no key
// behind.
func (s *Store) changeIndex(ctx context.Context, change func(*index) bool) error {
	for range maxAttempts {
		ix, version, err := s.readIndex(ctx)
		if err != nil {
			return err
		}
		if !change(&ix) {
			return nil
		}
		err = s.writeIndex(ctx, version, ix)
		if !errors.Is(err, kvstore.ErrConflict) {
			return err
		}
	}
	return errGaveUp
}

func (s *Store) writeIndex(ctx context.Context, version string, ix index) error {
	opCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	if len(ix.Contexts) == 0 {
		if version == "" {
			return nil
		}
		return s.kv.CompareAndDelete(opCtx, s.indexKey(), version)
	}
	value, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	_, err = s.kv.CompareAndSwap(opCtx, s.indexKey(), version, value, 0)
	return err
}
