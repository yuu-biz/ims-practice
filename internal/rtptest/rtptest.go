// Package rtptest receives the RTP that reaches a party in a test, a
// plain UDP socket that stands in for the media of a UE.
// Only tests use it: no binary in cmd builds it in.
//
// Every wait ends on something the test can observe: a number of
// packets known in advance (the frames of a WAV file), a packet with a
// given content, or a marker that the test sends to the socket itself.
// None ends on a pause in the media: on a busy machine the sender can
// pause for longer than any pause a test could pick, and a test that
// takes a pause for the end of the media then misses the rest of it.
package rtptest

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"math"
	"net"
	"net/netip"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/wav"
)

const (
	// PCMU is the static payload type of G.711 mu-law (RFC 3551 6).
	PCMU = 0
	// SampleRate is the clock rate of PCMU (RFC 3551 4.5.14).
	SampleRate = 8000
	// FrameSize is the number of samples in 20 ms of PCMU, one packet.
	FrameSize = 160
)

// Frames are the PCMU payloads of a WAV file, 20 ms each, the last one
// padded with silence (0xFF in mu-law), as an MRFP sends them.
func Frames(t testing.TB, path string) [][]byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	samples, err := wav.Read(f)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for start := 0; start < len(samples); start += FrameSize {
		frame := bytes.Repeat([]byte{0xFF}, FrameSize)
		copy(frame, g711.Encode(samples[start:min(start+FrameSize, len(samples))]))
		out = append(out, frame)
	}
	return out
}

// Next returns the next RTP packet that reaches c. It fails the test
// when none comes within limit, naming what, so that a test that waits
// says what it waited for.
func Next(t testing.TB, c *net.UDPConn, limit time.Duration, what string) rtp.Packet {
	t.Helper()
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(limit))
	defer c.SetReadDeadline(time.Time{})
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("no RTP packet (%s) within %v: %v", what, limit, err)
	}
	p, err := rtp.Unmarshal(buf[:n])
	if err != nil {
		t.Fatalf("%x is no RTP packet (%s): %v", buf[:n], what, err)
	}
	p.Payload = bytes.Clone(p.Payload)
	return p
}

// ReadPCMU returns the payloads of the next n packets that reach c,
// which must all be PCMU. It fails the test when they have not all come
// within limit.
func ReadPCMU(t testing.TB, c *net.UDPConn, n int, limit time.Duration) [][]byte {
	t.Helper()
	deadline := time.Now().Add(limit)
	got := make([][]byte, 0, n)
	for len(got) < n {
		p := Next(t, c, time.Until(deadline), fmt.Sprintf("packet %d of %d", len(got)+1, n))
		if p.PayloadType != PCMU {
			t.Fatalf("packet %d has payload type %d, want PCMU", len(got)+1, p.PayloadType)
		}
		got = append(got, p.Payload)
	}
	return got
}

// Queued returns the RTP packets that had reached c when Queued was
// called, and leaves c with none.
//
// It sends c a marker from a socket of its own and reads up to it. On
// loopback a datagram is in the queue of the receiving socket by the
// time the send that carried it returns, and the queue keeps the order
// of arrival. A test calls Queued once it has seen a sign that the
// sender has stopped, such as a final response, a BYE, or the MRFP
// letting go of its termination: then everything the sender sent is
// before the marker, and nothing comes after it.
func Queued(t testing.TB, c *net.UDPConn) []rtp.Packet {
	t.Helper()
	from, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	defer from.Close()
	marker := []byte("rtptest marker " + rand.Text())
	if _, err := from.WriteToUDPAddrPort(marker, c.LocalAddr().(*net.UDPAddr).AddrPort()); err != nil {
		t.Fatal(err)
	}
	markerFrom := from.LocalAddr().(*net.UDPAddr).AddrPort()
	var got []rtp.Packet
	buf := make([]byte, 1500)
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	defer c.SetReadDeadline(time.Time{})
	for {
		n, src, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			t.Fatalf("the marker sent to %v did not come back: %v", c.LocalAddr(), err)
		}
		if src == markerFrom && bytes.Equal(buf[:n], marker) {
			return got
		}
		p, err := rtp.Unmarshal(buf[:n])
		if err != nil {
			t.Fatalf("%x is no RTP packet: %v", buf[:n], err)
		}
		p.Payload = bytes.Clone(p.Payload)
		got = append(got, p)
	}
}

