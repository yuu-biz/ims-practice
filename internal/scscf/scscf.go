// Package scscf is the S-CSCF role: the registrar of the home network.
// It authenticates users with SIP Digest, using the H(A1) that the HSS
// returns over Cx, or with IMS AKA, using the authentication vectors of
// the HSS, and keeps the registrations (TS 24.229 5.4.1).
package scscf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
)

// HSS is how the S-CSCF reaches the HSS over Cx.
// *diameter.PeerGroup, the instances of the HSS, is one.
type HSS interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// Config is what the S-CSCF needs to know.
type Config struct {
	// ServerName is the SIP URI of this instance of the S-CSCF, the
	// Server-Name on Cx of a user that it assigns. A user that the HSS
	// holds under the name of another instance keeps that name
	// (assignedName).
	ServerName string
	// Host and Port name the S-CSCF in Service-Route.
	Host       string
	Port       int
	HomeDomain string
	MinExpires time.Duration
	MaxExpires time.Duration
	// RegAwaitAuth is the reg-await-auth timer; zero means
	// ims.RegAwaitAuth. Tests shorten it.
	RegAwaitAuth time.Duration
	// ENUMSuffix is the domain under which ENUM is looked up.
	ENUMSuffix string
	// IBCF is the IBCF of the home network that a request for another
	// network leaves through (TS 24.229 5.4.3.2 steps 11 and 12), such
	// as sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060. Without a host
	// the S-CSCF sends such a request to the other network itself.
	IBCF sip.URI
	// UnregisteredProfileLifetime is how long the S-CSCF keeps the
	// profile of a callee who is not registered (session.go).
	// Zero means UnregisteredProfileLifetime. Tests shorten it.
	UnregisteredProfileLifetime time.Duration
	// SweepInterval is how often this instance claims the expiries that
	// are due, and so how late an expiry may be handled; zero means
	// ims.SweepInterval. Tests shorten it.
	SweepInterval time.Duration
	// ExpiryLease is how long a claimed expiry is this instance's alone,
	// after which another instance may claim it; zero means
	// ims.ExpiryLease. It is longer than handling one takes, a Cx round
	// trip included. Tests shorten it.
	ExpiryLease time.Duration
	// UserLocks serialises what happens to one user. Nil means a lock
	// per user in the memory of this process (ims.UserLocks), which
	// serves an S-CSCF that runs as one instance. Instances that share a
	// store take the lock of a user in it
	// (internal/kvstore/kvlock), so that only one of them handles the
	// user at a time.
	UserLocks ims.UserLocker
	// Metrics is where the S-CSCF registers its counters, the
	// registration measurements (ims.Registrations) and the session
	// ones (ims.Sessions). Nil gives it a registry of its own, which
	// nothing serves.
	Metrics *metrics.Registry
}

// role is the name of the role in the configuration file: the value of
// the role label of what the S-CSCF counts, and the role of its
// maintenance function.
const role = "scscf"

// defaultExpires is used when a REGISTER asks for no interval. RFC 3261
// 10.3 step 7 leaves the default to the registrar.
const defaultExpires = 3600 * time.Second

// SCSCF serves REGISTER requests and the sessions of its users.
type SCSCF struct {
	cfg      Config
	hss      HSS
	node     *diameter.Node
	store    Store
	resolver Resolver
	stack    ims.StackRef
	log      *slog.Logger

	// users serializes what happens to one user's registration and the
	// subscriptions to it, so that the NOTIFYs of a user go out in the
	// order of the events. Each user has a lock of its own, so that a
	// Cx round trip of one user never holds up another.
	users ims.UserLocker
	// stopSweep ends the sweep of this instance and waits for the
	// handlers it started (expiry.go).
	stopSweep func()
	// registrations counts the registration procedures, and sessions
	// the session establishments (measurements.go).
	registrations *ims.Registrations
	sessions      *ims.Sessions
	// measured counts the RTR and the PPR that the HSS sends, with the
	// answers the S-CSCF gives them (Handle).
	measured *diameter.Answering
	// transactions counts the requests that the S-CSCF is serving, the
	// users of its maintenance function (maintenance.go).
	transactions atomic.Int64
	// managed is the administrative state that the maintenance API of
	// the node keeps for the S-CSCF; nil without one.
	managed atomic.Pointer[maintenance.ManagedFunction]
}

// New returns an S-CSCF and starts its sweep, which handles the expiries
// that are due until Close. It fails when cfg.Metrics holds the metrics
// of another S-CSCF already.
func New(cfg Config, hss HSS, node *diameter.Node, store Store, resolver Resolver, log *slog.Logger) (*SCSCF, error) {
	if cfg.RegAwaitAuth == 0 {
		cfg.RegAwaitAuth = ims.RegAwaitAuth
	}
	if cfg.ENUMSuffix == "" {
		cfg.ENUMSuffix = sipdns.ENUMSuffix
	}
	if cfg.UnregisteredProfileLifetime == 0 {
		cfg.UnregisteredProfileLifetime = UnregisteredProfileLifetime
	}
	if cfg.UserLocks == nil {
		cfg.UserLocks = &ims.UserLocks{}
	}
	if cfg.SweepInterval == 0 {
		cfg.SweepInterval = ims.SweepInterval
	}
	if cfg.ExpiryLease == 0 {
		cfg.ExpiryLease = ims.ExpiryLease
	}
	if cfg.Metrics == nil {
		cfg.Metrics = metrics.NewRegistry()
	}
	registrations, err := ims.NewRegistrations(cfg.Metrics)
	if err != nil {
		return nil, err
	}
	sessions, err := ims.NewSessions(cfg.Metrics)
	if err != nil {
		return nil, err
	}
	measured, err := diameter.NewAnswering(cfg.Metrics, role)
	if err != nil {
		return nil, err
	}
	s := &SCSCF{cfg: cfg, hss: hss, node: node, store: store, resolver: resolver, log: log, users: cfg.UserLocks,
		registrations: registrations, sessions: sessions, measured: measured}
	s.stopSweep = s.sweep()
	return s, nil
}

