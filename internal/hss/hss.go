// Package hss is the HSS stub. It answers the Cx requests of the
// I-CSCF and the S-CSCF from the subscribers in its Store, which the
// configuration seeds, following the HSS behaviour of TS
// 29.228, and the Sh requests of application servers, following TS
// 29.328. Cx and Sh are the boundary where a full HSS can
// replace it. Subscriptions are added and removed through an HTTP
// provisioning API, which, unlike Cx, is not a 3GPP interface
// but this project's own, as each product HSS has its own.
package hss

import (
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"encoding/hex"
	"log/slog"
	"slices"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/aka"
	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/ifc"
	"github.com/yuu-biz/ims-practice/internal/ims"
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
	// InitialFilterCriteria go into the profile of every subscription,
	// including those added through the provisioning API.
	// MSISDNPlaceholder in a ServiceInfo becomes the MSISDN of the
	// subscriber.
	InitialFilterCriteria []ifc.InitialFilterCriteria
	// ProvisioningListen is the address of the provisioning API, such as
	// :8081. Empty means the node has none.
	ProvisioningListen string
	// MMTelServices is the first MMTelServices document (TS 29.364 7.2)
	// of a subscription that the provisioning API adds without one, as
	// MMTelServicesData builds it. Empty gives such a subscription no
	// supplementary services.
	MMTelServices *sh.TransparentData
	// UserLocks serialises the requests that change one subscriber. Nil
	// means a lock per user in the memory of this process
	// (ims.UserLocks), which serves an HSS that runs as one instance.
	// Instances that share a store take the lock of a subscriber in it
	// (internal/kvstore/kvlock), so that only one of them changes the
	// subscriber at a time.
	UserLocks ims.UserLocker
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
}

func New(config Config, store Store, node *diameter.Node, log *slog.Logger) *HSS {
	if config.UserLocks == nil {
		config.UserLocks = &ims.UserLocks{}
	}
	return &HSS{config: config, store: store, node: node, log: log, users: config.UserLocks}
}

// Handle is the diameter.Handler of the HSS.
func (h *HSS) Handle(request *diam.Message) *diam.Message {
	identity := h.node.Identity()
	// diameter.Handler carries no context to bound the work for the
	// request. The Store bounds each of its operations itself: an
	// adapter on a store outside the process gives each one a time
	// limit.
	ctx := context.Background()
	if request.Header.ApplicationID == diameter.ApplicationIDSh {
		return h.handleSh(ctx, request)
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
	return cx.NewAnswer(request, h.node.Identity(), cx.Base(cx.DiameterUnableToComply))
}

// storeFailed is the result for a request whose subscriber the HSS
// cannot read or write. The HSS cannot tell then whether the user
// exists, or what the request has changed, so it answers neither
// DIAMETER_ERROR_USER_UNKNOWN nor a success (RFC 6733 7.1.5).
func (h *HSS) storeFailed(err error) cx.Result {
	h.log.Warn("subscriber store failed", "error", err.Error())
	return cx.Base(cx.DiameterUnableToComply)
}

// lockFailed is the result for a request whose subscriber the HSS could
// not lock. Another instance may be changing the subscriber, so this
// one does not go on without the lock.
func (h *HSS) lockFailed(err error) cx.Result {
	h.log.Warn("the lock of the subscriber was not taken", "error", err.Error())
	return cx.Base(cx.DiameterUnableToComply)
}

// find runs the identity checks that start the UAR, MAR and SAR
// procedures: both identities exist, then they belong together. An
// empty privateID skips the second check.
func (h *HSS) find(ctx context.Context, privateID, publicID string) (Subscriber, cx.Result, bool) {
	unknown := cx.Experimental(cx.DiameterErrorUserUnknown)
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
		return byPrivate, cx.Result{}, true
	case privateID != "" && privateKey(byPublic.PrivateIdentity) != privateKey(byPrivate.PrivateIdentity):
		return Subscriber{}, cx.Experimental(cx.DiameterErrorIdentitiesDontMatch), false
	}
	return byPublic, cx.Result{}, true
}

