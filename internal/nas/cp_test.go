package nas

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The test vectors are written out by hand from the layouts of TS 24.011
// and TS 23.040. All numbers are fictitious: the SC is +1 202 555 0100,
// the sender +1 202 555 0112 and the recipient +1 202 555 0123.
const (
	// rpDataSubmit is RP-DATA (MS to network), TS 24.011 table 7.5:
	//
	//	00                       RP-MTI 000
	//	01                       RP-Message Reference
	//	00                       RP-Originator Address, empty
	//	07 91 21 20 55 05 01 F0  RP-Destination Address: the SC
	//	12                       RP-User Data, 18 octets
	//
	// and in it an SMS-SUBMIT, TS 23.040 9.2.2.2:
	//
	//	01                       TP-MTI 01, no TP-VP
	//	2A                       TP-MR
	//	0B 91 21 20 55 05 21 F3  TP-DA: 11 digits, international, E.164
	//	00 00                    TP-PID, TP-DCS (GSM 7 bit default alphabet)
	//	05 E8 32 9B FD 06        TP-UDL 5 septets, "hello"
	rpDataSubmit = "00 01 00 07 91 21 20 55 05 01 F0 12" +
		"01 2A 0B 91 21 20 55 05 21 F3 00 00 05 E8 32 9B FD 06"

	// rpDataDeliver is RP-DATA (network to MS), TS 24.011 table 7.4:
	//
	//	01                       RP-MTI 001
	//	02                       RP-Message Reference
	//	07 91 21 20 55 05 01 F0  RP-Originator Address: the SC
	//	00                       RP-Destination Address, empty
	//	18                       RP-User Data, 24 octets
	//
	// and in it an SMS-DELIVER, TS 23.040 9.2.2.1:
	//
	//	04                       TP-MTI 00, TP-MMS 1 (no more messages)
	//	0B 91 21 20 55 05 11 F2  TP-OA: the sender
	//	00 00                    TP-PID, TP-DCS
	//	62 11 02 60 83 13 00     TP-SCTS 2026-11-20 06:38:31 +00:00
	//	05 E8 32 9B FD 06        TP-UDL 5 septets, "hello"
	rpDataDeliver = "01 02 07 91 21 20 55 05 01 F0 00 18" +
		"04 0B 91 21 20 55 05 11 F2 00 00 62 11 02 60 83 13 00 05 E8 32 9B FD 06"

	// rpAckFromNetwork and rpAckFromMS are RP-ACK without RP-User Data
	// (TS 24.011 table 7.7): RP-MTI 011 and 010, then the reference.
	rpAckFromNetwork = "03 01"
	rpAckFromMS      = "02 02"
)

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCPMessages follows one mobile originated and one mobile terminated
// transfer through the CP layer (TS 24.011 5.3.2.2, annex B).
func TestCPMessages(t *testing.T) {
	// The UE starts the MO transaction with TI value 0; the network
	// starts the MT transaction with TI value 2.
	mo := TransactionIdentifier{Flag: TIFlagFromOriginator, Value: 0}
	mt := TransactionIdentifier{Flag: TIFlagFromOriginator, Value: 2}
	tests := []struct {
		name string
		cp   CPMessage
		hex  string
	}{
		{
			name: "MO: CP-DATA with RP-DATA from the UE",
			cp:   CPData{TransactionIdentifier: mo, UserData: mustHex(t, rpDataSubmit)},
			// TI flag 0, TI value 000, PD 1001; CP-DATA; 30 octets.
			hex: "09 01 1E" + rpDataSubmit,
		},
		{
			name: "MO: CP-ACK from the network",
			cp:   CPAck{TransactionIdentifier: mo.Opposite()},
			// TI flag 1: sent to the side that originated the TI.
			hex: "89 04",
		},
		{
			name: "MO: CP-DATA with RP-ACK from the network",
			cp:   CPData{TransactionIdentifier: mo.Opposite(), UserData: mustHex(t, rpAckFromNetwork)},
			hex:  "89 01 02" + rpAckFromNetwork,
		},
		{
			name: "MO: CP-ACK from the UE",
			cp:   CPAck{TransactionIdentifier: mo},
			hex:  "09 04",
		},
		{
			name: "MT: CP-DATA with RP-DATA from the network",
			cp:   CPData{TransactionIdentifier: mt, UserData: mustHex(t, rpDataDeliver)},
			// TI flag 0, TI value 010, PD 1001; CP-DATA; 36 octets.
			hex: "29 01 24" + rpDataDeliver,
		},
		{
			name: "MT: CP-ACK from the UE",
			cp:   CPAck{TransactionIdentifier: mt.Opposite()},
			hex:  "A9 04",
		},
		{
			name: "MT: CP-DATA with RP-ACK from the UE",
			cp:   CPData{TransactionIdentifier: mt.Opposite(), UserData: mustHex(t, rpAckFromMS)},
			hex:  "A9 01 02" + rpAckFromMS,
		},
		{
			name: "MT: CP-ACK from the network",
			cp:   CPAck{TransactionIdentifier: mt},
			hex:  "29 04",
		},
		{
			name: "CP-ERROR for a CP-ACK of an unknown transaction",
			cp:   CPError{TransactionIdentifier: TransactionIdentifier{Flag: TIFlagToOriginator, Value: 6}, Cause: CPCauseInvalidTransactionIdentifier},
			// TI flag 1, TI value 110, PD 1001; CP-ERROR; cause 81.
			hex: "E9 10 51",
		},
		{
			name: "CP-ERROR with a cause that Table 8.2 does not list",
			cp:   CPError{TransactionIdentifier: mo, Cause: 42},
			hex:  "09 10 2A",
		},
		{
			name: "CP-DATA with the largest RPDU",
			cp:   CPData{TransactionIdentifier: mo, UserData: bytes.Repeat([]byte{0xAA}, maxRPDU)},
			hex:  "09 01 F8" + strings.Repeat("AA", maxRPDU),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustHex(t, tt.hex)
			got, err := tt.cp.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Marshal = % X\nwant      % X", got, want)
			}
			parsed, err := ParseCP(want)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parsed, tt.cp) {
				t.Errorf("ParseCP = %+v, want %+v", parsed, tt.cp)
			}
			if parsed.MessageType() != CPMessageType(want[1]) {
				t.Errorf("MessageType() = %s, want %#02x", parsed.MessageType(), want[1])
			}
			if ti := parsed.TI(); ti.Flag != TIFlag(want[0]>>7) || ti.Value != want[0]>>4&7 {
				t.Errorf("TI() = %+v, first octet is %#02x", ti, want[0])
			}
		})
	}
}

