// Package kvstore is a store of keyed items outside the nodes, so that
// the state of a node does not die with its process and several
// instances of a role can share it: a node that is replaced after a
// failure goes on with the registrations and calls of the old one.
//
// The operations are those that the Store interfaces of the roles need,
// and no more:
//
//   - Get, Put and Delete of one item by its key, with a time to live
//     for the items that only have to go away when they expire.
//   - CompareAndSwap and CompareAndDelete, so that several instances
//     can change the same item without losing a change: an update that
//     reads, changes and writes back, or a "take" that only one of them
//     wins.
//   - A time index with claims, so that when something expires that
//     needs an action (the end of a registration, a timer such as TR1N),
//     exactly one instance acts on it, and another takes over if that
//     one stops.
//
// Every operation touches one key, or the front of one time index.
// None walks all keys, which could be millions: a lookup by another
// identity is an item
// of its own whose value is the key of the main item.
//
// MemoryStore keeps the items in the process; Valkey keeps them in a
// Valkey server, through a small client of its protocol written with
// the standard library only.
package kvstore

import (
	"context"
	"errors"
	"time"
)

// Item is a value as stored, with the version of the write that stored
// it.
type Item struct {
	Value []byte
	// Version names the write. Every write gives a new one, chosen at
	// random, so a version never comes back once it is gone, even if
	// the item is deleted and written again.
	Version string
}

var (
	// ErrNotFound: there is no item under the key, or it has expired.
	ErrNotFound = errors.New("kvstore: no such item")
	// ErrConflict: the item is no longer at the version that the caller
	// gave. Someone else changed it; read it again and decide anew.
	ErrConflict = errors.New("kvstore: the item has changed")
	// ErrClaimLost: the claim has ended, by a Schedule or Unschedule of
	// its member, or because it ran out and another claim took over.
	ErrClaimLost = errors.New("kvstore: the claim is no longer in force")
	// ErrShortLease: a lease must last at least a millisecond, the
	// resolution of a time index; a shorter one would end before it
	// began.
	ErrShortLease = errors.New("kvstore: a lease must last at least a millisecond")
)

// Store is the store. A ttl of zero keeps an item until it is deleted;
// any other ttl removes it that long after the write.
type Store interface {
	// Get returns the item under key, or ErrNotFound.
	Get(ctx context.Context, key string) (Item, error)
	// Put writes value under key whatever is there, and returns the new
	// version.
	Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error)
	// CompareAndSwap writes value under key only if the item is still at
	// version, or, with version "", only if there is no item. It returns
	// the new version, or ErrConflict.
	CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error)
	// Delete removes the item under key, if there is one.
	Delete(ctx context.Context, key string) error
	// CompareAndDelete removes the item under key only if it is still at
	// version, or returns ErrConflict. Of several callers that read the
	// same version, only one removes the item: a "take" is a Get and a
	// CompareAndDelete.
	CompareAndDelete(ctx context.Context, key, version string) error

	// Schedule puts member into the time index named index, due at at.
	// If member is there already it moves, and a claim on it ends.
	Schedule(ctx context.Context, index, member string, at time.Time) error
	// Unschedule removes member from the index, claimed or not.
	Unschedule(ctx context.Context, index, member string) error
	// ClaimDue claims at most limit members of the index that are due at
	// now, earliest first, each until now plus lease. While a member is
	// claimed it is not due, and no other ClaimDue gets it; when the
	// claim runs out without Done, the member is due again, for any
	// instance that asks.
	ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]Claim, error)
	// Done removes the member of a claim from its index, if the claim is
	// still in force, or returns ErrClaimLost.
	Done(ctx context.Context, c Claim) error
}

// Claim is the right of one caller to act on one member of a time index
// until Until.
type Claim struct {
	Index  string
	Member string
	// Due is when the member was due.
	Due time.Time
	// Until is when the claim runs out. It also tells this claim from a
	// later one on the same member.
	Until time.Time
}

// Times in a time index count whole milliseconds since the Unix epoch.
// That is the resolution of the score of a sorted set that holds them
// exactly (a double has 53 bits), and the same in every Store.
func indexTime(t time.Time) int64 { return t.UnixMilli() }

func fromIndexTime(ms int64) time.Time { return time.UnixMilli(ms) }
