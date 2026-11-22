package storetest

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// IBCF runs the suite of ibcf.Store. The two Stores of a pair stand for
// two processes of one instance, one after the other, or for two
// instances that share the store: each case names the instances whose
// records it writes.
func IBCF(t *testing.T, newPair func(t *testing.T) (ibcf.Store, ibcf.Store)) {
	run(t, newPair, []testCase[ibcf.Store]{
		{"NoCalls", ibcfNoCalls},
		{"PutReplaceDelete", ibcfPutReplaceDelete},
		{"InstancesApart", ibcfInstancesApart},
		{"ConcurrentPuts", ibcfConcurrentPuts},
		{"ConcurrentPutsAndDeletes", ibcfConcurrentPutsAndDeletes},
	})
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func putCallRecord(t *testing.T, s ibcf.Store, r ibcf.CallRecord) {
	t.Helper()
	if err := s.PutCall(context.Background(), r); err != nil {
		t.Errorf("PutCall(%s of %s): %v", r.ID, r.Instance, err)
	}
}

func deleteCallRecord(t *testing.T, s ibcf.Store, instance, id string) {
	t.Helper()
	if err := s.DeleteCall(context.Background(), instance, id); err != nil {
		t.Errorf("DeleteCall(%s of %s): %v", id, instance, err)
	}
}

func callsOf(t *testing.T, s ibcf.Store, instance string) []ibcf.CallRecord {
	t.Helper()
	records, err := s.CallsOf(context.Background(), instance)
	if err != nil {
		t.Errorf("CallsOf(%s): %v", instance, err)
	}
	return records
}

func recordIDs(records []ibcf.CallRecord) []string {
	ids := []string{}
	for _, r := range records {
		ids = append(ids, r.ID)
	}
	return ids
}

// borderCall is the record of a call that IBCF instance has relayed
// across the border, before the answer: its TrGW context, and no
// dialogs yet.
func borderCall(instance, id string) ibcf.CallRecord {
	return ibcf.CallRecord{
		Instance: instance, ID: id, Direction: interconnect.Exit, Peer: "peer-b",
		Media: &ibcf.MediaContext{
			ID:   "c" + id,
			Core: netip.MustParseAddrPort("192.0.2.50:20000"),
			Peer: netip.MustParseAddrPort("198.51.100.50:20002"),
		},
	}
}

// answered is r once both dialogs are confirmed, as the IBCF writes it
// after the 2xx and after each request within the call, with the CSeq
// numbers of the dialogs at seq.
func answered(r ibcf.CallRecord, seq uint32) ibcf.CallRecord {
	home := sip.Dialog{
		ID:    sip.DialogID{CallID: "home-" + r.ID + "@192.0.2.20", LocalTag: "ibcf-in", RemoteTag: "ue-a"},
		State: sip.DialogConfirmed, LocalSeq: seq, RemoteSeq: seq + 1,
		LocalURI: "sip:+15550100@ims.example.net", RemoteURI: "sip:+15550199@ims.example.org",
		RemoteTarget: "sip:001010000000001@192.0.2.10:5060", RouteSet: []string{"<sip:scscf.ims.example.net;lr>"},
	}
	peer := sip.Dialog{
		ID:    sip.DialogID{CallID: "peer-" + r.ID + "@198.51.100.1", LocalTag: "ibcf-out", RemoteTag: "ibcf-b"},
		State: sip.DialogConfirmed, LocalSeq: seq + 2, RemoteSeq: seq,
		LocalURI: "sip:+15550100@ims.example.net", RemoteURI: "sip:+15550199@ims.example.org",
		RemoteTarget: "sip:ibcf-1.nni.example.org:5060",
	}
	r.Home, r.PeerSide = &home, &peer
	return r
}

func ibcfNoCalls(t *testing.T, a, b ibcf.Store) {
	if got := callsOf(t, a, "ibcf-1"); len(got) != 0 {
		t.Errorf("CallsOf of an empty store = %+v", got)
	}
	deleteCallRecord(t, b, "ibcf-1", "gone")
	if got := callsOf(t, a, "ibcf-1"); len(got) != 0 {
		t.Errorf("CallsOf after deleting a missing call = %+v", got)
	}
}

// ibcfPutReplaceDelete has a process of ibcf-1 keep two calls, write one
// again with its dialogs, and end the other; the next process of the
// instance finds what is left, in the order of the IDs.
func ibcfPutReplaceDelete(t *testing.T, before, after ibcf.Store) {
	putCallRecord(t, before, borderCall("ibcf-1", "b"))
	putCallRecord(t, before, borderCall("ibcf-1", "a"))
	if got := callsOf(t, after, "ibcf-1"); !sameJSON(t, got, []ibcf.CallRecord{borderCall("ibcf-1", "a"), borderCall("ibcf-1", "b")}) {
		t.Fatalf("CallsOf = %+v, want calls a and b", got)
	}

	putCallRecord(t, before, answered(borderCall("ibcf-1", "b"), 1))
	putCallRecord(t, before, answered(borderCall("ibcf-1", "b"), 7))
	deleteCallRecord(t, before, "ibcf-1", "a")
	if got, want := callsOf(t, after, "ibcf-1"), []ibcf.CallRecord{answered(borderCall("ibcf-1", "b"), 7)}; !sameJSON(t, got, want) {
		t.Fatalf("CallsOf after a rewrite and a delete = %+v, want %+v", got, want)
	}

	deleteCallRecord(t, after, "ibcf-1", "b")
	if got := callsOf(t, before, "ibcf-1"); len(got) != 0 {
		t.Errorf("CallsOf after the last delete = %+v", got)
	}
}

// ibcfInstancesApart has three instances with a call ID in common, one
// of them with the separator of the keys in its name.
func ibcfInstancesApart(t *testing.T, a, b ibcf.Store) {
	putCallRecord(t, a, borderCall("ibcf-1", "x"))
	putCallRecord(t, a, borderCall("ibcf-1", "y"))
	putCallRecord(t, b, borderCall("ibcf-2", "x"))
	putCallRecord(t, b, borderCall("ibcf-1:call", "x"))

	deleteCallRecord(t, b, "ibcf-2", "y")
	if got := recordIDs(callsOf(t, b, "ibcf-1")); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("calls of ibcf-1 = %v, want x and y", got)
	}
	deleteCallRecord(t, a, "ibcf-1", "x")
	for _, instance := range []string{"ibcf-2", "ibcf-1:call"} {
		got := callsOf(t, a, instance)
		if len(got) != 1 || got[0].ID != "x" || got[0].Instance != instance {
			t.Errorf("calls of %s = %+v, want its call x", instance, got)
		}
	}
}

