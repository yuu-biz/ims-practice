package ims

import (
	"context"
	"strings"
	"sync"
	"time"
)

// UserLocker serialises what happens to one user across the instances
// of a role, without holding up the other users.
// UserLocks, in the process, is one; a lock in the shared store is the
// other.
type UserLocker interface {
	// Lock waits until it holds the lock of user, or ctx ends. On an
	// error nothing is held and unlock is nil. The S-CSCF and the HSS
	// lock a private identity, a NAI, and the IP-SM-GW a public one;
	// both compare without case.
	Lock(ctx context.Context, user string) (unlock func(), err error)
}

const (
	// UserLockLease bounds how long a lock in the store is held if its
	// holder stops. It is longer than the work under the lock: one Cx
	// or Sh round trip, which waits up to 5 s for its answer, and a few
	// operations on the store.
	UserLockLease = 15 * time.Second
	// ExpiryLease is the lease of a claimed expiry (kvstore.ClaimDue):
	// how long the instance that claimed it has to handle it before
	// another one may.
	ExpiryLease = 15 * time.Second
	// SweepInterval is how often each instance claims the expiries that
	// are due.
	SweepInterval = time.Second
)

// UserLocks is a lock per user in the memory of one process, for a role
// that runs as one instance. A lock is removed once no one holds it or
// waits for it, so that the table does not grow with every user ever
// seen. The zero value is ready to use.
type UserLocks struct {
	mu    sync.Mutex
	locks map[string]*userLock
}

var _ UserLocker = (*UserLocks)(nil)

type userLock struct {
	held  chan struct{} // has a token while someone holds the lock
	users int           // the holder and the waiters
}

// Lock takes the lock of user, and returns the function that gives it
// back. Its only error is that of ctx, when ctx ends before the lock is
// free.
func (u *UserLocks) Lock(ctx context.Context, user string) (unlock func(), err error) {
	k := strings.ToLower(user)
	u.mu.Lock()
	if u.locks == nil {
		u.locks = map[string]*userLock{}
	}
	l, ok := u.locks[k]
	if !ok {
		l = &userLock{held: make(chan struct{}, 1)}
		u.locks[k] = l
	}
	l.users++
	u.mu.Unlock()

	select {
	case l.held <- struct{}{}:
		return func() {
			<-l.held
			u.release(k, l)
		}, nil
	case <-ctx.Done():
		u.release(k, l)
		return nil, ctx.Err()
	}
}

// release drops one holder or waiter of l, and l itself with the last.
func (u *UserLocks) release(k string, l *userLock) {
	u.mu.Lock()
	l.users--
	if l.users == 0 {
		delete(u.locks, k)
	}
	u.mu.Unlock()
}
