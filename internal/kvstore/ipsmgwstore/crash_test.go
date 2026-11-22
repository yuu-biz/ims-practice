package ipsmgwstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests in this file stop an instance before one of its writes,
// with storetest.StopAfter, and look at what that instance is told and
// what the other instances find. The instance is told of the failure,
// never that an item is missing, so that the IP-SM-GW can answer it.

const (
	recipient = "tel:+12025550102"
	alice     = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org"
)

func pending(callID string) ipsmgw.PendingDelivery {
	now := time.Now()
	return ipsmgw.PendingDelivery{CallID: callID, Reference: "ref-" + callID, Recipient: recipient, MessageReference: 3,
		ReportDeadline: now.Add(time.Minute), Expires: now.Add(2 * time.Minute)}
}

func subscription(callID string) ipsmgw.RegSubscription {
	return ipsmgw.RegSubscription{PublicIdentity: alice,
		Dialog: sip.Dialog{ID: sip.DialogID{CallID: callID, LocalTag: "l"}, LocalSeq: 1}, Expires: time.Now().Add(time.Hour)}
}

// TestAStoppedInstanceIsToldOfTheFailure: every operation of an instance
// whose store has stopped returns the failure.
func TestAStoppedInstanceIsToldOfTheFailure(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := ipsmgwstore.New(kv, cfg)
			if err := other.PutDelivery(ctx, pending("msg-1")); err != nil {
				t.Fatal(err)
			}
			if err := other.PutRegSubscription(ctx, subscription("sub-1")); err != nil {
				t.Fatal(err)
			}
			stopped := ipsmgwstore.New(storetest.StopAfter(kv, 0), cfg)

			if _, err := stopped.NextMessageReference(ctx, recipient, time.Now().Add(time.Hour)); err == nil {
				t.Error("NextMessageReference returned no error")
			}
			if err := stopped.PutDelivery(ctx, pending("msg-2")); err == nil {
				t.Error("PutDelivery returned no error")
			}
			if _, ok, err := stopped.Delivery(ctx, "msg-1"); err == nil {
				t.Errorf("Delivery = %v, no error", ok)
			}
			if _, ok, err := stopped.TakeDelivery(ctx, "msg-1"); err == nil {
				t.Errorf("TakeDelivery = %v, no error", ok)
			}
			if err := stopped.PutRegSubscription(ctx, subscription("sub-2")); err == nil {
				t.Error("PutRegSubscription returned no error")
			}
			if _, ok, err := stopped.RegSubscription(ctx, "sub-1"); err == nil {
				t.Errorf("RegSubscription = %v, no error", ok)
			}
			if _, ok, err := stopped.RegSubscriptionOf(ctx, alice); err == nil {
				t.Errorf("RegSubscriptionOf = %v, no error", ok)
			}
			if err := stopped.DeleteRegSubscription(ctx, "sub-1"); err == nil {
				t.Error("DeleteRegSubscription returned no error")
			}

			// What the stopped instance failed to change is as it was.
			if mr, err := other.NextMessageReference(ctx, recipient, time.Now().Add(time.Hour)); err != nil || mr != 0 {
				t.Errorf("first RP-MR on the other instance = %d, %v; want 0", mr, err)
			}
			if _, ok, err := other.TakeDelivery(ctx, "msg-1"); err != nil || !ok {
				t.Errorf("TakeDelivery on the other instance = %v, %v; want the delivery", ok, err)
			}
			if got, ok, err := other.RegSubscriptionOf(ctx, alice); err != nil || !ok || got.Dialog.ID.CallID != "sub-1" {
				t.Errorf("RegSubscriptionOf on the other instance = %+v, %v, %v; want sub-1", got, ok, err)
			}
		})
	}
}

// TestADeliveryThatStopsHalfWayLeavesOnlyItsTimer: an instance that
// stops after it scheduled TR1N, before it wrote the delivery, is told
// so, and sends no MESSAGE. The instance that claims the timer finds no
// delivery to end.
func TestADeliveryThatStopsHalfWayLeavesOnlyItsTimer(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			d := pending("msg-1")
			if err := ipsmgwstore.New(storetest.StopAfter(kv, 1), cfg).PutDelivery(ctx, d); err == nil {
				t.Fatal("PutDelivery that stopped half way returned no error")
			}
			other := ipsmgwstore.New(kv, cfg)
			if _, ok, err := other.Delivery(ctx, d.CallID); err != nil || ok {
				t.Fatalf("Delivery on the other instance = %v, %v; want none", ok, err)
			}
			claims, err := other.ClaimDue(ctx, d.ReportDeadline, 10*time.Second, 10)
			if err != nil || len(claims) != 1 || claims[0].Member != d.CallID {
				t.Fatalf("ClaimDue at the ReportDeadline = %v, %v; want the timer of %s", claims, err, d.CallID)
			}
			if _, ok, err := other.TakeDelivery(ctx, claims[0].Member); err != nil || ok {
				t.Errorf("the claimer took %v, %v; want no delivery", ok, err)
			}
			if err := other.Done(ctx, claims[0]); err != nil {
				t.Error(err)
			}
		})
	}
}

// TestASubscriptionThatStopsHalfWayIsNotFound: an instance that stops
// after it wrote the index item of the public identity, before the
// subscription, is told so. The index item then names a subscription
// that is not there, which the other instances take for none, until the
// next subscription to the identity takes its place.
func TestASubscriptionThatStopsHalfWayIsNotFound(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			if err := ipsmgwstore.New(storetest.StopAfter(kv, 1), cfg).PutRegSubscription(ctx, subscription("sub-1")); err == nil {
				t.Fatal("PutRegSubscription that stopped half way returned no error")
			}
			other := ipsmgwstore.New(kv, cfg)
			if got, ok, err := other.RegSubscriptionOf(ctx, alice); err != nil || ok {
				t.Fatalf("RegSubscriptionOf on the other instance = %+v, %v, %v; want none", got, ok, err)
			}
			if err := other.PutRegSubscription(ctx, subscription("sub-2")); err != nil {
				t.Fatal(err)
			}
			if got, ok, err := other.RegSubscriptionOf(ctx, alice); err != nil || !ok || got.Dialog.ID.CallID != "sub-2" {
				t.Errorf("RegSubscriptionOf after a new subscription = %+v, %v, %v; want sub-2", got, ok, err)
			}
		})
	}
}
