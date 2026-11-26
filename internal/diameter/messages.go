package diameter

import (
	"fmt"
	"net"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"
	"github.com/fiorix/go-diameter/v4/diam/dict"
)

// This file builds and reads the base protocol messages of RFC 6733 5.3
// to 5.5 AVP by AVP, in the order of each message format. The AVP flags
// follow the table in RFC 6733 4.5.

const (
	// RFC 6733 5.3.3: zero means "no vendor"; this project is not one.
	vendorIDNone uint32 = 0
	productName         = "ims-practice"
)

// Identity is how this node names itself in every message.
type Identity struct {
	OriginHost  string
	OriginRealm string
}

// AddOrigin writes the Origin-Host and Origin-Realm that every request
// and answer carries.
func (i Identity) AddOrigin(m *diam.Message) {
	m.NewAVP(avp.OriginHost, avp.Mbit, 0, datatype.DiameterIdentity(i.OriginHost))
	m.NewAVP(avp.OriginRealm, avp.Mbit, 0, datatype.DiameterIdentity(i.OriginRealm))
}

// capabilitiesExchangeRequest is the CER of RFC 6733 5.3.1.
func capabilitiesExchangeRequest(local Identity, applications []uint32, hostIP net.IP, ids MessageIDs) *diam.Message {
	m := diam.NewMessage(diam.CapabilitiesExchange, diam.RequestFlag, 0, ids.HopByHop, ids.EndToEnd, dict.Default)
	addCapabilities(m, local, applications, hostIP)
	return m
}

// capabilitiesExchangeAnswer is the CEA of RFC 6733 5.3.2. It carries
// the same capabilities as a CER, after the Result-Code.
func capabilitiesExchangeAnswer(local Identity, applications []uint32, hostIP net.IP, request MessageIDs, resultCode uint32) *diam.Message {
	m := diam.NewMessage(diam.CapabilitiesExchange, 0, 0, request.HopByHop, request.EndToEnd, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(resultCode))
	addCapabilities(m, local, applications, hostIP)
	return m
}

func addCapabilities(m *diam.Message, local Identity, applications []uint32, hostIP net.IP) {
	local.AddOrigin(m)
	m.NewAVP(avp.HostIPAddress, avp.Mbit, 0, datatype.Address(hostIP))
	m.NewAVP(avp.VendorID, avp.Mbit, 0, datatype.Unsigned32(vendorIDNone))
	m.NewAVP(avp.ProductName, 0, 0, datatype.UTF8String(productName))
	// The Cx and Sh AVPs are 3GPP vendor-specific (RFC 6733 5.3.6).
	m.NewAVP(avp.SupportedVendorID, avp.Mbit, 0, datatype.Unsigned32(VendorID3GPP))
	// Only the applications the node's roles serve are advertised, so a
	// capture shows which node speaks what. Both Cx and Sh are
	// 3GPP applications.
	for _, id := range applications {
		AddVendorSpecificApplicationID(m, id)
	}
}

// deviceWatchdogRequest is the DWR of RFC 6733 5.5.1.
func deviceWatchdogRequest(local Identity, ids MessageIDs) *diam.Message {
	m := diam.NewMessage(diam.DeviceWatchdog, diam.RequestFlag, 0, ids.HopByHop, ids.EndToEnd, dict.Default)
	local.AddOrigin(m)
	return m
}

// deviceWatchdogAnswer is the DWA of RFC 6733 5.5.2.
func deviceWatchdogAnswer(local Identity, request MessageIDs) *diam.Message {
	m := diam.NewMessage(diam.DeviceWatchdog, 0, 0, request.HopByHop, request.EndToEnd, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(DiameterSuccess))
	local.AddOrigin(m)
	return m
}

// disconnectPeerRequest is the DPR of RFC 6733 5.4.1.
func disconnectPeerRequest(local Identity, ids MessageIDs, cause DisconnectCause) *diam.Message {
	m := diam.NewMessage(diam.DisconnectPeer, diam.RequestFlag, 0, ids.HopByHop, ids.EndToEnd, dict.Default)
	local.AddOrigin(m)
	m.NewAVP(avp.DisconnectCause, avp.Mbit, 0, datatype.Enumerated(cause))
	return m
}

// disconnectPeerAnswer is the DPA of RFC 6733 5.4.2.
func disconnectPeerAnswer(local Identity, request MessageIDs) *diam.Message {
	m := diam.NewMessage(diam.DisconnectPeer, 0, 0, request.HopByHop, request.EndToEnd, dict.Default)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(DiameterSuccess))
	local.AddOrigin(m)
	return m
}

// CommandUnsupportedAnswer is the protocol error answer of RFC 6733 7.2
// for a request this node has no handler for: the E bit is set and the
// Result-Code is DIAMETER_COMMAND_UNSUPPORTED (7.1.3).
func CommandUnsupportedAnswer(local Identity, request *diam.Message) *diam.Message {
	m := NewAnswer(request)
	// Where the answer of an application carries the P bit of its
	// request, this one carries the E bit alone.
	m.Header.CommandFlags = diam.ErrorFlag
	local.AddOrigin(m)
	m.NewAVP(avp.ResultCode, avp.Mbit, 0, datatype.Unsigned32(DiameterCommandUnsupported))
	return m
}

