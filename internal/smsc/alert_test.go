package smsc

import (
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// TestAbsentThenAlert is the flow of TS 23.040 3.2.6 and 3.2.7: the MME
// finds Bob absent, the SC has the HSS keep its address in the MWD, and
// when Bob is back the HSS alerts the SC, which delivers at once rather
// than at its next retry.
func TestAbsentThenAlert(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))

	reports := n.hss.reports()
	want := sgd.ReportSMDeliveryStatusRequest{
		Header:         diameter.Header{DestinationRealm: epcRealm},
		UserIdentifier: sgd.UserIdentifier{MSISDN: bob},
		SCAddress:      scAddress,
		SMDeliveryOutcome: sgd.SMDeliveryOutcome{
			MMESMDeliveryOutcome: &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseAbsentUser},
		},
	}
	if len(reports) != 1 || !reflect.DeepEqual(reports[0], want) {
		t.Fatalf("RDRs = %+v, want one: %+v", reports, want)
	}

	n.nodes.set(mmeHost, accepts)
	n.clock.Advance(5 * time.Second) // well before the first retry
	if ala := n.alert(scAddress, bob); ala.Result != diameter.Base(diameter.DiameterSuccess) {
		t.Errorf("ALA = %+v, want success", ala)
	}
	sent := n.nodes.sent()
	if len(sent) != 2 {
		t.Fatalf("%d TFRs, want the first attempt and the one after the alert", len(sent))
	}
	if d, ok := tpdu(t, sent[1]).(sms.Deliver); !ok || sent[1].UserName != imsiOf[bob] {
		t.Errorf("TFR after the alert = %+v to %s", d, sent[1].UserName)
	}
	// The message is gone: the retry finds nothing.
	n.advance(DefaultRetrySchedule[0])
	if got := len(n.nodes.sent()); got != 2 {
		t.Errorf("%d TFRs after the retry time, want 2", got)
	}
}

// TestAlertsThatFindNothing are answered with success and deliver
// nothing (TS 29.338 5.2.2.3, Table 5.2.2.1-2).
func TestAlertsThatFindNothing(t *testing.T) {
	n := newNetwork(t)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	for _, tc := range []struct{ name, sc, msisdn string }{
		{"another SC", "12025550101", bob},
		{"a user with nothing held", scAddress, carol},
		{"no MSISDN", scAddress, ""},
	} {
		if ala := n.alert(tc.sc, tc.msisdn); ala.Result != diameter.Base(diameter.DiameterSuccess) {
			t.Errorf("%s: ALA = %+v, want success", tc.name, ala)
		}
	}
	if got := len(n.nodes.sent()); got != 1 {
		t.Errorf("%d TFRs, want only the first attempt", got)
	}
}

// TestDeliveryStatusPerNode checks the causes of the RDR (TS 29.338
// 5.3.3.14 to 5.3.3.19) and the failures that bring none.
func TestDeliveryStatusPerNode(t *testing.T) {
	memoryFull := answerWithCause(sgd.MemoryCapacityExceeded)
	for _, tc := range []struct {
		name    string
		sra     sgd.SendRoutingInfoForSMAnswer
		answers map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)
		want    *sgd.SMDeliveryOutcome
	}{
		{
			name: "absent at the MME, memory full at the SMSF",
			sra: sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: imsiOf[bob],
				ServingNode:     &sgd.ServingNode{MMEName: mmeHost, MMERealm: epcRealm, MMENumberForMTSMS: mmeNumber},
				SMSF3GPPAddress: &sgd.SMSF3GPPAddress{SMSF3GPPName: smsfHost, SMSF3GPPRealm: epcRealm}},
			answers: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){mmeHost: absent, smsfHost: memoryFull},
			want: &sgd.SMDeliveryOutcome{
				MMESMDeliveryOutcome:      &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseAbsentUser},
				SMSF3GPPSMDeliveryOutcome: &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseUEMemoryCapacityExceeded},
			},
		},
		{
			name: "absent behind the IP-SM-GW",
			sra: sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: "001019876543210",
				ServingNode: &sgd.ServingNode{IPSMGWNumber: "12025550192", IPSMGWName: ipsmgw, IPSMGWRealm: imsRealm}},
			answers: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){ipsmgw: absent},
			want: &sgd.SMDeliveryOutcome{
				IPSMGWSMDeliveryOutcome: &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseAbsentUser},
			},
		},
		{
			name:    "busy",
			sra:     servedByMME(bob),
			answers: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){mmeHost: answerWith(diameter.Experimental(sgd.DiameterErrorUserBusyForMTSMS))},
		},
		{
			// The HSS updates the MWD itself (TS 29.338 5.2.1.3).
			name: "no node registered",
			sra:  sgd.SendRoutingInfoForSMAnswer{Result: diameter.Experimental(sgd.DiameterErrorAbsentUser)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			n.hss.set(bob, tc.sra)
			for host, f := range tc.answers {
				n.nodes.set(host, f)
			}
			n.submit(bob, hello(t, bob, 1))
			reports := n.hss.reports()
			if tc.want == nil {
				if len(reports) != 0 {
					t.Errorf("RDRs = %+v, want none", reports)
				}
				return
			}
			if len(reports) != 1 || !reflect.DeepEqual(reports[0].SMDeliveryOutcome, *tc.want) {
				t.Errorf("RDRs = %+v, want one with %+v", reports, *tc.want)
			}
		})
	}
}

