package maintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testNode is a node with a P-CSCF that counts its sessions and an
// I-CSCF that has none, served over httptest.
type testNode struct {
	node     *Node
	handler  http.Handler
	client   *Client
	clock    *fakeClock
	level    *slog.LevelVar
	sessions atomic.Int64
	disabled atomic.Bool
	log      *bytes.Buffer

	maxSessions atomic.Int64
	retryAfter  atomic.Int64
}

// pcscfView is how the test node shows its configuration file.
type pcscfView struct {
	Node  string `json:"node"`
	PCSCF struct {
		Listen      string `json:"listen"`
		MaxSessions int64  `json:"max_sessions"`
		RetryAfter  int64  `json:"retry_after"`
	} `json:"pcscf"`
	XCAP struct {
		NonceKey Secret `json:"nonce_key"`
	} `json:"xcap"`
}

func (tn *testNode) configuration() any {
	var v pcscfView
	v.Node = "pcscf-1"
	v.PCSCF.Listen = "0.0.0.0:5060"
	v.PCSCF.MaxSessions = tn.maxSessions.Load()
	v.PCSCF.RetryAfter = tn.retryAfter.Load()
	v.XCAP.NonceKey = Redacted("test-nonce-key")
	return v
}

func newTestNode(t *testing.T) *testNode {
	t.Helper()
	tn := &testNode{clock: newFakeClock(), level: new(slog.LevelVar), log: new(bytes.Buffer)}
	tn.maxSessions.Store(100)
	tn.retryAfter.Store(30)
	var logMu sync.Mutex
	logWriter := writerFunc(func(p []byte) (int, error) {
		logMu.Lock()
		defer logMu.Unlock()
		return tn.log.Write(p)
	})
	node, err := New(Config{
		Node:     "pcscf-1",
		Instance: "pcscf-1-7d9f",
		Build:    Build{Version: "v0.13.0", Revision: "8db954e"},
		LogLevel: tn.level,
		Functions: []Function{
			{
				Role:  "pcscf",
				Users: func() int { return int(tn.sessions.Load()) },
				OperationalState: func() OperationalState {
					if tn.disabled.Load() {
						return Disabled
					}
					return Enabled
				},
			},
			{Role: "icscf"},
		},
		Configuration: tn.configuration,
		Settings: []Setting{
			NewSetting("pcscf.max_sessions", "sessions the P-CSCF takes at most",
				func() int { return int(tn.maxSessions.Load()) },
				func(v int) error {
					if v < 1 {
						return errors.New("must be at least 1")
					}
					return nil
				},
				func(v int) { tn.maxSessions.Store(int64(v)) }),
			NewSetting("pcscf.retry_after", "seconds in the Retry-After of a 503 while shutting down",
				func() int { return int(tn.retryAfter.Load()) },
				func(v int) error {
					if v < 0 || v > 3600 {
						return errors.New("must be from 0 to 3600")
					}
					return nil
				},
				func(v int) { tn.retryAfter.Store(int64(v)) }),
		},
		Clock: tn.clock,
		Log:   slog.New(slog.NewJSONHandler(logWriter, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	tn.node = node
	tn.handler = checkedHandler(t, node)
	server := httptest.NewServer(tn.handler)
	t.Cleanup(server.Close)
	tn.client = NewClient(server.URL+"/", server.Client())
	return tn
}

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

func wantAPIError(t *testing.T, err error, status int, contains string) *APIError {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an APIError %d", err, status)
	}
	if apiErr.StatusCode != status || !strings.Contains(apiErr.Message, contains) {
		t.Fatalf("error = %d %q, want %d with %q", apiErr.StatusCode, apiErr.Message, status, contains)
	}
	return apiErr
}

func TestStateRoundTrip(t *testing.T) {
	tn := newTestNode(t)
	tn.sessions.Store(3)
	tn.disabled.Store(true)
	s, err := tn.client.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Node != "pcscf-1" || s.Instance != "pcscf-1-7d9f" || s.Build.Version != "v0.13.0" || s.Build.Revision != "8db954e" {
		t.Errorf("node = %+v", s)
	}
	if !s.StartTime.Equal(tn.clock.Now()) || s.LogLevel != slog.LevelInfo || s.ConfigurationGeneration != 0 {
		t.Errorf("start %v, level %v, generation %d", s.StartTime, s.LogLevel, s.ConfigurationGeneration)
	}
	if len(s.Functions) != 2 {
		t.Fatalf("functions = %+v", s.Functions)
	}
	p, i := s.Functions[0], s.Functions[1]
	if p.Role != "pcscf" || p.AdministrativeState != Unlocked || p.OperationalState != Disabled ||
		p.Users == nil || *p.Users != 3 || p.ShutdownDeadline != nil {
		t.Errorf("pcscf = %+v", p)
	}
	if i.Role != "icscf" || i.AdministrativeState != Unlocked || i.OperationalState != Enabled || i.Users != nil {
		t.Errorf("icscf = %+v", i)
	}
}

// The JSON names are part of the API: the OAM server and e2e scripts
// read them.
func TestStateJSON(t *testing.T) {
	tn := newTestNode(t)
	tn.sessions.Store(1)
	if err := tn.node.ManagedFunction("pcscf").ShutDown(time.Minute); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, StatePath, nil))
	want := `{"node":"pcscf-1","instance":"pcscf-1-7d9f","build":{"version":"v0.13.0","revision":"8db954e"},` +
		`"start_time":"2026-11-20T09:00:00Z","configuration_generation":0,"log_level":"INFO","functions":[` +
		`{"role":"pcscf","administrative_state":"SHUTTINGDOWN","operational_state":"ENABLED","users":1,` +
		`"shutdown_deadline":"2026-11-20T09:01:00Z"},` +
		`{"role":"icscf","administrative_state":"UNLOCKED","operational_state":"ENABLED"}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want || rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("GET state = %d %s\nwant %s", rec.Code, rec.Body, want)
	}
}

func TestDrainOverTheAPI(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	tn.sessions.Store(2)
	s, err := tn.client.SetAdministrativeState(ctx, "pcscf", AdministrativeStateRequest{
		AdministrativeState: ShuttingDown, TimeoutSeconds: 600, Operator: "test-operator",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.AdministrativeState != ShuttingDown || s.ShutdownDeadline == nil || !s.ShutdownDeadline.Equal(tn.clock.Now().Add(10*time.Minute)) {
		t.Fatalf("after shut down: %+v", s)
	}
	pcscf := tn.node.ManagedFunction("pcscf")
	tn.sessions.Add(-1)
	pcscf.UserQuit()
	tn.sessions.Add(-1)
	pcscf.UserQuit()
	state, err := tn.client.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Functions[0]; got.AdministrativeState != Locked || got.ShutdownDeadline != nil {
		t.Errorf("after the last session: %+v", got)
	}
	if got := state.Functions[1].AdministrativeState; got != Unlocked {
		t.Errorf("the I-CSCF of the same node is %s", got)
	}
	s, err = tn.client.SetAdministrativeState(ctx, "pcscf", AdministrativeStateRequest{AdministrativeState: Unlocked})
	if err != nil || s.AdministrativeState != Unlocked {
		t.Fatalf("unlock: %+v, %v", s, err)
	}
	if !strings.Contains(tn.log.String(), `"operator":"test-operator"`) {
		t.Errorf("log has no operator: %s", tn.log)
	}
}

func TestAdministrativeStateErrors(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	tn.node.ManagedFunction("icscf").Lock()
	for _, tc := range []struct {
		name     string
		role     string
		req      AdministrativeStateRequest
		status   int
		contains string
	}{
		{"unknown role", "scscf", AdministrativeStateRequest{AdministrativeState: Locked}, http.StatusNotFound, "no such role"},
		{"unknown value", "pcscf", AdministrativeStateRequest{AdministrativeState: "DRAINING"}, http.StatusBadRequest, "DRAINING"},
		{"no value", "pcscf", AdministrativeStateRequest{}, http.StatusBadRequest, "administrative state"},
		{"timeout without shut down", "pcscf", AdministrativeStateRequest{AdministrativeState: Locked, TimeoutSeconds: 5}, http.StatusBadRequest, "timeout_seconds"},
		{"negative timeout", "pcscf", AdministrativeStateRequest{AdministrativeState: ShuttingDown, TimeoutSeconds: -5}, http.StatusBadRequest, "negative"},
		// time.Duration counts nanoseconds in an int64: these seconds
		// would wrap to a deadline 0.29 s away, and to a negative one.
		{"timeout that wraps", "pcscf", AdministrativeStateRequest{AdministrativeState: ShuttingDown, TimeoutSeconds: 18446744074}, http.StatusBadRequest, "86400"},
		{"timeout that wraps to negative", "pcscf", AdministrativeStateRequest{AdministrativeState: ShuttingDown, TimeoutSeconds: 9223372037}, http.StatusBadRequest, "86400"},
		{"timeout over a day", "pcscf", AdministrativeStateRequest{AdministrativeState: ShuttingDown, TimeoutSeconds: 86401}, http.StatusBadRequest, "86400"},
		{"shut down of LOCKED", "icscf", AdministrativeStateRequest{AdministrativeState: ShuttingDown}, http.StatusConflict, "X.731 7.1.3.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tn.client.SetAdministrativeState(ctx, tc.role, tc.req)
			wantAPIError(t, err, tc.status, tc.contains)
		})
	}
	if got := tn.node.ManagedFunction("pcscf").AdministrativeState(); got != Unlocked {
		t.Errorf("refused requests left the P-CSCF %s", got)
	}
}

func TestRequestBodiesAreStrict(t *testing.T) {
	tn := newTestNode(t)
	for _, tc := range []struct {
		name, method, path, body string
	}{
		{"unknown field", http.MethodPut, AdministrativeStatePath("pcscf"), `{"administrative_state":"LOCKED","force":true}`},
		{"two values", http.MethodPut, AdministrativeStatePath("pcscf"), `{"administrative_state":"LOCKED"}{}`},
		{"not JSON", http.MethodPut, LogLevelPath, `DEBUG`},
		{"unknown level", http.MethodPut, LogLevelPath, `{"level":"LOUD"}`},
		{"too large", http.MethodPut, LogLevelPath, `{"operator":"` + strings.Repeat("x", maxRequestBody) + `"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tn.handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
			var body ErrorBody
			if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" {
				t.Errorf("%s %s = %d %s, want 400 with a JSON error", tc.method, tc.path, rec.Code, rec.Body)
			}
		})
	}
	if got := tn.node.ManagedFunction("pcscf").AdministrativeState(); got != Unlocked {
		t.Errorf("refused requests left the P-CSCF %s", got)
	}
	if got := tn.level.Level(); got != slog.LevelInfo {
		t.Errorf("refused requests set the level to %v", got)
	}
}

