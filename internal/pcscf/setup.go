package pcscf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// setup is one initial INVITE on its way through the P-CSCF: what the
// P-CSCF holds while the INVITE transaction lasts. Like the transaction
// it lives in the process. What outlasts the transaction is the
// Dialog, which setup puts in the Store with the first response that
// creates a dialog.
type setup struct {
	p   *PCSCF
	leg leg
	ue  Association
	// invite is the INVITE as the P-CSCF received it, and icid the
	// icid-value that it goes on with: the one that the P-CSCF wrote
	// for the calling UE, or the one that came with it for the called
	// UE.
	invite *sip.Message
	icid   string
	log    *slog.Logger

	// relay is the context of the call in the media relay, or nil.
	relay *RelayContext
	// offered is the SDP offer of the INVITE as the UE sees it, and
	// authorized the answer, as the UE sees it, that the PCRF last
	// authorised in session.
	offered    []byte
	authorized []byte
	session    policy.SessionID
	// key is the key of the Dialog once stored is true.
	key    DialogKey
	stored bool
	// givenUp is true once the P-CSCF has ended the INVITE itself.
	givenUp bool
	// ended makes end free what the call holds once.
	ended sync.Once
	// abort is closed, and aborted and abortCause set under setupsMu,
	// when the P-CSCF releases the early dialog while the INVITE is still
	// on its way (TS 24.229 5.2.8.1.1). abortCause tells the cause of the
	// release (ReleaseCause.err).
	abort      chan struct{}
	aborted    bool
	abortCause error
	// callerSeq is the CSeq of the last request that the caller sent in
	// the early dialog, as the Dialog had it when an abort took it out
	// of the Store (abortSetup), or before the P-CSCF gave the INVITE up
	// for a 2xx and freed what the call holds (proxyInvite).
	callerSeq uint32
}

func (p *PCSCF) newSetup(st *sip.ServerTransaction, l leg, ue Association, icid string, log *slog.Logger) *setup {
	return &setup{p: p, leg: l, ue: ue, invite: st.Request, icid: icid, log: log, abort: make(chan struct{})}
}

// toUE tells which way the INVITE passes the P-CSCF. Its responses pass
// the other way.
func (s *setup) toUE() bool { return s.leg == terminating }

// offer puts the call through the relay: a context for this
// pass through the P-CSCF, and the offer in out, the INVITE on
// its way to the next hop, pointed at the termination facing that hop.
//
// SIMPLIFIED: RFC 3264 and RFC 3262. An INVITE without SDP, whose offer
// comes in a reliable response with the answer in the PRACK or the ACK,
// gets no relay context, and its SDP is left as it is and is not
// authorised. The same holds for a re-INVITE without SDP. Offers in
// requests within the dialog are handled by renegotiate.
//
// SIMPLIFIED: RFC 3264 6. Only the first audio stream with a port goes
// through the relay, even when the answer accepts another one instead.
// The other streams get no termination, but the session-level address
// now names the relay, so one without an address of its own points at
// the relay with its original port, where nothing listens. The UE
// simulator offers a single stream.
func (s *setup) offer(out *sip.Message) error {
	if len(out.Body) == 0 {
		return nil
	}
	fromUE := out.Body
	if err := s.throughRelay(out); err != nil {
		return err
	}
	s.offered = ueSide(fromUE, out.Body, s.toUE())
	return nil
}

func (s *setup) throughRelay(out *sip.Message) error {
	if len(s.p.relays) == 0 {
		return nil
	}
	offer, err := sdp.Parse(out.Body)
	if err != nil {
		return err
	}
	offerAddr, _, err := offer.Audio()
	if err != nil {
		return nil
	}
	relay, c, err := s.p.addContext(s.invite.Get("Call-ID"), s.log)
	if err != nil {
		return err
	}
	s.relay = &RelayContext{Relay: relay.Name, ID: c.ID, Access: s.p.advertise(c, agw.Access, s.ue), Core: c.Terminations[agw.Core].Local}
	if err := s.p.relaySDP(out, s.relay, s.toUE()); err != nil {
		return err
	}
	s.log.Info("media through the relay", "relay", relay.Name, "context", c.ID, "leg", string(s.leg),
		"offer_from", offerAddr.String(), "access", s.relay.Access.String(), "core", s.relay.Core.String())
	return nil
}

