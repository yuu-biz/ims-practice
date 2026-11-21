package ifc

import (
	"encoding/xml"
	"reflect"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

func request(method sip.Method, requestURI string, headers ...string) *sip.Message {
	m := &sip.Message{Method: method, RequestURI: requestURI}
	for i := 0; i+1 < len(headers); i += 2 {
		m.Add(headers[i], headers[i+1])
	}
	return m
}

func sessionCase(c SessionCase) *SessionCase { return &c }

// smsContentType is the Content pattern of deploy/compose/hss.yaml: the
// media type of TS 24.341 5.3.1.2 in any case (RFC 2045 5.1), with or
// without parameters.
const smsContentType = `^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$`

// sms is the originating SMS criterion of the SMS design: Method
// MESSAGE AND an SMS body AND originating, in CNF with one SPT per
// group.
var sms = InitialFilterCriteria{
	Priority: 10,
	TriggerPoint: &TriggerPoint{ConditionTypeCNF: true, SPT: []SPT{
		{Group: []int{0}, Method: "MESSAGE"},
		{Group: []int{1}, SIPHeader: &SIPHeader{Header: "^Content-Type$", Content: smsContentType}},
		{Group: []int{2}, SessionCase: sessionCase(OriginatingRegistered)},
	}},
	ApplicationServer: ApplicationServer{ServerName: "sip:ipsmgw." + domain + ":5060"},
}

// thirdPartyRegister sends every REGISTER to the same server.
var thirdPartyRegister = InitialFilterCriteria{
	Priority:          0,
	TriggerPoint:      &TriggerPoint{SPT: []SPT{{Group: []int{0}, Method: "REGISTER"}}},
	ApplicationServer: ApplicationServer{ServerName: "sip:ipsmgw." + domain + ":5060", ServiceInfo: "12025550101"},
}

func TestSMSCriteria(t *testing.T) {
	user := "sip:001010000000001@" + domain
	tests := []struct {
		name     string
		criteria InitialFilterCriteria
		req      *sip.Message
		sc       SessionCase
		want     bool
	}{
		{"SMS from the user", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "application/vnd.3gpp.sms"), OriginatingRegistered, true},
		{"SMS to the user", sms, request(sip.MESSAGE, user, "Content-Type", "application/vnd.3gpp.sms"), TerminatingRegistered, false},
		{"header name in lower case", sms, request(sip.MESSAGE, "sip:sc@"+domain, "content-type", "application/vnd.3gpp.sms"), OriginatingRegistered, true},
		{"compact header name", sms, request(sip.MESSAGE, "sip:sc@"+domain, "c", "application/vnd.3gpp.sms"), OriginatingRegistered, true},
		{"media type with a parameter", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "application/vnd.3gpp.sms;x=y"), OriginatingRegistered, true},
		{"media type with a space before the parameter", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "application/vnd.3gpp.sms ;x=y"), OriginatingRegistered, true},
		{"media type in capitals", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "Application/VND.3GPP.SMS"), OriginatingRegistered, true},
		{"another media type that starts the same", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "application/vnd.3gpp.sms+xml"), OriginatingRegistered, false},
		{"text message", sms, request(sip.MESSAGE, "sip:sc@"+domain, "Content-Type", "text/plain"), OriginatingRegistered, false},
		{"no body", sms, request(sip.MESSAGE, "sip:sc@"+domain), OriginatingRegistered, false},
		{"INVITE", sms, request(sip.INVITE, "sip:sc@"+domain, "Content-Type", "application/vnd.3gpp.sms"), OriginatingRegistered, false},
		{"REGISTER", thirdPartyRegister, request(sip.REGISTER, "sip:"+domain), OriginatingRegistered, true},
		{"not a REGISTER", thirdPartyRegister, request(sip.MESSAGE, "sip:sc@"+domain), OriginatingRegistered, false},
	}
	for _, tt := range tests {
		if got := tt.criteria.Matches(tt.req, tt.sc); got != tt.want {
			t.Errorf("%s: Matches = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// The trigger of TS 29.228 Annex C, written both ways:
// Method INVITE OR Method MESSAGE OR (Method SUBSCRIBE AND NOT From
// "joe"). In CNF it is (INVITE OR MESSAGE OR SUBSCRIBE) AND (INVITE OR
// MESSAGE OR NOT From "joe"); in DNF it has three groups.
var (
	notJoe = SPT{ConditionNegated: true, SIPHeader: &SIPHeader{Header: "From", Content: `"joe"`}}
	cnf    = TriggerPoint{ConditionTypeCNF: true, SPT: []SPT{
		{Group: []int{0, 1}, Method: "INVITE"},
		{Group: []int{0, 1}, Method: "MESSAGE"},
		{Group: []int{0}, Method: "SUBSCRIBE"},
		withGroups(notJoe, 1),
	}}
	dnf = TriggerPoint{SPT: []SPT{
		{Group: []int{0}, Method: "INVITE"},
		{Group: []int{1}, Method: "MESSAGE"},
		{Group: []int{2}, Method: "SUBSCRIBE"},
		withGroups(notJoe, 2),
	}}
)

func withGroups(s SPT, groups ...int) SPT {
	s.Group = groups
	return s
}

func TestCNFAndDNFOfTheSameTrigger(t *testing.T) {
	joe := `"joe" <sip:joe@` + domain + `>;tag=1`
	ann := `"ann" <sip:ann@` + domain + `>;tag=1`
	tests := []struct {
		req  *sip.Message
		want bool
	}{
		{request(sip.INVITE, "sip:b@"+domain, "From", joe), true},
		{request(sip.MESSAGE, "sip:b@"+domain, "From", joe), true},
		{request(sip.SUBSCRIBE, "sip:b@"+domain, "From", ann), true},
		{request(sip.SUBSCRIBE, "sip:b@"+domain, "From", joe), false},
		{request(sip.OPTIONS, "sip:b@"+domain, "From", ann), false},
	}
	for _, tt := range tests {
		for name, tp := range map[string]TriggerPoint{"CNF": cnf, "DNF": dnf} {
			c := InitialFilterCriteria{TriggerPoint: &tp}
			if got := c.Matches(tt.req, OriginatingRegistered); got != tt.want {
				t.Errorf("%s, %s from %s: Matches = %v, want %v", name, tt.req.Method, tt.req.Get("From"), got, tt.want)
			}
		}
	}
}

func TestSPTs(t *testing.T) {
	tests := []struct {
		name string
		spt  SPT
		req  *sip.Message
		want bool
	}{
		{"Request-URI host", SPT{RequestURI: `^sc\.` + domain + `$`}, request(sip.MESSAGE, "sip:user@sc."+domain), true},
		{"Request-URI user is not matched", SPT{RequestURI: "user"}, request(sip.MESSAGE, "sip:user@sc."+domain), false},
		{"Request-URI port", SPT{RequestURI: ":5070$"}, request(sip.MESSAGE, "sip:sc."+domain+":5070;lr"), true},
		{"tel URI number", SPT{RequestURI: `^\+1202555`}, request(sip.INVITE, "tel:+12025550102"), true},
		{"pattern matches inside the text", SPT{RequestURI: "mnc001"}, request(sip.INVITE, "sip:b@"+domain), true},
		{"header present", SPT{SIPHeader: &SIPHeader{Header: "^Accept-Contact$"}}, request(sip.MESSAGE, "sip:b@"+domain, "Accept-Contact", "*;+g.3gpp.smsip"), true},
		{"header absent", SPT{ConditionNegated: true, SIPHeader: &SIPHeader{Header: "^Accept-Contact$"}}, request(sip.MESSAGE, "sip:b@"+domain), true},
		{"header name without case", SPT{SIPHeader: &SIPHeader{Header: "^Content-Type$"}}, request(sip.MESSAGE, "sip:b@"+domain, "content-type", "text/plain"), true},
		{"header pattern without case", SPT{SIPHeader: &SIPHeader{Header: "^content-type$"}}, request(sip.MESSAGE, "sip:b@"+domain, "Content-Type", "text/plain"), true},
		{"header value with case", SPT{SIPHeader: &SIPHeader{Header: "^Subject$", Content: "^hello$"}}, request(sip.MESSAGE, "sip:b@"+domain, "Subject", "Hello"), false},
		{"each value of a list", SPT{SIPHeader: &SIPHeader{Header: "^Supported$", Content: "^timer$"}}, request(sip.INVITE, "sip:b@"+domain, "Supported", "100rel, timer"), true},
		{"each row of a header", SPT{SIPHeader: &SIPHeader{Header: "^P-Asserted-Identity$", Content: "^<tel:"}},
			request(sip.INVITE, "sip:b@"+domain, "P-Asserted-Identity", "<sip:a@"+domain+">", "P-Asserted-Identity", "<tel:+12025550101>"), true},
		{"negated method", SPT{ConditionNegated: true, Method: "INVITE"}, request(sip.MESSAGE, "sip:b@"+domain), true},
		{"method with case", SPT{Method: "message"}, request(sip.MESSAGE, "sip:b@"+domain), false},
	}
	for _, tt := range tests {
		tt.spt.Group = []int{0}
		c := InitialFilterCriteria{TriggerPoint: &TriggerPoint{ConditionTypeCNF: true, SPT: []SPT{tt.spt}}}
		if err := Validate([]InitialFilterCriteria{withServer(c)}); err != nil {
			t.Errorf("%s: %v", tt.name, err)
		}
		if got := c.Matches(tt.req, OriginatingRegistered); got != tt.want {
			t.Errorf("%s: Matches = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestNegatedSessionCase is the NOTE of TS 29.228 B.2.3: NOT
// ORIGINATING_REGISTERED holds in every other session case.
func TestNegatedSessionCase(t *testing.T) {
	c := InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{
		{ConditionNegated: true, Group: []int{0}, SessionCase: sessionCase(OriginatingRegistered)},
	}}}
	req := request(sip.INVITE, "sip:b@"+domain)
	if c.Matches(req, OriginatingRegistered) {
		t.Error("matches the originating case")
	}
	if !c.Matches(req, TerminatingRegistered) {
		t.Error("does not match the terminating case")
	}
}

func TestNoTriggerPointMatchesEveryRequest(t *testing.T) {
	c := InitialFilterCriteria{ApplicationServer: ApplicationServer{ServerName: "sip:as." + domain}}
	if !c.Matches(request(sip.OPTIONS, "sip:b@"+domain), TerminatingRegistered) {
		t.Error("a criterion without TriggerPoint did not match")
	}
}

func TestOrderedByPriority(t *testing.T) {
	list := []InitialFilterCriteria{{Priority: 20}, {Priority: 0}, {Priority: 10}}
	got := Ordered(list)
	for i, want := range []int{0, 10, 20} {
		if got[i].Priority != want {
			t.Fatalf("Ordered priorities = %v, want 0, 10, 20", []int{got[0].Priority, got[1].Priority, got[2].Priority})
		}
	}
	if list[0].Priority != 20 {
		t.Error("Ordered changed its argument")
	}
}

// profileXML is an InitialFilterCriteria element as the User-Data of
// TS 29.228 Annex E carries it, with an element this package skips.
const profileXML = `<InitialFilterCriteria>
  <Priority>10</Priority>
  <TriggerPoint>
    <ConditionTypeCNF>1</ConditionTypeCNF>
    <SPT><ConditionNegated>0</ConditionNegated><Group>0</Group><Method>MESSAGE</Method></SPT>
    <SPT><ConditionNegated>0</ConditionNegated><Group>1</Group>
      <SIPHeader><Header>^Content-Type$</Header><Content>` + smsContentType + `</Content></SIPHeader></SPT>
    <SPT><ConditionNegated>0</ConditionNegated><Group>2</Group><SessionCase>0</SessionCase></SPT>
  </TriggerPoint>
  <ApplicationServer>
    <ServerName>sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060</ServerName>
    <DefaultHandling>0</DefaultHandling>
  </ApplicationServer>
  <ProfilePartIndicator>0</ProfilePartIndicator>
</InitialFilterCriteria>`

func TestXML(t *testing.T) {
	var got InitialFilterCriteria
	if err := xml.Unmarshal([]byte(profileXML), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, sms) {
		t.Errorf("parsed\n%+v\nwant\n%+v", got, sms)
	}
	b, err := xml.Marshal(sms)
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range []string{"<Priority>10</Priority>", "<ConditionTypeCNF>true</ConditionTypeCNF>",
		"<Group>2</Group><SessionCase>0</SessionCase>", "<SIPHeader><Header>^Content-Type$</Header>", "<DefaultHandling>0</DefaultHandling>"} {
		if !strings.Contains(string(b), element) {
			t.Errorf("marshalled XML has no %s:\n%s", element, b)
		}
	}
	var again InitialFilterCriteria
	if err := xml.Unmarshal(b, &again); err != nil || !reflect.DeepEqual(again, sms) {
		t.Errorf("round trip gave %+v, %v", again, err)
	}
}

func withServer(c InitialFilterCriteria) InitialFilterCriteria {
	c.ApplicationServer.ServerName = "sip:as." + domain
	return c
}

func TestValidate(t *testing.T) {
	if err := Validate([]InitialFilterCriteria{thirdPartyRegister, sms}); err != nil {
		t.Errorf("the SMS criteria: %v", err)
	}
	bad := map[string][]InitialFilterCriteria{
		"same priority twice": {sms, sms},
		"no server":           {{Priority: 1}},
		"tel URI server":      {{ApplicationServer: ApplicationServer{ServerName: "tel:+12025550100"}}},
		"no SPT":              {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{}})},
		"no group":            {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{{Method: "MESSAGE"}}}})},
		"two conditions":      {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{{Group: []int{0}, Method: "MESSAGE", RequestURI: "x"}}}})},
		"no condition":        {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{{Group: []int{0}}}}})},
		"not an ERE":          {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{{Group: []int{0}, RequestURI: `\d+`}}}})},
		"unknown session case": {withServer(InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{
			{Group: []int{0}, SessionCase: sessionCase(5)}}}})},
	}
	for name, list := range bad {
		if err := Validate(list); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestLabels(t *testing.T) {
	if c, err := ParseSessionCase("TERMINATING_REGISTERED"); err != nil || c != TerminatingRegistered {
		t.Errorf("ParseSessionCase = %v, %v", c, err)
	}
	if d, err := ParseDefaultHandling("SESSION_TERMINATED"); err != nil || d != SessionTerminated {
		t.Errorf("ParseDefaultHandling = %v, %v", d, err)
	}
	if _, err := ParseSessionCase("originating"); err == nil {
		t.Error("ParseSessionCase took an unknown label")
	}
	if OriginatingCDIV.String() != "ORIGINATING_CDIV" || SessionContinued.String() != "SESSION_CONTINUED" {
		t.Errorf("labels %s and %s", OriginatingCDIV, SessionContinued)
	}
}

// TestHasSessionCase: only a criterion that names the session case in
// an SPT that is not negated is meant for it.
func TestHasSessionCase(t *testing.T) {
	unregistered := InitialFilterCriteria{TriggerPoint: &TriggerPoint{ConditionTypeCNF: true, SPT: []SPT{
		{Group: []int{0}, Method: "INVITE"},
		{Group: []int{1}, SessionCase: sessionCase(TerminatingRegistered)},
		{Group: []int{1}, SessionCase: sessionCase(TerminatingUnregistered)},
	}}}
	negated := InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{
		{Group: []int{0}, SessionCase: sessionCase(OriginatingRegistered), ConditionNegated: true},
	}}}
	register := InitialFilterCriteria{TriggerPoint: &TriggerPoint{SPT: []SPT{{Group: []int{0}, Method: "REGISTER"}}}}
	for _, tt := range []struct {
		name     string
		criteria []InitialFilterCriteria
		sc       SessionCase
		want     bool
	}{
		{"named in a group of two", []InitialFilterCriteria{sms, unregistered}, TerminatingUnregistered, true},
		{"another case", []InitialFilterCriteria{sms, unregistered}, OriginatingCDIV, false},
		{"no SPT on the case", []InitialFilterCriteria{register, {}}, TerminatingUnregistered, false},
		{"negated", []InitialFilterCriteria{negated}, TerminatingUnregistered, false},
		{"none", nil, OriginatingRegistered, false},
	} {
		if got := HasSessionCase(tt.criteria, tt.sc); got != tt.want {
			t.Errorf("%s: HasSessionCase(%s) = %v, want %v", tt.name, tt.sc, got, tt.want)
		}
	}
}
