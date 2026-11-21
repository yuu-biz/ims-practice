package rtcp

import "time"

// The fixed minimum interval of RFC 3550 6.2, and half of it before the
// first compound packet.
const (
	MinInterval     = 5 * time.Second
	initialInterval = MinInterval / 2
)

// compensation is e - 3/2, which divides the randomised interval so
// that, with timer reconsideration, the RTCP bandwidth comes out right
// on average (RFC 3550 6.3.1).
const compensation = 2.71828 - 1.5

// IntervalParams are the inputs of RFC 3550 6.3.1 for one participant.
type IntervalParams struct {
	// Members is the number of participants, and Senders those that
	// sent RTP in the last two intervals. A two-party call has 2
	// members.
	Members, Senders int
	// WeSent tells that this participant is one of the senders.
	WeSent bool
	// RS and RR are the RTCP bandwidth of senders and of receivers in
	// bit/s, from b=RS and b=RR (RFC 3556 2).
	RS, RR int
	// AvgSize is avg_rtcp_size: the average compound packet in octets,
	// UDP and IP headers included (RFC 3550 6.3.3).
	AvgSize float64
	// Initial tells that this participant has not sent a compound
	// packet yet.
	Initial bool
}

// Disabled tells that the SDP turned RTCP off with RS and RR both 0:
// no RTCP is sent (IR.92 3.2.4).
func (p IntervalParams) Disabled() bool { return p.RS+p.RR == 0 }

// Deterministic is the interval Td of RFC 3550 6.3.1: the share of the
// RTCP bandwidth this participant may use, but never below the fixed
// minimum. When the senders are few, they share RS and the others RR;
// otherwise everybody shares both alike (RFC 3556 2).
//
// SIMPLIFIED: RFC 3550 6.2. The reduced minimum interval (360 s divided
// by the session bandwidth in kbit/s) is not used: it is above 5 s for
// any speech codec of this project.
func (p IntervalParams) Deterministic() time.Duration {
	total := float64(p.RS+p.RR) / 8 // octets per second
	n := float64(p.Members)
	bandwidth := total
	if senderShare := float64(p.RS) / float64(p.RS+p.RR); float64(p.Senders) <= n*senderShare {
		if p.WeSent {
			bandwidth, n = total*senderShare, float64(p.Senders)
		} else {
			bandwidth, n = total*(1-senderShare), n-float64(p.Senders)
		}
	}
	td := time.Duration(n * p.AvgSize / bandwidth * float64(time.Second))
	minimum := MinInterval
	if p.Initial {
		minimum = initialInterval
	}
	return max(td, minimum)
}

// Interval is when the next compound packet is due: Td times a factor
// spread evenly over [0.5, 1.5), so that participants do not send in
// step, and divided by the compensation of RFC 3550 6.3.1. u is a
// random number in [0, 1).
//
// With Td at the 5 s minimum, the interval lies between 2.05 s and
// 6.16 s and is 4.1 s on average. IR.92 3.2.4 asks for RS and RR that
// bring an RTCP packet at least every 5 s; RTCPReceivers in package
// sdp is far above what Td needs, so the 5 s minimum is what remains.
//
// SIMPLIFIED: RFC 3550 6.3.4 to 6.3.7. Timer reconsideration when the
// timer expires, reverse reconsideration on BYE, the timeout of members
// and BYE reconsideration are left out: a call has exactly two members,
// who never change, and the interval is computed afresh before each
// packet.
func (p IntervalParams) Interval(u float64) time.Duration {
	return time.Duration(float64(p.Deterministic()) * (0.5 + u) / compensation)
}

// UpdateAvgSize folds the size of a compound packet sent or received
// into avg_rtcp_size (RFC 3550 6.3.3). size counts the UDP and IP
// headers too.
func UpdateAvgSize(avg float64, size int) float64 {
	return float64(size)/16 + avg*15/16
}
