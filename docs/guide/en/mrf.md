# Announcements, alerting tones and conferences with the MRF

[日本語](../ja/mrf.md)

This guide follows the media that the network itself plays and mixes: an announcement before a barred call is rejected, the customized alerting tone (CAT) that a called user chooses for its callers, a call that waits while the user is on another one, and a conference of three. The MMTel AS decides what is played; the MRF, a controller (MRFC) and a media processor (MRFP), plays it. Every step shows up in the capture.

The guide follows these editions: TS 23.228 V20.1.0, TS 24.229 V20.1.0, TS 24.182 V19.0.0 (CAT), TS 24.628 V19.0.0 (announcements and early media), TS 24.611 V19.0.0 (barring), TS 24.615 V19.0.0 (CW), TS 24.147 V19.0.0 (conferencing), TS 24.605 V19.0.0 (the CONF service), TS 24.607 V19.1.0 (OIR), TS 24.608 V19.0.0 (TIR), TS 23.333 V19.0.0 (the MRFP), RFC 4240 (netann), RFC 4575 (the conference event package) and GSMA IR.92 v24.0. Clause numbers below refer to those editions.

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
| phone1, phone2, phone3 | 172.28.0.110, .111, .112 | The UE simulators of the `phones` profile, subscribers 1, 2 and 3, driven through the Lab: the waiting call and the conference. |
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
docker compose --profile phones up -d phone1 phone2 phone3
```

Give subscriber 2 the alerting tone `cat1`. The operator provisions it through the OAM, whose port is on no address of the host, so the request goes from inside the OAM's container. BusyBox `wget` cannot send a `PUT`, hence the request written out to `nc`:

```sh
docker compose exec oam sh -c '
body="{\"tone\":\"cat1\",\"active\":true}"
printf "PUT /oam/v1/subscriptions/001010000000002@ims.mnc001.mcc001.3gppnetwork.org/customized-alerting-tone HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8080'
```

Then open http://localhost:8080 and call `phone2` from `phone1`. For the waiting call and the conference, follow the steps in the Lab guide that take a second call and make a conference of the two. The ladder of a call draws the AS's dialog with the MRFC beside the dialogs of the call, because they share the icid-value of the P-Charging-Vector.

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

The operator provisions the CAT of a user in the HSS (TS 24.182 4.5.2); here the OAM does it, with `PUT /oam/v1/subscriptions/{private identity}/customized-alerting-tone` and `{"tone": "cat1", "active": true}`, which it passes on to the HSS. The user has no standard way to set it (4.8). The terminating AS of UE 2 reads it over Sh for each call (UDR with Service-Indication `Customized-Alerting-Tone`), asks the MRFC for the tone before it sends the INVITE to UE 2, and gives it to UE 1 while UE 2 rings (TS 24.182 4.5.5.3.7):

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
- **At the answer** the AS ends its dialog with the MRFC and moves UE 1's media to UE 2 with an UPDATE before the 200, in the gateway model of TS 24.628 Annex G. The P-CSCF gives the network side of UE 1's relay context the address in that UPDATE. That termination does not latch, so UE 1's voice goes to UE 2 from then on, and no longer to the MRFP whose tone it received ([Follow a voice call through the IMS](call.md)). The script checks that, after the answer, each UE received what the other sent.
- **UE 1 logs** `"msg":"ringback before the answer","ringback":"network"`. The script compares the first second of what UE 1 received with `deploy/announcements/cat1.wav` as G.711 μ-law encodes it: they are equal byte for byte.

Then the script switches the tone off (`"active": false`) and calls again. UE 2's 180 reaches UE 1 without P-Early-Media, no INVITE goes to the MRFC and no RTP comes from the MRFP; UE 1 logs `"ringback":"local"` and plays its own ringing tone (TS 24.628 4.7.2.1).

## Call waiting and hold

phone1 calls phone2; then phone3 calls phone1. IR.92 asks the UE, not the network, to handle the waiting call (2.3.4):

```text
#4772 29.049 phone1   -> P-CSCF A1 INVITE tel:+12025550102
#5171 30.097 P-CSCF -> phone1   A1 200 OK   phone2 answered
#5869 34.181 phone3   -> P-CSCF    INVITE tel:+12025550101
#5986 34.212 P-CSCF -> phone1   A2 INVITE
#6093 34.228 phone1   -> P-CSCF A2 180      Alert-Info: <urn:alert:service:call-waiting>
#6134 34.232 P-CSCF -> phone3      180      without that Alert-Info
#6278 38.256 phone1   -> P-CSCF A1 INVITE   re-INVITE, a=sendonly: phone2 on hold
#6389 38.277 phone1   -> P-CSCF A2 200 OK   phone1 answers phone3
#7172 42.361 phone1   -> P-CSCF A2 INVITE   Swap: phone3 on hold
#7283 42.394 phone1   -> P-CSCF A1 INVITE   Swap: phone2 back, a=sendrecv
```

- **phone1 rings with the call-waiting Alert-Info** (TS 24.615 4.5.5.3.2, RFC 7462) and does not answer by itself.
- **The AS of phone1 takes the Alert-Info out** of the 180 to phone3: the operator part of CW in `deploy/compose/hss.yaml` says that the caller does not learn that its call waits (TS 24.615 4.5.5.2.3). With the other setting the AS passes it on, and when an announcement is configured, plays it to the caller while the call waits.
- **Answering the waiting call holds the first** with a re-INVITE whose offer is `sendonly` (TS 24.615 4.5.5.3.3, TS 24.610). **Swap** holds one call and takes back the other.

## A conference of three

phone1 has two answered calls and presses **Merge**. It creates a conference at the conference factory URI and asks the focus, the MMTel AS, to bring each peer in with a REFER whose Refer-To carries a Replaces of phone1's call with that peer (TS 24.147 5.3.1.3.3, option 2b, which IR.92 2.3.3 asks for):

```text
#7480 40.121 phone1   -> P-CSCF F  INVITE sip:mmtel@conf-factory.ims.mnc001.mcc001.3gppnetwork.org
#7511 40.131 MMTel  -> MRFC   M1 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., with phone1's offer
#7534 40.136 P-CSCF -> phone1   F  200 OK  Contact: <sip:conf-o47ha7ifxyni@conf.ims.mnc001.mcc001.3gppnetwork.org>;isfocus
#7549 40.138 phone1   -> P-CSCF F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...  Event: conference
#7565 40.142 S-CSCF -> DNS       NAPTR? conf.ims.mnc001.mcc001.3gppnetwork.org
#7568 40.143 DNS    -> S-CSCF    SRV _sip._udp.conf...: 5060 mmtel.ims.mnc001.mcc001.3gppnetwork.org
#7571 40.144 S-CSCF -> MMTel  F  SUBSCRIBE sip:conf-o47ha7ifxyni@conf...
#7602 40.150 phone1   -> P-CSCF F  REFER   Refer-To: <sip:+12025550102@...;user=phone?Replaces=...>
#7635 40.156 P-CSCF -> phone1   F  200 OK  (REFER), not 202
#7662 40.164 MMTel  -> MRFC   M2 INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., no SDP
#7667 40.165 MRFC   -> MMTel  M2 200 OK  SDP offer of a new termination of the MRFP
#7712 40.179 P-CSCF -> phone2      INVITE  Replaces: phone2's call with phone1, the MRFP's offer
#7716 40.180 phone2   -> P-CSCF    183     phone2's answer
#7825 40.196 phone2   -> P-CSCF    200 OK  without SDP
#7867 40.201 phone2   -> P-CSCF    BYE     the call that was replaced
#7887 40.204 MMTel  -> MRFC   M2 ACK     phone2's answer from its 183
#7943 40.213 P-CSCF -> phone1   F  NOTIFY  Event: conference, phone1 and phone2
#8040 40.225 phone1   -> P-CSCF F  REFER   the same for phone3
#8398 40.306 P-CSCF -> phone1   F  NOTIFY  Event: conference, three users
```

- **The conference URI is a public service identity (PSI) of the home network** (TS 23.228 4.3.6), `sip:conf-<id>@conf.ims.mnc001.mcc001.3gppnetwork.org`. The operator defines its domain, and it has no port, which a PSI that may leave the network should not have (TS 23.003 13.5). It names no node: the subdomain `conf` stands for the conferences of the AS. The NAPTR and SRV records of the subdomain lead to the AS that hosts them (`deploy/compose/dns`), so the S-CSCF finds the focus as it finds any host, by RFC 3263: for the SUBSCRIBE of phone1, and for each request within a dialog with the focus. This is the subdomain way of TS 23.228 5.4.12.3. A request from another network comes in through the I-CSCF, which knows the subdomain (`psi_subdomains` in `deploy/compose/icscf.yaml`) and sends the request by DNS too, without asking the HSS (TS 24.229 5.3.2.1 step 3). A conference lives on one instance of the AS, with its dialogs. With several instances, each would have a subdomain of its own, so that DNS sends the requests of a conference to the instance that has it.
- **The factory URI is a PSI too**, of the originating side (TS 23.228 5.4.12.1): the INVITE to it reaches the AS by the creator's originating filter criteria, as every INVITE of the creator does, and the creator's originating services run on it before it becomes a conference. The S-CSCF matches an initial request against the filter criteria of the user before it routes the request on by its Request-URI (TS 24.229 5.4.3.2 step 5), so a focus in an application server of its own gets the INVITE the same way, after those services. The creator's barring rules decide on it like on any other outgoing call: a creator whose outgoing calls are barred gets 603 (Decline) from its own AS, and no conference is made (TS 24.611 4.5.2.4.1). Such a PSI serves the users of the home network only.
- **The AS is the focus and the notification service** of the conference (TS 24.147 4): it holds a dialog with each participant, and a dialog with the MRFC for each participant's media.
- **The MRFC creates the mix** at the first `conf=` INVITE and adds a termination for each later one. The MRFP sends each party the sum of the others (TS 23.333 6.2.9): in the capture three streams go from the MRFP to the agw and three come back, and the script finds that the mix that one party gets is louder than any single party's tone.
- **The invited peers use preconditions**, so each answers the MRFP's offer in its reliable 183 and sends its 200 without SDP. The AS passes that answer to the MRFC in the ACK of M2 (RFC 3262 5).
- **The focus accepts each REFER with 200 (OK).** RFC 3515 answered with 202 (Accepted). RFC 7647 5 has replaced that: an element that accepts a REFER sends 200 and never 202, and one that receives a 202 from an older UA takes it as a 200, as RFC 6665 8.3.1 asks for SUBSCRIBE. The reason is that a 202 tells nothing that a 200 does not, and a proxy may absorb it. TS 24.147 points to RFC 3515 as RFC 6665 and RFC 7647 update it, and its flows show the 200 (A.4.4.1).
- **Each REFER creates a subscription to the refer event** (RFC 3515 2.4.4). The focus reports at once that it is trying (`SIP/2.0 100 Trying` in a sipfrag body). Once the INVITE to the peer has its final response, it reports that response in a last NOTIFY whose Subscription-State is `terminated;reason=noresource` (2.4.7). The subscription lasts four minutes, longer than the INVITE may take (3.4). Should they run out first, the focus ends the subscription with `terminated;reason=timeout` (RFC 6665 4.2.1.4). phone1 ends it too at the time that the last `expires` gave, and at once on `active;expires=0` (4.1.3).
- **The NOTIFYs** carry conference-info XML (RFC 4575) with a `<user>` and an `<endpoint>` of status `connected` for each party. The `entity` of a `<user>` is the identity that the network asserted for the party. The `entity` of an `<endpoint>` is a key that the focus makes for it, a `urn:uuid:` URI. RFC 4575 5.7 leaves the key to the focus and gives the Contact and a GRUU as examples. The focus does not use the Contact: it is the address of the party's UE, and the NOTIFYs would show it to every subscriber, in another network too ([A conference across the border](interconnect.md#a-conference-across-the-border)). Each card of the Lab lists them under **Conference**. phone1 refreshes its subscription before it expires. A subscription that is not refreshed ends at its expiry with a last NOTIFY that says `terminated;reason=timeout`, and the focus sends nothing to it after that (RFC 6665 4.2.1.4).
- **Each subscription waits for its own subscriber only.** The focus sends the NOTIFYs of one subscription one after the other, each once the one before has its final response, so that they arrive in order. A subscriber that does not answer holds up neither the 200 to a user who joins nor the NOTIFYs to the others. It loses its subscription before its expiry: when Timer F fires 32 seconds after a NOTIFY went, with no response to it, the focus ends the subscription. It does the same on a 408 (Request Timeout), which counts as that timeout (RFC 5057 5.1), and on a response that says the subscriber has lost the subscription, such as 481 (RFC 6665 4.2.2). The focus then sends that subscription nothing more, not even a last NOTIFY. A UE that comes back is expected to subscribe again (4.2.2). For a subscription within the dialog of the participant's conference session, such a failure ends that subscription and nothing else: the session shares the dialog and goes on, and the participant stays in the conference (RFC 5057 5.1, 5.2). A 481 says that the peer has lost the state of that usage of the dialog, not the dialog, and a 408 counts as a timeout, which affects only the usage that the request was of. `TestA481ToANOTIFYWithinTheDialogEndsOnlyThatSubscription` in `internal/mmtel` answers such a NOTIFY with 481. The participant then holds the conference with a re-INVITE in the same dialog, which the other subscribers see, and the focus sends the participant no NOTIFY of it.
- **A participant that asks for privacy is in the state without its identity.** A focus must take such a request and reveal the identity in no URI of the document (RFC 4575 8.2). A user asks for it with the Privacy header field (RFC 3323 4.2): the values `id`, `user` and `header` each say that the identity of the party is restricted, and they are how that restriction reaches the focus, OIR for one that creates a conference or dials in (TS 24.605 4.6.5) and TIR for one that the focus invites, which asks for it in its 2xx (4.6.3). A UE does not have to ask for any of them: the services of the user put the value there before the focus reads it, OIR in the INVITE of a user whose identity the network restricts (TS 24.607 4.5.2.4) and TIR in the 2xx of one that answers (TS 24.608 4.5.2.9). The AS of a user with OIR writes `id` when the identity alone is restricted and `header` when all the private information that the UE cannot hide itself is, which the subscription option of that user decides (TS 24.607 4.5.2.4 item 1). The AS of a callee reads `header` as the same restriction, and gives the request `id` in its place (4.5.2.9), so the focus keeps the identity of such a party out of the state too. A user in the temporary mode of OIR, whose identity is restricted by default, presents it for one conference with `Privacy: none`. The `entity` of that participant is then `sip:anonymous1@anonymous.invalid`, with the display text `Anonymous1`, and each further one gets a higher number, which the focus never gives again. This is the convention of RFC 4575 5.6: the host is in the `.invalid` top level domain that RFC 2606 2 reserves, which no DNS resolves, and the user part makes the URI unique among the participants. The participant is a user of the state like any other, with an endpoint and a status of its own, so the others see that someone is there and not who. A participant that asks for privacy with `user` also keeps its identity out of the invitation it asks for: the focus sends the INVITE to the referred user without the Referred-By of the REFER, which would name it (TS 24.605 4.6.5). The identity stays with the focus: it is in no NOTIFY, and the focus still knows which subscriber is which participant. `TestPrivacyKeepsTheIdentityOutOfTheConferenceState` in `internal/mmtel` tries all three ways into a conference, and `TestTheCreatorsIdentityRestrictionReachesTheConferenceState` creates a conference for a user whose UE asks for nothing and whose OIR is in the permanent mode, under either subscription option, or in the temporary mode with the identity restricted by default, and for one that presents its identity with `Privacy: none`. The phones of this scenario ask for no privacy, so the capture shows each of them by its identity.
- **The conference ends with its creator** (TS 24.147 5.3.2.7): when phone1 hangs up, the AS sends BYE to phone2 and phone3 and to the MRFC for each party.
- **Every subscription ends with the conference.** The focus sends each subscription that it still has a last NOTIFY whose Subscription-State is `terminated;reason=noresource` (TS 24.147 5.3.3.4, RFC 4575 3.3). A participant may have subscribed within the dialog of its conference session instead, as a UE does when its SUBSCRIBE outside that dialog gets 403 (TS 24.147 5.3.1.2). That participant gets its last NOTIFY in the dialog, and the BYE only once it has answered that NOTIFY: a BYE ends the session, but not a subscription that shares its dialog (RFC 5057 5.5). The phones of this scenario subscribe outside the dialog. `TestTheLastNOTIFYWithinADialogComesBeforeTheBYE` in `internal/mmtel` subscribes within it, for a participant whose identity the network asserts and for one that came in without an asserted identity. A subscription that the focus has already removed after a NOTIFY that went unanswered gets no last NOTIFY.
- **The focus ends the subscription of a participant that leaves** with a last NOTIFY that says `terminated;reason=noresource` (TS 24.147 5.3.3.3), whether the participant subscribed outside the dialog of its conference session or within it. The other subscribers keep their subscriptions and see the participant `disconnected`. How that NOTIFY still finds a dialog whose session a BYE has ended is in [Leaving with a subscription within the dialog](#leaving-with-a-subscription-within-the-dialog).

## How the focus invites a peer

The INVITE that brings phone2 in (#7712 above) does not go straight from the AS to phone2. The focus sends it to the S-CSCF, which routes it as it routes a call of a user:

```text
focus    S-CSCF   HSS      I-CSCF   MMTel AS  P-CSCF   phone2
                                    services
 |        |        |        |        |         |        |  INVITE tel:+12025550102 with Replaces, Route: <sip:scscf...;lr;orig>,
 |        |        |        |        |         |        |  P-Asserted-Identity: <sip:conf-o47ha7ifxyni@conf...>
 |------->|        |        |        |         |        |
 |        |------->|        |        |         |        |  SAR UNREGISTERED_USER, sip:conf-o47ha7ifxyni@conf...
 |        |<-------|        |        |         |        |  SAA, the profile of the PSI user, with no iFCs
 |        |---------------->|        |         |        |  INVITE sip:+12025550102@ims...;user=phone, from ENUM
 |        |<----------------|        |         |        |  after the LIR: to the S-CSCF of phone2
 |        |------------------------->|         |        |  P-Served-User: phone2;sescase=term
 |        |<-------------------------|         |        |  the INVITE of the B2BUA
 |        |----------------------------------->|        |
 |        |        |        |        |         |------->|  INVITE, Contact: <sip:conf-o47ha7ifxyni@conf...>;isfocus