// Close stops the sweep of this instance: it claims no more expiries,
// and lets each handler it started finish within its claim, so that the
// NOTIFYs and the SARs of the expiries it claimed go out. A node calls
// it before it closes the stack and the Cx connections of the S-CSCF.
// An expiry that this instance had claimed and not handled is handled
// by another instance of the S-CSCF once the claim runs out.
func (s *SCSCF) Close() { s.stopSweep() }

// lockFailed answers a request whose user the S-CSCF could not lock
// (500, RFC 3261 21.5.1). Another instance may be handling the user, so
// this one does not go on without the lock.
func (s *SCSCF) lockFailed(st *sip.ServerTransaction, err error, log *slog.Logger) {
	log.Warn("the lock of the user was not taken", "method", string(st.Request.Method), "error", err.Error())
	s.respond(st, 500, nil)
}

// SetStack gives the S-CSCF the stack it runs on, which it needs to
// forward ACKs and CANCELs. Call it right after the stack is made.
func (s *SCSCF) SetStack(stack *sip.Stack) { s.stack.Set(stack) }

// OnRequest is the sip.Config.OnRequest of the S-CSCF.
func (s *SCSCF) OnRequest(st *sip.ServerTransaction) {
	s.transactions.Add(1)
	defer s.transactionEnded()
	if !s.unlocked() && !s.carriesOn(st.Request) {
		s.refuse(st)
		return
	}
	if st.Request.Method != sip.REGISTER {
		s.session(st)
		return
	}
	s.register(st)
}

