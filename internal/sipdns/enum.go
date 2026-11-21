package sipdns

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/miekg/dns"
)

// ENUMSuffix is the domain of the public ENUM tree (RFC 6116 3.2 step 4).
// A network can run ENUM under a private suffix instead (RFC 6116 2).
const ENUMSuffix = "e164.arpa"

// ErrNoENUMRecord means ENUM has no SIP URI for the number. The S-CSCF
// then treats the call as leaving the IMS.
var ErrNoENUMRecord = errors.New("sipdns: no ENUM record for the number")

// ApplicationUniqueString is RFC 6116 3.1: the E.164 number with every
// character but the digits and the leading "+" removed. It accepts the
// visual separators of a tel URI (RFC 3966 5.1.1).
func ApplicationUniqueString(number string) (string, error) {
	if !strings.HasPrefix(number, "+") {
		return "", fmt.Errorf("sipdns: %q is not a global number starting with +", number)
	}
	var b strings.Builder
	b.WriteByte('+')
	for _, c := range number[1:] {
		switch {
		case c >= '0' && c <= '9':
			b.WriteRune(c)
		case strings.ContainsRune("-.()", c):
		default:
			return "", fmt.Errorf("sipdns: %q has the character %q", number, c)
		}
	}
	if b.Len() < 2 {
		return "", fmt.Errorf("sipdns: %q has no digits", number)
	}
	return b.String(), nil
}

// ENUMDomain is the First Well Known Rule of RFC 6116 3.2: the digits of
// the AUS reversed, one label each, under suffix.
func ENUMDomain(aus, suffix string) string {
	digits := []byte(strings.TrimPrefix(aus, "+"))
	slices.Reverse(digits)
	labels := make([]string, len(digits))
	for i, d := range digits {
		labels[i] = string(d)
	}
	return strings.Join(labels, ".") + "." + dns.Fqdn(suffix)
}

// ENUM turns an E.164 number into the SIP URI its ENUM records name
// (RFC 6116 3 and 5.2): it asks for the NAPTR records of the number's
// domain, keeps the terminal ones for the "E2U+sip" service in order and
// preference order, and applies the first one's regular expression to
// the AUS.
//
// SIMPLIFIED: RFC 6116 5.2.1. A non-terminal NAPTR, whose flags are
// empty and whose result is another domain to look up, is skipped. The
// project's ENUM zone holds terminal records only.
func (r *Resolver) ENUM(ctx context.Context, number, suffix string) (string, error) {
	aus, err := ApplicationUniqueString(number)
	if err != nil {
		return "", err
	}
	answers, err := r.query(ctx, ENUMDomain(aus, suffix), dns.TypeNAPTR)
	if err != nil {
		return "", err
	}
	var records []*dns.NAPTR
	for _, rr := range answers {
		n, ok := rr.(*dns.NAPTR)
		// RFC 6116 3.4.2: the "u" flag marks a terminal rule whose result
		// is a URI. RFC 6116 3.4.3: the service names E2U and the
		// enumservice, here "sip".
		if ok && strings.EqualFold(n.Flags, "u") && hasEnumservice(n.Service, "sip") {
			records = append(records, n)
		}
	}
	slices.SortStableFunc(records, func(a, b *dns.NAPTR) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), cmp.Compare(a.Preference, b.Preference))
	})
	for _, n := range records {
		uri, err := substitute(unescape(n.Regexp), aus)
		if err != nil {
			r.log.Info("enum record skipped", "regexp", n.Regexp, "error", err.Error())
			continue
		}
		r.log.Info("enum result", "number", aus, "uri", uri)
		return uri, nil
	}
	return "", ErrNoENUMRecord
}

// hasEnumservice reports whether a NAPTR service field such as
// "E2U+sip" or "E2U+voice:sip" offers the enumservice (RFC 6116 3.4.3).
func hasEnumservice(service, want string) bool {
	parts := strings.Split(strings.ToLower(service), "+")
	if len(parts) < 2 || parts[0] != "e2u" {
		return false
	}
	for _, p := range parts[1:] {
		typ, sub, _ := strings.Cut(p, ":")
		if typ == want || sub == want {
			return true
		}
	}
	return false
}

// substitute applies a NAPTR regexp field, "<delim>ERE<delim>repl<delim>flags"
// (RFC 3402 3.2), to the AUS. The replacement may use \1 to \9 for the
// groups of the expression, and the flag "i" makes the match ignore case.
//
// SIMPLIFIED: RFC 3402 3.2. The delimiter may appear inside the
// expression or the replacement when escaped with a backslash; here
// every delimiter splits the field, so such a field is refused as
// malformed, or split in the wrong place. The records of this project
// use "!" and never escape it.
func substitute(field, aus string) (string, error) {
	if len(field) < 4 {
		return "", errors.New("regexp field too short")
	}
	delim := field[:1]
	parts := strings.Split(field[1:], delim)
	if len(parts) != 3 {
		return "", fmt.Errorf("regexp field %q is not <d>ERE<d>repl<d>flags", field)
	}
	expr, repl, flags := parts[0], parts[1], parts[2]
	if strings.Contains(flags, "i") {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return "", err
	}
	m := re.FindStringSubmatchIndex(aus)
	if m == nil {
		return "", errors.New("the expression does not match the number")
	}
	// RFC 3402 backreferences are \1 to \9; Go's Expand wants ${1}.
	template := regexp.MustCompile(`\\([1-9])`).ReplaceAllString(repl, "$${$1}")
	return string(re.ExpandString(nil, template, aus, m)), nil
}

// unescape undoes the escaping that miekg/dns applies to character
// strings in presentation format (RFC 1035 5.1): "\\" for a backslash,
// "\X" for X, and "\DDD" for a byte in decimal. The NAPTR regexp field
// is compared with the AUS as the raw bytes that were on the wire.
func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			b.WriteByte((s[i+1]-'0')*100 + (s[i+2]-'0')*10 + (s[i+3] - '0'))
			i += 3
			continue
		}
		b.WriteByte(s[i+1])
		i++
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
