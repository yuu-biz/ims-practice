package pcscf

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/policy"
)

// This file is the P-CSCF as the Application Function of policy and
// charging control: what it asks of the PCRF through the
// policy.PolicyControl boundary, and what it does with the answers. The
// procedures over Rx are in TS 29.214 Annex A.

// SetPolicyControl gives the P-CSCF its policy control, such as an
// *rx.Client. Call it before the P-CSCF takes requests. Without it the
// P-CSCF asks nothing of a PCRF, and serves every UE as it serves one
// the PCRF does not know.
func (p *PCSCF) SetPolicyControl(pc policy.PolicyControl) { p.policy = pc }

// watchSignallingPath asks the PCRF, once a UE has registered, to tell
// the P-CSCF when the UE loses the bearer of its SIP signalling (TS
// 29.214 A.4, 4.4.5). The AAR follows the initial registration once it
// has succeeded, when the UE is authenticated and registered (TS 29.213
// B.1 steps 1 to 5). It returns the association with the policy session
// in it.
//
// A UE that does not come through the EPC, such as a softphone on the
// host, has no IP-CAN session that the PCRF could bind to. Its
// registration stays without a policy session, and the P-CSCF then asks
// nothing of the PCRF for its calls either.
func (p *PCSCF) watchSignallingPath(a Association, log *slog.Logger) Association {
	if p.policy == nil {
		return a
	}
	session, err := p.policy.SubscribeSignallingPath(context.Background(), a.Source.Addr.Addr())
	switch {
	case errors.Is(err, policy.ErrIPCANSessionNotAvailable):
		log.Info("the PCRF knows no IP-CAN session for the UE, serving it without policy control")
		return a
	case err != nil:
		log.Warn("could not subscribe to the status of the signalling path", "error", err.Error())
		return a
	}
	log.Info("subscribed to the status of the signalling path", "policy_session", string(session))
	// The registration may have changed, or ended, while the PCRF
	// answered, so the session goes into the association as it is now.
	// A session that the association cannot keep is released: no event
	// of it would find the UE.
	ctx := context.Background()
	current, ok, err := p.store.Association(ctx, a.Source)
	if err == nil && ok && time.Now().Before(current.Expires) && current.SignallingSession == "" {
		current.SignallingSession = session
		if err = p.store.PutAssociation(ctx, current); err == nil {
			return current
		}
	}
	if err != nil {
		log.Warn("the UE is served without policy control: the store failed", "error", err.Error())
	}
	p.releasePolicySession(session, log)
	return a
}

// unwatchSignallingPath cancels the subscription of a registration that
// has ended (TS 29.214 A.4), and takes the session out of a.
func (p *PCSCF) unwatchSignallingPath(a *Association, log *slog.Logger) {
	if a.SignallingSession == "" || p.policy == nil {
		return
	}
	// The STR must not hold up the SIP response that ends the
	// registration.
	go p.releasePolicySession(a.SignallingSession, log)
	a.SignallingSession = ""
}

func (p *PCSCF) releasePolicySession(session policy.SessionID, log *slog.Logger) {
	if err := p.policy.Release(context.Background(), session); err != nil {
		log.Warn("could not release the policy session", "policy_session", string(session), "error", err.Error())
	}
}
