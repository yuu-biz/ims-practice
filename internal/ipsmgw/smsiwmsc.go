package ipsmgw

import (
	"context"
	"log/slog"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the SC behind the boundary of servicecentre.go reached
// over SGd, in place of the stub of package sc: the IP-SM-GW hands each
// short message of a UE to the SMS-IWMSC in an OFR and turns the OFA
// into the submit report for the sender (TS 23.204 6.3 steps 6 to 10, TS
// 29.338 6.2.1).
//
// The other direction needs nothing here. A short message for a UE of
// this network arrives in a TFR and the TFA says how the delivery ended,
// which is sgd.go; no outcome crosses this boundary.

// MOForwarder hands a short message to the SC. *sgd.MOClient is one.
type MOForwarder interface {
	MOForwardShortMessage(ctx context.Context, req sgd.MOForwardShortMessageRequest) (sgd.MOForwardShortMessageAnswer, error)
}

// SMSIWMSC is the SC as the IP-SM-GW reaches it over SGd.
type SMSIWMSC struct {
	sc MOForwarder
	// address is the E.164 number of the SC, digits only: the SC-Address
	// of the OFR, which is the RP-DA the UE sent (TS 29.338 Table
	// 6.2.1.1/1).
	address string
	// realm is the Destination-Realm of the OFR.
	realm string
	log   *slog.Logger
}

var _ ServiceCentre = (*SMSIWMSC)(nil)

// NewSMSIWMSC returns the SC over SGd. address is the number of the SC,
// digits only, and realm the Diameter realm the OFR goes to.
//
// The OFR names no SMS-IWMSC in Destination-Host, which TS 29.338
// 6.3.1.1 allows a serving node that cannot turn the SC address into
// one: sc holds every instance of the SC, and whichever of them answers
// is the one the request reaches, so a name in the request could only be
// the wrong one.
func NewSMSIWMSC(sc MOForwarder, address, realm string, log *slog.Logger) *SMSIWMSC {
	return &SMSIWMSC{sc: sc, address: address, realm: realm, log: log}
}

// Submit hands the SC the short message of a UE in an OFR and reads the
// OFA (TS 29.338 6.2.1.3): DIAMETER_SUCCESS accepts it, and the
// SMS-SUBMIT-REPORT that rides in SM-RP-UI goes to the sender in the
// RP-ACK; any other result is a *Refusal whose RP-Cause tells the UE
// why, with the report of the answer, which carries the TP-FCS, where
// the SC sent one.
//
// SIMPLIFIED: TS 29.338 Table 6.2.1.1/1. The User-Identifier of the OFR
// holds the MSISDN of the sender and not the IMSI, although the
// IP-SM-GW read the IMSI when it registered for the user
// (regevent.go). The boundary to the SC carries the sender as the E.164
// number, which is what the SMS-DELIVER needs in TP-OA (TS 23.204
// 5.3.1.1), and no node of this project reads the IMSI of an OFR.
func (c *SMSIWMSC) Submit(ctx context.Context, originator sms.Address, submit sms.Submit) (*sms.SubmitReport, error) {
	log := c.log.With("originator", originator.String(), "tp_da", submit.DestinationAddress.String(),
		"tp_mr", int(submit.MessageReference))
	tpdu, err := submit.Marshal()
	if err != nil {
		return nil, err
	}
	ofa, err := c.sc.MOForwardShortMessage(ctx, sgd.MOForwardShortMessageRequest{
		Header:         diameter.Header{DestinationRealm: c.realm},
		SCAddress:      c.address,
		UserIdentifier: sgd.UserIdentifier{MSISDN: originator.Digits},
		SMRPUI:         tpdu,
	})
	if err != nil {
		return nil, err
	}
	if ofa.Result.Success() {
		log.Info("the SC took the short message", "result", ofa.Result.String())
		return submitReportOf(ofa.SMRPUI, false, log), nil
	}
	cause := refusalCause(ofa)
	log.Info("the SC refused the short message", "result", ofa.Result.String(),
		"failure_cause", failureCauseName(ofa), "rp_cause", int(cause))
	return nil, &Refusal{Cause: cause, Report: submitReportOf(ofa.SMRPUI, true, log)}
}

// Report has nothing to carry over SGd: the IP-SM-GW answers each short
// message that the SC brought it in the TFA of that TFR (sgd.go), so the
// only delivery whose outcome reaches here is one that another IMS
// network sent straight to this network, which the SC of this network
// knows nothing of (shortMessageFromPeer in message.go).
func (c *SMSIWMSC) Report(_ context.Context, reference string, outcome Outcome) {
	c.log.Info("delivery ended; the SC of this network has nothing waiting for it",
		"reference", reference, "outcome", outcome.String())
}

// submitReportOf reads the SMS-SUBMIT-REPORT of an OFA, or nil where the
// answer carries none, which TS 29.338 Table 6.2.1.1/2 allows, or one
// this IP-SM-GW cannot read. The RP-ACK and the RP-ERROR to the UE then
// go without a report, as TS 24.011 tables 7.7 and 7.8 allow, rather
// than with one the SC did not send.
//
// forRPError says which of the two reports of TS 23.040 9.2.2.2a the
// TPDU is: the one for RP-ERROR carries TP-FCS, the one for RP-ACK does
// not, and the octets alone do not tell them apart. The result of the
// answer decides, as the RP message the report travels on to the UE does
// at the other end.
func submitReportOf(tpdu []byte, forRPError bool, log *slog.Logger) *sms.SubmitReport {
	if len(tpdu) == 0 {
		return nil
	}
	var rp sms.RPMessage = sms.RPAck{Direction: sms.NetworkToMS, UserData: tpdu}
	if forRPError {
		rp = sms.RPError{Direction: sms.NetworkToMS, UserData: tpdu}
	}
	parsed, err := sms.ParseTPDU(rp)
	if err != nil {
		log.Warn("the SM-RP-UI of the answer is not a report this IP-SM-GW can read", "error", err.Error())
		return nil
	}
	report := parsed.(sms.SubmitReport)
	return &report
}

// submitFailureCauses is the RP-Cause of TS 24.011 Table 8.4 part 1
// that each SM-Enumerated-Delivery-Failure-Cause an SMS-IWMSC may give
// becomes for the UE (TS 29.338 6.2.1.1). Each pair names the same thing
// on the two sides: an address no SME has, a user whose subscription
// holds no short message service, an SC that cannot take the message
// now, and an SC the network does not know, which the IP-SM-GW refuses
// with the same cause when the RP-DA names one (submit in message.go).
var submitFailureCauses = map[sgd.SMEnumeratedDeliveryFailureCause]sms.RPCause{
	sgd.InvalidSMEAddress:    sms.RPCauseUnassignedNumber,
	sgd.UserNotSCUser:        sms.RPCauseRequestedFacilityNotSubscribed,
	sgd.SCCongestion:         sms.RPCauseCongestion,
	sgd.UnknownServiceCentre: sms.RPCauseShortMessageTransferRejected,
}

// refusalCause is the RP-Cause for an OFA that is not a success. A
// facility the SC does not support becomes "requested facility not
// implemented", and every other result "temporary failure", the cause
// that the IP-SM-GW gives when it cannot reach the SC at all: a UE that
// gets it may send the short message again, which is what a result this
// IP-SM-GW cannot place calls for.
func refusalCause(ofa sgd.MOForwardShortMessageAnswer) sms.RPCause {
	if ofa.Result == diameter.Experimental(sgd.DiameterErrorFacilityNotSupported) {
		return sms.RPCauseRequestedFacilityNotImplemented
	}
	if ofa.Result == diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure) && ofa.SMDeliveryFailureCause != nil {
		if cause, ok := submitFailureCauses[ofa.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause]; ok {
			return cause
		}
	}
	return sms.RPCauseTemporaryFailure
}

// failureCauseName names the SM-Delivery-Failure-Cause of an answer for
// the log, and is empty where the answer has none.
func failureCauseName(ofa sgd.MOForwardShortMessageAnswer) string {
	if ofa.SMDeliveryFailureCause == nil {
		return ""
	}
	return ofa.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause.String()
}
