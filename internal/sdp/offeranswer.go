package sdp

import (
	"errors"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// PCMU is G.711 mu-law, static payload type 0 (RFC 3551 6, Table 4).
// It is the codec of every call in this project.
const PCMU = 0

// Ptime is the packet time this project uses: 20 ms of audio, which is
// 160 bytes of G.711 per RTP packet.
const Ptime = 20

// ErrNoCommonCodec means the offer has no audio stream with a codec
// this side supports.
var ErrNoCommonCodec = errors.New("sdp: the offer has no audio stream with a codec this side supports")

// Offer is an offer with one audio stream of PCMU on addr (RFC 3264 5.1).
func Offer(sessionID string, addr netip.AddrPort) Session {
	return Session{
		Origin:     Origin{Username: "-", SessionID: sessionID, Version: 1, Address: addr.Addr()},
		Name:       "-",
		Connection: addr.Addr(),
		Media: []Media{{
			Type:    "audio",
			Port:    int(addr.Port()),
			Proto:   "RTP/AVP",
			Formats: []int{PCMU},
			Attributes: []Attribute{
				{Name: "rtpmap", Value: "0 PCMU/8000"},
				{Name: "ptime", Value: strconv.Itoa(Ptime)},
				{Name: "sendrecv"},
			},
		}},
	}
}

// Answer answers offer from addr (RFC 3264 6.1). The answer has one
// media description per offered one, in the same order: the first
// audio stream that offers PCMU is accepted with PCMU only, and every
// other stream is rejected with port 0.
func Answer(offer Session, sessionID string, addr netip.AddrPort) (Session, error) {
	answer := Session{
		Origin:     Origin{Username: "-", SessionID: sessionID, Version: 1, Address: addr.Addr()},
		Name:       "-",
		Connection: addr.Addr(),
	}
	accepted := false
	for _, m := range offer.Media {
		reply := Media{Type: m.Type, Proto: m.Proto, Formats: m.Formats}
		if !accepted && m.Type == "audio" && m.Proto == "RTP/AVP" && m.Port != 0 && slices.Contains(m.Formats, PCMU) {
			accepted = true
			reply.Port = int(addr.Port())
			reply.Formats = []int{PCMU}
			reply.Attributes = []Attribute{
				{Name: "rtpmap", Value: "0 PCMU/8000"},
				{Name: "ptime", Value: strconv.Itoa(Ptime)},
				{Name: AnswerDirection(offer.Direction(m))},
			}
		}
		answer.Media = append(answer.Media, reply)
	}
	if !accepted {
		return Session{}, ErrNoCommonCodec
	}
	return answer, nil
}

// Reject answers offer by declining every stream (RFC 3264 6): one
// media description for each offered one, in the same order, with the
// same media type, transport protocol and formats and port 0. A UA that
// ends the dialog instead of taking the offer of a 2xx sends it in its
// ACK (RFC 3261 13.2.2.4). addr is the address of the o= and c= lines;
// no media goes there.
func Reject(offer Session, sessionID string, addr netip.Addr) Session {
	answer := Session{
		Origin:     Origin{Username: "-", SessionID: sessionID, Version: 1, Address: addr},
		Name:       "-",
		Connection: addr,
	}
	for _, m := range offer.Media {
		answer.Media = append(answer.Media, Media{Type: m.Type, Proto: m.Proto, Formats: m.Formats})
	}
	return answer
}

// AnswerDirection is RFC 3264 6.1: sendonly is answered recvonly and
// the other way round, inactive stays inactive, and sendrecv is
// answered sendrecv.
func AnswerDirection(offered string) string {
	switch offered {
	case "sendonly":
		return "recvonly"
	case "recvonly":
		return "sendonly"
	}
	return offered
}

// Direction is RFC 8866 6.7: the direction attribute of the media
// description, or else the one at session level, or else sendrecv.
func (s Session) Direction(m Media) string {
	for _, a := range m.Attributes {
		if isDirection(a.Name) {
			return a.Name
		}
	}
	for _, l := range s.Lines {
		if name, ok := strings.CutPrefix(l, "a="); ok && isDirection(name) {
			return name
		}
	}
	return "sendrecv"
}

func isDirection(name string) bool {
	return name == "sendrecv" || name == "sendonly" || name == "recvonly" || name == "inactive"
}

// Audio returns where the audio stream of s goes and its payload type:
// the first audio description with a non-zero port.
func (s Session) Audio() (netip.AddrPort, int, error) {
	for _, m := range s.Media {
		if m.Type == "audio" && m.Port != 0 && len(m.Formats) > 0 {
			return s.Address(m), m.Formats[0], nil
		}
	}
	return netip.AddrPort{}, 0, errors.New("sdp: no audio stream")
}

// SetAudio points the audio stream of s at addr. A media relay rewrites
// the SDP it passes on this way, so that each side sends its RTP to the
// relay.
//
// A stream without a c= line of its own uses the session-level one
// (RFC 8866 5.7), so the session-level address changes only when no
// other stream uses it; the stream's own c= line is then dropped, so
// that the two cannot disagree. Otherwise the new address would move
// the other streams to the relay too, where nothing listens on their
// ports, and the stream gets a c= line of its own instead.
//
// The address of the o= line follows in both cases. It names the
// creator of the description, which is now the relay (RFC 8866 5.2).
func (s *Session) SetAudio(addr netip.AddrPort) error {
	for i, m := range s.Media {
		if m.Type == "audio" && m.Port != 0 {
			s.Media[i].Port = int(addr.Port())
			s.Origin.Address = addr.Addr()
			if s.othersUseSessionAddress(i) {
				s.Media[i].Connection = addr.Addr()
				return nil
			}
			s.Media[i].Connection = netip.Addr{}
			s.Connection = addr.Addr()
			return nil
		}
	}
	return errors.New("sdp: no audio stream")
}

// othersUseSessionAddress reports whether a stream other than the one at
// index i has no c= line of its own and so goes by the session-level
// one (RFC 8866 5.7).
func (s Session) othersUseSessionAddress(i int) bool {
	for j, m := range s.Media {
		if j != i && !m.Connection.IsValid() {
			return true
		}
	}
	return false
}

// HoldDirection is the direction to offer when putting a stream on
// hold (RFC 3264 8.4): a sendrecv stream becomes sendonly and a
// recvonly one inactive. The other side then answers recvonly or
// inactive by AnswerDirection. To take the stream back, offer the
// direction it had before.
func HoldDirection(current string) string {
	if current == "recvonly" || current == "inactive" {
		return "inactive"
	}
	return "sendonly"
}

// SetDirection replaces the direction attribute of m with dir.
func (m *Media) SetDirection(dir string) {
	m.Attributes = slices.DeleteFunc(m.Attributes, func(a Attribute) bool { return isDirection(a.Name) })
	m.Attributes = append(m.Attributes, Attribute{Name: dir})
}