// addContext adds the context of one pass of a call through the P-CSCF
// to a media relay: Reserve AGW Connection Point (TS 29.334 5.17.2.2),
// with a termination towards the UE and one towards the network. Only
// the termination towards the UE latches, since only a UE can be behind
// a NAT that its SDP does not show. The one towards the network sends
// where the SDP of the network side says (H.248.37 (06/2008) 6.6.1):
// that party changes within a call, as when the MMTel AS ends a
// customized alerting tone and gives the caller the SDP of the callee,
// and a latched address would keep the media on the MRFP. The
// relay comes from a hash of the Call-ID, as the MRFC picks an MRFP, so
// that the calls spread over the relays of the list. When that relay
// gives no context, because it does not answer or has no port left
// (503), the next one of the list is tried, and after the last one the
// first. When no relay gives a context, the error is that of the last
// one tried. Each pass of a call through the P-CSCF has a dialog of its
// own, which names the relay of its context.
//
// SIMPLIFIED: TS 23.334 6.1.2 and 6.1.3. The P-CSCF does not keep track
// of which relays are unavailable, and does not audit them to find out
// when one is back: each new call tries the relay its Call-ID picks, and
// a call that comes to a relay that has gone waits for that Add to fail
// before it goes on to the next. A context that a relay added though its
// answer was lost stays in that relay, since the P-CSCF never learnt
// its ID. The calls whose contexts a relay held
// when it went keep their signalling, without media, until a UE hangs
// up; the P-CSCF neither ends them nor moves their media to another
// relay. A relay that comes back at its own address opens its contexts
// again, and their media goes on, so ending the calls would cut ones
// that recover. Moving the media would take an offer from the P-CSCF to
// each party, which a proxy does not send.
func (p *PCSCF) addContext(callID string, log *slog.Logger) (MediaRelay, agw.Context, error) {
	h := fnv.New32a()
	h.Write([]byte(callID))
	first := int(h.Sum32() % uint32(len(p.relays)))
	var last error
	for i := range p.relays {
		relay := p.relays[(first+i)%len(p.relays)]
		ctx, cancel := relayContext()
		c, err := relay.Control.Add(ctx, agw.TerminationRequest{ID: agw.Access}, agw.TerminationRequest{ID: agw.Core, Latch: agw.NAPTOff})
		cancel()
		if err == nil {
			return relay, c, nil
		}
		log.Warn("the media relay gave no context", "relay", relay.Name, "error", err.Error())
		last = err
	}
	return MediaRelay{}, agw.Context{}, last
}

// ueSide picks, of an SDP body before and after the P-CSCF pointed it
// at the relay, the one the UE has: the body as the UE sent it, or as
// the P-CSCF sends it to the UE. That is the SDP the PCRF gets, because
// the bearer carries the UE's media to and from the relay (TS 29.214
// A.1).
func ueSide(received, forwarded []byte, toUE bool) []byte {
	if toUE {
		return forwarded
	}
	return received
}

// forward sends the INVITE on to the first of targets, from stack, and
// sees it through to its final response, which it passes on. It then
// frees what the call holds, unless the INVITE was answered with a 2xx.
func (s *setup) forward(st *sip.ServerTransaction, out *sip.Message, targets []sip.Destination, stack *ims.StackRef) {
	final, err := s.p.proxyInvite(st, out, targets, stack, s)
	s.settle()
	if final != nil {
		s.p.ueResponse(s.leg, s.invite, final)
	}
	ims.Finish(st, final, err)
	// Freeing may wait for the relay and the PCRF, so the response goes
	// on first.
	if err != nil || s.givenUp || (final != nil && final.StatusCode >= 300) {
		s.end()
	}
}

