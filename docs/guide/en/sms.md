# Follow a short message through the IMS

[日本語](../ja/sms.md)

This guide sends one short message (SMS) from UE 1 to UE 2 over the IMS and walks through every message in the capture: how the IP-SM-GW learns that a user can take short messages, how the S-CSCF hands a short message to it, how the message reaches the other UE and how each side learns what became of it. It ends by reading the body of one MESSAGE octet by octet.

The guide follows these editions: TS 24.341 V19.0.0 and TS 23.204 V19.1.0 (SMS over IP), TS 24.011 V20.0.0 (relay layer), TS 23.040 V20.0.0 (transfer layer), TS 23.038 V20.0.0 (alphabets), TS 24.229 V20.1.0, TS 29.228 V20.0.0 (filter criteria), TS 29.328 V20.0.0 and TS 29.329 V19.1.0 (Sh), TS 29.338 V19.3.0 (S6c). Clause numbers below refer to those editions.

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
| SC | 172.28.0.71 | The Service Centre: it takes each short message, holds it until it is delivered or its validity period ends, and delivers it (TS 23.040 6.1). The IP-SM-GW hands it the short messages of the UEs over SGd, and it brings back the ones for them over the same interface. |
| HSS | 172.28.0.10 | Holds the iFCs (Cx). It answers the IP-SM-GW over Sh, as it does every application server, and the SC over S6c: which node serves a recipient, which Service Centres hold short messages for a user, and when that user can take them again. |
| DNS | 172.28.0.2 | Gives the address of `ipsmgw.ims.mnc001.mcc001.3gppnetwork.org`, the AS named in the iFCs. |

Two more numbers matter. The SC address, `+12025550100`, is what each UE puts in the short message as its SC; the UE simulator has it as `-sc`. The IP-SM-GW has the number `+12025550199`, which it registers in the HSS for each user it serves. All numbers are in the 555-0100 to 555-0199 range that the North American Numbering Plan keeps for fiction.

### Why an application server, and what the SC is

The CSCFs only route SIP. Short messages need a node that speaks both SIP and the language of the SMS network, where the SC stores and forwards each message (TS 23.040). That node is the IP-SM-GW. It reaches the SC through an SMS-IWMSC for a short message from a UE, and is reached through an SMS-GMSC for one to a UE, over MAP or Diameter (TS 23.204 5.2.4); here it is Diameter, on the SGd interface of TS 29.338. TS 23.040 4.1 lets one node be the SMS-SC, the SMS-IWMSC and the SMS-GMSC at once, and the `sc` node is all three.

The IP-SM-GW calls the SC through the `ServiceCentre` interface in `internal/ipsmgw/servicecentre.go`. Behind that interface sits either the SGd client, which this network uses, or the stub of `internal/sc`, which an IP-SM-GW whose configuration names no Service Centre among its Diameter peers uses instead. The stub holds nothing: it passes each short message on at once, and one that cannot be delivered then is dropped. [Follow a short message between two IMS networks](interconnect.md) shows that deployment.

The IP-SM-GW talks to the HSS over Sh (TS 29.328, TS 29.329). It writes which IP-SM-GW serves a user with a Profile-Update-Request (PUR), and reads the user's IMSI with a User-Data-Request (UDR). To know when to write, the IP-SM-GW subscribes to the registration state of each user, as the P-CSCF does.

The SC talks to the HSS over S6c (TS 29.338 5). It asks which node serves a recipient with a Send-Routing-Info-for-SM (SRR), reports a delivery that failed with a Report-SM-Delivery-Status (RDR), so that the HSS keeps its address in the message waiting data (MWD) of the user, and is told with an Alert-Service-Centre (ALR) that the user can take short messages again. Over SGd it takes a short message from the IP-SM-GW with an MO-Forward-Short-Message (OFR) and brings one back with an MT-Forward-Short-Message (TFR).

## How the S-CSCF picks the IP-SM-GW

### The filter criteria

The S-CSCF knows nothing of SMS. The HSS tells it, in the user profile that comes with the SAA at registration, which requests go to which AS. Those rules are the initial filter criteria (TS 29.228 B.2.2, schema in Annex E). A named list of them is a service profile, and each subscription gets one. Every subscription of the HSS stub gets the profile `default`, which `deploy/compose/hss.yaml` defines. Two of its criteria are the ones of SMS over IP:

```yaml
default_service_profile: default
service_profiles:
  default:
    - priority: 0
      feature: sms
      trigger_point: {condition_type_cnf: true, spt: [{group: [0], method: REGISTER}]}
      application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_CONTINUED, service_info: "{msisdn}"}
    - priority: 1
      feature: sms
      trigger_point:
        condition_type_cnf: true
        spt:
          - {group: [0], method: MESSAGE}
          - {group: [1], sip_header: {header: ^Content-Type$, content: '^[Aa][Pp][Pp][Ll][Ii][Cc][Aa][Tt][Ii][Oo][Nn]/[Vv][Nn][Dd]\.3[Gg][Pp][Pp]\.[Ss][Mm][Ss]([[:space:]]*;.*)?$'}}
          - {group: [2], session_case: ORIGINATING_REGISTERED}
      application_server: {server_name: sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org:5060, default_handling: SESSION_TERMINATED}
```

The profile name is the operator's own. Cx carries the criteria of the profile and not its name, so the S-CSCF never learns it.

`feature` is the operator's own word too, for what a criterion turns on. The features of a profile are those words without repetition, and the maintenance API of the HSS node lists them beside the name of each profile, so an operator can see which profile turns SMS over IP on without reading every criterion. Annex E has no element for it either, so it stays in the HSS with the name.

Which profile a subscription gets is read off the subscription. One that names a profile in its own `service_profile` gets that one. One that names none gets the profile of `test_user_service_profile` if it is marked `test_user`, and the one of `default_service_profile` otherwise. So a feature goes to one user by naming a profile on that subscription, to the test users by naming the profile of the feature in `test_user_service_profile`, and to everyone by naming it in `default_service_profile`. The HSS reads the name from the subscription it is already answering for, so none of this costs more as the network grows.

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
    <DefaultHandling>1</DefaultHandling>
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
| `DefaultHandling` | `0`, SESSION_CONTINUED, for the REGISTER; `1`, SESSION_TERMINATED, for the MESSAGE | What the S-CSCF does when the AS does not answer: go on without it (SESSION_CONTINUED) or give up (SESSION_TERMINATED). No specification says which a criterion has; it is the operator's choice. A registration can stand without the IP-SM-GW, but no other node can take a short message that a user sends. |
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
P-Served-User: <sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org>;sescase=orig;regstate=reg
```

An AS that serves both callers and callees, such as the MMTel AS ([Supplementary services with the MMTel AS](mmtel.md)), tells the cases apart by it (5.7.1.3A). A request to a user's terminating services has `sescase=term`. The S-CSCF removes P-Served-User before a request leaves for its destination or the UE (5.4.3.2 step 14A, 5.4.3.3 step 13A). One that a UE put in never reaches it: the P-CSCF removes it at the edge of the trust domain (TS 24.229 4.4.9).

If the IP-SM-GW does not answer the MESSAGE within Timer F (32 seconds), or answers 408 or 5xx, the default handling of the criterion applies (TS 24.229 5.4.3.2): with SESSION_TERMINATED the S-CSCF ends the MESSAGE there, with no criterion of lower priority, and the UE gets a failure response. A 503 of the IP-SM-GW reaches the UE as 500, since a proxy does not pass a 503 on (RFC 3261 16.7 step 6). With SESSION_CONTINUED the S-CSCF would send the MESSAGE on towards its Request-URI, the SC's tel URI. That number has no ENUM record, so the S-CSCF would answer 404 (step 10), as for a call to such a number: a short message that the network could not take would end as if the SC did not exist.

## Send a short message

`scripts/e2e-sms.sh` does everything in this section and in [Hold, alert and resend](#when-the-ue-cannot-take-it-hold-alert-and-resend), checks each message that this guide describes in the capture, and exits with an error if one is missing. It drives `phone1` and `phone2` of the `phones` profile over their control APIs, since the states in which a delivery fails are set there; the steps below do the plain delivery with the one-shot UE simulators instead. It leaves the capture in `captures/` and a list of its frames in `e2e-out/sms-ladder.txt`. The texts and numbers below are the ones the script uses, so its capture shows what this guide shows. To run the steps by hand:

1. Start the system and wait for the Diameter connections, as in the registration guide:

   ```sh
   docker compose up -d --build
   ```

2. In one terminal, register UE 2 and keep it registered. Every command of the UE simulator takes short messages while it is registered:

   ```sh
   docker compose run --rm ue2 register \
     -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
     -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
     -password test-password-2
   ```

3. In another terminal, send a short message from UE 1 and ask for a status report. The flags come before the number and the text:

   ```sh
   docker compose run --rm ue sms -status-report +12025550102 "Hello UE-B, this is UE-A."
   ```

   UE 1 registers, sends the message, logs `"msg":"submit report"` with `"state":"accepted"`, waits for the status report, logs `"msg":"status report"` with `"status":0`, and deregisters. UE 2 logs `"msg":"sms received"` with `"from":"+12025550101"` and `"text":"Hello UE-B, this is UE-A."`.

4. Stop UE 2 with Ctrl+C. It deregisters.

The text may be up to 160 characters of the GSM 7 bit alphabet, or up to 70 characters when it needs UCS2, as Japanese does. The script sends one in UCS2 the other way, from UE 2 to UE 1: with `docker compose run --rm ue register` in one terminal, `docker compose run --rm ue2 sms -impi ... -impu ... -password test-password-2 +12025550101 "こんにちは、UE-A さん"` in the other. A longer text is refused before anything is sent.

To try the failures, which need no other UE:

- **Unknown number.** `docker compose run --rm ue sms +12025550198 "Nobody has this number."`. No subscriber has the number. The submit report is an RP-ERROR, and `ue sms` exits with `the SC refused the short message: RP-Cause 1, TP-FCS 0xc3`.
- **Recipient not registered.** `docker compose run --rm ue sms +12025550103 "Are you there?"`. Subscriber 3 exists but never registers. The SC takes the message, so the submit report is an RP-ACK, and then holds it: no IP-SM-GW serves subscriber 3, so the HSS keeps the SC's address in the message waiting data and the short message waits for the alert that a registration would set off.

The same can be done from the Lab: see "Drive the UE simulators" in [the Lab guide](lab.md). The UE cards send short messages and list the latest 50 short messages each UE sent and the latest 50 it received, and the details of each MESSAGE show the decoded short message.

### Put the UE in the states where a delivery fails

A short message to a user who is registered usually arrives. The two states in which it does not are a UE with no memory left for it and a UE that takes no short messages at all. `ue serve` sets both over its HTTP control API, which "Drive a UE over HTTP" in [the call guide](call.md) introduces:

| Request | What the UE does |
| --- | --- |
| `PUT /sms-memory` with `{"full": true}` | Has no memory for an incoming short message. It answers each one it would store with an RP-ERROR, RP-Cause 22 (memory capacity exceeded) and TP-FCS `0xd3`, and keeps nothing of it. A status report is not stored, so it still gets through. The first refusal leaves the UE owing the network a notification. |
| `PUT /sms-memory` with `{"full": false}` | Has memory again. Where it owes a notification, it sends one at once: an RP-SMMA in a MESSAGE to the IP-SM-GW that brought the short message it could not store, or to the PSI of the SC when no IP-SM-GW has written to it (TS 24.341 5.3.2.5). The IP-SM-GW passes the notification to the HSS, which clears the memory capacity exceeded flag of the user and alerts the Service Centres that hold short messages for them, and answers the UE afterwards (TS 23.204 6.6, TS 23.040 3.2.6 item 2f). The RP-ACK for that notification ends it, and the UE owes none. |
| `POST /sms-memory/notify` | Sends one more notification, whatever it owes, with an RP-MR of its own (TS 24.011 6.3.3.1.3). That is how a phone repeats a notification that went unanswered, and it shows the network one it has answered already. |
| `PUT /sms-availability` with `{"available": false}` | Takes no short messages: it answers each MESSAGE that brings one with 480 (Temporarily Unavailable, RFC 3261 21.4.18), the response for a user who is reached but in a state that precludes the request, and sends no delivery report. The IP-SM-GW reports such a delivery to the SC as an absent subscriber (TS 24.341 5.3.3.4.2, TS 29.311 6.1.4.4.1), as it does for subscriber 3 above, who never registers at all. `{"available": true}` takes short messages again. |

Each of them answers with the state it leaves behind, such as `{"available": true, "memory_full": false, "memory_capacity_exceeded_notification": true}`. The last field is the memory capacity exceeded notification flag that a phone keeps on its (U)SIM (TS 23.040 10.3): the UE sets it when it refuses a short message for want of memory, and clears it when the notification is answered. For a UE whose control API is on port 8091, as in the call guide:

```sh
curl -X PUT localhost:8091/sms-memory -d '{"full": true}'
```

## Open the capture

Open the newest file in `captures/` in Wireshark with this display filter:

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70) || diameter.cmd.code in {302, 306, 307, 8388645, 8388646, 8388647, 8388648, 8388649}
```

