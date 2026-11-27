package sip

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

// waitArrival reads the next request of the peer that the user of the
// call got.
func waitArrival(t *testing.T, arrived <-chan *InDialogRequest, what string) *InDialogRequest {
	t.Helper()
	select {
	case r := <-arrived:
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s to reach the user of the call", what)
		return nil
	}
}

// acceptAll is the user of a call that accepts each request of the
// peer, with an answer when it has an offer.
func acceptAll(arrived chan<- *InDialogRequest) func(*InDialogRequest) {
	return func(r *InDialogRequest) {
		arrived <- r
		if r.Offer {
			r.Accept(holdAnswer)
		} else {
			r.Accept(Body{})
		}
	}
}

// RFC 3261 8.2.2.3 and 12.2.2: a request within a dialog goes through
// the checks of one outside it first. One that requires an extension
// the UA does not understand gets 420 (Bad Extension) with an
// Unsupported that lists just those of its option tags, and as a
// rejected request it changes nothing in the dialog: no remote sequence
// number, no remote target, nothing for the user of the call. A BYE
// refused this way leaves the call up.
func TestRequestWithinADialogRequiringAnUnknownExtension(t *testing.T) {
	event := HeaderField{Name: "Event", Value: "conference"}
	for _, tc := range []struct {
		method  Method
		body    Body
		headers []HeaderField
	}{
		{INVITE, holdOffer, nil},
		{UPDATE, offer2, nil},
		{"INFO", Body{}, nil},
		{MESSAGE, Body{Type: "text/plain", Content: []byte("hello")}, nil},
		{BYE, Body{}, nil},
		{OPTIONS, Body{}, nil},
		{NOTIFY, Body{}, []HeaderField{event, {Name: "Subscription-State", Value: "active;expires=60"}}},
		{REFER, Body{}, []HeaderField{{Name: "Refer-To", Value: "<sip:other@example.org>"}}},
		{SUBSCRIBE, Body{}, []HeaderField{event, {Name: "Expires", Value: "600"}}},
		// The UAC of a call takes PRACKs too.
		{PRACK, Body{}, []HeaderField{{Name: "RAck", Value: "1 1 INVITE"}}},
	} {
		t.Run(string(tc.method), func(t *testing.T) {
			arrived := make(chan *InDialogRequest, 2)
			callerUA := newUA(t, testTimers, "001010000000001", noCalls)
			out, p := rawAnswer(t, callerUA, Invitation{OnRequest: acceptAll(arrived)})
			before, _ := out.Dialog()
			moved := "sip:moved@" + addrOf(p.peer).String()

			// This UA lists timer but neither of the other two.
			req := p.request(tc.method, tc.body, append(slices.Clone(tc.headers),
				HeaderField{Name: "Contact", Value: "<" + moved + ">"},
				HeaderField{Name: "Require", Value: "foo, " + OptionPrecondition + ", " + OptionTimer})...)
			resp := readSkippingTrying(t, p.peer)
			if tc.method == INVITE {
				send(t, p.peer, newMatchingRequest(req, ACK, resp.Get("To")), callerUA.Stack())
			}
			if resp.StatusCode != 420 || resp.Get("CSeq") != req.Get("CSeq") {
				t.Fatalf("got %s with CSeq %q, want 420 for %q", startLine(resp), resp.Get("CSeq"), req.Get("CSeq"))
			}
			if got, want := resp.GetAll("Unsupported"), []string{"foo", OptionPrecondition}; !slices.Equal(got, want) {
				t.Errorf("420 has Unsupported %q, want %q", got, want)
			}
			if after, _ := out.Dialog(); !reflect.DeepEqual(after, before) {
				t.Errorf("dialog after the 420 is\n%+v\nwant it as it was\n%+v", after, before)
			}

			// The next request is the first that the user of the call gets.
			p.request("INFO", Body{})
			if r := waitArrival(t, arrived, "the INFO"); r.Request.Method != "INFO" || r.Arrival != 1 {
				t.Errorf("the user of the call got %s as arrival %d, want the INFO as the first", r.Request.Method, r.Arrival)
			}
			if ok := readSkippingTrying(t, p.peer); ok.StatusCode != 200 {
				t.Errorf("the INFO got %s, want 200", startLine(ok))
			}
			select {
			case <-out.Done():
				t.Errorf("the call ended with %v", out.Err())
			default:
			}
		})
	}
}

