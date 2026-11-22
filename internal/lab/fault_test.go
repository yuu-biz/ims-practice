package lab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestFaultRoutesGoToTheAgent: the three endpoints reach the fault agent
// at the paths of its API, with their method and body, and its answer
// comes back as it is, errors included.
func TestFaultRoutesGoToTheAgent(t *testing.T) {
	type seen struct{ method, path, body string }
	var (
		mu  sync.Mutex
		got []seen
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.Method, r.URL.Path, string(b)})
		mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[{"id":"113e54e0","target":"pcscf","kind":"pause","state":"active"}]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"error":"conflict: fault 113e54e0 (pause) on pcscf is not over"}`))
		default:
			w.Write([]byte(`{"id":"113e54e0","state":"cleared"}`))
		}
	}))
	defer agent.Close()
	h := newHarness(t, Config{FaultAgent: agent.URL})

	if status, body := send(t, http.MethodGet, h.server.URL+"/api/fault/faults", ""); status != 200 || !strings.Contains(body, `"active"`) {
		t.Errorf("GET: %d %s", status, body)
	}
	inject := `{"target":"pcscf","kind":"pause","duration":"20s"}`
	if status, body := send(t, http.MethodPost, h.server.URL+"/api/fault/faults", inject); status != http.StatusConflict || !strings.Contains(body, "is not over") {
		t.Errorf("POST: %d %s, want the agent's 409 with its reason", status, body)
	}
	if status, body := send(t, http.MethodDelete, h.server.URL+"/api/fault/faults/113e54e0", ""); status != 200 || !strings.Contains(body, `"cleared"`) {
		t.Errorf("DELETE: %d %s", status, body)
	}
	want := []seen{
		{http.MethodGet, "/fault/v1/faults", ""},
		{http.MethodPost, "/fault/v1/faults", inject},
		{http.MethodDelete, "/fault/v1/faults/113e54e0", ""},
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("the agent got %+v, want %+v", got, want)
	}
}

func TestFaultRoutesWithoutAnAgent(t *testing.T) {
	h := newHarness(t, Config{})
	for _, r := range [][2]string{
		{http.MethodGet, "/api/fault/faults"},
		{http.MethodPost, "/api/fault/faults"},
		{http.MethodDelete, "/api/fault/faults/113e54e0"},
	} {
		if status, body := send(t, r[0], h.server.URL+r[1], ""); status != http.StatusServiceUnavailable || !strings.Contains(body, "fault agent") {
			t.Errorf("%s %s without an agent: %d %s, want 503 and the reason", r[0], r[1], status, body)
		}
	}
	// The agent's own API is not reachable through the Lab under its own
	// paths, only under /api/fault.
	if status, _ := send(t, http.MethodGet, h.server.URL+"/fault/v1/faults", ""); status == http.StatusServiceUnavailable {
		t.Error("the fault agent is reachable through the Lab at the agent's own path")
	}
}