// register is TS 24.229 5.4.1.2. The integrity-protected parameter that
// the P-CSCF wrote tells IMS AKA from SIP Digest without TLS (5.4.1.1).
//
// Under the lock of the user it reads what it holds for the user, which
// tells the type of the registration procedure and whether the REGISTER
// starts one or answers its challenge, and counts the procedure (TS
// 32.409 4.1.1).
//
// SIMPLIFIED: TS 32.409 4.1.1.1.1 and 4.1.1.1.3. A REGISTER that the
// S-CSCF answers before it reads that counts in no registration
// measurement: one that it cannot read (400), one whose user it cannot
// lock (500), and one that it refuses while it is not UNLOCKED (503).
// Whether a REGISTER is an initial registration or a re-registration
// depends on the registration of the user.
func (s *SCSCF) register(st *sip.ServerTransaction) {
	req := st.Request
	publicID, err := ims.PublicIdentity(req.Get("To"))
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	privateID, creds, err := ims.PrivateIdentity(req, publicID)
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	var integrity sip.IntegrityProtected
	if creds != nil {
		integrity = creds.IntegrityProtected()
	}
	seconds, wildcard, err := sip.RequestedExpires(req)
	if err != nil {
		s.respond(st, 400, nil)
		return
	}
	// RFC 3261 10.2.3 and 10.3 step 6: a REGISTER without a Contact asks
	// for the bindings and changes none, so the interval it may carry in
	// Expires means nothing, and none of the checks of the interval below
	// applies to it. It is still authenticated (10.3 step 2).
	query := req.Get("Contact") == ""
	expires := defaultExpires
	if seconds != sip.NoExpires && !query {
		expires = time.Duration(seconds) * time.Second
	}
	log := s.log.With("private_identity", privateID, "public_identity", publicID, "call_id", req.Get("Call-ID"))
	ctx := context.Background()
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		s.lockFailed(st, err, log)
		return
	}
	defer unlock()

	current, registered, err := s.store.Registration(ctx, privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	ch, pending, err := s.store.Challenge(ctx, privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	// The Store keeps a challenge for reg-await-auth after its Expires, so
	// that the expiry is still handled when it comes up late. A challenge
	// past its Expires is no longer one that a REGISTER can answer: the
	// authentication has failed (TS 24.229 5.4.1.2.3), so it ends here,
	// as the expiry would have ended it, and the REGISTER is handled as
	// one with no challenge pending.
	if pending && !time.Now().Before(ch.Expires) {
		if err := s.endRegAwaitAuth(ctx, ch); err != nil {
			s.storeFailed(st, err, log)
			return
		}
		pending = false
	}
	kind := ims.RegistrationTypeOf(expires, registered)
	if !answersChallenge(creds, ch, pending) {
		s.registrations.Started(role, kind)
	}
	defer func() { s.registrations.Answered(role, kind, st.FinalStatus()) }()

	if expires == 0 {
		// TS 24.229 5.4.1.4.1: nothing to deregister gets 481 before any
		// authentication, and an existing IP association needs no new
		// authentication.
		if !registered {
			s.respond(st, 481, nil)
			return
		}
		// With IMS AKA, a protected REGISTER needs no new authentication
		// either (5.4.1.2.2 step 2).
		if integrity == sip.IPAssociationYes || integrity == sip.IntegrityProtectedYes {
			s.deregister(st, privateID, wildcard, log)
			return
		}
	}
	if expires != 0 && expires < s.cfg.MinExpires {
		s.respond(st, 423, func(r *sip.Message) {
			r.Add("Min-Expires", strconv.Itoa(int(s.cfg.MinExpires/time.Second)))
		})
		return
	}
	// 5.4.1.1 items 1 and 2.
	if integrity == sip.IntegrityProtectedNo || integrity == sip.IntegrityProtectedYes {
		s.registerAKA(st, privateID, publicID, current.ServerName, *creds, integrity == sip.IntegrityProtectedYes, min(expires, s.cfg.MaxExpires), ch, pending, log)
		return
	}

	checksResponse := creds != nil && creds.Response != "" && pending &&
		(integrity == sip.IPAssociationPending || integrity == sip.IPAssociationYes)
	if !checksResponse {
		// TS 24.229 5.4.1.2.2A step 1: a new challenge to a REGISTER that
		// carried a digest response says stale=true.
		stale := creds != nil && creds.Response != ""
		s.challenge(st, privateID, publicID, current.ServerName, integrity, stale, log)
		return
	}
	switch s.check(req, *creds, ch) {
	case wrong:
		// TS 24.229 5.4.1.2.3B: a wrong response or Call-ID gets 403.
		s.authenticationFailed(st, privateID, publicID, ch, "authentication failed", log)
		return
	case stale:
		// TS 24.229 5.4.1.2.3B: the password was right but the nonce or
		// the nonce count was not; challenge again with stale=true.
		log.Info("stale nonce")
		s.challenge(st, privateID, publicID, current.ServerName, integrity, true, log)
		return
	}
	if err := s.store.DeleteChallenge(ctx, privateID); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if expires == 0 {
		s.deregister(st, privateID, wildcard, log)
		return
	}
	s.accept(st, *creds, ch, min(expires, s.cfg.MaxExpires), log)
}

// answersChallenge reports whether a REGISTER with creds answers the
// challenge that the S-CSCF holds for the user, if pending: it names the
// nonce of that challenge. Such a REGISTER goes on with the registration
// procedure that the challenged one started.
func answersChallenge(creds *sip.Credentials, ch Challenge, pending bool) bool {
	return pending && creds != nil && creds.Nonce == ch.Nonce
}

// challenge runs the MAR and answers 401 (TS 24.229 5.4.1.2.1 and
// 5.4.1.2.1B). serverName is the one that the registration of the user
// holds, if the user has one (assignedName).
func (s *SCSCF) challenge(st *sip.ServerTransaction, privateID, publicID, serverName string, integrity sip.IntegrityProtected, stale bool, log *slog.Logger) {
	// TS 24.229 5.4.1.1 item 9: without "integrity-protected" the S-CSCF
	// lets the HSS choose the scheme.
	scheme := cx.SchemeSIPDigest
	if integrity == "" {
		scheme = cx.SchemeUnknown
	}
	serverName = s.assignedName(serverName)
	answer, err := s.request(context.Background(), cx.MultimediaAuthRequest{
		Header:                  diameter.NewHeader(s.node, "", s.cfg.HomeDomain),
		UserName:                privateID,
		PublicIdentity:          publicID,
		SIPAuthenticationScheme: scheme,
		SIPNumberAuthItems:      1,
		ServerName:              serverName,
	}.Message())
	if err != nil {
		log.Warn("MAR failed", "error", err.Error())
		s.respond(st, 500, nil)
		return
	}
	maa, err := cx.ParseMultimediaAuthAnswer(answer)
	if err != nil || !maa.Result.Success() || maa.SIPDigestAuthenticate == nil {
		log.Info("HSS refused authentication", "result", describe(maa.Result, err))
		s.respond(st, 403, nil)
		return
	}
	d := maa.SIPDigestAuthenticate
	algorithm := d.DigestAlgorithm
	if algorithm == "" {
		algorithm = "MD5"
	}
	ch := Challenge{
		PrivateIdentity: privateID,
		PublicIdentity:  publicID,
		CallID:          st.Request.Get("Call-ID"),
		Realm:           d.DigestRealm,
		Nonce:           rand.Text(),
		Algorithm:       algorithm,
		HA1:             d.DigestHA1,
		HSSHost:         originHost(answer),
		ServerName:      serverName,
	}
	if err := s.putChallenge(ch); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	qop := d.DigestQoP
	if qop == "" {
		qop = "auth"
	}
	log.Info("challenged", "nonce", ch.Nonce, "stale", stale)
	// SIMPLIFIED: TS 24.229 5.4.1.2.1B and RFC 8760. The S-CSCF should
	// offer one WWW-Authenticate per algorithm it supports, SHA-256
	// first, with MD5 kept for backward compatibility. The HSS stub hands
	// out only the MD5 H(A1), so MD5 is the only one offered.
	s.respond(st, 401, func(r *sip.Message) {
		r.Add("WWW-Authenticate", sip.Challenge{Realm: ch.Realm, Nonce: ch.Nonce, Stale: stale, Algorithm: algorithm, QOP: []string{qop}}.String())
	})
}

// putChallenge stores ch with its expiries, which puts reg-await-auth in
// the store (TS 24.229 5.4.1.2.1). The Store keeps the challenge for
// reg-await-auth again after that, so that the instance which handles
// the expiry still finds it: by then the expiry has ended the challenge,
// or the authentication has.
func (s *SCSCF) putChallenge(ch Challenge) error {
	ch.Expires = time.Now().Add(s.cfg.RegAwaitAuth)
	ch.KeepUntil = ch.Expires.Add(s.cfg.RegAwaitAuth)
	return s.store.PutChallenge(context.Background(), ch)
}

type verdict int

const (
	valid verdict = iota
	stale
	wrong
)

// check is TS 24.229 5.4.1.2.2A steps 1, 3 and 4 with the outcomes of
// 5.4.1.2.3B, and for IMS AKA 5.4.1.2.2 steps 1, 3 and 4 with those of
// 5.4.1.2.3A. The response is computed over the nonce the UE used, so a
// correct password with an old nonce is told apart from a wrong one.
func (s *SCSCF) check(req *sip.Message, c sip.Credentials, ch Challenge) verdict {
	if req.Get("Call-ID") != ch.CallID || c.Realm != ch.Realm || c.CNonce == "" || c.NC == "" {
		return wrong
	}
	want, err := sip.DigestResponse(ch.digestHA1(), sip.REGISTER, c)
	if err != nil || subtle.ConstantTimeCompare([]byte(want), []byte(c.Response)) != 1 {
		return wrong
	}
	// A fresh nonce is answered with the first nonce count.
	if c.Nonce != ch.Nonce || c.NC != "00000001" {
		return stale
	}
	return valid
}

// accept runs the SAR, stores the binding and answers 200 OK
// (TS 24.229 5.4.1.2.2A steps 5 to 10, and 5.4.1.2.2F). The subscribers
// to the registration then learn of it (5.4.2.1.2).
func (s *SCSCF) accept(st *sip.ServerTransaction, c sip.Credentials, ch Challenge, expires time.Duration, log *slog.Logger) {
	req := st.Request
	ctx := context.Background()
	old, registered, err := s.store.Registration(ctx, ch.PrivateIdentity)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if req.Get("Contact") == "" {
		s.answerQuery(st, old, registered, c, ch, log)
		return
	}
	contact, contactErr := sip.ParseNameAddr(req.Get("Contact"))
	if registered && contactErr == nil && sameURI(contact.URI, old.Contact) && outOfOrder(req, old) {
		s.refuseOutOfOrder(st, old, log)
		return
	}
	assignment := cx.AssignRegistration
	if registered {
		assignment = cx.ReRegistration
	}
	saa, err := s.serverAssignment(ctx, ch.PrivateIdentity, ch.PublicIdentity, ch.HSSHost, ch.ServerName, assignment)
	if err != nil || !saa.Result.Success() {
		log.Warn("SAR failed", "result", describe(saa.Result, err))
		s.respond(st, 500, nil)
		return
	}
	reg := Registration{
		PrivateIdentity: ch.PrivateIdentity,
		HSSHost:         ch.HSSHost,
		ServerName:      s.assignedName(ch.ServerName),
		Expires:         time.Now().Add(expires),
		Path:            req.GetAll("Path"),
	}
	if profile, err := registrableProfile(saa.UserData); err == nil {
		reg.PublicIdentities, reg.BarredIdentities = profile.PublicIdentities, profile.BarredIdentities
		reg.InitialFilterCriteria = profile.InitialFilterCriteria
	} else if registered {
		reg.PublicIdentities, reg.BarredIdentities = old.PublicIdentities, old.BarredIdentities
		reg.InitialFilterCriteria = old.InitialFilterCriteria
	} else {
		log.Warn("SAA has no usable User-Data", "error", err.Error())
		s.respond(st, 500, nil)
		return
	}
	if contactErr == nil {
		reg.Contact = contact.URI
		reg.ContactParams = bindingParams(contact.Params)
	}
	cseq, _ := sip.ParseCSeq(req.Get("CSeq"))
	reg.CallID, reg.CSeq = req.Get("Call-ID"), cseq.Number
	// TS 24.229 5.4.2.1.2: a new binding gets a new contact id and the
	// event registered, and replaces the binding of an older contact; a
	// binding that stays is refreshed.
	var replaced *Registration
	if registered && sameURI(old.Contact, reg.Contact) {
		reg.ContactID, reg.ContactEvent = old.ContactID, reginfo.Refreshed
	} else {
		reg.ContactID, reg.ContactEvent = sip.NewTag(), reginfo.Registered
		if registered {
			replaced = &old
		}
	}
	if err := s.store.PutRegistration(ctx, reg); err != nil {
		// The HSS has the user registered here, which a REGISTER that
		// the UE sends again carries on from.
		s.storeFailed(st, err, log)
		return
	}
	log.Info("registered", "contact", reg.Contact, "expires", int(expires/time.Second), "assignment", assignment.String())

	s.respond(st, 200, s.bindingResponse(reg, expires, c, ch))
	s.notifyRegistration(ctx, reg, replaced, log)
	s.notifyApplicationServers(req, reg, expires, log)
}

// bindingResponse fills the 200 (OK) to a REGISTER with the binding that
// reg holds, with expires as the time it has left.
func (s *SCSCF) bindingResponse(reg Registration, expires time.Duration, c sip.Credentials, ch Challenge) func(*sip.Message) {
	return func(r *sip.Message) {
		for _, p := range reg.Path {
			r.Add("Path", p)
		}
		r.Add("Service-Route", fmt.Sprintf("<sip:orig@%s:%d;lr>", s.cfg.Host, s.cfg.Port))
		// 5.4.1.2.2F item b: the default public identity first, and no
		// barred identity. A UE that registered a barred one learns from
		// its absence that it may not use it (5.1.1.2.1 item c).
		for _, id := range reg.NonBarredIdentities() {
			r.Add("P-Associated-URI", "<"+id+">")
		}
		// RFC 3261 10.3 step 8: the expires the registrar chose.
		params := append(slices.Clone(reg.ContactParams), sip.Param{Name: "expires", Value: strconv.Itoa(int(expires / time.Second))})
		r.Add("Contact", sip.NameAddr{URI: reg.Contact, Params: params}.String())
		// RFC 2617 3.2.3: rspauth proves that the S-CSCF knows H(A1) too;
		// with IMS AKA, XRES is the password (RFC 3310 3.5). A REGISTER
		// that answered no challenge gets none.
		if ha1 := ch.digestHA1(); ha1 != "" {
			rspauth, _ := sip.DigestResponse(ha1, "", c)
			r.Add("Authentication-Info", fmt.Sprintf(`qop=auth, rspauth="%s", cnonce="%s", nc=%s`, rspauth, c.CNonce, c.NC))
		}
	}
}

// answerQuery answers a REGISTER without a Contact, which fetches the
// bindings (RFC 3261 10.2.3). Step 6 of 10.3 skips the update of the
// bindings for it, and step 8 answers with the bindings as they are, each
// with the time it has left: the stored contact and expiry stay, and a user
// who is not registered gets a 200 without a Contact, with no binding
// made for it. TS 24.229 5.1.1.4.1 lets a UE fetch the bindings this way
// after the initial registration.
//
// Nothing changes in the registration, so no SAR goes out: the SAR
// tells the HSS of a change in the registration state of the user, and
// there is none. For the same reason there are no third-party REGISTERs
// to the application servers, and no NOTIFYs to the subscribers to the
// registration, who learn of changes only.
func (s *SCSCF) answerQuery(st *sip.ServerTransaction, reg Registration, registered bool, c sip.Credentials, ch Challenge, log *slog.Logger) {
	left := time.Until(reg.Expires)
	if !registered || left <= 0 {
		// An expiry that the sweep has not handled yet is no binding any
		// more (Store.RegistrationOf), and the sweep still ends it.
		log.Info("bindings fetched, none")
		s.respond(st, 200, nil)
		return
	}
	// Whole seconds, rounded up: 0 would read as the end of the binding.
	left = (left + time.Second - 1) / time.Second * time.Second
	log.Info("bindings fetched", "contact", reg.Contact, "expires", int(left/time.Second))
	s.respond(st, 200, s.bindingResponse(reg, left, c, ch))
}

// outOfOrder is RFC 3261 10.3 steps 6 and 7 for the binding that reg
// holds: a REGISTER with the Call-ID of the binding updates or removes it
// only with a CSeq higher than the stored one. This keeps a request that
// the network reordered, such as a de-registration that comes after the
// re-registration that followed it, from undoing the newer one. A request
// with another Call-ID is not compared.
func outOfOrder(req *sip.Message, reg Registration) bool {
	if req.Get("Call-ID") != reg.CallID {
		return false
	}
	cseq, _ := sip.ParseCSeq(req.Get("CSeq"))
	return cseq.Number <= reg.CSeq
}

// refuseOutOfOrder answers a REGISTER that outOfOrder found, and leaves
// the binding as it is. RFC 3261 10.3 step 7 says only that the request
// fails; 500 (Server Internal Error) is the code that the same step names
// for a binding update that fails. No Retry-After goes with it: the same
// request would fail again, so waiting does not help.
func (s *SCSCF) refuseOutOfOrder(st *sip.ServerTransaction, reg Registration, log *slog.Logger) {
	log.Info("REGISTER out of order", "cseq", st.Request.Get("CSeq"), "stored_cseq", reg.CSeq)
	s.respond(st, 500, nil)
}

// deregister is TS 24.229 5.4.1.4.1.
func (s *SCSCF) deregister(st *sip.ServerTransaction, privateID string, wildcard bool, log *slog.Logger) {
	ctx := context.Background()
	reg, ok, err := s.store.Registration(ctx, privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if !ok {
		s.respond(st, 481, nil)
		return
	}
	if !wildcard {
		contact, err := sip.ParseNameAddr(st.Request.Get("Contact"))
		if err != nil || !sameURI(contact.URI, reg.Contact) {
			s.respond(st, 481, nil)
			return
		}
	}
	if outOfOrder(st.Request, reg) {
		s.refuseOutOfOrder(st, reg, log)
		return
	}
	// TS 24.229 5.4.1.4.1: the S-CSCF removes the registration from its
	// local data once the procedure with the HSS (TS 29.228 6.1.2) has
	// completed, and TS 23.228 5.3.1 steps 7 to 9 have the HSS answer
	// the SAR before the S-CSCF answers 200.
	saa, err := s.serverAssignment(ctx, privateID, reg.PublicIdentities[0], reg.HSSHost, reg.ServerName, cx.UserDeregistration)
	switch {
	case err == nil && saa.Result.Success():
	case err == nil && saa.Result == diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered):
		// TS 29.228 8.1.2: the HSS holds the user at another S-CSCF,
		// which has taken the user over, and keeps it there. The
		// registration here is this S-CSCF's to end (8.1.1).
	default:
		// Without an SAA, the S-CSCF cannot tell whether the SAR reached
		// the HSS or only its answer was lost; a failed SAA says the HSS
		// changed nothing (TS 29.228 6.1.2.1). The S-CSCF keeps the
		// registration and answers 500, as for a SAR of a registration
		// that fails. The REGISTER that the UE sends again sends the SAR
		// again, and the expiry of the registration ends it otherwise.
		log.Warn("deregistration refused: SAR failed", "result", describe(saa.Result, err))
		s.respond(st, 500, nil)
		return
	}
	if err := s.store.DeleteRegistration(ctx, privateID); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	log.Info("deregistered")
	s.respond(st, 200, func(r *sip.Message) {
		r.Add("Contact", fmt.Sprintf("<%s>;expires=0", reg.Contact))
	})
	s.endRegistration(ctx, reg, reginfo.Unregistered, log)
	s.notifyApplicationServers(st.Request, reg, 0, log)
}

// Deregister is TS 24.229 5.4.1.5 when the S-CSCF itself ends a
// registration: the NOTIFY tells the UE with event whether to register
// again, reginfo.Deactivated if it should, reginfo.Rejected if that
// would not help. The S-CSCF then tells the HSS with a SAR of
// ADMINISTRATIVE_DEREGISTRATION (TS 29.229 6.3.15; the flow is TS
// 29.228 A.4.4.3).
//
// SIMPLIFIED: TS 24.229 5.4.1.5. Only tests call it: the maintenance API
// of the S-CSCF has no operation that ends a registration. The HSS asks
// for a deregistration
// with an RTR instead, which Handle takes. The S-CSCF ends a
// registration on its own only when an application server fails its
// third-party REGISTER with SESSION_TERMINATED (5.4.1.7).
func (s *SCSCF) Deregister(privateID string, event reginfo.Event) error {
	if event != reginfo.Deactivated && event != reginfo.Rejected {
		return fmt.Errorf("scscf: a network-initiated deregistration is deactivated or rejected, not %s", event)
	}
	ctx := context.Background()
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		return err
	}
	defer unlock()
	reg, ok, err := s.store.Registration(ctx, privateID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("scscf: %s is not registered", privateID)
	}
	if err := s.deregisterByNetwork(ctx, reg, event); err != nil {
		return err
	}
	_, err = s.serverAssignment(ctx, privateID, reg.PublicIdentities[0], reg.HSSHost, reg.ServerName, cx.AdministrativeDeregistration)
	return err
}

