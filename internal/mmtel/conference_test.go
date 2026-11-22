package mmtel_test

import (
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the conference focus of the AS with a stand-in for
// the MRFC: a UA that answers every INVITE and re-INVITE, and offers in
// its 200 when the INVITE has no offer, as RFC 4240 5 has the MRFC do.

func audio(port, direction string) sip.Body {
	return sip.Body{Type: "application/sdp", Content: []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\nc=IN IP4 127.0.0.1\r\nt=0 0\r\nm=audio " +
		port + " RTP/AVP 0\r\na=" + direction + "\r\n")}
}

// conferenceMRFC is the stand-in, and every INVITE it took.
type conferenceMRFC struct {
	ua    *sip.UA
	calls chan *sip.IncomingCall
	// While hold is set, the stand-in answers no INVITE by itself: each
	// comes on held instead of calls, and the test answers it with
	// answerAsMRFC when it wants the 200 of the MRFC to come.
	hold *atomic.Bool
	held chan *sip.IncomingCall
}

func startConferenceMRFC(t *testing.T) conferenceMRFC {
	t.Helper()
	m := conferenceMRFC{calls: make(chan *sip.IncomingCall, 8), hold: new(atomic.Bool), held: make(chan *sip.IncomingCall, 8)}
	m.ua = newUA(t, "mrfc", func(c *sip.IncomingCall) {
		if m.hold.Load() {
			m.held <- c
			return
		}
		m.calls <- c
		answerAsMRFC(c)
	})
	return m
}

// answerAsMRFC answers an INVITE at the stand-in as the MRFC does: with
// its answer, or with an offer when the INVITE has none (RFC 4240 5).
func answerAsMRFC(c *sip.IncomingCall) {
	c.Handle(func(r *sip.InDialogRequest) { r.Accept(audio("40002", "recvonly")) })
	if len(c.Request.Body) > 0 {
		c.Accept(audio("40000", "sendrecv"))
		return
	}
	c.Accept(audio("40004", "sendrecv"))
}

// conferenceDocument is the conference-info of a NOTIFY.
func conferenceDocument(t *testing.T, body []byte) confinfo.Document {
	t.Helper()
	d, err := confinfo.Parse(body)
	if err != nil {
		t.Fatalf("NOTIFY body: %v\n%s", err, body)
	}
	return d
}

// awaitNotification reads NOTIFYs until one has a document for which
// want holds, and returns it. Every NOTIFY has the full state, so one
// that comes twice, as when a change crosses the SUBSCRIBE, is no
// error.
func awaitNotification(t *testing.T, notifications <-chan sip.Notification, what string, want func(sip.Notification, confinfo.Document) bool) sip.Notification {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case n := <-notifications:
			if want(n, conferenceDocument(t, n.Body.Content)) {
				return n
			}
		case <-timeout:
			t.Fatalf("no NOTIFY %s", what)
		}
	}
}

// statusOf is the status of each user of a conference document.
func statusOf(d confinfo.Document) map[string]confinfo.EndpointStatus {
	got := map[string]confinfo.EndpointStatus{}
	for _, u := range d.Users.Users {
		for _, e := range u.Endpoints {
			got[u.Entity] = e.Status
		}
	}
	return got
}

