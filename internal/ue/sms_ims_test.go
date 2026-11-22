package ue_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// The tests of this file run SMS over IP through the whole IMS (TS
// 24.341 B.5 and B.6), with a fake IP-SM-GW as the application server of
// both users. It does what TS 24.341 5.3.3.4 asks of an IP-SM-GW whose
// SC takes every message at once: 202 for the submission, a submit
// report back to the sender, and the short message to the recipient,
// both through the I-CSCF.

const (
	telA = "tel:+12025550101"
	telB = "tel:+12025550102"
)

// moSMSCriterion sends the short messages a user sends to the IP-SM-GW
// (TS 23.218 5.2.3).
func moSMSCriterion() ifc.InitialFilterCriteria {
	originating := ifc.OriginatingRegistered
	return ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "MESSAGE"},
			{Group: []int{1}, SIPHeader: &ifc.SIPHeader{Header: "^Content-Type$", Content: `^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$`}},
			{Group: []int{2}, SessionCase: &originating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: ipsmgwURI, DefaultHandling: ifc.SessionTerminated},
	}
}

// fakeIPSMGW is the IP-SM-GW of the test. Each MESSAGE it gets is a
// submission or a delivery report; it passes both to the test.
type fakeIPSMGW struct {
	t     *testing.T
	n     testNetwork
	stack *sip.Stack
	// submissions and reports are the MESSAGEs from the UEs.
	submissions, reports chan *sip.Message
	// delivered has the final response to each MESSAGE that the IP-SM-GW
	// sent, with the request.
	delivered chan mtResult
}

type mtResult struct {
	req    *sip.Message
	status int
}

func newFakeIPSMGW(t *testing.T, n testNetwork) *fakeIPSMGW {
	g := &fakeIPSMGW{t: t, n: n, submissions: make(chan *sip.Message, 4), reports: make(chan *sip.Message, 4),
		delivered: make(chan mtResult, 4)}
	g.stack = stack(t, func(st *sip.ServerTransaction) {
		req := st.Request
		// TS 24.229 5.7.2: the IP-SM-GW is the UAS of the MESSAGE, and a
		// relay, so it answers 202 (RFC 3428 7).
		st.Respond(sip.NewResponse(req, 202, sip.NewTag()))
		if req.Get("In-Reply-To") != "" {
			g.reports <- req
			return
		}
		g.submissions <- req
		go g.relay(req)
	})
	n.dns.add("ipsmgw."+domain, g.stack.LocalAddr())
	return g
}

// relay is what the IP-SM-GW and its SC do with a submission: an RP-ACK
// in a submit report to the sender (TS 24.341 5.3.3.4.3), and the
// message as an SMS-DELIVER to the recipient (5.3.3.4.2). The sender's
// number comes from the tel URI in P-Asserted-Identity (5.3.1.2 NOTE 2).
func (g *fakeIPSMGW) relay(submission *sip.Message) {
	rp, err := sms.ParseRP(submission.Body)
	if err != nil {
		g.t.Errorf("submission: %v", err)
		return
	}
	data := rp.(sms.RPDataMSToNetwork)
	tpdu, err := sms.ParseTPDU(data)
	if err != nil {
		g.t.Errorf("submission: %v", err)
		return
	}
	submit := tpdu.(sms.Submit)
	var sender string
	for _, v := range submission.GetAll("P-Asserted-Identity") {
		if na, err := sip.ParseNameAddr(v); err == nil && len(na.URI) > 4 && na.URI[:4] == "tel:" {
			sender = na.URI
		}
	}
	scts := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	report, _ := sms.SubmitReport{ServiceCentreTimeStamp: scts}.Marshal()
	ack, _ := sms.RPAck{Direction: sms.NetworkToMS, MessageReference: data.MessageReference, UserData: report}.Marshal()
	g.send(fromAddressOf(submission), ack, "fork", sip.HeaderField{Name: "In-Reply-To", Value: submission.Get("Call-ID")})

	// The recipient is the tel URI of TP-DA, which the HSS knows as an
	// alias of its SIP URI. The SC stub looks the number up with
	// Sh instead.
	deliver, _ := sms.Deliver{OriginatingAddress: sms.InternationalNumber(sender[len("tel:"):]),
		DataCodingScheme: submit.DataCodingScheme, ServiceCentreTimeStamp: scts, UserData: submit.UserData}.Marshal()
	mt, _ := sms.RPDataNetworkToMS{MessageReference: 200, OriginatorAddress: data.DestinationAddress, UserData: deliver}.Marshal()
	g.send("tel:"+submit.DestinationAddress.String(), mt, "no-fork")
}

func fromAddressOf(m *sip.Message) string {
	na, _ := sip.ParseNameAddr(m.Get("From"))
	return na.URI
}

