package openapitest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingT stands in for the *testing.T of a test that uses the
// package, and keeps what it would have reported.
type recordingT struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (r *recordingT) Helper() {}

func (r *recordingT) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// counters is a handler that answers as testdata/counters.yaml says;
// the fields of brokenCounters make it answer otherwise.
type brokenCounters struct {
	extraField       bool
	teapot           bool
	extraRoute       bool
	takesNegatives   bool
	failsOnNegatives bool
	noCacheControl   bool
	noContentType    bool
}

func (b brokenCounters) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /counters/{name}", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case b.teapot:
			w.WriteHeader(http.StatusTeapot)
		case r.PathValue("name") == "missing":
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "no such counter"})
		case b.extraField:
			writeJSON(w, http.StatusOK, map[string]any{"value": 1, "unit": "calls"})
		case b.noContentType:
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"value": 1})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"value": 1})
		}
	})
	mux.HandleFunc("PUT /counters/{name}", func(w http.ResponseWriter, r *http.Request) {
		var c struct {
			Value *int `json:"value"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		err := dec.Decode(&c)
		if err == nil && c.Value != nil && *c.Value < 0 && b.failsOnNegatives {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "the counter went below zero"})
			return
		}
		if err != nil || c.Value == nil || (*c.Value < 0 && !b.takesNegatives) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad counter"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /counters/total", func(w http.ResponseWriter, r *http.Request) {
		if !b.noCacheControl {
			w.Header().Set("Cache-Control", "no-store")
		}
		writeJSON(w, http.StatusOK, map[string]any{"value": 2})
	})
	if b.extraRoute {
		mux.HandleFunc("DELETE /counters/{name}", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		})
		mux.HandleFunc("GET /gauges", func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, []int{})
		})
	}
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// exchanges sends one request of each kind through the checked handler.
func exchanges(t *testing.T, h http.Handler) {
	t.Helper()
	for _, r := range []struct{ method, path, body string }{
		{"GET", "/counters/calls?verbose=true", ""},
		{"GET", "/counters/missing", ""},
		{"PUT", "/counters/calls", `{"value": 3}`},
		{"PUT", "/counters/calls", `{"value": -1}`},
		{"PUT", "/counters/calls", `{"valu": 3}`},
		{"GET", "/counters/total", ""},
		{"GET", "/gauges", ""},
		{"DELETE", "/counters/calls", ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, strings.NewReader(r.body)))
		io.Copy(io.Discard, rec.Body)
	}
}

func TestAHandlerThatFollowsTheDocumentPasses(t *testing.T) {
	d := Load(t, "testdata/counters.yaml")
	h := brokenCounters{}.handler()
	exchanges(t, d.Handler(t, h))
	d.CheckRoutes(t, h)
	d.CheckCovered(t)
}

func TestMismatchesFailTheTest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler brokenCounters
		want    []string
	}{
		{"a field the document lacks", brokenCounters{extraField: true}, []string{"GET /counters/calls?verbose=true answered 200: answer body", "additional properties 'unit' not allowed", `body: "{\"unit\":\"calls\",\"value\":1}\n"`}},
		{"an answer without a Content-Type", brokenCounters{noContentType: true}, []string{"GET /counters/calls?verbose=true answered 200: answer no Content-Type"}},
		{"a refused request that fails", brokenCounters{failsOnNegatives: true}, []string{"PUT /counters/calls answered 500: the handler failed on a request that the document refuses", "minimum: got -1, want 0"}},
		{"a status the document lacks", brokenCounters{teapot: true}, []string{"answered 418: the document lists no such status for GET /counters/{name}"}},
		{"a route the document lacks", brokenCounters{extraRoute: true}, []string{"GET /gauges answered 200: the document has no such path", "DELETE /counters/calls answered 204: the document has no DELETE on this path"}},
		{"a request the document refuses", brokenCounters{takesNegatives: true}, []string{"PUT /counters/calls answered 204: the handler took a request that the document refuses", "minimum: got -1, want 0"}},
		{"a missing header", brokenCounters{noCacheControl: true}, []string{"GET /counters/total answered 200: header Cache-Control is missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Load(t, "testdata/counters.yaml")
			rt := &recordingT{TB: t}
			exchanges(t, d.Handler(rt, tc.handler.handler()))
			got := strings.Join(rt.errors, "\n")
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("the errors do not say %q:\n%s", w, got)
				}
			}
		})
	}
}

func TestCheckRoutesFindsAMethodTheDocumentLacks(t *testing.T) {
	d := Load(t, "testdata/counters.yaml")
	rt := &recordingT{TB: t}
	d.CheckRoutes(rt, brokenCounters{extraRoute: true}.handler())
	want := "/counters/{name}: the handler takes [DELETE GET PUT], the document lists [GET PUT]"
	if got := strings.Join(rt.errors, "\n"); !strings.Contains(got, want) {
		t.Errorf("CheckRoutes reported %q, want %q", got, want)
	}
}

func TestCheckCoveredNamesTheOperationsNotReached(t *testing.T) {
	d := Load(t, "testdata/counters.yaml")
	rec := httptest.NewRecorder()
	d.Handler(t, brokenCounters{}.handler()).ServeHTTP(rec, httptest.NewRequest("GET", "/counters/total", nil))
	rt := &recordingT{TB: t}
	d.CheckCovered(rt)
	got := strings.Join(rt.errors, "\n")
	for _, want := range []string{"no exchange reached GET /counters/{name}", "no exchange reached PUT /counters/{name}"} {
		if !strings.Contains(got, want) {
			t.Errorf("CheckCovered reported %q, want %q", got, want)
		}
	}
	if strings.Contains(got, "/counters/total") {
		t.Errorf("CheckCovered reported the operation that was reached: %q", got)
	}
}

// TestLoadRefusesWhatItDoesNotRead changes the document in one place at
// a time and expects Load to refuse it.
func TestLoadRefusesWhatItDoesNotRead(t *testing.T) {
	raw, err := os.ReadFile("testdata/counters.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, old, new, want string }{
		{"a misspelt keyword", "minimum: 0", "minimun: 0", `keyword "minimun" is not one this package reads`},
		{"an object open to any field", "          minimum: 0\n      additionalProperties: false", "          minimum: 0", "an object schema needs additionalProperties"},
		{"a misspelt field", "operationId: getTotal", "operationID: getTotal", "field operationID not found"},
		{"OpenAPI 3.0", "openapi: 3.1.0", "openapi: 3.0.3", `openapi is "3.0.3", not 3.1.x`},
		{"an optional path parameter", "      in: path\n      required: true", "      in: path", `path parameter "name" must be required`},
		{"a status that is not a code", "'204':", "'2XX':", `response "2XX" is not a status code or default`},
		{"a reference to nothing", "$ref: '#/components/schemas/Counter'\n        '404'", "$ref: '#/components/schemas/Count'\n        '404'", "Count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(string(raw), tc.old, tc.new, 1)
			if changed == string(raw) {
				t.Fatalf("the document has no %q", tc.old)
			}
			file := filepath.Join(t.TempDir(), "openapi.yaml")
			if err := os.WriteFile(file, []byte(changed), 0o600); err != nil {
				t.Fatal(err)
			}
			ft := &fatalT{TB: t}
			func() {
				defer func() {
					if r := recover(); r != nil && r != errFatal {
						panic(r)
					}
				}()
				Load(ft, file)
			}()
			if !strings.Contains(ft.message, tc.want) {
				t.Errorf("Load reported %q, want %q", ft.message, tc.want)
			}
		})
	}
}

var errFatal = new(int)

// fatalT keeps the message of Fatalf and stops Load there.
type fatalT struct {
	testing.TB
	message string
}

func (f *fatalT) Helper() {}

func (f *fatalT) Fatalf(format string, args ...any) {
	f.message = fmt.Sprintf(format, args...)
	panic(errFatal)
}

// TestAnEscapedSegmentIsTheSegment: net/http.ServeMux compares each
// segment of the path unescaped, so the document must too.
func TestAnEscapedSegmentIsTheSegment(t *testing.T) {
	d := Load(t, "testdata/counters.yaml")
	rt := &recordingT{TB: t}
	rec := httptest.NewRecorder()
	d.Handler(rt, brokenCounters{}.handler()).ServeHTTP(rec, httptest.NewRequest("GET", "/counters/tot%61l", nil))
	if rec.Code != http.StatusOK || len(rt.errors) > 0 {
		t.Errorf("GET /counters/tot%%61l = %d, errors %q", rec.Code, rt.errors)
	}
	if _, _, op, _ := d.find("GET", "/counters/tot%61l"); op == nil || op.OperationID != "getTotal" {
		t.Errorf("GET /counters/tot%%61l reaches %+v, want getTotal", op)
	}
}

// TestAHandlerMayAnswerWhileItReads: a handler that answers each line of
// a stream before the next one arrives still can behind Handler, which
// reads the body only as the handler does.
func TestAHandlerMayAnswerWhileItReads(t *testing.T) {
	d := Load(t, "testdata/lines.yaml")
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if err := rc.EnableFullDuplex(); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		sc := bufio.NewScanner(r.Body)
		for sc.Scan() {
			fmt.Fprintf(w, "%s\n", sc.Text())
			rc.Flush()
		}
	})
	server := httptest.NewServer(d.Handler(t, echo))
	body, writer := io.Pipe()
	stop := make(chan struct{})
	// Closing the pipe ends a write that waits and the body of the
	// request, so that the handler and every goroutine here return even
	// when the test stops early.
	t.Cleanup(func() {
		close(stop)
		writer.Close()
		server.Close()
	})
	lines := make(chan string)
	posted := make(chan struct{})
	go func() {
		defer close(posted)
		resp, err := server.Client().Post(server.URL+"/lines", "application/x-ndjson", body)
		if err != nil {
			t.Error(err)
			return
		}
		defer resp.Body.Close()
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-stop:
				return
			}
		}
	}()
	for _, l := range []string{`"one"`, `"two"`} {
		// Each line comes back before the next one is written.
		written := make(chan error, 1)
		go func() {
			_, err := io.WriteString(writer, l+"\n")
			written <- err
		}()
		select {
		case got := <-lines:
			if got != l {
				t.Fatalf("echo %q, want %q", got, l)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("no echo of %s within 10 s", l)
		}
		if err := <-written; err != nil {
			t.Fatalf("writing %s: %v", l, err)
		}
	}
	writer.Close()
	select {
	case <-posted:
	case <-time.After(10 * time.Second):
		t.Fatal("the answer did not end within 10 s of the end of the request body")
	}
}

// TestEachLineOfAnNDJSONBodyIsChecked: lines.yaml gives each line of
// its NDJSON bodies the schema of a string, so a line that is a number
// fails the request and the answer, and an empty line is skipped.
func TestEachLineOfAnNDJSONBodyIsChecked(t *testing.T) {
	d := Load(t, "testdata/lines.yaml")
	for _, tc := range []struct {
		name, request, answer string
		want                  []string
	}{
		{"lines that match", "\"one\"\n\n\"two\"\n", "\"one\"\n\n\"two\"\n", nil},
		{"an answer line that does not", "\"one\"\n", "\"one\"\n3\n", []string{"answer body: line 2:", `body: "3"`}},
		{"a request line that does not", "\"one\"\n{}\n", "\"one\"\n", []string{"the handler took a request that the document refuses: request body: line 2:"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := &recordingT{TB: t}
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "application/x-ndjson")
				io.WriteString(w, tc.answer)
			})
			req := httptest.NewRequest("POST", "/lines", strings.NewReader(tc.request))
			req.Header.Set("Content-Type", "application/x-ndjson")
			d.Handler(rt, h).ServeHTTP(httptest.NewRecorder(), req)
			got := strings.Join(rt.errors, "\n")
			if len(tc.want) == 0 && got != "" {
				t.Errorf("errors %q", got)
			}
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("the errors do not say %q:\n%s", w, got)
				}
			}
		})
	}
}

// TestARefIntoAnotherDocument: sums.yaml answers with the Counter of
// counters.yaml, which the check reads from that file.
func TestARefIntoAnotherDocument(t *testing.T) {
	d := Load(t, "testdata/sums.yaml")
	for _, tc := range []struct {
		value string
		ok    bool
	}{{`{"value": 2}`, true}, {`{"value": -2}`, false}} {
		rt := &recordingT{TB: t}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, tc.value)
		})
		d.Handler(rt, h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/counters/total", nil))
		if got := len(rt.errors) == 0; got != tc.ok {
			t.Errorf("answer %s: errors %q", tc.value, rt.errors)
		}
	}
}

// TestAnOpenLineSchemaLeavesTheLines: a request whose lines have a
// schema that says nothing may carry lines that are not even JSON, as
// the bulk provisioning of the OAM takes them and reports each.
func TestAnOpenLineSchemaLeavesTheLines(t *testing.T) {
	raw, err := os.ReadFile("testdata/lines.yaml")
	if err != nil {
		t.Fatal(err)
	}
	open := strings.Replace(string(raw), "          application/x-ndjson:\n            schema:\n              type: string\n",
		"          application/x-ndjson:\n            schema:\n              description: Any line.\n", 1)
	file := filepath.Join(t.TempDir(), "lines.yaml")
	if err := os.WriteFile(file, []byte(open), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file   string
		errors bool
	}{{"testdata/lines.yaml", true}, {file, false}} {
		d := Load(t, tc.file)
		rt := &recordingT{TB: t}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			w.Header().Set("Content-Type", "application/x-ndjson")
			io.WriteString(w, "\"rejected\"\n")
		})
		req := httptest.NewRequest("POST", "/lines", strings.NewReader("not json\n"))
		req.Header.Set("Content-Type", "application/x-ndjson")
		d.Handler(rt, h).ServeHTTP(httptest.NewRecorder(), req)
		if got := len(rt.errors) > 0; got != tc.errors {
			t.Errorf("%s: errors %q", tc.file, rt.errors)
		}
	}
}

// TestADefaultAnswerTakesAnyStatus: the document gives the PUT of
// counters a default answer, which stands for the statuses it does not
// list.
func TestADefaultAnswerTakesAnyStatus(t *testing.T) {
	raw, err := os.ReadFile("testdata/counters.yaml")
	if err != nil {
		t.Fatal(err)
	}
	withDefault := strings.Replace(string(raw), "        '204':\n          description: The counter is set.\n",
		"        '204':\n          description: The counter is set.\n        default:\n          description: Any other answer.\n", 1)
	file := filepath.Join(t.TempDir(), "counters.yaml")
	if err := os.WriteFile(file, []byte(withDefault), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		file   string
		errors bool
	}{{"testdata/counters.yaml", true}, {file, false}} {
		d := Load(t, tc.file)
		rt := &recordingT{TB: t}
		teapot := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
		d.Handler(rt, teapot).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("PUT", "/counters/calls", strings.NewReader(`{"value": 1}`)))
		if got := len(rt.errors) > 0; got != tc.errors {
			t.Errorf("%s: errors %q", tc.file, rt.errors)
		}
	}
}

// TestAMediaTypeRange: an answer whose content is */* takes any
// Content-Type, and none; text/* takes text/plain and not
// application/json.
func TestAMediaTypeRange(t *testing.T) {
	doc := `openapi: 3.1.0
info: {title: Ranges, version: 1.0.0}
paths:
  /any:
    get:
      operationId: getAny
      responses:
        '200': {description: Anything., content: {'*/*': {}}}
  /text:
    get:
      operationId: getText
      responses:
        '200': {description: Some text., content: {'text/*': {}}}
`
	file := filepath.Join(t.TempDir(), "ranges.yaml")
	if err := os.WriteFile(file, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	d := Load(t, file)
	for _, tc := range []struct {
		path, contentType string
		ok                bool
	}{
		{"/any", "", true},
		{"/any", "application/xml", true},
		{"/text", "text/plain; charset=utf-8", true},
		{"/text", "application/json", false},
	} {
		rt := &recordingT{TB: t}
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.contentType != "" {
				w.Header().Set("Content-Type", tc.contentType)
			}
			w.WriteHeader(http.StatusOK)
		})
		d.Handler(rt, h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", tc.path, nil))
		if got := len(rt.errors) == 0; got != tc.ok {
			t.Errorf("%s with %q: errors %q", tc.path, tc.contentType, rt.errors)
		}
	}
}

// TestAWideRouteMayRefuseWith405: a handler with a pattern as wide as
// GET / answers 405 to a POST on a path that the document lacks, which
// is a refusal as much as 404 is.
func TestAWideRouteMayRefuseWith405(t *testing.T) {
	d := Load(t, "testdata/counters.yaml")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.NotFoundHandler())
	rt := &recordingT{TB: t}
	h := d.Handler(rt, mux)
	for _, r := range []struct{ method, path string }{{"POST", "/gauges"}, {"GET", "/gauges"}, {"POST", "/counters/total"}} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, nil))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want a refusal", r.method, r.path, rec.Code)
		}
	}
	if len(rt.errors) > 0 {
		t.Errorf("errors %q", rt.errors)
	}
}

func TestJSONNames(t *testing.T) {
	type inner struct {
		Cause string `json:"cause,omitempty"`
	}
	type outer struct {
		inner
		Status  int    `json:"status"`
		Secret  string `json:"-"`
		Plain   string
		private string
	}
	got := strings.Join(JSONNames(outer{}), " ")
	if want := "cause status Plain"; got != want {
		t.Errorf("JSONNames = %q, want %q", got, want)
	}
}
