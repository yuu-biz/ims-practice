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

// TestNoPacketOfASubtractedParty: a packet that the relay had read from
// the socket of a party just before the party was subtracted from a
// conference of three goes to neither of the two left, whose context
// now relays as it is. Once the Subtract has returned, nothing that the
// party sent leaves the context.
func TestNoPacketOfASubtractedParty(t *testing.T) {
	r := newRelay(t, 20)
	a, b, c := udp(t), udp(t), udp(t)
	ctx, err := r.Add(TerminationRequest{ID: "a", Remote: addr(a)}, TerminationRequest{ID: "b", Remote: addr(b)}, TerminationRequest{ID: "c", Remote: addr(c)})
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := r.context(ctx.ID)
	rc.mu.Lock()
	gone := rc.terms["c"]
	rc.mu.Unlock()
	if _, _, err := r.SubtractTermination(ctx.ID, "c"); err != nil {
		t.Fatal(err)
	}
	sent := func() map[TerminationID]uint64 {
		out := map[TerminationID]uint64{}
		for id, view := range r.Contexts()[0].Terminations {
			out[id] = view.PacketsSent
		}
		return out
	}
	before := sent()
	// What the reader of c's socket hands over after the Subtract.
	rc.receive(gone, rtpChannel, gone.rtp, (rtp.Packet{PayloadType: 0, SSRC: 42, Payload: []byte("too late")}).Marshal())
	if after := sent(); after["a"] != before["a"] || after["b"] != before["b"] {
		t.Errorf("packets sent went from %v to %v after the Subtract, want no packet of c", before, after)
	}
}

// TestMixAfterSilenceStartsATalkspurt: a conference that goes down to
// two parties stops mixing, and one that grows to three again mixes
// again from the same RTP source. Its timestamp has gone on over the
// silence, as the sampling clock did (RFC 3550 5.1), and the first
// packet after the silence has the marker set (RFC 3551 4.1).
func TestMixAfterSilenceStartsATalkspurt(t *testing.T) {
	const silence = 300 * time.Millisecond
	r := newRelay(t, 20)
	a, b, c := udp(t), udp(t), udp(t)
	ctx, err := r.Add(TerminationRequest{ID: "a", Remote: addr(a)}, TerminationRequest{ID: "b", Remote: addr(b)}, TerminationRequest{ID: "c", Remote: addr(c)})
	if err != nil {
		t.Fatal(err)
	}
	last := rtptest.Next(t, a, time.Second, "the mix of a conference of three")
	began := time.Now()
	if _, _, err := r.SubtractTermination(ctx.ID, "c"); err != nil {
		t.Fatal(err)
	}
	if queued := rtptest.Queued(t, a); len(queued) > 0 {
		last = queued[len(queued)-1]
	}
	stopped := time.Now()
	time.Sleep(silence)
	resumed := time.Now()
	if _, err := r.AddTermination(ctx.ID, TerminationRequest{ID: "c", Remote: addr(c)}); err != nil {
		t.Fatal(err)
	}
	first := rtptest.Next(t, a, time.Second, "the mix of the conference of three again")
	ended := time.Now()

	if first.SSRC != last.SSRC {
		t.Fatalf("the mix comes from SSRC %d after the silence, %d before; want one RTP source", first.SSRC, last.SSRC)
	}
	if !first.Marker {
		t.Error("the first packet after the silence has no marker")
	}
	frames := func(d time.Duration) uint32 { return uint32(d / tick) }
	advance := first.Timestamp - last.Timestamp
	// The silence lasted at least from the Subtract to the Add, and at
	// most from before the Subtract to the packet after the Add.
	if least, most := frames(resumed.Sub(stopped))*mixer.FrameSize, (frames(ended.Sub(began))+2)*mixer.FrameSize; advance < least || advance > most {
		t.Errorf("the timestamp went on by %d over the silence, want %d to %d", advance, least, most)
	}
}

