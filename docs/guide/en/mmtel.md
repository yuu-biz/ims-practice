# Supplementary services with the MMTel AS

[日本語](../ja/mmtel.md)

This guide follows the supplementary services of multimedia telephony (MMTel) through the IMS: how the S-CSCF hands every call to the MMTel AS, what the AS does to the call as a B2BUA, how a user sets the services up over Ut with XCAP, and one call per service in the capture: identity presentation and restriction, call forwarding (CFU, CFB, CFNR, CFNL) and barring.

The guide follows these editions: TS 24.229 V20.1.0, TS 24.604 V19.0.0 (CDIV), TS 24.607 V19.1.0 (OIP, OIR), TS 24.608 V19.0.0 (TIP, TIR), TS 24.611 V19.0.0 (barring), TS 24.615 V19.0.0 (CW), TS 24.623 V19.0.0 (Ut), TS 29.364 V19.1.0 (the service data), TS 29.328 V20.0.0 (Sh), TS 29.228 (filter criteria) and GSMA IR.92 v24.0. Clause numbers below refer to those editions.

## Before you start

- You have read [Follow a voice call through the IMS](call.md) and [Follow a short message through the IMS](sms.md). This guide reuses what they show: the INVITE, the PRACK and the ACK of a call, and the initial filter criteria (iFCs), the original dialog identifier (ODI) and P-Served-User of the IP-SM-GW.
- The abbreviations are explained in the [glossary](../../glossary.md).

## What MMTel is, and why an application server

A phone call over the IMS is the MMTel service (TS 24.173). Its supplementary services are what a phone network has always offered beside the call itself: showing or hiding who calls (OIP, OIR) and who answers (TIP, TIR), forwarding calls (CDIV), barring calls (CB), holding a call (HOLD) and taking a second call while on one (CW). GSMA IR.92 2.3 lists which of them a VoLTE network must offer.

The CSCFs only route. A service needs a node that knows the user's settings and changes the call by them, so each service is a procedure of an application server (AS): the MMTel AS. The S-CSCF sends it every INVITE of a user by the iFCs, and the AS reads the user's settings from the HSS over Sh for each call (TS 29.364). It keeps no copy of them, so that any instance of the AS can take any call. The user changes the settings over Ut, the HTTP interface between the UE and the AS, with XCAP (TS 24.623). The `mmtel` role runs both: the AS on SIP port 5060, and the XCAP server on HTTP port 80.

MWI and ECT, which IR.92 leaves optional for the network (2.3.5, 2.3.11), are not built.

## The nodes

| Node | Address | What it does for MMTel |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller, `tel:+12025550101`. |
| UE 2 | 172.28.0.101 | The served user who sets up its services, `tel:+12025550102`. |
| UE 3 | 172.28.0.103 | Where UE 2 forwards its calls, `tel:+12025550103`. |
| P-CSCF | 172.28.0.40 | Asserts who calls and who answers, and keeps a withheld identity from the UE. |
| S-CSCF | 172.28.0.30 | Serves all three users. Its iFCs send every INVITE to the MMTel AS. |
| I-CSCF | 172.28.0.20 | Finds the S-CSCF of the callee (LIR), also for a callee who is not registered. |
| MMTel AS | 172.28.0.75 | The `mmtel` role: `mmtel.ims.mnc001.mcc001.3gppnetwork.org` for SIP and `xcap.ims.mnc001.mcc001.3gppnetwork.org` for Ut. |
| HSS | 172.28.0.10 | Holds the iFCs (Cx), and each user's `MMTelServices` document and Ut password as Sh repository data. |

## How the S-CSCF picks the MMTel AS

### The four filter criteria

`deploy/compose/hss.yaml` gives every subscription four criteria for the AS, beside the two of SMS:

```yaml
- priority: 10
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: ORIGINATING_REGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
- priority: 11
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: TERMINATING_REGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
- priority: 12
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: TERMINATING_UNREGISTERED}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED}
- priority: 13
  trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: INVITE}, {group: [1], session_case: ORIGINATING_CDIV}]}
  application_server: {server_name: sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
```

Element by element (TS 29.228 Annex E; [the SMS guide](sms.md) explains the elements that it shares):

| Element | Value here | Meaning |
| --- | --- | --- |
| `Priority` | 10 to 13 | After the SMS criteria, 0 and 1. A criterion matches only one session case, so the four never compete. |
| `ConditionTypeCNF` | `true` | Group 0 and group 1 are ANDed (TS 29.228 B.2.3). |
| `SPT` with `Method` | `INVITE` | Only calls. The services of IR.92 act on INVITE alone (TS 24.604 Annex B). |
| `SPT` with `SessionCase` | `0`, `1`, `2`, `4` | ORIGINATING_REGISTERED: a call that the user makes. TERMINATING_REGISTERED: a call to the user. TERMINATING_UNREGISTERED: a call to the user while not registered, which CFNL needs. ORIGINATING_CDIV: a call that the AS forwarded away from the user, which then gets the user's originating services, such as the barring of the forwarded-to number (TS 24.229 5.4.3.3 step 3). |
| `ServerName` | `sip:mmtel...:5060` | The AS, found in DNS. |
| `DefaultHandling` | `1` SESSION_TERMINATED for the originating cases, `0` SESSION_CONTINUED for the terminating ones | Barring must hold, so a call that the user makes ends when the AS does not answer. A call to the user goes on without the AS (TS 24.229 5.4.3.2 step 5, 5.4.3.3 step 4). |

