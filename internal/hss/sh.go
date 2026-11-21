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
func (h *HSS) handleSh(request *diam.Message) *diam.Message {
	identity := h.node.Identity()
	switch request.Header.CommandCode {
	case sh.CommandUserData:
		udr, err := sh.ParseUserDataRequest(request)
		if err != nil {
			return h.shUnableToComply(request, err)
		}
		return h.userData(udr).Message(request, identity)
	case sh.CommandProfileUpdate:
		pur, err := sh.ParseProfileUpdateRequest(request)
		if err != nil {
			return h.shUnableToComply(request, err)
		}
		return h.profileUpdate(pur).Message(request, identity)
	}
	return diameter.CommandUnsupportedAnswer(identity, request)
}

func (h *HSS) shUnableToComply(request *diam.Message, err error) *diam.Message {
	h.log.Warn("cannot read request", "error", err.Error())
	return sh.NewAnswer(request, h.node.Identity(), cx.Base(cx.DiameterUnableToComply))
}

// shCheck runs the checks that start both Sh-Pull and Sh-Update, in
// the order of TS 29.328 6.1.1.1 and 6.1.2.1: the AS may read or
// update the data (step 1), the user exists (step 2), and the kind of
// identity is a key of the data (step 3).
//
// SIMPLIFIED: TS 29.328 6.1.1.1 and 6.1.2.1 step 2a. The private
// identity that an AS may add in User-Name is not read, so it is never
// checked against the user identity.
func (h *HSS) shCheck(id sh.UserIdentity, refs []sh.DataReference, update bool) (Subscriber, sh.DataReference, cx.Result, bool) {
	if len(refs) != 1 {
		// Several are allowed only with features this HSS does not
		// negotiate, and none is a malformed request.
		return Subscriber{}, 0, cx.Base(cx.DiameterUnableToComply), false
	}
	ref := refs[0]
	access, known := shDataAccess[ref]
	switch {
	case update && !(known && access.update):
		return Subscriber{}, ref, cx.Experimental(sh.DiameterErrorUserDataCannotBeModified), false
	case !update && !(known && access.pull):
		return Subscriber{}, ref, cx.Experimental(sh.DiameterErrorUserDataCannotBeRead), false
	}
	u, ok := h.shSubscriber(id)
	if !ok {
		return Subscriber{}, ref, cx.Experimental(cx.DiameterErrorUserUnknown), false
	}
	if id.PublicIdentity != "" && !access.byPublicIdentity || id.PublicIdentity == "" && !access.byMSISDN {
		return Subscriber{}, ref, cx.Experimental(sh.DiameterErrorOperationNotAllowed), false
	}
	return u, ref, cx.Result{}, true
}

// shSubscriber finds the subscriber that a User-Identity names. Both
// ways are a lookup in the public identity index, never a walk over
// all subscribers.
func (h *HSS) shSubscriber(id sh.UserIdentity) (Subscriber, bool) {
	switch {
	case id.PublicIdentity != "":
		return h.store.SubscriberOf(id.PublicIdentity)
	case sh.ValidMSISDN(id.MSISDN):
		return h.store.SubscriberOf(telURI(id.MSISDN))
	}
	return Subscriber{}, false
}

// telURI is the public identity that an MSISDN is stored as, the global
// number form of RFC 3966 that ims.CanonicalURI also produces.
func telURI(msisdn string) string { return "tel:+" + msisdn }

