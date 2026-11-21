package n5

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// recordingAF keeps what the handler gave it.
type recordingAF struct {
	mu        sync.Mutex
	notified  []EventsNotification
	terminate []TerminationInfo
	err       error
}

func (a *recordingAF) Notify(_ context.Context, n EventsNotification) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.notified = append(a.notified, n)
	return a.err
}

func (a *recordingAF) Terminate(_ context.Context, t TerminationInfo) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminate = append(a.terminate, t)
	return a.err
}

// post sends body to uri over h2c as a PCF would.
func post(t *testing.T, method, uri, contentType, body string) (int, ProblemDetails) {
	t.Helper()
	req, err := http.NewRequest(method, uri, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := sbi.NewHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var p ProblemDetails
	if resp.StatusCode >= 300 {
		if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
			t.Errorf("error answer as %q", ct)
		}
		if err := json.Unmarshal(b, &p); err != nil {
			t.Errorf("error body %q: %v", b, err)
		}
	}
	return resp.StatusCode, p
}

func TestNotificationHandler(t *testing.T) {
	af := &recordingAF{}
	root := serveH2C(t, NewNotificationHandler(af))
	notifUri := root + "/n5-callbacks/call-1"

	if status, _ := post(t, "POST", notifUri+"/notify", "application/json", eventsNotification); status != http.StatusNoContent {
		t.Errorf("notify answered %d", status)
	}
	if status, _ := post(t, "POST", notifUri+"/terminate", "application/json", terminationInfo); status != http.StatusNoContent {
		t.Errorf("terminate answered %d", status)
	}
	af.mu.Lock()
	defer af.mu.Unlock()
	if len(af.notified) != 1 || af.notified[0].EvNotifs[0].Event != FailedResourcesAllocation {
		t.Errorf("notified %+v", af.notified)
	}
	if len(af.terminate) != 1 || af.terminate[0].TermCause != PDUSessionTermination {
		t.Errorf("terminate %+v", af.terminate)
	}
}

func TestNotificationHandlerErrors(t *testing.T) {
	af := &recordingAF{}
	root := serveH2C(t, NewNotificationHandler(af)) + "/n5-callbacks/call-1"
	cases := []struct {
		name, method, path, contentType, body string
		status                                int
		cause                                 string
		params                                []string
	}{
		{"not JSON", "POST", "/notify", "application/json", "{", 400, CauseInvalidMsgFormat, nil},
		{"wrong media type", "POST", "/notify", "text/plain", eventsNotification, 415, "", nil},
		{"no evSubsUri", "POST", "/notify", "application/json", `{"evNotifs":[{"event":"FAILED_RESOURCES_ALLOCATION"}]}`,
			400, CauseMandatoryIEMissing, []string{"/evSubsUri"}},
		{"no termCause nor resUri", "POST", "/terminate", "application/json", `{}`,
			400, CauseMandatoryIEMissing, []string{"/termCause", "/resUri"}},
		{"unknown segment", "POST", "/other", "application/json", `{}`, 404, "", nil},
		{"not POST", "PUT", "/notify", "application/json", eventsNotification, 405, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, p := post(t, c.method, root+c.path, c.contentType, c.body)
			if status != c.status || p.Cause != c.cause || p.Status != c.status {
				t.Errorf("got %d %+v, want %d %q", status, p, c.status, c.cause)
			}
			var params []string
			for _, ip := range p.InvalidParams {
				params = append(params, ip.Param)
			}
			if strings.Join(params, ",") != strings.Join(c.params, ",") {
				t.Errorf("invalidParams %v, want %v", params, c.params)
			}
		})
	}
	af.mu.Lock()
	defer af.mu.Unlock()
	if len(af.notified)+len(af.terminate) != 0 {
		t.Errorf("a bad request reached the AF")
	}
}

// TestNotificationHandlerAFError: the AF's own errors become the
// answer, a *ProblemError as it is and anything else as 500.
func TestNotificationHandlerAFError(t *testing.T) {
	af := &recordingAF{err: &ProblemError{Status: http.StatusNotFound, Problem: ProblemDetails{Cause: "CONTEXT_NOT_FOUND"}}}
	root := serveH2C(t, NewNotificationHandler(af)) + "/cb"
	if status, p := post(t, "POST", root+"/terminate", "application/json", terminationInfo); status != 404 || p.Cause != "CONTEXT_NOT_FOUND" {
		t.Errorf("got %d %+v", status, p)
	}
	af.mu.Lock()
	af.err = io.ErrUnexpectedEOF
	af.mu.Unlock()
	if status, p := post(t, "POST", root+"/notify", "application/json", eventsNotification); status != 500 || p.Cause != CauseSystemFailure {
		t.Errorf("got %d %+v", status, p)
	}
}
