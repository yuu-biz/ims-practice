package mrfcstore_test

import (
	"context"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mrfcstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. Each operation of the Store writes one
// key at most, so nothing is ever half written: the write is in the
// store, or it is not.

var (
	mix      = mrfc.ConferenceMedia{MRFP: "http://mrfp-1:8080", Context: "c1"}
	otherMix = mrfc.ConferenceMedia{MRFP: "http://mrfp-2:8080", Context: "c9"}
)

// TestACreateThatFailsCreatesNothing: an instance that cannot write
// learns that the store failed, so that the MRFC does not mix a party
// in a context that no other instance can find. Nor does it take a
// failed read for a conference that is not there. Another instance
// then creates the conference as the first one.
func TestACreateThatFailsCreatesNothing(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			stopped := mrfcstore.New(storetest.StopAfter(kv, 0), cfg)
			if got, created, err := stopped.CreateConference(ctx, "room1", mix); err == nil || created || got != (mrfc.ConferenceMedia{}) {
				t.Fatalf("CreateConference on a stopped instance = %+v, %v, %v; want the failure of the store", got, created, err)
			}
			if got, ok, err := stopped.Conference(ctx, "room1"); err == nil {
				t.Errorf("Conference on a stopped instance = %+v, %v, no error; want the failure of the store", got, ok)
			}
			other := mrfcstore.New(kv, cfg)
			if got, ok, err := other.Conference(ctx, "room1"); err != nil || ok {
				t.Fatalf("the stopped instance left %+v, %v, %v", got, ok, err)
			}
			if got, created, err := other.CreateConference(ctx, "room1", otherMix); err != nil || !created || got != otherMix {
				t.Errorf("CreateConference on another instance = %+v, %v, %v", got, created, err)
			}
		})
	}
}

// TestARemoveThatStopsLeavesTheConference: an instance that stops before
// it removes an ended conference learns that it did not, and leaves the
// item. The MRFC of another instance that a party reaches then finds
// that the MRFP has no such context any more, and removes the item
// itself, with the same media.
func TestARemoveThatStopsLeavesTheConference(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			if _, _, err := mrfcstore.New(kv, cfg).CreateConference(ctx, "room1", mix); err != nil {
				t.Fatal(err)
			}
			if err := mrfcstore.New(storetest.StopAfter(kv, 0), cfg).RemoveConference(ctx, "room1", mix); err == nil {
				t.Error("RemoveConference on a stopped instance returned no error")
			}

			other := mrfcstore.New(kv, cfg)
			if got, ok, err := other.Conference(ctx, "room1"); err != nil || !ok || got != mix {
				t.Fatalf("Conference after the stopped removal = %+v, %v, %v; want %+v", got, ok, err, mix)
			}
			if err := other.RemoveConference(ctx, "room1", mix); err != nil {
				t.Fatal(err)
			}
			if got, ok, err := other.Conference(ctx, "room1"); err != nil || ok {
				t.Errorf("the other instance did not remove the conference: %+v, %v, %v", got, ok, err)
			}
		})
	}
}
