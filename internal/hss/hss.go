// Package hss is the HSS stub. It answers the Cx requests of the
// I-CSCF and the S-CSCF from the subscribers in its Store, which the
// configuration seeds, following the HSS behaviour of TS
// 29.228, the Sh requests of application servers, following TS
// 29.328, and the S6c requests of the SMS central functions, following
// TS 29.338. Cx, Sh and S6c are the boundary where a full HSS can
// replace it. Subscriptions are added and removed through an HTTP
// provisioning API, which, unlike Cx, is not a 3GPP interface
// but this project's own, as each product HSS has its own.
package hss

import (
	"cmp"
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/sgd"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// Config is what the HSS stub knows about the network and its users.
type Config struct {
	// Realm is the home network domain, used as the SIP Digest realm.
	Realm string
	// VisitedNetworks are the Visited-Network-Identifier values whose
	// users may register (the roaming check of TS 29.228 6.1.1.1).
	VisitedNetworks []string
	// ServerCapabilities go into a UAA for a first registration, so that
	// the I-CSCF can pick an S-CSCF (TS 29.228 6.7).
	ServerCapabilities cx.ServerCapabilities
	// Subscriptions are the subscribers the Store starts with.
	Subscriptions []Subscription
	// PSIUsers are the ranges of public service identities that the
	// application servers of the network host (TS 23.228 5.4.12.4).
	PSIUsers []PSIUser
	// ServiceProfiles are the service profiles of the network by name
	// (TS 29.228 B.2.2). The name stays in the HSS, as the user profile
	// of Cx has no element for it (TS 29.228 Annex E, Table E.2).
	ServiceProfiles map[string]ServiceProfile
	// DefaultServiceProfile is the name of the service profile that a
	// subscription gets when it names none of its own and is not marked
	// as a test user, those added through the provisioning API
	// included. Operators give a feature to every user by naming here
	// the profile whose criteria call the application server of that
	// feature. The maintenance API changes it while the HSS runs
	// (Settings), so the name in force is HSS.DefaultServiceProfile.
	DefaultServiceProfile string
	// TestUserServiceProfile is the name of the service profile that a
	// subscription marked Subscription.TestUser gets when it names none
	// of its own. Operators try a feature out on the test users alone
	// by naming here the profile whose criteria call the application
	// server of that feature, and leaving DefaultServiceProfile as it
	// is. The maintenance API changes it too, so the name in force is
	// HSS.TestUserServiceProfile.
	TestUserServiceProfile string
	// Admission is which subscribers the network admits when the HSS
	// starts. The empty Admission is AdmitAll. The maintenance API
	// changes it while the HSS runs (Settings), so the policy in force
	// is HSS.Admission and not this field.
	Admission Admission
	// ProvisioningListen is the address of the provisioning API, such as
	// :8081. Empty means the node has none.
	ProvisioningListen string
	// MMTelServices is the first MMTelServices document (TS 29.364 7.2)
	// of a subscription that the provisioning API adds without one, as
	// MMTelServicesData builds it. Empty gives such a subscription no
	// supplementary services.
	MMTelServices *sh.TransparentData
	// ServiceCentres are the SMS central functions the HSS can alert,
	// one entry per Service Centre address that appears in the message
	// waiting data of a user. Without an entry for an address the HSS
	// cannot alert it, and the SC is left with its own retries.
	ServiceCentres []ServiceCentre
	// UserLocks serialises the requests that change one subscriber. Nil
	// means a lock per user in the memory of this process
	// (ims.UserLocks), which serves an HSS that runs as one instance.
	// Instances that share a store take the lock of a subscriber in it
	// (internal/kvstore/kvlock), so that only one of them changes the
	// subscriber at a time.
	UserLocks ims.UserLocker
	// Metrics is where the HSS registers its gauges, the database usage
	// measurements (metrics.go). Nil gives it a registry of its own,
	// which nothing serves.
	Metrics *metrics.Registry
}

// Admission is the operator's policy on which subscribers the network
// admits. It decides whether a subscriber is authorized to register
// (TS 29.228 6.1.1.1 step 5), and the HSS reads it from the subscriber
// it already has in hand, so the policy costs nothing per subscriber
// and needs no walk over them.
//
// Changing the policy, or taking the test user mark off a subscriber
// while it is AdmitTestUsers, leaves the registrations that are valid
// then as they are: the HSS sends no Registration-Termination-Request
// and refuses the requests that arrive from that moment on. The S-CSCF
// holds the expiry, so each of those users drops out when its
// registration runs out.
type Admission string

const (
	// AdmitAll admits every subscriber the HSS has.
	AdmitAll Admission = "all"
	// AdmitTestUsers admits only the subscribers marked
	// Subscription.TestUser, which is how an operator keeps a network to
	// the users it tries features out on.
	AdmitTestUsers Admission = "test_users"
)

// Admissions are the policies in the order a configuration lists them.
var Admissions = []Admission{AdmitAll, AdmitTestUsers}

// ParseAdmission reads the admission policy of a configuration. The
// empty string is AdmitAll, so a network that names no policy admits
// every subscriber.
func ParseAdmission(s string) (Admission, error) {
	if s == "" {
		return AdmitAll, nil
	}
	if !slices.Contains(Admissions, Admission(s)) {
		return "", fmt.Errorf("admission: %q is not one of %v", s, Admissions)
	}
	return Admission(s), nil
}

// admits reports whether the admission policy in force lets u register
// and take an S-CSCF of its own.
func (h *HSS) admits(u Subscriber) bool { return h.settings.Load().admits(u) }

// admits reports whether the admission policy of s lets u register and
// take an S-CSCF of its own.
func (s *settings) admits(u Subscriber) bool {
	return s.admission != AdmitTestUsers || u.TestUser
}

// settings are the parts of the configuration that an operator changes
// while the HSS runs, through the maintenance API (Settings): which
// subscribers the network admits, and the service profiles of the
// subscriptions that name none of their own.
//
// One value holds all three, and a change replaces it whole, the
// settings of one maintenance request together (Settings), so a request
// reads them all from before that change or all from after it. A
// request that decides more than once on them, such as the SAR of a
// user who is not registered, which asks whether the policy admits the
// user and then which profile the user gets, loads them once and
// decides each time on that one value.
type settings struct {
	admission              Admission
	defaultServiceProfile  string
	testUserServiceProfile string
}

// Admission is the admission policy in force.
func (h *HSS) Admission() Admission { return h.settings.Load().admission }

// DefaultServiceProfile is the name of the service profile in force for
// the subscriptions that name none of their own.
func (h *HSS) DefaultServiceProfile() string { return h.settings.Load().defaultServiceProfile }

// TestUserServiceProfile is the name of the service profile in force
// for the test users that name none of their own.
func (h *HSS) TestUserServiceProfile() string { return h.settings.Load().testUserServiceProfile }

// changeSettings puts in force the settings that change makes of the
// ones in force. The requests read the settings without a lock, so what
// they see is the value before this change or the value after it; they
// are never held up by one, and no request path takes a lock of the
// whole role.
func (h *HSS) changeSettings(change func(*settings)) {
	h.beginSettings()
	change(h.pending)
	h.commitSettings()
}

// beginSettings starts a change of the settings: the ones in force are
// copied to pending, which the change writes to. settingsMu is held from
// here to commitSettings, so that two changes cannot each write back
// what it read.
//
// Every beginSettings is answered by a commitSettings, so the lock is
// always let go again: the maintenance API calls the begin of a group
// of settings before the first of them is set and its commit after the
// last, for one change of the configuration at a time, and the sets in
// between only write fields of pending, so none of them can fail or
// panic (maintenance.Together, maintenance.ChangeConfiguration).
func (h *HSS) beginSettings() {
	h.settingsMu.Lock()
	next := *h.settings.Load()
	h.pending = &next
}

// commitSettings puts pending in force, all of it at once, and then
// pushes the profiles that the change gives another name
// (pushChangedSettings).
func (h *HSS) commitSettings() {
	before, after := h.settings.Load(), h.pending
	h.settings.Store(after)
	h.pending = nil
	h.settingsMu.Unlock()
	// Outside settingsMu: a push reads the settings in force, which are
	// these, and sends to one subscriber after another.
	h.pushChangedSettings(before, after)
}

// ServiceCentre is how the HSS reaches one of the SMS central
// functions over S6c. TS 29.338 5.3.1.2 has the Destination-Host and
// Destination-Realm of an alert name the SMS-SC that the message
// waiting data holds, which is why the HSS needs the Diameter identity
// of each Service Centre address it may store.
type ServiceCentre struct {
	// Address is the E.164 number of the SC, digits only, as it arrives
	// in the SC-Address of an SRR or an RDR.
	Address string
	// Hosts are the Diameter identities of the instances of the
	// SMS-IWMSC that take the alert for that SC, in the order the HSS
	// tries them, and Realm is their realm. The instances share the
	// short messages that the SC holds, so any one of them that takes
	// the alert will do.
	Hosts []string
	Realm string
}

// HSS answers Cx requests from the subscribers in its Store.
type HSS struct {
	config Config
	store  Store
	node   *diameter.Node
	log    *slog.Logger

	// users serializes the requests that change one subscriber, so that
	// a MAR and a SAR for the same user never overwrite each other, and
	// the requests of other users are not held up.
	users ims.UserLocker
	// serviceCentres are the ServiceCentres of the configuration by
	// their Address, so that an alert finds one without a walk.
	serviceCentres map[string]ServiceCentre
	// peers reaches the nodes that connected to the HSS. SetPeers gives
	// it the server once the node listens, which is after New.
	peers atomic.Pointer[Peers]
	// alerts counts the alerts to the service centres that are on their
	// way, for Close.
	alerts sync.WaitGroup
	// life is the context of the work that the HSS does of its own
	// accord, outside any request: the pushes of a changed setting
	// (pushChangedSettings). Close ends it and waits for that work to
	// stop, so that no push outlives the node.
	life    context.Context
	endLife context.CancelFunc
	// lifeMu is held over the end of life in Close and over the start of
	// a push (pushChangedSettings), so that no push starts once Close
	// waits for them.
	lifeMu sync.Mutex
	// pushes counts the pushes of a changed setting that are on their
	// way, for Close, as alerts counts the alerts.
	pushes sync.WaitGroup

	// settings are the parts of the configuration that the maintenance
	// API changes while the HSS runs (Settings). A request loads the
	// whole set once; settingsMu is held only to put a change in force,
	// never on the path of a request, and it guards pending as well.
	// The maintenance API carries out one change of the configuration at
	// a time under a lock of its own, so this one is not what holds two
	// changes from that API apart; it holds this value and pending
	// together for one change, wherever the change comes from.
	settings   atomic.Pointer[settings]
	settingsMu sync.Mutex
	// pending is the settings a change is writing, between beginSettings
	// and commitSettings, under settingsMu. No request reads it.
	pending *settings

	// requests counts the Cx and Sh requests this instance is
	// answering, the users of the maintenance API (maintenance.go). It
	// counts, rather than holding the requests, so that a shut down
	// never walks over anything the HSS keeps.
	requests atomic.Int64
	// measured counts the requests that reach the HSS and the answers it
	// hands back, the measurements of TS 32.409 4.2.12 (Handle).
	measured *diameter.Answering
	// managed is the administrative state of the HSS, or nil in a node
	// without a maintenance API.
	managed atomic.Pointer[maintenance.ManagedFunction]
}

// New returns an HSS on store. It fails when config.Metrics holds the
// database usage measurements of another HSS already, when it holds the
// Diameter measurements under another definition, or when the store
// cannot be read.
func New(config Config, store Store, node *diameter.Node, log *slog.Logger) (*HSS, error) {
	if config.UserLocks == nil {
		config.UserLocks = &ims.UserLocks{}
	}
	if config.Metrics == nil {
		config.Metrics = metrics.NewRegistry()
	}
	measured, err := diameter.NewAnswering(config.Metrics, role)
	if err != nil {
		return nil, err
	}
	h := &HSS{config: config, store: store, node: node, log: log, users: config.UserLocks,
		serviceCentres: map[string]ServiceCentre{}, measured: measured}
	h.life, h.endLife = context.WithCancel(context.Background())
	// A configuration that names no policy admits every subscriber, and
	// the policy in force is written out, so that what the maintenance
	// API answers with is a policy an operator can send back.
	h.settings.Store(&settings{
		admission:              cmp.Or(config.Admission, AdmitAll),
		defaultServiceProfile:  config.DefaultServiceProfile,
		testUserServiceProfile: config.TestUserServiceProfile,
	})
	for _, c := range config.ServiceCentres {
		h.serviceCentres[c.Address] = c
	}
	// A store that keeps none of the numbers of TS 32.409 4.2.1 gets no
	// gauge at all: a gauge without labels stands in the output from
	// the moment it is registered, so registering it would publish a
	// zero for a store that holds subscribers (metrics.go).
	_, kept, err := store.Counts(context.Background())
	if err != nil {
		return nil, err
	}
	if kept {
		if err := h.registerDatabaseUsage(config.Metrics); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// SetPeers gives the HSS the nodes that connected to it, over which it
// alerts a service centre. Without them it sends no alert.
func (h *HSS) SetPeers(p Peers) { h.peers.Store(&p) }

// Close waits for the alerts that are on their way to the service
// centres, and ends the pushes of a changed setting and waits for them:
// one that is on its way sends no more profiles, and the S-CSCFs it did
// not reach read the profile at the next registration or from the push
// of a later change. A node calls it once its Diameter server has
// stopped, when a push would reach nobody anyway.
func (h *HSS) Close() {
	h.lifeMu.Lock()
	h.endLife()
	h.lifeMu.Unlock()
	h.alerts.Wait()
	h.pushes.Wait()
}

// Handle is the diameter.Handler of the HSS. It counts the request on
// its receipt and the answer as it hands it back, which is where TS
// 32.409 4.2.12.1 and 4.2.12.2 count the requests an HSS receives and
// the answers it sends. Every request the HSS answers passes here,
// whichever of its interfaces carried it, and that is what those two
// measurements count: 4.2.12.1 names the Update-Location-Request of
// S6a as one example among the requests of all of them.
func (h *HSS) Handle(request *diam.Message) *diam.Message {
	// The request counts as a user of the HSS until its answer is on
	// its way, so that a shut down waits for it (maintenance.go). The
	// count comes down only in the follow-up the node runs then.
	h.requests.Add(1)
	h.node.AfterAnswer(request, h.requestAnswered)
	h.measured.RequestReceived(request)
	answer := h.answer(request)
	h.measured.AnswerSent(request, answer)
	return answer
}

// answer is the answer that Handle gives request.
func (h *HSS) answer(request *diam.Message) *diam.Message {
	identity := h.node.Identity()
	// diameter.Handler carries no context to bound the work for the
	// request. The Store bounds each of its operations itself: an
	// adapter on a store outside the process gives each one a time
	// limit.
	ctx := context.Background()
	if request.Header.ApplicationID == diameter.ApplicationIDSh {
		return h.handleSh(ctx, request)
	}
	if request.Header.ApplicationID == sgd.ApplicationIDS6c {
		return h.handleS6c(ctx, request)
	}
	if request.Header.ApplicationID != diameter.ApplicationIDCx {
		return diameter.CommandUnsupportedAnswer(identity, request)
	}
	switch request.Header.CommandCode {
	case cx.CommandUserAuthorization:
		uar, err := cx.ParseUserAuthorizationRequest(request)
		if err != nil {
			return h.unableToComply(request, err)
		}
		return h.userAuthorization(ctx, uar).Message(request, identity)
	case cx.CommandMultimediaAuth:
		mar, err := cx.ParseMultimediaAuthRequest(request)
		if err != nil {
			return h.unableToComply(request, err)
		}
		return h.multimediaAuth(ctx, mar).Message(request, identity)
	case cx.CommandLocationInfo:
		lir, err := cx.ParseLocationInfoRequest(request)
		if err != nil {
			return h.unableToComply(request, err)
		}
		return h.locationInfo(ctx, lir).Message(request, identity)
	case cx.CommandServerAssignment:
		sar, err := cx.ParseServerAssignmentRequest(request)
		if err != nil {
			return h.unableToComply(request, err)
		}
		return h.serverAssignment(ctx, sar).Message(request, identity)
	}
	return diameter.CommandUnsupportedAnswer(identity, request)
}

func (h *HSS) unableToComply(request *diam.Message, err error) *diam.Message {
	h.log.Warn("cannot read request", "error", err.Error())
	return cx.NewAnswer(request, h.node.Identity(), diameter.Base(diameter.DiameterUnableToComply))
}

// storeFailed is the result for a request whose subscriber the HSS
// cannot read or write. The HSS cannot tell then whether the user
// exists, or what the request has changed, so it answers neither
// DIAMETER_ERROR_USER_UNKNOWN nor a success (RFC 6733 7.1.5).
func (h *HSS) storeFailed(err error) diameter.Result {
	h.log.Warn("subscriber store failed", "error", err.Error())
	return diameter.Base(diameter.DiameterUnableToComply)
}

// lockWait is how long a request waits for the lock of its subscriber.
// The Diameter client waits 5 s for the answer (the application-defined
// Timeout of RFC 6733 5.6.2, internal/config) and then gives the
// request up: an HSS that took the lock after that would change the
// subscriber for a request that its sender reports as failed. The lease
// of a lock that a stopped instance left (ims.UserLockLease) is longer,
// so such a request gets DIAMETER_UNABLE_TO_COMPLY instead.
const lockWait = 4 * time.Second

// lockSubscriber takes the lock of the subscriber privateID for one
// request, waiting at most lockWait.
func (h *HSS) lockSubscriber(privateID string) (unlock func(), err error) {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	return h.users.Lock(ctx, privateID)
}

// lockFailed is the result for a request whose subscriber the HSS could
// not lock. Another instance may be changing the subscriber, so this
// one does not go on without the lock.
func (h *HSS) lockFailed(err error) diameter.Result {
	h.log.Warn("the lock of the subscriber was not taken", "error", err.Error())
	return diameter.Base(diameter.DiameterUnableToComply)
}

// find runs the identity checks that start the UAR, MAR and SAR
// procedures: both identities exist, then they belong together. An
// empty privateID skips the second check.
func (h *HSS) find(ctx context.Context, privateID, publicID string) (Subscriber, diameter.Result, bool) {
	unknown := diameter.Experimental(cx.DiameterErrorUserUnknown)
	var byPublic, byPrivate Subscriber
	if publicID != "" {
		s, ok, err := h.store.SubscriberOf(ctx, publicID)
		if err != nil {
			return Subscriber{}, h.storeFailed(err), false
		}
		if !ok {
			return Subscriber{}, unknown, false
		}
		byPublic = s
	}
	if privateID != "" {
		s, ok, err := h.store.Subscriber(ctx, privateID)
		if err != nil {
			return Subscriber{}, h.storeFailed(err), false
		}
		if !ok {
			return Subscriber{}, unknown, false
		}
		byPrivate = s
	}
	switch {
	case publicID == "" && privateID == "":
		return Subscriber{}, unknown, false
	case publicID == "":
		return byPrivate, diameter.Result{}, true
	case privateID != "" && privateKey(byPublic.PrivateIdentity) != privateKey(byPrivate.PrivateIdentity):
		return Subscriber{}, diameter.Experimental(cx.DiameterErrorIdentitiesDontMatch), false
	}
	return byPublic, diameter.Result{}, true
}

// lock is find under the lock of the subscriber it finds, for the
// requests that change the subscriber. The subscriber is read again once
// the lock is held, because another request may have changed it, or a
// provisioning request removed it, in between.
func (h *HSS) lock(ctx context.Context, privateID, publicID string) (Subscriber, diameter.Result, func(), bool) {
	s, result, ok := h.find(ctx, privateID, publicID)
	if !ok {
		return Subscriber{}, result, nil, false
	}
	unlock, err := h.lockSubscriber(s.PrivateIdentity)
	if err != nil {
		return Subscriber{}, h.lockFailed(err), nil, false
	}
	s, result, ok = h.find(ctx, privateID, publicID)
	if !ok {
		unlock()
		return Subscriber{}, result, nil, false
	}
	return s, result, unlock, true
}

// userAuthorization is the HSS side of the user registration status
// query, TS 29.228 6.1.1.1.
func (h *HSS) userAuthorization(ctx context.Context, r cx.UserAuthorizationRequest) cx.UserAuthorizationAnswer {
	u, result, ok := h.find(ctx, r.UserName, r.PublicIdentity)
	if !ok {
		return cx.UserAuthorizationAnswer{Result: result}
	}
	// Step 4: a barred identity may register only to bring in the
	// identities of its implicit registration set that are not barred,
	// as the temporary public identity of a UE without an ISIM does
	// (TS 23.228 E.3.1).
	if u.Barred(r.PublicIdentity) && !slices.ContainsFunc(u.PublicIdentities, func(id string) bool { return !u.Barred(id) }) {
		return cx.UserAuthorizationAnswer{Result: diameter.Base(diameter.DiameterAuthorizationRejected)}
	}
	// Step 5: REGISTRATION, a request without a User-Authorization-Type
	// and REGISTRATION_AND_CAPABILITIES all ask that the identity be
	// allowed to roam in the visited network and be authorized to
	// register. A first registration and a refresh take this same
	// branch, so one check refuses both. DE_REGISTRATION asks for
	// neither: refusing it would leave the user unable to end a
	// registration that the S-CSCF and the P-CSCF still hold.
	if r.UserAuthorizationType != cx.DeRegistration {
		if !slices.Contains(h.config.VisitedNetworks, r.VisitedNetworkIdentifier) {
			return cx.UserAuthorizationAnswer{Result: diameter.Experimental(cx.DiameterErrorRoamingNotAllowed)}
		}
		if !h.admits(u) {
			return cx.UserAuthorizationAnswer{Result: diameter.Base(diameter.DiameterAuthorizationRejected)}
		}
	}
	if r.UserAuthorizationType == cx.RegistrationAndCapabilities {
		caps := h.config.ServerCapabilities
		return cx.UserAuthorizationAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerCapabilities: &caps}
	}

	deregistration := r.UserAuthorizationType == cx.DeRegistration
	switch {
	case u.State == Registered || u.State == Unregistered:
		if deregistration {
			return cx.UserAuthorizationAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerName: u.ServerName}
		}
		return cx.UserAuthorizationAnswer{Result: diameter.Experimental(cx.DiameterSubsequentRegistration), ServerName: u.ServerName}
	case deregistration:
		if u.AuthenticationPending {
			return cx.UserAuthorizationAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerName: u.ServerName}
		}
		return cx.UserAuthorizationAnswer{Result: diameter.Experimental(cx.DiameterErrorIdentityNotRegistered)}
	case u.ServerName != "":
		// Not registered, but an S-CSCF was stored by a MAR whose
		// authentication is still pending: send the user there again.
		return cx.UserAuthorizationAnswer{Result: diameter.Experimental(cx.DiameterSubsequentRegistration), ServerName: u.ServerName}
	}
	caps := h.config.ServerCapabilities
	return cx.UserAuthorizationAnswer{Result: diameter.Experimental(cx.DiameterFirstRegistration), ServerCapabilities: &caps}
}