// errRelay marks an SDP answer that the P-CSCF could not point at the
// media relay, because the relay did not take it or its SDP could not
// be read. The IMS-ALG has no answer of its own to pass on then (TS
// 24.229 6.7.2.1), and the one that came would lead its party past the
// relay.
var errRelay = errors.New("pcscf: the answer is not pointed at the media relay")

// errReleased marks a response to an INVITE whose early dialog the
// P-CSCF has released, or that has gone from the Store, while the
// INVITE was on its way. Another instance of the P-CSCF releases it so
// when a request within the early dialog passes that instance, and it
// leaves the INVITE to the instance that forwards it (releaseNow). The
// same instance may release it between two responses too, and the
// response that comes first then finds the mark before the abort.
var errReleased = errors.New("pcscf: the dialog of the INVITE has been released")

// errResources marks a session that the network has no resources for,
// or whose resources it has taken away. The caller's 500 tells so (TS
// 24.229 5.2.7.2, 7.2A.18.12.2).
var errResources = errors.New("pcscf: the network has no resources for the session")

// releasedFor is errReleased for a dialog that the P-CSCF released for
// cause, so that the INVITE ends with the 500 of that cause.
func releasedFor(cause ReleaseCause) error {
	return fmt.Errorf("%w: %w", errReleased, cause.err())
}

// response sees every response to the INVITE that the P-CSCF passes on,
// the provisional ones and the 2xx, before it leaves: it sets the
// identities in it, points the answer at the relay, has the PCRF
// authorise the media, and keeps the dialog. An error means that the
// network has no resources for the session (errResources); with
// errRelay, that the answer is not pointed at the relay, which leaves
// the PCRF without it too; with errReleased, that the dialog has been
// released, wrapping the cause of the release when it is known; or, with
// errStore, that the P-CSCF cannot keep the dialog. A body that is not
// SDP carries no answer, and goes on as it came.
//
// What a released dialog held is freed, so its answer goes neither to
// the relay nor to the PCRF. remember checks once more, as one step with
// its change of the dialog, since the release may come in between.
func (s *setup) response(resp *sip.Message) error {
	s.p.ueResponse(s.leg, s.invite, resp)
	if err := s.released(); err != nil {
		return err
	}
	var refused error
	if sip.HasSessionDescription(resp) {
		received := resp.Body
		if err := s.p.relaySDP(resp, s.relay, !s.toUE()); err != nil {
			s.log.Warn("media relay refused the answer", "error", err.Error())
			return fmt.Errorf("%w: %w", errRelay, err)
		}
		if s.offered != nil {
			refused = s.authorize(ueSide(received, resp.Body, !s.toUE()))
		}
	}
	switch err := s.remember(resp); {
	case errors.Is(err, errReleased):
		return err
	case err != nil:
		return storeError(err)
	}
	return refused
}

// released is the part of response that reads whether the early dialog
// of the INVITE has been released: errReleased, with the cause of the
// release when it is known, or the failure of the Store.
func (s *setup) released() error {
	if !s.stored {
		return nil
	}
	d, ok, err := s.p.store.UpdateDialog(context.Background(), s.key, func(*Dialog) {})
	switch {
	case err != nil:
		return storeError(err)
	case !ok:
		return errReleased
	case d.Released:
		return releasedFor(d.ReleaseCause)
	}
	return nil
}

// mediaEvents are what the P-CSCF asks to hear about the media of a
// session: each of them takes the resources of the session away, so the
// P-CSCF ends the session (TS 24.229 5.2.7.2, 5.2.8.1).
var mediaEvents = []policy.EventKind{policy.LossOfBearer, policy.ReleaseOfBearer, policy.FailedResourcesAllocation}

