package nhss

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
)

// The vector is the one of TS 35.208 test set 1 (RAND, XRES = f2, CK,
// IK, and AUTN = SQN xor AK || AMF || MAC-A), as internal/aka checks it.
const akaRequest = `{
  "cscfServerName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org",
  "sipAuthenticationScheme": "DIGEST-AKAV1-MD5"
}`

const akaResult = `{
  "impi": "001010000000001@ims.mnc001.mcc001.3gppnetwork.org",
  "3gAkaAvs": [{
    "rand": "23553cbe9637a89d218ae64dae47bf35",
    "xres": "a54211d5e3ba50bf",
    "autn": "55f328b43577b9b94a9ffac354dfafb3",
    "ck": "b40ba9a3c58b2a05bbf0d987b21bf8cb",
    "ik": "f769bcd751044604127672711c6d3441"
  }]
}`

// resyncRequest carries the AUTS of a UE whose sequence number is out
// of range (TS 33.203); the value only has the length of one.
const resyncRequest = `{
  "cscfServerName": "sip:scscf.ims.mnc001.mcc001.3gppnetwork.org",
  "sipAuthenticationScheme": "DIGEST-AKAV1-MD5",
  "resynchronizationInfo": {"rand": "23553cbe9637a89d218ae64dae47bf35", "auts": "00112233445566778899aabbccdd"}
}`

const digestResult = `{
  "impi": "alice@ims.example.org",
  "digestAuth": {"digestRealm": "ims.example.org", "digestAlgorithm": "MD5", "digestQop": "AUTH", "ha1": "0123456789abcdef0123456789abcdef"}
}`

// The patterns of TS 29.503 for the parts of a vector.
var (
	hex32 = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
	xres  = regexp.MustCompile(`^[A-Fa-f0-9]{8,32}$`)
	auts  = regexp.MustCompile(`^[A-Fa-f0-9]{28}$`)
)

func TestUEAURoundTrips(t *testing.T) {
	roundTrip[SipAuthenticationInfoRequest](t, akaRequest)
	r := roundTrip[SipAuthenticationInfoRequest](t, resyncRequest)
	if !hex32.MatchString(r.ResynchronizationInfo.Rand) || !auts.MatchString(r.ResynchronizationInfo.Auts) {
		t.Errorf("%+v", r.ResynchronizationInfo)
	}
	res := roundTrip[SipAuthenticationInfoResult](t, akaResult)
	av := res.ThreeGAkaAvs[0]
	for _, part := range []string{av.Rand, av.Autn, av.Ck, av.Ik} {
		if !hex32.MatchString(part) {
			t.Errorf("%q does not have the pattern of TS 29.503", part)
		}
	}
	if !xres.MatchString(av.Xres) {
		t.Errorf("xres %q", av.Xres)
	}
	if d := roundTrip[SipAuthenticationInfoResult](t, digestResult); d.DigestAuth.DigestQop != QopAuth {
		t.Errorf("%+v", d)
	}
}

type stubUEAU struct{}

func (stubUEAU) GenerateSipAuthData(_ context.Context, impi string, req SipAuthenticationInfoRequest) (SipAuthenticationInfoResult, error) {
	if impi != testImpi {
		return SipAuthenticationInfoResult{}, NewProblem(CauseUserNotFound, "")
	}
	if req.SipAuthenticationScheme != DigestAKAv1MD5 {
		return SipAuthenticationInfoResult{}, NewProblem(CauseUnsupportedSIPAuthenticationScheme, "")
	}
	return decoded[SipAuthenticationInfoResult](akaResult), nil
}

// decoded reads an example body that the tests check elsewhere.
func decoded[T any](body string) T {
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		panic(err)
	}
	return v
}

func TestUEAUClientAndServer(t *testing.T) {
	root := serveH2C(t, NewImsUEAUHandler(stubUEAU{}))
	ctx, cancel := contextWithTimeout(t)
	defer cancel()
	c := NewClient(root, NFTypeSCSCF)

	res, err := c.GenerateSipAuthData(ctx, testImpi, roundTrip[SipAuthenticationInfoRequest](t, akaRequest))
	if err != nil || len(res.ThreeGAkaAvs) != 1 || res.ThreeGAkaAvs[0].Xres != "a54211d5e3ba50bf" {
		t.Fatalf("got %+v, %v", res, err)
	}

	var p *ProblemError
	_, err = c.GenerateSipAuthData(ctx, testImpi, SipAuthenticationInfoRequest{CscfServerName: testSCSCF, SipAuthenticationScheme: "NBA"})
	if !errors.As(err, &p) || p.Status != 501 || p.Problem.Cause != CauseUnsupportedSIPAuthenticationScheme {
		t.Errorf("unsupported scheme: %v", err)
	}
	_, err = c.GenerateSipAuthData(ctx, "nobody@ims.example.org", SipAuthenticationInfoRequest{CscfServerName: testSCSCF, SipAuthenticationScheme: DigestAKAv1MD5})
	if !errors.As(err, &p) || p.Status != 404 || p.Problem.Cause != CauseUserNotFound {
		t.Errorf("unknown user: %v", err)
	}

	status, header, body := request(t, "POST", root+UEAURoot+"/"+testImpi+"/security-information/generate-sip-auth-data", "application/json", `{}`)
	if p := problemOf(t, header, body); status != 400 || p.Cause != CauseMandatoryIEMissing || params(p) != "/cscfServerName /sipAuthenticationScheme" {
		t.Errorf("got %d %+v", status, p)
	}
}
