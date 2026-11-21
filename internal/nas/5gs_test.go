package nas

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestULNASTransport(t *testing.T) {
	m := ULNASTransport{PayloadContainerType: PayloadContainerTypeSMS, PayloadContainer: mustHex(t, cpDataSubmit)}
	// Extended protocol discriminator 0x7E; spare half octet and
	// security header type 0000; message type 0x67; spare half octet and
	// payload container type 0010; the container has 33 octets, in a
	// length of two octets.
	want := mustHex(t, "7E 00 67 02 00 21"+cpDataSubmit)
	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Marshal = % X\nwant      % X", got, want)
	}
	parsed, err := ParseULNASTransport(want)
	if err != nil || !reflect.DeepEqual(parsed, m) {
		t.Errorf("ParseULNASTransport = %+v, %v\nwant %+v", parsed, err, m)
	}
}

func TestDLNASTransport(t *testing.T) {
	m := DLNASTransport{PayloadContainerType: PayloadContainerTypeSMS, PayloadContainer: mustHex(t, cpDataDeliver)}
	// Message type 0x68; the container has 39 octets.
	want := mustHex(t, "7E 00 68 02 00 27"+cpDataDeliver)
	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Marshal = % X\nwant      % X", got, want)
	}
	parsed, err := ParseDLNASTransport(want)
	if err != nil || !reflect.DeepEqual(parsed, m) {
		t.Errorf("ParseDLNASTransport = %+v, %v\nwant %+v", parsed, err, m)
	}
}

// TestPayloadContainerLength checks that the length of the LV-E element
// is written and read as two octets, most significant first.
func TestPayloadContainerLength(t *testing.T) {
	container := bytes.Repeat([]byte{0xAA}, 0x0123)
	b, err := DLNASTransport{PayloadContainerType: PayloadContainerTypeSMS, PayloadContainer: container}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b[:6], mustHex(t, "7E 00 68 02 01 23")) || len(b) != 6+len(container) {
		t.Errorf("message of %d octets starts with % X", len(b), b[:6])
	}
	parsed, err := ParseDLNASTransport(b)
	if err != nil || !bytes.Equal(parsed.PayloadContainer, container) {
		t.Errorf("parsed %d octets, %v", len(parsed.PayloadContainer), err)
	}
}

func TestParse5GMMNASTransportOtherPayloads(t *testing.T) {
	// A UL NAS TRANSPORT with "N1 SM information" (0001), a one octet
	// payload and the PDU session ID element (IEI 0x12) of TS 24.501
	// 8.2.10.2. The elements after the payload container are not read.
	m, err := ParseULNASTransport(mustHex(t, "7E 00 67 01 00 01 AA 12 05"))
	want := ULNASTransport{PayloadContainerType: 0b0001, PayloadContainer: []byte{0xAA}}
	if err != nil || !reflect.DeepEqual(m, want) {
		t.Errorf("got %+v, %v, want %+v", m, err, want)
	}
	// The spare half octets are not checked (TS 24.501 9.5).
	spare, err := ParseDLNASTransport(mustHex(t, "7E F0 68 F2 00 02 09 04"))
	if err != nil || spare.PayloadContainerType != PayloadContainerTypeSMS {
		t.Errorf("spare bits set: got %+v, %v", spare, err)
	}
}

