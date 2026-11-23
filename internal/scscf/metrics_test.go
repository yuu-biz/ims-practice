package scscf

import (
	"bytes"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// scraped is the value of series, such as
// ims_registration_attempts_total{role="scscf",type="initial"}, in what
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

// counts checks the series of reg against want, and that no failure was
// counted beyond those in want. The S-CSCF counts the outcome of a
// REGISTER once its handler returns, just after the response has left,
// so counts waits for the series to settle before it reports a
// difference.
func counts(t *testing.T, reg *metrics.Registry, want map[string]float64) {
	t.Helper()
	var diffs []string
	deadline := time.Now().Add(3 * time.Second)
	for {
		diffs = countDiffs(t, reg, want)
		if len(diffs) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, d := range diffs {
		t.Error(d)
	}
}

// countDiffs lists how the series of reg differ from want, and whether
// failures were counted beyond those in want.
func countDiffs(t *testing.T, reg *metrics.Registry, want map[string]float64) []string {
	t.Helper()
	var diffs []string
	failures := 0.0
	for series, n := range want {
		if got := scraped(t, reg, series); got != n {
			diffs = append(diffs, fmt.Sprintf("%s = %v, want %v", series, got, n))
		}
		if strings.HasPrefix(series, "ims_registration_failures_total") {
			failures += n
		}
	}
	var text bytes.Buffer
	reg.WriteText(&text)
	got := 0.0
	for line := range strings.SplitSeq(text.String(), "\n") {
		if strings.HasPrefix(line, "ims_registration_failures_total{") {
			v, _ := strconv.ParseFloat(line[strings.LastIndex(line, " ")+1:], 64)
			got += v
		}
	}
	if got != failures {
		diffs = append(diffs, fmt.Sprintf("%v failures counted, want %v:\n%s", got, failures, text.String()))
	}
	return diffs
}

const (
	attemptsInitial  = `ims_registration_attempts_total{role="scscf",type="initial"}`
	attemptsRe       = `ims_registration_attempts_total{role="scscf",type="re"}`
	attemptsDe       = `ims_registration_attempts_total{role="scscf",type="de"}`
	successesInitial = `ims_registration_successes_total{role="scscf",type="initial"}`
	successesRe      = `ims_registration_successes_total{role="scscf",type="re"}`
	successesDe      = `ims_registration_successes_total{role="scscf",type="de"}`
)

// TS 32.409 4.1.1 counts registration procedures: the REGISTER that the
// S-CSCF challenges and the one that answers the challenge are one
// attempt, and the 401 between them neither a success nor a failure.
// The type comes from the registration that the S-CSCF holds when the
// REGISTER arrives.
func TestRegistrationProceduresWithSIPDigestAreCounted(t *testing.T) {
	reg := metrics.NewRegistry()
	h := newHarness(t, Config{Metrics: reg})
	callID := sip.NewCallID()

	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d, want 200", r.StatusCode)
	}
	counts(t, reg, map[string]float64{attemptsInitial: 1, successesInitial: 1})

	// A refresh with the credentials of the last challenge gets a new
	// one (TS 24.229 5.4.1.2.1), which the next REGISTER answers.
	r = h.register(callID, 3, 600000, auth)
	if r.StatusCode != 401 {
		t.Fatalf("refresh with an old nonce got %d, want 401", r.StatusCode)
	}
	auth, _ = answer(t, r, password, "ip-assoc-yes")
	if r := h.register(callID, 4, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("re-registration got %d, want 200", r.StatusCode)
	}
	counts(t, reg, map[string]float64{attemptsInitial: 1, successesInitial: 1, attemptsRe: 1, successesRe: 1})

	dereg := `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain + `", response="", integrity-protected="ip-assoc-yes"`
	if r := h.register(callID, 5, 0, dereg); r.StatusCode != 200 {
		t.Fatalf("deregistration got %d, want 200", r.StatusCode)
	}
	counts(t, reg, map[string]float64{
		attemptsInitial: 1, successesInitial: 1, attemptsRe: 1, successesRe: 1, attemptsDe: 1, successesDe: 1,
	})
}

// With IMS AKA a protected re-registration and deregistration need no
// new challenge, and each is a procedure of its own.
func TestRegistrationProceduresWithIMSAKAAreCounted(t *testing.T) {
	reg := metrics.NewRegistry()
	h := newHarnessWithHSS(t, Config{Metrics: reg}, nil, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: testSet1}})
	usim := aka.NewUSIM(testSet1, 0)
	callID := sip.NewCallID()

	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	res, err := authenticate(t, usim, ch)
	if err != nil {
		t.Fatal(err)
	}
	creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
	for cseq, expires := range []int{600000, 600000, 0} {
		if r := h.register(callID, cseq+2, expires, creds.String()); r.StatusCode != 200 {
			t.Fatalf("REGISTER %d got %d, want 200", cseq+2, r.StatusCode)
		}
	}
	counts(t, reg, map[string]float64{
		attemptsInitial: 1, successesInitial: 1, attemptsRe: 1, successesRe: 1, attemptsDe: 1, successesDe: 1,
	})
}

