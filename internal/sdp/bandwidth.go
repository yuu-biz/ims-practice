package sdp

import (
	"fmt"
	"strings"
)

// headerBits are the IPv4, UDP and RTP headers of each packet: 20, 8 and
// 12 octets. b=AS counts them (TS 26.114 6.2.5.2 and Annex K).
const headerBits = (20 + 8 + 12) * 8

// ASBandwidth is the b=AS value in kbit/s that codec c needs with ptime
// ms of audio in each packet, rounded up: the RTP payload of its
// highest mode plus the headers of each packet, without RTCP (TS 26.114
// 6.2.5.2). For AMR and AMR-WB with ptime 20 this gives Tables 6.7 and
// 6.8, such as 29 for AMR 12.2 and 41 for AMR-WB 23.85 over IPv4 in the
// bandwidth-efficient format.
func ASBandwidth(c Codec, ptime int) (int, error) {
	frames := ptime / 20
	var payloadBits int
	switch {
	case c.Is(NameAMR), c.Is(NameAMRWB):
		f, err := c.AMRFormat()
		if err != nil {
			return 0, err
		}
		mode, err := c.highestMode()
		if err != nil {
			return 0, err
		}
		bits, _ := f.Codec.FrameBits(mode)
		if f.OctetAligned {
			// RFC 4867 4.4: one octet of CMR, and per frame one ToC
			// octet and the frame padded to the octet.
			payloadBits = 8 + frames*(8+(bits+7)/8*8)
		} else {
			// RFC 4867 4.3: 4 bits of CMR, and per frame 6 bits of ToC
			// and the frame, all padded to the octet at the end.
			payloadBits = (4 + frames*(6+bits) + 7) / 8 * 8
		}
	case c.Is(NameEVS):
		return evsASBandwidth(c, ptime)
	case c.Is(NamePCMU), c.Is("PCMA"):
		payloadBits = 8 * c.ClockRate * ptime / 1000 // one octet per sample
	case c.Is(NameTelephoneEvent):
		payloadBits = 4 * 8 // one event report (RFC 4733 2.3)
	default:
		return 0, fmt.Errorf("sdp: no bandwidth known for %s", c.Name)
	}
	bps := (payloadBits + headerBits) * 1000 / ptime
	return (bps + 999) / 1000, nil
}

// RTCP bandwidth in bit/s (RFC 3556 2). TS 26.114 A.6.2 allocates 4000
// bit/s in all to a speech session, 2000 for each of the two
// terminals, which leaves room for more than the compound packet of
// IR.92 3.2.4. RS of 0 does not stop senders from sending RTCP: they
// share the receivers' bandwidth (RFC 3556 2). Both values are below
// the limits of TS 26.114 7.3.1 (8000 and 6000).
const (
	RTCPSenders   = 0
	RTCPReceivers = 4000
)

// Bandwidth returns the b= line of this type that holds for m: its own,
// or else one before the first m= line, which gives the bandwidth of
// the session as a whole (RFC 3556 4). Only the first such line counts,
// as in Media.Bandwidth.
func (s Session) Bandwidth(m Media, bwtype string) (int, bool) {
	if v, ok := m.Bandwidth(bwtype); ok {
		return v, true
	}
	for _, l := range s.Lines {
		if v, ok := strings.CutPrefix(l, "b="); ok {
			if bw, err := parseBandwidth(v); err == nil && bw.Type == bwtype {
				return bw.Value, true
			}
		}
	}
	return 0, false
}

// RTCPBandwidth returns the RTCP bandwidth of m in bit/s: its b=RS and
// b=RR. An explicit line of the media comes first, then one of the
// session; a value that neither gives follows from b=AS, which the
// media gives or else the session, with the defaults of RFC 3556 3 and
// the order of RFC 3556 4. IR.92 3.2.4 asks for 5% of b=AS in all, 1.25%
// for senders and 3.75% for receivers when both are missing. Both 0
// means RTCP is off.
//
// b=AS of the session is the bandwidth of all its media, and calls carry
// one audio stream, so the stream takes all of it.
func (s Session) RTCPBandwidth(m Media) (rs, rr int) {
	rs, hasRS := s.Bandwidth(m, "RS")
	rr, hasRR := s.Bandwidth(m, "RR")
	as, _ := s.Bandwidth(m, "AS")
	total := as * 1000 * 5 / 100
	switch {
	case !hasRS && !hasRR:
		return as * 1000 * 125 / 10000, as * 1000 * 375 / 10000
	case !hasRS:
		return max(total-rr, 0), rr
	case !hasRR:
		return rs, max(total-rs, 0)
	}
	return rs, rr
}

// setRTCPBandwidth writes this side's b=RS and b=RR in the media
// section. An answer to offered, a stream of offer, answers zero with
// zero (IR.92 3.2.4): the zero that holds for the stream, its own line
// or else one of the session (RFC 3556 4). An offer passes nil.
func (m *Media) setRTCPBandwidth(offer *Session, offered Media) {
	rs, rr := RTCPSenders, RTCPReceivers
	if offer != nil {
		if v, ok := offer.Bandwidth(offered, "RS"); ok && v == 0 {
			rs = 0
		}
		if v, ok := offer.Bandwidth(offered, "RR"); ok && v == 0 {
			rr = 0
		}
	}
	m.SetBandwidth("RS", rs)
	m.SetBandwidth("RR", rr)
}

// setASBandwidth writes b=AS for the codec that needs the most: only one
// is sent at a time (TS 26.114 A.6.2).
func (m *Media) setASBandwidth(ptime int) error {
	as := 0
	for _, c := range m.Codecs() {
		if strings.EqualFold(c.Name, NameTelephoneEvent) {
			continue
		}
		v, err := ASBandwidth(c, ptime)
		if err != nil {
			return err
		}
		as = max(as, v)
	}
	m.SetBandwidth("AS", as)
	return nil
}
