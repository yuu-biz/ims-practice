package mmtel_test

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// oir is the OIR settings of a user whose AS restricts the identity
// alone, which it does with Privacy: id.
func oir(mode, defaultBehaviour string) string {
	return oirOption(mode, defaultBehaviour, "only-identity")
}

// oirOption is the OIR settings with the subscription option by which
// the AS chooses between restricting the identity alone and restricting
// all the private information of the request (TS 24.607 4.5.2.4 item 1).
func oirOption(mode, defaultBehaviour, restriction string) string {
	return `<complete-originating-identity-restriction>
  <originating-identity-presentation-restriction active="true"><default-behaviour>` + defaultBehaviour + `</default-behaviour></originating-identity-presentation-restriction>
  <operator-originating-identity-presentation-restriction authorized="true"><mode>` + mode + `</mode><restriction>` + restriction + `</restriction></operator-originating-identity-presentation-restriction>
</complete-originating-identity-restriction>`
}

func oip(authorized, override string) string {
	return `<complete-originating-identity-presentation>
  <originating-identity-presentation active="true"/>
  <operator-originating-identity-presentation authorized="` + authorized + `">` + override + `</operator-originating-identity-presentation>
</complete-originating-identity-presentation>`
}

// callThrough sends the caller's INVITE for sescase through an AS whose
// HSS has doc for the served user alice, and returns the INVITE that
// the callee got. privacy, when not empty, is the caller's own Privacy.
func callThrough(t *testing.T, sescase, doc, privacy string) *sip.Message {
	t.Helper()
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(doc)}}
	as := startAS(t, hss, mmtel.Config{})
	got := make(chan *sip.Message, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		got <- c.Request
		c.Reject(486)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, sescase, offer)
	inv.FromDisplayName = "Alice"
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Call-Info", Value: "<http://example.com/alice.png>;purpose=icon"})
	if privacy != "" {
		inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Privacy", Value: privacy})
	}
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	readAll(t, out)
	return receive(t, got, "INVITE at the callee")
}

func TestOriginatingIdentityRestriction(t *testing.T) {
	for _, tc := range []struct {
		name, doc, privacy string
		wantPrivacy        string
		wantAnonymous      bool
	}{
		{"no OIR, nothing asked", "", "", "", false},
		{"no OIR, the caller asks", "", "id", "id", false},
		{"permanent", oir("permanent", "presentation-not-restricted"), "", "id", true},
		{"permanent, the caller asks to present", oir("permanent", "presentation-not-restricted"), "none", "id", true},
		{"permanent, all private information", oirOption("permanent", "presentation-not-restricted", "all-private-information"), "", "header", true},
		{"temporary, restricted by default", oir("temporary", "presentation-restricted"), "", "id", true},
		{"temporary, restricted by default, presented this time", oir("temporary", "presentation-restricted"), "none", "none", false},
		{"temporary, presented by default", oir("temporary", "presentation-not-restricted"), "", "", false},
		{"temporary, presented by default, restricted this time", oir("temporary", "presentation-not-restricted"), "id", "id", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := callThrough(t, "orig", tc.doc, tc.privacy)
			from, _ := sip.ParseNameAddr(req.Get("From"))
			anonymous := from.URI == "sip:anonymous@anonymous.invalid" && from.DisplayName == "Anonymous"
			if req.Get("Privacy") != tc.wantPrivacy || anonymous != tc.wantAnonymous {
				t.Errorf("callee got Privacy %q and From %q; want Privacy %q, anonymous %v", req.Get("Privacy"), req.Get("From"), tc.wantPrivacy, tc.wantAnonymous)
			}
			// The identity stays within the trust domain: the S-CSCF
			// takes it out before the callee's UE (TS 24.229 5.4.3.3 step
			// 12).
			if req.Get("P-Asserted-Identity") == "" {
				t.Error("the AS removed P-Asserted-Identity")
			}
		})
	}
}

func TestOriginatingIdentityPresentation(t *testing.T) {
	for _, tc := range []struct {
		name, doc, privacy     string
		wantPAI, wantPrivacy   string
		wantCallInfo, wantAnon bool
	}{
		{"OIP by default", "", "", "<sip:alice@" + domain + ">", "", true, false},
		{"caller withheld its identity", "", "id", "<sip:alice@" + domain + ">", "id", true, false},
		{"caller asks for header privacy", "", "header", "<sip:alice@" + domain + ">", "id", false, false},
		{"caller asks for user privacy", "", "user;id", "<sip:alice@" + domain + ">", "id", true, true},
		{"no OIP", oip("false", ""), "id", "", "", true, false},
		{"override", oip("true", "<restriction-override>override-active</restriction-override>"), "id", "<sip:alice@" + domain + ">", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := callThrough(t, "term", tc.doc, tc.privacy)
			from, _ := sip.ParseNameAddr(req.Get("From"))
			if req.Get("P-Asserted-Identity") != tc.wantPAI || req.Get("Privacy") != tc.wantPrivacy ||
				(req.Get("Call-Info") != "") != tc.wantCallInfo || (from.URI == "sip:anonymous@anonymous.invalid") != tc.wantAnon {
				t.Errorf("callee got\n%s\nwant P-Asserted-Identity %q, Privacy %q, Call-Info %v, anonymous From %v",
					req, tc.wantPAI, tc.wantPrivacy, tc.wantCallInfo, tc.wantAnon)
			}
		})
	}
}

