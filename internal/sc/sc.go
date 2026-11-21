// Package sc is the stub of the service centre (SC) that the IP-SM-GW
// hands short messages to. In a network the SC is a node
// of its own, which the IP-SM-GW reaches through an SMS-IWMSC and an
// SMS-GMSC (TS 23.204 5.2.4). Here it sits in the process of the
// IP-SM-GW, behind the ipsmgw.ServiceCentre interface, and passes each
// short message on at once: TS 24.341 B.5 assumes, for its example,
// that the IP-SM-GW reaches the SC directly.
//
// SIMPLIFIED: TS 23.204 6.3 and 6.4, TS 23.040 3.2.1 and 3.2.6. The stub
// speaks neither SGd and S6c (TS 29.338) nor MAP, does no domain
// selection between the IMS and the CS and PS domains (TS 23.204 6.4
// step 5), and does not store a short message to try again later: one
// that cannot be delivered at once is dropped, and the status report,
// if the sender asked for one, says so. So there is no message waiting
// data either, and the validity period is not read.
package sc

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// Users finds the user that has a number. *sh.Client is one.
type Users interface {
	PublicIdentities(ctx context.Context, msisdn string) ([]string, error)
}

// IPSMGW is where the SC delivers short messages for users of the IMS.
// *ipsmgw.IPSMGW is one.
type IPSMGW interface {
	Deliver(ctx context.Context, d ipsmgw.Delivery)
}

// ENUM turns a number into a SIP URI (RFC 6116). *sipdns.Resolver is
// one.
type ENUM interface {
	ENUM(ctx context.Context, number, suffix string) (string, error)
}

// ServiceCentre is the SC stub.
type ServiceCentre struct {
	users  Users
	store  Store
	ipsmgw IPSMGW
	log    *slog.Logger
	// enum and homeDomain find the users of other IMS networks; nil
	// knows only the users of the home network.
	enum       ENUM
	homeDomain string
}

var _ ipsmgw.ServiceCentre = (*ServiceCentre)(nil)

// New returns the stub. It delivers nothing until SetIPSMGW has given it
// the IP-SM-GW, which is made after it.
func New(users Users, store Store, log *slog.Logger) *ServiceCentre {
	return &ServiceCentre{users: users, store: store, log: log}
}

// SetIPSMGW gives the stub the IP-SM-GW to deliver through. It is called
// before the IP-SM-GW takes requests.
func (c *ServiceCentre) SetIPSMGW(g IPSMGW) { c.ipsmgw = g }

// SetPeerNetworks lets the stub deliver to the users of other IMS
// networks that the home network interconnects with: a number
// that no user of the home network has, and that ENUM gives a SIP URI
// outside homeDomain for, is delivered to that URI. It is called before
// the IP-SM-GW takes requests.
func (c *ServiceCentre) SetPeerNetworks(enum ENUM, homeDomain string) {
	c.enum, c.homeDomain = enum, homeDomain
}

// statusReportLifetime is how long the stub remembers that a sender
// asked for a status report. It outlasts the wait of the IP-SM-GW for
// the delivery report, so that the outcome always finds it.
const statusReportLifetime = 10 * time.Minute

// Submit takes a short message from originator. It
// finds the user that the TP-DA names, and when there is one, accepts
// the message with an SMS-SUBMIT-REPORT and delivers it in an
// SMS-DELIVER right after.
//
// A TP-DA that names no user is refused with RP-Cause 1, "Unassigned
// (unallocated) number" (TS 24.011 Table 8.4 part 1, E-2), and TP-FCS
// C3, "Invalid SME address" (TS 23.040 9.2.3.22). So is a number that
// is not an international E.164 number of 1 to 15 decimal digits, before
// the HSS is asked: TS 23.040 9.1.2.5 lets an SC refuse a type of number
// it does not support, and the stub has no numbering plan to read a
// national number with.
//
// SIMPLIFIED: TS 23.204 6.4 step 3. Asking the HSS for the routeing
// information (Send Routeing Info for SM, or S6c) is replaced by an Sh
// read of the public identities that have the number. The stub
// then knows the user only as a user of the IMS.
func (c *ServiceCentre) Submit(ctx context.Context, originator sms.Address, submit sms.Submit) (sms.SubmitReport, error) {
	now := time.Now()
	da := submit.DestinationAddress
	log := c.log.With("originator", originator.String(), "tp_da", da.String(), "tp_mr", int(submit.MessageReference))
	if da.TypeOfNumber != sms.TONInternational || da.NumberingPlan != sms.NPIISDNTelephone || !sh.ValidMSISDN(da.Digits) {
		log.Info("short message refused: TP-DA is not an international E.164 number")
		return sms.SubmitReport{}, refusal(sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress, now)
	}
	recipient, err := c.userOf(ctx, da)
	if errors.Is(err, errNoSuchUser) {
		if uri, ok := c.peerUserOf(ctx, da); ok {
			log.Info("the number is in another network", "uri", uri)
			recipient, err = uri, nil
		}
	}
	switch {
	case errors.Is(err, errNoSuchUser):
		log.Info("short message refused: no user has the number")
		return sms.SubmitReport{}, refusal(sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress, now)
	case err != nil:
		// TS 24.011 E-2: cause 41 is a failure that the sender may try
		// again soon; TS 23.040 9.2.3.22: C2, "SC system failure".
		log.Warn("short message refused: the HSS did not answer", "error", err.Error())
		return sms.SubmitReport{}, refusal(sms.RPCauseTemporaryFailure, sms.FCSSCSystemFailure, now)
	}

	reference := rand.Text()
	log = log.With("reference", reference, "recipient", recipient)
	if text, err := sms.DecodeText(submit.DataCodingScheme, submit.UserData); err == nil {
		log = log.With("text", text)
	}
	if submit.StatusReportRequest {
		c.store.PutStatusReportRequest(StatusReportRequest{
			Reference: reference, Originator: originator, MessageReference: submit.MessageReference,
			Recipient: da, ServiceCentreTimeStamp: now, Expires: now.Add(statusReportLifetime),
		})
	}
	log.Info("short message accepted", "status_report_request", submit.StatusReportRequest)
	// The SMS-DELIVER of TS 23.040 9.2.2.1 carries what the sender wrote,
	// with the sender in TP-OA and the time the SC took it in TP-SCTS.
	deliver := sms.Deliver{
		StatusReportIndication: submit.StatusReportRequest,
		OriginatingAddress:     originator,
		ProtocolIdentifier:     submit.ProtocolIdentifier,
		DataCodingScheme:       submit.DataCodingScheme,
		ServiceCentreTimeStamp: now,
		UserData:               submit.UserData,
	}
	// The sender gets the submit report first; the delivery goes on
	// without it.
	go c.ipsmgw.Deliver(context.Background(), ipsmgw.Delivery{Reference: reference, Recipient: recipient, TPDU: deliver})
	return sms.SubmitReport{ServiceCentreTimeStamp: now}, nil
}

