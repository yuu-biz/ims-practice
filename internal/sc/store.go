package sc

import (
	"context"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sms"
)

// StatusReportRequest is what the SC keeps of a short message whose
// sender asked for a status report (TP-SRR, TS 23.040 9.2.3.5), to write
// the SMS-STATUS-REPORT once the delivery has ended (9.2.2.3).
type StatusReportRequest struct {
	// Reference names the delivery of the short message.
	Reference string
	// Originator is the sender, who gets the report.
	Originator sms.Address
	// MessageReference is the TP-MR of the SMS-SUBMIT, which the report
	// repeats.
	MessageReference uint8
	// Recipient is the TP-DA of the SMS-SUBMIT, the TP-RA of the report.
	Recipient              sms.Address
	ServiceCentreTimeStamp time.Time
	// Expires is when the Store forgets the request.
	Expires time.Time
}

// Store keeps the status report requests. The outcome of a delivery may
// come to the SC stub of another IP-SM-GW than the one that took the
// short message, so the requests live outside the stub.
// MemoryStore keeps them in the process. An error is a failure of the
// store, after which the caller cannot tell what the store holds.
type Store interface {
	// PutStatusReportRequest keeps r until its Expires.
	PutStatusReportRequest(ctx context.Context, r StatusReportRequest) error
	// TakeStatusReportRequest returns the request of a delivery, and
	// whether there is one, and removes it, so that one report goes out
	// per short message.
	TakeStatusReportRequest(ctx context.Context, reference string) (StatusReportRequest, bool, error)
}

// MemoryStore is a Store in the memory of one process. It never fails.
type MemoryStore struct {
	mu       sync.Mutex
	requests map[string]StatusReportRequest
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{requests: map[string]StatusReportRequest{}}
}

// PutStatusReportRequest keeps r. The store removes it at its Expires,
// as an external store would with a time to live.
func (s *MemoryStore) PutStatusReportRequest(_ context.Context, r StatusReportRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests[r.Reference] = r
	time.AfterFunc(time.Until(r.Expires), func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if stored, ok := s.requests[r.Reference]; ok && !time.Now().Before(stored.Expires) {
			delete(s.requests, r.Reference)
		}
	})
	return nil
}

func (s *MemoryStore) TakeStatusReportRequest(_ context.Context, reference string) (StatusReportRequest, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.requests[reference]
	delete(s.requests, reference)
	if !ok || !time.Now().Before(r.Expires) {
		return StatusReportRequest{}, false, nil
	}
	return r, true, nil
}
