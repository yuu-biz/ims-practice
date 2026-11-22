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

// TestWriteAndRemove: Write writes over what is there, and Remove of an
// item that is not there is no failure.
func TestWriteAndRemove(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			key := c.Key("counter", "x")
			for _, n := range []int{1, 2} {
				if err := kvjson.Write(ctx, c, key, counter{N: n}, 0); err != nil {
					t.Fatal(err)
				}
			}
			var got counter
			if _, err := kvjson.Read(ctx, c, key, &got); err != nil || got.N != 2 {
				t.Fatalf("Read after two writes = %+v, %v; want N 2", got, err)
			}
			for range 2 {
				if err := c.Remove(ctx, key); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := kvjson.Read(ctx, c, key, &got); !errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("Read after Remove = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestReadModifyWrite: the change starts from the zero value when there
// is no item, and from the item when there is one; two clients that
// change one item at once lose no change.
func TestReadModifyWrite(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			a, b := kvjson.New(kv, cfg, "test"), kvjson.New(kv, cfg, "test")
			key := a.Key("counter", "x")
			var wg sync.WaitGroup
			for _, c := range []*kvjson.Client{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 20 {
						if _, err := kvjson.ReadModifyWrite(ctx, c, key, noTTL, func(v *counter) { v.N++ }); err != nil {
							t.Error(err)
						}
					}
				}()
			}
			wg.Wait()
			var got counter
			if _, err := kvjson.Read(ctx, b, key, &got); err != nil || got.N != 40 {
				t.Errorf("Read = %+v, %v; want N 40", got, err)
			}
		})
	}
}

// TestModify: two clients that change one item at once lose no change;
// an item that is not there is not written, and a change that fails
// writes nothing.
func TestModify(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			a, b := kvjson.New(kv, cfg, "test"), kvjson.New(kv, cfg, "test")
			key := a.Key("counter", "x")
			if _, err := kvjson.Modify(ctx, a, key, noTTL, func(v *counter) error { v.N++; return nil }); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("Modify of nothing = %v, want ErrNotFound", err)
			}
			var got counter
			if _, err := kvjson.Read(ctx, b, key, &got); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("Modify of nothing wrote %+v, %v", got, err)
			}
			if err := kvjson.Write(ctx, a, key, counter{}, 0); err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for _, c := range []*kvjson.Client{a, b} {
				wg.Go(func() {
					for range 20 {
						if _, err := kvjson.Modify(ctx, c, key, noTTL, func(v *counter) error { v.N++; return nil }); err != nil {
							t.Error(err)
						}
					}
				})
			}
			wg.Wait()
			refused := errors.New("refused")
			if _, err := kvjson.Modify(ctx, a, key, noTTL, func(v *counter) error { v.N = 0; return refused }); err != refused {
				t.Errorf("Modify = %v, want the error of the change", err)
			}
			if _, err := kvjson.Read(ctx, b, key, &got); err != nil || got.N != 40 {
				t.Errorf("Read = %+v, %v; want N 40", got, err)
			}
		})
	}
}

// TestCompareAndSwapIndex: of two writers that claim one index item
// from the same version, one wins; the other learns of the conflict.
func TestCompareAndSwapIndex(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			key := c.Key("index", "x")
			version, err := c.CompareAndSwapIndex(ctx, key, "", "a", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.CompareAndSwapIndex(ctx, key, "", "b", 0); !errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("a second claim of a new item = %v, want ErrConflict", err)
			}
			if _, err := c.CompareAndSwapIndex(ctx, key, version, "b", 0); err != nil {
				t.Fatal(err)
			}
			if _, err := c.CompareAndSwapIndex(ctx, key, version, "c", 0); !errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("a claim from an old version = %v, want ErrConflict", err)
			}
			if got, _, err := c.ReadIndex(ctx, key); err != nil || got != "b" {
				t.Errorf("ReadIndex = %q, %v; want b", got, err)
			}
		})
	}
}

