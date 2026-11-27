package sdp

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// NameEVS is the encoding name of EVS in a=rtpmap (TS 26.445 A.3.1).
const NameEVS = "EVS"

// EVSParams reads the a=fmtp parameters of c, an EVS payload type,
// whose RTP clock rate is always 16000 (TS 26.445 A.3.2).
func (c Codec) EVSParams() (EVSParams, error) {
	if !c.Is(NameEVS) || c.ClockRate != 16000 {
		return EVSParams{}, fmt.Errorf("sdp: payload type %d (%s/%d) is not EVS/16000", c.PayloadType, c.Name, c.ClockRate)
	}
	return ParseEVSParams(c.Fmtp)
}

// EVSFormat tells how to pack the RTP payload of c, an EVS payload
// type: hf-only=1 allows the Header-Full format only (TS 26.445 A.3.1,
// A.2.3). It refuses more than one channel in either direction, which
// the evs package does not support (it leaves out dual-mono, A.2.5);
// TS 26.114 6.2.2.3 Table 6.3 lets a terminal reject those.
func (c Codec) EVSFormat() (evs.Format, error) {
	p, err := c.EVSParams()
	if err != nil {
		return evs.Format{}, err
	}
	if c.Channels > 1 || p.ChSend.Value > 1 || p.ChRecv.Value > 1 {
		return evs.Format{}, fmt.Errorf("sdp: payload type %d has more than one channel", c.PayloadType)
	}
	return evs.Format{HeaderFullOnly: p.HFOnly.Value}, nil
}
