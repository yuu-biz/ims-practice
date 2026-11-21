package ibcf

import (
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the traffic statistics of the border, for the
// operator: what crossed it and what it turned away, per peer. TS
// 32.409 4.11 has the IBCF count the sessions from and to other network
// domains and the 403 (Forbidden) responses for them; the counters here
// split those by peer, as 4.11 splits them by domain, and count every
// other refusal too.

// Reasons for which the IBCF drops a request without any response.
const (
	// DiscardUnknownSource: the request came from an address that is no
	// peer's (Peer.Sources). A border answers nothing to it, so that a
	// sender outside every agreement learns nothing about the network.
	DiscardUnknownSource = "unknown_source"
)

var (
	directionLabel = metrics.Label{Name: "direction", Values: []string{string(interconnect.Exit), string(interconnect.Entry)}}
	// methodLabel are the methods that a profile can allow (TS 29.165
	// Table 6.1); a request of any other method counts under "other".
	methodLabel = metrics.Label{Name: "method", Values: []string{
		string(sip.INVITE), string(sip.ACK), string(sip.BYE), string(sip.CANCEL), string(sip.PRACK),
		string(sip.UPDATE), string(sip.OPTIONS), string(sip.MESSAGE), string(sip.SUBSCRIBE),
		string(sip.NOTIFY), "INFO", "REFER", "PUBLISH", string(sip.REGISTER),
	}}
	// statusLabel are the status codes that the IBCF sends itself (the
	// Rejections of interconnect, and its own failures) and the ones a
	// call most often fails with.
	statusLabel = metrics.Label{Name: "status", Values: []string{
		"400", "403", "404", "405", "408", "415", "420", "480", "481", "483", "486", "487", "488",
		"491", "500", "502", "503", "504", "603",
	}}
	reasonLabel = metrics.Label{Name: "reason", Values: []string{DiscardUnknownSource}}
)

// borderMetrics are the counters of one IBCF instance.
type borderMetrics struct {
	sessions  *metrics.Counter
	failures  *metrics.Counter
	rejected  *metrics.Counter
	discarded *metrics.Counter
}

// newBorderMetrics registers the counters in reg. peers are the names
// of the configured peers: the values of the peer label, which a
// request that leaves for no peer network counts under "other". A
// request from no peer network has no response to count: Screen drops
// it, and it counts in ims_ibcf_requests_discarded_total.
func newBorderMetrics(reg *metrics.Registry, peers []string) (*borderMetrics, error) {
	peer := metrics.Label{Name: "peer", Values: peers}
	m := &borderMetrics{}
	var err error
	if m.sessions, err = reg.NewCounter("ims_ibcf_session_attempts_total",
		"Initial INVITE requests that the IBCF sent on across the border, by peer and direction (TS 32.409 IDS.AttSessionFromOtherNtwkDmn, IDS.AttSessionToOtherNtwkDmn).",
		peer, directionLabel); err != nil {
		return nil, err
	}
	if m.failures, err = reg.NewCounter("ims_ibcf_session_failures_total",
		"Final responses other than 2xx that came back across the border to an initial INVITE, by peer, direction of the INVITE and status code (TS 32.409 IDS.403SessionToOtherNtwkDmn for 403).",
		peer, directionLabel, statusLabel); err != nil {
		return nil, err
	}
	if m.rejected, err = reg.NewCounter("ims_ibcf_requests_rejected_total",
		"Requests that the IBCF answered itself with a final response other than 2xx instead of sending them on, by peer, direction, method and status code (TS 32.409 IDS.403SessionFromOtherNtwkDmn for an INVITE that enters and gets 403).",
		peer, directionLabel, methodLabel, statusLabel); err != nil {
		return nil, err
	}
	if m.discarded, err = reg.NewCounter("ims_ibcf_requests_discarded_total",
		"Requests that the IBCF dropped without any response, by reason and method.",
		reasonLabel, methodLabel); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *borderMetrics) session(peer string, d interconnect.Direction) {
	m.sessions.Inc(peer, string(d))
}

func (m *borderMetrics) failure(peer string, d interconnect.Direction, status int) {
	m.failures.Inc(peer, string(d), strconv.Itoa(status))
}

func (m *borderMetrics) reject(peer string, d interconnect.Direction, method sip.Method, status int) {
	m.rejected.Inc(peer, string(d), string(method), strconv.Itoa(status))
}

func (m *borderMetrics) discard(reason string, method sip.Method) {
	m.discarded.Inc(reason, string(method))
}