// Report learns how a delivery ended. When the sender of the short
// message asked for a status report, the stub sends it one (TS 23.040
// 3.2.9 and 9.2.2.3, TS 24.341 5.3.3.4.4). The delivery of the status
// report itself, and of a short message without the request, ends
// here.
func (c *ServiceCentre) Report(ctx context.Context, reference string, outcome ipsmgw.Outcome) {
	log := c.log.With("reference", reference, "outcome", outcome.String())
	r, ok := c.store.TakeStatusReportRequest(reference)
	if !ok {
		log.Info("delivery ended")
		return
	}
	log = log.With("originator", r.Originator.String())
	report := sms.StatusReport{
		MessageReference:       r.MessageReference,
		RecipientAddress:       r.Recipient,
		ServiceCentreTimeStamp: r.ServiceCentreTimeStamp,
		DischargeTime:          time.Now(),
		Status:                 statusOf(outcome),
	}
	originator, err := c.userOf(ctx, r.Originator)
	if err != nil {
		log.Info("status report dropped: no user for the originator", "error", err.Error())
		return
	}
	log.Info("delivery ended, sending the status report", "tp_st", int(report.Status))
	go c.ipsmgw.Deliver(context.Background(), ipsmgw.Delivery{Reference: rand.Text(), Recipient: originator, TPDU: report})
}

// statusOf is the TP-ST of TS 23.040 9.2.3.15 for an outcome. The stub
// does not try again, so every failure is one after which "the SC is not
// making any more transfer attempts": a temporary error for what a real
// SC would retry (the user absent or busy, the network failing, the
// memory of the UE full), a permanent one for the rest.
func statusOf(o ipsmgw.Outcome) sms.Status {
	switch o {
	case ipsmgw.Delivered:
		return sms.StatusReceivedBySME
	case ipsmgw.Forwarded:
		return statusForwardedUnconfirmed
	case ipsmgw.AbsentSubscriber, ipsmgw.SubscriberBusy, ipsmgw.SystemFailure, ipsmgw.MemoryCapacityExceeded:
		return sms.StatusNoResponseFromSME
	}
	return sms.StatusNotObtainable
}

// statusForwardedUnconfirmed is TP-ST 0000001 (TS 23.040 9.2.3.15): the
// SC forwarded the short message but cannot confirm its delivery, as
// for one that another IMS network took.
const statusForwardedUnconfirmed sms.Status = 0b000_0001

var errNoSuchUser = errors.New("sc: no user has the number")

// peerUserOf is the SIP URI of a user of another IMS network that has
// number, as ENUM gives it: a URI whose host is outside the home domain.
//
// SIMPLIFIED: TS 23.204 6.4 step 3. An SMS-GMSC finds where to deliver
// by asking the HSS of the recipient (MAP or S6c), which needs no
// interconnect of the IMS. The stub has no SMS-GMSC, and asks ENUM, as
// the S-CSCF does for a call (TS 24.229 5.4.3.2 step 10).
func (c *ServiceCentre) peerUserOf(ctx context.Context, number sms.Address) (string, bool) {
	if c.enum == nil {
		return "", false
	}
	uri, err := c.enum.ENUM(ctx, number.String(), sipdns.ENUMSuffix)
	if err != nil {
		return "", false
	}
	u, err := sip.ParseURI(uri)
	if err != nil {
		return "", false
	}
	host, home := strings.ToLower(strings.TrimSuffix(u.Host, ".")), strings.ToLower(c.homeDomain)
	if host == home || strings.HasSuffix(host, "."+home) {
		return "", false
	}
	return uri, true
}

// userOf is the public identity to deliver to for an international
// number: its tel URI, which TS 23.204 6.4 step 6 prefers, or else the
// first identity of the user.
func (c *ServiceCentre) userOf(ctx context.Context, number sms.Address) (string, error) {
	ids, err := c.users.PublicIdentities(ctx, number.Digits)
	var result *sh.ResultError
	if errors.As(err, &result) && result.Result == cx.Experimental(cx.DiameterErrorUserUnknown) {
		return "", errNoSuchUser
	}
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "", errNoSuchUser
	}
	for _, id := range ids {
		if _, ok := ims.TelNumber(id); ok {
			return id, nil
		}
	}
	return ids[0], nil
}

func refusal(cause sms.RPCause, fcs sms.FailureCause, now time.Time) *ipsmgw.Refusal {
	return &ipsmgw.Refusal{Cause: cause, Report: sms.SubmitReport{FailureCause: fcs, ServiceCentreTimeStamp: now}}
}
