package smsc

import (
	"reflect"
	"testing"
	"time"

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
		Header:         sgd.Header{DestinationRealm: epcRealm},
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
	if ala := n.alert(scAddress, bob); ala.Result != sgd.Base(sgd.DiameterSuccess) {
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
		if ala := n.alert(tc.sc, tc.msisdn); ala.Result != sgd.Base(sgd.DiameterSuccess) {
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
			sra: sgd.SendRoutingInfoForSMAnswer{Result: sgd.Base(sgd.DiameterSuccess), UserName: imsiOf[bob],
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
			sra: sgd.SendRoutingInfoForSMAnswer{Result: sgd.Base(sgd.DiameterSuccess), UserName: "001019876543210",
				ServingNode: &sgd.ServingNode{IPSMGWNumber: "12025550192", IPSMGWName: ipsmgw, IPSMGWRealm: imsRealm}},
			answers: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){ipsmgw: absent},
			want: &sgd.SMDeliveryOutcome{
				IPSMGWSMDeliveryOutcome: &sgd.NodeSMDeliveryOutcome{SMDeliveryCause: sgd.SMDeliveryCauseAbsentUser},
			},
		},
		{
			name:    "busy",
			sra:     servedByMME(bob),
			answers: map[string]func(sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error){mmeHost: answerWith(sgd.Experimental(sgd.DiameterErrorUserBusyForMTSMS))},
		},
		{
			// The HSS updates the MWD itself (TS 29.338 5.2.1.3).
			name: "no node registered",
			sra:  sgd.SendRoutingInfoForSMAnswer{Result: sgd.Experimental(sgd.DiameterErrorAbsentUser)},
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

// TestMWDListFull has the HSS refuse to keep the SC address. The SC
// still retries on its own (TS 23.040 3.2.8).
func TestMWDListFull(t *testing.T) {
	n := newNetwork(t)
	n.hss.rdaResult = sgd.Experimental(sgd.DiameterErrorMWDListFull)
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
			Result:                 sgd.Experimental(sgd.DiameterErrorSMDeliveryFailure),
			SMDeliveryFailureCause: &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: c},
		}, nil
	}
}
