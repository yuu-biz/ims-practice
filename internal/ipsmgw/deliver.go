package ipsmgw

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the delivery of a TPDU from the SC to a UE: a short
// message (TS 24.341 5.3.3.4.2, TS 23.204 6.4) or a status report
// (5.3.3.4.4). The UE first answers the MESSAGE with 200, which is not
// yet the delivery report (TS 23.204 6.4 NOTE 4); the report follows in
// a MESSAGE of its own (deliveryReport in message.go).

// defaultReportTimeout is TR1N, the time the network side waits for the
// RP-ACK of an RP-DATA (TS 24.011 6.2.2.2). TS 24.011 10 bounds only the
// timers of the MS. The wait has to outlast the MESSAGE that brings the
// RP-DATA and the MESSAGE that brings the report, each of which may take
// up to Timer F, 32 s; TS 24.341 5.3.1.1 NOTE 1 sets the sender's TR1M
// above Timer F for the same reason.
const defaultReportTimeout = 2 * 32 * time.Second

// messageReferenceLifetime is how long a recipient's counter of RP-MR
// is kept after the last RP-DATA to it. It outlives every delivery that
// could still wait for a report with one of its values.
const messageReferenceLifetime = 10 * time.Minute

// Deliver takes on the delivery of the TPDU of d to the UE of
// d.Recipient in RP-DATA and returns at once; the MESSAGE goes out in a
// goroutine of its own, which waits for its final response. The SC
// learns how the delivery ends from Report, once: when the MESSAGE
// fails, when the delivery report comes, or when ReportTimeout passes
// without one.
//
// A delivery is a user of the IP-SM-GW from here until the MESSAGE has
// its final response, so that a shut down waits for it. It is counted
// here, before the goroutine starts, rather than in the goroutine: a
// shut down in between would otherwise find the IP-SM-GW with no user
// and lock it while a delivery it had taken on was still to go out. It
// is no user afterwards: the wait for the delivery report belongs to
// the Store, and to whichever instance the report or the expiry of TR1N
// reaches (maintenance.go). The SC of this node asks only the instance
// that began the work, so the IP-SM-GW takes every delivery the SC
// hands it, in service or not.
func (g *IPSMGW) Deliver(d Delivery) {
	g.userArrived()
	go func() {
		defer g.userQuit()
		g.deliver(d)
	}()
}

