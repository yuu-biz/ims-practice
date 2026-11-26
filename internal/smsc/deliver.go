package smsc

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the mobile terminated side: the SC, as the SMS-GMSC, asks
// the HSS which node serves the recipient and forwards the short message
// there (TS 23.040 8.1.1 and 10.1, TS 29.338 5.2.1.2 and 6.2.2.3). A
// message that does not arrive stays in the Store and is tried again
// later, until its validity period ends (TS 23.040 6.1).

// TP-ST values of TS 23.040 9.2.3.15 that package sms has no name for.
const statusValidityPeriodExpired sms.Status = 0b100_0110 // permanent error

// ProcessDue starts a delivery to each destination that is due: a retry
// whose time has come, a message whose validity period has ended, or a
// destination whose lease ran out because the instance that held it
// stopped. Each delivery runs in a goroutine of its own, and ProcessDue
// returns once it has started them, without waiting for them: one
// attempt can wait a minute or more for its TFA, and neither the other
// destinations nor the next sweep are to wait with it. Wait waits for
// them.
//
// It starts no more deliveries than Config.Deliveries allows at once.
// What is due beyond that stays due in the Store, for another instance,
// or for a later sweep of this one once a delivery has ended.
//
// While the operator has the SC out of service it claims nothing, and
// what is due waits for an instance in service (maintenance.go). Once
// this instance has stopped (Run), it claims nothing either.
func (c *SMSC) ProcessDue() {
	ctx := context.Background()
	for c.reserve() {
		l, ok, err := c.store.ClaimDue(ctx, c.clock.Now(), c.cfg.Lease)
		if err != nil {
			c.log.Warn("cannot claim a destination", "error", err.Error())
		}
		if !ok {
			c.unreserve()
			return
		}
		go func() {
			defer c.unreserve()
			c.serve(ctx, l)
		}()
	}
}

// deliverSoon tries the queue of destination in the background, if it
// is due: a short message that has just come goes out at once rather
// than at the next ProcessDue. While the operator has the SC out of
// service, and once this instance has stopped, it leaves the queue
// due, for an instance in service to claim. So it does while this
// instance runs as many deliveries as it may, for another instance or
// for a later sweep.
func (c *SMSC) deliverSoon(destination string) {
	if !c.reserve() {
		if c.unlocked() && !c.stopped() {
			c.log.Info("delivery left to a later sweep: this instance runs as many deliveries as it may",
				"destination", destination, "deliveries", c.cfg.Deliveries)
			return
		}
		c.log.Info("delivery left to an instance in service", "destination", destination)
		return
	}
	ctx := context.Background()
	l, ok, err := c.store.Claim(ctx, destination, c.clock.Now(), c.cfg.Lease)
	if err != nil {
		c.log.Warn("cannot claim a destination", "destination", destination, "error", err.Error())
	}
	if !ok {
		c.unreserve()
		return
	}
	go func() {
		defer c.unreserve()
		c.serve(ctx, l)
	}()
}

// serve delivers the queue of the leased destination, one message at a
// time, as TS 23.040 6.2 allows only one outstanding SMS-DELIVER to an
// MS. After each message it releases the lease and claims the
// destination anew, so that a long queue does not outlast one lease.
// Once the operator has taken the SC out of service, or this instance
// has stopped, it claims it no more: the destination is due again, and
// an instance in service goes on with the queue. A destination whose
// claim came back after the stop goes back at once, without an attempt
// and with its count of attempts as it was.
func (c *SMSC) serve(ctx context.Context, l Lease) {
	if c.stopped() {
		c.release(ctx, l, c.clock.Now(), l.Attempts, c.log.With("destination", l.Destination))
		return
	}
	for c.attemptHead(ctx, l) && c.unlocked() && !c.stopped() {
		var ok bool
		var err error
		if l, ok, err = c.store.Claim(ctx, l.Destination, c.clock.Now(), c.cfg.Lease); err != nil || !ok {
			return
		}
	}
}

