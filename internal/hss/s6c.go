package hss

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// This file is the HSS side of S6c, the interface to the SMS central
// functions (TS 29.338 5). The SMS-GMSC asks which node serves a user
// before it forwards a short message (SRR, 5.2.1), and tells the HSS
// that a short message stays at the service centre when the delivery
// failed, or that one got through (RDR, 5.2.3); the HSS alerts the
// SMS-IWMSC once the user can take short messages again (ALR, 5.2.2).

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
// 23.040 3.2.6).
//
// SIMPLIFIED: TS 23.204 6.5a step 19. The HSS acts on the report of the
// SMS-GMSC, which the clause has it ignore because the IP-SM-GW has
// already reported in step 17. The IP-SM-GW of this project does not
// speak S6c, so the report of the SMS-GMSC is the only one the HSS
// gets.
//
// SIMPLIFIED: TS 23.040 3.2.6 items 1a and 1b. An absent user sets
// UNRI whichever node reported it, rather than MNRF for a report from
// an MSC and MNRG for one from an SGSN. The HSS of this project names
// no such serving node, so only a delivery through the IP-SM-GW can
// fail.
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
		// also reports here.
		stored, alert, fail := h.updateMessageWaiting(ctx, u, func(s *Subscriber) bool {
			return s.shortMessageDelivered(r.SCAddress)
		})
		if fail != (diameter.Result{}) {
			return sgd.ReportSMDeliveryStatusAnswer{Result: fail}
		}
		if alert {
			h.alertServiceCentres(stored)
		}
	case messageWaits:
		if r.RDRFlags&sgd.RDRSingleAttemptDelivery != 0 {
			// TS 29.338 5.2.3.4: one attempt is all this short message
			// gets, so an alert would come too late to be of use.
			log.Info("single attempt delivery; the SC address does not go into the message waiting data")
			break
		}
		kept := false
		_, _, fail := h.updateMessageWaiting(ctx, u, func(s *Subscriber) bool {
			kept = s.addMessageWaiting(r.SCAddress, cause)
			return false
		})
		switch {
		case fail != (diameter.Result{}):
			return sgd.ReportSMDeliveryStatusAnswer{Result: fail}
		case !kept:
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
// then, so only free memory helps (TS 23.040 3.2.6 items 1e and 2f).
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
			MWDStatus:   messageWaitingStatus(u, r.SCAddress),
		}
	}
	// No node is registered for MT SMS. The SC address goes into the
	// message waiting data, so that the HSS alerts the SC once a node
	// registers again. The read above is not under the lock of the
	// subscriber, so a PUR may have registered an IP-SM-GW in between: it
	// would have found an empty message waiting data and sent no alert,
	// and the address written here would wait for an alert that never
	// comes. The decision is therefore taken again under the lock.
	stored, _, fail := h.updateMessageWaiting(ctx, u, func(s *Subscriber) bool {
		switch {
		case servingNode(*s) != nil:
			return false
		case r.SRRFlags&sgd.SRRSingleAttemptDelivery != 0:
			// TS 29.338 5.2.1.3: one attempt is all this short message
			// gets, so an alert would come too late to be of use.
			log.Info("single attempt delivery; the SC address does not go into the message waiting data")
			return false
		case !sgd.ValidE164(r.SCAddress):
			return false
		}
		if !s.addMessageWaiting(r.SCAddress, AbsentUser) {
			log.Info("the message waiting data is full; the SC is left with its own retries")
		}
		return false
	})
	if fail != (diameter.Result{}) {
		return sgd.SendRoutingInfoForSMAnswer{Result: fail}
	}
	if node := servingNode(stored); node != nil {
		log.Info("routing info for a short message: a node registered while the query was answered", "serving_node", node.IPSMGWName)
		return sgd.SendRoutingInfoForSMAnswer{
			Result:      diameter.Base(diameter.DiameterSuccess),
			UserName:    imsi(stored.PrivateIdentity),
			ServingNode: node,
			MWDStatus:   messageWaitingStatus(stored, r.SCAddress),
		}
	}
	log.Info("routing info for a short message: no node is registered for MT SMS")
	return sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}
}

