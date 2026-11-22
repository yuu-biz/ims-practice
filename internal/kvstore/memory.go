package kvstore

import (
	"container/heap"
	"context"
	"crypto/rand"
	"slices"
	"sync"
	"time"
)

// MemoryStore is a Store in the memory of one process: for tests, and
// for a network with one instance of each role. Several instances share
// state only when they share one MemoryStore.
//
// Its clock decides when an item expires; ClaimDue goes by the now that
// its caller gives, as Valkey does.
type MemoryStore struct {
	now func() time.Time

	mu    sync.Mutex
	items map[string]memoryItem
	// expiries orders the items with a time to live by their end, so
	// that the expired ones are found from the front, as a store with
	// active expiry finds them, without a walk over all items.
	expiries expiryHeap
	indexes  map[string]*timeIndex
	// sorted holds each sorted index as a sorted slice of its members,
	// so that a page is a binary search and a slice of it, as a range
	// lookup in a server is.
	sorted map[string][]string
}

type memoryItem struct {
	item Item
	// expires is zero for an item without a time to live.
	expires time.Time
}

// NewMemoryStore returns an empty store whose clock is now; nil means
// time.Now.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = time.Now
	}
	return &MemoryStore{now: now, items: map[string]memoryItem{}, indexes: map[string]*timeIndex{}, sorted: map[string][]string{}}
}

var _ Store = (*MemoryStore)(nil)

func (s *MemoryStore) Get(_ context.Context, key string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	m, ok := s.items[key]
	if !ok {
		return Item{}, ErrNotFound
	}
	return Item{Value: slices.Clone(m.item.Value), Version: m.item.Version}, nil
}

func (s *MemoryStore) Put(_ context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	return s.write(key, value, ttl), nil
}

func (s *MemoryStore) CompareAndSwap(_ context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	if s.versionOf(key) != version {
		return "", ErrConflict
	}
	return s.write(key, value, ttl), nil
}

func (s *MemoryStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, key)
	return nil
}

func (s *MemoryStore) CompareAndDelete(_ context.Context, key, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpired()
	if _, ok := s.items[key]; !ok || s.versionOf(key) != version {
		return ErrConflict
	}
	delete(s.items, key)
	return nil
}

// versionOf is the version of the item under key, or "" when there is
// none. The lock is held.
func (s *MemoryStore) versionOf(key string) string {
	return s.items[key].item.Version
}

// write stores a copy of value under key with a new version. The lock
// is held.
func (s *MemoryStore) write(key string, value []byte, ttl time.Duration) string {
	m := memoryItem{item: Item{Value: slices.Clone(value), Version: newVersion()}}
	if ttl > 0 {
		m.expires = s.now().Add(ttl)
		heap.Push(&s.expiries, expiry{key: key, version: m.item.Version, at: m.expires})
	}
	s.items[key] = m
	return m.item.Version
}

// removeExpired removes the items whose time to live has passed. An
// entry of expiries whose item has been written again since is stale
// and only dropped. The lock is held.
func (s *MemoryStore) removeExpired() {
	now := s.now()
	for len(s.expiries) > 0 && !now.Before(s.expiries[0].at) {
		e := heap.Pop(&s.expiries).(expiry)
		if m, ok := s.items[e.key]; ok && m.item.Version == e.version {
			delete(s.items, e.key)
		}
	}
}

func newVersion() string { return rand.Text() }

func (s *MemoryStore) Schedule(_ context.Context, index, member string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index(index).set(member, indexTime(at))
	return nil
}

func (s *MemoryStore) Unschedule(_ context.Context, index, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.index(index).remove(member)
	return nil
}

func (s *MemoryStore) ClaimDue(_ context.Context, index string, now time.Time, lease time.Duration, limit int) ([]Claim, error) {
	if lease < time.Millisecond {
		return nil, ErrShortLease
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.index(index)
	until := indexTime(now.Add(lease))
	var claims []Claim
	// The claimed member goes to until, after every member that is due,
	// so the front is the next one each time.
	for len(claims) < limit && len(ix.heap) > 0 && ix.heap[0].at <= indexTime(now) {
		e := ix.heap[0]
		claims = append(claims, Claim{Index: index, Member: e.member, Due: fromIndexTime(e.at), Until: fromIndexTime(until)})
		ix.set(e.member, until)
	}
	return claims, nil
}

func (s *MemoryStore) Done(_ context.Context, c Claim) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix := s.index(c.Index)
	e, ok := ix.byMember[c.Member]
	if !ok || e.at != indexTime(c.Until) {
		return ErrClaimLost
	}
	ix.remove(c.Member)
	return nil
}

func (s *MemoryStore) AddToIndex(_ context.Context, index, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members := s.sorted[index]
	if i, found := slices.BinarySearch(members, member); !found {
		s.sorted[index] = slices.Insert(members, i, member)
	}
	return nil
}

func (s *MemoryStore) RemoveFromIndex(_ context.Context, index, member string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	members := s.sorted[index]
	if i, found := slices.BinarySearch(members, member); found {
		s.sorted[index] = slices.Delete(members, i, i+1)
	}
	return nil
}

func (s *MemoryStore) MembersAfter(_ context.Context, index, after string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	members := s.sorted[index]
	start := 0
	if after != "" {
		// after itself is not in the page, whether or not it is still a
		// member: a page goes on from where the one before it ended, and
		// that member may have been removed since.
		i, found := slices.BinarySearch(members, after)
		start = i
		if found {
			start++
		}
	}
	end := min(start+limit, len(members))
	return slices.Clone(members[start:end]), nil
}

// index returns the time index of name, made on first use. The lock is
// held.
func (s *MemoryStore) index(name string) *timeIndex {
	ix, ok := s.indexes[name]
	if !ok {
		ix = &timeIndex{byMember: map[string]*indexEntry{}}
		s.indexes[name] = ix
	}
	return ix
}

// timeIndex is one time index: a heap ordered by due time, earliest
// first, and the entries by member.
type timeIndex struct {
	heap     indexHeap
	byMember map[string]*indexEntry
}

type indexEntry struct {
	member string
	at     int64
	// position is the place in the heap (container/heap).
	position int
}

func (ix *timeIndex) set(member string, at int64) {
	if e, ok := ix.byMember[member]; ok {
		e.at = at
		heap.Fix(&ix.heap, e.position)
		return
	}
	e := &indexEntry{member: member, at: at}
	ix.byMember[member] = e
	heap.Push(&ix.heap, e)
}

func (ix *timeIndex) remove(member string) {
	if e, ok := ix.byMember[member]; ok {
		heap.Remove(&ix.heap, e.position)
		delete(ix.byMember, member)
	}
}

type indexHeap []*indexEntry

func (h indexHeap) Len() int           { return len(h) }
func (h indexHeap) Less(i, j int) bool { return h[i].at < h[j].at }
func (h indexHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].position, h[j].position = i, j
}
func (h *indexHeap) Push(x any) {
	e := x.(*indexEntry)
	e.position = len(*h)
	*h = append(*h, e)
}
func (h *indexHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	return e
}

type expiry struct {
	key     string
	version string
	at      time.Time
}

type expiryHeap []expiry

func (h expiryHeap) Len() int           { return len(h) }
func (h expiryHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h expiryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expiryHeap) Push(x any)        { *h = append(*h, x.(expiry)) }
func (h *expiryHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}
