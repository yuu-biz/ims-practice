package aka

import (
	"encoding/base64"
	"fmt"
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
