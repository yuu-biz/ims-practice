# When a media renegotiation fails

A re-INVITE or an UPDATE in a call carries a new offer that moves the media. The P-CSCF tells the media relay to send where the offer says before it passes the offer on. But when the offer ends with a final response other than 2xx, the SIP session stays as it was before the request (RFC 3261 14.1, RFC 3311 5.3). Only the relay sends to the new address.

This implementation answers as follows. Before the P-CSCF passes the non-2xx response on to the party that offered, it puts the relay back where the last offer and answer that were done said. When it cannot put the relay back, or cannot tell whether the relay carried out its change, the SIP agreement and the media path stay apart. The P-CSCF then ends the call with a BYE to each party.

This chapter splits the failures into four: the offer is turned down, no response comes, the relay may or may not have made the change, and the change back itself fails. It then compares three ways: put the relay back first, end the call, and keep the new setting.

## The setup and what this chapter assumes

The example is the `deploy/compose` setup, where the P-CSCF is the IMS-ALG that controls the media relay (the IMS-AGW). The P-CSCF has the relay make a context per call with `Add`, tells each termination where to send with `Modify`, and has the relay release the context with `Subtract`. The access termination faces the UE, and the core termination faces the network. The IBCF and the TrGW of `deploy/interconnect` follow the same rules; the second half of the chapter says where they differ.

Offer and answer, re-INVITE, reliable provisional responses and PRACK are in [Transactions, dialogs and media negotiation in a call](call-signaling.md). The IBCF and the TrGW are in [Interconnection between networks](interconnect.md), and the store that the P-CSCF instances share is in [What several nodes and failures hand over](design-shared-state.md).

The specifications this chapter cites are RFC 3261, RFC 3262, RFC 3264, RFC 3311, RFC 6141, TS 24.229 V18.11.0 and TS 29.214 V20.0.0. The ports A and B in the tables are made up for the explanation, not values of a capture.

## Why the agreement and the setting of the relay do not settle together

The SIP agreement is made when the answer arrives. The answer to the offer of a re-INVITE comes in a 2xx or in a reliable provisional response (RFC 3261 13.2.1, RFC 3262 5). The setting of the relay changes when the P-CSCF sends `Modify`, which is another request to another node. The two happen at different nodes and at different times.

The P-CSCF does not pass the offer on as it came. The IMS-ALG makes an offer of its own that names the address of the relay (TS 24.229 6.7.2.1). At that point this implementation also tells the termination that faces the party that offered, with `Modify`, to send to the address of the offer. So from the moment the offer passes the P-CSCF until the response comes back, the relay is one step ahead of the agreement.

For the party that offered, this lead does no harm. RFC 3264 8.3.1 has a party that offers a new port be ready to receive on both the old and the new port from the moment it sends the offer. The trouble starts when the offer is turned down. The same clause lets that party stop being ready on the new port as soon as it receives the rejection. If the relay goes on sending to the new port after the rejection reaches the UE, the UE may not take that media. One direction of the audio is lost, with no error in the signalling.

## Three states kept apart

To reason about this, three things must be kept apart.

| State | What it is | Where the P-CSCF keeps it | Where the IBCF keeps it |
| --- | --- | --- | --- |
| The last offer and answer that are done | Where each party takes its media, as far as SIP is concerned | `AccessRemote` and `CoreRemote` in the record of the dialog, in the store that the instances share | `agreed` of the call, in the memory of the instance |
| The offer in progress | The address of an offer that has gone out and has no answer yet | `offered` in the handling of that request; the SDP of a provisional response that is not reliable in `previewed` | The request itself, and `previews` |
| The current setting of the relay | Where each termination really sends | Only inside the relay; the P-CSCF cannot ask | Inside the TrGW; the IBCF keeps a copy in `sends` |

Take a call in which the UE takes its media at port A. The UE sends a re-INVITE that moves it to port B, and the far end turns it down with 488. In time order, for the access termination only:

| Moment | Last done | In progress | Setting of the relay |
| --- | --- | --- | --- |
| Before the re-INVITE | A | none | A |
| The re-INVITE has passed the P-CSCF | A | B | B (`Modify` for the offer) |
| The 488 has reached the P-CSCF | A | B (ends here) | B → A (`Modify` back) |
| The 488 has reached the UE | A | none | A |

The order of the third and fourth rows matters. The relay goes back before the 488 goes on to the UE. The UE may send its next offer right after the 488, and by the time that offer arrives, the relay is back.

