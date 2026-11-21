package scscf

import (
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the caller preferences of RFC 3841: the S-CSCF matches
// the Accept-Contact and Reject-Contact of a request to a user against
// the feature parameters that the user's contact registered (TS 24.229
// 5.4.3.3 step 9). SMS over IP relies on it: the IP-SM-GW sends its
// MESSAGEs with "Accept-Contact: *;+g.3gpp.smsip;require;explicit", so
// that only a UE that registered +g.3gpp.smsip gets them (TS 24.341
// 5.3.3.4.2).
//
// SIMPLIFIED: RFC 3841 7.2.4 and 9.1, TS 24.229 5.4.3.3 step 10 a. The
// target set here is the one contact that the S-CSCF keeps for a private
// identity (5.4.1.2.2A step 6), so the matching only decides whether
// that contact stays in the set. The caller preference Qa, which orders
// the contacts that stay, and the q-values are not computed, and the
// Request-Disposition directives are not acted on: with one contact,
// fork and no-fork, parallel and sequential all reach the same contact.
// Redirect and queue, which no IMS procedure here uses, are ignored.

// featureTerm is one feature parameter as a term of an RFC 2533
// predicate (RFC 3841 8): the decoded feature tag, and the values it
// allows, any one of which matches.
type featureTerm struct {
	tag    string
	values []featureValue
}

// featureValue is one tag-value, or the string-value, of a feature
// parameter (RFC 3840 9).
type featureValue struct {
	text string
	// caseSensitive is set for a string-value, written "<...>"; tokens
	// and booleans compare without case (RFC 3841 7.2.4).
	caseSensitive bool
}

// baseTags are the feature tags that a feature parameter names without a
// leading "+" (RFC 3840 9, RFC 3841 7.2.1).
var baseTags = []string{"audio", "automata", "class", "duplex", "data", "control", "mobility", "description",
	"events", "priority", "methods", "extensions", "schemes", "application", "video", "language", "type",
	"isfocus", "actor", "text"}

// featureTerms extracts the feature parameters among params, in the
// form of RFC 3841 8; every other parameter, such as q or require, is
// left out (7.2.1, 7.2.3).
func featureTerms(params sip.Params) []featureTerm {
	var terms []featureTerm
	for _, p := range params {
		if tag, ok := featureTag(p.Name); ok {
			terms = append(terms, featureTerm{tag: tag, values: featureValues(p.Value)})
		}
	}
	return terms
}

// featureTag decodes the name of a feature parameter into its feature
// tag (RFC 3841 8): "+g.3gpp.smsip" is g.3gpp.smsip, "audio" is
// sip.audio. ok is false for a parameter that is no feature parameter.
func featureTag(name string) (string, bool) {
	name = strings.ToLower(name)
	if encoded, plus := strings.CutPrefix(name, "+"); plus {
		return strings.NewReplacer("!", ":", "'", "/").Replace(encoded), true
	}
	if slices.Contains(baseTags, name) {
		return "sip." + name, true
	}
	return "", false
}

// featureValues reads the value of a feature parameter. No value means
// TRUE; a quoted value is a string-value or a tag-value-list (RFC 3841
// 8).
//
// SIMPLIFIED: RFC 3841 8. A negated value ("!x") and a numeric one
// ("#>=4") are compared as plain tokens, not as the negation or the
// range that RFC 2533 makes of them. SMS over IP and the feature tags
// of TS 24.229 7.9 use neither.
func featureValues(value string) []featureValue {
	if value == "" {
		return []featureValue{{text: "TRUE"}}
	}
	value = sip.Unquote(value)
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		return []featureValue{{text: value[1 : len(value)-1], caseSensitive: true}}
	}
	var values []featureValue
	for _, v := range strings.Split(value, ",") {
		values = append(values, featureValue{text: strings.TrimSpace(v)})
	}
	return values
}

func (v featureValue) equal(w featureValue) bool {
	if v.caseSensitive || w.caseSensitive {
		return v.caseSensitive == w.caseSensitive && v.text == w.text
	}
	return strings.EqualFold(v.text, w.text)
}

// find returns the term of contact for tag, if it has one.
func find(contact []featureTerm, tag string) (featureTerm, bool) {
	for _, t := range contact {
		if t.tag == tag {
			return t, true
		}
	}
	return featureTerm{}, false
}

// matches is the matching operation of RFC 2533 between a preference
// and a contact predicate, for conjunctions of disjunctions of plain
// values: each term of the preference must share a value with the
// contact's term of the same tag. A tag the contact did not register
// does not constrain the contact, so it matches; the explicit flag of
// Accept-Contact is what tells such a contact apart (RFC 3841 7.2.4).
// present counts the terms whose tag the contact registered, from which
// RFC 3841 computes the score.
func matches(preference, contact []featureTerm) (match bool, present int) {
	match = true
	for _, want := range preference {
		have, ok := find(contact, want.tag)
		if !ok {
			continue
		}
		present++
		if !slices.ContainsFunc(want.values, func(v featureValue) bool {
			return slices.ContainsFunc(have.values, v.equal)
		}) {
			match = false
		}
	}
	return match, present
}

// acceptContact is one value of Accept-Contact (RFC 3841 10): the
// predicate and its require and explicit flags (7.2.1).
type acceptContact struct {
	terms    []featureTerm
	require  bool
	explicit bool
}

// parseContactPreference reads an ac-value or an rc-value, "*" followed
// by parameters (RFC 3841 10).
func parseContactPreference(value string) (sip.Params, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(value), "*")
	if !ok {
		return nil, false
	}
	return sip.ParseParams(rest), true
}

// meetsCallerPreferences tells whether the contact that registered
// contactParams stays in the target set of req (RFC 3841 7.2.3 and
// 7.2.4). A contact that registered no feature parameters is immune to
// caller preferences and always stays (7.2.3).
//
// Without Accept-Contact and Reject-Contact, RFC 3841 7.2.2 derives
// implicit preferences from the method and the Event. Those are never
// explicit, and when they leave no contact the original target set is
// used again (7.2.4), so with one contact they never take it away; they
// are not computed.
func meetsCallerPreferences(req *sip.Message, contactParams sip.Params) bool {
	contact := featureTerms(contactParams)
	if len(contact) == 0 {
		return true
	}
	// 7.2.4: a Reject-Contact predicate applies only to a contact that
	// registered every tag it names, and drops the contact it matches.
	for _, value := range req.GetAll("Reject-Contact") {
		params, ok := parseContactPreference(value)
		if !ok {
			continue
		}
		reject := featureTerms(params)
		if match, present := matches(reject, contact); len(reject) > 0 && present == len(reject) && match {
			return false
		}
	}
	for _, value := range req.GetAll("Accept-Contact") {
		params, ok := parseContactPreference(value)
		if !ok {
			continue
		}
		_, require := params.Get("require")
		_, explicit := params.Get("explicit")
		ac := acceptContact{terms: featureTerms(params), require: require, explicit: explicit}
		if !ac.admits(contact) {
			return false
		}
	}
	return true
}

// admits is RFC 3841 7.2.4 and its Figure 1 for one Accept-Contact
// predicate: a contact that does not match a required predicate is
// dropped, and so is one that matches it without having registered every
// tag of an explicit one (a score below 1). A predicate that is not
// required never drops the contact; it only scores it.
func (ac acceptContact) admits(contact []featureTerm) bool {
	if !ac.require {
		return true
	}
	match, present := matches(ac.terms, contact)
	if !match {
		return false
	}
	scoreBelowOne := present < len(ac.terms)
	return !(ac.explicit && scoreBelowOne)
}
