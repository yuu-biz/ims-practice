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
| HSS | 172.28.0.10 | The subscriber database. It answers the I-CSCF and the S-CSCF over Diameter Cx, and the application servers over Sh: here the IP-SM-GW. |
| IP-SM-GW | 172.28.0.70 | The application server of SMS over IP. The S-CSCF registers the user there too (step 15). |
| DNS | 172.28.0.2 | It holds the NAPTR, SRV and A records of the home network domain. |

The home network domain is `ims.mnc001.mcc001.3gppnetwork.org`. The test user has the private identity `001010000000001@ims.mnc001.mcc001.3gppnetwork.org` and the public identity `sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org`. The password is `test-password-1`.

The UE of this guide is a softphone without a SIM card. Its identities and its password are configured in it, as an IMC holds them (TS 24.229 5.1.1.1B.1), so it registers the public identity it was given and uses that identity in its other requests too. That identity is the SIP URI of the user's number, the identity other networks see the user by, and it is not barred. A VoLTE phone whose card has a USIM and no ISIM registers a temporary public identity derived from the IMSI instead, which the network bars: see [The identities of a phone without an ISIM](volte.md#the-identities-of-a-phone-without-an-isim).

## Run a registration

1. Start the system at the root of the repository:

   ```sh
   docker compose up -d --build
   ```

2. Wait until the I-CSCF and the S-CSCF have opened their Diameter connections to the HSS. A node that starts before the HSS accepts connections tries again after 30 seconds (Tc, RFC 6733 2.1), so allow up to a minute. `docker compose logs scscf | grep I-Open` prints a line once that has happened.
3. Register the UE, and deregister it 5 seconds later:

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   The last log lines show `"msg":"registered"`, with the Service-Route and the associated identities, then `"msg":"reg event"` with `"state":"active"`, and then `"msg":"deregistered"`.

4. To see a refused registration, use a wrong password:

   ```sh
   docker compose run --rm ue register -password wrong-password
   ```

   The UE ends with `registration refused with 403`.

Without `-deregister-after`, the UE stays registered and re-registers before the registration expires, until you stop it with Ctrl+C.

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

It then sends a **Server-Assignment-Request** (SAR, Cx command 301) with `Server-Assignment-Type` **`REGISTRATION`** (1). The HSS records the user as registered at this S-CSCF and returns the user profile in **User-Data** (Wireshark: `Cx-User-Data`). The profile is an XML document (TS 29.228 Annex E) that lists the identities registered together. It also carries the initial filter criteria (iFCs), which tell the S-CSCF which application servers to involve in the user's requests. `deploy/compose/hss.yaml` gives every user the two iFCs of SMS over IP. Step 15 shows what the first one does with the REGISTER.

