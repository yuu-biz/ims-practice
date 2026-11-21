package sms

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The SC address is fictitious too.
var sc = InternationalNumber("+12025550100")

func TestRPMessages(t *testing.T) {
	submit := "01 01 0B 91 21 20 55 05 01 F2 00 00 0A E8329BFD4697D9EC37"
	deliver := "04 0B 91 21 20 55 05 01 F1 00 00" + sctsHex + "0A E8329BFD4697D9EC37"
	tests := []struct {
		name string
		rp   RPMessage
		hex  string
		tpdu TPDU // what ParseTPDU gives, nil for none
	}{
		{
			name: "RP-DATA (MS to network) with SMS-SUBMIT",
			rp:   RPDataMSToNetwork{MessageReference: 5, DestinationAddress: sc, UserData: mustHex(t, submit)},
			// RP-OA has length 0; RP-DA has 7 octets: type and 6 of digits.
			hex:  "00 05 00 07 91 21 20 55 05 01 F0 16" + submit,
			tpdu: Submit{MessageReference: 1, DestinationAddress: ueB, UserData: hellox2},
		},
		{
			name: "RP-DATA (network to MS) with SMS-DELIVER",
			rp:   RPDataNetworkToMS{MessageReference: 6, OriginatorAddress: sc, UserData: mustHex(t, deliver)},
			hex:  "01 06 07 91 21 20 55 05 01 F0 00 1C" + deliver,
			tpdu: Deliver{OriginatingAddress: ueA, ServiceCentreTimeStamp: scts, UserData: hellox2},
		},
		{
			name: "RP-DATA (network to MS) with SMS-STATUS-REPORT",
			rp: RPDataNetworkToMS{MessageReference: 7, OriginatorAddress: sc,
				UserData: mustHex(t, "06 01 0B 91 21 20 55 05 01 F2"+sctsHex+sctsHex+"00")},
			hex: "01 07 07 91 21 20 55 05 01 F0 00 19 06 01 0B 91 21 20 55 05 01 F2" + sctsHex + sctsHex + "00",
			tpdu: StatusReport{MessageReference: 1, RecipientAddress: ueB, ServiceCentreTimeStamp: scts,
				DischargeTime: scts, Status: StatusReceivedBySME},
		},
		{
			name: "RP-ACK (network to MS) with SMS-SUBMIT-REPORT",
			rp:   RPAck{Direction: NetworkToMS, MessageReference: 5, UserData: mustHex(t, "01 00"+sctsHex)},
			hex:  "03 05 41 09 01 00" + sctsHex,
			tpdu: SubmitReport{ServiceCentreTimeStamp: scts},
		},
		{
			name: "RP-ACK (MS to network) with SMS-DELIVER-REPORT",
			rp:   RPAck{Direction: MSToNetwork, MessageReference: 6, UserData: mustHex(t, "00 00")},
			hex:  "02 06 41 02 00 00",
			tpdu: DeliverReport{},
		},
		{
			name: "RP-ACK without RP-User-Data",
			rp:   RPAck{Direction: MSToNetwork, MessageReference: 6},
			hex:  "02 06",
		},
		{
			name: "RP-ERROR (network to MS) with SMS-SUBMIT-REPORT",
			rp: RPError{Direction: NetworkToMS, MessageReference: 5, Cause: RPCauseUnknownSubscriber,
				UserData: mustHex(t, "01 C3 00"+sctsHex)},
			// RP-Cause: length 1, cause 30.
			hex:  "05 05 01 1E 41 0A 01 C3 00" + sctsHex,
			tpdu: SubmitReport{FailureCause: FCSInvalidSMEAddress, ServiceCentreTimeStamp: scts},
		},
		{
			name: "RP-ERROR (MS to network) with SMS-DELIVER-REPORT",
			rp: RPError{Direction: MSToNetwork, MessageReference: 6, Cause: RPCauseMemoryCapacityExceeded,
				UserData: mustHex(t, "00 D3 00")},
			hex:  "04 06 01 16 41 03 00 D3 00",
			tpdu: DeliverReport{FailureCause: FCSMemoryCapacityExceeded},
		},
		{
			name: "RP-ERROR with a diagnostic",
			rp:   RPError{Direction: MSToNetwork, MessageReference: 6, Cause: RPCauseProtocolErrorUnspecified, Diagnostic: []byte{7}},
			hex:  "04 06 02 6F 07",
		},
		{
			name: "RP-SMMA",
			rp:   RPSMMA{MessageReference: 8},
			hex:  "06 08",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustHex(t, tt.hex)
			got, err := tt.rp.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Marshal = % X\nwant      % X", got, want)
			}
			parsed, err := ParseRP(want)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parsed, tt.rp) {
				t.Errorf("ParseRP = %+v, want %+v", parsed, tt.rp)
			}
			if parsed.MTI() != RPMTI(want[0]) {
				t.Errorf("MTI() = %03b, want %03b", parsed.MTI(), want[0])
			}
			tpdu, err := ParseTPDU(parsed)
			if tt.tpdu == nil {
				if !errors.Is(err, ErrNoUserData) {
					t.Errorf("ParseTPDU = %+v, %v, want ErrNoUserData", tpdu, err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(tpdu, tt.tpdu) {
				t.Errorf("ParseTPDU = %+v, %v\nwant       %+v", tpdu, err, tt.tpdu)
			}
		})
	}
}

