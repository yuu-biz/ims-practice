package sms

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

// The numbers are fictitious (555-01xx). UE-A is +1 202 555 0101 and
// UE-B is +1 202 555 0102.
var (
	ueA = InternationalNumber("+12025550101")
	ueB = InternationalNumber("+12025550102")
)

// 2026-11-19 10:30:00 at UTC+9 is 62 11 91 01 03 00 63: two swapped
// digits per field, and 36 quarter hours for the zone.
var (
	jst      = time.FixedZone("", 9*60*60)
	scts     = time.Date(2026, 11, 19, 10, 30, 0, 0, jst)
	sctsHex  = "62 11 91 01 03 00 63"
	hello    = UserData{Length: 5, Data: []byte{0xe8, 0x32, 0x9b, 0xfd, 0x06}}
	hellox2  = UserData{Length: 10, Data: []byte{0xe8, 0x32, 0x9b, 0xfd, 0x46, 0x97, 0xd9, 0xec, 0x37}}
	japanese = UserData{Length: 10, Data: []byte{0x30, 0x53, 0x30, 0x93, 0x30, 0x6b, 0x30, 0x61, 0x30, 0x6f}}
)

func TestTPDUs(t *testing.T) {
	tests := []struct {
		name  string
		tpdu  TPDU
		hex   string
		parse func([]byte) (TPDU, error)
	}{
		{
			name: "SMS-SUBMIT, 7 bit",
			tpdu: Submit{MessageReference: 1, DestinationAddress: ueB, UserData: hellox2},
			// TP-DA: 11 digits (not octets), international E.164.
			hex:   "01 01 0B 91 21 20 55 05 01 F2 00 00 0A E8329BFD4697D9EC37",
			parse: wrap(parseSubmit),
		},
		{
			name: "SMS-SUBMIT, UCS2, TP-RD, TP-SRR, relative TP-VP",
			tpdu: Submit{
				RejectDuplicates: true, StatusReportRequest: true, MessageReference: 2,
				DestinationAddress: ueB, DataCodingScheme: DCSUCS2,
				ValidityPeriod: ValidityPeriod{Format: ValidityPeriodRelative, Relative: 167},
				UserData:       japanese,
			},
			// 0x35: TP-MTI 01, TP-RD, TP-VPF 10, TP-SRR. TP-VP 167 is one day.
			hex:   "35 02 0B 91 21 20 55 05 01 F2 00 08 A7 0A 30533093306B3061306F",
			parse: wrap(parseSubmit),
		},
		{
			name: "SMS-DELIVER",
			tpdu: Deliver{OriginatingAddress: ueA, ServiceCentreTimeStamp: scts, UserData: hellox2},
			// 0x04: TP-MMS set, which means no more messages.
			hex:   "04 0B 91 21 20 55 05 01 F1 00 00" + sctsHex + "0A E8329BFD4697D9EC37",
			parse: wrap(parseDeliver),
		},
		{
			name: "SMS-DELIVER, more messages, TP-SRI, west of UTC",
			tpdu: Deliver{
				MoreMessagesToSend: true, StatusReportIndication: true, OriginatingAddress: ueA,
				DataCodingScheme:       DCSUCS2,
				ServiceCentreTimeStamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("", -5*60*60)),
				UserData:               japanese,
			},
			// UTC-5 is 20 quarter hours with the sign bit: 0x0A.
			hex:   "20 0B 91 21 20 55 05 01 F1 00 08 62 10 20 30 40 50 0A 0A 30533093306B3061306F",
			parse: wrap(parseDeliver),
		},
		{
			name:  "SMS-SUBMIT-REPORT for RP-ACK",
			tpdu:  SubmitReport{ServiceCentreTimeStamp: scts},
			hex:   "01 00" + sctsHex,
			parse: wrapReport(parseSubmitReport, false),
		},
		{
			name:  "SMS-SUBMIT-REPORT for RP-ERROR",
			tpdu:  SubmitReport{FailureCause: FCSInvalidSMEAddress, ServiceCentreTimeStamp: scts},
			hex:   "01 C3 00" + sctsHex,
			parse: wrapReport(parseSubmitReport, true),
		},
		{
			name:  "SMS-DELIVER-REPORT for RP-ACK",
			tpdu:  DeliverReport{},
			hex:   "00 00",
			parse: wrapReport(parseDeliverReport, false),
		},
		{
			name:  "SMS-DELIVER-REPORT for RP-ERROR",
			tpdu:  DeliverReport{FailureCause: FCSMemoryCapacityExceeded},
			hex:   "00 D3 00",
			parse: wrapReport(parseDeliverReport, true),
		},
		{
			name: "SMS-DELIVER-REPORT with every optional parameter",
			tpdu: DeliverReport{OptionalParameters: OptionalParameters{
				ParameterIndicator: PIProtocolIdentifier | PIDataCodingScheme | PIUserDataLength,
				UserData:           hello,
			}},
			hex:   "00 07 00 00 05 E8329BFD06",
			parse: wrapReport(parseDeliverReport, false),
		},
		{
			name: "SMS-STATUS-REPORT",
			tpdu: StatusReport{
				MessageReference: 1, RecipientAddress: ueB, ServiceCentreTimeStamp: scts,
				DischargeTime: scts.Add(5 * time.Second), Status: StatusReceivedBySME,
			},
			// 0x06: TP-MTI 10 and TP-MMS (no more). No TP-PI: nothing follows.
			hex:   "06 01 0B 91 21 20 55 05 01 F2" + sctsHex + "62 11 91 01 03 50 63 00",
			parse: wrap(parseStatusReport),
		},
		{
			name: "SMS-STATUS-REPORT with TP-UD and no TP-DCS",
			tpdu: StatusReport{
				MessageReference: 1, RecipientAddress: ueB, ServiceCentreTimeStamp: scts,
				DischargeTime: scts, Status: StatusNotObtainable,
				OptionalParameters: OptionalParameters{ParameterIndicator: PIUserDataLength, UserData: hello},
			},
			hex:   "06 01 0B 91 21 20 55 05 01 F2" + sctsHex + sctsHex + "43 04 05 E8329BFD06",
			parse: wrap(parseStatusReport),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := mustHex(t, tt.hex)
			got, err := tt.tpdu.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("Marshal = % X\nwant      % X", got, want)
			}
			parsed, err := tt.parse(want)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(parsed, tt.tpdu) {
				t.Errorf("parsed %+v\nwant   %+v", parsed, tt.tpdu)
			}
		})
	}
}

