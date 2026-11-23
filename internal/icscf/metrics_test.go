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
// front of a fake S-CSCF that answers 486, and returns the UE's stack,
// the address of the I-CSCF and that registry. The S-CSCF answers no
// 2xx to an INVITE, so that the UE's transaction ends at once instead
// of waiting out Timer M for retransmissions (RFC 3261 13.2.2.4).
func counting(t *testing.T) (*sip.Stack, netip.AddrPort, *metrics.Registry) {
	t.Helper()
	reg := metrics.NewRegistry()
	cfg := Config{HomeDomain: domain, SCSCFs: []SCSCF{{Name: scscf, Capabilities: []uint32{1, 2}}}, Metrics: reg}
	_, ue, icscf := start(t, cfg, registeredAtHSS(t), nil, func(st *sip.ServerTransaction) {
		st.Respond(sip.NewResponse(st.Request, 486, sip.NewTag()))
	})
	return ue, icscf, reg
}

// TS 32.409 4.1.2.11 counts the initial INVITE on receipt, whatever
// becomes of the session afterwards.
func TestInitialINVITEIsATerminatingSessionAttempt(t *testing.T) {
	ue, icscf, reg := counting(t)
	if r := invite(t, ue, icscf, impu); r.StatusCode != 486 {
		t.Fatalf("got %d, want the S-CSCF's 486", r.StatusCode)
	}
	if got := scraped(t, reg, terminatingAttempts); got != 1 {
		t.Errorf("%s = %v, want 1", terminatingAttempts, got)
	}
}

// Only an initial INVITE sets up a session. A REGISTER, a standalone
// request such as a MESSAGE, and a request within a dialog are no
// session establishments.
func TestOnlyAnInitialINVITECountsAsASessionAttempt(t *testing.T) {
	ue, icscf, reg := counting(t)
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

	if got := scraped(t, reg, terminatingAttempts); got != 0 {
		t.Errorf("%s = %v, want 0", terminatingAttempts, got)
	}
}
