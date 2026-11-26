package oam

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

const retryAfter = "pcscf.retry_after"

func newHistoryServer(t *testing.T, nodes ...*fakeNode) (*testServer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oam", "history.jsonl")
	history, err := NewFileChangeStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return newTestServer(t, Config{History: history}, nodes...), path
}

func (ts *testServer) change(t *testing.T, path string, value int) FanOut[ChangeRecord] {
	t.Helper()
	status, body := ts.call(t, "PATCH", path, `{"settings": {"`+retryAfter+`": `+jsonInt(value)+`}, "operator": "test-operator", "comment": "test"}`)
	if status != http.StatusOK {
		t.Fatalf("PATCH %s: %d %s", path, status, body)
	}
	return decodeAs[FanOut[ChangeRecord]](t, body)
}

func jsonInt(v int) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestAChangeOfARoleGetsOneGenerationAndIsRecordedPerNode(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	b := newFakeNode(t, "pcscf-2", []string{"pcscf"}, retryAfter)
	// pcscf-2 was changed 5 times before, without the OAM.
	for range 5 {
		if _, err := b.node.ChangeConfiguration(maintenance.ConfigurationChangeRequest{Settings: map[string]json.RawMessage{retryAfter: json.RawMessage(`1`)}}); err != nil {
			t.Fatal(err)
		}
	}
	ts, path := newHistoryServer(t, a, b)

	got := ts.change(t, "/oam/v1/roles/pcscf/config", 30)
	if got.Succeeded != 2 || got.Nodes[0].Result.Change.Generation != 6 || got.Nodes[1].Result.Change.Generation != 6 {
		t.Fatalf("change of the role: %+v", got)
	}
	r := got.Nodes[0].Result
	if r.Node != "pcscf-1" || r.ChangeSet == "" || r.ChangeSet != got.Nodes[1].Result.ChangeSet || r.Change.Operator != "test-operator" ||
		len(r.Change.Changes) != 1 || string(r.Change.Changes[0].Previous) != "1" || string(r.Change.Changes[0].Value) != "30" {
		t.Errorf("record: %+v", r)
	}
	if a.setting(retryAfter) != 30 || b.setting(retryAfter) != 30 {
		t.Error("the nodes did not change")
	}

	ts.change(t, "/oam/v1/nodes/pcscf-1/config", 60)
	status, body := ts.call(t, "GET", "/oam/v1/nodes/pcscf-1/config/history", "")
	h := decodeAs[History](t, body)
	if status != http.StatusOK || len(h.Changes) != 2 || string(h.Changes[0].Change.Changes[0].Value) != "60" || h.Changes[0].Change.Generation != 7 {
		t.Errorf("history of pcscf-1, newest first: %d %s", status, body)
	}
	_, body = ts.call(t, "GET", "/oam/v1/nodes/pcscf-1/config/history?limit=1", "")
	if h := decodeAs[History](t, body); len(h.Changes) != 1 {
		t.Errorf("limit 1: %s", body)
	}
	_, body = ts.call(t, "GET", "/oam/v1/config/history/"+r.ID, "")
	if got := decodeAs[ChangeRecord](t, body); got.ID != r.ID {
		t.Errorf("one record: %s", body)
	}

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("history file: %v %v", info, err)
	}
	if b, _ := os.ReadFile(path); strings.Count(string(b), "\n") != 3 {
		t.Errorf("history file:\n%s", b)
	}
}

func TestRollbackPutsThePreviousValueBackAsANewGeneration(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	ts, _ := newHistoryServer(t, a)
	r1 := ts.change(t, "/oam/v1/nodes/pcscf-1/config", 30).Nodes[0].Result

	status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", `{"operator": "test-operator"}`)
	got := decodeAs[ChangeRecord](t, body)
	if status != http.StatusOK || got.RollbackOf != r1.ID || got.Forced || got.Change.Generation != 2 || got.Change.Comment != "rollback of "+r1.ID {
		t.Fatalf("rollback: %d %s", status, body)
	}
	if a.setting(retryAfter) != 1 {
		t.Errorf("value after the rollback: %d", a.setting(retryAfter))
	}
	_, body = ts.call(t, "GET", "/oam/v1/nodes/pcscf-1/config/history", "")
	if h := decodeAs[History](t, body); len(h.Changes) != 2 || h.Changes[0].RollbackOf != r1.ID {
		t.Errorf("the history keeps both: %s", body)
	}
}

