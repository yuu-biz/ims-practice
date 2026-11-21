package precondition

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// attrs reads attribute lines as they appear in SDP, without "a=".
func attrs(t *testing.T, lines ...string) Attributes {
	t.Helper()
	var a Attributes
	for _, l := range lines {
		name, value, _ := strings.Cut(l, ":")
		var err error
		switch name {
		case "curr":
			var c Current
			c, err = ParseCurrent(value)
			a.Current = append(a.Current, c)
		case "des":
			var d Desired
			d, err = ParseDesired(value)
			a.Desired = append(a.Desired, d)
		case "conf":
			var c Confirm
			c, err = ParseConfirm(value)
			a.Confirm = append(a.Confirm, c)
		default:
			t.Fatalf("not a precondition attribute: %q", l)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return a
}

// lines writes a back as SDP attribute lines, in the package's order.
func lines(a Attributes) []string {
	var out []string
	for _, c := range a.Current {
		out = append(out, "curr:"+c.String())
	}
	for _, d := range a.Desired {
		out = append(out, "des:"+d.String())
	}
	for _, c := range a.Confirm {
		out = append(out, "conf:"+c.String())
	}
	return out
}

func expect(t *testing.T, step string, got Attributes, want ...string) {
	t.Helper()
	if g := lines(got); !slices.Equal(g, want) {
		t.Errorf("%s:\ngot  %q\nwant %q", step, g, want)
	}
}

func TestParseAndFormat(t *testing.T) {
	for _, v := range []string{"curr:qos local sendrecv", "curr:qos e2e none", "des:qos mandatory remote send",
		"des:qos failure e2e send", "des:foo unknown e2e send", "conf:qos remote sendrecv"} {
		if got := lines(attrs(t, v)); len(got) != 1 || got[0] != v {
			t.Errorf("%q came back as %q", v, got)
		}
	}
	for _, v := range []string{"qos local", "qos middle none", "qos local both"} {
		if _, err := ParseCurrent(v); err == nil {
			t.Errorf("ParseCurrent(%q) accepted it", v)
		}
	}
	if _, err := ParseDesired("qos maybe local none"); err == nil {
		t.Error("ParseDesired accepted an unknown strength-tag")
	}
}

// TestVoLTEFlow is the VoLTE call of docs/guide/en/volte.md: both
// UEs use the segmented status type and learn their own reservation
// from a dedicated bearer.
func TestVoLTEFlow(t *testing.T) {
	// TS 24.229 6.1.2: the local segment is mandatory, the remote one
	// optional.
	a := NewSegmented(Originating, Mandatory, Optional)
	b := NewSegmented(Terminating, Mandatory, Optional)

	invite := a.Offer()
	expect(t, "INVITE", invite,
		"curr:qos local none", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos optional remote sendrecv")

	ringing, err := b.Answer(invite)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "183", ringing,
		"curr:qos local none", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv",
		"conf:qos remote sendrecv")
	if err := a.ApplyAnswer(ringing); err != nil {
		t.Fatal(err)
	}

	// The bearers come after each P-CSCF has seen the answer. B is not
	// met: A's network is still unknown to it.
	b.SetReserved(SendRecv, true)
	if b.Met() || b.NeedsOffer() {
		t.Errorf("B after its bearer: met %v, needs offer %v; want neither", b.Met(), b.NeedsOffer())
	}
	if a.NeedsOffer() {
		t.Error("A needs an offer before its bearer")
	}
	a.SetReserved(SendRecv, true)
	if !a.NeedsOffer() {
		t.Fatal("A must offer once the rows B asked about are reserved (RFC 3312 7)")
	}

	update := a.Offer()
	expect(t, "UPDATE", update,
		"curr:qos local sendrecv", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
	if a.NeedsOffer() {
		t.Error("A still needs an offer after sending it")
	}

	ok, err := b.Answer(update)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "200 (UPDATE)", ok,
		"curr:qos local sendrecv", "curr:qos remote sendrecv",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
	if !b.Met() {
		t.Error("B is not met after the UPDATE, so it cannot send 180")
	}
	if err := a.ApplyAnswer(ok); err != nil {
		t.Fatal(err)
	}
	if !a.Met() {
		t.Error("A is not met after the 200 (UPDATE)")
	}

	// Losing the bearer crosses the threshold again (RFC 3312 7).
	a.SetReserved(SendRecv, false)
	if !a.NeedsOffer() || a.Met() {
		t.Errorf("A after losing its bearer: needs offer %v, met %v", a.NeedsOffer(), a.Met())
	}
}

// TestBearerBeforeAnswer covers A's bearer arriving before the 183:
// the threshold is already reached when B asks for confirmation.
func TestBearerBeforeAnswer(t *testing.T) {
	a := NewSegmented(Originating, Mandatory, Optional)
	b := NewSegmented(Terminating, Mandatory, Optional)
	invite := a.Offer()
	a.SetReserved(SendRecv, true)
	ringing, _ := b.Answer(invite)
	if err := a.ApplyAnswer(ringing); err != nil {
		t.Fatal(err)
	}
	if !a.NeedsOffer() {
		t.Error("A does not offer, although B asked about rows that are already reserved")
	}
	// The 183 only echoes A's "none" from the INVITE; A's own row stays reserved.
	expect(t, "UPDATE", a.Offer(),
		"curr:qos local sendrecv", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
}

// TestOfferInResponse is RFC 3312 6 with an INVITE without SDP: the
// terminating UA offers in a reliable 1xx and the originating UA answers
// in the PRACK. The originating UA still reports its reservation in the
// next offer (TS 24.229 5.1.3.1).
func TestOfferInResponse(t *testing.T) {
	a := NewSegmented(Originating, Mandatory, Optional)
	b := NewSegmented(Terminating, Mandatory, Optional)
	offer := b.Offer()
	expect(t, "183", offer,
		"curr:qos local none", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos optional remote sendrecv")
	prack, err := a.Answer(offer)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "PRACK", prack,
		"curr:qos local none", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
	if err := b.ApplyAnswer(prack); err != nil {
		t.Fatal(err)
	}
	b.SetReserved(SendRecv, true)
	if b.Met() {
		t.Error("B is met before it knows about A's network")
	}
	a.SetReserved(SendRecv, true)
	if !a.NeedsOffer() {
		t.Fatal("A does not report its reservation")
	}
	update, _ := b.Answer(a.Offer())
	expect(t, "200 (UPDATE)", update,
		"curr:qos local sendrecv", "curr:qos remote sendrecv",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
	if !b.Met() {
		t.Error("B is not met after the UPDATE")
	}
}

// TestSegmentedExample is RFC 3312 13.2: A has reserved before it
// offers, B reserves before it answers, and nobody needs to confirm.
func TestSegmentedExample(t *testing.T) {
	a := NewSegmented(Originating, Mandatory, Mandatory)
	a.SetReserved(SendRecv, true)
	sdp1 := a.Offer()
	expect(t, "SDP1", sdp1,
		"curr:qos local sendrecv", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")

	b := NewSegmented(Terminating, Mandatory, Mandatory)
	b.SetReserved(SendRecv, true)
	sdp2, err := b.Answer(sdp1)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "SDP2", sdp2,
		"curr:qos local sendrecv", "curr:qos remote sendrecv",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
	if !b.Met() {
		t.Error("B is not met, so it cannot send 180 with SDP2")
	}
	if err := a.ApplyAnswer(sdp2); err != nil {
		t.Fatal(err)
	}
	expect(t, "SDP3", a.Offer(),
		"curr:qos local sendrecv", "curr:qos remote sendrecv",
		"des:qos mandatory local sendrecv", "des:qos mandatory remote sendrecv")
}

// TestEndToEndExample is RFC 3312 13.1: each side learns the
// reservation of its send direction from RSVP, and B asks A to confirm
// the other direction.
func TestEndToEndExample(t *testing.T) {
	a := NewEndToEnd(Originating, Mandatory, Send)
	b := NewEndToEnd(Terminating, Mandatory, Send)

	sdp1 := a.Offer()
	expect(t, "SDP1", sdp1, "curr:qos e2e none", "des:qos mandatory e2e sendrecv")
	sdp2, err := b.Answer(sdp1)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "SDP2", sdp2, "curr:qos e2e none", "des:qos mandatory e2e sendrecv", "conf:qos e2e recv")
	if err := a.ApplyAnswer(sdp2); err != nil {
		t.Fatal(err)
	}

	b.SetReserved(Send, true)
	a.SetReserved(Send, true)
	if !a.NeedsOffer() {
		t.Fatal("A must send SDP3 once its send direction is reserved")
	}
	sdp3 := a.Offer()
	expect(t, "SDP3", sdp3, "curr:qos e2e send", "des:qos mandatory e2e sendrecv")
	sdp4, err := b.Answer(sdp3)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "SDP4", sdp4, "curr:qos e2e sendrecv", "des:qos mandatory e2e sendrecv")
	if !b.Met() {
		t.Error("B is not met after SDP4, so it cannot send 180")
	}
}

// TestAnswerUpgradesAndInverts uses the segmented example of RFC 3312
// 5.1.1 (Table 2). The answerer inverts every tag (Table 4), may raise
// a strength but never lower it (5.2), and writes two a=des lines for
// a segment whose directions differ (5.1.1).
func TestAnswerUpgradesAndInverts(t *testing.T) {
	offer := attrs(t, "curr:qos local none", "curr:qos remote none",
		"des:qos optional remote send", "des:qos none remote recv", "des:qos none local sendrecv")
	b := NewSegmented(Terminating, StrengthNone, StrengthNone)
	answer, err := b.Answer(offer)
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "answer", answer,
		"curr:qos local none", "curr:qos remote none",
		"des:qos none local send", "des:qos optional local recv",
		"des:qos none remote sendrecv")

	b2 := NewSegmented(Terminating, Mandatory, StrengthNone)
	answer, _ = b2.Answer(offer)
	expect(t, "answer that upgrades", answer,
		"curr:qos local none", "curr:qos remote none",
		"des:qos mandatory local sendrecv", "des:qos none remote sendrecv")
}

// TestOfferWithoutPreconditions is IR.92 2.4.1: the terminating UE does
// not assume the caller uses preconditions, answers without them, and
// still waits for its own resources before alerting.
func TestOfferWithoutPreconditions(t *testing.T) {
	b := NewSegmented(Terminating, Mandatory, Optional)
	answer, err := b.Answer(Attributes{})
	if err != nil || !answer.Empty() {
		t.Fatalf("Answer(no preconditions) = %q, %v; want none", lines(answer), err)
	}
	if b.Met() {
		t.Error("met before its own bearer")
	}
	b.SetReserved(SendRecv, true)
	if !b.Met() {
		t.Error("not met after its own bearer")
	}
	if !b.Offer().Empty() {
		t.Error("a later offer carries preconditions the caller does not use")
	}
}

func TestUnknownPrecondition(t *testing.T) {
	b := NewSegmented(Terminating, Mandatory, Optional)
	// RFC 3312 9: the offerer meets a mandatory precondition on its own network alone.
	if _, err := b.Answer(attrs(t, "curr:qos local none", "des:qos mandatory local sendrecv",
		"curr:foo local none", "des:foo mandatory local sendrecv")); err != nil {
		t.Errorf("refused an unknown precondition on the offerer's network: %v", err)
	}
	_, err := b.Answer(attrs(t, "curr:foo e2e none", "des:foo mandatory e2e sendrecv"))
	if !errors.Is(err, ErrUnknownPrecondition) {
		t.Errorf("err = %v, want ErrUnknownPrecondition", err)
	}
}

func TestMismatchedStatusType(t *testing.T) {
	b := NewSegmented(Terminating, Mandatory, Optional)
	if _, err := b.Answer(attrs(t, "curr:qos e2e none", "des:qos mandatory e2e sendrecv")); err == nil {
		t.Error("a segmented table accepted an end-to-end offer")
	}
}
