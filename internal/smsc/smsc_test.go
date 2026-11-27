package smsc

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

func TestShortMessageAcceptedAndDelivered(t *testing.T) {
	n := newNetwork(t)
	n.clock.Advance(400 * time.Millisecond)
	ofa := n.submit(bob, hello(t, bob, 7))

	if ofa.Result != diameter.Base(diameter.DiameterSuccess) || ofa.SMDeliveryFailureCause != nil {
		t.Fatalf("OFA = %+v, want success", ofa)
	}
	// TS 23.040 9.2.2.2a: the SMS-SUBMIT-REPORT for RP-ACK, with the
	// time the SC took the message.
	report, err := sms.ParseTPDU(sms.RPAck{Direction: sms.NetworkToMS, UserData: ofa.SMRPUI})
	if err != nil || !report.(sms.SubmitReport).ServiceCentreTimeStamp.Equal(t0) {
		t.Fatalf("SMS-SUBMIT-REPORT = %+v, %v, want TP-SCTS %v", report, err, t0)
	}

	sent := n.nodes.sent()
	if len(sent) != 1 {
		t.Fatalf("%d TFRs, want 1", len(sent))
	}
	tfr := sent[0]
	if tfr.DestinationHost != mmeHost || tfr.DestinationRealm != epcRealm || tfr.UserName != imsiOf[bob] ||
		tfr.SCAddress != scAddress || tfr.MMENumberForMTSMS != mmeNumber {
		t.Errorf("TFR = %+v", tfr)
	}
	deliver, ok := tpdu(t, tfr).(sms.Deliver)
	if !ok {
		t.Fatalf("TFR carries %T, want SMS-DELIVER", tpdu(t, tfr))
	}
	text, _ := sms.DecodeText(deliver.DataCodingScheme, deliver.UserData)
	if deliver.OriginatingAddress != sms.InternationalNumber("+"+alice) || text != "hello" ||
		!deliver.ServiceCentreTimeStamp.Equal(t0) || deliver.MoreMessagesToSend || deliver.StatusReportIndication {
		t.Errorf("SMS-DELIVER = %+v, %q", deliver, text)
	}
	// One SRR checks the recipient before the OFA, one finds the node.
	if got := n.hss.srrCount(); got != 2 {
		t.Errorf("%d SRRs, want 2", got)
	}
	for _, srr := range n.hss.srr {
		if srr.MSISDN != bob || srr.SCAddress != scAddress || srr.SupportedFeatures != sgd.SMSFSupport || srr.DestinationRealm != epcRealm {
			t.Errorf("SRR = %+v", srr)
		}
	}
	// Nothing is left to deliver.
	n.advance(48 * time.Hour)
	if got := len(n.nodes.sent()); got != 1 {
		t.Errorf("%d TFRs after the delivery, want still 1", got)
	}
}

// TestOFRSentAgainToAnotherInstance is RFC 6733 3: a serving node that
// got no OFA in time sends the OFR again, with its Origin-Host and
// End-to-End Identifier, and it may reach another instance of the SC.
// That one answers what the first one answers, both while the first is
// still at the request and after it has answered, and the short message
// is queued, and delivered, once.
func TestOFRSentAgainToAnotherInstance(t *testing.T) {
	n := newNetwork(t)
	other := n.newSC()
	ofr := n.ofr(hello(t, bob, 7))
	hold := make(chan struct{})
	n.hss.holdSRRs(hold)

	first := make(chan sgd.MOForwardShortMessageAnswer, 1)
	go func() { first <- n.sc.MOForwardShortMessage(context.Background(), ofr) }()
	deadline := time.Now().Add(5 * time.Second)
	for n.hss.srrCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first instance never asked the HSS about the recipient")
		}
		time.Sleep(time.Millisecond)
	}
	during := make(chan sgd.MOForwardShortMessageAnswer, 1)
	go func() { during <- other.MOForwardShortMessage(context.Background(), ofr) }()
	select {
	case a := <-during:
		t.Fatalf("the copy was answered %+v before the first instance had answered", a)
	case <-time.After(100 * time.Millisecond):
	}
	n.hss.holdSRRs(nil)
	close(hold)

	want := <-first
	if want.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("OFA = %+v, want success", want)
	}
	for _, got := range []sgd.MOForwardShortMessageAnswer{<-during, other.MOForwardShortMessage(context.Background(), ofr)} {
		if !reflect.DeepEqual(got, want) {
			t.Errorf("OFA to the copy = %+v, want %+v, the answer to the first", got, want)
		}
	}
	waitDeliveries(t, n.sc)
	waitDeliveries(t, other)
	if sent := n.nodes.sent(); len(sent) != 1 {
		t.Errorf("%d TFRs, want 1", len(sent))
	}
}

