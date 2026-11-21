package sbi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// extendedProblem is a body that adds an attribute beside
// ProblemDetails, as ExtendedProblemDetails of TS 29.562 does.
type extendedProblem struct {
	ProblemDetails
	ServerName string `json:"serverName,omitempty"`
}

type (
	plainError    = ProblemError[ProblemDetails, *ProblemDetails]
	extendedError = ProblemError[extendedProblem, *extendedProblem]
)

var (
	plainAPI = API[ProblemDetails, *ProblemDetails]{
		Name:            "test",
		CauseStatus:     map[string]int{"CONTEXT_NOT_FOUND": http.StatusNotFound},
		AnswersSeeOther: true,
	}
	extendedAPI = API[extendedProblem, *extendedProblem]{
		Name:        "ext",
		CauseStatus: map[string]int{"ALREADY_REGISTERED": http.StatusForbidden},
	}
)

func contextWithTimeout(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// serveH2C serves h with NewServer on a loopback port and returns its
// apiRoot. Every request that reaches h is checked to have come as
// HTTP/2.
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

// TestServerRefusesHTTP1: an SBI peer speaks HTTP/2 only (TS 29.500
// 5.2.1), so an HTTP/1.1 request gets no HTTP/1.1 answer.
func TestServerRefusesHTTP1(t *testing.T) {
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an HTTP/1.1 request reached the handler")
	}))
	conn, err := net.Dial("tcp", strings.TrimPrefix(root, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /nf-api/v1/resources HTTP/1.1\r\nHost: nf\r\n\r\n")
	line, _ := bufio.NewReader(conn).ReadString('\n')
	if strings.HasPrefix(line, "HTTP/1.1 2") {
		t.Errorf("HTTP/1.1 request was answered %q", line)
	}
}

// TestClientSpeaksHTTP2: the client opens with the HTTP/2 connection
// preface (RFC 9113 3.4), not with an HTTP/1.1 request line.
func TestClientSpeaksHTTP2(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			got <- err.Error()
			return
		}
		defer conn.Close()
		buf := make([]byte, 24)
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, _ := conn.Read(buf)
		got <- string(buf[:n])
	}()
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	go plainAPI.Send(ctx, NewHTTPClient(), Request{Method: http.MethodPost, URI: "http://" + l.Addr().String() + "/", Body: struct{}{}, ContentType: ContentTypeJSON})
	if preface := <-got; preface != "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" {
		t.Errorf("client started with %q", preface)
	}
}

// TestSendHeaders: the headers of TS 29.500 5.2.2.2 and 5.2.3.2.3 go
// out with the request.
func TestSendHeaders(t *testing.T) {
	got := make(chan http.Header, 2)
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	client := NewHTTPClient()

	_, err := plainAPI.Send(ctx, client, Request{
		Method: http.MethodPost, URI: root + "/notify",
		Body: map[string]string{"a": "b"}, ContentType: ContentTypeJSON,
		UserAgent: "PCF", Callback: "Npcf_PolicyAuthorization_Notify",
	})
	if err != nil {
		t.Fatal(err)
	}
	h := <-got
	for name, want := range map[string]string{
		"Content-Type":      ContentTypeJSON,
		"Accept":            "application/json, application/problem+json",
		"User-Agent":        "PCF",
		"3gpp-Sbi-Callback": "Npcf_PolicyAuthorization_Notify",
	} {
		if h.Get(name) != want {
			t.Errorf("%s: %q, want %q", name, h.Get(name), want)
		}
	}

	if _, err := plainAPI.Send(ctx, client, Request{Method: http.MethodGet, URI: root + "/data", UserAgent: "SCSCF"}); err != nil {
		t.Fatal(err)
	}
	h = <-got
	if h.Get("Content-Type") != "" || h.Get("3gpp-Sbi-Callback") != "" {
		t.Errorf("a GET without a body sent Content-Type %q and callback %q", h.Get("Content-Type"), h.Get("3gpp-Sbi-Callback"))
	}
}

// TestSendSeeOther: a 303 is an answer only for an API that says so.
func TestSendSeeOther(t *testing.T) {
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/existing")
		w.WriteHeader(http.StatusSeeOther)
	}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	r, err := plainAPI.Send(ctx, NewHTTPClient(), Request{Method: http.MethodPost, URI: root + "/"})
	if err != nil || r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/existing" {
		t.Errorf("AnswersSeeOther: %+v, %v", r, err)
	}
	_, err = extendedAPI.Send(ctx, NewHTTPClient(), Request{Method: http.MethodPost, URI: root + "/"})
	var p *extendedError
	if !errors.As(err, &p) || p.Status != http.StatusSeeOther {
		t.Errorf("without AnswersSeeOther: %v", err)
	}
}

