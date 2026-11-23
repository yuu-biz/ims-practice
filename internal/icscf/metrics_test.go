package icscf

import (
	"bytes"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// scraped is the value of series, such as
// ims_session_attempts_total{role="icscf",direction="terminating"}, in
// what Prometheus would scrape from reg, and 0 when reg has no such
// series.
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

const terminatingAttempts = `ims_session_attempts_total{role="icscf",direction="terminating"}`

// counting starts an I-CSCF that counts in a registry of the test, in
// front of a fake S-CSCF that answers as scscfBehaviour says, and
// returns the UE's stack, the address of the I-CSCF and that registry.
func counting(t *testing.T, scscfBehaviour func(*sip.ServerTransaction)) (*sip.Stack, netip.AddrPort, *metrics.Registry) {
	t.Helper()
	reg := metrics.NewRegistry()
	cfg := Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1, 2}}}, Metrics: reg}
	_, ue, icscf := start(t, cfg, registeredAtHSS(t), nil, scscfBehaviour)
	return ue, icscf, reg
}

// refuses answers every request 486 (Busy Here). No 2xx goes to an
// INVITE, so that the UE's transaction ends at once instead of waiting
// out Timer M for retransmissions (RFC 3261 13.2.2.4).
func refuses(st *sip.ServerTransaction) {
	st.Respond(sip.NewResponse(st.Request, 486, sip.NewTag()))
}

// TS 32.409 4.1.2.11 counts the initial INVITE on receipt, whatever
// becomes of the session afterwards.
func TestInitialINVITEIsATerminatingSessionAttempt(t *testing.T) {
	ue, icscf, reg := counting(t, refuses)
	if r := invite(t, ue, icscf, impu); r.StatusCode != 486 {
		t.Fatalf("got %d, want the S-CSCF's 486", r.StatusCode)
	}
	if got := scraped(t, reg, terminatingAttempts); got != 1 {
		t.Errorf("%s = %v, want 1", terminatingAttempts, got)
	}
}

// TS 32.409 4.1.2.12 counts the successful terminating session
// establishment on the first response that tells of one, which the
// I-CSCF passes back towards the S-CSCF that started the session, under
// the type of that message. Here the callee is alerted, so the 180
// (Ringing) is that response: the 183 (Session Progress) before it is
// none of the responses the clause names, the second 180 is not the
// first, and the 486 that ends the session afterwards takes nothing
// back.
func TestTheFirst180PassedBackIsATerminatingSessionSuccess(t *testing.T) {
	ue, icscf, reg := counting(t, func(st *sip.ServerTransaction) {
		callee := sip.NewTag()
		for _, code := range []int{183, 180, 180, 486} {
			st.Respond(sip.NewResponse(st.Request, code, callee))
		}
	})
	if r := invite(t, ue, icscf, impu); r.StatusCode != 486 {
		t.Fatalf("got %d, want the S-CSCF's 486", r.StatusCode)
	}
	for series, want := range map[string]float64{
		terminatingAttempts: 1,
		`ims_session_successes_total{role="icscf",direction="terminating",response="180"}`: 1,
		`ims_session_successes_total{role="icscf",direction="terminating",response="200"}`: 0,
	} {
		if got := scraped(t, reg, series); got != want {
			t.Errorf("%s = %v, want %v", series, got, want)
		}
	}
}

// Only an initial INVITE sets up a session. A REGISTER, a standalone
// request such as a MESSAGE, and a request within a dialog are no
// session establishments.
func TestOnlyAnInitialINVITECountsAsASessionAttempt(t *testing.T) {
	ue, icscf, reg := counting(t, refuses)
	register(t, ue, icscf, impu)

	message := &sip.Message{Method: sip.MESSAGE, RequestURI: impu}
	message.Add("Max-Forwards", "68")
	message.Add("From", "<sip:caller@"+domain+">;tag="+sip.NewTag())
	message.Add("To", "<"+impu+">")
	message.Add("Call-ID", sip.NewCallID())
	message.Add("CSeq", "1 MESSAGE")
	send(t, ue, icscf, message)

	reINVITE := &sip.Message{Method: sip.INVITE, RequestURI: impu}
	reINVITE.Add("Max-Forwards", "68")
	reINVITE.Add("From", "<sip:caller@"+domain+">;tag="+sip.NewTag())
	reINVITE.Add("To", "<"+impu+">;tag="+sip.NewTag())
	reINVITE.Add("Call-ID", sip.NewCallID())
	reINVITE.Add("CSeq", "2 INVITE")
	reINVITE.Add("Contact", "<sip:caller@192.0.2.20>")
	send(t, ue, icscf, reINVITE)

	for _, series := range []string{terminatingAttempts,
		`ims_session_successes_total{role="icscf",direction="terminating",response="180"}`} {
		if got := scraped(t, reg, series); got != 0 {
			t.Errorf("%s = %v, want 0", series, got)
		}
	}
}
