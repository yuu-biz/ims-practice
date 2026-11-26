package sms

import (
	"errors"
	"fmt"
	"time"
)

// TPDU is one PDU of the transfer layer: Submit, Deliver, SubmitReport,
// DeliverReport or StatusReport. It travels as the RP-User-Data of an
// RP message; ParseTPDU decodes it from there.
type TPDU interface {
	Marshal() ([]byte, error)
	mti() tpMTI
}

// tpMTI is TP-Message-Type-Indicator, bits 1..0 of the first octet
// (TS 23.040 9.2.3.1). The same value names a different PDU in each
// direction, so the direction must be known to read it.
type tpMTI uint8

const (
	mtiDeliver      tpMTI = 0b00 // SMS-DELIVER (SC to MS), SMS-DELIVER-REPORT (MS to SC)
	mtiSubmit       tpMTI = 0b01 // SMS-SUBMIT (MS to SC), SMS-SUBMIT-REPORT (SC to MS)
	mtiStatusReport tpMTI = 0b10 // SMS-STATUS-REPORT (SC to MS), SMS-COMMAND (MS to SC)
	mtiReserved     tpMTI = 0b11
)

// The other bits of the first octet (TS 23.040 9.2.3.2 to 9.2.3.28).
const (
	bitMoreMessagesToSend   = 1 << 2 // TP-MMS in SMS-DELIVER and SMS-STATUS-REPORT
	bitRejectDuplicates     = 1 << 2 // TP-RD in SMS-SUBMIT
	bitLoopPrevention       = 1 << 3 // TP-LP in SMS-DELIVER and SMS-STATUS-REPORT
	bitStatusReport         = 1 << 5 // TP-SRI, TP-SRR or TP-SRQ, by PDU
	bitUserDataHeader       = 1 << 6 // TP-UDHI
	bitReplyPath            = 1 << 7 // TP-RP
	validityPeriodFormatBit = 3      // TP-VPF is bits 4..3 of SMS-SUBMIT
)

// Submit is SMS-SUBMIT, a short message from the MS to the SC (TS
// 23.040 9.2.2.2).
type Submit struct {
	RejectDuplicates    bool // TP-RD
	ReplyPath           bool // TP-RP
	StatusReportRequest bool // TP-SRR: send an SMS-STATUS-REPORT back
	MessageReference    uint8
	DestinationAddress  Address
	ProtocolIdentifier  uint8 // TP-PID; 0 is a plain short message (9.2.3.9)
	DataCodingScheme    DataCodingScheme
	ValidityPeriod      ValidityPeriod
	UserData            UserData
}

// ValidityPeriod is TP-VPF with TP-VP (TS 23.040 9.2.3.3, 9.2.3.12).
//
// SIMPLIFIED: TS 23.040 9.2.3.12.2 and 9.2.3.12.3. Only "not present"
// and the relative format are read and written; an SMS-SUBMIT with the
// absolute or enhanced format is refused. The UE of this project asks
// for no validity period, so the Service Centre keeps every short
// message for its default period.
type ValidityPeriod struct {
	Format ValidityPeriodFormat
	// Relative is TP-VP in the relative format: 0 to 143 are steps of 5
	// minutes, 144 to 167 steps of 30 minutes after 12 hours, 168 to 196
	// days and 197 to 255 weeks (9.2.3.12.1).
	Relative uint8
}

// ValidityPeriodFormat is TP-VPF (TS 23.040 9.2.3.3).
type ValidityPeriodFormat uint8

const (
	ValidityPeriodNotPresent ValidityPeriodFormat = 0b00
	ValidityPeriodEnhanced   ValidityPeriodFormat = 0b01
	ValidityPeriodRelative   ValidityPeriodFormat = 0b10
	ValidityPeriodAbsolute   ValidityPeriodFormat = 0b11
)

func (Submit) mti() tpMTI { return mtiSubmit }