// ibcfConcurrentPuts has the calls of one instance write their records
// at once, through two handles of the store, each twice as an answered
// call does: none of them may lose its entry in the index.
func ibcfConcurrentPuts(t *testing.T, a, b ibcf.Store) {
	const n = 20
	parallel(n, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		r := borderCall("ibcf-1", fmt.Sprintf("%02d", i))
		putCallRecord(t, s, r)
		putCallRecord(t, s, answered(r, 1))
	})
	var want []string
	for i := range n {
		want = append(want, fmt.Sprintf("%02d", i))
	}
	if got := recordIDs(callsOf(t, b, "ibcf-1")); !slices.Equal(got, want) {
		t.Fatalf("calls after %d concurrent puts = %v, want %v", n, got, want)
	}
}

// ibcfConcurrentPutsAndDeletes ends half of the calls of an instance
// while new ones start.
func ibcfConcurrentPutsAndDeletes(t *testing.T, a, b ibcf.Store) {
	const n = 10
	for i := range n {
		putCallRecord(t, a, borderCall("ibcf-1", fmt.Sprintf("old%02d", i)))
	}
	parallel(2*n, func(i int) {
		if i < n {
			deleteCallRecord(t, b, "ibcf-1", fmt.Sprintf("old%02d", i))
		} else {
			putCallRecord(t, a, borderCall("ibcf-1", fmt.Sprintf("new%02d", i-n)))
		}
	})
	var want []string
	for i := range n {
		want = append(want, fmt.Sprintf("new%02d", i))
	}
	if got := recordIDs(callsOf(t, b, "ibcf-1")); !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}
