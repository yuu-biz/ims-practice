package evs

import "fmt"

// CMR is the codec mode request byte of the Header-Full format: the H
// bit (always 1), the 3-bit T field and the 4-bit D field (TS 26.445
// A.2.2.1.1, Figure A.4). The zero value, which has H = 0 and so is no
// CMR byte, stands for a payload without a codec mode request.
type CMR byte

// RequestType is the T field: what kind of request the D field holds
// (Table A.3).
type RequestType uint8

const (
	RequestNB  RequestType = 0b000 // EVS Primary, narrowband
	RequestIO  RequestType = 0b001 // EVS AMR-WB IO
	RequestWB  RequestType = 0b010 // EVS Primary, wideband
	RequestSWB RequestType = 0b011 // EVS Primary, super-wideband
	RequestFB  RequestType = 0b100 // EVS Primary, fullband
	// RequestWBChannelAware and RequestSWBChannelAware ask for the
	// 13.2 kbit/s channel-aware mode; D holds the FEC indicator and
	// the offset.
	RequestWBChannelAware  RequestType = 0b101
	RequestSWBChannelAware RequestType = 0b110
	// RequestReserved has only NO_REQ defined.
	RequestReserved RequestType = 0b111
)

// NoRequest is the code point NO_REQ: a CMR byte that requests nothing
// (Table A.3). The receiver treats it as no CMR.
const NoRequest CMR = 0xff

// NewCMR makes the CMR byte with the fields t and d.
func NewCMR(t RequestType, d uint8) CMR { return CMR(0x80 | byte(t&0x7)<<4 | d&0xf) }

// T is the type of request.
func (c CMR) T() RequestType { return RequestType(c >> 4 & 0x7) }

// D is the requested bit rate, or the channel-aware offset and FEC
// indicator.
func (c CMR) D() uint8 { return uint8(c & 0xf) }

// validD is the range of D that Table A.3 defines for each T. For the
// EVS Primary bandwidths, D is the frame type of Table A.4, except that
// 0 asks for 5.9 kbit/s source controlled VBR. For EVS AMR-WB IO it is
// the frame type of Table A.5.
var validD = map[RequestType][2]uint8{
	RequestNB:              {0, 6},  // 5.9 to 24.4
	RequestIO:              {0, 8},  // 6.6 to 23.85
	RequestWB:              {0, 11}, // 5.9 to 128
	RequestSWB:             {3, 11}, // 9.6 to 128
	RequestFB:              {5, 11}, // 16.4 to 128
	RequestWBChannelAware:  {0, 7},
	RequestSWBChannelAware: {0, 7},
	RequestReserved:        {15, 15}, // NO_REQ
}

// Valid tells that c is a CMR byte with a code point Table A.3 defines,
// not one marked "Not used" or "Reserved".
func (c CMR) Valid() bool {
	if c&0x80 == 0 {
		return false
	}
	r := validD[c.T()]
	return r[0] <= c.D() && c.D() <= r[1]
}

// String writes the code point as Table A.3 does, for logs: "SWB
// 13.2", "IO 23.85", "WB 13.2 CA-L-O3", "NO_REQ".
func (c CMR) String() string {
	if !c.Valid() {
		return fmt.Sprintf("CMR 0x%02x (not used)", byte(c))
	}
	d := c.D()
	switch t := c.T(); t {
	case RequestReserved:
		return "NO_REQ"
	case RequestIO:
		return fmt.Sprintf("IO %v", AMRWBIO.Bitrate(FrameType(d)))
	case RequestWBChannelAware, RequestSWBChannelAware:
		bw := "WB"
		if t == RequestSWBChannelAware {
			bw = "SWB"
		}
		fec := "L"
		if d&0b100 != 0 {
			fec = "H"
		}
		return fmt.Sprintf("%s 13.2 CA-%s-O%d", bw, fec, channelAwareOffsets[d&0b11])
	default:
		rate := "5.9 (VBR)"
		if d != 0 {
			rate = fmt.Sprint(Primary.Bitrate(FrameType(d)))
		}
		return fmt.Sprintf("%s %s", primaryBandwidths[t], rate)
	}
}

var primaryBandwidths = map[RequestType]string{
	RequestNB: "NB", RequestWB: "WB", RequestSWB: "SWB", RequestFB: "FB",
}

// channelAwareOffsets are the partial redundancy offsets in frames
// that the two low bits of D select (Table A.3, T = 101 and 110).
var channelAwareOffsets = [4]int{2, 3, 5, 7}

// compactCMR is the 3-bit CMR of the Compact EVS AMR-WB IO payload: the
// EVS AMR-WB IO frame type each code asks for (Table A.2). The code
// 111 is "none". 14.25 and 19.85 kbit/s have no code.
var compactCMR = [7]FrameType{
	AMRWBIO6600, AMRWBIO8850, AMRWBIO12650, AMRWBIO15850,
	AMRWBIO18250, AMRWBIO23050, AMRWBIO23850,
}

const compactCMRNone = 0b111

// compactCode is the 3-bit CMR that carries c. NO_REQ and the absence
// of a CMR both become "none", which A.2.1.2.1 defines as the same as
// no CMR. It fails for the requests the 3 bits cannot carry: any EVS
// Primary request, and EVS AMR-WB IO 14.25 and 19.85 kbit/s.
func (c CMR) compactCode() (uint, bool) {
	if c == 0 || c == NoRequest {
		return compactCMRNone, true
	}
	if c.T() != RequestIO {
		return 0, false
	}
	for code, ft := range compactCMR {
		if FrameType(c.D()) == ft {
			return uint(code), true
		}
	}
	return 0, false
}

// cmrFromCompact is the CMR byte that says the same as a 3-bit CMR.
func cmrFromCompact(code uint) CMR {
	if code == compactCMRNone {
		return NoRequest
	}
	return NewCMR(RequestIO, uint8(compactCMR[code]))
}
