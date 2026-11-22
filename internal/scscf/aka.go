package scscf

import (
	"context"
	"encoding/hex"
	"log/slog"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// registerAKA is TS 24.229 5.4.1.2 with IMS AKA as the security
// mechanism, for a REGISTER that the P-CSCF marked integrity-protected
// "no" (5.4.1.2.1 and 5.4.1.2.1A) or "yes" (5.4.1.2.2). The lock of the
// user is held, and a protected deregistration has been handled before.
func (s *SCSCF) registerAKA(st *sip.ServerTransaction, privateID, publicID string, c sip.Credentials, protected bool, expires time.Duration, log *slog.Logger) {
	ctx := context.Background()
	ch, pending, err := s.store.Challenge(ctx, privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	pending = pending && ch.Algorithm == sip.AlgorithmAKAv1MD5
	if !protected {
		// 5.4.1.2.1, last paragraph: an unprotected REGISTER that names the
		// nonce of the pending challenge tells that the UE found the
		// challenge invalid. Any other one starts an initial registration,
		// even while a challenge is pending (5.4.1.2.3). The response of an
		// unprotected REGISTER does not count (5.4.1.2.1).
		if !pending || c.Nonce != ch.Nonce {
			s.challengeAKA(st, privateID, publicID, nil, false, log)
			return
		}
		if err := s.endChallenge(ctx, privateID); err != nil {
			s.storeFailed(st, err, log)
			return
		}
		if auts, ok := c.AUTS(); ok {
			s.resynchronise(st, ch, auts, log)
			return
		}
		// TS 33.203 6.1.2.2: without auts, the UE could not authenticate
		// the network, from the MAC of the AUTN (TS 24.229 5.1.1.5.3).
		s.authenticationFailed(st, privateID, publicID, ch.HSSHost, "the UE found the challenge invalid", log)
		return
	}
	if !pending {
		s.reregister(st, privateID, publicID, c, expires, log)
		return
	}
	// 5.4.1.2.2 with reg-await-auth running: step 1 checks the Call-ID,
	// step 3 what Authorization holds and step 4 the response, which
	// check does.
	verdict := wrong
	if strings.EqualFold(c.Algorithm, sip.AlgorithmAKAv1MD5) && c.Response != "" {
		verdict = s.check(st.Request, c, ch)
	}
	switch verdict {
	case wrong:
		// 5.4.1.2.3A: a wrong response or Call-ID gets 403.
		s.authenticationFailed(st, privateID, publicID, ch.HSSHost, "authentication failed", log)
	case stale:
		// RES is good for one response; a later nonce count asks for a
		// new challenge, as for SIP Digest (5.4.1.2.3B).
		log.Info("stale nonce")
		// The timer of the challenge goes on until the new challenge,
		// once stored, takes its place.
		s.challengeAKA(st, privateID, publicID, nil, true, log)
	default:
		if err := s.endChallenge(ctx, privateID); err != nil {
			s.storeFailed(st, err, log)
			return
		}
		s.accept(st, c, ch, expires, log)
	}
}

// reregister is TS 24.229 5.4.1.2.2 for a protected REGISTER while no
// authentication is under way. Step 1 lets the S-CSCF decide whether to
// authenticate again; it does not, because the REGISTER came over the
// security associations of the last authentication (TS 33.203 7.4.0).
func (s *SCSCF) reregister(st *sip.ServerTransaction, privateID, publicID string, c sip.Credentials, expires time.Duration, log *slog.Logger) {
	reg, ok, err := s.store.Registration(context.Background(), privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if !ok {
		// 5.4.1.2.3A: protected, but nobody registered by that name, and
		// this S-CSCF has no restoration procedures.
		log.Info("protected REGISTER of a user not registered here")
		s.respond(st, 500, nil)
		return
	}
	// Step 2: a new contact is registered only by an initial
	// registration (NOTE 1).
	contact, err := sip.ParseNameAddr(st.Request.Get("Contact"))
	if err != nil || !sameURI(contact.URI, reg.Contact) {
		log.Info("protected REGISTER for a contact not registered", "contact", st.Request.Get("Contact"))
		s.respond(st, 403, nil)
		return
	}
	// Steps 3 and 5 to 11.
	s.accept(st, c, Challenge{PrivateIdentity: privateID, PublicIdentity: publicID, HSSHost: reg.HSSHost}, expires, log)
}

// challengeAKA fetches an authentication vector with a MAR and
// challenges the UE with a 401 (TS 24.229 5.4.1.2.1 steps 3 to 7, and
// 5.4.1.2.1A). resync carries the RAND and AUTS of a
// re-synchronisation (5.4.1.2.3A).
//
// SIMPLIFIED: TS 33.203 6.1.1 and TS 24.229 5.4.1.2.1A. The S-CSCF
// offers AKAv1-MD5 only, not AKAv2-SHA-256 first and AKAv1-MD5 after
// it: SIPp, the independent test peer, speaks AKAv1-MD5 only.
func (s *SCSCF) challengeAKA(st *sip.ServerTransaction, privateID, publicID string, resync *cx.Resynchronization, stale bool, log *slog.Logger) {
	answer, err := s.request(cx.MultimediaAuthRequest{
		Header:                  cx.NewHeader(s.node, "", s.cfg.HomeDomain),
		UserName:                privateID,
		PublicIdentity:          publicID,
		SIPAuthenticationScheme: cx.SchemeDigestAKAv1MD5,
		SIPAuthorization:        resync,
		SIPNumberAuthItems:      1,
		// The HSS takes an AUTS only from the S-CSCF it has stored (TS
		// 29.228 6.3.1 step 4), which this one is.
		ServerName: s.cfg.ServerName,
	}.Message())
	if err != nil {
		log.Warn("MAR failed", "error", err.Error())
		s.respond(st, 500, nil)
		return
	}
	maa, err := cx.ParseMultimediaAuthAnswer(answer)
	if err != nil || !maa.Result.Success() || maa.AuthenticationVector == nil {
		log.Info("HSS refused authentication", "result", describe(maa.Result, err))
		s.respond(st, 403, nil)
		return
	}
	av := maa.AuthenticationVector
	ch := Challenge{
		PrivateIdentity: privateID,
		PublicIdentity:  publicID,
		CallID:          st.Request.Get("Call-ID"),
		// 5.4.1.2.1A item 1 a: a globally unique name of the S-CSCF.
		Realm:     s.cfg.Host,
		Nonce:     aka.Nonce(av.RAND, av.AUTN),
		Algorithm: sip.AlgorithmAKAv1MD5,
		// The RAND of the newest 401 replaces any stored before
		// (5.4.1.2.1A).
		RAND:    av.RAND,
		XRES:    av.XRES,
		HSSHost: originHost(answer),
	}
	if err := s.putChallenge(ch); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	log.Info("challenged with IMS AKA", "nonce", ch.Nonce, "stale", stale, "resynchronised", resync != nil)
	challenge := sip.Challenge{Realm: ch.Realm, Nonce: ch.Nonce, Stale: stale, Algorithm: ch.Algorithm, QOP: []string{"auth"}}
	// 5.4.1.2.1A item 1 c: IK and CK for the P-CSCF (7.2A.1).
	challenge.SetKeys(hex.EncodeToString(av.IK[:]), hex.EncodeToString(av.CK[:]))
	s.respond(st, 401, func(r *sip.Message) { r.Add("WWW-Authenticate", challenge.String()) })
}

// resynchronise is TS 24.229 5.4.1.2.3A for a REGISTER with auts: the
// S-CSCF asks the HSS for a new vector with the RAND it stored and the
// AUTS of the UE, and challenges again (TS 33.203 6.1.3).
func (s *SCSCF) resynchronise(st *sip.ServerTransaction, ch Challenge, auts string, log *slog.Logger) {
	a, err := aka.ParseAUTS(auts)
	if err != nil {
		s.authenticationFailed(st, ch.PrivateIdentity, ch.PublicIdentity, ch.HSSHost, "unreadable auts", log)
		return
	}
	log.Info("synchronisation failure")
	s.challengeAKA(st, ch.PrivateIdentity, ch.PublicIdentity, &cx.Resynchronization{RAND: ch.RAND, AUTS: a}, false, log)
}

// authenticationFailed ends an authentication with 403 (TS 24.229
// 5.4.1.2.3A and 5.4.1.2.3B). The SAR clears the S-CSCF name in the HSS
// of a user who is not registered (TS 33.203 6.1.2.2); a user who was
// registered stays registered until the registration expires (TS
// 33.203 6.1.1).
func (s *SCSCF) authenticationFailed(st *sip.ServerTransaction, privateID, publicID, hssHost, why string, log *slog.Logger) {
	log.Info(why)
	if err := s.endChallenge(context.Background(), privateID); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	s.serverAssignment(privateID, publicID, hssHost, cx.AuthenticationFailure)
	s.respond(st, 403, nil)
}
