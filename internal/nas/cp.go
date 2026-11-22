// Package nas encodes and decodes the NAS messages that carry a short
// message between a UE and the core network without the IMS (SMS over
// NAS). It covers only what SMS needs:
//
//   - the CP layer of TS 24.011: CP-DATA, CP-ACK and CP-ERROR, which the
//     SMC entities of the UE and of the MME or SMSF exchange;
//   - the EPS messages that carry a CP message between the UE and the
//     MME: UPLINK NAS TRANSPORT and DOWNLINK NAS TRANSPORT (TS 24.301);
//   - the 5GS messages that carry one between the UE and the AMF: UL NAS
//     TRANSPORT and DL NAS TRANSPORT with the payload container type
//     "SMS" (TS 24.501).
//
// The layers nest like this, outermost first:
//
//	UPLINK NAS TRANSPORT (TS 24.301) or UL NAS TRANSPORT (TS 24.501)
//	  CP-DATA (TS 24.011 7.2.1)
//	    RP-DATA, RP-ACK, RP-ERROR or RP-SMMA (TS 24.011 7.3)
//	      TPDU such as SMS-SUBMIT (TS 23.040)
//
// The RP message is the same one that SMS over IP puts in the body of a
// SIP MESSAGE. This package takes and returns it as octets and does not
// look inside.
//
// No node of this project runs it: no binary in cmd builds it in.
package nas

import (
	"errors"
	"fmt"
)

// protocolDiscriminatorSMS is "SMS messages" in bits 4 to 1 of the first
// octet of a CP message (TS 24.007 11.2.3.1.1, Table 11.2).
const protocolDiscriminatorSMS = 0b1001

// CPMessageType is the second octet of a CP message (TS 24.011 8.1.3,
// Table 8.1).
type CPMessageType uint8

const (
	CPMessageTypeData  CPMessageType = 0b0000_0001
	CPMessageTypeAck   CPMessageType = 0b0000_0100
	CPMessageTypeError CPMessageType = 0b0001_0000
)

func (t CPMessageType) String() string {
	switch t {
	case CPMessageTypeData:
		return "CP-DATA"
	case CPMessageTypeAck:
		return "CP-ACK"
	case CPMessageTypeError:
		return "CP-ERROR"
	}
	return fmt.Sprintf("CP message type %#02x", uint8(t))
}

// TIFlag is bit 8 of the first octet of a CP message (TS 24.007
// 11.2.3.1.3, Table 11.3). The UE and the network each pick TI values on
// their own, so both may pick the same one; the flag tells the two
// transactions apart.
type TIFlag uint8

const (
	// TIFlagFromOriginator marks a message sent from the side that
	// originated the transaction and so chose the TI value.
	TIFlagFromOriginator TIFlag = 0
	// TIFlagToOriginator marks a message sent to the side that
	// originated the transaction.
	TIFlagToOriginator TIFlag = 1
)

// tiValueExtension is the TI value 111, which TS 24.007 11.2.3.1.3
// keeps for the extended TI. The CP layer does not use the extension,
// and TS 24.011 9.2.2 has a receiver ignore a CP message that carries
// this value.
const tiValueExtension = 0b111

// TransactionIdentifier is the transaction identifier (TI) in bits 8 to
// 5 of the first octet of a CP message (TS 24.011 8.1.2, TS 24.007
// 11.2.3.1.3). One transaction is the transfer of one short message or
// notification: the CP-DATA that carries RP-DATA or RP-SMMA, the CP-DATA
// that carries the RP-ACK or RP-ERROR back, and their CP-ACKs.
type TransactionIdentifier struct {
	Flag TIFlag
	// Value is the TI value (TIO), 0 to 6. The side that starts a
	// transaction picks a value it is not using.
	Value uint8
}

// Opposite is the same transaction as the other side writes it: the same
// TI value with the TI flag inverted. A receiver answers a message that
// has TransactionIdentifier ti with one that has ti.Opposite().
func (ti TransactionIdentifier) Opposite() TransactionIdentifier {
	ti.Flag ^= 1
	return ti
}

// header writes the first two octets of a CP message (TS 24.011 figure
// 8.1): the TI flag, the TI value and the protocol discriminator, then
// the message type.
func (ti TransactionIdentifier) header(t CPMessageType) ([]byte, error) {
	if ti.Flag > TIFlagToOriginator {
		return nil, fmt.Errorf("nas: %s: TI flag %d, want 0 or 1", t, ti.Flag)
	}
	if ti.Value >= tiValueExtension {
		return nil, fmt.Errorf("nas: %s: TI value %d, want 0 to 6", t, ti.Value)
	}
	return []byte{byte(ti.Flag)<<7 | ti.Value<<4 | protocolDiscriminatorSMS, byte(t)}, nil
}

