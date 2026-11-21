package sc_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sc"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

const (
	domain   = "ims.mnc001.mcc001.3gppnetwork.org"
	aliceTel = "tel:+12025550101"
	bob      = "sip:001010000000002@" + domain
	bobTel   = "tel:+12025550102"
)

var (
	alice     = sms.InternationalNumber("+12025550101")
	bobNumber = sms.InternationalNumber("+12025550102")
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// users answers PublicIdentities from a table; a number that is not in
// it is unknown to the HSS.
type users map[string][]string

func (u users) PublicIdentities(_ context.Context, msisdn string) ([]string, error) {
	if ids, ok := u[msisdn]; ok {
		return ids, nil
	}
	return nil, &sh.ResultError{Command: "UDA", Result: cx.Experimental(cx.DiameterErrorUserUnknown)}
}

type failingUsers struct{}

func (failingUsers) PublicIdentities(context.Context, string) ([]string, error) {
	return nil, errors.New("no answer from the HSS")
}

// fakeIPSMGW hands the test every delivery the SC asks for.
type fakeIPSMGW chan ipsmgw.Delivery

func (f fakeIPSMGW) Deliver(_ context.Context, d ipsmgw.Delivery) { f <- d }

func (f fakeIPSMGW) next(t *testing.T) ipsmgw.Delivery {
	t.Helper()
	select {
	case d := <-f:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the SC delivered nothing")
		return ipsmgw.Delivery{}
	}
}

var everyone = users{
	"12025550101": {"sip:001010000000001@" + domain, aliceTel},
	"12025550102": {bob, bobTel},
}

func newSC(u sc.Users, store sc.Store) (*sc.ServiceCentre, fakeIPSMGW) {
	c := sc.New(u, store, quiet())
	g := make(fakeIPSMGW, 10)
	c.SetIPSMGW(g)
	return c, g
}

var hello = sms.Submit{
	MessageReference:   42,
	DestinationAddress: bobNumber,
	DataCodingScheme:   sms.DCSGSM7,
	UserData:           sms.UserData{Length: 5, Data: []byte{0xe8, 0x32, 0x9b, 0xfd, 0x06}},
}

// TestSubmitDelivers: the SC accepts a short message for a user it can
// find, and asks the IP-SM-GW to deliver an SMS-DELIVER with the same
// text to the user's tel URI (TS 23.204 6.4 step 6).
func TestSubmitDelivers(t *testing.T) {
	c, g := newSC(everyone, sc.NewMemoryStore())
	report, err := c.Submit(context.Background(), alice, hello)
	if err != nil || report.FailureCause != 0 || report.ServiceCentreTimeStamp.IsZero() {
		t.Fatalf("Submit = %+v, %v", report, err)
	}
	d := g.next(t)
	want := sms.Deliver{OriginatingAddress: alice, DataCodingScheme: sms.DCSGSM7,
		ServiceCentreTimeStamp: report.ServiceCentreTimeStamp, UserData: hello.UserData}
	if d.Recipient != bobTel || !reflect.DeepEqual(d.TPDU, want) || d.Reference == "" {
		t.Errorf("delivery = %+v, want %+v to %s", d, want, bobTel)
	}
}

// TestSubmitRefused: a TP-DA that names no user gets RP-Cause 1 and
// TP-FCS C3; an HSS that does not answer, RP-Cause 41 and TP-FCS C2. A
// TP-DA that is no MSISDN gets cause 1 without the HSS being asked,
// which the HSS that never answers shows.
func TestSubmitRefused(t *testing.T) {
	tests := []struct {
		name  string
		users sc.Users
		da    sms.Address
		cause sms.RPCause
		fcs   sms.FailureCause
	}{
		{"no such number", everyone, sms.InternationalNumber("+12025550188"), sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"national number", everyone, sms.Address{TypeOfNumber: sms.TONNational, NumberingPlan: sms.NPIISDNTelephone, Digits: "2025550102"},
			sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"user without identities", users{"12025550102": nil}, bobNumber, sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"digit that is not decimal", failingUsers{}, sms.InternationalNumber("+1202555*102"), sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"16 digits", failingUsers{}, sms.InternationalNumber("+1202555010212345"), sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"no digits", failingUsers{}, sms.InternationalNumber("+"), sms.RPCauseUnassignedNumber, sms.FCSInvalidSMEAddress},
		{"HSS does not answer", failingUsers{}, bobNumber, sms.RPCauseTemporaryFailure, sms.FCSSCSystemFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, g := newSC(tt.users, sc.NewMemoryStore())
			submit := hello
			submit.DestinationAddress = tt.da
			_, err := c.Submit(context.Background(), alice, submit)
			var refusal *ipsmgw.Refusal
			if !errors.As(err, &refusal) || refusal.Cause != tt.cause || refusal.Report.FailureCause != tt.fcs {
				t.Fatalf("err = %v, want RP-Cause %d with TP-FCS %#x", err, tt.cause, tt.fcs)
			}
			if refusal.Report.ServiceCentreTimeStamp.IsZero() {
				t.Error("SMS-SUBMIT-REPORT without TP-SCTS")
			}
			if len(g) != 0 {
				t.Errorf("refused message delivered: %+v", <-g)
			}
		})
	}
}