// TestConferenceFocus is TS 24.147 with the three-way session of GSMA
// IR.92 2.3.3, at the focus: the creator's INVITE to the factory URI,
// the REFER that brings a user in with the INVITE of 5.3.2.5.4, the
// conference event package outside and within a dialog, hold, a user
// who leaves, and the end of the conference when the creator leaves.
func TestConferenceFocus(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	withinDialog := make(chan *sip.InDialogRequest, 4)
	bob := newParticipant(t, as, "bob", func(c *sip.IncomingCall) {
		c.Handle(func(r *sip.InDialogRequest) {
			withinDialog <- r
			r.Accept(sip.Body{})
		})
		invited <- c
		c.Accept(audio("41000", "sendrecv"))
	})
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })

	// The creator's INVITE, as the S-CSCF passes it on (5.3.1.3.2); bob
	// stands where the S-CSCF would be for the focus's INVITE to him.
	inv := viaAS(t, as, bob, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	conf, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, conf)
	ok := resps[len(resps)-1]
	focus, err := sip.ParseNameAddr(ok.Get("Contact"))
	if _, isFocus := focus.Params.Get("isfocus"); err != nil || ok.StatusCode != 200 || !isFocus || !strings.HasPrefix(focus.URI, "sip:conf-") {
		t.Fatalf("creator got %d with Contact %q, want 200 with the conference URI and isfocus", ok.StatusCode, ok.Get("Contact"))
	}
	// TS 23.003 13.5: the conference URI is a public service identity in
	// the subdomain of the conferences, with no port, not the host of the
	// AS instance.
	if u, err := sip.ParseURI(focus.URI); err != nil || u.Host != conferenceDomain || u.Port != 0 {
		t.Errorf("conference URI %q, want one in %s without a port", focus.URI, conferenceDomain)
	}
	creatorMedia := receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	if !strings.HasPrefix(creatorMedia.Request.RequestURI, "sip:conf=conf-") || len(creatorMedia.Request.Body) == 0 {
		t.Errorf("MRFC got Request-URI %q, want conf=<id> with the creator's offer", creatorMedia.Request.RequestURI)
	}

	// The creator subscribes outside the dialog (IR.92 2.3.3).
	notifications := make(chan sip.Notification, 8)
	sub, err := alice.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:alice@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }})
	if err != nil {
		t.Fatal(err)
	}
	first := conferenceDocument(t, receive(t, notifications, "first NOTIFY").Body.Content)
	if first.Entity != focus.URI || len(first.Users.Users) != 1 || first.Description.MaximumUserCount == nil {
		t.Errorf("first NOTIFY has entity %q and %d users, want the conference URI, the creator and maximum-user-count", first.Entity, len(first.Users.Users))
	}

	// 5.3.1.5.3: the creator refers bob to the focus, within the dialog.
	// The focus accepts with 200, not 202 (RFC 7647 5).
	replaces := sip.Replaces{CallID: "call-1@example", ToTag: "b", FromTag: "a"}
	r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain, Replaces: &replaces},
		sip.HeaderField{Name: "Referred-By", Value: "<sip:alice@" + domain + ">"})
	if err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	c := receive(t, invited, "the focus's INVITE at bob")
	req := c.Request
	if req.Get("P-Asserted-Identity") != "<"+focus.URI+">" || req.Get("Contact") != focus.String() ||
		req.Get("Replaces") != replaces.String() || req.Get("Referred-By") == "" {
		t.Errorf("bob's INVITE has P-Asserted-Identity %q, Contact %q, Replaces %q, Referred-By %q",
			req.Get("P-Asserted-Identity"), req.Get("Contact"), req.Get("Replaces"), req.Get("Referred-By"))
	}
	if !strings.Contains(string(req.Body), "m=audio 40004") {
		t.Errorf("bob's INVITE offers\n%s\nwant the MRFC's offer", req.Body)
	}
	var statuses []int
	for s := range r.Statuses() {
		statuses = append(statuses, s.StatusCode)
	}
	if len(statuses) != 2 || statuses[1] != 200 {
		t.Errorf("REFER got NOTIFYs %v, want 100 and 200", statuses)
	}
	awaitNotification(t, notifications, "with bob connected", func(_ sip.Notification, d confinfo.Document) bool {
		got := statusOf(d)
		return len(got) == 2 && got["sip:bob@"+domain] == confinfo.Connected
	})

	// IR.92 2.3.3: the network also takes a SUBSCRIBE within the dialog.
	resp, err := c.Send(sip.SUBSCRIBE, sip.Body{}, sip.HeaderField{Name: "Event", Value: confinfo.EventPackage},
		sip.HeaderField{Name: "Expires", Value: "600"})
	if err != nil || resp.StatusCode != 200 || resp.Get("Expires") != "600" {
		t.Fatalf("SUBSCRIBE within the dialog got %v, %v", resp, err)
	}
	if n := receive(t, withinDialog, "NOTIFY within bob's dialog"); n.Request.Method != sip.NOTIFY ||
		len(conferenceDocument(t, n.Request.Body).Users.Users) != 2 {
		t.Errorf("bob got %s within the dialog, want a NOTIFY with two users", n.Request.Method)
	}

	// Hold within the conference (TS 24.610) reaches the MRFC, and the
	// creator is on-hold in the package.
	if resp, err := conf.ReInvite(audio("42000", "sendonly")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("hold got %v, %v", resp, err)
	}
	awaitNotification(t, notifications, "with alice on-hold", func(_ sip.Notification, d confinfo.Document) bool {
		return statusOf(d)["sip:alice@"+domain] == confinfo.OnHold
	})
	receive(t, withinDialog, "NOTIFY of the hold within bob's dialog")

	// Bob leaves: the focus releases his media (5.3.2.6.1).
	bobMedia := receive(t, mrfc.calls, "bob's INVITE at the MRFC")
	if err := c.Bye(); err != nil {
		t.Fatal(err)
	}
	receive(t, bobMedia.Done(), "BYE of bob's media at the MRFC")
	awaitNotification(t, notifications, "with bob disconnected", func(_ sip.Notification, d confinfo.Document) bool {
		return statusOf(d)["sip:bob@"+domain] == confinfo.Disconnected
	})

	// The creator leaves: the conference ends (5.3.2.7), and with it the
	// subscription (5.3.3.4, RFC 4575 3.3).
	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	last := awaitNotification(t, notifications, "that ends the subscription", func(n sip.Notification, _ confinfo.Document) bool {
		return n.State.State == sip.SubscriptionTerminated
	})
	if last.State.Reason != sip.ReasonNoResource || len(statusOf(conferenceDocument(t, last.Body.Content))) == 0 {
		t.Errorf("last NOTIFY has Subscription-State %v, want reason noresource", last.State)
	}
	receive(t, sub.Done(), "end of the subscription")
	receive(t, creatorMedia.Done(), "BYE of the creator's media at the MRFC")
}