func TestParseRPTolerates(t *testing.T) {
	// TS 24.011 7.3.1: an RP-OA in RP-DATA from the MS is not rejected.
	m, err := ParseRP(mustHex(t, "00 05 07 91 21 20 55 05 01 F1 07 91 21 20 55 05 01 F0 02 00 00"))
	if err != nil {
		t.Fatal(err)
	}
	if data, ok := m.(RPDataMSToNetwork); !ok || data.DestinationAddress != sc {
		t.Errorf("got %+v", m)
	}
	// TS 24.007 11.2.4: an IE that the message does not know is skipped,
	// as TLV (bit 8 of the IEI 0) or as one octet (bit 8 set).
	for name, tt := range map[string]struct {
		hex  string
		want RPMessage
	}{
		"RP-ACK, unknown IE first": {"03 06 42 02 AA BB 41 02 01 00", RPAck{Direction: NetworkToMS, MessageReference: 6, UserData: []byte{0x01, 0x00}}},
		"RP-ACK, one-octet IE":     {"03 06 A1", RPAck{Direction: NetworkToMS, MessageReference: 6}},
		"RP-ERROR, unknown IE last": {"05 07 01 6F 41 01 01 50 00",
			RPError{Direction: NetworkToMS, MessageReference: 7, Cause: RPCauseProtocolErrorUnspecified, UserData: []byte{0x01}}},
		"RP-SMMA, unknown IE": {"06 08 42 00", RPSMMA{MessageReference: 8}},
	} {
		if m, err := ParseRP(mustHex(t, tt.hex)); err != nil || !reflect.DeepEqual(m, tt.want) {
			t.Errorf("%s: %+v, %v, want %+v", name, m, err, tt.want)
		}
	}
	// Bits 8..4 of the first octet are spare and ignored.
	if m, err := ParseRP(mustHex(t, "F6 08")); err != nil || m != (RPSMMA{MessageReference: 8}) {
		t.Errorf("spare bits: %+v, %v", m, err)
	}
	// TS 23.040 9.2.3.1: the MS takes a reserved TP-MTI as SMS-DELIVER.
	reserved := mustHex(t, "07 0B 91 21 20 55 05 01 F1 00 00"+sctsHex+"00")
	tpdu, err := ParseTPDU(RPDataNetworkToMS{OriginatorAddress: sc, UserData: reserved})
	if _, ok := tpdu.(Deliver); !ok || err != nil {
		t.Errorf("reserved TP-MTI: %+v, %v", tpdu, err)
	}
}

