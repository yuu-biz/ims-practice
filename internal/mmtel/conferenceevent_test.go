package mmtel_test

import (
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file tries the conference event package at the focus with
// subscribers on bare sockets, which answer each NOTIFY only when the
// test says so, or never.

// bareSubscriber is a subscriber to a conference on a bare socket.
type bareSubscriber struct {
	t    *testing.T
	conn *net.UDPConn
	// focus is where the NOTIFYs come from, and their responses go.
	focus netip.AddrPort
	// cseq is the CSeq number of the last NOTIFY that awaitNotify read.
	cseq uint32
}

// subscribeBare sends the SUBSCRIBE of user to the conference URI uri
// from a bare socket, as the S-CSCF passes it on, with the identity
// that the network asserts.
func subscribeBare(t *testing.T, as testAS, user, uri string) *bareSubscriber {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	self := conn.LocalAddr().String()
	subscribe := fmt.Sprintf("SUBSCRIBE %[1]s SIP/2.0\r\n"+
		"Via: SIP/2.0/UDP %[2]s;branch=z9hG4bK-%[3]s-1\r\n"+
		"Max-Forwards: 70\r\n"+
		"From: <sip:%[3]s@%[4]s>;tag=%[3]s\r\n"+
		"To: <%[1]s>\r\n"+
		"Call-ID: %[3]s-1@127.0.0.1\r\n"+
		"CSeq: 1 SUBSCRIBE\r\n"+
		"Contact: <sip:%[3]s@%[2]s>\r\n"+
		"Event: %[5]s\r\n"+
		"Accept: %[6]s\r\n"+
		"Expires: 3600\r\n"+
		"P-Asserted-Identity: <sip:%[3]s@%[4]s>\r\n"+
		"Content-Length: 0\r\n\r\n",
		uri, self, user, domain, confinfo.EventPackage, confinfo.ContentType)
	if _, err := conn.WriteToUDPAddrPort([]byte(subscribe), as.addr); err != nil {
		t.Fatal(err)
	}
	return &bareSubscriber{t: t, conn: conn, focus: as.addr}
}

// nextNotify reads up to the next NOTIFY, one with a CSeq number above
// that of the last one: it passes over the responses to the SUBSCRIBE
// and the retransmissions of a NOTIFY that it read before. It returns
// nil when none comes within d.
func (b *bareSubscriber) nextNotify(d time.Duration) *sip.Message {
	b.conn.SetReadDeadline(time.Now().Add(d))
	buf := make([]byte, 65535)
	for {
		n, err := b.conn.Read(buf)
		if err != nil {
			return nil
		}
		m, err := sip.Parse(buf[:n])
		if err != nil || m.Method != sip.NOTIFY {
			continue
		}
		cseq, err := sip.ParseCSeq(m.Get("CSeq"))
		if err != nil || cseq.Number <= b.cseq {
			continue
		}
		b.cseq = cseq.Number
		return m
	}
}

// awaitNotify is nextNotify, which fails the test when no NOTIFY comes.
func (b *bareSubscriber) awaitNotify(what string) *sip.Message {
	b.t.Helper()
	n := b.nextNotify(10 * time.Second)
	if n == nil {
		b.t.Fatalf("no %s", what)
	}
	return n
}

// answer sends the response with code to notify.
func (b *bareSubscriber) answer(notify *sip.Message, code int) {
	b.t.Helper()
	if _, err := b.conn.WriteToUDPAddrPort(sip.NewResponse(notify, code, "").Bytes(), b.focus); err != nil {
		b.t.Fatal(err)
	}
}

// TestASubscriptionExpiresWhileAnotherIsRemoved: a subscription expires
// while a NOTIFY to another is on its way, and that NOTIFY gets a 481,
// which removes the other subscription (RFC 6665 4.2.2). The one that
// expired still gets its last NOTIFY, with reason timeout (4.2.1.4).
//
// The focus keeps the subscriptions in a list, and the removal moves up
// those that come after the one removed. Under -race, this tries that
// the expiry reads its subscription only while it holds the lock of the
// conference. The test gives the expiry a moment to get past the lock
// before the 481 goes; on a machine too slow for that, the test passes
// without trying the race, and never fails for it.
func TestASubscriptionExpiresWhileAnotherIsRemoved(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	timer := newHandTimer()
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua), AfterFunc: timer.afterFunc})
	conf, focus, _ := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	// expiry is the expiry of the subscription that came last, which the
	// test fires by hand.
	expiry := func(what string) func() {
		t.Helper()
		receive(t, timer.set, what)
		return receive(t, timer.fire, what)
	}
	expiry("expiry timer of alice's subscription")
	xavier := subscribeBare(t, as, "xavier", focus.URI)
	expiry("expiry timer of xavier's subscription")
	xavier.answer(xavier.awaitNotify("first NOTIFY to xavier"), 200)
	yvonne := subscribeBare(t, as, "yvonne", focus.URI)
	expire := expiry("expiry timer of yvonne's subscription")
	yvonne.answer(yvonne.awaitNotify("first NOTIFY to yvonne"), 200)
	zoe := subscribeBare(t, as, "zoe", focus.URI)
	expiry("expiry timer of zoe's subscription")
	zoe.answer(zoe.awaitNotify("first NOTIFY to zoe"), 200)

	// Alice holds the conference, which each subscriber is told of.
	if resp, err := conf.ReInvite(audio("42000", "sendonly")); err != nil || resp.StatusCode != 200 {
		t.Fatalf("hold got %v, %v", resp, err)
	}
	held := xavier.awaitNotify("NOTIFY of the hold to xavier")
	go expire()
	time.Sleep(100 * time.Millisecond)
	xavier.answer(held, 481)

	yvonne.answer(yvonne.awaitNotify("NOTIFY of the hold to yvonne"), 200)
	zoe.answer(zoe.awaitNotify("NOTIFY of the hold to zoe"), 200)
	last := yvonne.awaitNotify("NOTIFY at the expiry of yvonne's subscription")
	yvonne.answer(last, 200)
	if state, err := sip.ParseSubscriptionState(last.Get("Subscription-State")); err != nil ||
		state.State != sip.SubscriptionTerminated || state.Reason != sip.ReasonTimeout {
		t.Errorf("NOTIFY at the expiry has Subscription-State %q, want terminated with reason timeout", last.Get("Subscription-State"))
	}
	if body := string(last.Body); !strings.Contains(body, "sip:alice@"+domain) {
		t.Errorf("NOTIFY at the expiry has\n%s\nwant the state of the conference", body)
	}
}

