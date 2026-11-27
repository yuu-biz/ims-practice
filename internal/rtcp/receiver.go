package rtcp

import "time"

// The constants of RFC 3550 A.1 for telling a jump in sequence numbers
// from packets that arrive late.
const (
	rtpSeqMod   = 1 << 16 // RTP_SEQ_MOD: sequence numbers are 16 bits
	maxDropout  = 3000    // MAX_DROPOUT
	maxMisorder = 100     // MAX_MISORDER
)

// Receiver keeps what this side has received from one source, to write
// its report block (RFC 3550 6.4.1). It belongs to one call: the
// caller keeps it with the call's media and needs no lock for it when
// one goroutine reads the socket.
//
// SIMPLIFIED: RFC 3550 A.1. A new source is accepted from its first
// packet, without the probation of MIN_SEQUENTIAL packets in order. The
// relay and the UE simulator are the only sources in a call, so there is
// no stray traffic to filter.
type Receiver struct {
	SSRC      uint32
	ClockRate int // of the RTP timestamp, to measure jitter

	started  bool
	baseSeq  uint16
	maxSeq   uint16
	badSeq   uint32 // RFC 3550 A.1 bad_seq; above 16 bits when no jump is waiting
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
	if !r.updateSeq(seq) {
		return
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

// updateSeq is update_seq of RFC 3550 A.1 without the probation. It is
// false for a packet that is not counted: the first packet of a jump of
// more than MAX_DROPOUT, which may be a stray packet. A jump is taken as
// a restart of the source only when the next packet follows it in
// sequence.
func (r *Receiver) updateSeq(seq uint16) bool {
	if !r.started {
		r.started = true
		r.initSeq(seq)
		return true
	}
	delta := seq - r.maxSeq
	switch {
	case delta < maxDropout:
		if seq < r.maxSeq {
			r.cycles += rtpSeqMod // the 16-bit number wrapped
		}
		r.maxSeq = seq
	case delta <= rtpSeqMod-maxMisorder:
		if uint32(seq) != r.badSeq {
			r.badSeq = uint32(seq + 1) // the number that would confirm the jump
			return false
		}
		r.initSeq(seq)
	}
	// Otherwise the packet is late or a duplicate: it still counts as
	// received, which is why the loss can go negative.
	return true
}

// initSeq is init_seq of RFC 3550 A.1: the statistics start again from
// seq.
func (r *Receiver) initSeq(seq uint16) {
	r.baseSeq, r.maxSeq = seq, seq
	r.badSeq = rtpSeqMod + 1
	r.cycles, r.received = 0, 0
	r.expectedPrior, r.receivedPrior = 0, 0
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