// messageWaitingStatus is the MWD-Status of a successful SRA to the
// service centre at scAddress (TS 29.338 5.3.3.8): whether its address
// is in the message waiting data of u, and whether MCEF is set. From it
// the SC learns that a delivery which succeeds is to be reported, so
// that the HSS clears MCEF and alerts the service centres that wait (TS
// 23.040 3.2.6 items 2g and 2h).
//
// MCEF is the only flag it carries. This HSS keeps UNRI rather than MNRF
// or MNRG, and while UNRI is set it names no serving node, so no
// successful SRA meets that flag; Table 5.3.3.8/1 has no bit for it
// either. The status is nil when neither the address nor MCEF is there:
// an SRA without MWD-Status tells the SC the same, and the answer for a
// user that nothing waits for stays as it was.
func messageWaitingStatus(u Subscriber, scAddress string) *sgd.MWDStatus {
	included := slices.Contains(u.MessageWaitingData, scAddress)
	if !included && !u.MCEF {
		return nil
	}
	var status sgd.MWDStatus
	if !included {
		status |= sgd.MWDSCAddressNotIncluded
	}
	if u.MCEF {
		status |= sgd.MWDMCEFSet
	}
	return &status
}

// servingNode is the node that serves u for mobile terminated short
// messages, or nil when none is registered (TS 29.338 5.2.1.3). Only an
// IP-SM-GW whose Diameter identity the HSS keeps can be named to an
// SMS-GMSC, which sends the TFR to that identity.
//
// 5.2.1.3 leaves out a node that is marked with the mobile not
// reachable flag. For a delivery over IP that flag is UNRI, which TS
// 23.040 3.2.6 sets while the message waiting data holds an address put
// there because the UE was absent: sending the SMS-GMSC to the IP-SM-GW
// again would only repeat the failure, and the alert of 3.2.6 item 2c is
// what the SC waits for instead.
func servingNode(u Subscriber) *sgd.ServingNode {
	if u.IPSMGWNumber == "" || u.IPSMGW.OriginHost == "" || u.UNRI {
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
// number. This HSS keeps no IMSI of its own. An Sh-Pull of the IMSI
// (TS 29.328 7.6.24) answers out of the same private identity, and
// without User-Data when there is none to read.
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

// updateMessageWaiting changes the message waiting data of u under the
// lock of the subscriber. change works on the subscriber as the store
// holds it, which is read again under the lock so that a PUR or a SAR
// that writes at the same time neither loses the change nor hides what
// it has written; it reports whether the service centres that are left
// are to be alerted.
//
// It returns the subscriber as the store now holds it, what change
// reported, and the result of a store or lock that failed, which is the
// zero Result when neither did. The subscriber is the zero value when
// the provisioning API removed the subscription in between.
func (h *HSS) updateMessageWaiting(ctx context.Context, u Subscriber, change func(*Subscriber) bool) (Subscriber, bool, diameter.Result) {
	unlock, err := h.lockSubscriber(u.PrivateIdentity)
	if err != nil {
		return Subscriber{}, false, h.lockFailed(err)
	}
	defer unlock()
	stored, ok, err := h.store.Subscriber(ctx, u.PrivateIdentity)
	if err != nil {
		return Subscriber{}, false, h.storeFailed(err)
	}
	if !ok {
		// The subscription went away since the request read it.
		return Subscriber{}, false, diameter.Experimental(sgd.DiameterErrorUserUnknown)
	}
	before := stored
	alert := change(&stored)
	if sameMessageWaiting(before, stored) {
		return stored, alert, diameter.Result{}
	}
	if err := h.store.Put(ctx, stored); err != nil {
		return Subscriber{}, false, h.storeFailed(err)
	}
	h.log.Info("message waiting data", "private_identity", stored.PrivateIdentity,
		"service_centres", stored.MessageWaitingData, "unri", stored.UNRI, "mcef", stored.MCEF)
	return stored, alert, diameter.Result{}
}

// sameMessageWaiting reports whether two copies of a subscriber hold the
// same message waiting indication, so that a change that writes nothing
// stores nothing.
func sameMessageWaiting(a, b Subscriber) bool {
	return a.UNRI == b.UNRI && a.MCEF == b.MCEF && slices.Equal(a.MessageWaitingData, b.MessageWaitingData)
}

// alertTimeout bounds one alert, which the HSS sends outside the
// request that set it off, to every instance of an address in turn. It
// outlasts the time the Diameter client waits for one answer several
// times over, so that the peers, not this timeout, decide when an alert
// has failed; an instance behind as many silent ones as fill it is not
// tried.
const alertTimeout = 30 * time.Second

// alertServiceCentres tells every service centre in the message waiting
// data of u that the user can take short messages again, so that each
// can try the short messages it holds (TS 29.338 5.2.2, TS 23.040
// 3.2.7). An address leaves the data once its service centre has
// answered; one whose service centre does not answer stays, and the next
// return of the user alerts it again, which TS 29.338 5.2.2.2 leaves to
// the operator.
//
// The alerts go out in the background, because the answer to the
// request that brought the user back must not wait for a service
// centre.
//
// An alert goes out on the registration of an IP-SM-GW
// (profileUpdate), on either notification that the user is ready for
// short messages again (readyForSM), and on a short message that
// arrived (reportSMDeliveryStatus).
func (h *HSS) alertServiceCentres(u Subscriber) {
	addresses := slices.Clone(u.MessageWaitingData)
	h.alerts.Add(1)
	go func() {
		defer h.alerts.Done()
		ctx, cancel := context.WithTimeout(context.Background(), alertTimeout)
		defer cancel()
		for _, address := range addresses {
			if h.alertServiceCentre(ctx, u, address) == alertRefused {
				continue
			}
			// The service centre took the alert and is now responsible for
			// the short messages it holds; or no alert will ever reach it,
			// which TS 29.338 5.2.2.2 has the HSS purge the address for.
			_, _, fail := h.updateMessageWaiting(ctx, u, func(s *Subscriber) bool {
				s.removeMessageWaiting(address)
				return false
			})
			if fail != (diameter.Result{}) {
				return
			}
		}
	}()
}

// alertOutcome is how one alert ended.
type alertOutcome int

const (
	// alertTaken is a service centre that answered that it took the
	// alert.
	alertTaken alertOutcome = iota
	// alertRefused is one that did not, for a reason that may pass. Its
	// address stays in the message waiting data.
	alertRefused
	// alertImpossible is one that no alert will ever reach.
	alertImpossible
)

// alertServiceCentre alerts the service centre of address and reports
// how it ended. It sends the ALR to the instances of that service centre
// in the order of the configuration, until one of them takes it. The
// instances hold the same short messages, so any one of them delivers
// them. Each has a Diameter identity of its own, which the alert names
// in Destination-Host (TS 29.338 5.3.1.2), and a Diameter agent does not
// take a request with a fixed destination to another server (RFC 6733
// 5.5.4), so it is the HSS that turns to the next instance. TS 29.338
// 5.2.2.2 leaves sending the alert again to the operator.
//
// SIMPLIFIED: TS 29.338 5.3.1.2. The alert goes out over the connection
// that the service centre opened to this HSS instance, as the
// Registration-Termination-Request to an S-CSCF does, instead of being
// routed to the Destination-Host by a Diameter agent. An instance that
// the service centre is not connected to therefore sends no alert; the
// address stays in the message waiting data, so the next return of the
// user alerts it from whichever instance answers that request.
func (h *HSS) alertServiceCentre(ctx context.Context, u Subscriber, address string) alertOutcome {
	log := h.log.With("private_identity", u.PrivateIdentity, "sc_address", address)
	centre, ok := h.serviceCentres[address]
	if !ok {
		// TS 29.338 5.3.1.2 routes the alert by the name of the service
		// centre, which the configuration of this HSS gives. Without one
		// no alert can ever reach it, so the address is purged rather than
		// kept for an alert that will not happen (5.2.2.2).
		log.Warn("the address is purged: the configuration names no Diameter identity for this service centre")
		return alertImpossible
	}
	peers := h.peers.Load()
	if peers == nil {
		log.Warn("no alert sent: this node takes no Diameter connections")
		return alertRefused
	}
	// The MSISDN-Alert of TS 23.040 3.2.7, by which the service centre
	// finds the short messages it holds for the user.
	msisdn := u.MSISDN()
	if msisdn == "" {
		log.Warn("no alert sent: the user has no MSISDN")
		return alertRefused
	}
	for _, host := range centre.Hosts {
		if h.alertInstance(ctx, *peers, host, centre.Realm, address, msisdn, log) {
			return alertTaken
		}
	}
	return alertRefused
}

// alertInstance sends one ALR to host, an instance of the service
// centre of address, and reports whether it took the alert.
func (h *HSS) alertInstance(ctx context.Context, peers Peers, host, realm, address, msisdn string, log *slog.Logger) bool {
	log = log.With("sc_host", host)
	request := sgd.AlertServiceCentreRequest{
		Header:         diameter.NewHeader(h.node, host, realm),
		SCAddress:      address,
		UserIdentifier: sgd.UserIdentifier{MSISDN: msisdn},
	}
	answer, err := peers.Request(ctx, host, request.Message())
	if err != nil {
		log.Warn("the service centre did not answer the alert", "error", err.Error())
		return false
	}
	ala, err := sgd.ParseAlertServiceCentreAnswer(answer)
	switch {
	case err != nil:
		log.Warn("cannot read the answer to the alert", "error", err.Error())
		return false
	case !ala.Result.Success():
		log.Warn("the service centre refused the alert", "result", ala.Result.String())
		return false
	}
	log.Info("the service centre is alerted that the user can take short messages again")
	return true
}
