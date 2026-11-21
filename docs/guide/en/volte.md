# The VoLTE call flow

[日本語](../ja/volte.md)

This guide follows what a VoLTE phone adds to the softphone of [Follow a UE registering with the IMS](registration.md) and [Follow a voice call through the IMS](call.md): the access network, IMS AKA with the security agreement, policy control over Rx, and a voice call the way a VoLTE phone makes it. Two UE simulators attach to a stub of the EPC, register with IMS AKA, and call each other in AMR-WB with preconditions; the PCRF sets up a voice bearer for each of them. Then the network takes one of the bearers away and the P-CSCF ends the call.

The guide follows these editions: TS 24.229 V20.1.0, TS 29.214 V20.0.0 (Rx), TS 29.213 (the SDP to Rx mapping), TS 24.301 V20.1.0 (the EPS messages), TS 26.114 V20.1.1 (media handling), TS 33.203 and TS 33.102 (IMS AKA), TS 35.206 and TS 35.208 (MILENAGE), RFC 3310 (Digest AKA), RFC 3329 (sec-agree), RFC 3312 (preconditions), RFC 4867 (AMR payload format), RFC 4028 (session timer) and GSMA IR.92 v24.0. Clause numbers below refer to those editions.

## Before you start

- You have read [Follow a UE registering with the IMS](registration.md), [Follow a voice call through the IMS](call.md) and [Supplementary services with the MMTel AS](mmtel.md). This guide reuses what they show: the REGISTER through the I-CSCF and the S-CSCF, the INVITE, PRACK and ACK of a call, the media relay, and the three dialogs that the MMTel AS makes of each call.
- The abbreviations are explained in the [glossary](../../glossary.md).

## How a VoLTE phone differs from a softphone

The UE of the other guides is a softphone: its P-CSCF address is configured, it authenticates with SIP Digest and a password, and it offers G.711 (PCMU). A VoLTE phone (IR.92) works differently in five ways:

1. **The access network.** The phone first attaches to the EPC. The network gives it an IP address, the P-CSCF address and a default bearer for SIP. For each call it sets up a second bearer for the voice (IR.92 4.4).
2. **IMS AKA.** The phone proves that it holds the keys of its USIM, the network proves that it knows them too, and both agree on security associations (TS 33.203 6.1.1, 7). There is no password.
3. **Policy control.** The P-CSCF tells the PCRF over Rx which media each call needs, and the PCRF has the access network reserve it (TS 29.214 4.4.1). When the access network loses the bearer, the PCRF tells the P-CSCF, and the P-CSCF ends the call (TS 24.229 5.2.8.1.2, IR.92 2.4.2.3).
4. **The VoLTE call.** The callee does not ring until both sides have their bearers (RFC 3312, IR.92 2.4.1). The speech is AMR-WB (IR.92 3.2.1), with RTCP, DTMF as telephone-events, a session timer and a Reason in the BYE.
5. **The identities.** The phone's card has a USIM and no ISIM, so the phone derives its identities from the IMSI. It registers a temporary public identity that the network bars, and calls with the default public identity that the network gives back (see "The identities of a phone without an ISIM").

The softphones keep working as before. A UE that does not come through the EPC stub is unknown to the PCRF, and the P-CSCF serves it without policy control.

## The nodes

| Node | Address | What it does in this guide |
| --- | --- | --- |
| volte-a | 172.28.0.120 | The caller, `tel:+12025550110`. Subscriber 10, whose USIM holds the K and OPc of MILENAGE test set 1 (TS 35.208 4.3.1). |
| volte-b | 172.28.0.121 | The callee, `tel:+12025550112`. Subscriber 12, with the K and OPc of test set 3 (4.3.3). |
| ue2 | 172.28.0.101 | A softphone-like UE that offers PCMU only and uses SIP Digest, `tel:+12025550102`. |
| SIPp | 172.28.0.102 | An independent user agent that registers with IMS AKA as subscriber 11. |
| EPC stub and PCRF | 172.28.0.80 | The `epc` role: the UE API on HTTP port 8080, and the PCRF on Diameter port 3868 (`pcrf.ims.mnc001.mcc001.3gppnetwork.org`). |
| P-CSCF | 172.28.0.40 | SIP on 5060, and the protected server port 5064 for UEs with IMS AKA. The Rx client of the PCRF. |
| S-CSCF | 172.28.0.30 | Challenges with IMS AKA. |
| HSS | 172.28.0.10 | Makes the authentication vectors (MAR and MAA). |
| MMTel AS | 172.28.0.75 | Takes every call, as in [Supplementary services with the MMTel AS](mmtel.md). |
| Media relay | 172.28.0.50 | Relays RTP, RTCP and the telephone-events without decoding them. |
| Dashboard | 172.28.0.60 | Drives volte-a and volte-b, shows Rx in the ladder, and passes on to the host the EPC stub's paths that start a procedure of the network. |

volte-a and volte-b are services of the `volte` profile in `compose.yaml`. Their keys are in `deploy/compose/hss.yaml`, and `deploy/compose/epc.yaml` gives each subscriber the address of its container.

## The access network: the EPC stub

### What the stub leaves out

A real EPC carries NAS messages over S1 between the UE, the eNodeB and the MME, and tunnels the UE's packets over GTP-U to the PDN GW. The stub keeps the messages and their names and drops the rest:

- The UE and the stub exchange the ESM and EMM messages of TS 24.301 (8.2, 8.3) as JSON over HTTP. The names are those of the spec, written in lower case in the URL: `POST /ues/001010000000010/attach-request`. What the network sends by itself arrives on the UE's event stream, `GET /ues/<IMSI>/events`, as server-sent events.
- Each subscriber has a static address (TS 23.401 5.3.1.1), which its container already has on the Docker network. Nothing is tunnelled: the UE's packets go straight to the P-CSCF and the relay.
- The PCRF and the PDN GW share the state of the PDN connection inside the `epc` role, instead of talking Gx (TS 29.212).
- There is no radio, no NAS security and no authentication of the attach. A bearer is a record: the stub enforces no QoS and filters no packets.

The `ue` command attaches when it gets `-epc` with the URL of the stub. The IMSI is the user part of `-impi`.

### Attach

In one run of `scripts/e2e-volte.sh`:

```text
#681  volte-a -> EPC  GET  /system-information                 the cell: MCC 001, MNC 01, TAC 1, ECI 257
#685  volte-a -> EPC  GET  /ues/001010000000010/events         the event stream stays open
#690  volte-a -> EPC  POST /ues/001010000000010/attach-request ATTACH REQUEST with a PDN CONNECTIVITY REQUEST for APN ims
#692  EPC -> volte-a  200  ATTACH ACCEPT with ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
#694  volte-a -> EPC  POST /ues/001010000000010/attach-complete
```

