package scscf

import (
	"context"
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

// akaInitial is the first REGISTER of TS 24.229 5.1.1.2.2 item a as the
// P-CSCF forwards it: empty nonce and response, no algorithm, and
// integrity-protected "no" (5.2.2.2 item 1).
var akaInitial = `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain +
	`", response="", integrity-protected="no"`

// akaInitialNaming is akaInitial with an algorithm named, as a UE that
// speaks one AKA version alone sends it (5.4.1.2.1A item 1 c).
func akaInitialNaming(algorithm string) string {
	return `Digest username="` + impi + `", realm="` + domain + `", nonce="", uri="sip:` + domain +
		`", response="", algorithm=` + algorithm + `, integrity-protected="no"`
}

// akaChallenges reads the challenges of the 401 of 5.4.1.2.1A, in the
// order the S-CSCF offers them, each with IK and CK for the P-CSCF.
func akaChallenges(t *testing.T, r *sip.Message) []sip.Challenge {
	t.Helper()
	if r.StatusCode != 401 {
		t.Fatalf("got %d, want 401", r.StatusCode)
	}
	var challenges []sip.Challenge
	for _, v := range r.GetAll("WWW-Authenticate") {
		ch, err := sip.ParseChallenge(v)
		if err != nil {
			t.Fatal(err)
		}
		if ch.Realm != "scscf."+domain || len(ch.QOP) != 1 || ch.QOP[0] != "auth" {
			t.Fatalf("WWW-Authenticate = %s", v)
		}
		challenges = append(challenges, ch)
	}
	if len(challenges) == 0 {
		t.Fatal("the 401 carries no WWW-Authenticate")
	}
	return challenges
}

// akaChallenge is the challenge of the 401 that a UE answers: the
// topmost one it supports (TS 24.229 5.1.1.5.1).
func akaChallenge(t *testing.T, r *sip.Message) sip.Challenge {
	t.Helper()
	return akaChallenges(t, r)[0]
}

// akaPassword is the Digest password of the AKA version that ch names:
// RES for AKAv1-MD5 (RFC 3310 3.3), and RES with IK and CK for
// AKAv2-SHA-256 (RFC 4169 2.1).
func akaPassword(ch sip.Challenge, res aka.Response) string {
	if ch.Algorithm == sip.AlgorithmAKAv2SHA256 {
		return aka.AKAv2DigestPassword(res.RES, res.IK, res.CK)
	}
	return aka.DigestPassword(res.RES)
}

// akaAnswer is the Authorization of the REGISTER that answers ch,
// computed with password, as the P-CSCF forwards it with integrity.
func akaAnswer(ch sip.Challenge, password string, integrity sip.IntegrityProtected) sip.Credentials {
	c := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain,
		Algorithm: ch.Algorithm, CNonce: "0a4f113b", QOP: "auth", NC: "00000001"}
	c.Response, _ = sip.DigestResponse(sip.HA1(c.Algorithm, impi, ch.Realm, password), sip.REGISTER, c)
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

	creds := akaAnswer(ch, akaPassword(ch, res), sip.IntegrityProtectedYes)
	r := h.register(callID, 2, 600000, creds.String())
	if r.StatusCode != 200 {
		t.Fatalf("answer got %d, want 200", r.StatusCode)
	}
	// RFC 3310 3.5: rspauth with XRES as the password.
	rspauth, _ := sip.DigestResponse(sip.HA1(creds.Algorithm, impi, ch.Realm, akaPassword(ch, res)), "", creds)
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

// TS 33.203 6.1.1 and TS 24.229 5.4.1.2.1A item 1 c: the 401 to a
// REGISTER that names no algorithm offers both algorithms of IMS AKA,
// AKAv2-SHA-256 first, with IK and CK in each; the 401 to one that names
// an algorithm of IMS AKA offers that one alone, which is what an
// independent peer that speaks AKAv1-MD5 only receives.
func TestIMSAKAOfferedAlgorithms(t *testing.T) {
	for name, tc := range map[string]struct {
		initial string
		want    []string
	}{
		"no algorithm named": {akaInitial, []string{sip.AlgorithmAKAv2SHA256, sip.AlgorithmAKAv1MD5}},
		"AKAv1-MD5 named":    {akaInitialNaming(sip.AlgorithmAKAv1MD5), []string{sip.AlgorithmAKAv1MD5}},
		"AKAv2-SHA-256 named": {akaInitialNaming(sip.AlgorithmAKAv2SHA256),
			[]string{sip.AlgorithmAKAv2SHA256}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAKAHarness(t)
			challenges := akaChallenges(t, h.register(sip.NewCallID(), 1, 600000, tc.initial))
			var got []string
			for _, ch := range challenges {
				got = append(got, ch.Algorithm)
				// 5.4.1.2.1A item 1 c: each challenge carries the keys, so
				// the P-CSCF finds them whichever one the UE answers.
				if _, _, ok := ch.Keys(); !ok {
					t.Errorf("the %s challenge has no ik and ck", ch.Algorithm)
				}
				if ch.Nonce != challenges[0].Nonce {
					t.Errorf("the %s challenge has another nonce: one vector gives one nonce", ch.Algorithm)
				}
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("the 401 offers %v, want %v", got, tc.want)
			}
		})
	}
}

