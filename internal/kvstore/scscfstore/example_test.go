package scscfstore_test

import (
	"context"
	"fmt"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/scscf"
)

// An S-CSCF instance takes the Store in place of scscf.NewMemoryStore,
// and runs one loop that claims the due timers, in place of
// time.AfterFunc. The loop calls what the timer would have called; that
// function reads the item and checks that it has really expired.
func Example() {
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	kv := kvstore.NewMemoryStore(func() time.Time { return now })
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	store := scscfstore.New(kv, scscfstore.Config{Config: kvjson.Config{Now: func() time.Time { return now }}})
	var _ scscf.Store = store // what scscf.New takes

	store.PutRegistration(scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu}, Expires: now.Add(time.Hour)})

	registrationExpired := func(privateID string, expires time.Time) {
		fmt.Println("registration expired:", privateID)
	}
	// One round of the loop, an hour later. The lease is longer than the
	// SAR that the timer function sends may take.
	now = now.Add(time.Hour)
	claims, _ := store.ClaimDue(ctx, scscfstore.RegistrationTimer, now, 30*time.Second, 100)
	for _, c := range claims {
		if reg, ok := store.Registration(c.Member); ok && !reg.Expires.After(now) {
			registrationExpired(reg.PrivateIdentity, reg.Expires)
		}
		store.Done(ctx, c)
	}
	// Output:
	// registration expired: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org
}