// TestIndexItems: an index item names its main item until it is
// removed, and RemoveIndexIf leaves one that names another main item.
func TestIndexItems(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			key := c.Key("index", "x")
			if _, _, err := c.ReadIndex(ctx, key); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("ReadIndex of nothing = %v, want ErrNotFound", err)
			}
			if err := c.RemoveIndexIf(ctx, key, "a"); err != nil {
				t.Fatalf("RemoveIndexIf of nothing = %v", err)
			}
			if err := c.WriteIndex(ctx, key, "b", 0); err != nil {
				t.Fatal(err)
			}
			if err := c.RemoveIndexIf(ctx, key, "a"); err != nil {
				t.Fatal(err)
			}
			if got, _, err := c.ReadIndex(ctx, key); err != nil || got != "b" {
				t.Fatalf("ReadIndex after removing another target = %q, %v; want b", got, err)
			}
			if err := c.RemoveIndexIf(ctx, key, "b"); err != nil {
				t.Fatal(err)
			}
			if got, _, err := c.ReadIndex(ctx, key); !errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("ReadIndex after the removal = %q, %v; want ErrNotFound", got, err)
			}
		})
	}
}

// TestReadAndRemove: of several clients that take one item at once,
// one gets it and the others find nothing; an item that is not there is
// no failure either.
func TestReadAndRemove(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			a, b := kvjson.New(kv, cfg, "test"), kvjson.New(kv, cfg, "test")
			key := a.Key("delivery", "x")
			if _, err := kvjson.ReadAndRemove[counter](ctx, a, key); !errors.Is(err, kvstore.ErrNotFound) {
				t.Fatalf("ReadAndRemove of nothing = %v, want ErrNotFound", err)
			}
			for range 10 {
				if err := kvjson.Write(ctx, a, key, counter{N: 7}, 0); err != nil {
					t.Fatal(err)
				}
				var mu sync.Mutex
				var got []counter
				var wg sync.WaitGroup
				for i := range 4 {
					c := a
					if i%2 == 1 {
						c = b
					}
					wg.Add(1)
					go func() {
						defer wg.Done()
						v, err := kvjson.ReadAndRemove[counter](ctx, c, key)
						if errors.Is(err, kvstore.ErrNotFound) {
							return
						}
						if err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						got = append(got, v)
						mu.Unlock()
					}()
				}
				wg.Wait()
				if len(got) != 1 || got[0].N != 7 {
					t.Fatalf("the takers got %+v, want one N 7", got)
				}
			}
		})
	}
}

// TestTimeIndexMembers: a member is due at the time WriteDue gave it,
// and RemoveDue takes it out, whether it is there or not.
func TestTimeIndexMembers(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := kvjson.New(backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}, "test")
			if err := c.RemoveDue(ctx, "timer", "a"); err != nil {
				t.Fatalf("RemoveDue of nothing = %v", err)
			}
			for member, at := range map[string]time.Time{"a": now, "b": now.Add(time.Minute)} {
				if err := c.WriteDue(ctx, "timer", member, at); err != nil {
					t.Fatal(err)
				}
			}
			claims, err := c.ClaimDue(ctx, "timer", now.Add(time.Second), time.Minute, 10)
			if err != nil || len(claims) != 1 || claims[0].Member != "a" {
				t.Fatalf("ClaimDue a second after a = %v, %v; want a alone", claims, err)
			}
			if err := c.Done(ctx, claims[0]); err != nil {
				t.Fatal(err)
			}
			if err := c.RemoveDue(ctx, "timer", "b"); err != nil {
				t.Fatal(err)
			}
			if claims, err := c.ClaimDue(ctx, "timer", now.Add(time.Hour), time.Minute, 10); err != nil || len(claims) != 0 {
				t.Errorf("ClaimDue after RemoveDue of b = %v, %v; want nothing", claims, err)
			}
		})
	}
}

