package lab

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// subscriberAPI forwards the subscriber endpoints to the HSS
// provisioning API, /api/subscribers[/{impi}] to /subscriptions[/{impi}]
// with the same method and body, and passes its answer back. The HSS owns the subscriptions, so the Lab keeps no
// copy of them.
func subscriberAPI(provisioning string, log *slog.Logger) (http.Handler, error) {
	if provisioning == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, apiError{"no HSS provisioning API is configured"})
		}), nil
	}
	target, err := url.Parse(provisioning)
	if err != nil {
		return nil, err
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			rest := strings.TrimPrefix(r.In.URL.Path, "/api/subscribers")
			r.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + "/subscriptions" + rest
			r.Out.URL.RawPath = ""
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("HSS provisioning API failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
			writeJSON(w, http.StatusBadGateway, apiError{"the HSS provisioning API did not answer"})
		},
	}, nil
}

type apiError struct {
	Error string `json:"error"`
}
