package ue

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// AKA is what the UE needs for IMS AKA besides its identities.
type AKA struct {
	// Secret is K and OPc of the USIM (TS 35.206 4.1). milenage.OPc
	// derives OPc from the OP of an operator.
	Secret aka.Secret
	// SQN is SQN_MS, the highest sequence number that the USIM has
	// accepted (TS 33.102 C.2); a new card has 0.
	SQN aka.SQN
	// Unprotected is the stack that sends a REGISTER that no security
	// association protects: the initial one, and one that reports a
	// challenge the UE found invalid (TS 24.229 5.1.1.5.3). It is on
	// another port than the stack that New takes, which is the protected
	// server port, the port of the UE's contact (TS 33.203 7.1).
	Unprotected *sip.Stack
}

// SIMPLIFIED: TS 33.203 7.1 and TS 24.229 5.1.1.5.1. The UE sets
// up no IPsec ESP. It agrees on security associations with the P-CSCF,
// derives IK and CK, and then sends every protected message from its
// protected server port to the P-CSCF's; its protected client port is
// agreed but never bound. internal/pcscf/secagree.go gives the reasons.

// securityAssociations is the UE's side of the agreement with the
// P-CSCF: an entry of its SA table (TS 33.203 7.1 rule 5).
type securityAssociations struct {
	// temporary is true from the 401 until the 200 (OK) to the REGISTER
	// that answers it (TS 24.229 5.1.1.5.1).
	temporary bool
	// client is the Security-Client of the REGISTER that the 401
	// challenged, and server the Security-Server of the 401. The REGISTERs
	// over these associations mirror server in Security-Verify (5.1.1.5.1
	// item 4, 5.1.1.4.2 item e).
	client []ims.IPsec3GPP
	server []string
	// pcscf is the P-CSCF at its protected server port.
	pcscf netip.AddrPort
	// ik and ck are the keys of the authentication, which ESP would use.
	ik      aka.IK
	ck      aka.CK
	expires time.Time
}

// akaAnswer is the last Authorization that answered a challenge, with
// the RES it was computed with.
type akaAnswer struct {
	creds sip.Credentials
	res   []byte
}

// maxChallenges bounds the 401s that one Register answers. After a
// challenge it finds invalid the UE answers one more, and TS 24.229
// 5.4.1.2.3A NOTE 3 has the S-CSCF send a new challenge only twice.
const maxChallenges = 3

// Outbound is where the UE sends its requests: the P-CSCF (TS 24.229
// 5.1.2A.1.1), at its protected server port once IMS AKA has set up
// security associations with it (5.1.1.5.1).
func (u *UE) Outbound() sip.Destination {
	if sa := u.securityAssociations(); sa != nil {
		return sip.Destination{Transport: sip.UDP, Addr: sa.pcscf}
	}
	return sip.Destination{Transport: sip.UDP, Addr: u.cfg.PCSCF}
}

// pcscfURI is the P-CSCF in the Route of the UE's requests: with the
// protected server port that the registration learnt, for IMS AKA
// (TS 24.229 5.1.2A.1.1 item b).
func (u *UE) pcscfURI() string {
	sa := u.securityAssociations()
	pcscf, err := sip.ParseURI(u.cfg.PCSCFURI)
	if sa == nil || err != nil {
		return u.cfg.PCSCFURI
	}
	pcscf.Port = int(sa.pcscf.Port())
	return pcscf.String()
}

// securityAssociations are the established associations with the
// P-CSCF that the UE uses, if any; the UE deletes them when their
// lifetime is over (TS 33.203 7.4.1a).
func (u *UE) securityAssociations() *securityAssociations {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sa != nil && time.Now().After(u.sa.expires) {
		u.sa = nil
	}
	return u.sa
}

func (u *UE) setSecurityAssociations(sa *securityAssociations) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sa = sa
}

