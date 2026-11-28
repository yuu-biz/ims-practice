package scscfstore_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/scscf"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// TestAChallengePastItsExpiresIsNotAnswered: the Store holds a challenge
// until its KeepUntil, so that the expiry of reg-await-auth finds it, and
// an instance whose sweep is late still holds it after its Expires. A
// REGISTER with the right digest response is then not accepted: the
// authentication has failed (TS 24.229 5.4.1.2.3), the instance ends the
// challenge and tells the HSS once, and nobody is registered.
func TestAChallengePastItsExpiresIsNotAnswered(t *testing.T) {
	const password, realm, callID = "secret", "scscf." + domain, "call-1"
	// The sweeps are an hour apart, so the expiry is not what ends the
	// challenge here.
	in := startInstancesSweeping(t, 1, time.Minute, time.Hour)
	ctx := context.Background()
	store := in.store()
	now := time.Now()
	must(t, store.PutChallenge(ctx, scscf.Challenge{
		PrivateIdentity: impi, PublicIdentity: impu, CallID: callID, Realm: realm, Nonce: "n1",
		Algorithm: "MD5", HA1: sip.HA1("MD5", impi, realm, password),
		Expires: now.Add(-time.Second), KeepUntil: now.Add(time.Minute),
	}))
	if _, ok, err := store.Challenge(ctx, impi); err != nil || !ok {
		t.Fatalf("the Store holds the challenge between Expires and KeepUntil: %v %v", ok, err)
	}

	c := sip.Credentials{Username: impi, Realm: realm, Nonce: "n1", URI: "sip:" + domain,
		Algorithm: "MD5", CNonce: "0a4f113b", QOP: "auth", NC: "00000001"}
	var err error
	if c.Response, err = sip.DigestResponse(sip.HA1(c.Algorithm, impi, realm, password), sip.REGISTER, c); err != nil {
		t.Fatal(err)
	}
	c.AuthParams.Set("integrity-protected", `"ip-assoc-pending"`)
	m := &sip.Message{Method: sip.REGISTER, RequestURI: "sip:" + domain}
	m.Add("Max-Forwards", "70")
	m.Add("From", "<"+impu+">;tag="+sip.NewTag())
	m.Add("To", "<"+impu+">")
	m.Add("Call-ID", callID)
	m.Add("CSeq", "2 REGISTER")
	m.Add("Contact", "<sip:ue1@192.0.2.10:5060>;expires=3600")
	m.Add("Path", "<sip:term@pcscf."+domain+":5060;lr>")
	m.Add("Authorization", c.String())
	ue := listen(t, func(*sip.ServerTransaction) {})
	ct, err := ue.SendRequest(m, sip.Destination{Transport: sip.UDP, Addr: in.sip})
	if err != nil {
		t.Fatal(err)
	}
	var final *sip.Message
	for r := range ct.Responses() {
		final = r
	}
	if final == nil {
		t.Fatalf("no final response: %v", ct.Err())
	}
	if final.StatusCode == 200 {
		t.Error("REGISTER accepted after reg-await-auth expired")
	}
	if _, ok, err := store.Registration(ctx, impi); err != nil || ok {
		t.Errorf("registration after the refused answer: %v %v, want none", ok, err)
	}
	if got, want := in.hss.assignments(), []cx.ServerAssignmentType{cx.AuthenticationTimeout}; !slices.Equal(got, want) {
		t.Errorf("SARs = %v, want %v", got, want)
	}
}
