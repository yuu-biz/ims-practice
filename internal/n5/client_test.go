package n5

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func contextWithTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// serveH2C serves h with NewServer on a loopback port, as a PCF or an
// AF would, and returns its apiRoot. Every request that reaches h is
// checked to have come as HTTP/2.
func serveH2C(t *testing.T, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("%s %s came as %s, want HTTP/2", r.Method, r.URL.Path, r.Proto)
		}
		h.ServeHTTP(w, r)
	}))
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return "http://" + l.Addr().String()
}

// fakePCF answers like a PCF and records what it received. It is
// written by hand, without the package's own server, so that the
// client is tested against the wire format alone. The mutex orders the
// handler's goroutine and the test's.
type fakePCF struct {
	mu     sync.Mutex
	status int
	header map[string]string
	answer string
	got    received
}

type received struct {
	method, path, contentType, userAgent, callback string
	body                                           []byte
}

func (f *fakePCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.got = received{r.Method, r.URL.Path, r.Header.Get("Content-Type"), r.Header.Get("User-Agent"),
		r.Header.Get("3gpp-Sbi-Callback"), body}
	status, header, answer := f.status, f.header, f.answer
	f.mu.Unlock()
	for k, v := range header {
		w.Header().Set(k, v)
	}
	w.WriteHeader(status)
	io.WriteString(w, answer)
}

func (f *fakePCF) set(status int, header map[string]string, answer string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status, f.header, f.answer = status, header, answer
}

func (f *fakePCF) last() received {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.got
}

func TestCreate(t *testing.T) {
	pcf := &fakePCF{status: http.StatusCreated, answer: createAnswer, header: map[string]string{
		"Content-Type": "application/json",
		"Location":     "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/42",
	}}
	c := NewClient(serveH2C(t, pcf) + "/")
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	var create AppSessionContext
	if err := json.Unmarshal([]byte(signallingPathCreate), &create); err != nil {
		t.Fatal(err)
	}
	s, err := c.Create(ctx, *create.AscReqData)
	if err != nil {
		t.Fatal(err)
	}
	got := pcf.last()
	if got.method != http.MethodPost || got.path != "/npcf-policyauthorization/v1/app-sessions" {
		t.Errorf("request %s %s", got.method, got.path)
	}
	if got.contentType != "application/json" || got.userAgent != "PCSCF" {
		t.Errorf("Content-Type %q, User-Agent %q", got.contentType, got.userAgent)
	}
	if mustCompact(t, string(got.body)) != mustCompact(t, signallingPathCreate) {
		t.Errorf("sent %s, want %s", got.body, signallingPathCreate)
	}
	if s.URI != "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/42" {
		t.Errorf("URI %q", s.URI)
	}
	if s.Context.AscRespData == nil || !s.Context.AscRespData.SuppFeat.Has(IMSSBI) {
		t.Errorf("context %+v", s.Context)
	}
}

func TestCreateWithRelativeLocation(t *testing.T) {
	pcf := &fakePCF{status: http.StatusCreated, answer: createAnswer, header: map[string]string{
		"Content-Type": "application/json",
		"Location":     "/npcf-policyauthorization/v1/app-sessions/7",
	}}
	root := serveH2C(t, pcf)
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	s, err := NewClient(root).Create(ctx, AppSessionContextReqData{NotifUri: "http://af/n", SuppFeat: "10", UeIpv4: "192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	if s.URI != root+"/npcf-policyauthorization/v1/app-sessions/7" {
		t.Errorf("URI %q", s.URI)
	}
}

// TestCreateSeeOther: 303 names an existing context and is not
// followed.
func TestCreateSeeOther(t *testing.T) {
	pcf := &fakePCF{status: http.StatusSeeOther, header: map[string]string{
		"Location": "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/9",
	}}
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	s, err := NewClient(serveH2C(t, pcf)).Create(ctx, AppSessionContextReqData{})
	if err != nil {
		t.Fatal(err)
	}
	if s.URI != "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/9" || s.Context.AscReqData != nil {
		t.Errorf("session %+v", s)
	}
}

func TestCreateErrors(t *testing.T) {
	problem := map[string]string{"Content-Type": "application/problem+json"}
	cases := []struct {
		name   string
		pcf    *fakePCF
		status int
		cause  string
	}{
		{"session binding fails", &fakePCF{status: 500, answer: problemDetails, header: problem}, 500, CausePDUSessionNotAvailable},
		{"service refused", &fakePCF{status: 403, answer: `{"status":403,"cause":"REQUESTED_SERVICE_NOT_AUTHORIZED"}`, header: problem},
			403, CauseRequestedServiceNotAuthorized},
		{"not a problem body", &fakePCF{status: 503, answer: "busy", header: map[string]string{"Content-Type": "text/plain"}}, 503, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := contextWithTimeout(t)
			defer cancel()
			_, err := NewClient(serveH2C(t, c.pcf)).Create(ctx, AppSessionContextReqData{})
			var p *ProblemError
			if !errors.As(err, &p) {
				t.Fatalf("error %v is not a *ProblemError", err)
			}
			if p.Status != c.status || p.Problem.Cause != c.cause {
				t.Errorf("got %d %q, want %d %q", p.Status, p.Problem.Cause, c.status, c.cause)
			}
		})
	}
	t.Run("201 without Location", func(t *testing.T) {
		ctx, cancel := contextWithTimeout(t)
		defer cancel()
		_, err := NewClient(serveH2C(t, &fakePCF{status: 201, answer: "{}"})).Create(ctx, AppSessionContextReqData{})
		if err == nil {
			t.Error("no error")
		}
	})
}

