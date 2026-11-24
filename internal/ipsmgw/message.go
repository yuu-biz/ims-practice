package ipsmgw

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is TS 24.341 5.3.3.4.1, the MESSAGEs that come from a UE:
// a short message for the SC (RP-DATA), a notification that the UE has
// memory again (RP-SMMA), and the delivery report for an RP-DATA that
// the IP-SM-GW sent (RP-ACK or RP-ERROR). The IP-SM-GW supports
// In-Reply-To, so the last kind is told from the others by that header.

// message answers a MESSAGE as its final recipient (TS 24.229 5.7.2):
// with 202 (Accepted), the answer of a relay that passes the message on
// (RFC 3428 7), because what becomes of the short message is reported
// later, in a MESSAGE of its own.
func (g *IPSMGW) message(st *sip.ServerTransaction) {
	req := st.Request
	log := g.log.With("method", "MESSAGE", "call_id", req.Get("Call-ID"))
	if !isShortMessage(req) {
		// RFC 3261 8.2.3: a body the UAS does not understand.
		g.respond(st, 415, func(resp *sip.Message) { resp.Add("Accept", ContentType) })
		return
	}
	// The S-CSCF puts the identities of the user in P-Asserted-Identity
	// (5.3.3.4.1 NOTE 1); without them the request did not come through
	// the S-CSCF, and the IP-SM-GW has no one to answer.
	sender, err := ims.AssertedIdentity(req)
	if err != nil {
		g.respond(st, 403, nil)
		return
	}
	log = log.With("sender", sender)
	// TS 23.204 6.5b step 2: a request from a user this IP-SM-GW serves
	// is a sign that the UE is there again, whatever the request
	// carries. An asserted identity that belongs to no such user, as the
	// short message of another IMS network carries, has no subscription
	// to find and leaves everything as it is. It runs beside the answer,
	// so that the Sh exchange it may need does not hold the answer up,
	// and counts as work in hand until it is done (maintenance.go).
	g.userArrived()
	go func() {
		defer g.userQuit()
		g.reachableAgain(req, log)
	}()
	if callID := req.Get("In-Reply-To"); callID != "" {
		g.deliveryReport(st, callID, log)
		return
	}
	g.respond(st, 202, nil)

	// Steps b and c: the RPDU type, and the format of the SC address.
	rp, err := sms.ParseRP(req.Body)
	if err != nil {
		g.refuseUndecodable(req, sender, err, log)
		return
	}
	switch m := rp.(type) {
	case sms.RPDataMSToNetwork:
		g.submit(req, sender, m, log)
	case sms.RPSMMA:
		g.memoryAvailable(req, sender, m, log)
	case sms.RPAck, sms.RPError:
		// SIMPLIFIED: TS 24.341 5.3.3.4.1. A delivery report without
		// In-Reply-To is dropped. 5.3.2.4 item d has the UE name the
		// MESSAGE it answers, and finding the delivery by its RP-MR alone
		// would need a second key in the Store for a UE that breaks that
		// rule.
		log.Info("delivery report without In-Reply-To dropped", "rp_mti", int(rp.MTI()))
	case sms.RPDataNetworkToMS:
		if user, ok := terminatingUser(req); ok {
			g.shortMessageFromPeer(req, user, m, log)
			return
		}
		// TS 24.011 9.3.3, which the network may follow: RP-DATA for the
		// MS is not a message the network expects from a UE (cause 98).
		g.answer(req, sender, sms.RPError{Direction: sms.NetworkToMS, MessageReference: m.MessageReference,
			Cause: sms.RPCauseMessageNotCompatibleWithState}, log)
	}
}

// terminatingUser is the user that req is for, when the S-CSCF sent req
// here for the terminating case: P-Served-User has sescase term (RFC
// 8498 6.2). A short message for a user that comes from another IMS
// network reaches the IP-SM-GW so, by a filter criterion of that case
// (TS 23.204 6.4a step 2); one from a UE is of the originating case.
func terminatingUser(req *sip.Message) (string, bool) {
	served, err := sip.ParsePServedUser(req.Get("P-Served-User"))
	if err != nil || served.SessionCase != sip.SessionCaseTerm {
		return "", false
	}
	user, err := ims.CanonicalURI(served.URI)
	return user, err == nil
}

