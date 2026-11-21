// Package ifc is the initial filter criteria of the IMS user profile
// (TS 29.228 Annex B.2.2 and B.2.3, schema in Annex E): which requests
// of a user the S-CSCF sends to which application server, in what
// order, and what it does when the server cannot be reached. The HSS
// sends them inside the User-Data of the SAA (TS 29.228 6.1.2), and the
// S-CSCF evaluates them (TS 23.218 5.2, TS 24.229 5.4.3.2 and 5.4.3.3).
//
// The struct tags are the element names of the schema, so the types
// marshal to and from the XML of the User-Data as they are.
//
// SIMPLIFIED: TS 29.228 Annex E. Only the parts that SMS uses are
// modelled. Unknown elements are skipped when the XML is read:
// ProfilePartIndicator, and the Extension elements that carry
// RegistrationType, IncludeRegisterRequest and IncludeRegisterResponse.
// TS 29.228 B.2.3 makes RegistrationType optional for the S-CSCF; one
// that does not support it matches a REGISTER SPT to every REGISTER, as
// this package does. The SessionDescription SPT is refused by Validate.
package ifc

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// InitialFilterCriteria is one filter criterion (TS 29.228 B.2.2): when
// TriggerPoint matches a request, the S-CSCF sends the request to
// ApplicationServer.
type InitialFilterCriteria struct {
	// Priority orders the criteria: the smaller number is assessed
	// first. No two criteria of a user share a priority.
	Priority int `xml:"Priority"`
	// TriggerPoint nil means the criterion matches every request.
	TriggerPoint      *TriggerPoint     `xml:"TriggerPoint,omitempty"`
	ApplicationServer ApplicationServer `xml:"ApplicationServer"`
}

// TriggerPoint is a boolean expression over SPTs (TS 29.228 B.2.2 and
// Annex C). In conjunctive normal form (CNF) it is an AND of groups,
// each the OR of its SPTs; in disjunctive normal form (DNF) an OR of
// groups, each the AND of its SPTs.
type TriggerPoint struct {
	ConditionTypeCNF bool  `xml:"ConditionTypeCNF"`
	SPT              []SPT `xml:"SPT"`
}

// SPT is a Service Point Trigger (TS 29.228 B.2.3): one condition on the
// request. Exactly one of RequestURI, Method, SIPHeader and SessionCase
// is set; the schema makes them a choice.
type SPT struct {
	// ConditionNegated turns the condition into its NOT.
	ConditionNegated bool `xml:"ConditionNegated"`
	// Group says which groups of the TriggerPoint the SPT belongs to.
	// An SPT may be in more than one.
	Group []int `xml:"Group"`
	// RequestURI is a regular expression on the host and port of a SIP
	// URI, or on the number of a tel URI (TS 29.228 Annex F).
	RequestURI string `xml:"RequestURI,omitempty"`
	// Method is the name of a SIP method.
	Method      string       `xml:"Method,omitempty"`
	SIPHeader   *SIPHeader   `xml:"SIPHeader,omitempty"`
	SessionCase *SessionCase `xml:"SessionCase,omitempty"`
}

// SIPHeader is the SPT on a header field (TS 29.228 B.2.3 and Annex
// F). Header is a regular expression on the header name, which is
// matched without case. Content, when set, is a regular expression on
// a value of that header. Without Content the SPT is about the
// presence of the header, or, negated, its absence.
type SIPHeader struct {
	Header  string `xml:"Header"`
	Content string `xml:"Content,omitempty"`
}

// ApplicationServer is the server that a matching criterion sends the
// request to (TS 29.228 B.2.2).
type ApplicationServer struct {
	// ServerName is the SIP URI of the server.
	ServerName      string          `xml:"ServerName"`
	DefaultHandling DefaultHandling `xml:"DefaultHandling"`
	// ServiceInfo goes to the server unchanged, in the body of the
	// third-party REGISTER (TS 24.229 5.4.1.7A).
	ServiceInfo string `xml:"ServiceInfo,omitempty"`
}

