package sms

import (
	"errors"
	"fmt"
	"strings"
)

// Address is a number in an RP or TP address field. Both layers code
// the type of address and the digits in the same way; they differ only
// in what the length octet counts (see appendRPAddress and
// appendTPAddress).
type Address struct {
	TypeOfNumber  TypeOfNumber
	NumberingPlan NumberingPlanIdentification
	// Digits holds the number without a leading '+', for example
	// "12025550101". Besides 0-9 it may hold '*', '#', 'a', 'b' and 'c'.
	Digits string
}

// TypeOfNumber is bits 6..4 of the type of address (TS 23.040 9.1.2.5).
type TypeOfNumber uint8

const (
	TONUnknown         TypeOfNumber = 0b000
	TONInternational   TypeOfNumber = 0b001
	TONNational        TypeOfNumber = 0b010
	TONNetworkSpecific TypeOfNumber = 0b011
	TONSubscriber      TypeOfNumber = 0b100
	TONAlphanumeric    TypeOfNumber = 0b101
	TONAbbreviated     TypeOfNumber = 0b110
)

// NumberingPlanIdentification is bits 3..0 of the type of address
// (TS 23.040 9.1.2.5).
type NumberingPlanIdentification uint8

const (
	NPIUnknown NumberingPlanIdentification = 0b0000
	// NPIISDNTelephone is E.164. TS 23.040 9.1.2.5 requires it for the
	// address of an SC or an MS.
	NPIISDNTelephone NumberingPlanIdentification = 0b0001
	NPINational      NumberingPlanIdentification = 0b1000
	NPIPrivate       NumberingPlanIdentification = 0b1001
)

// InternationalNumber is the address of an E.164 number such as
// "+12025550101".
func InternationalNumber(e164 string) Address {
	return Address{
		TypeOfNumber:  TONInternational,
		NumberingPlan: NPIISDNTelephone,
		Digits:        strings.TrimPrefix(e164, "+"),
	}
}

// String writes an international number with a leading '+'.
func (a Address) String() string {
	if a.TypeOfNumber == TONInternational {
		return "+" + a.Digits
	}
	return a.Digits
}

// typeOfAddress is the octet before the digits. Bit 7 is always 1: in
// TP it is fixed, and in RP it is the extension bit of the TS 24.008
// Called Party BCD Number, which says no octet 3a follows.
func (a Address) typeOfAddress() byte {
	return 0x80 | byte(a.TypeOfNumber&0b111)<<4 | byte(a.NumberingPlan&0b1111)
}

func addressOf(typeOfAddress byte, digits string) Address {
	return Address{
		TypeOfNumber:  TypeOfNumber(typeOfAddress >> 4 & 0b111),
		NumberingPlan: NumberingPlanIdentification(typeOfAddress & 0b1111),
		Digits:        digits,
	}
}

// semiOctetDigits gives each semi-octet value its digit (TS 23.040
// 9.1.2.3, after the "called BCD number" of TS 24.008). 1111 is the
// filler and has no digit.
const semiOctetDigits = "0123456789*#abc"

const filler = 0b1111

// errAlphanumeric is returned for an alphanumeric TP address.
//
// SIMPLIFIED: TS 23.040 9.1.2.5. An address with type of number 101
// holds GSM 7 bit characters, such as a sender name, instead of digits.
// The Service Centre uses E.164 numbers only, so such an address is
// refused.
var errAlphanumeric = errors.New("alphanumeric address is not supported")

// encodeSemiOctets packs digits two to an octet, the first digit in bits
// 3..0 (TS 23.040 9.1.2.3). An odd count leaves 1111 in bits 7..4 of the
// last octet.
func encodeSemiOctets(digits string) ([]byte, error) {
	b := make([]byte, (len(digits)+1)/2)
	for i := range b {
		b[i] = filler << 4
	}
	for i := 0; i < len(digits); i++ {
		v := strings.IndexByte(semiOctetDigits, digits[i])
		if v < 0 {
			return nil, fmt.Errorf("%q is not a digit of an address", digits[i])
		}
		if i%2 == 0 {
			b[i/2] = b[i/2]&0xf0 | byte(v)
		} else {
			b[i/2] = b[i/2]&0x0f | byte(v)<<4
		}
	}
	return b, nil
}

// decodeSemiOctets reads up to n digits. A filler is skipped wherever it
// is: TS 23.040 9.1.2.3 says to carry on with the next semi-octet.
func decodeSemiOctets(b []byte, n int) string {
	var digits strings.Builder
	for _, octet := range b {
		for _, v := range []byte{octet & 0x0f, octet >> 4} {
			if v != filler && digits.Len() < n {
				digits.WriteByte(semiOctetDigits[v])
			}
		}
	}
	return digits.String()
}

// maxAddressDigits is how many digits fit in an address: TS 23.040
// 9.1.2.5 allows 12 octets for the whole TP field, which leaves 10 for
// the digits, and TS 24.011 8.2.5.1 allows the RP field 11 octets of
// contents, which also leaves 10.
const maxAddressDigits = 20

// appendTPAddress writes a TP address field (TS 23.040 9.1.2.5). Its
// Address-Length counts the digits (useful semi-octets), not octets.
func appendTPAddress(b []byte, a Address) ([]byte, error) {
	if a.TypeOfNumber == TONAlphanumeric {
		return nil, errAlphanumeric
	}
	if len(a.Digits) > maxAddressDigits {
		return nil, fmt.Errorf("address of %d digits, at most %d fit", len(a.Digits), maxAddressDigits)
	}
	value, err := encodeSemiOctets(a.Digits)
	if err != nil {
		return nil, err
	}
	b = append(b, byte(len(a.Digits)), a.typeOfAddress())
	return append(b, value...), nil
}

// tpAddress reads a TP address field (TS 23.040 9.1.2.5).
func (d *decoder) tpAddress() Address {
	digits := int(d.octet())
	if d.err == nil && digits > maxAddressDigits {
		d.fail(fmt.Errorf("address of %d digits, at most %d fit", digits, maxAddressDigits))
	}
	typeOfAddress := d.octet()
	value := d.octets((digits + 1) / 2)
	a := addressOf(typeOfAddress, decodeSemiOctets(value, digits))
	if a.TypeOfNumber == TONAlphanumeric {
		d.fail(errAlphanumeric)
	}
	return a
}

// appendRPAddress writes the contents of an RP address element with its
// length (TS 24.011 8.2.5.1, 8.2.5.2). Unlike TP, the length counts
// octets: the type of address and the digit octets.
func appendRPAddress(b []byte, a Address) ([]byte, error) {
	if a.Digits == "" || len(a.Digits) > maxAddressDigits {
		return nil, fmt.Errorf("RP address of %d digits, want 1 to %d", len(a.Digits), maxAddressDigits)
	}
	value, err := encodeSemiOctets(a.Digits)
	if err != nil {
		return nil, err
	}
	b = append(b, byte(1+len(value)), a.typeOfAddress())
	return append(b, value...), nil
}

// rpAddress reads an RP address element. A length of 0 is the empty
// address that one of the two addresses of RP-DATA has; any other is
// at most 11 octets (TS 24.011 8.2.5.1, 8.2.5.2).
func (d *decoder) rpAddress() Address {
	n := int(d.octet())
	if d.err == nil && n > 1+maxAddressDigits/2 {
		d.fail(fmt.Errorf("RP address of %d octets, at most %d", n, 1+maxAddressDigits/2))
	}
	contents := d.octets(n)
	if len(contents) == 0 {
		return Address{}
	}
	return addressOf(contents[0], decodeSemiOctets(contents[1:], 2*len(contents)))
}