// sendAKA is a REGISTER with IMS AKA (TS 24.229 5.1.1.2.2, 5.1.1.4.2,
// 5.1.1.6.2) and the answers to its challenges (5.1.1.5.1, 5.1.1.5.3).
// A REGISTER goes over the established associations if there are any,
// and unprotected otherwise.
func (u *UE) sendAKA(ctx context.Context, expires time.Duration) (*sip.Message, error) {
	over := u.securityAssociations()
	creds := sip.Credentials{Username: u.cfg.PrivateIdentity, Realm: u.cfg.HomeDomain, URI: "sip:" + u.cfg.HomeDomain,
		Algorithm: sip.AlgorithmAKAv1MD5}
	if over != nil && u.answer != nil {
		// 5.1.1.4.2 item a and 5.1.1.6.2: the last nonce and response.
		creds = u.answer.creds
	}
	client := u.newSecurityClient(over)
	sentOver := over
	resp, err := u.sendOver(ctx, expires, over, client, creds)
	for challenges := 0; err == nil && resp.StatusCode == 401; challenges++ {
		if challenges == maxChallenges {
			return nil, fmt.Errorf("challenged %d times in a row", challenges)
		}
		ch, server, offer, err := readChallenge(resp, client)
		if err != nil {
			return nil, err
		}
		challenge, autn, err := aka.ParseNonce(ch.Nonce)
		if err != nil {
			return nil, err
		}
		result, err := u.usim.Authenticate(challenge, autn)
		var failure aka.SynchronisationFailure
		switch {
		case errors.Is(err, aka.ErrMACFailure):
			// 5.1.1.5.3: neither a response nor auts, with new
			// Security-Client values, and no temporary associations.
			u.log.Info("challenge refused", "why", "MAC failure")
			resp, err := u.sendOver(ctx, expires, over, u.newSecurityClient(over), u.failureCredentials(ch, nil))
			if err == nil && resp.StatusCode == 403 {
				return nil, fmt.Errorf("%w after a challenge with a %w", ErrForbidden, aka.ErrMACFailure)
			}
			return resp, err
		case errors.As(err, &failure):
			// 5.1.1.5.3 and RFC 3310 3.4: auts, which the S-CSCF passes to
			// the HSS to re-synchronise.
			u.log.Info("challenge refused", "why", "synchronisation failure", "sqn_ms", uint64(u.usim.SQNMS()))
			client, sentOver = u.newSecurityClient(over), over
			creds = u.failureCredentials(ch, &failure.AUTS)
		case err != nil:
			return nil, err
		default:
			// 5.1.1.5.1: temporary associations, with the parameters of
			// the Security-Server, for the answer.
			sentOver = &securityAssociations{temporary: true, client: client, server: server,
				pcscf: netip.AddrPortFrom(u.cfg.PCSCF.Addr(), offer.PortS), ik: result.IK, ck: result.CK,
				expires: time.Now().Add(ims.RegAwaitAuth)}
			u.answer = &akaAnswer{creds: u.answerCredentials(ch, result.RES), res: result.RES}
			creds = u.answer.creds
			u.log.Info("challenged", "realm", ch.Realm, "nonce", ch.Nonce, "alg", offer.Algorithm, "pcscf", sentOver.pcscf.String())
		}
		resp, err = u.sendOver(ctx, expires, sentOver, client, creds)
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 && sentOver != nil {
		u.registeredOver(sentOver, parseRegistration(resp).Expires)
	}
	return resp, nil
}

// registeredOver keeps the associations that a REGISTER went over once
// it is answered 200 (OK) for interval (TS 24.229 5.1.1.5.1, 5.1.1.4.2):
// they become established, and last 30 s longer than the registration
// or as long as they did, whichever is longer. A deregistration ends
// them (5.1.1.6.2 NOTE 2).
func (u *UE) registeredOver(sa *securityAssociations, interval time.Duration) {
	if interval == 0 {
		u.setSecurityAssociations(nil)
		return
	}
	established := *sa
	until := time.Now().Add(interval + 30*time.Second)
	if established.temporary || until.After(established.expires) {
		established.expires = until
	}
	established.temporary = false
	u.setSecurityAssociations(&established)
}

// sendOver sends a REGISTER with the security agreement of RFC 3329 2.3.1
// and TS 24.229 5.1.1.2.2 item d: Security-Client with client, and
// sec-agree in Require and Proxy-Require. Over associations, it goes to
// the P-CSCF's protected server port and mirrors their Security-Server
// in Security-Verify; without, it goes unprotected.
func (u *UE) sendOver(ctx context.Context, expires time.Duration, over *securityAssociations, client []ims.IPsec3GPP, creds sip.Credentials) (*sip.Message, error) {
	m := u.newREGISTER(expires, creds.String())
	m.Add("Require", sip.OptionSecAgree)
	m.Add("Proxy-Require", sip.OptionSecAgree)
	for _, c := range client {
		m.Add("Security-Client", c.SecurityMechanism().String())
	}
	if over == nil {
		return u.requestVia(ctx, u.cfg.AKA.Unprotected, sip.Destination{Transport: sip.UDP, Addr: u.cfg.PCSCF}, m)
	}
	for _, v := range over.server {
		m.Add("Security-Verify", v)
	}
	return u.requestVia(ctx, u.stack, sip.Destination{Transport: sip.UDP, Addr: over.pcscf}, m)
}

// newSecurityClient is the Security-Client of a new agreement: the
// transforms the UE supports, with new SPIs and a new protected client
// port, none of which old associations use (TS 33.203 7.1, TS 24.229
// 5.1.1.5.3). The protected server port stays that of the contact
// (TS 33.203 7.1 NOTE 10).
func (u *UE) newSecurityClient(old *securityAssociations) []ims.IPsec3GPP {
	portS := u.contactAddr.Port()
	var spis []uint32
	ports := []uint16{portS}
	if old != nil {
		for _, c := range old.client {
			spis = append(spis, c.SPIC, c.SPIS)
			ports = append(ports, c.PortC)
		}
	}
	spiC := ims.NewSPI(spis...)
	spiS := ims.NewSPI(append(spis, spiC)...)
	portC := ims.NewProtectedPort(ports...)
	var client []ims.IPsec3GPP
	for _, t := range ims.SupportedTransforms {
		client = append(client, ims.IPsec3GPP{Transform: t, SPIC: spiC, SPIS: spiS, PortC: portC, PortS: portS})
	}
	return client
}

// readChallenge reads a 401 as TS 24.229 5.1.1.5.1 asks: the topmost
// challenge with AKAv1-MD5, and a Security-Server with parameters for
// the associations. It returns the challenge, the Security-Server to
// mirror and the mechanism that the UE takes from it (TS 33.203 7.2).
func readChallenge(resp *sip.Message, client []ims.IPsec3GPP) (sip.Challenge, []string, ims.IPsec3GPP, error) {
	i := slices.IndexFunc(resp.GetAll("WWW-Authenticate"), func(v string) bool {
		ch, err := sip.ParseChallenge(v)
		return err == nil && strings.EqualFold(ch.Algorithm, sip.AlgorithmAKAv1MD5)
	})
	if i < 0 {
		return sip.Challenge{}, nil, ims.IPsec3GPP{}, errors.New("401 without an AKAv1-MD5 challenge")
	}
	ch, _ := sip.ParseChallenge(resp.GetAll("WWW-Authenticate")[i])
	// Item 3. The UE gives up rather than send a REGISTER with a new
	// Call-ID.
	offers, err := ims.IPsec3GPPMechanisms(resp, "Security-Server")
	if err != nil || len(offers) == 0 {
		return sip.Challenge{}, nil, ims.IPsec3GPP{}, fmt.Errorf("401 without a usable Security-Server: %v", err)
	}
	offer, _, ok := ims.ChooseTransform(offers, client)
	if !ok {
		// TS 33.203 7.3.2.2.
		return sip.Challenge{}, nil, ims.IPsec3GPP{}, errors.New("Security-Server offers no algorithms of the UE")
	}
	return ch, resp.GetAll("Security-Server"), offer, nil
}

// answerCredentials answer ch with RES as the password (RFC 3310 3.3),
// with the fields of TS 24.229 5.1.1.5.1 item 4.
func (u *UE) answerCredentials(ch sip.Challenge, res []byte) sip.Credentials {
	c := u.challengeCredentials(ch)
	c.Response, _ = sip.DigestResponse(sip.HA1(u.cfg.PrivateIdentity, ch.Realm, aka.DigestPassword(res)), sip.REGISTER, c)
	return c
}

// failureCredentials tell the S-CSCF that the UE found ch invalid (TS
// 24.229 5.1.1.5.3): with auts, computed with the empty password (RFC
// 3310 3.4); without, no response at all.
func (u *UE) failureCredentials(ch sip.Challenge, auts *aka.AUTS) sip.Credentials {
	if auts == nil {
		return sip.Credentials{Username: u.cfg.PrivateIdentity, Realm: ch.Realm, Nonce: ch.Nonce,
			URI: "sip:" + u.cfg.HomeDomain, Algorithm: ch.Algorithm}
	}
	c := u.challengeCredentials(ch)
	c.SetAUTS(aka.EncodeAUTS(*auts))
	c.Response, _ = sip.DigestResponse(sip.HA1(u.cfg.PrivateIdentity, ch.Realm, ""), sip.REGISTER, c)
	return c
}

// challengeCredentials are the fields of a response to ch, before the
// response itself: qop auth when the challenge offers it (RFC 2617
// 3.2.2), with the first nonce count.
func (u *UE) challengeCredentials(ch sip.Challenge) sip.Credentials {
	c := sip.Credentials{Username: u.cfg.PrivateIdentity, Realm: ch.Realm, Nonce: ch.Nonce, URI: "sip:" + u.cfg.HomeDomain,
		Algorithm: ch.Algorithm, Opaque: ch.Opaque}
	if slices.Contains(ch.QOP, "auth") {
		c.QOP, c.CNonce, c.NC = "auth", rand.Text()[:16], "00000001"
	}
	return c
}