func TestParse5GMMNASTransportErrors(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want string // part of the error text
	}{
		{"empty", "", "too short"},
		{"two octets", "7E 00", "too short"},
		{"EPS message", "07 63 02 09 04", "extended protocol discriminator 0x07"},
		{"5GSM message", "2E 01 01 C1", "extended protocol discriminator 0x2e"},
		{"integrity protected", "7E 01 00 00 00 00 00 7E 00 67 02 00 02 09 04", "security header type 0001"},
		{"integrity protected and ciphered", "7E 02 00 00 00 00 00 7E 00 67 02 00 02 09 04", "security header type 0010"},
		{"the message of the other direction", "7E 00 68 02 00 02 09 04", "message type 0x68 is not UL NAS TRANSPORT"},
		{"another 5GMM message", "7E 00 41", "message type 0x41"},
		{"no payload container type", "7E 00 67", "truncated before the Payload container type"},
		{"no payload container", "7E 00 67 02", "truncated before its length"},
		{"half a length", "7E 00 67 02 00", "truncated before its length"},
		{"container cut short", "7E 00 67 02 00 21 09 01 1E", "its length says 33 octets, 3 follow"},
		{"empty container", "7E 00 67 02 00 00", "Payload container is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseULNASTransport(mustHex(t, tt.hex))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %+v, %v, want an error with %q", m, err, tt.want)
			}
			if tooShort := len(mustHex(t, tt.hex)) < 3; errors.Is(err, ErrMessageTooShort) != tooShort {
				t.Errorf("errors.Is(err, ErrMessageTooShort) = %t, want %t", !tooShort, tooShort)
			}
		})
	}
	if m, err := ParseDLNASTransport(mustHex(t, "7E 00 67 02 00 02 09 04")); err == nil {
		t.Errorf("UL NAS TRANSPORT read as DL: %+v", m)
	}
}

func TestMarshal5GMMNASTransportErrors(t *testing.T) {
	cp := []byte{0x09, 0x04}
	tests := []struct {
		name      string
		t         PayloadContainerType
		container []byte
	}{
		{"no payload container type", 0, cp},
		{"N1 SM information", 0b0001, cp},
		{"empty container", PayloadContainerTypeSMS, nil},
		{"container beyond the two octet length", PayloadContainerTypeSMS, make([]byte, maxPayloadContainer+1)},
	}
	for _, tt := range tests {
		if b, err := (ULNASTransport{PayloadContainerType: tt.t, PayloadContainer: tt.container}).Marshal(); err == nil {
			t.Errorf("UL, %s: got %d octets, want an error", tt.name, len(b))
		}
		if b, err := (DLNASTransport{PayloadContainerType: tt.t, PayloadContainer: tt.container}).Marshal(); err == nil {
			t.Errorf("DL, %s: got %d octets, want an error", tt.name, len(b))
		}
	}
}

func TestPayloadContainerTypeNames(t *testing.T) {
	if got := PayloadContainerTypeSMS.String(); got != "SMS" {
		t.Errorf("got %q", got)
	}
	if got := PayloadContainerType(1).String(); got != "Payload container type 0001" {
		t.Errorf("got %q", got)
	}
}

// Example_fiveGSMobileTerminated shows the first message of a mobile
// terminated short message in 5GS. The SMSF builds the CP-DATA, the AMF
// only wraps it, and the UE takes it apart. The RPDU would come from the
// RP layer; here it is an RP-ACK, to keep the output short.
func Example_fiveGSMobileTerminated() {
	rpdu := []byte{0x03, 0x01}

	// The SMSF: its SMC entity starts a transaction.
	cp, _ := CPData{
		TransactionIdentifier: TransactionIdentifier{Flag: TIFlagFromOriginator, Value: 2},
		UserData:              rpdu,
	}.Marshal()

	// The AMF: it carries what the SMSF handed it (TS 24.501 5.4.5.3.2).
	downlink, _ := DLNASTransport{PayloadContainerType: PayloadContainerTypeSMS, PayloadContainer: cp}.Marshal()
	fmt.Printf("% X\n", downlink)

	// The UE: the 5GMM entity hands a payload of type SMS to the SMC
	// entity, which answers with CP-ACK in a UL NAS TRANSPORT.
	received, _ := ParseDLNASTransport(downlink)
	if received.PayloadContainerType != PayloadContainerTypeSMS {
		return
	}
	m, _ := ParseCP(received.PayloadContainer)
	data := m.(CPData)
	fmt.Printf("%s, TI value %d, RPDU % X\n", data.MessageType(), data.TI().Value, data.UserData)

	ack, _ := CPAck{TransactionIdentifier: data.TI().Opposite()}.Marshal()
	uplink, _ := ULNASTransport{PayloadContainerType: PayloadContainerTypeSMS, PayloadContainer: ack}.Marshal()
	fmt.Printf("% X\n", uplink)
	// Output:
	// 7E 00 68 02 00 05 29 01 02 03 01
	// CP-DATA, TI value 2, RPDU 03 01
	// 7E 00 67 02 00 02 A9 04
}