// TestConferenceSubscriptionExpires: a subscription that is not
// refreshed ends at its expiry with a NOTIFY that says terminated with
// reason timeout (RFC 6665 4.2.1.4), and the subscriber is told nothing
// after it.
func TestConferenceSubscriptionExpires(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	timer := newHandTimer()
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua), AfterFunc: timer.afterFunc})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	conf, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conf.Bye() })
	resps := readAll(t, conf)
	focus, err := sip.ParseNameAddr(resps[len(resps)-1].Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	notifications := make(chan sip.Notification, 8)
	sub, err := alice.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:alice@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: 10 * time.Minute,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }})
	if err != nil {
		t.Fatal(err)
	}
	if first := receive(t, notifications, "first NOTIFY"); first.State.State != sip.SubscriptionActive {
		t.Fatalf("first NOTIFY says %+v, want active", first.State)
	}
	if d := receive(t, timer.set, "the expiry timer of the subscription"); d <= 9*time.Minute || d > 10*time.Minute {
		t.Errorf("expiry timer set for %v, want the 10 minutes granted", d)
	}
	receive(t, timer.fire, "the expiry")()
	last := receive(t, notifications, "NOTIFY at the expiry")
	if last.State.State != sip.SubscriptionTerminated || last.State.Reason != sip.ReasonTimeout {
		t.Errorf("NOTIFY at the expiry says %+v, want terminated with reason timeout", last.State)
	}
	receive(t, sub.Done(), "end of the subscription")
	if err := sub.Err(); err != nil {
		t.Errorf("subscription ended with %v, want the NOTIFY that ends it", err)
	}
}

// TestCreatorIsInTheConferenceOnceAnswered: the 200 gives the creator
// the conference URI, and the creator may subscribe at once, before the
// focus has its ACK. The conference then already has the creator. The
// creator here is a bare socket, which holds its ACK back.
func TestCreatorIsInTheConferenceOnceAnswered(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	bob := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	creator, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { creator.Close() })
	self := creator.LocalAddr().String()

	served := sip.PServedUser{URI: "sip:alice@" + domain, SessionCase: sip.SessionCaseOrig, RegState: sip.RegStateRegistered}
	factory := ims.ConferenceFactoryURI(domain)
	sdp := audio("42000", "sendrecv").Content
	invite := fmt.Sprintf("INVITE %[1]s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %[2]s;branch=z9hG4bK-creator-1\r\n"+
		"Max-Forwards: 70\r\n"+
		"Route: <sip:%[3]s;lr>, <sip:%[4]s;lr;odi=x1>\r\n"+
		"From: <sip:alice@%[5]s>;tag=creator\r\n"+
		"To: <%[1]s>\r\n"+
		"Call-ID: creator-1@127.0.0.1\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:alice@%[2]s>\r\n"+
		"P-Served-User: %[6]s\r\n"+
		"P-Asserted-Identity: <sip:alice@%[5]s>\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %[7]d\r\n\r\n%[8]s",
		factory, self, as.addr, contactOf(t, bob), domain, served.String(), len(sdp), sdp)
	if _, err := creator.WriteToUDPAddrPort([]byte(invite), as.addr); err != nil {
		t.Fatal(err)
	}
	ok := readResponse(t, creator, 200)
	focus, err := sip.ParseNameAddr(ok.Get("Contact"))
	if err != nil {
		t.Fatalf("200 has Contact %q: %v", ok.Get("Contact"), err)
	}

	notifications := make(chan sip.Notification, 8)
	_, err = alice.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:alice@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }})
	if err != nil {
		t.Fatal(err)
	}
	first := conferenceDocument(t, receive(t, notifications, "first NOTIFY").Body.Content)
	if got := statusOf(first); got["sip:alice@"+domain] != confinfo.Connected || len(got) != 1 {
		t.Errorf("first NOTIFY before the creator's ACK has users %v, want the creator connected", got)
	}
}

