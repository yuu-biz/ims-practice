# Announcements, alerting tones and conferences with the MRF

[日本語](../ja/mrf.md)

This guide follows the media that the network itself plays and mixes: an announcement before a barred call is rejected, the customized alerting tone (CAT) that a called user chooses for its callers, a call that waits while the user is on another one, and a conference of three. The MMTel AS decides what is played; the MRF, a controller (MRFC) and a media processor (MRFP), plays it. Every step shows up in the capture.

The guide follows these editions: TS 23.228 V20.1.0, TS 24.229 V20.1.0, TS 24.182 V19.0.0 (CAT), TS 24.628 V19.0.0 (announcements and early media), TS 24.615 V19.0.0 (CW), TS 24.147 V19.0.0 (conferencing), TS 23.333 V19.0.0 (the MRFP), RFC 4240 (netann), RFC 4575 (the conference event package) and GSMA IR.92 v24.0. Clause numbers below refer to those editions.

## Before you start

- You have read [Supplementary services with the MMTel AS](mmtel.md). This guide reuses what it shows: the AS as a B2BUA with three dialogs per call, the services that a user sets over Ut, and P-Served-User.
- [Follow a voice call through the IMS](call.md) shows the reliable provisional responses (PRACK) and the preconditions that the calls here use.
- The abbreviations are explained in the [glossary](../../glossary.md).

## What an MRF is

The MRF is the node of the IMS that makes media of its own (TS 23.228 4.7): announcements, tones, and the mix of a conference. It is split in two:

- The **MRFC** speaks SIP. An AS asks it for a service with an INVITE, and the MRFC decides which media resources that takes.
- The **MRFP** handles the RTP. It plays an announcement to a termination, or sums the audio of several terminations into one mix for each of them (TS 23.333 6.2.9).

They meet at three reference points (TS 23.218):

| Reference point | Between | In this project |
| --- | --- | --- |
| Mr' | the AS and the MRFC | SIP, straight from the MMTel AS to the MRFC. Mr, from the S-CSCF, is not used. |
| Mp | the MRFC and the MRFP | H.248 in the standard (TS 29.333). Here a small HTTP API on port 8080, with one operation per H.248 command: add a termination, modify it (mode, remote address, play), subtract it, and a Notify when a play ends. |
| Cr | the AS and the MRFC | Fetching prompts and media control commands. Not built: the MRFP plays announcements that it has as WAV files. |

The MRFP is the same relay as the media gateway of the P-CSCF (the agw node), with the announcements and the mixer added.

## How the AS asks: RFC 4240

The AS names the service in the user part of the Request-URI of its INVITE to the MRFC, as RFC 4240 defines:

| Request-URI | Service |
| --- | --- |
| `sip:annc@mrfc.ims.mnc001.mcc001.3gppnetwork.org:5060;play=/provisioned/notice` | Play the announcement `notice` once (RFC 4240 3). |
| `sip:annc@...;play=/provisioned/cat1;repeat=forever` | Play `cat1` until the AS ends the dialog. |
| `sip:conf=conf-pcn6hpat5alr@mrfc...` | Join the conference `conf-pcn6hpat5alr`, which the first INVITE creates (RFC 4240 5). |

The INVITE for an announcement has no SDP, so the MRFC answers with an offer of the MRFP's termination in its 200, and the AS sends the answer in the ACK. The MRFP starts to play on the ACK (TS 24.229 5.8.2.2.1) and the MRFC ends the dialog with BYE when a play that has an end is over (5.8.3.2.1). The AS therefore chooses the moment the play starts, after the caller is ready for it.

To get the audio to the caller before the call is answered, the AS uses the gateway model of TS 24.628 Annex G: it answers the caller's offer itself with the SDP of the MRFP and authorizes the early media with `P-Early-Media: sendonly` (RFC 5009). The caller's UE plays what comes, instead of a ringing tone of its own (TS 24.628 4.7.2.1).

## The nodes

