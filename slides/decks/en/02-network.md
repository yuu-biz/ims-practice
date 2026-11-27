---
title: The map of this IMS
footer: ims-practice study session 2. The map of the network
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: the phone, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: its first hop, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: entry of the network, at: [2, 1]}
  - {id: scscf, label: S-CSCF, role: "registers, starts services", at: [3, 1]}
  - {id: hss, label: HSS, role: subscriber data, at: [3, 0]}
  - {id: ipsmgw, label: IP-SM-GW, role: the AS for SMS, at: [4, 0]}
  - {id: sc, label: SC, role: service centre of SMS, at: [4, -1]}
  - {id: mmtel, label: MMTel AS, role: AS of the services, at: [4, 2]}
  - {id: mrfc, label: MRFC, role: the SIP side of the MRF, at: [5, 2]}
  - {id: mrfp, label: MRFP, role: the media side of the MRF, at: [5, 3]}
  - {id: agw, label: IMS-AGW, role: the media relay, at: [1, 2]}
  - {id: epc, label: EPC, role: the stub and the PCRF, at: [0, 2]}
  - {id: ibcf, label: IBCF, role: door of the network, at: [3, 2]}
  - {id: trgw, label: TrGW, role: media relay at the border, at: [2, 2]}
  - {id: peer-ibcf, label: IBCF, role: door of the peer, at: [3, 3]}
  - {id: peer-trgw, label: TrGW, role: media relay of the peer, at: [2, 3]}
groups:
  - {id: peer, label: the peer network, nodes: [peer-ibcf, peer-trgw]}