// attemptHead discards the messages of the leased destination whose
// validity period has ended, tries to deliver the first of the others,
// and releases the lease with the time of the next attempt. It reports
// whether the queue can go on at once. ctx is for the calls to the
// Store; the attempt runs until it ends or this instance stops.
func (c *SMSC) attemptHead(ctx context.Context, l Lease) bool {
	log := c.log.With("destination", l.Destination)
	queue, err := c.store.Queue(ctx, l)
	if err != nil {
		// The lease runs out, and whichever instance claims the
		// destination next tries again.
		log.Warn("cannot read the queue", "error", err.Error())
		return false
	}
	now := c.clock.Now()
	var live []Message
	for _, m := range queue {
		if now.Before(m.Expires) {
			live = append(live, m)
			continue
		}
		if err := c.store.Remove(ctx, l, m.ID); err != nil {
			log.Warn("cannot discard a message", "message_id", m.ID, "error", err.Error())
			return false
		}
		// TS 23.040 6.1: the SC is responsible for the message only
		// until its validity period ends.
		log.Info("short message discarded: its validity period ended", "message_id", m.ID, "expires", m.Expires)
		c.reportStatus(ctx, m, statusValidityPeriodExpired, now)
	}
	if len(live) == 0 {
		c.release(ctx, l, now, 0, log)
		return false
	}

	head := live[0]
	log = log.With("message_id", head.ID)
	// TP-MMS tells the MS that more messages wait for it (TS 23.040
	// 3.2.4).
	o := c.attempt(c.life, head, len(live) > 1, log)
	after := c.clock.Now()
	next, attempts := after, 0
	switch {
	case o.kind == temporaryFailure && c.stopped():
		// The stop of this instance cut the attempt short, or came as it
		// failed. Neither tells when the recipient can take the message,
		// so the attempt is not counted, and the destination is due at
		// once for an instance in service rather than at the next retry.
		// The serving node may have delivered a TFR that the stop cut, and
		// the recipient then gets the message twice.
		attempts = l.Attempts
		log.Info("delivery attempt cut short by the stop of the SC; an instance in service takes it at once", "reason", o.reason)
	case o.kind == delivered, o.kind == permanentFailure:
		if err := c.store.Remove(ctx, l, head.ID); err != nil {
			log.Warn("cannot remove the message", "error", err.Error())
			return false
		}
		live = live[1:]
		if o.kind == delivered {
			log.Info("short message delivered")
			c.reportStatus(ctx, head, sms.StatusReceivedBySME, after)
		} else {
			log.Info("short message discarded: it cannot be delivered", "reason", o.reason)
			c.reportStatus(ctx, head, sms.StatusNotObtainable, after)
		}
	case o.kind == temporaryFailure:
		attempts = l.Attempts + 1
		next = after.Add(c.retryDelay(attempts))
		log.Info("short message not delivered, trying again later", "reason", o.reason, "attempts", attempts, "next_attempt", next)
	}
	// The SC wakes up for the first message whose validity period
	// ends before the next attempt.
	for _, m := range live {
		if m.Expires.Before(next) {
			next = m.Expires
		}
	}
	c.release(ctx, l, next, attempts, log)
	return o.kind != temporaryFailure
}

func (c *SMSC) release(ctx context.Context, l Lease, next time.Time, attempts int, log *slog.Logger) {
	if err := c.store.Release(ctx, l, next, attempts); err != nil {
		log.Warn("cannot release the destination", "error", err.Error())
	}
}

// retryDelay is the wait after the attempts-th failure in a row.
func (c *SMSC) retryDelay(attempts int) time.Duration {
	return c.cfg.RetrySchedule[min(attempts, len(c.cfg.RetrySchedule))-1]
}

// reportStatus sends the sender of m the SMS-STATUS-REPORT of TS 23.040
// 9.2.2.3, if it asked for one (3.2.9). The report goes into the queue of
// the sender like any short message, and is delivered the same way.
func (c *SMSC) reportStatus(ctx context.Context, m Message, status sms.Status, at time.Time) {
	r := m.StatusReportRequest
	if r == nil || m.Deliver == nil {
		return
	}
	report := Message{
		ID:          newMessageID(),
		Destination: r.Originator,
		StatusReport: &sms.StatusReport{
			MessageReference: r.MessageReference,
			RecipientAddress: r.Recipient,
			// TS 23.040 9.2.2.3: when the SC received the SMS-SUBMIT,
			// and when its fate was settled (9.2.3.13).
			ServiceCentreTimeStamp: m.Deliver.ServiceCentreTimeStamp,
			DischargeTime:          at.Truncate(time.Second),
			Status:                 status,
		},
		Expires: at.Add(c.cfg.DefaultValidityPeriod),
	}
	log := c.log.With("destination", r.Originator, "message_id", report.ID, "reports_on", m.ID, "tp_st", int(status))
	if err := c.store.Enqueue(ctx, report, at); err != nil {
		log.Warn("status report dropped: the store failed", "error", err.Error())
		return
	}
	log.Info("status report queued")
	c.deliverSoon(r.Originator)
}

