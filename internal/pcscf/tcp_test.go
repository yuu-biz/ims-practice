package pcscf

import (
	"strings"
	"testing"
)

// TestLargeRequestOverTCPFromARegisteredUE is RFC 3261 18.1.1: a request
// over 1300 bytes, as a VoLTE INVITE is, goes over TCP from a port of
// the UE's own. The P-CSCF knows the UE by the sent-by of its Via.
func TestLargeRequestOverTCPFromARegisteredUE(t *testing.T) {
	h := newSessionHarness(t)
	m := invite("sip:001010000000002@"+domain, "<sip:pcscf."+domain+":5060;lr>")
	m.Add("Subject", strings.Repeat("x", 1300))
	responses := h.send(h.ue, m)
	got := arrives(t, h.scscf, responses, "S-CSCF")
	if got.Get("P-Asserted-Identity") != "<"+impu+">" {
		t.Errorf("P-Asserted-Identity = %q, want the UE's", got.Get("P-Asserted-Identity"))
	}
	if r := next(t, responses); r.StatusCode != 183 {
		t.Errorf("UE got %d, want the 183", r.StatusCode)
	}
	close(h.release)
}
