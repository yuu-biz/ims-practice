package mrfc

import "sync"

// ConferenceMedia is where the mix of a conference is: the MRFP, by its
// Processor name, and the context in it.
type ConferenceMedia struct {
	MRFP    string
	Context string
}

// Store keeps what the MRFC instances share: the media
// of each conference, by its conference ID. Every call reads or writes
// one conference.
type Store interface {
	// Conference returns the media of the conference id.
	Conference(id string) (ConferenceMedia, bool)
	// CreateConference keeps media for the conference id unless it has
	// media already. It returns the media that the conference has after
	// the call, and whether this call put them there.
	CreateConference(id string, media ConferenceMedia) (ConferenceMedia, bool)
	// RemoveConference forgets the conference id, if it still has
	// media: an instance that removes a conference that has ended does
	// not remove one that another instance has created since.
	RemoveConference(id string, media ConferenceMedia)
}

// MemoryStore is a Store in the memory of one process, which instances
// in other processes do not share.
type MemoryStore struct {
	mu          sync.Mutex
	conferences map[string]ConferenceMedia
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{conferences: map[string]ConferenceMedia{}}
}

func (s *MemoryStore) Conference(id string) (ConferenceMedia, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	media, ok := s.conferences[id]
	return media, ok
}

func (s *MemoryStore) CreateConference(id string, media ConferenceMedia) (ConferenceMedia, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kept, ok := s.conferences[id]; ok {
		return kept, false
	}
	s.conferences[id] = media
	return media, true
}

func (s *MemoryStore) RemoveConference(id string, media ConferenceMedia) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conferences[id] == media {
		delete(s.conferences, id)
	}
}
