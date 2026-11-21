package g711

import "testing"

func TestKnownValues(t *testing.T) {
	// Silence encodes to 0xFF; the largest magnitudes to 0x80 and 0x00.
	tests := []struct {
		sample int16
		want   byte
	}{
		{0, 0xFF},
		{32767, 0x80},
		{-32768, 0x00},
		{-1, 0x7F},
	}
	for _, tt := range tests {
		if got := EncodeMuLaw(tt.sample); got != tt.want {
			t.Errorf("EncodeMuLaw(%d) = %#02x, want %#02x", tt.sample, got, tt.want)
		}
	}
	if got := DecodeMuLaw(0xFF); got != 0 {
		t.Errorf("DecodeMuLaw(0xFF) = %d, want 0", got)
	}
	if got := DecodeMuLaw(0x80); got != 32124 {
		t.Errorf("DecodeMuLaw(0x80) = %d, want 32124", got)
	}
	if got := DecodeMuLaw(0x00); got != -32124 {
		t.Errorf("DecodeMuLaw(0x00) = %d, want -32124", got)
	}
}

// TestEveryCodeRoundTrips checks that decoding and encoding again gives
// the same byte for all 256 codes, except 0x7F, the "negative zero",
// which decodes to 0 and so encodes back to 0xFF.
func TestEveryCodeRoundTrips(t *testing.T) {
	for c := 0; c < 256; c++ {
		got := EncodeMuLaw(DecodeMuLaw(byte(c)))
		want := byte(c)
		if c == 0x7F {
			want = 0xFF
		}
		if got != want {
			t.Errorf("code %#02x -> %d -> %#02x", c, DecodeMuLaw(byte(c)), got)
		}
	}
}

func TestErrorIsBoundedBySegment(t *testing.T) {
	// mu-law keeps a relative error of a few percent; check a loose bound.
	for s := -32000; s <= 32000; s += 97 {
		d := int(DecodeMuLaw(EncodeMuLaw(int16(s))))
		diff := d - s
		if diff < 0 {
			diff = -diff
		}
		// The smallest step is 8, so the error near zero can reach 4.
		limit := 4 + abs(s)/16
		if diff > limit {
			t.Fatalf("%d decodes to %d, error %d > %d", s, d, diff, limit)
		}
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