// SessionCase is the direction of the request as seen from the served
// user: the tDirectionOfRequest of TS 29.228 Annex E, Table E.1.
type SessionCase int

const (
	OriginatingRegistered   SessionCase = 0
	TerminatingRegistered   SessionCase = 1
	TerminatingUnregistered SessionCase = 2
	OriginatingUnregistered SessionCase = 3
	OriginatingCDIV         SessionCase = 4
)

// sessionCases are the labels that Annex E gives the values.
var sessionCases = []string{"ORIGINATING_REGISTERED", "TERMINATING_REGISTERED", "TERMINATING_UNREGISTERED",
	"ORIGINATING_UNREGISTERED", "ORIGINATING_CDIV"}

func (c SessionCase) String() string {
	if c < 0 || int(c) >= len(sessionCases) {
		return fmt.Sprintf("SessionCase(%d)", int(c))
	}
	return sessionCases[c]
}

// ParseSessionCase reads the label of a session case, such as
// ORIGINATING_REGISTERED.
func ParseSessionCase(label string) (SessionCase, error) {
	if i := slices.Index(sessionCases, label); i >= 0 {
		return SessionCase(i), nil
	}
	return 0, fmt.Errorf("ifc: unknown session case %q", label)
}

// DefaultHandling is what the S-CSCF does when the application server
// cannot be reached: the tDefaultHandling of TS 29.228 Annex E, Table
// E.1. When a criterion gives none, TS 24.229 5.4.3.2 acts as for
// SessionContinued, which is the zero value.
type DefaultHandling int

const (
	// SessionContinued goes on with the criteria of lower priority.
	SessionContinued DefaultHandling = 0
	// SessionTerminated ends the request.
	SessionTerminated DefaultHandling = 1
)

var defaultHandlings = []string{"SESSION_CONTINUED", "SESSION_TERMINATED"}

func (d DefaultHandling) String() string {
	if d < 0 || int(d) >= len(defaultHandlings) {
		return fmt.Sprintf("DefaultHandling(%d)", int(d))
	}
	return defaultHandlings[d]
}

// ParseDefaultHandling reads the label of a default handling, such as
// SESSION_CONTINUED.
func ParseDefaultHandling(label string) (DefaultHandling, error) {
	if i := slices.Index(defaultHandlings, label); i >= 0 {
		return DefaultHandling(i), nil
	}
	return 0, fmt.Errorf("ifc: unknown default handling %q", label)
}

// Ordered is the ordered list of TS 24.229 5.4.3.2 step 3 and 5.4.3.3
// step 2A: the criteria in the order of their priority, smallest number
// first. criteria itself is left as it is.
func Ordered(criteria []InitialFilterCriteria) []InitialFilterCriteria {
	ordered := slices.Clone(criteria)
	slices.SortStableFunc(ordered, func(a, b InitialFilterCriteria) int { return cmp.Compare(a.Priority, b.Priority) })
	return ordered
}

// HasSessionCase reports whether one of criteria is meant for session
// case sc: its trigger point has an SPT of sc that is not negated. The
// HSS asks it to know whether a user has services for the unregistered
// state (TS 29.228 6.1.4.1 step 3), and the S-CSCF to choose between
// the options of TS 24.229 5.4.3.3 step 3, which NOTE 6 there bases on
// the criteria.
//
// A criterion without an SPT on the session case matches in every case,
// but it does not count here: then every user with the REGISTER
// criterion of SMS would have services for the unregistered state. A
// negated one does not count either, although it matches the other
// cases (TS 29.228 B.2.3 NOTE).
func HasSessionCase(criteria []InitialFilterCriteria, sc SessionCase) bool {
	for _, c := range criteria {
		if c.TriggerPoint == nil {
			continue
		}
		for _, spt := range c.TriggerPoint.SPT {
			if spt.SessionCase != nil && *spt.SessionCase == sc && !spt.ConditionNegated {
				return true
			}
		}
	}
	return false
}

