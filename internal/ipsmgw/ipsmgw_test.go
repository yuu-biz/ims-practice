package ipsmgw_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

const (
	domain   = "ims.mnc001.mcc001.3gppnetwork.org"
	ownURI   = "sip:ipsmgw." + domain + ":5060"
	alice    = "sip:+12025550101@" + domain
	aliceTel = "tel:+12025550101"
	bobTel   = "tel:+12025550102"
	number   = "12025550199"
	// aliceIMSI is the IMSI the HSS has for alice, which a TFR names her
	// by (TS 29.338 Table 6.2.2.1/1).
	aliceIMSI = "001010000000001"
	// scscfHost is the host of the S-CSCF in the NOTIFYs of the tests.
	scscfHost = "scscf." + domain
	// ibcfHost is the IBCF of the home network (interconnect_test.go).
	ibcfHost = "ibcf." + domain
	// gwDiameterHost and scDiameterHost are the Diameter identities of
	// the IP-SM-GW and of the SC it speaks SGd with (sgd_test.go).
	gwDiameterHost = "ipsmgw." + domain
	scDiameterHost = "sc." + domain
)

var scAddress = sms.InternationalNumber("+12025550100")

// quiet discards logs; IPSMGW_TEST_LOG=1 shows them.
func quiet() *slog.Logger {
	if os.Getenv("IPSMGW_TEST_LOG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type submitted struct {
	originator sms.Address
	submit     sms.Submit
}

type reported struct {
	reference string
	outcome   ipsmgw.Outcome
}

// fakeSC records what the IP-SM-GW hands the SC, and answers each
// Submit with what answer gives. A test may change answer while the
// IP-SM-GW runs; the lock makes that visible to it.
type fakeSC struct {
	submits chan submitted
	reports chan reported
	mu      sync.Mutex
	answer  func() (*sms.SubmitReport, error)
}

func (f *fakeSC) Submit(_ context.Context, originator sms.Address, submit sms.Submit) (*sms.SubmitReport, error) {
	f.submits <- submitted{originator, submit}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answer()
}

func (f *fakeSC) answerWith(report sms.SubmitReport, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = func() (*sms.SubmitReport, error) { return &report, err }
}

func (f *fakeSC) Report(_ context.Context, reference string, outcome ipsmgw.Outcome) {
	f.reports <- reported{reference, outcome}
}

type update struct {
	id     sh.UserIdentity
	number string
}

type fakeHSS struct {
	updates chan update
	// notifications are the ReadyForSM notifications of TS 23.204 6.5b
	// and 6.6, and notifyFails makes each of them fail.
	notifications chan notification
	notifyFails   atomic.Pointer[error]
	// imsi is what the Sh read of the IMSI answers, by public identity.
	// A user that is not there has no IMSI, as the HSS answers for a
	// private identity that is not derived from one.
	imsi map[string]string
	// onUpdate, where set, runs as each PUR reaches the HSS, before it
	// is answered, and updateFails makes each PUR fail after it.
	onUpdate    atomic.Pointer[func()]
	updateFails atomic.Pointer[error]
}

func (f *fakeHSS) IMSI(_ context.Context, publicID string) (string, error) {
	return f.imsi[publicID], nil
}

func (f *fakeHSS) UpdateSMSRegistrationInfo(_ context.Context, id sh.UserIdentity, ipsmgwNumber string) error {
	if hook := f.onUpdate.Load(); hook != nil {
		(*hook)()
	}
	f.updates <- update{id, ipsmgwNumber}
	if err := f.updateFails.Load(); err != nil {
		return *err
	}
	return nil
}

func (f *fakeHSS) NotifyReadyForSM(_ context.Context, id sh.UserIdentity, ipsmgwNumber string, reason sh.ReadyForSM) error {
	if err := f.notifyFails.Load(); err != nil {
		return *err
	}
	f.notifications <- notification{id, ipsmgwNumber, reason}
	return nil
}

// notification is a ReadyForSM that the IP-SM-GW sent the HSS.
type notification struct {
	id           sh.UserIdentity
	ipsmgwNumber string
	reason       sh.ReadyForSM
}

// locator stands in for DNS. It is filled before any node looks a name
// up.
type locator map[string]netip.AddrPort

func (l locator) Locate(_ context.Context, host string, _ int, _ sip.Transport) ([]sip.Destination, error) {
	if a, ok := l[host]; ok {
		return []sip.Destination{{Transport: sip.UDP, Addr: a}}, nil
	}
	return nil, errors.New("no such host: " + host)
}

// fastTimers make a MESSAGE that no one answers time out in 320 ms
// (Timer F is 64*T1).
var fastTimers = sip.Timers{T1: 5 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}

func newStack(t *testing.T, timers sip.Timers, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	return newStackConfig(t, sip.Config{Timers: timers, OnRequest: onRequest})
}

func newStackConfig(t *testing.T, cfg sip.Config) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	cfg.SentBy = tr.LocalAddr().String()
	s, err := sip.NewStack(tr, cfg, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// harness is one IP-SM-GW with fakes around it. The test sends requests
// to it as the S-CSCF would, and the entry point of the home network,
// where the IP-SM-GW's own MESSAGEs go, hands them to the test and
// answers each with the status that answer gives for it (0: no answer).
type harness struct {
	t        *testing.T
	g        *ipsmgw.IPSMGW
	sc       *fakeSC
	hss      *fakeHSS
	gw       netip.AddrPort
	scscf    *sip.Stack
	received chan *sip.Message
	// atSCSCF gets the requests that reach the S-CSCF, which answers each
	// with 200: the IP-SM-GW sends it only the requests within a dialog
	// that a NOTIFY from it set up.
	atSCSCF chan *sip.Message
}

func newHarness(t *testing.T, store ipsmgw.Store, reportTimeout time.Duration, answer func(*sip.Message) int) *harness {
	t.Helper()
	return newHarnessWith(t, store, reportTimeout, answer, ipsmgw.Config{})
}

// newHarnessWith is newHarness for an IP-SM-GW with the IBCF and the
// lock per user of extra. The IBCF is the entry point too, as far as
// the test sees it: a MESSAGE for it carries the IBCF in Route.
func newHarnessWith(t *testing.T, store ipsmgw.Store, reportTimeout time.Duration, answer func(*sip.Message) int, extra ipsmgw.Config) *harness {
	t.Helper()
	h := &harness{
		t:  t,
		sc: &fakeSC{submits: make(chan submitted, 10), reports: make(chan reported, 10)},
		hss: &fakeHSS{updates: make(chan update, 10), notifications: make(chan notification, 10),
			imsi: map[string]string{alice: aliceIMSI}},
		received: make(chan *sip.Message, 10),
		atSCSCF:  make(chan *sip.Message, 10),
	}
	h.scscf = newStack(t, sip.Timers{}, func(st *sip.ServerTransaction) {
		h.atSCSCF <- st.Request
		st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag()))
	})
	entry := newStack(t, sip.Timers{}, func(st *sip.ServerTransaction) {
		h.received <- st.Request
		if code := answer(st.Request); code != 0 {
			st.Respond(sip.NewResponse(st.Request, code, sip.NewTag()))
		}
	})
	h.sc.answerWith(sms.SubmitReport{ServiceCentreTimeStamp: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)}, nil)
	h.g = ipsmgw.New(ipsmgw.Config{URI: ownURI, HomeDomain: domain, Number: number, ServiceCentre: scAddress, ReportTimeout: reportTimeout,
		Diameter: diameter.Identity{OriginHost: gwDiameterHost, OriginRealm: domain},
		IBCF:     extra.IBCF, UserLocks: extra.UserLocks,
		// The sweep comes round often, so that a test which shortens
		// TR1N does not wait a second for the delivery it ends, and ends
		// it while the Store still keeps the delivery.
		SweepInterval: 20 * time.Millisecond},
		h.sc, store, h.hss, locator{domain: entry.LocalAddr(), scscfHost: h.scscf.LocalAddr(), ibcfHost: entry.LocalAddr()}, quiet())
	t.Cleanup(h.g.Close)
	stack := newStack(t, fastTimers, h.g.OnRequest)
	h.g.SetStack(stack)
	h.gw = stack.LocalAddr()
	return h
}