// locationInfo is the HSS side of the user location query, TS 29.228
// 6.1.4.1, for public user identities.
//
// SIMPLIFIED: TS 29.228 6.1.4.1 steps 2, 2a and 3. The query finds
// public user identities only: one for a PSI gets
// DIAMETER_ERROR_USER_UNKNOWN, as the HSS keeps no S-CSCF of a PSI user
// (psiServerAssignment). IMS restoration is not part of this stub, and
// the Originating-Request AVP is not read: no I-CSCF of this project
// routes an originating request (TS 24.229 5.3.2.1A).
func (h *HSS) locationInfo(ctx context.Context, r cx.LocationInfoRequest) cx.LocationInfoAnswer {
	u, result, ok := h.find(ctx, "", r.PublicIdentity)
	if !ok {
		return cx.LocationInfoAnswer{Result: result}
	}
	// Step 3. A subscription of the stub is one implicit registration
	// set with one state (see Subscription), so the S-CSCF of the user
	// is the one of any identity of the subscription.
	switch {
	case (u.State == Registered || u.State == Unregistered) && u.ServerName != "":
		return cx.LocationInfoAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerName: u.ServerName}
	case !ifc.HasSessionCase(h.userProfile(u).InitialFilterCriteria, ifc.TerminatingUnregistered):
		return cx.LocationInfoAnswer{Result: diameter.Experimental(cx.DiameterErrorIdentityNotRegistered)}
	case u.ServerName != "":
		// Not registered, but an S-CSCF is stored, by a MAR whose
		// authentication is still pending.
		return cx.LocationInfoAnswer{Result: diameter.Base(diameter.DiameterSuccess), ServerName: u.ServerName}
	}
	// The same capabilities as for a first registration, so that the
	// I-CSCF picks an S-CSCF for the services of the unregistered state.
	caps := h.config.ServerCapabilities
	return cx.LocationInfoAnswer{Result: diameter.Experimental(cx.DiameterUnregisteredService), ServerCapabilities: &caps}
}

