# Follow a voice call through the IMS

[日本語](../ja/call.md)

This guide makes one call from UE 1 to UE 2 by telephone number and walks through every message in the capture: how the number becomes a SIP address, how each CSCF finds the next one, how the two UEs agree on the media, and how the voice flows through the media relay.

The guide follows 3GPP Release 18 (TS 24.229 V18.11.0, TS 29.228 V18.0.0) and the RFCs named in each step.

## Before you start

- You have read [Follow a UE registering with the IMS](registration.md). Both UEs register first, exactly as that guide shows, and a call reuses what registration set up: the Path, the Service-Route and the P-CSCF's IP association.
- The abbreviations are explained in the [glossary](../../glossary.md).

## The nodes

| Node | Address | What it does in a call |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller, `+1-202-555-0101`. |
| UE 2 | 172.28.0.101 | The callee, `+1-202-555-0102`. |
| P-CSCF | 172.28.0.40 | Serves both UEs. Each call passes it twice: once on the caller's side, once on the callee's. |
| S-CSCF | 172.28.0.30 | Serves both UEs too, so it is both the caller's (originating) and the callee's (terminating) S-CSCF. |
| I-CSCF | 172.28.0.20 | Entry point of the callee's home network. It asks the HSS where the callee is. |
| MMTel AS | 172.28.0.75 | Takes part twice, once for each user (steps 3 and 6). The S-CSCF sends it each INVITE by an iFC. It reads the user's service settings from the HSS over Sh, and with the default settings changes nothing. [Supplementary services with the MMTel AS](mmtel.md) shows what it does with other settings. |
| HSS | 172.28.0.10 | Answers the I-CSCF's location query over Cx, and the MMTel AS's query for the service settings over Sh. |
| DNS | 172.28.0.2 | Holds the ENUM records that turn numbers into SIP URIs. |
| Media relay (IMS-AGW) | 172.28.0.50 | Relays the RTP. The P-CSCF controls it. |

The numbers are in the 555-0100 to 555-0199 range that the North American Numbering Plan keeps for fiction.

## Make a call

1. Start the system and wait for the Diameter connections, as in the registration guide:

   ```sh
   docker compose up -d --build
   ```

2. In one terminal, let UE 2 wait for a call. It answers after ringing for a second, plays a tone, and saves what it hears:

   ```sh
   docker compose run --rm ue2 answer \
     -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
     -password test-password-2 -wav-out /captures/ue2-heard.wav
   ```

3. In another terminal, call UE 2 from UE 1 by number. UE 1 plays a 3-second tone and hangs up:

   ```sh
   docker compose run --rm ue call -tone 3s tel:+1-202-555-0102
   ```

   With `-wav-in <file>` instead of `-tone`, the UE plays a WAV file. It must be 16-bit PCM, mono, 8000 Hz.

4. `captures/ue2-heard.wav` now holds what UE 2 heard. Open it in any audio player.

To try the failures:

- **Busy.** Start UE 2 with `-reject 486`. The call ends with `486 Busy Here`.
- **Not registered.** Call `tel:+12025550103`, a subscriber that never registers. The call ends with `480 Temporarily Unavailable`.
- **Unknown number.** Call `tel:+12025550198`, which has no ENUM record. The call ends with `404 Not Found`.
- **One-way audio.** Start UE 2 with `-tone 0s`, so that it sends no RTP. UE 1 still hangs up with a BYE, but `ue call` ends with the error `no RTP received`: UE 2's SDP answer said `sendrecv`, so its RTP should have come after the answer. UE 2 heard UE 1, so its call ends without an error. A call is not one-way when, at its end, the peer's SDP says `sendonly`, `recvonly` or `inactive`, or the UE itself holds it: a UE that holds a call sends nothing (IR.92 2.3.10). Early media before the answer does not count as RTP of the call.

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
sip || diameter.cmd.code == 302 || dns.qry.type == 35 || rtp || rtcp
```

**Telephony > VoIP Calls** lists the call. Select it and press **Flow Sequence** for the ladder, or **Play Streams** to hear both directions.

RTP runs on ports that only the SDP announces. Wireshark finds them when the capture also holds the SIP of the call, which it does here. If a stream shows up as plain UDP, enable **Analyze > Enabled Protocols > rtp_udp** (the RTP heuristic).

## The sequence

```text
  UE 1      P-CSCF     S-CSCF    MMTel AS      DNS      I-CSCF       HSS      S-CSCF     P-CSCF      UE 2
    |INVITE (1)>          |          |          |          |          |          |          |          |
    |          |INVITE (2)>          |          |          |          |          |          |          |
    |          |          |INVITE (3)>          |          |          |          |          |          |
    |          |          <new INVITE|          |          |          |          |          |          |
    |          |          |ENUM NAPTR (3)------->          |          |          |          |          |
    |          |          <sip: URI-------------|          |          |          |          |          |
    |          |          |INVITE (4)---------------------->          |          |          |          |
    |          |          |          |          |          |LIR------->          |          |          |
    |          |          |          |          |          <LIA-------|          |          |          |
    |          |          |          |          |          |INVITE (5)----------->          |          |
    |          |          |          <INVITE (6)---------------------------------|          |          |
    |          |          |          |new INVITE--------------------------------->          |          |
    |          |          |          |          |          |          |          |INVITE (6)>          |
    |          |          |          |          |          |          |          |          |INVITE (7)>
    <183 with the SDP answer, back through every hop (8)-----------------------------------------------|
    <PRACK, 200 (PRACK) in each of the three dialogs (9)===============================================>
    <180, PRACK, 200 (PRACK) (10)======================================================================>
    <200 OK (INVITE) (10)------------------------------------------------------------------------------|
    |ACK (11)==========================================================================================>
    | RTP and RTCP through the media relay (12) |          |          |          |          |          |
    | BYE, 200 (BYE) (13) |          |          |          |          |          |          |          |