// A request within a dialog that requires only what the UA understands
// is taken as before: the UA's own extensions, and those its user lists
// (UAConfig.Supported).
func TestRequestWithinADialogRequiringAKnownExtension(t *testing.T) {
	for _, tc := range []struct {
		name      string
		supported []string
		method    Method
		body      Body
		headers   []HeaderField
	}{
		{"100rel", nil, INVITE, holdOffer, []HeaderField{{Name: "Require", Value: Option100rel}}},
		{"timer", nil, UPDATE, Body{}, []HeaderField{{Name: "Require", Value: OptionTimer}, {Name: "Session-Expires", Value: "1800;refresher=uac"}}},
		{"precondition", []string{OptionPrecondition}, UPDATE, offer2, []HeaderField{{Name: "Require", Value: OptionPrecondition}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			arrived := make(chan *InDialogRequest, 1)
			callerUA := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, Supported: tc.supported}, "001010000000001")
			_, p := rawAnswer(t, callerUA, Invitation{OnRequest: acceptAll(arrived)})
			req := p.request(tc.method, tc.body, append(tc.headers, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})...)
			if r := waitArrival(t, arrived, "the "+string(tc.method)); r.Request.Get("CSeq") != req.Get("CSeq") {
				t.Errorf("the user of the call got %s, want the request", r.Request.Get("CSeq"))
			}
			ok := readSkippingTrying(t, p.peer)
			if ok.StatusCode != 200 {
				t.Fatalf("got %s, want 200", startLine(ok))
			}
			if tc.method == INVITE {
				send(t, p.peer, rawACK(p.peer, req, ok), callerUA.Stack())
			}
		})
	}
}

// RFC 3261 8.2: the method is inspected before the header fields, so a
// request within a dialog whose method the UA does not take there gets
// 405 (Method Not Allowed), whatever its Require lists: here an INFO in
// a call without a user to answer it.
func TestRequestWithinADialogOfAMethodNotTakenAndAnUnknownExtension(t *testing.T) {
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	_, p := rawAnswer(t, callerUA, Invitation{})
	p.request("INFO", Body{}, HeaderField{Name: "Require", Value: "foo"})
	resp := readDatagram(t, p.peer)
	if resp.StatusCode != 405 || resp.Get("Allow") == "" || resp.Get("Unsupported") != "" {
		t.Errorf("got %s with Allow %q and Unsupported %q, want 405 with Allow only",
			startLine(resp), resp.Get("Allow"), resp.Get("Unsupported"))
	}
}

// A PRACK at the UAS goes through the check of Require as well: one
// that requires an unknown extension gets 420 and acknowledges nothing,
// so the reliable 183 still waits for the PRACK that the next one is.
func TestPRACKRequiringAnUnknownExtension(t *testing.T) {
	progress := make(chan error, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		progress <- c.Progress(183, answer)
	})
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add("Supported", Option100rel)
	send(t, peer, invite, calleeUA.Stack())
	prov := readSkippingTrying(t, peer)
	if prov.StatusCode != 183 || prov.Get("RSeq") == "" {
		t.Fatalf("got %s with RSeq %q, want a reliable 183", startLine(prov), prov.Get("RSeq"))
	}

	refused := rawPRACK(t, peer, invite, prov, 2)
	refused.Add("Require", "foo")
	send(t, peer, refused, calleeUA.Stack())
	if resp := readSkipping183(t, peer); resp.StatusCode != 420 || resp.Get("CSeq") != "2 PRACK" || resp.Get("Unsupported") != "foo" {
		t.Fatalf("PRACK with Require foo got %s with CSeq %q and Unsupported %q, want 420 with foo",
			startLine(resp), resp.Get("CSeq"), resp.Get("Unsupported"))
	}
	select {
	case err := <-progress:
		t.Fatalf("Progress returned %v after the refused PRACK, want it to wait", err)
	default:
	}

	// RFC 3261 12.2.2: the refused PRACK left the remote sequence number
	// as it was, so its CSeq is in order once more.
	send(t, peer, rawPRACK(t, peer, invite, prov, 2), calleeUA.Stack())
	if resp := readSkipping183(t, peer); resp.StatusCode != 200 || resp.Get("CSeq") != "2 PRACK" {
		t.Fatalf("PRACK got %s with CSeq %q, want 200", startLine(resp), resp.Get("CSeq"))
	}
	select {
	case err := <-progress:
		if err != nil {
			t.Errorf("Progress: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Progress did not return after the PRACK")
	}
}

