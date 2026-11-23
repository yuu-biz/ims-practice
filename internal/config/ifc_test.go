package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// smsCriteriaYAML is a service profile with the two criteria of the SMS
// design: the third-party REGISTER to the IP-SM-GW with the MSISDN, and
// the SMS that the user sends.
const smsCriteriaYAML = `  default_service_profile: default
  service_profiles:
    default:
      - priority: 0
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: REGISTER}
        application_server:
          server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060
          default_handling: SESSION_CONTINUED
          service_info: "{msisdn}"
      - priority: 1
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: MESSAGE}
            - group: [1]
              sip_header: {header: ^Content-Type$, content: '^application/vnd\.3gpp\.sms$'}
            - {group: [2], session_case: ORIGINATING_REGISTERED}
        application_server:
          server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060
`

func withSMSCriteria(yaml string) string {
	return strings.Replace(yaml, "hss:\n", "hss:\n"+smsCriteriaYAML, 1)
}

func TestParseServiceProfiles(t *testing.T) {
	cfg, err := Parse(strings.NewReader(withSMSCriteria(combinedYAML)))
	if err != nil {
		t.Fatal(err)
	}
	originating := ifc.OriginatingRegistered
	server := "sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060"
	want := []ifc.InitialFilterCriteria{
		{
			Priority:          0,
			TriggerPoint:      &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{{Group: []int{0}, Method: "REGISTER"}}},
			ApplicationServer: ifc.ApplicationServer{ServerName: server, ServiceInfo: hss.MSISDNPlaceholder},
		},
		{
			Priority: 1,
			TriggerPoint: &ifc.TriggerPoint{ConditionTypeCNF: true, SPT: []ifc.SPT{
				{Group: []int{0}, Method: "MESSAGE"},
				{Group: []int{1}, SIPHeader: &ifc.SIPHeader{Header: "^Content-Type$", Content: `^application/vnd\.3gpp\.sms$`}},
				{Group: []int{2}, SessionCase: &originating},
			}},
			ApplicationServer: ifc.ApplicationServer{ServerName: server},
		},
	}
	if !reflect.DeepEqual(cfg.HSS.ServiceProfiles, map[string][]ifc.InitialFilterCriteria{"default": want}) {
		t.Errorf("profiles\n%+v\nwant default\n%+v", cfg.HSS.ServiceProfiles, want)
	}
	if cfg.HSS.DefaultServiceProfile != "default" {
		t.Errorf("the profile of a subscription is %q", cfg.HSS.DefaultServiceProfile)
	}
	// A node may answer for subscriptions without calling any
	// application server, so service_profiles is not required.
	if cfg, err := Parse(strings.NewReader(combinedYAML)); err != nil || cfg.HSS.ServiceProfiles != nil ||
		cfg.HSS.DefaultServiceProfile != "" {
		t.Errorf("without service_profiles: %v, %+v", err, cfg.HSS.ServiceProfiles)
	}
}