The ATTACH ACCEPT carries the default bearer of the IMS APN in its ESM message container:

```json
"esm_message_container": {
  "eps_bearer_identity": 5,
  "eps_qos": {"qci": 5},
  "access_point_name": "ims",
  "pdn_address": {"pdn_type_value": "IPv4", "ipv4_address": "172.28.0.120"},
  "protocol_configuration_options": {"containers": [{"id": "000CH", "contents": "172.28.0.40"}]}
}
```

- EPS bearer 5 is the default bearer, with QCI 5, the class of IMS signalling (IR.92 4.4.1, TS 23.203 6.1.7).
- The PDN address is the subscriber's static address. The UE checks that it is the address it reaches the P-CSCF from, and stops if not.
- The protocol configuration options (PCO) carry the P-CSCF address in a container with the identifier 000CH (TS 24.008 10.5.6.3). This is method II of TS 24.229 L.2.2.1, which IR.92 4.5 asks for. The first address of the list is the preferred one. The PCO gives an address only, so the UE sends to port 5060 of it.

The UE also writes the cell into P-Access-Network-Info, `3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101`: MCC 001, MNC 01, the TAC in 4 hexadecimal digits and the ECI in 7 (TS 24.229 7.2A.4.3, L.3.1.1).

### Bearers

The voice bearer comes with each call, when the PCRF has authorised its media (TS 24.229 L.2.2.5.1). The stub sends it on the UE's event stream:

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80,"maximum_bit_rate_for_downlink":41,
                  "guaranteed_bit_rate_for_uplink":80,"guaranteed_bit_rate_for_downlink":41},
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... RTP and RTCP to and from the relay ... ]}}
```

QCI 1 is the guaranteed bit rate class of conversational voice (IR.92 4.4.2). The rates are in kbit/s and come from the bandwidth that the P-CSCF asked for. The traffic flow template (TFT) has four packet filters: RTP and RTCP, each way, between the UE and its termination on the relay. The UE answers with `POST .../activate-dedicated-eps-bearer-context-accept` and takes the bearer as the resources of its call (IR.92 2.4.3.1). When the call ends, `DEACTIVATE EPS BEARER CONTEXT REQUEST` comes on the stream, and the UE answers `deactivate-eps-bearer-context-accept`.

## IMS AKA, step by step

### The keys

| Who | Holds | Where |
| --- | --- | --- |
| The USIM of volte-a | K, OPc and SQN_MS | `-k`, `-opc` and `-sqn` of the `ue` command (`compose.yaml`) |
| The HSS | K, OPc, AMF and SQN_HE of the same subscriber | `aka:` of subscriber 10 in `deploy/compose/hss.yaml` |

K and OPc are those of MILENAGE test set 1. OPc is derived from the operator's OP and K (TS 35.206 4.1); `-op` takes the OP instead, as SIPp does. Nothing else is shared: the network and the UE each compute the rest from these and the random challenge.

### The ladder

```text
#698  volte-a:5060 -> P-CSCF:5060  REGISTER   Security-Client, Require: sec-agree, Authorization with an empty response
#709  P-CSCF -> I-CSCF             REGISTER   Authorization: ... integrity-protected="no"
#712  I-CSCF -> HSS                UAR / UAA
#721  I-CSCF -> S-CSCF             REGISTER
#724  S-CSCF -> HSS                MAR        SIP-Auth-Data-Item: SIP-Authentication-Scheme Digest-AKAv1-MD5
#726  HSS -> S-CSCF                MAA 2001   SIP-Authenticate (RAND, AUTN), SIP-Authorization (XRES), Confidentiality-Key, Integrity-Key
#727  S-CSCF -> I-CSCF             401        WWW-Authenticate: nonce, algorithm=AKAv1-MD5, ik, ck
#734  I-CSCF -> P-CSCF             401
#737  P-CSCF:5060 -> volte-a:5060  401        ik and ck taken out, Security-Server added
#740  volte-a:5062 -> P-CSCF:5064  REGISTER   Security-Client, Security-Verify, Authorization with the response from RES
#749  P-CSCF -> I-CSCF             REGISTER   Authorization: ... integrity-protected="yes"
#752  I-CSCF -> HSS                UAR / UAA
#760  I-CSCF -> S-CSCF             REGISTER
#764  S-CSCF -> HSS                SAR / SAA
#771  S-CSCF -> I-CSCF             200 OK
#776  I-CSCF -> P-CSCF             200 OK
#783  P-CSCF:5064 -> volte-a:5062  200 OK
#786  P-CSCF -> PCRF               AAR        the signalling path of 172.28.0.120 (see "Policy control")
#793  PCRF -> P-CSCF               AAA 2001
```

1. **REGISTER without protection (#698).** The UE sends its first REGISTER from port 5060 to the P-CSCF's port 5060, the only REGISTER that goes there (TS 24.229 5.1.1.2.2). It has `Require: sec-agree` and `Proxy-Require: sec-agree` and offers its security associations in `Security-Client: ipsec-3gpp;alg=hmac-sha2-256-128;ealg=null;spi-c=...;spi-s=...;port-c=52975;port-s=5062` (RFC 3329 2.3.1, TS 33.203 Annex H). Its Authorization has the private identity as username, `algorithm=AKAv1-MD5`, and an empty nonce and response.
2. **The P-CSCF keeps the Security-Client (#709).** It sees Security-Client with sec-agree and chooses IMS AKA (TS 24.229 5.2.2.1). It takes Security-Client out, keeps it, and marks the Authorization `integrity-protected="no"`: nothing has protected this REGISTER (5.2.2.2).
3. **The S-CSCF asks for a vector (#724).** `integrity-protected="no"` means a first registration with IMS AKA (5.4.1.1). The MAR asks for one item with the scheme `Digest-AKAv1-MD5` (TS 29.229 6.3.9).
4. **The HSS makes the vector (#726).** It raises SQN_HE, draws RAND, and computes XRES, CK, IK and AUTN with MILENAGE (TS 33.102 6.3.2, TS 35.206). SIP-Authenticate is RAND followed by AUTN, 32 octets; SIP-Authorization is XRES; Confidentiality-Key and Integrity-Key are CK and IK (TS 29.228 6.3).
5. **The 401 (#727).** The S-CSCF writes RAND and AUTN into the nonce, base64 encoded (RFC 3310 3.2), and IK and CK in the `ik` and `ck` parameters for the P-CSCF (TS 24.229 5.4.1.2.1A). It keeps RAND and XRES for the answer.
6. **The P-CSCF sets up temporary associations (#737).** It takes `ik` and `ck` out of the 401, so that the keys never travel to the UE, and offers its own associations in `Security-Server: ipsec-3gpp;q=0.1;alg=hmac-sha2-256-128;ealg=null;spi-c=...;spi-s=...;port-c=50711;port-s=5064` (5.2.2.2, TS 33.203 7.2).
7. **The UE checks the network (#740).** Its USIM computes the MAC of AUTN and compares it with the one in AUTN: a match shows that the network knows K. It checks that the SQN is fresh (TS 33.102 6.3.3, C.2), and computes RES, CK and IK.
8. **REGISTER over the associations (#740).** The UE answers from its protected server port 5062 to the P-CSCF's protected server port 5064 (TS 33.203 7.1). It repeats its Security-Client and mirrors the Security-Server in `Security-Verify`, so that a man in the middle cannot have removed a stronger offer (RFC 3329). The response of the Digest is computed with RES as the password (RFC 3310 3.3).
9. **The P-CSCF checks the agreement (#749).** The REGISTER came to the protected port from the UE's address and port, and Security-Verify matches what it sent. A REGISTER over 1300 bytes comes over TCP from another port, and the P-CSCF takes the UE's port from its Via instead. It marks the Authorization `integrity-protected="yes"`.
10. **The S-CSCF checks RES (#760, #764).** It computes the Digest with XRES and compares (TS 24.229 5.4.1.2.2). They match, so it sends the SAR and registers the UE.
11. **The 200 OK (#783).** It goes back over the associations, which now become established; their lifetime follows the registration (5.2.2.2). From now on the UE sends every request to port 5064, with that port in the P-CSCF URI of its Route, and the P-CSCF sends to the UE at 5062; the Path that the P-CSCF added names its protected port.
12. **The signalling path (#786).** Once the UE is registered, the P-CSCF asks the PCRF to tell it when the UE loses its signalling bearer (TS 29.214 A.4). It does so after the initial registration has succeeded, as the flow of TS 29.213 B.1 shows, so a UE that fails to authenticate opens no Rx session. The 200 OK and this AAR leave the P-CSCF in either order.

A refresh and the deregistration go over the associations without a new challenge (TS 24.229 5.1.1.4.2, 5.1.1.6.2).

### Check the vector by hand

Every value of the computation is in the capture. In the run above:

- The nonce of the 401 is `sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc=`. `echo sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc= | base64 -d | od -An -tx1` gives the 32 octets of SIP-Authenticate in the MAA: RAND `b14af726ac27dc0886a6a0c6c789ac2c`, then AUTN `46421d52b3db 0000 9490137df8f39cd7`.
- AUTN is SQN ⊕ AK (6 octets), AMF (2 octets) and MAC-A (8 octets) (TS 33.102 6.3.2). The AMF is `0000`, as hss.yaml sets it for this subscriber. SQN is hidden by AK, which only K and RAND give.
- The `ck` and `ik` of the 401 from the S-CSCF are the Confidentiality-Key and Integrity-Key of the MAA.
- The response of the second REGISTER is RFC 2617 Digest with the 8 octets of XRES as the password (RFC 3310 3.3). With the values of that REGISTER (username, realm, nonce, nc, cnonce, qop and uri) and the SIP-Authorization of the MAA, `927f738d28dc1790`, a shell computes the same response:

  ```sh
  user=001010000000010@ims.mnc001.mcc001.3gppnetwork.org
  realm=scscf.ims.mnc001.mcc001.3gppnetwork.org
  nonce=sUr3Jqwn3AiGpqDGx4msLEZCHVKz2wAAlJATffjznNc=
  ha1=$( { printf '%s:%s:' "$user" "$realm"; printf '\x92\x7f\x73\x8d\x28\xdc\x17\x90'; } | md5sum | cut -d' ' -f1)
  ha2=$(printf 'REGISTER:sip:ims.mnc001.mcc001.3gppnetwork.org' | md5sum | cut -d' ' -f1)
  printf '%s:%s:00000001:OPSWEFQXM5HAX4ZR:auth:%s' "$ha1" "$nonce" "$ha2" | md5sum
  # 3f158cbe664b17c52742ad50ff25d9f5, the response in the capture
  ```

The RAND of a capture is random, so MILENAGE gives other outputs than the tables of TS 35.208. The tables check the computation itself: subscriber 10 has the K and OPc of test set 1, and with the RAND, SQN and AMF of that set the HSS's code gives exactly the outputs of TS 35.208 4.3.1:

```sh
go test ./internal/aka -run TestAuthenticationVectorFromTestSet1 -v
go test ./internal/milenage -run TestConformanceTestSets -v   # all 20 sets of TS 35.208 4.3
```

### The security agreement without ESP

The standard protects everything after the 401 with IPsec ESP: two pairs of security associations between the UE's and the P-CSCF's protected ports, with the keys derived from IK and CK (TS 33.203 7.1). This project agrees on the associations, keeps them in the P-CSCF's SA table with IK and CK, and uses the protected ports, but sets up no ESP. A message counts as protected when it comes to the protected server port from the address and port of a set of associations. The Security-Client says `ealg=null`, and nothing is encrypted or integrity protected on the wire.

Captures are readable by default, so that a learner sees the SIP of a registered VoLTE UE in Wireshark as plainly as that of a softphone. ESP would also need NET_ADMIN in the UE and P-CSCF containers. The standard has no IMS AKA without protection: without IPsec it has only AKAv2-SHA-256 over TLS (TS 24.229 4.2B.1), so this is a simplification of its own. This implementation turns on neither ESP nor TLS.

Without ESP, two things differ from a real network:

- Each side sends from its protected server port, so the protected client ports (`port-c`) are agreed and never bound.
- A request over TCP comes from a port of the moment. The VoLTE INVITE is larger than 1300 bytes and goes over TCP (RFC 3261 18.1.1), so the P-CSCF knows the UE of a TCP request by the sent-by of its Via. A REGISTER that answers a challenge is close to 1300 bytes, so whether it goes over TCP depends on how many digits its SPIs have. TCP and UDP share the security associations (TS 33.203 7.1), so the P-CSCF finds them for that REGISTER by the sent-by of its Via too. Nothing authenticates that sent-by, so a host that shares the UE's IP address could name the UE's protected server port in it; with ESP the security associations would authenticate the sender.

Only a message that comes to the protected server port counts as protected. A request other than REGISTER that comes to port 5060 from the address and port of a UE with established associations is therefore unprotected. The P-CSCF discards it: it sends nothing back, not even a 100 (Trying), and forwards it nowhere. It logs `discarded an unprotected request from a UE with security associations` (TS 24.229 5.2.1 NOTE 8, TS 33.203 7.1).

### SIPp

SIPp registers with IMS AKA from `images/peer/sipp/register-aka.xml`. It sends everything from one port, 5060, which is therefore its protected server port too. SIPp 3.7 takes its K and OP as the bytes of their text, so subscriber 11 has printable keys, `ims-practice-K01` and `ims-practice-OP1`; hss.yaml has them in hexadecimal, with the OPc that MILENAGE derives from that OP. SIPp computes the RES and the Digest itself, which checks the S-CSCF's computation against code written by others.

## The identities of a phone without an ISIM

The card of volte-a and volte-b has a USIM and no ISIM. A USIM holds the IMSI and the keys, but no IMS identity, so the phone makes its identities from the IMSI, here 001010000000010 (TS 24.229 5.1.1.1A, TS 23.003 13.2, 13.3 and 13.4B):

| Identity | Value | Where it appears |
| --- | --- | --- |
| Home network domain | `ims.mnc001.mcc001.3gppnetwork.org` | The Request-URI of REGISTER. |
| Private user identity | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | `username` of Authorization, and User-Name on Cx. |
| Temporary public user identity | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | From and To of REGISTER, and Public-Identity on Cx. Nowhere else. |

The temporary public identity carries the IMSI, so it stays inside the network: TS 23.228 E.3.1 strongly recommends that the operator bar it, and then it serves registration only, only the CSCFs and the HSS see it, and the phone does not show it to the user. The HSS of this network bars it: the subscription in `deploy/compose/hss.yaml` lists it under `barred_identities`, and the user profile in the SAA marks it with `<BarringIndication>` (TS 29.228 B.2.1). The same implicit registration set holds the identities of the user's number, `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` and `tel:+12025550110`, which are not barred.

Each node plays its part:

1. **HSS.** The UAR for the temporary identity succeeds, because its implicit registration set has identities that are not barred. A set with no identity but barred ones would get DIAMETER_AUTHORIZATION_REJECTED (TS 29.228 6.1.1.1 step 4).
2. **S-CSCF at registration.** The default public identity is the first identity of the profile that is not barred (TS 29.228 6.5.1.1). The 200 OK lists in P-Associated-URI only the identities that are not barred, the default first (TS 24.229 5.4.1.2.2F), and the reg event reports only those (5.4.2.1.2). The third-party REGISTER to an application server names the default identity in To (5.4.1.7).
3. **UE.** It stores the first URI of P-Associated-URI as its default public identity. The identity it registered is missing from the list, so it takes that one as barred (5.1.1.2.1). It writes the default identity in the From of its INVITEs and MESSAGEs and subscribes to its reg event (5.1.1.3). The status of the phone shows the default identity, and no identity before the phone has registered: a UE shows the barred temporary identity to nobody (5.1.1.1A). Until then the phone also refuses requests over Ut, which need the default identity as the XUI.
4. **P-CSCF.** It asserts the identity that a P-Preferred-Identity of the UE names, when that is one of P-Associated-URI, and otherwise the default identity (5.2.6.3.1). The UE simulator sends no P-Preferred-Identity, so P-Asserted-Identity is the default identity.
5. **S-CSCF for every other request.** A request whose served user is a barred identity gets 403 (5.4.3.2 step 1), and a request to a barred identity gets 404 (5.4.3.3 step 1). An application server that asks the HSS for the identities of the number over Sh does not get the barred one either (TS 29.328 7.6.2).

In the capture of the VoLTE call:

```text
REGISTER  volte-a -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte-a  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte-a -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
MESSAGE   volte-a -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
```

The Wireshark filter `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` finds no packet. The IMSI is not in the Contact either: `sip.Contact contains "001010000000010"` finds no packet. TS 24.229 5.1.1.2.1 item c asks that the user part of the URI in Contact be globally unique and reveal nothing private, so the UE puts a UUID there that it derives from its private identity with K as the key. Every UE of this project does the same, a softphone with its password as the key. The softphones of the other guides have no SIM card at all: their identities are configured in them, as an IMC holds them (TS 24.229 5.1.1.1B.1). They register a public identity that is not barred and use it as it is.

ENUM turns the number of a VoLTE UE into the SIP URI of the number, not into the temporary public identity, so that a call to `tel:+12025550112` reaches an identity that may receive calls (`deploy/compose/dns/e164.arpa.zone`).

## Policy control over Rx

The P-CSCF is the application function (AF) of policy and charging control; the PCRF decides what the access network carries (TS 29.214 4). Each Rx session is a Diameter session of its own: one per registered UE for the signalling path, and one per call and side for the media.

### The signalling path

The AAR right after the registration (#786) has one Media-Component-Description with Media-Component-Number 0, Flow-Number 0 and `Flow-Usage: AF_SIGNALLING`, two `Specific-Action` values, INDICATION_OF_LOSS_OF_BEARER (2) and INDICATION_OF_RELEASE_OF_BEARER (4), and `Framed-IP-Address` 172.28.0.120 (TS 29.214 4.4.5). The PCRF binds it to the UE's IP-CAN session by that address and answers 2001.

For a UE that did not attach, such as ue2 or pjsua, the PCRF finds no IP-CAN session and answers with the Experimental-Result-Code IP-CAN_SESSION_NOT_AVAILABLE (5065). The P-CSCF then serves that UE without policy control, and sends no AAR for its calls. The registration of every softphone therefore shows this AAR and its 5065 too.

### The media of a call

The P-CSCF of each side sends an AAR when it has the SDP answer of the call (TS 29.214 A.1): the callee's P-CSCF when the 183 comes from volte-b, the caller's when the 183 reaches it on the way to volte-a. The AAR of the callee's side in the run above (#1036):

| AVP | Value | Meaning |
| --- | --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` | The service, from P-Asserted-Service, which the S-CSCF sets. Only the callee's P-CSCF sends it (see "What this project simplifies"). |
| `Media-Component-Description` | number 1 | The first m= line (TS 29.213 6.2 Table 6.2.1). |
| `Media-Sub-Component` | flows 1 and 2 | RTP and RTCP, each with two `Flow-Description` rules: `permit in 17 from 172.28.0.121 to 172.28.0.50 40004` and `permit out 17 from 172.28.0.50 to 172.28.0.121 60734`. The other end of the media is the relay. |
| `Media-Type` | AUDIO | |
| `Max-Requested-Bandwidth-UL` and `-DL` | 80000 and 41000 | From the b=AS lines of the offer and the answer, in bit/s. |
| `RR-Bandwidth`, `RS-Bandwidth` | 4000, 0 | From b=RR and b=RS. |
| `Flow-Status` | ENABLED (2) | From the direction attributes: sendrecv. |
| `Codec-Data` | `uplink offer m=audio ...` and `downlink answer m=audio ...` | The m= lines and attributes of the offer and the answer. |
| `Specific-Action` | 2, 4, 9 | Report the loss and the release of the bearer, and a failed allocation. |
| `Service-Info-Status` | FINAL_SERVICE_INFORMATION | Based on the answer, not on the offer only. |
| `Framed-IP-Address` | 172.28.0.121 | The UE, so that the PCRF finds its IP-CAN session. |
| `Rx-Request-Type` | INITIAL_REQUEST | A new Rx session for the call. |

