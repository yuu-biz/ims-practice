package rtptest

import (
	"bytes"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/rtp"
)

func socket(t *testing.T) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func sendPCMU(t *testing.T, from, to *net.UDPConn, payloads ...[]byte) {
	t.Helper()
	for i, payload := range payloads {
		p := rtp.Packet{PayloadType: PCMU, SequenceNumber: uint16(i), Payload: payload}
		if _, err := from.WriteToUDPAddrPort(p.Marshal(), to.LocalAddr().(*net.UDPAddr).AddrPort()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueuedReturnsWhatCameBeforeInOrder(t *testing.T) {
	from, to := socket(t), socket(t)
	sendPCMU(t, from, to, []byte("one"), []byte("two"), []byte("three"))
	got := Queued(t, to)
	if len(got) != 3 || string(got[0].Payload) != "one" || string(got[1].Payload) != "two" || string(got[2].Payload) != "three" {
		t.Fatalf("Queued = %v, want the three packets in order", got)
	}
	if again := Queued(t, to); len(again) != 0 {
		t.Errorf("Queued left %d packets behind", len(again))
	}
}

func TestReadPCMUReadsTheCountGiven(t *testing.T) {
	from, to := socket(t), socket(t)
	sendPCMU(t, from, to, []byte("a"), []byte("b"), []byte("c"))
	if got := ReadPCMU(t, to, PCMU, 2, time.Second); len(got) != 2 || string(got[1]) != "b" {
		t.Fatalf("ReadPCMU = %q, want a and b", got)
	}
	if rest := Queued(t, to); len(rest) != 1 || string(rest[0].Payload) != "c" {
		t.Errorf("after ReadPCMU, %v is left; want c", rest)
	}
}

func TestHeardSkipsMixesWithoutEveryTone(t *testing.T) {
	from, to := socket(t), socket(t)
	low, high := Tone(400, FrameSize), Tone(1000, FrameSize)
	both := make([]int16, FrameSize)
	for i := range both {
		both[i] = low[i] + high[i]
	}
	sendPCMU(t, from, to, g711.Encode(low), g711.Encode(both), g711.Encode(both), g711.Encode(low))
	samples := Heard(t, to, PCMU, []float64{400, 1000}, 2, time.Second)
	if want := append(g711.Decode(g711.Encode(both)), g711.Decode(g711.Encode(both))...); !bytes.Equal(g711.Encode(samples), g711.Encode(want)) {
		t.Error("Heard did not start at the first frame with both tones")
	}
}

func TestPortRangesDoNotOverlap(t *testing.T) {
	firstMin, firstMax := PortRange(t, 40)
	secondMin, secondMax := PortRange(t, 4)
	if firstMax-firstMin != 39 || secondMax-secondMin != 3 {
		t.Fatalf("got %d-%d and %d-%d, want 40 ports and 4", firstMin, firstMax, secondMin, secondMax)
	}
	if secondMin <= firstMax && firstMin <= secondMax {
		t.Errorf("%d-%d overlaps %d-%d", firstMin, firstMax, secondMin, secondMax)
	}
}
