package diameter

import "fmt"

// EncodeTBCD packs decimal digits two to an octet as TS 29.329 6.3.2
// says for the MSISDN AVP: the first digit in bits 4 to 1, the second
// in bits 8 to 5, and 1111 filling the last high nibble when the count
// is odd. The digits must be decimal, as sh.ValidMSISDN checks.
func EncodeTBCD(digits string) []byte {
	out := make([]byte, (len(digits)+1)/2)
	for i := range out {
		low := digits[2*i] - '0'
		high := byte(0x0f)
		if 2*i+1 < len(digits) {
			high = digits[2*i+1] - '0'
		}
		out[i] = high<<4 | low
	}
	return out
}

// DecodeTBCD is the reverse of EncodeTBCD. A filler is allowed only as
// the last high nibble.
func DecodeTBCD(b []byte) (string, error) {
	digits := make([]byte, 0, 2*len(b))
	for i, octet := range b {
		for n, nibble := range []byte{octet & 0x0f, octet >> 4} {
			switch {
			case nibble <= 9:
				digits = append(digits, '0'+nibble)
			case nibble == 0x0f && n == 1 && i == len(b)-1:
			default:
				return "", fmt.Errorf("octet %d holds %#02x, which is not a TBCD digit", i, octet)
			}
		}
	}
	if len(digits) == 0 {
		return "", fmt.Errorf("no digits")
	}
	return string(digits), nil
}
