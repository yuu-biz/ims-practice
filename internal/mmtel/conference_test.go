package mmtel_test

import (
	"context"
	"errors"
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
	// answer when it wants the 200 of the MRFC to come.
	hold *atomic.Bool
	held chan *sip.IncomingCall
	// While holdInDialog is set, the stand-in answers no request within
	// a dialog it has by itself: each comes on inDialog, and the test
	// answers it when it wants the response of the MRFC to come.
	holdInDialog *atomic.Bool
	inDialog     chan *sip.InDialogRequest
}

func startConferenceMRFC(t *testing.T) conferenceMRFC {
	t.Helper()
	m := conferenceMRFC{calls: make(chan *sip.IncomingCall, 8), hold: new(atomic.Bool), held: make(chan *sip.IncomingCall, 8),
		holdInDialog: new(atomic.Bool), inDialog: make(chan *sip.InDialogRequest, 8)}
	// answers is the copy of the stand-in that takes its calls. It holds
	// no UA: the field below is set once the UA is up, which is after the
	// UA may have taken a call.
	answers := m
	m.ua = newUA(t, "mrfc", func(c *sip.IncomingCall) {
		if answers.hold.Load() {
			answers.held <- c
			return
		}
		answers.calls <- c
		answers.answer(c)
	})
	return m
}