func TestRollbackRefusesWhenALaterChangeTouchedTheSetting(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter, "pcscf.max_sessions")
	ts, _ := newHistoryServer(t, a)
	r1 := ts.change(t, "/oam/v1/nodes/pcscf-1/config", 30).Nodes[0].Result
	r2 := ts.change(t, "/oam/v1/nodes/pcscf-1/config", 60).Nodes[0].Result
	// A change of another setting is no conflict.
	ts.call(t, "PATCH", "/oam/v1/nodes/pcscf-1/config", `{"settings": {"pcscf.max_sessions": 5}}`)

	status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", "")
	e := decodeAs[maintenance.ErrorBody](t, body)
	if status != http.StatusConflict || !strings.Contains(e.Settings[retryAfter], r2.ID) || len(e.Settings) != 1 {
		t.Fatalf("rollback under a later change: %d %s", status, body)
	}
	if a.setting(retryAfter) != 60 {
		t.Error("a refused rollback changed the node")
	}

	if status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r2.ID+"/rollback", ""); status != http.StatusOK || a.setting(retryAfter) != 30 {
		t.Fatalf("rollback of the last change: %d %s", status, body)
	}
	status, body = ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", `{"force": true}`)
	got := decodeAs[ChangeRecord](t, body)
	if status != http.StatusOK || !got.Forced || a.setting(retryAfter) != 1 || a.setting("pcscf.max_sessions") != 5 {
		t.Errorf("forced rollback: %d %s", status, body)
	}
}

func TestRollbackRefusesWhenTheNodeWasChangedOutsideTheOAM(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	ts, _ := newHistoryServer(t, a)
	r1 := ts.change(t, "/oam/v1/nodes/pcscf-1/config", 30).Nodes[0].Result
	a.setSetting(retryAfter, 45)

	status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", "")
	if e := decodeAs[maintenance.ErrorBody](t, body); status != http.StatusConflict || !strings.Contains(e.Settings[retryAfter], "the node has 45") {
		t.Errorf("rollback over a change outside the OAM: %d %s", status, body)
	}
	if status, _ := ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", `{"force": true}`); status != http.StatusOK || a.setting(retryAfter) != 1 {
		t.Errorf("forced: %d, value %d", status, a.setting(retryAfter))
	}
}

func TestRollbackOfAChangeSetRollsBackEveryNode(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	b := newFakeNode(t, "pcscf-2", []string{"pcscf"}, retryAfter)
	ts, _ := newHistoryServer(t, a, b)
	set := ts.change(t, "/oam/v1/roles/pcscf/config", 30).Nodes[0].Result.ChangeSet
	b.setSetting(retryAfter, 45)

	status, body := ts.call(t, "POST", "/oam/v1/config/change-sets/"+set+"/rollback", "")
	got := decodeAs[FanOut[ChangeRecord]](t, body)
	if status != http.StatusOK || got.Succeeded != 1 || got.Failed != 1 || got.Nodes[1].Error.StatusCode != http.StatusConflict || got.Nodes[1].Error.Settings[retryAfter] == "" {
		t.Fatalf("rollback of the set: %d %s", status, body)
	}
	if a.setting(retryAfter) != 1 || b.setting(retryAfter) != 45 {
		t.Errorf("values: %d %d", a.setting(retryAfter), b.setting(retryAfter))
	}
}

// TestRollbackOfAChangeSetWhoseNodesShareTheirSettings: the instances of
// a role that keep their settings in one store, as those of the HSS do,
// take on a change that reached another of them. The rollback of the
// first node of the set reaches the second through the store, and the
// rollback of the second finds the previous value there already: it is
// done, not a conflict.
func TestRollbackOfAChangeSetWhoseNodesShareTheirSettings(t *testing.T) {
	a := newFakeNode(t, "hss-1", []string{"hss"}, retryAfter)
	b := newFakeNode(t, "hss-2", []string{"hss"}, retryAfter)
	ts, _ := newHistoryServer(t, a, b)
	change := ts.change(t, "/oam/v1/roles/hss/config", 30)
	r1 := change.Nodes[0].Result
	if status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r1.ID+"/rollback", ""); status != http.StatusOK {
		t.Fatalf("rollback of the first node: %d %s", status, body)
	}
	// The second node took the rollback on from the store.
	b.setSetting(retryAfter, 1)

	r2 := change.Nodes[1].Result
	status, body := ts.call(t, "POST", "/oam/v1/config/history/"+r2.ID+"/rollback", "")
	got := decodeAs[ChangeRecord](t, body)
	if status != http.StatusOK || got.RollbackOf != r2.ID || got.Forced || len(got.Change.Changes) != 0 {
		t.Fatalf("rollback of the second node: %d %s", status, body)
	}
	if a.setting(retryAfter) != 1 || b.setting(retryAfter) != 1 {
		t.Errorf("values: %d %d", a.setting(retryAfter), b.setting(retryAfter))
	}
}

