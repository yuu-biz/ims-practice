package pcscf

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestCANCELToTheUEKeepsTheReason is RFC 3326 2 at the P-CSCF: the
// CANCEL that it sends the UE carries the Reason of the one it got, such
// as the SIP cause 408 with which call forwarding on no reply cancels
// the call to the diverting user (TS 24.604 4.5.2.6.3).
func TestCANCELToTheUEKeepsTheReason(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	out, in := h.callUE()
	if err := in.Progress(180, sip.Body{}); err != nil {
		t.Fatal(err)
	}
	reason := sip.NewReason(sip.ReasonSIP, 408, sip.ReasonPhrase(408)).Header()
	if err := out.Cancel(reason); err != nil {
		t.Fatal(err)
	}
	answered(t, out, 487)
	ended(t, "the UE's call", in.Done())
	if got := in.EndedBy().GetAll("Reason"); len(got) != 1 || got[0] != reason.Value {
		t.Errorf("the CANCEL at the UE has Reason %q, want %q", got, reason.Value)
	}
}
