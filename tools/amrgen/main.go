//go:build amrgen

// Command amrgen encodes a sine tone into an AMR or AMR-WB file in the
// storage format of RFC 4867 5, for the tests and the UE simulator.
// The product never encodes speech, so this
// tool is the only place that links an encoder, and the build tag
// keeps it out of every normal build.
//
// It needs cgo and the encoders from opencore-amr (AMR) and
// vo-amrwbenc (AMR-WB), both under the Apache License 2.0. On Ubuntu or
// Debian:
//
//	apt-get install libopencore-amrnb-dev libvo-amrwbenc-dev
//	CGO_ENABLED=1 go run -tags amrgen ./tools/amrgen -codec AMR-WB -mode 8 -hz 440 -seconds 1 -o internal/amr/testdata/tone-amrwb-23k85.amr
//
// On Alpine the packages are opencore-amr-dev and vo-amrwbenc-dev.
package main

/*
#cgo LDFLAGS: -lopencore-amrnb -lvo-amrwbenc
#include <opencore-amrnb/interf_enc.h>
#include <vo-amrwbenc/enc_if.h>
*/
import "C"

import (
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"unsafe"
)

func main() {
	codec := flag.String("codec", "AMR-WB", "AMR or AMR-WB")
	mode := flag.Int("mode", 8, "speech mode: 0-7 for AMR, 0-8 for AMR-WB")
	hz := flag.Float64("hz", 440, "frequency of the tone")
	seconds := flag.Float64("seconds", 1, "length of the tone")
	out := flag.String("o", "", "output file")
	flag.Parse()
	if *out == "" {
		log.Fatal("amrgen: -o is required")
	}
	file, err := encode(*codec, *mode, *hz, *seconds)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, file, 0o644); err != nil {
		log.Fatal(err)
	}
}

// encode returns the whole file. Both encoders write each frame with
// the one-octet header of RFC 4867 5.3, so the file is the magic
// number followed by what they return.
func encode(codec string, mode int, hz, seconds float64) ([]byte, error) {
	var rate int
	var file []byte
	var frame func(pcm []int16) []byte
	buf := make([]byte, 64) // the largest frame, AMR-WB 23.85, is 61 octets with its header
	out := (*C.uchar)(unsafe.Pointer(&buf[0]))
	switch codec {
	case "AMR":
		if mode < 0 || mode > 7 {
			return nil, fmt.Errorf("amrgen: AMR mode %d", mode)
		}
		rate, file = 8000, []byte("#!AMR\n")
		state := C.Encoder_Interface_init(0) // no DTX: every frame is speech
		defer C.Encoder_Interface_exit(state)
		frame = func(pcm []int16) []byte {
			n := C.Encoder_Interface_Encode(state, C.enum_Mode(mode), (*C.short)(unsafe.Pointer(&pcm[0])), out, 0)
			return buf[:n]
		}
	case "AMR-WB":
		if mode < 0 || mode > 8 {
			return nil, fmt.Errorf("amrgen: AMR-WB mode %d", mode)
		}
		rate, file = 16000, []byte("#!AMR-WB\n")
		state := C.E_IF_init()
		defer C.E_IF_exit(state)
		frame = func(pcm []int16) []byte {
			n := C.E_IF_encode(state, C.int(mode), (*C.short)(unsafe.Pointer(&pcm[0])), out, 0)
			return buf[:n]
		}
	default:
		return nil, fmt.Errorf("amrgen: codec %q", codec)
	}
	samples := rate / 50 // 20 ms per frame
	frames := int(seconds * 50)
	pcm := make([]int16, samples)
	for f := range frames {
		for i := range pcm {
			t := float64(f*samples+i) / float64(rate)
			pcm[i] = int16(0.5 * math.MaxInt16 * math.Sin(2*math.Pi*hz*t))
		}
		file = append(file, frame(pcm)...)
	}
	return file, nil
}
