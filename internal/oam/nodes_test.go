package oam

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// fakeNode is a node that serves the real maintenance API with callbacks
// that the test sets.
type fakeNode struct {
	name   string
	roles  []string
	node   *maintenance.Node
	server *httptest.Server
	level  *slog.LevelVar
	users  atomic.Int64

	// settings is the value of each changeable setting.
	mu       sync.Mutex
	settings map[string]int
	// view is the configuration view, which tests may change.
	view func() any
	// wrap, when set, stands between the client and the API.
	wrap func(http.Handler) http.Handler
}

type fakeView struct {
	Node     string             `json:"node"`
	Settings map[string]int     `json:"settings"`
	Password maintenance.Secret `json:"password"`
}

func newFakeNode(t *testing.T, name string, roles []string, settings ...string) *fakeNode {
	t.Helper()
	n := &fakeNode{name: name, roles: roles, level: new(slog.LevelVar), settings: map[string]int{}}
	var functions []maintenance.Function
	for _, r := range roles {
		functions = append(functions, maintenance.Function{Role: r, Users: func() int { return int(n.users.Load()) }})
	}
	var declared []maintenance.Setting
	for _, s := range settings {
		n.settings[s] = 1
		declared = append(declared, maintenance.NewSetting(s, "a test setting",
			func() int { n.mu.Lock(); defer n.mu.Unlock(); return n.settings[s] },
			nil,
			func(v int) { n.mu.Lock(); defer n.mu.Unlock(); n.settings[s] = v }))
	}
	n.view = func() any {
		n.mu.Lock()
		defer n.mu.Unlock()
		v := fakeView{Node: name, Settings: map[string]int{}, Password: maintenance.Redacted("test-password")}
		for k, x := range n.settings {
			v.Settings[k] = x
		}
		return v
	}
	node, err := maintenance.New(maintenance.Config{
		Node:          name,
		Instance:      name + "-0",
		LogLevel:      n.level,
		Functions:     functions,
		Configuration: func() any { return n.view() },
		Settings:      declared,
	})
	if err != nil {
		t.Fatal(err)
	}
	n.node = node
	n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var h http.Handler = node.Handler()
		if n.wrap != nil {
			h = n.wrap(h)
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(n.server.Close)
	return n
}

func (n *fakeNode) endpoint() Endpoint {
	return Endpoint{Name: n.name, Roles: n.roles, URL: n.server.URL}
}

func (n *fakeNode) setting(name string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.settings[name]
}

func (n *fakeNode) setSetting(name string, v int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.settings[name] = v
}

// testServer is the OAM API over httptest, with the nodes it operates.
type testServer struct {
	oam    *Server
	server *httptest.Server
	log    *syncBuffer
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

var testTime = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

func newTestServer(t *testing.T, c Config, nodes ...*fakeNode) *testServer {
	t.Helper()
	for _, n := range nodes {
		c.Nodes = append(c.Nodes, n.endpoint())
	}
	ts := &testServer{log: &syncBuffer{}}
	if c.Log == nil {
		c.Log = slog.New(slog.NewJSONHandler(ts.log, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	if c.Now == nil {
		c.Now = func() time.Time { return testTime }
	}
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	ts.oam = s
	mux := http.NewServeMux()
	mux.Handle(PathPrefix, s.Handler())
	ts.server = httptest.NewServer(mux)
	t.Cleanup(ts.server.Close)
	return ts
}

// call sends a request to the OAM API and returns the status and body.
func (ts *testServer) call(t *testing.T, method, path, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.server.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); method != http.MethodHead && !strings.HasPrefix(ct, "application/") {
		t.Errorf("%s %s: Content-Type %q", method, path, ct)
	}
	return resp.StatusCode, b
}

func decodeAs[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	return v
}

func TestStateListsEveryNodeAndTheOneThatFailed(t *testing.T) {
	p1 := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	p2 := newFakeNode(t, "pcscf-2", []string{"pcscf"})
	cscf := newFakeNode(t, "cscf-1", []string{"icscf", "scscf"})
	p2.server.Close()
	ts := newTestServer(t, Config{}, p1, p2, cscf)

	status, body := ts.call(t, "GET", "/oam/v1/state", "")
	if status != http.StatusOK {
		t.Fatalf("GET state: %d %s", status, body)
	}
	got := decodeAs[FanOut[maintenance.NodeState]](t, body)
	if got.Succeeded != 2 || got.Failed != 1 || len(got.Nodes) != 3 {
		t.Fatalf("state: %s", body)
	}
	for i, want := range []string{"pcscf-1", "pcscf-2", "cscf-1"} {
		if got.Nodes[i].Node != want {
			t.Errorf("node %d is %s, want %s in the order of the inventory", i, got.Nodes[i].Node, want)
		}
	}
	if r := got.Nodes[1]; r.Error == nil || r.Result != nil || r.Error.Error == "" {
		t.Errorf("the closed node: %+v", r)
	}
	if r := got.Nodes[2]; r.Result == nil || len(r.Result.Functions) != 2 || r.Result.Functions[1].Role != "scscf" {
		t.Errorf("the node with two roles: %s", body)
	}

	status, body = ts.call(t, "GET", "/oam/v1/roles/scscf/state", "")
	if got := decodeAs[FanOut[maintenance.NodeState]](t, body); status != http.StatusOK || len(got.Nodes) != 1 || got.Nodes[0].Node != "cscf-1" {
		t.Errorf("GET the scscf role: %d %s", status, body)
	}
	status, body = ts.call(t, "GET", "/oam/v1/nodes/pcscf-1/state", "")
	if got := decodeAs[FanOut[maintenance.NodeState]](t, body); status != http.StatusOK || got.Succeeded != 1 || got.Nodes[0].Result.Node != "pcscf-1" {
		t.Errorf("GET one node: %d %s", status, body)
	}
}

func TestANodeThatDoesNotAnswerInTimeFailsAlone(t *testing.T) {
	slow := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	quick := newFakeNode(t, "pcscf-2", []string{"pcscf"})
	slow.wrap = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	}
	ts := newTestServer(t, Config{NodeTimeout: 50 * time.Millisecond}, slow, quick)

	_, body := ts.call(t, "GET", "/oam/v1/roles/pcscf/state", "")
	got := decodeAs[FanOut[maintenance.NodeState]](t, body)
	if got.Failed != 1 || got.Nodes[0].Error == nil || got.Nodes[0].Error.Error != "no answer within 50ms" {
		t.Errorf("the slow node: %s", body)
	}
	if got.Nodes[1].Result == nil {
		t.Errorf("the quick node: %s", body)
	}
}

func TestDrainARoleThenCancelIt(t *testing.T) {
	busy := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	idle := newFakeNode(t, "pcscf-2", []string{"pcscf"})
	other := newFakeNode(t, "scscf-1", []string{"scscf"})
	busy.users.Store(2)
	ts := newTestServer(t, Config{}, busy, idle, other)

	status, body := ts.call(t, "PUT", "/oam/v1/roles/pcscf/administrative-state",
		`{"administrative_state": "SHUTTINGDOWN", "timeout_seconds": 600, "operator": "test-operator"}`)
	got := decodeAs[FanOut[maintenance.FunctionState]](t, body)
	if status != http.StatusOK || got.Succeeded != 2 || len(got.Nodes) != 2 {
		t.Fatalf("drain: %d %s", status, body)
	}
	if s := got.Nodes[0].Result; s.AdministrativeState != maintenance.ShuttingDown || *s.Users != 2 || s.ShutdownDeadline == nil {
		t.Errorf("the busy node: %s", body)
	}
	if s := got.Nodes[1].Result; s.AdministrativeState != maintenance.Locked {
		t.Errorf("the idle node locks at once (X.731 7.1.3.3): %s", body)
	}
	if other.node.ManagedFunction("scscf").AdministrativeState() != maintenance.Unlocked {
		t.Error("a node of another role was drained")
	}

	status, body = ts.call(t, "PUT", "/oam/v1/roles/pcscf/administrative-state", `{"administrative_state": "UNLOCKED"}`)
	got = decodeAs[FanOut[maintenance.FunctionState]](t, body)
	if status != http.StatusOK || got.Succeeded != 2 {
		t.Fatalf("cancel: %d %s", status, body)
	}
	for _, n := range []*fakeNode{busy, idle} {
		if s := n.node.ManagedFunction("pcscf").AdministrativeState(); s != maintenance.Unlocked {
			t.Errorf("%s after the cancel: %s", n.name, s)
		}
	}
	if !strings.Contains(ts.log.String(), `"operator":"test-operator"`) {
		t.Errorf("the log does not name the operator: %s", ts.log)
	}
}

func TestDrainOneNode(t *testing.T) {
	a := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	b := newFakeNode(t, "pcscf-2", []string{"pcscf"})
	ts := newTestServer(t, Config{}, a, b)

	status, body := ts.call(t, "PUT", "/oam/v1/nodes/pcscf-2/functions/pcscf/administrative-state", `{"administrative_state": "SHUTTINGDOWN"}`)
	if got := decodeAs[FanOut[maintenance.FunctionState]](t, body); status != http.StatusOK || got.Succeeded != 1 || got.Nodes[0].Node != "pcscf-2" {
		t.Fatalf("drain one node: %d %s", status, body)
	}
	if a.node.ManagedFunction("pcscf").AdministrativeState() != maintenance.Unlocked || b.node.ManagedFunction("pcscf").AdministrativeState() != maintenance.Locked {
		t.Error("the drain did not go to the one node only")
	}

	// The node, not the inventory, knows its roles: a role that it does
	// not run fails on that node with the node's 404.
	_, body = ts.call(t, "PUT", "/oam/v1/nodes/pcscf-1/functions/scscf/administrative-state", `{"administrative_state": "LOCKED"}`)
	if got := decodeAs[FanOut[maintenance.FunctionState]](t, body); got.Failed != 1 || got.Nodes[0].Error.StatusCode != http.StatusNotFound {
		t.Errorf("a role the node does not run: %s", body)
	}
	// A shut down of a LOCKED role is the node's 409 (X.731 7.1.3.3).
	_, body = ts.call(t, "PUT", "/oam/v1/nodes/pcscf-2/functions/pcscf/administrative-state", `{"administrative_state": "SHUTTINGDOWN"}`)
	if got := decodeAs[FanOut[maintenance.FunctionState]](t, body); got.Failed != 1 || got.Nodes[0].Error.StatusCode != http.StatusConflict {
		t.Errorf("shut down of a LOCKED role: %s", body)
	}
}

func TestLogLevelOfARole(t *testing.T) {
	a := newFakeNode(t, "scscf-1", []string{"scscf"})
	b := newFakeNode(t, "scscf-2", []string{"scscf"})
	ts := newTestServer(t, Config{}, a, b)

	status, body := ts.call(t, "PUT", "/oam/v1/roles/scscf/log-level", `{"level": "DEBUG", "operator": "test-operator"}`)
	got := decodeAs[FanOut[maintenance.LogLevelChange]](t, body)
	if status != http.StatusOK || got.Succeeded != 2 || got.Nodes[0].Result.Previous != slog.LevelInfo {
		t.Fatalf("PUT log level: %d %s", status, body)
	}
	if a.level.Level() != slog.LevelDebug || b.level.Level() != slog.LevelDebug {
		t.Error("the levels did not change")
	}
	_, body = ts.call(t, "GET", "/oam/v1/nodes/scscf-2/log-level", "")
	if got := decodeAs[FanOut[maintenance.LogLevel]](t, body); got.Nodes[0].Result.Level != slog.LevelDebug {
		t.Errorf("GET log level: %s", body)
	}
}

func TestConfigurationIsReadWithSecretsRedacted(t *testing.T) {
	good := newFakeNode(t, "hss-1", []string{"hss"}, "hss.limit")
	leaky := newFakeNode(t, "hss-2", []string{"hss"})
	// A node of another build that does not redact its view.
	leaky.wrap = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"generation": 1, "configuration": map[string]string{"password": "test-password"}, "changeable": []any{}})
		})
	}
	ts := newTestServer(t, Config{}, good, leaky)

	_, body := ts.call(t, "GET", "/oam/v1/roles/hss/config", "")
	got := decodeAs[FanOut[maintenance.Configuration]](t, body)
	if got.Succeeded != 1 || got.Nodes[0].Result == nil || len(got.Nodes[0].Result.Changeable) != 1 {
		t.Errorf("the good node: %s", body)
	}
	if got.Failed != 1 || got.Nodes[1].Result != nil {
		t.Errorf("the leaky node: %s", body)
	}
	if strings.Contains(string(body), "test-password") {
		t.Errorf("the answer holds a secret: %s", body)
	}
}

