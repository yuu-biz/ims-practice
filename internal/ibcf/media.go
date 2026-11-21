package ibcf

import (
	"errors"
	"log/slog"
	"strings"

	"github.com/yuu-biz/ims-practice/internal/interconnect"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file is the IBCF's part in the media: it reserves a TrGW context
// for the first SDP of a call that has an audio stream, points the SDP
// of each side at the termination that faces it (TS 24.229 6.7.1.4.1),
// and releases the context when the call ends. The codecs that the
// peer does not accept are gone from the SDP by then: interconnect.Border
// filters them out first (TS 29.165 7.1).

// releaseMedia releases the TrGW context c. A context that is gone has
// been released already.
//
// SIMPLIFIED: TS 29.238 Table 5.17.2.1.1. A release that fails is
// logged and not tried again, which leaves the context in the TrGW.
// The audit of TrGW contexts (5.17.3) that would find such a context is
// left out: a context left behind holds only its ports in the TrGW and
// does not disturb other calls.
func (b *IBCF) releaseMedia(c MediaContext, log *slog.Logger) {
	ctx, cancel := trgwContext()
	defer cancel()
	if err := b.trgw.Release(ctx, c.ID); err != nil && !errors.Is(err, ErrNoContext) {
		log.Warn("could not release the TrGW context", "context", c.ID, "error", err.Error())
		return
	}
	log.Info("TrGW context released", "context", c.ID)
}

// facing are the termination that faces where a message crossing in
// direction d comes from, and the one that faces where it goes.
func facing(d interconnect.Direction) (from, to Side) {
	if d == interconnect.Exit {
		return CoreSide, PeerSide
	}
	return PeerSide, CoreSide
}

// throughTrGW points the SDP of m, an offer or an answer that crosses
// the border in direction d, at the context c (TS 24.229 6.7.1.4.1): the
// termination facing where m comes from learns where that side takes
// its media, and m goes on with the address of the termination facing
// where it goes. A body without an audio stream that has a port is left
// as it is.
//
// SIMPLIFIED: TS 24.229 6.7.1.4.1 and RFC 3264 6. Only the first audio
// stream with a port goes through the TrGW, as the P-CSCF does with
// the media relay.
// Another stream keeps its address, and so would show the address of
// its sender to the other network. The UEs of this project offer one
// audio stream.
func (b *IBCF) throughTrGW(m *sip.Message, c MediaContext, d interconnect.Direction) error {
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return err
	}
	sender, _, err := description.Audio()
	if err != nil {
		return nil
	}
	from, to := facing(d)
	ctx, cancel := trgwContext()
	defer cancel()
	if err := b.trgw.Configure(ctx, c.ID, from, sender); err != nil {
		return err
	}
	if err := description.SetAudio(c.local(to)); err != nil {
		return err
	}
	m.Body = description.Bytes()
	return nil
}

// hasAudio reports whether m carries an SDP body with an audio stream
// that has a port: one that the TrGW has to carry.
func hasAudio(m *sip.Message) bool {
	if !hasSDP(m) {
		return false
	}
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return false
	}
	_, _, err = description.Audio()
	return err == nil
}

// hasSDP reports whether the body of m is a session description (RFC
// 3261 13.2.1): only then is it an offer or an answer.
func hasSDP(m *sip.Message) bool {
	mediaType, _, _ := strings.Cut(m.Get("Content-Type"), ";")
	return len(m.Body) > 0 && strings.EqualFold(strings.TrimSpace(mediaType), "application/sdp")
}