func TestParseRPErrors(t *testing.T) {
	for name, hex := range map[string]string{
		"empty":                  "",
		"reserved RP-MTI":        "07 00",
		"truncated RP-DA":        "00 05 00 07 91 21",
		"no RP-User-Data":        "00 05 00 07 91 21 20 55 05 01 F0 00",
		"truncated RP-User-Data": "00 05 00 07 91 21 20 55 05 01 F0 16 01 01",
		"empty RP-Cause":         "04 06 00",
		"truncated unknown IE":   "02 06 42 05 00",
		"octets left over":       "06 08 00",
		// TS 24.011 table 7.4 and 8.2.5.3: RP-User-Data is at most 233
		// octets with its length octet, so the TPDU is at most 232.
		// TS 24.011 8.2.5.2: the RP-DA has at most 11 octets.
		"RP-DA of 12 octets":         "00 05 00 0C 91 21 20 55 05 01 21 43 65 87 09 21 01 00",
		"RP-User-Data of 233 octets": "01 05 07 91 21 20 55 05 01 F0 00 E9" + strings.Repeat("00", 233),
		"RP-ACK user data of 233":    "02 06 41 E9" + strings.Repeat("00", 233),
	} {
		if m, err := ParseRP(mustHex(t, hex)); err == nil {
			t.Errorf("%s: got %+v, want an error", name, m)
		}
	}
}

func TestParseTPDUWrongPDU(t *testing.T) {
	deliver := mustHex(t, "04 0B 91 21 20 55 05 01 F1 00 00"+sctsHex+"00")
	if _, err := ParseTPDU(RPDataMSToNetwork{DestinationAddress: sc, UserData: deliver}); err == nil {
		t.Error("SMS-DELIVER in RP-DATA from the MS: no error")
	}
	command := mustHex(t, "02 01 00 00 01 0B 91 21 20 55 05 01 F2 00")
	if _, err := ParseTPDU(RPDataMSToNetwork{DestinationAddress: sc, UserData: command}); err == nil {
		t.Error("SMS-COMMAND: no error")
	}
	if _, err := ParseTPDU(RPDataNetworkToMS{OriginatorAddress: sc, UserData: mustHex(t, "01 00"+sctsHex)}); err == nil {
		t.Error("SMS-SUBMIT-REPORT in RP-DATA: no error")
	}
	if _, err := ParseTPDU(RPSMMA{}); !errors.Is(err, ErrNoUserData) {
		t.Errorf("RP-SMMA: %v, want ErrNoUserData", err)
	}
}

func TestMarshalRPErrors(t *testing.T) {
	tests := []struct {
		name string
		rp   RPMessage
	}{
		{"no RP-DA", RPDataMSToNetwork{UserData: []byte{1}}},
		{"no RP-OA", RPDataNetworkToMS{UserData: []byte{4}}},
		{"no TPDU", RPDataMSToNetwork{DestinationAddress: sc}},
		{"TPDU too long", RPAck{UserData: make([]byte, maxTPDU+1)}},
		{"cause beyond 7 bits", RPError{Cause: 128}},
	}
	for _, tt := range tests {
		if _, err := tt.rp.Marshal(); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

// Example shows the body of the SIP MESSAGE that UE-A sends to its SC,
// and how the IP-SM-GW reads it back.
func Example() {
	dcs, ud, _ := EncodeText("hellohello")
	submit, _ := Submit{
		MessageReference:   1,
		DestinationAddress: InternationalNumber("+12025550102"),
		DataCodingScheme:   dcs,
		UserData:           ud,
	}.Marshal()
	body, _ := RPDataMSToNetwork{
		MessageReference:   5,
		DestinationAddress: InternationalNumber("+12025550100"),
		UserData:           submit,
	}.Marshal()
	fmt.Printf("% X\n", body)

	rp, _ := ParseRP(body)
	tpdu, _ := ParseTPDU(rp)
	s := tpdu.(Submit)
	text, _ := DecodeText(s.DataCodingScheme, s.UserData)
	fmt.Println(s.DestinationAddress, text)
	// Output:
	// 00 05 00 07 91 21 20 55 05 01 F0 16 01 01 0B 91 21 20 55 05 01 F2 00 00 0A E8 32 9B FD 46 97 D9 EC 37
	// +12025550102 hellohello
}
