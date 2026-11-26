package lab

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOpenAPI sends a request to each operation of openapi.yaml, through
// a Lab whose HSS, EPC stub, fault agent and UE simulator are fakes that
// answer 200 with an empty JSON object. What the Lab derives from the
// messages is checked by the other tests, whose harness checks every
// exchange against the document too.
//
// The methods of each path are not asked for with CheckRoutes: the web
// UI on GET / makes every path of the Lab take GET.
func TestOpenAPI(t *testing.T) {
	backend := func() string {
		s := httptest.NewServer(answerJSON(http.StatusOK, "{}\n"))
		t.Cleanup(s.Close)
		return s.URL
	}
	h := newHarness(t, Config{
		HSSProvisioning: backend(),
		EPC:             backend(),
		FaultAgent:      backend(),
		UEs:             []UESimulator{{"phone1", backend()}},
	})
	h.along(toCallee, invite("call-api@172.28.0.100"))
	sendAs := func(method, path, contentType, body string, status int) {
		t.Helper()
		var in io.Reader
		if body != "" {
			in = strings.NewReader(body)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, method, h.server.URL+path, in)
		if err != nil {
			t.Fatal(err)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		// An event stream ends when the client goes; the status and the
		// headers are what is checked.
		if resp.Header.Get("Content-Type") != "text/event-stream" {
			io.Copy(io.Discard, resp.Body)
		}
		cancel()
		resp.Body.Close()
		if resp.StatusCode != status {
			t.Errorf("%s %s = %d, want %d", method, path, resp.StatusCode, status)
		}
	}

	// A POST or a PUT goes as JSON, one without a body too.
	send := func(method, path, body string, status int) {
		t.Helper()
		contentType := ""
		if method == http.MethodPost || method == http.MethodPut {
			contentType = "application/json"
		}
		sendAs(method, path, contentType, body, status)
	}

	send("GET", "/", "", 200)
	send("GET", "/api/messages?call_id=call-api@172.28.0.100", "", 200)
	send("GET", "/api/messages?call_id=%zz", "", 400)
	send("GET", "/api/calls", "", 200)
	send("GET", "/api/registrations", "", 200)
	send("GET", "/api/events", "", 200)
	send("GET", "/api/subscribers", "", 200)
	send("GET", "/api/epc/ues", "", 200)
	send("GET", "/api/epc/ues/001010000000001", "", 200)
	send("DELETE", "/api/epc/network-initiated/ues/001010000000001/bearers/6", "", 200)
	send("POST", "/api/epc/network-initiated/ues/001010000000001/detach", "", 200)
	send("GET", "/api/fault/faults", "", 200)
	send("POST", "/api/fault/faults", `{"target": "pcscf", "kind": "stop"}`, 200)
	send("DELETE", "/api/fault/faults/f1", "", 200)
	send("GET", "/api/ues", "", 200)
	for _, r := range []struct{ method, path, body string }{
		{"POST", "/register", ""},
		{"POST", "/deregister", ""},
		{"POST", "/call", `{"target": "tel:+12025550102"}`},
		{"POST", "/answer", ""},
		{"POST", "/hangup", ""},
		{"POST", "/hold", ""},
		{"POST", "/resume", ""},
		{"POST", "/dtmf", `{"digits": "1"}`},
		{"POST", "/swap", ""},
		{"POST", "/merge", ""},
		{"PUT", "/answer-policy", `{"mode": "manual"}`},
		{"POST", "/sms", `{"to": "+12025550102", "text": "Hello"}`},
		{"PUT", "/sms-memory", `{"full": true}`},
		{"POST", "/sms-memory/notify", ""},
		{"PUT", "/sms-availability", `{"available": false}`},
		{"GET", "/events", ""},
		{"GET", "/xcap", ""},
		{"PUT", "/xcap/services/communication-diversion", `{"active": true}`},
		{"PUT", "/xcap/oir", `{"active": true}`},
	} {
		send(r.method, "/api/ues/phone1"+r.path, r.body, 200)
	}
	send("POST", "/api/ues/phone9/register", "", 404)
	sendAs("POST", "/api/ues/phone1/register", "text/plain", "", 415)
	sendAs("POST", "/api/fault/faults", "", `{"target": "pcscf", "kind": "stop"}`, 415)
	sendAs("PUT", "/api/ues/phone1/answer-policy", "application/x-www-form-urlencoded", "mode=manual", 415)
	send("PATCH", "/api/calls", "", 405)

	// An event stream ends in the handler some time after the client
	// has gone, and only then is its exchange checked. Close waits for
	// every handler to return.
	closed := make(chan struct{})
	go func() {
		h.server.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("a handler of the Lab did not return within 10 s of the end of the tests")
	}
	h.api.CheckCovered(t)
}
