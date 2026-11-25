package smsc

import (
	"context"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"reflect"
	"sync"
	"testing"
	"time"

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
			n.sc.Wait()
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
		wg.Go(func() { sc.ProcessDue(context.Background()) })
	}
	<-arrived
	other.ProcessDue(context.Background()) // the destination is leased
	close(release)
	wg.Wait()
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
	other.ProcessDue(context.Background())
	if got := len(n.nodes.sent()); got != 1 {
		t.Fatalf("%d TFRs during the lease, want 1", got)
	}
	n.clock.Advance(defaultLease)
	other.ProcessDue(context.Background())
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
			n.newSC().ProcessDue(ctx)
			if got := len(n.nodes.sent()); got != tt.attempts+2 {
				t.Errorf("%d TFRs, want the one that the stop cut and one from the instance in service", got)
			}
			if _, ok, err := n.store.Claim(ctx, bob, n.clock.Now(), defaultLease); err != nil || ok {
				t.Errorf("Bob still has a queue after the instance in service delivered: %v, %v", ok, err)
			}
		})
	}
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
