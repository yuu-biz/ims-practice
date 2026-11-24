package ue_test

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestOpenAPI sends a request to each operation of openapi.yaml, and
// checks that the handler takes exactly the methods of the document.
// The answers that depend on the network, such as those of a short
// message, are only checked against the document.
func TestOpenAPI(t *testing.T) {
	addr := network(t, time.Second)
	caller := newPhone(t, addr, impi, impu, password, autoAnswer)
	newPhone(t, addr, impi2, impu2, password2, autoAnswer)

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

	caller.wantCode(http.MethodGet, "/sms", "", http.StatusOK)
	caller.do(http.MethodPost, "/sms", `{"to": "+12025550102", "text": "Hello"}`)
	caller.wantCode(http.MethodPost, "/sms", `{"to": "+12025550102", "txt": "Hello"}`, http.StatusBadRequest)
	caller.do(http.MethodPut, "/sms-memory", `{"full": true}`)
	caller.wantCode(http.MethodPut, "/sms-memory", `{}`, http.StatusBadRequest)
	caller.do(http.MethodPost, "/sms-memory/notify", "")
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
	caller.api.CheckCovered(t)
}