Because the terminating criteria include TERMINATING_UNREGISTERED, the HSS treats every test subscriber as one with services in the unregistered state: an LIR for a user who is not registered gets DIAMETER_UNREGISTERED_SERVICE (2003) and the capabilities of an S-CSCF, not DIAMETER_ERROR_IDENTITY_NOT_REGISTERED (TS 29.228 6.1.4.1 step 3). The S-CSCF then takes the user's profile with a SAR of UNREGISTERED_USER and runs these criteria (TS 29.228 6.1.2.1). Without call forwarding, the AS sends the INVITE back unchanged and the S-CSCF answers 480.

### P-Served-User and the session cases

One AS serves both ends of a call, so it has to know for whom and in which case each INVITE comes. The S-CSCF says so in P-Served-User (RFC 5502, RFC 8498; TS 24.229 5.4.3.2 step 5 b, 5.4.3.3 step 4 c):

```text
P-Served-User: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;sescase=orig;regstate=reg
P-Served-User: <sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org>;sescase=term;regstate=reg
P-Served-User: <sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org>;sescase=term;regstate=unreg
P-Served-User: <sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org>;orig-cdiv;regstate=reg
```

The AS determines the served user from it (TS 24.229 5.7.1.3A) and runs the services of that case in a fixed order (`internal/mmtel/sessioncase.go`):

- **orig**: OCB first, then OIR, then TIP for the responses.
- **term**: ICB and ACR first, then CFU and CFNL, then OIP and TIR, then the leg to the user; on how it ends, CFB or CFNR.
- **orig-cdiv**: the OCB of the diverting user.

An INVITE without P-Served-User did not come from the S-CSCF, and the AS answers 403. The S-CSCF removes P-Served-User before a request leaves the network (5.4.3.2 step 14A, 5.4.3.3 step 13A).

### The original dialog identifier

As for the IP-SM-GW, the S-CSCF puts the AS and then itself with an ODI on top of Route (TS 24.229 5.4.3.2 step 5 a):

```text
Route: <sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>,<sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr;odi=...>
```

This time the request comes back. The AS removes its own entry and sends its new INVITE along the rest, which brings it to the S-CSCF with the ODI (TS 24.229 5.7.5.2.1 steps 1 and 4). The S-CSCF recognizes the ODI and goes on with the criterion after the one that matched. When the Request-URI has changed, as after a diversion, it evaluates the criteria of ORIGINATING_CDIV for the diverting user instead (5.4.3.3 step 3).

## A B2BUA, not a proxy: one call, three dialogs

The MMTel AS acts as a routeing B2BUA (TS 24.229 5.7.5): it answers the INVITE that the S-CSCF sends it as a UA, and sends a new INVITE, based on the one it got, with a Call-ID of its own. It passes on what no service changes, keeps the icid-value of P-Charging-Vector, and takes out only the orig-ioi (5.7.5.1). TS 24.604 4.5.2.6.0 lets an AS that diverts a call without changing To act as a proxy instead; this AS is a B2BUA for every service, so that it keeps the caller's dialog when a forwarded call rings somewhere else.

Each user has an AS on the path, so a call from UE 1 to UE 2 is three dialogs, each with its own Call-ID, its own tags and its own CSeq:

```text
 UE 1 ── P-CSCF ── S-CSCF ── MMTel AS ── S-CSCF ── I-CSCF ── S-CSCF ── MMTel AS ── S-CSCF ── P-CSCF ── UE 2
 |<--------- dialog C1 ---------->|<------------ dialog C2 ------------>|<--------- dialog C3 ---------->|
            (UE 1's services: sescase=orig)                (UE 2's services: sescase=term)
```

What this does to the capture:

- **Three Call-IDs per call.** A filter on one Call-ID shows one dialog. Inside the network all three carry the same icid-value (the P-CSCF removes the header that holds it from what it sends to a UE), which is how the dashboard lists them as one call (see "How the ladder is built" in [the dashboard guide](dashboard.md)). A diverted call adds three more: the AS's INVITE to the forwarded-to user, the S-CSCF's orig-cdiv pass through the AS, and the forwarded-to user's own AS.
- **The LIR belongs to dialog C2.** It is the AS's INVITE that the S-CSCF sends to the I-CSCF.
- **Requests within the dialog go dialog by dialog.** The PRACK and the ACK of UE 1 end at UE 1's AS, which sends its own on C2; UE 2 gets its PRACK from UE 2's AS. On each dialog the AS sends a request only once the one it sent before has its answer, so its UPDATE waits for the 200 to its PRACK, as a UE's does: otherwise the larger UPDATE, over TCP, could reach the callee before the PRACK, over UDP, and the callee would reject the PRACK for its lower CSeq (RFC 3261 12.2.2). The AS acknowledges a 2xx on its own leg as soon as it comes, so the ACK can reach UE 2 before the 200 reaches UE 1. A BYE is answered by the AS on the dialog it came on, and the AS sends a new BYE on the next one, with the same Reason (RFC 3326 2).
- **A re-INVITE or an UPDATE with SDP goes on to the other leg** and its answer comes back. The re-INVITE of a HOLD goes this way (TS 24.610). A bodyless UPDATE that refreshes a session timer ends at the AS, since each dialog has its own timer.

