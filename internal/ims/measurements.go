package ims

import (
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// This file holds the performance measurements of TS 32.409 that more
// than one role counts. A role counts under its own value of the role
// label, its name in the configuration file. The roles of one node
// register the same definitions in the registry of the node, which
// gives them all the same metric (internal/metrics).

// RegistrationType is the type of a registration procedure, the type
// label of the registration measurements.
type RegistrationType string

const (
	// InitialRegistration is TS 32.409 4.1.1.1: a REGISTER of a user
	// that is not registered (TS 23.228 5.2.2.3).
	InitialRegistration RegistrationType = "initial"
	// ReRegistration is TS 32.409 4.1.1.2: a REGISTER of a registered
	// user (TS 23.228 5.2.2.4).
	ReRegistration RegistrationType = "re"
	// DeRegistration is TS 32.409 4.1.1.3.1: a REGISTER with an expiry
	// of 0 (TS 23.228 5.3.1).
	DeRegistration RegistrationType = "de"
)

// RegistrationTypeOf is the type of a REGISTER that asks for expires,
// of a user who is registered or not.
func RegistrationTypeOf(expires time.Duration, registered bool) RegistrationType {
	switch {
	case expires == 0:
		return DeRegistration
	case registered:
		return ReRegistration
	default:
		return InitialRegistration
	}
}

var (
	// registrationRoles are the functions that TS 32.409 4.1.1.1 to
	// 4.1.1.3.1 has count the registration procedures.
	registrationRoles = metrics.Label{Name: "role", Values: []string{"pcscf", "scscf"}}
	registrationTypes = metrics.Label{Name: "type", Values: []string{
		string(InitialRegistration), string(ReRegistration), string(DeRegistration),
	}}
	// registrationStatuses are the status codes of a failed registration
	// that are counted one by one; any other one counts under "other",
	// so that the codes add up to all the failures (TS 32.409
	// 4.1.1.1.3). Among them is the 481 (Call/Transaction Does Not
	// Exist) that a deregistration gets when the user has no
	// registration to remove (TS 24.229 5.4.1.4.1): the S-CSCF sends it
	// and the P-CSCF passes it on, so it is a failure that this network
	// produces by itself, and one worth telling from the rest.
	registrationStatuses = metrics.Label{Name: "status", Values: []string{
		"400", "403", "404", "408", "420", "423", "480", "481", "500", "503", "504",
	}}
)

// Registrations are the registration measurements of TS 32.409 4.1.1:
// how many registration procedures of each type started, succeeded and
// failed. A procedure is the REGISTER that starts it and, when the
// S-CSCF challenges that one, the REGISTER that answers the challenge:
// the 401 between them is a step of the procedure, not its end.
type Registrations struct {
	attempts  *metrics.Counter
	successes *metrics.Counter
	failures  *metrics.Counter
}

// NewRegistrations registers the registration measurements in reg, or
// finds them there when another role of the node has registered them.
func NewRegistrations(reg *metrics.Registry) (*Registrations, error) {
	m := &Registrations{}
	var err error
	if m.attempts, err = reg.NewCounter("ims_registration_attempts_total",
		"REGISTER requests that start a registration procedure, by type (TS 32.409 UR.AttInitReg, UR.AttReReg, UR.AttDeRegUe).",
		registrationRoles, registrationTypes); err != nil {
		return nil, err
	}
	if m.successes, err = reg.NewCounter("ims_registration_successes_total",
		"200 (OK) responses that end a registration procedure, by type (TS 32.409 UR.SuccInitReg, UR.SuccReReg, UR.SuccDeRegUe).",
		registrationRoles, registrationTypes); err != nil {
		return nil, err
	}
	if m.failures, err = reg.NewCounter("ims_registration_failures_total",
		"Final responses other than 2xx and 401 that end a registration procedure, by type and status code (TS 32.409 UR.FailInitReg.Cause, UR.FailReReg.Cause, UR.FailDeRegUe.Cause).",
		registrationRoles, registrationTypes, registrationStatuses); err != nil {
		return nil, err
	}
	return m, nil
}

// Started counts the REGISTER that starts a procedure of type t at
// role, on receipt (TS 32.409 4.1.1.1.1, 4.1.1.2.1, 4.1.1.3.1.1).
func (m *Registrations) Started(role string, t RegistrationType) {
	m.attempts.Inc(role, string(t))
}

// Answered counts the final response that role sent to a REGISTER of a
// procedure of type t: a 2xx is the success of the procedure
// (4.1.1.1.2, 4.1.1.2.2, 4.1.1.3.1.2), and any other final response but
// a 401 its failure (4.1.1.1.3, 4.1.1.2.3, 4.1.1.3.1.3). The 401 asks
// for the REGISTER that goes on with the procedure.
func (m *Registrations) Answered(role string, t RegistrationType, status int) {
	switch {
	case status == 401 || status < 200:
	case status < 300:
		m.successes.Inc(role, string(t))
	default:
		m.failures.Inc(role, string(t), strconv.Itoa(status))
	}
}

// SessionDirection is the direction of a session establishment, the
// direction label of the session measurements: originating for an
// initial INVITE from the UE (TS 32.409 4.1.2.9), terminating for one
// to the UE (4.1.2.11).
type SessionDirection string

const (
	OriginatingSession SessionDirection = "originating"
	TerminatingSession SessionDirection = "terminating"
)

var (
	// sessionRoles are the functions that TS 32.409 4.1.2.1 to 4.1.2.12
	// has count the session establishments.
	sessionRoles      = metrics.Label{Name: "role", Values: []string{"pcscf", "icscf", "scscf"}}
	sessionDirections = metrics.Label{Name: "direction", Values: []string{string(OriginatingSession), string(TerminatingSession)}}
	// sessionResponses are the message types that TS 32.409 4.1.2.2
	// splits the successful session establishments by: the 180
	// (Ringing), or the 200 (OK) when no 180 went before it.
	sessionResponses = metrics.Label{Name: "response", Values: []string{"180", "200"}}
	// sessionStatuses are the status codes of a failed session
	// establishment that are counted one by one; any other one counts
	// under "other", so that the codes add up to all the failures (TS
	// 32.409 4.1.2.4).
	sessionStatuses = metrics.Label{Name: "status", Values: []string{
		"400", "403", "404", "408", "410", "480", "484", "486", "487", "488", "500", "503", "504", "580", "600", "603",
	}}
)

// Sessions are the session establishment measurements of TS 32.409
// 4.1.2: the initial INVITEs, how they ended, and the sessions that
// they set up and that have not ended yet.
type Sessions struct {
	attempts  *metrics.Counter
	successes *metrics.Counter
	answers   *metrics.Counter
	failures  *metrics.Counter
	answered  *metrics.Gauge
}

// NewSessions registers the session measurements in reg, or finds them
// there when another role of the node has registered them.
func NewSessions(reg *metrics.Registry) (*Sessions, error) {
	m := &Sessions{}
	var err error
	if m.attempts, err = reg.NewCounter("ims_session_attempts_total",
		"Initial INVITE requests received, by direction (TS 32.409 SC.AttSession, SC.AttSessionOrig, SC.AttSessionTerm).",
		sessionRoles, sessionDirections); err != nil {
		return nil, err
	}
	if m.successes, err = reg.NewCounter("ims_session_successes_total",
		"First responses sent to an initial INVITE that tell of a successful session establishment, by direction and by the type of that message (TS 32.409 SC.SuccSession.type, SC.SuccSessionOrig.type, SC.SuccSessionTerm.type).",
		sessionRoles, sessionDirections, sessionResponses); err != nil {
		return nil, err
	}
	if m.answers, err = reg.NewCounter("ims_session_answers_total",
		"First 200 (OK) responses sent to an initial INVITE, by direction (TS 32.409 SC.AnsSession).",
		sessionRoles, sessionDirections); err != nil {
		return nil, err
	}
	if m.failures, err = reg.NewCounter("ims_session_failures_total",
		"Final responses other than 2xx sent to an initial INVITE, by direction and status code (TS 32.409 SC.FailSession.Cause).",
		sessionRoles, sessionDirections, sessionStatuses); err != nil {
		return nil, err
	}
	if m.answered, err = reg.NewGauge("ims_answered_sessions",
		"Sessions that a 200 (OK) to the initial INVITE has set up and that have not ended; max_over_time and avg_over_time give TS 32.409 SC.NbrSimulAnsSessionMax and SC.NbrSimulAnsSessionMean.",
		sessionRoles); err != nil {
		return nil, err
	}
	return m, nil
}

// Started counts an initial INVITE that role received, on receipt (TS
// 32.409 4.1.2.1, 4.1.2.9, 4.1.2.11).
func (m *Sessions) Started(role string, d SessionDirection) {
	m.attempts.Inc(role, string(d))
}

// Succeeded counts the session establishment that role answered with
// status, the first response it sent to the initial INVITE that tells of
// a success: the 180 (Ringing), or the 2xx when no 180 went before it
// (sip.ServerTransaction.ClaimFirstSuccess). Summed over the
// directions it is the successful session establishments of TS 32.409
// 4.1.2.2, which the P-CSCF and the S-CSCF count; the originating
// direction at the P-CSCF is the response that goes to the UE
// (4.1.2.10), and the terminating direction at the P-CSCF and the
// I-CSCF the response that goes towards the S-CSCF that started the
// session (4.1.2.12).
//
// The spread over the message types is that of 4.1.2.2: the response
// label is "180" or "200", and the two add up to the successes.
func (m *Sessions) Succeeded(role string, d SessionDirection, status int) {
	m.successes.Inc(role, string(d), strconv.Itoa(status))
}

// Answered counts the 2xx that role sends to an initial INVITE (TS
// 32.409 4.1.2.3): the first, not its retransmissions.
func (m *Sessions) Answered(role string, d SessionDirection) {
	m.answers.Inc(role, string(d))
}

// Failed counts the final response other than 2xx that role sends to an
// initial INVITE, by its status code (TS 32.409 4.1.2.4).
func (m *Sessions) Failed(role string, d SessionDirection, status int) {
	m.failures.Inc(role, string(d), strconv.Itoa(status))
}

// SetUp and Ended count a session in and out of the answered sessions
// that TS 32.409 4.1.2.5 and 4.1.2.7 sample: from the 200 (OK) to the
// initial INVITE to the end of the session. They need not be called in
// the same process: the gauges of a role's instances add up to the
// sessions of the network.
func (m *Sessions) SetUp(role string) { m.answered.Inc(role) }

func (m *Sessions) Ended(role string) { m.answered.Dec(role) }
