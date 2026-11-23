package mmtelstore_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mmtelstore"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
)

// An MMTel AS instance takes one Store in place of mmtel.NewMemoryStore,
// both for its calls and for its conferences. The instance that hosts a
// conference writes its record at each change; any other instance can
// read it, by the conference ID that the conference URI holds.
func Example() {
	kv := kvstore.NewMemoryStore(nil)
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	cfg := kvjson.Config{Prefix: "ims:"}
	const domain = "ims.mnc001.mcc001.3gppnetwork.org"

	store := mmtelstore.New(kv, cfg)
	as := mmtel.New(mmtel.Config{Host: "mmtel-1." + domain, Port: 5060, HomeDomain: domain,
		MRFC: "sip:mrfc." + domain, ConferenceDomain: "conf." + domain},
		nil, store, store, nil, slog.New(slog.DiscardHandler))
	_ = as

	// What the focus of mmtel-1 writes once the creator has joined.
	ctx := context.Background()
	err := store.PutConference(ctx, mmtel.ConferenceRecord{
		ID: "conf-a1", URI: "sip:conf-a1@conf." + domain, Creator: "tel:+12025550101",
		MRFC: "sip:conf=conf-a1@mrfc." + domain,
		Participants: []mmtel.ConferenceParticipant{{Participant: confinfo.Participant{
			User: "tel:+12025550101", Status: confinfo.Connected, JoiningMethod: confinfo.DialedIn}}},
	})
	if err != nil {
		fmt.Println(err)
		return
	}

	other := mmtelstore.New(kv, cfg) // the Store of mmtel-2
	r, _, err := other.Conference(ctx, "conf-a1")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, p := range r.Participants {
		fmt.Printf("%s: %s is %s (%s)\n", r.URI, p.User, p.Status, p.JoiningMethod)
	}
	// Output:
	// sip:conf-a1@conf.ims.mnc001.mcc001.3gppnetwork.org: tel:+12025550101 is connected (dialed-in)
}
