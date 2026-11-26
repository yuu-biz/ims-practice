package pcscf

import (
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The tests of this file give the P-CSCF, as an IMS-ALG, bodies whose
// SDP it cannot point at the relay as they are: one without
// Content-Type, which RFC 3261 20.15 forbids, so that the P-CSCF cannot
// tell whether it is SDP, and one whose SDP is a part of a multipart
// body (RFC 5621). Requests and responses get the same treatment: a
// request goes no further, and a response ends the call as one whose
// answer the relay did not take.

// untyped is an SDP body without a media type.
func untyped(s sdp.Session) sip.Body { return sip.Body{Content: s.Bytes()} }

// inMultipart is s as the first part of a multipart/mixed body, with a
// second part of another type, as an emergency call carries its location
// (RFC 5621 4).
func inMultipart(s sdp.Session) sip.Body {
	content := "--part\r\nContent-Type: application/sdp\r\n\r\n" + string(s.Bytes()) +
		"\r\n--part\r\nContent-Type: application/pidf+xml\r\n\r\n<presence/>\r\n--part--\r\n"
	return sip.Body{Type: "multipart/mixed;boundary=part", Content: []byte(content)}
}

// TestINVITEWhoseSDPTheP_CSCFCannotReadGoesNoFurther: an INVITE whose
// body has no Content-Type gets 400 (RFC 3261 21.4.1), as one whose SDP
// does not parse. One whose SDP is in a multipart body gets 415 with the
// type that the P-CSCF reads (RFC 3261 21.4.13, RFC 5621 8.4). Neither
// reaches the callee, and no relay context stays.
func TestINVITEWhoseSDPTheP_CSCFCannotReadGoesNoFurther(t *testing.T) {
	for _, tc := range []struct {
		name string
		body sip.Body
		want int
	}{
		{"untyped", untyped(sdp.Offer("1", ueMedia)), 400},
		{"malformed", sip.Body{Type: "application/sdp", Content: []byte("not sdp")}, 400},
		{"multipart", inMultipart(sdp.Offer("1", ueMedia)), 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, relay := newPolicyHarness(t)
			out, err := h.ue.Invite(sip.Invitation{RequestURI: "sip:001010000000002@" + domain, From: impu,
				Route: []string{"<sip:pcscf." + domain + ":5060;lr>"}, Body: tc.body})
			if err != nil {
				t.Fatal(err)
			}
			resp := answered(t, out, tc.want)
			if tc.want == 415 && resp.Get("Accept") != "application/sdp" {
				t.Errorf("Accept of the 415 = %q, want application/sdp", resp.Get("Accept"))
			}
			select {
			case <-h.toCore:
				t.Error("the callee got the INVITE")
			case <-time.After(watch):
			}
			relay.waitHeld(t, 0, time.Second)
		})
	}
}

// TestUPDATEWhoseSDPTheP_CSCFCannotReadGoesNoFurther: the same within
// a call that is up. The relay is not asked, so the call stays.
func TestUPDATEWhoseSDPTheP_CSCFCannotReadGoesNoFurther(t *testing.T) {
	for _, tc := range []struct {
		name string
		body sip.Body
		want int
	}{
		{"untyped", untyped(sdp.Offer("2", moved)), 400},
		{"multipart", inMultipart(sdp.Offer("2", moved)), 415},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, pcrf, relay := newPolicyHarness(t)
			c := newUpCall(t, h)
			resp, err := c.out.Update(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tc.want {
				t.Fatalf("the UPDATE got %d, want %d from the P-CSCF", resp.StatusCode, tc.want)
			}
			wantNotForwarded(t, "the far end", h.coreGot, resp)
			wantForwarded(t, "the far end", h.coreGot, reoffer(t, c.out, "UPDATE", moved))
			wantCallUp(t, h, pcrf, relay, c.key)
			hangUp(t, h, c.out, pcrf, relay, c.key)
		})
	}
}

// TestResponseWhoseSDPTheP_CSCFCannotReadEndsTheCall: a 183 whose answer
// has no Content-Type, or is a part of a multipart body, goes no
// further, and the call ends as for a 183 whose answer the relay did not
// take: the caller gets 500, and the callee a CANCEL.
func TestResponseWhoseSDPTheP_CSCFCannotReadEndsTheCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		body sip.Body
	}{
		{"untyped", untyped(sdp.Offer("2", coreMedia))},
		{"multipart", inMultipart(sdp.Offer("2", coreMedia))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newRawINVITE(t)
			progress := c.response(183)
			progress.Remove("Content-Type")
			if tc.body.Type != "" {
				progress.Add("Content-Type", tc.body.Type)
			}
			progress.Body = tc.body.Content
			c.st.Respond(progress)
			if r := final(t, c.ct.Responses(), 3*time.Second); r.StatusCode != 500 {
				t.Fatalf("the caller got %d, want 500", r.StatusCode)
			}
			select {
			case <-c.st.Cancelled():
			case <-time.After(3 * time.Second):
				t.Fatal("the callee got no CANCEL")
			}
			// RFC 3261 9.2: the callee ends the cancelled INVITE.
			c.st.Respond(sip.NewResponse(c.st.Request, 487, "callee"))
			c.relay.waitHeld(t, 0, time.Second)
		})
	}
}
