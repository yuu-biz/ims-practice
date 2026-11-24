package oam

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAStateFileThatCannotBeRead: a crash can leave the file of a state
// empty or cut short. The store does not refuse the watch of the node
// for good: it takes the file for a state that was lost, which the
// watch starts from, and logs it.
func TestAStateFileThatCannotBeRead(t *testing.T) {
	for _, content := range []string{"", `{"generation": 4, "journal_id": "j`} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "scscf.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var log bytes.Buffer
		store, err := NewFileAlarmWatchStore(dir, slog.New(slog.NewTextHandler(&log, nil)))
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if st, err := store.AlarmWatchState(ctx, "scscf"); err != nil || !st.Lost {
			t.Errorf("%q: AlarmWatchState = %+v, %v; want a lost state", content, st, err)
		}
		w, ok, err := store.Watch(ctx, "scscf", "oam-1")
		if err != nil || !ok {
			t.Fatalf("%q: Watch = %v, %v", content, ok, err)
		}
		if st := w.State(); !st.Lost || st.Generation != 1 || st.JournalID != "" || st.Seed == "" {
			t.Errorf("%q: the state of the watch is %+v", content, st)
		}
		if !strings.Contains(log.String(), "cannot be read") {
			t.Errorf("%q: nothing logged: %s", content, log.String())
		}
		// The watch writes the state again, whole.
		st := w.State()
		st.Lost, st.JournalID = false, "journal-1"
		if err := w.Save(ctx, st); err != nil {
			t.Fatal(err)
		}
		if got, err := store.AlarmWatchState(ctx, "scscf"); err != nil || got != st {
			t.Errorf("%q: after the save: %+v, %v", content, got, err)
		}
	}
}
