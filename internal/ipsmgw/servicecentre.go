package ipsmgw

import (
	"context"
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the boundary between the IP-SM-GW and the SC. In a
// network the IP-SM-GW reaches the SC through an SMS-IWMSC for a short
// message from a UE and is reached through an SMS-GMSC for one to a UE,
// over MAP or over SGd (TS 23.204 5.2.4). What crosses the boundary
// either way is a TPDU and the addresses around it: Submit is
// MO-Forward-Short-Message, and IPSMGW.Deliver with Report is
// MT-Forward-Short-Message and its answer.
//
// Two SCs implement ServiceCentre. The stub of package sc sits in the
// process of the IP-SM-GW and calls Deliver itself. SMSIWMSC
// (smsiwmsc.go) reaches the SC node of the sc role over SGd: Submit
// sends an OFR, and a short message for a UE arrives in a TFR instead
// of through Deliver (sgd.go).

// ServiceCentre is the SC as the IP-SM-GW sees it.
type ServiceCentre interface {
	// Submit hands the SC a short message from the user whose E.164
	// number is originator. The report goes to the sender in RP-ACK, and
	// nil gives an RP-ACK without one, which TS 24.011 table 7.7 allows:
	// the SMS-SUBMIT-REPORT is an optional element of the RP-ACK, and the
	// SM-RP-UI of an OFA is optional as well (TS 29.338 table
	// 6.2.1.1/2). An error that is a *Refusal goes in RP-ERROR.
	Submit(ctx context.Context, originator sms.Address, submit sms.Submit) (*sms.SubmitReport, error)
	// Report tells the SC how the delivery it named reference ended.
	Report(ctx context.Context, reference string, outcome Outcome)
}

// Refusal is a short message that the SC did not accept: the RP-Cause of
// the RP-ERROR to the sender (TS 24.011 8.2.5.4, Table 8.4 part 1) and
// the SMS-SUBMIT-REPORT, with its TP-FCS, that rides in it (TS 23.040
// 9.2.2.2a). Report is nil where the SC gave none, which TS 24.011
// table 7.8 allows the RP-ERROR to go without.
type Refusal struct {
	Cause  sms.RPCause
	Report *sms.SubmitReport
}

func (r *Refusal) Error() string {
	if r.Report == nil {
		return fmt.Sprintf("SC refused the short message: RP-Cause %d", r.Cause)
	}
	return fmt.Sprintf("SC refused the short message: RP-Cause %d, TP-FCS %#02x", r.Cause, uint8(r.Report.FailureCause))
}

// Delivery is a TPDU that the SC asks the IP-SM-GW to deliver to a user
// of the IMS.
type Delivery struct {
	// Reference names the delivery for the SC, as the Session-Id of an
	// SGd request would. The IP-SM-GW hands it back with the Outcome.
	Reference string
	// Recipient is the public user identity to deliver to. TS 23.204 6.4
	// step 6 prefers the tel URI of the user, and leaves it to the IMS
	// core to find the SIP URI.
	Recipient string
	// TPDU is an sms.Deliver or an sms.StatusReport.
	TPDU sms.TPDU
	// ServiceCentre is the RP-Originator Address of the RP-DATA: the SC
	// that took the short message (TS 24.011 8.2.5.1). The zero value is
	// the SC of this IP-SM-GW; a short message from another network
	// keeps the SC of that network.
	ServiceCentre sms.Address
	// OverSGd marks a short message that came in a TFR (sgd.go), whose
	// Reference names that one TFR. Its outcome goes into the
	// Store, for whichever instance holds the TFR, instead of to the
	// ServiceCentre of this node.
	OverSGd bool
}

// Outcome is how a delivery ended: the answer to
// MT-Forward-Short-Message. When the MESSAGE itself fails, TS 24.341
// 5.3.3.4.2 has the IP-SM-GW report as TS 29.311 6.1.4.4.1 says, which
// maps the SIP status code to a user error of MAP (Table 6.1.4.4.1.1);
// the outcomes below are those user errors. When the MESSAGE got through
// but the UE answered with RP-ERROR, the outcome names its cause.
type Outcome int

const (
	// Delivered is an RP-ACK from the UE.
	Delivered Outcome = iota
	// SystemFailure is what TS 29.311 gives most status codes, 408 among
	// them. The IP-SM-GW also reports it when no delivery report comes.
	SystemFailure
	// AbsentSubscriber is "Absent Subscriber SM", for a 480: the user is
	// not registered.
	AbsentSubscriber
	// UnidentifiedSubscriber is for a 404 or 604.
	UnidentifiedSubscriber
	// SubscriberBusy is "Subscriber busy for MT SMS", for a 486, 600 or
	// 603.
	SubscriberBusy
	// IllegalSubscriber is for a 401 or 407.
	IllegalSubscriber
	// MemoryCapacityExceeded is an RP-ERROR with RP-Cause 22 (TS 24.011
	// Table 8.4 part 2).
	MemoryCapacityExceeded
	// ProtocolError is an RP-ERROR with any other cause.
	ProtocolError
	// Forwarded is a short message that the IP-SM-GW handed to another
	// IMS network, which accepted it. Whether the UE got it is not
	// known here (deliver.go).
	Forwarded
)

var outcomeNames = map[Outcome]string{
	Delivered:              "delivered",
	SystemFailure:          "system failure",
	AbsentSubscriber:       "absent subscriber",
	UnidentifiedSubscriber: "unidentified subscriber",
	SubscriberBusy:         "subscriber busy",
	IllegalSubscriber:      "illegal subscriber",
	MemoryCapacityExceeded: "memory capacity exceeded",
	ProtocolError:          "protocol error",
	Forwarded:              "forwarded to another network",
}

func (o Outcome) String() string { return outcomeNames[o] }

// failedMessageOutcomes is TS 29.311 V19.0.0 Table 6.1.4.4.1.1: the
// status codes of a failed MESSAGE that map to a user error other than
// System Failure.
var failedMessageOutcomes = map[int]Outcome{
	401: IllegalSubscriber,
	404: UnidentifiedSubscriber,
	407: IllegalSubscriber,
	480: AbsentSubscriber,
	486: SubscriberBusy,
	600: SubscriberBusy,
	603: SubscriberBusy,
	604: UnidentifiedSubscriber,
}

// outcomeOfFailure is the outcome of a MESSAGE that got a final response
// of statusCode, which is not a 2xx. No response at all counts as a 408,
// as the transaction layer would have said (RFC 3261 8.1.3.1).
func outcomeOfFailure(statusCode int) Outcome {
	if o, ok := failedMessageOutcomes[statusCode]; ok {
		return o
	}
	return SystemFailure
}

// outcomeOfReport is the outcome of the delivery report of the UE.
func outcomeOfReport(m sms.RPMessage) Outcome {
	e, ok := m.(sms.RPError)
	switch {
	case !ok:
		return Delivered
	case e.Cause == sms.RPCauseMemoryCapacityExceeded:
		return MemoryCapacityExceeded
	}
	return ProtocolError
}
