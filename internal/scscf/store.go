package scscf

import (
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/reginfo"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Challenge is an authentication in progress: the S-CSCF sent a 401 and
// waits for the REGISTER that answers it (TS 24.229 5.4.1.2.1).
type Challenge struct {
	PrivateIdentity string
	PublicIdentity  string
	// CallID is the Call-ID of the challenged REGISTER. The answering
	// REGISTER must use the same one (TS 24.229 5.4.1.2.2A).
	CallID string
	Realm  string
	Nonce  string
	// Algorithm is the one of the WWW-Authenticate: MD5 for SIP Digest,
	// AKAv1-MD5 for IMS AKA.
	Algorithm string
	HA1       string // from the MAA, for SIP Digest
	// RAND and XRES come from the authentication vector of IMS AKA. RAND
	// is kept for a re-synchronisation (TS 24.229 5.4.1.2.1A), XRES to
	// check the response (5.4.1.2.2 step 4).
	RAND aka.RAND
	XRES []byte
	// HSSHost is the Origin-Host of the MAA, the Destination-Host of the
	// SAR that follows (TS 29.228 6.1.2).
	HSSHost string
	// Expires is when reg-await-auth runs out (TS 24.229 5.4.1.2.1):
	// the authentication fails then if no REGISTER has answered the
	// challenge.
	Expires time.Time
	// KeepUntil is when the Store forgets the challenge: reg-await-auth
	// after Expires (putChallenge).
	KeepUntil time.Time
}

// digestHA1 is H(A1) of the response that the challenge expects: the one
// of the MAA for SIP Digest, and for IMS AKA the one with XRES as the
// password (RFC 3310 3.3). It is empty for a registration without a
// challenge.
func (c Challenge) digestHA1() string {
	if c.Algorithm == sip.AlgorithmAKAv1MD5 {
		return sip.HA1(c.PrivateIdentity, c.Realm, aka.DigestPassword(c.XRES))
	}
	return c.HA1
}

// Registration is one registered user: the bindings of the registrar
// (RFC 3261 10.3) and what TS 24.229 5.4.1.2.2A asks the S-CSCF to keep.
type Registration struct {
	PrivateIdentity string
	// PublicIdentities are the implicit registration set, in the order
	// of the user profile.
	PublicIdentities []string
	// BarredIdentities are those of PublicIdentities that the user
	// profile bars (TS 29.228 B.2.1): they register, and do nothing
	// else. The contact is not bound to them (TS 24.229 5.4.1.2.2 NOTE
	// 9, 5.4.1.2.2A NOTE 11).
	BarredIdentities []string
	// Contact is the single contact of this private identity. Without
	// reg-id the S-CSCF keeps only the newest one (TS 24.229
	// 5.4.1.2.2A step 6).
	Contact string
	// ContactParams are the header field parameters of that Contact but
	// expires, such as the feature parameters of RFC 3840 9 that tell
	// what the UE can do (+g.3gpp.smsip). The 200 OK lists them with the
	// binding (TS 24.229 5.4.1.2.2F item f), the reg event reports them
	// (5.4.2.1.2), and caller preferences match against them (RFC 3841
	// 7.2.3).
	ContactParams sip.Params
	Expires       time.Time
	// Path is the Path header of the REGISTER, the route back to the UE
	// for terminating requests (RFC 3327).
	Path    []string
	HSSHost string
	// The contact as the reg event package reports it (RFC 3680 5.1, TS
	// 24.229 5.4.2.1.2). ContactID is new with each new binding, and
	// ContactEvent is the event that last changed it: registered or
	// refreshed. CallID and CSeq are those of the REGISTER behind that
	// event.
	ContactID    string
	ContactEvent reginfo.Event
	CallID       string
	CSeq         uint32
	// InitialFilterCriteria are those of the user profile that the SAA
	// brought (TS 29.228 6.1.2), in the order the HSS sent them. The
	// S-CSCF evaluates them for every initial request of the user
	// (TS 23.218 5.2.3) and for the third-party registration (TS 24.229
	// 5.4.1.7).
	InitialFilterCriteria []ifc.InitialFilterCriteria
}

// Barred reports whether id is one of the barred identities of the
// registration.
func (r Registration) Barred(id string) bool { return barred(r.BarredIdentities, id) }

// NonBarredIdentities are the identities of the registration that the
// user may use, the default public identity first: P-Associated-URI
// lists them (TS 24.229 5.4.1.2.2F item b) and the reg event reports
// them (5.4.2.1.2).
func (r Registration) NonBarredIdentities() []string {
	return nonBarred(r.PublicIdentities, r.BarredIdentities)
}

// DefaultIdentity is the default public identity: the first identity of
// the profile that is not barred (TS 29.228 6.5.1.1). The HSS bars no
// identity of a set without leaving one that is not barred (6.1.1.1
// step 4), so a registration always has one.
func (r Registration) DefaultIdentity() string { return r.NonBarredIdentities()[0] }

func barred(barredIDs []string, id string) bool {
	return slices.ContainsFunc(barredIDs, func(b string) bool { return ims.SamePublicIdentity(b, id) })
}

func nonBarred(ids, barredIDs []string) []string {
	var out []string
	for _, id := range ids {
		if !barred(barredIDs, id) {
			out = append(out, id)
		}
	}
	return out
}

// UnregisteredProfile is the user profile that the S-CSCF took from
// the HSS with a SAR of UNREGISTERED_USER, for a callee who has no
// registration here (TS 24.229 5.4.3.3 NOTE 27, TS 29.228 6.1.2.1).
// While the S-CSCF keeps it, the HSS has the user in the unregistered
// state with this S-CSCF assigned, and sends the user's calls here.
type UnregisteredProfile struct {
	// PrivateIdentity is the User-Name of the SAA. The SAR had none.
	PrivateIdentity  string
	PublicIdentities []string
	// BarredIdentities are those of PublicIdentities that the user
	// profile bars, as in Registration.
	BarredIdentities []string
	// InitialFilterCriteria are those of the profile, in the order the
	// HSS sent them.
	InitialFilterCriteria []ifc.InitialFilterCriteria
	// HSSHost is the Origin-Host of the SAA, the Destination-Host of the
	// SAR that lets the user go.
	HSSHost string
	// Expires is when the S-CSCF lets the profile go.
	Expires time.Time
}

// Subscription is a subscription to the reg event package of one user
// (TS 24.229 5.4.2.1.1), from the UE itself or from its P-CSCF.
type Subscription struct {
	sip.NotifierSubscription
	// PrivateIdentity is the applicable private user identity of step 4:
	// the registration whose state the subscription reports.
	PrivateIdentity string
	// ICID is the icid-value of the SUBSCRIBE (step 1B), which every
	// NOTIFY repeats (5.4.2.1.2 step 5).
	ICID string
	// Version is the version of the next reginfo document (RFC 3680
	// 5.1).
	Version uint32
}

// OriginalDialog is what the S-CSCF keeps for one original dialog
// identifier (TS 24.229 5.4.3.4): what it needs to go on with the
// filter criteria when a request comes back from the application server
// with the identifier in its Route, on whichever S-CSCF node that is.
// There is one for each request sent to a server.
type OriginalDialog struct {
	// ID is the identifier. It is random, so that it is unique.
	ID string
	// ServedUser is the public identity whose criteria apply: the
	// asserted caller (5.4.3.2 step 1) or the callee of the Request-URI
	// (5.4.3.3 step 1), in the form of ims.CanonicalURI.
	ServedUser string
	// SessionCase tells the originating case from the terminating one
	// (5.4.3.1). The Route of a request that comes back does not say.
	SessionCase ifc.SessionCase
	// RegState is whether the served user was registered here when the
	// request first came: sip.RegStateRegistered or
	// sip.RegStateUnregistered. P-Served-User carries it to the
	// application server (5.4.3.2 step 5 b, 5.4.3.3 step 4 c).
	RegState string
	// Criteria is the ordered list of 5.4.3.2 step 3 or 5.4.3.3 step 2A,
	// as it was when the request first came: a change of the profile does
	// not affect a request under way (5.4.3.2 NOTE 12).
	Criteria []ifc.InitialFilterCriteria
	// Invoked is the index in Criteria of the criterion whose server got
	// the request with this identifier. The evaluation goes on after it.
	Invoked int
	// RequestURI is the Request-URI that the terminating case saved
	// (5.4.3.3 step 1), so that a request that a server retargeted can
	// be told (step 3).
	RequestURI string
	// Aliases are the served user's public identities, in the form of
	// ims.CanonicalURI. A Request-URI that names one of them is still the
	// served user (step 3 c): every identity of a subscription of the
	// HSS stub is an alias of the others (TS 29.228 B.2.1).
	Aliases []string
	// ICID is the icid-value of the request (5.4.3.2 step 6, 5.4.3.3
	// step 6).
	ICID string
	// Returned is set once the server has sent a request back with this
	// identifier. A 408 or 5xx from the server after that is the answer
	// of the session, not a failure of the server, so the default
	// handling no longer applies (5.4.3.2, 5.4.3.3).
	Returned bool
	// Expires is when the Store forgets the identifier.
	Expires time.Time
}

// Store keeps the registrar state of the S-CSCF. It is an interface so
// that the state can live outside the node; MemoryStore keeps
// it in the process.
type Store interface {
	Challenge(privateID string) (Challenge, bool)
	// PutChallenge keeps c, in place of the challenge of its user,
	// until its KeepUntil.
	PutChallenge(Challenge)
	DeleteChallenge(privateID string)
	Registration(privateID string) (Registration, bool)
	// RegistrationOf finds the registration that holds a public
	// identity, in the canonical form of ims.CanonicalURI.
	//
	// SIMPLIFIED: TS 23.228 4.3.3.4 and 4.2.7.2. A public identity has
	// at most one registration, of one private identity with one
	// contact, so a request to it goes to that contact alone: the
	// S-CSCF never forks. The HSS shares no public identity between
	// private identities, and without reg-id the S-CSCF keeps only the
	// newest contact of a private identity (TS 24.229 5.4.1.2.2A step
	// 6).
	RegistrationOf(publicID string) (Registration, bool)
	PutRegistration(Registration)
	DeleteRegistration(privateID string)
	Subscription(id sip.DialogID) (Subscription, bool)
	// SubscriptionsOf returns the subscriptions to the registration of a
	// private identity.
	SubscriptionsOf(privateID string) []Subscription
	PutSubscription(Subscription)
	DeleteSubscription(id sip.DialogID)
	// OriginalDialog returns what PutOriginalDialog stored for an
	// identifier, until its Expires.
	OriginalDialog(id string) (OriginalDialog, bool)
	PutOriginalDialog(OriginalDialog)
	// UnregisteredProfileOf finds, until its Expires, the profile that
	// holds a public identity in the canonical form of ims.CanonicalURI.
	// It is a lookup by key, never a walk over all profiles.
	UnregisteredProfileOf(publicID string) (UnregisteredProfile, bool)
	PutUnregisteredProfile(UnregisteredProfile)
	// DeleteUnregisteredProfile removes the profile of privateID if it
	// still expires at expires, and reports whether it did, so that a
	// timer never removes a profile that came after it.
	DeleteUnregisteredProfile(privateID string, expires time.Time) bool
}

// MemoryStore is a Store in memory. Private identities compare without
// case.
type MemoryStore struct {
	mu              sync.Mutex
	challenges      map[string]Challenge
	registrations   map[string]Registration
	subscriptions   map[sip.DialogID]Subscription
	originalDialogs map[string]OriginalDialog
	// unregistered holds the profiles by private identity, and
	// unregisteredOf the private identity of each of their public ones.
	unregistered   map[string]UnregisteredProfile
	unregisteredOf map[string]string
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{challenges: map[string]Challenge{}, registrations: map[string]Registration{},
		subscriptions: map[sip.DialogID]Subscription{}, originalDialogs: map[string]OriginalDialog{},
		unregistered: map[string]UnregisteredProfile{}, unregisteredOf: map[string]string{}}
}

func key(privateID string) string { return strings.ToLower(privateID) }

// Challenge finds a challenge until its KeepUntil, as an external store
// would with a time to live. The reg-await-auth timer removes it before
// then, so it takes no room after that.
func (s *MemoryStore) Challenge(privateID string) (Challenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.challenges[key(privateID)]
	if !ok || !c.KeepUntil.IsZero() && !time.Now().Before(c.KeepUntil) {
		return Challenge{}, false
	}
	return c, true
}

func (s *MemoryStore) PutChallenge(c Challenge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.XRES = slices.Clone(c.XRES)
	s.challenges[key(c.PrivateIdentity)] = c
}

func (s *MemoryStore) DeleteChallenge(privateID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.challenges, key(privateID))
}