func wrap[T TPDU](parse func([]byte) (T, error)) func([]byte) (TPDU, error) {
	return func(b []byte) (TPDU, error) { return parse(b) }
}

func wrapReport[T TPDU](parse func([]byte, bool) (T, error), forRPError bool) func([]byte) (TPDU, error) {
	return func(b []byte) (TPDU, error) { return parse(b, forRPError) }
}

func TestDeliverKeepsUserDataHeader(t *testing.T) {
	// A part of a concatenated message: UDHL 5, IEI 00, length 3,
	// reference CC, 2 parts, part 1, then "Hi" in UCS2.
	b := mustHex(t, "44 0B 91 21 20 55 05 01 F1 00 08"+sctsHex+"0A 050003CC0201 00480069")
	m, err := parseDeliver(b)
	if err != nil {
		t.Fatal(err)
	}
	if !m.UserData.HeaderIndicator || !bytes.Equal(m.UserData.Data, b[len(b)-10:]) {
		t.Errorf("user data %+v, want the header and text as received", m.UserData)
	}
	if _, err := DecodeText(m.DataCodingScheme, m.UserData); !errors.Is(err, ErrUserDataHeader) {
		t.Errorf("DecodeText: %v, want ErrUserDataHeader", err)
	}
	again, _ := m.Marshal()
	if !bytes.Equal(again, b) {
		t.Errorf("Marshal = % X, want the PDU as received", again)
	}
}

