package hss

import (
	"context"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/ims"
)

// askedLocks are the user locks of an HSS under test, which tell the
// test of each request for a lock, before it waits for the lock.
type askedLocks struct {
	ims.UserLocks
	mu     sync.Mutex
	onLock func(ctx context.Context, user string)
}

func (l *askedLocks) Lock(ctx context.Context, user string) (func(), error) {
	l.mu.Lock()
	onLock := l.onLock
	l.mu.Unlock()
	if onLock != nil {
		onLock(ctx, user)
	}
	return l.UserLocks.Lock(ctx, user)
}

// watch has f told of each request for a lock from now on.
func (l *askedLocks) watch(f func(ctx context.Context, user string)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onLock = f
}

// locksOf is the askedLocks of h, which the HSS of a test has.
func locksOf(h *HSS) *askedLocks { return h.users.(*askedLocks) }
