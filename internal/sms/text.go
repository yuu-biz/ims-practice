// Package sms reads and writes the short message carried in the body of
// a SIP MESSAGE with Content-Type application/vnd.3gpp.sms (TS 24.341).
//
// The body has two layers, one inside the other:
//
//   - the relay layer (RP, TS 24.011 V20.0.0 clause 7.3 and 8.2), in rp.go;
//   - the transfer layer (TP, TS 23.040 V20.0.0 clause 9.2), in tp.go,
//     whose PDU is the RP-User-Data of the RP message.
//
// The text of the message is coded as TS 23.038 V20.0.0 describes, in
// text.go.
package sms

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// DataCodingScheme is TP-DCS (TS 23.040 9.2.3.10), coded as TS 23.038
// clause 4 describes.
type DataCodingScheme uint8

const (
	// DCSGSM7 is the GSM 7 bit default alphabet with no message class
	// (TS 23.038 clause 4, the NOTE of group 00xx).
	DCSGSM7 DataCodingScheme = 0x00
	// DCSUCS2 is UCS2 with no message class (group 00xx, bits 3..2 = 10).
	DCSUCS2 DataCodingScheme = 0x08
)

// Alphabet is the character set that a TP-DCS selects.
type Alphabet uint8

const (
	AlphabetGSM7 Alphabet = iota
	Alphabet8Bit
	AlphabetUCS2
)

// Alphabet reads the character set out of the coding group in bits
// 7..4 (TS 23.038 clause 4). Reserved codings mean the GSM 7 bit
// default alphabet, as the clause says a receiver shall assume.
func (d DataCodingScheme) Alphabet() Alphabet {
	switch group := d >> 4; {
	case group <= 0b0111:
		// Groups 00xx and 01xx share bits 5..0; bits 3..2 are the set.
		switch (d >> 2) & 0b11 {
		case 0b01:
			return Alphabet8Bit
		case 0b10:
			return AlphabetUCS2
		}
	case group == 0b1110:
		return AlphabetUCS2
	case group == 0b1111:
		if d&0b0100 != 0 {
			return Alphabet8Bit
		}
	}
	return AlphabetGSM7
}

// Compressed reports bit 5 of groups 00xx and 01xx, which says the text
// is compressed with the algorithm of TS 23.042.
func (d DataCodingScheme) Compressed() bool {
	return d>>6 <= 0b01 && d&0x20 != 0
}

// UserData is TP-User-Data together with the two fields that describe
// it: TP-User-Data-Header-Indicator (TS 23.040 9.2.3.23) and
// TP-User-Data-Length (9.2.3.16). Data is the field exactly as it is on
// the wire: packed septets for the 7 bit alphabet, and the header, if
// any, still in front of the text.
type UserData struct {
	HeaderIndicator bool
	// Length counts septets when the alphabet is the uncompressed GSM 7
	// bit default alphabet, and octets otherwise (9.2.3.16).
	Length uint8
	Data   []byte
}

// octetLength is how many octets of TP-UD a TP-UDL of length needs.
func octetLength(dcs DataCodingScheme, length uint8) int {
	if dcs.Alphabet() == AlphabetGSM7 && !dcs.Compressed() {
		return (int(length)*7 + 7) / 8
	}
	return int(length)
}

// One short message holds 140 octets of TP-UD, which is 160 septets
// (TS 23.038 clause 4 and 6.1.2.1.1).
const (
	maxOctets  = 140
	maxSeptets = 160
)

// ErrTextTooLong means the text needs more than one short message.
var ErrTextTooLong = errors.New("sms: the text does not fit in one short message")

// ErrUserDataHeader means TP-UDHI is set. The header is kept in
// UserData.Data but is not decoded.
var ErrUserDataHeader = errors.New("sms: the user data starts with a header, which this package does not decode")