// Once a BYE has ended the session, a request of the session finds no
// usage in the dialog, which the subscription keeps: it gets the 481, or
// a REFER the 603, of the ended session, whatever its Require lists.
func TestRequestOfAnEndedSessionRequiringAnUnknownExtension(t *testing.T) {
	s := subscribeWithinTheCall(t)
	if err := s.out.Bye(); err != nil {
		t.Fatal(err)
	}
	waitDone(t, s.in.Done(), "callee")
	uac, _ := s.out.Dialog()
	for method, want := range map[Method]int{"INFO": 481, REFER: 603} {
		req := uac.NewRequest(method)
		req.Add("Require", "foo")
		if resp := sendInDialog(t, s.established, req); resp.StatusCode != want {
			t.Errorf("%s after the BYE got %s, want %d", method, startLine(resp), want)
		}
	}
	s.calleeEnds()
	s.callerEnds()
}

// RFC 3261 8.2.2.3: Require is ignored in a CANCEL. The CANCEL of a
// re-INVITE still gets 200, and the re-INVITE 487.
func TestCANCELWithAnUnknownRequire(t *testing.T) {
	arrived := make(chan *InDialogRequest, 1)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	_, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) { arrived <- r }})
	reinvite := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	waitArrival(t, arrived, "the re-INVITE")
	cancel := newMatchingRequest(reinvite, CANCEL, reinvite.Get("To"))
	cancel.Add("Require", "foo")
	send(t, p.peer, cancel, callerUA.Stack())
	codes := map[string]int{}
	for len(codes) < 2 {
		if m := readDatagram(t, p.peer); m.StatusCode >= 200 {
			codes[m.Get("CSeq")] = m.StatusCode
		}
	}
	if codes["1 CANCEL"] != 200 || codes["1 INVITE"] != 487 {
		t.Errorf("got %v, want 200 for the CANCEL and 487 for the re-INVITE", codes)
	}
}

// An ACK gets no response, so not a 420 either: the ACK of a 2xx to a
// re-INVITE completes the re-INVITE whatever its Require lists.
func TestACKWithAnUnknownRequire(t *testing.T) {
	acks := make(chan reinviteResult, 1)
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	_, p := rawAnswer(t, callerUA, Invitation{OnRequest: func(r *InDialogRequest) {
		ack, err := r.Accept(holdAnswer)
		acks <- reinviteResult{ack, err}
	}})
	reinvite := p.request(INVITE, holdOffer, HeaderField{Name: "Contact", Value: "<" + p.target + ">"})
	ok := readSkippingTrying(t, p.peer)
	if ok.StatusCode != 200 {
		t.Fatalf("got %s, want 200", startLine(ok))
	}
	ack := rawACK(p.peer, reinvite, ok)
	ack.Add("Require", "foo")
	send(t, p.peer, ack, callerUA.Stack())
	select {
	case r := <-acks:
		if r.err != nil || r.ack == nil {
			t.Errorf("Accept returned %v, %v; want the ACK", r.ack, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Accept to return")
	}
}

// What the UA leaves out with the far end (UAConfig.OptionsLeftOut) is
// gone from the peer's request before the check of Require: the UA
// takes the request as if it never required the extension.
func TestRequestWithinADialogRequiringAnExtensionLeftOut(t *testing.T) {
	tr := listenLayer(t, testLogger(t))
	callerUA, err := NewUA(tr, UAConfig{
		SentBy: tr.LocalAddr().String(), Timers: testTimers, Contact: "sip:border@" + tr.LocalAddr().String(),
		OnCall:         noCalls,
		OptionsLeftOut: func(*Message, Destination, bool) []string { return []string{"foo"} },
	}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(callerUA.Close)
	arrived := make(chan *InDialogRequest, 1)
	_, p := rawAnswer(t, callerUA, Invitation{OnRequest: acceptAll(arrived)})
	p.request("INFO", Body{}, HeaderField{Name: "Require", Value: "foo"})
	if r := waitArrival(t, arrived, "the INFO"); r.Request.Get("Require") != "" {
		t.Errorf("the user of the call got the INFO with Require %q, want none", r.Request.Get("Require"))
	}
	if ok := readDatagram(t, p.peer); ok.StatusCode != 200 {
		t.Errorf("the INFO got %s, want 200", startLine(ok))
	}
}

// requiring is a request of the method to ua that requires the
// extensions of require: outside a dialog, or with toTag in the dialog
// of a subscription that ua keeps as a notifier.
func requiring(ua *UA, method Method, toTag, require string) *Message {
	m := &Message{Method: method, RequestURI: ua.cfg.Contact}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+caller+">;tag="+NewTag())
	to := "<" + calleeURI + ">"
	if toTag != "" {
		to += ";tag=" + toTag
	}
	m.Add("To", to)
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 "+string(method))
	m.Add("Contact", "<sip:peer@127.0.0.1>")
	m.Add("Require", require)
	switch method {
	case SUBSCRIBE:
		m.Add("Event", "conference")
	case MESSAGE:
		Body{Type: "text/plain", Content: []byte("hello")}.addTo(m)
	}
	return m
}

// respond200 is an OnMessage or OnSubscribe that takes every request.
func respond200(st *ServerTransaction) { st.Respond(NewResponse(st.Request, 200, NewTag())) }

// RFC 3261 8.2.2.3 holds for every request that a UA takes as a UAS:
// one outside a dialog, and a SUBSCRIBE within the dialog of a
// subscription that the UA keeps as a notifier, too. What the UA or its
// user understands is taken as before.
func TestRequestOutsideADialogRequiringAnExtension(t *testing.T) {
	ua := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, OnMessage: respond200, OnSubscribe: respond200,
		Supported: []string{OptionPrecondition}}, callee)
	for _, tc := range []struct {
		name   string
		method Method
		toTag  string
	}{
		{"INVITE", INVITE, ""},
		{"OPTIONS", OPTIONS, ""},
		{"MESSAGE", MESSAGE, ""},
		{"SUBSCRIBE", SUBSCRIBE, ""},
		{"SUBSCRIBE within a subscription", SUBSCRIBE, "notifier"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := finalTo(t, ua, requiring(ua, tc.method, tc.toTag, "foo, "+OptionTimer+", bar"))
			if got, want := refused.GetAll("Unsupported"), []string{"foo", "bar"}; refused.StatusCode != 420 || !slices.Equal(got, want) {
				t.Errorf("got %s with Unsupported %q, want 420 with %q", startLine(refused), got, want)
			}
			if tc.method == INVITE {
				return // the user of this UA turns every call down
			}
			if taken := finalTo(t, ua, requiring(ua, tc.method, tc.toTag, OptionTimer+", "+OptionPrecondition)); taken.StatusCode != 200 {
				t.Errorf("with Require of known extensions got %s, want 200", startLine(taken))
			}
		})
	}
}

