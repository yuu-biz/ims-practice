package agw

import (
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/g711"
	"github.com/yuu-biz/ims-practice/internal/mixer"
	"github.com/yuu-biz/ims-practice/internal/rtp"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// speak sends hz as PCMU from conn to dst every 20 ms until stop.
func speak(conn *net.UDPConn, dst netip.AddrPort, hz float64, stop <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	samples := rtptest.Tone(hz, 100*mixer.FrameSize)
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		frame := samples[(i%100)*mixer.FrameSize : (i%100+1)*mixer.FrameSize]
		p := rtp.Packet{PayloadType: 0, SequenceNumber: uint16(i), Timestamp: uint32(i * mixer.FrameSize), SSRC: uint32(hz), Payload: g711.Encode(frame)}
		conn.WriteToUDPAddrPort(p.Marshal(), dst)
	}
}

// TestConferenceGivesEachTheMixOfTheOthers is TS 23.333 6.2.9.1: with a
// third termination the context mixes, and each party hears the other
// two and never itself.
func TestConferenceGivesEachTheMixOfTheOthers(t *testing.T) {
	r := newRelay(t, 20)
	hz := map[TerminationID]float64{"a": 400, "b": 1000, "c": 1700}
	parties := map[TerminationID]*net.UDPConn{"a": udp(t), "b": udp(t), "c": udp(t)}
	c, err := r.Add(TerminationRequest{ID: "a", Remote: addr(parties["a"])})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []TerminationID{"b", "c"} {
		if _, err := r.AddTermination(c.ID, TerminationRequest{ID: id, Remote: addr(parties[id])}); err != nil {
			t.Fatal(err)
		}
	}
	stop := make(chan struct{})
	var speaking sync.WaitGroup
	for _, view := range r.Contexts()[0].Terminations {
		speaking.Add(1)
		go speak(parties[view.ID], view.Local, hz[view.ID], stop, &speaking)
	}
	defer speaking.Wait()
	defer close(stop)

	// Each party's socket keeps what comes while the test reads the
	// others.
	for id, conn := range parties {
		var others []float64
		for other, f := range hz {
			if other != id {
				others = append(others, f)
			}
		}
		samples := rtptest.Heard(t, conn, others, 30, 10*time.Second)
		for other, f := range hz {
			p := rtptest.Power(samples, f)
			switch {
			case other == id && p > 1e4:
				t.Errorf("%s hears its own %v Hz at %.0f", id, f, p)
			case other != id && p < 1e5:
				t.Errorf("%s hears %s's %v Hz at only %.0f", id, other, f, p)
			}
		}
	}
}

// TestConferenceOfTwoRelaysAgain: when a party leaves a conference of
// three, the two left get each other's packets as they are again.
func TestConferenceOfTwoRelaysAgain(t *testing.T) {
	r := newRelay(t, 20)
	a, b, c := udp(t), udp(t), udp(t)
	ctx, err := r.Add(TerminationRequest{ID: "a", Remote: addr(a)}, TerminationRequest{ID: "b", Remote: addr(b)}, TerminationRequest{ID: "c", Remote: addr(c)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readRTP(t, a, time.Second); !ok {
		t.Fatal("a conference of three sends nothing")
	}
	if _, _, err := r.SubtractTermination(ctx.ID, "c"); err != nil {
		t.Fatal(err)
	}
	// The mix that went out before the Subtract returned is all in a's
	// socket; with two terminations left the context mixes no more.
	rtptest.Queued(t, a)
	b.WriteToUDPAddrPort((rtp.Packet{PayloadType: 0, SSRC: 42, Payload: []byte("as it is")}).Marshal(), ctx.Terminations["b"].Local)
	p, ok := readRTP(t, a, time.Second)
	if !ok || p.SSRC != 42 || string(p.Payload) != "as it is" {
		t.Errorf("a got %v, %+v; want b's packet as b sent it", ok, p)
	}
}
