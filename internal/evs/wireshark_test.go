package evs

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestWiresharkDump writes RTP packets with EVS payloads as a text2pcap
// hex dump, to check by hand that Wireshark reads the payloads as the
// package means them. It runs only when EVS_TEXT2PCAP names the file to
// write. The payload type is the dynamic 96, which Wireshark does not
// map to EVS without an SDP, so name the mapping with "Decode As":
//
//	EVS_TEXT2PCAP=/tmp/evs.txt go test -run TestWiresharkDump ./internal/evs
//	text2pcap -4 192.0.2.1,192.0.2.2 -u 49152,49154 /tmp/evs.txt /tmp/evs.pcap
//	tshark -r /tmp/evs.pcap -d udp.port==49154,rtp -d rtp.pt==96,evs -V
//
// Packets from the Header-Full-only session (hf-only=1) need the
// preference -o evs.hf_only:TRUE as well; they are written to a second
// file, EVS_TEXT2PCAP with ".hf-only" added. Without it, the 160-bit
// payloads there read as Compact EVS Primary 8.0, as A.2.3.2 says a
// receiver must not. Even with it, tshark 4.2.2 reads the 56-bit
// Header-Full EVS Primary SID (ToC 0x0c and 48 bits) as a Compact 2.8
// kbit/s frame; the payload follows A.2.3.2 and the dissector does not.
func TestWiresharkDump(t *testing.T) {
	out := os.Getenv("EVS_TEXT2PCAP")
	if out == "" {
		t.Skip("set EVS_TEXT2PCAP to write the dump")
	}
	p132 := frame(t, Primary, Primary13200, 1)
	p244 := frame(t, Primary, Primary24400, 2)
	io2385 := frame(t, AMRWBIO, AMRWBIO23850, 3)
	io66 := frame(t, AMRWBIO, AMRWBIO6600, 4)
	noData := Frame{Mode: Primary, Type: NoData, Quality: true}
	payloads := []Payload{
		{Frames: []Frame{p132}},                              // Compact EVS Primary 13.2
		{Frames: []Frame{frame(t, Primary, Primary2800, 5)}}, // Compact 2.8, 56 bits
		{Frames: []Frame{frame(t, Primary, PrimarySID, 6)}},  // Compact SID
		{CMR: NewCMR(RequestSWB, uint8(Primary24400)), Frames: []Frame{p244}},
		{CMR: NewCMR(RequestWB, uint8(Primary13200)), Frames: []Frame{p132, p132, noData}},
		{CMR: NewCMR(RequestWB, uint8(Primary7200)), Frames: []Frame{frame(t, Primary, Primary7200, 7)}}, // padded from 160 bits
		{CMR: NewCMR(RequestIO, uint8(AMRWBIO23850)), Frames: []Frame{io2385}},                           // Compact AMR-WB IO
		{Frames: []Frame{io66}}, // Compact AMR-WB IO 6.6, CMR none
		{CMR: NewCMR(RequestIO, uint8(AMRWBIO12650)), Frames: []Frame{frame(t, AMRWBIO, AMRWBIOSID, 8)}}, // 56-bit special case
		{CMR: NewCMR(RequestIO, uint8(AMRWBIO14250)), Frames: []Frame{io2385}},                           // Header-Full AMR-WB IO
		{CMR: NewCMR(RequestSWBChannelAware, 0b0101), Frames: []Frame{p132}},
		{CMR: NoRequest, Frames: []Frame{{Mode: AMRWBIO, Type: SpeechLost, Quality: true}, io66}},
	}
	for _, f := range []Format{{}, {HeaderFullOnly: true}} {
		name := out
		if f.HeaderFullOnly {
			name += ".hf-only"
		}
		var dump strings.Builder
		for i, p := range payloads {
			b, err := f.Marshal(p)
			if err != nil {
				t.Fatalf("payload %d: %v", i+1, err)
			}
			dump.WriteString(hexDump(append(rtpHeader(uint16(i+1), uint32(320*i)), b...)))
		}
		if err := os.WriteFile(name, []byte(dump.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// rtpHeader is the fixed RTP header of RFC 3550 5.1: version 2, no
// padding, extension or CSRC, marker 0 and payload type 96.
func rtpHeader(seq uint16, timestamp uint32) []byte {
	h := []byte{0x80, 96, 0, 0, 0, 0, 0, 0, 0x12, 0x34, 0x56, 0x78}
	binary.BigEndian.PutUint16(h[2:], seq)
	binary.BigEndian.PutUint32(h[4:], timestamp)
	return h
}

// hexDump writes one packet in the input format of text2pcap.
func hexDump(b []byte) string {
	var s strings.Builder
	for i := 0; i < len(b); i += 16 {
		fmt.Fprintf(&s, "%06x", i)
		for _, c := range b[i:min(i+16, len(b))] {
			fmt.Fprintf(&s, " %02x", c)
		}
		s.WriteByte('\n')
	}
	return s.String()
}