type outcomeKind int

const (
	delivered outcomeKind = iota
	// temporaryFailure is a failure that may pass: the SC keeps the
	// message and tries again (TS 23.040 3.3.2, Table 1, "T").
	temporaryFailure
	// permanentFailure is a failure that will not pass: the SC drops the
	// message (Table 1, "P").
	permanentFailure
)

// outcome is how one attempt at a message ended.
type outcome struct {
	kind   outcomeKind
	reason string
}

// attempt delivers m once: it asks the HSS for the nodes that serve the
// recipient and forwards the message to them in turn.
func (c *SMSC) attempt(ctx context.Context, m Message, more bool, log *slog.Logger) outcome {
	// The SC has to release the lease before it runs out.
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Lease*9/10)
	defer cancel()
	tpdu, err := marshalTPDU(m, more)
	if err != nil {
		return outcome{kind: permanentFailure, reason: "cannot write the TPDU: " + err.Error()}
	}

	sra, err := c.s6c.SendRoutingInfoForSM(ctx, sgd.SendRoutingInfoForSMRequest{
		Header:            c.s6cHeader(),
		MSISDN:            m.Destination,
		SupportedFeatures: sgd.SMSFSupport,
		SCAddress:         c.cfg.Address,
	})
	if err != nil {
		return outcome{kind: temporaryFailure, reason: "system failure: the HSS did not answer: " + err.Error()}
	}
	if !sra.Result.Success() {
		return routingFailure(sra.Result)
	}
	nodes := servingNodes(sra)
	if len(nodes) == 0 {
		return outcome{kind: temporaryFailure, reason: "system failure: the HSS named no node that SGd reaches"}
	}

	var failures []failure
	for _, node := range nodes {
		f := c.forward(ctx, node, sra.UserName, tpdu, log)
		if f == nil {
			c.reportDelivered(ctx, m.Destination, node.kind, sra.MWDStatus, log)
			return outcome{kind: delivered}
		}
		failures = append(failures, *f)
		if !f.tryNextNode {
			break
		}
	}
	c.reportDeliveryStatus(ctx, m.Destination, failures, log)
	// TS 23.040 8.1.1: the report to the SC carries the errors of every
	// path. The message is kept if any of them may pass.
	o := outcome{kind: permanentFailure}
	var reasons []string
	for _, f := range failures {
		if !f.permanent {
			o.kind = temporaryFailure
		}
		reasons = append(reasons, f.node.kind.String()+": "+f.reason)
	}
	o.reason = strings.Join(reasons, "; ")
	return o
}

// marshalTPDU writes the TPDU of m, with TP-MMS as more says.
func marshalTPDU(m Message, more bool) ([]byte, error) {
	if m.StatusReport != nil {
		r := *m.StatusReport
		r.MoreMessagesToSend = more
		return r.Marshal()
	}
	d := *m.Deliver
	d.MoreMessagesToSend = more
	return d.Marshal()
}

// routingFailure is the outcome of an SRA that names no serving node
// (TS 29.338 5.2.1.3), read with TS 23.040 Table 1.
func routingFailure(r diameter.Result) outcome {
	switch r {
	case diameter.Experimental(sgd.DiameterErrorUserUnknown):
		return outcome{kind: permanentFailure, reason: "unknown subscriber"}
	case diameter.Experimental(sgd.DiameterErrorServiceNotSubscribed):
		return outcome{kind: permanentFailure, reason: "teleservice not provisioned"}
	case diameter.Experimental(sgd.DiameterErrorServiceBarred):
		return outcome{kind: temporaryFailure, reason: "call barred"}
	case diameter.Experimental(sgd.DiameterErrorAbsentUser):
		// The HSS has put the SC address into the MWD itself (TS 29.338
		// 5.2.1.3), and alerts the SC when the user is back.
		return outcome{kind: temporaryFailure, reason: "absent subscriber: no node is registered for MT SMS"}
	}
	return outcome{kind: temporaryFailure, reason: "system failure: the HSS answered " + r.String()}
}

type nodeKind int

const (
	nodeMME nodeKind = iota
	nodeIPSMGW
	nodeSMSF
)

func (k nodeKind) String() string {
	return [...]string{"MME", "IP-SM-GW", "SMSF"}[k]
}

