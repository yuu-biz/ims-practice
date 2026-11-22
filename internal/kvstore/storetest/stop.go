package storetest

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// StopAfter is kv as an instance sees it that stops after writes
// writes: every operation after those fails, as if the process had
// died there. The tests use it to leave the store as a crash between
// two writes leaves it.
func StopAfter(kv kvstore.Store, writes int) kvstore.Store {
	return &stopping{Store: kv, left: writes}
}

type stopping struct {
	kvstore.Store
	mu   sync.Mutex
	left int
}

var errStopped = errors.New("storetest: the instance has stopped")

// write counts one write, and reports whether it may go ahead.
func (s *stopping) write() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.left--
	return s.left >= 0
}

func (s *stopping) stopped() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.left < 0
}

func (s *stopping) Get(ctx context.Context, key string) (kvstore.Item, error) {
	if s.stopped() {
		return kvstore.Item{}, errStopped
	}
	return s.Store.Get(ctx, key)
}

func (s *stopping) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	if !s.write() {
		return "", errStopped
	}
	return s.Store.Put(ctx, key, value, ttl)
}

func (s *stopping) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	if !s.write() {
		return "", errStopped
	}
	return s.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (s *stopping) Delete(ctx context.Context, key string) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.Delete(ctx, key)
}

func (s *stopping) CompareAndDelete(ctx context.Context, key, version string) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.CompareAndDelete(ctx, key, version)
}

func (s *stopping) Schedule(ctx context.Context, index, member string, at time.Time) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.Schedule(ctx, index, member, at)
}

func (s *stopping) Unschedule(ctx context.Context, index, member string) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.Unschedule(ctx, index, member)
}

func (s *stopping) ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	if s.stopped() {
		return nil, errStopped
	}
	return s.Store.ClaimDue(ctx, index, now, lease, limit)
}

func (s *stopping) Done(ctx context.Context, c kvstore.Claim) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.Done(ctx, c)
}

func (s *stopping) AddToIndex(ctx context.Context, index, member string) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.AddToIndex(ctx, index, member)
}

func (s *stopping) RemoveFromIndex(ctx context.Context, index, member string) error {
	if !s.write() {
		return errStopped
	}
	return s.Store.RemoveFromIndex(ctx, index, member)
}

func (s *stopping) MembersAfter(ctx context.Context, index, after string, limit int) ([]string, error) {
	if s.stopped() {
		return nil, errStopped
	}
	return s.Store.MembersAfter(ctx, index, after, limit)
}