| Node | Address | What it does here |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller of the announcement and the CAT, `tel:+12025550101`. |
| UE 2 | 172.28.0.101 | The called user who has a CAT, `tel:+12025550102`. |
| ue-a, ue-b, ue-c | 172.28.0.110, .111, .112 | The UE simulators of the `sim` profile, subscribers 1, 2 and 3, driven through the Lab: the waiting call and the conference. |
| P-CSCF and agw | 172.28.0.40, 172.28.0.50 | As in every call; RTP between a UE and the MRFP passes the agw. |
| MMTel AS | 172.28.0.75 | Decides what is played, and is the conference focus. |
| MRFC | 172.28.0.85 | The `mrfc` role, `mrfc.ims.mnc001.mcc001.3gppnetwork.org`. |
| MRFP | 172.28.0.86 | The `mrfp` role: control API on port 8080, RTP on ports 40100 to 40199. |

`deploy/compose/mmtel.yaml` names the MRFC (`mrfc:`) and the announcement before a rejection (`announcement: notice`). `deploy/compose/mrfp.yaml` maps the names `notice`, `cat1` and `cat2` to the WAV files in `deploy/announcements`, which `go generate ./deploy/announcements` makes: three rising tones for `notice` and two short tunes for the CATs, 8 kHz, 16-bit mono.

## Run it

`scripts/e2e-mrf.sh` does everything below and checks it in the capture. It leaves the capture in `captures/`, the frames of the calls and the MRF in `e2e-out/mrf-ladder.txt`, the audio that UE 1 heard during the CAT in `captures/mrf-cat-received.raw`, and the logs of the UEs in `e2e-out/`. It takes about a minute after the build.

By hand, with the Lab (see "Drive the UE simulators" in [the Lab guide](lab.md)):

```sh
docker compose up -d --build
docker compose --profile sim up -d ue-a ue-b ue-c
```

