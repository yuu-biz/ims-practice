---
title: Announcements, tones and conferences with the MRF
footer: ims-practice 8. The MRF
lang: en
format: visual
---

```ims-model
nodes:
  - {id: dns, label: DNS, role: resolves names, at: [1, 0]}
  - {id: hss, label: HSS, role: subscriber data, at: [2, 0]}
  - {id: focus, label: MMTel AS, role: conference focus, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: registration and services, at: [1, 1]}
  - {id: mmtel, label: MMTel AS, role: the user's services, at: [2, 1]}
  - {id: mrfc, label: MRFC, role: the SIP side of the MRF, at: [3, 1]}
  - {id: ue1, label: UE 1, role: calls, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: its first hop, at: [1, 2]}
  - {id: ue2, label: UE 2, role: is called, at: [0, 3]}
  - {id: agw, label: IMS-AGW, role: media relay, at: [1, 3]}
  - {id: mrfp, label: MRFP, role: makes the audio, at: [3, 3]}
  - {id: phone1, label: phone1, role: makes the conference, at: [0, 2]}
  - {id: phone2, label: phone2, role: is invited, at: [0, 3]}
  - {id: phone3, label: phone3, role: is invited, at: [0, 4]}
events:
  # Reference points
  - {id: gm, rel: Gm, from: ue1, to: pcscf, proto: SIP}
  - {id: mw, rel: Mw, from: pcscf, to: scscf, proto: SIP}
  - {id: isc, rel: ISC, from: scscf, to: mmtel, proto: SIP}
  - {id: mr, rel: "Mr'", from: mmtel, to: mrfc, proto: SIP}
  - {id: mp, rel: Mp, from: mrfc, to: mrfp, proto: HTTP}
  - {id: media-ue, rel: RTP, from: ue1, to: agw, proto: RTP}
  - {id: media-mrfp, rel: RTP, from: agw, to: mrfp, proto: RTP}
  # An announcement before a rejection
  - id: an-inv-ue-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    note: (C1)
    fields:
      - Request-URI: "`tel:+12025550102`"
  - {id: an-inv-p-s, msg: INVITE, from: pcscf, to: scscf, proto: SIP}
  - {id: an-inv-s-as, msg: INVITE, from: scscf, to: mmtel, proto: SIP}
  - id: an-ocb
    do: applies the barring
    at: mmtel
    fields:
      - Barring: "UE 1 has set `baoc`, so the call is rejected (TS 24.611 4.5.2.4.1)"
      - Before rejecting: "play `notice` (TS 24.628 4.2.4)"
  - id: an-annc
    msg: INVITE
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/notice`"
      - SDP: none
  - id: an-annc-200
    msg: 200 OK
    from: mrfc
    to: mmtel
    proto: SIP
    fields:
      - SDP: "the offer of the MRFP (172.28.0.86). PCMU, `sendonly`"
  - {id: an-183-as-s, msg: "183", from: mmtel, to: scscf, proto: SIP}
  - {id: an-183-s-p, msg: "183", from: scscf, to: pcscf, proto: SIP}
  - id: an-183-p-ue
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Require: "`100rel`"
      - P-Early-Media: "`sendonly`. It authorizes early media (RFC 5009)"
      - SDP: the answer to the offer of UE 1, with the SDP of the MRFP
  - {id: an-prack-ue-p, msg: PRACK, from: ue1, to: pcscf, proto: SIP}
  - {id: an-prack-p-s, msg: PRACK, from: pcscf, to: scscf, proto: SIP}
  - {id: an-prack-s-as, msg: PRACK, from: scscf, to: mmtel, proto: SIP}
  - id: an-ack
    msg: ACK
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - SDP: "the answer. The media of UE 1, `recvonly`"
  - id: an-play
    do: starts to play notice
    at: mrfp
    fields:
      - Trigger: the ACK that the MRFC got (TS 24.229 5.8.2.2.1)
      - Codec: PCMU
  - {id: an-rtp-p-agw, msg: RTP, from: mrfp, to: agw, proto: RTP}
  - id: an-rtp-agw-ue
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
    fields:
      - Content: "`notice`. UE 1 plays it instead of its own ringback tone (TS 24.628 4.7.2.1)"
  - id: an-bye
    msg: BYE
    from: mrfc
    to: mmtel
    proto: SIP
    note: (M)
    fields:
      - When: "1.08 s after the ACK, the length of the WAV file of `notice` (TS 24.229 5.8.3.2.1)"
  - {id: an-603-as-s, msg: "603", from: mmtel, to: scscf, proto: SIP}
  - {id: an-603-s-p, msg: "603", from: scscf, to: pcscf, proto: SIP}
  - id: an-603-p-ue
    msg: "603"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Reason: '`SIP;cause=603;text="Decline"`'
  # CAT
  - id: cat-inv-s-as
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (terminating)
    fields:
      - Called: "UE 2 (`tel:+12025550102`)"
  - id: cat-udr
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    fields:
      - Service-Indication: "`Customized-Alerting-Tone`"
  - id: cat-uda
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    fields:
      - Content: "the tone set for subscriber 2 (`cat1`)"
  - id: cat-annc
    msg: INVITE
    from: mmtel
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/cat1;repeat=forever`"
  - id: cat-annc-200
    msg: 200 OK
    from: mrfc
    to: mmtel
    proto: SIP
    fields:
      - SDP: the offer of the MRFP
  - {id: cat-inv-as-s, msg: INVITE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-inv-s-p, msg: INVITE, from: scscf, to: pcscf, proto: SIP}
  - {id: cat-inv-p-ue, msg: INVITE, from: pcscf, to: ue2, proto: SIP, note: (C3)}
  - {id: cat-180-ue-p, msg: "180", from: ue2, to: pcscf, proto: SIP}
  - {id: cat-180-p-s, msg: "180", from: pcscf, to: scscf, proto: SIP}
  - {id: cat-180-s-as, msg: "180", from: scscf, to: mmtel, proto: SIP}
  - {id: cat-update-as-s, msg: UPDATE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-update-s-p, msg: UPDATE, from: scscf, to: pcscf, proto: SIP}
  - id: cat-update-p-ue
    msg: UPDATE
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - P-Early-Media: "`sendonly`"
      - SDP: "the SDP of the MRFP, with `sendonly` and `a=content:g.3gpp.cat` (RFC 4796)"
  - {id: cat-ack, msg: ACK, from: mmtel, to: mrfc, proto: SIP, note: (M)}
  - {id: cat-rtp-p-agw, msg: RTP, from: mrfp, to: agw, proto: RTP}
  - id: cat-rtp-agw-ue
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
    fields:
      - Content: "`cat1`. Its first second does not differ by one byte from `cat1.wav` as the MRFP encodes it in PCMU"
  - {id: cat-200-ue-p, msg: 200 OK, from: ue2, to: pcscf, proto: SIP}
  - {id: cat-200-p-s, msg: 200 OK, from: pcscf, to: scscf, proto: SIP}
  - {id: cat-200-s-as, msg: 200 OK, from: scscf, to: mmtel, proto: SIP}
  - {id: cat-bye, msg: BYE, from: mmtel, to: mrfc, proto: SIP, note: (M)}
  - {id: cat-move-as-s, msg: UPDATE, from: mmtel, to: scscf, proto: SIP}
  - {id: cat-move-s-p, msg: UPDATE, from: scscf, to: pcscf, proto: SIP}
  - id: cat-move-p-ue
    msg: UPDATE
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - SDP: the SDP of UE 2. The media of UE 1 moves to UE 2
  # The conference URI is a PSI
  - id: psi-sub-ue-p
    msg: SUBSCRIBE
    from: phone1
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`sip:conf-o47ha7ifxyni@conf.ims.mnc001.mcc001.3gppnetwork.org`"
      - Event: "`conference`"
  - {id: psi-sub-p-s, msg: SUBSCRIBE, from: pcscf, to: scscf, proto: SIP}
  - id: psi-naptr
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (query)
    fields:
      - Name: "`conf.ims.mnc001.mcc001.3gppnetwork.org`"
  - {id: psi-naptr-ans, msg: NAPTR, from: dns, to: scscf, proto: DNS, note: (answer)}
  - {id: psi-srv, msg: SRV, from: scscf, to: dns, proto: DNS, note: (query)}
  - id: psi-srv-ans
    msg: SRV
    from: dns
    to: scscf
    proto: DNS
    note: (answer)
    fields:
      - Answer: "port 5060 of `mmtel.ims.mnc001.mcc001.3gppnetwork.org`"
  - {id: psi-sub-s-f, msg: SUBSCRIBE, from: scscf, to: focus, proto: SIP}
  # The focus, and the mix
  - id: mix-conf
    msg: INVITE
    from: focus
    to: mrfc
    proto: SIP
    note: (one per participant)
    fields:
      - Request-URI: "`sip:conf=conf-o47ha7ifxyni@mrfc...`"
      - The first INVITE: makes the mix
      - Each later INVITE: adds a termination
  - {id: mix-up-1, msg: RTP, from: phone1, to: agw, proto: RTP}
  - {id: mix-up-2, msg: RTP, from: phone2, to: agw, proto: RTP}
  - {id: mix-up-3, msg: RTP, from: phone3, to: agw, proto: RTP}
  - {id: mix-up-agw, msg: RTP, from: agw, to: mrfp, proto: RTP, note: (3 streams)}
  - id: mix-sum
    do: makes a sum for each participant
    at: mrfp
    fields:
      - To phone1: the sum of the audio of phone2 and phone3
      - To phone2: the sum of the audio of phone1 and phone3
      - To phone3: the sum of the audio of phone1 and phone2
  - {id: mix-down-agw, msg: RTP, from: mrfp, to: agw, proto: RTP, note: (3 streams)}
  - {id: mix-down-1, msg: RTP, from: agw, to: phone1, proto: RTP}
  - {id: mix-down-2, msg: RTP, from: agw, to: phone2, proto: RTP}
  - {id: mix-down-3, msg: RTP, from: agw, to: phone3, proto: RTP}
  # Coming back to the conference
  - id: rj-inv-ue-p
    msg: INVITE
    from: phone2
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`sip:conf-o47ha7ifxyni@conf...`, the conference URI learnt from the Contact of the focus"
  - {id: rj-inv-p-s, msg: INVITE, from: pcscf, to: scscf, proto: SIP}
  - id: rj-inv-s-as
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - P-Served-User: "`phone2;sescase=orig`"
  - id: rj-inv-as-s
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (B2BUA)
    fields:
      - Request-URI: the same conference URI
  - id: rj-inv-s-f
    msg: INVITE
    from: scscf
    to: focus
    proto: SIP
    fields:
      - Sent to: the focus, found by DNS
      - P-Served-User: none
  - id: rj-tell
    do: tells that it is for the focus
    at: focus
    fields:
      - Looks at: whether P-Served-User is there. Both have the same Request-URI
      - When it is not: the INVITE is for the focus. No filter criterion brought it
  - id: rj-conf
    msg: INVITE
    from: focus
    to: mrfc
    proto: SIP
    fields:
      - Request-URI: "`sip:conf=conf-o47ha7ifxyni@mrfc...`"
      - SDP: the offer of phone2
  - {id: rj-conf-200, msg: 200 OK, from: mrfc, to: focus, proto: SIP}
  - id: rj-200-f-s
    msg: 200 OK
    from: focus
    to: scscf
    proto: SIP
    fields:
      - Contact: "`<sip:conf-o47ha7ifxyni@conf...>;isfocus`"
      - SDP: the answer of the MRFP
  - {id: rj-200-s-as, msg: 200 OK, from: scscf, to: mmtel, proto: SIP}
  - {id: rj-200-as-s, msg: 200 OK, from: mmtel, to: scscf, proto: SIP}
  - {id: rj-200-s-p, msg: 200 OK, from: scscf, to: pcscf, proto: SIP}
  - {id: rj-200-p-ue, msg: 200 OK, from: pcscf, to: phone2, proto: SIP}
```

