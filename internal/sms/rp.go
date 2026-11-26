package sms

import (
	"errors"
	"fmt"
)

// Direction is the way an RP message travels.
type Direction uint8

const (
	MSToNetwork Direction = iota
	NetworkToMS
)

// RPMessage is one message of the relay layer (TS 24.011 7.3):
// RPDataMSToNetwork, RPDataNetworkToMS, RPAck, RPError or RPSMMA. It is
// the whole body of the SIP MESSAGE (TS 24.341).
type RPMessage interface {
	MTI() RPMTI
	Marshal() ([]byte, error)
}

// RPMTI is the RP Message Type Indicator, bits 3..1 of the first octet
// (TS 24.011 8.2.2, table 8.3; TS 24.011 numbers bits from 1, TS 23.040
// from 0). Every value names both the message and its direction.
type RPMTI uint8

const (
	RPMTIDataMSToNetwork  RPMTI = 0b000
	RPMTIDataNetworkToMS  RPMTI = 0b001
	RPMTIAckMSToNetwork   RPMTI = 0b010
	RPMTIAckNetworkToMS   RPMTI = 0b011
	RPMTIErrorMSToNetwork RPMTI = 0b100
	RPMTIErrorNetworkToMS RPMTI = 0b101
	RPMTISMMA             RPMTI = 0b110 // MS to network only
)

// maxTPDU is the largest TPDU that RP-User-Data holds (TS 24.011
// 8.2.5.3, figure 8.7).
const maxTPDU = 232

// rpUserDataIEI is the IEI of RP-User-Data where it is optional, in
// RP-ACK and RP-ERROR (TS 24.011 table 7.7, 7.8).
const rpUserDataIEI = 0x41

// RPDataMSToNetwork is RP-DATA from the MS: an SMS-SUBMIT on its way to
// the SC (TS 24.011 7.3.1.2). Its RP-Originator Address is always
// empty, so the struct has no field for it.
type RPDataMSToNetwork struct {
	MessageReference uint8 // links the RP-ACK or RP-ERROR to this message (8.2.3)
	// DestinationAddress is the address of the SC (8.2.5.2).
	DestinationAddress Address
	UserData           []byte // the TPDU
}

func (RPDataMSToNetwork) MTI() RPMTI { return RPMTIDataMSToNetwork }

// Marshal writes table 7.5 of TS 24.011.
func (m RPDataMSToNetwork) Marshal() ([]byte, error) {
	b := []byte{byte(RPMTIDataMSToNetwork), m.MessageReference, 0} // RP-OA has length 0
	b, err := appendRPAddress(b, m.DestinationAddress)
	if err != nil {
		return nil, fmt.Errorf("sms: RP-DATA RP-DA: %w", err)
	}
	return appendRPData(b, m.UserData)
}

// RPDataNetworkToMS is RP-DATA from the network: an SMS-DELIVER or
// SMS-STATUS-REPORT for the MS (TS 24.011 7.3.1.1). Its RP-Destination
// Address is always empty, so the struct has no field for it.
type RPDataNetworkToMS struct {
	MessageReference uint8
	// OriginatorAddress is the address of the SC (8.2.5.1).
	OriginatorAddress Address
	UserData          []byte
}

func (RPDataNetworkToMS) MTI() RPMTI { return RPMTIDataNetworkToMS }

// Marshal writes table 7.4 of TS 24.011.
func (m RPDataNetworkToMS) Marshal() ([]byte, error) {
	b, err := appendRPAddress([]byte{byte(RPMTIDataNetworkToMS), m.MessageReference}, m.OriginatorAddress)
	if err != nil {
		return nil, fmt.Errorf("sms: RP-DATA RP-OA: %w", err)
	}
	return appendRPData(append(b, 0), m.UserData) // RP-DA has length 0
}

