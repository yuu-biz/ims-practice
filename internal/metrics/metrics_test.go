package metrics

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func mustCounter(t *testing.T, r *Registry, name, help string, labels ...Label) *Counter {
	t.Helper()
	c, err := r.NewCounter(name, help, labels...)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func mustGauge(t *testing.T, r *Registry, name, help string, labels ...Label) *Gauge {
	t.Helper()
	g, err := r.NewGauge(name, help, labels...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mustHistogram(t *testing.T, r *Registry, name, help string, buckets []float64, labels ...Label) *Histogram {
	t.Helper()
	h, err := r.NewHistogram(name, help, buckets, labels...)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func text(t *testing.T, r *Registry) string {
	t.Helper()
	var b strings.Builder
	if err := r.WriteText(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

var roles = Label{Name: "role", Values: []string{"pcscf", "scscf"}}

// goldenRegistry holds one metric of each kind, labelled and not, and
// the characters that the format escapes.
func goldenRegistry(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()

	// Registered out of order: the output is sorted by name.
	escaping := mustCounter(t, r, "test_escaping_total", "First line\nsecond line with a \\ backslash",
		Label{Name: "reason", Values: []string{`say "hi"`, `a\b`, "two\nlines"}})
	escaping.Inc(`say "hi"`)
	escaping.Inc(`a\b`)
	escaping.Add(1.5, "two\nlines")

	attempts := mustCounter(t, r, "ims_registration_attempts_total", "REGISTER requests received.",
		roles, Label{Name: "type", Values: []string{"initial", "re", "de"}})
	attempts.Inc("scscf", "initial")
	attempts.Inc("pcscf", "initial")
	attempts.Inc("scscf", "initial")
	attempts.Inc("scscf", "emergency")

	mustCounter(t, r, "ims_hep_dropped_total", "HEP copies dropped because the send queue was full.")

	subscriptions := mustGauge(t, r, "ims_hss_subscriptions", "IMS subscriptions stored in the HSS (TS 32.409 DBU.NbrImsSubscription).")
	subscriptions.Set(3)

	sessions := mustGauge(t, r, "ims_sessions", "Sessions in progress.", roles)
	sessions.Set(1234567, "pcscf")
	sessions.Inc("scscf")
	sessions.Dec("scscf")

	setup := mustHistogram(t, r, "ims_registration_setup_seconds", "Time from a REGISTER to its 200 (OK).",
		[]float64{0.1, 0.5, 1}, Label{Name: "role", Values: []string{"pcscf"}})
	setup.Observe(0.25, "pcscf")
	setup.Observe(0.5, "pcscf")
	setup.Observe(2, "pcscf")
	return r
}

func TestWriteTextMatchesGolden(t *testing.T) {
	want, err := os.ReadFile("testdata/exposition.txt")
	if err != nil {
		t.Fatal(err)
	}
	if got := text(t, goldenRegistry(t)); got != string(want) {
		t.Errorf("exposition differs from testdata/exposition.txt\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestHandlerServesTheTextFormat(t *testing.T) {
	r := goldenRegistry(t)
	srv := httptest.NewServer(NewHandler(r))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if string(body) != text(t, r) {
		t.Errorf("body differs from WriteText:\n%s", body)
	}

	resp, err = http.Head(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != 0 {
		t.Errorf("HEAD status = %d with %d bytes", resp.StatusCode, len(body))
	}

	resp, err = http.Post(srv.URL+"/metrics", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Errorf("POST status = %d, Allow = %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

// TestSubscriberValuesDoNotAddSeries checks that a value that differs
// per subscriber cannot make a series of its own, however many
// subscribers there are, so that the metrics stay usable at any scale.
func TestSubscriberValuesDoNotAddSeries(t *testing.T) {
	r := NewRegistry()
	c := mustCounter(t, r, "ims_registration_failures_total", "Failed registrations.",
		Label{Name: "status", Values: []string{"401", "403"}})
	c.Inc("403")
	before := strings.Count(text(t, r), "\n")

	for i := 0; i < 1000; i++ {
		c.Inc(fmt.Sprintf("sip:00101%010d@ims.mnc001.mcc001.3gppnetwork.org", i))
	}
	out := text(t, r)
	if lines := strings.Count(out, "\n"); lines != before+1 {
		t.Errorf("%d lines after 1000 subscribers, want %d (one more, for %q):\n%s", lines, before+1, OtherValue, out)
	}
	if !strings.Contains(out, `ims_registration_failures_total{status="other"} 1000`+"\n") {
		t.Errorf("the 1000 values are not counted under %q:\n%s", OtherValue, out)
	}
}

func TestRegistrationRejects(t *testing.T) {
	bigSet := make([]string, 30)
	for i := range bigSet {
		bigSet[i] = fmt.Sprint(i)
	}
	tests := []struct {
		name     string
		register func(r *Registry) error
	}{
		{"counter without _total", func(r *Registry) error {
			_, err := r.NewCounter("ims_registrations", "h")
			return err
		}},
		{"gauge with _total", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions_total", "h")
			return err
		}},
		{"histogram with _total", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_total", "h", DefaultBuckets())
			return err
		}},
		{"upper case", func(r *Registry) error {
			_, err := r.NewGauge("ims_Sessions", "h")
			return err
		}},
		{"colon", func(r *Registry) error {
			_, err := r.NewGauge("ims:sessions", "h")
			return err
		}},
		{"leading digit", func(r *Registry) error {
			_, err := r.NewGauge("3gpp_sessions", "h")
			return err
		}},
		{"dot", func(r *Registry) error {
			_, err := r.NewGauge("UR.AttInitReg", "h")
			return err
		}},
		{"_count suffix", func(r *Registry) error {
			_, err := r.NewGauge("ims_session_count", "h")
			return err
		}},
		{"_bucket suffix", func(r *Registry) error {
			_, err := r.NewGauge("ims_bucket", "h")
			return err
		}},
		{"_sum suffix", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_sum", "h", DefaultBuckets())
			return err
		}},
		{"no help", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "")
			return err
		}},
		{"same name with other labels", func(r *Registry) error {
			if _, err := r.NewGauge("ims_sessions", "h"); err != nil {
				return nil
			}
			_, err := r.NewGauge("ims_sessions", "h", roles)
			return err
		}},
		{"same name with other label values", func(r *Registry) error {
			if _, err := r.NewGauge("ims_sessions", "h", roles); err != nil {
				return nil
			}
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role", Values: []string{"pcscf"}})
			return err
		}},
		{"same name with other buckets", func(r *Registry) error {
			if _, err := r.NewHistogram("ims_setup_seconds", "h", []float64{1}); err != nil {
				return nil
			}
			_, err := r.NewHistogram("ims_setup_seconds", "h", []float64{2})
			return err
		}},
		{"reserved label prefix", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "__role", Values: []string{"pcscf"}})
			return err
		}},
		{"label le", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_seconds", "h", DefaultBuckets(), Label{Name: "le", Values: []string{"1"}})
			return err
		}},
		{"label quantile", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "quantile", Values: []string{"0.5"}})
			return err
		}},
		{"upper case label", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "Role", Values: []string{"pcscf"}})
			return err
		}},
		{"label twice", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", roles, roles)
			return err
		}},
		{"label without values", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role"})
			return err
		}},
		{"empty value", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role", Values: []string{""}})
			return err
		}},
		{"value other", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role", Values: []string{"pcscf", OtherValue}})
			return err
		}},
		{"value twice", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role", Values: []string{"pcscf", "pcscf"}})
			return err
		}},
		{"value not UTF-8", func(r *Registry) error {
			_, err := r.NewGauge("ims_sessions", "h", Label{Name: "role", Values: []string{"\xff"}})
			return err
		}},
		{"too many series", func(r *Registry) error {
			_, err := r.NewCounter("ims_messages_total", "h",
				Label{Name: "a", Values: bigSet}, Label{Name: "b", Values: bigSet}, Label{Name: "c", Values: bigSet})
			return err
		}},
		{"no buckets", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_seconds", "h", nil)
			return err
		}},
		{"buckets not increasing", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_seconds", "h", []float64{0.1, 0.1})
			return err
		}},
		{"bucket +Inf", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_seconds", "h", []float64{0.1, math.Inf(1)})
			return err
		}},
		{"bucket NaN", func(r *Registry) error {
			_, err := r.NewHistogram("ims_setup_seconds", "h", []float64{math.NaN()})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			if err := tt.register(r); err == nil {
				t.Fatal("registered, want an error")
			}
		})
	}
}

