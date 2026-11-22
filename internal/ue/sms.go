package ue

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the SMS over IP side of the UE (TS 24.341): the
// SM-over-IP sender of 5.3.1, which submits a short message to its SC in
// a MESSAGE and learns the outcome from a submit report in a MESSAGE
// that the IP-SM-GW sends back; and the SM-over-IP receiver of 5.3.2,
// which takes a short message from the IP-SM-GW in a MESSAGE and answers
// with a delivery report in a MESSAGE of its own.
//
// The two layers inside the body of each MESSAGE, RP and TP, are those
// of a mobile station that sends and receives short messages over the
// CS or PS domain (TS 24.011, TS 23.040); SIP only carries them.
//
// SIMPLIFIED: TS 24.341 5.3.2.5. The receiver sends RP-SMMA when it has
// memory again after it refused a short message for want of it (RP-Cause
// 22). The simulator keeps every short message it gets and never runs
// out of memory, so it never refuses one that way and never sends
// RP-SMMA.

// SMSContentType is the media type of a short message in a MESSAGE
// (TS 24.341 5.3.1.2 d).
const SMSContentType = "application/vnd.3gpp.sms"

// ReceivedSMS is a short message that the UE received: an SMS-DELIVER
// (TS 23.040 9.2.2.1).
type ReceivedSMS struct {
	// CallID is that of the MESSAGE that brought it, which the delivery
	// report names in In-Reply-To.
	CallID string `json:"call_id"`
	// From is the sender's number, TP-OA, such as +12025550101.
	From string `json:"from"`
	Text string `json:"text"`
	// ServiceCentreTimeStamp is TP-SCTS: when the SC took the message.
	ServiceCentreTimeStamp time.Time `json:"service_centre_time_stamp"`
	Received               time.Time `json:"received"`
}

// OutgoingSMS is a short message for SendSMS.
type OutgoingSMS struct {
	// To is the recipient's number in international format, such as
	// +12025550102: TP-DA.
	To   string
	Text string
	// StatusReport asks the SC for an SMS-STATUS-REPORT once the message
	// has reached the recipient, or failed to (TP-SRR).
	StatusReport bool
}

// SubmitState is how far a short message that the UE sent has got.
type SubmitState string

const (
	// Submitted: the MESSAGE got its 202 (Accepted), and the submit
	// report has not come yet.
	Submitted SubmitState = "submitted"
	// SubmitAccepted: the submit report is an RP-ACK; the SC has taken
	// the message.
	SubmitAccepted SubmitState = "accepted"
	// SubmitFailed: the submit report is an RP-ERROR.
	SubmitFailed SubmitState = "failed"
)

// SentSMS is a short message that the UE sent, with what it has learnt
// of it since.
type SentSMS struct {
	// CallID is that of the MESSAGE that carried it, which the submit
	// report names in In-Reply-To (TS 24.341 5.3.1.2 NOTE 3).
	CallID string `json:"call_id"`
	To     string `json:"to"`
	Text   string `json:"text"`
	// MessageReference is TP-MR, which the status report repeats, and
	// RPMessageReference RP-MR, which the submit report repeats.
	MessageReference      uint8       `json:"message_reference"`
	RPMessageReference    uint8       `json:"rp_message_reference"`
	StatusReportRequested bool        `json:"status_report_requested"`
	Sent                  time.Time   `json:"sent"`
	State                 SubmitState `json:"state"`
	// ServiceCentreTimeStamp is the TP-SCTS of the SMS-SUBMIT-REPORT in
	// an RP-ACK, when it has one.
	ServiceCentreTimeStamp *time.Time `json:"service_centre_time_stamp,omitempty"`
	// RPCause is that of an RP-ERROR, and FailureCause the TP-FCS of the
	// SMS-SUBMIT-REPORT in it, when it has one.
	RPCause      sms.RPCause       `json:"rp_cause,omitempty"`
	FailureCause sms.FailureCause  `json:"failure_cause,omitempty"`
	StatusReport *StatusReportInfo `json:"status_report,omitempty"`
}

// StatusReportInfo is what an SMS-STATUS-REPORT told of a short message
// that the UE sent (TS 23.040 9.2.2.3).
type StatusReportInfo struct {
	// Status is TP-ST: 0 means the recipient received the message
	// (9.2.3.15).
	Status        sms.Status `json:"status"`
	DischargeTime time.Time  `json:"discharge_time"` // TP-DT
	Received      time.Time  `json:"received"`
}

