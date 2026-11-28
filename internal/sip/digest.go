package sip

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
)

// SIMPLIFIED: RFC 2617 3.2.2.2 and 3.2.2.3, RFC 7616 and RFC 8760. Only
// the algorithms whose digest is MD5 or SHA-256, with qop "auth" (or no
// qop), are computed; MD5-sess, SHA-256-sess, auth-int and SHA-512-256
// are not. The HSS stub hands out only the MD5 H(A1) of TS 29.228 Table
// 6.3.7, so no node in this project offers SHA-256 in a SIP Digest
// challenge, which TS 24.229 5.4.1.2.1B and 5.1.1.5.4 prefer over MD5.
// The SHA-256 digest is computed for AKAv2-SHA-256, whose password comes
// from the authentication vector and not from the HSS.

// HA1 is H(A1) = H(username ":" realm ":" password) for an algorithm
// that is not a session variant (RFC 2617 3.2.2.2, RFC 7616 3.4.2). The
// IMS HSS hands the S-CSCF this value rather than the password (the
// Digest-HA1 AVP of TS 29.229), so a registrar can verify a response
// without ever knowing the password.
//
// For an algorithm whose digest this project does not compute it
// returns the MD5 value. DigestResponse refuses such an algorithm, so
// that H(A1) never verifies a response.
func HA1(algorithm, username, realm, password string) string {
	h, _ := digestHash(algorithm)
	return digest(h, username+":"+realm+":"+password)
}

// DigestResponse computes the request-digest that credentials c must
// carry in a request with method (RFC 2617 3.2.2.1). It uses c.Nonce,
// c.URI, c.QOP, c.NC and c.CNonce, so a UE fills those in before
// calling it, and a registrar calls it with the credentials it received
// and compares the result with c.Response.
//
// Digest AKA changes only the password, which is RES for AKAv1-MD5
// (RFC 3310 3.3) and derived from RES, IK and CK for AKAv2-SHA-256
// (RFC 4169 2.1), so the caller passes the H(A1) of that password.
func DigestResponse(ha1 string, method Method, c Credentials) (string, error) {
	h, ok := digestHash(c.Algorithm)
	if !ok {
		return "", fmt.Errorf("sip: digest algorithm %q is not supported", c.Algorithm)
	}
	// RFC 2617 3.2.2.3: with qop "auth" or no qop, A2 = Method ":"
	// digest-uri-value.
	ha2 := digest(h, string(method)+":"+c.URI)
	switch strings.ToLower(c.QOP) {
	case "":
		// The RFC 2069 form, kept for compatibility (RFC 3261 22.4).
		return digest(h, ha1+":"+c.Nonce+":"+ha2), nil
	case "auth":
		if c.NC == "" || c.CNonce == "" {
			return "", errors.New("sip: qop=auth needs nc and cnonce")
		}
		return digest(h, ha1+":"+c.Nonce+":"+c.NC+":"+c.CNonce+":"+c.QOP+":"+ha2), nil
	}
	return "", fmt.Errorf("sip: digest qop %q is not supported", c.QOP)
}

// digestHash is the hash H() of RFC 2617 3.2.1 that an algorithm
// parameter names, and whether this project computes it: MD5 when the
// algorithm-value is MD5 or absent (RFC 3310 3.1), and SHA-256 when it
// is SHA-256 (RFC 7616 3.3). An AKA version before the algorithm-value
// changes the password, not the hash (RFC 3310 3.3, RFC 4169 2.1), so
// AKAv1-MD5 is digested with MD5 and AKAv2-SHA-256 with SHA-256.
func digestHash(algorithm string) (func() hash.Hash, bool) {
	switch value := algorithmValue(algorithm); {
	case value == "" || strings.EqualFold(value, "MD5"):
		return md5.New, true
	case strings.EqualFold(value, "SHA-256"):
		return sha256.New, true
	}
	return md5.New, false
}

// algorithmValue is the algorithm-value of an algorithm parameter: what
// follows the AKA version when the parameter names one, and the whole
// parameter otherwise (RFC 3310 3.1, aka-namespace = aka-version "-"
// algorithm-value with aka-version = "AKAv" 1*DIGIT).
func algorithmValue(algorithm string) string {
	version, value, found := strings.Cut(algorithm, "-")
	if !found || len(version) <= len("AKAv") || !strings.EqualFold(version[:len("AKAv")], "AKAv") {
		return algorithm
	}
	if _, err := strconv.ParseUint(version[len("AKAv"):], 10, 8); err != nil {
		return algorithm
	}
	return value
}

// digest is H(data) of RFC 2617 3.2.1: the hash as lower-case hex
// digits.
func digest(newHash func() hash.Hash, data string) string {
	h := newHash()
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
