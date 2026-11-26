package smsc

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the mobile originated side: the SC, as the SMS-IWMSC,
// takes a short message from the node that serves its sender (TS 23.040
// 8.2.2 and 10.2, TS 29.338 6.2.1.3).

// TP-FCS values of TS 23.040 9.2.3.22 that package sms has no name for.
const (
	fcsCommandUnsupported   sms.FailureCause = 0xa1
	fcsNoSCSubscription     sms.FailureCause = 0xc1
	fcsDestinationSMEBarred sms.FailureCause = 0xc4
	fcsTPVPFNotSupported    sms.FailureCause = 0xc6
)

// The first octet of a TPDU from the MS: TP-MTI in bits 1 and 0 (TS
// 23.040 9.2.3.1), and in an SMS-SUBMIT TP-VPF in bits 4 and 3
// (9.2.3.3).
const (
	tpMTIMask     = 0b11
	tpMTISubmit   = 0b01
	tpMTICommand  = 0b10
	tpVPFMask     = 0b11 << 3
	tpVPFEnhanced = 0b01 << 3
	tpVPFAbsolute = 0b11 << 3
)

// MOForwardShortMessage takes the short message of an OFR. It accepts it
// with DIAMETER_SUCCESS and an SMS-SUBMIT-REPORT for RP-ACK, and starts
// delivering it at once; or it refuses it with the result and
// SM-Delivery-Failure-Cause of TS 29.338 6.2.1.1 and an
// SMS-SUBMIT-REPORT for RP-ERROR whose TP-FCS tells the UE why (TS
// 23.040 3.4.2). The serving node turns the answer into the RP-ACK or
// RP-ERROR for the UE.
//
// Before it accepts the message, the SC asks the HSS about the recipient
// with an SRR, which TS 23.040 8.2.2 allows the SMS-IWMSC to do, and
// refuses a number that no user has. A sender learns at once that it
// mistyped the number, as with the in-process stub of package sc,
// instead of in a status
// report. The SRR that the delivery sends afterwards is the SMS-GMSC's
// own (8.1.1).
//
// SIMPLIFIED: TS 29.338 5.3.3.5. The SRR of this check does not carry
// SM-Delivery-Not-Intended, so the HSS reads it like the SRR of a
// delivery. Package sgd does not write that AVP, as no node of this
// project acts on it.
//
// SIMPLIFIED: TS 23.040 6.2 item 3, 9.2.3.6 and 9.2.3.25. The SC
// neither rejects a repeated SMS-SUBMIT with TP-RD set nor replaces a
// short message it holds by a later one of a replace type of TP-PID
// (9.2.3.9): each SMS-SUBMIT is a short message of its own. A repeat
// after a lost RP-ACK may therefore arrive twice. No UE of this project
// sets TP-RD or a replace type.
//
// SIMPLIFIED: RFC 6733 3 (End-to-End Identifier) and Appendix C. The SC
// detects no duplicate OFR. A serving node whose OFR got no answer in
// time sends it again to another instance of the SC, with the T flag
// set, and if the first instance took it all the same, both hold the
// short message and the recipient gets it twice. The cache of requests
// already seen would have to be shared by every instance, as the short
// messages are, and find the first copy also when it arrives after the
// repeat.
func (c *SMSC) MOForwardShortMessage(ctx context.Context, req sgd.MOForwardShortMessageRequest) sgd.MOForwardShortMessageAnswer {
	now := c.clock.Now()
	log := c.log.With("session_id", req.SessionID, "sc_address", req.SCAddress,
		"imsi", req.UserIdentifier.UserName, "msisdn", req.UserIdentifier.MSISDN)

	// TS 29.338 6.2.1.3: the SMS-IWMSC checks that it knows the SC.
	if req.SCAddress != c.cfg.Address {
		log.Info("short message refused: unknown SC address")
		return refusal(sgd.UnknownServiceCentre, 0, now)
	}
	// TP-OA of the SMS-DELIVER is the MSISDN of the sender (TS 23.040
	// 9.2.3.7); an IP-SM-GW may know only that (TS 29.338 Table
	// 6.2.1.1/1).
	originator := req.UserIdentifier.MSISDN
	if !sgd.ValidE164(originator) {
		log.Info("short message refused: the sender has no MSISDN")
		return refusal(sgd.UserNotSCUser, fcsNoSCSubscription, now)
	}
	submit, fcs, err := parseSubmit(req.SMRPUI)
	if err != nil {
		// TS 29.338 6.2.1.3: a facility the SC does not support. TS
		// 29.338 has no cause for a TPDU the SC cannot read; TP-FCS
		// tells the UE more.
		log.Info("short message refused: TPDU not supported", "error", err.Error(), "tp_fcs", int(fcs))
		return sgd.MOForwardShortMessageAnswer{
			Result: diameter.Experimental(sgd.DiameterErrorFacilityNotSupported),
			SMRPUI: submitReport(fcs, now),
		}
	}
	da := submit.DestinationAddress
	log = log.With("tp_da", da.String(), "tp_mr", int(submit.MessageReference))
	// TS 23.040 9.1.2.5 lets an SC refuse a type of number it does not
	// support. The SC knows no numbering plan to read a national number
	// with, and the HSS is asked by international number.
	if da.TypeOfNumber != sms.TONInternational || da.NumberingPlan != sms.NPIISDNTelephone || !sgd.ValidE164(da.Digits) {
		log.Info("short message refused: TP-DA is not an international E.164 number")
		return refusal(sgd.InvalidSMEAddress, sms.FCSInvalidSMEAddress, now)
	}
	if refused, ok := c.checkRecipient(ctx, da.Digits, now, log); !ok {
		return refused
	}

	scts, err := c.store.ServiceCentreTimeStamp(ctx, da.Digits, now)
	if err != nil {
		log.Warn("short message refused: the store failed", "error", err.Error())
		return refusal(sgd.SCCongestion, sms.FCSSCSystemFailure, now)
	}
	m := Message{
		ID:          newMessageID(),
		Destination: da.Digits,
		// TS 23.040 9.2.2.1: what the sender wrote, with the sender in
		// TP-OA and the time the SC took it in TP-SCTS.
		Deliver: &sms.Deliver{
			StatusReportIndication: submit.StatusReportRequest,
			OriginatingAddress:     sms.InternationalNumber("+" + originator),
			ProtocolIdentifier:     submit.ProtocolIdentifier,
			DataCodingScheme:       submit.DataCodingScheme,
			ServiceCentreTimeStamp: scts,
			UserData:               submit.UserData,
		},
		Expires: now.Add(c.validityPeriod(submit.ValidityPeriod)),
	}
	if submit.StatusReportRequest {
		m.StatusReportRequest = &StatusReportRequest{Originator: originator, MessageReference: submit.MessageReference, Recipient: da}
	}
	if err := c.store.Enqueue(ctx, m, now); err != nil {
		log.Warn("short message refused: the store failed", "error", err.Error())
		return refusal(sgd.SCCongestion, sms.FCSSCSystemFailure, now)
	}
	log = log.With("message_id", m.ID, "expires", m.Expires)
	if text, err := sms.DecodeText(submit.DataCodingScheme, submit.UserData); err == nil {
		log = log.With("text", text)
	}
	log.Info("short message accepted", "status_report_request", submit.StatusReportRequest)
	c.deliverSoon(m.Destination)
	return sgd.MOForwardShortMessageAnswer{
		Result: diameter.Base(diameter.DiameterSuccess),
		SMRPUI: submitReport(0, scts),
	}
}

