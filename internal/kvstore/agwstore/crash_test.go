package agwstore_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/agwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// The tests in this file stop a relay between two writes, with
// storetest.StopAfter, and look at what a relay created again under the
// same name finds. They depend on the order of the writes, which is the
// adapter's, so they are not in the suite.

const relay = "agw-1"

func call(id string, port uint16) agw.SavedContext {
	return agw.SavedContext{ID: id, Terminations: []agw.SavedTermination{
		{ID: agw.Access, LocalRTP: port, Remote: netip.MustParseAddrPort("192.0.2.10:49170"), Mode: agw.SendReceive},
		{ID: agw.Core, LocalRTP: port + 2, Mode: agw.SendReceive},
	}}
}

// relayOn is the Store of relay on kv.
func relayOn(t *testing.T, kv kvstore.Store, prefix string) *agwstore.Store {
	t.Helper()
	s, err := agwstore.New(kv, agwstore.Config{Config: kvjson.Config{Prefix: prefix}, Relay: relay})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ids(t *testing.T, s *agwstore.Store) []string {
	t.Helper()
	cs, err := s.Contexts(context.Background())
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	var got []string
	for _, c := range cs {
		got = append(got, c.ID)
	}
	return got
}

// indexIDs reads the index as it is in the store. The tests leave far
// fewer entries than the limit of the read.
func indexIDs(t *testing.T, kv kvstore.Store, prefix string) []string {
	t.Helper()
	ids, err := kv.MembersAfter(context.Background(), prefix+"agw:"+relay+":context-ids", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func hasItem(t *testing.T, kv kvstore.Store, prefix, id string) bool {
	t.Helper()
	_, err := kv.Get(context.Background(), prefix+"agw:"+relay+":context:"+id)
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

// TestAPutThatStopsLeavesOnlyASkippedEntry stops a relay after it wrote
// the index entry of a new context, before the context. The relay
// created again restores the other context only, and removes the entry
// at once: while a relay starts, no PutContext of it is under way.
func TestAPutThatStopsLeavesOnlyASkippedEntry(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := context.Background()
			kv, prefix := backend.New(t), storetest.Prefix()
			if err := relayOn(t, kv, prefix).PutContext(ctx, call("c1", 20000)); err != nil {
				t.Fatal(err)
			}
			if err := relayOn(t, storetest.StopAfter(kv, 1), prefix).PutContext(ctx, call("c2", 20004)); err == nil {
				t.Fatal("PutContext of a relay that stopped half way reported no error")
			}
			if got := indexIDs(t, kv, prefix); !slices.Equal(got, []string{"c1", "c2"}) {
				t.Fatalf("index right after the stop = %v, want [c1 c2]", got)
			}

			if got := ids(t, relayOn(t, kv, prefix)); !slices.Equal(got, []string{"c1"}) {
				t.Fatalf("Contexts after the stop = %v, want [c1]", got)
			}
			if got := indexIDs(t, kv, prefix); !slices.Equal(got, []string{"c1"}) {
				t.Fatalf("index after the repair = %v, want [c1]", got)
			}
		})
	}
}

// TestNoContextThatContextsCannotFind stops a relay after each number of
// writes of a PutContext of a new context, a PutContext of one that is
// there, and a DeleteContext. Whatever the store holds then, every
// context item in it is one that Contexts returns: a recreated relay
// opens the ports of every context it had saved.
func TestNoContextThatContextsCannotFind(t *testing.T) {
	ops := []struct {
		name string
		do   func(*agwstore.Store) error
	}{
		{"PutNew", func(s *agwstore.Store) error { return s.PutContext(context.Background(), call("c2", 20004)) }},
		{"PutAgain", func(s *agwstore.Store) error {
			c := call("c1", 20000)
			c.Terminations[1].Remote = netip.MustParseAddrPort("198.51.100.20:30000")
			return s.PutContext(context.Background(), c)
		}},
		{"Delete", func(s *agwstore.Store) error { return s.DeleteContext(context.Background(), "c1") }},
	}
	for _, backend := range storetest.Backends() {
		for _, op := range ops {
			for writes := range 3 {
				t.Run(fmt.Sprintf("%s/%s/%d", backend.Name, op.name, writes), func(t *testing.T) {
					kv, prefix := backend.New(t), storetest.Prefix()
					if err := relayOn(t, kv, prefix).PutContext(context.Background(), call("c1", 20000)); err != nil {
						t.Fatal(err)
					}
					op.do(relayOn(t, storetest.StopAfter(kv, writes), prefix))

					restored := ids(t, relayOn(t, kv, prefix))
					for _, id := range []string{"c1", "c2"} {
						if hasItem(t, kv, prefix, id) && !slices.Contains(restored, id) {
							t.Errorf("context %s is in the store, and Contexts (%v) does not find it", id, restored)
						}
					}
				})
			}
		}
	}
}

// TestContextsReportsAFailingStore: a relay that cannot read its
// contexts must know it, rather than start as one without contexts.
func TestContextsReportsAFailingStore(t *testing.T) {
	ctx := context.Background()
	kv, prefix := kvstore.NewMemoryStore(nil), storetest.Prefix()
	if err := relayOn(t, kv, prefix).PutContext(ctx, call("c1", 20000)); err != nil {
		t.Fatal(err)
	}
	stopped := relayOn(t, storetest.StopAfter(kv, 0), prefix)
	if err := stopped.DeleteContext(ctx, "c1"); err == nil {
		t.Fatal("DeleteContext on a stopped instance reported no error")
	}
	if got, err := stopped.Contexts(ctx); err == nil {
		t.Fatalf("Contexts on a stopped instance = %+v and no error", got)
	}
}
