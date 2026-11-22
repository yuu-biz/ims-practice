package lab

import (
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

const uarSessionID = "icscf." + domain + ";1;2"

func uar() string {
	return `{"command":"User-Authorization","code":300,"request":true,"application_id":16777216,"hop_by_hop":1,"end_to_end":2,
		"avps":[{"name":"Session-Id","code":263,"vendor":0,"value":"` + uarSessionID + `"},
		{"name":"User-Name","code":1,"vendor":0,"value":"` + impi + `"}]}`
}

func uaa() string {
	return `{"command":"User-Authorization","code":300,"request":false,"application_id":16777216,"hop_by_hop":1,"end_to_end":2,
		"avps":[{"name":"Session-Id","code":263,"vendor":0,"value":"` + uarSessionID + `"},
		{"name":"Experimental-Result","code":297,"vendor":0,"avps":[
			{"name":"Vendor-Id","code":266,"vendor":0,"value":10415},
			{"name":"Experimental-Result-Code","code":298,"vendor":0,"value":2001}]}]}`
}

func TestMessages(t *testing.T) {
	h := newHarness(t, Config{})
	const id = "call-msg@172.28.0.100"
	h.hop(ue1, pcscf, invite(id))
	h.capture(icscf, icscf, hss, hep.ProtocolTypeDiameterJSON, uarSessionID, uar())
	h.capture(hss, icscf, hss, hep.ProtocolTypeDiameterJSON, uarSessionID, uaa())
	h.hop(pcscf, ue1, response(id, 200, "OK", 1, "INVITE", "callee2"))

	var all []Message
	h.get("/api/messages", &all)
	if len(all) != 6 {
		t.Fatalf("%d messages, want 6", len(all))
	}
	for i, m := range all {
		if m.ID != uint64(i+1) || !m.Time.Equal(start.Add(time.Duration(i+1)*time.Millisecond)) {
			t.Errorf("message %d has id %d and time %v, want capture order", i, m.ID, m.Time)
		}
	}
	sent, received := all[0], all[1]
	want := Message{
		ID: 1, Time: sent.Time, Protocol: ProtocolSIP, Transport: "udp",
		Src: "172.28.0.100:5060", Dst: "172.28.0.40:5060", Node: "ue", Direction: DirectionSend,
		Summary: "INVITE " + callee, CorrelationID: id, CallID: id, Payload: invite(id),
		Refs: sent.Refs,
	}
	if !reflect.DeepEqual(sent, want) {
		t.Errorf("sent copy = %+v\nwant %+v", sent, want)
	}
	if len(sent.Refs) == 0 || sent.Refs[len(sent.Refs)-3] != (Ref{"RFC 3261", "13", "Setting up a session with INVITE"}) {
		t.Errorf("INVITE refs = %+v", sent.Refs)
	}
	if received.Node != "pcscf" || received.Direction != DirectionRecv || received.Payload != sent.Payload {
		t.Errorf("received copy = %+v", received)
	}
	summaries := []string{all[2].Summary, all[3].Summary, all[4].Summary}
	if want := []string{"UAR", "UAA 2001", "200 OK (INVITE)"}; !reflect.DeepEqual(summaries, want) {
		t.Errorf("summaries = %q, want %q", summaries, want)
	}
	if all[2].Direction != DirectionSend || all[3].Direction != DirectionRecv || all[3].Payload != uaa() {
		t.Errorf("diameter copies = %+v", all[2:4])
	}
	if refs := all[3].Refs; len(refs) == 0 || refs[0] != (Ref{"TS 29.228", "6.1.1", "User registration status query"}) {
		t.Errorf("UAA refs = %+v", refs)
	}

	var byCall, byCorrelation, none []Message
	h.get("/api/messages?call_id="+id, &byCall)
	h.get("/api/messages?correlation_id="+url.QueryEscape(uarSessionID), &byCorrelation)
	h.get("/api/messages?call_id=nothing", &none)
	if len(byCall) != 4 || len(byCorrelation) != 2 || byCorrelation[0].ID != 3 || none == nil || len(none) != 0 {
		t.Errorf("by call_id %d, by correlation_id %d, unknown %#v", len(byCall), len(byCorrelation), none)
	}

	resp, err := http.Get(h.server.URL + "/api/messages?correlation_id=" + uarSessionID)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unescaped ; in the query: %s, want 400", resp.Status)
	}
}