// authorize gives the PCRF the service information of the session once
// its SDP answer is known (TS 29.214 A.1, 4.4.1): the PCRF authorises
// the media, and the network sets up the bearer that carries it. answer
// is the answer as the UE sees it.
//
// A.1 asks for this on every SIP message with an SDP answer. The same
// answer comes again in the 2xx after an unreliable 183, and in each
// retransmission of the 2xx, so an answer that has been authorised is
// not sent again.
//
// A UE whose registration has no policy session does not come through
// the EPC, and its calls get no policy control.
//
// Of the choices that A.1 and A.2.0 leave to the P-CSCF, it sends no
// service information for an offer alone, and it enables the flows as
// the SDP directions say also before the 2xx, rather than gate early
// media by operator policy or P-Early-Media.
func (s *setup) authorize(answer []byte) error {
	if s.p.policy == nil || bytes.Equal(answer, s.authorized) {
		return nil
	}
	// The P-CSCF subscribes to the signalling path after its 200 to the
	// REGISTER (watchSignallingPath), so a UE that calls at once can send
	// its INVITE before the policy session is in the association. The
	// answer comes back through the callee, so the registration is read
	// as it is now, not as it was when the INVITE came.
	a, ok, err := s.p.store.Association(context.Background(), s.ue.Source)
	if err != nil {
		return storeError(err)
	}
	if !ok || a.SignallingSession == "" {
		return nil
	}
	session, err := s.p.policy.AuthorizeMedia(context.Background(), policy.MediaRequest{
		Session:       s.session,
		UE:            s.ue.Source.Addr.Addr(),
		SDP:           policy.OfferAnswer{Offer: s.offered, Answer: answer, UEOffered: !s.toUE()},
		ApplicationID: s.communicationService(),
		Subscribe:     mediaEvents,
	})
	if err != nil {
		s.log.Warn("the network has no resources for the media", "error", err.Error())
		return fmt.Errorf("%w: %w", errResources, err)
	}
	s.log.Info("media authorised", "policy_session", string(session))
	s.session, s.authorized = session, answer
	return nil
}

// communicationService is the IMS communication service identifier of
// the session for the PCRF (TS 29.214 A.1 NOTE 3). The terminating
// P-CSCF takes it from P-Asserted-Service in the INVITE.
//
// SIMPLIFIED: TS 29.214 A.1. The originating P-CSCF should take it from
// the Feature-Caps of the 18x or 2xx. No node of this project writes
// that header field, so the originating P-CSCF sends no identifier.
func (s *setup) communicationService() string {
	if s.leg == terminating {
		return s.invite.Get("P-Asserted-Service")
	}
	return ""
}

// failed is the 500 with which the P-CSCF ends an INVITE itself. Operator
// policy may add who answered to any failure response of the P-CSCF
// (TS 24.229 5.2.1, Response-Source): it is added here, for the learner
// who reads the capture.
func (s *setup) failed() *sip.Message {
	resp := sip.NewResponse(s.invite, 500, sip.NewTag())
	side := "term"
	if s.leg == originating {
		side = "orig"
	}
	resp.Set("Response-Source", "fe=<urn:3gpp:fe:p-cscf."+side+">")
	return resp
}

// unavailable is the response with which the P-CSCF ends an INVITE whose
// session the network has no resources for: 500 (TS 24.229 5.2.7.2,
// 5.2.8.1.1). Operator policy may add, towards the calling UE, why
// (5.2.7.2, Reason): it is added here too.
func (s *setup) unavailable() *sip.Message {
	resp := s.failed()
	if s.leg == originating {
		// TS 24.229 7.2A.18.12.2, Table 7.2A.18.12-2.
		resp.Set("Reason", `FAILURE_CAUSE;cause=1;text="Media bearer or QoS lost"`)
	}
	return resp
}

