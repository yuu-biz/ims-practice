package dashboard

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// The endpoints of the EPC stub that the dashboard passes on, under
// /api/epc: a learner reads a UE's bearers, and makes the network
// deactivate one or detach the UE, procedures that the network
// initiates, from the host, where the addresses of the Compose network
// may not be reachable. The UE API of the stub is not among them.
var epcRoutes = []string{
	"GET /api/epc/ues/{imsi}",
	"DELETE /api/epc/network-initiated/ues/{imsi}/bearers/{ebi}",
	"POST /api/epc/network-initiated/ues/{imsi}/detach",
}

// epcAPI forwards the routes of epcRoutes to the EPC stub at base,
// /api/epc/... to /..., and passes its answer back. The stub owns the state of the UEs, so the dashboard
// keeps no copy of it.
func epcAPI(base string, log *slog.Logger) (http.Handler, error) {
	if base == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, apiError{"no EPC stub is configured"})
		}), nil
	}
	target, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	return &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			rest := strings.TrimPrefix(r.In.URL.Path, "/api/epc")
			r.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + rest
			r.Out.URL.RawPath = ""
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("EPC stub API failed", "method", r.Method, "path", r.URL.Path, "error", err.Error())
			writeJSON(w, http.StatusBadGateway, apiError{"the EPC stub did not answer"})
		},
	}, nil
}
