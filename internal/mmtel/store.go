package mmtel

import (
	"context"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallRecord is what the AS keeps of a call from its 2xx to its end:
// enough for any instance to know that a dialog is one leg of the call,
// and which dialog is the other (otherSide). Only the instance that
// took the INVITE acts on the call (AS.uri). The settings of the
// services are not part of it; they stay in the HSS.
type CallRecord struct {
	// Incoming is the dialog with the S-CSCF on the side of the INVITE
	// that the AS took, and Outgoing the one on the side of the INVITE it
	// sent.
	Incoming sip.Dialog
	Outgoing sip.Dialog
	// ServedUser is the user the AS served, from P-Served-User, and
	// SessionCase the session case it served the user in.
	ServedUser  string
	SessionCase sip.SessionCase
	// Services are the services that acted on the call, in order, such
	// as "OIR" or "CFNR".
	Services []string
}

// Store keeps the records of the calls that are up. Several instances
// of the AS can share one; the MemoryStore is the one of a
// single process. Each operation returns the failure of the store, so
// that the AS does not take a store that it cannot reach for one
// without the call.
type Store interface {
	// PutCall keeps r under both of its dialogs.
	PutCall(ctx context.Context, r CallRecord) error
	// CallOf returns the record of the call that the dialog id belongs
	// to, on either leg, and false when there is none.
	CallOf(ctx context.Context, id sip.DialogID) (CallRecord, bool, error)
	// DeleteCall forgets r.
	DeleteCall(ctx context.Context, r CallRecord) error
}

// ConferenceStore keeps the records of the conferences that the AS
// hosts as their focus (conference.go). Several instances can share
// one; the MemoryStore is one too. Each operation returns the failure
// of the store, as those of Store do.
type ConferenceStore interface {
	// PutConference keeps r under its ID, in place of what it had.
	PutConference(ctx context.Context, r ConferenceRecord) error
	// Conference returns the record of the conference id, and false
	// when there is none.
	Conference(ctx context.Context, id string) (ConferenceRecord, bool, error)
	// DeleteConference forgets the conference id.
	DeleteConference(ctx context.Context, id string) error
}

// MemoryStore is a Store and a ConferenceStore in the memory of one
// process. It never fails.
type MemoryStore struct {
	mu          sync.Mutex
	calls       map[sip.DialogID]CallRecord
	conferences map[string]ConferenceRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{calls: map[sip.DialogID]CallRecord{}, conferences: map[string]ConferenceRecord{}}
}

func (s *MemoryStore) PutConference(_ context.Context, r ConferenceRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conferences[r.ID] = r.clone()
	return nil
}

func (s *MemoryStore) Conference(_ context.Context, id string) (ConferenceRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.conferences[id]
	return r.clone(), ok, nil
}

func (s *MemoryStore) DeleteConference(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conferences, id)
	return nil
}

func (s *MemoryStore) PutCall(_ context.Context, r CallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[r.Incoming.ID] = r
	s.calls[r.Outgoing.ID] = r
	return nil
}

func (s *MemoryStore) CallOf(_ context.Context, id sip.DialogID) (CallRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.calls[id]
	return r, ok, nil
}

func (s *MemoryStore) DeleteCall(_ context.Context, r CallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.calls, r.Incoming.ID)
	delete(s.calls, r.Outgoing.ID)
	return nil
}
