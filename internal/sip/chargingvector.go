package sip

import "strings"

// ChargingVector is the value of the P-Charging-Vector header field
// (RFC 7315 4.6, 5.6, TS 24.229 7.2A.5). The nodes of one network, and
// of networks that trust each other, pass it on with the requests and
// their responses, so that the charging records that each node writes
// can be put together afterwards.
//
//	P-Charging-Vector: icid-value=a84b4c76e66710;orig-ioi=ims.mnc001.mcc001.3gppnetwork.org;term-ioi=ims.mnc002.mcc001.3gppnetwork.org
type ChargingVector struct {
	// ICID is the icid-value, the IMS charging identity: one value for
	// the whole of a dialog or a standalone transaction, which the first
	// node that sees the request generates.
	ICID string
	// OrigIOI and TermIOI are the orig-ioi and the term-ioi, the inter
	// operator identifiers of the network that sent the request and of
	// the network that sent the response, or "" when the value has none.
	OrigIOI string
	TermIOI string
	// Params are the other parameters, such as transit-ioi.
	Params Params
}

// ParseChargingVector reads a P-Charging-Vector value. Its parameters
// have no value in front of them.
func ParseChargingVector(value string) ChargingVector {
	var c ChargingVector
	for _, p := range ParseParams(value) {
		switch strings.ToLower(p.Name) {
		case "icid-value":
			c.ICID = p.Value
		case "orig-ioi":
			c.OrigIOI = p.Value
		case "term-ioi":
			c.TermIOI = p.Value
		default:
			c.Params = append(c.Params, p)
		}
	}
	return c
}

// ChargingVectorOf is the P-Charging-Vector of m, and whether m has one
// with the icid-value that every value must have.
func ChargingVectorOf(m *Message) (ChargingVector, bool) {
	c := ParseChargingVector(m.Get("P-Charging-Vector"))
	return c, c.ICID != ""
}

// String writes the icid-value first, as the syntax has it.
func (c ChargingVector) String() string {
	params := Params{{Name: "icid-value", Value: c.ICID}}
	if c.OrigIOI != "" {
		params.Set("orig-ioi", c.OrigIOI)
	}
	if c.TermIOI != "" {
		params.Set("term-ioi", c.TermIOI)
	}
	return strings.TrimPrefix(append(params, c.Params...).String(), ";")
}

// Response is the P-Charging-Vector of a response to a request that
// carried c: the icid-value and the orig-ioi of the request, and
// termIOI, which names the network of the node that sends the response.
func (c ChargingVector) Response(termIOI string) ChargingVector {
	return ChargingVector{ICID: c.ICID, OrigIOI: c.OrigIOI, TermIOI: termIOI}
}