// TestOFRSentAgainWhileTheFirstHasNoAnswer: a copy of an OFR waits for
// the answer to the first copy no longer than its sender waits for an
// answer at all, the Answer timer. The SC cannot tell then whether the
// first instance took the short message, and refuses the copy.
func TestOFRSentAgainWhileTheFirstHasNoAnswer(t *testing.T) {
	n := newNetwork(t)
	other := n.newSC()
	ofr := n.ofr(hello(t, bob, 7))
	hold := make(chan struct{})
	n.hss.holdSRRs(hold)

	first := make(chan sgd.MOForwardShortMessageAnswer, 1)
	go func() { first <- n.sc.MOForwardShortMessage(context.Background(), ofr) }()
	deadline := time.Now().Add(5 * time.Second)
	for n.hss.srrCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first instance never asked the HSS about the recipient")
		}
		time.Sleep(time.Millisecond)
	}
	start := time.Now()
	copied := make(chan sgd.MOForwardShortMessageAnswer, 1)
	go func() { copied <- other.MOForwardShortMessage(context.Background(), ofr) }()
	select {
	case a := <-copied:
		if waited := time.Since(start); waited < answerTimer {
			t.Errorf("the copy was answered after %v, before the Answer timer of %v ran out", waited, answerTimer)
		}
		if a.SMDeliveryFailureCause == nil || a.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause != sgd.SCCongestion {
			t.Errorf("OFA to the copy = %+v, want SC congestion", a)
		}
	case <-time.After(answerTimer + 2*time.Second):
		t.Errorf("the copy got no answer within the Answer timer of %v", answerTimer)
	}
	n.hss.holdSRRs(nil)
	close(hold)
	<-first
	waitDeliveries(t, n.sc)
}

// TestOFAKeptAfterAStoreThatFailedOnce: the instance that took an OFR
// writes its answer again when the store fails to take it, so that a
// copy of the OFR still gets that answer rather than a refusal for a
// short message the SC holds.
func TestOFAKeptAfterAStoreThatFailedOnce(t *testing.T) {
	n := newNetwork(t)
	flaky := &ofaFails{MemoryStore: n.store}
	flaky.fails.Store(1)
	first := New(Config{Address: scAddress, HSSRealm: epcRealm, AnswerTimer: answerTimer}, flaky, n.nodes, n.hss, n.clock, quietLog)
	ofr := n.ofr(hello(t, bob, 7))

	want := first.MOForwardShortMessage(context.Background(), ofr)
	if want.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("OFA = %+v, want success", want)
	}
	if got := n.sc.MOForwardShortMessage(context.Background(), ofr); !reflect.DeepEqual(got, want) {
		t.Errorf("OFA to the copy = %+v, want %+v, the answer to the first", got, want)
	}
	waitDeliveries(t, first)
	if sent := n.nodes.sent(); len(sent) != 1 {
		t.Errorf("%d TFRs, want 1", len(sent))
	}
}

// ofaFails is a Store whose PutOFA fails a number of times before it
// succeeds, as a store that is briefly out of reach.
type ofaFails struct {
	*MemoryStore
	fails atomic.Int32
}

func (s *ofaFails) PutOFA(ctx context.Context, request string, a sgd.MOForwardShortMessageAnswer, expires time.Time) error {
	if s.fails.Add(-1) >= 0 {
		return errors.New("the store is out of reach")
	}
	return s.MemoryStore.PutOFA(ctx, request, a, expires)
}

func TestStatusReport(t *testing.T) {
	n := newNetwork(t)
	submit := hello(t, bob, 42)
	submit.StatusReportRequest = true
	n.submit(bob, submit)

	sent := n.nodes.sent()
	if len(sent) != 2 {
		t.Fatalf("%d TFRs, want the short message and the status report", len(sent))
	}
	if d := tpdu(t, sent[0]).(sms.Deliver); !d.StatusReportIndication || sent[0].UserName != imsiOf[bob] {
		t.Errorf("SMS-DELIVER = %+v to %s, want TP-SRI to Bob", d, sent[0].UserName)
	}
	report, ok := tpdu(t, sent[1]).(sms.StatusReport)
	if !ok || sent[1].UserName != imsiOf[alice] {
		t.Fatalf("second TFR = %T to %s, want SMS-STATUS-REPORT to Alice", tpdu(t, sent[1]), sent[1].UserName)
	}
	// TS 23.040 9.2.2.3: the TP-MR of the SMS-SUBMIT, its TP-DA, and
	// when the SC received it.
	if report.MessageReference != 42 || report.RecipientAddress != sms.InternationalNumber("+"+bob) ||
		!report.ServiceCentreTimeStamp.Equal(t0) || report.Status != sms.StatusReceivedBySME {
		t.Errorf("SMS-STATUS-REPORT = %+v", report)
	}
}

