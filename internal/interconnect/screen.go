package interconnect

import (
	"maps"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the screening of SIP signalling (TS 24.229 5.10.6):
// which methods, option tags and header fields cross the border.

// ScreenMethod rejects a request whose method the II-NNI with this
// peer does not support: one that TS 29.165 Table 6.1 makes optional
// and the agreement leaves out, or one that is not applicable there.
// Table 6.3 lets the IBCF discard such a request; it answers 405
// (Method Not Allowed) with the methods it allows here instead, as TS
// 24.229 5.10.1 permits, so that the caller learns why.
func ScreenMethod(req *sip.Message, p Profile) error {
	allowed := p.allowedMethods()
	if slices.Contains(allowed, req.Method) {
		return nil
	}
	names := make([]string, len(allowed))
	for i, m := range allowed {
		names[i] = string(m)
	}
	return reject(405, ErrMethodNotAllowed, sip.HeaderField{Name: "Allow", Value: strings.Join(names, ", ")})
}

// ApplyOptionTags applies the profile's option tag policies to the
// Supported, Require and Proxy-Require header fields of a request. The
// tags in a response follow from those of its request, so responses
// are left alone.
func ApplyOptionTags(req *sip.Message, p Profile) error {
	for _, tag := range slices.Sorted(maps.Keys(p.OptionTags)) {
		required := slices.Contains(req.GetAll("Require"), tag) || slices.Contains(req.GetAll("Proxy-Require"), tag)
		switch p.OptionTags[tag] {
		case OptionRemove:
			removeElement(req, "Supported", tag)
			removeElement(req, "Require", tag)
			removeElement(req, "Proxy-Require", tag)
		case OptionReject:
			if required {
				return reject(420, ErrExtensionNotAllowed, sip.HeaderField{Name: "Unsupported", Value: tag})
			}
		}
	}
	return nil
}

// notApplicable are the header fields that cannot cross a non-roaming
// II-NNI, so the IBCF removes them in both directions.
var notApplicable = []string{
	// TS 29.165 6.1.1.3.3: only on a roaming II-NNI.
	"Authentication-Info", "Authorization", "P-Associated-URI",
	"P-Called-Party-ID", "P-Preferred-Service", "P-Profile-Key",
	"P-Served-User", "P-Visited-Network-ID", "Path", "Priority-Share",
	"Proxy-Authenticate", "Proxy-Authorization", "Resource-Share",
	"Restoration-Info", "Service-Route", "WWW-Authenticate",
	// TS 29.165 Table 6.1.3.1: n/a at the II-NNI. Item 22 is
	// P-Media-Authorization, item 26 P-Preferred-Identity, item 37
	// P-Charging-Function-Addresses (also TS 24.229 5.10.2.2 step 9
	// and the responses of 5.10.3.2), and item 39 the security
	// mechanism agreement of RFC 3329.
	"P-Media-Authorization", "P-Preferred-Identity",
	"P-Charging-Function-Addresses",
	"Security-Client", "Security-Server", "Security-Verify",
	// TS 29.165 Table 6.2 NOTE 4.
	"Relayed-Charge",
}

// RemoveNotApplicable removes the header fields that a non-roaming
// II-NNI does not carry. It applies to requests and responses in both
// directions.
func RemoveNotApplicable(m *sip.Message) {
	for _, name := range notApplicable {
		m.Remove(name)
	}
}

// ApplyHeaderRules applies the operator's rules for a message going in
// direction d (TS 24.229 5.10.6.2). Pass rules do nothing here; they
// change what ApplyTrustDomain removes.
func ApplyHeaderRules(m *sip.Message, p Profile, d Direction) {
	for _, r := range p.Headers {
		if r.Direction != "" && r.Direction != d {
			continue
		}
		values := m.GetAll(r.Header)
		if len(values) == 0 {
			continue
		}
		switch {
		case r.Action == HeaderRemove && r.Param == "":
			m.Remove(r.Header)
		case r.Action == HeaderRewrite && r.Param == "":
			replaceValues(m, r.Header, []string{r.Value})
		case r.Action == HeaderRemove:
			for i, v := range values {
				values[i] = editParams(v, func(p *sip.Params) { p.Remove(r.Param) })
			}
			replaceValues(m, r.Header, values)
		case r.Action == HeaderRewrite:
			for i, v := range values {
				values[i] = editParams(v, func(p *sip.Params) { p.Set(r.Param, r.Value) })
			}
			replaceValues(m, r.Header, values)
		}
	}
}

// replaceValues puts values in place of every row of the named header
// field, one row each, where the first row was. A list header field may
// hold its values in one row or in several (RFC 3261 7.3.1), so one row
// per value fits every header field. No values removes the field.
func replaceValues(m *sip.Message, name string, values []string) {
	at := slices.IndexFunc(m.Headers, func(h sip.HeaderField) bool { return strings.EqualFold(h.Name, name) })
	if at < 0 {
		at = len(m.Headers)
	} else {
		name = m.Headers[at].Name
	}
	kept := slices.DeleteFunc(slices.Clone(m.Headers[at:]), func(h sip.HeaderField) bool { return strings.EqualFold(h.Name, name) })
	rows := make([]sip.HeaderField, len(values))
	for i, v := range values {
		rows[i] = sip.HeaderField{Name: name, Value: v}
	}
	m.Headers = slices.Concat(m.Headers[:at], rows, kept)
}

// removeElement removes one element, such as an option tag, from a list
// header field.
func removeElement(m *sip.Message, name, element string) {
	values := m.GetAll(name)
	if !slices.Contains(values, element) {
		return
	}
	replaceValues(m, name, slices.DeleteFunc(values, func(v string) bool { return v == element }))
}

// editParams changes the header field parameters of one header field
// value. The value is a base, such as a name-addr, followed by
// ";name=value" parameters; a value such as that of P-Charging-Vector
// has parameters only.
func editParams(value string, edit func(*sip.Params)) string {
	base, params := splitParams(value)
	edit(&params)
	if base == "" {
		return strings.TrimPrefix(params.String(), ";")
	}
	return base + params.String()
}

// splitParams splits a header field value into its base and its
// parameters. The base ends at the first ";" outside quotes and angle
// brackets. When the text before that ";" is itself a name=value pair,
// as in "icid-value=1;orig-ioi=a" (RFC 7315 5.6), the value has no
// base.
func splitParams(value string) (string, sip.Params) {
	at := indexOutside(value, ';')
	base, rest := value, ""
	if at >= 0 {
		base, rest = value[:at], value[at:]
	}
	if indexOutside(base, '=') >= 0 {
		return "", sip.ParseParams(value)
	}
	return strings.TrimSpace(base), sip.ParseParams(rest)
}

// indexOutside is the index of the first sep that is neither inside a
// quoted string (with its backslash escapes, RFC 3261 25.1) nor inside
// <...>, or -1.
func indexOutside(s string, sep byte) int {
	inQuotes, inAngle := false, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inQuotes:
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuotes = false
			}
		case inAngle:
			inAngle = c != '>'
		case c == '"':
			inQuotes = true
		case c == '<':
			inAngle = true
		case c == sep:
			return i
		}
	}
	return -1
}

// isInDialog reports whether a request belongs to a dialog: its To has
// a tag (RFC 3261 12.2).
func isInDialog(req *sip.Message) bool {
	to, err := sip.ParseNameAddr(req.Get("To"))
	if err != nil {
		return false
	}
	_, ok := to.Params.Get("tag")
	return ok
}
