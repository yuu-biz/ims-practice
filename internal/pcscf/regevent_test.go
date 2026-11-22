package pcscf

import (
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// fakeHome plays the I-CSCF and the S-CSCF behind the P-CSCF: it
// answers every REGISTER and MESSAGE with 200 and accepts every
// SUBSCRIBE, which it hands to the test.
type fakeHome struct {
	stack      *sip.Stack
	subscribes chan *sip.Message
	accepted   chan sip.NotifierSubscription
}

func newFakeHome(t *testing.T) *fakeHome {
	h := &fakeHome{subscribes: make(chan *sip.Message, 4), accepted: make(chan sip.NotifierSubscription, 4)}
	h.stack = newStack(t, func(st *sip.ServerTransaction) {
		req := st.Request
		switch req.Method {
		case sip.REGISTER:
			resp := sip.NewResponse(req, 200, sip.NewTag())
			resp.Add("Service-Route", "<sip:orig@scscf."+domain+":5060;lr>")
			resp.Add("P-Associated-URI", "<"+impu+">")
			resp.Add("Contact", "<sip:ue@127.0.0.1>;expires=3600")
			st.Respond(resp)
		case sip.MESSAGE:
			st.Respond(sip.NewResponse(req, 200, sip.NewTag()))
		case sip.SUBSCRIBE:
			h.subscribes <- req
			ok, sub, err := sip.AcceptSubscription(req, time.Hour, "sip:scscf."+domain+":5060", time.Now())
			if err != nil {
				t.Errorf("AcceptSubscription: %v", err)
				return
			}
			st.Respond(ok)
			h.accepted <- sub
		}
	})
	return h
}

// notify sends a NOTIFY with doc to the P-CSCF and returns the answer.
func (h *fakeHome) notify(t *testing.T, sub *sip.NotifierSubscription, pcscf *sip.Stack, doc reginfo.Document) *sip.Message {
	t.Helper()
	doc.State = reginfo.Full
	body, err := doc.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	req := sub.NewNotify(sip.Body{Type: reginfo.ContentType, Content: body}, time.Now())
	req.Add("P-Charging-Vector", "icid-value=x;orig-ioi=home.example.org")
	ct, err := h.stack.SendRequest(req, sip.Destination{Transport: sip.UDP, Addr: pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no answer to the NOTIFY: %v", ct.Err())
	}
	return final
}

func registration(state, contactState reginfo.State, event reginfo.Event) reginfo.Document {
	return reginfo.Document{Registrations: []reginfo.Registration{{AOR: impu, ID: "reg1", State: state,
		Contacts: []reginfo.Contact{{ID: "c1", State: contactState, Event: event, URI: "sip:ue@127.0.0.1"}}}}}
}

func TestPCSCFSubscribesToTheRegEventAndEndsTheAssociation(t *testing.T) {
	grace := associationGrace
	associationGrace = 300 * time.Millisecond
	t.Cleanup(func() { associationGrace = grace })
	home := newFakeHome(t)
	p := New(Config{Host: "pcscf." + domain, Port: 5060, VisitedNetworkID: domain},
		locatorFunc(func(string) []sip.Destination {
			return []sip.Destination{{Transport: sip.UDP, Addr: home.stack.LocalAddr()}}
		}),
		NewMemoryStore(), nil, quiet())
	pcscfStack := newPCSCFStack(t, p)
	p.SetStack(pcscfStack)
	// Fast timers, so that a request that the P-CSCF discards times out
	// soon.
	ue := newStackWith(t, fast, func(st *sip.ServerTransaction) { st.Respond(sip.NewResponse(st.Request, 200, sip.NewTag())) })
	// send sends a MESSAGE from one stack to the P-CSCF, and returns its
	// final response, or nil when none comes.
	send := func(from *sip.Stack, m *sip.Message) *sip.Message {
		t.Helper()
		m.Add("Max-Forwards", "70")
		m.Add("Call-ID", sip.NewCallID())
		m.Add("CSeq", "1 MESSAGE")
		ct, err := from.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		var final *sip.Message
		for r := range ct.Responses() {
			final = r
		}
		return final
	}
	// toUE is a request from the network to the UE's contact, and fromUE
	// one from the UE; each returns the status code of its answer, or 0
	// for none.
	status := func(r *sip.Message) int {
		if r == nil {
			return 0
		}
		return r.StatusCode
	}
	toUE := func() int {
		t.Helper()
		m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:ue@127.0.0.1"}
		m.Add("Route", "<sip:term@pcscf."+domain+":5060;lr>")
		m.Add("From", "<sip:001010000000002@"+domain+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		return status(send(home.stack, m))
	}
	fromUE := func() int {
		t.Helper()
		m := &sip.Message{Method: sip.MESSAGE, RequestURI: "sip:001010000000002@" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<sip:001010000000002@"+domain+">")
		return status(send(ue, m))
	}

	register := func(cseq string) {
		t.Helper()
		m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
		m.Add("From", "<"+impu+">;tag="+sip.NewTag())
		m.Add("To", "<"+impu+">")
		m.Add("Call-ID", "call-1")
		m.Add("CSeq", cseq+" REGISTER")
		m.Add("Contact", "<sip:ue@127.0.0.1>;expires=600000")
		m.Add("Authorization", `Digest username="`+impi+`", realm="`+domain+`", nonce="n", uri="sip:`+domain+`", response="r"`)
		ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: pcscfStack.LocalAddr()})
		if err != nil {
			t.Fatal(err)
		}
		for range ct.Responses() {
		}
	}
	register("1")

	var subscribe *sip.Message
	select {
	case subscribe = <-home.subscribes:
	case <-time.After(5 * time.Second):
		t.Fatal("the P-CSCF did not subscribe")
	}
	// TS 24.229 5.2.3 items a to g.
	from, _ := sip.ParseNameAddr(subscribe.Get("From"))
	for name, got := range map[string]bool{
		"Request-URI":         subscribe.RequestURI == impu,
		"From":                from.URI == "sip:pcscf."+domain+":5060",
		"To":                  subscribe.Get("To") == "<"+impu+">",
		"Event":               subscribe.Get("Event") == "reg",
		"Expires":             subscribe.Get("Expires") == "3761",
		"P-Asserted-Identity": subscribe.Get("P-Asserted-Identity") == "<sip:term@pcscf."+domain+":5060;lr>",
		"P-Charging-Vector":   strings.HasPrefix(subscribe.Get("P-Charging-Vector"), "icid-value=") && strings.HasSuffix(subscribe.Get("P-Charging-Vector"), ";orig-ioi="+domain),
		"Contact":             subscribe.Get("Contact") == "<sip:pcscf."+domain+":5060>",
	} {
		if !got {
			t.Errorf("SUBSCRIBE %s wrong:\n%s", name, subscribe)
		}
	}
	sub := <-home.accepted

	resp := home.notify(t, &sub, pcscfStack, registration(reginfo.Active, reginfo.Active, reginfo.Registered))
	if resp.StatusCode != 200 || !strings.Contains(resp.Get("P-Charging-Vector"), "orig-ioi=home.example.org;term-ioi="+domain) {
		t.Fatalf("NOTIFY answered %d with P-Charging-Vector %q", resp.StatusCode, resp.Get("P-Charging-Vector"))
	}

	// A refresh is no first registration: no second SUBSCRIBE.
	register("2")
	select {
	case m := <-home.subscribes:
		t.Fatalf("second SUBSCRIBE while the first holds:\n%s", m)
	case <-time.After(200 * time.Millisecond):
	}
	if got := fromUE(); got != 200 {
		t.Fatalf("a request from the registered UE was answered %d, want the 200 of the home network", got)
	}
	if got := toUE(); got != 200 {
		t.Fatalf("a request to the registered UE was answered %d, want the UE's 200", got)
	}

	sub.Terminate(sip.ReasonNoResource)
	if resp := home.notify(t, &sub, pcscfStack, registration(reginfo.Terminated, reginfo.Terminated, reginfo.Expired)); resp.StatusCode != 200 {
		t.Fatalf("last NOTIFY answered %d", resp.StatusCode)
	}
	// 5.2.5.2 NOTE 2: the last NOTIFY must still reach the UE, so a
	// request to it goes on for the grace, and finds no registration
	// after. The UE's own requests are discarded at once (5.2.1).
	if got := toUE(); got != 200 {
		t.Errorf("a request to the UE within the grace was answered %d, want the UE's 200", got)
	}
	if got := fromUE(); got != 0 {
		t.Errorf("a request from the UE after the registration ended was answered %d, want nothing (TS 24.229 5.2.5.2)", got)
	}
	time.Sleep(associationGrace + 200*time.Millisecond)
	if got := toUE(); got != 480 {
		t.Errorf("a request to the UE after the grace was answered %d, want 480", got)
	}
}

func TestRemovedByTheNetwork(t *testing.T) {
	active := reginfo.Registration{State: reginfo.Active}
	for _, tc := range []struct {
		r    reginfo.Registration
		c    reginfo.Contact
		want bool
	}{
		{reginfo.Registration{State: reginfo.Terminated}, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Expired}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Deactivated}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Rejected}, true},
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Unregistered}, true},
		// Another contact of the identity expired; this one stays.
		{active, reginfo.Contact{State: reginfo.Terminated, Event: reginfo.Expired}, false},
		{active, reginfo.Contact{State: reginfo.Active, Event: reginfo.Refreshed}, false},
	} {
		if got := removedByTheNetwork(tc.r, tc.c); got != tc.want {
			t.Errorf("%s registration, %s/%s contact: %v, want %v", tc.r.State, tc.c.State, tc.c.Event, got, tc.want)
		}
	}
}