// answer answers an INVITE at the stand-in as the MRFC does: with its
// answer, or with an offer when the INVITE has none (RFC 4240 5).
func (m conferenceMRFC) answer(c *sip.IncomingCall) {
	c.Handle(func(r *sip.InDialogRequest) {
		if m.holdInDialog.Load() {
			m.inDialog <- r
			return
		}
		r.Accept(audio("40002", "recvonly"))
	})
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
	var carolKey string
	awaitNotification(t, notifications, "with carol dialed in", func(_ sip.Notification, d confinfo.Document) bool {
		for _, u := range d.Users.Users {
			if u.Entity == "sip:carol@"+domain && len(u.Endpoints) == 1 {
				e := u.Endpoints[0]
				carolKey = e.Entity
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
	// RFC 4575 5.7: the focus names each endpoint by a key of its own,
	// which names no address of the participant and stays the same in
	// every notification.
	awaitNotification(t, notifications, "with 6 users", func(_ sip.Notification, d confinfo.Document) bool {
		if len(d.Users.Users) != 6 {
			return false
		}
		keys := map[string]bool{}
		for _, u := range d.Users.Users {
			for _, e := range u.Endpoints {
				if !strings.HasPrefix(e.Entity, "urn:uuid:") || keys[e.Entity] {
					t.Errorf("endpoint of %s is %q, want a urn:uuid key of its own", u.Entity, e.Entity)
				}
				keys[e.Entity] = true
				if u.Entity == "sip:carol@"+domain && e.Entity != carolKey {
					t.Errorf("carol's endpoint is %q, was %q", e.Entity, carolKey)
				}
			}
		}
		return true
	})
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
func aliceConference(t *testing.T, as testAS, invited func(*sip.IncomingCall), headers ...sip.HeaderField) (*sip.OutgoingCall, sip.NameAddr, <-chan sip.Notification) {
	t.Helper()
	callee := newUA(t, "bob", invited)
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	inv.Headers = append(inv.Headers, headers...)
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

// usersOf is each user of a conference document by its entity.
func usersOf(d confinfo.Document) map[string]confinfo.User {
	got := map[string]confinfo.User{}
	for _, u := range d.Users.Users {
		got[u.Entity] = u
	}
	return got
}

// TestPrivacyKeepsTheIdentityOutOfTheConferenceState is RFC 4575 8.2 on
// each of the three ways into a conference: the focus takes a
// participant's request for privacy and reveals its identity in no URI
// of the conference state. Such a participant is a user of the document
// under an anonymous URI of RFC 4575 5.6, with a status and a joining
// method of its own; a participant that asks for nothing is named by
// its identity. "header" asks for privacy as "id" and "user" do: it is
// the other value with which the AS of a user with OIR restricts that
// user's identity (TS 24.607 4.5.2.4 item 1 b).
func TestPrivacyKeepsTheIdentityOutOfTheConferenceState(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	// The creator asks for privacy in its INVITE to the factory URI, as a
	// user with OIR does (TS 24.605 4.6.5, TS 24.607 4.5.2.4). The user
	// that the focus invites asks for it in its 2xx, as one with TIR does
	// (TS 24.605 4.6.3, TS 24.608 4.5.2.9).
	conf, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) {
		c.Accept(audio("41000", "sendrecv"),
			sip.HeaderField{Name: "P-Asserted-Identity", Value: "<sip:bob@" + domain + ">"},
			sip.HeaderField{Name: "Privacy", Value: sip.PrivacyID})
	}, sip.HeaderField{Name: "Privacy", Value: sip.PrivacyID})
	receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	if r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain}); err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	// The focus names each participant whose identity it withholds in
	// turn, so the invited user is in the conference before the next
	// user dials in.
	awaitNotification(t, notifications, "with the invited user connected", func(_ sip.Notification, d confinfo.Document) bool {
		return len(d.Users.Users) == 2
	})

	// carol withholds its identity with "user", dave asks for nothing,
	// and eve comes in with the "header" of a user whose AS restricts
	// all of its private information.
	for _, user := range []struct {
		name    string
		privacy string
	}{{"carol", sip.PrivacyUser}, {"dave", ""}, {"eve", sip.PrivacyHeader}} {
		ua := newParticipant(t, as, user.name, func(c *sip.IncomingCall) { c.Reject(480) })
		inv := joining(user.name, focus.URI)
		if user.privacy != "" {
			inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Privacy", Value: user.privacy})
		}
		out, err := ua.Invite(inv)
		if err != nil {
			t.Fatal(err)
		}
		if got := statuses(readAll(t, out)); got[len(got)-1] != 200 {
			t.Fatalf("%s got %v, want 200", user.name, got)
		}
	}

	creator, _ := confinfo.AnonymousName(1)
	invited, _ := confinfo.AnonymousName(2)
	dialedIn, _ := confinfo.AnonymousName(3)
	headerPrivacy, _ := confinfo.AnonymousName(4)
	n := awaitNotification(t, notifications, "with all five participants", func(_ sip.Notification, d confinfo.Document) bool {
		return len(d.Users.Users) == 5
	})
	got := usersOf(conferenceDocument(t, n.Body.Content))
	for entity, want := range map[string]confinfo.User{
		// The focus withheld these four identities, and names each
		// participant by an anonymous URI of its own.
		creator:       {Entity: creator, DisplayText: "Anonymous1"},
		invited:       {Entity: invited, DisplayText: "Anonymous2"},
		dialedIn:      {Entity: dialedIn, DisplayText: "Anonymous3"},
		headerPrivacy: {Entity: headerPrivacy, DisplayText: "Anonymous4"},
		// GSMA IR.92 2.3.3: without a display text of its own, a user's
		// entity stands in for it.
		"sip:dave@" + domain: {Entity: "sip:dave@" + domain, DisplayText: "sip:dave@" + domain},
	} {
		u, ok := got[entity]
		if !ok {
			t.Errorf("no user %s in the conference state, which has %v", entity, slices.Sorted(maps.Keys(got)))
			continue
		}
		if u.DisplayText != want.DisplayText || len(u.Endpoints) != 1 || u.Endpoints[0].Status != confinfo.Connected {
			t.Errorf("user %s is %+v, want display text %q and one connected endpoint", entity, u, want.DisplayText)
		}
	}
	// The identity behind each anonymous URI is nowhere in the document,
	// and no participant is named twice.
	for _, hidden := range []string{"alice", "bob", "carol", "eve"} {
		if strings.Contains(string(n.Body.Content), hidden+"@"+domain) {
			t.Errorf("the conference state tells the identity of %s:\n%s", hidden, n.Body.Content)
		}
	}
	// RFC 4575 5.7.4: the creator and carol dialed in, and the focus
	// dialed bob out, which the state says of them while saying no more.
	for entity, want := range map[string]confinfo.JoiningMethod{
		creator: confinfo.DialedIn, dialedIn: confinfo.DialedIn, invited: confinfo.DialedOut,
	} {
		if method := got[entity].Endpoints[0].JoiningMethod; method != want {
			t.Errorf("user %s joined by %q, want %q", entity, method, want)
		}
	}
}

// TestTheCreatorsIdentityRestrictionReachesTheConferenceState is TS
// 24.605 4.6.5 for the user that creates the conference: the network
// decides whether its identity is in the conference state, and its UE
// does not have to ask for anything. The INVITE to the factory URI
// passes the originating services of the creator before it becomes a
// conference, and OIR puts "id" or "header" in its Privacy there, by
// the subscription option of the user (TS 24.607 4.5.2.4 item 1 a and
// b). A creator that presents its identity for this conference with
// "none" keeps it: OIR in the temporary mode lets such a request
// through as it is.
func TestTheCreatorsIdentityRestrictionReachesTheConferenceState(t *testing.T) {
	alice := "sip:alice@" + domain
	anonymous, anonymousText := confinfo.AnonymousName(1)
	for _, tc := range []struct {
		name, doc, privacy   string
		wantEntity, wantText string
	}{
		// A user whose OIR is in the permanent mode never presents its
		// identity, and its UE asks for nothing.
		{"permanent, the UE asks for nothing", oir("permanent", "presentation-not-restricted"), "", anonymous, anonymousText},
		// The AS of a user whose subscription option restricts all the
		// private information asks for that with "header", which restricts
		// the identity as "id" does.
		{"permanent, all private information", oirOption("permanent", "presentation-not-restricted", "all-private-information"), "", anonymous, anonymousText},
		// In the temporary mode the identity is restricted by default, and
		// the UE of this creator asks for nothing either.
		{"temporary, restricted by default", oir("temporary", "presentation-restricted"), "", anonymous, anonymousText},
		// In the temporary mode the user presents its identity for this
		// conference with "none".
		{"temporary, restricted by default, presented this time", oir("temporary", "presentation-restricted"), "none", alice, alice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mrfc := startConferenceMRFC(t)
			hss := &documents{docs: map[string]string{alice: mmtelServices(tc.doc)}}
			as := startAS(t, hss, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
			var headers []sip.HeaderField
			if tc.privacy != "" {
				headers = append(headers, sip.HeaderField{Name: "Privacy", Value: tc.privacy})
			}
			_, _, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) }, headers...)
			receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
			n := awaitNotification(t, notifications, "with the creator in the conference", func(_ sip.Notification, d confinfo.Document) bool {
				return len(d.Users.Users) == 1
			})
			creator := conferenceDocument(t, n.Body.Content).Users.Users[0]
			if creator.Entity != tc.wantEntity || creator.DisplayText != tc.wantText {
				t.Errorf("the creator is %q with the display text %q; want %q and %q",
					creator.Entity, creator.DisplayText, tc.wantEntity, tc.wantText)
			}
			if tc.wantEntity == anonymous && strings.Contains(string(n.Body.Content), "alice@"+domain) {
				t.Errorf("the conference state tells the creator's identity:\n%s", n.Body.Content)
			}
		})
	}
}

