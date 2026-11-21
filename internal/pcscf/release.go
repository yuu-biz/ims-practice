package pcscf

import (
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the call release that the P-CSCF starts itself (TS
// 24.229 5.2.8.1), when the PCRF reports that a session has lost its
// resources.

// PolicyEvent receives what the PCRF reports about a policy session. It
// is the policy.Notify of the P-CSCF's policy control.
//
// Every event the P-CSCF asks for takes the resources of a session's
// media away: the bearer was released or lost, or could not be set up,
// or the PCRF ended the session because the UE's IP-CAN session ended
// (TS 29.214 4.4.6.1, 4.4.6.2). The P-CSCF then ends the session.
//
// An event may come twice, or cross the BYE of a UE. The dialog is then
// gone from the Store, and the event changes nothing. An event that the
// PCRF sends at once after the answer in which it authorised the
// session, such as a failed allocation for a UE that has no event
// stream open to the EPC stub, may also arrive before the P-CSCF has
// put the session in the dialog; it finds no dialog and is lost too.
//
// SIMPLIFIED: TS 24.229 5.2.8.1.1 and 5.2.8.1.2 item 1. The P-CSCF
// should wait an operator defined time for a SIP message that removes
// the media whose resources are gone, and release only then. A call
// here has one audio stream, so the P-CSCF releases at once.
func (p *PCSCF) PolicyEvent(e policy.Event) {
	log := p.log.With("policy_session", string(e.Session), "event", e.Kind.String())
	if e.Kind == policy.SuccessfulResourcesAllocation {
		return
	}
	d, ok := p.store.DialogOfPolicySession(e.Session)
	if !ok {
		if a, ok := p.store.AssociationOfPolicySession(e.Session); ok {
			p.signallingPathLost(a, e.Kind, log)
			return
		}
		log.Info("event for a policy session that has no dialog")
		return
	}
	if e.Kind == policy.SessionTerminated {
		// The PCRF has ended the session, so it needs no release. The
		// dialog stays; only its session goes, unless it has a new one.
		p.store.UpdateDialog(d.Key, func(d *Dialog) {
			if d.PolicySession == e.Session {
				d.PolicySession = ""
			}
		})
	}
	log.Info("the network took the resources of the session", "call_id", d.Key.CallID)
	p.release(d.Key, log)
}

// release ends the dialog of key from the P-CSCF. A dialog that is gone
// has been ended already.
//
// While the INVITE is still on its way, the P-CSCF answers it 500 and
// cancels it towards the callee (TS 24.229 5.2.8.1.1). Once the dialog
// is confirmed, it sends a BYE (5.2.8.1.2).
//
// One of the BYEs speaks for the callee, and a callee must not send a
// BYE before the ACK for its 2xx has come, or the server transaction
// has timed out (RFC 3261 15). Otherwise the BYE can overtake the 2xx,
// and the caller, which has no dialog yet, answers it 481 and stays in
// the call. So a dialog whose 2xx has had no ACK yet is only marked
// ReleasePending: the BYEs go out when the ACK passes the P-CSCF
// (forwardACK), or after ackWait without one. The mark is in the
// Store, so that the instance which forwards the ACK may be another
// one; the timer lives in the process, as every timer does.
func (p *PCSCF) release(key DialogKey, log *slog.Logger) {
	d, ok := p.store.UpdateDialog(key, func(d *Dialog) {
		if d.Confirmed && !d.Acknowledged {
			d.ReleasePending = true
		}
	})
	if !ok {
		return
	}
	if d.ReleasePending {
		log.Info("the dialog is released once the ACK for its 2xx has passed", "call_id", key.CallID)
		time.AfterFunc(p.ackWait, func() { p.releaseNow(key, log) })
		return
	}
	p.releaseNow(key, log)
}

// releaseNow is release once the BYEs may go out.
//
// SIMPLIFIED: TS 24.229 5.2.8.1.2, last paragraph, and 5.2.8.1.3. The
// P-CSCF forgets the dialog when it sends its BYEs, not when they are
// answered, so a request that a party sends in the dialog afterwards is
// forwarded like one of a dialog the P-CSCF does not keep, where it
// should be answered 481. The other party, which has the P-CSCF's BYE
// or is about to get it, ends the dialog all the same (RFC 3261 15.1.2).
func (p *PCSCF) releaseNow(key DialogKey, log *slog.Logger) {
	d, ok := p.store.TakeDialog(key)
	if !ok {
		return
	}
	log = log.With("call_id", key.CallID)
	switch {
	case p.abortSetup(key):
		log.Info("cancelling the session that is being set up")
	case d.Confirmed:
		p.sendBYEs(d, log)
	default:
		// An early dialog whose INVITE another P-CSCF instance forwards.
		// The PCRF sends the events of a session to the instance that
		// opened it, so this does not happen in practice.
		log.Warn("cannot cancel an INVITE that this P-CSCF instance does not hold")
	}
	p.free(d, log)
}

// sendBYEs releases a confirmed dialog (TS 24.229 5.2.8.1.2): a BYE to
// the other party in the name of the served UE (items 1 and 3).
//
// For lost resources the spec has the P-CSCF send only that BYE, and
// leaves the served UE to notice the lost bearer itself. Here the
// served UE gets a BYE too, in the name of the other party, built like
// the one of items 2 and 4 but with the Reason of the first BYE, as the
// cause is the same: after the network released only the voice
// bearer the UE still has its signalling, and without a BYE it would
// stay in a call that the network has ended. A UE that is gone leaves
// that BYE unanswered.
func (p *PCSCF) sendBYEs(d Dialog, log *slog.Logger) {
	p.sendBYE(releaseBYE(d, d.remote(), d.ue()), "the other party", log)
	p.sendBYE(releaseBYE(d, d.ue(), d.remote()), "the served UE", log)
}

// sendBYE sends bye, a BYE of TS 24.229 5.2.8.1.2 to the party that
// what names, and logs its answer.
func (p *PCSCF) sendBYE(bye *sip.Message, what string, log *slog.Logger) {
	dst, _, err := p.nextHop(bye)
	var stack *sip.Stack
	if err == nil {
		stack, err = p.stackToward(dst).Get()
	}
	if err == nil {
		var ct *sip.ClientTransaction
		if ct, err = stack.SendRequest(bye, dst); err == nil {
			log.Info("released the dialog with a BYE", "to", what, "target", bye.RequestURI)
			go logAnswer(ct, what, log)
			return
		}
	}
	log.Warn("could not send the BYE", "to", what, "error", err.Error())
}

// crossedCancel is RFC 3261 9.1 for a 2xx that crossed the CANCEL of an
// INVITE that the P-CSCF gave up (TS 24.229 5.2.8.1.1). The caller has
// its 500 and never sees the 2xx, so the P-CSCF acknowledges it in the
// caller's name, the first 2xx and each retransmission of it (RFC 3261
// 13.2.2.4). The 2xx has confirmed a dialog whose session has no
// resources, and the P-CSCF releases it with the BYE of TS 24.229
// 5.2.8.1.2 to the callee, once: first is true for the first 2xx of
// the dialog. The BYE follows the ACK, as RFC 3261 15 asks of a caller.
func (s *setup) crossedCancel(resp *sip.Message, first bool) {
	toCallee := []string(nil)
	if _, route, ok := routeSets(resp.GetAll("Record-Route"), len(s.invite.GetAll("Record-Route"))); ok {
		toCallee = route
	}
	d := Dialog{Key: DialogKey{CallID: s.invite.Get("Call-ID")}}
	d.Caller = Party{NameAddr: s.invite.Get("From")}
	if cseq, err := sip.ParseCSeq(s.invite.Get("CSeq")); err == nil {
		d.Caller.CSeq = cseq.Number
	}
	d.Callee = Party{NameAddr: resp.Get("To"), Target: contactOf(resp), Route: toCallee}

	ack := inDialogRequest(sip.ACK, d.Caller.CSeq, d, &d.Callee, &d.Caller)
	dst, _, err := s.p.nextHop(ack)
	var stack *sip.Stack
	if err == nil {
		stack, err = s.p.stackToward(dst).Get()
	}
	if err == nil {
		err = stack.SendACK(ack, dst)
	}
	if err != nil {
		s.log.Warn("could not acknowledge the 2xx that crossed the CANCEL", "error", err.Error())
		return
	}
	if first {
		s.log.Info("acknowledged the 2xx that crossed the CANCEL")
		s.p.sendBYE(releaseBYE(d, &d.Callee, &d.Caller), "the callee", s.log)
	}
}

func logAnswer(ct *sip.ClientTransaction, what string, log *slog.Logger) {
	for resp := range ct.Responses() {
		if resp.StatusCode >= 200 {
			log.Info("BYE answered", "to", what, "status", resp.StatusCode)
			return
		}
	}
	log.Warn("BYE not answered", "to", what, "error", ct.Err().Error())
}

// releaseBYE is the BYE of TS 24.229 5.2.8.1.2 to one party of the
// dialog in the name of the other, from what the Dialog saved: the
// Request-URI is the Contact of the party, To and From are those of the
// INVITE and its 200, the Route is the route to the party, and CSeq is
// one above the last request from the party it speaks for.
func releaseBYE(d Dialog, to, from *Party) *sip.Message {
	m := inDialogRequest(sip.BYE, from.CSeq+1, d, to, from)
	m.Add("Reason", reasonServiceUnavailable)
	return m
}

// inDialogRequest is a request of method to one party of the dialog in
// the name of the other, with the CSeq number seq (RFC 3261 12.2.1.1).
func inDialogRequest(method sip.Method, seq uint32, d Dialog, to, from *Party) *sip.Message {
	m := &sip.Message{Method: method, RequestURI: to.Target}
	for _, r := range to.Route {
		m.Add("Route", r)
	}
	m.Add("Max-Forwards", "70")
	m.Add("From", from.NameAddr)
	m.Add("To", to.NameAddr)
	m.Add("Call-ID", d.Key.CallID)
	m.Add("CSeq", sip.CSeq{Number: seq, Method: method}.String())
	return m
}

// reasonServiceUnavailable is the Reason of the BYE of TS 24.229
// 5.2.8.1.2 items 1 b and 3 b, and of the CANCEL of 5.2.8.1.1: the
// resources are gone, and the entity that controls them gave no cause of
// its own, so the P-CSCF gives the SIP status code 503 (RFC 3326 2).
const reasonServiceUnavailable = `SIP;cause=503;text="Service Unavailable"`

// signallingPathLost is the event that the UE of association a has lost
// the bearer of its SIP signalling (TS 29.214 4.4.6.3), or its whole
// IP-CAN session (4.4.6.1). From now on the P-CSCF refuses requests to
// the UE (TS 24.229 5.2.6.4.3 item 1, 5.2.6.4.7 item 1), until the UE
// registers again. The policy session has done its work and is released
// (TS 29.214 4.4.6.3), unless the PCRF has ended it already.
//
// SIMPLIFIED: TS 24.229 5.2.8.1.1 and 5.2.8.1.2 item 2. The P-CSCF
// should also cancel or release every session of the UE. It keeps no
// index of the dialogs by UE, and leaves that to the events of the
// sessions themselves: the PCRF ends the policy session of each of them
// together with this one (TS 29.214 4.4.6.1). A session that is being
// set up and has no SDP answer yet has no policy session, and ends when
// its INVITE is not answered.
func (p *PCSCF) signallingPathLost(a Association, kind policy.EventKind, log *slog.Logger) {
	log.Info("the UE lost its signalling bearer", "private_identity", a.PrivateIdentity, "source", a.Source.String())
	if kind == policy.SessionTerminated {
		a.SignallingSession = ""
	}
	p.unwatchSignallingPath(&a, log)
	a.SignallingLost = true
	p.store.PutAssociation(a)
}