// SMSHistory is what the UE has sent and received.
type SMSHistory struct {
	Sent     []SentSMS     `json:"sent"`
	Received []ReceivedSMS `json:"received"`
}

// smsState is the SMS side of the UE, under UE.mu.
type smsState struct {
	sent     []SentSMS
	received []ReceivedSMS
	// nextMR and nextTPMR are the RP-MR and the TP-MR of the next
	// SMS-SUBMIT. The MS picks a new RP-MR for each RP-DATA (TS 24.011
	// 8.2.3) and adds one to TP-MR for each SMS-SUBMIT (TS 23.040
	// 9.2.3.6).
	nextMR, nextTPMR uint8
	// changed is closed, and replaced, on each change, which wakes
	// WaitSMS.
	changed chan struct{}
}

// changedLocked wakes whoever waits for a change. The caller holds u.mu.
func (u *UE) changedLocked() {
	if u.sms.changed != nil {
		close(u.sms.changed)
	}
	u.sms.changed = make(chan struct{})
}

// SMS returns what the UE has sent and received so far.
func (u *UE) SMS() SMSHistory {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.smsLocked()
}

func (u *UE) smsLocked() SMSHistory {
	return SMSHistory{Sent: append([]SentSMS(nil), u.sms.sent...), Received: append([]ReceivedSMS(nil), u.sms.received...)}
}

// WaitSMS waits until done holds for the history, and returns it.
func (u *UE) WaitSMS(ctx context.Context, done func(SMSHistory) bool) (SMSHistory, error) {
	for {
		u.mu.Lock()
		h := u.smsLocked()
		if u.sms.changed == nil {
			u.sms.changed = make(chan struct{})
		}
		changed := u.sms.changed
		u.mu.Unlock()
		if done(h) {
			return h, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return h, ctx.Err()
		}
	}
}

// ReceiveSMS is the UAConfig.OnMessage of the UE: it takes each MESSAGE
// outside a dialog.
//
// SIMPLIFIED: RFC 3428 7. A UAS that takes MESSAGE must also take
// text/plain, the body of an instant message. The UE simulator takes
// only short messages, so any other body gets 415 (Unsupported Media
// Type) with Accept, as RFC 3261 21.4.13 says.
func (u *UE) ReceiveSMS(st *sip.ServerTransaction) {
	req := st.Request
	log := u.log.With("call_id", req.Get("Call-ID"))
	mediaType, _, _ := strings.Cut(req.Get("Content-Type"), ";")
	if !strings.EqualFold(strings.TrimSpace(mediaType), SMSContentType) {
		resp := sip.NewResponse(req, 415, sip.NewTag())
		resp.Add("Accept", SMSContentType)
		st.Respond(resp)
		return
	}
	// TS 24.341 5.3.2.3 has the receiver check nothing of who sent the
	// short message; the network sees to it. Every MESSAGE with this
	// media type that a user sends goes through the IP-SM-GW, by the
	// originating filter criterion in the user profile (TS 29.228 Annex
	// F, deploy/compose/hss.yaml), and the IP-SM-GW refuses RP-DATA that
	// a UE sends as the network would (TS 24.011 9.3.3).
	//
	// TS 24.011 9.3.1: a message too short for the message type and the
	// message reference is ignored. The MESSAGE that carried it still
	// gets its response (RFC 3428 7).
	if len(req.Body) < 2 {
		log.Warn("ignored a short message body too short for RP")
		st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
		return
	}
	// RP-MR is the second octet of every RP message (TS 24.011 8.2.3), so
	// an RP-ERROR can name it even when the rest is not understood. The
	// MESSAGE gets its response first; the RP layer answers in the
	// delivery report (TS 24.341 5.3.2.3).
	refuse := func(cause sms.RPCause) {
		st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
		u.sendDeliveryReport(req, sms.RPError{Direction: sms.MSToNetwork, MessageReference: req.Body[1], Cause: cause}, log)
	}
	rp, err := sms.ParseRP(req.Body)
	if err != nil {
		log.Warn("short message body not understood", "error", err.Error())
		switch sms.RPMTI(req.Body[0] & 0b111) {
		case sms.RPMTIErrorNetworkToMS:
			// TS 24.011 9.3.4: an RP-ERROR with an error in a mandatory
			// element is taken as one with cause 111, without diagnostic
			// or RP-User-Data. An RP-ERROR is never answered with an
			// RP-ERROR (9.3.3, 9.3.4).
			rp = sms.RPError{Direction: sms.NetworkToMS, MessageReference: req.Body[1], Cause: sms.RPCauseProtocolErrorUnspecified}
		case sms.RPMTIErrorMSToNetwork:
			// TS 24.011 9.3.3: an RP-ERROR that the protocol state does
			// not expect is ignored.
			st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
			return
		default:
			refuse(undecodableCause(req.Body))
			return
		}
	}
	switch rp.MTI() {
	case sms.RPMTIDataNetworkToMS:
		u.receiveRPData(st, rp.(sms.RPDataNetworkToMS), log)
	case sms.RPMTIAckNetworkToMS, sms.RPMTIErrorNetworkToMS:
		u.receiveSubmitReport(st, rp, log)
	case sms.RPMTIErrorMSToNetwork:
		// TS 24.011 9.3.3: an RP-ERROR that the protocol state does not
		// expect is ignored.
		st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
		log.Warn("ignored an RP-ERROR that answers nothing the UE sent")
	default:
		// TS 24.011 9.3.3: a message that an MS does not receive here.
		log.Warn("RP message that an MS does not receive", "rp", rpName(rp.MTI()))
		refuse(sms.RPCauseMessageNotCompatibleWithState)
	}
}

