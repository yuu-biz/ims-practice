package mrfc

import (
	"context"
	"sync"
)

// ConferenceMedia is where the mix of a conference is: the MRFP, by its
// Processor name, and the context in it.
type ConferenceMedia struct {
	MRFP    string
	Context string
}

// Store keeps what the MRFC instances share: the media
// of each conference, by its conference ID. Every call reads or writes
// one conference. An error is a failure of the store, after which the
// caller cannot tell what the store holds.
type Store interface {
	// Conference returns the media of the conference id, and whether it
	// has any.
	Conference(ctx context.Context, id string) (ConferenceMedia, bool, error)
	// CreateConference keeps media for the conference id unless it has
	// media already. It returns the media that the conference has after
	// the call, and whether this call put them there.
	CreateConference(ctx context.Context, id string, media ConferenceMedia) (ConferenceMedia, bool, error)
	// RemoveConference forgets the conference id, if it still has
	// media: an instance that removes a conference that has ended does
	// not remove one that another instance has created since.
	RemoveConference(ctx context.Context, id string, media ConferenceMedia) error
}

// MemoryStore is a Store in the memory of one process, which instances
// in other processes do not share. It never fails.
type MemoryStore struct {
	mu          sync.Mutex
	conferences map[string]ConferenceMedia
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{conferences: map[string]ConferenceMedia{}}
}

func (s *MemoryStore) Conference(_ context.Context, id string) (ConferenceMedia, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	media, ok := s.conferences[id]
	return media, ok, nil
}

func (s *MemoryStore) CreateConference(_ context.Context, id string, media ConferenceMedia) (ConferenceMedia, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept, ok := s.conferences[id]; ok {
		return kept, false, nil
	}
	s.conferences[id] = media
	return media, true, nil
}

func (s *MemoryStore) RemoveConference(_ context.Context, id string, media ConferenceMedia) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conferences[id] == media {
		delete(s.conferences, id)
	}
	return nil
}
