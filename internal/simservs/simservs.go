package simservs

import (
	"encoding/xml"
	"fmt"
)

// Names of the supplementary services XCAP application usage (TS 24.623
// 6.2). The document of a user is at
// <XCAP Root>/simservs.ngn.etsi.org/users/<XUI>/simservs.xml.
const (
	AUID              = "simservs.ngn.etsi.org"
	NamespaceSimservs = "http://uri.etsi.org/ngn/params/xml/simservs/xcap"
	MIMEType          = "application/vnd.etsi.simservs+xml"
	DocumentName      = "simservs.xml"
)

// Namespaces of the rule sets. The rules of communication diversion and
// communication barring are common policy rules (RFC 4745 13) with
// conditions and actions from the simservs namespace. The OMA
// extensions (external-list, other-identity) are not supported here;
// the namespace is listed so that documents that declare it keep the
// usual prefix.
const (
	NamespaceCommonPolicy    = "urn:ietf:params:xml:ns:common-policy"
	NamespaceOMACommonPolicy = "urn:oma:xml:xdm:common-policy"
)

// Simservs is the <simservs> root element of the supplementary services
// document (TS 24.623 6.3). Each service element is optional; the
// schema lets them appear in any order, and Marshal writes them in the
// order of the fields.
type Simservs struct {
	XMLName xml.Name `xml:"http://uri.etsi.org/ngn/params/xml/simservs/xcap simservs"`

	OriginatingIdentityPresentation            *OriginatingIdentityPresentation
	OriginatingIdentityPresentationRestriction *OriginatingIdentityPresentationRestriction
	TerminatingIdentityPresentation            *TerminatingIdentityPresentation
	TerminatingIdentityPresentationRestriction *TerminatingIdentityPresentationRestriction
	CommunicationDiversion                     *CommunicationDiversion
	CommunicationWaiting                       *CommunicationWaiting
	IncomingCommunicationBarring               *IncomingCommunicationBarring
	OutgoingCommunicationBarring               *OutgoingCommunicationBarring

	// Unknown names the other children: services of other
	// specifications, service capability elements and <extensions>.
	Unknown []UnknownElement `xml:",any"`
}

// SimservType is the simservType of TS 24.623 6.3 that every service
// element extends.
type SimservType struct {
	// Active is the active attribute. When it is absent the service is
	// active (TS 24.623 6.1); use IsActive.
	Active *bool `xml:"active,attr,omitempty"`
}

// IsActive tells whether the service is activated (TS 24.623 6.1).
func (s SimservType) IsActive() bool { return s.Active == nil || *s.Active }

// Empty is the empty-element-type of TS 24.623 6.3, used by the
// conditions that have no content (<busy/>, <roaming/>, ...).
type Empty struct{}

// UnknownElement records the name of an element that the typed view
// does not model. Validate uses the names to tell elements of other
// namespaces, which the schemas allow, from misspelt simservs elements.
//
// The typed view is for reading the settings and for writing documents
// from scratch, so Marshal leaves these elements out. A server that has
// to keep every element keeps the document as a Document.
type UnknownElement struct {
	Name xml.Name
}

// UnmarshalXML records the name and skips the content.
func (u *UnknownElement) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	u.Name = start.Name
	return d.Skip()
}

// MarshalXML writes nothing; see UnknownElement.
func (u UnknownElement) MarshalXML(*xml.Encoder, xml.StartElement) error { return nil }

// Validate checks the constraints that the schemas and the data
// semantics of the services put on the document. The error is a
// *ConflictError, which an XCAP server answers with 409 (RFC 4825
// 8.2.5).
func (s Simservs) Validate() error {
	v := validator{ruleIDs: map[string]string{}}
	for _, u := range s.Unknown {
		// Elements of other namespaces go inside <extensions> (TS 24.623
		// 6.3). Unknown elements of the simservs namespace are services
		// and capabilities of other specifications, and are kept.
		if u.Name.Space != NamespaceSimservs {
			v.fail(conflict(SchemaValidationError, "<%s> of %s belongs inside <extensions>", u.Name.Local, u.Name.Space))
		}
	}
	if o := s.OriginatingIdentityPresentationRestriction; o != nil {
		v.defaultBehaviour("originating-identity-presentation-restriction", o.DefaultBehaviour)
	}
	if o := s.TerminatingIdentityPresentationRestriction; o != nil {
		v.defaultBehaviour("terminating-identity-presentation-restriction", o.DefaultBehaviour)
	}
	v.diversion(s.CommunicationDiversion)
	if cb := s.IncomingCommunicationBarring; cb != nil {
		v.barring("incoming-communication-barring", &cb.CommunicationBarring)
	}
	if cb := s.OutgoingCommunicationBarring; cb != nil {
		v.barring("outgoing-communication-barring", &cb.CommunicationBarring)
	}
	return v.err
}

// ParseSimservs reads a simservs document.
func ParseSimservs(data []byte) (Simservs, error) {
	var s Simservs
	if err := unmarshal(data, &s); err != nil {
		return Simservs{}, fmt.Errorf("simservs: %w", err)
	}
	return s, nil
}

// Marshal writes s as a simservs document. The simservs namespace is the
// default namespace and the common policy namespace has the prefix cp,
// as in the examples of the service specifications.
func (s Simservs) Marshal() []byte {
	return append([]byte(xml.Header), MarshalElement(s)...)
}

// MarshalElement writes one element of the typed view, such as a Rule or
// a CommunicationDiversion, as an XML fragment body with the namespace
// declarations it needs on the element itself. An XCAP client PUTs such
// a fragment to replace one part of the document (RFC 4825 7.4).
func MarshalElement(v any) []byte {
	raw, err := xml.Marshal(v)
	if err != nil {
		panic(err) // the types of this package always marshal
	}
	p, err := parseXML(raw, documentScope())
	if err != nil {
		panic(err) // encoding/xml writes well-formed XML
	}
	canonicalize(p.root)
	return p.root.serialize()
}

// unmarshal reads data with encoding/xml after checking that it is a
// well-formed document with every prefix declared, which encoding/xml
// does not check.
func unmarshal(data []byte, v any) error {
	if _, err := parseXML(data, documentScope()); err != nil {
		return err
	}
	return xml.Unmarshal(data, v)
}
