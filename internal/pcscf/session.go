package pcscf

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/ims"
	"github.com/yuu-biz/ims-practice/internal/policy"
	"github.com/yuu-biz/ims-practice/internal/sdp"
	"github.com/yuu-biz/ims-practice/internal/sip"
)

// Relay is the media relay that the P-CSCF, as the IMS-ALG, controls
// over Iq (TS 23.334, TS 29.334). The operations carry the names of the
// H.248 commands, on the types of internal/agw, and each is a call
// related procedure of Iq. The P-CSCF adds a context with the two
// terminations agw.Access, which faces the UE, and agw.Core, which faces
// the rest of the network, tells each where to send, and subtracts the
// context. Two values implement it: *agw.Client, over the HTTP API of
// the media node, and the H.248 Controller of internal/h248/mediactl.
type Relay interface {
	// Add is Reserve AGW Connection Point (TS 29.334 5.17.2.2) for the
	// terminations of one new context. The context it returns has, for
	// each termination, the address on which it receives RTP, which the
	// SDP that its party receives is to show.
	Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error)
	// Modify is Configure AGW Connection Point (TS 29.334 5.17.2.3): it
	// tells a termination where its party takes the media, as the SDP
	// of that party gives it.
	Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error
	// Subtract is Release AGW Termination (TS 29.334 5.17.2.5) for both
	// terminations of the context.
	Subtract(ctx context.Context, id string) error
}

// MediaRelay is a media relay that the P-CSCF can use. Name is how a
// Dialog remembers it (RelayContext.Relay), the same in every P-CSCF
// instance, such as the URL of its control API.
type MediaRelay struct {
	Name    string
	Control Relay
}

// HostAccess is for UEs that reach the P-CSCF through the NAT of the
// container host, such as a softphone on the same machine. Their
// requests come from Gateway, and they can reach the relay's published
// ports only at Address, not at the relay's own address.
type HostAccess struct {
	Gateway netip.Addr
	Address netip.Addr
}

// leg is which way a request passes the P-CSCF (TS 24.229 5.2.6.2).
type leg string

const (
	originating leg = "originating" // from the UE
	terminating leg = "terminating" // to the UE
)

func (p *PCSCF) ownURI() sip.URI {
	u, _ := ims.LooseRoute(fmt.Sprintf("sip:%s:%d", p.cfg.Host, p.cfg.Port))
	return u
}

// session handles every request but REGISTER. A NOTIFY of the
// P-CSCF's own reg event subscription is for the P-CSCF; any other
// request it routes.
func (p *PCSCF) session(st *sip.ServerTransaction) {
	if p.ownRegEventNotify(st.Request) {
		p.receiveRegEvent(st)
		return
	}
	req := st.Request
	out := req.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	top, popped := sip.PopOwnRoute(out, p.ownURIs()...)
	log := p.log.With("method", string(req.Method), "call_id", req.Get("Call-ID"))
	switch {
	case ims.IsInDialog(req):
		p.subsequent(st, out, log)
	case popped && top.User == "term":
		// The Path URI that the P-CSCF put in the REGISTER marks requests
		// to the UE (5.2.6.2).
		p.initial(st, out, terminating, log)
	default:
		p.initial(st, out, originating, log)
	}
}