---

# Announcements, tones and conferences with the MRF

The MRF (the MRFC and the MRFP, announcements, the tone the callee chooses, a conference of three)

---

[layout: summary]

## What this deck covers

To begin: a caller hears a tune while the callee's phone rings, and the callee chose that tune. Which node plays it, and which one decided to? The callee's MMTel AS decides, and the MRFP of the MRF plays it. The AS asks the MRFC over Mr', and the MRFC drives the MRFP.

- What an MRF is, and why it is two nodes: the MRFC and the MRFP.
- How an AS asks for a service: the Request-URIs of RFC 4240, over Mr'.
- How the network gets audio to a caller before the call is answered.
- What to look at for an announcement, for the tone the callee chose, and for a waiting call.
- How a conference of three is made, and who mixes it.

> Book: [Announcements, ringback tones and conferences of the MRF](../../../docs/book/en/mrf.md)
> Guide: [Announcements, alerting tones and conferences with the MRF](../../../docs/guide/en/mrf.md) (`docs/guide/en/mrf.md`)
> The editions are named at the top of the guide, among them TS 23.228 V20.1.0, TS 24.229 V20.1.0, TS 24.182 V19.0.0, TS 24.628 V19.0.0, TS 24.615 V19.0.0, TS 24.147 V19.0.0, TS 23.333 V19.0.0, RFC 4240, RFC 4575 and GSMA IR.92 v24.0.

