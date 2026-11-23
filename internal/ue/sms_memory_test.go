package ue_test

import (
	"context"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// The tests of this file drive the memory of the UE for short messages
// and its availability for them, the two states in which a delivery to a
// registered user fails.

// fullMemory takes the memory of the UE away, with nothing to notify.
func fullMemory(t *testing.T, u *ue.UE) {
	t.Helper()
	got, err := u.SetSMSMemoryFull(context.Background(), true)
	if err != nil || !got.MemoryFull {
		t.Fatalf("the memory of the UE is %+v, %v; want it full", got, err)
	}
}

// freeMemory gives it back, which notifies the network where the UE owes
// a notification.
func freeMemory(t *testing.T, u *ue.UE) ue.SMSReception {
	t.Helper()
	got, err := u.SetSMSMemoryFull(context.Background(), false)
	if err != nil {
		t.Fatalf("the memory of the UE is %+v: %v", got, err)
	}
	return got
}

// checkNotification reads the RP-SMMA of a MESSAGE that TS 24.341
// 5.3.2.5 asks for, after checking its header fields, and returns its
// RP-MR.
func checkNotification(t *testing.T, m *sip.Message, target string) uint8 {
	t.Helper()
	if m.Method != sip.MESSAGE || m.RequestURI != target {
		t.Errorf("notification is %s %s, want a MESSAGE to %s", m.Method, m.RequestURI, target)
	}
	if to, _ := sip.ParseNameAddr(m.Get("To")); to.URI != target {
		t.Errorf("To = %q, want %s", m.Get("To"), target)
	}
	if from, _ := sip.ParseNameAddr(m.Get("From")); from.URI != impu {
		t.Errorf("From = %q, want the UE's public identity", m.Get("From"))
	}
	if m.Get("Content-Type") != ue.SMSContentType || m.Get("In-Reply-To") != "" {
		t.Errorf("Content-Type %q and In-Reply-To %q, want %s and none",
			m.Get("Content-Type"), m.Get("In-Reply-To"), ue.SMSContentType)
	}
	if routes := m.GetAll("Route"); len(routes) != 2 {
		t.Errorf("Route = %v, want the P-CSCF and the Service-Route", routes)
	}
	rp, err := sms.ParseRP(m.Body)
	if err != nil {
		t.Fatalf("notification body: %v", err)
	}
	smma, ok := rp.(sms.RPSMMA)
	if !ok {
		t.Fatalf("notification body %+v, want an RP-SMMA", rp)
	}
	return smma.MessageReference
}

// noNotification checks that the UE sends nothing. The fake network
// answers a request before it hands it over, so this waits a little.
func noNotification(t *testing.T, n *fakeNetwork) {
	t.Helper()
	select {
	case m := <-n.requests:
		t.Errorf("the UE sent %s %s, want no notification", m.Method, m.RequestURI)
	case <-time.After(200 * time.Millisecond):
	}
}

// TS 24.011 8.2.5.4 table 8.4 and TS 23.040 9.2.2.1a: a short message
// that the UE has no memory for is refused with RP-Cause 22 and TP-FCS
// D3, and the UE keeps neither the message nor anything of it. The
// refusal sets the memory capacity exceeded notification flag of TS
// 23.040 10.3 operation 14.
func TestTheUERefusesAShortMessageItHasNoMemoryFor(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	fullMemory(t, u)

	scts := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	delivered, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 42, deliverOf(t, "Is there room?", scts)))
	if code != 200 {
		t.Fatalf("MT MESSAGE got %d, want 200", code)
	}
	refused, ok := checkDeliveryReport(t, n.next(), delivered).(sms.RPError)
	if !ok || refused.MessageReference != 42 || refused.Cause != sms.RPCauseMemoryCapacityExceeded {
		t.Fatalf("report %+v, want RP-ERROR for RP-MR 42 with cause 22", refused)
	}
	tpdu, err := sms.ParseTPDU(refused)
	if report, ok := tpdu.(sms.DeliverReport); err != nil || !ok || report.FailureCause != sms.FCSMemoryCapacityExceeded {
		t.Errorf("RP-ERROR carries %+v (%v), want an SMS-DELIVER-REPORT with TP-FCS 0xd3", tpdu, err)
	}
	if got := u.SMS().Received; len(got) != 0 {
		t.Errorf("the UE kept %+v", got)
	}
	if got := u.SMSReception(); !got.MemoryFull || !got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want it to owe a notification", got)
	}
}

// A status report is not a short message for the memory: the UE files it
// with the SMS-SUBMIT that asked for it (TS 23.040 9.2.3.6), so it takes
// one while its memory is full, and owes no notification for it.
func TestTheUETakesAStatusReportWithoutMemory(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	const to = "+12025550102"
	sent, err := u.SendSMS(context.Background(), ue.OutgoingSMS{To: to, Text: "full", StatusReport: true})
	if err != nil {
		t.Fatal(err)
	}
	submitted(t, n.next())
	fullMemory(t, u)

	scts := time.Date(2026, 11, 20, 10, 0, 5, 0, time.UTC)
	statusReport, err := sms.StatusReport{MessageReference: sent.MessageReference, RecipientAddress: sms.InternationalNumber(to),
		ServiceCentreTimeStamp: scts, DischargeTime: scts.Add(time.Second), Status: sms.StatusReceivedBySME}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if _, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 78, statusReport)); code != 200 {
		t.Fatalf("status report got %d, want 200", code)
	}
	rp, err := sms.ParseRP(n.next().Body)
	if ack, ok := rp.(sms.RPAck); err != nil || !ok || ack.MessageReference != 78 {
		t.Errorf("report for the status report %+v (%v), want RP-ACK for RP-MR 78", rp, err)
	}
	waitSent(t, u, sent.CallID, func(s ue.SentSMS) bool { return s.StatusReport != nil })
	if got := u.SMSReception(); got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want no notification owed", got)
	}
}

