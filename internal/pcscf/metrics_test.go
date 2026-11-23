package pcscf

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// scraped is the value of series, such as
// ims_registration_attempts_total{role="pcscf",type="initial"}, in what
// Prometheus would scrape from reg, and 0 when reg has no such series.
func scraped(t *testing.T, reg *metrics.Registry, series string) float64 {
	t.Helper()
	var text bytes.Buffer
	if err := reg.WriteText(&text); err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(text.String(), "\n") {
		if v, ok := strings.CutPrefix(line, series+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("%s: %v", line, err)
			}
			return f
		}
	}
	return 0
}

// counted checks the series of reg against want. The P-CSCF counts a
// response before it hands it to the stack, so the counts are there
// once the UE has the response.
func counted(t *testing.T, reg *metrics.Registry, want map[string]float64) {
	t.Helper()
	for series, n := range want {
		if got := scraped(t, reg, series); got != n {
			t.Errorf("%s = %v, want %v", series, got, n)
		}
	}
}

// digestRegistrar is a fake home network that challenges a REGISTER, accepts
// the one that answers its last challenge, and answers 403 to one that
// does so with the response "wrong". A deregistration it accepts at
// once.
type digestRegistrar struct {
	mu      sync.Mutex
	pending string
	nonces  int
}

func (r *digestRegistrar) serve(st *sip.ServerTransaction) {
	req := st.Request
	c, _ := sip.ParseCredentials(req.Get("Authorization"))
	r.mu.Lock()
	answers := c.Nonce != "" && c.Nonce == r.pending
	if answers {
		r.pending = ""
	}
	deregisters := strings.Contains(req.Get("Contact"), "expires=0")
	if !answers && !deregisters {
		r.nonces++
		r.pending = "nonce-" + strconv.Itoa(r.nonces)
	}
	nonce := r.pending
	r.mu.Unlock()
	switch {
	case deregisters:
		resp := sip.NewResponse(req, 200, sip.NewTag())
		resp.Add("Contact", "<sip:ue@127.0.0.1>;expires=0")
		st.Respond(resp)
	case !answers:
		resp := sip.NewResponse(req, 401, sip.NewTag())
		resp.Add("WWW-Authenticate", sip.Challenge{Realm: domain, Nonce: nonce, Algorithm: "MD5", QOP: []string{"auth"}}.String())
		st.Respond(resp)
	case c.Response == "wrong":
		st.Respond(sip.NewResponse(req, 403, sip.NewTag()))
	default:
		resp := sip.NewResponse(req, 200, sip.NewTag())
		resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
		resp.Add("P-Associated-URI", "<"+impu+">")
		resp.Add("Contact", "<sip:ue@127.0.0.1>;expires=3600")
		st.Respond(resp)
	}
}

// TS 32.409 4.1.1 at the P-CSCF: a REGISTER that the home network
// challenges and the one that answers the challenge are one procedure,
// whose type comes from the IP association that the UE has when the
// procedure starts. The 401 between them is neither a success nor a
// failure, and the set-up time of an initial registration runs from
// its first REGISTER to its 200 (OK).
func TestRegistrationProceduresAreCountedAtThePCSCF(t *testing.T) {
	registrar := &digestRegistrar{}
	home := newStack(t, registrar.serve)
	reg := metrics.NewRegistry()
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, Metrics: reg},
		locatorFunc(func(string) []sip.Destination { return []sip.Destination{{Transport: sip.UDP, Addr: home.LocalAddr()}} }),
		NewMemoryStore(), nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	p.SetStack(pcscfStack)
	ue := newStack(t, func(*sip.ServerTransaction) {})
	cseq := 0
	register := func(nonce, response, expires string) *sip.Message {
		t.Helper()
		cseq++
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "call-1")
		m.Add("CSeq", strconv.Itoa(cseq)+" REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>;expires="+expires)
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="`+nonce+`", uri="sip:`+domain+`", response="`+response+`"`)
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
		return final
	}
	answer := func(r *sip.Message, response string) *sip.Message {
		t.Helper()
		ch, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
		if r.StatusCode != 401 || err != nil {
			t.Fatalf("got %d with %q, want a 401 with a challenge", r.StatusCode, r.Get("WWW-Authenticate"))
		}
		return register(ch.Nonce, response, "600000")
	}

	if r := answer(register("", "", "600000"), "right"); r.StatusCode != 200 {
		t.Fatalf("initial registration got %d, want 200", r.StatusCode)
	}
	counted(t, reg, map[string]float64{
		`ims_registration_attempts_total{role="pcscf",type="initial"}`:  1,
		`ims_registration_successes_total{role="pcscf",type="initial"}`: 1,
		`ims_registration_setup_seconds_count{role="pcscf"}`:            1,
	})
	if got := scraped(t, reg, `ims_registration_setup_seconds_sum{role="pcscf"}`); got <= 0 {
		t.Errorf("the set-up time is %v s, want more than 0", got)
	}

	// The refresh names the nonce of the last challenge, which the
	// P-CSCF no longer holds: it starts a procedure of its own, and the
	// home network challenges it again.
	if r := answer(register("nonce-1", "right", "600000"), "right"); r.StatusCode != 200 {
		t.Fatalf("re-registration got %d, want 200", r.StatusCode)
	}
	if r := register("", "", "0"); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d, want 200", r.StatusCode)
	}
	if r := answer(register("", "", "600000"), "wrong"); r.StatusCode != 403 {
		t.Fatalf("a wrong response got %d, want 403", r.StatusCode)
	}
	counted(t, reg, map[string]float64{
		`ims_registration_attempts_total{role="pcscf",type="initial"}`:              2,
		`ims_registration_successes_total{role="pcscf",type="initial"}`:             1,
		`ims_registration_failures_total{role="pcscf",type="initial",status="403"}`: 1,
		`ims_registration_attempts_total{role="pcscf",type="re"}`:                   1,
		`ims_registration_successes_total{role="pcscf",type="re"}`:                  1,
		`ims_registration_attempts_total{role="pcscf",type="de"}`:                   1,
		`ims_registration_successes_total{role="pcscf",type="de"}`:                  1,
		// Only a successful initial registration has a set-up time.
		`ims_registration_setup_seconds_count{role="pcscf"}`: 1,
	})
}

// A REGISTER that the P-CSCF answers itself ends its procedure too: with
// no I-CSCF left to try, it fails with 504 (TS 24.229 5.2.2.1).
func TestTheAnswerOfThePCSCFItselfEndsTheProcedure(t *testing.T) {
	reg := metrics.NewRegistry()
	p := newPCSCF(t, Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain, Metrics: reg},
		locatorFunc(func(string) []sip.Destination { return nil }), NewMemoryStore(), nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	ue := newStack(t, func(*sip.ServerTransaction) {})
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
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
	if final == nil || final.StatusCode != 504 {
		t.Fatalf("got %v, want 504", final)
	}
	counted(t, reg, map[string]float64{
		`ims_registration_attempts_total{role="pcscf",type="initial"}`:              1,
		`ims_registration_failures_total{role="pcscf",type="initial",status="504"}`: 1,
	})
}
