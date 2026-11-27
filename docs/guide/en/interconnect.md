# Connect two IMS networks

[日本語](../ja/interconnect.md)

This guide joins two IMS networks of two operators and follows a call, a call back and short messages in both directions between them. On the border of each network sit an IBCF, which handles the SIP, and a TrGW, which relays the media. The guide shows what each of them changes in the messages, what each network sees of the other in its capture, and what happens when the agreements of the two operators do not match.

The guide follows these editions: TS 24.229 V20.1.0, TS 29.165 V20.0.0 (the II-NNI), TS 23.204 V19.1.0 and TS 24.341 V19.0.0 (SMS over IP), TS 23.040 V20.0.0, TS 32.409 V19.0.0 (performance measurements), TS 24.147 V19.0.0 (conferences), RFC 3261, RFC 3312 and RFC 3966. Clause numbers below refer to those editions.

## Before you start

- You have read [Follow a voice call through the IMS](call.md), [Follow a short message through the IMS](sms.md) and [Supplementary services with the MMTel AS](mmtel.md). This guide reuses what they show, and every INVITE of network A still goes through its MMTel AS.
- The abbreviations are explained in the [glossary](../../glossary.md).

## The two networks

Network A is the network of `compose.yaml`, the home network of the other guides. Network B, the peer network, is a second IMS of another operator: the same image with other settings, another domain, other numbers and other subscribers. `deploy/interconnect/compose.yaml` adds it on top of `compose.yaml`, which stays as it is. Every service of network B ends in `-b`, so `scscf-b` is its S-CSCF and `scscf` network A's, and the node files of the two networks are in `deploy/interconnect/a` and `deploy/interconnect/b`.

```text
       network A (ims0)                link (nni0)                network B (ims1)
     172.28.0.0/24                   172.28.2.0/24               172.28.1.0/24

 UE A1  P-CSCF  S-CSCF            .10         .20               I-CSCF  S-CSCF  P-CSCF  UE B1
   |      |       |    IBCF-A  ---------- Ici ----------  IBCF-B    |       |       |      |
   |      |       |-----(.90)                                (.90)--|       |       |      |
   |     AGW            TrGW-A ---------- Izi ----------  TrGW-B                          |
   |    (.50)-----------(.91) .11                    .21  (.91)---------------------------|
```

Only the IBCFs and the TrGWs are on the link between the networks, `nni0`. A node inside network A cannot reach a node inside network B at all; everything goes through the border. Each bridge has a capture of its own: `captures/ims-*.pcap` for network A, `captures/b/ims-*.pcap` for network B and `captures/nni/ims-*.pcap` for the link.

| Node | Network A | Network B |
| --- | --- | --- |
| Domain | `ims.mnc001.mcc001.3gppnetwork.org` (test PLMN 001/01) | `ims.mnc002.mcc001.3gppnetwork.org` (test PLMN 001/02) |
| UE 1 | UE A1, 172.28.0.100, `+1-202-555-0101` | UE B1, 172.28.1.100, `+1-202-555-0151` |
| UE 2 | UE A2, 172.28.0.101, `+1-202-555-0102` | UE B2, 172.28.1.101, `+1-202-555-0152` |
| P-CSCF | 172.28.0.40, with the media relay at 172.28.0.50 | 172.28.1.40, no media relay |
| I-CSCF | 172.28.0.20 | 172.28.1.20 |
| S-CSCF | 172.28.0.30 | 172.28.1.30 |
| MMTel AS | 172.28.0.75 | none |
| IP-SM-GW with the SC stub | 172.28.0.70, SC `+12025550100` | 172.28.1.70, SC `+12025550150` |
| HSS | 172.28.0.10 | 172.28.1.10 |
| DNS | 172.28.0.2 | 172.28.1.2 |
| IBCF | IBCF-A: 172.28.0.90 inside, 172.28.2.10 on the link | IBCF-B: 172.28.1.90 inside, 172.28.2.20 on the link |
| TrGW | TrGW-A: 172.28.0.91 inside, 172.28.2.11 on the link | TrGW-B: 172.28.1.91 inside, 172.28.2.21 on the link |

Network B is kept as small as the scenarios allow. It has no MMTel AS, no EPC with its PCRF, no media relay at its P-CSCF and no Lab. All numbers are in the 555-0100 to 555-0199 range that the North American Numbering Plan keeps for fiction, and all keys are test values.

## What the IBCF and the TrGW do

TS 29.165 calls the interface between two IMS networks the II-NNI. It has two reference points (TS 29.165 5.1): Ici between the IBCFs, for the SIP, and Izi between the TrGWs, for the media. Inside its own network the IBCF talks to the CSCFs over Mx, and the reference point towards its TrGW is Ix (TS 29.165 3.2).

An IBCF has two sides. In this project it is a **B2BUA** (TS 24.229 5.10.5, TS 29.165 5.2.1) with a user agent on each side: the internal one listens on the address in its own network, and the external one on the address on the link. A request that reaches the internal side leaves the network, and the IBCF is its **exit point** (TS 24.229 5.10.2). A request that reaches the external side enters the network, and the IBCF is its **entry point** (5.10.3). The IBCF ends the request in a dialog with the side it came from, and sends a new request in a dialog of its own with the other side. Each dialog has its own Call-ID, tags, CSeq, Via and Contact. What the IBCF copies from one dialog to the other is decided by the agreement with the other network, the **interconnect profile** of this project:

- it removes the header fields that must not leave the trust domain (TS 24.229 4.4),
- it screens the methods, the option tags and the header fields (5.10.6),
- it writes the numbers in the form that the agreement asks for (TS 29.165 8.1),
- it takes the codecs that the other network does not take out of the SDP (TS 29.165 7.1),
- and it puts the media through its TrGW, whose address it writes into the SDP (TS 24.229 6.7.1.4.1).

Nothing that routes a message inside one network belongs to the dialog with the other network: the Via, Route, Record-Route and Contact of the nodes of network A, and its Call-IDs and tags, never reach network B. That is how the B2BUA hides the topology of its network. TS 24.229 5.10.4 describes another way, for an IBCF that passes the requests on as a proxy: it seals those entries into encrypted tokens. A B2BUA has nothing to seal. The one Contact that does cross as it came is that of a GRUU or a conference focus (see "A conference across the border").

The TrGW is the media relay of the `agw` role again, on both networks. Its `core` termination faces its own network, and its `access` termination the TrGW of the other network. TS 29.165 3.2 names the reference point between the IBCF and the TrGW Ix, whose protocol is H.248 (TS 29.238). In this project the IBCF drives the relay over the same HTTP control API, with the same client, that the P-CSCF uses for its media relay. The operations carry the names of the H.248 commands: Add reserves a context with a termination on each side, Modify tells a termination where its side takes the media, and Subtract releases the context (the `TrGW` interface in `internal/ibcf/trgw.go`). The relay writes one address for both terminations, so the IBCF shows the TrGW's address in each network in the SDP of that side, with the port that the relay chose.

The two dialogs of a call live in the IBCF process that took the INVITE. Each IBCF instance writes its own name in the Contact of its dialogs, so the later requests of the call come back to it, while a new call may go to any instance of the network. So that several instances can share the work, the IBCF keeps nothing in a table of all calls: each instance keeps a record of each of its calls under its own name.

## Run it

`scripts/e2e-interconnect.sh` does everything in this guide and checks it in the three captures. To run the steps by hand:

1. Start both networks:

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml up -d --build
   ```

   The IBCFs log `"msg":"IBCF ready"` with their two URIs and the name of their peer network.

2. In one terminal, let UE B1 wait for a call. The UE simulator takes the settings of network B as flags:

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 answer \
     -domain ims.mnc002.mcc001.3gppnetwork.org \
     -impi 001020000000001@ims.mnc002.mcc001.3gppnetwork.org \
     -impu sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org \
     -password test-password-b1 \
     -pcscf 172.28.1.40:5060 -pcscf-uri sip:pcscf.ims.mnc002.mcc001.3gppnetwork.org:5060
   ```

3. In another terminal, call it from UE A1 by its number:

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue call tel:+12025550151
   ```

   UE A1 logs `183`, `180` and `200`, then `"msg":"call answered"` with the media relay of network A, `172.28.0.50`, as the far end. UE B1 logs `"msg":"media negotiated"` with TrGW-B, `172.28.1.91`, as the far end. Neither UE learns anything of the other network.

4. To call back, let UE A2 wait (`run --rm ue2 answer -impi 001010000000002@... -impu sip:+12025550102@... -password test-password-2`) and call `tel:+12025550102` from UE B1 with the flags of step 2.

5. To send a short message, keep UE B1 registered (`run --rm ue-b1 register` with the flags of step 2) and send one from UE A1:

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue sms -status-report +12025550151 "Hello UE-B1"
   ```

6. To send one back, stop UE B1's registration of step 5, keep UE A1 registered (`run --rm ue register`) and send from UE B1 with the flags of step 2 and `-sc +12025550150`, the number of the SC of network B:

   ```sh
   docker compose -f compose.yaml -f deploy/interconnect/compose.yaml run --rm ue-b1 sms <flags of step 2> -sc +12025550150 -status-report +12025550101 "Hello UE-A1"
   ```

   UE B1 sends from a registration of its own. With a second one still there, the status report could go to that one instead.

## A call from network A to network B

```text
UE A1  P-CSCF  S-CSCF  MMTel  DNS-A  IBCF-A ║ IBCF-B  I-CSCF  HSS-B  S-CSCF  P-CSCF  UE B1
  |INVITE |      |       |      |      |    ║   |       |       |       |       |      |
  |------>|----->|------>| (1)  |      |    ║   |       |       |       |       |      |
  |       |      |<------|      |      |    ║   |       |       |       |       |      |
  |       |      | ENUM (2)     |      |    ║   |       |       |       |       |      |
  |       |      |------------->|      |    ║   |       |       |       |       |      |
  |       |      |   INVITE (3) |      |    ║   |       |       |       |       |      |
  |       |      |-------------------->|    ║   |       |       |       |       |      |
  |       |      |       |      |      |  INVITE (4)    |       |       |       |      |
  |       |      |       |      |      |=============>|(5)     |       |       |      |
  |       |      |       |      |      |    ║   |------>| LIR (6)       |       |      |
  |       |      |       |      |      |    ║   |       |<----->|       |       |      |
  |       |      |       |      |      |    ║   |       |-------------->|------>|----->|
  |       |      |       |      |      |    ║   |       |       |  183  |       | (7)  |
  |       |      |       |      |      |    ║   |<------|<--------------|<------|<-----|
  |<------|<-----|<------|<-----------------|<=============|      |       |       |      |
  | PRACK, 180, PRACK, 200, ACK (8): in three dialogs, A1-IBCF-A, IBCF-A-IBCF-B, IBCF-B-B1 |
  |<=================== RTP: AGW, TrGW-A, TrGW-B (9) ===================================>|
```

