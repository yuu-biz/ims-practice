package mmtel_test

import (
	"context"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// This file runs the three-way session of GSMA IR.92 2.3.3 with a peer
// in another network: UE 1 talks with B1, a user of network B, and with
// UE 2 of its own network, and merges the two calls. The focus invites
// each peer on behalf of the conference URI, a PSI that it hosts: the
// S-CSCF serves the PSI in the originating case and routes the INVITE
// as it routes a call of UE 1, through the IBCF for B1.

// peerDomain is the domain of network B, and peerNumber the number of
// B1 there, as in deploy/interconnect.
const (
	peerDomain = "ims.mnc002.mcc001.3gppnetwork.org"
	peerNumber = "+12025550151"
)

// ibcfHost is the IBCF of the network, on its side of the border.
const ibcfHost = "ibcf." + domain

// borderParty is B1 as the network sees it: at the far side of its
// IBCF, which the S-CSCF sends each request for network B to. The
// test leaves the IBCFs and network B out, so B1's UA takes the place
// of the IBCF, and gets each request with the IBCF on top of Route.
func (n *network) borderParty(hz float64) *party {
	n.t.Helper()
	codecs, err := ue.CodecsNamed(ue.VoLTECodecNames)
	if err != nil {
		n.t.Fatal(err)
	}
	p := &party{subscriber: &subscriber{n: n, number: 51, impu: "sip:" + peerNumber + "@" + peerDomain, tel: "tel:" + peerNumber},
		hz: hz, calls: make(chan *sip.IncomingCall, 2)}
	tr, err := sip.ListenObserved("127.0.0.1:0", quiet(), n.wire.observer("IBCF"))
	if err != nil {
		n.t.Fatal(err)
	}
	addr := tr.LocalAddr()
	calls := ue.CallConfig{Codecs: codecs}
	cfg := ue.Config{PrivateIdentity: "001020000000001@" + peerDomain, PublicIdentity: p.impu, HomeDomain: peerDomain, Calls: calls}
	contact := ue.NewContact(cfg, addr)
	p.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy: addr.String(), Contact: contact.URI(),
		OnCall: func(ic *sip.IncomingCall) { p.calls <- ic },
		// Within a dialog, B1's requests follow the Record-Route of the
		// network, the S-CSCF first.
		Locate: func(u sip.URI) ([]sip.Destination, error) { return ims.Targets(context.Background(), n.dns, u) },
	}, calls), quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(p.ua.Close)
	p.ue = ue.New(cfg, p.ua.Stack(), contact, quiet())
	n.dns.add(ibcfHost, addr)
	return p
}

// TestConferenceInvitesAUserOfAnotherNetwork: the focus brings B1 into
// the conference with an INVITE on behalf of the conference URI (TS
// 24.229 5.7.3), which the S-CSCF takes in the originating case (orig),
// translates with ENUM and sends out through the IBCF (5.4.3.2 steps 10
// to 12), as it sent UE 1's call to B1.
func TestConferenceInvitesAUserOfAnotherNetwork(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true, border: true})
	a, c := n.party(1, 500), n.party(2, 1700)
	b := n.borderParty(1100)

	// UE 1 calls B1, which answers; UE 1 holds B1 and calls UE 2.
	ab := a.call(b.tel)
	ba := b.answer(receive(t, b.calls, "UE 1's call at B1"))
	abSession := ab.await(t, "UE 1's call to B1")
	ba.await(t, "B1's call from UE 1")
	if err := abSession.Hold(); err != nil {
		t.Fatalf("hold: %v", err)
	}
	ac := a.call(c.tel)
	ca := c.answer(receive(t, c.calls, "UE 1's call at UE 2"))
	acSession := ac.await(t, "UE 1's call to UE 2")
	ca.await(t, "UE 2's call from UE 1")

	confLeg, ctx := newLeg(a)
	conf, _, err := a.ue.CreateConference(ctx, a.ua, a.reg, confLeg.media, a.speech(), ue.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Subscribe(); err != nil {
		t.Fatalf("SUBSCRIBE to the conference: %v", err)
	}
	merge := func(peer *party, session *ue.Session, call *leg) *leg {
		t.Helper()
		d, ok := session.Dialog()
		if !ok {
			t.Fatal("UE 1's call has no dialog")
		}
		taken := make(chan *leg, 1)
		go func() { taken <- peer.takeReplacement(t, a.impu) }()
		if status, err := conf.Invite(peer.tel, d); err != nil || status != 200 {
			t.Fatalf("REFER for %d got %d, %v; want 200", peer.number, status, err)
		}
		call.hangUp()
		call.awaitEnd(t, "UE 1's old call")
		l := receive(t, taken, "the peer's new leg")
		l.await(t, "the peer's leg of the conference")
		return l
	}
	bConf := merge(b, abSession, ab)
	ba.awaitEnd(t, "B1's old call")
	cConf := merge(c, acSession, ac)
	ca.awaitEnd(t, "UE 2's old call")
	awaitUsers(t, conf, 3)

	// The focus's INVITE to B1: to the S-CSCF with "orig", and from there
	// to the IBCF with the number that ENUM gave, the conference URI as
	// the identity, and the S-CSCF in Record-Route.
	var toSCSCF, toIBCF *sip.Message
	arrivals, _ := n.wire.arrivals()
	for _, r := range arrivals {
		if r.m.Method != sip.INVITE || sip.ReceivedDialogID(r.m).LocalTag != "" || r.m.Get("Replaces") == "" {
			continue
		}
		if _, focus := ue.FocusOf(r.m); !focus {
			continue
		}
		switch {
		case r.at == "S-CSCF" && r.m.RequestURI == b.tel && toSCSCF == nil:
			toSCSCF = r.m
		case r.at == "IBCF" && toIBCF == nil:
			toIBCF = r.m
		}
	}
	if toSCSCF == nil || toIBCF == nil {
		t.Fatalf("the focus's INVITE to B1 reached the S-CSCF (%v) and the IBCF (%v); want both", toSCSCF != nil, toIBCF != nil)
	}
	if route := toSCSCF.Get("Route"); !strings.Contains(route, "scscf."+domain) || !strings.Contains(route, ";orig") {
		t.Errorf("the focus's INVITE came to the S-CSCF with Route %q, want the S-CSCF with orig", route)
	}
	if want := "sip:" + peerNumber + "@" + peerDomain + ";user=phone"; toIBCF.RequestURI != want {
		t.Errorf("the IBCF got Request-URI %q, want %q", toIBCF.RequestURI, want)
	}
	if got, want := toIBCF.Get("P-Asserted-Identity"), "<"+conf.URI+">"; got != want {
		t.Errorf("the IBCF got P-Asserted-Identity %q, want the conference URI %s", got, want)
	}
	if rr := strings.Join(toIBCF.GetAll("Record-Route"), ", "); !strings.Contains(rr, "scscf."+domain) {
		t.Errorf("the IBCF got Record-Route %q, without the S-CSCF", rr)
	}

	// The three hear one another through the mixer.
	for _, m := range []*ue.Media{confLeg.media, bConf.media, cConf.media} {
		awaitPackets(t, m, 75)
	}
	hearsTheOthers(t, a, confLeg.media, b, c)
	hearsTheOthers(t, b, bConf.media, a, c)
	hearsTheOthers(t, c, cConf.media, a, b)

	confLeg.hangUp()
	bConf.awaitEnd(t, "B1's leg, which the focus ends")
	cConf.awaitEnd(t, "UE 2's leg, which the focus ends")
}
