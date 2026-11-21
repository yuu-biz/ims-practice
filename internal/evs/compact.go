package evs

import "fmt"

// compactSizes is Table A.1: the payload sizes in bits that the Compact
// format reserves, and the frame each one carries. An EVS AMR-WB IO
// size counts the 3-bit CMR and the padding as well (A.2.1.2.2).
var compactSizes = map[int]Frame{
	48:   {Mode: Primary, Type: PrimarySID},
	56:   {Mode: Primary, Type: Primary2800}, // shared with a Header-Full AMR-WB IO SID (A.2.1.3)
	136:  {Mode: AMRWBIO, Type: AMRWBIO6600},
	144:  {Mode: Primary, Type: Primary7200},
	160:  {Mode: Primary, Type: Primary8000},
	184:  {Mode: AMRWBIO, Type: AMRWBIO8850},
	192:  {Mode: Primary, Type: Primary9600},
	256:  {Mode: AMRWBIO, Type: AMRWBIO12650},
	264:  {Mode: Primary, Type: Primary13200},
	288:  {Mode: AMRWBIO, Type: AMRWBIO14250},
	320:  {Mode: AMRWBIO, Type: AMRWBIO15850},
	328:  {Mode: Primary, Type: Primary16400},
	368:  {Mode: AMRWBIO, Type: AMRWBIO18250},
	400:  {Mode: AMRWBIO, Type: AMRWBIO19850},
	464:  {Mode: AMRWBIO, Type: AMRWBIO23050},
	480:  {Mode: AMRWBIO, Type: AMRWBIO23850},
	488:  {Mode: Primary, Type: Primary24400},
	640:  {Mode: Primary, Type: Primary32000},
	960:  {Mode: Primary, Type: Primary48000},
	1280: {Mode: Primary, Type: Primary64000},
	1920: {Mode: Primary, Type: Primary96000},
	2560: {Mode: Primary, Type: Primary128000},
}

// specialCaseBits is the size of A.2.1.3: a Compact EVS Primary 2.8
// kbit/s frame, or a Header-Full EVS AMR-WB IO SID with its CMR byte.
// The first bit tells them apart: d(0) of the 2.8 kbit/s frame is
// always 0, the H bit of the CMR byte is 1.
const specialCaseBits = 56

// marshalCompact writes the one frame of a Compact payload. An EVS
// Primary frame goes as it is (A.2.1.1). An EVS AMR-WB IO frame goes
// after the 3-bit CMR, from d(1) on, with d(0) moved to the end, and is
// padded to the octet (A.2.1.2.2, Figure A.1). d(1) then starts at bit
// 3 of an octet, as it does in the AMR-WB bandwidth-efficient payload
// of RFC 4867 4.3 (after a 4-bit CMR, a 6-bit ToC entry and d(0)), so a
// media gateway that converts between the two shifts whole octets.
func marshalCompact(f Frame, bits int, cmrCode uint) []byte {
	var w bitWriter
	if f.Mode == AMRWBIO {
		w.writeUint(cmrCode, 3)
		w.writeBits(f.Data, 1, bits)
		w.writeBits(f.Data, 0, 1)
	} else {
		w.writeBits(f.Data, 0, bits)
	}
	w.align()
	return w.bytes
}

// unmarshalCompact reads a Compact payload whose size Table A.1 lists.
// The payload has no Q bit, so the frame is taken as undamaged
// (A.2.1.2.2).
func unmarshalCompact(b []byte) (Payload, error) {
	f, ok := compactSizes[8*len(b)]
	if !ok {
		return Payload{}, fmt.Errorf("evs: %d bits is not a Compact payload size (TS 26.445 Table A.1)", 8*len(b))
	}
	f.Quality = true
	bits, _ := f.Mode.FrameBits(f.Type)
	f.Data = make([]byte, (bits+7)/8)
	r := bitReader{bytes: b}
	var p Payload
	if f.Mode == AMRWBIO {
		p.CMR = cmrFromCompact(r.readUint(3))
		r.readBitsInto(f.Data, 1, bits)
		r.readBitsInto(f.Data, 0, 1)
	} else {
		r.readBitsInto(f.Data, 0, bits)
	}
	p.Frames = []Frame{f}
	return p, nil
}
