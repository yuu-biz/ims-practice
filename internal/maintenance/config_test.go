package maintenance

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestConfigurationRoundTrip(t *testing.T) {
	tn := newTestNode(t)
	c, err := tn.client.Configuration(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantView := `{"node":"pcscf-1","pcscf":{"listen":"0.0.0.0:5060","max_sessions":100,"retry_after":30},"xcap":{"nonce_key":"REDACTED"}}`
	if c.Generation != 0 || string(c.Configuration) != wantView {
		t.Errorf("configuration = %d %s, want 0 %s", c.Generation, c.Configuration, wantView)
	}
	if len(c.Changeable) != 2 ||
		c.Changeable[0].Name != "pcscf.max_sessions" || string(c.Changeable[0].Value) != "100" || c.Changeable[0].Description == "" ||
		c.Changeable[1].Name != "pcscf.retry_after" || string(c.Changeable[1].Value) != "30" {
		t.Errorf("changeable = %+v", c.Changeable)
	}
}

func TestConfigurationRefusesAViewWithASecret(t *testing.T) {
	node, err := New(Config{
		Node:      "hss-1",
		LogLevel:  new(slog.LevelVar),
		Functions: []Function{{Role: "hss"}},
		Configuration: func() any {
			return map[string]any{"hss": map[string]any{"subscriptions": []any{
				map[string]any{"private_identity": "user0123@ims.mnc001.mcc001.3gppnetwork.org", "password": "secret-test-password"},
			}}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	node.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ConfigurationPath, nil))
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "$.hss.subscriptions[0].password") ||
		strings.Contains(rec.Body.String(), "secret-test-password") {
		t.Errorf("GET config = %d %s", rec.Code, rec.Body)
	}
}

func TestChangeConfiguration(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	change, err := tn.client.ChangeConfiguration(ctx, ConfigurationChangeRequest{
		Settings: map[string]json.RawMessage{"pcscf.max_sessions": json.RawMessage(`200`)},
		Operator: "test-operator",
		Comment:  "more sessions for the load test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if change.Node != "pcscf-1" || change.Instance != "pcscf-1-7d9f" || change.Generation != 1 || !change.Time.Equal(tn.clock.Now()) ||
		change.Operator != "test-operator" || change.Comment != "more sessions for the load test" {
		t.Errorf("record = %+v", change)
	}
	if len(change.Changes) != 1 || change.Changes[0].Name != "pcscf.max_sessions" ||
		string(change.Changes[0].Previous) != "100" || string(change.Changes[0].Value) != "200" {
		t.Errorf("changes = %+v", change.Changes)
	}
	if got := tn.maxSessions.Load(); got != 200 {
		t.Errorf("max_sessions in force = %d", got)
	}
	state, err := tn.client.State(ctx)
	if err != nil || state.ConfigurationGeneration != 1 {
		t.Errorf("state generation = %d, %v", state.ConfigurationGeneration, err)
	}
	c, err := tn.client.Configuration(ctx)
	if err != nil || c.Generation != 1 || !strings.Contains(string(c.Configuration), `"max_sessions":200`) {
		t.Errorf("configuration after the change = %+v, %v", c, err)
	}
}

// The JSON of the record is what the OAM server stores in its history.
func TestConfigurationChangeJSON(t *testing.T) {
	tn := newTestNode(t)
	rec := httptest.NewRecorder()
	body := `{"settings":{"pcscf.retry_after":60,"pcscf.max_sessions":100},"operator":"test-operator"}`
	tn.node.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, ConfigurationPath, strings.NewReader(body)))
	want := `{"node":"pcscf-1","instance":"pcscf-1-7d9f","generation":1,"time":"2026-11-20T09:00:00Z",` +
		`"operator":"test-operator","changes":[{"name":"pcscf.retry_after","previous":30,"value":60}]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Errorf("PATCH = %d %s\nwant %s", rec.Code, rec.Body, want)
	}
}

// One refused setting refuses the whole change: the valid one is not
// set either, and the generation stays.
func TestConfigurationChangeIsAllOrNothing(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	_, err := tn.client.ChangeConfiguration(ctx, ConfigurationChangeRequest{Settings: map[string]json.RawMessage{
		"pcscf.max_sessions": json.RawMessage(`200`),
		"pcscf.retry_after":  json.RawMessage(`99999`),
		"pcscf.listen":       json.RawMessage(`"0.0.0.0:5070"`),
	}})
	apiErr := wantAPIError(t, err, http.StatusBadRequest, "2 of the settings refused, nothing changed")
	if len(apiErr.Settings) != 2 || !strings.Contains(apiErr.Settings["pcscf.retry_after"], "from 0 to 3600") ||
		!strings.Contains(apiErr.Settings["pcscf.listen"], "restart") {
		t.Errorf("refused settings = %q", apiErr.Settings)
	}
	if got := tn.maxSessions.Load(); got != 100 {
		t.Errorf("max_sessions = %d after a refused change", got)
	}
	if got := tn.node.generation.Load(); got != 0 {
		t.Errorf("generation = %d after a refused change", got)
	}
}

func TestConfigurationChangeErrors(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		req      ConfigurationChangeRequest
		status   int
		contains string
		setting  string
	}{
		{"no settings", ConfigurationChangeRequest{}, http.StatusBadRequest, "no settings", ""},
		{"wrong type", ConfigurationChangeRequest{Settings: map[string]json.RawMessage{"pcscf.max_sessions": json.RawMessage(`"many"`)}},
			http.StatusBadRequest, "refused", "pcscf.max_sessions"},
		{"validation", ConfigurationChangeRequest{Settings: map[string]json.RawMessage{"pcscf.max_sessions": json.RawMessage(`0`)}},
			http.StatusBadRequest, "refused", "pcscf.max_sessions"},
		{"unknown", ConfigurationChangeRequest{Settings: map[string]json.RawMessage{"scscf.max_expires": json.RawMessage(`600`)}},
			http.StatusBadRequest, "refused", "scscf.max_expires"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tn.client.ChangeConfiguration(ctx, tc.req)
			apiErr := wantAPIError(t, err, tc.status, tc.contains)
			if _, ok := apiErr.Settings[tc.setting]; tc.setting != "" && !ok {
				t.Errorf("refused settings = %q, want %s", apiErr.Settings, tc.setting)
			}
		})
	}
	rec := httptest.NewRecorder()
	tn.node.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPatch, ConfigurationPath, strings.NewReader(`{"settings":{},"force":true}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("PATCH with an unknown field = %d %s", rec.Code, rec.Body)
	}
}

// The OAM server numbers its configurations. A node takes only
// a higher number, so a change that an earlier one overtook is refused.
func TestConfigurationGeneration(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	set := func(generation uint64, retryAfter string) (ConfigurationChange, error) {
		return tn.client.ChangeConfiguration(ctx, ConfigurationChangeRequest{
			Generation: generation,
			Settings:   map[string]json.RawMessage{"pcscf.retry_after": json.RawMessage(retryAfter)},
		})
	}
	change, err := set(10, "40")
	if err != nil || change.Generation != 10 {
		t.Fatalf("generation 10: %+v, %v", change, err)
	}
	_, err = set(10, "50")
	wantAPIError(t, err, http.StatusConflict, "10 is not above 10")
	if got := tn.retryAfter.Load(); got != 40 {
		t.Errorf("retry_after = %d after a stale change", got)
	}
	// The present value again: nothing changes, but the generation is
	// taken, as when the OAM server sends a restarted node the whole
	// configuration.
	change, err = set(0, "40")
	if err != nil || change.Generation != 11 || len(change.Changes) != 0 {
		t.Errorf("same value: %+v, %v", change, err)
	}
}

// Under -race: changes and reads at once. Each change gets its own
// generation, and the last one is the node's.
func TestConcurrentConfigurationChanges(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	const workers, each = 4, 10
	var mu sync.Mutex
	seen := map[uint64]bool{}
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			for j := range each {
				change, err := tn.client.ChangeConfiguration(ctx, ConfigurationChangeRequest{Settings: map[string]json.RawMessage{
					"pcscf.max_sessions": json.RawMessage(`1` + strings.Repeat("0", i)),
					"pcscf.retry_after":  json.RawMessage(strings.Repeat("1", j%3+1)),
				}})
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				seen[change.Generation] = true
				mu.Unlock()
				if _, err := tn.client.Configuration(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if len(seen) != workers*each || tn.node.generation.Load() != workers*each {
		t.Errorf("%d generations, node at %d, want %d", len(seen), tn.node.generation.Load(), workers*each)
	}
}

// TestSettingsOfAGroupGoInForceTogether: a change that names several
// settings of one group calls begin once, then the set of each, then
// commit once, and the record reads each value back after the commit.
// A change that names no setting of the group calls neither.
func TestSettingsOfAGroupGoInForceTogether(t *testing.T) {
	var calls []string
	published := map[string]string{}
	pending := map[string]string{}
	setting := func(name string) Setting {
		return NewSetting(name, "", func() string { return published[name] }, nil,
			func(v string) { calls = append(calls, "set "+name); pending[name] = v })
	}
	settings := Together(
		func() { calls = append(calls, "begin") },
		func() {
			calls = append(calls, "commit")
			for k, v := range pending {
				published[k] = v
			}
		},
		setting("hss.a"), setting("hss.b"))
	settings = append(settings, NewSetting("hss.c", "", func() string { return "" }, nil, func(string) { calls = append(calls, "set hss.c") }))
	node, err := New(Config{Node: "hss-1", LogLevel: new(slog.LevelVar), Functions: []Function{{Role: "hss"}},
		Configuration: func() any { return map[string]any{} }, Settings: settings})
	if err != nil {
		t.Fatal(err)
	}

	change, err := node.ChangeConfiguration(ConfigurationChangeRequest{Settings: map[string]json.RawMessage{
		"hss.a": json.RawMessage(`"1"`), "hss.b": json.RawMessage(`"2"`)}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"begin", "set hss.a", "set hss.b", "commit"}; !slices.Equal(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	if len(change.Changes) != 2 || string(change.Changes[0].Value) != `"1"` || string(change.Changes[1].Value) != `"2"` {
		t.Errorf("record = %+v, want the values read back after the commit", change.Changes)
	}

	calls = nil
	if _, err := node.ChangeConfiguration(ConfigurationChangeRequest{Settings: map[string]json.RawMessage{
		"hss.c": json.RawMessage(`"3"`)}}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"set hss.c"}; !slices.Equal(calls, want) {
		t.Errorf("calls for a setting outside the group = %v, want %v", calls, want)
	}
}
