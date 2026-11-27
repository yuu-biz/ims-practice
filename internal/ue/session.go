package ue

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/precondition"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallConfig is how the UE makes and takes calls.
type CallConfig struct {
	// Codecs are the payload formats the UE offers and accepts, the most
	// preferred first; nil means PCMU with its telephone-event, as a
	// softphone offers. VoLTECodecNames are those of IR.92.
	Codecs []sdp.Codec
	// NoPreconditions turns the precondition mechanism off, as the
	// precondition disabling policy of TS 24.229 5.1.5A can. Without such
	// a policy the mechanism is enabled.
	NoPreconditions bool
	// Resources tells whether the access network has reserved the
	// resources for the media of calls. Nil means an access network that
	// needs no reservation: the UE always has them.
	Resources *Resources
}

// VoLTECodecNames are the speech codecs of the UE's offer under IR.92,
// the most preferred first: AMR-WB and AMR (IR.92 2.4.3.2, 3.2.1), and
// PCMU for softphones and for interconnects that need G.711.
var VoLTECodecNames = []string{sdp.NameAMRWB, sdp.NameAMR, sdp.NamePCMU}

// CodecsNamed picks the payload formats of the named speech codecs from
// those of the VoLTE offer (sdp.VoLTECodecs), in the order of names, and
// adds a telephone-event for each clock rate they use (TS 24.229 6.1.1,
// TS 26.114 G.3).
func CodecsNamed(names []string) ([]sdp.Codec, error) {
	all := sdp.VoLTECodecs()
	var codecs []sdp.Codec
	rates := map[int]bool{}
	for _, name := range names {
		found := false
		for _, c := range all {
			if strings.EqualFold(c.Name, name) && c.Name != sdp.NameTelephoneEvent {
				if slices.ContainsFunc(codecs, func(d sdp.Codec) bool { return d.PayloadType == c.PayloadType }) {
					return nil, fmt.Errorf("codec %s is named twice", name)
				}
				codecs = append(codecs, c)
				rates[c.ClockRate] = true
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("codec %q is none of %s", name, strings.Join(VoLTECodecNames, ", "))
		}
	}
	if len(codecs) == 0 {
		return nil, errors.New("no codec")
	}
	for _, c := range all {
		if c.Name == sdp.NameTelephoneEvent && rates[c.ClockRate] {
			codecs = append(codecs, c)
		}
	}
	return codecs, nil
}

func (c CallConfig) codecs() []sdp.Codec {
	if c.Codecs != nil {
		return c.Codecs
	}
	codecs, _ := CodecsNamed([]string{sdp.NamePCMU})
	return codecs
}

// UAConfig is cfg with what the UA of the UE needs for calls: it takes
// the precondition mechanism, whose SDP the UE handles (RFC 3312 11, TS
// 24.229 5.1.3.1, 5.1.4.1).
//
// It also sets the feature parameters of the Contact of the UE's
// dialogs (DialogContactParams), unless cfg has some, the session timer
// of IR.92 2.2.8 (SessionInterval), unless cfg has one, and the header
// fields of what the UA sends by itself (OwnHeaders), without
// P-Access-Network-Info unless cfg has its own.
//
// SIMPLIFIED: TS 24.229 5.1.3.1 b) and 5.1.4A.1. The UA lists
// precondition in the Supported of every request within a dialog, also
// when the peer did not use the mechanism, where the UE should leave it
// out. The UA has one Supported for all its calls; a peer that does not
// use preconditions ignores the option tag in Supported (5.1.4A.2).
func UAConfig(cfg sip.UAConfig, calls CallConfig) sip.UAConfig {
	if !calls.NoPreconditions {
		cfg.Supported = append(slices.Clone(cfg.Supported), sip.OptionPrecondition)
	}
	// RFC 3891 6.2: a UA that takes Replaces says so (conference.go).
	cfg.Supported = append(slices.Clone(cfg.Supported), sip.OptionReplaces)
	if cfg.ContactParams == nil {
		cfg.ContactParams = DialogContactParams
	}
	if cfg.SessionTimer == (sip.SessionTimerPolicy{}) {
		cfg.SessionTimer = sip.SessionTimerPolicy{Interval: SessionInterval}
	}
	if cfg.OwnHeaders == nil {
		cfg.OwnHeaders = OwnHeaders("")
	}
	return cfg
}

// The direction attributes of a media description (RFC 8866 6.7).
const (
	dirSendrecv = "sendrecv"
	dirSendonly = "sendonly"
	dirRecvonly = "recvonly"
)

// Session is the session of one call: the offer and answer and the
// media they describe (RFC 3264): what this UE sent last, what the peer sent last,
// the precondition table (RFC 3312), and which way the media may go.
type Session struct {
	log   *slog.Logger
	media *Media
	// addr is where this UE takes the media, as its SDP says.
	addr   netip.AddrPort
	codecs []sdp.Codec
	id     string // the session id of o=
	// kicked tells the goroutine of the call that a request of the peer
	// changed the session.
	kicked  chan struct{}
	changed func()
	// requestHeaders and responseHeaders go into every request and
	// response of the UE in the call (UE.requestHeaders).
	requestHeaders, responseHeaders []sip.HeaderField

	mu sync.Mutex
	// sent is the last SDP this UE sent, with version 0: the version
	// goes up only when the next one differs (RFC 3264 8).
	sent    []byte
	version uint64
	offered sdp.Session // this UE's last offer
	stream  *Stream
	// localDir and remoteDir are the directions of the audio stream in
	// the last SDP of this UE and of the peer.
	localDir, remoteDir string
	precond             *precondition.Table // nil when not used
	reserved            bool
	// reservationNeeded is set when the access network of the UE
	// reserves the resources for media (CallConfig.Resources).
	reservationNeeded bool
	answered          bool // the 2xx to the INVITE has come or gone
	// For a caller, per early dialog by its To tag: the early media
	// authorization that the last P-Early-Media gave for the audio
	// stream (RFC 5009 8), and whether a 180 came (IR.92 2.2.7).
	earlyMedia map[string]string
	alerting   map[string]bool
	// answerTag is the To tag of the dialog whose answer the session
	// holds.
	answerTag string
	held      bool // this UE has put the call on hold
	// rang is how the call rang before the answer (Ringback), kept at
	// the answer.
	rang Ringback
	// caller is set for the session of the UE that sent the INVITE.
	caller bool
	// dialog is the dialog of the call, for the re-INVITEs of hold, and
	// bye ends the call.
	dialog sessionDialog
	bye    func(headers ...sip.HeaderField) error
	// other answers a request within the dialog that the session does
	// not take itself, such as a NOTIFY of a subscription that shares
	// the dialog (conference.go); it reports false to leave it to the
	// session.
	other func(*sip.InDialogRequest) bool
	// lost is set once the UE released the call for the loss of its
	// media bearer; release is the timer of that.
	lost    bool
	release *time.Timer
	// receivedBefore counts the RTP packets that came before the answer
	// (noMedia).
	receivedBefore int
}

// bearerLossGrace is how long a UE that lost the bearer of its media
// waits for the network to end the call before it ends the call itself.
// IR.92 2.4.2.3 has the network end it (TS 24.229 5.2.8.1.2) and the UE
// handle the loss as well; the wait keeps the two BYEs from crossing.
var bearerLossGrace = 2 * time.Second

// ErrMediaBearerLost is how a call ends that the UE released because
// its access network took the bearer of the media away.
var ErrMediaBearerLost = errors.New("the bearer of the media was lost")

func (u *UE) newSession(media *Media, codecs []sdp.Codec, changed func()) *Session {
	if changed == nil {
		changed = func() {}
	}
	s := &Session{log: u.log, media: media, addr: netip.AddrPortFrom(u.contact.addr.Addr(), media.Port()), codecs: codecs,
		changed: changed, requestHeaders: u.requestHeaders(), responseHeaders: u.responseHeaders(),
		id: strconv.FormatInt(time.Now().UnixNano(), 10), kicked: make(chan struct{}, 1)}
	media.OnDigit(func(d rune) {
		s.log.Info("dtmf received", "digit", string(d))
		s.changed()
	})
	return s
}

// sessionDialog is the dialog of a call as its session uses it: an
// OutgoingCall or an IncomingCall.
type sessionDialog interface {
	ReInviteWith(re sip.ReInvitation) (*sip.Message, error)
	Dialog() (sip.Dialog, bool)
}

// setDialog gives the session the dialog of its call.
func (s *Session) setDialog(d sessionDialog, bye func(headers ...sip.HeaderField) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialog, s.bye = d, bye
}

// Dialog is the state of the dialog of the call, and false while it has
// none: what a Replaces names it by (RFC 3891 3).
func (s *Session) Dialog() (sip.Dialog, bool) {
	s.mu.Lock()
	d := s.dialog
	s.mu.Unlock()
	if d == nil {
		return sip.Dialog{}, false
	}
	return d.Dialog()
}

// takeOther has f answer the requests within the dialog that the session
// does not take itself; f reports false for one it leaves to the
// session, which then refuses it.
func (s *Session) takeOther(f func(*sip.InDialogRequest) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.other = f
}

// isHeld tells that this UE has put the call on hold.
func (s *Session) isHeld() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

// ErrNotAnswered means the call has no answer yet, so its session
// cannot change.
var ErrNotAnswered = errors.New("the call is not answered")

// Hold puts the call on hold: a re-INVITE whose offer makes the media
// sendonly (RFC 3264 8.4), after which this UE sends no media (IR.92
// 2.3.10). The peer answers recvonly. The MMTel AS builds the HOLD
// service of TS 24.610 on it.
func (s *Session) Hold() error { return s.hold(true) }

// Resume takes the call back from hold with a re-INVITE that offers
// sendrecv again.
func (s *Session) Resume() error { return s.hold(false) }

func (s *Session) hold(held bool) error {
	s.mu.Lock()
	if !s.answered || s.dialog == nil {
		s.mu.Unlock()
		return ErrNotAnswered
	}
	if s.held == held {
		s.mu.Unlock()
		return nil
	}
	s.held = held
	before, dialog, preconditions := s.state(), s.dialog, s.precond != nil
	s.mu.Unlock()
	defer s.changed()
	for attempt := 0; ; attempt++ {
		resp, early, err := s.offerAnew(dialog)
		if resp != nil && resp.StatusCode == 491 && !early && attempt == 0 {
			// RFC 3261 14.1: the re-INVITE crossed one of the peer.
			time.Sleep(sip.GlareDelay(s.caller))
			continue
		}
		if err != nil {
			// RFC 3264 8: a refused offer leaves the session as it was.
			s.mu.Lock()
			s.held = !held
			s.restore(before)
			s.updateFlow()
			s.mu.Unlock()
			if early && resp != nil && resp.StatusCode >= 300 && !preconditions {
				s.resynchronize(dialog)
			}
			return err
		}
		s.log.Info("call hold", "held", held)
		return nil
	}
}

// offerAnew sends the session as it now stands in a re-INVITE within
// dialog, and takes the answer. The first reliable provisional response
// with SDP carries it, and the 2xx then carries none (RFC 3261 13.2.1,
// RFC 3262 5). The UE takes that answer as soon as it comes, as the SIP
// stack does, so that an offer of the peer after it, in an UPDATE,
// changes the session after it too. Without such a response the 2xx
// carries the answer. offerAnew returns the final response, whether a
// provisional response carried the answer (early), and why the offer
// failed: a final response other than 2xx, or an answer that the UE
// cannot take.
func (s *Session) offerAnew(dialog sessionDialog) (resp *sip.Message, early bool, err error) {
	body, err := s.reoffer()
	if err != nil {
		return nil, false, err
	}
	var earlyErr error
	resp, err = dialog.ReInviteWith(sip.ReInvitation{Body: body, Headers: s.requestHeaders,
		OnProvisional: func(p *sip.Message) {
			if !early && p.Requires(sip.Option100rel) && sip.HasSessionDescription(p) {
				early, earlyErr = true, s.receiveAnswer(p)
			}
		}})
	switch {
	case err != nil:
	case resp.StatusCode >= 300:
		err = fmt.Errorf("re-INVITE answered %d %s", resp.StatusCode, resp.Reason)
	case early:
		err = earlyErr
	default:
		err = s.receiveAnswer(resp)
	}
	return resp, early, err
}

// sessionState is what an offer of this UE and its answer change in the
// session.
type sessionState struct {
	localDir, remoteDir string
	offered             sdp.Session
	stream              *Stream
}

// state is the session as the last offer and answer left it. s.mu is
// held.
func (s *Session) state() sessionState {
	return sessionState{localDir: s.localDir, remoteDir: s.remoteDir, offered: s.offered, stream: s.stream}
}

// restore puts the session back to st, the state before an offer that
// failed (RFC 3264 8), with the media of the peer that the answer to
// that offer may have moved. The precondition table keeps what the
// answer said of the resources of the peer: the failure of the offer
// does not undo a reservation. s.mu is held.
func (s *Session) restore(st sessionState) {
	if st.stream != nil && st.stream != s.stream {
		if err := s.media.SetStream(*st.stream); err != nil {
			s.log.Warn("the media of the session before the offer not set again", "error", err.Error())
		}
	}
	s.localDir, s.remoteDir, s.offered, s.stream = st.localDir, st.remoteDir, st.offered, st.stream
}

// resynchronize offers the session as it was before a re-INVITE that
// failed after a reliable provisional response had answered it. The
// offer and answer of that response were done, and without
// preconditions that is a change that the peer has carried out (RFC
// 6141 3.3), although its error says otherwise. So the UE offers the
// session as close to that before the re-INVITE as it can, and both
// sides have the same view of it again (RFC 6141 3.4). It does so once:
// a new offer that fails too leaves the session as the UE has it.
//
// SIMPLIFIED: RFC 6141 3.3. With preconditions, the offer and answer
// of the provisional response only make the new parameters ready, and
// the peer that then fails the re-INVITE has used none of them, unless
// it has sent media with them. The UE does not look at the media to
// tell, and takes the error as saying that nothing was carried out.
func (s *Session) resynchronize(dialog sessionDialog) {
	if _, _, err := s.offerAnew(dialog); err != nil {
		s.log.Warn("the offer that brings the peer back to the session before the failed re-INVITE failed too", "error", err.Error())
		return
	}
	s.log.Info("the peer is back on the session before the failed re-INVITE")
}

// SendDTMF sends digits as telephone events in the audio stream (TS
// 26.114 Annex G, IR.92 3.3).
func (s *Session) SendDTMF(digits string) error {
	s.mu.Lock()
	answered := s.answered
	s.mu.Unlock()
	if !answered {
		return ErrNotAnswered
	}
	if err := s.media.SendDTMF(digits); err != nil {
		return err
	}
	s.log.Info("dtmf sent", "digits", digits)
	return nil
}

// usePreconditions starts the precondition table of the call: this UE
// reserves in its own access network only, so the status type is
// segmented, mandatory for its own segment and optional for the peer's
// (TS 24.229 6.1.2, 6.1.3).
func (s *Session) usePreconditions(role precondition.Role, reserved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.precond = precondition.NewSegmented(role, precondition.Mandatory, precondition.Optional)
	s.reserved = reserved
	s.precond.SetReserved(precondition.SendRecv, reserved)
}

// kick wakes the goroutine of the call.
func (s *Session) kick() {
	select {
	case s.kicked <- struct{}{}:
	default:
	}
}

// body writes d as the next SDP of this UE. m.mu is held.
func (s *Session) body(d sdp.Session) sip.Body {
	d.Origin.SessionID, d.Origin.Version = s.id, 0
	if b := d.Bytes(); !bytes.Equal(b, s.sent) {
		s.sent = b
		s.version++
	}
	d.Origin.Version = s.version
	if m := d.AudioMedia(); m != nil {
		s.localDir = d.Direction(*m)
	}
	return sip.Body{Type: "application/sdp", Content: d.Bytes()}
}

// initialOffer is the offer of the INVITE: every codec of the UE, sendrecv
// (IR.92 2.2.4), and the local preconditions as they are (TS 24.229
// 6.1.2).
func (s *Session) initialOffer() (sip.Body, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := sdp.AudioOffer(s.id, s.addr, s.codecs)
	if err != nil {
		return sip.Body{}, err
	}
	if s.precond != nil {
		d.Media[0].SetPreconditions(s.precond.Offer())
	}
	s.offered = d
	return s.body(d), nil
}

// reoffer is an offer within the session, in an UPDATE or a re-INVITE:
// only the codec the answer chose, with its telephone-event (IR.92
// 2.4.1, TS 24.229 6.1.2), on hold or not, and the preconditions as
// they are now.
func (s *Session) reoffer() (sip.Body, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stream == nil {
		return sip.Body{}, errors.New("no session to offer anew")
	}
	codecs := []sdp.Codec{s.stream.Codec}
	if s.stream.Events != nil {
		codecs = append(codecs, *s.stream.Events)
	}
	d, err := sdp.AudioOffer(s.id, s.addr, codecs)
	if err != nil {
		return sip.Body{}, err
	}
	if s.held {
		// RFC 3264 8.4.
		d.Media[0].SetDirection(sdp.HoldDirection(dirSendrecv))
	}
	if s.precond != nil {
		d.Media[0].SetPreconditions(s.precond.Offer())
	}
	s.offered = d
	return s.body(d), nil
}

// receiveAnswer reads the answer to the last offer of this UE. The first
// answer of the INVITE tells whether the callee uses preconditions: only
// when it requires them (TS 24.229 5.1.3.1).
func (s *Session) receiveAnswer(m *sip.Message) error {
	answer, err := sdp.Parse(m.Body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.StatusCode != 0 {
		s.answerTag = toTag(m)
	}
	if s.stream == nil && s.precond != nil && !m.Requires(sip.OptionPrecondition) {
		s.log.Info("the callee does not use preconditions")
		s.precond = nil
	}
	if err := s.negotiated(s.offered, answer, answer); err != nil {
		return err
	}
	if s.precond != nil {
		attrs, err := answer.AudioMedia().Preconditions()
		if err == nil {
			err = s.precond.ApplyAnswer(attrs)
		}
		if err != nil {
			return err
		}
	}
	s.updateFlow()
	return nil
}

// errNotAcceptable is an offer this UE cannot take: 488 in SIP, or 580
// for a precondition it does not know (RFC 3312 9).
type errNotAcceptable struct {
	code int
	err  error
}

func (e *errNotAcceptable) Error() string { return e.err.Error() }
func (e *errNotAcceptable) Unwrap() error { return e.err }

// answerTo answers an offer of the peer (RFC 3264 6), with one codec
// (TS 24.229 6.1.3). A UE that holds the call keeps its media one way
// (RFC 3264 8.4).
//
// The first offer of the call tells whether the caller uses
// preconditions: an offer without them gets an answer without them, and
// the call goes on without the mechanism (TS 24.229 6.1.3 NOTE 1).
func (s *Session) answerTo(offerBody []byte, initial bool) (sip.Body, error) {
	offer, err := sdp.Parse(offerBody)
	if err != nil {
		return sip.Body{}, &errNotAcceptable{488, err}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := sdp.AudioAnswer(offer, s.id, s.addr, s.codecs)
	if err != nil {
		return sip.Body{}, &errNotAcceptable{488, err}
	}
	m := d.AudioMedia()
	if s.held {
		m.SetDirection(sdp.HoldDirection(d.Direction(*m)))
	}
	attrs, err := offer.Media[slices.IndexFunc(d.Media, func(m sdp.Media) bool { return m.Port != 0 })].Preconditions()
	if err != nil {
		return sip.Body{}, &errNotAcceptable{488, err}
	}
	if s.precond != nil && initial && attrs.Empty() {
		s.log.Info("the caller does not use preconditions")
		s.precond = nil
	}
	if s.precond != nil {
		attrs, err = s.precond.Answer(attrs)
		if errors.Is(err, precondition.ErrUnknownPrecondition) {
			return sip.Body{}, &errNotAcceptable{580, err}
		}
		if err != nil {
			return sip.Body{}, &errNotAcceptable{488, err}
		}
		m.SetPreconditions(attrs)
	}
	if err := s.negotiated(d, offer, offer); err != nil {
		return sip.Body{}, &errNotAcceptable{488, err}
	}
	body := s.body(d)
	s.updateFlow()
	return body, nil
}

// negotiated takes the stream that local and remote, an offer and its
// answer, settled. peer is the SDP of the peer, whose address and
// payload types this UE sends with. s.mu is held.
//
// SIMPLIFIED: RFC 3264 6.1. An answer may number its payload types its
// own way; the UE sends and receives with the numbers of the peer's
// SDP. Every UA in this project answers with the numbers of the offer.
func (s *Session) negotiated(local, remote, peer sdp.Session) error {
	i := slices.IndexFunc(local.Media, func(m sdp.Media) bool { return m.Type == "audio" && m.Port != 0 })
	if i < 0 || i >= len(remote.Media) || remote.Media[i].Port == 0 {
		return errors.New("no audio stream on both sides")
	}
	pm := peer.Media[i]
	stream := Stream{Remote: peer.Address(pm)}
	stream.RS, stream.RR = pm.RTCPBandwidth()
	speech := false
	for _, c := range pm.Codecs() {
		if !slices.ContainsFunc(s.codecs, func(o sdp.Codec) bool { return sameFormat(o, c) }) {
			continue
		}
		if !c.Is(sdp.NameTelephoneEvent) && !speech {
			stream.Codec, speech = c, true
		}
	}
	if !speech {
		return errors.New("no speech codec of this UE in the session")
	}
	for _, c := range pm.Codecs() {
		if c.Is(sdp.NameTelephoneEvent) && c.ClockRate == stream.Codec.ClockRate {
			stream.Events = &c
			break
		}
	}
	s.remoteDir = remote.Direction(remote.Media[i])
	if err := s.media.SetStream(stream); err != nil {
		return err
	}
	if s.stream == nil || s.stream.Codec != stream.Codec {
		s.log.Info("media negotiated", "codec", codecInfo(stream).String(), "media_to", stream.Remote.String(),
			"rtcp_rs", stream.RS, "rtcp_rr", stream.RR)
	}
	s.stream = &stream
	return nil
}

// toTag is the tag of the To header field of m.
func toTag(m *sip.Message) string {
	na, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return ""
	}
	tag, _ := na.Params.Get("tag")
	return tag
}

// sameFormat tells that c, from an SDP of the peer, is the payload
// format o that this UE has: the same encoding, clock rate and AMR
// payload format.
func sameFormat(o, c sdp.Codec) bool {
	if !strings.EqualFold(o.Name, c.Name) || o.ClockRate != c.ClockRate {
		return false
	}
	of, oerr := o.AMRFormat()
	cf, cerr := c.AMRFormat()
	return (oerr != nil) == (cerr != nil) && of == cf
}

// setReserved records what the access network says about the resources
// of this UE.
//
// A call that loses its resources once it is answered has no media any
// longer: the UE sends none (IR.92 2.4.3.1), and ends the call with a
// BYE that says why, unless the network does first or the resources
// come back (TS 24.229 6.1.1 item 2, IR.92 2.4.2.3).
//
// SIMPLIFIED: TS 24.229 6.1.1 items 3 and 4. A loss before the answer
// only changes the precondition table and stops the media; the call
// waits for the network to end it (5.2.8.1.1).
func (s *Session) setReserved(reserved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reserved == reserved {
		return
	}
	s.reserved = reserved
	if s.precond != nil {
		s.precond.SetReserved(precondition.SendRecv, reserved)
	}
	s.updateFlow()
	switch {
	case reserved && s.release != nil:
		s.release.Stop()
		s.release = nil
	case !reserved && s.answered && s.bye != nil && s.release == nil:
		s.log.Warn("the bearer of the media is lost")
		s.release = time.AfterFunc(bearerLossGrace, s.releaseForBearerLoss)
	}
}

// releaseForBearerLoss is TS 24.229 6.1.1 item 2: the call ends with a
// BYE whose Reason is the loss of the media bearer (7.2A.18.11.2).
func (s *Session) releaseForBearerLoss() {
	s.mu.Lock()
	if s.reserved || s.release == nil {
		s.mu.Unlock()
		return
	}
	s.release, s.lost = nil, true
	bye := s.bye
	s.mu.Unlock()
	s.log.Info("ending the call for the loss of the media bearer")
	if err := bye(with(s.requestHeaders, reasonMediaBearerLoss.Header())...); err != nil && !errors.Is(err, sip.ErrCallEnded) {
		s.log.Warn("BYE failed", "error", err.Error())
	}
}

// ended tells why the call is over: ErrMediaBearerLost when the UE ended
// it for that, and nil otherwise. It stops a release that has not
// happened yet.
func (s *Session) ended() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.release != nil {
		s.release.Stop()
		s.release = nil
	}
	if s.lost {
		return ErrMediaBearerLost
	}
	return nil
}

// noMedia is ErrNoMedia for an answered call that ends with both SDPs
// saying the peer sends to this UE (RFC 3264 6.1), when no RTP came
// after the answer, and nil otherwise. The peer's SDP has to say
// sendrecv: one that says sendonly may have put the call on hold, and a
// UE that holds a call sends nothing (IR.92 2.3.10). A call that ends
// on hold is no failure either, as the hold may have come before the
// first packet could. Early media before the answer does not count.
func (s *Session) noMedia() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	receives := s.localDir == dirSendrecv || s.localDir == dirRecvonly
	if s.answered && receives && s.remoteDir == dirSendrecv && s.media.Stats().PacketsReceived == s.receivedBefore {
		return ErrNoMedia
	}
	return nil
}