// Marshal writes the layout of TS 23.040 9.2.2.2.
func (s Submit) Marshal() ([]byte, error) {
	first := byte(mtiSubmit) | byte(s.ValidityPeriod.Format)<<validityPeriodFormatBit
	first |= flag(s.RejectDuplicates, bitRejectDuplicates) | flag(s.StatusReportRequest, bitStatusReport) |
		flag(s.UserData.HeaderIndicator, bitUserDataHeader) | flag(s.ReplyPath, bitReplyPath)
	b, err := appendTPAddress([]byte{first, s.MessageReference}, s.DestinationAddress)
	if err != nil {
		return nil, fmt.Errorf("sms: SMS-SUBMIT TP-DA: %w", err)
	}
	b = append(b, s.ProtocolIdentifier, byte(s.DataCodingScheme))
	switch s.ValidityPeriod.Format {
	case ValidityPeriodNotPresent:
	case ValidityPeriodRelative:
		b = append(b, s.ValidityPeriod.Relative)
	default:
		return nil, fmt.Errorf("sms: SMS-SUBMIT TP-VPF %02b is not supported", s.ValidityPeriod.Format)
	}
	return appendUserData(b, "SMS-SUBMIT", s.DataCodingScheme, s.UserData, maxOctets)
}

func parseSubmit(b []byte) (Submit, error) {
	d := decoder{b: b}
	first := d.octet()
	s := Submit{
		RejectDuplicates:    first&bitRejectDuplicates != 0,
		StatusReportRequest: first&bitStatusReport != 0,
		ReplyPath:           first&bitReplyPath != 0,
		ValidityPeriod:      ValidityPeriod{Format: ValidityPeriodFormat(first >> validityPeriodFormatBit & 0b11)},
	}
	s.MessageReference = d.octet()
	s.DestinationAddress = d.tpAddress()
	s.ProtocolIdentifier = d.octet()
	s.DataCodingScheme = DataCodingScheme(d.octet())
	switch s.ValidityPeriod.Format {
	case ValidityPeriodNotPresent:
	case ValidityPeriodRelative:
		s.ValidityPeriod.Relative = d.octet()
	default:
		d.fail(fmt.Errorf("TP-VPF %02b is not supported", s.ValidityPeriod.Format))
	}
	s.UserData = d.userData(s.DataCodingScheme, first&bitUserDataHeader != 0, maxOctets)
	if err := d.end(); err != nil {
		return Submit{}, fmt.Errorf("sms: SMS-SUBMIT: %w", err)
	}
	return s, nil
}

// Deliver is SMS-DELIVER, a short message from the SC to the MS (TS
// 23.040 9.2.2.1).
type Deliver struct {
	// MoreMessagesToSend is TP-MMS. On the wire the bit is inverted: 0
	// means more messages are waiting in the SC (9.2.3.2).
	MoreMessagesToSend     bool
	LoopPrevention         bool // TP-LP
	ReplyPath              bool // TP-RP
	StatusReportIndication bool // TP-SRI: the sender asked for a status report
	OriginatingAddress     Address
	ProtocolIdentifier     uint8
	DataCodingScheme       DataCodingScheme
	ServiceCentreTimeStamp time.Time
	UserData               UserData
}

func (Deliver) mti() tpMTI { return mtiDeliver }

// Marshal writes the layout of TS 23.040 9.2.2.1.
func (m Deliver) Marshal() ([]byte, error) {
	first := byte(mtiDeliver) | flag(!m.MoreMessagesToSend, bitMoreMessagesToSend) |
		flag(m.LoopPrevention, bitLoopPrevention) | flag(m.StatusReportIndication, bitStatusReport) |
		flag(m.UserData.HeaderIndicator, bitUserDataHeader) | flag(m.ReplyPath, bitReplyPath)
	b, err := appendTPAddress([]byte{first}, m.OriginatingAddress)
	if err != nil {
		return nil, fmt.Errorf("sms: SMS-DELIVER TP-OA: %w", err)
	}
	b = append(b, m.ProtocolIdentifier, byte(m.DataCodingScheme))
	if b, err = appendTimestamp(b, m.ServiceCentreTimeStamp); err != nil {
		return nil, fmt.Errorf("sms: SMS-DELIVER TP-SCTS: %w", err)
	}
	return appendUserData(b, "SMS-DELIVER", m.DataCodingScheme, m.UserData, maxOctets)
}

