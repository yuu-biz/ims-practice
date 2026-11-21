package hep

import (
	"bytes"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// specExample is the example packet at the end of REV 37, octet for octet.
var specExample = []byte{
	0x48, 0x45, 0x50, 0x33, // HEP3 ID
	0x00, 0x71, // total length = 113 octets
	0x00, 0x00, 0x00, 0x01, 0x00, 0x07, 0x02, // protocol family = 2 (IPv4)
	0x00, 0x00, 0x00, 0x02, 0x00, 0x07, 0x11, // protocol ID = 17 (UDP)
	0x00, 0x00, 0x00, 0x03, 0x00, 0x0a, 0xd4, 0xca, 0x00, 0x01, // IPv4 source address = 212.202.0.1
	0x00, 0x00, 0x00, 0x04, 0x00, 0x0a, 0x52, 0x74, 0x00, 0xd3, // IPv4 destination address = 82.116.0.211
	0x00, 0x00, 0x00, 0x07, 0x00, 0x08, 0x2e, 0xea, // source port = 12010
	0x00, 0x00, 0x00, 0x08, 0x00, 0x08, 0x13, 0xc4, // destination port = 5060
	0x00, 0x00, 0x00, 0x09, 0x00, 0x0a, 0x4e, 0x49, 0x82, 0xcb, // seconds = 1313440459
	0x00, 0x00, 0x00, 0x0a, 0x00, 0x0a, 0x00, 0x01, 0xd4, 0xc0, // microseconds = 120000
	0x00, 0x00, 0x00, 0x0b, 0x00, 0x07, 0x01, // protocol type = SIP
	0x00, 0x00, 0x00, 0x0c, 0x00, 0x0a, 0x00, 0x00, 0x00, 0xe4, // capture agent ID = 228
	0x00, 0x00, 0x00, 0x0f, 0x00, 0x14, 'I', 'N', 'V', 'I', 'T', 'E', ' ', 's', 'i', 'p', ':', 'b', 'o', 'b', // payload
}

var specExamplePacket = Packet{
	IPProtocol:     IPProtocolUDP,
	Source:         netip.MustParseAddrPort("212.202.0.1:12010"),
	Destination:    netip.MustParseAddrPort("82.116.0.211:5060"),
	Time:           time.Date(2011, time.August, 15, 20, 34, 19, 120000000, time.UTC),
	ProtocolType:   ProtocolTypeSIP,
	CaptureAgentID: 228,
	Payload:        []byte("INVITE sip:bob"),
}

// withCorrelationID is specExample with a correlation ID chunk before the
// payload, built by hand from the chunk layout.
func withCorrelationID() []byte {
	b := bytes.Clone(specExample[:len(specExample)-20])
	b[5] = 0x71 + 0x12                                                        // total length grows by the 18-octet chunk
	b = append(b, 0x00, 0x00, 0x00, 0x11, 0x00, 0x12)                         // correlation ID, length 6 + 12
	b = append(b, 'a', '8', '4', 'b', '4', 'c', '7', '6', '@', 'u', 'e', '1') // Call-ID
	return append(b, specExample[len(specExample)-20:]...)
}

func TestGoldenVectors(t *testing.T) {
	withCallID := specExamplePacket
	withCallID.CorrelationID = "a84b4c76@ue1"
	tests := []struct {
		name   string
		packet Packet
		wire   []byte
	}{
		{"REV 37 example", specExamplePacket, specExample},
		{"with correlation ID", withCallID, withCorrelationID()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.packet.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.wire) {
				t.Errorf("Marshal:\n got %x\nwant %x", got, tt.wire)
			}
			decoded, err := Unmarshal(tt.wire)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, tt.packet) {
				t.Errorf("Unmarshal:\n got %+v\nwant %+v", decoded, tt.packet)
			}
		})
	}
}

func TestRoundTripKeepsMicroseconds(t *testing.T) {
	p := Packet{
		IPProtocol:     IPProtocolTCP,
		Source:         netip.MustParseAddrPort("172.22.0.20:3868"),
		Destination:    netip.MustParseAddrPort("172.22.0.30:41000"),
		Time:           time.Date(2026, time.November, 19, 9, 30, 0, 123456789, time.UTC),
		ProtocolType:   ProtocolTypeDiameterJSON,
		CaptureAgentID: 0xfffffffe,
		CorrelationID:  "scscf.ims.example;1;42",
		NodeName:       "scscf",
		Payload:        []byte(`{"command":"Server-Assignment","avps":"` + strings.Repeat("x", 2000) + `"}`),
	}
	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	want := p
	want.Time = time.Date(2026, time.November, 19, 9, 30, 0, 123456000, time.UTC)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestUnmarshalDoesNotShareMemory(t *testing.T) {
	b := bytes.Clone(specExample)
	p, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	clear(b)
	if string(p.Payload) != "INVITE sip:bob" {
		t.Errorf("payload = %q after the input was cleared", p.Payload)
	}
}

func TestUnmarshalSkipsUnknownChunks(t *testing.T) {
	extra := []byte{
		0x00, 0x02, 0x00, 0x0c, 0x00, 0x08, 0xab, 0xcd, // vendor 0x0002 reuses type 0x000c with 2 octets
		0x00, 0x00, 0x00, 0x10, 0x00, 0x0b, 'x', 'x', 'x', 'x', 'x', // generic 0x0010 (compressed payload), not decoded here
	}
	b := append(bytes.Clone(specExample[:6]), extra...)
	b = append(b, specExample[6:]...)
	b[5] += byte(len(extra))
	got, err := Unmarshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specExamplePacket) {
		t.Errorf("got %+v\nwant %+v", got, specExamplePacket)
	}
}

func TestUnmarshalRejectsInvalidInput(t *testing.T) {
	// patch returns specExample with the octets at offset replaced.
	patch := func(offset int, octets ...byte) []byte {
		b := bytes.Clone(specExample)
		copy(b[offset:], octets)
		return b
	}
	tests := []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"shorter than the header", []byte("HEP3\x00")},
		{"wrong ID", patch(0, 'H', 'E', 'P', '2')},
		{"truncated", specExample[:len(specExample)-1]},
		{"trailing octets", append(bytes.Clone(specExample), 0)},
		{"total length below the header", patch(4, 0x00, 0x05)},
		{"chunk header cut short", append(patch(5, 0x71+3), 0x00, 0x00, 0x00)},
		{"chunk length zero", patch(6+4, 0x00, 0x00)},
		{"chunk length below its header", patch(6+4, 0x00, 0x05)},
		{"chunk runs past the packet", patch(len(specExample)-20+4, 0x00, 0x15)},
		{"port with three octets", patch(6+7+7+10+10+4, 0x00, 0x09)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if p, err := Unmarshal(tt.b); err == nil {
				t.Errorf("Unmarshal(%x) = %+v, want an error", tt.b, p)
			}
		})
	}
}

func TestMarshalRejects(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Packet)
	}{
		{"IPv6 source", func(p *Packet) { p.Source = netip.MustParseAddrPort("[2001:db8::1]:5060") }},
		{"no destination", func(p *Packet) { p.Destination = netip.AddrPort{} }},
		{"payload over the total length", func(p *Packet) { p.Payload = []byte(strings.Repeat("x", maxTotalLength)) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := specExamplePacket
			tt.modify(&p)
			if b, err := p.Marshal(); err == nil {
				t.Errorf("Marshal returned %d octets, want an error", len(b))
			}
		})
	}
}