// remember stores the dialog with the first response that creates one,
// and brings it up to date with each later one (TS 24.229 5.2.6.3.4,
// 5.2.6.4.4). It returns errReleased for a dialog that has been released
// or is gone, which it leaves as it is, and otherwise the failure of the
// Store.
func (s *setup) remember(resp *sip.Message) error {
	calleeTag := tagOf(resp.Get("To"))
	if calleeTag == "" {
		// RFC 3261 12.1: a response without a To tag creates no dialog.
		return nil
	}
	// The response has the Record-Route of the whole path. The INVITE
	// had the part of it that leads to the caller when it arrived.
	upstream := len(s.invite.GetAll("Record-Route"))
	toCaller, toCallee, ok := routeSets(resp.GetAll("Record-Route"), upstream)
	if !ok {
		s.log.Warn("response without the Record-Route of the P-CSCF", "record_route", resp.GetAll("Record-Route"))
		toCaller = s.invite.GetAll("Record-Route")
	}
	// confirmed is true when the 2xx confirms the dialog that was early,
	// which sets up the session (TS 32.409 4.1.2.7); endDialog counts it
	// out again.
	confirmed, released := false, false
	var cause ReleaseCause
	update := func(d *Dialog) {
		confirmed, released, cause = false, d.Released, d.ReleaseCause
		if released {
			return
		}
		confirmed = !d.Confirmed && resp.StatusCode >= 200
		d.Confirmed = d.Confirmed || resp.StatusCode >= 200
		d.PolicySession = s.session
		d.Callee.NameAddr = resp.Get("To")
		d.Callee.Route = toCallee
		if contact := contactOf(resp); contact != "" {
			d.Callee.Target = contact
		}
	}
	ctx := context.Background()
	if s.stored {
		_, ok, err := s.p.store.UpdateDialog(ctx, s.key, update)
		switch {
		case err != nil:
			return err
		case !ok:
			return errReleased
		case released:
			return releasedFor(cause)
		case confirmed:
			s.p.measurements.sessions.SetUp(role)
		}
		return nil
	}
	d := Dialog{Originating: s.leg == originating, Relay: s.relay, ICID: s.icid}
	d.Key = DialogKey{CallID: s.invite.Get("Call-ID"), UETag: calleeTag}
	if d.Originating {
		d.Key.UETag = tagOf(s.invite.Get("From"))
	}
	d.Caller = Party{NameAddr: s.invite.Get("From"), Target: contactOf(s.invite), Route: toCaller}
	if cseq, err := sip.ParseCSeq(s.invite.Get("CSeq")); err == nil {
		d.Caller.CSeq = cseq.Number
	}
	update(&d)
	if err := s.p.store.PutDialog(ctx, d); err != nil {
		return err
	}
	if confirmed {
		s.p.measurements.sessions.SetUp(role)
	}
	s.key, s.stored = d.Key, true
	s.p.setupsMu.Lock()
	s.p.setups[s.key] = s
	s.p.setupsMu.Unlock()
	return nil
}

// settle takes the INVITE out of the setups when its 2xx is about to be
// passed on, or when the INVITE has ended. From then on the dialog is
// released with a BYE (TS 24.229 5.2.8.1.2). It reports false when the
// INVITE has been aborted first: the 2xx must then not be passed on.
func (s *setup) settle() bool {
	s.p.setupsMu.Lock()
	defer s.p.setupsMu.Unlock()
	if s.p.setups[s.key] == s {
		delete(s.p.setups, s.key)
	}
	return !s.aborted
}

// abortSetup ends the INVITE that created d, the early dialog that the
// P-CSCF has marked Released, if this P-CSCF instance still forwards it:
// its loop then answers the caller and cancels the INVITE towards the
// callee, as giveUp does for the cause of the release.
func (p *PCSCF) abortSetup(d Dialog) bool {
	p.setupsMu.Lock()
	defer p.setupsMu.Unlock()
	s, ok := p.setups[d.Key]
	if !ok {
		return false
	}
	delete(p.setups, d.Key)
	s.aborted = true
	s.abortCause = d.ReleaseCause.err()
	s.callerSeq = d.Caller.CSeq
	close(s.abort)
	return true
}

// end frees what the call holds when its INVITE fails: the dialog, if
// a response has created one, and otherwise the relay context and the
// policy session alone. Only the first call frees them.
//
// A call whose dialog the network released has freed them already; the
// closed abort says so, in case the Store fails and endDialog has only
// holds to go by.
func (s *setup) end() {
	s.ended.Do(func() {
		holds := Dialog{Relay: s.relay, PolicySession: s.session}
		select {
		case <-s.abort:
			holds.Released = true
		default:
		}
		if s.stored {
			s.p.endDialog(s.key, holds, s.log)
			return
		}
		s.p.free(holds, s.log)
	})
}
