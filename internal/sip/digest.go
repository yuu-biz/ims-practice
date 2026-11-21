package sip

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// SIMPLIFIED: RFC 2617 3.2.2.2 and 3.2.2.3, RFC 7616 and RFC 8760. Only
// algorithm MD5 and qop "auth" (or no qop) are computed; MD5-sess,
// auth-int and the SHA-256 algorithms of RFC 7616, which TS 24.229
// 5.4.1.2.1B and 5.1.1.5.4 prefer over MD5, are not. The HSS stub hands
// out only the MD5 H(A1) of TS 29.228 Table 6.3.7, so no node in this
// project offers the other variants in a challenge. Digest AKA is
// computed for AKAv1-MD5 only, whose digest is MD5 too.

// HA1 is H(A1) = MD5(username ":" realm ":" password) for algorithm MD5
// (RFC 2617 3.2.2.2). The IMS HSS hands the S-CSCF this value rather
// than the password (the Digest-HA1 AVP of TS 29.229), so a registrar
// can verify a response without ever knowing the password.
func HA1(username, realm, password string) string {
	return h(username + ":" + realm + ":" + password)
}

// DigestResponse computes the request-digest that credentials c must
// carry in a request with method (RFC 2617 3.2.2.1). It uses c.Nonce,
// c.URI, c.QOP, c.NC and c.CNonce, so a UE fills those in before
// calling it, and a registrar calls it with the credentials it received
// and compares the result with c.Response.
//
// With algorithm AKAv1-MD5 the digest is the same; Digest AKA changes
// only the password, which is RES (RFC 3310 3.3), so the caller passes
// the H(A1) of that password.
func DigestResponse(ha1 string, method Method, c Credentials) (string, error) {
	algorithm := c.Algorithm
	if strings.EqualFold(algorithm, AlgorithmAKAv1MD5) {
		// RFC 3310 3.1: aka-version "-" algorithm-value, here MD5.
		algorithm = "MD5"
	}
	if algorithm != "" && !strings.EqualFold(algorithm, "MD5") {
		return "", fmt.Errorf("sip: digest algorithm %q is not supported", c.Algorithm)
	}
	// RFC 2617 3.2.2.3: with qop "auth" or no qop, A2 = Method ":"
	// digest-uri-value.
	ha2 := h(string(method) + ":" + c.URI)
	switch strings.ToLower(c.QOP) {
	case "":
		// The RFC 2069 form, kept for compatibility (RFC 3261 22.4).
		return h(ha1 + ":" + c.Nonce + ":" + ha2), nil
	case "auth":
		if c.NC == "" || c.CNonce == "" {
			return "", errors.New("sip: qop=auth needs nc and cnonce")
		}
		return h(ha1 + ":" + c.Nonce + ":" + c.NC + ":" + c.CNonce + ":" + c.QOP + ":" + ha2), nil
	}
	return "", fmt.Errorf("sip: digest qop %q is not supported", c.QOP)
}

// h is H() of RFC 2617 3.2.1 for MD5: the digest as 32 lower-case hex
// digits.
func h(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}
