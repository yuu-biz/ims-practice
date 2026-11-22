package scscf

import (
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// This file holds the registration measurements of the S-CSCF, TS
// 32.409 4.1.1: how many registration procedures of each type started,
// succeeded and failed. A procedure is the REGISTER that starts it and,
// when the S-CSCF challenges that one, the REGISTER that answers the
// challenge: the 401 between them is a step of the procedure, not its
// end.

// role is the name of the role in the configuration file, the value of
// the role label of its metrics.
const role = "scscf"

// registrationType is the type of a registration procedure, the type
// label of the measurements.
type registrationType string

const (
	// initialRegistration is TS 32.409 4.1.1.1: a REGISTER of a user
	// that the S-CSCF holds no registration of (TS 23.228 5.2.2.3).
	initialRegistration registrationType = "initial"
	// reRegistration is TS 32.409 4.1.1.2: a REGISTER of a registered
	// user (TS 23.228 5.2.2.4).
	reRegistration registrationType = "re"
	// deRegistration is TS 32.409 4.1.1.3.1: a REGISTER with an expiry
	// of 0 (TS 23.228 5.3.1).
	deRegistration registrationType = "de"
)

// registrationTypeOf is the type of a REGISTER that asks for expires,
// of a user who is registered or not.
func registrationTypeOf(expires time.Duration, registered bool) registrationType {
	switch {
	case expires == 0:
		return deRegistration
	case registered:
		return reRegistration
	default:
		return initialRegistration
	}
}

var (
	roleLabel = metrics.Label{Name: "role", Values: []string{role}}
	typeLabel = metrics.Label{Name: "type", Values: []string{
		string(initialRegistration), string(reRegistration), string(deRegistration),
	}}
	// statusLabel are the status codes of a failed registration that are
	// counted one by one; any other one counts under "other", so that
	// the codes add up to all the failures (TS 32.409 4.1.1.1.3).
	statusLabel = metrics.Label{Name: "status", Values: []string{
		"400", "403", "404", "408", "420", "423", "480", "500", "503", "504",
	}}
)

// registrationMetrics are the counters of the registration procedures.
type registrationMetrics struct {
	attempts  *metrics.Counter
	successes *metrics.Counter
	failures  *metrics.Counter
}

func newRegistrationMetrics(reg *metrics.Registry) (*registrationMetrics, error) {
	m := &registrationMetrics{}
	var err error
	if m.attempts, err = reg.NewCounter("ims_registration_attempts_total",
		"REGISTER requests that start a registration procedure, by type (TS 32.409 UR.AttInitReg, UR.AttReReg, UR.AttDeRegUe).",
		roleLabel, typeLabel); err != nil {
		return nil, err
	}
	if m.successes, err = reg.NewCounter("ims_registration_successes_total",
		"200 (OK) responses that end a registration procedure, by type (TS 32.409 UR.SuccInitReg, UR.SuccReReg, UR.SuccDeRegUe).",
		roleLabel, typeLabel); err != nil {
		return nil, err
	}
	if m.failures, err = reg.NewCounter("ims_registration_failures_total",
		"Final responses other than 2xx and 401 that end a registration procedure, by type and status code (TS 32.409 UR.FailInitReg.Cause, UR.FailReReg.Cause, UR.FailDeRegUe.Cause).",
		roleLabel, typeLabel, statusLabel); err != nil {
		return nil, err
	}
	return m, nil
}

// started counts the REGISTER that starts a procedure of type t, on
// receipt (TS 32.409 4.1.1.1.1, 4.1.1.2.1, 4.1.1.3.1.1).
func (m *registrationMetrics) started(t registrationType) {
	m.attempts.Inc(role, string(t))
}

// answered counts the final response that the S-CSCF sent to a REGISTER
// of a procedure of type t: a 2xx is the success of the procedure
// (4.1.1.1.2, 4.1.1.2.2, 4.1.1.3.1.2), and any other final response but
// a 401 its failure (4.1.1.1.3, 4.1.1.2.3, 4.1.1.3.1.3). The 401 asks
// for the REGISTER that goes on with the procedure.
func (m *registrationMetrics) answered(t registrationType, status int) {
	switch {
	case status == 401 || status < 200:
	case status < 300:
		m.successes.Inc(role, string(t))
	default:
		m.failures.Inc(role, string(t), strconv.Itoa(status))
	}
}