// TestOutgoingBarringStopsTheCreator is TS 24.611 4.5.2.4.1 at the
// conference factory URI: the INVITE to it is an outgoing call of the
// creator like any other, so the barring rules of the creator decide on
// it before it becomes a conference. A creator whose outgoing calls are
// all barred gets 603 (Decline), and no conference is made: the focus
// asks the MRFC for no mix.
func TestOutgoingBarringStopsTheCreator(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(barring("", rule("baoc", "", false)))}}
	as := startAS(t, hss, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Reject(480) })
	alice := newParticipant(t, as, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, "orig", audio("42000", "sendrecv"))
	inv.RequestURI, inv.To = ims.ConferenceFactoryURI(domain), ims.ConferenceFactoryURI(domain)
	out, err := alice.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !slices.Equal(got, []int{603}) {
		t.Fatalf("the creator got %v, want 603 alone", got)
	}
	if len(mrfc.calls) > 0 {
		t.Error("the AS set up media at the MRFC although the barring rules stop the call")
	}
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
// conference id. The store is what the other instances of the AS read,
// and no message on the wire tells when the focus has removed the
// record.
func awaitNoConference(t *testing.T, as testAS, id string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, ok := conferenceOf(t, as, id); !ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the store still has a record of the conference %s", id)
		}
	}
}

// conferenceOf is the record of the conference id in the store of as.
func conferenceOf(t *testing.T, as testAS, id string) (mmtel.ConferenceRecord, bool) {
	t.Helper()
	r, ok, err := as.store.Conference(context.Background(), id)
	if err != nil {
		t.Fatalf("Conference(%s): %v", id, err)
	}
	return r, ok
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

	go mrfc.answer(media)
	if got := statuses(readAll(t, in)); slices.Contains(got, 200) || got[len(got)-1] != 404 {
		t.Errorf("carol got %v, want 404", got)
	}
	if r, ok := conferenceOf(t, as, uri.User); ok {
		t.Errorf("the store has the conference again after it ended, with %d participants", len(r.Participants))
	}
	receive(t, media.Done(), "BYE of carol's media at the MRFC")
}

