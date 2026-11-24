package ipsmgw

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the IP-SM-GW as a serving node of SGd, which TS 23.204
// 5.2.4 has it appear to an SMS-GMSC as: the SMS-GMSC brings a short
// message for a user of this network in a TFR, the IP-SM-GW delivers it
// to the UE over SIP as it delivers any other (deliver.go), and the TFA
// says how that ended (TS 29.338 6.2.2, TS 23.204 6.4 and 6.5a).
//
// The TFR names the recipient by IMSI, which the IP-SM-GW read over Sh
// when it registered itself for the user (regevent.go). An SMS-GMSC
// sends the TFR to the Diameter identity that the HSS kept from that
// registration, so a TFR only ever arrives for a user this IP-SM-GW has
// registered.
//
// The TFA waits for the UE's delivery report, which comes in a MESSAGE
// of its own and may reach another instance (TS 23.204 6.5 step 3 asks
// for the same instance; DNS does not promise it, which is why the
// delivery is in the Store). The instance that ends the delivery leaves
// the outcome in the Store under a reference of the TFR's own, and this
// one takes it there.
//
// SIMPLIFIED: RFC 6733 3 (End-to-End Identifier) and Appendix C. The
// IP-SM-GW detects no duplicate TFR. An SMS-GMSC that sends a TFR again
// after a failover, with the T flag set, starts a second delivery, so
// the UE may get the short message twice, and each TFR is answered by
// how its own delivery ended. The duplicate cache that RFC 6733 asks
// for would have to be shared by every instance, like the deliveries.
//
// SIMPLIFIED: TS 23.204 6.4 step 5 and 6.5a. The IP-SM-GW selects no
// domain: it delivers over IMS and nowhere else. A delivery that fails
// is not tried again through an MSC, an SGSN, an MME or an SMSF,
// because this project has no such serving node for the IP-SM-GW to
// reach, and the TFA reports the failure of the one domain there is.
//
// SIMPLIFIED: TS 23.204 6.5a steps 17 and 18, TS 23.204 6.5 step 7. The
// IP-SM-GW sends the HSS no Report SM Delivery Status of its own, and
// subscribes to no one-time notification that the UE is reachable
// again. The SMS-GMSC reports the same failure over S6c, which is what
// writes the message waiting data in the HSS of this project, and the
// IP-SM-GW hears of the UE's return from the reg event subscription it
// holds anyway (TS 24.341 5.3.3.2). It keeps UNRI of its own (store.go)
// all the same, since only this node knows that the delivery over IMS
// failed.

// outcomePoll is how often the instance that holds a TFR looks in the
// Store for the outcome of its delivery. It is the delay that a
// delivery which ends at once adds to the TFA, and at the other end one
// read of the Store per outstanding TFR in that time.
const outcomePoll = 50 * time.Millisecond

// Handle answers the Diameter requests that reach the node of the
// IP-SM-GW over SGd: the TFR of a short message for a user it serves
// (TS 29.338 6.2.2). Every other command gets the protocol error of RFC
// 6733 7.2, as it would on a node without this role.
//
// A request that does not parse is answered DIAMETER_UNABLE_TO_COMPLY,
// as the HSS answers one it cannot read: the IP-SM-GW cannot say which
// AVP of TS 29.338 6.3.2.5 the sender left out without reading it.
func (g *IPSMGW) Handle(request *diam.Message) *diam.Message {
	identity := g.cfg.Diameter
	if request.Header.ApplicationID != sgd.ApplicationIDSGd || request.Header.CommandCode != sgd.CommandMTForwardShortMessage {
		return diameter.CommandUnsupportedAnswer(identity, request)
	}
	tfr, err := sgd.ParseMTForwardShortMessageRequest(request)
	if err != nil {
		g.log.Warn("cannot read request", "error", err.Error())
		return sgd.MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, identity)
	}
	return g.mtForwardShortMessage(context.Background(), tfr).Message(request, identity)
}

