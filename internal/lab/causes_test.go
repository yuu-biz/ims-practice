package lab

import (
	"encoding/base64"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
)

func userNameAVP(v string) string {
	return `{"name":"User-Name","code":1,"vendor":0,"value":"` + v + `"}`
}

func publicIdentityAVP(v string) string {
	return `{"name":"Public-Identity","code":601,"vendor":10415,"value":"` + v + `"}`
}

func cxJSON(code int, request bool, sessionID string, avps ...string) string {
	avps = append([]string{`{"name":"Session-Id","code":263,"vendor":0,"value":"` + sessionID + `"}`}, avps...)
	return `{"command":"X","code":` + strconv.Itoa(code) + `,"request":` + strconv.FormatBool(request) +
		`,"application_id":16777216,"avps":[` + strings.Join(avps, ",") + `]}`
}

// cx is a Cx request that n sends to the HSS and its answer, each
// captured by both ends.
func (h *harness) cx(n node, code int, sessionID string, avps ...string) {
	request := cxJSON(code, true, sessionID, avps...)
	answer := cxJSON(code, false, sessionID, `{"name":"Result-Code","code":268,"vendor":0,"value":2001}`)
	h.capture(n, n, hss, hep.ProtocolTypeDiameterJSON, sessionID, request)
	h.capture(hss, n, hss, hep.ProtocolTypeDiameterJSON, sessionID, request)
	h.capture(hss, hss, n, hep.ProtocolTypeDiameterJSON, sessionID, answer)
	h.capture(n, hss, n, hep.ProtocolTypeDiameterJSON, sessionID, answer)
}

const (
	uarCode = 300
	sarCode = 301
	lirCode = 302
	marCode = 303
)

func digest(response string) string {
	return `Digest username="` + impi + `", realm="` + domain + `", nonce="abc", uri="sip:` + domain + `", response="` + response + `"`
}

// userRequest is a request other than REGISTER from user1, as the P-CSCF or
// the S-CSCF forwards it.
func userRequest(method, requestURI, callID string) string {
	return sipText(method+" "+requestURI+" SIP/2.0", "",
		"Via: SIP/2.0/UDP 172.28.0.40:5060;branch=z9hG4bK"+callID,
		"Max-Forwards: 69",
		"From: <"+impu+">;tag=f1",
		"To: <"+requestURI+">",
		"Call-ID: "+callID,
		"CSeq: 1 "+method)
}