// initial is TS 24.229 5.2.6.3.3 for an initial request from the UE,
// and 5.2.6.4.3 for one to the UE. A request for a standalone
// transaction, such as a MESSAGE, takes the same steps but the
// Record-Route (5.2.6.3.7, 5.2.6.4.7).
func (p *PCSCF) initial(st *sip.ServerTransaction, out *sip.Message, l leg, log *slog.Logger) {
	req := st.Request
	var targets []sip.Destination
	var ue Association
	// icid is the icid-value of the request as it leaves the P-CSCF.
	var icid string
	if l == originating {
		a, ok, err := p.association(st, "")
		if err != nil {
			st.Respond(storeFailed(req, err, log))
			return
		}
		if !ok {
			// Screen let the request in, so the association was there when
			// the request arrived. It has ended since, or Screen is not the
			// one of the stack; either way the P-CSCF cannot serve it.
			log.Warn("the IP association of the request is gone", "source", st.Source.String())
			st.Respond(sip.NewResponse(req, 500, sip.NewTag()))
			return
		}
		ue = a
		crossBoundary(out, requestFromUE)
		// Step 1 has already removed the P-CSCF's own Route. Step 2 ii:
		// the rest must be the Service-Route of the registration; option
		// b puts it there when the UE did not preload it right.
		if !equalRoutes(out.GetAll("Route"), a.ServiceRoute) {
			log.Info("replaced the preloaded Route with the Service-Route", "route", out.GetAll("Route"))
			out.Remove("Route")
			for _, r := range a.ServiceRoute {
				out.Add("Route", r)
			}
		}
		// Step 6: the P-CSCF asserts who the caller is, as
		// 5.2.6.3.1 identifies the caller.
		out.Remove("P-Asserted-Identity")
		for _, id := range assertedIdentities(req, a) {
			out.Add("P-Asserted-Identity", "<"+id+">")
		}
		// Step 7.
		icid = sip.NewCallID()
		p.chargeRequest(out, icid)
		u, err := sip.NextHop(out)
		if err == nil {
			targets, err = ims.Targets(context.Background(), p.locator, u)
		}
		if err != nil {
			log.Warn("cannot route the request", "error", err.Error())
			st.Respond(sip.NewResponse(req, 504, sip.NewTag()))
			return
		}
	} else {
		a, ok, err := p.associationOfContact(out.RequestURI)
		if err != nil {
			st.Respond(storeFailed(req, err, log))
			return
		}
		if !ok {
			log.Info("no registration for the contact", "contact", out.RequestURI)
			st.Respond(sip.NewResponse(req, 480, sip.NewTag()))
			return
		}
		if a.SignallingLost {
			// 5.2.6.4.3 item 1, 5.2.6.4.7 item 1.
			log.Info("the signalling bearer of the UE is lost", "contact", out.RequestURI)
			st.Respond(sip.NewResponse(req, 500, sip.NewTag()))
			return
		}
		ue = a
		// The UE is behind NAT, so the request goes to where its REGISTER
		// came from, not to its Contact.
		targets = []sip.Destination{a.Source}
		// The P-CSCF has read P-Asserted-Service for the PCRF from the
		// request it received (communicationService), and reads the
		// charging vector there when the UE answers (chargeResponse).
		charging, _ := sip.ChargingVectorOf(req)
		icid = charging.ICID
		crossBoundary(out, requestToUE)
	}
	// Step 5 of 5.2.6.3.3, and 5.2.6.4.3: later requests of the dialog
	// come through the P-CSCF. A request for a standalone transaction
	// has no dialog to stay on (5.2.6.3.7, 5.2.6.4.7).
	if ims.CreatesDialog(out.Method) {
		own, err := p.ownURIToward(ue.Source)
		if err != nil {
			st.Respond(storeFailed(req, err, log))
			return
		}
		sip.AddRecordRoute(out, own)
	}

	if out.Method != sip.INVITE {
		log.Info("forwarding", "leg", string(l), "to", targets[0].String())
		identity := func(resp *sip.Message) { p.ueResponse(l, req, resp) }
		final, err := st.ProxyWith(out, identity, targets...)
		if final != nil {
			identity(final)
		}
		ims.Finish(st, final, err)
		return
	}
	// Every target of one next hop is on the same side, the UE's or the
	// network's.
	stack, err := p.stackToward(targets[0])
	if err != nil {
		st.Respond(storeFailed(req, err, log))
		return
	}
	call := p.newSetup(st, l, ue, icid, log)
	if err := call.offer(out); err != nil {
		log.Warn("media relay refused the call", "error", err.Error())
		st.Respond(sip.NewResponse(req, 500, sip.NewTag()))
		// The 500 ends the call, so a context already added goes too.
		call.end()
		return
	}
	log.Info("forwarding", "leg", string(l), "to", targets[0].String())
	call.forward(st, out, targets, stack)
}