// CPMessage is one message of the CP layer, which two SMC entities
// exchange (TS 24.011 7.2): CPData, CPAck or CPError. It is the whole
// content of the NAS message container of TS 24.301 and of a payload
// container of type "SMS" of TS 24.501.
type CPMessage interface {
	MessageType() CPMessageType
	// TI is the transaction the message belongs to.
	TI() TransactionIdentifier
	Marshal() ([]byte, error)
}

// maxRPDU is the largest RPDU that CP-User data holds (TS 24.011
// 8.1.4.1, figure 8.2).
const maxRPDU = 248

// CPData carries one RP message (TS 24.011 7.2.1). The receiver answers
// it with CP-ACK, or with CP-ERROR if it cannot accept it (5.3.2.2).
type CPData struct {
	TransactionIdentifier TransactionIdentifier
	// UserData is the RPDU in CP-User data (8.1.4.1): RP-DATA, RP-ACK,
	// RP-ERROR or RP-SMMA.
	UserData []byte
}

func (CPData) MessageType() CPMessageType { return CPMessageTypeData }

func (m CPData) TI() TransactionIdentifier { return m.TransactionIdentifier }

// Marshal writes table 7.1 of TS 24.011. In a message CP-User data is an
// LV element: the length and the RPDU, without the IEI of figure 8.2.
func (m CPData) Marshal() ([]byte, error) {
	b, err := m.TransactionIdentifier.header(CPMessageTypeData)
	if err != nil {
		return nil, err
	}
	if len(m.UserData) == 0 || len(m.UserData) > maxRPDU {
		return nil, fmt.Errorf("nas: CP-DATA: CP-User data of %d octets, want 1 to %d", len(m.UserData), maxRPDU)
	}
	return append(append(b, byte(len(m.UserData))), m.UserData...), nil
}

// CPAck acknowledges a CP-DATA (TS 24.011 7.2.2). It only says that the
// CP-DATA arrived; whether the short message was accepted is said by the
// RP-ACK or RP-ERROR that follows in a CP-DATA of its own.
type CPAck struct {
	TransactionIdentifier TransactionIdentifier
}

func (CPAck) MessageType() CPMessageType { return CPMessageTypeAck }

func (m CPAck) TI() TransactionIdentifier { return m.TransactionIdentifier }

// Marshal writes table 7.2 of TS 24.011.
func (m CPAck) Marshal() ([]byte, error) {
	return m.TransactionIdentifier.header(CPMessageTypeAck)
}

// CPError ends a transaction because of a fault in the CP layer (TS
// 24.011 7.2.3, 5.3.4). Both sides go back to the idle state (9.2).
type CPError struct {
	TransactionIdentifier TransactionIdentifier
	Cause                 CPCause
}

func (CPError) MessageType() CPMessageType { return CPMessageTypeError }

func (m CPError) TI() TransactionIdentifier { return m.TransactionIdentifier }

// Marshal writes table 7.3 of TS 24.011. In a message CP-Cause is a V
// element: one octet with bit 8 at 0 and the cause value in bits 7 to 1
// (figure 8.3).
func (m CPError) Marshal() ([]byte, error) {
	b, err := m.TransactionIdentifier.header(CPMessageTypeError)
	if err != nil {
		return nil, err
	}
	if m.Cause > 0x7f {
		return nil, fmt.Errorf("nas: CP-ERROR: CP-Cause %d does not fit in 7 bits", uint8(m.Cause))
	}
	return append(b, byte(m.Cause)), nil
}

// CPCause is the cause value of CP-Cause (TS 24.011 8.1.4.2, Table 8.2).
// A receiver treats a value that the table does not list as
// CPCauseProtocolErrorUnspecified.
type CPCause uint8

const (
	CPCauseNetworkFailure                CPCause = 17
	CPCauseCongestion                    CPCause = 22
	CPCauseInvalidTransactionIdentifier  CPCause = 81
	CPCauseSemanticallyIncorrectMessage  CPCause = 95
	CPCauseInvalidMandatoryInformation   CPCause = 96
	CPCauseMessageTypeNonExistent        CPCause = 97
	CPCauseMessageNotCompatibleWithState CPCause = 98
	CPCauseInformationElementNonExistent CPCause = 99
	CPCauseProtocolErrorUnspecified      CPCause = 111
)

var cpCauseNames = map[CPCause]string{
	CPCauseNetworkFailure:                "Network failure",
	CPCauseCongestion:                    "Congestion",
	CPCauseInvalidTransactionIdentifier:  "Invalid Transaction Identifier value",
	CPCauseSemanticallyIncorrectMessage:  "Semantically incorrect message",
	CPCauseInvalidMandatoryInformation:   "Invalid mandatory information",
	CPCauseMessageTypeNonExistent:        "Message type non-existent or not implemented",
	CPCauseMessageNotCompatibleWithState: "Message not compatible with the short message protocol state",
	CPCauseInformationElementNonExistent: "Information element non-existent or not implemented",
	CPCauseProtocolErrorUnspecified:      "Protocol error, unspecified",
}