// The Call-ID a Cx message gets from the SIP request that caused it
// (TS 24.229 5.3.1.2, 5.4.1.2.1, 5.4.1.2.2A and 5.3.2.1).
func TestCxMessagesGetTheCallIDOfTheirCause(t *testing.T) {
	const (
		subscribeID = "sub-1@172.28.0.40"
		inviteID    = "call-1@172.28.0.100"
		user2       = "sip:001010000000002@" + domain
	)
	tests := []struct {
		name string
		run  func(h *harness)
		want map[string]string // by Session-Id
	}{{
		name: "registration: UAR, MAR, then UAR and SAR",
		run: func(h *harness) {
			h.along(toRegistrar[:3], register(1, digest(""), 600000))
			h.cx(icscf, uarCode, "icscf;1", userNameAVP(impi), publicIdentityAVP(impu))
			h.hop(icscf, scscf, register(1, digest(""), 600000))
			h.cx(scscf, marCode, "scscf;1", userNameAVP(impi), publicIdentityAVP(impu))
			h.along(toUE, registerResponse(1, 401, "Unauthorized"))
			h.along(toRegistrar[:3], register(2, digest("0123"), 600000))
			h.cx(icscf, uarCode, "icscf;2", userNameAVP(impi), publicIdentityAVP(impu))
			h.hop(icscf, scscf, register(2, digest("0123"), 600000))
			h.cx(scscf, sarCode, "scscf;2", userNameAVP(impi), publicIdentityAVP(impu))
			h.along(toUE, registerResponse(2, 200, "OK", "Contact: <"+ueContact+">;expires=600"))
		},
		want: map[string]string{"icscf;1": regCallID, "scscf;1": regCallID, "icscf;2": regCallID, "scscf;2": regCallID},
	}, {
		name: "a REGISTER without Authorization, by its To",
		run: func(h *harness) {
			h.hop(pcscf, icscf, strings.Replace(register(1, "", 600000), "Authorization: \r\n", "", 1))
			h.cx(icscf, uarCode, "icscf;1", userNameAVP("001010000000001@"+domain), publicIdentityAVP(impu))
		},
		want: map[string]string{"icscf;1": regCallID},
	}, {
		name: "the SAR of a deregistration, sent after the 200",
		run: func(h *harness) {
			h.hop(icscf, scscf, register(3, digest("4567"), 0))
			h.hop(scscf, icscf, registerResponse(3, 200, "OK", "Contact: <"+ueContact+">;expires=0"))
			h.cx(scscf, sarCode, "scscf;3", userNameAVP(impi), publicIdentityAVP(impu))
		},
		want: map[string]string{"scscf;3": regCallID},
	}, {
		// The reported case: the P-CSCF subscribes to the reg event of
		// the caller while the INVITE to the callee passes the I-CSCF.
		name: "the LIRs of a reg event SUBSCRIBE and of an INVITE, for different users",
		run: func(h *harness) {
			h.hop(pcscf, icscf, userRequest("SUBSCRIBE", impu, subscribeID))
			h.hop(scscf, icscf, userRequest("INVITE", callee, inviteID))
			h.cx(icscf, lirCode, "icscf;sub", publicIdentityAVP(impu))
			h.cx(icscf, lirCode, "icscf;inv", publicIdentityAVP(callee))
		},
		want: map[string]string{"icscf;sub": subscribeID, "icscf;inv": inviteID},
	}, {
		name: "the LIRs of a reg event SUBSCRIBE and of an INVITE, for the same user",
		run: func(h *harness) {
			h.hop(pcscf, icscf, userRequest("SUBSCRIBE", user2, subscribeID))
			h.cx(icscf, lirCode, "icscf;sub", publicIdentityAVP(user2))
			h.hop(scscf, icscf, userRequest("INVITE", user2, inviteID))
			h.cx(icscf, lirCode, "icscf;inv", publicIdentityAVP(user2))
		},
		want: map[string]string{"icscf;sub": subscribeID, "icscf;inv": inviteID},
	}, {
		name: "a tel URI with visual separators, as the HSS stores it",
		run: func(h *harness) {
			h.hop(scscf, icscf, userRequest("INVITE", "tel:+1-202-555-0102", inviteID))
			h.cx(icscf, lirCode, "icscf;inv", publicIdentityAVP(callee))
		},
		want: map[string]string{"icscf;inv": inviteID},
	}, {
		name: "no cause",
		run: func(h *harness) {
			// Nothing named this user.
			h.cx(icscf, lirCode, "icscf;nobody", publicIdentityAVP(user2))
			// Another node received the request.
			h.hop(pcscf, scscf, userRequest("INVITE", callee, inviteID))
			h.cx(icscf, lirCode, "icscf;elsewhere", publicIdentityAVP(callee))
			// A timer, long after the REGISTER.
			h.hop(icscf, scscf, register(1, digest(""), 600000))
			h.clock = h.clock.Add(causeWindow)
			h.cx(scscf, sarCode, "scscf;timer", userNameAVP(impi), publicIdentityAVP(impu))
		},
		want: map[string]string{"icscf;nobody": "", "icscf;elsewhere": "", "scscf;timer": ""},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, Config{})
			tt.run(h)
			var all []Message
			h.get("/api/messages", &all)
			got := map[string]string{}
			copies := map[string]int{}
			for _, m := range all {
				if m.Protocol != ProtocolDiameter {
					continue
				}
				copies[m.CorrelationID]++
				if seen, ok := got[m.CorrelationID]; ok && seen != m.CallID {
					t.Errorf("copies of %s have call_id %q and %q", m.CorrelationID, seen, m.CallID)
				}
				got[m.CorrelationID] = m.CallID
			}
			for session, want := range tt.want {
				if copies[session] != 4 || got[session] != want {
					t.Errorf("%s: %d copies with call_id %q, want 4 with %q", session, copies[session], got[session], want)
				}
				if want == "" {
					continue
				}
				var linked []Message
				h.get("/api/messages?call_id="+url.QueryEscape(want), &linked)
				n := 0
				for _, m := range linked {
					if m.CorrelationID == session {
						n++
					}
				}
				if n != 4 {
					t.Errorf("call_id=%s returns %d copies of %s, want 4", want, n, session)
				}
			}
		})
	}
}

