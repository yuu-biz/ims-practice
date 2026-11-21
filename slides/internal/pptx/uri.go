package pptx

import (
	"fmt"
	"strings"
)

// uriReference percent-encodes the address of a hyperlink for the
// target of a relationship, which is a URI: the schema of the
// relationships part in ECMA-376 Part 2 (Open Packaging Conventions,
// opc-relationships.xsd) gives Target the type xsd:anyURI. The links of
// the decks point at headings of the guides, and the anchors of
// Japanese headings are Japanese text. Every byte that RFC 3986 2.2 and
// 2.3 do not allow is written as %XX of its UTF-8 encoding (2.1): in the
// path, the query and the fragment alike (3.3 to 3.5). A "%" that
// already starts a %XX is kept, so an address that is already encoded
// stays as it is. The brackets are left alone only in the authority,
// where an IPv6 address needs them (3.2.2).
func uriReference(s string) string {
	var b strings.Builder
	rest := s
	if scheme, after, ok := strings.Cut(s, "://"); ok && isScheme(scheme) {
		end := strings.IndexAny(after, "/?#")
		if end < 0 {
			end = len(after)
		}
		b.WriteString(scheme + "://")
		for i := 0; i < end; i++ {
			writeURIByte(&b, after, &i, ":@!$&'()*+,;=[]")
		}
		rest = after[end:]
	}
	fragment := false
	for i := 0; i < len(rest); i++ {
		if rest[i] == '#' && !fragment {
			fragment = true
			b.WriteByte('#')
			continue
		}
		writeURIByte(&b, rest, &i, ":@!$&'()*+,;=/?")
	}
	return b.String()
}

// writeURIByte writes s[*i], encoded unless it is unreserved (RFC 3986
// 2.3), one of allowed, or the "%" of a %XX, which it writes whole.
func writeURIByte(b *strings.Builder, s string, i *int, allowed string) {
	c := s[*i]
	switch {
	case c == '%' && *i+2 < len(s) && isHex(s[*i+1]) && isHex(s[*i+2]):
		b.WriteString(s[*i : *i+3])
		*i += 2
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', strings.IndexByte("-._~", c) >= 0, strings.IndexByte(allowed, c) >= 0:
		b.WriteByte(c)
	default:
		fmt.Fprintf(b, "%%%02X", c)
	}
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// isScheme tells whether s is a scheme of RFC 3986 3.1.
func isScheme(s string) bool {
	if s == "" || !('a' <= s[0] && s[0] <= 'z' || 'A' <= s[0] && s[0] <= 'Z') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		if !('a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '+' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