func TestLogLevelRoundTrip(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	l, err := tn.client.LogLevel(ctx)
	if err != nil || l.Level != slog.LevelInfo {
		t.Fatalf("level = %v, %v", l, err)
	}
	change, err := tn.client.SetLogLevel(ctx, LogLevelRequest{Level: slog.LevelDebug, Operator: "test-operator"})
	if err != nil {
		t.Fatal(err)
	}
	if change.Level != slog.LevelDebug || change.Previous != slog.LevelInfo || tn.level.Level() != slog.LevelDebug {
		t.Errorf("change = %+v, level %v", change, tn.level.Level())
	}
	// log/slog reads the names in any case, and with an offset.
	rec := httptest.NewRecorder()
	tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, LogLevelPath, strings.NewReader(`{"level":"warn+1"}`)))
	if rec.Code != http.StatusOK || rec.Body.String() != `{"level":"WARN+1","previous":"DEBUG"}`+"\n" {
		t.Errorf("PUT warn+1 = %d %s", rec.Code, rec.Body)
	}
}

// The zero slog.Level is INFO, so a body without a level must not be
// read as a request for INFO.
func TestLogLevelRequestNeedsALevel(t *testing.T) {
	tn := newTestNode(t)
	tn.level.Set(slog.LevelDebug)
	for _, body := range []string{`{"operator":"test-operator"}`, `{}`, `null`} {
		rec := httptest.NewRecorder()
		tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, LogLevelPath, strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "level") {
			t.Errorf("PUT %s = %d %s, want 400 that names the level", body, rec.Code, rec.Body)
		}
	}
	if got := tn.level.Level(); got != slog.LevelDebug {
		t.Errorf("level = %v after the refused requests, want DEBUG", got)
	}
}

