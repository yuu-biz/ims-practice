package evs

import (
	"math/rand/v2"
	"testing"
)

// frame makes a frame whose bits follow a pattern, so that a bit out
// of place shows. An EVS Primary 2.8 kbit/s frame gets d(0) = 0, as the
// encoder makes it (TS 26.445 A.2.1.3).
func frame(t *testing.T, m Mode, ft FrameType, seed uint64) Frame {
	t.Helper()
	bits, err := m.FrameBits(ft)
	if err != nil {
		t.Fatal(err)
	}
	if bits == 0 {
		return Frame{Mode: m, Type: ft, Quality: true}
	}
	r := rand.New(rand.NewPCG(seed, uint64(m)<<8|uint64(ft)))
	data := make([]byte, (bits+7)/8)
	for i := range bits {
		if r.IntN(2) == 1 {
			data[i/8] |= 1 << (7 - i%8)
		}
	}
	if m == Primary && ft == Primary2800 {
		data[0] &^= 0x80
	}
	return Frame{Mode: m, Type: ft, Quality: true, Data: data}
}

// TestFrameTypes checks every row of TS 26.445 Tables A.4 and A.5: the
// bit rate and the frame length, which for EVS Primary is the bit rate
// times 20 ms.
func TestFrameTypes(t *testing.T) {
	for _, tc := range []struct {
		m       Mode
		ft      FrameType
		bits    int
		bitrate float64
	}{
		{Primary, Primary2800, 56, 2.8},
		{Primary, Primary7200, 144, 7.2},
		{Primary, Primary8000, 160, 8.0},
		{Primary, Primary9600, 192, 9.6},
		{Primary, Primary13200, 264, 13.2},
		{Primary, Primary16400, 328, 16.4},
		{Primary, Primary24400, 488, 24.4},
		{Primary, Primary32000, 640, 32},
		{Primary, Primary48000, 960, 48},
		{Primary, Primary64000, 1280, 64},
		{Primary, Primary96000, 1920, 96},
		{Primary, Primary128000, 2560, 128},
		{Primary, PrimarySID, 48, 2.4},
		{Primary, SpeechLost, 0, 0},
		{Primary, NoData, 0, 0},
		{AMRWBIO, AMRWBIO6600, 132, 6.60},
		{AMRWBIO, AMRWBIO8850, 177, 8.85},
		{AMRWBIO, AMRWBIO12650, 253, 12.65},
		{AMRWBIO, AMRWBIO14250, 285, 14.25},
		{AMRWBIO, AMRWBIO15850, 317, 15.85},
		{AMRWBIO, AMRWBIO18250, 365, 18.25},
		{AMRWBIO, AMRWBIO19850, 397, 19.85},
		{AMRWBIO, AMRWBIO23050, 461, 23.05},
		{AMRWBIO, AMRWBIO23850, 477, 23.85},
		{AMRWBIO, AMRWBIOSID, 40, 2.0},
		{AMRWBIO, SpeechLost, 0, 0},
		{AMRWBIO, NoData, 0, 0},
	} {
		bits, err := tc.m.FrameBits(tc.ft)
		if err != nil || bits != tc.bits {
			t.Errorf("%s frame type %d: %d bits, %v; want %d", tc.m, tc.ft, bits, err, tc.bits)
		}
		if got := tc.m.Bitrate(tc.ft); got != tc.bitrate {
			t.Errorf("%s frame type %d: %v kbit/s, want %v", tc.m, tc.ft, got, tc.bitrate)
		}
		if tc.m == Primary && tc.m.IsSpeech(tc.ft) && tc.bits != int(tc.bitrate*20+0.5) {
			t.Errorf("%s frame type %d: %d bits is not 20 ms at %v kbit/s", tc.m, tc.ft, tc.bits, tc.bitrate)
		}
	}
}

func TestFrameTypesForFutureUse(t *testing.T) {
	if _, err := Primary.FrameBits(13); err == nil {
		t.Error("EVS Primary frame type 13 is accepted")
	}
	for _, ft := range []FrameType{10, 11, 12, 13} {
		if _, err := AMRWBIO.FrameBits(ft); err == nil {
			t.Errorf("EVS AMR-WB IO frame type %d is accepted", ft)
		}
	}
}

func TestFrameCheck(t *testing.T) {
	ok := frame(t, Primary, Primary13200, 1)
	if _, err := ok.check(); err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]Frame{
		"short":           {Mode: Primary, Type: Primary13200, Quality: true, Data: ok.Data[1:]},
		"damaged primary": {Mode: Primary, Type: Primary13200, Data: ok.Data},
		"2.8 with d(0)=1": {Mode: Primary, Type: Primary2800, Quality: true, Data: []byte{0x80, 0, 0, 0, 0, 0, 0}},
		"unknown mode":    {Mode: 2, Type: NoData, Quality: true},
		"future use":      {Mode: AMRWBIO, Type: 10, Quality: true},
	} {
		if _, err := f.check(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
