package ipsmgw_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the IP-SM-GW handing the short message of a UE to the SC
// over SGd: the OFR it sends and what each answer of TS 29.338 6.2.1.1
// becomes for the sender (TS 23.204 6.3 steps 6 to 10).

// forwarder records the OFR the IP-SM-GW sends and answers it with what
// answer gives.
type forwarder struct {
	requests chan sgd.MOForwardShortMessageRequest
	answer   func() (sgd.MOForwardShortMessageAnswer, error)
}

func (f *forwarder) MOForwardShortMessage(_ context.Context, req sgd.MOForwardShortMessageRequest) (sgd.MOForwardShortMessageAnswer, error) {
	f.requests <- req
	return f.answer()
}

// smsIWMSC is an SC over SGd whose answers the test chooses.
func smsIWMSC(answer func() (sgd.MOForwardShortMessageAnswer, error)) (*ipsmgw.SMSIWMSC, *forwarder) {
	f := &forwarder{requests: make(chan sgd.MOForwardShortMessageRequest, 10), answer: answer}
	return ipsmgw.NewSMSIWMSC(f, scNumber, domain, quiet()), f
}

func answeringOFA(a sgd.MOForwardShortMessageAnswer) func() (sgd.MOForwardShortMessageAnswer, error) {
	return func() (sgd.MOForwardShortMessageAnswer, error) { return a, nil }
}

// submitReport is the SMS-SUBMIT-REPORT that an SC puts in SM-RP-UI, for
// RP-ACK when fcs is zero and for RP-ERROR otherwise (TS 23.040
// 9.2.2.2a).
func submitReport(t *testing.T, fcs sms.FailureCause) []byte {
	t.Helper()
	return marshal(t, sms.SubmitReport{FailureCause: fcs,
		ServiceCentreTimeStamp: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)})
}

// TestShortMessageGoesToTheSCOverSGd is TS 29.338 Table 6.2.1.1/1: the
// OFR carries the SC that the UE addressed, the sender's number and the
// SMS-SUBMIT as the UE wrote it, and names only the realm it goes to
// (6.3.1.1). The SMS-SUBMIT-REPORT of the OFA reaches the sender in the
// RP-ACK.
func TestShortMessageGoesToTheSCOverSGd(t *testing.T) {
	sc, f := smsIWMSC(answeringOFA(sgd.MOForwardShortMessageAnswer{
		Result: diameter.Base(diameter.DiameterSuccess),
		SMRPUI: submitReport(t, 0),
	}))
	h := newHarnessParts(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK, ipsmgw.Config{}, sc, nil)

	mo := message(submitBody(t, 7, scAddress, hello))
	if resp := h.send(mo); resp.StatusCode != 202 {
		t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
	}
	ofr := <-f.requests
	if ofr.SCAddress != scNumber || ofr.UserIdentifier.MSISDN != "12025550101" || ofr.UserIdentifier.UserName != "" {
		t.Errorf("OFR = %+v, want SC-Address %s and the sender's MSISDN alone", ofr, scNumber)
	}
	if ofr.Header.DestinationRealm != domain || ofr.Header.DestinationHost != "" {
		t.Errorf("OFR goes to host %q realm %q, want the realm alone", ofr.Header.DestinationHost, ofr.Header.DestinationRealm)
	}
	tpdu, err := sms.ParseTPDU(sms.RPDataMSToNetwork{UserData: ofr.SMRPUI})
	if err != nil {
		t.Fatalf("SM-RP-UI: %v", err)
	}
	if submit, ok := tpdu.(sms.Submit); !ok || submit.DestinationAddress != hello.DestinationAddress ||
		submit.MessageReference != hello.MessageReference {
		t.Errorf("SM-RP-UI holds %+v, want the SMS-SUBMIT of the UE", tpdu)
	}

	report := h.next()
	ack, ok := parseRP(t, report).(sms.RPAck)
	if !ok || ack.MessageReference != 7 {
		t.Fatalf("submit report body = %+v, want an RP-ACK with RP-MR 7", parseRP(t, report))
	}
	got, err := sms.ParseTPDU(ack)
	if err != nil || got.(sms.SubmitReport).ServiceCentreTimeStamp.IsZero() {
		t.Errorf("SMS-SUBMIT-REPORT = %+v, %v", got, err)
	}
}

