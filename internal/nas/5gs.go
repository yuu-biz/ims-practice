package nas

import "fmt"

// This file is the 5GS side: the two 5GMM messages of the NAS transport
// procedures (TS 24.501 5.4.5), which carry a payload between the UE and
// the AMF. For SMS the payload is a CP message, and the AMF passes it on
// to the SMSF without looking inside (5.4.5.2.3, 5.4.5.3.2). The UE may
// send SMS this way after a registration in which it asked for SMS over
// NAS and the network allowed it (5.5.1.2.4).

// extendedProtocolDiscriminator5GMM is "5GS mobility management
// messages" in the first octet (TS 24.501 9.2, TS 24.007 11.2.3.1.1A,
// Table 11.2.3.1.1A.1).
const extendedProtocolDiscriminator5GMM = 0b0111_1110

// Message types of 5GS mobility management (TS 24.501 9.7, Table 9.7.1).
const (
	fiveGMMMessageTypeULNASTransport = 0b0110_0111
	fiveGMMMessageTypeDLNASTransport = 0b0110_1000
)

// PayloadContainerType is the Payload container type element (TS 24.501
// 9.11.3.40, Table 9.11.3.40.1): what the payload container holds. Only
// the value this project sends is named.
type PayloadContainerType uint8

// PayloadContainerTypeSMS marks a payload container that holds an SMS
// message: CP-DATA, CP-ACK or CP-ERROR (TS 24.501 Table 9.11.3.39.1).
const PayloadContainerTypeSMS PayloadContainerType = 0b0010

func (t PayloadContainerType) String() string {
	if t == PayloadContainerTypeSMS {
		return "SMS"
	}
	return fmt.Sprintf("Payload container type %04b", uint8(t))
}

// maxPayloadContainer is the largest contents of the Payload container,
// an LV-E element of 3 to 65537 octets (TS 24.501 Table 8.2.10.1.1 and
// Table 8.2.11.1.1).
const maxPayloadContainer = 65535

// ULNASTransport is the UL NAS TRANSPORT message, which the UE sends to
// the AMF (TS 24.501 8.2.10, 5.4.5.2).
type ULNASTransport struct {
	PayloadContainerType PayloadContainerType
	// PayloadContainer is the contents of the Payload container
	// (9.11.3.39). For PayloadContainerTypeSMS it is one CP message.
	PayloadContainer []byte
}

// Marshal writes the mandatory elements of Table 8.2.10.1.1 of TS 24.501
// as a plain 5GS NAS message.
func (m ULNASTransport) Marshal() ([]byte, error) {
	return marshal5GMMNASTransport("UL NAS TRANSPORT", fiveGMMMessageTypeULNASTransport, m.PayloadContainerType, m.PayloadContainer)
}

// ParseULNASTransport reads a UL NAS TRANSPORT message. The payload may
// be of any type; the AMF passes on only what has
// PayloadContainerTypeSMS to the SMSF.
func ParseULNASTransport(b []byte) (ULNASTransport, error) {
	t, container, err := parse5GMMNASTransport("UL NAS TRANSPORT", fiveGMMMessageTypeULNASTransport, b)
	return ULNASTransport{PayloadContainerType: t, PayloadContainer: container}, err
}

// DLNASTransport is the DL NAS TRANSPORT message, which the AMF sends to
// the UE (TS 24.501 8.2.11, 5.4.5.3).
type DLNASTransport struct {
	PayloadContainerType PayloadContainerType
	// PayloadContainer is the contents of the Payload container
	// (9.11.3.39). For PayloadContainerTypeSMS it is one CP message.
	PayloadContainer []byte
}

// Marshal writes the mandatory elements of Table 8.2.11.1.1 of TS 24.501
// as a plain 5GS NAS message.
func (m DLNASTransport) Marshal() ([]byte, error) {
	return marshal5GMMNASTransport("DL NAS TRANSPORT", fiveGMMMessageTypeDLNASTransport, m.PayloadContainerType, m.PayloadContainer)
}

