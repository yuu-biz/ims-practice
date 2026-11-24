package ipsmgw_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/ipsmgw"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// thirdPartyRegister sends the IP-SM-GW the REGISTER of TS 24.229
// 5.4.1.7 for Alice, with her MSISDN in service-info unless it is a
// deregistration, and returns the answer.
func (h *harness) thirdPartyRegister(expires int) *sip.Message {
	h.t.Helper()
	m := &sip.Message{Method: sip.REGISTER, RequestURI: ownURI}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<sip:"+scscfHost+":5060>;tag="+sip.NewTag())
	m.Add("To", "<"+alice+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:"+scscfHost+":5060>")
	m.Add("Expires", strconv.Itoa(expires))
	if expires > 0 {
		m.Add("Content-Type", ims.IMS3GPPContentType)
		m.Body = ims.ServiceInfoBody("12025550101")
	}
	return h.send(m)
}

// subscribed is the SUBSCRIBE that a third-party REGISTER made the
// IP-SM-GW send to the entry point.
func (h *harness) subscribed() *sip.Message {
	h.t.Helper()
	m := h.next()
	if m.Method != sip.SUBSCRIBE {
		h.t.Fatalf("got %s at the entry point, want SUBSCRIBE", m.Method)
	}
	return m
}

// regDoc is the full state of Alice's registration as the S-CSCF
// reports it, with her contact in contactState and the parameters
// given (TS 24.229 5.4.2.1.2).
func regDoc(state, contactState reginfo.State, params ...string) []byte {
	c := reginfo.Contact{ID: "c1", State: contactState, Event: reginfo.Registered, URI: "sip:001010000000001@192.0.2.10:5060"}
	if contactState == reginfo.Terminated {
		c.Event = reginfo.Unregistered
	}
	for _, p := range params {
		c.UnknownParams = append(c.UnknownParams, reginfo.UnknownParam{Name: p})
	}
	doc := reginfo.Document{State: reginfo.Full, Registrations: []reginfo.Registration{
		{AOR: alice, ID: "reg1", State: state, Contacts: []reginfo.Contact{c}},
		{AOR: aliceTel, ID: "reg2", State: state, Contacts: []reginfo.Contact{c}},
	}}
	b, err := doc.Marshal()
	if err != nil {
		panic(err)
	}
	return b
}

// notify is the NOTIFY number cseq of the S-CSCF for the subscription
// of subscribe, as the S-CSCF builds it (TS 24.229 5.4.2.1.2).
func notify(subscribe *sip.Message, cseq int, subscriptionState string, body []byte) *sip.Message {
	contact, _ := sip.ParseNameAddr(subscribe.Get("Contact"))
	m := &sip.Message{Method: sip.NOTIFY, RequestURI: contact.URI}
	m.Add("Max-Forwards", "70")
	m.Add("From", subscribe.Get("To")+";tag=scscf-tag")
	m.Add("To", subscribe.Get("From"))
	m.Add("Call-ID", subscribe.Get("Call-ID"))
	m.Add("CSeq", strconv.Itoa(cseq)+" NOTIFY")
	m.Add("Event", "reg")
	m.Add("Subscription-State", subscriptionState)
	m.Add("Contact", "<sip:"+scscfHost+":5060>")
	icid, _, _ := strings.Cut(subscribe.Get("P-Charging-Vector"), ";")
	m.Add("P-Charging-Vector", icid+";orig-ioi="+domain)
	m.Add("Content-Type", reginfo.ContentType)
	m.Body = body
	return m
}

// noUpdate checks that the HSS heard nothing.
func (h *harness) noUpdate() {
	h.t.Helper()
	select {
	case u := <-h.hss.updates:
		h.t.Errorf("HSS updated with %+v, want no update", u)
	case <-time.After(50 * time.Millisecond):
	}
}

func (h *harness) nextUpdate() update {
	h.t.Helper()
	select {
	case u := <-h.hss.updates:
		return u
	case <-time.After(5 * time.Second):
		h.t.Fatal("the HSS heard of no update")
		return update{}
	}
}

var (
	registered   = update{sh.UserIdentity{MSISDN: "12025550101"}, number}
	unregistered = update{sh.UserIdentity{MSISDN: "12025550101"}, ""}
)

// TestThirdPartyRegisterSubscribesToTheRegistrationState is TS 24.341
// 5.3.3.2 with TS 24.229 5.7.1.1: a 200 for the REGISTER, a SUBSCRIBE
// to Alice's registration state, and a PUR only when a NOTIFY says
// that she has a contact that takes short messages, or no longer has
// one.
func TestThirdPartyRegisterSubscribesToTheRegistrationState(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	resp := h.thirdPartyRegister(3600)
	if resp.StatusCode != 200 || resp.Get("Contact") != "<sip:"+scscfHost+":5060>;expires=3600" {
		t.Errorf("answer %d with Contact %q", resp.StatusCode, resp.Get("Contact"))
	}
	subscribe := h.subscribed()
	if subscribe.RequestURI != alice {
		t.Errorf("Request-URI = %s, want %s", subscribe.RequestURI, alice)
	}
	from, _ := sip.ParseNameAddr(subscribe.Get("From"))
	if _, tagged := from.Params.Get("tag"); from.URI != ownURI || !tagged {
		t.Errorf("From = %q, want the IP-SM-GW's URI with a tag", subscribe.Get("From"))
	}
	checkHeaders(t, subscribe, map[string]string{
		"To":                  "<" + alice + ">",
		"Event":               "reg",
		"Expires":             "3761", // the registration and the margin of RFC 3680 4.4
		"P-Asserted-Identity": "<" + ownURI + ">",
		"Contact":             "<" + ownURI + ">",
		"CSeq":                "1 SUBSCRIBE",
	})
	if pcv := subscribe.Get("P-Charging-Vector"); !strings.HasPrefix(pcv, "icid-value=") || !strings.HasSuffix(pcv, ";orig-ioi="+domain) {
		t.Errorf("P-Charging-Vector = %q, want an icid-value and orig-ioi=%s", pcv, domain)
	}
	h.noUpdate()

	n := notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))
	resp = h.send(n)
	if resp.StatusCode != 200 || resp.Get("P-Charging-Vector") != n.Get("P-Charging-Vector")+";term-ioi="+domain {
		t.Errorf("NOTIFY answered %d with P-Charging-Vector %q", resp.StatusCode, resp.Get("P-Charging-Vector"))
	}
	if got := h.nextUpdate(); got != registered {
		t.Errorf("PUR = %+v, want %+v", got, registered)
	}

	// The same state again, as after a reregistration: nothing to tell.
	if resp := h.send(notify(subscribe, 2, "active;expires=3000", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))); resp.StatusCode != 200 {
		t.Errorf("second NOTIFY answered %d", resp.StatusCode)
	}
	h.noUpdate()

	// Deregistration: the third-party REGISTER is answered, and the
	// NOTIFY that ends the subscription withdraws the registration.
	if resp := h.thirdPartyRegister(0); resp.StatusCode != 200 || resp.Get("Contact") != "" {
		t.Errorf("deregistration answered %d with Contact %q", resp.StatusCode, resp.Get("Contact"))
	}
	h.noUpdate()
	if resp := h.send(notify(subscribe, 3, "terminated;reason=noresource", regDoc(reginfo.Terminated, reginfo.Terminated))); resp.StatusCode != 200 {
		t.Errorf("last NOTIFY answered %d", resp.StatusCode)
	}
	if got := h.nextUpdate(); got != unregistered {
		t.Errorf("PUR = %+v, want %+v", got, unregistered)
	}
	if resp := h.send(notify(subscribe, 4, "active;expires=100", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))); resp.StatusCode != 481 {
		t.Errorf("NOTIFY after the end answered %d, want 481", resp.StatusCode)
	}
	h.noUpdate()
}

