package agw

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// checkedHandler is the control API of r with every exchange checked
// against openapi.yaml, which the tests of the package go through.
func checkedHandler(t *testing.T, r *Relay) http.Handler {
	t.Helper()
	return openapitest.Load(t, "openapi.yaml").Handler(t, Handler(r))
}

// TestOpenAPI sends a request to each operation of openapi.yaml, with
// the answers that the description of the operation names, and checks
// that the handler takes exactly the methods of the document.
func TestOpenAPI(t *testing.T) {
	clip, _ := ramp(t, 4)
	r := newMRFP(t, 20, Announcements{"ramp": clip})
	doc := openapitest.Load(t, "openapi.yaml")
	h := doc.Handler(t, Handler(r))
	send := func(method, path, body string, status int) []byte {
		t.Helper()
		var in io.Reader
		if body != "" {
			in = strings.NewReader(body)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, in))
		if rec.Code != status {
			t.Errorf("%s %s %s = %d %s, want %d", method, path, body, rec.Code, rec.Body, status)
		}
		return rec.Body.Bytes()
	}

	var c Context
	if err := json.Unmarshal(send("POST", "/contexts", `{"terminations": [{"id": "access", "mode": "sendrecv", "remote": "192.0.2.10:4000"}]}`, 200), &c); err != nil {
		t.Fatal(err)
	}
	send("POST", "/contexts", `{"terminations": [{"remote": "nowhere"}]}`, 400)
	send("POST", "/contexts/"+c.ID+"/terminations", `{"id": "core", "latch": "off",
		"signals": {"play": {"announcement": "ramp", "repeat": 2, "delay_ms": 20}},
		"events": {"request_id": 7, "detect": ["signal_completed"], "notify": "http://192.0.2.30:8081/notify"}}`, 200)
	send("POST", "/contexts/no-such-context/terminations", `{"id": "core"}`, 404)
	send("PUT", "/contexts/"+c.ID+"/terminations/access", `{"mode": "sendonly", "signals": {}}`, 200)
	send("PUT", "/contexts/"+c.ID+"/terminations/access", `{"mode": "loud"}`, 400)
	send("PUT", "/contexts/"+c.ID+"/terminations/trunk", `{"mode": "sendonly"}`, 404)
	send("GET", "/contexts", "", 200)
	send("GET", "/announcements/ramp", "", 200)
	send("GET", "/announcements/fanfare", "", 404)
	send("DELETE", "/contexts/"+c.ID+"/terminations/core", "", 200)
	send("DELETE", "/contexts/"+c.ID+"/terminations/core", "", 404)
	send("DELETE", "/contexts/"+c.ID, "", 200)
	send("DELETE", "/contexts/"+c.ID, "", 404)
	send("GET", "/bearers", "", 404)
	send("PATCH", "/contexts", "{}", 405)

	doc.CheckRoutes(t, Handler(r))
	doc.CheckCovered(t)
}