// A node that listens on 0.0.0.0 may report that as its own address. Its
// copy must still name the address the receiver's copy names.
func TestWildcardAddressBecomesTheNodes(t *testing.T) {
	h := newHarness(t, Config{})
	const id = "call-wildcard@172.28.0.40"
	wildcard := node{"pcscf", netip.MustParseAddrPort("0.0.0.0:5060")}
	h.capture(pcscf, wildcard, scscf, hep.ProtocolTypeSIP, id, invite(id))
	h.capture(scscf, pcscf, scscf, hep.ProtocolTypeSIP, id, invite(id))
	var all []Message
	h.get("/api/messages", &all)
	type ends struct {
		src, dst  string
		direction Direction
	}
	var got []ends
	for _, m := range all {
		got = append(got, ends{m.Src, m.Dst, m.Direction})
	}
	want := []ends{
		{"172.28.0.40:5060", "172.28.0.30:5060", DirectionSend},
		{"172.28.0.40:5060", "172.28.0.30:5060", DirectionRecv},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMessageJSON(t *testing.T) {
	h := newHarness(t, Config{})
	h.capture(hss, icscf, hss, hep.ProtocolTypeDiameterJSON, "", `{"command":"Unknown","code":999,"request":true,"avps":[]}`)
	resp, err := http.Get(h.server.URL + "/api/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var raw []map[string]any
	if err := json.Unmarshal(body, &raw); err != nil || len(raw) != 1 {
		t.Fatalf("body %s: %v", body, err)
	}
	m := raw[0]
	if _, ok := m["call_id"]; ok {
		t.Errorf("a Diameter message has call_id: %s", body)
	}
	if refs, ok := m["refs"].([]any); !ok || len(refs) != 0 {
		t.Errorf("refs = %#v, want []", m["refs"])
	}
	if m["time"] != "2026-11-19T09:00:00.001Z" || m["summary"] != "Unknown Request" {
		t.Errorf("time %v, summary %v", m["time"], m["summary"])
	}
}

func TestDiameterSummary(t *testing.T) {
	tests := []struct {
		payload string
		want    string
	}{
		{uar(), "UAR"},
		{uaa(), "UAA 2001"},
		{`{"command":"Server-Assignment","code":301,"request":false,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":2001}]}`, "SAA 2001"},
		{`{"command":"Multimedia-Auth","code":303,"request":true,"avps":[]}`, "MAR"},
		{`{"command":"Location-Info","code":302,"request":false,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":5001}]}`, "LIA 5001"},
		{`{"command":"Registration-Termination","code":304,"request":true,"avps":[]}`, "RTR"},
		{`{"command":"Registration-Termination","code":304,"request":false,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":2001}]}`, "RTA 2001"},
		{`{"command":"Capabilities-Exchange","code":257,"request":true,"avps":[]}`, "CER"},
		{`{"command":"Device-Watchdog","code":280,"request":false,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":2001}]}`, "DWA 2001"},
		{`{"command":"Disconnect-Peer","code":282,"request":true,"avps":[]}`, "DPR"},
		{`{"command":"Push-Profile","code":305,"request":false,"avps":[]}`, "Push-Profile Answer"},
		{`{"command":"User-Data","code":306,"request":true,"avps":[]}`, "UDR"},
		{`{"command":"Profile-Update","code":307,"request":false,"avps":[{"name":"Experimental-Result","code":297,"vendor":0,` +
			`"avps":[{"name":"Vendor-Id","code":266,"vendor":0,"value":10415},{"name":"Experimental-Result-Code","code":298,"vendor":0,"value":5103}]}]}`,
			"PUA 5103"},
		{`{"command":"User-Authorization","code":300,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":5003}]}`, "UAA 5003"},
		{`{"command":"AA","code":265,"request":true,"avps":[]}`, "AAR"},
		{`{"command":"AA","code":265,"request":false,"avps":[{"name":"Experimental-Result","code":297,"vendor":0,` +
			`"avps":[{"name":"Vendor-Id","code":266,"vendor":0,"value":10415},{"name":"Experimental-Result-Code","code":298,"vendor":0,"value":5065}]}]}`,
			"AAA 5065"},
		{`{"command":"Abort-Session","code":274,"request":true,"avps":[]}`, "ASR"},
		{`{"command":"Session-Termination","code":275,"request":false,"avps":[{"name":"Result-Code","code":268,"vendor":0,"value":2001}]}`, "STA 2001"},
	}
	for _, tt := range tests {
		d, err := parseDiameter([]byte(tt.payload))
		if err != nil {
			t.Errorf("%s: %v", tt.want, err)
			continue
		}
		if got := d.summary(); got != tt.want {
			t.Errorf("summary = %q, want %q", got, tt.want)
		}
	}
}

// Every command the Lab names points the learner at its clauses.
func TestEveryNamedCommandHasRefs(t *testing.T) {
	for code := range commandAbbreviations {
		for _, request := range []bool{true, false} {
			d := diameterMessage{Code: code, Request: request}
			if refs := d.refs(); len(refs) == 0 {
				t.Errorf("%s (%d) has no refs", d.abbreviation(), code)
			}
		}
	}
}

// Every SIP method the nodes send points the learner at its clauses, as
// does the response to it.
func TestEverySentMethodHasRefs(t *testing.T) {
	for _, method := range []sip.Method{sip.REGISTER, sip.INVITE, sip.ACK, sip.CANCEL, sip.BYE, sip.PRACK, sip.SUBSCRIBE, sip.NOTIFY, sip.MESSAGE} {
		req := &sip.Message{Method: method, RequestURI: "sip:" + domain}
		req.Add("CSeq", "1 "+string(method))
		if refs := sipRefs(req); len(refs) == 0 {
			t.Errorf("%s has no refs", method)
		}
		if refs := sipRefs(sip.NewResponse(req, 202, "")); len(refs) == 0 {
			t.Errorf("response to %s has no refs", method)
		}
	}
}

// TestNetannINVITEHasTheRefsOfTheMRF: an INVITE from the AS to the
// MRFC points at RFC 4240 and the MRFC procedures, not at those of a
// call between users.
func TestNetannINVITEHasTheRefsOfTheMRF(t *testing.T) {
	for uri, want := range map[string]Ref{
		"sip:annc@mrfc." + domain + ";play=/provisioned/notice": {"RFC 4240", "3", "Announcement service: sip:annc with play="},
		"sip:conf=room1@mrfc." + domain:                         {"RFC 4240", "5", "Conference service: sip:conf=<id>"},
		"sip:bob@" + domain:                                     {"TS 24.229", "5.1.3.1", "UE: originating a session"},
	} {
		req := &sip.Message{Method: sip.INVITE, RequestURI: uri}
		req.Add("CSeq", "1 INVITE")
		if refs := sipRefs(req); len(refs) == 0 || refs[0] != want {
			t.Errorf("INVITE %s has refs %+v, want %+v first", uri, refs, want)
		}
	}
}

func TestBadPacketsAreSkipped(t *testing.T) {
	h := newHarness(t, Config{})
	h.capture(ue1, ue1, pcscf, hep.ProtocolTypeSIP, "", "not SIP at all")
	h.capture(icscf, icscf, hss, hep.ProtocolTypeDiameterJSON, "", "{not json")
	h.capture(icscf, icscf, hss, hep.ProtocolTypeDiameterJSON, "", `{"avps":[]}`)
	h.capture(ue1, ue1, pcscf, hep.ProtocolType(0x05), "", "RTCP")
	h.hop(ue1, pcscf, invite("after-bad@172.28.0.100"))
	var all []Message
	h.get("/api/messages", &all)
	if len(all) != 2 || all[0].ID != 1 {
		t.Errorf("messages = %+v, want only the INVITE's two copies", all)
	}
}

func TestLogIsBounded(t *testing.T) {
	h := newHarness(t, Config{MaxMessages: 4})
	h.hop(ue1, pcscf, invite("old@172.28.0.100"))
	h.hop(pcscf, ue1, response("old@172.28.0.100", 486, "Busy Here", 1, "INVITE", "x"))
	h.hop(ue1, pcscf, invite("new@172.28.0.100"))
	var all []Message
	h.get("/api/messages", &all)
	if len(all) != 4 || all[0].ID != 3 {
		t.Fatalf("kept %d messages from id %d, want the last 4", len(all), all[0].ID)
	}
	old := h.call("old@172.28.0.100")
	if old.Messages != 2 {
		t.Errorf("old call counts %d messages, want the 2 still kept", old.Messages)
	}
	h.hop(pcscf, ue1, response("new@172.28.0.100", 180, "Ringing", 1, "INVITE", "x"))
	var calls []Call
	h.get("/api/calls", &calls)
	if len(calls) != 1 || calls[0].CallID != "new@172.28.0.100" {
		t.Errorf("calls = %+v, want the old call dropped with its last message", calls)
	}
}