// TestMediaChangeAsTheConferenceEnds: a participant changes its media
// while the conference ends, and the MRFC answers the change only
// afterwards. The conference has given its URI up by then (TS 24.147
// 5.3.2.7) and its record is out of the store, and the change that
// finishes after that puts no record back: the other instances read
// the store, and a record there names a conference that no instance
// hosts and no request would reach.
func TestMediaChangeAsTheConferenceEnds(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	uri, err := sip.ParseURI(focus.URI)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, in)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	media := receive(t, mrfc.calls, "carol's INVITE at the MRFC")

	// Carol holds the conference (TS 24.610 4.5.2.1), which the focus
	// passes on to its dialog with the MRFC (TS 24.229 5.8.4.1.1.1). The
	// stand-in holds that re-INVITE, so the change is still on at the
	// MRFC while the conference ends.
	mrfc.holdInDialog.Store(true)
	answered := make(chan *sip.Message, 1)
	go func() {
		resp, _ := in.ReInvite(audio("43000", "sendonly"))
		answered <- resp
	}()
	change := receive(t, mrfc.inDialog, "carol's change of media at the MRFC")

	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	awaitNoConference(t, as, uri.User)
	receive(t, media.Done(), "BYE of carol's media at the MRFC")

	change.Accept(audio("40002", "recvonly"))
	receive(t, answered, "the focus's answer to carol's change of media")
	if r, ok := conferenceOf(t, as, uri.User); ok {
		t.Errorf("the store has the conference again after it ended, with %d participants", len(r.Participants))
	}
}

// TestInvitedAsTheConferenceEnds: the creator leaves while the user
// whom it referred to the focus is still to answer. The focus does not
// take in the user who then answers: it ends the user's session with a
// BYE, releases the user's media at the MRFC, and leaves no record of
// the conference. The creator, which asked for the invitation, is gone
// with the dialog that carried the subscription of its REFER, so the
// focus reports the refusal to no one.
func TestInvitedAsTheConferenceEnds(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { invited <- c })
	uri, err := sip.ParseURI(focus.URI)
	if err != nil {
		t.Fatal(err)
	}
	receive(t, mrfc.calls, "the creator's INVITE at the MRFC")
	if r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain}); err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	bob := receive(t, invited, "the focus's INVITE at bob")
	bobMedia := receive(t, mrfc.calls, "bob's INVITE at the MRFC")
	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	awaitNoConference(t, as, uri.User)

	acceptAndBeRemoved(t, bob, "bob")
	receive(t, bobMedia.Done(), "BYE of bob's media at the MRFC")
	if r, ok := conferenceOf(t, as, uri.User); ok {
		t.Errorf("the store has the conference again after it ended, with %d participants", len(r.Participants))
	}
}

// acceptAndBeRemoved answers the focus's INVITE at an invited user whom
// the focus does not take into the conference, and waits for the user's
// session to end.
//
// The focus acknowledges the 200 (RFC 3261 13.2.2.4) and sends the BYE
// right after, and the two reach the user in either order. A user that
// takes the ACK first has a confirmed dialog that the BYE then ends. A
// user that takes the BYE first ends the session there and then (RFC
// 3261 15.1.2), as it may: the clause has the UAS end the session
// whatever else is pending, and 15 says a UA can hang up before the ACK
// has come. Its 2xx gets no ACK, and Accept reports that the call has
// ended. Either way the user is out of the session and the focus did
// not take it in, so the test takes both.
func acceptAndBeRemoved(t *testing.T, c *sip.IncomingCall, user string) {
	t.Helper()
	if _, err := c.Accept(audio("41000", "sendrecv")); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		t.Fatalf("%s's 200 got neither an ACK nor a BYE: %v", user, err)
	}
	receive(t, c.Done(), "BYE of the focus at "+user)
	if by := c.EndedBy(); by == nil || by.Method != sip.BYE {
		t.Errorf("%s's session ended by %v, want the BYE of the focus", user, by)
	}
}

