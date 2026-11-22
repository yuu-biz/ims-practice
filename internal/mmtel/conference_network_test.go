package mmtel_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// This file runs the three-way session of GSMA IR.92 2.3.3 through the
// whole IMS: UE A calls B, holds, calls C and merges the two calls into
// a conference (TS 24.147 5.3.1.3.3, option 2b), and the three hear one
// another through the mixer of the MRFP.

// party is a UE of the conference test: the UE simulator on a UA set up
// as ue serve sets it up, with the VoLTE codecs, and the calls it gets.
type party struct {
	*subscriber
	hz    float64
	calls chan *sip.IncomingCall
}

func (n *network) party(k int, hz float64) *party {
	n.t.Helper()
	codecs, err := ue.CodecsNamed(ue.VoLTECodecNames)
	if err != nil {
		n.t.Fatal(err)
	}
	id := "00101000000000" + string(rune('0'+k))
	p := &party{subscriber: &subscriber{n: n, number: k, impu: "sip:" + id + "@" + domain, tel: "tel:+1202555010" + string(rune('0'+k))},
		hz: hz, calls: make(chan *sip.IncomingCall, 2)}
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	addr := tr.LocalAddr()
	calls := ue.CallConfig{Codecs: codecs}
	cfg := ue.Config{PrivateIdentity: id + "@" + domain, PublicIdentity: p.impu, Password: "test-password-" + string(rune('0'+k)),
		HomeDomain: domain, PCSCF: n.pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
	contact := ue.NewContact(cfg, addr)
	p.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy: addr.String(), Contact: contact.URI(),
		OnCall: func(ic *sip.IncomingCall) { p.calls <- ic },
		Locate: func(sip.URI) (sip.Destination, error) { return sip.Destination{Transport: sip.UDP, Addr: n.pcscf}, nil },
	}, calls), quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(p.ua.Close)
	p.ue = ue.New(cfg, p.ua.Stack(), contact, quiet())
	if p.reg, err = p.ue.Register(context.Background()); err != nil {
		n.t.Fatal(err)
	}
	return p
}

// speech is the party's own tone, a third of full scale so that the mix
// of two does not clip, long enough for the whole test.
func (p *party) speech() ue.Speech {
	pcm := ue.Tone(p.hz, 30*time.Second)
	for i := range pcm {
		pcm[i] /= 3
	}
	return ue.Speech{PCM: pcm}
}

func (p *party) media() *ue.Media {
	p.n.t.Helper()
	m, err := ue.ListenMedia("127.0.0.1:0")
	if err != nil {
		p.n.t.Fatal(err)
	}
	p.n.t.Cleanup(func() { m.Close() })
	return m
}

// leg is a call of a party that runs in the background until its
// context ends or the peer hangs up.
type leg struct {
	media   *ue.Media
	session chan *ue.Session
	active  chan struct{}
	hangUp  context.CancelFunc
	ended   chan error
}

func newLeg(p *party) (*leg, context.Context) {
	ctx, cancel := context.WithCancel(context.Background())
	p.n.t.Cleanup(cancel)
	return &leg{media: p.media(), session: make(chan *ue.Session, 1), active: make(chan struct{}), hangUp: cancel, ended: make(chan error, 1)}, ctx
}

func (l *leg) options() (ue.Progress, func(*ue.Session)) {
	var once bool
	return func(state ue.CallState, _ string) {
			if state == ue.CallActive && !once {
				once = true
				close(l.active)
			}
		}, func(s *ue.Session) {
			select {
			case l.session <- s:
			default:
			}
		}
}

// call has p call target.
func (p *party) call(target string) *leg {
	l, ctx := newLeg(p)
	progress, session := l.options()
	go func() {
		l.ended <- p.ue.Call(ctx, p.ua, p.reg, target, l.media, p.speech(), ue.CallOptions{StayUp: true, Progress: progress, Session: session})
	}()
	return l
}

// answer has p answer ic at once.
func (p *party) answer(ic *sip.IncomingCall) *leg {
	l, ctx := newLeg(p)
	progress, session := l.options()
	go func() {
		l.ended <- p.ue.Answer(ctx, ic, l.media, p.speech(), ue.AnswerOptions{PickUp: time.After(0), Progress: progress, Session: session})
	}()
	return l
}