---

## What an MRF is

```ims-scene
id: roles
nodes: [scscf, mmtel, mrfc, ue1, pcscf, agw, mrfp]
lead: The AS decides what to play, and the MRF makes the audio. The MRF is split into the MRFC on the SIP side and the MRFP on the RTP side.
steps:
  - id: route
    title: The CSCFs route, the AS changes the call
    show: [gm, mw, isc]
    text: Neither makes audio. The node of the IMS that makes media of its own is the MRF (TS 23.228 4.7).
  - id: mrfc
    title: The MRFC speaks SIP
    show: [mr]
    focus: [mrfc]
    text: When an AS asks for a service with an INVITE on Mr', the MRFC decides the media resources that the service needs.
  - id: mrfp
    title: The MRFC drives the MRFP over Mp
    show: [mp]
    focus: [mrfp]
    text: The MRFP plays audio into a termination and adds up the audio of several terminations (TS 23.333 6.2.9).
  - id: media
    title: The RTP between a UE and the MRFP goes through the IMS-AGW
    show: [media-ue, media-mrfp]
    focus: [agw, mrfp]
    text: The MRFP is the same relay as the IMS-AGW, with announcements and the mix added.
```

- The CSCFs route, and the AS of the supplementary services deck changes a call. Neither makes audio. The MRF is the node of the IMS that makes media of its own (TS 23.228 4.7): announcements, tones, and the mix of a conference.
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

