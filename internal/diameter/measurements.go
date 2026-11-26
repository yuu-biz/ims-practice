package diameter

import (
	"fmt"
	"slices"
	"strings"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// This file holds what a node counts of the Diameter it speaks: the
// requests a role sends to its peers and those that reach it, the
// answers of both with the result each one carries, and whether a peer
// takes requests.
//
// A role sends every request through its own PeerGroup, so the group is
// the one place they all pass, whatever command they carry and
// whichever instance of the peer serves them. A role that answers
// requests counts them where it answers them (Answering). Either way
// the count is under the role, because the measurements of TS 32.409
// belong to a function and not to a connection: one node can hold
// several roles that reach the HSS over the same connection, and one
// connection can carry the requests of two roles of a node, the Cx of
// an HSS and the RTR and PPR it sends that node's S-CSCF.
//
// A request that goes out is counted for each attempt that went out on
// a connection, which is when TS 32.409 4.1.1.5.1, 4.1.1.6.1, 4.1.3.1
// and 4.1.6.1 count theirs, and its answer when it arrives, which is
// when 4.1.1.5.2, 4.1.1.5.3, 4.1.1.6.2, 4.1.1.6.3, 4.1.3.2, 4.1.3.3,
// 4.1.6.2 and 4.1.6.3 count theirs. A failover to the next instance
// sends a second attempt, which is a second request and, when it is
// answered, a second answer. The requests of a command that no answer
// followed are the cause 'No Reply' that 4.1.6.3 names: the requests
// less the answers.
//
// A request that arrives is counted on its receipt, which is when
// 4.2.12.1 counts the requests of an HSS, 4.1.1.3.2.1 the RTR that an
// S-CSCF gets and 4.1.7.1 its PPR, and its answer when the role hands
// it back, which is when 4.2.12.2, 4.1.1.3.2.2, 4.1.1.3.2.3, 4.1.7.2
// and 4.1.7.3 count theirs. The clauses that split the failures by
// cause (4.1.1.3.2.3 and 4.1.7.3) are the result label of the answer.
//
// SIMPLIFIED: TS 32.409 4.2.12.1, 4.2.12.2, 4.1.1.3.2.2 and 4.1.7.2.
// A role counts the requests that reach it and the answers it hands
// back, not what the connections of its node carry. The CER, DWR and
// DPR of the base protocol reach no role, so no request counts them,
// and an answer that its connection dropped before writing it out,
// because the peer went away just then, is counted although it was
// never transmitted (the RefuseMessage action of loop.do).

const (
	// receivedDirection and sentDirection are the node's view of a
	// message, the direction label.
	receivedDirection = "received"
	sentDirection     = "sent"
)

var (
	// countingRoles are the roles that count Diameter messages, by their
	// name in the configuration file: those that send requests through a
	// peer group of their own, and the two that answer the requests that
	// reach them, the HSS and the S-CSCF (Answering).
	countingRoles = metrics.Label{Name: "role", Values: []string{"pcscf", "icscf", "scscf", "mmtel", "ipsmgw", "sc", "hss"}}
	directions    = metrics.Label{Name: "direction", Values: []string{receivedDirection, sentDirection}}
	// commandLabel holds the short name of the request (CommandShortName
	// with R after it), or the full name of a command that has none
	// (commandName), which an answer is counted under as well: the
	// answer to a UAR counts under "UAR", so that the two counters of
	// one command divide into each other.
	commandLabel = metrics.Label{Name: "command", Values: []string{
		// Cx (TS 29.229 Table 6.1.1) and Sh (package sh), which a role
		// sends the HSS and the HSS answers.
		"UAR", "SAR", "LIR", "MAR", "UDR", "PUR",
		// Rx (TS 29.214 5.6), of which the AF sends these two.
		"AAR", "STR",
		// The two Cx requests that the HSS sends an S-CSCF and the
		// S-CSCF answers (TS 29.229 Table 6.1.1).
		"RTR", "PPR",
		// S6c (package sgd), of which the HSS answers these two and the
		// SC sends them.
		"SRR", "RDR",
		// SGd (package sgd): the short message of a UE, which the
		// IP-SM-GW sends the SC.
		"OFR",
		// Ready-For-SM (package sgd), the command of this project's own
		// on S6c with which the IP-SM-GW tells the HSS that a user can
		// take short messages again, in place of MAP-READY-FOR-SM. No
		// specification gives it an abbreviation.
		"Ready-For-SM-Request",
	}}
	// resultLabel names the result of an answer one by one, so that a
	// dashboard can tell DIAMETER_FIRST_REGISTRATION from the other
	// successes without reading a number. Any other result counts under
	// "other" (metrics.OtherValue).
	resultLabel = metrics.Label{Name: "result", Values: resultLabelValues()}
)

// resultNames are the results that the result label names one by one,
// in the order in which it declares them: the base Result-Codes, then
// the Experimental-Result-Codes by their number. The same numbers are
// the constants that the roles build their answers with: the base
// Result-Codes in codes.go, and the Experimental-Result-Codes of Cx in
// package cx, which this package cannot read because it is the one that
// package builds its messages with. A number alone cannot name the
// result, because a 3GPP application carries its codes in
// Experimental-Result (RFC 6733 7.7): 2001 there is
// DIAMETER_FIRST_REGISTRATION of TS 29.229 6.2.1.1, while 2001 in
// Result-Code is DIAMETER_SUCCESS of RFC 6733 7.1.2.
var resultNames = []struct {
	result Result
	name   string
}{
	// RFC 6733 7.1.
	{Base(DiameterSuccess), "DIAMETER_SUCCESS"},
	{Base(DiameterTooBusy), "DIAMETER_TOO_BUSY"},
	{Base(DiameterUnableToDeliver), "DIAMETER_UNABLE_TO_DELIVER"},
	{Base(DiameterUnableToComply), "DIAMETER_UNABLE_TO_COMPLY"},
	// TS 29.229 6.2.1 and 6.2.2.
	{Experimental(2001), "DIAMETER_FIRST_REGISTRATION"},
	{Experimental(2002), "DIAMETER_SUBSEQUENT_REGISTRATION"},
	{Experimental(2003), "DIAMETER_UNREGISTERED_SERVICE"},
	{Experimental(2004), "DIAMETER_SUCCESS_SERVER_NAME_NOT_STORED"},
	{Experimental(5001), "DIAMETER_ERROR_USER_UNKNOWN"},
	{Experimental(5002), "DIAMETER_ERROR_IDENTITIES_DONT_MATCH"},
	{Experimental(5003), "DIAMETER_ERROR_IDENTITY_NOT_REGISTERED"},
	{Experimental(5004), "DIAMETER_ERROR_ROAMING_NOT_ALLOWED"},
	{Experimental(5005), "DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED"},
	{Experimental(5006), "DIAMETER_ERROR_AUTH_SCHEME_NOT_SUPPORTED"},
	{Experimental(5007), "DIAMETER_ERROR_IN_ASSIGNMENT_TYPE"},
	{Experimental(5009), "DIAMETER_ERROR_NOT_SUPPORTED_USER_DATA"},
}

func resultLabelValues() []string {
	values := make([]string, 0, len(resultNames))
	for _, r := range resultNames {
		values = append(values, r.name)
	}
	return values
}

// measurements are the counters of the Diameter messages of a node. The
// roles of one node share them, each counting under its own role, as
// the roles of one node share the registry.
type measurements struct {
	requests *metrics.Counter
	answers  *metrics.Counter
}

func newMeasurements(reg *metrics.Registry) (*measurements, error) {
	m := &measurements{}
	var err error
	if m.requests, err = reg.NewCounter("ims_diameter_requests_total",
		"Diameter requests, by command, counted on transmission of each attempt that goes out and on receipt of each one that arrives (TS 32.409 UR.AttUAR, UR.AttSAR, LIQ.AttLIR, MA.AttMAR, UR.AttDeRegHss, UP.AttPPR, UR.Request).",
		countingRoles, directions, commandLabel); err != nil {
		return nil, err
	}
	if m.answers, err = reg.NewCounter("ims_diameter_answers_total",
		"Diameter answers, by the command of the request they answer and the result they carry, counted on receipt of each one that arrives and on transmission of each one that goes out (TS 32.409 UR.SuccUAA.ResultCode, UR.FailUAA.Cause, UR.SuccSAA, UR.FailSAA.Cause, LIQ.SuccLIA, LIQ.FailLIA.Cause, MA.SuccMAA, MA.FailMAA.Cause, UR.SuccDeRegHss, UR.FailDeRegHss.Cause, UP.SuccPPA, UP.FailPPA.Cause, UR.Answer).",
		countingRoles, directions, commandLabel, resultLabel); err != nil {
		return nil, err
	}
	return m, nil
}

// sent counts a request of role that went out on a connection.
func (m *measurements) sent(role string, request *diam.Message) {
	m.requests.Inc(role, sentDirection, commandName(request))
}

// received counts the answer that came back to request.
func (m *measurements) received(role string, request, answer *diam.Message) {
	m.answers.Inc(role, receivedDirection, commandName(request), resultOf(answer))
}

// requestReceived counts a request that arrived for role.
func (m *measurements) requestReceived(role string, request *diam.Message) {
	m.requests.Inc(role, receivedDirection, commandName(request))
}

// answerSent counts the answer that role gives back to request.
func (m *measurements) answerSent(role string, request, answer *diam.Message) {
	m.answers.Inc(role, sentDirection, commandName(request), resultOf(answer))
}

// Answering counts, for one role, the Diameter requests that reach it
// and the answers it hands back: the requests and answers of an HSS (TS
// 32.409 4.2.12.1, 4.2.12.2), and the RTR and PPR that an S-CSCF gets
// from the HSS with the answers it gives them (4.1.1.3.2.1 to
// 4.1.1.3.2.3, 4.1.7.1 to 4.1.7.3).
//
// It counts in the same two metrics as a PeerGroup, under the other
// value of the direction label, so that one command of one role divides
// into itself whichever way the request went.
type Answering struct {
	// role is the value of the role label of what it counts, the name of
	// the role in the configuration file.
	role     string
	measured *measurements
}

// NewAnswering registers the Diameter measurements in reg, the registry
// of the node of role, or finds them there when a role of that node has
// registered them already. role is one of the roles that count Diameter
// messages (measurements.go). Nil gives it a registry of its own, which
// nothing serves.
func NewAnswering(reg *metrics.Registry, role string) (*Answering, error) {
	if !slices.Contains(countingRoles.Values, role) {
		return nil, fmt.Errorf("diameter: %q is not a role that counts Diameter messages (%s)",
			role, strings.Join(countingRoles.Values, ", "))
	}
	if reg == nil {
		reg = metrics.NewRegistry()
	}
	measured, err := newMeasurements(reg)
	if err != nil {
		return nil, err
	}
	return &Answering{role: role, measured: measured}, nil
}

// RequestReceived counts request on its receipt, which is where TS
// 32.409 4.2.12.1, 4.1.1.3.2.1 and 4.1.7.1 count theirs. A role calls
// it as its Handler starts, before it has read the request, so that a
// request counts however it ends: a request the role cannot read, and
// one of a command it does not serve, are requests that reached it. A
// request that the node gives no role of its own, because no role of
// the node serves that application, reaches none and is not counted.
func (a *Answering) RequestReceived(request *diam.Message) {
	a.measured.requestReceived(a.role, request)
}

// AnswerSent counts the answer that the role gives back to request,
// which is where TS 32.409 4.2.12.2, 4.1.1.3.2.2, 4.1.1.3.2.3, 4.1.7.2
// and 4.1.7.3 count theirs. The successes and the failures of those
// clauses are told apart by the result label: the result that the
// answer carries, named as it is for an answer that came back.
func (a *Answering) AnswerSent(request, answer *diam.Message) {
	a.measured.answerSent(a.role, request, answer)
}

// resultOf is the name of the result that answer carries, for the
// result label. It reads Experimental-Result before Result-Code, as
// ResultAVPs.Result does, since that is where a 3GPP application puts
// its codes. An answer whose result is not named one by one, and one
// that carries neither AVP, counts under "other".
func resultOf(answer *diam.Message) string {
	result := resultOrExperimental(answer)
	for _, r := range resultNames {
		if r.result == result {
			return r.name
		}
	}
	return metrics.OtherValue
}

// resultOrExperimental is the result that answer carries: the
// Experimental-Result-Code when it has one, the Result-Code otherwise.
// FindAVP looks inside grouped AVPs, so it reaches the
// Experimental-Result-Code within Experimental-Result.
func resultOrExperimental(answer *diam.Message) Result {
	if a, err := answer.FindAVP(avp.ExperimentalResultCode, 0); err == nil {
		code, _ := a.Data.(datatype.Unsigned32)
		return Experimental(uint32(code))
	}
	if a, err := answer.FindAVP(avp.ResultCode, 0); err == nil {
		code, _ := a.Data.(datatype.Unsigned32)
		return Base(uint32(code))
	}
	// No result at all: Result{} is the result of no name, so it counts
	// under "other".
	return Result{}
}

// RegisterPeerUp registers ims_diameter_peer_up, which says of each
// peer whether its connection takes requests now. The gauge is read
// when the registry is scraped, from the state of each peer, so that no
// part of the node has to keep the number up to date
// (metrics.Registry.NewGaugeFunc).
//
// The label holds the Origin-Host of each peer, which comes from the
// configuration of the node, so the series of the gauge are as many as
// the peers the node is configured with. A node with no peer registers
// nothing: a label needs at least one value, and a node that connects
// to nobody has no peer to report.
//
// Two entries of one Origin-Host share one series, which is 1 while at
// least one of their connections takes requests: a request for that
// host can go out as long as one of them can carry it. The series is
// the greatest of their values and not the last one read, which is
// what a gauge func would otherwise keep.
func RegisterPeerUp(reg *metrics.Registry, peers []*Peer) error {
	if len(peers) == 0 {
		return nil
	}
	var hosts []string
	for _, p := range peers {
		if !slices.Contains(hosts, p.host) {
			hosts = append(hosts, p.host)
		}
	}
	read := slices.Clone(peers)
	return reg.NewGaugeFunc("ims_diameter_peer_up",
		"1 while a Diameter connection to the peer takes requests: it is open and the watchdog does not suspect it (RFC 3539 3.4.1).",
		func(emit func(value float64, labelValues ...string)) {
			up := make(map[string]float64, len(hosts))
			for _, p := range read {
				if p.Available() {
					up[p.host] = 1
				}
			}
			// Every host of a peer is in hosts, and one that no peer of it
			// reported up is 0.
			for _, host := range hosts {
				emit(up[host], host)
			}
		},
		metrics.Label{Name: "peer", Values: hosts})
}