At the P-CSCF the addresses of the last offer and answer are in the record of the dialog, `RelayContext`.

[internal/pcscf/dialog.go](../../../internal/pcscf/dialog.go)

```go
	// AccessRemote and CoreRemote are where the access and the core
	// termination send for the last offer and answer that are done: where
	// the UE, and the party on the network side, take their media. A
	// request whose offer gets no 2xx leaves the session as it was before
	// the request (RFC 3261 14.1, RFC 3311 5.3), and the termination that
	// the offer moved goes back there (goBack). The relay cannot be asked,
	// and the next request of the dialog may pass another P-CSCF
	// instance, so the Dialog keeps them. Not valid in a Dialog of a
	// P-CSCF that kept neither.
	AccessRemote netip.AddrPort
	CoreRemote   netip.AddrPort
```

The comment gives two reasons for keeping these values in the store. The operations that control the relay are only `Add`, `Modify` and `Subtract`, and none of them reads the current setting. And the next request of the same dialog may pass another P-CSCF instance. So that whichever instance handles the response can put the relay back, the target of the change back lives outside the instance.

These values change only when an answer is done, right after the P-CSCF has put the SDP of a response through the relay. That is also the only time at which the PCRF hears of the changed media.

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
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
```

The inputs are the response to the request, `resp`, and `sent`, where the party that answers takes its media, as the relay was told. The SDP of a provisional response that is not reliable is no answer (RFC 3261 13.2.1); it only shows the answer early. The relay already sends where it says, so that early media of the session being renegotiated reach the party that offered, and the address goes into `previewed`, so that it can be taken back. Nothing else follows it: the function returns there, the addresses that are done stay those of the last offer and answer, and the PCRF hears nothing. For a 2xx or a reliable provisional response, the address of the offer, `offered`, and that of the party that answered, `sent`, are written to the store as the ones that are done, and `settled` becomes true. When that write fails, the response still goes on. A later offer that is turned down then puts the relay back where the offer and answer before this one said. That is one limit of this implementation.

Only after that does the PCRF get the changed service information, in the policy session of the dialog (TS 29.214 A.1, 4.4.2). That information describes the media of a session whose offer and answer are done, and the network reserves the resources of the media it names. If the P-CSCF sent it for the SDP of a 183 that is not reliable, a request that then fails would leave the bearer reserved for the media of an offer that the session never took. `authorized` keeps the 2xx of a re-INVITE, which comes again until its ACK arrives, from sending the same information twice. The initial INVITE is different: it has no earlier media to keep, so there the P-CSCF does send the answer of a 183 that is not reliable to the PCRF, which gives the early media of the call a bearer ([Registration and calls over VoLTE](volte.md#the-aar-for-the-media-of-a-call)).

## What the standards fix, and what they leave open

What the standards fix is the state of the session.

- After a final response other than 2xx, the session is as it was before the request (RFC 3261 14.1 for a re-INVITE, RFC 3311 5.3 for an UPDATE).
- After a 408 or a 481, or when no response comes at all, the UAC should end the dialog (RFC 3261 12.2.1.2, repeated in 14.1 and RFC 3311 5.3).
- The answer comes in a 2xx or in a reliable provisional response. The same answer may also be in provisional responses before it (RFC 3261 13.2.1).
- A UAS that has already carried out changes within a re-INVITE should return a 2xx, not an error response (RFC 6141 3.3). A UAC that still gets an error response sends a new offer, so that both parties have one view of the session (RFC 6141 3.4).
- A UE that uses preconditions gives up a change of the session with a CANCEL when the network cannot reserve the resources it needs (TS 24.229 6.1.4.2 item b). A cancelled re-INVITE also ends with a response other than 2xx.

What they leave open is the handling of the relay. TS 24.229 6.7.2.1 has the IMS-ALG make a new offer with the address of the relay when it receives one, but names no step for an offer that is then turned down. Nor does it say whether the relay is told on the offer or on the answer, or what to do when the relay does not answer. These are choices of this implementation.

The way to end a call is in TS 24.229. How the P-CSCF builds the BYEs that release an existing session is in 5.2.8.1.2. But 5.2.8.1.2 names three triggers for the release: radio or bearer resources that are lost, a signalling bearer that is lost, and an SDP offer in a response that the local policy does not allow. A failure of the relay is not among them. Using the BYEs of 5.2.8.1.2 in this case is the choice of this implementation. The IBCF has a clause of its own: an IBCF that controls the transport plane may send a BYE to each side when the TrGW or another node tells it of an error of the transport plane (5.10.2.4, 5.10.3.4).

## When the offer is turned down

A far end that turns the offer down with 488, and a UE that cancels so that the far end answers 487, look the same to the P-CSCF: a final response other than 2xx. After it has forwarded a request within a dialog, the P-CSCF handles the final response in this order.

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
	final, err := st.ProxyWith(out, relay, targets...)
	if final != nil {
		// Proxy passes on a 2xx to an INVITE itself, and leaves every
		// other final response to its user.
		relay(final)
	}
	unanswered(final, err)
	ims.Finish(st, final, err)
	answered(final, err)
```