// servingNode is one node that the SC can forward a short message to
// over SGd.
type servingNode struct {
	kind      nodeKind
	host      string
	realm     string
	mmeNumber string
}

// servingNodes are the nodes of an SRA that SGd reaches, in the order the
// SC tries them: the Serving-Node first, then the SMSF. TS 23.040 8.1.1
// NOTE 3 leaves the order to the operator.
func servingNodes(sra sgd.SendRoutingInfoForSMAnswer) []servingNode {
	var nodes []servingNode
	if n := sra.ServingNode; n != nil {
		switch {
		case n.MMEName != "":
			nodes = append(nodes, servingNode{kind: nodeMME, host: n.MMEName, realm: realmOf(n.MMEName, n.MMERealm), mmeNumber: n.MMENumberForMTSMS})
		case n.IPSMGWName != "":
			nodes = append(nodes, servingNode{kind: nodeIPSMGW, host: n.IPSMGWName, realm: realmOf(n.IPSMGWName, n.IPSMGWRealm)})
		}
	}
	if a := sra.SMSF3GPPAddress; a != nil && a.SMSF3GPPName != "" {
		nodes = append(nodes, servingNode{kind: nodeSMSF, host: a.SMSF3GPPName, realm: realmOf(a.SMSF3GPPName, a.SMSF3GPPRealm)})
	}
	return nodes
}

// realmOf is the realm the SRA gave for host, or, for the combination g
// of TS 29.338 5.3.3.6 that gives none, the rest of the host name after
// its first label. The hosts of this project are named inside their
// realm.
func realmOf(host, realm string) string {
	if realm != "" {
		return realm
	}
	_, rest, _ := strings.Cut(host, ".")
	return rest
}

// failure is a delivery through one node that did not succeed.
type failure struct {
	node      servingNode
	permanent bool
	// tryNextNode is set for the errors after which TS 23.040 8.1.1
	// has the SMS-GMSC try the second path.
	tryNextNode bool
	// waiting is the cause with which the SC address goes into the MWD
	// of the HSS, for the errors after which 8.1.1 asks for it, or nil.
	waiting *sgd.SMDeliveryCause
	// notBegun is set when the node has not begun the delivery: it
	// answered DIAMETER_TOO_BUSY, or the TFR never went out because the
	// SC has no connection to it.
	notBegun bool
	reason   string
}

func waitingFor(c sgd.SMDeliveryCause) *sgd.SMDeliveryCause { return &c }

// forward sends the TPDU to one node in a TFR (TS 29.338 6.2.2.3), and
// returns nil when the node delivered it.
//
// An IP-SM-GW that answers DIAMETER_TOO_BUSY, or that the SC has no
// connection to, has not begun the delivery. The SC then sends the TFR
// to the other IP-SM-GWs of the same realm that are connected to it,
// one after another, until one of them has begun it: the instances of
// the IP-SM-GW share the state of their users, so any of them finds the
// recipient by the IMSI. The TFR names its node in Destination-Host (TS
// 29.338 6.3.1.2), so no Diameter agent could take it to another node
// in place of the SC: the destination of such a request is fixed, and
// an agent answers DIAMETER_UNABLE_TO_DELIVER when that node is gone
// (RFC 6733 5.5.4). DIAMETER_TOO_BUSY is the answer after which the
// node that gets it tries an alternate peer (7.1.3).
//
// A TFR that went out and got no answer stays with its node: the
// IP-SM-GW may have delivered it, and the short message waits for the
// next attempt rather than reach the UE twice. Nor does a TFR to an MME
// go to another MME, as it carries the number of that MME for MT SMS.
func (c *SMSC) forward(ctx context.Context, node servingNode, userName string, tpdu []byte, log *slog.Logger) *failure {
	f := c.forwardTo(ctx, node, userName, tpdu, log)
	if f == nil || !f.notBegun || node.kind != nodeIPSMGW {
		return f
	}
	// The other instances are the peers of the realm that took SGd in
	// their CER. That holds while the IP-SM-GWs are the only nodes of
	// their realm with SGd that connect to the SC, as in every network
	// of this project; an MME or another SC of the same realm would be
	// tried too, and would refuse or fail the TFR.
	for _, host := range c.sgd.ServingNodes(node.realm) {
		if strings.EqualFold(host, node.host) {
			continue
		}
		log.Info("the IP-SM-GW has not begun the delivery; trying another of its realm",
			"node", f.node.host, "reason", f.reason, "next_node", host)
		other := node
		other.host = host
		if f = c.forwardTo(ctx, other, userName, tpdu, log); f == nil || !f.notBegun {
			return f
		}
	}
	return f
}

