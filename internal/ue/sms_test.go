package ue_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

const (
	ipsmgwURI = "sip:ipsmgw." + domain + ":5060"
	// The numbers are fictitious NANP numbers (555-0100 to 555-0199).
	scAddress = "+12025550100"
	senderNo  = "+12025550101"
)

// fakeNetwork stands for the P-CSCF and everything behind it: it
// registers the UE at once, and passes every other request of the UE to
// the test, answered with the status answer.
type fakeNetwork struct {
	t        *testing.T
	stack    *sip.Stack
	requests chan *sip.Message
}

// newFakeNetwork answers each MESSAGE 202, as the IP-SM-GW does.
func newFakeNetwork(t *testing.T) *fakeNetwork { return newFakeNetworkAnswering(t, 202) }

func newFakeNetworkAnswering(t *testing.T, answer int) *fakeNetwork {
	t.Helper()
	n := &fakeNetwork{t: t, requests: make(chan *sip.Message, 8)}
	n.stack = stack(t, func(st *sip.ServerTransaction) {
		req := st.Request
		if req.Method == sip.REGISTER {
			resp := sip.NewResponse(req, 200, sip.NewTag())
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", req.Get("Contact"))
			st.Respond(resp)
			return
		}
		st.Respond(sip.NewResponse(req, answer, sip.NewTag()))
		n.requests <- req
	})
	return n
}

// next is the next request of the UE besides REGISTER.
func (n *fakeNetwork) next() *sip.Message {
	n.t.Helper()
	select {
	case m := <-n.requests:
		return m
	case <-time.After(5 * time.Second):
		n.t.Fatal("the UE sent no request")
		return nil
	}
}

// smsUE is a UE on a UA that takes MESSAGEs, registered with n.
func smsUE(t *testing.T, n *fakeNetwork) (*ue.UE, *sip.UA) {
	t.Helper()
	u, ua := newSMSUE(t, n.stack.LocalAddr(), impi, impu, password, ue.DefaultFeatures)
	if _, err := u.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return u, ua
}

// newSMSUE is a UE whose requests go to pcscf, on a UA that hands it
// the MESSAGEs. It registers features in its Contact.
func newSMSUE(t *testing.T, pcscf netip.AddrPort, private, public, pass string, features sip.Params) (*ue.UE, *sip.UA) {
	t.Helper()
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	// The UA needs the UE's ReceiveSMS before the UE exists.
	var u atomic.Pointer[ue.UE]
	ua, err := sip.NewUA(tr, sip.UAConfig{
		SentBy: addr.String(), Contact: ue.ContactURI(public, addr), OnCall: func(c *sip.IncomingCall) { c.Reject(486) },
		OnMessage: func(st *sip.ServerTransaction) { u.Load().ReceiveSMS(st) },
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	u.Store(ue.New(ue.Config{PrivateIdentity: private, PublicIdentity: public, Password: pass, HomeDomain: domain,
		PCSCF: pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Features: features, SCAddress: scAddress},
		ua.Stack(), addr, quiet()))
	return u.Load(), ua
}

// mtMessage is a MESSAGE from the IP-SM-GW to the UE (TS 24.341
// 5.3.3.4.2) with body, sent straight to the UE's address.
func (n *fakeNetwork) mtMessage(ua *sip.UA, contentType string, body []byte, extra ...sip.HeaderField) (*sip.Message, int) {
	n.t.Helper()
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: ue.ContactURI(impu, ua.Stack().LocalAddr())}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+ipsmgwURI+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", "<"+ipsmgwURI+">")
	m.Add("Accept-Contact", "*;+g.3gpp.smsip;require;explicit")
	m.Add("Request-Disposition", "no-fork")
	for _, f := range extra {
		m.Add(f.Name, f.Value)
	}
	m.Add("Content-Type", contentType)
	m.Body = body
	ct, err := n.stack.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: ua.Stack().LocalAddr()})
	if err != nil {
		n.t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		n.t.Fatalf("no final response: %v", ct.Err())
	}
	return m, final.StatusCode
}

