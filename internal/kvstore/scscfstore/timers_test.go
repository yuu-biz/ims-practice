package scscfstore_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
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

func registration(expires time.Time) scscf.Registration {
	return scscf.Registration{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Contact: "sip:ue1@192.0.2.10", Expires: expires}
}

// TestRegistrationExpiryIsClaimedOnce has both instances look for due
// registrations at once: one of them gets the registration.
func TestRegistrationExpiryIsClaimedOnce(t *testing.T) {
	ctx := context.Background()
	a, b, _, _ := twoInstances()
	a.PutRegistration(registration(start.Add(time.Hour)))
	if claims, _ := b.ClaimDue(ctx, scscfstore.RegistrationTimer, start, time.Minute, 10); len(claims) != 0 {
		t.Fatalf("claimed %v before the registration expired", claims)
	}
	var mu sync.Mutex
	var claims []kvstore.Claim
	var wg sync.WaitGroup
	for _, s := range []*scscfstore.Store{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.ClaimDue(ctx, scscfstore.RegistrationTimer, start.Add(time.Hour), time.Minute, 10)
			if err != nil {
				t.Error(err)
			}
			mu.Lock()
			defer mu.Unlock()
			claims = append(claims, got...)
		}()
	}
	wg.Wait()
	if len(claims) != 1 || claims[0].Member != impi {
		t.Fatalf("claims = %v, want the registration once", claims)
	}
}

// TestRefreshMovesTheExpiry: a registration refreshed while an instance
// holds the claim of its old expiry keeps its new one.
func TestRefreshMovesTheExpiry(t *testing.T) {
	ctx := context.Background()
	a, b, _, _ := twoInstances()
	first, second := start.Add(time.Hour), start.Add(2*time.Hour)
	a.PutRegistration(registration(first))
	claims, _ := a.ClaimDue(ctx, scscfstore.RegistrationTimer, first, time.Minute, 10)
	b.PutRegistration(registration(second))
	if len(claims) != 1 {
		t.Fatalf("claims = %v", claims)
	}
	// The claimer reads the registration, as scscf.registrationExpired
	// does, and finds that it has not expired.
	if reg, _ := a.Registration(claims[0].Member); !reg.Expires.Equal(second) {
		t.Errorf("Expires = %v, want %v", reg.Expires, second)
	}
	if err := a.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Errorf("Done after the refresh = %v, want ErrClaimLost", err)
	}
	if claims, _ := b.ClaimDue(ctx, scscfstore.RegistrationTimer, first.Add(2*time.Minute), time.Minute, 10); len(claims) != 0 {
		t.Errorf("claimed %v at the old expiry", claims)
	}
	if claims, _ := b.ClaimDue(ctx, scscfstore.RegistrationTimer, second, time.Minute, 10); len(claims) != 1 {
		t.Errorf("claims at the new expiry = %v", claims)
	}
}

// TestRegAwaitAuthNamesTheChallenge: the timer of a challenge that the
// UE answered is gone, and that of a replaced challenge names the old
// nonce, so that it ends nothing.
func TestRegAwaitAuthNamesTheChallenge(t *testing.T) {
	ctx := context.Background()
	a, b, _, _ := twoInstances()
	a.PutChallenge(challenge("n1"))
	b.DeleteChallenge(impi)
	if claims, _ := a.ClaimDue(ctx, scscfstore.RegAwaitAuthTimer, start.Add(time.Minute), time.Minute, 10); len(claims) != 0 {
		t.Errorf("an answered challenge still has its timer: %v", claims)
	}
	a.PutChallenge(challenge("n2"))
	b.PutChallenge(challenge("n3"))
	claims, _ := a.ClaimDue(ctx, scscfstore.RegAwaitAuthTimer, start.Add(time.Minute), time.Minute, 10)
	current, _ := a.Challenge(impi)
	ended := 0
	for _, c := range claims {
		privateID, nonce := scscfstore.ParseChallengeMember(c.Member)
		if privateID != impi {
			t.Errorf("member %q names %q", c.Member, privateID)
		}
		if nonce == current.Nonce {
			ended++
		}
	}
	if len(claims) != 2 || ended != 1 {
		t.Errorf("claims = %v, of which %d name the current challenge; want 2 and 1", claims, ended)
	}
}