```

- **The focus is an originating UA on behalf of the conference URI** (TS 24.147 5.3.2.2.1, TS 24.229 5.7.3). The conference URI is a PSI that the AS hosts. It is in P-Asserted-Identity, where TS 24.147 5.3.2.5.4 step 2 puts it, and it is also the identity that the S-CSCF serves. The AS does not route the INVITE itself: it puts the S-CSCF on top of Route with the `orig` parameter. That S-CSCF is the one that brought phone1's INVITE to the factory URI.
- **`orig` makes the S-CSCF serve the caller** (TS 24.229 5.4.3.1), as the user part `orig` of a UE's Service-Route does. The caller here is the conference URI, which has no registration. The S-CSCF takes its profile from the HSS with a SAR of `UNREGISTERED_USER`, as for an application server that sends on behalf of a user who is not registered (TS 24.229 5.4.3.2, NOTE 24), and evaluates the iFCs of `ORIGINATING_UNREGISTERED`.
- **The HSS keeps all the conference URIs as one PSI user** (TS 23.228 5.4.12.4): the wildcarded PSI `sip:!.*!@conf.ims.mnc001.mcc001.3gppnetwork.org`, whose part between the two `!` is a regular expression, and the private service identity `conference@ims.mnc001.mcc001.3gppnetwork.org`, which only ties the PSIs to Cx (TS 23.003 13.5 and 13.5A; `psi_users` in `deploy/compose/hss.yaml`). The profile in the SAA names the conference URI with `IdentityType` 2 (`WILDCARDED_PSI`) and the wildcarded PSI it matched (TS 29.228 B.2.1), and has no iFCs. The S-CSCF keeps it for a few minutes, so that the next invitation to the same conference needs no SAR.
- **From there the INVITE goes as phone1's calls go** (TS 23.228 5.4.12.5 c). ENUM turns the number into a SIP URI (TS 24.229 5.4.3.2 step 10), the S-CSCF puts itself in Record-Route, and the I-CSCF finds the S-CSCF of phone2. That is the same S-CSCF: it now serves phone2, and gives the INVITE to the MMTel AS for phone2's terminating services. For a number of another network, the S-CSCF sends the INVITE to the IBCF instead ([A conference across the border](interconnect.md#a-conference-across-the-border)).

## Coming back to the conference

A participant that has left can come back while the conference goes on. It sends an INVITE to the conference URI, which it has from the Contact of the focus (TS 24.147 5.3.1.4.1). Say phone2 hangs up, and then calls the conference URI:

```text
phone2     P-CSCF   S-CSCF   MMTel AS              MRFC
                           services  focus
 | INVITE sip:conf-o47ha7ifxyni@conf...            |
 |-------->|------->|------->|        |            |  P-Served-User: phone2;sescase=orig
 |         |        |<-------|        |            |  the INVITE of the B2BUA, to the same URI
 |         |        |---------------->|            |  by DNS, without P-Served-User
 |         |        |        |        |----------->|  INVITE sip:conf=conf-o47ha7ifxyni@mrfc..., phone2's offer
 |         |        |        |        |<-----------|  200 OK, the answer of the MRFP
 |         |        |<----------------|            |  200 OK, Contact: <sip:conf-o47ha7ifxyni@conf...>;isfocus
 |         |        |------->|        |            |
 |<--------|<-------|<-------|        |            |  200 OK, the same Contact