func parseDeliver(b []byte) (Deliver, error) {
	d := decoder{b: b}
	first := d.octet()
	m := Deliver{
		MoreMessagesToSend:     first&bitMoreMessagesToSend == 0,
		LoopPrevention:         first&bitLoopPrevention != 0,
		StatusReportIndication: first&bitStatusReport != 0,
		ReplyPath:              first&bitReplyPath != 0,
	}
	m.OriginatingAddress = d.tpAddress()
	m.ProtocolIdentifier = d.octet()
	m.DataCodingScheme = DataCodingScheme(d.octet())
	m.ServiceCentreTimeStamp = d.timestamp()
	m.UserData = d.userData(m.DataCodingScheme, first&bitUserDataHeader != 0, maxOctets)
	if err := d.end(); err != nil {
		return Deliver{}, fmt.Errorf("sms: SMS-DELIVER: %w", err)
	}
	return m, nil
}

// SubmitReport is SMS-SUBMIT-REPORT, the SC's answer to an SMS-SUBMIT
// (TS 23.040 9.2.2.2a). It rides in RP-ACK, or in RP-ERROR with a
// FailureCause.
type SubmitReport struct {
	// FailureCause is TP-FCS. It is present only in the form for
	// RP-ERROR; zero means absent, since 0x00 to 0x7f are reserved
	// (9.2.3.22).
	FailureCause           FailureCause
	ServiceCentreTimeStamp time.Time
	OptionalParameters
}

func (SubmitReport) mti() tpMTI { return mtiSubmit }

// Marshal writes the layout of TS 23.040 9.2.2.2a: the form for
// RP-ERROR when FailureCause is set, the form for RP-ACK otherwise.
func (r SubmitReport) Marshal() ([]byte, error) {
	b := []byte{byte(mtiSubmit) | flag(r.UserData.HeaderIndicator, bitUserDataHeader)}
	if r.FailureCause != 0 {
		b = append(b, byte(r.FailureCause))
	}
	b = append(b, byte(r.ParameterIndicator))
	b, err := appendTimestamp(b, r.ServiceCentreTimeStamp)
	if err != nil {
		return nil, fmt.Errorf("sms: SMS-SUBMIT-REPORT TP-SCTS: %w", err)
	}
	return r.OptionalParameters.append(b, "SMS-SUBMIT-REPORT")
}

func parseSubmitReport(b []byte, forRPError bool) (SubmitReport, error) {
	d := decoder{b: b}
	first := d.octet()
	if tpMTI(first&0b11) != mtiSubmit {
		d.fail(fmt.Errorf("TP-MTI %02b", first&0b11))
	}
	var r SubmitReport
	if forRPError {
		r.FailureCause = FailureCause(d.octet())
	}
	pi := d.parameterIndicator()
	r.ServiceCentreTimeStamp = d.timestamp()
	r.OptionalParameters = d.optionalParameters(pi, first&bitUserDataHeader != 0)
	if err := d.end(); err != nil {
		return SubmitReport{}, fmt.Errorf("sms: SMS-SUBMIT-REPORT: %w", err)
	}
	return r, nil
}

// DeliverReport is SMS-DELIVER-REPORT, the MS's answer to an
// SMS-DELIVER or SMS-STATUS-REPORT (TS 23.040 9.2.2.1a). It rides in
// RP-ACK, or in RP-ERROR with a FailureCause.
type DeliverReport struct {
	// FailureCause is TP-FCS, present only in the form for RP-ERROR;
	// zero means absent, as in SubmitReport.
	FailureCause FailureCause
	OptionalParameters
}

func (DeliverReport) mti() tpMTI { return mtiDeliver }

// Marshal writes the layout of TS 23.040 9.2.2.1a: the form for
// RP-ERROR when FailureCause is set, the form for RP-ACK otherwise.
func (r DeliverReport) Marshal() ([]byte, error) {
	b := []byte{byte(mtiDeliver) | flag(r.UserData.HeaderIndicator, bitUserDataHeader)}
	if r.FailureCause != 0 {
		b = append(b, byte(r.FailureCause))
	}
	b = append(b, byte(r.ParameterIndicator))
	return r.OptionalParameters.append(b, "SMS-DELIVER-REPORT")
}

