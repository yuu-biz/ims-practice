package ipsmgwstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/ipsmgwstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
)

func TestConformance(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv := backend.New(t)
			storetest.IPSMGW(t, func(*testing.T) (ipsmgw.Store, ipsmgw.Store) {
				cfg := kvjson.Config{Prefix: storetest.Prefix()}
				return ipsmgwstore.New(kv, cfg), ipsmgwstore.New(kv, cfg)
			})
		})
	}
}

// TestReportTimeoutOfAStoppedSender: the IP-SM-GW that sent the MESSAGE
// stops, and another one ends the delivery when TR1N runs out, at the
// ReportDeadline of the delivery. The SC hears of it once, even if the
// report comes at the same moment.
func TestReportTimeoutOfAStoppedSender(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			// The clock of the adapter decides what has passed its
			// Expires, and ClaimDue goes by the time it is given; the
			// times to live, which the clock of the store counts, are
			// minutes long and do not run out during the test.
			now := time.Now()
			cfg := kvjson.Config{Prefix: storetest.Prefix(), Now: func() time.Time { return now }}
			kv := backend.New(t)
			sender, other := ipsmgwstore.New(kv, cfg), ipsmgwstore.New(kv, cfg)
			if err := sender.PutDelivery(ctx, ipsmgw.PendingDelivery{CallID: "msg-1", Reference: "ref-1",
				ReportDeadline: now.Add(time.Minute), Expires: now.Add(2 * time.Minute)}); err != nil {
				t.Fatal(err)
			}

			if claims, err := other.ClaimDue(ctx, now.Add(59*time.Second), 10*time.Second, 10); err != nil || len(claims) != 0 {
				t.Fatalf("claimed %v, %v before TR1N ran out", claims, err)
			}
			claims, err := other.ClaimDue(ctx, now.Add(time.Minute), 10*time.Second, 10)
			if err != nil || len(claims) != 1 || claims[0].Member != "msg-1" {
				t.Fatalf("ClaimDue = %v, %v", claims, err)
			}
			if _, ok, err := other.TakeDelivery(ctx, claims[0].Member); err != nil || !ok {
				t.Fatalf("the claimer did not get the delivery: %v, %v", ok, err)
			}
			if _, ok, err := sender.TakeDelivery(ctx, "msg-1"); err != nil || ok {
				t.Errorf("the delivery report got the delivery after the timer: %v, %v", ok, err)
			}
			// TakeDelivery took the member out of the index, which ends
			// the claim.
			if err := other.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
				t.Errorf("Done = %v, want ErrClaimLost", err)
			}
		})
	}
}
