---
title: Supplementary services with the MMTel AS
footer: ims-practice study session 5. Supplementary services
lang: en
---

# Supplementary services with the MMTel AS

ims-practice study session 5 (the MMTel AS, its settings over Ut/XCAP, and the services in the capture)

---

[layout: summary]

## What you will know after this session

To begin: you call a phone that forwards its calls, and another phone rings. Which node decides that?

- What a supplementary service is, and which node runs it.
- How the S-CSCF hands a call to the AS: iFCs, P-Served-User, ODI.
- How the AS, a B2BUA, turns one call into three dialogs.
- Where a user's settings are, and how the UE changes them over Ut with XCAP.
- What to look at in the capture for each service: identities, forwarding, barring.

> Guide: [Supplementary services with the MMTel AS](../../../docs/guide/en/mmtel.md) (`docs/guide/en/mmtel.md`)
> The editions are named at the top of the guide, among them TS 24.229 V20.1.0, TS 24.604 V19.0.0, TS 24.607 V19.1.0, TS 24.611 V19.0.0, TS 24.623 V19.0.0, TS 29.364 V19.1.0 and GSMA IR.92 v24.0.

---

## Where supplementary services run

- Supplementary services are what a phone network offers beside the call: showing or hiding the number, forwarding, barring, hold, call waiting.
- A phone call over the IMS is the MMTel service (TS 24.173). GSMA IR.92 2.3 lists what the network offers.
- The CSCFs only route. The node that knows the user's settings and changes the call is an application server (AS), the MMTel AS.

```text
 UE --- P-CSCF --- S-CSCF ===(SIP, iFC)===> MMTel AS ---(Sh)--- HSS
  |                                            |
  +---------------(Ut, HTTP + XCAP)------------+
```

- The SIP reference point between the S-CSCF and an AS is called ISC. The `mmtel` role listens on SIP port 5060 for the AS and on HTTP port 80 for the XCAP server.
- MWI and ECT, which IR.92 leaves optional for the network, are not built. Announcements and conferences are played and mixed by the MRF, which Compose runs as `mrfc` and `mrfp`.

