package scscf

import (
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/hss"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// testSet1 is K and OPc of MILENAGE test set 1 (TS 35.208 4.3.1).
var testSet1 = aka.Secret{
	K:   [16]byte{0x46, 0x5b, 0x5c, 0xe8, 0xb1, 0x99, 0xb4, 0x9f, 0xaa, 0x5f, 0x0a, 0x2e, 0xe2, 0x38, 0xa6, 0xbc},
	OPc: [16]byte{0xcd, 0x63, 0xcb, 0x71, 0x95, 0x4a, 0x9f, 0x4e, 0x48, 0xa5, 0x99, 0x4e, 0x37, 0xa0, 0x2b, 0xaf},
}

func newAKAHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithHSS(t, Config{}, nil, hss.Config{},
		hss.Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, AKA: &hss.AKACredentials{Secret: testSet1}})
}

// akaInitial is the first REGISTER of TS 24.229 5.1.1.2.2 as the P-CSCF
// forwards it: empty nonce and response, integrity-protected "no"
// (5.2.2.2 item 1).
var akaInitial = `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain +
	`", response="", algorithm=AKAv1-MD5, integrity-protected="no"`

// akaChallenge reads the 401 of 5.4.1.2.1A.
func akaChallenge(t *testing.T, r *sip.Message) sip.Challenge {
	t.Helper()
	if r.StatusCode != 401 {
		t.Fatalf("got %d, want 401", r.StatusCode)
	}
	ch, err := sip.ParseChallenge(r.Get("WWW-Authenticate"))
	if err != nil {
		t.Fatal(err)
	}
	if ch.Algorithm != sip.AlgorithmAKAv1MD5 || ch.Realm != "scscf."+domain || len(ch.QOP) != 1 || ch.QOP[0] != "auth" {
		t.Fatalf("WWW-Authenticate = %s", r.Get("WWW-Authenticate"))
	}
	return ch
}

// akaAnswer is the Authorization of the REGISTER that answers ch,
// computed with password, as the P-CSCF forwards it with integrity.
func akaAnswer(ch sip.Challenge, password string, integrity sip.IntegrityProtected) sip.Credentials {
	c := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain,
		Algorithm: sip.AlgorithmAKAv1MD5, CNonce: "0a4f113b", QOP: "auth", NC: "00000001"}
	c.Response, _ = sip.DigestResponse(sip.HA1(impi, ch.Realm, password), sip.REGISTER, c)
	c.SetIntegrityProtected(integrity)
	return c
}

