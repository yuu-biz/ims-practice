package lab

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// simpleContentTypes are the Content-Types that a page of another site
// can have the browser send with a POST without asking the server
// first: none, and the three of a CORS-safelisted request-header
// (Fetch Standard).
var simpleContentTypes = []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"}

// TestAChangeOnlyAsJSON: a POST or a PUT that is not application/json
// is refused with 415 and passed on to no node, so that a page of
// another site open in the learner's browser cannot change the network
// through the Lab.
func TestAChangeOnlyAsJSON(t *testing.T) {
	var reached atomic.Int32
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		answerJSON(http.StatusOK, ueStatus)(w, r)
	}))
	defer node.Close()
	h := newHarness(t, Config{EPC: node.URL, FaultAgent: node.URL, UEs: []UESimulator{{"phone1", node.URL}}})
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/ues/phone1/register"},
		{http.MethodPost, "/api/ues/phone1/call"},
		{http.MethodPut, "/api/ues/phone1/answer-policy"},
		{http.MethodPut, "/api/ues/phone1/xcap/oir"},
		{http.MethodPost, "/api/fault/faults"},
		{http.MethodPost, "/api/epc/network-initiated/ues/001010000000010/detach"},
	} {
		for _, contentType := range simpleContentTypes {
			req, _ := http.NewRequest(route.method, h.server.URL+route.path, strings.NewReader(`{"target":"tel:+12025550102"}`))
			if contentType != "" {
				req.Header.Set("Content-Type", contentType)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnsupportedMediaType {
				t.Errorf("%s %s with Content-Type %q: %d, want 415", route.method, route.path, contentType, resp.StatusCode)
			}
		}
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("%d of the requests reached a node, want none", n)
	}
}
