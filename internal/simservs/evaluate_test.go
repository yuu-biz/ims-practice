package simservs

import (
	"reflect"
	"strings"
	"testing"
)

const (
	caller    = "tel:+12025550170"
	callerSIP = "sip:+1-202-555-0170@ims.mnc001.mcc001.3gppnetwork.org;user=phone"
)

func mustSimservs(t *testing.T, doc string) Simservs {
	t.Helper()
	s, err := ParseSimservs([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEvaluateDiversionOfTheProvisionedRules(t *testing.T) {
	cd := mustSimservs(t, diversionDocument).CommunicationDiversion
	audio := []string{"audio"}
	for _, tc := range []struct {
		name   string
		event  DiversionEvent
		call   CallInfo
		rule   string // "" for no match
		target string
	}{
		// CFU is deactivated, so nothing matches on arrival.
		{"registered user, call arrives", AtCommunicationSetup, CallInfo{CallingIdentity: caller, Media: audio}, "", ""},
		{"CFNL", AtCommunicationSetup, CallInfo{CallingIdentity: caller, Media: audio, ServedUserNotRegistered: true},
			"call-diversion-not-logged-in", "tel:+12025550152"},
		{"CFB", OnBusy, CallInfo{CallingIdentity: caller, Media: audio}, "call-diversion-busy", "tel:+12025550150"},
		{"CFNR for a voice call", OnNoAnswer, CallInfo{CallingIdentity: caller, Media: audio},
			"call-diversion-no-reply", "sip:+12025550151@ims.mnc001.mcc001.3gppnetwork.org;user=phone"},
		// The CFNR rule asks for audio among the media of the offer.
		{"CFNR for a video call with audio", OnNoAnswer, CallInfo{Media: []string{"video", "AUDIO"}},
			"call-diversion-no-reply", "sip:+12025550151@ims.mnc001.mcc001.3gppnetwork.org;user=phone"},
		{"no CFNR without audio", OnNoAnswer, CallInfo{Media: []string{"message"}}, "", ""},
		{"CFNRc", OnNotReachable, CallInfo{Media: audio}, "call-diversion-not-reachable", "tel:+12025550152"},
		// The not-registered rule is not evaluated at a busy event.
		{"busy rule only at busy", OnBusy, CallInfo{ServedUserNotRegistered: true}, "call-diversion-busy", "tel:+12025550150"},
	} {
		got := EvaluateDiversion(cd, tc.event, tc.call)
		var rule, target string
		if got.Rule != nil {
			rule = got.Rule.ID
		}
		if got.ForwardTo != nil {
			target = got.ForwardTo.Target
		}
		if rule != tc.rule || target != tc.target || got.Diverts() != (tc.target != "") {
			t.Errorf("%s: rule %q target %q, want %q %q", tc.name, rule, target, tc.rule, tc.target)
		}
	}
}

func TestEvaluateDiversionStopsAtTheFirstMatch(t *testing.T) {
	doc := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <communication-diversion>
    <cp:ruleset>
      <cp:rule id="boss-straight-through">
        <cp:conditions><cp:identity><cp:one id="tel:+12025550170"/></cp:identity></cp:conditions>
        <cp:actions/>
      </cp:rule>
      <cp:rule id="anonymous-to-voicemail">
        <cp:conditions><anonymous/></cp:conditions>
        <cp:actions><forward-to><target>sip:voicemail@ims.mnc001.mcc001.3gppnetwork.org</target></forward-to></cp:actions>
      </cp:rule>
      <cp:rule id="cfu">
        <cp:actions><forward-to><target>tel:+12025550153</target></forward-to></cp:actions>
      </cp:rule>
      <cp:rule id="cfb-never-reached">
        <cp:conditions><busy/></cp:conditions>
        <cp:actions><forward-to><target>tel:+12025550150</target></forward-to></cp:actions>
      </cp:rule>
      <cp:rule id="cfu-unset">
        <cp:conditions><media>video</media></cp:conditions>
        <cp:actions><forward-to><target/></forward-to></cp:actions>
      </cp:rule>
    </cp:ruleset>
  </communication-diversion>
</simservs>`
	cd := mustSimservs(t, doc).CommunicationDiversion
	for _, tc := range []struct {
		name    string
		event   DiversionEvent
		call    CallInfo
		rule    string
		diverts bool
	}{
		// A matching rule with empty actions ends the evaluation without
		// a diversion (TS 24.604 4.9.1.4).
		{"empty actions", AtCommunicationSetup, CallInfo{CallingIdentity: caller}, "boss-straight-through", false},
		{"same number as a SIP URI", AtCommunicationSetup, CallInfo{CallingIdentity: callerSIP}, "boss-straight-through", false},
		{"no identity is anonymous", AtCommunicationSetup, CallInfo{}, "anonymous-to-voicemail", true},
		{"restricted identity is anonymous", AtCommunicationSetup, CallInfo{CallingIdentity: "tel:+12025550171", CallingIdentityRestricted: true}, "anonymous-to-voicemail", true},
		{"unconditional", AtCommunicationSetup, CallInfo{CallingIdentity: "tel:+12025550171"}, "cfu", true},
		{"busy rule at busy", OnBusy, CallInfo{CallingIdentity: "tel:+12025550171"}, "cfb-never-reached", true},
		{"no rule for no answer", OnNoAnswer, CallInfo{CallingIdentity: "tel:+12025550171"}, "", false},
	} {
		got := EvaluateDiversion(cd, tc.event, tc.call)
		var rule string
		if got.Rule != nil {
			rule = got.Rule.ID
		}
		if rule != tc.rule || got.Diverts() != tc.diverts {
			t.Errorf("%s: rule %q diverts %v, want %q %v", tc.name, rule, got.Diverts(), tc.rule, tc.diverts)
		}
	}

	// An empty target is a provisioned service that is not set up.
	cd.Ruleset.Rules = cd.Ruleset.Rules[4:]
	got := EvaluateDiversion(cd, AtCommunicationSetup, CallInfo{Media: []string{"video"}})
	if got.Rule == nil || got.Rule.ID != "cfu-unset" || got.ForwardTo == nil || got.Diverts() {
		t.Errorf("empty target: %+v", got)
	}
}

func TestEvaluateDiversionOfAnInactiveService(t *testing.T) {
	doc := strings.Replace(diversionDocument, `<communication-diversion active="true">`, `<communication-diversion active="false">`, 1)
	cd := mustSimservs(t, doc).CommunicationDiversion
	if got := EvaluateDiversion(cd, OnBusy, CallInfo{}); got.Rule != nil {
		t.Errorf("inactive service matched %s", got.Rule.ID)
	}
	if got := EvaluateDiversion(nil, OnBusy, CallInfo{}); got.Rule != nil {
		t.Error("no service matched")
	}
}

func TestEvaluateIncomingBarring(t *testing.T) {
	s := mustSimservs(t, barringDocument)
	baicOn := mustSimservs(t, strings.Replace(barringDocument,
		`<cp:rule id="baic">
        <cp:conditions><rule-deactivated/></cp:conditions>`, `<cp:rule id="baic">`, 1))
	for _, tc := range []struct {
		name    string
		icb     *IncomingCommunicationBarring
		call    CallInfo
		allow   bool
		matched []string
		acr     bool
	}{
		{"nothing matches", s.IncomingCommunicationBarring, CallInfo{CallingIdentity: caller}, true, nil, false},
		{"BIC-Roam", s.IncomingCommunicationBarring, CallInfo{CallingIdentity: caller, ServedUserRoaming: true}, false, []string{"bic-roam"}, false},
		// ACR needs a P-Asserted-Identity with privacy (TS 24.611
		// 4.5.2.6.2).
		{"ACR", s.IncomingCommunicationBarring, CallInfo{CallingIdentity: caller, CallingIdentityRestricted: true}, false, []string{"acr"}, true},
		{"no identity is not ACR", s.IncomingCommunicationBarring, CallInfo{}, true, nil, false},
		// One allowing rule is enough (TS 24.611 4.9.1.3).
		{"allow list wins over roaming", s.IncomingCommunicationBarring,
			CallInfo{CallingIdentity: "tel:+1-202-555-0160", ServedUserRoaming: true}, true, []string{"bic-roam", "family"}, false},
		{"BAIC", baicOn.IncomingCommunicationBarring, CallInfo{CallingIdentity: caller}, false, []string{"baic"}, false},
		{"BAIC with ACR answers 433", baicOn.IncomingCommunicationBarring, CallInfo{CallingIdentity: caller, CallingIdentityRestricted: true}, false, []string{"baic", "acr"}, true},
		{"BAIC with the allow list", baicOn.IncomingCommunicationBarring, CallInfo{CallingIdentity: "tel:+12025550160"}, true, []string{"baic", "family"}, false},
		{"no service", nil, CallInfo{ServedUserRoaming: true}, true, nil, false},
	} {
		got := EvaluateIncomingBarring(tc.icb, tc.call)
		if got.Allow != tc.allow || !reflect.DeepEqual(got.MatchedRules, tc.matched) || got.AnonymousCommunicationRejection != tc.acr {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
}

func TestEvaluateOutgoingBarring(t *testing.T) {
	// The outgoing rule set of barringDocument is inactive; switch it
	// on.
	s := mustSimservs(t, strings.Replace(barringDocument, `active="false" password-required="true"`, `password-required="true"`, 1))
	ocb := s.OutgoingCommunicationBarring
	home := CallInfo{HomeCountryCode: "1"}
	at := func(c CallInfo, called string, media ...string) CallInfo {
		c.CalledIdentity, c.Media = called, media
		return c
	}
	// The foreign numbers use the made-up country codes 999 (the
	// visited country) and 0 with 555-01xx subscriber numbers.
	roaming := CallInfo{HomeCountryCode: "1", VisitedCountryCode: "999", ServedUserRoaming: true}
	for _, tc := range []struct {
		name    string
		call    CallInfo
		allow   bool
		matched []string
	}{
		{"national call", at(home, "tel:+12025550180", "audio"), true, nil},
		{"national call as a SIP URI", at(home, "sip:+12025550180@ims.mnc001.mcc001.3gppnetwork.org;user=phone", "audio"), true, nil},
		{"local number", at(home, "tel:5550180;phone-context=ims.mnc001.mcc001.3gppnetwork.org", "audio"), true, nil},
		{"not a number", at(home, "sip:conference@ims.mnc001.mcc001.3gppnetwork.org", "audio"), true, nil},
		{"BOIC", at(home, "tel:+999-555-0181", "audio"), false, []string{"boic"}},
		{"BOIC and BOIC-exHC for video", at(home, "tel:+9995550181", "video"), false, []string{"boic", "boic-exhc"}},
		// Abroad, a call home is international but not to a foreign
		// country other than the home country (TS 24.611 4.9.1.4).
		{"call home from abroad", at(roaming, "tel:+12025550180", "video"), false, []string{"boic"}},
		{"call within the visited country", at(roaming, "tel:+9995550181", "video"), true, nil},
		{"third country from abroad", at(roaming, "tel:+05550182", "video"), false, []string{"boic", "boic-exhc"}},
		{"no home country code", at(CallInfo{}, "tel:+9995550181", "video"), true, nil},
	} {
		got := EvaluateOutgoingBarring(ocb, tc.call)
		if got.Allow != tc.allow || !reflect.DeepEqual(got.MatchedRules, tc.matched) || got.AnonymousCommunicationRejection {
			t.Errorf("%s: %+v", tc.name, got)
		}
	}
	if got := EvaluateOutgoingBarring(mustSimservs(t, barringDocument).OutgoingCommunicationBarring, at(home, "tel:+9995550181")); !got.Allow {
		t.Error("inactive OCB barred a call")
	}
}

func TestEvaluateOutgoingBarringMatchesTheCalledIdentity(t *testing.T) {
	doc := `<simservs xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy">
  <outgoing-communication-barring>
    <cp:ruleset>
      <cp:rule id="baoc"><cp:actions><allow>false</allow></cp:actions></cp:rule>
      <cp:rule id="allowed-numbers">
        <cp:conditions><cp:identity><cp:one id="tel:+12025550190"/></cp:identity></cp:conditions>
        <cp:actions><allow>true</allow></cp:actions>
      </cp:rule>
      <cp:rule id="allowed-domain">
        <cp:conditions><cp:identity>
          <cp:many domain="ims.mnc001.mcc001.3gppnetwork.org"><cp:except id="sip:premium@ims.mnc001.mcc001.3gppnetwork.org"/></cp:many>
        </cp:identity></cp:conditions>
        <cp:actions><allow>true</allow></cp:actions>
      </cp:rule>
      <cp:rule id="no-action"><cp:conditions><anonymous/></cp:conditions></cp:rule>
    </cp:ruleset>
  </outgoing-communication-barring>
</simservs>`
	ocb := mustSimservs(t, doc).OutgoingCommunicationBarring
	for _, tc := range []struct {
		called string
		allow  bool
	}{
		{"tel:+12025550190", true},
		{"tel:+1.202.555.0190;foo=bar", true},
		{"tel:+12025550191", false},
		{"sip:alice@IMS.mnc001.mcc001.3gppnetwork.org", true},
		{"sip:premium@ims.mnc001.mcc001.3gppnetwork.org", false},
		{"sip:bob@example.net", false},
		{"", false},
	} {
		// The anonymous condition is about callers, so the rule without
		// an action never matches an outgoing call; if it did, it would
		// count as allow=false (RFC 4745 10.2).
		got := EvaluateOutgoingBarring(ocb, CallInfo{CalledIdentity: tc.called, CallingIdentityRestricted: true, CallingIdentity: caller})
		if got.Allow != tc.allow {
			t.Errorf("%q: %+v", tc.called, got)
		}
	}
}

func TestUnknownConditionsNeverMatch(t *testing.T) {
	doc := strings.Replace(barringDocument, "<roaming/>", `<roaming/><x:weather xmlns:x="urn:example:vendor"/>`, 1)
	s := mustSimservs(t, doc)
	if got := EvaluateIncomingBarring(s.IncomingCommunicationBarring, CallInfo{ServedUserRoaming: true}); !got.Allow {
		t.Errorf("%+v", got)
	}
}

func TestSameIdentity(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"tel:+12025550170", "tel:+1-202-555-0170", true},
		{"tel:+12025550170", "sip:+12025550170@ims.mnc001.mcc001.3gppnetwork.org;user=phone", true},
		{"tel:+12025550170", "sip:+12025550170@ims.mnc001.mcc001.3gppnetwork.org", false},
		{"tel:+12025550170", "tel:+12025550171", false},
		{"sip:alice@IMS.example.net;transport=tcp", "sip:alice@ims.example.net", true},
		{"sip:Alice@ims.example.net", "sip:alice@ims.example.net", false},
		{"sips:alice@ims.example.net", "sip:alice@ims.example.net", false},
		// The host of an identity compares as a whole, an IPv6 address
		// in brackets included.
		{"sip:alice@[2001:db8::1]", "sip:alice@[2001:DB8::1]", true},
		{"sip:alice@[2001:db8::1]", "sip:alice@[2001:db8::2]", false},
		{"mailto:alice@example.net", "mailto:alice@example.net", false},
	} {
		if got := sameIdentity(tc.a, tc.b); got != tc.want {
			t.Errorf("sameIdentity(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}