// TestStatusReport is TS 23.040 9.2.2.3 and 9.2.3.15: when the sender
// asked for it, the outcome of the delivery goes back to the sender's
// tel URI in an SMS-STATUS-REPORT. The stub tries no delivery twice, so
// a failure is always one after which the SC makes no more attempts.
func TestStatusReport(t *testing.T) {
	tests := []struct {
		outcome ipsmgw.Outcome
		want    sms.Status
	}{
		{ipsmgw.Delivered, sms.StatusReceivedBySME},
		{ipsmgw.AbsentSubscriber, sms.StatusNoResponseFromSME},
		{ipsmgw.SubscriberBusy, sms.StatusNoResponseFromSME},
		{ipsmgw.SystemFailure, sms.StatusNoResponseFromSME},
		{ipsmgw.MemoryCapacityExceeded, sms.StatusNoResponseFromSME},
		{ipsmgw.UnidentifiedSubscriber, sms.StatusNotObtainable},
		{ipsmgw.IllegalSubscriber, sms.StatusNotObtainable},
		{ipsmgw.ProtocolError, sms.StatusNotObtainable},
		// TS 23.040 9.2.3.15: forwarded, but the SC cannot confirm it.
		{ipsmgw.Forwarded, sms.Status(0b000_0001)},
	}
	for _, tt := range tests {
		t.Run(tt.outcome.String(), func(t *testing.T) {
			c, g := newSC(everyone, sc.NewMemoryStore())
			submit := hello
			submit.StatusReportRequest = true
			report, err := c.Submit(context.Background(), alice, submit)
			if err != nil {
				t.Fatal(err)
			}
			d := g.next(t)
			if !d.TPDU.(sms.Deliver).StatusReportIndication {
				t.Error("SMS-DELIVER without TP-SRI")
			}

			c.Report(context.Background(), d.Reference, tt.outcome)
			sr := g.next(t)
			got, ok := sr.TPDU.(sms.StatusReport)
			if sr.Recipient != aliceTel || !ok || sr.Reference == d.Reference {
				t.Fatalf("status report delivery = %+v", sr)
			}
			if got.MessageReference != 42 || got.RecipientAddress != bobNumber || got.Status != tt.want ||
				!got.ServiceCentreTimeStamp.Equal(report.ServiceCentreTimeStamp) || got.DischargeTime.IsZero() {
				t.Errorf("SMS-STATUS-REPORT = %+v, want TP-MR 42, TP-RA %v, TP-ST %#x", got, bobNumber, tt.want)
			}

			// The status report is delivered like any short message, but
			// its own outcome ends there.
			c.Report(context.Background(), sr.Reference, ipsmgw.Delivered)
			c.Report(context.Background(), d.Reference, tt.outcome)
			if len(g) != 0 {
				t.Errorf("a second report went out: %+v", <-g)
			}
		})
	}
}

func TestNoStatusReportWithoutTheRequest(t *testing.T) {
	c, g := newSC(everyone, sc.NewMemoryStore())
	if _, err := c.Submit(context.Background(), alice, hello); err != nil {
		t.Fatal(err)
	}
	c.Report(context.Background(), g.next(t).Reference, ipsmgw.Delivered)
	if len(g) != 0 {
		t.Errorf("status report without TP-SRR: %+v", <-g)
	}
}

// TestStatusReportFromAnotherSC: the outcome reaches the SC
// stub of another IP-SM-GW, which shares the Store.
func TestStatusReportFromAnotherSC(t *testing.T) {
	store := sc.NewMemoryStore()
	first, g1 := newSC(everyone, store)
	second, g2 := newSC(everyone, store)
	submit := hello
	submit.StatusReportRequest = true
	if _, err := first.Submit(context.Background(), alice, submit); err != nil {
		t.Fatal(err)
	}
	second.Report(context.Background(), g1.next(t).Reference, ipsmgw.Delivered)
	if sr := g2.next(t); sr.Recipient != aliceTel {
		t.Errorf("status report went to %s", sr.Recipient)
	}
}