// rpDataWith is an RP-DATA from the network with tpdu (TS 24.011
// 7.3.1.1).
func rpDataWith(t *testing.T, mr uint8, tpdu []byte) []byte {
	t.Helper()
	b, err := sms.RPDataNetworkToMS{MessageReference: mr, OriginatorAddress: sms.InternationalNumber(scAddress), UserData: tpdu}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// deliverOf is an SMS-DELIVER of text from the test sender.
func deliverOf(t *testing.T, text string, scts time.Time) []byte {
	t.Helper()
	dcs, ud, err := sms.EncodeText(text)
	if err != nil {
		t.Fatal(err)
	}
	b, err := sms.Deliver{OriginatingAddress: sms.InternationalNumber(senderNo), DataCodingScheme: dcs,
		ServiceCentreTimeStamp: scts, UserData: ud}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// report reads the RP message of a report the UE sent, after checking
// the header fields of TS 24.341 5.3.2.4.
func checkDeliveryReport(t *testing.T, report, delivered *sip.Message) sms.RPMessage {
	t.Helper()
	if report.Method != sip.MESSAGE || report.RequestURI != ipsmgwURI {
		t.Errorf("report is %s %s, want a MESSAGE to the IP-SM-GW", report.Method, report.RequestURI)
	}
	if to, _ := sip.ParseNameAddr(report.Get("To")); to.URI != ipsmgwURI {
		t.Errorf("To = %q, want the IP-SM-GW", report.Get("To"))
	}
	if from, _ := sip.ParseNameAddr(report.Get("From")); from.URI != impu {
		t.Errorf("From = %q, want the UE's public identity", report.Get("From"))
	}
	if got := report.Get("In-Reply-To"); got != delivered.Get("Call-ID") {
		t.Errorf("In-Reply-To = %q, want the Call-ID %q of the delivered MESSAGE", got, delivered.Get("Call-ID"))
	}
	if report.Get("Content-Type") != ue.SMSContentType || report.Get("Contact") != "" {
		t.Errorf("Content-Type %q and Contact %q, want %s and none", report.Get("Content-Type"), report.Get("Contact"), ue.SMSContentType)
	}
	if routes := report.GetAll("Route"); len(routes) != 2 {
		t.Errorf("Route = %v, want the P-CSCF and the Service-Route", routes)
	}
	m, err := sms.ParseRP(report.Body)
	if err != nil {
		t.Fatalf("report body: %v", err)
	}
	return m
}

// TS 24.341 5.3.2.3 and 5.3.2.4: the UE answers 200, keeps the message,
// and acknowledges it in a MESSAGE of its own, an RP-ACK with the RP-MR
// of the RP-DATA and an SMS-DELIVER-REPORT.
func TestTheUEReceivesAShortMessage(t *testing.T) {
	for _, text := range []string{"Hello from the IMS @ 10:00", "こんにちは、IMS の SMS です"} {
		n := newFakeNetwork(t)
		u, ua := smsUE(t, n)
		scts := time.Date(2026, 11, 20, 10, 0, 0, 0, time.FixedZone("", 9*3600))
		delivered, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 42, deliverOf(t, text, scts)))
		if code != 200 {
			t.Fatalf("MT MESSAGE got %d, want 200", code)
		}
		ack, ok := checkDeliveryReport(t, n.next(), delivered).(sms.RPAck)
		if !ok || ack.Direction != sms.MSToNetwork || ack.MessageReference != 42 {
			t.Fatalf("report %+v, want an RP-ACK from the MS for RP-MR 42", ack)
		}
		if tpdu, err := sms.ParseTPDU(ack); err != nil {
			t.Errorf("RP-ACK carries no SMS-DELIVER-REPORT: %v", err)
		} else if _, ok := tpdu.(sms.DeliverReport); !ok {
			t.Errorf("RP-ACK carries %T", tpdu)
		}
		got := u.SMS().Received
		if len(got) != 1 || got[0].From != senderNo || got[0].Text != text || !got[0].ServiceCentreTimeStamp.Equal(scts) ||
			got[0].CallID != delivered.Get("Call-ID") {
			t.Errorf("received %+v, want %q from %s", got, text, senderNo)
		}
	}
}

