# Follow a short message through the IMS

[日本語](../ja/sms.md)

This guide sends one short message (SMS) from UE 1 to UE 2 over the IMS and walks through every message in the capture: how the IP-SM-GW learns that a user can take short messages, how the S-CSCF hands a short message to it, how the message reaches the other UE and how each side learns what became of it. It ends by reading the body of one MESSAGE octet by octet.

The guide follows these editions: TS 24.341 V19.0.0 and TS 23.204 V19.1.0 (SMS over IP), TS 24.011 V20.0.0 (relay layer), TS 23.040 V20.0.0 (transfer layer), TS 23.038 V20.0.0 (alphabets), TS 24.229 V20.1.0, TS 29.228 V20.0.0 (filter criteria), TS 29.328 V20.0.0 and TS 29.329 V19.1.0 (Sh). Clause numbers below refer to those editions.

## Before you start

- You have read [Follow a UE registering with the IMS](registration.md) and [Follow a voice call through the IMS](call.md). A short message reuses what they show: the registration, the Path and Service-Route, and the way the I-CSCF finds the S-CSCF of a user.
- The abbreviations are explained in the [glossary](../../glossary.md).

## Where SMS over IP fits

A short message can travel to a phone in several ways. Over the IMS, which this guide shows, it rides in the body of a SIP MESSAGE (TS 24.341). Over NAS, the signaling between the phone and the core network, it goes through the MSC with SMS over SGs, through the MME itself with SMS in MME, or through the SMSF in 5GS; TS 24.011 describes the procedures between the phone and each of these nodes. This implementation carries short messages over the IMS only. In every one of these ways, the short message inside has the same two layers: the relay layer (RP) of TS 24.011 and the transfer layer (TP) of TS 23.040. SIP only replaces what carries them.

## The nodes

| Node | Address | What it does for SMS |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The sender, `+1-202-555-0101`. TS 24.341 calls it the SM-over-IP sender. |
| UE 2 | 172.28.0.101 | The recipient, `+1-202-555-0102`, the SM-over-IP receiver. |
| P-CSCF | 172.28.0.40 | Passes each MESSAGE between a UE and its S-CSCF, as for a call. |
| S-CSCF | 172.28.0.30 | Serves both UEs. Its initial filter criteria (iFCs) send registrations and short messages to the IP-SM-GW. |
| I-CSCF | 172.28.0.20 | Entry point of the home network for the MESSAGEs that the IP-SM-GW sends. It asks the HSS where the user is (LIR). |
| IP-SM-GW | 172.28.0.70 | The application server (AS) of SMS over IP (TS 24.341 5.3.3). It turns a MESSAGE into a short message for the SC and back. |
| SC (stub) | inside the IP-SM-GW | The service centre that takes each short message and delivers it. Here it is a stub inside the IP-SM-GW process, behind a Go interface. |
| HSS | 172.28.0.10 | Holds the iFCs (Cx) and answers the IP-SM-GW over Sh. |
| DNS | 172.28.0.2 | Gives the address of `ipsmgw.ims.mnc001.mcc001.3gppnetwork.org`, the AS named in the iFCs. |

Two more numbers matter. The SC address, `+12025550100`, is what each UE puts in the short message as its SC; the UE simulator has it as `-sc`. The IP-SM-GW has the number `+12025550199`, which it registers in the HSS for each user it serves. All numbers are in the 555-0100 to 555-0199 range that the North American Numbering Plan keeps for fiction.

### Why an application server, and what the SC is

The CSCFs only route SIP. Short messages need a node that speaks both SIP and the language of the SMS network, where the SC stores and forwards each message (TS 23.040). That node is the IP-SM-GW. In a real network it reaches the SC through an SMS-IWMSC or SMS-GMSC over MAP or Diameter (TS 23.204 5.2.4). This project has no SMS network, so the SC is a stub inside the IP-SM-GW: the IP-SM-GW calls it through the `ServiceCentre` interface in `internal/ipsmgw/servicecentre.go`, and a real SGd client could take its place there. The stub stores nothing: a short message that it cannot deliver at once is dropped, and the status report says so.

The IP-SM-GW talks to the HSS over Sh (TS 29.328, TS 29.329). It writes which IP-SM-GW serves a user with a Profile-Update-Request (PUR), and the SC stub reads the public identities of a number with a User-Data-Request (UDR). To know when to write, the IP-SM-GW subscribes to the registration state of each user, as the P-CSCF does.

## How the S-CSCF picks the IP-SM-GW

### The filter criteria

The S-CSCF knows nothing of SMS. The HSS tells it, in the user profile that comes with the SAA at registration, which requests go to which AS. Those rules are the initial filter criteria (TS 29.228 B.2.2, schema in Annex E). The HSS stub gives every subscription the two criteria in `deploy/compose/hss.yaml`:

```yaml
initial_filter_criteria:
  - priority: 0
    trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: REGISTER}]}
    application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED, service_info: "{msisdn}"}
  - priority: 1
    trigger_point:
      condition_type_cnf: true
      spt:
        - {group: [0], method: MESSAGE}
        - {group: [1], sip_header: {header: ^Content-Type$, content: '^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$'}}
        - {group: [2], session_case: ORIGINATING_REGISTERED}
    application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060}
```

In the SAA they arrive in the User-Data AVP as XML of the schema of TS 29.228 Annex E. The second criterion looks like this:

```xml
<InitialFilterCriteria>
  <Priority>1</Priority>
  <TriggerPoint>
    <ConditionTypeCNF>true</ConditionTypeCNF>
    <SPT><ConditionNegated>false</ConditionNegated><Group>0</Group><Method>MESSAGE</Method></SPT>
    <SPT><ConditionNegated>false</ConditionNegated><Group>1</Group>
      <SIPHeader><Header>^Content-Type$</Header><Content>^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$</Content></SIPHeader></SPT>
    <SPT><ConditionNegated>false</ConditionNegated><Group>2</Group><SessionCase>0</SessionCase></SPT>
  </TriggerPoint>
  <ApplicationServer>
    <ServerName>sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060</ServerName>
    <DefaultHandling>0</DefaultHandling>
  </ApplicationServer>
</InitialFilterCriteria>
```

Element by element:

