package hss

import (
	"context"

	"github.com/fiorix/go-diameter/v4/diam"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/sh"
)

// This file is the HSS side of Sh (TS 29.328 6.1.1 and 6.1.2): an
// application server, such as the IP-SM-GW, reads and updates
// subscriber data.
//
// SIMPLIFIED: TS 29.329 7.1. The HSS and the AS do not negotiate the
// optional features of Table 7.1.1 with Supported-Features, so the HSS
// behaves as for an AS that supports none of them: a request names one
// Data-Reference, and one Service-Indication or RepositoryData for
// repository data, and an answer leaves User-Data out when the HSS has
// none of the data (TS 29.328 6.1.1.1 step 5).

// shAccess is one row of TS 29.328 Table 7.6.1: the Sh operations
// allowed on a data reference, and which kinds of user identity may be
// its key.
type shAccess struct {
	pull, update     bool
	byPublicIdentity bool
	byMSISDN         bool
}

// shDataAccess is the AS permissions list of TS 29.328 6.2 for the
// data references the stub serves, with the operations Table 7.6.1
// allows. A data reference that is not here can be neither read nor
// updated.
//
// SIMPLIFIED: TS 29.328 6.2. The HSS should hold permissions per AS,
// identified by the Origin-Host of the request. Here every peer gets
// the same permissions, so any peer that connected may use Sh.
var shDataAccess = map[sh.DataReference]shAccess{
	sh.RepositoryData:      {pull: true, update: true, byPublicIdentity: true},
	sh.IMSPublicIdentity:   {pull: true, byPublicIdentity: true, byMSISDN: true},
	sh.SCSCFName:           {pull: true, byPublicIdentity: true},
	sh.MSISDN:              {pull: true, byPublicIdentity: true, byMSISDN: true},
	sh.SMSRegistrationInfo: {pull: true, update: true, byPublicIdentity: true, byMSISDN: true},
}

// handleSh answers the Sh requests that reach the HSS.
func (h *HSS) handleSh(ctx context.Context, request *diam.Message) *diam.Message {
	identity := h.node.Identity()
	switch request.Header.CommandCode {
	case sh.CommandUserData:
		udr, err := sh.ParseUserDataRequest(request)
		if err != nil {
			return h.shUnableToComply(request, err)
		}
		return h.userData(ctx, udr).Message(request, identity)
	case sh.CommandProfileUpdate:
		pur, err := sh.ParseProfileUpdateRequest(request)
		if err != nil {
			return h.shUnableToComply(request, err)
		}
		return h.profileUpdate(ctx, pur).Message(request, identity)
	}
	return diameter.CommandUnsupportedAnswer(identity, request)
}

func (h *HSS) shUnableToComply(request *diam.Message, err error) *diam.Message {
	h.log.Warn("cannot read request", "error", err.Error())
	return sh.NewAnswer(request, h.node.Identity(), diameter.Base(diameter.DiameterUnableToComply))
}

// shCheck runs the checks that start both Sh-Pull and Sh-Update, in
// the order of TS 29.328 6.1.1.1 and 6.1.2.1: the AS may read or
// update the data (step 1), the user exists (step 2), and the kind of
// identity is a key of the data (step 3).
//
// SIMPLIFIED: TS 29.328 6.1.1.1 and 6.1.2.1 step 2a. The private
// identity that an AS may add in User-Name is not read, so it is never
// checked against the user identity.
func (h *HSS) shCheck(ctx context.Context, id sh.UserIdentity, refs []sh.DataReference, update bool) (Subscriber, sh.DataReference, diameter.Result, bool) {
	if len(refs) != 1 {
		// Several are allowed only with features this HSS does not
		// negotiate, and none is a malformed request.
		return Subscriber{}, 0, diameter.Base(diameter.DiameterUnableToComply), false
	}
	ref := refs[0]
	access, known := shDataAccess[ref]
	switch {
	case update && !(known && access.update):
		return Subscriber{}, ref, diameter.Experimental(sh.DiameterErrorUserDataCannotBeModified), false
	case !update && !(known && access.pull):
		return Subscriber{}, ref, diameter.Experimental(sh.DiameterErrorUserDataCannotBeRead), false
	}
	u, ok, err := h.shSubscriber(ctx, id)
	if err != nil {
		return Subscriber{}, ref, h.storeFailed(err), false
	}
	if !ok {
		return Subscriber{}, ref, diameter.Experimental(cx.DiameterErrorUserUnknown), false
	}
	if id.PublicIdentity != "" && !access.byPublicIdentity || id.PublicIdentity == "" && !access.byMSISDN {
		return Subscriber{}, ref, diameter.Experimental(sh.DiameterErrorOperationNotAllowed), false
	}
	return u, ref, diameter.Result{}, true
}

// shSubscriber finds the subscriber that a User-Identity names. Both
// ways are a lookup in the public identity index, never a walk over
// all subscribers.
func (h *HSS) shSubscriber(ctx context.Context, id sh.UserIdentity) (Subscriber, bool, error) {
	switch {
	case id.PublicIdentity != "":
		return h.store.SubscriberOf(ctx, id.PublicIdentity)
	case sh.ValidMSISDN(id.MSISDN):
		return h.store.SubscriberOf(ctx, telURI(id.MSISDN))
	}
	return Subscriber{}, false, nil
}