// multimediaAuth is the HSS side of the authentication procedure,
// TS 29.228 6.3.1, for SIP Digest and IMS-AKA.
func (h *HSS) multimediaAuth(ctx context.Context, r cx.MultimediaAuthRequest) cx.MultimediaAuthAnswer {
	u, result, unlock, ok := h.lock(ctx, r.UserName, r.PublicIdentity)
	if !ok {
		return cx.MultimediaAuthAnswer{Result: result}
	}
	defer unlock()
	scheme, ok := authenticationScheme(u.Subscription, r.SIPAuthenticationScheme)
	if !ok {
		return cx.MultimediaAuthAnswer{Result: diameter.Experimental(cx.DiameterErrorAuthSchemeNotSupported)}
	}
	answer := cx.MultimediaAuthAnswer{
		Result:                  diameter.Base(diameter.DiameterSuccess),
		UserName:                u.PrivateIdentity,
		PublicIdentity:          r.PublicIdentity,
		SIPAuthenticationScheme: scheme,
	}
	unableToComply := cx.MultimediaAuthAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}

	if resync := r.SIPAuthorization; resync != nil {
		// Step 4: a synchronization failure is answered only for IMS-AKA
		// (Table 6.3.2) and only to the S-CSCF that the HSS stored. Any
		// other case is an exception, answered with
		// DIAMETER_UNABLE_TO_COMPLY (the end of 6.3.1).
		if scheme != cx.SchemeDigestAKAv1MD5 || r.ServerName != u.ServerName {
			return unableToComply
		}
		av, ok, err := h.authenticationVector(ctx, u, resync)
		if err != nil {
			return cx.MultimediaAuthAnswer{Result: h.storeFailed(err)}
		}
		if !ok {
			return unableToComply
		}
		answer.AuthenticationVector = &av
		return answer
	}

	switch scheme {
	case cx.SchemeSIPDigest:
		answer.SIPDigestAuthenticate = &cx.SIPDigestAuthenticate{
			DigestRealm:     h.config.Realm,
			DigestAlgorithm: "MD5",
			DigestQoP:       "auth",
			DigestHA1:       HA1(u.PrivateIdentity, h.config.Realm, u.Password),
		}
	case cx.SchemeDigestAKAv1MD5:
		av, ok, err := h.authenticationVector(ctx, u, nil)
		if err != nil {
			return cx.MultimediaAuthAnswer{Result: h.storeFailed(err)}
		}
		if !ok {
			return unableToComply
		}
		answer.AuthenticationVector = &av
	}
	// Step 5.
	//
	// SIMPLIFIED: TS 29.228 8.1.1. When the MAR of another S-CSCF takes
	// over a registered user, the HSS sends the old S-CSCF no
	// Registration-Termination-Request. The I-CSCF of this project gives
	// every user the same S-CSCF and picks no other when that one does
	// not answer (TS 24.229 5.3.1.3), so no user moves to another S-CSCF
	// in its network. The old S-CSCF ends the registration when it
	// expires, which 8.1.1 also provides for, and its SAR then changes
	// nothing (8.1.2).
	if u.State != Registered || u.ServerName != r.ServerName {
		if err := h.setServer(ctx, u, r.ServerName, origin(r.Header), u.State, true); err != nil {
			return cx.MultimediaAuthAnswer{Result: h.storeFailed(err)}
		}
	}
	return answer
}