// TestParseRejectsServiceProfileNames: a profile is reached by its
// name, so the setting must name a profile that the file defines and
// every profile must have a name. A renamed profile is then found when
// the file is read, not when a subscription needs it.
func TestParseRejectsServiceProfileNames(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"unknown default", "default_service_profile: default", "default_service_profile: trial",
			`default_service_profile: "trial" is not a profile of service_profiles, which has [default]`},
		{"no default", "  default_service_profile: default\n", "",
			"default_service_profile: required, one of [default]"},
		{"a profile without a name", "    default:", `    "":`,
			"service_profiles: a profile without a name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(withSMSCriteria(combinedYAML), tt.old, tt.new, 1)
			if yaml == withSMSCriteria(combinedYAML) {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
	// A name with no profile to name is a mistake of its own: the file
	// says which profile to use and defines none.
	named := strings.Replace(combinedYAML, "hss:\n", "hss:\n  default_service_profile: default\n", 1)
	_, err := Parse(strings.NewReader(named))
	if err == nil || !strings.Contains(err.Error(), `default_service_profile: "default", but the node defines no service_profiles`) {
		t.Errorf("error = %v, want it to mention that the node defines no service_profiles", err)
	}
}

func TestParseRejectsInitialFilterCriteria(t *testing.T) {
	tests := []struct {
		name    string
		old     string
		new     string
		wantErr string
	}{
		{"unknown session case", "session_case: ORIGINATING_REGISTERED", "session_case: originating",
			"service_profiles.default[1].trigger_point.spt[2].session_case"},
		{"unknown default handling", "default_handling: SESSION_CONTINUED", "default_handling: continue",
			"service_profiles.default[0].application_server.default_handling"},
		{"same priority twice", "priority: 1", "priority: 0", "Priority 0 appears twice"},
		{"not an ERE", `content: '^application/vnd\.3gpp\.sms$'`, `content: '\d'`, "not an Extended Regular Expression"},
		{"no group", "{group: [0], method: MESSAGE}", "{method: MESSAGE}", "Group: at least one is required"},
		{"server is not a SIP URI", "server_name: sip:ipsmgw", "server_name: tel:+12025550100;x=sip:ipsmgw", "ServerName"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			yaml := strings.Replace(withSMSCriteria(combinedYAML), tt.old, tt.new, 1)
			if yaml == withSMSCriteria(combinedYAML) {
				t.Fatalf("test edit %q did not apply", tt.old)
			}
			_, err := Parse(strings.NewReader(yaml))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// mmtelCriteriaYAML are criteria for the MMTel AS, one
// per session case it serves, after those of SMS. They are not in
// deploy/compose/hss.yaml until the AS exists.
const mmtelCriteriaYAML = `      - priority: 10
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: INVITE}
            - {group: [1], session_case: ORIGINATING_REGISTERED}
        application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
      - priority: 11
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: INVITE}
            - {group: [1], session_case: TERMINATING_REGISTERED}
        application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
      - priority: 12
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: INVITE}
            - {group: [1], session_case: TERMINATING_UNREGISTERED}
        application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
      - priority: 13
        trigger_point:
          condition_type_cnf: true
          spt:
            - {group: [0], method: INVITE}
            - {group: [1], session_case: ORIGINATING_CDIV}
        application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
`

// TestMMTelCriteria: the configuration expresses a criterion for each
// session case of TS 29.228 Annex E, so that an INVITE goes to the
// MMTel AS in each case exactly once, and a MESSAGE or REGISTER still
// goes only where the SMS criteria send it.
func TestMMTelCriteria(t *testing.T) {
	cfg, err := Parse(strings.NewReader(strings.Replace(combinedYAML, "hss:\n", "hss:\n"+smsCriteriaYAML+mmtelCriteriaYAML, 1)))
	if err != nil {
		t.Fatal(err)
	}
	criteria := ifc.Ordered(cfg.HSS.ServiceProfiles["default"])
	invite := &sip.Message{Method: sip.INVITE, RequestURI: "tel:+12025550102"}
	message := &sip.Message{Method: sip.MESSAGE, RequestURI: "tel:+12025550100"}
	message.Add("Content-Type", "application/vnd.3gpp.sms")
	for sc, want := range map[ifc.SessionCase]int{
		ifc.OriginatingRegistered: 10, ifc.TerminatingRegistered: 11, ifc.TerminatingUnregistered: 12, ifc.OriginatingCDIV: 13,
	} {
		var matched []int
		for _, c := range criteria {
			if c.Matches(invite, sc) {
				matched = append(matched, c.Priority)
			}
			if c.Matches(message, sc) && c.Priority >= 10 {
				t.Errorf("%s: a MESSAGE matches the MMTel criterion %d", sc, c.Priority)
			}
		}
		if len(matched) != 1 || matched[0] != want {
			t.Errorf("%s: an INVITE matches the criteria %v, want %d", sc, matched, want)
		}
	}
	for _, sc := range []ifc.SessionCase{ifc.TerminatingUnregistered, ifc.OriginatingCDIV} {
		if !ifc.HasSessionCase(criteria, sc) {
			t.Errorf("no criterion is meant for %s", sc)
		}
	}
}
