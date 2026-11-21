package dashboard

import (
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
)

const (
	impi      = "001010000000001@" + domain
	impu      = "sip:001010000000001@" + domain
	impuTel   = "tel:+12025550101"
	ueContact = "sip:001010000000001@172.28.0.100:5060"
	regCallID = "reg-1@172.28.0.100"
)

// The registration of docs/guide/en/registration.md: UE, P-CSCF, I-CSCF,
// S-CSCF and back.
var (
	toRegistrar = []node{ue1, pcscf, icscf, scscf}
	toUE        = []node{scscf, icscf, pcscf, ue1}
)

func register(seq int, authorization string, expires int) string {
	return sipText("REGISTER sip:"+domain+" SIP/2.0", "",
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bKreg;rport",
		"Max-Forwards: 70",
		"From: <"+impu+">;tag=reg1",
		"To: <"+impu+">",
		"Call-ID: "+regCallID,
		"CSeq: "+strconv.Itoa(seq)+" REGISTER",
		"Contact: <"+ueContact+">;expires="+strconv.Itoa(expires),
		"Authorization: "+authorization,
		"Supported: path")
}

func registerResponse(seq int, code int, reason string, rows ...string) string {
	return sipText("SIP/2.0 "+strconv.Itoa(code)+" "+reason, "", append([]string{
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bKreg;rport=5060;received=172.28.0.100",
		"From: <" + impu + ">;tag=reg1",
		"To: <" + impu + ">;tag=scscf1",
		"Call-ID: " + regCallID,
		"CSeq: " + strconv.Itoa(seq) + " REGISTER",
	}, rows...)...)
}

func notify(t *testing.T, doc reginfo.Document) string {
	t.Helper()
	body, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return sipText("NOTIFY "+ueContact+" SIP/2.0", string(body),
		"Via: SIP/2.0/UDP scscf."+domain+":5060;branch=z9hG4bKn1",
		"Max-Forwards: 70",
		"From: <"+impu+">;tag=notifier",
		"To: <"+impu+">;tag=sub1",
		"Call-ID: sub-1@172.28.0.40",
		"CSeq: 1 NOTIFY",
		"Event: reg",
		"Subscription-State: active;expires=600",
		"Content-Type: "+reginfo.ContentType)
}

func (h *harness) registrations() map[string]Registration {
	h.t.Helper()
	var regs []Registration
	h.get("/api/registrations", &regs)
	byIMPU := map[string]Registration{}
	for _, r := range regs {
		byIMPU[r.IMPU] = r
	}
	return byIMPU
}

func TestRegistration(t *testing.T) {
	h := newHarness(t, Config{})
	h.along(toRegistrar, register(1, `Digest username="`+impi+`", realm="`+domain+`", nonce="", uri="sip:`+domain+`", response=""`, 600000))
	h.along(toUE, registerResponse(1, 401, "Unauthorized", `WWW-Authenticate: Digest realm="`+domain+`", nonce="abc", algorithm=MD5, qop="auth"`))
	if regs := h.registrations(); len(regs) != 0 {
		t.Fatalf("registrations after the 401: %+v", regs)
	}

	h.along(toRegistrar, register(2, `Digest username="`+impi+`", realm="`+domain+`", nonce="abc", uri="sip:`+domain+`", response="0123", qop=auth, nc=00000001, cnonce="c1"`, 600000))
	// Every node captures the 200; the S-CSCF's copy, the first, counts.
	sent := h.clock.Add(time.Millisecond)
	h.along(toUE, registerResponse(2, 200, "OK",
		"Path: <sip:term@pcscf."+domain+":5060;lr>",
		"Service-Route: <sip:orig@scscf."+domain+":5060;lr>",
		"P-Associated-URI: <"+impu+">",
		"P-Associated-URI: <"+impuTel+">",
		"Contact: <"+ueContact+">;expires=600"))
	regs := h.registrations()
	for _, id := range []string{impu, impuTel} {
		r, ok := regs[id]
		want := Registration{IMPU: id, IMPI: impi, Contact: ueContact, State: Registered, SCSCF: "sip:scscf." + domain + ":5060", Expires: r.Expires, CallID: regCallID}
		if !ok || r != want {
			t.Errorf("registration of %s = %+v, want %+v", id, r, want)
		}
		if left := r.Expires.Sub(sent); left != 600*time.Second {
			t.Errorf("%s expires %v after the S-CSCF sent the 200, want 600s", id, left)
		}
	}

	// The S-CSCF's NOTIFY ends the tel URI but keeps the SIP URI.
	sent = h.clock.Add(time.Millisecond)
	h.along([]node{scscf, pcscf, ue1}, notify(t, reginfo.Document{State: reginfo.Full, Registrations: []reginfo.Registration{
		{AOR: impu, ID: "r1", State: reginfo.Active, Contacts: []reginfo.Contact{
			{ID: "c1", State: reginfo.Active, Event: reginfo.Refreshed, Expires: 590, URI: ueContact}}},
		{AOR: impuTel, ID: "r2", State: reginfo.Terminated, Contacts: []reginfo.Contact{
			{ID: "c2", State: reginfo.Terminated, Event: reginfo.Unregistered, URI: ueContact}}},
	}}))
	regs = h.registrations()
	if r := regs[impu]; r.State != Registered || r.IMPI != impi || r.Expires.Sub(sent) != 590*time.Second || r.CallID != regCallID {
		t.Errorf("after the NOTIFY, %s = %+v", impu, r)
	}
	if r := regs[impuTel]; r.State != Unregistered || r.IMPI != impi || r.Contact != ueContact || !r.Expires.Equal(sent) || r.CallID != regCallID {
		t.Errorf("after the NOTIFY, %s = %+v", impuTel, r)
	}

	// Deregistration: the 200 has expires=0 and no Service-Route.
	h.along(toRegistrar, register(3, `Digest username="`+impi+`", realm="`+domain+`", nonce="abc", uri="sip:`+domain+`", response="4567", qop=auth, nc=00000002, cnonce="c2"`, 0))
	sent = h.clock.Add(time.Millisecond)
	h.along(toUE, registerResponse(3, 200, "OK", "Contact: <"+ueContact+">;expires=0"))
	r := h.registrations()[impu]
	if r.State != Unregistered || r.SCSCF != "sip:scscf."+domain+":5060" || !r.Expires.Equal(sent) {
		t.Errorf("after deregistration, %s = %+v", impu, r)
	}
}

// A NOTIFY has the Call-ID of the subscription, which is no
// registration's ladder.
func TestRegistrationFromNotifyHasNoCallID(t *testing.T) {
	h := newHarness(t, Config{})
	h.hop(scscf, pcscf, notify(t, reginfo.Document{State: reginfo.Full, Registrations: []reginfo.Registration{
		{AOR: impu, ID: "r1", State: reginfo.Active, Contacts: []reginfo.Contact{
			{ID: "c1", State: reginfo.Active, Event: reginfo.Registered, Expires: 600, URI: ueContact}}},
	}}))
	if r, ok := h.registrations()[impu]; !ok || r.State != Registered || r.CallID != "" {
		t.Errorf("registration from a NOTIFY = %+v, want registered with no call_id", r)
	}
}