The INVITE that the AS sends may fork beyond it: several UEs of the callee may answer it, each with an early dialog of its own (RFC 3261 13.2.2.4). The AS is one UAS to the caller, and a UAS gives all its responses to a request one To tag (8.2.6.2), so the caller has one early dialog with the AS however many the other side has. The caller takes the first session description of that dialog as the answer and ignores any later one (13.2.1). The AS therefore passes on the session description of the first dialog that sent one, and the responses of the other dialogs without theirs; the 200 of another dialog reaches the caller without its SDP too. The AS keeps the last session description of each dialog, from its responses, its UPDATEs and its answers to the caller's UPDATEs and PRACKs, and uses that of the dialog that answers: with a CAT, the UPDATE that ends the tone moves the caller's media to that dialog (see [the MRF guide](mrf.md)). The tests of `internal/mmtel` show the case (`TestForkBeyondTheAS`, `TestToneOfAForkedCall`); no node of this project forks an INVITE.

## Ut: setting up the services with XCAP

### The document

Each user has one XML document of the simservs application usage of XCAP (TS 24.623, RFC 4825). The XCAP server keeps none: for each request it reads the user's `MMTelServices` document from the HSS over Sh as repository data with Service-Indication `MMTEL-Services` (TS 29.364 7.2), shows or changes the user's part of it, and writes it back with a PUR (TS 29.328 6.1.2.1). The repository data has a Sequence Number, which the HSS checks on each update and which the XCAP server uses as the entity tag (ETag) of the document (RFC 4825 7.11).

The document lives at:

```text
http://xcap.ims.mnc001.mcc001.3gppnetwork.org/simservs.ngn.etsi.org/users/sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org/simservs.xml
```

`http://xcap.<home domain>` is the XCAP Root URI a UE builds when none is configured (TS 23.003 13.9.1.2), and the user part is the user's public identity (the XUI). The document that `hss.yaml` provisions has every service that IR.92 asks for. The rules of CDIV and CB are there but deactivated, with `<rule-deactivated/>` and no target, so that a UE switches a service on by changing its rule rather than by adding one (GSMA IR.92 2.3.2 Note 3).

### One change: CFU on

`ue xcap set cfu on tel:+12025550103` sends:

1. **GET** of the whole document, without credentials. The AS reads the user's Ut password over Sh, as repository data with Service-Indication `Ut-Digest-Credential` (a UDR), and answers **401** with two `WWW-Authenticate: Digest` challenges, SHA-256 first and MD5 second (RFC 7616 3.3, TS 24.623 5.2.3.2.1).
2. **GET** again, with `Authorization: Digest` for the first challenge. The AS reads the credential and the document (two UDRs) and answers **200** with the document and `ETag: "0"`.
3. **PUT** of one rule, with a node selector after `~~` and `If-Match: "0"` (RFC 4825 6, 7.11; GSMA IR.92 2.3.2):

   ```text
   PUT /simservs.ngn.etsi.org/users/sip:001010000000002@.../simservs.xml/~~/simservs/communication-diversion/cp:ruleset/cp:rule%5B@id=%22cfu%22%5D?xmlns(cp=urn:ietf:params:xml:ns:common-policy)
   Content-Type: application/xcap-el+xml
   If-Match: "0"

   <cp:rule xmlns="http://uri.etsi.org/ngn/params/xml/simservs/xcap" xmlns:cp="urn:ietf:params:xml:ns:common-policy" id="cfu"><cp:conditions/><cp:actions><forward-to><target>tel:+12025550103</target></forward-to></cp:actions></cp:rule>
   ```

   The rule has lost its `<rule-deactivated/>` and has a target. The AS reads the credential and the document again, replaces the rule, checks the result, and writes it with a **PUR** whose Sh-User-Data has `<SequenceNumber>1</SequenceNumber>` (TS 29.328 7.6.1, 6.1.2.1). After the **PUA** it answers **200** with `ETag: "1"`.
4. **GET** of the whole document, which `ue xcap` prints as settings.

If someone else changed the document between the GET and the PUT, the HSS refuses the PUR, the AS answers 412, and the UE reads the document again and retries. `ue xcap noreply 5` replaces the `NoReplyTimer` element the same way: how many seconds a call rings before CFNR diverts it, 5 to 180 (TS 24.604 4.9.1.1A).

## Run it

`scripts/e2e-mmtel.sh` does everything below, checks each message that this guide describes in the capture, and exits with an error if one is missing. It leaves the capture in `captures/`, the frames of the calls, Ut and Sh in `e2e-out/mmtel-ladder.txt`, and the logs of the UEs in `e2e-out/`. The calls take about 90 seconds.

To run one service by hand, start the system and set the service of UE 2. The `ue xcap` command needs no registration:

```sh
docker compose up -d --build
docker compose run --rm ue2 xcap \
  --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-2 set cfu on tel:+12025550103
```

Then have UE 3 wait for a call. The command stays in the foreground until the call ends:

```sh
docker compose run --rm ue3 answer \
  --impi 001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:001010000000003@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-3
```

In another terminal, call UE 2 from UE 1:

```sh
docker compose run --rm ue call tel:+12025550102
```