| Element | Value here | Meaning |
| --- | --- | --- |
| `Priority` | 0, then 1 | The S-CSCF tries the criteria in this order, lowest first. |
| `TriggerPoint` | | The condition. With no trigger point a criterion matches every request. |
| `ConditionTypeCNF` | `true` | The service point triggers (SPTs) are in conjunctive normal form: the SPTs with the same `Group` are ORed, and the groups are ANDed (TS 29.228 B.2.3). |
| `SPT` with `Method` | `REGISTER`; `MESSAGE` | Matches the SIP method. |
| `SPT` with `SIPHeader` | `Content-Type` is `application/vnd.3gpp.sms` | Matches a header by regular expressions on its name and its content. A MESSAGE with another body, such as a text chat, does not go to the IP-SM-GW. The name is matched without case, because SIP header names have none (RFC 3261 7.3.1), so `content-type` matches too. The content is matched as written, so the pattern spells each letter in both cases (a media type has no case, RFC 2045 5.1) and allows parameters after `;`. |
| `SPT` with `SessionCase` | `0`, ORIGINATING_REGISTERED | Matches only a request that a registered user sends. |
| `ConditionNegated` | `false` | Each SPT can be negated. The criterion shown here negates none; the one for a MESSAGE to a user negates its last SPT, as described below. |
| `ApplicationServer` / `ServerName` | `sip:ipsmgw...:5060` | The AS to send the request to. The S-CSCF finds its address in DNS. |
| `DefaultHandling` | `0`, SESSION_CONTINUED | What the S-CSCF does when the AS does not answer: go on without it (SESSION_CONTINUED) or give up (SESSION_TERMINATED). |
| `ServiceInfo` | the user's MSISDN, such as `12025550101` | Text that the S-CSCF passes on to the AS in the third-party REGISTER. The HSS stub puts the MSISDN of each user in place of `{msisdn}`. |

So the first criterion reads "every REGISTER goes to the IP-SM-GW, with the MSISDN", and the second "a MESSAGE that a registered user sends with an SMS body goes to the IP-SM-GW".

A third criterion covers a MESSAGE to a user (a terminating SessionCase), but only one that the IP-SM-GW did not send itself: its last condition is negated and asks that P-Asserted-Identity is not the URI of the IP-SM-GW. The IP-SM-GW sends its own MESSAGEs to users, and without that condition each of them would come back to the IP-SM-GW (TS 23.218 5.2.3 NOTE 5). What the criterion does catch is a short message that arrives from another network, which the IP-SM-GW of the user then delivers ([Connect two IMS networks](interconnect.md)).

### The third-party REGISTER

When a registration succeeds, the S-CSCF sends a REGISTER of its own to each AS whose criterion matches the REGISTER (TS 24.229 5.4.1.7). In this third-party REGISTER the Request-URI is the AS, From and Contact are the S-CSCF, To is the user, and Expires is what the UE got in its 200 OK, or 0 for a deregistration. Because the criterion has a ServiceInfo, the body is `application/3gpp-ims+xml` with the text in `<service-info>` (TS 24.229 5.4.1.7A):

```xml
<?xml version="1.0" encoding="UTF-8"?>
<ims-3gpp version="1"><service-info>12025550101</service-info></ims-3gpp>
```

If the AS does not answer, or answers 408 or 5xx, the default handling applies: SESSION_CONTINUED leaves the registration as it is, SESSION_TERMINATED deregisters the user (TS 24.229 5.4.1.7).

### The original dialog identifier

When a MESSAGE matches the second criterion, the S-CSCF does not send it to the Request-URI. It puts two entries on top of Route: the AS, then its own URI with an original dialog identifier (ODI) (TS 24.229 5.4.3.2 step 5 a, 5.4.3.4):

```text
Route: <sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060;lr>
Route: <sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060;lr;odi=W46DU74CLAQFEFRO3XYECCE5TS>
```

An AS that acts as a proxy sends the request back along that Route. The S-CSCF recognizes the ODI and goes on with the next criterion instead of starting again (5.4.3.2 step 3). The IP-SM-GW is not a proxy: it ends the MESSAGE as a UA (TS 24.229 5.7.2), so the request never comes back. The S-CSCF adds the ODI anyway, since it cannot know what the AS will do. The ODI is a random token; what the S-CSCF needs to go on is kept in its `Store` for a while, so another S-CSCF node with the same store could take the request back.

The S-CSCF also adds P-Served-User (RFC 5502). It names the user whom the AS serves, with the session case and whether that user is registered (TS 24.229 5.4.3.2 step 5 b):

```text
P-Served-User: <sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org>;sescase=orig;regstate=reg
```

An AS that serves both callers and callees, such as the MMTel AS ([Supplementary services with the MMTel AS](mmtel.md)), tells the cases apart by it (5.7.1.3A). A request to a user's terminating services has `sescase=term`. The S-CSCF removes P-Served-User before a request leaves for its destination or the UE (5.4.3.2 step 14A, 5.4.3.3 step 13A). One that a UE put in never reaches it: the P-CSCF removes it at the edge of the trust domain (TS 24.229 4.4.9).

If the IP-SM-GW does not answer the MESSAGE within Timer F (32 seconds), or answers 408 or 5xx, the default handling of the criterion applies (TS 24.229 5.4.3.2): with SESSION_CONTINUED the S-CSCF sends the MESSAGE on towards its Request-URI, the SC's tel URI. That number has no ENUM record, so the S-CSCF answers 404 (step 10), as for a call to such a number.

## Send a short message

`scripts/e2e-sms.sh` does everything in this section, checks each message that this guide describes in the capture, and exits with an error if one is missing. It leaves the capture in `captures/` and a list of its frames in `e2e-out/sms-ladder.txt`. The texts and numbers below are the ones the script uses, so its capture shows what this guide shows. To run the steps by hand:

1. Start the system and wait for the Diameter connections, as in the registration guide:

   ```sh
   docker compose up -d --build
   ```

2. In one terminal, register UE 2 and keep it registered. Every command of the UE simulator takes short messages while it is registered:

   ```sh
   docker compose run --rm ue2 register \
     --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     --impu sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     --password test-password-2
   ```

3. In another terminal, send a short message from UE 1 and ask for a status report. The flags come before the number and the text:

   ```sh
   docker compose run --rm ue sms --status-report +12025550102 "Hello UE-B, this is UE-A."
   ```

   UE 1 registers, sends the message, logs `"msg":"submit report"` with `"state":"accepted"`, waits for the status report, logs `"msg":"status report"` with `"status":0`, and deregisters. UE 2 logs `"msg":"sms received"` with `"from":"+12025550101"` and `"text":"Hello UE-B, this is UE-A."`.

