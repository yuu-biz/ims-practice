// Package reginfo is the registration information document of RFC 3680
// 5, application/reginfo+xml: the body of every NOTIFY of the reg event
// package. It tells, for each address-of-record, whether it is
// registered, and the state of each of its contacts.
package reginfo

import (
	"encoding/xml"
	"errors"
	"fmt"
)

// ContentType is the media type of the document (RFC 3680 8.2).
const ContentType = "application/reginfo+xml"

// Namespace is the XML namespace of the document (RFC 3680 5.1).
const Namespace = "urn:ietf:params:xml:ns:reginfo"

// Document is the reginfo element.
type Document struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:reginfo reginfo"`
	// Version starts at 0 and goes up by one with each document sent
	// within a subscription, so that the subscriber can order them.
	Version uint32 `xml:"version,attr"`
	// State says whether the document holds the full state or only what
	// changed.
	State         DocumentState  `xml:"state,attr"`
	Registrations []Registration `xml:"registration"`
}

type DocumentState string

const (
	Full    DocumentState = "full"
	Partial DocumentState = "partial"
)

// Registration is the state of one address-of-record, in IMS a public
// user identity.
type Registration struct {
	AOR string `xml:"aor,attr"`
	// ID stays the same for the address-of-record across the documents
	// of a subscription.
	ID       string    `xml:"id,attr"`
	State    State     `xml:"state,attr"`
	Contacts []Contact `xml:"contact"`
}

// State is the state of a registration (RFC 3680 4.7.1 Figure 1) or of
// a contact (Figure 2). A contact is never in Init.
type State string

const (
	Init       State = "init"
	Active     State = "active"
	Terminated State = "terminated"
)

// Contact is one contact bound to the address-of-record.
type Contact struct {
	// ID stays the same for the same contact URI (SHOULD in RFC 3680
	// 5.1).
	ID    string `xml:"id,attr"`
	State State  `xml:"state,attr"`
	// Event is what moved the contact into its state.
	Event Event `xml:"event,attr"`
	// Expires is the number of seconds left on the binding. It MUST be
	// present with Shortened.
	Expires uint64 `xml:"expires,attr,omitempty"`
	// RetryAfter MUST be present with Probation.
	RetryAfter         uint64 `xml:"retry-after,attr,omitempty"`
	DurationRegistered uint64 `xml:"duration-registered,attr,omitempty"`
	Q                  string `xml:"q,attr,omitempty"`
	// CallID and CSeq are those of the REGISTER that last changed the
	// contact.
	CallID        string         `xml:"callid,attr,omitempty"`
	CSeq          uint32         `xml:"cseq,attr,omitempty"`
	URI           string         `xml:"uri"`
	DisplayName   string         `xml:"display-name,omitempty"`
	UnknownParams []UnknownParam `xml:"unknown-param"`
}

// UnknownParam is a Contact header field parameter that RFC 3261 does
// not define, such as a feature tag of RFC 3840.
type UnknownParam struct {
	Name  string `xml:"name,attr"`
	Value string `xml:",chardata"`
}

// Event is the event attribute of a contact (RFC 3680 4.7.1).
type Event string

const (
	// Registered: a REGISTER bound the contact.
	Registered Event = "registered"
	// Created: the registrar bound it by other means.
	Created Event = "created"
	// Refreshed: a REGISTER renewed it.
	Refreshed Event = "refreshed"
	// Shortened: the registrar cut its expiry, so that the UE registers
	// again soon.
	Shortened Event = "shortened"
	// Expired: it was not refreshed in time.
	Expired Event = "expired"
	// Deactivated: the registrar removed it and expects the UE to
	// register again.
	Deactivated Event = "deactivated"
	// Probation: as Deactivated, but the UE is to wait RetryAfter first.
	Probation Event = "probation"
	// Unregistered: a REGISTER with an expiry of zero removed it.
	Unregistered Event = "unregistered"
	// Rejected: the registrar removed it, and registering again will not
	// help.
	Rejected Event = "rejected"
)

// Marshal writes the document as XML 1.0 in UTF-8, as RFC 3680 5.1
// requires.
func (d Document) Marshal() ([]byte, error) {
	if err := d.check(); err != nil {
		return nil, err
	}
	body, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// Parse reads a document and checks the attributes that RFC 3680 5.1
// requires. Elements and attributes from other namespaces are ignored,
// as 5.1 asks.
func Parse(b []byte) (Document, error) {
	var d Document
	if err := xml.Unmarshal(b, &d); err != nil {
		return Document{}, fmt.Errorf("reginfo: %w", err)
	}
	if err := d.check(); err != nil {
		return Document{}, err
	}
	return d, nil
}

func (d Document) check() error {
	if d.State != Full && d.State != Partial {
		return fmt.Errorf("reginfo: reginfo state %q", d.State)
	}
	for _, r := range d.Registrations {
		if r.AOR == "" || r.ID == "" {
			return errors.New("reginfo: registration without aor or id")
		}
		switch r.State {
		case Init, Active, Terminated:
		default:
			return fmt.Errorf("reginfo: registration %s has state %q", r.AOR, r.State)
		}
		for _, c := range r.Contacts {
			if err := c.check(); err != nil {
				return fmt.Errorf("reginfo: registration %s: %w", r.AOR, err)
			}
		}
	}
	return nil
}

func (c Contact) check() error {
	if c.ID == "" || c.URI == "" {
		return errors.New("contact without id or uri")
	}
	if c.State != Active && c.State != Terminated {
		return fmt.Errorf("contact %s has state %q", c.URI, c.State)
	}
	switch c.Event {
	case Registered, Created, Refreshed, Shortened, Expired, Deactivated, Probation, Unregistered, Rejected:
	default:
		return fmt.Errorf("contact %s has event %q", c.URI, c.Event)
	}
	if c.Event == Shortened && c.Expires == 0 {
		return fmt.Errorf("contact %s is shortened without expires", c.URI)
	}
	if c.Event == Probation && c.RetryAfter == 0 {
		return fmt.Errorf("contact %s is on probation without retry-after", c.URI)
	}
	return nil
}
