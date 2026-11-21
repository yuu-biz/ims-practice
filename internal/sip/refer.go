package sip

import (
	"fmt"
	"strings"
)

// This file holds the header fields of call transfer: Replaces (RFC
// 3891), and Refer-To (RFC 3515), which may carry a Replaces escaped in
// its URI. Referred-By (RFC 3892 3) is a name-addr with parameters, so
// ParseNameAddr reads it and NameAddr.String writes it. Only the header
// fields are here; the REFER method and its implicit subscription are
// in referral.go.

// Replaces is the value of the Replaces header field (RFC 3891 6.1). An
// INVITE that carries it asks the UAS to put the new dialog in the
// place of the dialog it names.
//
//	Replaces: 12adf2f34456gs5;to-tag=12345;from-tag=54321;early-only
type Replaces struct {
	CallID string
	// ToTag and FromTag are the tags of the dialog to replace, as its
	// UAS has them: to-tag is that UA's local tag.
	ToTag   string
	FromTag string
	// EarlyOnly asks to replace the dialog only while it is early.
	EarlyOnly bool
}

func ParseReplaces(value string) (Replaces, error) {
	callID, rest, _ := strings.Cut(value, ";")
	r := Replaces{CallID: strings.TrimSpace(callID)}
	params := parseParams(rest)
	var hasTo, hasFrom bool
	r.ToTag, hasTo = params.Get("to-tag")
	r.FromTag, hasFrom = params.Get("from-tag")
	_, r.EarlyOnly = params.Get("early-only")
	// RFC 3891 6.1: exactly one to-tag and one from-tag.
	if r.CallID == "" || !hasTo || !hasFrom {
		return Replaces{}, fmt.Errorf("sip: malformed Replaces %q", value)
	}
	return r, nil
}

func (r Replaces) String() string {
	params := Params{{Name: "to-tag", Value: r.ToTag}, {Name: "from-tag", Value: r.FromTag}}
	if r.EarlyOnly {
		params.Set("early-only", "")
	}
	return r.CallID + params.String()
}

// Header is the Replaces header field with this value.
func (r Replaces) Header() HeaderField { return HeaderField{Name: "Replaces", Value: r.String()} }

// DialogID is the ID that the dialog to replace has at the UA that
// receives the Replaces: to-tag is compared with its local tag and
// from-tag with its remote tag (RFC 3891 3).
func (r Replaces) DialogID() DialogID {
	return DialogID{CallID: r.CallID, LocalTag: r.ToTag, RemoteTag: r.FromTag}
}

// ReferTo is the value of the Refer-To header field (RFC 3515 2.1): the
// URI that the receiver of a REFER is asked to contact.
//
//	Refer-To: <sip:carol@example.com?Replaces=12345%40example.com%3Bto-tag%3Da%3Bfrom-tag%3Db>
type ReferTo struct {
	// URI is the target without its headers part.
	URI string
	// Replaces is the Replaces header field that the URI carries for
	// the INVITE to the target, nil when it has none.
	Replaces *Replaces
	// Params are the parameters of the header field.
	Params Params
}

func ParseReferTo(value string) (ReferTo, error) {
	na, err := ParseNameAddr(value)
	if err != nil {
		return ReferTo{}, fmt.Errorf("sip: Refer-To: %w", err)
	}
	r := ReferTo{Params: na.Params}
	r.URI, _, _ = strings.Cut(na.URI, "?")
	if values := uriHeaders(na.URI, "Replaces"); len(values) > 0 {
		replaces, err := ParseReplaces(values[0])
		if err != nil {
			return ReferTo{}, fmt.Errorf("sip: Refer-To: %w", err)
		}
		r.Replaces = &replaces
	}
	return r, nil
}

// String always writes the name-addr form: without the angle brackets,
// the "?" of the headers part would not belong to the URI.
func (r ReferTo) String() string {
	uri := r.URI
	if r.Replaces != nil {
		uri = withURIHeader(uri, "Replaces", r.Replaces.String())
	}
	return NameAddr{URI: uri, Params: r.Params}.String()
}

// Header is the Refer-To header field with this value.
func (r ReferTo) Header() HeaderField { return HeaderField{Name: "Refer-To", Value: r.String()} }