// TestErrorAnswer sends an error answer from a handler and reads it
// back with Send: the status, the cause and the attribute an API adds
// beside ProblemDetails survive the trip.
func TestErrorAnswer(t *testing.T) {
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/registered":
			p := extendedAPI.NewProblem("ALREADY_REGISTERED", "")
			p.Problem.ServerName = "sip:scscf.ims.example"
			extendedAPI.WriteError(w, p)
		case "/failed":
			extendedAPI.WriteError(w, errors.New("database down"))
		case "/literal":
			extendedAPI.WriteError(w, &extendedError{Status: http.StatusMethodNotAllowed})
		default:
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, "no route")
		}
	}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	cases := []struct {
		path   string
		status int
		cause  string
		detail string
		server string
		text   string
	}{
		{"/registered", 403, "ALREADY_REGISTERED", "", "sip:scscf.ims.example", "ext: 403 ALREADY_REGISTERED"},
		{"/failed", 500, CauseSystemFailure, "database down", "", "ext: 500 SYSTEM_FAILURE"},
		{"/literal", 405, "", "", "", "ext: 405 Method Not Allowed"},
		{"/other", 502, "", "no route", "", "ext: 502 Bad Gateway"},
	}
	for _, c := range cases {
		_, err := extendedAPI.Send(ctx, NewHTTPClient(), Request{Method: http.MethodGet, URI: root + c.path})
		var p *extendedError
		if !errors.As(err, &p) {
			t.Errorf("%s: error %v is not a *ProblemError", c.path, err)
			continue
		}
		if p.Status != c.status || p.Problem.Status != c.status || p.Problem.Cause != c.cause ||
			p.Problem.Detail != c.detail || p.Problem.ServerName != c.server || p.Error() != c.text {
			t.Errorf("%s: %+v, %q", c.path, p, p.Error())
		}
	}
}

func TestNewProblem(t *testing.T) {
	cases := map[string]int{
		"CONTEXT_NOT_FOUND":      404,
		CauseInvalidMsgFormat:    400,
		CauseMandatoryIEMissing:  400,
		CauseMaxJSONSizeExceeded: 413,
		CauseSystemFailure:       500,
		"SOMETHING_ELSE":         500,
	}
	for cause, status := range cases {
		p := plainAPI.NewProblem(cause, "why")
		if p.Status != status || p.Problem.Status != status || p.Problem.Cause != cause || p.Problem.Detail != "why" {
			t.Errorf("NewProblem(%s) = %+v", cause, p)
		}
	}
	if got := (&plainError{Status: 404}).Error(); got != "sbi: 404 Not Found" {
		t.Errorf("a literal says %q", got)
	}
}

// TestReadJSON checks the answers to a request body that cannot be
// read: the media type (415), the size (413, MAX_JSON_SIZE_EXCEEDED)
// and the JSON (400, INVALID_MSG_FORMAT), and the 400 of Missing.
func TestReadJSON(t *testing.T) {
	root := serveH2C(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Name string `json:"name"`
		}
		if p := plainAPI.ReadJSON(w, r, ContentTypeJSON, &v); p != nil {
			plainAPI.WriteError(w, p)
			return
		}
		if v.Name == "" {
			plainAPI.WriteError(w, plainAPI.Missing("/name", "/other"))
			return
		}
		plainAPI.WriteJSON(w, http.StatusOK, v)
	}))
	cases := []struct {
		name, contentType, body string
		status                  int
		want                    ProblemDetails
	}{
		{"media type", "text/plain", `{}`, 415, ProblemDetails{Status: 415, Detail: "the body must be application/json"}},
		{"too large", ContentTypeJSON, `{"name":"` + strings.Repeat("x", MaxBodySize) + `"}`, 413, ProblemDetails{Status: 413, Cause: CauseMaxJSONSizeExceeded}},
		{"not JSON", ContentTypeJSON, `{`, 400, ProblemDetails{Status: 400, Cause: CauseInvalidMsgFormat, Detail: "unexpected EOF"}},
		{"missing", ContentTypeJSON + "; charset=utf-8", `{}`, 400, ProblemDetails{Status: 400, Cause: CauseMandatoryIEMissing, InvalidParams: []InvalidParam{{"/name", "missing"}, {"/other", "missing"}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, root+"/", strings.NewReader(c.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", c.contentType)
			resp, err := NewHTTPClient().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != c.status || resp.Header.Get("Content-Type") != ContentTypeProblem {
				t.Errorf("answered %d as %q", resp.StatusCode, resp.Header.Get("Content-Type"))
			}
			var got ProblemDetails
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}

	req, _ := http.NewRequest(http.MethodPost, root+"/", strings.NewReader(`{"name":"a"}`))
	req.Header.Set("Content-Type", ContentTypeJSON)
	resp, err := NewHTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != ContentTypeJSON || string(b) != `{"name":"a"}` {
		t.Errorf("answered %d %q %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
}