// TestAFailedStoreIsNoMissingItem: when the store fails, the operations
// that take a context return its failure, which a caller can tell from
// a missing item and from a lost race.
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
			if err := kvjson.Write(ctx, c, key, counter{N: 1}, 0); err == nil {
				t.Error("Write on a stopped store returned no error")
			}
			if _, err := kvjson.ReadModifyWrite(ctx, c, key, noTTL, func(v *counter) { v.N++ }); err == nil || errors.Is(err, kvjson.ErrGaveUp) {
				t.Errorf("ReadModifyWrite on a stopped store = %v, want its failure", err)
			}
			if err := c.Remove(ctx, key); err == nil {
				t.Error("Remove on a stopped store returned no error")
			}
			index := c.Key("index", "x")
			if err := c.WriteIndex(ctx, index, key, 0); err == nil {
				t.Error("WriteIndex on a stopped store returned no error")
			}
			if _, _, err := c.ReadIndex(ctx, index); err == nil || errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("ReadIndex on a stopped store = %v, want its failure", err)
			}
			if err := c.RemoveIndexIf(ctx, index, key); err == nil {
				t.Error("RemoveIndexIf on a stopped store returned no error")
			}
			if _, err := c.CompareAndSwapIndex(ctx, index, "", key, 0); err == nil || errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("CompareAndSwapIndex on a stopped store = %v, want its failure", err)
			}
			if _, err := kvjson.Modify(ctx, c, key, noTTL, func(v *counter) error { v.N++; return nil }); err == nil || errors.Is(err, kvstore.ErrNotFound) || errors.Is(err, kvjson.ErrGaveUp) {
				t.Errorf("Modify on a stopped store = %v, want its failure", err)
			}
			if _, err := kvjson.ReadAndRemove[counter](ctx, c, key); err == nil || errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("ReadAndRemove on a stopped store = %v, want its failure", err)
			}
			if err := c.WriteDue(ctx, "timer", "a", time.Now()); err == nil {
				t.Error("WriteDue on a stopped store returned no error")
			}
			if err := c.RemoveDue(ctx, "timer", "a"); err == nil {
				t.Error("RemoveDue on a stopped store returned no error")
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

func (hanging) Put(ctx context.Context, _ string, _ []byte, _ time.Duration) (string, error) {
	<-ctx.Done()
	return "", ctx.Err()
}

func (hanging) Delete(ctx context.Context, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (hanging) Schedule(ctx context.Context, _, _ string, _ time.Time) error {
	<-ctx.Done()
	return ctx.Err()
}

func (hanging) Unschedule(ctx context.Context, _, _ string) error {
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
		"Write":    func(ctx context.Context, c *kvjson.Client) error { return kvjson.Write(ctx, c, "k", counter{}, 0) },
		"ReadModifyWrite": func(ctx context.Context, c *kvjson.Client) error {
			_, err := kvjson.ReadModifyWrite(ctx, c, "k", noTTL, func(*counter) {})
			return err
		},
		"Remove":     func(ctx context.Context, c *kvjson.Client) error { return c.Remove(ctx, "k") },
		"WriteIndex": func(ctx context.Context, c *kvjson.Client) error { return c.WriteIndex(ctx, "k", "t", 0) },
		"ReadIndex": func(ctx context.Context, c *kvjson.Client) error {
			_, _, err := c.ReadIndex(ctx, "k")
			return err
		},
		"RemoveIndexIf": func(ctx context.Context, c *kvjson.Client) error { return c.RemoveIndexIf(ctx, "k", "t") },
		"CompareAndSwapIndex": func(ctx context.Context, c *kvjson.Client) error {
			_, err := c.CompareAndSwapIndex(ctx, "k", "", "t", 0)
			return err
		},
		"Modify": func(ctx context.Context, c *kvjson.Client) error {
			_, err := kvjson.Modify(ctx, c, "k", noTTL, func(*counter) error { return nil })
			return err
		},
		"ReadAndRemove": func(ctx context.Context, c *kvjson.Client) error {
			_, err := kvjson.ReadAndRemove[counter](ctx, c, "k")
			return err
		},
		"WriteDue":  func(ctx context.Context, c *kvjson.Client) error { return c.WriteDue(ctx, "i", "m", time.Now()) },
		"RemoveDue": func(ctx context.Context, c *kvjson.Client) error { return c.RemoveDue(ctx, "i", "m") },
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
