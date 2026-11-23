package ibcf_test

import (
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file runs an INVITE with Replaces across the border (RFC 3891).
// The Replaces names a dialog as the IBCF has it on the side where the
// INVITE comes in, and each IBCF passes it on naming the dialog of the
// other side of that call (TS 24.229 5.10.5), so that the UA at the far
// end finds its own dialog.

// awaitDialogs waits until the record of the one call of instance i of
// n has both dialogs, which the IBCF writes once the 2xx has its ACK.
func (n *network) awaitDialogs(t *testing.T, i int) {
	t.Helper()
	instance, _ := n.plan.instanceHosts(i)
	deadline := time.Now().Add(5 * time.Second)
	for {
		records := n.callsOf(t, instance)
		if len(records) == 1 && len(records[0].DialogIDs()) == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("records of %s: %+v, want one call with both dialogs", instance, records)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// replacesOf is the Replaces that names d at the other end of d (RFC
// 3891 4): the far end's tag is d's remote tag.
func replacesOf(d sip.Dialog) sip.Replaces {
	return sip.Replaces{CallID: d.ID.CallID, ToTag: d.ID.RemoteTag, FromTag: d.ID.LocalTag}
}

// Network A's UE calls network B's UE through the first instance of
// IBCF-A, and then sends a new INVITE with a Replaces of that call
// through the second instance, which holds nothing of the call. Each
// IBCF finds the call in its Store, the second instance of IBCF-A a
// call of the first one, and passes the Replaces on with the dialog of
// its other side. Network B's UE takes the new call in the place of the
// first and ends that one, as RFC 3891 3 has it.
func TestReplacesNamesTheDialogOfTheNextHop(t *testing.T) {
	for name, start := range map[string]func(*testing.T) *pair{
		"memory store":    func(t *testing.T) *pair { return newPair(t, nil) },
		"shared kv store": newPairOnKV,
	} {
		t.Run(name, func(t *testing.T) { replacesNamesTheDialogOfTheNextHop(t, start(t)) })
	}
}

func replacesNamesTheDialogOfTheNextHop(t *testing.T, p *pair) {
	first := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	answered := p.b.nextCall(t)
	answered.Handle(p.b.answerInDialog(nil))
	go answered.Accept(p.b.answer(t, answered.Request))
	if resp := final(t, first); resp.StatusCode != 200 {
		t.Fatalf("the first call got %d", resp.StatusCode)
	}
	p.a.awaitDialogs(t, 0)
	p.b.awaitDialogs(t, 0)
	dialogA, _ := first.Dialog()
	dialogB, _ := answered.Dialog()

	p.a.use(1)
	replaces := replacesOf(dialogA)
	second := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil, replaces.Header())
	replacement := p.b.nextCall(t)
	replacement.Handle(p.b.answerInDialog(nil))
	replaced, refusal := replacement.ReplacedDialog()
	if refusal != 0 {
		t.Fatalf("network B's UE refused the replacement with %d: Replaces %q, its dialog %+v",
			refusal, replacement.Request.Get("Replaces"), dialogB.ID)
	}
	if got, _ := replaced.Dialog(); got.ID != dialogB.ID {
		t.Fatalf("the Replaces names the dialog %+v, want the first call's %+v", got.ID, dialogB.ID)
	}
	if got := replacement.Request.Get("Replaces"); strings.Contains(got, dialogA.ID.CallID) {
		t.Errorf("network B's UE got the Call-ID of network A: Replaces %q", got)
	}

	// The second instance of IBCF-A got the Replaces as network A has it,
	// and the link carried that of the dialog between the IBCFs.
	if got := p.a.rec.requests("ibcf2-internal", sip.INVITE); len(got) != 1 || got[0].Get("Replaces") != replaces.String() {
		t.Fatalf("IBCF-A's second instance got the INVITEs %v, want one with Replaces %q", got, replaces.String())
	}
	crossed := p.b.rec.requests("ibcf1-external", sip.INVITE)
	if len(crossed) != 2 {
		t.Fatalf("network B's IBCF received %d INVITEs, want 2", len(crossed))
	}
	link, err := sip.ParseReplaces(crossed[1].Get("Replaces"))
	if err != nil || link.CallID != crossed[0].Get("Call-ID") || link.FromTag != tagOf(t, crossed[0].Get("From")) {
		t.Errorf("the Replaces on the link is %q, want the first call's dialog on the link (Call-ID %s)",
			crossed[1].Get("Replaces"), crossed[0].Get("Call-ID"))
	}

	go replacement.Accept(p.b.answer(t, replacement.Request))
	if resp := final(t, second); resp.StatusCode != 200 {
		t.Fatalf("the replacement got %d", resp.StatusCode)
	}
	if err := replaced.Bye(); err != nil {
		t.Fatalf("BYE in the replaced dialog: %v", err)
	}
	waitDone(t, "the first call at network A's UE", first.Done())
	if err := second.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the replacement at network B's UE", replacement.Done())
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
	assertHidden(t, p.a, planB)
}

// A Replaces that names no call of the IBCFs crosses as it came, as TS
// 24.229 5.7.5.1 has a routeing B2BUA pass on one that names no dialog
// of its own: a node beyond the IBCF may hold the dialog. Here none
// does, and network B's UE answers 481 (RFC 3891 3).
func TestAReplacesOfNoCallCrossesAsItCame(t *testing.T) {
	p := newPair(t, nil)
	replaces := sip.Replaces{CallID: "no-such-call", ToTag: "a1b2", FromTag: "c3d4", EarlyOnly: true}
	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil, replaces.Header())
	in := p.b.nextCall(t)
	if got := in.Request.Get("Replaces"); got != replaces.String() {
		t.Errorf("network B's UE got Replaces %q, want %q as it was sent", got, replaces.String())
	}
	_, refusal := in.ReplacedDialog()
	if refusal != 481 {
		t.Fatalf("ReplacedDialog at network B's UE = %d, want 481", refusal)
	}
	in.Reject(refusal)
	if resp := final(t, call); resp.StatusCode != 481 {
		t.Errorf("the INVITE got %d, want the 481 of network B's UE", resp.StatusCode)
	}
	p.waitReleased(t)
}
