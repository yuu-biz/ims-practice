package hss

import (
	"context"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// This file is the HSS side of S6c, the interface to the SMS central
// functions (TS 29.338 5). The SMS-GMSC asks which node serves a user
// before it forwards a short message (SRR, 5.2.1), and tells the HSS
// that a short message stays at the service centre when the delivery
// failed (RDR, 5.2.3); the HSS alerts the SMS-IWMSC once the user can
// take short messages again (ALR, 5.2.2).

// handleS6c answers the S6c requests that reach the HSS.
func (h *HSS) handleS6c(ctx context.Context, request *diam.Message) *diam.Message {
	identity := h.node.Identity()
	switch request.Header.CommandCode {
	case sgd.CommandSendRoutingInfoForSM:
		srr, err := sgd.ParseSendRoutingInfoForSMRequest(request)
		if err != nil {
			h.log.Warn("cannot read request", "error", err.Error())
			return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, identity)
		}
		return h.sendRoutingInfoForSM(ctx, srr).Message(request, identity)
	case sgd.CommandReportSMDeliveryStatus:
		rdr, err := sgd.ParseReportSMDeliveryStatusRequest(request)
		if err != nil {
			h.log.Warn("cannot read request", "error", err.Error())
			return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, identity)
		}
		return h.reportSMDeliveryStatus(ctx, rdr).Message(request, identity)
	}
	return diameter.CommandUnsupportedAnswer(identity, request)
}

// reportSMDeliveryStatus is the HSS side of the delivery status report,
// TS 29.338 5.2.3.4: the SMS-GMSC tells the HSS how a delivery ended,
// and the HSS keeps or drops the service centre address accordingly (TS
// 23.040 3.2.8 case 1).
//
// The answer never carries the MSISDN-Alert of Table 5.2.3.1-2: the HSS
// of this project finds the user by the very MSISDN of the request, so
// the two are never different.
func (h *HSS) reportSMDeliveryStatus(ctx context.Context, r sgd.ReportSMDeliveryStatusRequest) sgd.ReportSMDeliveryStatusAnswer {
	log := h.log.With("msisdn", r.UserIdentifier.MSISDN, "sc_address", r.SCAddress)
	u, ok, err := h.shSubscriber(ctx, sh.UserIdentity{MSISDN: r.UserIdentifier.MSISDN})
	if err != nil {
		return sgd.ReportSMDeliveryStatusAnswer{Result: h.storeFailed(err)}
	}
	if !ok {
		log.Info("delivery status report: no such user")
		return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Experimental(sgd.DiameterErrorUserUnknown)}
	}
	switch report, cause := reportedOutcome(r.SMDeliveryOutcome); report {
	case messageDelivered:
		// The short message got through after all, which TS 29.338 5.2.3.1
		// also reports here: the SC that delivered it has nothing waiting
		// any more.
		if fail := h.forgetMessageWaiting(ctx, u, r.SCAddress); fail != (diameter.Result{}) {
			return sgd.ReportSMDeliveryStatusAnswer{Result: fail}
		}
	case messageWaits:
		stored, fail := h.recordMessageWaiting(ctx, u, r.SCAddress, cause)
		switch {
		case fail != (diameter.Result{}):
			return sgd.ReportSMDeliveryStatusAnswer{Result: fail}
		case !stored:
			log.Info("the message waiting data is full; the SC is left with its own retries")
			return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Experimental(sgd.DiameterErrorMWDListFull)}
		}
	default:
		log.Info("delivery status report with no cause this HSS acts on")
	}
	return sgd.ReportSMDeliveryStatusAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
}

// smDeliveryReport is what an RDR asks the HSS to do with the service
// centre address.
type smDeliveryReport int

const (
	// noSuchCause is a report whose causes the HSS knows none of. It
	// leaves the message waiting data as it is.
	noSuchCause smDeliveryReport = iota
	// messageWaits is a delivery that failed, so the short message stays
	// at the service centre.
	messageWaits
	// messageDelivered is a delivery that succeeded.
	messageDelivered
)

// reportedOutcome reads the outcomes of an RDR, and for a delivery that
// failed the cause the HSS records. A memory that was full decides over
// an absent user, whichever node reported which: the UE is reachable
// then, so only free memory helps (TS 23.040 3.2.8 case 1 items 1e and
// 2f).
func reportedOutcome(o sgd.SMDeliveryOutcome) (smDeliveryReport, DeliveryFailure) {
	report, cause := noSuchCause, AbsentUser
	for _, n := range []*sgd.NodeSMDeliveryOutcome{o.MMESMDeliveryOutcome, o.IPSMGWSMDeliveryOutcome, o.SMSF3GPPSMDeliveryOutcome} {
		if n == nil {
			continue
		}
		switch n.SMDeliveryCause {
		case sgd.SMDeliveryCauseUEMemoryCapacityExceeded:
			return messageWaits, MemoryCapacityExceeded
		case sgd.SMDeliveryCauseAbsentUser:
			report, cause = messageWaits, AbsentUser
		case sgd.SMDeliveryCauseSuccessfulTransfer:
			if report == noSuchCause {
				report = messageDelivered
			}
		}
	}
	return report, cause
}