// send is a MESSAGE that the IP-SM-GW originates (TS 24.229 5.7.3) to
// target, through the I-CSCF, with the header fields of TS 24.341
// 5.3.3.4.2 and 5.3.3.4.3.
func (g *fakeIPSMGW) send(target string, body []byte, disposition string, extra ...sip.HeaderField) {
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: target}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+ipsmgwURI+">;tag="+sip.NewTag())
	m.Add("To", "<"+target+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", "<"+ipsmgwURI+">")
	m.Add("Accept-Contact", "*;+g.3gpp.smsip;require;explicit")
	m.Add("Request-Disposition", disposition)
	for _, f := range extra {
		m.Add(f.Name, f.Value)
	}
	m.Add("Content-Type", ue.SMSContentType)
	m.Body = body
	icscf, _ := g.n.dns.Locate(context.Background(), domain, 0, sip.UDP)
	ct, err := g.stack.SendRequest(m, icscf[0])
	if err != nil {
		g.t.Errorf("MESSAGE to %s: %v", target, err)
		return
	}
	status := 0
	for r := range ct.Responses() {
		status = r.StatusCode
	}
	g.delivered <- mtResult{req: m, status: status}
}

func receive[T any](t *testing.T, c <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s", what)
		var zero T
		return zero
	}
}

// smsNetwork is smsIMS with UE-A registered.
func smsNetwork(t *testing.T) (testNetwork, *fakeIPSMGW, *ue.UE) {
	t.Helper()
	n, g := smsIMS(t)
	a, _ := newSMSUE(t, n.pcscf, impi, impu, password, ue.DefaultFeatures)
	if _, err := a.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return n, g, a
}

// smsIMS is the IMS with both users served by the fake IP-SM-GW, each
// with a tel URI as the alias of its SIP URI.
func smsIMS(t *testing.T) (testNetwork, *fakeIPSMGW) {
	t.Helper()
	n := startNetwork(t, networkConfig{minExpires: time.Second, maxExpires: time.Hour,
		subscriptions: []hss.Subscription{
			{PrivateIdentity: impi, PublicIdentities: []string{impu, telA}, Password: password},
			{PrivateIdentity: impi2, PublicIdentities: []string{impu2, telB}, Password: password2},
		},
		criteria: []ifc.InitialFilterCriteria{moSMSCriterion()},
	})
	return n, newFakeIPSMGW(t, n)
}

// TS 24.341 B.5 and B.6: UE-A submits a short message, the IP-SM-GW
// accepts it and sends the submit report back, and UE-B receives the
// message and sends its delivery report, which its S-CSCF passes to the
// IP-SM-GW by UE-B's own criteria.
func TestShortMessageThroughTheWholeIMS(t *testing.T) {
	n, g, a := smsNetwork(t)
	b, _ := newSMSUE(t, n.pcscf, impi2, impu2, password2, ue.DefaultFeatures)
	if _, err := b.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, text := range []string{"Hello, B! It's A @ the IMS", "こんにちは。IMS の SMS です"} {
		sent, err := a.SendSMS(ctx, ue.OutgoingSMS{To: "+12025550102", Text: text})
		if err != nil {
			t.Fatal(err)
		}

		// The submission reached the IP-SM-GW by UE-A's criteria, with the
		// tel URI that the S-CSCF asserted besides the SIP URI.
		submission := receive(t, g.submissions, "submission")
		if submission.RequestURI != "tel:"+scAddress || submission.Get("Call-ID") != sent.CallID {
			t.Errorf("submission %s with Call-ID %s", submission.RequestURI, submission.Get("Call-ID"))
		}
		if pai := submission.GetAll("P-Asserted-Identity"); !slices.Equal(pai, []string{"<" + impu + ">", "<" + telA + ">"}) {
			t.Errorf("submission P-Asserted-Identity = %v", pai)
		}

		// The submit report reached UE-A, which answered 200.
		for range 2 {
			r := receive(t, g.delivered, "MESSAGE from the IP-SM-GW")
			if r.status != 200 {
				t.Errorf("MESSAGE to %s got %d, want 200", r.req.RequestURI, r.status)
			}
		}
		got := waitSent(t, a, sent.CallID, func(s ue.SentSMS) bool { return s.State == ue.SubmitAccepted })
		if got.ServiceCentreTimeStamp == nil {
			t.Errorf("accepted without TP-SCTS: %+v", got)
		}

		// UE-B got the text from UE-A's number.
		h, err := b.WaitSMS(ctx, func(h ue.SMSHistory) bool { return len(h.Received) == i+1 })
		if err != nil {
			t.Fatalf("UE-B received %+v: %v", h.Received, err)
		}
		if r := h.Received[i]; r.Text != text || r.From != "+12025550101" {
			t.Errorf("UE-B received %q from %s, want %q from +12025550101", r.Text, r.From, text)
		}

		// The delivery report of UE-B came back to the IP-SM-GW, by UE-B's
		// originating criteria.
		report := receive(t, g.reports, "delivery report")
		if report.RequestURI != ipsmgwURI || report.Get("In-Reply-To") != h.Received[i].CallID {
			t.Errorf("delivery report to %s, In-Reply-To %q; want the IP-SM-GW and %q",
				report.RequestURI, report.Get("In-Reply-To"), h.Received[i].CallID)
		}
		if pai := report.Get("P-Asserted-Identity"); pai != "<"+impu2+">" {
			t.Errorf("delivery report P-Asserted-Identity = %q, want UE-B", pai)
		}
		rp, err := sms.ParseRP(report.Body)
		if ack, ok := rp.(sms.RPAck); err != nil || !ok || ack.Direction != sms.MSToNetwork || ack.MessageReference != 200 {
			t.Errorf("delivery report body %+v (%v), want RP-ACK from the MS for RP-MR 200", rp, err)
		}
	}
}