// needsOffer is RFC 3312 7 and TS 24.229 5.1.3.1: an offer has to tell
// the peer of a change in the reservation.
func (s *Session) needsOffer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream != nil && s.precond != nil && s.precond.NeedsOffer()
}

// settled tells that the offer and answer have settled the stream.
func (s *Session) settled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stream != nil
}

// readyToAlert is when the callee may alert its user: every mandatory
// precondition met (RFC 3312 6), or without preconditions its own
// resources reserved (IR.92 2.4.1).
func (s *Session) readyToAlert() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.precond != nil {
		return s.precond.Met()
	}
	return s.reserved
}

// preconditionHeaders are the header fields that a request or response
// with SDP gets while the precondition mechanism is in use: Require
// during the session setup (TS 24.229 5.1.3.1 a, 5.1.4.1 a), and none
// in a request that modifies the session later, where Supported says
// enough (5.1.4A.1).
func (s *Session) preconditionHeaders(response bool) []sip.HeaderField {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.precond == nil || (!response && s.answered) {
		return nil
	}
	return []sip.HeaderField{{Name: "Require", Value: sip.OptionPrecondition}}
}

// setAnswered records the 2xx to the INVITE: from now on the media
// flows (RFC 5009 8: the 200 authorizes it).
func (s *Session) setAnswered() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.caller {
		s.rang = s.ringback()
		s.log.Info("ringback before the answer", "ringback", string(s.rang))
	}
	s.answered = true
	s.receivedBefore = s.media.Stats().PacketsReceived
	s.updateFlow()
}

