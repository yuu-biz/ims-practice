---
title: The VoLTE call flow
footer: ims-practice 9. VoLTE
lang: en
format: visual
---

```ims-model
nodes:
  - {id: volte10, label: volte10, role: the caller's phone, at: [0, 0]}
  - {id: epc, label: EPC + PCRF, role: the access network stub, at: [1, 2]}
  - {id: volte12, label: volte12, role: the callee's phone, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: its first hop, at: [1, 1]}
  - {id: ue2, label: ue2, role: a softphone, at: [2, 2]}
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: hss, label: HSS, role: makes the vectors, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: authenticates and registers, at: [3, 1]}
  - {id: mmtel, label: MMTel AS, role: the AS of every call, at: [4, 1]}
events:
  # The attach, and finding the P-CSCF
  - id: at-si
    msg: GET /system-information
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - cell: MCC 001, MNC 01, TAC 1, ECI 257
  - id: at-stream
    msg: GET /ues/<IMSI>/events
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - path: "`/ues/001010000000010/events`"
      - what arrives: the messages the network sends by itself
  - id: at-req
    msg: ATTACH REQUEST
    from: volte10
    to: epc
    proto: HTTP
    note: (POST .../attach-request)
    fields:
      - ESM container: PDN CONNECTIVITY REQUEST
      - access_point_name: "`ims`"
      - PCO: asks for the P-CSCF addresses
  - id: at-accept
    msg: ATTACH ACCEPT
    from: epc
    to: volte10
    proto: HTTP
    note: (200)
    fields:
      - eps_bearer_identity: 5, the default bearer for signalling
      - eps_qos: QCI 5, the class of IMS signalling
      - pdn_address: 172.28.0.120, the subscriber's static address
      - PCO: 172.28.0.40 in the container 000CH
  - id: at-complete
    msg: ATTACH COMPLETE
    from: volte10
    to: epc
    proto: HTTP
    fields:
      - ESM container: ACTIVATE DEFAULT EPS BEARER CONTEXT ACCEPT
  - id: ue-pcscf
    do: decides on the P-CSCF address
    at: volte10
    fields:
      - what it checks: that the PDN address is the address it reaches the P-CSCF from
      - what it uses: the first address of the list, and port 5060
  # IMS AKA, the first REGISTER (steps 1 to 6)
  - id: reg1-ue-p
    msg: REGISTER
    from: volte10
    to: pcscf
    proto: SIP
    note: (the first)
    fields:
      - where to: from the UE's 5060 to the P-CSCF's 5060
      - Security-Client: its offer of associations
      - Require: "`sec-agree`, in Proxy-Require too"
      - Authorization: "no algorithm; the nonce and the response are empty"
  - id: pcscf-keep
    do: keeps the Security-Client
    at: pcscf
    fields:
      - the scheme it picks: IMS AKA, because sec-agree is there
      - what it keeps: the Security-Client, taken out
      - added to Authorization: '`integrity-protected="no"`'
  - id: reg1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (the first)
  - id: reg1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (the first)
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - what it saw: '`integrity-protected="no"`: an initial IMS AKA registration'
      - SIP-Auth-Data-Item: "one item of the scheme `Digest-AKAv1-MD5`"
  - id: hss-vector
    do: makes an authentication vector
    at: hss
    fields:
      - what it steps: SQN_HE
      - what it computes: XRES, CK, IK and AUTN, from a RAND with MILENAGE
  - id: maa
    msg: MAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - SIP-Authenticate: RAND and AUTN, 32 octets together
      - SIP-Authorization: XRES
      - Confidentiality-Key: CK
      - Integrity-Key: IK
  - id: scscf-challenge
    do: builds the challenge
    at: scscf
    fields:
      - nonce: RAND and AUTN in base64
      - for the P-CSCF: "the parameters `ik` and `ck`"
      - what it keeps: RAND, XRES, IK and CK
  - id: r401-s-i
    msg: "401"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - WWW-Authenticate: "one per algorithm: `AKAv2-SHA-256` then `AKAv1-MD5`, each with the nonce, `ik`, `ck`"
  - id: r401-i-p
    msg: "401"
    from: icscf
    to: pcscf
    proto: SIP
  - id: pcscf-sa
    do: takes the keys out and adds its own offer
    at: pcscf
    fields:
      - what it takes out: "`ik` and `ck`: the keys never travel to the UE"
      - what it adds: the Security-Server; its protected server port is 5064
  - id: r401-p-ue
    msg: "401"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - where it arrives: the UE's port 5060
  # IMS AKA, the second REGISTER (steps 7 to 12)
  - id: ue-verify
    do: checks the network
    at: volte10
    fields:
      - what it compares: the MAC in AUTN and the MAC the USIM computed
      - the other check: that the SQN is fresh
      - what it computes: RES, CK, IK
  - id: reg2-ue-p
    msg: REGISTER
    from: volte10
    to: pcscf
    proto: SIP
    note: (the second)
    fields:
      - where to: from the UE's 5062 to the P-CSCF's 5064
      - Security-Verify: a copy of the P-CSCF's Security-Server
      - Authorization: "`algorithm=AKAv2-SHA-256`, the response with RES, IK and CK in the password"
  - id: pcscf-verify
    do: checks the agreement
    at: pcscf
    fields:
      - where it arrived: the protected server port 5064
      - Security-Verify: the same as the Security-Server it sent
      - added to Authorization: '`integrity-protected="yes"`'
  - id: reg2-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (the second)
  - id: reg2-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (the second)
  - id: scscf-verify
    do: checks the RES
    at: scscf
    fields:
      - the computation: the Digest of the algorithm the UE named, from XRES, IK and CK
      - when they match: an SAR tells the HSS of the registration
  - id: sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
  - id: saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: ok-p-ue
    msg: 200 OK
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - where to: from the P-CSCF's 5064 to the UE's 5062
      - from now on: the UE sends every request to port 5064
      - P-Associated-URI: the identities that are not barred, the default first
  # The signalling path over Rx
  - id: aar-sig
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (the signalling path)
    fields:
      - Flow-Usage: AF_SIGNALLING
      - Media-Component-Number: 0, and Flow-Number 0 too
      - Specific-Action: "2 and 4: loss and release of a bearer"
      - Framed-IP-Address: 172.28.0.120
  - id: aaa-sig
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (the signalling path)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - what the PCRF did: bound the session to the IP-CAN session of that address
  - id: reg-ue2
    msg: REGISTER
    from: ue2
    to: pcscf
    proto: SIP
    note: (a softphone)
    fields:
      - authentication: SIP Digest and a password
      - access network: it did not attach
  - id: aar-ue2
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (the signalling path of ue2)
    fields:
      - Framed-IP-Address: 172.28.0.101
  - id: aaa-ue2
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (the signalling path of ue2)
    fields:
      - Experimental-Result-Code: IP-CAN_SESSION_NOT_AVAILABLE (5065)
  - id: pcscf-nopolicy
    do: serves the UE without policy control
    at: pcscf
    fields:
      - for this UE: it sends no AAR for a call either
  # The media of a call, and the dedicated bearer
  - id: inv-10-p
    msg: INVITE
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550112`"
      - Supported: "`100rel, timer, 199, precondition`, not Require"
      - SDP: the offer; AMR-WB first, PCMU last
      - "curr:qos local": "`none`: no resources reserved yet"
      - "des:qos": "local `mandatory`, remote `optional`"
  - id: inv-p-12
    msg: INVITE
    from: pcscf
    to: volte12
    proto: SIP
    note: (after both S-CSCFs and the MMTel AS)
  - id: r183-12-p
    msg: "183"
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - Require: "`100rel`, `precondition`"
      - SDP: "the answer: `m=audio 60734 RTP/AVP 96 100` and `b=AS:41`"
      - "conf:qos": "`remote sendrecv`: tell me when the caller has its resources"
      - "des:qos": raises the caller's segment to mandatory too
  - id: aar-12
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (the callee's media)
    fields:
      - AF-Application-Identifier: "`urn:urn-7:3gpp-service.ims.icsi.mmtel`"
      - Media-Sub-Component: "flows 1 and 2: RTP and RTCP"
      - Flow-Description: "`permit in 17 from 172.28.0.121 to 172.28.0.50 40004`"
      - Max-Requested-Bandwidth-UL: "80000, from `b=AS`, in bit/s"
      - Rx-Request-Type: INITIAL_REQUEST
  - id: pcrf-grant
    do: authorises the audio
    at: epc
    fields:
      - QCI: 1, the guaranteed bit rate class of conversational voice
      - the rates: from the bandwidth the P-CSCF asked for, in kbit/s
  - id: aaa-12
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (the callee's media)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: bearer-12
    msg: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte12
    proto: HTTP
    fields:
      - eps_bearer_identity: 6, linked to the default bearer 5
      - eps_qos: "QCI 1; 80 up, 41 down, in kbit/s"
      - tft: "RTP and RTCP between the UE and the relay, each way: four filters"
  - id: ue12-reserved
    do: takes its resources as reserved
    at: volte12
    fields:
      - its answer: "`activate-dedicated-eps-bearer-context-accept`"
      - preconditions: its own segment becomes sendrecv
  - id: aar-10
    msg: AAR
    from: pcscf
    to: epc
    proto: Rx
    note: (the caller's media)
    fields:
      - what triggers it: the 183 on its way to volte10, with the same answer
      - Framed-IP-Address: 172.28.0.120
  - id: aaa-10
    msg: AAA
    from: epc
    to: pcscf
    proto: Rx
    note: (the caller's media)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: bearer-10
    msg: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte10
    proto: HTTP
    fields:
      - tft: the far address is the relay, 172.28.0.50
  - id: ue10-reserved
    do: takes its resources as reserved
    at: volte10
    fields:
      - what it sends next: an UPDATE, a new offer that says so
  - id: r183-p-10
    msg: "183"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - when it goes: once the caller's AAR has its answer
  # Preconditions
  - id: prack
    msg: PRACK
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - RAck: the RSeq of the 183 and the CSeq of the INVITE
  - id: prack-ok
    msg: 200 (PRACK)
    from: pcscf
    to: volte10
    proto: SIP
  - id: update-10-p
    msg: UPDATE
    from: volte10
    to: pcscf
    proto: SIP
    fields:
      - "curr:qos local": "`sendrecv`: the dedicated bearer is there"
      - when it goes: once the PRACK has its 200
  - id: update-p-12
    msg: UPDATE
    from: pcscf
    to: volte12
    proto: SIP
  - id: update-ok-12
    msg: 200 (UPDATE)
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - "curr:qos local": "`sendrecv`"
      - "curr:qos remote": "`sendrecv`: both segments are there"
  - id: update-ok-10
    msg: 200 (UPDATE)
    from: pcscf
    to: volte10
    proto: SIP
  - id: pre-met
    do: every mandatory precondition is met
    at: volte12
    fields:
      - a row of the table: a segment (local or remote) and a direction (send or recv)
      - when it may alert: every mandatory row is current
  - id: r180-12-p
    msg: "180"
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - only now: the resources are there, so it starts to alert
  - id: r180-p-10
    msg: "180"
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - at the caller: it may arrive before the 200 to the UPDATE
  - id: ok-inv-12
    msg: 200 (INVITE)
    from: volte12
    to: pcscf
    proto: SIP
    fields:
      - SDP: "none; the answer came in the 183"
  - id: ok-inv-10
    msg: 200 (INVITE)
    from: pcscf
    to: volte10
    proto: SIP
  - id: ack
    msg: ACK
    from: volte10
    to: pcscf
    proto: SIP
  # The network takes the bearer
  - id: epc-deact
    do: deactivates the voice bearer
    at: epc
    fields:
      - the procedure: a deactivation of an EPS bearer context by the network
      - what starts it: a DELETE on the bearer, from the Lab's button or `curl`
  - id: deact-10
    msg: DEACTIVATE EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte10
    proto: HTTP
    note: (on the event stream)
  - id: asr
    msg: ASR
    from: epc
    to: pcscf
    proto: Rx
    fields:
      - Abort-Cause: BEARER_RELEASED (0)
      - why not an RAR: the voice bearer carried all the media of this session
  - id: asa
    msg: ASA
    from: pcscf
    to: epc
    proto: Rx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: str
    msg: STR
    from: pcscf
    to: epc
    proto: Rx
    note: (then the STA)
    fields:
      - what it ends: the Rx session of this call
  - id: pcscf-bye
    do: builds the BYEs from the dialog it kept
    at: pcscf
    fields:
      - what from: the Contact, the tags, the CSeq, the route
      - where to: the other party, and the served UE
  - id: bye-core
    msg: BYE
    from: pcscf
    to: scscf
    proto: SIP
    note: (in volte10's name)
    fields:
      - Reason: '`SIP;cause=503;text="Service Unavailable"`'
  - id: bye-10
    msg: BYE
    from: pcscf
    to: volte10
    proto: SIP
    fields:
      - Reason: "`SIP;cause=503`"
      - this project's addition: it tells a UE that still has its signalling
  - id: bye-12
    msg: BYE
    from: pcscf
    to: volte12
    proto: SIP
    note: (after the S-CSCF and the MMTel AS)
  - id: deact-12
    msg: DEACTIVATE EPS BEARER CONTEXT REQUEST
    from: epc
    to: volte12
    proto: HTTP
    note: (on the event stream)
    fields:
      - before it: the P-CSCF of volte12's side ends its session with an STR
```

---

# The VoLTE call flow

(the access network, IMS AKA, policy control over Rx, and the VoLTE call)

---

[layout: summary]

## What this deck covers

To begin: when you call from a mobile phone, what does the network prepare before the other phone rings? A voice bearer for each phone. The callee rings only once the resources of both sides are there.

- Where a VoLTE phone differs from the softphone that registers with SIP Digest.
- How the UE attaches to the EPC and is given the P-CSCF address.
- How the UE and the network prove themselves to each other with IMS AKA.
- Which identities a phone without an ISIM makes from the IMSI, and which of them the network bars.
- How the P-CSCF asks the PCRF for voice resources over Rx, and the network sets up a bearer.
- How preconditions keep the callee from ringing until the resources are there.
- How the P-CSCF ends the call when the network loses the bearer.

> Guide: [The VoLTE call flow](../../../docs/guide/en/volte.md) (`docs/guide/en/volte.md`)
> The editions are named at the top of the guide, among them TS 24.229 V20.1.0, TS 29.214 V20.0.0, TS 24.301 V20.1.0, TS 33.203, TS 33.102, TS 35.206, TS 35.208, RFC 3310, RFC 3329, RFC 3312, RFC 4867 and GSMA IR.92 v24.0.
> Book: [Registration and calls over VoLTE](../../../docs/book/en/volte.md), [Mobile access and other operators' networks](../../../docs/book/en/access-and-borders.md)

---

## What changes from the softphone's call

| | The softphone (SIP Digest, PCMU) | The VoLTE phone |
| --- | --- | --- |
| Access network | None; the P-CSCF is configured | Attaches to the EPC, which names the P-CSCF |
| Authentication | SIP Digest and a password | IMS AKA and sec-agree; no password |
| Resources | Not considered | The P-CSCF asks the PCRF over Rx |
| Ringing | Right after the 183 | Once both sides have their bearers |
| Speech | PCMU over RTP | AMR-WB, RTCP, telephone-events |
| Release | Only the UE's BYE | The P-CSCF, when the network loses the bearer |
| Identities | Configured, and used as registered | Made from the IMSI; the one registered is barred |

- What goes wrong if everything stays as in the left column? The callee rings before the voice has a path, and answers into silence.
- The softphones keep working: a UE that did not come through the EPC stub is unknown to the PCRF, and the P-CSCF serves it without policy control.

> Guide: ["How a VoLTE phone differs from a softphone"](../../../docs/guide/en/volte.md#how-a-volte-phone-differs-from-a-softphone). IR.92 4.4, 2.4.1, 2.4.2.3, 3.2.1. TS 33.203 6.1.1, 7. TS 29.214 4.4.1. TS 24.229 5.2.8.1.2. RFC 3312.

---

## The nodes

```ims-scene
id: nodes
nodes: [volte10, epc, volte12, pcscf, ue2, icscf, hss, scscf, mmtel]
lead: "What a VoLTE phone adds are the three on the left: the access network, and the two phones on it."
steps:
  - id: volte10
    title: volte10 (172.28.0.120)
    focus: [volte10]
    text: |
      The caller. Its number is `tel:+12025550110`, and the USIM of subscriber 10 holds K and OPc.
  - id: volte12
    title: volte12 (172.28.0.121)
    focus: [volte12]
    text: The callee. Its number is `tel:+12025550112`, and it answers by itself after its ring time.
  - id: epc
    title: The EPC stub and the PCRF (172.28.0.80)
    focus: [epc]
    checkpoint: true
    text: |
      One node, of the role `epc`. It answers the phones on HTTP 8080 and the P-CSCF on Rx 3868.
      In a real network the PDN GW and the PCRF are separate nodes, with Gx between them.
  - id: pcscf
    title: The P-CSCF (172.28.0.40)
    focus: [pcscf]
    text: |
      It listens on SIP port 5060 and, for the phones that use IMS AKA, on the protected server port 5064.
      It is also the Rx client towards the PCRF.
  - id: core
    title: The I-CSCF, the S-CSCF and the HSS (172.28.0.20, 30, 10)
    focus: [icscf, scscf, hss]
    checkpoint: true
    text: |
      The S-CSCF asks for authentication with IMS AKA, and the HSS makes the vector.
      Both the first and the second REGISTER go through the I-CSCF.
  - id: others
    title: ue2 and the MMTel AS (172.28.0.101, 75)
    focus: [ue2, mmtel]
    text: |
      ue2 is a softphone that does not attach, and it offers PCMU only.
      The MMTel AS is the one of the deck on supplementary services, and every VoLTE call goes through it.
  - id: added
    title: The three nodes that VoLTE adds
    focus: [volte10, epc, volte12]
    checkpoint: true
    text: |
      The two VoLTE phones, and the EPC stub that stands for the access network.
      The nodes from the P-CSCF on are those of the softphone's registration and calls.
```

| Node | Address | What it does in a VoLTE call |
| --- | --- | --- |
| volte10 | 172.28.0.120 | The caller, `tel:+12025550110`. Subscriber 10. |
| volte12 | 172.28.0.121 | The callee, `tel:+12025550112`. Subscriber 12. |
| ue2, SIPp | 172.28.0.101, 102 | A PCMU-only softphone, and the IMS AKA peer. |
| EPC stub and PCRF | 172.28.0.80 | The `epc` role: the UE API on HTTP 8080, Rx on 3868. |
| P-CSCF | 172.28.0.40 | Port 5060, and the protected server port 5064. The Rx client. |
| S-CSCF, HSS | 172.28.0.30, 10 | The IMS AKA challenge, and the vectors. |
| MMTel AS, relay | 172.28.0.75, 50 | As in the deck on supplementary services. The relay passes RTCP too. |

- volte10 and volte12 are services of the profile `volte` in `compose.yaml`. Their keys are in `deploy/compose/hss.yaml`; `deploy/compose/epc.yaml` gives each subscriber the address of its container.
- The Lab (172.28.0.60) drives volte10 and volte12, shows Rx in the ladder, and passes on to the host the EPC stub's paths that start a procedure of the network.

> Guide: ["The nodes"](../../../docs/guide/en/volte.md#the-nodes)

---

## What the EPC stub keeps, and what it leaves out

```text
 UE ==(HTTP + JSON: ATTACH REQUEST, ...)==> EPC stub: PDN GW and PCRF (epc role)
 |                                           ^
 |                                           |  Rx (Diameter)
 +------------ SIP ------------> P-CSCF -----+
```

- The messages and their fields are the ESM and EMM messages of TS 24.301 (8.2, 8.3), in lower case in the URL, such as `POST /ues/001010000000010/attach-request`.
- What the network sends comes on the UE's event stream, `GET /ues/<IMSI>/events`, as server-sent events.
- Each subscriber has a static address (TS 23.401 5.3.1.1). Nothing is tunnelled; the packets go straight on.
- No radio, no S1, no NAS encoding or security, no Gx: the PCRF and the PDN GW share the state inside the `epc` role. A bearer is a record; no QoS is enforced and no packet filtered.
- Only Rx is real Diameter, because the IMS side is what a learner reads in Wireshark. The names of the NAS messages let the attach be read without a radio or a NAS decoder.

> Guide: ["What the stub leaves out"](../../../docs/guide/en/volte.md#what-the-stub-leaves-out)

---

## Attach, and finding the P-CSCF

```ims-scene
id: attach
view: ladder
nodes: [volte10, epc, pcscf]
lead: The phone is given the P-CSCF address by the access network. It is in no configuration file.
steps:
  - id: cell
    title: It reads the information of the cell
    show: [at-si]
    text: |
      With no radio to broadcast it, the stub made the system information an HTTP resource.
      The phone writes these values into P-Access-Network-Info.
  - id: stream
    title: The event stream opens before the attach
    show: [at-stream]
    checkpoint: true
    text: |
      What the network sends by itself arrives bit by bit in this response, which stays open.
      It opens first, so that what the network sends right after the attach arrives too.
  - id: request
    title: The ATTACH REQUEST connects to the IMS APN
    show: [at-req]
    text: The EMM ATTACH REQUEST carries the ESM PDN CONNECTIVITY REQUEST. The PCO asks for the P-CSCF address.
  - id: accept
    title: The ATTACH ACCEPT gives an address and a bearer
    show: [at-accept]
    checkpoint: true
    text: |
      The default bearer is QCI 5, the one for IMS signalling.
      The P-CSCF address is in the PCO, in the container with the identifier 000CH.
  - id: complete
    title: The ATTACH COMPLETE ends the attach
    show: [at-complete]
    text: "The PDN connection is up. Nothing is tunnelled: the phone's packets go straight to their peer."
  - id: decide
    title: The phone decides on the P-CSCF
    show: [ue-pcscf]
    text: |
      It uses the first address of the list only. The PCO has no port, so it sends to the standard 5060.
  - id: register
    title: The first REGISTER goes to that address
    show: [reg1-ue-p]
    detail: []
    checkpoint: true
    text: The registration starts here. The next scene reads it one hop at a time.
```

```ladder
volte10, EPC stub
volte10 -> EPC stub: GET /system-information // the cell: MCC 001, MNC 01, TAC 1
volte10 -> EPC stub: GET /ues/001010000000010/events // the event stream stays open
volte10 -> EPC stub: POST /ues/001010000000010/attach-request // ATTACH REQUEST, APN ims
volte10 <-- EPC stub: 200, ATTACH ACCEPT with ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
volte10 -> EPC stub: POST /ues/001010000000010/attach-complete
```

| In the ATTACH ACCEPT | What to look at |
| --- | --- |
| `eps_bearer_identity` 5, `qci` 5 | The default bearer, for IMS signalling. |
| `pdn_address` | The subscriber's static address, 172.28.0.120. |
| The PCO container `000CH` | The P-CSCF address, 172.28.0.40. Port 5060. |

> Guide: ["Attach"](../../../docs/guide/en/volte.md#attach). IR.92 4.4.1, 4.5. TS 23.203 6.1.7. TS 24.008 10.5.6.3. TS 24.229 L.2.2.1, 7.2A.4.3, L.3.1.1.

---

## Attach: what else to see

- In the capture, `http && ip.addr == 172.28.0.80` finds the exchanges with the EPC stub. The messages are the JSON of the HTTP bodies.
- The P-CSCF in the PCO is method II of TS 24.229 L.2.2.1, which IR.92 4.5 asks for. The first address of the list is the preferred one.
- The port is the standard 5060, so that Wireshark decodes SIP without a Decode As.
- The UE checks that the PDN address is the address it reaches the P-CSCF from, and stops if not.
- The UE writes the cell into P-Access-Network-Info: in `3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101` come MCC 001, MNC 01, the TAC in 4 hexadecimal digits and the ECI in 7.

---

## The boundary between the UE and the access network, in the code

[internal/ue/access.go](../../../internal/ue/access.go)

```go
type AccessNetwork interface {
	// Attach connects the UE to the IMS APN and returns what the network
	// gave it (TS 24.229 L.2.2.1).
	Attach(ctx context.Context) (Attachment, error)
	// Events delivers what the network tells the UE after the attach. It
	// is closed once the UE has detached or the network has detached it.
	Events() <-chan AccessEvent
	// Detach disconnects the UE from the network.
	Detach(ctx context.Context) error
}
```

- The one implementation is a client of the EPC stub's HTTP API (`access_epc.go`).
- The events are three: a bearer set up, a bearer released, a detach by the network.
- The UE sees the access network only through these three methods, so a NAS implementation could stand behind the same interface without a change to the rest of the UE.
- The boundary on the P-CSCF's side is `PolicyControl` (`internal/policy`). The N5 of a 5GC can stand behind it in place of Rx. A 5GC stub and the N5 parts (`internal/fivegc`, `internal/n5`) exist, but no node runs them. In 5GC, SIP signalling uses a QoS flow of 5QI 5 and voice one of 5QI 1, the same idea as QCI 5 and QCI 1 in EPS.

---

## The keys of IMS AKA

| Who | Holds | Where |
| --- | --- | --- |
| The USIM of volte10 | K, OPc and SQN_MS | `-k`, `-opc`, `-sqn` of `ue` (`compose.yaml`) |
| The HSS | K, OPc, AMF and SQN_HE of the same subscriber | `aka:` of subscriber 10 in `deploy/compose/hss.yaml` |

- K and OPc are those of MILENAGE test set 1 (TS 35.208 4.3.1).
- OPc is derived from the operator's OP and K (TS 35.206 4.1). `-op` takes the OP instead, as SIPp does.
- Nothing else is shared. The network and the UE each compute the rest from these and the random challenge. There is no password anywhere, unlike the subscribers that register with SIP Digest.
- volte12 has the K and OPc of test set 3 (4.3.3). SIPp 3.7 takes K and OP as the bytes of their text, so subscriber 11, for SIPp, has printable keys; the guide's section "SIPp" has them.

> Guide: ["The keys"](../../../docs/guide/en/volte.md#the-keys)

---

## The identities of a phone without an ISIM

| Identity | Value | Where it appears |
| --- | --- | --- |
| Private user identity | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | `username` of Authorization, User-Name on Cx. |
| Temporary public user identity | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | From and To of REGISTER, Public-Identity on Cx. Nowhere else. |
| Default public user identity | `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` | The first P-Associated-URI of the 200 OK. Then From and P-Asserted-Identity. |
| The tel URI of the number | `tel:+12025550110` | The second P-Associated-URI. |

- The card of volte10 and volte12 has a USIM and no ISIM. A USIM holds the IMSI and the keys but no IMS identity, so the phone makes its identities from the IMSI, here 001010000000010.
- The temporary identity carries the IMSI, so the HSS bars it: it serves registration only, and only the CSCFs and the HSS see it. It is under `barred_identities` in `deploy/compose/hss.yaml`, and the user profile in the SAA marks it with `<BarringIndication>`.

> Guide: ["The identities of a phone without an ISIM"](../../../docs/guide/en/volte.md#the-identities-of-a-phone-without-an-isim). TS 24.229 5.1.1.1A. TS 23.003 13.2, 13.3, 13.4B. TS 23.228 E.3.1. TS 29.228 B.2.1.

---

## The identities in the capture

```text
REGISTER  volte10 -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte10  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte10 -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
```

- The S-CSCF lists in P-Associated-URI only the identities that are not barred, the default first. The UE keeps the first as its default public identity; the one it registered is missing, so it takes that one as barred.
- The UE sends no P-Preferred-Identity, so the P-CSCF asserts the default identity. A request from a barred identity gets 403, and one to it 404.
- `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` finds no packet. No Contact carries the IMSI either: its user part is a UUID. A softphone has no SIM: its identity is configured, as an IMC holds it, and it uses the identity it registered.

> Guide: ["The identities of a phone without an ISIM"](../../../docs/guide/en/volte.md#the-identities-of-a-phone-without-an-isim). TS 24.229 5.4.1.2.2F, 5.1.1.2.1, 5.2.6.3.1, 5.4.3.2, 5.4.3.3, 5.1.1.1B.1.

---

## The first REGISTER of IMS AKA, and the 401 (1 to 6)

```ims-scene
id: challenge
nodes: [volte10, pcscf, icscf, scscf, hss]
lead: What the HSS hands over is no password, but an authentication vector for one use.
steps:
  - id: send
    title: The REGISTER that nothing protects
    only: [reg1-ue-p]
    checkpoint: true
    text: |
      This is the only REGISTER that the phone sends to port 5060.
      The nonce and the response of Authorization are empty: it says only who is to be authenticated.
  - id: keep
    title: The P-CSCF keeps the Security-Client
    show: [pcscf-keep]
    zoom: [volte10, pcscf]
    checkpoint: true
    text: |
      It sees the Security-Client of sec-agree and picks IMS AKA.
      It takes the header out, keeps it, and says with `integrity-protected` that nothing protects this one yet.
  - id: forward
    title: Through the I-CSCF to the S-CSCF
    show: [reg1-p-i, reg1-i-s]
    active: reg1-i-s
    checkpoint: true
    text: |
      The I-CSCF asks with a UAR and a UAA which S-CSCF serves the user. That is as in the deck on registration, so the diagram leaves it out.
  - id: mar
    title: The S-CSCF asks for a vector (MAR)
    only: [reg1-i-s, mar]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      It does not leave the scheme `Unknown`: it names IMS AKA.
      It can, because it saw the `integrity-protected="no"` that the P-CSCF added.
  - id: vector
    title: The HSS computes the vector
    show: [hss-vector]
    zoom: [icscf, hss, scscf]
    text: It steps SQN_HE once, picks a new RAND, and makes five values from the same keys as the USIM.
  - id: maa
    title: The HSS returns the challenge and the answer (MAA)
    show: [maa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      RAND and AUTN are the challenge for the phone, XRES is what the answer is checked against, CK and IK are the keys of the associations.
      There is no password anywhere.
  - id: nonce
    title: The S-CSCF builds the challenge
    show: [scscf-challenge]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      RAND and AUTN go into the nonce in base64, as they are. The keys go into separate parameters, for the P-CSCF.
      One WWW-Authenticate per algorithm offered, the preferred one first: AKAv2-SHA-256, then AKAv1-MD5 for backward compatibility.
  - id: back
    title: The 401 comes back to the P-CSCF
    only: [r401-s-i, r401-i-p]
    active: r401-i-p
    text: The 401 is no failure but a step of the procedure. Each node takes its own Via out.
  - id: strip
    title: The P-CSCF takes the keys out and adds its own offer
    show: [pcscf-sa]
    checkpoint: true
    text: |
      `ik` and `ck` end here. The phone computes the same keys from its K and the RAND, so they need not travel.
  - id: ue
    title: The 401 reaches the phone
    show: [r401-p-ue]
    text: Both ways run over port 5060. The protected ports come with the next REGISTER.
```

| # | Hop | What to look at |
| --- | --- | --- |
| 698 | UE:5060 → P-CSCF:5060 | `Security-Client`, `Require: sec-agree`. Empty nonce and response. |
| 709 | P-CSCF → I-CSCF | Security-Client taken out and kept. `integrity-protected="no"`. |
| 724 | S-CSCF → HSS (MAR) | Asks for one item of the scheme `Digest-AKAv1-MD5`, the scheme of both AKA versions. |
| 726 | HSS → S-CSCF (MAA) | RAND and AUTN, XRES, CK, IK. |
| 727 | S-CSCF → I-CSCF (401) | Two WWW-Authenticate, `AKAv2-SHA-256` first. The nonce is RAND and AUTN in base64. `ik` and `ck`. |
| 737 | P-CSCF → UE (401) | `ik` and `ck` taken out, `Security-Server` added. |

> Guide: ["The ladder"](../../../docs/guide/en/volte.md#the-ladder), steps 1 to 6. TS 24.229 5.1.1.2.2, 5.2.2.1, 5.2.2.2, 5.4.1.1, 5.4.1.2.1A. RFC 3329 2.3.1. TS 33.203 Annex H, 7.2. TS 29.229 6.3.9. TS 33.102 6.3.2. TS 29.228 6.3. RFC 3310 3.2.

---

## Checking the network and answering with IMS AKA (7 to 12)

```ims-scene
id: answer
nodes: [volte10, epc, pcscf, icscf, scscf, hss]
lead: The phone checks the network too. SIP Digest did not have that direction.
steps:
  - id: verify
    title: The phone checks the AUTN
    only: [r401-p-ue, ue-verify]
    active: ue-verify
    zoom: [volte10, pcscf]
    checkpoint: true
    text: |
      A matching MAC shows that the network knows the same K.
      It also checks that the SQN is fresh, and only then computes RES, CK and IK.
  - id: respond
    title: The second REGISTER goes over the associations
    show: [reg2-ue-p]
    zoom: [volte10, pcscf]
    checkpoint: true
    text: |
      The ports change to the protected ones. The response is a Digest with RES as the password.
      Security-Verify is there so that the P-CSCF can check that nobody removed a stronger offer.
  - id: check
    title: The P-CSCF checks the agreement
    show: [pcscf-verify]
    zoom: [volte10, pcscf]
    checkpoint: true
    text: The port it arrived on and the Security-Verify agree, so it takes the request as protected.
  - id: forward
    title: Through the I-CSCF to the S-CSCF again
    show: [reg2-p-i, reg2-i-s]
    active: reg2-i-s
    checkpoint: true
    text: The second UAR and UAA are as in the deck on registration too. The diagram leaves them out.
  - id: res
    title: The S-CSCF checks the RES
    only: [reg2-i-s, scscf-verify]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: It builds the password of the algorithm the UE named from the XRES, IK and CK it kept, computes the same Digest, and compares it with the response that came.
  - id: sar
    title: It tells the HSS of the registration (SAR and SAA)
    show: [sar, saa]
    active: sar
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: "From here on it is as in the deck on registration: the profile in the SAA holds the implicitly registered identities and the iFCs."
  - id: ok
    title: The 200 OK comes back to the phone
    only: [ok-s-i, ok-i-p, ok-p-ue]
    active: ok-p-ue
    checkpoint: true
    text: |
      The 200 OK comes back over the pair of protected ports, and the associations are established.
      From now on the phone sends every request to port 5064.
  - id: aar
    title: The P-CSCF watches the signalling path
    show: [aar-sig]
    detail: []
    text: |
      For a phone that is registered, it asks the PCRF to tell it when the signalling bearer is lost.
      The Rx part of this deck reads what is inside.
  - id: aaa
    title: The PCRF binds it to the IP-CAN session
    show: [aaa-sig]
    detail: []
    text: The 200 OK and this AAR leave the P-CSCF in either order.
```

| # | Hop | What to look at |
| --- | --- | --- |
| 740 | UE | Checks the MAC of AUTN and the SQN; computes RES, CK, IK. |
| 740 | UE:5062 → P-CSCF:5064 | `Security-Verify` mirrors the Security-Server. `algorithm=AKAv2-SHA-256` and its response. |
| 749 | P-CSCF → I-CSCF | Agreement checked: `integrity-protected="yes"`. |
| 760, 764 | S-CSCF, SAR | Computes the Digest of the algorithm the UE named and compares. They match: registered. |
| 783 | P-CSCF:5064 → UE:5062 | The 200 OK. The associations are established. |
| 786 | P-CSCF → PCRF | AAR: the signalling path (in the Rx part). |

> Guide: ["The ladder"](../../../docs/guide/en/volte.md#the-ladder), steps 7 to 12. TS 33.102 6.3.3, C.2. TS 33.203 7.1. RFC 3329. RFC 3310 3.3. TS 24.229 5.4.1.2.2, 5.2.2.2. TS 29.214 A.4.

---

## IMS AKA: what the two parts show

- The first REGISTER is the only one the UE sends to the unprotected port 5060. After the 200 OK the UE sends every request to the protected port 5064 of the P-CSCF, and the P-CSCF sends to the UE at 5062.
- The P-CSCF takes `ik` and `ck` out of the 401, so the keys never travel to the UE. The UE computes the same keys from its K and the RAND.
- A matching MAC in AUTN shows that the network knows K: the network is authenticated too, a direction that SIP Digest did not have. The UE also checks that the SQN is fresh.
- Security-Verify lets the P-CSCF check that no man in the middle removed a stronger offer. The 200 OK and the AAR #786 leave the P-CSCF in either order.
- A refresh and the deregistration go over the associations without a new challenge (TS 24.229 5.1.1.4.2, 5.1.1.6.2).
- Filters: `sip.Security-Client || sip.Security-Server || sip.Security-Verify`; `sip.WWW-Authenticate contains "AKAv2-SHA-256" || sip.WWW-Authenticate contains "AKAv1-MD5"` (with `contains "ck="`, only the 401 from the S-CSCF stays); `udp.port == 5064 || tcp.port == 5064` for the protected port.

---

## The authentication vector, in the code

[internal/aka/aka.go](../../../internal/aka/aka.go)

```go
func NewAuthenticationVector(secret Secret, rand RAND, sqn SQN, amf AMF) AuthenticationVector {
	m := secret.milenage()
	mac := m.F1(rand, sqn.octets(), amf)
	res := m.F2(rand)
	return AuthenticationVector{
		RAND: rand,
		XRES: res[:],
		CK:   m.F3(rand),
		IK:   m.F4(rand),
		AUTN: newAUTN(conceal(sqn, m.F5(rand)), amf, mac),
	}
}
```

- f1 to f5 are the MILENAGE functions (TS 35.206). AUTN is SQN ⊕ AK (6 octets), AMF (2 octets) and MAC-A (8 octets).
- The nonce of the 401, decoded from base64, is the same 32 octets as the SIP-Authenticate of the MAA. The first 16 octets are RAND and the rest AUTN, whose octets 7 and 8 are the AMF, 0000.
- The RAND of a capture is random, so its outputs are not those of TS 35.208. The test sets check the computation itself: the test `TestAuthenticationVectorFromTestSet1` checks the set of 4.3.1, and `TestConformanceTestSets` all 20 sets of 4.3.

> Guide: ["Check the vector by hand"](../../../docs/guide/en/volte.md#check-the-vector-by-hand). TS 33.102 6.3.2. TS 35.208 4.3.1.

---

## The security agreement without ESP

- The standard protects everything after the 401 with IPsec ESP (TS 33.203 7.1).
- By default this project agrees on the associations, keeps them, and uses the protected ports, but sets up no ESP, so that the captures stay readable. The standard has no IMS AKA without protection: this is a simplification of this project's own. Without IPsec the standard has only AKAv2-SHA-256 over TLS (TS 24.229 4.2B.1).
- The setup in `deploy/esp` puts ESP between the UE and the P-CSCF, with integrity and null encryption, so the content still reads (["The same agreement with ESP"](../../../docs/guide/en/volte.md#the-same-agreement-with-esp)). `deploy/esp/encryption.yaml` on top adds AES-CBC, read with the keys the P-CSCF writes out (["The signalling encrypted"](../../../docs/guide/en/volte.md#the-signalling-encrypted)).

| Unlike a real network, by default | Why |
| --- | --- |
| The protected client ports (`port-c`) are never used (the setup with ESP uses them) | Both sides send from their protected server port |
| For a TCP request, the UE is known by the sent-by of its Via | The INVITE, and a REGISTER over 1300 bytes, go over TCP (RFC 3261 18.1.1) |

- By default the SIP reads in Wireshark with no keys to load, and the containers need no NET_ADMIN. The associations go into the SA table of the P-CSCF with IK and CK.
- The phones answer with AKAv2-SHA-256, whose password binds RES, IK and CK (RFC 4169 2.1). SIPp, the independent peer that checks the S-CSCF, names AKAv1-MD5 in its first REGISTER and is challenged with that one alone; it computes the RES and the Digest with code written by others.

> Guide: ["The security agreement without ESP"](../../../docs/guide/en/volte.md#the-security-agreement-without-esp)

---

# Policy control over Rx, and the VoLTE call

The P-CSCF is the application function (AF) of policy and charging control; the PCRF decides what the access network carries (TS 29.214 4).

---

## The AAR of the signalling path (#786)

```ims-scene
id: signalling-path
nodes: [volte10, epc, pcscf, ue2]
lead: Each registered phone gets one Rx session. A phone that did not attach gets none.
steps:
  - id: aar
    title: The AAR right after the registration
    only: [aar-sig]
    checkpoint: true
    text: |
      There is one Media-Component-Description, and both of its numbers are 0.
      `Flow-Usage` says that this is the signalling, not the media.
  - id: aaa
    title: The PCRF finds the phone by its address
    show: [aaa-sig]
    checkpoint: true
    text: It binds the session to the IP-CAN session of the Framed-IP-Address and promises to report a lost bearer.
  - id: softphone
    title: A softphone that did not attach registers
    only: [reg-ue2]
    text: ue2 never goes through the EPC stub. The registration itself succeeds, as in the deck on registration.
  - id: notfound
    title: The PCRF finds no IP-CAN session
    show: [aar-ue2, aaa-ue2]
    active: aaa-ue2
    detail: [aar-ue2, aaa-ue2]
    checkpoint: true
    text: The same AAR goes out, but there is no PDN connection for that address.
  - id: nopolicy
    title: The P-CSCF serves that phone without policy control
    show: [pcscf-nopolicy]
    text: |
      So every registration of a softphone shows this pair in the capture.
      The captures of the decks on registration and calls have it too.
```

| AVP | Value |
| --- | --- |
| Media-Component-Number, Flow-Number | 0, 0 |
| `Flow-Usage` | AF_SIGNALLING |
| `Specific-Action` | 2 INDICATION_OF_LOSS_OF_BEARER, 4 INDICATION_OF_RELEASE_OF_BEARER |
| `Framed-IP-Address` | 172.28.0.120, to find the IP-CAN session by |

- This is the AAR right after the registration. For a UE that did not attach (ue2, pjsua), the PCRF answers 5065, IP-CAN_SESSION_NOT_AVAILABLE, and the P-CSCF serves that UE without policy control: every registration of a softphone in the captures of the decks on registration and calls shows this pair.
- Each Rx session is a Diameter session of its own: one per registered UE for the signalling path, one per call and side for the media.

The filters are `diameter.Flow-Usage == 2` and `diameter.Experimental-Result-Code == 5065`.

> Guide: ["The signalling path"](../../../docs/guide/en/volte.md#the-signalling-path). TS 29.214 4.4.5, A.4.

---

## The AAR for the media of a call

```ims-scene
id: media
view: ladder
nodes: [volte10, volte12, pcscf, epc]
lead: The path for the voice is set up because the P-CSCF, having seen the SDP answer, asks for it.
steps:
  - id: answer
    title: The 183 carries the answer
    only: [inv-p-12, r183-12-p]
    active: r183-12-p
    detail: [r183-12-p]
    checkpoint: true
    text: |
      The INVITE arrives along the path of the deck on calls. The diagram leaves out both S-CSCFs and the MMTel AS.
      The answer in the 183 settles which codec goes to which port.
  - id: aar
    title: The callee's P-CSCF asks for the resources (AAR)
    show: [aar-12]
    checkpoint: true
    text: |
      It builds from the SDP which two addresses need how much bandwidth, and hands that over.
      The m= lines of the offer and the answer go into `Codec-Data` as they are.
  - id: grant
    title: The PCRF authorises the audio
    show: [pcrf-grant]
    text: It authorises the audio only, in class QCI 1. The rates come from what the P-CSCF asked for.
  - id: aaa
    title: The AAA comes before the bearer
    show: [aaa-12]
    checkpoint: true
    text: It answers before, or in parallel with, the provisioning of the rules (TS 29.214 4.4.1).
  - id: bearer
    title: The stub sets up the dedicated bearer
    show: [bearer-12]
    checkpoint: true
    text: |
      It arrives on the event stream. The four filters of the TFT are the RTP and the RTCP between the phone and the relay.
      The default bearer stays: one more bearer, for the voice, comes up beside it.
      The stub sets up the bearer after it sends the AAA, so it may come before or after the P-CSCF sends the 183 on.
  - id: reserved
    title: The phone takes its resources as reserved
    show: [ue12-reserved]
    text: Here the callee meets the precondition of its own segment. It does not alert yet.
  - id: caller
    title: The same answer becomes an AAR at the caller too
    show: [aar-10, aaa-10]
    active: aar-10
    detail: [aar-10]
    checkpoint: true
    text: |
      The 183 passes the same P-CSCF a second time, now as the caller's side.
      One call opens two Rx sessions for media, apart from those of the signalling paths.
  - id: caller-bearer
    title: The caller gets a voice bearer as well
    show: [bearer-10, ue10-reserved]
    active: ue10-reserved
    checkpoint: true
    text: The far address of the filters is the relay, not the other phone. This bearer and the next 183 may reach the phone in either order.
  - id: forward
    title: Once the AAA is back, the 183 reaches the caller
    show: [r183-p-10]
    text: The P-CSCF holds the 183 until the AAR has its answer. It waits for the AAA only, not for the bearer. If the AAR is refused, it ends the call here.
```

The P-CSCF of each side sends an AAR once it has the SDP answer: the callee's side on the 183 from volte12, the caller's side on the 183 on its way to volte10.

| AVP | Value in the callee's AAR (#1036) |
| --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` |
| `Media-Sub-Component` | Flows 1 and 2: RTP and RTCP, with `Flow-Description` |
| `Max-Requested-Bandwidth-UL`, `-DL` | 80000 and 41000, from b=AS, in bit/s |
| `Flow-Status` | `ENABLED` (2), from sendrecv |
| `Codec-Data` | The m= lines and attributes of the offer and the answer |
| `Specific-Action` | 2, 4, 9: loss and release of the bearer, failed allocation |
| `Rx-Request-Type` | INITIAL_REQUEST; each later offer and answer, UPDATE_REQUEST |

> Guide: ["The media of a call"](../../../docs/guide/en/volte.md#the-media-of-a-call). TS 29.214 A.1. TS 29.213 6.2 Table 6.2.1, 6.3.

---

## Reading the AAR for the media of a call

- In the capture, `diameter.Codec-Data` finds the AARs of a call. Number 1 of the Media-Component-Description is the first m= line.
- One Flow-Description is `permit in 17 from 172.28.0.121 to 172.28.0.50 40004`. The other end of the media is the relay, not the other UE.
- Only the callee's P-CSCF sends AF-Application-Identifier. The caller's would take it from Feature-Caps, which no node writes; it is in the list of simplifications.
- Service-Info-Status is FINAL_SERVICE_INFORMATION, because the information is based on the answer, not on the offer only.
- On hold, the caller's side has Flow-Status `ENABLED-UPLINK` (0), the callee's `ENABLED-DOWNLINK` (1).

---

## The dedicated bearer that carries the voice

Once the PCRF authorises the audio, the stub sends it on the event stream:

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80, ... },
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... ]}}
```

- QCI 1 is the guaranteed bit rate class of conversational voice. The rates, in kbit/s, come from what the P-CSCF asked for.
- The TFT has four filters: RTP and RTCP, each way, between the UE and the relay.
- The UE answers `activate-dedicated-eps-bearer-context-accept` and takes it as the resources of its call. When the call ends, a DEACTIVATE EPS BEARER CONTEXT REQUEST comes.
- The default bearer is 5, the voice bearer 6; both belong to one PDN connection, so `linked_eps_bearer_identity` is 5. Without a relay, the far address of the filters would be the other UE's.
- The filter is `frame contains "EPS BEARER CONTEXT"`. The events arrive bit by bit within one HTTP response.

> Guide: ["Bearers"](../../../docs/guide/en/volte.md#bearers). TS 24.229 L.2.2.5.1. TS 29.213 6.3. IR.92 4.4.2, 2.4.3.1.

---

## Preconditions (no ringing until the resources are there)

```ims-scene
id: precondition
view: ladder
nodes: [volte10, pcscf, volte12]
lead: The callee starts to ring once the resources of both segments are there.
steps:
  - id: invite
    title: The INVITE carries the offer
    only: [inv-10-p, inv-p-12]
    active: inv-10-p
    detail: [inv-10-p]
    checkpoint: true
    text: |
      `curr:qos local` is `none`: the caller has no voice bearer yet.
      The mechanism is offered in Supported only, never in Require.
  - id: answer
    title: The 183 returns the answer and asks to be told
    show: [r183-12-p]
    checkpoint: true
    text: |
      The callee raises the caller's segment to mandatory and asks to be told once those resources are there.
      Its own are still `none`.
  - id: resources
    title: The 183 moves on once the AAA is back
    show: [r183-p-10]
    text: |
      In between come the two AARs of the previous scene. The P-CSCF waits for the AAA only, not for the bearer.
      The caller tells that its resources are there in the UPDATE that follows.
  - id: prack
    title: The PRACK acknowledges the 183
    show: [prack, prack-ok]
    active: prack-ok
    detail: [prack]
    text: The 183 is a reliable provisional response, so it needs an acknowledgement. The UPDATE waits for this 200.
  - id: update
    title: The UPDATE tells of its own segment
    show: [update-10-p, update-p-12]
    active: update-10-p
    detail: [update-10-p]
    checkpoint: true
    text: |
      The caller has its dedicated bearer, so its `curr:qos local` becomes `sendrecv`.
      The status is updated with an UPDATE.
  - id: both
    title: The 200 completes both segments
    show: [update-ok-12]
    checkpoint: true
    text: Local and remote, as the callee sees them, are both `sendrecv` now.
  - id: alert
    title: Only now does the ringing start
    show: [pre-met, r180-12-p]
    active: r180-12-p
    detail: [pre-met, r180-12-p]
    checkpoint: true
    text: Every mandatory row is current, so the callee sends the 180.
  - id: caller
    title: At the caller the two responses cross
    show: [update-ok-10, r180-p-10]
    active: r180-p-10
    checkpoint: true
    text: |
      The P-CSCF holds the 200 for the AAR of the new answer, so the 180 may arrive first.
      Seen from the callee, the 180 always comes after the 200 to the UPDATE.
  - id: answered
    title: The answer and the ACK confirm the call
    show: [ok-inv-12, ok-inv-10, ack]
    active: ack
    detail: [ok-inv-12]
    text: The 200 (INVITE) has no SDP. The answer came in the reliable 183.
```

```ladder
volte10, P-CSCF, PCRF, volte12
volte10 -> P-CSCF: INVITE // curr:qos local none
P-CSCF -> volte12: INVITE, through the S-CSCFs and the ASs
P-CSCF <-- volte12: 183 // conf:qos remote sendrecv
P-CSCF <-> PCRF: AAR, AAA for each side // a bearer each
volte10 <-- P-CSCF: 183, then the PRACK and its 200
volte10 -> P-CSCF -> volte12: UPDATE // curr:qos local sendrecv
volte10 <-- P-CSCF <-- volte12: 200 (UPDATE) // both segments sendrecv
volte10 <-- P-CSCF <-- volte12: 180 // only now
volte10 <-- P-CSCF <-- volte12: 200 (INVITE), then the ACK
```

- The INVITE has `Supported: 100rel, timer, 199, precondition`, not Require. The 183 has `Require: 100rel, precondition`. The UPDATE goes once the PRACK has its 200.
- At the caller the 180 may come before the 200 to the UPDATE: the P-CSCF holds that 200 for the AAR of the new answer. Filters: `sip.Require contains "precondition"`, and `sdp.media_attr contains "qos"` for every precondition line.

> Guide: ["Preconditions"](../../../docs/guide/en/volte.md#preconditions). RFC 3312. IR.92 2.4.1. TS 24.229 5.1.3.1. The full ladder and the frame numbers are in the guide.

---

## Read the precondition lines

| Step | Sender | `curr` local | `curr` remote | `des` strength (local, remote) | `conf` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte10 | none | none | mandatory, optional | |
| 183 | volte12 | none | none | mandatory, mandatory | remote |
| UPDATE | volte10 | sendrecv | none | mandatory, mandatory | |
| 200 (UPDATE) | volte12 | sendrecv | sendrecv | mandatory, mandatory | |

- Each value is as the sender of the SDP sees it. The caller uses the segmented status type (TS 24.229 6.1.2).
- In the 183 the callee raises the caller's segment to mandatory and asks with `conf:qos remote sendrecv` to be told (RFC 3312 7). Its local and remote are as volte12 sees them: its remote is volte10's segment.
- A UE takes its resources as reserved once it has the dedicated bearer (TS 24.229 L.2.2.5.1B, IR.92 2.4.3.1); the UPDATE says so. A UE without the EPC stub, such as ue2, has them from the start. The direction is sendrecv in every row.

> Guide: the table in ["Preconditions"](../../../docs/guide/en/volte.md#preconditions). RFC 3312 6.

---

## When the callee may ring, in the code

[internal/precondition/table.go](../../../internal/precondition/table.go)

```go
func (t *Table) Met() bool {
	for _, r := range t.rows {
		if r.Strength == Mandatory && !r.Current {
			return false
		}
	}
	return true
}
```

- A row of the table is a segment (local or remote) and a direction (send or recv).
- The callee's UE does not alert until this returns true. The comment above the function points to RFC 3312 6.
- `NeedsOffer`, in the same file, decides whether a change of the UE's own resources calls for a new offer: that is how the caller's UPDATE comes about. `internal/ue/session.go` uses the table.

> Guide: ["Where the code is"](../../../docs/guide/en/volte.md#where-the-code-is). RFC 3312 5, 6.

---

## The offer and the answer of the codecs

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
```

- 96 and 98 are bandwidth-efficient, 97 and 99 octet-aligned; the caller prefers the former (IR.92 3.2.5). No mode-set (IR.92 2.4.3.2).
- PCMU stays last, so that a VoLTE UE can talk to a softphone that knows PCMU only. b=AS:80 is the value for PCMU, and the uplink bandwidth takes it too.
- volte12 answers `m=audio 60734 RTP/AVP 96 100` with `b=AS:41`. ue2, which knows PCMU only, answers `RTP/AVP 0 101`, and the VoLTE UE then speaks PCMU. ue2 did not attach, so only volte10's side has an AAR for the call.
- b=RS:0 and b=RR:4000 are the RTCP bandwidth of senders and of receivers in bit/s (RFC 3556 2); the UE computes its report interval from them (RFC 3550 6.3.1), never below 5 seconds.

> Guide: ["The offer and the answer"](../../../docs/guide/en/volte.md#the-offer-and-the-answer). The whole offer is in the guide: AMR as 98 and 99, PCMU as 0, telephone-event as 100 and 101 at the clock rate of each speech codec (TS 26.114 Annex G), ptime 20, maxptime 240. RFC 4867.

---

## AMR frames, RTCP and DTMF

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

- One 20 ms frame per packet: the CMR, the table of contents (F, FT, Q), the speech bits (RFC 4867 4.3).
- The UE never encodes or decodes AMR. It sends frames in the storage format of RFC 4867 5, and what was sent compares bit for bit with what was received. Wireshark decodes the AMR-WB frames by the SDP it saw.
- Each DTMF digit is a run of packets, the last three with the end bit.
- RTCP is on the port above RTP (RFC 3550 6.4.1, IR.92 3.2.4). DTMF goes as telephone-events (RFC 4733). The filters are `amr`, `rtcp` and `rtpevent`.

> Guide: ["The frames"](../../../docs/guide/en/volte.md#the-frames) and ["RTCP and DTMF"](../../../docs/guide/en/volte.md#rtcp-and-dtmf)

---

## Session timer, Reason, early media, hold

| Item | What to look at in the capture |
| --- | --- |
| Session timer | `Session-Expires: 1800` in the INVITE, `refresher=uac` in the 200; the caller sends an UPDATE every 900 seconds. |
| The UE's Reason | `RELEASE_CAUSE;cause=1;text="User ends call"` in the BYE. |
| Early media | `P-Early-Media: supported` in the INVITE. No node authorises it. |
| Hold and resume | A re-INVITE with `a=sendonly`, its 200 with `a=recvonly`. Both Rx sessions are updated. |

The filters are `sip.Session-Expires` and `sip.Reason`.

- A call ended because its media bearer was lost has cause 3 in the UE's Reason; the P-CSCF's BYEs say `Reason: SIP;cause=503`. With no early media authorised, the caller plays a ringing tone of its own after the 180.
- The hold re-INVITE passes the MMTel AS as it came. The call with hold and DTMF is the one that the e2e script drives through the Lab.

> Guide: ["Session timer, Reason and early media"](../../../docs/guide/en/volte.md#session-timer-reason-and-early-media) and ["Hold, resume and DTMF"](../../../docs/guide/en/volte.md#hold-resume-and-dtmf). IR.92 2.2.8, 2.2.4, 2.2.7. RFC 4028 9. TS 24.229 7.2A.18.11. RFC 5009. TS 24.610 4.5.2.1.

---

## The network takes the bearer

```ims-scene
id: release
view: ladder
nodes: [volte10, volte12, pcscf, scscf, epc]
lead: "No phone hangs up: the network that took the resources away ends the call."
steps:
  - id: deactivate
    title: The network takes the voice bearer down
    only: [epc-deact]
    checkpoint: true
    text: |
      It is the deactivation by the network, the same as when the radio is lost.
      It starts from the Lab's button, or from a DELETE by `curl` on the stub's resource of the bearer. The diagram draws the network only.
  - id: tell
    title: It tells the phone of the deactivation
    show: [deact-10]
    text: It arrives on the event stream. volte10 sends no BYE of its own.
  - id: asr
    title: The PCRF aborts the Rx session (ASR)
    show: [asr, asa]
    active: asr
    detail: [asr]
    checkpoint: true
    text: |
      Rather than report the lost flow with an RAR, it ends the whole session.
      The P-CSCF answers with an ASA.
  - id: str
    title: The P-CSCF ends its session too, with an STR
    show: [str]
    text: A call that ends with an ordinary BYE has an STR from each side's P-CSCF as well, with another Termination-Cause.
  - id: build
    title: It builds the BYEs from the dialog it kept
    show: [pcscf-bye]
    checkpoint: true
    text: The P-CSCF puts together the BYE that a phone was to have sent.
  - id: remote
    title: To the other party, in the served user's name
    show: [bye-core]
    checkpoint: true
    text: The Reason gives the cause. `cause=503` says that the service became unavailable.
  - id: ue
    title: A BYE for the served phone too
    show: [bye-10]
    checkpoint: true
    text: |
      This BYE is this project's addition.
      A phone that lost only its voice bearer still has its signalling, and without the BYE it would stay in the call.
  - id: far
    title: The BYE reaches the callee
    show: [bye-12]
    text: To the other side it goes through the S-CSCF and the MMTel AS. The Reason is the same.
  - id: release12
    title: The callee's bearer is released too
    show: [deact-12]
    text: Once volte12's call is over, the Rx session of that side ends and the stub releases its bearer.
```

```ladder
volte10, P-CSCF, S-CSCF, volte12, EPC and PCRF, Lab
EPC and PCRF <- Lab: DELETE .../bearers/6
volte10 <- EPC and PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST, on the event stream
P-CSCF <- EPC and PCRF: ASR, Abort-Cause BEARER_RELEASED (0) // then the ASA
P-CSCF -> EPC and PCRF: STR // then the STA
P-CSCF -> S-CSCF: BYE in volte10's name // cause=503
volte10 <- P-CSCF: BYE // cause=503
P-CSCF -> volte12: BYE, after the S-CSCF and the MMTel AS
volte12 <- EPC and PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST
```

> Guide: ["The network takes the bearer"](../../../docs/guide/en/volte.md#the-network-takes-the-bearer). TS 29.214 4.4.6.1, 4.4.6.2. TS 24.229 5.2.8.1.2. IR.92 2.4.2.3.

---

## How it looks when the bearer is lost

- The voice bearer carried all the media, so the PCRF ends the Rx session with an ASR, not an RAR. volte10 sends no BYE of its own.
- The P-CSCF builds the BYEs from the dialog it keeps behind its `Store`. The BYE to the other side goes in the served user's name.
- The BYE to the served UE is this project's addition: a UE that lost only its voice bearer still has its signalling, and without a BYE it would stay in a call that the network has ended (the comment in `internal/pcscf/release.go`).
- A call that ends with an ordinary BYE has each side's P-CSCF end its Rx session with an STR, Termination-Cause DIAMETER_LOGOUT (TS 29.214 4.4.4).
- The filter is `diameter.Abort-Cause`. All of Rx is `diameter.cmd.code in {265, 258, 274, 275}`: AAR and AAA, RAR and RAA, ASR and ASA, STR and STA; RAR and RAA do not occur in this run.

---

## What IR.92 asks for

| IR.92 | What | This project |
| --- | --- | --- |
| 4.4.1, 4.4.2, 4.5 | Signalling on QCI 5, voice on QCI 1, the P-CSCF in the PCO | The EPC stub |
| 2.4.1 | Preconditions, with UPDATE for the status | Done |
| 2.4.2.1, 2.4.2.3 | The network ends the session when a bearer is lost | The P-CSCF's BYEs |
| 3.2.1, 3.2.5, 2.4.3.2 | AMR and AMR-WB, both payload formats | Frames encoded beforehand |
| 3.2.4, 2.2.4, 2.2.8 | RTCP, Reason, the session timer | Done |

- Not built: the mmtel ICSI, +sip.instance and the audio tag in the Contact of the REGISTER; following the CMR; IPsec ESP in the default setup; IPv6. The next slide has the reasons.

---

## What is not built

- The Contact of the REGISTER has no ICSI, no +sip.instance and no audio tag. The Contact of the INVITE and of its responses has the ICSI and audio.
- Following the CMR needs a UE that can encode; this UE sends frames encoded beforehand.
- The default setup puts no ESP on the signalling, so that the captures stay readable. The setup in `deploy/esp` puts ESP with null encryption on it. With `deploy/esp/encryption.yaml` on top, it encrypts with AES-CBC too.
- Every node uses IPv4, on one Docker network.
- The national standards, such as TTC in Japan, cover the interconnection of networks, so they do not shape the call inside one network. Differences such as the length of the session timer belong to the profile of each peer network: [the interconnect guide](../../../docs/guide/en/interconnect.md) covers them.

> Guide: ["How a VoLTE phone differs from a softphone"](../../../docs/guide/en/volte.md#how-a-volte-phone-differs-from-a-softphone) and ["What this project simplifies"](../../../docs/guide/en/volte.md#what-this-project-simplifies)

---

## What this project simplifies

| Where | The main items |
| --- | --- |
| EPC stub | No NAS encoding, S1-AP, radio, GTP-U, Gx, or authentication of the attach. A bearer is a record. |
| PCRF | Audio only, with QCI 1. No DRA. |
| P-CSCF, UE, S-CSCF | No ESP by default (the setup in `deploy/esp` has it). |
| P-CSCF | Releases at once when a bearer is lost, as each call has one audio stream. A dialog that ends without a BYE stays in the `Store`: the P-CSCF keeps no index of dialogs by UE, so that several P-CSCF nodes can serve the same users. |
| UE and AMR | CMR and mode-set are not followed. A UE that loses its PDN connection stops. |

- Each item has a comment in the code that starts with `SIMPLIFIED:`.

> Guide: ["What this project simplifies"](../../../docs/guide/en/volte.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## If you want to run it yourself

You need not run anything to read this deck. To see the same signals in a capture of your own, the guide has the steps. The frame numbers of the guide and of this deck are from one run of the e2e script of the VoLTE call.

- ["Run it"](../../../docs/guide/en/volte.md#run-it). The script registers SIPp, makes the VoLTE call and the call to ue2, drives DTMF and hold through the Lab, has the network release the bearer, and checks each message in the capture.
- ["Open the capture"](../../../docs/guide/en/volte.md#open-the-capture). The display filter that keeps all of this deck, and the settings with which Wireshark reads the AMR-WB frames.
- ["With the Lab"](../../../docs/guide/en/volte.md#with-the-lab) and ["From the command line"](../../../docs/guide/en/volte.md#from-the-command-line). How to start volte10 and volte12, call, and release the voice bearer with `curl`, and the table of the flags of a VoLTE UE.
- ["Clean up"](../../../docs/guide/en/volte.md#clean-up). The list of the guides is in [docs/guide/](../../../docs/guide/).

---

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | sec-agree |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | MAR, MAA of IMS AKA |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx: AAR and AAA, RAR and RAA, ASR and ASA, STR and STA |
| `diameter.Flow-Usage == 2`, `diameter.Codec-Data` | AARs: signalling, media |
| `http && ip.addr == 172.28.0.80` | The EPC stub's API |
| `sip.Require contains "precondition"` | Precondition 183s |
| `amr`, `rtcp`, `rtpevent` | AMR, RTCP, DTMF |

- The guide checked these filters with tshark 4.2 against a capture of the e2e script.

> Guide: ["Wireshark display filters"](../../../docs/guide/en/volte.md#wireshark-display-filters). The whole table is in the guide.

---

## Where the code is

```ims-scene
id: code
nodes: [volte10, epc, volte12, pcscf, scscf, hss]
lead: Each decision of this deck is in a function of its own, node by node.
steps:
  - id: epc
    title: The access network stub and the PCRF
    focus: [epc]
    text: "`http.go`, `api.go`, `network.go` and `pcrf.go` in `internal/epc`. `aaRequest` answers the AAA before it sets up the bearer"
  - id: access
    title: The access network as a phone sees it
    focus: [volte10, volte12]
    text: "`internal/ue/access.go`, `access_epc.go`. The attach is `EPCAccess.Attach`"
  - id: vector
    title: MILENAGE and the authentication vector
    focus: [hss]
    checkpoint: true
    text: "`internal/milenage`, `internal/aka`. The vector is `NewAuthenticationVector`, the USIM's check `USIM.Authenticate`"
  - id: aka
    title: The challenge of IMS AKA, and sec-agree
    focus: [scscf, pcscf, volte10]
    text: "`internal/scscf/aka.go`, `internal/pcscf/secagree.go`, `internal/ue/aka.go`. The P-CSCF's `challenged` takes the keys out"
  - id: policy
    title: Policy control and Rx
    focus: [pcscf, epc]
    checkpoint: true
    text: "`internal/policy`, `internal/rx`, `internal/pcscf/policy.go`. The AAR of a call is `setup.authorize`"
  - id: release
    title: The release by the network
    focus: [pcscf]
    text: "`internal/pcscf/release.go`, `dialog.go`. The two BYEs are `sendBYEs`"
  - id: media
    title: Preconditions, AMR and RTCP
    focus: [volte10, volte12]
    text: "`internal/precondition`, `internal/amr`, `internal/rtcp`. Whether the callee may ring is `Table.Met`"
```

| What | Where | The function that decides |
| --- | --- | --- |
| The EPC stub and the PCRF | `http.go`, `api.go`, `network.go`, `pcrf.go` in `internal/epc` | `EPC.aaRequest` |
| The UE's access network | `internal/ue/access.go`, `access_epc.go` | `EPCAccess.Attach` |
| MILENAGE and the vector | `internal/milenage`, `internal/aka` | `NewAuthenticationVector`, `USIM.Authenticate` |
| The IMS AKA challenge, sec-agree | `internal/scscf/aka.go`, `internal/pcscf/secagree.go`, `internal/ue/aka.go` | `SCSCF.challengeAKA`, `PCSCF.challenged` |
| Policy control and Rx | `internal/policy`, `internal/rx`, `internal/pcscf/policy.go` | `setup.authorize` |
| The release by the network | `internal/pcscf/release.go`, `dialog.go` | `PCSCF.PolicyEvent`, `PCSCF.sendBYEs` |
| Preconditions, AMR, RTCP | `internal/precondition`, `internal/amr`, `internal/rtcp` | `Table.Met` |

- `internal/ue/volte_epc_test.go` and `volte_aka_test.go` run the VoLTE call through the whole IMS in one process.
- Each of the three methods of `PolicyControl` in `internal/policy` stands for an Rx message: `SubscribeSignallingPath` for the AAR of the signalling path, `AuthorizeMedia` for the AAR of a call's media, and `Release` for the STR.

> Guide: ["Where the code is"](../../../docs/guide/en/volte.md#where-the-code-is)

