package evs

import (
	"bytes"
	"reflect"
	"testing"
)

// cat joins header octets and frame data into the payload the figures
// of TS 26.445 A.2.2.1 draw. It is built without the package's bit
// writer: every Header-Full field is a whole octet, and the test frames
// already have zero padding bits.
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestHeaderFull(t *testing.T) {
	p132 := frame(t, Primary, Primary13200, 1)
	p244 := frame(t, Primary, Primary24400, 2)
	pSID := frame(t, Primary, PrimarySID, 3)
	io2385 := frame(t, AMRWBIO, AMRWBIO23850, 4)
	ioSID := frame(t, AMRWBIO, AMRWBIOSID, 5)
	io66 := frame(t, AMRWBIO, AMRWBIO6600, 6)
	io66.Quality = false

	for _, tc := range []struct {
		name string
		p    Payload
		want []byte
		// back is what Unmarshal returns when it differs from p.
		back *Payload
	}{
		{
			name: "Figure A.3 (a): one ToC, EVS Primary 13.2",
			p:    Payload{Frames: []Frame{p132}},
			want: cat([]byte{0b0_0_0_0_0100}, p132.Data),
		},
		{
			name: "Figure A.3 (c): CMR SWB 13.2 and EVS Primary 24.4",
			p:    Payload{CMR: NewCMR(RequestSWB, uint8(Primary13200)), Frames: []Frame{p244}},
			want: cat([]byte{0b1_011_0100, 0b0_0_0_0_0110}, p244.Data),
		},
		{
			name: "Figure A.3 (d): CMR, 13.2, SID and NO_DATA",
			p:    Payload{CMR: NewCMR(RequestWB, uint8(Primary24400)), Frames: []Frame{p132, pSID, {Mode: Primary, Type: NoData, Quality: true}}},
			want: cat([]byte{0b1_010_0110, 0b0_1_0_0_0100, 0b0_1_0_0_1100, 0b0_0_0_0_1111}, p132.Data, pSID.Data),
		},
		{
			name: "EVS AMR-WB IO 23.85 without a CMR gets NO_REQ",
			p:    Payload{Frames: []Frame{io2385}},
			want: cat([]byte{0b1_111_1111, 0b0_0_1_1_1000}, io2385.Data),
			back: &Payload{CMR: NoRequest, Frames: []Frame{io2385}},
		},
		{
			name: "Figure A.6: CMR IO 12.65 and EVS AMR-WB IO SID",
			p:    Payload{CMR: NewCMR(RequestIO, uint8(AMRWBIO12650)), Frames: []Frame{ioSID}},
			want: cat([]byte{0b1_001_0010, 0b0_0_1_1_1001}, ioSID.Data),
		},
		{
			name: "damaged EVS AMR-WB IO 6.6 and SPEECH_LOST, padded per frame",
			p:    Payload{CMR: NoRequest, Frames: []Frame{io66, {Mode: AMRWBIO, Type: SpeechLost, Quality: true}, io2385}},
			want: cat([]byte{0xff, 0b0_1_1_0_0000, 0b0_1_1_1_1110, 0b0_0_1_1_1000}, io66.Data, io2385.Data),
		},
	} {
		bits, err := tc.p.check()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := marshalHeaderFull(tc.p, bits)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("%s:\ngot  %x\nwant %x", tc.name, got, tc.want)
		}
		back, err := unmarshalHeaderFull(got)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		want := tc.p
		if tc.back != nil {
			want = *tc.back
		}
		if !reflect.DeepEqual(back, want) {
			t.Errorf("%s: read back\n%+v\nwant\n%+v", tc.name, back, want)
		}
	}
}

// TestHeaderFullPadding checks that the receiver discards padding: the
// bits after an EVS AMR-WB IO frame inside its last octet, and whole
// octets after the last frame (A.2.2.1, A.2.2.1.3).
func TestHeaderFullPadding(t *testing.T) {
	io66 := frame(t, AMRWBIO, AMRWBIO6600, 7) // 132 bits: 4 padding bits
	b := cat([]byte{0xff, 0b0_0_1_1_0000}, io66.Data, []byte{0, 0})
	b[len(b)-3] |= 0x0f
	p, err := unmarshalHeaderFull(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Frames) != 1 || !bytes.Equal(p.Frames[0].Data, io66.Data) {
		t.Errorf("got %+v, want the frame without padding bits", p)
	}
}

func TestHeaderFullUnknownCMRIsIgnored(t *testing.T) {
	p, err := unmarshalHeaderFull([]byte{0b1_000_0111, 0b0_0_0_0_1111}) // T=000 D=0111 is not used
	if err != nil {
		t.Fatal(err)
	}
	if p.CMR != NoRequest {
		t.Errorf("CMR %s, want NO_REQ", p.CMR)
	}
}

func TestHeaderFullMalformed(t *testing.T) {
	p132 := frame(t, Primary, Primary13200, 1)
	for name, b := range map[string][]byte{
		"empty":                     {},
		"CMR only":                  {0xb4},
		"F = 1 on the last ToC":     {0b0_1_0_0_0100},
		"two CMR bytes":             cat([]byte{0xb4, 0xb4, 0x04}, p132.Data),
		"frame cut short":           cat([]byte{0x04}, p132.Data[:32]),
		"second frame missing":      cat([]byte{0x44, 0x04}, p132.Data),
		"EVS Primary future use":    {0b0_0_0_0_1101},
		"EVS AMR-WB IO future use":  {0xff, 0b0_0_1_1_1010, 0, 0, 0, 0, 0},
		"future use after a frame":  cat([]byte{0x44, 0b0_0_0_0_1101}, p132.Data),
		"SID cut short after a CMR": {0x92, 0x39, 1, 2, 3, 4},
	} {
		if p, err := unmarshalHeaderFull(b); err == nil {
			t.Errorf("%s: accepted as %+v", name, p)
		}
	}
}

func TestPayloadCheck(t *testing.T) {
	for name, p := range map[string]Payload{
		"no frames":    {},
		"CMR not used": {CMR: 0x87, Frames: []Frame{{Mode: Primary, Type: NoData, Quality: true}}},
		"bad frame":    {Frames: []Frame{{Mode: Primary, Type: Primary13200, Quality: true}}},
	} {
		if _, err := p.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
