package simservs

import (
	"encoding/xml"
	"fmt"
)

// ServiceIndication is the Service-Indication of the Sh Repository Data
// that holds an MMTelServices document (TS 29.364 7.2.0). The document
// is the one element inside the ServiceData of the RepositoryData (TS
// 29.328 Annex D, tServiceData).
//
// TS 29.364 also defines a binary format for the subset of services
// that match the PSTN/ISDN and CS services (clause 6). This package
// uses the XML format, which covers all MMTel services (TS 29.364 4).
const ServiceIndication = "MMTEL-Services"

// MMTelServices is the root element of the MMTel service data (TS
// 29.364 7.2.1). For each service, a complete-* element holds the user
// part, which is the element of the simservs document, and the operator
// part, which says whether the operator authorized the service and with
// which subscription options. The user changes the user parts over Ut;
// only the operator changes the operator parts.
//
// SIMPLIFIED: TS 29.364 7.2.1. Only the services that GSMA IR.92 2.3
// asks the network for are modelled: OIP, OIR, TIP, TIR, CDIV, CW, HOLD
// and CB. The other complete-* elements (MCID, CCBS, CCNR, MWI, CONF,
// AOC, ECT, CAT, FA) are named in Unknown and left out by Marshal. A
// Document keeps them, so the XCAP server does not lose them.
type MMTelServices struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap MMTelServices"`

	CompleteOriginatingIdentityPresentation *CompleteOriginatingIdentityPresentation
	CompleteOriginatingIdentityRestriction  *CompleteOriginatingIdentityRestriction
	CompleteTerminatingIdentityPresentation *CompleteTerminatingIdentityPresentation
	CompleteTerminatingIdentityRestriction  *CompleteTerminatingIdentityRestriction
	CompleteCommunicationDiversion          *CompleteCommunicationDiversion
	CompleteCommunicationWaiting            *CompleteCommunicationWaiting
	CompleteCommunicationHold               *CompleteCommunicationHold
	CompleteCommunicationBarring            *CompleteCommunicationBarring

	Unknown []UnknownElement `xml:",any"`
}

// OperatorServiceConfig is the operatorServiceConfigType that every
// operator part extends.
//
// TS 29.364 V19.1.0 includes operator-common-data.xsd, which defines
// this type, but no longer prints it. The definition used here, a
// required boolean attribute authorized, is the one printed in V9.3.0
// 7.2.1; the data semantics of V19.1.0 7.2.3.2.1 and the clauses after
// it still describe the same attribute.
type OperatorServiceConfig struct {
	Authorized bool `xml:"authorized,attr"`
}

// IdentityPresentationMode is the identityPresentationModeType of
// operator-common-data.xsd (TS 29.364 V9.3.0 7.2.1).
type IdentityPresentationMode string

const (
	ModePermanent IdentityPresentationMode = "permanent"
	ModeTemporary IdentityPresentationMode = "temporary"
)

// IdentityPresentationRestriction is the
// identityPresentationRestrictionType of operator-common-data.xsd.
type IdentityPresentationRestriction string

const (
	RestrictOnlyIdentity          IdentityPresentationRestriction = "only-identity"
	RestrictAllPrivateInformation IdentityPresentationRestriction = "all-private-information"
)

// IdentityPresentationRestrictionOverride is the
// identityPresentationRestrictionOverrideType of
// operator-common-data.xsd: whether the user sees identities that the
// other party restricted (the override category of TS 24.607).
type IdentityPresentationRestrictionOverride string

const (
	OverrideActive    IdentityPresentationRestrictionOverride = "override-active"
	OverrideNotActive IdentityPresentationRestrictionOverride = "override-not-active"
)

// CompleteOriginatingIdentityPresentation is the OIP service (TS 29.364
// 7.2.3).
type CompleteOriginatingIdentityPresentation struct {
	XMLName                                 xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-originating-identity-presentation"`
	OriginatingIdentityPresentation         OriginatingIdentityPresentation
	OperatorOriginatingIdentityPresentation OperatorOriginatingIdentityPresentation
}

// OperatorOriginatingIdentityPresentation is the operator part of OIP
// (TS 29.364 7.2.3.2.2).
type OperatorOriginatingIdentityPresentation struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-originating-identity-presentation"`
	OperatorServiceConfig
	// RestrictionOverride defaults to override-not-active.
	RestrictionOverride *IdentityPresentationRestrictionOverride `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap restriction-override"`
}

// CompleteOriginatingIdentityRestriction is the OIR service (TS 29.364
// 7.2.3).
type CompleteOriginatingIdentityRestriction struct {
	XMLName                                            xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-originating-identity-restriction"`
	OriginatingIdentityPresentationRestriction         OriginatingIdentityPresentationRestriction
	OperatorOriginatingIdentityPresentationRestriction OperatorOriginatingIdentityPresentationRestriction
}

// OperatorOriginatingIdentityPresentationRestriction is the operator
// part of OIR (TS 29.364 7.2.3.2.2): the permanent or temporary mode
// and what is restricted.
type OperatorOriginatingIdentityPresentationRestriction struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-originating-identity-presentation-restriction"`
	OperatorServiceConfig
	Mode        IdentityPresentationMode        `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap mode"`
	Restriction IdentityPresentationRestriction `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap restriction"`
}