func (s *MemoryStore) Registration(privateID string) (Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.registrations[key(privateID)]
	return r, ok
}

func (s *MemoryStore) RegistrationOf(publicID string) (Registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.registrations {
		for _, id := range r.PublicIdentities {
			if ims.SamePublicIdentity(id, publicID) {
				return r, true
			}
		}
	}
	return Registration{}, false
}

func (s *MemoryStore) PutRegistration(r Registration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.PublicIdentities = append([]string(nil), r.PublicIdentities...)
	r.BarredIdentities = slices.Clone(r.BarredIdentities)
	r.Path = append([]string(nil), r.Path...)
	r.ContactParams = slices.Clone(r.ContactParams)
	// The criteria are never changed in place, so a shallow copy keeps
	// them apart.
	r.InitialFilterCriteria = slices.Clone(r.InitialFilterCriteria)
	s.registrations[key(r.PrivateIdentity)] = r
}

func (s *MemoryStore) DeleteRegistration(privateID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.registrations, key(privateID))
}

func (s *MemoryStore) Subscription(id sip.DialogID) (Subscription, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subscriptions[id]
	return copySubscription(sub), ok
}

func (s *MemoryStore) SubscriptionsOf(privateID string) []Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	var subs []Subscription
	for _, sub := range s.subscriptions {
		if key(sub.PrivateIdentity) == key(privateID) {
			subs = append(subs, copySubscription(sub))
		}
	}
	return subs
}

