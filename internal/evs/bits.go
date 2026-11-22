package evs

// bitWriter packs fields from the most significant bit of each octet
// on (TS 26.445 A.0). Package amr has one of the same shape for RFC
// 4867; each codec keeps its own so that it reads apart from the other,
// and the two catch a short payload differently: amr at every read,
// evs by asking left() first.
type bitWriter struct {
	bytes []byte
	n     int // bits written
}

func (w *bitWriter) writeUint(v uint, bits int) {
	for i := bits - 1; i >= 0; i-- {
		w.writeBit(byte(v>>i) & 1)
	}
}

// writeBits writes bits from..to-1 of data.
func (w *bitWriter) writeBits(data []byte, from, to int) {
	for i := from; i < to; i++ {
		w.writeBit(bitAt(data, i))
	}
}

func (w *bitWriter) writeBit(b byte) {
	if w.n%8 == 0 {
		w.bytes = append(w.bytes, 0)
	}
	w.bytes[len(w.bytes)-1] |= b << (7 - w.n%8)
	w.n++
}

// align pads with zero bits up to the next octet.
func (w *bitWriter) align() { w.n = 8 * len(w.bytes) }

func bitAt(data []byte, i int) byte { return data[i/8] >> (7 - i%8) & 1 }

type bitReader struct {
	bytes []byte
	n     int // bits read
}

func (r *bitReader) left() int { return 8*len(r.bytes) - r.n }

func (r *bitReader) readBit() byte {
	b := bitAt(r.bytes, r.n)
	r.n++
	return b
}

func (r *bitReader) readUint(bits int) uint {
	var v uint
	for range bits {
		v = v<<1 | uint(r.readBit())
	}
	return v
}

// readBitsInto sets bits from..to-1 of out. The caller checks left()
// first.
func (r *bitReader) readBitsInto(out []byte, from, to int) {
	for i := from; i < to; i++ {
		out[i/8] |= r.readBit() << (7 - i%8)
	}
}

func (r *bitReader) align() { r.n = (r.n + 7) / 8 * 8 }