It shows the REGISTERs (with the third-party ones), the MESSAGEs and their responses, the SUBSCRIBEs and NOTIFYs of the IP-SM-GW, the LIR, UDR and PUR with their answers, and the five commands of SGd and S6c with theirs. Wireshark decodes the body of each MESSAGE by itself: the Info column ends with the RP message, such as `(RP) RP-DATA (MS to Network)`, and the packet details have a `GSM A-I/F RP` tree with a `GSM SMS TPDU` tree inside. **Statistics > Flow Graph** with **Limit to display filter** draws the ladder.

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
  |          |          |          |          | UDR (R5) |
  |          |          |          |          |--------->|
  |          |          |          |          |  UDA     |
  |          |          |          |          |<---------|
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
<registration aor="sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
  <contact id="LJDTDE6YNM" state="active" event="registered" expires="3600" ...>
    <uri>sip:ab6af11f-a43e-860e-9064-207615ae1f12@172.28.0.101:5060</uri>
    <unknown-param name="+g.3gpp.smsip"></unknown-param>
  </contact>
</registration>
```

The IP-SM-GW answers 200 OK, and looks for an active contact of the user with `+g.3gpp.smsip` (TS 24.341 5.3.3.2).

### R5. The IP-SM-GW registers itself in the HSS: UDR, UDA, PUR and PUA

There is such a contact, so the IP-SM-GW tells the HSS that it now delivers the user's short messages.

First it reads the user's IMSI with a UDR (command 306) of Data-Reference 32, IMSI (TS 29.328 7.6.24), and keeps it with the subscription. A short message that reaches the IP-SM-GW over SGd names the recipient by IMSI, so this is how it will find the user again. The IP-SM-GW reads the IMSI, and stores it with the news that the user takes short messages, before the registration. The registration is what makes the HSS name this IP-SM-GW to a Service Centre, and the HSS alerts the waiting Service Centres as soon as it has it, so a short message may arrive before the IP-SM-GW has the answer. A registration that fails takes that news back, and the next NOTIFY tries again.

Then comes the registration itself. TS 24.341 5.3.3.2 and TS 23.204 6.1 do it with a MAP update (TS 29.002); this project uses the Sh update instead (TS 29.328 6.1.2). The PUR (command 307) names the user by the MSISDN of R2, uses Data-Reference 24, SMSRegistrationInfo (TS 29.328 7.6.16), and carries the IP-SM-GW's number, `12025550199`, in `<IP-SM-GW-Number>` of the Sh-Data. Below the number, three `<Extension>` elements deep where the schema puts it, `<IP-SM-GW-S6cSupportIndication>1</IP-SM-GW-S6cSupportIndication>` says that S6c reaches this IP-SM-GW. The HSS therefore keeps its Diameter identity as well as its number, and can name it to an SMS-GMSC as the node that serves the user (TS 29.328 6.1.2.1). The HSS answers with a PUA with DIAMETER_SUCCESS (2001).

The IP-SM-GW sends a PUR only when a NOTIFY changes whether the user has such a contact. A UE that registers without `+g.3gpp.smsip` gets no PUR, so the HSS does not list the IP-SM-GW for it.

### Re-registration and deregistration

Each re-registration brings another third-party REGISTER. The IP-SM-GW then refreshes its subscription with a SUBSCRIBE within the dialog, straight to the S-CSCF, so it needs no timer of its own. A NOTIFY that shows no change sends no PUR. The re-registration also shows that the UE is there, which matters when a short message did not reach it before: see [UE 2 only registers again](#ue-2-only-registers-again).

When the UE deregisters, the S-CSCF ends the subscription with a NOTIFY whose Subscription-State is `terminated;reason=noresource` (TS 24.229 5.4.2.1.2). The IP-SM-GW answers 200 OK and sends a PUR, again with the MSISDN, whose `<IP-SM-GW-Number>` is empty: this takes the IP-SM-GW out of the HSS. The S-CSCF also sends a third-party REGISTER with Expires 0 and no body, which the IP-SM-GW answers with 200 OK and nothing more. In the capture of the script, the NOTIFY comes before that REGISTER.

## Sending and delivering: "Hello UE-B, this is UE-A."

```text
 UE 1      P-CSCF    S-CSCF    I-CSCF    IP-SM-GW  SC        HSS       UE 2
  |-------->|         |         |         |         |         |         |  MESSAGE (1)
  |         |-------->|         |         |         |         |         |  (2)
  |         |         |------------------>|         |         |         |  (3)
  |         |         |<------------------|         |         |         |  202 (4)
  |         |<--------|         |         |         |         |         |
  |<--------|         |         |         |         |         |         |
  |         |         |         |         |-------->|         |         |  OFR (5)
  |         |         |         |         |         |-------->|         |  SRR (5)
  |         |         |         |         |         |<--------|         |  SRA
  |         |         |         |         |<--------|         |         |  OFA
  |         |         |         |<--------|         |         |         |  MESSAGE submit report (6)
  |         |         |         |---------------------------->|         |  LIR
  |         |         |         |<----------------------------|         |  LIA
  |         |         |<--------|         |         |         |         |
  |         |<--------|         |         |         |         |         |
  |<--------|         |         |         |         |         |         |
  |-------->|         |         |         |         |         |         |  200 (7)
  |         |-------->|         |         |         |         |         |
  |         |         |-------->|         |         |         |         |
  |         |         |         |-------->|         |         |         |
  |         |         |         |         |         |-------->|         |  SRR (8)
  |         |         |         |         |         |<--------|         |  SRA
  |         |         |         |         |<--------|         |         |  TFR (8)
  |         |         |         |<--------|         |         |         |  MESSAGE SMS-DELIVER (8)
  |         |         |         |---------------------------->|         |  LIR
  |         |         |         |<----------------------------|         |  LIA
  |         |         |<--------|         |         |         |         |  (9)
  |         |<--------|         |         |         |         |         |
  |         |---------------------------------------------------------->|
  |         |<----------------------------------------------------------|  200 (10)
  |         |-------->|         |         |         |         |         |
  |         |         |-------->|         |         |         |         |
  |         |         |         |-------->|         |         |         |
  |         |<----------------------------------------------------------|  MESSAGE delivery report (11)
  |         |-------->|         |         |         |         |         |  (12)
  |         |         |------------------>|         |         |         |
  |         |         |<------------------|         |         |         |  202
  |         |         |         |         |-------->|         |         |  TFA (12)
  |         |<--------|         |         |         |         |         |
  |         |---------------------------------------------------------->|
