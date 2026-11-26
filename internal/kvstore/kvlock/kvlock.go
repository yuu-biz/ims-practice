// Package kvlock is a lock per user kept in a kvstore.Store, so that the
// instances of a role that share the store serialise what happens to
// one user, as ims.UserLocks does inside one process. The S-CSCF, the
// HSS and the
// IP-SM-GW take such a lock around the handling of one user.
//
// A lock is one item, <prefix><role>:lock:<user>, written only if it is
// absent and with a time to live: the lease. Its version is the proof
// of holding it, and the release deletes the item only at that version,
// so a holder whose lease ran out cannot free the lock that another
// instance took after it. Its value is the name of the holder and a
// random token that the holder draws for each take, by which a holder
// whose take reached the store without its answer finds the lock its
// own. An instance that waits tries again after a
// short pause, with jitter, until it gets the lock or its context ends.
// The lock is per user, so it never holds up the other users of the
// role.
//
// The lease bounds how long a stopped holder keeps the others waiting.
// It is not renewed: a live holder has to finish within it. Under the
// lock a holder waits for at most two Diameter answers, makes a few
// operations on the store, and may send SIP requests without waiting
// for their responses:
//
//   - the S-CSCF waits for one Cx answer, the MAA of a challenge or the
//     SAA of a registration or a deregistration, and then sends the reg
//     event NOTIFY of each subscription to the user. For each NOTIFY it
//     finds the next hop in DNS (RFC 3263) and, where the NOTIFY goes
//     over TCP and no connection is open, opens one, which may take up
//     to 2 s for each target (package sip);
//   - the IP-SM-GW waits for two Sh answers when a reg event NOTIFY says
//     that the user takes short messages, the UDA with the IMSI and then
//     the PUA of SMSRegistrationInfo, and for one, the PUA, when it says
//     that the user no longer does. For a third-party REGISTER it sends
//     a SUBSCRIBE as the S-CSCF sends a NOTIFY;
//   - the HSS answers one request under the lock, and sends its own
//     requests, an RTR or an alert, after it has let the lock go.
//
// Each Diameter request waits at most 5 s for its answer, over all the
// peers it tries (diameter.PeerGroup.Request), and each operation on
// the store at most its own timeout, so the Diameter answers and the
// store fit inside the 15 s lease. The SIP requests add the time DNS
// and the connections take, NOTIFY by NOTIFY, which no timer of this
// package bounds. When the lease runs out first, the release finds the
// item gone or someone else's, and logs it.
//
// Waiters of the same instance queue in the process, so that only one
// of them at a time tries the store; the others wait on a channel and do
// not add to the load of the store. This queue is per user too, and
// removed when no one holds it or waits for it.
package kvlock

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
)

// The pause between two tries of a waiter starts at firstRetry and
// doubles up to maxRetry. Each pause is drawn at random from its upper
// half, so that waiters of several instances that found the lock taken
// at the same moment do not all try again at the same moment. maxRetry
// is what a waiter may lose after the release, small next to the
// T1 of SIP (500 ms, RFC 3261 17.1.1.1).
const (
	firstRetry = 5 * time.Millisecond
	maxRetry   = 100 * time.Millisecond
)

// Config is what a Locker takes.
type Config struct {
	// Prefix comes before every key, as store.prefix does for the Store
	// of the role, so that the lock items sit next to the role's state.
	Prefix string
	// Role is the name of the role whose users are locked, such as
	// scscf, hss or ipsmgw. The instances of one role share the locks;
	// the roles do not.
	Role string
	// Holder is written as the value of a lock, before the token of
	// the take, so that a learner who reads the store sees which
	// instance holds it. Empty means the host name.
	Holder string
	// Lease is how long a lock lasts if its holder does not release it.
	// Zero means ims.UserLockLease.
	Lease time.Duration
	// Timeout bounds each operation on the store; zero means a second,
	// as store.timeout does.
	Timeout time.Duration
	// Log gets a release that failed or came after the lease; nil
	// discards it.
	Log *slog.Logger
}

// Locker hands out the locks of the users of one role. Several Lockers
// with the same Prefix and Role on one store, in one process or many,
// exclude each other.
type Locker struct {
	kv      kvstore.Store
	prefix  string
	holder  []byte
	lease   time.Duration
	timeout time.Duration
	log     *slog.Logger

	mu    sync.Mutex
	queue map[string]*queue
}

// queue is the turn of the goroutines of this process that want the
// lock of one user. turn holds a token while one of them has the lock
// or tries the store for it.
type queue struct {
	turn  chan struct{}
	users int // the one with the turn and the ones that wait for it
}

// New returns a Locker on kv.
func New(kv kvstore.Store, cfg Config) *Locker {
	l := &Locker{kv: kv, prefix: cfg.Prefix + cfg.Role + ":lock:", holder: []byte(cfg.Holder),
		lease: cfg.Lease, timeout: cfg.Timeout, log: cfg.Log, queue: map[string]*queue{}}
	if cfg.Holder == "" {
		host, _ := os.Hostname()
		l.holder = []byte(host)
	}
	if l.lease == 0 {
		l.lease = ims.UserLockLease
	}
	if l.timeout == 0 {
		l.timeout = time.Second
	}
	if l.log == nil {
		l.log = slog.New(slog.DiscardHandler)
	}
	return l
}

var _ ims.UserLocker = (*Locker)(nil)