// deregisterByNetwork removes reg and sends the NOTIFYs of TS 24.229
// 5.4.1.5 with event. The lock of the user is held. It returns the
// failure of the store to remove reg, and then has sent nothing.
//
// SIMPLIFIED: TS 24.229 5.4.1.5. Before the NOTIFY, the S-CSCF should
// also release the sessions of the contact (5.4.5.1.2); as a proxy it
// keeps no state of the dialogs it routes, so it cannot.
func (s *SCSCF) deregisterByNetwork(ctx context.Context, reg Registration, event reginfo.Event) error {
	log := s.log.With("private_identity", reg.PrivateIdentity)
	if err := s.store.DeleteRegistration(ctx, reg.PrivateIdentity); err != nil {
		return err
	}
	log.Info("deregistered by the network", "event", string(event))
	s.endRegistration(ctx, reg, event, log)
	s.notifyApplicationServers(s.equivalentRegister(reg), reg, 0, log)
	return nil
}

// Handle is the diameter.Handler of the S-CSCF. The HSS sends it two Cx
// requests: the Registration-Termination-Request, which deregisters a
// user (TS 29.228 6.1.3), and the Push-Profile-Request, which updates
// the user information it holds (6.2.2).
//
// Each is counted on its receipt, where TS 32.409 4.1.1.3.2.1 counts
// the de-registrations the HSS starts and 4.1.7.1 the user profile
// updates it starts, and its answer as the S-CSCF hands it back, where
// 4.1.1.3.2.2 and 4.1.7.2 count the ones that succeeded and 4.1.1.3.2.3
// and 4.1.7.3 the ones that failed, by the Diameter result code. The
// result label holds that code, so the successes and the failures of a
// command are read apart without a measurement of each.
func (s *SCSCF) Handle(request *diam.Message) *diam.Message {
	s.measured.RequestReceived(request)
	answer := s.answer(request)
	s.measured.AnswerSent(request, answer)
	return answer
}