// sendRoutingInfoForSM is the HSS side of the routing info query, TS
// 29.338 5.2.1.3.
//
// SIMPLIFIED: TS 29.338 5.2.1.3. The checks for an MT SMS teleservice
// subscription and for barring of MT short messages are left out, so
// the HSS answers neither DIAMETER_ERROR_SERVICE_NOT_SUBSCRIBED nor
// DIAMETER_ERROR_SERVICE_BARRED: every subscriber of this HSS may
// receive short messages, and it keeps no operator determined barring.
//
// SIMPLIFIED: TS 23.204 6.4 steps 3a to 3c. The HSS answers the query
// itself instead of forwarding it to the IP-SM-GW, which would answer
// with its own address and an MT Correlation ID. Forwarding is what
// enforces delivery through the home network of the recipient (TS
// 29.338 5.2.1.1), which this project does not do, and the HSS has the
// IP-SM-GW's Diameter identity from the registration anyway.
func (h *HSS) sendRoutingInfoForSM(ctx context.Context, r sgd.SendRoutingInfoForSMRequest) sgd.SendRoutingInfoForSMAnswer {
	log := h.log.With("msisdn", r.MSISDN, "sc_address", r.SCAddress)
	u, ok, err := h.shSubscriber(ctx, sh.UserIdentity{MSISDN: r.MSISDN})
	if err != nil {
		return sgd.SendRoutingInfoForSMAnswer{Result: h.storeFailed(err)}
	}
	if !ok {
		log.Info("routing info for a short message: no such user")
		return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorUserUnknown)}
	}
	if node := servingNode(u); node != nil {
		log.Info("routing info for a short message", "serving_node", node.IPSMGWName)
		return sgd.SendRoutingInfoForSMAnswer{
			Result:      diameter.Base(diameter.DiameterSuccess),
			UserName:    imsi(u.PrivateIdentity),
			ServingNode: node,
		}
	}
	// No node is registered for MT SMS. The SC address goes into the
	// message waiting data, so that the HSS alerts the SC once a node
	// registers again.
	if sgd.ValidE164(r.SCAddress) {
		stored, fail := h.recordMessageWaiting(ctx, u, r.SCAddress, AbsentUser)
		switch {
		case fail != (diameter.Result{}):
			return sgd.SendRoutingInfoForSMAnswer{Result: fail}
		case !stored:
			log.Info("the message waiting data is full; the SC is left with its own retries")
		}
	}
	log.Info("routing info for a short message: no node is registered for MT SMS")
	return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}
}

// servingNode is the node that serves u for mobile terminated short
// messages, or nil when none is registered (TS 29.338 5.2.1.3). Only an
// IP-SM-GW whose Diameter identity the HSS keeps can be named to an
// SMS-GMSC, which sends the TFR to that identity.
func servingNode(u Subscriber) *sgd.ServingNode {
	if u.IPSMGWNumber == "" || u.IPSMGW.OriginHost == "" {
		return nil
	}
	return &sgd.ServingNode{
		IPSMGWNumber: u.IPSMGWNumber,
		IPSMGWName:   u.IPSMGW.OriginHost,
		IPSMGWRealm:  u.IPSMGW.OriginRealm,
	}
}

// imsi is the IMSI of a user, which the private identity of TS 23.003
// 13.3 carries before its at sign.
//
// SIMPLIFIED: TS 29.338 Table 5.2.1.1-2. A successful SRA leaves
// User-Name out for a subscription whose private identity is not
// derived from an IMSI, such as one a softphone registers by its
// number. This HSS keeps no IMSI of its own.
func imsi(privateIdentity string) string {
	for i, c := range privateIdentity {
		switch {
		case c == '@':
			if i == 0 || i > 15 {
				return ""
			}
			return privateIdentity[:i]
		case c < '0' || c > '9':
			return ""
		}
	}
	return ""
}

// recordMessageWaiting records that the service centre at scAddress
// holds a short message for u, under the lock of the subscriber. It
// reports whether the address is in the message waiting data
// afterwards, and the result of a store or lock that failed, which is
// the zero Result when neither did.
func (h *HSS) recordMessageWaiting(ctx context.Context, u Subscriber, scAddress string, cause DeliveryFailure) (bool, diameter.Result) {
	unlock, err := h.lockSubscriber(u.PrivateIdentity)
	if err != nil {
		return false, h.lockFailed(err)
	}
	defer unlock()
	// The subscriber is read again under its lock, so that a PUR or a
	// SAR that writes at the same time does not lose the entry.
	stored, ok, err := h.store.Subscriber(ctx, u.PrivateIdentity)
	if err != nil {
		return false, h.storeFailed(err)
	}
	if !ok {
		// The provisioning API removed the subscription meanwhile.
		return false, diameter.Result{}
	}
	added := stored.addMessageWaiting(scAddress, cause)
	if err := h.store.Put(ctx, stored); err != nil {
		return false, h.storeFailed(err)
	}
	h.log.Info("message waiting data", "private_identity", stored.PrivateIdentity, "sc_address", scAddress,
		"cause", cause.String(), "stored", added, "unri", stored.UNRI, "mcef", stored.MCEF)
	return added, diameter.Result{}
}

// forgetMessageWaiting takes scAddress out of the message waiting data
// of u, under the lock of the subscriber. The flags stay as they are:
// TS 23.040 3.2.8 NOTE 3 has them outlive an empty MWD, so that the HSS
// still answers the next routing info query with what it knows.
func (h *HSS) forgetMessageWaiting(ctx context.Context, u Subscriber, scAddress string) diameter.Result {
	unlock, err := h.lockSubscriber(u.PrivateIdentity)
	if err != nil {
		return h.lockFailed(err)
	}
	defer unlock()
	stored, ok, err := h.store.Subscriber(ctx, u.PrivateIdentity)
	if err != nil {
		return h.storeFailed(err)
	}
	if !ok || !stored.removeMessageWaiting(scAddress) {
		return diameter.Result{}
	}
	if err := h.store.Put(ctx, stored); err != nil {
		return h.storeFailed(err)
	}
	h.log.Info("message waiting data", "private_identity", stored.PrivateIdentity, "sc_address", scAddress, "stored", false)
	return diameter.Result{}
}
