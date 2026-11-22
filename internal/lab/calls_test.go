package lab

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	caller = "sip:001010000000001@" + domain
	callee = "tel:+12025550102"
)

// A call from UE 1 to UE 2 through the P-CSCF and the S-CSCF, as in
// docs/guide/en/call.md. The I-CSCF is left out: it adds hops, not
// states.
var (
	toCallee = []node{ue1, pcscf, scscf, pcscf, ue2}
	toCaller = []node{ue2, pcscf, scscf, pcscf, ue1}
)

// along sends text hop by hop over path, each hop captured by both ends.
func (h *harness) along(path []node, text string) {
	for i := 0; i+1 < len(path); i++ {
		h.hop(path[i], path[i+1], text)
	}
}

func (h *harness) call(callID string) Call {
	h.t.Helper()
	var calls []Call
	h.get("/api/calls", &calls)
	for _, c := range calls {
		if c.CallID == callID {
			return c
		}
	}
	h.t.Fatalf("no call %s in %+v", callID, calls)
	return Call{}
}

func (h *harness) wantCall(callID string, state CallState, status int) Call {
	h.t.Helper()
	c := h.call(callID)
	got := 0
	if c.Status != nil {
		got = *c.Status
	}
	if c.State != state || got != status {
		h.t.Fatalf("call is %s with status %d, want %s with %d", c.State, got, state, status)
	}
	if ended := c.Ended != nil; ended != (state == CallTerminated || state == CallFailed || state == CallCancelled) {
		h.t.Errorf("call in state %s has ended = %v", state, c.Ended)
	}
	return c
}

func invite(callID string) string {
	return sipText("INVITE "+callee+" SIP/2.0", "v=0\r\n",
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bKinv;rport",
		"Max-Forwards: 70",
		`From: "UE 1" <`+caller+`>;tag=caller1`,
		"To: <"+callee+">",
		"Call-ID: "+callID,
		"CSeq: 1 INVITE",
		"Contact: <sip:001010000000001@172.28.0.100:5060>",
		"Supported: 100rel",
		"Content-Type: application/sdp")
}

// response answers the request with CSeq "seq method"; toTag "" leaves
// To without a tag, as in 100 Trying.
func response(callID string, code int, reason string, seq int, method, toTag string) string {
	to := "To: <" + callee + ">"
	if toTag != "" {
		to += ";tag=" + toTag
	}
	return sipText(fmt.Sprintf("SIP/2.0 %d %s", code, reason), "",
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bKx;rport=5060;received=172.28.0.100",
		`From: "UE 1" <`+caller+`>;tag=caller1`,
		to,
		"Call-ID: "+callID,
		fmt.Sprintf("CSeq: %d %s", seq, method))
}

func request(callID, method string, seq int, toTag string) string {
	to := "To: <" + callee + ">"
	if toTag != "" {
		to += ";tag=" + toTag
	}
	return sipText(method+" sip:001010000000002@172.28.0.101:5060 SIP/2.0", "",
		"Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bK"+method+";rport",
		"Max-Forwards: 70",
		`From: "UE 1" <`+caller+`>;tag=caller1`,
		to,
		"Call-ID: "+callID,
		fmt.Sprintf("CSeq: %d %s", seq, method))
}

func TestSuccessfulCall(t *testing.T) {
	h := newHarness(t, Config{})
	const id = "call-ok@172.28.0.100"
	h.along(toCallee, invite(id))
	h.hop(pcscf, ue1, response(id, 100, "Trying", 1, "INVITE", ""))
	c := h.wantCall(id, CallCalling, 0)
	if c.From != caller || c.To != callee || !c.Started.Equal(start.Add(time.Millisecond)) {
		t.Errorf("call = %+v, want from %s to %s started at the first copy of the INVITE", c, caller, callee)
	}

	h.along(toCaller, response(id, 183, "Session Progress", 1, "INVITE", "callee2"))
	h.wantCall(id, CallEarly, 183)
	h.along(toCallee, request(id, "PRACK", 2, "callee2"))
	h.along(toCaller, response(id, 200, "OK", 2, "PRACK", "callee2"))
	h.wantCall(id, CallEarly, 183)

	h.along(toCaller, response(id, 200, "OK", 1, "INVITE", "callee2"))
	h.wantCall(id, CallConfirmed, 200)
	// A copy of the 183 that reaches the Lab late, and a
	// retransmitted 200, leave the call confirmed.
	h.hop(pcscf, ue1, response(id, 183, "Session Progress", 1, "INVITE", "callee2"))
	h.wantCall(id, CallConfirmed, 200)
	h.hop(pcscf, ue1, response(id, 200, "OK", 1, "INVITE", "callee2"))
	h.along(toCallee, request(id, "ACK", 1, "callee2"))
	h.wantCall(id, CallConfirmed, 200)

	h.along(toCallee, request(id, "BYE", 3, "callee2"))
	ended := h.wantCall(id, CallTerminated, 200).Ended
	h.along(toCaller, response(id, 200, "OK", 3, "BYE", "callee2"))
	c = h.wantCall(id, CallTerminated, 200)
	if !c.Ended.Equal(*ended) {
		t.Errorf("ended moved from %v to %v on the 200 to BYE", ended, c.Ended)
	}
	var messages []Message
	h.get("/api/messages?call_id="+id, &messages)
	if c.Messages != len(messages) || c.Messages != 2*(8*4+3) {
		t.Errorf("messages = %d, /api/messages has %d", c.Messages, len(messages))
	}
}

