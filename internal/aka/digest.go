package aka

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"slices"
)

// Nonce is the nonce of the challenge, RFC 3310 3.2: the base64 of RAND
// and AUTN. RFC 3310 allows server data after them; this project sends
// none.
func Nonce(rand RAND, autn AUTN) string {
	return base64.StdEncoding.EncodeToString(append(rand[:], autn[:]...))
}

// ParseNonce takes RAND and AUTN out of a nonce, ignoring any server
// data after them (RFC 3310 3.2).
func ParseNonce(nonce string) (RAND, AUTN, error) {
	b, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil {
		return RAND{}, AUTN{}, fmt.Errorf("aka: nonce: %w", err)
	}
	var rand RAND
	var autn AUTN
	if len(b) < len(rand)+len(autn) {
		return RAND{}, AUTN{}, fmt.Errorf("aka: nonce has %d octets, want at least %d", len(b), len(rand)+len(autn))
	}
	copy(rand[:], b)
	copy(autn[:], b[len(rand):])
	return rand, autn, nil
}

// EncodeAUTS is the value of the auts parameter, RFC 3310 3.4: the
// base64 of AUTS.
func EncodeAUTS(auts AUTS) string {
	return base64.StdEncoding.EncodeToString(auts[:])
}

// ParseAUTS reads the value of an auts parameter.
func ParseAUTS(s string) (AUTS, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	var auts AUTS
	if err != nil {
		return AUTS{}, fmt.Errorf("aka: auts: %w", err)
	}
	if len(b) != len(auts) {
		return AUTS{}, fmt.Errorf("aka: auts has %d octets, want %d", len(b), len(auts))
	}
	copy(auts[:], b)
	return auts, nil
}

// DigestPassword is the password that the Digest response of AKAv1-MD5
// is computed with (RFC 3310 3.3): the octets of RES on the UE, of XRES
// on the S-CSCF. Everything else is RFC 2617 MD5 Digest. A response
// that carries auts uses the empty password instead (RFC 3310 3.4), so
// pass nil then.
func DigestPassword(res []byte) string {
	return string(res)
}

// akav2PasswordLabel is the string that the password is derived with
// (RFC 4169 2.1). It is case sensitive.
const akav2PasswordLabel = "http-digest-akav2-password"

// AKAv2DigestPassword is the password that the Digest response of
// AKAv2-SHA-256 is computed with (RFC 4169 2.1): the base64 of
// PRF(RES || IK || CK, "http-digest-akav2-password"), where res is RES
// on the UE and XRES on the S-CSCF. Binding the session keys into the
// password is all that AKAv2 changes, and it is what makes the response
// prove that the sender holds them and not only RES (RFC 4169 1).
//
// The PRF is HMAC with the hash that produces the digest (RFC 4169
// 2.1), SHA-256 for AKAv2-SHA-256. A response that carries auts uses
// the empty password of RFC 3310 3.4 instead, whatever the version.
func AKAv2DigestPassword(res []byte, ik IK, ck CK) string {
	prf := hmac.New(sha256.New, slices.Concat(res, ik[:], ck[:]))
	prf.Write([]byte(akav2PasswordLabel))
	return base64.StdEncoding.EncodeToString(prf.Sum(nil))
}