// TestASubscriberThatDoesNotAnswerLosesItsSubscription: a NOTIFY that
// has no response when Timer F fires ends its subscription (RFC 6665
// 4.2.2), and the focus sends that subscriber nothing after it. Timer F
// is short here, so that the test need not wait 32 seconds for it.
func TestASubscriberThatDoesNotAnswerLosesItsSubscription(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	timers := sip.DefaultTimers()
	timers.TransactionTimeout = 500 * time.Millisecond
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua), Timers: timers})
	_, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	receive(t, notifications, "first NOTIFY to alice")
	xavier := subscribeBare(t, as, "xavier", focus.URI)
	xavier.awaitNotify("first NOTIFY to xavier")

	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Bye() })
	if got := statuses(readAll(t, in)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	awaitNotification(t, notifications, "to alice with carol", func(_ sip.Notification, d confinfo.Document) bool {
		return statusOf(d)["sip:carol@"+domain] == confinfo.Connected
	})
	if n := xavier.nextNotify(3 * timers.TransactionTimeout); n != nil {
		t.Errorf("xavier got a NOTIFY with CSeq %q and Subscription-State %q after one that timed out",
			n.Get("CSeq"), n.Get("Subscription-State"))
	}
}

// TestASubscriberThatDoesNotAnswerHoldsUpNoOneElse: while a NOTIFY to
// one subscriber waits for its response, with Timer F at its default of
// 32 seconds, a user who dials in gets the 200 at once, and the other
// subscribers are told at once, both one that subscribed before and one
// that subscribed after. The NOTIFYs of one subscription go one after
// the other: the subscriber that has not answered gets the next only
// once it answers the one before.
func TestASubscriberThatDoesNotAnswerHoldsUpNoOneElse(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	_, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	receive(t, notifications, "first NOTIFY to alice")
	xavier := subscribeBare(t, as, "xavier", focus.URI)
	first := xavier.awaitNotify("first NOTIFY to xavier")

	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	in, err := carol.Invite(joining("carol", focus.URI))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Bye() })
	if got := statuses(readAll(t, in)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	awaitNotification(t, notifications, "to alice with carol", func(_ sip.Notification, d confinfo.Document) bool {
		return statusOf(d)["sip:carol@"+domain] == confinfo.Connected
	})
	carolNotifications := make(chan sip.Notification, 8)
	if _, err := carol.Subscribe(sip.SubscribeRequest{RequestURI: focus.URI, From: "sip:carol@" + domain, Event: confinfo.EventPackage,
		Accept: confinfo.ContentType, Expires: time.Hour,
		Headers:  []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:carol@" + domain + ">"}},
		OnNotify: func(n sip.Notification, _ *sip.Message) { carolNotifications <- n }}); err != nil {
		t.Fatal(err)
	}
	receive(t, carolNotifications, "first NOTIFY to carol")

	if n := xavier.nextNotify(100 * time.Millisecond); n != nil {
		t.Errorf("xavier got the NOTIFY with CSeq %q before he answered the one before", n.Get("CSeq"))
	}
	xavier.answer(first, 200)
	next := xavier.awaitNotify("NOTIFY to xavier once he answered the first")
	firstCSeq, _ := sip.ParseCSeq(first.Get("CSeq"))
	if cseq, _ := sip.ParseCSeq(next.Get("CSeq")); cseq.Number != firstCSeq.Number+1 {
		t.Errorf("xavier's next NOTIFY has CSeq %q after %q, want the one after it", next.Get("CSeq"), first.Get("CSeq"))
	}
	if got := statusOf(conferenceDocument(t, next.Body)); got["sip:carol@"+domain] != confinfo.Connected {
		t.Errorf("xavier's next NOTIFY has users %v, want carol connected", got)
	}
}