```

The I-CSCF is not in the route set. It does not record-route, so the PRACK, the ACK and the BYE skip it.

The MMTel AS takes part twice, once for each user (steps 3 and 6). It is a routeing B2BUA (TS 24.229 5.7.5): it answers the INVITE that it gets and sends a new one, with a Call-ID of its own. The call therefore has three dialogs: UE 1 with the caller's AS, that AS with the callee's AS, and that AS with UE 2. A request within a dialog ends at the far end of its dialog, and the AS sends it on in the next one.

### 1. UE 1 sends the INVITE

TS 24.229 5.1.3.1 and 5.1.2A.1.1. Look at:

- **Request-URI** `tel:+1-202-555-0102`. The UE dials a number; it does not know where the callee is.
- **Route** with two entries: the P-CSCF, then the Service-Route that UE 1 got at registration (`sip:orig@scscf...`). Every request of a UE goes through its P-CSCF to its S-CSCF.
- **Supported: 100rel, timer, 199, precondition**. UE 1 accepts reliable provisional responses (RFC 3262 4), the session timer (RFC 4028), the 199 response (RFC 6228) and the precondition mechanism (RFC 3312). Preconditions are only supported here, not required (TS 24.229 5.1.3.1).
- **Session-Expires: 1800**, the session interval that IR.92 2.2.8 asks a UE for. The callee's 200 makes UE 1 the refresher, and UE 1 refreshes the session with an UPDATE after half the interval.
- **P-Early-Media: supported**, which says that UE 1 takes the network's authorization of early media (TS 24.229 5.1.3.1, RFC 5009). **P-Preferred-Service** and **Accept-Contact** carry the ICSI of multimedia telephony (TS 24.229 5.1.2A.1.1), and **User-Agent** names the UE (IR.92 2.6). The Contact carries the same ICSI and the `audio` feature tag (IR.92 2.2.4).
- **The SDP offer** (RFC 3264 5.1). `c=` and `m=audio` name UE 1's own address and RTP port. `a=rtpmap:0 PCMU/8000` offers G.711, and `a=rtpmap:101 telephone-event/8000` offers DTMF (TS 24.229 6.1.1). `b=AS`, `b=RS` and `b=RR` give the bandwidth of RTP and RTCP (IR.92 3.2.2.4, 3.2.4). The `a=curr` and `a=des` lines are the preconditions: UE 1 has no access network that reserves resources, so it says that its own are there already (TS 24.229 6.1.2).

A UE started with `-epc` attaches to the EPC stub first and is a VoLTE UE: it offers AMR-WB and AMR before PCMU, and its offer says that its resources are not reserved until the network sets up its voice bearer. [The VoLTE call flow](volte.md) follows that call.

### 2. The P-CSCF sends it to the caller's S-CSCF

The P-CSCF knows this is the caller's side because the top Route does not carry the `term` user part of its Path (TS 24.229 5.2.6.2). It then does what TS 24.229 5.2.6.3.3 lists:

- It removes its own Route and checks that the rest is the Service-Route. A UE that preloads something else gets the Service-Route instead.
- It adds **P-Asserted-Identity**, UE 1's default public identity. The network trusts this header; whatever the UE claims in From does not matter.
- It adds **Record-Route** with its own URI, so that the later requests of the call come back through it.
- It asks the media relay for a **context** with an access termination (towards UE 1) and a core termination (towards the network). It tells the access termination where UE 1 receives (the address in UE 1's SDP), and **rewrites the SDP** so that `c=` and `m=` name the core termination. From here on, the network sees the relay, not UE 1.

The P-CSCF can have several media relays: `media_relays` in `deploy/compose/pcscf.yaml` lists them, and this network has one. The P-CSCF picks one for each call by a hash of the Call-ID, so that the calls spread over the relays. When that relay gives no context, for example because it does not answer or has no port left, the P-CSCF takes the next one in the list. The dialog that the P-CSCF keeps names the relay that holds the context, and the later changes of the media and the release of the context go to that relay.

It asks over a small HTTP control API of this project, which the capture shows between the P-CSCF and the relay. The standard protocol for the task is H.248, and the P-CSCF speaks it in place of the HTTP API on the deployment in [the guide to the setup](README.md#the-media-relay-controlled-over-h248).

### 3. The S-CSCF turns the number into a SIP URI

The top Route carries `orig`, so the S-CSCF serves the caller (TS 24.229 5.4.3.1). The user it serves is the one in P-Asserted-Identity, or the one in P-Served-User when a node of the network sends a request on behalf of a user (TS 24.229 5.4.3.2 step 1). It checks that this user is registered. (An application server that sends on behalf of a user or of a PSI that it hosts appends `orig` as a parameter to the S-CSCF's URI instead. For a served user with no registration, such as a conference URI, the S-CSCF takes the profile from the HSS: see [How the focus invites a peer](mrf.md#how-the-focus-invites-a-peer).) It then adds UE 1's tel URI as a second P-Asserted-Identity, because the tel URI is an alias of the asserted SIP URI (TS 24.229 5.4.3.2 step 9). The INVITE of UE 1 carries **P-Preferred-Service** with the ICSI of MMTel. The S-CSCF checks that the INVITE is a call with audio or video, which the operator data of this project counts as MMTel. It then puts the ICSI into **P-Asserted-Service** and removes P-Preferred-Service (TS 24.229 5.4.3.2 step 4C, RFC 6050). The P-CSCF of UE 2 later names this service to the PCRF. Then it record-routes. An initial filter criterion (iFC) of every subscriber sends each INVITE to the MMTel AS first (TS 24.229 5.4.3.2 step 5). The S-CSCF adds **P-Served-User** with `sescase=orig`, and puts on top of Route the AS and then itself, with an original dialog identifier (`odi`). The AS reads the caller's service settings from the HSS over Sh and sends a new INVITE back along that Route. With the default settings of `deploy/compose/hss.yaml` it changes nothing else in the call. The INVITE comes back with the `odi`, and the S-CSCF goes on after that criterion. The Request-URI is a tel URI, so the S-CSCF asks DNS (TS 24.229 5.4.3.2 step 10, RFC 6116):

1. The number `+12025550102` becomes the domain `2.0.1.0.5.5.5.2.0.2.1.e164.arpa`: the digits reversed, one label each.
2. The NAPTR record there has the service `E2U+sip` and a regular expression that produces `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`.
3. That becomes the new Request-URI.

The records are in `deploy/compose/dns/e164.arpa.zone`. A number with no record cannot be reached inside the IMS. A real network may send the call to a BGCF towards the telephone network; this project has none, so the S-CSCF answers 404. TS 24.229 5.4.3.2 step 10 lets the S-CSCF answer the caller in place of sending the call to a BGCF.

### 4. The S-CSCF finds the callee's home network

The Request-URI now names the domain `ims.mnc001.mcc001.3gppnetwork.org`. The S-CSCF looks it up with NAPTR and SRV, as the P-CSCF did for the REGISTER, and forwards the INVITE to the I-CSCF, the entry point of that domain.

The INVITE has grown with every hop. From the S-CSCF on it is larger than 1300 bytes, so each CSCF sends it over TCP instead of UDP (RFC 3261 18.1.1). In the capture, those hops show `SIP/2.0/TCP` in the Via, and the responses come back on the same TCP connection.

### 5. The I-CSCF asks the HSS where the callee is: LIR and LIA

The I-CSCF sends a **Location-Info-Request** (LIR, Cx command 302) with the callee's public identity (TS 24.229 5.3.2.1, TS 29.228 6.1.4). The HSS answers DIAMETER_SUCCESS with the **Server-Name** of the S-CSCF where UE 2 is registered.

The I-CSCF puts that S-CSCF on top of Route and forwards the INVITE. It does not add Record-Route. The P-Charging-Vector goes on as it came; a request that comes without one, such as from a peer network outside the trust domain, gets an icid-value of the I-CSCF (TS 24.229 5.3.2.1).

If UE 2 were not registered, the HSS would answer DIAMETER_ERROR_IDENTITY_NOT_REGISTERED and the I-CSCF 480. An unknown user gives DIAMETER_ERROR_USER_UNKNOWN and 404.

A user who is not registered but has services for that state, an iFC with the session case TERMINATING_UNREGISTERED such as the call forwarding of the MMTel AS ([Supplementary services with the MMTel AS](mmtel.md)), gets DIAMETER_UNREGISTERED_SERVICE (2003) with capabilities instead (TS 29.228 6.1.4.1). The I-CSCF then picks an S-CSCF as for a first registration, and that S-CSCF takes the user profile with a SAR of UNREGISTERED_USER and runs those services (TS 24.229 5.4.3.3). When none of them takes the call, the S-CSCF answers 480. Every test subscriber of `deploy/compose/hss.yaml` has such a service: the iFC of the MMTel AS for TERMINATING_UNREGISTERED. Unless call forwarding is set up, the AS sends the INVITE back unchanged, and the S-CSCF answers 480.

### 6. The callee's S-CSCF sends the INVITE along the Path

The top Route has no `orig`, so this time the S-CSCF serves the callee (TS 24.229 5.4.3.3). It finds UE 2's registration. It keeps the P-Asserted-Service that the caller's S-CSCF put there, because UE 2 subscribes to MMTel (step 3C). The iFC of the MMTel AS sends the INVITE to the AS again, now with `sescase=term` (step 4), and the AS sends a new INVITE back. Then the S-CSCF:

- record-routes,
- adds **P-Called-Party-ID** with the address that was called,
- sets the Request-URI to the **contact** UE 2 registered,
- puts the **Path** from UE 2's REGISTER into Route, which brings the INVITE to the P-CSCF that UE 2 registered through.

The same S-CSCF node plays both parts here because it serves both users. The capture shows the INVITE leaving the S-CSCF for the I-CSCF and coming back.

### 7. The P-CSCF delivers the INVITE to UE 2

The top Route is the P-CSCF's Path URI with the user part `term`, so this pass is the callee's side (TS 24.229 5.2.6.4.3). The P-CSCF:

- removes its Route and record-routes,
- creates a **second relay context** for this side of the call. The core termination sends to the address in the offer, which is the first context's core termination. The offer is rewritten to name the new context's access termination.
- sends the INVITE to the address UE 2's REGISTER came from, not to its Contact. Behind NAT, only the former reaches the UE.

### 8. UE 2 answers the offer: a reliable 183

UE 2 answers with **183 Session Progress** that carries its SDP answer (RFC 3264 6.1): one codec, PCMU, with its telephone-event, and its own address and port. Because the INVITE said `Supported: 100rel`, the 183 is reliable (RFC 3262 3): it has **Require: 100rel** and an **RSeq**, and UE 2 sends it again until a PRACK arrives.

The 183 also has **Require: precondition**: UE 2 uses the mechanism because the INVITE supports it (TS 24.229 5.1.4.1). Its SDP shows the precondition table of both ends. Here both UEs have their resources already, so every mandatory precondition is met, and UE 2 asks UE 1 for no confirmation (no `a=conf` line).

The 183 creates the **early dialog**: it carries a To tag, Contact and the Record-Route list.

On the way back, each P-CSCF pass rewrites the answer: the callee's side points the access termination at UE 2's answer and names its core termination in the SDP; the caller's side points the core termination at that answer, the core termination of context 2, and names its access termination in the SDP, so that UE 1 sees its own context's access termination. In the end, each UE talks only to the relay.

The callee's side also asserts who answered. It removes any P-Asserted-Identity or P-Preferred-Identity that UE 2 put in the 183, the 180 and the 200, and puts in **P-Asserted-Identity** with the identity of the P-Called-Party-ID of step 6 (TS 24.229 5.2.6.4.4 step 1). That is the identity that UE 1 sees as the one who answered (TIP). When a response carries `Privacy: id`, the caller's side removes the P-Asserted-Identity before the response reaches UE 1, because the UE is outside the trust domain (RFC 3325 7, TS 24.229 4.4.2).

### 9. UE 1 acknowledges the 183: PRACK

UE 1 sends **PRACK** with **RAck** (the RSeq, and the CSeq of the INVITE) inside the early dialog (RFC 3262 4). It is a request within the dialog, so it follows the route set built from Record-Route (RFC 3261 12.2.1.1): the P-CSCF and the S-CSCF, to the caller's MMTel AS at the far end of the dialog. Each AS answers the PRACK at once, as a UA does, and sends a PRACK of its own on its next dialog, so a PRACK reaches UE 2 dialog by dialog, and UE 2 answers it with 200. With preconditions, the UPDATE goes the same way.

### 10. UE 2 rings, then answers: 180 and 200 OK

Once every mandatory precondition is met, UE 2 alerts its user and sends **180 Ringing** (RFC 3312 6, TS 24.229 5.1.4.1). The 180 is reliable too and gets a PRACK of its own. A callee whose resources are not there yet sends no 180 until they are (IR.92 2.4.1).

When the user answers, UE 2 sends **200 OK** to the INVITE (RFC 3261 13.3.1.4). The dialog is now confirmed. UE 2 sends the 200 again, with growing intervals, until the ACK arrives.

The 200 carries no SDP: the answer in the reliable 183 is the answer (RFC 3262 5). UE 1 keeps the first answer it receives and ignores any later one (RFC 3261 13.2.1).

### 11. UE 1 confirms: ACK

UE 1 sends the **ACK** for the 2xx along the route set (RFC 3261 13.2.2.4). It is not part of the INVITE transaction; each proxy forwards it as its own request.

### 12. The voice flows through the relay

Each UE sends RTP every 20 ms: 160 bytes of G.711 μ-law per packet (RFC 3550 5.1, RFC 3551 4.5.14), from the same port it receives on (symmetric RTP, RFC 4961).

The path of UE 1's audio is:

```text
UE 1 -> relay access (context 1) -> relay core (context 1) -> relay core (context 2) -> relay access (context 2) -> UE 2
```

The termination towards the UE **latches**: it learns where to send from the first packet it receives, instead of trusting the SDP. A UE behind NAT sends from an address its SDP cannot know, and latching still reaches it.

The termination towards the network does not latch, and sends where the latest SDP of the network side says. Nothing on that side is behind NAT, and the party there can change within a call: when a customized alerting tone ends, the callee's SDP takes the place of the MRFP's ([Announcements, alerting tones and conferences with the MRF](mrf.md)). A termination that had latched onto the MRFP would go on sending the caller's voice there.

Every few seconds each UE also sends an **RTCP** compound packet from the port above its RTP port: a sender report, or a receiver report while it sends nothing, and an SDES with its CNAME (RFC 3550 6, IR.92 3.2.4). It sends one at least every 5 seconds, so that the other side can tell that the call is still up. At the end of the call it sends an RTCP BYE.

The e2e test checks that UE 2 received exactly the bytes UE 1 encoded. G.711 is lossy, so the WAV files themselves are not compared.

### 13. UE 1 hangs up: BYE

UE 1 sends **BYE** within the dialog (RFC 3261 15.1.1), along the route set. The BYE carries `Reason: RELEASE_CAUSE;cause=1;text="User ends call"`, which tells why the call ended (TS 24.229 5.1.5, IR.92 2.2.4). UE 2 answers 200 (RFC 3261 15.1.2). A request of UE 1 in the dialog that UE 2 has not answered yet, such as a re-INVITE, gets 487 (Request Terminated) then, and so does a request of UE 2 that UE 1 has not answered when it sends the BYE (RFC 3261 15.1.2). When the 200 passes the P-CSCF, the P-CSCF removes the relay contexts of the call. It waits for the answer because a BYE can be turned down, such as with 500 for a BYE out of order (RFC 3261 12.2.2), and then the dialog stays up. A 481, a 408 or no answer at all ends the dialog as well (RFC 3261 15.1.1), so they also remove the contexts.

## One charging identifier for the call

Inside the network, the INVITE and every response to it carry a **P-Charging-Vector** (RFC 7315 4.6). Its **icid-value** names the call for charging. The P-CSCF of UE 1 writes a new one into the INVITE (TS 24.229 5.2.6.3.3 step 7), and the nodes after it pass it on; the MMTel AS puts it into the INVITE of each of its dialogs (5.7.5.1). Its **orig-ioi** names the network that sent the request, and its **term-ioi** the network that sent the response.

Each node writes its own network into the IOIs. The S-CSCF writes its home domain as the orig-ioi of the INVITE that it sends to the MMTel AS and on (TS 24.229 5.4.3.2 steps 5 d and 7, 5.4.3.3 steps 4 d, 7 and 7A). Into the responses that it passes back, it writes the orig-ioi that the INVITE brought and its home domain as the term-ioi. In this one network every IOI is the same domain; in [Connect two IMS networks](interconnect.md) the two networks write different ones.

A UE never sees the header field: the P-CSCF removes it from what goes to the UE and from what comes from it (5.2.1). Before it removes it from the INVITE to UE 2, the P-CSCF of UE 2 stores the icid-value and the orig-ioi (5.2.6.4.3 item 9). It writes them, with its own term-ioi, into UE 2's 183, 180 and 200 (5.2.6.4.4 item 6), and gives each later request of UE 2 in the dialog the icid-value of the INVITE (5.2.6.3.9 step 3). The P-CSCF of UE 1 gives the same icid-value to UE 1's PRACK, ACK and BYE.

To follow the call, copy the icid-value of the INVITE that the P-CSCF sends to the S-CSCF (step 2), and filter with `sip.P-Charging-Vector contains "icid-value=..."`. The INVITE and its responses show up at every node and in all three dialogs.

## Talk from your own machine

The P-CSCF port 5060/udp and the relay's ports 40000 to 40039/udp are published on the host's loopback address, 127.0.0.1. A softphone on the host, such as Linphone, can register with the SIP Digest credentials of a test subscriber, with the P-CSCF at `127.0.0.1:5060` and the domain `ims.mnc001.mcc001.3gppnetwork.org`.

The P-CSCF recognizes a UE that comes from the bridge gateway 172.28.0.1 and gives it the relay's address as `127.0.0.1`, where the ports are published (`host_access` in `deploy/compose/pcscf.yaml`). Latching takes care of the address that Docker's NAT gives the softphone. A softphone on another machine would be told the same `127.0.0.1`, which is its own address, so the softphone runs on the host.

The call test of `scripts/e2e-call.sh` ends with a call over this path, wherever the machine can run the UE simulator of the container image: the simulator on the host calls UE 2, and UE 2 in its container receives every packet the host sent, through the published relay port. Only the simulator has been tried on this path, built for Windows too, and no softphone.

## Drive a UE over HTTP

`ue serve` keeps a UE registered and lets another program drive it. This is how the Lab makes and answers calls. The HTTP API is this project's own, of the kind a test tool offers for remote control. No specification defines it, and none of it goes to the IMS: the capture shows only the SIP that the UE sends as a result.

1. Start UE 2 and UE 1 in two terminals. Each one publishes its API port on the host:

   ```sh
   docker compose run --rm -p 127.0.0.1:8091:8090 ue2 serve \
     -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
     -password test-password-2
   ```

   ```sh
   docker compose run --rm -p 127.0.0.1:8090:8090 ue serve
   ```

2. Have UE 2 ring until you answer, call it from UE 1, and look at UE 2:

   ```sh
   curl -X PUT -H 'Content-Type: application/json' localhost:8091/answer-policy -d '{"mode": "manual"}'
   curl -X POST -H 'Content-Type: application/json' localhost:8090/call -d '{"target": "tel:+1-202-555-0102"}'
   curl localhost:8091/status
   ```

   The status of UE 2 shows an `incoming` call in the `ringing` state, with the same Call-ID as in the capture.

3. Answer on UE 2, then hang up on UE 1:

   ```sh
   curl -X POST -H 'Content-Type: application/json' localhost:8091/answer
   curl -X POST -H 'Content-Type: application/json' localhost:8090/hangup
   ```

   A hang-up sends a BYE once the call is answered, and a CANCEL while it rings. On the callee's side, a hang-up while the call rings turns it down with 603 (Decline), or with the `reject_status` in the body.

   A call that rings for more than a minute gets a 180 (Ringing) every minute, so that no proxy gives up on the INVITE (RFC 3261 13.3.1.1). A UE that stops while a call rings turns it down with 480 (Temporarily Unavailable).

During the call, `curl -X POST -H 'Content-Type: application/json' localhost:8090/hold` puts it on hold with a re-INVITE that offers `a=sendonly`, `/resume` takes it back, and `curl -X POST -H 'Content-Type: application/json' localhost:8090/dtmf -d '{"digits": "123"}'` sends DTMF as telephone events. The `session` in the status shows the codec, the directions, the preconditions and the digits that came. Every `POST` and `PUT` carries `Content-Type: application/json`, one without a body too: the API answers any other with 415, so that a page of another site in your browser cannot drive the UE without a preflight ([Open the Lab](lab.md#open-the-lab) explains this).

UE 1 plays its tone once and stays on the line until one side hangs up. `curl -N localhost:8091/events` follows the status as it changes. The answer policy can also be `{"mode": "auto", "ring_seconds": 2}`, which answers after two seconds, or `{"mode": "reject", "reject_status": 486}`, which turns every call down. Each UE has one line, which holds two calls at most. A second call goes out only once the first is on hold (`POST /hold`); until then the API answers 409 (Conflict). An INVITE that comes during a call waits: the UE rings with `Alert-Info: <urn:alert:service:call-waiting>` and does not answer by itself, and `POST /answer` puts the first call on hold and takes the waiting one (TS 24.615 4.5.5.3). A third INVITE gets 486 (Busy Here). `ue ctl` sends these requests from a shell. All the requests are listed in the comment of `Phone.Handler` in `internal/ue/phone_http.go`.

## Take a media relay out of service

An operator takes a node out of service without cutting what it serves: the node takes nothing new and goes on with what it has begun, until nothing is left. This is called a drain.

A media relay has to go on with what it carries, because a context lives in the node that opened its ports. The relay holds the UDP sockets of the call, the P-CSCF holds the context ID that this relay gave it, and the RTP of the call passes through this process and no other. A commercial network runs several relays, and the P-CSCF picks one per call from a hash of the Call-ID, so the new calls go to the others while this one drains. What the relay does is the graceful method of TS 23.334 6.1.2 for an IMS-AGW and of TS 23.333 6.1.2 for an MRFP: the node is locked for new calls, and the connections it holds are cleared as their calls are disconnected.

The node of the relay serves a maintenance API for this on port 8085, the listener of the `maintenance` section of `deploy/compose/agw.yaml`. The API asks for no password, so the port is not published on the host: send the requests from inside the container. The administrative state has the values of TS 28.622 5.3.1. `UNLOCKED` takes new work, `SHUTTINGDOWN` serves only what it has, and `LOCKED` takes nothing new.

1. Shut the relay down:

   ```sh
   docker compose exec agw sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/agw/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the relay. With no call going through it, it is `LOCKED` at once:

   ```json
   {"role":"agw","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` counts the contexts this relay holds: one for each pass of a call through a P-CSCF, one for each announcement, and one for each conference, whatever the number of terminations in it. The contexts that an earlier process of the relay left in its store, and that it opened again when it started, are among them. With a call going through it the shut down leaves the relay `SHUTTINGDOWN`, and it becomes `LOCKED` once the P-CSCF has subtracted the last of its contexts, which tells the operator that stopping the node would cut no media. `operational_state` says whether the relay can work (TS 28.622 5.3.3): it is `ENABLED` while the node runs, since the relay answers from its own sockets and holds no association whose state it could report. `docker compose exec agw wget -qO- http://127.0.0.1:8085/maintenance/v1/state` shows the whole node.