func answerOK(*sip.Message) int { return 200 }

// send sends req to the IP-SM-GW and returns its final response.
func (h *harness) send(req *sip.Message) *sip.Message {
	h.t.Helper()
	return sendTo(h.t, h.scscf, h.gw, req)
}

func sendTo(t *testing.T, from *sip.Stack, to netip.AddrPort, req *sip.Message) *sip.Message {
	t.Helper()
	ct, err := from.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: to})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		t.Fatalf("no final response to %s: %v", req.Method, ct.Err())
	}
	return final
}

// next is the next request that reached the entry point.
func (h *harness) next() *sip.Message {
	h.t.Helper()
	select {
	case m := <-h.received:
		return m
	case <-time.After(5 * time.Second):
		h.t.Fatal("no request reached the entry point")
		return nil
	}
}

// nextNotification is the next ReadyForSM that reached the HSS.
func (h *harness) nextNotification() notification {
	h.t.Helper()
	select {
	case n := <-h.hss.notifications:
		return n
	case <-time.After(5 * time.Second):
		h.t.Fatal("the HSS heard of no notification")
		return notification{}
	}
}

func (h *harness) nextReport() reported {
	h.t.Helper()
	select {
	case r := <-h.sc.reports:
		return r
	case <-time.After(5 * time.Second):
		h.t.Fatal("the SC heard of no outcome")
		return reported{}
	}
}

