package evs_test

import (
	"fmt"

	"github.com/yuu-biz/ims-practice/internal/evs"
)

// A UE in a session without hf-only sends one EVS Primary 13.2 kbit/s
// frame per packet. Alone, it goes in the Compact format; with a codec
// mode request, in the Header-Full format.
func ExampleFormat_Marshal() {
	frame := evs.Frame{Mode: evs.Primary, Type: evs.Primary13200, Quality: true, Data: make([]byte, 33)}
	var session evs.Format // hf-only absent, so hf-only=0

	b, _ := session.Marshal(evs.Payload{Frames: []evs.Frame{frame}})
	fmt.Println(8*len(b), "bits, Compact:", session.IsCompact(b))

	cmr := evs.NewCMR(evs.RequestSWB, uint8(evs.Primary24400))
	b, _ = session.Marshal(evs.Payload{CMR: cmr, Frames: []evs.Frame{frame}})
	fmt.Printf("%d bits, Compact: %v, header % x\n", 8*len(b), session.IsCompact(b), b[:2])

	p, _ := session.Unmarshal(b)
	f := p.Frames[0]
	fmt.Println(p.CMR, "|", f.Mode, f.Mode.Bitrate(f.Type), "kbit/s")
	// Output:
	// 264 bits, Compact: true
	// 280 bits, Compact: false, header b6 04
	// SWB 24.4 | EVS Primary 13.2 kbit/s
}

// An EVS AMR-WB IO frame read from an AMR-WB file is sent as EVS.
// Alone it goes in the Compact format after a 3-bit CMR; its comfort
// noise frame needs the Header-Full format with a CMR byte, which
// shares the 56-bit size of a Compact EVS Primary 2.8 kbit/s frame.
func ExampleFormat_Unmarshal() {
	var session evs.Format
	speech := evs.Frame{Mode: evs.AMRWBIO, Type: evs.AMRWBIO12650, Quality: true, Data: make([]byte, 32)}
	sid := evs.Frame{Mode: evs.AMRWBIO, Type: evs.AMRWBIOSID, Quality: true, Data: make([]byte, 5)}
	for _, f := range []evs.Frame{speech, sid} {
		b, _ := session.Marshal(evs.Payload{Frames: []evs.Frame{f}})
		p, _ := session.Unmarshal(b)
		got := p.Frames[0]
		fmt.Println(8*len(b), "bits, Compact:", session.IsCompact(b), "|", p.CMR, "|", got.Mode, got.Mode.Bitrate(got.Type))
	}
	// Output:
	// 256 bits, Compact: true | NO_REQ | EVS AMR-WB IO 12.65
	// 56 bits, Compact: false | NO_REQ | EVS AMR-WB IO 2
}