// TestRegistrationKeepsTheIMSI: before it registers itself for a user,
// the IP-SM-GW reads the user's IMSI over Sh (TS 29.328 7.6.24) and
// keeps it, so that a TFR, which names the recipient of a short message
// by IMSI, finds the user (TS 29.338 Table 6.2.2.1/1). The
// deregistration takes the subscription and the IMSI with it.
func TestRegistrationKeepsTheIMSI(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	// The HSS may name this IP-SM-GW to an SMS-GMSC as soon as it has the
	// PUR, so the IMSI has to find a user who takes short messages by
	// then: a TFR that crossed the answer would otherwise be told that
	// the user is unknown, or absent.
	foundAtPUR := make(chan bool, 2)
	hook := func() {
		sub, ok, err := store.RegSubscriptionOfIMSI(t.Context(), aliceIMSI)
		foundAtPUR <- ok && err == nil && sub.Available
	}
	h.hss.onUpdate.Store(&hook)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Fatalf("PUR = %+v, want %+v", got, registered)
	}
	sub, ok, err := store.RegSubscriptionOfIMSI(t.Context(), aliceIMSI)
	if err != nil || !ok {
		t.Fatalf("RegSubscriptionOfIMSI(%s) = %v, %v", aliceIMSI, ok, err)
	}
	if sub.PublicIdentity != alice || sub.MSISDN != "12025550101" || !sub.Available {
		t.Errorf("the subscription found by IMSI = %+v", sub)
	}
	if !<-foundAtPUR {
		t.Error("the IMSI found no user who takes short messages while the PUR was with the HSS")
	}
	h.hss.onUpdate.Store(nil)

	h.send(notify(subscribe, 2, "terminated;reason=noresource", regDoc(reginfo.Terminated, reginfo.Terminated)))
	if got := h.nextUpdate(); got != unregistered {
		t.Fatalf("PUR = %+v, want %+v", got, unregistered)
	}
	if _, ok, err := store.RegSubscriptionOfIMSI(t.Context(), aliceIMSI); err != nil || ok {
		t.Errorf("RegSubscriptionOfIMSI after the deregistration = %v, %v", ok, err)
	}
}

