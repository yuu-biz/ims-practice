package scscf

import (
	"context"
	"log/slog"
	"time"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the terminating requests to a user who is not
// registered here, the last paragraphs of TS 24.229 5.4.3.3: the S-CSCF
// takes the user profile from the HSS, runs the services of the
// unregistered state, and answers when none of them took the request.

// UnregisteredProfileLifetime is how long the S-CSCF keeps the profile
// of a callee who is not registered. TS 24.229 and TS 29.228 leave it
// to the S-CSCF. It covers one call attempt with its retries, as long
// as the original dialog identifiers live, and is short enough that a
// change of the profile in the HSS applies within minutes: the HSS stub
// pushes no changes to the S-CSCF (no PPR, TS 29.228 6.2.2).
const UnregisteredProfileLifetime = originalDialogLifetime

// unregisteredProfile is the profile of a callee who has no
// registration here: the one the S-CSCF still keeps, or one it takes
// from the HSS with a SAR of UNREGISTERED_USER (5.4.3.3 NOTE 27). With
// no profile to use, it answers st itself and reports false.
func (s *SCSCF) unregisteredProfile(st *sip.ServerTransaction, callee string, log *slog.Logger) (UnregisteredProfile, bool) {
	ctx := context.Background()
	p, ok, err := s.store.UnregisteredProfileOf(ctx, callee)
	if err != nil {
		s.storeFailed(st, err, log)
		return UnregisteredProfile{}, false
	}
	if ok {
		return p, true
	}
	// TS 29.228 Table 6.1.2.1: no User-Name, which the S-CSCF does not
	// know, and no Destination-Host, since no HSS has answered for the
	// user yet.
	answer, err := s.request(cx.ServerAssignmentRequest{
		Header:               cx.NewHeader(s.node, "", s.cfg.HomeDomain),
		PublicIdentities:     []string{callee},
		ServerName:           s.cfg.ServerName,
		ServerAssignmentType: cx.UnregisteredUser,
	}.Message())
	if err != nil {
		log.Warn("SAR failed", "callee", callee, "type", cx.UnregisteredUser.String(), "error", err.Error())
		s.respond(st, 500, nil)
		return UnregisteredProfile{}, false
	}
	saa, err := cx.ParseServerAssignmentAnswer(answer)
	switch {
	case err != nil:
		log.Warn("cannot read the SAA", "error", err.Error())
		s.respond(st, 500, nil)
		return UnregisteredProfile{}, false
	case saa.Result == cx.Experimental(cx.DiameterErrorIdentityAlreadyRegistered) && saa.ServerName != "":
		// 5.4.3.3: another S-CSCF serves the user. The I-CSCF sends the
		// request there (5.3.2.1).
		log.Info("another S-CSCF serves the callee", "callee", callee, "server_name", saa.ServerName)
		s.respond(st, 305, func(r *sip.Message) { r.Add("Contact", sip.NameAddr{URI: saa.ServerName}.String()) })
		return UnregisteredProfile{}, false
	case saa.Result == cx.Experimental(cx.DiameterErrorUserUnknown):
		// As for a barred identity (5.4.3.3 step 1).
		log.Info("the HSS does not know the callee", "callee", callee)
		s.respond(st, 404, nil)
		return UnregisteredProfile{}, false
	case !saa.Result.Success():
		log.Warn("SAR refused", "callee", callee, "result", saa.Result.String())
		s.respond(st, 500, nil)
		return UnregisteredProfile{}, false
	}
	profile, err := cx.ParseUserProfile(saa.UserData)
	if err != nil {
		log.Warn("SAA has no usable User-Data", "error", err.Error())
		s.respond(st, 500, nil)
		return UnregisteredProfile{}, false
	}
	p = UnregisteredProfile{
		PrivateIdentity:       saa.UserName,
		PublicIdentities:      profile.PublicIdentities,
		BarredIdentities:      profile.BarredIdentities,
		InitialFilterCriteria: profile.InitialFilterCriteria,
		HSSHost:               originHost(answer),
		Expires:               time.Now().Add(s.cfg.UnregisteredProfileLifetime),
	}
	unlock, err := s.users.Lock(ctx, p.PrivateIdentity)
	if err != nil {
		s.lockFailed(st, err, log)
		return UnregisteredProfile{}, false
	}
	defer unlock()
	// A failure of the store from here on leaves the HSS with the user
	// assigned to this S-CSCF, as the next SAR for the user finds it.
	_, registered, err := s.store.Registration(ctx, p.PrivateIdentity)
	if err != nil {
		s.storeFailed(st, err, log)
		return UnregisteredProfile{}, false
	}
	if registered {
		// The user registered while the SAR was on its way. This request
		// uses the profile, and the registration keeps the HSS state.
		return p, true
	}
	if err := s.store.PutUnregisteredProfile(ctx, p); err != nil {
		s.storeFailed(st, err, log)
		return UnregisteredProfile{}, false
	}
	s.unregisteredTimers.start(key(p.PrivateIdentity), s.cfg.UnregisteredProfileLifetime, func() { s.unregisteredProfileExpired(p.PrivateIdentity) })
	log.Info("took the profile of an unregistered callee", "callee", callee, "private_identity", p.PrivateIdentity,
		"expires", int(s.cfg.UnregisteredProfileLifetime/time.Second))
	return p, true
}

// unregisteredProfileExpired lets the profile of privateID go and tells
// the HSS, which then has the user not registered, with no S-CSCF (TS
// 29.228 6.1.2.1). TS 29.229 6.3.15 names no Server-Assignment-Type for
// the end of the unregistered state; TIMEOUT_DEREGISTRATION, the end of
// a registration whose time is up, is the nearest.
func (s *SCSCF) unregisteredProfileExpired(privateID string) {
	ctx := context.Background()
	log := s.log.With("private_identity", privateID)
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		log.Warn("the profile of an unregistered user is not let go: the lock of the user was not taken; the HSS keeps the user assigned to this S-CSCF", "error", err.Error())
		return
	}
	defer unlock()
	p, ok, err := s.store.UnregisteredProfile(ctx, privateID)
	if err != nil {
		log.Warn("the profile of an unregistered user is not let go: the store failed; the HSS keeps the user assigned to this S-CSCF", "error", err.Error())
		return
	}
	// Under the lock, the profile may be gone, or a newer one may have
	// taken its place, which has its own expiry.
	if !ok || time.Now().Before(p.Expires) {
		return
	}
	deleted, err := s.store.DeleteUnregisteredProfile(ctx, privateID, p.Expires)
	if err != nil {
		log.Warn("the profile of an unregistered user is not let go: the store failed; the HSS keeps the user assigned to this S-CSCF", "error", err.Error())
		return
	}
	if !deleted {
		return
	}
	_, registered, err := s.store.Registration(ctx, p.PrivateIdentity)
	if err != nil {
		log.Warn("the HSS is not told that the profile of an unregistered user is let go: the store failed; it keeps the user assigned to this S-CSCF", "error", err.Error())
		return
	}
	if registered {
		// The registration has the HSS state now.
		return
	}
	log.Info("let the profile of an unregistered user go")
	s.serverAssignment(p.PrivateIdentity, p.PublicIdentities[0], p.HSSHost, cx.TimeoutDeregistration)
}

// routeUnregistered is the end of the terminating case for a user who
// is not registered: with no application server left, the S-CSCF
// answers 480 (5.4.3.3, the paragraph on an unregistered served user,
// item 3), unless a server gave the request a Route of its own to
// follow (step 14).
func (s *SCSCF) routeUnregistered(st *sip.ServerTransaction, out *sip.Message, d OriginalDialog, log *slog.Logger) {
	if out.Get("Route") != "" {
		log.Info("routing the terminating request along its Route", "route", out.Get("Route"))
		s.forward(st, out, s.chargeResponses(st.Request, false), log)
		return
	}
	log.Info("no application server took the request for the unregistered callee", "callee", d.ServedUser)
	s.respond(st, 480, nil)
}