// answer is the answer that Handle gives request.
func (s *SCSCF) answer(request *diam.Message) *diam.Message {
	identity := s.node.Identity()
	if request.Header.ApplicationID != diameter.ApplicationIDCx {
		return diameter.CommandUnsupportedAnswer(identity, request)
	}
	switch request.Header.CommandCode {
	case cx.CommandRegistrationTermination:
		rtr, err := cx.ParseRegistrationTerminationRequest(request)
		if err != nil {
			s.log.Warn("cannot read RTR", "error", err.Error())
			return cx.NewAnswer(request, identity, diameter.Base(diameter.DiameterUnableToComply))
		}
		return s.registrationTermination(rtr).Message(request, identity)
	case cx.CommandPushProfile:
		ppr, err := cx.ParsePushProfileRequest(request)
		if err != nil {
			s.log.Warn("cannot read PPR", "error", err.Error())
			return cx.NewAnswer(request, identity, diameter.Base(diameter.DiameterUnableToComply))
		}
		return s.pushProfile(ppr).Message(request, identity)
	}
	return diameter.CommandUnsupportedAnswer(identity, request)
}

// registrableProfile reads the User-Data of the SAA of a registration:
// a profile with a default public identity (cx.UserProfile.Registrable).
func registrableProfile(data []byte) (cx.UserProfile, error) {
	profile, err := cx.ParseUserProfile(data)
	if err != nil {
		return cx.UserProfile{}, err
	}
	return profile, profile.Registrable()
}

