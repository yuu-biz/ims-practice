package interconnect

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the format of telephone numbers at the II-NNI (TS
// 29.165 8.1). A number in the Request-URI or in P-Asserted-Identity
// crosses a non-roaming II-NNI as a global number (RFC 3966 5.1.4),
// either in a tel URI or in the user part of a SIP URI with user=phone,
// unless the operators agree on local numbers too. Global numbers let
// the terminating network find the subscriber in the HSS without
// translating the number (8.1 NOTE 5).

// NumberFormat is the URI form that carries a telephone number.
type NumberFormat string

const (
	// NumberFormatTel is the tel URI of RFC 3966: tel:+12025550151.
	NumberFormatTel NumberFormat = "tel"
	// NumberFormatSIP is a SIP URI whose user part is the number and
	// which has user=phone (RFC 3261 19.1.6):
	// sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone.
	// TS 29.165 8.1 makes this the form every non-roaming II-NNI
	// supports, and the tel URI one by agreement.
	NumberFormatSIP NumberFormat = "sip"
)

// ErrLocalNumber reports a local number that no LocalNumberPlan makes
// global and that the agreement does not allow as it is.
var ErrLocalNumber = errors.New("interconnect: a local number the agreement does not allow")

// visualSeparators are the characters a phone number may have for
// readability only (RFC 3966 5.1.1).
var visualSeparators = strings.NewReplacer("-", "", ".", "", "(", "", ")", "")

// NormalizeNumber writes a URI that holds a telephone number in format:
// a tel URI, or a SIP URI with user=phone. A local number becomes
// global through the profile's LocalNumbers, and is kept as it is only
// when the profile allows local numbers. host is the host of the SIP
// URI that a tel URI turns into. Any other URI comes back unchanged.
// The empty format keeps the scheme.
//
// The number loses its visual separators. The parameters of the number,
// such as npdi and rn (RFC 4694), stay with it, in the tel URI or in
// the user part (RFC 3261 19.1.6).
func NormalizeNumber(uri string, format NumberFormat, host string, p Profile) (string, error) {
	n, ok := parseNumber(uri)
	if !ok {
		return uri, nil
	}
	n.number = visualSeparators.Replace(n.number)
	if !strings.HasPrefix(n.number, "+") {
		context, _ := n.params.Get("phone-context")
		i := slices.IndexFunc(p.LocalNumbers, func(l LocalNumberPlan) bool { return sameContext(l.PhoneContext, context) })
		switch {
		case i >= 0:
			n.number = p.LocalNumbers[i].GlobalPrefix + n.number
		case !p.AllowLocalNumbers:
			return "", fmt.Errorf("%w: %s", ErrLocalNumber, uri)
		}
	}
	if strings.HasPrefix(n.number, "+") {
		// A global number has no phone-context (RFC 3966 3).
		n.params.Remove("phone-context")
	}
	if format == "" {
		format = NumberFormatTel
		if n.sip != nil {
			format = NumberFormatSIP
		}
	}
	if format == NumberFormatTel {
		return "tel:" + n.number + n.params.String(), nil
	}
	u := sip.URI{Scheme: "sip", Host: host, Params: sip.Params{{Name: "user", Value: "phone"}}}
	if n.sip != nil {
		u = *n.sip
	} else if host == "" {
		return "", fmt.Errorf("interconnect: no host for %s as a SIP URI", uri)
	}
	u.User = n.number + n.params.String()
	return u.String(), nil
}

// telephoneNumber is a number and its parameters, out of a tel URI or
// the user part of a SIP URI.
type telephoneNumber struct {
	number string
	params sip.Params
	// sip is the SIP URI the number came in, without the user part, or
	// nil for a tel URI.
	sip *sip.URI
}

func parseNumber(uri string) (telephoneNumber, bool) {
	if len(uri) > 4 && strings.EqualFold(uri[:4], "tel:") {
		number, params, _ := strings.Cut(uri[4:], ";")
		return telephoneNumber{number: number, params: sip.ParseParams(params)}, true
	}
	u, err := sip.ParseURI(uri)
	if err != nil {
		return telephoneNumber{}, false
	}
	// RFC 3261 19.1.1: only user=phone makes the user part a number
	// (also TS 29.165 8.1 NOTE 1).
	if user, ok := u.Params.Get("user"); !ok || !strings.EqualFold(user, "phone") {
		return telephoneNumber{}, false
	}
	number, params, _ := strings.Cut(u.User, ";")
	u.User = ""
	return telephoneNumber{number: number, params: sip.ParseParams(params), sip: &u}, true
}

func sameContext(a, b string) bool {
	clean := func(s string) string { return strings.ToLower(visualSeparators.Replace(s)) }
	if strings.HasPrefix(a, "+") || strings.HasPrefix(b, "+") {
		return clean(a) == clean(b)
	}
	// A domain name keeps its dots.
	return strings.EqualFold(a, b)
}

// NormalizeNumbers applies NormalizeNumber to the numbers that TS
// 29.165 8.1 names: the Request-URI of a request, with requestHost, and
// each P-Asserted-Identity, with identityHost. A Request-URI that cannot
// cross is rejected with 404 (Not Found), which TS 24.229 5.10.1 allows
// by local policy. A P-Asserted-Identity that cannot be made global is
// left as it is: the asserted identity is the network's, and 8.1 NOTE 4
// leaves its formats to the operator. Two P-Asserted-Identity values
// that end up the same become one: RFC 3325 5 has a proxy assert at
// most one SIP URI and one tel URI.
func NormalizeNumbers(m *sip.Message, p Profile, format NumberFormat, requestHost, identityHost string) error {
	if m.IsRequest() {
		uri, err := NormalizeNumber(m.RequestURI, format, requestHost, p)
		if err != nil {
			return reject(404, err)
		}
		m.RequestURI = uri
	}
	values := m.GetAll("P-Asserted-Identity")
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		na, err := sip.ParseNameAddr(v)
		if err != nil {
			out = append(out, v)
			continue
		}
		if uri, err := NormalizeNumber(na.URI, format, identityHost, p); err == nil {
			na.URI = uri
		}
		if seen[na.URI] {
			continue
		}
		seen[na.URI] = true
		out = append(out, na.String())
	}
	if !slices.Equal(out, values) {
		replaceValues(m, "P-Asserted-Identity", out)
	}
	return nil
}

// isGlobalNumber reports whether s is "+" and at least one digit, a
// global number without visual separators (RFC 3966 5.1.4).
func isGlobalNumber(s string) bool {
	digits, ok := strings.CutPrefix(s, "+")
	return ok && digits != "" && strings.Trim(digits, "0123456789") == ""
}