func TestRefusals(t *testing.T) {
	absolute := hello(t, bob, 1)
	absolute.ValidityPeriod.Format = sms.ValidityPeriodRelative
	absoluteTPDU, _ := absolute.Marshal()
	absoluteTPDU[0] |= 0b11 << 3 // TP-VPF absolute, which package sms does not write

	national := hello(t, bob, 1)
	national.DestinationAddress.TypeOfNumber = sms.TONNational
	nationalTPDU, _ := national.Marshal()
	unknown, _ := hello(t, "12025550198", 1).Marshal()
	barred, _ := hello(t, carol, 1).Marshal()
	good, _ := hello(t, bob, 1).Marshal()

	for _, tc := range []struct {
		name   string
		change func(*network, *sgd.MOForwardShortMessageRequest)
		result diameter.Result
		cause  sgd.SMEnumeratedDeliveryFailureCause
		fcs    sms.FailureCause // zero: no SMS-SUBMIT-REPORT
	}{
		{"another SC", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SCAddress = "12025550101" },
			diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.UnknownServiceCentre, 0},
		{"sender without MSISDN", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.UserIdentifier.MSISDN = "" },
			diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.UserNotSCUser, 0xc1},
		{"not a TPDU", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SMRPUI = []byte{0x01} },
			diameter.Experimental(sgd.DiameterErrorFacilityNotSupported), 0, 0xb0},
		{"SMS-COMMAND", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SMRPUI = []byte{0x02, 0x01, 0x00} },
			diameter.Experimental(sgd.DiameterErrorFacilityNotSupported), 0, 0xa1},
		{"absolute TP-VP", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SMRPUI = absoluteTPDU },
			diameter.Experimental(sgd.DiameterErrorFacilityNotSupported), 0, 0xc6},
		{"national TP-DA", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SMRPUI = nationalTPDU },
			diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.InvalidSMEAddress, 0xc3},
		{"no user has the number", func(_ *network, r *sgd.MOForwardShortMessageRequest) { r.SMRPUI = unknown },
			diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.InvalidSMEAddress, 0xc3},
		{"recipient barred", func(n *network, r *sgd.MOForwardShortMessageRequest) {
			n.hss.set(carol, sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorServiceBarred)})
			r.SMRPUI = barred
		}, diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.InvalidSMEAddress, 0xc4},
		{"HSS down", func(n *network, r *sgd.MOForwardShortMessageRequest) { n.hss.down = true },
			diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), sgd.SCCongestion, 0xc2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			req := sgd.MOForwardShortMessageRequest{
				Header:         diameter.Header{SessionID: "s", OriginHost: mmeHost, OriginRealm: epcRealm, DestinationRealm: epcRealm},
				SCAddress:      scAddress,
				UserIdentifier: sgd.UserIdentifier{UserName: imsiOf[alice], MSISDN: alice},
				SMRPUI:         good,
			}
			tc.change(n, &req)
			ofa := n.sc.MOForwardShortMessage(context.Background(), req)
			waitDeliveries(t, n.sc)
			if ofa.Result != tc.result {
				t.Errorf("result = %v, want %v", ofa.Result, tc.result)
			}
			if tc.result == diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure) &&
				(ofa.SMDeliveryFailureCause == nil || ofa.SMDeliveryFailureCause.SMEnumeratedDeliveryFailureCause != tc.cause) {
				t.Errorf("SM-Delivery-Failure-Cause = %+v, want %v", ofa.SMDeliveryFailureCause, tc.cause)
			}
			if tc.fcs == 0 {
				if ofa.SMRPUI != nil {
					t.Errorf("SM-RP-UI = % X, want none", ofa.SMRPUI)
				}
			} else if r, err := sms.ParseTPDU(sms.RPError{Direction: sms.NetworkToMS, UserData: ofa.SMRPUI}); err != nil || r.(sms.SubmitReport).FailureCause != tc.fcs {
				t.Errorf("SMS-SUBMIT-REPORT = %+v, %v, want TP-FCS %#x", r, err, tc.fcs)
			}
			if sent := n.nodes.sent(); len(sent) != 0 {
				t.Errorf("a refused message was delivered: %+v", sent)
			}
		})
	}
}

// TestRetrySchedule has the MME find Bob absent until the fourth
// attempt.
func TestRetrySchedule(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	attempts := func() int { return len(n.nodes.sent()) }
	if got := attempts(); got != 1 {
		t.Fatalf("%d attempts at once, want 1", got)
	}
	for i, wait := range DefaultRetrySchedule[:2] {
		n.advance(wait - time.Second)
		if got := attempts(); got != i+1 {
			t.Fatalf("%d attempts a second before retry %d, want %d", got, i+1, i+1)
		}
		n.advance(time.Second)
		if got := attempts(); got != i+2 {
			t.Fatalf("%d attempts at retry %d, want %d", got, i+1, i+2)
		}
	}
	n.nodes.set(mmeHost, accepts)
	n.advance(DefaultRetrySchedule[2])
	if got := attempts(); got != 4 {
		t.Fatalf("%d attempts, want 4", got)
	}
	n.advance(24 * time.Hour)
	if got := attempts(); got != 4 {
		t.Errorf("%d attempts after the delivery, want 4", got)
	}
}

// TestLastRetryDelayRepeats checks that the SC keeps trying at the last
// delay of the schedule.
func TestLastRetryDelayRepeats(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	for _, wait := range DefaultRetrySchedule {
		n.advance(wait)
	}
	before := len(n.nodes.sent())
	n.advance(time.Hour)
	n.advance(time.Hour)
	if got := len(n.nodes.sent()) - before; got != 2 {
		t.Errorf("%d attempts in two hours after the schedule, want 2", got)
	}
}