```

- **The INVITE goes as every call of phone2 goes.** The S-CSCF gives it to the MMTel AS for phone2's originating services, and the AS, a B2BUA, sends a new INVITE to the same URI back. The S-CSCF then finds the focus by DNS, as for the SUBSCRIBE above. From another network, the INVITE comes through the I-CSCF, which sends it by DNS too. No filter criterion brings the INVITE to the focus, so it has no P-Served-User. This is how the AS tells the INVITE for the focus from the INVITE for phone2's services, which has the same Request-URI.
- **The focus takes phone2 in as it takes the creator** (5.3.2.4.1). It asks the MRFC for a new termination of the mix with phone2's offer. Once the MRFC has answered, it answers phone2 with the answer of the MRFP and the conference URI with `isfocus` in Contact. The AS of phone2's services passes that Contact on and puts itself in Record-Route, as for an invited peer, so that phone2's requests to the conference URI still pass it (TS 24.229 5.7.5.1).
- **The NOTIFYs** list phone2 as `connected` again, now with the joining-method `dialed-in`, as for the creator. Before, when the focus invited phone2, it was `dialed-out`.
- **Any user whose identity the network asserts may join**, and so may one whose Privacy holds `id` or `user`, which is anonymous to an AS and needs no asserted identity to be taken in (TS 24.229 5.7.1.4 a), whether or not the user has been in the conference before, up to six participants, the maximum-user-count of the NOTIFYs. The seventh gets 403 (Forbidden). An INVITE to a conference URI that no conference has gets 404 (Not Found) (5.3.2.4.1 step 1). That is what the creator gets too: the conference ended when it left, and its URI with it (5.3.2.7).

The script does not run this. `TestRejoinWithTheConferenceURI` in `internal/mmtel/conference_network_test.go` runs it through the whole IMS in one process: B leaves A's conference, comes back with the conference URI, and hears A through the mixer.

## Leaving with a subscription within the dialog

A UE whose SUBSCRIBE outside the dialog gets 403 subscribes within the dialog of its conference session instead (TS 24.147 5.3.1.2). The dialog then has two usages: the session, which the INVITE made, and the subscription, which the SUBSCRIBE made (RFC 6665 4.5.2). A dialog ends with its last usage (RFC 5057 4), and a BYE ends the session only (5.5). So when such a participant hangs up, its dialog lasts until the NOTIFY that ends the subscription has crossed it. Say a UE that dialed in, as phone2 did above, has subscribed within its dialog and hangs up:

```text
UE         P-CSCF   S-CSCF   MMTel AS
                           services  focus
 |-------->|------->|------->|        |  BYE
 |<--------|<-------|<-------|        |  200 OK from the B2BUA: the session ends, the dialog stays
 |         |        |<-------|        |  BYE on the other leg of the B2BUA
 |         |        |---------------->|
 |         |        |<----------------|  200 OK
 |         |        |------->|        |
 |         |        |<----------------|  NOTIFY Event: conference, Subscription-State: terminated;reason=noresource
 |         |        |------->|        |
 |<--------|<-------|<-------|        |  the same NOTIFY, on the leg of the B2BUA to the UE
 |-------->|------->|------->|        |  200 OK: the UE lets the dialog go
 |         |        |<-------|        |
 |         |        |---------------->|  200 OK: the B2BUA lets both legs go, and the focus the dialog
