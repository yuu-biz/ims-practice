---
title: The VoLTE call flow
footer: ims-practice study session 6. VoLTE
lang: en
---

# The VoLTE call flow

ims-practice study session 6 (the access network, IMS AKA, policy control over Rx, and the VoLTE call)

---

[layout: summary]

## What you will know after this session

To begin: when you call from a mobile phone, what does the network prepare before the other phone rings?

- Where a VoLTE phone differs from the softphone that registers with SIP Digest.
- How the UE attaches to the EPC and is given the P-CSCF address.
- How the UE and the network prove themselves to each other with IMS AKA.
- Which identities a phone without an ISIM makes from the IMSI, and which of them the network bars.
- How the P-CSCF asks the PCRF for voice resources over Rx, and the network sets up a bearer.
- How preconditions keep the callee from ringing until the resources are there.
- How the P-CSCF ends the call when the network loses the bearer.

> Guide: [The VoLTE call flow](../../../docs/guide/en/volte.md) (`docs/guide/en/volte.md`)
> The editions are named at the top of the guide, among them TS 24.229 V20.1.0, TS 29.214 V20.0.0, TS 24.301 V20.1.0, TS 33.203, TS 33.102, TS 35.206, TS 35.208, RFC 3310, RFC 3329, RFC 3312, RFC 4867 and GSMA IR.92 v24.0.

---

## What changes from the softphone's call

| | The softphone (SIP Digest, PCMU) | The VoLTE phone |
| --- | --- | --- |
| Access network | None; the P-CSCF is configured | Attaches to the EPC, which names the P-CSCF |
| Authentication | SIP Digest and a password | IMS AKA and sec-agree; no password |
| Resources | Not considered | The P-CSCF asks the PCRF over Rx |
| Ringing | Right after the 183 | Once both sides have their bearers |
| Speech | PCMU over RTP | AMR-WB, RTCP, telephone-events |
| Release | Only the UE's BYE | The P-CSCF, when the network loses the bearer |
| Identities | Configured, and used as registered | Made from the IMSI; the one registered is barred |

- What goes wrong if everything stays as in the left column? The callee rings before the voice has a path, and answers into silence.
- The softphones keep working: a UE that did not come through the EPC stub is unknown to the PCRF, and the P-CSCF serves it without policy control.