// updateFlow works out which way RTP may go now. s.mu is held.
//
//   - RFC 3264 6.1: this UE sends when both directions allow it, and
//     receives when its own does.
//   - IR.92 2.3.10: a UE that holds the call sends nothing.
//   - IR.92 2.4.3.1: without local resources, the UE sends nothing.
//   - RFC 5009 8: before the 2xx, a caller takes backward early media
//     only when the network authorized it and the preconditions are
//     met. The UE sends no early media itself: before the callee
//     answers it has nobody to talk to. The callee takes what comes as
//     soon as it has answered the offer.
func (s *Session) updateFlow() {
	if s.stream == nil {
		return
	}
	sends := s.localDir == dirSendrecv || s.localDir == dirSendonly
	peerTakes := s.remoteDir == dirSendrecv || s.remoteDir == dirRecvonly
	receives := s.localDir == dirSendrecv || s.localDir == dirRecvonly
	f := Flow{
		Send:    s.answered && sends && peerTakes && !s.held && s.reserved,
		Receive: receives && (s.answered || !s.caller || s.backwardEarlyMedia() && (s.precond == nil || s.precond.Met())),
	}
	s.media.SetFlow(f)
}

// earlyMediaAuthorized records the P-Early-Media of a message that
// came on the early dialog of tag (RFC 5009 8). A message without
// direction parameters changes nothing.
func (s *Session) earlyMediaAuthorized(tag string, m *sip.Message) {
	p := sip.ParsePEarlyMedia(m.GetAll("P-Early-Media"))
	// The audio stream is the first media line of every SDP of the UE.
	dir, ok := p.Direction(0)
	if !ok {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.answered {
		// The 200 authorized all media (RFC 5009 8).
		return
	}
	if s.earlyMedia == nil {
		s.earlyMedia = map[string]string{}
	}
	s.earlyMedia[tag] = dir
	s.log.Info("early media authorization", "to_tag", tag, "p_early_media", dir)
	s.updateFlow()
}

// alerted records a 180 on the early dialog of tag.
func (s *Session) alerted(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.alerting == nil {
		s.alerting = map[string]bool{}
	}
	s.alerting[tag] = true
}

// earlyDialogTerminated is TS 24.229 5.1.3.1 for a 199 on the early
// dialog of tag: the UE lets go of what belongs to that dialog, its
// early media authorization and its answer, and the next early dialog
// brings an answer of its own (RFC 6228 4).
func (s *Session) earlyDialogTerminated(tag string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.earlyMedia, tag)
	delete(s.alerting, tag)
	if s.answerTag == tag {
		s.answerTag = ""
	}
	s.updateFlow()
}