```ims-scene
id: early
nodes: [ue1, pcscf, scscf, mmtel, mrfc, agw, mrfp]
lead: The AS answers the caller with the SDP of the MRFP, and decides when the playing starts by when it sends the ACK.
steps:
  - id: invite
    title: The INVITE of UE 1 reaches the AS
    show: [an-inv-ue-p, an-inv-p-s, an-inv-s-as]
    active: an-inv-s-as
    detail: [an-inv-ue-p]
    text: The call of UE 1 reaches the AS of UE 1 by the same path as any other call.
  - id: ask
    title: The AS asks the MRFC, without SDP
    show: [an-annc]
    text: The INVITE that asks for an announcement carries no SDP.
  - id: offer
    title: The MRFC returns an offer of the MRFP
    show: [an-annc-200]
    checkpoint: true
    text: The MRFC puts the offer of a termination of the MRFP in its 200.
  - id: early
    title: The AS answers the offer of the caller with the SDP of the MRFP
    show: [an-183-as-s, an-183-s-p, an-183-p-ue]
    active: an-183-p-ue
    checkpoint: true
    text: |
      To bring audio before the call is answered, the AS answers on its own and authorizes early media.
      This is the gateway model of TS 24.628 Annex G.
  - id: ack
    title: The MRFP starts to play after the ACK
    show: [an-ack, an-play]
    active: an-play
    detail: [an-ack, an-play]
    text: The AS puts the answer in the ACK. The MRFP starts to play after the ACK.
  - id: rtp
    title: The audio reaches UE 1 through the IMS-AGW
    show: [an-rtp-p-agw, an-rtp-agw-ue]
    active: an-rtp-agw-ue
    checkpoint: true
    text: UE 1 plays the audio that comes instead of its own ringback tone.
  - id: bye
    title: When it has played, the MRFC sends a BYE
    show: [an-bye]
    text: |
      When a playing that has an end is over, the MRFC ends the dialog with a BYE.
      So the AS can start the playing once the caller can receive it.
```

