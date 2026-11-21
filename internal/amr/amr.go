// Package amr carries AMR and AMR-WB speech frames in RTP and in files,
// in the formats of RFC 4867: the RTP payload in bandwidth-efficient
// and octet-aligned mode (4.3, 4.4), and the single-channel storage
// format (5).
//
// The package never encodes or decodes speech:
// frames come encoded from a file, travel as they are, and are written
// to a file on the other side. A frame is a string of bits whose
// length the frame type fixes (TS 26.101 and TS 26.201, Table 2 and
// Table 3).
//
// SIMPLIFIED: RFC 4867 4.4. The optional frame CRCs, robust sorting,
// interleaving and multi-channel sessions are not supported. A
// terminal may reject payload types that use them (TS 26.114 6.2.2.3,
// Table 6.3), and IR.92 offers none of them.
package amr

import "fmt"

// Codec is AMR (narrowband) or AMR-WB (wideband).
type Codec int

const (
	AMR Codec = iota
	AMRWB
)

// String is the encoding name of a=rtpmap (RFC 4867 8.3).
func (c Codec) String() string {
	if c == AMRWB {
		return "AMR-WB"
	}
	return "AMR"
}

// ClockRate is the RTP clock rate, the sampling rate of the codec
// (RFC 4867 4.1).
func (c Codec) ClockRate() int {
	if c == AMRWB {
		return 16000
	}
	return 8000
}

// SamplesPerFrame is how much the RTP timestamp grows per frame: 20 ms
// of samples (RFC 4867 4.1).
func (c Codec) SamplesPerFrame() uint32 { return uint32(c.ClockRate() / 50) }

// FrameType is the frame type index of TS 26.101 Table 1a (AMR) and TS
// 26.201 Table 1a (AMR-WB). The codec mode requested in a CMR uses the
// same numbers.
type FrameType uint8

const (
	SpeechLost FrameType = 14 // AMR-WB only
	NoData     FrameType = 15
	// NoModeRequest is the CMR of a payload that requests no mode
	// (RFC 4867 4.3.1).
	NoModeRequest FrameType = 15
)

// speechBits is the number of bits of each speech mode, indexed by
// frame type (TS 26.101 V19.0.0 Table 2, TS 26.201 V19.0.0 Table 2).
var speechBits = map[Codec][]int{
	AMR:   {95, 103, 118, 134, 148, 159, 204, 244},
	AMRWB: {132, 177, 253, 285, 317, 365, 397, 461, 477},
}

// bitrates are the speech modes in kbit/s, indexed by frame type
// (Table 1a of the same specifications).
var bitrates = map[Codec][]float64{
	AMR:   {4.75, 5.15, 5.90, 6.70, 7.40, 7.95, 10.2, 12.2},
	AMRWB: {6.60, 8.85, 12.65, 14.25, 15.85, 18.25, 19.85, 23.05, 23.85},
}

// sidBits is the comfort noise frame: 35 parameter bits, the SID type
// indicator and the mode indication of 3 bits (AMR) or 4 bits (AMR-WB)
// (TS 26.101 Table 3, TS 26.201 Table 3).
var sidBits = map[Codec]int{AMR: 39, AMRWB: 40}

// SID is the frame type of a comfort noise frame.
func (c Codec) SID() FrameType {
	if c == AMRWB {
		return 9
	}
	return 8
}

// Modes is the number of speech modes, so that frame types 0 to
// Modes()-1 are speech.
func (c Codec) Modes() int { return len(speechBits[c]) }

// Bitrate is the bit rate of speech mode ft in kbit/s, for logs.
func (c Codec) Bitrate(ft FrameType) float64 { return bitrates[c][ft] }

// IsSpeech tells that ft is one of the codec's speech modes.
func (c Codec) IsSpeech(ft FrameType) bool { return int(ft) < c.Modes() }

// FrameBits is the length in bits of a frame of type ft. NO_DATA and
// SPEECH_LOST have no bits. Frame types the payload format does not
// allow are an error: 9 to 14 for AMR (the comfort noise of GSM-EFR,
// TDMA-EFR and PDC-EFR among them) and 10 to 13 for AMR-WB (RFC 4867
// 4.3.2).
func (c Codec) FrameBits(ft FrameType) (int, error) {
	switch {
	case c.IsSpeech(ft):
		return speechBits[c][ft], nil
	case ft == c.SID():
		return sidBits[c], nil
	case ft == NoData, c == AMRWB && ft == SpeechLost:
		return 0, nil
	}
	return 0, fmt.Errorf("amr: frame type %d is not allowed for %s (RFC 4867 4.3.2)", ft, c)
}

// Frame is one speech or comfort noise frame.
type Frame struct {
	Type FrameType
	// Quality is the Q bit: false means the frame is damaged (RFC 4867
	// 4.3.2).
	Quality bool
	// Data holds the frame's bits d(0) to d(K-1), from the most
	// significant bit of the first octet on, in the order of TS 26.101
	// or TS 26.201. The last octet is padded with zero bits.
	Data []byte
}

func (c Codec) checkFrame(f Frame) (int, error) {
	bits, err := c.FrameBits(f.Type)
	if err != nil {
		return 0, err
	}
	if len(f.Data) != (bits+7)/8 {
		return 0, fmt.Errorf("amr: %s frame type %d has %d octets, want %d", c, f.Type, len(f.Data), (bits+7)/8)
	}
	return bits, nil
}