// hasAnswer tells that the session holds an answer of a dialog that
// has not ended.
func (s *Session) hasAnswer() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.answerTag != ""
}

// backwardEarlyMedia tells that an early dialog has the network's
// authorization for early media towards the caller (RFC 5009 8). s.mu
// is held.
func (s *Session) backwardEarlyMedia() bool {
	for _, dir := range s.earlyMedia {
		if dir == sip.EarlyMediaSendrecv || dir == sip.EarlyMediaSendonly {
			return true
		}
	}
	return false
}

// localRingback is IR.92 2.2.7 and TS 24.628 4.7.2.1: the caller plays
// a ringing tone of its own when an early dialog has had a 180, no
// early dialog has backward early media authorized, and no media comes
// from the network. s.mu is held.
func (s *Session) localRingback() bool {
	if !s.caller || s.answered || s.backwardEarlyMedia() || s.media.Stats().PacketsReceived > 0 {
		return false
	}
	for range s.alerting {
		return true
	}
	return false
}

// Ringback is how a caller hears its call ring before the answer.
type Ringback string

const (
	// RingbackLocal: an early dialog has had a 180, and no early media
	// came, so the UE plays a ringing tone of its own (IR.92 2.2.7, TS
	// 24.628 4.7.2.1).
	RingbackLocal Ringback = "local"
	// RingbackNetwork: the network authorized early media (RFC 5009)
	// and sent RTP before the answer, such as the customized alerting
	// tone of the callee (TS 24.182), which the UE plays instead.
	RingbackNetwork Ringback = "network"
)

