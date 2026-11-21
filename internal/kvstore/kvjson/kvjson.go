// Package kvjson is what the Store of every role needs on top of a
// kvstore.Store: values written as JSON, a time limit for each
// operation, read-modify-write by compare and swap, and index items that
// point to a main item.
//
// The Store interfaces of the roles return no errors: they were written
// for a store in the process, which cannot fail. A Client therefore logs
// a failure of the store and answers as if the item were not there.
// This is the one place where an adapter differs from the MemoryStore
// of its role. A Store interface that takes a context and returns an
// error would let the role answer a failure of the store with an error
// instead.
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

func (c *Client) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.timeout)
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

// Get reads the item under key into v, and returns its version. It
// reports false when there is none, or the store failed.
func Get[T any](c *Client, key string, v *T) (version string, ok bool) {
	ctx, cancel := c.context()
	defer cancel()
	item, err := c.kv.Get(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return "", false
	}
	if err != nil {
		c.failed("Get", key, err)
		return "", false
	}
	if err := json.Unmarshal(item.Value, v); err != nil {
		c.failed("Get", key, fmt.Errorf("decode: %w", err))
		return "", false
	}
	return item.Version, true
}

// Put writes v under key, whatever is there.
func Put[T any](c *Client, key string, v T, ttl time.Duration) {
	ctx, cancel := c.context()
	defer cancel()
	if _, err := c.kv.Put(ctx, key, encode(v), ttl); err != nil {
		c.failed("Put", key, err)
	}
}

// Swap writes v under key if the item is still at version, "" for none.
// It returns kvstore.ErrConflict when someone else wrote first, and the
// failure of the store otherwise.
func Swap[T any](c *Client, key, version string, v T, ttl time.Duration) (string, error) {
	ctx, cancel := c.context()
	defer cancel()
	newVersion, err := c.kv.CompareAndSwap(ctx, key, version, encode(v), ttl)
	if err != nil && !errors.Is(err, kvstore.ErrConflict) {
		c.failed("CompareAndSwap", key, err)
	}
	return newVersion, err
}

// Delete removes the item under key.
func (c *Client) Delete(key string) {
	ctx, cancel := c.context()
	defer cancel()
	if err := c.kv.Delete(ctx, key); err != nil {
		c.failed("Delete", key, err)
	}
}

// DeleteIf removes the item under key if it is still at version, and
// reports whether it did.
func (c *Client) DeleteIf(key, version string) bool {
	ctx, cancel := c.context()
	defer cancel()
	err := c.kv.CompareAndDelete(ctx, key, version)
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

// Upsert is Update for an item that may not be there yet: change then
// gets the zero value, and the item is written only if there is still
// none.
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

// Take reads the item under key and removes it, if no one else changed
// or removed it in between. Of several callers that take the same item,
// only one gets it (a Get and a CompareAndDelete, 13.2).
func Take[T any](c *Client, key string) (T, bool) {
	for range maxAttempts {
		var v T
		version, ok := Get(c, key, &v)
		if !ok {
			return v, false
		}
		if c.DeleteIf(key, version) {
			return v, true
		}
		// Changed, removed by another taker, or a failure: read again
		// to tell which.
	}
	c.failed("Take", key, ErrGaveUp)
	var zero T
	return zero, false
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
func (c *Client) PutIndex(key, target string, ttl time.Duration) {
	ctx, cancel := c.context()
	defer cancel()
	if _, err := c.kv.Put(ctx, key, []byte(target), ttl); err != nil {
		c.failed("Put", key, err)
	}
}

// IndexTarget reads the index item key, and returns the name of the
// main item it points to and its own version.
func (c *Client) IndexTarget(key string) (target, version string, ok bool) {
	ctx, cancel := c.context()
	defer cancel()
	item, err := c.kv.Get(ctx, key)
	if errors.Is(err, kvstore.ErrNotFound) {
		return "", "", false
	}
	if err != nil {
		c.failed("Get", key, err)
		return "", "", false
	}
	return string(item.Value), item.Version, true
}

// SwapIndex writes the index item key, pointing to target, only if it
// is still at version, "" for none: an index item that only one main
// item may hold, such as a public identity that belongs to one
// subscription. It returns kvstore.ErrConflict when someone else wrote
// first.
func (c *Client) SwapIndex(key, version, target string, ttl time.Duration) (string, error) {
	ctx, cancel := c.context()
	defer cancel()
	newVersion, err := c.kv.CompareAndSwap(ctx, key, version, []byte(target), ttl)
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

// Schedule puts member in the time index of the Client named index, due
// at at.
func (c *Client) Schedule(index, member string, at time.Time) {
	ctx, cancel := c.context()
	defer cancel()
	if err := c.kv.Schedule(ctx, c.Index(index), member, at); err != nil {
		c.failed("Schedule", c.Index(index), err)
	}
}

// Unschedule takes member out of the time index.
func (c *Client) Unschedule(index, member string) {
	ctx, cancel := c.context()
	defer cancel()
	if err := c.kv.Unschedule(ctx, c.Index(index), member); err != nil {
		c.failed("Unschedule", c.Index(index), err)
	}
}

// ClaimDue claims the due members of the time index; see
// kvstore.Store.ClaimDue. Unlike the other operations it returns the
// error, since its caller is a loop of the adapter's user, not a method
// of the role's Store interface.
func (c *Client) ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]kvstore.Claim, error) {
	return c.kv.ClaimDue(ctx, c.Index(index), now, lease, limit)
}

// Done ends a claim that ClaimDue gave.
func (c *Client) Done(ctx context.Context, claim kvstore.Claim) error {
	return c.kv.Done(ctx, claim)
}
