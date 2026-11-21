package rtcp

import "time"

// The limits of RFC 3550 A.1 for telling a jump in sequence numbers
// from packets that arrive late.
const (
	maxDropout  = 3000
	maxMisorder = 100
)

// Receiver keeps what this side has received from one source, to write
// its report block (RFC 3550 6.4.1). It belongs to one call: the
// caller keeps it with the call's media and needs no lock for it when
// one goroutine reads the socket.
//
// SIMPLIFIED: RFC 3550 A.1. A new source is accepted from its first
// packet, without the probation of MIN_SEQUENTIAL packets in order, and
// a large jump in sequence numbers is taken as packets lost rather than
// waiting for a second packet to confirm that the source restarted. The relay and the
// UE simulator are the only sources in a call, so there is no stray
// traffic to filter.
type Receiver struct {
	SSRC      uint32
	ClockRate int // of the RTP timestamp, to measure jitter

	started  bool
	baseSeq  uint16
	maxSeq   uint16
	cycles   uint32
	received uint32

	expectedPrior, receivedPrior uint32

	lastArrival   time.Time
	lastTimestamp uint32
	jitter        int64 // 16 times the jitter, as RFC 3550 A.8 keeps it

	lastSR        uint32
	lastSRArrival time.Time
}

// Packet records an RTP packet that arrived at arrival.
func (r *Receiver) Packet(seq uint16, timestamp uint32, arrival time.Time) {
	if !r.started {
		r.started, r.baseSeq, r.maxSeq = true, seq, seq
	} else {
		delta := seq - r.maxSeq
		switch {
		case delta < maxDropout:
			if seq < r.maxSeq {
				r.cycles += 1 << 16 // the 16-bit number wrapped
			}
			r.maxSeq = seq
		case delta <= 1<<16-maxMisorder:
			r.maxSeq = seq // a jump: start counting from here
		}
		// Otherwise the packet is late or a duplicate: it still counts
		// as received, which is why the loss can go negative.
	}
	r.received++

	// RFC 3550 6.4.1 and A.8: the jitter smooths the difference D in
	// transit time of consecutive packets, the gap between their
	// arrivals minus the gap between their timestamps, in timestamp
	// units. The timestamp gap is taken as signed 32 bits, so that the
	// timestamp may wrap.
	if r.received > 1 {
		arrivalGap := int64(arrival.Sub(r.lastArrival)) * int64(r.ClockRate) / int64(time.Second)
		d := arrivalGap - int64(int32(timestamp-r.lastTimestamp))
		if d < 0 {
			d = -d
		}
		r.jitter += d - (r.jitter+8)>>4
	}
	r.lastArrival, r.lastTimestamp = arrival, timestamp
}

// SenderReport records an SR from the source, for LSR and DLSR.
func (r *Receiver) SenderReport(sr SenderReport, arrival time.Time) {
	r.lastSR, r.lastSRArrival = Middle32(sr.NTPTime), arrival
}

// Block writes the report block for a report sent at now, and starts
// the next interval for the fraction lost (RFC 3550 A.3). It is false
// before the first packet: there is nothing to report on yet.
func (r *Receiver) Block(now time.Time) (ReportBlock, bool) {
	if !r.started {
		return ReportBlock{}, false
	}
	extendedMax := r.cycles + uint32(r.maxSeq)
	expected := extendedMax - uint32(r.baseSeq) + 1
	lost := int64(expected) - int64(r.received)
	lost = min(max(lost, -0x800000), 0x7fffff) // 24 bits, signed

	expectedInterval := expected - r.expectedPrior
	receivedInterval := r.received - r.receivedPrior
	r.expectedPrior, r.receivedPrior = expected, r.received
	var fraction uint8
	if lostInterval := int64(expectedInterval) - int64(receivedInterval); expectedInterval != 0 && lostInterval > 0 {
		fraction = uint8(lostInterval << 8 / int64(expectedInterval))
	}

	b := ReportBlock{
		SSRC:            r.SSRC,
		FractionLost:    fraction,
		CumulativeLost:  int32(lost),
		HighestSequence: extendedMax,
		Jitter:          uint32(r.jitter >> 4),
		LastSR:          r.lastSR,
	}
	if r.lastSR != 0 {
		b.DelaySinceLastSR = uint32(now.Sub(r.lastSRArrival) * 65536 / time.Second)
	}
	return b, true
}
