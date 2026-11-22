// Package ims holds the IMS rules that more than one role applies, such
// as how a REGISTER names the user, or when the UE and the P-CSCF
// refresh.
package ims

import (
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
)

// PublicIdentity is the public user identity a REGISTER registers: the
// To URI without its port and parameters (TS 24.229 5.3.1.2 and
// 5.4.1.2.1).
func PublicIdentity(to string) (string, error) {
	na, err := sip.ParseNameAddr(to)
	if err != nil {
		return "", err
	}
	return CanonicalURI(na.URI)
}

// CanonicalURI is the form in which the HSS stores a public identity
// (TS 29.229 6.3.2): a SIP URI as sip:user@host, and a tel URI as tel:
// with the global number and no visual separators or parameters.
//
// It is also the form in which two public identities compare: they are
// the same identity exactly when their canonical forms are equal. So
// the scheme and the host are in lower case, since they compare without
// case, while the user part keeps its case, since it compares with case
// (RFC 3261 19.1.4). An escaped character in the user part that needs
// no escaping is the same as the character itself (19.1.4 again), and
// is written unescaped. A tel URI compares by its digits, without the
// visual separators and without case (RFC 3966 4).
func CanonicalURI(uri string) (string, error) {
	if number, ok := TelNumber(uri); ok {
		aus, err := sipdns.ApplicationUniqueString(number)
		if err != nil {
			return "", err
		}
		return "tel:" + aus, nil
	}
	u, err := sip.ParseURI(uri)
	if err != nil {
		return "", err
	}
	return u.Scheme + ":" + unescapeUnreserved(u.User) + "@" + strings.ToLower(u.Host), nil
}

// PublicIdentityKey is the key under which a public identity is kept
// and found: its CanonicalURI, so that two forms of one identity find
// the same entry. An identity that is neither a SIP nor a tel URI is
// its own key.
func PublicIdentityKey(id string) string {
	if c, err := CanonicalURI(id); err == nil {
		return c
	}
	return id
}

// SamePublicIdentity reports whether a and b name the same public
// identity: both are SIP or tel URIs with equal canonical forms.
func SamePublicIdentity(a, b string) bool {
	ca, errA := CanonicalURI(a)
	cb, errB := CanonicalURI(b)
	return errA == nil && errB == nil && ca == cb
}

// unescapeUnreserved writes each escaped unreserved character of a user
// part (RFC 3261 25.1: a letter, a digit or a mark) as the character.
// Any other escape stays as it is.
func unescapeUnreserved(user string) string {
	if !strings.Contains(user, "%") {
		return user
	}
	var b strings.Builder
	for i := 0; i < len(user); i++ {
		if user[i] == '%' && i+2 < len(user) {
			if c, err := strconv.ParseUint(user[i+1:i+3], 16, 8); err == nil && isUnreserved(byte(c)) {
				b.WriteByte(byte(c))
				i += 2
				continue
			}
		}
		b.WriteByte(user[i])
	}
	return b.String()
}

func isUnreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0
}

// TelNumber returns the number of a tel URI (RFC 3966 3), without its
// parameters. It reports false for any other URI, a SIP URI with
// user=phone included: a caller that takes a number in either form
// calls ParseTelephoneNumber.
func TelNumber(uri string) (string, bool) {
	n, ok := ParseTelephoneNumber(uri)
	if !ok || n.SIP != nil {
		return "", false
	}
	return n.Number, true
}

// PrivateIdentity is the private user identity of a REGISTER: the
// username of its Authorization header, or, when there is none, the
// public identity without its scheme (TS 24.229 5.3.1.2 and 5.4.1.1
// item 3).
func PrivateIdentity(req *sip.Message, publicID string) (string, *sip.Credentials, error) {
	v := req.Get("Authorization")
	if v == "" {
		_, rest, _ := strings.Cut(publicID, ":")
		return rest, nil, nil
	}
	c, err := sip.ParseCredentials(v)
	if err != nil {
		return "", nil, err
	}
	return c.Username, &c, nil
}

// ServedUser is the user an originating request is for (TS 24.229
// 5.4.3.2 step 1). A node of the trust domain that sends a request on
// behalf of a user, such as an application server, names the user in
// P-Served-User (a, RFC 5502 7.2): the request may then appear to come
// from someone else, whom P-Asserted-Identity names (RFC 5502 4.4).
// Without P-Served-User, the user is the first P-Asserted-Identity (b).
// Only a node of the trust domain may be the source: the P-CSCF and the
// IBCF remove a P-Served-User that comes from outside.
func ServedUser(req *sip.Message) (string, error) {
	if v := req.Get("P-Served-User"); v != "" {
		served, err := sip.ParsePServedUser(v)
		if err != nil {
			return "", err
		}
		return CanonicalURI(served.URI)
	}
	return AssertedIdentity(req)
}

// AssertedIdentity is the first P-Asserted-Identity of req in canonical
// form: the user who sent the request, as the network asserts it. The
// P-CSCF puts it there for a request of its UE (TS 24.229 5.2.6.3.3
// step 6).
func AssertedIdentity(req *sip.Message) (string, error) {
	na, err := sip.ParseNameAddr(req.Get("P-Asserted-Identity"))
	if err != nil {
		return "", err
	}
	return CanonicalURI(na.URI)
}