func TestValidityPeriodExpires(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, func(r sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		if r.UserName == imsiOf[bob] {
			return sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}, nil
		}
		return sgd.MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
	})
	submit := hello(t, bob, 9)
	submit.StatusReportRequest = true
	// TS 23.040 9.2.3.12.1: TP-VP 0 is five minutes.
	submit.ValidityPeriod = sms.ValidityPeriod{Format: sms.ValidityPeriodRelative, Relative: 0}
	n.submit(bob, submit)
	// The first retry is at 30 s, the second at 2 min 30 s; then the
	// next would be at 12 min 30 s, after the period.
	n.advance(30 * time.Second)
	n.advance(2 * time.Minute)
	toBob := len(n.nodes.sent())
	n.advance(2*time.Minute + 29*time.Second)
	if got := len(n.nodes.sent()); got != toBob {
		t.Fatalf("%d TFRs before the period ended, want %d", got, toBob)
	}
	n.advance(time.Second)
	sent := n.nodes.sent()
	if len(sent) != toBob+1 {
		t.Fatalf("%d TFRs, want the status report after %d attempts", len(sent), toBob)
	}
	report, ok := tpdu(t, sent[toBob]).(sms.StatusReport)
	if !ok || sent[toBob].UserName != imsiOf[alice] {
		t.Fatalf("last TFR = %T to %s, want SMS-STATUS-REPORT to Alice", tpdu(t, sent[toBob]), sent[toBob].UserName)
	}
	// TS 23.040 9.2.3.15: SM Validity Period Expired, a permanent error.
	if report.Status != 0b100_0110 || report.MessageReference != 9 || !report.DischargeTime.Equal(t0.Add(5*time.Minute)) {
		t.Errorf("SMS-STATUS-REPORT = %+v", report)
	}
	n.advance(time.Hour)
	if got := len(n.nodes.sent()); got != toBob+1 {
		t.Errorf("%d TFRs after the message was discarded, want %d", got, toBob+1)
	}
}

func TestValidityPeriodRelative(t *testing.T) {
	c := New(Config{}, nil, nil, nil, nil, quietLog)
	for vp, want := range map[uint8]time.Duration{
		0:   5 * time.Minute,
		143: 12 * time.Hour,
		144: 12*time.Hour + 30*time.Minute,
		167: 24 * time.Hour,
		168: 2 * 24 * time.Hour,
		196: 30 * 24 * time.Hour,
		197: 5 * 7 * 24 * time.Hour,
		255: 63 * 7 * 24 * time.Hour,
	} {
		if got := c.validityPeriod(sms.ValidityPeriod{Format: sms.ValidityPeriodRelative, Relative: vp}); got != want {
			t.Errorf("TP-VP %d = %v, want %v", vp, got, want)
		}
	}
	if got := c.validityPeriod(sms.ValidityPeriod{}); got != 72*time.Hour {
		t.Errorf("without TP-VP = %v, want the default", got)
	}
}

// TestDeliveryFailures reads each error of the TFA (TS 29.338 6.2.2.2)
// as TS 23.040 Table 1 and 8.1.1 say: whether the SC keeps the message,
// and whether it tries the SMSF after the MME.
func TestDeliveryFailures(t *testing.T) {
	failure := func(c sgd.SMEnumeratedDeliveryFailureCause) *sgd.SMDeliveryFailureCause {
		return &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: c}
	}
	for _, tc := range []struct {
		name      string
		tfa       sgd.MTForwardShortMessageAnswer
		kept      bool
		smsfTried bool
	}{
		{"absent user", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}, true, true},
		{"user unknown in the MME", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorUserUnknown)}, true, true},
		{"busy", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorUserBusyForMTSMS)}, true, false},
		{"illegal user", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorIllegalUser)}, false, false},
		{"illegal equipment", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorIllegalEquipment)}, false, false},
		{"facility not supported", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorFacilityNotSupported)}, true, true},
		{"memory capacity exceeded", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), SMDeliveryFailureCause: failure(sgd.MemoryCapacityExceeded)}, true, false},
		{"protocol error", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), SMDeliveryFailureCause: failure(sgd.EquipmentProtocolError)}, true, false},
		{"not SM equipped", sgd.MTForwardShortMessageAnswer{Result: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), SMDeliveryFailureCause: failure(sgd.EquipmentNotSMEquipped)}, true, true},
		{"base error", sgd.MTForwardShortMessageAnswer{Result: diameter.Base(5012)}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			sra := servedByMME(bob)
			sra.SMSF3GPPAddress = &sgd.SMSF3GPPAddress{SMSF3GPPNumber: "12025550191", SMSF3GPPName: smsfHost, SMSF3GPPRealm: epcRealm}
			n.hss.set(bob, sra)
			n.nodes.set(mmeHost, func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) { return tc.tfa, nil })
			n.nodes.set(smsfHost, absent)
			submit := hello(t, bob, 3)
			submit.StatusReportRequest = true
			n.submit(bob, submit)

			var hosts []string
			for _, tfr := range n.nodes.sent() {
				hosts = append(hosts, tfr.DestinationHost)
			}
			want := []string{mmeHost}
			if tc.smsfTried {
				want = append(want, smsfHost)
			}
			if !tc.kept {
				want = append(want, mmeHost) // the status report to Alice
			}
			if !reflect.DeepEqual(hosts, want) {
				t.Fatalf("TFRs went to %v, want %v", hosts, want)
			}
			if !tc.kept {
				last := n.nodes.sent()[len(hosts)-1]
				if r, ok := tpdu(t, last).(sms.StatusReport); !ok || r.Status != sms.StatusNotObtainable {
					t.Errorf("status report = %+v, want TP-ST Not obtainable", tpdu(t, last))
				}
			}
			n.advance(DefaultRetrySchedule[0])
			retried := len(n.nodes.sent()) > len(hosts)
			if retried != tc.kept {
				t.Errorf("retried = %v, want %v", retried, tc.kept)
			}
		})
	}
}

