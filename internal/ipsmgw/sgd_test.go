package ipsmgw_test

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sms"
)

// This file is the IP-SM-GW as the serving node of SGd: an SMS-GMSC
// sends it a short message for one of its users in a TFR, and the TFA
// says how the delivery ended (TS 29.338 6.2.2). The SC side is the
// client of package sgd, the one an SMS-GMSC uses, on a real Diameter
// connection: the IP-SM-GW's node opens it, as the serving node does,
// and the TFR comes back over it (TS 29.338 6.3.1.2).

// scNumber is the E.164 number of the SC in the SC-Address of a TFR,
// digits only (TS 29.338 6.3.3.2).
const scNumber = "12025550100"

// sgdTimers open the connection quickly and give an answer longer than
// a delivery takes, as TS 23.204 6.5a NOTE 1 has the timer of the
// SMS-GMSC set.
var sgdTimers = diameter.Timers{Watchdog: time.Minute, Reconnect: 20 * time.Millisecond, Answer: 30 * time.Second}

// serviceCentre is the Diameter node of an SC beside the IP-SM-GW of h,
// with the client that an SMS-GMSC sends its TFRs with.
type serviceCentre struct {
	*sgd.SGdClient
	t *testing.T
}

// overSGd gives h an SC that reaches it over SGd. The IP-SM-GW's node
// connects to the SC, which is the end that listens because the serving
// node is also the one that brings mobile originated short messages (TS
// 29.338 6.3.1.1).
func (h *harness) overSGd() serviceCentre {
	h.t.Helper()
	if err := sgd.LoadDictionary(); err != nil {
		h.t.Fatal(err)
	}
	scID := diameter.Identity{OriginHost: scDiameterHost, OriginRealm: domain}
	scNode := diameter.NewNode(scID, []uint32{sgd.ApplicationIDSGd}, sgdTimers, quiet())
	server, err := scNode.Listen("127.0.0.1:0")
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(server.Stop)

	gwNode := diameter.NewNode(diameter.Identity{OriginHost: gwDiameterHost, OriginRealm: domain},
		[]uint32{sgd.ApplicationIDSGd}, sgdTimers, quiet())
	gwNode.Handle(h.g.Handle)
	peer := gwNode.Connect(diameter.PeerConfig{Address: server.Addr(), OriginHost: scID.OriginHost})
	h.t.Cleanup(peer.Stop)

	sc := serviceCentre{SGdClient: sgd.NewSGdClient(scNode, server, 30*time.Second), t: h.t}
	// The connection opens in the background. A TFR for an IMSI no user
	// has is answered without any delivery, so it is a cheap way to wait
	// until one would get through.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := sc.forward("000000000000000", []byte{0x00})
		if err == nil {
			return sc
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("the SGd connection to the IP-SM-GW never opened: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// forward sends one TFR and waits for the TFA.
func (s serviceCentre) forward(imsi string, tpdu []byte) (sgd.MTForwardShortMessageAnswer, error) {
	return s.MTForwardShortMessage(context.Background(), sgd.MTForwardShortMessageRequest{
		Header:    diameter.Header{DestinationHost: gwDiameterHost, DestinationRealm: domain},
		UserName:  imsi,
		SCAddress: scNumber,
		SMRPUI:    tpdu,
	})
}

// answered is the TFA of a TFR that the test sent, or the reason none
// came.
type answered struct {
	tfa sgd.MTForwardShortMessageAnswer
	err error
}

// forwarding sends a TFR for Alice on a goroutine of its own and hands
// the TFA back, so that the test can answer the MESSAGE that the
// IP-SM-GW sends while the TFR waits for the delivery report.
func (s serviceCentre) forwarding(t *testing.T, tpdu sms.TPDU) <-chan answered {
	t.Helper()
	done := make(chan answered, 1)
	go func() {
		tfa, err := s.forward(aliceIMSI, marshal(t, tpdu))
		done <- answered{tfa, err}
	}()
	return done
}

// nextTFA waits for the answer to a TFR that forwarding sent.
func nextTFA(t *testing.T, done <-chan answered) sgd.MTForwardShortMessageAnswer {
	t.Helper()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("no TFA: %v", got.err)
		}
		return got.tfa
	case <-time.After(10 * time.Second):
		t.Fatal("the SC got no answer to its TFR")
		return sgd.MTForwardShortMessageAnswer{}
	}
}

// registered runs the registration of Alice through the IP-SM-GW, so
// that it holds her IMSI and knows that she takes short messages over IP
// (regevent.go). It returns the SUBSCRIBE, which a later NOTIFY of the
// test answers within.
func (h *harness) registered() *sip.Message {
	h.t.Helper()
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		h.t.Fatalf("PUR = %+v, want %+v", got, registered)
	}
	return subscribe
}