func appendRPData(b, tpdu []byte) ([]byte, error) {
	if len(tpdu) == 0 || len(tpdu) > maxTPDU {
		return nil, fmt.Errorf("sms: RP-DATA RP-User-Data of %d octets, want 1 to %d", len(tpdu), maxTPDU)
	}
	return append(append(b, byte(len(tpdu))), tpdu...), nil
}

// RPAck acknowledges an RP-DATA or RP-SMMA (TS 24.011 7.3.3). From the
// MS it may carry an SMS-DELIVER-REPORT, from the network an
// SMS-SUBMIT-REPORT.
type RPAck struct {
	Direction        Direction
	MessageReference uint8  // the RP-MR of the message acknowledged
	UserData         []byte // the TPDU, or nil when absent
}

func (m RPAck) MTI() RPMTI {
	if m.Direction == NetworkToMS {
		return RPMTIAckNetworkToMS
	}
	return RPMTIAckMSToNetwork
}

// Marshal writes table 7.7 of TS 24.011.
func (m RPAck) Marshal() ([]byte, error) {
	return appendOptionalRPUserData([]byte{byte(m.MTI()), m.MessageReference}, m.UserData)
}

// RPError reports that an RP-DATA or RP-SMMA failed (TS 24.011 7.3.4).
// Like RPAck it may carry a report TPDU, with TP-FCS.
type RPError struct {
	Direction        Direction
	MessageReference uint8
	Cause            RPCause
	// Diagnostic is the optional diagnostic field after the cause
	// (8.2.5.4), as received.
	Diagnostic []byte
	UserData   []byte
}

func (m RPError) MTI() RPMTI {
	if m.Direction == NetworkToMS {
		return RPMTIErrorNetworkToMS
	}
	return RPMTIErrorMSToNetwork
}

// Marshal writes table 7.8 of TS 24.011. RP-Cause is an LV element: the
// length, then the cause value with bit 8 (ext) at 0, then the
// diagnostic (figure 8.8).
func (m RPError) Marshal() ([]byte, error) {
	if m.Cause > 0x7f {
		return nil, fmt.Errorf("sms: RP-Cause %d does not fit in 7 bits", m.Cause)
	}
	b := []byte{byte(m.MTI()), m.MessageReference, byte(1 + len(m.Diagnostic)), byte(m.Cause)}
	return appendOptionalRPUserData(append(b, m.Diagnostic...), m.UserData)
}

func appendOptionalRPUserData(b, tpdu []byte) ([]byte, error) {
	if len(tpdu) == 0 {
		return b, nil
	}
	if len(tpdu) > maxTPDU {
		return nil, fmt.Errorf("sms: RP-User-Data of %d octets, at most %d", len(tpdu), maxTPDU)
	}
	return append(append(b, rpUserDataIEI, byte(len(tpdu))), tpdu...), nil
}

// RPSMMA tells the network that the MS has memory for short messages
// again (TS 24.011 7.3.2).
type RPSMMA struct {
	MessageReference uint8
}

func (RPSMMA) MTI() RPMTI { return RPMTISMMA }

// Marshal writes table 7.6 of TS 24.011.
func (m RPSMMA) Marshal() ([]byte, error) {
	return []byte{byte(RPMTISMMA), m.MessageReference}, nil
}

// RPCause is the cause value of RP-Cause (TS 24.011 8.2.5.4, table
// 8.4).
type RPCause uint8

