package kvlock_test

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
)

// registrar stands for a role, such as the S-CSCF, that holds an
// ims.UserLocker: an ims.UserLocks with one instance, a kvlock.Locker
// with several.
type registrar struct {
	users ims.UserLocker
	log   *slog.Logger
}

// register handles one REGISTER of privateID under its lock, and
// returns the status code of the answer.
func (r *registrar) register(ctx context.Context, privateID string) int {
	unlock, err := r.users.Lock(ctx, privateID)
	if err != nil {
		// A request whose state cannot be read or written gets 500.
		r.log.Warn("user lock not had", "error", err.Error())
		return 500
	}
	defer unlock()
	// Read the registration, send the SAR, write the registration.
	return 200
}

// Two instances of the S-CSCF share a store. Each makes a Locker on it
// with the same prefix and role, so the two never handle one user at
// the same time.
func Example() {
	ctx := context.Background()
	kv := kvstore.NewMemoryStore(nil)
	scscf1 := kvlock.New(kv, kvlock.Config{Role: "scscf", Holder: "scscf-1.ims.example"})
	scscf2 := &registrar{log: slog.New(slog.DiscardHandler),
		users: kvlock.New(kv, kvlock.Config{Role: "scscf", Holder: "scscf-2.ims.example"})}

	// scscf-1 is in the middle of a registration of the user: it waits
	// for the SAA.
	unlock, err := scscf1.Lock(ctx, impi)
	if err != nil {
		fmt.Println(err)
		return
	}
	lock, _ := kv.Get(ctx, "scscf:lock:"+impi)
	fmt.Printf("held by %s\n", lock.Value)

	// A REGISTER of the same user reaches scscf-2, which gives up when
	// its context ends.
	waiting, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	fmt.Println("scscf-2 while scscf-1 holds the lock:", scscf2.register(waiting, impi))
	cancel()

	unlock()
	fmt.Println("scscf-2 after the release:", scscf2.register(ctx, impi))
	// Output:
	// held by scscf-1.ims.example
	// scscf-2 while scscf-1 holds the lock: 500
	// scscf-2 after the release: 200
}