// A failed procedure counts under the status code of its final
// response. The 481 (Call/Transaction Does Not Exist) that a
// deregistration gets when the user has no registration (TS 24.229
// 5.4.1.4.1) has a series of its own, like every other code that this
// network produces.
func TestFailedRegistrationsAreCountedByStatus(t *testing.T) {
	reg := metrics.NewRegistry()
	h := newHarness(t, Config{Metrics: reg, MinExpires: time.Minute})

	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, "wrong-password", "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 403 {
		t.Fatalf("wrong password got %d, want 403", r.StatusCode)
	}
	if r := h.register(sip.NewCallID(), 1, 10, initial); r.StatusCode != 423 {
		t.Fatalf("too brief an interval got %d, want 423", r.StatusCode)
	}
	if r := h.register(sip.NewCallID(), 1, 0, initial); r.StatusCode != 481 {
		t.Fatalf("deregistration of a user not registered got %d, want 481", r.StatusCode)
	}
	counts(t, reg, map[string]float64{
		attemptsInitial: 2,
		attemptsDe:      1,
		`ims_registration_failures_total{role="scscf",type="initial",status="403"}`: 1,
		`ims_registration_failures_total{role="scscf",type="initial",status="423"}`: 1,
		`ims_registration_failures_total{role="scscf",type="de",status="481"}`:      1,
	})
}

// ringingHop alerts the caller with a 180 (Ringing) and then refuses
// the call. No 2xx goes to an INVITE, so that the caller's transaction
// ends at once instead of waiting out Timer M for retransmissions (RFC
// 3261 13.2.2.4).
func ringingHop(t *testing.T) *sip.Stack {
	t.Helper()
	return newStack(t, func(st *sip.ServerTransaction) {
		callee := sip.NewTag()
		st.Respond(sip.NewResponse(st.Request, 180, callee))
		st.Respond(sip.NewResponse(st.Request, 486, callee))
	})
}

// sessionSeries is the value of each session measurement of the S-CSCF
// that a test reads.
func sessionSeries(t *testing.T, reg *metrics.Registry, want map[string]float64) {
	t.Helper()
	for series, n := range want {
		if got := scraped(t, reg, series); got != n {
			t.Errorf("%s = %v, want %v", series, got, n)
		}
	}
}

// TS 32.409 4.1.2.1 counts an initial INVITE at the S-CSCF on receipt
// and 4.1.2.2 the response that tells the session got through, under
// the type of that message: the 180 (Ringing) of the callee here. A
// call also visits the S-CSCF once more for every application server of
// the criteria, each on a transaction of its own (TS 24.229 5.4.3.4),
// and counts once all the same. The directions add up to the two
// measurements, which name none: 4.1.2.9 to 4.1.2.12 split the
// measurements of the P-CSCF and the I-CSCF, not those of the S-CSCF.
func TestSessionEstablishmentsAreCountedOnceAtTheSCSCF(t *testing.T) {
	reg := metrics.NewRegistry()
	terminating := ifc.TerminatingRegistered
	criterion := ifc.InitialFilterCriteria{
		TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
			{Group: []int{0}, Method: "INVITE"},
			{Group: []int{1}, SessionCase: &terminating},
		}},
		ApplicationServer: ifc.ApplicationServer{ServerName: "sip:as1." + domain + ":5060"},
	}
	proxy := newProxyAS(t, "as1."+domain, "")
	h := newServiceHarnessWith(t, Config{Metrics: reg}, []ifc.InitialFilterCriteria{criterion},
		map[string]netip.AddrPort{
			"as1." + domain:   proxy.stack.LocalAddr(),
			"pcscf." + domain: ringingHop(t).LocalAddr(),
			domain:            ringingHop(t).LocalAddr(),
		})
	proxy.serve(h)
	h.registerUser(sip.NewCallID())

	if r := h.invite(impu, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 486 {
		t.Fatalf("the call to the user got %d, want 486", r.StatusCode)
	}
	// The server took the call and sent it back, so the S-CSCF served
	// two transactions of this one session establishment.
	received(t, proxy.got)
	if r := h.invite("sip:001010000000002@"+domain, orig, impu); r.StatusCode != 486 {
		t.Fatalf("the call from the user got %d, want 486", r.StatusCode)
	}
	sessionSeries(t, reg, map[string]float64{
		`ims_session_attempts_total{role="scscf",direction="terminating"}`:                 1,
		`ims_session_successes_total{role="scscf",direction="terminating",response="180"}`: 1,
		`ims_session_successes_total{role="scscf",direction="terminating",response="200"}`: 0,
		`ims_session_attempts_total{role="scscf",direction="originating"}`:                 1,
		`ims_session_successes_total{role="scscf",direction="originating",response="180"}`: 1,
	})
}

