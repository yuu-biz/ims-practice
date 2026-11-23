---
title: Announcements, tones and conferences with the MRF
footer: ims-practice study session 8. The MRF
lang: en
---

# Announcements, tones and conferences with the MRF

ims-practice study session 8 (the MRFC and the MRFP, announcements, the tone the callee chooses, a conference of three)

---

[layout: summary]

## What you will know after this session

To begin: a caller hears a tune while the callee's phone rings, and the callee chose that tune. Which node plays it, and which one decided to?

- What an MRF is, and why it is two nodes: the MRFC and the MRFP.
- How an AS asks for a service: the Request-URIs of RFC 4240, over Mr'.
- How the network gets audio to a caller before the call is answered.
- What to look at for an announcement, for the tone the callee chose, and for a waiting call.
- How a conference of three is made, and who mixes it.

> Guide: [Announcements, alerting tones and conferences with the MRF](../../../docs/guide/en/mrf.md) (`docs/guide/en/mrf.md`)
> The editions are named at the top of the guide, among them TS 23.228 V20.1.0, TS 24.229 V20.1.0, TS 24.182 V19.0.0, TS 24.628 V19.0.0, TS 24.615 V19.0.0, TS 24.147 V19.0.0, TS 23.333 V19.0.0, RFC 4240, RFC 4575 and GSMA IR.92 v24.0.

---

## What an MRF is

- The CSCFs route, and the AS of the last session changes a call. Neither makes audio. The MRF is the node of the IMS that makes media of its own (TS 23.228 4.7): announcements, tones, and the mix of a conference.
- The **MRFC** speaks SIP. An AS asks it for a service with an INVITE, and the MRFC decides which media resources that takes.
- The **MRFP** handles the RTP. It plays to a termination, or sums the audio of several terminations into one mix for each of them (TS 23.333 6.2.9).

```text
  S-CSCF --- MMTel AS ---(Mr', SIP)--- MRFC
                                         |
                                       (Mp)
                                         |
  UE --- P-CSCF --- agw -----(RTP)---- MRFP
```

- The MRFP is the same relay as the media gateway of the P-CSCF, with the announcements and the mixer added. The RTP between a UE and the MRFP passes the agw.