The double line is the link between the networks, `nni0`. The messages over it belong to the dialog between IBCF-A and IBCF-B, which neither UE sees.

### 1. UE A1 calls a number

UE A1 sends the INVITE to `tel:+12025550151` as in any call. The P-CSCF passes it to the S-CSCF, which sends it to the MMTel AS by the filter criteria of the originating case, and the AS sends it back (see the MMTel guide). Nothing so far knows that the number is in another network.

### 2. ENUM finds network B

Once no AS is left, the S-CSCF translates the number with ENUM (TS 24.229 5.4.3.2 step 10). The DNS of network A holds a zone for network B's numbers, `5.1.0.5.5.5.2.0.2.1.e164.arpa`, and answers `1.5.1.0.5.5.5.2.0.2.1.e164.arpa` with:

```text
NAPTR 10 100 "u" "E2U+sip" "!^.*$!sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone!" .
```

The result is the global number in a SIP URI of network B's domain with `user=phone`, the form that every non-roaming II-NNI supports (TS 29.165 8.1). The record names the number, not the private identity of the subscriber, which network B keeps to itself.

### 3. The S-CSCF sends the call to IBCF-A

The domain is not network A's. TS 24.229 5.4.3.2 step 11 has the S-CSCF send a request whose destination needs the interconnect functions through an IBCF of its own network, and step 12 puts the IBCF on top of Route when the network hides its topology. The `ibcf` setting of the S-CSCF names it (`deploy/interconnect/a/scscf.yaml`):

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Route: <sip:ibcf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
Record-Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
```

The S-CSCF still record-routes (step 13), so the requests of the dialog come back through it. Without an `ibcf` setting, the S-CSCF would send the request to network B's domain itself, which it cannot reach.

### 4. IBCF-A: the exit point

IBCF-A takes the request on its internal side, as the UAS of a dialog with network A, and does the steps of TS 24.229 5.10.2.2:

- It removes its own URI from the top of Route (step 1A).
- It finds the peer network from the host of the Request-URI, and with it the profile `profiles/network-b.yaml`.
- It applies the profile (the sections below) to a copy of the request: the trust domain, the header rules, the numbers and the codecs. P-Charging-Function-Addresses goes (step 9).
- It reserves a context in TrGW-A, tells the `core` termination where network A takes the media (the media relay, 172.28.0.50), and writes the address of the other termination, 172.28.2.11, into the SDP: in `c=` and `o=` (TS 24.229 6.7.1.4.1).
- It sends a new INVITE from its external side, as the UAC of a dialog with network B: a new Call-ID and From tag, its own Via and Contact, CSeq 1, and IBCF-B, the entry point that its profile names, in Route (5.10.2.2 NOTE 3). The new INVITE carries the header fields of the copy that do not belong to one dialog, the body, and the Supported and Session-Expires of the caller.

It does not record-route (step 2B): the later requests of the call reach it as the far end of each dialog. The one exception is a Contact that it passes on as it came (see "A conference across the border"). An INVITE with SDP is longer than 1300 bytes, so it goes over TCP (RFC 3261 18.1.1, which TS 24.229 4.2A points to).

### 5. IBCF-B: the entry point

IBCF-B takes the request on its external side. It tells the peer by the address the request came from, 172.28.2.10, which its profile for network A lists. It applies the profile `profiles/network-a.yaml`, removes its own URI from Route (TS 24.229 5.10.3.2 step 1C), and, with no Route left, sends a new INVITE in a dialog of its own to its preconfigured I-CSCF (step 4). It puts the media through TrGW-B in the same way: the SDP that network B gets points at 172.28.1.91.

### 6. The I-CSCF of network B finds the callee

The Request-URI is a SIP URI with a global number and `user=phone`. The HSS knows UE B1 by its tel URI, not by that SIP URI, so the I-CSCF replaces the Request-URI with `tel:+12025550151` (TS 24.229 5.3.2.1 item 1 b) before it asks the HSS with an LIR. The S-CSCF of network B gets the tel URI, and the call reaches UE B1 through the P-CSCF of network B as any call does.

### 7. The answer comes back

UE B1 answers with a reliable 183 that has the SDP answer. Each IBCF passes it on as a 183 of its own in its other dialog, with the header fields that the profile lets across: IBCF-B gives TrGW-B the address of UE B1 and writes 172.28.2.21 into the SDP; IBCF-A gives TrGW-A the address 172.28.2.21 and writes 172.28.0.91 into the SDP for network A.

### 8. The rest of the call

Each dialog has its own PRACKs, ACK and BYE. An IBCF acknowledges a reliable response of the callee's side once the caller's side has acknowledged its copy, so the PRACKs still go from end to end (RFC 3262). It copies a provisional response reliably only when the response came reliably and the caller's INVITE offered 100rel. One that came without 100rel is copied without it too: its SDP is no answer, and a reliable copy would have the caller take it for the answer and leave the answer in the 2xx unread (RFC 3261 13.2.1). RFC 3262 3 leaves the choice to the UAS, which the IBCF is in the caller's dialog. A caller that offered no 100rel gets every copy unreliably and takes its answer from the 2xx alone, so when the callee's answer went in a reliable 183 and its 200 carries none, the 200 that the IBCF sends that caller carries the answer of the 183 (`TestAnUnreliable183IsCopiedUnreliably` and `TestThe2xxCarriesTheAnswerThatTheCallerGotUnreliably` in `internal/b2bua`). A PRACK, an UPDATE or a re-INVITE with SDP goes through the TrGW like the INVITE (see "When the TrGW does not take an SDP"). When one side hangs up, the IBCF answers the BYE in that dialog and sends a BYE of its own in the other, with the same Reason (RFC 3326). It does the same when one dialog fails, for instance when the far end answers a request with 481 (RFC 3261 12.2.1.2). Then it releases the TrGW context.

The INVITE that an IBCF sends may fork beyond it: several UEs of one user may answer it, each with an early dialog of its own (RFC 3261 13.2.2.4). The IBCF is one UAS to the caller's side, and a UAS gives all its responses to a request one To tag (8.2.6.2), so the caller has one early dialog with the IBCF however many the other side has. The caller takes the first session description of that dialog as the answer and ignores any later one (13.2.1). The IBCF therefore passes on the session description of the first dialog that sent one, and the responses of the other dialogs without theirs, which it keeps for each dialog. All this lasts while that first dialog does: once a 199 has ended it (RFC 6228), the next session description that answers the caller's offer goes to the caller as it comes, since with preconditions its UAS alerts its user only once the caller has confirmed its resources, which needs that answer (RFC 3312). The caller never sees that 199, as its one early dialog with the IBCF goes on, so a caller that already has an answer to its offer keeps it and ignores this next session description (13.2.1); the IBCF counts it as the one the caller has, and sends no offer of its own for that dialog when it answers. When a dialog answers with its 200, the IBCF points the TrGW at that dialog, from the SDP of the 200 or the last one that dialog sent. When that is not the dialog whose session description the caller got, the IBCF also offers the caller the session of the dialog that answered, once the caller has acknowledged the 200: in an UPDATE when the caller's INVITE listed UPDATE in its Allow (RFC 3311), and otherwise in a re-INVITE (RFC 3261 14.1). Without that offer the caller's media would stay on the codec that the first dialog chose. The offer keeps the `o=` line of the session description that the caller got, with the version one up (RFC 3264 8), and its SDP goes through the TrGW like any other. After an INVITE without an offer, the 200 of that dialog carries the offer of its UAS, and the caller's answer then goes to that UAS in the ACK (13.2.2.4). An offer that crosses one of the caller gets a 491 (Request Pending) and goes again a little later, three times in all (14.1). The tests of `internal/ibcf` show both cases (`TestForkBeyondTheBorder`); no scenario of the script forks.

### 9. The media

The RTP goes from UE A1 to the media relay of network A, to TrGW-A, over the link to TrGW-B, and to UE B1. The script checks that UE B1 received the same PCMU payloads that UE A1 sent.

The call back from UE B1 to UE A2 is the same with the roles of the networks swapped: ENUM in network B gives `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org;user=phone`, the S-CSCF of network B sends it to IBCF-B, and the I-CSCF of network A turns it into `tel:+12025550102`. In network A the call then visits the MMTel AS of UE A2, in the terminating case.

## What each capture shows

| Capture | What is there | What must not be there |
| --- | --- | --- |
| `captures/ims-*.pcap` (network A) | ENUM for network B's number, the INVITE from the S-CSCF to IBCF-A with the IBCF in Route, and the messages of network B as IBCF-A passes them on | Any address of 172.28.1.0/24, and any host name of a node of network B |
| `captures/nni/ims-*.pcap` (the link) | Only the two IBCFs and the two TrGWs: one Via and a Contact with the external name of an IBCF, Call-IDs of the IBCFs' own, SDP with the addresses of the TrGWs on the link, RTP between the TrGWs | Any address of either network's inside, any host name of their nodes, any identity made from an IMSI of network A, and any Record-Route |
| `captures/b/ims-*.pcap` (network B) | The INVITE from IBCF-B to the I-CSCF and on, the tel URI, the SDP with TrGW-B | Any address of 172.28.0.0/24, any host name of a node of network A, and any identity made from an IMSI of network A |

These checks show the topology hiding: the capture of the peer network has no internal address of the home network. The script looks for the addresses in the IP headers and anywhere in the frames, and for the names `pcscf`, `scscf`, `icscf`, `mmtel`, `agw`, `ipsmgw`, `hss`, `ibcf`, `trgw` and the others in front of the other network's domain. It also looks in the SIP of network B's capture and of the link for a user part that begins with network A's IMSIs, and finds none.

Some names of the home network do show in the peer network, and should: the domain itself, in the identities of the users, such as `sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org` in From and P-Asserted-Identity. The identities that cross are those of the users' numbers, which the peer network needs in order to call back. A number and a domain tell nothing of the nodes inside, and nothing of the IMSI.

## Three dialogs in the captures

Open the capture of the link and look at the INVITE from 172.28.2.10. Its header fields, shortened:

```text
INVITE sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone SIP/2.0
Via: SIP/2.0/TCP ibcf.nni.mnc001.example.net:5060;branch=z9hG4bKW6NI6...;rport
Max-Forwards: 65
Route: <sip:ibcf.nni.mnc002.example.net:5060;lr>
From: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;tag=5MWO4Z6R4C
To: <tel:+12025550151>
Call-ID: FMZQJ3BZL2YI4ILP6CEIPAUT5Z
CSeq: 1 INVITE
Contact: <sip:ibcf.nni.mnc001.example.net:5060>
Supported: 100rel, timer, 199, precondition, replaces
```

- **Via.** One entry, IBCF-A's. Network A's nodes put their Via entries on the INVITE that reached IBCF-A, and that INVITE ended there.
- **Record-Route.** None. IBCF-A is the far end of this dialog, so the requests of network B reach it by its Contact.
- **Contact.** IBCF-A's external name. The PRACKs, the ACK and the BYE from network B go there.
- **Call-ID, From tag, CSeq.** IBCF-A's own. Compare the Call-ID with the INVITE from the S-CSCF to IBCF-A in the capture of network A, and with the INVITE from IBCF-B to the I-CSCF in the capture of network B: the call has a different Call-ID in each of the three captures.
- **Max-Forwards.** IBCF-A took one hop away, as a proxy would (RFC 3261 16.6 step 3), so that a call that two networks send round in a loop still ends.
- **Supported.** UE A1's, as the profile left it, so that network B uses no extension that UE A1 does not take. `replaces` is among them: each IBCF names the dialog of its other side in a Replaces that it passes on (see "A conference across the border").

The INVITE that UE B1 receives has IBCF-B's internal name in Contact, `sip:ibcf.ims.mnc002.mcc001.3gppnetwork.org:5060`, with the feature tags that the caller's side had there (RFC 3840). Each UE talks only to the IBCF of its own network.

## A conference across the border

Not every Contact is replaced. TS 24.229 5.10.5 has the IBCF pass a Contact on as it came when it is a GRUU, or when it has a feature tag that tells what the far end can do at that URI. Its example is `isfocus`, the tag of a conference focus: the URI in that Contact is the conference URI, which a participant stores and subscribes to (TS 24.147 5.3.1.3.2), and with which it rejoins the conference (the NOTE of 5.10.5). When the IBCF passes such a Contact on, it puts its own URI in Record-Route, so that the requests of the far end, sent to the conference URI, still come to it.

The IBCF stays a B2BUA all the same. Take a focus in network A that invites a user of network B. The focus sends the INVITE on behalf of the conference URI, a PSI that it hosts, to the S-CSCF of network A with `orig`. The S-CSCF serves the PSI with the profile of its PSI user from the HSS and routes the INVITE as a call of UE A1: ENUM gives the number a SIP URI of network B, so the INVITE leaves through IBCF-A ([How the focus invites a peer](mrf.md#how-the-focus-invites-a-peer)):

```text
focus (MMTel AS)  S-CSCF-A   IBCF-A ║ IBCF-B   UE B1
  | INVITE tel:+12025550151 |    ║   |        |
  | Route: <S-CSCF-A;lr;orig>    ║   |        |
  | Contact: <conf>;isfocus |    ║   |        |
  |---------------->|       |    ║   |        |  P-Asserted-Identity: <conf>, the PSI that the S-CSCF serves
  |                 |------>|==========>|------->|  Contact: <conf>;isfocus in each INVITE
  |                 |       | Record-Route:     |  Record-Route: IBCF-A on the link, IBCF-B in network B
  |                 |       |    ║   |        |
  |   SUBSCRIBE sip:conf (within the dialog)     |
  |<----------------|<------|<==========|<-------|
  |   NOTIFY        |       |    ║   |        |
  |---------------->|------>|==========>|------->|