// TS 24.341 5.3.2.5 and TS 23.040 10.3 operation 14: once it has memory
// again, a UE that refused a short message for want of it notifies the
// IP-SM-GW that brought that message (NOTE 1) with an RP-SMMA. The
// RP-ACK clears the flag. A notification asked for after that carries an
// RP-MR of its own (TS 24.011 6.3.3.1.3), so the network sees a
// notification it has already answered.
func TestTheUENotifiesTheNetworkOnceItHasMemory(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	fullMemory(t, u)
	n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 7, deliverOf(t, "Is there room?", time.Now())))
	n.next() // the RP-ERROR of the refusal

	got := freeMemory(t, u)
	if got.MemoryFull || !got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want its memory free and the notification still owed", got)
	}
	notification := n.next()
	mr := checkNotification(t, notification, ipsmgwURI)

	// The IP-SM-GW answers the notification as it answers a submission,
	// in a MESSAGE that names the one it answers (TS 24.341 5.3.3.4.3).
	ack := marshal(t, sms.RPAck{Direction: sms.NetworkToMS, MessageReference: mr})
	_, code := n.mtMessage(ua, ue.SMSContentType, ack, sip.HeaderField{Name: "In-Reply-To", Value: notification.Get("Call-ID")})
	if code != 200 {
		t.Fatalf("the answer to the notification got %d, want 200", code)
	}
	if got := u.SMSReception(); got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want the flag cleared by the RP-ACK", got)
	}

	if got, err := u.NotifyMemoryAvailable(context.Background()); err != nil || got.MemoryCapacityExceededNotification {
		t.Fatalf("one more notification: %+v, %v", got, err)
	}
	if again := checkNotification(t, n.next(), ipsmgwURI); again == mr {
		t.Errorf("the second notification carries RP-MR %d too, want one of its own", again)
	}
}

// TS 23.040 10.3 operation 14 has the MS notify the network only where
// the flag is set, so memory that is taken away and given back with no
// short message refused in between notifies nothing. A notification
// asked for by hand goes to the PSI of the SC, the one address the UE
// has before an IP-SM-GW has written to it (TS 24.341 5.3.2.5 item a).
func TestTheUENotifiesNothingWithoutARefusal(t *testing.T) {
	n := newFakeNetwork(t)
	u, _ := smsUE(t, n)
	fullMemory(t, u)
	if got := freeMemory(t, u); got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want no notification owed", got)
	}
	noNotification(t, n)

	if _, err := u.NotifyMemoryAvailable(context.Background()); err != nil {
		t.Fatal(err)
	}
	checkNotification(t, n.next(), "tel:"+scAddress)
}

// RFC 3261 21.4.18: a UE that is registered but takes no short messages
// answers the MESSAGE that brings one with 480, and sends no delivery
// report. The IP-SM-GW reports such a delivery to the SC as an absent
// subscriber (TS 29.311 6.1.4.4.1), which is the one state the UE cannot
// reach by deregistering.
func TestTheUEIsUnavailableForShortMessages(t *testing.T) {
	n := newFakeNetwork(t)
	u, ua := smsUE(t, n)
	if got := u.SetSMSAvailable(false); got.Available {
		t.Fatalf("the UE is %+v, want it unavailable", got)
	}

	if _, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 11, deliverOf(t, "Are you there?", time.Now()))); code != 480 {
		t.Fatalf("MT MESSAGE got %d, want 480", code)
	}
	noNotification(t, n)
	if got := u.SMS().Received; len(got) != 0 {
		t.Errorf("the UE kept %+v", got)
	}
	// Nothing of the refusal is the memory of the UE, so it owes no
	// notification.
	if got := u.SMSReception(); got.MemoryCapacityExceededNotification {
		t.Errorf("the UE is %+v, want no notification owed", got)
	}

	// Available again, the UE takes the next short message as it always
	// does.
	if got := u.SetSMSAvailable(true); !got.Available {
		t.Fatalf("the UE is %+v, want it available", got)
	}
	delivered, code := n.mtMessage(ua, ue.SMSContentType, rpDataWith(t, 12, deliverOf(t, "Are you there?", time.Now())))
	if code != 200 {
		t.Fatalf("MT MESSAGE got %d, want 200", code)
	}
	if ack, ok := checkDeliveryReport(t, n.next(), delivered).(sms.RPAck); !ok || ack.MessageReference != 12 {
		t.Errorf("report %+v, want an RP-ACK for RP-MR 12", ack)
	}
	if got := u.SMS().Received; len(got) != 1 {
		t.Errorf("the UE received %+v, want the short message", got)
	}
}
