---
title: Two IMS networks joined at the border
footer: ims-practice study session 10. Two networks
lang: en
format: visual
---

```ims-model
nodes:
  # Network A (ims0, 172.28.0.0/24)
  - {id: mmtel-a, label: MMTel AS, role: the conference focus, at: [0, 0]}
  - {id: dns-a, label: DNS-A, role: answers the ENUM, at: [0.9, 0]}
  - {id: ue-a1, label: UE A1, role: the UE that calls, at: [0, 1]}
  - {id: scscf-a, label: S-CSCF-A, role: the originating side, at: [0.9, 1]}
  - {id: ibcf-a, label: IBCF-A, role: the border of network A, at: [1.8, 1]}
  - {id: ipsmgw-a, label: IP-SM-GW-A, role: the SC is here too, at: [0, 2]}
  - {id: agw-a, label: IMS-AGW, role: the relay of network A, at: [0.9, 2]}
  - {id: trgw-a, label: TrGW-A, role: the media of network A, at: [1.8, 2]}
  # A host on the link (nni0, 172.28.2.0/24) that belongs to neither network
  - {id: host99, label: 172.28.2.99, role: in no agreement, at: [2.25, 0]}
  # Network B (ims1, 172.28.1.0/24)
  - {id: icscf-b, label: I-CSCF-B, role: the entry of network B, at: [3.6, 0]}
  - {id: hss-b, label: HSS-B, role: the subscriber data, at: [4.5, 0]}
  - {id: ibcf-b, label: IBCF-B, role: the border of network B, at: [2.7, 1]}
  - {id: scscf-b, label: S-CSCF-B, role: the terminating side, at: [3.6, 1]}
  - {id: ue-b1, label: UE B1, role: the UE that is called, at: [4.5, 1]}
  - {id: trgw-b, label: TrGW-B, role: the media of network B, at: [2.7, 2]}
  - {id: ipsmgw-b, label: IP-SM-GW-B, role: the SMS AS of network B, at: [4.5, 2]}
groups:
  - {id: net-a, label: network A (ims0), nodes: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]}
  - {id: net-b, label: network B (ims1), nodes: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]}
events:
  # The four reference points of the border
  - id: mx
    rel: Mx
    from: scscf-a
    to: ibcf-a
    proto: SIP
  - id: ici
    rel: Ici
    from: ibcf-a
    to: ibcf-b
    proto: SIP
  - id: izi
    rel: Izi
    from: trgw-a
    to: trgw-b
    proto: RTP
  - id: ix
    rel: Ix
    from: ibcf-a
    to: trgw-a
    proto: HTTP
  # The call, up to where it leaves network A (steps 1 to 4)
  - id: inv-ue-s
    msg: INVITE
    from: ue-a1
    to: scscf-a
    proto: SIP
    note: (via the P-CSCF)
    fields:
      - Request-URI: "`tel:+12025550151`"
      - the AS on the way: the MMTel AS of the originating filter criteria
  - id: enum-q
    msg: NAPTR
    from: scscf-a
    to: dns-a
    proto: DNS
    note: (query)
    fields:
      - name: "`1.5.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: enum-a
    msg: NAPTR
    from: dns-a
    to: scscf-a
    proto: DNS
    note: (answer)
    fields:
      - service: "`E2U+sip`"
      - what the rewrite gives: a SIP URI of network B's domain with `user=phone`
  - id: scscf-exit
    do: send it through an IBCF of its own network
    at: scscf-a
    fields:
      - the domain of the destination: not one of network A
      - on top of Route: "`sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr`"
      - Record-Route: its own URI, as always
  - id: inv-s-ia
    msg: INVITE
    from: scscf-a
    to: ibcf-a
    proto: SIP
    fields:
      - Request-URI: the number in a SIP URI with `user=phone`
      - Call-ID: the one of the dialog in network A
      - Via: one entry for each node of network A
  - id: ibcfa-exit
    do: build the request again as the exit point
    at: ibcf-a
    fields:
      - Route: its own URI comes off the top
      - the peer network: the host of the Request-URI picks `network-b.yaml`
      - the header it removes: P-Charging-Function-Addresses
      - numbers and codecs: in the form of the agreement, without what the peer does not take
      - Max-Forwards: one hop off
  - id: trgw-add
    msg: Add
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - the terminations: "`core` faces its own network, `access` the peer"
      - the context: one for the call
  - id: trgw-mod-a
    msg: Modify
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - where core sends: 172.28.0.50, the media relay of network A
      - the address in the SDP on the link: 172.28.2.11
  - id: inv-nni
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (on the link)
    fields:
      - Via: the external name of IBCF-A alone
      - Call-ID: made by IBCF-A, and CSeq is 1
      - Contact: its own external name `ibcf.nni.mnc001.example.net`
  # The call, once it is in network B (steps 5 to 7)
  - id: ibcfb-entry
    do: build the request again as the entry point
    at: ibcf-b
    fields:
      - the source 172.28.2.10: it is in the `sources` of network A's agreement
      - Route: its own URI comes off the top
      - the next hop: the I-CSCF of the configuration
      - the media: through TrGW-B, so the SDP of network B points at 172.28.1.91
  - id: inv-ib-i
    msg: INVITE
    from: ibcf-b
    to: icscf-b
    proto: SIP
    fields:
      - Via: the internal name of IBCF-B alone
      - Call-ID: made by IBCF-B
  - id: icscfb-tel
    do: put a tel URI in the Request-URI
    at: icscf-b
    fields:
      - the Request-URI that arrived: the number in a SIP URI with `user=phone`
      - after the change: "`tel:+12025550151`"
      - why: the HSS knows UE B1 by its tel URI
  - id: lir
    msg: LIR
    from: icscf-b
    to: hss-b
    proto: Cx
    fields:
      - Public-Identity: "`tel:+12025550151`"
  - id: lia
    msg: LIA
    from: hss-b
    to: icscf-b
    proto: Cx
    fields:
      - Server-Name: the S-CSCF that UE B1 is registered with
  - id: inv-i-sb
    msg: INVITE
    from: icscf-b
    to: scscf-b
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550151`"
  - id: inv-sb-ue
    msg: INVITE
    from: scscf-b
    to: ue-b1
    proto: SIP
    note: (via the P-CSCF)
    fields:
      - the way: along the Path of UE B1, to the P-CSCF of network B
      - SDP: "`c=` is 172.28.1.91, TrGW-B"
  # The call, as the answer comes back (step 7)
  - id: r183-ue-sb
    msg: "183"
    from: ue-b1
    to: scscf-b
    proto: SIP
    note: (answer)
    fields:
      - SDP: the address and the port of UE B1
  - id: r183-sb-i
    msg: "183"
    from: scscf-b
    to: icscf-b
    proto: SIP
    note: (along the Via)
  - id: r183-i-ib
    msg: "183"
    from: icscf-b
    to: ibcf-b
    proto: SIP
  - id: r183-ib-ia
    msg: "183"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (on the link)
    fields:
      - SDP: "`c=` is 172.28.2.21, the link side of TrGW-B"
  - id: r183-ia-s
    msg: "183"
    from: ibcf-a
    to: scscf-a
    proto: SIP
    fields:
      - SDP: "`c=` is 172.28.0.91, the network A side of TrGW-A"
  - id: r183-s-ue
    msg: "183"
    from: scscf-a
    to: ue-a1
    proto: SIP
    note: (via the P-CSCF)
    fields:
      - SDP: "`c=` is 172.28.0.50, the media relay of network A"
  # The media (step 9)
  - id: trgwb-mod
    msg: Modify
    from: ibcf-b
    to: trgw-b
    proto: HTTP
    note: (Ix)
    fields:
      - where core sends: 172.28.1.100, UE B1
      - the address in the SDP on the link: 172.28.2.21
  - id: trgwa-mod-back
    msg: Modify
    from: ibcf-a
    to: trgw-a
    proto: HTTP
    note: (Ix)
    fields:
      - where access sends: 172.28.2.21, TrGW-B
      - the address in the SDP for network A: 172.28.0.91
  - id: rtp-ue-agw
    msg: RTP
    from: ue-a1
    to: agw-a
    proto: RTP
    note: (the speech)
  - id: rtp-agw-trgw
    msg: RTP
    from: agw-a
    to: trgw-a
    proto: RTP
  - id: rtp-nni
    msg: RTP
    from: trgw-a
    to: trgw-b
    proto: RTP
    note: (Izi)
  - id: rtp-trgw-ue
    msg: RTP
    from: trgw-b
    to: ue-b1
    proto: RTP
  # The two sides of an IBCF
  - id: ibcfa-legs
    do: end it, and send a new request
    at: ibcf-a
    fields:
      - the internal UA: listens on an address of its own network
      - the external UA: listens on an address of the link
      - what each side has of its own: Call-ID, tags, CSeq, Via, Contact
  - id: ibcfb-legs
    do: end it, and send a new request
    at: ibcf-b
    fields:
      - the side that ends it: the dialog on the link
      - the side that sends: the dialog in network B
  # When the two agreements do not match
  - id: ibcfa-no-message
    do: reject a method outside the agreement
    at: ibcf-a
    fields:
      - the agreement: "the `methods` of `network-b-no-message.yaml` has no MESSAGE"
      - the response it sends: 405 (Method Not Allowed)
      - the SC of network A: TP-ST 98, no response from SME
  - id: r405
    msg: "405"
    from: ibcf-a
    to: ipsmgw-a
    proto: SIP
    note: (Method Not Allowed)
  - id: ibcfb-codec
    do: remove the codecs the peer does not take
    at: ibcf-b
    fields:
      - the codecs of network B's agreement: AMR-WB alone
      - the offer of UE A1: PCMU
      - the speech codecs left: none
  - id: r488
    msg: "488"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (Not Acceptable Here)
  - id: inv-nni-local
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (a local number)
    fields:
      - Request-URI: "a local number with `phone-context=+1`"
      - the agreement of network A: "`network-b-local-numbers.yaml` passes it as it is"
  - id: ibcfb-number
    do: reject a number that is not global
    at: ibcf-b
    fields:
      - the agreement of network B: global numbers only
      - the response it sends: 404 (Not Found), by local policy
  - id: r404
    msg: "404"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (Not Found)
  # A request from no peer network
  - id: inv-99
    msg: INVITE
    from: host99
    to: ibcf-a
    proto: SIP
    note: (from outside every agreement)
  - id: msg-99
    msg: MESSAGE
    from: host99
    to: ibcf-a
    proto: SIP
    note: (from outside every agreement)
  - id: ibcfa-screen
    do: ask whether the request may come in
    at: ibcf-a
    fields:
      - what asks: the SIP stack calls `IBCF.Screen`
      - the address it came from: in the `sources` of no peer network
      - what the stack does: drops it, with no transaction and no response
  # Short messages across
  - id: sms-ue-s
    msg: MESSAGE
    from: ue-a1
    to: scscf-a
    proto: SIP
    note: (via the P-CSCF)
    fields:
      - Request-URI: "`tel:+12025550100`, the PSI of the SC"
      - the body: an SMS-SUBMIT whose TP-DA is `+12025550151`
  - id: sms-s-ip
    msg: MESSAGE
    from: scscf-a
    to: ipsmgw-a
    proto: SIP
    note: (originating iFC)
  - id: sms-enum-q
    msg: NAPTR
    from: ipsmgw-a
    to: dns-a
    proto: DNS
    note: (query)
    fields:
      - name: "`1.5.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: sms-enum-a
    msg: NAPTR
    from: dns-a
    to: ipsmgw-a
    proto: DNS
    note: (answer)
    fields:
      - what the rewrite gives: a SIP URI of network B's domain with `user=phone`
  - id: sms-ip-ia
    msg: MESSAGE
    from: ipsmgw-a
    to: ibcf-a
    proto: SIP
    note: (SMS-DELIVER)
    fields:
      - Request-URI: the number in a SIP URI of network B's domain
      - From and P-Asserted-Identity: "`tel:+12025550100`, the SC by its number"
      - Route: the IBCF of its own network
  - id: sms-nni
    msg: MESSAGE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (on the link)
    fields:
      - the agreements: both have MESSAGE in `methods`
      - the sender: the SC by its number, so no host name of network A crosses
  - id: sms-ib-i
    msg: MESSAGE
    from: ibcf-b
    to: icscf-b
    proto: SIP
  - id: sms-i-sb
    msg: MESSAGE
    from: icscf-b
    to: scscf-b
    proto: SIP
    fields:
      - Request-URI: "`tel:+12025550151`"
  - id: sms-sb-ip
    msg: MESSAGE
    from: scscf-b
    to: ipsmgw-b
    proto: SIP
    note: (terminating iFC)
    fields:
      - the criterion it matches: a MESSAGE whose P-Asserted-Identity is not its own IP-SM-GW
  - id: sms-202-nni
    msg: "202"
    from: ibcf-b
    to: ibcf-a
    proto: SIP
    note: (on the link)
  - id: sms-202-ip
    msg: "202"
    from: ibcf-a
    to: ipsmgw-a
    proto: SIP
  - id: sms-report
    msg: MESSAGE
    from: ipsmgw-a
    to: scscf-a
    proto: SIP
    note: (status report)
    fields:
      - TP-ST: 1, forwarded but the delivery cannot be confirmed
  - id: sms-report-ue
    msg: MESSAGE
    from: scscf-a
    to: ue-a1
    proto: SIP
    note: (via the P-CSCF)
  - id: sms-deliver-i
    msg: MESSAGE
    from: ipsmgw-b
    to: icscf-b
    proto: SIP
    note: (SMS-DELIVER)
  - id: sms-deliver-sb
    msg: MESSAGE
    from: icscf-b
    to: scscf-b
    proto: SIP
  - id: sms-deliver-ue
    msg: MESSAGE
    from: scscf-b
    to: ue-b1
    proto: SIP
    note: (via the P-CSCF)
    fields:
      - RP-OA: the SC of network A, `12025550100`
      - the log of UE B1: the sender is `+12025550101`
  # A conference across the border
  - id: conf-inv-m-s
    msg: INVITE
    from: mmtel-a
    to: scscf-a
    proto: SIP
    note: (the focus)
    fields:
      - Request-URI: "`tel:+12025550151`"
      - Route: the S-CSCF of its own network, with `orig`
      - Contact: the conference URI with `isfocus`
  - id: conf-inv-s-ia
    msg: INVITE
    from: scscf-a
    to: ibcf-a
    proto: SIP
    fields:
      - P-Asserted-Identity: the conference URI, a PSI that the S-CSCF serves
  - id: conf-inv-nni
    msg: INVITE
    from: ibcf-a
    to: ibcf-b
    proto: SIP
    note: (on the link)
    fields:
      - Contact: as it came, with `isfocus` on it
      - Record-Route: IBCF-A puts its own external name in
      - the Record-Route on the link: this one only
  - id: conf-inv-ib-i
    msg: INVITE
    from: ibcf-b
    to: icscf-b
    proto: SIP
    fields:
      - Record-Route: IBCF-B puts its own internal name in
  - id: conf-sub
    msg: SUBSCRIBE
    from: scscf-b
    to: ibcf-b
    proto: SIP
    note: (outside the dialog)
    fields:
      - Request-URI: the conference URI
  - id: ibcfb-sub
    do: reject a subscription outside the dialog
    at: ibcf-b
    fields:
      - the response it sends: 403 (Forbidden), by local policy
      - the participant: subscribes again within the dialog of the conference session
  - id: conf-403
    msg: "403"
    from: ibcf-b
    to: scscf-b
    proto: SIP
    note: (Forbidden)
