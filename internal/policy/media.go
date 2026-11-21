package policy

import "net/netip"

// MediaComponent is the service information of one media component of
// an IMS session, or of its signalling (TS 29.214 5.3.16, the
// Media-Component-Description AVP). The P-CSCF derives it from one "m="
// line of the SDP (TS 29.213 6.2 Table 6.2.1).
type MediaComponent struct {
	// Number is the position of the "m=" line in the SDP, counting from
	// 1. Number 0 is the AF signalling (TS 29.214 5.3.17).
	Number uint32
	Type   MediaType
	// FlowStatus is NotSupplied when the status has not changed since
	// earlier service information (TS 29.214 5.3.16).
	FlowStatus FlowStatus
	// The bandwidths are in bit/s, including IP, UDP and RTP overhead
	// (TS 29.214 5.3.14, 5.3.15, 5.3.20, 5.3.21). Nil means not supplied.
	MaxRequestedBandwidthUL *uint32
	MaxRequestedBandwidthDL *uint32
	RRBandwidth             *uint32
	RSBandwidth             *uint32
	// Codecs holds at most two codec descriptions: one from the offer and
	// one from the answer (TS 29.214 5.3.16).
	Codecs        []CodecData
	SubComponents []MediaSubComponent
}

// MediaSubComponent is one IP flow, or a pair of flows in opposite
// directions, of a media component (TS 29.214 5.3.18): RTP, RTCP, or
// both when they share a port (rtcp-mux).
type MediaSubComponent struct {
	// FlowNumber counts the flows of the media component from 1, in the
	// order of their downlink destination ports (TS 29.213 Table 6.2.2).
	// Flow 0 of component 0 stands for all of the AF signalling (TS
	// 29.214 4.4.5).
	FlowNumber uint32
	// Flows has at most one uplink and one downlink flow.
	Flows []FlowDescription
	Usage FlowUsage
}

// MediaType is the Media-Type AVP (TS 29.214 5.3.19): the media of the
// "m=" line.
type MediaType int

const (
	// NoMediaType leaves the type out, as for the AF signalling (TS
	// 29.214 4.4.5).
	NoMediaType MediaType = iota
	Audio
	Video
	Data
	Application
	Control
	Text
	Message
	OtherMedia
)

// FlowStatus says which directions of a media component may pass (TS
// 29.214 5.3.11).
type FlowStatus int

const (
	// NotSupplied leaves the status as earlier service information set
	// it.
	NotSupplied FlowStatus = iota
	EnabledUplink
	EnabledDownlink
	Enabled
	Disabled
	// Removed: the flows are gone, as when the port in "m=" is 0.
	Removed
)

// FlowUsage says what an IP flow carries (TS 29.214 5.3.12).
type FlowUsage int

const (
	NoInformation FlowUsage = iota
	RTCP
	AFSignalling
)

// Direction is the direction of an IP flow seen from the UE (TS 29.214
// 5.3.8): uplink from the UE, downlink to it.
type Direction int

const (
	Uplink Direction = iota + 1
	Downlink
)

// FlowDescription is one IP flow (TS 29.214 5.3.8). An invalid address
// or a zero port is a wildcard. Source ports are never given (TS 29.213
// Table 6.2.2).
type FlowDescription struct {
	Direction       Direction
	Protocol        uint8 // IP protocol number: 17 for UDP, 6 for TCP
	Source          netip.Addr
	Destination     netip.Addr
	DestinationPort uint16
}

// CodecData is the codec information of a media component (TS 29.214
// 5.3.7): the SDP lines of one "m=" line in an offer or an answer.
type CodecData struct {
	// Direction is Uplink when the SDP came from the UE and Downlink
	// when it went to the UE.
	Direction Direction
	// Answer is true for lines from the SDP answer, false for the offer.
	Answer bool
	// Lines are the "m=" line and the "a=" and "b=" lines that follow it,
	// such as "m=audio 49000 RTP/AVP 0".
	Lines []string
}