// Only an initial INVITE sets up a session. A REGISTER, a standalone
// request such as the MESSAGE that carries an SMS, and a request within
// a dialog are no session establishments.
func TestOnlyAnInitialINVITEIsASessionEstablishmentAtTheSCSCF(t *testing.T) {
	reg := metrics.NewRegistry()
	h := newServiceHarnessWith(t, Config{Metrics: reg}, nil, nil)
	h.registerUser(sip.NewCallID())
	h.send(sms("sip:001010000000002@"+domain, orig, impu))
	// The next hop keeps one request at a time, so this one is taken
	// before the next request goes there.
	received(t, h.icscf)

	withinDialog := newRequest(sip.INVITE, impu, "<sip:scscf."+domain+":5060;lr>", "")
	withinDialog.Set("To", "<"+impu+">;tag="+sip.NewTag())
	withinDialog.Add("Contact", "<sip:001010000000001@192.0.2.10:5060>")
	h.send(withinDialog)
	received(t, h.icscf)

	sessionSeries(t, reg, map[string]float64{
		`ims_session_attempts_total{role="scscf",direction="terminating"}`: 0,
		`ims_session_attempts_total{role="scscf",direction="originating"}`: 0,
	})
}

// TS 32.409 4.1.1.3.2 counts the de-registrations that the HSS starts
// at the S-CSCF and 4.1.7 the user profile updates it starts: the RTR
// and the PPR on their receipt (4.1.1.3.2.1, 4.1.7.1), and the answer
// to each as the S-CSCF hands it back, the successes apart from the
// failures (4.1.1.3.2.2, 4.1.1.3.2.3, 4.1.7.2, 4.1.7.3). The Diameter
// result code that those two failure clauses split by is the result
// label: a PPR for a user this S-CSCF does not serve is answered
// DIAMETER_ERROR_USER_UNKNOWN (TS 29.228 Table 6.2.2.1.1).
func TestTheRTRAndThePPRThatTheHSSSendsAreCounted(t *testing.T) {
	reg := metrics.NewRegistry()
	criteria := []ifc.InitialFilterCriteria{smsCriterion(0, asURI, ifc.SessionContinued)}
	h := newServiceHarnessWith(t, Config{Metrics: reg}, criteria, nil)
	h.registerUser(sip.NewCallID())

	if r := h.ppr(impi, userProfile(criteria...)); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("PPA = %s, want DIAMETER_SUCCESS", r)
	}
	if r := h.ppr("001010000000009@"+domain, userProfile(criteria...)); r != diameter.Experimental(cx.DiameterErrorUserUnknown) {
		t.Fatalf("PPA for a user this S-CSCF does not serve = %s, want DIAMETER_ERROR_USER_UNKNOWN", r)
	}
	if r := rtaResult(t, h.rtr(impi, cx.PermanentTermination)); r != diameter.Base(diameter.DiameterSuccess) {
		t.Fatalf("RTA = %s, want DIAMETER_SUCCESS", r)
	}

	counts(t, reg, map[string]float64{
		`ims_diameter_requests_total{role="scscf",direction="received",command="PPR"}`:                                 2,
		`ims_diameter_answers_total{role="scscf",direction="sent",command="PPR",result="DIAMETER_SUCCESS"}`:            1,
		`ims_diameter_answers_total{role="scscf",direction="sent",command="PPR",result="DIAMETER_ERROR_USER_UNKNOWN"}`: 1,
		`ims_diameter_requests_total{role="scscf",direction="received",command="RTR"}`:                                 1,
		`ims_diameter_answers_total{role="scscf",direction="sent",command="RTR",result="DIAMETER_SUCCESS"}`:            1,
	})
}
