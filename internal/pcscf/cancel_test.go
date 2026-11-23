package pcscf

import (
	"context"
	"testing"
	"time"

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

// TestCANCELThatMatchesNothingGoesOnStatelessly is RFC 3261 16.10 at the
// P-CSCF: a CANCEL that matches no transaction goes on statelessly, as
// an ACK does. One from the UE follows its Route to the S-CSCF, and one
// for the UE goes to the address that the UE registered from.
func TestCANCELThatMatchesNothingGoesOnStatelessly(t *testing.T) {
	capture := func() (*sip.Stack, chan *sip.Message) {
		got := make(chan *sip.Message, 1)
		return newStackConfig(t, sip.Config{OnRequest: func(*sip.ServerTransaction) {},
			OnCANCEL: func(m *sip.Message) { got <- m }}), got
	}
	scscf, toNetwork := capture()
	ue, toUE := capture()
	locate := locatorFunc(func(string) []sip.Destination {
		return []sip.Destination{{Transport: sip.UDP, Addr: scscf.LocalAddr()}}
	})
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain}, locate, NewMemoryStore(), nil, quiet())
	p.SetStack(newPCSCFStack(t, p))
	contact := "sip:001010000000001@" + ue.LocalAddr().String()
	p.store.PutAssociation(context.Background(), Association{
		Source:           sip.Destination{Transport: sip.UDP, Addr: ue.LocalAddr()},
		SentBy:           ue.LocalAddr().String(),
		Contact:          contact,
		PrivateIdentity:  impi,
		PublicIdentities: []string{impu},
		Expires:          time.Now().Add(time.Hour),
	})
	cancel := func(requestURI string, routes ...string) *sip.Message {
		m := &sip.Message{Method: sip.CANCEL, RequestURI: requestURI}
		m.Add("Via", "SIP/2.0/UDP 192.0.2.20:5060;branch="+sip.NewBranch())
		for _, r := range routes {
			m.Add("Route", r)
		}
		m.Add("Max-Forwards", "70")
		m.Add("From", "<"+impu+">;tag=caller")
		m.Add("To", "<sip:001010000000002@"+domain+">")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 CANCEL")
		return m
	}
	own, orig := "<sip:pcscf."+domain+":5060;lr>", "<sip:orig@scscf."+domain+":5060;lr>"
	for _, c := range []struct {
		name   string
		cancel *sip.Message
		at     chan *sip.Message
		route  []string
	}{
		{"from the UE", cancel("sip:001010000000002@"+domain, own, orig), toNetwork, []string{orig}},
		{"for the UE", cancel(contact, own), toUE, nil},
	} {
		p.OnCANCEL(c.cancel)
		select {
		case got := <-c.at:
			if routes := got.GetAll("Route"); len(routes) != len(c.route) || (len(routes) == 1 && routes[0] != c.route[0]) {
				t.Errorf("%s: forwarded CANCEL has Route %q, want %q", c.name, routes, c.route)
			}
			if got.Get("Max-Forwards") != "69" {
				t.Errorf("%s: forwarded CANCEL has Max-Forwards %q, want 69", c.name, got.Get("Max-Forwards"))
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the CANCEL was not forwarded", c.name)
		}
	}
}
