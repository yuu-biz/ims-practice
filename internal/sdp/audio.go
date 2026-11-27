package sdp

import (
	"net/netip"
	"slices"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/amr"
)

// MaxPtime is the most audio a packet may carry, in ms: twelve frames
// of AMR (IR.92 3.2.5, TS 26.114 Table 7.1).
const MaxPtime = 240

// VoLTECodecs is what the UE offers, most preferred first:
// AMR-WB and AMR, each in the bandwidth-efficient format,
// which IR.92 3.2.5 asks the originating side to prefer, and then in
// the octet-aligned one; PCMU for softphones and for interconnects
// that need G.711 (JJ-90.30); and telephone-event at both clock rates
// (TS 26.114 G.3). No AMR payload type has a mode-set (IR.92 2.4.3.2).
func VoLTECodecs() []Codec {
	return []Codec{
		{PayloadType: 96, Name: NameAMRWB, ClockRate: 16000, Channels: 1, Fmtp: amrFmtp(false, "")},
		{PayloadType: 97, Name: NameAMRWB, ClockRate: 16000, Channels: 1, Fmtp: amrFmtp(true, "")},
		{PayloadType: 98, Name: NameAMR, ClockRate: 8000, Channels: 1, Fmtp: amrFmtp(false, "")},
		{PayloadType: 99, Name: NameAMR, ClockRate: 8000, Channels: 1, Fmtp: amrFmtp(true, "")},
		{PayloadType: PCMU, Name: NamePCMU, ClockRate: 8000},
		{PayloadType: 100, Name: NameTelephoneEvent, ClockRate: 16000, Fmtp: "0-15"},
		{PayloadType: 101, Name: NameTelephoneEvent, ClockRate: 8000, Fmtp: "0-15"},
	}
}

// VoNRCodecs is what the UE offers on NR access, most preferred first:
// the codecs of VoLTECodecs, AMR-WB before AMR (TS 26.114 5.2.1.6),
// which are all the UE can send.
//
// SIMPLIFIED: TS 26.114 5.2.1.1. A terminal is to support
// super-wideband speech, which takes EVS, and GSMA NG.114 asks a VoNR
// UE for EVS in its offer; this offer has none, as the UE has no EVS
// codec. An EVS payload type would offer EVS Primary: a terminal that
// offers EVS supports its whole bit-rate range (Table 6.2a).
func VoNRCodecs() []Codec {
	return VoLTECodecs()
}

// AudioOffer is an offer with one audio stream on addr that carries
// codecs, most preferred first (RFC 3264 5.1), with what IR.92 asks of
// a UE's offer: a=ptime:20 and a=maxptime:240 (3.2.5), b=AS for the
// codec that needs the most (TS 26.114 6.2.5.2), b=RS and b=RR
// (3.2.4), and sendrecv (2.2.4).
//
// The same offer goes into a reliable 18x or a 200 when an INVITE came
// without SDP (IR.92 2.2.4, RFC 3312 6).
func AudioOffer(sessionID string, addr netip.AddrPort, codecs []Codec) (Session, error) {
	m := Media{Type: "audio", Port: int(addr.Port()), Proto: "RTP/AVP"}
	m.SetCodecs(codecs)
	if err := m.setASBandwidth(Ptime); err != nil {
		return Session{}, err
	}
	m.setRTCPBandwidth(nil, Media{})
	m.Attributes = append(m.Attributes,
		Attribute{Name: "ptime", Value: strconv.Itoa(Ptime)},
		Attribute{Name: "maxptime", Value: strconv.Itoa(MaxPtime)},
		Attribute{Name: "sendrecv"})
	return Session{
		Origin:     Origin{Username: "-", SessionID: sessionID, Version: 1, Address: addr.Addr()},
		Name:       "-",
		Connection: addr.Addr(),
		Media:      []Media{m},
	}, nil
}

