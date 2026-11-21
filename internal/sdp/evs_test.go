package sdp

import (
	"testing"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

func TestEVSFormat(t *testing.T) {
	for _, tc := range []struct {
		codec Codec
		want  evs.Format
		ok    bool
	}{
		{Codec{Name: "EVS", ClockRate: 16000, Channels: 1, Fmtp: "br=5.9-24.4; bw=nb-swb; max-red=0"}, evs.Format{}, true},
		{Codec{Name: "evs", ClockRate: 16000}, evs.Format{}, true},
		{Codec{Name: "EVS", ClockRate: 16000, Fmtp: "hf-only=1"}, evs.Format{HeaderFullOnly: true}, true},
		{Codec{Name: "EVS", ClockRate: 16000, Fmtp: "hf-only=0"}, evs.Format{}, true},
		// The clock rate is 16000 whatever the bandwidth (TS 26.445 A.3.2).
		{Codec{Name: "EVS", ClockRate: 48000}, evs.Format{}, false},
		// Dual-mono, which the evs package leaves out.
		{Codec{Name: "EVS", ClockRate: 16000, Channels: 2}, evs.Format{}, false},
		{Codec{Name: "EVS", ClockRate: 16000, Fmtp: "ch-send=2; ch-recv=1"}, evs.Format{}, false},
		{Codec{Name: "EVS", ClockRate: 16000, Fmtp: "br=128; bw=nb"}, evs.Format{}, false},
		{Codec{Name: "AMR-WB", ClockRate: 16000}, evs.Format{}, false},
	} {
		got, err := tc.codec.EVSFormat()
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("%+v: EVSFormat() = %+v, %v", tc.codec, got, err)
		}
	}
}

// TestEVSIsNotAMR keeps the AMR helpers to AMR: an EVS payload type
// carries AMR-WB IO frames in its own format, not that of RFC 4867.
func TestEVSIsNotAMR(t *testing.T) {
	if _, err := (Codec{Name: NameEVS, ClockRate: 16000}).AMRFormat(); err == nil {
		t.Error("AMRFormat accepted EVS")
	}
}