func TestFanOutCallsAtMostNodeConcurrencyNodesAtOnce(t *testing.T) {
	var inFlight, most atomic.Int64
	var nodes []*fakeNode
	for i := range 8 {
		n := newFakeNode(t, "pcscf-"+string(rune('a'+i)), []string{"pcscf"})
		n.wrap = func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				now := inFlight.Add(1)
				for {
					m := most.Load()
					if now <= m || most.CompareAndSwap(m, now) {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
				inFlight.Add(-1)
				h.ServeHTTP(w, r)
			})
		}
		nodes = append(nodes, n)
	}
	ts := newTestServer(t, Config{NodeConcurrency: 3}, nodes...)

	_, body := ts.call(t, "GET", "/oam/v1/roles/pcscf/state", "")
	if got := decodeAs[FanOut[maintenance.NodeState]](t, body); got.Succeeded != 8 {
		t.Fatalf("state: %s", body)
	}
	if m := most.Load(); m > 3 || m < 2 {
		t.Errorf("%d calls at once, want at most 3", m)
	}
}

func TestErrorsOfTheNodeOperations(t *testing.T) {
	ts := newTestServer(t, Config{}, newFakeNode(t, "pcscf-1", []string{"pcscf"}))
	tests := []struct {
		method, path, body string
		want               int
	}{
		{"GET", "/oam/v1/nodes/pcscf-9/state", "", http.StatusNotFound},
		{"GET", "/oam/v1/roles/hss/state", "", http.StatusNotFound},
		{"GET", "/oam/v1/nodes/pcscf-1/nothing", "", http.StatusNotFound},
		{"POST", "/oam/v1/state", "", http.StatusMethodNotAllowed},
		{"PUT", "/oam/v1/roles/pcscf/administrative-state", `{"administrative_state": "SHUTTINGDOWN", "force": true}`, http.StatusBadRequest},
		{"PUT", "/oam/v1/roles/pcscf/log-level", `{"level": "LOUD"}`, http.StatusBadRequest},
		{"PUT", "/oam/v1/roles/pcscf/log-level", `{"level": "DEBUG"} {}`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		status, body := ts.call(t, tt.method, tt.path, tt.body)
		if status != tt.want {
			t.Errorf("%s %s: %d %s, want %d", tt.method, tt.path, status, body, tt.want)
		}
		if e := decodeAs[maintenance.ErrorBody](t, body); e.Error == "" {
			t.Errorf("%s %s: no error in %s", tt.method, tt.path, body)
		}
	}
	req, _ := http.NewRequest("DELETE", ts.server.URL+"/oam/v1/roles/pcscf/log-level", nil)
	resp, err := ts.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Allow") != "GET, HEAD, PUT" {
		t.Errorf("Allow: %q", resp.Header.Get("Allow"))
	}
}

