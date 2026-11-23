package metrics

import (
	"bufio"
	"fmt"
	"math"
	"sort"
	"sync/atomic"
)

// floatValue is a float64 that goroutines update without a lock. The
// bits of the number live in an atomic integer; an addition retries
// until no other goroutine has changed the number in between.
type floatValue struct {
	bits atomic.Uint64
}

func (v *floatValue) load() float64 { return math.Float64frombits(v.bits.Load()) }

func (v *floatValue) store(f float64) { v.bits.Store(math.Float64bits(f)) }

func (v *floatValue) add(delta float64) {
	for {
		old := v.bits.Load()
		next := math.Float64bits(math.Float64frombits(old) + delta)
		if v.bits.CompareAndSwap(old, next) {
			return
		}
	}
}

// Counter is a value that only goes up, like a Cumulative Counter of
// TS 32.401 4.2.2. Unlike there, it is not reset at the start of each
// granularity period; Prometheus computes the increase over a period
// from two samples (for example with increase()).
type Counter struct {
	f *family[floatValue]
}

// Inc adds one to the series of the label values, given in the
// declared order.
func (c *Counter) Inc(labelValues ...string) {
	c.f.get(labelValues).add(1)
}

// Add adds delta, which must not be negative.
func (c *Counter) Add(delta float64, labelValues ...string) {
	if delta < 0 || math.IsNaN(delta) {
		panic(fmt.Sprintf("metrics: counter %s cannot add %v", c.f.name, delta))
	}
	c.f.get(labelValues).add(delta)
}

func (c *Counter) metricName() string { return c.f.name }

func (c *Counter) definition() string { return c.f.definition() }

func (c *Counter) write(w *bufio.Writer) {
	c.f.writeHeader(w)
	c.f.each(func(pairs string, v *floatValue) {
		writeSample(w, c.f.name, pairs, v.load())
	})
}

// Gauge is a value that goes up and down, like the number of sessions
// in progress.
type Gauge struct {
	f *family[floatValue]
}

// Set sets the series of the label values to v.
func (g *Gauge) Set(v float64, labelValues ...string) {
	g.f.get(labelValues).store(v)
}

// Add adds delta, which may be negative.
func (g *Gauge) Add(delta float64, labelValues ...string) {
	g.f.get(labelValues).add(delta)
}

// Inc adds one.
func (g *Gauge) Inc(labelValues ...string) { g.Add(1, labelValues...) }

// Dec subtracts one.
func (g *Gauge) Dec(labelValues ...string) { g.Add(-1, labelValues...) }

func (g *Gauge) metricName() string { return g.f.name }

func (g *Gauge) definition() string { return g.f.definition() }

func (g *Gauge) write(w *bufio.Writer) {
	g.f.writeHeader(w)
	g.f.each(func(pairs string, v *floatValue) {
		writeSample(w, g.f.name, pairs, v.load())
	})
}

// gaugeFunc is a gauge whose values are read at the scrape, from the
// function given at registration. It has no slot to count in, so it
// holds its label space itself. See Registry.NewGaugeFunc.
type gaugeFunc struct {
	name  string
	help  string
	space labelSpace
	read  func(emit func(value float64, labelValues ...string))
}

func (g *gaugeFunc) metricName() string { return g.name }

// definition tells a gauge func apart from a Gauge of the same name,
// help and labels. The two carry the same type to Prometheus but not to
// a role: one is written as it changes and the other read at the
// scrape, so letting them share would drop one of the two.
func (g *gaugeFunc) definition() string {
	return fmt.Sprintf("%s func\n%s\n%q\n%q", gaugeKind, g.help, g.space.names, g.space.values)
}

// write calls read once and writes the samples it emitted, in the order
// of the declared label values, so that the output is in the same order
// from one scrape to the next. A series emitted twice keeps the value
// emitted last.
func (g *gaugeFunc) write(w *bufio.Writer) {
	writeHeader(w, g.name, gaugeKind, g.help)
	values := make([]float64, g.space.size)
	emitted := make([]bool, g.space.size)
	g.read(func(v float64, labelValues ...string) {
		i := g.space.index(g.name, labelValues)
		values[i], emitted[i] = v, true
	})
	for i, ok := range emitted {
		if ok {
			writeSample(w, g.name, g.space.pairs(i), values[i])
		}
	}
}

