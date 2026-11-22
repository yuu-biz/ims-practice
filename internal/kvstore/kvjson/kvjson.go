// Package kvjson is what the Store of every role needs on top of a
// kvstore.Store: values written as JSON, a time limit for each
// operation, read-modify-write by compare and swap, and index items that
// point to a main item.
//
// Read, Write, CompareAndSwap, Modify, ReadModifyWrite, ReadAndRemove,
// Remove and RemoveIf, ReadIndex, WriteIndex, CompareAndSwapIndex and
// RemoveIndexIf for index items, and WriteDue and RemoveDue for time
// indexes, take the context of the
// caller and return the failure of the store, so that a role whose
// Store interface returns errors can answer that failure, with a 500 in
// SIP, rather than take it for an item that is not there.
//
// Most of the other operations serve the Store interfaces that return
// no errors, which were written for a store in the process that cannot
// fail. They log a failure of the store, and those that read then
// answer as if the item were not there. This is the one place where
// such an adapter differs from the MemoryStore of its role.
package kvjson

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// Config is what every adapter takes.
type Config struct {
	// Prefix comes before every key and time index of the adapter, so
	// that several networks, or tests, can share one store. The adapter
	// adds the name of its role after it.
	Prefix string
	// Timeout bounds each operation on the store; zero means a second.
	Timeout time.Duration
	// Log gets the failures of the store; nil discards them.
	Log *slog.Logger
	// Now is the clock by which the adapter decides whether an item has
	// passed its Expires; nil means time.Now. The store's own clock
	// removes the items whose time to live has run out.
	Now func() time.Time
}

// Client is a kvstore.Store seen through Config.
type Client struct {
	kv      kvstore.Store
	prefix  string
	timeout time.Duration
	log     *slog.Logger
	now     func() time.Time
}

// New returns a Client whose keys begin with cfg.Prefix and role.
func New(kv kvstore.Store, cfg Config, role string) *Client {
	c := &Client{kv: kv, prefix: cfg.Prefix + role + ":", timeout: cfg.Timeout, log: cfg.Log, now: cfg.Now}
	if c.timeout == 0 {
		c.timeout = time.Second
	}
	if c.log == nil {
		c.log = slog.New(slog.DiscardHandler)
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// Now is the time by the Client's clock.
func (c *Client) Now() time.Time { return c.now() }

// Key is the key of an item: the prefix, the kind of item, and the
// parts that name it. A part may hold any character; ":" and "%" are
// escaped, so that two different lists of parts never give the same key
// (a Call-ID may contain ":", RFC 3261 25.1 word).
func (c *Client) Key(kind string, parts ...string) string {
	return c.prefix + kind + ":" + Member(parts...)
}

// Member joins parts as Key does, for a member of a time index.
func Member(parts ...string) string {
	escaped := make([]string, len(parts))
	for i, p := range parts {
		escaped[i] = escaper.Replace(p)
	}
	return strings.Join(escaped, ":")
}

// SplitMember gives back the parts that Member joined.
func SplitMember(member string) []string {
	parts := strings.Split(member, ":")
	for i, p := range parts {
		parts[i] = unescaper.Replace(p)
	}
	return parts
}

var (
	escaper   = strings.NewReplacer("%", "%25", ":", "%3A")
	unescaper = strings.NewReplacer("%3A", ":", "%25", "%")
)

// Index is the name of a time index of the Client.
func (c *Client) Index(name string) string { return c.prefix + name }

// within bounds ctx by the Timeout of the Client.
func (c *Client) within(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.timeout)
}

// failed logs err, the failure of op on key.
func (c *Client) failed(op, key string, err error) {
	c.log.Warn("kvstore operation failed", "operation", op, "key", key, "error", err.Error())
}

// TTLUntil is the time to live of an item that is to go at expires. A
// zero expires means no time to live. One that has already passed gets
// the shortest time to live there is, since zero would keep the item
// for ever.
func (c *Client) TTLUntil(expires time.Time) time.Duration {
	if expires.IsZero() {
		return 0
	}
	return max(expires.Sub(c.now()), time.Millisecond)
}

// Read reads the item under key into v, and returns its version. It
// returns kvstore.ErrNotFound when there is none, and otherwise the
// failure of the store or of decoding the item.
func Read[T any](ctx context.Context, c *Client, key string, v *T) (version string, err error) {
	ctx, cancel := c.within(ctx)
	defer cancel()
	item, err := c.kv.Get(ctx, key)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(item.Value, v); err != nil {
		return "", fmt.Errorf("decode: %w", err)
	}
	return item.Version, nil
}

// Get is Read for a Store interface that returns no errors. It reports
// false when there is no item, or the store failed.
func Get[T any](c *Client, key string, v *T) (version string, ok bool) {
	version, err := Read(context.Background(), c, key, v)
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		c.failed("Get", key, err)
	}
	return version, err == nil
}

