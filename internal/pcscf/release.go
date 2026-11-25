package pcscf

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
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
// released already or gone from the Store, and the event changes
// nothing. An event that the
// PCRF sends at once after the answer in which it authorised the
// session, such as a failed allocation for a UE that has no event
// stream open to the EPC stub, may also arrive before the P-CSCF has
// put the session in the dialog; it finds no dialog and is lost too.
//
// The PCRF has its answer before the P-CSCF hears of the event
// (policy.Notify), so no request waits when the Store fails here. The
// P-CSCF logs it, and the session stays until a party ends it.
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
	ctx := context.Background()
	lost := func(err error) {
		log.Warn("the event is lost: the store failed", "error", err.Error())
	}
	d, ok, err := p.store.DialogOfPolicySession(ctx, e.Session)
	if err != nil {
		lost(err)
		return
	}
	if !ok {
		a, ok, err := p.store.AssociationOfPolicySession(ctx, e.Session)
		switch {
		case err != nil:
			lost(err)
		case ok:
			p.signallingPathLost(a, e.Kind, log)
		default:
			log.Info("event for a policy session that has no dialog")
		}
		return
	}
	if e.Kind == policy.SessionTerminated {
		// The PCRF has ended the session, so it needs no release. The
		// dialog stays; only its session goes, unless it has a new one.
		_, _, err := p.store.UpdateDialog(ctx, d.Key, func(d *Dialog) {
			if d.PolicySession == e.Session {
				d.PolicySession = ""
			}
		})
		if err != nil {
			lost(err)
			return
		}
	}
	log.Info("the network took the resources of the session", "call_id", d.Key.CallID)
	p.release(d.Key, nil, false, log)
}

// release ends the dialog of key from the P-CSCF, for why: nil when the
// network took the resources of the session away, or errRelay when the
// media relay did not take an answer. A dialog that is gone has been
// ended already, and a Released one is being ended.
//
// While the INVITE is still on its way, the P-CSCF answers it 500 and
// cancels it towards the callee (TS 24.229 5.2.8.1.1), as proxyInvite
// does for why. Once the dialog is confirmed, it sends a BYE
// (5.2.8.1.2), the same for every why.
//
// One of the BYEs speaks for the callee, and a callee must not send a
// BYE before the ACK for its 2xx has come, or the server transaction
// has timed out (RFC 3261 15). Otherwise the BYE can overtake the 2xx,
// and the caller, which has no dialog yet, answers it 481 and stays in
// the call. So a dialog whose 2xx has had no ACK yet is only marked
// ReleasePending: the BYEs go out when the ACK passes the P-CSCF
// (forwardACK), or after ackWait without one. afterACK asks for the
// same wait for the 2xx to a re-INVITE that is on its way (renegotiate).
// The mark is in the Store, so that the instance which forwards the ACK
// may be another one. This wait is one of the few that stay in the
// process rather than going into a time index of the Store
// (ExpiryKind): it is short, and the 2xx it waits for has just passed
// this instance.
func (p *PCSCF) release(key DialogKey, why error, afterACK bool, log *slog.Logger) {
	d, ok, err := p.store.UpdateDialog(context.Background(), key, func(d *Dialog) {
		if d.Confirmed && (afterACK || !d.Acknowledged) {
			d.ReleasePending = true
		}
	})
	if err != nil {
		log.Warn("the dialog is not released: the store failed", "call_id", key.CallID, "error", err.Error())
		return
	}
	if !ok || d.Released {
		return
	}
	if d.ReleasePending {
		log.Info("the dialog is released once the ACK for its 2xx has passed", "call_id", key.CallID)
		time.AfterFunc(p.ackWait, func() { p.releaseNow(key, why, log) })
		return
	}
	p.releaseNow(key, why, log)
}

