package interconnect

import (
	"errors"
	"fmt"
	"mime"
	"slices"
	"strconv"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the removal of the codecs the peer does not accept
// from an SDP body. TS 29.165 7.1 lets the IBCF remove codecs from the
// end-to-end negotiation, and leaves the list of codecs to the
// agreement (Table C.3.1.9). The addresses in the SDP are not changed
// here: that is the TrGW's part, which the IBCF controls (TS 24.229
// 6.7.1.4.1).

// ErrNoAcceptedCodec reports an SDP body in which no stream is left
// that the peer accepts. A request with such an offer is answered with
// 488 (Not Acceptable Here) (RFC 3261 21.4.26).
var ErrNoAcceptedCodec = errors.New("interconnect: no stream with a codec the peer accepts")

// FilterCodecs removes from each stream of s the payload formats whose
// encoding the peer does not accept, with their a=rtpmap, a=fmtp and
// a=rtcp-fb lines. A stream that loses all of them is rejected with
// port 0 and keeps its formats, since an m= line needs one (RFC 8866
// 5.14, RFC 3264 6). It reports ErrNoAcceptedCodec when s had a stream
// in use and has none left; s is then unchanged. accepted lists
// encoding names with an optional clock rate, as Profile.Codecs does.
//
// SIMPLIFIED: TS 26.114 6.2.5.2. The b=AS of a stream is not computed
// again for the codecs that are left. The UE's offer puts the value of
// its widest codec there (PCMU), so the value stays large enough.
func FilterCodecs(s *sdp.Session, accepted []string) error {
	filtered := slices.Clone(s.Media)
	inUse, left := false, false
	for i, m := range filtered {
		if m.Port == 0 {
			continue
		}
		inUse = true
		var kept []sdp.Codec
		var removed []int
		for _, c := range m.Codecs() {
			if acceptsCodec(accepted, c) {
				kept = append(kept, c)
			} else {
				removed = append(removed, c.PayloadType)
			}
		}
		// telephone-event carries DTMF next to a speech codec, no speech
		// of its own, so a stream with nothing else left is lost too.
		if !slices.ContainsFunc(kept, func(c sdp.Codec) bool { return !strings.EqualFold(c.Name, sdp.NameTelephoneEvent) }) {
			filtered[i].Port = 0
			continue
		}
		left = true
		m.Attributes = slices.DeleteFunc(slices.Clone(m.Attributes), func(a sdp.Attribute) bool {
			pt, _, _ := strings.Cut(a.Value, " ")
			n, err := strconv.Atoi(pt)
			return a.Name == "rtcp-fb" && err == nil && slices.Contains(removed, n)
		})
		m.SetCodecs(kept)
		filtered[i] = m
	}
	if inUse && !left {
		return ErrNoAcceptedCodec
	}
	s.Media = filtered
	return nil
}

func acceptsCodec(accepted []string, c sdp.Codec) bool {
	return slices.ContainsFunc(accepted, func(a string) bool {
		name, rate, err := parseCodecName(a)
		return err == nil && c.Name != "" && strings.EqualFold(name, c.Name) && (rate == 0 || rate == c.ClockRate)
	})
}

// parseCodecName reads "name" or "name/clock rate", the form of
// Profile.Codecs.
func parseCodecName(s string) (string, int, error) {
	name, rate, hasRate := strings.Cut(s, "/")
	if !isToken(name) {
		return "", 0, fmt.Errorf("codec %q: not an encoding name", s)
	}
	if !hasRate {
		return name, 0, nil
	}
	n, err := strconv.Atoi(rate)
	if err != nil || n <= 0 {
		return "", 0, fmt.Errorf("codec %q: the clock rate is not a number", s)
	}
	return name, n, nil
}

// FilterBody applies FilterCodecs to the SDP body of m, when m has one.
// A body that is not SDP stays as it is.
//
// SIMPLIFIED: TS 29.165 6.1.4. An SDP part inside a multipart body is
// not filtered. No node of this project sends one.
func FilterBody(m *sip.Message, accepted []string) error {
	if len(accepted) == 0 || len(m.Body) == 0 {
		return nil
	}
	mediaType, _, err := mime.ParseMediaType(m.Get("Content-Type"))
	if err != nil || mediaType != "application/sdp" {
		return nil
	}
	s, err := sdp.Parse(m.Body)
	if err != nil {
		return fmt.Errorf("interconnect: SDP body: %w", err)
	}
	if err := FilterCodecs(&s, accepted); err != nil {
		return err
	}
	m.Body = s.Bytes()
	return nil
}
