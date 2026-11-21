package pcscf

import (
	"slices"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestResponsesOfTheUEAssertTheCalledIdentity is TS 24.229 5.2.6.4.4
// step 1: the 180 and the 200 of the UE leave the P-CSCF with the
// identity of the P-Called-Party-ID as the asserted one, whatever the UE
// put in.
func TestResponsesOfTheUEAssertTheCalledIdentity(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	out, err := h.core.Invite(sip.Invitation{RequestURI: h.ueContact, From: "sip:001010000000002@" + domain, To: impu,
		Route:   []string{"<sip:term@pcscf." + domain + ":5060;lr>"},
		Headers: []sip.HeaderField{{Name: "P-Called-Party-ID", Value: "<" + impu + ">"}},
		Body:    sdpBody(sdp.Offer("1", coreMedia))})
	if err != nil {
		t.Fatal(err)
	}
	in := receiveCall(t, h.toUE)
	claimed := []sip.HeaderField{
		{Name: "P-Asserted-Identity", Value: "<sip:somebody-else@" + domain + ">"},
		{Name: "P-Preferred-Identity", Value: "<sip:somebody-else@" + domain + ">"},
	}
	if err := in.Progress(180, sip.Body{}, claimed...); err != nil {
		t.Fatal(err)
	}
	go in.Accept(answerTo(t, in.Request, ueMedia), claimed...)
	var got []*sip.Message
	for resp := range out.Responses() {
		if resp.StatusCode > 100 {
			got = append(got, resp)
		}
	}
	if len(got) != 2 {
		t.Fatalf("the far end got %d responses, want the 180 and the 200", len(got))
	}
	for _, resp := range got {
		if pai := resp.GetAll("P-Asserted-Identity"); len(pai) != 1 || pai[0] != "<"+impu+">" || resp.Get("P-Preferred-Identity") != "" {
			t.Errorf("%d has P-Asserted-Identity %q and P-Preferred-Identity %q, want <%s> and none",
				resp.StatusCode, pai, resp.Get("P-Preferred-Identity"), impu)
		}
	}
}

// TestResponseOfAStandaloneRequestAssertsTheCalledIdentity is TS 24.229
// 5.2.6.4.8 step 2: any response to a MESSAGE to the UE, here the 405 of
// a UE that takes no MESSAGE, gets the asserted identity too.
func TestResponseOfAStandaloneRequestAssertsTheCalledIdentity(t *testing.T) {
	h := newCallHarness(t, harnessConfig{})
	msg := &sip.Message{Method: sip.MESSAGE, RequestURI: h.ueContact}
	msg.Add("Route", "<sip:term@pcscf."+domain+":5060;lr>")
	msg.Add("From", "<sip:001010000000002@"+domain+">;tag="+sip.NewTag())
	msg.Add("To", "<"+impu+">")
	msg.Add("Call-ID", sip.NewCallID())
	msg.Add("CSeq", "1 MESSAGE")
	msg.Add("Max-Forwards", "70")
	msg.Add("P-Called-Party-ID", "<"+impu+">")
	ct, err := h.core.Stack().SendRequest(msg, sip.Destination{Transport: sip.UDP, Addr: h.pcscf.LocalAddr()})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for resp := range ct.Responses() {
		final = resp
	}
	if final == nil || final.StatusCode != 405 {
		t.Fatalf("the MESSAGE got %v, %v; want the UE's 405", final, ct.Err())
	}
	if got := final.Get("P-Asserted-Identity"); got != "<"+impu+">" {
		t.Errorf("the 405 has P-Asserted-Identity %q, want <%s>", got, impu)
	}
}

// TestResponseToTheUEWithholdsAPrivateIdentity is RFC 3325 7 at the edge
// of the trust domain (TS 24.229 4.4.2): with Privacy: id the caller's
// UE gets the callee's answer without P-Asserted-Identity, and with "id"
// still there; without it the identity reaches the UE.
func TestResponseToTheUEWithholdsAPrivateIdentity(t *testing.T) {
	callee := "<sip:001010000000002@" + domain + ">"
	for _, tc := range []struct {
		name    string
		privacy string
		want    string
	}{
		{"withheld", "id", ""},
		{"presented", "", callee},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCallHarness(t, harnessConfig{})
			out, in := h.call()
			headers := []sip.HeaderField{{Name: "P-Asserted-Identity", Value: callee}}
			if tc.privacy != "" {
				headers = append(headers, sip.HeaderField{Name: "Privacy", Value: tc.privacy})
			}
			go in.Accept(answerTo(t, in.Request, coreMedia), headers...)
			resp := answered(t, out, 200)
			if got := resp.Get("P-Asserted-Identity"); got != tc.want {
				t.Errorf("the UE got P-Asserted-Identity %q, want %q", got, tc.want)
			}
			if got := resp.Get("Privacy"); got != tc.privacy {
				t.Errorf("the UE got Privacy %q, want %q", got, tc.privacy)
			}
		})
	}
}

// TestAssertedIdentityOfTheCaller is TS 24.229 5.2.6.3.1: a
// P-Preferred-Identity that names a registered identity picks the one
// the P-CSCF asserts, a second one adds the alternative identity, and
// otherwise the default public identity is asserted. The temporary
// public identity is not registered: P-Associated-URI does not list it.
func TestAssertedIdentityOfTheCaller(t *testing.T) {
	const (
		sipURI    = "sip:+12025550110@" + domain
		telURI    = "tel:+12025550110"
		temporary = "sip:001010000000010@" + domain
	)
	a := Association{PublicIdentities: []string{sipURI, telURI}}
	for _, tc := range []struct {
		name      string
		preferred []string
		want      []string
	}{
		{"none", nil, []string{sipURI}},
		{"the temporary identity", []string{"<" + temporary + ">"}, []string{sipURI}},
		{"somebody else", []string{"<sip:somebody-else@" + domain + ">"}, []string{sipURI}},
		{"the tel URI", []string{"<" + telURI + ">"}, []string{telURI}},
		{"the tel URI as a SIP URI", []string{"<sip:+12025550110@other.example;user=phone>"}, []string{telURI}},
		{"both", []string{`"Alice" <` + telURI + ">", "<" + sipURI + ">"}, []string{telURI, sipURI}},
		{"one that is not registered first", []string{"<" + temporary + ">", "<" + telURI + ">"}, []string{telURI}},
	} {
		req := &sip.Message{Method: sip.INVITE, RequestURI: "tel:+12025550112"}
		for _, v := range tc.preferred {
			req.Add("P-Preferred-Identity", v)
		}
		if got := assertedIdentities(req, a); !slices.Equal(got, tc.want) {
			t.Errorf("%s: asserted %v, want %v", tc.name, got, tc.want)
		}
	}
}