// CompleteTerminatingIdentityPresentation is the TIP service (TS 29.364
// 7.2.5).
type CompleteTerminatingIdentityPresentation struct {
	XMLName                                 xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-terminating-identity-presentation"`
	TerminatingIdentityPresentation         TerminatingIdentityPresentation
	OperatorTerminatingIdentityPresentation OperatorTerminatingIdentityPresentation
}

// OperatorTerminatingIdentityPresentation is the operator part of TIP
// (TS 29.364 7.2.5.2.2).
type OperatorTerminatingIdentityPresentation struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-terminating-identity-presentation"`
	OperatorServiceConfig
	RestrictionOverride *IdentityPresentationRestrictionOverride `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap restriction-override"`
}

// CompleteTerminatingIdentityRestriction is the TIR service (TS 29.364
// 7.2.5).
type CompleteTerminatingIdentityRestriction struct {
	XMLName                                            xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-terminating-identity-restriction"`
	TerminatingIdentityPresentationRestriction         TerminatingIdentityPresentationRestriction
	OperatorTerminatingIdentityPresentationRestriction OperatorTerminatingIdentityPresentationRestriction
}

// OperatorTerminatingIdentityPresentationRestriction is the operator
// part of TIR (TS 29.364 7.2.5.2.2).
type OperatorTerminatingIdentityPresentationRestriction struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-terminating-identity-presentation-restriction"`
	OperatorServiceConfig
	Mode IdentityPresentationMode `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap mode"`
}

// CompleteCommunicationDiversion is the CDIV service (TS 29.364 7.2.8).
type CompleteCommunicationDiversion struct {
	XMLName                        xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-communication-diversion"`
	CommunicationDiversion         CommunicationDiversion
	OperatorCommunicationDiversion OperatorCommunicationDiversion
}

// OperatorCommunicationDiversion is the operator part of CDIV (TS 29.364
// 7.2.8.2.2). Every element is required by the schema.
type OperatorCommunicationDiversion struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-communication-diversion"`
	OperatorServiceConfig
	// CommunicationRetentionOnInvocation is
	// retain-until-alerting-at-diverted-to-user or
	// clear-communication-on-invocation-of-diversion.
	CommunicationRetentionOnInvocation string `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap communication-retention-on-invocation"`
	// RetentionWhenDivertingRejectedAtDivertedToUser is
	// continue-to-alert-diverting-user or no-action-at-diverting-user.
	RetentionWhenDivertingRejectedAtDivertedToUser string `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap retention-when-diverting-rejected-at-diverted-to-user"`
	// TotalNumberOfDiversionsForEachCommunication is the limit that the
	// AS checks against the History-Info of a call (TS 24.604
	// 4.5.2.6.1).
	TotalNumberOfDiversionsForEachCommunication int `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap total-number-of-diversions-for-each-communication"`
	// CdivIndicationTimer is in seconds, from 0 to 60.
	CdivIndicationTimer int `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap cdiv-indication-timer"`
	// CommunicationForwardingOnNoReplyTimer is the operator default of
	// the no reply timer in seconds, from 0 to 180 (TS 24.604 4.8.1).
	CommunicationForwardingOnNoReplyTimer int `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap communication-forwarding-on-no-reply-timer"`
}

// NoReplyTimer returns the seconds CFNR waits: the user's NoReplyTimer
// when it is set, else the operator default (TS 24.604 4.8.1).
func (c CompleteCommunicationDiversion) NoReplyTimer() int {
	if t := c.CommunicationDiversion.NoReplyTimer; t != nil {
		return *t
	}
	return c.OperatorCommunicationDiversion.CommunicationForwardingOnNoReplyTimer
}

// CompleteCommunicationWaiting is the CW service (TS 29.364 7.2.9).
type CompleteCommunicationWaiting struct {
	XMLName                      xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-communication-waiting"`
	CommunicationWaiting         CommunicationWaiting
	OperatorCommunicationWaiting OperatorCommunicationWaiting
}

// OperatorCommunicationWaiting is the operator part of CW (TS 29.364
// 7.2.9.2.2).
type OperatorCommunicationWaiting struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-communication-waiting"`
	OperatorServiceConfig
	CallingUserReceivesNotificationHisCallIsWaiting bool `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap calling-user-receives-notification-his-call-is-waiting"`
}

// CompleteCommunicationHold is the HOLD service (TS 29.364 7.2.10). HOLD
// has no user data.
type CompleteCommunicationHold struct {
	XMLName                   xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-communication-hold"`
	OperatorCommunicationHold OperatorCommunicationHold
}

// OperatorCommunicationHold is the operator part of HOLD (TS 29.364
// 7.2.10.2.2).
type OperatorCommunicationHold struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-communication-hold"`
	OperatorServiceConfig
}