// undecodableCause is the RP-Cause for a body that is not an RP message
// the UE understands: a reserved message type is 97 (TS 24.011 9.3.3),
// anything else an error in a mandatory information element, 96
// (9.3.4).
func undecodableCause(body []byte) sms.RPCause {
	if mti := sms.RPMTI(body[0] & 0b111); mti > sms.RPMTISMMA {
		return sms.RPCauseMessageTypeNonExistent
	}
	return sms.RPCauseInvalidMandatoryInformation
}

// receiveRPData is TS 24.341 5.3.2.3: the UE answers the MESSAGE, takes
// the SMS-DELIVER out of the RP-DATA, and acknowledges it in a delivery
// report.
func (u *UE) receiveRPData(st *sip.ServerTransaction, m sms.RPDataNetworkToMS, log *slog.Logger) {
	req := st.Request
	st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
	tpdu, err := sms.ParseTPDU(m)
	if report, ok := tpdu.(sms.StatusReport); err == nil && ok {
		u.receiveStatusReport(req, m, report, log)
		return
	}
	var deliver sms.Deliver
	var text string
	fcs := sms.FCSTPDUNotSupported
	if err == nil {
		deliver = tpdu.(sms.Deliver) // ParseTPDU gives no other TPDU for RP-DATA from the network
		if text, err = sms.DecodeText(deliver.DataCodingScheme, deliver.UserData); err != nil {
			fcs = textFailureCause(deliver.DataCodingScheme)
		}
	}
	if err != nil {
		// TS 23.040 9.2.2.1a: the MS refuses the TPDU with TP-FCS in an
		// SMS-DELIVER-REPORT for RP-ERROR. TS 24.011 has no RP-Cause for
		// a TPDU the MS cannot take, so it is 111, the cause for when no
		// other applies (Annex E-3).
		log.Warn("short message not taken", "error", err.Error())
		report, _ := sms.DeliverReport{FailureCause: fcs}.Marshal()
		u.sendDeliveryReport(req, sms.RPError{Direction: sms.MSToNetwork, MessageReference: m.MessageReference,
			Cause: sms.RPCauseProtocolErrorUnspecified, UserData: report}, log)
		return
	}
	got := ReceivedSMS{CallID: req.Get("Call-ID"), From: deliver.OriginatingAddress.String(), Text: text,
		ServiceCentreTimeStamp: deliver.ServiceCentreTimeStamp, Received: time.Now()}
	u.mu.Lock()
	u.sms.received = append(u.sms.received, got)
	u.changedLocked()
	u.mu.Unlock()
	log.Info("sms received", "from", got.From, "text", got.Text,
		"service_centre_time_stamp", got.ServiceCentreTimeStamp.Format(time.RFC3339))
	report, _ := sms.DeliverReport{}.Marshal()
	u.sendDeliveryReport(req, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: m.MessageReference, UserData: report}, log)
}

