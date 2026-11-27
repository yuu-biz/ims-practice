package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/smsc"
)

// This file wires the sc role: the Service Centre of SMS, which holds
// the short messages of the network and delivers them (TS 23.040 4.1).
// Its Diameter node speaks S6c with the HSS, as the SMS-GMSC asking
// where a user is and reporting how a delivery ended, and SGd with the
// serving nodes, as the SMS-IWMSC taking the short messages of their
// UEs and as the SMS-GMSC forwarding the ones for them (TS 29.338 5, 6).
//
// The serving node is the end that opens the connection, because it is
// also the one that brings the mobile originated short messages, so the
// SC listens and sends each TFR back over the connection of the node it
// goes to (TS 29.338 6.3.1.1, 6.3.1.2).

// scAnswerTimeout is how long the SC waits for a TFA. A serving node
// answers only once the UE has acknowledged the short message or TR1N
// has run out, which for the IP-SM-GW of this project is
// ipsmgw.DefaultOutcomeWait; TS 23.204 6.5a NOTE 1 has the timer of the
// SMS-GMSC made longer where it leaves the serving node too little
// time. The margin above that wait carries the Diameter round trip and
// the answer that the serving node writes.
const scAnswerTimeout = ipsmgw.DefaultOutcomeWait + 24*time.Second

// startSC makes the Service Centre of the sc role and starts the
// deliveries that are due. realm is the realm of the HSS, which the SC
// names in its S6c requests without a Destination-Host, as TS 29.338
// 5.3.1.1.3 allows a sender that knows only the realm: every instance of
// the HSS of this project holds every subscriber. answerTimer is the
// Answer timer of the Diameter node, which the serving nodes that send
// the OFRs have as well.
//
// The function it returns stops the SC: it cuts the attempts under way
// short, which leaves their recipients due at once for another
// instance (smsc.SMSC.Run), and waits for them. The SC runs on a context
// of its own and not on ctx, which ends at the signal to stop, so that
// it stops when the node stops the work of its roles (stopNode): before
// the Diameter connections that its attempts send on, so that no TFR
// fails because its connection has closed. The caller also calls the
// function on an error that ends the start of the node, so that the
// node does not wait for an SC that was never told to stop. An OFR or
// an ALR that arrives after the stop is answered as ever, and leaves its
// delivery to an instance in service.
func startSC(ctx context.Context, c config.SC, realm string, answerTimer time.Duration, store smsc.Store,
	node *diameter.Node, server *diameter.Server, hss *diameter.PeerGroup, log *slog.Logger) (*smsc.SMSC, func()) {
	centre := smsc.New(smsc.Config{
		Address:       c.Address,
		HSSRealm:      realm,
		RetrySchedule: c.RetryDelays,
		AnswerTimer:   answerTimer,
	}, store, sgd.NewSGdClient(node, server, scAnswerTimeout), sgd.NewS6cClient(node, hss), smsc.SystemClock, log)
	runCtx, end := context.WithCancel(context.WithoutCancel(ctx))
	stopped := make(chan struct{})
	go func() {
		centre.Run(runCtx)
		close(stopped)
	}()
	log.Info("service centre ready", "address", c.Address, "retry_delays", c.RetryDelays)
	return centre, func() {
		end()
		<-stopped
	}
}

// scFunction is what the sc role tells the maintenance API of its node:
// what the SC tells of itself (smsc.SMSC.Function), and what the node
// does to its Diameter listener when the operator takes the SC out of
// service. The serving nodes reach the SC on that listener, and the OFR
// they send names no Destination-Host, so the SC cannot turn one away in
// its answer (RFC 6733 7.1.3). A shut down therefore stops the node
// accepting connections, and a lock ends the connections it has with a
// DPR whose Disconnect-Cause is REBOOTING (RFC 6733 5.4, 5.4.3), after
// which a serving node sends its OFRs to another instance of the SC and
// connects to this one again after Tc once it is back. The connection
// to the HSS is the SC's own and stays open, so the HSS can still alert
// it.
//
// The SC is DISABLED, besides while no instance of the HSS takes its
// S6c requests, while it is in service and no serving node can connect
// to it: the node failed to listen again after an unlock, or another
// role of the node has the listener closed.
func scFunction(centre *smsc.SMSC, server *diameter.Server, hssAvailable func() bool, log *slog.Logger) maintenance.Function {
	f := centre.Function(hssAvailable)
	drain := diameter.NewListenerDrain(server, string(config.RoleSC), log)
	ownState := f.OperationalState
	f.OperationalState = func() maintenance.OperationalState {
		if !drain.Reachable() {
			return maintenance.Disabled
		}
		return ownState()
	}
	f.AdministrativeStateChanged = func(_, to maintenance.AdministrativeState) {
		drain.Change(to == maintenance.Unlocked, to == maintenance.Locked)
	}
	return f
}

// serviceCentreHandler answers the requests that reach the node of the
// sc role: the OFR of a short message that a serving node brings over
// SGd (TS 29.338 6.2.1), and the ALR with which the HSS says over S6c
// that a user the SC holds short messages for can take them again
// (5.2.2). A request that does not parse is answered
// DIAMETER_UNABLE_TO_COMPLY, as the other roles answer one they cannot
// read: the SC cannot say which AVP the sender left out without reading
// it.
//
// Each request is a user of the SC until its answer is on its way, so
// that the DPR of a shut down that ends with it goes behind the answer.
func serviceCentreHandler(node *diameter.Node, centre *smsc.SMSC, request *diam.Message, log *slog.Logger) *diam.Message {
	node.AfterAnswer(request, centre.Answering())
	identity := node.Identity()
	unreadable := func(err error) {
		log.Warn("cannot read request", "command", request.Header.CommandCode, "error", err.Error())
	}
	if request.Header.CommandCode == sgd.CommandMOForwardShortMessage {
		ofr, err := sgd.ParseMOForwardShortMessageRequest(request)
		if err != nil {
			unreadable(err)
			return sgd.MOForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, identity)
		}
		return centre.MOForwardShortMessage(context.Background(), ofr).Message(request, identity)
	}
	alr, err := sgd.ParseAlertServiceCentreRequest(request)
	if err != nil {
		unreadable(err)
		return sgd.AlertServiceCentreAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}.Message(request, identity)
	}
	return centre.AlertServiceCentre(context.Background(), alr).Message(request, identity)
}