The PCRF authorises the audio with QCI 1 (TS 29.213 6.3, IR.92 4.4.2), the stub sets up the dedicated bearer, and the PCRF answers 2001. Each later offer and answer with a new SDP, such as the UPDATE's or a hold's, gives an AAR with `Rx-Request-Type: UPDATE_REQUEST` on the same session. On hold the caller's side has `Flow-Status: ENABLED-UPLINK` (0) and the callee's `ENABLED-DOWNLINK` (1).

### The end of a call

When a BYE passes, the P-CSCF of each side ends its Rx session with an STR, `Termination-Cause: DIAMETER_LOGOUT` (TS 29.214 4.4.4). The PCRF answers with an STA and the stub releases the bearer with DEACTIVATE EPS BEARER CONTEXT REQUEST.

### The network takes the bearer

The stub can deactivate a bearer as a lost radio link would: the EPS bearer context deactivation that the network initiates (TS 24.301 6.4.4.2). A DELETE on `/network-initiated/ues/<IMSI>/bearers/<EBI>` starts it, and a POST on `/network-initiated/ues/<IMSI>/detach` starts the network initiated detach (5.5.2.3). The dashboard passes both on, so on the host:

```sh
curl http://localhost:8080/api/epc/ues/001010000000010                       # the bearers of volte-a
curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6    # deactivate the voice bearer
```