func parseDeliverReport(b []byte, forRPError bool) (DeliverReport, error) {
	d := decoder{b: b}
	first := d.octet()
	if tpMTI(first&0b11) != mtiDeliver {
		d.fail(fmt.Errorf("TP-MTI %02b", first&0b11))
	}
	var r DeliverReport
	if forRPError {
		r.FailureCause = FailureCause(d.octet())
	}
	pi := d.parameterIndicator()
	r.OptionalParameters = d.optionalParameters(pi, first&bitUserDataHeader != 0)
	if err := d.end(); err != nil {
		return DeliverReport{}, fmt.Errorf("sms: SMS-DELIVER-REPORT: %w", err)
	}
	return r, nil
}

// StatusReport is SMS-STATUS-REPORT, which tells the sender of an
// SMS-SUBMIT what became of it (TS 23.040 9.2.2.3).
type StatusReport struct {
	MoreMessagesToSend bool // TP-MMS, inverted on the wire as in Deliver
	LoopPrevention     bool // TP-LP
	// StatusReportQualifier is TP-SRQ: false when the report answers an
	// SMS-SUBMIT, true when it answers an SMS-COMMAND (9.2.3.26).
	StatusReportQualifier bool
	// MessageReference is the TP-MR of the SMS-SUBMIT it reports on.
	MessageReference       uint8
	RecipientAddress       Address // TP-RA
	ServiceCentreTimeStamp time.Time
	DischargeTime          time.Time // TP-DT
	Status                 Status
	// OptionalParameters follow only when TP-PI is not zero: TP-PI
	// itself may be left out when nothing follows it (9.2.2.3, note 4).
	OptionalParameters
}

func (StatusReport) mti() tpMTI { return mtiStatusReport }

// Marshal writes the layout of TS 23.040 9.2.2.3. TP-PI is written only
// when it announces a field.
func (r StatusReport) Marshal() ([]byte, error) {
	first := byte(mtiStatusReport) | flag(!r.MoreMessagesToSend, bitMoreMessagesToSend) |
		flag(r.LoopPrevention, bitLoopPrevention) | flag(r.StatusReportQualifier, bitStatusReport) |
		flag(r.UserData.HeaderIndicator, bitUserDataHeader)
	b, err := appendTPAddress([]byte{first, r.MessageReference}, r.RecipientAddress)
	if err != nil {
		return nil, fmt.Errorf("sms: SMS-STATUS-REPORT TP-RA: %w", err)
	}
	if b, err = appendTimestamp(b, r.ServiceCentreTimeStamp); err != nil {
		return nil, fmt.Errorf("sms: SMS-STATUS-REPORT TP-SCTS: %w", err)
	}
	if b, err = appendTimestamp(b, r.DischargeTime); err != nil {
		return nil, fmt.Errorf("sms: SMS-STATUS-REPORT TP-DT: %w", err)
	}
	b = append(b, byte(r.Status))
	if r.ParameterIndicator == 0 {
		return b, nil
	}
	return r.OptionalParameters.append(append(b, byte(r.ParameterIndicator)), "SMS-STATUS-REPORT")
}

func parseStatusReport(b []byte) (StatusReport, error) {
	d := decoder{b: b}
	first := d.octet()
	r := StatusReport{
		MoreMessagesToSend:    first&bitMoreMessagesToSend == 0,
		LoopPrevention:        first&bitLoopPrevention != 0,
		StatusReportQualifier: first&bitStatusReport != 0,
	}
	r.MessageReference = d.octet()
	r.RecipientAddress = d.tpAddress()
	r.ServiceCentreTimeStamp = d.timestamp()
	r.DischargeTime = d.timestamp()
	r.Status = Status(d.octet())
	if d.err == nil && len(d.b) > 0 {
		pi := d.parameterIndicator()
		r.OptionalParameters = d.optionalParameters(pi, first&bitUserDataHeader != 0)
	}
	if err := d.end(); err != nil {
		return StatusReport{}, fmt.Errorf("sms: SMS-STATUS-REPORT: %w", err)
	}
	return r, nil
}