const (
	RPCauseUnassignedNumber                RPCause = 1
	RPCauseOperatorDeterminedBarring       RPCause = 8
	RPCauseCallBarred                      RPCause = 10
	RPCauseShortMessageTransferRejected    RPCause = 21
	RPCauseMemoryCapacityExceeded          RPCause = 22
	RPCauseDestinationOutOfOrder           RPCause = 27
	RPCauseUnidentifiedSubscriber          RPCause = 28
	RPCauseFacilityRejected                RPCause = 29
	RPCauseUnknownSubscriber               RPCause = 30
	RPCauseNetworkOutOfOrder               RPCause = 38
	RPCauseTemporaryFailure                RPCause = 41
	RPCauseCongestion                      RPCause = 42
	RPCauseResourcesUnavailable            RPCause = 47
	RPCauseRequestedFacilityNotSubscribed  RPCause = 50
	RPCauseRequestedFacilityNotImplemented RPCause = 69
	RPCauseInvalidShortMessageTransferRef  RPCause = 81
	RPCauseSemanticallyIncorrectMessage    RPCause = 95
	RPCauseInvalidMandatoryInformation     RPCause = 96
	RPCauseMessageTypeNonExistent          RPCause = 97
	RPCauseMessageNotCompatibleWithState   RPCause = 98
	RPCauseInformationElementNonExistent   RPCause = 99
	RPCauseProtocolErrorUnspecified        RPCause = 111
	RPCauseInterworkingUnspecified         RPCause = 127
)

// ParseRP reads the body of a SIP MESSAGE whose Content-Type is
// application/vnd.3gpp.sms. The result is one of the RP message types;
// a type switch tells which.
func ParseRP(b []byte) (RPMessage, error) {
	d := decoder{b: b}
	mti := RPMTI(d.octet() & 0b111) // bits 8..4 are spare (figure 8.4)
	mr := d.octet()
	var m RPMessage
	switch mti {
	case RPMTIDataMSToNetwork:
		d.rpAddress() // RP-OA: empty, but 7.3.1 says not to reject one that is not
		da := d.rpAddress()
		m = RPDataMSToNetwork{MessageReference: mr, DestinationAddress: da, UserData: d.rpData()}
		d.nonImperativePart(false)
	case RPMTIDataNetworkToMS:
		oa := d.rpAddress()
		d.rpAddress() // RP-DA: empty, as above
		m = RPDataNetworkToMS{MessageReference: mr, OriginatorAddress: oa, UserData: d.rpData()}
		d.nonImperativePart(false)
	case RPMTIAckMSToNetwork, RPMTIAckNetworkToMS:
		m = RPAck{Direction: directionOf(mti), MessageReference: mr, UserData: d.nonImperativePart(true)}
	case RPMTIErrorMSToNetwork, RPMTIErrorNetworkToMS:
		cause := d.octets(int(d.octet()))
		if d.err == nil && len(cause) == 0 {
			d.fail(errors.New("RP-Cause is empty"))
		}
		e := RPError{Direction: directionOf(mti), MessageReference: mr}
		if len(cause) > 0 {
			e.Cause = RPCause(cause[0] & 0x7f)
			if len(cause) > 1 {
				e.Diagnostic = cause[1:]
			}
		}
		e.UserData = d.nonImperativePart(true)
		m = e
	case RPMTISMMA:
		m = RPSMMA{MessageReference: mr}
		d.nonImperativePart(false)
	default:
		d.fail(fmt.Errorf("reserved RP-MTI %03b", mti))
	}
	if err := d.end(); err != nil {
		return nil, fmt.Errorf("sms: RP message: %w", err)
	}
	return m, nil
}

// directionOf reads the direction out of an RP-ACK or RP-ERROR MTI: bit
// 1 is set for network to MS (table 8.3).
func directionOf(mti RPMTI) Direction {
	if mti&1 != 0 {
		return NetworkToMS
	}
	return MSToNetwork
}

// rpData reads the RP-User-Data of RP-DATA, an LV element that must
// hold a TPDU (8.2.5.3).
func (d *decoder) rpData() []byte {
	tpdu := d.rpUserData()
	if d.err == nil && len(tpdu) == 0 {
		d.fail(errors.New("RP-DATA has no RP-User-Data"))
	}
	return tpdu
}