// A PUR that the HSS refuses takes back what the IP-SM-GW wrote before
// it: the user takes no short messages through this IP-SM-GW, and the
// next NOTIFY tells the HSS again.
func TestARegistrationThatFailsIsTakenBack(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	refused := errors.New("DIAMETER_UNABLE_TO_COMPLY")
	h.hss.updateFails.Store(&refused)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Fatalf("PUR = %+v, want %+v", got, registered)
	}
	// The PUR has failed once its update is out; the take back follows.
	deadline := time.Now().Add(5 * time.Second)
	for {
		sub, ok, err := store.RegSubscriptionOfIMSI(t.Context(), aliceIMSI)
		if err != nil {
			t.Fatal(err)
		}
		if ok && !sub.Available {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscription found by IMSI = %+v, %v; want it kept, taking no short messages", sub, ok)
		}
		time.Sleep(10 * time.Millisecond)
	}

	h.hss.updateFails.Store(nil)
	h.send(notify(subscribe, 2, "active;expires=3600", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Fatalf("PUR after the NOTIFY = %+v, want %+v", got, registered)
	}
}

// TestTheReturnOfAUserItCouldNotReach is TS 23.204 6.5b step 2 with TS
// 23.040 3.2.6 item 2c: a short message did not reach Alice, so the
// IP-SM-GW holds its own flag for her; her next MESSAGE tells the HSS
// that she can take short messages again, and the one after it tells it
// nothing, since the flag is gone.
func TestTheReturnOfAUserItCouldNotReach(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	h.registered()
	if err := store.PutNotReachable(t.Context(), alice, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	h.send(message(submitBody(t, 1, scAddress, hello)))
	want := notification{sh.UserIdentity{MSISDN: "12025550101"}, number, sh.MSPresent}
	if got := h.nextNotification(); got != want {
		t.Errorf("notification = %+v, want %+v", got, want)
	}

	h.send(message(submitBody(t, 2, scAddress, hello)))
	select {
	case got := <-h.hss.notifications:
		t.Errorf("the HSS heard %+v of a user whose flag was gone", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestTheReturnOfAUserWhoIsNotRegistered is TS 23.204 6.5b step 2d: the
// IP-SM-GW discards the news where the UE is not registered for short
// messages over IP, since a short message would not get through now
// either. The flag stays, for a return that counts.
func TestTheReturnOfAUserWhoIsNotRegistered(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	if err := store.PutNotReachable(t.Context(), alice, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	h.send(message(submitBody(t, 1, scAddress, hello)))
	select {
	case got := <-h.hss.notifications:
		t.Errorf("the HSS heard %+v of a user who is not registered", got)
	case <-time.After(100 * time.Millisecond):
	}
	if set, err := store.TakeNotReachable(t.Context(), alice); err != nil || !set {
		t.Errorf("the flag of the user = %v, %v; want it still set", set, err)
	}
}

// TestTheRegistrationOfAUserItCouldNotReach is TS 23.040 3.2.6 item 2c:
// the PUR that registers the IP-SM-GW for the user is itself the news
// that the user is reachable, so it clears the IP-SM-GW's own flag and
// no notification goes beside it. A later MESSAGE then tells the HSS
// nothing.
func TestTheRegistrationOfAUserItCouldNotReach(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	h := newHarness(t, store, time.Minute, answerOK)
	if err := store.PutNotReachable(t.Context(), alice, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	h.registered()

	if set, err := store.TakeNotReachable(t.Context(), alice); err != nil || set {
		t.Errorf("the flag of the user after the registration = %v, %v; want it clear", set, err)
	}
	h.send(message(submitBody(t, 1, scAddress, hello)))
	select {
	case got := <-h.hss.notifications:
		t.Errorf("the HSS heard %+v beside the registration", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestNotifyWithNoTimeLeftEndsTheSubscription is RFC 6665 4.1.3: the
// expires of Subscription-State is the time left, so a NOTIFY that
// says active;expires=0 ends the subscription as terminated does. The
// registration is withdrawn from the HSS, and a later NOTIFY gets 481.
func TestNotifyWithNoTimeLeftEndsTheSubscription(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Fatalf("PUR = %+v, want %+v", got, registered)
	}
	if resp := h.send(notify(subscribe, 2, "active;expires=0", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))); resp.StatusCode != 200 {
		t.Errorf("NOTIFY with expires=0 answered %d", resp.StatusCode)
	}
	if got := h.nextUpdate(); got != unregistered {
		t.Errorf("PUR = %+v, want %+v", got, unregistered)
	}
	if resp := h.send(notify(subscribe, 3, "active;expires=100", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))); resp.StatusCode != 481 {
		t.Errorf("NOTIFY after the end answered %d, want 481", resp.StatusCode)
	}
}

// TestContactWithoutTheFeatureTag: a UE that registered without
// "+g.3gpp.smsip" (TS 24.341 5.3.2.2) takes no short messages over IP,
// so the HSS hears nothing until a contact with the tag is there.
func TestContactWithoutTheFeatureTag(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	if resp := h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.icsi-ref"))); resp.StatusCode != 200 {
		t.Fatalf("NOTIFY answered %d", resp.StatusCode)
	}
	h.noUpdate()
	h.send(notify(subscribe, 2, "active;expires=3700", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	if got := h.nextUpdate(); got != registered {
		t.Errorf("PUR = %+v, want %+v", got, registered)
	}
}

// TestNotifyInDisorder is RFC 3261 12.2.2: a NOTIFY with a lower CSeq
// than one already taken gets 500 and changes nothing.
func TestNotifyInDisorder(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 2, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	h.nextUpdate()
	if resp := h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active))); resp.StatusCode != 500 {
		t.Errorf("late NOTIFY answered %d, want 500", resp.StatusCode)
	}
	h.noUpdate()
}

// TestReregistrationRefreshesTheSubscription is RFC 6665 4.1.2.2: the
// third-party REGISTER of a reregistration refreshes the subscription
// within its dialog, at the S-CSCF that the NOTIFY named, instead of
// starting another one.
func TestReregistrationRefreshesTheSubscription(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	h.thirdPartyRegister(3600)
	subscribe := h.subscribed()
	h.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip")))
	h.nextUpdate()

	h.thirdPartyRegister(1800)
	var refresh *sip.Message
	select {
	case refresh = <-h.atSCSCF:
	case <-time.After(5 * time.Second):
		t.Fatal("no refresh reached the S-CSCF")
	}
	if refresh.Method != sip.SUBSCRIBE || refresh.RequestURI != "sip:"+scscfHost+":5060" {
		t.Errorf("refresh is %s %s, want SUBSCRIBE to the S-CSCF's Contact", refresh.Method, refresh.RequestURI)
	}
	checkHeaders(t, refresh, map[string]string{
		"Call-ID": subscribe.Get("Call-ID"),
		"From":    subscribe.Get("From"),
		"To":      "<" + alice + ">;tag=scscf-tag",
		"CSeq":    "2 SUBSCRIBE",
		// The dialog has no route set: the S-CSCF did not record-route.
		"Route":             "",
		"Event":             "reg",
		"Expires":           "1961",
		"P-Charging-Vector": subscribe.Get("P-Charging-Vector"),
	})
	select {
	case m := <-h.received:
		t.Errorf("a %s went to the entry point too", m.Method)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNotifyToAnotherIPSMGW: two IP-SM-GWs share a Store, and
// the NOTIFY of the subscription that one of them made reaches the
// other, as DNS may have it.
func TestNotifyToAnotherIPSMGW(t *testing.T) {
	store := ipsmgw.NewMemoryStore()
	a := newHarness(t, store, time.Minute, answerOK)
	b := newHarness(t, store, time.Minute, answerOK)
	a.thirdPartyRegister(3600)
	subscribe := a.subscribed()
	if resp := b.send(notify(subscribe, 1, "active;expires=3761", regDoc(reginfo.Active, reginfo.Active, "+g.3gpp.smsip"))); resp.StatusCode != 200 {
		t.Fatalf("NOTIFY to the second IP-SM-GW answered %d", resp.StatusCode)
	}
	if got := b.nextUpdate(); got != registered {
		t.Errorf("PUR of the second IP-SM-GW = %+v", got)
	}
	a.noUpdate()
}

// A NOTIFY that belongs to no subscription gets 481 (RFC 6665 4.1.3).
func TestNotifyForNoSubscription(t *testing.T) {
	h := newHarness(t, ipsmgw.NewMemoryStore(), time.Minute, answerOK)
	stranger := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: alice}
	stranger.Add("From", "<"+ownURI+">;tag=other")
	stranger.Add("To", "<"+alice+">")
	stranger.Add("Call-ID", "no-such-subscription")
	stranger.Add("Contact", "<"+ownURI+">")
	if resp := h.send(notify(stranger, 1, "active;expires=60", regDoc(reginfo.Active, reginfo.Active))); resp.StatusCode != 481 {
		t.Errorf("answered %d, want 481", resp.StatusCode)
	}
}
