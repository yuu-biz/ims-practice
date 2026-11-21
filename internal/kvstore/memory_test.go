package kvstore_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// clock is a clock that moves only when told.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestMemoryStore(t *testing.T) {
	testConformance(t, func(t *testing.T) harness {
		c := &clock{now: base}
		s := kvstore.NewMemoryStore(c.Now)
		return harness{a: s, b: s, wait: c.Advance}
	})
}

// TestMemoryStoreForgetsExpiredItems checks that items that are never
// read again still go, so that the memory of a long run does not grow
// with every Challenge ever written.
func TestMemoryStoreForgetsExpiredItems(t *testing.T) {
	ctx := context.Background()
	c := &clock{now: base}
	s := kvstore.NewMemoryStore(c.Now)
	for i := range 1000 {
		if _, err := s.Put(ctx, "challenge:"+strconv.Itoa(i), []byte("x"), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	c.Advance(time.Minute)
	if _, err := s.Get(ctx, "challenge:0"); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatalf("Get after the time to live: %v", err)
	}
	if n := kvstore.MemoryItems(s); n != 0 {
		t.Errorf("%d items left after their time to live, want 0", n)
	}
}
