package amr

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"strings"
	"testing"
)

// frame makes a frame of type ft whose bits follow a pattern, so that a
// bit out of place shows.
func frame(t *testing.T, c Codec, ft FrameType, seed uint64) Frame {
	t.Helper()
	bits, err := c.FrameBits(ft)
	if err != nil {
		t.Fatal(err)
	}
	r := rand.New(rand.NewPCG(seed, uint64(ft)))
	data := make([]byte, (bits+7)/8)
	for i := range bits {
		if r.IntN(2) == 1 {
			data[i/8] |= 1 << (7 - i%8)
		}
	}
	return Frame{Type: ft, Quality: true, Data: data}
}

// bitString writes the payload the way the RFC figures draw it: fields
// as binary digits, then each frame's bits, then zero padding to the
// octet. It is built independently of the package's bit writer.
func bitString(c Codec, fields string, frames ...Frame) []byte {
	var s strings.Builder
	s.WriteString(fields)
	for _, f := range frames {
		bits, _ := c.FrameBits(f.Type)
		for i := range bits {
			s.WriteByte('0' + f.Data[i/8]>>(7-i%8)&1)
		}
	}
	return toBytes(s.String())
}

func toBytes(bits string) []byte {
	bits = strings.ReplaceAll(bits, " ", "")
	for len(bits)%8 != 0 {
		bits += "0"
	}
	out := make([]byte, len(bits)/8)
	for i, b := range bits {
		if b == '1' {
			out[i/8] |= 1 << (7 - i%8)
		}
	}
	return out
}

// TestBandwidthEfficientExamples are the payloads of RFC 4867 4.3.5.1
// and 4.3.5.2.
func TestBandwidthEfficientExamples(t *testing.T) {
	be := func(c Codec) Format { return Format{Codec: c} }

	// 4.3.5.1: CMR=15, one AMR 7.4 frame, two bits of padding.
	f74 := frame(t, AMR, 4, 1)
	got, err := be(AMR).Marshal(Payload{CMR: NoModeRequest, Frames: []Frame{f74}})
	if err != nil {
		t.Fatal(err)
	}
	want := bitString(AMR, "1111 0 0100 1", f74)
	if !bytes.Equal(got, want) || len(got) != 20 {
		t.Errorf("4.3.5.1:\ngot  %x\nwant %x (20 octets)", got, want)
	}

	// 4.3.5.2: CMR=1, AMR-WB 6.6, SID, NO_DATA and 8.85; seven bits of padding.
	frames := []Frame{frame(t, AMRWB, 0, 2), frame(t, AMRWB, 9, 3), {Type: NoData, Quality: true}, frame(t, AMRWB, 1, 4)}
	got, err = be(AMRWB).Marshal(Payload{CMR: 1, Frames: frames})
	if err != nil {
		t.Fatal(err)
	}
	want = bitString(AMRWB, "0001 1 0000 1 1 1001 1 1 1111 1 0 0001 1", frames[0], frames[1], frames[3])
	if !bytes.Equal(got, want) || len(got) != 48 {
		t.Errorf("4.3.5.2:\ngot  %x\nwant %x (48 octets)", got, want)
	}
	back, err := be(AMRWB).Unmarshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if back.CMR != 1 || len(back.Frames) != 4 {
		t.Fatalf("unmarshalled %+v", back)
	}
	for i := range frames {
		if back.Frames[i].Type != frames[i].Type || !bytes.Equal(back.Frames[i].Data, frames[i].Data) {
			t.Errorf("frame %d came back as %+v", i+1, back.Frames[i])
		}
	}
}

// TestOctetAlignedExample is RFC 4867 4.4.5.1: CMR=6 and two AMR 7.95
// frames, each padded with one zero bit to the octet.
func TestOctetAlignedExample(t *testing.T) {
	f1, f2 := frame(t, AMR, 5, 5), frame(t, AMR, 5, 6)
	got, err := Format{Codec: AMR, OctetAligned: true}.Marshal(Payload{CMR: 6, Frames: []Frame{f1, f2}})
	if err != nil {
		t.Fatal(err)
	}
	want := append(toBytes("0110 0000 1 0101 1 00 0 0101 1 00"), append(append([]byte{}, f1.Data...), f2.Data...)...)
	if !bytes.Equal(got, want) || len(got) != 3+20+20 {
		t.Errorf("got  %x\nwant %x", got, want)
	}
}

// TestRoundTrip packs and unpacks every frame type of both codecs in
// both formats.
func TestRoundTrip(t *testing.T) {
	for _, c := range []Codec{AMR, AMRWB} {
		types := []FrameType{c.SID(), NoData}
		for ft := range c.Modes() {
			types = append(types, FrameType(ft))
		}
		if c == AMRWB {
			types = append(types, SpeechLost)
		}
		for _, oa := range []bool{false, true} {
			format := Format{Codec: c, OctetAligned: oa}
			for _, ft := range types {
				in := Payload{CMR: NoModeRequest, Frames: []Frame{frame(t, c, ft, 7)}}
				in.Frames[0].Quality = ft%2 == 0
				b, err := format.Marshal(in)
				if err != nil {
					t.Fatalf("%+v type %d: %v", format, ft, err)
				}
				out, err := format.Unmarshal(b)
				if err != nil {
					t.Fatalf("%+v type %d: %v", format, ft, err)
				}
				got := out.Frames[0]
				if len(out.Frames) != 1 || got.Type != ft || got.Quality != in.Frames[0].Quality || !bytes.Equal(got.Data, in.Frames[0].Data) {
					t.Errorf("%+v type %d came back as %+v", format, ft, out)
				}
			}
		}
	}
}