// lock is find under the lock of the subscriber it finds, for the
// requests that change the subscriber. The subscriber is read again once
// the lock is held, because another request may have changed it, or a
// provisioning request removed it, in between.
func (h *HSS) lock(ctx context.Context, privateID, publicID string) (Subscriber, cx.Result, func(), bool) {
	s, result, ok := h.find(ctx, privateID, publicID)
	if !ok {
		return Subscriber{}, result, nil, false
	}
	unlock, err := h.users.Lock(context.Background(), s.PrivateIdentity)
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
		return cx.UserAuthorizationAnswer{Result: cx.Base(cx.DiameterAuthorizationRejected)}
	}
	// SIMPLIFIED: TS 29.228 6.1.1.1 step 5. The HSS should check whether
	// the user may register at all, and answer
	// DIAMETER_AUTHORIZATION_REJECTED otherwise. Every user of this stub
	// may register, so only the roaming check is made.
	if r.UserAuthorizationType != cx.DeRegistration && !slices.Contains(h.config.VisitedNetworks, r.VisitedNetworkIdentifier) {
		return cx.UserAuthorizationAnswer{Result: cx.Experimental(cx.DiameterErrorRoamingNotAllowed)}
	}
	if r.UserAuthorizationType == cx.RegistrationAndCapabilities {
		caps := h.config.ServerCapabilities
		return cx.UserAuthorizationAnswer{Result: cx.Base(cx.DiameterSuccess), ServerCapabilities: &caps}
	}

	deregistration := r.UserAuthorizationType == cx.DeRegistration
	switch {
	case u.State == Registered || u.State == Unregistered:
		if deregistration {
			return cx.UserAuthorizationAnswer{Result: cx.Base(cx.DiameterSuccess), ServerName: u.ServerName}
		}
		return cx.UserAuthorizationAnswer{Result: cx.Experimental(cx.DiameterSubsequentRegistration), ServerName: u.ServerName}
	case deregistration:
		if u.AuthenticationPending {
			return cx.UserAuthorizationAnswer{Result: cx.Base(cx.DiameterSuccess), ServerName: u.ServerName}
		}
		return cx.UserAuthorizationAnswer{Result: cx.Experimental(cx.DiameterErrorIdentityNotRegistered)}
	case u.ServerName != "":
		// Not registered, but an S-CSCF was stored by a MAR whose
		// authentication is still pending: send the user there again.
		return cx.UserAuthorizationAnswer{Result: cx.Experimental(cx.DiameterSubsequentRegistration), ServerName: u.ServerName}
	}
	caps := h.config.ServerCapabilities
	return cx.UserAuthorizationAnswer{Result: cx.Experimental(cx.DiameterFirstRegistration), ServerCapabilities: &caps}
}

