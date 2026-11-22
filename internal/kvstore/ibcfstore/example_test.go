package ibcfstore_test

import (
	"context"
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/ibcf"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ibcfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// Each IBCF instance takes a Store in place of ibcf.NewMemoryStore. All
// the instances of a network use the same store and prefix; each keeps
// its records under its own name, the host of its internal URI, which
// a new process of the instance has again.
func Example() {
	kv := kvstore.NewMemoryStore(nil)
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	cfg := kvjson.Config{Prefix: "ims:"}
	const instance = "ibcf-1.ims.mnc001.mcc001.3gppnetwork.org"
	ctx := context.Background()

	// What a process of the instance writes for a call that it relays
	// to network B: the TrGW context once the INVITE has its SDP. An
	// error would be a failure of the store, which the IBCF answers
	// with 500 (Server Internal Error).
	before := ibcfstore.New(kv, cfg)
	err := before.PutCall(ctx, ibcf.CallRecord{Instance: instance, ID: "k3x9", Direction: interconnect.Exit,
		Peer: "peer-b", Media: &ibcf.MediaContext{ID: "c17"}})
	if err != nil {
		panic(err)
	}

	// The process stops. The next one reads the records of the instance
	// in Listen, before its UAs start, and ends their calls.
	after := ibcfstore.New(kv, cfg)
	records, err := after.CallsOf(ctx, instance)
	if err != nil {
		panic(err)
	}
	for _, r := range records {
		fmt.Printf("call %s to %s, TrGW context %s\n", r.ID, r.Peer, r.Media.ID)
	}
	// Output:
	// call k3x9 to peer-b, TrGW context c17
}