// TestRoutingFailures reads the errors of the SRA (TS 29.338 5.2.1.3).
func TestRoutingFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		sra  sgd.SendRoutingInfoForSMAnswer
		kept bool
	}{
		{"absent user", sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)}, true},
		{"service barred", sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorServiceBarred)}, true},
		{"not subscribed", sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorServiceNotSubscribed)}, false},
		{"no node for SGd", sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: imsiOf[bob],
			ServingNode: &sgd.ServingNode{IPSMGWNumber: "12025550192"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			n.nodes.set(mmeHost, absent)
			n.submit(bob, hello(t, bob, 1))
			// Bob's routing changes before the first retry.
			n.hss.set(bob, tc.sra)
			n.advance(DefaultRetrySchedule[0])
			srr := n.hss.srrCount()
			n.advance(DefaultRetrySchedule[1])
			if retried := n.hss.srrCount() > srr; retried != tc.kept {
				t.Errorf("retried = %v, want %v", retried, tc.kept)
			}
		})
	}
}

func TestServingNodes(t *testing.T) {
	sra := sgd.SendRoutingInfoForSMAnswer{
		Result:          diameter.Base(diameter.DiameterSuccess),
		ServingNode:     &sgd.ServingNode{IPSMGWNumber: "12025550192", IPSMGWName: ipsmgw},
		SMSF3GPPAddress: &sgd.SMSF3GPPAddress{SMSF3GPPName: smsfHost, SMSF3GPPRealm: epcRealm},
	}
	want := []servingNode{
		{kind: nodeIPSMGW, host: ipsmgw, realm: imsRealm},
		{kind: nodeSMSF, host: smsfHost, realm: epcRealm},
	}
	if got := servingNodes(sra); !reflect.DeepEqual(got, want) {
		t.Errorf("servingNodes = %+v, want %+v", got, want)
	}
}

// TestAnotherInstanceOfTheIPSMGW: an IP-SM-GW that answers the TFR
// DIAMETER_TOO_BUSY, or that the SC has no connection to, has not begun
// the delivery, so the SC sends the TFR to the other IP-SM-GWs of the
// realm until one takes it (RFC 6733 7.1.3). An IP-SM-GW that took the
// TFR and gave no answer may have delivered it, and an MME is reached
// with a number of its own in the TFR, so after either the short
// message waits for the retry instead.
func TestAnotherInstanceOfTheIPSMGW(t *testing.T) {
	const (
		second = "ipsmgw-2." + imsRealm
		third  = "ipsmgw-3." + imsRealm
		mme2   = "mme-2." + epcRealm
	)
	tooBusy := answerWith(diameter.Base(diameter.DiameterTooBusy))
	noAnswer := func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		return sgd.MTForwardShortMessageAnswer{}, errors.New("no answer to the TFR")
	}
	servedByIPSMGW := sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: imsiOf[bob],
		ServingNode: &sgd.ServingNode{IPSMGWNumber: "12025550192", IPSMGWName: ipsmgw, IPSMGWRealm: imsRealm}}
	for _, tc := range []struct {
		name  string
		nodes func(*fakeNodes)
		mme   bool
		// tried are the nodes that the TFRs went to, and kept whether the
		// short message waits for a retry.
		tried []string
		kept  bool
	}{
		{"too busy", func(n *fakeNodes) { n.set(ipsmgw, tooBusy) }, false, []string{ipsmgw, second}, false},
		{"no connection", func(n *fakeNodes) { n.disconnect(ipsmgw) }, false, []string{second}, false},
		{"every instance too busy", func(n *fakeNodes) {
			n.set(ipsmgw, tooBusy)
			n.set(second, tooBusy)
			n.set(third, tooBusy)
		}, false, []string{ipsmgw, second, third}, true},
		{"no answer", func(n *fakeNodes) { n.set(ipsmgw, noAnswer) }, false, []string{ipsmgw}, true},
		{"an MME too busy", func(n *fakeNodes) { n.set(mmeHost, tooBusy) }, true, []string{mmeHost}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			if !tc.mme {
				n.hss.set(bob, servedByIPSMGW)
			}
			n.nodes.set(ipsmgw, accepts)
			n.nodes.set(second, accepts)
			n.nodes.set(mme2, accepts)
			tc.nodes(n.nodes)
			n.submit(bob, hello(t, bob, 3))

			var hosts []string
			for _, tfr := range n.nodes.sent() {
				hosts = append(hosts, tfr.DestinationHost)
				if want := realmOf(tfr.DestinationHost, ""); tfr.DestinationRealm != want || tfr.UserName != imsiOf[bob] {
					t.Errorf("TFR to %s for %s in realm %s, want %s and realm %s",
						tfr.DestinationHost, tfr.UserName, tfr.DestinationRealm, imsiOf[bob], want)
				}
			}
			if !reflect.DeepEqual(hosts, tc.tried) {
				t.Fatalf("TFRs went to %v, want %v", hosts, tc.tried)
			}
			n.advance(DefaultRetrySchedule[0])
			if retried := len(n.nodes.sent()) > len(hosts); retried != tc.kept {
				t.Errorf("retried = %v, want %v", retried, tc.kept)
			}
		})
	}
}