// forwardTo sends the TPDU to node in a TFR, and returns nil when the
// node delivered it.
func (c *SMSC) forwardTo(ctx context.Context, node servingNode, userName string, tpdu []byte, log *slog.Logger) *failure {
	log = log.With("node", node.host)
	log.Info("forwarding the short message", "node_kind", node.kind.String())
	tfa, err := c.sgd.MTForwardShortMessage(ctx, sgd.MTForwardShortMessageRequest{
		Header: diameter.Header{DestinationHost: node.host, DestinationRealm: node.realm},
		// The IMSI, or the MT Correlation ID of an IP-SM-GW that
		// answered the SRR in place of the HSS (TS 23.204 6.4 step 3c).
		UserName:          userName,
		SCAddress:         c.cfg.Address,
		SMRPUI:            tpdu,
		MMENumberForMTSMS: node.mmeNumber,
	})
	if err != nil {
		return &failure{node: node, tryNextNode: true, notBegun: errors.Is(err, diameter.ErrPeerUnavailable),
			reason: "system failure: no answer: " + err.Error()}
	}
	if tfa.Result.Success() {
		return nil
	}
	f := deliveryFailure(tfa)
	f.node = node
	f.notBegun = tfa.Result == diameter.Base(diameter.DiameterTooBusy)
	return &f
}

// deliveryFailure reads a TFA that is not a success: the errors of TS
// 29.338 6.2.2.2, as TS 23.040 Table 1 classifies them, and whether
// 8.1.1 tries the next node after them.
func deliveryFailure(tfa sgd.MTForwardShortMessageAnswer) failure {
	switch tfa.Result {
	case diameter.Experimental(sgd.DiameterErrorAbsentUser):
		return failure{tryNextNode: true, waiting: waitingFor(sgd.SMDeliveryCauseAbsentUser), reason: "absent subscriber"}
	case diameter.Experimental(sgd.DiameterErrorUserUnknown):
		// The node does not know the IMSI: "unidentified subscriber",
		// one of the reasons of an absent subscriber in Table 1, after
		// which TS 29.338 5.2.3.2 also reports to the HSS.
		return failure{tryNextNode: true, waiting: waitingFor(sgd.SMDeliveryCauseAbsentUser), reason: "unidentified subscriber"}
	case diameter.Experimental(sgd.DiameterErrorUserBusyForMTSMS):
		return failure{reason: "MS busy for MT SMS"}
	case diameter.Experimental(sgd.DiameterErrorIllegalUser):
		return failure{permanent: true, reason: "illegal subscriber"}
	case diameter.Experimental(sgd.DiameterErrorIllegalEquipment):
		return failure{permanent: true, reason: "illegal equipment"}
	case diameter.Experimental(sgd.DiameterErrorFacilityNotSupported):
		return failure{tryNextNode: true, reason: "facility not supported"}
	case diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure):
		if tfa.SMDeliveryFailureCause == nil {
			return failure{reason: "error in MS"}
		}
		switch tfa.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause {
		case sgd.MemoryCapacityExceeded:
			return failure{waiting: waitingFor(sgd.SMDeliveryCauseUEMemoryCapacityExceeded), reason: "memory capacity exceeded"}
		case sgd.EquipmentNotSMEquipped:
			return failure{tryNextNode: true, reason: "SMS lower layers capabilities not provisioned"}
		}
		return failure{reason: "error in MS"}
	}
	return failure{tryNextNode: true, reason: "system failure: the node answered " + tfa.Result.String()}
}

