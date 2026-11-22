package mmtel

import (
	"slices"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/b2bua"
	"github.com/yuu-biz/ims-practice/internal/simservs"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the identity services: OIP and OIR (TS 24.607), TIP
// and TIR (TS 24.608). They change the Privacy header field and the
// identities in From and P-Asserted-Identity. The P-Asserted-Identity
// itself leaves the trust domain only where the user allows it: the
// S-CSCF, or the P-CSCF, removes it toward a UE when Privacy holds "id"
// (TS 24.229 4.4.2, 5.4.3.3 step 12; TS 24.608 4.5.2.4 NOTE 1).

// anonymous is the From of a caller whose identity is withheld (RFC
// 3323 4.1.1.3).
const (
	anonymousURI         = "sip:anonymous@anonymous.invalid"
	anonymousDisplayName = "Anonymous"
)

// privateFields are the header fields that may tell who the user is,
// which a request for header privacy keeps out of the message (RFC 3323
// 5.1). Via, Contact and Record-Route, which that clause also names, are
// the AS's own on each leg anyway.
var privateFields = []string{"Call-Info", "Organization", "Server", "User-Agent"}

// restrictOriginatingIdentity is OIR at the AS of the caller (TS 24.607
// 4.5.2.4). By default the user does not subscribe to OIR, and a call
// goes out as the caller asked: a UE that asks for privacy by itself
// (4.5.2.1) gets it, since the network keeps the identity within the
// trust domain whoever asked. The clause would let the operator turn
// such a call down with 403 "OIR not subscribed" instead; this network
// does not.
//
// The AS removes the identity from From, rather than add "user" to
// Privacy; the clause leaves that choice to the operator.
func (c *call) restrictOriginatingIdentity(inv *sip.Invitation, fields *sip.Message) {
	oir := c.services.CompleteOriginatingIdentityRestriction
	if oir == nil || !oir.OperatorOriginatingIdentityPresentationRestriction.Authorized {
		return
	}
	op := oir.OperatorOriginatingIdentityPresentationRestriction
	privacy := sip.ParsePrivacy(fields.Get("Privacy"))
	restricted, anonymizeFrom := false, false
	switch {
	case op.Mode == simservs.ModePermanent:
		restricted = true
	case temporaryDefault(oir.OriginatingIdentityPresentationRestriction.SimservType,
		oir.OriginatingIdentityPresentationRestriction.DefaultBehaviourOrDefault()) == simservs.PresentationRestricted:
		// The caller can still present its identity for this call with
		// Privacy: none.
		restricted = !privacy.Has(sip.PrivacyNone)
	default:
		anonymizeFrom = privacy.Has(sip.PrivacyID) || privacy.Has(sip.PrivacyHeader)
	}
	if restricted {
		value := sip.PrivacyID
		if op.Restriction == simservs.RestrictAllPrivateInformation {
			value = sip.PrivacyHeader
		}
		privacy = slices.DeleteFunc(privacy, func(v string) bool { return v == sip.PrivacyNone })
		if !privacy.Has(value) {
			privacy = append(privacy, value)
		}
		setPrivacy(fields, privacy)
		anonymizeFrom = true
	}
	if anonymizeFrom {
		// NOTE 3 of the clause: the UE should have done so, but it is not
		// trusted to.
		inv.From, inv.FromDisplayName = anonymousURI, anonymousDisplayName
		c.actedOn("OIR")
		c.log.Info("OIR: the caller's identity is restricted", "privacy", fields.Get("Privacy"))
	}
}

// temporaryDefault is the default of OIR or TIR in the temporary mode:
// the user's choice while the user has the service activated, and
// presentation otherwise (TS 24.607 4.10.1, TS 24.608 4.9.1).
func temporaryDefault(user simservs.SimservType, behaviour simservs.DefaultBehaviour) simservs.DefaultBehaviour {
	if !user.IsActive() {
		return simservs.PresentationNotRestricted
	}
	return behaviour
}

// presentOriginatingIdentity is OIP at the AS of the callee (TS 24.607
// 4.5.2.9). By default the callee has OIP, and sees the identity of the
// caller unless the caller withheld it. A callee who does not have it
// gets the call without P-Asserted-Identity and Privacy. A callee in
// the override category gets the identity even when it is withheld.
func (c *call) presentOriginatingIdentity(inv *sip.Invitation, fields *sip.Message) {
	if oip := c.services.CompleteOriginatingIdentityPresentation; oip != nil {
		op := oip.OperatorOriginatingIdentityPresentation
		switch {
		case op.RestrictionOverride != nil && *op.RestrictionOverride == simservs.OverrideActive:
			fields.Remove("Privacy")
			c.actedOn("OIP")
			c.log.Info("OIP: the callee sees the caller's identity in any case")
			return
		case !op.Authorized || !oip.OriginatingIdentityPresentation.IsActive():
			fields.Remove("P-Asserted-Identity")
			fields.Remove("Privacy")
			c.actedOn("OIP")
			c.log.Info("OIP: the callee does not see the caller's identity")
			return
		}
	}
	privacy := sip.ParsePrivacy(fields.Get("Privacy"))
	if privacy.Has(sip.PrivacyHeader) {
		for _, name := range privateFields {
			fields.Remove(name)
		}
		privacy = slices.DeleteFunc(privacy, func(v string) bool { return v == sip.PrivacyHeader })
		if !privacy.Has(sip.PrivacyID) {
			privacy = append(privacy, sip.PrivacyID)
		}
	}
	if privacy.Has(sip.PrivacyUser) {
		inv.From, inv.FromDisplayName = anonymousURI, anonymousDisplayName
		privacy = slices.DeleteFunc(privacy, func(v string) bool { return v == sip.PrivacyUser })
	}
	// "id" stays: it tells the callee's UE that the caller withheld its
	// identity (NOTE 2 of the clause).
	setPrivacy(fields, privacy)
}

// setPrivacy writes privacy into fields, or takes Privacy out when no
// value is left.
func setPrivacy(fields *sip.Message, privacy sip.Privacy) {
	if len(privacy) == 0 {
		fields.Remove("Privacy")
		return
	}
	fields.Set("Privacy", privacy.String())
}

// fromChange is the option tag with which a caller says that it takes a
// change of the From of the callee (RFC 4916, TS 24.608 4.5.2.4).
const fromChange = "from-change"

// hasTerminatingIdentityPresentation tells whether the served user has
// TIP. By default a user has it (TS 24.608 4.5.2.4).
func (c *call) hasTerminatingIdentityPresentation() bool {
	tip := c.services.CompleteTerminatingIdentityPresentation
	return tip == nil || tip.OperatorTerminatingIdentityPresentation.Authorized && tip.TerminatingIdentityPresentation.IsActive()
}

// offerTerminatingIdentity is TIP at the AS of the caller on the INVITE
// (TS 24.608 4.5.2.4): without TIP, the callee is not to change the
// caller's idea of who answered.
func (c *call) offerTerminatingIdentity(fields *sip.Message) {
	if !c.hasTerminatingIdentityPresentation() {
		removeOption(fields, "Supported", fromChange)
	}
}

// presentTerminatingIdentity is TIP at the AS of the caller on a
// response from the callee's side (TS 24.608 4.5.2.4): a caller without
// TIP gets no P-Asserted-Identity and no Privacy, one in the override
// category gets the identity even when it is withheld. Otherwise "id"
// stays, so that the caller's UE knows that the callee withheld its
// identity (NOTE 2 there).
func (c *call) presentTerminatingIdentity(fields *sip.Message) {
	tip := c.services.CompleteTerminatingIdentityPresentation
	if tip == nil {
		return
	}
	if o := tip.OperatorTerminatingIdentityPresentation.RestrictionOverride; o != nil && *o == simservs.OverrideActive {
		fields.Remove("Privacy")
		return
	}
	if !c.hasTerminatingIdentityPresentation() {
		fields.Remove("P-Asserted-Identity")
		fields.Remove("Privacy")
	}
}

// terminatingIdentityRestricted tells whether TIR restricts the served
// user's identity in a response, and whether in the permanent mode.
// By default a user does not have TIR.
func (c *call) terminatingIdentityRestricted() (restricted, permanent bool) {
	tir := c.services.CompleteTerminatingIdentityRestriction
	if tir == nil || !tir.OperatorTerminatingIdentityPresentationRestriction.Authorized {
		return false, false
	}
	if tir.OperatorTerminatingIdentityPresentationRestriction.Mode == simservs.ModePermanent {
		return true, true
	}
	user := tir.TerminatingIdentityPresentationRestriction
	return temporaryDefault(user.SimservType, user.DefaultBehaviourOrDefault()) == simservs.PresentationRestricted, false
}

// withholdTerminatingIdentity is TIR at the AS of the callee on the
// INVITE (TS 24.608 4.5.2.9): in the permanent mode, the callee may not
// tell the caller a new identity with from-change.
func (c *call) withholdTerminatingIdentity(fields *sip.Message) {
	if _, permanent := c.terminatingIdentityRestricted(); permanent {
		removeOption(fields, "Supported", fromChange)
	}
}

// restrictTerminatingIdentity is TIR at the AS of the callee on a
// response from the callee (TS 24.608 4.5.2.9). In the permanent mode
// "none" goes and "id" comes in; in the temporary mode with restriction
// by default, "id" comes in when the callee asked for nothing.
func (c *call) restrictTerminatingIdentity(fields *sip.Message) {
	restricted, permanent := c.terminatingIdentityRestricted()
	privacy := sip.ParsePrivacy(fields.Get("Privacy"))
	switch {
	case !restricted:
		return
	case permanent:
		privacy = slices.DeleteFunc(privacy, func(v string) bool { return v == sip.PrivacyNone })
		if !privacy.Has(sip.PrivacyID) {
			privacy = append(privacy, sip.PrivacyID)
		}
	case len(privacy) == 0:
		privacy = sip.Privacy{sip.PrivacyID}
	}
	setPrivacy(fields, privacy)
}

// removeOption takes one option tag out of a header field that lists
// them, such as Supported.
func removeOption(fields *sip.Message, name, option string) {
	list := b2bua.WithoutOptions(strings.Join(fields.GetAll(name), ","), []string{option})
	if list == "" {
		fields.Remove(name)
		return
	}
	fields.Set(name, list)
}
