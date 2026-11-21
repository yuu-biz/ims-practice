package sip

import (
	"slices"
	"strings"
)

// compactForms maps each compact header name to its long form (RFC 3261
// 7.3.3; the letters are given in each header's subsection of 20).
var compactForms = map[string]string{
	"c": "Content-Type",
	"e": "Content-Encoding",
	"f": "From",
	"i": "Call-ID",
	"k": "Supported",
	"l": "Content-Length",
	"m": "Contact",
	"s": "Subject",
	"t": "To",
	"v": "Via",
	// RFC 6665 8.4
	"o": "Event",
	"u": "Allow-Events",
	// RFC 3841 9.1 and 9.2
	"a": "Accept-Contact",
	"j": "Reject-Contact",
	"d": "Request-Disposition",
	// RFC 4028 4
	"x": "Session-Expires",
	// RFC 3515 2.1 and RFC 3892 3
	"r": "Refer-To",
	"b": "Referred-By",
}

func expandCompactForm(name string) string {
	if long, ok := compactForms[strings.ToLower(name)]; ok {
		return long
	}
	return name
}

// listHeaders are the header fields whose value is a comma-separated
// list, so that several rows may be combined into one and one row may
// hold several values (RFC 3261 7.3.1). Any header not listed here is
// treated as a single value. Authorization and WWW-Authenticate contain
// commas but are not lists, which 7.3.1 calls out.
var listHeaders = map[string]bool{
	// RFC 3261 20
	"accept":           true,
	"accept-encoding":  true,
	"accept-language":  true,
	"alert-info":       true,
	"allow":            true,
	"call-info":        true,
	"contact":          true,
	"content-encoding": true,
	"content-language": true,
	"error-info":       true,
	"in-reply-to":      true,
	"proxy-require":    true,
	"record-route":     true,
	"require":          true,
	"route":            true,
	"supported":        true,
	"unsupported":      true,
	"via":              true,
	"warning":          true,
	// RFC 3327 4
	"path": true,
	// RFC 6665 8.4
	"allow-events": true,
	// RFC 3608 5
	"service-route": true,
	// RFC 3325 9.1
	"p-asserted-identity": true,
	// RFC 7315 5.1 and 5.3
	"p-associated-uri":     true,
	"p-visited-network-id": true,
	// RFC 3841 10
	"accept-contact":      true,
	"reject-contact":      true,
	"request-disposition": true,
	// RFC 3329 2.2
	"security-client": true,
	"security-server": true,
	"security-verify": true,
	// RFC 3326 2
	"reason": true,
	// RFC 5009 9
	"p-early-media": true,
	// RFC 7044 5
	"history-info": true,
}

func isListHeader(name string) bool { return listHeaders[strings.ToLower(name)] }

// splitList splits a comma-separated header value into its elements. A
// comma inside a quoted string or inside <...> belongs to the element:
// a display name or a URI may contain one (RFC 3261 20).
func splitList(value string) []string {
	var elements []string
	for _, e := range splitOutside(value, ',') {
		if e = strings.TrimSpace(e); e != "" {
			elements = append(elements, e)
		}
	}
	return elements
}

// splitOutside splits s at every sep that is neither inside a quoted
// string (with its backslash escapes, RFC 3261 25.1) nor inside <...>.
func splitOutside(s string, sep byte) []string {
	var parts []string
	inQuotes, inAngle := false, false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inQuotes:
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuotes = false
			}
		case inAngle:
			if c == '>' {
				inAngle = false
			}
		case c == '"':
			inQuotes = true
		case c == '<':
			inAngle = true
		case c == sep:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

// Param is one parameter of a header value or a URI, such as
// ";branch=z9hG4bK..." or ";lr". Value is written as received: a quoted
// string keeps its quotes, and a parameter without "=" has Value "".
type Param struct {
	Name  string
	Value string
}

// Params keeps parameters in their order. Names compare
// case-insensitively (RFC 3261 7.3.1).
type Params []Param

// ParseParams reads parameters written as "name=value;name", with or
// without a leading ";", such as the feature parameters of a Contact
// that a command line gives.
func ParseParams(s string) Params { return parseParams(s) }

// parseParams reads ";name=value;name..." as it follows a header value
// or a URI. SWS around "=" is allowed (RFC 3261 25.1 EQUAL).
func parseParams(s string) Params {
	var params Params
	for _, p := range splitOutside(s, ';') {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		name, value, _ := strings.Cut(p, "=")
		params = append(params, Param{Name: strings.TrimSpace(name), Value: strings.TrimSpace(value)})
	}
	return params
}

// Get returns the value of the named parameter and whether it is
// present. A present parameter without a value, such as "lr", gives "".
func (p Params) Get(name string) (string, bool) {
	for _, param := range p {
		if strings.EqualFold(param.Name, name) {
			return param.Value, true
		}
	}
	return "", false
}

// Set gives the named parameter a value, adding it at the end if it is
// not present. Value "" makes it a parameter without "=".
func (p *Params) Set(name, value string) {
	for i, param := range *p {
		if strings.EqualFold(param.Name, name) {
			(*p)[i].Value = value
			return
		}
	}
	*p = append(*p, Param{Name: name, Value: value})
}

// Remove deletes the named parameter.
func (p *Params) Remove(name string) {
	*p = slices.DeleteFunc(*p, func(param Param) bool { return strings.EqualFold(param.Name, name) })
}

// String writes each parameter as ";name=value", or ";name" when it has
// no value.
func (p Params) String() string {
	var b strings.Builder
	for _, param := range p {
		b.WriteString(";")
		b.WriteString(param.Name)
		if param.Value != "" {
			b.WriteString("=")
			b.WriteString(param.Value)
		}
	}
	return b.String()
}

// Unquote returns the content of a quoted-string, with its backslash
// escapes (quoted-pair) resolved (RFC 3261 25.1). A value without quotes
// is returned unchanged.
func Unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// Quote writes s as a quoted-string (RFC 3261 25.1).
func Quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
