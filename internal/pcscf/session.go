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

// direction is the direction of a session that an initial INVITE on l
// sets up, in the session measurements (TS 32.409 4.1.2.9, 4.1.2.11).
func (l leg) direction() ims.SessionDirection {
	if l == terminating {
		return ims.TerminatingSession
	}
	return ims.OriginatingSession
}

func (p *PCSCF) ownURI() sip.URI {
	u, _ := ims.LooseRoute(fmt.Sprintf("sip:%s:%d", p.cfg.Host, p.cfg.Port))
	return u
}

// session handles every request but REGISTER. A NOTIFY of the
// P-CSCF's own reg event subscription is for the P-CSCF; any other
// request it routes.
//
// An initial INVITE is a session establishment, which the P-CSCF counts
// on receipt and with the final response that it sends to it, under the
// direction that the leg gives (TS 32.409 4.1.2, metrics.go). The
// response that tells of a success is counted as it goes out, in
// proxyInvite.
func (p *PCSCF) session(st *sip.ServerTransaction) {
	if p.ownRegEventNotify(st.Request) {
		p.receiveRegEvent(st)
		return
	}
	req := st.Request
	out := req.Clone()
	top, popped := sip.PopOwnRoute(out, p.ownURIs()...)
	// The Path URI that the P-CSCF put in the REGISTER marks requests to
	// the UE (5.2.6.2).
	l := originating
	if popped && top.User == "term" {
		l = terminating
	}
	inDialog := ims.IsInDialog(req)
	if req.Method == sip.INVITE && !inDialog {
		p.measurements.sessions.Started(role, l.direction())
		defer func() {
			if status := st.FinalStatus(); status >= 300 {
				p.measurements.sessions.Failed(role, l.direction(), status)
			}
		}()
	}
	if err := sip.DecrementMaxForwards(out); err != nil {
		st.Respond(sip.NewResponse(req, 483, sip.NewTag()))
		return
	}
	log := p.log.With("method", string(req.Method), "call_id", req.Get("Call-ID"))
	if inDialog {
		p.subsequent(st, out, log)
		return
	}
	p.initial(st, out, l, log)
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
			// screen let the request in, so the association was there when
			// the request arrived. It has ended since, or the Admit of the
			// stack is not the P-CSCF's; either way the P-CSCF cannot serve
			// it.
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
		log.Warn("the SDP of the INVITE is not pointed at the media relay", "error", err.Error())
		st.Respond(refusalOf(req, err, sip.NewTag()))
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
	// dialog, such as a NOTIFY, finds none. A request of another dialog
	// of a forked INVITE finds the Dialog of the dialog that its 2xx
	// confirmed, and changes nothing of it (ofAnotherDialog) but the
	// CSeq of its sender. That CSeq is the highest of the sender's
	// requests in any dialog of the INVITE, so that a BYE of the P-CSCF
	// in either dialog takes a number above all of them: the one that
	// ends a later 2xx of the other dialog (crossedCancel) as much as one
	// that releases the call, where a higher number is still in order
	// (RFC 3261 12.2.2).
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
	other := known && d.ofAnotherDialog(st.Request, toUE)
	if other && carriesSDP(out, d.Relay) {
		// The relay context and the policy session of the Dialog serve
		// the dialog that the 2xx confirmed. This SDP cannot go through
		// them without moving the media of that call, and as it came it
		// would take the media past the relay. So the P-CSCF answers the
		// request itself, as for an SDP that the relay does not take
		// (notRelayed), with 500 (RFC 3261 21.5.1), and leaves the
		// Dialog as it is.
		log.Warn("an SDP within another dialog of the forked INVITE: the request goes no further")
		st.Respond(sip.NewResponse(st.Request, 500, ""))
		return
	}
	kept := known && !other
	answer, unanswered, answered := func(*sip.Message) {}, func(*sip.Message, error) {}, func(*sip.Message, error) {}
	if kept && carriesSDP(out, d.Relay) {
		ue := st.Source.Addr.Addr()
		if toUE {
			ue = targets[0].Addr.Addr()
		}
		answer, unanswered, answered, err = p.renegotiate(out, d, toUE, ue, log)
		if err != nil {
			p.notRelayed(st, d, err, log)
			return
		}
	}
	if kept && isTargetRefresh(out.Method) {
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
	unanswered(final, err)
	ims.Finish(st, final, err)
	answered(final, err)
	if kept && out.Method == sip.BYE && byeEndsDialog(final, err) {
		p.endDialog(key, d, log)
	}
}

// renegotiate handles the SDP in out, a request within the dialog d on
// its way to the next hop, such as an UPDATE that tells of reserved
// resources (RFC 3311, RFC 3312) or a re-INVITE that puts the call on
// hold (RFC 3261 14). The SDP of an UPDATE or a re-INVITE is an offer,
// which the 2xx answers (RFC 3311 3, RFC 3261 14.1). That of a PRACK is
// an offer too, or the answer to an offer in the reliable response that
// the PRACK acknowledges (RFC 3262 5); the relay takes either the same
// way. The SDP goes through the relay context of the dialog like the
// first offer. The first function it returns takes each response on its
// way back: with the answer the PCRF gets the changed service
// information in the policy session of the dialog (TS 29.214 A.1,
// 4.4.2), and the Dialog keeps where the relay now sends to each party
// (RelayContext.AccessRemote). An answer in a provisional response that
// is not reliable is no answer (RFC 3261 13.2.1), and moves the relay
// alone. What the PCRF gets is the service information of a session
// whose media are negotiated (TS 29.214 A.1), and the network reserves
// the resources of the media it names; were this SDP sent as that, a
// request that then fails would leave the bearer with the media of an
// offer that the session never took. The INVITE has no earlier media of
// its own to keep, so the P-CSCF does send the answer of an unreliable
// 183 to it (setup.authorize), which gives the early media of the call
// a bearer. The second and the third one are for subsequent to call
// with what ProxyWith returned, before the final response goes on and
// once it has gone. ue is the address of the served UE.
//
// A final response other than 2xx, or none, leaves the session as it was
// before the request (RFC 3261 14.1, RFC 3311 5.3): the party that
// offered takes its media where the last offer and answer said, though
// the P-CSCF told the relay where the offer says when it passed the offer
// on. So the second function has the termination that faces that party
// send there again (goBack). It does so before the response goes on, so
// that the party finds the relay as it was when it sends its next offer,
// which may follow the response at once. An answer that showed in a
// provisional response that is not reliable is no answer (RFC 3261
// 13.2.1), and the termination that faces the other party goes back
// too. When the relay does not take the change back, the termination
// keeps sending where the party no longer takes
// the media, and the P-CSCF releases the dialog once the response has
// gone, as for an answer that the relay did not take. An answer that the
// relay took in a reliable provisional response counts though the
// re-INVITE then fails: that offer and answer are done, and the UAC is to
// bring both parties to one view of the session with a new offer (RFC
// 6141 3.3, 3.4), which goes through the relay as any other.
//
// When the SDP is not pointed at the relay, renegotiate returns the
// error, and the request goes no further: the P-CSCF, as an IMS-ALG, is
// to pass on an SDP of its own with the address of the relay (TS 24.229
// 6.7.2.1), and has none. notRelayed answers the request.
//
// When the relay does not take the answer, the response goes on as it
// came, since it is the final response of the request and the party
// that answered has taken the offer already. The PCRF does not get that
// answer, and the P-CSCF then releases the dialog (TS 24.229 5.2.8.1).
// TS 24.229 names no procedure for this. The nearest is that of 6.2 for
// a 200 whose SDP offer has media that the local policy does not allow:
// the P-CSCF passes it on and, once the ACK with the answer has come,
// releases the session with the BYEs of 5.2.8.1.2. Those carry the
// Reason 488 (5.2.8.1.2 items 1 d, 2, 3 c and 4), as the parties asked
// for media that the network does not allow. Here they asked for
// nothing wrong, and the BYEs carry the 503 of lost resources. As in
// 6.2, the release of a re-INVITE waits for the ACK of its 2xx, at most
// ackWait; that of an UPDATE, which has no ACK, starts once the 2xx has
// gone. Either way no BYE can overtake the 2xx. When the answer that the
// relay did not take came in a 1xx and the re-INVITE then ends with a
// final response other than 2xx, or with none, no ACK passes the P-CSCF
// (RFC 3261 17.1.1.3: the ACK of such a response goes hop by hop), and
// the release goes on once that response has gone. A failure after the
// relay took an answer of the request is that of a retransmitted 2xx to
// a re-INVITE, and changes nothing; once the release is under way, the
// P-CSCF leaves the responses alone.
//
// SIMPLIFIED: TS 29.214 4.4.2. When the PCRF refuses the changed
// service information, the P-CSCF only logs it, and the session stays
// authorised as it was: the far end has given its answer by then. TS
// 24.229 V20.1.0 has a procedure for refused resources only for the
// initial INVITE (5.2.7.2).
func (p *PCSCF) renegotiate(out *sip.Message, d Dialog, toUE bool, ue netip.Addr, log *slog.Logger) (answer func(*sip.Message), unanswered, answered func(final *sip.Message, err error), err error) {
	received := out.Body
	offered, err := p.relaySDP(out, d.Relay, toUE)
	if err != nil {
		return nil, nil, nil, err
	}
	offer := ueSide(received, out.Body, toUE)
	// taken is true once the relay has taken an answer to the offer, and
	// refused once the P-CSCF has set out to release the dialog for one
	// that it did not take. settled is true once the answer that the relay
	// took came in a 2xx or a reliable provisional response: only there is
	// it the answer (RFC 3261 13.2.1). The SDP of a provisional response
	// that is not reliable may show that answer before it comes, and
	// previewed is where the relay then sends to the party that answers.
	// stuck is true once the relay has not gone back after a response
	// other than 2xx.
	authorized, taken, refused, settled, stuck := false, false, false, false, false
	var previewed netip.AddrPort
	unanswered = func(final *sip.Message, err error) {
		// A 2xx comes as final, or, for a re-INVITE, goes through Proxy,
		// which then returns neither a final response nor an error.
		answeredWith2xx := err == nil && (final == nil || final.StatusCode < 300)
		if settled || refused || answeredWith2xx {
			return
		}
		// The session is as it was before the request (RFC 3261 14.1), on
		// both sides when an answer showed in a provisional response only.
		stuck = !p.goBack(d.Relay, facingSender(toUE), offered, log) ||
			!p.goBack(d.Relay, facingSender(!toUE), previewed, log)
	}
	answered = func(final *sip.Message, err error) {
		switch {
		case stuck:
			p.release(d.Key, SDPNotRelayed, false, log)
		case !refused:
		case out.Method != sip.INVITE:
			p.release(d.Key, SDPNotRelayed, false, log)
		case final != nil || err != nil:
			// The 2xx of a re-INVITE goes through Proxy, and so does not
			// come back as final.
			p.releaseNow(d.Key, log)
		}
	}
	answer = func(resp *sip.Message) {
		if refused || resp.StatusCode >= 300 || !carriesSDP(resp, d.Relay) {
			return
		}
		received := resp.Body
		sent, err := p.relaySDP(resp, d.Relay, !toUE)
		if err != nil {
			log.Warn("media relay refused the answer", "error", err.Error())
			if !taken {
				refused = true
				if out.Method == sip.INVITE {
					// The mark is in the Store before the 2xx goes on, so
					// the ACK cannot pass the P-CSCF first.
					p.release(d.Key, SDPNotRelayed, true, log)
				}
			}
			return
		}
		taken = true
		if resp.StatusCode < 200 && !resp.Requires(sip.Option100rel) {
			// The SDP of a provisional response that is not reliable is no
			// answer (RFC 3261 13.2.1). The relay sends where it says, so
			// that early media of the session being renegotiated reaches the
			// party that offered, and goBack takes that back when the request
			// fails. Nothing else of the session follows it: the media that
			// the dialog keeps and that the PCRF has authorised stay those of
			// the last offer and answer that are done, which is what the
			// session carries until this offer is answered.
			previewed = sent
			return
		}
		if !settled && d.Relay != nil {
			_, _, err := p.store.UpdateDialog(context.Background(), d.Key, func(d *Dialog) {
				if d.Relay != nil {
					d.Relay = d.Relay.agreeing(facingSender(toUE), offered).agreeing(facingSender(!toUE), sent)
				}
			})
			if err != nil {
				// The response goes on; a later offer that gets no 2xx puts
				// the relay back where the offer and answer before this one
				// said.
				log.Warn("the dialog keeps the offer and answer before this one: the store failed", "error", err.Error())
			}
		}
		settled = true
		// A 2xx to a re-INVITE comes again until its ACK arrives.
		if authorized || d.PolicySession == "" || p.policy == nil {
			return
		}
		authorized = true
		_, err = p.policy.AuthorizeMedia(context.Background(), policy.MediaRequest{
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
	return answer, unanswered, answered, nil
}

// goBack has side, the termination of rc that faces the party of an
// offer that got no 2xx, send where the last offer and answer said, when
// that offer told it to send elsewhere, to offered (renegotiate). It is
// Configure AGW Connection Point (TS 29.334 5.17.2.3) like the Modify of
// the offer, and reports false when the relay did not take the change
// back. A Dialog that keeps no last offer and answer, as one of a P-CSCF
// that kept none, leaves the relay as it is.
func (p *PCSCF) goBack(rc *RelayContext, side agw.TerminationID, offered netip.AddrPort, log *slog.Logger) bool {
	if rc == nil {
		return true
	}
	agreed := rc.agreed(side)
	if !offered.IsValid() || !agreed.IsValid() || offered == agreed {
		return true
	}
	relay, err := p.relayOf(*rc)
	if err == nil {
		ctx, cancel := relayContext()
		err = relay.Modify(ctx, rc.ID, side, agw.TerminationRequest{Remote: agreed})
		cancel()
	}
	if err != nil {
		log.Warn("the media relay does not go back to the last offer and answer: the P-CSCF releases the dialog",
			"context", rc.ID, "termination", string(side), "error", err.Error())
		return false
	}
	log.Info("the media relay sends where the last offer and answer said again", "context", rc.ID, "termination", string(side), "remote", agreed.String())
	return true
}

// notRelayed answers st, a request within the dialog d whose SDP
// renegotiate could not point at the relay for err, and releases the
// dialog when the call cannot go on as it was.
//
// The P-CSCF answers the request itself, as TS 24.229 6.2 has it answer
// a request whose offer it cannot let through. A final response other
// than 2xx leaves the session as it was before the request (RFC 3261
// 14.1, RFC 3311 5.3), and only a 481 or a 408 ends the dialog (RFC 3261
// 12.2.1.2). The answer is that of refusalOf: for a body that the P-CSCF
// cannot read 400 or 415, and otherwise 500 (21.5.1) rather than the 488
// of 6.2. That one tells of media that the network does not allow, and
// carries the media that it does, and these media are allowed.
//
// The dialog stays only when the call can go on as it was: when the
// relay was not asked, as for a body that cannot be read, or when it
// answered that it carried out nothing (agw.ErrRejected). The media then
// still take the path of the last offer and answer. Otherwise the
// P-CSCF releases the dialog, as it does when the relay does not take an
// answer:
//   - The relay has lost the context or its termination, or the P-CSCF
//     lists no relay that holds it (mediaGone). The media of the call are
//     gone already, and every later offer would fail the same way.
//   - The relay did not answer in time, or gave an answer that the P-CSCF
//     cannot read. It may have carried the Modify out, and the
//     termination may now send where the offer said, which the party
//     that offered no longer takes after the non-2xx.
//   - The request is a PRACK. Its UAC does not send it again (RFC 3262
//     4), so the reliable response stays unacknowledged, and the UAS
//     ends the INVITE with a 5xx after 64*T1 (RFC 3262 3). The P-CSCF
//     ends it now, as for a refused answer in the early dialog (TS
//     24.229 5.2.8.1.1). The PRACK gets the 500 too, not the 2xx that
//     RFC 3262 3 asks for a PRACK that matches its reliable response:
//     every INVITE whose call goes through the relay has the offer, and
//     once the UAS has answered it, no response to the INVITE carries an
//     offer (RFC 3261 13.2.1). The SDP of a PRACK in the early dialog is
//     then a new offer, and a 2xx to it would have to carry the answer
//     (RFC 3262 5), which the P-CSCF does not have.
func (p *PCSCF) notRelayed(st *sip.ServerTransaction, d Dialog, err error, log *slog.Logger) {
	resp := refusalOf(st.Request, err, "")
	keep := !mediaGone(err) && (errors.Is(err, errBody) || errors.Is(err, agw.ErrRejected))
	if keep && st.Request.Method != sip.PRACK {
		log.Warn("the SDP of the request is not pointed at the media relay: the request goes no further", "status", resp.StatusCode, "error", err.Error())
		st.Respond(resp)
		return
	}
	log.Warn("the SDP of the request is not pointed at the media relay: the P-CSCF releases the dialog", "status", resp.StatusCode, "error", err.Error())
	st.Respond(resp)
	p.release(d.Key, SDPNotRelayed, false, log)
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

// OnCANCEL forwards a CANCEL that matches no transaction statelessly
// (RFC 3261 16.10), the way an ACK goes.
func (p *PCSCF) OnCANCEL(cancel *sip.Message) { p.forwardCANCEL(&p.stack, cancel) }

// withoutTransaction is m, an ACK for a 2xx or a CANCEL, which no
// transaction of the P-CSCF holds, as it goes on to the top Route or
// else the Request-URI, and where that is. toUE reports that it goes to
// the UE.
func (p *PCSCF) withoutTransaction(m *sip.Message) (out *sip.Message, targets []sip.Destination, toUE bool, err error) {
	out = m.Clone()
	if err := sip.DecrementMaxForwards(out); err != nil {
		return nil, nil, false, err
	}
	sip.PopOwnRoute(out, p.ownURIs()...)
	if targets, toUE, err = p.nextHop(out); err != nil {
		return nil, nil, false, err
	}
	if toUE {
		crossBoundary(out, requestToUE)
	} else {
		crossBoundary(out, requestFromUE)
	}
	return out, targets, toUE, nil
}

// forwardCANCEL forwards cancel from the stack it came to.
//
// SIMPLIFIED: RFC 3261 16.10. A CANCEL that the P-CSCF cannot forward is
// dropped, with no response: one whose Max-Forwards is used up (16.3
// step 3), and one whose next hop DNS does not find.
func (p *PCSCF) forwardCANCEL(ref *ims.StackRef, cancel *sip.Message) {
	stack, err := ref.Get()
	if err != nil {
		return
	}
	out, targets, _, err := p.withoutTransaction(cancel)
	if err == nil {
		err = stack.SendCANCEL(out, targets...)
	}
	if err != nil && !errors.Is(err, sip.ErrStackClosed) {
		p.log.Warn("cannot forward CANCEL", "call_id", cancel.Get("Call-ID"), "error", err.Error())
	}
}

// forwardACK forwards ack from the stack it came to. It notes in the
// Dialog that the 2xx has its ACK, and sends the BYEs of a release that
// waited for that ACK (release). RFC 3261 16.3 step 3 answers 483 to a
// request whose Max-Forwards is used up, but no response to an ACK is
// allowed (17), so such an ACK is only dropped.
func (p *PCSCF) forwardACK(ref *ims.StackRef, ack *sip.Message) {
	stack, err := ref.Get()
	if err != nil {
		return
	}
	out, targets, toUE, err := p.withoutTransaction(ack)
	if err != nil {
		p.log.Warn("cannot forward ACK", "call_id", ack.Get("Call-ID"), "error", err.Error())
		return
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
	// Only a confirmed dialog waits for an ACK, and its BYEs are the
	// same whatever the release is for.
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
// sender is where the termination was told to send, and is not valid
// when m is left as it is.
func (p *PCSCF) relaySDP(m *sip.Message, rc *RelayContext, toUE bool) (sender netip.AddrPort, err error) {
	if rc == nil || len(m.Body) == 0 {
		return netip.AddrPort{}, nil
	}
	if err := sdpBodyError(m); err != nil {
		return netip.AddrPort{}, err
	}
	if !sip.HasSessionDescription(m) {
		return netip.AddrPort{}, nil
	}
	description, err := sdp.Parse(m.Body)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %w", errMalformedSDP, err)
	}
	sender, _, err = description.Audio()
	if err != nil {
		return netip.AddrPort{}, nil
	}
	shown := rc.Core
	if toUE {
		shown = rc.Access
	}
	relay, err := p.relayOf(*rc)
	if err != nil {
		return netip.AddrPort{}, err
	}
	ctx, cancel := relayContext()
	defer cancel()
	if err := relay.Modify(ctx, rc.ID, facingSender(toUE), agw.TerminationRequest{Remote: sender}); err != nil {
		return netip.AddrPort{}, err
	}
	if err := description.SetAudio(shown); err != nil {
		return netip.AddrPort{}, err
	}
	m.Body = description.Bytes()
	return sender, nil
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
	switch {
	case err != nil:
		log.Warn("the dialog stays in the store: the store failed", "call_id", key.CallID, "error", err.Error())
		d, ok = known, true
	case ok && d.Confirmed:
		// The session that the 2xx set up has ended (remember).
		p.measurements.sessions.Ended(role)
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
	return nil, fmt.Errorf("%w: the context %s is in the media relay %q", errRelayNotListed, rc.ID, rc.Relay)
}

// errRelayNotListed is the error of relayOf for a context in a media
// relay that this P-CSCF does not list.
var errRelayNotListed = errors.New("pcscf: the media relay is not in the list of this P-CSCF")

// mediaGone reports whether err, an error of relaySDP, means that the
// relay holds no media of the call any more: it has no context or
// termination of that ID, as after a restart that lost its store, or
// the P-CSCF lists no relay that holds the context.
func mediaGone(err error) bool {
	return errors.Is(err, agw.ErrNoContext) || errors.Is(err, agw.ErrNoTermination) || errors.Is(err, errRelayNotListed)
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
