package scscf

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
)

// fakeResolver maps host names to loopback stacks and numbers to URIs.
type fakeResolver struct {
	hosts map[string]netip.AddrPort
	enum  map[string]string
}

func (f fakeResolver) Locate(_ context.Context, host string, _ int, _ sip.Transport) ([]sip.Destination, error) {
	if a, ok := f.hosts[host]; ok {
		return []sip.Destination{{Transport: sip.UDP, Addr: a}}, nil
	}
	return nil, errors.New("no such host " + host)
}

func (f fakeResolver) ENUM(_ context.Context, number, _ string) (string, error) {
	aus, err := sipdns.ApplicationUniqueString(number)
	if err != nil {
		return "", err
	}
	if uri, ok := f.enum[aus]; ok {
		return uri, nil
	}
	return "", sipdns.ErrNoENUMRecord
}

// nextHop is a stack that records the request it gets and answers 486,
// so that no dialog is left behind.
func nextHop(t *testing.T) (*sip.Stack, <-chan *sip.Message) {
	got := make(chan *sip.Message, 1)
	s := newStack(t, func(st *sip.ServerTransaction) {
		got <- st.Request
		st.Respond(sip.NewResponse(st.Request, 486, sip.NewTag()))
	})
	return s, got
}

// registeredHarness registers the test user, whose REGISTER carried the
// Path of a P-CSCF, and returns the harness with the two next hops.
func registeredHarness(t *testing.T) (h *harness, icscf, pcscf <-chan *sip.Message) {
	t.Helper()
	return registeredHarnessWith(t, "")
}

// registeredHarnessWith is registeredHarness with contactParams after
// the registered contact, such as ";+g.3gpp.smsip".
func registeredHarnessWith(t *testing.T, contactParams string) (h *harness, icscf, pcscf <-chan *sip.Message) {
	t.Helper()
	icscfStack, icscfGot := nextHop(t)
	pcscfStack, pcscfGot := nextHop(t)
	resolver := fakeResolver{
		hosts: map[string]netip.AddrPort{domain: icscfStack.LocalAddr(), "pcscf." + domain: pcscfStack.LocalAddr()},
		enum:  map[string]string{"+12025550102": "sip:001010000000002@" + domain},
	}
	h = newHarnessWith(t, Config{}, resolver)
	h.contactParams = contactParams
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d", r.StatusCode)
	}
	return h, icscfGot, pcscfGot
}

// newRequest is an initial request from the test user, as the P-CSCF
// passes it on: route on top, and the user it asserts.
func newRequest(method sip.Method, requestURI, route, assertedIdentity string) *sip.Message {
	m := &sip.Message{Method: method, RequestURI: requestURI}
	m.Add("Max-Forwards", "69")
	m.Add("Route", route)
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+requestURI+">")
	m.Add("Call-ID", sip.NewCallID())
	m.Add("CSeq", "1 "+string(method))
	if assertedIdentity != "" {
		m.Add("P-Asserted-Identity", "<"+assertedIdentity+">")
	}
	return m
}

func (h *harness) invite(requestURI, route, assertedIdentity string) *sip.Message {
	h.t.Helper()
	m := newRequest(sip.INVITE, requestURI, route, assertedIdentity)
	m.Add("Contact", "<sip:001010000000001@192.0.2.10:5060>")
	return h.send(m)
}

// sms is a MESSAGE that carries an SMS (TS 24.341 5.3.1.2). The body
// only stands in for an RP-DATA.
func sms(requestURI, route, assertedIdentity string) *sip.Message {
	m := newRequest(sip.MESSAGE, requestURI, route, assertedIdentity)
	m.Add("Content-Type", "application/vnd.3gpp.sms")
	m.Body = []byte{0x00, 0x01, 0x00, 0x00}
	return m
}

// send sends m to the S-CSCF and returns the final response.
func (h *harness) send(m *sip.Message) *sip.Message {
	h.t.Helper()
	ct, err := h.ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: h.scscf.LocalAddr()})
	if err != nil {
		h.t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		if r.StatusCode >= 200 {
			final = r
		}
	}
	if final == nil {
		h.t.Fatalf("no final response: %v", ct.Err())
	}
	return final
}

