package nhss

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/yuu-biz/ims-practice/internal/sbi"
)

// This file is what the three services of TS 29.562 add to the HTTP/2
// plumbing of package sbi (TS 29.500 5.1 to 5.4). Their bodies are all
// application/json (TS 29.562 6.1.2.2.2, 6.2.2.2.2 and 6.3.2.2.2).

// NewServer returns a server that serves h on addr with cleartext
// HTTP/2 only (sbi.NewServer). TS 29.500 5.3 makes TCP port 80 the
// default for an "http" apiRoot, so addr is usually ":80".
func NewServer(addr string, h http.Handler) *http.Server {
	return sbi.NewServer(addr, h)
}

// decodeAnswer reads the JSON of a success answer into v.
func decodeAnswer(operation string, r sbi.Response, v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return fmt.Errorf("nhss: %s answer: %w", operation, err)
	}
	return nil
}

// writeResource answers a PUT that creates or replaces a resource:
// 201 with Location and the resource when it was created, 200 with it
// when it was replaced and the HSS returns it, and 204 otherwise (TS
// 29.562 5.2.2.2.2, 6.2.3.30.3.2).
func writeResource[T any](w http.ResponseWriter, r *http.Request, resource *T, created bool, err error) {
	switch {
	case err != nil:
		api.WriteError(w, err)
	case created && resource != nil:
		w.Header().Set("Location", "http://"+r.Host+r.URL.EscapedPath())
		api.WriteJSON(w, http.StatusCreated, resource)
	case resource != nil:
		api.WriteJSON(w, http.StatusOK, resource)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