func TestUnmarshalRefusesFrameTypes(t *testing.T) {
	for _, tc := range []struct {
		c      Codec
		ft     FrameType
		reason string
	}{
		{AMR, 9, "GSM-EFR SID must not be used"},
		{AMR, 14, "SPEECH_LOST only exists for AMR-WB"},
		{AMRWB, 10, "reserved"},
		{AMRWB, 13, "reserved"},
	} {
		b := toBytes("1111 0" + bits4(tc.ft) + "1")
		if _, err := (Format{Codec: tc.c}).Unmarshal(b); err == nil {
			t.Errorf("%s frame type %d accepted: %s", tc.c, tc.ft, tc.reason)
		}
	}
}

func bits4(ft FrameType) string {
	var s string
	for i := 3; i >= 0; i-- {
		s += string('0' + rune(ft>>i&1))
	}
	return s
}

func TestUnknownCMRIsIgnored(t *testing.T) {
	// CMR 8 is not an AMR mode (RFC 4867 4.3.1): treat it as no request.
	b := append(toBytes("1000 0 1111 1"), 0)
	p, err := Format{Codec: AMR}.Unmarshal(b)
	if err != nil || p.CMR != NoModeRequest {
		t.Errorf("Unmarshal = %+v, %v; want CMR %d", p, err, NoModeRequest)
	}
	if _, err := (Format{Codec: AMR}).Marshal(Payload{CMR: 8, Frames: []Frame{{Type: NoData}}}); err == nil {
		t.Error("Marshal sent CMR 8")
	}
}

func TestTruncated(t *testing.T) {
	f := frame(t, AMRWB, 8, 8)
	b, _ := Format{Codec: AMRWB}.Marshal(Payload{CMR: NoModeRequest, Frames: []Frame{f}})
	if _, err := (Format{Codec: AMRWB}).Unmarshal(b[:len(b)-2]); err == nil {
		t.Error("accepted a frame cut short")
	}
	if _, err := (Format{Codec: AMRWB}).Marshal(Payload{Frames: []Frame{{Type: 8, Data: f.Data[:10]}}}); err == nil {
		t.Error("packed a frame with too few octets")
	}
}

// TestStorage checks the magic numbers of RFC 4867 5.1 and the frame
// of the example in 5.3 (AMR 5.9, 118 bits).
func TestStorage(t *testing.T) {
	f59 := frame(t, AMR, 2, 9)
	var b bytes.Buffer
	if err := WriteStorage(&b, AMR, []Frame{f59, {Type: NoData, Quality: true}}); err != nil {
		t.Fatal(err)
	}
	wantHead, _ := hex.DecodeString("2321414d520a" + "14")
	if !bytes.HasPrefix(b.Bytes(), wantHead) || b.Len() != 6+1+15+1 {
		t.Errorf("file = %x", b.Bytes())
	}
	if last := b.Bytes()[b.Len()-1]; last != 0x7c {
		t.Errorf("NO_DATA header = %#x, want 0x7c", last)
	}
	c, frames, err := ReadStorage(&b)
	if err != nil || c != AMR || len(frames) != 2 || !bytes.Equal(frames[0].Data, f59.Data) {
		t.Errorf("ReadStorage = %v, %+v, %v", c, frames, err)
	}

	b.Reset()
	if err := WriteStorage(&b, AMRWB, []Frame{frame(t, AMRWB, 8, 10)}); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b.Bytes(), []byte{0x23, 0x21, 0x41, 0x4d, 0x52, 0x2d, 0x57, 0x42, 0x0a}) {
		t.Errorf("AMR-WB magic = %x", b.Bytes()[:9])
	}
	if c, _, err := ReadStorage(&b); err != nil || c != AMRWB {
		t.Errorf("ReadStorage = %v, %v", c, err)
	}

	if _, _, err := ReadStorage(strings.NewReader("#!AMR_MC1.0\n\x00\x00\x00\x02")); err == nil {
		t.Error("read a multi-channel file")
	}
	if _, _, err := ReadStorage(strings.NewReader("RIFF")); err == nil {
		t.Error("read a WAV file")
	}
	_, _, err = ReadStorage(strings.NewReader("#!AMR\n\x3c\x01"))
	if err == nil {
		t.Error("read a frame cut short")
	}
}

func TestTimestampStep(t *testing.T) {
	if AMR.SamplesPerFrame() != 160 || AMRWB.SamplesPerFrame() != 320 {
		t.Errorf("samples per frame %d and %d, want 160 and 320 (RFC 4867 4.1)", AMR.SamplesPerFrame(), AMRWB.SamplesPerFrame())
	}
}