```

---

# Two IMS networks joined at the border

ims-practice study session 10 (the IBCF and the TrGW, the agreement between two operators, a call and short messages across)

---

[layout: summary]

## What you will know after this session

To begin: a call leaves for another operator's network. What does that operator get to see of this one?

- What stands on the border: the IBCF for the SIP, the TrGW for the media.
- Which reference points join two networks, and which join an IBCF to its own network.
- Why the border is a B2BUA, and what that hides.
- What the agreement between two operators decides, and what fails when the two do not match.
- Where to look for the call, the short messages and the host that no agreement covers, in three captures.

> Guide: [Connect two IMS networks](../../../docs/guide/en/interconnect.md) (`docs/guide/en/interconnect.md`)
> The editions are named at the top of the guide, among them TS 24.229 V20.1.0, TS 29.165 V20.0.0, TS 23.204 V19.1.0, TS 24.341 V19.0.0, TS 23.040 V20.0.0, TS 32.409 V19.0.0, TS 24.147 V19.0.0, RFC 3261, RFC 3312 and RFC 3966.

---

## The two networks

```ims-scene
id: networks
nodes: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a, icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
lead: Only the nodes of the border join the two networks. A node inside one of them cannot reach a node inside the other at all.
steps:
  - id: net-a
    title: Network A is the one every earlier session used
    focus: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]
    zoom: [mmtel-a, dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, agw-a, trgw-a]
    checkpoint: true
    text: |
      Its domain is `ims.mnc001.mcc001.3gppnetwork.org` and the number of UE A1 is `+1-202-555-0101`.
      Only the nodes of this session are drawn. The P-CSCF and the HSS are there as always.
  - id: net-b
    title: Network B is the network of another operator
    focus: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
    zoom: [icscf-b, hss-b, ibcf-b, scscf-b, ue-b1, trgw-b, ipsmgw-b]
    checkpoint: true
    text: |
      The same image runs with another domain, other numbers and other subscribers.
      It is as small as the scenarios allow: no MMTel AS, no EPC with its PCRF, no media relay at its P-CSCF and no Lab.
  - id: border
    title: Only the nodes of the border are on the link
    focus: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    zoom: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    checkpoint: true
    text: |
      The bridge `nni0` holds the IBCFs and the TrGWs of both networks.
      The IBCFs carry the SIP, the TrGWs the media.
  - id: both
    title: Every signal goes through the border
    focus: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      A node of network A cannot reach an address of network B. The calls and the short messages all cross at the border.
      Each bridge has a capture of its own.