// TestAChallengeEndsAtTheTimesItCarries: the reg-await-auth timer of a
// challenge is due at its Expires, and the Store forgets the challenge
// at its KeepUntil, whatever the times are.
func TestAChallengeEndsAtTheTimesItCarries(t *testing.T) {
	ctx := context.Background()
	a, b, _, c := twoInstances()
	ch := scscf.Challenge{PrivateIdentity: impi, Nonce: "n1", Expires: start.Add(30 * time.Second), KeepUntil: start.Add(90 * time.Second)}
	a.PutChallenge(ch)
	if claims, _ := b.ClaimDue(ctx, scscfstore.RegAwaitAuthTimer, ch.Expires.Add(-time.Millisecond), time.Minute, 10); len(claims) != 0 {
		t.Errorf("claimed %v before the Expires of the challenge", claims)
	}
	if claims, _ := b.ClaimDue(ctx, scscfstore.RegAwaitAuthTimer, ch.Expires, time.Minute, 10); len(claims) != 1 {
		t.Errorf("claims at the Expires of the challenge = %v, want one", claims)
	}
	c.set(ch.KeepUntil.Add(-time.Millisecond))
	if _, ok := b.Challenge(impi); !ok {
		t.Error("the challenge is gone before its KeepUntil")
	}
	c.set(ch.KeepUntil)
	if _, ok := b.Challenge(impi); ok {
		t.Error("the challenge is still there at its KeepUntil")
	}
}

// TestAWriterThatStopsLeavesOnlyAMiss stops an instance after it has
// written the index items of a new registration, and before the
// registration itself.
func TestAWriterThatStopsLeavesOnlyAMiss(t *testing.T) {
	_, b, kv, c := twoInstances()
	stopped := scscfstore.New(storetest.StopAfter(kv, 2), kvjson.Config{Now: c.Now})
	stopped.PutRegistration(registration(start.Add(time.Hour)))
	if _, ok := b.RegistrationOf(tel); ok {
		t.Error("RegistrationOf found a registration that was never written")
	}
	b.PutRegistration(registration(start.Add(time.Hour)))
	if _, ok := b.RegistrationOf(tel); !ok {
		t.Error("RegistrationOf after the next registration")
	}
	// The next registration took over the index items of the stopped
	// instance, so its end removes them.
	b.DeleteRegistration(impi)
	for _, key := range []string{"scscf:impu:tel%3A+12025550101", "scscf:impu:" + impu[:3] + "%3A" + impu[4:]} {
		if _, err := kv.Get(context.Background(), key); !errors.Is(err, kvstore.ErrNotFound) {
			t.Errorf("Get(%s) = %v after the registration, want ErrNotFound", key, err)
		}
	}
}

// TestSubscriptionExpiry: the member of a subscription names its dialog.
func TestSubscriptionExpiry(t *testing.T) {
	ctx := context.Background()
	a, b, _, _ := twoInstances()
	var sub scscf.Subscription
	sub.PrivateIdentity = impi
	sub.Dialog.ID.CallID, sub.Dialog.ID.LocalTag, sub.Dialog.ID.RemoteTag = "sub:1@192.0.2.10", "l1", "r1"
	sub.Expires = start.Add(10 * time.Minute)
	a.PutSubscription(sub)
	claims, _ := b.ClaimDue(ctx, scscfstore.SubscriptionTimer, sub.Expires, time.Minute, 10)
	if len(claims) != 1 {
		t.Fatalf("claims = %v", claims)
	}
	id, ok := scscfstore.ParseSubscriptionMember(claims[0].Member)
	if !ok || id != sub.Dialog.ID {
		t.Fatalf("member %q gives %v, %v", claims[0].Member, id, ok)
	}
	b.DeleteSubscription(id)
	if err := b.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		// DeleteSubscription takes the member out of the index.
		t.Errorf("Done after DeleteSubscription = %v, want ErrClaimLost", err)
	}
	if got := a.SubscriptionsOf(impi); len(got) != 0 {
		t.Errorf("SubscriptionsOf = %v", got)
	}
}