```

- **The AS of the UE's services answers the BYE** and ends the session on its other leg with a BYE of its own, as it does for every call that it is a B2BUA for. It keeps both legs, though: the SUBSCRIBE went through it within the dialogs, so it knows that a subscription shares them. The focus answers its BYE with 200, and then sends the last NOTIFY in the same dialog (TS 24.147 5.3.3.3). The B2BUA passes the NOTIFY on to the UE, and the 200 of the UE back to the focus.
- **Each end lets the dialog go when the subscription ends, not at the BYE.** The focus forgets the dialog once its NOTIFY has a response. The UE lets it go once it has answered that NOTIFY, or, should the NOTIFY never come, 32 seconds (64 times T1) after the subscription has expired, as a B2BUA does (RFC 5057 4.2).
- **So does every B2BUA on the path.** A B2BUA keeps both legs for as long as a subscription that a SUBSCRIBE within the dialogs created goes on. It lets them go once a NOTIFY that ends the subscription has its response, once a NOTIFY or a refresh fails in a way that ends the subscription, or 32 seconds (64 times T1) after the subscription has expired, which leaves time for the NOTIFY that ends an expired subscription to cross (RFC 6665 4.2.1.4). The MMTel AS of a user's services is such a B2BUA, and so is the IBCF of a conference across the border ([A conference across the border](interconnect.md#a-conference-across-the-border)).
- **The dialog takes no new usage after the BYE.** A SUBSCRIBE that would make another subscription gets 603 (Decline), and so does a REFER, which would make a subscription to the refer event: 603 refuses the new usage and leaves the dialog and its other usages alone (RFC 5057 5.6). A request of the session, such as a re-INVITE, gets 481, since the session is over.

The phones of this scenario subscribe outside the dialog, so the capture does not show this flow. `TestAParticipantThatLeavesGetsTheLastNOTIFYWithinItsDialog` in `internal/mmtel` runs it at the focus, for a participant that joined and for the creator, whose BYE ends the conference as well. `TestASubscriptionWithinTheDialogsOutlivesTheBYE` in `internal/b2bua` runs it through a B2BUA, `TestConferenceStateAcrossTheBorder` in `internal/ibcf` through the IBCFs of two networks, and `TestTheUEKeepsItsDialogForTheLastNOTIFY` in `internal/ue` at the UE.

## Take the MRFC out of service

An operator takes a node out of service without cutting what it serves: the node takes nothing new and goes on with what it has begun, until nothing is left. This is called a drain.

The MRFC has to go on with what it has begun, because each dialog lives in the instance that took its INVITE. The Contact of the dialog names that instance, and the termination on the MRFP, the timers and the wait for the report that an announcement has ended are all in that instance. A commercial network runs several instances of the MRFC behind the one name that the AS has in its configuration, and DNS spreads the new requests over them.

The node of the MRFC serves a maintenance API for this on port 8085, the listener of the `maintenance` section of `deploy/compose/mrfc.yaml`. The API asks for no password, so the port is not published on the host: send the requests from inside the container of the MRFC. The administrative state has the values of TS 28.622 5.3.1. `UNLOCKED` takes new work, `SHUTTINGDOWN` serves only what it has, and `LOCKED` takes nothing new.

1. Shut the MRFC down:

   ```sh
   docker compose exec mrfc sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the MRFC. With no dialog of its own it is `LOCKED` at once:

   ```json
   {"role":"mrfc","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` counts the dialogs that this instance holds, one for every INVITE outside a dialog that it has taken and not yet finished with: an announcement until it has played or the AS has stopped it, and a party of a conference for as long as that party is in the mix. An INVITE that the MRFC refuses for the service its Request-URI names, or for its SDP, is one for the moment it takes to answer it; an INVITE whose Request-URI does not parse at all is answered 400 (Bad Request) before it is counted. With an announcement playing the shut down leaves the MRFC `SHUTTINGDOWN`, and it becomes `LOCKED` once the last of its dialogs has ended; the mixes themselves are in the MRFPs and outlive this node. `operational_state` says whether the MRFC can work (TS 28.622 5.3.3): it is `ENABLED` while the node runs, since the MRFC keeps no association with an MRFP whose state it could report. Every operation it sends an MRFP is a request of its own, and one that an MRFP does not answer shows in the refusal of the INVITE that needed it. `docker compose exec mrfc wget -qO- http://127.0.0.1:8085/maintenance/v1/state` shows the whole node.