// deliver sends the MESSAGE of d and waits for its final response. It
// runs in a goroutine of its own, which outlives the request that
// brought the delivery about, so it works with the background context.
//
// The delivery is in the Store before the MESSAGE goes out, since the
// report can reach another IP-SM-GW even before the 200 is back here.
// Its ReportDeadline goes with it, in the time index of TR1N, so that
// whichever instance claims the delivery when that time comes ends it
// (reportTimedOut): the delivery of an IP-SM-GW that stops is not left
// without an outcome.
//
// When the Store fails before the MESSAGE goes out, the SC hears of a
// system failure at once, and no MESSAGE goes out whose report no
// IP-SM-GW could match with its delivery.
func (g *IPSMGW) deliver(d Delivery) {
	ctx := context.Background()
	log := g.log.With("reference", d.Reference, "recipient", d.Recipient)
	now := time.Now()
	mr, err := g.store.NextMessageReference(ctx, d.Recipient, now.Add(messageReferenceLifetime))
	if err != nil {
		log.Warn("no RP-MR: the store failed", "error", err.Error())
		g.sc.Report(ctx, d.Reference, SystemFailure)
		return
	}
	m, err := g.deliveryMessage(d, mr)
	if err != nil {
		log.Warn("cannot write the RP-DATA", "error", err.Error())
		g.sc.Report(ctx, d.Reference, SystemFailure)
		return
	}
	callID := m.Get("Call-ID")
	log = log.With("call_id", callID, "rp_mr", int(mr))
	if err := g.store.PutDelivery(ctx, PendingDelivery{
		CallID: callID, Reference: d.Reference, Recipient: d.Recipient, MessageReference: mr,
		ReportDeadline: now.Add(g.cfg.ReportTimeout),
		// Longer than TR1N by TR1N itself, so that the round which claims
		// the expiry still finds the delivery. After that the Store
		// forgets it, and the SC hears of no outcome.
		Expires: now.Add(2 * g.cfg.ReportTimeout),
	}); err != nil {
		log.Warn("delivery not stored, not delivering", "error", err.Error())
		g.sc.Report(ctx, d.Reference, SystemFailure)
		return
	}

	next := g.entryPoint()
	ibcf, leaves := g.exitIBCF(d.Recipient)
	if leaves {
		next = ibcf
		g.leaveThroughIBCF(m)
		log = log.With("ibcf", ibcf.String())
	}
	log.Info("delivering")
	final, err := g.send(m, next)
	switch {
	case err != nil:
		// No final response is a 408 to TS 29.311 Table 6.1.4.4.1.1.
		g.endDeliveryNow(callID, outcomeOfFailure(408), log.With("error", err.Error()))
	case final.StatusCode >= 300:
		g.endDeliveryNow(callID, outcomeOfFailure(final.StatusCode), log.With("status", final.StatusCode))
	case leaves:
		// SIMPLIFIED: TS 24.341 5.3.3.5.4 and 5.3.3.5.6, TS 23.204 6.5
		// step 6. The IP-SM-GW of the other network sends no delivery
		// report back across the border, so the delivery ends when that
		// network accepts the MESSAGE. The SC learns that it cannot
		// confirm the delivery (statusOf in package sc).
		g.endDeliveryNow(callID, Forwarded, log.With("status", final.StatusCode))
	default:
		log.Info("MESSAGE accepted, waiting for the delivery report", "status", final.StatusCode)
	}
}

// exitIBCF is the IBCF that a MESSAGE to recipient leaves through, as a
// loose route: when the IP-SM-GW has an IBCF and the recipient is a SIP
// URI of a host outside the home domain, as the SC finds a user of
// another network by ENUM. The S-CSCF routes its own requests the same
// way (TS 24.229 5.4.3.2 steps 11 and 12).
func (g *IPSMGW) exitIBCF(recipient string) (sip.URI, bool) {
	if g.cfg.IBCF.Host == "" {
		return sip.URI{}, false
	}
	u, err := sip.ParseURI(recipient)
	if err != nil {
		return sip.URI{}, false
	}
	host, home := strings.ToLower(strings.TrimSuffix(u.Host, ".")), strings.ToLower(g.cfg.HomeDomain)
	if host == home || strings.HasSuffix(host, "."+home) {
		return sip.URI{}, false
	}
	ibcf := g.cfg.IBCF
	ibcf.Params = slices.Clone(ibcf.Params)
	ibcf.Params.Set("lr", "")
	return ibcf, true
}

// leaveThroughIBCF prepares m, the delivery of a short message, for a
// user of another IMS network, which send hands to the IBCF: the
// MESSAGE comes from the SC, by its E.164 number, the RP-OA of the
// RP-DATA. The IP-SM-GW of the other network takes it as one from
// outside, and delivers it (shortMessageFromPeer).
//
// SIMPLIFIED: TS 23.204 6.4 steps 2 to 4. A short message for a user
// with an MSISDN in another network goes from the SC to an SMS-GMSC,
// which asks the HSS of that network for the routeing information and
// hands the short message to its IP-SM-GW over MAP or SGd. This project
// has neither, so the short message crosses the II-NNI in a SIP MESSAGE
// to the recipient's number, as TS 23.204 6.3a and 6.4a carry one
// between IMS networks for users without an MSISDN. The MESSAGE is not
// the one of TS 24.341 5.3.3.5.2: it has the RP-DATA alone, without the
// multipart body, the XML of the sender and Feature-Caps. The SC, not
// the IP-SM-GW's SIP URI, is the asserted identity, so that no host name
// of the home network crosses the border.
func (g *IPSMGW) leaveThroughIBCF(m *sip.Message) {
	sc := sip.NameAddr{URI: "tel:" + g.cfg.ServiceCentre.String()}
	m.Set("From", sc.String()+";tag="+sip.NewTag())
	m.Set("P-Asserted-Identity", sc.String())
}

