package xcap

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// The example of RFC 7616 3.9.1: user Mufasa, password "Circle of
// Life", a GET of /dir/index.html.
var mufasa = sip.Credentials{
	Username: "Mufasa",
	Realm:    "http-auth@example.org",
	URI:      "/dir/index.html",
	Nonce:    "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v",
	NC:       "00000001",
	CNonce:   "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ",
	QOP:      "auth",
}

func TestResponseMatchesRFC7616Example(t *testing.T) {
	cred := NewCredential(mufasa.Username, mufasa.Realm, "Circle of Life")
	for a, want := range map[Algorithm]string{
		MD5:    "8ca523f5e9506fed4657c9700eebdbec",
		SHA256: "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1",
	} {
		ha1, ok := cred.ha1(a)
		if !ok {
			t.Fatalf("no H(A1) for %s", a)
		}
		if got := response(a, ha1, http.MethodGet, mufasa); got != want {
			t.Errorf("%s response = %s, want %s", a, got, want)
		}
	}
}

func TestCredentialRoundTrip(t *testing.T) {
	c := NewCredential("001010000000001@ims.example", "ims.example", "test-password-1")
	got, err := ParseCredential(c.Marshal())
	if err != nil {
		t.Fatal(err)
	}
	got.XMLName = c.XMLName
	if !reflect.DeepEqual(got, c) {
		t.Errorf("got %+v, want %+v", got, c)
	}
	if bytes.Contains(c.Marshal(), []byte("test-password-1")) {
		t.Errorf("the stored credential holds the password: %s", c.Marshal())
	}
	if _, err := ParseCredential([]byte(`<ut-digest-credential><username>u</username></ut-digest-credential>`)); err == nil {
		t.Error("a credential without a realm parsed")
	}
}

// signedRequest is a request whose Authorization answers a challenge
// of n for cred with password.
func signedRequest(t *testing.T, n nonces, cred Credential, username, password string, a Algorithm) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPut, "/simservs.ngn.etsi.org/users/sip:a@ims.example/simservs.xml", nil)
	c := sip.Credentials{
		Username: username, Realm: cred.Realm, Nonce: n.issue(), URI: r.RequestURI,
		Algorithm: string(a), CNonce: "0a4f113b", QOP: "auth", NC: "00000001",
	}
	c.Response = response(a, a.h(username+":"+cred.Realm+":"+password), r.Method, c)
	r.Header.Set("Authorization", c.String())
	return r
}

func TestVerify(t *testing.T) {
	now := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	n := nonces{key: []byte("test-nonce-key-0123456789"), now: func() time.Time { return now }}
	cred := NewCredential("001010000000001@ims.example", "ims.example", "test-password-1")
	for _, a := range []Algorithm{SHA256, MD5} {
		r := signedRequest(t, n, cred, cred.Username, "test-password-1", a)
		v, info := n.verify(r, cred)
		if v != authenticated {
			t.Fatalf("%s: verdict %d", a, v)
		}
		got, err := sip.ParseCredentials("Digest " + info)
		if err != nil || got.QOP != "auth" || got.NC != "00000001" || got.CNonce != "0a4f113b" || len(got.AuthParams) != 1 {
			t.Errorf("%s: Authentication-Info %q", a, info)
		}
	}

	if v, _ := n.verify(signedRequest(t, n, cred, cred.Username, "wrong", SHA256), cred); v != unauthenticated {
		t.Errorf("wrong password: verdict %d", v)
	}
	if v, _ := n.verify(signedRequest(t, n, cred, "001010000000002@ims.example", "test-password-2", SHA256), cred); v != notOwner {
		t.Errorf("another user: verdict %d", v)
	}
	if v, _ := n.verify(httptest.NewRequest(http.MethodGet, "/", nil), cred); v != unauthenticated {
		t.Errorf("no Authorization: verdict %d", v)
	}

	r := signedRequest(t, n, cred, cred.Username, "test-password-1", SHA256)
	r.RequestURI = "/simservs.ngn.etsi.org/users/sip:b@ims.example/simservs.xml"
	if v, _ := n.verify(r, cred); v != malformed {
		t.Errorf("uri of another resource: verdict %d", v)
	}

	// A right response with a nonce that expired, or that a server with
	// another key issued, asks the client to retry with a new nonce.
	r = signedRequest(t, n, cred, cred.Username, "test-password-1", SHA256)
	later := nonces{key: n.key, now: func() time.Time { return now.Add(nonceLifetime) }}
	if v, _ := later.verify(r, cred); v != staleNonce {
		t.Errorf("expired nonce: verdict %d", v)
	}
	other := nonces{key: []byte("another-key"), now: n.now}
	if v, _ := other.verify(r, cred); v != staleNonce {
		t.Errorf("foreign nonce: verdict %d", v)
	}
	// Another instance with the same key takes the nonce.
	same := nonces{key: []byte("test-nonce-key-0123456789"), now: n.now}
	if v, _ := same.verify(r, cred); v != authenticated {
		t.Errorf("nonce of another instance: verdict %d", v)
	}
}

func TestChallengeOrder(t *testing.T) {
	n := nonces{key: []byte("k"), now: time.Now}
	h := http.Header{}
	n.challenge(h, NewCredential("u", "ims.example", "p"), true)
	values := h.Values("WWW-Authenticate")
	if len(values) != 2 {
		t.Fatalf("challenges %q", values)
	}
	for i, want := range []string{"SHA-256", "MD5"} {
		ch, err := sip.ParseChallenge(values[i])
		if err != nil {
			t.Fatal(err)
		}
		if ch.Algorithm != want || ch.Realm != "ims.example" || !ch.Stale || len(ch.QOP) != 1 || ch.QOP[0] != "auth" {
			t.Errorf("challenge %d = %+v", i, ch)
		}
	}
}
