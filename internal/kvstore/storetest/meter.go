package storetest

import (
	"context"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// Tally is what went through a Metered store: the operations, and the
// bytes of the values and members that they carried either way. Keys
// are not counted; an operation names a few of them, whatever the store
// holds.
type Tally struct {
	Operations int
	Bytes      int
}

// Metered is a kvstore.Store that keeps the Tally of what goes through
// it. The tests use it to show that an operation costs the same however
// many items the store holds: one that reads or writes a list of them
// carries more bytes as the list grows.
type Metered struct {
	kvstore.Store
	mu    sync.Mutex
	tally Tally
}

// Meter returns kv, metered.
func Meter(kv kvstore.Store) *Metered { return &Metered{Store: kv} }

// Take returns the Tally since the last Take, and starts a new one.
func (m *Metered) Take() Tally {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tally
	m.tally = Tally{}
	return t
}

func (m *Metered) add(bytes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tally.Operations++
	m.tally.Bytes += bytes
}

func (m *Metered) Get(ctx context.Context, key string) (kvstore.Item, error) {
	item, err := m.Store.Get(ctx, key)
	m.add(len(item.Value))
	return item, err
}

func (m *Metered) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	m.add(len(value))
	return m.Store.Put(ctx, key, value, ttl)
}

func (m *Metered) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	m.add(len(value))
	return m.Store.CompareAndSwap(ctx, key, version, value, ttl)
}

func (m *Metered) Delete(ctx context.Context, key string) error {
	m.add(0)
	return m.Store.Delete(ctx, key)
}

func (m *Metered) CompareAndDelete(ctx context.Context, key, version string) error {
	m.add(0)
	return m.Store.CompareAndDelete(ctx, key, version)
}

func (m *Metered) Schedule(ctx context.Context, index, member string, at time.Time) error {
	m.add(len(member))
	return m.Store.Schedule(ctx, index, member, at)
}

func (m *Metered) Unschedule(ctx context.Context, index, member string) error {
	m.add(len(member))
	return m.Store.Unschedule(ctx, index, member)
}

func (m *Metered) ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	claims, err := m.Store.ClaimDue(ctx, index, now, lease, limit)
	bytes := 0
	for _, c := range claims {
		bytes += len(c.Member)
	}
	m.add(bytes)
	return claims, err
}

func (m *Metered) Done(ctx context.Context, c kvstore.Claim) error {
	m.add(len(c.Member))
	return m.Store.Done(ctx, c)
}

func (m *Metered) AddToIndex(ctx context.Context, index, member string) error {
	m.add(len(member))
	return m.Store.AddToIndex(ctx, index, member)
}

func (m *Metered) RemoveFromIndex(ctx context.Context, index, member string) error {
	m.add(len(member))
	return m.Store.RemoveFromIndex(ctx, index, member)
}

func (m *Metered) MembersAfter(ctx context.Context, index, after string, limit int) ([]string, error) {
	members, err := m.Store.MembersAfter(ctx, index, after, limit)
	bytes := 0
	for _, member := range members {
		bytes += len(member)
	}
	m.add(bytes)
	return members, err
}