> Guide: ["What an MRF is"](../../../docs/guide/en/mrf.md#what-an-mrf-is)

---

## The three reference points

| Reference point | Between | In this project |
| --- | --- | --- |
| Mr' | the AS and the MRFC | SIP, straight from the MMTel AS. Mr, from the S-CSCF, is not used. |
| Mp | the MRFC and the MRFP | H.248 in the standard (TS 29.333). Here a small HTTP API on port 8080, with one operation per H.248 command. |
| Cr | the AS and the MRFC | Prompts and media control commands. Not built: the MRFP plays the WAV files it has. |

- The operations of Mp here: add a termination, modify it (mode, remote address, play), subtract it, and a Notify when a play ends. `http && ip.addr == 172.28.0.86` shows them in the capture.
- Every request for media comes from the AS here, not from the S-CSCF (TS 23.218), so Mr stays empty. So does Cr, which is not built at all.

> Guide: ["What an MRF is"](../../../docs/guide/en/mrf.md#what-an-mrf-is)

---

## The nodes

| Node | Address | What it does here |
| --- | --- | --- |
| UE 1, UE 2 | 172.28.0.100, .101 | The caller and the called user of the announcement and the tone. |
| phone1, phone2, phone3 | 172.28.0.110, .111, .112 | The UEs of the `phones` profile, driven from the Lab: the waiting call and the conference. |
| P-CSCF and agw | 172.28.0.40, .50 | As in every call. The RTP to and from the MRFP passes the agw. |
| MMTel AS | 172.28.0.75 | Decides what is played, and is the conference focus. |
| MRFC | 172.28.0.85 | The `mrfc` role. |
| MRFP | 172.28.0.86 | The `mrfp` role: the control API on port 8080, RTP on ports 40100 to 40199. |

- `deploy/compose/mmtel.yaml` names the MRFC and the announcement to play before a rejection. `deploy/compose/mrfp.yaml` maps the names `notice`, `cat1` and `cat2` to the WAV files in `deploy/announcements`.

> Guide: ["The nodes"](../../../docs/guide/en/mrf.md#the-nodes)

---

## How the AS asks: RFC 4240

| Request-URI | Service |
| --- | --- |
| `sip:annc@mrfc...:5060;play=/provisioned/notice` | Play `notice` once. |
| `sip:annc@...;play=/provisioned/cat1;repeat=forever` | Play `cat1` until the AS ends the dialog. |
| `sip:conf=conf-pcn6hpat5alr@mrfc...` | Join that conference, which the first INVITE creates. |

- The service is in the user part of the Request-URI: `annc` plays, `conf=` joins a conference. `play=` names a file of the MRFP's configuration.
- The first `conf=` INVITE creates the mix, and every later one adds a termination to it.

The filters are `sip.r-uri contains "annc@"` and `sip.r-uri contains "conf="`.

> Guide: ["How the AS asks: RFC 4240"](../../../docs/guide/en/mrf.md#how-the-as-asks-rfc-4240). RFC 4240 3, 5.

---

## Audio before the answer

- The INVITE for an announcement carries no SDP. The MRFC therefore offers the MRFP's termination in its 200, and the AS sends the answer in the ACK.
- The MRFP starts to play on the ACK (TS 24.229 5.8.2.2.1), and the MRFC ends the dialog with BYE when a play that has an end is over (5.8.3.2.1). The AS thus chooses the moment the play starts, after the caller is ready for it.
- To get the audio to the caller before the call is answered, the AS answers the caller's offer itself with the SDP of the MRFP and authorizes the early media with `P-Early-Media: sendonly` (RFC 5009). This is the gateway model of TS 24.628 Annex G.
- The caller's UE plays what comes instead of a ringing tone of its own (TS 24.628 4.7.2.1).

The filter is `sip.P-Early-Media`.

> Guide: ["How the AS asks: RFC 4240"](../../../docs/guide/en/mrf.md#how-the-as-asks-rfc-4240)

---

## Run it

```sh
docker compose up -d --build

# An announcement: UE 1 bars its outgoing calls, then calls UE 2.
docker compose run --rm ue xcap set baoc on
docker compose run --rm ue call tel:+12025550102
docker compose run --rm ue xcap set baoc off

# The waiting call and the conference, driven from the Lab.
docker compose --profile phones up -d phone1 phone2 phone3
```

- For the tone, choose `cat1` as the alerting tone of subscriber 2 in the Subscribers panel of the Lab at http://localhost:8080, then call `phone2` from `phone1`.
- `scripts/e2e-mrf.sh` runs everything of this session and checks it in the capture. It leaves the capture, the frames of the calls and the MRF, the audio UE 1 heard during the tone, and the logs of the UEs.

> Guide: ["Run it"](../../../docs/guide/en/mrf.md#run-it) and ["Drive the UE simulators" in Watch the IMS in the Lab](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## Open the capture

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

- It keeps the calls, the subscriptions and NOTIFYs of the conference, and the control API of the MRFP.
- **Telephony > RTP > RTP Streams** lists the streams of the MRFP; **Play Streams** plays them.
- Some RTP starts before Wireshark has read the SDP that describes it. Switch on **Analyze > Enabled Protocols > RTP > rtp_udp** to decode all of it.

> Guide: ["Open the capture"](../../../docs/guide/en/mrf.md#open-the-capture)

---

## An announcement before a rejection

```ladder
UE 1, P-CSCF, MMTel AS, MRFC
UE 1 -> P-CSCF -> MMTel AS: INVITE // C1
MMTel AS -> MRFC: INVITE annc, no SDP // M
MMTel AS <-- MRFC: 200, offer of the MRFP
UE 1 <-- P-CSCF <-- MMTel AS: 183, P-Early-Media // C1
UE 1 -> P-CSCF -> MMTel AS: PRACK
MMTel AS -> MRFC: ACK, the answer: play starts
MMTel AS <-- MRFC: BYE, notice has played
UE 1 <-- P-CSCF <-- MMTel AS: 603, Reason cause=603
```

- UE 1 bars its own outgoing calls, so its AS rejects the call (TS 24.611 4.5.2.4.1), but plays `notice` first (TS 24.628 4.2.4).
- The AS waits for the PRACK before it sends the ACK, so no frame of the announcement is lost before UE 1 has the SDP.
- Measure the ACK to the BYE: 1.08 s, the length of the WAV file.

> Guide: ["An announcement before a rejection"](../../../docs/guide/en/mrf.md#an-announcement-before-a-rejection)

---

## CAT: the tone the called user chooses

| Look at | What to see |
| --- | --- |
| The UDR of UE 2's AS | Service-Indication `Customized-Alerting-Tone`, read for each call. |
| The AS's INVITE to the MRFC | `play=/provisioned/cat1;repeat=forever`, before the INVITE goes to UE 2. |
| The 183 that reaches UE 1 | UE 2's answer with the precondition table, as it came. |
| The UPDATE to UE 1, after UE 2's 180 | `P-Early-Media: sendonly` and the SDP of the MRFP, with `a=content:g.3gpp.cat`. |
| At the answer | A BYE to the MRFC, and an UPDATE that moves UE 1's media to UE 2, before the 200. |

- The operator provisions the tone in the HSS (TS 24.182 4.5.2); here the Lab does it. The terminating AS reads it over Sh for each call and asks the MRFC before UE 2 is called (4.5.5.3.7).
- UE 1 uses preconditions, so the tone comes in an UPDATE once UE 2 alerts (4.5.5.3.7 b 1). A caller without preconditions gets it in the 18x itself.

The filter is `sdp.media_attr == "content:g.3gpp.cat"` (RFC 4796).

> Guide: ["CAT: the tone that the called user chooses"](../../../docs/guide/en/mrf.md#cat-the-tone-that-the-called-user-chooses). TS 24.182 4.5.5.3.7 b 2.

---

## The same call with the tone off

| Look at | With `cat1` | With the tone off |
| --- | --- | --- |
| UE 1's log | `"ringback":"network"` | `"ringback":"local"` |
| What reaches UE 1 | an UPDATE with P-Early-Media | the 180, without it |
| The MRFC | an INVITE with `repeat=forever` | none |
| The MRFP | RTP to UE 1 before the answer | none |

- With the tone off, UE 1 plays its own ringing tone (TS 24.628 4.7.2.1). The two calls are one after the other in the capture of the e2e script: compare them.
- Why compare the two? The pair shows which frames the tone adds, and that nothing else of the call changes.
- The script compares the first second that UE 1 received with `cat1.wav` as the MRFP encodes it to PCMU: they are equal byte for byte.

> Guide: ["CAT: the tone that the called user chooses"](../../../docs/guide/en/mrf.md#cat-the-tone-that-the-called-user-chooses)

---

## Call waiting and hold

| Look at | What to see |
| --- | --- |
| phone1's 180 on the second call | `Alert-Info: <urn:alert:service:call-waiting>` (TS 24.615 4.5.5.3.2, RFC 7462). |
| The 180 that reaches phone3 | Without that Alert-Info: the operator part of CW in `hss.yaml` does not tell the caller (4.5.5.2.3). |
| Answering the waiting call | A re-INVITE on the first call with `a=sendonly`: phone2 goes on hold (4.5.5.3.3, TS 24.610). |
| **Swap** | One call held, the other back with `a=sendrecv`. |

- IR.92 asks the UE, not the network, to handle the waiting call (2.3.4): phone1 rings and does not answer by itself.
- With the other setting the AS passes the Alert-Info on, and, when an announcement is configured, plays it to the caller while the call waits. That is where the MRF would come into this part.

The filter is `sip.Alert-Info contains "call-waiting"`.

> Guide: ["Call waiting and hold"](../../../docs/guide/en/mrf.md#call-waiting-and-hold)

---

## A conference of three

phone1 has two answered calls and presses **Merge**.

| Step | What to see |
| --- | --- |
| 1 | An INVITE to the conference factory URI, `sip:mmtel@conf-factory...`. |
| 2 | The AS asks the MRFC with `conf=` and phone1's offer, and answers phone1 with the conference URI in Contact, with `isfocus`. |
| 3 | phone1 SUBSCRIBEs to that conference URI, Event `conference`. |
| 4 | A REFER per peer, whose Refer-To carries a Replaces of phone1's call with that peer. |
| 5 | The focus INVITEs each peer with that Replaces and a new offer of the MRFP. The peer ends the call that was replaced with BYE. |

- The creator refers each peer in, the way TS 24.147 5.3.1.3.3 calls option 2b, which IR.92 2.3.3 asks for.
- The focus accepts each REFER with 200, never 202: RFC 7647 5 replaced the 202 of RFC 3515, as RFC 6665 8.3.1 did for SUBSCRIBE. The flows of TS 24.147 show the 200 (A.4.4.1).

The filters are `sip.Refer-To contains "Replaces"` and `sip.Replaces`.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## The conference URI is a PSI

- A conference URI is a public service identity of the home network (TS 23.228 4.3.6): `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org`. The operator defines its domain. It names no node and has no port, which a PSI that may leave the network should not have (TS 23.003 13.5).
- The NAPTR and SRV records of that subdomain lead to the AS that hosts the conferences, so the S-CSCF finds the focus as it finds any host, by RFC 3263. This is the subdomain way of TS 23.228 5.4.12.3.
- A request from another network comes in through the I-CSCF, which knows the subdomain and sends it by DNS too, without asking the HSS (TS 24.229 5.3.2.1 step 3).
- The factory URI is a PSI of the originating side (TS 23.228 5.4.12.1): the INVITE to it reaches the AS by the creator's own originating filter criteria, as every INVITE of the creator does.

The filters are `dns.qry.name contains "conf."` and `sip.Contact contains "isfocus"`.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## The focus, and the mix

- The AS is the focus and the notification service of the conference (TS 24.147 4): it holds a dialog with each participant, and a dialog with the MRFC for each participant's media.
- The MRFC creates the mix at the first `conf=` INVITE and adds a termination for each later one. The MRFP sends each party the sum of the others (TS 23.333 6.2.9).
- The invited peers use preconditions, so each answers the MRFP's offer in its reliable 183 and sends its 200 without SDP. The AS passes that answer to the MRFC in the ACK (RFC 3262 5).
- In the capture three streams go from the MRFP to the agw and three come back. The mix that one party gets is louder than any single party's tone.
- The conference ends with its creator (TS 24.147 5.3.2.7): when phone1 hangs up, the AS sends a BYE to phone2 and phone3, and one to the MRFC for each party.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## The conference event package

| Look at | What to see |
| --- | --- |
| The NOTIFYs | conference-info XML (RFC 4575), with a `<user>` per party and an `<endpoint>` of status `connected`. |
| The `entity` of a `<user>` | The identity that the network asserted for the party, or an anonymous URI for one that asks for privacy (RFC 4575 8.2). |
| The `entity` of an `<endpoint>` | A key that the focus makes, a `urn:uuid:` URI. RFC 4575 5.7 leaves the key to the focus. |
| maximum-user-count | Six. The seventh participant gets 403. |
| The NOTIFYs of a REFER | Each REFER makes a subscription to the refer event (RFC 3515 2.4.4): `100 Trying` at once, and the final response in a last NOTIFY with `terminated;reason=noresource` (2.4.7). |

- Why is the endpoint key not the Contact of the UE? The Contact is the address of that UE, and the NOTIFYs show it to every subscriber, in another network too.
- The **Conference** row of each card of the Lab lists what these NOTIFYs say.

The filter is `sip.Event contains "conference"`.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## Coming back to the conference

- A participant that has left can come back while the conference goes on: an INVITE to the conference URI, which it has from the Contact of the focus (TS 24.147 5.3.1.4.1).
- That INVITE goes as every call of that user goes. The S-CSCF gives it to the AS for the user's originating services, the AS sends a new INVITE to the same URI back, and the S-CSCF then finds the focus by DNS.
- No filter criterion brings an INVITE to the focus, so the one for the focus carries no P-Served-User. That is how the AS tells the two apart: the Request-URI is the same in both.
- The focus takes the party in as it took the creator (5.3.2.4.1), and the NOTIFYs list it as `connected` again, with the joining-method `dialed-in` instead of `dialed-out`.
- An INVITE to a conference URI that no conference has gets 404, the creator included: the conference ended when it left, and its URI with it (5.3.2.7).

> Guide: ["Coming back to the conference"](../../../docs/guide/en/mrf.md#coming-back-to-the-conference). The e2e script does not run this; `TestRejoinWithTheConferenceURI` in `internal/mmtel` runs it in one process.

---

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.r-uri contains "annc@"` | The requests for an announcement or a tone. |
| `sip.r-uri contains "conf="` | The parties joining a conference at the MRFC. |
| `sip.P-Early-Media` | The responses and UPDATEs that authorize early media. |
| `sdp.media_attr == "content:g.3gpp.cat"` | The SDP of a customized alerting tone. |
| `sip.Alert-Info contains "call-waiting"` | The 180s of a waiting call. |
| `sip.Event contains "conference"` | The subscriptions and NOTIFYs of the conference event package. |
| `rtp && ip.addr == 172.28.0.86` | The RTP of the MRFP, with the RTP heuristic on. |
| `http && ip.addr == 172.28.0.86` | The control API of the MRFP, over Mp. |

- The guide has four more: the Replaces of a conference, the answers of the focus, the DNS queries that lead to it, and the CATs alone.
- The e2e script runs every filter of the guide's table against the capture: one that finds no frame fails the run.

> Guide: ["Wireshark display filters"](../../../docs/guide/en/mrf.md#wireshark-display-filters)

---

## Drive it from the Lab

```sh
docker compose --profile phones up -d --build
```

1. In the Subscribers panel, choose `cat1` in the **Alerting tone (CAT)** column of subscriber 2. Call `phone2` from `phone1`: the Session row of `phone1` says that it rang with early media from the network.
2. Call `phone2` from `phone1` again and let it answer, then call `phone1` from `phone3`. The card of `phone1` shows the new call as `waiting`.
3. **Answer** holds the call with `phone2` and takes `phone3`. **Swap** goes from one call to the other.
4. **Merge** makes the conference. The **Conference** row of all three cards then lists three users.

- The ladder of a call shows the AS's dialog with the MRFC beside the dialogs of the call: they share the icid-value of the P-Charging-Vector.

> Guide: ["Drive the UE simulators" in Watch the IMS in the Lab](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## What this project simplifies

| Where | The main items |
| --- | --- |
| MRFC | PCMU only, and no transcoding: an offer without PCMU gets 488 with a Warning. Only provisioned announcements, so no Cr. The Contact is the URI of the instance, not a GRUU. |
| MRFP | The mixer takes PCMU, keeps three frames for jitter, writes no CSRC list and sends no RTCP. |
| AS, the tone | The callee's 183 with preconditions goes to the caller as it came, and the tone starts without a look at the caller's preconditions. |
| AS, the conference | Every served user may create one; there is no conference policy. A REFER that asks the focus to remove a participant is refused with 403. |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/mrfc internal/netann internal/agw
```

- Why PCMU only: a conference mixes decoded speech, and there is no AMR decoder here. The UE simulators offer PCMU.

> Guide: ["What this project simplifies"](../../../docs/guide/en/mrf.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Check it in the code

| What you saw | Where to look |
| --- | --- |
| The AS's dialog with the MRFC | `mrf.go`, `announcement.go` in `internal/mmtel` |
| The tone at the terminating AS | `cat.go`, `catdata.go` in `internal/mmtel` |
| CW at the AS | `internal/mmtel/waiting.go` |
| The focus and the conference event package | `conference.go`, `conferenceevent.go` in `internal/mmtel` |
| The MRFC, and the Request-URIs of RFC 4240 | `internal/mrfc`, `internal/netann` |
| The MRFP: terminations, announcements, the mixer | `internal/agw` |
| Swap, Merge, and the UE side of a conference | `phone_conference.go`, `conference.go` in `internal/ue` |

- `mrf_network_test.go` and `conference_network_test.go` in `internal/mmtel` run the same flows through the whole IMS in one process.

> Guide: ["Where the code is"](../../../docs/guide/en/mrf.md#where-the-code-is)

---

[layout: exercise]

## Exercises

1. Bar UE 1's outgoing calls and call UE 2. Measure the time from the AS's ACK to the MRFC to the MRFC's BYE. What is that time, and what decides it?
2. Give subscriber 2 the tone `cat1` and call it. Which SDP attribute marks the tone, and which header authorizes it?
3. In the conference, count the RTP streams between the MRFP and the agw. Why three in each direction?
4. Find a NOTIFY of the conference event package. Why is the `entity` of an `<endpoint>` not the Contact of that UE?
5. Call a conference URI whose conference has ended. What comes back?

For 1, run `ue xcap` and `ue call` in the form of the slide "Run it".

---

[layout: answers]

## Exercises: answers

1. About 1.08 s, the length of the WAV file of `notice`. The MRFC sends the BYE when the play is over.
2. `a=content:g.3gpp.cat` marks it, and `P-Early-Media: sendonly` authorizes it.
3. Three parties: each sends its own audio to the MRFP, and each gets back the sum of the other two.
4. The Contact is the address of that UE, and the NOTIFYs go to every subscriber of the conference, in another network too.
5. 404. The conference ended when its creator left, and its URI with it.

---

[layout: exercise]

## Looking back

1. Which node decides what is played, and which one plays it?
2. Why does the INVITE for an announcement carry no SDP?
3. How does a caller hear anything before the call is answered?
4. Why does the AS wait for the PRACK before it sends the ACK to the MRFC?
5. How does the AS tell an INVITE meant for the focus from one meant for a user's services?
6. What does the MRFP send to each party of a conference?

---

[layout: answers]

## Looking back: answers

1. The MMTel AS decides and asks over Mr'. The MRFC takes the request, and the MRFP makes the audio.
2. So that the MRFC offers the MRFP's termination in its 200 and the AS answers in the ACK. The AS then chooses when the play starts.
3. The AS answers the caller's offer itself with the SDP of the MRFP and authorizes the early media with `P-Early-Media: sendonly`.
4. So that no frame of the announcement is lost before the caller has the SDP.
5. By P-Served-User. No filter criterion sends an INVITE to the focus, so that one has none.
6. The sum of what the other parties send, one mix per party.

---

## Next session

- Next is the VoLTE session: the access network, IMS AKA, policy control over Rx, and the VoLTE call.
- The border between two networks, and a conference whose parties are in both, come in the last session, with [the interconnect guide](../../../docs/guide/en/interconnect.md).

```sh
docker compose --profile ue --profile phones down -v --remove-orphans
```

- This stops the UEs of both profiles and the nodes, and keeps the images, so the next session starts without a new build.

> The guides are listed in [docs/guide/](../../../docs/guide/).