// TestQueueInOrder sends Bob two messages while he is absent. They go
// out one at a time, in order, with TP-MMS on the first (TS 23.040
// 3.2.4, 6.2), and with time stamps a second apart.
func TestQueueInOrder(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.submit(bob, hello(t, bob, 2))
	if got := len(n.nodes.sent()); got != 1 {
		t.Fatalf("%d TFRs, want 1: the second message waits behind the first", got)
	}
	n.nodes.set(mmeHost, accepts)
	n.advance(DefaultRetrySchedule[0])
	sent := n.nodes.sent()[1:]
	if len(sent) != 2 {
		t.Fatalf("%d TFRs after the retry, want 2", len(sent))
	}
	first, second := tpdu(t, sent[0]).(sms.Deliver), tpdu(t, sent[1]).(sms.Deliver)
	if !first.MoreMessagesToSend || second.MoreMessagesToSend {
		t.Errorf("TP-MMS = %v then %v, want more then no more", first.MoreMessagesToSend, second.MoreMessagesToSend)
	}
	if !first.ServiceCentreTimeStamp.Equal(t0) || !second.ServiceCentreTimeStamp.Equal(t0.Add(time.Second)) {
		t.Errorf("TP-SCTS = %v then %v, want a second apart", first.ServiceCentreTimeStamp, second.ServiceCentreTimeStamp)
	}
}

// TestTwoInstancesDeliverOnce has two SC instances share one store. Both
// see the same message due at the same time; only one delivers it.
func TestTwoInstancesDeliverOnce(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))

	// The MME holds the TFR until both instances have had a chance at
	// the destination.
	release := make(chan struct{})
	var once sync.Once
	arrived := make(chan struct{})
	n.nodes.set(mmeHost, func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		once.Do(func() { close(arrived) })
		<-release
		return sgd.MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
	})
	other := n.newSC()
	n.clock.Advance(DefaultRetrySchedule[0])
	var wg sync.WaitGroup
	for _, sc := range []*SMSC{n.sc, other} {
		wg.Go(sc.ProcessDue)
	}
	<-arrived
	other.ProcessDue() // the destination is leased
	close(release)
	wg.Wait()
	waitDeliveries(t, n.sc)
	waitDeliveries(t, other)
	if got := len(n.nodes.sent()); got != 2 {
		t.Errorf("%d TFRs, want the first attempt and one retry", got)
	}
}

// TestLeaseTakenOver has an instance stop in the middle of a delivery.
// Once the lease has run out, the other instance delivers.
func TestLeaseTakenOver(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.clock.Advance(DefaultRetrySchedule[0])
	// The first instance claims Bob and stops before the TFR.
	if _, ok, _ := n.store.ClaimDue(context.Background(), n.clock.Now(), defaultLease); !ok {
		t.Fatal("cannot claim Bob")
	}
	n.nodes.set(mmeHost, accepts)
	other := n.newSC()
	sweep(t, other)
	if got := len(n.nodes.sent()); got != 1 {
		t.Fatalf("%d TFRs during the lease, want 1", got)
	}
	n.clock.Advance(defaultLease)
	sweep(t, other)
	if got := len(n.nodes.sent()); got != 2 {
		t.Errorf("%d TFRs after the lease ran out, want 2", got)
	}
}

