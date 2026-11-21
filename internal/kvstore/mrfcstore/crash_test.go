package mrfcstore_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mrfcstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what the other instances find.
// Each operation of the Store writes one key at most, so nothing is ever
// half written: the write is in the store, or it is not.

var (
	mix      = mrfc.ConferenceMedia{MRFP: "http://mrfp-1:8080", Context: "c1"}
	otherMix = mrfc.ConferenceMedia{MRFP: "http://mrfp-2:8080", Context: "c9"}
)

// TestACreateThatFailsCreatesNothing: an instance that cannot write
// reports that it did not create the conference, so that the MRFC does
// not mix a party in a context that no other instance can find. Another
// instance then creates the conference as the first one.
func TestACreateThatFailsCreatesNothing(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			stopped := mrfcstore.New(storetest.StopAfter(kv, 0), cfg)
			if got, created := stopped.CreateConference("room1", mix); created || got != (mrfc.ConferenceMedia{}) {
				t.Fatalf("CreateConference on a stopped instance = %+v, %v; want no media, not created", got, created)
			}
			other := mrfcstore.New(kv, cfg)
			if got, ok := other.Conference("room1"); ok {
				t.Fatalf("the stopped instance left %+v", got)
			}
			if got, created := other.CreateConference("room1", otherMix); !created || got != otherMix {
				t.Errorf("CreateConference on another instance = %+v, %v", got, created)
			}
		})
	}
}

// TestARemoveThatStopsLeavesTheConference: an instance that stops before
// it removes an ended conference leaves the item. The MRFC of another
// instance that a party reaches then finds that the MRFP has no such
// context any more, and removes the item itself, with the same media.
func TestARemoveThatStopsLeavesTheConference(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			mrfcstore.New(kv, cfg).CreateConference("room1", mix)
			mrfcstore.New(storetest.StopAfter(kv, 0), cfg).RemoveConference("room1", mix)

			other := mrfcstore.New(kv, cfg)
			if got, ok := other.Conference("room1"); !ok || got != mix {
				t.Fatalf("Conference after the stopped removal = %+v, %v; want %+v", got, ok, mix)
			}
			other.RemoveConference("room1", mix)
			if got, ok := other.Conference("room1"); ok {
				t.Errorf("the other instance did not remove the conference: %+v", got)
			}
		})
	}
}