// readResponse reads the responses on conn up to the one with code.
func readResponse(t *testing.T, conn *net.UDPConn, code int) *sip.Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 65535)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatalf("no %d: %v", code, err)
		}
		if m, err := sip.Parse(buf[:n]); err == nil && m.StatusCode == code {
			return m
		}
	}
}

// joining is the INVITE of user to the conference URI uri as the focus
// gets it (TS 24.147 5.3.1.4.1): from the S-CSCF or the I-CSCF, by DNS,
// with the identity that the network asserts and no P-Served-User.
func joining(user, uri string) sip.Invitation {
	return sip.Invitation{RequestURI: uri, From: "sip:" + user + "@" + domain, To: uri,
		Headers: []sip.HeaderField{
			{Name: "P-Asserted-Identity", Value: "<sip:" + user + "@" + domain + ">"},
			{Name: "P-Charging-Vector", Value: "icid-value=icid-" + user + ";orig-ioi=" + domain},
		},
		Body: audio("43000", "sendrecv")}
}

// TestJoinWithTheConferenceURI is TS 24.147 5.3.2.4.1: a user who has
// the conference URI joins with an INVITE to it, which the focus
// answers as it answers the creator, until the conference is full.
func TestJoinWithTheConferenceURI(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	conf, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conf.Bye() })
	resps := readAll(t, conf)
	focus, err := sip.ParseNameAddr(resps[len(resps)-1].Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	notifications := make(chan sip.Notification, 8)
	if _, err := alice.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:alice@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }}); err != nil {
		t.Fatal(err)
	}

	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	resps = readAll(t, in)
	ok := resps[len(resps)-1]
	if ok.StatusCode != 200 || ok.Get("Contact") != focus.String() || ok.Get("P-Asserted-Identity") != "<"+focus.URI+">" {
		t.Fatalf("carol got %d with Contact %q and P-Asserted-Identity %q, want 200 with the conference URI, with isfocus in Contact",
			ok.StatusCode, ok.Get("Contact"), ok.Get("P-Asserted-Identity"))
	}
	// 5.3.2.2.2: the first response has the icid-value and orig-ioi of
	// the INVITE, and the network of the focus as term-ioi.
	if pcv, _ := sip.ChargingVectorOf(ok); pcv.ICID != "icid-carol" || pcv.OrigIOI != domain || pcv.TermIOI != domain {
		t.Errorf("carol's 200 has P-Charging-Vector %q", ok.Get("P-Charging-Vector"))
	}
	media := receive(t, mrfc.calls, "carol's INVITE at the MRFC")
	id, _ := sip.ParseURI(focus.URI)
	if !strings.HasPrefix(media.Request.RequestURI, "sip:conf="+id.User+"@") || !strings.Contains(string(media.Request.Body), "m=audio 43000") {
		t.Errorf("MRFC got Request-URI %q with\n%s\nwant the conference with carol's offer", media.Request.RequestURI, media.Request.Body)
	}
	awaitNotification(t, notifications, "with carol dialed in", func(_ sip.Notification, d confinfo.Document) bool {
		for _, u := range d.Users.Users {
			if u.Entity == "sip:carol@"+domain && len(u.Endpoints) == 1 {
				e := u.Endpoints[0]
				return e.Status == confinfo.Connected && e.JoiningMethod == confinfo.DialedIn
			}
		}
		return false
	})

	// Alice and carol are 2 of the 6 users that a conference takes.
	for k := 3; k <= 6; k++ {
		user := "user" + string(rune('0'+k))
		ua := newParticipant(t, as, user, func(c *sip.IncomingCall) { c.Reject(480) })
		out, err := ua.Invite(joining(user, focus.URI))
		if err != nil {
			t.Fatal(err)
		}
		if got := statuses(readAll(t, out)); got[len(got)-1] != 200 {
			t.Fatalf("%s got %v, want 200", user, got)
		}
	}
	ua := newParticipant(t, as, "user7", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := ua.Invite(joining("user7", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); len(got) != 1 || got[0] != 403 {
		t.Errorf("the seventh user got %v, want 403", got)
	}
}

// aliceConference has alice create a conference at as, with the INVITE
// to the factory URI that the S-CSCF passes on, and subscribe to it.
// Where the S-CSCF would be for the INVITEs of the focus, bob answers
// them with invited. It returns her conference session, the conference
// URI with isfocus from the 200, and the NOTIFYs of her subscription.
func aliceConference(t *testing.T, as testAS, invited func(*sip.IncomingCall)) (*sip.OutgoingCall, sip.NameAddr, <-chan sip.Notification) {
	t.Helper()
	callee := newUA(t, "bob", invited)
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	conf, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conf.Bye() })
	resps := readAll(t, conf)
	focus, err := sip.ParseNameAddr(resps[len(resps)-1].Get("Contact"))
	if err != nil {
		t.Fatalf("the creator got %v with Contact %q: %v", statuses(resps), resps[len(resps)-1].Get("Contact"), err)
	}
	notifications := make(chan sip.Notification, 16)
	if _, err := alice.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:alice@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:alice@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }}); err != nil {
		t.Fatal(err)
	}
	return conf, focus, notifications
}