// DefaultBuckets returns the bucket bounds, in seconds, that the
// official clients use when none are given (client_golang's
// DefBuckets). They cover a registration in a few milliseconds as well
// as a session set-up that waits seconds for the callee.
func DefaultBuckets() []float64 {
	return []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}
}

func validateBuckets(name string, buckets []float64) ([]float64, error) {
	if len(buckets) == 0 {
		return nil, fmt.Errorf("metrics: histogram %s has no buckets", name)
	}
	for i, b := range buckets {
		if math.IsNaN(b) || math.IsInf(b, 0) {
			return nil, fmt.Errorf("metrics: histogram %s has bucket bound %v; +Inf is added by itself", name, b)
		}
		if i > 0 && b <= buckets[i-1] {
			return nil, fmt.Errorf("metrics: histogram %s has bucket bounds that do not increase at %v", name, b)
		}
	}
	return append([]float64(nil), buckets...), nil
}

// Histogram counts observations, such as set-up times, in buckets with
// fixed upper bounds, and keeps their sum. TS 32.409 asks for the mean
// set-up time (a Discrete Event Registration, for example 4.1.1.1.4);
// the mean is the sum divided by the count, and the buckets show the
// spread as well.
type Histogram struct {
	f      *family[histogramValue]
	bounds []float64
}

// histogramValue keeps one count per bucket, not cumulative, the last
// one for the observations above every bound.
type histogramValue struct {
	counts []atomic.Uint64
	sum    floatValue
}

// Observe records one observation, in seconds for a time.
func (h *Histogram) Observe(v float64, labelValues ...string) {
	hv := h.f.get(labelValues)
	// The first bound that is not below v, so that a value equal to a
	// bound falls in that bound's bucket ("le" means less or equal).
	i := sort.SearchFloat64s(h.bounds, v)
	hv.counts[i].Add(1)
	hv.sum.add(v)
}

func (h *Histogram) metricName() string { return h.f.name }

// definition adds the bucket bounds, which two registrations of a
// histogram must agree on too.
func (h *Histogram) definition() string { return fmt.Sprintf("%s\n%v", h.f.definition(), h.bounds) }

// write writes the cumulative buckets, the sum and the count.
//
// SIMPLIFIED: prometheus/docs instrumenting/exposition_formats.md
// requires the "+Inf" bucket to equal "_count", and the official client
// goes further: it takes a consistent snapshot of buckets, sum and
// count while observations go on. Here the buckets and the count come
// from one pass over the bucket counts, which meets the requirement,
// but "_sum" may count an observation made during the scrape that the
// buckets do not, or miss one that they count. The snapshot needs a
// second set of counters and a swap that Observe takes part in; here
// Observe adds to two atomic values and takes no lock. The difference
// is at most the observations made while one scrape reads them, and is
// gone at the next scrape, since both values only grow.
func (h *Histogram) write(w *bufio.Writer) {
	h.f.writeHeader(w)
	h.f.each(func(pairs string, hv *histogramValue) {
		sep := ""
		if pairs != "" {
			sep = ","
		}
		var cumulative uint64
		for i, bound := range h.bounds {
			cumulative += hv.counts[i].Load()
			writeSample(w, h.f.name+"_bucket", pairs+sep+`le="`+formatFloat(bound)+`"`, float64(cumulative))
		}
		cumulative += hv.counts[len(h.bounds)].Load()
		writeSample(w, h.f.name+"_bucket", pairs+sep+`le="+Inf"`, float64(cumulative))
		writeSample(w, h.f.name+"_sum", pairs, hv.sum.load())
		writeSample(w, h.f.name+"_count", pairs, float64(cumulative))
	})
}