// deliveryMessage is the MESSAGE of TS 24.341 5.3.3.4.2 or 5.3.3.4.4:
// to one contact only (item c, "no-fork"), with the TPDU in RP-DATA
// whose RP-OA is the SC (TS 24.011 7.3.1.1, 8.2.5.1).
func (g *IPSMGW) deliveryMessage(d Delivery, mr uint8) (*sip.Message, error) {
	tpdu, err := d.TPDU.Marshal()
	if err != nil {
		return nil, err
	}
	sc := g.cfg.ServiceCentre
	if d.ServiceCentre.Digits != "" {
		sc = d.ServiceCentre
	}
	return g.newMessage(d.Recipient, "no-fork", sms.RPDataNetworkToMS{
		MessageReference: mr, OriginatorAddress: sc, UserData: tpdu,
	})
}

// endDelivery ends the delivery of the MESSAGE with callID, if nothing
// has ended it yet, and tells the SC. It returns the failure of the
// Store that left the delivery where it was.
func (g *IPSMGW) endDelivery(ctx context.Context, callID string, outcome Outcome, log *slog.Logger) error {
	d, ok, err := g.store.TakeDelivery(ctx, callID)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	log.Info("delivery ended", "outcome", outcome.String())
	g.sc.Report(ctx, d.Reference, outcome)
	return nil
}

// endDeliveryNow ends a delivery whose outcome is known while no request
// waits for it: the MESSAGE has had its final response, or none, and a
// delivery report has been answered already. When the Store fails, the
// delivery stays with its TR1N, which ends it instead, on this instance
// or on another.
func (g *IPSMGW) endDeliveryNow(callID string, outcome Outcome, log *slog.Logger) {
	if err := g.endDelivery(context.Background(), callID, outcome, log); err != nil {
		log.Warn("delivery not ended: the store failed; TR1N ends it", "outcome", outcome.String(), "error", err.Error())
	}
}

// claimDue claims the deliveries whose TR1N is due, for the sweep of
// this instance (New), and counts each claim as a user of the IP-SM-GW
// (maintenance.go).
//
// A claim is work in hand from the moment it is made, not from the
// moment its handler runs: a shut down in between would otherwise find
// the IP-SM-GW with no user and lock it while a delivery it had claimed
// was still without an outcome. Every expiry that this returns goes to
// exactly one Handle (internal/ims/expiry.go), which is handleExpiry,
// and that is where the count ends. An expiry that the Store did not
// give out is none of this instance's work, so a failure counts
// nothing.
func (g *IPSMGW) claimDue(ctx context.Context, _ string, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
	due, err := g.store.ClaimDue(ctx, now, lease, limit)
	if err != nil {
		return nil, err
	}
	for range due {
		g.userArrived()
	}
	return due, nil
}

// handleExpiry ends the delivery of a claimed expiry and gives up the
// count that claimDue took for it. The sweep goes on while the
// IP-SM-GW drains: ending a delivery that has no outcome yet is how the
// network keeps its promise to the SC, as the expiries of a draining
// S-CSCF are kept (maintenance.go).
func (g *IPSMGW) handleExpiry(ctx context.Context, e Expiry) error {
	defer g.userQuit()
	return g.reportTimedOut(ctx, e.CallID)
}

// reportTimedOut is TR1N running out for a delivery (TS 24.011 6.2.2.2):
// no delivery report has come, so the SC hears of a system failure.
//
// The instance that claimed the expiry takes the delivery, which only
// one caller wins, so the SC hears the outcome once however many
// instances claim the same delivery. The lock of the user is not taken:
// a delivery belongs to one Call-ID, which no other request of the user
// touches.
func (g *IPSMGW) reportTimedOut(ctx context.Context, callID string) error {
	log := g.log.With("call_id", callID, "reason", "no delivery report")
	return g.endDelivery(ctx, callID, SystemFailure, log)
}