```

The I-CSCF does not record-route, and a MESSAGE starts no dialog, so every MESSAGE and its response go hop by hop. The MESSAGEs that the IP-SM-GW sends are new requests, each with its own Call-ID; the ladder of the Lab draws one Call-ID at a time.

The ladder draws one thing after another, but some of them run at the same time. The IP-SM-GW hands the message to the SC as soon as it has sent the 202, so the OFR of step 5 may come before the 202 in the capture. Once the SC has taken the message, the submit report (step 6) and the delivery (step 8) leave together, and their LIRs and responses are interleaved.

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

### 5. The IP-SM-GW hands the short message to the SC: OFR, SRR and OFA

The IP-SM-GW sends the SMS-SUBMIT to the SC in an **MO-Forward-Short-Message** (OFR, command 8388645 of SGd), which TS 23.204 6.3 step 6 and TS 29.338 6.2.1 describe. Look at:

- **SC-Address** `12025550100`, the number the UE addressed in the RP-DA. The SMS-IWMSC checks that it knows this SC (TS 29.338 6.2.1.3).
- **User-Identifier** with the MSISDN of the sender, `12025550101`, which becomes TP-OA of the SMS-DELIVER.
- **`SM-RP-UI`**, the SMS-SUBMIT as UE 1 wrote it, octet for octet.
- **Destination-Realm** `ims.mnc001.mcc001.3gppnetwork.org` and no Destination-Host: any instance of the SC may answer (TS 29.338 6.3.1.1).

Before it takes the message, the SC asks the HSS whether anyone has the number: a **Send-Routing-Info-for-SM** (SRR, command 8388647 of S6c) with the MSISDN `12025550102`, which TS 23.040 8.2.2 lets the SMS-IWMSC send. The SRA names the IP-SM-GW that serves UE 2, from the registration of step R5, and carries UE 2's IMSI in User-Name. A number no subscriber has gets DIAMETER_ERROR_USER_UNKNOWN instead, and the sender hears so at once rather than in a status report (see "When it fails").

The SC takes the short message, gives it a time stamp of its own (TP-SCTS) and answers the OFA with Result-Code 2001 (DIAMETER_SUCCESS) and the SMS-SUBMIT-REPORT in `SM-RP-UI`. From here on the short message belongs to the SC, which keeps it until it is delivered or its validity period ends (TS 23.040 6.1).

### 6. The submit report goes back to UE 1

The SC has taken the message, so the IP-SM-GW sends UE 1 a new MESSAGE with the submit report (TS 24.341 5.3.3.4.3):

- **Request-URI** UE 1's SIP URI, the first P-Asserted-Identity of the MESSAGE in step 1.
- **P-Asserted-Identity** the IP-SM-GW's SIP URI.
- **Accept-Contact: \*;+g.3gpp.smsip;require;explicit** and **Request-Disposition: fork**. Only a contact that registered the feature tag may get it (see step 9).
- **In-Reply-To** the Call-ID of step 1, so that UE 1 knows which short message this is about (TS 24.341 5.3.1.2).
- **P-Charging-Vector** with a new icid-value and an orig-ioi. The IP-SM-GW starts each of its own MESSAGEs this way, as an AS that acts as an originating UA (TS 24.229 5.7.3).
- **The body**, an RP-ACK with the same RP-MR as the RP-DATA, and inside it an SMS-SUBMIT-REPORT with the SC's time stamp (TP-SCTS).

The IP-SM-GW does not use the Route of the S-CSCF. It sends its own requests to the entry point of the home network, with `Route: <sip:ims.mnc001.mcc001.3gppnetwork.org;lr>`: an AS that sends a request to a user puts the user's S-CSCF or the entry point of the user's network in Route (TS 24.229 5.7.3). DNS resolves the name of the home domain to the I-CSCF, which takes that Route as its own and removes it (5.3.2.1). The I-CSCF asks the HSS with an LIR, as for a call, and forwards the MESSAGE to UE 1's S-CSCF, which serves it as a terminating request and sends it along UE 1's Path.

### 7. UE 1 answers 200

UE 1 matches In-Reply-To to its short message, answers **200** (TS 24.341 5.3.1.2), and marks the message accepted.

### 8. The SC forwards the message, and the IP-SM-GW delivers it: TFR and SMS-DELIVER

The SC turns the SMS-SUBMIT into an SMS-DELIVER: the sender's number as TP-OA, the time it took the message as TP-SCTS, the same text, and TP-SRI set because UE 1 asked for a status report.

It then asks the HSS again, with an SRR of its own (TS 23.040 8.1.1): the first one belonged to the SMS-IWMSC taking the message, this one to the SMS-GMSC delivering it, and in between the user may have moved or gone. The SRA names the IP-SM-GW again, and the SC sends it the short message in an **MT-Forward-Short-Message** (TFR, command 8388646 of SGd), which TS 29.338 6.2.2 describes:

- **Destination-Host** the IP-SM-GW's Diameter identity, which the SRA gave. A TFR always names the node it goes to (TS 29.338 6.3.1.2).
- **User-Name** UE 2's IMSI, which is how a TFR names the recipient (TS 29.338 Table 6.2.2.1/1). The IP-SM-GW finds the user by the IMSI it read in step R5.
- **SC-Address** `12025550100`, which becomes the RP-OA of the RP-DATA to the UE.
- **`SM-RP-UI`**, the SMS-DELIVER.

The IP-SM-GW looks the IMSI up, finds UE 2 and sends the short message in a MESSAGE (TS 24.341 5.3.3.4.2):

- **Request-URI** `tel:+12025550102`, the tel URI of the IMSI it was given (TS 23.204 6.4 step 6).
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

The delivery report is an originating request of UE 2, a MESSAGE with an SMS body, so UE 2's own second criterion sends it to the IP-SM-GW, with an ODI, exactly as in step 3. The IP-SM-GW answers 202 Accepted (TS 24.341 5.3.3.4.1) and finds the delivery by the Call-ID in In-Reply-To.

Only now does the TFR of step 8 get its answer: the **TFA** with Result-Code 2001, which tells the SC that the short message arrived (TS 23.204 6.4 NOTE 4). The SC drops it from its queue, and sends the status report if the sender asked for one. The TFR therefore waits for as long as the delivery takes, up to 66 seconds here, which is why the SC waits longer than that for a TFA: TS 23.204 6.5a NOTE 1 has the timer of the SMS-GMSC made longer where it leaves the serving node too little time.

TS 23.204 6.5 step 3 asks that the delivery report reach the IP-SM-GW that sent the short message. Here DNS may give another IP-SM-GW node, so every IP-SM-GW node keeps the deliveries in one shared `Store`, and whichever node gets the report finishes the delivery (`TestDeliveryReportThroughAnotherIPSMGW` in `internal/ipsmgw/network_test.go`). The wait for the report, 64 seconds, is kept with the delivery: every node looks once a second for the deliveries whose wait is over and tells the SC that those it finds could not be confirmed, so a delivery is not left without an outcome when the node that sent the message stops.

## The status report

UE 1 asked for one (TP-SRR in the SMS-SUBMIT), so once the delivery report is in, the SC sends an SMS-STATUS-REPORT to UE 1:

```text
 UE 1     P-CSCF    S-CSCF    I-CSCF   IP-SM-GW   SC   HSS
  |         |         |         |          |    SRR (13)    |
  |         |         |         |          |    |---------->|
  |         |         |         |          |    |  SRA      |
  |         |         |         |          |    |<----------|
  |         |         |         |          |<---| TFR (13)  |
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

13. As soon as the TFA of step 12 is in, the SC puts an SMS-STATUS-REPORT for `+12025550101` in the queue of that number and delivers it like any short message: an SRR for the sender, and a TFR to the IP-SM-GW that serves them. The capture may show the SRR before the 202 of step 12.
14. The IP-SM-GW sends the status report like a short message (TS 24.341 5.3.3.4.4): to UE 1's tel URI, with Accept-Contact and `Request-Disposition: no-fork`, and an RP-DATA with an SMS-STATUS-REPORT inside. It repeats the TP-MR of the SMS-SUBMIT, so that UE 1 knows which message it reports on, and TP-ST 0x00 says that the recipient received it (TS 23.040 9.2.3.15).
15. UE 1 answers 200 and acknowledges the status report with a delivery report, as UE 2 did in step 11 (TS 24.341 5.3.1.3).

## When the UE cannot take it: hold, alert and resend

A registered UE does not always take a short message. It may answer that it cannot be reached just now, or that it has no memory left for one. The SC keeps the short message instead of dropping it, the HSS writes the SC's address into the message waiting data (MWD) of the user, and once the user can take short messages again the HSS alerts the SC, which delivers what it held (TS 23.040 3.2.6, TS 23.204 6.5a, 6.5b and 6.6).