func isRequest(m *diam.Message) bool {
	return m.Header.CommandFlags&diam.RequestFlag != 0
}

func messageIDs(m *diam.Message) MessageIDs {
	return MessageIDs{HopByHop: m.Header.HopByHopID, EndToEnd: m.Header.EndToEndID}
}

// commandName is the name of m in the logs and in the command label of
// the metrics: the short name that the dictionary gives its command,
// with R or A after it, such as "CER" or "DWA". A command that the
// dictionary gives no short name, as a command of this project's own
// has none, goes by its full name with "-Request" or "-Answer" after
// it, such as "Ready-For-SM-Request".
func commandName(m *diam.Message) string {
	suffix, long := "A", "-Answer"
	if isRequest(m) {
		suffix, long = "R", "-Request"
	}
	cmd, err := dict.Default.FindCommand(m.Header.ApplicationID, m.Header.CommandCode)
	switch {
	case err != nil:
		return fmt.Sprintf("command %d (%s)", m.Header.CommandCode, suffix)
	case cmd.Short == "":
		return cmd.Name + long
	}
	return cmd.Short + suffix
}

// CommandShortName is the short name that the dictionary gives a
// command, such as "UA" for User-Authorization. With R or A after it,
// it names the request or the answer: "UAR", "UAA". False for a
// command the dictionary does not have, and for one that it gives no
// short name, such as a command of this project's own.
func CommandShortName(applicationID, code uint32) (string, bool) {
	cmd, err := dict.Default.FindCommand(applicationID, code)
	if err != nil || cmd.Short == "" {
		return "", false
	}
	return cmd.Short, true
}

// parseEvent turns a received message into a state machine event. A CER
// is parsed as RConnCER; only a new connection should ever start with
// one, and the state machine treats a later CER like any other message.
func parseEvent(m *diam.Message) Event {
	request := isRequest(m)
	switch m.Header.CommandCode {
	case diam.CapabilitiesExchange:
		if request {
			return RConnCER{
				Request:        messageIDs(m),
				OriginHost:     originHost(m),
				ApplicationIDs: applicationIDs(m),
			}
		}
		return RcvCEA{
			ResultCode:     resultCode(m),
			OriginHost:     originHost(m),
			ApplicationIDs: applicationIDs(m),
		}
	case diam.DeviceWatchdog:
		if request {
			return RcvDWR{Request: messageIDs(m)}
		}
		return RcvDWA{}
	case diam.DisconnectPeer:
		if request {
			return RcvDPR{Request: messageIDs(m), Cause: disconnectCause(m)}
		}
		return RcvDPA{}
	}
	return RcvMessage{Message: m}
}

func originHost(m *diam.Message) string {
	a, err := m.FindAVP(avp.OriginHost, 0)
	if err != nil {
		return ""
	}
	host, _ := a.Data.(datatype.DiameterIdentity)
	return string(host)
}

func originRealm(m *diam.Message) string {
	a, err := m.FindAVP(avp.OriginRealm, 0)
	if err != nil {
		return ""
	}
	realm, _ := a.Data.(datatype.DiameterIdentity)
	return string(realm)
}

// destinationHost is the Destination-Host of a request, or "" for a
// request that names none and is for any node of its realm that serves
// the application (RFC 6733 6.1).
func destinationHost(m *diam.Message) string {
	a, err := m.FindAVP(avp.DestinationHost, 0)
	if err != nil {
		return ""
	}
	host, _ := a.Data.(datatype.DiameterIdentity)
	return string(host)
}

// resultCode returns 0 when the AVP is missing, which no caller treats
// as success.
func resultCode(m *diam.Message) uint32 {
	a, err := m.FindAVP(avp.ResultCode, 0)
	if err != nil {
		return 0
	}
	code, _ := a.Data.(datatype.Unsigned32)
	return uint32(code)
}

// disconnectCause defaults to BUSY when the AVP is missing, so that a
// malformed DPR never invites a reconnect.
func disconnectCause(m *diam.Message) DisconnectCause {
	a, err := m.FindAVP(avp.DisconnectCause, 0)
	if err != nil {
		return Busy
	}
	cause, ok := a.Data.(datatype.Enumerated)
	if !ok {
		return Busy
	}
	return DisconnectCause(cause)
}

// applicationIDs collects every Auth-Application-Id and
// Acct-Application-Id, at the top level and inside each
// Vendor-Specific-Application-Id (RFC 6733 5.3).
func applicationIDs(m *diam.Message) []uint32 {
	var ids []uint32
	collect := func(avps []*diam.AVP) {
		for _, a := range avps {
			if a.Code != avp.AuthApplicationID && a.Code != avp.AcctApplicationID {
				continue
			}
			if id, ok := a.Data.(datatype.Unsigned32); ok {
				ids = append(ids, uint32(id))
			}
		}
	}
	collect(m.AVP)
	for _, a := range m.AVP {
		if a.Code != avp.VendorSpecificApplicationID {
			continue
		}
		if g, ok := a.Data.(*diam.GroupedAVP); ok {
			collect(g.AVP)
		}
	}
	return ids
}
