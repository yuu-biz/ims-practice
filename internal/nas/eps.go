package nas

import (
	"errors"
	"fmt"
)

// This file is the EPS side: the two EMM messages of the "transport of
// NAS messages" procedure (TS 24.301 5.6.3), which carry a CP message
// between the UE and the MME. The UE may use the procedure once it is
// attached for EPS services and "SMS only", or for EPS and non-EPS
// services, and is in EMM-CONNECTED mode (5.6.3.1).

const (
	// protocolDiscriminatorEMM is "EPS mobility management messages" in
	// bits 4 to 1 of the first octet (TS 24.007 11.2.3.1.1, Table 11.2).
	protocolDiscriminatorEMM = 0b0111
	// securityHeaderTypePlain is "Plain NAS message, not security
	// protected" in bits 8 to 5 of the first octet (TS 24.301 9.3.1,
	// Table 9.3.1). TS 24.501 9.3 gives the same value to 5GS.
	securityHeaderTypePlain = 0b0000
)

// Message types of EPS mobility management (TS 24.301 9.8, Table 9.8.1).
const (
	emmMessageTypeDownlinkNASTransport = 0b0110_0010
	emmMessageTypeUplinkNASTransport   = 0b0110_0011
)

// The NAS message container is an LV element of 3 to 252 octets in both
// messages (TS 24.301 Table 8.2.12.1 and Table 8.2.30.1), so its
// contents are 2 to 251 octets: from a CP-ACK to a CP-DATA with the
// largest RPDU.
const (
	minNASMessageContainer = 2
	maxNASMessageContainer = 251
)

// UplinkNASTransport is the UPLINK NAS TRANSPORT message, which the UE
// sends to the MME to carry an SMS message (TS 24.301 8.2.30, 5.6.3.2).
type UplinkNASTransport struct {
	// NASMessageContainer is the contents of the NAS message container
	// (9.9.3.22): one CP message.
	NASMessageContainer []byte
}

// Marshal writes Table 8.2.30.1 of TS 24.301 as a plain NAS message.
func (m UplinkNASTransport) Marshal() ([]byte, error) {
	return marshalEMMNASTransport("UPLINK NAS TRANSPORT", emmMessageTypeUplinkNASTransport, m.NASMessageContainer)
}

// ParseUplinkNASTransport reads an UPLINK NAS TRANSPORT message.
func ParseUplinkNASTransport(b []byte) (UplinkNASTransport, error) {
	container, err := parseEMMNASTransport("UPLINK NAS TRANSPORT", emmMessageTypeUplinkNASTransport, b)
	return UplinkNASTransport{NASMessageContainer: container}, err
}

// DownlinkNASTransport is the DOWNLINK NAS TRANSPORT message, which the
// MME sends to the UE to carry an SMS message (TS 24.301 8.2.12,
// 5.6.3.3).
type DownlinkNASTransport struct {
	// NASMessageContainer is the contents of the NAS message container
	// (9.9.3.22): one CP message.
	NASMessageContainer []byte
}

// Marshal writes Table 8.2.12.1 of TS 24.301 as a plain NAS message.
func (m DownlinkNASTransport) Marshal() ([]byte, error) {
	return marshalEMMNASTransport("DOWNLINK NAS TRANSPORT", emmMessageTypeDownlinkNASTransport, m.NASMessageContainer)
}

// ParseDownlinkNASTransport reads a DOWNLINK NAS TRANSPORT message.
func ParseDownlinkNASTransport(b []byte) (DownlinkNASTransport, error) {
	container, err := parseEMMNASTransport("DOWNLINK NAS TRANSPORT", emmMessageTypeDownlinkNASTransport, b)
	return DownlinkNASTransport{NASMessageContainer: container}, err
}

// marshalEMMNASTransport writes the layout the two messages share: the
// security header type and the protocol discriminator, the message type,
// and the NAS message container as an LV element.
//
// SIMPLIFIED: TS 24.301 4.4.2.3, 4.4.4.1 and 4.4.5. After the security
// mode control procedure of the attach, a UE and an MME send these
// messages integrity protected and ciphered: the security header type
// is "Integrity protected and ciphered", and a message authentication
// code and a sequence number come before the plain message (9.1, figure
// 9.1.2). This package writes and reads the plain message only. The keys
// come from EPS AKA and the security mode control procedure, which are
// not part of this package, so it has no keys to protect a message with.
func marshalEMMNASTransport(name string, messageType byte, container []byte) ([]byte, error) {
	if len(container) < minNASMessageContainer || len(container) > maxNASMessageContainer {
		return nil, fmt.Errorf("nas: %s: NAS message container of %d octets, want %d to %d",
			name, len(container), minNASMessageContainer, maxNASMessageContainer)
	}
	b := []byte{securityHeaderTypePlain<<4 | protocolDiscriminatorEMM, messageType, byte(len(container))}
	return append(b, container...), nil
}

// parseEMMNASTransport reads what marshalEMMNASTransport writes and
// returns the contents of the NAS message container.
//
// SIMPLIFIED: TS 24.301 7.6.1 and TS 24.007 11.2.5. Octets after the NAS
// message container would be information elements that this version of
// the message does not know. They are ignored without being read, so an
// element coded as "comprehension required", which 7.5.1 has a receiver
// refuse, is ignored too. Neither message has an optional element.
func parseEMMNASTransport(name string, messageType byte, b []byte) ([]byte, error) {
	if len(b) < 2 {
		return nil, fmt.Errorf("%w: %s of %d octets", ErrMessageTooShort, name, len(b))
	}
	if pd := b[0] & 0x0f; pd != protocolDiscriminatorEMM {
		return nil, fmt.Errorf("nas: %s: protocol discriminator %04b, want %04b (EPS mobility management messages)",
			name, pd, protocolDiscriminatorEMM)
	}
	if sht := b[0] >> 4; sht != securityHeaderTypePlain {
		return nil, fmt.Errorf("nas: %s: security header type %04b: a security protected NAS message is not supported", name, sht)
	}
	if b[1] != messageType {
		return nil, fmt.Errorf("nas: message type %#02x is not %s (%#02x)", b[1], name, messageType)
	}
	container, err := lengthValue(b[2:], 1)
	if err != nil {
		return nil, fmt.Errorf("nas: %s: NAS message container: %w", name, err)
	}
	if len(container) < minNASMessageContainer || len(container) > maxNASMessageContainer {
		return nil, fmt.Errorf("nas: %s: NAS message container of %d octets, want %d to %d",
			name, len(container), minNASMessageContainer, maxNASMessageContainer)
	}
	return container, nil
}

// lengthValue reads the information element at the front of b that has
// a length indicator of lengthOctets octets and then a value: format LV
// with one octet, LV-E with two (TS 24.007 11.2.1.1.4). It returns the
// value.
func lengthValue(b []byte, lengthOctets int) ([]byte, error) {
	if len(b) < lengthOctets {
		return nil, errors.New("truncated before its length")
	}
	length := 0
	for _, octet := range b[:lengthOctets] {
		length = length<<8 | int(octet)
	}
	value := b[lengthOctets:]
	if length > len(value) {
		return nil, fmt.Errorf("truncated: its length says %d octets, %d follow", length, len(value))
	}
	return value[:length:length], nil
}