`unanswered` puts the relay back, `ims.Finish` passes the response on to the party that offered, and `answered` releases the call when it has to. The change back comes before the response goes on. `unanswered` is:

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
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
```

It does nothing in three cases: the request ended with a 2xx; an answer was already done (`settled`); or the P-CSCF had already decided to release the call (`refused`). The second is the case of a reliable 183 to the re-INVITE that carried the answer: that offer and answer are done. RFC 6141 3.3 has a UAS that has carried out such a change return a 2xx, but the re-INVITE may still end with 488. Nothing goes back then either: each termination keeps sending where that offer and answer said, and the PCRF keeps the media of that answer. What brings the two parties to one view is the next offer of the UAC (RFC 6141 3.4).

Otherwise the termination that faces the party that offered goes back from `offered`. The other termination, which the SDP of a provisional response that is not reliable may have moved, goes back from `previewed`. The PCRF was told nothing of the offer or of that SDP, so nothing goes back there: what the P-CSCF last gave the PCRF is still the media of the last offer and answer that are done. When either termination does not go back, `stuck` becomes true.

The change back is `goBack`.

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
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
		err = relay.Modify(ctx, rc.ID, side, agw.TerminationRequest{Remote: agreed, Latch: relatching(side, offered, agreed)})
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
```

The inputs are the relay context of the dialog's record, `rc`, the termination to put back, `side`, and the address the offer moved it to, `offered`. The first condition leaves out the cases with nothing to undo. When the offer did not change the address, as for a hold that keeps the port, `offered` equals the address that is done, and no `Modify` goes out. Only when it is needed does the P-CSCF send one `Modify` with the address that is done as `Remote`. Going back moves the address as much as the offer did, so for the access termination it asks for latching again too, as for the offer (`relatching`): a termination that has latched does not change where it sends for a new `Remote` alone. It follows the packets of the UE, which is back on the address from before the offer (`TestRejectedOfferAsksTheAccessTerminationToLatchBack` in `internal/pcscf/latch_test.go`). Like the `Modify` of the offer, it waits for its answer within the bound on each request to the relay, `agw.RequestTimeout`, 2 seconds.

There is a second way for an offer to be turned down: the relay itself refuses the `Modify` of the offer while the offer passes the P-CSCF. What an error answer of the relay means is fixed by the control API.

[internal/agw/api.go](../../../internal/agw/api.go)

```go
// ErrRejected marks the error with which a media node answered a
// request, over the control API or in an H.248 Error descriptor. A
// media node of this project carries out none of a request that it
// answers with an error, so its controller knows that nothing changed.
// Any other error, such as a request that got no answer in time, leaves
// the controller unable to tell whether the node carried it out.
var ErrRejected = errors.New("agw: the media node rejected the request")
```

A media node of this project carries out nothing of a request that it answers with an error. So after an `ErrRejected` error, the controller knows that nothing changed. The P-CSCF cannot make an offer that names the relay, so it does not pass the request on and answers it 500 itself (RFC 3261 21.5.1). The relay changed nothing, so there is nothing to put back either. The call goes on with the last offer and answer.

## When no response comes

When a re-INVITE or an UPDATE gets no final response, the session is also as it was before the request. At the P-CSCF, `unanswered` gets the error `err` in place of a final response. `answeredWith2xx` is false, so the relay goes back as it does for a rejection. Then `ims.Finish` answers the party that offered with 504 (Server Time-out). The CSCFs of this implementation answer 504 to a request that the next node never answered.

Whether the call goes on depends on what the party that offered receives.

- A 504 is an ordinary response other than 2xx. The session is as before, and the dialog goes on.
- When its own transaction times out, or when it receives a 408 or a 481, the UAC ends the dialog (RFC 3261 12.2.1.2). The UA of this implementation then sends a BYE (`lost` in `internal/sip/indialog.go`).