events:
  # The reference points that carry SIP
  - {id: gm, rel: Gm, from: ue, to: pcscf, proto: SIP}
  - {id: mw-p-i, rel: Mw, from: pcscf, to: icscf, proto: SIP}
  - {id: mw-i-s, rel: Mw, from: icscf, to: scscf, proto: SIP}
  - {id: isc-mmtel, rel: ISC, from: scscf, to: mmtel, proto: SIP}
  - {id: isc-ipsmgw, rel: ISC, from: scscf, to: ipsmgw, proto: SIP}
  - {id: mr, rel: Mr', from: mmtel, to: mrfc, proto: SIP}
  - {id: mx, rel: Mx, from: scscf, to: ibcf, proto: SIP}
  - {id: ici, rel: Ici, from: ibcf, to: peer-ibcf, proto: SIP}
  # The reference points that carry Diameter
  - {id: cx-i, rel: Cx, from: icscf, to: hss, proto: Diameter}
  - {id: cx-s, rel: Cx, from: scscf, to: hss, proto: Diameter}
  - {id: sh-ipsmgw, rel: Sh, from: ipsmgw, to: hss, proto: Diameter}
  - {id: sh-mmtel, rel: Sh, from: mmtel, to: hss, proto: Diameter}
  - {id: sgd, rel: SGd, from: ipsmgw, to: sc, proto: Diameter}
  - {id: s6c, rel: S6c, from: sc, to: hss, proto: Diameter}
  - {id: rx, rel: Rx, from: pcscf, to: epc, proto: Diameter}
  # HTTP, H.248 and the media
  - {id: ut, rel: Ut, from: ue, to: mmtel, proto: HTTP}
  - {id: mp, rel: Mp, from: mrfc, to: mrfp, proto: HTTP}
  - {id: iq, rel: Iq, from: pcscf, to: agw, proto: HTTP}
  - {id: izi, rel: Izi, from: trgw, to: peer-trgw, proto: RTP}
```

---

# The map of this IMS

ims-practice study session 2 (the nodes, the reference points between them, and which session opens each part)

---

[layout: summary]

## What you will know after this session

- Which nodes an IMS is made of, and what each one is for.
- Which interface joins two nodes, and which protocol runs on it.
- Where SIP ends and Diameter, HTTP, H.248 and RTP begin.
- Which session and which guide opens each part of the map.

> No capture is read in this session. It draws the map that the later sessions follow.
> The guides are in [docs/guide/en/](../../../docs/guide/en/), the abbreviations in the [glossary](../../../docs/glossary.md).

---

## What the IMS adds to a SIP proxy

```ims-scene
id: jobs
nodes: [ue, pcscf, icscf, scscf, hss, ipsmgw, sc, mmtel, mrfc, mrfp, agw, epc]
lead: The IMS split the work of a SIP proxy into nodes with one job each.
steps:
  - id: pcscf
    title: The first SIP hop from the phone
    focus: [pcscf]
    zoom: [ue, pcscf, icscf]
    text: The P-CSCF. Every request of the UE enters here.
  - id: icscf
    title: Find the node that serves this user
    focus: [icscf]
    zoom: [icscf, scscf, hss]
    text: The I-CSCF. It keeps no state of its own and asks the HSS.
  - id: scscf
    title: Keep the registration and call the services
    focus: [scscf]
    zoom: [icscf, scscf, hss]
    text: The S-CSCF. It authenticates, keeps the registration and evaluates the iFCs.
  - id: hss
    title: Hold the subscriber data
    focus: [hss]
    zoom: [scscf, hss, ipsmgw]
    text: The HSS. It holds the identities, the keys, the iFCs and the settings of the services.
  - id: as
    title: Change the call
    focus: [mmtel, ipsmgw]
    zoom: [scscf, mmtel, ipsmgw]
    checkpoint: true
    text: The application servers. The MMTel AS handles forwarding and barring, the IP-SM-GW short messages.
  - id: media
    title: Relay, play and mix the media
    focus: [agw, mrfc, mrfp]
    text: |
      The media relay (IMS-AGW) and the MRF.
      With a node for each job, each job can get as many instances as it needs.
```


- A SIP proxy can already set up a call between two phones. The IMS splits that work between nodes that each have one job, and gives the interface between two of them a name (TS 23.228).
- The names are the point: nodes from two vendors meet at a named interface, and so do the networks of two operators.
- The split is also why each job can run on as many nodes as it needs, and why a capture tells you which node did what.

| The job | The node |
| --- | --- |
| Be the first SIP hop of the phone | P-CSCF |
| Find the node that serves this user | I-CSCF |
| Hold the registration and invoke the services | S-CSCF |
| Keep the subscriber data | HSS |
| Change the call: forwarding, barring, short messages | application servers |
| Relay, play and mix the media | media relay, MRF |

> Guide: [Guides, "How the whole setup is built"](../../../docs/guide/en/README.md#how-the-whole-setup-is-built)

---

## The spine of the signaling

```ims-scene
id: backbone
nodes: [ue, pcscf, icscf, scscf, mmtel, ipsmgw]
lead: A request of the UE enters at the P-CSCF, crosses the CSCFs over Mw and reaches the ASs over ISC. All of it is SIP.
steps:
  - id: gm
    title: Gm, where the phone meets the network
    show: [gm]
    zoom: [ue, pcscf]
    text: A phone meets two interfaces, Gm and Ut, which is for its own settings.
  - id: mw
    title: Mw between the CSCFs
    show: [mw-p-i, mw-i-s]
    zoom: [pcscf, icscf, scscf]
    text: Every hop carries SIP on port 5060, so a message looks much the same at each hop.
  - id: isc
    title: ISC towards the ASs
    show: [isc-mmtel, isc-ipsmgw]
    focus: [scscf, mmtel, ipsmgw]
    text: |
      An AS is on the path, not at its end. The MMTel AS hands the request back to the S-CSCF.
      The IP-SM-GW answers instead: a short message ends there.
```


```text
  UE --- Gm --- P-CSCF --- Mw --- I-CSCF --- Mw --- S-CSCF --- ISC --- AS
```

- Every request of a UE enters at the P-CSCF. The hops after it carry SIP on port 5060, so one message looks much the same at each of them.
- A phone meets two of these interfaces: Gm, and Ut for its own settings. What is to the right of the P-CSCF is the operator's business.
- An AS sits in the path, not at its end: the MMTel AS sends the request back to the S-CSCF, which goes on with the next criterion. The IP-SM-GW answers it instead, because the short message ends there.

> Guide: [Follow a UE registering with the IMS, "The nodes"](../../../docs/guide/en/registration.md#the-nodes)

---

## The nodes at the edge

| Node | Address | What it is for |
| --- | --- | --- |
| UE | 172.28.0.100, 101, 103 | The phone. A softphone without a SIM, or a VoLTE UE on the EPC stub. |
| P-CSCF | 172.28.0.40 | The first SIP hop. It adds Path, asserts the identity, and keeps the IP association of each UE. |
| Media relay | 172.28.0.50 | Relays the RTP of every call. The P-CSCF controls it, in the IMS-AGW role. |
| EPC stub, with a PCRF | 172.28.0.80 | Gives a VoLTE UE its address and its P-CSCF, and authorises the media of each call. |

- The P-CSCF is the one address a UE has to know. A softphone has it in its configuration; a VoLTE UE is given it by the access network when it attaches.
- The P-CSCF stays in the path of everything a UE it serves sends and receives, registration included. Path, which it adds to the REGISTER, is what keeps it there for the calls that come in.

> Guide: [The VoLTE call flow, "The access network: the EPC stub"](../../../docs/guide/en/volte.md#the-access-network-the-epc-stub)

---

## The nodes of the home network

| Node | Address | What it is for |
| --- | --- | --- |
| I-CSCF | 172.28.0.20 | The entry point. It asks the HSS which S-CSCF serves the user, and keeps no state of its own. |
| S-CSCF | 172.28.0.30 | The registrar. It authenticates, holds the registration, and evaluates the iFCs. |
| HSS | 172.28.0.10 | The subscriber data: the identities, the keys, the iFCs and the service settings. |
| DNS | 172.28.0.2 | NAPTR, SRV and A to find a node (RFC 3263), ENUM to turn a number into a SIP URI (RFC 6116). |

- The S-CSCF and the HSS are nodes of the user's own network. The P-CSCF is the node that can be in a visited one, which is why the REGISTER carries P-Visited-Network-ID.
- The last number of each address names the node in a capture: the HSS is 10, the I-CSCF 20, the S-CSCF 30, the P-CSCF 40.

> Guide: [Capture signaling and open it in Wireshark, "What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start)

---

## The application servers, the SC and the MRF

| Node | Address | What it is for |
| --- | --- | --- |
| IP-SM-GW | 172.28.0.70 | SMS over IP. It speaks SIP on one side and the language of the SMS network on the other. |
| SC | 172.28.0.71 | The Service Centre of SMS. It holds each short message until it is delivered or its validity period ends, and delivers it. |
| MMTel AS | 172.28.0.75 | The supplementary services, and the settings that a UE makes itself over Ut. |
| MRFC | 172.28.0.85 | The SIP side of the MRF. An AS asks it for an announcement, a tone or a conference. |
| MRFP | 172.28.0.86 | The media side of the MRF. It plays and it mixes, and the MRFC drives it. |

- The CSCFs only route. What changes a call is an application server, and what makes media of its own is the MRF (TS 23.228 4.7).
- The SC is not part of the IMS but of the SMS network. The IP-SM-GW reaches it over Diameter.

> Guides: [Follow a short message through the IMS, "Why an application server, and what the SC is"](../../../docs/guide/en/sms.md#why-an-application-server-and-what-the-sc-is) and [Announcements, alerting tones and conferences with the MRF, "What an MRF is"](../../../docs/guide/en/mrf.md#what-an-mrf-is)

---

## The border, and what watches from outside

| Node | Address | What it is for |
| --- | --- | --- |
| IBCF | 172.28.0.90 | The exit point of a request that leaves the network and the entry point of one that comes in. It hides the topology. |
| TrGW | 172.28.0.91 | The media relay at the border. The IBCF controls it. |
| Lab | 172.28.0.60 | Draws each registration and call as a ladder, from copies that the nodes send it (HEP). Not part of the IMS. |
| OAM server | 172.28.0.65 | Operates every node through its maintenance API, and serves the OAM screen. Not part of the IMS, and it sends no signaling. |
| capture | host network | tcpdump on the bridge. It writes the files that every session reads. |

- The IBCF and the TrGW come with the second network, which `deploy/interconnect/compose.yaml` adds on top of `compose.yaml`. Session 10 reads the border in the captures of both networks and of the link between them.
- The Lab, the OAM server and the capture are outside the IMS. No call waits for an answer from any of them: the copies go to the Lab over UDP, and tcpdump only listens.

> Guides: [Connect two IMS networks, "The two networks"](../../../docs/guide/en/interconnect.md#the-two-networks) and [Watch the IMS in the Lab, "These APIs are this project's own"](../../../docs/guide/en/lab.md#these-apis-are-this-projects-own)

---

## The reference points that carry SIP

```ims-scene
id: sip-points
nodes: [ue, pcscf, icscf, scscf, mmtel, ipsmgw, mrfc, ibcf, peer-ibcf]
lead: All of them carry SIP in clear text. The addresses in a capture tell which reference point it is.
steps:
  - id: core
    title: Gm and Mw
    show: [gm, mw-p-i, mw-i-s]
    zoom: [ue, pcscf, icscf, scscf]
    text: The P-CSCF has Gm on the side of the UE and Mw on the side of the network. Here both are on the same address and port.
  - id: services
    title: ISC and Mr'
    show: [isc-mmtel, isc-ipsmgw, mr]
    focus: [scscf, mmtel, ipsmgw, mrfc]
    zoom: [scscf, mmtel, ipsmgw, mrfc]
    text: ISC carries the third-party REGISTER and the INVITEs that the iFCs send; Mr' the INVITEs that ask for announcements and conferences.
  - id: border
    title: Mx and Ici, out of the network
    show: [mx, ici]
    focus: [scscf, ibcf, peer-ibcf]
    text: Over Mx a request that leaves the network reaches the IBCF, and over Ici the IBCFs of two networks meet.
```


| Reference point | Between | Where you see it |
| --- | --- | --- |
| Gm | the UE and the P-CSCF | every REGISTER and every INVITE of a UE |
| Mw | the CSCFs | the same request, one hop further on |
| ISC | the S-CSCF and an AS | the third-party REGISTER, and each INVITE the iFCs send |
| Mr' | an AS and the MRFC | the INVITE for an announcement, a tone or a conference |
| Mx | a CSCF and the IBCF of its own network | the request that leaves the network |
| Ici | the IBCFs of two networks | the capture of the link between them |

- All of them carry SIP, in plain text. Wireshark cannot tell them apart by the protocol; the addresses tell you which one you are looking at.
- The P-CSCF has Gm towards the UEs and Mw towards the network. Here both are the same address and port, and the P-CSCF tells the two apart by the URI a request is addressed to.

> Guide: [Follow a UE registering with the IMS, "What this project simplifies"](../../../docs/guide/en/registration.md#what-this-project-simplifies)

---

## The reference points that carry Diameter

```ims-scene
id: diameter-points
nodes: [pcscf, epc, icscf, scscf, hss, ipsmgw, sc, mmtel]
lead: SIP carries a request onwards; Diameter asks and waits for the answer. Most Diameter lines meet at the HSS.
steps:
  - id: cx
    title: Cx between the CSCFs and the HSS
    show: [cx-i, cx-s]
    zoom: [icscf, scscf, hss]
    text: Which S-CSCF serves the user, the authentication data, the user profile.
  - id: sh
    title: Sh between the ASs and the HSS
    show: [sh-ipsmgw, sh-mmtel]
    focus: [ipsmgw, mmtel, hss]
    zoom: [hss, ipsmgw, mmtel, scscf]
    text: The settings of the user, its number, and the password for Ut are read and written.
  - id: sms
    title: SGd and S6c for SMS
    show: [sgd, s6c]
    focus: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd carries the short messages themselves; S6c which node serves the recipient, and which SC holds messages for it.
  - id: rx
    title: Rx between the P-CSCF and the PCRF
    show: [rx]
    focus: [pcscf, epc]
    zoom: [pcscf, epc, icscf]
    text: The authorization of the media of a call, and the notice when that media is lost. There are seven connections, all on port 3868.
```


```text
  I-CSCF   --- Cx  ---> HSS <--- Cx  --- S-CSCF
  IP-SM-GW --- Sh  ---> HSS <--- Sh  --- MMTel AS
  IP-SM-GW --- SGd ---> SC  --- S6c ---> HSS
  P-CSCF   --- Rx  ---> PCRF (inside the EPC stub)
```

| Reference point | Between | What is asked for |
| --- | --- | --- |
| Cx | a CSCF and the HSS | which S-CSCF serves the user, the authentication data, the user profile |
| Sh | an AS and the HSS | the settings of a user, its number, the password it uses on Ut |
| SGd | the IP-SM-GW and the SC | the short message itself, from a UE and to one |
| S6c | the SC and the HSS | which node serves the recipient, and which SCs hold short messages for a user who can take them again |
| Rx | the P-CSCF and the PCRF | authorisation for the media of a call, and word when that media is lost |

- Seven connections, all to port 3868, and each one opens with a CER and a CEA before its first question. The capture guide's table lists them.
- SIP carries a request on its way; Diameter asks a question and waits for the answer. That is the division between the two.

> Guide: [Capture signaling and open it in Wireshark, "What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start)

---

## The reference points of HTTP, H.248 and the media

```ims-scene
id: other-points
nodes: [ue, pcscf, agw, mmtel, mrfc, mrfp, trgw, peer-trgw]
lead: The media does not follow the path of the signaling. The SDP of both sides decides where RTP goes.
steps:
  - id: ut
    title: Ut, where users read and write their settings
    show: [ut]
    text: HTTP carrying XCAP (TS 24.623).
  - id: mp
    title: Mp, where the MRFC drives the MRFP
    show: [mp]
    focus: [mrfc, mrfp]
    zoom: [mmtel, mrfc, mrfp]
    text: H.248 in the standard. Here a simple HTTP API with one operation per command.
  - id: iq
    title: Iq, where the P-CSCF drives the media relay
    show: [iq]
    focus: [pcscf, agw]
    zoom: [ue, pcscf, agw]
    checkpoint: true
    text: The same HTTP API by default. With `deploy/h248/h248.yaml` it is H.248.
  - id: izi
    title: Izi, the media across the border
    show: [izi]
    focus: [trgw, peer-trgw]
    text: RTP passes between the TrGWs of the two networks.
```


| Reference point | Between | The protocol |
| --- | --- | --- |
| Ut | the UE and the MMTel AS | HTTP with XCAP: the user reads and writes its own settings (TS 24.623) |
| Mp | the MRFC and the MRFP | H.248 in the standard (TS 29.333); here a small HTTP API, one operation per command |
| Iq | the P-CSCF and the media relay | the same HTTP API by default, H.248 with `deploy/h248/h248.yaml` (TS 29.334) |
| Izi | the TrGWs of two networks | the media that crosses the border (TS 29.165 5.1) |

- The media does not follow the path of the signaling. The SDP of each side says where its RTP goes, and the only nodes in that path are the relays and the MRFP.
- A VoLTE call adds RTCP, and the P-CSCF asks for a bearer for that media over Rx before the phone rings.

> Guide: [Guides, "The media relay controlled over H.248"](../../../docs/guide/en/README.md#the-media-relay-controlled-over-h248)

---

## What this map leaves out

| Not here | What it would be for |
| --- | --- |
| The telephone network | A number with no ENUM record cannot be reached. The S-CSCF answers 404 where a larger network would send the call out towards a BGCF. |
| Gx | The PCRF and the gateway of the bearer share their state inside the EPC stub, so there is nothing between them to see. |
| Mr | The S-CSCF asking the MRFC itself. Here every request for media comes from the AS, over Mr'. |
| Cr | The AS fetching prompts and media control commands. The MRFP plays the files it already has. |
| GBA | A key for Ut derived from AKA. The UE uses a password that the AS reads over Sh. |

- Each guide ends with "What this project simplifies", which says what was left out of that flow and why.

---

## Which session opens which part of the map

| Part of the map | Where to read it |
| --- | --- |
| Gm, Mw, Cx, and the CSCFs with the HSS | session 4, registration |
| ENUM, the media relay, both sides of one call | session 5, the call |
| ISC, Sh, SGd and S6c, the IP-SM-GW and the SC | session 6, SMS |
| Ut and the MMTel AS | session 7, the supplementary services |
| Rx, the EPC stub, IMS AKA | session 9, VoLTE |
| Mr', Mp and the MRF | session 8, the MRF |
| Mx, Ici, Izi and the border | session 10, two networks joined at the border |
| HEP and the Lab | session 3, captures and the Lab |

> Guide: [Guides, "The order to read them in"](../../../docs/guide/en/README.md#the-order-to-read-them-in)

---

## Where the map is in the repository

| What | Where |
| --- | --- |
| The nodes and their addresses | `compose.yaml` |
| What each node is | `roles` in `deploy/compose/<node>.yaml` |
| Which Diameter peers a node opens | `peers` in the same file |
| Which AS the S-CSCF sends a request to | the filter criteria in `deploy/compose/hss.yaml` |
| The names, the SRV records and the ENUM records | `deploy/compose/dns/` |

- One image is every node: its file says which roles it takes, so the map is configuration and not code.
- The table of the capture guide has the address of every service, and the table under it every Diameter connection. Both are checked against `compose.yaml`, so neither can go stale.

> Guide: [Guides, "What the Compose project starts"](../../../docs/guide/en/README.md#what-the-compose-project-starts)

---

[layout: exercise]

## Looking back

1. Which nodes does a REGISTER pass, and on which reference points?
2. Which protocol runs on Cx, on Ut and on Mp in this project?
3. Why can the I-CSCF not say by itself which S-CSCF serves a user?
4. Someone calls a number that no ENUM record names. Who answers, and what?
5. Which address does a UE have to know, and how does it learn it?

---

[layout: answers]

## Looking back: answers

1. The P-CSCF over Gm, then the I-CSCF and the S-CSCF over Mw. On the way the I-CSCF and the S-CSCF ask the HSS over Cx.
2. Cx is Diameter and Ut is HTTP with XCAP. Mp is H.248 in the standard, and a small HTTP API here.
3. It keeps no state. The HSS is where the S-CSCF of a user is written down.
4. The S-CSCF, with 404. A larger network would send the call out towards the telephone network.
5. The address of its P-CSCF. A softphone has it in its configuration, a VoLTE UE is given it by the access network when it attaches.

---

## Next session

- Next are the two ways of watching this map at work: a capture in Wireshark, and the Lab.
- Registration follows them. It crosses Gm, Mw and Cx, and at its end ISC and Sh as well.
- Before then, start the system once and watch the Diameter connections come up.

```sh
docker compose up -d --build
docker compose logs scscf | grep I-Open
```

> The guides of the next session are [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md), which has the commands above, and [Watch the IMS in the Lab](../../../docs/guide/en/lab.md).