// ringback is how the call rings now, "" before any ringing; after the
// answer, how it rang. s.mu is held.
func (s *Session) ringback() Ringback {
	switch {
	case !s.caller:
		return ""
	case s.answered:
		return s.rang
	case s.backwardEarlyMedia() && s.media.Stats().PacketsReceived > 0:
		return RingbackNetwork
	case s.localRingback():
		return RingbackLocal
	}
	return ""
}

// SessionStatus is the media side of a call, as a controller sees it.
type SessionStatus struct {
	// Codec is nil until the offer and answer have settled one.
	Codec *CodecInfo `json:"codec,omitempty"`
	// Direction is the direction of the audio stream in this UE's last
	// SDP (RFC 3264 6.1), and PeerDirection in the peer's.
	Direction     string `json:"direction,omitempty"`
	PeerDirection string `json:"peer_direction,omitempty"`
	// Preconditions is nil when the call does not use them.
	Preconditions *PreconditionStatus `json:"preconditions,omitempty"`
	// MediaResources tells that the access network carries the media
	// (IR.92 2.4.3.1).
	MediaResources bool `json:"media_resources"`
	// ReservationNeeded tells that the access network reserves those
	// resources, as the EPC sets up a dedicated bearer (IR.92 2.4.1).
	// Without it, as on the LAN of a softphone, the UE always has them
	// (TS 24.229 6.1.2), and MediaResources is always true.
	ReservationNeeded bool `json:"reservation_needed"`
	// Held tells that this UE has put the call on hold.
	Held bool `json:"held"`
	// EarlyMedia is the early media authorization of the network on the
	// early dialogs, by To tag (RFC 5009 8), and LocalRingback tells
	// that the caller plays a ringing tone of its own (IR.92 2.2.7).
	EarlyMedia    map[string]string `json:"early_media,omitempty"`
	LocalRingback bool              `json:"local_ringback,omitempty"`
	// Ringback is how the caller hears the call ring, and after the
	// answer how it rang: local or network.
	Ringback Ringback   `json:"ringback,omitempty"`
	Media    MediaStats `json:"media"`
	// DTMFSent and DTMFReceived are the digits so far.
	DTMFSent     string `json:"dtmf_sent,omitempty"`
	DTMFReceived string `json:"dtmf_received,omitempty"`
}

