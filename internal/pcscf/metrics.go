package pcscf

import (
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the P-CSCF counts for the operator: the
// registration procedures and the set-up time of the initial ones (TS
// 32.409 4.1.1).

// role is the name of the role in the configuration file: the value of
// the role label of what the P-CSCF counts, and the role of its
// maintenance function.
const role = "pcscf"

// measurements are the counters of one P-CSCF instance.
type measurements struct {
	registrations *ims.Registrations
	// registrationSetup gives UR.MeanInitRegSetupTime, the mean set-up
	// time of the successful initial registrations at the P-CSCF (TS
	// 32.409 4.1.1.1.4): its sum divided by its count.
	registrationSetup *metrics.Histogram
	// challenged holds each registration procedure that a 401 has
	// paused, by the nonce of the challenge, until the REGISTER that
	// answers it comes or the S-CSCF stops waiting for it. The procedure
	// lives in the instance that passed the 401 on, as a transaction
	// does: the UE sends the REGISTER that answers to the P-CSCF that it
	// registers through, and under IMS AKA over the security
	// associations that this 401 set up with it. A P-CSCF that starts
	// again in between counts that REGISTER as a procedure of its own. A
	// procedure that the UE gives up after the 401 counts as an attempt
	// with no success or failure, as at the S-CSCF.
	challenged sync.Map // nonce -> *registrationProcedure
}

func newMeasurements(reg *metrics.Registry) (*measurements, error) {
	registrations, err := ims.NewRegistrations(reg)
	if err != nil {
		return nil, err
	}
	setup, err := reg.NewHistogram("ims_registration_setup_seconds",
		"Time from the REGISTER that starts a successful initial registration to its 200 (OK), at the P-CSCF; the sum divided by the count is TS 32.409 UR.MeanInitRegSetupTime.",
		metrics.DefaultBuckets(), metrics.Label{Name: "role", Values: []string{role}})
	if err != nil {
		return nil, err
	}
	return &measurements{registrations: registrations, registrationSetup: setup}, nil
}

// registrationProcedure is one registration procedure at the P-CSCF: its
// type, and when the REGISTER that started it arrived.
type registrationProcedure struct {
	kind     ims.RegistrationType
	received time.Time
}

// registrationOf is the registration procedure of req, a REGISTER with
// creds that arrived at received. A REGISTER that names the nonce of a
// 401 that the P-CSCF passed on answers that challenge, and goes on with
// the procedure that the challenged REGISTER started, as at the S-CSCF.
// Any other one starts a procedure, counted on receipt (TS 32.409
// 4.1.1.1.1, 4.1.1.2.1, 4.1.1.3.1.1): a deregistration when it asks for
// an expiry of 0, a re-registration when the UE has an IP association
// with the P-CSCF (associated), and an initial registration otherwise.
func (m *measurements) registrationOf(req *sip.Message, creds *sip.Credentials, associated bool, received time.Time) *registrationProcedure {
	if creds != nil && creds.Nonce != "" {
		if proc, ok := m.challenged.LoadAndDelete(creds.Nonce); ok {
			return proc.(*registrationProcedure)
		}
	}
	seconds, _, err := sip.RequestedExpires(req)
	if err != nil {
		// No deregistration: the registrar answers what it cannot read.
		seconds = sip.NoExpires
	}
	proc := &registrationProcedure{kind: ims.RegistrationTypeOf(time.Duration(seconds)*time.Second, associated), received: received}
	m.registrations.Started(role, proc.kind)
	return proc
}

// registrationAnswered counts resp, the final response that the P-CSCF
// sends to a REGISTER of proc (ims.Registrations.Answered). A 401 pauses
// the procedure for as long as the S-CSCF waits for the REGISTER that
// answers it: reg-await-auth (TS 24.229 5.4.1.2.1 step 7). The 200 (OK)
// that ends an initial registration gives its set-up time. TS 32.409
// 4.1.1.1.4 measures it from the REGISTER to the 200 (OK) of the
// initial registration; with a challenge in between, that is from the
// REGISTER that started the procedure, since the attempts count
// procedures.
func (m *measurements) registrationAnswered(proc *registrationProcedure, resp *sip.Message) {
	m.registrations.Answered(role, proc.kind, resp.StatusCode)
	switch {
	case resp.StatusCode == 401:
		for _, v := range resp.GetAll("WWW-Authenticate") {
			if ch, err := sip.ParseChallenge(v); err == nil && ch.Nonce != "" {
				m.challenged.Store(ch.Nonce, proc)
				time.AfterFunc(ims.RegAwaitAuth, func() { m.challenged.CompareAndDelete(ch.Nonce, proc) })
			}
		}
	case resp.StatusCode/100 == 2 && proc.kind == ims.InitialRegistration:
		m.registrationSetup.Observe(time.Since(proc.received).Seconds(), role)
	}
}