// subsequent forwards a request within a dialog along its route set
// (RFC 3261 16.12), and keeps the Dialog up to date with it. A BYE that
// ends the dialog ends what the dialog holds (TS 24.229 5.2.8.2).
func (p *PCSCF) subsequent(st *sip.ServerTransaction, out *sip.Message, log *slog.Logger) {
	targets, toUE, err := p.nextHop(out)
	if errors.Is(err, errStore) {
		st.Respond(storeFailed(st.Request, err, log))
		return
	}
	if err != nil {
		log.Warn("cannot route the request", "error", err.Error())
		st.Respond(sip.NewResponse(st.Request, 404, sip.NewTag()))
		return
	}
	request, response := requestFromUE, responseToUE
	if toUE {
		request, response = requestToUE, responseFromUE
	}
	crossBoundary(out, request)
	if toUE && isTargetRefresh(out.Method) {
		// 5.2.6.4.5 item 4. For a NOTIFY this is what keeps the P-CSCF
		// on the route of the subscription: the subscriber takes the
		// route set from the first NOTIFY (RFC 6665 4.3, 4.4.1).
		own, err := p.ownURIToward(targets[0])
		if err != nil {
			st.Respond(storeFailed(st.Request, err, log))
			return
		}
		sip.AddRecordRoute(out, own)
	}
	// Only the dialogs of INVITEs are kept, so a request of another
	// dialog, such as a NOTIFY, finds none.
	key := subsequentKey(st.Request, toUE)
	d, known, err := p.store.UpdateDialog(context.Background(), key, func(d *Dialog) { d.noteRequest(st.Request, toUE) })
	if err != nil {
		// Without the Dialog the P-CSCF could not release the session
		// with the right CSeq, nor end what it holds after a BYE.
		st.Respond(storeFailed(st.Request, err, log))
		return
	}
	if known && d.Released {
		// TS 24.229 5.2.8.1.3: the P-CSCF is releasing the dialog itself.
		log.Info("answered a request within a dialog that the P-CSCF releases")
		st.Respond(sip.NewResponse(st.Request, 481, ""))
		return
	}
	// The icid-value of every request of the dialog and of its responses
	// is that of the INVITE.
	//
	// SIMPLIFIED: TS 24.229 5.2.6.3.5 step 7 and 5.2.6.3.9 step 3. A
	// request that the UE sends in a dialog other than that of an
	// INVITE, such as the refresh of its subscription, goes on without a
	// P-Charging-Vector: the P-CSCF keeps no such dialog, and so has no
	// icid-value of the request that started it. A response from the UE
	// in such a dialog takes the icid-value of the request it answers.
	charging, _ := sip.ChargingVectorOf(st.Request)
	if known && d.ICID != "" {
		charging.ICID = d.ICID
		if !toUE {
			p.chargeRequest(out, d.ICID)
		}
	}
	answer := func(*sip.Message) {}
	if known && sip.HasSessionDescription(out) {
		ue := st.Source.Addr.Addr()
		if toUE {
			ue = targets[0].Addr.Addr()
		}
		answer = p.renegotiate(out, d, toUE, ue, log)
	}
	if known && isTargetRefresh(out.Method) {
		renegotiated := answer
		answer = func(resp *sip.Message) {
			if resp.StatusCode < 300 {
				_, _, err := p.store.UpdateDialog(context.Background(), key, func(d *Dialog) { d.noteTargetRefresh(st.Request, resp, toUE) })
				if err != nil {
					// The response goes on; a release would send its BYE
					// to the targets that the dialog had before.
					log.Warn("the dialog keeps its old targets: the store failed", "error", err.Error())
				}
			}
			renegotiated(resp)
		}
	}
	relay := func(resp *sip.Message) {
		crossBoundary(resp, response)
		if toUE {
			p.chargeResponse(resp, charging)
		}
		answer(resp)
	}
	final, err := st.ProxyWith(out, relay, targets...)
	if final != nil {
		// Proxy passes on a 2xx to an INVITE itself, and leaves every
		// other final response to its user.
		relay(final)
	}
	ims.Finish(st, final, err)
	if out.Method == sip.BYE && byeEndsDialog(final, err) {
		p.endDialog(key, d, log)
	}
}