// OptionalParameters are the fields that may follow TP-PI in the
// reports (TS 23.040 9.2.3.27). ParameterIndicator says which of the
// others are on the wire.
type OptionalParameters struct {
	ParameterIndicator ParameterIndicator
	ProtocolIdentifier uint8
	// DataCodingScheme is 0, the GSM 7 bit default alphabet, when TP-PI
	// announces TP-UDL but not TP-DCS (9.2.3.27).
	DataCodingScheme DataCodingScheme
	UserData         UserData
}

// ParameterIndicator is TP-PI (TS 23.040 9.2.3.27).
type ParameterIndicator uint8

const (
	PIProtocolIdentifier ParameterIndicator = 1 << 0
	PIDataCodingScheme   ParameterIndicator = 1 << 1
	PIUserDataLength     ParameterIndicator = 1 << 2
)

// piExtension and piReserved are the other bits of the first TP-PI
// octet: the extension bit says another TP-PI octet follows.
const (
	piExtension = 1 << 7
	piReserved  = 0b0111_1000
)

// parameterIndicator reads the first octet of TP-PI and skips the
// extension octets after it.
func (d *decoder) parameterIndicator() byte {
	pi := d.octet()
	for ext := pi; ext&piExtension != 0 && d.err == nil; {
		ext = d.octet()
	}
	return pi
}

// optionalParameters reads the fields that pi announces. Reserved bits
// and extension octets announce fields of later releases: TS 23.040
// 9.2.3.27 says to ignore the bits and discard the octets after TP-UD.
func (d *decoder) optionalParameters(pi byte, headerIndicator bool) OptionalParameters {
	p := OptionalParameters{ParameterIndicator: ParameterIndicator(pi &^ (piExtension | piReserved))}
	if p.ParameterIndicator&PIProtocolIdentifier != 0 {
		p.ProtocolIdentifier = d.octet()
	}
	if p.ParameterIndicator&PIDataCodingScheme != 0 {
		p.DataCodingScheme = DataCodingScheme(d.octet())
	}
	if p.ParameterIndicator&PIUserDataLength != 0 {
		p.UserData = d.userData(p.DataCodingScheme, headerIndicator, maxReportOctets)
	}
	if pi&(piExtension|piReserved) != 0 {
		d.b = nil
	}
	return p
}

func (p OptionalParameters) append(b []byte, pdu string) ([]byte, error) {
	if p.ParameterIndicator&PIProtocolIdentifier != 0 {
		b = append(b, p.ProtocolIdentifier)
	}
	if p.ParameterIndicator&PIDataCodingScheme != 0 {
		b = append(b, byte(p.DataCodingScheme))
	}
	if p.ParameterIndicator&PIUserDataLength == 0 {
		if p.UserData.HeaderIndicator || len(p.UserData.Data) > 0 {
			return nil, fmt.Errorf("sms: %s has user data but TP-PI does not announce TP-UDL", pdu)
		}
		return b, nil
	}
	return appendUserData(b, pdu, p.DataCodingScheme, p.UserData, maxReportOctets)
}

// FailureCause is TP-FCS (TS 23.040 9.2.3.22). The constants are the
// causes the UE and the SC of this project can give.
type FailureCause uint8

const (
	FCSDataCodingSchemeNotSupported FailureCause = 0x90
	FCSTPDUNotSupported             FailureCause = 0xb0
	FCSSCSystemFailure              FailureCause = 0xc2
	FCSInvalidSMEAddress            FailureCause = 0xc3
	FCSMemoryCapacityExceeded       FailureCause = 0xd3
	FCSUnspecifiedError             FailureCause = 0xff
)

// Status is TP-ST (TS 23.040 9.2.3.15). The constants are the outcomes
// the SC of this project can report.
type Status uint8