> Guide: ["How a VoLTE phone differs from a softphone"](../../../docs/guide/en/volte.md#how-a-volte-phone-differs-from-a-softphone). IR.92 4.4, 2.4.1, 2.4.2.3, 3.2.1. TS 33.203 6.1.1, 7. TS 29.214 4.4.1. TS 24.229 5.2.8.1.2. RFC 3312.

---

## The nodes

| Node | Address | What it does in a VoLTE call |
| --- | --- | --- |
| volte-a | 172.28.0.120 | The caller, `tel:+12025550110`. Subscriber 10. |
| volte-b | 172.28.0.121 | The callee, `tel:+12025550112`. Subscriber 12. |
| ue2, SIPp | 172.28.0.101, 102 | A PCMU-only softphone, and the IMS AKA peer. |
| EPC stub and PCRF | 172.28.0.80 | The `epc` role: the UE API on HTTP 8080, Rx on 3868. |
| P-CSCF | 172.28.0.40 | Port 5060, and the protected server port 5064. The Rx client. |
| S-CSCF, HSS | 172.28.0.30, 10 | The IMS AKA challenge, and the vectors. |
| MMTel AS, relay | 172.28.0.75, 50 | As in the session on the MMTel AS. The relay passes RTCP too. |

- volte-a and volte-b are services of the profile `volte` in `compose.yaml`. Their keys are in `deploy/compose/hss.yaml`; `deploy/compose/epc.yaml` gives each subscriber the address of its container.
- The dashboard (172.28.0.60) drives volte-a and volte-b, shows Rx in the ladder, and passes on to the host the EPC stub's paths that start a procedure of the network.

> Guide: ["The nodes"](../../../docs/guide/en/volte.md#the-nodes)

---

## A stub: what the EPC stub keeps, and what it leaves out

```text
 UE ==(HTTP + JSON: ATTACH REQUEST, ...)==> EPC stub: PDN GW and PCRF (epc role)
 |                                           ^
 |                                           |  Rx (Diameter)
 +------------ SIP ------------> P-CSCF -----+
```

- The messages and their fields are the ESM and EMM messages of TS 24.301 (8.2, 8.3), in lower case in the URL, such as `POST /ues/001010000000010/attach-request`.
- What the network sends comes on the UE's event stream, `GET /ues/<IMSI>/events`, as server-sent events.
- Each subscriber has a static address (TS 23.401 5.3.1.1). Nothing is tunnelled; the packets go straight on.
- No radio, no S1, no NAS encoding or security, no Gx: the PCRF and the PDN GW share the state inside the `epc` role. A bearer is a record; no QoS is enforced and no packet filtered.
- Only Rx is real Diameter, because the IMS side is what a learner reads in Wireshark. The names of the NAS messages let the attach be read without a radio or a NAS decoder.

> Guide: ["What the stub leaves out"](../../../docs/guide/en/volte.md#what-the-stub-leaves-out)

---

## Get a capture

- The e2e script of the VoLTE call in `scripts/` runs every step of this session: it registers SIPp, makes the VoLTE call and the call to ue2, drives DTMF and hold through the dashboard, has the network release the bearer, and checks each message in the capture. The guide's ["Run it"](../../../docs/guide/en/volte.md#run-it) names it.
- The frame numbers of the guide are from one run of this script. Yours differ.
- It leaves the capture in `captures/`, the list of frames in `e2e-out/` and the AMR-WB recordings in `captures/volte-*.amr`. It takes about two minutes.

The display filter of this session:

```text
(sip && !(sip.CSeq.method in {"SUBSCRIBE", "NOTIFY", "MESSAGE"})) || diameter.cmd.code in {265, 274, 275, 303} || (http && ip.addr == 172.28.0.80)
```

- It keeps the registrations, the calls, Rx (265 is AA, 274 AS, 275 ST), the MARs (303) and the API of the EPC stub.

> Guide: ["Run it"](../../../docs/guide/en/volte.md#run-it) and ["Open the capture"](../../../docs/guide/en/volte.md#open-the-capture)

---

## Attach, and finding the P-CSCF

```ladder
volte-a, EPC stub
volte-a -> EPC stub: GET /system-information // the cell: MCC 001, MNC 01, TAC 1
volte-a -> EPC stub: GET /ues/001010000000010/events // the event stream stays open
volte-a -> EPC stub: POST /ues/001010000000010/attach-request // ATTACH REQUEST, APN ims
volte-a <-- EPC stub: 200, ATTACH ACCEPT with ACTIVATE DEFAULT EPS BEARER CONTEXT REQUEST
volte-a -> EPC stub: POST /ues/001010000000010/attach-complete
```

| In the ATTACH ACCEPT | What to look at |
| --- | --- |
| `eps_bearer_identity` 5, `qci` 5 | The default bearer, for IMS signalling. |
| `pdn_address` | The subscriber's static address, 172.28.0.120. |
| The PCO container `000CH` | The P-CSCF address, 172.28.0.40. Port 5060. |

> Guide: ["Attach"](../../../docs/guide/en/volte.md#attach). IR.92 4.4.1, 4.5. TS 23.203 6.1.7. TS 24.008 10.5.6.3. TS 24.229 L.2.2.1, 7.2A.4.3, L.3.1.1.

---

## Attach: what else to see

- The filter is `http && ip.addr == 172.28.0.80`. Open the JSON in the HTTP body.
- The P-CSCF in the PCO is method II of TS 24.229 L.2.2.1, which IR.92 4.5 asks for. The first address of the list is the preferred one.
- The port is the standard 5060, so that Wireshark decodes SIP without a Decode As.
- The UE checks that the PDN address is the address it reaches the P-CSCF from, and stops if not.
- The UE writes the cell into P-Access-Network-Info: in `3GPP-E-UTRAN-FDD;utran-cell-id-3gpp=0010100010000101` come MCC 001, MNC 01, the TAC in 4 hexadecimal digits and the ECI in 7.

---

## The boundary between the UE and the access network, in the code

[internal/ue/access.go](../../../internal/ue/access.go)

```go
type AccessNetwork interface {
	// Attach connects the UE to the IMS APN and returns what the network
	// gave it (TS 24.229 L.2.2.1).
	Attach(ctx context.Context) (Attachment, error)
	// Events delivers what the network tells the UE after the attach. It
	// is closed once the UE has detached or the network has detached it.
	Events() <-chan AccessEvent
	// Detach disconnects the UE from the network.
	Detach(ctx context.Context) error
}
```

- The one implementation is a client of the EPC stub's HTTP API (`access_epc.go`).
- The events are three: a bearer set up, a bearer released, a detach by the network.
- The UE sees the access network only through these three methods, so a NAS implementation could stand behind the same interface without a change to the rest of the UE.

---

## The keys of IMS AKA

| Who | Holds | Where |
| --- | --- | --- |
| The USIM of volte-a | K, OPc and SQN_MS | `-k`, `-opc`, `-sqn` of `ue` (`compose.yaml`) |
| The HSS | K, OPc, AMF and SQN_HE of the same subscriber | `aka:` of subscriber 10 in `deploy/compose/hss.yaml` |

- K and OPc are those of MILENAGE test set 1 (TS 35.208 4.3.1).
- OPc is derived from the operator's OP and K (TS 35.206 4.1). `-op` takes the OP instead, as SIPp does.
- Nothing else is shared. The network and the UE each compute the rest from these and the random challenge. There is no password anywhere, unlike the subscribers that register with SIP Digest.
- volte-b has the K and OPc of test set 3 (4.3.3). SIPp 3.7 takes K and OP as the bytes of their text, so subscriber 11, for SIPp, has printable keys; the guide's section "SIPp" has them.

> Guide: ["The keys"](../../../docs/guide/en/volte.md#the-keys)

---

## The identities of a phone without an ISIM

| Identity | Value | Where it appears |
| --- | --- | --- |
| Private user identity | `001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | `username` of Authorization, User-Name on Cx. |
| Temporary public user identity | `sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org` | From and To of REGISTER, Public-Identity on Cx. Nowhere else. |
| Default public user identity | `sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org` | The first P-Associated-URI of the 200 OK. Then From and P-Asserted-Identity. |
| The tel URI of the number | `tel:+12025550110` | The second P-Associated-URI. |

- The card of volte-a and volte-b has a USIM and no ISIM. A USIM holds the IMSI and the keys but no IMS identity, so the phone makes its identities from the IMSI, here 001010000000010.
- The temporary identity carries the IMSI, so the HSS bars it: it serves registration only, and only the CSCFs and the HSS see it. It is under `barred_identities` in `deploy/compose/hss.yaml`, and the user profile in the SAA marks it with `<BarringIndication>`.

> Guide: ["The identities of a phone without an ISIM"](../../../docs/guide/en/volte.md#the-identities-of-a-phone-without-an-isim). TS 24.229 5.1.1.1A. TS 23.003 13.2, 13.3, 13.4B. TS 23.228 E.3.1. TS 29.228 B.2.1.

---

## The identities in the capture

```text
REGISTER  volte-a -> P-CSCF  To: <sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org>
200 OK    P-CSCF -> volte-a  P-Associated-URI: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
                             P-Associated-URI: <tel:+12025550110>
INVITE    volte-a -> P-CSCF  From: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>;tag=...
INVITE    P-CSCF -> S-CSCF   P-Asserted-Identity: <sip:+12025550110@ims.mnc001.mcc001.3gppnetwork.org>
```

- The S-CSCF lists in P-Associated-URI only the identities that are not barred, the default first. The UE keeps the first as its default public identity; the one it registered is missing, so it takes that one as barred.
- The UE sends no P-Preferred-Identity, so the P-CSCF asserts the default identity. A request from a barred identity gets 403, and one to it 404.
- `sip && !(sip.CSeq.method == "REGISTER") && frame contains "sip:001010000000010@ims"` finds no packet. No Contact carries the IMSI either: its user part is a UUID. A softphone has no SIM: its identity is configured, as an IMC holds it, and it uses the identity it registered.

> Guide: ["The identities of a phone without an ISIM"](../../../docs/guide/en/volte.md#the-identities-of-a-phone-without-an-isim). TS 24.229 5.4.1.2.2F, 5.1.1.2.1, 5.2.6.3.1, 5.4.3.2, 5.4.3.3, 5.1.1.1B.1.

---

## IMS AKA, part 1: the first REGISTER and the 401

| # | Hop | What to look at |
| --- | --- | --- |
| 698 | UE:5060 → P-CSCF:5060 | `Security-Client`, `Require: sec-agree`. Empty nonce and response. |
| 709 | P-CSCF → I-CSCF | Security-Client taken out and kept. `integrity-protected="no"`. |
| 724 | S-CSCF → HSS (MAR) | Asks for one item of the scheme `Digest-AKAv1-MD5`. |
| 726 | HSS → S-CSCF (MAA) | RAND and AUTN, XRES, CK, IK. |
| 727 | S-CSCF → I-CSCF (401) | The nonce is RAND and AUTN in base64. `ik` and `ck`. |
| 737 | P-CSCF → UE (401) | `ik` and `ck` taken out, `Security-Server` added. |

> Guide: ["The ladder"](../../../docs/guide/en/volte.md#the-ladder), steps 1 to 6. TS 24.229 5.1.1.2.2, 5.2.2.1, 5.2.2.2, 5.4.1.1, 5.4.1.2.1A. RFC 3329 2.3.1. TS 33.203 Annex H, 7.2. TS 29.229 6.3.9. TS 33.102 6.3.2. TS 29.228 6.3. RFC 3310 3.2.

---

## IMS AKA, part 2: check the network, and answer

| # | Hop | What to look at |
| --- | --- | --- |
| 740 | UE | Checks the MAC of AUTN and the SQN; computes RES, CK, IK. |
| 740 | UE:5062 → P-CSCF:5064 | `Security-Verify` mirrors the Security-Server. The response from RES. |
| 749 | P-CSCF → I-CSCF | Agreement checked: `integrity-protected="yes"`. |
| 760, 764 | S-CSCF, SAR | Computes the Digest with XRES and compares. They match: registered. |
| 783 | P-CSCF:5064 → UE:5062 | The 200 OK. The associations are established. |
| 786 | P-CSCF → PCRF | AAR: the signalling path (in the Rx part). |

> Guide: ["The ladder"](../../../docs/guide/en/volte.md#the-ladder), steps 7 to 12. TS 33.102 6.3.3, C.2. TS 33.203 7.1. RFC 3329. RFC 3310 3.3. TS 24.229 5.4.1.2.2, 5.2.2.2. TS 29.214 A.4.

---

## IMS AKA: what the two parts show

- The first REGISTER is the only one the UE sends to the unprotected port 5060. After the 200 OK the UE sends every request to the protected port 5064 of the P-CSCF, and the P-CSCF sends to the UE at 5062.
- The P-CSCF takes `ik` and `ck` out of the 401, so the keys never travel to the UE. The UE computes the same keys from its K and the RAND.
- A matching MAC in AUTN shows that the network knows K: the network is authenticated too, a direction that SIP Digest did not have. The UE also checks that the SQN is fresh.
- Security-Verify lets the P-CSCF check that no man in the middle removed a stronger offer. The 200 OK and the AAR #786 leave the P-CSCF in either order.
- A refresh and the deregistration go over the associations without a new challenge (TS 24.229 5.1.1.4.2, 5.1.1.6.2).
- Filters: `sip.Security-Client || sip.Security-Server || sip.Security-Verify`; `sip.WWW-Authenticate contains "AKAv1-MD5"` (with `contains "ck="`, only the 401 from the S-CSCF stays); `udp.port == 5064 || tcp.port == 5064` for the protected port.

---

## The authentication vector, in the code

[internal/aka/aka.go](../../../internal/aka/aka.go)

```go
func NewAuthenticationVector(secret Secret, rand RAND, sqn SQN, amf AMF) AuthenticationVector {
	m := secret.milenage()
	mac := m.F1(rand, sqn.octets(), amf)
	res := m.F2(rand)
	return AuthenticationVector{
		RAND: rand,
		XRES: res[:],
		CK:   m.F3(rand),
		IK:   m.F4(rand),
		AUTN: newAUTN(conceal(sqn, m.F5(rand)), amf, mac),
	}
}
```

```sh
go test ./internal/aka -run TestAuthenticationVectorFromTestSet1 -v
```

- f1 to f5 are the MILENAGE functions (TS 35.206). AUTN is SQN ⊕ AK (6 octets), AMF (2 octets) and MAC-A (8 octets).
- The RAND of a capture is random, so its outputs are not those of TS 35.208. The test sets check the computation itself: `go test ./internal/milenage -run TestConformanceTestSets -v` checks all 20 sets of 4.3.

> Guide: ["Check the vector by hand"](../../../docs/guide/en/volte.md#check-the-vector-by-hand). TS 33.102 6.3.2. TS 35.208 4.3.1.

---

## The security agreement without ESP

- The standard protects everything after the 401 with IPsec ESP (TS 33.203 7.1).
- This project agrees on the associations, keeps them, and uses the protected ports; it only sets up no ESP (`ealg=null`), so that the captures stay readable.
- The standard has no IMS AKA without protection; without IPsec it has only AKAv2-SHA-256 over TLS (TS 24.229 4.2B.1). This is a simplification of this project's own.

| Unlike a real network | Why |
| --- | --- |
| The protected client ports (`port-c`) are never used | Both sides send from their protected server port |
| For a TCP request, the UE is known by the sent-by of its Via | The INVITE, and a REGISTER over 1300 bytes, go over TCP (RFC 3261 18.1.1) |

- Without ESP the SIP of a VoLTE UE reads in Wireshark as plainly as a softphone's, with no keys to load, and the containers need no NET_ADMIN. The associations go into the SA table of the P-CSCF with IK and CK.
- AKAv1-MD5 only, because SIPp, the independent peer that checks the S-CSCF, speaks only that; it computes the RES and the Digest with code written by others.

> Guide: ["The security agreement without ESP"](../../../docs/guide/en/volte.md#the-security-agreement-without-esp)

---

# Policy control over Rx, and the VoLTE call

The P-CSCF is the application function (AF) of policy and charging control; the PCRF decides what the access network carries (TS 29.214 4).

---

## Rx, part 1: the signalling path (#786)

| AVP | Value |
| --- | --- |
| Media-Component-Number, Flow-Number | 0, 0 |
| `Flow-Usage` | AF_SIGNALLING |
| `Specific-Action` | 2 INDICATION_OF_LOSS_OF_BEARER, 4 INDICATION_OF_RELEASE_OF_BEARER |
| `Framed-IP-Address` | 172.28.0.120, to find the IP-CAN session by |

- This is the AAR right after the registration. For a UE that did not attach (ue2, pjsua), the PCRF answers 5065, IP-CAN_SESSION_NOT_AVAILABLE, and the P-CSCF serves that UE without policy control: every registration of a softphone in the earlier captures shows this pair.
- Each Rx session is a Diameter session of its own: one per registered UE for the signalling path, one per call and side for the media.

The filters are `diameter.Flow-Usage == 2` and `diameter.Experimental-Result-Code == 5065`.

> Guide: ["The signalling path"](../../../docs/guide/en/volte.md#the-signalling-path). TS 29.214 4.4.5, A.4.

---

## Rx, part 2: the AAR for the media of a call

The P-CSCF of each side sends an AAR once it has the SDP answer: the callee's side on the 183 from volte-b, the caller's side on the 183 on its way to volte-a.

| AVP | Value in the callee's AAR (#1036) |
| --- | --- |
| `AF-Application-Identifier` | `urn:urn-7:3gpp-service.ims.icsi.mmtel` |
| `Media-Sub-Component` | Flows 1 and 2: RTP and RTCP, with `Flow-Description` |
| `Max-Requested-Bandwidth-UL`, `-DL` | 80000 and 41000, from b=AS, in bit/s |
| `Flow-Status` | ENABLED (2), from sendrecv |
| `Codec-Data` | The m= lines and attributes of the offer and the answer |
| `Specific-Action` | 2, 4, 9: loss and release of the bearer, failed allocation |
| `Rx-Request-Type` | INITIAL_REQUEST; each later offer and answer, UPDATE_REQUEST |

> Guide: ["The media of a call"](../../../docs/guide/en/volte.md#the-media-of-a-call). TS 29.214 A.1. TS 29.213 6.2 Table 6.2.1, 6.3.

---

## Rx, part 2: reading the AAR

- The filter is `diameter.Codec-Data`. Open the AAR and walk through the Media-Component-Description: number 1 is the first m= line.
- One Flow-Description is `permit in 17 from 172.28.0.121 to 172.28.0.50 40004`. The other end of the media is the relay, not the other UE.
- Only the callee's P-CSCF sends AF-Application-Identifier. The caller's would take it from Feature-Caps, which no node writes; it is in the list of simplifications.
- Service-Info-Status is FINAL_SERVICE_INFORMATION, because the information is based on the answer, not on the offer only.
- On hold, the caller's side has Flow-Status ENABLED-UPLINK (0), the callee's ENABLED-DOWNLINK (1).

---

## The dedicated bearer: a path for the voice

Once the PCRF authorises the audio, the stub sends it on the event stream:

```text
event: ACTIVATE DEDICATED EPS BEARER CONTEXT REQUEST
data: {"eps_bearer_identity":6,"linked_eps_bearer_identity":5,
       "eps_qos":{"qci":1,"maximum_bit_rate_for_uplink":80, ... },
       "tft":{"tft_operation_code":"create new TFT","packet_filters":[ ... ]}}
```

- QCI 1 is the guaranteed bit rate class of conversational voice. The rates, in kbit/s, come from what the P-CSCF asked for.
- The TFT has four filters: RTP and RTCP, each way, between the UE and the relay.
- The UE answers `activate-dedicated-eps-bearer-context-accept` and takes it as the resources of its call. When the call ends, a DEACTIVATE EPS BEARER CONTEXT REQUEST comes.
- The default bearer is 5, the voice bearer 6; both belong to one PDN connection, so `linked_eps_bearer_identity` is 5. Without a relay, the far address of the filters would be the other UE's.
- The filter is `frame contains "EPS BEARER CONTEXT"`. The events arrive bit by bit within one HTTP response.

> Guide: ["Bearers"](../../../docs/guide/en/volte.md#bearers). TS 24.229 L.2.2.5.1. TS 29.213 6.3. IR.92 4.4.2, 2.4.3.1.

---

## Preconditions: no ringing until the resources are there

```ladder
volte-a, P-CSCF, PCRF, volte-b
volte-a -> P-CSCF: INVITE // curr:qos local none
P-CSCF -> volte-b: INVITE, through the S-CSCFs and the ASs
P-CSCF <-- volte-b: 183 // conf:qos remote sendrecv
P-CSCF <-> PCRF: AAR, AAA for each side // a bearer each
volte-a <-- P-CSCF: 183, then the PRACK and its 200
volte-a -> P-CSCF -> volte-b: UPDATE // curr:qos local sendrecv
volte-a <-- P-CSCF <-- volte-b: 200 (UPDATE) // both segments sendrecv
volte-a <-- P-CSCF <-- volte-b: 180 // only now
volte-a <-- P-CSCF <-- volte-b: 200 (INVITE), then the ACK
```

- The INVITE has `Supported: 100rel, timer, 199, precondition`, not Require. The 183 has `Require: 100rel, precondition`. The UPDATE goes once the PRACK has its 200.
- At the caller the 180 may come before the 200 to the UPDATE: the P-CSCF holds that 200 for the AAR of the new answer. Filters: `sip.Require contains "precondition"`, and `sdp.media_attr contains "qos"` for every precondition line.

> Guide: ["Preconditions"](../../../docs/guide/en/volte.md#preconditions). RFC 3312. IR.92 2.4.1. TS 24.229 5.1.3.1. The full ladder and the frame numbers are in the guide.

---

## Read the precondition lines

| Step | Sender | `curr` local | `curr` remote | `des` strength (local, remote) | `conf` |
| --- | --- | --- | --- | --- | --- |
| INVITE | volte-a | none | none | mandatory, optional | |
| 183 | volte-b | none | none | mandatory, mandatory | remote |
| UPDATE | volte-a | sendrecv | none | mandatory, mandatory | |
| 200 (UPDATE) | volte-b | sendrecv | sendrecv | mandatory, mandatory | |

- Each value is as the sender of the SDP sees it. The caller uses the segmented status type (TS 24.229 6.1.2).
- In the 183 the callee raises the caller's segment to mandatory and asks with `conf:qos remote sendrecv` to be told (RFC 3312 7). Its local and remote are as volte-b sees them: its remote is volte-a's segment.
- A UE takes its resources as reserved once it has the dedicated bearer (TS 24.229 L.2.2.5.1B, IR.92 2.4.3.1); the UPDATE says so. A UE without the EPC stub, such as ue2, has them from the start. The direction is sendrecv in every row.

> Guide: the table in ["Preconditions"](../../../docs/guide/en/volte.md#preconditions). RFC 3312 6.

---

## When the callee may ring, in the code

[internal/precondition/table.go](../../../internal/precondition/table.go)

```go
func (t *Table) Met() bool {
	for _, r := range t.rows {
		if r.Strength == Mandatory && !r.Current {
			return false
		}
	}
	return true
}
```

- A row of the table is a segment (local or remote) and a direction (send or recv).
- The callee's UE does not alert until this returns true. The comment above the function points to RFC 3312 6.
- `NeedsOffer`, in the same file, decides whether a change of the UE's own resources calls for a new offer: that is how the caller's UPDATE comes about. `internal/ue/session.go` uses the table.

> Guide: ["Where the code is"](../../../docs/guide/en/volte.md#where-the-code-is). RFC 3312 5, 6.

---

## Codecs: the offer and the answer

```text
m=audio 53946 RTP/AVP 96 97 98 99 0 100 101
b=AS:80
b=RS:0
b=RR:4000
a=rtpmap:96 AMR-WB/16000/1
a=fmtp:96 mode-change-capability=2; max-red=0
a=rtpmap:97 AMR-WB/16000/1
a=fmtp:97 mode-change-capability=2; max-red=0; octet-align=1
```

- 96 and 98 are bandwidth-efficient, 97 and 99 octet-aligned; the caller prefers the former (IR.92 3.2.5). No mode-set (IR.92 2.4.3.2).
- PCMU stays last, so that a VoLTE UE can talk to a softphone that knows PCMU only. b=AS:80 is the value for PCMU, and the uplink bandwidth takes it too.
- volte-b answers `m=audio 60734 RTP/AVP 96 100` with `b=AS:41`. ue2, which knows PCMU only, answers `RTP/AVP 0 101`, and the VoLTE UE then speaks PCMU.
- b=RS:0 and b=RR:4000 are the RTCP bandwidth of senders and of receivers in bit/s (RFC 3556 2); the UE computes its report interval from them (RFC 3550 6.3.1), never below 5 seconds.

> Guide: ["The offer and the answer"](../../../docs/guide/en/volte.md#the-offer-and-the-answer). The whole offer is in the guide: AMR as 98 and 99, PCMU as 0, telephone-event as 100 and 101 at the clock rate of each speech codec (TS 26.114 Annex G), ptime 20, maxptime 240. RFC 4867.

---

## AMR frames, RTCP and DTMF

```text
Adaptive Multi-Rate
    [Payload decoded as: RFC 3267 bandwidth-efficient mode (1)]
    1111 .... = CMR: No mode request (15)
    .... 0... = F bit: Last frame in this payload
    .... .100  0... .... = FT bits: AMR-WB 23.85 kbit/s (8) / Frame OK
    .1.. .... = Q bit: Ok
```

- One 20 ms frame per packet: the CMR, the table of contents (F, FT, Q), the speech bits (RFC 4867 4.3).
- The UE never encodes or decodes AMR. It sends frames in the storage format of RFC 4867 5, and what was sent compares bit for bit with what was received.
- RTCP is on the port above RTP (RFC 3550 6.4.1, IR.92 3.2.4). DTMF goes as telephone-events (RFC 4733). The filters are `amr`, `rtcp` and `rtpevent`.

> Guide: ["The frames"](../../../docs/guide/en/volte.md#the-frames) and ["RTCP and DTMF"](../../../docs/guide/en/volte.md#rtcp-and-dtmf)

---

## AMR in Wireshark, and the frames to compare

- Wireshark decodes the AMR-WB frames by the SDP it saw. Without that SDP, set **Preferences > Protocols > AMR** to Wideband AMR and to the payload format, and use **Decode As** for the RTP port.
- `amr.wb.cmr` and `amr.wb.toc.ft` filter on the CMR and the frame type of AMR-WB.
- `-sent-out` and `-received-out` write the frames to compare; the e2e script compares them.
- The frames sent are tones embedded in `internal/ue/speech`, made with `tools/amrgen`, a tool with cgo outside the product build.
- Each DTMF digit is a run of packets, the last three with the end bit.

---

## Session timer, Reason, early media, hold

| Item | What to look at in the capture |
| --- | --- |
| Session timer | `Session-Expires: 1800` in the INVITE, `refresher=uac` in the 200; the caller sends an UPDATE every 900 seconds. |
| The UE's Reason | `RELEASE_CAUSE;cause=1;text="User ends call"` in the BYE. |
| Early media | `P-Early-Media: supported` in the INVITE. No node authorises it. |
| Hold and resume | A re-INVITE with `a=sendonly`, its 200 with `a=recvonly`. Both Rx sessions are updated. |

The filters are `sip.Session-Expires` and `sip.Reason`.

- A call ended because its media bearer was lost has cause 3 in the UE's Reason; the P-CSCF's BYEs say `Reason: SIP;cause=503`. With no early media authorised, the caller plays a ringing tone of its own after the 180.
- The hold re-INVITE passes the MMTel AS as it came. The call with hold and DTMF is the one that the e2e script drives through the dashboard.

> Guide: ["Session timer, Reason and early media"](../../../docs/guide/en/volte.md#session-timer-reason-and-early-media) and ["Hold, resume and DTMF"](../../../docs/guide/en/volte.md#hold-resume-and-dtmf). IR.92 2.2.8, 2.2.4, 2.2.7. RFC 4028 9. TS 24.229 7.2A.18.11. RFC 5009. TS 24.610 4.5.2.1.

---

## The network takes the bearer

```sh
curl -X DELETE http://localhost:8080/api/epc/network-initiated/ues/001010000000010/bearers/6
```

```ladder
volte-a, P-CSCF, S-CSCF, volte-b, EPC and PCRF, dashboard
EPC and PCRF <- dashboard: DELETE .../bearers/6
volte-a <- EPC and PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST, on the event stream
P-CSCF <- EPC and PCRF: ASR, Abort-Cause BEARER_RELEASED (0) // then the ASA
P-CSCF -> EPC and PCRF: STR // then the STA
P-CSCF -> S-CSCF: BYE in volte-a's name // cause=503
volte-a <- P-CSCF: BYE // cause=503
P-CSCF -> volte-b: BYE, after the S-CSCF and the MMTel AS
volte-b <- EPC and PCRF: DEACTIVATE EPS BEARER CONTEXT REQUEST
```

> Guide: ["The network takes the bearer"](../../../docs/guide/en/volte.md#the-network-takes-the-bearer). TS 29.214 4.4.6.1, 4.4.6.2. TS 24.229 5.2.8.1.2. IR.92 2.4.2.3.

---

## The network takes the bearer: why it looks like this

- The voice bearer carried all the media, so the PCRF ends the Rx session with an ASR, not an RAR. volte-a sends no BYE of its own.
- The P-CSCF builds the BYEs from the dialog it keeps behind its `Store`. The BYE to the other side goes in the served user's name.
- The BYE to the served UE is this project's addition: a UE that lost only its voice bearer still has its signalling, and without a BYE it would stay in a call that the network has ended (the comment in `internal/pcscf/release.go`).
- A call that ends with an ordinary BYE has each side's P-CSCF end its Rx session with an STR, Termination-Cause DIAMETER_LOGOUT (TS 29.214 4.4.4).
- The filter is `diameter.Abort-Cause`. All of Rx is `diameter.cmd.code in {265, 258, 274, 275}`: AA, RA, AS and ST; RA does not occur in this run.

---

## What IR.92 asks for

| IR.92 | What | This project |
| --- | --- | --- |
| 4.4.1, 4.4.2, 4.5 | Signalling on QCI 5, voice on QCI 1, the P-CSCF in the PCO | The EPC stub |
| 2.4.1 | Preconditions, with UPDATE for the status | Done |
| 2.4.2.1, 2.4.2.3 | The network ends the session when a bearer is lost | The P-CSCF's BYEs |
| 3.2.1, 3.2.5, 2.4.3.2 | AMR and AMR-WB, both payload formats | Frames encoded beforehand |
| 3.2.4, 2.2.4, 2.2.8 | RTCP, Reason, the session timer | Done |

- Not built: the mmtel ICSI, +sip.instance and the audio tag in the Contact of the REGISTER; following the CMR; IPsec ESP; IPv6. The next slide has the reasons.

---

## What is not built, and why

- The Contact of the REGISTER has no ICSI, no +sip.instance and no audio tag. The Contact of the INVITE and of its responses has the ICSI and audio.
- Following the CMR needs a UE that can encode; this UE sends frames encoded beforehand.
- ESP is left out so that the captures stay readable.
- Every node uses IPv4, on one Docker network.
- The national standards, such as TTC in Japan, cover the interconnection of networks, so they do not shape the call inside one network. Differences such as the length of the session timer belong to the profile of each peer network: the interconnect guide covers them.

> Guide: ["How a VoLTE phone differs from a softphone"](../../../docs/guide/en/volte.md#how-a-volte-phone-differs-from-a-softphone) and ["What this project simplifies"](../../../docs/guide/en/volte.md#what-this-project-simplifies)

---

## What this project simplifies

| Where | The main items |
| --- | --- |
| EPC stub | No NAS encoding, S1-AP, radio, GTP-U, Gx, or authentication of the attach. A bearer is a record. |
| PCRF | Audio only, with QCI 1. No DRA. |
| P-CSCF, UE, S-CSCF | No ESP. AKAv1-MD5 only. |
| P-CSCF | Releases at once when a bearer is lost, as each call has one audio stream. A dialog that ends without a BYE stays in the `Store`: the P-CSCF keeps no index of dialogs by UE, so that several P-CSCF nodes can serve the same users. |
| UE and AMR | CMR and mode-set are not followed. A UE that loses its PDN connection stops. |

```sh
grep -rn "SIMPLIFIED:" internal/epc internal/pcscf internal/ue internal/aka internal/amr internal/rtcp internal/precondition
```

> Guide: ["What this project simplifies"](../../../docs/guide/en/volte.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Drive it from the dashboard

```sh
docker compose up -d --build
docker compose --profile volte up -d volte-a volte-b
curl http://localhost:8080/api/epc/ues/001010000000010
```

1. Open <http://localhost:8080>. The cards of volte-a and volte-b say that they are registered.
2. Call `tel:+12025550112` from volte-a. volte-b answers by itself after its ring time.
3. Press the hold, resume and DTMF buttons of the card. It also shows the codec and the precondition table.
4. Release the voice bearer with the `curl -X DELETE` of the previous slide.

- The ladder of the call has a column for the PCRF, next to the P-CSCF.
- The `curl` lists the bearers of volte-a: 5, and 6 during a call. Under `/api/epc` the dashboard passes on the state of a UE and the EPC stub's paths that start a procedure of the network.

> Guide: ["With the dashboard"](../../../docs/guide/en/volte.md#with-the-dashboard)

---

## Drive it from the command line

```sh
docker compose --profile volte rm -sf volte-a volte-b
docker compose run --rm volte-b answer -epc http://172.28.0.80:8080 \
  -impi 001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000012@ims.mnc001.mcc001.3gppnetwork.org \
  -k fec86ba6eb707ed08905757b1bb44b8f -opc 1006020f0a478bf6b699f15c062e42b3 \
  -received-out /captures/volte-b-received.amr
docker compose run --rm volte-a call -epc http://172.28.0.80:8080 \
  -impi 001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:001010000000010@ims.mnc001.mcc001.3gppnetwork.org \
  -k 465b5ce8b199b49faa5f0a2ee238a6bc -opc cd63cb71954a9f4e48a5994e37a02baf \
  -sent-out /captures/volte-a-sent.amr tel:+12025550112
```

- `-epc` attaches, and `-k` with `-opc` uses IMS AKA.
- `-codecs` picks the codecs of the offer. `-no-preconditions` leaves the preconditions out.
- The first line stops the UEs of the profile, which share the addresses of volte-a and volte-b with these commands. Run volte-b in one terminal and volte-a in another. The guide has the command that registers SIPp with IMS AKA.

> Guide: ["From the command line"](../../../docs/guide/en/volte.md#from-the-command-line). The flags are listed in a table there.

---

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.Security-Client \|\| sip.Security-Server \|\| sip.Security-Verify` | sec-agree |
| `diameter.3GPP-SIP-Authentication-Scheme == "Digest-AKAv1-MD5"` | MAR, MAA of IMS AKA |
| `diameter.cmd.code in {265, 258, 274, 275}` | Rx: AA, RA, AS, ST |
| `diameter.Flow-Usage == 2`, `diameter.Codec-Data` | AARs: signalling, media |
| `http && ip.addr == 172.28.0.80` | The EPC stub's API |
| `sip.Require contains "precondition"` | Precondition 183s |
| `amr`, `rtcp`, `rtpevent` | AMR, RTCP, DTMF |

- The guide checked these filters with tshark 4.2 against a capture of the e2e script.

> Guide: ["Wireshark display filters"](../../../docs/guide/en/volte.md#wireshark-display-filters). The whole table is in the guide.

---

## Check it in the code

| What you saw | Where to look |
| --- | --- |
| The EPC stub and the PCRF | `http.go`, `api.go`, `network.go`, `pcrf.go` in `internal/epc` |
| The UE's access network | `internal/ue/access.go`, `access_epc.go` |
| MILENAGE and the vector | `internal/milenage`, `internal/aka` |
| The IMS AKA challenge, sec-agree | `internal/scscf/aka.go`, `internal/pcscf/secagree.go`, `internal/ue/aka.go` |
| Policy control and Rx | `internal/policy`, `internal/rx`, `internal/pcscf/policy.go` |
| The release by the network | `internal/pcscf/release.go`, `dialog.go` |
| Preconditions, AMR, RTCP | `internal/precondition`, `internal/amr`, `internal/rtcp` |

- `internal/ue/volte_epc_test.go` and `volte_aka_test.go` run the VoLTE call through the whole IMS in one process.
- Match the three methods of `PolicyControl` in `internal/policy`, `SubscribeSignallingPath`, `AuthorizeMedia` and `Release`, with the AARs and the STR of this session.

> Guide: ["Where the code is"](../../../docs/guide/en/volte.md#where-the-code-is)

---

[layout: exercise]

## Exercises

1. Decode the nonce of the 401 with base64 and compare it with the SIP-Authenticate of the MAA. Where is the AMF in AUTN?
2. Read the `Flow-Description` of the callee's AAR for the call. Between which two addresses are the flows?
3. Compare the frame numbers of volte-b's 180 and of its 200 to the UPDATE.
4. Read the `Flow-Status` of both sides in the AARs after a hold.
5. Call ue2 from volte-a. Which codec does the answer pick, and is there an AAR for the call?
6. Find the BYEs after the bearer is released. Who sent them, and with which Reason?

For 1, use the commands of "Check the vector by hand" in the guide; the same section computes the response of the second REGISTER in a shell.

---

[layout: answers]

## Exercises: answers

1. The first 16 octets are RAND, the rest AUTN. Octets 7 and 8 of AUTN are the AMF, 0000.
2. The UE (172.28.0.121) and the relay (172.28.0.50), not the other UE.
3. The 180 comes later: the callee alerts only once every mandatory precondition is met.
4. The caller's side ENABLED-UPLINK (0), the callee's ENABLED-DOWNLINK (1).
5. PCMU. ue2 did not attach, so its side has no AAR for the call; look for the AAR of volte-a's side.
6. The P-CSCF, with `Reason: SIP;cause=503`. volte-a sends no BYE of its own.

---

[layout: exercise]

## Looking back

1. Where does the UE learn the P-CSCF address from?
2. Why do the `ik` and `ck` of the 401 never reach the UE?
3. With which values does the UE check the network?
4. On which response does the P-CSCF send the AAR for a call?
5. Why did an ASR come rather than an RAR?
6. What happens on Rx when a softphone that did not attach registers?

---

[layout: answers]

## Looking back: answers

1. From the container with the identifier 000CH in the PCO of the ATTACH ACCEPT (method II of TS 24.229 L.2.2.1).
2. The P-CSCF takes them out. IK and CK are the keys of the associations, and the UE computes the same keys from its K and the RAND, so they need not travel.
3. The MAC in AUTN and the SQN. A matching MAC shows that the network knows K.
4. When it has the SDP answer: on the callee's side the 183 from volte-b, on the caller's side the 183 on its way to volte-a.
5. The voice bearer carried all the media of the session, so the PCRF ends the whole Rx session.
6. The PCRF answers the AAR of the signalling path with 5065 (IP-CAN_SESSION_NOT_AVAILABLE), and the P-CSCF serves that UE without policy control.

---

## Where to go from here

- [Connect two IMS networks](../../../docs/guide/en/interconnect.md): the IBCF and the TrGW at the border, and the profile of each peer network.
- The two boundaries of this session, the UE's `AccessNetwork` and the P-CSCF's `PolicyControl`, are where another access network would plug in: NAS behind the first, the N5 of the 5GC behind the second.

```sh
docker compose --profile ue --profile volte down --rmi all -v --remove-orphans
```

- This is the "Clean up" of the guide. If the `sim` profile of the dashboard was used too, add `--profile sim`.
- In the 5GC, SIP signalling uses a QoS flow of 5QI 5 and the voice one of 5QI 1: the same idea as QCI 5 and QCI 1 in EPS.

> The guides are listed in [docs/guide/](../../../docs/guide/).
