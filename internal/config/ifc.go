package config

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/ifc"
)

// rawInitialFilterCriteria is one criterion of the hss section. The keys
// are the element names of TS 29.228 Annex E in snake case, and the
// session case and the default handling take the labels of Annex E,
// such as ORIGINATING_REGISTERED and SESSION_CONTINUED.
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

func parseInitialFilterCriteria(raw []rawInitialFilterCriteria) ([]ifc.InitialFilterCriteria, error) {
	var criteria []ifc.InitialFilterCriteria
	for i, r := range raw {
		c := ifc.InitialFilterCriteria{Priority: r.Priority, ApplicationServer: ifc.ApplicationServer{
			ServerName:  r.ApplicationServer.ServerName,
			ServiceInfo: r.ApplicationServer.ServiceInfo,
		}}
		if r.ApplicationServer.DefaultHandling != "" {
			d, err := ifc.ParseDefaultHandling(r.ApplicationServer.DefaultHandling)
			if err != nil {
				return nil, fmt.Errorf("initial_filter_criteria[%d].application_server.default_handling: %w", i, err)
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
						return nil, fmt.Errorf("initial_filter_criteria[%d].trigger_point.spt[%d].session_case: %w", i, j, err)
					}
					spt.SessionCase = &sc
				}
				c.TriggerPoint.SPT = append(c.TriggerPoint.SPT, spt)
			}
		}
		criteria = append(criteria, c)
	}
	if err := ifc.Validate(criteria); err != nil {
		return nil, fmt.Errorf("initial_filter_criteria: %w", err)
	}
	return criteria, nil
}
