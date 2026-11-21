// Package confinfo is the conference information document of RFC 4575,
// application/conference-info+xml: the body of every NOTIFY of the
// conference event package. The conference focus sends it to tell the
// participants who is in the conference and in what state (TS 24.147
// 5.3.3.2, TS 24.605 4.5.2.2.2).
package confinfo

import (
	"encoding/xml"
	"errors"
	"fmt"
	"time"
)

// EventPackage is the value of the Event header field (RFC 4575 3.1).
const EventPackage = "conference"

// ContentType is the media type of the document, which every notifier
// and subscriber must support (RFC 4575 3.4).
const ContentType = "application/conference-info+xml"

// Namespace is the XML namespace of the document (RFC 4575 4.2).
const Namespace = "urn:ietf:params:xml:ns:conference-info"

// Document is the conference-info element (RFC 4575 5.2).
//
// SIMPLIFIED: RFC 4575 5.3, 5.4, 5.6.2 to 5.6.5 and 5.9. The elements
// host-info, sidebars-by-ref, sidebars-by-val, free-text, keywords,
// associated-aors, roles, languages and cascaded-focus, and elements
// and attributes from other namespaces, are not modelled. Parse skips
// them, so they are lost if a parsed document is marshalled again. The
// conference AS of this project does not produce them, and GSMA IR.92
// 2.3.3 does not ask for them.
type Document struct {
	XMLName xml.Name `xml:"urn:ietf:params:xml:ns:conference-info conference-info"`
	// Entity is the conference URI, the URI to SUBSCRIBE to.
	Entity string `xml:"entity,attr"`
	// State is empty or Full for the whole state, Partial for what
	// changed, Deleted when the conference has ended. Empty means Full,
	// the default of the schema (RFC 4575 6).
	State State `xml:"state,attr,omitempty"`
	// Version orders the documents of one subscription. It goes up for
	// every notification of a change, and by exactly one for a partial
	// one (RFC 4575 5.2). The version lives in the subscription, not in
	// the conference: see NextVersion.
	Version         uint32           `xml:"version,attr"`
	Description     *Description     `xml:"conference-description"`
	ConferenceState *ConferenceState `xml:"conference-state"`
	Users           *Users           `xml:"users"`
}

// State is the state attribute of the elements that may be sent in
// part (RFC 4575 4.4).
type State string

const (
	Full    State = "full"
	Partial State = "partial"
	Deleted State = "deleted"
)

// effective reads an absent state attribute as the schema default.
func (s State) effective() State {
	if s == "" {
		return Full
	}
	return s
}

// Description is the conference-description element (RFC 4575 5.3):
// what the conference is, set before it starts and rarely changed.
type Description struct {
	DisplayText string `xml:"display-text,omitempty"`
	Subject     string `xml:"subject,omitempty"`
	// ConfURIs are the URIs to join the conference by.
	ConfURIs *URIs `xml:"conf-uris"`
	// ServiceURIs are the URIs of services around the conference.
	ServiceURIs *URIs `xml:"service-uris"`
	// MaximumUserCount is how many users the conference takes. GSMA
	// IR.92 2.3.3 asks for it.
	MaximumUserCount *uint32         `xml:"maximum-user-count"`
	AvailableMedia   *AvailableMedia `xml:"available-media"`
}

// URIs is a list of URIs with what each is for (RFC 4575 5.3.1, 5.3.2).
type URIs struct {
	State   State `xml:"state,attr,omitempty"`
	Entries []URI `xml:"entry"`
}

type URI struct {
	URI         string  `xml:"uri"`
	DisplayText string  `xml:"display-text,omitempty"`
	Purpose     Purpose `xml:"purpose,omitempty"`
}

// Purpose says what happens when the URI is used (RFC 4575 5.3.1,
// 5.3.2).
type Purpose string

const (
	// In conf-uris.
	Participation Purpose = "participation"
	Streaming     Purpose = "streaming"
	// In service-uris.
	WebPage   Purpose = "web-page"
	Recording Purpose = "recording"
	Event     Purpose = "event"
)

// AvailableMedia lists the media streams of the conference (RFC 4575
// 5.3.4).
type AvailableMedia struct {
	Entries []AvailableMedium `xml:"entry"`
}

type AvailableMedium struct {
	// Label identifies the stream in the conference, typically the SDP
	// label attribute.
	Label       string      `xml:"label,attr"`
	DisplayText string      `xml:"display-text,omitempty"`
	Type        string      `xml:"type"`
	Status      MediaStatus `xml:"status,omitempty"`
}

// ConferenceState is the conference-state element (RFC 4575 5.5).
type ConferenceState struct {
	UserCount *uint32 `xml:"user-count"`
	Active    *bool   `xml:"active"`
	Locked    *bool   `xml:"locked"`
}

// Users is the users element (RFC 4575 5.6).
type Users struct {
	State State  `xml:"state,attr,omitempty"`
	Users []User `xml:"user"`
}

// User is one participant (RFC 4575 5.6).
type User struct {
	// Entity is the participant's authenticated identity, in IMS its
	// P-Asserted-Identity. It is the key of the user in partial
	// documents (RFC 4575 4.5).
	Entity      string     `xml:"entity,attr"`
	State       State      `xml:"state,attr,omitempty"`
	DisplayText string     `xml:"display-text,omitempty"`
	Endpoints   []Endpoint `xml:"endpoint"`
}

