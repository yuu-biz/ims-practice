package evs

import (
	"bytes"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
)

// bits writes a frame's bits d(from) to d(to-1) as binary digits.
func bits(f Frame, from, to int) string {
	var s strings.Builder
	for i := from; i < to; i++ {
		s.WriteByte('0' + f.Data[i/8]>>(7-i%8)&1)
	}
	return s.String()
}

// toBytes turns binary digits into octets, padding the last one with
// zeros. It is independent of the package's bit writer.
func toBytes(digits string) []byte {
	digits = strings.ReplaceAll(digits, " ", "")
	for len(digits)%8 != 0 {
		digits += "0"
	}
	out := make([]byte, len(digits)/8)
	for i, b := range digits {
		if b == '1' {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

// TestCompactSizes derives Table A.1 from the frame lengths: an EVS
// Primary frame alone, or an EVS AMR-WB IO frame after the 3-bit CMR,
// padded to the octet (A.2.1.2.2). The table has 22 rows, one for each
// speech frame type of both modes and for the EVS Primary SID.
func TestCompactSizes(t *testing.T) {
	derived := map[int]Frame{}
	for ft := range FrameType(Primary.Modes()) {
		b, _ := Primary.FrameBits(ft)
		derived[b] = Frame{Mode: Primary, Type: ft}
	}
	b, _ := Primary.FrameBits(PrimarySID)
	derived[b] = Frame{Mode: Primary, Type: PrimarySID}
	for ft := range FrameType(AMRWBIO.Modes()) {
		b, _ := AMRWBIO.FrameBits(ft)
		derived[(3+b+7)/8*8] = Frame{Mode: AMRWBIO, Type: ft}
	}
	if len(compactSizes) != 22 || !reflect.DeepEqual(derived, compactSizes) {
		t.Errorf("Table A.1 is\n%v\nthe frame lengths give\n%v", compactSizes, derived)
	}
}

// TestCompactEveryFrameType sends every frame type that has a Compact
// size alone, without a CMR, and reads it back.
func TestCompactEveryFrameType(t *testing.T) {
	var def Format
	for size, entry := range compactSizes {
		f := frame(t, entry.Mode, entry.Type, uint64(size))
		b, err := def.Marshal(Payload{Frames: []Frame{f}})
		if err != nil {
			t.Fatalf("%s frame type %d: %v", f.Mode, f.Type, err)
		}
		if 8*len(b) != size || !def.IsCompact(b) {
			t.Errorf("%s frame type %d: %d bits, Compact %v; want %d bits, Compact", f.Mode, f.Type, 8*len(b), def.IsCompact(b), size)
		}
		if f.Mode == Primary && !bytes.Equal(b, f.Data) {
			t.Errorf("%s frame type %d: the Compact payload is not the frame itself (A.2.1.1)", f.Mode, f.Type)
		}
		p, err := def.Unmarshal(b)
		if err != nil {
			t.Fatalf("%s frame type %d: %v", f.Mode, f.Type, err)
		}
		want := Payload{Frames: []Frame{f}}
		if f.Mode == AMRWBIO {
			want.CMR = NoRequest
		}
		if !reflect.DeepEqual(p, want) {
			t.Errorf("%s frame type %d: read back %+v", f.Mode, f.Type, p)
		}
	}
}

// TestCompactAMRWBIOBitOrder checks Figure A.1: the 3-bit CMR, d(1) to
// d(K-1), then d(0), then zero padding (1 bit for 6.6, 4 bits for 8.85,
// none for the others).
func TestCompactAMRWBIOBitOrder(t *testing.T) {
	var def Format
	for _, tc := range []struct {
		ft      FrameType
		cmr     CMR
		code    string
		padding int
	}{
		{AMRWBIO6600, 0, "111", 1},
		{AMRWBIO8850, NewCMR(RequestIO, uint8(AMRWBIO6600)), "000", 4},
		{AMRWBIO12650, NewCMR(RequestIO, uint8(AMRWBIO8850)), "001", 0},
		{AMRWBIO14250, NewCMR(RequestIO, uint8(AMRWBIO12650)), "010", 0},
		{AMRWBIO15850, NewCMR(RequestIO, uint8(AMRWBIO15850)), "011", 0},
		{AMRWBIO18250, NewCMR(RequestIO, uint8(AMRWBIO18250)), "100", 0},
		{AMRWBIO19850, NewCMR(RequestIO, uint8(AMRWBIO23050)), "101", 0},
		{AMRWBIO23050, NewCMR(RequestIO, uint8(AMRWBIO23850)), "110", 0},
		{AMRWBIO23850, NoRequest, "111", 0},
	} {
		f := frame(t, AMRWBIO, tc.ft, 9)
		k, _ := AMRWBIO.FrameBits(tc.ft)
		got, err := def.Marshal(Payload{CMR: tc.cmr, Frames: []Frame{f}})
		if err != nil {
			t.Fatal(err)
		}
		want := toBytes(tc.code + bits(f, 1, k) + bits(f, 0, 1))
		if !bytes.Equal(got, want) || 8*len(got)-3-k != tc.padding {
			t.Errorf("AMR-WB IO frame type %d:\ngot  %x\nwant %x (%d padding bits)", tc.ft, got, want, tc.padding)
		}
	}
}

// TestDefaultFormatHandling checks which format A.2.3.1 picks.
func TestDefaultFormatHandling(t *testing.T) {
	p132 := frame(t, Primary, Primary13200, 1)
	io2385 := frame(t, AMRWBIO, AMRWBIO23850, 2)
	ioSID := frame(t, AMRWBIO, AMRWBIOSID, 3)
	damaged := io2385
	damaged.Quality = false
	for _, tc := range []struct {
		name    string
		p       Payload
		compact bool
		first   byte // first octet of a Header-Full payload
	}{
		{"EVS Primary without a CMR", Payload{Frames: []Frame{p132}}, true, 0},
		{"EVS Primary with a CMR", Payload{CMR: NewCMR(RequestSWB, 4), Frames: []Frame{p132}}, false, 0xb4},
		{"EVS Primary with NO_REQ (cmr=1)", Payload{CMR: NoRequest, Frames: []Frame{p132}}, false, 0xff},
		{"two EVS Primary frames", Payload{Frames: []Frame{p132, p132}}, false, 0x44},
		{"EVS AMR-WB IO without a CMR", Payload{Frames: []Frame{io2385}}, true, 0},
		{"EVS AMR-WB IO asking for 23.85", Payload{CMR: NewCMR(RequestIO, 8), Frames: []Frame{io2385}}, true, 0},
		{"EVS AMR-WB IO asking for 14.25", Payload{CMR: NewCMR(RequestIO, 3), Frames: []Frame{io2385}}, false, 0x93},
		{"EVS AMR-WB IO asking for 19.85", Payload{CMR: NewCMR(RequestIO, 6), Frames: []Frame{io2385}}, false, 0x96},
		{"EVS AMR-WB IO asking for EVS Primary", Payload{CMR: NewCMR(RequestWB, 4), Frames: []Frame{io2385}}, false, 0xa4},
		{"damaged EVS AMR-WB IO asking for EVS Primary", Payload{CMR: NewCMR(RequestWB, 4), Frames: []Frame{damaged}}, false, 0xa4},
		{"EVS AMR-WB IO SID", Payload{Frames: []Frame{ioSID}}, false, 0xff},
		{"NO_DATA only, to send a CMR", Payload{CMR: NewCMR(RequestWB, 4), Frames: []Frame{{Mode: Primary, Type: NoData, Quality: true}}}, false, 0xa4},
	} {
		var def Format
		b, err := def.Marshal(tc.p)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if def.IsCompact(b) != tc.compact {
			t.Errorf("%s: Compact %v, want %v", tc.name, def.IsCompact(b), tc.compact)
		}
		if !tc.compact && b[0] != tc.first {
			t.Errorf("%s: starts with 0x%02x, want 0x%02x", tc.name, b[0], tc.first)
		}
	}
	if _, err := (Format{}).Marshal(Payload{Frames: []Frame{damaged}}); err == nil {
		t.Error("a damaged EVS AMR-WB IO frame went in the Compact format, which has no Q bit")
	}
	if _, err := (Format{HeaderFullOnly: true}).Marshal(Payload{Frames: []Frame{damaged}}); err != nil {
		t.Errorf("hf-only=1: %v", err)
	}
}

// TestSizeCollisionAvoidance checks A.2.2.1.4.2: with hf-only=0 a
// Header-Full payload is padded with zero octets until no Compact size
// of Table A.1 is left, and with hf-only=1 it is not.
func TestSizeCollisionAvoidance(t *testing.T) {
	p72 := frame(t, Primary, Primary7200, 1)  // 18 octets
	io66 := frame(t, AMRWBIO, AMRWBIO6600, 2) // 17 octets in Header-Full
	ioSID := frame(t, AMRWBIO, AMRWBIOSID, 3) // 5 octets
	noData := Frame{Mode: Primary, Type: NoData, Quality: true}
	for _, tc := range []struct {
		name     string
		p        Payload
		hf, want int // octets before and after the padding
	}{
		{"CMR, 7.2: 160 bits is EVS Primary 8.0", Payload{CMR: NewCMR(RequestWB, 1), Frames: []Frame{p72}}, 20, 21},
		{"NO_DATA, 7.2: 160 bits", Payload{Frames: []Frame{noData, p72}}, 20, 21},
		{"CMR, 4 NO_DATA, IO 6.6: 184 bits, then 192 bits", Payload{CMR: NoRequest, Frames: []Frame{noData, noData, noData, noData, io66}}, 23, 25},
		{"7 NO_DATA: 56 bits starting with 0 would read as 2.8", Payload{Frames: []Frame{noData, noData, noData, noData, noData, noData, noData}}, 7, 8},
		{"CMR, 6 NO_DATA: 56 bits that are not the AMR-WB IO SID", Payload{CMR: NoRequest, Frames: []Frame{noData, noData, noData, noData, noData, noData}}, 7, 8},
		{"CMR, IO SID: the 56-bit special case is not padded", Payload{CMR: NoRequest, Frames: []Frame{ioSID}}, 7, 7},
		{"CMR, 13.2: 280 bits collides with nothing", Payload{CMR: NoRequest, Frames: []Frame{frame(t, Primary, Primary13200, 4)}}, 35, 35},
	} {
		def, hfOnly := Format{}, Format{HeaderFullOnly: true}
		b, err := def.Marshal(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(b) != tc.want || def.IsCompact(b) || !bytes.Equal(b[tc.hf:], make([]byte, tc.want-tc.hf)) {
			t.Errorf("%s: %d octets (%x), want %d with %d zero octets of padding", tc.name, len(b), b, tc.want, tc.want-tc.hf)
		}
		if got, err := def.Unmarshal(b); err != nil || !reflect.DeepEqual(got, tc.p) {
			t.Errorf("%s: read back %+v, %v", tc.name, got, err)
		}
		b, err = hfOnly.Marshal(tc.p)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(b) != tc.hf {
			t.Errorf("%s, hf-only=1: %d octets, want %d without padding (A.2.3.2)", tc.name, len(b), tc.hf)
		}
		if got, err := hfOnly.Unmarshal(b); err != nil || !reflect.DeepEqual(got, tc.p) {
			t.Errorf("%s, hf-only=1: read back %+v, %v", tc.name, got, err)
		}
	}
}

// TestSpecialCase56Bits checks A.2.1.3: both payloads are 56 bits, and
// the first bit tells an EVS Primary 2.8 kbit/s frame (0) from an EVS
// AMR-WB IO SID with its CMR byte (1).
func TestSpecialCase56Bits(t *testing.T) {
	var def Format
	p28 := frame(t, Primary, Primary2800, 1)
	ioSID := frame(t, AMRWBIO, AMRWBIOSID, 2)
	cmr := NewCMR(RequestIO, uint8(AMRWBIO12650))

	b28, err := def.Marshal(Payload{Frames: []Frame{p28}})
	if err != nil {
		t.Fatal(err)
	}
	bSID, err := def.Marshal(Payload{CMR: cmr, Frames: []Frame{ioSID}})
	if err != nil {
		t.Fatal(err)
	}
	want := toBytes("1 001 0010 0 0 1 1 1001" + bits(ioSID, 0, 40))
	if len(b28) != 7 || len(bSID) != 7 || !bytes.Equal(bSID, want) {
		t.Fatalf("2.8 kbit/s %x, AMR-WB IO SID %x (want %x); both should be 56 bits", b28, bSID, want)
	}
	if !def.IsCompact(b28) || def.IsCompact(bSID) {
		t.Errorf("Compact: 2.8 kbit/s %v, AMR-WB IO SID %v; want true, false", def.IsCompact(b28), def.IsCompact(bSID))
	}
	if p, err := def.Unmarshal(b28); err != nil || !reflect.DeepEqual(p.Frames, []Frame{p28}) {
		t.Errorf("2.8 kbit/s read back as %+v, %v", p, err)
	}
	if p, err := def.Unmarshal(bSID); err != nil || !reflect.DeepEqual(p, Payload{CMR: cmr, Frames: []Frame{ioSID}}) {
		t.Errorf("AMR-WB IO SID read back as %+v, %v", p, err)
	}
	// With hf-only=1 there is no Compact format: 56 bits starting with
	// 0 is a ToC byte, here the EVS Primary SID.
	pSID := frame(t, Primary, PrimarySID, 3)
	hf, err := Format{HeaderFullOnly: true}.Marshal(Payload{Frames: []Frame{pSID}})
	if err != nil || len(hf) != 7 {
		t.Fatalf("hf-only=1: %x, %v", hf, err)
	}
	if p, err := (Format{HeaderFullOnly: true}).Unmarshal(hf); err != nil || !reflect.DeepEqual(p.Frames, []Frame{pSID}) {
		t.Errorf("hf-only=1: read back %+v, %v", p, err)
	}
}

// TestRoundTrip packs random payloads in both formats and reads them
// back.
func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	var cmrs []CMR
	for b := 0x80; b <= 0xff; b++ {
		if CMR(b).Valid() {
			cmrs = append(cmrs, CMR(b))
		}
	}
	types := map[Mode][]FrameType{
		Primary: {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, PrimarySID, SpeechLost, NoData},
		AMRWBIO: {0, 1, 2, 3, 4, 5, 6, 7, 8, AMRWBIOSID, SpeechLost, NoData},
	}
	for i := range 2000 {
		var p Payload
		if r.IntN(2) == 0 {
			p.CMR = cmrs[r.IntN(len(cmrs))]
		}
		for range 1 + r.IntN(4) {
			m := Mode(r.IntN(2))
			f := frame(t, m, types[m][r.IntN(len(types[m]))], uint64(i))
			f.Quality = m == Primary || r.IntN(4) != 0
			p.Frames = append(p.Frames, f)
		}
		want := p
		if want.CMR == 0 && p.hasAMRWBIO() {
			want.CMR = NoRequest
		}
		for _, format := range []Format{{}, {HeaderFullOnly: true}} {
			b, err := format.Marshal(p)
			if err != nil {
				if !format.HeaderFullOnly && len(p.Frames) == 1 && !p.Frames[0].Quality {
					continue // damaged AMR-WB IO frame refused by the Compact format
				}
				t.Fatalf("%+v: %v", p, err)
			}
			got, err := format.Unmarshal(b)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("hf-only=%v: %x read back as\n%+v, %v\nwant\n%+v", format.HeaderFullOnly, b, got, err, want)
			}
		}
	}
}

func TestUnmarshalMalformed(t *testing.T) {
	for _, format := range []Format{{}, {HeaderFullOnly: true}} {
		for name, b := range map[string][]byte{
			"empty":                 nil,
			"ToC cut short":         {0b0_1_0_0_0100},
			"frame cut short":       {0x04, 1, 2, 3},
			"frame type for future": {0x0d},
		} {
			if p, err := format.Unmarshal(b); err == nil {
				t.Errorf("hf-only=%v, %s: accepted as %+v", format.HeaderFullOnly, name, p)
			}
		}
	}
}

// FuzzUnmarshal checks that no input makes Unmarshal panic, and that
// whatever it accepts packs again.
func FuzzUnmarshal(f *testing.F) {
	f.Add([]byte{0x04}, false)
	f.Add(make([]byte, 33), false)
	f.Add([]byte{0x92, 0x39, 1, 2, 3, 4, 5}, false)
	f.Add([]byte{0xff, 0x44, 0x0f}, true)
	f.Fuzz(func(t *testing.T, b []byte, hfOnly bool) {
		format := Format{HeaderFullOnly: hfOnly}
		p, err := format.Unmarshal(b)
		if err != nil {
			return
		}
		for i := range p.Frames {
			// An EVS Primary 2.8 kbit/s frame from the wire may break
			// the rule of A.2.1.3, which Marshal enforces.
			if p.Frames[i].Mode == Primary && p.Frames[i].Type == Primary2800 {
				return
			}
		}
		if _, err := (Format{HeaderFullOnly: true}).Marshal(p); err != nil {
			t.Errorf("%x read as %+v, which does not pack: %v", b, p, err)
		}
	})
}