The retry schedule of this SC is one hour (`retry_delays` in `deploy/compose/sc.yaml`), so what delivers the short message in the runs below is the alert and not a retry. TS 23.040 3.2.7 NOTE 5 leaves those autonomous retries to the operator. Here they cover an alert that the HSS sent but that never reached the SC. They cannot stand in for the news that the UE is back: while the HSS holds the flag of A4 below, the SRA of each retry says DIAMETER_ERROR_ABSENT_USER.

The ladders leave out the P-CSCF between the S-CSCF and the UE, and the LIR with which the I-CSCF finds UE 2's S-CSCF. The ladder of the delivery above shows both. The ladder of a re-registration below shows the P-CSCF, which the REGISTER passes, and leaves out the I-CSCF.

### UE 2 takes no short message

`PUT /sms-availability` with `{"available": false}` on UE 2's control API puts it in that state. UE 1 then sends it a short message.

```text
 S-CSCF    I-CSCF    IP-SM-GW  SC        HSS       UE 2
  |         |         |<--------|         |         |  TFR (A1)
  |         |<--------|         |         |         |  MESSAGE SMS-DELIVER
  |<--------|         |         |         |         |
  |------------------------------------------------>|
  |<------------------------------------------------|  480 (A2)
  |-------->|         |         |         |         |
  |         |-------->|         |         |         |
  |         |         |-------->|         |         |  TFA absent user (A3)
  |         |         |         |-------->|         |  RDR (A4)
  |         |         |         |<--------|         |  RDA
  :         :         :         :         :         :  UE 2 takes short messages again
  |<------------------------------------------------|  MESSAGE (A5)
  |------------------>|         |         |         |
  |<------------------|         |         |         |  202
  |         |         |------------------>|         |  PUR ReadyForSM msPresent (A6)
  |         |         |<------------------|         |  PUA
  |         |         |         |<--------|         |  ALR (A7)
  |         |         |         |-------->|         |  ALA
  |         |         |         |-------->|         |  SRR (A8)
  |         |         |         |<--------|         |  SRA
  |         |         |<--------|         |         |  TFR (A8)
  |         |<--------|         |         |         |  MESSAGE SMS-DELIVER
  |<--------|         |         |         |         |
  |------------------------------------------------>|
  |<------------------------------------------------|  200, then MESSAGE delivery report (A9)
  |------------------>|         |         |         |
  |         |         |-------->|         |         |  TFA 2001 (A9)
```

- **A1 to A2.** The delivery is the one of step 8 above, as far as UE 2, which answers **480 (Temporarily Unavailable)**: it was reached, but its state does not allow the request (TS 24.341 5.3.3.4.2). No delivery report follows.
- **A3.** TS 29.311 6.1.4.4.1 maps a 480 to the user error of MAP for an absent subscriber, which TS 29.338 7.3.3 carries as **DIAMETER_ERROR_ABSENT_USER (5550)** in the TFA. The IP-SM-GW also raises its own UE-Not-Reachable-for-IP flag (UNRI) for the user, since only it knows that the delivery over IMS failed (TS 23.040 2.1.1).
- **A4.** The SC tells the HSS with a **Report-SM-Delivery-Status** (RDR, command 8388649 of S6c) whose `SM-Delivery-Cause` is ABSENT_USER (TS 29.338 5.2.3, TS 23.040 3.2.8). The HSS puts the SC's address in the MWD and sets UNRI of its own. While UNRI is set the HSS names no serving node in an SRA, so a Service Centre that asks now is not sent to an IP-SM-GW where the delivery would fail again (TS 29.338 5.2.1.3).
- **A5.** `{"available": true}` takes short messages again, and the next request of UE 2 that reaches the IP-SM-GW says that the UE is there, whatever that request carries (TS 23.204 6.5b step 2). The run uses a short message of UE 2's own, whose own OFR the ladder leaves out. A re-registration says the same, as the next run shows.
- **A6.** The IP-SM-GW clears its own UNRI and tells the HSS in a PUR whose SMSRegistrationInfo carries `<ReadyForSM xmlns="https://github.com/yuu-biz/ims-practice/ns/sh">msPresent</ReadyForSM>`. Sh has no element for this notification and this project speaks no MAP, so this one is of the project's own namespace (see [What this project simplifies](#what-this-project-simplifies)). The PUA does not wait for the alert, so the capture may show it before the ALR.
- **A7.** The HSS clears UNRI and sends every Service Centre in the MWD an **Alert-Service-Centre** (ALR, command 8388648 of S6c), over the connection that Service Centre opened (TS 29.338 5.2.2, TS 23.040 3.2.6 item 2c). An address whose Service Centre answers with an ALA leaves the MWD. The HSS finds the Diameter identity of the Service Centre by its address, in `service_centres` of `deploy/compose/hss.yaml`, and names it in Destination-Host (TS 29.338 5.3.1.2). A Service Centre that runs as several instances is listed there with one identity for each, under `hosts`, and the HSS alerts them in that order until one answers with a successful ALA: the instances hold the same short messages, so any one of them delivers.
- **A8.** The alert makes the queue of that number due at once, ahead of the retry schedule: an SRR, which names the IP-SM-GW again now that UNRI is clear, and the TFR of the short message the SC held.
- **A9.** UE 2 takes it this time. Its delivery report ends the delivery, and the TFA says 2001, as in steps 11 and 12.

### UE 2 only registers again

A UE need not send anything to show that it is back. After A1 to A4 of the run above, `{"available": true}` and then `POST /register` on UE 2's control API make it register again, and it does nothing else.

```text
 UE 2      P-CSCF    S-CSCF    IP-SM-GW  SC        HSS
  :         :         :         :         :         :  A1 to A4: the 480, and the SC holds the short message
  |<=================>|         |         |         |  REGISTER ... 200 OK (C1)
  |         |         |-------->|         |         |  third-party REGISTER (C2)
  |         |         |<--------|         |         |  200 OK
  |         |         |         |------------------>|  PUR ReadyForSM msPresent (C3)
  |         |         |         |<------------------|  PUA
  |         |         |         |         |<--------|  ALR (C4)
  |         |         |         |         |-------->|  ALA
  :         :         :         :         :         :  the resend of A8 and A9 follows
```