UE 1 logs `"msg":"call progress","status":181` and then `"msg":"call answered"`; UE 3 logs the incoming call. Switch the service off again with `set cfu off`. The other commands are `ue xcap get`, `set SERVICE on|off [TARGET]` for `cfu`, `cfb`, `cfnr`, `cfnrc`, `cfnl`, `baic`, `bic-roam`, `acr`, `baoc`, `boic` and `boic-exhc`, `oir restricted|not-restricted`, and `noreply SECONDS`. The `ue2` service has a fixed address, so `ue2 xcap` cannot run while another container of `ue2` does.

The dashboard does the same from the UE cards: see "Drive the UE simulators" in [the dashboard guide](dashboard.md). Its ladder draws all the dialogs of a call in one diagram, with a column for the MMTel AS.

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

It shows the calls, Ut, the Sh reads and updates of the AS, and the LIRs and SARs. **Telephony > VoIP Calls** lists each dialog as a call of its own.

## A plain call through the AS

The first call of the script, with every service off. The frames are from one run of the script:

```text
#468  UE 1   -> P-CSCF  C1 INVITE tel:+12025550102
#589  S-CSCF -> MMTel   C1 INVITE  P-Served-User: <UE 1>;sescase=orig;regstate=reg, Route: mmtel, scscf;odi
#596  MMTel  -> HSS        UDR  Data-Reference 0 (RepositoryData), Service-Indication MMTEL-Services, UE 1
#612  HSS    -> MMTel      UDA 2001 with UE 1's MMTelServices
#618  MMTel  -> S-CSCF  C2 INVITE  same icid-value, Route: scscf;odi
#632  I-CSCF -> HSS        LIR  sip:001010000000002@...
#645  S-CSCF -> MMTel   C2 INVITE  P-Served-User: <UE 2>;sescase=term;regstate=reg
#658  MMTel  -> S-CSCF  C3 INVITE  same icid-value
#670  P-CSCF -> UE 2    C3 INVITE
      ... 183, PRACK, 180, PRACK dialog by dialog ...
#886  P-CSCF -> S-CSCF  C3 200 OK  P-Asserted-Identity: <sip:001010000000002@...>
#941  P-CSCF -> UE 1    C1 200 OK  P-Asserted-Identity: <sip:001010000000002@...>
#1164 UE 1   -> P-CSCF  C1 BYE
#1239 P-CSCF -> UE 2    C3 BYE
```

1. **UE 1's S-CSCF sends the INVITE to the AS** (TS 24.229 5.4.3.2 step 5) with P-Served-User and the ODI.
2. **The AS reads UE 1's services** with a UDR (TS 29.364 7.2.0, TS 29.328 6.1.1). The default document changes nothing in the call.
3. **The AS sends a new INVITE, dialog C2** (TS 24.229 5.7.5.2.1). It copies the rest of the Route, the P-Asserted-Identity and the header fields that no service changes.
4. **The S-CSCF goes on with the call** from the ODI: ENUM, then the I-CSCF and its LIR.
5. **UE 2's S-CSCF sends the INVITE to the AS again**, now `sescase=term` (5.4.3.3 step 4), and the AS sends dialog C3 back. Then the call goes on as in [the call guide](call.md).
6. **The 200 names who answered.** UE 2's P-CSCF puts the P-Called-Party-ID of the INVITE into P-Asserted-Identity of UE 2's responses (TS 24.229 5.2.6.4.4 step 1), and UE 1 gets it: TIP, which every user has by default (TS 24.608 4.5.2.4).

## OIR: the caller withholds its identity

UE 1 sets OIR to restricted by default (`ue xcap oir restricted`, which PUTs `simservs/originating-identity-presentation-restriction/default-behaviour`), then calls. OIR in hss.yaml is in the temporary mode: the user chooses the default, and the UE could still present itself for one call with `Privacy: none` (TS 24.607 4.5.2.4).

- **UE 1's AS** adds `Privacy: id` and sets From to `"Anonymous" <sip:anonymous@anonymous.invalid>` (TS 24.607 4.5.2.4, RFC 3323 4.1.1.3). P-Asserted-Identity stays: inside the network the identity is still asserted.
- **UE 2's AS** runs OIP (TS 24.607 4.5.2.9): UE 2 has it, and keeps `id`, so that UE 2 knows that the caller withheld its identity.
- **UE 2's S-CSCF** removes P-Asserted-Identity, because the UE is outside the trust domain and Privacy has `id` (TS 24.229 5.4.3.3 step 12, 4.4.2; RFC 3325).

The INVITE that reaches UE 2 has the anonymous From and `Privacy: id`, and nothing in it names UE 1. TIR works the same way the other way round: UE 2's AS puts `id` into Privacy of UE 2's responses (TS 24.608 4.5.2.9), and UE 1's P-CSCF removes their P-Asserted-Identity before they reach UE 1 (RFC 3325 7).

## CFU: forwarding every call

UE 2 sets CFU to UE 3 over Ut (above) and stays registered. UE 1 calls UE 2:

