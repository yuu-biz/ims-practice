package lab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func send(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	// The Lab takes a POST or a PUT as JSON only.
	if method == http.MethodPost || method == http.MethodPut {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestTheLabListsTheSubscribersAndChangesNone: the listing goes to the
// HSS, and every request that would change a subscription is no route
// of the Lab. Provisioning a subscription is an operator's work, which
// the OAM does, so the HSS hears nothing of those requests.
func TestTheLabListsTheSubscribersAndChangesNone(t *testing.T) {
	type seen struct{ method, path, body string }
	var (
		mu  sync.Mutex
		got []seen
	)
	provisioning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{r.Method, r.URL.Path, string(b)})
		mu.Unlock()
		w.Write([]byte(`[{"private_identity":"` + impi + `"}]`))
	}))
	defer provisioning.Close()
	h := newHarness(t, Config{HSSProvisioning: provisioning.URL})

	if status, body := send(t, http.MethodGet, h.server.URL+"/api/subscribers", ""); status != 200 || !strings.Contains(body, impi) {
		t.Errorf("GET: %d %s", status, body)
	}
	for _, c := range []struct{ what, method, path, body string }{
		{"adding one", http.MethodPost, "/api/subscribers", `{"private_identity":"` + impi + `"}`},
		{"changing one", http.MethodPatch, "/api/subscribers/" + impi, `{"test_user":true}`},
		{"removing one", http.MethodDelete, "/api/subscribers/" + impi, ""},
		{"setting the alerting tone of one", http.MethodPut, "/api/subscribers/" + impi + "/customized-alerting-tone", `{"tone":"cat2","active":true}`},
	} {
		if status, _ := send(t, c.method, h.server.URL+c.path, c.body); status != http.StatusMethodNotAllowed {
			t.Errorf("%s: %d, want 405", c.what, status)
		}
	}

	want := []seen{{http.MethodGet, "/subscriptions", ""}}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("the HSS saw %+v, want %+v", got, want)
	}
}

func TestTheSubscriberListWhenTheHSSIsNotThere(t *testing.T) {
	provisioning := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[]`))
	}))
	h := newHarness(t, Config{HSSProvisioning: provisioning.URL})
	provisioning.Close()
	if status, _ := send(t, http.MethodGet, h.server.URL+"/api/subscribers", ""); status != http.StatusBadGateway {
		t.Errorf("GET with the HSS down: %d, want 502", status)
	}

	// A deployment without a provisioning API: the panel of the page
	// says so instead of showing an empty HSS.
	without := newHarness(t, Config{})
	if status, body := send(t, http.MethodGet, without.server.URL+"/api/subscribers", ""); status != http.StatusServiceUnavailable || !strings.Contains(body, `"error"`) {
		t.Errorf("GET without a provisioning API: %d %s, want 503 with an error", status, body)
	}
}
