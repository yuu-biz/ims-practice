package lab

import (
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// proxyDialTimeout bounds how long a request waits for a connection to
// the other node, so that the page gets an answer instead of hanging
// when nothing answers at that address. The fault agent sits on a
// network that the Lab joins only with deploy/chaos/agent.yaml, so
// without the agent its address is not even routed.
const proxyDialTimeout = 3 * time.Second

// apiProxy forwards paths of the Lab's API to the API of another node at
// base, each to the path that rewrite returns for it, with the same
// method and body, and passes the answer back. what names that node in
// the errors, as in "EPC stub". An empty base answers 503: this
// deployment has no such node. The node owns the state behind its API,
// so the Lab keeps no copy of it.
func apiProxy(what, base string, rewrite func(path string) string, log *slog.Logger) (http.Handler, error) {
	if base == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, apiError{"no " + what + " is configured"})
		}), nil
	}
	target, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	// The transport of the standard library with a shorter dial, so that
	// everything else about the connections stays as the library has it.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: proxyDialTimeout}).DialContext
	return &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + rewrite(r.In.URL.Path)
			r.Out.URL.RawPath = ""
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("the "+what+" did not answer", "method", r.Method, "path", r.URL.Path, "error", err.Error())
			writeJSON(w, http.StatusBadGateway, apiError{"the " + what + " did not answer"})
		},
	}, nil
}

type apiError struct {
	Error string `json:"error"`
}