// shortMessageFromPeer delivers to user, a user that this IP-SM-GW
// serves, the short message of rp, which the IP-SM-GW of another IMS
// network sent (TS 23.204 6.4a steps 2 and 3, TS 24.341 5.3.3.5.6 and
// 5.3.3.5.3): an SMS-DELIVER in RP-DATA, with the SC of that network in
// the RP-OA. The IP-SM-GW has answered 202 already; the delivery to the
// UE is the one of a short message from the SC (deliver.go).
//
// SIMPLIFIED: TS 24.341 5.3.3.5.6 and 5.3.3.5.4. The outcome of the
// delivery goes to the SC of this network, which knows nothing of the
// short message and ends there; no delivery report, and no RP-ERROR for
// a short message that cannot be delivered, goes back to the other
// network. The SC of that network took the acceptance of the MESSAGE as
// the end of its part (exitIBCF in deliver.go).
func (g *IPSMGW) shortMessageFromPeer(req *sip.Message, user string, rp sms.RPDataNetworkToMS, log *slog.Logger) {
	log = log.With("recipient", user, "rp_oa", rp.OriginatorAddress.String())
	tpdu, err := sms.ParseTPDU(rp)
	if err != nil {
		log.Info("short message from another network does not decode, dropped", "error", err.Error())
		return
	}
	if _, ok := tpdu.(sms.Deliver); !ok {
		log.Info("RP-DATA from another network without an SMS-DELIVER, dropped")
		return
	}
	log.Info("short message from another network, delivering it to the user")
	g.Deliver(Delivery{
		Reference:     req.Get("Call-ID"),
		Recipient:     user,
		TPDU:          tpdu,
		ServiceCentre: rp.OriginatorAddress,
	})
}

// reservedMTI is the one value of the RP-MTI that TS 24.011 table 8.3
// leaves reserved.
const reservedMTI sms.RPMTI = 0b111

// refuseUndecodable is the RP-ERROR that steps b and c ask for when the
// body of a short message or a notification cannot be read. The
// message reference is the second octet of every RP message (TS 24.011
// 8.2.3, figure 8.4), so the RP-ERROR can name it even then. A reserved
// message type gets cause 97 (TS 24.011 9.3.3), a content error of
// RP-DATA or RP-SMMA cause 96 (9.3.4); the network may follow both.
func (g *IPSMGW) refuseUndecodable(req *sip.Message, sender string, err error, log *slog.Logger) {
	log.Info("RP message does not decode", "error", err.Error())
	if len(req.Body) < 2 {
		// TS 24.011 9.3.1: a message too short to name its reference is
		// ignored.
		return
	}
	mti, mr := sms.RPMTI(req.Body[0]&0b111), req.Body[1]
	var cause sms.RPCause
	switch mti {
	case sms.RPMTIDataMSToNetwork, sms.RPMTISMMA:
		cause = sms.RPCauseInvalidMandatoryInformation
	case reservedMTI:
		cause = sms.RPCauseMessageTypeNonExistent
	default:
		// 5.3.3.4.1 asks for the RP-ERROR only for RP-DATA and RP-SMMA.
		return
	}
	g.answer(req, sender, sms.RPError{Direction: sms.NetworkToMS, MessageReference: mr, Cause: cause}, log)
}

// submit is a short message from the UE (TS 24.341 5.3.3.4.1 steps b to
// e, TS 23.204 6.3): it goes to the SC, and the SC's answer goes back to
// the UE as the submit report (5.3.3.4.3).
func (g *IPSMGW) submit(req *sip.Message, sender string, rp sms.RPDataMSToNetwork, log *slog.Logger) {
	refuse := func(cause sms.RPCause, why string) {
		log.Info("short message refused", "reason", why, "rp_cause", int(cause))
		g.answer(req, sender, sms.RPError{Direction: sms.NetworkToMS, MessageReference: rp.MessageReference, Cause: cause}, log)
	}
	// Step b: TS 23.040 9.1.2.5 has the address of an SC always use the
	// ISDN/telephone numbering plan.
	da := rp.DestinationAddress
	if da.NumberingPlan != sms.NPIISDNTelephone {
		refuse(sms.RPCauseInvalidMandatoryInformation, "RP-DA is not in the ISDN/telephone numbering plan")
		return
	}
	// The IP-SM-GW reaches one SC only, by its international number. TS
	// 24.011 names no cause for an SC that the network does not know;
	// this IP-SM-GW refuses the transfer (cause 21) rather than hand the
	// message to an SC the sender did not choose.
	if da != g.cfg.ServiceCentre {
		refuse(sms.RPCauseShortMessageTransferRejected, "RP-DA is not the SC of this IP-SM-GW")
		return
	}
	tpdu, err := sms.ParseTPDU(rp)
	if err != nil {
		refuse(sms.RPCauseInvalidMandatoryInformation, "SMS-SUBMIT does not decode: "+err.Error())
		return
	}
	// 5.3.1.2 NOTE 2 and TS 23.204 5.3.1.1: the SC needs the sender as an
	// E.164 number, which the tel URI in P-Asserted-Identity gives. A user
	// without one has no MSISDN, and so no subscription to short
	// messages (cause 50).
	originator, ok := telNumber(req)
	if !ok {
		refuse(sms.RPCauseRequestedFacilityNotSubscribed, "no tel URI in P-Asserted-Identity")
		return
	}
	submit := tpdu.(sms.Submit)
	log = log.With("originator", originator.String(), "tp_da", submit.DestinationAddress.String(), "tp_mr", int(submit.MessageReference))

	// Step e: the short message goes to the SC.
	report, err := g.sc.Submit(context.Background(), originator, submit)
	var answer sms.RPMessage
	var refusal *Refusal
	switch {
	case err == nil:
		log.Info("SC accepted the short message")
		answer, err = ackWith(rp.MessageReference, report)
	case errors.As(err, &refusal):
		log.Info("SC refused the short message", "rp_cause", int(refusal.Cause), "tp_fcs", int(refusal.Report.FailureCause))
		answer, err = errorWith(rp.MessageReference, refusal)
	default:
		// The SC could not be asked. TS 24.011 Table 8.4 part 1: cause 41
		// is a failure that the sender may try again soon.
		log.Warn("SC did not answer", "error", err.Error())
		answer, err = sms.RPError{Direction: sms.NetworkToMS, MessageReference: rp.MessageReference, Cause: sms.RPCauseTemporaryFailure}, nil
	}
	if err != nil {
		log.Warn("cannot write the submit report", "error", err.Error())
		return
	}
	g.answer(req, sender, answer, log)
}

