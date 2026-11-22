package kvstore

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Valkey is a Store in a Valkey server (https://valkey.io), or any
// server that speaks the protocol of Redis 6.2 or later.
//
// An item is a hash with two fields, value and version, so that
// valkey-cli HGETALL shows both. A time index is a sorted set whose
// score is the due time in milliseconds; a claim moves the member's
// score to the end of the lease. Every operation that has to check
// before it writes is a Lua script, which the server runs as one step.
// Each script names one key, so that in a Valkey cluster it runs on the
// node that holds the key.
type Valkey struct {
	c *respClient
}

// ValkeyConfig names the server and how long to wait for it.
type ValkeyConfig struct {
	// Address is host:port, by default port 6379.
	Address string
	// Timeout bounds each operation; zero means a second.
	Timeout time.Duration
	// MaxIdle is how many idle connections are kept; zero means 8.
	MaxIdle int
}

// NewValkey returns a Store in the server of cfg. It connects when the
// first operation needs a connection.
//
// SIMPLIFIED: the server is reached in plain text without AUTH, on one
// address. TLS and AUTH (with the users and ACLs of Valkey), and the
// discovery of a replica that takes over (Sentinel, cluster), are left
// out: the store sits in the private network of the nodes, and like
// Diameter and SIP it is not encrypted by default, so that captures can
// be read. The store is one server and so a single point of failure;
// what this project shows is the redundancy of the IMS nodes, not that
// of the store.
func NewValkey(cfg ValkeyConfig) *Valkey {
	if cfg.Timeout == 0 {
		cfg.Timeout = time.Second
	}
	if cfg.MaxIdle == 0 {
		cfg.MaxIdle = 8
	}
	return &Valkey{c: newRESPClient(cfg.Address, cfg.Timeout, cfg.MaxIdle)}
}

// Close closes the idle connections. Operations after Close open new
// ones.
func (v *Valkey) Close() { v.c.closeIdle() }

var _ Store = (*Valkey)(nil)

// The scripts. KEYS[1] is the key of the item or of the index. Valkey
// runs a script without anything else in between (EVAL), so a check
// and the write that it allows are one step.
const (
	// writeScript writes ARGV[2] at version ARGV[3] with a time to live
	// of ARGV[4] milliseconds, 0 for none. With ARGV[1] = "check" it
	// writes only if the version is still ARGV[5], "" for no item, and
	// returns 0 otherwise.
	writeScript = `
if ARGV[1] == 'check' then
  local current = redis.call('HGET', KEYS[1], 'version') or ''
  if current ~= ARGV[5] then return 0 end
end
redis.call('HSET', KEYS[1], 'value', ARGV[2], 'version', ARGV[3])
if ARGV[4] == '0' then
  redis.call('PERSIST', KEYS[1])
else
  redis.call('PEXPIRE', KEYS[1], ARGV[4])
end
return 1`

	// compareAndDeleteScript removes the item if it is at version ARGV[1].
	compareAndDeleteScript = `
if redis.call('HGET', KEYS[1], 'version') == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0`

	// claimScript claims at most ARGV[3] members due at ARGV[1], until
	// ARGV[2], and returns them with their due times.
	claimScript = `
local due = redis.call('ZRANGE', KEYS[1], '-inf', ARGV[1], 'BYSCORE', 'LIMIT', 0, ARGV[3], 'WITHSCORES')
for i = 1, #due, 2 do
  redis.call('ZADD', KEYS[1], ARGV[2], due[i])
end
return due`

	// doneScript removes member ARGV[1] if its claim still ends at
	// ARGV[2].
	doneScript = `
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if score and tonumber(score) == tonumber(ARGV[2]) then
  return redis.call('ZREM', KEYS[1], ARGV[1])
end
return 0`
)

func (v *Valkey) Get(ctx context.Context, key string) (Item, error) {
	reply, err := v.c.do(ctx, "HMGET", key, "value", "version")
	if err != nil {
		return Item{}, err
	}
	fields, ok := reply.([]any)
	if !ok || len(fields) != 2 {
		return Item{}, unexpected("HMGET", reply)
	}
	value, okValue := fields[0].([]byte)
	version, okVersion := fields[1].([]byte)
	if !okValue || !okVersion {
		return Item{}, unexpected("HMGET", reply)
	}
	if version == nil {
		return Item{}, ErrNotFound
	}
	if value == nil {
		value = []byte{}
	}
	return Item{Value: value, Version: string(version)}, nil
}

func (v *Valkey) Put(ctx context.Context, key string, value []byte, ttl time.Duration) (string, error) {
	version := newVersion()
	if _, err := v.eval(ctx, writeScript, key, "always", string(value), version, milliseconds(ttl)); err != nil {
		return "", err
	}
	return version, nil
}

func (v *Valkey) CompareAndSwap(ctx context.Context, key, version string, value []byte, ttl time.Duration) (string, error) {
	next := newVersion()
	written, err := v.eval(ctx, writeScript, key, "check", string(value), next, milliseconds(ttl), version)
	if err != nil {
		return "", err
	}
	if written != 1 {
		return "", ErrConflict
	}
	return next, nil
}

func (v *Valkey) Delete(ctx context.Context, key string) error {
	_, err := v.c.do(ctx, "DEL", key)
	return err
}

func (v *Valkey) CompareAndDelete(ctx context.Context, key, version string) error {
	removed, err := v.eval(ctx, compareAndDeleteScript, key, version)
	if err != nil {
		return err
	}
	if removed != 1 {
		return ErrConflict
	}
	return nil
}

func (v *Valkey) Schedule(ctx context.Context, index, member string, at time.Time) error {
	_, err := v.c.do(ctx, "ZADD", index, strconv.FormatInt(indexTime(at), 10), member)
	return err
}

func (v *Valkey) Unschedule(ctx context.Context, index, member string) error {
	_, err := v.c.do(ctx, "ZREM", index, member)
	return err
}

func (v *Valkey) ClaimDue(ctx context.Context, index string, now time.Time, lease time.Duration, limit int) ([]Claim, error) {
	if lease < time.Millisecond {
		return nil, ErrShortLease
	}
	until := indexTime(now.Add(lease))
	reply, err := v.c.do(ctx, "EVAL", claimScript, "1", index,
		strconv.FormatInt(indexTime(now), 10), strconv.FormatInt(until, 10), strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	due, ok := reply.([]any)
	if !ok || len(due)%2 != 0 {
		return nil, unexpected("claim", reply)
	}
	var claims []Claim
	for i := 0; i < len(due); i += 2 {
		member, okMember := due[i].([]byte)
		score, okScore := due[i+1].([]byte)
		if !okMember || !okScore {
			return nil, unexpected("claim", reply)
		}
		ms, err := strconv.ParseFloat(string(score), 64)
		if err != nil {
			return nil, unexpected("claim", reply)
		}
		claims = append(claims, Claim{Index: index, Member: string(member), Due: fromIndexTime(int64(ms)), Until: fromIndexTime(until)})
	}
	return claims, nil
}

func (v *Valkey) Done(ctx context.Context, c Claim) error {
	removed, err := v.eval(ctx, doneScript, c.Index, c.Member, strconv.FormatInt(indexTime(c.Until), 10))
	if err != nil {
		return err
	}
	if removed != 1 {
		return ErrClaimLost
	}
	return nil
}

// A sorted index is a sorted set whose members all have score 0, so
// that the set is ordered by its members and a page of it is one
// ZRANGE BYLEX (Redis 6.2 and later).
func (v *Valkey) AddToIndex(ctx context.Context, index, member string) error {
	_, err := v.c.do(ctx, "ZADD", index, "0", member)
	return err
}

func (v *Valkey) RemoveFromIndex(ctx context.Context, index, member string) error {
	_, err := v.c.do(ctx, "ZREM", index, member)
	return err
}

func (v *Valkey) MembersAfter(ctx context.Context, index, after string, limit int) ([]string, error) {
	if limit < 1 {
		return nil, nil
	}
	// "-" is before every member and "+" after every one; "(" makes the
	// start exclusive, so after itself is not in the page.
	start := "-"
	if after != "" {
		start = "(" + after
	}
	reply, err := v.c.do(ctx, "ZRANGE", index, start, "+", "BYLEX", "LIMIT", "0", strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	values, ok := reply.([]any)
	if !ok {
		return nil, unexpected("ZRANGE", reply)
	}
	members := make([]string, 0, len(values))
	for _, value := range values {
		b, ok := value.([]byte)
		if !ok {
			return nil, unexpected("ZRANGE", reply)
		}
		members = append(members, string(b))
	}
	return members, nil
}

// eval runs a script on one key and returns its integer reply.
//
// The script goes with every call (EVAL). EVALSHA would send only its
// digest, but needs a fallback for a server that has not seen the
// script yet, such as one that has just restarted; on the short scripts
// here the saving does not pay for that.
func (v *Valkey) eval(ctx context.Context, script, key string, args ...string) (int64, error) {
	reply, err := v.c.do(ctx, append([]string{"EVAL", script, "1", key}, args...)...)
	if err != nil {
		return 0, err
	}
	n, ok := reply.(int64)
	if !ok {
		return 0, unexpected("EVAL", reply)
	}
	return n, nil
}

// milliseconds is a time to live as PEXPIRE takes it. A part of a
// millisecond counts as a whole one, so that a short time to live never
// becomes none.
func milliseconds(ttl time.Duration) string {
	if ttl <= 0 {
		return "0"
	}
	return strconv.FormatInt(int64((ttl+time.Millisecond-1)/time.Millisecond), 10)
}

var errUnexpected = errors.New("kvstore: unexpected reply")

func unexpected(command string, reply any) error {
	return fmt.Errorf("%w to %s: %v", errUnexpected, command, reply)
}
