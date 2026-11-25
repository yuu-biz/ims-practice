package ibcfstore_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// The tests in this file look at what the calls of one instance cost
// the store, and what they do to each other, as the number of calls of
// the instance grows.

// TestACallCostsTheSameHoweverManyCallsTheInstanceHas: the start of a
// call, its rewrite after the answer, and its end each carry as much to
// and from the store when the instance has 200 other calls as when it
// has one. None of them reads or writes a list of the calls of the
// instance, which would grow with them.
func TestACallCostsTheSameHoweverManyCallsTheInstanceHas(t *testing.T) {
	ctx := context.Background()
	steps := []string{"start", "rewrite", "end"}
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			costs := func(others int) []storetest.Tally {
				kv := storetest.Meter(backend.New(t))
				s := ibcfstore.New(kv, kvjson.Config{Prefix: storetest.Prefix()})
				for i := range others {
					if err := s.PutCall(ctx, record(fmt.Sprintf("%04d", i))); err != nil {
						t.Fatal(err)
					}
				}
				kv.Take()
				var tallies []storetest.Tally
				for _, step := range []func() error{
					func() error { return s.PutCall(ctx, record("new")) },
					func() error { return s.PutCall(ctx, answeredRecord("new")) },
					func() error { return s.DeleteCall(ctx, instance, "new") },
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
					t.Errorf("the %s of a call costs %+v beside 1 call, and %+v beside 200", step, few[i], many[i])
				}
			}
		})
	}
}

// TestACallStartOvertakenAtEveryWriteGetsThrough: the calls of one
// instance start at once, and another call starts before each write of
// one of them. That start still gets through, and the next process of
// the instance finds every call that started: no start loses to
// another, however many there are.
func TestACallStartOvertakenAtEveryWriteGetsThrough(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			others := ibcfstore.New(kv, cfg)
			var started []string
			overtaken := ibcfstore.New(storetest.BeforeEachWrite(kv, func() {
				id := fmt.Sprintf("%04d", len(started))
				if err := others.PutCall(ctx, record(id)); err != nil {
					t.Errorf("PutCall(%s): %v", id, err)
				}
				started = append(started, id)
			}), cfg)
			if err := overtaken.PutCall(ctx, record("a")); err != nil {
				t.Fatalf("PutCall overtaken at each of its writes: %v", err)
			}
			if got, want := ids(t, ibcfstore.New(kv, cfg)), append(started, "a"); !slices.Equal(got, want) {
				t.Errorf("calls of the instance = %v, want %v", got, want)
			}
		})
	}
}

// TestARecreatedInstanceFindsMoreCallsThanOnePage: an instance that
// starts reads its index one page at a time, and finds every call that
// its earlier process left, in the order of their IDs, however many
// pages they fill. An entry without its record, wherever it falls, is
// left out and taken out of the index.
func TestARecreatedInstanceFindsMoreCallsThanOnePage(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			before := ibcfstore.New(kv, cfg)
			var want []string
			for i := range 2*ibcfstore.Page + 1 {
				id := fmt.Sprintf("%04d", i)
				if err := before.PutCall(ctx, record(id)); err != nil {
					t.Fatal(err)
				}
				want = append(want, id)
			}
			leftover := want[ibcfstore.Page+1]
			if err := ibcfstore.New(storetest.StopAfter(kv, 1), cfg).DeleteCall(ctx, instance, leftover); err == nil {
				t.Fatal("DeleteCall that stops after the record returned no error")
			}
			want = slices.DeleteFunc(want, func(id string) bool { return id == leftover })

			if got := ids(t, ibcfstore.New(kv, cfg)); !slices.Equal(got, want) {
				t.Errorf("the next process finds %d calls, want %d: %v", len(got), len(want), got)
			}
			if got := indexIDs(t, kv, cfg.Prefix); !slices.Equal(got, want) {
				t.Errorf("the index after CallsOf holds %d IDs, want %d", len(got), len(want))
			}
		})
	}
}