func received(t *testing.T, got <-chan *sip.Message) *sip.Message {
	t.Helper()
	select {
	case m := <-got:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("the next hop got no request")
		return nil
	}
}

const orig = "<sip:orig@scscf." + domain + ":5060;lr>"

func TestOriginatingCallToATelURIGoesToTheICSCF(t *testing.T) {
	h, icscf, _ := registeredHarness(t)
	if r := h.invite("tel:+1-202-555-0102", orig, impu); r.StatusCode != 486 {
		t.Fatalf("caller got %d, want the 486 of the next hop", r.StatusCode)
	}
	got := received(t, icscf)
	if got.RequestURI != "sip:001010000000002@"+domain {
		t.Errorf("Request-URI = %q, want the ENUM result", got.RequestURI)
	}
	if got.Get("Route") != "" {
		t.Errorf("Route %q left; the S-CSCF must remove its own", got.Get("Route"))
	}
	if got.Get("Record-Route") != "<sip:scscf."+domain+":5060;lr>" {
		t.Errorf("Record-Route = %q", got.Get("Record-Route"))
	}
}

// gruu is the "gr" of a public GRUU (RFC 5627 3.1).
const gruu = "urn:uuid:f81d4fae-7dec-11d0-a765-00a0c91e6bf6"

// TestOriginatingCallToANumberInASIPURI: a SIP URI of a global number
// with user=phone in the home domain, as an AS writes the target of a
// diversion, is translated with ENUM like a tel URI (TS 24.229 5.4.3.2
// step 10); one without user=phone is not, and neither is a GRUU, which
// names one UA instance of the user rather than the user's number.
func TestOriginatingCallToANumberInASIPURI(t *testing.T) {
	for uri, want := range map[string]string{
		"sip:+1-202-555-0102@" + domain + ";user=phone;cause=302": "sip:001010000000002@" + domain,
		"sip:+12025550102@" + domain:                              "sip:+12025550102@" + domain,
		"sip:+12025550102@" + domain + ";user=phone;gr=" + gruu:   "sip:+12025550102@" + domain + ";user=phone;gr=" + gruu,
	} {
		t.Run(uri, func(t *testing.T) {
			h, icscf, _ := registeredHarness(t)
			h.invite(uri, orig, impu)
			if got := received(t, icscf); got.RequestURI != want {
				t.Errorf("Request-URI = %q, want %q", got.RequestURI, want)
			}
		})
	}
}

func TestOriginatingCallToANumberWithoutENUMGets404(t *testing.T) {
	h, _, _ := registeredHarness(t)
	if r := h.invite("tel:+12025550198", orig, impu); r.StatusCode != 404 {
		t.Fatalf("got %d, want 404", r.StatusCode)
	}
}

func TestOriginatingCallFromAnUnregisteredUserIsForbidden(t *testing.T) {
	h, _, _ := registeredHarness(t)
	if r := h.invite("sip:001010000000002@"+domain, orig, "sip:someone-else@"+domain); r.StatusCode != 403 {
		t.Fatalf("got %d, want 403", r.StatusCode)
	}
}

const peerDomain = "ims.mnc002.mcc001.3gppnetwork.org"

// interconnectHarness is registeredHarness for an S-CSCF with an IBCF.
// ENUM has a number of the home network and one of a peer network, and
// the next hops are the I-CSCF of the home domain, the IBCF and the
// peer network itself.
func interconnectHarness(t *testing.T, ibcf string) (h *harness, icscf, toIBCF, toPeer <-chan *sip.Message) {
	t.Helper()
	icscfStack, icscfGot := nextHop(t)
	ibcfStack, ibcfGot := nextHop(t)
	peerStack, peerGot := nextHop(t)
	resolver := fakeResolver{
		hosts: map[string]netip.AddrPort{domain: icscfStack.LocalAddr(), "ibcf." + domain: ibcfStack.LocalAddr(), peerDomain: peerStack.LocalAddr()},
		enum: map[string]string{
			"+12025550102": "sip:001010000000002@" + domain,
			"+12025550151": "sip:+12025550151@" + peerDomain + ";user=phone",
		},
	}
	var cfg Config
	if ibcf != "" {
		u, err := sip.ParseURI(ibcf)
		if err != nil {
			t.Fatal(err)
		}
		cfg.IBCF = u
	}
	h = newHarnessWith(t, cfg, resolver)
	callID := sip.NewCallID()
	r := h.register(callID, 1, 600000, initial)
	auth, _ := answer(t, r, password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d", r.StatusCode)
	}
	return h, icscfGot, ibcfGot, peerGot
}