const (
	StatusReceivedBySME     Status = 0b000_0000
	StatusNotObtainable     Status = 0b100_0011 // permanent error
	StatusNoResponseFromSME Status = 0b110_0010 // temporary error, SC has given up
)

// TP-UD of SMS-SUBMIT and SMS-DELIVER holds at most maxOctets, 140
// octets or 160 septets (the layouts of TS 23.040 9.2.2.1 and 9.2.2.2,
// with TP-UDL counted as 9.2.3.16 says). The reports give TP-UD more
// room (up to 159 octets in the layout of 9.2.2.1a), which RP-User-Data
// bounds before that: the whole TPDU is at most maxTPDU octets.
const maxReportOctets = maxTPDU

// appendUserData writes TP-UDL and TP-UD, checking that they agree and
// that TP-UD has at most max octets.
func appendUserData(b []byte, pdu string, dcs DataCodingScheme, ud UserData, max int) ([]byte, error) {
	n := octetLength(dcs, ud.Length)
	if n > max {
		return nil, fmt.Errorf("sms: %s TP-UDL %d needs %d octets of TP-UD, more than %d", pdu, ud.Length, n, max)
	}
	if len(ud.Data) != n {
		return nil, fmt.Errorf("sms: %s TP-UDL %d needs %d octets of TP-UD, have %d", pdu, ud.Length, n, len(ud.Data))
	}
	return append(append(b, ud.Length), ud.Data...), nil
}

// userData reads TP-UDL and TP-UD, of at most max octets.
func (d *decoder) userData(dcs DataCodingScheme, headerIndicator bool, max int) UserData {
	length := d.octet()
	n := octetLength(dcs, length)
	if d.err == nil && n > max {
		d.fail(fmt.Errorf("TP-UDL %d needs %d octets of TP-UD, more than %d", length, n, max))
	}
	return UserData{HeaderIndicator: headerIndicator, Length: length, Data: d.octets(n)}
}

// appendTimestamp writes t as TP-SCTS (TS 23.040 9.2.3.11): year,
// month, day, hour, minute and second as two semi-octets each, the
// first digit in bits 3..0, then the time zone in quarters of an hour.
// The sign of the time zone is bit 3 of the last octet.
func appendTimestamp(b []byte, t time.Time) ([]byte, error) {
	if t.Year() < 2000 || t.Year() > 2099 {
		return nil, fmt.Errorf("year %d does not fit in two digits", t.Year())
	}
	_, offset := t.Zone()
	if offset%(15*60) != 0 {
		return nil, fmt.Errorf("time zone offset %ds is not a whole number of quarter hours", offset)
	}
	quarters := offset / (15 * 60)
	negative := quarters < 0
	if negative {
		quarters = -quarters
	}
	if quarters > 79 {
		return nil, fmt.Errorf("time zone offset %ds is too large", offset)
	}
	for _, v := range []int{t.Year() - 2000, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second()} {
		b = append(b, swappedDigits(v))
	}
	zone := swappedDigits(quarters)
	if negative {
		zone |= 1 << 3
	}
	return append(b, zone), nil
}

func swappedDigits(v int) byte {
	return byte(v%10)<<4 | byte(v/10)
}

// timestamp reads TP-SCTS or TP-DT. The year has two digits; this
// package reads it as 2000 to 2099.
func (d *decoder) timestamp() time.Time {
	b := d.octets(7)
	if b == nil {
		return time.Time{}
	}
	var v [7]int
	for i, octet := range b {
		tens, units := octet&0x0f, octet>>4
		if i == 6 {
			tens &^= 1 << 3 // the sign of the time zone
		}
		if tens > 9 || units > 9 {
			d.fail(errors.New("time stamp digit is not 0 to 9"))
			return time.Time{}
		}
		v[i] = int(tens)*10 + int(units)
	}
	offset := v[6] * 15 * 60
	if b[6]&(1<<3) != 0 {
		offset = -offset
	}
	return time.Date(2000+v[0], time.Month(v[1]), v[2], v[3], v[4], v[5], 0, time.FixedZone("", offset))
}

func flag(set bool, bit byte) byte {
	if set {
		return bit
	}
	return 0
}