// takeReplacement is TS 24.147 5.3.1.4.3 at a peer of A: the focus's
// INVITE with Replaces of the call with A. The peer answers it in the
// place of that call, and then ends the call (RFC 3891 3).
func (p *party) takeReplacement(t *testing.T, peerOfOld string) *leg {
	t.Helper()
	ic := receive(t, p.calls, "the focus's INVITE")
	focus, ok := ue.FocusOf(ic.Request)
	if !ok {
		t.Errorf("the focus's INVITE has Contact %q, want isfocus", ic.Request.Get("Contact"))
	}
	// The conference URI is a public service identity of the home
	// network's subdomain of conferences, with no port (TS 23.003 13.5),
	// so it names no node of the network.
	if u, err := sip.ParseURI(focus); ok && (err != nil || u.Host != "conf."+domain || u.Port != 0) {
		t.Errorf("the conference URI is %q, want one in conf.%s without a port", focus, domain)
	}
	old, refusal := ue.Replacement(ic, peerOfOld)
	if refusal != 0 {
		ic.Reject(refusal)
		t.Fatalf("replacement refused with %d (Replaces %q, Referred-By %q)", refusal, ic.Request.Get("Replaces"), ic.Request.Get("Referred-By"))
	}
	l := p.answer(ic)
	go func() {
		<-l.active
		old.Bye()
	}()
	return l
}