// Endpoint is one device or dialog of a user (RFC 4575 5.7).
type Endpoint struct {
	// Entity identifies the endpoint among those of the user, for
	// example its Contact URI or GRUU.
	Entity              string              `xml:"entity,attr"`
	State               State               `xml:"state,attr,omitempty"`
	DisplayText         string              `xml:"display-text,omitempty"`
	Referred            *Execution          `xml:"referred"`
	Status              EndpointStatus      `xml:"status,omitempty"`
	JoiningMethod       JoiningMethod       `xml:"joining-method,omitempty"`
	JoiningInfo         *Execution          `xml:"joining-info"`
	DisconnectionMethod DisconnectionMethod `xml:"disconnection-method,omitempty"`
	DisconnectionInfo   *Execution          `xml:"disconnection-info"`
	Media               []Media             `xml:"media"`
	CallInfo            *CallInfo           `xml:"call-info"`
}

// EndpointStatus is the status of an endpoint (RFC 4575 5.7.3). GSMA
// IR.92 2.3.3 asks for Connected, Disconnected and OnHold.
type EndpointStatus string

const (
	Pending       EndpointStatus = "pending"
	DialingOut    EndpointStatus = "dialing-out"
	DialingIn     EndpointStatus = "dialing-in"
	Alerting      EndpointStatus = "alerting"
	OnHold        EndpointStatus = "on-hold"
	Connected     EndpointStatus = "connected"
	MutedViaFocus EndpointStatus = "muted-via-focus"
	Disconnecting EndpointStatus = "disconnecting"
	Disconnected  EndpointStatus = "disconnected"
)

// JoiningMethod is how an endpoint came into the conference (RFC 4575
// 5.7.4).
type JoiningMethod string

const (
	// DialedIn: the endpoint sent INVITE to the focus.
	DialedIn JoiningMethod = "dialed-in"
	// DialedOut: the focus sent INVITE to the endpoint, as after a
	// REFER from another participant.
	DialedOut  JoiningMethod = "dialed-out"
	FocusOwner JoiningMethod = "focus-owner"
)

// DisconnectionMethod is how an endpoint left (RFC 4575 5.7.6).
type DisconnectionMethod string

const (
	// Departed: the endpoint sent BYE.
	Departed DisconnectionMethod = "departed"
	// Booted: the focus sent BYE, or refused the endpoint.
	Booted DisconnectionMethod = "booted"
	// Failed: the focus could not reach the endpoint.
	Failed DisconnectionMethod = "failed"
	// Busy: the endpoint answered 486.
	Busy DisconnectionMethod = "busy"
)

// Execution says when, why and by whom something happened to an
// endpoint (RFC 4575 5.7.2, 5.7.5, 5.7.7).
type Execution struct {
	When *time.Time `xml:"when"`
	// Reason is recommended to be in the form of a Reason header field
	// of RFC 3326, such as `Reason: SIP;text="Ad-hoc Invitation"`.
	Reason string `xml:"reason,omitempty"`
	By     string `xml:"by,omitempty"`
}

// Media is one media stream between the endpoint and the focus (RFC
// 4575 5.8).
type Media struct {
	ID          string `xml:"id,attr"`
	DisplayText string `xml:"display-text,omitempty"`
	// Type is the SDP media type, such as "audio".
	Type  string `xml:"type,omitempty"`
	Label string `xml:"label,omitempty"`
	// SrcID is the SSRC of the RTP stream the endpoint sends.
	SrcID  string      `xml:"src-id,omitempty"`
	Status MediaStatus `xml:"status,omitempty"`
}

// MediaStatus is the direction of a stream from the participant's point
// of view, as in SDP (RFC 4575 5.8.5).
type MediaStatus string

const (
	SendRecv MediaStatus = "sendrecv"
	SendOnly MediaStatus = "sendonly"
	RecvOnly MediaStatus = "recvonly"
	Inactive MediaStatus = "inactive"
)

// CallInfo identifies the SIP dialog between the endpoint and the focus
// (RFC 4575 5.7.9).
type CallInfo struct {
	SIP *SIPDialogID `xml:"sip"`
}

type SIPDialogID struct {
	DisplayText string `xml:"display-text,omitempty"`
	CallID      string `xml:"call-id"`
	FromTag     string `xml:"from-tag"`
	ToTag       string `xml:"to-tag"`
}

// Marshal writes the document as XML 1.0 in UTF-8, as RFC 4575 4.1
// requires, after checking it.
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

// Parse reads a document and checks it.
func Parse(b []byte) (Document, error) {
	var d Document
	if err := xml.Unmarshal(b, &d); err != nil {
		return Document{}, fmt.Errorf("confinfo: %w", err)
	}
	// The schema makes version optional, but RFC 4575 4.3 says it MUST
	// be on the root. A uint32 cannot tell absent from zero, so look
	// for the attribute itself.
	var root struct {
		Version *string `xml:"version,attr"`
	}
	if err := xml.Unmarshal(b, &root); err != nil {
		return Document{}, fmt.Errorf("confinfo: %w", err)
	}
	if root.Version == nil {
		return Document{}, errors.New("confinfo: conference-info without version")
	}
	if err := d.check(); err != nil {
		return Document{}, err
	}
	return d, nil
}