// CompleteCommunicationBarring is the CB service, ICB and OCB together
// (TS 29.364 7.2.11).
type CompleteCommunicationBarring struct {
	XMLName                              xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap complete-communication-barring"`
	IncomingCommunicationBarring         IncomingCommunicationBarring
	OutgoingCommunicationBarring         OutgoingCommunicationBarring
	OperatorIncomingCommunicationBarring OperatorIncomingCommunicationBarring
	OperatorOutgoingCommunicationBarring OperatorOutgoingCommunicationBarring
}

// OperatorIncomingCommunicationBarring is the operator part of ICB (TS
// 29.364 7.2.11.2.2).
type OperatorIncomingCommunicationBarring struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-incoming-communication-barring"`
	OperatorServiceConfig
}

// OperatorOutgoingCommunicationBarring is the operator part of OCB (TS
// 29.364 7.2.11.2.2).
type OperatorOutgoingCommunicationBarring struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap operator-outgoing-communication-barring"`
	OperatorServiceConfig
}

// ParseMMTelServices reads the MMTelServices document from the
// ServiceData of the Repository Data.
func ParseMMTelServices(data []byte) (MMTelServices, error) {
	var m MMTelServices
	if err := unmarshal(data, &m); err != nil {
		return MMTelServices{}, fmt.Errorf("MMTelServices: %w", err)
	}
	return m, nil
}

// Marshal writes m as the element that goes into ServiceData, without
// an XML declaration, because it sits inside the Sh-Data document.
func (m MMTelServices) Marshal() []byte { return MarshalElement(m) }

// Simservs returns the user parts as a simservs document: what the user
// sees over Ut (TS 24.623 6.1).
func (m MMTelServices) Simservs() Simservs {
	var s Simservs
	if c := m.CompleteOriginatingIdentityPresentation; c != nil {
		s.OriginatingIdentityPresentation = &c.OriginatingIdentityPresentation
	}
	if c := m.CompleteOriginatingIdentityRestriction; c != nil {
		s.OriginatingIdentityPresentationRestriction = &c.OriginatingIdentityPresentationRestriction
	}
	if c := m.CompleteTerminatingIdentityPresentation; c != nil {
		s.TerminatingIdentityPresentation = &c.TerminatingIdentityPresentation
	}
	if c := m.CompleteTerminatingIdentityRestriction; c != nil {
		s.TerminatingIdentityPresentationRestriction = &c.TerminatingIdentityPresentationRestriction
	}
	if c := m.CompleteCommunicationDiversion; c != nil {
		s.CommunicationDiversion = &c.CommunicationDiversion
	}
	if c := m.CompleteCommunicationWaiting; c != nil {
		s.CommunicationWaiting = &c.CommunicationWaiting
	}
	if c := m.CompleteCommunicationBarring; c != nil {
		s.IncomingCommunicationBarring = &c.IncomingCommunicationBarring
		s.OutgoingCommunicationBarring = &c.OutgoingCommunicationBarring
	}
	return s
}

// Validate checks the user parts as Simservs.Validate does, and the
// values that the operator parts restrict (TS 29.364 7.2).
func (m MMTelServices) Validate() error {
	if err := m.Simservs().Validate(); err != nil {
		return err
	}
	if c := m.CompleteCommunicationDiversion; c != nil {
		o := c.OperatorCommunicationDiversion
		switch {
		case o.CommunicationRetentionOnInvocation != "retain-until-alerting-at-diverted-to-user" &&
			o.CommunicationRetentionOnInvocation != "clear-communication-on-invocation-of-diversion":
			return conflict(SchemaValidationError, "communication-retention-on-invocation %q is not allowed", o.CommunicationRetentionOnInvocation)
		case o.RetentionWhenDivertingRejectedAtDivertedToUser != "continue-to-alert-diverting-user" &&
			o.RetentionWhenDivertingRejectedAtDivertedToUser != "no-action-at-diverting-user":
			return conflict(SchemaValidationError, "retention-when-diverting-rejected-at-diverted-to-user %q is not allowed", o.RetentionWhenDivertingRejectedAtDivertedToUser)
		case o.CdivIndicationTimer < 0 || o.CdivIndicationTimer > 60:
			return conflict(SchemaValidationError, "cdiv-indication-timer %d is not between 0 and 60", o.CdivIndicationTimer)
		case o.CommunicationForwardingOnNoReplyTimer < 0 || o.CommunicationForwardingOnNoReplyTimer > 180:
			return conflict(SchemaValidationError, "communication-forwarding-on-no-reply-timer %d is not between 0 and 180", o.CommunicationForwardingOnNoReplyTimer)
		}
	}
	for _, mode := range m.identityModes() {
		if mode != ModePermanent && mode != ModeTemporary {
			return conflict(SchemaValidationError, "mode %q is not allowed", mode)
		}
	}
	return nil
}

func (m MMTelServices) identityModes() []IdentityPresentationMode {
	var out []IdentityPresentationMode
	if c := m.CompleteOriginatingIdentityRestriction; c != nil {
		out = append(out, c.OperatorOriginatingIdentityPresentationRestriction.Mode)
	}
	if c := m.CompleteTerminatingIdentityRestriction; c != nil {
		out = append(out, c.OperatorTerminatingIdentityPresentationRestriction.Mode)
	}
	return out
}