// authenticate runs the USIM on the challenge (TS 33.203 6.1.1, SM6).
func authenticate(t *testing.T, usim *aka.USIM, ch sip.Challenge) (aka.Response, error) {
	t.Helper()
	rand, autn, err := aka.ParseNonce(ch.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	return usim.Authenticate(rand, autn)
}

// TS 33.203 6.1.1 as the S-CSCF sees it: a challenge with RAND and AUTN
// for the UE and IK and CK for the P-CSCF, the response checked against
// XRES, and then a protected re-registration and deregistration that
// need no new challenge (TS 24.229 5.4.1.2.2).
func TestRegistrationWithIMSAKA(t *testing.T) {
	h := newAKAHarness(t)
	usim := aka.NewUSIM(testSet1, 0)
	callID := sip.NewCallID()

	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	res, err := authenticate(t, usim, ch)
	if err != nil {
		t.Fatal(err)
	}
	if ik, ck, ok := ch.Keys(); !ok || ik != hex.EncodeToString(res.IK[:]) || ck != hex.EncodeToString(res.CK[:]) {
		t.Errorf("ik %s, ck %s; want the IK and CK the USIM derives", ik, ck)
	}

	creds := akaAnswer(ch, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes)
	r := h.register(callID, 2, 600000, creds.String())
	if r.StatusCode != 200 {
		t.Fatalf("answer got %d, want 200", r.StatusCode)
	}
	// RFC 3310 3.5: rspauth with XRES as the password.
	rspauth, _ := sip.DigestResponse(sip.HA1(impi, ch.Realm, aka.DigestPassword(res.RES)), "", creds)
	if got := r.Get("Authentication-Info"); !strings.Contains(got, `rspauth="`+rspauth+`"`) {
		t.Errorf("Authentication-Info = %q, want rspauth %s", got, rspauth)
	}

	// 5.1.1.4.2: a re-registration repeats the last nonce and response.
	r = h.register(callID, 3, 600000, creds.String())
	if r.StatusCode != 200 || r.Get("Authentication-Info") != "" {
		t.Fatalf("protected re-registration got %d, Authentication-Info %q; want 200 and none", r.StatusCode, r.Get("Authentication-Info"))
	}
	if r := h.register(callID, 4, 0, creds.String()); r.StatusCode != 200 {
		t.Fatalf("protected deregistration got %d, want 200", r.StatusCode)
	}
	want := []cx.ServerAssignmentType{cx.AssignRegistration, cx.ReRegistration, cx.UserDeregistration}
	if got := h.hss.assignments(); !slices.Equal(got, want) {
		t.Errorf("SARs = %v, want %v", got, want)
	}
}

// TS 33.203 6.1.3: a USIM ahead of the HSS answers with auts; the
// S-CSCF passes it with the stored RAND to the HSS, and the new
// challenge is one the USIM takes.
func TestIMSAKAResynchronisation(t *testing.T) {
	h := newAKAHarness(t)
	// The first vector of the HSS has SQN 1 with IND 1 (TS 33.102 C.3.4),
	// which this USIM has seen beaten.
	usim := aka.NewUSIM(testSet1, aka.NewSQN(5000, 1))
	callID := sip.NewCallID()

	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	_, err := authenticate(t, usim, ch)
	var failure aka.SynchronisationFailure
	if !errors.As(err, &failure) {
		t.Fatalf("USIM: %v, want a synchronisation failure", err)
	}
	// RFC 3310 3.4: auts, and a response with the empty password.
	resync := akaAnswer(ch, "", sip.IntegrityProtectedNo)
	resync.SetAUTS(aka.EncodeAUTS(failure.AUTS))
	again := akaChallenge(t, h.register(callID, 2, 600000, resync.String()))
	if again.Nonce == ch.Nonce {
		t.Fatal("the second challenge repeats the first")
	}
	res, err := authenticate(t, usim, again)
	if err != nil {
		t.Fatalf("USIM refused the challenge after the resynchronisation: %v", err)
	}
	if r := h.register(callID, 3, 600000, akaAnswer(again, aka.DigestPassword(res.RES), sip.IntegrityProtectedYes).String()); r.StatusCode != 200 {
		t.Fatalf("answer got %d, want 200", r.StatusCode)
	}
}

// TS 33.203 6.1.2.2 and TS 24.229 5.1.1.5.3: a UE that cannot
// authenticate the network sends neither response nor auts; the S-CSCF
// answers 403 and clears its name in the HSS.
func TestIMSAKANetworkAuthenticationFailure(t *testing.T) {
	h := newAKAHarness(t)
	stranger := testSet1
	stranger.K[0] ^= 1
	callID := sip.NewCallID()

	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	if _, err := authenticate(t, aka.NewUSIM(stranger, 0), ch); !errors.Is(err, aka.ErrMACFailure) {
		t.Fatalf("USIM with another K: %v, want ErrMACFailure", err)
	}
	failure := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain, Algorithm: sip.AlgorithmAKAv1MD5}
	failure.SetIntegrityProtected(sip.IntegrityProtectedNo)
	if r := h.register(callID, 2, 600000, failure.String()); r.StatusCode != 403 {
		t.Fatalf("got %d, want 403", r.StatusCode)
	}
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AuthenticationFailure}) {
		t.Errorf("SARs = %v, want AUTHENTICATION_FAILURE", got)
	}
	// The challenge is no longer pending: the same REGISTER again is
	// one that starts an initial registration (5.4.1.2.1), and gets a
	// new challenge rather than a second 403.
	if r := h.register(callID, 3, 600000, failure.String()); r.StatusCode != 401 {
		t.Errorf("the same REGISTER again got %d, want 401", r.StatusCode)
	}
}

// TS 24.229 5.4.1.2.3A: a wrong response, or the right one on another
// Call-ID, gets 403.
func TestIMSAKAWrongAnswersAreForbidden(t *testing.T) {
	for name, tc := range map[string]struct {
		password func(res []byte) string
		callID   func(first string) string
	}{
		"wrong response": {func([]byte) string { return "not RES" }, func(first string) string { return first }},
		"other Call-ID":  {aka.DigestPassword, func(string) string { return sip.NewCallID() }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAKAHarness(t)
			callID := sip.NewCallID()
			ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
			res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
			if err != nil {
				t.Fatal(err)
			}
			answer := akaAnswer(ch, tc.password(res.RES), sip.IntegrityProtectedYes)
			if r := h.register(tc.callID(callID), 2, 600000, answer.String()); r.StatusCode != 403 {
				t.Fatalf("got %d, want 403", r.StatusCode)
			}
		})
	}
}

// TS 24.229 5.4.1.2.3A: a REGISTER that says it came protected, from a
// user that this S-CSCF has not registered, gets 500.
func TestProtectedREGISTERFromAStranger(t *testing.T) {
	h := newAKAHarness(t)
	creds := akaAnswer(sip.Challenge{Realm: "scscf." + domain, Nonce: "bm9uY2U="}, "x", sip.IntegrityProtectedYes)
	if r := h.register(sip.NewCallID(), 1, 600000, creds.String()); r.StatusCode != 500 {
		t.Fatalf("got %d, want 500", r.StatusCode)
	}
}

// TS 29.228 6.3.1 step 4: a subscriber with a password only has no
// vector to give, so an IMS AKA REGISTER of that user is refused.
func TestIMSAKAForASubscriberWithoutAKA(t *testing.T) {
	h := newHarness(t, Config{})
	if r := h.register(sip.NewCallID(), 1, 600000, akaInitial); r.StatusCode != 403 {
		t.Fatalf("got %d, want 403", r.StatusCode)
	}
}