func (s *MemoryStore) PutSubscription(sub Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriptions[sub.Dialog.ID] = copySubscription(sub)
}

func (s *MemoryStore) DeleteSubscription(id sip.DialogID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscriptions, id)
}

// copySubscription keeps the route set of the store apart from the
// caller's.
func copySubscription(sub Subscription) Subscription {
	sub.Dialog.RouteSet = slices.Clone(sub.Dialog.RouteSet)
	return sub
}

func (s *MemoryStore) OriginalDialog(id string) (OriginalDialog, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.originalDialogs[id]
	if !ok || !time.Now().Before(d.Expires) {
		return OriginalDialog{}, false
	}
	return d, true
}

// PutOriginalDialog stores d. The store removes it at its Expires, as
// an external store would with a time to live.
func (s *MemoryStore) PutOriginalDialog(d OriginalDialog) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, known := s.originalDialogs[d.ID]
	d.Criteria = slices.Clone(d.Criteria)
	d.Aliases = slices.Clone(d.Aliases)
	s.originalDialogs[d.ID] = d
	if !known {
		time.AfterFunc(time.Until(d.Expires), func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if stored, ok := s.originalDialogs[d.ID]; ok && !time.Now().Before(stored.Expires) {
				delete(s.originalDialogs, d.ID)
			}
		})
	}
}

