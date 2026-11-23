// Package metrics counts what an IMS node does and publishes the counts
// in the Prometheus text exposition format, version 0.0.4, so that the
// OAM server and widely used tools such as Prometheus and Grafana can
// show them.
//
// It has three kinds of metric: Counter, Gauge and Histogram. They
// correspond to the Cumulative Counter, the Gauge and the Discrete
// Event Registration of TS 32.401 4.2.2. The measurements that the IMS
// nodes count come from TS 32.409. A gauge over a number that a role
// already holds elsewhere is registered with the function that reads
// it, which the scrape calls (Registry.NewGaugeFunc).
//
// Every label is declared with the closed set of values it may take
// (Label). A value outside the set is counted under the value "other"
// (OtherValue). So the number of series of a metric is fixed when the
// metric is registered, and no label can carry a value that grows with
// the number of subscribers, such as an IMPU, an IMSI or a Call-ID, so
// that the number of series stays the same however many subscribers
// the network has.
//
// There is no default registry. A node creates one Registry, passes it
// to the parts that count, and serves it with NewHandler.
package metrics