// TS 33.203 6.1.1 with each version of Digest AKA: the S-CSCF accepts
// the response of the algorithm the UE answered with, and the rspauth
// of the 200 is computed with the same password (RFC 3310 3.5, RFC 4169
// 2.1).
func TestIMSAKAAcceptsTheAlgorithmTheUEChose(t *testing.T) {
	for _, algorithm := range []string{sip.AlgorithmAKAv2SHA256, sip.AlgorithmAKAv1MD5} {
		t.Run(algorithm, func(t *testing.T) {
			h := newAKAHarness(t)
			callID := sip.NewCallID()
			// A UE that names no algorithm is offered both and answers the
			// topmost it supports (TS 24.229 5.1.1.5.1); the challenge of
			// the other algorithm carries the same nonce.
			challenges := akaChallenges(t, h.register(callID, 1, 600000, akaInitial))
			i := slices.IndexFunc(challenges, func(ch sip.Challenge) bool { return ch.Algorithm == algorithm })
			if i < 0 {
				t.Fatalf("the 401 offers no %s", algorithm)
			}
			ch := challenges[i]
			res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
			if err != nil {
				t.Fatal(err)
			}
			creds := akaAnswer(ch, akaPassword(ch, res), sip.IntegrityProtectedYes)
			r := h.register(callID, 2, 600000, creds.String())
			if r.StatusCode != 200 {
				t.Fatalf("answer got %d, want 200", r.StatusCode)
			}
			rspauth, _ := sip.DigestResponse(sip.HA1(algorithm, impi, ch.Realm, akaPassword(ch, res)), "", creds)
			if got := r.Get("Authentication-Info"); !strings.Contains(got, `rspauth="`+rspauth+`"`) {
				t.Errorf("Authentication-Info = %q, want rspauth %s", got, rspauth)
			}
		})
	}
}

// TS 33.203 6.1.3: a USIM ahead of the HSS answers with auts; the
// S-CSCF passes it with the stored RAND to the HSS, and the new
// challenge is one the USIM takes. The REGISTER with auts names the
// algorithm the UE chose, so the new 401 offers that one (TS 24.229
// 5.4.1.2.1A item 1 c), and its response uses the empty password
// whichever version it is (RFC 3310 3.4).
func TestIMSAKAResynchronisation(t *testing.T) {
	for name, tc := range map[string]struct {
		initial string
		want    string
	}{
		"AKAv2-SHA-256": {akaInitial, sip.AlgorithmAKAv2SHA256},
		"AKAv1-MD5":     {akaInitialNaming(sip.AlgorithmAKAv1MD5), sip.AlgorithmAKAv1MD5},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAKAHarness(t)
			// The first vector of the HSS has SQN 1 with IND 1 (TS 33.102
			// C.3.4), which this USIM has seen beaten.
			usim := aka.NewUSIM(testSet1, aka.NewSQN(5000, 1))
			callID := sip.NewCallID()

			ch := akaChallenge(t, h.register(callID, 1, 600000, tc.initial))
			if ch.Algorithm != tc.want {
				t.Fatalf("the challenge names %s, want %s", ch.Algorithm, tc.want)
			}
			_, err := authenticate(t, usim, ch)
			var failure aka.SynchronisationFailure
			if !errors.As(err, &failure) {
				t.Fatalf("USIM: %v, want a synchronisation failure", err)
			}
			// RFC 3310 3.4: auts, and a response with the empty password.
			resync := akaAnswer(ch, "", sip.IntegrityProtectedNo)
			resync.SetAUTS(aka.EncodeAUTS(failure.AUTS))
			challenges := akaChallenges(t, h.register(callID, 2, 600000, resync.String()))
			again := challenges[0]
			if len(challenges) != 1 || again.Algorithm != tc.want {
				t.Fatalf("the second 401 offers %d challenges, the first %s; want one %s", len(challenges), again.Algorithm, tc.want)
			}
			if again.Nonce == ch.Nonce {
				t.Fatal("the second challenge repeats the first")
			}
			res, err := authenticate(t, usim, again)
			if err != nil {
				t.Fatalf("USIM refused the challenge after the resynchronisation: %v", err)
			}
			if r := h.register(callID, 3, 600000, akaAnswer(again, akaPassword(again, res), sip.IntegrityProtectedYes).String()); r.StatusCode != 200 {
				t.Fatalf("answer got %d, want 200", r.StatusCode)
			}
		})
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
	failure := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain, Algorithm: ch.Algorithm}
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