// TestReferToANumber: the focus invites the user of a Refer-To that
// holds a number by the tel URI of that number, which the S-CSCF finds
// the user by (TS 24.147 5.3.1.5.3, RFC 3261 19.1.6). A GRUU keeps its
// form: it names one UA instance of the user (RFC 5627 3.1), so the
// number in it is not what the REFER asks for.
func TestReferToANumber(t *testing.T) {
	const number = "sip:+12025550102@" + domain + ";user=phone"
	const gruu = number + ";gr=urn:uuid:f81d4fae-7dec-11d0-a765-00a0c91e6bf6"
	for name, tc := range map[string]struct{ referTo, requestURI string }{
		"a number becomes a tel URI": {number, "tel:+12025550102"},
		"a GRUU stays a SIP URI":     {gruu, gruu},
	} {
		t.Run(name, func(t *testing.T) {
			mrfc := startConferenceMRFC(t)
			as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
			invited := make(chan *sip.IncomingCall, 1)
			conf, _, _ := aliceConference(t, as, func(c *sip.IncomingCall) {
				invited <- c
				c.Accept(audio("41000", "sendrecv"))
			})
			if r, err := conf.Refer(sip.ReferTo{URI: tc.referTo}); err != nil || r.Response.StatusCode != 200 {
				t.Fatalf("REFER got %v, %v; want 200", r, err)
			}
			got := receive(t, invited, "the focus's INVITE at the invited user").Request
			if got.RequestURI != tc.requestURI {
				t.Errorf("Request-URI = %q, want %q", got.RequestURI, tc.requestURI)
			}
		})
	}
}