// Matches reports whether the criterion applies to req in the session
// case sc. A criterion without a trigger point applies to every request
// (TS 29.228 B.2.2).
func (c InitialFilterCriteria) Matches(req *sip.Message, sc SessionCase) bool {
	if c.TriggerPoint == nil {
		return true
	}
	return c.TriggerPoint.matches(req, sc)
}

// matches evaluates the trigger point on req in the session case sc.
func (t TriggerPoint) matches(req *sip.Message, sc SessionCase) bool {
	groups := map[int][]bool{}
	for _, spt := range t.SPT {
		result := spt.matches(req, sc) != spt.ConditionNegated
		for _, g := range spt.Group {
			groups[g] = append(groups[g], result)
		}
	}
	if t.ConditionTypeCNF {
		// Every group needs one SPT that holds.
		for _, results := range groups {
			if !slices.Contains(results, true) {
				return false
			}
		}
		return true
	}
	// One group needs all of its SPTs to hold.
	for _, results := range groups {
		if !slices.Contains(results, false) {
			return true
		}
	}
	return false
}

// matches evaluates the condition of the SPT on req in the session case
// sc, before ConditionNegated is applied.
//
// The regular expressions are Extended Regular Expressions (TS 29.228
// Annex F). As with POSIX regexec, a pattern matches when it matches
// any part of the text; ^ and $ anchor it.
func (s SPT) matches(req *sip.Message, sc SessionCase) bool {
	switch {
	case s.Method != "":
		// Method names compare with case: the grammar of RFC 3261 25.1
		// spells them in capitals with %x codes.
		return string(req.Method) == s.Method
	case s.SessionCase != nil:
		return *s.SessionCase == sc
	case s.RequestURI != "":
		return ereMatches(s.RequestURI, requestURIText(req.RequestURI))
	case s.SIPHeader != nil:
		return s.SIPHeader.matches(req)
	}
	return false
}

// matches is Annex F for the SIP header SPT: every header of req whose
// name matches Header is tried, and each value of a comma-separated
// list on its own. One that matches is enough.
//
// Annex F matches Header against the header-name of RFC 3261, and
// header names compare without case (RFC 3261 7.3.1), so "content-type"
// is the same header as "Content-Type": the pattern is matched without
// case, as regcomp does with REG_ICASE. A compact form such as "c" is
// already its long form here, because the sip package expands it
// (RFC 3261 7.3.3). The Content pattern is matched with case: what a
// value means depends on the header, and the pattern has to allow for
// it, such as the media type of Content-Type, whose case does not
// matter (RFC 2045 5.1).
func (h SIPHeader) matches(req *sip.Message) bool {
	var names []string
	for _, f := range req.Headers {
		if ereMatchesWithoutCase(h.Header, f.Name) && !slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, f.Name) }) {
			names = append(names, f.Name)
		}
	}
	if h.Content == "" {
		return len(names) > 0
	}
	for _, name := range names {
		for _, value := range req.GetAll(name) {
			if ereMatches(h.Content, value) {
				return true
			}
		}
	}
	return false
}

// requestURIText is the part of a Request-URI that Annex F matches: the
// hostport of a SIP URI, the telephone-subscriber of a tel URI (RFC
// 3966 3). Any other URI has no such part.
func requestURIText(uri string) string {
	if number, ok := strings.CutPrefix(uri, "tel:"); ok {
		return number
	}
	u, err := sip.ParseURI(uri)
	if err != nil {
		return ""
	}
	host := u.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // an IPv6 reference (RFC 3261 25.1)
	}
	if u.Port != 0 {
		return fmt.Sprintf("%s:%d", host, u.Port)
	}
	return host
}