```

- Each INVITE has the conference URI with `isfocus` in Contact: the one on the link and the one that UE B1 gets. UE B1 learns the URI of the conference it is in.
- Each IBCF puts its own name in Record-Route: IBCF-A its external name on the link, IBCF-B its internal name in network B. They are the only Record-Route on the link. Inside network A, S-CSCF-A record-routes too, so the requests within the dialog between IBCF-A and the focus pass it.
- A request of UE B1 within the dialog has the conference URI as its Request-URI and IBCF-B in Route. IBCF-B takes it in its dialog with network B, as it takes every request of a call, and sends a request of its own in its dialog with IBCF-A, to the conference URI again, and IBCF-A does the same towards the focus.
- The focus still sees IBCF-A's internal name as the Contact of UE B1's side: the Contact of UE B1 has no such tag, so IBCF-A replaces it.

The same holds the other way: when a focus answers with `isfocus` in the Contact of its 200, each IBCF passes that Contact on in its own 200 and puts itself in its Record-Route.

In the three-way session of GSMA IR.92 2.3.3, the focus's INVITE also carries a Replaces of the invited user's call with the UE that merges (TS 24.147 5.3.2.5.4), so that the user takes the new call in the place of that one. A Replaces names a dialog by its Call-ID and the tags of its two ends, as the UA that gets the INVITE has them: the to-tag is that UA's own tag (RFC 3891 3). Every B2BUA on the way has a dialog of its own on each side, with a Call-ID and tags of its own, so each one names in the Replaces the dialog of its other side, the one that its next hop knows. The MMTel AS does it for UE A1's call (TS 24.229 5.7.5.1), and each IBCF does it too: as an IMS-ALG, the IBCF correlates the dialog identifiers of its two dialogs (5.10.5). The IBCF finds the call by the dialog that the Replaces names in its `Store`, also when another instance of the IBCF holds the call, and sends the INVITE on with the dialog of the other side. UE B1 then finds its own dialog, takes the new call and ends the old one with a BYE.

```text
UE A1     MMTel AS (focus)   S-CSCF-A   IBCF-A ║ IBCF-B    UE B1
  | REFER: Refer-To B1 with Replaces of dialog 1      |          |
  |-------------->|               |         |  ║   |          |  dialog 1: UE A1 and the AS
  |               | INVITE with Replaces of dialog 2  |          |
  |               |-------------->|-------->|  ║   |          |  dialog 2: the AS and IBCF-A
  |               |               |         | INVITE with Replaces of dialog 3
  |               |               |         |=======>|          |  dialog 3: IBCF-A and IBCF-B, on the link
  |               |               |         |  ║   | INVITE with Replaces of dialog 4
  |               |               |         |  ║   |--------->|  dialog 4: IBCF-B and UE B1
  |               |               |         |  ║   |<---------|  200 OK: UE B1 takes the new call
  |               |               |         |  ║   |<---------|  BYE in dialog 4: the old call ends
```

Dialogs 1 to 4 all belong to the old call between UE A1 and UE B1, which has one dialog between each two of the UAs and B2BUAs on its way; the proxies of each network are left out of the picture. A Replaces that names no call of the IBCF goes on as it came, as TS 24.229 5.7.5.1 has an AS pass on one that names no dialog of its own: a B2BUA beyond the IBCF may hold that dialog. When no node does, the UA at the end answers 481 (Call/Transaction Does Not Exist) (RFC 3891 3). When the IBCF cannot read its `Store`, it cannot tell which, and answers the INVITE with 500 (Server Internal Error).

The script merges UE A1's calls with UE B1 and UE A2 this way. What you see:

- **The capture of network A.** The focus's INVITE from the S-CSCF to IBCF-A, with `isfocus` in Contact and a Replaces whose Call-ID is that of UE A1's call to UE B1 from the S-CSCF to IBCF-A: dialog 2.
- **The capture of the link.** The INVITE from IBCF-A to IBCF-B, whose Replaces has the Call-ID of the same call on the link instead: dialog 3.
- **The capture of network B.** The INVITE that reaches UE B1, whose Replaces has the Call-ID of UE B1's own call: dialog 4. UE B1 answers it with 200, and a BYE ends the old call.

A user of network B joins with an INVITE to the conference URI (TS 24.147 5.3.1.4.1), and a participant that has left comes back the same way. The script runs this after the merge: UE B1 leaves the conference and comes back by calling the conference URI, which the script reads from UE A1 (`ue ctl conference`). The INVITE is a request outside any dialog, a new call, which crosses as any call does: the P-CSCF and the S-CSCF of network B, IBCF-B, the link, IBCF-A. IBCF-A passes it to the I-CSCF of network A. The URI is in a subdomain of public service identities, so the I-CSCF asks the HSS nothing: it looks up the NAPTR and SRV records of `conf.ims.mnc001.mcc001.3gppnetwork.org` (RFC 3263) and sends the INVITE to the MMTel AS that they name (TS 24.229 5.3.2.1 step 3). The focus takes UE B1 in as it takes a user of its own network ([Coming back to the conference](mrf.md#coming-back-to-the-conference)), and answers with the conference URI and `isfocus` in Contact.

```text
UE B1   P-CSCF-B  S-CSCF-B  IBCF-B ║ IBCF-A   I-CSCF-A   DNS    MMTel AS (focus)
  | INVITE sip:conf-...@conf.ims.mnc001.mcc001.3gppnetwork.org     |
  |------->|-------->|------->|=========>|------->|        |        |
  |        |         |        |    ║     |        |------->|        |  NAPTR conf.ims.mnc001...
  |        |         |        |    ║     |        |<-------|        |  SRV _sip._udp.conf...: mmtel
  |        |         |        |    ║     |        |---------------->|  no LIR to the HSS
  |        |         |        |    ║     |        |<----------------|  200 OK, Contact: <conf>;isfocus
  |<-------|<--------|<-------|<=========|<-------|        |        |  Record-Route: IBCF-A on the link, IBCF-B in network B