// Tone is n samples of a sine at hz, at a level well above the
// quantization noise of mu-law.
func Tone(hz float64, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(6000 * math.Sin(2*math.Pi*hz*float64(i)/SampleRate))
	}
	return out
}

// Power is the share of the signal at hz (the Goertzel algorithm), so
// that a test can tell which tones a mix holds.
func Power(samples []int16, hz float64) float64 {
	k := 2 * math.Cos(2*math.Pi*hz/SampleRate)
	var s1, s2 float64
	for _, x := range samples {
		s1, s2 = float64(x)+k*s1-s2, s1
	}
	n := float64(len(samples))
	return (s1*s1 + s2*s2 - k*s1*s2) / (n * n)
}

// Heard returns the decoded samples of n PCMU packets from c, starting
// with the first packet that holds every tone of hz. A mixer mixes what
// has reached it, so the first mixes of a conference hold only the
// parties whose packets came first; the test skips those, however long
// they last, instead of guessing how long. It fails the test when the n
// packets have not come within limit.
func Heard(t testing.TB, c *net.UDPConn, hz []float64, n int, limit time.Duration) []int16 {
	t.Helper()
	deadline := time.Now().Add(limit)
	var samples []int16
	for got := 0; got < n; {
		p := Next(t, c, time.Until(deadline), fmt.Sprintf("packet %d of %d of the mix", got+1, n))
		if p.PayloadType != PCMU {
			t.Fatalf("a packet of the mix has payload type %d, want PCMU", p.PayloadType)
		}
		frame := g711.Decode(p.Payload)
		if got == 0 && !holdsAll(frame, hz) {
			continue
		}
		samples = append(samples, frame...)
		got++
	}
	return samples
}

// holdsAll tells whether a frame holds each tone of hz at a tenth of
// the power of a lone Tone or more.
func holdsAll(frame []int16, hz []float64) bool {
	for _, f := range hz {
		if Power(frame, f) < 1e6 {
			return false
		}
	}
	return true
}

// The port ranges of PortRange lie between these, below 32768: Linux
// hands out ephemeral ports from 32768 up (60999 by default), Windows
// and macOS from 49152 up, so no socket bound to port 0 takes one.
const (
	firstPort = 20000
	lastPort  = 31999
	// blockSize is the most ports one range can have.
	blockSize = 40
)

// PortRange returns n UDP ports, minPort to maxPort, for the RTP of a
// relay that a test starts, with minPort even, as RTP takes the even
// port of a pair (RFC 3550 11), and which no other test that calls
// PortRange is given while this one runs: in this process, in a test
// process of another package, or in a second run of the same tests on
// the machine.
//
// The ranges are blocks of blockSize ports. A test claims a block by
// listening on TCP at the first port of it, which the kernel grants to
// one socket only; the claim ends with the test. TCP and UDP have ports
// of their own, so the claim does not take a port from the relay. A
// UDP port of the block that something else holds all the same is left
// out by the relay.
func PortRange(t testing.TB, n int) (minPort, maxPort uint16) {
	t.Helper()
	if n < 2 || n > blockSize {
		t.Fatalf("PortRange(%d): want 2 to %d ports", n, blockSize)
	}
	blocks := (lastPort - firstPort + 1) / blockSize
	start := os.Getpid() % blocks
	for i := range blocks {
		base := firstPort + ((start+i)%blocks)*blockSize
		claim, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(base))
		if err != nil {
			continue
		}
		t.Cleanup(func() { claim.Close() })
		return uint16(base), uint16(base + n - 1)
	}
	t.Fatalf("all %d port ranges between %d and %d are claimed", blocks, firstPort, lastPort)
	return 0, 0
}
