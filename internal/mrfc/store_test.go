package mrfc

import "testing"

// TestMemoryStoreKeepsTheFirstConference: of two instances that create
// a conference at the same time, the second learns the media of the
// first; and an instance that removes a conference that has ended
// leaves one that another instance has made since.
func TestMemoryStoreKeepsTheFirstConference(t *testing.T) {
	s := NewMemoryStore()
	first := ConferenceMedia{MRFP: "mrfp1", Context: "c1"}
	second := ConferenceMedia{MRFP: "mrfp2", Context: "c7"}
	if got, created := s.CreateConference("x", first); !created || got != first {
		t.Fatalf("CreateConference = %+v, %v", got, created)
	}
	if got, created := s.CreateConference("x", second); created || got != first {
		t.Fatalf("a second CreateConference = %+v, %v; want the first media kept", got, created)
	}
	s.RemoveConference("x", first)
	if _, ok := s.Conference("x"); ok {
		t.Fatal("RemoveConference kept the conference")
	}
	s.CreateConference("x", second)
	s.RemoveConference("x", first)
	if got, ok := s.Conference("x"); !ok || got != second {
		t.Errorf("removing the ended conference took the new one: %+v, %v", got, ok)
	}
}
