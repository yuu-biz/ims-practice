package scscfstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/scscf"
)

const (
	impi = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	impu = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
	tel  = "tel:+12025550101"
)

var start = time.UnixMilli(1_790_000_000_000)

// clock is a clock that a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// twoInstances are two S-CSCF instances on one kvstore.MemoryStore, with
// a clock that the test moves.
func twoInstances() (a, b *scscfstore.Store, kv *kvstore.MemoryStore, c *clock) {
	c = &clock{now: start}
	kv = kvstore.NewMemoryStore(c.Now)
	cfg := kvjson.Config{Now: c.Now}
	return scscfstore.New(kv, cfg), scscfstore.New(kv, cfg), kv, c
}

// challenge is a challenge whose reg-await-auth runs out a minute after
// start, and that the Store keeps for a minute more, as the S-CSCF
// writes it with a reg-await-auth of a minute.
func challenge(nonce string) scscf.Challenge {
	return scscf.Challenge{PrivateIdentity: impi, Nonce: nonce, Expires: start.Add(time.Minute), KeepUntil: start.Add(2 * time.Minute)}
}

// must fails the test on a failure of the store, which the stores of
// these tests do not have.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func registration(expires time.Time) scscf.Registration {
	return scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Contact: "sip:ue1@192.0.2.10", Expires: expires}
}

// TestAChallengeEndsAtTheTimesItCarries: the reg-await-auth of a
// challenge is due at its Expires, and the Store forgets the challenge
// at its KeepUntil, whatever the times are.
func TestAChallengeEndsAtTheTimesItCarries(t *testing.T) {
	ctx := context.Background()
	a, b, _, c := twoInstances()
	ch := scscf.Challenge{PrivateIdentity: impi, Nonce: "n1", Expires: start.Add(30 * time.Second), KeepUntil: start.Add(90 * time.Second)}
	must(t, a.PutChallenge(ctx, ch))
	if claims, _ := b.ClaimDue(ctx, scscf.RegAwaitAuthExpiry, ch.Expires.Add(-time.Millisecond), time.Minute, 10); len(claims) != 0 {
		t.Errorf("claimed %v before the Expires of the challenge", claims)
	}
	if claims, _ := b.ClaimDue(ctx, scscf.RegAwaitAuthExpiry, ch.Expires, time.Minute, 10); len(claims) != 1 {
		t.Errorf("claims at the Expires of the challenge = %v, want one", claims)
	}
	c.set(ch.KeepUntil.Add(-time.Millisecond))
	if _, ok, err := b.Challenge(ctx, impi); err != nil || !ok {
		t.Error("the challenge is gone before its KeepUntil")
	}
	c.set(ch.KeepUntil)
	if _, ok, err := b.Challenge(ctx, impi); err != nil || ok {
		t.Error("the challenge is still there at its KeepUntil")
	}
}
