package scscf

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The test user registers impu, which the HSS bars: the temporary public
// identity of a UE without an ISIM (TS 23.228 E.3.1). Its implicit
// registration set has two identities of the MSISDN.
const (
	defaultIdentity = "sip:+12025550101@" + domain
	telIdentity     = "tel:+12025550101"
)

// barredHarness registers impu, barred, with the Path of a P-CSCF. The
// P-CSCF is pcscfUA: it gets the NOTIFYs of the reg event, and rejects
// with 480 each INVITE to the UE, whose Request-URI goes to calls.
func barredHarness(t *testing.T) (h *harness, calls <-chan string, pcscfUA *sip.UA) {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	h = newHarnessWithHSS(t, Config{}, fakeResolver{hosts: map[string]netip.AddrPort{"pcscf." + domain: tr.LocalAddr()}}, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, defaultIdentity, telIdentity},
			BarredIdentities: []string{impu}, Password: password})
	scscf := h.scscf.LocalAddr()
	got := make(chan string, 4)
	pcscfUA, err = sip.NewUA(tr, sip.UAConfig{
		SentBy:  tr.LocalAddr().String(),
		Contact: pcscfURI,
		OnCall: func(c *sip.IncomingCall) {
			got <- c.Request.RequestURI
			c.Reject(480)
		},
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: scscf}}, nil
		},
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pcscfUA.Close)
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	r = h.register(callID, 2, 600000, auth)
	if r.StatusCode != 200 {
		t.Fatalf("registration of the barred identity got %d", r.StatusCode)
	}
	// TS 24.229 5.4.1.2.2F item b: the default public identity first,
	// and not the barred identity that was registered.
	if got := r.GetAll("P-Associated-URI"); strings.Join(got, ",") != "<"+defaultIdentity+">,<"+telIdentity+">" {
		t.Fatalf("P-Associated-URI = %q, want the default public identity and the tel URI", got)
	}
	return h, got, pcscfUA
}

// TestBarredIdentityOriginates checks TS 24.229 5.4.3.2 step 1: a
// request whose served user is barred gets 403, one from the default
// public identity is served.
func TestBarredIdentityOriginates(t *testing.T) {
	h, _, _ := barredHarness(t)
	if r := h.invite("tel:+12025550102", orig, impu); r.StatusCode != 403 {
		t.Errorf("INVITE asserted as the barred identity got %d, want 403", r.StatusCode)
	}
	m := sms("tel:+12025550102", orig, impu)
	m.Add("P-Served-User", "<"+impu+">;sescase=orig;regstate=reg")
	if r := h.send(m); r.StatusCode != 403 {
		t.Errorf("MESSAGE served for the barred identity got %d, want 403", r.StatusCode)
	}
	// The default identity gets past step 1: without ENUM for the
	// number the S-CSCF answers 404 at step 10 instead.
	if r := h.invite("tel:+12025550102", orig, defaultIdentity); r.StatusCode == 403 {
		t.Errorf("INVITE from the default public identity got 403")
	}
}

// TestBarredIdentityIsNotReachable checks TS 24.229 5.4.3.3 step 1: a
// request to the barred identity gets 404 and does not reach the UE,
// while a request to an identity of the set that is not barred does.
func TestBarredIdentityIsNotReachable(t *testing.T) {
	h, calls, _ := barredHarness(t)
	if r := h.invite(impu, "", "sip:caller@"+domain); r.StatusCode != 404 {
		t.Errorf("INVITE to the barred identity got %d, want 404", r.StatusCode)
	}
	select {
	case uri := <-calls:
		t.Fatalf("an INVITE reached the P-CSCF for %s", uri)
	case <-time.After(200 * time.Millisecond):
	}
	if r := h.invite(telIdentity, "", "sip:caller@"+domain); r.StatusCode != 480 {
		t.Errorf("INVITE to the tel URI got %d, want the 480 of the P-CSCF", r.StatusCode)
	}
}

// TestRegEventLeavesOutTheBarredIdentity checks TS 24.229 5.4.2.1.1 step
// 0 and 5.4.2.1.2: the barred identity has no binding to subscribe to,
// and the NOTIFY of the default identity lists only the identities that
// are not barred.
func TestRegEventLeavesOutTheBarredIdentity(t *testing.T) {
	_, _, ua := barredHarness(t)
	subscribe := func(resource string) (*sip.Subscription, <-chan sip.Notification) {
		got := make(chan sip.Notification, 8)
		sub, err := ua.Subscribe(sip.SubscribeRequest{
			RequestURI: resource,
			From:       pcscfURI,
			Event:      "reg",
			Expires:    time.Hour,
			Headers:    []sip.HeaderField{{Name: "P-Asserted-Identity", Value: pcscfPath}},
			OnNotify:   func(n sip.Notification, _ *sip.Message) { got <- n },
		})
		if err != nil {
			t.Fatal(err)
		}
		return sub, got
	}
	sub, _ := subscribe(impu)
	select {
	case <-sub.Done():
		if err := sub.Err(); err == nil || !strings.Contains(err.Error(), "480") {
			t.Errorf("subscription to the barred identity ended with %v, want a 480", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the SUBSCRIBE to the barred identity was not refused")
	}
	_, got := subscribe(defaultIdentity)
	_, doc := nextNotify(t, got)
	var aors []string
	for _, r := range doc.Registrations {
		aors = append(aors, r.AOR)
	}
	if strings.Join(aors, ",") != defaultIdentity+","+telIdentity {
		t.Errorf("the reg event reports %v, want %s and %s only", aors, defaultIdentity, telIdentity)
	}
}

// TestThirdPartyRegisterNamesTheDefaultIdentity checks TS 24.229 5.4.1.7
// item c: the To of the third-party REGISTER is not barred, even when
// the UE registered a barred identity.
func TestThirdPartyRegisterNamesTheDefaultIdentity(t *testing.T) {
	as, got := fakeAS(t, answers(200))
	h := newHarnessWithHSS(t, Config{}, fakeResolver{hosts: map[string]netip.AddrPort{asHost: as.LocalAddr()}},
		hss.Config{InitialFilterCriteria: []ifc.InitialFilterCriteria{registerCriterion(0, asURI, ifc.SessionContinued)}},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu, defaultIdentity, telIdentity},
			BarredIdentities: []string{impu}, Password: password})
	h.registerUser(sip.NewCallID())
	if m := received(t, got); m.Get("To") != "<"+defaultIdentity+">" {
		t.Errorf("third-party REGISTER To = %q, want the default public identity", m.Get("To"))
	}
}
