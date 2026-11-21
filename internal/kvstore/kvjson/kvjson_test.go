package kvjson_test

import (
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

func TestMemberKeepsThePartsApart(t *testing.T) {
	for _, parts := range [][]string{{"a:b", "c"}, {"a", "b:c"}, {"100%3A", ""}, {"x"}} {
		if got := kvjson.SplitMember(kvjson.Member(parts...)); !slices.Equal(got, parts) {
			t.Errorf("SplitMember(Member(%q)) = %q", parts, got)
		}
	}
	if kvjson.Member("a:b", "c") == kvjson.Member("a", "b:c") {
		t.Error("two lists of parts give one member")
	}
}

type counter struct{ N int }

func noTTL(counter) time.Duration { return 0 }

// TestUpdateLosesNoChange has two clients of one store add to one item
// at once.
func TestUpdateLosesNoChange(t *testing.T) {
	kv := kvstore.NewMemoryStore(nil)
	a, b := kvjson.New(kv, kvjson.Config{}, "test"), kvjson.New(kv, kvjson.Config{}, "test")
	key := a.Key("counter", "x")
	kvjson.Put(a, key, counter{}, 0)
	var wg sync.WaitGroup
	for _, c := range []*kvjson.Client{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := kvjson.Update(c, key, noTTL, func(v *counter) error { v.N++; return nil }); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	var got counter
	kvjson.Get(b, key, &got)
	if got.N != 100 {
		t.Errorf("N = %d, want 100", got.N)
	}
}

func TestUpdateWritesNothingWhenTheChangeFails(t *testing.T) {
	c := kvjson.New(kvstore.NewMemoryStore(nil), kvjson.Config{}, "test")
	key := c.Key("counter", "x")
	if _, err := kvjson.Update(c, key, noTTL, func(*counter) error { return nil }); !errors.Is(err, kvstore.ErrNotFound) {
		t.Errorf("Update of nothing = %v, want ErrNotFound", err)
	}
	kvjson.Put(c, key, counter{N: 1}, 0)
	refused := errors.New("refused")
	if _, err := kvjson.Update(c, key, noTTL, func(v *counter) error { v.N = 9; return refused }); err != refused {
		t.Errorf("Update = %v, want the error of the change", err)
	}
	var got counter
	if kvjson.Get(c, key, &got); got.N != 1 {
		t.Errorf("N = %d after a failed change, want 1", got.N)
	}
}

func TestOnlyOneTakes(t *testing.T) {
	kv := kvstore.NewMemoryStore(nil)
	a, b := kvjson.New(kv, kvjson.Config{}, "test"), kvjson.New(kv, kvjson.Config{}, "test")
	key := a.Key("delivery", "x")
	kvjson.Put(a, key, counter{N: 7}, 0)
	results := make(chan bool, 2)
	for _, c := range []*kvjson.Client{a, b} {
		go func() {
			_, ok := kvjson.Take[counter](c, key)
			results <- ok
		}()
	}
	if first, second := <-results, <-results; first == second {
		t.Errorf("Take = %v and %v, want one true", first, second)
	}
}

func TestTTLUntil(t *testing.T) {
	now := time.UnixMilli(1_790_000_000_000)
	c := kvjson.New(kvstore.NewMemoryStore(nil), kvjson.Config{Now: func() time.Time { return now }}, "test")
	for _, tt := range []struct {
		expires time.Time
		want    time.Duration
	}{
		{time.Time{}, 0},
		{now.Add(time.Minute), time.Minute},
		// Zero would keep the item for ever.
		{now.Add(-time.Minute), time.Millisecond},
	} {
		if got := c.TTLUntil(tt.expires); got != tt.want {
			t.Errorf("TTLUntil(%v) = %v, want %v", tt.expires, got, tt.want)
		}
	}
}