// Write writes v under key, whatever is there, and returns the failure
// of the store.
func Write[T any](ctx context.Context, c *Client, key string, v T, ttl time.Duration) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	_, err := c.kv.Put(ctx, key, encode(v), ttl)
	return err
}

// Put is Write for a Store interface that returns no errors: it logs a
// failure of the store.
func Put[T any](c *Client, key string, v T, ttl time.Duration) {
	if err := Write(context.Background(), c, key, v, ttl); err != nil {
		c.failed("Put", key, err)
	}
}

// CompareAndSwap writes v under key if the item is still at version, ""
// for none, and returns the new version. It returns kvstore.ErrConflict
// when someone else wrote first, and the failure of the store otherwise.
func CompareAndSwap[T any](ctx context.Context, c *Client, key, version string, v T, ttl time.Duration) (string, error) {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.CompareAndSwap(ctx, key, version, encode(v), ttl)
}

// Swap is CompareAndSwap for a Store interface that returns no errors:
// it also logs a failure of the store.
func Swap[T any](c *Client, key, version string, v T, ttl time.Duration) (string, error) {
	newVersion, err := CompareAndSwap(context.Background(), c, key, version, v, ttl)
	if err != nil && !errors.Is(err, kvstore.ErrConflict) {
		c.failed("CompareAndSwap", key, err)
	}
	return newVersion, err
}

// Remove removes the item under key, if there is one, and returns the
// failure of the store.
func (c *Client) Remove(ctx context.Context, key string) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.Delete(ctx, key)
}

// Delete is Remove for a Store interface that returns no errors: it
// logs a failure of the store.
func (c *Client) Delete(key string) {
	if err := c.Remove(context.Background(), key); err != nil {
		c.failed("Delete", key, err)
	}
}

// RemoveIf removes the item under key if it is still at version. It
// returns kvstore.ErrConflict when the item has changed or gone, and the
// failure of the store otherwise.
func (c *Client) RemoveIf(ctx context.Context, key, version string) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.CompareAndDelete(ctx, key, version)
}

// DeleteIf is RemoveIf for a Store interface that returns no errors. It
// reports whether it removed the item, and logs a failure of the store.
func (c *Client) DeleteIf(key, version string) bool {
	err := c.RemoveIf(context.Background(), key, version)
	if err != nil && !errors.Is(err, kvstore.ErrConflict) {
		c.failed("CompareAndDelete", key, err)
	}
	return err == nil
}

// maxAttempts bounds the rounds of a read-modify-write that others keep
// overtaking. Each lost round means that another instance changed the
// item, so a few are normal under contention; this many means
// something else is wrong.
const maxAttempts = 100

// ErrGaveUp: an Update or a Take lost to other writers maxAttempts
// times, or the store failed.
var ErrGaveUp = errors.New("kvjson: the item could not be changed")

// Update reads the item under key, lets change change it, and writes it
// back if no one else wrote it in between; otherwise it starts again
// from the new value. change may therefore run more than once, each
// time on a fresh copy, and must not do anything but change its
// argument. ttl gives the time to live of the value that change made.
// Update returns kvstore.ErrNotFound when there is no item, the error of
// change when change fails (and then writes nothing), or ErrGaveUp.
func Update[T any](c *Client, key string, ttl func(T) time.Duration, change func(*T) error) (T, error) {
	for range maxAttempts {
		var v T
		version, ok := Get(c, key, &v)
		if !ok {
			var zero T
			return zero, kvstore.ErrNotFound
		}
		if err := change(&v); err != nil {
			var zero T
			return zero, err
		}
		_, err := Swap(c, key, version, v, ttl(v))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			var zero T
			return zero, ErrGaveUp
		}
	}
	c.failed("Update", key, ErrGaveUp)
	var zero T
	return zero, ErrGaveUp
}

