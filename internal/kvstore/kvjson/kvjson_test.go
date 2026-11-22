package kvjson_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
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

// TestCompareAndSwapAndRemoveIf: a write or a removal at a version that
// is no longer the item's changes nothing, and says so.
func TestCompareAndSwapAndRemoveIf(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			key := c.Key("counter", "x")
			var got counter
			if _, err := kvjson.Read(ctx, c, key, &got); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("Read of nothing = %v, want ErrNotFound", err)
			}
			first, err := kvjson.CompareAndSwap(ctx, c, key, "", counter{N: 1}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := kvjson.CompareAndSwap(ctx, c, key, "", counter{N: 9}, 0); !errors.Is(err, kvstore.ErrConflict) {
				t.Fatalf("a second creation = %v, want ErrConflict", err)
			}
			if version, err := kvjson.Read(ctx, c, key, &got); err != nil || version != first || got.N != 1 {
				t.Fatalf("Read = %+v at %q, %v; want N 1 at %q", got, version, err, first)
			}
			second, err := kvjson.CompareAndSwap(ctx, c, key, first, counter{N: 2}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := c.RemoveIf(ctx, key, first); !errors.Is(err, kvstore.ErrConflict) {
				t.Fatalf("RemoveIf at the old version = %v, want ErrConflict", err)
			}
			if _, err := kvjson.Read(ctx, c, key, &got); err != nil || got.N != 2 {
				t.Fatalf("Read after a refused removal = %+v, %v; want N 2", got, err)
			}
			if err := c.RemoveIf(ctx, key, second); err != nil {
				t.Fatal(err)
			}
			if _, err := kvjson.Read(ctx, c, key, &got); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("Read after the removal = %v, want ErrNotFound", err)
			}
			if err := c.RemoveIf(ctx, key, second); !errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("RemoveIf of nothing = %v, want ErrConflict", err)
			}
		})
	}
}

// TestAFailedStoreIsNoMissingItem: when the store fails, Read,
// CompareAndSwap and RemoveIf return its failure, which a caller can
// tell from a missing item and from a lost race.
func TestAFailedStoreIsNoMissingItem(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(storetest.StopAfter(backend.New(t), 0), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			key := c.Key("counter", "x")
			if _, err := kvjson.CompareAndSwap(ctx, c, key, "", counter{N: 1}, 0); err == nil || errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("CompareAndSwap on a stopped store = %v, want its failure", err)
			}
			var got counter
			if _, err := kvjson.Read(ctx, c, key, &got); err == nil || errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("Read on a stopped store = %v, want its failure", err)
			}
			if err := c.RemoveIf(ctx, key, "v"); err == nil || errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("RemoveIf on a stopped store = %v, want its failure", err)
			}
		})
	}
}

func TestReadOfAnItemThatIsNotJSON(t *testing.T) {
	kv := kvstore.NewMemoryStore(nil)
	c := kvjson.New(kv, kvjson.Config{}, "test")
	key := c.Key("counter", "x")
	if _, err := kv.Put(context.Background(), key, []byte("{"), 0); err != nil {
		t.Fatal(err)
	}
	var got counter
	if _, err := kvjson.Read(context.Background(), c, key, &got); err == nil || errors.Is(err, kvstore.ErrNotFound) {
		t.Errorf("Read = %v, want a failure to decode", err)
	}
}

// hanging is a store that never answers.
type hanging struct{ kvstore.Store }

func (hanging) Get(ctx context.Context, _ string) (kvstore.Item, error) {
	<-ctx.Done()
	return kvstore.Item{}, ctx.Err()
}

func (hanging) CompareAndSwap(ctx context.Context, _, _ string, _ []byte, _ time.Duration) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (hanging) CompareAndDelete(ctx context.Context, _, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestTheOperationsEnd: an operation on a store that does not answer
// ends when the Timeout of the Config runs out, or when the caller's
// context ends first.
func TestTheOperationsEnd(t *testing.T) {
	operations := map[string]func(context.Context, *kvjson.Client) error{
		"Read": func(ctx context.Context, c *kvjson.Client) error {
			var v counter
			_, err := kvjson.Read(ctx, c, "k", &v)
			return err
		},
		"CompareAndSwap": func(ctx context.Context, c *kvjson.Client) error {
			_, err := kvjson.CompareAndSwap(ctx, c, "k", "", counter{}, 0)
			return err
		},
		"RemoveIf": func(ctx context.Context, c *kvjson.Client) error { return c.RemoveIf(ctx, "k", "v") },
	}
	for name, op := range operations {
		t.Run(name, func(t *testing.T) {
			short := kvjson.New(hanging{}, kvjson.Config{Timeout: 20 * time.Millisecond}, "test")
			if err := op(context.Background(), short); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("with a Timeout of 20 ms: %v, want the deadline", err)
			}
			long := kvjson.New(hanging{}, kvjson.Config{Timeout: time.Hour}, "test")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := op(ctx, long); !errors.Is(err, context.Canceled) {
				t.Errorf("with the caller's context ended: %v, want it to end too", err)
			}
		})
	}
}
