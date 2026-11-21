package sip

import (
	"fmt"
	"strings"
)

// NameAddr is one value of From, To, Contact, Route, Record-Route, Path,
// Service-Route or P-Associated-URI: a name-addr or an addr-spec,
// followed by header parameters (RFC 3261 20.10, 25.1).
//
//	"Alice" <sip:alice@example.org;transport=udp>;tag=1928301774
//
// URI is kept as a string so that any scheme fits, including the tel
// URI that P-Associated-URI carries in IMS. ParseURI reads a SIP URI
// from it.
type NameAddr struct {
	DisplayName string // without quotes; "" when there is none
	URI         string
	Params      Params
}

// ParseNameAddr reads one value. Without angle brackets, everything
// after the first ";" is a header parameter, not a URI parameter (RFC
// 3261 20). The Contact value "*" is not a NameAddr; a registrar checks
// for it before calling this.
func ParseNameAddr(value string) (NameAddr, error) {
	value = strings.TrimSpace(value)
	var na NameAddr
	var rest string
	if open := indexOutsideQuotes(value, '<'); open >= 0 {
		end := strings.IndexByte(value[open:], '>')
		if end < 0 {
			return NameAddr{}, fmt.Errorf("sip: unterminated <...> in %q", value)
		}
		na.DisplayName = Unquote(strings.TrimSpace(value[:open]))
		na.URI = value[open+1 : open+end]
		rest = value[open+end+1:]
	} else {
		na.URI, rest, _ = strings.Cut(value, ";")
		na.URI = strings.TrimSpace(na.URI)
		if rest != "" {
			rest = ";" + rest
		}
	}
	if na.URI == "" || !strings.Contains(na.URI, ":") {
		return NameAddr{}, fmt.Errorf("sip: no URI in %q", value)
	}
	rest = strings.TrimSpace(rest)
	if rest != "" && rest[0] != ';' {
		return NameAddr{}, fmt.Errorf("sip: unexpected %q after the address in %q", rest, value)
	}
	na.Params = parseParams(rest)
	return na, nil
}

// String always writes the name-addr form, which is valid wherever an
// addr-spec is.
func (na NameAddr) String() string {
	s := "<" + na.URI + ">" + na.Params.String()
	if na.DisplayName != "" {
		return Quote(na.DisplayName) + " " + s
	}
	return s
}

func indexOutsideQuotes(s string, c byte) int {
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch {
		case inQuotes && s[i] == '\\':
			i++
		case s[i] == '"':
			inQuotes = !inQuotes
		case !inQuotes && s[i] == c:
			return i
		}
	}
	return -1
}
