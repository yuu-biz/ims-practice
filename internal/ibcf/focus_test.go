package ibcf_test

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/confinfo"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sh"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The conference event package (RFC 4575 3.1) and its document type.
const (
	conferenceEvent    = "conference"
	conferenceDocument = "application/conference-info+xml"
)

// A conference focus in network A invites a user of network B (TS
// 24.147 5.3.2.5.4): its INVITE has the conference URI with isfocus in
// Contact. Both IBCFs pass that Contact on as it came and record-route
// themselves (TS 24.229 5.10.5), so the user sees the conference URI
// and takes the call as a conference session (TS 24.147 5.3.1.4.3).
// Within that dialog the user subscribes to the conference event
// package: its SUBSCRIBE goes to the conference URI through the IBCF
// of each network, and the focus's NOTIFY comes back the same way.
//
// A SUBSCRIBE to the conference URI outside the dialog, the first one
// that a participant tries (TS 24.147 5.3.1.2), does not cross: the
// IBCF of network B answers it 403, after which a participant
// subscribes within the dialog, as above.
func TestConferenceFocusAcrossTheBorder(t *testing.T) {
	// The agreements let the requests of the event package cross (TS
	// 29.165 Table 6.1).
	p := newPair(t, func(ofB, ofA *interconnect.Profile) {
		for _, profile := range []*interconnect.Profile{ofB, ofA} {
			profile.Methods = append(profile.Methods, sip.SUBSCRIBE, sip.NOTIFY)
		}
	})
	// The focus is network A's UE, whose conference URI names a host of
	// network A's domain.
	conferenceHost := "conference." + domainA
	p.a.names.set(conferenceHost, p.a.ue.Stack().LocalAddr())
	conferenceURI := "sip:conf-7f3a@" + conferenceHost

	subscribed := make(chan *sip.Message, 1)
	focus, err := p.a.ue.Invite(sip.Invitation{
		RequestURI: p.b.sipURI(),
		From:       conferenceURI,
		Route:      []string{p.a.ueRoute},
		Body:       p.a.offer(t, sdp.VoLTECodecs(), 1, ""),
		Contact:    sip.NameAddr{URI: conferenceURI, Params: sip.Params{{Name: "isfocus"}}},
		OnRequest: func(r *sip.InDialogRequest) {
			if r.Request.Method == sip.SUBSCRIBE {
				subscribed <- r.Request
				r.Accept(sip.Body{}, sip.HeaderField{Name: "Expires", Value: "3600"})
				return
			}
			r.Accept(sip.Body{})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	in := p.b.nextCall(t)
	na, err := sip.ParseNameAddr(in.Request.Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := na.Params.Get("isfocus"); na.URI != conferenceURI || !ok {
		t.Fatalf("the participant got Contact %q, want the conference URI %s with isfocus", in.Request.Get("Contact"), conferenceURI)
	}
	inB, _ := planB.instanceHosts(0)
	if rr := strings.Join(in.Request.GetAll("Record-Route"), ", "); !strings.Contains(rr, inB) {
		t.Errorf("the participant's INVITE has Record-Route %q, without network B's IBCF instance %s", rr, inB)
	}
	notified := make(chan *sip.Message, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if r.Request.Method == sip.NOTIFY {
			notified <- r.Request
		}
		r.Accept(sip.Body{})
	})
	go in.Accept(p.b.answer(t, in.Request))
	if resp := final(t, focus); resp.StatusCode != 200 {
		t.Fatalf("the focus's INVITE got %d", resp.StatusCode)
	}

	// Between the networks the INVITE has the conference URI too, and
	// network A's IBCF in Record-Route; the participant's side of the
	// dialog still has the IBCF in Contact.
	crossed := p.b.rec.requests("ibcf1-external", sip.INVITE)
	if len(crossed) != 1 {
		t.Fatalf("network B's IBCF received %d INVITEs, want 1", len(crossed))
	}
	_, exA := planA.instanceHosts(0)
	if got := crossed[0].Get("Contact"); !strings.Contains(got, conferenceURI) || !strings.Contains(got, "isfocus") {
		t.Errorf("Contact at the II-NNI: %q", got)
	}
	if rr := crossed[0].GetAll("Record-Route"); len(rr) != 1 || !strings.Contains(rr[0], exA) {
		t.Errorf("Record-Route at the II-NNI: %q, want network A's IBCF instance %s alone", rr, exA)
	}
	dialog, ok := focus.Dialog()
	if !ok {
		t.Fatal("the focus has no dialog")
	}
	inA, _ := planA.instanceHosts(0)
	if !strings.Contains(dialog.RemoteTarget, inA) {
		t.Errorf("the focus's remote target is %s, want network A's IBCF instance %s", dialog.RemoteTarget, inA)
	}

	// The participant subscribes within the dialog of the conference
	// session.
	resp, err := in.Send(sip.SUBSCRIBE, sip.Body{},
		sip.HeaderField{Name: "Event", Value: conferenceEvent},
		sip.HeaderField{Name: "Accept", Value: conferenceDocument},
		sip.HeaderField{Name: "Expires", Value: "3600"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog: %v %v", resp, err)
	}
	select {
	case sub := <-subscribed:
		if sub.RequestURI != conferenceURI {
			t.Errorf("the focus got the SUBSCRIBE at %s, want the conference URI %s", sub.RequestURI, conferenceURI)
		}
		if sip.EventType(sub.Get("Event")) != conferenceEvent {
			t.Errorf("the focus got Event %q", sub.Get("Event"))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the SUBSCRIBE did not reach the focus")
	}
	if got := p.a.rec.requests("ibcf1-external", sip.SUBSCRIBE); len(got) != 1 || got[0].RequestURI != conferenceURI {
		t.Errorf("network A's IBCF received the SUBSCRIBEs %v, want one to the conference URI", got)
	}

	document := []byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<conference-info xmlns="urn:ietf:params:xml:ns:conference-info" entity="` + conferenceURI + `" state="full" version="1"/>`)
	resp, err = focus.Send(sip.NOTIFY, sip.Body{Type: conferenceDocument, Content: document},
		sip.HeaderField{Name: "Event", Value: conferenceEvent},
		sip.HeaderField{Name: "Subscription-State", Value: "active;expires=3600"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("NOTIFY within the dialog: %v %v", resp, err)
	}
	select {
	case n := <-notified:
		if string(n.Body) != string(document) || n.Get("Content-Type") != conferenceDocument {
			t.Errorf("the participant got a NOTIFY with %q: %s", n.Get("Content-Type"), n.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the NOTIFY did not reach the participant")
	}

	// Outside the dialog, the SUBSCRIBE goes from network B's S-CSCF to
	// its IBCF, which refuses it.
	out := &sip.Message{Method: sip.SUBSCRIBE, RequestURI: conferenceURI}
	out.Add("Route", p.b.ueRoute)
	out.Add("Max-Forwards", "70")
	out.Add("From", "<"+p.b.sipURI()+">;tag="+sip.NewTag())
	out.Add("To", "<"+conferenceURI+">")
	out.Add("Call-ID", sip.NewCallID())
	out.Add("CSeq", "1 SUBSCRIBE")
	out.Add("Contact", "<"+p.b.contact+">")
	out.Add("Event", conferenceEvent)
	out.Add("Accept", conferenceDocument)
	out.Add("Expires", "3600")
	if resp := p.b.send(t, out); resp.StatusCode != 403 {
		t.Errorf("the SUBSCRIBE outside the dialog got %d, want 403 from network B's IBCF", resp.StatusCode)
	}
	if got := p.b.counter(t, `ims_ibcf_requests_rejected_total{peer="peer-a",direction="exit",method="SUBSCRIBE",status="403"}`); got != 1 {
		t.Errorf("network B counted %v SUBSCRIBEs it refused, want 1", got)
	}
	// Without SUBSCRIBE in the agreement, the border answers 405 first
	// (TS 29.165 Table 6.1).
	q := newPair(t, nil)
	out.Set("Route", q.b.ueRoute)
	out.Set("Call-ID", sip.NewCallID())
	if resp := q.b.send(t, out); resp.StatusCode != 405 {
		t.Errorf("the SUBSCRIBE outside an agreement got %d, want 405", resp.StatusCode)
	}
	if got := p.a.rec.requests("ibcf1-external", sip.SUBSCRIBE); len(got) != 1 {
		t.Errorf("network A's IBCF received %d SUBSCRIBEs, want only the one within the dialog", len(got))
	}

	if err := in.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the focus's call after the participant's BYE", focus.Done())
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
	assertHidden(t, p.a, planB)
	dumpCaptures(t, p.a)
	dumpCaptures(t, p.b)
}

// A user of network A calls a conference focus of network B, as a UE
// does to create a conference at the conference factory URI (TS 24.147
// 5.3.1.3.2): the focus answers with the conference URI and isfocus in
// Contact. Both IBCFs pass that Contact on in their responses and put
// themselves in Record-Route there, so the caller's requests to the
// conference URI still go through them.
func TestConferenceFocusAnswersAcrossTheBorder(t *testing.T) {
	p := newPair(t, func(ofB, ofA *interconnect.Profile) {
		for _, profile := range []*interconnect.Profile{ofB, ofA} {
			profile.Methods = append(profile.Methods, sip.SUBSCRIBE)
		}
	})
	conferenceHost := "conference." + domainB
	p.b.names.set(conferenceHost, p.b.ue.Stack().LocalAddr())
	conferenceURI := "sip:conf-91c2@" + conferenceHost

	call := p.a.invite(t, p.b.sipURI(), p.a.offer(t, sdp.VoLTECodecs(), 1, ""), nil)
	in := p.b.nextCall(t)
	subscribed := make(chan *sip.Message, 1)
	in.Handle(func(r *sip.InDialogRequest) {
		if r.Request.Method == sip.SUBSCRIBE {
			subscribed <- r.Request
		}
		r.Accept(sip.Body{})
	})
	in.SetContact(sip.NameAddr{URI: conferenceURI, Params: sip.Params{{Name: "isfocus"}}}, false)
	go in.Accept(p.b.answer(t, in.Request))
	ok := final(t, call)
	if ok.StatusCode != 200 {
		t.Fatalf("the call got %d", ok.StatusCode)
	}
	na, err := sip.ParseNameAddr(ok.Get("Contact"))
	if err != nil {
		t.Fatal(err)
	}
	if _, focus := na.Params.Get("isfocus"); na.URI != conferenceURI || !focus {
		t.Fatalf("the caller got Contact %q, want the conference URI %s with isfocus", ok.Get("Contact"), conferenceURI)
	}
	inA, _ := planA.instanceHosts(0)
	if rr := strings.Join(ok.GetAll("Record-Route"), ", "); !strings.Contains(rr, inA) {
		t.Errorf("the caller's 200 has Record-Route %q, without network A's IBCF instance %s", rr, inA)
	}

	resp, err := call.Send(sip.SUBSCRIBE, sip.Body{},
		sip.HeaderField{Name: "Event", Value: conferenceEvent},
		sip.HeaderField{Name: "Accept", Value: conferenceDocument},
		sip.HeaderField{Name: "Expires", Value: "3600"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog: %v %v", resp, err)
	}
	select {
	case sub := <-subscribed:
		if sub.RequestURI != conferenceURI {
			t.Errorf("the focus got the SUBSCRIBE at %s, want the conference URI %s", sub.RequestURI, conferenceURI)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the SUBSCRIBE did not reach the focus")
	}
	if got := p.a.rec.requests("ibcf1-internal", sip.SUBSCRIBE); len(got) != 1 || got[0].RequestURI != conferenceURI {
		t.Errorf("network A's IBCF received the SUBSCRIBEs %v, want one to the conference URI", got)
	}

	if err := call.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the focus's call after the caller's BYE", in.Done())
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
	assertHidden(t, p.a, planB)
}

// A user of network B joins a conference of network A with an INVITE to
// its conference URI (TS 24.147 5.3.1.4.1, 5.3.2.4.1), and subscribes
// to the conference event package within that dialog. The focus is the
// conference AS of this project. The IBCFs pass the conference state on
// as the focus wrote it, so it is the focus that keeps the inside of
// network A out of it: no endpoint is named by the address of the
// creator's UE, nor by the IBCF that the focus has as the joining user's
// Contact.
func TestConferenceStateAcrossTheBorder(t *testing.T) {
	// The focus takes the identity that a trusted network asserts.
	p := newPair(t, func(ofB, ofA *interconnect.Profile) {
		for _, profile := range []*interconnect.Profile{ofB, ofA} {
			profile.Trusted = true
			profile.Methods = append(profile.Methods, sip.SUBSCRIBE, sip.NOTIFY)
		}
	})
	focus := startFocus(t, p.a)

	// The UE of network A creates the conference: its INVITE to the
	// factory URI reaches the AS as the S-CSCF passes it on after the
	// originating filter criteria (TS 24.147 5.3.1.3.2).
	factory := ims.ConferenceFactoryURI(domainA)
	served := sip.PServedUser{URI: p.a.sipURI(), SessionCase: sip.SessionCaseOrig, RegState: sip.RegStateRegistered}
	creator, err := p.a.ue.Invite(sip.Invitation{
		RequestURI: factory, From: p.a.sipURI(), To: factory,
		Route: []string{"<sip:" + focus.String() + ";lr>", p.a.ueRoute},
		Headers: []sip.HeaderField{
			{Name: "P-Served-User", Value: served.String()},
			{Name: "P-Asserted-Identity", Value: "<" + p.a.sipURI() + ">"},
			{Name: "P-Charging-Vector", Value: "icid-value=" + sip.NewCallID() + ";orig-ioi=" + domainA},
		},
		Body:      p.a.offer(t, sdp.VoLTECodecs(), 1, ""),
		OnRequest: p.a.answerInDialog(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	created := final(t, creator)
	conference, err := sip.ParseNameAddr(created.Get("Contact"))
	if err != nil || created.StatusCode != 200 {
		t.Fatalf("the creator got %d with Contact %q, want 200 with the conference URI", created.StatusCode, created.Get("Contact"))
	}

	// The user of network B joins and subscribes within the dialog.
	notified := make(chan *sip.Message, 8)
	joined := p.b.invite(t, conference.URI, p.b.offer(t, sdp.VoLTECodecs(), 1, ""), notified)
	if resp := final(t, joined); resp.StatusCode != 200 {
		t.Fatalf("the user of network B got %d to its INVITE to the conference URI", resp.StatusCode)
	}
	resp, err := joined.Send(sip.SUBSCRIBE, sip.Body{},
		sip.HeaderField{Name: "Event", Value: conferenceEvent},
		sip.HeaderField{Name: "Accept", Value: conferenceDocument},
		sip.HeaderField{Name: "Expires", Value: "3600"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("SUBSCRIBE within the dialog: %v %v", resp, err)
	}
	var notify *sip.Message
	for notify == nil {
		select {
		case r := <-notified:
			if r.Method == sip.NOTIFY {
				notify = r
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no NOTIFY reached the user of network B")
		}
	}
	doc, err := confinfo.Parse(notify.Body)
	if err != nil {
		t.Fatalf("the NOTIFY has no conference-info: %v\n%s", err, notify.Body)
	}
	if len(doc.Users.Users) != 2 {
		t.Fatalf("the conference state has %d users, want the creator and the user of network B:\n%s", len(doc.Users.Users), notify.Body)
	}
	for _, s := range internals(planA) {
		if strings.Contains(string(notify.Body), s) {
			t.Errorf("the conference state that reached network B has %q of network A:\n%s", s, notify.Body)
		}
	}

	if err := joined.Bye(); err != nil {
		t.Fatalf("BYE: %v", err)
	}
	waitDone(t, "the call of the user of network B", joined.Done())
	if err := creator.Bye(); err != nil {
		t.Fatalf("BYE of the creator: %v", err)
	}
	p.waitReleased(t)
	assertHidden(t, p.b, planA)
	assertHidden(t, p.a, planB)
}

// conferenceDomain is the subdomain of the conference URIs of the
// focus in the network of domain.
func conferenceDomain(domain string) string { return "conf." + domain }

// startFocus starts the conference AS of this project inside network n,
// where DNS leads the subdomain of its conference URIs, with a stand-in
// for its MRFC that answers each INVITE as n's UE would. It returns the
// address of the AS.
func startFocus(t *testing.T, n *network) netip.AddrPort {
	t.Helper()
	mrfcTL, _ := listen(t, n.plan.internal+".41", 0, "mrfc", n.rec)
	mrfc, err := sip.NewUA(mrfcTL, sip.UAConfig{
		SentBy: mrfcTL.LocalAddr().String(), Contact: "sip:mrfc@" + mrfcTL.LocalAddr().String(), Timers: testTimers,
		OnCall: func(c *sip.IncomingCall) {
			answer, err := answerTo(n, c.Request)
			if err != nil {
				c.Reject(488)
				return
			}
			c.Accept(answer)
		},
	}, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mrfc.Close)

	tl, _ := listen(t, n.plan.internal+".40", 0, "mmtel", n.rec)
	addr := tl.LocalAddr()
	store := mmtel.NewMemoryStore()
	as := mmtel.New(mmtel.Config{
		Host: addr.Addr().String(), Port: int(addr.Port()), HomeDomain: n.domain,
		MRFC: "sip:" + mrfcTL.LocalAddr().String(), ConferenceDomain: conferenceDomain(n.domain),
	}, noServices{}, store, store, n.names, discard)
	if err := as.Listen(tl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(as.Close)
	n.names.set(conferenceDomain(n.domain), addr)
	return addr
}

// noServices is an HSS that has no service settings for anybody.
type noServices struct{}

func (noServices) RepositoryData(context.Context, string, string) (sh.TransparentData, bool, error) {
	return sh.TransparentData{}, false, nil
}