```

```text
     network A (ims0)          the link (nni0)         network B (ims1)
     172.28.0.0/24              172.28.2.0/24           172.28.1.0/24

  S-CSCF --- IBCF-A (.90) --- .10 -- Ici -- .20 --- IBCF-B (.90) --- I-CSCF
     agw --- TrGW-A (.91) --- .11 -- Izi -- .21 --- TrGW-B (.91) --- UE B1
```

- Network A is the network of `compose.yaml`, the one every earlier session used. Network B is a second IMS of another operator: the same image with another domain, other numbers and other subscribers.
- Only the IBCFs and the TrGWs are on the link. A node inside network A cannot reach a node inside network B at all.
- Network B is as small as the scenarios allow: no MMTel AS, no EPC with its PCRF, no media relay at its P-CSCF and no Lab.
- Each bridge has a capture of its own: `captures/` for network A, `captures/b/` for network B and `captures/nni/` for the link.

> Guide: ["The two networks"](../../../docs/guide/en/interconnect.md#the-two-networks)

---

## The nodes of both networks

| | Network A | Network B |
| --- | --- | --- |
| Domain | `ims.mnc001.mcc001.3gppnetwork.org` | `ims.mnc002.mcc001.3gppnetwork.org` |
| The UE that calls | UE A1, 172.28.0.100, `+1-202-555-0101` | UE B1, 172.28.1.100, `+1-202-555-0151` |
| IBCF | 172.28.0.90 inside, 172.28.2.10 on the link | 172.28.1.90 inside, 172.28.2.20 on the link |
| TrGW | 172.28.0.91 inside, 172.28.2.11 on the link | 172.28.1.91 inside, 172.28.2.21 on the link |
| SC of the short messages | `+12025550100` | `+12025550150` |

- `deploy/interconnect/compose.yaml` adds network B and the link on top of `compose.yaml`, which stays as it is. Every service of network B ends in `-b`, so `scscf-b` is its S-CSCF and `scscf` network A's.
- The node files of the two networks are in `deploy/interconnect/a` and `deploy/interconnect/b`. The numbers of both networks are in the range that the North American Numbering Plan keeps for fiction, and the keys are test values.

> Guide: ["The two networks"](../../../docs/guide/en/interconnect.md#the-two-networks)

---

## The IBCF is a B2BUA

```ims-scene
id: legs
nodes: [scscf-a, ibcf-a, ibcf-b]
lead: An IBCF has a user agent on each side. A request that arrives ends there, and what goes out on the other side is a new request of the IBCF's own.
steps:
  - id: sides
    title: It listens on two sides
    focus: [ibcf-a]
    zoom: [scscf-a, ibcf-a, ibcf-b]
    text: |
      The internal UA listens on 172.28.0.90, an address of its own network, the external one on 172.28.2.10, an address of the link.
      Both take the standard port 5060.
  - id: exit
    title: A request on the internal side leaves through the exit point
    show: [inv-s-ia]
    checkpoint: true
    text: |
      The request leaves the network, so IBCF-A is its exit point (TS 24.229 5.10.2).
      The S-CSCF has finished the originating side and sends it to an IBCF of its own network.
  - id: b2bua
    title: It ends the request on one side and sends it again on the other
    show: [ibcfa-legs]
    focus: [ibcf-a]
    text: |
      IBCF-A ends the request it received in its dialog with network A.
      What goes to network B is a request of its own, in its dialog with network B.
  - id: entry
    title: A request on the external side comes in through the entry point
    show: [inv-nni]
    focus: [ibcf-b]
    checkpoint: true
    text: |
      The request on the link arrives on the external side of IBCF-B. It enters that network, so IBCF-B is its entry point (5.10.3).
      Not one Via, Route, Record-Route or Contact of a node of network A is in it.
  - id: hidden
    title: What the request on the link carries
    only: [inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      Nothing that routes a message inside one network belongs to the dialog with the other network.
      That is how the B2BUA hides the topology of its network.
```

- TS 29.165 calls the interface between two IMS networks the II-NNI. The node that stands there is the IBCF, with a user agent on each side: the internal one listens in its own network, the external one on the link.
- A request that reaches the internal side leaves the network, and the IBCF is its **exit point** (TS 24.229 5.10.2). One that reaches the external side enters, and the IBCF is the **entry point** (5.10.3).
- The IBCF ends the request in a dialog with the side it came from, and sends a new request in a dialog of its own with the other side (TS 24.229 5.10.5, TS 29.165 5.2.1). Each dialog has its own Call-ID, tags, CSeq, Via and Contact.
- Nothing that routes a message inside one network belongs to the dialog with the other network. That is how the B2BUA hides the topology of its network.
- The TrGW is the media relay of the `agw` role again. Its `core` termination faces its own network, and its `access` termination the TrGW of the other network.

> Guide: ["What the IBCF and the TrGW do"](../../../docs/guide/en/interconnect.md#what-the-ibcf-and-the-trgw-do)

---

## The four reference points of the border

```ims-scene
id: refpoints
nodes: [scscf-a, ibcf-a, trgw-a, ibcf-b, trgw-b]
lead: Two of them are between the networks and two are inside one network. The II-NNI is made of the two between.
steps:
  - id: mx
    title: Mx is between a CSCF and the IBCF of its own network
    show: [mx]
    text: |
      It carries SIP. The `ibcf` setting of the S-CSCF names it, and it goes on top of Route of a request that leaves.
      It is a reference point inside one network (TS 29.165 3.2).
  - id: ici
    title: Ici is between the IBCFs of two networks
    show: [ici]
    checkpoint: true
    text: |
      This is the SIP on the link. An INVITE with SDP is over 1300 bytes, so it goes over TCP (RFC 3261 18.1.1).
  - id: izi
    title: Izi is between the TrGWs of two networks
    show: [izi]
    checkpoint: true
    text: |
      The RTP that crosses the border runs here. Ici and Izi make up the II-NNI (TS 29.165 5.1).
  - id: ix
    title: Ix is between an IBCF and the TrGW of its own network
    show: [ix]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a, ibcf-b, trgw-b]
    checkpoint: true
    text: |
      H.248 in the standard (TS 29.238). Here it is the HTTP control API that the P-CSCF uses for its own relay.
      This one is inside one network too.
```

| Reference point | Between | In this project |
| --- | --- | --- |
| Mx | a CSCF and the IBCF of its own network | SIP. The `ibcf` setting of the S-CSCF names it, and it goes on top of Route. |
| Ici | the IBCFs of two networks | SIP on the link. An INVITE with SDP is over 1300 bytes, so it goes over TCP (RFC 3261 18.1.1). |
| Izi | the TrGWs of two networks | The RTP that crosses the border. |
| Ix | the IBCF and its TrGW | H.248 in the standard (TS 29.238). Here the HTTP control API that the P-CSCF uses for its own relay. |

- Ici and Izi make up the II-NNI (TS 29.165 5.1). Mx and Ix are inside one network (TS 29.165 3.2).
- Session 2 drew these four on the map of the network. This session reads what they carry.

> Guide: ["What the IBCF and the TrGW do"](../../../docs/guide/en/interconnect.md#what-the-ibcf-and-the-trgw-do)

---

## Run it

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml up -d --build

# UE B1 waits for a call. The UE simulator takes network B's settings as flags.
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 answer \
  -domain ims.mnc002.mcc001.3gppnetwork.org \
  -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org \
  -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org \
  -password test-password-b1 \
  -pcscf 172.28.1.40:5060 -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060

# UE A1 calls that number, with the settings of network A as always.
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue call tel:+12025550151
```

- `ue sms` of network B takes one flag more, `-sc`, the number of the SC of that network.
- `scripts/e2e-interconnect.sh` runs everything of this session and checks it in the three captures.

