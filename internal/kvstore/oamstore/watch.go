package oamstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// The keys of the watches, after the prefix of the Config and "oam:":
//
//	watch-lease:<node>    the watch that holds the node (lease), time to live of a lease
//	watch-state:<node>    oam.AlarmWatchState, no time to live
//	presence:<instance>   that the OAM instance runs, with the nodes it cannot reach (presence), time to live of a lease
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
	holder := lease{Instance: instance, Token: randomHex(8)}
	leaseVersion, err := kvjson.CompareAndSwap(ctx, s.c, s.leaseKey(node), "", holder, s.lease)
	if errors.Is(err, kvstore.ErrConflict) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	w := &watch{s: s, node: node, holder: holder, leaseVersion: leaseVersion}
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

// presence is the value of the presence of an instance.
type presence struct {
	// Unreachable are the nodes that the instance cannot reach.
	Unreachable []string `json:"unreachable,omitempty"`
}

// Present writes the presence of the instance for one lease, puts it in
// the index, and reads the members of the index whose presence has not
// run out. A member whose presence has gone, an instance that stopped,
// leaves the index. It reads the presence of every member, as only its
// item says whether it has run out: a call costs one read per instance,
// and the calls of all the instances, one each, the square of their
// number. The nodes that an instance cannot reach are in its presence,
// so that each instance writes its own item, and they cost no more
// reads.
//
// An instance of an earlier version wrote true as its presence, which
// does not say which nodes it cannot reach. Its presence counts as one
// that reaches every node, so that the instances of the two versions
// share the watches while they run side by side.
func (s *Store) Present(ctx context.Context, instance string, unreachable []string) (oam.Presence, error) {
	if err := kvjson.Write(ctx, s.c, s.presenceKey(instance), presence{Unreachable: unreachable}, s.lease); err != nil {
		return oam.Presence{}, err
	}
	if err := s.c.AddToIndex(ctx, presenceIndex, instance); err != nil {
		return oam.Presence{}, err
	}
	p := oam.Presence{Unreachable: map[string]int{}}
	for after := ""; ; {
		members, err := s.c.MembersAfter(ctx, presenceIndex, after, presencePage)
		if err != nil {
			return oam.Presence{}, err
		}
		for _, m := range members {
			var v presence
			_, err := kvjson.Read(ctx, s.c, s.presenceKey(m), &v)
			switch {
			case errors.Is(err, kvstore.ErrNotFound):
				if err := s.c.RemoveFromIndex(ctx, presenceIndex, m); err != nil {
					return oam.Presence{}, err
				}
			case undecodable(err):
				s.log.Warn("the presence of an OAM instance cannot be read, as one of an earlier version; it counts as one that reaches every node",
					"instance", m, "error", err.Error())
				p.Instances++
			case err != nil:
				return oam.Presence{}, err
			default:
				p.Instances++
				for _, node := range v.Unreachable {
					p.Unreachable[node]++
				}
			}
		}
		if len(members) < presencePage {
			return p, nil
		}
		after = members[len(members)-1]
	}
}

// undecodable says whether err is the failure to decode a value that the
// store gave, rather than a failure of the store.
func undecodable(err error) bool {
	var typeErr *json.UnmarshalTypeError
	var syntaxErr *json.SyntaxError
	return errors.As(err, &typeErr) || errors.As(err, &syntaxErr)
}

// lease is the value of the lease of a node: the watch that holds it.
type lease struct {
	// Instance is the OAM instance that holds the watch.
	Instance string `json:"instance"`
	// Token is random, chosen when the watch is taken, so that a lease
	// tells the watch that wrote it from any other: one of another
	// process with the same instance name, or an earlier watch of the
	// same instance.
	Token string `json:"token"`
}

// watch is the watch of one node that this instance holds.
type watch struct {
	s      *Store
	node   string
	holder lease
	state  oam.AlarmWatchState

	// mu guards the versions, which Save and Renew change from the
	// goroutines of the watcher.
	mu           sync.Mutex
	leaseVersion string
	stateVersion string
}

func (w *watch) State() oam.AlarmWatchState { return w.state }