func TestRejectedMetricIsNotExposed(t *testing.T) {
	r := NewRegistry()
	mustGauge(t, r, "ims_sessions", "h")
	if _, err := r.NewGauge("ims_sessions", "other help"); err == nil {
		t.Fatal("registered the same name twice")
	}
	if out := text(t, r); strings.Count(out, "# TYPE") != 1 || strings.Contains(out, "other help") {
		t.Errorf("output:\n%s", out)
	}
}

// Two parts of a node that register a name with the same definition
// count in one metric, and the output has it once.
func TestSameDefinitionSharesTheMetric(t *testing.T) {
	r := NewRegistry()
	types := Label{Name: "type", Values: []string{"initial", "re", "de"}}
	byPCSCF := mustCounter(t, r, "ims_registration_attempts_total", "h", roles, types)
	bySCSCF := mustCounter(t, r, "ims_registration_attempts_total", "h", roles, types)
	if byPCSCF != bySCSCF {
		t.Fatal("the second registration made another counter")
	}
	byPCSCF.Inc("pcscf", "initial")
	bySCSCF.Inc("scscf", "initial")
	bySCSCF.Inc("pcscf", "initial")
	out := text(t, r)
	if strings.Count(out, "# TYPE ims_registration_attempts_total") != 1 ||
		!strings.Contains(out, `ims_registration_attempts_total{role="pcscf",type="initial"} 2`+"\n") ||
		!strings.Contains(out, `ims_registration_attempts_total{role="scscf",type="initial"} 1`+"\n") {
		t.Errorf("output:\n%s", out)
	}
	if g, h := mustGauge(t, r, "ims_sessions", "h", roles), mustGauge(t, r, "ims_sessions", "h", roles); g != h {
		t.Error("the second registration made another gauge")
	}
	buckets := []float64{0.5, 1}
	if g, h := mustHistogram(t, r, "ims_setup_seconds", "h", buckets), mustHistogram(t, r, "ims_setup_seconds", "h", buckets); g != h {
		t.Error("the second registration made another histogram")
	}
}

func expectPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", what)
		}
	}()
	fn()
}

func TestProgrammingErrorsPanic(t *testing.T) {
	r := NewRegistry()
	c := mustCounter(t, r, "ims_registration_attempts_total", "h", roles)
	expectPanic(t, "too few label values", func() { c.Inc() })
	expectPanic(t, "too many label values", func() { c.Inc("pcscf", "initial") })
	expectPanic(t, "negative add", func() { c.Add(-1, "pcscf") })
	expectPanic(t, "NaN add", func() { c.Add(math.NaN(), "pcscf") })
}

func TestHistogramBuckets(t *testing.T) {
	r := NewRegistry()
	h := mustHistogram(t, r, "ims_session_setup_seconds", "h", []float64{0.5, 1, 2.5})
	// Below the first bound, on each bound, between bounds and above
	// the last: "le" means less than or equal.
	for _, v := range []float64{-1, 0, 0.5, 0.75, 1, 2.5, 3, 10} {
		h.Observe(v)
	}
	want := `# HELP ims_session_setup_seconds h
# TYPE ims_session_setup_seconds histogram
ims_session_setup_seconds_bucket{le="0.5"} 3
ims_session_setup_seconds_bucket{le="1"} 5
ims_session_setup_seconds_bucket{le="2.5"} 6
ims_session_setup_seconds_bucket{le="+Inf"} 8
ims_session_setup_seconds_sum 16.75
ims_session_setup_seconds_count 8
`
	if got := text(t, r); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestHistogramStartsAtZero(t *testing.T) {
	r := NewRegistry()
	mustHistogram(t, r, "ims_registration_setup_seconds", "h", []float64{1})
	want := `# HELP ims_registration_setup_seconds h
# TYPE ims_registration_setup_seconds histogram
ims_registration_setup_seconds_bucket{le="1"} 0
ims_registration_setup_seconds_bucket{le="+Inf"} 0
ims_registration_setup_seconds_sum 0
ims_registration_setup_seconds_count 0
`
	if got := text(t, r); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBucketsAreCopied(t *testing.T) {
	r := NewRegistry()
	buckets := []float64{1, 2}
	h := mustHistogram(t, r, "ims_registration_setup_seconds", "h", buckets)
	buckets[0] = 100
	h.Observe(1.5)
	if !strings.Contains(text(t, r), `ims_registration_setup_seconds_bucket{le="1"} 0`) {
		t.Errorf("changing the slice after registration changed the buckets:\n%s", text(t, r))
	}
}

func TestFormatFloat(t *testing.T) {
	for v, want := range map[float64]string{
		0:            "0",
		1:            "1",
		0.005:        "0.005",
		2.75:         "2.75",
		1e21:         "1e+21",
		-3:           "-3",
		math.Inf(1):  "+Inf",
		math.Inf(-1): "-Inf",
	} {
		if got := formatFloat(v); got != want {
			t.Errorf("formatFloat(%v) = %q, want %q", v, got, want)
		}
	}
	if got := formatFloat(math.NaN()); got != "NaN" {
		t.Errorf("formatFloat(NaN) = %q", got)
	}
}

func TestStatusClass(t *testing.T) {
	for code, want := range map[int]string{
		100: "1xx", 180: "1xx", 200: "2xx", 302: "3xx", 403: "4xx", 503: "5xx", 699: "6xx",
		0: OtherValue, 99: OtherValue, 700: OtherValue,
	} {
		if got := StatusClass(code); got != want {
			t.Errorf("StatusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// TestConcurrentUse counts from many goroutines while others scrape.
// Run with -race; the totals must come out exact.
func TestConcurrentUse(t *testing.T) {
	r := NewRegistry()
	c := mustCounter(t, r, "ims_sip_messages_total", "h", roles)
	g := mustGauge(t, r, "ims_sessions", "h", roles)
	h := mustHistogram(t, r, "ims_session_setup_seconds", "h", []float64{1}, roles)

	const workers, rounds = 8, 1000
	var counting, scraping sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 2; i++ {
		scraping.Add(1)
		go func() {
			defer scraping.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if err := r.WriteText(io.Discard); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}()
	}
	for i := 0; i < workers; i++ {
		counting.Add(1)
		go func() {
			defer counting.Done()
			for j := 0; j < rounds; j++ {
				c.Inc("pcscf")
				g.Inc("scscf")
				g.Dec("scscf")
				g.Add(1, "pcscf")
				h.Observe(0.5, "pcscf")
			}
		}()
	}
	counting.Wait()
	close(stop)
	scraping.Wait()

	out := text(t, r)
	for _, line := range []string{
		`ims_sip_messages_total{role="pcscf"} 8000`,
		`ims_sessions{role="pcscf"} 8000`,
		`ims_sessions{role="scscf"} 0`,
		`ims_session_setup_seconds_bucket{role="pcscf",le="1"} 8000`,
		`ims_session_setup_seconds_sum{role="pcscf"} 4000`,
		`ims_session_setup_seconds_count{role="pcscf"} 8000`,
	} {
		if !strings.Contains(out, line+"\n") {
			t.Errorf("missing %q in:\n%s", line, out)
		}
	}
}

func TestRegistriesAreSeparate(t *testing.T) {
	a, b := NewRegistry(), NewRegistry()
	mustCounter(t, a, "ims_hep_dropped_total", "h").Inc()
	mustCounter(t, b, "ims_hep_dropped_total", "h")
	if !bytes.Contains([]byte(text(t, b)), []byte("ims_hep_dropped_total 0\n")) {
		t.Errorf("a count in one registry shows in another:\n%s", text(t, b))
	}
}
