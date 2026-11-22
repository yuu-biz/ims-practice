package diameter

import (
	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// This file holds the parts that every 3GPP Diameter application of this
// project builds its commands from: the routing AVPs each request
// starts with, and the pieces of a vendor specific application. What an
// application itself defines, such as its own AVPs, commands and
// Experimental-Result-Codes, stays in the package of that application.

// AuthSessionStateNoStateMaintained is NO_STATE_MAINTAINED, the
// Auth-Session-State of a session that ends with its answer, so that no
// node keeps session state (RFC 6733 8.11).
const AuthSessionStateNoStateMaintained = 1

// Header is the routing part every request starts with. DestinationHost
// is empty in a request that names no node, which is then for any node
// of the realm that serves the application (RFC 6733 6.1). The avp tags
// read the same AVPs back: a parse function embeds Header in the struct
// it unmarshals the request into.
type Header struct {
	SessionID        string `avp:"Session-Id"`
	OriginHost       string `avp:"Origin-Host"`
	OriginRealm      string `avp:"Origin-Realm"`
	DestinationHost  string `avp:"Destination-Host,omitempty"`
	DestinationRealm string `avp:"Destination-Realm"`
}

// NewHeader fills in the identity of node and a new Session-Id.
func NewHeader(node *Node, destinationHost, destinationRealm string) Header {
	id := node.Identity()
	return Header{
		SessionID:        node.NewSessionID(),
		OriginHost:       id.OriginHost,
		OriginRealm:      id.OriginRealm,
		DestinationHost:  destinationHost,
		DestinationRealm: destinationRealm,
	}
}

// The Add methods below write one part of the header each. The command
// format of each application decides their order, and which of its own
// AVPs go between them.

// AddSessionID writes the Session-Id that names the session a request
// belongs to (RFC 6733 8.8).
func (h Header) AddSessionID(m *diam.Message) {
	m.NewAVP(avp.SessionID, avp.Mbit, 0, datatype.UTF8String(h.SessionID))
}

// AddOrigin writes the Origin-Host and Origin-Realm of the sender.
func (h Header) AddOrigin(m *diam.Message) {
	Identity{OriginHost: h.OriginHost, OriginRealm: h.OriginRealm}.AddOrigin(m)
}

// AddDestinationHost writes Destination-Host, and nothing at all when
// the request names no node.
func (h Header) AddDestinationHost(m *diam.Message) {
	if h.DestinationHost != "" {
		m.NewAVP(avp.DestinationHost, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationHost))
	}
}

// AddDestinationRealm writes the Destination-Realm, which every request
// of these applications names.
func (h Header) AddDestinationRealm(m *diam.Message) {
	m.NewAVP(avp.DestinationRealm, avp.Mbit, 0, datatype.DiameterIdentity(h.DestinationRealm))
}

// NewRequest starts a request of application with the R and P bits set
// (RFC 6733 3). go-diameter picks the Hop-by-Hop and End-to-End
// identifiers.
func NewRequest(application, command uint32) *diam.Message {
	return diam.NewMessage(command, diam.RequestFlag|diam.ProxiableFlag, application, 0, 0, dict.Default)
}

// NewAnswer starts the answer to request: the same Command-Code and
// Application-Id, the Hop-by-Hop and End-to-End identifiers that pair
// it with the request, the P bit the request carried, and the request's
// Session-Id (RFC 6733 6.2). The AVPs of the answer format of each
// application follow.
func NewAnswer(request *diam.Message) *diam.Message {
	m := diam.NewMessage(request.Header.CommandCode, request.Header.CommandFlags&diam.ProxiableFlag,
		request.Header.ApplicationID, request.Header.HopByHopID, request.Header.EndToEndID, dict.Default)
	if sid, err := request.FindAVP(avp.SessionID, 0); err == nil {
		m.AddAVP(sid)
	}
	return m
}

// VendorAVP is an AVP of 3GPP: the Vendor-Id is VendorID3GPP, and flags
// say whether M is set beside V. Which AVP sets what is in the table of
// AVP codes of each application.
func VendorAVP(code uint32, flags uint8, data datatype.Type) *diam.AVP {
	return diam.NewAVP(code, flags, VendorID3GPP, data)
}

// AddVendorSpecificApplicationID writes the Vendor-Specific-Application-Id
// of a 3GPP application. It is kept even though RFC 6733 deprecated it,
// because the command formats of TS 29.229 6.1 and TS 29.329 6.1 still
// require it (both say so in a NOTE).
func AddVendorSpecificApplicationID(m *diam.Message, application uint32) {
	m.NewAVP(avp.VendorSpecificApplicationID, avp.Mbit, 0, &diam.GroupedAVP{AVP: []*diam.AVP{
		diam.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP)),
		diam.NewAVP(avp.AuthApplicationID, avp.Mbit, 0, datatype.Unsigned32(application)),
	}})
}
