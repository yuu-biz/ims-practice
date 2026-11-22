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

// An S-CSCF instance takes the Store in place of scscf.NewMemoryStore.
// Its sweep claims the expiries that are due, handles each of them, and
// marks it done; this is one round of it, for the registrations.
func Example() {
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	kv := kvstore.NewMemoryStore(func() time.Time { return now })
	// In a network: kvstore.NewValkey(kvstore.ValkeyConfig{Address: "kvstore:6379"}).
	store := scscfstore.New(kv, kvjson.Config{Now: func() time.Time { return now }})
	var _ scscf.Store = store // what scscf.New takes

	err := store.PutRegistration(ctx, scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu}, Expires: now.Add(time.Hour)})
	if err != nil {
		fmt.Println(err)
		return
	}

	// An hour later. The lease is longer than the SAR that the handler
	// sends may take.
	now = now.Add(time.Hour)
	expiries, _ := store.ClaimDue(ctx, scscf.RegistrationExpiry, now, 30*time.Second, 100)
	for _, e := range expiries {
		// The handler reads the item under the lock of its user and
		// checks that it has really expired.
		reg, ok, err := store.Registration(ctx, e.PrivateIdentity)
		if err != nil {
			// Without Done, the lease runs out and an instance claims the
			// expiry again.
			continue
		}
		if ok && !reg.Expires.After(now) {
			fmt.Println("registration expired:", reg.PrivateIdentity)
		}
		store.Done(ctx, e)
	}
	// Output:
	// registration expired: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org
}
