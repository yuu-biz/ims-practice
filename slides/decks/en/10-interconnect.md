---
title: Two IMS networks joined at the border
footer: ims-practice study session 10. Two networks
lang: en
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

- TS 29.165 calls the interface between two IMS networks the II-NNI. The node that stands there is the IBCF, with a user agent on each side: the internal one listens in its own network, the external one on the link.
- A request that reaches the internal side leaves the network, and the IBCF is its **exit point** (TS 24.229 5.10.2). One that reaches the external side enters, and the IBCF is the **entry point** (5.10.3).
- The IBCF ends the request in a dialog with the side it came from, and sends a new request in a dialog of its own with the other side (TS 24.229 5.10.5, TS 29.165 5.2.1). Each dialog has its own Call-ID, tags, CSeq, Via and Contact.
- Nothing that routes a message inside one network belongs to the dialog with the other network. That is how the B2BUA hides the topology of its network.
- The TrGW is the media relay of the `agw` role again. Its `core` termination faces its own network, and its `access` termination the TrGW of the other network.

> Guide: ["What the IBCF and the TrGW do"](../../../docs/guide/en/interconnect.md#what-the-ibcf-and-the-trgw-do)

---

## The four reference points of the border

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
- **Statistics > Flow Graph** on the capture of the link draws the ladder between the two IBCFs.

> Guide: ["What each capture shows"](../../../docs/guide/en/interconnect.md#what-each-capture-shows)

---

## A call from network A to network B

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

## What the border changes in a request

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

> Guides: ["What the IBCF and the TrGW do"](../../../docs/guide/en/interconnect.md#what-the-ibcf-and-the-trgw-do) and ["The trust domain"](../../../docs/guide/en/interconnect.md#the-trust-domain)

---

## Three dialogs, three Call-IDs

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
- **Call-ID.** IBCF-A's own. Compare it with the INVITE to IBCF-A and with the one from IBCF-B: the call has a different Call-ID in each of the three captures.
- **Max-Forwards.** IBCF-A took one hop off, as a proxy would (RFC 3261 16.6 step 3), so a call that two networks send round in a loop still ends.

> Guide: ["Three dialogs in the captures"](../../../docs/guide/en/interconnect.md#three-dialogs-in-the-captures)

---

## Topology hiding: what must not be in a capture

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

## A conference across the border

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

1. Find the INVITE of the call in each of the three captures. How many Call-IDs does one call have, and why?
2. Open the INVITE on the link. How many Via entries does it carry, and where are the others?
3. Give IBCF-B the agreement `network-a-amr-wb-only.yaml` and call UE B1. Which status comes back, and which node sent it?
4. Search the capture of network B for an address of 172.28.0.0/24. What should the result be?
5. Send IBCF-A a request from an address that no profile lists. What comes back?

For 3, the commands are in "When the profiles do not match" in the guide. For 5, the script sends an INVITE and a MESSAGE from 172.28.2.99.

---

[layout: answers]

## Exercises: answers

1. Three, one for each dialog. Each IBCF is a B2BUA: it ends the request on the side it came from and sends a new one on the other.
2. One, IBCF-A's. The nodes of network A put theirs on the INVITE that ended at IBCF-A.
3. 488 (Not Acceptable Here), from IBCF-B. Network B takes AMR-WB alone, and UE A1 offers PCMU.
4. Nothing. No address of network A's inside crosses the border.
5. Nothing at all: no 100 (Trying), no error response. IBCF-A drops the request, so the sender learns nothing of network A.

---

[layout: exercise]

## Looking back

1. What does an IBCF do with a request that reaches it from its own network?
2. Why does one call across the border have three Call-IDs?
3. Which two reference points make up the II-NNI?
4. What decides whether a MESSAGE may cross the border?
5. Why does the caller's UE never see an address of the other network?
6. What becomes of the calls of an IBCF instance whose process stops?

---

[layout: answers]

## Looking back: answers

1. It ends it as the exit point of its network, and sends a new request in a dialog of its own towards the peer network.
2. One per dialog: the caller's side, the link, and the callee's side. Each IBCF ends one dialog and starts another.
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