func tip(authorized, override string) string {
	return `<complete-terminating-identity-presentation>
  <terminating-identity-presentation active="true"/>
  <operator-terminating-identity-presentation authorized="` + authorized + `">` + override + `</operator-terminating-identity-presentation>
</complete-terminating-identity-presentation>`
}

func tir(mode, defaultBehaviour string) string {
	return `<complete-terminating-identity-restriction>
  <terminating-identity-presentation-restriction active="true"><default-behaviour>` + defaultBehaviour + `</default-behaviour></terminating-identity-presentation-restriction>
  <operator-terminating-identity-presentation-restriction authorized="true"><mode>` + mode + `</mode></operator-terminating-identity-presentation-restriction>
</complete-terminating-identity-restriction>`
}

// answeredThrough is callThrough for the callee's side: the callee
// answers with the P-Asserted-Identity that its P-CSCF would add (TS
// 24.229 5.2.6.4.4) and with privacy, when not empty, and the caller's
// 200 and the callee's INVITE come back.
func answeredThrough(t *testing.T, sescase, doc, privacy string) (*sip.Message, *sip.Message) {
	t.Helper()
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(doc)}}
	as := startAS(t, hss, mmtel.Config{})
	got := make(chan *sip.Message, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		got <- c.Request
		h := []sip.HeaderField{{Name: "P-Asserted-Identity", Value: "<sip:bob@" + domain + ">"}}
		if privacy != "" {
			h = append(h, sip.HeaderField{Name: "Privacy", Value: privacy})
		}
		c.Accept(answer, h...)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, sescase, offer)
	inv.Headers = append(inv.Headers, sip.HeaderField{Name: "Supported", Value: "from-change"})
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	resps := readAll(t, out)
	out.Bye()
	return resps[len(resps)-1], receive(t, got, "INVITE at the callee")
}

func TestTerminatingIdentityPresentation(t *testing.T) {
	bob := "<sip:bob@" + domain + ">"
	for _, tc := range []struct {
		name, doc, privacy   string
		wantPAI, wantPrivacy string
		wantFromChange       bool
	}{
		{"TIP by default", "", "", bob, "", true},
		{"callee withheld its identity", "", "id", bob, "id", true},
		{"no TIP", tip("false", ""), "id", "", "", false},
		{"override", tip("true", "<restriction-override>override-active</restriction-override>"), "id", bob, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, req := answeredThrough(t, "orig", tc.doc, tc.privacy)
			if ok.Get("P-Asserted-Identity") != tc.wantPAI || ok.Get("Privacy") != tc.wantPrivacy {
				t.Errorf("caller got P-Asserted-Identity %q and Privacy %q; want %q and %q",
					ok.Get("P-Asserted-Identity"), ok.Get("Privacy"), tc.wantPAI, tc.wantPrivacy)
			}
			if req.Supports("from-change") != tc.wantFromChange {
				t.Errorf("callee's INVITE has Supported %q, want from-change %v", req.GetAll("Supported"), tc.wantFromChange)
			}
		})
	}
}

func TestTerminatingIdentityRestriction(t *testing.T) {
	for _, tc := range []struct {
		name, doc, privacy string
		wantPrivacy        string
		wantFromChange     bool
	}{
		{"no TIR", "", "", "", true},
		{"permanent", tir("permanent", "presentation-not-restricted"), "none", "id", false},
		{"temporary, restricted by default", tir("temporary", "presentation-restricted"), "", "id", true},
		{"temporary, restricted by default, presented this time", tir("temporary", "presentation-restricted"), "none", "none", true},
		{"temporary, presented by default", tir("temporary", "presentation-not-restricted"), "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, req := answeredThrough(t, "term", tc.doc, tc.privacy)
			if ok.Get("Privacy") != tc.wantPrivacy || ok.Get("P-Asserted-Identity") == "" {
				t.Errorf("caller got Privacy %q and P-Asserted-Identity %q; want Privacy %q and the identity within the trust domain",
					ok.Get("Privacy"), ok.Get("P-Asserted-Identity"), tc.wantPrivacy)
			}
			if req.Supports("from-change") != tc.wantFromChange {
				t.Errorf("callee's INVITE has Supported %q, want from-change %v", req.GetAll("Supported"), tc.wantFromChange)
			}
		})
	}
}