// Modify is Update with the context of the caller. It returns
// kvstore.ErrNotFound when there is no item, the error of change when
// change fails (and then writes nothing), the failure of the store,
// which it does not take for a missing item, or ErrGaveUp when others
// overtook it maxAttempts times.
func Modify[T any](ctx context.Context, c *Client, key string, ttl func(T) time.Duration, change func(*T) error) (T, error) {
	var zero T
	for range maxAttempts {
		var v T
		version, err := Read(ctx, c, key, &v)
		if err != nil {
			return zero, err
		}
		if err := change(&v); err != nil {
			return zero, err
		}
		_, err = CompareAndSwap(ctx, c, key, version, v, ttl(v))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return zero, err
		}
	}
	return zero, ErrGaveUp
}

// ReadModifyWrite is Update for an item that may not be there yet, with
// the context of the caller: change gets the zero value when there is no
// item, and the item is then written only if there is still none. It
// returns the failure of the store, which it does not take for a
// missing item, or ErrGaveUp when others overtook it maxAttempts times.
func ReadModifyWrite[T any](ctx context.Context, c *Client, key string, ttl func(T) time.Duration, change func(*T)) (T, error) {
	var zero T
	for range maxAttempts {
		var v T
		version, err := Read(ctx, c, key, &v)
		if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
			return zero, err
		}
		change(&v)
		_, err = CompareAndSwap(ctx, c, key, version, v, ttl(v))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return zero, err
		}
	}
	return zero, ErrGaveUp
}

// Upsert is Update for an item that may not be there yet: change then
// gets the zero value, and the item is written only if there is still
// none. It serves the Store interfaces that return no errors, and takes
// a failed read for a missing item.
func Upsert[T any](c *Client, key string, ttl func(T) time.Duration, change func(*T)) (T, error) {
	for range maxAttempts {
		var v T
		version, _ := Get(c, key, &v)
		change(&v)
		_, err := Swap(c, key, version, v, ttl(v))
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			var zero T
			return zero, ErrGaveUp
		}
	}
	c.failed("Upsert", key, ErrGaveUp)
	var zero T
	return zero, ErrGaveUp
}

// ReadAndRemove reads the item under key and removes it, if no one else
// changed or removed it in between: a Read and a RemoveIf at the version
// read. Of several callers that take the same item, only one gets it. It
// returns kvstore.ErrNotFound when there is no item, or another caller
// took it first; the failure of the store; or ErrGaveUp when others
// changed the item maxAttempts times between the two operations.
func ReadAndRemove[T any](ctx context.Context, c *Client, key string) (T, error) {
	var zero T
	for range maxAttempts {
		var v T
		version, err := Read(ctx, c, key, &v)
		if err != nil {
			return zero, err
		}
		err = c.RemoveIf(ctx, key, version)
		if err == nil {
			return v, nil
		}
		if !errors.Is(err, kvstore.ErrConflict) {
			return zero, err
		}
		// Changed, or removed by another taker: read again to tell which.
	}
	return zero, ErrGaveUp
}

// Take is ReadAndRemove for a Store interface that returns no errors. It
// reports false when there is no item, another caller took it, or the
// store failed, and logs a failure of the store.
func Take[T any](c *Client, key string) (T, bool) {
	v, err := ReadAndRemove[T](context.Background(), c, key)
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		c.failed("Take", key, err)
	}
	return v, err == nil
}

// encode cannot fail for the values of the Stores: they hold strings,
// numbers, times, addresses, and slices and structs of them.
func encode[T any](v T) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("kvjson: cannot encode %T: %v", v, err))
	}
	return b
}

// PutIndex writes the index item key, whose value is the name of a main
// item, target.
//
// The adapters write an index item before the main item it points to,
// and remove it after: a reader that comes in between finds an index
// item whose main item does not hold the identity yet, or any more, and
// takes that as a miss (IndexTarget, then a check of the main item).
// An index item left behind by an instance that stopped between the two
// writes is only such a miss, and goes with its time to live.
//
// PutIndex logs a failure of the store; WriteIndex returns it.
func (c *Client) PutIndex(key, target string, ttl time.Duration) {
	if err := c.WriteIndex(context.Background(), key, target, ttl); err != nil {
		c.failed("Put", key, err)
	}
}

// WriteIndex is PutIndex with the context of the caller, and returns the
// failure of the store.
func (c *Client) WriteIndex(ctx context.Context, key, target string, ttl time.Duration) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	_, err := c.kv.Put(ctx, key, []byte(target), ttl)
	return err
}