// TestAStoppingInstanceLeavesItsAttemptToAnother: an instance stops while
// it waits for the TFA of an attempt. The stop cuts the attempt short,
// which tells nothing of the recipient, so the attempt does not count and
// the destination is due at once: an instance in service delivers the
// message without waiting for the next retry or for the lease to run
// out. The store of the instance that stops fails a call whose context
// has ended, as one outside the process does. The attempt of a short
// message that has just come and that of a retry end the same way.
func TestAStoppingInstanceLeavesItsAttemptToAnother(t *testing.T) {
	for _, tt := range []struct {
		name string
		// attempts is how many attempts failed before the one that the
		// stop cuts.
		attempts int
	}{
		{"a short message that has just come", 0},
		{"a retry", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := newNetwork(t)
			if tt.attempts > 0 {
				n.nodes.set(mmeHost, absent)
				n.submit(bob, hello(t, bob, 1))
				n.clock.Advance(DefaultRetrySchedule[0])
			}
			n.nodes.silence(mmeHost, true)
			stopping := New(Config{Address: scAddress, HSSRealm: epcRealm}, cutStore{n.store}, n.nodes, n.hss, n.clock, quietLog)
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			ran := make(chan struct{})
			go func() {
				stopping.Run(ctx)
				close(ran)
			}()
			if tt.attempts == 0 {
				n.submitTo(stopping, hello(t, bob, 1))
			}
			waitFor(t, func() bool { return len(n.nodes.sent()) == tt.attempts+1 }, "the TFR of the instance that stops")

			stop()
			select {
			case <-ran:
			case <-time.After(5 * time.Second):
				t.Fatal("the SC did not stop within 5 s of its context ending")
			}
			ctx = context.Background()
			l, ok, err := n.store.Claim(ctx, bob, n.clock.Now(), defaultLease)
			if err != nil || !ok {
				t.Fatalf("Bob is not due once the instance has stopped: %v, %v", ok, err)
			}
			if l.Attempts != tt.attempts {
				t.Errorf("%d attempts after the stop, want the %d before the one that the stop cut", l.Attempts, tt.attempts)
			}
			if err := n.store.Release(ctx, l, n.clock.Now(), l.Attempts); err != nil {
				t.Fatal(err)
			}

			n.nodes.silence(mmeHost, false)
			n.nodes.set(mmeHost, accepts)
			sweep(t, n.newSC())
			if got := len(n.nodes.sent()); got != tt.attempts+2 {
				t.Errorf("%d TFRs, want the one that the stop cut and one from the instance in service", got)
			}
			if _, ok, err := n.store.Claim(ctx, bob, n.clock.Now(), defaultLease); err != nil || ok {
				t.Errorf("Bob still has a queue after the instance in service delivered: %v, %v", ok, err)
			}
		})
	}
}

// run starts Run on sc. The function it returns ends the context of
// Run, and the channel is closed once Run has returned. At the end of
// the test it ends the context and waits up to ten seconds for Run.
func run(t *testing.T, sc *SMSC) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	ran := make(chan struct{})
	go func() {
		sc.Run(ctx)
		close(ran)
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-ran:
		case <-time.After(10 * time.Second):
			t.Error("Run did not return within 10 s of its context ending")
		}
	})
	return stop, ran
}

// gatedStore is a Store whose second ClaimDue waits until the test opens
// the gate, as a claim does that the store is slow to answer. reached
// is closed once that claim has begun.
type gatedStore struct {
	*MemoryStore
	claims  atomic.Int32
	reached chan struct{}
	gate    chan struct{}
}

func (s *gatedStore) ClaimDue(ctx context.Context, now time.Time, lease time.Duration) (Lease, bool, error) {
	if s.claims.Add(1) == 2 {
		close(s.reached)
		<-s.gate
	}
	return s.MemoryStore.ClaimDue(ctx, now, lease)
}

// TestAStopCutsTheSweepShort: the end of the context of Run stops the
// instance at once, also while its sweep is still claiming. The attempt
// under way is cut short and its destination is due again, and the
// destination that the sweep claimed as the instance stopped goes back
// without an attempt. Neither attempt counts, and an instance in service
// takes both at once.
func TestAStopCutsTheSweepShort(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.clock.Advance(time.Second)
	n.submit(carol, hello(t, carol, 1))
	n.clock.Advance(DefaultRetrySchedule[0])
	n.nodes.set(mmeHost, accepts)
	n.nodes.silence(mmeHost, true)
	store := &gatedStore{MemoryStore: n.store, reached: make(chan struct{}), gate: make(chan struct{})}
	// One sweep, as Run starts: it claims Bob, who has been due the
	// longest, and then Carol.
	sc := New(Config{Address: scAddress, HSSRealm: epcRealm, PollInterval: time.Hour}, store, n.nodes, n.hss, n.clock, quietLog)
	stop, ran := run(t, sc)
	var once sync.Once
	letGo := func() { once.Do(func() { close(store.gate) }) }
	// The cleanups run last in first, so the gate opens before Run is
	// waited for.
	t.Cleanup(letGo)

	waitFor(t, func() bool { return n.sentTo(bob) == 2 }, "the retry to Bob")
	select {
	case <-store.reached:
	case <-time.After(10 * time.Second):
		t.Fatal("the sweep made no second claim within 10 s")
	}
	stop()
	var l Lease
	waitFor(t, func() bool {
		var ok bool
		l, ok, _ = n.store.Claim(context.Background(), bob, n.clock.Now(), defaultLease)
		return ok
	}, "Bob to be due again once the stop has cut his attempt short")
	if l.Attempts != 1 {
		t.Errorf("Bob has %d attempts, want the one before the stop", l.Attempts)
	}

	letGo()
	select {
	case <-ran:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return within 10 s of its last claim")
	}
	if got := n.sentTo(carol); got != 1 {
		t.Errorf("%d TFRs to Carol, want only the first: the instance had stopped when its claim came back", got)
	}
	if l, ok, err := n.store.Claim(context.Background(), carol, n.clock.Now(), defaultLease); err != nil || !ok || l.Attempts != 1 {
		t.Errorf("Carol is %+v, %v, %v; want her due at once, with the one attempt before the stop", l, ok, err)
	}
}