```

What you see:

- **The capture of network B.** UE B1's INVITE to the conference URI, from the S-CSCF to IBCF-B, and the 200 that reaches UE B1 with the conference URI and `isfocus` in Contact and IBCF-B in Record-Route. When UE A1 leaves, the focus ends UE B1's call with a BYE.
- **The capture of the link.** The INVITE from IBCF-B to IBCF-A, and the 200 of IBCF-A with that Contact and its external name as the only Record-Route.
- **The capture of network A.** The INVITE from IBCF-A to the I-CSCF, the I-CSCF's NAPTR query for the subdomain and the SRV answer that names the MMTel AS, the INVITE from the I-CSCF to the AS and its 200. No LIR asks the HSS about the conference URI.

The IBCF hides nothing in that URI: what the focus writes there, the other network sees. The MMTel AS of this project builds its conference URIs in a subdomain of the home domain, `conf.ims.mnc001.mcc001.3gppnetwork.org`, which is not the name of a node, and with no port, so a conference URI shows network B no name of a node of network A. The script checks each Contact with `isfocus` in the capture of network B and on the link: none names a node or has a port.

A participant first subscribes with a SUBSCRIBE to the conference URI outside the dialog (TS 24.147 5.3.1.2). That one does not cross: the IBCF of the participant's network answers it with 403 (Forbidden) itself, as 5.10.1 lets it reject a request by local policy ([What this project simplifies](#what-this-project-simplifies)). On a 403, TS 24.147 5.3.1.2 has the participant send its SUBSCRIBE within the dialog of the conference session instead, and that one crosses like any other request of a call, when the agreements allow SUBSCRIBE and NOTIFY. Without SUBSCRIBE in the agreement, the IBCF answers 405 (Method Not Allowed) to both, as to any method outside the agreement. The tests of `internal/ibcf` run a focus's INVITE across the border in both directions, in one process (`TestConferenceFocusAcrossTheBorder`, `TestConferenceFocusAnswersAcrossTheBorder`). `TestConferenceInvitesAUserOfAnotherNetwork` in `internal/mmtel` runs the focus of this project and the S-CSCF of network A in one process, with a UA in the place of IBCF-A for the user of network B: UE A1 merges its call with that user and one with a user of network A, and the three hear one another. `TestReplacesNamesTheDialogOfTheNextHop` in `internal/ibcf` sends an INVITE with Replaces through both IBCFs, through another instance of IBCF-A than the one that holds the call, and `TestAReplacesOfNoCallCrossesAsItCame` one whose Replaces names no call. `TestConferenceMergesAUserOfTheOtherNetwork` merges the calls through the IBCFs of both networks, with the conference AS of this project as the focus of network A: the user of network B takes the focus's call in the place of its call with network A's UE.

The IBCF does not read the conference state either. The NOTIFYs carry it in their body. TS 24.229 5.10.6.3 has the IBCF change the SDP when it translates the addresses of the media, and leaves any other body to local policy, with passing it on unchanged, removing it and rejecting the request as examples. The IBCF of this project passes every other body on unchanged, so the focus decides what network B sees. RFC 4575 5.7 has the focus make the key of each `<endpoint>`, and gives the Contact and a GRUU as examples. The Contact that the focus of network A has for a participant is the address of a UE of network A, or, for a participant of network B, the internal name of IBCF-A, which took the place of the participant's own. The MMTel AS of this project names each endpoint by a `urn:uuid:` URI instead, so the conference state shows network B no address and no node name of network A. `TestConferenceStateAcrossTheBorder` in `internal/ibcf` checks this with the conference AS of this project as the focus of network A.

## A request from no peer network

IBCF-A takes requests on its external side only from the addresses that its profile of network B lists (`sources` in `ibcf.yaml`). The script sends IBCF-A an INVITE and a MESSAGE from 172.28.2.99, a host on the link that is neither IBCF-B nor a TrGW. IBCF-A drops both without any answer: no 100 (Trying), no error response, nothing at all. A border answers nothing to a sender outside every agreement, so that the sender learns nothing about the network, not even that a SIP node listens there.

```text
host 172.28.2.99            IBCF-A ║ network A
  |  INVITE                    |   ║
  |--------------------------->| x ║   (dropped: no peer sends from 172.28.2.99)
  |  MESSAGE                   |   ║
  |--------------------------->| x ║
  |                            |   ║
  |  (nothing comes back)      |   ║   (nothing enters network A)
```

The SIP stack of IBCF-A asks the IBCF about each request that belongs to no transaction it has, before it does anything with the request (`IBCF.Screen`). For a request from no peer network the answer is no, and the stack drops it: it starts no transaction, sends no response, and does not hand the request on. This holds for every method, CANCEL and ACK too, and for each retransmission, since nothing of the first copy is kept. A request that belongs to a transaction the IBCF already let in, such as the retransmission of a peer's INVITE, is not asked about again.

What you see:

- **The capture of the link.** The INVITE and the MESSAGE from 172.28.2.99 to IBCF-A, and nothing back to 172.28.2.99 but the ARP reply with IBCF-A's MAC address, which any host on the link gets (`ip.dst == 172.28.2.99` finds nothing). **Statistics > Flow Graph** draws each request as an arrow with no answer.
- **The capture of network A.** Nothing of the host: no request with its From, `+12025550198`.
- **The log of IBCF-A.** For each request, `"msg":"discarded a request from an address of no peer network"` with the method, the source and the Call-ID, and then the line of the SIP stack, `"msg":"dropped request that the screen refused"`.

A request from a peer that the border turns away gets a response, such as the 405 of a method outside the agreement: the peer is inside the agreement, and the response tells it what went wrong. TS 24.229 5.10.1 lets an IBCF reject a request with a 4xx response by local policy. TS 24.229 has a similar rule for the P-CSCF (5.2.6.3.2A): a request other than REGISTER that maps to no IP association of a UE is dropped without a 100 (Trying).

## When an IBCF instance starts again

The two dialogs of a call live in the process of the IBCF instance that took its INVITE. When that process stops, its calls stop with it, but their parties do not know: each keeps its dialog, and the TrGW keeps the context of the call. TS 24.229 5.10.2.2 step 2 and 5.10.3.2 step 2 have an IBCF that controls the transport plane save the Contact, CSeq and Record-Route of a call, so that it can release the session by itself (5.10.2.4, 5.10.3.4).

Each IBCF instance keeps such a record of each of its calls in its `Store`, under its own name: the TrGW context, and the two dialogs once the call is answered. It writes the dialogs again after each request within the call, since TS 24.229 5.10.2.3 and 5.10.3.3 have it save the Contact and CSeq of each one: a re-INVITE or an UPDATE can move a party to another Contact (RFC 3261 12.2). When an instance starts again under the same name, it reads its own records first. For each one it releases the TrGW context and sends a BYE in each of the two dialogs, with a CSeq well above the one of the record, since the dialog may have gone on after the record was written (RFC 3261 12.2.2). The IBCF logs `"msg":"ending a call that an earlier process of this instance left"` for each.

The records of one instance are under its name, so an instance reads only its own, once, when it starts: there is no search through the calls of the whole network. Each answered call is also under each of its two dialogs, and an INVITE with a Replaces has an instance read that one record by the dialog that the Replaces names, whichever instance holds the call (see "A conference across the border").

The records last only as long as the `Store` that holds them. A node whose configuration has a `store` section of kind `valkey` gives its IBCF an `ibcfstore.Store`, which keeps the records in that Valkey server: each record under `ibcf:call:<instance>:<ID>`, the IDs of the calls of an instance in the sorted set `ibcf:call-ids:<instance>`, and the instance and ID of the call that has a dialog under `ibcf:dialog:<Call-ID>:<local tag>:<remote tag>`, after the `prefix` of the section. `<instance>` is the host of the IBCF's URI in the home network. A call that starts or ends adds or removes only its own ID in the sorted set, so the calls of one instance never contend for a single list of them. A recreated IBCF container reads the sorted set from the server one page at a time, reads the record of each ID in it, and ends the calls of the process that stopped as above.

The new process reads its records before its UAs start. When it cannot read them, it does not start: an instance that took calls without its records would never end the calls of its earlier process. The node then logs `"msg":"node failed"` with an error that begins `ibcf: cannot read the calls that an earlier process of <instance> left`, and stops. Start it again once the store answers. In a network with several IBCF instances, the others take the new calls meanwhile.

A node without a `store` section keeps the records in the memory of its process (`ibcf.MemoryStore`), and they go with the process. The IBCF nodes of the networks of this guide have no `store` section, so a recreated IBCF container starts with an empty `Store`, reads nothing and ends nothing. If you recreate IBCF-A during a call:

- IBCF-A logs no `"msg":"ending a call that an earlier process of this instance left"`.
- A request of a party within that call, such as the BYE when UE A1 hangs up, gets 481 (Call/Transaction Does Not Exist) from the new process, which knows no such dialog (RFC 3261 12.2.2). The party on the other side of IBCF-A gets no BYE.
- TrGW-A keeps the context of the call reserved: nothing releases it.

The tests of `internal/ibcf` show the release itself. In `TestRestartedInstanceEndsTheCallsItLeft`, the first process of an instance holds a call across the border and stops without ending it; the next one sends a BYE in both dialogs and releases the TrGW contexts. It runs twice: with a `Store` in the program that the processes share, and with each process on an `ibcfstore.Store` of its own over a shared store, in place of the Valkey server. `TestRestartedInstanceEndsACallAtItsNewTarget` shows the BYE going to the Contact that a re-INVITE moved the party to.

## A re-INVITE within the call

A party changes the session of a call it is in with a re-INVITE: UE A1 puts the call on hold (TS 24.610), or moves its media to another port. Each IBCF on the way relays that re-INVITE like any request within the call, and every response to it, not the final one alone.

The far end may answer the offer in a reliable 183 and wait for the resources of the new session to be reserved before it changes anything (RFC 3312, TS 24.229 6.1.4.2 item b). The 183, the PRACK that acknowledges it and the UPDATE that tells that the resources are reserved then all cross both IBCFs, each as a message of its own dialog:

```text
 UE A1         IBCF-A          IBCF-B          UE B1
 |  re-INVITE: offer, Require: precondition     |
 |------------->|  re-INVITE     |              |
 |              |===============>|  re-INVITE   |
 |              |                |------------->|
 |              |                |  183: answer, Require: 100rel, RSeq
 |              |  183: RSeq     |<-------------|
 |              |<===============|              |
 |  183: RSeq   |                |              |
 |<-------------|                |              |
 |  PRACK: RAck |                |              |
 |------------->|                |              |
 |  200         |                |              |
 |<-------------|  PRACK: RAck   |              |
 |              |===============>|              |
 |              |  200           |              |
 |              |<===============|  PRACK: RAck |
 |              |                |------------->|
 |              |                |  200         |
 |              |                |<-------------|
 |  UPDATE: the resources are reserved          |
 |------------->|  UPDATE        |              |
 |              |===============>|  UPDATE      |
 |              |                |------------->|
 |              |                |  200: answer |
 |              |  200: answer   |<-------------|
 |              |<===============|              |
 |  200: answer |                |              |
 |<-------------|                |              |
 |              |                |  200: the re-INVITE
 |              |  200           |<-------------|
 |              |<===============|              |
 |  200         |                |              |
 |<-------------|                |              |
 |  ACK         |                |              |
 |------------->|  ACK           |              |
 |              |===============>|  ACK         |
 |              |                |------------->|
