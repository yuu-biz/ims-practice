package ue_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/ue"
)

// fakeFocus is a conference focus that refuses SUBSCRIBEs outside the
// dialog with 403, as a network before Release 12 may (TS 24.147
// 5.3.1.2 NOTE), takes one within the dialog, and accepts REFERs.
type fakeFocus struct {
	ua      *sip.UA
	uri     string
	refers  chan *sip.Message
	session chan *sip.IncomingCall
	// ends gets the end of the usage of each subscription that the focus
	// takes within the dialog (sip.CallDialog.AddSubscriptionUsage).
	ends chan func()
}

func startFakeFocus(t *testing.T) *fakeFocus {
	t.Helper()
	f := &fakeFocus{refers: make(chan *sip.Message, 2), session: make(chan *sip.IncomingCall, 1), ends: make(chan func(), 1)}
	tr := listenTransport(t)
	f.uri = "sip:conf-1@" + tr.LocalAddr().String()
	var err error
	f.ua, err = sip.NewUA(tr, sip.UAConfig{
		SentBy: tr.LocalAddr().String(), Contact: "sip:focus@" + tr.LocalAddr().String(), Allow: []sip.Method{sip.REFER},
		OnSubscribe: func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 403, sip.NewTag())) },
		OnCall: func(ic *sip.IncomingCall) {
			ic.Handle(func(r *sip.InDialogRequest) { f.inDialog(t, ic, r) })
			ic.SetContact(sip.NameAddr{URI: f.uri, Params: sip.Params{{Name: "isfocus"}}}, false)
			offer, _ := sdp.Parse(ic.Request.Body)
			answer, err := sdp.Answer(offer, "9", netip.MustParseAddrPort("127.0.0.1:40000"))
			if err != nil {
				t.Error(err)
				ic.Reject(488)
				return
			}
			f.session <- ic
			ic.Accept(sip.Body{Type: "application/sdp", Content: answer.Bytes()})
		},
	}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.ua.Close)
	return f
}

func (f *fakeFocus) inDialog(t *testing.T, ic *sip.IncomingCall, r *sip.InDialogRequest) {
	switch r.Request.Method {
	case sip.REFER:
		f.refers <- r.Request
		if s, err := r.AcceptRefer(time.Minute); err == nil {
			s.Notify(200)
		}
	case sip.SUBSCRIBE:
		end, err := ic.AddSubscriptionUsage()
		if err != nil {
			t.Error(err)
			r.Reject(500)
			return
		}
		f.ends <- end
		r.Accept(sip.Body{}, sip.HeaderField{Name: "Expires", Value: "3600"})
		doc := confinfo.Conference{URI: f.uri, MaximumUserCount: 6, Participants: []confinfo.Participant{
			{User: impu, Endpoint: impu, Status: confinfo.Connected}}}
		body, _ := doc.FullState(1).Marshal()
		if _, err := ic.Send(sip.NOTIFY, sip.Body{Type: confinfo.ContentType, Content: body},
			sip.HeaderField{Name: "Event", Value: confinfo.EventPackage},
			sip.HeaderField{Name: "Subscription-State", Value: "active;expires=3600"}); err != nil {
			t.Error(err)
		}
	default:
		r.Reject(405)
	}
}

// TestConferenceOfTheUE is TS 24.147 5.3.1 at the UE: the call to the
// conference factory URI takes the Contact with isfocus as the
// conference URI (5.3.1.3.2); after a 403 to the SUBSCRIBE outside the
// dialog the UE subscribes within it (5.3.1.2, GSMA IR.92 2.3.3); and
// the REFER for a peer carries the peer's number as a SIP URI with
// user=phone and the Replaces of the UE's call with it (5.3.1.5.3).
func TestConferenceOfTheUE(t *testing.T) {
	f := startFakeFocus(t)
	conf, ended, cancel, _ := conferenceOfTheUE(t, f)
	defer cancel()
	if conf.URI != f.uri {
		t.Errorf("conference URI %q, want the Contact of the focus %q", conf.URI, f.uri)
	}
	if ic := <-f.session; ic.Request.RequestURI != "sip:mmtel@conf-factory."+domain {
		t.Errorf("INVITE to %q, want the default conference factory URI", ic.Request.RequestURI)
	}
	subscribeWithin(t, conf)
	if users := conf.Users(); users[0].Entity != impu || users[0].Status != "connected" {
		t.Errorf("users %v, want the UE connected", users)
	}
	defer (<-f.ends)()

	call := sip.Dialog{ID: sip.DialogID{CallID: "call-1@example", LocalTag: "mine", RemoteTag: "theirs"}}
	status, err := conf.Invite("tel:+12025550102", call)
	if err != nil || status != 200 {
		t.Fatalf("Invite got %d, %v; want 200", status, err)
	}
	rt, err := sip.ParseReferTo((<-f.refers).Get("Refer-To"))
	if err != nil || rt.URI != "sip:+12025550102@"+domain+";user=phone" || rt.Replaces == nil ||
		*rt.Replaces != (sip.Replaces{CallID: "call-1@example", ToTag: "theirs", FromTag: "mine"}) {
		t.Errorf("Refer-To is %+v (%v), want the peer's number with user=phone and the Replaces of the call", rt, err)
	}
	cancel()
	<-ended
}