// joinWithoutACK is the INVITE of user to the conference URI uri from a
// bare socket, as joining has it. The socket reads the 200 of the focus
// and holds its ACK back, so that the focus has no confirmed dialog with
// the user.
func joinWithoutACK(t *testing.T, as testAS, user, uri string) *sip.Message {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	self := conn.LocalAddr().String()
	sdp := audio("44000", "sendrecv").Content
	invite := fmt.Sprintf("INVITE %[1]s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %[2]s;branch=z9hG4bK-%[3]s-1\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%[3]s@%[4]s>;tag=%[3]s\r\n"+
		"To: <%[1]s>\r\n"+
		"Call-ID: %[3]s-1@127.0.0.1\r\n"+
		"CSeq: 1 INVITE\r\n"+
		"Contact: <sip:%[3]s@%[2]s>\r\n"+
		"P-Asserted-Identity: <sip:%[3]s@%[4]s>\r\n"+
		"Content-Type: application/sdp\r\n"+
		"Content-Length: %[5]d\r\n\r\n%[6]s",
		uri, self, user, domain, len(sdp), sdp)
	if _, err := conn.WriteToUDPAddrPort([]byte(invite), as.addr); err != nil {
		t.Fatal(err)
	}
	return readResponse(t, conn, 200)
}

// TestJoinersBeforeTheirACKs: the focus takes a user who dials in as a
// participant once it answers, before its ACK (TS 24.147 5.3.2.4.1), so
// that two users may be in the conference with no confirmed dialog at
// the same time. Each stays the participant it is: when carol leaves,
// dave, who holds his ACK back, is still connected.
func TestJoinersBeforeTheirACKs(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })

	joinWithoutACK(t, as, "dave", focus.URI)
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, in)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	awaitNotification(t, notifications, "with carol and dave connected", func(_ sip.Notification, d confinfo.Document) bool {
		got := statusOf(d)
		return got["sip:carol@"+domain] == confinfo.Connected && got["sip:dave@"+domain] == confinfo.Connected
	})

	if err := in.Bye(); err != nil {
		t.Fatal(err)
	}
	left := awaitNotification(t, notifications, "with a participant disconnected", func(_ sip.Notification, d confinfo.Document) bool {
		return slices.Contains(slices.Collect(maps.Values(statusOf(d))), confinfo.Disconnected)
	})
	want := map[string]confinfo.EndpointStatus{
		"sip:alice@" + domain: confinfo.Connected, "sip:carol@" + domain: confinfo.Disconnected, "sip:dave@" + domain: confinfo.Connected,
	}
	if got := statusOf(conferenceDocument(t, left.Body.Content)); !maps.Equal(got, want) {
		t.Errorf("after carol left, the NOTIFY has %v, want %v", got, want)
	}
}