```text
UE 1  P-CSCF S-CSCF  MMTel AS(UE 1)  I-CSCF  MMTel AS(UE 2)        UE 3
 |INVITE|     |           |            |          |                 |
 |----->|---->|---------->| C1         |          |                 |
 |      |     |<----------| C2         |          |                 |
 |      |     |------------------------>  LIR     |                 |
 |      |     |<------------------------|         |                 |
 |      |     |----------------------------------->| term: CFU      |
 |      |     |<-----------------------------------| 181 on C2      |
 |<-----|<----|<----------| 181 on C1  |          |                 |
 |      |     |<-----------------------------------| INVITE C4, cause=302, History-Info
 |      |     |----------------------------------->| orig-cdiv (C4) |
 |      |     |<-----------------------------------| INVITE C5      |
 |      |     |  ... I-CSCF, UE 3's S-CSCF, UE 3's AS (C6) ...      |
 |      |     |------------------------------------------------------->| INVITE with History-Info
 |<-----|<----|<---------------------------------------------------------| 200 OK
```

- **UE 2's AS diverts when the INVITE arrives** (TS 24.604 4.5.2.6.3 item 1, 4.6.7). The INVITE does not reach UE 2.
- **The caller is told** with **181 Call Is Being Forwarded** (TS 24.604 4.5.2.6.4), with UE 2 in P-Asserted-Identity and the History-Info of the diversion. The target's entry has `Privacy=history`, because the AS does not know whether UE 3 lets its identity be shown.
- **The diverted INVITE**, dialog C4 (TS 24.604 4.5.2.6.2):
  - the Request-URI is the target as a SIP URI with `user=phone` and the cause of the diversion (RFC 4458): `sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org;user=phone;cause=302`;
  - History-Info has two entries, UE 2 with index 1 and UE 3 with index 1.1 and `mp=1`, which tells that 1.1 was retargeted from 1 (RFC 7044 10.3):

    ```text
    History-Info: <sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org>;index=1
    History-Info: <sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org;user=phone;cause=302>;index=1.1;mp=1
    ```

  - the cause values are 302 for CFU, 486 for CFB, 408 for CFNR, 404 for CFNL and 503 for CFNRc (TS 24.604 Annex C).
- **The S-CSCF sees a new Request-URI** with the ODI and runs UE 2's originating services after the diversion: the INVITE goes to the AS again with `orig-cdiv` (TS 24.229 5.4.3.3 step 3, RFC 8498). UE 2's outgoing barring would apply here to the call to UE 3 (TS 24.611 4.6.7). Then the S-CSCF turns the number into UE 3's SIP URI with ENUM (5.4.3.2 step 10) and the call goes on to UE 3 like any other call, through UE 3's own AS.

The AS limits a call to 5 diversions, the operator value in hss.yaml, by the entries with a cause in History-Info. After that it answers 480 with `Warning: 399 ... "Too many diversions appeared"` (TS 24.604 4.5.2.6.1).

## CFB: the callee is busy

UE 2 answers the INVITE of dialog C3 with **486 Busy Here**. UE 2's AS acknowledges it, sends UE 1 the 181, and diverts with `cause=486`; the entry of UE 2 in History-Info gets the Reason of the response, `Reason=SIP%3Bcause%3D486...` (TS 24.604 4.5.2.6.2.2 b, 4.5.2.6.3).

## CFNR: nobody answers

UE 2 sets CFNR to UE 3 and its no reply timer to 5 seconds, and rings without answering. This is the ladder of one run, the frames of the dialogs numbered C1 to C6 in the order they appear:

```text
#7660  34.861 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#7775  34.876 S-CSCF -> MMTel  C1 INVITE   P-Served-User: <UE 1>;sescase=orig;regstate=reg
#7779  34.876 MMTel  -> HSS       UDR      (UE 1's MMTEL-Services)
#7803  34.880 MMTel  -> S-CSCF C2 INVITE   tel:+12025550102
#7817  34.884 I-CSCF -> HSS       LIR      (UE 2)
#7830  34.887 S-CSCF -> MMTel  C2 INVITE   P-Served-User: <UE 2>;sescase=term;regstate=reg
#7834  34.887 MMTel  -> HSS       UDR      (UE 2's MMTEL-Services)
#7843  34.891 MMTel  -> S-CSCF C3 INVITE   sip:001010000000002@...
#7855  34.894 P-CSCF -> UE 2   C3 INVITE
#7858  34.896 UE 2   -> P-CSCF C3 183      then PRACK and 200 dialog by dialog
#7953  34.919 UE 2   -> P-CSCF C3 180      UE 2 rings
#7964  34.922 S-CSCF -> MMTel  C3 180      P-Asserted-Identity: <UE 2>; the no reply timer starts
#7990  34.926 P-CSCF -> UE 1   C1 180      then PRACK and 200 dialog by dialog
#8082  39.924 MMTel  -> S-CSCF C3 CANCEL   Reason: SIP;cause=408;text="Request Timeout"
#8094  39.925 P-CSCF -> UE 2   C3 CANCEL   Reason: SIP;cause=408;text="Request Timeout"
#8101  39.926 UE 2   -> P-CSCF C3 487
#8125  39.928 MMTel  -> S-CSCF C2 199      Reason: SIP;cause=408
#8127  39.928 MMTel  -> S-CSCF C2 181      History-Info: <UE 2>;index=1, <...+12025550103...;cause=408?Privacy=history>;index=1.1;mp=1
#8164  39.932 P-CSCF -> UE 1   C1 181
#8256  39.941 MMTel  -> S-CSCF C4 INVITE   sip:+12025550103@...;user=phone;cause=408, History-Info
#8294  39.946 S-CSCF -> MMTel  C4 INVITE   P-Served-User: <UE 2>;orig-cdiv;regstate=reg
#8314  39.950 MMTel  -> S-CSCF C5 INVITE
#8326  39.958 S-CSCF -> I-CSCF C5 INVITE   sip:001010000000003@... (after ENUM)
#8343  39.961 S-CSCF -> MMTel  C5 INVITE   P-Served-User: <UE 3>;sescase=term;regstate=reg
#8356  39.964 MMTel  -> S-CSCF C6 INVITE
#8369  39.968 P-CSCF -> UE 3   C6 INVITE   History-Info
       ... UE 3's 183, 180 and 200 back to UE 1 on C1 ...
```