// TestReferredByOfAParticipantThatAsksForPrivacy is TS 24.605 4.6.5: a
// REFER whose sender asks for privacy with "user" brings the invited
// user an INVITE without the Referred-By that names the sender. With
// "id", which keeps the identity within the trust domain, the clause
// leaves the Referred-By there.
func TestReferredByOfAParticipantThatAsksForPrivacy(t *testing.T) {
	const referredBy = "<sip:alice@" + domain + ">"
	for name, tc := range map[string]struct{ privacy, want string }{
		"user privacy takes the Referred-By out": {sip.PrivacyUser, ""},
		"id privacy leaves it":                   {sip.PrivacyID, referredBy},
		"header privacy leaves it":               {sip.PrivacyHeader, referredBy},
		"no privacy leaves it":                   {"", referredBy},
	} {
		t.Run(name, func(t *testing.T) {
			mrfc := startConferenceMRFC(t)
			as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
			invited := make(chan *sip.IncomingCall, 1)
			conf, _, _ := aliceConference(t, as, func(c *sip.IncomingCall) {
				invited <- c
				c.Accept(audio("41000", "sendrecv"))
			})
			headers := []sip.HeaderField{{Name: "Referred-By", Value: referredBy}}
			if tc.privacy != "" {
				headers = append(headers, sip.HeaderField{Name: "Privacy", Value: tc.privacy})
			}
			if r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain}, headers...); err != nil || r.Response.StatusCode != 200 {
				t.Fatalf("REFER got %v, %v; want 200", r, err)
			}
			if got := receive(t, invited, "the focus's INVITE at the invited user").Request.Get("Referred-By"); got != tc.want {
				t.Errorf("the INVITE has Referred-By %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAnonymousParticipantsWithoutAnIdentity: a user that asks for
// privacy needs no asserted identity to dial in (TS 24.229 5.7.1.4 a).
// When one such participant leaves, the subscription of another one,
// which the focus knows by no identity either, goes on: the focus ends
// the subscription of a participant that left (TS 24.147 5.3.3.3), and
// cannot tell that either of them is the subscriber.
func TestAnonymousParticipantsWithoutAnIdentity(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	anonymously := func(user string) sip.Invitation {
		inv := joining(user, focus.URI)
		inv.Headers = slices.DeleteFunc(inv.Headers, func(h sip.HeaderField) bool { return h.Name == "P-Asserted-Identity" })
		inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Privacy", Value: sip.PrivacyID})
		return inv
	}

	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	if out, err := carol.Invite(anonymously("carol")); err != nil {
		t.Fatal(err)
	} else if got := statuses(readAll(t, out)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	notifications := make(chan sip.Notification, 16)
	if _, err := carol.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:carol@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "Privacy", Value: sip.PrivacyID}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { notifications <- n }}); err != nil {
		t.Fatal(err)
	}

	dave := newParticipant(t, as, "dave", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := dave.Invite(anonymously("dave"))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); got[len(got)-1] != 200 {
		t.Fatalf("dave got %v, want 200", got)
	}
	awaitNotification(t, notifications, "with dave connected", func(_ sip.Notification, d confinfo.Document) bool {
		return len(d.Users.Users) == 3
	})
	if err := out.Bye(); err != nil {
		t.Fatal(err)
	}
	n := awaitNotification(t, notifications, "with dave gone", func(_ sip.Notification, d confinfo.Document) bool {
		for _, u := range d.Users.Users {
			for _, e := range u.Endpoints {
				if e.Status == confinfo.Disconnected {
					return true
				}
			}
		}
		return false
	})
	if n.State.State != sip.SubscriptionActive {
		t.Errorf("carol's subscription is %+v after dave left, want active", n.State)
	}
}

// TestJoinTheLastPlaceAtOnce: two users dial in at the same time to a
// conference that has room for one more. Both pass the check before the
// focus asks the MRFC for their media; the one who joins second gets
// 403, and the focus releases its media at the MRFC.
func TestJoinTheLastPlaceAtOnce(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	for k := 2; k <= 5; k++ {
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

	mrfc.hold.Store(true)
	var outs []*sip.OutgoingCall
	var media []*sip.IncomingCall
	for _, user := range []string{"user6", "user7"} {
		ua := newParticipant(t, as, user, func(c *sip.IncomingCall) { c.Reject(480) })
		out, err := ua.Invite(joining(user, focus.URI))
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, out)
		media = append(media, receive(t, mrfc.held, user+"'s INVITE at the MRFC"))
	}
	for _, m := range media {
		go mrfc.answer(m)
	}
	finals := map[int]int{}
	for i, out := range outs {
		resps := readAll(t, out)
		final := resps[len(resps)-1]
		finals[final.StatusCode]++
		if final.StatusCode == 403 && !strings.Contains(final.Get("Warning"), "full") {
			t.Errorf("the 403 has Warning %q, want one that says the conference is full", final.Get("Warning"))
		}
		if final.StatusCode != 200 {
			receive(t, media[i].Done(), "BYE of the refused user's media at the MRFC")
		}
	}
	if finals[200] != 1 || finals[403] != 1 {
		t.Errorf("the two users got %v of each final status, want one 200 and one 403", finals)
	}
}

// TestInvitedWhenTheConferenceFills: users dial in and fill the
// conference while the user whom the creator referred to the focus is
// still to answer. The focus does not take in the user who then
// answers: it ends the user's session with a BYE, and the last NOTIFY
// of the REFER says 403.
func TestInvitedWhenTheConferenceFills(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	invited := make(chan *sip.IncomingCall, 1)
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { invited <- c })
	r, err := conf.Refer(sip.ReferTo{URI: "sip:bob@" + domain})
	if err != nil || r.Response.StatusCode != 200 {
		t.Fatalf("REFER got %v, %v; want 200", r, err)
	}
	bob := receive(t, invited, "the focus's INVITE at bob")
	for k := 2; k <= 6; k++ {
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

	acceptAndBeRemoved(t, bob, "bob")
	if got := referStatuses(t, r); len(got) == 0 || got[len(got)-1] != 403 {
		t.Errorf("REFER got NOTIFYs %v, want the last with 403", got)
	}
}

// TestJoinCancelledWhileTheMRFCAnswers: a user who dials in cancels
// while the MRFC sets up its media. The user has its 487 (RFC 3261 9.2),
// the focus releases the media once the MRFC answers, and the
// subscribers are never told of the user.
func TestJoinCancelledWhileTheMRFCAnswers(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	conf, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })

	mrfc.hold.Store(true)
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	media := receive(t, mrfc.held, "carol's INVITE at the MRFC")
	if err := in.Cancel(); err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, in)); got[len(got)-1] != 487 {
		t.Fatalf("carol got %v after her CANCEL, want 487", got)
	}
	go mrfc.answer(media)
	receive(t, media.Done(), "BYE of carol's media at the MRFC")

	// Alice holds the conference, which the focus tells her of: every
	// NOTIFY up to that one tells of alice alone.
	if resp, err := conf.ReInvite(audio("42000", "sendonly")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("hold got %v, %v", resp, err)
	}
	awaitNotification(t, notifications, "with alice on-hold", func(_ sip.Notification, d confinfo.Document) bool {
		got := statusOf(d)
		if _, ok := got["sip:carol@"+domain]; ok {
			t.Errorf("a NOTIFY tells of carol, who cancelled: %v", got)
		}
		return got["sip:alice@"+domain] == confinfo.OnHold
	})
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
	// A tel URI with no number names nobody, so the focus has no user to
	// invite and does not accept the REFER (RFC 3515 2.4.2).
	r, err = conf.Refer(sip.ReferTo{URI: "tel:"})
	if err != nil || r.Response.StatusCode != 403 {
		t.Errorf("REFER to a tel URI with no number got %v, %v; want 403", r, err)
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