Either way, the P-CSCF has already put the relay back. When the call goes on, the media flow as the last offer and answer said. When it ends, the P-CSCF releases the context as the BYE passes it.

## When it is not known whether the relay made the change

The relay may not answer the `Modify` of an offer in time, or give an answer that the P-CSCF cannot read. The relay may then have carried the `Modify` out. If it did, the termination now sends to an address that the party that offered will no longer take. The relay may also answer that it has lost the context or the termination of the call, as a relay that restarted without its store does. The media of the call then no longer pass through the relay at all.

The P-CSCF decides how to answer the request, and whether the call stays, in `notRelayed`.

[internal/pcscf/session.go](../../../internal/pcscf/session.go)

```go
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
```

The input is why the offer could not be put through the relay, `err`. The call stays (`keep`) only when the relay is known to have changed nothing: when the P-CSCF could not read the SDP and asked the relay nothing (`errBody`), or when the relay answered with an error (`ErrRejected`). An error that says the context or the termination is gone (`mediaGone`) is left out, even when the relay answered with it: the media are gone, and every later offer would fail the same way. A PRACK is left out too. The UAC does not send a failed PRACK again (RFC 3262 4), so the reliable response would stay unacknowledged.

In every other case the P-CSCF answers the request, with 500 or the like, and then releases the call: a BYE to each party, and the release of the PCRF session and of the relay context.

When the relay does not take the answer in a response, the call ends too, but in another order. Nobody can refuse an answer: the party that answered has already taken the offer. The P-CSCF passes the 2xx on as it came, and sends the BYEs after the ACK for a re-INVITE, or after the 2xx for an UPDATE.

The IBCF draws the same line.

[internal/ibcf/call.go](../../../internal/ibcf/call.go)

```go
func callStays(err error) bool {
	switch {
	case errors.Is(err, errMalformedSDP), errors.Is(err, errNotKept):
		return true
	case errors.Is(err, agw.ErrNoContext), errors.Is(err, agw.ErrNoTermination):
		return false
	}
	return errors.Is(err, agw.ErrRejected)
}
```

What only the IBCF has is `errNotKept`: the TrGW reserved the context, but the IBCF could not write the record of the call that keeps it to the store. The context stays with the call, so the call can go on.

## When the change back itself fails

The far end turns the offer down, and the P-CSCF sends the `Modify` that puts the relay back, but the relay does not take it. The termination keeps sending to an address that the party that offered no longer takes. `goBack` returns false, and `unanswered` sets `stuck`.

The P-CSCF first passes the non-2xx response on to the party that offered, since the response itself is right. Then `answered` sees `stuck` and releases the call. Each party gets a BYE, and the PCRF session and the relay context are released.

## The failure cases side by side

The table lists the four failures and the cases close to them.

| Failure | Setting of the relay | Where it goes back to | The call |
| --- | --- | --- | --- |
| The far end turned the offer down (488, the 487 of a cancel, ...) | Moved to the address of the offer | The last that is done, before the response goes on | Goes on |
| The relay refused the `Modify` of the offer (not a PRACK) | Unchanged | Nothing; the P-CSCF answers 500 and the request goes no further | Goes on |
| No final response | Moved to the address of the offer | The last that is done; then 504 | Goes on after a 504; the UAC ends it after its own timeout, a 408 or a 481 |
| The relay did not answer in time, or its answer cannot be read | Not known | Cannot be decided | The P-CSCF answers 500 and sends a BYE to each party |
| The relay has lost the context | No media | Nothing | The P-CSCF answers 500 and sends a BYE to each party |
| The relay does not take the answer in a response | Not known on the side that answered | Cannot be decided | The 2xx goes on, then a BYE to each party |
| The `Modify` that puts the relay back fails | Still at the address of the offer | Could not go back | The non-2xx goes on, then a BYE to each party |

The call goes on when the P-CSCF knows that the setting of the relay matches the last offer and answer. When it cannot make them match, or cannot tell whether they match, it ends the call.

## Three ways compared

There are several ways to handle the relay after an offer is turned down. None of them breaks the rules of the standards above. They differ in what they keep and what they give up.

