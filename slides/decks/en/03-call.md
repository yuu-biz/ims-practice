---
title: A voice call through the IMS
footer: ims-practice study session 3. Call
lang: en
---

# A voice call through the IMS

ims-practice study session 3 (a call through the media relay, with preconditions and the MMTel AS on its way)

---

[layout: summary]

## What you will know after this session

To begin: does UE 1 know the IP address of UE 2? It does not. It knows a telephone number.

- How a telephone number becomes a SIP address.
- How each CSCF finds the next one.
- How the two UEs agree on the media.
- How the voice flows through the media relay.

> Guide: [Follow a voice call through the IMS](../../../docs/guide/en/call.md) (`docs/guide/en/call.md`)
> The editions are those of Release 18, TS 24.229 V18.11.0 and TS 29.228 V18.0.0, and the RFCs named in each step.

---

## What the call takes over from registration

A call uses three things that registration set up.

| Set up by registration | Kept by | Used in the call |
| --- | --- | --- |
| Service-Route | UE | To send the UE's requests to its S-CSCF. |
| Path | S-CSCF | To send requests for the UE to its P-CSCF. |
| IP association | P-CSCF | To check that a request comes from the registered UE. |

- Both UEs register before the call, as in the last session. Their REGISTERs open the capture.

> Guide: [registration, "10. The S-CSCF accepts: 200 OK"](../../../docs/guide/en/registration.md#10-the-s-cscf-accepts-200-ok)

---

## The nodes

| Node | Address | What it does in the call |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller, `+1-202-555-0101`. |
| UE 2 | 172.28.0.101 | The callee, `+1-202-555-0102`. |
| P-CSCF | 172.28.0.40 | Serves both UEs. One call passes it twice. |
| S-CSCF | 172.28.0.30 | Both the caller's and the callee's S-CSCF. |
| I-CSCF | 172.28.0.20 | The entry point of the callee's home network. It asks the HSS where the callee is. |
| DNS | 172.28.0.2 | Holds the ENUM records that turn numbers into SIP URIs. |
| Media relay | 172.28.0.50 | Relays the RTP. The P-CSCF controls it. |
| MMTel AS | 172.28.0.75 | Takes the INVITE of the caller and of the callee, and sends each back. |

- One node acts in two positions, for the caller and for the callee: the key to reading this capture. In a real network the two sides are often other nodes, in other networks.
- Here the MMTel AS sends each INVITE back unchanged. The session on supplementary services shows what it does.

> Guide: ["The nodes"](../../../docs/guide/en/call.md#the-nodes)

---

## Run it

```sh
docker compose up -d --build

# First terminal: UE 2 waits for a call.
docker compose run --rm ue2 answer \
  --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-2 --wav-out /captures/ue2-heard.wav

# Second terminal: UE 1 calls the number, plays a tone for 3 seconds, hangs up.
docker compose run --rm ue call --tone 3s tel:+1-202-555-0102
```

- `captures/ue2-heard.wav` holds what UE 2 heard.
- The numbers are in the range 555-0100 to 555-0199, which the North American Numbering Plan keeps for fiction.

> Guide: ["Make a call"](../../../docs/guide/en/call.md#make-a-call)

---

## Open the capture

```text
sip || diameter.cmd.code == 302 || dns.qry.type == 35 || rtp || rtcp
```

- **Telephony > VoIP Calls** lists the call.
  - **Flow Sequence** draws the ladder.
  - **Play Streams** plays both directions.
- If the RTP shows up as plain UDP, enable **rtp_udp** under **Analyze > Enabled Protocols**.
- The numbers 1 to 13 of the guide's ladder are the numbers of the next slides.

> Guide: ["Open the capture"](../../../docs/guide/en/call.md#open-the-capture) and ["The sequence"](../../../docs/guide/en/call.md#the-sequence), which has the full ladder.

---

## The shape of the whole

The INVITE passes the nodes in this order.

```ladder
UE 1, P-CSCF, S-CSCF, MMTel AS, I-CSCF, HSS, UE 2
UE 1 -> P-CSCF -> S-CSCF: INVITE (orig) // 1 - 2
S-CSCF -> MMTel AS: INVITE (orig) // 3
S-CSCF <- MMTel AS: new INVITE
S-CSCF -> I-CSCF: INVITE // 4
I-CSCF <-> HSS: LIR, LIA // 5
I-CSCF -> S-CSCF: INVITE (term) // 6
S-CSCF -> MMTel AS: INVITE (term)
S-CSCF <- MMTel AS: new INVITE
S-CSCF -> P-CSCF: INVITE (term) // 6
P-CSCF -> UE 2: INVITE (term) // 7
```

- `orig` is the originating position, the caller's side; `term` the terminating position, the callee's side.
- The AS is a routeing B2BUA (TS 24.229 5.7.5): its new INVITE has a Call-ID of its own, so one call has three dialogs.

---

## The parts of the call

| Part | Steps | What happens |
| --- | --- | --- |
| The caller's side | 1 to 3 | The caller is asserted, the AS is passed, and the number becomes a SIP URI. |
| The entry point | 4 and 5 | The HSS names the S-CSCF that serves the callee. |
| The callee's side | 6 and 7 | The AS is passed, and the INVITE follows the Path to the callee's UE. |
| The answer | 8 to 11 | 183, PRACK, 180, PRACK, 200 OK, ACK. |
| Voice and hang-up | 12 and 13 | RTP through the relay. BYE ends the call. |

- Come back to this table at each new part: it is the map of the session.

---

## 1 and 2. The INVITE, and the P-CSCF on the caller's side

| Look at | What to see |
| --- | --- |
| Request-URI | `tel:+1-202-555-0102`. The UE only dials a number. |
| Route | Two: the P-CSCF, and the Service-Route from registration. |
| Supported | `100rel`, `timer`, `199`, `precondition`. |
| Session-Expires | 1800. UE 1 refreshes the session with an UPDATE. |
| The SDP offer | UE 1's address and port. PCMU and telephone-event. |
| `a=curr` and `a=des` of the offer | Preconditions: UE 1's resources are there already. |
| Headers the P-CSCF adds | P-Asserted-Identity and Record-Route. |
| The SDP after the P-CSCF | `c=` and `m=` name the media relay. |

- The network trusts P-Asserted-Identity, not From. Whatever the UE writes in From does not matter.
- UE 1 has no access network that reserves resources, so its offer says that they are there already. A UE started with `--epc` is a VoLTE UE and offers AMR-WB and AMR before PCMU; the VoLTE session follows that call.

> Guide: [1](../../../docs/guide/en/call.md#1-ue-1-sends-the-invite) and [2](../../../docs/guide/en/call.md#2-the-p-cscf-sends-it-to-the-callers-s-cscf). TS 24.229 5.1.3.1, 5.1.2A.1.1, 6.1.2, 5.2.6.2, 5.2.6.3.3. RFC 3262 4. RFC 3312. RFC 3264 5.1.

---

## 3. The S-CSCF turns the number into a SIP URI

- The top Route carries `orig`, so the S-CSCF serves the caller.

| Look at | What to see |
| --- | --- |
| P-Asserted-Identity | A second one is added: UE 1's tel URI. |
| P-Asserted-Service | The ICSI of MMTel. P-Preferred-Service is gone. |
| The INVITE to the AS | The AS on top of Route, then the S-CSCF with `odi`. `sescase=orig`. |
| The DNS lookup | NAPTR of `2.0.1.0.5.5.5.2.0.2.1.e164.arpa`, the number reversed. |
| The NAPTR answer | The service is `E2U+sip`. A regular expression makes the SIP URI. |
| Request-URI after the S-CSCF | `sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org`. |

The filter is `dns.qry.type == 35`. The records are in `deploy/compose/dns/e164.arpa.zone`.

- Build the ENUM domain by hand: reverse the digits, one label each.
- The AS reads the caller's services from the HSS over Sh. With the default settings it changes nothing and sends a new INVITE back. The S-CSCF finds its `odi` in it, goes on with the next iFC, and only then turns the number into a SIP URI.

> Guide: [3](../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri). TS 24.229 5.4.3.1, 5.4.3.2, 5.7.5. RFC 6050. RFC 6116.

---

## 4 and 5. The entry point of the home network, and the LIR

| Look at | What to see |
| --- | --- |
| DNS lookups of the S-CSCF | NAPTR and SRV as in registration. They give the I-CSCF. |
| Via after the S-CSCF | `SIP/2.0/TCP`: the INVITE has grown over 1300 bytes. |
| LIR | Carries the public identity of the callee. |
| LIA | DIAMETER_SUCCESS, and the Server-Name of the S-CSCF where UE 2 is registered. |
| The INVITE the I-CSCF forwards | That S-CSCF on top of Route. No Record-Route. |

The filter is `diameter.cmd.code == 302`.

- The I-CSCF adds no Record-Route, so the later PRACK, ACK and BYE skip it.
- The LIR asks what the UAR of registration asks: which S-CSCF serves the user.

> Guide: [4](../../../docs/guide/en/call.md#4-the-s-cscf-finds-the-callees-home-network) and [5](../../../docs/guide/en/call.md#5-the-i-cscf-asks-the-hss-where-the-callee-is-lir-and-lia). TS 24.229 5.3.2.1. TS 29.228 6.1.4. RFC 3261 18.1.1.

---

## 6 and 7. The S-CSCF and the P-CSCF on the callee's side

| Look at | What to see |
| --- | --- |
| Route as it reaches the S-CSCF | No `orig`: this time the S-CSCF serves the callee. |
| The INVITE to the AS | `sescase=term`. The AS sends a new INVITE back. |
| P-Called-Party-ID | The destination that was called. |
| Request-URI after the S-CSCF | The Contact that UE 2 registered. |
| Route after the S-CSCF | The Path of UE 2's REGISTER. |
| The SDP after the P-CSCF | Names the access termination of a second context. |
| Where the INVITE is sent | The address that UE 2's REGISTER came from. |

- The INVITE left the S-CSCF for the I-CSCF and comes back to the same S-CSCF.
- The P-CSCF knows it is on the callee's side: the top Route is the URI of its Path, with the user part `term`.
- It sends to the address of the REGISTER, not to the Contact: behind NAT only that address reaches the UE.

> Guide: [6](../../../docs/guide/en/call.md#6-the-callees-s-cscf-sends-the-invite-along-the-path) and [7](../../../docs/guide/en/call.md#7-the-p-cscf-delivers-the-invite-to-ue-2). TS 24.229 5.4.3.3, 5.2.6.4.3.

---

## 8 and 9. 183 and PRACK

| Look at | What to see |
| --- | --- |
| The SDP of the 183 | The answer: PCMU, UE 2's address and port. |
| Require and RSeq of the 183 | `100rel` and `precondition`: a reliable provisional response. |
| The preconditions of the 183 | Both ends have their resources. No `a=conf`. |
| To tag, Contact, Record-Route of the 183 | They make the early dialog. |
| The SDP of the 183 as UE 1 gets it | Rewritten twice by the P-CSCF. It names the relay. |
| RAck of the PRACK | The RSeq of the 183 and the CSeq of the INVITE. |
| The way of the PRACK | It ends at the caller's AS, which sends it on in the next dialog. |

- A PRACK is a request within a dialog, so it ends at the far end of that dialog. The AS sends the PRACK of the next dialog on, then answers the one it got with 200: one PRACK and one 200 for each of the three dialogs. None passes the I-CSCF, which added no Record-Route.

> Guide: [8](../../../docs/guide/en/call.md#8-ue-2-answers-the-offer-a-reliable-183) and [9](../../../docs/guide/en/call.md#9-ue-1-acknowledges-the-183-prack). RFC 3264 6.1. RFC 3262 3, 4. TS 24.229 5.1.4.1. RFC 3261 12.2.1.1.

---

## 10 and 11. 180, 200 OK and ACK

| Look at | What to see |
| --- | --- |
| 180 Ringing | Sent once the preconditions are met. It gets a PRACK too. |
| The 200 OK to the INVITE | The dialog is confirmed. |
| The SDP of the 200 OK | None. The answer came in the reliable 183. |
| ACK | Follows the route set. It is not part of the INVITE transaction. |

- UE 2 sends the 200 OK again, with growing intervals, until the ACK arrives.
- A callee whose resources are not there yet sends no 180 until they are. Both UEs here have theirs, so the 180 follows the 183 at once.
- The AS acknowledges UE 2's 200 on its own dialog at once, so the ACK to UE 2 can come before the 200 to UE 1.

> Guide: [10](../../../docs/guide/en/call.md#10-ue-2-rings-then-answers-180-and-200-ok) and [11](../../../docs/guide/en/call.md#11-ue-1-confirms-ack). RFC 3312 6. TS 24.229 5.1.4.1. IR.92 2.4.1. RFC 3261 13.3.1.4, 13.2.1, 13.2.2.4. RFC 3262 5.

---

## 12. The voice flows through the relay

```text
UE 1 -> relay access (context 1) -> relay core (context 1)
     -> relay core (context 2) -> relay access (context 2) -> UE 2
```

| Look at | What to see |
| --- | --- |
| Interval and size of the RTP | Every 20 ms, 160 bytes of G.711 μ-law. |
| Source and destination of the RTP | Always a UE and the relay. The UEs never talk to each other. |
| Ports for sending and receiving | The same port: symmetric RTP. |
| RTCP | The port above the RTP port. At least every 5 seconds. |

- The relay sends to where the first packet came from. This is called latching.

The filter is `rtp || rtcp`. **Play Streams** plays the voice.

> Guide: [12](../../../docs/guide/en/call.md#12-the-voice-flows-through-the-relay). RFC 3550 5.1, 6. RFC 3551 4.5.14. RFC 4961. IR.92 3.2.4.

---

## 12. Two contexts, and latching

- One call has two contexts in the relay: one from the pass of the P-CSCF on the caller's side, one from the pass on the callee's side. Each pass handles its own side only.
- So the RTP crosses the relay twice, as it would cross the IMS-AGWs of two networks, and the capture tells the two sides apart. With the UEs behind two P-CSCFs, the call would look the same.
- Latching is for UEs behind NAT: a softphone on the host sends from an address that its SDP cannot know. The P-CSCF and the IMS-AGW of a real network do the same.
- An RTCP compound packet holds a sender report (a receiver report while the UE sends nothing) and an SDES with the CNAME. An RTCP BYE ends the stream.

---

## 13. Hanging up

| Look at | What to see |
| --- | --- |
| BYE | A request within the dialog. It follows the route set. |
| Reason of the BYE | `RELEASE_CAUSE;cause=1`: why the call ended. |
| The 200 to the BYE | The far end of each dialog sends one. The last is UE 2's. |
| What the P-CSCF does | It removes the relay contexts when the 200 passes. |

- The P-CSCF waits for the answer because a BYE can be turned down.
- Removing the contexts shows in no SIP message: the P-CSCF calls the API of the relay, a small HTTP API of this project, so that the relay reads more easily than with a protocol as large as H.248.
- The AS answers UE 1's BYE on its own dialog and sends a BYE of its own on the next, so the 200 to UE 1 and the BYE to UE 2 may come in either order.

> Guide: [13](../../../docs/guide/en/call.md#13-ue-1-hangs-up-bye). TS 24.229 5.1.5. IR.92 2.2.4. RFC 3261 15.1.1, 15.1.2, 12.2.2.

---

## Watching it fail

| Case | How to try it | How it ends | Who answers |
| --- | --- | --- | --- |
| Busy | Start UE 2 with `--reject 486`. | 486 Busy Here | UE 2 |
| Not registered | Call `tel:+12025550103`. | 480 Temporarily Unavailable | S-CSCF |
| Unknown number | Call `tel:+12025550199`. | 404 Not Found | S-CSCF |

- An unregistered user still has an iFC that calls the MMTel AS. The LIA says DIAMETER_UNREGISTERED_SERVICE (2003). The S-CSCF that the I-CSCF picks answers 480 once the AS sends the INVITE back.
- An unknown number has no ENUM record. This project has no BGCF, so the S-CSCF answers 404.
- Which node made a response: the source address of the first packet that carries it. The AS copies each response onto its own dialog, so look beyond the AS.

> Guide: ["Make a call"](../../../docs/guide/en/call.md#make-a-call), [3](../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri), [5](../../../docs/guide/en/call.md#5-the-i-cscf-asks-the-hss-where-the-callee-is-lir-and-lia). TS 29.228 6.1.4.1. TS 24.229 5.4.3.3.

---

## Calling from the dashboard

```sh
docker compose --profile sim up -d --build
```

1. Open <http://localhost:8080>. In the card of `ue-b`, set the answer policy to `manual`.
2. In the card of `ue-a`, press **Call**.
3. In the card of `ue-b`, press **Answer**. Press **Hang up** in either card.
4. **ladder** next to the call draws its ladder, even while the call is up.

- Hanging up before the answer makes the caller send CANCEL, and the callee decline with 603.
- Do not run the `sim` and the `ue` profiles at once: two UEs of one subscriber overwrite each other's registration. The HTTP API that drives a UE is this project's own; none of it goes to the IMS.

> Guide: [Watch the IMS in the dashboard, "Drive the UE simulators"](../../../docs/guide/en/dashboard.md#drive-the-ue-simulators), and [the call guide, "Drive a UE over HTTP"](../../../docs/guide/en/call.md#drive-a-ue-over-http)

---

## Check it in the code

| Step | File to open |
| --- | --- |
| 1, 8 to 13. The UE calls, answers, sends voice | `internal/ue/call.go`, `internal/ue/media.go` |
| 2, 7. The two passes of the P-CSCF, rewriting the SDP | `internal/pcscf/session.go` |
| 3, 6. The S-CSCF on the caller's and the callee's side | `internal/scscf/session.go` |
| 3, 6. The MMTel AS, a routeing B2BUA | `internal/mmtel/call.go` |
| 8 to 10. Preconditions | `internal/precondition` |
| 3. ENUM | `internal/sipdns/enum.go` |
| 5. LIR | `internal/icscf/session.go` |
| 12. Relaying and latching | `internal/agw/relay.go` |
| Dialogs and route sets | `internal/sip/dialog.go` |

- In `internal/scscf/session.go`, find the condition that tells originating from terminating.

> Guide: ["What this project simplifies"](../../../docs/guide/en/call.md#what-this-project-simplifies)

---

[layout: exercise]

## Looking back

1. How often does the Request-URI of UE 1's INVITE change before it reaches UE 2?
2. How does the S-CSCF tell whether it serves the caller or the callee?
3. Why is the I-CSCF not on the way of the PRACK and the BYE?
4. Whose address is in the SDP that UE 1 receives?
5. Which node answers 480, which answers 404, and on what grounds?
6. Why does UE 2's 200 OK carry no SDP?

---

[layout: answers]

## Looking back: answers

1. Twice. The S-CSCF turns the tel URI into a SIP URI, and the callee's S-CSCF sets UE 2's Contact.
2. By `orig` in the URI on top of Route.
3. It adds no Record-Route.
4. The media relay's: the access termination of UE 1's context.
5. The S-CSCF answers both. 480, because the LIA said DIAMETER_UNREGISTERED_SERVICE and the service of the unregistered user, the MMTel AS, did not take the call. 404, because ENUM has no record.
6. The answer came in the reliable 183 (RFC 3262 5).

---

## Next session

- Next is SMS. It uses registration, and the way the I-CSCF finds the S-CSCF, as they are.
- The iFCs that sent this call to the MMTel AS take the lead: they bring a short message to the IP-SM-GW.

```sh
docker compose --profile ue --profile sim down --rmi all -v --remove-orphans
```

> The guide of the next session is [Follow a short message through the IMS](../../../docs/guide/en/sms.md).
