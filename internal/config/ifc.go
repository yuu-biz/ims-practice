package config

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// rawInitialFilterCriteria is one criterion of a service profile of the
// hss section. The keys are the element names of TS 29.228 Annex E in
// snake case, and the session case and the default handling take the
// labels of Annex E, such as ORIGINATING_REGISTERED and
// SESSION_CONTINUED.
type rawInitialFilterCriteria struct {
	Priority     int `yaml:"priority"`
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
// of a subscription that uses it (TS 29.228 B.2.2). A profile without
// criteria calls no application server, so an empty one is a profile
// like any other.
func parseServiceProfiles(raw map[string][]rawInitialFilterCriteria) (map[string][]ifc.InitialFilterCriteria, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	profiles := make(map[string][]ifc.InitialFilterCriteria, len(raw))
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
		profiles[name] = criteria
	}
	return profiles, nil
}

// checkServiceProfileName checks that a setting of the hss section
// which names a service profile names one that the section defines. The
// setting is required where there are profiles, so that the HSS has a
// profile for every subscription it answers for.
func checkServiceProfileName(key, name string, profiles map[string][]ifc.InitialFilterCriteria) error {
	_, found := profiles[name]
	switch {
	case len(profiles) == 0 && name != "":
		return fmt.Errorf("%s: %q, but the node defines no service_profiles", key, name)
	case len(profiles) == 0:
		return nil
	case name == "":
		return fmt.Errorf("%s: required, one of %v", key, slices.Sorted(maps.Keys(profiles)))
	case !found:
		return fmt.Errorf("%s: %q is not a profile of service_profiles, which has %v", key, name, slices.Sorted(maps.Keys(profiles)))
	}
	return nil
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
