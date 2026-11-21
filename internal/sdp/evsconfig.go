package sdp

import "github.com/yuu-biz/ims-practice/internal/evs"

// EVSConfiguration is one of the EVS configurations that GSMA NG.114
// v11.0 3.2.2.3 lets a UE offer: EVS Primary bit rates and audio
// bandwidths, all of them reaching super-wideband (NG.114 3.2.2.1).
type EVSConfiguration struct {
	Name       string
	BitRates   EVSBitRateRange
	Bandwidths EVSBandwidthRange
}

// The configurations of NG.114 3.2.2.3. A1 and A2 also offer narrowband
// and wideband; B0 to B2 offer super-wideband only.
var (
	EVSConfigA1 = EVSConfiguration{"A1", EVSBitRateRange{EVS5900, EVS13200}, EVSBandwidthRange{EVSNB, EVSSWB}}
	EVSConfigA2 = EVSConfiguration{"A2", EVSBitRateRange{EVS5900, EVS24400}, EVSBandwidthRange{EVSNB, EVSSWB}}
	EVSConfigB0 = EVSConfiguration{"B0", EVSBitRateRange{EVS13200, EVS13200}, EVSBandwidthRange{EVSSWB, EVSSWB}}
	EVSConfigB1 = EVSConfiguration{"B1", EVSBitRateRange{EVS9600, EVS13200}, EVSBandwidthRange{EVSSWB, EVSSWB}}
	EVSConfigB2 = EVSConfiguration{"B2", EVSBitRateRange{EVS9600, EVS24400}, EVSBandwidthRange{EVSSWB, EVSSWB}}
)

// ng114ModeSet is the mode-set NG.114 3.2.2.3 adds to an answer that
// selects A1, B0 or B1: EVS AMR-WB IO 6.6, 8.85 and 12.65. With it,
// AMR-WB IO needs no more b=AS than EVS Primary at 13.2 kbit/s (TS
// 26.114 A.14.3.1). Other answers get none, which allows every mode.
func ng114ModeSet(br EVSBitRateRange, bw EVSBandwidthRange) []evs.FrameType {
	for _, c := range []EVSConfiguration{EVSConfigA1, EVSConfigB0, EVSConfigB1} {
		if c.BitRates == br && c.Bandwidths == bw {
			return []evs.FrameType{evs.AMRWBIO6600, evs.AMRWBIO8850, evs.AMRWBIO12650}
		}
	}
	return nil
}