4. Stop UE 2 with Ctrl+C. It deregisters.

The text may be up to 160 characters of the GSM 7 bit alphabet, or up to 70 characters when it needs UCS2, as Japanese does. The script sends one in UCS2 the other way, from UE 2 to UE 1: with `docker compose run --rm ue register` in one terminal, `docker compose run --rm ue2 sms --impi ... --impu ... --password test-password-2 +12025550101 "こんにちは、UE-A さん"` in the other. A longer text is refused before anything is sent.

To try the failures, which need no other UE:

- **Unknown number.** `docker compose run --rm ue sms +12025550198 "Nobody has this number."`. No subscriber has the number. The submit report is an RP-ERROR, and `ue sms` exits with `the SC refused the short message: RP-Cause 1, TP-FCS 0xc3`.
- **Recipient not registered.** `docker compose run --rm ue sms --status-report +12025550103 "Are you there?"`. Subscriber 3 exists but never registers. The SC accepts the message, the delivery fails with 480, and UE 1 logs `"msg":"status report"` with `"status":98`, TP-ST 0x62.

The same can be done from the dashboard: see "Drive the UE simulators" in [the dashboard guide](dashboard.md). The UE cards send short messages and list what each UE sent and received, and the details of each MESSAGE show the decoded short message.

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code in {302, 306, 307}
```

It shows the REGISTERs (with the third-party ones), the MESSAGEs and their responses, the SUBSCRIBEs and NOTIFYs of the IP-SM-GW, and the LIR, UDR and PUR with their answers. Wireshark decodes the body of each MESSAGE by itself: the Info column ends with the RP message, such as `(RP) RP-DATA (MS to Network)`, and the packet details have a "GSM A-I/F RP" tree with a "GSM SMS TPDU" tree inside. **Statistics > Flow Graph** with **Limit to display filter** draws the ladder.

## Registration: the IP-SM-GW learns about the user

[The registration guide](registration.md) walks through this flow in steps 15 to 18; here is what it means for short messages.

```text
 UE 2      P-CSCF     I-CSCF     S-CSCF    IP-SM-GW     HSS
  |          |          |          |          |          |
  |  REGISTER, 401, REGISTER,      |          |          |
  |  200 OK (R1)                   |          |          |
  |<==============================>|          |          |
  |          |          |          | REGISTER (R2)       |
  |          |          |          |--------->|          |
  |          |          |          |  200 OK  |          |
  |          |          |          |<---------|          |
  |          |          |  SUBSCRIBE (R3)     |          |
  |          |          |<--------------------|          |
  |          |          |  LIR, LIA           |          |
  |          |          |<------------------------------>|
  |          |          |--------->|          |          |
  |          |          |  200 OK  |          |          |
  |          |          |<---------|          |          |
  |          |          |-------------------->|          |
  |          |          |          | NOTIFY (R4)         |
  |          |          |          |--------->|          |
  |          |          |          |  200 OK  |          |
  |          |          |          |<---------|          |
  |          |          |          |          | PUR (R5) |
  |          |          |          |          |--------->|
  |          |          |          |          |  PUA     |
  |          |          |          |          |<---------|
```

### R1. The UE registers with the feature tag of SMS over IP

The registration is the one of the registration guide, with one addition: the Contact of the REGISTER carries the feature tag `+g.3gpp.smsip` (TS 24.341 5.3.2.2, RFC 3840), which says that this contact takes short messages over IP. GSMA IR.92 asks it of every VoLTE phone. The S-CSCF keeps the parameters of the contact, lists them in the reginfo of its NOTIFYs (R4), and uses them later to pick the contact for a short message (step 9 below).

### R2. The S-CSCF sends a third-party REGISTER

Right after the 200 OK, the S-CSCF sends the third-party REGISTER described above to the IP-SM-GW (TS 24.229 5.4.1.7, 5.4.1.7A), with the user's MSISDN in `<service-info>`. The IP-SM-GW answers 200 OK with the binding in Contact (TS 24.341 5.3.3.2; RFC 3261 10.3, as table B.3-2 of TS 24.341 shows). It keeps the MSISDN with the subscription of R3, to name the user in the PUR of R5.

Like every response of the IP-SM-GW, the 200 OK repeats the P-Charging-Vector of the request and adds a term-ioi (TS 24.229 5.7.2).

### R3. The IP-SM-GW subscribes to the reg event: SUBSCRIBE

The third-party REGISTER does not say whether the UE registered `+g.3gpp.smsip`. So the IP-SM-GW subscribes to the reg event package of the user (TS 24.341 5.3.3.2, TS 24.229 5.7.1.1), with Event `reg`, Expires 3761 (the 3600 seconds of the registration and the 161 seconds that RFC 3680 4.4 puts between the two defaults), and its own URI in P-Asserted-Identity, by which the S-CSCF accepts it as an AS of the user's criteria (TS 24.229 5.4.2.1.1). As an AS that starts a request on its own, it puts a P-Charging-Vector with a new icid-value and an orig-ioi on the SUBSCRIBE (TS 24.229 5.7.3).

The SUBSCRIBE goes to the I-CSCF, the entry point of the home network, which finds the S-CSCF with an LIR, as for any request of the IP-SM-GW (see step 6 below). The subscription is kept in the IP-SM-GW's `Store`, so whichever IP-SM-GW node the NOTIFY reaches can take it.

### R4. The S-CSCF reports the state: NOTIFY

The S-CSCF sends a NOTIFY straight to the IP-SM-GW at once. The 200 OK to the SUBSCRIBE goes back through the I-CSCF, so the capture may show the NOTIFY before or after it: the NOTIFY, not the 200 OK, creates the dialog of a subscription (RFC 6665 4.4.1). The reginfo lists each contact of the user, and the feature tags of a contact as `<unknown-param>` elements (TS 24.229 5.4.2.1.2):

```xml
<registration aor="sip:001010000000002@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
  <contact id="LJDTDE6YNM" state="active" event="registered" expires="3600" ...>
    <uri>sip:001010000000002@172.28.0.101:5060</uri>
    <unknown-param name="+g.3gpp.smsip"></unknown-param>
  </contact>