// AudioAnswer answers offer from addr (RFC 3264 6.1) with the codecs
// this side supports. The first audio stream is accepted with one
// speech codec: the first offered one that this side supports, which
// keeps the offerer's order of preference (RFC 3264 6.1, TS 26.114
// 6.2.2.3 Table 6.3). The answer adds the telephone-event of the same
// clock rate when the offer has one (TS 26.114 G.3). Every other
// stream is rejected with port 0.
//
// An AMR or AMR-WB answer keeps the offered payload type number,
// octet-align and mode-set (RFC 4867 8.3.1), and states this side's
// mode-change-capability and max-red (TS 26.114 Table 6.4).
//
// A mode-set on an AMR or AMR-WB codec of supported lists the modes this
// side sends in that format. An offered payload type with a mode-set is
// taken only when its mode-set allows each of them: the answerer returns
// an offered mode-set unmodified or removes the payload type, and the
// mode-set binds both sides (RFC 4867 8.3.1). The answer never carries
// the mode-set of supported.
func AudioAnswer(offer Session, sessionID string, addr netip.AddrPort, supported []Codec) (Session, error) {
	answer := Session{
		Origin:     Origin{Username: "-", SessionID: sessionID, Version: 1, Address: addr.Addr()},
		Name:       "-",
		Connection: addr.Addr(),
	}
	accepted := false
	for _, m := range offer.Media {
		reply := Media{Type: m.Type, Proto: m.Proto, Formats: m.Formats}
		if !accepted && m.Type == "audio" && m.Proto == "RTP/AVP" && m.Port != 0 {
			if codecs := chooseCodecs(m.Codecs(), supported); codecs != nil {
				accepted = true
				reply.Port = int(addr.Port())
				reply.SetCodecs(codecs)
				if err := reply.setASBandwidth(Ptime); err != nil {
					return Session{}, err
				}
				reply.setRTCPBandwidth(&offer, m)
				reply.Attributes = append(reply.Attributes,
					Attribute{Name: "ptime", Value: strconv.Itoa(Ptime)},
					Attribute{Name: "maxptime", Value: strconv.Itoa(MaxPtime)},
					Attribute{Name: AnswerDirection(offer.Direction(m))})
			}
		}
		answer.Media = append(answer.Media, reply)
	}
	if !accepted {
		return Session{}, ErrNoCommonCodec
	}
	return answer, nil
}

// chooseCodecs returns the codecs of an answer to offered, or nil.
func chooseCodecs(offered, supported []Codec) []Codec {
	for _, o := range offered {
		if o.Is(NameTelephoneEvent) || !slices.ContainsFunc(supported, func(s Codec) bool { return compatible(o, s) }) {
			continue
		}
		chosen := []Codec{answerCodec(o)}
		for _, te := range offered {
			if te.Is(NameTelephoneEvent) && te.ClockRate == o.ClockRate &&
				slices.ContainsFunc(supported, func(s Codec) bool { return compatible(te, s) }) {
				if te.Fmtp == "" {
					te.Fmtp = "0-15" // the default of RFC 4733 7.1.1, written out
				}
				chosen = append(chosen, te)
				break
			}
		}
		return chosen
	}
	return nil
}

// compatible tells that this side can use the offered payload format
// o as it supports s: the same encoding and clock rate, one channel,
// and for AMR the same octet-align, no option the amr package lacks, and
// no mode-set that leaves out a mode of the mode-set of s.
func compatible(o, s Codec) bool {
	if !o.Is(s.Name) || o.ClockRate != s.ClockRate || o.Channels > 1 {
		return false
	}
	if !o.Is(NameAMR) && !o.Is(NameAMRWB) {
		return true
	}
	of, err := o.AMRFormat()
	if err != nil {
		return false
	}
	sf, err := s.AMRFormat()
	if err != nil || of != sf {
		return false
	}
	allowed, err := o.ModeSet()
	if err != nil {
		return false
	}
	sent, err := s.ModeSet()
	return err == nil && (allowed == nil || !slices.ContainsFunc(sent, func(m amr.FrameType) bool { return !slices.Contains(allowed, m) }))
}

func answerCodec(o Codec) Codec {
	a := Codec{PayloadType: o.PayloadType, Name: o.Name, ClockRate: o.ClockRate, Channels: o.Channels, Fmtp: o.Fmtp}
	if f, err := o.AMRFormat(); err == nil {
		modeSet, _ := o.Param("mode-set")
		a.Fmtp = amrFmtp(f.OctetAligned, modeSet)
	}
	return a
}

// AudioMedia returns the first audio stream of s that is not rejected,
// or nil.
func (s *Session) AudioMedia() *Media {
	for i, m := range s.Media {
		if m.Type == "audio" && m.Port != 0 {
			return &s.Media[i]
		}
	}
	return nil
}