```text
#7775 dashboard -> EPC  DELETE /network-initiated/ues/001010000000010/bearers/6
#7776 EPC -> volte-a    DEACTIVATE EPS BEARER CONTEXT REQUEST (event stream)
#7778 PCRF -> P-CSCF    ASR   Abort-Cause BEARER_RELEASED (0)
#7785 P-CSCF -> PCRF    ASA 2001
#7787 P-CSCF -> PCRF    STR
#7804 P-CSCF -> S-CSCF  BYE   in volte-a's name, Reason: SIP;cause=503;text="Service Unavailable"
#7806 P-CSCF -> volte-a BYE   Reason: SIP;cause=503
#7872 P-CSCF -> volte-b BYE   through the S-CSCF and the MMTel AS, with the same Reason
#7885 EPC -> volte-b    DEACTIVATE EPS BEARER CONTEXT REQUEST
```

The voice bearer carried all the media of the session, so the PCRF ends the whole Rx session with an ASR rather than reporting the lost flows in an RAR (TS 29.214 4.4.6.1, 4.4.6.2). The P-CSCF answers it, ends the session with an STR, and releases the call itself: a BYE to the other side in the served user's name and, here, one to the served UE as well (TS 24.229 5.2.8.1.2, IR.92 2.4.2.3). It builds the BYEs from the dialog information it keeps behind its Store: the Contacts, the tags, the CSeq and the route set. volte-a sends no BYE of its own. When volte-b's call ends, its P-CSCF ends volte-b's Rx session and the stub releases volte-b's bearer too.

