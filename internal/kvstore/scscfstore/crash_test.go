package scscfstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scscfstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. The instance is told of the failure,
// never that an item is missing, so that the S-CSCF can answer it; once
// the main item is written or removed, what is left is a miss.

func subscription(expires time.Time) scscf.Subscription {
	var sub scscf.Subscription
	sub.PrivateIdentity, sub.Expires = impi, expires
	sub.Dialog.ID = sip.DialogID{CallID: "sub:1@192.0.2.10", LocalTag: "l1", RemoteTag: "r1"}
	return sub
}

func unregisteredProfile(expires time.Time) scscf.UnregisteredProfile {
	return scscf.UnregisteredProfile{PrivateIdentity: impi, PublicIdentities: []string{impu, tel}, Expires: expires}
}

// TestAStoppedInstanceIsToldOfTheFailure: every operation of an instance
// whose store has stopped returns the failure, and what it failed to
// change is as it was for the other instances.
func TestAStoppedInstanceIsToldOfTheFailure(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := scscfstore.New(kv, cfg)
			expires := time.Now().Add(time.Hour)
			ch := scscf.Challenge{PrivateIdentity: impi, Nonce: "n1", Expires: expires, KeepUntil: expires.Add(time.Minute)}
			reg, sub := registration(expires), subscription(expires)
			od := scscf.OriginalDialog{ID: "od-1", ServedUser: impu, Expires: expires}
			p := unregisteredProfile(expires)
			p.PrivateIdentity = "001010000000002@ims.mnc001.mcc001.3gppnetwork.org"
			p.PublicIdentities = []string{"sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org"}
			must(t, other.PutChallenge(ctx, ch))
			must(t, other.PutRegistration(ctx, reg))
			must(t, other.PutSubscription(ctx, sub))
			must(t, other.PutOriginalDialog(ctx, od))
			must(t, other.PutUnregisteredProfile(ctx, p))
			// The first write stops the instance, and every operation fails
			// from then on.
			stopped := scscfstore.New(storetest.StopAfter(kv, 0), cfg)

			if err := stopped.PutChallenge(ctx, scscf.Challenge{PrivateIdentity: impi, Nonce: "n2", Expires: expires}); err == nil {
				t.Error("PutChallenge returned no error")
			}
			if err := stopped.DeleteChallenge(ctx, impi); err == nil {
				t.Error("DeleteChallenge returned no error")
			}
			if _, ok, err := stopped.Challenge(ctx, impi); err == nil {
				t.Errorf("Challenge = %v, no error", ok)
			}
			if err := stopped.PutRegistration(ctx, registration(expires.Add(time.Hour))); err == nil {
				t.Error("PutRegistration returned no error")
			}
			if err := stopped.DeleteRegistration(ctx, impi); err == nil {
				t.Error("DeleteRegistration returned no error")
			}
			if _, ok, err := stopped.Registration(ctx, impi); err == nil {
				t.Errorf("Registration = %v, no error", ok)
			}
			if _, ok, err := stopped.RegistrationOf(ctx, tel); err == nil {
				t.Errorf("RegistrationOf = %v, no error", ok)
			}
			if err := stopped.PutSubscription(ctx, subscription(expires.Add(time.Hour))); err == nil {
				t.Error("PutSubscription returned no error")
			}
			if err := stopped.DeleteSubscription(ctx, sub.Dialog.ID); err == nil {
				t.Error("DeleteSubscription returned no error")
			}
			if _, ok, err := stopped.Subscription(ctx, sub.Dialog.ID); err == nil {
				t.Errorf("Subscription = %v, no error", ok)
			}
			if subs, err := stopped.SubscriptionsOf(ctx, impi); err == nil {
				t.Errorf("SubscriptionsOf = %v, no error", subs)
			}
			if err := stopped.PutOriginalDialog(ctx, scscf.OriginalDialog{ID: "od-1", Returned: true, Expires: expires}); err == nil {
				t.Error("PutOriginalDialog returned no error")
			}
			if _, ok, err := stopped.OriginalDialog(ctx, od.ID); err == nil {
				t.Errorf("OriginalDialog = %v, no error", ok)
			}
			if err := stopped.PutUnregisteredProfile(ctx, unregisteredProfile(expires)); err == nil {
				t.Error("PutUnregisteredProfile returned no error")
			}
			if _, ok, err := stopped.UnregisteredProfileOf(ctx, p.PublicIdentities[0]); err == nil {
				t.Errorf("UnregisteredProfileOf = %v, no error", ok)
			}
			if deleted, err := stopped.DeleteUnregisteredProfile(ctx, p.PrivateIdentity, p.Expires); err == nil {
				t.Errorf("DeleteUnregisteredProfile = %v, no error", deleted)
			}

			if got, ok, err := other.Challenge(ctx, impi); err != nil || !ok || got.Nonce != ch.Nonce {
				t.Errorf("Challenge on the other instance = %+v, %v, %v; want the challenge as it was", got, ok, err)
			}
			if got, ok, err := other.RegistrationOf(ctx, tel); err != nil || !ok || !got.Expires.Equal(reg.Expires) {
				t.Errorf("RegistrationOf on the other instance = %+v, %v, %v; want the registration as it was", got, ok, err)
			}
			if got, err := other.SubscriptionsOf(ctx, impi); err != nil || len(got) != 1 || !got[0].Expires.Equal(sub.Expires) {
				t.Errorf("SubscriptionsOf on the other instance = %+v, %v; want the subscription as it was", got, err)
			}
			if got, ok, err := other.OriginalDialog(ctx, od.ID); err != nil || !ok || got.Returned {
				t.Errorf("OriginalDialog on the other instance = %+v, %v, %v; want the identifier as it was", got, ok, err)
			}
			if _, ok, err := other.UnregisteredProfileOf(ctx, p.PublicIdentities[0]); err != nil || !ok {
				t.Errorf("UnregisteredProfileOf on the other instance = %v, %v; want the profile", ok, err)
			}
		})
	}
}