2. Call UE 2 from UE 1:

   ```sh
   docker compose run --rm ue call tel:+12025550102
   ```

   The call fails with `500 Server Internal Error`, because this network has one relay and the P-CSCF has no other to put the media through.

```text
 UE 1          P-CSCF         media relay
 |   INVITE      |                 |
 |-------------->|                 |  the SDP offer of UE 1
 |               | POST /contexts  |  Add: one termination towards the UE, one towards the network
 |               |---------------->|
 |               | 503 Service Unavailable
 |               |<----------------|  out of service, so no context
 |   500         |                 |  no other relay in the list, and the INVITE goes no further
 |<--------------|                 |
```

- **The P-CSCF asks for a context** for this pass of the call, a termination towards the UE and one towards the network, as it does for every call with an offer in the INVITE (TS 29.334 5.17.2.2). It picks the relay from a hash of the Call-ID and knows nothing of the drain.
- **The relay answers 503 (Service Unavailable)** with the error code `out_of_service`, and reserves no port. The answer carries no `Retry-After`: the P-CSCF keeps no list of the relays that are unavailable, so nothing would read one.
- **The P-CSCF takes the next relay of its list**, and answers only once none is left. Here the list has one, so the call ends: the P-CSCF answers the INVITE 500 and forwards nothing. It sends no `Retry-After` of its own, since the 503 was about the relay and not about the P-CSCF.

