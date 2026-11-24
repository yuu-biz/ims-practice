package oamstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// The keys of the watches, after the prefix of the Config and "oam:":
//
//	watch-lease:<node>    the instance that holds the watch of the node, time to live of a lease
//	watch-state:<node>    oam.AlarmWatchState, no time to live
//	presence:<instance>   that the OAM instance runs, time to live of a lease
//
// and the sorted index presence of the instances that have said they
// run.
//
// The store keeps every time: an item with a time to live goes away
// when it runs out, and no instance reads its own clock to decide
// whether a lease has ended.

func (s *Store) leaseKey(node string) string        { return s.c.Key("watch-lease", node) }
func (s *Store) stateKey(node string) string        { return s.c.Key("watch-state", node) }
func (s *Store) presenceKey(instance string) string { return s.c.Key("presence", instance) }

var _ oam.AlarmWatchStore = (*Store)(nil)

// WatchLease is how long a watch and a presence last unless they are
// renewed. oam.New checks that the instances renew them often enough.
func (s *Store) WatchLease() time.Duration { return s.lease }

// maxAttempts bounds how often Watch starts again because an earlier
// holder wrote the state in between. kvjson.Modify has the same bound,
// but answers with the value it wrote and not its version, which the
// watch needs to fence its later writes; so the loop is written here.
const maxAttempts = 100

// Watch takes the lease of the node, with a compare and swap that writes
// it only if there is none, then raises the generation of the state
// with a compare and swap at the version it read. Between the read and
// the write it renews the lease at the version it took: an instance
// that stopped for longer than a lease after it took it may find
// another holder there, which has raised the generation already, and
// must not raise it once more over that holder's state. A renewal that
// fails means just that, and the watch is not taken. Should another
// instance take the lease after the renewal, it raises the generation
// after it, and the compare and swap of this one fails, or this one's
// Save does later. Unlike a file, an
// item of the store is written whole or not at all, so a state that
// cannot be read is a failure of the store, which the caller logs and
// tries again. Every later Save is a
// compare and swap at the version that the last write of this holder
// left, so that once another instance has raised the generation, the
// earlier holder can no longer write.
func (s *Store) Watch(ctx context.Context, node, instance string) (oam.AlarmWatch, bool, error) {
	leaseVersion, err := kvjson.CompareAndSwap(ctx, s.c, s.leaseKey(node), "", instance, s.lease)
	if errors.Is(err, kvstore.ErrConflict) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	w := &watch{s: s, node: node, instance: instance, leaseVersion: leaseVersion}
	for range maxAttempts {
		var st oam.AlarmWatchState
		version, err := kvjson.Read(ctx, s.c, s.stateKey(node), &st)
		if errors.Is(err, kvstore.ErrNotFound) {
			version, err = "", nil
		}
		if err != nil {
			w.Release(context.WithoutCancel(ctx))
			return nil, false, err
		}
		if st.Seed == "" {
			st.Seed = randomHex(8)
		}
		st.Generation++
		if err := w.Renew(ctx); errors.Is(err, oam.ErrWatchLost) {
			// Another instance holds the lease now: the watch is its.
			return nil, false, nil
		} else if err != nil {
			w.Release(context.WithoutCancel(ctx))
			return nil, false, err
		}
		version, err = kvjson.CompareAndSwap(ctx, s.c, s.stateKey(node), version, st, 0)
		if errors.Is(err, kvstore.ErrConflict) {
			// Another holder wrote in between. The renewal at the next
			// try finds whether it holds the lease now.
			continue
		}
		if err != nil {
			w.Release(context.WithoutCancel(ctx))
			return nil, false, err
		}
		w.state, w.stateVersion = st, version
		return w, true, nil
	}
	w.Release(context.WithoutCancel(ctx))
	return nil, false, kvjson.ErrGaveUp
}

func (s *Store) AlarmWatchState(ctx context.Context, node string) (oam.AlarmWatchState, error) {
	var st oam.AlarmWatchState
	_, err := kvjson.Read(ctx, s.c, s.stateKey(node), &st)
	if errors.Is(err, kvstore.ErrNotFound) {
		return oam.AlarmWatchState{}, nil
	}
	return st, err
}

// presencePage is how many instances one read of the index returns.
const presencePage = 100

// presenceIndex is the sorted index of the instances that have said
// they run.
const presenceIndex = "presence"

// Present writes the presence of the instance for one lease, puts it in
// the index, and counts the members of the index whose presence has
// not run out. A member whose presence has gone, an instance that
// stopped, leaves the index.
func (s *Store) Present(ctx context.Context, instance string) (int, error) {
	if err := kvjson.Write(ctx, s.c, s.presenceKey(instance), true, s.lease); err != nil {
		return 0, err
	}
	if err := s.c.AddToIndex(ctx, presenceIndex, instance); err != nil {
		return 0, err
	}
	present := 0
	for after := ""; ; {
		members, err := s.c.MembersAfter(ctx, presenceIndex, after, presencePage)
		if err != nil {
			return 0, err
		}
		for _, m := range members {
			var alive bool
			_, err := kvjson.Read(ctx, s.c, s.presenceKey(m), &alive)
			switch {
			case errors.Is(err, kvstore.ErrNotFound):
				if err := s.c.RemoveFromIndex(ctx, presenceIndex, m); err != nil {
					return 0, err
				}
			case err != nil:
				return 0, err
			default:
				present++
			}
		}
		if len(members) < presencePage {
			return present, nil
		}
		after = members[len(members)-1]
	}
}

// watch is the watch of one node that this instance holds.
type watch struct {
	s        *Store
	node     string
	instance string
	state    oam.AlarmWatchState

	// mu guards the versions, which Save and Renew change from the
	// goroutines of the watcher.
	mu           sync.Mutex
	leaseVersion string
	stateVersion string
}

func (w *watch) State() oam.AlarmWatchState { return w.state }

func (w *watch) Save(ctx context.Context, st oam.AlarmWatchState) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.Generation != w.state.Generation {
		return oam.ErrWatchLost
	}
	version, err := kvjson.CompareAndSwap(ctx, w.s.c, w.s.stateKey(w.node), w.stateVersion, st, 0)
	if errors.Is(err, kvstore.ErrConflict) {
		return oam.ErrWatchLost
	}
	if err != nil {
		return err
	}
	w.stateVersion = version
	return nil
}

// Renew writes the lease again at the version it has, with a new time
// to live. A lease that has run out, or that another instance holds,
// is no longer at that version.
func (w *watch) Renew(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	version, err := kvjson.CompareAndSwap(ctx, w.s.c, w.s.leaseKey(w.node), w.leaseVersion, w.instance, w.s.lease)
	if errors.Is(err, kvstore.ErrConflict) {
		return oam.ErrWatchLost
	}
	if err != nil {
		return err
	}
	w.leaseVersion = version
	return nil
}

// Release removes the lease if it is still the one this holder wrote.
func (w *watch) Release(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.s.c.RemoveIf(ctx, w.s.leaseKey(w.node), w.leaseVersion)
	if errors.Is(err, kvstore.ErrConflict) || errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	return err
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