// telURI is the public identity that an MSISDN is stored as, the global
// number form of RFC 3966 that ims.CanonicalURI also produces.
func telURI(msisdn string) string { return "tel:+" + msisdn }

// userData is the HSS side of Sh-Pull, TS 29.328 6.1.1.1.
func (h *HSS) userData(ctx context.Context, r sh.UserDataRequest) sh.UserDataAnswer {
	u, ref, result, ok := h.shCheck(ctx, r.UserIdentity, r.DataReferences, false)
	if !ok {
		return sh.UserDataAnswer{Result: result}
	}
	var data sh.ShData
	switch ref {
	case sh.RepositoryData:
		// TS 29.328 6.1.1.1: a read of repository data names its
		// Service-Indication.
		if len(r.ServiceIndications) != 1 {
			return sh.UserDataAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
		}
		d, ok, err := h.store.RepositoryData(ctx, u.PrivateIdentity, r.ServiceIndications[0])
		if err != nil {
			return sh.UserDataAnswer{Result: h.storeFailed(err)}
		}
		if ok {
			data.RepositoryData = []sh.TransparentData{d}
		}
	case sh.IMSPublicIdentity:
		if ids := publicIdentities(u, r.IdentitySets); len(ids) > 0 {
			data.PublicIdentifiers = &sh.PublicIdentifiers{IMSPublicIdentities: ids}
		}
	case sh.MSISDN:
		if m := u.MSISDN(); m != "" {
			data.PublicIdentifiers = &sh.PublicIdentifiers{MSISDNs: []string{m}}
		}
	case sh.SCSCFName:
		if u.ServerName != "" {
			data.ShIMSData = &sh.ShIMSData{SCSCFName: u.ServerName}
		}
	case sh.SMSRegistrationInfo:
		if u.IPSMGWNumber != "" {
			data.ShIMSData = &sh.ShIMSData{SMSRegistrationInfo: &sh.SMSRegistrationInfoData{IPSMGWNumber: u.IPSMGWNumber}}
		}
	}
	a := sh.UserDataAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
	if !data.Empty() {
		a.UserData = data.Marshal()
	}
	return a
}

