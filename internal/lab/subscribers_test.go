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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSubscribersGoToTheHSS(t *testing.T) {
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
		switch r.Method {
		case http.MethodGet:
			w.Write([]byte(`[{"private_identity":"` + impi + `"}]`))
		case http.MethodPost:
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			w.Write(b)
		}
	}))
	defer provisioning.Close()
	h := newHarness(t, Config{HSSProvisioning: provisioning.URL})

	const added = `{"private_identity":"001010000000009@` + domain + `","public_identities":["sip:001010000000009@` + domain + `"],"password":"test-password-9"}`
	if status, body := send(t, http.MethodGet, h.server.URL+"/api/subscribers", ""); status != 200 || !strings.Contains(body, impi) {
		t.Errorf("GET: %d %s", status, body)
	}
	if status, _ := send(t, http.MethodPost, h.server.URL+"/api/subscribers", added); status != http.StatusCreated {
		t.Errorf("POST: %d", status)
	}
	if status, _ := send(t, http.MethodDelete, h.server.URL+"/api/subscribers/"+impi, ""); status != http.StatusNotFound {
		t.Errorf("DELETE: %d, want the HSS's 404", status)
	}
	const tone = `{"tone":"cat2","active":true}`
	if status, body := send(t, http.MethodPut, h.server.URL+"/api/subscribers/"+impi+"/customized-alerting-tone", tone); status != 200 || body != tone {
		t.Errorf("PUT of the alerting tone: %d %s", status, body)
	}
	want := []seen{
		{http.MethodGet, "/subscriptions", ""},
		{http.MethodPost, "/subscriptions", added},
		{http.MethodDelete, "/subscriptions/" + impi, ""},
		{http.MethodPut, "/subscriptions/" + impi + "/customized-alerting-tone", `{"tone":"cat2","active":true}`},
	}
	mu.Lock()
	if len(got) != len(want) {
		t.Fatalf("the HSS saw %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: the HSS saw %+v, want %+v", i, got[i], want[i])
		}
	}
	mu.Unlock()

	provisioning.Close()
	if status, _ := send(t, http.MethodGet, h.server.URL+"/api/subscribers", ""); status != http.StatusBadGateway {
		t.Errorf("GET with the HSS down: %d, want 502", status)
	}
}

func TestSubscribersWithoutProvisioning(t *testing.T) {
	h := newHarness(t, Config{})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if status, body := send(t, method, h.server.URL+"/api/subscribers", ""); status != http.StatusServiceUnavailable || !strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s, want 503 with an error", method, status, body)
		}
	}
	if status, _ := send(t, http.MethodDelete, h.server.URL+"/api/subscribers/"+impi, ""); status != http.StatusServiceUnavailable {
		t.Errorf("DELETE: %d, want 503", status)
	}
}