```

- **Each 183 belongs to the dialog it goes in**, with an RSeq of that dialog, and so does the PRACK that acknowledges it. The IBCF acknowledges the 183 of its other leg with a PRACK of its own, once the side that sent the re-INVITE has acknowledged its copy, so the PRACKs go from end to end as they would without the IBCF (RFC 3262 4). Each IBCF answers a PRACK without a body with 200 at once, as a UA does, and then sends its own on: a 200 that waited for the one of the far end would have the side retransmit its PRACK meanwhile (RFC 3261 17.1.2.2). The RAck of each PRACK names the RSeq and the CSeq of its own dialog (RFC 3262 7.2). A new offer that a side puts in its PRACK goes on in that PRACK (RFC 3262 5), through the TrGW like any other SDP.
- **A reliable copy for a reliable response only.** The IBCF copies a provisional response reliably when it came reliably and the re-INVITE offered 100rel. One that came without 100rel is copied without it too, even where the re-INVITE offered it: the session description in such a response is no answer, and a reliable copy would have the side that sent the re-INVITE take it for the answer and leave the answer that follows unread (RFC 3261 13.2.1). RFC 3262 3 leaves the choice to the UAS, which the IBCF is in that dialog. The SDP of such a response still goes through the TrGW, since the side that sent it may receive media there from now on.
- **The answer went in the 183, so the 200 to the re-INVITE carries none** (RFC 3261 13.2.1, RFC 3262 5). A side that offered no 100rel got its copy unreliably, and takes the session description of the 2xx alone: the 200 that the IBCF sends it carries the answer of the reliable 183 instead of the empty body of the 200 that came.
- **The re-INVITE does not hold its own PRACK and UPDATE up.** The IBCF sends the requests of one side on in the order they came, each once the one before has its response, so that the far end never gets them the other way round (RFC 3261 12.2.2). A re-INVITE has its final response only at the end of this flow, so the requests after it may go as soon as the far end has answered it with a reliable provisional response that the IBCF copies reliably: the PRACK of that copy and the UPDATE after it would otherwise wait for the final response, and the far end has the re-INVITE by then, with the CSeq that the IBCF gave it. Without such a response the re-INVITE keeps its turn up to its final response, and up to the ACK when its 2xx carries an offer.
- **No PRACK, no re-INVITE.** When the side that sent the re-INVITE does not acknowledge a reliable 183 within 64\*T1, 32 seconds, the IBCF ends the re-INVITE with 500, as RFC 3262 3 has a UAS do. The 183 of the other leg then gets no PRACK either, and the far end ends its own re-INVITE the same way, 32 seconds after it sent that 183. An error response of the far end does not wait for that PRACK: RFC 3262 3 lets a UAS send a final response other than 2xx before it, and the IBCF passes it on at once.
- **A CANCEL for the re-INVITE splits on the answer**, as RFC 6141 3.3 and 3.8 do. While no reliable provisional response has carried the session description of the re-INVITE, nothing of the session has changed, and the IBCF answers the re-INVITE 487 (Request Terminated) at once; the session stays as it was (RFC 3261 14.1). Once a reliable response has carried it, the change may have been made, and only the far end can tell: the IBCF leaves the re-INVITE to the final response of its other leg and passes that on, whether it is the 200 that RFC 6141 3.3 asks the far end for or an error response. The CANCEL itself ends at the IBCF, which answers it 200 (RFC 3261 9.2), and the IBCF sends none in its other dialog (see "What this project simplifies").

The UEs of this project answer a re-INVITE with a final response alone, so no capture of the scripts shows this flow. The tests of `internal/b2bua` run it in one process: `TestReliable183ToAReINVITECrossesBothLegs` for the reliable 183, its PRACK, the UPDATE and the 200, and `TestAnUnreliable183ToAReINVITEIsCopiedUnreliably` and `TestAReINVITEWithout100relKeepsTheAnswerForThe2xx` for the two copies that do not go reliably. The MMTel AS relays all of it the same way (see [the supplementary services guide](mmtel.md)).

## When the TrGW does not take an SDP

Every SDP of a call goes through the TrGW: the offer of the INVITE, and each one after it, such as the offer of an UPDATE, that of the re-INVITE of a hold, or the SDP of a PRACK. For each one the IBCF tells the termination that faces the sending side where that side now receives, with a `Modify`, and then writes the address of the TrGW towards the other side into the SDP (TS 24.229 6.7.1.4.1, TS 29.162 9.1.1 and 9.1.3). When the `Modify` fails, the IBCF has no SDP of its own to pass on. The SDP that came names the address of the sending side: that of a node inside the network, or that of the TrGW of the other network. TS 24.229 5.10.6.3 has the IBCF modify the SDP according to 6.7.1 when the TrGW translates the addresses, and the IBCF cannot do that for this SDP, so it does not let the SDP cross. If it did, the other side would send its RTP straight to that address, past the TrGW, and network B would learn an address inside network A.

### An SDP in a request

A request with such an SDP goes no further, and the IBCF answers it itself with 500 (Server Internal Error, RFC 3261 21.5.1). An SDP that the IBCF cannot read at all gets 488 (Not Acceptable Here), as the border answers one, and the TrGW is not asked; that holds also for the first SDP of the call, which the border does not read when the profile lists no codecs. For the INVITE that starts the call, that is all: the caller gets 500 or 488, the other network gets no INVITE, and the IBCF releases any context that it reserved. For a request within the call, what happens next depends on what the TrGW answered.

**The TrGW turns the `Modify` down and changes nothing.** The call stays up:

```text
 UE A1         IBCF-A          TrGW-A          IBCF-B
 | re-INVITE with a new SDP offer |                |
 |------------->|                 |                |
 |              | Modify: where UE A1 now receives |
 |              |---------------->|                |
 |              | error: nothing changed           |
 |              |<----------------|                |
 |  500         |                 |                |  the re-INVITE goes no further
 |<-------------|                 |                |
 |  ACK         |                 |                |
 |------------->|                 |                |
```

- **A final response other than 2xx leaves the session as it was before the request.** RFC 3261 14.1 says so for a re-INVITE, and RFC 3311 5.3 for an UPDATE. Only a 481, a 408 or no answer at all ends the dialog (RFC 3261 12.2.1.2), so the IBCF answers with none of them. The media goes on as the last offer and answer set it up, through the TrGW.
- **An IBCF on the way that passed the offer on puts its TrGW back.** When IBCF-B refuses the offer of UE A1, IBCF-A has told TrGW-A already to send where that offer said, as TS 29.162 9.1.1.2 lets it do on the offer. The 500 leaves UE A1 on the media of the last offer and answer, so IBCF-A has TrGW-A send there again with another `Modify`. The same holds for any final response other than 2xx, such as a 488 of UE B1, or a request that gets no answer. Neither TS 29.162 9.1.3 nor TS 24.229 6.7.1 names a procedure of its own for this; it follows from the session that RFC 3261 14.1 and RFC 3311 5.3 leave. When TrGW-A does not take that `Modify` either, its termination keeps sending where UE A1 no longer receives, and IBCF-A releases the call as below.
- **An answer in a provisional response that is not reliable puts two terminations back.** A far end may show its answer in a 183 that is not reliable, at another port, and then refuse the offer. The IBCF passes that 183 on and its SDP through the TrGW, since the far end may receive media at that port from now on, but the session description of such a response is no answer (RFC 3261 13.2.1): after the error response the session is the one of the last offer and answer. So the IBCF puts both terminations of the call back, the one that the offer moved and the one that the 183 moved. After an answer in a reliable 183 it puts neither back: that answer is the one that counts, both sides have it, and the change of the session counts as made (RFC 6141 3.3). `TestRejectedOfferAfterAPreviewGoesBackToTheLastOfferAndAnswer` and `TestRejectedOfferAfterAReliableAnswerKeepsIt` show the two.
- **An offer from network B**, such as that of an UPDATE of UE B1, takes the `Modify` of the termination that faces network B. The 500 then goes back to IBCF-B, which passes it on to UE B1, and UE A1 gets nothing.
- **The sending side may send the offer again later** (RFC 3261 21.5.1).
- **Which answers say that nothing changed.** The media relay carries out none of a request that it answers with an error, whether over its control API or in an H.248 Error descriptor. The one exception is an error that says that it has no such context or termination, which comes below.

When IBCF-B refuses the offer of a re-INVITE of UE A1, IBCF-A puts TrGW-A back:

```text
 UE A1         IBCF-A          TrGW-A          IBCF-B          TrGW-B
 | re-INVITE with a new SDP offer |                |               |
 |------------->| Modify: where UE A1 now receives |               |
 |              |---------------->|                |               |
 |              |  re-INVITE      |                |               |
 |              |=================================>| Modify        |
 |              |                 |                |-------------->|
 |              |                 |                | error: nothing changed
 |              |                 |                |<--------------|
 |              |  500            |                |               |
 |              |<=================================|               |
 |              | Modify: where UE A1 received before               |
 |              |---------------->|                |               |
 |  500         |                 |                |               |
 |<-------------|                 |                |               |
