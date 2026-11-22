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
	"sync"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
	"github.com/yuu-biz/ims-practice/internal/sipdns"
)

// HSS is how the S-CSCF reaches the HSS over Cx. *diameter.Peer is one.
type HSS interface {
	Request(ctx context.Context, request *diam.Message) (*diam.Message, error)
}

// Config is what the S-CSCF needs to know.
type Config struct {
	// ServerName is the S-CSCF's SIP URI, sent as Server-Name on Cx.
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
	// UserLocks serialises what happens to one user. Nil means a lock
	// per user in the memory of this process (ims.UserLocks), which
	// serves an S-CSCF that runs as one instance. Instances that share a
	// store take the lock of a user in it
	// (internal/kvstore/kvlock), so that only one of them handles the
	// user at a time.
	UserLocks ims.UserLocker
}

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
	// authTimers are the reg-await-auth timers and regTimers the
	// registration timers of this process, by private identity in the
	// form of key. They are kept apart so that an authentication that
	// ends early never touches the expiry of a registration.
	// subscriptionTimers are the expiry timers of the reg event
	// subscriptions, by dialog. When a timer fires, the store decides
	// whether it still applies.
	authTimers         timers[string]
	regTimers          timers[string]
	subscriptionTimers timers[sip.DialogID]
	// unregisteredTimers end the unregistered profiles that this process
	// took, by private identity in the form of key.
	unregisteredTimers timers[string]
}

// timers are timers by key. Their own lock is held only while the map
// changes, never while a timer's function runs.
type timers[K comparable] struct {
	mu sync.Mutex
	m  map[K]*time.Timer
}

func (t *timers[K]) start(k K, d time.Duration, f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if timer, ok := t.m[k]; ok {
		timer.Stop()
	}
	if t.m == nil {
		t.m = map[K]*time.Timer{}
	}
	t.m[k] = time.AfterFunc(d, f)
}

func (t *timers[K]) stop(k K) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if timer, ok := t.m[k]; ok {
		timer.Stop()
		delete(t.m, k)
	}
}

func New(cfg Config, hss HSS, node *diameter.Node, store Store, resolver Resolver, log *slog.Logger) *SCSCF {
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
	return &SCSCF{cfg: cfg, hss: hss, node: node, store: store, resolver: resolver, log: log, users: cfg.UserLocks}
}

// lockFailed answers a request whose user the S-CSCF could not lock
// (500, RFC 3261 21.5.1). Another instance may be handling the user, so
// this one does not go on without the lock.
func (s *SCSCF) lockFailed(st *sip.ServerTransaction, err error, log *slog.Logger) {
	log.Warn("the lock of the user was not taken", "method", string(st.Request.Method), "error", err.Error())
	s.respond(st, 500, nil)
}

// SetStack gives the S-CSCF the stack it runs on, which it needs to
// forward ACKs. Call it right after the stack is made.
func (s *SCSCF) SetStack(stack *sip.Stack) { s.stack.Set(stack) }

// OnRequest is the sip.Config.OnRequest of the S-CSCF.
func (s *SCSCF) OnRequest(st *sip.ServerTransaction) {
	if st.Request.Method != sip.REGISTER {
		s.session(st)
		return
	}
	s.register(st)
}