While it is not `UNLOCKED`, the relay goes on with what it carries and refuses what would start something new.

It goes on with:

- **Every operation on a context it holds.** The `Modify` with which a controller points a termination at the other party, after the answer of the callee or a re-INVITE, and the `Subtract` with which it releases a termination or the whole context when the call ends. Without them a drained relay would hold its ports for good.
- **The `Add` of a termination to a context it holds**, with which a party joins a conference whose mix is in this node. The mix is here and nowhere else, so a refusal would take a party out of a conference that is in progress. It adds no context either: the relay counts the contexts, and their number only falls while it is shutting down.
- **The events it reports to its controller**, and the audits that read its contexts and its announcements. They say what the node holds, and the node is alive.

It answers 503 to the `Add` of a new context, the one operation that would start something new.

3. Unlock the relay with the same request and `UNLOCKED`, and call again:

   ```sh
   docker compose exec agw sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/agw/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue call tel:+12025550102
   ```

   The call goes through the relay again. The node of the MRFP is drained the same way, with `mrfp` in the place of `agw` in the path and in the service name; what its drain costs the MRFC is in [Take the MRFP out of service](mrf.md#take-the-mrfp-out-of-service).

In Wireshark, `http.request.uri == "/contexts" && http.request.method == "POST"` shows each request for a context, and `http.response.code == 503 && ip.src == 172.28.0.50` the refusals of the drained relay. The PUT request travels over the loopback interface of the container, so the capture does not hold it.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-sip.sh` runs these steps and checks the capture.

## When the relay does not take an answer

The P-CSCF points every SDP answer at the relay before it passes the answer on (steps 8 and 10). It first tells the termination that faces the answering party where that party receives, with a `Modify` (TS 29.334 5.17.2.3), and only then writes the address of its other termination into the SDP (TS 24.229 6.7.2.1). A relay that is restarting, or that has lost the context, refuses the `Modify` or does not answer it in time. The P-CSCF then has no answer of its own to pass on, and the answer that came names the address of the far party. A UE that got it would send its RTP past the relay, and the PCRF would authorise media on a path that the call does not take. So the P-CSCF ends the call. It does the same with an answer that it cannot read: SDP that does not parse, a body without `Content-Type`, which RFC 3261 20.15 forbids, and an SDP in a part of a multipart body.

TS 24.229 names no procedure for a media relay that fails. The P-CSCF ends the call with the steps it takes when the network takes away the resources of a session that is being set up (TS 24.229 5.2.8.1.1), with one difference: its 500 carries no `Reason: FAILURE_CAUSE`, which tells of lost radio or bearer resources (TS 24.229 7.2A.18.12.2). The CANCEL and the BYE carry `Reason: SIP;cause=503` (RFC 3326 2). TS 24.229 5.2.8.1.1 and 5.2.8.1.2 use this value when the resources of a session are gone and the entity that controls the radio or bearer resources gave no cause of its own. The relay is not that entity, and the radio and bearer resources are still there, so the P-CSCF has no cause to pass on and uses the same 503.

When the answer comes in a 183, the call is still being set up:

```text
 UE 1          P-CSCF          media relay      network
 |               |  183 with the SDP answer         |
 |               |<---------------------------------|
 |               | PUT .../terminations/core        |  Modify: where the callee receives
 |               |--------------->|                 |
 |               | error, or no answer in time      |
 |               |<---------------|                 |
 |   500         |                |                 |  the 183 goes no further
 |<--------------|                |                 |
 |               | CANCEL  Reason: SIP;cause=503    |
 |               |--------------------------------->|
 |               |      200 (CANCEL), 487 (INVITE)  |
 |               |<---------------------------------|
 |               | DELETE /contexts/{id}            |  Subtract
 |               |--------------->|                 |
```

- **UE 1 gets 500** for its INVITE, and never sees the 183.
- **The INVITE towards the network is cancelled**, and the callee answers it 487 (RFC 3261 9.2).
- **The P-CSCF removes the relay context**, trying again a few times when the relay does not answer. The PCRF never gets the answer that the relay did not take. When an earlier response of the call carried an answer that the relay did take, the PCRF has that one, and the P-CSCF ends the Rx session of the call (TS 29.214 4.4.4).
- **A retransmission of a reliable 183 whose answer the relay did take changes nothing.** The callee sends a reliable 183 again until its PRACK comes (RFC 3262 3). UE 1 has that 183 already, so the P-CSCF sends it the retransmission as it sent the first 183, with the SDP that names the relay, and the call goes on. An SDP whose version has not changed has to be the same SDP (RFC 3264 8), and the relay is not asked again.

When the answer comes in the 200, the callee has answered already:

```text
 UE 1          P-CSCF          media relay      network
 |               |  200 (INVITE) with the SDP answer|
 |               |<---------------------------------|
 |               | PUT .../terminations/core        |  Modify
 |               |--------------->|                 |
 |               | error, or no answer in time      |
 |               |<---------------|                 |
 |   500         |                |                 |  in the place of the 200
 |<--------------|                |                 |
 |               | ACK, in the name of UE 1         |
 |               |--------------------------------->|
 |               | BYE  Reason: SIP;cause=503       |
 |               |--------------------------------->|
 |               |                      200 (BYE)   |
 |               |<---------------------------------|
 |               | DELETE /contexts/{id}            |  Subtract
 |               |--------------->|                 |
```

- **UE 1 gets 500 in the place of the 200.** The INVITE of UE 1 has no final response yet, so the P-CSCF can still end the call there, and the 200 with an SDP that leads past the relay never reaches UE 1.
- **The callee's dialog is confirmed**, so the P-CSCF acknowledges the 200 in the name of UE 1 (RFC 3261 13.2.2.4) and ends the session with a BYE (RFC 3261 15), as for a 200 that crossed its CANCEL. The callee sends the 200 again until the ACK comes, and each retransmission gets the ACK again.
- **What the call holds goes as for a 183:** the relay context, and the Rx session of an answer that an earlier response carried.
- **A retransmission of a 200 whose answer the relay did take is different.** UE 1 has that 200 already, so the P-CSCF sends it the retransmission as it sent the first 200, and the call stays up.
- **A 200 with another To tag is no retransmission.** It is the 200 of another dialog of a forked INVITE, after UE 1 has the 200 of the first. TS 24.229 names no procedure for it. A proxy forwards every 200 to an INVITE (RFC 3261 16.7 step 5). But the call has one relay context and one Rx session, and both serve the dialog of the 200 that UE 1 has. The answer of the other dialog cannot go through them without moving the media of UE 1's call, and as it came it would lead UE 1 past the relay. So that 200 goes no further, whether or not the relay would take its answer, and the P-CSCF does not ask the relay. A 200 that goes no further comes again until the callee gives up waiting for its ACK and ends the dialog itself (RFC 3261 13.3.1.4). So the P-CSCF ends that dialog at once: it acknowledges the 200 in the name of UE 1 and sends a BYE, as for a 200 that crossed its CANCEL. UE 1 keeps the call of its own 200, with its relay context, its Rx session and its dialog as they were. `TestA2xxOfAnotherForkLeavesTheCallAsItIs` in `internal/pcscf` shows it with a relay that takes every answer.
- **A request within the other dialog leaves UE 1's call alone.** The P-CSCF keeps the dialog of UE 1 under the Call-ID and the tag of UE 1, and a request within the other dialog carries both. Once UE 1 has its 200, the P-CSCF tells the two dialogs apart by the tag of the other party, and such a request changes nothing of UE 1's call. A BYE goes on, and its answer, a 481 too, ends only the other dialog (RFC 3261 15.1.1). An SDP in such a request could only go through the relay context of UE 1's call, so the P-CSCF answers the request 500 itself. `TestRequestOfAnotherForkLeavesTheCallAsItIs` shows these requests.

At the P-CSCF of UE 2 the same happens the other way round. The 183 or the 200 comes from UE 2, the `Modify` is for the access termination, the 500 goes to the network, and the CANCEL, or the ACK and the BYE, go to UE 2.

An answer within the dialog, in the 200 to an UPDATE or to a re-INVITE such as the one of a hold, goes another way. That 200 is the final response of its request, and the party that answered has taken the offer already, so the P-CSCF lets the 200 go on as it came. It then releases the dialog. TS 24.229 names no procedure for this either. The nearest is TS 24.229 6.2, for a 200 that carries an SDP offer with media that the local policy does not allow: the P-CSCF passes that 200 on, and once the ACK with the answer has come, it releases the session with BYEs (TS 24.229 5.2.8.1.2). Those BYEs carry `Reason: SIP;cause=488` (Not Acceptable Here), since the parties asked for media that the network does not allow. Here they asked for nothing wrong, and the BYEs carry the 503 of lost resources instead:

```text
 UE 1          P-CSCF          media relay      network
 |               |  200 (UPDATE) with the SDP answer|
 |               |<---------------------------------|
 |               | PUT .../terminations/core        |  Modify
 |               |--------------->|                 |
 |               | error, or no answer in time      |
 |               |<---------------|                 |
 |  200 (UPDATE) |                |                 |  as it came
 |<--------------|                |                 |
 |               | BYE  Reason: SIP;cause=503       |  in the name of UE 1
 |               |--------------------------------->|
 |  BYE  Reason: SIP;cause=503    |                 |  in the name of the other party
 |<--------------|                |                 |
 |               | DELETE /contexts/{id}            |  Subtract
 |               |--------------->|                 |
```

- **In a confirmed dialog, each party gets a BYE** (TS 24.229 5.2.8.1.2), the same two BYEs as when [the network takes the bearer](volte.md#the-network-takes-the-bearer).
- **No BYE overtakes the 200.** After an UPDATE the BYEs go once the P-CSCF has passed the 200 on. The 200 to a re-INVITE waits for its ACK, and so does the release, as in TS 24.229 6.2: the BYEs go once the ACK of UE 1 has passed the P-CSCF. When no ACK comes, they go after 64*T1, 32 seconds, when the callee too gives up waiting for the ACK (RFC 3261 13.3.1.4). When the answer came in a 183 to the re-INVITE, and the re-INVITE then fails, no ACK passes the P-CSCF at all, as the ACK of a final response other than 2xx goes hop by hop (RFC 3261 17.1.1.3), and the BYEs go once that response has gone.

```text
 UE 1          P-CSCF          media relay      network
 |               |  200 (INVITE) with the SDP answer|  to the re-INVITE
 |               |<---------------------------------|
 |               | PUT .../terminations/core        |  Modify
 |               |--------------->|                 |
 |               | error, or no answer in time      |
 |               |<---------------|                 |
 |  200 (INVITE) |                |                 |  as it came
 |<--------------|                |                 |
 |  ACK          |                |                 |
 |-------------->| ACK                              |
 |               |--------------------------------->|
 |               | BYE  Reason: SIP;cause=503       |  in the name of UE 1
 |               |--------------------------------->|
 |  BYE  Reason: SIP;cause=503    |                 |  in the name of the other party
 |<--------------|                |                 |
```

- **In an early dialog**, such as that of the UPDATE with which a VoLTE UE tells that its resources are reserved ([Preconditions](volte.md#preconditions)), the INVITE has no final response yet. UE 1 gets 500 for its INVITE, and the INVITE towards the network is cancelled, as for a 183.
- **When the UPDATE passed another instance of the P-CSCF than the INVITE**, that instance cannot end the INVITE: only the one that forwards the INVITE can answer and cancel it. It frees what the dialog holds and marks the dialog released in the store that the instances share. The instance that forwards the INVITE finds the mark with the next response to the INVITE, and ends the call there, as for a 183 whose answer the relay did not take. When that response is the 200, the network gets the ACK and the BYE instead of the CANCEL.

```text
 UE 1          P-CSCF a        P-CSCF b         network
 | UPDATE        |                |                 |
 |------------------------------->| UPDATE          |
 |               |                |---------------->|
 |               |                |    200 (UPDATE) |  the relay does not take its answer
 |               |                |<----------------|
 | 200 (UPDATE)  |                |                 |  b marks the dialog released
 |<-------------------------------|                 |
 |               |                     180 (INVITE) |  a finds the mark
 |               |<---------------------------------|
 | 500           |                |                 |  the 180 goes no further
 |<--------------|                |                 |
 |               | CANCEL  Reason: SIP;cause=503    |
 |               |--------------------------------->|
```

- **The PCRF never gets the answer.** The P-CSCF ends the Rx session of the call (TS 29.214 4.4.4) and removes the relay context.

The tests of `internal/pcscf` run these flows in one process, with a relay that refuses the `Modify` of the answer: `TestRefusedAnswerOfA183EndsTheCall`, `TestRefusedAnswerOfThe200EndsTheCall`, `TestRefusedRetransmissionOfThe200KeepsTheCall`, `TestRefusedRetransmissionOfAReliable183KeepsTheCall`, `TestResponseWhoseSDPTheP_CSCFCannotReadEndsTheCall`, `TestRefusedAnswerWithinTheDialogReleasesTheCall`, `TestRefusedAnswerOfAReINVITEThatFailsIsReleasedAtOnce`, `TestRefusedAnswerOfAnUPDATEInTheEarlyDialogEndsTheCall` and, with two instances of the P-CSCF on one store, `TestRefusedAnswerOfAnUPDATEAtAnotherInstanceEndsTheCall`.

## When the relay does not take an offer

A new SDP within the dialog goes through the relay like the offer of the INVITE. That SDP may be the offer of an UPDATE, or of the re-INVITE of a hold. It may also be the SDP of a PRACK, which is a new offer or the answer to an offer in the reliable response that the PRACK acknowledges (RFC 3262 5). The P-CSCF tells the termination that faces the sending party where that party now receives, with a `Modify`, and then writes the address of its other termination into the SDP (TS 24.229 6.7.2.1). When the `Modify` fails, the P-CSCF has no SDP of its own to pass on. The SDP that came names the address of the sending party. The other party would send its RTP there, past the relay, and the PCRF would authorise media on a path that the call does not take.

So the request goes no further, and the P-CSCF answers it itself. TS 24.229 6.2 does the same with a request whose offer the P-CSCF cannot let through: the P-CSCF answers that request and does not forward it. For an offer with media that the local policy does not allow, 6.2 gives the answer 488 (Not Acceptable Here), with the media that the policy allows. Here the media are allowed and the relay is at fault, so the answer is 500 (Server Internal Error, RFC 3261 21.5.1). An SDP that the P-CSCF cannot read at all gets 400 (Bad Request, RFC 3261 21.4.1), and so does a body without `Content-Type`, which RFC 3261 20.15 forbids, as the P-CSCF cannot tell whether it is SDP. An SDP in a part of a multipart body gets 415 (Unsupported Media Type, RFC 3261 21.4.13) with `Accept: application/sdp`, as a node that does not read multipart bodies answers (RFC 5621 4.2). The relay is not asked in any of these cases, and an INVITE that carries such a body gets the same answers. Only the requests that carry offers and answers are read this way: INVITE, ACK, UPDATE and PRACK (RFC 3261 13.2.1). The body of any other request, such as an INFO, goes on as it came.

What happens to the call depends on what the relay answered.

**The relay turns the `Modify` down and changes nothing.** The call stays up:

```text
 UE 1          P-CSCF          media relay      network
 | UPDATE with a new SDP offer    |                 |
 |-------------->|                |                 |
 |               | PUT .../terminations/access      |  Modify: where UE 1 now receives
 |               |--------------->|                 |
 |               | error: nothing changed           |
 |               |<---------------|                 |
 |  500 (UPDATE) |                |                 |  the UPDATE goes no further
 |<--------------|                |                 |
```

- **A final response other than 2xx leaves the session as it was before the request.** RFC 3261 14.1 says so for a re-INVITE, and RFC 3311 5.3 for an UPDATE. Only a 481, a 408 or no answer at all ends the dialog (RFC 3261 12.2.1.2), so the P-CSCF answers with none of them. The relay has changed nothing, so the media goes on as the last offer and answer set it up, through the relay, and the PCRF keeps what it authorised.
- **A re-INVITE gets the same 500.** UE 1 acknowledges it with an ACK that goes no further than the P-CSCF, as for any final response other than 2xx (RFC 3261 17.1.1.3). A UE that wanted to put the call on hold stays in the call as it was.
- **UE 1 may send the offer again later** (RFC 3261 21.5.1).
- **An offer from the network** takes the `Modify` of the core termination. The 500 then goes back to the network, and UE 1 gets nothing.
- **In an early dialog**, such as that of the UPDATE with which a VoLTE UE tells that its resources are reserved ([Preconditions](volte.md#preconditions)), the INVITE goes on. The callee has not heard that the resources of UE 1 are reserved, and waits for a later offer that tells it so (RFC 3312 6).

**The relay does not answer in time, has lost the context, or is not in the P-CSCF's list.** The P-CSCF answers 500 and then releases the call, as when the relay does not take an answer:

```text
 UE 1          P-CSCF          media relay      network
 | UPDATE with a new SDP offer    |                 |
 |-------------->|                |                 |
 |               | PUT .../terminations/access      |  Modify
 |               |--------------->|                 |
 |               | no answer in time, or no context |
 |               |<---------------|                 |
 |  500 (UPDATE) |                |                 |
 |<--------------|                |                 |
 |               | BYE  Reason: SIP;cause=503       |  in the name of UE 1
 |               |--------------------------------->|
 |  BYE  Reason: SIP;cause=503    |                 |  in the name of the other party
 |<--------------|                |                 |
 |               | DELETE /contexts/{id}            |  Subtract
 |               |--------------->|                 |
```

- **A relay that answers too late may have carried the `Modify` out.** Its termination would then send to the address of the offer that UE 1 got 500 for, and UE 1 no longer takes media there. The P-CSCF cannot tell, so it ends the call.
- **A relay that has lost the context**, such as one that restarted without its store, carries no media of the call any more, and would turn every later offer down in the same way.
- **The P-CSCF ends the Rx session** of the call (TS 29.214 4.4.4) and removes the relay context, as after every release.
- **In an early dialog** the INVITE has no final response yet. UE 1 gets 500 for its INVITE as well, and the INVITE towards the network is cancelled, as for a 183 whose answer the relay did not take.

**The request is a PRACK.** The P-CSCF answers 500 and ends the INVITE, whatever the relay answered. A UAC does not send a PRACK again (RFC 3262 4). Its reliable response would stay unacknowledged, and the callee ends the INVITE with a 5xx once the response has waited 64*T1, 32 seconds, for its PRACK (RFC 3262 3). The P-CSCF ends the INVITE at once instead: UE 1 gets 500 for its INVITE, and the network a CANCEL. The PRACK gets the 500 too, not the 200 that RFC 3262 3 asks for a PRACK that matches its reliable response. Every INVITE whose call goes through the relay has the offer, and once the callee has answered it, no response to the INVITE carries an offer (RFC 3261 13.2.1). The SDP of the PRACK is then a new offer, and a 200 to it would have to carry the answer (RFC 3262 5), which the P-CSCF does not have.

TS 24.229 releases a dialog for an offer only when the offer comes in a response (5.2.8.1.2): 6.2 has the P-CSCF pass a 200 with such an offer on, since nobody can turn a 200 down, and release the session once the ACK has come. An answer in a response cannot be turned down either, which is why the P-CSCF releases the call when the relay does not take an answer in a response. An answer in a request is different: 6.2 has the P-CSCF answer 488 to the request that carries the answer to an offer from a provisional response. Here that request is a PRACK, which ends the call as above.

The tests of `internal/pcscf` run these flows in one process, with a relay that fails the `Modify` of the SDP: `TestRefusedOfferWithinTheDialogIsRejected`, `TestRefusedOfferOfAnUPDATEInTheEarlyDialogKeepsTheCall`, `TestOfferWithinTheDialogEndsTheCallWhenTheRelayMayHaveTakenIt`, `TestMalformedOfferWithinTheDialogGets400`, `TestINVITEWhoseSDPTheP_CSCFCannotReadGoesNoFurther`, `TestUPDATEWhoseSDPTheP_CSCFCannotReadGoesNoFurther` and `TestRefusedSDPOfAPRACKEndsTheCall`.

## When the far end turns an offer down

The relay may take the offer of a request within the dialog, and a node further on may still turn the offer down. UE 2 answers 488 (Not Acceptable Here) to an offer that it cannot take, and the P-CSCF of UE 2 answers 500 to one that its own relay does not take, as above. The request then gets a final response other than 2xx, and the session stays as it was before the request (RFC 3261 14.1, RFC 3311 5.3): UE 1 goes on taking its media where the last offer and answer said. The P-CSCF told the access termination where the offer said before it passed the offer on. So it has the termination send where the last offer and answer said again, with another `Modify`, before it passes the response to UE 1:

```text
 UE 1          P-CSCF          media relay      network
 | re-INVITE with a new SDP offer |                 |
 |-------------->|                |                 |
 |               | PUT .../terminations/access      |  Modify: where UE 1 would receive
 |               |--------------->|                 |
 |               | re-INVITE                        |
 |               |--------------------------------->|
 |               |                     488 (INVITE) |  UE 2 turns the offer down
 |               |<---------------------------------|
 |               | PUT .../terminations/access      |  Modify: where UE 1 received before
 |               |--------------->|                 |
 |  488 (INVITE) |                |                 |
 |<--------------|                |                 |
```

- **The `Modify` goes before the response.** UE 1 may send a new offer right after the 488, and that offer then finds the relay as it was.
- **Any final response other than 2xx does the same**, and so does no answer at all, for which UE 1 gets 504 (Server Time-out). That holds for an UPDATE, for a re-INVITE such as the one of a hold, and for a PRACK with a new offer.
- **An offer from the network** has the core termination go back, and the 488 of UE 1 goes to the network.
- **Each termination goes back to the last offer and answer that were done:** those of the INVITE, or those of a later request within the dialog that got its 2xx. The P-CSCF keeps them with the dialog, in the store that its instances share, so the request may pass another instance than the requests before it.
- **When the offer does not move the media**, as with a hold that keeps the port, the termination already sends where it did, and the P-CSCF sends no second `Modify`. The offer reaches the P-CSCF of UE 2 with the address of the relay of UE 1, which no offer of UE 1 changes, so only the P-CSCF of UE 1 puts its relay back.
- **When the answer came in a reliable 183** to the re-INVITE, and the re-INVITE then fails, that offer and answer are done. RFC 6141 3.3 asks a UAS that has carried such changes out to answer the re-INVITE with a 2xx, and a UAC that gets an error response all the same sends a new offer, so that both parties have the same view of the session (RFC 6141 3.4). The P-CSCF leaves the relay as that answer set it, and the new offer goes through the relay like any other.
- **When the answer only showed in a 183 that is not reliable**, and the re-INVITE then fails, there was no answer. The answer is the one in a 2xx or in a reliable provisional response; the same answer may also come earlier in a provisional response (RFC 3261 13.2.1). The relay sent to the far end where that 183 said, so when the re-INVITE fails, both terminations go back where the last offer and answer said. `TestRejectedOfferAfterAPreviewPutsBothSidesBack` shows it.
- **A re-INVITE that UE 1 cancels** ends the same way. UE 1 gives the change up before it has the answer, because the resources of the new session could not be reserved, and cancels its re-INVITE (RFC 3261 9.1, TS 24.229 6.1.4.2 item b). The P-CSCF is a proxy, so when the CANCEL arrives it cancels the branch it forwarded and passes the Reason of that CANCEL on (RFC 3261 16.10, RFC 3326 2). A far end that has made no change of the session answers 487 (Request Terminated), which is a final response other than 2xx like any other, so the relay goes back where the last offer and answer said. `TestCancelledReInvitePutsTheRelayBack` shows it.
- **When the relay does not take the change back**, the access termination keeps sending where UE 1 no longer receives. The P-CSCF then releases the call, as when the relay does not take an answer: once the 488 has gone, each party gets a BYE with `Reason: SIP;cause=503`. In an early dialog, UE 1 gets 500 for its INVITE, and the INVITE towards the network is cancelled.

The tests `TestRejectedOfferPutsTheRelayBack`, `TestRejectedOfferGoesBackToTheLastOfferAndAnswer` and `TestRejectedOfferEndsTheCallWhenTheRelayDoesNotGoBack` of `internal/pcscf` run these flows in one process.

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| S-CSCF, TS 24.229 5.4.3.2 and 5.4.3.3 | When the S-CSCF decides whether a failed application server gets the default handling, a 100 (Trying) from the server does not count as a 1xx. | Proxy keeps a 100 to the hop it came over (RFC 3261 16.7 step 5). |
| S-CSCF, TS 24.229 5.4.3.2 | The term-ioi that a response to a request outside a dialog brings is not stored. | It names the network that answered in the charging records of the S-CSCF, which no node of this project writes, and no later message carries it. |
| S-CSCF, TS 24.229 5.4.3.2 steps 4C and 4D, 5.4.3.3 steps 3C and 3D | The user profile lists no subscribed services, so every user subscribes to MMTel. A request that matches no subscribed service is never answered 403. | The HSS keeps only iFCs for a user, and MMTel is the only service of this project with an ICSI. |
| ENUM, RFC 6116 5.2.1 | Non-terminal NAPTR records are skipped. | The project's zone holds terminal records only. |
| ENUM, RFC 3402 3.2 | A delimiter escaped with a backslash inside the NAPTR regexp is not supported; such a record is refused as malformed, or split in the wrong place. | The project's records use `!` and never escape it. |
| DNS, RFC 3263 4.2 | A SIP server is looked up by its A records only, not by AAAA, so a server that has only an IPv6 address is not found. | Every node of this project has an IPv4 address. |
| I-CSCF, TS 24.229 5.3.2.1, 5.3.2.1A and 5.3.2.2 | No "orig" routing for an application server that sends on behalf of a user, and no reselection of another S-CSCF. | The one application server that sends on behalf of someone, the conference focus on behalf of a conference URI, sends to the S-CSCF, and this network has no redundant S-CSCF to choose. |
| I-CSCF, TS 24.229 5.3.2.1 | The I-CSCF does not check whether a request comes from a trusted domain, so it never removes P-Charging-Vector and P-Charging-Function-Addresses for that reason. It stores neither the icid-value of a request nor the term-ioi of a response. | In the networks that this project sets up, every node that sends to the I-CSCF belongs to its own network: another network reaches it only through the IBCF, which removes both header fields from the requests of an untrusted peer as they enter (5.10.3.2), and a UE only through its P-CSCF. The values are for the charging records of the I-CSCF, which no node of this project writes. |
| P-CSCF, S-CSCF and I-CSCF, RFC 3261 16.10 | A CANCEL that matches no transaction, and that the node cannot forward, is dropped with no response. At the S-CSCF and the I-CSCF, that is a CANCEL outside a dialog that has no Route left once the node has taken its own. At all three, it is also a CANCEL whose Max-Forwards is used up (16.3 step 3), or whose next hop DNS does not find. | The S-CSCF sent the INVITE that such a CANCEL would cancel where the filter criteria and the registration of the served user said, and the I-CSCF sent it to the S-CSCF that the HSS named. The CANCEL alone tells neither. Both forwarded that INVITE statefully, with a branch of their own, so no node further on would match the CANCEL to it either. Max-Forwards runs out only in a loop, and DNS lacks a next hop only when the configuration is broken. |
| P-CSCF, TS 24.229 5.2.6.4.4 step 1 and 5.2.6.4.8 step 2 | The P-Asserted-Identity of the UE that answers carries no display name. | The user profiles of this project hold no display name, so the S-CSCF puts none in P-Associated-URI at registration, and the P-CSCF has none to add. |
| P-CSCF, TS 24.229 4.4.2 and RFC 3325 5 | The P-CSCF tells a request from the network by how it is addressed, since the UEs and the network reach it at the same address and port. Any host that reaches that port can send a request with the URI that the P-CSCF put in Path as its top Route and the contact of a UE as its Request-URI. The P-CSCF forwards it to the UE without an IP association, and the P-Asserted-Identity that the host wrote reaches the UE as one that the network asserts. | A real P-CSCF takes the requests of the network (Mw) on an interface of its own, which only the nodes of the network reach. The containers of this project share one network, and the security that a commercial network puts on that boundary is out of scope. |
| P-CSCF, TS 24.229 5.2.6.3.5 step 7 and 5.2.6.3.9 step 3 | A request that the UE sends in a dialog other than that of an INVITE, such as the refresh of its subscription, carries no P-Charging-Vector. A response of the UE in such a dialog takes the icid-value of the request that it answers. | The P-CSCF keeps only the dialogs of INVITEs, and with them the icid-value of the INVITE. |
| P-CSCF, RFC 3264 and RFC 3262 | An INVITE without an offer gets no relay context: the offer that then comes in a reliable 1xx or in the 2xx, and the answer in the PRACK or the ACK, are left as they are. An offer in an UPDATE or a re-INVITE goes through the relay like the one in the INVITE. | The UE simulator always puts its offer in the INVITE. |
| P-CSCF, RFC 3264 6 | Only the first audio stream with a port goes through the relay. Another stream gets no termination and keeps the address it was offered with, which the other side sees. | The UE simulator offers a single stream. |
| P-CSCF, RFC 5621 4.2 and TS 24.229 6.7.2.1 | An SDP in a part of a multipart body does not go through the relay. A request that carries one gets 415, and a response ends the call, as one whose answer the relay does not take. | A UA has to read multipart bodies, and the SDP of an emergency call comes in one, with the location of the UE. No node of this project sends a multipart body in an INVITE dialog. |
| P-CSCF, TS 24.229 5.2.8.1.1 | When a request within an early dialog, such as an UPDATE whose answer the relay does not take, passes another instance of the P-CSCF than its INVITE, the INVITE is answered 500 and cancelled only when its next response reaches the instance that forwards it: a provisional response or the 200, or a final response that ends the INVITE anyway. Without one, Timer C of that instance ends it (RFC 3261 16.8): 3 minutes 30 seconds here, as RFC 3261 16.6 step 11 asks for more than 3 minutes. Until then the INVITE goes on, without media through the relay. When that instance has stopped, the store forgets the dialog after the same time. | Only the instance that forwards an INVITE can answer and cancel it. The instances share one store and do not ask one another: the instance that releases the dialog marks it in the store, and the other finds the mark with the next response. In the preconditions of a VoLTE call, the 180 comes right after the 200 to the UPDATE. The PCRF sends the events of a session to the instance that opened it, so a lost bearer always reaches the instance that forwards the INVITE. |
| P-CSCF, RFC 3261 13.3.1.4 and 15 | A release that waits for the ACK of a 200 waits on a timer of the instance of the P-CSCF that started it. When that instance stops before the timer fires, and no ACK comes, the call keeps its relay context and its Rx session until a party ends it with a BYE. | The wait lasts 32 seconds at most, and the 200 it waits for has just passed that instance. An ACK that comes releases the call at whichever instance it passes. |
| P-CSCF, TS 23.334 6.1.2 and 6.1.3 | The P-CSCF does not keep track of the media relays that are unavailable, and does not audit them. Each new call tries the relay that its Call-ID picks, and goes on to the next one only when that one gives no context. The calls on a relay that never comes back keep their signalling, without media, until a UE hangs up: the P-CSCF neither ends them nor moves them to another relay. | A relay that comes back at its own address opens its contexts again, and the media of their calls goes on, so ending the calls would cut ones that recover. Moving a call to another relay would take a new offer from the P-CSCF to each party, which a proxy does not send. |
| UE, RFC 6141 3.3 | With preconditions, when the answer to the re-INVITE of a hold or a resume came in a reliable provisional response and the re-INVITE then fails, the UE takes the error as saying that the peer used none of the new parameters, and does not look at the media to tell. | With preconditions, that offer and answer only make the new parameters ready, and the peer uses them once it sends media with them (RFC 6141 3.3). Without preconditions the change was carried out, and the UE offers the session before the re-INVITE anew (RFC 6141 3.4). |
| UE, TS 24.229 5.1.3.1 b) and 5.1.4A.1 | The UE lists precondition in the Supported of every request within the dialog, also when the peer does not use preconditions. | The UA has one Supported for all its calls; a peer that does not use the mechanism ignores the option tag in Supported. |
| Media relay, RFC 4961 | The termination towards the UE keeps the address it latched for the whole call, unless the controller asks it to re-latch. RTP and RTCP latch separately. | Re-latching only matters when a UE moves in the middle of a call. A new party on the network side needs none, as the termination towards the network does not latch. |
| Media relay, H.248.37 6.6.7.1 | After a latch, the packets of other sources are passed on as well, rather than discarded. | The termination towards the UE latches, and the P-CSCF gives it the address of each new SDP without asking it to latch again. The filter would drop what a party sends from a new address. |
| Media relay, RFC 3605 and RFC 5761 | RTCP is taken to arrive on the port above RTP; a=rtcp and rtcp-mux are not read. | No UE of this project sends either. |
| Media relay, TS 29.334 5.17.2.5 | A node with a `store` section removes a context from the store when its controller subtracts it, and tries again for a few seconds when the store does not answer. A removal that never succeeds leaves the context there with no time to live: the relay, and every later process of it, opens the ports of a call that has ended and holds them until the store is cleared. | The relay cannot tell such a context from one whose call is still up. Nothing of the call is left to ask, and the items of the IBCF and the dialogs of the P-CSCF stay in the same way. |
| Media relay, TS 29.334 5.17.3.2 and TS 23.333 8.30 | A media node that an operator takes out of service tells no controller so. A real IMS-AGW or MRFP sends each of its controllers a ServiceChange on `ROOT` with the method `GRACEFUL`, and they open no new connection on it until it comes back. Here a controller finds out from the refusal of its next Add, and goes on asking this node once per call until the operator unlocks it. | The controllers of this project keep no list of the media nodes that are unavailable either, so the indication would change only how soon they stop asking. What it costs them meanwhile is one refused request per call, which a media node answers from memory. |
| UA, RFC 3261 8.2 and 13.3.1 | No Request-URI or merged-request checks, no 415, and Expires in an INVITE is ignored. | The UEs here never send what those checks catch. |
| UE, IR.92 3.2.1 | AMR and AMR-WB are sent from frames encoded beforehand, in the modes they were encoded with, whatever CMR or mode-set the peer sends. Without `-epc` the UE offers PCMU only. | The UE never encodes AMR. |
| SIP stack, RFC 3261 12.1.1 and 12.1.2 | A dialog keeps no "secure" flag. | The flag is true only for a dialog set up over TLS with a SIPS URI, and SIP here is plain text. |
| SIP stack, RFC 3261 12.2.1.1, 16.4 and 16.6 step 6 | No strict routing. A route set or a top Route whose URI lacks lr is refused, and a proxy neither restores a Request-URI that a strict router rewrote nor strips a maddr that names it. | No node in this project is a strict router or writes maddr. |
| SIP stack, RFC 3261 17.2.3 | A request whose branch lacks the magic cookie, as an RFC 2543 client sends it, is dropped. | Every client here follows RFC 3261. |
| SIP stack, RFC 3261 18.2.2 | A response is never sent to servers found with RFC 3263 5: neither when its connection cannot be opened again nor for a sent-by that is a domain name. A maddr for multicast is not handled. | Every sent-by here is an IP address or has a received parameter, and no node sends multicast. |
| SIP stack, RFC 3261 18.3 | A request whose body is shorter than its Content-Length is dropped, not answered 400. | Only a broken peer sends one. |
| SIP stack, RFC 3261 21.5.4 | A 503 with Retry-After does not keep other requests away from the server that sent it: each new request goes to the first target again. | Only a node that the operator has shut down or locked answers 503 with Retry-After. The P-CSCF answers so only to a REGISTER, from a UE that uses no other P-CSCF and waits the time of Retry-After itself before it registers again. A request that goes to such an I-CSCF, S-CSCF, MMTel AS, MRFC or IP-SM-GW first all the same gets its 503 and goes on to the next target, one round trip later. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
