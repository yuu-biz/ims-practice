package aka

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNonceCarriesRANDAndAUTN(t *testing.T) {
	av := NewAuthenticationVector(set1Secret, set1RAND, set1SQN, set1AMF)
	nonce := Nonce(av.RAND, av.AUTN)
	raw, err := base64.StdEncoding.DecodeString(nonce)
	if err != nil || len(raw) != 32 || hexOf(raw[:16]) != hexOf(set1RAND[:]) || hexOf(raw[16:]) != hexOf(av.AUTN[:]) {
		t.Fatalf("nonce %q decodes to %x, %v; want RAND || AUTN", nonce, raw, err)
	}
	rand, autn, err := ParseNonce(nonce)
	if err != nil || rand != av.RAND || autn != av.AUTN {
		t.Fatalf("ParseNonce = %x %x %v", rand, autn, err)
	}
}

func TestParseNonceIgnoresServerData(t *testing.T) {
	raw := append(append(set1RAND[:], make([]byte, 16)...), "server data"...)
	rand, _, err := ParseNonce(base64.StdEncoding.EncodeToString(raw))
	if err != nil || rand != set1RAND {
		t.Fatalf("ParseNonce = %x, %v", rand, err)
	}
}

func TestParseNonceRefusesShortNonce(t *testing.T) {
	if _, _, err := ParseNonce(base64.StdEncoding.EncodeToString(make([]byte, 31))); err == nil {
		t.Fatal("a 31-octet nonce was accepted")
	}
	if _, _, err := ParseNonce("not base64!"); err == nil {
		t.Fatal("a nonce that is not base64 was accepted")
	}
}

func TestAUTSRoundTrip(t *testing.T) {
	auts := newAUTS(set1Secret.milenage(), set1RAND, NewSQN(42, 1))
	got, err := ParseAUTS(EncodeAUTS(auts))
	if err != nil || got != auts {
		t.Fatalf("ParseAUTS = %x, %v; want %x", got, err, auts)
	}
	if _, err := ParseAUTS(base64.StdEncoding.EncodeToString(make([]byte, 13))); err == nil || !strings.Contains(err.Error(), "13 octets") {
		t.Fatalf("a 13-octet auts: %v", err)
	}
}

// RFC 4169 2.1: the AKAv2 password is the base64 of
// PRF(RES || IK || CK, "http-digest-akav2-password"), the PRF HMAC with
// the hash of the digest. RES, IK and CK are the outputs that TS 35.208
// 4.3.1 lists for test set 1, and the expected password is
//
//	python3 -c 'import base64, hashlib, hmac;
//	  print(base64.b64encode(hmac.new(bytes.fromhex(
//	  "a54211d5e3ba50bf" "f769bcd751044604127672711c6d3441"
//	  "b40ba9a3c58b2a05bbf0d987b21bf8cb"),
//	  b"http-digest-akav2-password", hashlib.sha256).digest()).decode())'
//
// The RFC gives no test vector of its own.
func TestAKAv2DigestPasswordFromTestSet1(t *testing.T) {
	av := NewAuthenticationVector(set1Secret, set1RAND, set1SQN, set1AMF)
	want := "wpC+5dv/7kC35JEI2hluv8XcBd+vf8XcfedJBan1buk="
	if got := AKAv2DigestPassword(av.XRES, av.IK, av.CK); got != want {
		t.Errorf("password = %s, want %s", got, want)
	}
}

// The password binds RES, IK and CK, so a holder of RES alone cannot
// compute it (RFC 4169 1).
func TestAKAv2DigestPasswordBindsTheSessionKeys(t *testing.T) {
	av := NewAuthenticationVector(set1Secret, set1RAND, set1SQN, set1AMF)
	password := AKAv2DigestPassword(av.XRES, av.IK, av.CK)
	for name, other := range map[string]string{
		"another RES":       AKAv2DigestPassword(append([]byte{^av.XRES[0]}, av.XRES[1:]...), av.IK, av.CK),
		"another IK":        AKAv2DigestPassword(av.XRES, IK{}, av.CK),
		"another CK":        AKAv2DigestPassword(av.XRES, av.IK, CK{}),
		"IK and CK swapped": AKAv2DigestPassword(av.XRES, IK(av.CK), CK(av.IK)),
	} {
		if other == password {
			t.Errorf("%s gives the same password", name)
		}
	}
}

func TestDigestPasswordIsTheOctetsOfRES(t *testing.T) {
	res := []byte{0xa5, 0x42, 0x11, 0xd5, 0xe3, 0xba, 0x50, 0xbf}
	if got := DigestPassword(res); got != "\xa5\x42\x11\xd5\xe3\xba\x50\xbf" {
		t.Fatalf("password = %q", got)
	}
	if DigestPassword(nil) != "" {
		t.Fatal("the auts response must use the empty password")
	}
}