var success = diameter.Base(diameter.DiameterSuccess)

// TestMTForwardShortMessage is TS 29.338 6.2.2 with TS 23.204 6.4: the
// short message of a TFR reaches the UE in a MESSAGE, and the TFA
// follows the UE's delivery report rather than the 200 to that MESSAGE
// (6.4 NOTE 4).
func TestMTForwardShortMessage(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.registered()
	sc := h.overSGd()

	done := sc.forwarding(t, deliver)
	mt := h.next()
	// TS 23.204 6.4 step 6: the tel URI of the IMSI the TFR named.
	if mt.RequestURI != aliceTel {
		t.Errorf("Request-URI = %s, want %s", mt.RequestURI, aliceTel)
	}
	data, ok := parseRP(h.t, mt).(sms.RPDataNetworkToMS)
	if !ok {
		t.Fatalf("MESSAGE carries %+v, want RP-DATA", parseRP(h.t, mt))
	}
	if data.OriginatorAddress != sms.InternationalNumber("+"+scNumber) {
		t.Errorf("RP-OA = %v, want the SC of the TFR", data.OriginatorAddress)
	}
	tpdu, err := sms.ParseTPDU(data)
	if got, ok := tpdu.(sms.Deliver); err != nil || !ok || got.OriginatingAddress != deliver.OriginatingAddress {
		t.Errorf("the UE got %+v, %v; want the SMS-DELIVER of the TFR", tpdu, err)
	}
	select {
	case got := <-done:
		t.Fatalf("the TFA came before the delivery report: %+v", got)
	case <-time.After(100 * time.Millisecond):
	}

	h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		UserData: marshal(t, sms.DeliverReport{})}))
	tfa := nextTFA(t, done)
	if tfa.Result != success || tfa.SMDeliveryFailureCause != nil {
		t.Errorf("TFA = %+v, want DIAMETER_SUCCESS alone", tfa)
	}
	// The outcome went to the TFA, not to the SC stub of this node.
	select {
	case got := <-h.sc.reports:
		t.Errorf("the SC stub heard %+v of a short message that came over SGd", got)
	case <-time.After(50 * time.Millisecond):
	}
}

// outcomesThatFail is a Store whose reads of an outcome fail a number of
// times before they succeed, as a store that is briefly out of reach.
type outcomesThatFail struct {
	ipsmgw.Store
	fails atomic.Int32
}

func (s *outcomesThatFail) TakeOutcome(ctx context.Context, reference string) (ipsmgw.Outcome, bool, error) {
	if s.fails.Add(-1) >= 0 {
		return 0, false, errUnreachable
	}
	return s.Store.TakeOutcome(ctx, reference)
}

// A read of the outcome that fails is tried again: the delivery goes on
// meanwhile, and the TFA says how it ended.
func TestMTForwardShortMessageWhenAnOutcomeReadFails(t *testing.T) {
	store := &outcomesThatFail{Store: ipsmgw.NewMemoryStore()}
	store.fails.Store(3)
	h := newHarness(t, store, time.Minute, answerOK)
	h.registered()
	sc := h.overSGd()

	done := sc.forwarding(t, deliver)
	mt := h.next()
	data, ok := parseRP(h.t, mt).(sms.RPDataNetworkToMS)
	if !ok {
		t.Fatalf("MESSAGE carries %+v, want RP-DATA", parseRP(h.t, mt))
	}
	h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference,
		UserData: marshal(t, sms.DeliverReport{})}))
	if tfa := nextTFA(t, done); tfa.Result != success {
		t.Errorf("TFA = %+v, want DIAMETER_SUCCESS", tfa)
	}
	if left := store.fails.Load(); left >= 0 {
		t.Errorf("%d failed reads left, want all of them used", left+1)
	}
}