> Guide: ["What MMTel is, and why an application server"](../../../docs/guide/en/mmtel.md#what-mmtel-is-and-why-an-application-server)

---

## The nodes

| Node | Address | What it does |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller, `tel:+12025550101`. |
| UE 2 | 172.28.0.101 | The served user who sets up its services, `tel:+12025550102`. |
| UE 3 | 172.28.0.103 | Where UE 2 forwards its calls, `tel:+12025550103`. |
| P-CSCF, S-CSCF, I-CSCF | 172.28.0.40, 30, 20 | Assert and withhold identities. The iFCs send to the AS. |
| MMTel AS | 172.28.0.75 | The `mmtel` role: `mmtel.…` for SIP, `xcap.…` for Ut. |
| HSS | 172.28.0.10 | The iFCs (Cx). The settings and the Ut passwords (Sh). |

> Guide: ["The nodes"](../../../docs/guide/en/mmtel.md#the-nodes)

---

## How a request reaches the AS: four filter criteria

| Priority | Matches | DefaultHandling |
| --- | --- | --- |
| 10 | INVITE, ORIGINATING_REGISTERED | SESSION_TERMINATED |
| 11 | INVITE, TERMINATING_REGISTERED | SESSION_CONTINUED |
| 12 | INVITE, TERMINATING_UNREGISTERED | SESSION_CONTINUED |
| 13 | INVITE, ORIGINATING_CDIV | SESSION_TERMINATED |

- Each sends to `sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060`.
- On the originating side the call ends when the AS does not answer, so that barring holds.
- They come after the two criteria of SMS, priorities 0 and 1. Each matches one session case only, so they never compete. The one for TERMINATING_UNREGISTERED is there for CFNL.

> Guide: ["The four filter criteria"](../../../docs/guide/en/mmtel.md#the-four-filter-criteria). TS 29.228 Annex E, B.2.3. TS 24.229 5.4.3.2 step 5, 5.4.3.3 step 4.

---

## Originating or terminating: P-Served-User

```text
P-Served-User: <sip:+12025550101@…>;sescase=orig;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=reg
P-Served-User: <sip:+12025550102@…>;sescase=term;regstate=unreg
P-Served-User: <sip:+12025550102@…>;orig-cdiv;regstate=reg
```

| Case | The order of the services the AS runs |
| --- | --- |
| orig | OCB, OIR, then TIP for the responses |
| term | ICB and ACR, CFU and CFNL, OIP and TIR, the leg to the user; by how it ends, CFB or CFNR |
| orig-cdiv | The OCB of the diverting user |

The filter is `sip.P-Served-User`.

- One AS serves both ends of a call, so it has to know for which user, and in which case, each INVITE comes. An INVITE without P-Served-User did not come from the S-CSCF, and the AS answers 403.

> Guide: ["P-Served-User and the session cases"](../../../docs/guide/en/mmtel.md#p-served-user-and-the-session-cases). RFC 5502, RFC 8498. TS 24.229 5.7.1.3A.

---

## The order in the code: terminating

[internal/mmtel/sessioncase.go](../../../internal/mmtel/sessioncase.go)

```go
	info := c.callInfo(fields, inv.RequestURI)
	if c.barIncoming(info) {
		return
	}
	// CFU, and CFNL for a user who is not registered (TS 24.604 4.6.7).
	if d, ok := c.divertAtSetup(info); ok {
		c.divert(d)
		return
	}
```

- Barring comes first. A barred call is not forwarded.
- CFU and CFNL divert when the INVITE arrives. The user's phone does not ring.
- OIP and TIR follow. Then `c.attempt` tries the leg to the user, and how it ends decides CFB, CFNRc or CFNR. The clause in the comment is the one on how the services interact.

---

## The ODI, and three dialogs for one call

```text
Route: <sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>
```

```text
 UE 1 ... S-CSCF -- AS(UE 1) -- S-CSCF ... I-CSCF ... S-CSCF -- AS(UE 2) -- S-CSCF ... UE 2
 |<------ C1 ------>|<------------------ C2 ------------------->|<--------- C3 ---------->|
```

- The AS is a routeing B2BUA. It answers the INVITE as a UA and sends a new one, with a Call-ID of its own.
- The new INVITE comes back to the S-CSCF with the ODI, and the S-CSCF goes on with the next criterion. The IP-SM-GW of the SMS session sent nothing back; the MMTel AS does.
- Why a B2BUA and not a proxy: CFNR, a diversion that changes To, an OIR that makes From anonymous, the announcements and the conferences all need one.

> Guide: ["The original dialog identifier"](../../../docs/guide/en/mmtel.md#the-original-dialog-identifier) and ["A B2BUA, not a proxy"](../../../docs/guide/en/mmtel.md#a-b2bua-not-a-proxy-one-call-three-dialogs). TS 24.229 5.7.5, 5.7.5.2.1.

---

## What three dialogs do to the capture

| Look at | What to see |
| --- | --- |
| Call-ID | Three per call. A diverted call adds three more. |
| icid-value of P-Charging-Vector | The same in all three, inside the network. The Lab joins them into one call by it. |
| LIR | It belongs to dialog C2. |
| PRACK and ACK | They go dialog by dialog. The ACK can reach UE 2 before the 200 reaches UE 1. |
| BYE | The AS answers it, and sends a new BYE with the same Reason on the next dialog. |

The filter is `sip.P-Charging-Vector contains "icid-value=..."`, with the value for `...`: copy it from the INVITE that UE 1's P-CSCF sends to the S-CSCF.

- The header stops at the P-CSCF. The UE sends none, and the P-CSCF removes it from every request and response it sends to a UE. The capture of the ims0 bridge has the legs to and from the UEs too, but the filter leaves them out.
- Wireshark's **VoIP Calls** lists each dialog as a call of its own, so one call looks like three at first sight.

> Guide: ["A B2BUA, not a proxy"](../../../docs/guide/en/mmtel.md#a-b2bua-not-a-proxy-one-call-three-dialogs). TS 24.229 5.7.5.1, 5.2.1. RFC 3326 2.

---

## Where the settings are: the simservs document

- Each user has one XML document, the simservs of XCAP (TS 24.623, RFC 4825).
- It lives only in the HSS, as Sh repository data with Service-Indication `MMTEL-Services` (TS 29.364 7.2).
- The AS reads it with a UDR for each call and keeps no copy, so that any instance of the AS can take any call.

[deploy/compose/hss.yaml](../../../deploy/compose/hss.yaml)

```xml
            <cp:rule id="cfu">
              <cp:conditions><rule-deactivated/></cp:conditions>
              <cp:actions><forward-to><target/></forward-to></cp:actions>
            </cp:rule>
```

- The first document has a rule for every service that IR.92 asks for. Those of CDIV and CB are switched off, with `rule-deactivated` and no target. A UE adds no rule: it switches a service on by changing the rule that is there.
- Its address is `http://xcap.ims.mnc001.mcc001.3gppnetwork.org/simservs.ngn.etsi.org/users/<XUI>/simservs.xml`, where the XUI is the user's public identity.

> Guide: ["The document"](../../../docs/guide/en/mmtel.md#the-document). GSMA IR.92 2.3.2 Note 3.

---

## Change it over Ut: CFU on

What `ue xcap set cfu on tel:+12025550103` sends:

| Step | Request | The answer, and the Sh in between |
| --- | --- | --- |
| 1 | GET, no credentials | A UDR for the Ut password. 401 with two challenges, SHA-256 then MD5. |
| 2 | GET with `Authorization: Digest` | Two UDRs. 200 with `ETag: "0"`. |
| 3 | PUT of one rule, a node selector after `~~`, `If-Match: "0"` | A PUR with `<SequenceNumber>1</SequenceNumber>`. After the PUA, 200 with `ETag: "1"`. |
| 4 | GET | The document, printed as settings. |

- If the document changed in between, If-Match does not match and the AS answers 412 without a PUR. The ETag is the Sequence Number that the HSS checks on each update. When another write gets in between the read and the PUR, the HSS refuses the PUR with DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC, and the AS answers 412 all the same.
- The filter is `http && ip.addr == 172.28.0.75`; `http.www_authenticate` finds the challenges, `http.request.method == "PUT"` the changes. The guide has the whole URI and body of the PUT.

> Guide: ["One change: CFU on"](../../../docs/guide/en/mmtel.md#one-change-cfu-on). RFC 7616 3.3. TS 24.623 5.2.3.2.1. RFC 4825 6, 7.11. TS 29.328 7.6.1, 6.1.2.1.

---

## The HTTP Digest challenge in the code

[internal/xcap/digest.go](../../../internal/xcap/digest.go)

```go
func (n nonces) challenge(h http.Header, c Credential, stale bool) {
	nonce := n.issue()
	for _, a := range algorithms {
		if _, ok := c.ha1(a); !ok {
			continue
		}
		h.Add("WWW-Authenticate", sip.Challenge{
			Realm: c.Realm, Nonce: nonce, Stale: stale, Algorithm: string(a), QOP: []string{qopAuth},
		}.String())
	}
}
```

- `algorithms` are SHA-256, then MD5. All the challenges share one nonce.
- The credential is repository data in the HSS, with Service-Indication `Ut-Digest-Credential`. It holds HA1 values, not the password (the comment in `internal/xcap/credential.go`).
- WWW-Authenticate has the form of SIP Digest, so the code uses the `Challenge` of `internal/sip` as it is: compare it with the 401 of registration.

---

## Run it

```sh
docker compose up -d --build
docker compose run --rm ue2 xcap \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 set cfu on tel:+12025550103

# UE 3 waits for a call
docker compose run --rm ue3 answer -impi 001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org -password test-password-3
docker compose run --rm ue call tel:+12025550102
```

- UE 1 logs `"msg":"call progress","status":181` and then `"msg":"call answered"`.
- `set cfu off` switches it off again. `ue xcap` needs no registration. Its other commands are `get`, `set SERVICE on|off [TARGET]`, `oir restricted|not-restricted` and `noreply SECONDS`.
- The `ue2` service has a fixed address, so `ue2 xcap` cannot run while another container of `ue2` does.

> Guide: ["Run it"](../../../docs/guide/en/mmtel.md#run-it). The e2e script of the supplementary services in `scripts/` runs every service in turn.

---

## Open the capture

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

- It keeps the calls, Ut, the Sh reads and updates of the AS, and the LIRs and SARs.
- **Telephony > VoIP Calls** lists each dialog as a call of its own.
- The e2e script leaves every service in one capture. Narrow it to one service with a display filter.

| Service | Trigger | What the capture shows |
| --- | --- | --- |
| OIR | The caller restricts by default | An anonymous From, `Privacy: id` |
| CFU, CFB, CFNR, CFNL | A call, 486, no answer, not registered | 181, History-Info, `cause=` |
| BAIC, ACR, BAOC | A call, an anonymous call, an outgoing call | An announcement in a 183, then 603 or 433 |

> Guide: ["Open the capture"](../../../docs/guide/en/mmtel.md#open-the-capture)

---

## A plain call through the AS, and TIP

| Look at | What to see |
| --- | --- |
| C1 INVITE from the S-CSCF to the AS | P-Served-User with `sescase=orig`, and a Route with `odi`. |
| The UDR of the AS | Data-Reference 0, Service-Indication `MMTEL-Services`. |
| The C2 INVITE of the AS | The same icid-value. ENUM and the LIR follow. |
| The second AS | Gets it with `sescase=term`, and sends C3 back. |
| 200 OK | UE 2 in P-Asserted-Identity: TIP. |

- With the default document the AS changes nothing, but every INVITE passes an AS twice. Every user has TIP by default: the P-CSCF puts P-Called-Party-ID into P-Asserted-Identity.
- The frame numbers in the guide come from one run of the script, so they differ from the capture at hand.

> Guide: ["A plain call through the AS"](../../../docs/guide/en/mmtel.md#a-plain-call-through-the-as). TS 24.229 5.4.3.2 step 5, 5.7.5.2.1, 5.2.6.4.4 step 1. TS 29.364 7.2.0. TS 24.608 4.5.2.4.

---

## OIR: the caller withholds its identity

UE 1 sets its default to restricted with `ue xcap oir restricted`, then calls.

| Node | What it does |
| --- | --- |
| UE 1's AS | Adds `Privacy: id` and sets From to `"Anonymous" <sip:anonymous@anonymous.invalid>`. Keeps P-Asserted-Identity. |
| UE 2's AS | Runs OIP and keeps `id`. |
| UE 2's S-CSCF | Removes P-Asserted-Identity: the UE is outside the trust domain. |

The filters are `sip.Privacy` and `sip.from.addr == "sip:anonymous@anonymous.invalid"`.

- Inside the network the identity stays asserted; it is withheld only from a UE, outside the trust domain. OIR is in the temporary mode: the UE can still present itself for one call with `Privacy: none`.
- TIR works the same way the other way round: UE 2's AS puts `id` into Privacy of the responses, and UE 1's P-CSCF removes P-Asserted-Identity (TS 24.608 4.5.2.9, RFC 3325 7).

> Guide: ["OIR: the caller withholds its identity"](../../../docs/guide/en/mmtel.md#oir-the-caller-withholds-its-identity). TS 24.607 4.5.2.4, 4.5.2.9. RFC 3323 4.1.1.3. TS 24.229 5.4.3.3 step 12, 4.4.2. RFC 3325.

---

## CFU: forwarding every call

| Look at | What to see |
| --- | --- |
| UE 2's AS | Diverts when the INVITE arrives. It never reaches UE 2. |
| 181 Call Is Being Forwarded | Tells the caller. The target's entry has `Privacy=history`. |
| Request-URI of the diverted INVITE (C4) | `sip:+12025550103@…;user=phone;cause=302` |
| History-Info | Index 1 is UE 2; index 1.1 is UE 3, with `mp=1`. |
| INVITE from the S-CSCF to the AS | `orig-cdiv`. UE 2's outgoing barring applies here. |

- A call is diverted at most 5 times. After that, 480 with `Warning: 399`.
- The causes are 302 for CFU, 486 for CFB, 408 for CFNR, 404 for CFNL and 503 for CFNRc (TS 24.604 Annex C). The guide's ladder has six dialogs, C1 to C6.
- Why does UE 3's entry carry `Privacy=history`? UE 2's AS does not know whether UE 3 lets its identity be shown.

The filters are `sip.History-Info`, `sip.r-uri contains "cause="` and `sip.Status-Code == 181`.

> Guide: ["CFU: forwarding every call"](../../../docs/guide/en/mmtel.md#cfu-forwarding-every-call). TS 24.604 4.5.2.6.2, 4.5.2.6.3, 4.5.2.6.4, 4.5.2.6.1. RFC 4458. RFC 7044 10.3. TS 24.229 5.4.3.3 step 3.

---

## CFB and CFNR: busy and no answer

| Case | What to see |
| --- | --- |
| CFB | UE 2 answers C3 with 486. The AS acknowledges it, sends the 181, diverts with `cause=486`. |
| CFNR timer | Starts with the first 180. Its length is `NoReplyTimer`, 5 s in the e2e. |
| CFNR CANCEL | `Reason: SIP;cause=408;text="Request Timeout"`, copied by each CSCF. |
| 199 Early Dialog Terminated | Ends UE 2's early dialog on C2. It does not reach UE 1. |
| Then | The 181, and a diversion with `cause=408`. |

The filters are `sip.Method == "CANCEL" && sip.Reason` and `sip.Status-Code == 199`.

- Without a NoReplyTimer, the operator's default of 20 seconds applies. Measure the time from the 180 to the CANCEL in the capture.
- UE 1's AS keeps its one early dialog with UE 1, so UE 1 gets the 181 and UE 3's responses on the same early dialog.

> Guide: ["CFB"](../../../docs/guide/en/mmtel.md#cfb-the-callee-is-busy) and ["CFNR"](../../../docs/guide/en/mmtel.md#cfnr-nobody-answers). TS 24.604 4.5.2.6.2.2 b, 4.5.2.6.3 item 2, 4.8.1. RFC 3326 2. RFC 6228.

---

## CFNL: the callee is not registered

| Step | Look at | What to see |
| --- | --- | --- |
| 1 | The I-CSCF's LIR | DIAMETER_UNREGISTERED_SERVICE (2003) with the S-CSCF capabilities. |
| 2 | The S-CSCF's SAR | UNREGISTERED_USER, for the profile. |
| 3 | INVITE to the AS | `sescase=term;regstate=unreg` |
| 4 | The AS diverts | By the rule with `<not-registered/>`, at once, `cause=404`. |

- Without CFNL, the AS sends the INVITE back unchanged and the S-CSCF answers 480, not the I-CSCF.
- The LIA is 2003 because every test subscriber has a service for the unregistered state, the iFC of the MMTel AS for TERMINATING_UNREGISTERED. A subscriber without one would get DIAMETER_ERROR_IDENTITY_NOT_REGISTERED, and the I-CSCF would answer 480.

The filters are `diameter.Experimental-Result-Code == 2003` and `sip.P-Served-User contains "regstate=unreg"`.

> Guide: ["CFNL: the callee is not registered"](../../../docs/guide/en/mmtel.md#cfnl-the-callee-is-not-registered). TS 29.228 6.1.4.1 step 3, 6.7, 6.1.2.1. TS 24.229 5.4.3.3. TS 24.604 4.5.2.6.2, 4.6.7.

---

## Barring

| Service | Setting | What to see |
| --- | --- | --- |
| BAIC | UE 2's `baic on` | UE 2's AS answers 603 Decline, before any other service. |
| ACR | The rule with `<anonymous/>` | 433 Anonymity Disallowed. |
| BAOC | UE 1's `baoc on` | UE 1's AS answers 603 and sends no INVITE of its own. Emergency calls are never barred. |
| BOIC, BOIC-exHC | `boic on` and so on | The callee's country code against the home network's, `1`. |

- Before the 603 or the 433 the caller hears an announcement. The AS sends a reliable 183 with the SDP of the MRFP and `P-Early-Media: sendonly`. When the MRFC ends the announcement with BYE, the 603 follows with `Reason: SIP;cause=603`.
- A 603 is found with `sip.Status-Code == 603`. With BAOC there is no C2. The home country code is in `deploy/compose/mmtel.yaml`.

> Guide: ["Barring"](../../../docs/guide/en/mmtel.md#barring). TS 24.611 4.5.2.6.1, 4.6.7, 4.5.2.4.1, 4.9.1.4. RFC 5079.

---

## CW and HOLD

- IR.92 asks for call waiting done by the UE, not the network (2.3.4).
  - The callee's UE sends a 180 with `Alert-Info: <urn:alert:service:call-waiting>`.
  - The AS passes it on, or takes the Alert-Info out by the operator's setting.
- HOLD is a re-INVITE with `a=sendonly`. The AS passes it on to the other leg as it came.
- `ue serve` takes a second call while it has one: it rings with that Alert-Info, and `POST /answer` holds the first call and takes the waiting one. `scripts/e2e-mrf.sh` shows CW in the capture, and in-process tests check it.

```sh
go test -run 'TestCommunicationWaiting|TestHoldThroughTheIMS' -v ./internal/mmtel
go test -run TestServeTwoCalls -v ./internal/ue
```

- `ue serve` answers a third call with 486. The **Hold** button of the Lab sends HOLD too.

> Guide: ["CW and HOLD"](../../../docs/guide/en/mmtel.md#cw-and-hold). TS 24.615 4.5.5.3.2, 4.5.5.2.3. RFC 7462. TS 24.610 4.5.2.1.

---

## What IR.92 asks for

| IR.92 | What | This project |
| --- | --- | --- |
| 2.3 | The supplementary services of a VoLTE network | OIP, OIR, TIP, TIR, CDIV, CB, HOLD, CW |
| 2.3.2 | Set them over Ut with XCAP, one rule at a time, If-Match | `ue xcap` |
| 2.3.2 Note 3 | The operator puts every rule in the first document | The switched-off rules of hss.yaml |
| 2.3.4 | CW is done by the UE | The AS passes Alert-Info on |
| 2.3.5, 2.3.11 | MWI and ECT are optional for the network | Not built |
| 2.2.2 | GBA is recommended | Not built; Digest |

- The project follows what 3GPP has in common. The differences per country, such as TTC in Japan, concern the connection between networks, so they belong to the profile of each peer network, which the interconnect guide covers.

> Guide: ["What MMTel is, and why an application server"](../../../docs/guide/en/mmtel.md#what-mmtel-is-and-why-an-application-server) and ["What this project simplifies"](../../../docs/guide/en/mmtel.md#what-this-project-simplifies)

---

## What this project simplifies

| Where | The main items |
| --- | --- |
| AS | No announcement on the 181. No subscription to the settings; read for each call. BIC-Roam never holds. |
| XCAP | No GBA. Plain HTTP, so that the capture shows the XML. A replayed nonce count is not detected. No xcap-diff subscription. |
| simservs | Some conditions and the serv-cap are missing. A country is its country code, so the countries under 1 count as one. |
| CSCFs and HSS | No privacy on History-Info entries. No public service identities, no ORIGINATING_UNREGISTERED. |

```sh
grep -rn "SIMPLIFIED:" internal/mmtel internal/xcap internal/simservs internal/config/xcap.go cmd/ue/xcap.go
```

- Match the output with the table of the guide. The items of the CSCFs and the HSS are in `internal/scscf`, `internal/icscf` and `internal/hss`.
- The nonce count is the price of running several instances: keeping the counts would need a Store that every instance shares.

> Guide: ["What this project simplifies"](../../../docs/guide/en/mmtel.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Set it from the Lab

```sh
docker compose --profile phones up -d --build
```

1. On the card of `phone2`, open **Supplementary services (Ut/XCAP)**. Every service starts as `off`.
2. Choose **CFU (unconditional)**, type a target and press **On**.
3. The list shows `CFU (unconditional): on to …`, and the ETag goes up by one.
4. **Off** switches it off. The target stays.

- The ladders show neither XCAP nor Sh: neither belongs to a SIP request. The capture has them, as HTTP on port 80 and Diameter on port 3868.
- The Lab draws all the dialogs of a call in one ladder, with a column for the MMTel AS. Do not run the `phones` and `ue` profiles at once: a subscriber would have two UEs.

> Guide: ["Drive the UE simulators" in Watch the IMS in the Lab](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## Check it in the code

| What you saw | Where to look |
| --- | --- |
| The B2BUA, the two legs of each call | `mmtel.go`, `call.go` in `internal/mmtel` |
| The order of the services | `internal/mmtel/sessioncase.go` |
| Identities, forwarding, barring, CW | `identity.go`, `diversion.go`, `barring.go`, `waiting.go` in `internal/mmtel` |
| The document and how rules are evaluated | `internal/simservs` |
| The XCAP server of Ut, and Digest | `internal/xcap` |
| The UE's `ue xcap` | `internal/ue/xcap.go`, `cmd/ue/xcap.go` |
| P-Served-User, the ODI, the iFCs | `internal/scscf/services.go`, `internal/ifc` |
| The settings and the Ut passwords in the HSS | `servicedata.go`, `sh.go` in `internal/hss`, `deploy/compose/hss.yaml` |

- `internal/mmtel/network_test.go` runs every service through the whole IMS in one process. Find the cause values in `internal/mmtel/diversion.go` and match them with the `cause=302` of the capture.

> Guide: ["Where the code is"](../../../docs/guide/en/mmtel.md#where-the-code-is)

---

[layout: exercise]

## Exercises

1. Set CFNR, change `noreply 5` to `noreply 10`, and call. Measure the time from the 180 to the CANCEL.
2. Filter a diverted call by its icid-value and count its dialogs.
3. Set `boic on` and call UE 2 from UE 1. Is the call barred?
4. Explain why in `hss.yaml` only the originating criteria have SESSION_TERMINATED.
5. Read the `Authorization` of the second XCAP GET. Which algorithm did the UE answer with?

For 1 and 3, run `ue xcap` as UE 1 or UE 2, in the form of the slide "Run it".

---

[layout: answers]

## Exercises: answers

1. About 10 seconds. The timer starts with the first 180.
2. Six, C1 to C6.
3. No. UE 2's number has the country code 1, the same as the home network.
4. So that barring holds. If a call went on without the AS when the AS did not answer, it would pass the barring by.
5. SHA-256, the first challenge.

---

[layout: exercise]

## Looking back

1. How does the S-CSCF know that it sends an INVITE to the MMTel AS?
2. How does the AS know whether the INVITE is originating or terminating?
3. Why does one call have three Call-IDs?
4. Why does the AS keep no copy of the user's settings?
5. When two change the settings at once, why does one PUT get 412?
6. With CFU and with CFNR, in which does UE 2's phone ring?

---

[layout: answers]

## Looking back: answers

1. The INVITE matches a criterion of priority 10 to 13 of the iFCs that came in the SAA at registration.
2. From `sescase` and `orig-cdiv` in P-Served-User.
3. Each user has an AS on the path, and the AS, a B2BUA, makes a new dialog.
4. So that any instance can take any call, and the AS can run as several instances. The settings are only in the HSS.
5. The PUT carries in If-Match the ETag of the version it read. The first writer moved the Sequence Number on, so the versions differ: the AS sees it from If-Match, or, if the write came too late for that, the HSS refuses the PUR.
6. With CFNR. CFU diverts when the INVITE arrives, so UE 2 never rings.

---

## Next session

- Next is the VoLTE session: the access network, IMS AKA, Rx and preconditions. VoLTE calls go through the MMTel AS of this session too.
- The announcements, the alerting tone the callee chooses, CW and the conference are in [the MRF guide](../../../docs/guide/en/mrf.md): the MRFC and the MRFP, and `scripts/e2e-mrf.sh`.

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

- This stops the `ue` and the `phones` profiles. The guide's "Clean up" stops only `ue`, which is enough when the Lab was not used.

> The guides are listed in [docs/guide/](../../../docs/guide/).
