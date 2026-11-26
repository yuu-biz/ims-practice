package mrfc

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/netip"
	"strconv"
	"strings"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// pcmu is the one codec of the MRFP.
//
// SIMPLIFIED: TS 23.333 5.12. This MRFP plays and mixes G.711 PCMU
// only and transcodes nothing. 5.12.1 asks an MRFP to transcode between
// the terminations of a context and to support AMR at least, and lets
// it support the other codecs of TS 26.114, AMR-WB among them. Mixing a
// conference sums decoded speech, and the product decodes no AMR; a
// decoder would cost much and change little of the signalling that the
// project shows. The MRFC answers an offer without PCMU with 488 (Not
// Acceptable Here) and Warning 305 (Incompatible media format): a call
// whose codec is AMR-WB reaches an announcement or a conference only
// after the AS has offered PCMU to the MRFC, which the UE simulator's
// VoLTE offer still carries, and no MRFP transcodes between the two.
var pcmu = []sdp.Codec{{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}}

var errNoPCMU = errors.New("mrfc: the offer has no audio stream of PCMU")

// leg is the media of one dialog: one termination of a context in an
// MRFP, and the SDP that the MRFC writes for it.
type leg struct {
	mrfp    Processor
	context string
	id      agw.TerminationID
	local   netip.AddrPort
	log     *slog.Logger

	mu sync.Mutex
	// sessionID and version are those of the o= line, which goes up with
	// each new description (RFC 3264 8).
	sessionID string
	version   uint64
}

func newLeg(p Processor, contextID string, t agw.Termination, log *slog.Logger) *leg {
	return &leg{
		mrfp: p, context: contextID, id: t.ID, local: t.Local, sessionID: strconv.FormatUint(rand.Uint64N(1<<62), 10),
		log: log.With("mrfp", p.Name, "context", contextID, "termination", string(t.ID)),
	}
}

func (l *leg) nextVersion() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.version++
	return l.version
}

// offered is the audio of an SDP offer that the MRFP can take: where it
// goes, and the direction the offer asks for.
type offered struct {
	session   sdp.Session
	remote    netip.AddrPort
	direction string
}

// readOffer reads the SDP offer in body (RFC 3264 5).
func readOffer(body []byte) (offered, error) {
	s, err := sdp.Parse(body)
	if err != nil {
		return offered{}, err
	}
	m := s.AudioMedia()
	if m == nil || m.Proto != "RTP/AVP" {
		return offered{}, errNoPCMU
	}
	for _, c := range m.Codecs() {
		if c.PayloadType == sdp.PCMU || strings.EqualFold(c.Name, sdp.NamePCMU) && c.ClockRate == 8000 {
			return offered{session: s, remote: s.Address(*m), direction: s.Direction(*m)}, nil
		}
	}
	return offered{}, errNoPCMU
}

// answer is the SDP answer of the termination to o, with direction
// (RFC 3264 6.1).
func (l *leg) answer(o offered, direction string) (sip.Body, error) {
	a, err := sdp.AudioAnswer(o.session, l.sessionID, l.local, pcmu)
	if err != nil {
		return sip.Body{}, err
	}
	a.Origin.Version = l.nextVersion()
	a.AudioMedia().SetDirection(direction)
	return sdpBody(a), nil
}

// offer is an SDP offer of the termination, for an INVITE that came
// without one (RFC 3264 5).
func (l *leg) offer(direction string) (sip.Body, error) {
	o, err := sdp.AudioOffer(l.sessionID, l.local, pcmu)
	if err != nil {
		return sip.Body{}, err
	}
	o.Origin.Version = l.nextVersion()
	o.AudioMedia().SetDirection(direction)
	return sdpBody(o), nil
}

func sdpBody(s sdp.Session) sip.Body {
	return sip.Body{Type: "application/sdp", Content: s.Bytes()}
}

// mp bounds one request to an MRFP, so that one that stalls fails the
// request instead of holding up the dialog for good.
func mp() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), agw.RequestTimeout)
}

// modify changes the termination of the leg.
func (l *leg) modify(change agw.TerminationRequest) error {
	ctx, cancel := mp()
	defer cancel()
	return l.mrfp.Control.Modify(ctx, l.context, l.id, change)
}

