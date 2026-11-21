package kvstore_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// A registrar keeps a registration under its private identity, an index
// item for the public identity, and the expiry in a time index. Whichever
// instance claims the expiry ends the registration.
func Example() {
	ctx := context.Background()
	now := time.UnixMilli(1_790_000_000_000)
	s := kvstore.NewMemoryStore(func() time.Time { return now })

	impi := "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	// The index item first: a reader that finds it checks the main item,
	// so an index item without one is only a miss.
	s.Put(ctx, "impu:tel:+12025550101", []byte(impi), 0)
	s.Put(ctx, "registration:"+impi, []byte(`{"Contact":"sip:ue@10.45.0.2"}`), 0)
	s.Schedule(ctx, "registration-expiry", impi, now.Add(time.Hour))

	// Another instance looks the user up by the public identity.
	key, _ := s.Get(ctx, "impu:tel:+12025550101")
	reg, _ := s.Get(ctx, "registration:"+string(key.Value))
	fmt.Printf("%s\n", reg.Value)

	// An hour later one instance claims the expiry, for a minute.
	claims, _ := s.ClaimDue(ctx, "registration-expiry", now.Add(time.Hour), time.Minute, 10)
	for _, c := range claims {
		item, err := s.Get(ctx, "registration:"+c.Member)
		if err != nil {
			continue
		}
		// Only one of the instances that read the item removes it.
		if err := s.CompareAndDelete(ctx, "registration:"+c.Member, item.Version); errors.Is(err, kvstore.ErrConflict) {
			continue
		}
		fmt.Println("registration expired:", c.Member)
		s.Done(ctx, c)
	}
	// Output:
	// {"Contact":"sip:ue@10.45.0.2"}
	// registration expired: 001010000000001@ims.mnc001.mcc001.3gppnetwork.org
}
