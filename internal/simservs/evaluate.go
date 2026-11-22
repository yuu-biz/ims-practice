package simservs

import (
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallInfo is what the MMTel AS knows about a call when it evaluates
// the rules of the served user. The AS fills it from the SIP request,
// the registration state and the configuration; the evaluation itself
// does not look at SIP.
type CallInfo struct {
	// CallingIdentity is the URI of the calling user, taken from
	// P-Asserted-Identity (TS 24.604 4.9.1.3, TS 24.611 4.5.2.6.1), for
	// example tel:+12025550123. It is empty when the request has none.
	CallingIdentity string
	// CallingIdentityRestricted is true when the Privacy header field
	// holds id, header or user (TS 24.611 4.5.2.6.2).
	CallingIdentityRestricted bool
	// CalledIdentity is the Request-URI of the outgoing request, against
	// which outgoing barring evaluates cp:identity and the international
	// conditions (TS 24.611 4.5.2.4.1, 4.9.1.4).
	CalledIdentity string
	// Media are the media fields of the m= lines of the SDP offer, such
	// as audio and video (TS 24.604 4.9.1.3, TS 24.611 4.9.1.4).
	Media []string
	// CommunicationDiverted is true when the incoming call was diverted
	// before, which History-Info shows (TS 24.611 4.9.1.4 NOTE 2).
	CommunicationDiverted bool
	// ServedUserNotRegistered is true when the served user has no
	// registration, the condition of CFNL.
	ServedUserNotRegistered bool
	// ServedUserRoaming is true when the served user registered from an
	// access network other than its home network, which the AS learns
	// from P-Visited-Network-ID and P-Access-Network-Info of the
	// registration (TS 24.611 4.9.1.4 NOTE 3).
	ServedUserRoaming bool
	// HomeCountryCode is the E.164 country code of the served user's
	// home network, digits only, such as 1. Without it the international
	// conditions are false.
	HomeCountryCode string
	// VisitedCountryCode is the country code of the country where the
	// served user is when calling; empty means the home country.
	VisitedCountryCode string
}

// DiversionEvent is the moment of the call at which the AS evaluates
// the diversion rules. Rules with the conditions busy, no-answer and
// not-reachable are evaluated only at their event; all the others when
// the call arrives (TS 24.604 4.9.1.1, 4.9.1.3).
type DiversionEvent int

const (
	// AtCommunicationSetup is the arrival of the initial INVITE: CFU,
	// CFNL, and rules with only other conditions.
	AtCommunicationSetup DiversionEvent = iota
	// OnBusy is a busy indication, such as a 486 response: CFB.
	OnBusy
	// OnNoAnswer is the expiry of the no reply timer: CFNR.
	OnNoAnswer
	// OnNotReachable is the loss of the signalling path to the UE during
	// setup, such as a 500 response before any provisional response
	// other than 100: CFNRc (TS 24.604 4.9.1.3, 4.5.2.6.6).
	OnNotReachable
)

// DiversionResult is the outcome of EvaluateDiversion.
type DiversionResult struct {
	// Rule is the rule that matched, nil when none did.
	Rule *Rule
	// ForwardTo is the action of that rule. It is nil when the rule has
	// empty actions, which ends the evaluation without a diversion (TS
	// 24.604 4.9.1.4).
	ForwardTo *ForwardTo
}

// Diverts tells whether the call is to be diverted: a rule matched and
// its target is set. An empty target means the service is provisioned
// but not set up (TS 24.604 4.9.1.4).
func (r DiversionResult) Diverts() bool { return r.ForwardTo != nil && r.ForwardTo.Target != "" }

// EvaluateDiversion runs the communication diversion rules of the
// served user for one event of a call. Among the rules that apply to
// the event, the first one in document order whose conditions all hold
// matches, and the rules after it are not looked at (TS 24.604
// 4.9.1.1, 4.9.1.2). A service that is not active diverts nothing (TS
// 24.623 6.1).
//
// Evaluating OnNoAnswer when the call arrives tells the AS whether a
// CFNR rule can match this call, and so whether it needs to run the no
// reply timer at all.
func EvaluateDiversion(cd *CommunicationDiversion, event DiversionEvent, call CallInfo) DiversionResult {
	if cd == nil || !cd.IsActive() || cd.Ruleset == nil {
		return DiversionResult{}
	}
	ev := evaluation{
		call:  call,
		event: event,
		// TS 24.604 4.9.1.3: the calling user's P-Asserted-Identity is
		// not provided or privacy restricted.
		anonymous: call.CallingIdentity == "" || call.CallingIdentityRestricted,
		identity:  call.CallingIdentity,
	}
	for i := range cd.Ruleset.Rules {
		r := &cd.Ruleset.Rules[i]
		if !appliesAt(r.Conditions, event) || !ev.holds(r.Conditions) {
			continue
		}
		result := DiversionResult{Rule: r}
		if r.Actions != nil {
			result.ForwardTo = r.Actions.ForwardTo
		}
		return result
	}
	return DiversionResult{}
}

// appliesAt tells whether a rule takes part in the evaluation at event
// (TS 24.604 4.9.1.1).
func appliesAt(c *Conditions, event DiversionEvent) bool {
	var busy, noAnswer, notReachable bool
	if c != nil {
		busy, noAnswer, notReachable = c.Busy != nil, c.NoAnswer != nil, c.NotReachable != nil
	}
	switch event {
	case OnBusy:
		return busy
	case OnNoAnswer:
		return noAnswer
	case OnNotReachable:
		return notReachable
	default:
		return !busy && !noAnswer && !notReachable
	}
}

// BarringResult is the outcome of EvaluateIncomingBarring and
// EvaluateOutgoingBarring.
type BarringResult struct {
	// Allow is the combined allow action. When it is false the AS
	// rejects the call: with 603 (TS 24.611 4.5.2.4.1, 4.5.2.6.1), or
	// with 433 when AnonymousCommunicationRejection is set.
	Allow bool
	// MatchedRules are the ids of the rules whose conditions held.
	MatchedRules []string
	// AnonymousCommunicationRejection is true when the call is barred
	// and a rule with the anonymous condition barred it: ACR, answered
	// with 433 (Anonymity Disallowed) (TS 24.611 4.5.2.6.2).
	AnonymousCommunicationRejection bool
}

// EvaluateIncomingBarring runs the incoming barring rules (ICB, ACR) of
// the served user for an incoming call. cp:identity is matched against
// the calling identity (TS 24.611 4.5.2.6.1). The international
// conditions are about outgoing requests and are false here.
func EvaluateIncomingBarring(icb *IncomingCommunicationBarring, call CallInfo) BarringResult {
	if icb == nil {
		return BarringResult{Allow: true}
	}
	return evaluateBarring(&icb.CommunicationBarring, evaluation{
		call:     call,
		event:    AtCommunicationSetup,
		identity: call.CallingIdentity,
		// TS 24.611 4.5.2.6.2: P-Asserted-Identity is present and the
		// Privacy header field holds id, header or user.
		anonymous: call.CallingIdentity != "" && call.CallingIdentityRestricted,
	})
}

// EvaluateOutgoingBarring runs the outgoing barring rules (OCB) of the
// served user for an outgoing call. cp:identity is matched against the
// called identity (TS 24.611 4.5.2.4.1). The anonymous condition is
// about the calling party and is false here.
//
// Calls to emergency services are never barred (TS 24.611 4.5.2.4.1).
// The AS does not evaluate OCB for them, or the operator adds an allow
// rule for the emergency numbers.
func EvaluateOutgoingBarring(ocb *OutgoingCommunicationBarring, call CallInfo) BarringResult {
	if ocb == nil {
		return BarringResult{Allow: true}
	}
	return evaluateBarring(&ocb.CommunicationBarring, evaluation{
		call:     call,
		event:    AtCommunicationSetup,
		identity: call.CalledIdentity,
		outgoing: true,
	})
}

// evaluateBarring combines every matching rule (TS 24.611 4.9.1.3, RFC
// 4745 10.2): the call is allowed when no rule matches or when one of
// the matching rules allows it. A matching rule without an allow action
// counts as false, the lowest value of a boolean permission (RFC 4745
// 10.2).
func evaluateBarring(cb *CommunicationBarring, ev evaluation) BarringResult {
	result := BarringResult{Allow: true}
	if !cb.IsActive() || cb.Ruleset == nil {
		return result
	}
	allow, anonymous := false, false
	for _, r := range cb.Ruleset.Rules {
		if !ev.holds(r.Conditions) {
			continue
		}
		result.MatchedRules = append(result.MatchedRules, r.ID)
		if r.Actions != nil && r.Actions.Allow != nil && *r.Actions.Allow {
			allow = true
		} else if r.Conditions != nil && r.Conditions.Anonymous != nil {
			anonymous = true
		}
	}
	if len(result.MatchedRules) > 0 {
		result.Allow = allow
	}
	result.AnonymousCommunicationRejection = !result.Allow && anonymous
	return result
}

// evaluation holds what the conditions are evaluated against.
type evaluation struct {
	call  CallInfo
	event DiversionEvent
	// identity is the other party that cp:identity matches.
	identity  string
	anonymous bool
	// outgoing is set for outgoing barring, where the international
	// conditions apply.
	outgoing bool
}

// holds tells whether all conditions of a rule are true (RFC 4745
// 10.1). A rule without conditions always holds.
func (ev evaluation) holds(c *Conditions) bool {
	if c == nil {
		return true
	}
	if len(c.Unknown) > 0 || c.RuleDeactivated != nil {
		// An unknown condition is false (RFC 4745 7), and
		// rule-deactivated is always false (TS 24.604 4.9.1.3, TS 24.611
		// 4.9.1.4).
		return false
	}
	checks := []struct {
		present, value bool
	}{
		{c.Identity != nil, c.Identity != nil && matchIdentity(c.Identity, ev.identity)},
		{c.Anonymous != nil, ev.anonymous},
		{len(c.Media) > 0, offersAll(ev.call.Media, c.Media)},
		{c.CommunicationDiverted != nil, ev.call.CommunicationDiverted},
		{c.NotRegistered != nil, ev.call.ServedUserNotRegistered},
		{c.Busy != nil, ev.event == OnBusy},
		{c.NoAnswer != nil, ev.event == OnNoAnswer},
		{c.NotReachable != nil, ev.event == OnNotReachable},
		{c.Roaming != nil, ev.call.ServedUserRoaming},
		{c.International != nil, ev.outgoing && international(ev.call)},
		{c.InternationalExHC != nil, ev.outgoing && internationalExHC(ev.call)},
	}
	for _, ch := range checks {
		if ch.present && !ch.value {
			return false
		}
	}
	return true
}

// offersAll tells whether every media condition matches the media field
// of one of the m= lines; several media conditions in a rule all have
// to hold.
func offersAll(offered, wanted []string) bool {
	for _, w := range wanted {
		if !slices.ContainsFunc(offered, func(o string) bool { return strings.EqualFold(o, w) }) {
			return false
		}
	}
	return true
}

// international is the condition of TS 24.611 4.9.1.4: the Request-URI
// is a telephone number of a country other than the one where the
// served user is.
//
// SIMPLIFIED: TS 24.611 4.9.1.4. The country of a number is read from
// its country code, so countries that share a code (the NANP countries
// under 1) count as one. A number without a country code (a local number
// with phone-context) is taken to be in the country of the served user.
// Number plans per country are out of scope.
func international(call CallInfo) bool {
	number, ok := telephoneNumber(call.CalledIdentity)
	if !ok || call.HomeCountryCode == "" || !strings.HasPrefix(number, "+") {
		return false
	}
	located := call.VisitedCountryCode
	if located == "" {
		located = call.HomeCountryCode
	}
	return !strings.HasPrefix(number[1:], located)
}

// internationalExHC is international, except towards the served user's
// home (TS 24.611 4.9.1.4).
//
// SIMPLIFIED: TS 24.611 4.9.1.4 speaks of destinations served within
// the home network. Without number ranges per network, the home network
// is taken to be the home country.
func internationalExHC(call CallInfo) bool {
	if !international(call) {
		return false
	}
	number, _ := telephoneNumber(call.CalledIdentity)
	return !strings.HasPrefix(number[1:], call.HomeCountryCode)
}

// matchIdentity evaluates cp:identity: true when one of its children
// matches (RFC 4745 7.1.1). Only an identity the AS knows can match, so
// an empty identity matches nothing.
func matchIdentity(id *Identity, identity string) bool {
	if identity == "" {
		return false
	}
	for _, one := range id.One {
		if sameIdentity(one.ID, identity) {
			return true
		}
	}
	for _, many := range id.Many {
		if many.Domain != "" && !strings.EqualFold(many.Domain, domainOf(identity)) {
			continue
		}
		excepted := slices.ContainsFunc(many.Except, func(e Except) bool {
			return (e.ID != "" && sameIdentity(e.ID, identity)) ||
				(e.Domain != "" && strings.EqualFold(e.Domain, domainOf(identity)))
		})
		if !excepted {
			return true
		}
	}
	return false
}

// sameIdentity compares two identities as URIs. A tel URI and a SIP URI
// with user=phone are the same identity when their numbers are the same,
// so that a rule written with either form matches a P-Asserted-Identity
// of either form.
//
// SIMPLIFIED: RFC 3261 19.1.4, RFC 3966 4. Numbers are compared with
// their visual separators removed and their parameters ignored; other
// SIP URIs by scheme, user and host, with the host in any case and the
// parameters ignored. The full comparison rules are not needed for the
// identities that an IMS network asserts.
func sameIdentity(a, b string) bool {
	na, okA := telephoneNumber(a)
	nb, okB := telephoneNumber(b)
	if okA || okB {
		return okA && okB && na == nb
	}
	ua, errA := sip.ParseURI(a)
	ub, errB := sip.ParseURI(b)
	return errA == nil && errB == nil && ua.Scheme == ub.Scheme && ua.User == ub.User && strings.EqualFold(ua.Host, ub.Host)
}

// telephoneNumber returns the number of a tel URI, or of a SIP URI with
// user=phone, in the form that two numbers compare in: without visual
// separators, so +12025550123 for tel:+1-202-555-0123. A number of
// separators alone is no number.
func telephoneNumber(uri string) (string, bool) {
	n, ok := ims.ParseTelephoneNumber(uri)
	if !ok {
		return "", false
	}
	number := ims.WithoutVisualSeparators(n.Number)
	if number == "" || number == "+" {
		return "", false
	}
	return number, true
}

// domainOf returns the host of a SIP URI, the domain that cp:many
// compares (RFC 4745 7.1.3). A tel URI has none.
//
// SIMPLIFIED: RFC 4745 7.1.3. Domains are compared without
// percent-decoding and without the ToASCII step for internationalized
// names; IMS domains are ASCII.
func domainOf(identity string) string {
	u, err := sip.ParseURI(identity)
	if err != nil {
		return ""
	}
	return u.Host
}
