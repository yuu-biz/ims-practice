package sip

import (
	"errors"
	"reflect"
	"testing"
)

const (
	caller     = "sip:001010000000001@" + imsDomain
	callerAt   = "sip:001010000000001@192.0.2.10:5060"
	calleeURI  = "sip:" + callee + "@" + imsDomain
	calleeAt   = "sip:" + callee + "@192.0.2.20:5060"
	nearCaller = "sip:192.0.2.1:5060;lr" // the first proxy from the caller
	nearCallee = "sip:192.0.2.2:5060;lr" // the first proxy from the callee
)

// dialogInvite is an INVITE as the callee receives it, after two
// proxies have each put their Record-Route on top (RFC 3261 16.6 step
// 4).
func dialogInvite() *Message {
	m := &Message{Method: INVITE, RequestURI: calleeAt}
	m.Add("Record-Route", "<"+nearCallee+">")
	m.Add("Record-Route", "<"+nearCaller+">")
	m.Add("From", "<"+caller+">;tag=caller-tag")
	m.Add("To", "<"+calleeURI+">")
	m.Add("Call-ID", "dialog-test")
	m.Add("CSeq", "1 INVITE")
	m.Add("Contact", "<"+callerAt+">")
	return m
}

// dialogResponse is the callee's response, as a UAS builds it: the
// Record-Route values copied in order and its own Contact (RFC 3261
// 12.1.1).
func dialogResponse(invite *Message, code int) *Message {
	resp := NewResponse(invite, code, "callee-tag")
	for _, rr := range invite.GetAll("Record-Route") {
		resp.Add("Record-Route", rr)
	}
	resp.Add("Contact", "<"+calleeAt+">")
	return resp
}

func TestUACDialogFromAProvisionalResponse(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUACDialog(invite, dialogResponse(invite, 183))
	if err != nil {
		t.Fatal(err)
	}
	want := Dialog{
		ID:           DialogID{CallID: "dialog-test", LocalTag: "caller-tag", RemoteTag: "callee-tag"},
		State:        DialogEarly,
		LocalSeq:     1,
		LocalURI:     caller,
		RemoteURI:    calleeURI,
		RemoteTarget: calleeAt,
		// RFC 3261 12.1.2: the Record-Route of the response, reversed.
		RouteSet: []string{nearCaller, nearCallee},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("dialog = %+v\nwant     %+v", d, want)
	}
}

func TestUASDialogFromTheINVITE(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUASDialog(invite, dialogResponse(invite, 200))
	if err != nil {
		t.Fatal(err)
	}
	want := Dialog{
		ID:           DialogID{CallID: "dialog-test", LocalTag: "callee-tag", RemoteTag: "caller-tag"},
		State:        DialogConfirmed,
		RemoteSeq:    1,
		LocalURI:     calleeURI,
		RemoteURI:    caller,
		RemoteTarget: callerAt,
		// RFC 3261 12.1.1: the Record-Route of the request, in order.
		RouteSet: []string{nearCallee, nearCaller},
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("dialog = %+v\nwant     %+v", d, want)
	}
}

func TestOnlyTaggedProvisionalAndSuccessResponsesCreateADialog(t *testing.T) {
	invite := dialogInvite()
	untagged := NewResponse(invite, 180, "")
	untagged.Add("Contact", "<"+calleeAt+">")
	for _, resp := range []*Message{dialogResponse(invite, 100), untagged, dialogResponse(invite, 486)} {
		if _, err := NewUACDialog(invite, resp); err == nil {
			t.Errorf("a %d with To %q created a dialog", resp.StatusCode, resp.Get("To"))
		}
	}
}

