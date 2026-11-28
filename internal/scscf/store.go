package scscf

import (
	"context"
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
	// Algorithms are the ones of the WWW-Authenticate header fields that
	// the 401 carried, the most preferred first (TS 24.229 5.4.1.2.1A,
	// 5.4.1.2.1B): MD5 for SIP Digest, and for IMS AKA the AKA versions
	// of imsAKAAlgorithms. The UE answers with one of them.
	Algorithms []string
	HA1        string // from the MAA, for SIP Digest
	// RAND, XRES, IK and CK come from the authentication vector of IMS
	// AKA. RAND is kept for a re-synchronisation (TS 24.229 5.4.1.2.1A),
	// and the rest to compute the password of the response the challenge
	// expects (5.4.1.2.2 step 4): XRES alone for AKAv1-MD5, all three
	// for AKAv2-SHA-256.
	RAND aka.RAND
	XRES []byte
	IK   aka.IK
	CK   aka.CK
	// HSSHost is the Origin-Host of the MAA, the Destination-Host of the
	// SAR that follows (TS 29.228 6.1.2).
	HSSHost string
	// ServerName is the Server-Name of the MAR, under which the HSS holds
	// the user now. The Cx requests that go on with the challenge name
	// it, whichever instance of the S-CSCF sends them: the SAR that
	// follows, which the HSS takes under no other name (TS 29.228
	// 8.1.2), and the MAR of a re-synchronisation (6.3.1 step 4).
	ServerName string
	// Expires is when reg-await-auth runs out (TS 24.229 5.4.1.2.1):
	// the authentication fails then if no REGISTER has answered the
	// challenge.
	Expires time.Time
	// KeepUntil is when the Store forgets the challenge: reg-await-auth
	// after Expires (putChallenge).
	KeepUntil time.Time
}

// offers reports whether the 401 offered algorithm, so that a response
// is checked against the algorithm the UE chose and no other (TS 24.229
// 5.4.1.2.2 step 3, 5.4.1.2.2A step 3). Credentials without an
// algorithm parameter mean MD5 (RFC 3310 3.1).
func (c Challenge) offers(algorithm string) bool {
	if algorithm == "" {
		algorithm = "MD5"
	}
	return slices.ContainsFunc(c.Algorithms, func(a string) bool { return strings.EqualFold(a, algorithm) })
}

// imsAKA reports whether the challenge is one of IMS AKA rather than of
// SIP Digest: it offers an AKA version (TS 33.203 6.1.1).
func (c Challenge) imsAKA() bool {
	return c.offers(sip.AlgorithmAKAv2SHA256) || c.offers(sip.AlgorithmAKAv1MD5)
}

