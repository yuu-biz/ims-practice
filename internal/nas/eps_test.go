package nas

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The CP messages of cp_test.go, as the NAS message container carries
// them.
const (
	cpDataSubmit  = "09 01 1E" + rpDataSubmit  // 33 octets
	cpDataDeliver = "29 01 24" + rpDataDeliver // 39 octets
)

func TestUplinkNASTransport(t *testing.T) {
	m := UplinkNASTransport{NASMessageContainer: mustHex(t, cpDataSubmit)}
	// Security header type 0000 and protocol discriminator 0111; message
	// type 0x63; the container has 33 octets.
	want := mustHex(t, "07 63 21"+cpDataSubmit)
	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Marshal = % X\nwant      % X", got, want)
	}
	parsed, err := ParseUplinkNASTransport(want)
	if err != nil || !reflect.DeepEqual(parsed, m) {
		t.Errorf("ParseUplinkNASTransport = %+v, %v\nwant %+v", parsed, err, m)
	}
}

func TestDownlinkNASTransport(t *testing.T) {
	m := DownlinkNASTransport{NASMessageContainer: mustHex(t, cpDataDeliver)}
	// Message type 0x62; the container has 39 octets.
	want := mustHex(t, "07 62 27"+cpDataDeliver)
	got, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Marshal = % X\nwant      % X", got, want)
	}
	parsed, err := ParseDownlinkNASTransport(want)
	if err != nil || !reflect.DeepEqual(parsed, m) {
		t.Errorf("ParseDownlinkNASTransport = %+v, %v\nwant %+v", parsed, err, m)
	}
}

// TestEMMNASTransportContainerSizes checks the two ends of the LV
// element of 3 to 252 octets: a CP-ACK, and a CP-DATA with the largest
// RPDU.
func TestEMMNASTransportContainerSizes(t *testing.T) {
	largest, err := CPData{UserData: bytes.Repeat([]byte{0xAA}, maxRPDU)}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range [][]byte{mustHex(t, "89 04"), largest} {
		b, err := DownlinkNASTransport{NASMessageContainer: container}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		if len(b) != 3+len(container) || int(b[2]) != len(container) {
			t.Errorf("container of %d octets: message of %d octets with length octet %d", len(container), len(b), b[2])
		}
		parsed, err := ParseDownlinkNASTransport(b)
		if err != nil || !bytes.Equal(parsed.NASMessageContainer, container) {
			t.Errorf("container of %d octets: parsed %d octets, %v", len(container), len(parsed.NASMessageContainer), err)
		}
	}
}

func TestParseEMMNASTransportIgnoresLaterElements(t *testing.T) {
	// TS 24.301 7.6.1: an information element that the receiver does
	// not know is ignored. 0x5A with one octet stands for one here.
	m, err := ParseUplinkNASTransport(mustHex(t, "07 63 02 09 04 5A 01 00"))
	if err != nil || !bytes.Equal(m.NASMessageContainer, []byte{0x09, 0x04}) {
		t.Errorf("got %+v, %v", m, err)
	}
}

func TestParseEMMNASTransportErrors(t *testing.T) {
	tests := []struct {
		name string
		hex  string
		want string // part of the error text
	}{
		{"empty", "", "too short"},
		{"one octet", "07", "too short"},
		{"protocol discriminator of the CP layer", "09 01 02 03 01", "protocol discriminator 1001"},
		{"integrity protected", "17 00 00 00 00 00 07 63 02 09 04", "security header type 0001"},
		{"integrity protected and ciphered", "27 00 00 00 00 00 07 63 02 09 04", "security header type 0010"},
		{"the message of the other direction", "07 62 02 09 04", "message type 0x62 is not UPLINK NAS TRANSPORT"},
		{"another EMM message", "07 41", "message type 0x41"},
		{"no NAS message container", "07 63", "truncated before its length"},
		{"container cut short", "07 63 21 09 01 1E 00 01", "its length says 33 octets, 5 follow"},
		{"container too short for a CP message", "07 63 01 09", "NAS message container of 1 octets"},
		{"empty container", "07 63 00", "NAS message container of 0 octets"},
		{"container beyond 251 octets", "07 63 FC" + strings.Repeat("AA", 252), "NAS message container of 252 octets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseUplinkNASTransport(mustHex(t, tt.hex))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("got %+v, %v, want an error with %q", m, err, tt.want)
			}
			if tooShort := len(mustHex(t, tt.hex)) < 2; errors.Is(err, ErrMessageTooShort) != tooShort {
				t.Errorf("errors.Is(err, ErrMessageTooShort) = %t, want %t", !tooShort, tooShort)
			}
		})
	}
	if m, err := ParseDownlinkNASTransport(mustHex(t, "07 63 02 09 04")); err == nil {
		t.Errorf("UPLINK NAS TRANSPORT read as downlink: %+v", m)
	}
}

func TestMarshalEMMNASTransportErrors(t *testing.T) {
	for _, container := range [][]byte{nil, {0x09}, make([]byte, maxNASMessageContainer+1)} {
		if b, err := (UplinkNASTransport{NASMessageContainer: container}).Marshal(); err == nil {
			t.Errorf("uplink, container of %d octets: got % X, want an error", len(container), b)
		}
		if b, err := (DownlinkNASTransport{NASMessageContainer: container}).Marshal(); err == nil {
			t.Errorf("downlink, container of %d octets: got % X, want an error", len(container), b)
		}
	}
}

// Example_epsMobileOriginated shows the first message of a mobile
// originated short message in EPS, as the UE builds it and as the MME
// takes it apart. The RPDU would come from the RP layer; here it is
// RP-SMMA.
func Example_epsMobileOriginated() {
	rpdu := []byte{0x06, 0x05}

	// The UE: the SMC entity starts a transaction, and the EMM entity
	// carries its CP-DATA (TS 24.301 5.6.3.2).
	cp, _ := CPData{
		TransactionIdentifier: TransactionIdentifier{Flag: TIFlagFromOriginator, Value: 0},
		UserData:              rpdu,
	}.Marshal()
	uplink, _ := UplinkNASTransport{NASMessageContainer: cp}.Marshal()
	fmt.Printf("% X\n", uplink)

	// The MME: the EMM entity hands the container to the SMC entity,
	// which answers with CP-ACK in a DOWNLINK NAS TRANSPORT.
	received, _ := ParseUplinkNASTransport(uplink)
	m, _ := ParseCP(received.NASMessageContainer)
	data := m.(CPData)
	fmt.Printf("%s, RPDU % X\n", data.MessageType(), data.UserData)

	ack, _ := CPAck{TransactionIdentifier: data.TI().Opposite()}.Marshal()
	downlink, _ := DownlinkNASTransport{NASMessageContainer: ack}.Marshal()
	fmt.Printf("% X\n", downlink)
	// Output:
	// 07 63 05 09 01 02 06 05
	// CP-DATA, RPDU 06 05
	// 07 62 02 89 04
}