// String gives the number and the name of Table 8.2, as a capture shows
// them.
func (c CPCause) String() string {
	if name, ok := cpCauseNames[c]; ok {
		return fmt.Sprintf("#%d %s", uint8(c), name)
	}
	return fmt.Sprintf("#%d", uint8(c))
}

// ErrMessageTooShort is the error of ParseCP for a message that is too
// short to hold a message type. TS 24.011 9.2.1 has the receiver ignore
// it.
var ErrMessageTooShort = errors.New("nas: message too short to hold a message type")

// MalformedCPMessage is the error of ParseCP for a message that names
// its transaction but cannot be accepted. TS 24.011 9.2.3 and 9.2.4 let
// the receiver answer such a message with CP-ERROR; Cause is the cause
// value they give for the fault, and TransactionIdentifier is the one of
// the message received.
type MalformedCPMessage struct {
	TransactionIdentifier TransactionIdentifier
	Cause                 CPCause
	Reason                string
}

func (e *MalformedCPMessage) Error() string {
	return fmt.Sprintf("nas: CP message: %s (CP-Cause %s)", e.Reason, e.Cause)
}

// ParseCP reads a CP message. The result is CPData, CPAck or CPError; a
// type switch tells which.
//
// What a receiver does with an error follows TS 24.011 9.2. If the error
// is a *MalformedCPMessage, the receiver may answer with CP-ERROR:
//
//	var malformed *nas.MalformedCPMessage
//	if errors.As(err, &malformed) {
//		reply := nas.CPError{
//			TransactionIdentifier: malformed.TransactionIdentifier.Opposite(),
//			Cause:                 malformed.Cause,
//		}
//	}
//
// Any other error means the message is to be ignored: it is too short
// (9.2.1), it has the TI value 111 (9.2.2), it is not a CP message at
// all, or it is a CP-ERROR that does not decode, which 9.2.4 says not to
// answer.
//
// SIMPLIFIED: TS 24.007 11.2.4 and 11.2.5. Octets after the last element
// of a CP message would be its non-imperative part: information elements
// that this version of TS 24.011 does not define, whose presence is no
// error by itself. They are ignored without being read as elements, so a
// malformed one goes unnoticed. TS 24.011 defines no optional element in
// any CP message.
func ParseCP(b []byte) (CPMessage, error) {
	if len(b) < 2 {
		return nil, fmt.Errorf("%w: CP message of %d octets", ErrMessageTooShort, len(b))
	}
	if pd := b[0] & 0x0f; pd != protocolDiscriminatorSMS {
		return nil, fmt.Errorf("nas: CP message: protocol discriminator %04b, want %04b (SMS messages)", pd, protocolDiscriminatorSMS)
	}
	ti := TransactionIdentifier{Flag: TIFlag(b[0] >> 7), Value: b[0] >> 4 & 0b111}
	if ti.Value == tiValueExtension {
		return nil, errors.New("nas: CP message: TI value 111, which the CP layer does not use")
	}
	malformed := func(cause CPCause, format string, args ...any) error {
		return &MalformedCPMessage{TransactionIdentifier: ti, Cause: cause, Reason: fmt.Sprintf(format, args...)}
	}
	ies := b[2:]
	switch t := CPMessageType(b[1]); t {
	case CPMessageTypeData:
		if len(ies) == 0 {
			return nil, malformed(CPCauseInvalidMandatoryInformation, "CP-DATA is truncated before CP-User data")
		}
		length, rest := int(ies[0]), ies[1:]
		switch {
		case length == 0:
			return nil, malformed(CPCauseInvalidMandatoryInformation, "CP-DATA has an empty CP-User data")
		case length > maxRPDU:
			return nil, malformed(CPCauseInvalidMandatoryInformation, "CP-User data of %d octets, at most %d", length, maxRPDU)
		case length > len(rest):
			return nil, malformed(CPCauseInvalidMandatoryInformation,
				"CP-User data is truncated: its length says %d octets, %d follow", length, len(rest))
		}
		return CPData{TransactionIdentifier: ti, UserData: rest[:length:length]}, nil
	case CPMessageTypeAck:
		return CPAck{TransactionIdentifier: ti}, nil
	case CPMessageTypeError:
		// TS 24.011 9.2.4: a CP-ERROR is never answered with a CP-ERROR,
		// so this is a plain error.
		if len(ies) == 0 {
			return nil, errors.New("nas: CP-ERROR is truncated before CP-Cause")
		}
		return CPError{TransactionIdentifier: ti, Cause: CPCause(ies[0] & 0x7f)}, nil // bit 8 is spare (figure 8.3)
	default:
		return nil, malformed(CPCauseMessageTypeNonExistent, "message type %#02x is not CP-DATA, CP-ACK or CP-ERROR", uint8(t))
	}
}