// userData is the HSS side of Sh-Pull, TS 29.328 6.1.1.1.
func (h *HSS) userData(r sh.UserDataRequest) sh.UserDataAnswer {
	u, ref, result, ok := h.shCheck(r.UserIdentity, r.DataReferences, false)
	if !ok {
		return sh.UserDataAnswer{Result: result}
	}
	var data sh.ShData
	switch ref {
	case sh.RepositoryData:
		// TS 29.328 6.1.1.1: a read of repository data names its
		// Service-Indication.
		if len(r.ServiceIndications) != 1 {
			return sh.UserDataAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
		}
		if d, ok := h.store.RepositoryData(u.PrivateIdentity, r.ServiceIndications[0]); ok {
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
	a := sh.UserDataAnswer{Result: cx.Base(cx.DiameterSuccess)}
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
func (h *HSS) profileUpdate(r sh.ProfileUpdateRequest) sh.ProfileUpdateAnswer {
	u, ref, result, ok := h.shCheck(r.UserIdentity, r.DataReferences, true)
	if !ok {
		return sh.ProfileUpdateAnswer{Result: result}
	}
	if ref == sh.RepositoryData {
		return sh.ProfileUpdateAnswer{Result: h.updateRepositoryData(u, r)}
	}
	data, err := sh.ParseShData(r.UserData)
	if err != nil || data.ShIMSData == nil || data.ShIMSData.SMSRegistrationInfo == nil {
		h.log.Warn("PUR without SMSRegistrationInfo", "public_identity", r.UserIdentity.PublicIdentity, "msisdn", r.UserIdentity.MSISDN)
		return sh.ProfileUpdateAnswer{Result: cx.Base(cx.DiameterUnableToComply)}
	}
	number := data.ShIMSData.SMSRegistrationInfo.IPSMGWNumber

	// The subscriber is read again under its lock, so that a SAR that
	// writes the registration state at the same time keeps this number.
	unlock, _ := h.users.Lock(context.Background(), u.PrivateIdentity)
	defer unlock()
	u, ok = h.shSubscriber(r.UserIdentity)
	if !ok {
		return sh.ProfileUpdateAnswer{Result: cx.Experimental(cx.DiameterErrorUserUnknown)}
	}
	// Step 4b: an empty number removes the registration. The IP-SM-GW
	// Diameter identity is stored only when the HSS and the IP-SM-GW
	// support S6c, which neither does here.
	if u.IPSMGWNumber != number {
		h.log.Info("SMS registration", "private_identity", u.PrivateIdentity, "from", u.IPSMGWNumber, "to", number)
	}
	u.IPSMGWNumber = number
	h.store.Put(u)
	return sh.ProfileUpdateAnswer{Result: cx.Base(cx.DiameterSuccess)}
}

// maxServiceData is the most ServiceData the HSS takes for one
// Service-Indication (TS 29.328 6.1.2.1 step 6). The clause leaves the
// size to the HSS. This one is far above an MMTelServices document
// with every service of TS 29.364 7.2, and keeps an AS from filling the
// HSS.
const maxServiceData = 64 << 10

// updateRepositoryData is steps 5 and 6 of TS 29.328 6.1.2.1 for the
// repository data of u.
func (h *HSS) updateRepositoryData(u Subscriber, r sh.ProfileUpdateRequest) cx.Result {
	data, err := sh.ParseShData(r.UserData)
	if err != nil || len(data.RepositoryData) != 1 {
		h.log.Warn("PUR without one RepositoryData", "public_identity", r.UserIdentity.PublicIdentity)
		return cx.Base(cx.DiameterUnableToComply)
	}
	update := data.RepositoryData[0]
	unlock, _ := h.users.Lock(context.Background(), u.PrivateIdentity)
	defer unlock()
	for {
		stored, ok := h.store.RepositoryData(u.PrivateIdentity, update.ServiceIndication)
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
		if h.store.CompareAndSwapRepositoryData(u.PrivateIdentity, old, update) {
			h.log.Info("repository data", "private_identity", u.PrivateIdentity, "service_indication", update.ServiceIndication,
				"sequence_number", update.SequenceNumber, "removed", update.ServiceData == nil)
			return cx.Base(cx.DiameterSuccess)
		}
		if _, ok := h.store.Subscriber(u.PrivateIdentity); !ok {
			// The provisioning API removed the subscription meanwhile.
			return cx.Experimental(cx.DiameterErrorUserUnknown)
		}
	}
}

// repositoryUpdate is the check of TS 29.328 6.1.2.1 step 6 for update,
// a RepositoryData of a PUR, against stored, the data that the HSS has
// under the same Service-Indication, or nil. A success lets the HSS
// store the update, or remove the data if the update has no
// ServiceData.
func repositoryUpdate(stored *sh.TransparentData, update sh.TransparentData) cx.Result {
	outOfSync := cx.Experimental(sh.DiameterErrorTransparentDataOutOfSync)
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
			return cx.Experimental(sh.DiameterErrorOperationNotAllowed)
		}
	}
	if tooMuch {
		return cx.Experimental(sh.DiameterErrorTooMuchData)
	}
	return cx.Base(cx.DiameterSuccess)
}
