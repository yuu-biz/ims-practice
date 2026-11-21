package xcap

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
)

// CredentialServiceIndication is the Service-Indication of the Sh
// Repository Data that holds the Ut credential of a user. TS 24.623
// 5.2.3.2.1 leaves how the AS gets the credential out of its scope;
// this project keeps it in the HSS next to the other data of the subscription,
// in this project's own format, which the HSS stores without reading
// (TS 29.328 7.6.1).
const CredentialServiceIndication = "Ut-Digest-Credential"

// Algorithm is a digest algorithm of RFC 7616 3.3.
type Algorithm string

const (
	// SHA256 is mandatory to implement (RFC 7616 3.7).
	SHA256 Algorithm = "SHA-256"
	// MD5 is there for clients that know nothing else (RFC 7616 3.7),
	// and is what the algorithm parameter means when it is absent (3.3).
	MD5 Algorithm = "MD5"
)

// algorithms are the ones the server offers, most preferred first, the
// order RFC 7616 3.7 has the challenges in. SHA-512-256, the backup
// algorithm of 3.7, is not offered.
var algorithms = []Algorithm{SHA256, MD5}

// h is H(data) of RFC 7616 3.3, in lower-case hex.
func (a Algorithm) h(data string) string {
	switch a {
	case SHA256:
		sum := sha256.Sum256([]byte(data))
		return hex.EncodeToString(sum[:])
	case MD5:
		sum := md5.Sum([]byte(data))
		return hex.EncodeToString(sum[:])
	}
	panic("xcap: unknown digest algorithm " + string(a))
}

// kd is KD(secret, data) of RFC 7616 3.3.
func (a Algorithm) kd(secret, data string) string { return a.h(secret + ":" + data) }

// Credential is what the XCAP server needs to check a user's Digest
// response: the username and realm the user answers with, and H(A1) of
// RFC 7616 3.4.2 for each algorithm, so that the password itself is
// never stored (RFC 7616 5.2).
//
//	<ut-digest-credential>
//	  <username>001010000000001@ims.mnc001.mcc001.3gppnetwork.org</username>
//	  <realm>ims.mnc001.mcc001.3gppnetwork.org</realm>
//	  <ha1 algorithm="SHA-256">...</ha1>
//	  <ha1 algorithm="MD5">...</ha1>
//	</ut-digest-credential>
//
// The username is the private user identity, as in the SIP Digest of
// IMS (TS 24.229 5.1.1.2.3), not the XUI: the XUI names the document,
// the username the one who asks for it.
type Credential struct {
	XMLName  xml.Name   `xml:"ut-digest-credential"`
	Username string     `xml:"username"`
	Realm    string     `xml:"realm"`
	HA1      []HashedA1 `xml:"ha1"`
}

// HashedA1 is H(A1) for one algorithm.
type HashedA1 struct {
	Algorithm Algorithm `xml:"algorithm,attr"`
	Value     string    `xml:",chardata"`
}

// NewCredential derives the credential of a user from the password,
// for every algorithm the server offers.
func NewCredential(username, realm, password string) Credential {
	c := Credential{Username: username, Realm: realm}
	for _, a := range algorithms {
		c.HA1 = append(c.HA1, HashedA1{Algorithm: a, Value: a.h(username + ":" + realm + ":" + password)})
	}
	return c
}

// ParseCredential reads a credential from the ServiceData of the
// Repository Data.
func ParseCredential(data []byte) (Credential, error) {
	var c Credential
	if err := xml.Unmarshal(data, &c); err != nil {
		return Credential{}, fmt.Errorf("xcap: credential: %w", err)
	}
	if c.Username == "" || c.Realm == "" {
		return Credential{}, errors.New("xcap: credential without a username or a realm")
	}
	for _, a := range c.HA1 {
		if a.Algorithm != SHA256 && a.Algorithm != MD5 {
			return Credential{}, fmt.Errorf("xcap: credential: unknown algorithm %q", a.Algorithm)
		}
	}
	return c, nil
}

// Marshal writes the credential as the one element that goes into the
// ServiceData.
func (c Credential) Marshal() []byte {
	b, err := xml.Marshal(c)
	if err != nil {
		panic(err) // only strings, which always marshal
	}
	return b
}

// ha1 returns H(A1) for algorithm a, if the credential has it.
func (c Credential) ha1(a Algorithm) (string, bool) {
	for _, h := range c.HA1 {
		if h.Algorithm == a {
			return h.Value, true
		}
	}
	return "", false
}