The HSS counts that record. While the user is registered, its public identities are in `ims_hss_registered_public_identities` and its private identity in `ims_hss_registered_private_identities`; the deregistration takes them out again. The node of the HSS serves them at `http://127.0.0.1:8085/metrics` inside its container, beside `ims_hss_subscriptions` and `ims_hss_public_identities`, which count what the HSS holds whether anyone is registered or not. The same listener serves the maintenance API that takes the HSS out of service ([Take the HSS out of service](#take-the-hss-out-of-service)).

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

Right after it passes the 200 OK to the UE, the P-CSCF sends a SUBSCRIBE (TS 24.229 5.2.3). It subscribes once per private identity, after the first registration. A re-registration does not repeat it: the P-CSCF refreshes the subscription 600 seconds before it expires, within its dialog and straight to the S-CSCF. That time is kept with the subscription, so a P-CSCF node started in place of an earlier one goes on refreshing a subscription the earlier one opened.

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
  <registration aor="sip:+12025550101@ims.mnc001.mcc001.3gppnetwork.org" id="reg1" state="active">
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

If the UE does not re-register in time, the registration expires. The S-CSCF sends a NOTIFY with `event="expired"`, and then a SAR with **TIMEOUT_DEREGISTRATION** (4). It looks for the registrations whose time has come once a second, so the two messages may be that much later than the expiry itself. A request for the user in that time already finds the user not registered, and does not go to the contact. On that NOTIFY, the P-CSCF ends the IP association of the UE the same way, so that the NOTIFY to the UE still gets through (TS 24.229 5.2.5.2 and its NOTE 2). The UE learns that it is no longer registered. `ue register` then exits with an error; `ue call` and `ue answer` only log it, and `ue serve` stays unregistered until it is asked to register. If the S-CSCF itself ends a registration with `event="deactivated"`, the UE registers again, as TS 24.229 5.1.1.7 asks.

Every SAR names its S-CSCF in **Server-Name**. A SAR from an S-CSCF other than the one that the HSS holds for the user changes nothing, whatever its `Server-Assignment-Type`. The HSS answers **DIAMETER_ERROR_IDENTITY_ALREADY_REGISTERED** (5005), or DIAMETER_UNABLE_TO_COMPLY (5012) for NO_ASSIGNMENT, and the SAA carries the Server-Name of the S-CSCF that the HSS holds (TS 29.228 8.1.2 and Table 6.1.2.2). The rule matters when a user registers again at another S-CSCF, for example because the first one did not answer. The MAR of the new S-CSCF takes the user over (TS 29.228 8.1.1). The same clause has the HSS also end the registration at the old S-CSCF with a Registration-Termination-Request (RTR), but this HSS sends none ([What this project simplifies](#what-this-project-simplifies)). The old S-CSCF therefore keeps its registration until it expires, which the clause also provides for, and then sends its SAR with TIMEOUT_DEREGISTRATION. The HSS keeps the user at the new S-CSCF, so calls to the user still go there. The old S-CSCF has already removed its registration when it sends the SAR, so the error leaves nothing behind there either. The I-CSCF of this network names one S-CSCF, so the captures of this guide do not show this answer.

The instances of one S-CSCF share the state of the users, and each may have a name of its own. The HSS then holds a user under the name of the instance whose MAR or SAR assigned the user. The S-CSCF keeps that name with the challenge, with the registration and with the profile of a user who is not registered, and every Cx request about the user names it, whichever instance sends the request. So the HSS takes the SAR of an instance that ends a registration made through another one, for example after that one has stopped.

## Remove a subscription: deregistration by the HSS

The HSS has a provisioning API over HTTP, which listens where the hss key `provisioning_listen` says, for example `:8081`. The API is this project's own, not a 3GPP interface. In User Data Convergence (UDC), where one repository holds the subscriber data of a network, that repository has a provisioning interface towards the operator's OSS. The interface itself is outside the scope of the specification (TS 23.335 4.2.3). Subscription Management (SuM) does have a standard interface towards the OSS, an IRP on Itf-N (TS 32.141 4.2.1) in the form of a Bulk CM file in XML (TS 28.753 Annex A.3.1), but its information model (TS 28.752) has no attribute for the long-term key, OPc or the SIP Digest password that this API carries. The standard form alone cannot bring a subscription of this network in, so the provisioning goes over an API of this project's own. The JSON fields have the names of the configuration keys.

| Request | What it does |
| --- | --- |
| `GET /subscriptions` | Lists the subscriptions with their registration state, one page at a time. A page holds 1000 of them, or the smaller number that `?limit=` asks for. `?after={private identity}` starts the page after that identity, in the order the identities sort in. While pages are left, the answer carries the query of the request for the next one in a `Link` header with `rel="next"` (RFC 8288), such as `<?after=001010000000001%40ims.mnc001.mcc001.3gppnetwork.org&limit=2>; rel="next"`. The HSS reads a page from its index of the private identities, so a page of a network with a million subscriptions costs what a page of this one costs. |
| `GET /subscriptions/{private identity}` | Shows one subscription, with `test_user` and `service_profile` where it has them. |
| `POST /subscriptions` | Adds one: `{"private_identity": "...", "public_identities": ["sip:...", "tel:..."], "password": "..."}`. An optional `"tel": "tel:+..."` adds that tel URI to the public identities. For IMS-AKA, `"aka": {"k": "...", "opc": "...", "amf": "...", "sqn": "..."}` in hexadecimal goes with the password or in its place. `"ut_password": "..."` lets the user change the supplementary services over Ut; the HSS keeps only H(A1) of it, as Sh repository data. `"mmtel_services": "<MMTelServices ...>"` gives the first supplementary service document; without it the subscription gets the one of the hss key `mmtel_services`. `"test_user": true` marks a subscription that the operator keeps for trying features out, and `"service_profile": "..."` names the service profile the subscription gets; both choose which filter criteria reach the S-CSCF, as [Follow a short message through the IMS](sms.md) shows. A profile that the hss key `service_profiles` does not define is refused with 400. |
| `PUT /subscriptions/{private identity}` | Adds one or replaces the whole of one, from the body of the `POST` with `"repository_data"` beside it. The `private_identity` of the body is the one of the path. Whatever the body leaves out goes, so a `PUT` with none of `repository_data`, `mmtel_services` and `ut_password` leaves the subscription with the document of the hss key `mmtel_services`, and the supplementary services the user set over Ut are back to that default. `"repository_data": [{"service_indication": "MMTEL-Services", "service_data": "<MMTelServices ...>"}]` carries the Sh repository data as it is to be stored, in place of `mmtel_services`, `ut_password` and `customized_alerting_tone`; an empty array asks for a subscription with no repository data at all. What the running subscriber has stays: the registration state, the S-CSCF serving it, the short messages waiting for it and SQN_HE. The one exception is a registered user whom the `PUT` gives another default public identity, whom the HSS deregisters ([Change the service profile of a registered user](#change-the-service-profile-of-a-registered-user)); the answer, a dry run's too, shows the user Not Registered. `aka.sqn` is therefore read only where the subscription is new, so sending the same file again never takes SQN_HE back and lets an authentication vector be used twice. The answer is `{"result": "created", "subscription": {…}}`, with `updated` for a subscription that changed and `unchanged` for one that the body already describes, credentials and repository data included. The status is 201 for a subscription it created and 200 otherwise. `?dry_run=true` changes nothing and answers what the request would do, which is 200 even where it says `created`. A replacement that changes the user profile of a registered user reaches its S-CSCF at once, as a `PATCH` does: another service profile, a barred identity or one no longer barred, an identity added to the set or taken away. A replacement that gives a registered user another default public identity, the first identity of the set that is not barred (TS 29.228 6.5.1.1), is the exception: barring the first identity is one. |
| `PATCH /subscriptions/{private identity}` | Changes the mark and the service profile name of one and nothing else: `{"test_user": true, "service_profile": "..."}`, either field on its own. A field the body leaves out stays as it is, so `{}` changes nothing. These two are changed this way because a `PUT` would have to carry the credentials of the subscription, and the API hands those out to nobody. A profile that the hss key `service_profiles` does not define is refused with 400. The answer is the subscription as a `GET` shows it. A change that leaves a registered user with another service profile reaches its S-CSCF at once ([Change the service profile of a registered user](#change-the-service-profile-of-a-registered-user)). |
| `DELETE /subscriptions/{private identity}` | Removes one. |
| `GET /subscriptions/export` | Writes the subscriptions out for an archive of the network, one subscription per line as JSON (the media type `application/x-ndjson`). Each line is the body of the `PUT` that puts that very subscription back, credentials and all: `password`, `aka` with `k`, `opc`, `amf` and `sqn`, which is SQN_HE as it now stands rather than the value that was provisioned, and `repository_data` with the Ut credential, as H(A1), and the customized alerting tone among it. Sending the lines of an archive back therefore answers `unchanged` for every one of them, and a subscription that is gone comes back as it was. The pages are those of `GET /subscriptions`: `?after=` and `?limit=`, and a `Link` header of `rel="next"` while pages are left. Left out is what the running subscriber has rather than what the operator provisioned, the registration state and the S-CSCF serving it, so a subscription that goes back in is Not Registered and its user registers again. Nothing is locked over the whole network: each line holds one subscription as it stood when that line was written, and a run that takes several pages carries the changes made while it read them. A subscription removed while its page is read is left out of it. **This one answer hands the long-term key, OPc, the SIP Digest password and SQN_HE to whoever reaches the provisioning listener.** That is why the listener belongs on an internal network and on no address of the host: the `hss` service of `compose.yaml` publishes no port of its own, and in Kubernetes the HSS is reachable inside the cluster alone. |

No answer other than the export contains the password or the AKA keys (K and OPc). The answers list the authentication schemes that the subscription allows in `authentication_schemes`. They name in `effective_service_profile` the service profile that the subscription gets: its own `service_profile` where it names one, and otherwise the profile that the setting for its kind of subscription names, `test_user_service_profile` for a marked one and `default_service_profile` for any other. The data of the subscription does not say which that is, because the two settings change while the HSS runs, so the answer names it. A new subscription follows the same rules as one in the configuration file: 400 for a missing field or a public identity that is not a SIP or tel URI, 409 for an identity that already belongs to another subscription.

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

## Change the service profile of a registered user

An operator gives a user another service profile while the user is registered, with a `PATCH` of `service_profile` or of `test_user`, or changes the user profile otherwise with a `PUT`, by barring an identity or changing the identities of the set. The S-CSCF has the criteria of the old profile, from the SAA of the registration. The HSS does not leave it with them until the user registers again: it sends the whole user profile there at once, in a **Push-Profile-Request** (PPR, Cx command 305) (TS 29.228 6.2.2 and 6.6.1). In Wireshark, `diameter.cmd.code == 305` shows this flow.

While subscriber 3 is registered, move it to the profile `no-services`, which holds no criteria at all. The provisioning API is not published on the host, so the request goes from inside the container of the HSS. BusyBox `wget` cannot send a `PATCH`, hence the request written out to `nc`:

```sh
docker compose exec hss sh -c '
body="{\"service_profile\":\"no-services\"}"
printf "PATCH /subscriptions/001010000000003@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8081'
```

```text
 S-CSCF          HSS
 |                |  PATCH /subscriptions/...
 |  PPR (1)       |
 |<---------------|
 |  PPA (2)       |
 |--------------->|
 |                |  200 OK with the subscription (3)
```

1. The HSS sends the PPR to the S-CSCF whose name it stored for the user, by the Origin-Host of that S-CSCF's SAR, as it sends an RTR (TS 29.228 Table 6.2.2.1). User-Name is the private identity. **User-Data** holds the whole user profile: every public identity of the implicit registration set with the criteria of its new service profile, the identities that did not change included (6.5.2.1).
2. The S-CSCF replaces what it holds for those identities with what it was given and answers a **Push-Profile-Answer** (PPA) with DIAMETER_SUCCESS (2001) (6.2.2.1). The registration itself stays, with its expiry and its bindings: the UE learns nothing of the change, and the user's next request is evaluated against the new criteria. A PPR for a user whose profile the S-CSCF does not hold gets DIAMETER_ERROR_USER_UNKNOWN (5001), and user information it cannot read DIAMETER_ERROR_NOT_SUPPORTED_USER_DATA (5009), with what it has left as it was (Table 6.2.2.1.1). A profile that bars every identity of a registered user is one it cannot take: the default public identity is the first identity that is not barred (6.5.1.1), and such a profile has none.
3. The HSS answers the provisioning request once the PPA is in. A PPR that gets no answer, or a PPA with an error, is written to the log of the HSS, and the request is still answered 200 with the new subscription: the subscription is stored either way, and the S-CSCF gets the new profile at the next registration.

With `no-services`, no criterion of subscriber 3 matches anything any more: its short messages no longer reach the IP-SM-GW and its calls no longer reach the MMTel AS. The same `PATCH` with `"service_profile":"default"` pushes the criteria back.

The HSS pushes for a user it has as registered, and for a user in the unregistered state, where an S-CSCF keeps the profile for calls to a user who is not registered (6.6.1). For any other user it writes the subscription and nothing more: the next registration brings the profile. It also pushes only when the user profile it would send really changes, so a `test_user` mark that picks the same service profile as before sends nothing.

A `PUT` that gives a registered user another default public identity sends no PPR. The default identity is the first identity of the set that is not barred (6.5.1.1), so changing the order of the set, barring its first identity, lifting the barring of an identity before it, taking it out of the set, or barring all of them gives another one, or none. A PPR must not change the default identity of a registered set, and the HSS deregisters the set with an RTR instead (6.5.2.1). The HSS has the user Not Registered, and the RTR carries no Public-Identity, as for a removed subscription. Its Reason-Code depends on what is left:

- While an identity of the set is not barred, the subscription stays valid and the user only has to register again. The Reason-Code is **SERVER_CHANGE** (2), with which the S-CSCF asks the user to register again (6.1.3.1). The NOTIFYs have `event="deactivated"`, and the UE registers again at once (TS 24.229 5.1.1.7). The SAA of that registration brings the new default identity.
- When every identity is barred, registering again would not help: the UAR of the user is refused with DIAMETER_AUTHORIZATION_REJECTED (5003) (6.1.1.1 step 4). The Reason-Code is **PERMANENT_TERMINATION** (0), and the NOTIFYs have `event="rejected"`, as for a removed subscription.

A change of the two settings that name the service profile of a kind of subscription, `default_service_profile` and `test_user_service_profile`, is pushed the same way ([Admit only the test users](#admit-only-the-test-users) shows how a setting is changed while the HSS runs). Naming another profile there gives another user profile to every subscription that takes its name from that setting, so the HSS sends one PPR per user, to the S-CSCF of each of them whose profile an S-CSCF holds. Those are the only users it sends to: a subscription that names a `service_profile` of its own takes neither setting, and a user that no S-CSCF serves has no profile anywhere to replace. The HSS keeps an index of the users whose profile an S-CSCF holds, grouped by where the name of their service profile comes from, and reads the group of the changed setting a page at a time, so the work of a change is one PPR per user it concerns and no reading of the subscriptions it does not.

The PPRs follow the answer to the maintenance request: the operator is told that the setting is in force as soon as it is, and a network with many registered users does not hold that answer up. Each change of the name gets a round of PPRs of its own, which carries the profile of that change. The rounds of one setting go one after another: a change made while a round is under way starts its own as that one ends. That holds even when the name is back to the one of the round by then, since the users may have been given the other profile in between. So two changes close together each reach the users, in their order, and leave the S-CSCFs with the profile of the later one. A change of the admission policy alone names no profile, and pushes nothing. A PPR that fails is written to the log of the HSS, as for a change of one subscription, and that S-CSCF reads the profile at the next registration of its user.

The HSS keeps the push of a change with its settings. One instance at a time claims it for a page of users, sends their PPRs one after another, and records each one, so a user gets one PPR however many instances of the HSS take the change on. An instance that stops in the middle of a page leaves its claim to run out, after 30 seconds, and the instance that claims the push next goes on from the last PPR recorded: with [the state of the users in a store outside the nodes](README.md#the-state-of-the-users-in-a-store-outside-the-nodes), that is another instance, or the same one once it has started again. An instance that the operator has shut down or locked claims no push, as it takes on no other new work. An HSS reaches an S-CSCF only over the connection that the S-CSCF opened to it, and an S-CSCF that names one instance of the HSS connects to that one alone. So a PPR that the instance of a round cannot send for want of that connection is left for the other instances, and the one that the S-CSCF connected to sends it, while the round goes on with the other users. Sending a setting again with the value it has pushes nothing, since no user profile changes.

## Run the network from the OAM

The `oam` service is the OAM server. It operates each node through the maintenance API of that node, and the subscribers through the provisioning API of the HSS, and it serves the OAM screen on the same listener. The interface between a manager and the nodes it manages is left to each product (TS 32.600 4.2.1.3), so this one is this project's own, under `/oam/v1/`.

Its port is on no address of the host. The API changes the configuration of the nodes and hands out archives that hold the credentials of every subscription, and it asks for no password, so the requests below go from inside the container of the OAM, as those to the maintenance API of a node do.

The inventory in `deploy/compose/oam.yaml` holds the ten nodes that serve the maintenance API: the three CSCFs, the HSS, the MMTel AS, the MRFC, the IP-SM-GW, the Service Centre and the two media relays. One request reads the state of all of them:

```sh
docker compose exec oam wget -qO - http://127.0.0.1:8080/oam/v1/state
```

The answer has an entry for every node of the inventory, with that node's state or why it did not answer; a node that failed is never left out, and `succeeded` and `failed` count them. Everything the sections on taking a node out of service do one node at a time, the OAM does for every node of a role at once, at `/oam/v1/roles/{role}/administrative-state`.

### Provision subscribers in bulk

An operator provisions many subscriptions at once as a file, one JSON object per line. A line is the body of the `PUT` of the provisioning API, with `"delete": true` beside the private identity as the one field the OAM reads itself. Every identity of a line must be test data, a number of a test IMSI or of the fictional range, or the OAM refuses the line without asking the HSS.

```sh
docker compose exec oam sh -c 'cat >/tmp/new-subscribers.jsonl <<"EOF"
{"private_identity":"001010000000120@ims.mnc001.mcc001.3gppnetwork.org","public_identities":["sip:+12025550120@ims.mnc001.mcc001.3gppnetwork.org"],"tel":"tel:+12025550120","password":"test-password-0120","test_user":true}
{"private_identity":"001010000000121@ims.mnc001.mcc001.3gppnetwork.org","public_identities":["sip:+12025550121@ims.mnc001.mcc001.3gppnetwork.org"],"tel":"tel:+12025550121","password":"test-password-0121","test_user":true}
EOF
length="$(wc -c </tmp/new-subscribers.jsonl | tr -d " ")"
{ printf "POST /oam/v1/provisioning HTTP/1.0\r\nContent-Type: application/x-ndjson\r\nContent-Length: %s\r\n\r\n" "$length"
  cat /tmp/new-subscribers.jsonl
} | nc -w 30 127.0.0.1 8080'
```

BusyBox `wget` sends no method but `GET` and `POST` and no body of its own, hence the request written out to `nc`. The answer comes back as it is made, one line for each record and the summary last:

```text
{"outcome":{"line":1,"private_identity":"001010000000120@ims.mnc001.mcc001.3gppnetwork.org","result":"created"}}
{"outcome":{"line":2,"private_identity":"001010000000121@ims.mnc001.mcc001.3gppnetwork.org","result":"created"}}
{"summary":{"lines":2,"created":2,"updated":0,"unchanged":0,"deleted":0,"rejected":0,"failed":0}}
```

The OAM reads, sends and reports one record at a time, so a file of any length costs it the same memory. A record is `created`, `updated`, `unchanged`, `deleted`, `rejected` (the line is wrong, and the reason says how) or `failed` (the HSS did not take it, and sending it again may succeed). The same file sent again leaves every record `unchanged`, credentials included, so a file is safe to send twice; `?dry_run=true` on the path changes nothing and answers what each line would do. A reason never shows a credential, even where the HSS quotes one back.

The first of these subscribers registers like any other, with the password of its line:

```sh
docker compose run --rm ue register \
  --impi 001010000000120@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550120@ims.mnc001.mcc001.3gppnetwork.org \
  --password test-password-0120 --deregister-after 5s
```

The capture then holds the UAR, the MAR and the SAR of a subscriber that no configuration file of the deployment names. A file of lines with `"delete": true` removes them again.

### Back the subscriptions up and put them back

A backup writes the persistent data of the network out section by section. There is one section here, `hss.subscriptions`, which the OAM reads through the paged export of the provisioning API, and it writes it as one archive of JSON lines beside a manifest that records how each step went.

```sh
docker compose exec oam sh -c '
body="{\"operator\":\"alice\",\"comment\":\"before the change\"}"
printf "POST /oam/v1/backups HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 30 127.0.0.1 8080'
```

The answer streams each step as it starts and ends, and the manifest last, with the identifier of the backup. `GET /oam/v1/backups` lists the backups, the newest first. A restore reads the archive to the end first, to see that it is whole, and then either says what each record would do or puts each one back through the same `PUT` as a provisioning:

```sh
docker compose exec oam sh -c '
printf "POST /oam/v1/backups/$1/restore?dry_run=true HTTP/1.0\r\n\r\n" |
  nc -w 60 127.0.0.1 8080' sh <identifier>
```

Leave `?dry_run=true` off to put them back. Restoring a network that is still there leaves every record `unchanged`, and restoring the same archive twice changes nothing the second time. A restored subscription is Not Registered and its user registers again: what lives only while a node runs, the registrations, the dialogs and the media contexts, is in no backup.

**Each line of the archive holds the long-term key, OPc and the password of its subscription**, as the HSS needs them back. The archive is a file of the OAM's own volume, readable by nobody else, and the OAM hands it out over its API to whoever reaches that listener, which is why the listener is on no address of the host.

## Admit only the test users

An operator who is trying a feature out can keep the network to the users it is tried on. The `admission` setting says which subscribers the HSS admits:

- `all`, the setting this deployment starts on, admits every subscriber the HSS has.
- `test_users` admits only the subscribers marked `test_user`, the mark that `POST`, `PUT` and `PATCH` of the provisioning API set, and that also picks the service profile of a test user.

`admission` is in the `hss` section of `deploy/compose/hss.yaml`, which says what the HSS starts on, and it is one of the settings that the maintenance API changes while the HSS runs, so narrowing the network takes no restart.

With `test_users`, the HSS refuses the UAR of a subscriber that is not marked. Step 5 of the registration status query asks that the identity be authorized to register, and names **DIAMETER_AUTHORIZATION_REJECTED** (5003) for one that is not (TS 29.228 6.1.1.1). That value belongs to the base protocol, not to Cx (RFC 6733 7.1.5), so it arrives in **Result-Code** rather than in Experimental-Result-Code. Cx has a 5003 of its own, DIAMETER_ERROR_IDENTITY_NOT_REGISTERED (TS 29.229 6.2.2.3), which is always in Experimental-Result-Code, so the two are told apart by the AVP that carries them. The I-CSCF turns a negative UAA into **403 Forbidden** (TS 24.229 5.3.1.3).

```text
 UE             P-CSCF         I-CSCF          HSS
 |  REGISTER (1) |              |              |
 |-------------->|------------->|              |
 |               |              |  UAR (2)     |
 |               |              |------------->|
 |               |              |  UAA (3)     |
 |               |              |<-------------|
 |  403 (4)      |              |              |
 |<--------------|<-------------|              |
```

1. The UE sends the first REGISTER, which reaches the I-CSCF as in steps 1 and 2 of the sequence above.
2. The I-CSCF asks the HSS whether the user may register here, with **User-Authorization-Type** `REGISTRATION`. A request without that AVP means the same.
3. The subscriber is not marked `test_user`, so the HSS answers Result-Code DIAMETER_AUTHORIZATION_REJECTED, with no S-CSCF name and no capabilities. In Wireshark, `diameter.Result-Code == 5003` finds the answer.
4. The I-CSCF answers 403, which the P-CSCF passes to the UE. The S-CSCF is never asked, so no MAR goes out and the UE is never challenged. `ue register` ends with an error, as it does for a subscription that was removed.

A **REGISTRATION_AND_CAPABILITIES** UAR takes the same branch of step 5 and gets the same answer, so a refused user is stopped before the I-CSCF picks an S-CSCF. A **DE_REGISTRATION** UAR is not checked at all: step 5 asks nothing of it, and refusing it would leave the user unable to end a registration that the S-CSCF and the P-CSCF still hold.

Refusing the registration is not enough on its own. A call to a user who is not registered reaches the HSS's location query, which answers for any identity whose profile has a criterion of the terminating unregistered case (TS 29.228 6.1.4.1), and the S-CSCF that the I-CSCF then picks asks for the profile with a SAR of **UNREGISTERED_USER**. So the HSS refuses that SAR with DIAMETER_AUTHORIZATION_REJECTED too, for a subscriber it does not admit and holds no valid registration of. The S-CSCF has no profile to serve the call with and answers **500 Server Internal Error**, as it does for any SAA that did not succeed; the caller learns nothing of the reason. Every other Server-Assignment-Type goes through, so that a registration that is ending is not left half finished in the HSS.

Narrowing the policy, or taking the mark off a subscriber while the policy is `test_users`, leaves the registrations that are valid at that moment as they are. The HSS sends no RTR, and calls to those users still find their S-CSCF. Each of them drops out when its registration expires, because the refresh is refused: the S-CSCF holds the expiry, and the HSS neither counts it down nor asks for it to be ended.

To see the refusal, narrow the policy on the maintenance API of the HSS node, the listener on port 8085 that also takes the HSS out of service ([Take the HSS out of service](#take-the-hss-out-of-service)). A `PATCH` of `/maintenance/v1/config` carries the settings to change by name, with who asks and why:

```sh
docker compose exec hss sh -c '
body="{\"settings\":{\"hss.admission\":\"test_users\"},\"operator\":\"learner\",\"comment\":\"trying a feature out\"}"
printf "PATCH /maintenance/v1/config HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8085'
```

The answer is the record of the change, with the value of each setting before and after, which is what an operator keeps to undo it later:

```json
{"node":"hss","instance":"...","generation":1,"time":"...","operator":"learner","comment":"trying a feature out","changes":[{"name":"hss.admission","previous":"all","value":"test_users"}]}
```

The policy is in force at once, for the requests that arrive from that moment on. `GET /maintenance/v1/config` reads it back under `changeable`, beside the two settings that name the service profile of a subscription and, in the `hss` section, the service profiles of the network with the features each one turns on:

```sh
docker compose exec hss wget -qO- http://127.0.0.1:8085/maintenance/v1/config
```

The HSS refuses a value it cannot take, and then changes none of the settings of the request: a policy that is neither of the two, or a service profile that `service_profiles` does not define.

The HSS keeps these settings where it keeps its subscribers. In this deployment that is the memory of its process, so a recreated container starts again on the policy of its file and answers on `all` until an operator sets it afresh. With [the state of the users in a store outside the nodes](README.md#the-state-of-the-users-in-a-store-outside-the-nodes), the settings are in the store, with the generation of the change that set them, and every instance of the HSS answers with them. An HSS that starts reads them there before it answers, so a recreated container answers on the policy in force, and an instance takes on a change made through another within a second. The file gives the settings only to the first HSS that finds the store empty. The instances of the HSS are meant to run the same file. One whose `service_profiles` does not define a profile that the store names takes on every other setting of the store, keeps its last good value for that one, and logs an error once for each change; it does the same when it takes a change of its own, and leaves the PPRs of that profile to the instances that define it. When such a round has not moved on for two minutes because none of the instances that take it up defines the profile, as when the only instance that did has stopped, the round is given up, so that a correction of the setting is pushed. A round that is moving on is never given up, however long it takes.

The two settings that name a service profile are changed in the same `PATCH`, as `hss.default_service_profile` and `hss.test_user_service_profile`, and one request may carry all three settings at once. A change of either name reaches the users that are registered at that moment: the HSS pushes the new profile to their S-CSCFs, as it does for a change of one subscription ([Change the service profile of a registered user](#change-the-service-profile-of-a-registered-user)). That is how a feature is turned on for the test users, or for everyone, without waiting for each user to register again.

Now mark subscriber 2 as a test user. The provisioning API is not published on the host either, so this request goes from inside the container of the HSS as well, on port 8081.

```sh
docker compose exec hss sh -c '
body="{\"test_user\":true}"
printf "PATCH /subscriptions/001010000000002@ims.mnc001.mcc001.3gppnetwork.org HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
  nc -w 5 127.0.0.1 8081'
```

Subscriber 1 is now refused and subscriber 2 registers:

```sh
docker compose run --no-deps --rm ue register --deregister-after 2s
docker compose run --no-deps --rm ue register --deregister-after 2s \
  --impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  --impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org --password test-password-2
```

To leave the deployment as the rest of this guide describes it, send the same `PATCH` with `"hss.admission":"all"`, which is the `previous` value of the record above, and take the mark off subscriber 2 with `{"test_user":false}`. `scripts/e2e-admission.sh` runs this scenario and reads the UAA and the 403 out of a capture of its own.

## A wrong password

With a wrong password, steps 1 to 8 are the same. In step 9 the response does not match. The S-CSCF answers **403 Forbidden** (TS 24.229 5.4.1.2.3B) and sends a SAR with **AUTHENTICATION_FAILURE** (9). The HSS clears the S-CSCF it had stored (TS 29.228 6.1.2.1), so the next attempt starts again with FIRST_REGISTRATION.

## A registration that fails

A registered UE refreshes its registration before it expires (TS 24.229 5.1.1.4.1). When the refresh gets 408, 500, 504 or 403, the UE no longer counts on the registration it had and makes an initial registration: a REGISTER like the first one, which the S-CSCF challenges again (5.1.1.4.1). The UE simulator does so after any failed refresh. When the response carries Retry-After, the UE first waits that long: for that time it makes no initial registration through the same P-CSCF (5.1.1.2.1).

When an initial registration fails as well, the UE does not try again at once. Before each new attempt it waits the retry delay of RFC 5626 4.5 (TS 24.229 5.1.1.2.1):

- The upper bound W is 30 seconds times 2 to the power of the number of failures in a row, and at most 1800 seconds.
- The UE waits a random time from half of W to all of W. The first retry comes 30 to 60 seconds after the first failure, the second 60 to 120 seconds after the second, and from the sixth failure on the UE tries every 15 to 30 minutes.
- A response with Retry-After asks for its own wait instead. After it, the count of failures starts again.

The random part spreads the attempts of UEs that lost their registration at the same moment, so that they do not all come back at once. `ue serve` and `ue register` keep trying until they are registered again. Each failure is logged as `"msg":"registration failed"`, with `retry_after_ms`, the wait before the next attempt. `ue serve` also waits and tries again when its first registration fails. The other commands end with an error then, as `ue register` does above for a removed subscription.

## Take the S-CSCF out of service

An operator takes a node out of service without cutting what it serves: the node takes nothing new and goes on with what it has begun, until nothing is left. This is called a drain. A commercial network runs several instances of the S-CSCF, which share the state of the users, so the new work goes to the other instances and no user has to register again.

The node of the S-CSCF serves a maintenance API for this on port 8085, the listener of the `maintenance` section of `deploy/compose/scscf.yaml`, next to the metrics that Prometheus scrapes there. The API asks for no password, so the port is not published on the host: send the requests from inside the container of the S-CSCF. The administrative state of the S-CSCF has the values of TS 28.622 5.3.1. `UNLOCKED` takes new work, `SHUTTINGDOWN` serves only what it has, and `LOCKED` takes nothing new. A shut down makes the S-CSCF `SHUTTINGDOWN` while it still has transactions in progress, and `LOCKED` once the last one has ended (ITU-T X.731 7.1.3).

1. Shut the S-CSCF down:

   ```sh
   docker compose exec scscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/scscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the S-CSCF. No transaction was in progress, so it is `LOCKED` at once:

   ```json
   {"role":"scscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   `users` counts the transactions that the S-CSCF is serving. `operational_state` says whether it can work (TS 28.622 5.3.3): it is `DISABLED` while no instance of the HSS takes its Cx requests. `docker compose exec scscf wget -qO- http://127.0.0.1:8085/maintenance/v1/state` shows the whole node.

2. Register the UE:

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   The UE ends with `REGISTER answered 500 Server Internal Error`.

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|  REGISTER    |               |              |
 |              |------------->|  UAR, UAA     |              |
 |              |              |<---------------------------->|
 |              |              |  REGISTER     |              |
 |              |              |-------------->|              |
 |              |              |  503 Retry-After: 30         |
 |              |  500         |<--------------|              |
 |  500         |<-------------|               |              |
 |<-------------|              |               |              |
```

- The REGISTER reaches the S-CSCF as in steps 1 to 4 of the registration: the I-CSCF asks the HSS which S-CSCF serves the user, and forwards it there.
- The S-CSCF answers **503 Service Unavailable** with **Retry-After: 30**. A request outside a dialog starts something new, which the S-CSCF does not take while it is not `UNLOCKED`. Retry-After asks the client to send this server no other request for that many seconds (RFC 3261 21.5.4). The S-CSCF does not authenticate the user, so no MAR goes to the HSS.
- A client that gets a 503 sends the request to the next server that DNS gave it (RFC 3263 4.3). With several instances of the S-CSCF the REGISTER would go to the next instance and register the user there. The I-CSCF of this network has one S-CSCF, so it has no other to try.
- A proxy does not pass a 503 on (RFC 3261 16.7 step 6). The 503 says that the S-CSCF takes no request now. Passed on, it would say the same of the I-CSCF, which still takes requests for the other S-CSCFs, and the UE would read it as said of its P-CSCF: a 503 whose Retry-After is longer than Timer F has the UE mark its P-CSCF as unavailable for that time (TS 24.229 5.1.1.2.1). So the I-CSCF answers **500 Server Internal Error** of its own. The Retry-After was about the S-CSCF, so the 500 does not carry it.
- The P-CSCF passes the 500 on to the UE. A 500 does not send the REGISTER on to another I-CSCF, as a 3xx, a 480 or a 503 would (TS 24.229 5.2.2.1 item 7, RFC 3263 4.3).

In Wireshark, `sip.Status-Code == 503` shows the 503 of the S-CSCF, and `sip.Status-Code == 500` the two 500 responses after it. The PUT request travels over the loopback interface of the container, so the capture does not hold it.

While it is not `UNLOCKED`, the S-CSCF still serves the requests within a dialog, such as the BYE of a call in progress, and the requests that an application server sends back to it with the original dialog identifier, so that what has begun ends as it would have. It also ends the registrations that expire. A refresh of a registration is a new REGISTER outside a dialog, so it gets the 503 too.

3. Unlock the S-CSCF with the same request and `UNLOCKED`:

   ```sh
   docker compose exec scscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/scscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The next `ue register` registers as before.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-sip.sh` runs these steps and checks the capture.

## Take the P-CSCF out of service

A UE sends every request to the P-CSCF it registered through, so the UEs of a P-CSCF do not move to another one by themselves. A P-CSCF that the operator takes out of service refuses each REGISTER that would start or refresh a registration through it, and the UE makes its initial registration through another P-CSCF instead. Until then the P-CSCF goes on serving the UEs registered through it: their calls, the requests within a dialog, and the deregistration that ends a registration.

The node of the P-CSCF serves the same maintenance API on port 8085, the listener of the `maintenance` section of `deploy/compose/pcscf.yaml`. Its `users` count the IP associations of the UEs that use this P-CSCF. A shut down makes the P-CSCF `SHUTTINGDOWN` while it still has one, and `LOCKED` once it has forgotten the last. A UE that has deregistered stays a user for another 32 seconds, while the requests to it that were under way end. A UE that has registered through another P-CSCF after the 503 stays a user until its registration through this one expires, as it sends no deregistration through this P-CSCF. `operational_state` is always `ENABLED`: the P-CSCF needs no peer to take requests, as it finds an I-CSCF through DNS for each REGISTER.

1. Shut the P-CSCF down:

   ```sh
   docker compose exec pcscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/pcscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the P-CSCF. With no UE registered, it is `LOCKED` at once:

   ```json
   {"role":"pcscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

   With a UE registered, it is `SHUTTINGDOWN`, and `users` counts that UE.

2. Register the UE:

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   The UE ends with `REGISTER answered 503 Service Unavailable, Retry-After 300`.

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|              |               |              |
 |  503 Retry-After: 300       |               |              |
 |<-------------|              |               |              |
```

- The P-CSCF answers **503 Service Unavailable** with **Retry-After: 300** itself, and forwards nothing: no node of the home network sees the REGISTER.
- The UE takes a 503 to a REGISTER as said of its P-CSCF. When its Retry-After is longer than Timer F, the UE marks the P-CSCF as unavailable for that time, and may make its initial registration through another P-CSCF that it knows, or find the P-CSCFs again; with a shorter one it goes on using the same P-CSCF (TS 24.229 5.1.1.2.1). Timer F is 64*T1: 128 seconds with the T1 of 2 seconds that TS 24.229 Table 7.7.1 gives the UE. No specification gives the value of Retry-After; 300 seconds is this project's choice.
- The UE simulator knows one P-CSCF address, so `ue serve` waits the 300 seconds and registers through the same P-CSCF again (see [What this project simplifies](#what-this-project-simplifies)). `ue register` ends at once, as it does after any registration that fails.
- A refresh of a registration is a REGISTER too, and gets the same 503: a registered UE moves to another P-CSCF at its next refresh at the latest.
- The P-CSCF counts the REGISTER it refused as a registration procedure that failed with 503. The metrics at `http://127.0.0.1:8085/metrics` in the container of the P-CSCF show it as `ims_registration_failures_total{role="pcscf",type="initial",status="503"}`.

In Wireshark, `sip.Status-Code == 503 && ip.src == 172.28.0.40` shows the 503 of the P-CSCF.

3. Unlock the P-CSCF with the same request and `UNLOCKED`:

   ```sh
   docker compose exec pcscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/pcscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The next `ue register` registers as before.

`scripts/e2e-maintenance-sip.sh` runs these steps as well, and checks the 503 in the capture.

## Take the I-CSCF out of service

The I-CSCF is the entry point of the home network. It holds nothing from one request to the next, and it does not record-route, so no later request of a dialog comes back to it. An instance that the operator takes out of service therefore has nothing of its own to carry on with: it refuses every request, and the sender tries the next I-CSCF.

The node of the I-CSCF serves the same maintenance API on port 8085, the listener of the `maintenance` section of `deploy/compose/icscf.yaml`. Its `users` count the transactions it is serving, so a shut down with nothing in progress makes it `LOCKED` at once. Its `operational_state` is `DISABLED` while no instance of the HSS takes its Cx requests: the I-CSCF asks the HSS which S-CSCF serves the user of every REGISTER, and where the callee of every initial request is, so without the HSS it can serve neither.

1. Shut the I-CSCF down:

   ```sh
   docker compose exec icscf sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/icscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the I-CSCF:

   ```json
   {"role":"icscf","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

2. Register the UE:

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   The UE ends with `REGISTER answered 504 Server Time-out`.

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|  REGISTER    |               |              |
 |              |------------->|               |              |
 |              |  503 Retry-After: 30         |              |
 |              |<-------------|               |              |
 |  504         |              |               |              |
 |<-------------|              |               |              |
```

- The I-CSCF answers **503 Service Unavailable** with **Retry-After: 30** before it asks the HSS anything: no UAR goes out, and the S-CSCF never sees the REGISTER. No specification gives the value of Retry-After; 30 seconds is this project's choice.
- A client that gets a 503 sends the request to the next server that DNS gave it (RFC 3263 4.3), and for a REGISTER the P-CSCF moves to the next I-CSCF of its own accord as well (TS 24.229 5.2.2.1 item 7). With several instances of the I-CSCF the REGISTER would go to the next one and register the user as usual. The DNS zone of this network names one I-CSCF, so none is left.
- The P-CSCF answers **504 Server Time-out**: TS 24.229 5.2.2.1 item 7 has it send one when it fails to forward the REGISTER to any I-CSCF. It does not pass the 503 on, for the same reason the I-CSCF does not pass on the 503 of the S-CSCF.
- An initial INVITE into the home network, from another network or from the originating S-CSCF, gets the same 503, and its sender tries the next I-CSCF (RFC 3263 4.3). With none left, the originating S-CSCF or the IBCF answers **500 Server Internal Error**, as a proxy does not pass a 503 on (RFC 3261 16.7 step 6); the 504 is for a REGISTER only. The I-CSCF does not count the INVITE it refused in `ims_session_attempts_total{role="icscf",direction="terminating"}`: the next I-CSCF, which serves it, counts it, so the instances add up to the attempts of the network.
- A CANCEL for an INVITE that the I-CSCF was already proxying when it was shut down still cancels that INVITE: the CANCEL is not a new request, and the SIP stack answers it with the transaction of the INVITE.

In Wireshark, `sip.Status-Code == 503 && ip.src == 172.28.0.20` shows the 503 of the I-CSCF, and `sip.Status-Code == 504` the 504 that the P-CSCF sends the UE.

3. Unlock the I-CSCF with the same request and `UNLOCKED`:

   ```sh
   docker compose exec icscf sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/icscf/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The next `ue register` registers as before.

`scripts/e2e-maintenance-sip.sh` runs these steps as well, and checks the 503 and the 504 in the capture.

## Take the HSS out of service

The CSCFs and the application servers reach the HSS over Diameter, so an instance that goes out of service does not say so in an answer the way a CSCF does: it ends its Diameter connections instead. Every peer gets a **Disconnect-Peer-Request** (DPR) whose `Disconnect-Cause` is `REBOOTING`, and the HSS closes the connection once the peer's **Disconnect-Peer-Answer** (DPA) comes back (RFC 6733 5.4). Until the operator unlocks it, the node does not listen on port 3868 at all, so a peer that tries to open a connection is refused by the transport and tries again Tc later, 30 seconds here (RFC 6733 2.1).

`REBOOTING` is the cause that invites the peer back: a node that receives a DPR with `REBOOTING` may connect again, while `BUSY` and `DO_NOT_WANT_TO_TALK_TO_YOU` ask it not to (RFC 6733 5.4.3). The instance means to come back, so it sends `REBOOTING`, and its peers are there again at their next attempt after the unlock.

Holding no socket on 3868 departs from RFC 6733 2.1, which requires a node to be ready for connections on that port at all times. It is a deliberate choice for the time the HSS is out of service, and the `REBOOTING` it sent is what makes it fit: that cause says the node is about to reboot (5.4.3), and a node that is rebooting has nothing listening either, so a refused connection and another attempt after Tc are what the peer is told to expect. The alternatives are worse. A CEA that refuses the CER has no Result-Code that says a node is out of service: 5.3 gives `DIAMETER_NO_COMMON_APPLICATION`, `DIAMETER_NO_COMMON_SECURITY` and `DIAMETER_UNKNOWN_PEER`, and none of them is true of this node. Taking the connection and answering with another DPR repeats the CER, the CEA, the DPR and the DPA every Tc for as long as the HSS is out of service.

A commercial network runs several instances of the HSS, and each of them holds every subscriber (TS 29.228 6.4). A client keeps a connection to each and sends a Cx or an Sh request on the first one that is open, so the requests of a drained instance go to the next instance and no user has to register again. This network has one HSS, so while it is out of service the I-CSCF and the S-CSCF have none.

The node of the HSS serves the same maintenance API on port 8085, the listener of the `maintenance` section of `deploy/compose/hss.yaml`, beside the metrics that Prometheus scrapes there. Its `users` count the Cx and Sh requests it is answering. A shut down with none of them in flight makes it `LOCKED` at once; with one in flight it stays `SHUTTINGDOWN` until that answer is on its way, so the DPR follows the answer instead of cutting it off.

A `SHUTTINGDOWN` HSS keeps the connections it has, and its peers go on sending Cx and Sh requests over them, since nothing has told them to stop. The count of requests in flight therefore reaches zero when the traffic pauses, and under a steady stream of registrations it need not reach zero at all. A shut down may carry `timeout_seconds` for that: when that many seconds have passed the HSS becomes `LOCKED` with the requests it still has, as it does the moment the operator locks it instead of shutting it down. Both of those close the connections while requests are in flight, and the answer to such a request is then dropped rather than sent, so the peer that asked sees its connection close with no answer. Only a shut down that ends because the last request was answered sends its DPRs behind every answer the HSS owed.

`operational_state` stays `ENABLED`: the HSS answers from the subscribers it holds, so it needs no connection of its own to another node.

An instance out of service could answer every new request `DIAMETER_TOO_BUSY` instead, which tells the sender to try another server. It does not, because that answer is only for a request that asks for one named server which cannot serve it (RFC 6733 7.1.3). A request names a server in `Destination-Host`; the UAR, the LIR, the MAR, the SAR for a user who is not registered, and the Sh requests carry a `Destination-Realm` and no host, so any instance of the realm may answer them and `DIAMETER_TOO_BUSY` would be the wrong answer to give.

1. Shut the HSS down:

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The answer ends with the state of the HSS. No Cx or Sh request was in flight, so it is `LOCKED` at once:

   ```json
   {"role":"hss","administrative_state":"LOCKED","operational_state":"ENABLED","users":0}
   ```

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |              |              |  DPR, DPA     |              |
 |              |              |<---------------------------->|
 |              |              |               |  DPR, DPA    |
 |              |              |               |<------------>|
```

- The HSS sends the DPR, the peer answers the DPA, and the peer that received the DPA closes the transport connection (RFC 6733 5.4). The ladder has the two Cx connections; the IP-SM-GW and the MMTel AS hold an Sh connection each and get a DPR too, so four of these exchanges go out. `docker compose logs hss` shows `stopped accepting connections` after them.
- In Wireshark, `diameter.cmd.code == 282` shows the DPRs and the DPAs. The Disconnect-Cause AVP of each DPR reads `REBOOTING (0)`.
- The I-CSCF and the S-CSCF now have no instance of the HSS to take their requests. `docker compose exec icscf wget -qO- http://127.0.0.1:8085/maintenance/v1/state` shows `"operational_state":"DISABLED"` for the I-CSCF: it cannot work without the HSS, although the operator has not taken it out of service.
- Each of them tries to connect again every Tc, 30 seconds, and is refused until the unlock. `docker compose logs icscf` shows `connection failed` once per attempt. In Wireshark, `tcp.flags.reset == 1 && tcp.srcport == 3868` shows the resets with which the HSS refuses them.
- The first attempt that is refused makes each of them raise an alarm about the HSS, Tc after the DPR. The DPR alone raises none, as it asks the peer to come back. [Read the alarms about the HSS](#read-the-alarms-about-the-hss) shows the alarm.

2. Register the UE:

   ```sh
   docker compose run --rm ue register -deregister-after 5s
   ```

   The UE ends with `REGISTER answered 504 Server Time-out`.

```text
 UE           P-CSCF         I-CSCF          S-CSCF          HSS
 |  REGISTER    |              |               |              |
 |------------->|  REGISTER    |               |              |
 |              |------------->|               |              |
 |              |  480         |               |              |
 |              |<-------------|               |              |
 |  504         |              |               |              |
 |<-------------|              |               |              |
```

- The I-CSCF has no HSS to send the UAR to, so it cannot find out which S-CSCF serves the user, and answers **480 Temporarily Unavailable** (TS 24.229 5.3.1.3).
- The P-CSCF moves a REGISTER on to the next I-CSCF when it gets a 480 (TS 24.229 5.2.2.1 item 7). The DNS zone of this network names one I-CSCF, so none is left, and the P-CSCF answers the UE **504 Server Time-out**.
- In Wireshark, `sip.Status-Code == 480` shows the answer of the I-CSCF. No UAR goes out before it: the Cx connection is gone.

3. Unlock the HSS with the same request and `UNLOCKED`:

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"UNLOCKED\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   The node listens again, and the I-CSCF and the S-CSCF come back at their next attempt, up to Tc later. `docker compose logs icscf` shows a new CER, CEA and `"to":"I-Open (OKAY)"`; the next `ue register` registers as before. The CEA clears the alarm of each node.

The node binds port 3868 again as part of the unlock, and that can fail, if something else took the port while the HSS was out of service. The HSS is then `UNLOCKED` and reports `"operational_state":"DISABLED"`, and `docker compose logs hss` holds `cannot listen for Diameter connections again`. A second unlock does nothing, since the HSS is `UNLOCKED` already: lock it and unlock it again to have it try the port once more.

The administrative state belongs to the process of the node: a node that starts again is `UNLOCKED`. `scripts/e2e-maintenance-hss.sh` runs these steps as well, and checks the DPRs in the capture and the alarms of the nodes.

### Read the alarms about the HSS

Each node keeps a list of the faults that it has found, the alarm list of TS 28.111. A role raises an alarm when it detects a fault and clears it when the condition is gone; an operator reads the list and acknowledges alarms, but cannot clear one (TS 28.111 6.8). The I-CSCF, the S-CSCF, the IP-SM-GW, the MMTel AS and the Service Centre each raise one alarm for each instance of the HSS that they cannot reach. While the HSS is out of service, read the list of the S-CSCF on its maintenance API:

```sh
docker compose exec scscf wget -qO- 'http://127.0.0.1:8085/maintenance/v1/alarms?cleared=false'
```

```json
{"node":"scscf","instance":"3f1c0e2a9b7d","num_of_alarm_records":1,"last_modification":"2026-11-24T10:15:02Z","dropped_records":0,
 "alarm_records":[{"alarm_id":"9f3c2a7b1d4e6058","object_instance":"ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
  "role":"scscf","peer":"hss.ims.mnc001.mcc001.3gppnetwork.org","notification_id":4711093825561044,"alarm_raised_time":"2026-11-24T10:15:02Z",
  "alarm_type":"COMMUNICATIONS_ALARM","probable_cause":"COMMUNICATIONS_SUBSYSTEM_FAILURE","specific_problem":"Diameter peer down",
  "perceived_severity":"MAJOR","additional_text":"no transport connection to the peer","ack_state":"UNACKNOWLEDGED"}]}
```

| Field | Value | Why |
| --- | --- | --- |
| `object_instance` | `ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org` | The alarmed object is one instance of the HSS, by its Origin-Host. In a network with several instances, each one that is gone has an alarm of its own, which names the instance to repair. Whether the S-CSCF has an instance left is its `operational_state`, not an alarm. |
| `alarm_type` | `COMMUNICATIONS_ALARM` | A fault in conveying information from one point to another (ITU-T X.733 8.1.1). |
| `probable_cause` | `COMMUNICATIONS_SUBSYSTEM_FAILURE` | A failure of what carries the communications over a link (X.733 8.1.2.1). TS 28.111 lists it in Table B.2 with the communications alarm type, and spells it this way in its OpenAPI definition. |
| `specific_problem` | `Diameter peer down` | Tells the alarm apart from another of the same cause, such as one about a media relay. |
| `perceived_severity` | `MAJOR` | Service is affected and needs urgent action (X.733 8.1.2.3), but a node with another instance of the HSS still serves. |
| `additional_text` | `no transport connection to the peer` | What failed. Here the HSS refused the TCP connection. `no capabilities exchange with the peer` says that the connection opened and no CEA came back. |

The alarm comes when an attempt to open the connection fails, not when a connection closes. A node that loses a connection connects again after Tc (RFC 6733 2.1), and a peer that is back by then raises no alarm, so a short reconnect does not raise and clear one each time. The watchdog alone raises none either. A connection that it suspects (RFC 3539 3.4.1) is closed one Tw later and opened again after Tc, and the alarm comes only if that fails. Otherwise a peer that answers late would make the alarm come and go every few Tw. The attempts that fail after the first change nothing in the list.

A DPR is the other way an alarm comes. A peer that ends the connection with a DPR whose `Disconnect-Cause` is `BUSY` or `DO_NOT_WANT_TO_TALK_TO_YOU` asks not to be connected to again (RFC 6733 5.4.3), so the node makes no further attempt. The alarm then comes at once, with `additional_text` `the peer disconnected and asked not to be connected to again`, and is never cleared: no connection opens again until the node starts anew. The HSS of this network always sends `REBOOTING`, so its drain never raises the alarm this way.

The CEA of the next connection clears the alarm: `perceived_severity` becomes `CLEARED`, and `alarm_cleared_time` says when. The record stays in the list until an operator acknowledges it with `PUT /maintenance/v1/alarms/{alarm_id}/ack-state`, so the list still tells afterwards what happened. When nobody does within an hour, the node acknowledges it itself, with `ack_user_id` `retention` ([The HTTP APIs](apis.md#what-the-maintenance-api-leaves-out)). A node that starts before the HSS listens may already hold such a cleared record from its first attempt.

The OAM gathers the alarm lists of every node of its inventory:

```sh
docker compose exec oam wget -qO - 'http://127.0.0.1:8080/oam/v1/alarms?cleared=false'
```

`alarms` holds the alarm of each of the five nodes, with `node` beside each record, the most severe first. The OAM screen shows the same list in its alarm part. A node that does not answer is in `nodes` with the reason, and its alarms are unknown rather than absent: the OAM raises no alarm for a node that it cannot read.

The OAM also offers the same alarms to the operator's network management, the OSS, through the interface that 3GPP defines for fault supervision: one alarm list for the whole network, acknowledgements, and a notification of each change. [Hand the alarms to an OSS](oss.md) follows that interface with this drain of the HSS.

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

The registration test of `scripts/e2e-registration.sh` ends with such a UE, wherever the machine can run the simulator of the container image, and reads its whole registration out of the capture: the REGISTER from the gateway, the `received` and `rport` that the P-CSCF wrote into the UE's Via, the 401 and the 200 OK, and the NOTIFY of the UE's own subscription, which finds its way back through the NAT as well.

## What this project simplifies

These parts of the specifications are left out or shortened. Each has a `SIMPLIFIED:` comment in the code; `grep -rn "SIMPLIFIED:" internal cmd` lists them all.

| Where | What is left out | Why |
| --- | --- | --- |
| P-CSCF, TS 24.229 5.2.2.1 item 1 | The Path URI has no flow token and no `ob` parameter. | They belong to SIP outbound (RFC 5626), which this project does not implement. |
| P-CSCF, TS 24.229 5.2.2.1 item 6 | The term-ioi of the P-Charging-Vector of the 200 to the REGISTER is not stored. | It names the home network in the charging records of the P-CSCF, which no node of this project writes, and no later message carries it. |
| S-CSCF, TS 24.229 5.4.1.5 | The S-CSCF ends a registration on its own only when an application server fails the third-party REGISTER and its iFC says SESSION_TERMINATED. Otherwise only an RTR from the HSS ends one. It releases no sessions before the NOTIFY. | The maintenance API of the S-CSCF has no operation that ends a registration, and the S-CSCF keeps no state of the dialogs it routes. |
| S-CSCF, TS 29.228 6.1.3.1 | An RTR with NEW_SERVER_ASSIGNED or REMOVE_S-CSCF gets DIAMETER_UNABLE_TO_COMPLY. | The HSS stub sends only PERMANENT_TERMINATION and SERVER_CHANGE. The S-CSCF lets the profile of a user who is not registered go by itself after a few minutes. |
| Subscriber, RFC 6665 4.1.4 and 5.4.9 | A NOTIFY from a second dialog gets 481. | The reg event package forbids a subscription to fork (RFC 3680 4.9). |
| I-CSCF, TS 24.229 5.3.1.3 | No reselection of another S-CSCF when the S-CSCF does not answer. | The configuration names one S-CSCF. When it runs as several instances, a REGISTER that one leaves unanswered already goes on to the next. |
| UE, TS 24.229 9.2.1 | A UE without an access network, such as the UEs of this guide or a softphone on the host, has its P-CSCF address configured. A UE on the EPC stub (`ue -epc`) takes it from the PCO when it attaches; see [The VoLTE call flow](volte.md). | The softphone path stays, for softphones and for UEs that do not attach. |
| UE, TS 24.229 5.1.2A.1.6 | After a request that got no response, the UE registers again through the same P-CSCF at once. It does not mark the P-CSCF as unavailable or look for another one. | The UE knows one P-CSCF address, and finding the P-CSCFs again would give the same one. |
| UE, TS 24.229 5.1.1.2.1 and 5.1.1.4.1 | When Timer F fires for a REGISTER, or a 503 comes with a Retry-After longer than Timer F, the UE waits and registers through the same P-CSCF. It does not mark the P-CSCF as unavailable or look for another one. After a failed refresh it always makes an initial registration, also where a short Retry-After of a 503 would let it refresh. | The UE knows one P-CSCF address, and finding the P-CSCFs again would give the same one. No 503 with a short Retry-After reaches the UE: a P-CSCF that the operator has shut down answers 503 with a Retry-After longer than Timer F, and when the S-CSCF answers 503 with one, the I-CSCF answers 500 without it. |
| UE, TS 24.229 5.1.1.2.1 | max-time and base-time of the retry delay are always the defaults of RFC 5626 4.5. | The network can provide them only through a management object of TS 24.167, which the UE does not have. |
| HSS, TS 29.228 6.1.1.1 and 6.1.2.1 | One registration state per subscription, not per public identity. Some Server-Assignment-Type values are not handled. | Each test subscription is one implicit registration set, and the S-CSCF never sends the other values. |
| HSS, TS 29.228 6.1.2.1 and 6.1.3.1 | When a `PUT` gives a registered user another default identity, the HSS does not hold back the SARs of the user between writing the user Not Registered and the RTA. A re-registration whose SAR it answers in that moment has the user Registered again, and the RTR then ends that registration at the S-CSCF, so the HSS has the user Registered while the S-CSCF has no registration. | With SERVER_CHANGE the UE registers again at once, and that registration puts the two in step. With PERMANENT_TERMINATION, every identity barred, the UAR of the user is refused either way, and the state stays until a SAR for a terminating request, a `PUT` that gives the set a default identity again, or a `DELETE` changes it. Holding the lock of the subscriber over the RTR would close the gap, but the S-CSCF may be waiting for the answer to that very SAR before it takes up the RTR. |
| HSS, TS 29.228 6.2.2.1 | A PPA that reports an error leaves the registration state as it is: the HSS neither sets a user that the S-CSCF does not know to not registered nor deregisters the user with SERVER_CHANGE. | A push that failed does not take the user's registration away. The S-CSCF reads the profile again at the next registration, and asks the HSS anew for a user it does not know. |
| HSS, TS 29.228 6.5.2.3, 6.5.2.4 and 6.5.2.5 | A PPR carries the user profile alone: no charging addresses, no changed SIP Digest password, and none of the other user information that a PPR may update. | The HSS holds no charging addresses, and between two registrations the S-CSCF keeps no password of the user for a new one to replace. |
| HSS, TS 29.228 8.1.1 | When the MAR of another S-CSCF takes over a registered user, the HSS sends the old S-CSCF no RTR. | The I-CSCF gives every user the same S-CSCF and picks no other when that one does not answer, so no user moves to another S-CSCF in this network. The old S-CSCF ends the registration when it expires, which 8.1.1 also provides for, and the HSS then refuses its SAR. |
| User profile, TS 29.228 Annex E | One service profile. Of the optional elements, only the barring indication, the type of a public service identity and the iFCs. | The test subscriptions and PSI users need nothing more. |
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
| P-CSCF and UE, RFC 3680 5.2 | The version of the reginfo document is not checked. | The S-CSCF always sends the full state, and a NOTIFY that comes after a later one is refused for its CSeq. |
| P-CSCF, TS 24.229 5.2.6.2 | The UEs and the network reach the P-CSCF at the same address and port. A request counts as one from the network, and needs no IP association, when it is addressed to the URI that the P-CSCF put in Path, to the P-CSCF's own contact, or to the contact of a UE that it serves. | A real P-CSCF usually serves the UEs (Gm) and the network (Mw) on separate interfaces. The containers of this project share one network. |
| P-CSCF, TS 24.229 5.2.2.3 | The IP association is looked up by the address and port that a request over UDP comes from. A request over TCP, which a UE sends when the request is larger than 1300 bytes, is matched by the sent-by of its Via alone, when that has the address the request came from; the port of the connection is not checked. A TCP request whose sent-by names another address is not recognized. Nothing authenticates the port in that sent-by, so a host that shares the UE's IP address could name it. | Without IPsec a TCP connection comes from a port of the moment, which no IP association holds. |
| SIP Digest, TS 24.229 5.4.1.2.1B, RFC 7616 and RFC 8760 | The S-CSCF offers MD5 with qop=auth only, although the specifications prefer SHA-256 and keep MD5 for backward compatibility. | The HSS stub provides only the MD5 H(A1); SHA-256 needs the alternate digest AVPs. |
| Diameter, RFC 3539 3.4.1 and Appendix A, RFC 6733 5.6.4 | No `REOPEN` state: a reconnected peer is in service at once. A request pending on a connection that the watchdog suspects waits for its own timeout before it goes to another instance of the HSS. Only the configured initiator connects, so there is no election between two connections to the same peer. | A request that an instance leaves unanswered goes on to the next instance of the HSS, so an instance that is not yet steady delays requests rather than losing them. Which side connects is part of the configuration of each node. |
| OAM, TS 32.600 4.2.1.1 | The OAM API has no authentication and no authorisation, although only operators with the right authorisation may change the configuration of a network. The `operator` of a request is the name the caller gives, not a checked identity. Neither have the 3GPP management services that the OAM offers an OSS: any client that reaches them reads and acknowledges the alarms, and can create a subscription with any http or https address, to which the OAM then posts the alarm notifications. | Its listener is on the network of the nodes alone and on no address of the host, as that of the maintenance API of a node is, and every interface of this project runs in the clear so that a capture can be read. |
| OAM, TS 33.102 6.3.1 | The archive of a backup is not encrypted; the permissions of its file are its only protection. | The long-term key is available only to the USIM and to the authentication centre of the home network, so a copy of it outside the HSS must be as well kept as that centre, and a product encrypts such an archive. Only test credentials are in this network, and encryption would need a key of its own and the handling of that key, which is not what a backup and a restore teach. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
```

This stops the nodes and the UEs of every profile and keeps the images, so the next guide starts without a new build. To delete the images as well, see [Stop the system](capture.md#stop-the-system).