// publicIdentities is the IMSPublicIdentity data of TS 29.328 7.6.2.
// At most one Identity-Set is read; none means ALL_IDENTITIES. Every
// set leaves out the barred identities, such as a temporary public
// identity.
//
// SIMPLIFIED: TS 29.328 7.6.2. A subscription of the stub is one
// implicit registration set whose identities share one service profile,
// so IMPLICIT_IDENTITIES and ALIAS_IDENTITIES return the same set as
// ALL_IDENTITIES. The HSS does not reject them when the user is named
// by MSISDN, for which the clause says they do not apply.
func publicIdentities(u Subscriber, sets []sh.IdentitySet) []string {
	if len(sets) > 0 && sets[0] == sh.RegisteredIdentities && u.State != Registered {
		return nil
	}
	var ids []string
	for _, id := range u.PublicIdentities {
		if !u.Barred(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// profileUpdate is the HSS side of Sh-Update, TS 29.328 6.1.2.1, for
// the data references an AS may update here: repository data and
// SMSRegistrationInfo.
func (h *HSS) profileUpdate(ctx context.Context, r sh.ProfileUpdateRequest) sh.ProfileUpdateAnswer {
	u, ref, result, ok := h.shCheck(ctx, r.UserIdentity, r.DataReferences, true)
	if !ok {
		return sh.ProfileUpdateAnswer{Result: result}
	}
	if ref == sh.RepositoryData {
		return sh.ProfileUpdateAnswer{Result: h.updateRepositoryData(ctx, u, r)}
	}
	data, err := sh.ParseShData(r.UserData)
	if err != nil || data.ShIMSData == nil || data.ShIMSData.SMSRegistrationInfo == nil {
		h.log.Warn("PUR without SMSRegistrationInfo", "public_identity", r.UserIdentity.PublicIdentity, "msisdn", r.UserIdentity.MSISDN)
		return sh.ProfileUpdateAnswer{Result: diameter.Base(diameter.DiameterUnableToComply)}
	}
	number := data.ShIMSData.SMSRegistrationInfo.IPSMGWNumber

	// The subscriber is read again under its lock, so that a SAR that
	// writes the registration state at the same time keeps this number.
	unlock, err := h.lockSubscriber(u.PrivateIdentity)
	if err != nil {
		return sh.ProfileUpdateAnswer{Result: h.lockFailed(err)}
	}
	defer unlock()
	u, ok, err = h.shSubscriber(ctx, r.UserIdentity)
	if err != nil {
		return sh.ProfileUpdateAnswer{Result: h.storeFailed(err)}
	}
	if !ok {
		return sh.ProfileUpdateAnswer{Result: diameter.Experimental(cx.DiameterErrorUserUnknown)}
	}
	// Step 4b: an empty number removes the registration, and with it the
	// Diameter identity. A number that is there brings the identity of
	// the IP-SM-GW that sent the PUR, if S6c reaches it.
	from := u.IPSMGWNumber
	u.IPSMGWNumber, u.IPSMGW = number, diameter.Identity{}
	if number != "" && data.ShIMSData.SMSRegistrationInfo.SupportsS6c() {
		u.IPSMGW = origin(r.Header)
	}
	// TS 23.040 3.2.6 item 2c, TS 23.204 6.5b step 2: an IP-SM-GW
	// that registers says that the UE is reachable for SMS over IP
	// again, so UNRI goes. The service centres that hold a short message
	// for the user are then alerted, unless the memory of the UE is the
	// reason they hold it: only a notification of free memory helps
	// then.
	alert := false
	if number != "" {
		u.UNRI = false
		alert = len(u.MessageWaitingData) > 0 && !u.MCEF
	}
	if err := h.store.Put(ctx, u); err != nil {
		return sh.ProfileUpdateAnswer{Result: h.storeFailed(err)}
	}
	if from != number {
		h.log.Info("SMS registration", "private_identity", u.PrivateIdentity, "from", from, "to", number,
			"ip_sm_gw", u.IPSMGW.OriginHost)
	}
	if alert {
		h.alertServiceCentres(u)
	}
	return sh.ProfileUpdateAnswer{Result: diameter.Base(diameter.DiameterSuccess)}
}

// maxServiceData is the most ServiceData the HSS takes for one
// Service-Indication (TS 29.328 6.1.2.1 step 6). The clause leaves the
// size to the HSS. This one is far above an MMTelServices document
// with every service of TS 29.364 7.2, and keeps an AS from filling the
// HSS.
const maxServiceData = 64 << 10

// updateRepositoryData is steps 5 and 6 of TS 29.328 6.1.2.1 for the
// repository data of u.
func (h *HSS) updateRepositoryData(ctx context.Context, u Subscriber, r sh.ProfileUpdateRequest) diameter.Result {
	data, err := sh.ParseShData(r.UserData)
	if err != nil || len(data.RepositoryData) != 1 {
		h.log.Warn("PUR without one RepositoryData", "public_identity", r.UserIdentity.PublicIdentity)
		return diameter.Base(diameter.DiameterUnableToComply)
	}
	update := data.RepositoryData[0]
	unlock, err := h.lockSubscriber(u.PrivateIdentity)
	if err != nil {
		return h.lockFailed(err)
	}
	defer unlock()
	for {
		stored, ok, err := h.store.RepositoryData(ctx, u.PrivateIdentity, update.ServiceIndication)
		if err != nil {
			return h.storeFailed(err)
		}
		var old *sh.TransparentData
		if ok {
			old = &stored
		}
		if result := repositoryUpdate(old, update); !result.Success() {
			h.log.Info("repository data not updated", "private_identity", u.PrivateIdentity, "service_indication", update.ServiceIndication,
				"sequence_number", update.SequenceNumber, "stored", ok, "stored_sequence_number", stored.SequenceNumber, "result", result.String())
			return result
		}
		// Step 5 has another entity's update in progress refused. Here an
		// update either is done or has not begun, so one that another HSS
		// instance made in between is caught by step 6 on the next try.
		swapped, err := h.store.CompareAndSwapRepositoryData(ctx, u.PrivateIdentity, old, update)
		if err != nil {
			return h.storeFailed(err)
		}
		if swapped {
			h.log.Info("repository data", "private_identity", u.PrivateIdentity, "service_indication", update.ServiceIndication,
				"sequence_number", update.SequenceNumber, "removed", update.ServiceData == nil)
			return diameter.Base(diameter.DiameterSuccess)
		}
		_, ok, err = h.store.Subscriber(ctx, u.PrivateIdentity)
		if err != nil {
			return h.storeFailed(err)
		}
		if !ok {
			// The provisioning API removed the subscription meanwhile.
			return diameter.Experimental(cx.DiameterErrorUserUnknown)
		}
	}
}

// repositoryUpdate is the check of TS 29.328 6.1.2.1 step 6 for update,
// a RepositoryData of a PUR, against stored, the data that the HSS has
// under the same Service-Indication, or nil. A success lets the HSS
// store the update, or remove the data if the update has no
// ServiceData.
func repositoryUpdate(stored *sh.TransparentData, update sh.TransparentData) diameter.Result {
	outOfSync := diameter.Experimental(sh.DiameterErrorTransparentDataOutOfSync)
	tooMuch := update.ServiceData != nil && len(update.ServiceData.XML) > maxServiceData
	if stored != nil {
		n := int(update.SequenceNumber)
		if n == 0 || n-1 != int(stored.SequenceNumber)%65535 {
			return outOfSync
		}
	} else {
		switch {
		case update.SequenceNumber != 0:
			return outOfSync
		case update.ServiceData == nil:
			return diameter.Experimental(sh.DiameterErrorOperationNotAllowed)
		}
	}
	if tooMuch {
		return diameter.Experimental(sh.DiameterErrorTooMuchData)
	}
	return diameter.Base(diameter.DiameterSuccess)
}
