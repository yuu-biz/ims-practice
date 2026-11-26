package ue_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// TestOpenAPI sends a request to each operation of openapi.yaml, and
// checks that the handler takes exactly the methods of the document.
// Each operation succeeds once, so that the schema of its success is
// checked, but for those of a conference, which needs a focus.
func TestOpenAPI(t *testing.T) {
	n, _ := smsIMS(t)
	caller := newPhone(t, n.pcscf, impi, impu, password, autoAnswer)
	callee := newPhone(t, n.pcscf, impi2, impu2, password2, autoAnswer)

	caller.must(http.MethodGet, "/status", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, caller.url+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	resp.Body.Close()

	caller.wantCode(http.MethodPut, "/answer-policy", `{"mode": "auto", "ring_seconds": 0.2}`, http.StatusOK)
	caller.wantCode(http.MethodPut, "/answer-policy", `{"mode": "never"}`, http.StatusBadRequest)
	caller.wantCode(http.MethodPost, "/call", `{"target": "mailto:someone"}`, http.StatusBadRequest)
	caller.wantCode(http.MethodPost, "/answer", "", http.StatusConflict)
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`", "give_up_after_seconds": 5, "codecs": ["PCMU"]}`)
	caller.waitCall("outgoing active")
	caller.must(http.MethodPost, "/dtmf", `{"digits": "1#"}`)
	caller.wantCode(http.MethodPost, "/dtmf", `{"digits": "1x"}`, http.StatusBadRequest)
	caller.must(http.MethodPost, "/hold", "")
	caller.must(http.MethodPost, "/resume", "")
	caller.wantCode(http.MethodPost, "/swap", "", http.StatusConflict)
	caller.wantCode(http.MethodPost, "/merge", "", http.StatusConflict)
	caller.wantCode(http.MethodGet, "/conference", "", http.StatusNotFound)
	caller.wantCode(http.MethodPost, "/hangup", `{"reject_status": 200}`, http.StatusBadRequest)
	caller.must(http.MethodPost, "/hangup", "")
	caller.waitCall("none")
	caller.wantCode(http.MethodPost, "/hangup", "", http.StatusConflict)

	// The caller takes a call by hand, holds it, and makes a second one
	// to swap with, which waits at the callee until it takes it.
	caller.must(http.MethodPut, "/answer-policy", `{"mode": "manual"}`)
	callee.must(http.MethodPost, "/call", `{"target": "`+impu+`"}`)
	caller.waitCall("incoming ringing")
	caller.must(http.MethodPost, "/answer", "")
	caller.waitCall("incoming active")
	caller.must(http.MethodPost, "/hold", "")
	caller.must(http.MethodPost, "/call", `{"target": "`+impu2+`"}`)
	callee.waitFor("a waiting call", func(s statusJSON) bool { return len(s.Calls) == 2 && s.Calls[1].State == "ringing" })
	callee.must(http.MethodPost, "/answer", "")
	caller.waitFor("two calls, the first held", func(s statusJSON) bool {
		return len(s.Calls) == 2 && s.Calls[1].State == "active" && s.held()[0] && !s.held()[1]
	})
	caller.must(http.MethodPost, "/swap", "")
	caller.waitFor("the calls swapped", func(s statusJSON) bool { return len(s.Calls) == 2 && !s.held()[0] && s.held()[1] })
	caller.must(http.MethodPost, "/hangup", "")
	caller.waitFor("one call left", func(s statusJSON) bool { return len(s.Calls) == 1 })
	caller.must(http.MethodPost, "/hangup", "")
	caller.waitCall("none")

	caller.wantCode(http.MethodGet, "/sms", "", http.StatusOK)
	caller.wantCode(http.MethodPost, "/sms", `{"to": "+12025550102", "text": "Hello"}`, http.StatusOK)
	caller.wantCode(http.MethodPost, "/sms", `{"to": "+12025550102", "txt": "Hello"}`, http.StatusBadRequest)
	caller.wantCode(http.MethodPut, "/sms-memory", `{"full": true}`, http.StatusOK)
	caller.wantCode(http.MethodPut, "/sms-memory", `{}`, http.StatusBadRequest)
	caller.wantCode(http.MethodPost, "/sms-memory/notify", "", http.StatusOK)
	caller.wantCode(http.MethodPut, "/sms-availability", `{"available": false}`, http.StatusOK)
	caller.wantCode(http.MethodPut, "/sms-availability", `{}`, http.StatusBadRequest)

	caller.must(http.MethodPost, "/deregister", "")
	caller.must(http.MethodPost, "/register", "")
	caller.wantCode(http.MethodGet, "/contacts", "", http.StatusNotFound)
	caller.wantCode(http.MethodDelete, "/status", "", http.StatusMethodNotAllowed)

	// The event stream ends in the handler some time after the client
	// has gone, and only then is its exchange checked. Close waits for
	// every handler to return.
	closed := make(chan struct{})
	go func() {
		caller.server.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("a handler of the UE did not return within 10 s of the end of the tests")
	}
	caller.api.CheckRoutes(t, caller.handler)
	caller.api.CheckCovered(t,
		openapitest.FailureOnly{Operation: "POST /merge", Why: "the network of these tests has no conference focus to merge the calls at"},
		openapitest.FailureOnly{Operation: "GET /conference", Why: "without a merge the UE takes part in no conference"})
}