// renegotiate handles a new SDP offer in out, a request within the
// dialog d on its way to the next hop, such as an UPDATE that tells of
// reserved resources (RFC 3311, RFC 3312) or a re-INVITE that puts the
// call on hold (RFC 3261 14). The offer goes through the relay context
// of the dialog like the first one. The function it returns takes each
// response on its way back: with the answer the PCRF gets the changed
// service information in the policy session of the dialog (TS 29.214
// A.1, 4.4.2). ue is the address of the served UE.
//
// SIMPLIFIED: TS 29.214 4.4.2. When the PCRF refuses the changed
// service information, the P-CSCF only logs it, and the session stays
// authorised as it was: the far end has given its answer by then. TS
// 24.229 V20.1.0 has a procedure for refused resources only for the
// initial INVITE (5.2.7.2).
func (p *PCSCF) renegotiate(out *sip.Message, d Dialog, toUE bool, ue netip.Addr, log *slog.Logger) func(*sip.Message) {
	received := out.Body
	if err := p.relaySDP(out, d.Relay, toUE); err != nil {
		log.Warn("media relay refused the offer", "error", err.Error())
	}
	offer := ueSide(received, out.Body, toUE)
	authorized := false
	return func(resp *sip.Message) {
		if resp.StatusCode >= 300 || !sip.HasSessionDescription(resp) {
			return
		}
		received := resp.Body
		if err := p.relaySDP(resp, d.Relay, !toUE); err != nil {
			log.Warn("media relay refused the answer", "error", err.Error())
		}
		// A 2xx to a re-INVITE comes again until its ACK arrives.
		if authorized || d.PolicySession == "" || p.policy == nil {
			return
		}
		authorized = true
		_, err := p.policy.AuthorizeMedia(context.Background(), policy.MediaRequest{
			Session:   d.PolicySession,
			UE:        ue,
			SDP:       policy.OfferAnswer{Offer: offer, Answer: ueSide(received, resp.Body, !toUE), UEOffered: !toUE},
			Subscribe: mediaEvents,
		})
		if err != nil {
			log.Warn("the PCRF refused the changed media", "policy_session", string(d.PolicySession), "error", err.Error())
			return
		}
		log.Info("changed media authorised", "policy_session", string(d.PolicySession))
	}
}

// byeEndsDialog is RFC 3261 15.1.1 as a proxy sees it: the dialog ends
// when the BYE is answered with a 2xx, a 481 or a 408, or not at all.
// Any other answer, such as the 500 to a BYE out of order (12.2.2),
// leaves the dialog up, so the relay context stays for the BYE that
// will end it.
func byeEndsDialog(final *sip.Message, err error) bool {
	if err != nil || final == nil {
		return true
	}
	code := final.StatusCode
	return code < 300 || code == 481 || code == 408
}

// nextHop gives the targets of the top Route or the Request-URI, except
// that a UE's contact is reached at the address its REGISTER came from;
// toUE reports that case.
func (p *PCSCF) nextHop(m *sip.Message) (targets []sip.Destination, toUE bool, err error) {
	u, err := sip.NextHop(m)
	if err != nil {
		return nil, false, err
	}
	a, ok, err := p.associationOfContact(u.String())
	if err != nil {
		return nil, false, storeError(err)
	}
	if ok {
		return []sip.Destination{a.Source}, true, nil
	}
	targets, err = ims.Targets(context.Background(), p.locator, u)
	return targets, false, err
}