// mtForwardShortMessage delivers the short message of a TFR to the UE
// and answers once the delivery has ended. It runs for as long as the
// delivery takes, up to TR1N, on the goroutine that the Diameter node
// gives each request.
//
// The whole of it is one user of the IP-SM-GW, so that a shut down
// waits for the TFA: an instance that stopped in between would leave
// the SMS-GMSC without an answer until its own timer ran out, and the
// delivery it had begun would then be reported to nobody. That is the
// one piece of work of this role which takes longer than a few SIP
// transactions, so the timeout_seconds of a shut down has to allow for
// it (maintenance.go).
func (g *IPSMGW) mtForwardShortMessage(ctx context.Context, r sgd.MTForwardShortMessageRequest) sgd.MTForwardShortMessageAnswer {
	log := g.log.With("command", "TFR", "session_id", r.SessionID, "imsi", r.UserName)
	if !g.unlocked() {
		// RFC 6733 7.1.3: the SMS-GMSC should try another node of the
		// realm, which is what the IP-SM-GW's other instances are. The
		// delivery has not begun, so nothing is lost by refusing it.
		log.Info("short message refused while not unlocked")
		return answer(diameter.Base(diameter.DiameterTooBusy))
	}
	g.userArrived()
	defer g.userQuit()

	sub, ok, err := g.store.RegSubscriptionOfIMSI(ctx, r.UserName)
	switch {
	case err != nil:
		log.Warn("the user of the short message was not read: the store failed", "error", err.Error())
		return answer(diameter.Base(diameter.DiameterUnableToComply))
	case !ok:
		// TS 29.338 6.2.2.2 has a serving node that does not know the IMSI
		// answer so. The IP-SM-GW knows the IMSIs of the users it has
		// registered, and the HSS names it to an SMS-GMSC for those alone,
		// so an unknown one means a registration that has since ended.
		log.Info("short message for an IMSI this IP-SM-GW holds no user for")
		return answer(diameter.Experimental(sgd.DiameterErrorUserUnknown))
	case !sub.Available:
		// TS 23.204 6.5b: a UE that is not registered in the IMS, or is
		// registered without the SMS over IP capability, takes no short
		// message, and the SC hears that the user is absent.
		log.Info("short message for a user who takes none over IP", "public_identity", sub.PublicIdentity)
		g.setNotReachable(ctx, sub.PublicIdentity, log)
		return answer(diameter.Experimental(sgd.DiameterErrorAbsentUser))
	}
	tpdu, err := sms.ParseTPDU(sms.RPDataNetworkToMS{UserData: r.SMRPUI})
	if err != nil {
		// An SM-RP-UI this IP-SM-GW cannot read is one it cannot put in an
		// RP-DATA for the UE either.
		log.Info("the short message does not decode", "error", err.Error())
		return answer(diameter.Base(diameter.DiameterUnableToComply))
	}
	log = log.With("public_identity", sub.PublicIdentity)

	// The outcome of the delivery meets this request in the Store under
	// a reference that names this one TFR for every instance of the
	// IP-SM-GW: the Session-Id (RFC 6733 8.8), with a tag of this request
	// beside it, so that a TFR sent again in the same session waits for
	// its own delivery and never for the outcome of an earlier one.
	reference := r.SessionID + ";" + sip.NewTag()
	deadline := time.Now().Add(g.outcomeWait())
	g.Deliver(Delivery{
		Reference:     reference,
		Recipient:     recipientOf(sub),
		TPDU:          tpdu,
		ServiceCentre: sms.InternationalNumber("+" + r.SCAddress),
		OverSGd:       true,
	})
	outcome, err := g.awaitOutcome(ctx, reference, deadline, log)
	if err != nil {
		// Only a Store that failed gets here: every delivery ends, at the
		// latest when TR1N runs out (deliver.go).
		log.Warn("the short message is answered without an outcome", "error", err.Error())
		return answer(diameter.Base(diameter.DiameterUnableToComply))
	}
	if outcome == AbsentSubscriber {
		g.setNotReachable(ctx, sub.PublicIdentity, log)
	}
	a := answerOf(outcome)
	log.Info("short message answered", "outcome", outcome.String(), "result", a.Result.String())
	return a
}

// recipientOf is where the MESSAGE of a short message from an SMS-GMSC
// goes: TS 23.204 6.4 step 6 has the IP-SM-GW address the UE by the tel
// URI of the IMSI it was given, and by a SIP URI where there is no tel
// URI. The number of the subscription is the MSISDN that the
// third-party REGISTER carried, in international format without "+" (TS
// 29.328 7.6.9).
func recipientOf(sub RegSubscription) string {
	if sub.MSISDN != "" {
		return "tel:+" + sub.MSISDN
	}
	return sub.PublicIdentity
}

// DefaultOutcomeWait is outcomeWait with the default timers: how long
// an IP-SM-GW of this project holds a TFR before it answers. The
// SMS-GMSC has to wait longer than this for the TFA (TS 23.204 6.5a
// NOTE 1), so the role that wires an SMS-GMSC reads it to set that
// timer.
const DefaultOutcomeWait = defaultReportTimeout + ims.SweepInterval + time.Second

// outcomeWait is how long the instance that holds a TFR waits for the
// outcome of its delivery: TR1N, after which the delivery ends without
// a report, and the round of the sweep that ends it, which comes within
// SweepInterval and takes a few store operations (deliver.go).
//
// TS 23.204 6.5a NOTE 1 has the timer of the SMS-GMSC reconfigured
// where it leaves the IP-SM-GW too little time, so the wait is the one
// the delivery needs rather than one cut to fit that timer.
func (g *IPSMGW) outcomeWait() time.Duration {
	return g.cfg.ReportTimeout + g.cfg.SweepInterval + time.Second
}

