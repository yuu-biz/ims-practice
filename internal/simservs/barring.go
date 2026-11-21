package simservs

import "encoding/xml"

// IncomingCommunicationBarring is the <incoming-communication-barring>
// service element of TS 24.611 4.9.2. BAIC is a rule without
// conditions, BIC-Roam a rule with <roaming/>, and ACR a rule with
// <anonymous/> (TS 29.364 7.2.7 treats ACR as part of ICB). Each rule
// has <allow>false</allow> as its action; a rule with
// <allow>true</allow> makes an exception list (TS 24.611 4.9.1.3).
type IncomingCommunicationBarring struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap incoming-communication-barring"`
	CommunicationBarring
}

// OutgoingCommunicationBarring is the <outgoing-communication-barring>
// service element of TS 24.611 4.9.2. BAOC is a rule without
// conditions, BOIC a rule with <international/>, and BOIC-exHC a rule
// with <international-exHC/>.
type OutgoingCommunicationBarring struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap outgoing-communication-barring"`
	CommunicationBarring
}

// CommunicationBarring is the content that the two barring elements
// share (TS 24.611 4.9.2).
type CommunicationBarring struct {
	SimservType
	// PasswordRequired tells whether a change needs the password of TS
	// 24.623 5.3.1.2.1. The default is false. Checking the password is
	// the XCAP server's part (TS 24.623 5.3.2.5); this package only
	// carries the attribute.
	PasswordRequired *bool `xml:"password-required,attr,omitempty"`
	Ruleset          *Ruleset
}

// barringConditions are the conditions of TS 24.611 4.9.1.4 that this
// package supports. GSMA IR.92 2.3.9 asks for roaming, international,
// international-exHC and rule-deactivated; anonymous is ACR, and
// identity, media and communication-diverted are part of the same
// clause and cost nothing more.
var barringConditions = conditionNames{
	"identity":               true,
	"anonymous":              true,
	"media":                  true,
	"communication-diverted": true,
	"roaming":                true,
	"rule-deactivated":       true,
	"international":          true,
	"international-exHC":     true,
}

func (v *validator) barring(service string, cb *CommunicationBarring) {
	if cb == nil {
		return
	}
	v.ruleset(service, cb.Ruleset, barringConditions, func(path string, a *Actions) {
		if a.ForwardTo != nil {
			v.fail(conflict(ConstraintFailure, "%s: <forward-to> is an action of communication diversion", path))
		}
	})
}