// TestNoPacketOfASubtractedContext: a Subtract of the whole context
// marks it closed before it closes the sockets. A packet that the
// relay read from a socket just before, and hands over while the
// sockets are still open, is neither counted nor sent on.
func TestNoPacketOfASubtractedContext(t *testing.T) {
	r := newRelay(t, 20)
	a, b := udp(t), udp(t)
	rc := &relayContext{id: "closing", relay: r, terms: map[TerminationID]*relayTermination{}, mix: mixer.NewConference()}
	rc.mu.Lock()
	for id, conn := range map[TerminationID]*net.UDPConn{"a": a, "b": b} {
		if _, err := rc.add(TerminationRequest{ID: id, Remote: addr(conn)}); err != nil {
			t.Fatal(err)
		}
	}
	from, to := rc.terms["a"], rc.terms["b"]
	// What Subtract does before it lets go of rc.mu and closes the
	// sockets.
	rc.closed = true
	rc.mu.Unlock()
	defer rc.close()
	rc.receive(from, rtpChannel, from.rtp, (rtp.Packet{PayloadType: 0, SSRC: 42, Payload: []byte("too late")}).Marshal())
	if got, sent := from.rtp.received.Load(), to.rtp.sent.Load(); got != 0 || sent != 0 {
		t.Errorf("the closed context counted %d packets in and sent %d on, want none", got, sent)
	}
	if p, ok := readRTP(t, b, 100*time.Millisecond); ok {
		t.Errorf("b got %+v from a context that was subtracted", p)
	}
}

// handClocked is a context of three terminations, a, b and c, towards
// the sockets of parties, whose clock does not run: the test calls step
// itself, so that it decides when each tick comes.
func handClocked(t *testing.T, r *Relay) (*relayContext, map[TerminationID]*net.UDPConn) {
	t.Helper()
	parties := map[TerminationID]*net.UDPConn{"a": udp(t), "b": udp(t), "c": udp(t)}
	rc := &relayContext{id: "hand", relay: r, terms: map[TerminationID]*relayTermination{}, mix: mixer.NewConference()}
	rc.mu.Lock()
	for _, id := range []TerminationID{"a", "b", "c"} {
		if _, err := rc.add(TerminationRequest{ID: id, Remote: addr(parties[id])}); err != nil {
			t.Fatal(err)
		}
	}
	rc.mu.Unlock()
	t.Cleanup(func() {
		rc.mu.Lock()
		rc.closed = true
		rc.mu.Unlock()
		rc.close()
	})
	return rc, parties
}

// TestTheTimestampCountsTicksNotTheTimeOfDay: the timestamp of the
// node's own media goes on by the ticks of the clock (RFC 3550 5.1),
// however late or early a tick comes. A tick in which a party gets
// nothing adds a frame however soon the next one follows, and a tick
// that comes late adds no frame of its own.
func TestTheTimestampCountsTicksNotTheTimeOfDay(t *testing.T) {
	r := newRelay(t, 20)
	rc, parties := handClocked(t, r)
	setMode := func(id TerminationID, m Mode) {
		rc.mu.Lock()
		rc.terms[id].mode = m
		rc.mu.Unlock()
	}
	next := func(what string) rtp.Packet { return rtptest.Next(t, parties["a"], time.Second, what) }

	rc.step()
	first := next("the mix of the first tick")
	setMode("a", Inactive)
	rc.step() // a gets nothing in this tick
	setMode("a", SendReceive)
	rc.step() // at once
	after := next("the mix after a tick of silence")
	if !after.Marker || after.Timestamp-first.Timestamp != 2*mixer.FrameSize {
		t.Errorf("after one tick of silence: marker %v, timestamp on by %d; want the marker and %d",
			after.Marker, after.Timestamp-first.Timestamp, 2*mixer.FrameSize)
	}

	// A new announcement starts a talkspurt in the next tick, which
	// comes 31 ms late: the frames are contiguous all the same.
	clip, _ := ramp(t, 4)
	playback, err := clip.Play(mixer.PlayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rc.mu.Lock()
	rc.terms["a"].setSignals(&Signals{Play: &Play{Announcement: "ramp"}}, playback)
	rc.mu.Unlock()
	time.Sleep(tick + 11*time.Millisecond)
	rc.step()
	played := next("the first frame of the announcement")
	if !played.Marker || played.Timestamp-after.Timestamp != mixer.FrameSize {
		t.Errorf("an announcement in the tick after the mix: marker %v, timestamp on by %d; want the marker and %d",
			played.Marker, played.Timestamp-after.Timestamp, mixer.FrameSize)
	}
}