// releaseNow is release once the BYEs may go out. It marks the dialog
// Released, once however many events ask for it, and frees what the
// dialog holds. The dialog stays in the Store until the release is over,
// so that the P-CSCF answers a request within it 481 meanwhile (TS
// 24.229 5.2.8.1.3, subsequent): until the BYEs are answered for a
// confirmed dialog (sendBYEs), and until its INVITE has ended for an
// early one (setup.end). why is that of release.
func (p *PCSCF) releaseNow(key DialogKey, why error, log *slog.Logger) {
	first := false
	d, ok, err := p.store.UpdateDialog(context.Background(), key, func(d *Dialog) {
		first = !d.Released
		d.Released = true
	})
	if err != nil {
		log.Warn("the dialog is not released: the store failed", "call_id", key.CallID, "error", err.Error())
		return
	}
	if !ok || !first {
		return
	}
	log = log.With("call_id", key.CallID)
	switch {
	case p.abortSetup(d, why):
		log.Info("cancelling the session that is being set up")
	case d.Confirmed:
		p.sendBYEs(d, log)
	default:
		// An early dialog whose INVITE another instance forwards. Only
		// that instance can answer the INVITE 500 and cancel it. The
		// dialog stays in the Store, Released, and that instance ends the
		// INVITE with the next response to it (setup.response).
		//
		// SIMPLIFIED: TS 24.229 5.2.8.1.1. The instances share the Store
		// and do not ask one another, so the INVITE is cancelled only
		// when its next response reaches the instance that forwards it,
		// at the latest its 2xx, and goes on until then without media
		// through the relay. The PCRF sends the events of a session to
		// the instance that opened it, so the release that comes here is
		// that of a request within the early dialog that passed another
		// instance, such as an UPDATE whose answer the relay did not take.
		// In the preconditions of a VoLTE call, the 180 comes right after
		// the 200 to that UPDATE.
		log.Info("the P-CSCF instance that forwards the INVITE cancels it with its next response")
	}
	p.free(d, log)
}

// sendBYEs releases a confirmed dialog (TS 24.229 5.2.8.1.2): a BYE to
// the other party in the name of the served UE (items 1 and 3).
//
// SIMPLIFIED: TS 24.229 5.2.8.1.2. For lost resources the clause has
// the P-CSCF send only that BYE, and leaves the served UE to notice the
// lost bearer itself. Here the served UE gets a BYE too, in the name of
// the other party, built like the one of items 2 and 4 but with the
// Reason of the first BYE, as the cause is the same. After the network
// released only the voice bearer the UE still has its signalling, and a
// UE that does not act on the loss of that bearer would, without a BYE,
// stay in a call that the network has ended. A UE that is gone leaves
// that BYE unanswered.
//
// Once both BYEs have their answers, the P-CSCF deletes the dialog (the
// last paragraph of 5.2.8.1.2). It does so after an answer other than
// 2xx, or none, too: a UAC takes the dialog as ended after a 481, a 408
// or a timeout of its BYE (RFC 3261 15.1.1), so nothing would delete it
// later.
func (p *PCSCF) sendBYEs(d Dialog, log *slog.Logger) {
	toRemote := p.sendBYE(releaseBYE(d, d.remote(), d.ue()), "the other party", log)
	toUE := p.sendBYE(releaseBYE(d, d.ue(), d.remote()), "the served UE", log)
	go func() {
		logAnswer(toRemote, "the other party", log)
		logAnswer(toUE, "the served UE", log)
		p.endDialog(d.Key, d, log)
	}()
}

// sendBYE sends bye, a BYE of TS 24.229 5.2.8.1.2 to the party that
// what names. It returns the client transaction of the BYE, or nil when
// the BYE could not go out.
func (p *PCSCF) sendBYE(bye *sip.Message, what string, log *slog.Logger) *sip.ClientTransaction {
	targets, _, err := p.nextHop(bye)
	var ref *ims.StackRef
	if err == nil {
		ref, err = p.stackToward(targets[0])
	}
	var stack *sip.Stack
	if err == nil {
		stack, err = ref.Get()
	}
	if err == nil {
		var ct *sip.ClientTransaction
		if ct, err = stack.SendRequest(bye, targets...); err == nil {
			log.Info("released the dialog with a BYE", "to", what, "target", bye.RequestURI)
			return ct
		}
	}
	log.Warn("could not send the BYE", "to", what, "error", err.Error())
	return nil
}