func TestParseCPIgnoresSpareBitOfCause(t *testing.T) {
	// Bit 8 of CP-Cause is spare (TS 24.011 figure 8.3).
	m, err := ParseCP(mustHex(t, "09 10 E0"))
	if want := (CPError{Cause: CPCauseInvalidMandatoryInformation}); err != nil || m != want {
		t.Errorf("got %+v, %v, want %+v", m, err, want)
	}
}

// TestParseCPSkipsNonImperativePart checks that octets after the last
// element of a CP message do not make it an error (TS 24.007 11.2.4 and
// 11.2.5): a later version of TS 24.011 may add optional elements there.
func TestParseCPSkipsNonImperativePart(t *testing.T) {
	ti := TransactionIdentifier{Flag: TIFlagToOriginator, Value: 3} // first octet B9
	tests := []struct {
		name string
		hex  string
		want CPMessage
	}{
		// A TLV element with the unknown IEI 0x45 after CP-User data.
		{"CP-DATA", "B9 01 02 03 01 45 01 00", CPData{TransactionIdentifier: ti, UserData: []byte{0x03, 0x01}}},
		{"CP-ACK", "B9 04 45 01 00", CPAck{TransactionIdentifier: ti}},
		{"CP-ERROR", "B9 10 51 45 01 00", CPError{TransactionIdentifier: ti, Cause: CPCauseInvalidTransactionIdentifier}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCP(mustHex(t, tt.hex))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCP = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestParseCPMalformed checks the faults that TS 24.011 9.2.3 and 9.2.4
// let a receiver answer with CP-ERROR, and the cause for each.
func TestParseCPMalformed(t *testing.T) {
	ti := TransactionIdentifier{Flag: TIFlagToOriginator, Value: 3} // first octet B9
	tests := []struct {
		name  string
		hex   string
		cause CPCause
	}{
		{"unknown message type", "B9 02", CPCauseMessageTypeNonExistent},
		{"CP-DATA without CP-User data", "B9 01", CPCauseInvalidMandatoryInformation},
		{"CP-DATA with an empty CP-User data", "B9 01 00", CPCauseInvalidMandatoryInformation},
		{"CP-User data cut short", "B9 01 1E 00 01 00 07 91 21", CPCauseInvalidMandatoryInformation},
		{"CP-User data longer than 248 octets", "B9 01 F9" + strings.Repeat("AA", 249), CPCauseInvalidMandatoryInformation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ParseCP(mustHex(t, tt.hex))
			var malformed *MalformedCPMessage
			if !errors.As(err, &malformed) {
				t.Fatalf("got %+v, %v, want a *MalformedCPMessage", m, err)
			}
			if malformed.TransactionIdentifier != ti || malformed.Cause != tt.cause {
				t.Errorf("got TI %+v and cause %s, want %+v and %s", malformed.TransactionIdentifier, malformed.Cause, ti, tt.cause)
			}
		})
	}
}

// TestParseCPIgnored checks the messages that TS 24.011 9.2.1, 9.2.2 and
// 9.2.4 have a receiver ignore: the error is not a *MalformedCPMessage.
func TestParseCPIgnored(t *testing.T) {
	for name, hex := range map[string]string{
		"empty":                       "",
		"one octet":                   "09",
		"TI value 111":                "79 04",
		"protocol discriminator EMM":  "07 63",
		"CP-ERROR without CP-Cause":   "09 10",
		"TI value 111, flag set":      "F9 01 02 03 01",
		"protocol discriminator 5GMM": "7E 00",
	} {
		m, err := ParseCP(mustHex(t, hex))
		var malformed *MalformedCPMessage
		if err == nil || errors.As(err, &malformed) {
			t.Errorf("%s: got %+v, %v, want an error that is not a *MalformedCPMessage", name, m, err)
		}
		if tooShort := len(mustHex(t, hex)) < 2; errors.Is(err, ErrMessageTooShort) != tooShort {
			t.Errorf("%s: errors.Is(err, ErrMessageTooShort) = %t, want %t", name, !tooShort, tooShort)
		}
	}
}

func TestMarshalCPErrors(t *testing.T) {
	tests := []struct {
		name string
		cp   CPMessage
	}{
		{"TI value 7", CPAck{TransactionIdentifier: TransactionIdentifier{Value: 7}}},
		{"TI value beyond 3 bits", CPAck{TransactionIdentifier: TransactionIdentifier{Value: 8}}},
		{"TI flag beyond 1 bit", CPAck{TransactionIdentifier: TransactionIdentifier{Flag: 2}}},
		{"CP-DATA without an RPDU", CPData{}},
		{"RPDU too long", CPData{UserData: make([]byte, maxRPDU+1)}},
		{"CP-DATA with TI value 7", CPData{TransactionIdentifier: TransactionIdentifier{Value: 7}, UserData: []byte{2, 0}}},
		{"cause beyond 7 bits", CPError{Cause: 128}},
		{"CP-ERROR with TI value 7", CPError{TransactionIdentifier: TransactionIdentifier{Value: 7}, Cause: CPCauseCongestion}},
	}
	for _, tt := range tests {
		if b, err := tt.cp.Marshal(); err == nil {
			t.Errorf("%s: got % X, want an error", tt.name, b)
		}
	}
}

func TestCPNames(t *testing.T) {
	for got, want := range map[string]string{
		CPMessageTypeData.String():                  "CP-DATA",
		CPMessageTypeAck.String():                   "CP-ACK",
		CPMessageTypeError.String():                 "CP-ERROR",
		CPMessageType(2).String():                   "CP message type 0x02",
		CPCauseInvalidMandatoryInformation.String(): "#96 Invalid mandatory information",
		CPCause(42).String():                        "#42",
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

// ExampleParseCP shows how the SMC entity of the network handles the
// CP-DATA that starts a mobile originated transfer: it passes the RPDU
// up to the relay layer and answers with CP-ACK on the same transaction.
func ExampleParseCP() {
	received := []byte{0x09, 0x01, 0x02, 0x06, 0x05} // CP-DATA with RP-SMMA

	m, err := ParseCP(received)
	if err != nil {
		fmt.Println(err)
		return
	}
	data := m.(CPData)
	fmt.Printf("%s, TI flag %d, TI value %d, RPDU % X\n", data.MessageType(), data.TI().Flag, data.TI().Value, data.UserData)

	ack, _ := CPAck{TransactionIdentifier: data.TI().Opposite()}.Marshal()
	fmt.Printf("% X\n", ack)
	// Output:
	// CP-DATA, TI flag 0, TI value 0, RPDU 06 05
	// 89 04
}
