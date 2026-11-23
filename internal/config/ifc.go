package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// rawInitialFilterCriteria is one criterion of a service profile of the
// hss section. The keys are the element names of TS 29.228 Annex E in
// snake case, and the session case and the default handling take the
// labels of Annex E, such as ORIGINATING_REGISTERED and
// SESSION_CONTINUED.
type rawInitialFilterCriteria struct {
	// Feature is the operator's word for what this criterion turns on,
	// such as sms or mmtel. It is no part of the criterion that reaches
	// an S-CSCF: TS 29.228 Annex E has no element for it, so it stays in
	// the configuration and in the list of features that the HSS keeps
	// for the profile (hss.ServiceProfile). A criterion may leave it
	// out, and several criteria of one profile may share it.
	Feature      string `yaml:"feature"`
	Priority     int    `yaml:"priority"`
	TriggerPoint *struct {
		ConditionTypeCNF bool     `yaml:"condition_type_cnf"`
		SPT              []rawSPT `yaml:"spt"`
	} `yaml:"trigger_point"`
	ApplicationServer struct {
		ServerName string `yaml:"server_name"`
		// DefaultHandling empty means SESSION_CONTINUED, as when the
		// element is left out of the XML.
		DefaultHandling string `yaml:"default_handling"`
		ServiceInfo     string `yaml:"service_info"`
	} `yaml:"application_server"`
}

type rawSPT struct {
	ConditionNegated bool   `yaml:"condition_negated"`
	Group            []int  `yaml:"group"`
	RequestURI       string `yaml:"request_uri"`
	Method           string `yaml:"method"`
	SIPHeader        *struct {
		Header  string `yaml:"header"`
		Content string `yaml:"content"`
	} `yaml:"sip_header"`
	SessionCase string `yaml:"session_case"`
}

// parseServiceProfiles reads the named service profiles of the hss
// section, each one the criteria that the HSS puts in the user profile
// of a subscription that uses it (TS 29.228 B.2.2), with the features
// that those criteria name. A profile without criteria calls no
// application server, so an empty one is a profile like any other.
func parseServiceProfiles(raw map[string][]rawInitialFilterCriteria) (map[string]hss.ServiceProfile, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	profiles := make(map[string]hss.ServiceProfile, len(raw))
	// Sorted, so that a file with a mistake in two profiles always
	// fails with the same message.
	for _, name := range slices.Sorted(maps.Keys(raw)) {
		if name == "" {
			return nil, errors.New("service_profiles: a profile without a name")
		}
		criteria, err := parseInitialFilterCriteria("service_profiles."+name, raw[name])
		if err != nil {
			return nil, err
		}
		profiles[name] = hss.ServiceProfile{Criteria: criteria, Features: featuresOf(raw[name])}
	}
	return profiles, nil
}

// featuresOf collects the features that the criteria of one profile
// name, each one once and in the order the criteria give them. The
// list is taken from the criteria rather than written out beside them,
// so a profile cannot claim a feature whose criterion it does not have.
func featuresOf(raw []rawInitialFilterCriteria) []string {
	var features []string
	for _, c := range raw {
		if c.Feature != "" && !slices.Contains(features, c.Feature) {
			features = append(features, c.Feature)
		}
	}
	return features
}

// parseInitialFilterCriteria reads the criteria of the service profile
// at the key where, which its errors name.
func parseInitialFilterCriteria(where string, raw []rawInitialFilterCriteria) ([]ifc.InitialFilterCriteria, error) {
	var criteria []ifc.InitialFilterCriteria
	for i, r := range raw {
		c := ifc.InitialFilterCriteria{Priority: r.Priority, ApplicationServer: ifc.ApplicationServer{
			ServerName:  r.ApplicationServer.ServerName,
			ServiceInfo: r.ApplicationServer.ServiceInfo,
		}}
		if r.ApplicationServer.DefaultHandling != "" {
			d, err := ifc.ParseDefaultHandling(r.ApplicationServer.DefaultHandling)
			if err != nil {
				return nil, fmt.Errorf("%s[%d].application_server.default_handling: %w", where, i, err)
			}
			c.ApplicationServer.DefaultHandling = d
		}
		if tp := r.TriggerPoint; tp != nil {
			c.TriggerPoint = &ifc.TriggerPoint{ConditionTypeCNF: tp.ConditionTypeCNF}
			for j, s := range tp.SPT {
				spt := ifc.SPT{ConditionNegated: s.ConditionNegated, Group: s.Group, RequestURI: s.RequestURI, Method: s.Method}
				if s.SIPHeader != nil {
					spt.SIPHeader = &ifc.SIPHeader{Header: s.SIPHeader.Header, Content: s.SIPHeader.Content}
				}
				if s.SessionCase != "" {
					sc, err := ifc.ParseSessionCase(s.SessionCase)
					if err != nil {
						return nil, fmt.Errorf("%s[%d].trigger_point.spt[%d].session_case: %w", where, i, j, err)
					}
					spt.SessionCase = &sc
				}
				c.TriggerPoint.SPT = append(c.TriggerPoint.SPT, spt)
			}
		}
		criteria = append(criteria, c)
	}
	if err := ifc.Validate(criteria); err != nil {
		return nil, fmt.Errorf("%s: %w", where, err)
	}
	return criteria, nil
}