```

**The TrGW does not answer in time, or has lost the context.** The IBCF answers 500, and then releases the call:

```text
 UE A1         IBCF-A          TrGW-A          IBCF-B          UE B1
 | re-INVITE    |                 |                |               |
 |------------->| Modify          |                |               |
 |              |---------------->|                |               |
 |              | no answer in time, or no context |               |
 |  500         |                 |                |               |
 |<-------------|                 |                |               |
 |  BYE         |                 |                |               |
 |<-------------|  BYE            |                |               |
 |              |=================================>|  BYE          |
 |              |                 |                |-------------->|
 |              | Subtract        |                |               |  IBCF-B releases TrGW-B's context too
 |              |---------------->|                |               |
```

- **A TrGW that answers too late may have carried the `Modify` out.** Its termination would then send to the address of the SDP that UE A1 got 500 for, and UE A1 no longer takes media there. The IBCF cannot tell, so it ends the call.
- **A TrGW that has lost the context**, such as one that restarted without it, carries no media of the call any more, and would turn every later offer down in the same way.
- **TS 24.229 5.10.2.4 and 5.10.3.4** let an IBCF that controls the transport plane release a call by itself when the transport plane fails: it sends a BYE to each side, from what it saved of the two dialogs. The IBCF of the other network then ends its own dialogs and context as for any BYE. These clauses ask for no Reason in the BYEs, and the IBCF puts none there.
- **Before the call is answered** there is no dialog for a BYE yet. The caller gets 500 for its INVITE (RFC 3261 21.5.1), and the INVITE towards the other side is cancelled (RFC 3261 9.1). TS 24.229 5.10.2.4 and 5.10.3.4 speak of the BYEs only.

**The request is the PRACK of a reliable response of the callee.** The IBCF ends the INVITE, whatever the TrGW answered. The callee's reliable response would get no PRACK, and the callee would end the INVITE with a 5xx only once that response has waited 64*T1, 32 seconds, for its PRACK (RFC 3262 3). The PRACK of the caller matches the reliable response that the caller got, so the IBCF answers it 200 (RFC 3262 3), and then answers the INVITE 500 and sends the callee a CANCEL: RFC 6337 2.3 lets a UAS that cannot take what a PRACK carries end the INVITE with an error response. A PRACK with a new offer gets 500 instead of the 200, since a 200 to it would have to carry the answer (RFC 3262 5). A caller that requires 100rel gets every provisional response reliably (RFC 3262 3), the copy of one that the callee sent without 100rel among them. A PRACK with a new offer for such a copy goes on as an UPDATE (RFC 3311 5.1), and the IBCF refuses it as an UPDATE: the call may stay.

The tests of `internal/ibcf` run these flows in one process, with a TrGW that turns the `Modify` of one side down: `TestRejectedOfferPutsTheTrGWBack`, `TestRejectedOfferEndsTheCallWhenTheTrGWDoesNotGoBack`, `TestRefusedOfferOfTheINVITEIsRejected`, `TestRefusedOfferWithinTheCallIsRejected`, `TestOfferWithinTheCallEndsTheCallWhenTheTrGWMayHaveTakenIt`, `TestRefusedSDPOfAPRACKEndsTheINVITE` and `TestRefusedOfferOfAPRACKSentOnAsAnUPDATEKeepsTheCall`.

### An SDP in a response or an ACK

Nobody can turn a response or an ACK down, and the side that sent it has taken the session that its SDP describes already: it has answered an offer, or made one in a 2xx. The IBCF still does not pass that SDP on, for the reason above, so the call cannot go on, whatever the TrGW answered. The IBCF releases it, as above: once the call is answered with a BYE to each side, as TS 24.229 5.10.2.4 and 5.10.3.4 let it, and before that with the 500 and the CANCEL of RFC 3261. The response does not cross:

```text
 UE A1         IBCF-A          TrGW-A          IBCF-B          UE B1
 | INVITE       |                 |                |               |
 |------------->|=================================>|-------------->|
 |              |                 |                | 183 with the answer
 |              |<=================================|<--------------|
 |              | Modify: where TrGW-B receives    |               |
 |              |---------------->|                |               |
 |              | error           |                |               |
 |              |<----------------|                |               |
 |  500         |                 |                |               |  the 183 goes no further
 |<-------------|  CANCEL         |                |               |
 |              |=================================>|  CANCEL       |
 |              |                 |                |-------------->|
 |              | Subtract        |                |               |
 |              |---------------->|                |               |
```

- **A provisional response to the INVITE**, such as the reliable 183 with the answer: the caller gets 500 for its INVITE, and the INVITE towards the callee is cancelled.
- **The 200 of the INVITE**: the caller gets 500 in its place. The IBCF acknowledges the 200 and sends the callee a BYE.
- **The 2xx of a request within the call**: the request gets 500 in its place, and each side gets a BYE. A request from network B, such as an UPDATE of UE B1, reaches IBCF-A through IBCF-B, which may take the BYE of IBCF-A before the 500 that went ahead of it. IBCF-B then answers the UPDATE 487 (Request Terminated), as a request that the end of its dialog left pending (RFC 3261 15.1.2).
- **The answer in the ACK**, after an INVITE whose 200 carried the offer: the IBCF acknowledges the 200 without that answer, and sends each side a BYE. RFC 3261 13.2.2.4 asks a UAC that does not take the offer of a 2xx for a valid answer in the ACK, so the ACK carries an SDP that rejects every stream of the offer, in the same order and with port 0 (RFC 3264 6), and the BYE follows right after it.

An SDP in a response that the IBCF cannot read ends the call in the same way, such as an offer in the 183 or the 200 of an INVITE without one.

The tests `TestRefusedAnswerOfTheINVITEEndsTheCall`, `TestRefusedAnswerWithinTheCallEndsTheCall` and `TestRefusedAnswerInTheACKEndsTheCall` of `internal/ibcf` show these flows, for an SDP that enters a network and for one that leaves it. `TestUnreadableSDPOfTheINVITEIsRejected`, `TestUnreadableOfferInAResponseEndsTheCall` and `TestUnreadableSDPWithinTheCallGets488` show those for an SDP that the IBCF cannot read.

## Traffic statistics of the border

Each IBCF instance counts what it lets across and what it turns away, per peer network, for the operator. TS 32.409 4.11 has the IBCF count the sessions from and to other network domains and the 403 (Forbidden) responses for them; the counters below split them by peer, and count every other refusal too.

| Counter | Labels | What it counts |
| --- | --- | --- |
| `ims_ibcf_session_attempts_total` | `peer`, `direction` | Initial INVITEs that the IBCF sent on across the border (TS 32.409 4.11.1 and 4.11.3) |
| `ims_ibcf_session_failures_total` | `peer`, `direction`, `status` | Final responses other than 2xx that came back across the border to them, such as the 403 of 4.11.4 |
| `ims_ibcf_requests_rejected_total` | `peer`, `direction`, `method`, `status` | Requests that the IBCF answered itself instead of sending them on: the refusals of the profile (405, 420, 488, 404), the 403 to a SUBSCRIBE outside a dialog, the 504 when the other side does not answer, and the 404 to a request that leaves for no peer network, under the peer `other` |
| `ims_ibcf_requests_discarded_total` | `reason`, `method` | Requests dropped without any response, by reason: `unknown_source` for a request from an address of no peer network. The IBCF counts them when its SIP stack asks it whether a request may come in (`IBCF.Screen`). They have no response, so they do not count in the row above. |

`direction` is `exit` for a request that leaves the home network and `entry` for one that comes in. Every label has a fixed set of values: the names of the configured peers, the methods of TS 29.165 Table 6.1, and the status codes that the IBCF sends; anything else counts under `other`. The IBCF registers them in the `internal/metrics` registry that it is given, which writes them in the Prometheus text format. The IBCF nodes that Compose runs serve no such registry, so the counts stay inside their processes and you do not see them in the networks of this guide; the tests of `internal/ibcf` read them in the registry.

## The trust domain

The two operators have an agreement, so each network is in the other's trust domain (`trusted: true` in the profiles, TS 24.229 4.4.1). P-Asserted-Identity crosses the border: UE B1 learns who calls (4.4.2). P-Access-Network-Info and P-Charging-Vector cross too. P-Charging-Function-Addresses never crosses (5.10.2.2 step 9).

With `trusted: false`, the IBCF removes the header fields of TS 24.229 4.4 at the border: UE B1 would get the call without the caller's number. P-Charging-Vector goes as well (5.10.3.2), and the I-CSCF of the network that the call enters gives it a new icid-value (5.3.2.1). No scenario of the script uses it; try it by editing a profile.

## The profiles

The agreement of network A with network B is `deploy/interconnect/profiles/network-b.yaml`; that of network B with network A is `network-a.yaml`. TS 29.165 Annex C lists such choices as option items that two operators fill in; the fields are the ones the IBCF of this project uses.

| Field | What it decides | Reference |
| --- | --- | --- |
| `name`, `domain` | The peer network, and the host of a number that leaves as a SIP URI | TS 29.165 8.1 |
| `trusted` | Whether the peer is in the trust domain | TS 24.229 4.4 |
| `transports` | The transports to send with, preferred first | TS 29.165 6.2 |
| `methods` | The optional methods that the agreement allows, such as MESSAGE | TS 29.165 Table 6.1 |
| `option_tags` | `pass`, `remove` or `reject` for each option tag: `remove` takes the tag out of the requests that cross and out of everything that the IBCF itself sends the peer, and with `reject` the IBCF answers a request that requires the extension with 420 | TS 29.165 Table 6.1.3.1 (100rel item 18, precondition item 20), RFC 3261 8.2.2.3 |
| `headers` | The operator's rules to remove or rewrite header fields | TS 24.229 5.10.6.2 |
| `number_format`, `local_numbers`, `allow_local_numbers` | The form of the numbers, and how a local number becomes global | TS 29.165 8.1, RFC 3966 5.1.5 |
| `codecs` | The payload formats that the peer takes | TS 29.165 7.1 |

`IBCF_A_PROFILE` and `IBCF_B_PROFILE` pick another file for an IBCF. Compose then starts that IBCF again with it. The calls in progress through it are lost, since their dialogs were in the process that stopped and the new one has no record of them (see "When an IBCF instance starts again"), so change a profile between calls.

`profiles/example-ttc-jj-90.30.yaml` is an example in the style of a Japanese interconnect, after TTC JJ-90.30. It is not used by any scenario, and it shows the format of a profile, not the values of the standard ([TS 29.165, GSMA IR.95 and TTC](#ts-29165-gsma-ir95-and-ttc)).

## When the profiles do not match

These scenarios show agreements that do not match, a call that fails because of them, and an adjusted agreement with which it passes. The script runs these three. Before each command, set `C="docker compose -f compose.yaml -f deploy/interconnect/compose.yaml"`; UE B1 waits for a call, or stays registered for a short message, as in "Run it".

| Scenario | Mismatched profile | What happens | Adjusted profile |
| --- | --- | --- | --- |
| MESSAGE | `IBCF_A_PROFILE=network-b-no-message.yaml $C up -d ibcf-a` | IBCF-A answers the short message for network B with 405 (Method Not Allowed): MESSAGE needs an agreement (TS 29.165 Table 6.1). The SC of network A reports TP-ST 98, "no response from SME". | `network-b.yaml`: TP-ST 1 |
| Codecs | `IBCF_B_PROFILE=network-a-amr-wb-only.yaml $C up -d ibcf-b` | Network B takes AMR-WB alone, and UE A1 offers PCMU. IBCF-B removes every speech codec from the offer and answers 488 (Not Acceptable Here) (TS 29.165 7.1). | `network-a.yaml`, which lists PCMU |
| Numbers | `IBCF_A_PROFILE=network-b-local-numbers.yaml $C up -d ibcf-a`, then `$C run --rm ue call "sip:2025550151;phone-context=+1@ims.mnc002.mcc001.3gppnetwork.org;user=phone"` | IBCF-A lets the local number cross as it is. Network B's profile allows only global numbers (TS 29.165 8.1), so IBCF-B rejects the INVITE with 404 (Not Found) by local policy (TS 24.229 5.10.1). | `network-b-global-numbers.yaml`: IBCF-A makes the number `+12025550151` before it leaves |

The failures come from the IBCFs, by the agreement, not from the UEs: compare the INVITE on the link with the one that reaches network B, or the 405 and the 488 in the capture of the IBCF that sent them.

The precondition mechanism is optional at the II-NNI too (TS 29.165 Table 6.1.3.1 item 20), and the operators agree on it (Table C.3.1.2). Whether a call uses it is decided by its two ends, not by the border: an INVITE that only lists `precondition` in Supported gets an answer without preconditions from a callee that lacks them (RFC 3312 11), and one that requires it gets 420 from that callee (RFC 3261 8.2.2.3). With `precondition: reject` in its profile, the IBCF answers that 420 itself, as the UAS of the caller's dialog. A profile cannot `remove` precondition: the tag would go, and the precondition attributes would stay in the SDP.

`remove` is for an extension that lives in the header fields alone, such as `100rel` or `timer`. The IBCF takes the tag out of each request that crosses the border, either way. It also keeps the tag out of what its own UA sends in the dialog with the peer: the Supported of the requests within the dialog and of its responses, and the Require of a reliable provisional response. And it reads the requests of the peer without the tag, so that it does not use the extension with the peer either: with `100rel: remove`, the 183 that the IBCF sends the peer is not reliable, and the peer sends no PRACK for it (RFC 3262 3). In the dialog with the home network, the IBCF uses the extension as before.

## Short messages across

```text
UE A1   S-CSCF-A  IP-SM-GW-A+SC  IBCF-A ║ IBCF-B  I-CSCF-B  S-CSCF-B  IP-SM-GW-B   UE B1
  |MESSAGE |          |             |    ║   |        |         |          |          |
  |------->|--------->| (1)         |    ║   |        |         |          |          |
  |  202, submit report (RP-ACK)    |    ║   |        |         |          |          |
  |<-------|<---------| ENUM (2)    |    ║   |        |         |          |          |
  |        |          | MESSAGE (3) |    ║   |        |         |          |          |
  |        |          |------------>|===========>|--->|-------->| (4)      |          |
  |        |          |             |    ║   |        |         |--------->| 202      |
  |        |          |<------------|<===========|<---|<--------|<---------|          |
  |  MESSAGE status report, TP-ST 1 (5)  ║   |        |         | MESSAGE SMS-DELIVER (6)
  |<-------|<---------|             |    ║   |        |<--------|<---------|          |
  |        |          |             |    ║   |        |-------->|--------------------->|
  |        |          |             |    ║   |        | delivery report back to IP-SM-GW-B (7)