> Guide: ["Run it"](../../../docs/guide/en/interconnect.md#run-it)

---

## Open the three captures

| Capture | What is in it |
| --- | --- |
| `captures/ims-*.pcap` | Network A: the ENUM, the INVITE from the S-CSCF to IBCF-A, and the messages of network B as IBCF-A passes them on. |
| `captures/nni/ims-*.pcap` | The link: the two IBCFs and the two TrGWs, and nothing else. |
| `captures/b/ims-*.pcap` | Network B: the INVITE from IBCF-B to the I-CSCF and on, and the RTP of TrGW-B. |

- Three capture containers, one per bridge. Open all three: the same call is in each of them, under a Call-ID of its own.
- The filters of the earlier sessions work in each file. `sip.Method == "INVITE" && count(sip.Via) == 1` picks the INVITEs of the IBCFs out of the link.
- **Statistics > Flow Graph** on the capture of the link draws the sequence diagram between the two IBCFs.

> Guide: ["What each capture shows"](../../../docs/guide/en/interconnect.md#what-each-capture-shows)

---

## A call from network A to network B

```ims-scene
id: overview
view: ladder
nodes: [ue-a1, scscf-a, ibcf-a, ibcf-b, icscf-b, hss-b, scscf-b, ue-b1]
lead: One call runs in two networks and on the link between them. At each border the request ends and is built again.
steps:
  - id: out
    title: Through network A, up to the border (1 to 3)
    show: [inv-ue-s, inv-s-ia]
    detail: []
    checkpoint: true
    text: |
      UE A1 sends an INVITE to a number, as in any other call. The P-CSCF and the MMTel AS are not drawn here.
      Once the S-CSCF sees that the number is not its own network's, it sends the call to an IBCF of its own. The ENUM in between is not drawn either.
  - id: across
    title: Across the border, to the entry of network B (4, 5)
    show: [inv-nni, inv-ib-i]
    detail: []
    checkpoint: true
    text: |
      The INVITE on the link is IBCF-A's own, and the one that enters network B is IBCF-B's own.
      Each IBCF ends the dialog it received and starts one of its own. With the dialog of the MMTel AS that this picture leaves out, the whole call has four.
  - id: find
    title: Network B finds the callee (6)
    show: [lir, lia, inv-i-sb, inv-sb-ue]
    active: lir
    detail: []
    checkpoint: true
    text: |
      The I-CSCF turns the number into a tel URI before it asks the HSS, which names the S-CSCF of UE B1.
      From there it is an ordinary call to a user of network B.
  - id: answer
    title: The answer comes back to UE A1 (7)
    only: [r183-ue-sb, r183-sb-i, r183-i-ib, r183-ib-ia, r183-ia-s, r183-s-ue]
    active: r183-s-ue
    detail: []
    checkpoint: true
    text: |
      The 183 goes back along the Via, so it passes the I-CSCF, which put nothing in Record-Route.
      Each IBCF passes the 183 it received on as a 183 of its own, in its other dialog.
  - id: rest
    title: Each dialog has its own PRACKs, ACK and BYE
    only: [inv-s-ia, inv-nni, inv-ib-i]
    active: inv-nni
    detail: []
    checkpoint: true
    text: |
      Each of the four dialogs of the call has its own PRACKs, ACK and BYE (8). The ladder marks the three around the border. The fourth is between UE A1 and the MMTel AS.
      The RTP runs from UE A1 to the media relay of network A, to TrGW-A, to TrGW-B and to UE B1 (9).
```

```ladder
UE A1, S-CSCF, DNS-A, IBCF-A, IBCF-B, I-CSCF, UE B1
UE A1 -> S-CSCF: INVITE tel:+12025550151, through the P-CSCF and the MMTel AS // 1
S-CSCF <-> DNS-A: ENUM, which gives a SIP URI of network B's domain // 2
S-CSCF -> IBCF-A: INVITE, the IBCF on top of Route // 3
IBCF-A -> IBCF-B: INVITE on the link, in a dialog of IBCF-A's own // 4
IBCF-B -> I-CSCF: INVITE, the number in a SIP URI // 5
I-CSCF -> UE B1: INVITE tel:+12025550151, through the S-CSCF and the P-CSCF // 6
UE A1 <-- S-CSCF <-- IBCF-A <-- IBCF-B <-- I-CSCF <-- UE B1: 183 with the answer // 7
```

- The S-CSCF translates the number with ENUM once no AS is left (TS 24.229 5.4.3.2 step 10), and sends what is not its own domain through an IBCF of its own network (step 11).
- The HSS of network B knows UE B1 by its tel URI, so the I-CSCF puts the tel URI in the Request-URI before it asks with an LIR (TS 24.229 5.3.2.1 item 1 b).
- Each dialog has its own PRACKs, ACK and BYE. The RTP runs from UE A1 to the media relay of network A, to TrGW-A, over the link to TrGW-B, and to UE B1.
- Neither UE learns anything of the other network: the far end of UE A1 is 172.28.0.50, that of UE B1 is 172.28.1.91.

> Guide: ["A call from network A to network B"](../../../docs/guide/en/interconnect.md#a-call-from-network-a-to-network-b)

---

## Network A finds the peer and sends the request out (1 to 4)

```ims-scene
id: exit
nodes: [dns-a, ue-a1, scscf-a, ibcf-a, trgw-a, ibcf-b]
lead: The answer of the ENUM is what tells that the number belongs to another network. From there the destination is an IBCF of its own network.
steps:
  - id: invite
    title: UE A1 calls a number
    show: [inv-ue-s]
    checkpoint: true
    text: |
      The INVITE of any other call. It goes through the P-CSCF and, by the originating filter criteria, through the MMTel AS and back to the S-CSCF.
      Up to here nobody knows that the number belongs to another network.
  - id: enum
    title: The ENUM gives the domain of network B
    hide: [inv-ue-s]
    show: [enum-q, enum-a]
    active: enum-a
    detail: [enum-q, enum-a]
    zoom: [dns-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      No AS is left to invoke, so the S-CSCF translates the number with ENUM.
      What the record holds is the number, not the private identity of a subscriber.
  - id: route
    title: The S-CSCF puts its own IBCF on top of Route
    show: [scscf-exit]
    focus: [scscf-a]
    zoom: [dns-a, scscf-a, ibcf-a]
    text: |
      The domain is not its own, so the call goes through an IBCF of its own network, which has the interconnect function.
      Without the `ibcf` setting the S-CSCF would send it to network B's domain itself, and it would reach nothing.
  - id: arrive
    title: The request arrives on the internal side of IBCF-A
    hide: [enum-q, enum-a]
    show: [inv-s-ia]
    checkpoint: true
    text: The S-CSCF put its own URI in Record-Route as always, so the requests within the dialog pass the S-CSCF too.
  - id: border
    title: IBCF-A builds the request again as the exit point
    show: [ibcfa-exit]
    focus: [ibcf-a]
    zoom: [scscf-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      It applies the agreement with the peer network to a copy of the request it received.
      What it copies, what it removes and what it rewrites is what that agreement decides.
  - id: trgw
    title: It reserves a context in the TrGW of its own network
    show: [trgw-add]
    detail: [trgw-add]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a]
    checkpoint: true
    text: |
      The media of the call goes through two terminations, one facing each network.
      There is one context for the call, reserved when the IBCF sees the first SDP.
  - id: trgw-sdp
    title: It writes one address per side
    hide: [trgw-add]
    show: [trgw-mod-a]
    detail: [trgw-mod-a]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a]
    checkpoint: true
    text: |
      The relay writes the same address on both of its terminations, so the IBCF writes one address per side.
      The SDP that leaves for the link carries the link side address of TrGW-A.
  - id: send
    title: IBCF-A sends an INVITE of its own onto the link
    only: [inv-s-ia, inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      Route holds IBCF-B, the entry point of the peer that the agreement names.
      It puts nothing of itself in Record-Route: the requests of network B reach IBCF-A by its Contact.
```

The agreement with the other network, the interconnect profile, decides what the IBCF copies from one dialog to the other.

| What | Reference |
| --- | --- |
| It removes the header fields that must not leave the trust domain | TS 24.229 4.4 |
| It screens the methods, the option tags and the header fields | TS 24.229 5.10.6 |
| It writes the numbers in the form that the agreement asks for | TS 29.165 8.1 |
| It takes the codecs that the other network does not take out of the SDP | TS 29.165 7.1 |
| It puts the media through its TrGW and writes that address into the SDP | TS 24.229 6.7.1.4.1 |

- The two operators have an agreement, so each network is in the other's trust domain (`trusted: true`, TS 24.229 4.4.1). P-Asserted-Identity crosses, and UE B1 learns who calls (4.4.2).
- P-Charging-Function-Addresses never crosses (5.10.2.2 step 9). With `trusted: false`, UE B1 would get the call without the caller's number.

> Guide: [2](../../../docs/guide/en/interconnect.md#2-enum-finds-network-b), [3](../../../docs/guide/en/interconnect.md#3-the-s-cscf-sends-the-call-to-ibcf-a), [4](../../../docs/guide/en/interconnect.md#4-ibcf-a-the-exit-point) and ["The trust domain"](../../../docs/guide/en/interconnect.md#the-trust-domain)

---

## From the border of network B to UE B1 (5 to 7)

```ims-scene
id: entry
nodes: [ibcf-a, ibcf-b, trgw-b, icscf-b, hss-b, scscf-b, ue-b1]
lead: The address it came from is what tells that the request is a peer network's. From there it is an ordinary call to a user of network B.
steps:
  - id: arrive
    title: The request arrives on the external side of IBCF-B
    show: [inv-nni]
    zoom: [ibcf-a, ibcf-b, icscf-b]
    checkpoint: true
    text: It comes from the address of IBCF-A on the link. Nothing inside network B sees this request.
  - id: border
    title: IBCF-B builds the request again as the entry point
    show: [ibcfb-entry]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b, icscf-b]
    checkpoint: true
    text: |
      The address it came from names the peer, so it applies the agreement with network A.
      No Route is left, so the next hop is the I-CSCF of its configuration.
  - id: icscf
    title: The request enters at the entry of network B
    show: [inv-ib-i]
    text: From here on it is inside network B. Via and Call-ID hold nothing but what IBCF-B made.
  - id: tel
    title: The I-CSCF puts a tel URI in the Request-URI
    show: [icscfb-tel]
    focus: [icscf-b]
    zoom: [icscf-b, hss-b, scscf-b]
    checkpoint: true
    text: |
      A number from a peer network arrives in a SIP URI with `user=phone`.
      The HSS does not know the user in that form, so the change comes before the question.
  - id: lir
    title: It asks the HSS where the callee is (LIR and LIA)
    show: [lir, lia]
    active: lia
    detail: [lia]
    zoom: [icscf-b, hss-b, scscf-b]
    text: The same Cx question as in the sessions on registration and on a call. A call from network A changes nothing about it.
  - id: deliver
    title: Through network B to UE B1
    show: [inv-i-sb, inv-sb-ue]
    active: inv-sb-ue
    detail: [inv-sb-ue]
    checkpoint: true
    text: |
      The S-CSCF takes the terminating side and sends the call along the Path of UE B1, to the P-CSCF of network B.
      The SDP that reaches UE B1 points at TrGW-B. No address of the other network is in it.
  - id: answer
    title: UE B1 answers with a 183 that carries the answer
    show: [r183-ue-sb, r183-sb-i, r183-i-ib]
    active: r183-i-ib
    detail: [r183-ue-sb]
    checkpoint: true
    text: |
      A reliable 183. It goes back along the Via, so it passes the I-CSCF.
      IBCF-B passes it on as a 183 of its own, in its dialog on the link.
```

- IBCF-B takes the request on its external side, and the address it came from is in network A's agreement, so it knows the request is network A's. It applies the profile `network-a.yaml` and removes its own URI from Route (TS 24.229 5.10.3.2 step 1C).
- No Route is left, so it sends a new INVITE in a dialog of its own to the I-CSCF of its configuration (step 4). The media goes through TrGW-B the same way, so the SDP that reaches network B points at 172.28.1.91.
- The HSS of network B knows UE B1 by its tel URI, not by a SIP URI with `user=phone`. So the I-CSCF puts `tel:+12025550151` in the Request-URI before it sends the LIR (TS 24.229 5.3.2.1 item 1 b).
- The S-CSCF of network B gets the tel URI, and from there the call reaches UE B1 through the P-CSCF of network B, as any other terminating call.

> Guide: [5](../../../docs/guide/en/interconnect.md#5-ibcf-b-the-entry-point), [6](../../../docs/guide/en/interconnect.md#6-the-i-cscf-of-network-b-finds-the-callee) and [7](../../../docs/guide/en/interconnect.md#7-the-answer-comes-back)

---

## Three dialogs around the border, three Call-IDs

```ims-scene
id: dialogs
view: ladder
nodes: [scscf-a, ibcf-a, ibcf-b, icscf-b]
lead: Three of the four dialogs of the call are around the border, one in each capture, each with a Call-ID of its own. The fourth is between UE A1 and the MMTel AS.
steps:
  - id: first
    title: The INVITE in the capture of network A
    show: [inv-s-ia]
    detail: [inv-s-ia]
    checkpoint: true
    text: |
      The INVITE from the S-CSCF to IBCF-A, the second dialog of the call: the MMTel AS made it. Its Call-ID and its Via entries are network A's.
      This INVITE ends at IBCF-A.
  - id: legs-a
    title: IBCF-A ends it and sends it again
    show: [ibcfa-legs]
    focus: [ibcf-a]
    text: What it copies between its two UAs is what the agreement with the peer network decides.
  - id: nni
    title: The INVITE in the capture of the link
    show: [inv-nni]
    detail: [inv-nni]
    checkpoint: true
    text: |
      Via holds the external name of IBCF-A alone, and there is no Record-Route.
      Max-Forwards is one lower, so a call that two networks send round in a loop still ends.
  - id: legs-b
    title: IBCF-B does the same
    show: [ibcfb-legs]
    focus: [ibcf-b]
    text: It ends the request in its dialog on the link and sends one of its own in its dialog with network B.
  - id: third
    title: The INVITE in the capture of network B
    show: [inv-ib-i]
    detail: [inv-ib-i]
    checkpoint: true
    text: Via and Call-ID are IBCF-B's. Not one value of network A is in it.
  - id: compare
    title: The three Call-IDs side by side
    active: ''
    detail: [inv-s-ia, inv-nni, inv-ib-i]
    checkpoint: true
    text: Each dialog has its own Call-ID, so take the Call-ID again in each file you follow the call in.
```

The INVITE on the link, shortened.

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Via: SIP/2.0/TCP ibcf.nni.mnc001.example.net:5060;branch=z9hG4bKW6NI6...;rport
Max-Forwards: 65
Route: <sip:ibcf.nni.mnc002.example.net:5060;lr>
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=5MWO4Z6R4C
Call-ID: FMZQJ3BZL2YI4ILP6CEIPAUT5Z
Contact: <sip:ibcf.nni.mnc001.example.net:5060>
```

- **Via.** One entry, IBCF-A's. The nodes of network A put theirs on the INVITE that reached IBCF-A, and that INVITE ended there.
- **Record-Route.** None. IBCF-A is the far end of this dialog, so the requests of network B reach it by its Contact.
- **Call-ID.** IBCF-A's own. Compare it with the INVITE to IBCF-A and with the one from IBCF-B: the three are different. The INVITE of UE A1 to the P-CSCF, in the capture of network A too, has a fourth.
- **Max-Forwards.** IBCF-A took one hop off, as a proxy would (RFC 3261 16.6 step 3), so a call that two networks send round in a loop still ends.

> Guide: ["Three dialogs around the border in the captures"](../../../docs/guide/en/interconnect.md#three-dialogs-around-the-border-in-the-captures)

---

## The media goes through two TrGWs (9)

```ims-scene
id: media
nodes: [ue-a1, scscf-a, ibcf-a, agw-a, trgw-a, ibcf-b, trgw-b, ue-b1]
lead: Each UE sends its media to an address of its own network. No address of the other network is in either SDP.
steps:
  - id: modify-b
    title: IBCF-B tells TrGW-B the address of UE B1
    show: [trgwb-mod]
    focus: [ibcf-b, trgw-b]
    zoom: [ibcf-b, trgw-b, ue-b1]
    checkpoint: true
    text: |
      The answer of UE B1 has come back to IBCF-B through the P-CSCF, the S-CSCF and the I-CSCF of network B. Those hops are not drawn here.
      The SDP in network B points at UE B1: network B has no media relay at its P-CSCF.
  - id: nni
    title: The 183 on the link carries the address of TrGW-B
    show: [r183-ib-ia]
    detail: [r183-ib-ia]
    zoom: [ibcf-a, ibcf-b, trgw-a, trgw-b]
    checkpoint: true
    text: The SDP that leaves for the link carries the link side address of TrGW-B.
  - id: modify-a
    title: IBCF-A tells TrGW-A about the TrGW of the peer
    show: [trgwa-mod-back]
    focus: [ibcf-a, trgw-a]
    zoom: [ibcf-a, trgw-a, trgw-b]
    text: The access side of TrGW-A now sends to TrGW-B.
  - id: to-a
    title: The 183 that goes back into network A carries the address of TrGW-A
    show: [r183-ia-s, r183-s-ue]
    active: r183-s-ue
    detail: [r183-ia-s, r183-s-ue]
    checkpoint: true
    text: |
      The SDP for network A carries the network A side address of TrGW-A, and the P-CSCF rewrites it once more, to the relay of its own network.
      The address that UE A1 receives is 172.28.0.50.
  - id: rtp
    title: The RTP runs over four hops
    only: [rtp-ue-agw, rtp-agw-trgw, rtp-nni, rtp-trgw-ue]
    active: rtp-nni
    checkpoint: true
    text: |
      This is the direction from UE A1 to UE B1. The other direction takes the same hops back.
      The script checks that the PCMU payload UE A1 sent is the one UE B1 received.
  - id: ends
    title: Neither UE learns anything of the other network
    focus: [ue-a1, ue-b1]
    checkpoint: true
    text: |
      The far end of UE A1 is 172.28.0.50, that of UE B1 is 172.28.1.91: an address of its own network in both cases.
      On the hop that crosses the border, the only addresses are those of the two TrGWs.
```

| What to look at | What to see |
| --- | --- |
| The SDP in the capture of network A | `c=` is 172.28.0.50 or 172.28.0.91: addresses inside network A only. |
| The SDP in the capture of the link | `c=` is 172.28.2.11 or 172.28.2.21: the link side of the two TrGWs. |
| The SDP in the capture of network B | `c=` is 172.28.1.91 or the address of UE B1. |
| The RTP that crosses the border | Between the two TrGWs only. No address of a UE shows up. |

The filters are `sdp.connection_info.address == 172.28.2.11` and `rtp && ip.addr == 172.28.2.0/24`.

- For each SDP the IBCF tells the termination that faces where the SDP comes from where that side takes its media from now on. Then it writes the address of the TrGW that faces the other side into the SDP (TS 24.229 6.7.1.4.1).
- When that fails, the IBCF does not pass the SDP across the border. If it crossed, the far side would send its RTP to that address directly, past the TrGW, and the peer network would learn an address of this one. "When the TrGW does not take an SDP" in the guide has it.

> Guide: [7](../../../docs/guide/en/interconnect.md#7-the-answer-comes-back) and [9](../../../docs/guide/en/interconnect.md#9-the-media)

---

## Topology hiding

| Capture | What must not be there |
| --- | --- |
| Network A | Any address of 172.28.1.0/24, and any host name of a node of network B. |
| The link | Any address of either network's inside, any host name of their nodes, any identity made from an IMSI of network A, and any Record-Route. |
| Network B | Any address of 172.28.0.0/24, any host name of a node of network A, and any identity made from an IMSI of network A. |

- The script looks for the addresses in the IP headers and anywhere in the frames, and for the names of the nodes in front of the other network's domain. It finds none.
- Some names of the home network do show in the peer network, and should: the domain itself, in From and P-Asserted-Identity. The peer network needs the users' numbers in order to call back.
- A number and a domain tell nothing of the nodes inside, and nothing of the IMSI.

> Guide: ["What each capture shows"](../../../docs/guide/en/interconnect.md#what-each-capture-shows)

---

## The agreement of two operators

| Field | What it decides |
| --- | --- |
| `name`, `domain` | The peer network, and the host of a number that leaves as a SIP URI (TS 29.165 8.1). |
| `trusted` | Whether the peer is in the trust domain (TS 24.229 4.4). |
| `transports`, `methods` | The transports to send with, and the optional methods that the agreement allows, such as MESSAGE (TS 29.165 6.2, Table 6.1). |
| `option_tags` | `pass`, `remove` or `reject` for each option tag. With `reject` the IBCF answers 420 to a request that requires the extension. |
| `headers` | The operator's rules to remove or rewrite header fields (TS 24.229 5.10.6.2). |
| `number_format`, `local_numbers` | The form of the numbers, and how a local number becomes global (TS 29.165 8.1). |
| `codecs` | The payload formats that the peer takes (TS 29.165 7.1). |

- `profiles/network-b.yaml` is network A's agreement with network B, `network-a.yaml` network B's with network A. TS 29.165 Annex C lists such choices as option items that two operators fill in.
- `IBCF_A_PROFILE` and `IBCF_B_PROFILE` pick another file for an IBCF, which Compose then starts again with it.

> Guide: ["The profiles"](../../../docs/guide/en/interconnect.md#the-profiles)

---

## When the two agreements do not match

```ims-scene
id: mismatch
nodes: [ipsmgw-a, scscf-a, ibcf-a, ibcf-b]
lead: The failures come from the IBCFs, by the agreement, not from the UEs. The node that rejected is the source of the first packet that carries the response.
steps:
  - id: message
    title: MESSAGE is not in network A's agreement
    only: [sms-ip-ia, ibcfa-no-message]
    focus: [ibcf-a]
    zoom: [ipsmgw-a, scscf-a, ibcf-a]
    text: |
      MESSAGE is an optional method, so it needs an agreement to cross the border.
      This method is not in the agreement, so IBCF-A does not pass the request on.
  - id: m405
    title: IBCF-A answers 405
    show: [r405]
    detail: [ibcfa-no-message]
    zoom: [ipsmgw-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      The SC of network A reports to UE A1 that it could not forward the message.
      Put `network-b.yaml` back, and the same short message crosses and gets TP-ST 1.
  - id: codec
    title: Network B takes AMR-WB alone
    only: [inv-nni, ibcfb-codec]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b]
    text: |
      The codecs that the peer takes are in `codecs` of the agreement.
      IBCF-B removes from the offer every codec that is not there.
  - id: c488
    title: IBCF-B answers 488
    show: [r488]
    detail: [ibcfb-codec]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      Not one speech codec is left, so there is no session to set up.
      It does not transcode: that needs codecs in the TrGW, and the nodes of this project neither encode nor decode AMR.
  - id: number
    title: A local number crosses as it is
    only: [inv-nni-local, ibcfb-number]
    active: inv-nni-local
    detail: [inv-nni-local]
    focus: [ibcf-a, ibcf-b]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      Network A's agreement passes a local number as it is, so the number that crossed the border is still a local one.
      Network B's agreement allows global numbers only.
  - id: n404
    title: IBCF-B answers 404
    show: [r404]
    detail: [ibcfb-number]
    zoom: [ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      A rejection by local policy (TS 24.229 5.10.1).
      With `network-b-global-numbers.yaml`, IBCF-A makes the number global before it leaves.
```

| Mismatch | What happens | Adjusted agreement |
| --- | --- | --- |
| MESSAGE is not in network A's agreement | IBCF-A answers the short message with 405 (Method Not Allowed). The SC of network A reports TP-ST 98, "no response from SME". | `network-b.yaml`: TP-ST 1 |
| Network B takes AMR-WB alone | IBCF-B removes every speech codec from UE A1's offer of PCMU and answers 488 (Not Acceptable Here). | `network-a.yaml`, which lists PCMU |
| A local number crosses as it is | Network B's agreement allows only global numbers, so IBCF-B rejects the INVITE with 404 (Not Found) by local policy. | `network-b-global-numbers.yaml`: IBCF-A makes the number global before it leaves |

- The failures come from the IBCFs, by the agreement, not from the UEs: compare the INVITE on the link with the one that reaches network B.
- The script runs each mismatch and then the agreement that fits, so the capture holds the call that failed and the one that went through, one after the other.

> Guide: ["When the profiles do not match"](../../../docs/guide/en/interconnect.md#when-the-profiles-do-not-match). TS 29.165 7.1, Table 6.1. TS 24.229 5.10.1.

---

## A request from no peer network

```ims-scene
id: screen
nodes: [host99, ibcf-a, scscf-a]
lead: The border answers nothing at all to a sender it has no agreement with, not even that a SIP node listens there.
steps:
  - id: invite
    title: A host on the link sends an INVITE
    show: [inv-99]
    zoom: [host99, ibcf-a]
    text: |
      172.28.2.99 is on the link, and it is neither IBCF-B nor a TrGW.
      The script sends an INVITE and a MESSAGE from that address to IBCF-A.
  - id: screen
    title: The stack asks the IBCF whether it may come in
    show: [ibcfa-screen]
    focus: [ibcf-a]
    zoom: [host99, ibcf-a]
    checkpoint: true
    text: |
      Every request that belongs to no transaction the stack has goes through this question before anything else.
      When the answer is no, the stack drops the request.
  - id: message
    title: The MESSAGE is dropped the same way
    show: [msg-99]
    active: msg-99
    zoom: [host99, ibcf-a]
    text: |
      That holds for every method and for each retransmission: it remembers nothing of a request it dropped.
      No 100 (Trying) and no error response come back.
  - id: nothing
    title: Nothing enters network A
    focus: [scscf-a]
    checkpoint: true
    text: |
      The capture of network A holds nothing of this host.
      The capture of the link holds two arrows with no answer.
```

```text
host 172.28.2.99            IBCF-A ║ network A
  |  INVITE                    |   ║
  |--------------------------->| x ║   (dropped: no peer sends from 172.28.2.99)
  |  MESSAGE                   |   ║
  |--------------------------->| x ║
  |                            |   ║
  |  (nothing comes back)      |   ║   (nothing enters network A)
```

- IBCF-A takes requests on its external side only from the addresses that its profile of network B lists (`sources` in `ibcf.yaml`).
- It drops both without any answer: no 100 (Trying), no error response, nothing at all, so the sender learns nothing of the network, not even that a SIP node listens there.
- The SIP stack asks the IBCF about each request that belongs to no transaction it has, before it does anything with it (`IBCF.Screen`). That holds for every method and for each retransmission.
- A peer inside the agreement does get a response, such as the 405 of a method outside it: TS 24.229 5.10.1 lets an IBCF reject a request with a 4xx by local policy.

> Guide: ["A request from no peer network"](../../../docs/guide/en/interconnect.md#a-request-from-no-peer-network)

---

## Short messages across

```ims-scene
id: sms
nodes: [dns-a, ue-a1, scscf-a, ibcf-a, ipsmgw-a, ibcf-b, icscf-b, scscf-b, ue-b1, ipsmgw-b]
lead: The MESSAGE that crosses the border comes from the SC by its number. No host name of network A crosses.
steps:
  - id: submit
    title: UE A1 sends a short message to its own SC
    show: [sms-ue-s, sms-s-ip]
    active: sms-s-ip
    detail: [sms-ue-s]
    zoom: [ue-a1, scscf-a, ipsmgw-a, ibcf-a]
    checkpoint: true
    text: |
      The MESSAGE of the session on SMS. An originating filter criterion gives it to the IP-SM-GW.
      The SC stub first asks the HSS of network A for the number and is told that there is no such user. That question is not drawn here.
  - id: enum
    title: The SC finds the peer network with ENUM
    show: [sms-enum-q, sms-enum-a]
    active: sms-enum-a
    detail: [sms-enum-a]
    zoom: [dns-a, scscf-a, ipsmgw-a, ibcf-a]
    checkpoint: true
    text: |
      With an `ibcf` setting, the SC looks a number it does not find at home up in the ENUM.
      It accepts the message here, and a submit report reaches UE A1.
  - id: deliver
    title: The IP-SM-GW sends a MESSAGE to IBCF-A
    only: [sms-ip-ia]
    detail: [sms-ip-ia]
    zoom: [ipsmgw-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      The body is an SMS-DELIVER, and the destination is the SIP URI of network B's domain that the ENUM gave.
      The sender is the SC by its number, so that no host name of network A leaves the border.
  - id: cross
    title: The MESSAGE crosses the border
    show: [sms-nni, sms-ib-i, sms-i-sb]
    active: sms-nni
    detail: [sms-nni]
    checkpoint: true
    text: |
      Both profiles allow MESSAGE.
      In network B the I-CSCF turns the Request-URI into a tel URI and hands the message to the S-CSCF. The diagram leaves out its LIR and LIA.
```

```ladder
UE A1, IP-SM-GW-A, IBCF-A, IBCF-B, IP-SM-GW-B, UE B1
UE A1 -> IP-SM-GW-A: MESSAGE with the SMS-SUBMIT // 1
IP-SM-GW-A -> IBCF-A: MESSAGE with the SMS-DELIVER, from the SC's number // 3
IBCF-A -> IBCF-B: MESSAGE on the link
IBCF-B -> IP-SM-GW-B: MESSAGE, through the I-CSCF and the S-CSCF // 4
IP-SM-GW-A <-- IBCF-A <-- IBCF-B <-- IP-SM-GW-B: 202 // 5
UE A1 <-- IP-SM-GW-A: MESSAGE, the status report with TP-ST 1
IP-SM-GW-B -> UE B1: MESSAGE with the SMS-DELIVER // 6
```

- The SC stub asks the HSS of network A for the number, is told "user unknown", and with an `ibcf` setting then finds it in network B's domain by ENUM. Both profiles allow MESSAGE.
- The MESSAGE that crosses comes from the SC by its number, so no host name of network A crosses. No delivery report comes back: the SC ends its part when network B takes the message, and TP-ST 1 says that it forwarded the message but cannot confirm the delivery (TS 23.040 9.2.3.15).
- In network B a terminating filter criterion gives the MESSAGE to the IP-SM-GW of that user (TS 23.204 6.4a step 2), which delivers it with network A's SC in the RP-OA.

> Guide: ["Short messages across"](../../../docs/guide/en/interconnect.md#short-messages-across)

---

## Network B takes it, and a 202 goes back to network A

```ims-scene
id: sms-b
nodes: [ue-a1, scscf-a, ipsmgw-a, ibcf-a, ibcf-b, icscf-b, scscf-b, ue-b1, ipsmgw-b]
lead: No delivery report crosses back. The part of network A's SC ends when network B takes the MESSAGE.
steps:
  - id: ifc
    title: A terminating filter criterion picks the MESSAGE from the peer
    only: [sms-sb-ip]
    detail: [sms-sb-ip]
    zoom: [scscf-b, ue-b1, ipsmgw-b]
    checkpoint: true
    text: |
      A short message from another IMS network reaches the IP-SM-GW of that user (TS 23.204 6.4a step 2).
      The criterion leaves out what its own IP-SM-GW sent, so a MESSAGE never circles between the S-CSCF and the IP-SM-GW.
  - id: accepted
    title: The IP-SM-GW of network B answers 202
    only: [sms-202-nni, sms-202-ip]
    active: sms-202-ip
    zoom: [ipsmgw-a, ibcf-a, ibcf-b]
    checkpoint: true
    text: |
      The 202 takes the way it came, from the IP-SM-GW of network B.
      The SC of network A takes the delivery as done here: no delivery report crosses back.
  - id: report
    title: The status report in network A gets TP-ST 1
    show: [sms-report, sms-report-ue]
    active: sms-report
    detail: [sms-report]
    checkpoint: true
    text: |
      TP-ST 1 says that it forwarded the message but cannot confirm the delivery (TS 23.040 9.2.3.15).
      The status report reaches UE A1 in a MESSAGE, as in the session on SMS.
  - id: b-deliver
    title: The IP-SM-GW of network B delivers it in a MESSAGE of its own
    only: [sms-deliver-i, sms-deliver-sb]
    active: sms-deliver-i
    checkpoint: true
    text: |
      The way of any short message inside network B: the I-CSCF finds the S-CSCF of the user, which sends it along the route of UE B1.
      The body is an SMS-DELIVER.
  - id: b-ue
    title: The SMS-DELIVER reaches UE B1
    only: [sms-deliver-ue]
    detail: [sms-deliver-ue]
    checkpoint: true
    text: Network A's SC stays in the RP-OA, and the sender that UE B1 shows is the number of UE A1.
```

- The IP-SM-GW of network B answers 202. No delivery report comes back across the border, so the part of network A's SC ends there.
- In network B a terminating filter criterion gives the MESSAGE to the IP-SM-GW of that user, which delivers it with network A's SC in the RP-OA.
- The other way, from UE B1 to UE A1, works the same with the networks swapped: the SC of network B finds network A's domain by ENUM, and the message leaves through IBCF-B.

> Guide: ["Short messages across"](../../../docs/guide/en/interconnect.md#short-messages-across) and ["The other way, from network B to network A"](../../../docs/guide/en/interconnect.md#the-other-way-from-network-b-to-network-a)

---

## A conference across the border

```ims-scene
id: conference
nodes: [mmtel-a, scscf-a, ibcf-a, ibcf-b, icscf-b, scscf-b, ue-b1]
lead: "Not every Contact is replaced: the conference URI has to reach the participant unchanged."
steps:
  - id: invite
    title: The focus invites a user of the peer network
    show: [conf-inv-m-s, conf-inv-s-ia]
    active: conf-inv-s-ia
    detail: [conf-inv-m-s]
    zoom: [mmtel-a, scscf-a, ibcf-a]
    checkpoint: true
    text: |
      On behalf of the conference URI, a PSI that it serves, the focus sends the INVITE to the S-CSCF of its own network with `orig`.
      The ENUM turns the number into a SIP URI of network B, so the INVITE leaves through IBCF-A.
  - id: contact
    title: A Contact with isfocus passes on as it came
    show: [conf-inv-nni]
    detail: [conf-inv-nni]
    checkpoint: true
    text: |
      TS 24.229 5.10.5 has the IBCF pass a Contact on as it came when it is a GRUU, or when a feature tag tells what the far end can do at that URI.
      Its example is `isfocus`, the tag of a conference focus.
  - id: record-route
    title: An IBCF that passes such a Contact puts itself in Record-Route
    show: [conf-inv-ib-i]
    detail: [conf-inv-ib-i]
    focus: [ibcf-b, icscf-b]
    checkpoint: true
    text: |
      So that the requests to the conference URI still come to it. In network B, IBCF-B puts its own internal name in.
      From here the INVITE goes through network B to UE B1, as any other call.
  - id: subscribe
    title: A SUBSCRIBE outside the dialog does not cross
    only: [conf-sub, ibcfb-sub]
    focus: [ibcf-b]
    zoom: [ibcf-a, ibcf-b, scscf-b, ue-b1]
    text: |
      UE B1 sends a SUBSCRIBE to the conference URI it stored (TS 24.147 5.3.1.2).
      IBCF-B does not pass it across the border.
  - id: forbidden
    title: IBCF-B answers 403 itself
    show: [conf-403]
    detail: [ibcfb-sub]
    zoom: [ibcf-a, ibcf-b, scscf-b, ue-b1]
    checkpoint: true
    text: |
      The participant that gets the 403 subscribes again within the dialog of its conference session.
      That one crosses like any other request of the call, as long as the agreement allows SUBSCRIBE and NOTIFY.
```

- Not every Contact is replaced. TS 24.229 5.10.5 has the IBCF pass a Contact on as it came when it is a GRUU, or when a feature tag tells what the far end can do at that URI. Its example is `isfocus`, the tag of a conference focus.
- The URI in that Contact is the conference URI, which a participant stores, subscribes to and rejoins the conference with (TS 24.147 5.3.1.3.2), so it has to reach the participant unchanged.
- When the IBCF passes such a Contact on, it puts its own URI in Record-Route, so that the requests to the conference URI still come to it. They are the only Record-Route on the link.
- A SUBSCRIBE outside the dialog does not cross: the IBCF answers it with 403, and the participant then subscribes within the dialog of its conference session, as TS 24.147 5.3.1.2 has it do after a 403.
- The focus of this project names each endpoint of the conference state by a `urn:uuid:` URI, so network B sees no address and no node name of network A in the NOTIFYs.

> Guide: ["A conference across the border"](../../../docs/guide/en/interconnect.md#a-conference-across-the-border). No scenario of the script holds such a conference; the tests of `internal/ibcf` run it in one process.

---

## When an IBCF instance starts again

- The two dialogs of a call live in the process of the IBCF instance that took its INVITE. When that process stops, its calls stop with it, and their parties do not know: each keeps its dialog, and the TrGW keeps the context.
- TS 24.229 5.10.2.2 step 2 and 5.10.3.2 step 2 have an IBCF that controls the transport plane save the Contact, CSeq and Record-Route of a call, so that it can release the session by itself.
- Each instance keeps such a record of each of its own calls under its own name. One that starts again reads its own records, releases each TrGW context and sends a BYE in both dialogs. No instance searches through the calls of the whole network.
- A node whose configuration has a `store` section of kind `valkey` keeps those records in that server, and a recreated container reads them from there.
- The IBCFs of these two networks have no `store` section. Recreate IBCF-A during a call and nothing ends it: a BYE of a party gets 481 from the new process, and TrGW-A keeps the context of the call reserved.

> Guide: ["When an IBCF instance starts again"](../../../docs/guide/en/interconnect.md#when-an-ibcf-instance-starts-again)

---

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Method == "INVITE" && count(sip.Via) == 1` | On the link: the INVITEs of the IBCFs, each with its own Via alone. |
| `sip.Route contains "ibcf"` | Requests on their way to or from an IBCF. |
| `sip.r-uri contains "user=phone"` | Requests with a number in a SIP URI. |
| `sip.r-uri == "tel:+12025550151"` | The INVITE after the I-CSCF of network B turned it into a tel URI. |
| `sdp.connection_info.address == 172.28.2.11` | SDP with the address of TrGW-A on the link. |
| `rtp && ip.addr == 172.28.2.0/24` | RTP between the TrGWs, with the RTP heuristic on. |
| `dns.qry.name contains "5.1.0.5.5.5.2.0.2.1.e164.arpa"` | ENUM for network B's numbers. |
| `sip.Status-Code in {404, 405, 420, 488}` | The rejections of the scenarios. |

- The guide has five more, among them the short messages decoded, and the one that looks for anything of network A's inside in the other two captures. That one must find nothing.
- Each filter belongs to one of the three captures. The script checks the frames of each capture in the order of the scenarios, and the frames that must not be there at all.

> Guide: ["Wireshark display filters"](../../../docs/guide/en/interconnect.md#wireshark-display-filters)

---

## What this project simplifies

| Where | The main items |
| --- | --- |
| IBCF | Only the non-roaming II-NNI: a REGISTER does not cross (405), and there is no Path and no `iotl`. Of the requests outside a dialog, only INVITE and MESSAGE cross. No charging data and no session timer of its own. |
| The border | No transcoding: a codec that the peer does not take is removed. IPv4 only, and only the first audio stream goes through the TrGW. |
| Ix | The IBCF drives the TrGW over the HTTP control API of the media relay, not over H.248. |
| SMS | The SC finds a user of another network by ENUM, not by asking that network through an SMS-GMSC, and no delivery report crosses back. |

```sh
grep -rn "SIMPLIFIED:" internal/ibcf internal/b2bua internal/interconnect internal/sc internal/ipsmgw
```

- Why no transcoding: it needs codecs in the TrGW, and the nodes of this project neither encode nor decode AMR.

> Guide: ["What this project simplifies"](../../../docs/guide/en/interconnect.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Check it in the code

| What you saw | Where to look |
| --- | --- |
| The profile, and what the IBCF does to each message | `screen.go`, `trust.go`, `number.go`, `codec.go`, `leg.go`, `border.go` in `internal/interconnect` |
| The relay between the two dialogs of a call | `internal/b2bua` |
| The IBCF role: the two UAs, the call, the short messages, the TrGW | `internal/ibcf` |
| The request that leaves the network | `exitIBCF` in `internal/scscf/session.go` |
| The number in a SIP URI becoming a tel URI | `telURIOf` in `internal/icscf/session.go` |
| The short message across | `internal/sc/sc.go`, `deliver.go` and `message.go` in `internal/ipsmgw` |
| The two networks, their nodes and the agreements | `deploy/interconnect/` |

- The tests of `internal/ibcf` run two networks in one process: a fork beyond the border, a conference across it, and an instance that starts again and ends the calls it left.

> Guide: ["Where the code is"](../../../docs/guide/en/interconnect.md#where-the-code-is)

---

[layout: exercise]

## Exercises

1. Find the INVITE of the call in each of the three captures, and, in the capture of network A, the INVITE of UE A1 to the P-CSCF as well. How many Call-IDs does the call have in all, and why?
2. Open the INVITE on the link. How many Via entries does it carry, and where are the others?
3. Give IBCF-B the agreement `network-a-amr-wb-only.yaml` and call UE B1. Which status comes back, and which node sent it?
4. Search the capture of network B for an address of 172.28.0.0/24. What should the result be?
5. Send IBCF-A a request from an address that no profile lists. What comes back?

For 3, the commands are in "When the profiles do not match" in the guide. For 5, the script sends an INVITE and a MESSAGE from 172.28.2.99.

---

[layout: answers]

## Exercises: answers

1. Four, one for each dialog: UE A1 and the MMTel AS, the MMTel AS and IBCF-A, IBCF-A and IBCF-B, IBCF-B and UE B1. The MMTel AS and each IBCF are B2BUAs: each ends the request on the side it came from and sends a new one on the other. The captures put the last three side by side, and network B has no AS in this call.
2. One, IBCF-A's. The nodes of network A put theirs on the INVITE that ended at IBCF-A.
3. 488 (Not Acceptable Here), from IBCF-B. Network B takes AMR-WB alone, and UE A1 offers PCMU.
4. Nothing. No address of network A's inside crosses the border.
5. Nothing at all: no 100 (Trying), no error response. IBCF-A drops the request, so the sender learns nothing of network A.

---

[layout: exercise]

## Looking back

1. What does an IBCF do with a request that reaches it from its own network?
2. Why does one call across the border have four Call-IDs?
3. Which two reference points make up the II-NNI?
4. What decides whether a MESSAGE may cross the border?
5. Why does the caller's UE never see an address of the other network?
6. What becomes of the calls of an IBCF instance whose process stops?

---

[layout: answers]

## Looking back: answers

1. It ends it as the exit point of its network, and sends a new request in a dialog of its own towards the peer network.
2. One per dialog: the caller and the MMTel AS, the MMTel AS and IBCF-A, the link, and IBCF-B and the callee. Each B2BUA ends one dialog and starts another.
3. Ici between the IBCFs for the SIP, and Izi between the TrGWs for the media.
4. The agreement with the peer network. MESSAGE is an optional method, and `methods` in the profile says whether it is allowed.
5. The IBCF puts the media through its TrGW and writes the address of that TrGW in each network into the SDP of that side.
6. They stop with it, and the parties keep their dialogs. An instance that starts again with its records ends them with a BYE in both dialogs.

---

## Where to go from here

- This was the last session. The guides go on where the sessions stop, and each one ends with what this project simplifies and where its code is.

| Document | Place |
| --- | --- |
| What the project is, and how to run it | [README.md](../../../README.md) |
| Glossary | [docs/glossary.md](../../../docs/glossary.md) |
| Guides | [docs/guide/en/](../../../docs/guide/en/) |

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml --profile ue down -v --remove-orphans
```

- This is the "Clean up" of the guide, for the UEs of both networks and the nodes of both and of the link.

> Guide: ["Clean up"](../../../docs/guide/en/interconnect.md#clean-up)