func ackWith(mr uint8, report sms.SubmitReport) (sms.RPMessage, error) {
	tpdu, err := report.Marshal()
	return sms.RPAck{Direction: sms.NetworkToMS, MessageReference: mr, UserData: tpdu}, err
}

func errorWith(mr uint8, r *Refusal) (sms.RPMessage, error) {
	tpdu, err := r.Report.Marshal()
	return sms.RPError{Direction: sms.NetworkToMS, MessageReference: mr, Cause: r.Cause, UserData: tpdu}, err
}

// memoryAvailable is an RP-SMMA (TS 24.341 5.3.3.4.1 steps d and e, TS
// 24.011 7.3.2, TS 23.204 6.6): the UE can take short messages again.
// The IP-SM-GW passes the notification to the HSS, which clears the
// memory capacity exceeded flag of the user and alerts the Service
// Centres that hold short messages for them (TS 23.040 3.2.6 item 2f).
//
// Item d names the user by MSISDN where the IP-SM-GW has one, and by
// the public identity otherwise, which is how its registration names the
// user as well (regevent.go). The MSISDN is the one that the
// third-party REGISTER carried, which the subscription to the user's
// registration state holds; a Store that cannot be read leaves the
// public identity, which Sh takes as well.
//
// The RP-ACK follows the notification, so that it never tells the UE
// that a notification got through when it did not. A notification that
// failed gets RP-Cause 41, a temporary failure that the UE may try
// again soon (TS 24.011 Table 8.4 part 1), as a short message does when
// the SC cannot be asked (submit).
func (g *IPSMGW) memoryAvailable(req *sip.Message, sender string, rp sms.RPSMMA, log *slog.Logger) {
	ctx := context.Background()
	publicID, msisdn := sender, ""
	if sub, ok, err := g.subscriptionOfSender(ctx, req); err != nil {
		log.Warn("the MSISDN of the user was not read: the store failed", "error", err.Error())
	} else if ok {
		publicID, msisdn = sub.PublicIdentity, sub.MSISDN
	}
	id := shIdentity(publicID, msisdn)
	if err := g.hss.NotifyReadyForSM(ctx, id, g.cfg.Number, sh.MemoryAvailable); err != nil {
		log.Warn("memory available notification not passed to the HSS", "msisdn", msisdn, "error", err.Error())
		g.answer(req, sender, sms.RPError{Direction: sms.NetworkToMS, MessageReference: rp.MessageReference,
			Cause: sms.RPCauseTemporaryFailure}, log)
		return
	}
	log.Info("memory available notification passed to the HSS", "msisdn", msisdn)
	g.answer(req, sender, sms.RPAck{Direction: sms.NetworkToMS, MessageReference: rp.MessageReference}, log)
}