// nonImperativePart reads the information elements after the
// mandatory ones, to the end of the message, and returns the TPDU of
// RP-User-Data when userDataKnown: it is the one IE that RP-ACK and
// RP-ERROR know there (TS 24.011 tables 7.7 and 7.8), and RP-DATA and
// RP-SMMA know none. TS 24.011 clause 9 uses the terms of TS 24.007,
// whose 11.2.4 has the receiver skip an IE it does not know rather than
// fail: an IEI with bit 8 set is the whole IE, one octet; any other is
// followed by a length octet. Only the first RP-User-Data is read.
func (d *decoder) nonImperativePart(userDataKnown bool) []byte {
	var tpdu []byte
	seen := false
	for d.err == nil && len(d.b) > 0 {
		iei := d.octet()
		switch {
		case iei&0x80 != 0:
		case iei == rpUserDataIEI && userDataKnown && !seen:
			tpdu, seen = d.rpUserData(), true
		default:
			d.octets(int(d.octet()))
		}
	}
	return tpdu
}

// rpUserData reads the length and the TPDU of RP-User-Data, which holds
// at most maxTPDU octets.
func (d *decoder) rpUserData() []byte {
	n := int(d.octet())
	if d.err == nil && n > maxTPDU {
		d.fail(fmt.Errorf("RP-User-Data of %d octets, at most %d", n, maxTPDU))
	}
	return d.octets(n)
}

// ErrNoUserData means the RP message carries no TPDU.
var ErrNoUserData = errors.New("sms: the RP message carries no RP-User-Data")

// ParseTPDU reads the TPDU in the RP-User-Data of m. Which TPDU it is
// depends on the RP message around it as much as on TP-MTI:
//
//	RP-DATA, MS to network:   SMS-SUBMIT
//	RP-DATA, network to MS:   SMS-DELIVER or SMS-STATUS-REPORT
//	RP-ACK, MS to network:    SMS-DELIVER-REPORT for RP-ACK
//	RP-ACK, network to MS:    SMS-SUBMIT-REPORT for RP-ACK
//	RP-ERROR, MS to network:  SMS-DELIVER-REPORT for RP-ERROR (with TP-FCS)
//	RP-ERROR, network to MS:  SMS-SUBMIT-REPORT for RP-ERROR (with TP-FCS)
//
// SIMPLIFIED: TS 23.040 9.2.2.4. SMS-COMMAND, which asks the SC about a
// message it holds, is refused: no UE of this project sends one.
func ParseTPDU(m RPMessage) (TPDU, error) {
	switch m := m.(type) {
	case RPDataMSToNetwork:
		if len(m.UserData) == 0 {
			return nil, ErrNoUserData
		}
		if tpMTI(m.UserData[0]&0b11) != mtiSubmit {
			return nil, fmt.Errorf("sms: RP-DATA from the MS carries TP-MTI %02b, not SMS-SUBMIT", m.UserData[0]&0b11)
		}
		return parseSubmit(m.UserData)
	case RPDataNetworkToMS:
		if len(m.UserData) == 0 {
			return nil, ErrNoUserData
		}
		switch tpMTI(m.UserData[0] & 0b11) {
		case mtiStatusReport:
			return parseStatusReport(m.UserData)
		case mtiDeliver, mtiReserved:
			// TS 23.040 9.2.3.1: an MS takes a reserved TP-MTI as SMS-DELIVER.
			return parseDeliver(m.UserData)
		}
		return nil, errors.New("sms: RP-DATA from the network carries TP-MTI 01, which is SMS-SUBMIT-REPORT")
	case RPAck:
		return parseReport(m.Direction, m.UserData, false)
	case RPError:
		return parseReport(m.Direction, m.UserData, true)
	}
	return nil, ErrNoUserData
}

func parseReport(dir Direction, tpdu []byte, forRPError bool) (TPDU, error) {
	if len(tpdu) == 0 {
		return nil, ErrNoUserData
	}
	if dir == MSToNetwork {
		return parseDeliverReport(tpdu, forRPError)
	}
	return parseSubmitReport(tpdu, forRPError)
}