// TestMTForwardShortMessageFailures is the mapping of TS 29.338
// 6.2.2.1 Table 6.2.2.1/2 and 7.3: what the UE or its network answers
// becomes the result of the TFA. The SIP status codes are those of TS
// 29.311 Table 6.1.4.4.1.1 (servicecentre.go).
func TestMTForwardShortMessageFailures(t *testing.T) {
	memoryFull := &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: sgd.MemoryCapacityExceeded}
	ueError := &sgd.SMDeliveryFailureCause{SMEnumeratedDeliveryFailureCause: sgd.EquipmentProtocolError}
	tests := []struct {
		name string
		// status is what the UE's network answers the MESSAGE with; 200
		// means that the UE answers it and reports with report.
		status int
		report func(mr uint8) sms.RPMessage
		want   diameter.Result
		cause  *sgd.SMDeliveryFailureCause
		// notReachable is whether the IP-SM-GW marks the user unreachable
		// for short messages over IP (TS 23.204 6.5a).
		notReachable bool
	}{
		{name: "the UE has no memory", status: 200, want: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), cause: memoryFull,
			report: func(mr uint8) sms.RPMessage {
				return sms.RPError{Direction: sms.MSToNetwork, MessageReference: mr, Cause: sms.RPCauseMemoryCapacityExceeded}
			}},
		{name: "the UE answers another RP-Cause", status: 200, want: diameter.Experimental(sgd.DiameterErrorSMDeliveryFailure), cause: ueError,
			report: func(mr uint8) sms.RPMessage {
				return sms.RPError{Direction: sms.MSToNetwork, MessageReference: mr, Cause: sms.RPCauseProtocolErrorUnspecified}
			}},
		{name: "the UE is not there", status: 480, want: diameter.Experimental(sgd.DiameterErrorAbsentUser), notReachable: true},
		{name: "the UE is busy", status: 486, want: diameter.Experimental(sgd.DiameterErrorUserBusyForMTSMS)},
		{name: "no such user", status: 404, want: diameter.Experimental(sgd.DiameterErrorUserUnknown)},
		{name: "the UE failed authentication", status: 401, want: diameter.Experimental(sgd.DiameterErrorIllegalUser)},
		{name: "the network cannot say", status: 500, want: diameter.Base(diameter.DiameterUnableToComply)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := ipsmgw.NewMemoryStore()
			// The status is for the MESSAGE of the short message; the
			// SUBSCRIBE of the registration is accepted whatever it is.
			h := newHarness(t, store, time.Minute, func(m *sip.Message) int {
				if m.Method == sip.MESSAGE {
					return tt.status
				}
				return 200
			})
			h.registered()
			sc := h.overSGd()

			done := sc.forwarding(t, deliver)
			mt := h.next()
			if tt.report != nil {
				data := parseRP(h.t, mt).(sms.RPDataNetworkToMS)
				h.send(deliveryReport(t, mt, tt.report(data.MessageReference)))
			}
			tfa := nextTFA(t, done)
			if tfa.Result != tt.want {
				t.Errorf("TFA result = %v, want %v", tfa.Result, tt.want)
			}
			if !sameFailureCause(tfa.SMDeliveryFailureCause, tt.cause) {
				t.Errorf("SM-Delivery-Failure-Cause = %+v, want %+v", tfa.SMDeliveryFailureCause, tt.cause)
			}
			set, err := store.TakeNotReachable(t.Context(), alice)
			if err != nil || set != tt.notReachable {
				t.Errorf("the user is marked unreachable: %v, %v; want %v", set, err, tt.notReachable)
			}
		})
	}
}

func sameFailureCause(got, want *sgd.SMDeliveryFailureCause) bool {
	if got == nil || want == nil {
		return got == want
	}
	return got.SMEnumeratedDeliveryFailureCause == want.SMEnumeratedDeliveryFailureCause
}

