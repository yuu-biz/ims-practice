// Package g711 encodes and decodes G.711 mu-law (ITU-T G.711), the
// PCMU payload of RTP (RFC 3551 4.5.14). Each 16-bit linear sample
// becomes one byte: a sign bit, a 3-bit segment and a 4-bit step,
// stored inverted.
package g711

const (
	bias = 0x84 // 132: shifts the input so that every segment starts at a power of two
	clip = 32635
)

// EncodeMuLaw encodes one 16-bit linear PCM sample.
func EncodeMuLaw(sample int16) byte {
	s := int(sample)
	sign := 0
	if s < 0 {
		s = -s
		sign = 0x80
	}
	if s > clip {
		s = clip
	}
	s += bias
	// The segment is the position of the highest set bit above bit 7.
	segment := 7
	for mask := 0x4000; s&mask == 0 && segment > 0; mask >>= 1 {
		segment--
	}
	step := (s >> (segment + 3)) & 0x0f
	return ^byte(sign | segment<<4 | step)
}

// DecodeMuLaw decodes one PCMU byte to a 16-bit linear sample.
func DecodeMuLaw(b byte) int16 {
	b = ^b
	segment := int(b>>4) & 0x07
	step := int(b) & 0x0f
	s := ((step << 3) + bias) << segment
	s -= bias
	if b&0x80 != 0 {
		return int16(-s)
	}
	return int16(s)
}

// Encode encodes a block of samples.
func Encode(samples []int16) []byte {
	out := make([]byte, len(samples))
	for i, s := range samples {
		out[i] = EncodeMuLaw(s)
	}
	return out
}

// Decode decodes a block of PCMU bytes.
func Decode(payload []byte) []int16 {
	out := make([]int16, len(payload))
	for i, b := range payload {
		out[i] = DecodeMuLaw(b)
	}
	return out
}
