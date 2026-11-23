package metrics

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
)

type kind string

const (
	counterKind   kind = "counter"
	gaugeKind     kind = "gauge"
	histogramKind kind = "histogram"
)

// family is what every metric has in common: the name, the help text,
// the label space and one slot per combination of label values.
//
// A slot is filled the first time its combination is counted, so a
// series appears in the output only after it has been used, as with
// the official client. A metric without labels has its only series from
// the start, so that it is never missing from the output.
type family[V any] struct {
	name     string
	help     string
	kind     kind
	space    labelSpace
	newValue func() *V
	slots    []atomic.Pointer[series[V]]
}

type series[V any] struct {
	// pairs is the rendered label pairs, without braces.
	pairs string
	value *V
}

func newFamily[V any](name, help string, k kind, labels []Label, newValue func() *V) (*family[V], error) {
	if err := validateMetricName(name, k); err != nil {
		return nil, err
	}
	if help == "" {
		return nil, fmt.Errorf("metrics: %s has no help text", name)
	}
	space, err := newLabelSpace(labels)
	if err != nil {
		return nil, fmt.Errorf("%w (metric %s)", err, name)
	}
	f := &family[V]{
		name:     name,
		help:     help,
		kind:     k,
		space:    space,
		newValue: newValue,
		slots:    make([]atomic.Pointer[series[V]], space.size),
	}
	if len(labels) == 0 {
		f.get(nil)
	}
	return f, nil
}

// get returns the series for the label values, creating it on first
// use. The lookup takes no lock; two goroutines that create the same
// series at once agree on one through the compare-and-swap.
func (f *family[V]) get(values []string) *V {
	i := f.space.index(f.name, values)
	if s := f.slots[i].Load(); s != nil {
		return s.value
	}
	s := &series[V]{pairs: f.space.pairs(i), value: f.newValue()}
	if f.slots[i].CompareAndSwap(nil, s) {
		return s.value
	}
	return f.slots[i].Load().value
}

func (f *family[V]) metricName() string { return f.name }

// definition is what two registrations of one name must agree on to
// share the metric: the kind, the help text and the labels with their
// values.
func (f *family[V]) definition() string {
	return fmt.Sprintf("%s\n%s\n%q\n%q", f.kind, f.help, f.space.names, f.space.values)
}

// writeHeader writes the HELP and TYPE lines.
func (f *family[V]) writeHeader(w *bufio.Writer) {
	fmt.Fprintf(w, "# HELP %s %s\n", f.name, escapeHelp(f.help))
	fmt.Fprintf(w, "# TYPE %s %s\n", f.name, f.kind)
}

// each calls fn for the series in use, in the order of the declared
// label values, so that the output is the same from one scrape to the
// next.
func (f *family[V]) each(fn func(pairs string, v *V)) {
	for i := range f.slots {
		if s := f.slots[i].Load(); s != nil {
			fn(s.pairs, s.value)
		}
	}
}

func writeSample(w *bufio.Writer, name, pairs string, value float64) {
	w.WriteString(name)
	if pairs != "" {
		w.WriteString("{" + pairs + "}")
	}
	w.WriteString(" " + formatFloat(value) + "\n")
}

// metric is a registered Counter, Gauge or Histogram.
type metric interface {
	metricName() string
	definition() string
	write(w *bufio.Writer)
}

// Registry holds the metrics of one node.
//
// There is no default, package-level registry, unlike the official
// client. Each node, and each test, creates its own and passes it to
// the parts that count, so that two nodes in one process (the e2e
// tests in internal/ue) do not share counts.
//
// A name registered again with the same definition gives the metric
// registered first. The roles of one node that count the same
// measurement, such as the P-CSCF and the S-CSCF counting registration
// procedures (TS 32.409 4.1.1), so count in one metric, each under its
// own value of the role label. A name registered again with another
// definition is an error.
type Registry struct {
	// mu guards metrics. Only registration and scraping take it;
	// counting does not.
	mu      sync.Mutex
	metrics []metric // sorted by name
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// register adds m, or returns the metric of the same name and
// definition that is there already.
func (r *Registry) register(m metric) (metric, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	name := m.metricName()
	i := sort.Search(len(r.metrics), func(i int) bool { return r.metrics[i].metricName() >= name })
	if i < len(r.metrics) && r.metrics[i].metricName() == name {
		if r.metrics[i].definition() == m.definition() {
			return r.metrics[i], nil
		}
		return nil, fmt.Errorf("metrics: %s is already registered with another definition", name)
	}
	r.metrics = append(r.metrics, nil)
	copy(r.metrics[i+1:], r.metrics[i:])
	r.metrics[i] = m
	return m, nil
}

// NewCounter registers a counter. Its name ends in "_total". Every
// label has a closed set of values; see Label and OtherValue.
func (r *Registry) NewCounter(name, help string, labels ...Label) (*Counter, error) {
	f, err := newFamily(name, help, counterKind, labels, func() *floatValue { return new(floatValue) })
	if err != nil {
		return nil, err
	}
	m, err := r.register(&Counter{f: f})
	if err != nil {
		return nil, err
	}
	return m.(*Counter), nil
}

// NewGauge registers a gauge.
func (r *Registry) NewGauge(name, help string, labels ...Label) (*Gauge, error) {
	f, err := newFamily(name, help, gaugeKind, labels, func() *floatValue { return new(floatValue) })
	if err != nil {
		return nil, err
	}
	m, err := r.register(&Gauge{f: f})
	if err != nil {
		return nil, err
	}
	return m.(*Gauge), nil
}

// NewHistogram registers a histogram with the given bucket upper
// bounds, which must be finite and increasing. The "+Inf" bucket is
// added; the bounds cannot change afterwards.
func (r *Registry) NewHistogram(name, help string, buckets []float64, labels ...Label) (*Histogram, error) {
	bounds, err := validateBuckets(name, buckets)
	if err != nil {
		return nil, err
	}
	f, err := newFamily(name, help, histogramKind, labels, func() *histogramValue {
		return &histogramValue{counts: make([]atomic.Uint64, len(bounds)+1)}
	})
	if err != nil {
		return nil, err
	}
	m, err := r.register(&Histogram{f: f, bounds: bounds})
	if err != nil {
		return nil, err
	}
	return m.(*Histogram), nil
}

// WriteText writes every metric in the text exposition format, version
// 0.0.4, sorted by name.
func (r *Registry) WriteText(out io.Writer) error {
	r.mu.Lock()
	metrics := append([]metric(nil), r.metrics...)
	r.mu.Unlock()

	w := bufio.NewWriter(out)
	for _, m := range metrics {
		m.write(w)
	}
	return w.Flush()
}

// NewHandler returns the handler for the scrape endpoint, usually
// mounted at /metrics.
//
// SIMPLIFIED: prometheus/docs instrumenting/content_negotiation.md lets
// a target choose among the formats in the Accept header. The handler
// always answers with the text format 0.0.4, which every Prometheus
// asks for, and without gzip, which is optional. The other formats
// carry nothing these metrics use.
func NewHandler(r *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body bytes.Buffer
		if err := r.WriteText(&body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ContentType)
		if req.Method == http.MethodHead {
			return
		}
		w.Write(body.Bytes())
	})
}
