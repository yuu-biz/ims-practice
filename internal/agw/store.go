package agw

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
)

// SavedContext is what a relay keeps of a context outside its process,
// so that a relay recreated at the same address opens the same ports
// again and the media goes on. What a termination plays and
// reports is not kept: an announcement that was cut off ends.
type SavedContext struct {
	ID           string
	Terminations []SavedTermination
}

// SavedTermination is one termination of a SavedContext.
type SavedTermination struct {
	ID       TerminationID
	LocalRTP uint16         // the RTCP port is LocalRTP+1
	Remote   netip.AddrPort // zero until known
	Mode     Mode
	// Latch is NAPTOff for a termination that sends to Remote whatever
	// source its packets come from, and empty for one that latches.
	Latch Latch `json:",omitempty"`
	// ReceivePT and SendPT are the payload types of PCMU in what the
	// termination receives and sends (relayTermination).
	ReceivePT uint8 `json:",omitempty"`
	SendPT    uint8 `json:",omitempty"`
}

// ContextStore keeps the contexts of one relay. The relay's name is in
// the store's configuration, so Contexts returns this relay's only, at
// most as many as its port range holds.
type ContextStore interface {
	// PutContext writes c, replacing what was kept under c.ID.
	PutContext(ctx context.Context, c SavedContext) error
	// DeleteContext removes the context id. Removing one that is not
	// there is not an error.
	DeleteContext(ctx context.Context, id string) error
	Contexts(ctx context.Context) ([]SavedContext, error)
}

// MemoryContextStore is a ContextStore in the memory of the relay's own
// process, and goes with it: a relay whose process restarts starts with
// no contexts, as one without a store does.
type MemoryContextStore struct {
	mu       sync.Mutex
	contexts map[string]SavedContext
}

var _ ContextStore = (*MemoryContextStore)(nil)

func NewMemoryContextStore() *MemoryContextStore {
	return &MemoryContextStore{contexts: map[string]SavedContext{}}
}

func (s *MemoryContextStore) PutContext(_ context.Context, c SavedContext) error {
	c.Terminations = slices.Clone(c.Terminations)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contexts[c.ID] = c
	return nil
}

func (s *MemoryContextStore) DeleteContext(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.contexts, id)
	return nil
}

// Contexts returns the contexts in the order of their IDs.
func (s *MemoryContextStore) Contexts(context.Context) ([]SavedContext, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	saved := make([]SavedContext, 0, len(s.contexts))
	for _, c := range s.contexts {
		c.Terminations = slices.Clone(c.Terminations)
		saved = append(saved, c)
	}
	slices.SortFunc(saved, func(a, b SavedContext) int { return strings.Compare(a.ID, b.ID) })
	return saved, nil
}
