package agwstore_test

import (
	"context"
	"fmt"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/agwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// A relay instance takes the Store of its own name in place of
// agw.NewMemoryContextStore. It saves each context as it changes, and
// when it is created again under the same name, it reads them back to
// open the same ports.
func Example() {
	ctx := context.Background()
	kv := kvstore.NewMemoryStore(nil)
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	cfg := agwstore.Config{Config: kvjson.Config{Prefix: "ims:"}, Relay: "agw-1"}

	store, err := agwstore.New(kv, cfg)
	if err != nil {
		panic(err)
	}
	var _ agw.ContextStore = store // what the relay takes
	store.PutContext(ctx, agw.SavedContext{ID: "c4096", Terminations: []agw.SavedTermination{
		{ID: agw.Access, LocalRTP: 20000, Remote: netip.MustParseAddrPort("192.0.2.10:49170"), Mode: agw.SendReceive},
		{ID: agw.Core, LocalRTP: 20002, Mode: agw.SendReceive},
	}})

	// The process of agw-1 dies, and agw-1 is created again.
	recreated, _ := agwstore.New(kv, cfg)
	saved, _ := recreated.Contexts(ctx)
	for _, c := range saved {
		for _, term := range c.Terminations {
			remote := "not known yet"
			if term.Remote.IsValid() {
				remote = term.Remote.String()
			}
			fmt.Printf("context %s, termination %s: RTP port %d, remote %s, %s\n", c.ID, term.ID, term.LocalRTP, remote, term.Mode)
		}
	}
	// Output:
	// context c4096, termination access: RTP port 20000, remote 192.0.2.10:49170, sendrecv
	// context c4096, termination core: RTP port 20002, remote not known yet, sendrecv
}