</registration>
```

The IP-SM-GW answers 200 OK, and looks for an active contact of the user with `+g.3gpp.smsip` (TS 24.341 5.3.3.2).

### R5. The IP-SM-GW registers itself in the HSS: PUR and PUA

There is such a contact, so the IP-SM-GW tells the HSS that it now delivers the user's short messages. TS 24.341 5.3.3.2 and TS 23.204 6.1 do this with a MAP update (TS 29.002); this project uses the Sh update instead (TS 29.328 6.1.2). The PUR (command 307) names the user by the MSISDN of R2, uses Data-Reference 24, SMSRegistrationInfo (TS 29.328 7.6.16), and carries the IP-SM-GW's number, `12025550199`, in `<IP-SM-GW-Number>` of the Sh-Data. The HSS answers with a PUA with DIAMETER_SUCCESS (2001).

The IP-SM-GW sends a PUR only when a NOTIFY changes whether the user has such a contact. A UE that registers without `+g.3gpp.smsip` gets no PUR, so the HSS does not list the IP-SM-GW for it.

### Re-registration and deregistration

Each re-registration brings another third-party REGISTER. The IP-SM-GW then refreshes its subscription with a SUBSCRIBE within the dialog, straight to the S-CSCF, so it needs no timer of its own. A NOTIFY that shows no change sends no PUR.

When the UE deregisters, the S-CSCF ends the subscription with a NOTIFY whose Subscription-State is `terminated;reason=noresource` (TS 24.229 5.4.2.1.2). The IP-SM-GW answers 200 OK and sends a PUR, again with the MSISDN, whose `<IP-SM-GW-Number>` is empty: this takes the IP-SM-GW out of the HSS. The S-CSCF also sends a third-party REGISTER with Expires 0 and no body, which the IP-SM-GW answers with 200 OK and nothing more. In the capture of the script, the NOTIFY comes before that REGISTER.

## Sending and delivering: "Hello UE-B, this is UE-A."

```text
 UE 1     P-CSCF    S-CSCF    I-CSCF   IP-SM-GW+SC    HSS      UE 2
  | MESSAGE |         |         |          |           |         |
  |-------->| (1)     |         |          |           |         |
  |         |-------->| (2)     |          |           |         |
  |         |         |------------------->| (3)       |         |
  |  202    |  202    |  202    |          |           |         |
  |<--------|<--------|<-------------------| (4)       |         |
  |         |         |         |          |  UDR (5)  |         |
  |         |         |         |          |---------->|         |
  |         |         |         |          |  UDA      |         |
  |         |         |         |          |<----------|         |
  |         |         |         | MESSAGE submit report (6)      |
  |         |         |         |<---------|           |         |
  |         |         |         |  LIR, LIA            |         |
  |         |         |         |<-------------------->|         |
  |         |         |<--------|          |           |         |
  |<--------|<--------|         |          |           |         |
  |  200 (7)|         |         |          |           |         |
  |-------->|-------->|-------->|--------->|           |         |
  |         |         |         | MESSAGE SMS-DELIVER (8)        |
  |         |         |         |<---------|           |         |
  |         |         |         |  LIR, LIA            |         |
  |         |         |         |<-------------------->|         |
  |         |         |<--------| (9)      |           |         |
  |         |<--------|         |          |           |         |
  |         |---------------------------------------------------->|
  |         |  200 (10)         |          |           |         |
  |         |<----------------------------------------------------|
  |         |-------->|-------->|--------->|           |         |
  |         | MESSAGE delivery report (11) |           |         |
  |         |<----------------------------------------------------|
  |         |-------->| (12)    |          |           |         |
  |         |         |------------------->|           |         |
  |         |  202    |  202    |          |           |         |
  |         |         |<-------------------|           |         |
  |         |<--------|         |          |           |         |
  |         |---------------------------------------------------->|
