package mmtelstore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. A call is two items, the record and
// the index item of its Outgoing dialog, so a PutCall or a DeleteCall
// can stop between them. A conference is one item.

// call is a call that the AS relays, as its record has it.
var call = mmtel.CallRecord{
	Incoming:   sip.Dialog{ID: sip.DialogID{CallID: "a1@192.0.2.10", LocalTag: "as-in", RemoteTag: "ue1"}, State: sip.DialogConfirmed},
	Outgoing:   sip.Dialog{ID: sip.DialogID{CallID: "as-1", LocalTag: "as-out", RemoteTag: "ue2"}, State: sip.DialogConfirmed},
	ServedUser: "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org", SessionCase: sip.SessionCaseOrig,
}

// noCall checks that s finds no call under either dialog of call, and
// that it does not fail.
func noCall(t *testing.T, s *mmtelstore.Store, when string) {
	t.Helper()
	for _, id := range []sip.DialogID{call.Incoming.ID, call.Outgoing.ID} {
		if got, ok, err := s.CallOf(context.Background(), id); err != nil || ok {
			t.Errorf("%s, CallOf(%v) = %+v, %v, %v; want no call", when, id, got, ok, err)
		}
	}
}

// TestAPutCallThatStopsLeavesNoCall: an instance that stops before
// either write of a call, or between them, learns that the store
// failed, and does not take a failed read for a call that is not there.
// The other instances find no call: an index item without its record
// is a miss.
func TestAPutCallThatStopsLeavesNoCall(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		for _, writes := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/after %d writes", backend.Name, writes), func(t *testing.T) {
				kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
				stopped := mmtelstore.New(storetest.StopAfter(kv, writes), cfg)
				if err := stopped.PutCall(ctx, call); err == nil {
					t.Fatalf("PutCall that stops after %d writes returned no error", writes)
				}
				for _, id := range []sip.DialogID{call.Incoming.ID, call.Outgoing.ID} {
					if got, ok, err := stopped.CallOf(ctx, id); err == nil {
						t.Errorf("CallOf(%v) on a stopped instance = %+v, %v, no error; want the failure of the store", id, got, ok)
					}
				}
				noCall(t, mmtelstore.New(kv, cfg), "after the stopped PutCall")
			})
		}
	}
}

// TestADeleteCallThatStopsLeavesTheIndex: an instance that stops before
// it removes an ended call learns that it did not, and the call stays,
// as that of a call whose BYE no instance saw. One that stops between
// the two removals leaves only the index item, which is a miss, and
// which another DeleteCall of the same call removes.
func TestADeleteCallThatStopsLeavesTheIndex(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := mmtelstore.New(kv, cfg)
			if err := other.PutCall(ctx, call); err != nil {
				t.Fatal(err)
			}
			if err := mmtelstore.New(storetest.StopAfter(kv, 0), cfg).DeleteCall(ctx, call); err == nil {
				t.Fatal("DeleteCall on a stopped instance returned no error")
			}
			if got, ok, err := other.CallOf(ctx, call.Outgoing.ID); err != nil || !ok || got.Incoming.ID != call.Incoming.ID {
				t.Fatalf("CallOf after the stopped DeleteCall = %+v, %v, %v; want the call", got, ok, err)
			}

			if err := mmtelstore.New(storetest.StopAfter(kv, 1), cfg).DeleteCall(ctx, call); err == nil {
				t.Fatal("DeleteCall that stops after the record returned no error")
			}
			noCall(t, other, "with the index item left")
			if err := other.DeleteCall(ctx, call); err != nil {
				t.Fatal(err)
			}
			noCall(t, other, "after DeleteCall again")
		})
	}
}

// TestAConferenceWriteThatFailsLeavesTheRecordAsItWas: the record is one
// item, so a host that stops before a write, or before it removes the
// record, leaves the last state that it wrote in full. The stopped
// instance learns of each failure, and does not take a failed read for
// a conference that is not there.
func TestAConferenceWriteThatFailsLeavesTheRecordAsItWas(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := mmtelstore.New(kv, cfg)
			if err := other.PutConference(ctx, conference(1, confinfo.Connected)); err != nil {
				t.Fatal(err)
			}
			stopped := mmtelstore.New(storetest.StopAfter(kv, 0), cfg)
			if err := stopped.PutConference(ctx, conference(2, confinfo.Disconnected)); err == nil {
				t.Error("PutConference on a stopped instance returned no error")
			}
			if got, ok, err := stopped.Conference(ctx, "conf-a1"); err == nil {
				t.Errorf("Conference on a stopped instance = %+v, %v, no error; want the failure of the store", got, ok)
			}
			if err := stopped.DeleteConference(ctx, "conf-a1"); err == nil {
				t.Error("DeleteConference on a stopped instance returned no error")
			}

			got, ok, err := other.Conference(ctx, "conf-a1")
			if err != nil || !ok || got.Subscriptions[0].Version != 1 || got.Participants[0].Status != confinfo.Connected {
				t.Errorf("Conference after the failed writes = %+v, %v, %v; want the record of version 1", got, ok, err)
			}
		})
	}
}