// awaitOutcome takes the outcome of the delivery of reference out of the
// Store once it is there, looking every outcomePoll. It gives up at
// deadline, which only a Store that failed reaches: a read that fails
// is tried again at the next look, since the delivery goes on and its
// outcome may still arrive.
func (g *IPSMGW) awaitOutcome(ctx context.Context, reference string, deadline time.Time, log *slog.Logger) (Outcome, error) {
	var last error
	for {
		outcome, ok, err := g.store.TakeOutcome(ctx, reference)
		switch {
		case err != nil:
			log.Warn("the outcome of the delivery was not read: the store failed", "error", err.Error())
			last = err
		case ok:
			return outcome, nil
		}
		if !time.Now().Before(deadline) {
			if last != nil {
				return 0, fmt.Errorf("ipsmgw: no outcome of the delivery of %s within %s: %w", reference, g.outcomeWait(), last)
			}
			return 0, fmt.Errorf("ipsmgw: no outcome of the delivery of %s within %s", reference, g.outcomeWait())
		}
		select {
		case <-time.After(outcomePoll):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// setNotReachable raises the IP-SM-GW's own UNRI for a user that a short
// message did not reach because the UE was absent (TS 23.204 6.5a, TS
// 23.040 3.2.6). The next sign of life from the user clears it and tells
// the HSS (regevent.go). A Store that will not take it loses only that
// notification: the HSS holds UNRI of its own, which the SMS-GMSC's
// report over S6c sets.
func (g *IPSMGW) setNotReachable(ctx context.Context, publicID string, log *slog.Logger) {
	if err := g.store.PutNotReachable(ctx, publicID, time.Now().Add(notReachableLifetime)); err != nil {
		log.Warn("the user was not marked unreachable for short messages", "error", err.Error())
		return
	}
	log.Info("the user is marked unreachable for short messages over IP")
}

// notReachableLifetime is how long the IP-SM-GW keeps its own UNRI for a
// user. No specification bounds it: TS 23.040 3.2.6 has the flag
// cleared by an event, not by a timer. It is long enough to outlast a
// short message waiting at a Service Centre, since once no Service
// Centre holds one any longer there is nothing left for a notification
// to set off.
const notReachableLifetime = 72 * time.Hour

// answer is a TFA that carries a result and nothing else.
func answer(result diameter.Result) sgd.MTForwardShortMessageAnswer {
	return sgd.MTForwardShortMessageAnswer{Result: result}
}

// tfaResult is what one outcome of a delivery says in the TFA.
type tfaResult struct {
	result diameter.Result
	// failure is the SM-Enumerated-Delivery-Failure-Cause that TS 29.338
	// Table 6.2.2.1/2 makes mandatory beside
	// DIAMETER_ERROR_SM_DELIVERY_FAILURE. It is read for that result
	// alone.
	failure sgd.SMEnumeratedDeliveryFailureCause
}

// tfaResults is the result that each outcome of a delivery gives the
// SMS-GMSC. The outcomes themselves are the user errors of MAP that TS
// 29.311 Table 6.1.4.4.1.1 maps a SIP status code to, or the cause of
// the UE's RP-ERROR (servicecentre.go); this table carries each of them
// on to the result of TS 29.338 7.3 that stands for the same thing,
// which 6.2.2.1 lists for the answer.
//
// System Failure has no result of its own in TS 29.338: 7.3.1 leaves the
// permanent failures of the base protocol in force beside the ones it
// defines, so it becomes DIAMETER_UNABLE_TO_COMPLY (RFC 6733 7.1.5), the
// answer of a node that cannot say more. Forwarded cannot arise here: it
// is a short message that left for another IMS network (deliver.go), and
// a TFR is for a user this IP-SM-GW has registered.
var tfaResults = map[Outcome]tfaResult{
	Delivered:              {result: diameter.Base(diameter.DiameterSuccess)},
	AbsentSubscriber:       {result: diameter.Experimental(sgd.DiameterErrorAbsentUser)},
	UnidentifiedSubscriber: {result: diameter.Experimental(sgd.DiameterErrorUserUnknown)},
	SubscriberBusy:         {result: diameter.Experimental(sgd.DiameterErrorUserBusyForMTSMS)},
	IllegalSubscriber:      {result: diameter.Experimental(sgd.DiameterErrorIllegalUser)},
	MemoryCapacityExceeded: {result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), failure: sgd.MemoryCapacityExceeded},
	ProtocolError:          {result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), failure: sgd.EquipmentProtocolError},
}

// answerOf is the TFA of one outcome.
//
// SIMPLIFIED: TS 29.338 Table 6.2.2.1/2. The answer carries no
// SMS-DELIVER-REPORT in SM-RP-UI. The UE sends one in the RP-ACK or
// RP-ERROR of its delivery report, but the IP-SM-GW keeps only the
// outcome of a delivery, not the report itself (store.go), since the
// delivery report may reach another instance than the one that answers
// the TFR. The element is optional in the answer, and the result and the
// SM-Delivery-Failure-Cause carry the same news.
func answerOf(outcome Outcome) sgd.MTForwardShortMessageAnswer {
	t, ok := tfaResults[outcome]
	if !ok {
		return answer(diameter.Base(diameter.DiameterUnableToComply))
	}
	a := sgd.MTForwardShortMessageAnswer{Result: t.result}
	if t.result == diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure) {
		a.SMDeliveryFailureCause = &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: t.failure}
	}
	return a
}