// register is TS 24.229 5.4.1.2. The integrity-protected parameter that
// the P-CSCF wrote tells IMS AKA from SIP Digest without TLS (5.4.1.1).
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
	expires := defaultExpires
	if seconds != sip.NoExpires {
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

	if expires == 0 {
		// TS 24.229 5.4.1.4.1: nothing to deregister gets 481 before any
		// authentication, and an existing IP association needs no new
		// authentication.
		_, ok, err := s.store.Registration(ctx, privateID)
		if err != nil {
			s.storeFailed(st, err, log)
			return
		}
		if !ok {
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
		s.registerAKA(st, privateID, publicID, *creds, integrity == sip.IntegrityProtectedYes, min(expires, s.cfg.MaxExpires), log)
		return
	}

	ch, pending, err := s.store.Challenge(ctx, privateID)
	if err != nil {
		s.storeFailed(st, err, log)
		return
	}
	answersChallenge := creds != nil && creds.Response != "" && pending &&
		(integrity == sip.IPAssociationPending || integrity == sip.IPAssociationYes)
	if !answersChallenge {
		// TS 24.229 5.4.1.2.2A step 1: a new challenge to a REGISTER that
		// carried a digest response says stale=true.
		stale := creds != nil && creds.Response != ""
		s.challenge(st, privateID, publicID, integrity, stale, log)
		return
	}
	switch s.check(req, *creds, ch) {
	case wrong:
		// TS 24.229 5.4.1.2.3B: a wrong response or Call-ID gets 403.
		s.authenticationFailed(st, privateID, publicID, ch.HSSHost, "authentication failed", log)
		return
	case stale:
		// TS 24.229 5.4.1.2.3B: the password was right but the nonce or
		// the nonce count was not; challenge again with stale=true.
		log.Info("stale nonce")
		s.challenge(st, privateID, publicID, integrity, true, log)
		return
	}
	if err := s.endChallenge(ctx, privateID); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	if expires == 0 {
		s.deregister(st, privateID, wildcard, log)
		return
	}
	s.accept(st, *creds, ch, min(expires, s.cfg.MaxExpires), log)
}

// challenge runs the MAR and answers 401 (TS 24.229 5.4.1.2.1 and
// 5.4.1.2.1B).
func (s *SCSCF) challenge(st *sip.ServerTransaction, privateID, publicID string, integrity sip.IntegrityProtected, stale bool, log *slog.Logger) {
	// TS 24.229 5.4.1.1 item 9: without "integrity-protected" the S-CSCF
	// lets the HSS choose the scheme.
	scheme := cx.SchemeSIPDigest
	if integrity == "" {
		scheme = cx.SchemeUnknown
	}
	answer, err := s.request(cx.MultimediaAuthRequest{
		Header:                  cx.NewHeader(s.node, "", s.cfg.HomeDomain),
		UserName:                privateID,
		PublicIdentity:          publicID,
		SIPAuthenticationScheme: scheme,
		SIPNumberAuthItems:      1,
		ServerName:              s.cfg.ServerName,
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

// putChallenge stores ch with its expiries, and starts its
// reg-await-auth timer (TS 24.229 5.4.1.2.1). The Store keeps the
// challenge for reg-await-auth again after the timer, so that the
// instance that handles the timer still finds it: by then the timer
// has ended it, or the authentication has.
func (s *SCSCF) putChallenge(ch Challenge) error {
	ch.Expires = time.Now().Add(s.cfg.RegAwaitAuth)
	ch.KeepUntil = ch.Expires.Add(s.cfg.RegAwaitAuth)
	if err := s.store.PutChallenge(context.Background(), ch); err != nil {
		return err
	}
	s.authTimers.start(key(ch.PrivateIdentity), s.cfg.RegAwaitAuth, func() { s.regAwaitAuthExpired(ch.PrivateIdentity, ch.Nonce) })
	return nil
}

// endChallenge removes the challenge of privateID and stops its
// reg-await-auth timer. The timer stops only once the challenge is
// gone, so that a challenge that the store failed to remove still ends
// with its timer.
func (s *SCSCF) endChallenge(ctx context.Context, privateID string) error {
	if err := s.store.DeleteChallenge(ctx, privateID); err != nil {
		return err
	}
	s.authTimers.stop(key(privateID))
	return nil
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
	assignment := cx.AssignRegistration
	if registered {
		assignment = cx.ReRegistration
	}
	saa, err := s.serverAssignment(ch.PrivateIdentity, ch.PublicIdentity, ch.HSSHost, assignment)
	if err != nil || !saa.Result.Success() {
		log.Warn("SAR failed", "result", describe(saa.Result, err))
		s.respond(st, 500, nil)
		return
	}
	reg := Registration{
		PrivateIdentity: ch.PrivateIdentity,
		HSSHost:         ch.HSSHost,
		Expires:         time.Now().Add(expires),
		Path:            req.GetAll("Path"),
	}
	if profile, err := cx.ParseUserProfile(saa.UserData); err == nil {
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
	if contact, err := sip.ParseNameAddr(req.Get("Contact")); err == nil {
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
	s.regTimers.start(key(reg.PrivateIdentity), expires, func() { s.registrationExpired(reg.PrivateIdentity) })
	log.Info("registered", "contact", reg.Contact, "expires", int(expires/time.Second), "assignment", assignment.String())

	s.respond(st, 200, func(r *sip.Message) {
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
	})
	s.notifyRegistration(ctx, reg, replaced, log)
	s.notifyApplicationServers(req, reg, expires, log)
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
	if err := s.store.DeleteRegistration(ctx, privateID); err != nil {
		s.storeFailed(st, err, log)
		return
	}
	s.regTimers.stop(key(privateID))
	log.Info("deregistered")
	// TS 23.228 5.3.1 steps 7 to 9: the HSS has answered the SAR before
	// the S-CSCF answers 200.
	s.serverAssignment(privateID, reg.PublicIdentities[0], reg.HSSHost, cx.UserDeregistration)
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
// SIMPLIFIED: TS 24.229 5.4.1.5. Only tests call it: the S-CSCF has no
// operator interface. The HSS asks for a deregistration
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
	_, err = s.serverAssignment(privateID, reg.PublicIdentities[0], reg.HSSHost, cx.AdministrativeDeregistration)
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
	s.regTimers.stop(key(reg.PrivateIdentity))
	log.Info("deregistered by the network", "event", string(event))
	s.endRegistration(ctx, reg, event, log)
	s.notifyApplicationServers(s.equivalentRegister(reg), reg, 0, log)
	return nil
}

// Handle is the diameter.Handler of the S-CSCF. The only Cx request
// that the HSS sends it is the Registration-Termination-Request (TS
// 29.228 6.1.3).
func (s *SCSCF) Handle(request *diam.Message) *diam.Message {
	identity := s.node.Identity()
	if request.Header.ApplicationID != diameter.ApplicationIDCx || request.Header.CommandCode != cx.CommandRegistrationTermination {
		return diameter.CommandUnsupportedAnswer(identity, request)
	}
	rtr, err := cx.ParseRegistrationTerminationRequest(request)
	if err != nil {
		s.log.Warn("cannot read RTR", "error", err.Error())
		return cx.NewAnswer(request, identity, cx.Base(cx.DiameterUnableToComply))
	}
	return s.registrationTermination(rtr).Message(request, identity)
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
// assigns no second S-CSCF while one serves the user, and it takes no
// user who is not registered away from the S-CSCF, which lets the
// profile of such a user go by itself (UnregisteredProfileLifetime).
func (s *SCSCF) registrationTermination(r cx.RegistrationTerminationRequest) cx.RegistrationTerminationAnswer {
	event, ok := deregistrationEvents[r.DeregistrationReason.ReasonCode]
	if !ok {
		s.log.Info("RTR with a reason not handled", "private_identity", r.UserName, "reason", r.DeregistrationReason.ReasonCode.String())
		return cx.RegistrationTerminationAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
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
		return cx.RegistrationTerminationAnswer{Result: cx.Experimental(cx.DiameterErrorUserUnknown)}
	}
	// TS 29.228 6.1.3.1: the S-CSCF removes all it has stored for the
	// user, a registration under way included.
	if err := s.endChallenge(ctx, r.UserName); err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	if err := s.deregisterByNetwork(ctx, reg, event); err != nil {
		return s.registrationTerminationFailed(r, err)
	}
	return cx.RegistrationTerminationAnswer{Result: cx.Base(cx.DiameterSuccess)}
}

// registrationTerminationFailed answers an RTR whose user the S-CSCF
// cannot read or deregister. It can tell the HSS neither that it does
// not know the user nor that it has deregistered the user, so it
// answers DIAMETER_UNABLE_TO_COMPLY (RFC 6733 7.1.5). The registration
// stays as it was.
func (s *SCSCF) registrationTerminationFailed(r cx.RegistrationTerminationRequest, err error) cx.RegistrationTerminationAnswer {
	s.log.Warn("RTR not served: the store failed", "private_identity", r.UserName, "error", err.Error())
	return cx.RegistrationTerminationAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
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

func (s *SCSCF) regAwaitAuthExpired(privateID, nonce string) {
	ctx := context.Background()
	log := s.log.With("private_identity", privateID)
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		log.Warn("reg-await-auth not handled: the lock of the user was not taken; the HSS is not told, and the challenge goes at its KeepUntil", "error", err.Error())
		return
	}
	defer unlock()
	ch, ok, err := s.store.Challenge(ctx, privateID)
	if err != nil {
		log.Warn("reg-await-auth not handled: the store failed; the HSS is not told, and the challenge goes at its KeepUntil", "error", err.Error())
		return
	}
	if !ok || ch.Nonce != nonce {
		return
	}
	// TS 24.229 5.4.1.2.3 NOTE 2: authentication has failed; a user who
	// was registered stays registered.
	log.Info("reg-await-auth expired")
	if err := s.store.DeleteChallenge(ctx, privateID); err != nil {
		log.Warn("reg-await-auth not handled: the store failed; the HSS is not told, and the challenge goes at its KeepUntil", "error", err.Error())
		return
	}
	s.serverAssignment(privateID, ch.PublicIdentity, ch.HSSHost, cx.AuthenticationTimeout)
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
func (s *SCSCF) registrationExpired(privateID string) {
	ctx := context.Background()
	log := s.log.With("private_identity", privateID)
	unlock, err := s.users.Lock(ctx, privateID)
	if err != nil {
		log.Warn("expiry of the registration not handled: the lock of the user was not taken; it stays until the UE registers again or the HSS ends it", "error", err.Error())
		return
	}
	defer unlock()
	reg, ok, err := s.store.Registration(ctx, privateID)
	if err != nil {
		log.Warn("expiry of the registration not handled: the store failed; it stays until the UE registers again or the HSS ends it", "error", err.Error())
		return
	}
	// Under the lock, the registration may have ended or been refreshed
	// since its expiry came up.
	if !ok || time.Now().Before(reg.Expires) {
		return
	}
	log.Info("registration expired")
	if err := s.store.DeleteRegistration(ctx, privateID); err != nil {
		log.Warn("expiry of the registration not handled: the store failed; it stays until the UE registers again or the HSS ends it", "error", err.Error())
		return
	}
	s.endRegistration(ctx, reg, reginfo.Expired, log)
	s.serverAssignment(privateID, reg.PublicIdentities[0], reg.HSSHost, cx.TimeoutDeregistration)
}

func (s *SCSCF) serverAssignment(privateID, publicID, hssHost string, t cx.ServerAssignmentType) (cx.ServerAssignmentAnswer, error) {
	answer, err := s.request(cx.ServerAssignmentRequest{
		Header:               cx.NewHeader(s.node, hssHost, s.cfg.HomeDomain),
		UserName:             privateID,
		PublicIdentities:     []string{publicID},
		ServerName:           s.cfg.ServerName,
		ServerAssignmentType: t,
	}.Message())
	if err != nil {
		s.log.Warn("SAR failed", "private_identity", privateID, "type", t.String(), "error", err.Error())
		return cx.ServerAssignmentAnswer{}, err
	}
	return cx.ParseServerAssignmentAnswer(answer)
}

func (s *SCSCF) request(m *diam.Message) (*diam.Message, error) {
	return s.hss.Request(context.Background(), m)
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

func describe(r cx.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return r.String()
}