func (l *leg) await(t *testing.T, what string) *ue.Session {
	t.Helper()
	select {
	case <-l.active:
	case err := <-l.ended:
		t.Fatalf("%s ended before it was answered: %v", what, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s was not answered", what)
	}
	return <-l.session
}

func (l *leg) awaitEnd(t *testing.T, what string) {
	t.Helper()
	select {
	case err := <-l.ended:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("%s ended with %v", what, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not end", what)
	}
}

// power is the share of the signal at hz (the Goertzel algorithm).
func power(samples []int16, hz float64) float64 {
	k := 2 * math.Cos(2*math.Pi*hz/8000)
	var s1, s2 float64
	for _, x := range samples {
		s1, s2 = float64(x)+k*s1-s2, s1
	}
	n := float64(len(samples))
	return (s1*s1 + s2*s2 - k*s1*s2) / (n * n)
}

// hearsTheOthers checks what a party received on its leg of the conference: the
// tones of the others and not its own, which the mixer leaves out (TS
// 23.333 6.2.9.1).
func hearsTheOthers(t *testing.T, who *party, m *ue.Media, others ...*party) {
	t.Helper()
	payloads, packets := m.Received()
	if packets < 25 {
		t.Fatalf("%d got %d packets of the conference", who.number, packets)
	}
	samples := g711.Decode(payloads)
	own := power(samples, who.hz)
	for _, o := range others {
		if p := power(samples, o.hz); p < 1e4 || p < 20*own {
			t.Errorf("%d hears %d at %.0f Hz with power %.0f, and itself at %.0f", who.number, o.number, o.hz, p, own)
		}
	}
}

// awaitPackets waits until m has received n packets of speech.
func awaitPackets(t *testing.T, m *ue.Media, n int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, packets := m.Received()
		if packets >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a party got %d packets of the conference, want %d", packets, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// awaitUsers waits until the conference document of c lists want users
// as connected, and returns the users.
func awaitUsers(t *testing.T, c *ue.Conference, want int) []ue.ConferenceUser {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		changed := c.Changed()
		users := c.Users()
		connected := 0
		for _, u := range users {
			if u.Status == "connected" {
				connected++
			}
		}
		if connected == want {
			return users
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("the conference lists %v, want %d users connected", users, want)
		}
	}
}

func TestThreeWayConference(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	a, b, c := n.party(1, 500), n.party(2, 1100), n.party(3, 1700)

	// A calls B, and B answers.
	ab := a.call(b.tel)
	ba := b.answer(receive(t, b.calls, "A's call at B"))
	abSession := ab.await(t, "A's call to B")
	ba.await(t, "B's call from A")
	// A holds B (TS 24.610) and calls C.
	if err := abSession.Hold(); err != nil {
		t.Fatalf("hold: %v", err)
	}
	ac := a.call(c.tel)
	ca := c.answer(receive(t, c.calls, "A's call at C"))
	acSession := ac.await(t, "A's call to C")
	ca.await(t, "C's call from A")

	// A merges: the conference at the factory URI (TS 24.147 5.3.1.3.2),
	// then a REFER for each peer within the dialog with the focus.
	confLeg, ctx := newLeg(a)
	conf, confEnded, err := a.ue.CreateConference(ctx, a.ua, a.reg, confLeg.media, a.speech(), ue.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Subscribe(); err != nil {
		t.Fatalf("SUBSCRIBE to the conference: %v", err)
	}
	awaitUsers(t, conf, 1)

	merge := func(peer *party, session *ue.Session, call *leg) *leg {
		t.Helper()
		d, ok := session.Dialog()
		if !ok {
			t.Fatal("A's call has no dialog")
		}
		taken := make(chan *leg, 1)
		go func() { taken <- peer.takeReplacement(t, a.impu) }()
		status, err := conf.Invite(peer.tel, d)
		if err != nil || status != 200 {
			t.Fatalf("REFER for %d got %d, %v; want 200", peer.number, status, err)
		}
		// 5.3.1.3.3 step 3: A ends its call with the peer, unless the
		// peer's BYE came first, as it does here through the B2BUAs.
		call.hangUp()
		call.awaitEnd(t, "A's old call")
		l := receive(t, taken, "the peer's new leg")
		l.await(t, "the peer's leg of the conference")
		return l
	}
	bConf := merge(b, abSession, ab)
	ba.awaitEnd(t, "B's old call")
	cConf := merge(c, acSession, ac)
	ca.awaitEnd(t, "C's old call")

	users := awaitUsers(t, conf, 3)
	t.Logf("conference users: %v", users)
	if cs := n.mrfp.Contexts(); len(cs) != 1 || len(cs[0].Terminations) != 3 {
		t.Fatalf("the MRFP holds %v, want one context with the three parties", cs)
	}
	for _, l := range []*leg{confLeg, bConf, cConf} {
		if s, ok := l.media.Stream(); !ok || s.Codec.Name != "PCMU" {
			t.Errorf("a leg of the conference runs %v, want PCMU, the codec of the mixer", s.Codec)
		}
	}

	// Let the mix run for 75 packets, 1.5 s of it, then check what each
	// party heard.
	for _, m := range []*ue.Media{confLeg.media, bConf.media, cConf.media} {
		awaitPackets(t, m, 75)
	}
	hearsTheOthers(t, a, confLeg.media, b, c)
	hearsTheOthers(t, b, bConf.media, a, c)
	hearsTheOthers(t, c, cConf.media, a, b)

	// C leaves (5.3.1.6.1): the conference goes on with two.
	cConf.hangUp()
	cConf.awaitEnd(t, "C's leg of the conference")
	awaitUsers(t, conf, 2)

	// A, the creator, leaves: the focus ends the conference (5.3.2.7).
	confLeg.hangUp()
	if err := receive(t, confEnded, "A's conference session"); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("A's conference session ended with %v", err)
	}
	bConf.awaitEnd(t, "B's leg, which the focus ends")
	deadline := time.Now().Add(5 * time.Second)
	for len(n.mrfp.Contexts()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the MRFP still holds %v", n.mrfp.Contexts())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRejoinWithTheConferenceURI is TS 24.147 5.3.1.4.1 and 5.3.2.4.1:
// B, whom the focus brought into A's conference, leaves, and comes back
// with an INVITE to the conference URI, which the focus's INVITE had in
// its Contact. The INVITE goes as any call of B goes: through B's
// originating services at the AS, then from the S-CSCF by DNS to the
// focus.
func TestRejoinWithTheConferenceURI(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	a, b := n.party(1, 500), n.party(2, 1100)

	ab := a.call(b.tel)
	ba := b.answer(receive(t, b.calls, "A's call at B"))
	abSession := ab.await(t, "A's call to B")
	ba.await(t, "B's call from A")
	confLeg, ctx := newLeg(a)
	conf, confEnded, err := a.ue.CreateConference(ctx, a.ua, a.reg, confLeg.media, a.speech(), ue.CallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conf.Subscribe(); err != nil {
		t.Fatalf("SUBSCRIBE to the conference: %v", err)
	}
	d, ok := abSession.Dialog()
	if !ok {
		t.Fatal("A's call has no dialog")
	}
	taken := make(chan *leg, 1)
	go func() { taken <- b.takeReplacement(t, a.impu) }()
	if status, err := conf.Invite(b.tel, d); err != nil || status != 200 {
		t.Fatalf("REFER for B got %d, %v; want 200", status, err)
	}
	ab.hangUp()
	ab.awaitEnd(t, "A's old call")
	bConf := receive(t, taken, "B's leg of the conference")
	bConf.await(t, "B's leg of the conference")
	ba.awaitEnd(t, "B's old call")
	before := awaitUsers(t, conf, 2)

	// B leaves (5.3.1.6.1); the conference goes on with A.
	bConf.hangUp()
	bConf.awaitEnd(t, "B's leg of the conference")
	awaitUsers(t, conf, 1)

	// B comes back with the conference URI.
	back, ctx := newLeg(b)
	progress, session := back.options()
	answered := make(chan *sip.Message, 1)
	go func() {
		back.ended <- b.ue.Call(ctx, b.ua, b.reg, conf.URI, back.media, b.speech(), ue.CallOptions{StayUp: true, Progress: progress, Session: session,
			Answered: func(_ *sip.OutgoingCall, ok *sip.Message) { answered <- ok }})
	}()
	back.await(t, "B's INVITE to the conference URI")
	if focus, isFocus := ue.FocusOf(receive(t, answered, "the 200 of the focus")); !isFocus || focus != conf.URI {
		t.Errorf("the 200 to B has Contact %q, isfocus %v; want the conference URI %q with isfocus", focus, isFocus, conf.URI)
	}
	// The AS got the INVITE twice: for B's originating services, with
	// P-Served-User, and as the focus, without.
	var originating, atFocus bool
	arrivals, _ := n.wire.arrivals()
	for _, a := range arrivals {
		if a.at != "MMTel AS" || a.m.Method != sip.INVITE || a.m.RequestURI != conf.URI || sip.ReceivedDialogID(a.m).LocalTag != "" {
			continue
		}
		served, err := sip.ParsePServedUser(a.m.Get("P-Served-User"))
		originating = originating || err == nil && served.SessionCase == sip.SessionCaseOrig
		atFocus = atFocus || a.m.Get("P-Served-User") == ""
	}
	if !originating || !atFocus {
		t.Errorf("the AS got B's INVITE for B's originating services: %v; as the focus: %v; want both", originating, atFocus)
	}
	// The focus names B by the P-Asserted-Identity of B's INVITE now, and
	// by that of B's 200 before: either of B's public identities.
	after := awaitUsers(t, conf, 2)
	if after[0].Entity != before[0].Entity || (after[1].Entity != b.impu && after[1].Entity != b.tel) {
		t.Errorf("the conference lists %v after B came back, want A, as before (%v), and B", after, before)
	}
	if cs := n.mrfp.Contexts(); len(cs) != 1 || len(cs[0].Terminations) != 2 {
		t.Fatalf("the MRFP holds %v, want one context with A and B", cs)
	}
	awaitPackets(t, back.media, 75)
	hearsTheOthers(t, b, back.media, a)

	// A, the creator, leaves: the focus ends the conference, and with it
	// B's new leg (5.3.2.7).
	confLeg.hangUp()
	if err := receive(t, confEnded, "A's conference session"); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("A's conference session ended with %v", err)
	}
	back.awaitEnd(t, "B's leg, which the focus ends")
}

// phone is a party run as ue serve runs it: a Phone that registers
// and answers by its policy.
func (n *network) phone(k int, hz float64) (*ue.Phone, *party) {
	n.t.Helper()
	p := &party{subscriber: &subscriber{n: n, number: k}, hz: hz}
	codecs, err := ue.CodecsNamed(ue.VoLTECodecNames)
	if err != nil {
		n.t.Fatal(err)
	}
	id := "00101000000000" + string(rune('0'+k))
	p.impu, p.tel = "sip:"+id+"@"+domain, "tel:+1202555010"+string(rune('0'+k))
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	addr := tr.LocalAddr()
	calls := ue.CallConfig{Codecs: codecs}
	cfg := ue.Config{PrivateIdentity: id + "@" + domain, PublicIdentity: p.impu, Password: "test-password-" + string(rune('0'+k)),
		HomeDomain: domain, PCSCF: n.pcscf, PCSCFURI: "sip:pcscf." + domain + ":5060", Calls: calls}
	contact := ue.NewContact(cfg, addr)
	line := ue.NewLine(true)
	p.ua, err = sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy: addr.String(), Contact: contact.URI(), OnCall: line.Ring, OnOptions: line.Options,
		Locate: func(sip.URI) (sip.Destination, error) { return sip.Destination{Transport: sip.UDP, Addr: n.pcscf}, nil },
	}, calls), quiet())
	if err != nil {
		n.t.Fatal(err)
	}
	n.t.Cleanup(p.ua.Close)
	p.ue = ue.New(cfg, p.ua.Stack(), contact, quiet())
	phone := ue.NewPhone(p.ue, p.ua, line, ue.PhoneConfig{MediaListen: "127.0.0.1:0", Speech: p.speech(),
		Policy: ue.AnswerPolicy{Mode: ue.AnswerAuto}}, quiet())
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		phone.Serve(ctx)
	}()
	n.t.Cleanup(func() {
		stop()
		<-served
	})
	awaitStatus(n.t, phone, "registered", func(s ue.Status) bool { return s.Registered })
	return phone, p
}