// digestHA1 is H(A1) of the response that the challenge expects for
// algorithm: the one of the MAA for SIP Digest, and for IMS AKA the one
// of the password that the AKA version derives from the vector, XRES for
// AKAv1-MD5 (RFC 3310 3.3) and XRES with IK and CK for AKAv2-SHA-256
// (RFC 4169 2.1). It is empty for an algorithm that the challenge did
// not offer, and so for a registration without a challenge.
func (c Challenge) digestHA1(algorithm string) string {
	switch {
	case !c.offers(algorithm):
		return ""
	case strings.EqualFold(algorithm, sip.AlgorithmAKAv1MD5):
		return sip.HA1(algorithm, c.PrivateIdentity, c.Realm, aka.DigestPassword(c.XRES))
	case strings.EqualFold(algorithm, sip.AlgorithmAKAv2SHA256):
		return sip.HA1(algorithm, c.PrivateIdentity, c.Realm, aka.AKAv2DigestPassword(c.XRES, c.IK, c.CK))
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
	// ServerName is the Server-Name under which the HSS assigned the
	// user: the one of the challenge that the registration answered,
	// which every re-registration keeps. Each Cx request about the
	// registration names it, whichever instance of the S-CSCF sends it:
	// the HSS takes a SAR under no other name (TS 29.228 8.1.2), and a
	// MAR under another name would take the user from this S-CSCF (8.1).
	ServerName string
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
// the profile that is not barred (TS 29.228 6.5.1.1). The S-CSCF takes
// no profile without one for a registration (cx.UserProfile.Registrable),
// so a registration always has one.
func (r Registration) DefaultIdentity() string { return r.NonBarredIdentities()[0] }

// sameBinding reports whether r and other hold the same binding as the
// same REGISTER of the UE left it: the contact id, and the Call-ID and
// the CSeq of that REGISTER. A re-registration of the same contact
// keeps the contact id (TS 24.229 5.4.2.1.2), so the id alone does not
// tell one registration of the contact from the next; the Call-ID and
// the CSeq do, because a REGISTER that updates the binding carries a
// CSeq higher than the stored one under the same Call-ID (RFC 3261 10.3
// step 7). The Store keeps all three, so an instance of the S-CSCF that
// did not handle the REGISTER compares them just as well.
func (r Registration) sameBinding(other Registration) bool {
	return r.ContactID == other.ContactID && r.CallID == other.CallID && r.CSeq == other.CSeq
}

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
	// ServerName is the Server-Name of the SAR that took the profile,
	// under which the HSS holds the user. The SAR that lets the user go
	// names it, whichever instance of the S-CSCF sends it (TS 29.228
	// 8.1.2).
	ServerName string
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

// ExpiryKind names one of the expiries that the S-CSCF has to act on
// when its time comes: each of them sends a SAR, a NOTIFY or both, so
// the item cannot simply go away by itself. The Store keeps the items of
// one kind in a time index of its own, named after the kind, and hands
// out each expiry that is due to one instance at a time (ClaimDue), so
// that the expiries of an instance that has stopped are handled by
// another one.
type ExpiryKind string

const (
	// RegAwaitAuthExpiry is reg-await-auth (TS 24.229 5.4.1.2.1): the
	// REGISTER that answers a challenge has not come. Its item is the
	// challenge, named by the private identity and the nonce.
	RegAwaitAuthExpiry ExpiryKind = "reg-await-auth"
	// RegistrationExpiry is the end of a registration that was not
	// refreshed (TS 24.229 5.4.1.2.2A step 10). Its item is the
	// registration, named by the private identity.
	RegistrationExpiry ExpiryKind = "registration-expiry"
	// SubscriptionExpiry is the end of a subscription to the reg event
	// package that was not refreshed (RFC 6665 4.2.1.4). Its item is the
	// subscription, named by its dialog.
	SubscriptionExpiry ExpiryKind = "subscription-expiry"
	// UnregisteredProfileExpiry is the end of the profile of a callee who
	// is not registered (UnregisteredProfileLifetime). Its item is the
	// profile, named by the private identity.
	UnregisteredProfileExpiry ExpiryKind = "unregistered-profile-expiry"
)

// ExpiryKinds are all the kinds, which one sweep goes through.
var ExpiryKinds = []ExpiryKind{RegAwaitAuthExpiry, RegistrationExpiry, SubscriptionExpiry, UnregisteredProfileExpiry}

// Expiry is one expiry that an instance has claimed: which item is due,
// and until when this instance alone acts on it.
type Expiry struct {
	Kind ExpiryKind
	// PrivateIdentity names the item of every kind but
	// SubscriptionExpiry, in the form in which the Store compares one.
	PrivateIdentity string
	// Nonce is the nonce of the challenge, for RegAwaitAuthExpiry. The
	// expiry of a challenge that the UE answered must end no later
	// challenge of the same user, so the nonce is part of the name.
	Nonce string
	// Dialog names the subscription, for SubscriptionExpiry.
	Dialog sip.DialogID
	// Until is when the claim runs out, after which another instance may
	// claim the same item. It also tells this claim from a later one on
	// the same item, so that the late Done of an instance that was held
	// up takes nothing away from the instance that has the item now.
	Until time.Time
}

var _ ims.Expiry = Expiry{}

func (e Expiry) ClaimedUntil() time.Time { return e.Until }

func (e Expiry) LogFields() []any {
	return []any{"kind", string(e.Kind), "private_identity", e.PrivateIdentity, "call_id", e.Dialog.CallID}
}

// Store keeps the registrar state of the S-CSCF. It is an interface so
// that the state can live outside the node; MemoryStore keeps
// it in the process.
//
// An error is a failure of the store, after which the caller cannot
// tell what the store holds. The S-CSCF writes every expiry into what
// it stores, as a time.
//
// A SIP transaction, a Cx request and a claimed expiry carry no context
// of their own, so the S-CSCF passes context.Background() and relies on
// the time limit that an adapter on a store outside the process gives
// each operation.
type Store interface {
	Challenge(ctx context.Context, privateID string) (Challenge, bool, error)
	// PutChallenge keeps c, in place of the challenge of its user,
	// until its KeepUntil.
	PutChallenge(ctx context.Context, c Challenge) error
	DeleteChallenge(ctx context.Context, privateID string) error
	// Registration returns the registration of a private identity,
	// whether or not it has expired: the instance that claims its
	// RegistrationExpiry reads it to find whom to tell.
	Registration(ctx context.Context, privateID string) (Registration, bool, error)
	// RegistrationOf finds, until its Expires, the registration that
	// holds a public identity, in the canonical form of
	// ims.CanonicalURI. A registration whose Expires has passed is no
	// binding any more, although no instance may have handled its expiry
	// yet, so a request is not routed to its contact.
	//
	// SIMPLIFIED: TS 23.228 4.3.3.4 and 4.2.7.2. A public identity has
	// at most one registration, of one private identity with one
	// contact, so a request to it goes to that contact alone: the
	// S-CSCF never forks. The HSS shares no public identity between
	// private identities, and without reg-id the S-CSCF keeps only the
	// newest contact of a private identity (TS 24.229 5.4.1.2.2A step
	// 6).
	RegistrationOf(ctx context.Context, publicID string) (Registration, bool, error)
	PutRegistration(ctx context.Context, r Registration) error
	DeleteRegistration(ctx context.Context, privateID string) error
	Subscription(ctx context.Context, id sip.DialogID) (Subscription, bool, error)
	// SubscriptionsOf returns the subscriptions to the registration of a
	// private identity.
	SubscriptionsOf(ctx context.Context, privateID string) ([]Subscription, error)
	PutSubscription(ctx context.Context, sub Subscription) error
	DeleteSubscription(ctx context.Context, id sip.DialogID) error
	// OriginalDialog returns what PutOriginalDialog stored for an
	// identifier, until its Expires.
	OriginalDialog(ctx context.Context, id string) (OriginalDialog, bool, error)
	PutOriginalDialog(ctx context.Context, d OriginalDialog) error
	// UnregisteredProfile returns the profile of a private identity,
	// whether or not it has expired: the instance that claims an
	// UnregisteredProfileExpiry reads it to find what to tell the HSS.
	UnregisteredProfile(ctx context.Context, privateID string) (UnregisteredProfile, bool, error)
	// UnregisteredProfileOf finds, until its Expires, the profile that
	// holds a public identity in the canonical form of ims.CanonicalURI.
	// It is a lookup by key, never a walk over all profiles.
	UnregisteredProfileOf(ctx context.Context, publicID string) (UnregisteredProfile, bool, error)
	PutUnregisteredProfile(ctx context.Context, p UnregisteredProfile) error
	// DeleteUnregisteredProfile removes the profile of privateID if it
	// still expires at expires, and reports whether it did, so that the
	// instance which handles an expiry never removes a profile that came
	// after it. Of several instances that end the same profile, the one
	// that removes it is the one that tells the HSS.
	DeleteUnregisteredProfile(ctx context.Context, privateID string, expires time.Time) (bool, error)

	// ClaimDue claims the expiries of kind that are due at now, at most
	// limit of them and the ones due longest first, each until now plus
	// lease. While an expiry is claimed it is not due, and no other
	// ClaimDue gives it out; when its claim runs out without Done, it is
	// due again, for whichever instance asks next.
	//
	// A claim is no proof that the item has expired: it may have been
	// refreshed after it was due, or removed, since the Store does not
	// take the item of a removed registration or profile out of the index
	// (DeleteRegistration). The caller therefore reads the item under the
	// lock of its user and checks its expiry there, as the handlers of
	// the S-CSCF do.
	ClaimDue(ctx context.Context, kind ExpiryKind, now time.Time, lease time.Duration, limit int) ([]Expiry, error)
	// Done takes the item of a claimed expiry out of its index, once the
	// caller has handled it. A claim that has ended in the meantime, by a
	// write that rescheduled the item, by its removal, or because it ran
	// out and another instance has it now, leaves the index as it is;
	// that is the ordinary end of an expiry, not a failure.
	Done(ctx context.Context, e Expiry) error
}

// MemoryStore is a Store in memory. Private identities compare without
// case. A lookup by another identifier reads an index, as the stores
// outside the node do, so that its time does not grow with the number
// of users.
type MemoryStore struct {
	mu            sync.Mutex
	challenges    map[string]Challenge
	registrations map[string]Registration
	// registrationOf is the private key of the registration of each
	// public identity, by ims.PublicIdentityKey. A public identity
	// belongs to one registration, as the HSS gives it to one
	// subscription (hss.ErrIdentityTaken).
	registrationOf map[string]string
	subscriptions  map[sip.DialogID]Subscription
	// subscriptionsOf holds the dialogs of the subscriptions to the
	// registration of each private key.
	subscriptionsOf map[string]map[sip.DialogID]struct{}
	originalDialogs map[string]OriginalDialog
	// unregistered holds the profiles by private identity, and
	// unregisteredOf the private identity of each of their public ones.
	unregistered   map[string]UnregisteredProfile
	unregisteredOf map[string]string
	// due holds the time index of each kind of expiry, as a store
	// outside the process holds one.
	due map[ExpiryKind]*ims.DueIndex[dueItem]
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{challenges: map[string]Challenge{}, registrations: map[string]Registration{},
		registrationOf: map[string]string{}, subscriptions: map[sip.DialogID]Subscription{},
		subscriptionsOf: map[string]map[sip.DialogID]struct{}{}, originalDialogs: map[string]OriginalDialog{},
		unregistered: map[string]UnregisteredProfile{}, unregisteredOf: map[string]string{},
		due: map[ExpiryKind]*ims.DueIndex[dueItem]{}}
}

func key(privateID string) string { return strings.ToLower(privateID) }

// Challenge finds a challenge until its KeepUntil, as an external store
// would with a time to live. The instance that handles its reg-await-auth
// removes it before then, so it takes no room after that.
func (s *MemoryStore) Challenge(_ context.Context, privateID string) (Challenge, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.challenges[key(privateID)]
	if !ok || !c.KeepUntil.IsZero() && !time.Now().Before(c.KeepUntil) {
		return Challenge{}, false, nil
	}
	return c, true, nil
}

// PutChallenge stores c and schedules its reg-await-auth at its Expires.
// The expiry of the challenge that c replaces goes with it, so that a
// pending challenge has one expiry and no other.
func (s *MemoryStore) PutChallenge(_ context.Context, c Challenge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(c.PrivateIdentity)
	if old, ok := s.challenges[k]; ok && old.Nonce != c.Nonce {
		s.unschedule(challengeItem(c.PrivateIdentity, old.Nonce))
	}
	c.XRES = slices.Clone(c.XRES)
	s.challenges[k] = c
	s.schedule(challengeItem(c.PrivateIdentity, c.Nonce), c.Expires)
	return nil
}

// DeleteChallenge removes the challenge and its reg-await-auth. The
// expiry names the nonce, so a later challenge of the user keeps its
// own.
func (s *MemoryStore) DeleteChallenge(_ context.Context, privateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(privateID)
	if c, ok := s.challenges[k]; ok {
		s.unschedule(challengeItem(privateID, c.Nonce))
	}
	delete(s.challenges, k)
	return nil
}

func (s *MemoryStore) Registration(_ context.Context, privateID string) (Registration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.registrations[key(privateID)]
	return r, ok, nil
}

func (s *MemoryStore) RegistrationOf(_ context.Context, publicID string) (Registration, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, ok := s.registrationOf[ims.PublicIdentityKey(publicID)]
	if !ok {
		return Registration{}, false, nil
	}
	r, ok := s.registrations[k]
	if !ok || !time.Now().Before(r.Expires) {
		return Registration{}, false, nil
	}
	return r, true, nil
}

func (s *MemoryStore) PutRegistration(_ context.Context, r Registration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.PublicIdentities = append([]string(nil), r.PublicIdentities...)
	r.BarredIdentities = slices.Clone(r.BarredIdentities)
	r.Path = append([]string(nil), r.Path...)
	r.ContactParams = slices.Clone(r.ContactParams)
	// The criteria are never changed in place, so a shallow copy keeps
	// them apart.
	r.InitialFilterCriteria = slices.Clone(r.InitialFilterCriteria)
	k := key(r.PrivateIdentity)
	s.removeRegistration(k)
	s.registrations[k] = r
	for _, id := range r.PublicIdentities {
		s.registrationOf[ims.PublicIdentityKey(id)] = k
	}
	s.schedule(userItem(RegistrationExpiry, r.PrivateIdentity), r.Expires)
	return nil
}

// DeleteRegistration removes the registration and leaves its expiry: the
// item is named by the private identity alone, and a registration that
// another instance writes at the same moment would lose its own expiry
// with it. The instance that claims the expiry finds no registration, or
// one that has not expired, and does nothing.
func (s *MemoryStore) DeleteRegistration(_ context.Context, privateID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeRegistration(key(privateID))
	return nil
}

// removeRegistration removes the registration of the private key k and
// the index entries that name it, and leaves its expiry (ClaimDue). The
// lock is held.
func (s *MemoryStore) removeRegistration(k string) {
	r, ok := s.registrations[k]
	if !ok {
		return
	}
	for _, id := range r.PublicIdentities {
		if pk := ims.PublicIdentityKey(id); s.registrationOf[pk] == k {
			delete(s.registrationOf, pk)
		}
	}
	delete(s.registrations, k)
}

func (s *MemoryStore) Subscription(_ context.Context, id sip.DialogID) (Subscription, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub, ok := s.subscriptions[id]
	return copySubscription(sub), ok, nil
}

func (s *MemoryStore) SubscriptionsOf(_ context.Context, privateID string) ([]Subscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var subs []Subscription
	for id := range s.subscriptionsOf[key(privateID)] {
		subs = append(subs, copySubscription(s.subscriptions[id]))
	}
	return subs, nil
}

func (s *MemoryStore) PutSubscription(_ context.Context, sub Subscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := sub.Dialog.ID
	s.unindexSubscription(id)
	s.subscriptions[id] = copySubscription(sub)
	k := key(sub.PrivateIdentity)
	if s.subscriptionsOf[k] == nil {
		s.subscriptionsOf[k] = map[sip.DialogID]struct{}{}
	}
	s.subscriptionsOf[k][id] = struct{}{}
	s.schedule(subscriptionItem(id), sub.Expires)
	return nil
}

// DeleteSubscription removes the subscription and its expiry. A dialog
// is never used again, so the expiry can go with it.
func (s *MemoryStore) DeleteSubscription(_ context.Context, id sip.DialogID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unindexSubscription(id)
	delete(s.subscriptions, id)
	s.unschedule(subscriptionItem(id))
	return nil
}

// unindexSubscription takes the subscription of the dialog id out of
// the index of its registration. The lock is held.
func (s *MemoryStore) unindexSubscription(id sip.DialogID) {
	sub, ok := s.subscriptions[id]
	if !ok {
		return
	}
	k := key(sub.PrivateIdentity)
	delete(s.subscriptionsOf[k], id)
	if len(s.subscriptionsOf[k]) == 0 {
		delete(s.subscriptionsOf, k)
	}
}

// copySubscription keeps the route set of the store apart from the
// caller's.
func copySubscription(sub Subscription) Subscription {
	sub.Dialog.RouteSet = slices.Clone(sub.Dialog.RouteSet)
	return sub
}

func (s *MemoryStore) OriginalDialog(_ context.Context, id string) (OriginalDialog, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.originalDialogs[id]
	if !ok || !time.Now().Before(d.Expires) {
		return OriginalDialog{}, false, nil
	}
	return d, true, nil
}

// PutOriginalDialog stores d. The store removes it at its Expires, as
// an external store would with a time to live.
func (s *MemoryStore) PutOriginalDialog(_ context.Context, d OriginalDialog) error {
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
	return nil
}

// UnregisteredProfile returns the profile of a private identity, expired
// or not, for the instance that handles its expiry.
func (s *MemoryStore) UnregisteredProfile(_ context.Context, privateID string) (UnregisteredProfile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.unregistered[key(privateID)]
	return p, ok, nil
}

func (s *MemoryStore) UnregisteredProfileOf(_ context.Context, publicID string) (UnregisteredProfile, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.unregistered[s.unregisteredOf[ims.PublicIdentityKey(publicID)]]
	if !ok || !time.Now().Before(p.Expires) {
		return UnregisteredProfile{}, false, nil
	}
	return p, true, nil
}

func (s *MemoryStore) PutUnregisteredProfile(_ context.Context, p UnregisteredProfile) error {
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
	s.schedule(userItem(UnregisteredProfileExpiry, p.PrivateIdentity), p.Expires)
	return nil
}

// DeleteUnregisteredProfile removes the profile if it still expires at
// expires. Like DeleteRegistration it leaves the expiry, whose item is
// the private identity alone.
func (s *MemoryStore) DeleteUnregisteredProfile(_ context.Context, privateID string, expires time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(privateID)
	if p, ok := s.unregistered[k]; !ok || !p.Expires.Equal(expires) {
		return false, nil
	}
	s.removeUnregistered(k)
	return true, nil
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

func (s *MemoryStore) ClaimDue(_ context.Context, kind ExpiryKind, now time.Time, lease time.Duration, limit int) ([]Expiry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ix, ok := s.due[kind]
	if !ok {
		return nil, nil
	}
	var claimed []Expiry
	for _, due := range ix.ClaimDue(now, lease, limit) {
		claimed = append(claimed, due.Member.expiry(due.Until))
	}
	return claimed, nil
}

func (s *MemoryStore) Done(_ context.Context, e Expiry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ix, ok := s.due[e.Kind]; ok {
		ix.RemoveClaimed(itemOf(e), e.Until)
	}
	return nil
}

// schedule puts item in the index of its kind, due at at, and ends any
// claim on it. The lock is held.
func (s *MemoryStore) schedule(item dueItem, at time.Time) {
	ix, ok := s.due[item.kind]
	if !ok {
		ix = ims.NewDueIndex[dueItem]()
		s.due[item.kind] = ix
	}
	ix.Set(item, at)
}

// unschedule takes item out of the index of its kind, claimed or not.
// The lock is held.
func (s *MemoryStore) unschedule(item dueItem) {
	if ix, ok := s.due[item.kind]; ok {
		ix.Remove(item)
	}
}

// dueItem names the item of one expiry, apart from the claim on it: what
// the member of a time index names in a store outside the process.
type dueItem struct {
	kind   ExpiryKind
	user   string
	nonce  string
	dialog sip.DialogID
}

func userItem(kind ExpiryKind, privateID string) dueItem {
	return dueItem{kind: kind, user: key(privateID)}
}

func challengeItem(privateID, nonce string) dueItem {
	return dueItem{kind: RegAwaitAuthExpiry, user: key(privateID), nonce: nonce}
}

func subscriptionItem(id sip.DialogID) dueItem {
	return dueItem{kind: SubscriptionExpiry, dialog: id}
}

func itemOf(e Expiry) dueItem {
	return dueItem{kind: e.Kind, user: key(e.PrivateIdentity), nonce: e.Nonce, dialog: e.Dialog}
}

func (i dueItem) expiry(until time.Time) Expiry {
	return Expiry{Kind: i.kind, PrivateIdentity: i.user, Nonce: i.nonce, Dialog: i.dialog, Until: until}
}
