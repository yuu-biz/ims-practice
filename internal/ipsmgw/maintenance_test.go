package ipsmgw_test

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/config"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file tries the IP-SM-GW while the operator has it out of service
// through the maintenance API of its node.

// gwRole is the name of the IP-SM-GW in the configuration file, which
// is the path segment of its maintenance API.
const gwRole = string(config.RoleIPSMGW)

// gwRetryAfter is the Retry-After that the IP-SM-GW answers with its
// 503, in seconds.
const gwRetryAfter = "30"

// manage gives the IP-SM-GW of h the maintenance API of a node, as the
// node of the IP-SM-GW does, and returns the node. The HSS takes Sh
// requests, as it does while the node runs with a peer.
func (h *harness) manage() *maintenance.Node {
	h.t.Helper()
	return h.manageWith(func() bool { return true })
}

func (h *harness) manageWith(hssAvailable func() bool) *maintenance.Node {
	h.t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          gwRole,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{h.g.Function(hssAvailable)},
		Configuration: func() any { return map[string]string{"node": gwRole} },
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.g.Manage(node.ManagedFunction(gwRole))
	return node
}

// gwState is the state of the IP-SM-GW in the maintenance API of node.
func gwState(t *testing.T, node *maintenance.Node) maintenance.FunctionState {
	t.Helper()
	return node.State().Functions[0]
}

// setState makes the event that takes the IP-SM-GW to want, and returns
// its state after it.
func setState(t *testing.T, node *maintenance.Node, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := node.SetAdministrativeState(gwRole, maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// inHand is how much work the IP-SM-GW counts, which must be a count
// that the role gives.
func inHand(t *testing.T, node *maintenance.Node) int {
	t.Helper()
	f := gwState(t, node)
	if f.Users == nil {
		t.Fatalf("the IP-SM-GW is %+v, want a count of its users", f)
	}
	return *f.Users
}

// waitFor waits up to ten seconds for cond, which work that ends in
// another goroutine brings about.
func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// refusal checks that resp is the 503 of an IP-SM-GW out of service:
// with Retry-After and the Warning that says why.
func refusal(t *testing.T, resp *sip.Message) {
	t.Helper()
	if resp.StatusCode != 503 || resp.Get("Retry-After") != gwRetryAfter {
		t.Errorf("got %d with Retry-After %q, want 503 with %s", resp.StatusCode, resp.Get("Retry-After"), gwRetryAfter)
	}
	if w := resp.Get("Warning"); !strings.Contains(w, "Out of service") {
		t.Errorf("the 503 has Warning %q, want one that says the IP-SM-GW is out of service", w)
	}
}

// nextSubmit is the next short message that the SC was handed.
func (h *harness) nextSubmit() submitted {
	h.t.Helper()
	select {
	case got := <-h.sc.submits:
		return got
	case <-time.After(5 * time.Second):
		h.t.Fatal("the SC was handed no short message")
		return submitted{}
	}
}

// noSubmit checks that the SC was handed no short message.
func (h *harness) noSubmit() {
	h.t.Helper()
	select {
	case got := <-h.sc.submits:
		h.t.Errorf("the SC got %+v, want no short message", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// A shut down IP-SM-GW refuses the MESSAGE that carries a short message
// from a UE with 503 and Retry-After, and hands the SC nothing. With no
// work in hand it is LOCKED at once, as a shut down leaves a function
// with no user (X.731 7.1.3.3), and after the unlock the short message
// reaches the SC again.
func TestAShutDownIPSMGWRefusesAShortMessageFromAUE(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v, want LOCKED with no work in hand", f)
	}
	refusal(t, h.send(message(submitBody(t, 7, scAddress, hello))))
	h.noSubmit()
	if n := inHand(t, node); n != 0 {
		t.Errorf("the IP-SM-GW counts %d users after the refusal, want none of a refused MESSAGE", n)
	}

	setState(t, node, maintenance.Unlocked)
	if resp := h.send(message(submitBody(t, 8, scAddress, hello))); resp.StatusCode != 202 {
		t.Fatalf("the short message after the unlock got %d, want 202", resp.StatusCode)
	}
	if got := h.nextSubmit(); got.submit.MessageReference != hello.MessageReference {
		t.Errorf("the SC got %+v after the unlock", got)
	}
}

// A shut down IP-SM-GW takes the delivery report of an RP-DATA that
// this network has already sent, although the MESSAGE that carries it
// arrives outside any dialog: In-Reply-To names the MESSAGE it answers
// (TS 24.341 5.3.2.4 item d), so it ends a delivery that has begun, and
// the SC hears that the UE got the short message.
func TestAShutDownIPSMGWTakesADeliveryReport(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	mt, data := h.delivered("ref-1")
	waitFor(t, func() bool { return inHand(t, node) == 0 }, "the delivery to be out of the IP-SM-GW's hands")

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("shut down gave %+v, want LOCKED: the wait for a delivery report is no user", f)
	}
	report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})
	if resp := h.send(report); resp.StatusCode != 202 {
		t.Fatalf("the delivery report got %d while the IP-SM-GW is locked, want 202", resp.StatusCode)
	}
	if got := h.nextReport(); got.reference != "ref-1" || got.outcome != ipsmgw.Delivered {
		t.Errorf("the SC heard %+v, want ref-1 delivered", got)
	}
}

// A MESSAGE whose In-Reply-To names no delivery of this network is
// taken too, and answered 488 as ever (TS 24.341 5.3.3.4.1 item a). The
// header is what tells a delivery report from a new short message, and
// a 503 would send the UE to another instance that has no such delivery
// either.
func TestALockedIPSMGWAnswersADeliveryReportForNoDelivery(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	setState(t, node, maintenance.Locked)

	m := message(marshal(t, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: 1}))
	m.Add("In-Reply-To", "no-such-call-id")
	if resp := h.send(m); resp.StatusCode != 488 {
		t.Errorf("the delivery report for no delivery got %d, want 488", resp.StatusCode)
	}
}