Open http://localhost:8080. In the Subscribers panel choose `cat1` as the alerting tone of subscriber 2, then call `ue-b` from `ue-a`. For the waiting call and the conference, follow the steps in the Lab guide that take a second call and make a conference of the two. The ladder of a call draws the AS's dialog with the MRFC beside the dialogs of the call, because they share the icid-value of the P-Charging-Vector.

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (sip.Event contains "conference") || (http && ip.addr == 172.28.0.86)
```

**Telephony > RTP > RTP Streams** lists the streams of the MRFP (172.28.0.86); **Play Streams** plays them. Some RTP starts before Wireshark has read the SDP that describes it: switch on **Analyze > Enabled Protocols > RTP > rtp_udp** (in tshark, `-o rtp.heuristic_rtp:TRUE`) to decode all of it.

## An announcement before a rejection

UE 1 bars its outgoing calls with `ue xcap set baoc on` and calls UE 2. UE 1's AS bars the call (TS 24.611 4.5.2.4.1), but first plays `notice` (TS 24.628 4.2.4):

```text
#361  UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#525  MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/notice, no SDP
#546  MRFC   -> MMTel  M  200 OK   SDP offer: the MRFP 172.28.0.86, PCMU, sendonly
#559  P-CSCF -> UE 1   C1 183      Require: 100rel, P-Early-Media: sendonly, SDP answer from the MRFP
#564  UE 1   -> P-CSCF C1 PRACK
#582  MMTel  -> MRFC   M  ACK      SDP answer: UE 1's media, recvonly
#598  MRFP   -> agw       RTP      PCMU, notice, on to UE 1 (#601)
#728  MRFC   -> MMTel  M  BYE      notice has played, 1.08 s after the ACK
#746  P-CSCF -> UE 1   C1 603      Reason: SIP;cause=603;text="Decline"
```

- **The AS waits for the PRACK** before it sends the ACK, so no frame of the announcement is lost before UE 1 has the SDP.
- **The 603 carries a Reason** with the cause of the rejection, since the caller already has an early dialog (TS 24.628 4.2.4).
- A caller whose INVITE does not take reliable provisional responses, or requires preconditions, gets the 603 at once: the MRFC writes no precondition attributes.

## CAT: the tone that the called user chooses

The operator provisions the CAT of a user in the HSS (TS 24.182 4.5.2); here the Lab does it, with `PUT /api/subscribers/{impi}/customized-alerting-tone` and `{"tone": "cat1", "active": true}`. The terminating AS of UE 2 reads it over Sh for each call (UDR with Service-Indication `Customized-Alerting-Tone`), asks the MRFC for the tone before it sends the INVITE to UE 2, and gives it to UE 1 while UE 2 rings (TS 24.182 4.5.5.3.7):

```text
#1288 12.471 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#1489 12.530 MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/cat1;repeat=forever
#1497 12.532 MRFC   -> MMTel  M  200 OK   SDP offer of the MRFP
#1517 12.539 P-CSCF -> UE 2   C3 INVITE
#1560 12.543 P-CSCF -> UE 1   C1 183      UE 2's answer with the precondition table, as it came
#1627 12.558 UE 2   -> P-CSCF C3 180      UE 2 alerts its user
#1670 12.572 P-CSCF -> UE 1   C1 UPDATE   P-Early-Media: sendonly, SDP of the MRFP: sendonly, a=content:g.3gpp.cat
#1695 12.577 MMTel  -> MRFC   M  ACK      the tone starts
#1795 12.600 MRFP   -> agw       RTP      cat1, on to UE 1 (#1796)
#2105 15.540 UE 2   -> P-CSCF C3 200 OK   UE 2 answers
#2137 15.551 MMTel  -> MRFC   M  BYE      the tone stops
#2175 15.573 P-CSCF -> UE 1   C1 UPDATE   SDP of UE 2: UE 1's media moves to UE 2
#2221 15.580 P-CSCF -> UE 1   C1 200 OK   (INVITE)
```

- **UE 1 uses preconditions**, so UE 2's 183 goes to UE 1 as it came, and the tone comes in an UPDATE once UE 2 alerts (TS 24.182 4.5.5.3.7 b 1). A caller without preconditions gets the tone in the 18x itself.
- **`a=content:g.3gpp.cat`** (RFC 4796) marks the SDP of the tone, and `P-Early-Media: sendonly` authorizes it (TS 24.182 4.5.5.3.7 b 2).
- **At the answer** the AS ends its dialog with the MRFC and moves UE 1's media to UE 2 with an UPDATE before the 200, in the gateway model of TS 24.628 Annex G.
- **UE 1 logs** `"msg":"ringback before the answer","ringback":"network"`. The script compares the first second of what UE 1 received with `deploy/announcements/cat1.wav` as G.711 μ-law encodes it: they are equal byte for byte.

Then the script switches the tone off (`"active": false`) and calls again. UE 2's 180 reaches UE 1 without P-Early-Media, no INVITE goes to the MRFC and no RTP comes from the MRFP; UE 1 logs `"ringback":"local"` and plays its own ringing tone (TS 24.628 4.7.2.1).

## Call waiting and hold

ue-a calls ue-b; then ue-c calls ue-a. IR.92 asks the UE, not the network, to handle the waiting call (2.3.4):

```text
#4772 29.049 ue-a   -> P-CSCF A1 INVITE tel:+12025550102
#5171 30.097 P-CSCF -> ue-a   A1 200 OK   ue-b answered
#5869 34.181 ue-c   -> P-CSCF    INVITE tel:+12025550101
#5986 34.212 P-CSCF -> ue-a   A2 INVITE
#6093 34.228 ue-a   -> P-CSCF A2 180      Alert-Info: <urn:alert:service:call-waiting>
#6134 34.232 P-CSCF -> ue-c      180      without that Alert-Info
#6278 38.256 ue-a   -> P-CSCF A1 INVITE   re-INVITE, a=sendonly: ue-b on hold
#6389 38.277 ue-a   -> P-CSCF A2 200 OK   ue-a answers ue-c
#7172 42.361 ue-a   -> P-CSCF A2 INVITE   Swap: ue-c on hold
#7283 42.394 ue-a   -> P-CSCF A1 INVITE   Swap: ue-b back, a=sendrecv
```

- **ue-a rings with the call-waiting Alert-Info** (TS 24.615 4.5.5.3.2, RFC 7462) and does not answer by itself.
- **The AS of ue-a takes the Alert-Info out** of the 180 to ue-c: the operator part of CW in `deploy/compose/hss.yaml` says that the caller does not learn that its call waits (TS 24.615 4.5.5.2.3). With the other setting the AS passes it on, and when an announcement is configured, plays it to the caller while the call waits.
- **Answering the waiting call holds the first** with a re-INVITE whose offer is `sendonly` (TS 24.615 4.5.5.3.3, TS 24.610). **Swap** holds one call and takes back the other.

## A conference of three

ue-a has two answered calls and presses **Merge**. It creates a conference at the conference factory URI and asks the focus, the MMTel AS, to bring each peer in with a REFER whose Refer-To carries a Replaces of ue-a's call with that peer (TS 24.147 5.3.1.3.3, option 2b, which IR.92 2.3.3 asks for):

```text
#7480 40.121 ue-a   -> P-CSCF F  INVITE sip:mmtel@conf-factory.ims.mnc001.mcc001.3gppnetwork.org
#7511 40.131 MMTel  -> MRFC   M1 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., with ue-a's offer
#7534 40.136 P-CSCF -> ue-a   F  200 OK  Contact: <sip:conf-o47ha7ifxyni@conf.ims.mnc001.mcc001.3gppnetwork.org>;isfocus
#7549 40.138 ue-a   -> P-CSCF F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...  Event: conference
#7565 40.142 S-CSCF -> DNS       NAPTR? conf.ims.mnc001.mcc001.3gppnetwork.org
#7568 40.143 DNS    -> S-CSCF    SRV _sip._udp.conf...: 5060 mmtel.ims.mnc001.mcc001.3gppnetwork.org
#7571 40.144 S-CSCF -> MMTel  F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...
#7602 40.150 ue-a   -> P-CSCF F  REFER   Refer-To: <sip:+12025550102@...;user=phone?Replaces=...>
#7635 40.156 P-CSCF -> ue-a   F  200 OK  (REFER), not 202
#7662 40.164 MMTel  -> MRFC   M2 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., no SDP
#7667 40.165 MRFC   -> MMTel  M2 200 OK  SDP offer of a new termination of the MRFP
#7712 40.179 P-CSCF -> ue-b      INVITE  Replaces: ue-b's call with ue-a, the MRFP's offer
#7716 40.180 ue-b   -> P-CSCF    183     ue-b's answer
#7825 40.196 ue-b   -> P-CSCF    200 OK  without SDP
#7867 40.201 ue-b   -> P-CSCF    BYE     the call that was replaced
#7887 40.204 MMTel  -> MRFC   M2 ACK     ue-b's answer from its 183
#7943 40.213 P-CSCF -> ue-a   F  NOTIFY  Event: conference, ue-a and ue-b
#8040 40.225 ue-a   -> P-CSCF F  REFER   the same for ue-c
#8398 40.306 P-CSCF -> ue-a   F  NOTIFY  Event: conference, three users
```

- **The conference URI is a public service identity (PSI) of the home network** (TS 23.228 4.3.6), `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org`. The operator defines its domain, and it has no port, which a PSI that may leave the network should not have (TS 23.003 13.5). It names no node: the subdomain `conf` stands for the conferences of the AS. The NAPTR and SRV records of the subdomain lead to the AS that hosts them (`deploy/compose/dns`), so the S-CSCF finds the focus as it finds any host, by RFC 3263: for the SUBSCRIBE of ue-a, and for each request within a dialog with the focus. This is the subdomain way of TS 23.228 5.4.12.3. A request from another network comes in through the I-CSCF, which knows the subdomain (`psi_subdomains` in `deploy/compose/icscf.yaml`) and sends the request by DNS too, without asking the HSS (TS 24.229 5.3.2.1 step 3). A conference lives on one instance of the AS, with its dialogs. With several instances, each would have a subdomain of its own, so that DNS sends the requests of a conference to the instance that has it.
- **The factory URI is a PSI too**, of the originating side (TS 23.228 5.4.12.1): the INVITE to it reaches the AS by the creator's originating filter criteria, as every INVITE of the creator does. Such a PSI serves the users of the home network only.
- **The AS is the focus and the notification service** of the conference (TS 24.147 4): it holds a dialog with each participant, and a dialog with the MRFC for each participant's media.
- **The MRFC creates the mix** at the first `conf=` INVITE and adds a termination for each later one. The MRFP sends each party the sum of the others (TS 23.333 6.2.9): in the capture three streams go from the MRFP to the agw and three come back, and the script finds that the mix that one party gets is louder than any single party's tone.
- **The invited peers use preconditions**, so each answers the MRFP's offer in its reliable 183 and sends its 200 without SDP. The AS passes that answer to the MRFC in the ACK of M2 (RFC 3262 5).
- **The focus accepts each REFER with 200 (OK).** RFC 3515 answered with 202 (Accepted). RFC 7647 5 has replaced that: an element that accepts a REFER sends 200 and never 202, and one that receives a 202 from an older UA takes it as a 200, as RFC 6665 8.3.1 asks for SUBSCRIBE. The reason is that a 202 tells nothing that a 200 does not, and a proxy may absorb it. TS 24.147 points to RFC 3515 as RFC 6665 and RFC 7647 update it, and its flows show the 200 (A.4.4.1).
- **Each REFER creates a subscription to the refer event** (RFC 3515 2.4.4). The focus reports at once that it is trying (`SIP/2.0 100 Trying` in a sipfrag body). Once the INVITE to the peer has its final response, it reports that response in a last NOTIFY whose Subscription-State is `terminated;reason=noresource` (2.4.7). The subscription lasts four minutes, longer than the INVITE may take (3.4). Should they run out first, the focus ends the subscription with `terminated;reason=timeout` (RFC 6665 4.2.1.4). ue-a ends it too at the time that the last `expires` gave, and at once on `active;expires=0` (4.1.3).
- **The NOTIFYs** carry conference-info XML (RFC 4575) with a `<user>` and an `<endpoint>` of status `connected` for each party. The `entity` of a `<user>` is the identity that the network asserted for the party. The `entity` of an `<endpoint>` is a key that the focus makes for it, a `urn:uuid:` URI. RFC 4575 5.7 leaves the key to the focus and gives the Contact and a GRUU as examples. The focus does not use the Contact: it is the address of the party's UE, and the NOTIFYs would show it to every subscriber, in another network too ([A conference across the border](interconnect.md#a-conference-across-the-border)). Each card of the Lab lists them under **Conference**. ue-a refreshes its subscription before it expires. A subscription that is not refreshed ends at its expiry with a last NOTIFY that says `terminated;reason=timeout`, and the focus sends nothing to it after that (RFC 6665 4.2.1.4).
- **The conference ends with its creator** (TS 24.147 5.3.2.7): when ue-a hangs up, the AS sends BYE to ue-b and ue-c and to the MRFC for each party.

## Coming back to the conference

A participant that has left can come back while the conference goes on. It sends an INVITE to the conference URI, which it has from the Contact of the focus (TS 24.147 5.3.1.4.1). Say ue-b hangs up, and then calls the conference URI:

```text
ue-b     P-CSCF   S-CSCF   MMTel AS              MRFC
                           services  focus
 | INVITE sip:conf-o47ha7ifxyni@conf...            |
 |-------->|------->|------->|        |            |  P-Served-User: ue-b;sescase=orig
 |         |        |<-------|        |            |  the INVITE of the B2BUA, to the same URI
 |         |        |---------------->|            |  by DNS, without P-Served-User
 |         |        |        |        |----------->|  INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., ue-b's offer
 |         |        |        |        |<-----------|  200 OK, the answer of the MRFP
 |         |        |<----------------|            |  200 OK, Contact: <sip:conf-o47ha7ifxyni@conf...>;isfocus
 |         |        |------->|        |            |
 |<--------|<-------|<-------|        |            |  200 OK, the same Contact
