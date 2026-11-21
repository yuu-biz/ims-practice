package sdp

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/precondition"
)

// TestPreconditionsThroughSDP runs the INVITE and 183 of the VoLTE flow
// (docs/guide/en/volte.md) through real SDP text.
func TestPreconditionsThroughSDP(t *testing.T) {
	a := precondition.NewSegmented(precondition.Originating, precondition.Mandatory, precondition.Optional)
	b := precondition.NewSegmented(precondition.Terminating, precondition.Mandatory, precondition.Optional)

	offer, _ := AudioOffer("1", netip.MustParseAddrPort("172.28.0.100:40000"), VoLTECodecs())
	offer.AudioMedia().SetPreconditions(a.Offer())
	text := string(offer.Bytes())
	for _, l := range []string{"a=curr:qos local none\r\n", "a=curr:qos remote none\r\n",
		"a=des:qos mandatory local sendrecv\r\n", "a=des:qos optional remote sendrecv\r\n"} {
		if !strings.Contains(text, l) {
			t.Errorf("INVITE SDP lacks %q:\n%s", l, text)
		}
	}

	received, err := Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	offered, err := received.AudioMedia().Preconditions()
	if err != nil {
		t.Fatal(err)
	}
	answer, err := AudioAnswer(received, "2", netip.MustParseAddrPort("172.28.0.101:40000"), VoLTECodecs())
	if err != nil {
		t.Fatal(err)
	}
	lines, err := b.Answer(offered)
	if err != nil {
		t.Fatal(err)
	}
	answer.AudioMedia().SetPreconditions(lines)
	if !strings.Contains(string(answer.Bytes()), "a=conf:qos remote sendrecv\r\n") {
		t.Errorf("183 SDP lacks the confirmation request:\n%s", answer.Bytes())
	}
	back, _ := Parse(answer.Bytes())
	got, err := back.AudioMedia().Preconditions()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyAnswer(got); err != nil {
		t.Fatal(err)
	}
	a.SetReserved(precondition.SendRecv, true)
	if !a.NeedsOffer() {
		t.Error("A does not see the confirmation B asked for in the SDP")
	}

	// Writing again replaces the lines rather than adding to them.
	m := back.AudioMedia()
	m.SetPreconditions(a.Offer())
	if n := strings.Count(string(back.Bytes()), "a=curr:"); n != 2 {
		t.Errorf("%d a=curr lines after a second SetPreconditions", n)
	}
	if _, err := (Media{Attributes: []Attribute{{Name: "curr", Value: "qos nowhere none"}}}).Preconditions(); err == nil {
		t.Error("a bad a=curr was accepted")
	}
}
