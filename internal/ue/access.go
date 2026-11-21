package ue

import (
	"context"
	"net/netip"
	"sync"
)

// AccessNetwork is the access network as the UE sees it:
// it connects the UE to the IMS APN, tells it where its P-CSCFs are, and
// tells it when the network sets up or takes away the resources that
// carry the media of its calls. The EPC stub is behind it today
// (EPCAccess); real NAS can stand behind the same interface.
type AccessNetwork interface {
	// Attach connects the UE to the IMS APN and returns what the network
	// gave it (TS 24.229 L.2.2.1).
	Attach(ctx context.Context) (Attachment, error)
	// Events delivers what the network tells the UE after the attach. It
	// is closed once the UE has detached or the network has detached it.
	Events() <-chan AccessEvent
	// Detach disconnects the UE from the network.
	Detach(ctx context.Context) error
}

// Attachment is what the UE learns when it attaches.
type Attachment struct {
	// Address is the UE's IP address on the IMS PDN (TS 23.401 5.3.1.1).
	Address netip.Addr
	// PCSCFs are the P-CSCF addresses of the PCO, the most preferred
	// first (TS 24.229 L.2.2.1).
	PCSCFs []netip.Addr
	// AccessNetworkInfo is the value of the P-Access-Network-Info that
	// the UE puts in its requests and responses (TS 24.229 7.2A.4), or ""
	// when the UE does not know its cell.
	AccessNetworkInfo string
}

// AccessEvent is one thing the network tells the UE after the attach.
type AccessEvent int

const (
	// MediaResourcesReserved: the network has set up the dedicated
	// bearer for voice. Until then the UE has no local resources for the
	// media of its calls and sends none (IR.92 2.4.3.1); now it has
	// (TS 24.229 L.2.2.5.1B).
	MediaResourcesReserved AccessEvent = iota + 1
	// MediaResourcesReleased: the network has released that bearer, as
	// when the radio can no longer carry it (IR.92 2.4.2.3).
	MediaResourcesReleased
	// Detached: the network has detached the UE, which no longer has a
	// PDN connection to the IMS (IR.92 2.4.2.1).
	Detached
)

func (e AccessEvent) String() string {
	switch e {
	case MediaResourcesReserved:
		return "media resources reserved"
	case MediaResourcesReleased:
		return "media resources released"
	case Detached:
		return "detached"
	}
	return "unknown access event"
}

// Resources follows whether the access network carries the media of
// the UE's calls now. IR.92 4.4.2 gives a UE one dedicated bearer for
// voice, which carries the media of all its calls, so the UE has one
// such state, which every call reads.
//
// A nil *Resources is an access network that needs no reservation, such
// as the LAN of a softphone: the UE always has its local resources, and
// says so in its first offer (TS 24.229 6.1.2).
type Resources struct {
	mu       sync.Mutex
	reserved bool
	// changed is closed, and replaced, on each change of reserved.
	changed chan struct{}
}

// NewResources starts without the resources reserved.
func NewResources() *Resources { return &Resources{changed: make(chan struct{})} }

// State tells whether the resources are reserved now, and gives a
// channel that is closed when that changes. On a nil *Resources they
// always are, and the channel is nil.
func (r *Resources) State() (reserved bool, changed <-chan struct{}) {
	if r == nil {
		return true, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reserved, r.changed
}

// Set records what the access network says.
func (r *Resources) Set(reserved bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reserved == reserved {
		return
	}
	r.reserved = reserved
	close(r.changed)
	r.changed = make(chan struct{})
}

// FollowAccess keeps the returned Resources up to date with the events
// of an, an access network the UE has attached to, and closes detached
// when the network detaches the UE or the events end.
func FollowAccess(an AccessNetwork) (r *Resources, detached <-chan struct{}) {
	r = NewResources()
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		for e := range an.Events() {
			switch e {
			case MediaResourcesReserved:
				r.Set(true)
			case MediaResourcesReleased:
				r.Set(false)
			case Detached:
				r.Set(false)
				return
			}
		}
	}()
	return r, gone
}