// crossedCancel is RFC 3261 9.1 for a 2xx that crossed the CANCEL of an
// INVITE that the P-CSCF gave up (TS 24.229 5.2.8.1.1). The caller has
// its 500 and never sees the 2xx, so the P-CSCF acknowledges it in the
// caller's name, the first 2xx and each retransmission of it (RFC 3261
// 13.2.2.4). The 2xx has confirmed a dialog whose session has no
// resources, and the P-CSCF releases it with the BYE of TS 24.229
// 5.2.8.1.2 to the callee, once: first is true until a call for the
// dialog has reported true, which it does once it has sent the ACK and
// the BYE. The BYE follows the ACK, as RFC 3261 15 asks of a caller.
// At the P-CSCF of the callee, the callee is the served UE, which gets
// the BYE that sendBYEs also sends a served UE.
func (s *setup) crossedCancel(resp *sip.Message, first bool) bool {
	toCallee := []string(nil)
	if _, route, ok := routeSets(resp.GetAll("Record-Route"), len(s.invite.GetAll("Record-Route"))); ok {
		toCallee = route
	}
	d := Dialog{Key: DialogKey{CallID: s.invite.Get("Call-ID")}}
	d.Caller = Party{NameAddr: s.invite.Get("From")}
	inviteSeq := uint32(0)
	if cseq, err := sip.ParseCSeq(s.invite.Get("CSeq")); err == nil {
		inviteSeq = cseq.Number
	}
	d.Callee = Party{NameAddr: resp.Get("To"), Target: contactOf(resp), Route: toCallee}

	ack := inDialogRequest(sip.ACK, inviteSeq, d, &d.Callee, &d.Caller)
	targets, _, err := s.p.nextHop(ack)
	var ref *ims.StackRef
	if err == nil {
		ref, err = s.p.stackToward(targets[0])
	}
	var stack *sip.Stack
	if err == nil {
		stack, err = ref.Get()
	}
	if err == nil {
		err = stack.SendACK(ack, targets...)
	}
	if err != nil {
		s.log.Warn("could not acknowledge the 2xx that crossed the CANCEL", "error", err.Error())
		return false
	}
	if first {
		s.log.Info("acknowledged the 2xx that crossed the CANCEL")
		// The BYE takes a number above every request of the caller,
		// such as a PRACK or an UPDATE in the early dialog (RFC 3261
		// 12.2.1.1).
		d.Caller.CSeq = max(inviteSeq, s.lastCallerSeq())
		go logAnswer(s.p.sendBYE(releaseBYE(d, &d.Callee, &d.Caller), "the callee", s.log), "the callee", s.log)
	}
	return true
}

// lastCallerSeq is the CSeq of the last request that the caller sent in
// the early dialog: from the Dialog when it is still in the Store, or
// else as callerSeq keeps it.
func (s *setup) lastCallerSeq() uint32 {
	if !s.stored {
		return s.callerSeq
	}
	d, ok, err := s.p.store.UpdateDialog(context.Background(), s.key, func(*Dialog) {})
	if err != nil {
		// The BYE may then take a number that the callee has seen
		// already, and the callee answers it 500 (RFC 3261 12.2.2) and
		// stays in the call until its user ends it.
		s.log.Warn("could not read the last CSeq of the caller: the store failed", "error", err.Error())
	}
	if ok {
		return max(d.Caller.CSeq, s.callerSeq)
	}
	return s.callerSeq
}

// logAnswer logs the answer to the BYE of ct, once it has come or the
// BYE has failed. A nil ct is a BYE that never went out.
func logAnswer(ct *sip.ClientTransaction, what string, log *slog.Logger) {
	if ct == nil {
		return
	}
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
	if err := p.store.PutAssociation(context.Background(), a); err != nil {
		log.Warn("requests to the UE still go on until it registers again: the store failed", "error", err.Error())
	}
}
