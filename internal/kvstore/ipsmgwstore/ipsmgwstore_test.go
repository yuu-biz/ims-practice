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
	now := time.UnixMilli(1_790_000_000_000)
	clock := func() time.Time { return now }
	kv := kvstore.NewMemoryStore(clock)
	cfg := kvjson.Config{Now: clock}
	sender, other := ipsmgwstore.New(kv, cfg), ipsmgwstore.New(kv, cfg)
	sender.PutDelivery(ipsmgw.PendingDelivery{CallID: "msg-1", Reference: "ref-1",
		ReportDeadline: now.Add(time.Minute), Expires: now.Add(2 * time.Minute)})

	if claims, _ := other.ClaimDue(ctx, now.Add(59*time.Second), 10*time.Second, 10); len(claims) != 0 {
		t.Fatalf("claimed %v before TR1N ran out", claims)
	}
	now = now.Add(time.Minute)
	claims, err := other.ClaimDue(ctx, now, 10*time.Second, 10)
	if err != nil || len(claims) != 1 || claims[0].Member != "msg-1" {
		t.Fatalf("ClaimDue = %v, %v", claims, err)
	}
	if _, ok := other.TakeDelivery(claims[0].Member); !ok {
		t.Fatal("the claimer did not get the delivery")
	}
	if _, ok := sender.TakeDelivery("msg-1"); ok {
		t.Error("the delivery report got the delivery after the timer")
	}
	// TakeDelivery took the member out of the index, which ends the claim.
	if err := other.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Errorf("Done = %v, want ErrClaimLost", err)
	}
}
