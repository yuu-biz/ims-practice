package evs

import "errors"

// Format is how a session packs its EVS payloads. Of the a=fmtp
// parameters (TS 26.445 A.3.1), only hf-only changes what a payload
// looks like on the wire; the others limit which frames and requests
// the caller puts in it:
//   - cmr=1 asks for a CMR in every packet: set Payload.CMR, to
//     NoRequest when there is nothing to ask.
//   - cmr=-1 in EVS Primary forbids the CMR: leave Payload.CMR zero.
//   - br, bw, mode-set and the like limit the frame types and the
//     requests; Marshal does not check them.
type Format struct {
	// HeaderFullOnly is hf-only=1: every payload is Header-Full, and
	// none is padded to avoid the Compact sizes (A.2.3.2).
	HeaderFullOnly bool
}

// Marshal packs p following the format handling of A.2.3. With
// hf-only=0, a single frame goes in the Compact format when it can: an
// EVS Primary frame without a CMR, or an EVS AMR-WB IO speech frame
// whose CMR fits the 3 bits of Table A.2. Everything else is
// Header-Full, padded with zero octets until its size is none of Table
// A.1 (A.2.2.1.4.2). TS 26.114 7.5.2.1.2.3 asks for one frame per
// packet, but a packet may carry more.
func (f Format) Marshal(p Payload) ([]byte, error) {
	bits, err := p.check()
	if err != nil {
		return nil, err
	}
	if f.HeaderFullOnly {
		return marshalHeaderFull(p, bits), nil
	}
	code, ok, err := p.compact()
	if err != nil {
		return nil, err
	}
	if ok {
		return marshalCompact(p.Frames[0], bits[0], code), nil
	}
	b := marshalHeaderFull(p, bits)
	for collides(b, p) {
		b = append(b, 0)
	}
	return b, nil
}

// compact applies the default format handling of A.2.3.1: whether p is
// one frame that goes in the Compact format, and the 3-bit CMR for an
// EVS AMR-WB IO frame. NO_DATA, SPEECH_LOST and the SID of EVS AMR-WB
// IO have no Compact size. A CMR for 14.25 or 19.85 kbit/s, or for EVS
// Primary, does not fit the 3 bits and takes the frame to Header-Full,
// which A.2.3.1 allows for these.
func (p Payload) compact() (uint, bool, error) {
	if len(p.Frames) != 1 {
		return 0, false, nil
	}
	fr := p.Frames[0]
	switch {
	case fr.Mode == Primary && (fr.Mode.IsSpeech(fr.Type) || fr.Type == PrimarySID):
		return 0, p.CMR == 0, nil
	case fr.Mode == AMRWBIO && fr.Mode.IsSpeech(fr.Type):
		code, ok := p.CMR.compactCode()
		if ok && !fr.Quality {
			return 0, false, errors.New("evs: a damaged EVS AMR-WB IO frame cannot go in the Compact format, which has no Q bit; drop it or send NO_DATA (TS 26.445 A.2.1.2.2)")
		}
		return code, ok, nil
	}
	return 0, false, nil
}

// collides tells that the Header-Full payload b would be read as
// Compact. The one exception is the EVS AMR-WB IO SID with its CMR
// byte, which keeps the 56 bits of A.2.1.3 because the receiver tells
// it by the H bit (A.2.2.1.4.2).
func collides(b []byte, p Payload) bool {
	if len(p.Frames) == 1 && p.Frames[0].Mode == AMRWBIO && p.Frames[0].Type == AMRWBIOSID {
		return false
	}
	_, ok := compactSizes[8*len(b)]
	return ok
}

// IsCompact tells whether a received payload is in the Compact format.
// With hf-only=0 the size decides (Table A.1), and for 56 bits the
// first bit (A.2.1.3). With hf-only=1 every payload is Header-Full
// whatever its size (A.2.3.2).
func (f Format) IsCompact(b []byte) bool {
	if f.HeaderFullOnly {
		return false
	}
	if _, ok := compactSizes[8*len(b)]; !ok {
		return false
	}
	return 8*len(b) != specialCaseBits || b[0]&0x80 == 0
}

// Unmarshal unpacks a payload. A Compact EVS AMR-WB IO payload returns
// its 3-bit CMR as the CMR byte that says the same (Table A.2 and Table
// A.3), and "none" as NoRequest. Padding is discarded.
func (f Format) Unmarshal(b []byte) (Payload, error) {
	if f.IsCompact(b) {
		return unmarshalCompact(b)
	}
	return unmarshalHeaderFull(b)
}
