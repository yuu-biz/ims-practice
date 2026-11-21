package oam

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// The errors of the operations, which the handlers turn into status
// codes.
var (
	// ErrInvalidRequest is a request that the OAM cannot take as it is.
	ErrInvalidRequest = errors.New("invalid request")
	// ErrNotFound is a node, role, subscription, record or backup that
	// does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is an operation that the present state does not
	// allow, such as restoring a backup that did not complete.
	ErrConflict = errors.New("conflict")
	// ErrUnavailable is an operation whose store or HSS the OAM was not
	// configured with.
	ErrUnavailable = errors.New("not configured")
	// ErrUpstream is an answer of the HSS or of a node that the OAM
	// could not use.
	ErrUpstream = errors.New("upstream failed")
)

// SettingsConflictError is a refusal that names each setting in the
// way, as ErrorBody.Settings does for the maintenance API.
type SettingsConflictError struct {
	Message  string
	Settings map[string]string
}

func (e *SettingsConflictError) Error() string { return e.Message }

func (e *SettingsConflictError) Unwrap() error { return ErrConflict }

// maxRequestBody bounds the body of a request other than a stream of
// subscription records.
const maxRequestBody = 64 << 10

// routes is the mux of the API, which also answers a method that a path
// does not take with 405 and Allow, and an unknown path with 404, both
// with an ErrorBody like every other error.
type routes struct {
	mux   *http.ServeMux
	allow map[string][]string
	order []string
}

func newRoutes() *routes {
	return &routes{mux: http.NewServeMux(), allow: map[string][]string{}}
}

func (rt *routes) handle(method, path string, h http.HandlerFunc) {
	rt.mux.HandleFunc(method+" "+path, h)
	if _, seen := rt.allow[path]; !seen {
		rt.order = append(rt.order, path)
	}
	rt.allow[path] = append(rt.allow[path], method)
	if method == http.MethodGet {
		rt.allow[path] = append(rt.allow[path], http.MethodHead)
	}
}

func (rt *routes) handler() http.Handler {
	for _, path := range rt.order {
		allow := strings.Join(slices.Sorted(slices.Values(rt.allow[path])), ", ")
		rt.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeJSON(w, http.StatusMethodNotAllowed, maintenance.ErrorBody{Error: r.Method + " is not allowed on " + r.URL.Path})
		})
	}
	rt.mux.HandleFunc(PathPrefix, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, maintenance.ErrorBody{Error: "no such resource: " + r.URL.Path})
	})
	return rt.mux
}

// readJSON decodes the body of r into v, refusing unknown fields and
// anything after the value. An empty body leaves v as it is when
// optional is set.
func readJSON(w http.ResponseWriter, r *http.Request, v any, optional bool) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if optional && errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("%w: body: %v", ErrInvalidRequest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body: more than one JSON value", ErrInvalidRequest)
	}
	return nil
}

// dryRun reads the dry_run query parameter: absent is false.
func dryRun(r *http.Request) (bool, error) {
	v := r.URL.Query().Get("dry_run")
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%w: dry_run: %q is not true or false", ErrInvalidRequest, v)
	}
	return b, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers an error of the operations with its status.
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	body := maintenance.ErrorBody{Error: err.Error()}
	var conflict *SettingsConflictError
	if errors.As(err, &conflict) {
		body.Settings = conflict.Settings
	}
	switch {
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, maintenance.ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, ErrUpstream):
		status = http.StatusBadGateway
	}
	writeJSON(w, status, body)
}