```

- **The INVITE goes as every call of ue-b goes.** The S-CSCF gives it to the MMTel AS for ue-b's originating services, and the AS, a B2BUA, sends a new INVITE to the same URI back. The S-CSCF then finds the focus by DNS, as for the SUBSCRIBE above. From another network, the INVITE comes through the I-CSCF, which sends it by DNS too. No filter criterion brings the INVITE to the focus, so it has no P-Served-User. This is how the AS tells the INVITE for the focus from the INVITE for ue-b's services, which has the same Request-URI.
- **The focus takes ue-b in as it takes the creator** (5.3.2.4.1). It asks the MRFC for a new termination of the mix with ue-b's offer. Once the MRFC has answered, it answers ue-b with the answer of the MRFP and the conference URI with `isfocus` in Contact. The AS of ue-b's services passes that Contact on and puts itself in Record-Route, as for an invited peer, so that ue-b's requests to the conference URI still pass it (TS 24.229 5.7.5.1).
- **The NOTIFYs** list ue-b as `connected` again, now with the joining-method `dialed-in`, as for the creator. Before, when the focus invited ue-b, it was `dialed-out`.
- **Any user whose identity the network asserts may join**, whether or not the user has been in the conference before, up to six participants, the maximum-user-count of the NOTIFYs. The seventh gets 403 (Forbidden). An INVITE to a conference URI that no conference has gets 404 (Not Found) (5.3.2.4.1 step 1). That is what the creator gets too: the conference ended when it left, and its URI with it (5.3.2.7).

The script does not run this. `TestRejoinWithTheConferenceURI` in `internal/mmtel/conference_network_test.go` runs it through the whole IMS in one process: B leaves A's conference, comes back with the conference URI, and hears A through the mixer.

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.r-uri contains "annc@"` | The requests for an announcement or a tone. `contains "play=/provisioned/cat"` finds the CATs. |
| `sip.r-uri contains "conf="` | The parties joining a conference at the MRFC. |
| `sip.P-Early-Media` | The responses and UPDATEs that authorize early media. |
| `sdp.media_attr == "content:g.3gpp.cat"` | The SDP of a customized alerting tone. |
| `sip.Alert-Info contains "call-waiting"` | The 180s of a waiting call. |
| `sip.Refer-To contains "Replaces"` | The REFERs that bring a peer into a conference. |
| `sip.Replaces` | The INVITEs of the focus that take over a call. |
| `sip.Event contains "conference"` | The subscriptions and NOTIFYs of the conference event package. |
| `sip.Contact contains "isfocus"` | The answers of the focus. |
| `dns.qry.name contains "conf."` | The DNS queries that lead to the focus of a conference. |
| `rtp && ip.addr == 172.28.0.86` | The RTP of the MRFP, with the RTP heuristic on. |
| `http && ip.addr == 172.28.0.86` | The control API of the MRFP: the POST, PUT and DELETE of terminations, and the Notify that a play ended. |