// message is a MESSAGE as the S-CSCF passes it on from Alice: with her
// SIP URI and its tel URI alias in P-Asserted-Identity (TS 24.229
// 5.4.3.2 step 9).
func message(body []byte) *sip.Message {
	m := &sip.Message{Method: sip.MESSAGE, RequestURI: "tel:+12025550100"}
	m.Add("Max-Forwards", "69")
	m.Add("From", "<"+alice+">;tag="+sip.NewTag())
	m.Add("To", "<tel:+12025550100>")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 MESSAGE")
	m.Add("P-Asserted-Identity", "<"+alice+">")
	m.Add("P-Asserted-Identity", "<"+aliceTel+">")
	m.Add("Content-Type", ipsmgw.ContentType)
	m.Body = body
	return m
}

func marshal(t *testing.T, m interface{ Marshal() ([]byte, error) }) []byte {
	t.Helper()
	b, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var hello = sms.Submit{
	StatusReportRequest: true,
	MessageReference:    42,
	DestinationAddress:  sms.InternationalNumber("+12025550102"),
	UserData:            sms.UserData{Length: 5, Data: []byte{0xe8, 0x32, 0x9b, 0xfd, 0x06}}, // "hello"
}

func submitBody(t *testing.T, mr uint8, da sms.Address, s sms.Submit) []byte {
	t.Helper()
	return marshal(t, sms.RPDataMSToNetwork{MessageReference: mr, DestinationAddress: da, UserData: marshal(t, s)})
}

// header checks the header fields of a MESSAGE of the IP-SM-GW's own.
func checkHeaders(t *testing.T, m *sip.Message, want map[string]string) {
	t.Helper()
	for name, value := range want {
		if got := m.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func parseRP(t *testing.T, m *sip.Message) sms.RPMessage {
	t.Helper()
	rp, err := sms.ParseRP(m.Body)
	if err != nil {
		t.Fatal(err)
	}
	return rp
}

// TestShortMessageGoesToTheSC is TS 24.341 5.3.3.4.1 and 5.3.3.4.3: a
// 202, the SMS-SUBMIT to the SC with the sender's number from the tel
// URI, and the SC's report back to the sender in a MESSAGE of its own.
func TestShortMessageGoesToTheSC(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	mo := message(submitBody(t, 7, scAddress, hello))
	if resp := h.send(mo); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	got := <-h.sc.submits
	if got.originator != sms.InternationalNumber("+12025550101") || !reflect.DeepEqual(got.submit, hello) {
		t.Errorf("SC got %+v", got)
	}

	report := h.next()
	if report.Method != sip.MESSAGE || report.RequestURI != alice {
		t.Errorf("submit report is %s %s, want MESSAGE to %s", report.Method, report.RequestURI, alice)
	}
	checkHeaders(t, report, map[string]string{
		"To":                  "<" + alice + ">",
		"P-Asserted-Identity": "<" + ownURI + ">",
		"Accept-Contact":      "*;+g.3gpp.smsip;require;explicit",
		"Request-Disposition": "fork",
		"In-Reply-To":         mo.Get("Call-ID"),
		"Content-Type":        ipsmgw.ContentType,
		"Route":               "<sip:" + domain + ";lr>",
	})
	ack, ok := parseRP(t, report).(sms.RPAck)
	if !ok || ack.Direction != sms.NetworkToMS || ack.MessageReference != 7 {
		t.Fatalf("submit report body = %+v, want RP-ACK with RP-MR 7", parseRP(t, report))
	}
	if tpdu, err := sms.ParseTPDU(ack); err != nil || tpdu.(sms.SubmitReport).ServiceCentreTimeStamp.IsZero() {
		t.Errorf("SMS-SUBMIT-REPORT = %+v, %v", tpdu, err)
	}
}

// TestShortMessageAcceptedWithoutASubmitReport is TS 24.011 table 7.7:
// the SMS-SUBMIT-REPORT is an optional element of the RP-ACK, so an SC
// that gives none, as an OFA without SM-RP-UI does, still gets the
// sender an RP-ACK.
func TestShortMessageAcceptedWithoutASubmitReport(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.sc.mu.Lock()
	h.sc.answer = func() (*sms.SubmitReport, error) { return nil, nil }
	h.sc.mu.Unlock()
	mo := message(submitBody(t, 7, scAddress, hello))
	if resp := h.send(mo); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	<-h.sc.submits
	report := h.next()
	ack, ok := parseRP(t, report).(sms.RPAck)
	if !ok || ack.MessageReference != 7 || ack.UserData != nil {
		t.Fatalf("submit report body = %+v, want an RP-ACK with RP-MR 7 and no RP-User Data", parseRP(t, report))
	}
}

// TestChargingVector is TS 24.229 5.7.2 and 5.7.3: the answer to a
// request repeats its icid-value and orig-ioi and adds this network's
// term-ioi; a MESSAGE of the IP-SM-GW's own starts a charging
// identifier of its own, with an orig-ioi and no term-ioi.
func TestChargingVector(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	mo := message(submitBody(t, 7, scAddress, hello))
	mo.Add("P-Charging-Vector", "icid-value=MO-ICID;orig-ioi="+domain)
	resp := h.send(mo)
	if got, want := resp.Get("P-Charging-Vector"), "icid-value=MO-ICID;orig-ioi="+domain+";term-ioi="+domain; got != want {
		t.Errorf("202 P-Charging-Vector = %q, want %q", got, want)
	}

	report := h.next()
	icid, rest, _ := strings.Cut(report.Get("P-Charging-Vector"), ";")
	if !strings.HasPrefix(icid, "icid-value=") || icid == "icid-value=" || icid == "icid-value=MO-ICID" || rest != "orig-ioi="+domain {
		t.Errorf("submit report P-Charging-Vector = %q, want a new icid-value and orig-ioi=%s alone", report.Get("P-Charging-Vector"), domain)
	}
}

// TestShortMessageRefused covers the RP-ERROR of the submit report: the
// cause the SC or the IP-SM-GW gives, with the SMS-SUBMIT-REPORT of the
// SC when there is one.
func TestShortMessageRefused(t *testing.T) {
	refused := &ipsmgw.Refusal{Cause: sms.RPCauseUnassignedNumber,
		Report: &sms.SubmitReport{FailureCause: sms.FCSInvalidSMEAddress, ServiceCentreTimeStamp: time.Now()}}
	tests := []struct {
		name     string
		body     func(t *testing.T) []byte
		noTel    bool
		scResult error
		wantMR   uint8
		want     sms.RPCause
		wantFCS  sms.FailureCause // 0: no SMS-SUBMIT-REPORT
		reachSC  bool
	}{
		{name: "SC refuses", body: func(t *testing.T) []byte { return submitBody(t, 1, scAddress, hello) },
			scResult: refused, wantMR: 1, want: sms.RPCauseUnassignedNumber, wantFCS: sms.FCSInvalidSMEAddress, reachSC: true},
		// TS 24.011 table 7.8: the RP-ERROR may go without a report.
		{name: "SC refuses without a report", body: func(t *testing.T) []byte { return submitBody(t, 1, scAddress, hello) },
			scResult: &ipsmgw.Refusal{Cause: sms.RPCauseCongestion}, wantMR: 1, want: sms.RPCauseCongestion, reachSC: true},
		{name: "SC does not answer", body: func(t *testing.T) []byte { return submitBody(t, 2, scAddress, hello) },
			scResult: errors.New("HSS down"), wantMR: 2, want: sms.RPCauseTemporaryFailure, reachSC: true},
		{name: "another SC", body: func(t *testing.T) []byte { return submitBody(t, 3, sms.InternationalNumber("+12025550177"), hello) },
			wantMR: 3, want: sms.RPCauseShortMessageTransferRejected},
		{name: "SC address in a private plan", body: func(t *testing.T) []byte {
			return submitBody(t, 4, sms.Address{TypeOfNumber: sms.TONInternational, NumberingPlan: sms.NPIPrivate, Digits: "12025550100"}, hello)
		}, wantMR: 4, want: sms.RPCauseInvalidMandatoryInformation},
		{name: "RP-DATA that does not decode", body: func(*testing.T) []byte { return []byte{0x00, 0x05, 0x09} },
			wantMR: 5, want: sms.RPCauseInvalidMandatoryInformation},
		{name: "reserved RP-MTI", body: func(*testing.T) []byte { return []byte{0x07, 0x06} },
			wantMR: 6, want: sms.RPCauseMessageTypeNonExistent},
		{name: "SMS-SUBMIT that does not decode", body: func(t *testing.T) []byte {
			return marshal(t, sms.RPDataMSToNetwork{MessageReference: 8, DestinationAddress: scAddress, UserData: []byte{0x01}})
		}, wantMR: 8, want: sms.RPCauseInvalidMandatoryInformation},
		{name: "TP-UD beyond one short message", body: func(t *testing.T) []byte {
			// SMS-SUBMIT in UCS2 with 200 octets of TP-UD, which fits in
			// RP-User-Data; TS 23.040 9.2.2.2 allows 140.
			tpdu := append([]byte{0x01, 0x00, 0x0B, 0x91, 0x21, 0x20, 0x55, 0x05, 0x01, 0xF2, 0x00, 0x08, 200}, make([]byte, 200)...)
			return marshal(t, sms.RPDataMSToNetwork{MessageReference: 11, DestinationAddress: scAddress, UserData: tpdu})
		}, wantMR: 11, want: sms.RPCauseInvalidMandatoryInformation},
		{name: "TP-UDL 255", body: func(t *testing.T) []byte {
			// TP-UDL 255 in the 7 bit alphabet with the 224 octets that
			// 255 septets need: a TPDU of 237 octets, more than
			// RP-User-Data holds (TS 24.011 8.2.5.3).
			tpdu := append([]byte{0x01, 0x00, 0x0B, 0x91, 0x21, 0x20, 0x55, 0x05, 0x01, 0xF2, 0x00, 0x00, 0xFF}, make([]byte, 224)...)
			rp := []byte{0x00, 12, 0x00, 0x07, 0x91, 0x21, 0x20, 0x55, 0x05, 0x01, 0xF0, byte(len(tpdu))}
			return append(rp, tpdu...)
		}, wantMR: 12, want: sms.RPCauseInvalidMandatoryInformation},
		{name: "RP-DATA for the MS", body: func(t *testing.T) []byte {
			return marshal(t, sms.RPDataNetworkToMS{MessageReference: 9, OriginatorAddress: scAddress, UserData: []byte{0x00}})
		}, wantMR: 9, want: sms.RPCauseMessageNotCompatibleWithState},
		{name: "sender without a tel URI", body: func(t *testing.T) []byte { return submitBody(t, 10, scAddress, hello) },
			noTel: true, wantMR: 10, want: sms.RPCauseRequestedFacilityNotSubscribed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
			h.sc.answerWith(sms.SubmitReport{}, tt.scResult)
			mo := message(tt.body(t))
			if tt.noTel {
				mo.Remove("P-Asserted-Identity")
				mo.Add("P-Asserted-Identity", "<"+alice+">")
			}
			if resp := h.send(mo); resp.StatusCode != 202 {
				t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
			}
			report := h.next()
			if report.RequestURI != alice || report.Get("In-Reply-To") != mo.Get("Call-ID") {
				t.Errorf("RP-ERROR went to %s in reply to %q", report.RequestURI, report.Get("In-Reply-To"))
			}
			e, ok := parseRP(t, report).(sms.RPError)
			if !ok || e.Direction != sms.NetworkToMS || e.MessageReference != tt.wantMR || e.Cause != tt.want {
				t.Fatalf("body = %+v, want RP-ERROR with RP-MR %d and cause %d", parseRP(t, report), tt.wantMR, tt.want)
			}
			tpdu, err := sms.ParseTPDU(e)
			switch {
			case tt.wantFCS == 0 && !errors.Is(err, sms.ErrNoUserData):
				t.Errorf("RP-ERROR carries %+v, want no TPDU", tpdu)
			case tt.wantFCS != 0 && (err != nil || tpdu.(sms.SubmitReport).FailureCause != tt.wantFCS):
				t.Errorf("SMS-SUBMIT-REPORT = %+v, %v, want TP-FCS %#x", tpdu, err, tt.wantFCS)
			}
			if reached := len(h.sc.submits) > 0; reached != tt.reachSC {
				t.Errorf("SC reached: %v, want %v", reached, tt.reachSC)
			}
		})
	}
}

// TestMemoryAvailableGoesToTheHSS is the RP-SMMA of TS 24.341 5.3.3.4.1
// steps d and e with TS 23.204 6.6: a 202, the notification to the HSS
// by the MSISDN of the user, and then an RP-ACK with the same
// reference. The HSS hears the IP-SM-GW's own number, which it compares
// with the registration it holds.
func TestMemoryAvailableGoesToTheHSS(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.registered()
	mo := message(marshal(t, sms.RPSMMA{MessageReference: 3}))
	if resp := h.send(mo); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	want := notification{sh.UserIdentity{MSISDN: "12025550101"}, number, sh.MemoryAvailable}
	if got := h.nextNotification(); got != want {
		t.Errorf("notification = %+v, want %+v", got, want)
	}
	ack := h.next()
	if got := parseRP(t, ack); !reflect.DeepEqual(got, sms.RPAck{Direction: sms.NetworkToMS, MessageReference: 3}) {
		t.Errorf("answer = %+v, want RP-ACK with RP-MR 3", got)
	}
	if ack.Get("In-Reply-To") != mo.Get("Call-ID") {
		t.Errorf("In-Reply-To = %q", ack.Get("In-Reply-To"))
	}
}

// TestMemoryAvailableWithoutARegistration: the IP-SM-GW holds no
// subscription for the user, so it has no MSISDN and names the user by
// the public identity, which Sh takes as well (TS 29.328 Table 7.6.1).
func TestMemoryAvailableWithoutARegistration(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.send(message(marshal(t, sms.RPSMMA{MessageReference: 1})))
	want := notification{sh.UserIdentity{PublicIdentity: alice}, number, sh.MemoryAvailable}
	if got := h.nextNotification(); got != want {
		t.Errorf("notification = %+v, want %+v", got, want)
	}
}

// TestMemoryAvailableThatTheHSSRefuses: the UE hears RP-Cause 41, a
// temporary failure, rather than an RP-ACK for a notification that did
// not get through (TS 24.011 Table 8.4 part 1).
func TestMemoryAvailableThatTheHSSRefuses(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	refused := errors.New("the HSS refused the notification")
	h.hss.notifyFails.Store(&refused)
	h.send(message(marshal(t, sms.RPSMMA{MessageReference: 4})))
	want := sms.RPError{Direction: sms.NetworkToMS, MessageReference: 4, Cause: sms.RPCauseTemporaryFailure}
	if got := parseRP(t, h.next()); !reflect.DeepEqual(got, want) {
		t.Errorf("answer = %+v, want %+v", got, want)
	}
}

func TestMessageWithoutAShortMessageIsRefused(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	m := message([]byte("hello"))
	m.Set("Content-Type", "text/plain")
	if resp := h.send(m); resp.StatusCode != 415 {
		t.Errorf("text/plain answered %d, want 415", resp.StatusCode)
	}
	m = message(marshal(t, sms.RPSMMA{}))
	m.Remove("P-Asserted-Identity")
	if resp := h.send(m); resp.StatusCode != 403 {
		t.Errorf("MESSAGE without P-Asserted-Identity answered %d, want 403", resp.StatusCode)
	}
}
