package pcscf

import (
	"net/netip"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// wantLatchings checks the latching of every Modify that the
// termination side of the context took, in order.
func wantLatchings(t *testing.T, relay *fakeRelay, side agw.TerminationID, want ...agw.Latch) {
	t.Helper()
	var got []agw.Latch
	for _, change := range relay.modified("1", side) {
		got = append(got, change.Latch)
	}
	if len(got) != len(want) {
		t.Fatalf("the %s termination took %d Modify with the latchings %q, want %d with %q", side, len(got), got, len(want), want)
	}
	for i, latch := range want {
		if got[i] != latch {
			t.Errorf("Modify %d of the %s termination asks for the latching %q, want %q", i+1, side, got[i], latch)
		}
	}
}

// TestOfferThatMovesTheUEAsksTheAccessTerminationToLatchAgain: the
// access termination latches, so it keeps the address it latched
// whatever a later Remote descriptor says. When the UE moves its media
// in an offer of its own, the P-CSCF asks that termination to latch onto
// the new source of the UE's media, so that it follows a UE behind a NAT
// that the UE's SDP does not show.
func TestOfferThatMovesTheUEAsksTheAccessTerminationToLatchAgain(t *testing.T) {
	h, _, relay := newPolicyHarness(t)
	c := newUpCall(t, h)
	// The INVITE told the access termination where the UE's SDP said,
	// and the Add had already asked it to latch.
	wantLatchings(t, relay, agw.Access, "")

	if resp := reoffer(t, c.out, "re-INVITE", moved); resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE of the UE got %d, want 200", resp.StatusCode)
	}
	wantLatchings(t, relay, agw.Access, "", agw.NAPTRelatch)
	if got := relay.remote("1", agw.Access); got != moved {
		t.Errorf("the access termination sends to %v, want the UE's new address %v", got, moved)
	}
}

// TestOfferThatLeavesTheUEWhereItIsAsksForNoRelatch: an offer of the UE
// that keeps its media where it is moves nothing, so the P-CSCF asks for
// no latching. A latching that waits would otherwise take the
// termination to the next source that sends to it.
func TestOfferThatLeavesTheUEWhereItIsAsksForNoRelatch(t *testing.T) {
	h, _, relay := newPolicyHarness(t)
	c := newUpCall(t, h)
	if resp := reoffer(t, c.out, "re-INVITE", ueMedia); resp.StatusCode != 200 {
		t.Fatalf("the re-INVITE of the UE got %d, want 200", resp.StatusCode)
	}
	wantLatchings(t, relay, agw.Access, "", "")
}

// TestOfferOfTheFarEndAsksTheCoreTerminationForNoLatching: the core
// termination sends where the SDP of the network side says and never
// latches, so neither an offer nor an answer that moves the far end
// asks it for a latching.
func TestOfferOfTheFarEndAsksTheCoreTerminationForNoLatching(t *testing.T) {
	c := newRawCall(t)
	farMoved := netip.MustParseAddrPort("192.0.2.99:6004")
	c.send(c.far, update(c.invite, true, c.ueContact, "1", farMoved))
	<-c.toUE
	if got := c.relay.remote("1", agw.Core); got != farMoved {
		t.Errorf("the core termination sends to %v, want the far end's new address %v", got, farMoved)
	}
	wantLatchings(t, c.relay, agw.Core, "", "")
}

// TestRejectedOfferAsksTheAccessTerminationToLatchBack: an offer of the
// UE that gets no 2xx leaves the session as it was before the request
// (RFC 3261 14.1), so the UE takes its media where the last offer and
// answer said again. The P-CSCF asks the access termination to latch
// again for that move back as it did for the move away, since a
// termination that followed the UE to its new port does not go back for
// a Remote descriptor alone.
func TestRejectedOfferAsksTheAccessTerminationToLatchBack(t *testing.T) {
	h, pcrf, relay := newPolicyHarness(t)
	c := newUpCall(t, h)
	c.in.Handle(func(r *sip.InDialogRequest) { r.Reject(488) })

	if resp := reoffer(t, c.out, "re-INVITE", moved); resp.StatusCode != 488 {
		t.Fatalf("the re-INVITE of the UE got %d, want the 488 of the far end", resp.StatusCode)
	}
	wantLatchings(t, relay, agw.Access, "", agw.NAPTRelatch, agw.NAPTRelatch)
	if got := relay.remote("1", agw.Access); got != ueMedia {
		t.Errorf("the access termination sends to %v after the 488, want %v of the INVITE again", got, ueMedia)
	}
	wantCallUp(t, h, pcrf, relay, c.key)
	hangUp(t, h, c.out, pcrf, relay, c.key)
}
