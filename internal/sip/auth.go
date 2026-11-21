package sip

import (
	"fmt"
	"strings"
)

// Challenge is the Digest challenge in WWW-Authenticate (RFC 2617
// 3.2.1, RFC 3261 22.4 and 20.44).
//
//	WWW-Authenticate: Digest realm="ims.example", nonce="...", algorithm=MD5, qop="auth"
type Challenge struct {
	Realm     string
	Domain    string // a space-separated list of URIs
	Nonce     string
	Opaque    string
	Stale     bool
	Algorithm string   // "MD5" when empty (RFC 2617 3.2.1)
	QOP       []string // qop-options, e.g. ["auth"]
	// AuthParams are the other parameters, values as received. IMS adds
	// its own, such as ik and ck in the 401 from the S-CSCF to the
	// P-CSCF (TS 24.229).
	AuthParams Params
}

// Credentials are the Digest credentials in Authorization (RFC 2617
// 3.2.2, RFC 3261 22.4 and 20.7).
//
//	Authorization: Digest username="...", realm="...", nonce="...", uri="sip:...", response="..."
//
// The first REGISTER of an IMS UE carries credentials with an empty
// nonce and response, so that the S-CSCF learns the private user
// identity before any challenge (TS 24.229). String writes those
// fields even when they are empty.
type Credentials struct {
	Username  string
	Realm     string
	Nonce     string
	URI       string
	Response  string
	Algorithm string
	CNonce    string
	Opaque    string
	QOP       string // the single qop-value chosen, e.g. "auth"
	NC        string // nonce-count, 8 hex digits, e.g. "00000001"
	// AuthParams are the other parameters, values as received, such as
	// integrity-protected from TS 24.229.
	AuthParams Params
}

// AlgorithmAKAv1MD5 is the algorithm of Digest AKA version 1 (RFC 3310
// 3.1), the one that IMS AKA uses in this project.
const AlgorithmAKAv1MD5 = "AKAv1-MD5"

// Keys returns the ik and ck auth-params of a challenge, IK and CK in
// hexadecimal, and whether both are there. With IMS AKA the S-CSCF adds
// them to the 401 for the P-CSCF, which removes them before the 401
// goes on to the UE (TS 24.229 7.2A.1.3).
func (c Challenge) Keys() (ik, ck string, ok bool) {
	ik, okIK := c.AuthParams.Get("ik")
	ck, okCK := c.AuthParams.Get("ck")
	return Unquote(ik), Unquote(ck), okIK && okCK
}

// SetKeys adds ik and ck, quoted as TS 24.229 7.2A.1.2 writes them.
func (c *Challenge) SetKeys(ik, ck string) {
	c.AuthParams.Set("ik", Quote(ik))
	c.AuthParams.Set("ck", Quote(ck))
}

// RemoveKeys removes ik and ck.
func (c *Challenge) RemoveKeys() {
	c.AuthParams.Remove("ik")
	c.AuthParams.Remove("ck")
}

// IntegrityProtected is the value of the integrity-protected auth-param
// of Authorization: what the P-CSCF tells the S-CSCF about the REGISTER
// it forwards (TS 24.229 7.2A.2.3).
type IntegrityProtected string

// The values of TS 24.229 Table 7.2A.2 that this project uses.
const (
	// IntegrityProtectedYes: the REGISTER came protected by an IMS AKA
	// security association.
	IntegrityProtectedYes IntegrityProtected = "yes"
	// IntegrityProtectedNo: IMS AKA, but the REGISTER came without
	// protection, so it starts an initial registration.
	IntegrityProtectedNo IntegrityProtected = "no"
	// IPAssociationPending: SIP Digest without TLS, a REGISTER with a
	// challenge response from an address with no IP association yet.
	IPAssociationPending IntegrityProtected = "ip-assoc-pending"
	// IPAssociationYes: SIP Digest without TLS, from an address with an
	// IP association.
	IPAssociationYes IntegrityProtected = "ip-assoc-yes"
)

// IntegrityProtected is the integrity-protected auth-param, unquoted,
// or "" when there is none.
func (c Credentials) IntegrityProtected() IntegrityProtected {
	v, _ := c.AuthParams.Get("integrity-protected")
	return IntegrityProtected(Unquote(v))
}

// SetIntegrityProtected replaces any integrity-protected auth-param
// with v (TS 24.229 5.2.2.1 item 4B), or removes it when v is "". The
// value is written quoted, which RFC 2617 1.2 allows for any
// auth-param.
func (c *Credentials) SetIntegrityProtected(v IntegrityProtected) {
	c.AuthParams.Remove("integrity-protected")
	if v != "" {
		c.AuthParams.Set("integrity-protected", Quote(string(v)))
	}
}

// AUTS returns the auts directive of Digest AKA, the base64 of the AUTS
// with which the UE reports a synchronisation failure (RFC 3310 3.4),
// and whether it is there.
func (c Credentials) AUTS() (string, bool) {
	v, ok := c.AuthParams.Get("auts")
	return Unquote(v), ok
}