// locationInfo is the HSS side of the user location query, TS 29.228
// 6.1.4.1, for public user identities.
//
// SIMPLIFIED: TS 29.228 6.1.4.1 steps 2, 2a and 3. Public Service
// Identities and IMS restoration are not part of this stub, so a
// request is always about a public user identity, and the
// Originating-Request AVP is not read: no I-CSCF of this project routes
// an originating request (TS 24.229 5.3.2.1A).
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
		return cx.LocationInfoAnswer{Result: cx.Base(cx.DiameterSuccess), ServerName: u.ServerName}
	case !ifc.HasSessionCase(h.userProfile(u).InitialFilterCriteria, ifc.TerminatingUnregistered):
		return cx.LocationInfoAnswer{Result: cx.Experimental(cx.DiameterErrorIdentityNotRegistered)}
	case u.ServerName != "":
		// Not registered, but an S-CSCF is stored, by a MAR whose
		// authentication is still pending.
		return cx.LocationInfoAnswer{Result: cx.Base(cx.DiameterSuccess), ServerName: u.ServerName}
	}
	// The same capabilities as for a first registration, so that the
	// I-CSCF picks an S-CSCF for the services of the unregistered state.
	caps := h.config.ServerCapabilities
	return cx.LocationInfoAnswer{Result: cx.Experimental(cx.DiameterUnregisteredService), ServerCapabilities: &caps}
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
		return cx.MultimediaAuthAnswer{Result: cx.Experimental(cx.DiameterErrorAuthSchemeNotSupported)}
	}
	answer := cx.MultimediaAuthAnswer{
		Result:                  cx.Base(cx.DiameterSuccess),
		UserName:                u.PrivateIdentity,
		PublicIdentity:          r.PublicIdentity,
		SIPAuthenticationScheme: scheme,
	}
	unableToComply := cx.MultimediaAuthAnswer{Result: cx.Base(cx.DiameterUnableToComply)}

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
	u, result, unlock, ok := h.lock(ctx, r.UserName, publicID)
	if !ok {
		return cx.ServerAssignmentAnswer{Result: result}
	}
	defer unlock()
	switch r.ServerAssignmentType {
	case cx.AssignRegistration, cx.ReRegistration:
		if len(r.PublicIdentities) > 1 {
			return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterAVPOccursTooManyTimes)}
		}
		if u.State == Registered && u.ServerName != "" && u.ServerName != r.ServerName {
			return cx.ServerAssignmentAnswer{Result: cx.Experimental(cx.DiameterErrorIdentityAlreadyRegistered), ServerName: u.ServerName}
		}
		if err := h.setServer(ctx, u, r.ServerName, origin(r.Header), Registered, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return h.withUserData(u, r)
	case cx.UnregisteredUser:
		// The S-CSCF sends it for a request to, or on behalf of, a user it
		// has no registration of. Another S-CSCF may already serve the
		// user: the S-CSCF then sends the request there (TS 24.229
		// 5.4.3.3).
		if u.ServerName != "" && u.ServerName != r.ServerName {
			return cx.ServerAssignmentAnswer{Result: cx.Experimental(cx.DiameterErrorIdentityAlreadyRegistered), ServerName: u.ServerName}
		}
		if err := h.setServer(ctx, u, r.ServerName, origin(r.Header), Unregistered, u.AuthenticationPending); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return h.withUserData(u, r)
	case cx.TimeoutDeregistration, cx.UserDeregistration, cx.AdministrativeDeregistration:
		if err := h.setServer(ctx, u, "", diameter.Identity{}, NotRegistered, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterSuccess)}
	case cx.AuthenticationFailure, cx.AuthenticationTimeout:
		name, scscf := u.ServerName, u.SCSCF
		if u.State == NotRegistered {
			name, scscf = "", diameter.Identity{}
		}
		if err := h.setServer(ctx, u, name, scscf, u.State, false); err != nil {
			return cx.ServerAssignmentAnswer{Result: h.storeFailed(err)}
		}
		return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterSuccess)}
	case cx.NoAssignment:
		if u.ServerName != r.ServerName {
			return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
		}
		return h.withUserData(u, r)
	}
	// SIMPLIFIED: TS 29.228 6.1.2.1. The *_STORE_SERVER_NAME and
	// DEREGISTRATION_TOO_MUCH_DATA assignment types are not used by this
	// project's S-CSCF.
	return cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
}

func (h *HSS) withUserData(u Subscriber, r cx.ServerAssignmentRequest) cx.ServerAssignmentAnswer {
	a := cx.ServerAssignmentAnswer{Result: cx.Base(cx.DiameterSuccess), UserName: u.PrivateIdentity}
	// TS 29.228 6.6: the HSS may skip the profile when the S-CSCF says it
	// already has it.
	if !r.UserDataAlreadyAvailable {
		a.UserData = h.userProfile(u).Marshal()
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
func origin(h cx.Header) diameter.Identity {
	return diameter.Identity{OriginHost: h.OriginHost, OriginRealm: h.OriginRealm}
}
