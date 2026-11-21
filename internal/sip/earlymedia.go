package sip

import "strings"

// Direction parameters of P-Early-Media (RFC 5009 8). They read like
// the SDP direction attributes, seen from the UAS: sendonly asks to
// authorize early media from the UAS towards the UAC, which is the
// direction of a ringback tone or an announcement.
const (
	EarlyMediaSendrecv = "sendrecv"
	EarlyMediaSendonly = "sendonly"
	EarlyMediaRecvonly = "recvonly"
	EarlyMediaInactive = "inactive"
)

// PEarlyMedia is the value of the P-Early-Media header field (RFC 5009
// 8, 9). In an INVITE it says that the UAC or a proxy on the way
// recognizes the header field (Supported). In a message towards the
// UAC, an 18x or a PRACK or UPDATE with its 2xx, it asks to authorize
// early media, or takes the authorization back.
//
//	P-Early-Media: supported
//	P-Early-Media: sendonly, gated
type PEarlyMedia struct {
	Supported bool
	// Directions has one direction parameter per media line of the SDP,
	// in order. It is empty when the value asks for no authorization.
	Directions []string
	// Gated tells that a network entity on the way to the UAS gates the
	// early media already.
	Gated bool
}

// ParsePEarlyMedia reads the parameters of P-Early-Media, as
// Message.GetAll("P-Early-Media") gives them. Parameters it does not
// know are discarded, as RFC 5009 8 asks.
func ParsePEarlyMedia(params []string) PEarlyMedia {
	var p PEarlyMedia
	for _, param := range params {
		switch param = strings.ToLower(strings.TrimSpace(param)); param {
		case "supported":
			p.Supported = true
		case "gated":
			p.Gated = true
		case EarlyMediaSendrecv, EarlyMediaSendonly, EarlyMediaRecvonly, EarlyMediaInactive:
			p.Directions = append(p.Directions, param)
		}
	}
	return p
}

// String writes the direction parameters first and gated after them,
// the order that RFC 5009 8 asks for.
func (p PEarlyMedia) String() string {
	params := append([]string(nil), p.Directions...)
	if p.Gated {
		params = append(params, "gated")
	}
	if p.Supported {
		params = append(params, "supported")
	}
	return strings.Join(params, ", ")
}

// Header is the P-Early-Media header field with this value.
func (p PEarlyMedia) Header() HeaderField {
	return HeaderField{Name: "P-Early-Media", Value: p.String()}
}

// Direction is the direction parameter for the media line at index in
// the SDP, counted from 0: its own parameter, or the last one when
// there are fewer parameters than media lines (RFC 5009 8). It reports
// false when the value has no direction parameter at all; such a value
// is no request to authorize early media, and the authorization that
// the dialog had before stays as it was.
func (p PEarlyMedia) Direction(index int) (string, bool) {
	if len(p.Directions) == 0 || index < 0 {
		return "", false
	}
	return p.Directions[min(index, len(p.Directions)-1)], true
}