// parseSubmit reads the SMS-SUBMIT of an OFR. When it cannot, it gives
// the TP-FCS of TS 23.040 9.2.3.22 that says why.
//
// SIMPLIFIED: TS 23.040 9.2.3.12.2 and 9.2.3.12.3. An SMS-SUBMIT with
// the absolute or the enhanced format of TP-VP is refused with "TP-VPF
// not supported", which TS 23.040 9.2.3.3 allows. Package sms reads
// only the relative format, and the UE of this project asks for no
// validity period.
//
// SIMPLIFIED: TS 23.040 9.2.2.4. SMS-COMMAND, which asks the SC about a
// message it holds, is refused with "Command unsupported". No UE of
// this project sends one.
func parseSubmit(tpdu []byte) (sms.Submit, sms.FailureCause, error) {
	parsed, err := sms.ParseTPDU(sms.RPDataMSToNetwork{UserData: tpdu})
	if err == nil {
		return parsed.(sms.Submit), 0, nil
	}
	fcs := sms.FCSTPDUNotSupported
	if len(tpdu) > 0 {
		switch first := tpdu[0]; {
		case first&tpMTIMask == tpMTICommand:
			fcs = fcsCommandUnsupported
		case first&tpMTIMask == tpMTISubmit && (first&tpVPFMask == tpVPFEnhanced || first&tpVPFMask == tpVPFAbsolute):
			fcs = fcsTPVPFNotSupported
		}
	}
	return sms.Submit{}, fcs, err
}

