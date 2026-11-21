package simservs

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

// validator collects the checks that an XCAP server makes on a document
// after a PUT or DELETE (RFC 4825 8.2.5). The checks are those of the
// schemas this package implements and of the data semantics of the
// service specifications. The first failure is kept.
type validator struct {
	err error
	// ruleIDs maps each rule id to the node selector of its rule. The id
	// of a rule is an xs:ID (RFC 4745 13), which XML Schema makes unique
	// in the whole document, not only in one rule set.
	ruleIDs map[string]string
}

func (v *validator) fail(err *ConflictError) {
	if v.err == nil {
		v.err = err
	}
}

// selectorQuery is the query component that binds the prefix cp used in
// the node selectors of the reports (RFC 4825 6.4, 11.1).
const selectorQuery = "?xmlns(cp=" + NamespaceCommonPolicy + ")"

// conditionNames lists which conditions a service accepts, by the local
// name of the element.
type conditionNames map[string]bool

func (v *validator) ruleset(service string, rs *Ruleset, allowed conditionNames, actions func(path string, a *Actions)) {
	if rs == nil {
		return
	}
	for i, r := range rs.Rules {
		path := fmt.Sprintf("simservs/%s/cp:ruleset/cp:rule[%d]", service, i+1)
		if !isNCName(r.ID) {
			v.fail(conflict(SchemaValidationError, "%s: id %q is not an NCName", path, r.ID))
			continue
		}
		if first, dup := v.ruleIDs[r.ID]; dup {
			v.fail(&ConflictError{
				Condition: UniquenessFailure,
				Phrase:    fmt.Sprintf("rule id %q is already used by %s", r.ID, first),
				Field:     percentEncodeSelector(path+"/@id") + selectorQuery,
			})
			continue
		}
		v.ruleIDs[r.ID] = path
		if r.Conditions != nil {
			v.conditions(path, r.Conditions, allowed)
			if r.Conditions.Identity != nil {
				v.unknown(path+"/cp:conditions/cp:identity", r.Conditions.Identity.Unknown)
			}
		}
		if r.Actions != nil {
			v.unknown(path+"/cp:actions", r.Actions.Unknown)
			actions(path, r.Actions)
		}
	}
}

func (v *validator) conditions(path string, c *Conditions, allowed conditionNames) {
	for _, name := range c.names() {
		if !allowed[name] {
			v.fail(conflict(ConstraintFailure, "%s: condition <%s> is not allowed in this service", path, name))
		}
	}
	if c.Identity != nil {
		// The identityType needs at least one child (RFC 4745 13).
		if len(c.Identity.One) == 0 && len(c.Identity.Many) == 0 && len(c.Identity.Unknown) == 0 {
			v.fail(conflict(SchemaValidationError, "%s: <cp:identity> is empty", path))
		}
	}
	for _, u := range c.Unknown {
		switch u.Name.Space {
		case NamespaceSimservs, NamespaceCommonPolicy, NamespaceOMACommonPolicy:
			if unsupportedConditions[u.Name.Local] {
				v.fail(conflict(ConstraintFailure, "%s: condition <%s> is not supported", path, u.Name.Local))
			} else {
				v.fail(conflict(SchemaValidationError, "%s: unknown condition <%s>", path, u.Name.Local))
			}
		}
		// Conditions of other namespaces are allowed by the schema and
		// evaluate to false (RFC 4745 7, RFC 4825 8.2.5).
	}
}

// unsupportedConditions are defined by the schemas but not supported
// here; see Conditions.Unknown.
var unsupportedConditions = map[string]bool{
	"sphere":          true,
	"validity":        true,
	"presence-status": true,
	"external-list":   true,
	"other-identity":  true,
}

// unknown refuses elements of the simservs and common policy namespaces
// that no schema of this package defines. This server understands those
// namespaces, so it must have a schema for every element in them (RFC
// 4825 8.2.5). Elements of other namespaces are allowed.
func (v *validator) unknown(path string, us []UnknownElement) {
	for _, u := range us {
		if u.Name.Space == NamespaceSimservs || u.Name.Space == NamespaceCommonPolicy {
			v.fail(conflict(SchemaValidationError, "%s: unknown element <%s>", path, u.Name.Local))
		}
	}
}

// names lists the conditions that are present, by local name.
func (c *Conditions) names() []string {
	var out []string
	add := func(present bool, name string) {
		if present {
			out = append(out, name)
		}
	}
	add(c.Identity != nil, "identity")
	add(c.Anonymous != nil, "anonymous")
	add(len(c.Media) > 0, "media")
	add(c.CommunicationDiverted != nil, "communication-diverted")
	add(c.RuleDeactivated != nil, "rule-deactivated")
	add(c.NotRegistered != nil, "not-registered")
	add(c.Busy != nil, "busy")
	add(c.NoAnswer != nil, "no-answer")
	add(c.NotReachable != nil, "not-reachable")
	add(c.Roaming != nil, "roaming")
	add(c.International != nil, "international")
	add(c.InternationalExHC != nil, "international-exHC")
	return out
}

// isNCName tells whether s is a non-colonised name (Namespaces in XML
// 1.0, section 3), which an xs:ID has to be. The check covers the
// ASCII range exactly and lets other letters through.
func isNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case i > 0 && (r == '-' || r == '.' || unicode.IsDigit(r)):
		default:
			return false
		}
	}
	return true
}

// isURI tells whether s has the form of an absolute URI, such as a SIP
// or tel URI.
func isURI(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme != "" && !strings.ContainsAny(s, " \t\r\n")
}

// percentEncodeSelector encodes the characters of a node selector that
// cannot appear in the path of an HTTP URI (RFC 4825 6.3).
func percentEncodeSelector(s string) string {
	return strings.NewReplacer(`[`, "%5B", `]`, "%5D", `"`, "%22", ` `, "%20").Replace(s)
}