// A shut down IP-SM-GW takes the NOTIFY of a reg event subscription,
// which carries on the SUBSCRIBE that this network sent: the first one
// completes the dialog and says that the user has a contact with the
// feature tag of SMS over IP, so the HSS learns that the user takes
// short messages (TS 24.341 5.3.3.2).
func TestAShutDownIPSMGWTakesTheNotifyOfARegEventSubscription(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	h.thirdPartyRegister(600)
	subscribe := h.subscribed()
	waitFor(t, func() bool { return inHand(t, node) == 0 }, "the third-party REGISTER and its SUBSCRIBE to be done")

	setState(t, node, maintenance.ShuttingDown)
	n := notify(subscribe, 1, "active;expires=600", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))
	if resp := h.send(n); resp.StatusCode != 200 {
		t.Fatalf("the reg event NOTIFY got %d while the IP-SM-GW is shut down, want 200", resp.StatusCode)
	}
	if got := h.nextUpdate(); got != registered {
		t.Errorf("the HSS was told %+v, want %+v", got, registered)
	}
}

// A shut down IP-SM-GW refuses the third-party REGISTER, of a
// registration and of a deregistration alike, and sends no SUBSCRIBE:
// the subscription it would make is in the Store, where any other
// instance finds it.
func TestAShutDownIPSMGWRefusesTheThirdPartyRegister(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	setState(t, node, maintenance.ShuttingDown)

	for _, expires := range []int{600, 0} {
		refusal(t, h.thirdPartyRegister(expires))
	}
	select {
	case m := <-h.received:
		t.Errorf("the entry point got %s %s, want no request of a refused third-party REGISTER", m.Method, m.RequestURI)
	case <-time.After(50 * time.Millisecond):
	}
	h.noUpdate()
}

// A shut down IP-SM-GW refuses the MESSAGE of a short message that the
// IP-SM-GW of another IMS network sends to a user of this one (TS
// 23.204 6.4a step 2). Nothing of it has reached this instance, so
// another instance can take it whole.
func TestAShutDownIPSMGWRefusesAShortMessageFromAnotherNetwork(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	setState(t, node, maintenance.ShuttingDown)

	refusal(t, h.send(fromPeerNetwork(t)))
	select {
	case m := <-h.received:
		t.Errorf("the entry point got %s %s, want no delivery of a refused short message", m.Method, m.RequestURI)
	case <-time.After(50 * time.Millisecond):
	}
}

// fromPeerNetwork is the MESSAGE with which the IP-SM-GW of another IMS
// network delivers a short message to Bob, as the S-CSCF of this
// network passes it on by the filter criterion of the terminating case.
// It is the request of TestShortMessageFromAnotherNetworkIsDelivered,
// which an IP-SM-GW in service delivers.
func fromPeerNetwork(t *testing.T) *sip.Message {
	t.Helper()
	m := message(marshal(t, sms.RPDataNetworkToMS{MessageReference: 7,
		OriginatorAddress: sms.InternationalNumber("+12025550150"), UserData: marshal(t, deliver)}))
	m.RequestURI = bobTel
	m.Set("To", "<"+bobTel+">")
	m.Set("P-Asserted-Identity", "<sip:+12025550150@ims.mnc002.mcc001.3gppnetwork.org;user=phone>")
	m.Set("P-Served-User", "<"+bobTel+">;sescase=term;regstate=reg")
	return m
}

