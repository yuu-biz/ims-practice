package fault

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("%s %s: Content-Type %q", method, path, ct)
	}
	return w
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return v
}

func TestHTTPInjectListAndClear(t *testing.T) {
	m, e, c := newTestManager()
	h := m.Handler()

	w := do(t, h, "POST", FaultsPath, `{"target":"pcscf","kind":"delay","delay":"200ms","jitter":"20ms","duration":"1m"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST = %d %s", w.Code, w.Body)
	}
	f := decode[Fault](t, w)
	if w.Header().Get("Location") != FaultPath(f.ID) {
		t.Fatalf("Location = %q", w.Header().Get("Location"))
	}
	if f.State != Active || time.Duration(f.Delay) != 200*time.Millisecond || time.Duration(f.Jitter) != 20*time.Millisecond {
		t.Fatalf("fault = %+v", f)
	}
	// The body speaks in durations and RFC 3339 times, and leaves out
	// what the kind does not use.
	var raw map[string]any
	json.Unmarshal(w.Body.Bytes(), &raw)
	if raw["delay"] != "200ms" || raw["duration"] != "1m0s" || raw["expires_at"] != "2026-11-20T09:01:00Z" {
		t.Fatalf("body = %s", w.Body)
	}
	for _, absent := range []string{"loss_percent", "peers", "error", "ended_at"} {
		if _, ok := raw[absent]; ok {
			t.Errorf("body has %q: %s", absent, w.Body)
		}
	}

	list := decode[[]Fault](t, do(t, h, "GET", FaultsPath, ""))
	if len(list) != 1 || list[0].ID != f.ID {
		t.Fatalf("GET list = %+v", list)
	}
	got := decode[Fault](t, do(t, h, "GET", FaultPath(f.ID), ""))
	if got.ID != f.ID || got.State != Active {
		t.Fatalf("GET one = %+v", got)
	}

	c.Advance(10 * time.Second)
	w = do(t, h, "DELETE", FaultPath(f.ID), "")
	if w.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", w.Code, w.Body)
	}
	if got := decode[Fault](t, w); got.State != Cleared || got.EndedAt.IsZero() {
		t.Fatalf("DELETE answered %+v", got)
	}
	if len(e.Calls()) != 2 {
		t.Fatalf("calls = %v", e.Calls())
	}
}

func TestHTTPErrors(t *testing.T) {
	m, e, _ := newTestManager()
	h := m.Handler()
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"POST", FaultsPath, `{"target":"pcscf","kind":"loss"}`, http.StatusBadRequest},
		{"POST", FaultsPath, `{"target":"pcscf","kind":"loss","loss_percent":10,"colour":"red"}`, http.StatusBadRequest},
		{"POST", FaultsPath, `{"target":"pcscf","kind":"delay","delay":200}`, http.StatusBadRequest},
		{"POST", FaultsPath, `{"target":"pcscf","kind":"pause"}{}`, http.StatusBadRequest},
		{"GET", FaultPath("nope"), "", http.StatusNotFound},
		{"DELETE", FaultPath("nope"), "", http.StatusNotFound},
		{"PUT", FaultsPath, "", http.StatusMethodNotAllowed},
		{"GET", PathPrefix + "other", "", http.StatusNotFound},
	} {
		w := do(t, h, tc.method, tc.path, tc.body)
		if w.Code != tc.status || decode[ErrorBody](t, w).Error == "" {
			t.Errorf("%s %s %s = %d %s, want %d with an error", tc.method, tc.path, tc.body, w.Code, w.Body, tc.status)
		}
	}
	if w := do(t, h, "PUT", FaultsPath, ""); w.Header().Get("Allow") != "GET, HEAD, POST" {
		t.Errorf("Allow = %q", w.Header().Get("Allow"))
	}

	do(t, h, "POST", FaultsPath, `{"target":"pcscf","kind":"pause"}`)
	if w := do(t, h, "POST", FaultsPath, `{"target":"pcscf","kind":"stop"}`); w.Code != http.StatusConflict {
		t.Errorf("second node fault = %d %s, want 409", w.Code, w.Body)
	}

	e.applyErr = errors.New("No such image: ims-practice/fault-tools:dev")
	w := do(t, h, "POST", FaultsPath, `{"target":"scscf","kind":"loss","loss_percent":10}`)
	body := decode[ErrorBody](t, w)
	if w.Code != http.StatusInternalServerError || body.Fault == nil || body.Fault.State != Failed || body.Fault.ID == "" {
		t.Fatalf("failed apply = %d %s, want 500 with the failed fault", w.Code, w.Body)
	}
}

// TestTheChangeIsMadeAfterTheRequestHasEnded: closing the Lab page,
// reloading it or stopping curl ends the request while the executor is
// at work. The executor still finishes: the fault becomes active and
// its expiry clears it, and a clear is carried out too, so that no
// rule stays on the target with no fault to end it.
func TestTheChangeIsMadeAfterTheRequestHasEnded(t *testing.T) {
	e := &fakeExecutor{block: make(chan struct{}), started: make(chan struct{})}
	c := newFakeClock()
	m := NewManager(Config{Executor: e, Clock: c})
	h := m.Handler()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", FaultsPath, strings.NewReader(`{"target":"pcscf","kind":"loss","loss_percent":10,"duration":"1m"}`)).WithContext(ctx)
	w := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		h.ServeHTTP(w, req)
	}()
	select {
	case <-e.started:
	case <-time.After(10 * time.Second):
		t.Fatal("Apply did not start within 10 s")
	}
	cancel()
	close(e.block)
	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("POST was not answered within 10 s of Apply returning")
	}
	if w.Code != http.StatusCreated {
		t.Fatalf("POST whose request ended while applying = %d %s, want 201", w.Code, w.Body)
	}
	f := decode[Fault](t, w)
	c.Advance(time.Minute)
	m.ExpireDue(context.Background())
	if got, _ := m.Get(f.ID); got.State != Expired {
		t.Fatalf("after its duration: %+v, want expired", got)
	}

	e.mu.Lock()
	e.block = nil
	e.mu.Unlock()
	g, err := m.Inject(context.Background(), Request{Target: "scscf", Kind: Pause})
	if err != nil {
		t.Fatal(err)
	}
	ended, end := context.WithCancel(context.Background())
	end()
	req = httptest.NewRequest("DELETE", FaultPath(g.ID), nil).WithContext(ended)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || decode[Fault](t, w).State != Cleared {
		t.Fatalf("DELETE whose request had ended = %d %s, want 200 and cleared", w.Code, w.Body)
	}
	want := []string{"apply loss pcscf", "clear loss pcscf", "apply pause scscf", "clear pause scscf"}
	if fmt.Sprint(e.Calls()) != fmt.Sprint(want) {
		t.Fatalf("calls = %v, want %v", e.Calls(), want)
	}
}
