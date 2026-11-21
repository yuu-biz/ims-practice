package sip

import (
	"reflect"
	"strings"
	"testing"
)

// options is an OPTIONS outside a dialog to the UA ua (RFC 3261 11.1).
func options(ua *UA) *Message {
	m := &Message{Method: OPTIONS, RequestURI: ua.cfg.Contact}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+caller+">;tag="+NewTag())
	m.Add("To", "<"+calleeURI+">")
	m.Add("Call-ID", NewCallID())
	m.Add("CSeq", "1 OPTIONS")
	m.Add("Accept", "application/sdp")
	return m
}

// RFC 3261 11.2: the answer to an OPTIONS lists what the UA can do.
func TestOPTIONSIsAnsweredWithTheCapabilities(t *testing.T) {
	icsi := Params{{Name: "+g.3gpp.icsi-ref", Value: `"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`}}
	ua := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls, ContactParams: icsi, Supported: []string{OptionPrecondition}}, callee)
	resp := finalTo(t, ua, options(ua))
	if resp.StatusCode != 200 || tagOf(resp.Get("To")) == "" {
		t.Fatalf("OPTIONS got %s with To %q, want 200 with a tag", startLine(resp), resp.Get("To"))
	}
	want := map[string]string{
		"Allow":           "INVITE, ACK, CANCEL, BYE, PRACK, UPDATE, OPTIONS, NOTIFY",
		"Accept":          "application/sdp",
		"Accept-Encoding": "identity",
		"Accept-Language": "en",
		"Supported":       "100rel, timer, 199, precondition",
	}
	for name, value := range want {
		if got := strings.Join(resp.GetAll(name), ", "); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	// IR.92 asks for the MMTel ICSI in this Contact; the UA writes the feature tags of its configuration.
	contact, err := ParseNameAddr(resp.Get("Contact"))
	if err != nil || contact.URI != ua.cfg.Contact || !reflect.DeepEqual(contact.Params, icsi) {
		t.Errorf("Contact = %q, want the UA's URI with its feature tags", resp.Get("Contact"))
	}
}

// RFC 3261 11.2: the status code is the one an INVITE would get now,
// and the body describes the media that the UA takes.
func TestOPTIONSTellsWhatAnINVITEWouldGet(t *testing.T) {
	ua := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls,
		OnOptions: func(*Message) (int, Body) { return 486, offer }}, callee)
	resp := finalTo(t, ua, options(ua))
	if resp.StatusCode != 486 || string(resp.Body) != string(offer.Content) || resp.Get("Content-Type") != offer.Type {
		t.Errorf("OPTIONS got %s with body %q, want 486 with the capabilities", startLine(resp), resp.Body)
	}
}

// RFC 3261 11.2: an OPTIONS within a dialog gets the same answer and
// leaves the dialog alone.
func TestOPTIONSWithinADialog(t *testing.T) {
	e := establish(t)
	resp, err := e.out.Send(OPTIONS, Body{})
	if err != nil || resp.StatusCode != 200 || resp.Get("Allow") == "" || resp.Get("Supported") == "" {
		t.Fatalf("OPTIONS within the dialog got %v, %v; want 200 with Allow and Supported", resp, err)
	}
	if err := e.out.Bye(); err != nil {
		t.Errorf("Bye after the OPTIONS: %v", err)
	}
	waitDone(t, e.in.Done(), "callee")
}