// EncodeText codes text in the GSM 7 bit default alphabet when every
// character is in it or in its extension table, and in UCS2 otherwise.
//
// SIMPLIFIED: TS 23.040 9.2.3.24.1 (concatenated short messages). A
// text longer than one short message would need a User-Data-Header in
// each part; this project sends one short message only, so such a text
// is refused with ErrTextTooLong.
func EncodeText(text string) (DataCodingScheme, UserData, error) {
	if septets, ok := gsm7Septets(text); ok {
		if len(septets) > maxSeptets {
			return 0, UserData{}, ErrTextTooLong
		}
		return DCSGSM7, UserData{Length: uint8(len(septets)), Data: packSeptets(septets)}, nil
	}
	// Every character takes at least as many octets in UCS2 as septets in
	// the 7 bit alphabet, so a text that is too long for one stays too
	// long for the other.
	data := ucs2Encode(text)
	if len(data) > maxOctets {
		return 0, UserData{}, ErrTextTooLong
	}
	return DCSUCS2, UserData{Length: uint8(len(data)), Data: data}, nil
}

// DecodeText returns the text of ud, coded as dcs says.
//
// SIMPLIFIED: TS 23.040 9.2.3.24 (User-Data-Header) and TS 23.042
// (compression). A header would carry concatenation or a national
// language table (TS 23.038 6.2.1.2), and this project sends neither,
// so DecodeText returns ErrUserDataHeader rather than guess at the text
// behind a header. Compressed text is refused for the same reason.
func DecodeText(dcs DataCodingScheme, ud UserData) (string, error) {
	if ud.HeaderIndicator {
		return "", ErrUserDataHeader
	}
	if dcs.Compressed() {
		return "", errors.New("sms: compressed user data (TS 23.042) is not supported")
	}
	if len(ud.Data) != octetLength(dcs, ud.Length) {
		return "", fmt.Errorf("sms: TP-UDL %d needs %d octets of user data, have %d",
			ud.Length, octetLength(dcs, ud.Length), len(ud.Data))
	}
	switch dcs.Alphabet() {
	case AlphabetGSM7:
		return gsm7Decode(unpackSeptets(ud.Data, int(ud.Length))), nil
	case AlphabetUCS2:
		return ucs2Decode(ud.Data)
	}
	return "", errors.New("sms: 8 bit data is not text")
}

// escape is the code that switches to the extension table for the next
// septet (TS 23.038 6.2.1, NOTE 1).
const escape = 0x1b

// gsm7Default is the GSM 7 bit default alphabet (TS 23.038 6.2.1). Each
// line is one column of the table in the clause, 16 codes. The escape
// code 0x1b stands as a space, which is how a receiver that does not
// understand it shows it (NOTE 1).
var gsm7Default = [128]rune{
	'@', '£', '$', '¥', 'è', 'é', 'ù', 'ì', 'ò', 'Ç', '\n', 'Ø', 'ø', '\r', 'Å', 'å',
	'Δ', '_', 'Φ', 'Γ', 'Λ', 'Ω', 'Π', 'Ψ', 'Σ', 'Θ', 'Ξ', ' ', 'Æ', 'æ', 'ß', 'É',
	' ', '!', '"', '#', '¤', '%', '&', '\'', '(', ')', '*', '+', ',', '-', '.', '/',
	'0', '1', '2', '3', '4', '5', '6', '7', '8', '9', ':', ';', '<', '=', '>', '?',
	'¡', 'A', 'B', 'C', 'D', 'E', 'F', 'G', 'H', 'I', 'J', 'K', 'L', 'M', 'N', 'O',
	'P', 'Q', 'R', 'S', 'T', 'U', 'V', 'W', 'X', 'Y', 'Z', 'Ä', 'Ö', 'Ñ', 'Ü', '§',
	'¿', 'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j', 'k', 'l', 'm', 'n', 'o',
	'p', 'q', 'r', 's', 't', 'u', 'v', 'w', 'x', 'y', 'z', 'ä', 'ö', 'ñ', 'ü', 'à',
}

// gsm7Extension is the extension table (TS 23.038 6.2.1.1): the codes
// that follow an escape. 0x0a is the page break (NOTE 3), written here
// as a form feed.
var gsm7Extension = map[byte]rune{
	0x0a: '\f',
	0x14: '^',
	0x28: '{',
	0x29: '}',
	0x2f: '\\',
	0x3c: '[',
	0x3d: '~',
	0x3e: ']',
	0x40: '|',
	0x65: '€',
}