// authenticationScheme is the check of TS 29.228 6.3.1 step 4: the
// scheme to answer with, if the subscription has what it needs.
// "Unknown" asks for the scheme stored in the HSS, which may only be SIP
// Digest (or NASS-Bundled, which this stub does not have).
func authenticationScheme(s Subscription, requested string) (string, bool) {
	switch requested {
	case cx.SchemeUnknown, cx.SchemeSIPDigest:
		return cx.SchemeSIPDigest, s.Password != ""
	case cx.SchemeDigestAKAv1MD5:
		return cx.SchemeDigestAKAv1MD5, s.AKA != nil
	}
	return "", false
}

// authenticationVector is the AuC part of an IMS-AKA MAR: it takes the
// next SQN_HE (TS 33.102 6.3.2), after re-synchronising with the AUTS
// of resync if there is one (6.3.5), and builds a vector with a fresh
// RAND. It reports false if the subscription is gone, and returns the
// failure of the store. The lock of u is held.
//
// The HSS sends one vector whatever SIP-Number-Auth-Items asks for: TS
// 29.228 6.3.1 lets it send fewer, and the S-CSCF uses one vector per
// registration (TS 33.203 6.1.1).
func (h *HSS) authenticationVector(ctx context.Context, u Subscriber, resync *cx.Resynchronization) (aka.AuthenticationVector, bool, error) {
	log := h.log.With("private_identity", u.PrivateIdentity)
	for {
		sqnHE := u.AKA.SQN
		base := sqnHE
		if resync != nil {
			var err error
			base, err = aka.Resynchronise(u.AKA.Secret, sqnHE, resync.RAND, resync.AUTS)
			if err != nil {
				// 6.3.5 step 6: a new vector still goes out, from the
				// SQN_HE that the HSS has.
				log.Warn("AUTS fails verification", "error", err.Error())
			} else if base != sqnHE {
				log.Info("SQN re-synchronised", "sqn_he", uint64(sqnHE), "sqn_ms", uint64(base))
			}
		}
		next := base.Next()
		swapped, err := h.store.CompareAndSwapSQN(ctx, u.PrivateIdentity, sqnHE, next)
		if err != nil {
			return aka.AuthenticationVector{}, false, err
		}
		if swapped {
			var rand aka.RAND
			cryptorand.Read(rand[:])
			return aka.NewAuthenticationVector(u.AKA.Secret, rand, next, u.AKA.AMF), true, nil
		}
		// Another HSS instance used SQN_HE first: start again from the
		// one it stored.
		var ok bool
		u, ok, err = h.store.Subscriber(ctx, u.PrivateIdentity)
		if err != nil || !ok || u.AKA == nil {
			return aka.AuthenticationVector{}, false, err
		}
	}
}

