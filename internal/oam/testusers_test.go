package oam

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// hssNode is the maintenance API of an HSS node that has the service
// profiles and the settings that the OAM uses beyond those of
// internal/hss.
type hssNode struct {
	server *httptest.Server

	mu                     sync.Mutex
	admission              string
	defaultServiceProfile  string
	testUserServiceProfile string
}

var serviceProfiles = map[string][]string{"default": {"sms"}, "trial": {"sms", "mmtel"}}

func newHSSNode(t *testing.T, withProfiles bool) *hssNode {
	t.Helper()
	h := &hssNode{admission: "all", defaultServiceProfile: "default", testUserServiceProfile: "default"}
	setting := func(name string, field *string, valid func(string) error) maintenance.Setting {
		return maintenance.NewSetting(name, "a test setting",
			func() string { h.mu.Lock(); defer h.mu.Unlock(); return *field },
			valid,
			func(v string) { h.mu.Lock(); defer h.mu.Unlock(); *field = v })
	}
	profile := func(v string) error {
		if _, ok := serviceProfiles[v]; !ok {
			return errors.New("no such service profile")
		}
		return nil
	}
	var settings []maintenance.Setting
	view := func() any { return map[string]any{"hss": map[string]any{"provisioning_listen": "0.0.0.0:8081"}} }
	if withProfiles {
		settings = []maintenance.Setting{
			setting(SettingAdmission, &h.admission, func(v string) error {
				if v != "all" && v != "test_users" {
					return errors.New("all or test_users")
				}
				return nil
			}),
			setting(SettingDefaultServiceProfile, &h.defaultServiceProfile, profile),
			setting(SettingTestUserServiceProfile, &h.testUserServiceProfile, profile),
		}
		view = func() any {
			profiles := map[string]any{}
			for name, features := range serviceProfiles {
				profiles[name] = map[string]any{"features": features}
			}
			return map[string]any{"hss": map[string]any{"service_profiles": profiles}}
		}
	}
	node, err := maintenance.New(maintenance.Config{
		Node: "hss", LogLevel: new(slog.LevelVar), Functions: []maintenance.Function{{Role: "hss"}},
		Configuration: view, Settings: settings,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.server = httptest.NewServer(node.Handler())
	t.Cleanup(h.server.Close)
	return h
}

func (h *hssNode) testUserProfile() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.testUserServiceProfile
}

func newTestUserServer(t *testing.T, hss *fakeHSS, nodes ...*hssNode) *testServer {
	t.Helper()
	history, err := NewFileChangeStore(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c := Config{HSSProvisioning: hss.server.URL, History: history}
	for i, n := range nodes {
		c.Nodes = append(c.Nodes, Endpoint{Name: fmt.Sprintf("hss-%d", i+1), Roles: []string{"hss"}, URL: n.server.URL})
	}
	return newTestServer(t, c)
}

func (ts *testServer) feature(t *testing.T, impi, feature string) FeatureState {
	t.Helper()
	status, body := ts.call(t, "GET", "/oam/v1/subscriptions/"+impi+"/features/"+feature, "")
	if status != http.StatusOK {
		t.Fatalf("feature %s of %s: %d %s", feature, impi, status, body)
	}
	return decodeAs[FeatureState](t, body)
}

func TestAFeatureForTestUsersOnly(t *testing.T) {
	hss := newFakeHSS(t)
	h1, h2 := newHSSNode(t, true), newHSSNode(t, true)
	ts := newTestUserServer(t, hss, h1, h2)
	tester, other := impiOf(1), impiOf(2)
	ts.bulk(t, "", strings.Replace(digestLine(1, "test-password-1"), `"password"`, `"test_user": true, "password"`, 1)+"\n"+digestLine(2, "test-password-2"))

	if f := ts.feature(t, tester, "mmtel"); f.Active || !f.TestUser || f.ServiceProfile != "default" || f.ProfileSource != "test_users" {
		t.Errorf("before: %+v", f)
	}

	status, body := ts.call(t, "PUT", "/oam/v1/test-users/service-profile", `{"service_profile": "trial", "operator": "test-operator"}`)
	set := decodeAs[FanOut[ChangeRecord]](t, body)
	if status != http.StatusOK || set.Succeeded != 2 || h1.testUserProfile() != "trial" || h2.testUserProfile() != "trial" {
		t.Fatalf("enable for test users: %d %s", status, body)
	}
	if f := ts.feature(t, tester, "mmtel"); !f.Active || f.ServiceProfile != "trial" || !f.Admitted || f.HSSNode != "hss-1" {
		t.Errorf("the test user: %+v", f)
	}
	if f := ts.feature(t, other, "mmtel"); f.Active || f.ProfileSource != "default" || !strings.Contains(f.Reason, `"default" does not have it`) {
		t.Errorf("another user: %+v", f)
	}
	if f := ts.feature(t, other, "sms"); !f.Active {
		t.Errorf("a feature of every profile: %+v", f)
	}

	// Widening it one user at a time.
	status, body = ts.call(t, "PUT", "/oam/v1/subscriptions/"+other+"/test-user", `{"test_user": true, "operator": "test-operator"}`)
	if sub := decodeAs[Subscription](t, body); status != http.StatusOK || !sub.TestUser {
		t.Fatalf("mark: %d %s", status, body)
	}
	if f := ts.feature(t, other, "mmtel"); !f.Active {
		t.Errorf("after the mark: %+v", f)
	}
	ts.call(t, "PUT", "/oam/v1/subscriptions/"+other+"/test-user", `{"test_user": false}`)
	if f := ts.feature(t, other, "mmtel"); f.Active {
		t.Errorf("after the unmark: %+v", f)
	}
	if s, _ := hss.stored(other); s.Password != "test-password-2" {
		t.Error("the mark changed the credentials")
	}

	// The change is in the history and rolls back for both HSS nodes.
	status, body = ts.call(t, "POST", "/oam/v1/config/change-sets/"+set.Nodes[0].Result.ChangeSet+"/rollback", "")
	if got := decodeAs[FanOut[ChangeRecord]](t, body); status != http.StatusOK || got.Succeeded != 2 || h1.testUserProfile() != "default" || h2.testUserProfile() != "default" {
		t.Errorf("rollback: %d %s", status, body)
	}
	if f := ts.feature(t, tester, "mmtel"); f.Active {
		t.Errorf("after the rollback: %+v", f)
	}
}

func TestAdmitTestUsersOnly(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newTestUserServer(t, hss, newHSSNode(t, true))
	ts.bulk(t, "", strings.Replace(digestLine(1, "test-password-1"), `"password"`, `"test_user": true, "password"`, 1)+"\n"+
		strings.Replace(digestLine(2, "test-password-2"), `"password"`, `"service_profile": "trial", "password"`, 1))

	if status, body := ts.call(t, "PUT", "/oam/v1/test-users/admission", `{"admission": "test_users"}`); status != http.StatusOK {
		t.Fatalf("admission: %d %s", status, body)
	}
	if f := ts.feature(t, impiOf(2), "sms"); f.Active || f.Admitted || f.Reason != "the HSS admits test users only" || f.ProfileSource != "subscription" {
		t.Errorf("another user: %+v", f)
	}
	if f := ts.feature(t, impiOf(1), "sms"); !f.Active || !f.Admitted {
		t.Errorf("the test user: %+v", f)
	}
	_, body := ts.call(t, "GET", "/oam/v1/test-users", "")
	if got := decodeAs[TestUsers](t, body); got.Admission != AdmitTestUsers || !slices.Equal(got.ServiceProfiles["trial"], []string{"sms", "mmtel"}) {
		t.Errorf("test users: %s", body)
	}
	ts.call(t, "PUT", "/oam/v1/test-users/admission", `{"admission": "all"}`)
	if f := ts.feature(t, impiOf(2), "mmtel"); !f.Active {
		t.Errorf("a user whose subscription names the profile: %+v", f)
	}
}

func TestTestUserErrors(t *testing.T) {
	hss := newFakeHSS(t)
	ts := newTestUserServer(t, hss, newHSSNode(t, true))
	ts.bulk(t, "", digestLine(1, "test-password-1"))
	for _, tt := range []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/oam/v1/test-users/admission", `{"admission": "some"}`, http.StatusBadRequest},
		{"PUT", "/oam/v1/test-users/service-profile", `{"service_profile": "gold"}`, http.StatusBadRequest},
		{"PUT", "/oam/v1/subscriptions/" + impiOf(1) + "/test-user", `{}`, http.StatusBadRequest},
		{"PUT", "/oam/v1/subscriptions/" + impiOf(9) + "/test-user", `{"test_user": true}`, http.StatusNotFound},
		{"GET", "/oam/v1/subscriptions/" + impiOf(9) + "/features/sms", "", http.StatusNotFound},
		{"GET", "/oam/v1/subscriptions/" + impiOf(1) + "/features/video", "", http.StatusNotFound},
	} {
		status, body := ts.call(t, tt.method, tt.path, tt.body)
		if status != tt.want {
			t.Errorf("%s %s: %d %s, want %d", tt.method, tt.path, status, body, tt.want)
		}
	}

	// A node whose configuration carries neither the service profiles nor
	// the settings: it is not an HSS of this network.
	other := newTestUserServer(t, newFakeHSS(t), newHSSNode(t, false))
	status, body := other.call(t, "GET", "/oam/v1/subscriptions/"+impiOf(1)+"/features/sms", "")
	if status != http.StatusBadGateway || !strings.Contains(string(body), "no service profiles") {
		t.Errorf("feature against a node without the profiles: %d %s", status, body)
	}
	none := newTestServer(t, Config{HSSProvisioning: hss.server.URL}, newFakeNode(t, "pcscf-1", []string{"pcscf"}))
	if status, body := none.call(t, "GET", "/oam/v1/test-users", ""); status != http.StatusServiceUnavailable {
		t.Errorf("no HSS node: %d %s", status, body)
	}
}