// RFC 3261 8.2.2.3 and 12.2.2: a NOTIFY of a subscription that requires
// an unknown extension gets 420, and changes nothing: the subscriber
// does not see it, and a terminated state in it ends nothing.
func TestNOTIFYOfASubscriptionRequiringAnUnknownExtension(t *testing.T) {
	n := newTestNotifier(t)
	r := subscribeRequest()
	ua, got := subscriberUA(t, n, &r)
	sub, err := ua.Subscribe(r)
	if err != nil {
		t.Fatal(err)
	}
	nextNotification(t, got)

	n.mu.Lock()
	req := n.sub.NewNotify(state, time.Now())
	n.mu.Unlock()
	req.Set("Subscription-State", "terminated;reason="+ReasonNoResource)
	req.Add("Require", "foo")
	ct, err := n.stack.SendRequest(req, Destination{Transport: UDP, Addr: ua.Stack().LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	if resp := next(t, ct); resp.StatusCode != 420 || resp.Get("Unsupported") != "foo" {
		t.Fatalf("got %s with Unsupported %q, want 420 with foo", startLine(resp), resp.Get("Unsupported"))
	}

	n.notify()
	if again := nextNotification(t, got); again.State.State != SubscriptionActive {
		t.Errorf("the subscriber got a NOTIFY with state %+v, want the active one after the refused NOTIFY", again.State)
	}
	select {
	case <-sub.Done():
		t.Errorf("the subscription ended with %v", sub.Err())
	default:
	}
}

// RFC 3261 8.2: outside a dialog, too, a method that the UA does not
// take gets 405 (Method Not Allowed) before its Require is looked at.
func TestRequestOutsideADialogOfAMethodNotTakenAndAnUnknownExtension(t *testing.T) {
	ua := newUA(t, testTimers, callee, noCalls)
	for _, method := range []Method{"FOO", MESSAGE, SUBSCRIBE} {
		resp := finalTo(t, ua, requiring(ua, method, "", "foo"))
		if resp.StatusCode != 405 || resp.Get("Allow") == "" || resp.Get("Unsupported") != "" {
			t.Errorf("%s got %s with Allow %q and Unsupported %q, want 405 with Allow only",
				method, startLine(resp), resp.Get("Allow"), resp.Get("Unsupported"))
		}
	}
}
