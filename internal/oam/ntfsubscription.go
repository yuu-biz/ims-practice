package oam

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// NtfSubscriptionControl is a subscription of an OSS to the alarm
// notifications, as the store keeps it: the id that the OSS gave it and
// its attributes as the OSS wrote them, after the OAM checked them (TS
// 28.622 4.3.22). The number of them follows the number of OSSs, never
// that of the subscribers.
type NtfSubscriptionControl struct {
	ID         string                               `json:"id"`
	Attributes mns.NtfSubscriptionControlAttributes `json:"attributes"`
	// Missed says that a notification to the subscription could not be
	// sent. Until the recipient has taken a notifyAlarmListRebuilt that
	// asks the OSS to read the list again, it is sent nothing else. The
	// OSS neither sees nor writes the mark.
	Missed bool `json:"missed,omitempty"`
	// MissedEpoch counts the misses, and the skips that a mark has to
	// outlast (MarkNtfSubscriptionSkipped). A mark is cleared only at the
	// epoch that the notifyAlarmListRebuilt was sent for, so that a miss
	// that another watcher saw in between, or an alarm that changed after
	// the OSS may have read the list, keeps it.
	MissedEpoch uint64 `json:"missed_epoch,omitempty"`
	// RetryAt is when a watcher may try the owed notifyAlarmListRebuilt
	// next. It keeps the watchers of every node from trying it at once.
	RetryAt time.Time `json:"retry_at,omitzero"`
	// Claimed says that the turn of MissedEpoch has been taken
	// (ClaimNtfSubscriptionRetry): its notifyAlarmListRebuilt may be on
	// its way, and the OSS may have read the list already.
	Claimed bool `json:"claimed,omitempty"`
}

// ErrTooManySubscriptions is the answer of an NtfSubscriptionStore to a
// new subscription beyond its bound.
var ErrTooManySubscriptions = errors.New("as many subscriptions as the OAM keeps")

// NtfSubscriptionStore keeps the subscriptions to the alarm
// notifications. Several OAM instances that share one store see the
// same subscriptions, so that the instance that sends a notification
// need not be the one that the OSS created the subscription with.
type NtfSubscriptionStore interface {
	// PutNtfSubscription creates the subscription, or replaces the one
	// of its id, and says whether it created it. A new subscription
	// when there are max already is ErrTooManySubscriptions. Two
	// instances that put at once do not lose a subscription, nor pass
	// max together.
	PutNtfSubscription(ctx context.Context, s NtfSubscriptionControl, max int) (created bool, err error)
	// NtfSubscription reads the subscription of id; ErrNotFound when
	// there is none.
	NtfSubscription(ctx context.Context, id string) (NtfSubscriptionControl, error)
	// DeleteNtfSubscription removes the subscription of id; ErrNotFound
	// when there is none.
	DeleteNtfSubscription(ctx context.Context, id string) error
	// NtfSubscriptions reads every subscription, in the order of their
	// ids.
	NtfSubscriptions(ctx context.Context) ([]NtfSubscriptionControl, error)
	// MarkNtfSubscriptionMissed records a miss of the subscription of
	// id: it sets Missed, raises MissedEpoch, and sets RetryAt to
	// retryAt. ErrNotFound when there is no subscription. A
	// PutNtfSubscription that replaces the subscription keeps the mark.
	MarkNtfSubscriptionMissed(ctx context.Context, id string, retryAt time.Time) error
	// MarkNtfSubscriptionSkipped records that a watcher passed the
	// subscription of id over for a notification, as it was marked, with
	// a copy that said the turn of epoch was taken. The OSS may have
	// read the list for the notifyAlarmListRebuilt of that turn before
	// the alarm changed, so its answer must not clear the mark: if
	// MissedEpoch is still epoch, it raises it, and sets Missed again if
	// that answer has cleared it already. RetryAt stays as it is.
	// ErrNotFound when there is no subscription.
	MarkNtfSubscriptionSkipped(ctx context.Context, id string, epoch uint64) error
	// ClaimNtfSubscriptionRetry takes the turn to send the
	// notifyAlarmListRebuilt that the miss of epoch owes: true when the
	// mark is still at epoch and RetryAt is not after now, and RetryAt
	// is then next, so that no other watcher takes the turn before it,
	// and Claimed is set.
	ClaimNtfSubscriptionRetry(ctx context.Context, id string, epoch uint64, now, next time.Time) (bool, error)
	// ClearNtfSubscriptionMissed clears the mark if it is still at
	// epoch, and says whether it did.
	ClearNtfSubscriptionMissed(ctx context.Context, id string, epoch uint64) (bool, error)
}

