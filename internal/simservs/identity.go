package simservs

import "encoding/xml"

// OriginatingIdentityPresentation is the OIP service element of TS
// 24.607 4.10.2. The user turns OIP on and off with the active
// attribute (TS 24.607 4.10.1).
type OriginatingIdentityPresentation struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap originating-identity-presentation"`
	SimservType
}

// OriginatingIdentityPresentationRestriction is the OIR service element
// of TS 24.607 4.10.2. Activating it activates OIR in the temporary
// mode, whose default is DefaultBehaviour (TS 24.607 4.10.1). GSMA
// IR.92 2.3.6 does not ask for this setting, but the AS reads it when it
// is there.
type OriginatingIdentityPresentationRestriction struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap originating-identity-presentation-restriction"`
	SimservType
	DefaultBehaviour *DefaultBehaviour `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap default-behaviour"`
}

// TerminatingIdentityPresentation is the TIP service element of TS
// 24.608 4.9.2.
type TerminatingIdentityPresentation struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap terminating-identity-presentation"`
	SimservType
}

// TerminatingIdentityPresentationRestriction is the TIR service element
// of TS 24.608 4.9.2, with the same temporary mode as OIR (TS 24.608
// 4.9.1).
type TerminatingIdentityPresentationRestriction struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap terminating-identity-presentation-restriction"`
	SimservType
	DefaultBehaviour *DefaultBehaviour `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap default-behaviour"`
}

// DefaultBehaviour is the value of <default-behaviour> (TS 24.607
// 4.10.2, TS 24.608 4.9.2).
type DefaultBehaviour string

const (
	PresentationRestricted    DefaultBehaviour = "presentation-restricted"
	PresentationNotRestricted DefaultBehaviour = "presentation-not-restricted"
)

// defaultBehaviourOrDefault applies the schema default. An empty
// element takes the default of the schema too (XML Schema, element
// defaults).
func defaultBehaviourOrDefault(d *DefaultBehaviour) DefaultBehaviour {
	if d == nil || *d == "" {
		return PresentationRestricted
	}
	return *d
}

// DefaultBehaviourOrDefault returns the default of the temporary mode.
func (o OriginatingIdentityPresentationRestriction) DefaultBehaviourOrDefault() DefaultBehaviour {
	return defaultBehaviourOrDefault(o.DefaultBehaviour)
}

// DefaultBehaviourOrDefault returns the default of the temporary mode.
func (o TerminatingIdentityPresentationRestriction) DefaultBehaviourOrDefault() DefaultBehaviour {
	return defaultBehaviourOrDefault(o.DefaultBehaviour)
}

// CommunicationWaiting is the CW service element of TS 24.615 4.8.3.
// GSMA IR.92 2.3.4 only asks for the terminal based service, which the
// UE sets without Ut; the element is here because the AS and the HSS
// carry it in MMTelServices.
type CommunicationWaiting struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap communication-waiting"`
	SimservType
}

func (v *validator) defaultBehaviour(service string, d *DefaultBehaviour) {
	if d != nil && *d != "" && *d != PresentationRestricted && *d != PresentationNotRestricted {
		v.fail(conflict(SchemaValidationError, "%s: default-behaviour %q is not allowed", service, *d))
	}
}
