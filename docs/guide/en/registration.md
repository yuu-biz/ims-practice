# Follow a UE registering with the IMS

[日本語](../ja/registration.md)

Before a phone can make a call or send an SMS over IMS, it registers. Registration tells the home network where the phone is, proves who the user is, and sets up the route that later requests take. This guide runs one registration and walks through every message in the capture.

The guide follows 3GPP Release 18 (TS 24.229 V18.11.0, TS 29.228 V18.0.0, TS 29.229 V18.1.0). Clause numbers below refer to those editions.

## Before you start

- You can start the system and open a capture, as in [Capture signaling and open it in Wireshark](capture.md).
- The abbreviations are explained in the [glossary](../../glossary.md).

## The nodes

| Node | Address | What it does in registration |
| --- | --- | --- |
| UE simulator | 172.28.0.100 | The phone. It sends REGISTER and answers the challenge. |
| P-CSCF | 172.28.0.40 | The first SIP hop of the UE. It finds the home network and adds Path. |
| I-CSCF | 172.28.0.20 | The entry point of the home network. It asks the HSS which S-CSCF serves the user. |
| S-CSCF | 172.28.0.30 | The registrar. It authenticates the user and stores the registration. |
| HSS | 172.28.0.10 | The subscriber database. It answers the I-CSCF and the S-CSCF over Diameter Cx, and the IP-SM-GW over Sh. |
| IP-SM-GW | 172.28.0.70 | The application server of SMS over IP. The S-CSCF registers the user there too (step 15). |
| DNS | 172.28.0.2 | It holds the NAPTR, SRV and A records of the home network domain. |

The home network domain is `ims.mnc001.mcc001.3gppnetwork.org`. The test user has the private identity `001010000000001@ims.mnc001.mcc001.3gppnetwork.org` and the public identity `sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org`. The password is `test-password-1`.