A bearer can also go before the call is answered. The P-CSCF then gives the INVITE up: it answers the caller 500 and cancels the INVITE towards the callee, with a Reason that gives 503 (TS 24.229 5.2.8.1.1). When the callee has answered 200 just before the CANCEL reaches it, the 200 crosses the CANCEL (RFC 3261 9.1). The caller has its 500 and never sees that 200, so the P-CSCF sends the ACK in the caller's name, for the 200 and for each retransmission of it. Then it releases the dialog that the 200 confirmed with a BYE to the callee, built as above (5.2.8.1.2).

## Preconditions

A VoLTE caller does not want the callee's phone to ring while the voice cannot flow yet. With the precondition mechanism (RFC 3312), each side says in its SDP whether its resources are reserved, and the callee rings only once every mandatory precondition is met. IR.92 2.4.1 asks for it, with UPDATE to report the status. The INVITE has `Supported: 100rel, timer, 199, precondition`, not Require (TS 24.229 5.1.3.1).

```text
#871  volte-a -> P-CSCF  INVITE       offer, curr:qos local none
#1026 P-CSCF -> volte-b  INVITE       through the S-CSCFs and the MMTel AS of each user
#1030 volte-b -> P-CSCF  183          Require: 100rel, precondition; answer, conf:qos remote sendrecv
#1036 P-CSCF -> PCRF     AAR          volte-b's media; the stub sets up volte-b's bearer (#1040, #1046)
#1083 P-CSCF -> PCRF     AAR          volte-a's media; the stub sets up volte-a's bearer (#1087, #1089)
#1094 P-CSCF -> volte-a  183
#1099 volte-a -> P-CSCF  PRACK        and its 200 (#1120)
#1126 volte-a -> P-CSCF  UPDATE       curr:qos local sendrecv
#1212 P-CSCF -> volte-b  UPDATE
#1216 volte-b -> P-CSCF  200 (UPDATE) curr:qos local sendrecv, curr:qos remote sendrecv
#1219 volte-b -> P-CSCF  180          only now: every mandatory precondition is met
#1370 volte-b -> P-CSCF  200 (INVITE)
#1429 P-CSCF -> volte-a  200 (INVITE) and the ACK (#1432)
```

