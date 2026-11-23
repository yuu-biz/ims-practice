package pcscf

import (
	"log/slog"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds what the P-CSCF tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// A UE sends every request to the P-CSCF that it registered through, so
// the users of a P-CSCF do not move to another instance by themselves.
// An instance that the operator takes out of service refuses the
// REGISTERs that would start or refresh a registration through it, so
// that each of its UEs registers through another P-CSCF, at the latest
// when it refreshes its registration. Until then it serves the UEs
// registered through it: their calls, the requests within a dialog,
// and the deregistration that ends a registration.

// retryAfter is the Retry-After, in seconds, of the 503 with which the
// P-CSCF refuses a REGISTER while it is not UNLOCKED. With a Retry-After
// longer than its Timer F the UE marks this P-CSCF as unavailable for
// that time and may make its initial registration through another one;
// with a shorter one it goes on using this P-CSCF (TS 24.229 5.1.1.2.1
// and NOTE 18). Timer F is 64*T1, 128 s with the T1 of 2 s that TS
// 24.229 Table 7.7.1 gives the UE. No specification gives the value.
const retryAfter = 300

// Function is what the P-CSCF tells the maintenance API of its node. The
// P-CSCF needs no peer to take requests, as it finds an I-CSCF through
// DNS for each REGISTER, so it is ENABLED while the node runs. Its users
// are the IP associations of the UEs that use this instance (admissions):
// a shut down waits until the Store forgets the last of them, after the
// UE has registered through another P-CSCF or deregistered.
func (p *PCSCF) Function() maintenance.Function {
	return maintenance.Function{Role: role, Users: p.admissions.users}
}

// Manage gives the P-CSCF the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the P-CSCF is UNLOCKED.
func (p *PCSCF) Manage(m *maintenance.ManagedFunction) { p.managed.Store(m) }

// unlocked reports whether the operator lets the P-CSCF take new
// registrations.
func (p *PCSCF) unlocked() bool {
	m := p.managed.Load()
	return m == nil || m.AdministrativeState() == maintenance.Unlocked
}

// refusal is the 503 (Service Unavailable) with Retry-After (RFC 3261
// 21.5.4) with which the P-CSCF answers a REGISTER, an initial one or a
// refresh, while it is not UNLOCKED. The P-CSCF sends it itself, and the
// UE takes it as said of its P-CSCF (TS 24.229 5.1.1.2.1).
func (p *PCSCF) refusal(req *sip.Message, log *slog.Logger) *sip.Message {
	log.Info("refused a registration while not unlocked")
	resp := sip.NewResponse(req, 503, sip.NewTag())
	resp.Add("Retry-After", strconv.Itoa(retryAfter))
	return resp
}

// associationGone tells the maintenance API that an IP association has
// gone, which may be the last that a shut down waits for.
func (p *PCSCF) associationGone() {
	if m := p.managed.Load(); m != nil {
		m.UserQuit()
	}
}