// checkRecipient asks the HSS whether the recipient exists. It returns
// false with the answer that refuses the short message, or true when the
// SC may take it.
func (c *SMSC) checkRecipient(ctx context.Context, msisdn string, now time.Time, log *slog.Logger) (sgd.MOForwardShortMessageAnswer, bool) {
	sra, err := c.s6c.SendRoutingInfoForSM(ctx, sgd.SendRoutingInfoForSMRequest{
		Header:            c.s6cHeader(),
		MSISDN:            msisdn,
		SupportedFeatures: sgd.SMSFSupport,
		SCAddress:         c.cfg.Address,
	})
	switch {
	case err != nil:
		// TS 29.338 6.2.1.1 offers SC congestion for an SC that cannot
		// take the message now; TP-FCS C2 is "SC system failure".
		log.Warn("short message refused: the HSS did not answer", "error", err.Error())
		return refusal(sgd.SCCongestion, sms.FCSSCSystemFailure, now), false
	case sra.Result == diameter.Experimental(sgd.DiameterErrorUserUnknown):
		log.Info("short message refused: no user has the number")
		return refusal(sgd.InvalidSMEAddress, sms.FCSInvalidSMEAddress, now), false
	case sra.Result == diameter.Experimental(sgd.DiameterErrorServiceBarred):
		log.Info("short message refused: the recipient is barred from MT SMS")
		return refusal(sgd.InvalidSMEAddress, fcsDestinationSMEBarred, now), false
	}
	// Any other answer names a user who exists: whether the SC can reach
	// it now is for the delivery to find out.
	return sgd.MOForwardShortMessageAnswer{}, true
}

// refusal is the OFA of TS 29.338 6.2.1.1 for a short message the SC
// does not take: DIAMETER_ERROR_SM_DELIVERY_FAILURE with cause, and the
// SMS-SUBMIT-REPORT for RP-ERROR with fcs, unless fcs is zero.
func refusal(cause sgd.SMEnumeratedDeliveryFailureCause, fcs sms.FailureCause, now time.Time) sgd.MOForwardShortMessageAnswer {
	a := sgd.MOForwardShortMessageAnswer{
		Result:                 diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure),
		SMDeliveryFailureCause: &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: cause},
	}
	if fcs != 0 {
		a.SMRPUI = submitReport(fcs, now)
	}
	return a
}

// submitReport is the SMS-SUBMIT-REPORT of TS 23.040 9.2.2.2a: for
// RP-ERROR with TP-FCS fcs, or for RP-ACK when fcs is zero. It is nil if
// it cannot be written, as the OFA may go without it.
func submitReport(fcs sms.FailureCause, scts time.Time) []byte {
	b, err := sms.SubmitReport{FailureCause: fcs, ServiceCentreTimeStamp: scts.Truncate(time.Second)}.Marshal()
	if err != nil {
		return nil
	}
	return b
}

// validityPeriod is how long the SC keeps a short message: the relative
// TP-VP of TS 23.040 9.2.3.12.1, counted from when the SC received the
// SMS-SUBMIT, or the default of the SC.
func (c *SMSC) validityPeriod(vp sms.ValidityPeriod) time.Duration {
	if vp.Format != sms.ValidityPeriodRelative {
		return c.cfg.DefaultValidityPeriod
	}
	switch v := time.Duration(vp.Relative); {
	case v <= 143:
		return (v + 1) * 5 * time.Minute
	case v <= 167:
		return 12*time.Hour + (v-143)*30*time.Minute
	case v <= 196:
		return (v - 166) * 24 * time.Hour
	default:
		return (v - 192) * 7 * 24 * time.Hour
	}
}