// textFailureCause is the TP-FCS for a text that DecodeText refused: an
// alphabet or a compression the UE does not take, 0x90 (TS 23.040
// 9.2.3.22), or else an unspecified error.
func textFailureCause(dcs sms.DataCodingScheme) sms.FailureCause {
	if dcs.Compressed() || dcs.Alphabet() == sms.Alphabet8Bit {
		return sms.FCSDataCodingSchemeNotSupported
	}
	return sms.FCSUnspecifiedError
}

// sendDeliveryReport is TS 24.341 5.3.2.4: a MESSAGE of its own to the
// IP-SM-GW, which the received MESSAGE names in P-Asserted-Identity,
// with the Call-ID of that MESSAGE in In-Reply-To. The body is the RP-ACK
// or RP-ERROR for the RP-DATA.
func (u *UE) sendDeliveryReport(received *sip.Message, report sms.RPMessage, log *slog.Logger) {
	ipsmgw, ok := ipsmgwOf(received)
	if !ok {
		log.Warn("no delivery report: the MESSAGE names no IP-SM-GW in P-Asserted-Identity")
		return
	}
	body, err := report.Marshal()
	if err != nil {
		log.Warn("no delivery report", "error", err.Error())
		return
	}
	req, err := u.newSMSRequest(ipsmgw, body)
	if err != nil {
		log.Warn("no delivery report", "error", err.Error())
		return
	}
	req.Add("In-Reply-To", received.Get("Call-ID"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	resp, err := u.request(ctx, req)
	attrs := []any{"to", ipsmgw, "rp", rpName(report.MTI()), "report_call_id", req.Get("Call-ID")}
	if err != nil {
		log.Warn("delivery report failed", append(attrs, "error", err.Error())...)
		return
	}
	log.Info("delivery report sent", append(attrs, "status", resp.StatusCode)...)
}

// ipsmgwOf is the SIP URI of the IP-SM-GW that sent a MESSAGE, which it
// puts in P-Asserted-Identity (TS 24.341 5.3.2.4 NOTE 1, 5.3.3.4.2 d).
func ipsmgwOf(m *sip.Message) (string, bool) {
	for _, v := range m.GetAll("P-Asserted-Identity") {
		na, err := sip.ParseNameAddr(v)
		if err != nil {
			continue
		}
		if u, err := sip.ParseURI(na.URI); err == nil && (u.Scheme == "sip" || u.Scheme == "sips") {
			return na.URI, true
		}
	}
	return "", false
}

// newSMSRequest is a MESSAGE from the UE that carries body, an RP
// message, to target, along the route of every request of the UE (TS
// 24.341 5.3.1.2 and 5.3.2.4).
func (u *UE) newSMSRequest(target string, body []byte) (*sip.Message, error) {
	reg, ok := u.registration()
	if !ok {
		return nil, ErrNotRegistered
	}
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: target}
	m.Add("Max-Forwards", "70")
	for _, r := range u.routeSet(reg) {
		m.Add("Route", r)
	}
	m.Add("From", "<"+reg.DefaultIdentity()+">;tag="+sip.NewTag())
	m.Add("To", "<"+target+">")
	m.Add("Call-ID", sip.NewCallID())
	// A MESSAGE is a transaction of its own (RFC 3428 4), so each one
	// starts its own CSeq.
	m.Add("CSeq", "1 MESSAGE")
	// TS 24.341 5.3.1.2 NOTE 5: the body is binary. A MESSAGE carries no
	// Contact (RFC 3428 4).
	m.Add("Content-Type", SMSContentType)
	m.Add("User-Agent", UserAgent)
	u.addAccessNetworkInfo(m)
	m.Body = body
	return m, nil
}

// rpName names an RP message type for the log, as TS 24.011 table 8.3
// does.
func rpName(mti sms.RPMTI) string {
	switch mti {
	case sms.RPMTIDataMSToNetwork, sms.RPMTIDataNetworkToMS:
		return "RP-DATA"
	case sms.RPMTIAckMSToNetwork, sms.RPMTIAckNetworkToMS:
		return "RP-ACK"
	case sms.RPMTIErrorMSToNetwork, sms.RPMTIErrorNetworkToMS:
		return "RP-ERROR"
	case sms.RPMTISMMA:
		return "RP-SMMA"
	}
	return "RP MTI " + strconv.Itoa(int(mti))
}

// ErrBadShortMessage means SendSMS was given a short message it cannot
// send: a number not in international format, or a text that does not
// fit in one short message.
var ErrBadShortMessage = errors.New("not a short message the UE can send")

// ErrNoServiceCentre means the UE has no SC to submit a short message
// to (Config.SCAddress).
var ErrNoServiceCentre = errors.New("the UE has no SC address")

// SMSRefused is a MESSAGE with a short message that got a final response
// other than 2xx.
type SMSRefused struct {
	StatusCode int
	Reason     string
}

func (e *SMSRefused) Error() string {
	return fmt.Sprintf("MESSAGE answered %d %s", e.StatusCode, e.Reason)
}

// SendSMS submits a short message as TS 24.341 5.3.1.2 says: an
// SMS-SUBMIT inside an RP-DATA, in a MESSAGE to the PSI of the SC that
// goes along the Service-Route like any request of the UE. It returns
// once the MESSAGE has its final response, a 202 (Accepted) from the
// IP-SM-GW (RFC 3428 7). The submit report, and the status report when
// one is asked for, come later in MESSAGEs of their own; WaitSMS waits
// for them.
func (u *UE) SendSMS(ctx context.Context, out OutgoingSMS) (SentSMS, error) {
	if u.cfg.SCAddress == "" {
		return SentSMS{}, ErrNoServiceCentre
	}
	to, err := internationalNumber(out.To)
	if err != nil {
		return SentSMS{}, err
	}
	dcs, ud, err := sms.EncodeText(out.Text)
	if err != nil {
		return SentSMS{}, fmt.Errorf("%w: %w", ErrBadShortMessage, err)
	}
	u.mu.Lock()
	mr, tpMR := u.sms.nextMR, u.sms.nextTPMR
	u.sms.nextMR++
	u.sms.nextTPMR++
	u.mu.Unlock()
	// SIMPLIFIED: TS 23.040 9.2.3.12 and 9.2.3.24. The SMS-SUBMIT asks
	// for no validity period, which the SC stub has no use for, and holds
	// one short message only, without a User-Data-Header.
	tpdu, err := sms.Submit{
		StatusReportRequest: out.StatusReport,
		MessageReference:    tpMR,
		DestinationAddress:  sms.InternationalNumber(to),
		DataCodingScheme:    dcs,
		UserData:            ud,
	}.Marshal()
	if err != nil {
		return SentSMS{}, err
	}
	// TS 24.341 5.3.1.2 e and NOTE 4: the RP-DATA names the SC in
	// RP-DA, the address of the SC that the USIM would hold.
	body, err := sms.RPDataMSToNetwork{MessageReference: mr, DestinationAddress: sms.InternationalNumber(u.cfg.SCAddress),
		UserData: tpdu}.Marshal()
	if err != nil {
		return SentSMS{}, err
	}
	req, err := u.newSMSRequest(serviceCentrePSI(u.cfg.SCAddress), body)
	if err != nil {
		return SentSMS{}, err
	}
	sent := SentSMS{CallID: req.Get("Call-ID"), To: to, Text: out.Text, MessageReference: tpMR, RPMessageReference: mr,
		StatusReportRequested: out.StatusReport, Sent: time.Now(), State: Submitted}
	// The submit report may overtake the 202, so the UE knows the Call-ID
	// before it sends.
	u.mu.Lock()
	u.sms.sent = append(u.sms.sent, sent)
	u.changedLocked()
	u.mu.Unlock()
	resp, err := u.request(ctx, req)
	if err == nil && resp.StatusCode >= 300 {
		err = &SMSRefused{StatusCode: resp.StatusCode, Reason: resp.Reason}
	}
	log := u.log.With("call_id", sent.CallID)
	if err != nil {
		u.mu.Lock()
		u.sms.sent = slices.DeleteFunc(u.sms.sent, func(s SentSMS) bool { return s.CallID == sent.CallID })
		u.changedLocked()
		u.mu.Unlock()
		log.Warn("sms not sent", "to", to, "error", err.Error())
		return SentSMS{}, err
	}
	log.Info("sms sent", "to", to, "text", out.Text, "tp_mr", int(tpMR), "rp_mr", int(mr), "status", resp.StatusCode)
	return sent, nil
}

// serviceCentrePSI is the PSI of the SC as a tel URI made from the
// address of the SC, one of the forms TS 24.341 5.3.1.2 NOTE 1 allows
// when the PSI comes from that address.
func serviceCentrePSI(scAddress string) string { return "tel:" + scAddress }

// internationalNumber checks that number is an E.164 number with its
// "+", such as +12025550102, and returns it. A tel URI of one is taken
// too.
func internationalNumber(number string) (string, error) {
	number = strings.TrimPrefix(number, "tel:")
	digits, plus := strings.CutPrefix(number, "+")
	if !plus || len(digits) == 0 || len(digits) > 15 || strings.Trim(digits, "0123456789") != "" {
		return "", fmt.Errorf("%w: %q is no number in international format, such as +12025550102", ErrBadShortMessage, number)
	}
	return number, nil
}

// receiveSubmitReport is the end of TS 24.341 5.3.1.2: a MESSAGE whose
// In-Reply-To names a short message the UE sent gets 200, and its RP-ACK
// or RP-ERROR tells what became of the message; any other gets 488 (Not
// Acceptable Here).
func (u *UE) receiveSubmitReport(st *sip.ServerTransaction, rp sms.RPMessage, log *slog.Logger) {
	req := st.Request
	callID := req.Get("In-Reply-To")
	u.mu.Lock()
	i := slices.IndexFunc(u.sms.sent, func(s SentSMS) bool { return s.CallID == callID })
	if callID == "" || i < 0 {
		u.mu.Unlock()
		st.Respond(sip.NewResponse(req, 488, sip.NewTag()))
		log.Warn("submit report for no short message the UE sent", "in_reply_to", callID)
		return
	}
	sent := u.sms.sent[i]
	// SIMPLIFIED: TS 24.011 9.3.2. An RP-ACK or RP-ERROR whose RP-MR is
	// not that of the RP-DATA is discarded. For the RP-ACK the MS would
	// also send an RP-ERROR with cause 81 on the connection it came on;
	// over IP there is no such connection.
	mr := req.Body[1]
	if mr == sent.RPMessageReference {
		tpdu, _ := sms.ParseTPDU(rp)
		report, _ := tpdu.(sms.SubmitReport)
		switch m := rp.(type) {
		case sms.RPAck:
			sent.State = SubmitAccepted
			if tpdu != nil {
				sent.ServiceCentreTimeStamp = &report.ServiceCentreTimeStamp
			}
		case sms.RPError:
			sent.State, sent.RPCause, sent.FailureCause = SubmitFailed, m.Cause, report.FailureCause
		}
		u.sms.sent[i] = sent
		u.changedLocked()
	}
	u.mu.Unlock()
	st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
	log = log.With("sms_call_id", sent.CallID)
	if mr != sent.RPMessageReference {
		log.Warn("discarded a submit report for another RP-MR", "rp_mr", int(mr), "want", int(sent.RPMessageReference))
		return
	}
	attrs := []any{"state", string(sent.State), "rp", rpName(rp.MTI())}
	if sent.ServiceCentreTimeStamp != nil {
		attrs = append(attrs, "service_centre_time_stamp", sent.ServiceCentreTimeStamp.Format(time.RFC3339))
	}
	if sent.State == SubmitFailed {
		attrs = append(attrs, "rp_cause", int(sent.RPCause), "tp_fcs", int(sent.FailureCause))
	}
	log.Info("submit report", attrs...)
}

// receiveStatusReport is TS 24.341 5.3.1.3: the status report of a short
// message the UE sent, which it acknowledges with a delivery report as
// for any RP-DATA (5.3.2.4).
func (u *UE) receiveStatusReport(received *sip.Message, m sms.RPDataNetworkToMS, report sms.StatusReport, log *slog.Logger) {
	u.mu.Lock()
	// TP-MR names the SMS-SUBMIT (TS 23.040 9.2.3.6). It wraps after 255,
	// so the latest short message with it is the one.
	found := false
	for i := len(u.sms.sent) - 1; i >= 0; i-- {
		if s := &u.sms.sent[i]; s.StatusReportRequested && s.MessageReference == report.MessageReference {
			s.StatusReport = &StatusReportInfo{Status: report.Status, DischargeTime: report.DischargeTime, Received: time.Now()}
			log = log.With("sms_call_id", s.CallID)
			found = true
			break
		}
	}
	u.changedLocked()
	u.mu.Unlock()
	if found {
		log.Info("status report", "tp_mr", int(report.MessageReference), "status", int(report.Status),
			"discharge_time", report.DischargeTime.Format(time.RFC3339))
	} else {
		log.Warn("status report for no short message that asked for one", "tp_mr", int(report.MessageReference))
	}
	ack, _ := sms.DeliverReport{}.Marshal()
	u.sendDeliveryReport(received, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: m.MessageReference, UserData: ack}, log)
}
