package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// UESimulator is one UE simulator ("ue serve") that the web UI drives.
type UESimulator struct {
	Name    string // as in /api/ues/{name}
	Control string // the URL of its control API (ue.Phone.Handler)
}

// ueRoutes are the requests of the control API that the Lab passes
// on, each under /api/ues/{name}. GET /status is not among them: the
// list carries it. Nor is GET /sms: the status carries the short
// messages too, and its events bring each change.
var ueRoutes = []string{
	"POST /register",
	"POST /deregister",
	"POST /call",
	"POST /answer",
	"POST /hangup",
	// The answered call: hold and resume by re-INVITE (TS 24.610), and
	// DTMF as telephone events (RFC 4733).
	"POST /hold",
	"POST /resume",
	"POST /dtmf",
	// Two calls on the line: go from one to the other (TS 24.610), or
	// merge them into a three-way conference (TS 24.147).
	"POST /swap",
	"POST /merge",
	"PUT /answer-policy",
	"POST /sms",
	// How the UE takes short messages: without memory for them, or
	// answering each one 480, so that the network has to hold them
	// (TS 24.341 5.3.2.5, TS 23.204 6.5a), and the notification that
	// the UE has memory again.
	"PUT /sms-memory",
	"POST /sms-memory/notify",
	"PUT /sms-availability",
	"GET /events",
	// The supplementary services, which the UE sets over Ut with XCAP
	// (ue.SupplementaryServices.Handler). The Lab asks the UE
	// rather than the XCAP server, so that the XCAP requests come from
	// the UE as on a real network, and the capture shows them on Ut.
	"GET /xcap",
	"PUT /xcap/services/{service}",
	"PUT /xcap/oir",
}

// ueStatusTimeout bounds how long the list waits for one UE, so that a
// UE that is down does not hold up the others.
const ueStatusTimeout = 2 * time.Second

// ueStatusLimit bounds how much of a status the list reads from one UE.
const ueStatusLimit = 1 << 16

// ueSimulators forwards to the control APIs of the UE simulators. The UEs
// own their state; the Lab keeps none of it, as it keeps none of
// the HSS's subscriptions.
type ueSimulators struct {
	list   []ueSimulator
	byName map[string]ueSimulator
	client *http.Client
}

type ueSimulator struct {
	name   string
	status string // the URL of its GET /status
	proxy  *httputil.ReverseProxy
}

func newUESimulators(configured []UESimulator, log *slog.Logger) (*ueSimulators, error) {
	s := &ueSimulators{byName: map[string]ueSimulator{}, client: &http.Client{}}
	for _, c := range configured {
		target, err := url.Parse(c.Control)
		if err != nil {
			return nil, fmt.Errorf("UE simulator %s: %w", c.Name, err)
		}
		prefix := "/api/ues/" + c.Name
		sim := ueSimulator{
			name:   c.Name,
			status: target.JoinPath("status").String(),
			// The proxy flushes each server-sent event of /events as it
			// comes, by its Content-Type.
			proxy: &httputil.ReverseProxy{
				Rewrite: func(r *httputil.ProxyRequest) {
					r.SetURL(target)
					r.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + strings.TrimPrefix(r.In.URL.Path, prefix)
					r.Out.URL.RawPath = ""
				},
				ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
					log.Warn("UE simulator did not answer", "ue", c.Name, "method", r.Method, "path", r.URL.Path, "error", err.Error())
					writeJSON(w, http.StatusBadGateway, apiError{"the UE simulator " + c.Name + " did not answer"})
				},
			},
		}
		s.list = append(s.list, sim)
		s.byName[c.Name] = sim
	}
	return s, nil
}

func (s *ueSimulators) forward(w http.ResponseWriter, r *http.Request) {
	sim, ok := s.byName[r.PathValue("name")]
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{fmt.Sprintf("no UE simulator is named %q", r.PathValue("name"))})
		return
	}
	sim.proxy.ServeHTTP(w, r)
}

// ueEntry is one UE simulator in GET /api/ues.
type ueEntry struct {
	Name      string `json:"name"`
	Reachable bool   `json:"reachable"`
	// Status is what the UE's GET /status answered, as it is, or null
	// when the UE did not answer it.
	Status json.RawMessage `json:"status"`
	Error  string          `json:"error,omitempty"`
}

func (s *ueSimulators) getList(w http.ResponseWriter, r *http.Request) {
	entries := make([]ueEntry, len(s.list))
	var wg sync.WaitGroup
	for i, sim := range s.list {
		wg.Go(func() { entries[i] = s.fetchStatus(r.Context(), sim) })
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, entries)
}

func (s *ueSimulators) fetchStatus(ctx context.Context, sim ueSimulator) ueEntry {
	entry := ueEntry{Name: sim.name}
	ctx, cancel := context.WithTimeout(ctx, ueStatusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sim.status, nil)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	resp, err := s.client.Do(req)
	if err != nil {
		entry.Error = err.Error()
		return entry
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, ueStatusLimit+1))
	switch {
	case err != nil:
		entry.Error = err.Error()
	case resp.StatusCode != http.StatusOK:
		entry.Error = "GET /status answered " + resp.Status
	case len(body) > ueStatusLimit:
		entry.Error = fmt.Sprintf("GET /status answered a status longer than the %d bytes the Lab reads", ueStatusLimit)
	case !json.Valid(body):
		entry.Error = "GET /status answered no JSON"
	default:
		entry.Reachable, entry.Status = true, body
	}
	return entry
}
