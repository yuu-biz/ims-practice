package sdp

import (
	"slices"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// evsASBandwidth is the b=AS of an EVS payload type in kbit/s: the
// larger of what its highest EVS Primary bit rate and its highest EVS
// AMR-WB IO mode need, whatever evs-mode-switch says (TS 26.114
// 6.2.5.2).
//
// The EVS Primary bit rate is the highest the sender of the SDP may
// receive: of br or br-recv, or with neither, the highest its
// bandwidths allow, 24.4 for narrowband and 128 above (6.2.5.2). Table
// A.6 of TS 26.445 decides both, as only its bit rates may be used.
// The payload is that of Annex Q.3, a CMR byte, a ToC byte per frame
// and the frames, as the evs package packs it: with hf-only=0 it adds
// the zero padding that keeps the size off the Compact ones (Q.4 NOTE
// 3), so 7.2 kbit/s gives the 25 of Table Q.1, and with hf-only=1 the
// 24 of Table 6.9.
//
// EVS AMR-WB IO counts as AMR-WB in the octet-aligned format with the
// same mode-set (6.2.5.2 NOTE 3, Q.4).
func evsASBandwidth(c Codec, ptime int) (int, error) {
	f, err := c.EVSFormat()
	if err != nil {
		return 0, err
	}
	p, err := c.EVSParams()
	if err != nil {
		return 0, err
	}
	rate, _ := highestUsable(p.receiveBitRates(), p.receiveBandwidths()) // Validate made sure there is one
	ft := rate.frameType()
	bits, _ := evs.Primary.FrameBits(ft)
	frame := evs.Frame{Mode: evs.Primary, Type: ft, Quality: true, Data: make([]byte, (bits+7)/8)}
	payload, err := f.Marshal(evs.Payload{CMR: evs.NoRequest, Frames: slices.Repeat([]evs.Frame{frame}, ptime/20)})
	if err != nil {
		return 0, err
	}
	bps := (8*len(payload) + headerBits) * 1000 / ptime
	primary := (bps + 999) / 1000

	amrwbio := Codec{Name: NameAMRWB, ClockRate: 16000, Fmtp: "octet-align=1"}
	if p.ModeSet != nil {
		amrwbio.Fmtp += "; mode-set=" + modeSetString(p.ModeSet)
	}
	io, err := ASBandwidth(amrwbio, ptime)
	if err != nil {
		return 0, err
	}
	return max(primary, io), nil
}