// TestASuccessfulDeliveryIsReportedWhileTheHSSHoldsAFlag: a delivery that
// succeeds while the MWD-Status of the SRA shows MNRF, MCEF or MNRG set
// is reported to the HSS, with SUCCESSFUL_TRANSFER for the node that
// delivered it, so that the HSS clears the flag (TS 23.040 10.1
// operation 3, 3.2.6 items 2g and 2h). Any other delivery that succeeds
// goes unreported.
func TestASuccessfulDeliveryIsReportedWhileTheHSSHoldsAFlag(t *testing.T) {
	servedByIPSMGW := sgd.SendRoutingInfoForSMAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: imsiOf[bob],
		ServingNode: &sgd.ServingNode{IPSMGWNumber: "12025550192", IPSMGWName: ipsmgw, IPSMGWRealm: imsRealm}}
	success := &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseSuccessfulTransfer}
	for _, tc := range []struct {
		name   string
		sra    sgd.SendRoutingInfoForSMAnswer
		status *sgd.MWDStatus
		want   *sgd.SMDeliveryOutcome
	}{
		{"no MWD-Status", servedByMME(bob), nil, nil},
		{"the SC address and no flag", servedByMME(bob), new(sgd.MWDStatus(0)), nil},
		{"MCEF", servedByMME(bob), new(sgd.MWDMCEFSet), &sgd.SMDeliveryOutcome{MMESMDeliveryOutcome: success}},
		{"MCEF, for another SC", servedByMME(bob), new(sgd.MWDSCAddressNotIncluded | sgd.MWDMCEFSet),
			&sgd.SMDeliveryOutcome{MMESMDeliveryOutcome: success}},
		{"MNRF", servedByMME(bob), new(sgd.MWDMNRFSet), &sgd.SMDeliveryOutcome{MMESMDeliveryOutcome: success}},
		{"MNRG", servedByMME(bob), new(sgd.MWDMNRGSet), &sgd.SMDeliveryOutcome{MMESMDeliveryOutcome: success}},
		{"MCEF, through the IP-SM-GW", servedByIPSMGW, new(sgd.MWDMCEFSet), &sgd.SMDeliveryOutcome{IPSMGWSMDeliveryOutcome: success}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := newNetwork(t)
			n.nodes.set(ipsmgw, accepts)
			tc.sra.MWDStatus = tc.status
			n.hss.set(bob, tc.sra)
			n.submit(bob, hello(t, bob, 1))
			if got := n.sentTo(bob); got != 1 {
				t.Fatalf("%d TFRs, want the one that delivered", got)
			}
			reports := n.hss.reports()
			if tc.want == nil {
				if len(reports) != 0 {
					t.Errorf("RDRs = %+v, want none", reports)
				}
				return
			}
			want := sgd.ReportSMDeliveryStatusRequest{
				Header:            diameter.Header{DestinationRealm: epcRealm},
				UserIdentifier:    sgd.UserIdentifier{MSISDN: bob},
				SCAddress:         scAddress,
				SMDeliveryOutcome: *tc.want,
			}
			if len(reports) != 1 || !reflect.DeepEqual(reports[0], want) {
				t.Errorf("RDRs = %+v, want one: %+v", reports, want)
			}
		})
	}
}

// TestMWDListFull has the HSS refuse to keep the SC address. The SC
// still retries on its own (TS 23.040 3.2.8).
func TestMWDListFull(t *testing.T) {
	n := newNetwork(t)
	n.hss.rdaResult = diameter.Experimental(sgd.DiameterErrorMWDListFull)
	n.nodes.set(mmeHost, absent)
	n.submit(bob, hello(t, bob, 1))
	n.nodes.set(mmeHost, accepts)
	n.advance(DefaultRetrySchedule[0])
	if got := len(n.nodes.sent()); got != 2 {
		t.Errorf("%d TFRs, want the retry to have delivered", got)
	}
}

func answerWithCause(c sgd.SMEnumeratedDeliveryFailureCause) func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
	return func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error) {
		return sgd.MTForwardShortMessageAnswer{
			Result:                 diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: c},
		}, nil
	}
}
