package ibcf

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallRecord is what an IBCF instance keeps of one of its calls outside
// its process: the TrGW context of the call, and the two dialogs once
// the call is answered. TS 24.229 5.10.2.2 step 2 and 5.10.3.2 step 2
// have an IBCF that controls the transport plane save the Contact, CSeq
// and Record-Route of each side, so that it can release the session by
// itself (5.10.2.4, 5.10.3.4); a sip.Dialog holds exactly those.
//
// The dialogs themselves live in the process of the instance whose
// Contact they name: in-dialog requests come back to that instance. The
// record is for the instance that takes its place under the same name
// after a restart: it ends the calls that the earlier process left
// (ReleaseLeftovers), so that their TrGW contexts do not stay reserved
// for good. Any instance also reads the record of the call that the
// Replaces of an INVITE names, by one of its dialogs (Store.CallOf).
type CallRecord struct {
	// Instance is the name of the IBCF instance that holds the call,
	// the host of its URI in the home network.
	Instance string
	// ID tells the calls of one instance apart.
	ID string
	// Direction is the way the INVITE crossed the border.
	Direction interconnect.Direction
	// Peer is the name of the peer's profile.
	Peer string
	// Media is the TrGW context of the call, if it has one.
	Media *MediaContext
	// Home and PeerSide are the dialogs on each side once both are
	// confirmed, with the remote target and CSeq numbers they had after
	// the last request within the call; before the answer they are
	// empty.
	Home, PeerSide *sip.Dialog
}

// DialogIDs are the IDs of the two dialogs of the call, Home first, as
// the IBCF has them: its own tag is the local tag of each. A call that
// is not answered yet has none.
func (r CallRecord) DialogIDs() []sip.DialogID {
	if r.Home == nil || r.PeerSide == nil {
		return nil
	}
	return []sip.DialogID{r.Home.ID, r.PeerSide.ID}
}

// Store keeps the call records of every IBCF instance of the network.
// Each record is under the name of its instance, and each instance
// reads all of its own only once, when it starts (CallsOf). On the way
// of a request the IBCF reads at most one record, by one of its dialogs:
// the call that the Replaces of an INVITE names, which any instance may
// hold (CallOf). Neither walks the calls of the network, so that the
// store can live outside the IBCF processes and grow with the number of
// instances. Each operation returns the failure of the store, so that
// the IBCF does not take a store that it cannot reach for one without
// the call.
type Store interface {
	// PutCall stores r under its instance and ID, and under each of its
	// dialogs, replacing what was there.
	PutCall(ctx context.Context, r CallRecord) error
	// DeleteCall removes the record of a call that has ended, and what
	// names it under its dialogs. Removing one that is not there is not
	// an error.
	DeleteCall(ctx context.Context, instance, id string) error
	// CallsOf returns the records of one instance, in the order of
	// their IDs: at most the calls that the instance had at once.
	CallsOf(ctx context.Context, instance string) ([]CallRecord, error)
	// CallOf returns the record of the call that has the dialog id on
	// either side (CallRecord.DialogIDs), whichever instance holds it,
	// and false when no call has it.
	CallOf(ctx context.Context, id sip.DialogID) (CallRecord, bool, error)
}

// MemoryStore is a Store in the memory of one process. IBCF instances
// in the same process may share one; one in a process of its own loses
// the records with the process, and with them what ReleaseLeftovers
// would find. It never fails.
type MemoryStore struct {
	mu sync.Mutex
	// calls holds the records of each instance, by ID.
	calls map[string]map[string]CallRecord
	// dialogs names the record of the call that has each dialog.
	dialogs map[sip.DialogID]callKey
}

// callKey names a record: its instance and its ID.
type callKey struct{ instance, id string }

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{calls: map[string]map[string]CallRecord{}, dialogs: map[sip.DialogID]callKey{}}
}

func (s *MemoryStore) PutCall(_ context.Context, r CallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	of := s.calls[r.Instance]
	if of == nil {
		of = map[string]CallRecord{}
		s.calls[r.Instance] = of
	}
	s.forgetDialogs(of[r.ID])
	of[r.ID] = r
	for _, id := range r.DialogIDs() {
		s.dialogs[id] = callKey{instance: r.Instance, id: r.ID}
	}
	return nil
}

func (s *MemoryStore) DeleteCall(_ context.Context, instance, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forgetDialogs(s.calls[instance][id])
	delete(s.calls[instance], id)
	if len(s.calls[instance]) == 0 {
		delete(s.calls, instance)
	}
	return nil
}

// forgetDialogs removes what names r under its dialogs. s.mu is held.
func (s *MemoryStore) forgetDialogs(r CallRecord) {
	for _, id := range r.DialogIDs() {
		delete(s.dialogs, id)
	}
}

func (s *MemoryStore) CallOf(_ context.Context, id sip.DialogID) (CallRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.dialogs[id]
	if !ok {
		return CallRecord{}, false, nil
	}
	r, ok := s.calls[k.instance][k.id]
	return r, ok, nil
}

func (s *MemoryStore) CallsOf(_ context.Context, instance string) ([]CallRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []CallRecord
	for _, r := range s.calls[instance] {
		out = append(out, r)
	}
	slices.SortFunc(out, func(a, b CallRecord) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
