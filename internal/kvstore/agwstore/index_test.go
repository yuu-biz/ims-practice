package agwstore_test

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore/agwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// The tests in this file look at what the contexts of one relay cost
// the store, and what they do to each other, as the number of contexts
// of the relay grows.

// changed is c after a Modify that has learned the remote address of
// the core side.
func changed(c agw.SavedContext) agw.SavedContext {
	c.Terminations = slices.Clone(c.Terminations)
	c.Terminations[1].Remote = netip.MustParseAddrPort("198.51.100.20:30000")
	return c
}

// TestAContextCostsTheSameHoweverManyContextsTheRelayHas: the add of a
// context, its change by a Modify, and its subtract each carry as much
// to and from the store when the relay has 200 other contexts as when
// it has one. None of them reads or writes a list of the contexts of
// the relay, which would grow with them.
func TestAContextCostsTheSameHoweverManyContextsTheRelayHas(t *testing.T) {
	ctx := context.Background()
	steps := []string{"add", "change", "subtract"}
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			costs := func(others int) []storetest.Tally {
				kv := storetest.Meter(backend.New(t))
				s := relayOn(t, kv, storetest.Prefix())
				for i := range others {
					if err := s.PutContext(ctx, call(fmt.Sprintf("c%04d", i), 20000)); err != nil {
						t.Fatal(err)
					}
				}
				kv.Take()
				var tallies []storetest.Tally
				for _, step := range []func() error{
					func() error { return s.PutContext(ctx, call("new", 30000)) },
					func() error { return s.PutContext(ctx, changed(call("new", 30000))) },
					func() error { return s.DeleteContext(ctx, "new") },
				} {
					if err := step(); err != nil {
						t.Fatal(err)
					}
					tallies = append(tallies, kv.Take())
				}
				return tallies
			}
			few, many := costs(1), costs(200)
			for i, step := range steps {
				if few[i] != many[i] {
					t.Errorf("the %s of a context costs %+v beside 1 context, and %+v beside 200", step, few[i], many[i])
				}
			}
		})
	}
}

// TestAPutOvertakenAtEveryWriteGetsThrough: the calls of one relay add
// and change their contexts at once, and another context is added
// before each write of one PutContext, of a new context or of one that
// is there. That PutContext still gets through, and the relay created
// again finds every context: no PutContext loses to another, however
// many there are.
func TestAPutOvertakenAtEveryWriteGetsThrough(t *testing.T) {
	ctx := context.Background()
	puts := []struct {
		name string
		c    agw.SavedContext
	}{
		{"New", call("x-new", 30000)},
		{"Again", changed(call("x-there", 30004))},
	}
	for _, backend := range storetest.Backends() {
		for _, put := range puts {
			t.Run(backend.Name+"/"+put.name, func(t *testing.T) {
				kv, prefix := backend.New(t), storetest.Prefix()
				others := relayOn(t, kv, prefix)
				if err := others.PutContext(ctx, call("x-there", 30004)); err != nil {
					t.Fatal(err)
				}
				var added []string
				overtaken := relayOn(t, storetest.BeforeEachWrite(kv, func() {
					id := fmt.Sprintf("c%04d", len(added))
					if err := others.PutContext(ctx, call(id, 20000)); err != nil {
						t.Errorf("PutContext(%s): %v", id, err)
					}
					added = append(added, id)
				}), prefix)
				if err := overtaken.PutContext(ctx, put.c); err != nil {
					t.Fatalf("PutContext overtaken at each of its writes: %v", err)
				}
				want := append(slices.Clone(added), "x-there")
				if put.c.ID != "x-there" {
					want = append(want, put.c.ID)
				}
				slices.Sort(want)
				if got := ids(t, relayOn(t, kv, prefix)); !slices.Equal(got, want) {
					t.Errorf("contexts of the relay created again = %v, want %v", got, want)
				}
			})
		}
	}
}

// TestARecreatedRelayFindsMoreContextsThanOnePage: a relay that starts
// reads its index one page at a time, and finds every context that its
// earlier process left, in the order of their IDs, however many pages
// they fill. An entry without its context, wherever it falls, is left
// out and taken out of the index.
func TestARecreatedRelayFindsMoreContextsThanOnePage(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, prefix := backend.New(t), storetest.Prefix()
			before := relayOn(t, kv, prefix)
			var want []string
			for i := range 2*agwstore.Page + 1 {
				id := fmt.Sprintf("c%04d", i)
				if err := before.PutContext(ctx, call(id, 20000)); err != nil {
					t.Fatal(err)
				}
				want = append(want, id)
			}
			leftover := want[agwstore.Page+1]
			if err := relayOn(t, storetest.StopAfter(kv, 1), prefix).DeleteContext(ctx, leftover); err == nil {
				t.Fatal("DeleteContext that stops after the context reported no error")
			}
			want = slices.DeleteFunc(want, func(id string) bool { return id == leftover })

			if got := ids(t, relayOn(t, kv, prefix)); !slices.Equal(got, want) {
				t.Errorf("the relay created again finds %d contexts, want %d: %v", len(got), len(want), got)
			}
			if got := indexIDs(t, kv, prefix); !slices.Equal(got, want) {
				t.Errorf("the index after Contexts holds %d IDs, want %d", len(got), len(want))
			}
		})
	}
}