2. Have UE 1 bar its own outgoing calls, as in [An announcement before a rejection](#an-announcement-before-a-rejection), and call UE 2:

   ```sh
   docker compose run --rm ue xcap set baoc on
   docker compose run --rm ue call tel:+12025550102
   ```

   The call ends with `603 Decline`, as a barred call does, and UE 1 hears no announcement before it.

```text
 UE 1        P-CSCF      S-CSCF      MMTel AS      MRFC
 |  INVITE     |           |            |            |
 |------------>|---------->|----------->|            |  tel:+12025550102, which OCB bars
 |             |           |            |----------->|  INVITE sip:annc@mrfc...;play=/provisioned/notice
 |             |           |            |  503 Retry-After: 30
 |             |           |            |<-----------|
 |  603        |  603      |  603       |            |  no early media, no Reason
 |<------------|<----------|<-----------|            |
```

- **The AS asks the MRFC for the announcement** with the Request-URI of RFC 4240 3, as it does for a call it rejects when the MRFC is in service.
- **The MRFC answers 503 Service Unavailable** with **Retry-After: 30**, and has an MRFP set up nothing. A request outside a dialog starts something new, which the MRFC does not take while it is not `UNLOCKED`. Retry-After asks the client to send this server no other request for that many seconds (RFC 3261 21.5.4). No specification gives the value; 30 seconds is this project's choice. The response carries the P-Asserted-Identity of the service that was asked for, as every response of the MRFC to an initial INVITE does (TS 24.229 5.8.2.1.1.1).
- **The AS sends the INVITE on to the next instance** that DNS gave it for the name of the MRFC (RFC 3263 4.3), so the 503 reaches the AS itself only once there is none left to try. This network runs one instance.
- **The AS then rejects the barred call without the announcement.** UE 1 gets the 603 straight away, with neither the reliable 183 that would have authorized the early media of the MRFP nor the `Reason` that the AS adds to a rejection the caller has heard an announcement before (TS 24.628 4.2.4). The service itself holds: the call is barred either way, and only the announcement is lost. A customized alerting tone goes the same way, and the caller hears the ringing tone of its own UE while the callee rings.
- **An INVITE that would create a conference or join one is refused.** A conference has no media without a mixer, so the focus takes no participant it cannot connect. The AS rejects it with the status that the MRFC gave, 503, and the party sees 500 (Server Internal Error): a proxy on the way does not pass a 503 on, it answers 500 of its own instead, since the 503 is about the server that sent it and not about the proxy (RFC 3261 16.7 step 6).

While it is not `UNLOCKED`, the MRFC goes on with what it has begun and refuses what would start something new.

It goes on with:

- **Every request within the dialogs it holds**, so an announcement plays to its end and a party stays in its mix: the re-INVITE of a hold, or of a party that leaves the mix and rejoins it (TS 24.229 5.8.4.1.1.1), the BYE with which the AS stops a play (5.8.3.1.1), and the BYE that the MRFC itself sends once the MRFP reports that the play has ended (5.8.3.2.1).
- **An OPTIONS outside a dialog**, which it answers with 200 as ever (RFC 3261 11.2). The request asks what the node can do, and the node is alive.

It answers 503 to **every INVITE outside a dialog**: the announcement of RFC 4240 3, and the party of a conference of RFC 4240 5, even for a conference it already holds a party of. That last refusal costs the party nothing where another instance can take it, which is what tells the MRFC apart from the AS. A conference of the MRFC is tied to no instance of it as long as the instances share one `Store`: it holds which MRFP and which context hold the mix, and every instance knows every MRFP, so a party that another instance takes joins the same mix, beside the parties of the instance that is out of service. That is the deployment of `deploy/redundancy/redundancy.yaml`, where the `store` section of each node names one Valkey server. Instances that each keep the conferences in the memory of their own process share none of it: the instance that took the refused party would find no media for the conference, have an MRFP make a second context, and the mix would be split in two. The Compose project of these guides runs one MRFC, with the conferences in its own memory, so here a party that leaves the mix and rejoins it while the MRFC is out of service is refused outright, and the mix goes on without it. The focus of the conference is the MMTel AS, and it stays where it is ([A conference of three](#a-conference-of-three)). The MRFC takes no SUBSCRIBE at all: the conference event package is in the focus, so a SUBSCRIBE that reaches the MRFC gets 405 (Method Not Allowed), out of service or not.

3. Unlock the MRFC with the same request and `UNLOCKED`, and let UE 1 call again:

   ```sh
   docker compose exec mrfc sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue call tel:+12025550102
   docker compose run --rm ue xcap set baoc off
   ```

   UE 1 hears the announcement before the 603 again. The last command lets its outgoing calls through, for the scenarios that follow.

In Wireshark, `sip.Status-Code == 503 && ip.src == 172.28.0.85` shows the 503 of the MRFC, and `sip.r-uri contains "annc@"` the INVITEs that asked for the announcement. The PUT request travels over the loopback interface of the container, so the capture does not hold it.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-sip.sh` runs these steps and checks the capture.

## Take the MRFP out of service

The MRFP is the same media relay as the node that carries the voice of a call, so an operator drains it the same way, through the maintenance API on port 8085 of `deploy/compose/mrfp.yaml`, with `mrfp` as the role in the path. What the states mean and what the relay goes on with are in [Take a media relay out of service](call.md#take-a-media-relay-out-of-service); `users` counts the contexts of this MRFP, one for each announcement it plays and one for each conference it mixes.

What is its own is where the refusal lands. The MRFC asks an MRFP for a context when it takes an INVITE for an announcement, and when the first party of a conference arrives:

1. Shut the MRFP down, with UE 1's own calls barred over Ut as in [Take the MRFC out of service](#take-the-mrfc-out-of-service), and call UE 2:

   ```sh
   docker compose exec mrfp sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfp/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue xcap set baoc on
   docker compose run --rm ue call tel:+12025550102
   ```

   The call ends with `603 Decline`, as a barred call does, and UE 1 hears no announcement before it, exactly as when the MRFC itself is out of service.

```text
 MMTel AS        MRFC             MRFP
 |   INVITE        |               |  sip:annc@mrfc...;play=/provisioned/notice
 |---------------->|               |
 |                 | GET /announcements/notice
 |                 |-------------->|
 |                 | 200 OK        |  the MRFP holds it, and how long one play lasts
 |                 |<--------------|
 |                 | POST /contexts|  Add: a termination towards the caller
 |                 |-------------->|
 |                 | 503 Service Unavailable
 |                 |<--------------|  out of service, so no context
 |   503           |               |  no other MRFP in the list
 |<----------------|               |
```

- **The MRFC asks whether the MRFP holds the announcement**, and the MRFP answers that it does. That is an audit, not a context, and a drained MRFP answers it as ever.
- **The MRFC asks for the context** that would play the announcement (TS 29.333 5.17.2.2), and the MRFP answers **503 (Service Unavailable)** with the error code `out_of_service`.
- **The MRFC asks the next MRFP of its list**, from the one that the hash of the Call-ID picked, and after the last one the first, as the P-CSCF does for its relays. TS 23.333 6.1.2 has it establish no new connection on an MRFP that is locked for new calls. This network has one MRFP, so the list runs out.
- **The MRFC answers the AS 503** once no MRFP is left. The response carries no `Retry-After`: the MRFC itself is in service, and `Retry-After` would ask the AS to send the MRFC nothing for that long (RFC 3261 21.5.4).
- **The AS serves the call without the MRF.** It rejects the barred call without the early media it would have authorized, and the 603 carries no `Reason`. A customized alerting tone goes the same way, and a user that was to join a conference is refused, since a conference has no media without a mixer.

A party of a conference whose mix is already in a drained MRFP still joins it: the mix is in that MRFP and nowhere else, so the MRFC asks it for a termination in the context it holds, and the MRFP adds one. That adds no context, so the drain still ends when the last conference and the last announcement of this MRFP are over.

2. Unlock the MRFP with the same request and `UNLOCKED`, and let UE 1 call again:

   ```sh
   docker compose exec mrfp sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/mrfp/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue call tel:+12025550102
   docker compose run --rm ue xcap set baoc off
   ```

   UE 1 hears the announcement before the 603 again. The last command lets its outgoing calls through, for the scenarios that follow.

In Wireshark, `http.response.code == 503 && ip.src == 172.28.0.86` shows the refusals of the drained MRFP, and `sip.Status-Code == 503 && ip.src == 172.28.0.85` the 503 that the MRFC passes to the AS.

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

`scripts/e2e-mrf.sh` runs every filter of this table against the capture of this scenario: a filter that finds no frame fails the run.

## Where the code is

| File | What it holds |
| --- | --- |
| `internal/mmtel/mrf.go`, `internal/mmtel/announcement.go` | The AS's dialog with the MRFC, and the announcement before a rejection. |
| `internal/mmtel/cat.go`, `internal/mmtel/catdata.go` | CAT at the terminating AS, and its data in the HSS. |
| `internal/mmtel/waiting.go` | CW at the AS. |
| `internal/mmtel/conference.go`, `internal/mmtel/conferenceevent.go` | The focus and the conference event package. |
| `internal/scscf/session.go`, `internal/scscf/unregistered.go`, `internal/hss/psi.go` | The S-CSCF that serves a request on behalf of the conference URI, and the PSI user of the HSS. |
| `internal/mrfc`, `internal/netann` | The MRFC and the Request-URIs of RFC 4240. |
| `internal/mrfc/maintenance.go` | What the MRFC tells the maintenance API of its node, and what it refuses while the operator has it out of service. |
| `internal/agw` | The MRFP: terminations, announcements and the mixer, and its control API. |
| `internal/agw/maintenance.go` | What a media relay tells the maintenance API of its node, and what it refuses while the operator has it out of service. |
| `internal/ue/phone_conference.go`, `internal/ue/conference.go` | Swap, Merge and the UE side of a conference. |
| `internal/sip/call.go`, `internal/b2bua/subscription.go` | A dialog that lives on after its BYE for the subscriptions that share it, and the B2BUA that keeps both its legs for them. |
| `internal/mmtel/mrf_network_test.go`, `internal/mmtel/conference_network_test.go`, `internal/mmtel/conference_border_test.go` | The same flows through the whole IMS in one process, the last with a peer of another network. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| MRFC, TS 23.333 5.12 | The MRFP plays and mixes G.711 PCMU only and transcodes nothing. 5.12.1 asks an MRFP to transcode and to support AMR at least, and lets it support the other codecs of TS 26.114, such as AMR-WB. An offer without PCMU gets 488 with Warning 305. | A conference mixes decoded speech, and the product has no AMR decoder. Adding one would cost much and change little of the signalling these guides follow. The UE simulators still offer PCMU. |
| MRFC, RFC 4240 3 and TS 24.229 10.3.2.2 | Only provisioned announcements; no prompt is fetched from a URL over Cr. | The WAV files are in the MRFP's configuration. |
| MRFC, TS 24.229 5.8.1 | The Contact is the SIP URI of the instance, not a GRUU. | Nothing sends a request to the MRFC outside a dialog it has with it. |
| MRFC, TS 24.229 5.8.2.1.1.1 and RFC 4240 2 and 8 | No charging data, sips: is not enforced, no AS is authenticated. | No node of the project writes charging records, and nothing is encrypted so that the capture shows the content. |
| MRFC, RFC 4240 3 | The suggested reason phrases go into a Warning. | The UA writes the standard phrase in the status line. |
| MRFC, TS 23.333 6.1.2 and 6.1.3 | The MRFC keeps no list of the MRFPs that are unavailable, and audits none of them to find out when one is back, where 6.1.2 asks it to prevent the usage of an MRFP until that MRFP has recovered. Each request tries the MRFP that its Call-ID, or the conference ID, picks, and one whose MRFP has stopped waits for that Add to fail before it asks the next one. | An MRFP answers from memory, so the wait is one failed request, and the capture then shows both the refusal and the request that followed it. |
| MRFC, TS 23.333 6.1.5.1 and TS 29.333 5.17.3.5 | An MRFC instance that is killed, or whose process exits before its dialogs have subtracted their terminations, leaves those terminations at the MRFP. A node that is told to stop ends its dialogs without a BYE, and does not wait for the dialogs to subtract their terminations. An instance that starts again under the same name sends no ServiceChange to say that it has restarted. The terminations that are left stay at the MRFP with their ports open, and a context that mixes a conference keeps its clock. The conference stays in the Store, since its context never empties, and a party that joins it later is mixed with the terminations that are left. | Only the dialog that added a termination subtracts it: when the dialog ends, or when the MRFC gives up a conference context that it has just created. The MRFC keeps no list of its terminations outside its process. Neither the Mp operations of the MRFC nor the HTTP control API through which the node drives its MRFPs carry a restart indication. |
| netann, RFC 4240 3 | locale, content-type and param1 to param9 are ignored. | Each announcement has one variant. |
| MRFP, TS 23.333 5.10 and RFC 3550 6 and 7.1 | The mixer takes PCMU only, keeps three frames for jitter, writes no CSRC list and sends no RTCP. | Enough for a conference of a few UEs on one bridge. |
| MRFP, RFC 3550 6 | No RTCP sender reports for the media it makes. | The UE simulator needs none. |
| MRFP, TS 29.333 5.17.2.9 | An MRFP created again under the same name opens the ports of its contexts again, but plays nothing in them: an announcement or a tone that was playing does not start again, and the MRFC gets no event for an end that never comes. The media the MRFP makes itself comes from a new RTP source after the gap, and the packet counts of the terminations start at zero. | How far a termination had got through an announcement is not kept with the context. The MRFC starts the next announcement of a call itself, and a conference goes on being mixed. |
| AS, TS 24.182 4.5.5.3.7 | A callee's UPDATE with an offer after its 180 goes on to the caller, which moves the caller away from the tone. | The UE simulator sends none then. |
| AS, TS 24.182 4.5.5.3.7 b 2 and b 3 | The callee's 183 with preconditions goes to the caller as it came; the tone starts without a look at the caller's preconditions. | The callee has alerted, so both sides have their resources. |
| AS, TS 24.147 5.3.2.3.1 step 2 and TS 24.229 5.7.1.4 and 5.7.1.5 | Every served user may create a conference; no conference policy, no charging addresses. | There is no conference policy server, and no node of this project writes charging records. |
| AS, TS 24.147 5.3.2.4.1 step 2 and TS 24.229 5.7.1.4 and 5.7.1.5 | The focus takes the P-Asserted-Identity of the INVITE as the user's identity, as it takes the identity that the S-CSCF asserts for the creator, and checks nothing more. It does not check that the INVITE came from a node of the trust domain (TS 24.229 5.7.1.4 b i, RFC 3325 8). Any user may join a conference that has room. An INVITE that has no P-Asserted-Identity and whose Privacy holds neither `id` nor `user` gets 403, without a challenge. | There is no conference policy, and the AS holds no credentials of users. In the networks of this project, every request reaches the focus with the identity that a P-CSCF or a trusted peer network asserted. |
| AS, TS 24.229 5.7.3 NOTE 4 | The S-CSCF that hosts the conference URI is taken to be the one of the creator's INVITE, not asked of the HSS over Sh. | The configuration names one S-CSCF, which serves every user and PSI of the network. When it runs as several instances, they share one name and keep their state in one store. |
| AS, TS 24.147 5.3.2.6.2.2 | A REFER that asks the focus to remove a participant (method BYE) is refused with 403. | IR.92 2.3.3 does not ask for it. |
| AS, RFC 4575 3.9 | Each change is notified at once, not at most once in 5 seconds. | IR.92 2.3.3 asks for notifications within 5 seconds, and a small conference changes seldom. |
| AS, TS 24.147 5.3.3.2 step 2 and RFC 4575 3.5 | Every subscriber whose identity the network asserts may subscribe. | There is no conference policy. |
| HSS, TS 29.228 6.1.2.1 | A PSI user has no state in the HSS: no S-CSCF name, no registration state and no PSI Activation State. The HSS answers the SAR of any S-CSCF for it. A PSI user is a range of SIP URIs with no iFCs. | The S-CSCF asks for the profile of a PSI only when the AS that hosts it sends a request on its behalf. A request to a PSI finds the AS by DNS, so no node asks the HSS where a PSI is served. |
| UE, RFC 3891 3 and RFC 3892 | The Referred-By carries no Authenticated Identity Body. | The referrer's network asserted the identity to the focus. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
