package sip

import (
	"errors"
	"reflect"
	"testing"
)

// What a UE or an AS has to say in its INVITE beyond the offer: who
// calls, under which name, with which capabilities and extensions.
func TestInvitationAndConfigurationShapeTheINVITE(t *testing.T) {
	icsi := Params{{Name: "+g.3gpp.icsi-ref", Value: `"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`}, {Name: "audio"}}
	callerUA := newUAWith(t, UAConfig{Timers: testTimers, OnCall: noCalls,
		ContactParams: icsi, Supported: []string{OptionPrecondition}, Allow: []Method{"REFER"}}, "001010000000001")
	peer := rawSocket(t)
	target := "sip:" + callee + "@" + addrOf(peer).String()
	_, err := callerUA.Invite(Invitation{
		RequestURI: target, From: "sip:anonymous@anonymous.invalid", FromDisplayName: "Anonymous", Body: offer,
		Headers: []HeaderField{
			Privacy{PrivacyID}.Header(),
			{Name: "Max-Forwards", Value: "69"},
			PEarlyMedia{Supported: true}.Header(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	invite := readDatagram(t, peer)

	from, err := ParseNameAddr(invite.Get("From"))
	if err != nil || from.DisplayName != "Anonymous" || from.URI != "sip:anonymous@anonymous.invalid" || tagOf(invite.Get("From")) == "" {
		t.Errorf("From = %q, want the display name, the URI and a tag", invite.Get("From"))
	}
	contact, err := ParseNameAddr(invite.Get("Contact"))
	if err != nil || contact.URI != callerUA.cfg.Contact || !reflect.DeepEqual(contact.Params, icsi) {
		t.Errorf("Contact = %q, want the UA's URI with its feature tags", invite.Get("Contact"))
	}
	// A header field of the user replaces the UA's own of that name.
	if got := invite.GetAll("Max-Forwards"); !reflect.DeepEqual(got, []string{"69"}) {
		t.Errorf("Max-Forwards = %q, want the user's 69 alone", got)
	}
	if got := invite.GetAll("Supported"); !reflect.DeepEqual(got, []string{"100rel", "timer", "199", "precondition"}) {
		t.Errorf("Supported = %q, want the UA's tags and then the user's", got)
	}
	if got := invite.GetAll("Allow"); got[len(got)-1] != "REFER" {
		t.Errorf("Allow = %q, want the user's REFER last", got)
	}
	if invite.Get("Privacy") != "id" || invite.Get("P-Early-Media") != "supported" {
		t.Errorf("Privacy %q and P-Early-Media %q, want the user's header fields", invite.Get("Privacy"), invite.Get("P-Early-Media"))
	}
}

// RFC 3261 8.2.2.3: an extension that the user of the UA handles is as
// good as one of the UA's own.
func TestExtensionOfTheUserIsNotRefused(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUAWith(t, UAConfig{Timers: testTimers, Supported: []string{OptionPrecondition},
		OnCall: func(c *IncomingCall) { calls <- c; c.Reject(486) }}, callee)
	peer := rawSocket(t)
	invite := rawINVITE(peer)
	invite.Add("Require", "precondition")
	send(t, peer, invite, calleeUA.Stack())
	if resp := readSkippingTrying(t, peer); resp.StatusCode != 486 {
		t.Errorf("got %s, want the 486 of OnCall", startLine(resp))
	}
}

// The header fields that the callee's user adds to its responses, and
// the Reason of each side's hang-up as the other side sees it.
func TestUserHeadersInResponsesAndBYE(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{}, PEarlyMedia{Directions: []string{EarlyMediaInactive}}.Header(),
			HeaderField{Name: "Alert-Info", Value: "<urn:alert:service:call-waiting>"})
		if _, err := c.Accept(answer, HeaderField{Name: "P-Asserted-Identity", Value: "<" + calleeURI + ">"}); err != nil {
			t.Error(err)
		}
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if !reflect.DeepEqual(codes(got), []int{180, 200}) {
		t.Fatalf("caller got %v, want 180 and 200", codes(got))
	}
	if got[0].Get("P-Early-Media") != "inactive" || got[0].Get("Alert-Info") != "<urn:alert:service:call-waiting>" {
		t.Errorf("180 has P-Early-Media %q and Alert-Info %q, want the callee's", got[0].Get("P-Early-Media"), got[0].Get("Alert-Info"))
	}
	if got[1].Get("P-Asserted-Identity") != "<"+calleeURI+">" {
		t.Errorf("200 has P-Asserted-Identity %q, want the callee's", got[1].Get("P-Asserted-Identity"))
	}
	in := <-calls

	why := NewReason("RELEASE_CAUSE", 1, "User ends call")
	if err := out.Bye(why.Header()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, in.Done(), "callee")
	bye := in.EndedBy()
	if bye == nil || bye.Method != BYE || !reflect.DeepEqual(Reasons(bye), []Reason{why}) {
		t.Errorf("callee's call was ended by %v, want the BYE with its Reason", bye)
	}
	if out.EndedBy() != nil {
		t.Errorf("caller's call was ended by %v, want nil for the side that hung up", out.EndedBy())
	}
}

// RFC 3326 2: a CANCEL may say why, and the UAS learns it.
func TestCANCELCarriesAReason(t *testing.T) {
	calls := make(chan *IncomingCall, 1)
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Progress(180, Body{})
		calls <- c
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	<-out.Responses()
	// What an AS sends when its no-reply timer for a diversion fires.
	noReply := NewReason(ReasonSIP, 408, "")
	if err := out.Cancel(noReply.Header()); err != nil {
		t.Fatal(err)
	}
	in := <-calls
	waitDone(t, in.Done(), "callee")
	cancel := in.EndedBy()
	if !errors.Is(in.Err(), ErrCancelled) || cancel == nil || cancel.Method != CANCEL || !reflect.DeepEqual(Reasons(cancel), []Reason{noReply}) {
		t.Errorf("callee's call ended with %v by %v, want ErrCancelled by the CANCEL with its Reason", in.Err(), cancel)
	}
}

// A final response other than 2xx may carry what tells the caller more.
func TestRejectWithHeaders(t *testing.T) {
	calleeUA := newUA(t, testTimers, callee, func(c *IncomingCall) {
		c.Reject(603, HeaderField{Name: "Warning", Value: `399 ims.example "Incoming calls barred"`})
	})
	callerUA := newUA(t, testTimers, "001010000000001", noCalls)
	out, err := callerUA.Invite(invitation(calleeUA))
	if err != nil {
		t.Fatal(err)
	}
	got := responses(t, out)
	if len(got) != 1 || got[0].StatusCode != 603 || got[0].Get("Warning") != `399 ims.example "Incoming calls barred"` {
		t.Errorf("caller got %v, want 603 with the Warning", codes(got))
	}
}
