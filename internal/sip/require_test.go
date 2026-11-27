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
// number, no remote target, nothing for the user of the call.
func TestRequestWithinADialogRequiringAnUnknownExtension(t *testing.T) {
	for _, tc := range []struct {
		method Method
		body   Body
	}{
		{INVITE, holdOffer},
		{UPDATE, offer2},
		{"INFO", Body{}},
		{MESSAGE, Body{Type: "text/plain", Content: []byte("hello")}},
	} {
		t.Run(string(tc.method), func(t *testing.T) {
			arrived := make(chan *InDialogRequest, 2)
			callerUA := newUA(t, testTimers, "001010000000001", noCalls)
			out, p := rawAnswer(t, callerUA, Invitation{OnRequest: acceptAll(arrived)})
			before, _ := out.Dialog()
			moved := "sip:moved@" + addrOf(p.peer).String()

			// This UA lists timer but neither of the other two.
			req := p.request(tc.method, tc.body,
				HeaderField{Name: "Contact", Value: "<" + moved + ">"},
				HeaderField{Name: "Require", Value: "foo, " + OptionPrecondition + ", " + OptionTimer})
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
