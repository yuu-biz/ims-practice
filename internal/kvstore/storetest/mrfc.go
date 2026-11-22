package storetest

import (
	"context"
	"fmt"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mrfc"
)

// MRFC runs the suite of mrfc.Store.
func MRFC(t *testing.T, newPair func(t *testing.T) (mrfc.Store, mrfc.Store)) {
	run(t, newPair, []testCase[mrfc.Store]{
		{"NoConference", mrfcNoConference},
		{"CreateKeepsTheFirst", mrfcCreateKeepsTheFirst},
		{"RemoveLeavesANewerConference", mrfcRemoveLeavesANewerConference},
		{"ConcurrentCreate", mrfcConcurrentCreate},
		{"ConcurrentCreateAndRemove", mrfcConcurrentCreateAndRemove},
		{"ConferencesApart", mrfcConferencesApart},
	})
}

// mrfpMedia is the mix of a conference in context of the MRFP n, named
// as the MRFC names it: by the URL of its control API.
func mrfpMedia(n int, context string) mrfc.ConferenceMedia {
	return mrfc.ConferenceMedia{MRFP: fmt.Sprintf("http://mrfp-%d:8080", n), Context: context}
}

// The helpers below call the Store and report any error it returns,
// since the backends of the suite do not fail. They call t.Errorf, not
// t.Fatalf, because the concurrent cases call them on goroutines of
// their own.

func conference(t *testing.T, s mrfc.Store, id string) (mrfc.ConferenceMedia, bool) {
	t.Helper()
	media, ok, err := s.Conference(context.Background(), id)
	if err != nil {
		t.Errorf("Conference(%s): %v", id, err)
	}
	return media, ok
}

func createConference(t *testing.T, s mrfc.Store, id string, media mrfc.ConferenceMedia) (mrfc.ConferenceMedia, bool) {
	t.Helper()
	kept, created, err := s.CreateConference(context.Background(), id, media)
	if err != nil {
		t.Errorf("CreateConference(%s): %v", id, err)
	}
	return kept, created
}

func removeConference(t *testing.T, s mrfc.Store, id string, media mrfc.ConferenceMedia) {
	t.Helper()
	if err := s.RemoveConference(context.Background(), id, media); err != nil {
		t.Errorf("RemoveConference(%s): %v", id, err)
	}
}

func mrfcNoConference(t *testing.T, a, b mrfc.Store) {
	if got, ok := conference(t, a, "room1"); ok {
		t.Errorf("Conference of an empty store = %+v", got)
	}
	// Removing what is not there changes nothing.
	removeConference(t, b, "room1", mrfpMedia(1, "c1"))
	if got, ok := conference(t, a, "room1"); ok {
		t.Errorf("Conference after removing a missing one = %+v", got)
	}
}

// mrfcCreateKeepsTheFirst: of two instances that create a conference,
// the second learns the media of the first; and once the first has
// removed it, the conference can be created again.
func mrfcCreateKeepsTheFirst(t *testing.T, a, b mrfc.Store) {
	first, second := mrfpMedia(1, "c1"), mrfpMedia(2, "c7")
	if got, created := createConference(t, a, "room1", first); !created || got != first {
		t.Fatalf("CreateConference = %+v, %v", got, created)
	}
	if got, created := createConference(t, b, "room1", second); created || got != first {
		t.Fatalf("a second CreateConference = %+v, %v; want the first media kept", got, created)
	}
	if got, ok := conference(t, b, "room1"); !ok || got != first {
		t.Fatalf("Conference on the other instance = %+v, %v; want %+v", got, ok, first)
	}
	removeConference(t, b, "room1", first)
	if got, ok := conference(t, a, "room1"); ok {
		t.Fatalf("RemoveConference kept the conference: %+v", got)
	}
	if got, created := createConference(t, b, "room1", second); !created || got != second {
		t.Fatalf("CreateConference after the removal = %+v, %v", got, created)
	}
}

// mrfcRemoveLeavesANewerConference: an instance whose last party left
// removes the media it knew, which another instance has replaced since.
func mrfcRemoveLeavesANewerConference(t *testing.T, a, b mrfc.Store) {
	ended, newer := mrfpMedia(1, "c1"), mrfpMedia(2, "c7")
	createConference(t, a, "room1", ended)
	removeConference(t, a, "room1", ended)
	createConference(t, b, "room1", newer)
	removeConference(t, a, "room1", ended)
	if got, ok := conference(t, b, "room1"); !ok || got != newer {
		t.Errorf("removing the ended conference took the new one: %+v, %v", got, ok)
	}
}

// mrfcConcurrentCreate has the first parties of one conference reach
// two instances at once, each with a context of its own. One creates the
// conference; every other learns its media.
func mrfcConcurrentCreate(t *testing.T, a, b mrfc.Store) {
	const n = 20
	got := make([]mrfc.ConferenceMedia, n)
	created := make([]bool, n)
	parallel(n, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		got[i], created[i] = createConference(t, s, "room1", mrfpMedia(i%2+1, fmt.Sprintf("c%d", i)))
	})
	kept, ok := conference(t, a, "room1")
	if !ok {
		t.Fatal("no conference after the creations")
	}
	winners := 0
	for i := range n {
		if created[i] {
			winners++
		}
		if got[i] != kept {
			t.Errorf("creation %d returned %+v; the store has %+v", i, got[i], kept)
		}
	}
	if winners != 1 {
		t.Errorf("%d creations reported that they created the conference, want 1", winners)
	}
}

// mrfcConcurrentCreateAndRemove has parties join and leave one
// conference over and over from two instances. An instance that
// created the conference sees its own media until it removes them:
// no other instance removes or replaces them in between.
func mrfcConcurrentCreateAndRemove(t *testing.T, a, b mrfc.Store) {
	const workers, rounds = 6, 15
	errs := make(chan error, workers*rounds)
	parallel(workers, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		for r := range rounds {
			own := mrfpMedia(i%2+1, fmt.Sprintf("c%d-%d", i, r))
			if _, created := createConference(t, s, "room1", own); !created {
				continue
			}
			if got, ok := conference(t, s, "room1"); !ok || got != own {
				errs <- fmt.Errorf("worker %d created %+v and then read %+v, %v", i, own, got, ok)
			}
			removeConference(t, s, "room1", own)
		}
	})
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got, ok := conference(t, a, "room1"); ok {
		t.Errorf("every creator removed its conference, and the store still has %+v", got)
	}
}

// mrfcConferencesApart: conferences of different IDs do not touch one
// another.
func mrfcConferencesApart(t *testing.T, a, b mrfc.Store) {
	for i := range 10 {
		s := a
		if i%2 == 1 {
			s = b
		}
		createConference(t, s, fmt.Sprintf("room%d", i), mrfpMedia(1, fmt.Sprintf("c%d", i)))
	}
	removeConference(t, a, "room3", mrfpMedia(1, "c3"))
	for i := range 10 {
		got, ok := conference(t, b, fmt.Sprintf("room%d", i))
		if want := mrfpMedia(1, fmt.Sprintf("c%d", i)); i != 3 && (!ok || got != want) {
			t.Errorf("conference room%d = %+v, %v; want %+v", i, got, ok, want)
		}
		if i == 3 && ok {
			t.Errorf("conference room3 is still there: %+v", got)
		}
	}
}