// TestAWriterThatStopsLeavesOnlyAMiss stops an instance after it has
// written the index items of a new registration, and before the
// registration itself. It is told so, and no identity finds the
// registration.
func TestAWriterThatStopsLeavesOnlyAMiss(t *testing.T) {
	ctx := context.Background()
	_, b, kv, c := twoInstances()
	stopped := scscfstore.New(storetest.StopAfter(kv, 2), kvjson.Config{Now: c.Now})
	if err := stopped.PutRegistration(ctx, registration(start.Add(time.Hour))); err == nil {
		t.Fatal("PutRegistration that stopped half way returned no error")
	}
	if _, ok, err := b.RegistrationOf(ctx, tel); err != nil || ok {
		t.Errorf("RegistrationOf of a registration that was never written = %v, %v; want none", ok, err)
	}
	must(t, b.PutRegistration(ctx, registration(start.Add(time.Hour))))
	if _, ok, err := b.RegistrationOf(ctx, tel); err != nil || !ok {
		t.Errorf("RegistrationOf after the next registration = %v, %v", ok, err)
	}
	// The next registration took over the index items of the stopped
	// instance, so its end removes them.
	must(t, b.DeleteRegistration(ctx, impi))
	for _, key := range []string{"scscf:impu:tel%3A+12025550101", "scscf:impu:" + impu[:3] + "%3A" + impu[4:]} {
		if _, err := kv.Get(ctx, key); !errors.Is(err, kvstore.ErrNotFound) {
			t.Errorf("Get(%s) = %v after the registration, want ErrNotFound", key, err)
		}
	}
}

// TestADeleteThatStopsLeavesOnlyAMiss stops an instance after it has
// removed a main item, before its index items, its entry in a set or
// its timer. The item is gone, so the instance is told that the delete
// succeeded, and what stays finds nothing.
func TestADeleteThatStopsLeavesOnlyAMiss(t *testing.T) {
	ctx := context.Background()
	a, b, kv, c := twoInstances()
	expires := start.Add(time.Hour)
	must(t, a.PutRegistration(ctx, registration(expires)))
	must(t, a.PutSubscription(ctx, subscription(expires)))
	must(t, a.PutUnregisteredProfile(ctx, scscf.UnregisteredProfile{PrivateIdentity: "001010000000002@ims.mnc001.mcc001.3gppnetwork.org",
		PublicIdentities: []string{"tel:+12025550102"}, Expires: expires}))
	stopping := func() *scscfstore.Store {
		return scscfstore.New(storetest.StopAfter(kv, 1), kvjson.Config{Now: c.Now})
	}

	if err := stopping().DeleteRegistration(ctx, impi); err != nil {
		t.Errorf("DeleteRegistration that removed the registration = %v", err)
	}
	if _, ok, err := b.RegistrationOf(ctx, tel); err != nil || ok {
		t.Errorf("RegistrationOf after the delete = %v, %v; want none", ok, err)
	}
	if err := stopping().DeleteSubscription(ctx, subscription(expires).Dialog.ID); err != nil {
		t.Errorf("DeleteSubscription that removed the subscription = %v", err)
	}
	if got, err := b.SubscriptionsOf(ctx, impi); err != nil || len(got) != 0 {
		t.Errorf("SubscriptionsOf after the delete = %v, %v; want none", got, err)
	}
	if claims, err := b.ClaimDue(ctx, scscf.SubscriptionExpiry, expires, time.Minute, 10); err != nil || len(claims) != 1 {
		t.Fatalf("claims of the subscription expiries = %v, %v; want the one left behind", claims, err)
	}
	if _, ok, err := b.Subscription(ctx, subscription(expires).Dialog.ID); err != nil || ok {
		t.Errorf("the claimer of the expiry left behind finds a subscription: %v, %v", ok, err)
	}
	deleted, err := stopping().DeleteUnregisteredProfile(ctx, "001010000000002@ims.mnc001.mcc001.3gppnetwork.org", expires)
	if err != nil || !deleted {
		t.Errorf("DeleteUnregisteredProfile that removed the profile = %v, %v; want true", deleted, err)
	}
	if _, ok, err := b.UnregisteredProfileOf(ctx, "tel:+12025550102"); err != nil || ok {
		t.Errorf("UnregisteredProfileOf after the delete = %v, %v; want none", ok, err)
	}
}