// TS 24.341 5.3.3.4.2 and RFC 3841 7.2.4: a recipient whose contact did
// not register +g.3gpp.smsip, here a UE that registered only the MMTel
// service, is not offered the short message: its S-CSCF answers 480,
// and UE-B gets nothing.
func TestShortMessageToAUEWithoutSMSOverIP(t *testing.T) {
	n, g, a := smsNetwork(t)
	mmtel := sip.ParseParams(`+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`)
	b, _ := newSMSUE(t, n.pcscf, impi2, impu2, password2, mmtel)
	if _, err := b.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SendSMS(context.Background(), ue.OutgoingSMS{To: "+12025550102", Text: "Anyone there?"}); err != nil {
		t.Fatal(err)
	}
	receive(t, g.submissions, "submission")
	results := map[string]int{}
	for range 2 {
		r := receive(t, g.delivered, "MESSAGE from the IP-SM-GW")
		results[r.req.RequestURI] = r.status
	}
	if results[impu] != 200 || results[telB] != 480 {
		t.Errorf("submit report got %d, short message got %d; want 200 and 480", results[impu], results[telB])
	}
	if h := b.SMS(); len(h.Received) != 0 {
		t.Errorf("UE-B received %+v", h.Received)
	}
}

// The control API sends a short message and lists what each phone sent
// and received.
func TestPhoneSendsAndListsShortMessages(t *testing.T) {
	n, g := smsIMS(t)
	a := newPhone(t, n.pcscf, impi, impu, password, autoAnswer)
	b := newPhone(t, n.pcscf, impi2, impu2, password2, autoAnswer)
	aEvents, bEvents := a.events(), b.events()

	code, body := a.do("POST", "/sms", `{"to": "+12025550102", "text": "Hi B", "status_report": true}`)
	var sent ue.SentSMS
	if err := json.Unmarshal([]byte(body), &sent); code != http.StatusOK || err != nil {
		t.Fatalf("POST /sms: %d %s", code, body)
	}
	if sent.State != ue.Submitted || sent.To != "+12025550102" || !sent.StatusReportRequested || sent.CallID == "" {
		t.Errorf("POST /sms answered %s", body)
	}
	receive(t, g.submissions, "submission")
	receive(t, g.reports, "delivery report")

	history := func(p *phone, done func(ue.SMSHistory) bool) ue.SMSHistory {
		t.Helper()
		var h ue.SMSHistory
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			code, body := p.do("GET", "/sms", "")
			if err := json.Unmarshal([]byte(body), &h); code != http.StatusOK || err != nil {
				t.Fatalf("GET /sms: %d %s", code, body)
			}
			if done(h) {
				return h
			}
		}
		t.Fatalf("GET /sms: %+v", h)
		return h
	}
	history(a, func(h ue.SMSHistory) bool {
		return len(h.Sent) == 1 && h.Sent[0].CallID == sent.CallID && h.Sent[0].State == ue.SubmitAccepted
	})
	history(b, func(h ue.SMSHistory) bool {
		return len(h.Received) == 1 && h.Received[0].Text == "Hi B" && h.Received[0].From == "+12025550101"
	})

	// /events carries the same history as it changes, as the Lab sees
	// it when it follows the phone.
	nextEvent(t, aEvents, "the short message accepted", func(s statusJSON) bool {
		return len(s.SMS.Sent) == 1 && s.SMS.Sent[0].CallID == sent.CallID && s.SMS.Sent[0].State == ue.SubmitAccepted
	})
	nextEvent(t, bEvents, "the short message received", func(s statusJSON) bool {
		return len(s.SMS.Received) == 1 && s.SMS.Received[0].Text == "Hi B"
	})

	for _, tt := range []struct{ body, why string }{
		{`{"to": "12025550102", "text": "Hi"}`, "a number without +"},
		{`{"to": "+12025550102", "text": "` + strings.Repeat("a", 161) + `"}`, "a text too long"},
		{`{"to": "+12025550102", "txt": "Hi"}`, "an unknown field"},
	} {
		if code, body := a.do("POST", "/sms", tt.body); code != http.StatusBadRequest {
			t.Errorf("POST /sms with %s: %d %s, want 400", tt.why, code, body)
		}
	}
}