- **The first 180 starts the no reply timer** (TS 24.604 4.5.2.6.3 item 2); a 180 from another fork would not start it again. UE 2 sends its 180 when it alerts its user: after its 183, and with preconditions once they are met (RFC 3261 21.1.2; step 9 of [the call guide](call.md)). The timer is UE 2's `NoReplyTimer`, or else the operator's default of 20 seconds in hss.yaml (4.8.1).
- **When it expires, the AS cancels the leg to UE 2** with `Reason: SIP;cause=408` (4.5.2.6.3 item 2), 5.0 seconds after the 180 in this run. Each CSCF on the way copies the Reason into its own CANCEL (RFC 3326 2), so UE 2 learns why.
- **The 199 Early Dialog Terminated** ends the early dialog of UE 2 on dialog C2 (RFC 6228). UE 1's AS keeps its one early dialog with UE 1 and does not pass the 199 on, so UE 1 sees the 181 and then UE 3's responses on the same early dialog.
- From the 181 on, the call is diverted as for CFU, with `cause=408`.

If UE 2 answers 480 with Reason Q.850 cause 19 (no answer from the user), the AS takes it as the end of the no reply time too (4.5.2.6.6).

## CFNL: the callee is not registered

UE 2 sets CFNL to UE 3 and is not registered. The flow before the AS is the one of an unregistered callee with services:

1. The I-CSCF's **LIR** for UE 2 gets **DIAMETER_UNREGISTERED_SERVICE (2003)** with the capabilities, and the I-CSCF picks an S-CSCF (TS 29.228 6.1.4.1 step 3, 6.7).
2. That S-CSCF takes UE 2's profile with a **SAR** of **UNREGISTERED_USER** (TS 29.228 6.1.2.1) and evaluates the criteria of TERMINATING_UNREGISTERED (TS 24.229 5.4.3.3).
3. The INVITE reaches the AS with `P-Served-User: <UE 2>;sescase=term;regstate=unreg`. The AS's CFNL rule has the condition `<not-registered/>`, so it diverts at once with `cause=404` (TS 24.604 4.5.2.6.2, 4.6.7).

Without CFNL, the AS sends the INVITE back unchanged and the S-CSCF answers 480, which is what the call to the unregistered subscriber 3 in [Follow a voice call through the IMS](call.md) shows.

## Barring

- **Incoming (BAIC).** UE 2 sets `baic on`. UE 2's AS evaluates the incoming barring rules before any other service (TS 24.611 4.5.2.6.1, 4.6.7) and answers the INVITE with **603 Decline**. The call never reaches UE 2. A rule with the condition `<anonymous/>`, ACR, answers **433 Anonymity Disallowed** instead (RFC 5079).
- **Outgoing (BAOC).** UE 1 sets `baoc on`. UE 1's AS bars the call before it changes anything (TS 24.611 4.5.2.4.1) and answers **603**. It sends no INVITE toward the callee. A call to an emergency service is never barred.

Before either 603, the caller hears an announcement: `deploy/compose/mmtel.yaml` names the MRFC (`mrfc:`) and the announcement (`announcement: notice`). The AS asks the MRFC for it with an INVITE whose Request-URI names the prompt (RFC 4240 3), without SDP, and gives the caller a reliable 183 whose SDP points at the MRFP, with `P-Early-Media: sendonly` (TS 24.628 4.2.4 and Annex G). Once the caller has acknowledged the 183 with PRACK, the AS sends the MRFC its ACK, and the MRFP plays. When the announcement ends, the MRFC sends BYE, and the AS sends the 603 with `Reason: SIP;cause=603`. The incoming case in the capture:

```text
#11978 51.547 UE 1   -> P-CSCF C1 INVITE tel:+12025550102
#12124 51.580 S-CSCF -> MMTel  C2 INVITE   P-Served-User: <UE 2>;sescase=term
#12140 51.584 MMTel  -> MRFC   M  INVITE   sip:annc@mrfc...;play=/provisioned/notice, no SDP
#12161 51.588 MRFC   -> MMTel  M  200 OK   SDP offer: the MRFP 172.28.0.86, PCMU, sendonly
#12164 51.589 MMTel  -> S-CSCF C2 183      SDP answer from the MRFP, P-Early-Media: sendonly
#12187 51.591 P-CSCF -> UE 1   C1 183      then PRACK and 200 dialog by dialog
#12232 51.603 MMTel  -> MRFC   M  ACK      SDP answer: the caller's media, recvonly
#12248 51.626 MRFP   -> agw       RTP      PCMU, the announcement, on to UE 1
#12378 52.709 MRFC   -> MMTel  M  BYE      the announcement has ended
#12383 52.710 MMTel  -> S-CSCF C2 603      Reason: SIP;cause=603;text="Decline"
#12427 52.714 P-CSCF -> UE 1   C1 603      Reason: SIP;cause=603;text="Decline"
```

