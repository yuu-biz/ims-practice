package scstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/scstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/sc"
)

// TestAStoppedInstanceIsToldOfTheFailure: an instance whose store has
// stopped learns that it could not keep or take a request, never that
// there is none. The request that it could not take is still there for
// another instance.
func TestAStoppedInstanceIsToldOfTheFailure(t *testing.T) {
	ctx := context.Background()
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			kv, cfg := backend.New(t), kvjson.Config{Prefix: storetest.Prefix()}
			other := scstore.New(kv, cfg)
			r := sc.StatusReportRequest{Reference: "ref-1", Expires: time.Now().Add(time.Hour)}
			if err := other.PutStatusReportRequest(ctx, r); err != nil {
				t.Fatal(err)
			}
			stopped := scstore.New(storetest.StopAfter(kv, 0), cfg)
			if err := stopped.PutStatusReportRequest(ctx, sc.StatusReportRequest{Reference: "ref-2", Expires: r.Expires}); err == nil {
				t.Error("PutStatusReportRequest returned no error")
			}
			if _, ok, err := stopped.TakeStatusReportRequest(ctx, r.Reference); err == nil {
				t.Errorf("TakeStatusReportRequest = %v, no error", ok)
			}
			if _, ok, err := other.TakeStatusReportRequest(ctx, r.Reference); err != nil || !ok {
				t.Errorf("TakeStatusReportRequest on the other instance = %v, %v; want the request", ok, err)
			}
			if _, ok, err := other.TakeStatusReportRequest(ctx, "ref-2"); err != nil || ok {
				t.Errorf("the stopped instance left ref-2: %v, %v", ok, err)
			}
		})
	}
}