// NtfSubscriptionSet is every subscription by its id, as a store holds
// them in one item. The bound on the number keeps the item small, and
// one item lets a store check the bound and change the set in one
// compare and swap.
type NtfSubscriptionSet map[string]NtfSubscriptionControl

// Put is what PutNtfSubscription does to the set.
func (set NtfSubscriptionSet) Put(s NtfSubscriptionControl, max int) (created bool, err error) {
	old, exists := set[s.ID]
	if !exists && len(set) >= max {
		return false, fmt.Errorf("%w: %d", ErrTooManySubscriptions, max)
	}
	s.Missed, s.MissedEpoch, s.RetryAt, s.Claimed = old.Missed, old.MissedEpoch, old.RetryAt, old.Claimed
	set[s.ID] = s
	return !exists, nil
}

// Delete is what DeleteNtfSubscription does to the set.
func (set NtfSubscriptionSet) Delete(id string) error {
	if _, ok := set[id]; !ok {
		return fmt.Errorf("%w: no subscription %q", ErrNotFound, id)
	}
	delete(set, id)
	return nil
}

// MarkMissed is what MarkNtfSubscriptionMissed does to the set.
func (set NtfSubscriptionSet) MarkMissed(id string, retryAt time.Time) error {
	s, err := set.Get(id)
	if err != nil {
		return err
	}
	s.Missed, s.RetryAt, s.Claimed = true, retryAt, false
	s.MissedEpoch++
	set[id] = s
	return nil
}

// MarkSkipped is what MarkNtfSubscriptionSkipped does to the set.
func (set NtfSubscriptionSet) MarkSkipped(id string, epoch uint64) error {
	s, err := set.Get(id)
	if err != nil || s.MissedEpoch != epoch {
		return err
	}
	s.Missed, s.Claimed = true, false
	s.MissedEpoch++
	set[id] = s
	return nil
}

// ClaimRetry is what ClaimNtfSubscriptionRetry does to the set.
func (set NtfSubscriptionSet) ClaimRetry(id string, epoch uint64, now, next time.Time) (bool, error) {
	s, err := set.Get(id)
	if err != nil || !s.Missed || s.MissedEpoch != epoch || s.RetryAt.After(now) {
		return false, err
	}
	s.RetryAt, s.Claimed = next, true
	set[id] = s
	return true, nil
}

// ClearMissed is what ClearNtfSubscriptionMissed does to the set.
func (set NtfSubscriptionSet) ClearMissed(id string, epoch uint64) (bool, error) {
	s, err := set.Get(id)
	if err != nil || !s.Missed || s.MissedEpoch != epoch {
		return false, err
	}
	s.Missed, s.RetryAt, s.Claimed = false, time.Time{}, false
	set[id] = s
	return true, nil
}

// Get is what NtfSubscription answers from the set.
func (set NtfSubscriptionSet) Get(id string) (NtfSubscriptionControl, error) {
	s, ok := set[id]
	if !ok {
		return NtfSubscriptionControl{}, fmt.Errorf("%w: no subscription %q", ErrNotFound, id)
	}
	return s, nil
}

// Sorted is what NtfSubscriptions answers from the set.
func (set NtfSubscriptionSet) Sorted() []NtfSubscriptionControl {
	return slices.SortedFunc(maps.Values(set), func(a, b NtfSubscriptionControl) int { return cmp.Compare(a.ID, b.ID) })
}

