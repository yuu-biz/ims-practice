package fault

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/controlapi"
)

// The paths of the API (see the package documentation).
const (
	PathPrefix = "/fault/v1/"
	FaultsPath = PathPrefix + "faults"

	faultPattern = FaultsPath + "/{id}"
)

// FaultPath is the path of one fault.
func FaultPath(id string) string { return FaultsPath + "/" + id }

// maxRequestBody bounds the body of a request; a Request is far
// smaller.
const maxRequestBody = 16 << 10

// ErrorBody is the body of every error. Fault is there when the
// executor failed, so that the caller learns the id of the failed
// fault.
type ErrorBody struct {
	Error string `json:"error"`
	Fault *Fault `json:"fault,omitempty"`
}

// Handler serves the API at the paths of the package documentation.
func (m *Manager) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+FaultsPath, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, m.List())
	})
	mux.HandleFunc("POST "+FaultsPath, func(w http.ResponseWriter, r *http.Request) {
		var req Request
		if err := readJSON(w, r, &req); err != nil {
			writeError(w, Fault{}, err)
			return
		}
		f, err := m.Inject(r.Context(), req)
		if err != nil {
			writeError(w, f, err)
			return
		}
		w.Header().Set("Location", FaultPath(f.ID))
		writeJSON(w, http.StatusCreated, f)
	})
	mux.HandleFunc("GET "+faultPattern, func(w http.ResponseWriter, r *http.Request) {
		f, err := m.Get(r.PathValue("id"))
		if err != nil {
			writeError(w, Fault{}, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})
	mux.HandleFunc("DELETE "+faultPattern, func(w http.ResponseWriter, r *http.Request) {
		f, err := m.Clear(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, f, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	})

	// The patterns without a method take the methods that the ones
	// above do not, so that a 405 has a JSON body like every other
	// error.
	for path, allow := range map[string]string{
		FaultsPath:   "GET, HEAD, POST",
		faultPattern: "GET, HEAD, DELETE",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", allow)
			writeJSON(w, http.StatusMethodNotAllowed, ErrorBody{Error: r.Method + " is not allowed on " + r.URL.Path})
		})
	}
	mux.HandleFunc(PathPrefix, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, ErrorBody{Error: "no such resource: " + r.URL.Path})
	})
	return controlapi.RequireJSON(mux)
}

// readJSON decodes the body of r into v, refusing unknown fields and
// anything after the value.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: body: %v", ErrInvalidRequest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: body: more than one JSON value", ErrInvalidRequest)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeError answers err with its status. f is the fault that the
// executor failed on, if any.
func writeError(w http.ResponseWriter, f Fault, err error) {
	status := http.StatusInternalServerError
	body := ErrorBody{Error: err.Error()}
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status = http.StatusBadRequest
	case errors.Is(err, ErrUnknownFault):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrExecutor):
		body.Fault = &f
	}
	writeJSON(w, status, body)
}