// TestShortMessageRefusedOverSGd is TS 29.338 6.2.1.3: each way the
// SMS-IWMSC refuses a short message becomes the RP-Cause of TS 24.011
// Table 8.4 part 1 that says the same thing, and the TP-FCS of the
// answer's report goes to the UE with it.
func TestShortMessageRefusedOverSGd(t *testing.T) {
	refusal := func(cause sgd.SMEnumeratedDeliveryFailureCause, fcs sms.FailureCause) sgd.MOForwardShortMessageAnswer {
		return sgd.MOForwardShortMessageAnswer{
			Result:                 diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: cause},
			SMRPUI:                 submitReport(t, fcs),
		}
	}
	for _, tt := range []struct {
		name    string
		ofa     sgd.MOForwardShortMessageAnswer
		err     error
		want    sms.RPCause
		wantFCS sms.FailureCause // 0: no SMS-SUBMIT-REPORT
	}{
		{name: "invalid SME address", ofa: refusal(sgd.InvalidSMEAddress, sms.FCSInvalidSMEAddress),
			want: sms.RPCauseUnassignedNumber, wantFCS: sms.FCSInvalidSMEAddress},
		{name: "user not SC user", ofa: refusal(sgd.UserNotSCUser, 0xc1),
			want: sms.RPCauseRequestedFacilityNotSubscribed, wantFCS: 0xc1},
		{name: "SC congestion", ofa: refusal(sgd.SCCongestion, sms.FCSSCSystemFailure),
			want: sms.RPCauseCongestion, wantFCS: sms.FCSSCSystemFailure},
		{name: "unknown service centre", ofa: refusal(sgd.UnknownServiceCentre, 0),
			want: sms.RPCauseShortMessageTransferRejected},
		{name: "facility not supported", ofa: sgd.MOForwardShortMessageAnswer{
			Result: diameter.Experimental(sgd.DiameterErrorFacilityNotSupported), SMRPUI: submitReport(t, sms.FCSTPDUNotSupported)},
			want: sms.RPCauseRequestedFacilityNotImplemented, wantFCS: sms.FCSTPDUNotSupported},
		{name: "a delivery failure without a cause", ofa: sgd.MOForwardShortMessageAnswer{
			Result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure)}, want: sms.RPCauseTemporaryFailure},
		{name: "a result of the base protocol", ofa: sgd.MOForwardShortMessageAnswer{
			Result: diameter.Base(diameter.DiameterUnableToComply)}, want: sms.RPCauseTemporaryFailure},
		{name: "no answer at all", err: errors.New("the SC did not answer"), want: sms.RPCauseTemporaryFailure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ofa, err := tt.ofa, tt.err
			sc, _ := smsIWMSC(func() (sgd.MOForwardShortMessageAnswer, error) { return ofa, err })
			h := newHarnessParts(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK, ipsmgw.Config{}, sc, nil)
			if resp := h.send(message(submitBody(t, 3, scAddress, hello))); resp.StatusCode != 202 {
				t.Fatalf("MESSAGE answered %d, want 202", resp.StatusCode)
			}
			report := h.next()
			e, ok := parseRP(t, report).(sms.RPError)
			if !ok || e.MessageReference != 3 || e.Cause != tt.want {
				t.Fatalf("body = %+v, want an RP-ERROR with RP-MR 3 and cause %d", parseRP(t, report), tt.want)
			}
			tpdu, err := sms.ParseTPDU(e)
			switch {
			case tt.wantFCS == 0 && !errors.Is(err, sms.ErrNoUserData):
				t.Errorf("RP-ERROR carries %+v, want no TPDU", tpdu)
			case tt.wantFCS != 0 && (err != nil || tpdu.(sms.SubmitReport).FailureCause != tt.wantFCS):
				t.Errorf("RP-ERROR carries %+v (%v), want TP-FCS %#02x", tpdu, err, uint8(tt.wantFCS))
			}
		})
	}
}
