package ipsmgw_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

var deliver = sms.Deliver{
	OriginatingAddress:     sms.InternationalNumber("+12025550101"),
	ServiceCentreTimeStamp: time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC),
	UserData:               hello.UserData,
}

// deliveryReport is the MESSAGE of TS 24.341 5.3.2.4 that answers mt,
// as the S-CSCF of the recipient passes it on.
func deliveryReport(t *testing.T, mt *sip.Message, rp sms.RPMessage) *sip.Message {
	t.Helper()
	m := message(marshal(t, rp))
	m.RequestURI = ownURI
	m.Set("To", "<"+ownURI+">")
	m.Add("In-Reply-To", mt.Get("Call-ID"))
	return m
}

// delivered starts a delivery to Bob and returns the MESSAGE that
// carries it, with its RP-DATA.
func (h *harness) delivered(reference string) (*sip.Message, sms.RPDataNetworkToMS) {
	h.t.Helper()
	h.g.Deliver(ipsmgw.Delivery{Reference: reference, Recipient: bobTel, TPDU: deliver})
	mt := h.next()
	data, ok := parseRP(h.t, mt).(sms.RPDataNetworkToMS)
	if !ok {
		h.t.Fatalf("MESSAGE carries %+v, want RP-DATA", parseRP(h.t, mt))
	}
	return mt, data
}

// TestDeliveryAndItsReport is TS 24.341 5.3.3.4.2: the MESSAGE to the
// recipient, and the delivery report that ends the delivery. A second
// report for the same MESSAGE corresponds to nothing anymore (488).
func TestDeliveryAndItsReport(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	mt, data := h.delivered("ref-1")
	if mt.RequestURI != bobTel {
		t.Errorf("Request-URI = %s, want %s", mt.RequestURI, bobTel)
	}
	checkHeaders(t, mt, map[string]string{
		"To":                  "<" + bobTel + ">",
		"P-Asserted-Identity": "<" + ownURI + ">",
		"Accept-Contact":      "*;+g.3gpp.smsip;require;explicit",
		"Request-Disposition": "no-fork",
		"In-Reply-To":         "",
		"Content-Type":        ipsmgw.ContentType,
		"Route":               "<sip:" + domain + ";lr>",
	})
	if data.OriginatorAddress != scAddress {
		t.Errorf("RP-OA = %v, want the SC", data.OriginatorAddress)
	}
	tpdu, err := sms.ParseTPDU(data)
	got, ok := tpdu.(sms.Deliver)
	if err != nil || !ok || !got.ServiceCentreTimeStamp.Equal(deliver.ServiceCentreTimeStamp) {
		t.Fatalf("TPDU = %+v, %v, want %+v", tpdu, err, deliver)
	}
	// TP-SCTS comes back in a zone of its own offset.
	got.ServiceCentreTimeStamp = deliver.ServiceCentreTimeStamp
	if !reflect.DeepEqual(got, deliver) {
		t.Errorf("SMS-DELIVER = %+v, want %+v", got, deliver)
	}

	report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		UserData: marshal(t, sms.DeliverReport{})})
	if resp := h.send(report); resp.StatusCode != 202 {
		t.Fatalf("delivery report answered %d, want 202", resp.StatusCode)
	}
	if got := h.nextReport(); got != (reported{"ref-1", ipsmgw.Delivered}) {
		t.Errorf("SC heard %+v", got)
	}
	if resp := h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})); resp.StatusCode != 488 {
		t.Errorf("second delivery report answered %d, want 488", resp.StatusCode)
	}
}

func TestDeliveryReportWithRPError(t *testing.T) {
	tests := []struct {
		cause sms.RPCause
		want  ipsmgw.Outcome
	}{
		{sms.RPCauseMemoryCapacityExceeded, ipsmgw.MemoryCapacityExceeded},
		{sms.RPCauseProtocolErrorUnspecified, ipsmgw.ProtocolError},
	}
	for _, tt := range tests {
		h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
		mt, data := h.delivered("ref")
		h.send(deliveryReport(t, mt, sms.RPError{Direction: sms.MSToNetwork, MessageReference: data.MessageReference, Cause: tt.cause}))
		if got := h.nextReport(); got.outcome != tt.want {
			t.Errorf("RP-Cause %d: outcome %v, want %v", tt.cause, got.outcome, tt.want)
		}
	}
}