// gsm7DefaultCode and gsm7ExtensionCode turn a character back into its
// code. The escape code is left out, since it is not a character.
var gsm7DefaultCode, gsm7ExtensionCode = func() (map[rune]byte, map[rune]byte) {
	def := make(map[rune]byte, len(gsm7Default))
	for code, r := range gsm7Default {
		if code != escape {
			def[r] = byte(code)
		}
	}
	ext := make(map[rune]byte, len(gsm7Extension))
	for code, r := range gsm7Extension {
		ext[r] = code
	}
	return def, ext
}()

// gsm7Septets codes text as septets, one per character of the default
// alphabet and two (escape and code) per character of the extension
// table. ok is false when some character is in neither.
func gsm7Septets(text string) (septets []byte, ok bool) {
	for _, r := range text {
		if code, found := gsm7DefaultCode[r]; found {
			septets = append(septets, code)
		} else if code, found := gsm7ExtensionCode[r]; found {
			septets = append(septets, escape, code)
		} else {
			return nil, false
		}
	}
	return septets, true
}

// gsm7Decode turns septets into text. A code after an escape that the
// extension table does not define is shown as the default alphabet's
// character (TS 23.038 6.2.1.1). An escape with nothing after it, or
// followed by a second escape, is shown as a space (6.2.1 NOTE 1 and
// 6.2.1.1 NOTE 1).
func gsm7Decode(septets []byte) string {
	var b strings.Builder
	for i := 0; i < len(septets); i++ {
		if septets[i] != escape {
			b.WriteRune(gsm7Default[septets[i]])
			continue
		}
		i++
		if i == len(septets) || septets[i] == escape {
			b.WriteRune(' ')
			continue
		}
		if r, ok := gsm7Extension[septets[i]]; ok {
			b.WriteRune(r)
		} else {
			b.WriteRune(gsm7Default[septets[i]])
		}
	}
	return b.String()
}

// packSeptets packs septets into octets as TS 23.038 6.1.2.1.1 shows:
// the first septet takes bits 0..6 of the first octet, and each next
// septet starts at the next free bit. The unused bits of the last octet
// are zero.
//
// Seven septets leave seven zero bits at the end, which look like an
// extra '@' (code 0). USSD and cell broadcast fill them with CR instead
// (6.1.2.3.1), but a short message does not: TP-UDL counts the septets,
// so the receiver knows where the text ends.
func packSeptets(septets []byte) []byte {
	out := make([]byte, (len(septets)*7+7)/8)
	for i, s := range septets {
		bit := i * 7
		out[bit/8] |= s << (bit % 8)
		if bit%8 > 1 {
			out[bit/8+1] |= s >> (8 - bit%8)
		}
	}
	return out
}

// unpackSeptets takes n septets out of packed, which holds exactly
// (n*7+7)/8 octets.
func unpackSeptets(packed []byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		bit := i * 7
		v := packed[bit/8] >> (bit % 8)
		if bit%8 > 1 {
			v |= packed[bit/8+1] << (8 - bit%8)
		}
		out[i] = v & 0x7f
	}
	return out
}

// ucs2Encode writes text as 16 bit units, most significant octet first.
//
// TS 23.038 6.2.3 names only ISO/IEC 10646, whose UCS2 form has no
// characters beyond U+FFFF. Neither TS 23.038 nor TS 23.040 states the
// octet order of a UCS2 character; this package writes it as TS 23.040
// 9.1.2.1 writes an integer, big-endian. Handsets send characters
// beyond U+FFFF, such as emoji, as UTF-16 surrogate pairs, so this
// package does the same: such a character takes two units, four octets.
func ucs2Encode(text string) []byte {
	units := utf16.Encode([]rune(text))
	b := make([]byte, 0, 2*len(units))
	for _, u := range units {
		b = binary.BigEndian.AppendUint16(b, u)
	}
	return b
}

// ucs2Decode reads 16 bit units, joining surrogate pairs as ucs2Encode
// writes them. A lone surrogate becomes U+FFFD.
func ucs2Decode(b []byte) (string, error) {
	if len(b)%2 != 0 {
		return "", fmt.Errorf("sms: UCS2 user data of %d octets, not a whole number of characters", len(b))
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(b[2*i:])
	}
	return string(utf16.Decode(units)), nil
}