The precondition lines of each SDP, as its sender writes them (local is the sender's own segment):

| Step | Sender | `curr:qos local` | `curr:qos remote` | `des:qos` | `conf:qos` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte-a | none | none | mandatory local sendrecv, optional remote sendrecv | |
| 183 | volte-b | none | none | mandatory local sendrecv, mandatory remote sendrecv | remote sendrecv |
| UPDATE | volte-a | sendrecv | none | mandatory local sendrecv, mandatory remote sendrecv | |
| 200 to the UPDATE | volte-b | sendrecv | sendrecv | mandatory local sendrecv, mandatory remote sendrecv | |

- The caller uses the segmented status type: it can only say whether its own segment is reserved (TS 24.229 6.1.2).
- In the 183 the callee raises the caller's strength to mandatory and asks, with `conf:qos remote sendrecv`, to be told when the caller's resources are ready (RFC 3312 7).
- A UE counts its local resources as reserved when it has the dedicated bearer whose TFT carries the media of the call (TS 24.229 L.2.2.5.1B, IR.92 2.4.3.1). Its UPDATE goes once the PRACK has its 200.
- The callee does not alert until every mandatory precondition is met (RFC 3312 6). The capture shows the 180 from volte-b after its 200 to the UPDATE. At the caller the 180 may come before the 200 to the UPDATE: the P-CSCF holds that 200 for the AAR of the new answer.

A UE without the EPC stub, such as ue2, takes its resources as reserved from the start and answers `curr:qos local sendrecv`. `-no-preconditions` makes a UE leave the mechanism out.

## Codecs

### The offer and the answer

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
a=rtpmap:98 AMR/8000/1
a=fmtp:98 mode-change-capability=2; max-red=0
a=rtpmap:99 AMR/8000/1
a=fmtp:99 mode-change-capability=2; max-red=0; octet-align=1
a=rtpmap:0 PCMU/8000
a=rtpmap:100 telephone-event/16000
a=fmtp:100 0-15
a=rtpmap:101 telephone-event/8000
a=fmtp:101 0-15
a=ptime:20
a=maxptime:240
```

- AMR-WB comes first, then AMR, each in both payload formats of RFC 4867: bandwidth-efficient (96, 98), which IR.92 3.2.5 has the caller prefer, and octet-aligned (97, 99). No mode-set: every mode is allowed (IR.92 2.4.3.2).
- PCMU stays last, for softphones and the interconnection of JJ-90.30. It makes b=AS 80 kbit/s, the bandwidth of PCMU with its headers, and that is what the P-CSCF asks the PCRF for in the uplink.
- telephone-event comes at the clock rate of each speech codec (TS 26.114 Annex G).
- b=RS:0 and b=RR:4000 are the RTCP bandwidth of senders and of receivers in bit/s (RFC 3556 2). The UE computes its report interval from them (RFC 3550 6.3.1), never below 5 seconds; both at 0 would turn RTCP off (IR.92 3.2.4).

volte-b answers `m=audio 60734 RTP/AVP 96 100` with AMR-WB in the bandwidth-efficient format and telephone-event/16000, and `b=AS:41`. ue2, which knows PCMU only, answers `m=audio ... RTP/AVP 0 101`, and the VoLTE UE then speaks PCMU (`ue -codecs` changes the list).

### The frames

The UE never encodes or decodes AMR. It sends frames encoded beforehand, in the storage format of RFC 4867 5: the embedded tones of `internal/ue/speech`, made with `tools/amrgen` (a tool with cgo, outside the product build), or a file given with `-amr-in`. It packs them into RTP itself:

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

One frame of 20 ms per packet: the codec mode request (CMR) of 4 bits, then the table of contents (ToC) entry with F, FT and Q, then the speech bits (RFC 4867 4.3). The receiver writes the frames it gets back to the storage format with `-received-out`, so the frames can be compared bit for bit with what the sender wrote with `-sent-out`; the e2e script does that.

### RTCP and DTMF

Both UEs send RTCP sender reports to the port above RTP, through the relay (RFC 3550 6.4.1, IR.92 3.2.4). DTMF digits go as telephone-events in the RTP stream (RFC 4733): each digit is a run of packets, the last three with the end bit. The relay passes them on as they are.

## Session timer, Reason and early media

- **Session timer.** The INVITE has `Supported: timer` and `Session-Expires: 1800` (IR.92 2.2.8). The callee's 200 has `Require: timer` and `Session-Expires: 1800;refresher=uac` (RFC 4028 9): the caller refreshes the session with an UPDATE every 900 seconds, and either side ends it if no refresh comes.
- **Reason.** The UE's BYE has `Reason: RELEASE_CAUSE;cause=1;text="User ends call"` (TS 24.229 7.2A.18.11, IR.92 2.2.4); a call ended because its media bearer was lost has cause 3. The P-CSCF's BYEs say `Reason: SIP;cause=503`.
- **Early media.** The INVITE has `P-Early-Media: supported` (RFC 5009, IR.92 2.2.7). No node of this network authorises early media, so after the 180 the caller's status shows that it plays a ringing tone of its own. The UE also supports 199 (RFC 6228), which the MMTel AS sends in CFNR ([mmtel.md](mmtel.md)).

## Hold, resume and DTMF

The call that the dashboard drives in the e2e script:

```text
#4604 volte-a -> P-CSCF  INVITE      to volte-b, set up as above
#5820 volte-a -> relay   telephone-event 1, end   ... then 5, 9 and # (event 11)
#5953 volte-a -> P-CSCF  re-INVITE   a=sendonly: on hold
#6003 volte-b -> P-CSCF  200         a=recvonly
#6151 volte-a -> P-CSCF  re-INVITE   a=sendrecv: resumed
#6201 volte-b -> P-CSCF  200         a=sendrecv
#6347 volte-a -> P-CSCF  BYE
```

Each re-INVITE passes the MMTel AS as it came (TS 24.610 4.5.2.1), and each new answer updates both Rx sessions.

## Run it

`scripts/e2e-volte.sh` does everything in this guide and checks each message in the capture, in order. It leaves the capture in `captures/`, the frames of the registrations, the calls, Rx and the EPC stub's API in `e2e-out/volte-ladder.txt`, the AMR-WB recordings in `captures/volte-*.amr`, and the logs of the UEs in `e2e-out/`. It takes about two minutes.

### With the dashboard

```sh
docker compose up -d --build
docker compose --profile volte up -d --no-deps volte-a volte-b
```

`--no-deps` keeps Compose from recreating the P-CSCF, the EPC and the other nodes that the UEs depend on, which it may do after a fresh build: a recreated node has lost its Diameter connections.

Open http://localhost:8080. The UE cards of volte-a and volte-b say that they are registered. Call `tel:+12025550112` from volte-a; volte-b answers by itself after its ring time. The cards have buttons for hold, resume and DTMF, and show the codec and the precondition table of the call. The ladder of the call has a column for the PCRF, next to the P-CSCF. Release the voice bearer with the `curl` above and watch the P-CSCF end the call.

### From the command line

The VoLTE UEs of the profile and the one-shot commands share the addresses of volte-a and volte-b, so stop the profile first (`docker compose --profile volte rm -sf volte-a volte-b`). Then let volte-b wait for a call. The command stays in the foreground until the call ends:

```sh
docker compose run --rm --no-deps volte-b answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out /captures/volte-b-received.amr
```

In another terminal, call volte-b from volte-a:

```sh
docker compose run --rm --no-deps volte-a call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -sent-out /captures/volte-a-sent.amr tel:+12025550112
```

The flags for a VoLTE UE:

| Flag | Meaning |
| --- | --- |
| `-epc URL` | Attach to the EPC stub first, and take the P-CSCF from the PCO. |
| `-k`, `-opc` or `-op`, `-sqn` | IMS AKA with these keys instead of SIP Digest. |
| `-port-s` | The protected server port, 5062 by default. |
| `-codecs` | The speech codecs to offer, such as `AMR-WB,AMR,PCMU`. With `-epc` the default is all of them, without it PCMU. |
| `-no-preconditions` | Leave the precondition mechanism out. |
| `-amr-in`, `-sent-out`, `-received-out` | Play an AMR or AMR-WB file, and write what was sent and received. |

SIPp registers with IMS AKA and deregisters:

```sh
docker compose --profile ue build peer
docker compose run --rm --entrypoint sipp peer -sf /etc/sipp/register-aka.xml -m 1 -t u1 -p 5060 172.28.0.40:5060
```

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
(sip && !(sip.CSeq.method in {"SUBSCRIBE", "NOTIFY", "MESSAGE"})) || diameter.cmd.code in {265, 274, 275, 303} || (http && ip.addr == 172.28.0.80)
```

It shows the registrations, the calls, Rx, the MARs and the EPC stub's API. Wireshark decodes the AMR-WB frames by the SDP it saw: the payload type, AMR-WB and the bandwidth-efficient format come from the rtpmap and fmtp lines. Without that SDP, set **Preferences > Protocols > AMR** to Wideband AMR and the payload format, and use **Decode As** for the RTP port. **Telephony > VoIP Calls** lists each dialog as a call of its own.

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | The messages of the security agreement. |
| `sip.WWW-Authenticate contains "AKAv1-MD5"` | The challenges of IMS AKA. `contains "ck="` finds those that the S-CSCF sends to the P-CSCF. |
| `udp.port == 5064 \|\| tcp.port == 5064` | What goes over the P-CSCF's protected server port. |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | The MARs and MAAs of IMS AKA. |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx: AA, RA, AS and ST. |
| `diameter.Flow-Usage == 2` | The AARs of the signalling path. |
| `diameter.Codec-Data` | The AARs of a call's media. |
| `diameter.Experimental-Result-Code == 5065` | IP-CAN_SESSION_NOT_AVAILABLE: a UE that did not attach. |
| `diameter.Abort-Cause` | The ASRs of the PCRF. |
| `http && ip.addr == 172.28.0.80` | The EPC stub's API. `frame contains "EPS BEARER CONTEXT"` finds the bearer messages. |
| `sip.Require contains "precondition"` | The 183s of the precondition mechanism. `sdp.media_attr contains "qos"` finds every precondition line. |
| `amr` | The AMR and AMR-WB frames. `amr.wb.cmr` and `amr.wb.toc.ft` are the CMR and the frame type of AMR-WB. |
| `rtcp`, `rtpevent` | RTCP and the telephone-events. |
| `sip.Session-Expires`, `sip.Reason` | The session timer, and the reasons of BYEs and CANCELs. |

These filters were checked with tshark 4.2 against a capture of `scripts/e2e-volte.sh`.

## Where the code is

| Package or file | What it holds |
| --- | --- |
| `internal/epc` | The EPC stub and the PCRF: the UE API and the procedures that the network initiates (`http.go`), the messages (`api.go`), the PDN connections and bearers (`network.go`), Rx (`pcrf.go`). |
| `internal/ue/access.go`, `access_epc.go` | The `AccessNetwork` of the UE and its client of the stub's API. |
| `internal/milenage`, `internal/aka` | MILENAGE (TS 35.206), the authentication vector, the USIM's checks, re-synchronisation, and the Digest of RFC 3310. |
| `internal/hss`, `internal/cx` | The AKA credentials of a subscription, MAR and MAA. |
| `internal/scscf/aka.go` | The S-CSCF's challenge and check of IMS AKA. |
| `internal/pcscf/secagree.go` | The security agreement and the SA table of the P-CSCF. |
| `internal/ue/aka.go` | The UE's USIM and its side of the agreement. |
| `internal/policy`, `internal/rx` | The policy control boundary of the P-CSCF, the SDP to Rx mapping of TS 29.213, and the Rx messages and client. |
| `internal/pcscf/policy.go`, `setup.go`, `session.go`, `release.go`, `dialog.go` | The P-CSCF as AF: the signalling path, the AARs of a call, the STR, and the release by the network from the dialogs it keeps. |
| `internal/precondition` | The precondition table of RFC 3312 and its SDP attributes. |
| `internal/sdp` | The VoLTE offer and answer: several codecs, fmtp, b= lines per medium. |
| `internal/amr`, `internal/rtcp`, `internal/rtp` | The RFC 4867 payload and storage formats, RTCP, and the telephone-events. |
| `internal/ue/session.go`, `call.go`, `media.go` | The UE's VoLTE call: the offer and answer, the preconditions, hold, DTMF, AMR and RTCP. |
| `internal/sip` | UPDATE, re-INVITE, the session timer, 199, Reason and the B2BUA parts that the MMTel AS uses. |
| `internal/dashboard/epc.go`, `causes.go` | The dashboard's pass-through of the stub's paths, and the link of Rx sessions to their calls. |
| `internal/ue/volte_epc_test.go`, `volte_aka_test.go` | The VoLTE call through the whole IMS in one process. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| EPC stub, TS 24.301, TS 36.413 | No NAS encoding, no S1-AP and no radio: the messages are HTTP and JSON, and the IMSI in the URL stands for the S1 connection. No security, GUTI, tracking area update or network-side timers. | A learner reads the procedures, not the encoding. |
| EPC stub, TS 36.331 | System information is an HTTP resource. | There is no radio to broadcast it. |
| EPC stub, TS 23.401 5.3.1.1 | A static address per subscriber, no pool, no GTP-U or SGi. | The container already has the address, so the packets need no tunnel. |
| EPC stub, TS 23.401 5.3.2.1 | No authentication, NAS security or S6a in the attach, and no ATTACH REJECT. | The stub serves the IMSIs of its configuration. |
| EPC stub, TS 29.212 | No Gx: the PCRF and the PDN GW share the PDN connection in the Store. | One PCRF, one PDN GW; Gx adds messages a learner of the IMS does not need. |
| EPC stub, TS 24.301 6.4.3, 6.4.4.2 | No MODIFY EPS BEARER CONTEXT REQUEST when the rules change, and a bearer is forgotten when the deactivation is sent, without T3495. | The stub enforces no QoS, and a UE only needs to know that the bearer is there. |
| PCRF, TS 29.213 6.3 | Only audio, with QCI 1; the guaranteed rate is the maximum one, and RTCP has no share of its own. | Every call here has one audio stream. |
| PCRF, TS 29.214 4.4.2, 5.3.16 | Every AAR is taken to carry the whole service information. | The P-CSCF derives it from the whole SDP each time. |
| P-CSCF, TS 29.213 7.3.7 | No DRA: the one PCRF of the configuration. | One PCRF. |
| P-CSCF, TS 29.213 Tables 6.2.1, 6.2.2 | No media over TCP, no b=TIAS, and only the flow of the new direction when a stream turns one-way. | No call here uses them. |
| P-CSCF, TS 29.214 A.1 | Only the callee's P-CSCF sends AF-Application-Identifier. | The caller's would take it from Feature-Caps, which no node writes. |
| P-CSCF, TS 29.214 4.4.2 | A refused change of the service information is only logged. | The far end has answered by then; TS 24.229 handles refused resources only for the initial INVITE. |
| P-CSCF, TS 29.214 A.3 | The early dialogs of a forked INVITE are not told apart. | The S-CSCF does not fork. |
| P-CSCF, TS 24.229 5.2.8.1.1, 5.2.8.1.2 | The release comes at once, not after a wait for a SIP message that removes the media; the other sessions of a UE whose signalling is lost are left to their own events; the dialog is forgotten when the BYEs go. | One audio stream per call; the P-CSCF keeps no index of dialogs by UE. |
| P-CSCF, TS 24.229 5.2.8.1.2 | When a confirmed call loses its resources, the served UE is not left to notice the lost bearer itself, as the clause has it: the P-CSCF sends it a BYE too, in the name of the other party. | After the network released only the voice bearer the UE still has its signalling, and a UE that does not act on the loss of that bearer would, without a BYE, stay in a call that the network has ended. |
| P-CSCF, TS 24.229 5.2.8.1.4, 5.2.8.3 | A dialog that ends without a BYE through the P-CSCF stays in the Store. | It would need an index of dialogs by user, or a session timer of the P-CSCF. |
| P-CSCF, RFC 3264, RFC 3262 | An INVITE or re-INVITE without SDP gets no relay context and no authorisation. | The UE simulator always offers in the INVITE. |
| P-CSCF and UE, TS 33.203 7.1, 7.2 | No IPsec ESP: the associations are agreed and kept, the protected server ports are used, the client ports are never bound. | Captures stay readable. The standard has no IMS AKA without protection. |
| P-CSCF, TS 24.229 5.2.2.3 | A UE is known by the address and port its requests come from, or for TCP by the sent-by of its Via, which nothing authenticates. | Without ESP a TCP connection comes from a port of the moment. |
| S-CSCF, TS 33.203 6.1.1, TS 24.229 5.4.1.2.1A | AKAv1-MD5 only, no AKAv2-SHA-256. | SIPp speaks AKAv1-MD5 only. |
| AKA, TS 33.102 C.2.2, C.3.4 | No limit L on how old an accepted SQN may be, and IND is not kept per domain. | Profile 2 suggests no L; IMS is one domain with one vector per MAR. |
| UE, TS 24.301, TS 36.331 | The UE's packets go straight onto the Docker network. | No radio and no tunnel. |
| UE, TS 24.229 7.2A.4.3 | The access type is always 3GPP-E-UTRAN-FDD. | The stub says nothing of the duplex mode of its cell. |
| UE, TS 24.229 L.2.2.5.1B | The voice bearer counts as the resources of every call, without matching its TFT. | IR.92 4.4.2 has one voice bearer for all calls. |
| UE, IR.92 2.4.2.1 | A UE that lost its PDN connection stops instead of attaching and registering again. | The failure shows at once. |
| UE, TS 24.229 6.1.1 | A bearer lost before the answer only changes the precondition table; the network ends the call. | The P-CSCF releases it (5.2.8.1.1). |
| UE, TS 24.229 5.1.3.1, 5.1.4A.1 | precondition is in the Supported of every request within a dialog. | One Supported for all calls; a peer that does not use preconditions ignores it. |
| UE, IR.92 3.2.1, RFC 4867 8.3.1 | The frames go in the modes they were encoded in; CMR and mode-set are not followed. | The UE cannot encode. |
| UE, IR.92 2.6 | The User-Agent follows the examples of IR.92, not GSMA RCC.07. | RCC.07 was not read. |
| UE, TS 24.229 9.2.1, 5.1.2A.1.1 | A UE without `-epc` takes the P-CSCF address from its configuration; the P-CSCF URI always comes from the configuration. | The softphone path stays; the P-CSCF knows its Route by its host name. |
| UE, RFC 3264 6.1, RFC 3550 8.2 | The UE sends with the payload type numbers of the peer's SDP, and a new SSRC restarts the statistics. | Every UA here answers with the numbers of the offer, and the peer is the only source. |
| AMR, RFC 4867 4.4 | No frame CRCs, robust sorting, interleaving or several channels. | IR.92 offers none of them. |
| AMR, IR.92 3.2.5 | Frames that arrive before the first one received are dropped. | Only a reordered start of the stream brings them. |
| RTCP, RFC 3550 6.2, 6.3.4 to 6.3.7, A.1 | No reduced minimum interval, no reconsideration, no probation of a new source. | A call has exactly two members, who never change. |
| DTMF, RFC 4733 2.5.1.3 | A digit longer than 65535 timestamp units is refused. | A UE sends short digits. |
| Relay, RFC 3605, RFC 5761, RFC 4961 | RTCP is on the port above RTP; a latched address is kept for the whole call, unless the controller asks the termination to re-latch. | No UE here sends a=rtcp or rtcp-mux, or moves in a call. |
| Preconditions, RFC 4032 4.1, 4.2 | A stream that moves to a new address is not detected. | The UE keeps its address for the whole call. |
| SDP, RFC 8866 5.7 | IPv4 unicast addresses only in c=. | Every node here uses IPv4. |
| SDP, TS 26.114 5.2.1.1 | No EVS, and so no super-wideband speech, in the offer for NR access (`sdp.VoNRCodecs`): it is the offer for LTE. | The UE has no EVS codec. An EVS payload type would offer bit rates of EVS Primary that the UE cannot send. |
| UA, RFC 3261 12.2.1.2 | A lost early dialog, or a failed PRACK, is only logged. | The final response to the INVITE ends the call anyway. |
| UA, RFC 3261 13.2.2.4 | A BYE that ends a dialog whose 2xx had an offer sends the ACK without an answer first. | The UA has no SDP of its own then. |
| UA, RFC 4028 7.1, 10 | timer is in the Supported of the INVITE and its dialog only; after a refresh fails, the UA only logs and the expiry ends the session. | The session timer concerns the call's dialog; no node challenges a request within a dialog. |

## Clean up

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