// ereMatches reports whether the Extended Regular Expression pattern
// matches text. Validate has compiled every pattern of a valid
// criterion, so an error here only means no match.
func ereMatches(pattern, text string) bool {
	re, err := regexp.CompilePOSIX(pattern)
	return err == nil && re.MatchString(text)
}

// ereMatchesWithoutCase is ereMatches with upper and lower case letters
// taken as the same. Go's POSIX syntax has no flag for it, but on a
// text without line breaks, such as a header name, every Extended
// Regular Expression that CompilePOSIX takes means the same in Go's
// default syntax, where the (?i) flag exists. MatchString does not
// depend on which of the leftmost matches each syntax prefers.
func ereMatchesWithoutCase(pattern, text string) bool {
	if _, err := regexp.CompilePOSIX(pattern); err != nil {
		return false
	}
	re, err := regexp.Compile("(?i:" + pattern + ")")
	return err == nil && re.MatchString(text)
}

// Validate checks the rules of TS 29.228 Annex B.2.2, B.2.3 and E that
// the Go types do not enforce, for the criteria of one service profile.
// The HSS calls it on its configuration and the S-CSCF on the profile it
// receives.
func Validate(criteria []InitialFilterCriteria) error {
	priorities := map[int]bool{}
	for i, c := range criteria {
		if err := c.validate(); err != nil {
			return fmt.Errorf("InitialFilterCriteria[%d]: %w", i, err)
		}
		// B.2.2: the same priority is not given to two criteria.
		if priorities[c.Priority] {
			return fmt.Errorf("InitialFilterCriteria[%d]: Priority %d appears twice", i, c.Priority)
		}
		priorities[c.Priority] = true
	}
	return nil
}

func (c InitialFilterCriteria) validate() error {
	if c.Priority < 0 {
		return errors.New("Priority: below 0")
	}
	if _, err := sip.ParseURI(c.ApplicationServer.ServerName); err != nil {
		return fmt.Errorf("ServerName: %w", err)
	}
	if d := c.ApplicationServer.DefaultHandling; d != SessionContinued && d != SessionTerminated {
		return fmt.Errorf("DefaultHandling: %d is not a value", int(d))
	}
	if c.TriggerPoint == nil {
		return nil
	}
	if len(c.TriggerPoint.SPT) == 0 {
		return errors.New("TriggerPoint: no SPT")
	}
	for i, s := range c.TriggerPoint.SPT {
		if err := s.validate(); err != nil {
			return fmt.Errorf("SPT[%d]: %w", i, err)
		}
	}
	return nil
}

func (s SPT) validate() error {
	if len(s.Group) == 0 {
		return errors.New("Group: at least one is required")
	}
	for _, g := range s.Group {
		if g < 0 {
			return errors.New("Group: below 0")
		}
	}
	var patterns []string
	set := 0
	if s.RequestURI != "" {
		set++
		patterns = append(patterns, s.RequestURI)
	}
	if s.Method != "" {
		set++
	}
	if s.SIPHeader != nil {
		set++
		if s.SIPHeader.Header == "" {
			return errors.New("SIPHeader: Header is required")
		}
		patterns = append(patterns, s.SIPHeader.Header, s.SIPHeader.Content)
	}
	if s.SessionCase != nil {
		set++
		if *s.SessionCase < OriginatingRegistered || *s.SessionCase > OriginatingCDIV {
			return fmt.Errorf("SessionCase: %d is not a value", int(*s.SessionCase))
		}
	}
	if set != 1 {
		// An SPT on SessionDescription leaves all four unset.
		return errors.New("exactly one of RequestURI, Method, SIPHeader and SessionCase is required; SessionDescription is not supported")
	}
	for _, p := range patterns {
		if _, err := regexp.CompilePOSIX(p); err != nil {
			return fmt.Errorf("%q is not an Extended Regular Expression: %w", p, err)
		}
	}
	return nil
}