func TestParameterIndicatorReservedBits(t *testing.T) {
	// TP-PI 0x0C: TP-UDL and a reserved bit. The octets after TP-UD
	// belong to a later release and are discarded.
	r, err := parseDeliverReport(mustHex(t, "00 0C 05 E8329BFD06 AABB"), false)
	if err != nil {
		t.Fatal(err)
	}
	if r.ParameterIndicator != PIUserDataLength || !reflect.DeepEqual(r.UserData, hello) {
		t.Errorf("got %+v", r)
	}
	// An extension octet after TP-PI is skipped.
	r, err = parseDeliverReport(mustHex(t, "00 80 00"), false)
	if err != nil || r.ParameterIndicator != 0 {
		t.Errorf("with an extension octet: %+v, %v", r, err)
	}
}

func TestParseTPDUErrors(t *testing.T) {
	tests := []struct {
		name  string
		hex   string
		parse func([]byte) (TPDU, error)
	}{
		{"empty", "", wrap(parseSubmit)},
		{"truncated user data", "01 01 0B 91 21 20 55 05 01 F2 00 00 0A E8329B", wrap(parseSubmit)},
		{"octets left over", "01 01 0B 91 21 20 55 05 01 F2 00 00 00 FF", wrap(parseSubmit)},
		{"absolute TP-VP", "19 01 0B 91 21 20 55 05 01 F2 00 00" + sctsHex + "00", wrap(parseSubmit)},
		// TP-OA "ims" as GSM 7 bit characters: type of number 101.
		{"alphanumeric TP-OA", "04 06 D0 E9B61C 00 00" + sctsHex + "00", wrap(parseDeliver)},
		// TS 23.040 9.1.2.5: the whole address field is at most 12
		// octets, so at most 20 digits.
		{"TP-DA of 21 digits", "01 01 15 91 21 20 55 05 01 21 43 65 87 09 F1 00 00 00", wrap(parseSubmit)},
		{"time stamp digit", "04 0B 91 21 20 55 05 01 F1 00 00 6A 11 91 01 03 00 63 00", wrap(parseDeliver)},
		{"wrong TP-MTI in a report", "00 00" + sctsHex, wrapReport(parseSubmitReport, false)},
	}
	for _, tt := range tests {
		if _, err := tt.parse(mustHex(t, tt.hex)); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

func TestMarshalTPDUErrors(t *testing.T) {
	tests := []struct {
		name string
		tpdu TPDU
	}{
		{"TP-UDL does not match TP-UD", Submit{DestinationAddress: ueB, UserData: UserData{Length: 6, Data: hello.Data}}},
		{"absolute TP-VP", Submit{DestinationAddress: ueB, ValidityPeriod: ValidityPeriod{Format: ValidityPeriodAbsolute}}},
		{"not a digit", Submit{DestinationAddress: InternationalNumber("+1-202")}},
		{"too many digits", Submit{DestinationAddress: InternationalNumber("+123456789012345678901")}},
		{"alphanumeric", Submit{DestinationAddress: Address{TypeOfNumber: TONAlphanumeric}}},
		{"year", Deliver{OriginatingAddress: ueA, ServiceCentreTimeStamp: time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)}},
		{"zone", Deliver{OriginatingAddress: ueA, ServiceCentreTimeStamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("", 10*60))}},
		{"user data without TP-PI", DeliverReport{OptionalParameters: OptionalParameters{UserData: hello}}},
	}
	for _, tt := range tests {
		if _, err := tt.tpdu.Marshal(); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

// TestUserDataLength is the room for the short message itself: TP-UD
// of SMS-SUBMIT and SMS-DELIVER is at most 140 octets, which is 160
// septets of the GSM 7 bit default alphabet (TS 23.040 9.2.2.1, 9.2.2.2,
// 9.2.3.16). A TP-UDL beyond that is refused both ways, even when the
// octets are there.
func TestUserDataLength(t *testing.T) {
	tests := []struct {
		name   string
		dcs    DataCodingScheme
		length int // TP-UDL
		octets int // TP-UD
		ok     bool
	}{
		{"160 septets", DCSGSM7, 160, 140, true},
		{"161 septets", DCSGSM7, 161, 141, false},
		{"255 septets", DCSGSM7, 255, 224, false},
		{"140 octets of UCS2", DCSUCS2, 140, 140, true},
		{"141 octets of UCS2", DCSUCS2, 141, 141, false},
	}
	for _, tt := range tests {
		ud := UserData{Length: uint8(tt.length), Data: bytes.Repeat([]byte{0x41}, tt.octets)}
		submit := Submit{DestinationAddress: ueB, DataCodingScheme: tt.dcs, UserData: ud}
		deliver := Deliver{OriginatingAddress: ueA, DataCodingScheme: tt.dcs, ServiceCentreTimeStamp: scts, UserData: ud}
		for _, pdu := range []struct {
			tpdu   TPDU
			header string
			parse  func([]byte) (TPDU, error)
		}{
			{submit, "01 00 0B 91 21 20 55 05 01 F2 00", wrap(parseSubmit)},
			{deliver, "04 0B 91 21 20 55 05 01 F1 00", wrap(parseDeliver)},
		} {
			_, err := pdu.tpdu.Marshal()
			if (err == nil) != tt.ok {
				t.Errorf("%s, %T: Marshal error %v", tt.name, pdu.tpdu, err)
			}
			wire := mustHex(t, pdu.header)
			wire = append(wire, byte(tt.dcs))
			if _, ok := pdu.tpdu.(Deliver); ok {
				wire = append(wire, mustHex(t, sctsHex)...)
			}
			wire = append(append(wire, byte(tt.length)), ud.Data...)
			got, err := pdu.parse(wire)
			if (err == nil) != tt.ok {
				t.Errorf("%s, %T: parse error %v", tt.name, pdu.tpdu, err)
			}
			if tt.ok && err == nil && !reflect.DeepEqual(got, pdu.tpdu) {
				t.Errorf("%s: parsed %+v, want %+v", tt.name, got, pdu.tpdu)
			}
		}
	}
}

func TestAddress(t *testing.T) {
	tests := []struct {
		address Address
		tp, rp  string
	}{
		// Odd count: the last octet is filled with 1111.
		{ueA, "0B 91 21 20 55 05 01 F1", "07 91 21 20 55 05 01 F1"},
		// Even count: no filler, and the TP length is even.
		{Address{TypeOfNumber: TONNational, NumberingPlan: NPIISDNTelephone, Digits: "2025550100"}, "0A A1 02 52 55 10 00", "06 A1 02 52 55 10 00"},
		{Address{TypeOfNumber: TONUnknown, NumberingPlan: NPIISDNTelephone, Digits: "*#1"}, "03 81 BA F1", "03 81 BA F1"},
	}
	for _, tt := range tests {
		tp, err := appendTPAddress(nil, tt.address)
		if err != nil || !bytes.Equal(tp, mustHex(t, tt.tp)) {
			t.Errorf("TP address of %v = % X, %v, want %s", tt.address, tp, err, tt.tp)
		}
		rp, err := appendRPAddress(nil, tt.address)
		if err != nil || !bytes.Equal(rp, mustHex(t, tt.rp)) {
			t.Errorf("RP address of %v = % X, %v, want %s", tt.address, rp, err, tt.rp)
		}
		d := decoder{b: append(tp, rp...)}
		if got := d.tpAddress(); got != tt.address {
			t.Errorf("TP address read back as %+v", got)
		}
		if got := d.rpAddress(); got != tt.address || d.end() != nil {
			t.Errorf("RP address read back as %+v, %v", got, d.err)
		}
	}
	if got := ueA.String(); got != "+12025550101" {
		t.Errorf("String() = %q", got)
	}
	// A filler before the last semi-octet is skipped (TS 23.040 9.1.2.3).
	if got := decodeSemiOctets([]byte{0x21, 0x3f, 0x54}, 5); got != "12345" {
		t.Errorf("decodeSemiOctets skipped nothing: %q", got)
	}
}
