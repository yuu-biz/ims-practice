package mmtel_test

import (
	"reflect"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mmtel"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// barring is a CB service with the given rules for incoming and for
// outgoing calls.
func barring(incoming, outgoing string) string {
	return `<complete-communication-barring>
  <incoming-communication-barring><cp:ruleset>` + incoming + `</cp:ruleset></incoming-communication-barring>
  <outgoing-communication-barring><cp:ruleset>` + outgoing + `</cp:ruleset></outgoing-communication-barring>
  <operator-incoming-communication-barring authorized="true"/>
  <operator-outgoing-communication-barring authorized="true"/>
</complete-communication-barring>`
}

func rule(id, conditions string, allow bool) string {
	a := "false"
	if allow {
		a = "true"
	}
	return `<cp:rule id="` + id + `"><cp:conditions>` + conditions + `</cp:conditions><cp:actions><allow>` + a + `</allow></cp:actions></cp:rule>`
}

// barred sends the caller's INVITE to requestURI through an AS whose
// served user alice has doc, and returns the caller's final response
// and whether the callee got the INVITE.
func barred(t *testing.T, sescase, doc, requestURI string, headers ...sip.HeaderField) (int, bool) {
	t.Helper()
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(doc)}}
	as := startAS(t, hss, mmtel.Config{HomeCountryCode: "1"})
	reached := make(chan bool, 1)
	callee := newUA(t, "bob", func(c *sip.IncomingCall) {
		reached <- true
		c.Reject(486)
	})
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	inv := viaAS(t, as, callee, sescase, offer)
	inv.RequestURI = requestURI
	inv.Headers = append(inv.Headers, headers...)
	out, err := caller.Invite(inv)
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, out)
	return got[len(got)-1].StatusCode, len(reached) > 0
}

func TestOutgoingCommunicationBarring(t *testing.T) {
	baoc := barring("", rule("baoc", "", false))
	boic := barring("", rule("boic", "<international/>", false))
	for _, tc := range []struct {
		name, sescase, doc, to string
		want                   int
	}{
		{"no barring", "orig", "", "tel:+442079460123", 486},
		{"BAOC", "orig", baoc, "tel:+12025550102", 603},
		{"BAOC, emergency", "orig", baoc, "urn:service:sos", 486},
		{"BOIC, abroad", "orig", boic, "tel:+442079460123", 603},
		{"BOIC, at home", "orig", boic, "tel:+12025550102", 486},
		{"BOIC, a SIP URI of the number", "orig", boic, "sip:+442079460123@" + domain + ";user=phone", 603},
		{"BAOC of the diverting user", "orig-cdiv", baoc, "tel:+12025550103", 603},
		{"BAOC does not bar calls to the user", "term", baoc, "sip:alice@" + domain, 486},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, reached := barred(t, tc.sescase, tc.doc, tc.to)
			if code != tc.want || reached != (tc.want != 603) {
				t.Errorf("caller got %d, callee reached %v; want %d", code, reached, tc.want)
			}
		})
	}
}

func TestIncomingCommunicationBarring(t *testing.T) {
	carol := `<cp:identity><cp:one id="tel:+12025550103"/></cp:identity>`
	acr := barring(rule("acr", "<anonymous/>", false), "")
	withheld := sip.HeaderField{Name: "Privacy", Value: "id"}
	carolPAI := sip.HeaderField{Name: "P-Asserted-Identity", Value: "<tel:+12025550103>"}
	for _, tc := range []struct {
		name, doc string
		headers   []sip.HeaderField
		want      int
	}{
		{"no barring", "", nil, 486},
		{"BAIC", barring(rule("baic", "", false), ""), nil, 603},
		{"a barred caller", barring(rule("carol", carol, false), ""), []sip.HeaderField{carolPAI}, 603},
		{"another caller", barring(rule("carol", carol, false), ""), nil, 486},
		{"BAIC with an allowed caller", barring(rule("baic", "", false)+rule("carol", carol, true), ""), []sip.HeaderField{carolPAI}, 486},
		{"ACR, anonymous", acr, []sip.HeaderField{withheld}, 433},
		{"ACR, presented", acr, nil, 486},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, reached := barred(t, "term", tc.doc, "sip:alice@"+domain, tc.headers...)
			if code != tc.want || reached != (tc.want == 486) {
				t.Errorf("caller got %d, callee reached %v; want %d", code, reached, tc.want)
			}
		})
	}
}

// TestBarringRejectsBeforeAnyLeg: a barred call never reaches the
// S-CSCF again; the caller gets only the final response.
func TestBarringRejectsBeforeAnyLeg(t *testing.T) {
	hss := &documents{docs: map[string]string{"sip:alice@" + domain: mmtelServices(barring("", rule("baoc", "", false)))}}
	as := startAS(t, hss, mmtel.Config{})
	callee := newUA(t, "bob", func(c *sip.IncomingCall) { c.Accept(answer) })
	caller := newUA(t, "alice", func(c *sip.IncomingCall) { c.Reject(480) })
	out, err := caller.Invite(viaAS(t, as, callee, "orig", offer))
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(readAll(t, out)); !reflect.DeepEqual(got, []int{603}) {
		t.Errorf("caller got %v, want 603 alone", got)
	}
}