// TestMTForwardShortMessageForAUserItDoesNotServe: the IP-SM-GW answers
// for an IMSI it holds no user for as a serving node that does not know
// the IMSI does (TS 29.338 6.2.2.2), and sends no MESSAGE.
func TestMTForwardShortMessageForAUserItDoesNotServe(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.registered()
	sc := h.overSGd()

	tfa, err := sc.forward("001010000000009", marshal(t, deliver))
	if err != nil {
		t.Fatal(err)
	}
	if want := diameter.Experimental(sgd.DiameterErrorUserUnknown); tfa.Result != want {
		t.Errorf("TFA result = %v, want %v", tfa.Result, want)
	}
	select {
	case m := <-h.received:
		t.Errorf("the IP-SM-GW sent %s for a user it does not serve", m.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestMTForwardShortMessageForAUserWhoTakesNone is TS 23.204 6.5b: a UE
// that is registered without the SMS over IP capability takes no short
// message, so the SC hears that the user is absent and the IP-SM-GW
// marks the user unreachable, without trying a MESSAGE.
func TestMTForwardShortMessageForAUserWhoTakesNone(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	subscribe := h.registered()
	sc := h.overSGd()

	// A NOTIFY says that Alice no longer has a contact that takes short
	// messages, so the IP-SM-GW withdraws its registration and keeps a
	// subscription that says she takes none.
	h.send(notify(subscribe, 2, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active)))
	if got := h.nextUpdate(); got != unregistered {
		t.Fatalf("PUR = %+v, want %+v", got, unregistered)
	}

	tfa, err := sc.forward(aliceIMSI, marshal(t, deliver))
	if err != nil {
		t.Fatal(err)
	}
	if want := diameter.Experimental(sgd.DiameterErrorAbsentUser); tfa.Result != want {
		t.Errorf("TFA result = %v, want %v", tfa.Result, want)
	}
	if set, err := store.TakeNotReachable(t.Context(), alice); err != nil || !set {
		t.Errorf("the user is not marked unreachable: %v, %v", set, err)
	}
	select {
	case m := <-h.received:
		t.Errorf("the IP-SM-GW sent %s to a user who takes no short message over IP", m.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestMTForwardShortMessageWhileNotUnlocked: an IP-SM-GW that the
// operator has out of service refuses a TFR with DIAMETER_TOO_BUSY, so
// that the SMS-GMSC takes the short message to another instance (RFC
// 6733 7.1.3). Nothing of the delivery has begun.
func TestMTForwardShortMessageWhileNotUnlocked(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.registered()
	sc := h.overSGd()
	node := h.manage()
	setState(t, node, maintenance.ShuttingDown)

	tfa, err := sc.forward(aliceIMSI, marshal(t, deliver))
	if err != nil {
		t.Fatal(err)
	}
	if want := diameter.Base(diameter.DiameterTooBusy); tfa.Result != want {
		t.Errorf("TFA result = %v, want %v", tfa.Result, want)
	}
	select {
	case m := <-h.received:
		t.Errorf("the IP-SM-GW sent %s while it was out of service", m.Method)
	case <-time.After(50 * time.Millisecond):
	}

	setState(t, node, maintenance.Unlocked)
	done := sc.forwarding(t, deliver)
	mt := h.next()
	data := parseRP(h.t, mt).(sms.RPDataNetworkToMS)
	h.send(deliveryReport(t, mt, sms.RPAck{Direction: sms.MSToNetwork, MessageReference: data.MessageReference}))
	if tfa := nextTFA(t, done); tfa.Result != success {
		t.Errorf("TFA after the unlock = %+v, want DIAMETER_SUCCESS", tfa)
	}
}

// TestSGdCommandThatIsNotATFR: a command the IP-SM-GW does not take gets
// the protocol error of RFC 6733 7.2, as it would on a node without this
// role.
func TestSGdCommandThatIsNotATFR(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	if err := sgd.LoadDictionary(); err != nil {
		t.Fatal(err)
	}
	ofr := sgd.MOForwardShortMessageRequest{
		Header:         diameter.Header{SessionID: "sc;1", OriginHost: scDiameterHost, OriginRealm: domain, DestinationRealm: domain},
		SCAddress:      scNumber,
		UserIdentifier: sgd.UserIdentifier{UserName: aliceIMSI},
		SMRPUI:         []byte{0x00},
	}.Message()
	ofa := h.g.Handle(ofr)
	var raw struct {
		ResultCode uint32 `avp:"Result-Code"`
	}
	if err := ofa.Unmarshal(&raw); err != nil {
		t.Fatal(err)
	}
	if raw.ResultCode != diameter.DiameterCommandUnsupported || ofa.Header.CommandFlags&diam.ErrorFlag == 0 {
		t.Errorf("answer to an OFR = Result-Code %d, flags %#02x", raw.ResultCode, ofa.Header.CommandFlags)
	}
}

// flagStore is a MemoryStore that hands the test, after each
// PutNotReachable, the time until which it was asked to keep the flag.
type flagStore struct {
	*ipsmgw.MemoryStore
	until chan time.Time
	// beforePut, when set, runs once, before the first flag goes in.
	beforePut *func()
}

func (s flagStore) PutNotReachable(ctx context.Context, publicID string, expires time.Time) error {
	if s.beforePut != nil && *s.beforePut != nil {
		f := *s.beforePut
		*s.beforePut = nil
		f()
	}
	err := s.MemoryStore.PutNotReachable(ctx, publicID, expires)
	s.until <- expires
	return err
}

// nextFlag is the expiry of the next flag that the IP-SM-GW put.
func (s flagStore) nextFlag(t *testing.T) time.Time {
	t.Helper()
	select {
	case until := <-s.until:
		return until
	case <-time.After(5 * time.Second):
		t.Fatal("the IP-SM-GW put no unreachable flag")
		return time.Time{}
	}
}

// subscriptionExpires is when the store forgets Alice's subscription.
func (s flagStore) subscriptionExpires(t *testing.T) time.Time {
	t.Helper()
	sub, ok, err := s.RegSubscriptionOf(t.Context(), alice)
	if err != nil || !ok {
		t.Fatalf("RegSubscriptionOf(%s) = %v, %v", alice, ok, err)
	}
	return sub.Expires
}

// TestTheUnreachableFlagOutlastsARefreshThatCameBeforeIt: a
// reregistration refreshes the subscription after the delivery read it
// and before the 480 puts the flag. That reregistration found no flag,
// so the flag lasts as long as the refreshed subscription, for the
// reregistration after it.
func TestTheUnreachableFlagOutlastsARefreshThatCameBeforeIt(t *testing.T) {
	var beforePut func()
	store := flagStore{MemoryStore: ipsmgw.NewMemoryStore(), until: make(chan time.Time, 4), beforePut: &beforePut}
	h := newHarness(t, store, time.Minute, func(m *sip.Message) int {
		if m.Method == sip.MESSAGE {
			return 480
		}
		return 200
	})
	h.registered()
	sc := h.overSGd()
	refreshed := time.Now().Add(100 * time.Hour).Round(time.Second)
	beforePut = func() {
		sub, ok, err := store.RegSubscriptionOf(context.Background(), alice)
		if err != nil || !ok {
			t.Errorf("RegSubscriptionOf(%s) = %v, %v", alice, ok, err)
			return
		}
		sub.Expires = refreshed
		if err := store.PutRegSubscription(context.Background(), sub); err != nil {
			t.Error(err)
		}
	}
	if tfa := nextTFA(t, sc.forwarding(t, deliver)); tfa.Result != diameter.Experimental(sgd.DiameterErrorAbsentUser) {
		t.Fatalf("TFA result = %v, want DIAMETER_ERROR_ABSENT_USER", tfa.Result)
	}
	if first := store.nextFlag(t); !first.Before(refreshed) {
		t.Fatalf("the first flag goes at %v, want the expiry of the subscription before its refresh", first)
	}
	if again := store.nextFlag(t); !again.Equal(refreshed) {
		t.Errorf("the flag goes at %v, want %v, when the refreshed subscription goes", again, refreshed)
	}
}

// TestTheUnreachableFlagLastsAsLongAsTheSubscription: the flag that a
// 480 sets is what makes a reregistration of the user tell the HSS that
// the user is back (regevent.go), and the HSS keeps its own UNRI until
// it hears so, however long that takes (TS 23.040 3.2.6). A flag that
// went before the reregistration came would leave the HSS with UNRI for
// good, so the flag stays for as long as the subscription to the user's
// registration state, whose refresh that reregistration is. The
// registration here is one of 600000 seconds, longer than three days.
func TestTheUnreachableFlagLastsAsLongAsTheSubscription(t *testing.T) {
	store := flagStore{MemoryStore: ipsmgw.NewMemoryStore(), until: make(chan time.Time, 4)}
	h := newHarness(t, store, time.Minute, func(m *sip.Message) int {
		if m.Method == sip.MESSAGE {
			return 480
		}
		return 200
	})
	const registration = 600000
	h.thirdPartyRegister(registration)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires="+strconv.Itoa(registration+161), regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Fatalf("PUR = %+v, want %+v", got, registered)
	}
	sc := h.overSGd()

	if tfa := nextTFA(t, sc.forwarding(t, deliver)); tfa.Result != diameter.Experimental(sgd.DiameterErrorAbsentUser) {
		t.Fatalf("TFA result = %v, want DIAMETER_ERROR_ABSENT_USER", tfa.Result)
	}
	if until, subscription := store.nextFlag(t), store.subscriptionExpires(t); until.Before(subscription) {
		t.Errorf("the flag of the 480 goes at %v, before the subscription at %v", until, subscription)
	}

	// The HSS refuses the notification of the reregistration, so the flag
	// goes back, for as long as the subscription that the reregistration
	// refreshed.
	refused := errors.New("DIAMETER_UNABLE_TO_COMPLY")
	h.hss.notifyFails.Store(&refused)
	h.thirdPartyRegister(registration)
	if until, subscription := store.nextFlag(t), store.subscriptionExpires(t); until.Before(subscription) {
		t.Errorf("the flag put back goes at %v, before the refreshed subscription at %v", until, subscription)
	}
	h.hss.notifyFails.Store(nil)
	h.thirdPartyRegister(registration)
	want := notification{sh.UserIdentity{MSISDN: "12025550101"}, number, sh.MSPresent}
	if got := h.nextNotification(); got != want {
		t.Errorf("notification = %+v, want %+v", got, want)
	}
}