A caller whose INVITE does not take reliable provisional responses, or requires preconditions, gets the 603 at once, without the announcement. The guide to the MRF follows the MRFC and the MRFP in more detail.

BOIC and BOIC-exHC compare the country code of the called number with that of the home network, `1` in `deploy/compose/mmtel.yaml` (TS 24.611 4.9.1.4).

## CW and HOLD

IR.92 asks for call waiting done by the UE, not by the network (2.3.4). The callee's UE tells that a call waits with a 180 that carries `Alert-Info: <urn:alert:service:call-waiting>` (TS 24.615 4.5.5.3.2, RFC 7462), and the callee's AS passes it on, or takes the Alert-Info out when the operator's setting says that the caller does not learn of it, as in hss.yaml (TS 24.615 4.5.5.2.3). `ue serve` takes a second call while it has one: it rings with that Alert-Info and does not answer by itself, and `POST /answer` puts the first call on hold and takes the waiting one (TS 24.615 4.5.5.3.3); a third call gets 486. `scripts/e2e-mrf.sh` shows CW in the capture, and [the MRF guide](mrf.md) follows it; `TestCommunicationWaiting` in `internal/mmtel/waiting_test.go` and `TestServeTwoCalls` in `internal/ue/phone_test.go` check it in one process. HOLD is a re-INVITE with `a=sendonly` that the AS passes on to the other leg as it came (TS 24.610 4.5.2.1); the in-process test `TestHoldThroughTheIMS` shows it.

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `sip.P-Served-User` | The INVITEs that the S-CSCF sends to the AS. `sip.P-Served-User contains "orig-cdiv"` finds the passes after a diversion, `contains "regstate=unreg"` those for a user who is not registered. |
| `sip.Route contains "odi="` | Requests to an AS, with the S-CSCF's original dialog identifier. |
| `sip.P-Charging-Vector contains "icid-value=..."` | Every dialog of one call, inside the network. The P-CSCF removes P-Charging-Vector from what it sends to a UE. |
| `sip.History-Info` | Diverted INVITEs, and the 181s and 200s that report a diversion. |
| `sip.r-uri contains "cause="` | Diverted INVITEs, with the cause of RFC 4458. |
| `sip.Status-Code == 181` | The notifications of a diversion. |
| `sip.Status-Code == 199` | Early dialogs that ended. |
| `sip.Privacy` | Requests and responses that withhold an identity. |
| `sip.from.addr == "sip:anonymous@anonymous.invalid"` | Anonymous callers. |
| `sip.Method == "CANCEL" && sip.Reason` | CANCELs with a Reason, such as that of CFNR. |
| `http && ip.addr == 172.28.0.75` | Ut. `http.www_authenticate` finds the challenges, `http.request.method == "PUT"` the changes, `http.response.line matches "^etag:"` the versions. |
| `diameter.cmd.code in {306, 307} && ip.addr == 172.28.0.75` | The UDRs and PURs of the MMTel AS. `diameter.Service-Indication == "MMTEL-Services"` finds the reads of the service data, `== "Ut-Digest-Credential"` those of the Ut passwords. |
| `diameter.Experimental-Result-Code == 2003` | LIAs for a user who has services when not registered. |

These filters were checked with tshark 4.2 against a capture of `scripts/e2e-mmtel.sh`.

## Where the code is

