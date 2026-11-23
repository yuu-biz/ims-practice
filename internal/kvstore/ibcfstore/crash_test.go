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
	"github.com/yuu-biz/ims-practice/internal/sip"
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

// answeredRecord is record(id) once both dialogs of the call are
// confirmed.
func answeredRecord(id string) ibcf.CallRecord {
	r := record(id)
	r.Home = &sip.Dialog{ID: sip.DialogID{CallID: "home-" + id + "@192.0.2.20", LocalTag: "ibcf-in", RemoteTag: "scscf"},
		State: sip.DialogConfirmed}
	r.PeerSide = &sip.Dialog{ID: sip.DialogID{CallID: "peer-" + id + "@198.51.100.1", LocalTag: "ibcf-out", RemoteTag: "ibcf-b"},
		State: sip.DialogConfirmed}
	return r
}

// dialogEntries are the entries of the dialogs of r that are in the
// store.
func dialogEntries(t *testing.T, kv kvstore.Store, prefix string, r ibcf.CallRecord) []string {
	t.Helper()
	var keys []string
	for _, id := range r.DialogIDs() {
		key := prefix + "ibcf:dialog:" + kvjson.Member(id.CallID, id.LocalTag, id.RemoteTag)
		_, err := kv.Get(context.Background(), key)
		switch {
		case err == nil:
			keys = append(keys, key)
		case !errors.Is(err, kvstore.ErrNotFound):
			t.Fatal(err)
		}
	}
	return keys
}

// findsNone checks that s finds the call by neither of the dialogs of
// r.
func findsNone(t *testing.T, s *ibcfstore.Store, r ibcf.CallRecord) {
	t.Helper()
	for _, id := range r.DialogIDs() {
		if got, ok, err := s.CallOf(context.Background(), id); ok || err != nil {
			t.Errorf("CallOf(%+v) = %+v, %v, %v; want a miss", id, got, ok, err)
		}
	}
}

// TestAPutCallThatStopsBeforeItsDialogsLeavesNoEntry: the record of an
// answered call goes before the entries of its dialogs. A process that
// stops between the two learns that the store failed, and no instance
// finds the call by its dialogs yet. The next process finds the record,
// with its dialogs, and once it has ended the call nothing is left.
func TestAPutCallThatStopsBeforeItsDialogsLeavesNoEntry(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			// The call has its entry in the index since its TrGW context,
			// so the record of the answer is the first write.
			if err := ibcfstore.New(kv, cfg).PutCall(ctx, record("a")); err != nil {
				t.Fatal(err)
			}
			r := answeredRecord("a")
			if err := ibcfstore.New(storetest.StopAfter(kv, 1), cfg).PutCall(ctx, r); err == nil {
				t.Fatal("PutCall that stops after the record returned no error")
			}
			findsNone(t, ibcfstore.New(kv, cfg), r)

			next := ibcfstore.New(kv, cfg)
			records, err := next.CallsOf(ctx, instance)
			if err != nil || len(records) != 1 || len(records[0].DialogIDs()) != 2 {
				t.Fatalf("CallsOf = %+v, %v; want the call with its dialogs", records, err)
			}
			if err := next.DeleteCall(ctx, instance, "a"); err != nil {
				t.Fatal(err)
			}
			if got := dialogEntries(t, kv, cfg.Prefix, r); len(got) != 0 {
				t.Errorf("entries left after the call ended: %v", got)
			}
		})
	}
}

// TestADeleteCallThatStopsAfterItsDialogsLeavesTheCall: the entries of
// the dialogs go before the record. A process that stops between the
// two leaves the record in the index of the instance, and the next
// process ends the call again and removes it.
func TestADeleteCallThatStopsAfterItsDialogsLeavesTheCall(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			r := answeredRecord("a")
			if err := ibcfstore.New(kv, cfg).PutCall(ctx, r); err != nil {
				t.Fatal(err)
			}
			if got := dialogEntries(t, kv, cfg.Prefix, r); len(got) != 2 {
				t.Fatalf("entries of the dialogs = %v, want both", got)
			}
			if err := ibcfstore.New(storetest.StopAfter(kv, 2), cfg).DeleteCall(ctx, instance, "a"); err == nil {
				t.Fatal("DeleteCall that stops after the dialogs returned no error")
			}
			findsNone(t, ibcfstore.New(kv, cfg), r)
			if got := ids(t, ibcfstore.New(kv, cfg)); len(got) != 1 || got[0] != "a" {
				t.Fatalf("calls after the stopped DeleteCall = %v, want a", got)
			}

			if err := ibcfstore.New(kv, cfg).DeleteCall(ctx, instance, "a"); err != nil {
				t.Fatal(err)
			}
			if got := ids(t, ibcfstore.New(kv, cfg)); len(got) != 0 {
				t.Errorf("calls after the next DeleteCall = %v, want none", got)
			}
		})
	}
}
