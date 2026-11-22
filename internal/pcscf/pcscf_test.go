package pcscf

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

const (
	domain = "ims.mnc001.mcc001.3gppnetwork.org"
	impi   = "001010000000001@" + domain
	impu   = "sip:001010000000001@" + domain
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type locatorFunc func(host string) []sip.Destination

func (f locatorFunc) Locate(_ context.Context, host string, _ int, _ sip.Transport) ([]sip.Destination, error) {
	return f(host), nil
}

// fast makes Timer F fire after 640 ms instead of 32 s.
var fast = sip.Timers{T1: 10 * time.Millisecond, T2: 40 * time.Millisecond, T4: 50 * time.Millisecond}

func newStack(t *testing.T, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	return newStackWith(t, sip.DefaultTimers(), onRequest)
}

func newStackWith(t *testing.T, timers sip.Timers, onRequest func(*sip.ServerTransaction)) *sip.Stack {
	t.Helper()
	return newStackConfig(t, sip.Config{Timers: timers, OnRequest: onRequest})
}

// newPCSCFStack is the unprotected stack of p, screened as the
// P-CSCF's is.
func newPCSCFStack(t *testing.T, p *PCSCF) *sip.Stack {
	t.Helper()
	return newStackConfig(t, sip.Config{Timers: sip.DefaultTimers(), Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest})
}

func newStackConfig(t *testing.T, cfg sip.Config) *sip.Stack {
	t.Helper()
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	cfg.SentBy = tr.LocalAddr().String()
	s, err := sip.NewStack(tr, cfg, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// TestRegisterThroughThePCSCF plays the home network as a fake I-CSCF:
// a 401 for the first REGISTER, a 200 for the second, and a 200 with
// expires=0 for the deregistration.
func TestRegisterThroughThePCSCF(t *testing.T) {
	received := make(chan *sip.Message, 3)
	home := newStack(t, func(st *sip.ServerTransaction) {
		received <- st.Request
		expires := "3600"
		if strings.Contains(st.Request.Get("Contact"), "expires=0") {
			expires = "0"
		}
		var resp *sip.Message
		if c, _ := sip.ParseCredentials(st.Request.Get("Authorization")); c.Response == "" && expires != "0" {
			resp = sip.NewResponse(st.Request, 401, sip.NewTag())
		} else {
			resp = sip.NewResponse(st.Request, 200, sip.NewTag())
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", "<sip:ue@127.0.0.1>;expires="+expires)
		}
		st.Respond(resp)
	})
	locate := locatorFunc(func(host string) []sip.Destination {
		if host != domain {
			t.Errorf("located %q, want the home domain", host)
		}
		return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}}
	})
	store := NewMemoryStore()
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, store, nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	ue := newStack(t, func(*sip.ServerTransaction) {})

	send := func(authorization, expires string) *sip.Message {
		t.Helper()
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "call-1")
		m.Add("CSeq", "1 REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>;expires="+expires)
		m.Add("Supported", "path")
		m.Add("Authorization", authorization)
		ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		if final == nil {
			t.Fatalf("no final response: %v", ct.Err())
		}
		return <-received
	}
	auth := func(response string) string {
		return `Digest username="` + impi + `", realm="` + domain + `", nonce="n", uri="sip:` + domain +
			`", response="` + response + `", integrity-protected="forged-by-ue"`
	}

	first := send(auth(""), "600000")
	if got := first.Get("Path"); got != "<sip:term@pcscf."+domain+":5060;lr>" {
		t.Errorf("Path = %q", got)
	}
	if first.Headers[0].Name != "Via" {
		t.Errorf("first header is %s; Path must not go above Via", first.Headers[0].Name)
	}
	if got := first.Get("Require"); got != "path" {
		t.Errorf("Require = %q, want path", got)
	}
	if got := first.Get("P-Visited-Network-ID"); got != domain {
		t.Errorf("P-Visited-Network-ID = %q", got)
	}
	if !strings.HasPrefix(first.Get("P-Charging-Vector"), "icid-value=") {
		t.Errorf("P-Charging-Vector = %q", first.Get("P-Charging-Vector"))
	}
	if got := first.Get("Max-Forwards"); got != "69" {
		t.Errorf("Max-Forwards = %q", got)
	}
	// 5.2.2.3: no association and no response, so no parameter; the
	// UE's own value is removed.
	if strings.Contains(first.Get("Authorization"), "integrity-protected") {
		t.Errorf("first Authorization = %q, want no integrity-protected", first.Get("Authorization"))
	}

	second := send(auth("6629fae49393a05397450978507c4ef1"), "600000")
	if !strings.Contains(second.Get("Authorization"), `integrity-protected="ip-assoc-pending"`) {
		t.Errorf("second Authorization = %q, want ip-assoc-pending", second.Get("Authorization"))
	}
	a, ok, _ := store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()})
	if !ok || a.PrivateIdentity != impi || len(a.ServiceRoute) != 1 || a.PublicIdentities[0] != impu {
		t.Fatalf("association after 200 = %+v, %v", a, ok)
	}

	third := send(auth(""), "0")
	if !strings.Contains(third.Get("Authorization"), `integrity-protected="ip-assoc-yes"`) {
		t.Errorf("deregistration Authorization = %q, want ip-assoc-yes", third.Get("Authorization"))
	}
	if a, ok, _ := store.Association(context.Background(), sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()}); ok && a.Expires.After(time.Now()) {
		t.Error("association still valid after deregistration")
	}
}

// TestNextICSCFWhenTheFirstIsSilentOrTurnsAway walks the targets of
// TS 24.229 5.2.2.1 item 7: a silent I-CSCF and one that answers 480 are
// skipped, and the third answers.
func TestNextICSCFWhenTheFirstIsSilentOrTurnsAway(t *testing.T) {
	silent := newStack(t, func(*sip.ServerTransaction) {})
	away := newStack(t, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 480, sip.NewTag())) })
	good := newStack(t, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 401, sip.NewTag())) })
	targets := []sip.Destination{{Transport: sip.UDP, Addr: silent.LocalAddr()}, {Transport: sip.UDP, Addr: away.LocalAddr()}, {Transport: sip.UDP, Addr: good.LocalAddr()}}
	if got := registerVia(t, targets); got != 401 {
		t.Errorf("got %d, want the 401 of the third I-CSCF", got)
	}
	if got := registerVia(t, targets[:2]); got != 504 {
		t.Errorf("with no I-CSCF left got %d, want 504", got)
	}
}

func registerVia(t *testing.T, targets []sip.Destination) int {
	t.Helper()
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain},
		locatorFunc(func(string) []sip.Destination { return targets }), NewMemoryStore(), nil, quiet())
	pcscfStack := newStackConfig(t, sip.Config{Timers: fast, Screen: p.Screen, Admit: p.AdmitUnprotected, OnRequest: p.OnRequest})
	ue := newStack(t, func(*sip.ServerTransaction) {})
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 REGISTER")
	m.Add("Contact", "<sip:ue@127.0.0.1>")
	ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	return final.StatusCode
}