// A method that the IP-SM-GW does not take at all keeps its 405 while
// the IP-SM-GW is locked: the method is no more supported out of
// service than in it, and the next instance would answer the same.
func TestALockedIPSMGWAnswers405ToAMethodItDoesNotTake(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	setState(t, h.manage(), maintenance.Locked)

	m := message(nil)
	m.Method = sip.OPTIONS
	m.Set("CSeq", "1 OPTIONS")
	resp := h.send(m)
	if resp.StatusCode != 405 || resp.Get("Allow") == "" {
		t.Errorf("OPTIONS got %d with Allow %q, want 405 with Allow", resp.StatusCode, resp.Get("Allow"))
	}
}

// A shut down IP-SM-GW goes on delivering what the SC hands it: the SC
// of this node asks only the instance that began the work, so a
// delivery continues a short message this instance took from a UE, a
// delivery it ended, or a short message of another network it took.
func TestAShutDownIPSMGWDeliversWhatTheSCHandsIt(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	setState(t, node, maintenance.ShuttingDown)

	mt, data := h.delivered("ref-1")
	if mt.RequestURI != bobTel {
		t.Errorf("the MESSAGE went to %s, want %s", mt.RequestURI, bobTel)
	}
	report := deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference})
	if resp := h.send(report); resp.StatusCode != 202 {
		t.Fatalf("the delivery report got %d, want 202", resp.StatusCode)
	}
	if got := h.nextReport(); got.outcome != ipsmgw.Delivered {
		t.Errorf("the SC heard %+v, want delivered", got)
	}
}

// A shut down waits for the work the IP-SM-GW has in hand. A short
// message whose SC has not answered yet keeps it SHUTTINGDOWN, it
// refuses another short message meanwhile, and it becomes LOCKED once
// the submit report has gone out.
func TestAShutDownWaitsForTheWorkOfTheIPSMGW(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	node := h.manage()
	// The SC holds the short message until the test lets it answer, so
	// that the MESSAGE is in the IP-SM-GW's hands while the shut down
	// comes.
	answered := make(chan struct{})
	var answer sync.Once
	letAnswer := func() { answer.Do(func() { close(answered) }) }
	// A test that stops early still lets the SC answer, so that the
	// short message in its hands does not outlive the test.
	t.Cleanup(letAnswer)
	h.sc.mu.Lock()
	h.sc.answer = func() (*sms.SubmitReport, error) {
		<-answered
		return &sms.SubmitReport{ServiceCentreTimeStamp: time.Now()}, nil
	}
	h.sc.mu.Unlock()
	go h.send(message(submitBody(t, 9, scAddress, hello)))
	h.nextSubmit()
	waitFor(t, func() bool { return inHand(t, node) == 1 }, "the IP-SM-GW to count the short message as its user")

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a short message is in hand", f)
	}
	refusal(t, h.send(message(submitBody(t, 10, scAddress, hello))))
	if f := gwState(t, node); f.AdministrativeState != maintenance.ShuttingDown || f.Users == nil || *f.Users != 1 {
		t.Errorf("the IP-SM-GW is %+v after the refusal, want SHUTTINGDOWN with the one short message in hand", f)
	}

	letAnswer()
	if report := h.next(); report.Get("In-Reply-To") == "" {
		t.Errorf("the entry point got %s %s, want the submit report", report.Method, report.RequestURI)
	}
	waitFor(t, func() bool { return gwState(t, node).AdministrativeState == maintenance.Locked },
		"the IP-SM-GW to be LOCKED once the short message is done")
}

// The short message that a shut down instance refuses loses nothing:
// every IP-SM-GW of the network reads the same Store, so the S-CSCF
// sends the MESSAGE on to the next instance that DNS gave it for the
// name of the IP-SM-GW (RFC 3263 4.3), and that instance hands the
// short message to the SC and sends the submit report.
func TestAnotherInstanceTakesTheShortMessageThatAShutDownIPSMGWRefuses(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	first := newHarness(t, store, time.Minute, answerOK)
	second := newHarness(t, store, time.Minute, answerOK)
	setState(t, first.manage(), maintenance.ShuttingDown)

	mo := message(submitBody(t, 11, scAddress, hello))
	refusal(t, first.send(mo))
	if resp := second.send(mo); resp.StatusCode != 202 {
		t.Fatalf("the second instance answered %d, want 202", resp.StatusCode)
	}
	if got := second.nextSubmit(); got.submit.MessageReference != hello.MessageReference {
		t.Errorf("the SC of the second instance got %+v", got)
	}
	if report := second.next(); report.Get("In-Reply-To") != mo.Get("Call-ID") {
		t.Errorf("the second instance sent %s in reply to %q, want the submit report of the short message",
			report.Method, report.Get("In-Reply-To"))
	}
}