// TS 24.011 9.3: the UE refuses what it cannot take with RP-ERROR, and
// TS 23.040 9.2.2.1a a TPDU it cannot take with TP-FCS besides.
func TestTheUERefusesWhatItCannotTake(t *testing.T) {
	deliver8Bit, err := sms.Deliver{OriginatingAddress: sms.InternationalNumber(senderNo), DataCodingScheme: 0x04,
		ServiceCentreTimeStamp: time.Now(), UserData: sms.UserData{Length: 2, Data: []byte{1, 2}}}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		body  []byte
		cause sms.RPCause
		fcs   sms.FailureCause // 0: no SMS-DELIVER-REPORT
	}{
		{"reserved RP-MTI", []byte{0x07, 9, 0, 0}, sms.RPCauseMessageTypeNonExistent, 0},
		{"RP-DATA cut short", []byte{0x01, 9, 0x05}, sms.RPCauseInvalidMandatoryInformation, 0},
		{"RP-SMMA, which goes the other way", []byte{0x06, 9}, sms.RPCauseMessageNotCompatibleWithState, 0},
		{"8 bit data", rpDataWith(t, 9, deliver8Bit), sms.RPCauseProtocolErrorUnspecified, sms.FCSDataCodingSchemeNotSupported},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := newFakeNetwork(t)
			u, ua := smsUE(t, n)
			delivered, code := n.mtMessage(ua, ue.SMSContentType, tt.body)
			if code != 200 {
				t.Fatalf("MT MESSAGE got %d, want 200", code)
			}
			e, ok := checkDeliveryReport(t, n.next(), delivered).(sms.RPError)
			if !ok || e.MessageReference != 9 || e.Cause != tt.cause {
				t.Fatalf("report %+v, want RP-ERROR for RP-MR 9 with cause %d", e, tt.cause)
			}
			if tt.fcs != 0 {
				tpdu, err := sms.ParseTPDU(e)
				if r, ok := tpdu.(sms.DeliverReport); err != nil || !ok || r.FailureCause != tt.fcs {
					t.Errorf("RP-ERROR carries %+v (%v), want TP-FCS %#x", tpdu, err, tt.fcs)
				}
			}
			if got := u.SMS().Received; len(got) != 0 {
				t.Errorf("the UE kept %+v", got)
			}
		})
	}
}

// RFC 3261 21.4.13: a MESSAGE with a body other than a short message
// gets 415 with the type the UE takes.
func TestTheUEOnlyTakesShortMessages(t *testing.T) {
	n := newFakeNetwork(t)
	_, ua := smsUE(t, n)
	if _, code := n.mtMessage(ua, "text/plain", []byte("hello")); code != 415 {
		t.Errorf("text/plain MESSAGE got %d, want 415", code)
	}
}

// submitted checks the MESSAGE of TS 24.341 5.3.1.2 that SendSMS sent,
// and returns the RP-DATA and the SMS-SUBMIT in it.
func submitted(t *testing.T, m *sip.Message) (sms.RPDataMSToNetwork, sms.Submit) {
	t.Helper()
	psi := "tel:" + scAddress
	if m.Method != sip.MESSAGE || m.RequestURI != psi {
		t.Errorf("request is %s %s, want a MESSAGE to the PSI of the SC", m.Method, m.RequestURI)
	}
	if to, _ := sip.ParseNameAddr(m.Get("To")); to.URI != psi {
		t.Errorf("To = %q, want the PSI of the SC", m.Get("To"))
	}
	if from, _ := sip.ParseNameAddr(m.Get("From")); from.URI != impu {
		t.Errorf("From = %q, want the UE's public identity", m.Get("From"))
	}
	if m.Get("Content-Type") != ue.SMSContentType || m.Get("Contact") != "" || m.Get("In-Reply-To") != "" {
		t.Errorf("Content-Type %q, Contact %q, In-Reply-To %q; want %s and neither of the others",
			m.Get("Content-Type"), m.Get("Contact"), m.Get("In-Reply-To"), ue.SMSContentType)
	}
	if routes := m.GetAll("Route"); len(routes) != 2 || routes[1] != "<sip:orig@scscf."+domain+":5060;lr>" {
		t.Errorf("Route = %v, want the P-CSCF and the Service-Route", routes)
	}
	rp, err := sms.ParseRP(m.Body)
	if err != nil {
		t.Fatalf("body: %v", err)
	}
	data, ok := rp.(sms.RPDataMSToNetwork)
	if !ok || data.DestinationAddress.String() != scAddress {
		t.Fatalf("body %+v, want RP-DATA to the SC", rp)
	}
	tpdu, err := sms.ParseTPDU(data)
	if err != nil {
		t.Fatalf("TPDU: %v", err)
	}
	return data, tpdu.(sms.Submit)
}

