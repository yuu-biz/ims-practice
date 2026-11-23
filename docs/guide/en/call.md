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

On the way back, each P-CSCF pass rewrites the answer: the callee's side points the core termination at UE 2's answer and names its core termination in the SDP; the caller's side does the same, so that UE 1 sees its own context's access termination. In the end, each UE talks only to the relay.

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

Each termination **latches**: it learns where to send from the first packet it receives, instead of trusting the SDP. A UE behind NAT sends from an address its SDP cannot know, and latching still reaches it.

Every few seconds each UE also sends an **RTCP** compound packet from the port above its RTP port: a sender report, or a receiver report while it sends nothing, and an SDES with its CNAME (RFC 3550 6, IR.92 3.2.4). It sends one at least every 5 seconds, so that the other side can tell that the call is still up. At the end of the call it sends an RTCP BYE.

The e2e test checks that UE 2 received exactly the bytes UE 1 encoded. G.711 is lossy, so the WAV files themselves are not compared.

### 13. UE 1 hangs up: BYE

UE 1 sends **BYE** within the dialog (RFC 3261 15.1.1), along the route set. The BYE carries `Reason: RELEASE_CAUSE;cause=1;text="User ends call"`, which tells why the call ended (TS 24.229 5.1.5, IR.92 2.2.4). UE 2 answers 200 (RFC 3261 15.1.2). When the 200 passes the P-CSCF, the P-CSCF removes the relay contexts of the call. It waits for the answer because a BYE can be turned down, such as with 500 for a BYE out of order (RFC 3261 12.2.2), and then the dialog stays up. A 481, a 408 or no answer at all ends the dialog as well (RFC 3261 15.1.1), so they also remove the contexts.

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
   curl -X PUT localhost:8091/answer-policy -d '{"mode": "manual"}'
   curl -X POST localhost:8090/call -d '{"target": "tel:+1-202-555-0102"}'
   curl localhost:8091/status
   ```

   The status of UE 2 shows an `incoming` call in the `ringing` state, with the same Call-ID as in the capture.

3. Answer on UE 2, then hang up on UE 1:

   ```sh
   curl -X POST localhost:8091/answer
   curl -X POST localhost:8090/hangup
   ```

   A hang-up sends a BYE once the call is answered, and a CANCEL while it rings. On the callee's side, a hang-up while the call rings turns it down with 603 (Decline), or with the `reject_status` in the body.

   A call that rings for more than a minute gets a 180 (Ringing) every minute, so that no proxy gives up on the INVITE (RFC 3261 13.3.1.1). A UE that stops while a call rings turns it down with 480 (Temporarily Unavailable).

During the call, `curl -X POST localhost:8090/hold` puts it on hold with a re-INVITE that offers `a=sendonly`, `/resume` takes it back, and `curl -X POST localhost:8090/dtmf -d '{"digits": "123"}'` sends DTMF as telephone events. The `session` in the status shows the codec, the directions, the preconditions and the digits that came.

UE 1 plays its tone once and stays on the line until one side hangs up. `curl -N localhost:8091/events` follows the status as it changes. The answer policy can also be `{"mode": "auto", "ring_seconds": 2}`, which answers after two seconds, or `{"mode": "reject", "reject_status": 486}`, which turns every call down. Each UE has one line, which holds two calls at most. A second call goes out only once the first is on hold (`POST /hold`); until then the API answers 409 (Conflict). An INVITE that comes during a call waits: the UE rings with `Alert-Info: <urn:alert:service:call-waiting>` and does not answer by itself, and `POST /answer` puts the first call on hold and takes the waiting one (TS 24.615 4.5.5.3). A third INVITE gets 486 (Busy Here). `ue ctl` sends these requests from a shell. All the requests are listed in the comment of `Phone.Handler` in `internal/ue/phone_http.go`.

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| S-CSCF, TS 24.229 5.4.3.2 and 5.4.3.3 | When the S-CSCF decides whether a failed application server gets the default handling, a 100 (Trying) from the server does not count as a 1xx. | Proxy keeps a 100 to the hop it came over (RFC 3261 16.7 step 5). |
| S-CSCF, TS 24.229 5.4.3.2 | The term-ioi that a response to a request outside a dialog brings is not stored. | It names the network that answered in the charging records of the S-CSCF, which no node of this project writes, and no later message carries it. |
| S-CSCF, TS 24.229 5.4.3.2 steps 4C and 4D, 5.4.3.3 steps 3C and 3D | The user profile lists no subscribed services, so every user subscribes to MMTel. A request that matches no subscribed service is never answered 403. | The HSS keeps only iFCs for a user, and MMTel is the only service of this project with an ICSI. |
| ENUM, RFC 6116 5.2.1 | Non-terminal NAPTR records are skipped. | The project's zone holds terminal records only. |
| ENUM, RFC 3402 3.2 | A delimiter escaped with a backslash inside the NAPTR regexp is not supported; such a record is refused as malformed, or split in the wrong place. | The project's records use `!` and never escape it. |
| I-CSCF, TS 24.229 5.3.2.1, 5.3.2.1A and 5.3.2.2 | No "orig" routing for an application server that sends on behalf of a user, and no reselection of another S-CSCF. | The one application server that sends on behalf of someone, the conference focus on behalf of a conference URI, sends to the S-CSCF, and this network has no redundant S-CSCF to choose. |
| I-CSCF, TS 24.229 5.3.2.1 | The I-CSCF does not check whether a request comes from a trusted domain, so it never removes P-Charging-Vector and P-Charging-Function-Addresses for that reason. It stores neither the icid-value of a request nor the term-ioi of a response. | In the networks that this project sets up, every node that sends to the I-CSCF belongs to its own network: another network reaches it only through the IBCF, which removes both header fields from the requests of an untrusted peer as they enter (5.10.3.2), and a UE only through its P-CSCF. The values are for the charging records of the I-CSCF, which no node of this project writes. |
| P-CSCF, S-CSCF and I-CSCF, RFC 3261 16.10 | A CANCEL that matches no transaction, and that the node cannot forward, is dropped with no response. At the S-CSCF and the I-CSCF, that is a CANCEL outside a dialog that has no Route left once the node has taken its own. At all three, it is also a CANCEL whose Max-Forwards is used up (16.3 step 3), or whose next hop DNS does not find. | The S-CSCF sent the INVITE that such a CANCEL would cancel where the filter criteria and the registration of the served user said, and the I-CSCF sent it to the S-CSCF that the HSS named. The CANCEL alone tells neither. Both forwarded that INVITE statefully, with a branch of their own, so no node further on would match the CANCEL to it either. Max-Forwards runs out only in a loop, and DNS lacks a next hop only when the configuration is broken. |
| P-CSCF, TS 24.229 5.2.6.4.4 step 1 and 5.2.6.4.8 step 2 | The P-Asserted-Identity of the UE that answers carries no display name. | The user profiles of this project hold no display name, so the S-CSCF puts none in P-Associated-URI at registration, and the P-CSCF has none to add. |
| P-CSCF, TS 24.229 5.2.6.3.5 step 7 and 5.2.6.3.9 step 3 | A request that the UE sends in a dialog other than that of an INVITE, such as the refresh of its subscription, carries no P-Charging-Vector. A response of the UE in such a dialog takes the icid-value of the request that it answers. | The P-CSCF keeps only the dialogs of INVITEs, and with them the icid-value of the INVITE. |
| P-CSCF, RFC 3264 and RFC 3262 | An INVITE without an offer gets no relay context: the offer that then comes in a reliable 1xx or in the 2xx, and the answer in the PRACK or the ACK, are left as they are. An offer in an UPDATE or a re-INVITE goes through the relay like the one in the INVITE. | The UE simulator always puts its offer in the INVITE. |
| P-CSCF, RFC 3264 6 | Only the first audio stream with a port goes through the relay. Another stream gets no termination; without an address of its own it points at the relay, where nothing listens on its port. | The UE simulator offers a single stream. |
| P-CSCF, TS 23.334 6.1.2 and 6.1.3 | The P-CSCF does not keep track of the media relays that are unavailable, and does not audit them. Each new call tries the relay that its Call-ID picks, and goes on to the next one only when that one gives no context. The calls on a relay that never comes back keep their signalling, without media, until a UE hangs up: the P-CSCF neither ends them nor moves them to another relay. | A relay that comes back at its own address opens its contexts again, and the media of their calls goes on, so ending the calls would cut ones that recover. Moving a call to another relay would take a new offer from the P-CSCF to each party, which a proxy does not send. |
| UE, TS 24.229 5.1.3.1 b) and 5.1.4A.1 | The UE lists precondition in the Supported of every request within the dialog, also when the peer does not use preconditions. | The UA has one Supported for all its calls; a peer that does not use the mechanism ignores the option tag in Supported. |
| Media relay, RFC 4961 | A latched address is kept for the whole call, unless the controller asks the termination to re-latch. RTP and RTCP latch separately. | Re-latching only matters when a UE moves in the middle of a call. |
| Media relay, H.248.37 6.6.7.1 | After a latch, the packets of other sources are passed on as well, rather than discarded. | Terminations latch by default, and the P-CSCF gives a termination the address of each new SDP without asking it to latch again. The filter would drop what a party sends from a new address. |
| Media relay, RFC 3605 and RFC 5761 | RTCP is taken to arrive on the port above RTP; a=rtcp and rtcp-mux are not read. | No UE of this project sends either. |
| Media relay, TS 29.334 5.17.2.5 | A node with a `store` section removes a context from the store when its controller subtracts it, and tries again for a few seconds when the store does not answer. A removal that never succeeds leaves the context there with no time to live: the relay, and every later process of it, opens the ports of a call that has ended and holds them until the store is cleared. | The relay cannot tell such a context from one whose call is still up. Nothing of the call is left to ask, and the items of the IBCF and the dialogs of the P-CSCF stay in the same way. |
| UA, RFC 3261 8.2 and 13.3.1 | No Request-URI or merged-request checks, no 415, and Expires in an INVITE is ignored. | The UEs here never send what those checks catch. |
| UE, IR.92 3.2.1 | AMR and AMR-WB are sent from frames encoded beforehand, in the modes they were encoded with, whatever CMR or mode-set the peer sends. Without `-epc` the UE offers PCMU only. | The UE never encodes AMR. |
| SIP stack, RFC 3261 12.1.1 and 12.1.2 | A dialog keeps no "secure" flag. | The flag is true only for a dialog set up over TLS with a SIPS URI, and SIP here is plain text. |
| SIP stack, RFC 3261 12.2.1.1, 16.4 and 16.6 step 6 | No strict routing. A route set or a top Route whose URI lacks lr is refused, and a proxy neither restores a Request-URI that a strict router rewrote nor strips a maddr that names it. | No node in this project is a strict router or writes maddr. |
| SIP stack, RFC 3261 17.2.3 | A request whose branch lacks the magic cookie, as an RFC 2543 client sends it, is dropped. | Every client here follows RFC 3261. |
| SIP stack, RFC 3261 16.10 | For a request other than INVITE, a proxy does not cancel what it forwarded. | Clients should not cancel requests other than INVITE (9.1). |
| SIP stack, RFC 3261 18.2.2 | A response is never sent to servers found with RFC 3263 5: neither when its connection cannot be opened again nor for a sent-by that is a domain name. A maddr for multicast is not handled. | Every sent-by here is an IP address or has a received parameter, and no node sends multicast. |
| SIP stack, RFC 3261 18.3 | A request whose body is shorter than its Content-Length is dropped, not answered 400. | Only a broken peer sends one. |
| SIP stack, RFC 3261 21.5.4 | A 503 with Retry-After does not keep other requests away from the server that sent it: each new request goes to the first target again. | Only an S-CSCF that the operator has shut down or locked answers 503 with Retry-After. A request that goes to it first all the same gets its 503 and goes on to the next target, one round trip later. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