// Lock waits until it holds the lock of user, or ctx ends. A user is a
// private identity (a NAI) or a public one, which compare without case
// here, as in ims.UserLocks. The error is the end of ctx, or a failure
// of the store: the caller has no lock then, and answers that it could
// not handle the user (500 in SIP, DIAMETER_UNABLE_TO_COMPLY in
// Diameter).
//
// unlock gives the lock back. Call it once; later calls do nothing.
//
// SIMPLIFIED: the writes under the lock carry no fencing token:
// a holder that stops for longer than the lease and then goes on may
// overwrite what the next holder wrote. Fencing would make the version
// of the lock a condition of every write of the role's Store, which the
// Store of no role takes; the lease is longer than the longest work under the lock.
func (l *Locker) Lock(ctx context.Context, user string) (unlock func(), err error) {
	user = strings.ToLower(user)
	// A select with a free turn and an ended ctx picks either, and a
	// MemoryStore does not look at ctx.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("kvlock: lock of %s: %w", user, err)
	}
	q, err := l.waitTurn(ctx, user)
	if err != nil {
		return nil, err
	}
	key := l.prefix + kvjson.Member(user)
	version, err := l.acquire(ctx, key)
	if err != nil {
		l.endTurn(user, q)
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			l.release(key, version)
			l.endTurn(user, q)
		})
	}, nil
}

// waitTurn waits until this goroutine is the one of the process that
// may hold the lock of user.
func (l *Locker) waitTurn(ctx context.Context, user string) (*queue, error) {
	l.mu.Lock()
	q, ok := l.queue[user]
	if !ok {
		q = &queue{turn: make(chan struct{}, 1)}
		l.queue[user] = q
	}
	q.users++
	l.mu.Unlock()

	select {
	case q.turn <- struct{}{}:
		return q, nil
	case <-ctx.Done():
		l.leaveQueue(user, q)
		return nil, fmt.Errorf("kvlock: waiting for the lock of %s: %w", user, ctx.Err())
	}
}

// endTurn passes the turn of user to the next goroutine of the process.
func (l *Locker) endTurn(user string, q *queue) {
	<-q.turn
	l.leaveQueue(user, q)
}

func (l *Locker) leaveQueue(user string, q *queue) {
	l.mu.Lock()
	defer l.mu.Unlock()
	q.users--
	if q.users == 0 {
		delete(l.queue, user)
	}
}

// acquire writes the lock under key if there is none, until it can or
// ctx ends, and returns the version that it wrote.
//
// A take that fails other than by a conflict may have reached the store
// all the same, with only its answer lost, as when the answer comes
// after the timeout. The lock would then stay, held by no one, and keep
// the user from every instance until the lease ran out. acquire reads
// the item again, and finds it its own by the token of the take: no
// other take writes that token, and none writes an item that is there.
// A lock found its own is held, as if the answer had come, while at
// least half of its lease is left, counted from just before the take:
// the store started the lease no earlier. The caller can then go on
// handling the user rather than answer that it could not. With the
// defaults, a lease of 15 s and a timeout of 1 s, the lost answer and
// the read take at most 2 s, and the lock is always kept. A timeout
// long next to the lease could leave a lock that ends soon after it is
// handed back, while its holder still works under it and another
// instance takes it: such a lock is released, and Lock fails as if
// nothing had been written. Half is the line: the work under the lock
// fits in the lease with room to spare (see the package documentation),
// and a lost answer may take no more than half of the lease from it.
// When ctx has ended, the caller no longer wants the
// lock, and the lock found its own is released too.
func (l *Locker) acquire(ctx context.Context, key string) (string, error) {
	value := fmt.Appendf(nil, "%s %016x", l.holder, rand.Uint64())
	pause := firstRetry
	for {
		started := time.Now()
		try, cancel := context.WithTimeout(ctx, l.timeout)
		version, err := l.kv.CompareAndSwap(try, key, "", value, l.lease)
		cancel()
		if err == nil {
			return version, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			if version, own := l.own(key, value); own {
				if ctx.Err() == nil && time.Since(started) <= l.lease/2 {
					return version, nil
				}
				l.release(key, version)
				if ctx.Err() == nil {
					return "", fmt.Errorf("kvlock: taking %s: the answer was lost, and less than half the lease of %v was left: %w", key, l.lease, err)
				}
			}
		}
		if ctx.Err() != nil {
			return "", fmt.Errorf("kvlock: waiting for %s: %w", key, ctx.Err())
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return "", fmt.Errorf("kvlock: taking %s: %w", key, err)
		}
		// Someone holds the lock.
		t := time.NewTimer(pause/2 + rand.N(pause/2))
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return "", fmt.Errorf("kvlock: waiting for %s: %w", key, ctx.Err())
		}
		pause = min(2*pause, maxRetry)
	}
}

// own reads the lock under key, and tells its version if it holds
// value, the token of a take of this holder.
func (l *Locker) own(key string, value []byte) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	item, err := l.kv.Get(ctx, key)
	if err != nil || !bytes.Equal(item.Value, value) {
		return "", false
	}
	return item.Version, true
}

// release deletes the lock under key if it is still the one at version.
// Neither outcome that it logs can be undone here: the lock is gone
// either way, or ends with its lease.
func (l *Locker) release(key, version string) {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	switch err := l.kv.CompareAndDelete(ctx, key, version); {
	case err == nil:
	case errors.Is(err, kvstore.ErrConflict):
		l.log.Warn("user lock released after its lease ran out; another instance may have handled the user meanwhile",
			"key", key, "lease", l.lease.String())
	default:
		l.log.Warn("user lock not released; it ends with its lease", "key", key, "lease", l.lease.String(),
			"error", err.Error())
	}
}