// TestDeliveryReportsThatAreHardToRead is TS 24.011 9.3.4, which the
// network may follow, and TS 24.007 11.2.4: an RP-ERROR with an IE the
// IP-SM-GW does not know still reports its cause, and one whose RP-Cause
// is broken counts as cause 111. Either ends the delivery at once.
func TestDeliveryReportsThatAreHardToRead(t *testing.T) {
	tests := []struct {
		name string
		body func(mr uint8) []byte
		want ipsmgw.Outcome
	}{
		{"unknown IE", func(mr uint8) []byte { return []byte{0x04, mr, 0x01, 22, 0x42, 0x01, 0x00} }, ipsmgw.MemoryCapacityExceeded},
		{"empty RP-Cause", func(mr uint8) []byte { return []byte{0x04, mr, 0x00} }, ipsmgw.ProtocolError},
	}
	for _, tt := range tests {
		h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
		mt, data := h.delivered("ref")
		report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})
		report.Body = tt.body(data.MessageReference)
		if resp := h.send(report); resp.StatusCode != 202 {
			t.Fatalf("%s: delivery report answered %d, want 202", tt.name, resp.StatusCode)
		}
		if got := h.nextReport(); got.outcome != tt.want {
			t.Errorf("%s: outcome %v, want %v", tt.name, got.outcome, tt.want)
		}
	}
}

// TestMessageReferencesDifferPerRecipient checks that each RP-DATA to
// one UE gets the next RP-MR (TS 24.011 8.2.3).
func TestMessageReferencesDifferPerRecipient(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	_, first := h.delivered("ref-1")
	_, second := h.delivered("ref-2")
	if second.MessageReference != first.MessageReference+1 {
		t.Errorf("RP-MR %d then %d", first.MessageReference, second.MessageReference)
	}
}

// TestFailedMessageEndsTheDelivery is the mapping of TS 29.311 Table
// 6.1.4.4.1.1 that TS 24.341 5.3.3.4.2 refers to. A MESSAGE that no one
// answers counts as a 408.
func TestFailedMessageEndsTheDelivery(t *testing.T) {
	tests := []struct {
		status int
		want   ipsmgw.Outcome
	}{
		{480, ipsmgw.AbsentSubscriber},
		{404, ipsmgw.UnidentifiedSubscriber},
		{604, ipsmgw.UnidentifiedSubscriber},
		{486, ipsmgw.SubscriberBusy},
		{407, ipsmgw.IllegalSubscriber},
		{403, ipsmgw.SystemFailure},
		{503, ipsmgw.SystemFailure},
		{0, ipsmgw.SystemFailure},
	}
	for _, tt := range tests {
		h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, func(*sip.Message) int { return tt.status })
		mt, data := h.delivered("ref")
		if got := h.nextReport(); got != (reported{"ref", tt.want}) {
			t.Errorf("status %d: SC heard %+v, want %v", tt.status, got, tt.want)
		}
		// The delivery has ended, so a report that comes after all
		// corresponds to nothing.
		if resp := h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})); resp.StatusCode != 488 {
			t.Errorf("status %d: late delivery report answered %d, want 488", tt.status, resp.StatusCode)
		}
	}
}

// TestNoDeliveryReportEndsTheDelivery: a report for another RP-MR does
// not answer the RP-DATA (TS 24.011 9.3.2), so the timer ends the
// delivery.
func TestNoDeliveryReportEndsTheDelivery(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), 300*time.Millisecond, answerOK)
	mt, data := h.delivered("ref")
	if resp := h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference + 1})); resp.StatusCode != 202 {
		t.Fatalf("delivery report answered %d, want 202", resp.StatusCode)
	}
	if got := h.nextReport(); got != (reported{"ref", ipsmgw.SystemFailure}) {
		t.Errorf("SC heard %+v", got)
	}
}

// TestDeliveryReportToAnotherIPSMGW: two IP-SM-GWs share a
// Store, and the one that did not send the short message gets its
// delivery report, as DNS may have it.
func TestDeliveryReportToAnotherIPSMGW(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	a := newHarness(t, store, time.Minute, answerOK)
	b := newHarness(t, store, time.Minute, answerOK)
	mt, data := a.delivered("ref")
	report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})
	if resp := b.send(report); resp.StatusCode != 202 {
		t.Fatalf("delivery report answered %d, want 202", resp.StatusCode)
	}
	if got := b.nextReport(); got != (reported{"ref", ipsmgw.Delivered}) {
		t.Errorf("SC of the second IP-SM-GW heard %+v", got)
	}
	if len(a.sc.reports) != 0 {
		t.Errorf("SC of the first IP-SM-GW heard %+v too", <-a.sc.reports)
	}
}

func TestDeliveryReportForNoDelivery(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	m := message(marshal(t, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: 1}))
	m.Add("In-Reply-To", "no-such-call-id")
	if resp := h.send(m); resp.StatusCode != 488 {
		t.Errorf("answered %d, want 488", resp.StatusCode)
	}
}