func TestNewRefusesABadInventory(t *testing.T) {
	for _, nodes := range [][]Endpoint{
		nil,
		{{Name: "PCSCF", Roles: []string{"pcscf"}, URL: "http://pcscf:8085"}},
		{{Name: "pcscf-1", URL: "http://pcscf:8085"}},
		{{Name: "pcscf-1", Roles: []string{"pcscf"}, URL: "pcscf:8085"}},
		{{Name: "pcscf-1", Roles: []string{"pcscf"}, URL: "http://a"}, {Name: "pcscf-1", Roles: []string{"pcscf"}, URL: "http://b"}},
	} {
		if _, err := New(Config{Nodes: nodes}); err == nil {
			t.Errorf("New took %+v", nodes)
		}
	}
}

// TestAnOperationOutlivesTheClient: a client that closes its side of the
// connection once the request is sent, as nc does, cancels the context
// of the request. The OAM still asks every node and answers.
func TestAnOperationOutlivesTheClient(t *testing.T) {
	node := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	ts := newTestServer(t, Config{}, node)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	ts.oam.Handler().ServeHTTP(w, httptest.NewRequestWithContext(ctx, "GET", "/oam/v1/state", nil))
	got := decodeAs[FanOut[maintenance.NodeState]](t, w.Body.Bytes())
	if w.Code != http.StatusOK || got.Succeeded != 1 {
		t.Errorf("GET state with the client gone: %d %s, want the state of the node", w.Code, w.Body)
	}
}