// deregistrationEvents is the event that the NOTIFY gives for each
// Reason-Code of an RTR (TS 24.229 5.4.1.5): rejected when registering
// again would not help, deactivated when the UE is to register again.
var deregistrationEvents = map[cx.ReasonCode]reginfo.Event{
	// TS 29.228 6.1.3.1: no other S-CSCF can be assigned.
	cx.PermanentTermination: reginfo.Rejected,
	// TS 29.228 6.1.3.1: the user is asked to register again.
	cx.ServerChange: reginfo.Deactivated,
}

// registrationTermination is the S-CSCF side of the network initiated
// de-registration by the HSS, TS 29.228 6.1.3.1, and the end of TS
// 24.229 5.4.1.5: the S-CSCF removes what it has for the user and sends
// the NOTIFYs, but sends no SAR, because the HSS has already
// deregistered the user (TS 29.228 A.4.4.2).
//
// SIMPLIFIED: TS 29.228 6.1.3.1. NEW_SERVER_ASSIGNED and REMOVE_S-CSCF
// get DIAMETER_UNABLE_TO_COMPLY. The HSS stub never sends them: it
// sends no RTR when the MAR of another S-CSCF takes a user over (TS
// 29.228 8.1.1), and it takes no user who is not registered away from
// the S-CSCF, which lets the profile of such a user go by itself
// (UnregisteredProfileLifetime).
func (s *SCSCF) registrationTermination(r cx.RegistrationTerminationRequest) cx.RegistrationTerminationAnswer {
	event, ok := deregistrationEvents[r.DeregistrationReason.ReasonCode]
	if !ok {
		s.log.Info("RTR with a reason not handled", "private_identity", r.UserName, "reason", r.DeregistrationReason.ReasonCode.String())
		return cx.RegistrationTerminationAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
	}
	ctx := context.Background()
	unlock, err := s.users.Lock(ctx, r.UserName)
	if err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	defer unlock()
	reg, ok, err := s.store.Registration(ctx, r.UserName)
	if err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	if !ok || !covers(reg, r.PublicIdentities) {
		// TS 29.229 6.2.2.1. TS 29.228 6.1.3.1 names no error of its own
		// for a user the S-CSCF does not know.
		s.log.Info("RTR for a user not registered here", "private_identity", r.UserName)
		return cx.RegistrationTerminationAnswer{Result: diameter.Experimental(cx.DiameterErrorUserUnknown)}
	}
	// TS 29.228 6.1.3.1: the S-CSCF removes all it has stored for the
	// user, a registration under way included.
	if err := s.store.DeleteChallenge(ctx, r.UserName); err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	if err := s.deregisterByNetwork(ctx, reg, event); err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	return cx.RegistrationTerminationAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
}

// registrationTerminationFailed answers an RTR whose user the S-CSCF
// cannot read or deregister. It can tell the HSS neither that it does
// not know the user nor that it has deregistered the user, so it
// answers DIAMETER_UNABLE_TO_COMPLY (RFC 6733 7.1.5). The registration
// stays as it was.
func (s *SCSCF) registrationTerminationFailed(r cx.RegistrationTerminationRequest, err error) cx.RegistrationTerminationAnswer {
	s.log.Warn("RTR not served: the store failed", "private_identity", r.UserName, "error", err.Error())
	return cx.RegistrationTerminationAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
}