These filters were checked with tshark 4.2 against a capture of `scripts/e2e-mrf.sh`.

## Where the code is

| File | What it holds |
| --- | --- |
| `internal/mmtel/mrf.go`, `internal/mmtel/announcement.go` | The AS's dialog with the MRFC, and the announcement before a rejection. |
| `internal/mmtel/cat.go`, `internal/mmtel/catdata.go` | CAT at the terminating AS, and its data in the HSS. |
| `internal/mmtel/waiting.go` | CW at the AS. |
| `internal/mmtel/conference.go`, `internal/mmtel/conferenceevent.go` | The focus and the conference event package. |
| `internal/mrfc`, `internal/netann` | The MRFC and the Request-URIs of RFC 4240. |
| `internal/agw` | The MRFP: terminations, announcements and the mixer, and its control API. |
| `internal/ue/phone_conference.go`, `internal/ue/conference.go` | Swap, Merge and the UE side of a conference. |
| `internal/mmtel/mrf_network_test.go`, `internal/mmtel/conference_network_test.go` | The same flows through the whole IMS in one process. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| MRFC, TS 23.333 5.12 | The MRFP plays and mixes G.711 PCMU only and transcodes nothing. 5.12.1 asks an MRFP to transcode and to support AMR at least, and lets it support the other codecs of TS 26.114, such as AMR-WB. An offer without PCMU gets 488 with Warning 305. | A conference mixes decoded speech, and the product has no AMR decoder. Adding one would cost much and change little of the signalling these guides follow. The UE simulators still offer PCMU. |
| MRFC, RFC 4240 3 and TS 24.229 10.3.2.2 | Only provisioned announcements; no prompt is fetched from a URL over Cr. | The WAV files are in the MRFP's configuration. |
| MRFC, TS 24.229 5.8.1 | The Contact is the SIP URI of the instance, not a GRUU. | Nothing sends a request to the MRFC outside a dialog it has with it. |
| MRFC, TS 24.229 5.8.2.1.1.1 and RFC 4240 2 and 8 | No charging data, sips: is not enforced, no AS is authenticated. | No node of the project writes charging records, and nothing is encrypted so that the capture shows the content. |
| MRFC, RFC 4240 3 | The suggested reason phrases go into a Warning. | The UA writes the standard phrase in the status line. |
| netann, RFC 4240 3 | locale, content-type and param1 to param9 are ignored. | Each announcement has one variant. |
| MRFP, TS 23.333 5.10 and RFC 3550 6 and 7.1 | The mixer takes PCMU only, keeps three frames for jitter, writes no CSRC list and sends no RTCP. | Enough for a conference of a few UEs on one bridge. |
| MRFP, RFC 3550 6 | No RTCP sender reports for the media it makes. | The UE simulator needs none. |
| MRFP, TS 29.333 5.17.2.9 | An MRFP created again under the same name opens the ports of its contexts again, but plays nothing in them: an announcement or a tone that was playing does not start again, and the MRFC gets no event for an end that never comes. The media the MRFP makes itself comes from a new RTP source after the gap, and the packet counts of the terminations start at zero. | How far a termination had got through an announcement is not kept with the context. The MRFC starts the next announcement of a call itself, and a conference goes on being mixed. |
| AS, TS 24.182 4.5.5.3.7 | A callee's UPDATE with an offer after its 180 goes on to the caller, which moves the caller away from the tone. | The UE simulator sends none then. |
| AS, TS 24.182 4.5.5.3.7 b 2 and b 3 | The callee's 183 with preconditions goes to the caller as it came; the tone starts without a look at the caller's preconditions. | The callee has alerted, so both sides have their resources. |
| AS, TS 24.147 5.3.2.3.1 step 2 and TS 24.229 5.7.1.4 and 5.7.1.5 | Every served user may create a conference; no conference policy, no charging addresses. | There is no conference policy server, and no node of this project writes charging records. |
| AS, TS 24.147 5.3.2.4.1 step 2 and TS 24.229 5.7.1.4 and 5.7.1.5 | The focus takes the P-Asserted-Identity of the INVITE as the user's identity, as it takes the identity that the S-CSCF asserts for the creator, and checks nothing more. It does not check that the INVITE came from a node of the trust domain (TS 24.229 5.7.1.4 b i, RFC 3325 8). A user whose INVITE asks for privacy with `Privacy: id` or `user` is named by that identity in the NOTIFYs, where TS 24.229 5.7.1.4 a takes the user as anonymous and TS 24.605 4.6.5 keeps the identity of a user with OIR out of them. Any user may join a conference that has room. An INVITE without P-Asserted-Identity gets 403, without a challenge. | There is no conference policy, and the AS holds no credentials of users and gives no participant an anonymous URI (RFC 4575 5.6). In the networks of this project, every request reaches the focus with the identity that a P-CSCF or a trusted peer network asserted. |
| AS, TS 24.229 5.7.3 NOTE 8 | The S-CSCF of an invited user is taken to be the AS's own. | The configuration names one S-CSCF, which serves every user of the network. When it runs as several instances, they share one name and keep the registrations in one store. |
| AS, TS 24.147 5.3.2.6.2.2 | A REFER that asks the focus to remove a participant (method BYE) is refused with 403. | IR.92 2.3.3 does not ask for it. |
| AS, RFC 4575 3.9 | Each change is notified at once, not at most once in 5 seconds. | IR.92 2.3.3 asks for notifications within 5 seconds, and a small conference changes seldom. |
| AS, TS 24.147 5.3.3.2 step 2 and RFC 4575 3.5 | Every subscriber whose identity the network asserts may subscribe. | There is no conference policy. |
| UE, RFC 3891 3 and RFC 3892 | The Referred-By carries no Authenticated Identity Body. | The referrer's network asserted the identity to the focus. |

## Clean up

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
