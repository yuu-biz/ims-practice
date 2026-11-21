package mixer_test

import (
	"bytes"
	"fmt"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mixer"
)

// One tick of a three-party conference. a and b send opposite signals
// and c sends nothing. Each participant gets the other two: a hears b,
// b hears a, and for c the two cancel out.
func ExampleConference_Mix() {
	conf := mixer.NewConference()
	for _, id := range []string{"a", "b", "c"} {
		if err := conf.Join(id); err != nil {
			panic(err)
		}
	}
	// 0x80 and 0x00 are the loudest positive and negative mu-law codes,
	// 0xFF is silence.
	positive := bytes.Repeat([]byte{0x80}, mixer.FrameSize)
	negative := bytes.Repeat([]byte{0x00}, mixer.FrameSize)

	out, err := conf.Mix(map[string][]byte{"a": positive, "b": negative})
	if err != nil {
		panic(err)
	}
	for _, id := range conf.Participants() {
		fmt.Printf("%s hears %#02x (%d bytes)\n", id, out[id][0], len(out[id]))
	}
	// Output:
	// a hears 0x00 (160 bytes)
	// b hears 0x80 (160 bytes)
	// c hears 0xff (160 bytes)
}

// An announcement of 50 ms played twice with 20 ms of silence between
// the plays, as the Request-URI sip:annc@mrfc;play=...;repeat=2;delay=20
// of RFC 4240 asks. The caller sends one RTP packet per payload, every
// 20 ms, and ends the call when Next reports the end.
func ExampleClip_Play() {
	samples := make([]int16, 400) // 50 ms at 8 kHz; a real clip comes from ReadWAV
	clip, err := mixer.NewClip(samples)
	if err != nil {
		panic(err)
	}
	playback, err := clip.Play(mixer.PlayOptions{Repeat: 2, Delay: 20 * time.Millisecond})
	if err != nil {
		panic(err)
	}
	packets := 0
	for {
		payload, ok := playback.Next()
		if !ok {
			break
		}
		packets++
		_ = payload // the caller puts it in rtp.Packet{PayloadType: 0, Payload: payload}
	}
	fmt.Println(clip.Frames(), "payloads per play,", packets, "packets in all")
	// Output:
	// 3 payloads per play, 7 packets in all
}