// A write of the holder may reach the store while its answer does not
// reach the holder, as when the request runs out of time on its way
// back. The store then has a version that the holder never got, and the
// next compare and swap at the version the holder has fails. So one that
// fails reads the item again, and goes on at the version it finds if the
// holder wrote what is there (ownState, ownLease).

func (w *watch) Save(ctx context.Context, st oam.AlarmWatchState) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.Generation != w.state.Generation {
		return oam.ErrWatchLost
	}
	version, err := kvjson.CompareAndSwap(ctx, w.s.c, w.s.stateKey(w.node), w.stateVersion, st, 0)
	if errors.Is(err, kvstore.ErrConflict) {
		found, own, rerr := w.ownState(ctx)
		if rerr != nil {
			return rerr
		}
		if !own {
			return oam.ErrWatchLost
		}
		w.stateVersion = found
		version, err = kvjson.CompareAndSwap(ctx, w.s.c, w.s.stateKey(w.node), w.stateVersion, st, 0)
	}
	if errors.Is(err, kvstore.ErrConflict) {
		return oam.ErrWatchLost
	}
	if err != nil {
		return err
	}
	w.stateVersion = version
	return nil
}

// ownState reads the version of the state, and says whether this
// holder wrote it: whether it is of the generation that this holder
// raised, with the seed that it had. Another holder writes a state only
// after it has raised the generation, so no other writes this one. A
// store that was emptied in between starts the generations again, and
// a holder that takes the watch then writes the first generation with a
// new seed.
func (w *watch) ownState(ctx context.Context) (version string, own bool, err error) {
	var st oam.AlarmWatchState
	version, err = kvjson.Read(ctx, w.s.c, w.s.stateKey(w.node), &st)
	if errors.Is(err, kvstore.ErrNotFound) {
		return "", false, nil
	}
	own = err == nil && st.Generation == w.state.Generation && st.Seed == w.state.Seed
	return version, own, err
}

// Renew writes the lease again at the version it has, with a new time
// to live. A lease that has run out, or that another instance holds,
// is no longer at that version.
func (w *watch) Renew(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	version, err := kvjson.CompareAndSwap(ctx, w.s.c, w.s.leaseKey(w.node), w.leaseVersion, w.holder, w.s.lease)
	if errors.Is(err, kvstore.ErrConflict) {
		found, own, rerr := w.ownLease(ctx)
		if rerr != nil {
			return rerr
		}
		if !own {
			return oam.ErrWatchLost
		}
		w.leaseVersion = found
		version, err = kvjson.CompareAndSwap(ctx, w.s.c, w.s.leaseKey(w.node), w.leaseVersion, w.holder, w.s.lease)
	}
	if errors.Is(err, kvstore.ErrConflict) {
		return oam.ErrWatchLost
	}
	if err != nil {
		return err
	}
	w.leaseVersion = version
	return nil
}

// ownLease reads the version of the lease, and says whether this holder
// wrote it: whether it has the token of this watch. A watch takes a
// lease only where there is none, so a lease with its token is the one
// it took and renewed without a break. One that ran out is gone, and no
// compare and swap at its version writes it again. A lease that an
// earlier version wrote, with the name of an instance alone, is not
// this watch's.
func (w *watch) ownLease(ctx context.Context) (version string, own bool, err error) {
	var holder lease
	version, err = kvjson.Read(ctx, w.s.c, w.s.leaseKey(w.node), &holder)
	if errors.Is(err, kvstore.ErrNotFound) || undecodable(err) {
		return "", false, nil
	}
	return version, err == nil && holder == w.holder, err
}

// Release removes the lease if it is still the one this holder wrote.
func (w *watch) Release(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := w.s.c.RemoveIf(ctx, w.s.leaseKey(w.node), w.leaseVersion)
	if errors.Is(err, kvstore.ErrConflict) {
		found, own, rerr := w.ownLease(ctx)
		if rerr != nil || !own {
			return rerr
		}
		err = w.s.c.RemoveIf(ctx, w.s.leaseKey(w.node), found)
	}
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
