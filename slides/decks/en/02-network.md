---
title: The map of this IMS
footer: ims-practice study session 2. The map of the network
lang: en
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

## The application servers and the MRF

| Node | Address | What it is for |
| --- | --- | --- |
| IP-SM-GW | 172.28.0.70 | SMS over IP. It speaks SIP on one side and the language of the SMS network on the other. The SC stub is inside it. |
| MMTel AS | 172.28.0.75 | The supplementary services, and the settings that a UE makes itself over Ut. |
| MRFC | 172.28.0.85 | The SIP side of the MRF. An AS asks it for an announcement, a tone or a conference. |
| MRFP | 172.28.0.86 | The media side of the MRF. It plays and it mixes, and the MRFC drives it. |

- The CSCFs only route. What changes a call is an application server, and what makes media of its own is the MRF (TS 23.228 4.7).

> Guides: [Follow a short message through the IMS, "Why an application server, and what the SC is"](../../../docs/guide/en/sms.md#why-an-application-server-and-what-the-sc-is) and [Announcements, alerting tones and conferences with the MRF, "What an MRF is"](../../../docs/guide/en/mrf.md#what-an-mrf-is)

---

## The border, and what watches from outside

| Node | Address | What it is for |
| --- | --- | --- |
| IBCF | 172.28.0.90 | The exit point of a request that leaves the network and the entry point of one that comes in. It hides the topology. |
| TrGW | 172.28.0.91 | The media relay at the border. The IBCF controls it. |
| Lab | 172.28.0.60 | Draws each registration and call as a ladder, from copies that the nodes send it (HEP). Not part of the IMS. |
| capture | host network | tcpdump on the bridge. It writes the files that every session reads. |

- The IBCF and the TrGW come with the second network, which `deploy/interconnect/compose.yaml` adds on top of `compose.yaml`.
- The Lab and the capture are outside the IMS. No node waits for an answer from either: the copies go to the Lab over UDP, and tcpdump only listens.

> Guides: [Connect two IMS networks, "The two networks"](../../../docs/guide/en/interconnect.md#the-two-networks) and [Watch the IMS in the Lab, "These APIs are this project's own"](../../../docs/guide/en/lab.md#these-apis-are-this-projects-own)

---

## The reference points that carry SIP

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

```text
  I-CSCF  --- Cx ---> HSS <--- Cx ---  S-CSCF
  IP-SM-GW --- Sh ---> HSS <--- Sh --- MMTel AS
  P-CSCF  --- Rx ---> PCRF (inside the EPC stub)
```

| Reference point | Between | What is asked for |
| --- | --- | --- |
| Cx | a CSCF and the HSS | which S-CSCF serves the user, the authentication data, the user profile |
| Sh | an AS and the HSS | the settings of a user, its number, the password it uses on Ut |
| Rx | the P-CSCF and the PCRF | authorisation for the media of a call, and word when that media is lost |

- Five connections, all to port 3868, and each one opens with a CER and a CEA before its first question. The capture guide's table lists them.
- SIP carries a request on its way; Diameter asks a question and waits for the answer. That is the division between the two.

> Guide: [Capture signaling and open it in Wireshark, "What you see when the nodes start"](../../../docs/guide/en/capture.md#what-you-see-when-the-nodes-start)

---

## The reference points of HTTP, H.248 and the media

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
| Gm, Mw, Cx, and the CSCFs with the HSS | session 3, registration |
| ENUM, the media relay, both sides of one call | session 4, the call |
| ISC and Sh, the IP-SM-GW | session 5, SMS |
| Ut and the MMTel AS | session 6, the supplementary services |
| Rx, the EPC stub, IMS AKA | session 7, VoLTE |
| Mr', Mp and the MRF | the guide [Announcements, alerting tones and conferences with the MRF](../../../docs/guide/en/mrf.md) |
| Mx, Ici, Izi and the border | the guide [Connect two IMS networks](../../../docs/guide/en/interconnect.md) |
| HEP and the Lab | the guide [Watch the IMS in the Lab](../../../docs/guide/en/lab.md) |

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

- Next is registration: the first flow read in a capture. It crosses Gm, Mw and Cx, and at its end ISC and Sh as well.
- Before then, start the system once and watch the Diameter connections come up.

```sh
docker compose up -d --build
docker compose logs scscf | grep I-Open
```

> The guide of the next session is [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md). [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md) has the commands for starting the system and opening a capture.