// TestOriginatingCallToAnotherNetworkLeavesThroughTheIBCF: a number that
// ENUM gives a peer network's domain for, and a SIP URI of that domain,
// go to the IBCF, which is on top of Route (TS 24.229 5.4.3.2 steps 11
// and 12). The S-CSCF still record-routes (step 13).
func TestOriginatingCallToAnotherNetworkLeavesThroughTheIBCF(t *testing.T) {
	for uri, want := range map[string]string{
		"tel:+12025550151":                              "sip:+12025550151@" + peerDomain + ";user=phone",
		"sip:001020000000001@" + peerDomain:             "sip:001020000000001@" + peerDomain,
		"sip:001020000000001@PCSCF." + peerDomain + ".": "sip:001020000000001@PCSCF." + peerDomain + ".",
	} {
		t.Run(uri, func(t *testing.T) {
			h, _, ibcf, _ := interconnectHarness(t, "sip:ibcf."+domain+":5060")
			if r := h.invite(uri, orig, impu); r.StatusCode != 486 {
				t.Fatalf("caller got %d, want the 486 of the IBCF", r.StatusCode)
			}
			got := received(t, ibcf)
			if got.RequestURI != want {
				t.Errorf("Request-URI = %q, want %q", got.RequestURI, want)
			}
			if got.Get("Route") != "<sip:ibcf."+domain+":5060;lr>" {
				t.Errorf("Route = %q, want the IBCF alone", got.Get("Route"))
			}
			if got.Get("Record-Route") != "<sip:scscf."+domain+":5060;lr>" {
				t.Errorf("Record-Route = %q", got.Get("Record-Route"))
			}
		})
	}
}

// TestHomeNetworkCallsStayInside: with an IBCF, a call to a number of
// the home network and one to a node of the home domain still go there
// without the IBCF.
func TestHomeNetworkCallsStayInside(t *testing.T) {
	h, icscf, _, _ := interconnectHarness(t, "sip:ibcf."+domain+":5060")
	h.invite("tel:+12025550102", orig, impu)
	if got := received(t, icscf); got.RequestURI != "sip:001010000000002@"+domain || got.Get("Route") != "" {
		t.Errorf("Request-URI %q and Route %q, want the ENUM result and no Route", got.RequestURI, got.Get("Route"))
	}
}

// TestWithoutAnIBCFTheCallGoesToThePeerItself: an S-CSCF without an
// IBCF routes to the host of the Request-URI.
func TestWithoutAnIBCFTheCallGoesToThePeerItself(t *testing.T) {
	h, _, _, peer := interconnectHarness(t, "")
	h.invite("tel:+12025550151", orig, impu)
	if got := received(t, peer); got.Get("Route") != "" {
		t.Errorf("Route = %q, want none", got.Get("Route"))
	}
}