```

The I-CSCF does not record-route, and a MESSAGE starts no dialog, so every MESSAGE and its response go hop by hop. The MESSAGEs that the IP-SM-GW sends are new requests, each with its own Call-ID; the ladder of the dashboard draws one Call-ID at a time.

The ladder draws one thing after another, but some of them run at the same time. The IP-SM-GW hands the message to the SC as soon as it has sent the 202, so the UDR of step 5 may come before the 202 in the capture. Once the SC has taken the message, the submit report (step 6) and the SMS-DELIVER (step 8) leave together, and their LIRs and responses are interleaved.

UE 1's `ue sms` registers just before it sends, so the capture also has the flow of the section above for UE 1, which may still be going on when the MESSAGE of step 1 is sent.

### 1. UE 1 submits the short message

TS 24.341 5.3.1.2. Look at:

- **Request-URI and To** `tel:+12025550100`, the public service identity (PSI) of the SC. The UE does not send a short message to the recipient; it sends it to its SC, as a phone does over the radio.
- **From**, UE 1's public identity.
- **Content-Type: application/vnd.3gpp.sms**, and a body of 47 octets: an RP-DATA that holds an SMS-SUBMIT with the recipient's number and the text. "Read the body" below takes it apart.
- **Route**, the P-CSCF and UE 1's Service-Route, as for an INVITE.

UE 1 keeps the Call-ID: the submit report will name it.

### 2. The P-CSCF passes it on

A MESSAGE outside a dialog is a standalone transaction (TS 24.229 5.2.6.3.7). As for an INVITE, the P-CSCF removes its own Route, adds P-Asserted-Identity (step 4) and sends the MESSAGE along the Service-Route. Unlike for an INVITE, it does not record-route, since no dialog follows, and no media relay is involved.

### 3. The S-CSCF sends it to the IP-SM-GW

The S-CSCF serves the sender (the top Route carries `orig`). It adds UE 1's tel URI as a second P-Asserted-Identity (TS 24.229 5.4.3.2 step 9 a): the IP-SM-GW needs a number, not a SIP URI, to give the SC as the sender (TS 24.341 5.3.1.2 NOTE 2). The second criterion matches (step 5), so the S-CSCF puts the IP-SM-GW and its own URI with the ODI on top of Route, adds P-Served-User with UE 1's SIP URI, `sescase=orig` and `regstate=reg`, as shown above, and sends the MESSAGE to the IP-SM-GW.

### 4. The IP-SM-GW accepts: 202

The IP-SM-GW answers **202 Accepted** (TS 24.341 5.3.3.4.1), with the P-Charging-Vector of the request and a term-ioi (TS 24.229 5.7.2). A UA that relays a MESSAGE answers 202 rather than 200, because it has not delivered it yet (RFC 3428 7). The 202 tells UE 1 nothing about the short message itself; the submit report does.

The IP-SM-GW then checks the RP-DATA: that it decodes, that the RP-DA is the SC's number, and that P-Asserted-Identity has a tel URI. If one of these fails, the submit report is an RP-ERROR (see "When it fails").

### 5. The SC looks the recipient up: UDR and UDA

The IP-SM-GW hands the SMS-SUBMIT and the sender's number to the SC stub. The SC finds the user who has the TP-DA number `+12025550102` with a UDR (command 306), Data-Reference 10, IMSPublicIdentity (TS 29.328 7.6.2), naming the user by MSISDN. The UDA lists `sip:001010000000002@...` and `tel:+12025550102`. In a real network the SC side would ask for routeing information instead (TS 23.204 6.4 step 3); the Sh read takes its place here.

### 6. The submit report goes back to UE 1

The SC has taken the message, so the IP-SM-GW sends UE 1 a new MESSAGE with the submit report (TS 24.341 5.3.3.4.3):

- **Request-URI** UE 1's SIP URI, the first P-Asserted-Identity of the MESSAGE in step 1.
- **P-Asserted-Identity** the IP-SM-GW's SIP URI.
- **Accept-Contact: \*;+g.3gpp.smsip;require;explicit** and **Request-Disposition: fork**. Only a contact that registered the feature tag may get it (see step 9).
- **In-Reply-To** the Call-ID of step 1, so that UE 1 knows which short message this is about (TS 24.341 5.3.1.2).
- **P-Charging-Vector** with a new icid-value and an orig-ioi. The IP-SM-GW starts each of its own MESSAGEs this way, as an AS that acts as an originating UA (TS 24.229 5.7.3).
- **The body**, an RP-ACK with the same RP-MR as the RP-DATA, and inside it an SMS-SUBMIT-REPORT with the SC's time stamp (TP-SCTS).

The IP-SM-GW does not use the Route of the S-CSCF. It sends its own requests to the entry point of the home network, the I-CSCF, which it finds in DNS (TS 24.229 5.7.3). The I-CSCF asks the HSS with an LIR, as for a call, and forwards the MESSAGE to UE 1's S-CSCF, which serves it as a terminating request and sends it along UE 1's Path.

### 7. UE 1 answers 200

UE 1 matches In-Reply-To to its short message, answers **200** (TS 24.341 5.3.1.2), and marks the message accepted.

### 8. The SC delivers the message: SMS-DELIVER

The SC turns the SMS-SUBMIT into an SMS-DELIVER: the sender's number as TP-OA, the time it took the message as TP-SCTS, the same text, and TP-SRI set because UE 1 asked for a status report. The IP-SM-GW sends it in a MESSAGE (TS 24.341 5.3.3.4.2):

- **Request-URI** `tel:+12025550102`, the tel URI that the UDA gave (TS 23.204 6.4 step 6).
- **Accept-Contact: \*;+g.3gpp.smsip;require;explicit** and **Request-Disposition: no-fork**.
- **P-Asserted-Identity** the IP-SM-GW's SIP URI. UE 2 sends its delivery report there.
- **The body**, an RP-DATA from the network with the SC's address as RP-OA, and the SMS-DELIVER inside.

It also goes to the I-CSCF, which finds UE 2's S-CSCF with an LIR.

### 9. The S-CSCF picks the contact by caller preferences

The S-CSCF serves UE 2 now (TS 24.229 5.4.3.3). The terminating criterion does not match, since the P-Asserted-Identity is that of the IP-SM-GW (step 4). It matches the Accept-Contact of the request against the parameters of the contact that UE 2 registered (step 9, RFC 3841 7.2.4): `require` drops a contact that does not match, and `explicit` asks that the contact listed `+g.3gpp.smsip` itself. UE 2's contact has it, so it stays. A UE that registered only for voice would not, and the S-CSCF would answer 480 (`TestShortMessageToAUEWithoutSMSOverIP` in `internal/ue/sms_ims_test.go`). `no-fork` asks for one contact only (step 10 a, RFC 3841 9.1). Then, as for a call, the S-CSCF adds P-Called-Party-ID and sends the MESSAGE along UE 2's Path.

### 10. UE 2 answers 200

UE 2 answers **200** (TS 24.341 5.3.2.3). This says only that the MESSAGE arrived. Whether UE 2 took the short message is in the delivery report.

### 11. UE 2 sends the delivery report

UE 2 sends a new MESSAGE (TS 24.341 5.3.2.4):

- **Request-URI and To** the IP-SM-GW, from the P-Asserted-Identity of step 8.
- **In-Reply-To** the Call-ID of step 8.
- **The body**, an RP-ACK from the MS with the RP-MR of the RP-DATA, and an SMS-DELIVER-REPORT inside. A UE that cannot take the message, for example because its memory is full, sends an RP-ERROR instead.

### 12. The delivery report reaches the IP-SM-GW through UE 2's criteria

The delivery report is an originating request of UE 2, a MESSAGE with an SMS body, so UE 2's own second criterion sends it to the IP-SM-GW, with an ODI, exactly as in step 3. The IP-SM-GW answers 202 Accepted (TS 24.341 5.3.3.4.1), finds the delivery by the Call-ID in In-Reply-To, and tells the SC that the message was delivered.

TS 23.204 6.5 step 3 asks that the delivery report reach the IP-SM-GW that sent the short message. Here DNS may give another IP-SM-GW node, so every IP-SM-GW node keeps the deliveries in one shared `Store`, and whichever node gets the report finishes the delivery (`TestDeliveryReportThroughAnotherIPSMGW` in `internal/ipsmgw/network_test.go`). Only the timer that waits for the report, 64 seconds, runs in the node that sent the message.

## The status report

UE 1 asked for one (TP-SRR in the SMS-SUBMIT), so once the delivery report is in, the SC sends an SMS-STATUS-REPORT to UE 1:

```text
 UE 1     P-CSCF    S-CSCF    I-CSCF   IP-SM-GW+SC    HSS
  |         |         |         |          | UDR (13)  |
  |         |         |         |          |---------->|
  |         |         |         |          |  UDA      |
  |         |         |         |          |<----------|
  |         |         |         | MESSAGE status report (14)
  |         |         |         |<---------|           |
  |         |         |         |  LIR, LIA            |
  |         |         |         |<-------------------->|
  |         |         |<--------|          |           |
  |<--------|<--------|         |          |           |
  |  200    |         |         |          |           |
  |-------->|-------->|-------->|--------->|           |
  | MESSAGE delivery report (15)|          |           |
  |-------->|-------->|------------------->|           |
  |  202    |  202    |  202    |          |           |
  |<--------|<--------|<-------------------|           |