// marshal is m on the wire.
func marshal(t *testing.T, m sms.RPMessage) []byte {
	t.Helper()
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// waitSent waits until the short message with callID is in state.
func waitSent(t *testing.T, u *ue.UE, callID string, done func(ue.SentSMS) bool) ue.SentSMS {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got ue.SentSMS
	_, err := u.WaitSMS(ctx, func(h ue.SMSHistory) bool {
		for _, s := range h.Sent {
			if s.CallID == callID {
				got = s
				return done(s)
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("short message %s: %+v (%v)", callID, got, err)
	}
	return got
}

// TS 24.341 5.3.1.2: the UE submits an SMS-SUBMIT in an RP-DATA, and the
// submit report that comes back with its Call-ID in In-Reply-To tells it
// that the SC took the message. 5.3.1.3: the status report it asked for
// comes as an RP-DATA, which it acknowledges.
func TestTheUESendsAShortMessage(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	const to, text = "+12025550102", "Hello from the IMS"
	sent, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: to, Text: text, StatusReport: true})
	if err != nil {
		t.Fatal(err)
	}
	req := n.next()
	data, submit := submitted(t, req)
	if got, _ := sms.DecodeText(submit.DataCodingScheme, submit.UserData); got != text ||
		submit.DestinationAddress.String() != to || !submit.StatusReportRequest || submit.MessageReference != sent.MessageReference {
		t.Errorf("SMS-SUBMIT %+v with text %q, want %q to %s with TP-SRR", submit, got, text, to)
	}
	if sent.State != ue.Submitted || sent.CallID != req.Get("Call-ID") || sent.RPMessageReference != data.MessageReference {
		t.Errorf("sent %+v", sent)
	}

	// TS 24.341 5.3.1.2: a submit report for a short message the UE did
	// not send gets 488.
	ack := sms.RPAck{Direction: sms.NetworkToMS, MessageReference: data.MessageReference}
	if _, code := n.mtMessage(ua, ue.SMSContentType, marshal(t, ack), sip.HeaderField{Name: "In-Reply-To", Value: "unknown@callid"}); code != 488 {
		t.Errorf("submit report for another Call-ID got %d, want 488", code)
	}
	scts := time.Date(2026, 11, 20, 10, 0, 5, 0, time.UTC)
	report, err := sms.SubmitReport{ServiceCentreTimeStamp: scts}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	ack.UserData = report
	if _, code := n.mtMessage(ua, ue.SMSContentType, marshal(t, ack), sip.HeaderField{Name: "In-Reply-To", Value: sent.CallID}); code != 200 {
		t.Errorf("submit report got %d, want 200", code)
	}
	got := waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.State == ue.SubmitAccepted })
	if got.ServiceCentreTimeStamp == nil || !got.ServiceCentreTimeStamp.Equal(scts) {
		t.Errorf("TP-SCTS %v, want %v", got.ServiceCentreTimeStamp, scts)
	}

	statusReport, err := sms.StatusReport{MessageReference: sent.MessageReference, RecipientAddress: sms.InternationalNumber(to),
		ServiceCentreTimeStamp: scts, DischargeTime: scts.Add(time.Second), Status: sms.StatusReceivedBySME}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	delivered, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 77, statusReport))
	if code != 200 {
		t.Fatalf("status report got %d, want 200", code)
	}
	if a, ok := checkDeliveryReport(t, n.next(), delivered).(sms.RPAck); !ok || a.MessageReference != 77 {
		t.Errorf("report %+v, want an RP-ACK for RP-MR 77", a)
	}
	got = waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.StatusReport != nil })
	if got.StatusReport.Status != sms.StatusReceivedBySME || !got.StatusReport.DischargeTime.Equal(scts.Add(time.Second)) {
		t.Errorf("status report %+v", got.StatusReport)
	}
}

// The SC may start the delivery before the IP-SM-GW sends the submit
// report, and SIP puts no order on separate MESSAGEs (RFC 3428 7), so
// the status report can come first. The UE keeps both.
func TestStatusReportBeforeSubmitReport(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	const to = "+12025550102"
	sent, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: to, Text: "quick", StatusReport: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := submitted(t, n.next())
	scts := time.Date(2026, 11, 20, 10, 0, 5, 0, time.UTC)

	statusReport, err := sms.StatusReport{MessageReference: sent.MessageReference, RecipientAddress: sms.InternationalNumber(to),
		ServiceCentreTimeStamp: scts, DischargeTime: scts.Add(time.Second), Status: sms.StatusReceivedBySME}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 77, statusReport)); code != 200 {
		t.Fatalf("status report got %d, want 200", code)
	}
	n.next() // the delivery report for the status report
	if got := waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.StatusReport != nil }); got.State != ue.Submitted {
		t.Errorf("state %s before the submit report, want %s", got.State, ue.Submitted)
	}

	report, err := sms.SubmitReport{ServiceCentreTimeStamp: scts}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	ack := sms.RPAck{Direction: sms.NetworkToMS, MessageReference: data.MessageReference, UserData: report}
	if _, code := n.mtMessage(ua, ue.SMSContentType, marshal(t, ack), sip.HeaderField{Name: "In-Reply-To", Value: sent.CallID}); code != 200 {
		t.Errorf("submit report got %d, want 200", code)
	}
	got := waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.State == ue.SubmitAccepted })
	if got.StatusReport == nil || got.StatusReport.Status != sms.StatusReceivedBySME {
		t.Errorf("after the submit report: %+v, want the status report kept", got)
	}
}