func TestUnknownPathsAndMethodsAnswerJSON(t *testing.T) {
	tn := newTestNode(t)
	for _, tc := range []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, PathPrefix + "logs", http.StatusNotFound, ""},
		{http.MethodPost, StatePath, http.StatusMethodNotAllowed, "GET, HEAD"},
		{http.MethodPut, ConfigurationPath, http.StatusMethodNotAllowed, "GET, HEAD, PATCH"},
		{http.MethodDelete, LogLevelPath, http.StatusMethodNotAllowed, "GET, HEAD, PUT"},
		{http.MethodGet, AdministrativeStatePath("pcscf"), http.StatusMethodNotAllowed, "PUT"},
		{http.MethodDelete, AlarmsPath, http.StatusMethodNotAllowed, "GET, HEAD, PATCH"},
		{http.MethodGet, AckStatePath("0011223344556677"), http.StatusMethodNotAllowed, "PUT"},
	} {
		rec := httptest.NewRecorder()
		tn.handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		var body ErrorBody
		if rec.Code != tc.status || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Error == "" ||
			rec.Header().Get("Allow") != tc.allow {
			t.Errorf("%s %s = %d %q Allow %q, want %d Allow %q", tc.method, tc.path, rec.Code, rec.Body, rec.Header().Get("Allow"), tc.status, tc.allow)
		}
	}
}