func awaitStatus(t *testing.T, p *ue.Phone, what string, want func(ue.Status) bool) ue.Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := p.Status()
		if want(s) {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("phone %s is not %s: %+v", s.IMPU, what, s)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func connected(c *ue.ConferenceStatus) int {
	n := 0
	if c != nil {
		for _, u := range c.Users {
			if u.Status == "connected" {
				n++
			}
		}
	}
	return n
}

// TestThreeWayConferenceOfPhones is the same story with the Phone of
// ue serve, as a controller drives it, and call waiting on the way: A
// is in a call with B when C calls; the call waits (TS 24.615
// 4.5.5.3.2), A takes it, which holds B (4.5.5.3.3 case A), and merges
// the two calls.
func TestThreeWayConferenceOfPhones(t *testing.T) {
	n := startNetworkWith(t, netOptions{mrf: true})
	a, pa := n.phone(1, 500)
	b, _ := n.phone(2, 1100)
	c, _ := n.phone(3, 1700)

	if err := a.Dial("tel:+12025550102", 0, nil); err != nil {
		t.Fatal(err)
	}
	awaitStatus(t, a, "in a call with B", func(s ue.Status) bool { return s.Call != nil && s.Call.State == ue.CallActive })
	if err := c.Dial(pa.tel, 0, nil); err != nil {
		t.Fatal(err)
	}
	s := awaitStatus(t, a, "rung by a waiting call", func(s ue.Status) bool { return len(s.Calls) == 2 && s.Calls[1].Waiting })
	if s.Calls[1].State != ue.CallRinging || s.Call.Peer == s.Calls[1].Peer {
		t.Errorf("A's calls are %+v, want B in the foreground and C waiting", s.Calls)
	}
	if err := a.PickUp(); err != nil {
		t.Fatal(err)
	}
	awaitStatus(t, a, "with B held and C answered", func(s ue.Status) bool {
		return len(s.Calls) == 2 && s.Calls[0].Session != nil && s.Calls[0].Session.Held && s.Calls[1].State == ue.CallActive
	})
	if err := a.Merge(); err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, p := range []*ue.Phone{a, b, c} {
		awaitStatus(t, p, "in the conference with three", func(s ue.Status) bool { return len(s.Calls) == 1 && connected(s.Conference) == 3 })
	}
	cs := n.mrfp.Contexts()
	if len(cs) != 1 || len(cs[0].Terminations) != 3 {
		t.Fatalf("the MRFP holds %v, want one context with the three parties", cs)
	}
	// The focus passes the SDP answer of each party that it invited to
	// the MRFC in the ACK, and the MRFC gives the MRFP the party's
	// address with a Modify. A party that uses preconditions answers in
	// its reliable 183 and sends its 200 without SDP (RFC 3262 5).
	modified := 0
	for _, r := range n.mp.all() {
		if strings.HasPrefix(r, "PUT /contexts/"+cs[0].ID+"/terminations/") && strings.Contains(r, `"remote"`) {
			modified++
		}
	}
	if modified < 2 {
		t.Errorf("the MRFC set the remote address of %d terminations, want the 2 invited parties; Mp requests: %q", modified, n.mp.all())
	}
	// A, the creator, hangs up, and the conference ends (TS 24.147
	// 5.3.2.7).
	if err := a.HangUp(603); err != nil {
		t.Fatal(err)
	}
	for _, p := range []*ue.Phone{a, b, c} {
		awaitStatus(t, p, "free", func(s ue.Status) bool { return len(s.Calls) == 0 })
	}
}
