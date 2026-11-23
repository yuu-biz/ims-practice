package scscf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/hss"
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