// pushProfile is the S-CSCF side of the HSS initiated update of user
// information, TS 29.228 6.2.2.1: for the public identities of the
// profile in the request, the S-CSCF replaces what it holds with what
// the HSS sent. It holds a user profile in the registration of a user,
// and in the profile it took for a user who is not registered; a user it
// has neither of is unknown to it. The identities of the request are one
// implicit registration set with their service profiles (6.5.2.1), which
// is what either of those holds, so the whole of it is replaced.
func (s *SCSCF) pushProfile(r cx.PushProfileRequest) cx.PushProfileAnswer {
	log := s.log.With("private_identity", r.UserName)
	if len(r.UserData) == 0 {
		// 6.2.2.1: a request carries at least one of the pieces of user
		// information, and the user profile is the only one this S-CSCF
		// keeps.
		log.Info("PPR without User-Data")
		return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
	}
	profile, err := cx.ParseUserProfile(r.UserData)
	if err != nil {
		// Table 6.2.2.1.1: user information that the S-CSCF cannot
		// recognise. It keeps what it has (6.2.2.1).
		log.Warn("PPR has no usable User-Data", "error", err.Error())
		return cx.PushProfileAnswer{Result: diameter.Experimental(cx.DiameterErrorNotSupportedUserData)}
	}
	ctx := context.Background()
	unlock, err := s.users.Lock(ctx, r.UserName)
	if err != nil {
		return s.pushProfileFailed(r, err)
	}
	defer unlock()
	reg, registered, err := s.store.Registration(ctx, r.UserName)
	if err != nil {
		return s.pushProfileFailed(r, err)
	}
	if registered {
		if err := profile.Registrable(); err != nil {
			// Table 6.2.2.1.1: user information that the S-CSCF cannot take
			// for a registered user.
			log.Warn("PPR has no usable User-Data", "error", err.Error())
			return cx.PushProfileAnswer{Result: diameter.Experimental(cx.DiameterErrorNotSupportedUserData)}
		}
		reg.PublicIdentities, reg.BarredIdentities = profile.PublicIdentities, profile.BarredIdentities
		reg.InitialFilterCriteria = profile.InitialFilterCriteria
		if err := s.store.PutRegistration(ctx, reg); err != nil {
			return s.pushProfileFailed(r, err)
		}
		log.Info("took a pushed user profile", "state", "registered", "criteria", len(profile.InitialFilterCriteria))
		return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	}
	p, kept, err := s.store.UnregisteredProfile(ctx, r.UserName)
	if err != nil {
		return s.pushProfileFailed(r, err)
	}
	if kept && time.Now().Before(p.Expires) {
		p.PublicIdentities, p.BarredIdentities = profile.PublicIdentities, profile.BarredIdentities
		p.InitialFilterCriteria = profile.InitialFilterCriteria
		// Expires stays where it is: the push does not make the S-CSCF keep
		// the profile of a user who is not registered any longer than it
		// would have.
		if err := s.store.PutUnregisteredProfile(ctx, p); err != nil {
			return s.pushProfileFailed(r, err)
		}
		log.Info("took a pushed user profile", "state", "unregistered", "criteria", len(profile.InitialFilterCriteria))
		return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	}
	// TS 29.228 Table 6.2.2.1.1: the private identity is not found in the
	// S-CSCF, which is DIAMETER_ERROR_USER_UNKNOWN (TS 29.229 6.2.2.1).
	// The profile of a user who is not registered may have run out here
	// while the HSS still had the user in the unregistered state.
	log.Info("PPR for a user whose profile this S-CSCF does not hold")
	return cx.PushProfileAnswer{Result: diameter.Experimental(cx.DiameterErrorUserUnknown)}
}

// pushProfileFailed answers a PPR that the S-CSCF could not take: it can
// say neither that it does not know the user nor that it has the new
// profile, so it answers DIAMETER_UNABLE_TO_COMPLY (TS 29.228 Table
// 6.2.2.1.1). What it holds is unchanged, and the HSS logs the answer.
func (s *SCSCF) pushProfileFailed(r cx.PushProfileRequest, err error) cx.PushProfileAnswer {
	s.log.Warn("PPR not taken: the store failed", "private_identity", r.UserName, "error", err.Error())
	return cx.PushProfileAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
}

// covers reports whether an RTR that names ids is about reg. No
// Public-Identity means all identities of the user (TS 29.228 6.1.3.1).
// Naming any of them is enough, because reg holds one implicit
// registration set, which is deregistered as a whole.
func covers(reg Registration, ids []string) bool {
	return len(ids) == 0 || slices.ContainsFunc(ids, func(id string) bool {
		return slices.ContainsFunc(reg.PublicIdentities, func(own string) bool { return ims.SamePublicIdentity(own, id) })
	})
}

// regAwaitAuthExpired ends the challenge of nonce, whose REGISTER never
// came, and tells the HSS (TS 24.229 5.4.1.2.3). It returns the failure
// of the store or of the lock that left the challenge where it was, so
// that the expiry is handled again (handleExpiry).
func (s *SCSCF) regAwaitAuthExpired(ctx context.Context, privateID, nonce string) error {
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		return fmt.Errorf("the lock of the user was not taken: %w", err)
	}
	defer unlock()
	ch, ok, err := s.store.Challenge(ctx, privateID)
	if err != nil {
		return err
	}
	if !ok || ch.Nonce != nonce {
		return nil
	}
	return s.endRegAwaitAuth(ctx, ch)
}

// endRegAwaitAuth ends the challenge ch, whose reg-await-auth has run
// out, and tells the HSS (TS 24.229 5.4.1.2.3). The lock of the user is
// held: the expiry takes it in regAwaitAuthExpired, and a REGISTER that
// finds the challenge late already holds it (register).
func (s *SCSCF) endRegAwaitAuth(ctx context.Context, ch Challenge) error {
	// TS 24.229 5.4.1.2.3 NOTE 2: authentication has failed; a user who
	// was registered stays registered.
	s.log.Info("reg-await-auth expired", "private_identity", ch.PrivateIdentity)
	if err := s.store.DeleteChallenge(ctx, ch.PrivateIdentity); err != nil {
		return err
	}
	s.serverAssignment(ctx, ch.PrivateIdentity, ch.PublicIdentity, ch.HSSHost, ch.ServerName, cx.AuthenticationTimeout)
	return nil
}