```

13. As soon as the delivery report of step 12 is in, the SC looks up the sender's number `+12025550101` with a UDR, as in step 5. The capture may show this UDR before the 202 of step 12.
14. The IP-SM-GW sends the status report like a short message (TS 24.341 5.3.3.4.4): to UE 1's tel URI, with Accept-Contact and `Request-Disposition: no-fork`, and an RP-DATA with an SMS-STATUS-REPORT inside. It repeats the TP-MR of the SMS-SUBMIT, so that UE 1 knows which message it reports on, and TP-ST 0x00 says that the recipient received it (TS 23.040 9.2.3.15).
15. UE 1 answers 200 and acknowledges the status report with a delivery report, as UE 2 did in step 11 (TS 24.341 5.3.1.3).

## When it fails

The IP-SM-GW and the SC decide what goes back to the sender. The cases you can try are:

| Case | What happens | What UE 1 gets |
| --- | --- | --- |
| The number has no subscriber (`+12025550198`) | The UDA of step 5 has DIAMETER_ERROR_USER_UNKNOWN (5001). | A submit report that is an RP-ERROR with RP-Cause 1 (unassigned number) and an SMS-SUBMIT-REPORT with TP-FCS 0xC3 (invalid SME address) (TS 24.011 Table 8.4, TS 23.040 9.2.3.22). No delivery is tried. |
| The recipient is not registered (`+12025550103`) | The SC accepts the message, so the submit report is an RP-ACK. The recipient has a service for the unregistered state, the MMTel AS's iFC for TERMINATING_UNREGISTERED, so the I-CSCF's LIR gets DIAMETER_UNREGISTERED_SERVICE (2003), and the I-CSCF picks an S-CSCF (TS 29.228 6.1.4.1). That S-CSCF takes the user profile with a SAR of UNREGISTERED_USER, finds no iFC for the MESSAGE of step 8, and answers it with 480 (TS 24.229 5.4.3.3). The IP-SM-GW reports it to the SC as an absent subscriber (TS 24.341 5.3.3.4.2, TS 29.311 6.1.4.4.1). | A status report with TP-ST 0x62: a temporary error after which the SC makes no more attempts. The stub does not store the message to try again. |
| The recipient's memory is full | UE 2 answers the RP-DATA with an RP-ERROR, RP-Cause 22 (memory capacity exceeded), with TP-FCS 0xD3 in the report. | A status report with TP-ST 0x62. |
| The RP-DATA does not decode | The IP-SM-GW answers 202, then refuses it. | An RP-ERROR with RP-Cause 96 (invalid mandatory information) (TS 24.341 5.3.3.4.1, TS 24.011 9.3.4). |

The UE simulator never runs out of memory, so the memory-full case is only in the tests: `TestShortMessagesThatFail` in `internal/ipsmgw/network_test.go` runs it, and the undecodable RP-DATA, through the whole IMS.

## Read the body

The body of a MESSAGE is binary: TS 24.341 has the UE send the octets as they are. Here is the body of the MESSAGE of step 1, as the UE simulator built it with `internal/sms`. The dashboard shows the same octets under "Body (hex)" in the details of the MESSAGE.

```text
00 00 00 07 91 21 20 55 05 01 f0 23 21 00 0b 91 21 20 55 05 01 f2 00 00 19 c8 32 9b fd 06 55 8b 2d 21 0b
44 47 a7 e7 a0 f4 1c 54 2d b6 82 2e
```

### The relay layer (RP)

TS 24.011 7.3.1.2, Table 7.5, and 8.2:

| Octets | Field | Value |
| --- | --- | --- |
| `00` | RP-MTI (8.2.2), the low 3 bits | 0: RP-DATA from the MS to the network. 1 is RP-DATA from the network, 2 and 3 RP-ACK, 4 and 5 RP-ERROR, 6 RP-SMMA. |
| `00` | RP-MR (8.2.3) | 0. The RP-ACK or RP-ERROR that answers carries the same value. |
| `00` | RP-OA length (8.2.5.1) | 0: from the MS the originator address is empty. |
| `07` | RP-DA length (8.2.5.2) | 7 **octets** follow. |
| `91` | type of address | Extension bit 1, type of number 001 (international), numbering plan 0001 (E.164). |
| `21 20 55 05 01 f0` | RP-DA digits | Two digits per octet, the low half first, `f` filling the last: 1 2 0 2 5 5 5 0 1 0 0, the SC `+12025550100`. |
| `23` | RP-User-Data length (8.2.5.3) | 35 octets follow: the TPDU. |

In an RP-ACK or RP-ERROR the RP-User-Data is optional, so it also carries its element identifier `41` in front of the length.

### The transfer layer (TP): SMS-SUBMIT

TS 23.040 9.2.2.2 and 9.2.3:

| Octets | Field | Value |
| --- | --- | --- |
| `21` | first octet | `0010 0001`: TP-MTI 01 (bits 1-0, SMS-SUBMIT, 9.2.3.1), TP-RD 0 (bit 2), TP-VPF 00 (bits 4-3, no validity period, 9.2.3.3), TP-SRR 1 (bit 5, status report requested, 9.2.3.5), TP-UDHI 0 (bit 6, no header), TP-RP 0 (bit 7). |
| `00` | TP-MR (9.2.3.6) | 0. The status report repeats it. |
| `0b` | TP-DA length (9.1.2.5) | 11 **digits** (semi-octets). |
| `91` | type of address | International, E.164. |
| `21 20 55 05 01 f2` | TP-DA digits | `+12025550102`, the recipient. |
| `00` | TP-PID (9.2.3.9) | 0, a plain short message. |
| `00` | TP-DCS (9.2.3.10) | 0, the GSM 7 bit default alphabet (TS 23.038 4). |
| `19` | TP-UDL (9.2.3.16) | 25 **septets**, the 25 characters. |
| `c8 32 9b fd 06 55 ... b6 82 2e` | TP-UD | The text, packed into 22 octets. |

The two addresses hold the same 11-digit number in the same way, yet their lengths differ: RP counts the octets after the length, type of address included (7), and TP counts the digits (11). This is an easy place to go wrong when you build a short message by hand.

TP-MTI depends on the direction too: 01 is SMS-SUBMIT from the MS and SMS-SUBMIT-REPORT from the network, 00 is SMS-DELIVER from the network and SMS-DELIVER-REPORT from the MS, 10 is SMS-STATUS-REPORT (9.2.3.1). The RP message around the TPDU tells which.

### GSM 7 bit packing and UCS2

The GSM 7 bit default alphabet has 128 characters of 7 bits each (TS 23.038 6.2.1). For "Hello UE-B, this is UE-A." the codes are those of ASCII, but not every character is: `@`, for example, is 0x00. The septets are packed into octets from the lowest bit up (TS 23.038 6.1.2.1.1):

```text
H = 100 1000, e = 110 0101, l = 110 1100, ...
octet 1: lowest bit of 'e' (1), then 'H' (1001000)          = 1100 1000 = c8
octet 2: lowest 2 bits of 'l' (00), then rest of 'e' (110010) = 0011 0010 = 32
```

25 septets take 175 bits, so 22 octets. That is why 160 characters fit in the 140 octets of TP-UD.

A text with a character outside the alphabet, such as Japanese, is sent in UCS2 instead: TP-DCS is 0x08, each character takes two octets, most significant first, and TP-UDL counts octets (9.2.3.16). The message that UE 2 sends in the script, `こんにちは、UE-A さん`, has TP-DCS `08`, TP-UDL `1a` (26 octets) and TP-UD `30 53 30 93 30 6b 30 61 30 6f 30 01 00 55 00 45 00 2d 00 41 00 20 30 55 30 93`. 70 characters fit.

### Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `gsm_a.rp.msg_type == 0x00` | RP-DATA from the UE: the short messages the UEs submit. Use `0x01` for those to the UEs, `0x02`/`0x03` for RP-ACK, `0x04`/`0x05` for RP-ERROR. |
| `gsm_a.rp.cause` | RP-ERRORs, with their RP-Cause. |
| `gsm_sms.tp-mti == 2` | SMS-STATUS-REPORTs. `gsm_sms.tp-mti == 1` finds both SMS-SUBMIT and SMS-SUBMIT-REPORT, as explained above. |
| `gsm_sms.tp-da == "12025550102"` | Short messages to that number. `gsm_sms.tp-oa` is the sender of an SMS-DELIVER, `gsm_sms.tp-ra` the recipient in a status report. |
| `gsm_sms.sms_text contains "Hello"` | The text, decoded for both GSM 7 bit and UCS2. |
| `gsm_sms.tp-srr == 1` | SMS-SUBMITs that ask for a status report. |
| `gsm_sms.tp-fcs` | Reports with a TP-Failure-Cause. |
| `sip.In-Reply-To` | Submit reports and delivery reports. |
| `sip.Accept-Contact` | The MESSAGEs that the IP-SM-GW sends. |

These filters were checked with tshark 4.2 against MESSAGEs sent by the UE simulators and the IP-SM-GW of this project.

## Where the code is

| Package or file | What it holds |
| --- | --- |
| `internal/sms` | The body: `rp.go` (TS 24.011), `tp.go` (TS 23.040), `text.go` (TS 23.038), `address.go` (both address fields). |
| `internal/ifc` | The initial filter criteria and how a request matches them (TS 29.228 Annexes B, E and F). |
| `internal/cx/userdata.go`, `internal/hss/profile.go` | The user profile with the criteria, and how the HSS stub fills in `{msisdn}`. |
| `internal/sh`, `internal/hss/sh.go` | The Sh messages and Sh-Data, the client of the IP-SM-GW, and the HSS side. |
| `internal/scscf/services.go` | How the S-CSCF evaluates the criteria, adds the ODI and applies the default handling. |
| `internal/scscf/thirdparty.go` | The third-party REGISTER. |
| `internal/scscf/callerprefs.go` | Accept-Contact matching (RFC 3841). |
| `internal/ipsmgw` | The IP-SM-GW: `ipsmgw.go` (the third-party REGISTER), `regevent.go` (the reg event subscription and the PUR), `message.go` (MESSAGEs from UEs), `deliver.go` (MESSAGEs to UEs), `servicecentre.go` (the boundary to the SC), `store.go` (the deliveries in progress). |
| `internal/sc` | The SC stub. |
| `internal/ue/sms.go` | The UE as SM-over-IP sender and receiver. |
| `internal/dashboard/sms.go` | How the dashboard decodes a short message for the details view. |
| `internal/ipsmgw/network_test.go`, `internal/ue/sms_ims_test.go` | The whole flow in one process, with the cases above. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all. Some of them also appear in the registration and call guides.

| Where | What is left out | Why |
| --- | --- | --- |
| IP-SM-GW, TS 24.229 5.7.3 | The requests of the IP-SM-GW, such as its SUBSCRIBE and its MESSAGEs, carry no Route to the entry point. | The entry point is the name of the home domain. The I-CSCF removes only a Route that names its own host (5.3.2.1), so a Route with the domain would stay in the request. |
| IP-SM-GW, RFC 6665 4.1.2.4 | No Timer N. A SUBSCRIBE that no NOTIFY follows is replaced by a new one at the next third-party REGISTER. | The S-CSCF answers every SUBSCRIBE that it accepts with a NOTIFY at once. |
| IP-SM-GW, TS 24.341 5.3.3.2 | Without an MSISDN in service-info, the PUR names the user by the public identity, not by the IMSI of the UE's REGISTER. | The IMSI would come from the UE's REGISTER in a message/sip body, which the S-CSCF does not send. Sh accepts a public identity (TS 29.328 Table 7.6.1). |
| IP-SM-GW, TS 24.341 5.3.3.4.1 | A delivery report without In-Reply-To is dropped. | TS 24.341 5.3.2.4 has the UE send it. Finding the delivery by RP-MR alone would need a second key in the Store. |
| IP-SM-GW, TS 24.341 5.3.3.4.1 steps d and e, TS 23.204 6.6 | An RP-SMMA (memory available again) is not passed to the HSS; the IP-SM-GW answers it with RP-ACK at once. | The SC stub stores no messages, so there is nothing to alert. |
| SC, TS 23.204 6.3 and 6.4, TS 23.040 3.2.1 and 3.2.6 | No SGd, S6c or MAP, no choice between the IMS and the CS or PS domain, and no store-and-retry: a message that cannot be delivered at once is dropped, and the status report says so. No message waiting data; the validity period is not read. | The SC is a stub inside the IP-SM-GW. |
| SC, TS 23.204 6.4 step 3 | The routeing information query (SRI for SM, or S6c) is replaced by an Sh read of the public identities that have the number. | The stub knows the user only as a user of the IMS. |
| SMS, TS 23.040 9.1.2.5 | An alphanumeric address (type of number 101), such as a sender name, is refused. | The SC stub uses E.164 numbers only. |
| SMS, TS 23.040 9.2.2.4 | SMS-COMMAND is refused. | It asks the SC about a message it holds, and the stub holds none. |
| SMS, TS 23.040 9.2.3.12.2 and 9.2.3.12.3 | Only "no validity period" and the relative format are read and written; the absolute and enhanced formats are refused. | The SC stub stores nothing, so it has no use for the period. |
| SMS, TS 23.040 9.2.3.24.1 | No concatenated short messages: a text longer than one short message is refused. | This project sends one short message at a time. |
| SMS, TS 23.040 9.2.3.24 and TS 23.042 | A User-Data-Header, such as a national language table (TS 23.038 6.2.1.2), and compressed text are not decoded. | Nothing in this project sends them. |
| UE, TS 24.341 5.3.2.5 | The UE never refuses a short message for want of memory, so it never sends RP-SMMA. | The simulator keeps every short message it gets. |
| UE, RFC 3428 7 | Only short messages are taken; a MESSAGE with any other body, text/plain included, gets 415 (Unsupported Media Type). | The simulator is an SMS over IP UE, not an instant messaging client. |
| UE, TS 23.040 9.2.3.12 and 9.2.3.24 | The SMS-SUBMIT asks for no validity period and holds one short message, without a User-Data-Header. | The SC stub has no use for the period. |
| UE, TS 24.011 9.3.2 | A report whose RP-MR is not that of the RP-DATA is discarded, without the RP-ERROR with cause 81 that a phone would send. | Over IP there is no connection to send it on. |
| Sh, TS 29.328 6.1.3 and 6.1.4 | No subscriptions to notifications (SNR/SNA, PNR/PNA, commands 308 and 309). | The IP-SM-GW only registers itself and looks users up, and the MMTel AS reads its repository data for each initial request and keeps no copy. TS 29.364 8.1 recommends that such an AS subscribes. |
| Sh, TS 29.328 7.1.3 | No External-Identifier. | It is for machine-type communication, which this project does not serve. |
| Sh, TS 29.328 Annex D | Sh-Data has only PublicIdentifiers (IMSPublicIdentity, MSISDN), RepositoryData, and SCSCFName and SMSRegistrationInfo of Sh-IMS-Data. | The rest waits until a role needs it. |
| Sh, TS 29.328 7.6.16 | SMSRegistrationInfo has no SCAddress and no indications of SBI or S6c support. | The SC is inside the IP-SM-GW, which speaks neither SBI nor S6c. |
| HSS, TS 29.329 7.1 | No Supported-Features negotiation: the HSS acts as for an AS that supports none of the optional features, so a request names one Data-Reference, and for repository data one Service-Indication or one RepositoryData, and an answer leaves User-Data out when the HSS has none of the data. | The IP-SM-GW of this project announces no optional feature. |
| HSS, TS 29.328 6.2 | No permissions per AS: every peer that connected may use Sh. | The IP-SM-GW is the only AS of this project. |
| HSS, TS 29.328 6.1.1.1 and 6.1.2.1 step 2a | The private identity in User-Name is not read or checked. | The IP-SM-GW does not send it. |
| HSS, TS 29.328 7.6.9 | The MSISDN is the first tel URI among the public identities, not data of its own. | The configuration then states each number once, and the user is found by MSISDN through the tel URI. |
| HSS, TS 29.328 7.6.2 | IMPLICIT_IDENTITIES and ALIAS_IDENTITIES return the same set as ALL_IDENTITIES, and are not refused when the user is named by MSISDN. | Each test subscription is one implicit registration set with one service profile. |
| HSS, TS 29.228 B.2.2 | Every subscription gets the same criteria; only the MSISDN in ServiceInfo differs. | The iFCs of SMS and of the MMTel AS are the same for every user. |
| User profile, TS 29.228 Annex E | One ServiceProfile, with no optional element but the criteria, and without the schema's XML namespace. | The test subscriptions need nothing more. |
| iFC, TS 29.228 Annex E and B.2.3 | No SessionDescription SPT (refused), and ProfilePartIndicator, RegistrationType, IncludeRegisterRequest and IncludeRegisterResponse are skipped; a REGISTER SPT matches every REGISTER. | The iFCs of SMS and of the MMTel AS need none of them. RegistrationType is optional for the S-CSCF. |
| S-CSCF, TS 24.229 5.4.0 | The S-CSCF looks the AS up in DNS for every request, rather than keeping the address that the first third-party REGISTER went to. | Each AS role keeps its state in a Store that all its nodes share, so any node can take the next request. |
| S-CSCF, TS 24.229 5.4.1.7 item c | The To of the third-party REGISTER is always the default public identity. Item i has nothing to copy, and items j, l and m are for features this project does not have. | The operator may choose the identity. The HSS stub sends no charging addresses. |
| S-CSCF, RFC 3261 10.2 | Each third-party REGISTER has a new Call-ID. | One Call-ID per user and AS would be more state in the Store; a registrar accepts a new one (RFC 3261 10.3 step 7). |
| S-CSCF, TS 24.229 5.4.1.7 | The term-ioi of the answer to a third-party REGISTER is not stored. | The S-CSCF does no charging. |
| S-CSCF, TS 24.229 5.4.3.2 and 5.4.3.3 | A 100 (Trying) from the AS does not count as the 1xx that turns the default handling off. No orig-ioi or term-ioi. | Proxy keeps a 100 to the hop it came over (RFC 3261 16.7 step 5). The S-CSCF does no charging. |
| S-CSCF, TS 24.229 5.4.3.1 and 5.4.3.2 | Only a registered user is served. A request that an AS sends on behalf of a user, who is named in P-Served-User, gets 403 when that user is not registered, and the criteria of ORIGINATING_UNREGISTERED are not evaluated. | No AS of this network starts a request on behalf of a user, so no request would meet those criteria. |
| S-CSCF, RFC 3841 7.2.4 and 9.1, TS 24.229 5.4.3.3 step 10 a | The target set is the one contact kept for a private identity, so matching only decides whether it stays. Qa and q-values are not computed, and Request-Disposition is not acted on. | With one contact, every directive reaches the same contact. |
| S-CSCF, RFC 3841 8 | A negated value (`!x`) and a numeric one (`#>=4`) in a feature tag are compared as plain tokens. | SMS over IP and the tags of TS 24.229 7.9 use neither. |

## Clean up

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
