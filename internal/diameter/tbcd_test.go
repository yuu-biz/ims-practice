package diameter

import "testing"

func TestDecodeTBCDRejectsNonDigits(t *testing.T) {
	for _, b := range [][]byte{{0x2a}, {0xf1, 0x21}, {}} {
		if _, err := DecodeTBCD(b); err == nil {
			t.Errorf("DecodeTBCD(% x) succeeded", b)
		}
	}
	if got, err := DecodeTBCD([]byte{0x21, 0x43}); err != nil || got != "1234" {
		t.Errorf("DecodeTBCD(21 43) = %q, %v; want 1234", got, err)
	}
}