func TestNewChecksTheConfig(t *testing.T) {
	level := new(slog.LevelVar)
	view := func() any { return nil }
	hss := []Function{{Role: "hss"}}
	setting := func(name string) Setting {
		return NewSetting(name, "", func() string { return "" }, nil, func(string) {})
	}
	for name, c := range map[string]Config{
		"no node":           {LogLevel: level, Functions: hss, Configuration: view},
		"no level":          {Node: "n", Functions: hss, Configuration: view},
		"no roles":          {Node: "n", LogLevel: level, Configuration: view},
		"no view":           {Node: "n", LogLevel: level, Functions: hss},
		"bad role":          {Node: "n", LogLevel: level, Functions: []Function{{Role: "P-CSCF"}}, Configuration: view},
		"duplicate role":    {Node: "n", LogLevel: level, Functions: []Function{{Role: "hss"}, {Role: "hss"}}, Configuration: view},
		"zero setting":      {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{{}}},
		"bad setting name":  {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{setting("HSS.Admission")}},
		"empty name part":   {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{setting("hss..admission")}},
		"secret setting":    {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{setting("xcap.nonce_key")}},
		"secret inside":     {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{setting("hss.aka.k.value")}},
		"duplicate setting": {Node: "n", LogLevel: level, Functions: hss, Configuration: view, Settings: []Setting{setting("hss.admission"), setting("hss.admission")}},
	} {
		if _, err := New(c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := New(Config{Node: "n", LogLevel: level, Functions: hss, Configuration: view,
		Settings: []Setting{setting("hss.admission"), setting("log.level2")}}); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

// Under -race: operators and the role use the node at once.
func TestConcurrentUse(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	tn.sessions.Store(100)
	var wg sync.WaitGroup
	for i := range 4 {
		wg.Go(func() {
			for j := range 20 {
				if _, err := tn.client.State(ctx); err != nil {
					t.Error(err)
					return
				}
				want := []AdministrativeState{Unlocked, ShuttingDown, Locked}[(i+j)%3]
				_, err := tn.client.SetAdministrativeState(ctx, "pcscf", AdministrativeStateRequest{AdministrativeState: want})
				var apiErr *APIError
				if err != nil && !(errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict) {
					t.Error(err)
					return
				}
				level := []slog.Level{slog.LevelDebug, slog.LevelInfo}[j%2]
				if _, err := tn.client.SetLogLevel(ctx, LogLevelRequest{Level: level}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Go(func() {
		pcscf := tn.node.ManagedFunction("pcscf")
		for range 100 {
			tn.sessions.Add(-1)
			pcscf.UserQuit()
			_ = pcscf.AdministrativeState()
		}
	})
	wg.Wait()
}
