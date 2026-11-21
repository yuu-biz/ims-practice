package mmtel

import (
	"sync"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallRecord is what the AS keeps of a call from its 2xx to its end:
// enough for another instance to know the call, and to end both of its
// dialogs. The settings of the services are not part of it;
// they stay in the HSS.
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
// single process.
type Store interface {
	// PutCall keeps r under both of its dialogs.
	PutCall(r CallRecord)
	// CallOf returns the record of the call that the dialog id belongs
	// to, on either leg.
	CallOf(id sip.DialogID) (CallRecord, bool)
	// DeleteCall forgets r.
	DeleteCall(r CallRecord)
}

// ConferenceStore keeps the records of the conferences that the AS
// hosts as their focus (conference.go). Several instances can share
// one; the MemoryStore is one too.
type ConferenceStore interface {
	// PutConference keeps r under its ID, in place of what it had.
	PutConference(r ConferenceRecord)
	// Conference returns the record of the conference id.
	Conference(id string) (ConferenceRecord, bool)
	// DeleteConference forgets the conference id.
	DeleteConference(id string)
}

// MemoryStore is a Store and a ConferenceStore in the memory of one
// process.
type MemoryStore struct {
	mu          sync.Mutex
	calls       map[sip.DialogID]CallRecord
	conferences map[string]ConferenceRecord
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{calls: map[sip.DialogID]CallRecord{}, conferences: map[string]ConferenceRecord{}}
}

func (s *MemoryStore) PutConference(r ConferenceRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.conferences[r.ID] = r.clone()
}

func (s *MemoryStore) Conference(id string) (ConferenceRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.conferences[id]
	return r.clone(), ok
}

func (s *MemoryStore) DeleteConference(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conferences, id)
}

func (s *MemoryStore) PutCall(r CallRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[r.Incoming.ID] = r
	s.calls[r.Outgoing.ID] = r
}

func (s *MemoryStore) CallOf(id sip.DialogID) (CallRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.calls[id]
	return r, ok
}

func (s *MemoryStore) DeleteCall(r CallRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.calls, r.Incoming.ID)
	delete(s.calls, r.Outgoing.ID)
}
