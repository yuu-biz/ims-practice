package sip

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestParseURI(t *testing.T) {
	tests := []struct {
		in   string
		want URI
	}{
		{"sip:" + imsDomain, URI{Scheme: "sip", Host: imsDomain}},
		{"sip:001010000000001@192.0.2.10:5060", URI{Scheme: "sip", User: "001010000000001", Host: "192.0.2.10", Port: 5060}},
		{"SIPS:alice@example.org", URI{Scheme: "sips", User: "alice", Host: "example.org"}},
		{"sip:[2001:db8::1]:5070;transport=udp;lr", URI{Scheme: "sip", Host: "2001:db8::1", Port: 5070,
			Params: Params{{"transport", "udp"}, {"lr", ""}}}},
		{"sip:+0010000000001@" + imsDomain + ";user=phone", URI{Scheme: "sip", User: "+0010000000001", Host: imsDomain,
			Params: Params{{"user", "phone"}}}},
		// RFC 3261 19.1.3: a ";" before "@" belongs to the user.
		{"sip:alice;day=tuesday@atlanta.com", URI{Scheme: "sip", User: "alice;day=tuesday", Host: "atlanta.com"}},
		{"sip:bob@biloxi.com?subject=project%20x&priority=urgent", URI{Scheme: "sip", User: "bob", Host: "biloxi.com",
			Headers: "subject=project%20x&priority=urgent"}},
	}
	for _, tt := range tests {
		got, err := ParseURI(tt.in)
		if err != nil {
			t.Errorf("ParseURI(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseURI(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestURIString(t *testing.T) {
	for _, s := range []string{
		"sip:001010000000001@192.0.2.10:5060",
		"sip:[2001:db8::1]:5070;transport=udp;lr",
		"sip:[2001:db8::1]",
		"sip:alice;day=tuesday@atlanta.com",
		"sips:bob@biloxi.com?subject=project%20x",
	} {
		u, err := ParseURI(s)
		if err != nil {
			t.Fatalf("ParseURI(%q): %v", s, err)
		}
		if got := u.String(); got != s {
			t.Errorf("String() = %q, want %q", got, s)
		}
	}
}

func TestParseURIRejects(t *testing.T) {
	if _, err := ParseURI("tel:+0010000000001"); !errors.Is(err, ErrNotSIPURI) {
		t.Errorf("tel URI: err = %v, want ErrNotSIPURI", err)
	}
	for _, s := range []string{"sip:", "sip:host:0", "sip:host:99999", "sip:[2001:db8::1"} {
		if _, err := ParseURI(s); err == nil {
			t.Errorf("ParseURI(%q) succeeded", s)
		}
	}
}

func TestParseNameAddr(t *testing.T) {
	tests := []struct {
		in   string
		want NameAddr
	}{
		{`"UE, test" <sip:ue@192.0.2.10>;expires=600000`,
			NameAddr{DisplayName: "UE, test", URI: "sip:ue@192.0.2.10", Params: Params{{"expires", "600000"}}}},
		{`Alice <sip:alice@atlanta.com;transport=udp>;tag=1928301774`,
			NameAddr{DisplayName: "Alice", URI: "sip:alice@atlanta.com;transport=udp", Params: Params{{"tag", "1928301774"}}}},
		{`<tel:+0010000000001>`, NameAddr{URI: "tel:+0010000000001"}},
		// RFC 3261 20: without <>, the parameters belong to the header.
		{`sip:ue@192.0.2.10;expires=60`, NameAddr{URI: "sip:ue@192.0.2.10", Params: Params{{"expires", "60"}}}},
		{`<sip:pcscf.example;lr>;+g.3gpp.icsi-ref="urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`,
			NameAddr{URI: "sip:pcscf.example;lr", Params: Params{{"+g.3gpp.icsi-ref", `"urn%3Aurn-7%3A3gpp-service.ims.icsi.mmtel"`}}}},
		{`"say \"hi\"" <sip:a@b>`, NameAddr{DisplayName: `say "hi"`, URI: "sip:a@b"}},
	}
	for _, tt := range tests {
		got, err := ParseNameAddr(tt.in)
		if err != nil {
			t.Errorf("ParseNameAddr(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseNameAddr(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{"", "*", "<sip:a@b", "<sip:a@b> junk"} {
		if _, err := ParseNameAddr(bad); err == nil {
			t.Errorf("ParseNameAddr(%q) succeeded", bad)
		}
	}
}

func TestNameAddrString(t *testing.T) {
	na := NameAddr{DisplayName: `say "hi"`, URI: "sip:a@b;lr", Params: Params{{"tag", "x"}, {"lr", ""}}}
	if got, want := na.String(), `"say \"hi\"" <sip:a@b;lr>;tag=x;lr`; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseVia(t *testing.T) {
	tests := []struct {
		in   string
		want Via
	}{
		{"SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bKnashds7;rport",
			Via{Transport: "UDP", Host: "192.0.2.10", Port: 5060, Params: Params{{"branch", "z9hG4bKnashds7"}, {"rport", ""}}}},
		{"SIP/2.0/UDP 192.0.2.10;received=203.0.113.9;rport=40000;branch=z9hG4bK1",
			Via{Transport: "UDP", Host: "192.0.2.10", Params: Params{{"received", "203.0.113.9"}, {"rport", "40000"}, {"branch", "z9hG4bK1"}}}},
		{"SIP / 2.0 / tcp  pcscf.example : 5060 ; branch=z9hG4bK2",
			Via{Transport: "TCP", Host: "pcscf.example", Port: 5060, Params: Params{{"branch", "z9hG4bK2"}}}},
		{"SIP/2.0/UDP [2001:db8::10]:5060;branch=z9hG4bK3",
			Via{Transport: "UDP", Host: "2001:db8::10", Port: 5060, Params: Params{{"branch", "z9hG4bK3"}}}},
	}
	for _, tt := range tests {
		got, err := ParseVia(tt.in)
		if err != nil {
			t.Errorf("ParseVia(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseVia(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
	for _, bad := range []string{"", "SIP/2.0/UDP", "SIP/1.0/UDP host", "HTTP/2.0/UDP host"} {
		if _, err := ParseVia(bad); err == nil {
			t.Errorf("ParseVia(%q) succeeded", bad)
		}
	}
}

func TestViaString(t *testing.T) {
	v, err := ParseVia("SIP/2.0/UDP [2001:db8::10]:5060;branch=z9hG4bK3;rport")
	if err != nil {
		t.Fatal(err)
	}
	if v.Branch() != "z9hG4bK3" {
		t.Errorf("Branch() = %q", v.Branch())
	}
	v.Params.Set("rport", "40000")
	v.Params.Set("received", "2001:db8::99")
	if got, want := v.String(), "SIP/2.0/UDP [2001:db8::10]:5060;branch=z9hG4bK3;rport=40000;received=2001:db8::99"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseCSeq(t *testing.T) {
	got, err := ParseCSeq(" 4711   REGISTER ")
	if err != nil || got != (CSeq{4711, REGISTER}) {
		t.Errorf("ParseCSeq = %+v, %v", got, err)
	}
	if got.String() != "4711 REGISTER" {
		t.Errorf("String() = %q", got.String())
	}
	for _, bad := range []string{"", "REGISTER", "x REGISTER", "1 REGISTER extra", "4294967296 REGISTER"} {
		if _, err := ParseCSeq(bad); err == nil {
			t.Errorf("ParseCSeq(%q) succeeded", bad)
		}
	}
}

func TestParseDeltaSeconds(t *testing.T) {
	for in, want := range map[string]uint32{"0": 0, "600000": 600000, "99999999999": math.MaxUint32} {
		if got, err := ParseDeltaSeconds(in); err != nil || got != want {
			t.Errorf("ParseDeltaSeconds(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "-1", "1.5", "60s"} {
		if _, err := ParseDeltaSeconds(bad); err == nil {
			t.Errorf("ParseDeltaSeconds(%q) succeeded", bad)
		}
	}
}

func TestParseMaxForwards(t *testing.T) {
	if n, err := ParseMaxForwards("70"); err != nil || n != 70 {
		t.Errorf("ParseMaxForwards(70) = %d, %v", n, err)
	}
	for _, bad := range []string{"256", "-1", "x"} {
		if _, err := ParseMaxForwards(bad); err == nil {
			t.Errorf("ParseMaxForwards(%q) succeeded", bad)
		}
	}
}

func TestCredentialsWithEmptyNonceRoundTrip(t *testing.T) {
	c, err := ParseCredentials(initialRegisterAuthorization)
	if err != nil {
		t.Fatal(err)
	}
	want := Credentials{
		Username: "001010000000001@" + imsDomain,
		Realm:    imsDomain,
		URI:      "sip:" + imsDomain,
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("ParseCredentials = %+v, want %+v", c, want)
	}
	if got := c.String(); got != initialRegisterAuthorization {
		t.Errorf("String() =\n%s\nwant\n%s", got, initialRegisterAuthorization)
	}
}

func TestCredentialsWithQOP(t *testing.T) {
	// RFC 2617 3.5, with an extra auth-param of the kind IMS adds.
	in := `Digest username="Mufasa", realm="testrealm@host.com", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", ` +
		`uri="/dir/index.html", qop=auth, nc=00000001, cnonce="0a4f113b", ` +
		`response="6629fae49393a05397450978507c4ef1", opaque="5ccc069c403ebaf9f0171e9517f40e41", integrity-protected="no"`
	c, err := ParseCredentials(in)
	if err != nil {
		t.Fatal(err)
	}
	want := Credentials{
		Username: "Mufasa", Realm: "testrealm@host.com", Nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		URI: "/dir/index.html", Response: "6629fae49393a05397450978507c4ef1",
		CNonce: "0a4f113b", Opaque: "5ccc069c403ebaf9f0171e9517f40e41", QOP: "auth", NC: "00000001",
		AuthParams: Params{{"integrity-protected", `"no"`}},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("ParseCredentials = %+v, want %+v", c, want)
	}
	again, err := ParseCredentials(c.String())
	if err != nil || !reflect.DeepEqual(again, c) {
		t.Errorf("String() does not parse back: %q", c.String())
	}
}

func TestChallenge(t *testing.T) {
	in := `Digest realm="testrealm@host.com", qop="auth,auth-int", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", ` +
		`opaque="5ccc069c403ebaf9f0171e9517f40e41", stale=TRUE, ck="00112233", ik="44556677"`
	c, err := ParseChallenge(in)
	if err != nil {
		t.Fatal(err)
	}
	want := Challenge{
		Realm: "testrealm@host.com", Nonce: "dcd98b7102dd2f0e8b11d0f600bfb0c093",
		Opaque: "5ccc069c403ebaf9f0171e9517f40e41", Stale: true, QOP: []string{"auth", "auth-int"},
		AuthParams: Params{{"ck", `"00112233"`}, {"ik", `"44556677"`}},
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("ParseChallenge = %+v, want %+v", c, want)
	}
	wantString := `Digest realm="testrealm@host.com", nonce="dcd98b7102dd2f0e8b11d0f600bfb0c093", ` +
		`opaque="5ccc069c403ebaf9f0171e9517f40e41", stale=true, qop="auth,auth-int", ck="00112233", ik="44556677"`
	if got := c.String(); got != wantString {
		t.Errorf("String() =\n%s\nwant\n%s", got, wantString)
	}
	if _, err := ParseChallenge(`Basic realm="x"`); err == nil {
		t.Error("ParseChallenge accepted Basic")
	}
}

// TS 24.229 7.2A.1: the S-CSCF adds IK and CK for the P-CSCF, which
// takes them out again.
func TestChallengeKeys(t *testing.T) {
	c := Challenge{Realm: "scscf." + imsDomain, Nonce: "bm9uY2U=", Algorithm: AlgorithmAKAv1MD5, QOP: []string{"auth"}}
	if _, _, ok := c.Keys(); ok {
		t.Error("a challenge without keys has them")
	}
	c.SetKeys("0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210")
	want := `Digest realm="scscf.` + imsDomain + `", nonce="bm9uY2U=", algorithm=AKAv1-MD5, qop="auth", ` +
		`ik="0123456789abcdef0123456789abcdef", ck="fedcba9876543210fedcba9876543210"`
	if got := c.String(); got != want {
		t.Errorf("String() =\n%s\nwant\n%s", got, want)
	}
	parsed, err := ParseChallenge(want)
	if err != nil {
		t.Fatal(err)
	}
	if ik, ck, ok := parsed.Keys(); !ok || ik != "0123456789abcdef0123456789abcdef" || ck != "fedcba9876543210fedcba9876543210" {
		t.Errorf("Keys() = %s, %s, %v", ik, ck, ok)
	}
	parsed.RemoveKeys()
	if got := parsed.String(); strings.Contains(got, "ik=") || strings.Contains(got, "ck=") {
		t.Errorf("after RemoveKeys: %s", got)
	}
}

func TestIntegrityProtectedAndAUTS(t *testing.T) {
	c, err := ParseCredentials(`Digest username="u", realm="r", nonce="n", uri="sip:r", response="", ` +
		`auts="CjkyMzRfOiwg5CfkJ2UK=", integrity-protected=no`)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.IntegrityProtected(); got != IntegrityProtectedNo {
		t.Errorf("IntegrityProtected() = %q, want no", got)
	}
	if auts, ok := c.AUTS(); !ok || auts != "CjkyMzRfOiwg5CfkJ2UK=" {
		t.Errorf("AUTS() = %q, %v", auts, ok)
	}
	c.SetIntegrityProtected(IntegrityProtectedYes)
	if !strings.HasSuffix(c.String(), `auts="CjkyMzRfOiwg5CfkJ2UK=", integrity-protected="yes"`) {
		t.Errorf("String() = %s", c.String())
	}
	c.SetIntegrityProtected("")
	if strings.Contains(c.String(), "integrity-protected") || c.IntegrityProtected() != "" {
		t.Errorf("after removing: %s", c.String())
	}
	var fresh Credentials
	fresh.SetAUTS("5PYxMuX2NOT2NeQ=")
	if !strings.HasSuffix(fresh.String(), `auts="5PYxMuX2NOT2NeQ="`) {
		t.Errorf("String() = %s", fresh.String())
	}
}
