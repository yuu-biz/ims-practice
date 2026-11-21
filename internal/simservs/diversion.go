package simservs

import "encoding/xml"

// CommunicationDiversion is the <communication-diversion> service
// element of TS 24.604 4.9.2: the settings of CFU, CFB, CFNR, CFNRc and
// CFNL. Each of those services is a rule of the rule set, told apart by
// its conditions (TS 24.604 4.9.1.3):
//
//	CFU    no condition (a rule without conditions always matches)
//	CFB    <busy/>
//	CFNR   <no-answer/>
//	CFNRc  <not-reachable/>
//	CFNL   <not-registered/>
//
// The operator puts the rules in the first document with
// <rule-deactivated/>, and the user activates one by removing that
// condition and setting the target (GSMA IR.92 2.3.2).
type CommunicationDiversion struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap communication-diversion"`
	SimservType

	// NoReplyTimer is the time in seconds the AS waits for an answer
	// before CFNR diverts the call (TS 24.604 4.9.1.1A), from 5 to 180.
	// When it is absent the operator default applies (TS 24.604 4.8.1);
	// see CompleteCommunicationDiversion.NoReplyTimer.
	NoReplyTimer *int `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap NoReplyTimer"`
	Ruleset      *Ruleset
}

// ForwardTo is the forward-to-type of TS 24.604 4.9.2, the action of a
// diversion rule (TS 24.604 4.9.1.4). The optional elements are
// pointers, so that an absent element and its default stay apart; the
// methods give the value that applies.
type ForwardTo struct {
	// Target is a SIP or tel URI. An empty target means that the service
	// is provisioned but has no number yet (TS 24.604 4.9.1.4).
	Target                           string            `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap target"`
	NotifyCaller                     *bool             `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap notify-caller"`
	RevealIdentityToCaller           *RevealURIOptions `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap reveal-identity-to-caller"`
	RevealServedUserIdentityToCaller *RevealURIOptions `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap reveal-served-user-identity-to-caller"`
	NotifyServedUser                 *bool             `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap notify-served-user"`
	NotifyServedUserOnOutboundCall   *bool             `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap notify-served-user-on-outbound-call"`
	RevealIdentityToTarget           *RevealURIOptions `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap reveal-identity-to-target"`
}

// RevealURIOptions is the reveal-URI-options-type of TS 24.604 4.9.2.
type RevealURIOptions string

const (
	RevealFalse         RevealURIOptions = "false"
	RevealNotRevealGRUU RevealURIOptions = "not-reveal-GRUU"
	RevealTrue          RevealURIOptions = "true"
)

func (r RevealURIOptions) valid() bool {
	return r == RevealFalse || r == RevealNotRevealGRUU || r == RevealTrue
}

func boolOr(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func revealOr(r *RevealURIOptions) RevealURIOptions {
	if r == nil {
		return RevealTrue
	}
	return *r
}

// The defaults below are those of the schema (TS 24.604 4.9.2).

// NotifyCallerOrDefault tells whether the caller learns of the diversion
// (a 181 response, TS 24.604 4.5.2.6.4).
func (f ForwardTo) NotifyCallerOrDefault() bool { return boolOr(f.NotifyCaller, true) }

// NotifyServedUserOrDefault tells whether the served user is told that
// a call was diverted.
func (f ForwardTo) NotifyServedUserOrDefault() bool { return boolOr(f.NotifyServedUser, false) }

// NotifyServedUserOnOutboundCallOrDefault tells whether the served user
// is reminded of the diversion when calling.
func (f ForwardTo) NotifyServedUserOnOutboundCallOrDefault() bool {
	return boolOr(f.NotifyServedUserOnOutboundCall, false)
}

// RevealIdentityToCallerOrDefault tells whether the caller sees the
// diverted-to party in the notification.
func (f ForwardTo) RevealIdentityToCallerOrDefault() RevealURIOptions {
	return revealOr(f.RevealIdentityToCaller)
}

// RevealServedUserIdentityToCallerOrDefault tells whether the caller
// sees the diverting user in the notification.
func (f ForwardTo) RevealServedUserIdentityToCallerOrDefault() RevealURIOptions {
	return revealOr(f.RevealServedUserIdentityToCaller)
}

// RevealIdentityToTargetOrDefault tells whether the diverted-to party
// sees the diverting user.
func (f ForwardTo) RevealIdentityToTargetOrDefault() RevealURIOptions {
	return revealOr(f.RevealIdentityToTarget)
}

// diversionConditions are the conditions of TS 24.604 4.9.1.3 that this
// package supports. GSMA IR.92 2.3.8 asks for busy, media, no-answer,
// not-registered, not-reachable and rule-deactivated; identity and
// anonymous are part of the same clause and cost nothing more.
var diversionConditions = conditionNames{
	"busy":             true,
	"not-registered":   true,
	"identity":         true,
	"anonymous":        true,
	"media":            true,
	"no-answer":        true,
	"rule-deactivated": true,
	"not-reachable":    true,
}

// NoReplyTimer bounds of the schema (TS 24.604 4.9.2).
const (
	MinNoReplyTimer = 5
	MaxNoReplyTimer = 180
)

func (v *validator) diversion(cd *CommunicationDiversion) {
	if cd == nil {
		return
	}
	if cd.NoReplyTimer != nil && (*cd.NoReplyTimer < MinNoReplyTimer || *cd.NoReplyTimer > MaxNoReplyTimer) {
		v.fail(conflict(SchemaValidationError, "NoReplyTimer %d is not between %d and %d", *cd.NoReplyTimer, MinNoReplyTimer, MaxNoReplyTimer))
	}
	v.ruleset("communication-diversion", cd.Ruleset, diversionConditions, func(path string, a *Actions) {
		if a.Allow != nil {
			v.fail(conflict(ConstraintFailure, "%s: <allow> is an action of communication barring", path))
		}
		f := a.ForwardTo
		if f == nil {
			// A rule with empty actions stops the evaluation without a
			// diversion (TS 24.604 4.9.1.4).
			return
		}
		if f.Target != "" && !isURI(f.Target) {
			v.fail(conflict(SchemaValidationError, "%s: target %q is not a URI", path, f.Target))
		}
		for _, r := range []*RevealURIOptions{f.RevealIdentityToCaller, f.RevealServedUserIdentityToCaller, f.RevealIdentityToTarget} {
			if r != nil && !r.valid() {
				v.fail(conflict(SchemaValidationError, "%s: %q is not a reveal-URI-options-type", path, *r))
			}
		}
	})
}
