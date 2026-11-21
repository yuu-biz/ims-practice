// Package evs carries EVS speech frames in RTP and in files, in the
// formats of TS 26.445 Annex A: the Compact and the Header-Full RTP
// payload (A.2.1, A.2.2) and the storage format (A.2.6).
//
// Like the amr package, it never encodes or decodes speech: frames come
// encoded from elsewhere, travel as they are, and are handed on. A
// frame is a string of bits whose length the operation mode and the
// frame type fix (Tables A.1, A.4 and A.5). EVS has two operation
// modes: EVS Primary, and EVS AMR-WB IO, which uses the bit rates and
// bit order of AMR-WB (TS 26.441 clause 4). Toward a terminal without
// EVS, EVS AMR-WB IO frames travel in the AMR-WB payload of RFC 4867
// (A.2.4), which the amr package packs.
//
// The package checks the payload formats only. Whether a frame type or
// a codec mode request fits what the session negotiated (br, bw,
// mode-set and the like of A.3.1) is for the caller to check.
//
// SIMPLIFIED: TS 26.445 A.2.5. Sessions with more than one mono
// channel (dual-mono) are not supported; every packet carries the
// frames of one channel. IR.92 and NG.114 do not ask for them, and TS
// 26.114 5.2.1.4 makes dual-mono optional.
package evs

import "fmt"

// Mode is the operation mode of a frame. Its value is the EVS mode bit
// of the ToC byte (A.2.2.1.2).
type Mode uint8

const (
	Primary Mode = iota
	AMRWBIO
)

// String names the mode as TS 26.445 does.
func (m Mode) String() string {
	if m == AMRWBIO {
		return "EVS AMR-WB IO"
	}
	return "EVS Primary"
}

// FrameType is the 4-bit EVS bit rate index of the ToC byte. What it
// means depends on the mode: Table A.4 for EVS Primary, Table A.5 for
// EVS AMR-WB IO. The values are those of the constants below.
type FrameType uint8

// EVS Primary frame types (Table A.4). The name gives the bit rate in
// bit/s.
const (
	Primary2800 FrameType = iota
	Primary7200
	Primary8000
	Primary9600
	Primary13200
	Primary16400
	Primary24400
	Primary32000
	Primary48000
	Primary64000
	Primary96000
	Primary128000
	PrimarySID // 2.4 kbit/s comfort noise
)

// EVS AMR-WB IO frame types (Table A.5). They are the frame types of
// AMR-WB (TS 26.201 Table 1a).
const (
	AMRWBIO6600 FrameType = iota
	AMRWBIO8850
	AMRWBIO12650
	AMRWBIO14250
	AMRWBIO15850
	AMRWBIO18250
	AMRWBIO19850
	AMRWBIO23050
	AMRWBIO23850
	AMRWBIOSID // 2.0 kbit/s comfort noise
)

// Frame types that carry no bits, in both modes (Tables A.4 and A.5).
const (
	SpeechLost FrameType = 14
	NoData     FrameType = 15
)

// speechBits is the length in bits of each speech frame, indexed by
// frame type. EVS Primary frames are 20 ms at the bit rate, which are
// the Compact sizes of Table A.1. EVS AMR-WB IO frames are those of
// AMR-WB (TS 26.201 V19.0.0 Table 2); Table A.1 lists them with the 3
// CMR bits and the padding of A.2.1.2.2 added.
var speechBits = map[Mode][]int{
	Primary: {56, 144, 160, 192, 264, 328, 488, 640, 960, 1280, 1920, 2560},
	AMRWBIO: {132, 177, 253, 285, 317, 365, 397, 461, 477},
}

// bitrates are the speech bit rates in kbit/s, indexed by frame type
// (Tables A.4 and A.5).
var bitrates = map[Mode][]float64{
	Primary: {2.8, 7.2, 8.0, 9.6, 13.2, 16.4, 24.4, 32, 48, 64, 96, 128},
	AMRWBIO: {6.60, 8.85, 12.65, 14.25, 15.85, 18.25, 19.85, 23.05, 23.85},
}