// takeAnswer points the termination at the SDP answer in body, which
// came in the ACK after an INVITE without an offer: the mode of the
// termination is the reverse of the direction that the answer gives
// (RFC 3264 6.1).
func (l *leg) takeAnswer(body []byte) error {
	s, err := sdp.Parse(body)
	if err != nil {
		return err
	}
	m := s.AudioMedia()
	if m == nil {
		return errors.New("mrfc: the answer has no audio stream")
	}
	return l.modify(agw.TerminationRequest{Remote: s.Address(*m), Mode: agw.Mode(sdp.AnswerDirection(s.Direction(*m)))})
}

// release subtracts the termination, and tells whether that deleted the
// context. Only the goroutine that added the termination calls it: when
// its dialog ends, or when attach gives up a context that it created,
// because the Store failed or another instance created the conference
// first.
//
// SIMPLIFIED: TS 23.333 6.1.5.1 and TS 29.333 5.17.3.5. After an MRFC
// recovers, the terminations that the recovery affected are released,
// and the MRFC tells the MRFP that it has restarted with a ServiceChange
// on ROOT. Here nothing releases the terminations of an instance that
// is killed, or whose process exits before the goroutines of its
// dialogs have subtracted them. A node that is told to stop closes the
// UA, which ends each dialog without a BYE, and each goroutine then
// subtracts its termination, but the node does not wait for those
// goroutines (sip.UA.Close leaves them to the user). An instance that
// starts again under the same name sends no ServiceChange either. The
// terminations that are left stay at the MRFP, each with its ports
// open, and so does the clock of a context that mixes a conference. The
// item of such a conference stays in the Store, since the context never
// empties, and a party that joins later is mixed with the terminations
// that are left. The MRFC keeps no record of its terminations outside
// its process, and neither the operations of MRFP nor the HTTP control
// API through which the node drives its MRFPs carry a restart
// indication.
func (l *leg) release() bool {
	ctx, cancel := mp()
	defer cancel()
	t, deleted, err := l.mrfp.Control.SubtractTermination(ctx, l.context, l.id)
	if err != nil {
		l.log.Warn("termination not subtracted", "error", err.Error())
		return false
	}
	l.log.Info("termination subtracted", "packets", t.Packets, "packets_sent", t.PacketsSent, "context_deleted", deleted)
	return deleted
}

// handle answers the requests within the dialog of the leg. An offer in
// a re-INVITE or an UPDATE changes where and how the termination sends,
// as when a party of a conference holds it or comes back (TS 24.229
// 5.8.4.1.1.1); direction gives the direction of the answer to the
// direction offered.
func (m *MRFC) handle(l *leg, direction func(offered string) string) func(*sip.InDialogRequest) {
	return func(r *sip.InDialogRequest) {
		method := r.Request.Method
		switch {
		case r.Offer && (method == sip.INVITE || method == sip.UPDATE):
			o, err := readOffer(r.Request.Body)
			if err != nil {
				r.Reject(488, m.warning(305, "Incompatible media format"))
				return
			}
			dir := direction(o.direction)
			if err := l.modify(agw.TerminationRequest{Remote: o.remote, Mode: agw.Mode(dir)}); err != nil {
				l.log.Warn("MRFP refused the new offer", "error", err.Error())
				r.Reject(500)
				return
			}
			body, err := l.answer(o, dir)
			if err != nil {
				r.Reject(488, m.warning(305, "Incompatible media format"))
				return
			}
			r.Accept(body)
		case method == sip.INVITE:
			// A re-INVITE without an offer gets one in the 200, and the
			// answer comes in the ACK (RFC 3261 14.2).
			body, err := l.offer(direction("sendrecv"))
			if err != nil {
				r.Reject(500)
				return
			}
			ack, err := r.Accept(body)
			if err == nil && len(ack.Body) > 0 {
				if err := l.takeAnswer(ack.Body); err != nil {
					l.log.Warn("answer in the ACK not taken", "error", err.Error())
				}
			}
		case method == sip.UPDATE:
			// A session refresh (RFC 4028 9).
			r.Accept(sip.Body{})
		default:
			r.Reject(405)
		}
	}
}

// bye ends the dialog of c from the MRFC's side.
func bye(c *sip.IncomingCall, log *slog.Logger) {
	if err := c.Bye(); err != nil {
		log.Warn("BYE failed", "error", err.Error())
	}
}

// announcementDirection is how an MRFP termination that only plays
// answers an offered direction (RFC 3264 6.1): it sends to a party that
// receives, and does neither to one that does not.
func announcementDirection(offered string) string {
	if offered == "sendrecv" || offered == "recvonly" {
		return "sendonly"
	}
	return "inactive"
}
