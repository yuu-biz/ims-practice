package simservs

import "encoding/xml"

// Ruleset is the <cp:ruleset> of RFC 4745 13. Communication diversion
// evaluates the rules in document order and stops at the first match
// (TS 24.604 4.9.1.2); communication barring combines all matching
// rules (TS 24.611 4.9.1.2, RFC 4745 10.2). See EvaluateDiversion and
// EvaluateBarring.
type Ruleset struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:common-policy ruleset"`
	Rules   []Rule   `xml:"urn:ietf:params:xml:ns:common-policy rule"`
}

// Rule is the ruleType of RFC 4745 13. The XCAP client addresses one
// rule by its id (TS 24.604 4.9.1.2, TS 24.611 4.9.1.3), for example
// simservs/communication-diversion/cp:ruleset/cp:rule[@id="cfu"].
//
// The <cp:transformations> element is not modelled: no supplementary
// service defines a transformation.
type Rule struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:common-policy rule"`
	// ID is an xs:ID: unique in the whole document and an NCName.
	ID         string      `xml:"id,attr"`
	Conditions *Conditions `xml:"urn:ietf:params:xml:ns:common-policy conditions"`
	Actions    *Actions    `xml:"urn:ietf:params:xml:ns:common-policy actions"`
}

// Conditions is the conditionsType of RFC 4745 13 with the conditions
// that TS 24.623 6.3 adds to the simservs namespace. All conditions of
// a rule must be true for the rule to match (RFC 4745 10.1). A rule
// without conditions, or with an empty <cp:conditions>, always matches:
// that is how an unconditional rule (CFU, BAIC, BAOC) is written.
//
// Which conditions a service accepts is checked by Validate: diversion
// takes those of TS 24.604 4.9.1.3, barring those of TS 24.611 4.9.1.4.
type Conditions struct {
	Identity *Identity `xml:"urn:ietf:params:xml:ns:common-policy identity"`

	Anonymous             *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap anonymous"`
	Media                 []string `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap media"`
	CommunicationDiverted *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap communication-diverted"`
	RuleDeactivated       *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap rule-deactivated"`
	NotRegistered         *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap not-registered"`
	Busy                  *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap busy"`
	NoAnswer              *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap no-answer"`
	NotReachable          *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap not-reachable"`
	Roaming               *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap roaming"`
	International         *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap international"`
	InternationalExHC     *Empty   `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap international-exHC"`

	// Unknown names the conditions that are not modelled. They evaluate
	// to false (RFC 4745 7).
	//
	// SIMPLIFIED: TS 24.604 4.9.1.3, TS 24.611 4.9.1.4. The conditions
	// cp:sphere, cp:validity, ss:presence-status, ocp:external-list and
	// ocp:other-identity are not supported. IR.92 2.3.8 and 2.3.9 do not
	// ask for them, there is no presence server and no shared list
	// server, and Validate refuses them so that a user never stores a
	// rule that cannot match.
	Unknown []UnknownElement `xml:",any"`
}

// Identity is the identityType of RFC 4745 7.1. It is true when any of
// its children matches the identity of the other party (RFC 4745
// 7.1.1).
type Identity struct {
	One  []One  `xml:"urn:ietf:params:xml:ns:common-policy one"`
	Many []Many `xml:"urn:ietf:params:xml:ns:common-policy many"`

	// Unknown names children of other namespaces, which evaluate to
	// false (RFC 4745 7.1.1).
	Unknown []UnknownElement `xml:",any"`
}

// One matches exactly one identity, given as a URI (RFC 4745 7.1.2,
// 7.2), for example tel:+12025550123.
type One struct {
	ID string `xml:"id,attr"`
}

// Many matches every identity, or every identity of a domain, except
// those listed (RFC 4745 7.1.3).
type Many struct {
	Domain string   `xml:"domain,attr,omitempty"`
	Except []Except `xml:"urn:ietf:params:xml:ns:common-policy except"`
}

// Except takes one identity or one domain out of a Many (RFC 4745
// 7.1.3).
type Except struct {
	Domain string `xml:"domain,attr,omitempty"`
	ID     string `xml:"id,attr,omitempty"`
}

// Actions is the extensibleType of RFC 4745 13 holding the actions of a
// rule: <forward-to> for diversion (TS 24.604 4.9.1.4) and <allow> for
// barring (TS 24.611 4.9.1.5).
type Actions struct {
	ForwardTo *ForwardTo `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap forward-to"`
	Allow     *bool      `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap allow"`

	Unknown []UnknownElement `xml:",any"`
}