```

1. UE A1 submits the short message to its SC, as in the SMS guide.
2. The SC stub asks the HSS of network A for the number by Sh, and gets "user unknown". With an `ibcf` setting (`deploy/interconnect/a/ipsmgw.yaml`), it then asks ENUM, and finds the number in network B's domain. It accepts the message: UE A1 gets the submit report.
3. The IP-SM-GW sends the SMS-DELIVER in a MESSAGE to `sip:+12025550151@ims.mnc002.mcc001.3gppnetwork.org;user=phone`, with IBCF-A in Route. The sender is the SC by its number, `tel:+12025550100`, so that no host name of network A crosses. The profiles allow MESSAGE (TS 29.165 Table 6.1.3.1 item 29).
4. In network B the I-CSCF turns the Request-URI into the tel URI, and the S-CSCF finds a filter criterion of the terminating case: a MESSAGE with a short message whose P-Asserted-Identity is not network B's own IP-SM-GW goes to that IP-SM-GW (`deploy/interconnect/b/hss.yaml`). TS 23.204 6.4a step 2 has a short message from another IMS network reach the IP-SM-GW that serves the user.
5. The IP-SM-GW of network B answers 202. No delivery report comes back across the border, so the SC of network A ends its part there; the status report says TP-ST 1, "forwarded by the SC to the SME but the SC is unable to confirm delivery" (TS 23.040 9.2.3.15).
6. The IP-SM-GW of network B delivers the SMS-DELIVER to UE B1 in a MESSAGE of its own, keeping the SC of network A in the RP-OA. UE B1 logs `"msg":"sms received"` with `"from":"+12025550101"`.
7. UE B1's delivery report goes to the IP-SM-GW of network B, which ends the delivery.

### The other way, from network B to network A

A short message from UE B1 to UE A1 takes the same steps with the networks swapped.

1. The SC of network B finds no user for `+12025550101` in its own HSS. ENUM in network B gives `sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org;user=phone`. The IP-SM-GW of network B has an `ibcf` setting too (`deploy/interconnect/b/ipsmgw.yaml`), and sends the MESSAGE to IBCF-B, from the SC `tel:+12025550150`.
2. IBCF-B writes the number in P-Asserted-Identity as a SIP URI of its own domain, `sip:+12025550150@ims.mnc002.mcc001.3gppnetwork.org;user=phone`, since numbers cross in SIP URIs by the agreement (TS 29.165 8.1). IBCF-A passes the MESSAGE to the I-CSCF of network A, which turns the Request-URI into `tel:+12025550101` and asks the HSS where the user is (LIR).
3. The S-CSCF of network A finds the criterion of priority 2 in `deploy/compose/hss.yaml`, the same one that network B has, and sends the MESSAGE to the IP-SM-GW of network A, with `sescase=term` in P-Served-User. The IP-SM-GW answers 202, and the 202 goes back to network B: the status report to UE B1 says TP-ST 1.
4. The IP-SM-GW of network A delivers the SMS-DELIVER to UE A1 in a MESSAGE of its own, with the SC of network B, `12025550150`, in the RP-OA. UE A1 logs `"msg":"sms received"` with `"from":"+12025550151"`, and sends its delivery report to the IP-SM-GW of network A.

The IP-SM-GW of network A sends that MESSAGE to UE A1 through the I-CSCF and the S-CSCF, like every short message for a user. Its P-Asserted-Identity is the IP-SM-GW's own URI, and the criterion has a negated condition on it, so the S-CSCF does not send the MESSAGE back to the IP-SM-GW. Without that condition, every short message for a user would loop between the S-CSCF and the IP-SM-GW. TS 23.218 5.2.3 NOTE 5 warns about such loops of the filter criteria. A short message inside network A still reaches the user the way the SMS guide shows.

This project carries short messages between the networks this way because it is the one path that goes through the IBCFs, so the message shows in the capture of the link. The standard path for a user with an MSISDN goes from the SC through an SMS-GMSC, which asks the HSS of network B and hands the message to its IP-SM-GW over MAP or SGd (TS 23.204 6.4). This project has neither, so the short message crosses the II-NNI in SIP, the way TS 23.204 6.3a and 6.4a carry one between IMS networks for users without an MSISDN, without the multipart body that TS 24.341 5.3.3.5.2 adds.

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Method == "INVITE" && count(sip.Via) == 1` | On the link: the INVITEs of the IBCFs, each with its own Via alone. |
| `sip.Call-ID == "<Call-ID>"` | One dialog. A Call-ID of the link does not show in the capture of either network. |
| `sip.Route contains "ibcf"` | Requests on their way to or from an IBCF. |
| `sip.r-uri contains "user=phone"` | Requests with a number in a SIP URI, between the S-CSCF and the I-CSCF of the other network. |
| `sip.r-uri == "tel:+12025550151"` | The INVITE after the I-CSCF of network B turned it into a tel URI. |
| `sdp.connection_info.address == 172.28.2.11` | SDP with the address of TrGW-A on the link. |
| `rtp && ip.addr == 172.28.2.0/24` | RTP between the TrGWs (with `rtp.heuristic_rtp` on). |
| `dns.qry.name contains "5.1.0.5.5.5.2.0.2.1.e164.arpa"` | ENUM for network B's numbers. |
| `sip.Replaces` | The focus's INVITE that takes UE B1's call over, in each capture with the Call-ID of that call on its side. |
| `sip.Status-Code in {404, 405, 420, 488}` | The rejections of the scenarios. |
| `gsm_sms.sms_text` | The short messages, decoded, also on the link. |
| `sip.Method == "MESSAGE" && sip.P-Served-User contains "sescase=term"` | A short message from the other network, on its way from the S-CSCF to the IP-SM-GW. |
| `ip.addr == 172.28.0.0/24 \|\| frame contains "172.28.0."` | In the captures of network B and of the link: anything of network A's inside. It must find nothing. |
| `ip.addr == 172.28.2.99` | On the link: the requests of the host of no peer network. None of them has an answer. |