// ReadIndex reads the index item key, and returns the name of the main
// item it points to and its own version. It returns kvstore.ErrNotFound
// when there is none, and otherwise the failure of the store.
func (c *Client) ReadIndex(ctx context.Context, key string) (target, version string, err error) {
	ctx, cancel := c.within(ctx)
	defer cancel()
	item, err := c.kv.Get(ctx, key)
	if err != nil {
		return "", "", err
	}
	return string(item.Value), item.Version, nil
}

// IndexTarget is ReadIndex for a Store interface that returns no
// errors. It reports false when there is no index item, or the store
// failed.
func (c *Client) IndexTarget(key string) (target, version string, ok bool) {
	target, version, err := c.ReadIndex(context.Background(), key)
	if err != nil && !errors.Is(err, kvstore.ErrNotFound) {
		c.failed("Get", key, err)
	}
	return target, version, err == nil
}

// CompareAndSwapIndex writes the index item key, pointing to target,
// only if it is still at version, "" for none: an index item that only
// one main item may hold, such as a public identity that belongs to one
// subscription. It returns the new version, kvstore.ErrConflict when
// someone else wrote first, and the failure of the store otherwise.
func (c *Client) CompareAndSwapIndex(ctx context.Context, key, version, target string, ttl time.Duration) (string, error) {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.CompareAndSwap(ctx, key, version, []byte(target), ttl)
}

// SwapIndex is CompareAndSwapIndex for a Store interface that returns
// no errors: it also logs a failure of the store.
func (c *Client) SwapIndex(key, version, target string, ttl time.Duration) (string, error) {
	newVersion, err := c.CompareAndSwapIndex(context.Background(), key, version, target, ttl)
	if err != nil && !errors.Is(err, kvstore.ErrConflict) {
		c.failed("CompareAndSwap", key, err)
	}
	return newVersion, err
}

// DeleteIndexIf removes the index item key if it still points to
// target. An index item that another main item has taken over since is
// left alone.
func (c *Client) DeleteIndexIf(key, target string) {
	if got, version, ok := c.IndexTarget(key); ok && got == target {
		c.DeleteIf(key, version)
	}
}

// RemoveIndexIf is DeleteIndexIf with the context of the caller. An
// index item that is gone, or that another main item has taken over, is
// no failure; a failure of the store is.
func (c *Client) RemoveIndexIf(ctx context.Context, key, target string) error {
	got, version, err := c.ReadIndex(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if got != target {
		return nil
	}
	if err := c.RemoveIf(ctx, key, version); err != nil && !errors.Is(err, kvstore.ErrConflict) {
		return err
	}
	return nil
}

// WriteDue puts member in the time index of the Client named index, due
// at at, and returns the failure of the store.
func (c *Client) WriteDue(ctx context.Context, index, member string, at time.Time) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.Schedule(ctx, c.Index(index), member, at)
}

// Schedule is WriteDue for a Store interface that returns no errors: it
// logs a failure of the store.
func (c *Client) Schedule(index, member string, at time.Time) {
	if err := c.WriteDue(context.Background(), index, member, at); err != nil {
		c.failed("Schedule", c.Index(index), err)
	}
}

// RemoveDue takes member out of the time index, if it is there, and
// returns the failure of the store.
func (c *Client) RemoveDue(ctx context.Context, index, member string) error {
	ctx, cancel := c.within(ctx)
	defer cancel()
	return c.kv.Unschedule(ctx, c.Index(index), member)
}

// Unschedule is RemoveDue for a Store interface that returns no errors:
// it logs a failure of the store.
func (c *Client) Unschedule(index, member string) {
	if err := c.RemoveDue(context.Background(), index, member); err != nil {
		c.failed("Unschedule", c.Index(index), err)
	}
}

// ClaimDue claims the due members of the time index; see
// kvstore.Store.ClaimDue. It returns the error, as Read does, and does
// not log it: its caller is a loop of the adapter's user, not a method
// of the role's Store interface.
func (c *Client) ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	return c.kv.ClaimDue(ctx, c.Index(index), now, lease, limit)
}

// Done ends a claim that ClaimDue gave.
func (c *Client) Done(ctx context.Context, claim kvstore.Claim) error {
	return c.kv.Done(ctx, claim)
}
