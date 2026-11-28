package sip

import (
	"crypto/md5"
	"fmt"
	"testing"
)

// The values of RFC 2617 3.5.
const (
	rfc2617Username = "Mufasa"
	rfc2617Realm    = "testrealm@host.com"
	rfc2617Password = "Circle Of Life"
	rfc2617Nonce    = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
	rfc2617URI      = "/dir/index.html"
)

func TestHA1(t *testing.T) {
	want := fmt.Sprintf("%x", md5.Sum([]byte("Mufasa:testrealm@host.com:Circle Of Life")))
	if got := HA1("MD5", rfc2617Username, rfc2617Realm, rfc2617Password); got != want {
		t.Errorf("HA1 = %s, want %s", got, want)
	}
}

func TestDigestResponseRFC2617Example(t *testing.T) {
	c := Credentials{
		Username: rfc2617Username, Realm: rfc2617Realm, Nonce: rfc2617Nonce, URI: rfc2617URI,
		QOP: "auth", NC: "00000001", CNonce: "0a4f113b",
	}
	got, err := DigestResponse(HA1(c.Algorithm, rfc2617Username, rfc2617Realm, rfc2617Password), "GET", c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "6629fae49393a05397450978507c4ef1"; got != want {
		t.Errorf("response = %s, want %s (RFC 2617 3.5)", got, want)
	}
}

func TestDigestResponseWithoutQOP(t *testing.T) {
	md5hex := func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }
	ha1 := md5hex("001010000000001@" + imsDomain + ":" + imsDomain + ":secret")
	want := md5hex(ha1 + ":abc123:" + md5hex("REGISTER:sip:"+imsDomain))

	c := Credentials{Username: "001010000000001@" + imsDomain, Realm: imsDomain, Nonce: "abc123", URI: "sip:" + imsDomain}
	got, err := DigestResponse(ha1, REGISTER, c)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("response = %s, want %s", got, want)
	}
}

// The SHA-256 example of RFC 7616 3.9.1, the digest that AKAv2-SHA-256
// uses (RFC 4169 2.1).
func TestDigestResponseSHA256RFC7616Example(t *testing.T) {
	c := Credentials{
		Username: "Mufasa", Realm: "http-auth@example.org", URI: "/dir/index.html", Algorithm: "SHA-256",
		Nonce: "7ypf/xlj9XXwfDPEoM4URrv/xwf94BcCAzFZH4GiTo0v", NC: "00000001",
		CNonce: "f2/wE4q74E6zIJEtWaHKaf5wv/H5QzzpXusqGemxURZJ", QOP: "auth",
	}
	got, err := DigestResponse(HA1(c.Algorithm, c.Username, c.Realm, "Circle of Life"), "GET", c)
	if err != nil {
		t.Fatal(err)
	}
	if want := "753927fa0e85d155564e2e272a28d1802ca10daf4496794697cf8db5856cb6c1"; got != want {
		t.Errorf("response = %s, want %s (RFC 7616 3.9.1)", got, want)
	}
}

// RFC 3310 3.1 and RFC 4169 2: the AKA version before the
// algorithm-value chooses the password, not the digest, so AKAv2-SHA-256
// is digested exactly as SHA-256 and AKAv1-MD5 as MD5.
func TestDigestResponseAKAVersionKeepsTheDigest(t *testing.T) {
	for _, tc := range []struct{ aka, plain string }{
		{AlgorithmAKAv2SHA256, "SHA-256"},
		{AlgorithmAKAv1MD5, "MD5"},
	} {
		c := Credentials{Username: "Mufasa", Realm: "http-auth@example.org", URI: "/dir/index.html",
			Nonce: "nonce", NC: "00000001", CNonce: "0a4f113b", QOP: "auth"}
		c.Algorithm = tc.plain
		want, err := DigestResponse(HA1(c.Algorithm, c.Username, c.Realm, "password"), REGISTER, c)
		if err != nil {
			t.Fatal(err)
		}
		c.Algorithm = tc.aka
		got, err := DigestResponse(HA1(c.Algorithm, c.Username, c.Realm, "password"), REGISTER, c)
		if err != nil || got != want {
			t.Errorf("%s gives %s (%v), want the %s digest %s", tc.aka, got, err, tc.plain, want)
		}
	}
}

func TestDigestResponseRejectsUnsupportedVariants(t *testing.T) {
	base := Credentials{Nonce: "n", URI: "sip:" + imsDomain, NC: "00000001", CNonce: "c"}
	for name, c := range map[string]Credentials{
		"MD5-sess":        func() Credentials { c := base; c.Algorithm = "MD5-sess"; c.QOP = "auth"; return c }(),
		"SHA-256-sess":    func() Credentials { c := base; c.Algorithm = "SHA-256-sess"; c.QOP = "auth"; return c }(),
		"SHA-512-256":     func() Credentials { c := base; c.Algorithm = "SHA-512-256"; c.QOP = "auth"; return c }(),
		"auth-int":        func() Credentials { c := base; c.QOP = "auth-int"; return c }(),
		"auth without nc": func() Credentials { c := base; c.QOP = "auth"; c.NC = ""; return c }(),
	} {
		if _, err := DigestResponse("00", REGISTER, c); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// RFC 3310 3.3: with AKAv1-MD5 the response is MD5 Digest whose
// password is RES, eight octets of any value from MILENAGE. Here RES is
// f2 of TS 35.208 test set 1.
func TestDigestResponseAKAv1MD5(t *testing.T) {
	md5hex := func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }
	res := string([]byte{0xa5, 0x42, 0x11, 0xd5, 0xe3, 0xba, 0x50, 0xbf})
	user := "001010000000010@" + imsDomain
	c := Credentials{Username: user, Realm: "scscf." + imsDomain, Nonce: "bm9uY2U=", URI: "sip:" + imsDomain,
		Algorithm: AlgorithmAKAv1MD5, QOP: "auth", NC: "00000001", CNonce: "0a4f113b"}
	ha1 := md5hex(user + ":scscf." + imsDomain + ":" + res)
	want := md5hex(ha1 + ":bm9uY2U=:00000001:0a4f113b:auth:" + md5hex("REGISTER:sip:"+imsDomain))
	if HA1(c.Algorithm, user, c.Realm, res) != ha1 {
		t.Fatal("HA1 does not take RES as it is")
	}
	got, err := DigestResponse(ha1, REGISTER, c)
	if err != nil || got != want {
		t.Errorf("response = %s (%v), want %s", got, err, want)
	}
}