| Way | What it keeps | What it gives up or needs | When it fits |
| --- | --- | --- | --- |
| Put the relay back first (this implementation) | The call, with the media as SIP agreed | One more request to the relay on a failure, and the response waits for its answer (at most 2 s). The addresses that are done, readable by whichever instance handles the response | The relay answers, and says so when it did nothing. The controller can keep the addresses that are done per dialog |
| End the call | Media never stay apart from the agreement. No addresses to keep | An ordinary rejection or cancel drops the call: a response that in SIP means "nothing changed" becomes a hang-up | Only when the setting of the relay is not known, or cannot be put back |
| Keep the new setting | No more requests to the relay | When the party that offered stops taking media at the new address, one direction of the audio is lost silently | When the offer did not change the address, or when the termination sends to the source of the packets it gets rather than to the SDP |

**Ending the call** does not suit every failure. A UE that uses preconditions cancels a re-INVITE whose resources could not be reserved (TS 24.229 6.1.4.2 item b). If each such 487 dropped the call, a lack of resources would reach the user as a call cut off. This implementation ends the call only when the state of the relay does not match the agreement, or when it cannot tell.

**Keeping the new setting** is safe only in a few cases. When the offer did not change the address, there is nothing to undo, and `goBack` sends no `Modify`. A relay that sends to the source of the packets it gets (latching) depends less on the address in the SDP. Even so, the access termination does not move to a new source by itself once it has latched; it moves only when the P-CSCF asks it to latch again. And the core termination of this implementation does not latch: it sends to the address of the latest SDP. On that termination, which faces the network, putting the relay back is what keeps the audio right.

A further way is to **change the relay only when the answer comes**. The relay is left alone on the offer, and both terminations change as the answer passes. Nothing has to go back after a rejection. RFC 3264 8.3.1 has the party that offered keep taking media at the old port until it has the answer and media arrive at the new one, so sending to the old address in the meantime also follows the rules. In exchange, the failures of the relay all fall on the moment the answer passes. Nobody can refuse an answer, so a relay that fails then leaves nothing but ending the call. When the relay moves on the offer, most of its failures cost only a 500 to the offer. This implementation does not do this for the relay, but it does for the PCRF: the P-CSCF tells the PCRF of the changed media neither on the offer nor on the SDP of a provisional response that is not reliable, but only when the answer comes in a 2xx or a reliable provisional response. So an offer that is turned down leaves nothing to undo at the PCRF, even after a 183 that showed its answer early. For the PCRF the trade differs from that of the relay. Moving the relay early lets the early media of the renegotiated session through and leaves most failures of the relay at the offer, while telling the PCRF early would reserve resources for media that nobody has agreed.

The controller could also ask the relay for its setting before it decides, which would leave fewer cases unknown. But the control operations of this implementation have none that reads the setting of a termination, and the response would wait for the question as well.

## The choice of this implementation and its limits

**The P-CSCF.** It moves the relay when the offer passes, and puts it back before a non-2xx response goes on. It tells the PCRF of the changed media only once the answer is done, so a failed renegotiation leaves the PCRF with the media of the last offer and answer that are done. It keeps the addresses that are done with the record of the dialog, in the store that the instances share. When a re-INVITE passes the first instance and the next UPDATE the second, the second can put the relay back. The limits are these.

- When the addresses that are done cannot be written to the store, a later offer that is turned down puts the relay back where the offer and answer before them said.
- When the relay does not answer, the P-CSCF has no way to check the state of the relay. It ends the call.
- The BYEs that end a call for a failure of the relay follow 5.2.8.1.2, but that clause does not name this trigger. It is the choice of this implementation. The Reason of the BYE is `SIP;cause=503;text="Service Unavailable"`.
- When the PCRF refuses the changed media, the P-CSCF only logs it, and the call stays authorised as before (a `SIMPLIFIED:` in the code; TS 29.214 4.4.2).

**The IBCF.** The rules are those of the P-CSCF: it puts the TrGW back before a non-2xx response goes on, and releases the call when it cannot. What differs is where the addresses that are done are kept. The IBCF is a B2BUA, and the two dialogs of a call are in the process of the instance that took its INVITE. The requests within the call always come back to that instance, so its memory is enough for those addresses too. When the instance stops, the call stops with it, and as long as the store remains, the instance that starts again under the same name ends the call from its record there ([Interconnection between networks](interconnect.md) and [Taking a node out for maintenance, and losing it](design-maintenance.md)).

**Offers at the same time.** The two parties may each send an offer, in two requests. When one request fails, the IBCF puts back only the terminations that this request moved (`movedFor`). A termination that the other request moved waits for the outcome of that request.

## What this explanation rests on

The explanation of this chapter was checked against the following code and tests. Each test was run and passes as a Go test of this repository. The parties and the relays of the tests are test doubles, and the tests drive the flow with the responses those give.

