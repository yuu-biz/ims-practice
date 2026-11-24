package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/oam"
)

// OAMAlarmWatches runs the suite of oam.AlarmWatchStore. The two stores
// stand for two OAM instances, which call themselves "a" and "b".
func OAMAlarmWatches(t *testing.T, newPair func(t *testing.T) (oam.AlarmWatchStore, oam.AlarmWatchStore)) {
	run(t, newPair, []testCase[oam.AlarmWatchStore]{
		{"OneInstanceWatchesANode", oamOneWatch},
		{"TheStateGoesToTheNextHolder", oamStateToTheNextHolder},
		{"AnEarlierHolderCannotWrite", oamEarlierHolderCannotWrite},
		{"NodesAreWatchedApart", oamNodesApart},
	})
}

func watchNode(t *testing.T, s oam.AlarmWatchStore, node, instance string) (oam.AlarmWatch, bool) {
	t.Helper()
	w, ok, err := s.Watch(context.Background(), node, instance)
	if err != nil {
		t.Fatalf("Watch(%s, %s): %v", node, instance, err)
	}
	return w, ok
}

func oamOneWatch(t *testing.T, a, b oam.AlarmWatchStore) {
	ctx := context.Background()
	if st, err := b.AlarmWatchState(ctx, "scscf"); err != nil || st != (oam.AlarmWatchState{}) {
		t.Fatalf("the state of a node never watched: %+v, %v", st, err)
	}
	w, ok := watchNode(t, a, "scscf", "a")
	if !ok {
		t.Fatal("the first Watch did not take the watch")
	}
	if st := w.State(); st.Generation != 1 || st.Seed == "" {
		t.Errorf("the state of the first watch: %+v", st)
	}
	if _, ok := watchNode(t, b, "scscf", "b"); ok {
		t.Error("a second instance took a held watch")
	}
	if err := w.Renew(ctx); err != nil {
		t.Errorf("Renew: %v", err)
	}
}

func oamStateToTheNextHolder(t *testing.T, a, b oam.AlarmWatchStore) {
	ctx := context.Background()
	w, _ := watchNode(t, a, "scscf", "a")
	st := w.State()
	st.JournalID, st.LastNumber, st.Unreachable, st.Transitions = "journal-1", 7, true, 1
	if err := w.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got, err := b.AlarmWatchState(ctx, "scscf"); err != nil || got != st {
		t.Errorf("AlarmWatchState = %+v, %v; want %+v", got, err, st)
	}
	if err := w.Release(ctx); err != nil {
		t.Fatal(err)
	}
	next, ok := watchNode(t, b, "scscf", "b")
	if !ok {
		t.Fatal("the watch was not free after the Release")
	}
	want := st
	want.Generation++
	if got := next.State(); got != want {
		t.Errorf("the next holder has %+v, want %+v", got, want)
	}
}

func oamEarlierHolderCannotWrite(t *testing.T, a, b oam.AlarmWatchStore) {
	ctx := context.Background()
	old, _ := watchNode(t, a, "scscf", "a")
	if err := old.Release(ctx); err != nil {
		t.Fatal(err)
	}
	next, ok := watchNode(t, b, "scscf", "b")
	if !ok {
		t.Fatal("the watch was not free after the Release")
	}
	st := old.State()
	st.LastNumber = 99
	if err := old.Save(ctx, st); !errors.Is(err, oam.ErrWatchLost) {
		t.Errorf("Save by the earlier holder: %v, want ErrWatchLost", err)
	}
	if err := old.Renew(ctx); !errors.Is(err, oam.ErrWatchLost) {
		t.Errorf("Renew by the earlier holder: %v, want ErrWatchLost", err)
	}
	// Its Release leaves the watch of the next holder as it is.
	if err := old.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if err := next.Renew(ctx); err != nil {
		t.Errorf("Renew by the holder: %v", err)
	}
	if got, _ := a.AlarmWatchState(ctx, "scscf"); got.LastNumber == 99 || got.Generation != next.State().Generation {
		t.Errorf("the state is %+v", got)
	}
}

func oamNodesApart(t *testing.T, a, b oam.AlarmWatchStore) {
	if _, ok := watchNode(t, a, "scscf", "a"); !ok {
		t.Fatal("no watch of scscf")
	}
	if _, ok := watchNode(t, b, "pcscf", "b"); !ok {
		t.Error("the watch of scscf kept pcscf from being watched")
	}
}