// TestTheLastNOTIFYWithinADialogComesBeforeTheBYE: when the conference
// ends, a participant that subscribed within the dialog of its
// conference session gets the NOTIFY that ends the subscription, with
// reason noresource (TS 24.147 5.3.3.4), before the BYE that removes it
// from the conference (5.3.2.7) and ends the dialog. So it does when
// the NOTIFY before that one still waits for the participant's response
// as the conference ends. Of the participants, the focus sends that
// NOTIFY only to one that it cannot tell has left: this one asked for
// privacy and came in without an asserted identity (TS 24.229 5.7.1.4
// a).
func TestTheLastNOTIFYWithinADialogComesBeforeTheBYE(t *testing.T) {
	mrfc := startConferenceMRFC(t)
	as := startAS(t, noServices{}, mmtel.Config{MRFC: "sip:" + contactOf(t, mrfc.ua)})
	conf, focus, notifications := aliceConference(t, as, func(c *sip.IncomingCall) { c.Reject(480) })
	receive(t, notifications, "first NOTIFY to alice")

	// within takes the requests of the focus within carol's dialog, which
	// the test answers when it wants.
	within := make(chan *sip.InDialogRequest, 4)
	carol := newParticipant(t, as, "carol", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := joining("carol", focus.URI)
	inv.Headers = slices.DeleteFunc(inv.Headers, func(h sip.HeaderField) bool { return h.Name == "P-Asserted-Identity" })
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Privacy", Value: sip.PrivacyID})
	inv.OnRequest = func(r *sip.InDialogRequest) { within <- r }
	in, err := carol.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Bye() })
	if got := statuses(readAll(t, in)); got[len(got)-1] != 200 {
		t.Fatalf("carol got %v, want 200", got)
	}
	if resp, err := in.Send(sip.SUBSCRIBE, sip.Body{}, sip.HeaderField{Name: "Event", Value: confinfo.EventPackage},
		sip.HeaderField{Name: "Expires", Value: "600"}); err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within carol's dialog got %v, %v", resp, err)
	}
	first := receive(t, within, "first NOTIFY within carol's dialog")

	// Alice leaves, which ends the conference. Her subscription ends in
	// the same round of NOTIFYs as carol's, so once alice has her last
	// NOTIFY, carol's waits behind the first, which carol has not
	// answered.
	if err := conf.Bye(); err != nil {
		t.Fatal(err)
	}
	awaitNotification(t, notifications, "that ends alice's subscription", func(n sip.Notification, _ confinfo.Document) bool {
		return n.State.State == sip.SubscriptionTerminated
	})
	// A BYE that came first has answered the NOTIFY with 487 already
	// (RFC 3261 15.1.2), and ended the dialog, which the select finds.
	first.Accept(sip.Body{})
	var last *sip.InDialogRequest
	select {
	case last = <-within:
	case <-in.Done():
		t.Fatal("the focus ended carol's dialog before the NOTIFY that ends her subscription")
	case <-time.After(10 * time.Second):
		t.Fatal("no NOTIFY that ends carol's subscription")
	}
	if state, err := sip.ParseSubscriptionState(last.Request.Get("Subscription-State")); last.Request.Method != sip.NOTIFY ||
		err != nil || state.State != sip.SubscriptionTerminated || state.Reason != sip.ReasonNoResource {
		t.Errorf("carol got %s with Subscription-State %q, want a NOTIFY that says terminated with reason noresource",
			last.Request.Method, last.Request.Get("Subscription-State"))
	}
	select {
	case <-in.Done():
		t.Error("the focus ended carol's dialog before she answered the NOTIFY that ends her subscription")
	default:
	}
	last.Accept(sip.Body{})
	receive(t, in.Done(), "BYE to carol")
	if bye := in.EndedBy(); bye == nil || bye.Method != sip.BYE {
		t.Errorf("carol's dialog ended with %v, want the BYE of the focus", bye)
	}
}
