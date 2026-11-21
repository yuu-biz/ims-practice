package xcap

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The header fields of HTTP Digest have the grammar that SIP took from
// RFC 2617, so package sip reads and writes them; the digest itself is
// computed here, with the algorithms of RFC 7616 that package sip does
// not have.

// qopAuth is the only quality of protection offered: "auth" (RFC 7616
// 3.3). With "auth-int" the body would be digested too.
const qopAuth = "auth"

// nonceLifetime is how long a nonce is accepted. After it the server
// answers 401 with stale=true, and the client retries with the nonce of
// that answer without asking the user again (RFC 7616 3.3).
const nonceLifetime = 5 * time.Minute

// timeNow is time.Now; the tests of the nonces replace it.
var timeNow = time.Now

// nonces issues and checks nonces that need no state on the server: a
// nonce is a timestamp and an HMAC of it with a key that every instance
// of the server shares, the construction RFC 7616 3.3 suggests. So a
// nonce from one instance is good at every other.
//
// SIMPLIFIED: RFC 7616 3.4 and 5.5. Without state the server cannot
// tell a nonce count it has seen before from a new one, which is how
// the server detects a replayed request. A request copied off the
// network is accepted again until its nonce expires. RFC 7616 5.5
// points out that PUT is where this matters; the If-Match that GSMA
// IR.92 2.3.2 has the UE send on every PUT and DELETE turns a replay of
// one into 412 once the document has changed. Keeping the counts would
// need a store that every instance shares, for a gain that TLS gives
// better.
type nonces struct {
	key []byte
	now func() time.Time
}

// issue returns a new nonce: base64 of the 8-octet time in nanoseconds
// and the first 16 octets of its HMAC-SHA-256.
func (n nonces) issue() string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n.now().UnixNano()))
	return base64.StdEncoding.EncodeToString(append(b[:], n.mac(b[:])...))
}

func (n nonces) mac(timestamp []byte) []byte {
	m := hmac.New(sha256.New, n.key)
	m.Write(timestamp)
	return m.Sum(nil)[:16]
}

// fresh tells whether nonce is one that this server issued less than
// nonceLifetime ago.
func (n nonces) fresh(nonce string) bool {
	b, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil || len(b) != 8+16 {
		return false
	}
	if !hmac.Equal(b[8:], n.mac(b[:8])) {
		return false
	}
	issued := time.Unix(0, int64(binary.BigEndian.Uint64(b[:8])))
	age := n.now().Sub(issued)
	return age >= 0 && age < nonceLifetime
}

// challenge adds to h the WWW-Authenticate header fields of a 401
// (RFC 7616 3.3): one per algorithm the credential has, most preferred
// first (3.7), all with the same nonce.
func (n nonces) challenge(h http.Header, c Credential, stale bool) {
	nonce := n.issue()
	for _, a := range algorithms {
		if _, ok := c.ha1(a); !ok {
			continue
		}
		h.Add("WWW-Authenticate", sip.Challenge{
			Realm: c.Realm, Nonce: nonce, Stale: stale, Algorithm: string(a), QOP: []string{qopAuth},
		}.String())
	}
}

// verdict is the outcome of checking the Authorization of a request.
type verdict int

const (
	// authenticated: the response is right and the nonce fresh.
	authenticated verdict = iota
	// unauthenticated: no credentials, or wrong ones. 401 with a new
	// challenge.
	unauthenticated
	// staleNonce: the response is right for a nonce that has expired or
	// that this server did not issue. 401 with stale=true (RFC 7616
	// 3.3), so the client retries without asking the user again.
	staleNonce
	// malformed: a parameter is missing or improper. RFC 7616 3.4 asks
	// for a 4xx; the server answers 400.
	malformed
	// notOwner: the username is not the one of the credential of the
	// document's owner. The server has no credential to check it
	// against (Sh finds a user by public identity only), and whatever
	// the response, the user may not touch the document: 403.
	notOwner
)

// verify checks the Authorization header field of r against c, the
// credential of the owner of the document (RFC 7616 3.4, 3.6). With
// authenticated it also returns the Authentication-Info value.
func (n nonces) verify(r *http.Request, c Credential) (verdict, string) {
	value := r.Header.Get("Authorization")
	if value == "" || !strings.HasPrefix(strings.ToLower(value), "digest ") {
		return unauthenticated, ""
	}
	got, err := sip.ParseCredentials(value)
	if err != nil {
		return malformed, ""
	}
	if got.Username == "" || got.Nonce == "" || got.URI == "" || got.Response == "" || got.CNonce == "" || len(got.NC) != 8 ||
		!strings.EqualFold(got.QOP, qopAuth) {
		return malformed, ""
	}
	// RFC 7616 3.4.6: uri names the resource of the request line.
	if got.URI != r.RequestURI {
		return malformed, ""
	}
	if got.Username != c.Username {
		return notOwner, ""
	}
	algorithm := MD5
	if got.Algorithm != "" {
		algorithm = Algorithm(got.Algorithm)
	}
	ha1, ok := c.ha1(algorithm)
	if !ok || got.Realm != c.Realm {
		return unauthenticated, ""
	}
	want := response(algorithm, ha1, r.Method, got)
	if subtle.ConstantTimeCompare([]byte(want), []byte(strings.ToLower(got.Response))) != 1 {
		return unauthenticated, ""
	}
	if !n.fresh(got.Nonce) {
		return staleNonce, ""
	}
	// RFC 7616 3.5: rspauth is the response with an empty method, which
	// proves to the client that the server knows H(A1) too.
	info := strings.Join([]string{
		"qop=" + qopAuth,
		"rspauth=" + sip.Quote(response(algorithm, ha1, "", got)),
		"cnonce=" + sip.Quote(got.CNonce),
		"nc=" + got.NC,
	}, ", ")
	return authenticated, info
}

// response is the response of RFC 7616 3.4.1 for qop "auth", where A2
// is Method ":" request-uri (3.4.3).
func response(a Algorithm, ha1, method string, c sip.Credentials) string {
	ha2 := a.h(method + ":" + c.URI)
	return a.kd(ha1, c.Nonce+":"+c.NC+":"+c.CNonce+":"+c.QOP+":"+ha2)
}