// TS 24.341 5.3.1.2: an RP-ERROR in the submit report says that the SC
// refused the message, with RP-Cause and TP-FCS.
func TestTheSCRefusesAShortMessage(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	sent, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550199", Text: "nobody"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := submitted(t, n.next())
	report, err := sms.SubmitReport{FailureCause: sms.FCSInvalidSMEAddress, ServiceCentreTimeStamp: time.Now()}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	e := sms.RPError{Direction: sms.NetworkToMS, MessageReference: data.MessageReference, Cause: sms.RPCauseUnassignedNumber, UserData: report}
	if _, code := n.mtMessage(ua, ue.SMSContentType, marshal(t, e), sip.HeaderField{Name: "In-Reply-To", Value: sent.CallID}); code != 200 {
		t.Errorf("submit report got %d, want 200", code)
	}
	got := waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.State != ue.Submitted })
	if got.State != ue.SubmitFailed || got.RPCause != sms.RPCauseUnassignedNumber || got.FailureCause != sms.FCSInvalidSMEAddress {
		t.Errorf("sent %+v, want failed with RP-Cause 1 and TP-FCS 0xc3", got)
	}
}

// TS 24.011 9.3 through TS 24.007 11.2.4: a submit report with an IE
// the UE does not know still reports, and an RP-ERROR whose RP-Cause is
// broken counts as one with cause 111. Neither is answered.
func TestSubmitReportsThatAreHardToRead(t *testing.T) {
	tests := []struct {
		name  string
		body  func(mr uint8) []byte
		state ue.SubmitState
		cause sms.RPCause
	}{
		// RP-ACK with an IE of IEI 0x42, which no release defines yet.
		{"RP-ACK with an unknown IE", func(mr uint8) []byte { return []byte{0x03, mr, 0x42, 0x01, 0x00} }, ue.SubmitAccepted, 0},
		// RP-ERROR with an empty RP-Cause.
		{"RP-ERROR without a cause", func(mr uint8) []byte { return []byte{0x05, mr, 0x00} }, ue.SubmitFailed, sms.RPCauseProtocolErrorUnspecified},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := newFakeNetwork(t)
			u, ua := smsUE(t, n)
			sent, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550102", Text: "hi"})
			if err != nil {
				t.Fatal(err)
			}
			data, _ := submitted(t, n.next())
			if _, code := n.mtMessage(ua, ue.SMSContentType, tt.body(data.MessageReference),
				sip.HeaderField{Name: "In-Reply-To", Value: sent.CallID}); code != 200 {
				t.Errorf("submit report got %d, want 200", code)
			}
			got := waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.State != ue.Submitted })
			if got.State != tt.state || got.RPCause != tt.cause {
				t.Errorf("sent %+v, want %s with RP-Cause %d", got, tt.state, tt.cause)
			}
			select {
			case m := <-n.requests:
				t.Errorf("the UE answered the submit report with %q", m.Body)
			case <-time.After(200 * time.Millisecond):
			}
		})
	}
}

// RFC 3428 7: a MESSAGE that the network refuses fails SendSMS, and the
// UE keeps no record of it; nor does it send what is no short message.
func TestShortMessagesThatAreNotSent(t *testing.T) {
	n := newFakeNetworkAnswering(t, 403)
	u, _ := smsUE(t, n)
	var refused *ue.SMSRefused
	if _, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550102", Text: "hi"}); !errors.As(err, &refused) || refused.StatusCode != 403 {
		t.Errorf("SendSMS: %v, want the 403", err)
	}
	n.next()
	if _, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: "12025550102", Text: "hi"}); err == nil {
		t.Error("SendSMS took a number without +")
	}
	if _, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550102", Text: strings.Repeat("a", 161)}); !errors.Is(err, sms.ErrTextTooLong) {
		t.Errorf("SendSMS of 161 characters: %v, want ErrTextTooLong", err)
	}
	if h := u.SMS(); len(h.Sent) != 0 {
		t.Errorf("sent %+v, want none", h.Sent)
	}
}