// isTargetRefresh reports the target refresh requests of this project:
// re-INVITE (RFC 3261 12.2), UPDATE (RFC 3311 5.1), SUBSCRIBE and NOTIFY
// (RFC 6665 3.1, 3.2).
func isTargetRefresh(m sip.Method) bool {
	return m == sip.INVITE || m == sip.UPDATE || m == sip.SUBSCRIBE || m == sip.NOTIFY
}

// OnACK forwards the ACK for a 2xx the same way.
func (p *PCSCF) OnACK(ack *sip.Message) { p.forwardACK(&p.stack, ack) }

// forwardACK forwards ack from the stack it came to. It notes in the
// Dialog that the 2xx has its ACK, and sends the BYEs of a release that
// waited for that ACK (release).
func (p *PCSCF) forwardACK(ref *ims.StackRef, ack *sip.Message) {
	stack, err := ref.Get()
	if err != nil {
		return
	}
	out := ack.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		// RFC 3261 16.3 step 3 answers such a request 483, but no
		// response to an ACK is allowed (17), so it is only dropped.
		p.log.Info("dropped ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
		return
	}
	sip.PopOwnRoute(out, p.ownURIs()...)
	targets, toUE, err := p.nextHop(out)
	if err != nil {
		p.log.Warn("cannot forward ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
		return
	}
	if toUE {
		crossBoundary(out, requestToUE)
	} else {
		crossBoundary(out, requestFromUE)
	}
	key := subsequentKey(ack, toUE)
	d, known, err := p.store.UpdateDialog(context.Background(), key, func(d *Dialog) { d.Acknowledged = true })
	if err != nil {
		// The ACK goes on, without the charging identifier of the dialog.
		// A release that waits for it sends its BYEs after ackWait.
		p.log.Warn("could not note the ACK in the dialog: the store failed", "call_id", ack.Get("Call-ID"), "error", err.Error())
	}
	if !toUE && known && d.ICID != "" {
		// 5.2.6.3.9 step 3.
		p.chargeRequest(out, d.ICID)
	}
	if err := stack.SendACK(out, targets...); err != nil {
		p.log.Warn("cannot forward ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
	}
	// The ACK goes on first, so that the callee has it before the BYE.
	if known && d.ReleasePending {
		go p.releaseNow(key, p.log.With("method", string(sip.ACK), "call_id", key.CallID))
	}
}

// relaySDP puts the media of the SDP in m through the relay context rc.
// m passes the P-CSCF towards the UE, or away from it: the termination
// that faces where m came from learns from the SDP where to send, and
// the SDP goes on with the address of the termination that faces where
// m goes. The same holds for an offer and for an answer. Without a
// context, a body, or an audio stream with a port, m is left as it is.
func (p *PCSCF) relaySDP(m *sip.Message, rc *RelayContext, toUE bool) error {
	if rc == nil || len(m.Body) == 0 {
		return nil
	}
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return err
	}
	sender, _, err := description.Audio()
	if err != nil {
		return nil
	}
	cameFrom, shown := agw.Access, rc.Core
	if toUE {
		cameFrom, shown = agw.Core, rc.Access
	}
	relay, err := p.relayOf(*rc)
	if err != nil {
		return err
	}
	ctx, cancel := relayContext()
	defer cancel()
	if err := relay.Modify(ctx, rc.ID, cameFrom, agw.TerminationRequest{Remote: sender}); err != nil {
		return err
	}
	if err := description.SetAudio(shown); err != nil {
		return err
	}
	m.Body = description.Bytes()
	return nil
}

// advertise is the address of a termination as the SDP shows it. A UE
// behind the host's NAT sees the access termination at the published
// address instead.
func (p *PCSCF) advertise(c agw.Context, side agw.TerminationID, ue Association) netip.AddrPort {
	addr := c.Terminations[side].Local
	if side == agw.Access && p.cfg.HostAccess != nil && ue.Source.Addr.Addr() == p.cfg.HostAccess.Gateway {
		return netip.AddrPortFrom(p.cfg.HostAccess.Address, addr.Port())
	}
	return addr
}

// A relay that cannot be reached for a moment, such as one that is
// restarting, still gets its contexts subtracted: subtract tries each
// Subtract this many times, this far apart, before it leaves the
// context in the relay.
const (
	subtractAttempts   = 3
	subtractRetryAfter = time.Second
)

// endDialog ends what the dialog of key holds. A dialog that is gone
// has been ended already, by a request or an event that came first, and
// a Released one has freed what it held when the P-CSCF released it.
//
// When the Store fails, the P-CSCF frees what known holds, the dialog as
// the P-CSCF last read it, since nothing would free it later. The item
// stays in the Store, as that of a dialog whose BYE never passes the
// P-CSCF does.
func (p *PCSCF) endDialog(key DialogKey, known Dialog, log *slog.Logger) {
	d, ok, err := p.store.TakeDialog(context.Background(), key)
	if err != nil {
		log.Warn("the dialog stays in the store: the store failed", "call_id", key.CallID, "error", err.Error())
		d, ok = known, true
	}
	if ok && !d.Released {
		p.free(d, log)
	}
}

// free gives back what a dialog that has ended held outside the
// P-CSCF: its policy session, with which the network frees the bearer
// (TS 29.214 4.4.4), and its relay context.
func (p *PCSCF) free(d Dialog, log *slog.Logger) {
	if d.PolicySession != "" && p.policy != nil {
		p.releasePolicySession(d.PolicySession, log)
	}
	if d.Relay != nil {
		p.subtract(*d.Relay, log)
	}
}

func (p *PCSCF) subtract(rc RelayContext, log *slog.Logger) {
	relay, err := p.relayOf(rc)
	if err != nil {
		log.Warn("the relay context stays", "context", rc.ID, "error", err.Error())
		return
	}
	for attempt := 1; ; attempt++ {
		ctx, cancel := relayContext()
		err := relay.Subtract(ctx, rc.ID)
		cancel()
		if err == nil {
			return
		}
		if attempt == subtractAttempts {
			log.Warn("gave up subtracting the relay context", "relay", rc.Relay, "context", rc.ID, "attempts", attempt, "error", err.Error())
			return
		}
		log.Warn("could not subtract the relay context, trying again", "relay", rc.Relay, "context", rc.ID, "error", err.Error())
		time.Sleep(subtractRetryAfter)
	}
}

// relayOf is the media relay that holds the context rc. A context that
// names no relay was written by a P-CSCF that had one relay only, and so
// is in the one relay, when this P-CSCF lists one. A relay that this
// P-CSCF does not list, such as one taken out of its configuration while
// a call was on it, is an error.
func (p *PCSCF) relayOf(rc RelayContext) (Relay, error) {
	if rc.Relay == "" && len(p.relays) == 1 {
		return p.relays[0].Control, nil
	}
	for _, r := range p.relays {
		if r.Name == rc.Relay {
			return r.Control, nil
		}
	}
	return nil, fmt.Errorf("pcscf: the context %s is in the media relay %q, which this P-CSCF does not list", rc.ID, rc.Relay)
}

// relayContext bounds a request to the relay, so that a relay that
// stalls fails the request instead of holding up the call for good.
// The answer comes long after the offer, so each request gets its own.
func relayContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), agw.RequestTimeout)
}

func equalRoutes(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ua, errA := sip.ParseNameAddr(a[i])
		ub, errB := sip.ParseNameAddr(b[i])
		if errA != nil || errB != nil || !strings.EqualFold(ua.URI, ub.URI) {
			return false
		}
	}
	return true
}
