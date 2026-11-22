package ims

import (
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TelephoneNumber is the telephone number that a URI carries, in either
// of the two forms that IMS uses: the tel URI of RFC 3966, and a SIP
// URI whose user part is the number and that says so with user=phone
// (RFC 3261 19.1.6, TS 29.165 8.1).
//
// The conditions that tell one role's numbers from another's stay with
// the role: whether the number has to be global, whether its SIP URI
// has to name the home domain, and whether a GRUU (RFC 5627) counts as
// a number.
type TelephoneNumber struct {
	// Number is the telephone-subscriber as the URI writes it (RFC 3966
	// 3), without its parameters. It keeps the visual separators of the
	// URI, which WithoutVisualSeparators removes for a comparison.
	Number string
	// Params are the parameters of the number, such as phone-context
	// (RFC 3966 3) and the number portability parameters of RFC 4694. In
	// a SIP URI they belong to the user part, not to the URI (RFC 3261
	// 19.1.6).
	Params sip.Params
	// SIP is the SIP URI that carried the number, with the user part
	// taken out, or nil for a tel URI. Its host and its parameters are
	// what a role reads to add its own conditions: the user=phone that
	// marks the number, and the "gr" of a GRUU (RFC 5627 3.1).
	SIP *sip.URI
}

// ParseTelephoneNumber reads the telephone number that uri carries. It
// reports false for a URI that carries none: a SIP URI without
// user=phone, a URI of another scheme, or one with no
// telephone-subscriber at all.
//
// True says that the URI presents its user part as a telephone number,
// not that the number is one the network can dial: user=phone makes
// sip:alice@example.net;user=phone a number named "alice", and a tel
// URI may hold a local number. A role that needs a global number tests
// the form of Number itself.
func ParseTelephoneNumber(uri string) (TelephoneNumber, bool) {
	if len(uri) >= 4 && strings.EqualFold(uri[:4], "tel:") {
		return telephoneSubscriber(uri[4:], nil)
	}
	u, err := sip.ParseURI(uri)
	if err != nil {
		return TelephoneNumber{}, false
	}
	// Only user=phone makes the user part a telephone number (RFC 3261
	// 19.1.6, also TS 29.165 8.1 NOTE 1).
	if user, ok := u.Params.Get("user"); !ok || !strings.EqualFold(user, "phone") {
		return TelephoneNumber{}, false
	}
	user := u.User
	u.User = ""
	return telephoneSubscriber(user, &u)
}

// telephoneSubscriber reads "number;parameters", the form that both the
// tel URI and the user part of a SIP URI with user=phone write the
// number in (RFC 3966 3, RFC 3261 19.1.6).
func telephoneSubscriber(s string, u *sip.URI) (TelephoneNumber, bool) {
	number, params, _ := strings.Cut(s, ";")
	if number == "" {
		return TelephoneNumber{}, false
	}
	return TelephoneNumber{Number: number, Params: sip.ParseParams(params), SIP: u}, true
}

// visualSeparators are the characters that a telephone number may carry
// for readability only (RFC 3966 5.1.1).
var visualSeparators = strings.NewReplacer("-", "", ".", "", "(", "", ")", "")

// WithoutVisualSeparators is number with the characters that it carries
// for readability only taken out. Two numbers name the same subscriber
// exactly when these forms are equal (RFC 3966 4).
func WithoutVisualSeparators(number string) string {
	return visualSeparators.Replace(number)
}