// HA1 is H(A1) of RFC 2617 3.2.2.2 for the MD5 algorithm, in hex.
func HA1(username, realm, password string) string {
	sum := md5.Sum([]byte(username + ":" + realm + ":" + password))
	return hex.EncodeToString(sum[:])
}

// serverAssignment is the HSS side of the S-CSCF registration and
// deregistration notification, TS 29.228 6.1.2.1.
func (h *HSS) serverAssignment(ctx context.Context, r cx.ServerAssignmentRequest) cx.ServerAssignmentAnswer {
	publicID := ""
	if len(r.PublicIdentities) > 0 {
		publicID = r.PublicIdentities[0]
	}
	// One load of the settings for the whole request, so that the policy
	// that admits the user and the profile the answer carries come from
	// the same change of them.
	set := h.settings.Load()
	u, result, unlock, ok := h.lock(ctx, r.UserName, publicID)
	if !ok {
		if p, psi := h.psiUserOf(r.UserName, publicID); psi && result == diameter.Experimental(cx.DiameterErrorUserUnknown) {
			return psiServerAssignment(p, publicID, r)
		}
		return cx.ServerAssignmentAnswer{Result: result}
	}
	defer unlock()
	// Step 3.
	if tooManyIdentities(r) {
		return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterAVPOccursTooManyTimes)}
	}
	// 8.1.2: the HSS holds another S-CSCF for the user: one that has the
	// user registered, whose MAR may have taken the user over from this
	// one (8.1.1); one whose MAR is pending for a user who is not
	// registered; or one that serves the user in the unregistered state.
	// A registration that this S-CSCF ends, or an authentication that
	// fails at it, is not the one the HSS holds, so no
	// Server-Assignment-Type changes anything here. The answer names the
	// S-CSCF that the HSS holds (Table 6.1.2.2), so that an S-CSCF with
	// no registration of the user can send its request there (TS 24.229
	// 5.4.3.3). Only the S-CSCF reassignment pending flag of IMS
	// restoration lets the name be overwritten, and this HSS has no IMS
	// restoration.
	if u.ServerName != "" && u.ServerName != r.ServerName {
		result := diameter.Experimental(cx.DiameterErrorIdentityAlreadyRegistered)
		if r.ServerAssignmentType == cx.NoAssignment {
			result = diameter.Base(diameter.DiameterUnableToComply)
		}
		return cx.ServerAssignmentAnswer{Result: result, ServerName: u.ServerName}
	}
	switch r.ServerAssignmentType {
	case cx.AssignRegistration, cx.ReRegistration:
		if err := h.setServer(ctx, u, r.ServerName, origin(r.Header), Registered, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return h.withUserData(set, u, r)
	case cx.UnregisteredUser:
		// A check that the procedure does not have: step 5 authorizes
		// nothing on this branch. Refusing the registration alone would
		// leave a subscriber the admission policy does not admit with the
		// services of the unregistered state, because the LIR of a
		// terminating request is answered whatever the policy (6.1.4.1
		// step 3) and the S-CSCF the I-CSCF then picks takes the profile
		// here. DIAMETER_AUTHORIZATION_REJECTED is a base-protocol
		// Result-Code (RFC 6733 7.1.5), so it fits this answer as well as
		// the UAA's. A subscriber the HSS does not hold as registered has
		// no valid registration, whether or not a server name is stored
		// for it, as one is for a user whose S-CSCF kept its name.
		if u.State != Registered && !set.admits(u) {
			return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterAuthorizationRejected)}
		}
		if err := h.setServer(ctx, u, r.ServerName, origin(r.Header), Unregistered, u.AuthenticationPending); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return h.withUserData(set, u, r)
	case cx.TimeoutDeregistration, cx.UserDeregistration, cx.AdministrativeDeregistration:
		if err := h.setServer(ctx, u, "", diameter.Identity{}, NotRegistered, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	case cx.AuthenticationFailure, cx.AuthenticationTimeout:
		name, scscf := u.ServerName, u.SCSCF
		if u.State == NotRegistered {
			name, scscf = "", diameter.Identity{}
		}
		if err := h.setServer(ctx, u, name, scscf, u.State, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	case cx.NoAssignment:
		if u.ServerName != r.ServerName {
			return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
		}
		return h.withUserData(set, u, r)
	}
	// SIMPLIFIED: TS 29.228 6.1.2.1. The *_STORE_SERVER_NAME and
	// DEREGISTRATION_TOO_MUCH_DATA assignment types are not used by this
	// project's S-CSCF.
	return cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
}

