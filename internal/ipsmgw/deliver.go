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

// Deliver sends the TPDU of d to the UE of d.Recipient in RP-DATA, and
// waits for the final response to the MESSAGE. The SC learns how the
// delivery ends from Report, once: when the MESSAGE fails, when the
// delivery report comes, or when ReportTimeout passes without one.
//
// The delivery is in the Store before the MESSAGE goes out, since the
// report can reach another IP-SM-GW even before the 200 is back here.
//
// The timer of ReportTimeout runs only in this process, like the timers
// of the S-CSCF. If this IP-SM-GW stops, no one reports the
// end of a delivery whose report never comes; the Store forgets it.
func (g *IPSMGW) Deliver(ctx context.Context, d Delivery) {
	log := g.log.With("reference", d.Reference, "recipient", d.Recipient)
	mr := g.store.NextMessageReference(d.Recipient)
	m, err := g.deliveryMessage(d, mr)
	if err != nil {
		log.Warn("cannot write the RP-DATA", "error", err.Error())
		g.sc.Report(ctx, d.Reference, SystemFailure)
		return
	}
	callID := m.Get("Call-ID")
	log = log.With("call_id", callID, "rp_mr", int(mr))
	g.store.PutDelivery(PendingDelivery{
		CallID: callID, Reference: d.Reference, Recipient: d.Recipient, MessageReference: mr,
		// Longer than the timer below, which therefore always finds a
		// delivery that no report has ended. The Store forgets it by
		// itself only when this IP-SM-GW stops before the timer fires.
		Expires: time.Now().Add(2 * g.cfg.ReportTimeout),
	})
	time.AfterFunc(g.cfg.ReportTimeout, func() {
		g.endDelivery(callID, SystemFailure, log.With("reason", "no delivery report"))
	})

	next := g.entryPoint()
	ibcf, leaves := g.exitIBCF(d.Recipient)
	if leaves {
		next = ibcf
		g.leaveThroughIBCF(m, ibcf)
		log = log.With("ibcf", ibcf.String())
	}
	log.Info("delivering")
	final, err := g.send(m, next)
	switch {
	case err != nil:
		// No final response is a 408 to TS 29.311 Table 6.1.4.4.1.1.
		g.endDelivery(callID, outcomeOfFailure(408), log.With("error", err.Error()))
	case final.StatusCode >= 300:
		g.endDelivery(callID, outcomeOfFailure(final.StatusCode), log.With("status", final.StatusCode))
	case leaves:
		// SIMPLIFIED: TS 24.341 5.3.3.5.4 and 5.3.3.5.6, TS 23.204 6.5
		// step 6. The IP-SM-GW of the other network sends no delivery
		// report back across the border, so the delivery ends when that
		// network accepts the MESSAGE. The SC learns that it cannot
		// confirm the delivery (statusOf in package sc).
		g.endDelivery(callID, Forwarded, log.With("status", final.StatusCode))
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
// user of another IMS network: the IBCF goes on top of Route, and the
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
func (g *IPSMGW) leaveThroughIBCF(m *sip.Message, ibcf sip.URI) {
	sc := sip.NameAddr{URI: "tel:" + g.cfg.ServiceCentre.String()}
	m.Set("From", sc.String()+";tag="+sip.NewTag())
	m.Set("P-Asserted-Identity", sc.String())
	m.Prepend("Route", sip.NameAddr{URI: ibcf.String()}.String())
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
// has ended it yet, and tells the SC.
func (g *IPSMGW) endDelivery(callID string, outcome Outcome, log *slog.Logger) {
	d, ok := g.store.TakeDelivery(callID)
	if !ok {
		return
	}
	log.Info("delivery ended", "outcome", outcome.String())
	g.sc.Report(context.Background(), d.Reference, outcome)
}