- `TestRejectedOfferPutsTheRelayBack` in `internal/pcscf/rollback_test.go`. For an UPDATE and a re-INVITE, and an offer from either party, the termination sends to the old address again after the 488, and the call stays up.
- `TestCancelledReInvitePutsTheRelayBack` in the same file. While the re-INVITE is pending the relay sends to the address of the offer; after the 487 of the cancel it is back at the old address; the Reason of the CANCEL reaches the far end.
- `TestRejectedOfferGoesBackToTheLastOfferAndAnswer` in the same file. The relay goes back to the offer and answer of the last re-INVITE that got a 2xx, not to those of the INVITE.
- `TestRejectedOfferAfterAPreviewPutsBothSidesBack` in the same file. Both terminations go back, the one that a 183 that is not reliable moved included.
- `TestRejectedOfferEndsTheCallWhenTheRelayDoesNotGoBack` in the same file. When the `Modify` back fails, each party gets a BYE after the 488 has gone on, and the PCRF session and the context are released.
- `TestRejectedOfferAfterAPreviewKeepsTheAuthorisedMedia` in `internal/pcscf/renegotiate_test.go`. The far end shows its answer to a re-INVITE in a 183 that is not reliable and then answers 488. The PCRF still authorises the media of the INVITE, and the relay holds the one context of the call. The next offer, answered in a 200, has the PCRF authorise the media of that answer.
- `TestAnswerAfterAPreviewIsAuthorisedOnce` in the same file. After a 183 that is not reliable, the far end answers in its 200. The PCRF gets nothing for the 183, and the media of the 200 once.
- `TestRejectedOfferAfterAReliableAnswerKeepsTheNewMedia` in the same file. The far end answers a re-INVITE in a reliable 183, which the UE acknowledges with a PRACK, and then answers 488. The PCRF keeps the media of that answer, and each termination keeps sending where that offer and answer said.
- `TestRefusedOfferWithinTheDialogIsRejected` in `internal/pcscf/refusedoffer_test.go`. When the relay refuses the `Modify`, the P-CSCF answers 500 and passes nothing on, the call stays up, and the next offer goes through.
- `TestOfferWithinTheDialogEndsTheCallWhenTheRelayMayHaveTakenIt` in the same file. When the relay does not answer in time (the test relay has carried the change out), or answers that it has lost the context, the request gets 500 and each party a BYE.
- `TestRefusedAnswerWithinTheDialogReleasesTheCall` in `internal/pcscf/refusedanswer_test.go`. When the relay does not take the answer of a response, the BYEs come after the 200 has gone on (after the ACK for a re-INVITE).
- `TestRejectedOfferPutsTheTrGWBack`, `TestRejectedOfferEndsTheCallWhenTheTrGWDoesNotGoBack`, `TestCancelledReInvitePutsTheTrGWBack`, `TestRejectedOfferAfterAPreviewGoesBackToTheLastOfferAndAnswer` and `TestRejectedOfferAfterAReliableAnswerKeepsIt` in `internal/ibcf/rollback_test.go`. The same rules hold for the IBCF and the TrGW, and a 488 after an answer that a reliable 183 completed puts nothing back.
- `TestA481WithinTheDialogEndsTheCall` in `internal/sip/indialog_test.go` and `TestPRACKOfAReINVITEThatGets481EndsTheCall` in `internal/sip/reinviteprack_test.go`. A request within a dialog that gets 481 has the UA end the call with a BYE. No test times out a re-INVITE itself; what the UA does then is read from the code that calls `lost`, cited above.

## If you want to run it yourself

The usual setup of the guides has no far end that turns an offer down and no relay that fails to answer. The tests above run these flows, for example `go test ./internal/pcscf -run 'TestRejectedOffer|TestRefusedOffer|TestCancelledReInvite'`. Sequence diagrams of the signalling are in the guide sections [When the relay does not take an offer](../../guide/en/call.md#when-the-relay-does-not-take-an-offer) and [When the far end turns an offer down](../../guide/en/call.md#when-the-far-end-turns-an-offer-down), and for interconnection in [A re-INVITE within the call](../../guide/en/interconnect.md#a-re-invite-within-the-call) and [When the TrGW does not take an SDP](../../guide/en/interconnect.md#when-the-trgw-does-not-take-an-sdp).

The next chapter looks at what happens to the work in progress when a node is taken out of service, and when it is lost. [Taking a node out for maintenance, and losing it](design-maintenance.md)
