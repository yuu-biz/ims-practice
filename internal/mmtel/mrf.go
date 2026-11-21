package mmtel

import (
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// This file holds the media that the AS has the MRF play to the caller
// (TS 24.628 4.7.2.9.0): announcements and customized alerting tones.
// The AS asks the MRFC for an announcement of RFC 4240 3 with an INVITE
// without SDP, so that the 200 brings the offer of an MRFP termination
// and the AS chooses when to send the ACK that starts the play (TS
// 24.229 5.8.2.2.1). Toward the caller the AS uses the gateway model of
// TS 24.628 Annex G: a reliable provisional response with an SDP answer
// built from that offer, and P-Early-Media to authorize it (RFC 5009).

// pcmu is the one codec of the MRFP.
var pcmu = []sdp.Codec{{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}}

// mrfTimeout bounds the wait for the 200 of the MRFC.
const mrfTimeout = 5 * time.Second

// mrfMedia is the dialog of the AS with the MRFC for one announcement.
type mrfMedia struct {
	dialog *sip.OutgoingCall
	// offer is the SDP offer of the MRFP termination in the 200 of the
	// MRFC: its address, PCMU, sendonly.
	offer sdp.Session
	log   *slog.Logger
}

// errNoMRFC is why the AS plays nothing: the configuration names no
// MRFC.
var errNoMRFC = errors.New("mmtel: no MRFC is configured")

// announcementURI is the Request-URI of RFC 4240 3 that asks the MRFC
// for the provisioned announcement name, played repeat times (0 for
// once, -1 for ever).
func (as *AS) announcementURI(name string, repeat int) (string, error) {
	if as.cfg.MRFC == "" {
		return "", errNoMRFC
	}
	u, err := sip.ParseURI(as.cfg.MRFC)
	if err != nil {
		return "", err
	}
	u.User = "annc"
	u.Params.Set("play", "/provisioned/"+name)
	switch {
	case repeat < 0:
		u.Params.Set("repeat", "forever")
	case repeat > 0:
		u.Params.Set("repeat", strconv.Itoa(repeat))
	}
	return u.String(), nil
}

// reserveMedia sends the MRFC the INVITE for an announcement and waits
// for its 200 with the offer of the MRFP. The announcement does not
// play until start sends the ACK.
func (c *call) reserveMedia(name string, repeat int) (*mrfMedia, error) {
	uri, err := c.as.announcementURI(name, repeat)
	if err != nil {
		return nil, err
	}
	ua := c.as.ua.Load()
	if ua == nil {
		return nil, errNotListening
	}
	inv := sip.Invitation{RequestURI: uri, From: c.as.uri()}
	if pcv := chargingVector(c.in.Request); pcv != "" {
		inv.Headers = append(inv.Headers, sip.HeaderField{Name: "P-Charging-Vector", Value: pcv})
	}
	out, err := ua.Invite(inv)
	if err != nil {
		return nil, err
	}
	log := c.log.With("mrfc_call_id", out.Request.Get("Call-ID"), "announcement", name)
	timeout := time.NewTimer(mrfTimeout)
	defer timeout.Stop()
	for {
		select {
		case resp, ok := <-out.Responses():
			switch {
			case !ok:
				return nil, fmt.Errorf("mmtel: no answer from the MRFC: %w", out.Err())
			case resp.StatusCode < 200:
				continue
			case resp.StatusCode >= 300:
				return nil, fmt.Errorf("mmtel: the MRFC answered %d %s", resp.StatusCode, resp.Get("Warning"))
			}
			offer, err := sdp.Parse(resp.Body)
			if err != nil || offer.AudioMedia() == nil {
				out.Bye()
				return nil, fmt.Errorf("mmtel: the 200 of the MRFC has no SDP offer of audio: %v", err)
			}
			log.Info("MRF media reserved", "mrfp", offer.Address(*offer.AudioMedia()).String())
			return &mrfMedia{dialog: out, offer: offer, log: log}, nil
		case <-timeout.C:
			out.Cancel()
			return nil, errors.New("mmtel: the MRFC did not answer in time")
		}
	}
}

// address is where the MRFP termination sends from and listens.
func (m *mrfMedia) address() netip.AddrPort { return m.offer.Address(*m.offer.AudioMedia()) }

// answerTo is the SDP answer to the caller's offer that points the
// caller at the MRFP: PCMU and sendonly (TS 24.628 G.1), with content
// as the content attribute of RFC 4796 when it is not empty. origin
// is the o= line of the AS toward the caller.
func (m *mrfMedia) answerTo(callerOffer sdp.Session, origin sdp.Origin, content string) (sdp.Session, error) {
	a, err := sdp.AudioAnswer(callerOffer, origin.SessionID, m.address(), pcmu)
	if err != nil {
		return sdp.Session{}, err
	}
	a.Origin = origin
	audio := a.AudioMedia()
	audio.SetDirection("sendonly")
	if content != "" {
		audio.Attributes = append(audio.Attributes, sdp.Attribute{Name: "content", Value: content})
	}
	return a, nil
}

// start acknowledges the 200 of the MRFC with an answer that points the
// MRFP at to, the caller's media: the MRFP starts to play at the ACK
// (TS 24.229 5.8.2.2.1).
func (m *mrfMedia) start(to netip.AddrPort) error {
	answer, err := sdp.AudioAnswer(m.offer, strconv.FormatUint(rand.Uint64N(1<<62), 10), to, pcmu)
	if err != nil {
		return err
	}
	m.log.Info("MRF media starts", "to", to.String())
	return m.dialog.Ack(sdpBody(answer))
}

// stop ends the dialog with the MRFC, which stops the play (TS 24.229
// 5.8.3.1.1). The MRFC may have ended it already.
func (m *mrfMedia) stop() {
	if err := m.dialog.Bye(); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		m.log.Info("BYE to the MRFC failed", "error", err.Error())
	}
}

func sdpBody(s sdp.Session) sip.Body {
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}
}

// newOrigin is a fresh o= line of the AS toward the caller.
func newOrigin(addr netip.Addr) sdp.Origin {
	return sdp.Origin{Username: "-", SessionID: strconv.FormatUint(rand.Uint64N(1<<62), 10), Version: 1, Address: addr}
}

// hasPCMU tells whether an SDP offer has an audio stream that the MRFP
// can send to: RTP/AVP with PCMU.
func hasPCMU(offer sdp.Session) bool {
	m := offer.AudioMedia()
	if m == nil || m.Proto != "RTP/AVP" || m.Port == 0 {
		return false
	}
	for _, c := range m.Codecs() {
		if c.PayloadType == sdp.PCMU || strings.EqualFold(c.Name, sdp.NamePCMU) && c.ClockRate == 8000 {
			return true
		}
	}
	return false
}
