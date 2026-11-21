package h248

import (
	"fmt"
	"slices"
	"strings"
)

// AuditDescriptor says what the reply to a command is to report (RFC
// 3525 7.1.12). An empty one asks for nothing: with Subtract it keeps
// the media gateway from sending statistics (TS 29.334 5.8.3), and
// with AuditValue on ROOT it only checks that the media gateway
// answers (TS 29.334 5.12, Table 5.12.3).
type AuditDescriptor struct {
	// AuditToken lists the descriptors to report in full.
	AuditToken []AuditToken
	// AuditPropertyToken lists single items to report. This came with
	// version 2 of H.248.1.
	AuditPropertyToken []IndAuditParameter
}

// AuditToken names a descriptor to report. The value is the token of
// the descriptor.
type AuditToken string

const (
	MediaToken          AuditToken = "Media"
	EventsToken         AuditToken = "Events"
	SignalsToken        AuditToken = "Signals"
	ObservedEventsToken AuditToken = "ObservedEvents"
	StatsToken          AuditToken = "Statistics"
	PackagesToken       AuditToken = "Packages"
)

// IndAuditParameter names one item of the TerminationState descriptor
// to report: either the service state or one property. It is written
// as "Media { TerminationState { ServiceStates } }" or, for a
// property, "Media { TerminationState { root/maxNumberOfContexts } }".
// The profiles audit the ROOT termination this way (TS 29.334
// 5.17.3.10, Table 5.17.3.10.1).
//
// SIMPLIFIED: H.248.1 (05/2002) with Corrigendum 1 (03/2004), Annex
// B.2 (indAudterminationAudit). Of the items that can be
// audited one by one, only those of the TerminationState descriptor
// are covered. The ones of the LocalControl, Events, Signals,
// Statistics and Packages descriptors are left out: Iq and Ix audit
// none of them, and Mp audits one optional property of LocalControl
// (TS 29.333 5.8.5). The descriptors can be audited in full with
// AuditToken instead.
type IndAuditParameter struct {
	// ServiceStates asks for the service state. Otherwise PkgdName
	// names the property.
	ServiceStates bool
	PkgdName      PkgdName
}

// PackagesDescriptor lists the packages that a termination realizes
// (RFC 3525 7.1.16). It comes back from an AuditValue that asks for
// it.
type PackagesDescriptor []PackagesItem

// PackagesItem is one package and its version, written as "g-1".
type PackagesItem struct {
	PackageName    string
	PackageVersion uint16
}

// StatisticsDescriptor reports the statistics of a termination (RFC
// 3525 7.1.15), such as the octets sent. It comes back from Subtract
// and from an audit.
type StatisticsDescriptor []StatisticsParameter

// StatisticsParameter is one statistic of a package. StatValue is the
// zero value when the statistic is named without a value.
type StatisticsParameter struct {
	StatName  PkgdName
	StatValue Value
}

// auditDescriptor reads what follows the Audit token:
// "{ [ auditItem, ... ] }".
func (s *scanner) auditDescriptor() (*AuditDescriptor, error) {
	a := &AuditDescriptor{}
	err := s.listOrEmpty(func() error {
		name, err := s.token()
		if err != nil {
			return err
		}
		token := AuditToken(name)
		if !isAuditToken(token) {
			return s.errorf("auditing %s is not supported", name)
		}
		s.skipLWSP()
		switch {
		case s.peek() != '{' && s.peek() != '=':
			if slices.Contains(a.AuditToken, token) {
				return s.errorf("%s appears twice in an %s descriptor", name, tokAudit)
			}
			a.AuditToken = append(a.AuditToken, token)
			return nil
		case token != MediaToken:
			return s.errorf("auditing single items of the %s descriptor is not supported", name)
		}
		p, err := s.indAudMediaDescriptor()
		a.AuditPropertyToken = append(a.AuditPropertyToken, p)
		return err
	})
	return a, err
}