// TS 24.229 5.4.1.2.3A: a wrong response, the right one on another
// Call-ID, and one computed for an algorithm the 401 did not offer, all
// get 403. The last is the response of AKAv1-MD5 to a 401 that offered
// AKAv2-SHA-256 alone, which a UE would send to have the weaker version
// accepted (RFC 4169 4.1).
func TestIMSAKAWrongAnswersAreForbidden(t *testing.T) {
	for name, tc := range map[string]struct {
		initial  string
		password func(ch sip.Challenge, res aka.Response) string
		answered func(ch sip.Challenge) sip.Challenge
		callID   func(first string) string
	}{
		"wrong response": {
			akaInitial,
			func(sip.Challenge, aka.Response) string { return "not RES" },
			func(ch sip.Challenge) sip.Challenge { return ch },
			func(first string) string { return first },
		},
		"other Call-ID": {
			akaInitial, akaPassword,
			func(ch sip.Challenge) sip.Challenge { return ch },
			func(string) string { return sip.NewCallID() },
		},
		"an algorithm the 401 did not offer": {
			akaInitialNaming(sip.AlgorithmAKAv2SHA256),
			func(_ sip.Challenge, res aka.Response) string { return aka.DigestPassword(res.RES) },
			func(ch sip.Challenge) sip.Challenge { ch.Algorithm = sip.AlgorithmAKAv1MD5; return ch },
			func(first string) string { return first },
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newAKAHarness(t)
			callID := sip.NewCallID()
			ch := akaChallenge(t, h.register(callID, 1, 600000, tc.initial))
			res, err := authenticate(t, aka.NewUSIM(testSet1, 0), ch)
			if err != nil {
				t.Fatal(err)
			}
			answer := akaAnswer(tc.answered(ch), tc.password(ch, res), sip.IntegrityProtectedYes)
			if r := h.register(tc.callID(callID), 2, 600000, answer.String()); r.StatusCode != 403 {
				t.Fatalf("got %d, want 403", r.StatusCode)
			}
		})
	}
}

// TS 24.229 5.4.1.2.2 step 3: a response whose algorithm the 401 did not
// offer is refused before any digest is compared. The challenge holds no
// H(A1) for such an algorithm, so a response computed over the empty
// one, which anybody can compute without a credential of the user, must
// not be taken for the expected response.
func TestIMSAKARefusesAnAlgorithmWithoutAnExpectedResponse(t *testing.T) {
	h := newAKAHarness(t)
	callID := sip.NewCallID()
	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	c := sip.Credentials{Username: impi, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + domain,
		Algorithm: "MD5", CNonce: "0a4f113b", QOP: "auth", NC: "00000001"}
	c.Response, _ = sip.DigestResponse("", sip.REGISTER, c)
	c.SetIntegrityProtected(sip.IntegrityProtectedYes)
	if r := h.register(callID, 2, 600000, c.String()); r.StatusCode != 403 {
		t.Fatalf("got %d, want 403", r.StatusCode)
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

// RFC 3261 10.2.3 with TS 24.229 5.1.1.4.1: a protected REGISTER without
// a Contact, which needs no new authentication, fetches the binding. It
// is no contact that the S-CSCF has not registered (5.4.1.2.2 step 2),
// and it leaves the binding and the HSS alone.
func TestProtectedREGISTERWithoutContactFetchesTheBinding(t *testing.T) {
	h := newAKAHarness(t)
	usim := aka.NewUSIM(testSet1, 0)
	callID := sip.NewCallID()
	ch := akaChallenge(t, h.register(callID, 1, 600000, akaInitial))
	res, err := authenticate(t, usim, ch)
	if err != nil {
		t.Fatal(err)
	}
	creds := akaAnswer(ch, akaPassword(ch, res), sip.IntegrityProtectedYes)
	if r := h.register(callID, 2, 600000, creds.String()); r.StatusCode != 200 {
		t.Fatalf("answer got %d, want 200", r.StatusCode)
	}
	stored, _, _ := h.store.Registration(context.Background(), impi)

	h.contact = noContact
	r := h.register(callID, 3, -1, creds.String())
	if r.StatusCode != 200 {
		t.Fatalf("protected query got %d, want 200", r.StatusCode)
	}
	if na, err := sip.ParseNameAddr(r.Get("Contact")); err != nil || na.URI != ueContact {
		t.Errorf("answered Contact %q, want the stored contact", r.Get("Contact"))
	}
	after, ok, _ := h.store.Registration(context.Background(), impi)
	if !ok || after.Contact != stored.Contact || !after.Expires.Equal(stored.Expires) {
		t.Errorf("registration after the query = %+v, want %+v", after, stored)
	}
	if got := h.hss.assignments(); !slices.Equal(got, []cx.ServerAssignmentType{cx.AssignRegistration}) {
		t.Errorf("SARs = %v: a query must send none", got)
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