func TestBusyCallee(t *testing.T) {
	h := newHarness(t, Config{})
	const id = "call-busy@172.28.0.100"
	h.along(toCallee, invite(id))
	h.hop(pcscf, ue1, response(id, 100, "Trying", 1, "INVITE", ""))
	h.along(toCaller, response(id, 486, "Busy Here", 1, "INVITE", "callee2"))
	h.wantCall(id, CallFailed, 486)
	h.hop(ue1, pcscf, request(id, "ACK", 1, "callee2"))
	h.wantCall(id, CallFailed, 486)
}

func TestCancelledCall(t *testing.T) {
	h := newHarness(t, Config{})
	const id = "call-cancel@172.28.0.100"
	h.along(toCallee, invite(id))
	h.along(toCaller, response(id, 180, "Ringing", 1, "INVITE", "callee2"))
	h.wantCall(id, CallEarly, 180)
	h.along(toCallee, request(id, "CANCEL", 1, ""))
	h.along(toCaller, response(id, 200, "OK", 1, "CANCEL", "callee2"))
	h.wantCall(id, CallEarly, 180)
	h.along(toCaller, response(id, 487, "Request Terminated", 1, "INVITE", "callee2"))
	h.wantCall(id, CallCancelled, 487)
}

func TestResponsesBeforeAnInviteMakeNoCall(t *testing.T) {
	h := newHarness(t, Config{})
	h.hop(pcscf, ue1, response("stray@172.28.0.100", 200, "OK", 1, "INVITE", "x"))
	var calls []Call
	h.get("/api/calls", &calls)
	if calls == nil || len(calls) != 0 {
		t.Errorf("calls = %#v, want []", calls)
	}
}

var mmtel = node{"mmtel", netip.MustParseAddrPort("172.28.0.75:5060")}

// leg is one INVITE session of a call through the MMTel AS: an INVITE
// with the icid-value, its 200 and a BYE, over path.
func (h *harness) leg(path []node, callID, icid string) {
	inv := invite(callID)
	if icid != "" {
		inv = strings.Replace(inv, "CSeq: 1 INVITE", "CSeq: 1 INVITE\r\nP-Charging-Vector: icid-value="+icid+";orig-ioi=home", 1)
	}
	h.along(path, inv)
	back := slices.Clone(path)
	slices.Reverse(back)
	h.along(back, response(callID, 200, "OK", 1, "INVITE", "callee2"))
	h.along(path, request(callID, "BYE", 2, "callee2"))
}

// TestCallThroughTheASIsOneCall: the MMTel AS, a routeing B2BUA, starts
// a dialog of its own on each side, with a Call-ID of its own, and
// keeps the icid-value (TS 24.229 5.7.5.1). The Lab lists the
// three sessions as one call, the caller's, and gives the messages of
// all three for its ladder. A session with another icid-value, or with
// none, is a call of its own.
func TestCallThroughTheASIsOneCall(t *testing.T) {
	h := newHarness(t, Config{})
	// The UE's own copy of its INVITE has no P-Charging-Vector; the
	// P-CSCF adds it, so the session joins the call on its next hop.
	h.hop(ue1, pcscf, invite("a"))
	h.leg([]node{pcscf, scscf, mmtel}, "a", "icid-1")
	h.leg([]node{mmtel, scscf, icscf, scscf, mmtel}, "b", "icid-1")
	h.leg([]node{mmtel, scscf, pcscf, ue2}, "c", "icid-1")
	h.leg([]node{pcscf, scscf, mmtel}, "other", "icid-2")
	h.leg([]node{ue1, pcscf, scscf}, "old", "")

	var calls []Call
	h.get("/api/calls", &calls)
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want the call through the AS, the other one and the one without icid-value", calls)
	}
	c := h.wantCall("a", CallTerminated, 200)
	if !slices.Equal(c.Legs, []string{"a", "b", "c"}) || c.From != caller || c.To != callee {
		t.Errorf("call = %+v, want legs a, b, c from %s to %s", c, caller, callee)
	}
	var messages []Message
	h.get("/api/messages?call_id=a&call_id=b&call_id=c", &messages)
	if c.Messages != len(messages) || len(messages) != 2*(1+2*3+4*3+3*3) {
		t.Errorf("the call has %d messages, /api/messages has %d", c.Messages, len(messages))
	}
	if other := h.call("other"); !slices.Equal(other.Legs, []string{"other"}) {
		t.Errorf("the call with another icid-value has legs %v", other.Legs)
	}
	if old := h.call("old"); !slices.Equal(old.Legs, []string{"old"}) {
		t.Errorf("the call without icid-value has legs %v", old.Legs)
	}
}

// TestDroppedLegLeavesTheCall: the Lab forgets a session with the
// last of its messages, and the call goes on without it.
func TestDroppedLegLeavesTheCall(t *testing.T) {
	// Room for the 24 copies of leg b alone.
	h := newHarness(t, Config{MaxMessages: 2 * 4 * 3})
	h.leg([]node{pcscf, scscf, mmtel}, "a", "icid-1")
	h.leg([]node{mmtel, scscf, icscf, scscf, mmtel}, "b", "icid-1")
	var calls []Call
	h.get("/api/calls", &calls)
	if len(calls) != 1 || !slices.Equal(calls[0].Legs, []string{"b"}) {
		t.Errorf("calls = %+v, want only leg b left", calls)
	}
}