// indAudMediaDescriptor reads what follows the Media token in an Audit
// descriptor: "{ TerminationState { ServiceStates / pkgdName } }".
func (s *scanner) indAudMediaDescriptor() (IndAuditParameter, error) {
	var p IndAuditParameter
	if err := s.expect('{'); err != nil {
		return p, err
	}
	name, err := s.token()
	if err != nil {
		return p, err
	}
	if name != tokTerminationState {
		return p, s.errorf("auditing single items of the %s descriptor is not supported", name)
	}
	if err := s.expect('{'); err != nil {
		return p, err
	}
	text, pkgd, err := s.pkgdNameOrWord()
	if err != nil {
		return p, err
	}
	if pkgd {
		p.PkgdName = PkgdName(text)
	} else if name, _ := longToken(text); name == tokServiceStates {
		p.ServiceStates = true
	} else {
		return p, s.errorf("%q cannot be audited in a %s descriptor", text, tokTerminationState)
	}
	if err := s.expect('}'); err != nil {
		return p, err
	}
	return p, s.expect('}')
}

func isAuditToken(token AuditToken) bool {
	switch token {
	case MediaToken, EventsToken, SignalsToken, ObservedEventsToken, StatsToken, PackagesToken:
		return true
	}
	return false
}

func (w *writer) auditDescriptor(a *AuditDescriptor) {
	var items []func()
	for i, token := range a.AuditToken {
		if !isAuditToken(token) {
			w.fail("%q cannot be audited", string(token))
		}
		if slices.Contains(a.AuditToken[:i], token) {
			w.fail("%s appears twice in an %s descriptor", token, tokAudit)
		}
		items = append(items, func() { w.item(string(token)) })
	}
	for _, p := range a.AuditPropertyToken {
		item := tokServiceStates
		switch {
		case p.ServiceStates && p.PkgdName != "":
			w.fail("an item to audit is either the service state or a property")
		case !p.ServiceStates:
			if !validPkgdName(p.PkgdName) {
				w.fail("%q is not the name of a package item", string(p.PkgdName))
			}
			item = string(p.PkgdName)
		}
		items = append(items, func() { w.item(fmt.Sprintf("%s { %s { %s } }", tokMedia, tokTerminationState, item)) })
	}
	if len(items) == 0 {
		w.item(tokAudit + " { }")
		return
	}
	w.block(tokAudit, items)
}

// packagesDescriptor reads what follows the Packages token:
// "{ packagesItem, ... }".
func (s *scanner) packagesDescriptor() (PackagesDescriptor, error) {
	var p PackagesDescriptor
	err := s.list(func() error {
		item := PackagesItem{PackageName: s.word()}
		if !validName(item.PackageName) {
			return s.errorf("expected the name of a package, found %s", s.found())
		}
		if s.peek() != '-' {
			return s.errorf("expected \"-\" and the version of the package, found %s", s.found())
		}
		s.pos++
		version, err := s.uint(16)
		item.PackageVersion = uint16(version)
		p = append(p, item)
		return err
	})
	return p, err
}

func (w *writer) packagesDescriptor(p PackagesDescriptor) {
	if len(p) == 0 {
		w.fail("a %s descriptor is empty", tokPackages)
	}
	items := make([]string, len(p))
	for i, item := range p {
		if !validName(item.PackageName) {
			w.fail("%q is not the name of a package", item.PackageName)
		}
		items[i] = fmt.Sprintf("%s-%d", item.PackageName, item.PackageVersion)
	}
	w.item(fmt.Sprintf("%s { %s }", tokPackages, strings.Join(items, ", ")))
}

// statisticsDescriptor reads what follows the Statistics token:
// "{ statisticsParameter, ... }".
func (s *scanner) statisticsDescriptor() (StatisticsDescriptor, error) {
	var d StatisticsDescriptor
	err := s.list(func() error {
		var p StatisticsParameter
		var err error
		if p.StatName, err = s.pkgdName(); err != nil {
			return err
		}
		if s.eat('=') {
			p.StatValue, err = s.value()
		}
		d = append(d, p)
		return err
	})
	return d, err
}

func (w *writer) statisticsDescriptor(d StatisticsDescriptor) {
	parms := make([]string, len(d))
	for i, p := range d {
		if !validPkgdName(p.StatName) {
			w.fail("%q is not the name of a package item", string(p.StatName))
		}
		parms[i] = string(p.StatName)
		if p.StatValue != (Value{}) {
			parms[i] += " = " + w.value(p.StatValue)
		}
	}
	w.parmBlock(tokStatistics, parms)
}