// FileNtfSubscriptionStore keeps the subscriptions of one OAM instance
// in one file of its data directory, a JSON object of every
// subscription by its id. Each change writes the whole file anew and
// renames it into place, so a reader never sees half of one, and a
// crash leaves the file as it was before the change or after it. The
// file is 0600, as it holds the addresses of the OSSs.
//
// One OAM instance only: several instances that share the directory
// would each change the file under their own lock. They share a
// kvstore.Store instead (internal/kvstore/oamstore).
type FileNtfSubscriptionStore struct {
	path string
	mu   sync.Mutex
}

// NewFileNtfSubscriptionStore uses the file at path, which it makes,
// empty, if need be.
func NewFileNtfSubscriptionStore(path string) (*FileNtfSubscriptionStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f := &FileNtfSubscriptionStore{path: path}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return f, f.write(NtfSubscriptionSet{})
	} else if err != nil {
		return nil, err
	}
	if _, err := f.read(); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *FileNtfSubscriptionStore) read() (NtfSubscriptionSet, error) {
	b, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}
	set := NtfSubscriptionSet{}
	if err := json.Unmarshal(b, &set); err != nil {
		return nil, fmt.Errorf("oam: %s: %w", f.path, err)
	}
	return set, nil
}

func (f *FileNtfSubscriptionStore) write(set NtfSubscriptionSet) error {
	b, err := json.Marshal(set)
	if err != nil {
		return err
	}
	return replaceFile(f.path, b)
}

// replaceFile writes b to path as a whole: to a new file of the same
// directory, synced, then renamed into place, and then the directory
// synced, so that after a crash the file holds what it held before or
// b, never part of b, and the rename itself is on the disk. A file
// system that cannot sync a directory, as on Windows, is left to keep
// the rename by itself: the file is whole either way.
func replaceFile(path string, b []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.partial")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// change reads the set, lets do change it, and writes it back unless do
// fails.
func (f *FileNtfSubscriptionStore) change(do func(NtfSubscriptionSet) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, err := f.read()
	if err != nil {
		return err
	}
	if err := do(set); err != nil {
		return err
	}
	return f.write(set)
}

func (f *FileNtfSubscriptionStore) PutNtfSubscription(_ context.Context, s NtfSubscriptionControl, max int) (created bool, err error) {
	err = f.change(func(set NtfSubscriptionSet) error {
		created, err = set.Put(s, max)
		return err
	})
	return created, err
}

func (f *FileNtfSubscriptionStore) NtfSubscription(_ context.Context, id string) (NtfSubscriptionControl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, err := f.read()
	if err != nil {
		return NtfSubscriptionControl{}, err
	}
	return set.Get(id)
}

func (f *FileNtfSubscriptionStore) DeleteNtfSubscription(_ context.Context, id string) error {
	return f.change(func(set NtfSubscriptionSet) error { return set.Delete(id) })
}

func (f *FileNtfSubscriptionStore) NtfSubscriptions(context.Context) ([]NtfSubscriptionControl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, err := f.read()
	if err != nil {
		return nil, err
	}
	return set.Sorted(), nil
}

func (f *FileNtfSubscriptionStore) MarkNtfSubscriptionMissed(_ context.Context, id string, retryAt time.Time) error {
	return f.change(func(set NtfSubscriptionSet) error { return set.MarkMissed(id, retryAt) })
}

func (f *FileNtfSubscriptionStore) MarkNtfSubscriptionSkipped(_ context.Context, id string, epoch uint64) error {
	return f.change(func(set NtfSubscriptionSet) error { return set.MarkSkipped(id, epoch) })
}

func (f *FileNtfSubscriptionStore) ClaimNtfSubscriptionRetry(_ context.Context, id string, epoch uint64, now, next time.Time) (claimed bool, err error) {
	err = f.change(func(set NtfSubscriptionSet) error {
		claimed, err = set.ClaimRetry(id, epoch, now, next)
		return err
	})
	return claimed, err
}

func (f *FileNtfSubscriptionStore) ClearNtfSubscriptionMissed(_ context.Context, id string, epoch uint64) (cleared bool, err error) {
	err = f.change(func(set NtfSubscriptionSet) error {
		cleared, err = set.ClearMissed(id, epoch)
		return err
	})
	return cleared, err
}
