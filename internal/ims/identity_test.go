package ims

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

const domain = "ims.mnc001.mcc001.3gppnetwork.org"

func TestIdentities(t *testing.T) {
	publicID, err := PublicIdentity(`"UE" <sip:001010000000001@` + domain + `:5060;user=phone>;tag=1`)
	if err != nil {
		t.Fatal(err)
	}
	if publicID != "sip:001010000000001@"+domain {
		t.Errorf("public identity = %q", publicID)
	}

	// Without Authorization the private identity is derived from To.
	req := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	privateID, creds, err := PrivateIdentity(req, publicID)
	if err != nil || creds != nil || privateID != "001010000000001@"+domain {
		t.Errorf("derived private identity = %q, %v, %v", privateID, creds, err)
	}

	req.Add("Authorization", `Digest username="impi@`+domain+`", realm="`+domain+`", nonce="", uri="sip:`+domain+`", response=""`)
	privateID, creds, err = PrivateIdentity(req, publicID)
	if err != nil || creds == nil || privateID != "impi@"+domain {
		t.Errorf("private identity from Authorization = %q, %v", privateID, err)
	}
}

func TestCanonicalURI(t *testing.T) {
	tests := map[string]string{
		"tel:+1-202-555-0101;phone-context=example": "tel:+12025550101",
		"TEL:+12025550101":                          "tel:+12025550101",
		"sip:alice@EXAMPLE.org:5060;transport=udp":  "sip:alice@example.org",
		"SIP:%61lice@example.org":                   "sip:alice@example.org",
		"sip:a%40b@example.org":                     "sip:a%40b@example.org",
	}
	for in, want := range tests {
		if got, err := CanonicalURI(in); err != nil || got != want {
			t.Errorf("CanonicalURI(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := CanonicalURI("tel:5550101"); err == nil {
		t.Error("a local number without + was accepted")
	}
}

// TestSamePublicIdentity takes its pairs from the examples of RFC 3261
// 19.1.4 and RFC 3966. The canonical form of a public identity has no
// port and no parameters, so URIs that differ only in those name one
// identity, although as SIP URIs they are not equivalent.
func TestSamePublicIdentity(t *testing.T) {
	same := [][2]string{
		{"sip:%61lice@atlanta.com;transport=TCP", "sip:alice@AtLanTa.CoM;Transport=tcp"},
		{"sip:carol@chicago.com", "sip:carol@chicago.com;newparam=5"},
		{"sip:carol@chicago.com", "sip:carol@chicago.com;security=on"},
		{"sip:bob@biloxi.com", "sip:bob@biloxi.com:5060"},
		{"tel:+1-201-555-0123", "tel:+12015550123"},
		{"tel:+12015550123", "TEL:+1.201.555.0123"},
	}
	for _, p := range same {
		if !SamePublicIdentity(p[0], p[1]) {
			t.Errorf("%s and %s are not the same identity", p[0], p[1])
		}
		if PublicIdentityKey(p[0]) != PublicIdentityKey(p[1]) {
			t.Errorf("%s and %s have the keys %q and %q", p[0], p[1], PublicIdentityKey(p[0]), PublicIdentityKey(p[1]))
		}
	}
	different := [][2]string{
		// 19.1.4: different usernames, since the userinfo compares with case.
		{"SIP:ALICE@AtLanTa.CoM;Transport=udp", "sip:alice@AtLanTa.CoM;Transport=UDP"},
		{"sip:bob@phone21.boxesbybob.com", "sip:bob@192.0.2.4"},
		{"sip:alice@atlanta.com", "sips:alice@atlanta.com"},
		{"sip:+12015550123@atlanta.com;user=phone", "tel:+12015550123"},
		{"sip:alice@atlanta.com", "not a URI"},
	}
	for _, p := range different {
		if SamePublicIdentity(p[0], p[1]) {
			t.Errorf("%s and %s are the same identity", p[0], p[1])
		}
		if PublicIdentityKey(p[0]) == PublicIdentityKey(p[1]) {
			t.Errorf("%s and %s have the same key %q", p[0], p[1], PublicIdentityKey(p[0]))
		}
	}
}