func (s *MemoryStore) UnregisteredProfileOf(publicID string) (UnregisteredProfile, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.unregistered[s.unregisteredOf[ims.PublicIdentityKey(publicID)]]
	if !ok || !time.Now().Before(p.Expires) {
		return UnregisteredProfile{}, false
	}
	return p, true
}

func (s *MemoryStore) PutUnregisteredProfile(p UnregisteredProfile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(p.PrivateIdentity)
	s.removeUnregistered(k)
	p.PublicIdentities = slices.Clone(p.PublicIdentities)
	p.BarredIdentities = slices.Clone(p.BarredIdentities)
	p.InitialFilterCriteria = slices.Clone(p.InitialFilterCriteria)
	s.unregistered[k] = p
	for _, id := range p.PublicIdentities {
		s.unregisteredOf[ims.PublicIdentityKey(id)] = k
	}
}

func (s *MemoryStore) DeleteUnregisteredProfile(privateID string, expires time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(privateID)
	if p, ok := s.unregistered[k]; !ok || !p.Expires.Equal(expires) {
		return false
	}
	s.removeUnregistered(k)
	return true
}

// removeUnregistered removes the profile of the private key k and its
// index entries. The lock is held.
func (s *MemoryStore) removeUnregistered(k string) {
	p, ok := s.unregistered[k]
	if !ok {
		return
	}
	for _, id := range p.PublicIdentities {
		if pk := ims.PublicIdentityKey(id); s.unregisteredOf[pk] == k {
			delete(s.unregisteredOf, pk)
		}
	}
	delete(s.unregistered, k)
}