func TestTerminatingCallFollowsThePath(t *testing.T) {
	h, _, pcscf := registeredHarness(t)
	if r := h.invite(impu, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 486 {
		t.Fatalf("got %d, want the 486 of the P-CSCF", r.StatusCode)
	}
	got := received(t, pcscf)
	if got.RequestURI != "sip:001010000000001@192.0.2.10:5060" {
		t.Errorf("Request-URI = %q, want the registered contact", got.RequestURI)
	}
	if got.Get("Route") != "<sip:term@pcscf."+domain+":5060;lr>" {
		t.Errorf("Route = %q, want the Path", got.Get("Route"))
	}
	if got.Get("P-Called-Party-ID") != "<"+impu+">" {
		t.Errorf("P-Called-Party-ID = %q", got.Get("P-Called-Party-ID"))
	}
}

// TestTerminatingCallKeepsAWithheldIdentityFromTheUE is TS 24.229
// 5.4.3.3 step 12: the caller asked for "id" privacy (OIR), so its
// P-Asserted-Identity does not leave the trust domain toward the UE,
// while Privacy tells the UE that the identity was withheld.
func TestTerminatingCallKeepsAWithheldIdentityFromTheUE(t *testing.T) {
	for privacy, keep := range map[string]bool{"": true, "none": true, "id": false, "header;id": false} {
		t.Run(privacy, func(t *testing.T) {
			h, _, pcscf := registeredHarness(t)
			m := newRequest(sip.INVITE, impu, "<sip:scscf."+domain+":5060;lr>", "<sip:caller@visited.example>")
			m.Add("Contact", "<sip:caller@192.0.2.20:5060>")
			if privacy != "" {
				m.Add("Privacy", privacy)
			}
			h.send(m)
			got := received(t, pcscf)
			if (got.Get("P-Asserted-Identity") != "") != keep || got.Get("Privacy") != privacy {
				t.Errorf("P-CSCF got P-Asserted-Identity %q and Privacy %q; want it kept %v and Privacy %q",
					got.Get("P-Asserted-Identity"), got.Get("Privacy"), keep, privacy)
			}
		})
	}
}

// TestOriginatingRequestAssertsTheAlias is TS 24.229 5.4.3.2 step 9: the
// user has a SIP URI and a tel URI, and whichever of the two the P-CSCF
// asserted, the other goes in as a second P-Asserted-Identity.
func TestOriginatingRequestAssertsTheAlias(t *testing.T) {
	for _, tt := range []struct {
		asserted string
		want     []string
	}{
		{impu, []string{"<" + impu + ">", "<" + telURI + ">"}},
		{telURI, []string{"<" + telURI + ">", "<sip:+12025550101@" + domain + ";user=phone>"}},
	} {
		h := newServiceHarness(t, nil, nil)
		h.registerUser(sip.NewCallID())
		h.invite("sip:001010000000002@"+domain, orig, tt.asserted)
		got := received(t, h.icscf).GetAll("P-Asserted-Identity")
		if !slices.Equal(got, tt.want) {
			t.Errorf("asserted %s: P-Asserted-Identity = %q, want %q", tt.asserted, got, tt.want)
		}
	}
}

// TestMessageIsNotRecordRouted is TS 24.229 5.4.3.2 step 13 and
// 5.4.3.3 step 13: only an initial request for a dialog is
// record-routed, and a MESSAGE creates none (RFC 3428 4).
func TestMessageIsNotRecordRouted(t *testing.T) {
	h, icscf, pcscf := registeredHarness(t)
	if r := h.send(sms("sip:sc@"+domain, orig, impu)); r.StatusCode != 486 {
		t.Fatalf("originating MESSAGE got %d, want the 486 of the next hop", r.StatusCode)
	}
	if rr := received(t, icscf).Get("Record-Route"); rr != "" {
		t.Errorf("originating MESSAGE has Record-Route %q", rr)
	}
	if r := h.send(sms(impu, "<sip:scscf."+domain+":5060;lr>", "")); r.StatusCode != 486 {
		t.Fatalf("terminating MESSAGE got %d, want the 486 of the next hop", r.StatusCode)
	}
	if rr := received(t, pcscf).Get("Record-Route"); rr != "" {
		t.Errorf("terminating MESSAGE has Record-Route %q", rr)
	}
}

// TS 24.229 5.4.3.3 steps 9 and 10: an SMS from the IP-SM-GW requires a
// contact that registered +g.3gpp.smsip (TS 24.341 5.3.3.4.2), and gets
// 480 when the user's contact did not (RFC 3841 7.2.4).
func TestTerminatingSMSMeetsTheCallerPreferences(t *testing.T) {
	for _, tt := range []struct {
		contactParams string
		want          int
	}{
		{";+g.3gpp.smsip", 486}, // the P-CSCF stub answers 486
		{";" + mmtelICSI, 480},
	} {
		h, _, pcscf := registeredHarnessWith(t, tt.contactParams)
		m := sms(impu, "<sip:scscf."+domain+":5060;lr>", "sip:ipsmgw."+domain+":5060")
		m.Add("Accept-Contact", smsAcceptContact)
		m.Add("Request-Disposition", "no-fork")
		if r := h.send(m); r.StatusCode != tt.want {
			t.Errorf("contact with %q: got %d, want %d", tt.contactParams, r.StatusCode, tt.want)
		}
		select {
		case got := <-pcscf:
			if tt.want == 480 {
				t.Errorf("contact with %q: the P-CSCF got the MESSAGE", tt.contactParams)
			} else if got.Get("Accept-Contact") != smsAcceptContact {
				t.Errorf("Accept-Contact = %q, want it passed on", got.Get("Accept-Contact"))
			}
		default:
			if tt.want != 480 {
				t.Errorf("contact with %q: the P-CSCF got no MESSAGE", tt.contactParams)
			}
		}
	}
}

// TestServiceUnavailableIsNotPassedOn: the P-CSCF on the Path of the
// callee answers a call 503 with Retry-After, and has no other target.
// The S-CSCF answers 500 without Retry-After instead (RFC 3261 16.7
// step 6): the 503 tells that the P-CSCF takes no request, not that the
// S-CSCF takes none.
func TestServiceUnavailableIsNotPassedOn(t *testing.T) {
	pcscf := newStack(t, func(st *sip.ServerTransaction) {
		resp := sip.NewResponse(st.Request, 503, sip.NewTag())
		resp.Add("Retry-After", "30")
		st.Respond(resp)
	})
	h := newHarnessWith(t, Config{}, fakeResolver{hosts: map[string]netip.AddrPort{"pcscf." + domain: pcscf.LocalAddr()}})
	callID := sip.NewCallID()
	auth, _ := answer(t, h.register(callID, 1, 600000, initial), password, "ip-assoc-pending")
	if r := h.register(callID, 2, 600000, auth); r.StatusCode != 200 {
		t.Fatalf("registration got %d", r.StatusCode)
	}
	if r := h.invite(impu, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 500 || r.Get("Retry-After") != "" {
		t.Errorf("got %d with Retry-After %q, want 500 without one", r.StatusCode, r.Get("Retry-After"))
	}
}

// TestTerminatingCallToAnUnknownUserGets404: the HSS answers the SAR
// for a user it does not know with DIAMETER_ERROR_USER_UNKNOWN.
func TestTerminatingCallToAnUnknownUserGets404(t *testing.T) {
	h, _, _ := registeredHarness(t)
	if r := h.invite("sip:001010000000002@"+domain, "<sip:scscf."+domain+":5060;lr>", ""); r.StatusCode != 404 {
		t.Fatalf("got %d, want 404", r.StatusCode)
	}
}

// TestACKIsForwardedWithMaxForwardsLessOne is RFC 3261 16.6 step 3,
// which holds for an ACK as for any other request: an ACK whose
// Max-Forwards has reached 0 goes no further.
func TestACKIsForwardedWithMaxForwardsLessOne(t *testing.T) {
	acks := make(chan *sip.Message, 2)
	tr, err := sip.Listen("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	next, err := sip.NewStack(tr, sip.Config{SentBy: tr.LocalAddr().String(),
		OnRequest: func(*sip.ServerTransaction) {}, OnACK: func(m *sip.Message) { acks <- m }}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(next.Close)
	h := newHarnessWith(t, Config{}, fakeResolver{hosts: map[string]netip.AddrPort{"pcscf." + domain: next.LocalAddr()}})

	for _, maxForwards := range []string{"0", "70"} {
		ack := &sip.Message{Method: sip.ACK, RequestURI: "sip:001010000000001@192.0.2.10:5060"}
		ack.Add("Route", "<sip:term@pcscf."+domain+":5060;lr>")
		ack.Add("Max-Forwards", maxForwards)
		ack.Add("From", "<sip:001010000000002@"+domain+">;tag=caller")
		ack.Add("To", "<"+impu+">;tag=callee")
		ack.Add("Call-ID", sip.NewCallID())
		ack.Add("CSeq", "1 ACK")
		h.role.OnACK(ack)
	}
	select {
	case got := <-acks:
		if got.Get("Max-Forwards") != "69" {
			t.Errorf("forwarded an ACK with Max-Forwards %q, want only the one with 69", got.Get("Max-Forwards"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the ACK with Max-Forwards 70 was not forwarded")
	}
	select {
	case got := <-acks:
		t.Errorf("forwarded a second ACK, with Max-Forwards %q", got.Get("Max-Forwards"))
	case <-time.After(200 * time.Millisecond):
	}
}
