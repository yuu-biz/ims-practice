package ibcf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"

	"github.com/yuu-biz/ims-practice/internal/agw"
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

// reserveMedia makes a TrGW context with a termination on each side:
// Reserve TrGW Connection Point (TS 29.238 Table 5.17.2.1.1). The
// address of each termination, as the SDP of its side shows it, is the
// TrGW's address in that network (Config), with the port that the TrGW
// chose.
//
// The termination of the home network sends where the SDP of that side
// says, and does not latch (H.248.37 (06/2008) 6.6.1). The party there
// changes within a call, as when the MMTel AS ends a customized
// alerting tone and gives the peer the SDP of the callee, and a latched
// address would keep the media on the MRFP.
func (b *IBCF) reserveMedia(ctx context.Context) (MediaContext, error) {
	c, err := b.trgw.Add(ctx, agw.TerminationRequest{ID: terminations[PeerSide]}, agw.TerminationRequest{ID: terminations[CoreSide], Latch: agw.NAPTOff})
	if err != nil {
		return MediaContext{}, err
	}
	core, peer := c.Terminations[terminations[CoreSide]], c.Terminations[terminations[PeerSide]]
	if core == nil || peer == nil {
		return MediaContext{}, fmt.Errorf("ibcf: TrGW context %s lacks a termination", c.ID)
	}
	return MediaContext{
		ID:   c.ID,
		Core: withAddress(core.Local, b.cfg.TrGWCoreAddress),
		Peer: withAddress(peer.Local, b.cfg.TrGWPeerAddress),
	}, nil
}

func withAddress(local netip.AddrPort, public netip.Addr) netip.AddrPort {
	if !public.IsValid() {
		return local
	}
	return netip.AddrPortFrom(public, local.Port())
}

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
	if err := b.trgw.Subtract(ctx, c.ID); err != nil && !errors.Is(err, agw.ErrNoContext) {
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
// as it is. sender is where the termination now sends, once the TrGW has
// taken the change; it is not valid when the TrGW was not asked.
//
// SIMPLIFIED: TS 24.229 6.7.1.4.1 and RFC 3264 6. Only the first audio
// stream with a port goes through the TrGW, as the P-CSCF does with
// the media relay.
// Another stream keeps its address, and so would show the address of
// its sender to the other network. The UEs of this project offer one
// audio stream.
func (b *IBCF) throughTrGW(m *sip.Message, c MediaContext, d interconnect.Direction) (sender netip.AddrPort, err error) {
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %w", errMalformedSDP, err)
	}
	sender, _, err = description.Audio()
	if err != nil {
		return netip.AddrPort{}, nil
	}
	from, to := facing(d)
	ctx, cancel := trgwContext()
	defer cancel()
	if err := b.trgw.Modify(ctx, c.ID, terminations[from], agw.TerminationRequest{Remote: sender}); err != nil {
		return netip.AddrPort{}, err
	}
	if err := description.SetAudio(c.local(to)); err != nil {
		return sender, err
	}
	m.Body = description.Bytes()
	return sender, nil
}

// errMalformedSDP is the error of throughTrGW for a body that it cannot
// read as SDP. The TrGW is not asked.
var errMalformedSDP = errors.New("ibcf: the SDP cannot be read")

// hasAudio reports whether m carries an SDP body with an audio stream
// that has a port: one that the TrGW has to carry.
func hasAudio(m *sip.Message) bool {
	if !sip.HasSessionDescription(m) {
		return false
	}
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return false
	}
	_, _, err = description.Audio()
	return err == nil
}