// reportDeliveryStatus asks the HSS to keep the SC address in the MWD of
// the recipient, with the cause each node gave, after a delivery that
// failed because the recipient was absent or its memory was full (TS
// 23.040 3.2.8 case 1 and 8.1.1, TS 29.338 5.2.3.2). The HSS then alerts
// the SC when the recipient can take short messages again.
//
// SIMPLIFIED: TS 29.338 5.2.3.2. The SC reports each such failure, also
// where the MWD-Status of the SRA shows that the HSS holds its address
// and the flag of that failure already, which the clause leaves out.
// Such a report changes nothing in the HSS (TS 23.040 3.2.6 items 1a to
// 1e), and the flag of a failure through the IP-SM-GW, UNRI, has no bit
// in the MWD-Status (TS 29.338 Table 5.3.3.8/1).
func (c *SMSC) reportDeliveryStatus(ctx context.Context, msisdn string, failures []failure, log *slog.Logger) {
	var outcome sgd.SMDeliveryOutcome
	for _, f := range failures {
		if f.waiting != nil {
			setOutcome(&outcome, f.node.kind, *f.waiting)
		}
	}
	if outcome == (sgd.SMDeliveryOutcome{}) {
		return
	}
	rda, err := c.sendDeliveryStatus(ctx, msisdn, outcome)
	switch {
	case err != nil:
		log.Warn("the HSS did not answer the delivery status report; only the retries are left", "error", err.Error())
	case !rda.Result.Success():
		// TS 23.040 3.2.8: when the HSS cannot keep the SC address, the
		// SC relies on its own retries.
		log.Info("the HSS did not keep the SC address in the MWD; only the retries are left", "result", rda.Result.String())
	default:
		log.Info("the HSS keeps the SC address in the MWD")
	}
}

// reportDelivered tells the HSS that the short message reached the
// recipient through a node of kind, when the MWD-Status of the SRA
// shows that the HSS has MNRF, MCEF or MNRG set for the recipient (TS
// 23.040 10.1 operation 3, TS 29.338 5.2.3.1). The HSS then clears the
// flags, takes the SC address out of the MWD, and alerts the service
// centres that still wait for the recipient (TS 23.040 3.2.6 items 2g
// and 2h). Without the report, an MCEF that the UE's notification of
// free memory never cleared would stay set: a later return of the UE
// would alert no service centre, and what it held then would wait for
// its retry.
//
// The address of the SC in the MWD with no flag set asks for no report:
// the HSS takes the address out itself once the SC has answered its
// alert.
//
// The report goes out once. The short message has reached the
// recipient and leaves the store whether or not the HSS answers, so an
// HSS that did not hear of the delivery keeps its flags until the UE's
// next notification, or the report of a later delivery, clears them.
//
// SIMPLIFIED: TS 23.040 8.1.1. After a delivery that failed through the
// first node and succeeded through the second, the HSS does not hear of
// the absent subscriber of the first node: the SC reports the success
// alone where the MWD-Status asks for a report, and nothing otherwise.
// The HSS of this project names one serving node, so a delivery it
// routes has no second one.
func (c *SMSC) reportDelivered(ctx context.Context, msisdn string, kind nodeKind, status *sgd.MWDStatus, log *slog.Logger) {
	if status == nil || *status&(sgd.MWDMNRFSet|sgd.MWDMCEFSet|sgd.MWDMNRGSet) == 0 {
		return
	}
	var outcome sgd.SMDeliveryOutcome
	setOutcome(&outcome, kind, sgd.SMDeliveryCauseSuccessfulTransfer)
	rda, err := c.sendDeliveryStatus(ctx, msisdn, outcome)
	switch {
	case err != nil:
		log.Warn("the HSS did not answer the report of the delivery; its flags for the recipient stay set", "error", err.Error())
	case !rda.Result.Success():
		log.Info("the HSS refused the report of the delivery; its flags for the recipient stay set", "result", rda.Result.String())
	default:
		log.Info("the HSS heard of the delivery and clears its flags for the recipient", "mwd_status", uint32(*status))
	}
}

// setOutcome puts cause into o as the outcome of the delivery through
// a node of kind (TS 29.338 5.3.3.14).
func setOutcome(o *sgd.SMDeliveryOutcome, kind nodeKind, cause sgd.SMDeliveryCause) {
	n := &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: cause}
	switch kind {
	case nodeMME:
		o.MMESMDeliveryOutcome = n
	case nodeIPSMGW:
		o.IPSMGWSMDeliveryOutcome = n
	case nodeSMSF:
		o.SMSF3GPPSMDeliveryOutcome = n
	}
}

// sendDeliveryStatus sends the HSS the RDR of TS 29.338 5.2.3 for
// msisdn with outcome.
func (c *SMSC) sendDeliveryStatus(ctx context.Context, msisdn string, outcome sgd.SMDeliveryOutcome) (sgd.ReportSMDeliveryStatusAnswer, error) {
	return c.s6c.ReportSMDeliveryStatus(ctx, sgd.ReportSMDeliveryStatusRequest{
		Header:            c.s6cHeader(),
		UserIdentifier:    sgd.UserIdentifier{MSISDN: msisdn},
		SCAddress:         c.cfg.Address,
		SMDeliveryOutcome: outcome,
	})
}