| Package or file | What it holds |
| --- | --- |
| `internal/mmtel/mmtel.go`, `call.go` | The AS as a routeing B2BUA: its UA, the two legs of each call, the relay of responses and of requests within the dialogs. |
| `internal/mmtel/sessioncase.go` | The order of the services in each session case. |
| `internal/mmtel/identity.go` | OIP, OIR, TIP, TIR. |
| `internal/mmtel/diversion.go` | CDIV: CFU, CFB, CFNR, CFNRc, CFNL, History-Info, the 181, the no reply timer. |
| `internal/mmtel/barring.go` | OCB, ICB, ACR. |
| `internal/mmtel/waiting.go` | CW. |
| `internal/mmtel/services.go`, `store.go` | The Sh read for each call, and the call records in the Store. |
| `internal/simservs` | The simservs and MMTelServices documents, their rules (RFC 4745), the XCAP node selectors and how the rules are evaluated. |
| `internal/xcap` | The XCAP server of Ut, HTTP Digest, and the client of the UE. |
| `internal/ue/xcap.go`, `cmd/ue/xcap.go` | `ue xcap` and the UE's supplementary services. |
| `internal/scscf/services.go` | P-Served-User, the ODI and the criteria of ORIGINATING_CDIV. |
| `internal/scscf/session.go` | The services of an unregistered callee, and the identity that the S-CSCF keeps from the callee's UE. |
| `internal/pcscf/identity.go` | The identity that the P-CSCF asserts in the callee's responses, and keeps from the caller's UE. |
| `internal/hss`, `deploy/compose/hss.yaml` | The service data and Ut passwords as repository data, and the four criteria. |
| `internal/mmtel/network_test.go` | Every service through the whole IMS in one process. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| AS, TS 24.604 4.5.2.6.2.3 | The last History-Info entry is taken to be the served user's when it has a cause-param, rather than compared with the served user. | In this network a diverted request reaches the diverted-to user's AS only that way. The comparison would need the user's alias set. |
| AS, TS 24.604 4.5.2.6.4 | The 181 has no announcement (TS 24.628). | The call goes on to the target right after the 181. An announcement first would need the caller's media moved to the MRFP and then to the target, in an early dialog that the diversion may end with a 199. |
| AS, TS 29.364 8.1 | The AS does not subscribe to changes of the service data; it reads them again for each call. | Neither Sh-Subs-Notif nor the notifications are needed then. |
| AS, TS 24.611 4.9.1.4 NOTE 3 | The served user is taken to be at home, so BIC-Roam never holds. | Whether the user roams comes from its registration, which the AS does not get. |
| AS, RFC 3261 13.2.1, 14.1 | When the INVITE forks beyond the AS and the dialog that answers is not the one whose session description the caller got, the AS does not offer the caller the session of the dialog that answered in an UPDATE or a re-INVITE, so the caller's media goes on toward the first dialog. After an INVITE without an offer, the offer of a dialog that the caller did not answer gets no answer, and that UAS ends its dialog. | No node of this project forks an INVITE; the tests fork it to show the case. |
| XCAP, RFC 4825 8 | The xcap-caps document is read without authentication. | Sh finds a credential by a public identity, which only a document in a user's directory gives. The document holds nothing about any user. |
| XCAP, RFC 7616 3.4 and 5.5 | A nonce count that was seen before is not detected, so a replayed request is accepted until its nonce expires. | Keeping the counts needs a store that every instance shares. The If-Match on every PUT and DELETE turns a replay into 412 once the document has changed. |
| XCAP, TS 24.623 5.2.2 and 5.2.3.1, GSMA IR.92 2.2.2 | No GBA (TS 33.222, TS 24.109) and no authentication proxy. | GBA needs a BSF and Zh, too much to learn from here, and IR.92 only recommends it. |
| XCAP, TS 24.623 5.2.1.2 and 5.2.3.3 | No subscription to document changes (xcap-diff, RFC 5875). | The UE reads the document again before each change instead. |
| XCAP, TS 24.623 5.3.1.3 and 5.3.2.5 | No password check or change with POST, and no password control of a service; a service under password control cannot be changed. | IR.92 2.3.9 lets the network leave 5.3.2.5 out, and Digest already authenticates the user. |
| XCAP, TS 24.623 5.2.3.1 and RFC 4825 8 | Ut is plain HTTP, without TLS. | The project does not encrypt signalling by default, so that captures can be read, as for SIP and Diameter. |
| simservs, TS 24.604 4.9.1.3, TS 24.611 4.9.1.4 | The conditions cp:sphere, cp:validity, ss:presence-status, ocp:external-list and ocp:other-identity are refused. | IR.92 2.3.8 and 2.3.9 do not ask for them, and there is no presence or list server. |
| simservs, TS 24.623 5.2.3.4 and 6.2 e | No service capability fragments (the *-serv-cap elements). | A change is checked against the services and conditions that the server takes. |
| simservs, TS 24.611 4.9.1.4 | The country of a number is its country code, so the NANP countries under 1 count as one; a number without a country code is in the user's country. | Number plans per country are out of scope. |
| simservs, TS 24.611 4.9.1.4 | The home network of BOIC-exHC is taken to be the home country. | There are no number ranges per network. |
| simservs, RFC 3261 19.1.4, RFC 3966 4 | Numbers are compared without their visual separators and parameters, SIP URIs by scheme, user and host. | The identities that an IMS network asserts need no more. |
| simservs, RFC 4745 7.1.3 | Domains are compared without percent-decoding or the ToASCII step. | IMS domains are ASCII. |
| simservs, TS 29.364 7.2.1 | Only OIP, OIR, TIP, TIR, CDIV, CW, HOLD and CB are modelled; the other services of the document are kept but not read. | They are what IR.92 2.3 asks the network for. |
| UE, GSMA IR.92 2.6 | The User-Agent of Ut is a plain product token, not built as GSMA PRD RCC.07 C.4.1 says. | This project has not read RCC.07. |
| UE, GSMA IR.92 2.3.2 | `ue xcap` takes the `-impu` of the command line as the XUI, not the default identity of the last registration. `ue serve` takes the default identity. | `ue xcap` needs no registration. For a softphone the two are the same; a VoLTE UE registers a temporary identity, so its services are set through `ue serve`. |
| S-CSCF, TS 24.229 5.4.3.3 step 12 | The privacy of History-Info entries (RFC 7044 10.1.2) is not applied. | A diverted call carries the entries of the diverting users as the AS wrote them. |
| S-CSCF, TS 24.229 5.4.3.1 and 5.4.3.2 | Only a registered user is served. A request that an AS sends on behalf of a user, who is named in P-Served-User, gets 403 when that user is not registered, and the criteria of ORIGINATING_UNREGISTERED are not evaluated. | No AS of this network starts a request on behalf of a user, so no request would meet those criteria. |
| I-CSCF, TS 24.229 5.3.2.1, 5.3.2.1A and 5.3.2.2 | No "orig" routing for an AS that acts on behalf of a user, and no other S-CSCF when the chosen one does not answer. | No service sends on behalf of a user, and this network has no redundant S-CSCF to choose. |
| HSS, TS 29.228 6.1.4.1 steps 2, 2a and 3 | No public service identities and no IMS restoration; the Originating-Request AVP is not read. | No I-CSCF of this project routes an originating request. |

## Clean up

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