// TestWaitWhileTheSweepsGoOn: Wait may be called at any time, also from
// several goroutines while Run sweeps for what is due over and over.
func TestWaitWhileTheSweepsGoOn(t *testing.T) {
	n := newNetwork(t)
	sc := New(Config{Address: scAddress, HSSRealm: epcRealm, PollInterval: time.Microsecond},
		n.store, n.nodes, n.hss, n.clock, quietLog)
	run(t, sc)
	deadline := time.Now().Add(300 * time.Millisecond)
	var waiting sync.WaitGroup
	for range 4 {
		waiting.Go(func() {
			for time.Now().Before(deadline) {
				sc.Wait()
			}
		})
	}
	waiting.Wait()
}

// An empty retry schedule is no schedule: the SC takes the default one
// rather than having no delay to wait.
func TestAnEmptyRetryScheduleIsTheDefault(t *testing.T) {
	c := New(Config{Address: "12025550100", RetrySchedule: []time.Duration{}}, NewMemoryStore(), nil, nil,
		SystemClock, quietLog)
	if !reflect.DeepEqual(c.cfg.RetrySchedule, DefaultRetrySchedule) {
		t.Errorf("schedule = %v, want %v", c.cfg.RetrySchedule, DefaultRetrySchedule)
	}
}

// sentTo counts the TFRs that went to user.
func (n *network) sentTo(user string) int {
	count := 0
	for _, tfr := range n.nodes.sent() {
		if tfr.UserName == imsiOf[user] {
			count++
		}
	}
	return count
}

// holdTFRsTo has the MME accept every TFR, and hold those to user until
// the test ends or calls the function it returns, as an IP-SM-GW holds a
// TFR until the UE has sent its delivery report.
func (n *network) holdTFRsTo(user string) (letGo func()) {
	n.t.Helper()
	release := make(chan struct{})
	var once sync.Once
	letGo = func() { once.Do(func() { close(release) }) }
	n.t.Cleanup(letGo)
	n.nodes.set(mmeHost, func(r sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		if r.UserName == imsiOf[user] {
			<-release
		}
		return sgd.MTForwardShortMessageAnswer{Result: diameter.Base(diameter.DiameterSuccess)}, nil
	})
	return letGo
}

// TestTheSweepGoesOnWhileADeliveryWaits: while the TFR of one delivery
// waits for its answer, the instance goes on with the destinations that
// become due, here the retry of Carol's short message.
func TestTheSweepGoesOnWhileADeliveryWaits(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.clock.Advance(10 * time.Second)
	n.submit(carol, hello(t, carol, 1))
	// The retry to Bob is due 30 s after t0, the one to Carol 10 s
	// later.
	sc := New(Config{Address: scAddress, HSSRealm: epcRealm, PollInterval: 10 * time.Millisecond},
		n.store, n.nodes, n.hss, n.clock, quietLog)
	run(t, sc)
	// The cleanups run last in first, so the held TFR goes before the SC
	// stops.
	n.holdTFRsTo(bob)

	n.clock.Advance(20 * time.Second)
	waitFor(t, func() bool { return n.sentTo(bob) == 2 }, "the retry to Bob")
	n.clock.Advance(10 * time.Second)
	waitFor(t, func() bool { return n.sentTo(carol) == 2 }, "the retry to Carol while the one to Bob waits for its TFA")
}

// TestAnInstanceDeliversToAsManyDestinationsAsItMay: an instance whose
// one delivery waits for its TFA claims no other destination, neither in
// a sweep nor for a short message that has just come. Both stay due in
// the store, and another instance delivers them.
func TestAnInstanceDeliversToAsManyDestinationsAsItMay(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.clock.Advance(time.Second)
	n.submit(carol, hello(t, carol, 1))
	letGo := n.holdTFRsTo(bob)
	one := New(Config{Address: scAddress, HSSRealm: epcRealm, Deliveries: 1}, n.store, n.nodes, n.hss, n.clock, quietLog)

	// Bob has been due the longest, so the sweep claims him first.
	n.clock.Advance(DefaultRetrySchedule[0])
	one.ProcessDue()
	waitFor(t, func() bool { return n.sentTo(bob) == 2 }, "the retry to Bob")
	if ofa := n.submitTo(one, hello(t, alice, 2)); ofa.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("OFA = %+v, want the short message taken", ofa)
	}
	if toCarol, toAlice := n.sentTo(carol), n.sentTo(alice); toCarol != 1 || toAlice != 0 {
		t.Errorf("%d TFRs to Carol and %d to Alice while the instance is busy, want only the first one to Carol", toCarol, toAlice)
	}

	sweep(t, n.newSC())
	if toCarol, toAlice := n.sentTo(carol), n.sentTo(alice); toCarol != 2 || toAlice != 1 {
		t.Errorf("%d TFRs to Carol and %d to Alice, want the retry and the new short message from the other instance", toCarol, toAlice)
	}
	letGo()
	waitDeliveries(t, one)
}