**Statistics > Flow Graph** on the capture of the link draws the ladder between the two IBCFs.

`scripts/e2e-interconnect.sh` runs every filter of this table against the three captures of this scenario. A filter must find a frame in one of them, and the one that must find nothing must find no frame in the captures of network B and of the link; otherwise the run fails. The filter with `<Call-ID>` only has to be one that tshark can read.

## TS 29.165, GSMA IR.95 and TTC

| TS 29.165 | Here |
| --- | --- |
| 5.1, Ici and Izi | The link `nni0` with the two IBCFs and the two TrGWs |
| 6.1.1.2, Table 6.1 (methods) | `methods` in the profile; the mandatory methods need no agreement |
| 6.1.3, Table 6.1.3.1 (capabilities) | `option_tags`, `trusted` and `headers` |
| 6.2 (transport) | `transports`; a message over 1300 bytes goes over TCP all the same |
| 7.1 (codecs) | `codecs`; the IBCF removes codecs, and transcodes none |
| 8.1 (numbers) | `number_format` and `local_numbers`; the I-CSCF's tel URI |
| Annex C (option items) | The profile files as a whole |

GSMA IR.95 is the GSMA's profile of the SIP and SDP between IMS networks. This guide does not show how it narrows TS 29.165.

The Japanese standard for the interconnect between IMS operators is TTC JJ-90.30. `profiles/example-ttc-jj-90.30.yaml` is an example of the kind of choices that such a national standard makes on top of TS 29.165, such as SIP over UDP only and G.711 in every offer. It shows the format, not the standard: read the standard itself for its values. Two kinds of rule that a national standard can make cannot be written in a profile of this project: a session timer on every session, and a limit on the tel URIs in P-Asserted-Identity. JJ-90.31 (carrier ENUM) and JJ-90.32 (DNS between operators) are the Japanese standards for what the DNS of each network does here.

## Where the code is

| Package or file | What it holds |
| --- | --- |
| `internal/interconnect` | The profile, and what the IBCF does to each message: `screen.go` (methods, option tags, header rules), `trust.go` (the trust domain, privacy), `number.go` (numbers), `codec.go` (codecs), `leg.go` (which header fields go from one dialog to the other), `border.go` (the order of TS 24.229 5.10.2 and 5.10.3). |
| `internal/b2bua` | The relay between the two dialogs of a call: the responses and PRACKs, the 2xx and its ACK, the requests within the dialogs, the early dialogs of a fork, and the end of the call. The IBCF tells it what crosses (`Hooks`). |
| `internal/ibcf` | The IBCF role: the two UAs, exit and entry (`ibcf.go`), the INVITE of a call and what crosses between its two dialogs (`call.go`), the short messages (`message.go`), and the TrGW (`trgw.go`, `media.go`). Its tests run two networks in one process. |
| `internal/scscf/session.go` | `exitIBCF`: the request for another network goes to the IBCF. |
| `internal/icscf/session.go` | `telURIOf`: the number in a SIP URI becomes a tel URI. |
| `internal/sc/sc.go`, `internal/ipsmgw/deliver.go`, `internal/ipsmgw/message.go` | The short message across: ENUM in the SC, the MESSAGE through the IBCF, the one from another network. |
| `internal/config/ibcf.go` | The settings of the ibcf role and the profile files. |
| `deploy/interconnect/` | The Compose override, the files of both networks, and the profiles. |
| `scripts/e2e-interconnect.sh` | The scenarios and the checks of this guide. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal/ibcf internal/b2bua internal/interconnect internal/sc internal/ipsmgw` lists them, with those of the SMS guide.

| Where | What is left out | Why |
| --- | --- | --- |
| IBCF, TS 24.229 5.10.2.4, 5.10.3.4 | The IBCF ends a call by itself only when one of its two dialogs fails, when the TrGW does not take an SDP of the call, and when it takes the place of an earlier process of its instance. The TrGW reports nothing to the IBCF by itself, so a call whose TrGW context is lost goes on without media until a party hangs up, or until the next SDP of the call finds the context gone. The records of an instance that never starts again stay in the Store, with their TrGW contexts. | The TrGW of this project reports no failures to the IBCF, and the instances of this project keep their names when they start again. |
| IBCF, RFC 3261 9.1, RFC 6141 3.3 | A CANCEL for a re-INVITE within a call ends at the IBCF, which does not cancel the re-INVITE in its other dialog. The far end answers that one as it would have without the CANCEL, and the two sides can end up with different sessions. | No UE of this project cancels a re-INVITE: it waits for the final response, which comes at once unless the far end holds the change back with preconditions. |
| IBCF, TS 24.229 5.10.5 | The Contact of a 3xx does not cross, so a redirection from the other network reaches the caller without its targets. | The Contact of a 3xx names where to try next, not a dialog, and no node of this project answers with a 3xx. |
| IBCF, RFC 3891 3 | A Replaces that names an early dialog goes on as it came, so the UA at the far end does not find that dialog. | The IBCF writes the dialogs of a call in its record once both are confirmed, and no node of this project replaces an early dialog. |
| IBCF, RFC 3515 2.1 | A Refer-To that holds a Replaces goes on as it came. When a user transfers one user of the other network to another user there, the user that gets the INVITE with that Replaces would find no dialog of its own. | This project has no call transfer (TS 24.629). A UE that merges its calls into a conference sends its REFERs to the focus of its own network. |
| IBCF, TS 24.229 5.10.2.2, 5.10.3.2 | Of the requests outside a dialog, only INVITE and MESSAGE cross. The IBCF answers an OPTIONS itself, a SUBSCRIBE with 403, and a PUBLISH with 405: a participant of a conference of the other network subscribes to it within the dialog of its conference session, as TS 24.147 5.3.1.2 has it do after a 403. A provisional response to a MESSAGE stays on its side. | A SUBSCRIBE outside a dialog makes a dialog of its own, which the IBCF would have to hold on each side as it holds those of a call; that part of the B2BUA is not built. |
| IBCF, TS 29.238, TS 29.165 3.2 | The IBCF drives the TrGW over the HTTP control API of the media relay, not over H.248 on Ix. | The relay is the one that the P-CSCF drives over the same API; `internal/h248` shows the H.248 messages. |
| IBCF, TS 24.229 5.10.2.1, 5.10.3.1, TS 29.165 5.3.1 | Only the non-roaming II-NNI. A REGISTER does not cross (405); no Path, no restoration procedures, no `iotl`. | Roaming is out of the scope of this project. |
| IBCF, TS 24.229 5.10.2.2 and 5.10.3.2 | No charging data (P-Charging-Function-Addresses, fe-identifier), private network traffic, emergency calls or calling number verification, and no session timer of the IBCF's own. | No node of this project writes charging records, and the project has none of the others. |
| IBCF, TS 24.229 6.7.1.4.1 | IPv4 only; no translation between IPv4 and IPv6, no `altc`. | Both networks are IPv4. |
| IBCF, TS 24.229 6.7.1.4.1, RFC 3264 6 | Only the first audio stream goes through the TrGW. | The UEs offer one audio stream. |
| IBCF, TS 29.238 Table 5.17.2.1.1 | A release of a TrGW context that fails is not tried again; one IBCF uses one TrGW. | A context left behind holds only its ports in the TrGW, and one TrGW per network is enough to show the interconnect. |
| IBCF, TS 24.229 Annex I, RFC 4694 | A Request-URI without a host, such as a tel URI or one with `rn`, finds no peer. | The S-CSCF sends a SIP URI from ENUM; there is no table of number ranges. |
| Border, TS 29.165 7.1 | No transcoding, and a response whose SDP has no codec that the peer takes goes on unchanged. | Transcoding needs codecs in the TrGW, and the nodes of this project neither encode nor decode AMR. |
| Border, TS 29.165 6.1.4 | An SDP inside a multipart body is not filtered. | No node of this project sends one across. |
| Border, TS 26.114 6.2.5.2 | `b=AS` is not computed again for the codecs that are left. | The value of the widest codec stays large enough. |
| Border, TS 24.229 5.10.8 | There is no local policy that treats a request without Privacy as one that asks for it. | No scenario needs it. |
| SC, TS 23.204 6.4 step 3 | The SC finds a user of another network by ENUM, not by asking that network's HSS through an SMS-GMSC. | This project has no SMS-GMSC, MAP or S6c. |
| IP-SM-GW, TS 23.204 6.4 steps 2 to 4 | The short message crosses in a SIP MESSAGE with the RP-DATA alone, not over MAP or SGd, and not in the multipart form of TS 24.341 5.3.3.5.2. | It is the one path that goes through the IBCF, so the short message crosses the border in the capture. |
| IP-SM-GW, TS 24.341 5.3.3.5.4 and 5.3.3.5.6, TS 23.204 6.5 step 6 | No delivery report crosses back. The sending side ends the delivery when the other network accepts the MESSAGE; the receiving side reports to its own SC only. | It keeps both IP-SM-GWs to what they already do. |

## Clean up

```sh
docker compose -f compose.yaml -f deploy/interconnect/compose.yaml --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).

`scripts/e2e-interconnect.sh` runs under the project name `ims-practice-interconnect` and takes its containers down by itself, unless `E2E_KEEP=1` is set.