// answer sends rp to the UE that sent req, as the submit report of TS
// 24.341 5.3.3.4.3: to the user (item a), forked to every contact that
// takes short messages (item c), naming the MESSAGE it answers (item
// d). The user is the first P-Asserted-Identity, which is where table
// B.5-7 sends the report.
//
// The answers to an RP-SMMA, and the RP-ERROR of 5.3.3.4.1 for a body
// that does not decode, take the same form: they too answer an RP
// message from the UE.
func (g *IPSMGW) answer(req *sip.Message, sender string, rp sms.RPMessage, log *slog.Logger) {
	m, err := g.newMessage(sender, "fork", rp)
	if err != nil {
		log.Warn("cannot write the answer", "error", err.Error())
		return
	}
	m.Add("In-Reply-To", req.Get("Call-ID"))
	log = log.With("report_call_id", m.Get("Call-ID"), "rp_mti", int(rp.MTI()))
	final, err := g.send(m, g.entryPoint())
	if err != nil {
		log.Info("submit report not delivered", "error", err.Error())
		return
	}
	log.Info("submit report answered", "status", final.StatusCode)
}

// telNumber is the E.164 number in the first tel URI of
// P-Asserted-Identity, which the S-CSCF adds as an alias (TS 24.229
// 5.4.3.2 step 9). A local number, without "+", is no E.164 number.
func telNumber(req *sip.Message) (sms.Address, bool) {
	for _, v := range req.GetAll("P-Asserted-Identity") {
		na, err := sip.ParseNameAddr(v)
		if err != nil {
			continue
		}
		canonical, err := ims.CanonicalURI(na.URI)
		if err != nil {
			continue
		}
		if number, ok := ims.TelNumber(canonical); ok && strings.HasPrefix(number, "+") {
			return sms.InternationalNumber(number), true
		}
	}
	return sms.Address{}, false
}

// deliveryReport is a MESSAGE that answers an RP-DATA of the IP-SM-GW
// (TS 24.341 5.3.3.4.1, the case with In-Reply-To; TS 23.204 6.5). Any
// IP-SM-GW of the network may get it, so the delivery it reports on is
// read from the Store.
func (g *IPSMGW) deliveryReport(st *sip.ServerTransaction, callID string, log *slog.Logger) {
	req := st.Request
	log = log.With("in_reply_to", callID)
	d, ok, err := g.store.Delivery(context.Background(), callID)
	if err != nil {
		// RFC 3261 21.5.1: the IP-SM-GW cannot tell whether it has the
		// delivery, which a 488 would say it has not.
		log.Warn("delivery report not read: the store failed", "error", err.Error())
		g.respond(st, 500, nil)
		return
	}
	if !ok {
		// Item a: the IP-SM-GW sent no such short message, or has
		// already ended its delivery.
		log.Info("delivery report for no pending delivery")
		g.respond(st, 488, nil)
		return
	}
	g.respond(st, 202, nil)
	log = log.With("reference", d.Reference, "recipient", d.Recipient)

	rp, err := sms.ParseRP(req.Body)
	switch {
	case err != nil && len(req.Body) >= 2 && sms.RPMTI(req.Body[0]&0b111) == sms.RPMTIErrorMSToNetwork:
		// TS 24.011 9.3.4, which the network may follow: an RP-ERROR with
		// an error in a mandatory element is taken as one with cause 111,
		// without diagnostic or RP-User-Data.
		log.Info("delivery report does not decode, taken as RP-ERROR with cause 111", "error", err.Error())
		rp = sms.RPError{Direction: sms.MSToNetwork, MessageReference: req.Body[1], Cause: sms.RPCauseProtocolErrorUnspecified}
	case err != nil:
		// TS 24.011 9.3.4: the network may ignore any other message it
		// cannot read. The delivery waits on, until TR1N ends it.
		log.Info("delivery report does not decode, ignored", "error", err.Error())
		return
	}
	if mr, ok := reportedReference(rp); !ok || mr != d.MessageReference {
		// TS 24.011 9.3.2 and 9.3.3, which the network may follow: a
		// report for another reference, or no report at all, is
		// discarded.
		log.Info("delivery report does not answer the RP-DATA, ignored", "rp_mti", int(rp.MTI()), "rp_mr", int(mr))
		return
	}
	g.endDeliveryNow(callID, outcomeOfReport(rp), log)
}

// reportedReference is the RP-MR of an RP-ACK or RP-ERROR from the UE,
// the two RP messages that report on an RP-DATA (TS 24.011 7.3.3,
// 7.3.4).
func reportedReference(m sms.RPMessage) (uint8, bool) {
	switch r := m.(type) {
	case sms.RPAck:
		return r.MessageReference, r.Direction == sms.MSToNetwork
	case sms.RPError:
		return r.MessageReference, r.Direction == sms.MSToNetwork
	}
	return 0, false
}
