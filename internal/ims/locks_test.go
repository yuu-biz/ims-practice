package ims

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestUserLocksSerialisesOneUser(t *testing.T) {
	var locks UserLocks
	unlock, err := locks.Lock(context.Background(), "alice@ims.example.org")
	if err != nil {
		t.Fatal(err)
	}

	got := make(chan struct{})
	go func() {
		// The same NAI in other case is the same user.
		unlock, err := locks.Lock(context.Background(), "Alice@IMS.example.org")
		if err != nil {
			t.Error(err)
			return
		}
		close(got)
		unlock()
	}()
	select {
	case <-got:
		t.Fatal("a second holder got the lock while the first held it")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("the waiter did not get the lock once it was given back")
	}
}

func TestUserLocksDoesNotHoldUpOtherUsers(t *testing.T) {
	var locks UserLocks
	unlockAlice, err := locks.Lock(context.Background(), "alice@ims.example.org")
	if err != nil {
		t.Fatal(err)
	}
	defer unlockAlice()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlockBob, err := locks.Lock(ctx, "bob@ims.example.org")
	if err != nil {
		t.Fatalf("bob waited for alice's lock: %v", err)
	}
	unlockBob()
}

func TestUserLocksGivesUpWhenContextEnds(t *testing.T) {
	var locks UserLocks
	unlock, err := locks.Lock(context.Background(), "alice@ims.example.org")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waiterUnlock, err := locks.Lock(ctx, "alice@ims.example.org")
	if !errors.Is(err, context.DeadlineExceeded) || waiterUnlock != nil {
		t.Fatalf("Lock while held = (%v, %v), want (nil, deadline exceeded)", waiterUnlock != nil, err)
	}

	// The waiter that gave up leaves the lock usable.
	unlock()
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err = locks.Lock(ctx, "alice@ims.example.org")
	if err != nil {
		t.Fatalf("Lock after the holder gave it back: %v", err)
	}
	unlock()
}

func TestUserLocksForgetsFreeLocks(t *testing.T) {
	var locks UserLocks
	unlock, _ := locks.Lock(context.Background(), "alice@ims.example.org")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.Lock(ctx, "alice@ims.example.org"); err == nil {
		t.Fatal("Lock with an ended context took a held lock")
	}
	unlock()
	if n := len(locks.locks); n != 0 {
		t.Fatalf("%d locks are kept after every holder and waiter left", n)
	}
}
