// Package announcements makes the announcements that the MRFP of this
// project plays. They are tones made here, so that no recorded
// speech or music with rights of its own enters the repository:
//
//   - notice, the three rising tones of the special information tone
//     (ITU-T E.180), which comes before an
//     announcement of the network, such as one for a barred call;
//   - cat1 and cat2, two short tunes that a user can choose as the
//     customized alerting tone that callers hear (TS 24.182 4.1).
//
// The WAV files next to this file are what Clips returns, written by
// go generate. The MRFP reads them from the paths of its configuration,
// and an MRFC asks for one by its name (RFC 4240 3.3).
//
//go:generate go run ./gen
package announcements

import (
	"math"

	"github.com/yuu-biz/ims-practice/internal/wav"
)

// Clips are the samples of each announcement, by name.
func Clips() map[string][]int16 {
	return map[string][]int16{
		"notice": tune(note{950, 330}, rest(30), note{1400, 330}, rest(30), note{1800, 330}, rest(30)),
		// C5 E5 G5 C6, then a rest, so that the tune loops evenly.
		"cat1": tune(note{523.25, 200}, rest(40), note{659.26, 200}, rest(40), note{783.99, 200}, rest(40), note{1046.50, 400}, rest(400)),
		// G4 B4 D5 B4, then a rest.
		"cat2": tune(note{392.00, 300}, rest(30), note{493.88, 300}, rest(30), note{587.33, 300}, rest(30), note{493.88, 300}, rest(300)),
	}
}

// note is a sine of hz for ms milliseconds; hz 0 is silence.
type note struct {
	hz float64
	ms int
}

func rest(ms int) note { return note{0, ms} }

// fade is how long each note takes to rise and to fall, so that it
// starts and ends without a click.
const fade = wav.SampleRate * 5 / 1000

func tune(notes ...note) []int16 {
	var out []int16
	for _, n := range notes {
		count := wav.SampleRate * n.ms / 1000
		for i := range count {
			if n.hz == 0 {
				out = append(out, 0)
				continue
			}
			gain := min(1, float64(i)/fade, float64(count-1-i)/fade)
			out = append(out, int16(math.Round(8000*gain*math.Sin(2*math.Pi*n.hz*float64(i)/wav.SampleRate))))
		}
	}
	return out
}