The UE of this guide is a softphone without a SIM card. Its identities and its password are configured in it, as an IMC holds them (TS 24.229 5.1.1.1B.1), so it registers the public identity it was given and uses that identity in its other requests too. The test data give that identity the form of one derived from an IMSI, but it is a configured identity and is not barred. A VoLTE phone whose card has a USIM and no ISIM registers a temporary public identity derived from the IMSI instead, which the network bars: see [The identities of a phone without an ISIM](volte.md#the-identities-of-a-phone-without-an-isim).

## Run a registration

1. Start the system at the root of the repository:

   ```sh
   docker compose up -d --build
   ```

2. Wait until the I-CSCF and the S-CSCF have opened their Diameter connections to the HSS. A node that starts before the HSS accepts connections tries again after 30 seconds (Tc, RFC 6733 2.1), so allow up to a minute. `docker compose logs scscf | grep I-Open` prints a line once that has happened.
3. Register the UE, and deregister it 5 seconds later:

   ```sh
   docker compose run --rm ue register --deregister-after 5s
   ```

   The last log lines show `"msg":"registered"`, with the Service-Route and the associated identities, then `"msg":"reg event"` with `"state":"active"`, and then `"msg":"deregistered"`.

4. To see a refused registration, use a wrong password:

   ```sh
   docker compose run --rm ue register --password wrong-password
   ```

   The UE ends with `registration refused with 403`.

Without `--deregister-after`, the UE stays registered and re-registers before the registration expires, until you stop it with Ctrl+C.

## Open the capture

Open the newest file in `captures/` in Wireshark and use this display filter:

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

It shows the SIP messages, the Cx messages, and the NAPTR and SRV lookups, and hides the Diameter watchdogs. **Statistics > Flow Graph** with **Limit to display filter** draws the ladder below.

## The sequence

```text
 UE           P-CSCF         DNS          I-CSCF          S-CSCF          HSS
 |  REGISTER    |             |              |               |              |
 |------------->|  NAPTR, SRV, A             |               |              |
 |  (1)         |------------>|              |               |              |
 |              |  REGISTER (2)              |               |              |
 |              |--------------------------->|  UAR (3)      |              |
 |              |             |              |----------------------------->|
 |              |             |              |  UAA FIRST_REGISTRATION      |
 |              |             |              |<-----------------------------|
 |              |             |              |  REGISTER (4) |              |
 |              |             |              |-------------->|  MAR (5)     |
 |              |             |              |               |------------->|
 |              |             |              |               |  MAA H(A1)   |
 |              |             |              |  401 (6)      |<-------------|
 |  401         |  401        |              |<--------------|              |
 |<-------------|<---------------------------|               |              |
 |  REGISTER with the digest response (7)    |               |              |
 |------------->|--------------------------->|  UAR, UAA SUBSEQUENT_REGISTRATION (8)
 |              |             |              |<---------------------------->|
 |              |             |              |  REGISTER     |              |
 |              |             |              |-------------->|  SAR, SAA (9)|
 |              |             |              |               |<------------>|
 |              |             |              |  200 OK (10)  |              |
 |  200 OK      |  200 OK     |              |<--------------|              |
 |<-------------|<---------------------------|               |              |
```

### 1. The UE sends the first REGISTER

TS 24.229 5.1.1.2.1 and 5.1.1.2.3. Look at these headers:

- **Request-URI** is the home network domain, not a server. The network decides which server handles it.
- **From** and **To** are the public identity being registered.
- **Contact** is the address of the UE. Its user part is a UUID, not one of the user's identities. TS 24.229 5.1.1.2.1 item c asks for a user part that is globally unique and reveals nothing private, and its NOTE 3 gives a time-based UUID as an example. The UE derives its UUID instead: HMAC-SHA-256 of its private identity, keyed with its password (with K of the USIM for IMS AKA), written as a version 8 UUID (RFC 9562 5.8). Nobody without the key can work out the identity from it. The UE gets the same user part each time it starts, so its registration after a restart refreshes the binding instead of replacing it. The UE keeps the same user part in its later REGISTERs (5.1.1.4.1). The Contact has two parameters. `+g.3gpp.smsip` is a feature tag (RFC 3840): it says that the UE can receive short messages over IP (TS 24.341 5.3.2.2). `expires=600000` is the interval TS 24.229 has the UE ask for.
- **Authorization** already carries the private identity in `username`, with an empty `nonce` and `response`. The UE has no challenge to answer yet, but this tells the network which private identity to authenticate.
- **Via** has `rport` without a value. It asks the P-CSCF to send the response back to the address and port the request really came from (RFC 3581).

### 2. The P-CSCF finds the I-CSCF and forwards the REGISTER

The P-CSCF looks up the domain of the Request-URI in DNS, as RFC 3263 describes:

1. **NAPTR** `ims.mnc001.mcc001.3gppnetwork.org` says SIP over UDP is offered at `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org`.
2. **SRV** `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org` gives port 5060 on `icscf.ims.mnc001.mcc001.3gppnetwork.org`.
3. **A** `icscf.ims.mnc001.mcc001.3gppnetwork.org` gives 172.28.0.20.

The records are in `deploy/compose/dns/ims.mnc001.mcc001.3gppnetwork.org.zone`.

When the I-CSCF runs as several instances, the SRV records give one target for each, best first. The P-CSCF sends the REGISTER to the first. When that I-CSCF cannot be reached, gives no response before Timer F, or answers 3xx, 480 or 503, the P-CSCF sends the REGISTER to the next one, in a new transaction with a new branch in its Via, and answers 504 when none is left (TS 24.229 5.2.2.1 item 7, RFC 3263 4.3). Every SIP node here does the same with the targets of any other request, but sends it on only after a 503, a transport error, or no response at all before Timer B or Timer F (RFC 3263 4.3). The CANCEL of an INVITE, and the ACK for a 300-699 response to it, go to the target that took the INVITE. In this network every name leads to one server, so the capture shows the first target only. Timer B and Timer F last 32 seconds by default. `transaction_timeout` in the `sip` section of a role in the node's configuration shortens them, so that a request sent to a server that has stopped moves on to the next target sooner.

Before it forwards the REGISTER, the P-CSCF adds these headers (TS 24.229 5.2.2.1):

- **Path** with its own URI, `sip:term@pcscf...`. Requests to this UE, such as the INVITE of an incoming call, come back through the P-CSCF along this path (RFC 3327).
- **Require: path**, so that the registrar must support Path.
- **P-Charging-Vector** with a new charging identifier (`icid-value`).
- **P-Visited-Network-ID**, the name of the network the P-CSCF is in.

It also adds `received` and `rport` to the UE's Via, which record where the request came from.

### 3. The I-CSCF asks the HSS: UAR and UAA

The I-CSCF does not know which S-CSCF serves this user, so it asks the HSS with a **User-Authorization-Request** (UAR, Cx command 300). TS 24.229 5.3.1.2 and TS 29.228 6.1.1.

In Wireshark, open the Diameter part and look at `User-Name` (the private identity), `Public-Identity`, `Visited-Network-Identifier`, and `User-Authorization-Type`.

The HSS answers **DIAMETER_FIRST_REGISTRATION** (Experimental-Result-Code 2001, TS 29.229 6.2.1.1). It means no S-CSCF is assigned yet. Instead of a name, the answer carries **Server-Capabilities** (here, mandatory capability 1). The I-CSCF picks a configured S-CSCF that has every mandatory capability (TS 29.228 6.7).

Note that the 2001 here is inside **Experimental-Result**, not in Result-Code. The same number in Result-Code means DIAMETER_SUCCESS. The container tells them apart.

### 4. The I-CSCF forwards the REGISTER to the S-CSCF

The I-CSCF sets the Request-URI to the chosen S-CSCF, `sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060`, and forwards the request. It adds its own Via on top. The REGISTER now carries three Vias: the UE's, the P-CSCF's and the I-CSCF's. Each response travels back down that list.

### 5. The S-CSCF asks for authentication data: MAR and MAA

The S-CSCF sends a **Multimedia-Auth-Request** (MAR, Cx command 303). TS 24.229 5.4.1.2.1 and TS 29.228 6.3.

`3GPP-SIP-Authentication-Scheme` in the MAR is `Unknown`. The REGISTER had no `integrity-protected` parameter, so the S-CSCF leaves the choice of scheme to the HSS (TS 24.229 5.4.1.1 item 9).

The HSS answers with the scheme `SIP Digest` and a **SIP-Digest-Authenticate** AVP that holds:

- `Digest-Realm`, the realm,
- `Digest-Algorithm` `MD5`,
- `Digest-QoP` `auth`,
- `Digest-HA1`, the hash H(A1) of username, realm and password (RFC 2617 3.2.2.2).

The HSS never sends the password. The S-CSCF can check the UE's answer with H(A1) alone.

The HSS also stores this S-CSCF for the user and marks the authentication as pending. That matters in step 8.

### 6. The S-CSCF challenges the UE: 401

The S-CSCF answers **401 Unauthorized** with a **WWW-Authenticate** header: the realm, a `nonce` it generated, `algorithm=MD5` and `qop="auth"` (TS 24.229 5.4.1.2.1B). It starts the timer **reg-await-auth**, 4 minutes (TS 24.229 Table 7.7.1).

The 401 goes back through the I-CSCF and the P-CSCF. Each removes its own Via, so the UE gets the response with only its own Via left.

### 7. The UE answers the challenge

The UE computes the digest response from its password and the nonce (RFC 2617 3.2.2.1) and sends a second REGISTER (TS 24.229 5.1.1.5.4):

- The **Call-ID** is the same as in the first REGISTER, and **CSeq** goes up by one.
- **Authorization** now holds the nonce, `cnonce`, `nc=00000001`, `qop=auth` and the `response`.

The P-CSCF adds `integrity-protected="ip-assoc-pending"` to the Authorization header (TS 24.229 5.2.2.3). It tells the S-CSCF that this REGISTER answers a challenge and that no IP association exists yet.

### 8. The I-CSCF asks the HSS again

Every REGISTER goes through the I-CSCF, and the I-CSCF keeps no registration state, so it sends another UAR. This time the HSS answers **DIAMETER_SUBSEQUENT_REGISTRATION** (2002) with the **Server-Name** of the S-CSCF it stored in step 5 (TS 29.228 6.1.1.1). The request goes to the same S-CSCF.

With the credentials in it, this REGISTER is larger than 1300 bytes when the I-CSCF forwards it. RFC 3261 18.1.1 keeps a request of that size off UDP when the path MTU is unknown, so the I-CSCF sends it over TCP: it opens a connection to port 5060 of the S-CSCF, and its Via reads `SIP/2.0/TCP`. The S-CSCF sends the responses back on that connection (RFC 3261 18.2.2). The I-CSCF logs `request larger than 1300 bytes sent over TCP`. Only the size decides, so a shorter request, such as the REGISTER of step 4, may still go over UDP.

### 9. The S-CSCF checks the answer and tells the HSS: SAR and SAA

The S-CSCF checks that the Call-ID matches the challenged REGISTER, that the realm and the nonce are its own, and that the response matches the one it computes from H(A1) (TS 24.229 5.4.1.2.2A).

It then sends a **Server-Assignment-Request** (SAR, Cx command 301) with `Server-Assignment-Type` **REGISTRATION** (1). The HSS records the user as registered at this S-CSCF and returns the user profile in **User-Data** (Wireshark: `Cx-User-Data`). The profile is an XML document (TS 29.228 Annex E) that lists the identities registered together. It also carries the initial filter criteria (iFCs), which tell the S-CSCF which application servers to involve in the user's requests. `deploy/compose/hss.yaml` gives every user the two iFCs of SMS over IP. Step 15 shows what the first one does with the REGISTER.

### 10. The S-CSCF accepts: 200 OK

The 200 OK carries what the UE and the P-CSCF need afterwards (TS 24.229 5.4.1.2.2F):

- **Path**, the path the S-CSCF stored.
- **Service-Route**, `sip:orig@scscf...`. The UE puts it in front of its later requests, so that they reach this S-CSCF (RFC 3608).
- **P-Associated-URI**, the identities registered together, without any that the HSS bars. The first one is the default identity.
- **Contact** with `+g.3gpp.smsip` and `expires=3600`. The S-CSCF keeps the parameters of the contact with the binding, and lists them here. Later it matches the caller preferences of a request against them (RFC 3841, TS 24.229 5.4.3.3): a short message from the IP-SM-GW asks for a contact with this feature tag. The UE asked for 600000 seconds; the S-CSCF shortened it to its configured maximum.
- **Authentication-Info** with `rspauth`. It proves that the S-CSCF knows H(A1) too (RFC 2617 3.2.3). The UE checks it.

When the P-CSCF sees the 200 OK, it creates the IP association for the UE's address and stores the Service-Route and the identities (TS 24.229 5.2.2.1 and 5.2.2.3).

## Subscribe to the registration state

After the registration, the P-CSCF and the UE ask the S-CSCF to report every change of the user's registration. They use the reg event package: a SUBSCRIBE with `Event: reg`, answered by NOTIFYs that carry the registration state as an XML document (RFC 3680, RFC 6665). This is how they learn that a registration has ended even when the UE did not end it, for example when it expired.

```text
 P-CSCF                       I-CSCF          S-CSCF          HSS
 |  SUBSCRIBE Event: reg (11)  |               |              |
 |---------------------------->|  LIR, LIA     |              |
 |                             |<---------------------------->|
 |                             |  SUBSCRIBE    |              |
 |                             |-------------->|              |
 |  200 OK                     |  200 OK       |              |
 |<----------------------------|<--------------|              |
 |  NOTIFY (12)                                |              |
 |<--------------------------------------------|              |
 |  200 OK                                     |              |
 |-------------------------------------------->|              |
```

### 11. The P-CSCF subscribes: SUBSCRIBE

Right after it passes the 200 OK to the UE, the P-CSCF sends a SUBSCRIBE (TS 24.229 5.2.3). It subscribes once per private identity, after the first registration. A re-registration does not repeat it: the P-CSCF refreshes the subscription 600 seconds before it expires, within its dialog and straight to the S-CSCF.

- **Request-URI** and **To** are the default public identity, the first P-Associated-URI of the 200 OK.
- **From** is the P-CSCF's own URI, `sip:pcscf...:5060`.
- **Event** is `reg`.
- **Expires** is 3761. It must be longer than the 3600 seconds of the registration.
- **P-Asserted-Identity** is the URI that the P-CSCF put in Path, `sip:term@pcscf...`. The S-CSCF checks the subscriber with it.

The P-CSCF is in the home network, so the SUBSCRIBE goes to the I-CSCF, which DNS gives as for the REGISTER. The I-CSCF asks the HSS where the user is with a **Location-Info-Request** (LIR, Cx command 302, TS 29.228 6.1.4). It forwards the SUBSCRIBE to the S-CSCF that the answer names.

The S-CSCF accepts the subscription if the user has a binding (otherwise 480), and if the subscriber is the user, the P-CSCF in the Path of the registration, or an application server of the user's iFCs (otherwise 403) (TS 24.229 5.4.2.1.1). It answers 200 OK with Expires and its Contact. The 200 creates a dialog between the P-CSCF and the S-CSCF. It is a dialog without a call (RFC 6665 4.4.1).

### 12. The S-CSCF reports the state: NOTIFY

The S-CSCF sends a NOTIFY at once (RFC 6665 4.2.1.2). The I-CSCF did not stay on the route, so the NOTIFY goes straight to the Contact of the P-CSCF. Look at these headers:

- **Event** is `reg`.
- **Subscription-State** is `active;expires=3761`.
- **Content-Type** is `application/reginfo+xml`.

Wireshark shows the body as XML:

```xml
<reginfo xmlns="urn:ietf:params:xml:ns:reginfo" version="0" state="full">
  <registration aor="sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
    <contact id="..." state="active" event="registered" expires="3600" callid="..." cseq="2">
      <uri>sip:4218b61b-4e20-8e1c-872d-5b39fca1e64f@172.28.0.100:5060</uri>
      <unknown-param name="+g.3gpp.smsip"></unknown-param>
    </contact>
  </registration>
</reginfo>
```

There is one `registration` for each identity registered together, and one `contact` for each binding. Each parameter of the Contact that RFC 3261 does not define, such as the feature tag, is an `unknown-param` (RFC 3680 5.1, TS 24.229 5.4.2.1.2). `version` counts the documents of the subscription from 0. The S-CSCF always sends the full state (TS 24.229 5.4.2.1.2). The P-CSCF answers 200 OK.

The S-CSCF sends a NOTIFY again whenever the registration changes. After a re-registration, the contact has `event="refreshed"`.

The UE subscribes too, and its subscription takes another path:

```text
 UE           P-CSCF                        S-CSCF
 |  SUBSCRIBE Event: reg (13)  |              |
 |------------->|  SUBSCRIBE                  |
 |              |---------------------------->|
 |  200 OK      |  200 OK                     |
 |<-------------|<----------------------------|
 |  NOTIFY      |  NOTIFY (14)                |
 |<-------------|<----------------------------|
 |  200 OK      |  200 OK                     |
 |------------->|---------------------------->|
```

### 13. The UE subscribes: SUBSCRIBE

After the 200 OK to its registration, the UE subscribes to its own registration state (TS 24.229 5.1.1.3):

- **Request-URI**, **From** and **To** are the default public identity.
- **Event** is `reg`, and **Expires** is 600000, the value that 5.1.1.3 gives the UE.
- **Route** is the P-CSCF and then the Service-Route, as in every request of the UE.

The P-CSCF treats the SUBSCRIBE like any request from the UE: it adds P-Asserted-Identity with the user's identity, and Record-Route to stay on the path of the dialog. The SUBSCRIBE reaches the S-CSCF along the Service-Route, without the I-CSCF. The S-CSCF accepts it because P-Asserted-Identity is the user's own identity (TS 24.229 5.4.2.1.1), and answers 200 OK.

### 14. The NOTIFY reaches the UE through the P-CSCF

The NOTIFY carries a Route with the P-CSCF, which the Record-Route of the SUBSCRIBE put there. The P-CSCF adds its Record-Route to the NOTIFY as well (TS 24.229 5.2.6.4.5). The UE takes the route of the subscription from the NOTIFY, not from the 200 OK (RFC 6665 4.4.1), so its later SUBSCRIBEs also pass the P-CSCF. The body is the same reginfo as in step 12. The UE logs `"msg":"reg event"` with the state, and refreshes the subscription 600 seconds before it expires (TS 24.229 5.1.1.3).

## Register the user at the application server: third-party REGISTER

The first iFC of the profile matches every REGISTER, and names the IP-SM-GW, the application server of SMS over IP. So once the UE has its 200 OK, the S-CSCF registers the user at the IP-SM-GW too. The IP-SM-GW then subscribes to the registration state, as the P-CSCF does, and tells the HSS over Sh that it takes the user's short messages. `diameter.cmd.code == 307` shows the Sh messages of this flow.

```text
 S-CSCF            IP-SM-GW          I-CSCF            HSS
 |  REGISTER (15)    |                 |                 |
 |------------------>|                 |                 |
 |  200 OK           |                 |                 |
 |<------------------|                 |                 |
 |                   |  SUBSCRIBE (16) |                 |
 |                   |---------------->|  LIR, LIA       |
 |                   |                 |<--------------->|
 |  SUBSCRIBE        |                 |                 |
 |<------------------------------------|                 |
 |  200 OK           |                 |                 |
 |------------------------------------>|  200 OK         |
 |  NOTIFY (17)      |<----------------|                 |
 |------------------>|                 |                 |
 |  200 OK           |                 |                 |
 |<------------------|  PUR, PUA (18)  |                 |
 |                   |<--------------------------------->|
```

### 15. The S-CSCF registers the user at the IP-SM-GW: REGISTER

The S-CSCF matches the iFCs against the REGISTER of the UE. Each one that matches gets a REGISTER of the S-CSCF's own, a third-party REGISTER (TS 24.229 5.4.1.7):

- **Request-URI** is the ServerName of the iFC, `sip:ipsmgw...:5060`.
- **From** and **Contact** are the S-CSCF, and **To** is the default public identity.
- **Expires** is what the UE got, 3600.
- The body, `application/3gpp-ims+xml`, holds the ServiceInfo of the iFC in `<service-info>` (5.4.1.7A). Here it is the MSISDN, `12025550101`: `hss.yaml` writes `{msisdn}`, and the HSS puts in the number of each user.

The IP-SM-GW answers 200 OK with the binding in Contact (RFC 3261 10.3, as table B.3-2 of TS 24.341 shows). The UE does not wait for this: it has its 200 OK already. When the IP-SM-GW does not answer, the DefaultHandling of the iFC decides. Here it is SESSION_CONTINUED, and the registration stays.

### 16. The IP-SM-GW subscribes: SUBSCRIBE

The IP-SM-GW subscribes to the registration state of the user in To (TS 24.341 5.3.3.2, TS 24.229 5.7.1.1). The SUBSCRIBE looks like the P-CSCF's of step 11:

- **Request-URI** and **To** are the public identity, and **From** and **Contact** the IP-SM-GW.
- **Expires** is 3761, the 3600 seconds of the registration and the 161 seconds that RFC 3680 4.4 puts between the two defaults.
- **P-Asserted-Identity** is the URI of the IP-SM-GW. The S-CSCF accepts the subscription because it is the ServerName of an iFC of the user (TS 24.229 5.4.2.1.1).

Like every request of the IP-SM-GW, the SUBSCRIBE goes to the entry point of the home network: the I-CSCF finds the S-CSCF with an LIR. Each re-registration brings another third-party REGISTER, and the IP-SM-GW then refreshes the subscription within its dialog, straight to the S-CSCF.

### 17. The S-CSCF reports the state: NOTIFY

The S-CSCF sends the NOTIFY at once, as in step 12, to the Contact of the IP-SM-GW. The 200 OK to the SUBSCRIBE comes back through the I-CSCF, so the NOTIFY may arrive first. That is allowed: the NOTIFY, not the 200 OK, creates the dialog of a subscription (RFC 6665 4.4.1).

The IP-SM-GW looks in the reginfo for an active contact with `<unknown-param name="+g.3gpp.smsip">`. That feature tag says that the UE can receive short messages over IP (TS 24.341 5.3.2.2).

### 18. The IP-SM-GW tells the HSS: PUR and PUA

The user can now receive short messages through the IP-SM-GW, so the IP-SM-GW tells the HSS (TS 24.341 5.3.3.2). It sends a **Profile-Update-Request** (PUR, Sh command 307, TS 29.328 6.1.2) with these AVPs:

- **User-Identity** with the **MSISDN** of the third-party REGISTER. Wireshark shows it as digits, `12025550101`.
- **Data-Reference** **SMSRegistrationInfo** (24).
- **Sh-User-Data**, an XML document with the number of the IP-SM-GW, `12025550199`, in `<IP-SM-GW-Number>`.

The HSS answers with a **Profile-Update-Answer** (PUA) with DIAMETER_SUCCESS (2001). TS 24.341 describes this update in MAP; this project uses Sh.

A UE that registers without `+g.3gpp.smsip` gets no PUR: the NOTIFY tells the IP-SM-GW that no contact of the user takes short messages.

## Deregistration

The UE sends a REGISTER with `expires=0` in Contact (TS 24.229 5.1.1.6.1).

- The P-CSCF finds the IP association and adds `integrity-protected="ip-assoc-yes"`.
- The I-CSCF sends a UAR with `User-Authorization-Type` **DE_REGISTRATION** (1).
- The S-CSCF needs no new challenge because of the IP association (TS 24.229 5.4.1.4.1). It sends a SAR with **USER_DEREGISTRATION** (5), and once the SAA is back it answers 200 OK with `expires=0` (TS 23.228 5.3.1).
- After the 200 OK, the S-CSCF sends the last NOTIFY on each subscription. The registration and the contact are `terminated`, the contact has `event="unregistered"`, and Subscription-State is `terminated;reason=noresource`. The subscriptions and their dialogs end with these NOTIFYs (TS 24.229 5.4.2.1.2).
- The IP-SM-GW gets such a NOTIFY too, and sends a PUR with an empty `<IP-SM-GW-Number>`, which takes the IP-SM-GW out of the HSS. The S-CSCF also sends the IP-SM-GW a third-party REGISTER with Expires 0, which the IP-SM-GW answers with 200 OK.

Once its deregistration has succeeded, the UE treats its subscription as ended (TS 24.229 5.1.1.6.1). It still answers its last NOTIFY if that comes within a second. The P-CSCF passes that NOTIFY on: it takes no more requests from the UE, but keeps the IP association until the server transaction of the deregistration is over, 32 seconds over UDP (5.2.5.1 item 2).

If the UE does not re-register in time, the registration expires. The S-CSCF sends a NOTIFY with `event="expired"`, and then a SAR with **TIMEOUT_DEREGISTRATION** (4). It looks for the registrations whose time has come once a second, so the two messages may be that much later than the expiry itself. On that NOTIFY, the P-CSCF ends the IP association of the UE the same way, so that the NOTIFY to the UE still gets through (TS 24.229 5.2.5.2 and its NOTE 2). The UE learns that it is no longer registered. `ue register` then exits with an error; `ue call` and `ue answer` only log it, and `ue serve` stays unregistered until it is asked to register. If the S-CSCF itself ends a registration with `event="deactivated"`, the UE registers again, as TS 24.229 5.1.1.7 asks.

## Remove a subscription: deregistration by the HSS

The HSS has a provisioning API over HTTP, which listens where the hss key `provisioning_listen` says, for example `:8081`. The API is this project's own, not a 3GPP interface. Product HSSs have a provisioning interface too, each one its own. The JSON fields have the names of the configuration keys.

| Request | What it does |
| --- | --- |
| `GET /subscriptions` | Lists the subscriptions with their registration state, one page at a time. A page holds 1000 of them, or the smaller number that `?limit=` asks for. `?after={private identity}` starts the page after that identity, in the order the identities sort in. While pages are left, the answer carries the query of the request for the next one in a `Link` header with `rel="next"` (RFC 8288), such as `<?after=001010000000001%40ims.mnc001.mcc001.3gppnetwork.org&limit=2>; rel="next"`. The HSS reads a page from its index of the private identities, so a page of a network with a million subscriptions costs what a page of this one costs. |
| `GET /subscriptions/{private identity}` | Shows one subscription. |
| `POST /subscriptions` | Adds one: `{"private_identity": "...", "public_identities": ["sip:...", "tel:..."], "password": "..."}`. An optional `"tel": "tel:+..."` adds that tel URI to the public identities. For IMS-AKA, `"aka": {"k": "...", "opc": "...", "amf": "...", "sqn": "..."}` in hexadecimal goes with the password or in its place. `"ut_password": "..."` lets the user change the supplementary services over Ut; the HSS keeps only H(A1) of it, as Sh repository data. `"mmtel_services": "<MMTelServices ...>"` gives the first supplementary service document; without it the subscription gets the one of the hss key `mmtel_services`. |
| `DELETE /subscriptions/{private identity}` | Removes one. |

The answers never contain the password or the AKA keys (K and OPc). They list the authentication schemes that the subscription allows in `authentication_schemes`. A new subscription follows the same rules as one in the configuration file: 400 for a missing field or a public identity that is not a SIP or tel URI, 409 for an identity that already belongs to another subscription.

Removing the subscription of a registered user also deregisters the user at the S-CSCF. In Wireshark, `diameter.cmd.code == 304` shows the Cx messages of this flow.

```text
 UE           P-CSCF                         S-CSCF          HSS
 |             |                               |               |  DELETE /subscriptions/...
 |             |                               |  RTR (1)      |
 |             |                               |<--------------|
 |             |  NOTIFY event="rejected" (2)  |               |
 |  NOTIFY     |<------------------------------|               |
 |<------------|                               |  RTA (3)      |
 |             |                               |-------------->|
```

1. The HSS removes the subscription and sends a **Registration-Termination-Request** (RTR, Cx command 304) to the S-CSCF that registered the user (TS 29.228 6.1.3). Destination-Host is the Origin-Host of that S-CSCF's SAR, User-Name is the private identity, and **Deregistration-Reason** holds Reason-Code **PERMANENT_TERMINATION** (0) (TS 29.229 6.3.16 and 6.3.17). There is no Public-Identity: the private identity goes with all of its public identities (TS 29.228 6.1.3.1).
2. The S-CSCF removes the registration and sends the last NOTIFY on each subscription (TS 24.229 5.4.1.5). The registration and the contact are `terminated`, and the contact has `event="rejected"`: PERMANENT_TERMINATION means that no other S-CSCF can be assigned, so registering again would not help. The P-CSCF ends the IP association as for an expired registration.
3. The S-CSCF answers with a **Registration-Termination-Answer** (RTA) with DIAMETER_SUCCESS (2001). It sends no SAR, because the HSS has already deregistered the user (TS 29.228 A.4.4.2). An RTR for a user that the S-CSCF does not know gets DIAMETER_ERROR_USER_UNKNOWN (5001, TS 29.229 6.2.2.1).

On `event="rejected"` the UE does not register again (TS 24.229 5.1.1.7), and `ue register` exits with an error. A new `ue register` gets **403 Forbidden**: the HSS answers the UAR with DIAMETER_ERROR_USER_UNKNOWN (TS 29.228 6.1.1.1), and the I-CSCF turns a negative UAA into 403 (TS 24.229 5.3.1.3).

## A wrong password

With a wrong password, steps 1 to 8 are the same. In step 9 the response does not match. The S-CSCF answers **403 Forbidden** (TS 24.229 5.4.1.2.3B) and sends a SAR with **AUTHENTICATION_FAILURE** (9). The HSS clears the S-CSCF it had stored (TS 29.228 6.1.2.1), so the next attempt starts again with FIRST_REGISTRATION.

## A registration that fails

A registered UE refreshes its registration before it expires (TS 24.229 5.1.1.4.1). When the refresh gets 408, 500, 504 or 403, the UE no longer counts on the registration it had and makes an initial registration: a REGISTER like the first one, which the S-CSCF challenges again (5.1.1.4.1). The UE simulator does so after any failed refresh. When the response carries Retry-After, the UE first waits that long: for that time it makes no initial registration through the same P-CSCF (5.1.1.2.1).

When an initial registration fails as well, the UE does not try again at once. Before each new attempt it waits the retry delay of RFC 5626 4.5 (TS 24.229 5.1.1.2.1):

- The upper bound W is 30 seconds times 2 to the power of the number of failures in a row, and at most 1800 seconds.
- The UE waits a random time from half of W to all of W. The first retry comes 30 to 60 seconds after the first failure, the second 60 to 120 seconds after the second, and from the sixth failure on the UE tries every 15 to 30 minutes.
- A response with Retry-After asks for its own wait instead. After it, the count of failures starts again.

The random part spreads the attempts of UEs that lost their registration at the same moment, so that they do not all come back at once. `ue serve` and `ue register` keep trying until they are registered again. Each failure is logged as `"msg":"registration failed"`, with `retry_after_ms`, the wait before the next attempt. `ue serve` also waits and tries again when its first registration fails. The other commands end with an error then, as `ue register` does above for a removed subscription.

## A request without an IP association

The P-CSCF serves only the UEs that have registered through it. It discards a request other than REGISTER that does not map to an IP association, such as a request from a UE whose registration the P-CSCF has lost: it sends nothing back, not even a 100 Trying, and forwards nothing to the S-CSCF (TS 24.229 5.2.6.3.2A). A response would tell an unknown sender that a P-CSCF listens at this address. The UE only sees that no response comes. When Timer B or Timer F fires for a request of `ue serve`, the UE takes its contact as bound to no identity and registers again from the start (TS 24.229 5.1.2A.1.6). `ue call` and `ue sms` report the timeout and exit.

The registration test of `scripts/e2e-registration.sh` sends such a request at its end. After the UE has deregistered, a MESSAGE leaves the UE's address twice, as a UE retransmits over UDP. In Wireshark, `sip.Call-ID contains "unregistered"` shows the two copies arriving at the P-CSCF and nothing coming back. The P-CSCF logs `discarded a request that does not map to an IP association`.

Requests from the network to the UE need no IP association: they come from the S-CSCF, not from a UE.

## The edge of the trust domain

The nodes of one operator trust each other with some header fields: the identity that the network asserts, the charging information, the served user. The UE is outside this trust domain, and the P-CSCF is its edge towards the UE (TS 24.229 4.4.1). Every request and response that crosses the edge, REGISTER and its 401 and 200 included, passes one table in `internal/pcscf/boundary.go`:

| Header field | From the UE | To the UE | Specification |
| --- | --- | --- | --- |
| P-Charging-Vector, P-Charging-Function-Addresses | removed | removed | TS 24.229 5.2.1 |
| P-Media-Authorization | removed | removed | TS 24.229 5.2.1 |
| P-Access-Network-Info | the UE's own stays; one with `network-provided` is removed | removed | TS 24.229 5.2.1 item 3, 4.4.3, RFC 7315 |
| Feature-Caps | removed | stays | TS 24.229 5.2.1 item 5, 4.4.13 |
| Geolocation | the `loc-src` parameter is removed | stays | TS 24.229 5.2.1 item 8 |
| P-Asserted-Identity, P-Preferred-Identity | removed; the P-CSCF asserts the identity itself | P-Asserted-Identity is removed when Privacy has `id` | TS 24.229 4.4.2, 5.2.6.3.3 step 6, 5.2.6.4.4 step 1, RFC 3325 |
| P-Served-User, P-Profile-Key | removed | removed | TS 24.229 4.4.9, 4.4.8, RFC 5502, RFC 5002 |
| P-Asserted-Service, P-Private-Network-Indication | removed from requests | removed from requests | TS 24.229 4.4.5, 4.4.10, RFC 6050 |
| Resource-Priority | stays | removed | TS 24.229 4.4.6 |
| Relayed-Charge, Service-Interact-Info | removed | removed | TS 24.229 4.4.17, 4.4.18 |
| Cellular-Network-Info | stays | removed from requests | TS 24.229 4.4.19 |

The P-CSCF adds a P-Charging-Vector of its own to what it forwards from the UE (5.2.2.1 item 3, 5.2.6.3.3 step 7). A request to the UE goes the other way round: the P-CSCF stores its icid-value and orig-ioi before it removes the header field, and writes them, with its own term-ioi, into the UE's response (5.2.6.4.3 item 9, 5.2.6.4.4 item 6). [Follow a voice call through the IMS](call.md) follows one call by its icid-value. In Wireshark, `sip.P-Charging-Vector && ip.src == 172.28.0.40 && ip.dst == 172.28.0.100` finds nothing: the 401 and the 200 to the REGISTER reach the UE without it.

A Reason in a response to the UE stays. When an application server rejects a call after an announcement, it sends the cause in a Reason towards the caller (TS 24.628 Annex G), and TS 24.229 4.4.1 lets the trust domain of each header field have its own edge.

## Register from your own machine through NAT

The P-CSCF port 5060/udp is published on the host. A UE on the host reaches it through Docker's NAT, so the P-CSCF sees a different source address and port than the ones in the UE's Via (Docker puts NAT between the host and the containers). The `rport` and `received` parameters in the P-CSCF's copy of the Via show the real source, and the responses go there.

To try it, build the UE simulator for your machine. On Windows, from Git Bash:

```sh
MSYS_NO_PATHCONV=1 docker run --rm -v "$PWD:/src" -w /src -e GOOS=windows golang:1.27.4-alpine go build -o ue.exe ./cmd/ue
./ue.exe register -pcscf 127.0.0.1:5060 -listen 127.0.0.1:0 -deregister-after 5s
rm ue.exe
```

In the capture, the REGISTER arrives at the P-CSCF from the bridge gateway, 172.28.0.1, while its Via names `127.0.0.1`.

## What this project simplifies

These parts of the specifications are left out or shortened. Each has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| P-CSCF, TS 24.229 5.2.2.1 item 1 | The Path URI has no flow token and no `ob` parameter. | They belong to SIP outbound (RFC 5626), which this project does not implement. |
| P-CSCF, TS 24.229 5.2.2.1 item 6 | The term-ioi of the P-Charging-Vector of the 200 to the REGISTER is not stored. | It names the home network in the charging records of the P-CSCF, which no node of this project writes, and no later message carries it. |
| S-CSCF, TS 24.229 5.4.1.5 | The S-CSCF ends a registration on its own only when an application server fails the third-party REGISTER and its iFC says SESSION_TERMINATED. Otherwise only an RTR from the HSS ends one. It releases no sessions before the NOTIFY. | The S-CSCF has no operator interface, and it keeps no state of the dialogs it routes. |
| S-CSCF, TS 29.228 6.1.3.1 | An RTR with NEW_SERVER_ASSIGNED or REMOVE_S-CSCF gets DIAMETER_UNABLE_TO_COMPLY. | The HSS stub sends only PERMANENT_TERMINATION. The S-CSCF lets the profile of a user who is not registered go by itself after a few minutes. |
| Subscriber, RFC 6665 4.1.4 and 5.4.9 | A NOTIFY from a second dialog gets 481. | The reg event package forbids a subscription to fork (RFC 3680 4.9). |
| I-CSCF, TS 24.229 5.3.1.3 | No reselection of another S-CSCF when the S-CSCF does not answer. | This network has one S-CSCF. |
| UE, TS 24.229 9.2.1 | A UE without an access network, such as the UEs of this guide or a softphone on the host, has its P-CSCF address configured. A UE on the EPC stub (`ue -epc`) takes it from the PCO when it attaches; see [The VoLTE call flow](volte.md). | The softphone path stays, for softphones and for UEs that do not attach. |
| UE, TS 24.229 5.1.2A.1.6 | After a request that got no response, the UE registers again through the same P-CSCF at once. It does not mark the P-CSCF as unavailable or look for another one. | The UE knows one P-CSCF address, and finding the P-CSCFs again would give the same one. |
| UE, TS 24.229 5.1.1.2.1 and 5.1.1.4.1 | When Timer F fires for a REGISTER, or a 503 comes with a Retry-After longer than Timer F, the UE waits and registers through the same P-CSCF. It does not mark the P-CSCF as unavailable or look for another one. After a failed refresh it always makes an initial registration, also where a short Retry-After of a 503 would let it refresh. | The UE knows one P-CSCF address, and finding the P-CSCFs again would give the same one. No node of this network answers a REGISTER with Retry-After. |
| UE, TS 24.229 5.1.1.2.1 | max-time and base-time of the retry delay are always the defaults of RFC 5626 4.5. | The network can provide them only through a management object of TS 24.167, which the UE does not have. |
| HSS, TS 29.228 6.1.1.1 and 6.1.2.1 | One registration state per subscription, not per public identity. Some Server-Assignment-Type values are not handled. | Each test subscription is one implicit registration set, and the S-CSCF never sends the other values. |
| HSS, TS 29.228 6.1.1.1 step 5 | No check that the user may register at all. | Every test user may register. |
| User profile, TS 29.228 Annex E | One service profile, with no optional elements but the iFCs. | The test subscriptions need nothing more. |
| HSS, TS 29.228 B.2.2 | Every subscription gets the iFCs of the configuration. Only the MSISDN in ServiceInfo differs. | No service of this project differs between users. |
| HSS and S-CSCF, TS 23.228 4.3.3.4 and 4.2.7.2 | A public identity belongs to one private identity, so several UEs cannot share it. A request to a public identity goes to the one contact of its one registration: the S-CSCF never forks. | Each subscriber of this project has one UE. |
| iFC, TS 29.228 B.2.3 and Annex E | No SessionDescription SPT, RegistrationType, ProfilePartIndicator, IncludeRegisterRequest or IncludeRegisterResponse. | The iFCs of SMS and of the MMTel AS need none of them. RegistrationType is optional for the S-CSCF. |
| S-CSCF, TS 24.229 5.4.1.7 | The third-party REGISTER always names the default public identity in To, and has a new Call-ID each time. The term-ioi of its answer is not stored. | The operator may choose the identity. One Call-ID would need more state for each user and server. The term-ioi is for the charging records of the S-CSCF, which no node of this project writes. |
| S-CSCF, TS 24.229 5.4.0 | The S-CSCF looks the application server up in DNS for each request. It does not keep the address that the first third-party REGISTER went to. | Each application server role keeps its state in a Store that all its nodes share. |
| IP-SM-GW, RFC 6665 4.1.2.4 | No Timer N. A SUBSCRIBE that no NOTIFY follows is replaced by a new one at the next third-party REGISTER. | The S-CSCF answers every SUBSCRIBE that it accepts with a NOTIFY at once. |
| IP-SM-GW, TS 24.341 5.3.3.2 | Without an MSISDN in service-info, the PUR names the user by the public identity, not by the IMSI of the UE's REGISTER. | The IMSI would come from the UE's REGISTER in a message/sip body, which the S-CSCF does not send. Sh accepts a public identity (TS 29.328 Table 7.6.1). |
| UE, TS 24.229 5.1.1.3 | When a refresh of the UE's subscription gets 481, the UE subscribes again only after its next initial registration. | Refreshes come long before the subscription runs out. |
| UE, TS 24.229 5.1.1.7 | On `event="rejected"`, `ue call`, `ue answer` and `ue serve` do not release their call. | Only removing the subscription from the HSS rejects a registration. A call of `ue call` and `ue answer` ends by itself once its audio is sent, and a call of `ue serve` ends when the controller hangs up. |
| S-CSCF, TS 24.229 5.4.2.1.2 | The term-ioi of the answer to a NOTIFY of the reg event package is not stored. | It is for the charging records of the S-CSCF, which no node of this project writes. |
| P-CSCF, TS 24.229 5.2.3 | When a refresh of the P-CSCF's subscription gets 481, the next REGISTER of the user starts a new subscription, not the P-CSCF at once. | Refreshes come long before the subscription runs out. |
| P-CSCF, TS 24.229 5.2.3 | The timer that refreshes a subscription runs in the process that took its last NOTIFY, so a P-CSCF started in place of an earlier one holds no timer for a subscription the earlier one was watching. A NOTIFY of that subscription hands it over, but the S-CSCF sends one only when the registration changes, so the subscription usually runs out instead; the user's next REGISTER after that starts a new one, and in between the P-CSCF hears of no change. | Carrying the refresh over without a NOTIFY would need the timers themselves outside the process, and only the subscription is kept there. |
| P-CSCF and UE, RFC 3680 5.2 | The version of the reginfo document is not checked. | The S-CSCF always sends the full state, and a NOTIFY that comes after a later one is refused for its CSeq. |
| P-CSCF, TS 24.229 5.2.6.2 | The UEs and the network reach the P-CSCF at the same address and port. A request counts as one from the network, and needs no IP association, when it is addressed to the URI that the P-CSCF put in Path, to the P-CSCF's own contact, or to the contact of a UE that it serves. | A real P-CSCF usually serves the UEs (Gm) and the network (Mw) on separate interfaces. The containers of this project share one network. |
| P-CSCF, TS 24.229 5.2.2.3 | The IP association is looked up by the address and port that a request over UDP comes from. A request over TCP, which a UE sends when the request is larger than 1300 bytes, is matched by the sent-by of its Via alone, when that has the address the request came from; the port of the connection is not checked. A TCP request whose sent-by names another address is not recognized. Nothing authenticates the port in that sent-by, so a host that shares the UE's IP address could name it. | Without IPsec a TCP connection comes from a port of the moment, which no IP association holds. |
| SIP Digest, TS 24.229 5.4.1.2.1B, RFC 7616 and RFC 8760 | The S-CSCF offers MD5 with qop=auth only, although the specifications prefer SHA-256 and keep MD5 for backward compatibility. | The HSS stub provides only the MD5 H(A1); SHA-256 needs the alternate digest AVPs. |
| Diameter, RFC 3539 3.4.1 and Appendix A, RFC 6733 5.6.4 | No REOPEN state and no failover. Only the configured initiator connects, so there is no election between two connections to the same peer. | Each CSCF has a single HSS. |

## Clean up

```sh
docker compose --profile ue --profile sim --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