- The INVITE for an announcement carries no SDP. The MRFC therefore offers the MRFP's termination in its 200, and the AS sends the answer in the ACK.
- The MRFP starts to play on the ACK (TS 24.229 5.8.2.2.1), and the MRFC ends the dialog with BYE when a play that has an end is over (5.8.3.2.1). The AS thus chooses the moment the play starts, after the caller is ready for it.
- To get the audio to the caller before the call is answered, the AS answers the caller's offer itself with the SDP of the MRFP and authorizes the early media with `P-Early-Media: sendonly` (RFC 5009). This is the gateway model of TS 24.628 Annex G.
- The caller's UE plays what comes instead of a ringing tone of its own (TS 24.628 4.7.2.1).

The filter is `sip.P-Early-Media`.

> Guide: ["How the AS asks: RFC 4240"](../../../docs/guide/en/mrf.md#how-the-as-asks-rfc-4240)

---

## An announcement before a rejection

```ims-scene
id: announce
view: ladder
nodes: [ue1, pcscf, scscf, mmtel, mrfc]
lead: The AS plays an announcement before it rejects the call, and waits for the PRACK before it lets the playing start.
steps:
  - id: invite
    title: UE 1 calls UE 2
    show: [an-inv-ue-p, an-inv-p-s, an-inv-s-as]
    detail: []
    text: The INVITE of UE 1 reaches the AS of UE 1.
  - id: decide
    title: The AS applies the barring
    show: [an-ocb]
    text: UE 1 bars its own outgoing calls, so the AS rejects the call. Before that, it plays `notice`.
  - id: annc
    title: The AS asks the MRFC for an announcement
    show: [an-annc, an-annc-200]
    detail: []
    text: The MRFC answers the INVITE without SDP with an offer of the MRFP.
  - id: early
    title: A 183 brings the SDP of the MRFP to UE 1
    show: [an-183-as-s, an-183-s-p, an-183-p-ue]
    detail: []
    checkpoint: true
    text: "The 183 carries `P-Early-Media: sendonly`. It is a reliable response, so it asks for a PRACK."
  - id: prack
    title: The PRACK of UE 1 reaches the AS
    show: [an-prack-ue-p, an-prack-p-s, an-prack-s-as]
    detail: []
    text: From this the AS knows that UE 1 has the SDP.
  - id: ack
    title: The AS sends the ACK, and the playing starts
    show: [an-ack]
    text: The AS waits for the PRACK before it sends the ACK, so that no frame of the announcement is lost before UE 1 has the SDP.
  - id: bye
    title: The MRFC has played it and sends a BYE
    show: [an-bye]
    checkpoint: true
    text: In the capture of the guide, the ACK to the BYE takes 1.08 s, the length of the WAV file of `notice`.
  - id: reject
    title: The AS rejects the call with a 603
    show: [an-603-as-s, an-603-s-p, an-603-p-ue]
    active: an-603-p-ue
    text: Once the announcement has played, the AS rejects the call of UE 1.
```

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
- In the capture of the guide, the ACK to the BYE takes 1.08 s. That is the length of the WAV file of `notice`: the MRFC sends the BYE when the play ends.

> Guide: ["An announcement before a rejection"](../../../docs/guide/en/mrf.md#an-announcement-before-a-rejection)

---

## CAT: the tone the called user chooses

```ims-scene
id: cat
nodes: [ue1, pcscf, scscf, mmtel, hss, mrfc, ue2, agw, mrfp]
lead: The terminating AS decides the tone. It reads the setting from the HSS for each incoming call, and asks the MRFC before it calls UE 2.
steps:
  - id: arrive
    title: The INVITE reaches the terminating AS
    only: [cat-inv-s-as]
    text: The INVITE from UE 1 reaches the AS for the services of UE 2.
  - id: read
    title: The AS reads the tone setting over Sh
    show: [cat-udr, cat-uda]
    active: cat-udr
    detail: [cat-udr, cat-uda]
    text: |
      The operator sets the tone of a user in the HSS (TS 24.182 4.5.2). Here it is set through the OAM.
      The AS reads it for each incoming call.
  - id: ask
    title: It asks the MRFC before it calls UE 2
    show: [cat-annc, cat-annc-200]
    active: cat-annc
    detail: [cat-annc]
    checkpoint: true
    text: The AS asks the MRFC before its INVITE to UE 2 (4.5.5.3.7). With `repeat=forever`, the MRF plays the tone until the AS ends the dialog.
  - id: call
    title: The AS calls UE 2
    only: [cat-inv-as-s, cat-inv-s-p, cat-inv-p-ue]
    active: cat-inv-p-ue
    detail: []
    text: The INVITE reaches UE 2 through the S-CSCF and the P-CSCF.
  - id: alert
    title: UE 2 starts to alert
    only: [cat-180-ue-p, cat-180-p-s, cat-180-s-as]
    active: cat-180-s-as
    detail: []
    text: The 180 of UE 2 reaches the AS. Before it, the 183 of UE 2 with the precondition table has reached UE 1; the diagram leaves it out.
  - id: tone
    title: An UPDATE brings the SDP of the tone to UE 1
    show: [cat-update-as-s, cat-update-s-p, cat-update-p-ue]
    active: cat-update-p-ue
    checkpoint: true
    text: |
      UE 1 uses preconditions, so the tone comes in an UPDATE after UE 2 starts to alert (4.5.5.3.7 b 1).
      For a caller that does not use preconditions, it goes in the 18x itself.
  - id: play
    title: After the ACK, the MRFP plays the tone
    only: [cat-ack, cat-rtp-p-agw, cat-rtp-agw-ue]
    active: cat-rtp-agw-ue
    checkpoint: true
    text: The audio of the MRFP reaches UE 1 through the IMS-AGW before the answer.
  - id: answer
    title: UE 2 answers, and the AS stops the tone
    only: [cat-200-ue-p, cat-200-p-s, cat-200-s-as, cat-bye]
    active: cat-bye
    detail: []
    checkpoint: true
    text: When the 200 of UE 2 arrives, the AS stops the tone with a BYE to the MRFC.
  - id: move
    title: The media of UE 1 moves to UE 2
    only: [cat-move-as-s, cat-move-s-p, cat-move-p-ue]
    active: cat-move-p-ue
    text: Before it sends the 200 to UE 1, the AS sends an UPDATE with the SDP of UE 2.
```

| Look at | What to see |
| --- | --- |
| The UDR of UE 2's AS | Service-Indication `Customized-Alerting-Tone`, read for each call. |
| The AS's INVITE to the MRFC | `play=/provisioned/cat1;repeat=forever`, before the INVITE goes to UE 2. |
| The 183 that reaches UE 1 | UE 2's answer with the precondition table, as it came. |
| The UPDATE to UE 1, after UE 2's 180 | `P-Early-Media: sendonly` and the SDP of the MRFP, with `a=content:g.3gpp.cat`. |
| At the answer | A BYE to the MRFC, and an UPDATE that moves UE 1's media to UE 2, before the 200. |

- The operator provisions the tone in the HSS (TS 24.182 4.5.2); here it is done through the OAM. The terminating AS reads it over Sh for each call and asks the MRFC before UE 2 is called (4.5.5.3.7).
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

- With the tone off, UE 1 plays its own ringing tone (TS 24.628 4.7.2.1). The two calls are one after the other in the capture of the e2e script.
- Side by side, the tone adds only the dialog with the MRFC, the UPDATE and the RTP of the MRFP; nothing else of the call changes.
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

```ims-scene
id: psi
nodes: [phone1, pcscf, scscf, dns, focus]
lead: The conference URI does not name a node. The S-CSCF finds the focus by DNS, as it finds any other host.
steps:
  - id: subscribe
    title: phone1 subscribes to the conference URI
    show: [psi-sub-ue-p, psi-sub-p-s]
    active: psi-sub-ue-p
    text: The conference URI is a PSI of the home network, in a domain that the operator chooses. It has no port.
  - id: naptr
    title: The S-CSCF looks up the NAPTR of the subdomain
    show: [psi-naptr, psi-naptr-ans]
    active: psi-naptr
    text: The subdomain `conf` stands for all the conferences that the AS serves.
  - id: srv
    title: The SRV answer points at the AS that serves the conferences
    show: [psi-srv, psi-srv-ans]
    active: psi-srv-ans
    checkpoint: true
    text: The S-CSCF finds the focus with RFC 3263. This is the subdomain method of TS 23.228 5.4.12.3.
  - id: deliver
    title: The SUBSCRIBE reaches the focus
    show: [psi-sub-s-f]
    text: "The S-CSCF does not ask the HSS. A request from another network is sent by DNS too: the I-CSCF sends it without asking the HSS (TS 24.229 5.3.2.1 step 3)."
```

- A conference URI is a public service identity of the home network (TS 23.228 4.3.6): `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org`. The operator defines its domain. It names no node and has no port, which a PSI that may leave the network should not have (TS 23.003 13.5).
- The NAPTR and SRV records of that subdomain lead to the AS that hosts the conferences, so the S-CSCF finds the focus as it finds any host, by RFC 3263. This is the subdomain way of TS 23.228 5.4.12.3.
- A request from another network comes in through the I-CSCF, which knows the subdomain and sends it by DNS too, without asking the HSS (TS 24.229 5.3.2.1 step 3).
- The factory URI is a PSI of the originating side (TS 23.228 5.4.12.1): the INVITE to it reaches the AS by the creator's own originating filter criteria, as every INVITE of the creator does.

The filters are `dns.qry.name contains "conf."` and `sip.Contact contains "isfocus"`.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## The focus, and the mix

```ims-scene
id: mix
nodes: [phone1, phone2, phone3, agw, mrfp, mrfc, focus]
lead: The MRFP sends each participant the sum of the audio of the others.
steps:
  - id: ask
    title: The focus asks the MRFC for each participant
    show: [mix-conf]
    text: For the media of the participants, the focus has a dialog with the MRFC for each participant.
  - id: send
    title: Each participant sends its own audio
    show: [mix-up-1, mix-up-2, mix-up-3, mix-up-agw]
    active: mix-up-agw
    detail: []
    text: The RTP reaches the MRFP through the IMS-AGW.
  - id: sum
    title: The MRFP makes a sum for each participant
    show: [mix-sum]
    checkpoint: true
    text: It does not send a participant its own audio back (TS 23.333 6.2.9).
  - id: back
    title: Each sum reaches its participant
    show: [mix-down-agw, mix-down-1, mix-down-2, mix-down-3]
    active: mix-down-agw
    detail: []
    text: The capture shows three streams from the MRFP to the IMS-AGW, and three the other way.
```

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

- Why is the endpoint key not the Contact of the UE? The Contact is the address of that UE, and the NOTIFYs show it to every subscriber, in another network too. A conference whose parties are in both networks is in [the interconnect guide](../../../docs/guide/en/interconnect.md).
- The **Conference** row of each card of the Lab lists what these NOTIFYs say.

The filter is `sip.Event contains "conference"`.

> Guide: ["A conference of three"](../../../docs/guide/en/mrf.md#a-conference-of-three)

---

## Coming back to the conference

```ims-scene
id: rejoin
view: ladder
nodes: [phone2, pcscf, scscf, mmtel, focus, mrfc]
lead: Two INVITEs with the same Request-URI reach the AS. The AS tells which one is for the focus by whether it has P-Served-User.
steps:
  - id: out
    title: phone2 calls the conference URI
    show: [rj-inv-ue-p, rj-inv-p-s, rj-inv-s-as]
    active: rj-inv-s-as
    detail: [rj-inv-ue-p, rj-inv-s-as]
    text: The S-CSCF hands this INVITE to the AS for the originating services of phone2.
  - id: back
    title: The AS sends an INVITE to the same URI, and the S-CSCF finds the focus by DNS
    show: [rj-inv-as-s, rj-inv-s-f]
    active: rj-inv-s-f
    detail: [rj-inv-s-f]
    text: The AS is a B2BUA, so it returns a new INVITE to the same URI to the S-CSCF.
  - id: tell
    title: The focus tells that the INVITE is for it
    show: [rj-tell]
    checkpoint: true
    text: No filter criterion brings an INVITE to the focus, so it has no P-Served-User.
  - id: mix
    title: The focus asks the MRFC for a termination
    show: [rj-conf, rj-conf-200]
    active: rj-conf
    detail: [rj-conf]
    text: It takes the participant in the same way as the creator (TS 24.147 5.3.2.4.1).
  - id: answer
    title: A 200 with the isfocus Contact reaches phone2
    show: [rj-200-f-s, rj-200-s-as, rj-200-as-s, rj-200-s-p, rj-200-p-ue]
    active: rj-200-p-ue
    detail: [rj-200-f-s]
    text: The NOTIFY lists the participant as `connected` again, with the joining-method `dialed-in`.
```

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

## What this project simplifies

| Where | The main items |
| --- | --- |
| MRFC | PCMU only, and no transcoding: an offer without PCMU gets 488 with a Warning. Only provisioned announcements, so no Cr. The Contact is the URI of the instance, not a GRUU. |
| MRFP | The mixer takes PCMU, keeps three frames for jitter, writes no CSRC list and sends no RTCP. |
| AS, the tone | The callee's 183 with preconditions goes to the caller as it came, and the tone starts without a look at the caller's preconditions. |
| AS, the conference | Every served user may create one; there is no conference policy. A REFER that asks the focus to remove a participant is refused with 403. |

- Each item has a comment that starts with `SIMPLIFIED:` in the code.
- Why PCMU only: a conference mixes decoded speech, and there is no AMR decoder here. The UE simulators offer PCMU.

> Guide: ["What this project simplifies"](../../../docs/guide/en/mrf.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Where the code is

| What you saw | Where to look |
| --- | --- |
| The AS's dialog with the MRFC | `mrf.go`, `announcement.go` in `internal/mmtel` |
| The tone at the terminating AS | `cat.go`, `catdata.go` in `internal/mmtel` |
| CW at the AS | `internal/mmtel/waiting.go` |
| The focus and the conference event package | `conference.go`, `conferenceevent.go` in `internal/mmtel` |
| The MRFC, and the Request-URIs of RFC 4240 | `internal/mrfc`, `internal/netann` |
| The MRFP: terminations, announcements, the mixer | `internal/agw` |
| Swap, Merge, and the UE side of a conference | `phone_conference.go`, `conference.go` in `internal/ue` |

- The decisions are in `reserveMedia` of `internal/mmtel/mrf.go` (the INVITE without SDP), `announce` of `internal/mmtel/announcement.go` (the order of the 183, the ACK and the BYE) and `serve` of `internal/mmtel/call.go` (with or without P-Served-User).
- `mrf_network_test.go` and `conference_network_test.go` in `internal/mmtel` run the same flows through the whole IMS in one process.

> Book: [Announcements, ringback tones and conferences of the MRF](../../../docs/book/en/mrf.md). Guide: ["Where the code is"](../../../docs/guide/en/mrf.md#where-the-code-is)

---

## Run it yourself: the announcement and the Lab

What follows is for readers who want to run the setup. Reading does not need it.

```sh
docker compose up -d --build

# An announcement: UE 1 bars its outgoing calls, then calls UE 2.
docker compose run --rm ue xcap set baoc on
docker compose run --rm ue call tel:+12025550102
docker compose run --rm ue xcap set baoc off

# The waiting call and the conference, driven from the Lab.
docker compose --profile phones up -d phone1 phone2 phone3
```

- For the tone, give subscriber 2 the alerting tone `cat1` through the OAM, then call `phone2` from `phone1` in the Lab at http://localhost:8080. The Session row of `phone1` says that it rang with early media from the network.
- In the Lab, a call from `phone3` to `phone1` during a call shows as `waiting`. **Answer**, **Swap** and **Merge** hold, swap and make the conference. `docker compose --profile phones up -d --build` runs the Lab as well.
- `scripts/e2e-mrf.sh` runs everything of this deck and checks it in the capture.

> Guide: ["Run it"](../../../docs/guide/en/mrf.md#run-it) and ["Drive the UE simulators" in Watch the IMS in the Lab](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## Run it yourself: the capture and clean-up

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

- It keeps the calls, the subscriptions and NOTIFYs of the conference, and the control API of the MRFP. **Telephony > RTP > RTP Streams** lists the streams of the MRFP; **Play Streams** plays them. Switch on **Analyze > Enabled Protocols > RTP > rtp_udp** to decode all the RTP.

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/mrfc internal/netann internal/agw
docker compose --profile ue --profile phones down -v --remove-orphans
```

- The first line lists the comments of the simplified places. The second stops the UEs of both profiles and the nodes, and keeps the images.

> Guide: ["Open the capture"](../../../docs/guide/en/mrf.md#open-the-capture). The guides are listed in [docs/guide/](../../../docs/guide/).