// PreconditionStatus is the precondition table of a call (RFC 3312 5).
type PreconditionStatus struct {
	// Met tells that every mandatory precondition is met.
	Met  bool              `json:"met"`
	Rows []PreconditionRow `json:"rows"`
}

// PreconditionRow is one row of the table, as RFC 3312 writes it, such
// as "local send": reserved, mandatory.
type PreconditionRow struct {
	Segment   string `json:"segment"`   // local or remote
	Direction string `json:"direction"` // send or recv
	Reserved  bool   `json:"reserved"`
	Strength  string `json:"strength"`
	Confirm   bool   `json:"confirm,omitempty"`
}

// Status is the session as it is now.
func (s *Session) Status() SessionStatus {
	s.mu.Lock()
	st := SessionStatus{Direction: s.localDir, PeerDirection: s.remoteDir, MediaResources: s.reserved,
		ReservationNeeded: s.reservationNeeded, Held: s.held, LocalRingback: s.localRingback(), Ringback: s.ringback()}
	if !s.answered && len(s.earlyMedia) > 0 {
		st.EarlyMedia = maps.Clone(s.earlyMedia)
	}
	if s.stream != nil {
		c := codecInfo(*s.stream)
		st.Codec = &c
	}
	if s.precond != nil {
		p := &PreconditionStatus{Met: s.precond.Met()}
		for _, r := range s.precond.Rows() {
			p.Rows = append(p.Rows, PreconditionRow{Segment: string(r.Status), Direction: r.Direction.String(),
				Reserved: r.Current, Strength: r.Strength.String(), Confirm: r.Confirm})
		}
		st.Preconditions = p
	}
	s.mu.Unlock()
	st.Media = s.media.Stats()
	st.DTMFSent, st.DTMFReceived = s.media.DTMF()
	return st
}
