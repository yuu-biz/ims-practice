package diameter

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// A HEP packet of type "DIAMETER (JSON)" carries a Diameter message as
// JSON instead of the message itself, because HEP3 rev. 37 assigns no
// type to Diameter in its binary form. The names come from the
// dictionary, so the Lab shows "Public-Identity" where Wireshark
// does.

type messageJSON struct {
	// Command is the dictionary name without "Request" or "Answer",
	// such as "User-Authorization".
	Command       string    `json:"command"`
	Code          uint32    `json:"code"`
	Request       bool      `json:"request"`
	ApplicationID uint32    `json:"application_id"`
	HopByHop      uint32    `json:"hop_by_hop"`
	EndToEnd      uint32    `json:"end_to_end"`
	AVPs          []avpJSON `json:"avps"`
}

type avpJSON struct {
	Name   string `json:"name"`
	Code   uint32 `json:"code"`
	Vendor uint32 `json:"vendor"`
	// Value is a string, a number, or the base64 of octets that are not
	// text. A grouped AVP has AVPs instead.
	Value any       `json:"value,omitempty"`
	AVPs  []avpJSON `json:"avps,omitempty"`
}

// EncodeJSON renders m as JSON for a HEP packet. Call LoadDictionary
// first, or the Cx commands and AVPs have no names.
func EncodeJSON(m *diam.Message) ([]byte, error) {
	appID := m.Header.ApplicationID
	out := messageJSON{
		Code:          m.Header.CommandCode,
		Request:       isRequest(m),
		ApplicationID: appID,
		HopByHop:      m.Header.HopByHopID,
		EndToEnd:      m.Header.EndToEndID,
		AVPs:          avpsJSON(appID, m.AVP),
	}
	if cmd, err := dict.Default.FindCommand(appID, m.Header.CommandCode); err == nil {
		out.Command = cmd.Name
	}
	return json.Marshal(out)
}

func avpsJSON(appID uint32, avps []*diam.AVP) []avpJSON {
	out := make([]avpJSON, 0, len(avps))
	for _, a := range avps {
		j := avpJSON{Code: a.Code, Vendor: a.VendorID}
		if d, err := dict.Default.FindAVPByCode(appID, a.Code, a.VendorID); err == nil {
			j.Name = d.Name
		}
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			j.AVPs = avpsJSON(appID, g.AVP)
		} else if digits, ok := tbcdJSON(a); ok {
			j.Value = digits
		} else {
			j.Value = valueJSON(a.Data)
		}
		out = append(out, j)
	}
	return out
}

// avpCodeMSISDN is the MSISDN AVP of Sh (TS 29.329 6.3.2), an
// OctetString that holds a TBCD string.
const avpCodeMSISDN = 701

// tbcdJSON shows the digits of an MSISDN AVP, which would otherwise be
// base64, since its octets are not text. Octets that are not TBCD stay
// base64.
func tbcdJSON(a *diam.AVP) (string, bool) {
	octets, ok := a.Data.(datatype.OctetString)
	if !ok || a.Code != avpCodeMSISDN || a.VendorID != VendorID3GPP {
		return "", false
	}
	digits, err := DecodeTBCD([]byte(octets))
	return digits, err == nil
}

// valueJSON switches on the Go type of the data rather than on the
// dictionary type, because a message built here and a message read off
// the wire hold the same types.
func valueJSON(data datatype.Type) any {
	switch v := data.(type) {
	case datatype.Unsigned32:
		return uint32(v)
	case datatype.Unsigned64:
		return uint64(v)
	case datatype.Integer32:
		return int32(v)
	case datatype.Integer64:
		return int64(v)
	case datatype.Enumerated:
		return int32(v)
	case datatype.Float32:
		return float32(v)
	case datatype.Float64:
		return float64(v)
	case datatype.UTF8String:
		return string(v)
	case datatype.DiameterIdentity:
		return string(v)
	case datatype.DiameterURI:
		return string(v)
	case datatype.IPFilterRule:
		return string(v)
	case datatype.QoSFilterRule:
		return string(v)
	case datatype.Address:
		// An address family other than IPv4 and IPv6 keeps its family
		// prefix in v and ends up as base64 below.
		if len(v) == net.IPv4len || len(v) == net.IPv6len {
			return net.IP(v).String()
		}
	case datatype.IPv4:
		return net.IP(v).String()
	case datatype.IPv6:
		return net.IP(v).String()
	case datatype.Time:
		return time.Time(v).UTC().Format(time.RFC3339)
	case datatype.OctetString:
		return octetsJSON([]byte(v))
	}
	// Unknown AVPs, and AVPs that failed to decode, keep their raw
	// octets.
	return base64.StdEncoding.EncodeToString(data.Serialize())
}

// octetsJSON keeps an OctetString that holds text, such as a
// Visited-Network-Identifier, readable.
func octetsJSON(b []byte) string {
	if isText(b) {
		return string(b)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func isText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if !unicode.IsPrint(r) && r != '\t' && r != '\r' && r != '\n' {
			return false
		}
	}
	return true
}

// SessionID is the value of the Session-Id AVP, or "" for a message
// without one, such as a CER or a DWR.
func SessionID(m *diam.Message) string {
	a, err := m.FindAVP(avp.SessionID, 0)
	if err != nil {
		return ""
	}
	if s, ok := a.Data.(datatype.UTF8String); ok {
		return string(s)
	}
	return ""
}