func TestUpdate(t *testing.T) {
	pcf := &fakePCF{status: http.StatusNoContent}
	root := serveH2C(t, pcf)
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	uri := root + "/npcf-policyauthorization/v1/app-sessions/42"
	got, err := NewClient(root).Update(ctx, uri, AppSessionContextUpdateData{
		MedComponents: map[string]MediaComponentRm{"1": {MedCompN: 1, FStatus: FlowStatusDisabled}},
		ServInfStatus: ServiceInfoStatusFinal,
	})
	if err != nil || got != nil {
		t.Fatalf("Update = %v, %v", got, err)
	}
	req := pcf.last()
	if req.method != http.MethodPatch || req.path != "/npcf-policyauthorization/v1/app-sessions/42" {
		t.Errorf("request %s %s", req.method, req.path)
	}
	if req.contentType != "application/merge-patch+json" {
		t.Errorf("Content-Type %q", req.contentType)
	}
	if mustCompact(t, string(req.body)) != mustCompact(t, updatePatch) {
		t.Errorf("sent %s, want %s", req.body, updatePatch)
	}

	pcf.set(http.StatusOK, map[string]string{"Content-Type": "application/json"}, createAnswer)
	got, err = NewClient(root).Update(ctx, uri, AppSessionContextUpdateData{ServInfStatus: ServiceInfoStatusFinal})
	if err != nil || got == nil || got.AscRespData == nil {
		t.Fatalf("Update with 200 = %+v, %v", got, err)
	}
}

func TestDelete(t *testing.T) {
	pcf := &fakePCF{status: http.StatusNoContent}
	root := serveH2C(t, pcf)
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	uri := root + "/npcf-policyauthorization/v1/app-sessions/42"
	got, err := NewClient(root).Delete(ctx, uri, nil)
	if err != nil || got != nil {
		t.Fatalf("Delete = %v, %v", got, err)
	}
	req := pcf.last()
	if req.method != http.MethodPost || req.path != "/npcf-policyauthorization/v1/app-sessions/42/delete" {
		t.Errorf("request %s %s", req.method, req.path)
	}
	if len(req.body) != 0 || req.contentType != "" {
		t.Errorf("Delete without events sent %q as %q", req.body, req.contentType)
	}

	pcf.set(http.StatusNotFound, map[string]string{"Content-Type": "application/problem+json"},
		`{"status":404,"cause":"APPLICATION_SESSION_CONTEXT_NOT_FOUND"}`)
	_, err = NewClient(root).Delete(ctx, uri, &EventsSubscReqData{Events: []AfEventSubscription{{Event: "USAGE_REPORT"}}})
	var p *ProblemError
	if !errors.As(err, &p) || p.Problem.Cause != CauseApplicationSessionContextNotFound {
		t.Errorf("Delete of an unknown context: %v", err)
	}
	if req := pcf.last(); req.contentType != "application/json" || string(req.body) != `{"events":[{"event":"USAGE_REPORT"}]}` {
		t.Errorf("sent %q as %q", req.body, req.contentType)
	}
}

func TestAppSessionURI(t *testing.T) {
	const ctx = "http://pcf.5gc.example/npcf-policyauthorization/v1/app-sessions/42"
	if got := AppSessionURI(ctx + "/events-subscription"); got != ctx {
		t.Errorf("got %q", got)
	}
}

func mustCompact(t *testing.T, s string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	return string(b)
}