func TestRequestWithinTheDialog(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUACDialog(invite, dialogResponse(invite, 200))
	if err != nil {
		t.Fatal(err)
	}
	bye := d.NewRequest(BYE)
	// RFC 3261 12.2.1.1 with a loose router first in the route set.
	if bye.RequestURI != calleeAt {
		t.Errorf("Request-URI = %s, want the remote target %s", bye.RequestURI, calleeAt)
	}
	if got, want := bye.GetAll("Route"), []string{"<" + nearCaller + ">", "<" + nearCallee + ">"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Route = %q, want the route set %q", got, want)
	}
	for header, want := range map[string]string{
		"From":    "<" + caller + ">;tag=caller-tag",
		"To":      "<" + calleeURI + ">;tag=callee-tag",
		"Call-ID": "dialog-test",
		"CSeq":    "2 BYE",
	} {
		if got := bye.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// RFC 3261 13.2.2.4: the ACK keeps the INVITE's number and takes none.
	if ack := d.newACK(1); ack.Get("CSeq") != "1 ACK" || ack.RequestURI != calleeAt {
		t.Errorf("ACK is %s with CSeq %q", ack.RequestURI, ack.Get("CSeq"))
	}
	if next := d.NewRequest(BYE); next.Get("CSeq") != "3 BYE" {
		t.Errorf("next CSeq = %q, want 3 BYE", next.Get("CSeq"))
	}
}

func TestTheUASStartsItsOwnSequenceNumbers(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUASDialog(invite, dialogResponse(invite, 200))
	if err != nil {
		t.Fatal(err)
	}
	bye := d.NewRequest(BYE)
	if got := bye.Get("CSeq"); got != "1 BYE" {
		t.Errorf("CSeq = %q, want 1 BYE from an empty local sequence number", got)
	}
	if got := bye.Get("To"); got != "<"+caller+">;tag=caller-tag" {
		t.Errorf("To = %q, want the caller with its tag", got)
	}
}

func TestReceiveRequestKeepsTheOrder(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUASDialog(invite, dialogResponse(invite, 200))
	if err != nil {
		t.Fatal(err)
	}
	request := func(cseq string) *Message {
		m := &Message{Method: BYE, RequestURI: calleeAt}
		m.Add("CSeq", cseq)
		m.Add("Contact", "<sip:moved@192.0.2.99>")
		return m
	}
	// RFC 3261 12.2.2: a gap is fine.
	if err := d.ReceiveRequest(request("5 BYE")); err != nil || d.RemoteSeq != 5 {
		t.Fatalf("CSeq 5 after 1: err = %v, remote seq %d", err, d.RemoteSeq)
	}
	if err := d.ReceiveRequest(request("4 BYE")); !errors.Is(err, ErrOutOfOrder) || d.RemoteSeq != 5 {
		t.Errorf("CSeq 4 after 5: err = %v, remote seq %d; want ErrOutOfOrder and 5", err, d.RemoteSeq)
	}
	// A BYE is no target refresh request.
	if d.RemoteTarget != callerAt {
		t.Errorf("remote target = %s after a BYE, want %s", d.RemoteTarget, callerAt)
	}
}

func TestReINVITERefreshesTheRemoteTarget(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUASDialog(invite, dialogResponse(invite, 200))
	if err != nil {
		t.Fatal(err)
	}
	reinvite := &Message{Method: INVITE, RequestURI: calleeAt}
	reinvite.Add("CSeq", "2 INVITE")
	reinvite.Add("Contact", "<sip:moved@192.0.2.99>")
	if err := d.ReceiveRequest(reinvite); err != nil {
		t.Fatal(err)
	}
	if d.RemoteTarget != "sip:moved@192.0.2.99" {
		t.Errorf("remote target = %s, want the re-INVITE's Contact", d.RemoteTarget)
	}
}

func TestA2xxRecomputesOnlyTheRouteSet(t *testing.T) {
	invite := dialogInvite()
	d, err := NewUACDialog(invite, dialogResponse(invite, 183))
	if err != nil {
		t.Fatal(err)
	}
	d.NewRequest("PRACK")
	ok := dialogResponse(invite, 200)
	ok.Remove("Record-Route")
	ok.Add("Record-Route", "<"+nearCaller+">")
	if err := d.confirmByUAC(invite, ok); err != nil {
		t.Fatal(err)
	}
	if d.State != DialogConfirmed || !reflect.DeepEqual(d.RouteSet, []string{nearCaller}) || d.LocalSeq != 2 {
		t.Errorf("after the 2xx: %s, route set %q, local seq %d; want Confirmed, the 2xx's route, 2",
			d.State, d.RouteSet, d.LocalSeq)
	}
}

// A subscriber that keeps its subscription as data holds the half
// dialog of its SUBSCRIBE until the first NOTIFY, which completes it
// (RFC 6665 4.4.1). A refresh then goes in the dialog.
func TestSubscriberDialogFromTheFirstNotify(t *testing.T) {
	subscribe := &Message{Method: SUBSCRIBE, RequestURI: calleeURI}
	subscribe.Add("From", "<"+caller+">;tag=subscriber-tag")
	subscribe.Add("To", "<"+calleeURI+">")
	subscribe.Add("Call-ID", "subscription-test")
	subscribe.Add("CSeq", "1 SUBSCRIBE")
	d, err := NewSubscriberDialog(subscribe)
	if err != nil {
		t.Fatal(err)
	}
	if d.ID.RemoteTag != "" {
		t.Errorf("remote tag %q before any NOTIFY", d.ID.RemoteTag)
	}

	// The NOTIFY as the subscriber receives it: the proxy nearest to the
	// subscriber put its Record-Route on top last.
	notify := &Message{Method: NOTIFY, RequestURI: callerAt}
	notify.Add("Record-Route", "<"+nearCaller+">")
	notify.Add("Record-Route", "<"+nearCallee+">")
	notify.Add("From", "<"+calleeURI+">;tag=notifier-tag")
	notify.Add("To", "<"+caller+">;tag=subscriber-tag")
	notify.Add("Call-ID", "subscription-test")
	notify.Add("CSeq", "7 NOTIFY")
	notify.Add("Contact", "<"+calleeAt+">")
	if err := d.ReceiveFirstNotify(notify); err != nil {
		t.Fatal(err)
	}
	want := Dialog{
		ID:    DialogID{CallID: "subscription-test", LocalTag: "subscriber-tag", RemoteTag: "notifier-tag"},
		State: DialogConfirmed, LocalSeq: 1, RemoteSeq: 7, LocalURI: caller, RemoteURI: calleeURI,
		RemoteTarget: calleeAt, RouteSet: []string{nearCaller, nearCallee},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("dialog = %+v\nwant %+v", d, want)
	}
	refresh := d.NewRequest(SUBSCRIBE)
	if refresh.RequestURI != calleeAt || refresh.Get("To") != "<"+calleeURI+">;tag=notifier-tag" || refresh.Get("CSeq") != "2 SUBSCRIBE" {
		t.Errorf("refresh is %s with To %q and CSeq %q", refresh.RequestURI, refresh.Get("To"), refresh.Get("CSeq"))
	}
}
