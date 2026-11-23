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
	// 4.1.1.1.3).
	registrationStatuses = metrics.Label{Name: "status", Values: []string{
		"400", "403", "404", "408", "420", "423", "480", "500", "503", "504",
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