// SetAUTS adds auts, quoted as RFC 3310 3.4 writes it.
func (c *Credentials) SetAUTS(auts string) { c.AuthParams.Set("auts", Quote(auts)) }

func ParseChallenge(value string) (Challenge, error) {
	params, err := parseDigestParams(value)
	if err != nil {
		return Challenge{}, err
	}
	var c Challenge
	for _, p := range params {
		v := Unquote(p.Value)
		switch strings.ToLower(p.Name) {
		case "realm":
			c.Realm = v
		case "domain":
			c.Domain = v
		case "nonce":
			c.Nonce = v
		case "opaque":
			c.Opaque = v
		case "stale":
			c.Stale = strings.EqualFold(v, "true")
		case "algorithm":
			c.Algorithm = v
		case "qop":
			for _, q := range strings.Split(v, ",") {
				if q = strings.TrimSpace(q); q != "" {
					c.QOP = append(c.QOP, q)
				}
			}
		default:
			c.AuthParams = append(c.AuthParams, p)
		}
	}
	return c, nil
}

// String writes the parameters in the order of the RFC 2617 3.2.1
// grammar. realm and nonce are mandatory and always written.
func (c Challenge) String() string {
	w := digestWriter{}
	w.quoted("realm", c.Realm)
	if c.Domain != "" {
		w.quoted("domain", c.Domain)
	}
	w.quoted("nonce", c.Nonce)
	if c.Opaque != "" {
		w.quoted("opaque", c.Opaque)
	}
	if c.Stale {
		w.token("stale", "true")
	}
	if c.Algorithm != "" {
		w.token("algorithm", c.Algorithm)
	}
	if len(c.QOP) > 0 {
		w.quoted("qop", strings.Join(c.QOP, ","))
	}
	w.raw(c.AuthParams)
	return w.String()
}

func ParseCredentials(value string) (Credentials, error) {
	params, err := parseDigestParams(value)
	if err != nil {
		return Credentials{}, err
	}
	var c Credentials
	for _, p := range params {
		v := Unquote(p.Value)
		switch strings.ToLower(p.Name) {
		case "username":
			c.Username = v
		case "realm":
			c.Realm = v
		case "nonce":
			c.Nonce = v
		case "uri":
			c.URI = v
		case "response":
			c.Response = v
		case "algorithm":
			c.Algorithm = v
		case "cnonce":
			c.CNonce = v
		case "opaque":
			c.Opaque = v
		case "qop":
			c.QOP = v
		case "nc":
			c.NC = v
		default:
			c.AuthParams = append(c.AuthParams, p)
		}
	}
	return c, nil
}

// String writes the parameters in the order of the RFC 2617 3.2.2
// grammar. username, realm, nonce, uri and response are mandatory and
// always written, empty or not. uri is quoted, as RFC 3261 22.4 item 2
// requires for SIP.
func (c Credentials) String() string {
	w := digestWriter{}
	w.quoted("username", c.Username)
	w.quoted("realm", c.Realm)
	w.quoted("nonce", c.Nonce)
	w.quoted("uri", c.URI)
	w.quoted("response", c.Response)
	if c.Algorithm != "" {
		w.token("algorithm", c.Algorithm)
	}
	if c.CNonce != "" {
		w.quoted("cnonce", c.CNonce)
	}
	if c.Opaque != "" {
		w.quoted("opaque", c.Opaque)
	}
	if c.QOP != "" {
		w.token("qop", c.QOP)
	}
	if c.NC != "" {
		w.token("nc", c.NC)
	}
	w.raw(c.AuthParams)
	return w.String()
}

// parseDigestParams reads `Digest name=value, name="value", ...`. The
// parameters are separated by commas outside quoted strings (RFC 2617
// 1.2 and 3.2.1).
func parseDigestParams(value string) (Params, error) {
	scheme, rest, _ := strings.Cut(strings.TrimSpace(value), " ")
	if !strings.EqualFold(scheme, "Digest") {
		return nil, fmt.Errorf("sip: not a Digest authentication value: %q", value)
	}
	var params Params
	for _, p := range splitList(rest) {
		name, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("sip: malformed auth-param %q", p)
		}
		params = append(params, Param{Name: strings.TrimSpace(name), Value: strings.TrimSpace(v)})
	}
	return params, nil
}

type digestWriter struct{ params []string }

func (w *digestWriter) quoted(name, value string) {
	w.params = append(w.params, name+"="+Quote(value))
}

func (w *digestWriter) token(name, value string) {
	w.params = append(w.params, name+"="+value)
}

func (w *digestWriter) raw(params Params) {
	for _, p := range params {
		w.token(p.Name, p.Value)
	}
}

func (w *digestWriter) String() string { return "Digest " + strings.Join(w.params, ", ") }