// tooManyIdentities is the check of TS 29.228 6.1.2.1 step 3: whether r
// names more than one Public-Identity for a Server-Assignment-Type that
// applies to one only. Table 6.1.2.1 lets only the deregistrations name
// several. The types listed are the others that the HSS serves; it
// answers every type that it does not serve with
// DIAMETER_UNABLE_TO_COMPLY.
func tooManyIdentities(r cx.ServerAssignmentRequest) bool {
	switch r.ServerAssignmentType {
	case cx.NoAssignment, cx.AssignRegistration, cx.ReRegistration, cx.UnregisteredUser, cx.AuthenticationFailure, cx.AuthenticationTimeout:
		return len(r.PublicIdentities) > 1
	}
	return false
}

func (h *HSS) withUserData(set *settings, u Subscriber, r cx.ServerAssignmentRequest) cx.ServerAssignmentAnswer {
	a := cx.ServerAssignmentAnswer{Result: diameter.Base(diameter.DiameterSuccess), UserName: u.PrivateIdentity}
	// TS 29.228 6.6: the HSS may skip the profile when the S-CSCF says it
	// already has it.
	if !r.UserDataAlreadyAvailable {
		a.UserData = h.userProfileIn(set, u).Marshal()
	}
	return a
}

// setServer stores the registration state of u, and returns the
// failure of the store. The lock of u is held.
func (h *HSS) setServer(ctx context.Context, u Subscriber, serverName string, scscf diameter.Identity, state RegistrationState, pending bool) error {
	from, changed := u.State, u.State != state || u.ServerName != serverName
	u.ServerName, u.SCSCF, u.State, u.AuthenticationPending = serverName, scscf, state, pending
	if err := h.store.Put(ctx, u); err != nil {
		return err
	}
	if changed {
		h.log.Info("registration state", "private_identity", u.PrivateIdentity,
			"from", from.String(), "to", state.String(), "server_name", serverName)
	}
	return nil
}

// origin is the Diameter identity of the node that sent a request.
func origin(h diameter.Header) diameter.Identity {
	return diameter.Identity{OriginHost: h.OriginHost, OriginRealm: h.OriginRealm}
}