- **C1.** UE 2 registers again before its registration runs out, as a UE does on its own; `POST /register` does it at once. Its contact has `+g.3gpp.smsip`, as before.
- **C2.** The S-CSCF sends the IP-SM-GW a third-party REGISTER, as for every re-registration. The ladder leaves out the SUBSCRIBE with which the IP-SM-GW refreshes its subscription, and the NOTIFYs of the S-CSCF ([Re-registration and deregistration](#re-registration-and-deregistration)). They show no change, so none of them sends a PUR (TS 24.341 5.3.3.2).
- **C3.** The registration shows that the UE is there (TS 23.204 6.5b step 2a). The IP-SM-GW still holds its own UNRI for UE 2 from A3, so it clears it and sends the HSS the notification of A6.
- **C4.** The HSS clears its own UNRI and alerts the SC, as in A7 (TS 23.040 3.2.6 item 2c). Without the notification of C3, nothing would clear UNRI while UE 2 only registered again: each SRR of the SC would be answered DIAMETER_ERROR_ABSENT_USER, and so would the SRR of every new short message for UE 2.

The resend is the one of A8 and A9.

### UE 2 has no memory left

`PUT /sms-memory` with `{"full": true}` puts it in that state, and UE 1 sends it a short message.

```text
 S-CSCF    I-CSCF    IP-SM-GW  SC        HSS       UE 2
  |         |         |<--------|         |         |  TFR (B1)
  |         |<--------|         |         |         |  MESSAGE SMS-DELIVER
  |<--------|         |         |         |         |
  |------------------------------------------------>|
  |<------------------------------------------------|  200, then MESSAGE delivery report: RP-ERROR 22 (B2)
  |------------------>|         |         |         |
  |<------------------|         |         |         |  202
  |         |         |-------->|         |         |  TFA memory capacity exceeded (B3)
  |         |         |         |-------->|         |  RDR (B4)
  |         |         |         |<--------|         |  RDA
  :         :         :         :         :         :  UE 2 has memory again
  |<------------------------------------------------|  MESSAGE RP-SMMA (B5)
  |------------------>|         |         |         |
  |         |         |------------------>|         |  PUR ReadyForSM memoryAvailable (B6)
  |         |         |<------------------|         |  PUA
  |         |         |         |<--------|         |  ALR (B7)
  |         |         |         |-------->|         |  ALA
  |<------------------|         |         |         |  202, then MESSAGE RP-ACK (B8)
  :         :         :         :         :         :  the resend of A8 and A9 follows
```

- **B1 to B2.** UE 2 answers the MESSAGE **200**, so the short message reached it, and then sends a delivery report whose **RP-ERROR carries RP-Cause 22** (memory capacity exceeded) and whose SMS-DELIVER-REPORT has TP-FCS `0xD3` (TS 24.011 table 8.4 part 2, TS 23.040 9.2.3.22). It keeps nothing of the short message, and owes the network a notification.
- **B3.** The IP-SM-GW answers the TFA with **DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555)** and `SM-Enumerated-Delivery-Failure-Cause` MEMORY_CAPACITY_EXCEEDED (TS 29.338 7.3.8, Table 6.2.2.1/2).
- **B4.** The SC reports it with an RDR whose `SM-Delivery-Cause` is UE_MEMORY_CAPACITY_EXCEEDED. The HSS puts the SC's address in the MWD and sets the memory capacity exceeded flag (MCEF) of the user (TS 23.040 3.2.6 item 1). UNRI stays clear: the UE is reachable, it just has no room.
- **B5.** `{"full": false}` makes the UE send the notification it owes, an **RP-SMMA** in a MESSAGE to the IP-SM-GW (TS 24.011 7.3.2, TS 24.341 5.3.2.5).
- **B6 to B7.** The IP-SM-GW passes the notification to the HSS as `<ReadyForSM xmlns="https://github.com/yuu-biz/ims-practice/ns/sh">memoryAvailable</ReadyForSM>` (TS 23.204 6.6). The HSS clears MCEF and UNRI and alerts the Service Centres of the MWD (TS 23.040 3.2.6 item 2f).
- **B8.** The RP-ACK for the notification goes to the UE only after the HSS has taken it, so an RP-ACK never tells the UE that a notification got through when it did not. A notification the HSS refuses gets RP-Cause 41 instead, and the UE still owes one.

The resend is the one of A8 and A9: an SRR, the TFR of the short message the SC held, and the delivery report that ends it.

## When it fails

The IP-SM-GW and the SC decide what goes back to the sender. The cases you can try are:

| Case | What happens | What UE 1 gets |
| --- | --- | --- |
| The number has no subscriber (`+12025550198`) | The SRA of step 5 has DIAMETER_ERROR_USER_UNKNOWN (5001), and the OFA refuses the short message with DIAMETER_ERROR_SM_DELIVERY_FAILURE and the cause INVALID_SME-ADDRESS. | A submit report that is an RP-ERROR with RP-Cause 1 (unassigned number) and an SMS-SUBMIT-REPORT with TP-FCS 0xC3 (invalid SME address) (TS 24.011 Table 8.4, TS 23.040 9.2.3.22). No delivery is tried. |
| The recipient is not registered (`+12025550103`) | Subscriber 3 exists, so the SC takes the message and the submit report is an RP-ACK. No IP-SM-GW has registered for the user, so each SRR of the SC answers DIAMETER_ERROR_ABSENT_USER (5550) and the HSS keeps the SC's address in the MWD (TS 29.338 5.2.1.3). | Nothing yet. The SC holds the short message, and the alert of the HSS delivers it when subscriber 3 registers. UE 1's `ue sms -status-report` gives up waiting; the status report would come once the short message arrived, or once its validity period of 72 hours ended. |
| The recipient cannot be reached or has no memory | [Hold, alert and resend](#when-the-ue-cannot-take-it-hold-alert-and-resend) above. | Nothing yet, and then the status report of the delivery that the alert set off. |
| The RP-DATA does not decode | The IP-SM-GW answers 202, then refuses it. | An RP-ERROR with RP-Cause 96 (invalid mandatory information) (TS 24.341 5.3.3.4.1, TS 24.011 9.3.4). |

`TestShortMessagesThatFail` in `internal/ipsmgw/network_test.go` runs the undecodable RP-DATA through the whole IMS.

## Take the IP-SM-GW out of service

An operator takes a node out of service without cutting what it serves: the node takes nothing new and goes on with what it has begun, until nothing is left. This is called a drain.

Everything the IP-SM-GW keeps past one SIP transaction is in its store: the deliveries that wait for a delivery report, each with the time at which it gives up on one, and its subscriptions to the registration states. A commercial network runs several instances of the IP-SM-GW behind the one name that the filter criteria give as the server, and DNS spreads the new requests over them. None of the stored state is tied to one instance, so a delivery report reaches whichever instance DNS leads it to, and so does a NOTIFY: the Contact of the SUBSCRIBE is that one name and not one node. An instance that the operator takes out of service therefore has only what is in its hands at that moment to finish.

The node of the IP-SM-GW serves a maintenance API for this on port 8085, the listener of the `maintenance` section of `deploy/compose/ipsmgw.yaml`. The API asks for no password, so the port is not published on the host: send the requests from inside the container of the IP-SM-GW. The administrative state has the values of TS 28.622 5.3.1. `UNLOCKED` takes new work, `SHUTTINGDOWN` serves only what it has, and `LOCKED` takes nothing new.

1. Shut the IP-SM-GW down:

   ```sh
   docker compose exec ipsmgw sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/ipsmgw/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the IP-SM-GW. With nothing in its hands it is `LOCKED` at once (X.731 7.1.3.3), and the steps above leave it nothing in its hands:

   ```json
   {"role":"ipsmgw","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   If `users` is above zero, the answer says `SHUTTINGDOWN` instead, and the IP-SM-GW becomes `LOCKED` when the last of that work ends. Either way it takes nothing new from here on.

   `users` counts what this instance has in its hands and has not finished: each request it is answering, counted once it has decided to take the request; each SUBSCRIBE of its own, and each refresh of one, until it has its final response; the news to the HSS, after a MESSAGE from a user, that a user this instance could not reach is back; each delivery that the SC handed it, until the MESSAGE that carries the short message has its final response; each delivery whose wait for a report has run out and which this instance has taken on to end; and each short message that reached it over SGd, until the answer to that request goes back. The wait for a delivery report is none of them: the delivery and that wait are in the store, so the report is served by whichever instance it reaches and any instance ends the delivery when the wait runs out. Stopping this instance loses none of it. Everything that is counted but a short message from SGd ends in the time a few SIP transactions take, so a shut down with none of those in hand reaches `LOCKED` quickly; one of those holds for as long as its delivery does, and `timeout_seconds` in the request bounds both where work keeps arriving. `operational_state` says whether the IP-SM-GW can work (TS 28.622 5.3.3): it is `ENABLED` while an instance of the HSS takes its Sh requests, and `DISABLED` without one, since it could then neither register a user for short messages nor let the SC beside it find which user has the number a short message is for. `docker compose exec ipsmgw wget -qO- http://127.0.0.1:8085/maintenance/v1/state` shows the whole node.

2. Send a short message from UE 1:

   ```sh
   docker compose run --rm ue sms +12025550102 "Is anyone on duty?"
   ```

   UE 1 registers, and `ue sms` then ends with `MESSAGE answered 500 Server Internal Error`.

```text
 UE 1       P-CSCF    S-CSCF    IP-SM-GW
 |  REGISTER  |         |          |
 |----------->|-------->|          |  the registration itself is not refused
 |  200       |  200    |          |
 |<-----------|<--------|          |
 |            |         | REGISTER (third party)
 |            |         |--------->|
 |            |         |  503 Retry-After: 30
 |            |         |<---------|
 |  MESSAGE   |         |          |
 |----------->|-------->|          |  tel:+12025550100, the PSI of the SC
 |            |         |--------->|  RP-DATA with the SMS-SUBMIT
 |            |         |  503 Retry-After: 30
 |            |         |<---------|
 |  500       |  500    |          |  the 503 of the IP-SM-GW, as a proxy passes it on
 |<-----------|<--------|          |
```

- **The IP-SM-GW answers 503 Service Unavailable** with **Retry-After: 30** to the third-party REGISTER and to the MESSAGE. Neither has begun here: the subscription that the third-party REGISTER would make, and the delivery that the short message would start, both live in the store, where every instance finds them. Retry-After asks the client to send this server no other request for that many seconds (RFC 3261 21.5.4). No specification gives the value; 30 seconds is this project's choice.
- **The S-CSCF sends each request on to the next instance** that DNS gave it for the name of the IP-SM-GW (RFC 3263 4.3), so the 503 reaches the S-CSCF itself only once there is none left to try. This network runs one instance.
- **The registration stands.** The filter criterion that sends the third-party REGISTER says SESSION_CONTINUED, so a server that fails leaves the S-CSCF to go on with the criteria of lower priority (TS 24.229 5.4.1.7). UE 1 is registered and can call and be called. What it loses is SMS: no IP-SM-GW has subscribed to its registration state, so none has told the HSS that it takes short messages. The third-party REGISTER of its next re-registration subscribes.
- **The short message ends in 500 Server Internal Error.** The criterion of the MESSAGE says SESSION_TERMINATED, so the S-CSCF passes the response of the IP-SM-GW on instead of going on with the criteria of lower priority (TS 24.229 5.4.3.2). A proxy does not pass a 503 on, and turns it into 500 (RFC 3261 16.7 step 6), so the Retry-After stays between the IP-SM-GW and the S-CSCF. Going on without the IP-SM-GW would lead nowhere: the Request-URI is the PSI of the SC that the UE addressed (TS 24.341 5.3.1.2 item a), and no node answers SIP at that identity, since the Service Centre of this network speaks Diameter only. TS 24.341 5.3.1.2 gives the UE no handling of a failure response to that MESSAGE: it takes the submit report that comes in a MESSAGE of its own, and nothing else. The UE simulator reports the status code and sends nothing again.

While it is not `UNLOCKED`, the IP-SM-GW goes on with what has begun and refuses what would start something new.

It goes on with:

- **The MESSAGE that carries a delivery report**, which `In-Reply-To` tells from every other MESSAGE (TS 24.341 5.3.2.4 item d) although it arrives outside any dialog, as every MESSAGE does. It is the RP-ACK or RP-ERROR for a short message that this network has already delivered, and so the end of a delivery that has begun. A 503 to it would leave that delivery waiting until its timer ran out, and the SC would hear of a failure for a short message the UE did have. A delivery report whose `In-Reply-To` names no delivery of this network is taken too, and gets the 488 (Not Acceptable Here) of [When it fails](#when-it-fails): the header is what tells a report from a new short message, and another instance has no such delivery either.
- **The NOTIFY of a subscription to a registration state**, which is within the dialog of a SUBSCRIBE that this network sent and carries it on: the first one completes that dialog, and each one says whether the user still has a contact that takes short messages (step R4 above). A 503 to it would lose what it says until the next one comes, and leave the HSS holding a registration that no longer holds.
- **The short message that the IP-SM-GW of another IMS network sends to a user of this one once this instance has taken it on**, which the stub of the SC inside the node delivers where the deployment has no Service Centre. A short message that the Service Centre brings over SGd is refused instead, because that Service Centre can ask another instance.
- **The deliveries whose wait for a delivery report has run out**, its own and those of other instances. The SC hears the outcome of every delivery, and one whose report never came has no outcome until an instance ends it.

It answers 503 to:

- **the third-party REGISTER**, of a registration, a re-registration and a deregistration alike (step R2 above). For a registration or a re-registration, what the refusal costs is the subscription to the registration state that would have followed it; the third-party REGISTER of the next re-registration makes that subscription instead. A refused re-registration also holds back the news that a UE whose short message was not delivered is back (C3 above), until the next one. For a deregistration it costs nothing at all: the IP-SM-GW answers that one and does no more, because the S-CSCF ends the subscription with a NOTIFY of its own ([Re-registration and deregistration](#re-registration-and-deregistration)). That NOTIFY reaches whichever instance the store leads it to, and is within a dialog, so even an instance that is shutting down takes it, removes the subscription and tells the HSS that the user takes short messages no longer;
- **a short message from a UE**, and a notification that a UE has memory again. Each starts the transfer of a short message;
- **a short message that the IP-SM-GW of another IMS network sends to a user of this one.** It has begun in that network, but nothing of it has reached this instance, so another instance can take it whole. Where none can, the S-CSCF routes the MESSAGE to the recipient's contact itself, and the UE gets the short message of that network with no IP-SM-GW of this one in between.

A method that the IP-SM-GW does not take at all keeps its 405 (Method Not Allowed), out of service or not: the method is no more supported out of service than in it, and the next instance would answer the same.

On its Diameter side the IP-SM-GW refuses a short message that a Service Centre brings over SGd, with DIAMETER_TOO_BUSY (3004). That is the answer of a node that is up but cannot serve a request that names it, and RFC 6733 7.1.3 has the node that gets it try an alternate peer. Nothing of the delivery has begun, so the Service Centre sends the TFR again to another instance of the IP-SM-GW: one of the same realm that is connected to it over SGd. Any instance finds the user by the IMSI, since the instances keep their users in one store. A command the IP-SM-GW does not take at all keeps its DIAMETER_COMMAND_UNSUPPORTED (3001), as a method keeps its 405.

```text
 SC          IP-SM-GW 1     IP-SM-GW 2     UE 2
  |  TFR        |              |              |  Destination-Host: the instance the HSS named
  |------------>|              |              |
  |  TFA 3004   |              |              |  DIAMETER_TOO_BUSY: nothing has begun
  |<------------|              |              |
  |  TFR        |              |              |  Destination-Host: another instance of the realm
  |--------------------------->|              |
  |             |              |  MESSAGE     |  through the CSCFs, as in step 8
  |             |              |------------->|
  |  TFA 2001   |              |              |  once the delivery report has come
  |<---------------------------|              |
```

- **The SC makes the switch, not the Diameter routing.** A TFR names its node in Destination-Host (TS 29.338 6.3.1.2), which makes its destination fixed: a Diameter agent cannot send such a request to another server, and answers DIAMETER_UNABLE_TO_DELIVER when its destination is gone (RFC 6733 5.5.4). The sender is the one that can choose another node.
- **An instance that the SC has no connection to**, because it has stopped, is passed over in the same way, before any TFR goes out to it.
- **A TFR that went out and got no answer stays with its instance.** That instance may have delivered the short message already, and the UE would get it twice. The SC keeps the short message for its next retry instead.
- **The HSS goes on naming the instance that registered the user**, until the user's ability to take short messages changes (TS 24.341 5.3.3.2), so each short message for that user goes to it first.

This network runs one instance of the IP-SM-GW, so the Service Centre has no other to try, and a short message for a user waits for its next retry while the IP-SM-GW is out of service. `TestShortMessageForAShutDownIPSMGWGoesThroughAnother` in `internal/ipsmgw/chain_test.go` runs it with two.

3. Unlock the IP-SM-GW with the same request and `UNLOCKED`, and send the short message again:

   ```sh
   docker compose exec ipsmgw sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/ipsmgw/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   docker compose run --rm ue sms +12025550102 "Back on duty?"
   ```

   The IP-SM-GW answers 202 again and the submit report comes back. UE 2 is not running here, so the delivery itself fails as in [When it fails](#when-it-fails).

In Wireshark, `sip.Status-Code == 503 && ip.src == 172.28.0.70` shows the 503s of the IP-SM-GW. The PUT request travels over the loopback interface of the container, so the capture does not hold it.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-sms.sh` runs these steps and checks the capture.

## Take the Service Centre out of service

The Service Centre keeps every short message it holds in its store, by recipient, with the time of the next attempt for each. Any instance takes a short message and any instance delivers it: a commercial network runs several instances of the SC on one store, and each serving node keeps a Diameter connection to every one of them. An instance holds a piece of that work for itself only while it attempts a delivery, under a lease on the recipient that runs out if the instance stops. An instance that the operator takes out of service therefore has only the attempts it has begun to finish, and everything else goes to an instance in service through the store.

The SC cannot turn a short message away in its answer. The OFR of the IP-SM-GW names the realm of the SC and no Destination-Host (TS 29.338 6.3.1.1), and DIAMETER_TOO_BUSY, the answer that sends a request to another node, is only for a request that names one server (RFC 6733 7.1.3). The failure causes that TS 29.338 6.2.1.1 gives the answer refuse the short message itself: "Service Centre congestion" reaches the UE as RP-Cause 42 (congestion) (TS 24.011 Table 8.4), and the short message goes to no other instance. So the SC goes out of service on its Diameter connections, as the HSS does ([Take the HSS out of service](registration.md#take-the-hss-out-of-service)).

The node of the SC serves the same maintenance API on port 8085, the listener of the `maintenance` section of `deploy/compose/sc.yaml`, next to the metrics that Prometheus scrapes there. Send the requests from inside the container of the SC.

1. Shut the SC down:

   ```sh
   docker compose exec sc sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/sc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   With nothing in its hands it is `LOCKED` at once (X.731 7.1.3.3), and the steps above leave it nothing in its hands:

   ```json
   {"role":"sc","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` counts the OFRs and the alerts that the SC is answering, until the answer is on its way, and the recipients it holds a lease on, until the attempt has ended: the routing query, the TFR, and the report of a failure to the HSS. One attempt can last a minute or more, because the IP-SM-GW answers a TFR only once the UE has sent its delivery report or the wait for one has run out. The short messages that the SC holds are none of them: they are in the store, and any instance delivers them. `operational_state` is `ENABLED` while an instance of the HSS takes the S6c requests of the SC, and `DISABLED` without one, since the SC could then neither check the recipient of a short message nor find the node that serves it. It is also `DISABLED` while the SC is unlocked but its node does not listen for Diameter connections.

2. Send a short message from UE 1:

   ```sh
   docker compose run --rm ue sms +12025550102 "Is the Service Centre in?"
   ```

   UE 1 registers, and `ue sms` then ends with `the SC refused the short message: RP-Cause 41, TP-FCS 0x0`.

```text
 UE 1      CSCFs     IP-SM-GW     SC
  |          |          |  DPR, Disconnect-Cause REBOOTING
  |          |          |<---------|  the shut down, with nothing in hand
  |          |          |  DPA     |
  |          |          |--------->|  the connection closes
  |          |          |  a new connection every Tc, refused
  |          |          |----x     |
  |  MESSAGE |          |          |  RP-DATA with the SMS-SUBMIT
  |--------->|--------->|          |
  |  202     |  202     |          |
  |<---------|<---------|          |  no OFR: no connection to an SC
  |  MESSAGE |          |          |  RP-ERROR with RP-Cause 41
  |<---------|<---------|          |
```

- **The SC ends each connection that a serving node opened** with a Disconnect-Peer-Request (DPR) whose `Disconnect-Cause` is `REBOOTING`, and the connection closes once the Disconnect-Peer-Answer (DPA) comes back (RFC 6733 5.4). From the shut down on, its node holds no listening socket for Diameter, so the IP-SM-GW, which tries to connect again every Tc (30 seconds, RFC 6733 2.1), is refused by the transport. `REBOOTING` is the cause that lets the IP-SM-GW come back (5.4.3).
- **The IP-SM-GW sends the OFR to the next instance of the SC** that it has a connection to. This network runs one, so it has none left, and it answers the UE with an RP-ERROR whose RP-Cause is 41 (temporary failure) (TS 24.011 Table 8.4), the cause it gives whenever it cannot reach the SC. A UE that gets it may send the short message again later. The IP-SM-GW itself is in service, so the MESSAGE still gets its 202.
- **The connection to the HSS stays.** The SC opened it itself, and the HSS alerts the SC over it. An alert that reaches an SC out of service makes the queue of the user due in the store, and an instance in service delivers it.

While it is `SHUTTINGDOWN`, the SC goes on with what it has begun and starts no new attempt:

- **It keeps the connections it has.** An OFR that arrives on one of them is taken as ever: the SC holds the short message in the store, answers with its submit report, and leaves the delivery to an instance in service.
- **It finishes each attempt it has begun**, and then claims neither the next short message for that recipient nor any other recipient. The retries, the ends of the validity periods and the queues that an alert has made due wait in the store.

Once nothing is left in its hands it is `LOCKED`, and ends the connections as in step 2. A lock does that at once, and so does the end of `timeout_seconds` in the request. A TFR that is still waiting for its answer then gets none: the SC takes that as a failure that may pass (TS 23.040 Table 1) and keeps the short message for its next retry, although the IP-SM-GW may have delivered it already, and the recipient then gets it twice.

An instance that has stopped altogether has no connection to the HSS either. The HSS then alerts the next instance that `hosts` names for the SC address, and that one delivers (`TestAnotherSCDeliversWhatItHoldsWhenTheHSSAlertsIt` in `internal/ipsmgw/chain_test.go`). A Diameter agent could not do this in place of the HSS: the alert names its instance in Destination-Host, and an agent does not take a request with a fixed destination to another server (RFC 6733 5.5.4).

This network runs one instance of the SC, so whatever it holds while it is out of service waits for the unlock.

3. Unlock the SC with the same request and `UNLOCKED`:

   ```sh
   docker compose exec sc sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/sc/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The node listens again, and the IP-SM-GW connects at its next attempt, within 30 seconds: `docker compose logs ipsmgw` then shows its connection to `sc.ims.mnc001.mcc001.3gppnetwork.org` in `I-Open` again. Send the short message once more, and the submit report is an RP-ACK. UE 2 is not running here, so the delivery itself fails as in [When it fails](#when-it-fails).

In Wireshark, `diameter.cmd.code == 282 && ip.src == 172.28.0.71` shows the DPR of the SC. The PUT request travels over the loopback interface of the container, so the capture does not hold it.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-sms.sh` runs these steps and checks the capture.

## Read the body

The body of a MESSAGE is binary: TS 24.341 has the UE send the octets as they are. Here is the body of the MESSAGE of step 1, as the UE simulator built it with `internal/sms`. The Lab shows the same octets under "Body (hex)" in the details of the MESSAGE.

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
| `diameter.applicationId == 16777313` | SGd, between the IP-SM-GW and the Service Centre: the short messages themselves. `16777312` is S6c, between the Service Centre and the HSS (TS 29.338 4.8). |
| `diameter.cmd.code == 8388646` | The short messages that the Service Centre forwards to a serving node (TFR), and the answers that say how each delivery ended (TFA). `8388645` is the pair of a short message from a UE (OFR, OFA), `8388647` the routing info query (SRR, SRA), `8388649` the delivery status report (RDR, RDA) and `8388648` the alert (ALR, ALA). |

`scripts/e2e-sms.sh` runs every filter of this table against the capture of this scenario: a filter that finds no frame fails the run.

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
| `internal/ipsmgw` | The IP-SM-GW: `ipsmgw.go` (the third-party REGISTER), `regevent.go` (the reg event subscription and the PUR), `message.go` (MESSAGEs from UEs), `deliver.go` (MESSAGEs to UEs), `servicecentre.go` (the boundary to the SC), `sgd.go` (a short message that a Service Centre brings over SGd), `store.go` (the deliveries in progress). |
| `internal/smsc` | The Service Centre: `submit.go` (a short message from a serving node), `deliver.go` (the routing info query, the delivery and the retries), `alert.go` (the alert of the HSS), `store.go` (the short messages it holds, by recipient), `maintenance.go` (what it tells the maintenance API of its node, and what it does out of service). |
| `internal/sgd`, `internal/hss/s6c.go` | The SGd and S6c messages, their clients, and the HSS side of S6c. |
| `cmd/ims/sc.go` | How the `sc` role wires the Service Centre to its Diameter node, and takes it off the Diameter listener of the node while the operator has it out of service. |
| `internal/ipsmgw/smsiwmsc.go` | The IP-SM-GW handing a short message to the Service Centre over SGd. |
| `internal/sc` | The SC stub, which an IP-SM-GW without a Service Centre of its own uses instead. |
| `internal/ue/sms.go` | The UE as SM-over-IP sender and receiver. |
| `internal/lab/sms.go` | How the Lab decodes a short message for the details view. |
| `internal/ipsmgw/network_test.go`, `internal/ue/sms_ims_test.go` | The whole flow in one process, with the cases above. |
| `internal/ipsmgw/chain_test.go` | The IP-SM-GW, the Service Centre and the HSS in one process, on real Diameter connections: a delivery, and the ones that are held and alerted, with the UE back by a short message, by a re-registration or by the notification that it has memory again. |

## What this project simplifies

Each item has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all. Some of them also appear in the registration and call guides.

| Where | What is left out | Why |
| --- | --- | --- |
| IP-SM-GW, TS 24.229 5.7.1.1 and 5.7.3 | The icid-value and the orig-ioi of a third-party REGISTER, and the term-ioi of the responses to the IP-SM-GW's own requests, are not stored. | They are for the charging records of the IP-SM-GW, which no node of this project writes. |
| IP-SM-GW, RFC 6665 4.1.2.4 | No Timer N. A SUBSCRIBE that no NOTIFY follows is replaced by a new one at the next third-party REGISTER. | The S-CSCF answers every SUBSCRIBE that it accepts with a NOTIFY at once. |
| IP-SM-GW, TS 24.341 5.3.3.2 | Without an MSISDN in service-info, the PUR names the user by the public identity, not by the IMSI of the UE's REGISTER. | The IMSI would come from the UE's REGISTER in a message/sip body, which the S-CSCF does not send. Sh accepts a public identity (TS 29.328 Table 7.6.1). |
| IP-SM-GW, TS 24.341 5.3.3.4.1 | A delivery report without In-Reply-To is dropped. | TS 24.341 5.3.2.4 has the UE send it. Finding the delivery by RP-MR alone would need a second key in the Store. |
| IP-SM-GW, TS 23.204 6.4 step 5 and 6.5a | The IP-SM-GW selects no domain: it delivers over IMS and nowhere else, and reports a delivery that failed instead of trying the CS or PS domain next. | This project has no MSC, SGSN, MME or SMSF for the IP-SM-GW to reach, so there is no second domain to try. |
| IP-SM-GW, TS 23.204 6.5a steps 17 and 18, TS 23.204 6.5 step 7 | The IP-SM-GW sends the HSS no report of a delivery that failed, and subscribes to no one-time notification that the UE is reachable again. It does keep its own flag for a UE it could not reach. | The Service Centre reports the same failure to the HSS, which is what records the waiting Service Centre there, and the IP-SM-GW learns of the UE's return from the subscription to its registration state that it holds anyway. |
| IP-SM-GW, RFC 6733 3 (End-to-End Identifier) and Appendix C | The IP-SM-GW does not recognise a short message that a Service Centre sends again over SGd after a failover. The repeat starts a second delivery, so the UE may get the short message twice, and each request is answered by how its own delivery ended. | The cache of requests already seen would have to be shared by every instance of the IP-SM-GW, as its deliveries are. |
| IP-SM-GW, TS 29.338 Table 6.2.2.1/2 | The answer to a short message that came over SGd carries no SMS-DELIVER-REPORT. | The IP-SM-GW keeps the outcome of a delivery, not the report itself, because the report may reach another instance than the one that answers. The element is optional, and the result code and the failure cause carry the same news. |
| IP-SM-GW, TS 29.338 Table 6.2.1.1/1 | The short message that the IP-SM-GW hands a Service Centre over SGd names the sender by MSISDN only, and not by the IMSI that the IP-SM-GW read when it registered for the user. | The boundary to the Service Centre carries the sender as the E.164 number, which is what the SMS-DELIVER needs as its originating address, and no node of this project reads the IMSI of such a request. |
| SC, TS 23.204 6.3 and 6.4, TS 23.040 3.2.1 and 3.2.6 | The stub of the SC, which an IP-SM-GW without a Service Centre of its own uses, speaks neither SGd and S6c nor MAP, chooses no domain between the IMS and the CS or PS domain, and does not hold a short message to try again: one that cannot be delivered at once is dropped, and the status report says so. It keeps no message waiting data and does not read the validity period. | The stub stands in for the Service Centre where the deployment runs none, as in [the interconnect guide](interconnect.md). The `sc` node of this network does all of it but the domain selection. |
| SC, TS 23.204 6.4 step 3 | In the stub, the routeing information query (SRI for SM, or S6c) is replaced by an Sh read of the public identities that have the number. | The stub knows the user only as a user of the IMS. The `sc` node sends the query itself. |
| SGd and S6c, TS 29.338 4.6 | The two interfaces run over TCP, as every Diameter connection of this project does, and not over the SCTP that the clause requires of them. | RFC 6733 2.1 lets the base protocol run over either, and TCP works on every machine that runs the containers of this project, which SCTP does not. |
| SC, TS 29.338 5.3.3.5 | The routing info query with which the Service Centre checks, before it takes a short message, that someone has the number does not carry `SM-Delivery-Not-Intended`, so the HSS reads it like the query of a delivery. | No node of this project acts on that element, and the check wants the same answer as a delivery would get. |
| SC, TS 23.040 3.2.7 | An alert finds the short messages that wait by the MSISDN it carries, the MSISDN-Alert of the user. | Each user of this project has one number, so there is no second one to hold a queue of its own. |
| SMS, TS 23.040 9.1.2.5 | An alphanumeric address (type of number 101), such as a sender name, is refused. | The Service Centre uses E.164 numbers only: it has no numbering plan to read any other kind of address with. |
| SMS, TS 23.040 9.2.2.4 | SMS-COMMAND is refused. | It asks the Service Centre about a message it holds, which no UE of this project does. |
| SMS, TS 23.040 9.2.3.12.2 and 9.2.3.12.3 | Only "no validity period" and the relative format are read and written; the absolute and enhanced formats are refused, which TS 23.040 9.2.3.3 allows. | The UE of this project asks for no validity period, so the Service Centre keeps every short message for its default of 72 hours. |
| SMS, TS 23.040 9.2.3.24.1 | No concatenated short messages: a text longer than one short message is refused. | This project sends one short message at a time. |
| SMS, TS 23.040 9.2.3.24 and TS 23.042 | A User-Data-Header, such as a national language table (TS 23.038 6.2.1.2), and compressed text are not decoded. | Nothing in this project sends them. |
| UE, TS 24.011 6.3.3.1 | The UE sends its notification that memory is available again (RP-SMMA) once. It runs neither timer TR1M, which waits for the RP-ACK, nor timer TRAM, after which a phone sends the notification again with a new RP-MR, so a notification that goes unanswered leaves the UE owing one. Only an RP-ACK clears what it owes: the UE does not tell a permanent cause in an RP-ERROR, which clears it on a phone (TS 23.040 10.3), from a temporary one, so any RP-ERROR leaves the UE owing a notification. | `POST /sms-memory/notify` on the control API sends the next notification, which is what the timer would have sent. |
| UE, RFC 3428 7 | Only short messages are taken; a MESSAGE with any other body, text/plain included, gets 415 (Unsupported Media Type). | The simulator is an SMS over IP UE, not an instant messaging client. |
| UE, TS 23.040 9.2.3.12 and 9.2.3.24 | The SMS-SUBMIT asks for no validity period and holds one short message, without a User-Data-Header. | The Service Centre then keeps it for its default validity period of 72 hours. |
| UE, TS 24.011 9.3.2 | A report whose RP-MR is not that of the RP-DATA is discarded, without the RP-ERROR with cause 81 that a phone would send. | Over IP there is no connection to send it on. |
| Sh, TS 29.328 6.1.3 and 6.1.4 | No subscriptions to notifications (SNR/SNA, PNR/PNA, commands 308 and 309). | The IP-SM-GW only registers itself and looks users up, and the MMTel AS reads its repository data for each initial request and keeps no copy. TS 29.364 8.1 recommends that such an AS subscribes. |
| Sh, TS 29.328 7.1.3 | No External-Identifier. | It is for machine-type communication, which this project does not serve. |
| Sh, TS 29.328 Annex D | Sh-Data has only PublicIdentifiers (IMSPublicIdentity, MSISDN), RepositoryData, and SCSCFName and SMSRegistrationInfo of Sh-IMS-Data. | The rest waits until a role needs it. |
| Sh, TS 29.328 7.6.16 | SMSRegistrationInfo has no SCAddress and no indication of SBI support. | The HSS hands no Service Centre address to the IP-SM-GW, which reaches the SC over Diameter only. |
| HSS, TS 29.338 5.3.1.2 | The alert travels back over the connection that the service centre opened to this HSS instance, rather than being routed to its Diameter identity by an agent. An instance the service centre is not connected to sends no alert. | The HSS reaches an S-CSCF the same way, and this project has no Diameter agent. The address stays in the message waiting data, so the next return of the user alerts it from whichever instance answers. |
| HSS and Sh, TS 23.040 3.2.6 items 2c and 2f, TS 23.204 6.5b step 2d and 6.6 step 3, TS 29.002 12.4 | The two notifications that the UE is reachable for short messages again and that it has memory again travel in an element of this project's own namespace, `<ReadyForSM>`, at the end of the `SMSRegistrationInfo` of an Sh update. A network with MAP carries both in the READY_FOR_SM service of MAP instead. | Sh has no element for either notification, and this project speaks no MAP. The schema of the Sh user profile takes an element of another namespace at that place and processes it lax, so an HSS that does not know the namespace skips the element. |
| HSS, TS 23.204 6.5a step 19 | The HSS acts on the delivery status report of the SMS-GMSC, which the clause has it ignore because the IP-SM-GW has already reported. | The IP-SM-GW of this project does not speak S6c, so the report of the SMS-GMSC is the only one the HSS gets. |
| HSS, TS 23.040 3.2.6 items 1a and 1b | An absent user sets the flag for a UE not reachable over IP whichever node reported it, rather than one flag for a report from an MSC and another for one from an SGSN. | The HSS names no such serving node, so only a delivery through the IP-SM-GW can fail. |
| HSS, TS 29.338 5.2.1.3 | The routing info query is answered without the checks for an MT SMS teleservice subscription and for barring of mobile terminated short messages, so the answer is never DIAMETER_ERROR_SERVICE_NOT_SUBSCRIBED or DIAMETER_ERROR_SERVICE_BARRED. | Every subscriber of this HSS may receive short messages, and it keeps no operator determined barring. |
| HSS, TS 29.338 Table 5.2.1.1-2 | A successful routing info answer leaves the IMSI out for a subscription whose private identity is not derived from one, such as the one a softphone registers by its number. An Sh read of the IMSI answers such a subscription with no user data at all. | The HSS keeps no IMSI of its own. It reads the IMSI of TS 23.003 13.3 out of the private identity. |
| HSS, TS 23.204 6.4 steps 3a to 3c | The HSS answers the routing info query itself, instead of forwarding it to the IP-SM-GW, which would answer with its own address and an MT Correlation ID. | Forwarding is what makes a short message from another network pass through the home network of the recipient, which this project does not do, and the HSS has the IP-SM-GW's Diameter identity from the registration. |
| HSS, TS 29.329 7.1 | No Supported-Features negotiation: the HSS acts as for an AS that supports none of the optional features, so a request names one Data-Reference, and for repository data one Service-Indication or one RepositoryData, and an answer leaves User-Data out when the HSS has none of the data. | The IP-SM-GW of this project announces no optional feature. |
| HSS, TS 29.328 6.2 | No permissions per AS: every peer that connected may use Sh. | The IP-SM-GW is the only AS of this project. |
| HSS, TS 29.328 6.1.1.1 and 6.1.2.1 step 2a | The private identity in User-Name is not read or checked. | The IP-SM-GW does not send it. |
| HSS, TS 29.328 7.6.9 | The MSISDN is the first tel URI among the public identities, not data of its own. | The configuration then states each number once, and the user is found by MSISDN through the tel URI. |
| HSS, TS 29.328 7.6.2 | IMPLICIT_IDENTITIES and ALIAS_IDENTITIES return the same set as ALL_IDENTITIES, and are not refused when the user is named by MSISDN. | Each test subscription is one implicit registration set with one service profile. |
| User profile, TS 29.228 Annex E | One ServiceProfile, with no optional element but BarringIndication, the `IdentityType` and `WildcardedPSI` of a public service identity, and the criteria, and without the schema's XML namespace. | The test subscriptions and PSI users need nothing more. |
| iFC, TS 29.228 Annex E and B.2.3 | No SessionDescription SPT (refused), and ProfilePartIndicator, RegistrationType, IncludeRegisterRequest and IncludeRegisterResponse are skipped; a REGISTER SPT matches every REGISTER. | The iFCs of SMS and of the MMTel AS need none of them. RegistrationType is optional for the S-CSCF. |
| S-CSCF, TS 24.229 5.4.0 | The S-CSCF looks the AS up in DNS for every request, rather than keeping the address that the first third-party REGISTER went to. | Each AS role keeps its state in a Store that all its nodes share, so any node can take the next request. |
| S-CSCF, TS 24.229 5.4.1.7 item c | The To of the third-party REGISTER is always the default public identity. Item i has nothing to copy, and items j, l and m are for features this project does not have. | The operator may choose the identity. The HSS stub sends no charging addresses. |
| S-CSCF, RFC 3261 10.2 | Each third-party REGISTER has a new Call-ID. | One Call-ID per user and AS would be more state in the Store; a registrar accepts a new one (RFC 3261 10.3 step 7). |
| S-CSCF, TS 24.229 5.4.1.7 | The term-ioi of the answer to a third-party REGISTER is not stored. | It is for the charging records of the S-CSCF, which no node of this project writes. |
| S-CSCF, TS 24.229 5.4.3.2 and 5.4.3.3 | A 100 (Trying) from the AS does not count as the 1xx that turns the default handling off. | Proxy keeps a 100 to the hop it came over (RFC 3261 16.7 step 5). |
| S-CSCF, RFC 3841 7.2.4 and 9.1, TS 24.229 5.4.3.3 step 10 a | The target set is the one contact kept for a private identity, so matching only decides whether it stays. Qa and q-values are not computed, and Request-Disposition is not acted on. | With one contact, every directive reaches the same contact. |
| S-CSCF, RFC 3841 8 | A negated value (`!x`) and a numeric one (`#>=4`) in a feature tag are compared as plain tokens. | SMS over IP and the tags of TS 24.229 7.9 use neither. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
