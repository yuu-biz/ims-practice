package mrfcstore_test

import (
	"fmt"
	"log/slog"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/mrfcstore"
	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// Each MRFC instance takes a Store in place of mrfc.NewMemoryStore. All
// the instances of a network use the same store and prefix, so that a
// party that reaches any of them joins the mix that the first one made.
func Example() {
	kv := kvstore.NewMemoryStore(nil)
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	cfg := kvjson.Config{Prefix: "ims:"}
	const domain = "ims.mnc001.mcc001.3gppnetwork.org"

	var instances []*mrfcstore.Store
	for _, host := range []string{"mrfc-1." + domain, "mrfc-2." + domain} {
		store := mrfcstore.New(kv, cfg)
		processors := []mrfc.Processor{
			{Name: "http://mrfp-1:8080", Control: agw.NewClient("http://mrfp-1:8080")},
			{Name: "http://mrfp-2:8080", Control: agw.NewClient("http://mrfp-2:8080")},
		}
		_, err := mrfc.New(mrfc.Config{Host: host, Port: 5060, NotifyURL: "http://" + host + ":8090/notify"},
			processors, store, nil, slog.New(slog.DiscardHandler))
		if err != nil {
			panic(err)
		}
		instances = append(instances, store)
	}

	// What the two instances do with the Store when the first parties
	// of conf=room1 reach both at once, each having made a context on
	// the MRFP of its own choice.
	media, created := instances[0].CreateConference("room1", mrfc.ConferenceMedia{MRFP: "http://mrfp-1:8080", Context: "c1"})
	fmt.Printf("mrfc-1: created %v, mix in %s on %s\n", created, media.Context, media.MRFP)
	media, created = instances[1].CreateConference("room1", mrfc.ConferenceMedia{MRFP: "http://mrfp-2:8080", Context: "c4"})
	fmt.Printf("mrfc-2: created %v, mix in %s on %s\n", created, media.Context, media.MRFP)
	// Output:
	// mrfc-1: created true, mix in c1 on http://mrfp-1:8080
	// mrfc-2: created false, mix in c1 on http://mrfp-1:8080
}