// The copies of different nodes come in no fixed order. The HSS's copies
// that arrive before the I-CSCF's get the link once the I-CSCF's copy of
// the request shows it, and the list is in the order of the capture
// times.
func TestCxLinkDoesNotDependOnArrivalOrder(t *testing.T) {
	h := newHarness(t, Config{})
	const sessionID = "icscf;1"
	uarJSON := cxJSON(uarCode, true, sessionID, userNameAVP(impi), publicIdentityAVP(impu))
	uaaJSON := cxJSON(uarCode, false, sessionID)
	at := func(ms int, captor, src, dst node, proto hep.ProtocolType, correlationID, payload string) {
		h.clock = start.Add(time.Duration(ms)*time.Millisecond - time.Millisecond)
		h.capture(captor, src, dst, proto, correlationID, payload)
	}
	reg := register(1, digest(""), 600000)
	at(3, hss, icscf, hss, hep.ProtocolTypeDiameterJSON, sessionID, uarJSON)
	at(4, hss, hss, icscf, hep.ProtocolTypeDiameterJSON, sessionID, uaaJSON)
	at(1, icscf, pcscf, icscf, hep.ProtocolTypeSIP, regCallID, reg)
	at(2, icscf, icscf, hss, hep.ProtocolTypeDiameterJSON, sessionID, uarJSON)
	at(5, icscf, hss, icscf, hep.ProtocolTypeDiameterJSON, sessionID, uaaJSON)

	var linked []Message
	h.get("/api/messages?call_id="+regCallID, &linked)
	var order []string
	for _, m := range linked {
		order = append(order, m.Node+" "+m.Summary)
	}
	want := []string{"icscf REGISTER sip:" + domain, "icscf UAR", "hss UAR", "hss UAA", "icscf UAA"}
	if strings.Join(order, ", ") != strings.Join(want, ", ") {
		t.Errorf("call_id=%s gives %q, want %q", regCallID, order, want)
	}
}

// The causes and the sessions go with the messages that brought them,
// and the calls count only SIP messages.
func TestCausesAreBoundedWithTheLog(t *testing.T) {
	h := newHarness(t, Config{MaxMessages: 6})
	const inviteID = "call-1@172.28.0.100"
	h.hop(scscf, icscf, userRequest("INVITE", callee, inviteID))
	h.cx(icscf, lirCode, "icscf;1", publicIdentityAVP(callee))
	if c := h.call(inviteID); c.Messages != 2 {
		t.Errorf("call counts %d messages, want its 2 SIP copies", c.Messages)
	}
	// Six unrelated messages push the INVITE and the LIR out.
	for i := range 3 {
		h.hop(ue1, pcscf, userRequest("OPTIONS", impu, "opt-"+strconv.Itoa(i)))
	}
	h.l.store.mu.Lock()
	causes, sessions := len(h.l.store.causes), len(h.l.store.sessions)
	h.l.store.mu.Unlock()
	if causes != 1 || sessions != 0 {
		t.Errorf("%d identities and %d sessions kept, want only the OPTIONS' Request-URI and no session", causes, sessions)
	}
}

var pcrf = node{"epc", netip.MustParseAddrPort("172.28.0.80:3868")}

func framedIPAVP(a string) string {
	b := netip.MustParseAddr(a).As4()
	return `{"name":"Framed-IP-Address","code":8,"vendor":0,"value":"` + base64.StdEncoding.EncodeToString(b[:]) + `"}`
}

// rx is an Rx request that the P-CSCF sends to the PCRF and its answer,
// each captured by both ends.
func (h *harness) rx(code int, sessionID string, avps ...string) {
	request := cxJSON(code, true, sessionID, avps...)
	answer := cxJSON(code, false, sessionID, `{"name":"Result-Code","code":268,"vendor":0,"value":2001}`)
	h.capture(pcscf, pcscf, pcrf, hep.ProtocolTypeDiameterJSON, sessionID, request)
	h.capture(pcrf, pcscf, pcrf, hep.ProtocolTypeDiameterJSON, sessionID, request)
	h.capture(pcrf, pcrf, pcscf, hep.ProtocolTypeDiameterJSON, sessionID, answer)
	h.capture(pcscf, pcrf, pcscf, hep.ProtocolTypeDiameterJSON, sessionID, answer)
}

// withSDP is a SIP message of the call with an SDP body.
func withSDP(startLine, callID string) string {
	return sipText(startLine, "v=0\r\n", "Via: SIP/2.0/UDP 172.28.0.100:5060;branch=z9hG4bK"+callID,
		"From: <"+impu+">;tag=f1", "To: <"+callee+">", "Call-ID: "+callID, "CSeq: 1 INVITE",
		"Content-Type: application/sdp")
}