// TestTheUEKeepsItsDialogForTheLastNOTIFY is TS 24.147 5.3.3.3 at the
// UE, with RFC 5057 5.5 and RFC 6665 4.5.2: a UE that subscribed within
// the dialog of its conference session and hangs up still takes the
// NOTIFY that ends the subscription in that dialog, after the 200 to
// its BYE, and answers it 200. Once it has, the UE no longer knows the
// dialog (RFC 5057 4), and a request within it gets 481 (RFC 3261
// 12.2.2).
func TestTheUEKeepsItsDialogForTheLastNOTIFY(t *testing.T) {
	f := startFakeFocus(t)
	conf, ended, cancel, at := conferenceOfTheUE(t, f)
	defer cancel()
	ic := <-f.session
	subscribeWithin(t, conf)
	endAtTheFocus := <-f.ends
	defer endAtTheFocus()

	cancel()
	<-ended
	select {
	case <-ic.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the focus got no BYE")
	}
	d, _ := ic.Dialog()
	resp, err := ic.Send(sip.NOTIFY, sip.Body{}, sip.HeaderField{Name: "Event", Value: confinfo.EventPackage},
		sip.HeaderField{Name: "Subscription-State", Value: "terminated;reason=noresource"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("the NOTIFY after the BYE got %v, %v; want 200", resp, err)
	}
	endAtTheFocus()

	ct, err := f.ua.Stack().SendRequest(d.NewRequest(sip.OPTIONS), sip.Destination{Transport: sip.UDP, Addr: at})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for resp := range ct.Responses() {
		final = resp
	}
	if final == nil || final.StatusCode != 481 {
		t.Errorf("an OPTIONS within the dialog after the last NOTIFY got %v, want 481", final)
	}
}

// subscribeWithin subscribes conf to the conference, which the fake
// focus takes within the dialog only, and waits for the first NOTIFY.
func subscribeWithin(t *testing.T, conf *ue.Conference) {
	t.Helper()
	if err := conf.Subscribe(); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	deadline := time.After(5 * time.Second)
	for len(conf.Users()) == 0 {
		select {
		case <-conf.Changed():
		case <-deadline:
			t.Fatal("no NOTIFY within the dialog reached the conference")
		}
	}
}

// conferenceOfTheUE is a UE that has created a conference at f. cancel
// hangs up, and ended gets the end of the call. at is the address of
// the UE.
func conferenceOfTheUE(t *testing.T, f *fakeFocus) (conf *ue.Conference, ended <-chan error, cancel func(), at netip.AddrPort) {
	t.Helper()
	tr := listenTransport(t)
	addr := tr.LocalAddr()
	cfg := ue.Config{PrivateIdentity: impi, PublicIdentity: impu, Password: password, HomeDomain: domain,
		PCSCF: f.ua.Stack().LocalAddr(), PCSCFURI: "sip:pcscf." + domain + ":5060"}
	contact := ue.NewContact(cfg, addr)
	ua, err := sip.NewUA(tr, ue.UAConfig(sip.UAConfig{
		SentBy: addr.String(), Contact: contact.URI(), OnCall: func(ic *sip.IncomingCall) { ic.Reject(486) },
		Locate: func(sip.URI) ([]sip.Destination, error) {
			return []sip.Destination{{Transport: sip.UDP, Addr: f.ua.Stack().LocalAddr()}}, nil
		},
	}, ue.CallConfig{}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ua.Close)
	u := ue.New(cfg, ua.Stack(), contact, quiet())
	media, err := ue.ListenMedia("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { media.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	conf, ended, err = u.CreateConference(ctx, ua, directRegistration, media, ue.Speech{}, ue.CallOptions{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return conf, ended, cancel, addr
}

// TestWaitingCallRingsWithCallWaiting is TS 24.615 4.5.5.3.2 d: the 180
// of a call that waits behind another has the Alert-Info of call
// waiting, by which the AS of the callee learns of it (4.5.5.2.3).
func TestWaitingCallRingsWithCallWaiting(t *testing.T) {
	call, _ := ringingCallee(t, context.Background(), ue.AnswerOptions{Waiting: true, PickUp: time.After(100 * time.Millisecond)})
	alerts := map[int]string{}
	for resp := range call.Responses() {
		alerts[resp.StatusCode] = resp.Get("Alert-Info")
	}
	call.Bye()
	if alerts[180] != "<urn:alert:service:call-waiting>" || alerts[200] != "" {
		t.Errorf("Alert-Info of the 180 and the 200: %q, %q; want call-waiting in the 180 only", alerts[180], alerts[200])
	}
}