// ParseDLNASTransport reads a DL NAS TRANSPORT message. The payload may
// be of any type; the UE passes on only what has
// PayloadContainerTypeSMS to its SMS entity (5.4.5.3.3).
func ParseDLNASTransport(b []byte) (DLNASTransport, error) {
	t, container, err := parse5GMMNASTransport("DL NAS TRANSPORT", fiveGMMMessageTypeDLNASTransport, b)
	return DLNASTransport{PayloadContainerType: t, PayloadContainer: container}, err
}

// marshal5GMMNASTransport writes the layout the two messages share: the
// extended protocol discriminator, the security header type, the message
// type, the Payload container type and the Payload container as an LV-E
// element. Each of the two half octets shares its octet with a spare
// half octet in bits 8 to 5 (9.5).
//
// SIMPLIFIED: TS 24.501 8.2.10 and 8.2.11. Only a payload of type "SMS"
// is written, which needs none of the optional elements. The other
// payload types need them, such as the PDU session ID for "N1 SM
// information" (8.2.10.2). Those types carry the messages of other
// procedures, such as 5GS session management, and this package covers
// SMS over NAS only.
//
// SIMPLIFIED: TS 24.501 4.4.4.1 and 4.4.5. As in EPS, a UE and an AMF
// send these messages integrity protected and ciphered once the 5G NAS
// security context is in use, with a message authentication code and a
// sequence number before the plain message (9.1.1, figure 9.1.1.2). This
// package writes and reads the plain message only. The keys of that
// context come from 5G AKA and the security mode control procedure,
// which are not part of this package, so it has no keys to protect a
// message with.
func marshal5GMMNASTransport(name string, messageType byte, t PayloadContainerType, container []byte) ([]byte, error) {
	if t != PayloadContainerTypeSMS {
		return nil, fmt.Errorf("nas: %s: %s is not supported, only SMS", name, t)
	}
	if len(container) == 0 || len(container) > maxPayloadContainer {
		return nil, fmt.Errorf("nas: %s: Payload container of %d octets, want 1 to %d", name, len(container), maxPayloadContainer)
	}
	b := []byte{
		extendedProtocolDiscriminator5GMM,
		securityHeaderTypePlain,
		messageType,
		byte(t),
		byte(len(container) >> 8), byte(len(container)),
	}
	return append(b, container...), nil
}

// parse5GMMNASTransport reads the mandatory elements that
// marshal5GMMNASTransport writes.
//
// SIMPLIFIED: TS 24.501 8.2.10, 8.2.11 and 7.6.1. The optional elements
// after the Payload container are ignored without being read. A payload
// of type "SMS" comes without them. As in EPS, an unknown element coded
// as "comprehension required" (TS 24.007 11.2.5) is ignored too.
func parse5GMMNASTransport(name string, messageType byte, b []byte) (PayloadContainerType, []byte, error) {
	// TS 24.501 7.2.1: the message type is the third octet.
	if len(b) < 3 {
		return 0, nil, fmt.Errorf("%w: %s of %d octets", ErrMessageTooShort, name, len(b))
	}
	if b[0] != extendedProtocolDiscriminator5GMM {
		return 0, nil, fmt.Errorf("nas: %s: extended protocol discriminator %#02x, want %#02x (5GS mobility management messages)",
			name, b[0], extendedProtocolDiscriminator5GMM)
	}
	if sht := b[1] & 0x0f; sht != securityHeaderTypePlain {
		return 0, nil, fmt.Errorf("nas: %s: security header type %04b: a security protected 5GS NAS message is not supported", name, sht)
	}
	if b[2] != messageType {
		return 0, nil, fmt.Errorf("nas: message type %#02x is not %s (%#02x)", b[2], name, messageType)
	}
	if len(b) < 4 {
		return 0, nil, fmt.Errorf("nas: %s: truncated before the Payload container type", name)
	}
	t := PayloadContainerType(b[3] & 0x0f)
	container, err := lengthValue(b[4:], 2)
	if err != nil {
		return 0, nil, fmt.Errorf("nas: %s: Payload container: %w", name, err)
	}
	if len(container) == 0 {
		return 0, nil, fmt.Errorf("nas: %s: Payload container is empty", name)
	}
	return t, container, nil
}
