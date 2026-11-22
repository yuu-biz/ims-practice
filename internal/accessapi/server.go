package accessapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxRequestBodySize = 64 << 10

// Router serves the UE's paths of one stub. The stub gives it the error
// that marks a message it refuses, and the statuses of its own errors,
// so that the UE tells the cases apart.
type Router struct {
	*http.ServeMux
	invalidMessage error
	status         func(error) int
}

// NewRouter returns the Router of a stub. A body that cannot be read or
// decoded fails with invalidMessage. status maps an error of the stub to
// its HTTP status; 0 means it does not know the error, which is 500.
func NewRouter(invalidMessage error, status func(error) int) *Router {
	return &Router{ServeMux: http.NewServeMux(), invalidMessage: invalidMessage, status: status}
}

// HandleMessage serves the message of type t that a UE posts. The body is decoded
// as M, and unknown fields are errors, so a misspelt information element
// is caught. The answer goes out with 200, or 204 when serve returns nil.
func HandleMessage[M any](r *Router, t MessageType, serve func(ctx context.Context, imsi string, m M) (any, error)) {
	r.HandleFunc("POST "+Path("{imsi}", t), func(w http.ResponseWriter, req *http.Request) {
		req.Body = http.MaxBytesReader(w, req.Body, maxRequestBodySize)
		body, err := io.ReadAll(req.Body)
		if err != nil {
			r.WriteError(w, fmt.Errorf("%w: %v", r.invalidMessage, err))
			return
		}
		var m M
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&m); err != nil {
			r.WriteError(w, fmt.Errorf("%w: %v", r.invalidMessage, err))
			return
		}
		answer, err := serve(req.Context(), req.PathValue("imsi"), m)
		switch {
		case err != nil:
			r.WriteError(w, err)
		case answer == nil:
			w.WriteHeader(http.StatusNoContent)
		default:
			WriteJSON(w, http.StatusOK, answer)
		}
	})
}

// WriteError answers with the status of err and the JSON
// {"error": "..."}.
func (r *Router) WriteError(w http.ResponseWriter, err error) {
	status := r.status(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	WriteJSON(w, status, map[string]string{"error": err.Error()})
}

// WriteJSON answers with status and v as JSON.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
