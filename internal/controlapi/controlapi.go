// Package controlapi holds what the HTTP APIs that a learner drives from
// a browser or a shell share: the API of the Lab, of the fault agent and
// of the UE simulator. They are this project's own APIs, which no 3GPP
// or IETF specification defines.
package controlapi

import (
	"encoding/json"
	"mime"
	"net/http"
	"strings"
)

// RequireJSON serves mux, and refuses with 415 (Unsupported Media Type)
// a request for a POST or a PUT route of mux whose Content-Type is not
// application/json, before the route sees it. The error is
// {"error": text}, as every other error of these APIs.
//
// A page of any site open in the learner's browser can have the browser
// send a POST to 127.0.0.1 without asking the server first, if its
// Content-Type is text/plain, a form or multipart, or there is none: a
// request that the CORS protocol lets through as it is (Fetch Standard,
// "CORS-safelisted method" and "CORS-safelisted request-header"). The
// browser does not show that page the answer, but the server has
// carried the request out all the same. With application/json, as with
// any PUT or DELETE, the browser first asks the server with a preflight
// OPTIONS request, which these APIs do not answer with the
// Access-Control-Allow-* header fields, so the request itself is never
// sent. A POST without a body, such as one that ends a call, needs the
// Content-Type too.
//
// That keeps out the requests that a page of another site sends without
// a preflight, and nothing more. A page that the browser takes for one
// of the same origin as the API sends JSON with no preflight: one of a
// domain whose name the attacker then has resolve to 127.0.0.1, which
// is DNS rebinding. Nothing here checks the Host header that would tell
// such a request, nor asks who sends it.
//
// A request for a path or method that mux has no route for is left to
// mux, which answers it 404 or 405 as before.
func RequireJSON(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		method, _, _ := strings.Cut(pattern, " ")
		if method == http.MethodPost || method == http.MethodPut {
			if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnsupportedMediaType)
				json.NewEncoder(w).Encode(map[string]string{"error": "a " + r.Method + " takes Content-Type application/json only"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}