// awaitNoConference waits until the store of as has no record of the
// conference id.
func awaitNoConference(t *testing.T, as testAS, id string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := as.store.Conference(id); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the store still has a record of the conference %s", id)
		}
	}
}

// TestJoinAsTheConferenceEnds: the creator leaves while the MRFC sets up
// the media of a user who joins. When the MRFC answers, the conference
// has ended and given its URI up (TS 24.147 5.3.2.7): the user gets the
// 404 of a conference URI that is not allocated (5.3.2.4.1 step 1), the
// focus releases the user's media at the MRFC, and no record of the
// conference is left.
func TestJoinAsTheConferenceEnds(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	uri, err := sip.ParseURI(focus.URI)
	if err != nil {
		t.Fatal(err)
	}

	mrfc.hold.Store(true)
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	media := receive(t, mrfc.held, "carol's INVITE at the MRFC")
	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	awaitNoConference(t, as, uri.User)

	go answerAsMRFC(media)
	if got := statuses(readAll(t, in)); slices.Contains(got, 200) || got[len(got)-1] != 404 {
		t.Errorf("carol got %v, want 404", got)
	}
	if r, ok := as.store.Conference(uri.User); ok {
		t.Errorf("the store has the conference again after it ended, with %d participants", len(r.Participants))
	}
	receive(t, media.Done(), "BYE of carol's media at the MRFC")
}

// TestInvitedAsTheConferenceEnds: the creator leaves while the user
// whom it referred to the focus is still to answer. The focus does not
// take in the user who then answers: it ends the user's session with a
// BYE, and no record of the conference is left.
func TestInvitedAsTheConferenceEnds(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { invited <- c })
	uri, err := sip.ParseURI(focus.URI)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain}); err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	bob := receive(t, invited, "the focus's INVITE at bob")
	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	awaitNoConference(t, as, uri.User)

	if _, err := bob.Accept(audio("41000", "sendrecv")); err != nil {
		t.Fatalf("bob's 200 got no ACK: %v", err)
	}
	receive(t, bob.Done(), "BYE of the focus at bob")
	if r, ok := as.store.Conference(uri.User); ok {
		t.Errorf("the store has the conference again after it ended, with %d participants", len(r.Participants))
	}
}

// TestConferenceRefusals: without an MRFC the AS creates no conference,
// a REFER that asks the focus to remove someone is not taken, and an
// INVITE to a conference URI joins nothing when no conference has that
// URI, or when the network asserts no identity of the user.
func TestConferenceRefusals(t *testing.T) {
	alice := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	without := startAS(t, noServices{}, mmtel.Config{})
	inv := viaAS(t, without, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI = ims.ConferenceFactoryURI(domain)
	out, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); len(got) != 1 || got[0] != 503 {
		t.Errorf("factory without an MRFC got %v, want 503", got)
	}

	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	alice = newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv = viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI = ims.ConferenceFactoryURI(domain)
	conf, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, conf)
	r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain + ";method=BYE"})
	if err != nil || r.Response.StatusCode != 403 {
		t.Errorf("REFER with method BYE got %v, %v; want 403", r, err)
	}

	focus, err := sip.ParseNameAddr(resps[len(resps)-1].Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	anonymous := joining("carol", focus.URI)
	anonymous.Headers = anonymous.Headers[1:]
	for _, c := range []struct {
		what string
		inv  sip.Invitation
		want int
	}{
		// TS 24.147 5.3.2.4.1 step 1.
		{"a conference URI that is not allocated", joining("carol", "sip:conf-none@"+conferenceDomain), 404},
		// Step 2.
		{"no P-Asserted-Identity", anonymous, 403},
	} {
		out, err := carol.Invite(c.inv)
		if err != nil {
			t.Fatal(err)
		}
		if got := statuses(readAll(t, out)); len(got) != 1 || got[0] != c.want {
			t.Errorf("INVITE with %s got %v, want %d", c.what, got, c.want)
		}
	}
}