// registrationExpired ends a registration that was not refreshed, and
// tells the subscribers that its contact expired (TS 24.229 5.4.2.1.2).
// The SAR uses TIMEOUT_DEREGISTRATION (TS 29.229 6.3.15; the flow is TS
// 29.228 A.4.4.1).
//
// No third-party REGISTER goes to the application servers. TS 24.229
// 5.4.1.7 sends one after a registration (5.4.1.2), a user-initiated
// deregistration (5.4.1.4) and a network-initiated one (5.4.1.5), and
// expiry is none of them. The application server already knows when
// the registration ends, because the third-party REGISTER carried the
// same expiration time as the 200 (OK) to the UE (TS 23.218 6.3,
// 5.4.1.7 item e), and one that subscribed to the reg event package
// hears of the expiry in the NOTIFY.
//
// It returns the failure of the store or of the lock that left the
// registration where it was, so that the expiry is handled again
// (handleExpiry).
func (s *SCSCF) registrationExpired(ctx context.Context, privateID string) error {
	log := s.log.With("private_identity", privateID)
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		return fmt.Errorf("the lock of the user was not taken: %w", err)
	}
	defer unlock()
	reg, ok, err := s.store.Registration(ctx, privateID)
	if err != nil {
		return err
	}
	// Under the lock, the registration may have ended or been refreshed
	// since its expiry came up.
	if !ok || time.Now().Before(reg.Expires) {
		return nil
	}
	log.Info("registration expired")
	if err := s.store.DeleteRegistration(ctx, privateID); err != nil {
		return err
	}
	// The registration is gone, so the expiry is handled: a NOTIFY or a
	// SAR that does not get through is not sent again.
	//
	// SIMPLIFIED: TS 24.229 5.4.2.1.2 and TS 29.228 6.1.2. The expiry
	// is an event of the user's registration state, which the NOTIFYs
	// tell the subscribers, and the SAR tells the HSS that the S-CSCF no
	// longer serves the user. Both go out only after the registration is
	// deleted from the store. An instance that stops after the delete
	// sends neither, and the instance that claims the expiry again finds
	// no registration: no subscriber gets a NOTIFY of the expiry, and the
	// HSS keeps the user registered until a later SAR for the user
	// changes that. Sending first and deleting afterwards would send the
	// NOTIFYs and the SAR again whenever the delete fails or the instance
	// stops before it, since the expiry is then handled once more.
	s.endRegistration(ctx, reg, reginfo.Expired, log)
	s.serverAssignment(ctx, privateID, reg.PublicIdentities[0], reg.HSSHost, reg.ServerName, cx.TimeoutDeregistration)
	return nil
}

// serverAssignment sends a SAR of type t to hssHost, under the
// serverName that the challenge, the registration or the profile of the
// user holds (assignedName).
func (s *SCSCF) serverAssignment(ctx context.Context, privateID, publicID, hssHost, serverName string, t cx.ServerAssignmentType) (cx.ServerAssignmentAnswer, error) {
	answer, err := s.request(ctx, cx.ServerAssignmentRequest{
		Header:               diameter.NewHeader(s.node, hssHost, s.cfg.HomeDomain),
		UserName:             privateID,
		PublicIdentities:     []string{publicID},
		ServerName:           s.assignedName(serverName),
		ServerAssignmentType: t,
	}.Message())
	if err != nil {
		s.log.Warn("SAR failed", "private_identity", privateID, "type", t.String(), "error", err.Error())
		return cx.ServerAssignmentAnswer{}, err
	}
	return cx.ParseServerAssignmentAnswer(answer)
}

// assignedName is the Server-Name of a Cx request about a user whose
// challenge, registration or profile holds stored. The instances of one
// S-CSCF that share a store may each have a name of their own, and the
// HSS holds the user under the name of the instance that assigned the
// user (TS 29.228 8.1 and 8.1.2), whichever instance goes on with the
// user. With no name stored, for a user that this S-CSCF does not hold
// yet or in a record that an S-CSCF wrote without one, it is the name of
// this instance.
func (s *SCSCF) assignedName(stored string) string {
	if stored == "" {
		return s.cfg.ServerName
	}
	return stored
}

// request sends a Cx request and waits for its answer, until ctx ends: a
// request of a claimed expiry ends with the claim, one of a SIP
// transaction with the transaction.
func (s *SCSCF) request(ctx context.Context, m *diam.Message) (*diam.Message, error) {
	return s.hss.Request(ctx, m)
}

// storeFailed answers st with 500 (Server Internal Error): the S-CSCF
// cannot read or write the state that the request needs (RFC 3261
// 21.5.1), and takes the failure neither for state that is not there
// nor for a write that went through. The UE or the network may send
// the request again.
func (s *SCSCF) storeFailed(st *sip.ServerTransaction, err error, log *slog.Logger) {
	log.Warn("the store failed", "method", string(st.Request.Method), "error", err.Error())
	s.respond(st, 500, nil)
}

// respond answers with a response to the request.
func (s *SCSCF) respond(st *sip.ServerTransaction, code int, fill func(*sip.Message)) {
	resp := sip.NewResponse(st.Request, code, sip.NewTag())
	if fill != nil {
		fill(resp)
	}
	s.addChargingVector(resp, st.Request)
	st.Respond(resp)
}

// addChargingVector puts in a response the icid-value and the orig-ioi
// of the request's P-Charging-Vector, with this network's term-ioi (TS
// 24.229 5.4.1.2.2F (e) for REGISTER,
// 5.4.2.1.1 step 3 for SUBSCRIBE).
func (s *SCSCF) addChargingVector(resp, req *sip.Message) {
	if c, ok := sip.ChargingVectorOf(req); ok {
		resp.Add("P-Charging-Vector", c.Response(s.cfg.HomeDomain).String())
	}
}

// bindingParams are the Contact header field parameters that a binding
// keeps: all but expires, whose value the registrar chooses itself (RFC
// 3261 10.3 steps 7 and 8).
func bindingParams(params sip.Params) sip.Params {
	var kept sip.Params
	for _, p := range params {
		if !strings.EqualFold(p.Name, "expires") {
			kept = append(kept, p)
		}
	}
	return kept
}

func sameURI(a, b string) bool {
	ua, errA := sip.ParseURI(a)
	ub, errB := sip.ParseURI(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ua.User == ub.User && strings.EqualFold(ua.Host, ub.Host) && ua.Port == ub.Port
}

func originHost(m *diam.Message) string {
	var raw struct {
		OriginHost string `avp:"Origin-Host"`
	}
	m.Unmarshal(&raw)
	return raw.OriginHost
}

func describe(r diameter.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return r.String()
}
