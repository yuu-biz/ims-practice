package ibcfstore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

// The tests in this file stop a process of an IBCF instance before one
// of its writes, with storetest.StopAfter, and look at what that
// process is told and what the next process of the instance finds. A
// call is two items, the entry in the index of the instance and the
// record, so a PutCall or a DeleteCall can stop between them. They
// depend on the order of the writes, which is the adapter's, so they
// are not in the suite.

const instance = "ibcf-1.ims.mnc001.mcc001.3gppnetwork.org"

func record(id string) ibcf.CallRecord {
	return ibcf.CallRecord{Instance: instance, ID: id, Direction: interconnect.Entry, Peer: "peer-b",
		Media: &ibcf.MediaContext{ID: "c" + id}}
}

// ids are the IDs of the calls that s finds for the instance.
func ids(t *testing.T, s *ibcfstore.Store) []string {
	t.Helper()
	records, err := s.CallsOf(context.Background(), instance)
	if err != nil {
		t.Fatalf("CallsOf: %v", err)
	}
	var got []string
	for _, r := range records {
		got = append(got, r.ID)
	}
	return got
}

// indexIDs reads the index of the instance as it is in the store.
func indexIDs(t *testing.T, kv kvstore.Store, prefix string) []string {
	t.Helper()
	item, err := kv.Get(context.Background(), prefix+"ibcf:calls-of:"+instance)
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var ix struct{ IDs []string }
	if err := json.Unmarshal(item.Value, &ix); err != nil {
		t.Fatal(err)
	}
	return ix.IDs
}

// TestAPutCallThatStopsLeavesNoCall: a process that stops before either
// write of a new call, or between them, learns that the store failed,
// and does not take a failed read for an instance without calls. The
// next process finds no call: an entry without its record is skipped,
// and taken out of the index.
func TestAPutCallThatStopsLeavesNoCall(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		for _, writes := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/after %d writes", backend.Name, writes), func(t *testing.T) {
				kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
				stopped := ibcfstore.New(storetest.StopAfter(kv, writes), cfg)
				if err := stopped.PutCall(ctx, record("a")); err == nil {
					t.Fatalf("PutCall that stops after %d writes returned no error", writes)
				}
				if got, err := stopped.CallsOf(ctx, instance); err == nil {
					t.Errorf("CallsOf on a stopped process = %+v, no error; want the failure of the store", got)
				}

				next := ibcfstore.New(kv, cfg)
				if got := ids(t, next); len(got) != 0 {
					t.Errorf("the next process finds calls %v, want none", got)
				}
				if got := indexIDs(t, kv, cfg.Prefix); len(got) != 0 {
					t.Errorf("the index after CallsOf = %v, want none", got)
				}
			})
		}
	}
}

// TestARewriteThatStopsLeavesTheRecordAsItWas: the entry of a call that
// is written again is in the index already, so its one write is the
// record, which is one item: a process that stops before it leaves the
// record that it wrote last, in full.
func TestARewriteThatStopsLeavesTheRecordAsItWas(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			if err := ibcfstore.New(kv, cfg).PutCall(ctx, record("a")); err != nil {
				t.Fatal(err)
			}
			changed := record("a")
			changed.Media = &ibcf.MediaContext{ID: "c-other"}
			if err := ibcfstore.New(storetest.StopAfter(kv, 0), cfg).PutCall(ctx, changed); err == nil {
				t.Fatal("PutCall on a stopped process returned no error")
			}
			got, err := ibcfstore.New(kv, cfg).CallsOf(ctx, instance)
			if err != nil || len(got) != 1 || got[0].Media.ID != "ca" {
				t.Errorf("CallsOf after the stopped rewrite = %+v, %v; want the record as it was", got, err)
			}
		})
	}
}

// TestADeleteCallThatStopsLeavesTheIndex: a process that stops before it
// removes an ended call learns that it did not, and the next process
// finds the call, and ends it again. One that stops between the two
// removals leaves only the entry, which the next process skips, and
// takes out of the index.
func TestADeleteCallThatStopsLeavesTheIndex(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			if err := ibcfstore.New(kv, cfg).PutCall(ctx, record("a")); err != nil {
				t.Fatal(err)
			}
			if err := ibcfstore.New(storetest.StopAfter(kv, 0), cfg).DeleteCall(ctx, instance, "a"); err == nil {
				t.Fatal("DeleteCall on a stopped process returned no error")
			}
			if got := ids(t, ibcfstore.New(kv, cfg)); len(got) != 1 || got[0] != "a" {
				t.Fatalf("calls after the stopped DeleteCall = %v, want a", got)
			}

			if err := ibcfstore.New(storetest.StopAfter(kv, 1), cfg).DeleteCall(ctx, instance, "a"); err == nil {
				t.Fatal("DeleteCall that stops after the record returned no error")
			}
			if got := indexIDs(t, kv, cfg.Prefix); len(got) != 1 {
				t.Fatalf("the index after the stopped DeleteCall = %v, want the entry of a", got)
			}
			if got := ids(t, ibcfstore.New(kv, cfg)); len(got) != 0 {
				t.Errorf("calls with the entry left = %v, want none", got)
			}
			if got := indexIDs(t, kv, cfg.Prefix); len(got) != 0 {
				t.Errorf("the index after CallsOf = %v, want none", got)
			}
		})
	}
}

// TestCallsOfReturnsTheRecordsWhenTheRepairFails: CallsOf reads all it
// needs before it removes the entries without a record, so a store that
// fails then does not keep the instance from its calls.
func TestCallsOfReturnsTheRecordsWhenTheRepairFails(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			s := ibcfstore.New(kv, cfg)
			for _, id := range []string{"a", "b"} {
				if err := s.PutCall(ctx, record(id)); err != nil {
					t.Fatal(err)
				}
			}
			if err := ibcfstore.New(storetest.StopAfter(kv, 1), cfg).DeleteCall(ctx, instance, "a"); err == nil {
				t.Fatal("DeleteCall that stops after the record returned no error")
			}
			if got := ids(t, ibcfstore.New(storetest.StopAfter(kv, 0), cfg)); len(got) != 1 || got[0] != "b" {
				t.Errorf("calls on a store that fails the repair = %v, want b", got)
			}
			if got := indexIDs(t, kv, cfg.Prefix); len(got) != 2 {
				t.Errorf("the index after the failed repair = %v, want both entries", got)
			}
		})
	}
}