// The IP-SM-GW reports itself DISABLED while no instance of the HSS
// takes its Sh requests: it cannot write SMSRegistrationInfo for a user
// (TS 24.341 5.3.3.2), and the SC beside it cannot find which user has
// the number a short message is for.
func TestTheIPSMGWIsDisabledWithoutTheHSS(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	available := false
	node := h.manageWith(func() bool { return available })

	if f := gwState(t, node); f.OperationalState != maintenance.Disabled {
		t.Errorf("the IP-SM-GW is %s without the HSS, want DISABLED", f.OperationalState)
	}
	available = true
	if f := gwState(t, node); f.OperationalState != maintenance.Enabled {
		t.Errorf("the IP-SM-GW is %s with the HSS, want ENABLED", f.OperationalState)
	}
}

// The sweep that ends the deliveries whose TR1N has run out goes on
// while the IP-SM-GW drains: the SC hears the outcome of every
// delivery, and one whose delivery report never came has none until a
// sweep ends it.
func TestAShutDownIPSMGWStillEndsTheDeliveriesWhoseTR1NRanOut(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), 50*time.Millisecond, answerOK)
	node := h.manage()
	h.delivered("ref-1")
	setState(t, node, maintenance.ShuttingDown)

	if got := h.nextReport(); got.reference != "ref-1" || got.outcome != ipsmgw.SystemFailure {
		t.Errorf("the SC heard %+v, want a system failure for ref-1", got)
	}
	waitFor(t, func() bool { return gwState(t, node).AdministrativeState == maintenance.Locked },
		"the IP-SM-GW to be LOCKED once the expiry it claimed is done")
}

// A shut down waits for a delivery that the SC has just handed over,
// before anything of it has gone out: a delivery is a user of the
// IP-SM-GW from the hand-over itself, not from the moment its goroutine
// runs (Deliver), so the operator never stops a node that has a short
// message in hand and has yet to send it. The shut down here comes
// without waiting for the MESSAGE, so it falls in that moment. Nothing
// answers the MESSAGE, so the delivery ends when the transaction times
// out, and the SC hears the outcome before the IP-SM-GW is LOCKED.
func TestAShutDownWaitsForADeliveryJustHandedOver(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, func(*sip.Message) int { return 0 })
	node := h.manage()
	h.g.Deliver(ipsmgw.Delivery{Reference: "ref-1", Recipient: bobTel, TPDU: deliver})

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN: the delivery was handed over before it", f)
	}
	// No final response is a 408 to TS 29.311 Table 6.1.4.4.1.1.
	if got := h.nextReport(); got.reference != "ref-1" || got.outcome != ipsmgw.SystemFailure {
		t.Errorf("the SC heard %+v, want a system failure for ref-1", got)
	}
	waitFor(t, func() bool { return gwState(t, node).AdministrativeState == maintenance.Locked },
		"the IP-SM-GW to be LOCKED once the delivery is done")
}

// A shut down waits for a delivery that is in flight: the MESSAGE that
// carries the short message is a user of the IP-SM-GW until it has its
// final response, so that the operator does not stop a node whose
// RP-DATA has gone out with nothing back yet. Here nothing answers the
// MESSAGE, so the delivery ends when the transaction times out, and the
// SC hears the outcome before the IP-SM-GW is LOCKED.
func TestAShutDownWaitsForADeliveryInFlight(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, func(*sip.Message) int { return 0 })
	node := h.manage()
	h.g.Deliver(ipsmgw.Delivery{Reference: "ref-1", Recipient: bobTel, TPDU: deliver})
	h.next()
	waitFor(t, func() bool { return inHand(t, node) == 1 }, "the IP-SM-GW to count the delivery as its user")

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("shut down gave %+v, want SHUTTINGDOWN while a delivery is in flight", f)
	}
	// No final response is a 408 to TS 29.311 Table 6.1.4.4.1.1.
	if got := h.nextReport(); got.reference != "ref-1" || got.outcome != ipsmgw.SystemFailure {
		t.Errorf("the SC heard %+v, want a system failure for ref-1", got)
	}
	waitFor(t, func() bool { return gwState(t, node).AdministrativeState == maintenance.Locked },
		"the IP-SM-GW to be LOCKED once the delivery is done")
}
