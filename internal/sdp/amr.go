package sdp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/amr"
)

// AMRFormat tells how to pack the RTP payload of c, an AMR or AMR-WB
// payload type (RFC 4867 8.1). It refuses a payload type that needs
// frame CRCs, robust sorting, interleaving or more than one channel,
// which the amr package does not support; TS 26.114 6.2.2.3 Table 6.3
// lets a terminal reject those.
func (c Codec) AMRFormat() (amr.Format, error) {
	var f amr.Format
	switch {
	case c.Is(NameAMRWB) && c.ClockRate == 16000:
		f.Codec = amr.AMRWB
	case c.Is(NameAMR) && c.ClockRate == 8000:
		f.Codec = amr.AMR
	default:
		return amr.Format{}, fmt.Errorf("sdp: payload type %d (%s/%d) is not AMR or AMR-WB", c.PayloadType, c.Name, c.ClockRate)
	}
	if c.Channels > 1 {
		return amr.Format{}, fmt.Errorf("sdp: payload type %d has %d channels", c.PayloadType, c.Channels)
	}
	for _, p := range []string{"crc", "robust-sorting", "interleaving"} {
		if v, ok := c.Param(p); ok && (p == "interleaving" || v == "1") {
			return amr.Format{}, fmt.Errorf("sdp: payload type %d uses %s", c.PayloadType, p)
		}
	}
	v, _ := c.Param("octet-align")
	f.OctetAligned = v == "1"
	return f, nil
}

// ModeSet returns the modes the mode-set parameter allows, or nil when
// the payload type allows all of them (RFC 4867 8.1).
func (c Codec) ModeSet() ([]amr.FrameType, error) {
	v, ok := c.Param("mode-set")
	if !ok {
		return nil, nil
	}
	f, err := c.AMRFormat()
	if err != nil {
		return nil, err
	}
	var modes []amr.FrameType
	for _, s := range strings.Split(v, ",") {
		n, err := strconv.Atoi(s)
		if err != nil || !f.Codec.IsSpeech(amr.FrameType(n)) {
			return nil, fmt.Errorf("sdp: mode-set=%s of payload type %d", v, c.PayloadType)
		}
		modes = append(modes, amr.FrameType(n))
	}
	return modes, nil
}

// highestMode is the mode b=AS has to fit: the highest of the mode-set,
// or the codec's highest mode (TS 26.114 6.2.5.2).
func (c Codec) highestMode() (amr.FrameType, error) {
	f, err := c.AMRFormat()
	if err != nil {
		return 0, err
	}
	modes, err := c.ModeSet()
	if err != nil {
		return 0, err
	}
	if modes == nil {
		return amr.FrameType(f.Codec.Modes() - 1), nil
	}
	return slices.Max(modes), nil
}

// amrFmtp is the a=fmtp of an AMR or AMR-WB payload type of a UE (TS
// 26.114 6.2.2.2 Tables 6.1 and 6.2 for an offer, 6.2.2.3 Table 6.4
// for an answer): no mode-set unless the offer had one, the capability
// to restrict mode changes, and no redundancy. An answer carries
// octet-align and mode-set as the offer did (RFC 4867 8.3.1).
func amrFmtp(octetAligned bool, modeSet string) string {
	var params []string
	if modeSet != "" {
		params = append(params, "mode-set="+modeSet)
	}
	params = append(params, "mode-change-capability=2", "max-red=0")
	if octetAligned {
		params = append(params, "octet-align=1")
	}
	return strings.Join(params, "; ")
}