// The Rx sessions of a UE: the one of the signalling path goes with the
// REGISTER, the one of a call's media with the call, whose STR then
// shares it (TS 29.214 A.1, A.4).
func TestRxSessionsGetTheCallIDOfTheirCause(t *testing.T) {
	const (
		aarCode  = 265
		strCode  = 275
		callerID = "call-1@172.28.0.100"
		calleeID = "call-3@172.28.0.40"
		notifyID = "sub-1@172.28.0.40"
	)
	h := newHarness(t, Config{})
	h.hop(ue1, pcscf, register(2, digest("0123"), 600000))
	h.hop(pcscf, ue1, registerResponse(2, 200, "OK", "Contact: <"+ueContact+">;expires=600"))
	h.rx(aarCode, "pcscf;signalling", framedIPAVP("172.28.0.100"))
	// The calling UE's INVITE, then the 200 of the UE to a NOTIFY, which
	// has no SDP, before the 183 that makes the P-CSCF ask the PCRF.
	h.hop(ue1, pcscf, withSDP("INVITE "+callee+" SIP/2.0", callerID))
	h.hop(ue1, pcscf, sipText("SIP/2.0 200 OK", "", "Call-ID: "+notifyID, "CSeq: 1 NOTIFY"))
	h.hop(ue2, pcscf, withSDP("SIP/2.0 183 Session Progress", calleeID))
	h.rx(aarCode, "pcscf;callee", framedIPAVP("172.28.0.101"))
	h.hop(scscf, pcscf, withSDP("SIP/2.0 183 Session Progress", callerID))
	h.rx(aarCode, "pcscf;caller", framedIPAVP("172.28.0.100"))
	h.hop(ue1, pcscf, userRequest("BYE", callee, callerID))
	h.rx(strCode, "pcscf;caller")

	var all []Message
	h.get("/api/messages", &all)
	got := map[string][]string{}
	for _, m := range all {
		if m.Protocol == ProtocolDiameter {
			got[m.CorrelationID] = append(got[m.CorrelationID], m.CallID)
		}
	}
	want := map[string]string{"pcscf;signalling": regCallID, "pcscf;callee": calleeID, "pcscf;caller": callerID}
	for session, id := range want {
		copies := map[string]int{"pcscf;caller": 8}[session]
		if copies == 0 {
			copies = 4
		}
		if len(got[session]) != copies {
			t.Errorf("%s: %d copies, want %d", session, len(got[session]), copies)
		}
		for _, c := range got[session] {
			if c != id {
				t.Errorf("%s: a copy with call_id %q, want %q", session, c, id)
			}
		}
	}
}

// TestRxSessionOfAUEOnIPv6: a UE that registers over IPv6 is named in
// the AAR by Framed-IPv6-Prefix, the full address as a prefix of 128
// bits (TS 29.214 4.4.1), and its signalling path goes with its
// REGISTER as for a UE on IPv4.
func TestRxSessionOfAUEOnIPv6(t *testing.T) {
	ue := node{"ue", netip.MustParseAddrPort("[2001:db8::100]:5060")}
	pcscfV6 := node{"pcscf", netip.MustParseAddrPort("[2001:db8::40]:5060")}
	h := newHarness(t, Config{NodeAddresses: map[string][]netip.Addr{"pcscf": {pcscfV6.addr.Addr()}}})
	text := register(2, digest("0123"), 600000)
	h.capture(ue, ue, pcscfV6, hep.ProtocolTypeSIP, regCallID, text)
	h.capture(pcscf, ue, pcscfV6, hep.ProtocolTypeSIP, regCallID, text)
	prefix := append([]byte{0, 128}, ue.addr.Addr().AsSlice()...)
	h.rx(265, "pcscf;signalling", `{"name":"Framed-IPv6-Prefix","code":97,"vendor":0,"value":"`+base64.StdEncoding.EncodeToString(prefix)+`"}`)

	var all []Message
	h.get("/api/messages", &all)
	for _, m := range all {
		if m.Protocol == ProtocolDiameter && m.CallID != regCallID {
			t.Errorf("%s copy %d has call_id %q, want the REGISTER's %q", m.Summary, m.ID, m.CallID, regCallID)
		}
	}
}
