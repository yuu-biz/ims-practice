package lab

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

// TestEPCRoutesGoToTheStub: the three endpoints reach the EPC stub at
// the path without /api/epc, with their method, and its answer comes
// back as it is.
func TestEPCRoutesGoToTheStub(t *testing.T) {
	var (
		mu  sync.Mutex
		got []string
	)
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = append(got, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`{"imsi":"001010000000010","bearers":[]}`))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer stub.Close()
	h := newHarness(t, Config{EPC: stub.URL})

	if status, body := send(t, http.MethodGet, h.server.URL+"/api/epc/ues/001010000000010", ""); status != 200 || !strings.Contains(body, `"bearers"`) {
		t.Errorf("GET: %d %s", status, body)
	}
	if status, _ := send(t, http.MethodDelete, h.server.URL+"/api/epc/network-initiated/ues/001010000000010/bearers/6", ""); status != http.StatusNoContent {
		t.Errorf("DELETE: %d", status)
	}
	if status, _ := send(t, http.MethodPost, h.server.URL+"/api/epc/network-initiated/ues/001010000000010/detach", ""); status != http.StatusNotFound {
		t.Errorf("POST: %d, want the stub's 404", status)
	}
	want := []string{
		"GET /ues/001010000000010",
		"DELETE /network-initiated/ues/001010000000010/bearers/6",
		"POST /network-initiated/ues/001010000000010/detach",
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(got, want) {
		t.Errorf("the stub got %q, want %q", got, want)
	}
}

func TestEPCRoutesWithoutAStub(t *testing.T) {
	h := newHarness(t, Config{})
	if status, _ := send(t, http.MethodDelete, h.server.URL+"/api/epc/network-initiated/ues/001010000000010/bearers/6", ""); status != http.StatusServiceUnavailable {
		t.Errorf("DELETE without a stub: %d, want 503", status)
	}
	// Only the endpoints of epcRoutes are passed on.
	if status, _ := send(t, http.MethodPost, h.server.URL+"/api/epc/ues/001010000000010/attach-request", "{}"); status == http.StatusServiceUnavailable {
		t.Error("the UE API of the stub is reachable through the Lab")
	}
}
