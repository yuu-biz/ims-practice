package metrics_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// registrationMetrics is how a role would hold its metrics: registered
// once at start-up, then counted from the procedure.
type registrationMetrics struct {
	attempts *metrics.Counter
	failures *metrics.Counter
	setup    *metrics.Histogram
}

var (
	role    = metrics.Label{Name: "role", Values: []string{"pcscf"}}
	regType = metrics.Label{Name: "type", Values: []string{"initial", "re", "de"}}
	// The response codes counted one by one; any other code is counted
	// under "other". The 401 that carries the challenge is not a
	// failure, so it is not here.
	regStatus = metrics.Label{Name: "status", Values: []string{"403", "404", "408", "423", "480", "500", "503", "504"}}
)

func newRegistrationMetrics(reg *metrics.Registry) (*registrationMetrics, error) {
	attempts, err := reg.NewCounter("ims_registration_attempts_total",
		"REGISTER requests received (TS 32.409 UR.AttInitReg, UR.AttReReg, UR.AttDeRegUe).",
		role, regType)
	if err != nil {
		return nil, err
	}
	failures, err := reg.NewCounter("ims_registration_failures_total",
		"Final responses other than 2xx sent to a REGISTER, by status code (TS 32.409 UR.FailInitReg.Cause).",
		role, regType, regStatus)
	if err != nil {
		return nil, err
	}
	setup, err := reg.NewHistogram("ims_registration_setup_seconds",
		"Time from a REGISTER to its 200 (OK) (TS 32.409 UR.MeanInitRegSetupTime).",
		[]float64{0.05, 0.2, 1}, role)
	if err != nil {
		return nil, err
	}
	return &registrationMetrics{attempts: attempts, failures: failures, setup: setup}, nil
}

// Example shows a P-CSCF that registers its metrics, counts two
// initial registrations, one of which fails, and serves the counts.
func Example() {
	reg := metrics.NewRegistry()
	m, err := newRegistrationMetrics(reg)
	if err != nil {
		fmt.Println(err)
		return
	}

	// A REGISTER arrives and, after the challenge, ends with 200 (OK)
	// 12 ms later. A role measures this with time.Since.
	m.attempts.Inc("pcscf", "initial")
	m.setup.Observe((12 * time.Millisecond).Seconds(), "pcscf")

	// Another one ends with 403 (Forbidden).
	m.attempts.Inc("pcscf", "initial")
	m.failures.Inc("pcscf", "initial", strconv.Itoa(403))

	// The node serves the registry for Prometheus to scrape.
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.NewHandler(reg))

	scrape := httptest.NewRecorder()
	mux.ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	fmt.Print(scrape.Body.String())
	// Output:
	// # HELP ims_registration_attempts_total REGISTER requests received (TS 32.409 UR.AttInitReg, UR.AttReReg, UR.AttDeRegUe).
	// # TYPE ims_registration_attempts_total counter
	// ims_registration_attempts_total{role="pcscf",type="initial"} 2
	// # HELP ims_registration_failures_total Final responses other than 2xx sent to a REGISTER, by status code (TS 32.409 UR.FailInitReg.Cause).
	// # TYPE ims_registration_failures_total counter
	// ims_registration_failures_total{role="pcscf",type="initial",status="403"} 1
	// # HELP ims_registration_setup_seconds Time from a REGISTER to its 200 (OK) (TS 32.409 UR.MeanInitRegSetupTime).
	// # TYPE ims_registration_setup_seconds histogram
	// ims_registration_setup_seconds_bucket{role="pcscf",le="0.05"} 1
	// ims_registration_setup_seconds_bucket{role="pcscf",le="0.2"} 1
	// ims_registration_setup_seconds_bucket{role="pcscf",le="1"} 1
	// ims_registration_setup_seconds_bucket{role="pcscf",le="+Inf"} 1
	// ims_registration_setup_seconds_sum{role="pcscf"} 0.012
	// ims_registration_setup_seconds_count{role="pcscf"} 1
}
