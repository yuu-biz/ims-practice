package storetest

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// SC runs the suite of sc.Store.
func SC(t *testing.T, newPair func(t *testing.T) (sc.Store, sc.Store)) {
	run(t, newPair, []testCase[sc.Store]{
		{"StatusReportRequest", scStatusReportRequest},
		{"StatusReportRequestUntilItsExpires", scStatusReportRequestExpires},
		{"OnlyOneTakesARequest", scOnlyOneTakes},
	})
}

func statusReportRequest(reference string, lifetime time.Duration) sc.StatusReportRequest {
	return sc.StatusReportRequest{Reference: reference, Originator: sms.Address{Digits: "12025550101"}, MessageReference: 3,
		Recipient: sms.Address{Digits: "12025550102"}, ServiceCentreTimeStamp: time.Now().Round(time.Second),
		Expires: time.Now().Add(lifetime).Round(time.Millisecond)}
}

func scStatusReportRequest(t *testing.T, a, b sc.Store) {
	r := statusReportRequest("ref:1", time.Hour)
	a.PutStatusReportRequest(r)
	if got, ok := b.TakeStatusReportRequest(r.Reference); !ok || !sameJSON(t, got, r) {
		t.Fatalf("TakeStatusReportRequest = %+v, %v; want %+v", got, ok, r)
	}
	if _, ok := a.TakeStatusReportRequest(r.Reference); ok {
		t.Error("a second TakeStatusReportRequest got the request")
	}
}

func scStatusReportRequestExpires(t *testing.T, a, b sc.Store) {
	r := statusReportRequest("ref-1", short)
	a.PutStatusReportRequest(r)
	waitOut()
	if _, ok := b.TakeStatusReportRequest(r.Reference); ok {
		t.Error("TakeStatusReportRequest after its Expires")
	}
}

func scOnlyOneTakes(t *testing.T, a, b sc.Store) {
	for range 10 {
		r := statusReportRequest("ref-1", time.Hour)
		a.PutStatusReportRequest(r)
		var takes atomic.Int32
		parallel(4, func(i int) {
			s := a
			if i%2 == 1 {
				s = b
			}
			if _, ok := s.TakeStatusReportRequest(r.Reference); ok {
				takes.Add(1)
			}
		})
		if got := takes.Load(); got != 1 {
			t.Fatalf("%d instances took the request, want 1", got)
		}
	}
}