// sidBits is the comfort noise frame: 48 bits for EVS Primary (Table
// A.1), and d(0) to d(39) of AMR-WB for EVS AMR-WB IO (A.2.2.1.3,
// Figure A.6).
var sidBits = map[Mode]int{Primary: 48, AMRWBIO: 40}

var sidBitrates = map[Mode]float64{Primary: 2.4, AMRWBIO: 2.0}

// SID is the frame type of a comfort noise frame.
func (m Mode) SID() FrameType {
	if m == AMRWBIO {
		return AMRWBIOSID
	}
	return PrimarySID
}

// Modes is the number of speech frame types, so that frame types 0 to
// Modes()-1 are speech.
func (m Mode) Modes() int { return len(speechBits[m]) }

// IsSpeech tells that ft is one of the mode's speech frame types.
func (m Mode) IsSpeech(ft FrameType) bool { return int(ft) < m.Modes() }

// Bitrate is the bit rate of a speech or comfort noise frame in kbit/s,
// for logs. It is 0 for the other frame types.
func (m Mode) Bitrate(ft FrameType) float64 {
	switch {
	case m.IsSpeech(ft):
		return bitrates[m][ft]
	case ft == m.SID():
		return sidBitrates[m]
	}
	return 0
}

// FrameBits is the length in bits of a frame of type ft. NO_DATA and
// SPEECH_LOST have no bits. The frame types Tables A.4 and A.5 keep for
// future use are an error.
func (m Mode) FrameBits(ft FrameType) (int, error) {
	switch {
	case m.IsSpeech(ft):
		return speechBits[m][ft], nil
	case ft == m.SID():
		return sidBits[m], nil
	case ft == NoData, ft == SpeechLost:
		return 0, nil
	}
	return 0, fmt.Errorf("evs: frame type %d of %s is for future use (TS 26.445 Tables A.4 and A.5)", ft, m)
}

// Frame is one speech or comfort noise frame, or a NO_DATA or
// SPEECH_LOST entry.
type Frame struct {
	Mode Mode
	Type FrameType
	// Quality is the Q bit of an EVS AMR-WB IO frame: false means the
	// frame is severely damaged (Table A.5). An EVS Primary ToC byte has
	// no Q bit (Table A.4), so an EVS Primary frame always has Quality
	// true.
	Quality bool
	// Data holds the frame's bits from the most significant bit of the
	// first octet on, in the order the encoder produced them; for EVS
	// AMR-WB IO, d(0) to d(K-1) of AMR-WB. The last octet is padded
	// with zero bits.
	Data []byte
}

func (f Frame) check() (int, error) {
	if f.Mode > AMRWBIO {
		return 0, fmt.Errorf("evs: unknown mode %d", f.Mode)
	}
	bits, err := f.Mode.FrameBits(f.Type)
	if err != nil {
		return 0, err
	}
	if len(f.Data) != (bits+7)/8 {
		return 0, fmt.Errorf("evs: %s frame type %d has %d octets, want %d", f.Mode, f.Type, len(f.Data), (bits+7)/8)
	}
	if f.Mode == Primary && !f.Quality {
		return 0, fmt.Errorf("evs: an EVS Primary frame cannot be marked damaged; its ToC byte has no Q bit (TS 26.445 Table A.4)")
	}
	// A.2.1.3: the receiver tells a Compact 2.8 kbit/s frame from a
	// Header-Full AMR-WB IO SID by this bit, which the encoder always
	// sets to 0.
	if f.Mode == Primary && f.Type == Primary2800 && f.Data[0]&0x80 != 0 {
		return 0, fmt.Errorf("evs: the first bit d(0) of an EVS Primary 2.8 kbit/s frame must be 0 (TS 26.445 A.2.1.3)")
	}
	return bits, nil
}
