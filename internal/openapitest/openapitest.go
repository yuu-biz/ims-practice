// Package openapitest checks an HTTP API against its OpenAPI 3.1
// document: that the handler serves the paths and methods that the
// document lists and no others, and that every request it takes and
// every answer it gives has the form the document describes.
// Only tests use it: no binary in cmd builds it in.
//
// A test loads the document that sits next to the code (Load), puts
// the handler behind Document.Handler, and sends its requests as it
// would to the handler itself. Every exchange is then checked:
//
//   - The route: a path that the document does not have must answer 404
//     and a method that a path does not take 405. Any other answer is a
//     route that the handler serves and the document lacks.
//   - The request: its path and query parameters, and its body, which
//     must match the schema of the operation. A request that does not
//     match must be refused with a 4xx; the handler taking it, or
//     failing on it with a 5xx, is a mismatch.
//   - The answer: its status code must be one that the operation lists,
//     and the handler must set a Content-Type that is one of that
//     answer's media types whenever it writes a body. A JSON body must
//     match the schema, and the headers marked required must be there.
//
// A body of application/x-ndjson, one JSON value per line, is checked
// line by line: each line that is not empty must match the schema of
// the media type. OpenAPI 3.1 has no schema for the items of a
// sequence, so the documents here give that of a line.
//
// Document.CheckRoutes asks the handler which methods each path of the
// document takes, and Document.CheckCovered names each operation that
// no exchange reached, so that a test cannot pass by leaving one out.
//
// The schemas are JSON Schema 2020-12, on which the Schema Object of
// the OpenAPI Specification 3.1.0 builds, and are checked by
// github.com/santhosh-tekuri/jsonschema/v6. This package reads only the
// part of an OpenAPI document that the APIs here use, and refuses a
// document with anything else in it, so that a misspelt keyword fails
// the test instead of checking nothing:
//
//   - Path templates whose parameters are whole segments, as in
//     /contexts/{id}, which is also how net/http.ServeMux writes them.
//   - Parameters in the path and the query, written in place or as a
//     $ref to components/parameters.
//   - Request bodies and answers, written in place or, for answers, as a
//     $ref to components/responses, with the headers of an answer.
//   - In a schema, the keywords in schemaKeywords. Every object schema
//     says what it does with the properties that it does not list
//     (additionalProperties), so that a field the handler adds and the
//     document lacks is a mismatch too.
package openapitest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"go.yaml.in/yaml/v3"
)