func TestConfigurationChangeErrors(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	down := newFakeNode(t, "pcscf-2", []string{"pcscf"}, retryAfter)
	down.server.Close()
	ts, path := newHistoryServer(t, a, down)

	_, body := ts.call(t, "PATCH", "/oam/v1/roles/pcscf/config", `{"settings": {"pcscf.listen": "0.0.0.0:5070"}}`)
	got := decodeAs[FanOut[ChangeRecord]](t, body)
	if got.Failed != 2 || got.Nodes[0].Error.StatusCode != http.StatusBadRequest || got.Nodes[0].Error.Settings["pcscf.listen"] == "" || got.Nodes[1].Error == nil {
		t.Errorf("a setting that does not change while the node runs: %s", body)
	}
	if b, _ := os.ReadFile(path); len(b) != 0 {
		t.Errorf("a refused change was recorded: %s", b)
	}
	for _, tt := range []struct {
		method, path, body string
		want               int
	}{
		{"PATCH", "/oam/v1/roles/pcscf/config", `{"settings": {"` + retryAfter + `": 3}, "generation": 9}`, http.StatusBadRequest},
		{"PATCH", "/oam/v1/roles/pcscf/config", `{"settings": {}}`, http.StatusBadRequest},
		{"POST", "/oam/v1/config/history/0011223344556677/rollback", "", http.StatusNotFound},
		{"POST", "/oam/v1/config/change-sets/0011223344556677/rollback", "", http.StatusNotFound},
		{"GET", "/oam/v1/nodes/pcscf-1/config/history?limit=0", "", http.StatusBadRequest},
	} {
		if status, body := ts.call(t, tt.method, tt.path, tt.body); status != tt.want {
			t.Errorf("%s %s: %d %s, want %d", tt.method, tt.path, status, body, tt.want)
		}
	}
	none := newTestServer(t, Config{}, newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter))
	if status, body := none.call(t, "PATCH", "/oam/v1/roles/pcscf/config", `{"settings": {"`+retryAfter+`": 3}}`); status != http.StatusServiceUnavailable {
		t.Errorf("a change without a history: %d %s", status, body)
	}
}

func TestConcurrentChangesOfARole(t *testing.T) {
	var nodes []*fakeNode
	for _, name := range []string{"pcscf-1", "pcscf-2", "pcscf-3"} {
		nodes = append(nodes, newFakeNode(t, name, []string{"pcscf"}, retryAfter))
	}
	ts, _ := newHistoryServer(t, nodes...)
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := range 8 {
		wg.Go(func() {
			got, err := ts.oam.ChangeConfiguration(context.Background(), ts.oam.nodes, maintenance.ConfigurationChangeRequest{
				Settings: map[string]json.RawMessage{retryAfter: json.RawMessage(jsonInt(10 + i))},
			})
			if err != nil {
				t.Error(err)
				return
			}
			for _, n := range got.Nodes {
				// Two changes that read the same generation: the node
				// takes the first and refuses the second with 409.
				if n.Error != nil && n.Error.StatusCode != http.StatusConflict {
					t.Errorf("%s: %+v", n.Node, n.Error)
				}
			}
			mu.Lock()
			succeeded += got.Succeeded
			mu.Unlock()
		})
	}
	wg.Wait()
	recorded := 0
	ts.oam.history.Scan(func(ChangeRecord) bool { recorded++; return true })
	if recorded != succeeded || succeeded < 3 {
		t.Errorf("%d recorded, %d succeeded", recorded, succeeded)
	}
}

func TestTheHistoryFileSkipsALineCutShortAtTheEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	store, _ := NewFileChangeStore(path)
	store.Append(ChangeRecord{ID: "a", Node: "pcscf-1"})
	f, _ := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	f.WriteString(`{"id": "b", "no`)
	f.Close()
	var ids []string
	if err := store.Scan(func(r ChangeRecord) bool { ids = append(ids, r.ID); return true }); err != nil || len(ids) != 1 {
		t.Errorf("scan: %v %v", ids, err)
	}
	os.WriteFile(path, []byte("{\"id\": \"a\"}\nnot a record\n{\"id\": \"c\"}\n"), 0o600)
	if err := store.Scan(func(ChangeRecord) bool { return true }); err == nil {
		t.Error("a broken line in the middle passed")
	}
}