// The parts of an OpenAPI 3.1 document that this package reads, with
// the names of the objects and fields of the OpenAPI Specification
// 3.1.0.
type (
	openAPI struct {
		OpenAPI    string              `yaml:"openapi"`
		Info       info                `yaml:"info"`
		Paths      map[string]pathItem `yaml:"paths"`
		Components components          `yaml:"components"`
	}
	info struct {
		Title       string `yaml:"title"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	}
	components struct {
		Schemas    map[string]any       `yaml:"schemas"`
		Responses  map[string]response  `yaml:"responses"`
		Parameters map[string]parameter `yaml:"parameters"`
	}
	pathItem struct {
		Summary     string     `yaml:"summary"`
		Description string     `yaml:"description"`
		Get         *operation `yaml:"get"`
		Put         *operation `yaml:"put"`
		Post        *operation `yaml:"post"`
		Delete      *operation `yaml:"delete"`
		Patch       *operation `yaml:"patch"`
	}
	operation struct {
		OperationID string              `yaml:"operationId"`
		Summary     string              `yaml:"summary"`
		Description string              `yaml:"description"`
		Tags        []string            `yaml:"tags"`
		Parameters  []parameter         `yaml:"parameters"`
		RequestBody *requestBody        `yaml:"requestBody"`
		Responses   map[string]response `yaml:"responses"`
	}
	parameter struct {
		Ref         string `yaml:"$ref"`
		Name        string `yaml:"name"`
		In          string `yaml:"in"`
		Description string `yaml:"description"`
		Required    bool   `yaml:"required"`
		Schema      any    `yaml:"schema"`
	}
	requestBody struct {
		Description string               `yaml:"description"`
		Required    bool                 `yaml:"required"`
		Content     map[string]mediaType `yaml:"content"`
	}
	response struct {
		Ref         string               `yaml:"$ref"`
		Description string               `yaml:"description"`
		Headers     map[string]header    `yaml:"headers"`
		Content     map[string]mediaType `yaml:"content"`
	}
	header struct {
		Description string `yaml:"description"`
		Required    bool   `yaml:"required"`
		Schema      any    `yaml:"schema"`
	}
	mediaType struct {
		Schema any `yaml:"schema"`
	}
)

// schemaKeywords are the keywords of JSON Schema 2020-12 that a schema
// here may use. The values are the keywords whose value holds
// subschemas, which the check of the keywords walks into.
var schemaKeywords = map[string]string{
	"$ref":                 "",
	"type":                 "",
	"enum":                 "",
	"const":                "",
	"format":               "",
	"title":                "",
	"description":          "",
	"default":              "",
	"examples":             "",
	"deprecated":           "",
	"readOnly":             "",
	"writeOnly":            "",
	"minimum":              "",
	"maximum":              "",
	"exclusiveMinimum":     "",
	"exclusiveMaximum":     "",
	"minLength":            "",
	"maxLength":            "",
	"pattern":              "",
	"minItems":             "",
	"maxItems":             "",
	"uniqueItems":          "",
	"minProperties":        "",
	"maxProperties":        "",
	"required":             "",
	"properties":           "map",
	"additionalProperties": "schema",
	"propertyNames":        "schema",
	"items":                "schema",
	"not":                  "schema",
	"oneOf":                "list",
	"anyOf":                "list",
	"allOf":                "list",
}

// methods are the HTTP methods that a path item may take here.
var methods = []string{http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete, http.MethodPatch}

func (p pathItem) operation(method string) *operation {
	switch method {
	case http.MethodGet, http.MethodHead:
		return p.Get
	case http.MethodPut:
		return p.Put
	case http.MethodPost:
		return p.Post
	case http.MethodDelete:
		return p.Delete
	case http.MethodPatch:
		return p.Patch
	}
	return nil
}

// Document is an OpenAPI document, read and checked, with the
// operations that the exchanges so far have reached.
type Document struct {
	file    string
	doc     openAPI
	paths   []pathTemplate
	schemas map[string]*jsonschema.Schema

	mu      sync.Mutex
	reached map[string]bool
}

// pathTemplate is one path of the document, split into segments; a
// segment in braces is a parameter.
type pathTemplate struct {
	path     string
	segments []string
}

// match reports whether the escaped path p is an instance of the
// template, with the values of its parameters.
func (pt pathTemplate) match(p string) (map[string]string, bool) {
	segments := strings.Split(p, "/")
	if len(segments) != len(pt.segments) {
		return nil, false
	}
	values := map[string]string{}
	for i, s := range pt.segments {
		// Each segment is compared unescaped, as net/http.ServeMux does:
		// /subscriptions/ex%70ort is /subscriptions/export.
		v, err := url.PathUnescape(segments[i])
		if err != nil {
			return nil, false
		}
		if name, ok := parameterName(s); ok {
			if v == "" {
				return nil, false
			}
			values[name] = v
			continue
		}
		if s != v {
			return nil, false
		}
	}
	return values, true
}

// literals counts the segments that are not parameters. Of two
// templates that both match a path, the one with more literal segments
// is the more specific: a concrete path wins over a templated one
// (OpenAPI Specification 3.1.0, Paths Object), as net/http.ServeMux
// picks it too.
func (pt pathTemplate) literals() int {
	n := 0
	for _, s := range pt.segments {
		if _, ok := parameterName(s); !ok {
			n++
		}
	}
	return n
}

func parameterName(segment string) (string, bool) {
	if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
		return segment[1 : len(segment)-1], true
	}
	return "", false
}

// documentURL is the name under which the schemas of a document are
// compiled; every $ref in them is relative to it.
const documentURL = "urn:openapitest:document"

// Load reads the OpenAPI 3.1 document in file and checks it. It fails t
// for a document that does not parse, that has something this package
// does not read, or whose schemas do not compile.
func Load(t testing.TB, file string) *Document {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("OpenAPI document: %v", err)
	}
	d := &Document{file: file, schemas: map[string]*jsonschema.Schema{}, reached: map[string]bool{}}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d.doc); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	var tree any
	if err := yaml.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if err := d.check(); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	if err := d.compile(tree); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return d
}

// check refuses what this package does not read, and what the OpenAPI
// Specification 3.1.0 does not allow, such as a path parameter that is
// not required (Parameter Object).
func (d *Document) check() error {
	if !strings.HasPrefix(d.doc.OpenAPI, "3.1.") {
		return fmt.Errorf("openapi is %q, not 3.1.x", d.doc.OpenAPI)
	}
	if d.doc.Info.Title == "" || d.doc.Info.Version == "" {
		return fmt.Errorf("info needs a title and a version")
	}
	ids := map[string]bool{}
	for path, item := range d.doc.Paths {
		if !strings.HasPrefix(path, "/") {
			return fmt.Errorf("path %q does not start with /", path)
		}
		pt := pathTemplate{path: path, segments: strings.Split(path, "/")}
		d.paths = append(d.paths, pt)
		var inPath []string
		for _, s := range pt.segments {
			if name, ok := parameterName(s); ok {
				inPath = append(inPath, name)
			} else if strings.ContainsAny(s, "{}") {
				return fmt.Errorf("path %q: a parameter must be a whole segment", path)
			}
		}
		n := 0
		for _, method := range methods {
			op := item.operation(method)
			if op == nil {
				continue
			}
			n++
			where := method + " " + path
			if op.OperationID == "" || ids[op.OperationID] {
				return fmt.Errorf("%s: operationId %q is empty or not unique", where, op.OperationID)
			}
			ids[op.OperationID] = true
			var declared []string
			for _, p := range op.Parameters {
				p, err := d.parameter(p)
				if err != nil {
					return fmt.Errorf("%s: %v", where, err)
				}
				switch p.In {
				case "path":
					if !p.Required {
						return fmt.Errorf("%s: path parameter %q must be required", where, p.Name)
					}
					declared = append(declared, p.Name)
				case "query":
				default:
					return fmt.Errorf("%s: parameter %q is in %q; only path and query are read", where, p.Name, p.In)
				}
				if p.Schema == nil {
					return fmt.Errorf("%s: parameter %q has no schema", where, p.Name)
				}
			}
			slices.Sort(declared)
			if want := slices.Sorted(slices.Values(inPath)); !slices.Equal(declared, want) {
				return fmt.Errorf("%s: declares the path parameters %v, the path has %v", where, declared, want)
			}
			if b := op.RequestBody; b != nil && len(b.Content) == 0 {
				return fmt.Errorf("%s: requestBody has no content", where)
			}
			if len(op.Responses) == 0 {
				return fmt.Errorf("%s: no responses", where)
			}
			for code, r := range op.Responses {
				if _, err := strconv.Atoi(code); err != nil || len(code) != 3 {
					return fmt.Errorf("%s: response %q is not a status code", where, code)
				}
				r, err := d.response(r)
				if err != nil {
					return fmt.Errorf("%s %s: %v", where, code, err)
				}
				if r.Description == "" {
					return fmt.Errorf("%s %s: response has no description", where, code)
				}
			}
		}
		if n == 0 {
			return fmt.Errorf("path %q has no operation", path)
		}
	}
	return nil
}

func (d *Document) parameter(p parameter) (parameter, error) {
	if p.Ref == "" {
		return p, nil
	}
	name, ok := strings.CutPrefix(p.Ref, "#/components/parameters/")
	if !ok {
		return p, fmt.Errorf("$ref %q is not to components/parameters", p.Ref)
	}
	q, ok := d.doc.Components.Parameters[name]
	if !ok {
		return p, fmt.Errorf("$ref %q: no such parameter", p.Ref)
	}
	return q, nil
}

func (d *Document) response(r response) (response, error) {
	if r.Ref == "" {
		return r, nil
	}
	name, ok := strings.CutPrefix(r.Ref, "#/components/responses/")
	if !ok {
		return r, fmt.Errorf("$ref %q is not to components/responses", r.Ref)
	}
	q, ok := d.doc.Components.Responses[name]
	if !ok {
		return r, fmt.Errorf("$ref %q: no such response", r.Ref)
	}
	return q, nil
}

// compile checks the keywords of every schema of the document and
// compiles it, under the JSON pointer to where it is.
func (d *Document) compile(tree any) error {
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.AssertFormat()
	doc, err := toJSON(tree)
	if err != nil {
		return err
	}
	if err := c.AddResource(documentURL, doc); err != nil {
		return err
	}
	add := func(pointer string, schema any) error {
		if err := checkKeywords(pointer, schema); err != nil {
			return err
		}
		s, err := c.Compile(documentURL + "#" + pointer)
		if err != nil {
			return fmt.Errorf("%s: %v", pointer, err)
		}
		d.schemas[pointer] = s
		return nil
	}
	for name, s := range d.doc.Components.Schemas {
		if err := add("/components/schemas/"+escape(name), s); err != nil {
			return err
		}
	}
	for name, p := range d.doc.Components.Parameters {
		if err := add("/components/parameters/"+escape(name)+"/schema", p.Schema); err != nil {
			return err
		}
	}
	for name, r := range d.doc.Components.Responses {
		if err := addResponse(add, "/components/responses/"+escape(name), r); err != nil {
			return err
		}
	}
	for path, item := range d.doc.Paths {
		for _, method := range methods {
			op := item.operation(method)
			if op == nil {
				continue
			}
			base := "/paths/" + escape(path) + "/" + strings.ToLower(method)
			for i, p := range op.Parameters {
				if p.Ref == "" {
					if err := add(base+"/parameters/"+strconv.Itoa(i)+"/schema", p.Schema); err != nil {
						return err
					}
				}
			}
			if b := op.RequestBody; b != nil {
				for mt, m := range b.Content {
					if err := add(base+"/requestBody/content/"+escape(mt)+"/schema", m.Schema); err != nil {
						return err
					}
				}
			}
			for code, r := range op.Responses {
				if r.Ref == "" {
					if err := addResponse(add, base+"/responses/"+code, r); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func addResponse(add func(string, any) error, pointer string, r response) error {
	for name, h := range r.Headers {
		if err := add(pointer+"/headers/"+escape(name)+"/schema", h.Schema); err != nil {
			return err
		}
	}
	for mt, m := range r.Content {
		if m.Schema == nil {
			continue
		}
		if err := add(pointer+"/content/"+escape(mt)+"/schema", m.Schema); err != nil {
			return err
		}
	}
	return nil
}

// checkKeywords refuses a keyword that is not in schemaKeywords, and an
// object schema that does not say what it does with the properties it
// does not list.
func checkKeywords(pointer string, schema any) error {
	switch s := schema.(type) {
	case bool:
		return nil
	case map[string]any:
		for k, v := range s {
			kind, ok := schemaKeywords[k]
			if !ok {
				return fmt.Errorf("%s: keyword %q is not one this package reads", pointer, k)
			}
			switch kind {
			case "schema":
				if err := checkKeywords(pointer+"/"+k, v); err != nil {
					return err
				}
			case "map":
				m, ok := v.(map[string]any)
				if !ok {
					return fmt.Errorf("%s/%s is not a map", pointer, k)
				}
				for name, sub := range m {
					if err := checkKeywords(pointer+"/"+k+"/"+escape(name), sub); err != nil {
						return err
					}
				}
			case "list":
				l, ok := v.([]any)
				if !ok {
					return fmt.Errorf("%s/%s is not a list", pointer, k)
				}
				for i, sub := range l {
					if err := checkKeywords(pointer+"/"+k+"/"+strconv.Itoa(i), sub); err != nil {
						return err
					}
				}
			}
		}
		if isObject(s) {
			if _, ok := s["additionalProperties"]; !ok {
				return fmt.Errorf("%s: an object schema needs additionalProperties", pointer)
			}
		}
		return nil
	case nil:
		return fmt.Errorf("%s: no schema", pointer)
	}
	return fmt.Errorf("%s: a schema is a map or a boolean", pointer)
}

func isObject(s map[string]any) bool {
	if _, ok := s["properties"]; ok {
		return true
	}
	switch t := s["type"].(type) {
	case string:
		return t == "object"
	case []any:
		return slices.Contains(t, any("object"))
	}
	return false
}

// escape writes a name as a token of a JSON pointer (RFC 6901 3).
func escape(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
}

// toJSON turns what the YAML decoder gives into what the schema
// compiler reads: the values of a JSON document.
func toJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(b))
}

// find gives the operation that a request with the method and the
// escaped path p reaches: of the path templates that p is an instance
// of and that take the method, the most specific, as
// net/http.ServeMux picks its pattern. matched tells whether p is an
// instance of any template at all.
func (d *Document) find(method, p string) (pt pathTemplate, values map[string]string, op *operation, matched bool) {
	for _, candidate := range d.paths {
		v, ok := candidate.match(p)
		if !ok {
			continue
		}
		matched = true
		o := d.doc.Paths[candidate.path].operation(method)
		if o != nil && (op == nil || candidate.literals() > pt.literals()) {
			pt, values, op = candidate, v, o
		}
	}
	return pt, values, op, matched
}

// methodsOf gives the methods that the document takes on the escaped
// path p, from every template that p is an instance of.
func (d *Document) methodsOf(p string) []string {
	var taken []string
	for _, m := range methods {
		if _, _, op, _ := d.find(m, p); op != nil {
			taken = append(taken, m)
		}
	}
	return taken
}

// Handler returns h with every exchange checked against the document,
// as the package documentation describes. A mismatch fails t.
func (d *Document) Handler(t testing.TB, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body is copied as the handler reads it, so that a handler
		// that answers a stream while it reads one still can. What it
		// leaves unread is read afterwards, so that the whole body is
		// checked.
		var body bytes.Buffer
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, &body), r.Body}
		rec := &recorder{ResponseWriter: w}
		h.ServeHTTP(rec, r)
		_, _ = io.Copy(io.Discard, r.Body)
		if !rec.wroteHeader {
			rec.status = http.StatusOK
			rec.contentType = rec.Header().Get("Content-Type")
		}
		for _, problem := range d.exchange(r, body.Bytes(), rec) {
			t.Errorf("%s: %s %s answered %d: %s", d.file, r.Method, r.URL, rec.status, problem)
		}
	})
}

// recorder passes the answer on to the client and keeps a copy to
// check. It flushes, so that a stream of events reaches the client as
// it is written.
type recorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	// contentType is the Content-Type that the handler set before the
	// header went out. The ResponseWriter below may set one of its own
	// afterwards, from the first octets of the body, which is not what
	// the handler said.
	contentType string
	body        bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	if !r.wroteHeader {
		r.status, r.wroteHeader = status, true
		r.contentType = r.ResponseWriter.Header().Get("Content-Type")
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	r.body.Write(p)
	return r.ResponseWriter.Write(p)
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// exchange gives what is wrong with one request and its answer.
func (d *Document) exchange(r *http.Request, body []byte, rec *recorder) []string {
	pt, pathValues, op, matched := d.find(r.Method, r.URL.EscapedPath())
	switch {
	case !matched && rec.status != http.StatusNotFound:
		return []string{"the document has no such path"}
	case !matched:
		return nil
	case op == nil && rec.status != http.StatusMethodNotAllowed:
		return []string{"the document has no " + r.Method + " on this path"}
	case op == nil:
		return nil
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	base := "/paths/" + escape(pt.path) + "/" + strings.ToLower(method)
	refused := d.request(r, body, op, base, pathValues)
	var problems []string
	switch {
	case rec.status < 400:
		for _, p := range refused {
			problems = append(problems, "the handler took a request that the document refuses: "+p)
		}
	case rec.status >= 500:
		for _, p := range refused {
			problems = append(problems, "the handler failed on a request that the document refuses, instead of refusing it with a 4xx: "+p)
		}
	}
	resp, ok := op.Responses[strconv.Itoa(rec.status)]
	if !ok {
		return append(problems, "the document lists no such status for "+method+" "+pt.path)
	}
	d.mu.Lock()
	d.reached[method+" "+pt.path] = true
	d.mu.Unlock()
	pointer := base + "/responses/" + strconv.Itoa(rec.status)
	if resp.Ref != "" {
		pointer = "/components/responses/" + strings.TrimPrefix(resp.Ref, "#/components/responses/")
		resp, _ = d.response(resp)
	}
	return append(problems, d.answer(rec, r.Method == http.MethodHead, resp, pointer)...)
}

// request gives what in r the operation does not allow.
func (d *Document) request(r *http.Request, body []byte, op *operation, base string, pathValues map[string]string) []string {
	var problems []string
	query := r.URL.Query()
	known := map[string]bool{}
	for i, p := range op.Parameters {
		pointer := base + "/parameters/" + strconv.Itoa(i) + "/schema"
		if p.Ref != "" {
			pointer = "/components/parameters/" + strings.TrimPrefix(p.Ref, "#/components/parameters/") + "/schema"
			p, _ = d.parameter(p)
		}
		var values []string
		switch p.In {
		case "path":
			values = []string{pathValues[p.Name]}
		case "query":
			known[p.Name] = true
			values = query[p.Name]
			if len(values) == 0 && p.Required {
				problems = append(problems, "query parameter "+p.Name+" is missing")
			}
		}
		for _, v := range values {
			if err := d.parameterValue(pointer, v); err != nil {
				problems = append(problems, fmt.Sprintf("%s parameter %s=%q: %v", p.In, p.Name, v, err))
			}
		}
	}
	for name := range query {
		if !known[name] {
			problems = append(problems, "query parameter "+name+" is not in the document")
		}
	}
	b := op.RequestBody
	switch {
	case b == nil && len(body) > 0:
		problems = append(problems, "the operation takes no body")
	case b == nil:
	case len(body) == 0:
		if b.Required {
			problems = append(problems, "the body is missing")
		}
	default:
		mt, err := mediaTypeOf(r.Header.Get("Content-Type"), b.Content, true)
		if err != nil {
			return append(problems, "request "+err.Error())
		}
		if err := d.validateBody(base+"/requestBody/content/"+escape(mt)+"/schema", mt, body); err != nil {
			problems = append(problems, "request body: "+err.Error())
		}
	}
	return problems
}

// parameterValue checks a parameter, which is text on the wire,
// against its schema: as a string, or as the boolean or number it
// spells.
func (d *Document) parameterValue(pointer, v string) error {
	s := d.schemas[pointer]
	err := s.Validate(v)
	if err == nil {
		return nil
	}
	if b, perr := strconv.ParseBool(v); perr == nil && s.Validate(b) == nil {
		return nil
	}
	if _, perr := strconv.ParseFloat(v, 64); perr == nil && s.Validate(json.Number(v)) == nil {
		return nil
	}
	return err
}

// answer gives what in the answer the document does not describe.
func (d *Document) answer(rec *recorder, head bool, resp response, pointer string) []string {
	var problems []string
	for name, h := range resp.Headers {
		if h.Required && rec.Header().Get(name) == "" {
			problems = append(problems, "header "+name+" is missing")
		}
	}
	if len(resp.Content) == 0 {
		if rec.body.Len() > 0 {
			problems = append(problems, "the document gives this answer no body, the handler wrote "+strconv.Quote(rec.body.String()))
		}
		return problems
	}
	if head {
		return problems
	}
	mt, err := mediaTypeOf(rec.contentType, resp.Content, false)
	if err != nil {
		return append(problems, "answer "+err.Error())
	}
	if resp.Content[mt].Schema == nil {
		return problems
	}
	if err := d.validateBody(pointer+"/content/"+escape(mt)+"/schema", mt, rec.body.Bytes()); err != nil {
		problems = append(problems, "answer body: "+err.Error())
	}
	return problems
}

// mediaTypeOf gives the media type of a Content-Type, which must be one
// that content lists. When fill is set, a message without one is taken
// to be of the only media type there is: a client often sends a body
// without saying what it is, and the handlers here read it all the
// same. An answer always says.
func mediaTypeOf(contentType string, content map[string]mediaType, fill bool) (string, error) {
	if contentType == "" && fill && len(content) == 1 {
		for mt := range content {
			return mt, nil
		}
	}
	if contentType == "" {
		return "", fmt.Errorf("no Content-Type, where the document has %v", slices.Sorted(maps.Keys(content)))
	}
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("Content-Type %q: %v", contentType, err)
	}
	if _, ok := content[mt]; !ok {
		return "", fmt.Errorf("Content-Type %s is not one of %v", mt, slices.Sorted(maps.Keys(content)))
	}
	return mt, nil
}

// ndjson is the media type of JSON text with one value per line.
const ndjson = "application/x-ndjson"

// validateBody checks a body against the schema at pointer: a JSON body
// as one value, an NDJSON body line by line. A body of another media
// type is not checked.
func (d *Document) validateBody(pointer, mt string, body []byte) error {
	switch {
	case mt == "application/json" || strings.HasSuffix(mt, "+json"):
		return d.validateValue(pointer, body)
	case mt == ndjson:
		for i, line := range bytes.Split(body, []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			if err := d.validateValue(pointer, line); err != nil {
				return fmt.Errorf("line %d: %w", i+1, err)
			}
		}
	}
	return nil
}

// validateValue checks one JSON value against the schema at pointer,
// and quotes the start of the value when it does not match.
func (d *Document) validateValue(pointer string, b []byte) error {
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("not JSON: %v\n%s", err, excerpt(b))
	}
	if err := d.schemas[pointer].Validate(v); err != nil {
		return fmt.Errorf("%v\n%s", err, excerpt(b))
	}
	return nil
}

// excerptLength bounds how much of a body a mismatch quotes.
const excerptLength = 500

func excerpt(b []byte) string {
	if len(b) <= excerptLength {
		return "body: " + strconv.Quote(string(b))
	}
	return "body (first " + strconv.Itoa(excerptLength) + " octets): " + strconv.Quote(string(b[:excerptLength]))
}

// CheckRoutes asks h which methods each path of the document takes,
// and fails t for a path whose methods differ from the document's. A
// path that is an instance of more than one template, such as
// /counters/total of /counters/{name}, takes the methods of them all. It
// sends OPTIONS, which no API here takes, so that the handler answers
// 405 with an Allow header (RFC 9110 15.5.6), as net/http.ServeMux
// does for a path that it serves with other methods.
func (d *Document) CheckRoutes(t testing.TB, h http.Handler) {
	t.Helper()
	for _, pt := range d.paths {
		var segments []string
		for _, s := range pt.segments {
			if _, ok := parameterName(s); ok {
				s = "x"
			}
			segments = append(segments, s)
		}
		p := strings.Join(segments, "/")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodOptions, p, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: OPTIONS %s answered %d, not 405 with the methods of the path", d.file, p, rec.Code)
			continue
		}
		var allowed []string
		for _, m := range strings.Split(rec.Header().Get("Allow"), ",") {
			m = strings.TrimSpace(m)
			if m != "" && m != http.MethodHead && m != http.MethodOptions {
				allowed = append(allowed, m)
			}
		}
		slices.Sort(allowed)
		documented := d.methodsOf(p)
		slices.Sort(documented)
		if !slices.Equal(allowed, documented) {
			t.Errorf("%s: %s: the handler takes %v, the document lists %v", d.file, pt.path, allowed, documented)
		}
	}
}

// CheckCovered fails t for each operation of the document that no
// exchange through Handler has reached with a status that the
// operation lists.
func (d *Document) CheckCovered(t testing.TB) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	var missing []string
	for _, pt := range d.paths {
		item := d.doc.Paths[pt.path]
		for _, m := range methods {
			if item.operation(m) != nil && !d.reached[m+" "+pt.path] {
				missing = append(missing, m+" "+pt.path)
			}
		}
	}
	slices.Sort(missing)
	for _, m := range missing {
		t.Errorf("%s: no exchange reached %s", d.file, m)
	}
}
