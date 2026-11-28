---
title: A UE registers with the IMS
footer: ims-practice 4. Registration
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue, label: UE, role: the phone that registers, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: its first hop, at: [1, 1]}
  - {id: dns, label: DNS, role: resolves names, at: [1, 0]}
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: hss, label: HSS, role: subscriber data, at: [3, 0]}
  - {id: scscf, label: S-CSCF, role: authenticates and registers, at: [3, 1]}
  - {id: ipsmgw, label: IP-SM-GW, role: the AS for SMS, at: [4, 1]}
events:
  # The first REGISTER (steps 1 to 4)
  - id: reg1-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (1st)
    fields:
      - Request-URI: "`sip:ims.mnc001.mcc001.3gppnetwork.org`, the home network domain"
      - From, To: the public identity it registers
      - Contact: "A UUID as the user part, `+g.3gpp.smsip` and `expires=600000`"
      - Authorization: "The private identity in `username`; `nonce` and `response` empty"
  - id: dns-naptr
    msg: NAPTR
    from: pcscf
    to: dns
    proto: DNS
    note: (query)
    fields:
      - name: "`ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-naptr-ans
    msg: NAPTR
    from: dns
    to: pcscf
    proto: DNS
    note: (answer)
    fields:
      - answer: "SIP over UDP is at `_sip._udp.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-srv
    msg: SRV
    from: pcscf
    to: dns
    proto: DNS
    note: (query)
    fields:
      - name: "`_sip._udp.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-srv-ans
    msg: SRV
    from: dns
    to: pcscf
    proto: DNS
    note: (answer)
    fields:
      - answer: "`icscf.ims.mnc001.mcc001.3gppnetwork.org`, port 5060"
  - id: dns-a
    msg: A
    from: pcscf
    to: dns
    proto: DNS
    note: (query)
    fields:
      - name: "`icscf.ims.mnc001.mcc001.3gppnetwork.org`"
  - id: dns-a-ans
    msg: A
    from: dns
    to: pcscf
    proto: DNS
    note: (answer)
    fields:
      - answer: 172.28.0.20
  - id: pcscf-path
    do: adds headers
    at: pcscf
    fields:
      - Path: "`sip:term@pcscf...`, where incoming requests come back"
      - Require: "`path`"
      - P-Charging-Vector: "A new `icid-value`"
      - P-Visited-Network-ID: the name of the network of the P-CSCF
  - id: reg1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (1st)
  - id: uar1
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (1st)
    fields:
      - User-Name: the private identity
      - Public-Identity: the public identity
      - Visited-Network-Identifier: the network of the P-CSCF
      - User-Authorization-Type: REGISTRATION (0)
  - id: uaa1
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (1st)
    fields:
      - Experimental-Result: DIAMETER_FIRST_REGISTRATION (2001)
      - Server-Capabilities: mandatory capability 1, not the name of an S-CSCF
  - id: icscf-select
    do: chooses the S-CSCF
    at: icscf
    fields:
      - capabilities asked for: mandatory capability 1
      - configured candidate: "`scscf.ims.mnc001.mcc001.3gppnetwork.org` has capability 1"
      - choice: that S-CSCF; the I-CSCF does not remember it
  - id: reg1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (1st)
    fields:
      - Request-URI: "rewritten to `sip:scscf.ims.mnc001.mcc001.3gppnetwork.org:5060`"
      - Via: three, of the UE, the P-CSCF and the I-CSCF
  # The challenge (steps 5 and 6)
  - id: mar
    msg: MAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - 3GPP-SIP-Authentication-Scheme: "`Unknown`: the HSS is to choose the scheme"
  - id: maa
    msg: MAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - 3GPP-SIP-Authentication-Scheme: "`SIP Digest`"
      - Digest-Realm: the realm
      - Digest-Algorithm: "`MD5`"
      - Digest-QoP: "`auth`"
      - Digest-HA1: H(A1), not the password itself
  - id: hss-assign
    do: stores the S-CSCF of the user
    at: hss
    fields:
      - what it stores: this S-CSCF serves the user; authentication pending
      - used: in the answer to the second UAR
  - id: scscf-challenge
    do: makes a nonce, starts a timer
    at: scscf
    fields:
      - nonce: made by the S-CSCF
      - reg-await-auth: 4 minutes
  - id: r401-s-i
    msg: "401"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - WWW-Authenticate: 'the realm, a `nonce`, `algorithm=MD5`, `qop="auth"`'
  - id: r401-i-p
    msg: "401"
    from: icscf
    to: pcscf
    proto: SIP
  - id: r401-p-ue
    msg: "401"
    from: pcscf
    to: ue
    proto: SIP
    fields:
      - Via: only the Via of the UE is left
  # The second REGISTER (steps 7 and 8)
  - id: reg2-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (2nd)
    fields:
      - Call-ID: that of the first REGISTER
      - CSeq: one higher
      - Authorization: "with `nonce`, `cnonce`, `nc`, `qop` and `response`"
  - id: pcscf-pending
    do: adds ip-assoc-pending
    at: pcscf
    fields:
      - added to Authorization: '`integrity-protected="ip-assoc-pending"`'
      - meaning: an answer to a challenge, with no IP association yet
  - id: reg2-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (2nd)
  - id: uar2
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (2nd)
    fields:
      - User-Authorization-Type: REGISTRATION (0)
  - id: uaa2
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (2nd)
    fields:
      - Experimental-Result: DIAMETER_SUBSEQUENT_REGISTRATION (2002)
      - Server-Name: the S-CSCF that the HSS stored at the MAR
  - id: reg2-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (2nd)
    fields:
      - transport: TCP, as it is over 1300 bytes
      - Via: "the Via of the I-CSCF says `SIP/2.0/TCP`"
  # The registration is accepted (steps 9 and 10)
  - id: scscf-verify
    do: checks the answer
    at: scscf
    fields:
      - Call-ID: that of the challenged REGISTER?
      - realm and nonce: its own?
      - response: equal to the value computed from H(A1)?
  - id: sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: REGISTRATION (1)
  - id: saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - User-Data: "the user profile in XML; Wireshark says `Cx-User-Data`"
      - contents: the identities registered together, and the iFCs
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Path: the route that the S-CSCF stored
      - Service-Route: "`sip:orig@scscf...`"
      - P-Associated-URI: the identities registered together; the first is the default
      - Contact: "`+g.3gpp.smsip` and `expires=3600`"
      - Authentication-Info: "`rspauth`, proof that the network knows H(A1) too"
  - id: ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: pcscf-store
    do: keeps the routes and the association
    at: pcscf
    fields:
      - IP association: made for the address of the UE
      - it stores: the Service-Route and the identities
  - id: ok-p-ue
    msg: 200 OK
    from: pcscf
    to: ue
    proto: SIP
  # The routes used after the registration
  - id: route-path-store
    do: Stores the Path
    at: scscf
    fields:
      - What it stores: "The Contact and the Path, `sip:term@pcscf...`"
      - When it is used: When a request for this UE goes out
  - id: route-sr-store
    do: Stores the Service-Route
    at: ue
    fields:
      - What it stores: "The Service-Route of the 200 OK, `sip:orig@scscf...`"
      - When it is used: When the UE sends a new request
  - id: mt-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (incoming)
    fields:
      - Request-URI: The Contact that the UE registered
      - Route: "The stored Path, `sip:term@pcscf...`"
  - id: mt-p-ue
    msg: INVITE
    from: pcscf
    to: ue
    proto: SIP
    note: (incoming)
    fields:
      - Route: The P-CSCF removes its own value, and none is left
  - id: mo-ue-p
    msg: INVITE
    from: ue
    to: pcscf
    proto: SIP
    note: (outgoing)
    fields:
      - Request-URI: "The number called, `tel:+1-202-555-0102`"
      - Route: "The URI of the P-CSCF, then the stored Service-Route, `sip:orig@scscf...`"
  - id: mo-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (outgoing)
    fields:
      - Route: The P-CSCF removes its own value and checks that the rest is the Service-Route
  # The subscription of the P-CSCF (steps 11 and 12)
  - id: psub-p-i
    msg: SUBSCRIBE
    from: pcscf
    to: icscf
    proto: SIP
    fields:
      - Request-URI, To: the default public identity
      - Event: "`reg`"
      - Expires: 3761, longer than the 3600 s of the registration
      - P-Asserted-Identity: "`sip:term@pcscf...`"
  - id: lir1
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
  - id: lia1
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    fields:
      - Server-Name: the S-CSCF of the user
  - id: psub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: psub-ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: psub-ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: pnotify
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Subscription-State: "`active;expires=3761`"
      - Content-Type: "`application/reginfo+xml`"
      - body: a registration per identity, a contact per binding
  # The subscription of the UE (steps 13 and 14)
  - id: usub-ue-p
    msg: SUBSCRIBE
    from: ue
    to: pcscf
    proto: SIP
    fields:
      - Event: "`reg`"
      - Expires: 600000
      - Route: the P-CSCF and the Service-Route
  - id: usub-p-s
    msg: SUBSCRIBE
    from: pcscf
    to: scscf
    proto: SIP
    fields:
      - route: along the Service-Route to the S-CSCF, not through the I-CSCF
  - id: unotify-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Route: the P-CSCF, from the Record-Route of the SUBSCRIBE
  - id: unotify-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    fields:
      - Record-Route: added by the P-CSCF; the UE builds the route of the subscription from it
  # The third-party REGISTER (steps 15 to 18)
  - id: tp-register
    msg: REGISTER
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (third-party)
    fields:
      - Request-URI: "the ServerName of the iFC, `sip:ipsmgw...:5060`"
      - From, Contact: S-CSCF
      - To: the default public identity
      - Expires: 3600
      - body: "`application/3gpp-ims+xml` with the MSISDN `12025550101`"
  - id: tp-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
  - id: isub-ip-i
    msg: SUBSCRIBE
    from: ipsmgw
    to: icscf
    proto: SIP
    fields:
      - Expires: 3761
      - P-Asserted-Identity: the URI of the IP-SM-GW, the ServerName of the iFC
  - id: lir2
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
  - id: lia2
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
  - id: isub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: inotify
    msg: NOTIFY
    from: scscf
    to: ipsmgw
    proto: SIP
  - id: ipsmgw-check
    do: looks for a binding that takes SMS
    at: ipsmgw
    fields:
      - it looks for: 'an active contact with `<unknown-param name="+g.3gpp.smsip">`'
  - id: pur
    msg: PUR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - User-Identity: the MSISDN `12025550101`
      - Data-Reference: SMSRegistrationInfo (24)
      - Sh-User-Data: "`12025550199` in `<IP-SM-GW-Number>`"
  - id: pua
    msg: PUA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  # Deregistration
  - id: dreg-ue-p
    msg: REGISTER
    from: ue
    to: pcscf
    proto: SIP
    note: (deregister)
    fields:
      - Contact: "`expires=0`"
  - id: dreg-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (deregister)
    fields:
      - added to Authorization: '`integrity-protected="ip-assoc-yes"`'
  - id: duar
    msg: UAR
    from: icscf
    to: hss
    proto: Cx
    note: (deregister)
    fields:
      - User-Authorization-Type: DE_REGISTRATION (1)
  - id: duaa
    msg: UAA
    from: hss
    to: icscf
    proto: Cx
    note: (deregister)
  - id: dreg-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (deregister)
  - id: dsar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (deregister)
    fields:
      - Server-Assignment-Type: USER_DEREGISTRATION (5)
  - id: dsaa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    note: (deregister)
  - id: dok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (deregister)
  - id: dok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
    note: (deregister)
  - id: dok-p-ue
    msg: 200 OK
    from: pcscf
    to: ue
    proto: SIP
    note: (deregister)
    fields:
      - Contact: "`expires=0`"
  - id: dnotify-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    note: (last)
    fields:
      - registration and contact: "`terminated`"
      - event of the contact: "`unregistered`"
      - Subscription-State: "`terminated;reason=noresource`"
  - id: dnotify-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    note: (last)
  # Failures
  - id: wp-verify
    do: finds a response that does not match
    at: scscf
  - id: wp-403-s-i
    msg: "403"
    from: scscf
    to: icscf
    proto: SIP
  - id: wp-403-i-p
    msg: "403"
    from: icscf
    to: pcscf
    proto: SIP
  - id: wp-403-p-ue
    msg: "403"
    from: pcscf
    to: ue
    proto: SIP
  - id: wp-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (failure)
    fields:
      - Server-Assignment-Type: AUTHENTICATION_FAILURE (9)
  - id: wp-clear
    do: forgets the stored S-CSCF
    at: hss
    fields:
      - next attempt: starts again from DIAMETER_FIRST_REGISTRATION
  - id: rtr
    msg: RTR
    from: hss
    to: scscf
    proto: Cx
    fields:
      - Reason-Code: PERMANENT_TERMINATION (0)
      - User-Name: the private identity
  # A deregistration that comes late (an example to explain the rule)
  - id: ord-stored
    do: Holds the registration
    at: scscf
    fields:
      - Call-ID: A (a long value, written short for the example)
      - CSeq: 5 (stored by the re-registration)
      - Contact: C
      - Expiry: The one that the re-registration extended
  - id: ord-late
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (late deregistration)
    fields:
      - Call-ID: A
      - CSeq: "3"
      - Contact: "C with `expires=0`"
  - id: ord-compare
    do: Compares the order
    at: scscf
    fields:
      - Call-ID: A, the same as the stored one
      - CSeq: 3 is not higher than the stored 5
      - Decision: This request does not change the registration
  - id: ord-500
    msg: "500"
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Status code: RFC 3261 10.3 names none. This implementation answers 500
  - id: ord-kept
    do: Keeps the registration as it is
    at: scscf
    fields:
      - Contact and expiry: Unchanged
      - CSeq: Still 5
      - HSS: No SAR for a deregistration
  - id: ord-other
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (another Call-ID)
    fields:
      - Call-ID: B
      - CSeq: 2 (the answer to the 401)
  - id: ord-other-compare
    do: Does not compare the CSeq
    at: scscf
    fields:
      - Call-ID: B, not the stored A
      - Decision: Checks the answer and goes on as a re-registration
  - id: ord-other-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: RE_REGISTRATION (2)
  - id: ord-other-saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
  - id: ord-other-ok
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Values stored: Call-ID B and CSeq 2. The next request is compared with them
  - id: rej-s-p
    msg: NOTIFY
    from: scscf
    to: pcscf
    proto: SIP
    note: (rejected)
    fields:
      - event of the contact: "`rejected`"
  - id: rej-p-ue
    msg: NOTIFY
    from: pcscf
    to: ue
    proto: SIP
    note: (rejected)
  - id: rta
    msg: RTA
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
```


---

# A UE registers with the IMS

(registration with SIP Digest, and the third-party REGISTER to the application server)

---

[layout: summary]

## What this deck covers

Between switching a phone on and its first call, the phone registers with the IMS. This deck shows that registration one stretch at a time.

- The three things registration settles: where the phone is, who the user is, and the route of later requests.
- Why there are two REGISTERs.
- What the I-CSCF and the S-CSCF ask the HSS.
- Which headers of the 200 OK the later calls and short messages use.
- What follows registration: the subscriptions to the registration state, the third-party REGISTER, deregistration, expiry, and removal by the network.
- The call deck uses the Path, the Service-Route and the IP association that are set up here.

> Book: [Three things that registration settles](../../../docs/book/en/registration.md), [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md)
> Guide: [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md) (`docs/guide/en/registration.md`)
> The editions are those of Release 18: TS 24.229 V18.11.0, TS 29.228 V18.0.0, TS 29.229 V18.1.0.

---

## The nodes

```ims-scene
id: nodes
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The last number of an address tells you the node.
steps:
  - id: ue
    title: The UE (172.28.0.100)
    focus: [ue]
    text: |
      It sends REGISTER and answers the challenge.
      It is a softphone without a SIM; its identities and password are configured.
  - id: pcscf
    title: The P-CSCF (172.28.0.40)
    focus: [pcscf]
    text: The first hop of the UE. It adds Path.
  - id: dns
    title: The DNS (172.28.0.2)
    focus: [dns]
    checkpoint: true
    text: It holds NAPTR, SRV and A of the home network domain. The P-CSCF finds the I-CSCF here.
  - id: icscf
    title: The I-CSCF (172.28.0.20)
    focus: [icscf]
    text: The entry point of the home network. It asks the HSS for the S-CSCF.
  - id: hss
    title: The HSS (172.28.0.10)
    focus: [hss]
    text: The subscriber database. It answers the I-CSCF and the S-CSCF over Cx.
  - id: scscf
    title: The S-CSCF (172.28.0.30)
    focus: [scscf]
    text: |
      The registrar. It authenticates the user and stores the registration.
      The HSS is 10, the I-CSCF 20, the S-CSCF 30, the P-CSCF 40.
```

- The UE is a softphone without a SIM. Its identities are configured in it, as an IMC holds them (TS 24.229 5.1.1.1B.1), so it uses the public identity it registers in its other requests too. That identity is the SIP URI of the user's number, and it is not barred. A VoLTE phone registers a barred temporary identity instead; the VoLTE deck shows it.

| Node | Address | What it does in registration |
| --- | --- | --- |
| UE simulator | 172.28.0.100 | Sends REGISTER and answers the challenge. |
| P-CSCF | 172.28.0.40 | The first hop of the UE. It adds Path. |
| I-CSCF | 172.28.0.20 | The entry point of the home network. It asks the HSS for the S-CSCF. |
| S-CSCF | 172.28.0.30 | The registrar. It authenticates and stores the registration. |
| HSS | 172.28.0.10 | The subscriber database. It answers over Cx. |
| DNS | 172.28.0.2 | Holds NAPTR, SRV and A of the home network domain. |

> Guide: ["The nodes"](../../../docs/guide/en/registration.md#the-nodes), which also gives the identities and the password of the test user.

---

## The shape of the whole

```ims-scene
id: overview
view: ladder
nodes: [ue, pcscf, icscf, scscf, hss]
lead: The first REGISTER says who. The second proves it.
steps:
  - id: first
    title: The first REGISTER reaches the I-CSCF (1, 2)
    show: [reg1-ue-p, reg1-p-i]
    detail: []
    text: The UE sends REGISTER to the P-CSCF, which relays it to the I-CSCF, the entry of the home network.
  - id: ask
    title: The I-CSCF asks the HSS and sends it on to the S-CSCF (3, 4)
    show: [uar1, uaa1, reg1-i-s]
    active: uar1
    detail: []
    text: The first question to the HSS. The I-CSCF chooses the S-CSCF from the answer.
  - id: challenge
    title: The S-CSCF challenges (5, 6)
    show: [mar, maa, r401-s-i, r401-i-p, r401-p-ue]
    active: r401-p-ue
    detail: []
    checkpoint: true
    text: |
      The S-CSCF gets the authentication data from the HSS and puts a question to the UE in a 401.
      The 401 is not a failure; it is a step of the procedure.
  - id: answer
    title: The second REGISTER (7, 8)
    show: [reg2-ue-p, reg2-p-i, uar2, uaa2, reg2-i-s]
    active: reg2-i-s
    detail: []
    text: The UE answers the question. The I-CSCF asks the HSS once more.
  - id: done
    title: The registration is settled (9, 10)
    show: [sar, saa, ok-s-i, ok-i-p, ok-p-ue]
    active: ok-p-ue
    detail: []
    checkpoint: true
    text: |
      The HSS is asked four times: two UARs, one MAR, one SAR.
      The full ladder of the guide has DNS as well.
  - id: settled
    title: The three things registration settles
    active: ok-s-i
    detail: [ok-s-i]
    text: The Contact says where the UE is, the second answer proves who it is, and Path and Service-Route are the routes. P-Associated-URI lists the identities registered with it.
```

The same shape in a capture also has the DNS lookups, between 1 and 2. The next slides show it one stretch at a time, with the numbers of the guide's ladder.

```ladder
UE, P-CSCF, I-CSCF, S-CSCF, HSS
UE -> P-CSCF -> I-CSCF: REGISTER // 1 - 2
I-CSCF <-> HSS: UAR, UAA // 3
I-CSCF -> S-CSCF: REGISTER // 4
S-CSCF <-> HSS: MAR, MAA // 5
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 401 // 6
UE -> P-CSCF -> I-CSCF: REGISTER + response // 7
I-CSCF <-> HSS: UAR, UAA // 8
I-CSCF -> S-CSCF: REGISTER
S-CSCF <-> HSS: SAR, SAA // 9
UE <-- P-CSCF <-- I-CSCF <-- S-CSCF: 200 OK // 10
```

> Book: [Three things that registration settles](../../../docs/book/en/registration.md), [Why a UE sends REGISTER twice](../../../docs/book/en/registration-two-registers.md)

---

## The first REGISTER, and DNS finds the I-CSCF (1, 2)

```ims-scene
id: first-register
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: A REGISTER is addressed to a domain, not to a server. The network decides which server gets it.
steps:
  - id: send
    title: The UE sends REGISTER
    show: [reg1-ue-p]
    checkpoint: true
    text: |
      The Request-URI is the home network domain.
      Authorization already tells which private identity is to be authenticated.
  - id: naptr
    title: The P-CSCF looks up the NAPTR of the domain
    show: [dns-naptr, dns-naptr-ans]
    active: dns-naptr-ans
    detail: [dns-naptr, dns-naptr-ans]
    text: As RFC 3263 describes. The answer tells under which name SIP over UDP is offered.
  - id: srv
    title: It looks up the SRV of that name
    hide: [dns-naptr, dns-naptr-ans]
    show: [dns-srv, dns-srv-ans]
    active: dns-srv-ans
    detail: [dns-srv, dns-srv-ans]
    checkpoint: true
    text: The answer before decides the name to look up. This answer is the name of the I-CSCF and port 5060.
  - id: answer
    title: It looks up the A of that name, and the address of the I-CSCF is found
    hide: [dns-srv, dns-srv-ans]
    show: [dns-a, dns-a-ans]
    active: dns-a-ans
    detail: [dns-a, dns-a-ans]
    checkpoint: true
    text: The entry of the home network is port 5060 at 172.28.0.20. Each answer decides the name of the next query.
  - id: add
    title: The P-CSCF adds headers
    show: [pcscf-path]
    focus: [pcscf]
    text: |
      Path is the route that later brings incoming requests to the UE.
      These four headers are what differs between the REGISTER before and after the P-CSCF.
  - id: forward
    title: The REGISTER reaches the I-CSCF
    show: [reg1-p-i]
    text: From here on it is inside the home network. No S-CSCF serves the user yet.
```

| Look at | What to see |
| --- | --- |
| Request-URI of the UE's REGISTER | The home network domain, not a server. |
| Authorization | The private identity in `username`. `nonce` and `response` are empty. |
| Contact | A UUID as the user part, `+g.3gpp.smsip` and `expires=600000`. |
| Three DNS lookups | NAPTR, SRV, then A lead to the I-CSCF. |
| Headers the P-CSCF adds | Path, Require: path, P-Charging-Vector, P-Visited-Network-ID. |

The filter is `dns.qry.type == 35 || dns.qry.type == 33`.

- These four headers, which the P-CSCF adds, are what differs between the REGISTER before and after it. Path is what later brings an incoming call to the UE.

> Guide: [1](../../../docs/guide/en/registration.md#1-the-ue-sends-the-first-register) and [2](../../../docs/guide/en/registration.md#2-the-p-cscf-finds-the-i-cscf-and-forwards-the-register). TS 24.229 5.1.1.2.1, 5.1.1.2.3, 5.2.2.1. RFC 3263, RFC 3327, RFC 3581.

---

## The I-CSCF asks the HSS with a UAR (3, 4)

```ims-scene
id: select
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The first UAA returns capabilities, not the name of an S-CSCF. The I-CSCF chooses.
steps:
  - id: arrive
    title: The I-CSCF does not know the S-CSCF
    only: [reg1-ue-p, reg1-p-i]
    active: reg1-p-i
    focus: [icscf]
    zoom: [icscf, hss, scscf]
    text: The I-CSCF is an entry point that keeps no state. It does not remember who serves this user.
  - id: uar
    title: It asks the HSS (UAR)
    show: [uar1]
    zoom: [icscf, hss, scscf]
    text: Cx command 300. It carries the private identity, the public identity and the network of the P-CSCF.
  - id: uaa
    title: The HSS returns the capabilities needed (UAA)
    show: [uaa1]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      No S-CSCF serves the user yet, so the HSS returns Server-Capabilities instead of a name.
      The 2001 is inside Experimental-Result. In Result-Code, 2001 would be DIAMETER_SUCCESS.
  - id: choose
    title: The I-CSCF chooses the S-CSCF
    show: [icscf-select]
    focus: [icscf]
    zoom: [icscf, hss, scscf]
    text: |
      From its configured list it picks an S-CSCF that has every mandatory capability.
      This network configures one.
  - id: forward
    title: It forwards to the S-CSCF it chose
    show: [reg1-i-s]
    text: |
      The Request-URI becomes the S-CSCF, and there are three Vias.
      The I-CSCF does not remember the choice. The HSS does, at the MAR of the next step.
```

| Look at | What to see |
| --- | --- |
| AVPs of the UAR | User-Name, Public-Identity, Visited-Network-Identifier, User-Authorization-Type. |
| Result of the UAA | DIAMETER_FIRST_REGISTRATION (2001), inside Experimental-Result. |
| Server-Capabilities of the UAA | Capabilities instead of the name of an S-CSCF. |
| The REGISTER the I-CSCF forwards | The Request-URI is the chosen S-CSCF. Three Vias. |

The filter is `diameter.cmd.code == 300`.

- A trap: 2001 in Result-Code is DIAMETER_SUCCESS. Inside Experimental-Result it is DIAMETER_FIRST_REGISTRATION.
- Why does the I-CSCF not remember the S-CSCF itself? It is an entry point that keeps no state; the HSS keeps it.

> Guide: [3](../../../docs/guide/en/registration.md#3-the-i-cscf-asks-the-hss-uar-and-uaa) and [4](../../../docs/guide/en/registration.md#4-the-i-cscf-forwards-the-register-to-the-s-cscf). TS 24.229 5.3.1.2. TS 29.228 6.1.1, 6.7. TS 29.229 6.2.1.1.

---

## The S-CSCF challenges the UE (5, 6)

```ims-scene
id: challenge
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The HSS never hands over the password. It hands over H(A1).
steps:
  - id: mar
    title: The S-CSCF asks for authentication data (MAR)
    only: [reg1-i-s, mar]
    zoom: [icscf, hss, scscf]
    text: The scheme is `Unknown`. The REGISTER has no integrity-protected, so the HSS is to choose.
  - id: assign
    title: The HSS stores the S-CSCF of the user
    show: [hss-assign]
    focus: [hss]
    zoom: [icscf, hss, scscf]
    text: |
      With the MAR the HSS stores this S-CSCF as the one that serves the user, with authentication pending.
      The registration is not done yet. The second UAR uses what is stored here.
  - id: maa
    title: The HSS returns the SIP Digest data (MAA)
    show: [maa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: H(A1) is a hash of the user name, the realm and the password. With it alone the S-CSCF can check the answer of the UE.
  - id: nonce
    title: The S-CSCF makes its question
    show: [scscf-challenge]
    focus: [scscf]
    text: It makes a nonce and starts the timer reg-await-auth (4 minutes).
  - id: back
    title: The 401 goes back to the UE
    show: [r401-s-i, r401-i-p, r401-p-ue]
    text: |
      It goes back through the I-CSCF and the P-CSCF. Each removes its own Via.
      The 401 is not a failure. It is the question the UE has to answer.
```

| Look at | What to see |
| --- | --- |
| 3GPP-SIP-Authentication-Scheme of the MAR | `Unknown`: the HSS is to choose the scheme. |
| SIP-Digest-Authenticate of the MAA | Digest-Realm, Digest-Algorithm, Digest-QoP, Digest-HA1. |
| WWW-Authenticate of the 401 | The realm, a `nonce`, `algorithm=MD5`, `qop="auth"`. |
| Via of the 401 | One fewer at each hop. The UE gets only its own. |

The filter is `diameter.cmd.code == 303 || sip.Status-Code == 401`.

- The HSS never sends the password. It sends H(A1).
- With the 401 the S-CSCF starts the timer reg-await-auth, 4 minutes (step 6 of the guide).

> Guide: [5](../../../docs/guide/en/registration.md#5-the-s-cscf-asks-for-authentication-data-mar-and-maa) and [6](../../../docs/guide/en/registration.md#6-the-s-cscf-challenges-the-ue-401). TS 24.229 5.4.1.1, 5.4.1.2.1, 5.4.1.2.1B. TS 29.228 6.3. RFC 2617.

---

## The second REGISTER (7, 8)

```ims-scene
id: second-register
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The second REGISTER reaches the same S-CSCF because the HSS remembers it.
steps:
  - id: answer
    title: The UE answers the question
    only: [reg2-ue-p]
    checkpoint: true
    text: It computes the response from the password and the nonce and puts it in a second REGISTER. The Call-ID is that of the first.
  - id: pending
    title: The P-CSCF adds ip-assoc-pending
    show: [pcscf-pending]
    focus: [pcscf]
    text: It tells the S-CSCF that this answers a challenge, and that there is no IP association yet.
  - id: entry
    title: Through the I-CSCF again
    show: [reg2-p-i]
    checkpoint: true
    text: Every REGISTER goes through the I-CSCF, which does not remember its earlier choice.
  - id: uar
    title: It asks the HSS again
    show: [uar2]
    zoom: [icscf, hss, scscf]
    text: The second UAR. The HSS stored the S-CSCF at the MAR.
  - id: uaa
    title: This time a Server-Name comes back
    show: [uaa2]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: DIAMETER_SUBSEQUENT_REGISTRATION (2002) and the name of the S-CSCF it stored.
  - id: compare
    title: The first and the second UAA side by side
    show: [uaa1]
    active: uaa2
    detail: [uaa1, uaa2]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: |
      The first returned capabilities, and the I-CSCF chose.
      The second returns a name, and the I-CSCF follows it.
  - id: forward
    title: To the same S-CSCF, over TCP this time
    hide: [uaa1]
    show: [reg2-i-s]
    text: With the authentication data it is over 1300 bytes, so the I-CSCF sends it over TCP (RFC 3261 18.1.1).
```

| Look at | What to see |
| --- | --- |
| Call-ID and CSeq | The Call-ID of the first REGISTER. CSeq is one higher. |
| Authorization | Now with `nonce`, `cnonce`, `nc`, `qop` and `response`. |
| Parameter the P-CSCF adds | `integrity-protected="ip-assoc-pending"`. |
| The second UAA | DIAMETER_SUBSEQUENT_REGISTRATION (2002) and Server-Name. |
| Transport after the I-CSCF | TCP, because the request is over 1300 bytes. Via says `SIP/2.0/TCP`. |

- Why does the second REGISTER reach the same S-CSCF? The HSS stored the S-CSCF at the MAR and returns it as Server-Name in the second UAA.

> Guide: [7](../../../docs/guide/en/registration.md#7-the-ue-answers-the-challenge) and [8](../../../docs/guide/en/registration.md#8-the-i-cscf-asks-the-hss-again). TS 24.229 5.1.1.5.4, 5.2.2.3. TS 29.228 6.1.1.1. RFC 3261 18.1.1.

---

## SAR and the 200 OK (9, 10)

```ims-scene
id: accept
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: Only when the answer checks out does the S-CSCF tell the HSS and return 200 OK.
steps:
  - id: verify
    title: The S-CSCF checks the answer
    only: [reg2-i-s, scscf-verify]
    focus: [scscf]
    text: It checks three things. The Call-ID, the realm and the nonce, and the response.
  - id: sar
    title: It tells the HSS (SAR)
    show: [sar]
    zoom: [icscf, hss, scscf]
    text: Server-Assignment-Type is REGISTRATION (1). The HSS records that the user is registered at this S-CSCF.
  - id: saa
    title: It receives the profile (SAA)
    show: [saa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: User-Data holds the identities registered together and the iFCs. An iFC says which application servers join a request.
  - id: ok
    title: The 200 OK goes to the I-CSCF
    show: [ok-s-i]
    text: Everything that the UE and the P-CSCF use later is in this 200 OK.
  - id: relay
    title: The P-CSCF keeps the routes
    show: [ok-i-p, pcscf-store]
    active: pcscf-store
    focus: [pcscf]
    text: It makes an IP association for the address of the UE and stores the Service-Route and the identities.
  - id: registered
    title: The UE is registered
    show: [ok-p-ue]
    text: |
      The UE checks `rspauth`, the proof that the network knows H(A1) too.
      The Contact has 3600 seconds. The UE asked for 600000; the S-CSCF shortened it to its maximum.
```

| Look at | What to see |
| --- | --- |
| Server-Assignment-Type of the SAR | `REGISTRATION` (1). |
| User-Data of the SAA | The user profile, in XML, with the iFCs. |
| Path and Service-Route of the 200 OK | The routes of the requests that follow. |
| P-Associated-URI of the 200 OK | The identities registered together, without those that the HSS bars. The first is the default. |
| Contact of the 200 OK | `expires=3600`: the S-CSCF shortened it to its maximum. |
| Authentication-Info of the 200 OK | `rspauth`, the proof that the network knows H(A1) too. |

The filter is `diameter.cmd.code == 301`. Wireshark names User-Data `Cx-User-Data`.

> Guide: [9](../../../docs/guide/en/registration.md#9-the-s-cscf-checks-the-answer-and-tells-the-hss-sar-and-saa) and [10](../../../docs/guide/en/registration.md#10-the-s-cscf-accepts-200-ok). TS 24.229 5.4.1.2.2A, 5.4.1.2.2F. TS 29.228 Annex E. RFC 3608.

---

## The directions of Path and Service-Route

```ims-scene
id: routes
nodes: [ue, pcscf, icscf, scscf]
lead: Registration sets up both routes, but a different node keeps each, and they are used in opposite directions.
steps:
  - id: register
    title: Registration sets up two routes
    only: [route-path-store, route-sr-store]
    focus: [scscf, ue]
    detail: [route-path-store, route-sr-store]
    checkpoint: true
    text: |
      The S-CSCF stores the Path that the P-CSCF put in the REGISTER, with the binding.
      The UE stores the Service-Route that the S-CSCF put in the 200 OK.
  - id: path
    title: Path is the route of requests to the UE
    only: [mt-s-p, mt-p-ue]
    active: mt-p-ue
    focus: [scscf, pcscf, ue]
    checkpoint: true
    text: |
      The S-CSCF makes the Request-URI of a request for the UE the Contact, and puts the stored Path in its Route.
      An incoming INVITE reaches the UE through the P-CSCF that the UE registered through.
  - id: service-route
    title: Service-Route is the route of requests from the UE
    only: [mo-ue-p, mo-p-s]
    active: mo-p-s
    focus: [ue, pcscf, scscf]
    checkpoint: true
    text: |
      The UE puts the Service-Route at the top of its requests. They go from the P-CSCF straight to its S-CSCF, not through the I-CSCF.
      An outgoing INVITE takes this route, and so does the SUBSCRIBE of the UE later in this deck.
```

- The UE uses Service-Route when it calls. Path is used when it is called. Neither is the list of Via that the answer to the REGISTER goes back along: Via is the way back of the answer to the request just sent, and Path and Service-Route are the routes of the new requests that come after it.
- The call deck shows the whole incoming and outgoing INVITE again.

> Book: [Which request follows Path and which follows Service-Route](../../../docs/book/en/registration-routes.md)
> Guide: [10](../../../docs/guide/en/registration.md#10-the-s-cscf-accepts-200-ok). RFC 3327, RFC 3608.

---

# The UE is registered

The second half follows what comes after the registration.

---

## The P-CSCF subscribes to the registration state (11, 12)

```ims-scene
id: pcscf-subscribe
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The P-CSCF asks the S-CSCF to report every change of the registration, with the reg event package.
steps:
  - id: subscribe
    title: The P-CSCF sends SUBSCRIBE to the I-CSCF
    only: [psub-p-i]
    text: The P-CSCF is in the home network, so its SUBSCRIBE goes to the entry of the home network.
  - id: lir
    title: The I-CSCF finds the S-CSCF (LIR and LIA)
    show: [lir1, lia1]
    active: lia1
    zoom: [icscf, hss, scscf]
    text: It asks the HSS which S-CSCF serves the user. Cx command 302.
  - id: forward
    title: The SUBSCRIBE reaches the S-CSCF
    show: [psub-i-s]
    text: The S-CSCF checks that the user is registered and that the subscriber is a P-CSCF on its Path.
  - id: ok
    title: A 200 OK starts the subscription
    show: [psub-ok-s-i, psub-ok-i-p]
    active: psub-ok-i-p
    checkpoint: true
    text: With this 200 the P-CSCF and the S-CSCF share a dialog, one without a call.
  - id: notify
    title: The NOTIFY goes straight to the P-CSCF
    hide: [lir1, lia1]
    show: [pnotify]
    focus: [scscf, pcscf]
    text: |
      The I-CSCF did not stay on the route, so the NOTIFY does not pass it.
      Its body is `application/reginfo+xml`.
```

| Look at | What to see |
| --- | --- |
| The SUBSCRIBE of the P-CSCF | Goes through the I-CSCF, which finds the S-CSCF with an LIR. |
| Body of the NOTIFY | `application/reginfo+xml`, with a `contact` per binding. |
| Subscription-State | `active;expires=...`. |

The filter is `sip.CSeq.method == "SUBSCRIBE" || sip.CSeq.method == "NOTIFY" || diameter.cmd.code == 302`.

> Book: [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md)
> Guide: ["Subscribe to the registration state"](../../../docs/guide/en/registration.md#subscribe-to-the-registration-state). TS 24.229 5.2.3, 5.4.2.1.1, 5.4.2.1.2. RFC 3680, RFC 6665. TS 29.228 6.1.4.

---

## The UE subscribes to the registration state too (13, 14)

```ims-scene
id: ue-subscribe
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: The SUBSCRIBE of the UE follows the Service-Route and does not pass the I-CSCF.
steps:
  - id: subscribe
    title: The UE sends SUBSCRIBE
    only: [usub-ue-p]
    text: Its Route is the P-CSCF and the Service-Route, as in every other request of the UE.
  - id: direct
    title: From the P-CSCF straight to the S-CSCF
    show: [usub-p-s]
    focus: [pcscf, scscf]
    checkpoint: true
    text: The P-CSCF puts the identity in P-Asserted-Identity, adds a Record-Route and sends it to the S-CSCF of the Service-Route.
  - id: notify
    title: The NOTIFY reaches the UE through the P-CSCF
    show: [unotify-s-p, unotify-p-ue]
    active: unotify-p-ue
    detail: [unotify-s-p, unotify-p-ue]
    text: |
      The Route of the NOTIFY from the S-CSCF holds the P-CSCF. The P-CSCF adds its own Record-Route to the NOTIFY.
      The UE takes the route of the subscription from this NOTIFY, so its later SUBSCRIBEs pass the P-CSCF too.
```

| Look at | What to see |
| --- | --- |
| The SUBSCRIBE of the UE | Follows the Service-Route and skips the I-CSCF. |
| Body of the NOTIFY | The same reginfo as the one the P-CSCF gets. |

> Guide: [13](../../../docs/guide/en/registration.md#13-the-ue-subscribes-subscribe) and [14](../../../docs/guide/en/registration.md#14-the-notify-reaches-the-ue-through-the-p-cscf). TS 24.229 5.1.1.3, 5.2.6.4.5. RFC 6665 4.4.1.

---

## Third-party registration at the application server (15 to 18)

```ims-scene
id: third-party
nodes: [icscf, hss, scscf, ipsmgw]
lead: Following the iFCs of the profile, the S-CSCF sends a REGISTER of its own to the IP-SM-GW. It is called a third-party REGISTER.
steps:
  - id: register
    title: The S-CSCF sends REGISTER to the IP-SM-GW
    only: [tp-register]
    text: |
      From and Contact are the S-CSCF, To is the user. The body holds the MSISDN.
      The UE does not wait for this. It already has its 200 OK.
  - id: ok
    title: The IP-SM-GW answers 200 OK
    show: [tp-ok]
    checkpoint: true
    text: Without an answer, the DefaultHandling of the iFC applies, here SESSION_CONTINUED, and the registration stays.
  - id: subscribe
    title: The IP-SM-GW subscribes to the reg event too
    only: [isub-ip-i, lir2, lia2, isub-i-s]
    active: isub-i-s
    checkpoint: true
    text: As with the P-CSCF, the SUBSCRIBE goes to the entry of the home network, and the I-CSCF finds the S-CSCF with an LIR.
  - id: notify
    title: With the NOTIFY it looks for a binding that takes SMS
    only: [inotify, ipsmgw-check]
    active: ipsmgw-check
    focus: [ipsmgw]
    checkpoint: true
    text: '`+g.3gpp.smsip` is the feature tag that says the UE takes SMS over IP.'
  - id: pur
    title: The IP-SM-GW tells the HSS (PUR and PUA)
    only: [pur, pua]
    active: pur
    detail: [pur, pua]
    text: |
      With Sh command 307 it writes into the HSS that it handles the short messages of this user.
      The SMS deck shows this flow closely.
```

| Look at | What to see |
| --- | --- |
| The REGISTER of the S-CSCF | From and Contact are the S-CSCF, To is the user, the body holds the MSISDN. |
| SUBSCRIBE and NOTIFY of the IP-SM-GW | The IP-SM-GW subscribes to the reg event as well. |
| PUR and PUA | The IP-SM-GW registers its number in the HSS over Sh. |

- The UE does not wait for any of this: it already has its 200 OK. The SMS deck shows this flow closely.

The filter is `diameter.cmd.code == 307`.

> Book: [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md)
> Guide: ["Register the user at the application server (third-party REGISTER)"](../../../docs/guide/en/registration.md#register-the-user-at-the-application-server-third-party-register). TS 24.229 5.4.1.7, 5.4.1.7A. TS 24.341 5.3.3.2. TS 29.328 6.1.2.

---

## Deregistration

```ims-scene
id: deregister
view: ladder
nodes: [ue, pcscf, icscf, scscf, hss]
lead: The UE sends a REGISTER whose Contact has `expires=0`. This time no 401 comes back.
steps:
  - id: request
    title: The REGISTER that deregisters reaches the I-CSCF
    show: [dreg-ue-p, dreg-p-i]
    text: The P-CSCF finds the IP association and adds `integrity-protected="ip-assoc-yes"`.
  - id: uar
    title: A UAR with DE_REGISTRATION
    show: [duar, duaa, dreg-i-s]
    active: duar
    text: The I-CSCF asks with User-Authorization-Type DE_REGISTRATION (1) and sends the REGISTER to the S-CSCF.
  - id: sar
    title: A SAR without a challenge
    show: [dsar, dsaa]
    active: dsar
    checkpoint: true
    text: |
      There is an IP association, so the S-CSCF does not challenge again.
      It sends a SAR with USER_DEREGISTRATION (5) and waits for the SAA.
  - id: ok
    title: A 200 OK with expires=0
    show: [dok-s-i, dok-i-p, dok-p-ue]
    active: dok-p-ue
    text: The 200 OK goes back to the UE through the I-CSCF and the P-CSCF.
  - id: notify
    title: The last NOTIFY
    show: [dnotify-s-p, dnotify-p-ue]
    active: dnotify-s-p
    text: |
      The registration and the contact are `terminated`, the contact has `event="unregistered"`. This NOTIFY ends the subscription.
      It reaches the IP-SM-GW too, which then removes its own registration from the HSS.
```

| Look at | What to see |
| --- | --- |
| Parameter the P-CSCF adds | `integrity-protected="ip-assoc-yes"`. |
| User-Authorization-Type of the UAR | DE_REGISTRATION (1). |
| Answer of the S-CSCF | No 401: the P-CSCF found the IP association and added `ip-assoc-yes`. |
| Server-Assignment-Type of the SAR | USER_DEREGISTRATION (5). |
| The last NOTIFY | `terminated`, and the contact has `event="unregistered"`. |

- A registration that is not refreshed expires the same way: `event="expired"`, and a SAR with TIMEOUT_DEREGISTRATION. The same section of the guide has it.

> Book: [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md)
> Guide: ["Deregistration"](../../../docs/guide/en/registration.md#deregistration). TS 24.229 5.1.1.6.1, 5.4.1.4.1, 5.4.2.1.2.

---

## A deregistration that comes late

```ims-scene
id: registration-order
nodes: [icscf, scscf, hss]
basis: illustration
lead: A request with the same Call-ID changes the registration only with a CSeq higher than the stored one.
steps:
  - id: stored
    title: A newer registration is stored
    only: [ord-stored]
    focus: [scscf]
    text: A re-registration was accepted with CSeq 5. The S-CSCF keeps its Call-ID and CSeq with the binding.
  - id: late
    title: An old deregistration arrives
    show: [ord-late]
    text: A retransmission of a deregistration with CSeq 3, which the UE sent before the re-registration, comes late. The figure leaves out what the P-CSCF and the I-CSCF did before.
  - id: compare
    title: The S-CSCF compares it with the stored CSeq
    show: [ord-compare]
    text: The Call-ID is the same, so the S-CSCF compares the CSeq. 3 is not higher than 5.
  - id: refuse
    title: A 500, and the registration stays
    show: [ord-500, ord-kept]
    active: ord-500
    detail: [ord-500, ord-kept]
    checkpoint: true
    text: |
      The S-CSCF answers 500 and changes neither the Contact, nor the expiry, nor the CSeq. No SAR for a deregistration goes to the HSS.
      A request with CSeq 5 is refused the same way, since it is not higher.
  - id: other
    title: A REGISTER with another Call-ID is not compared
    only: [ord-stored, ord-other, ord-other-compare]
    focus: [scscf]
    detail: [ord-other, ord-other-compare]
    text: The Call-ID differs, so the S-CSCF does not compare the CSeq.
  - id: accepted
    title: An SAR, and the values of B are stored
    show: [ord-other-sar, ord-other-saa, ord-other-ok]
    active: ord-other-ok
    checkpoint: true
    text: The S-CSCF sends an SAR of RE_REGISTRATION and answers 200 OK. From now on, Call-ID B and CSeq 2 are the values to compare with.
```

- A and B for the Call-ID and C for the Contact are long values written short for the example. They are not values of a capture.
- The rule is step 6 of RFC 3261 10.3. With the same Call-ID, the binding changes only with a CSeq higher than the stored one; otherwise the request fails. The rule names no status code. This implementation answers 500, the code that step 7 of the same section names for a binding update that fails.
- The S-CSCF compares after it has checked the answer to the challenge, and before it sends an SAR to the HSS. A deregistration is compared without a new challenge when there is an IP association. Either way, a refused request leaves nothing behind, at the HSS or in the binding.

> Book: [Why a late deregistration does not remove a newer registration](../../../docs/book/en/registration-stale-deregistration.md). Guide: [7](../../../docs/guide/en/registration.md#7-the-ue-answers-the-challenge).

---

## Watching it fail

```ims-scene
id: failures
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: Both failures branch off a normal registration. They start at different nodes.
steps:
  - id: wrong
    title: A wrong password
    only: [reg2-i-s, wp-verify]
    focus: [scscf]
    text: Everything up to step 8 is the same. At step 9 the response does not match the value the S-CSCF computes.
  - id: forbidden
    title: A 403, and a SAR with AUTHENTICATION_FAILURE
    show: [wp-403-s-i, wp-403-i-p, wp-403-p-ue, wp-sar, wp-clear]
    active: wp-sar
    detail: [wp-sar, wp-clear]
    checkpoint: true
    text: |
      The S-CSCF answers 403 and sends a SAR with AUTHENTICATION_FAILURE (9).
      The HSS forgets the S-CSCF it stored, so the next attempt starts from the beginning.
  - id: rtr
    title: The HSS removes the subscription
    only: [rtr]
    focus: [hss, scscf]
    text: |
      This one starts at the HSS. The Cx requests so far came from the I-CSCF or the S-CSCF; the HSS sends the RTR.
      The Reason-Code is PERMANENT_TERMINATION (0).
  - id: rejected
    title: The UE gets a NOTIFY with rejected
    show: [rej-s-p, rej-p-ue, rta]
    active: rej-p-ue
    detail: [rej-s-p, rta]
    text: |
      The S-CSCF removes the registration, sends a NOTIFY with `event="rejected"` and answers with an RTA.
      A UE that gets rejected does not register again.
```

| Case | What sets it off | What to see |
| --- | --- | --- |
| A wrong password | `ue register -password wrong-password` | The same up to step 8. The S-CSCF answers 403 and sends a SAR with AUTHENTICATION_FAILURE (9). |
| The HSS removes the subscription | The operator removes the subscription of a registered user through the OAM. | The HSS sends an RTR with Reason-Code PERMANENT_TERMINATION (0). The UE gets a NOTIFY with `event="rejected"`. |

- The guide runs the `ue` command after `docker compose run --rm`.
- The filter for RTR and RTA is `diameter.cmd.code == 304`.
- The provisioning API of the HSS removes a subscription too; the guide has its table.

> Book: [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md), [Why a UE sends REGISTER twice](../../../docs/book/en/registration-two-registers.md)
> Guide: ["A wrong password"](../../../docs/guide/en/registration.md#a-wrong-password) and ["Remove a subscription (deregistration by the HSS)"](../../../docs/guide/en/registration.md#remove-a-subscription-deregistration-by-the-hss), and in the Lab guide ["The network cancels a registration"](../../../docs/guide/en/lab.md#the-network-cancels-a-registration). TS 24.229 5.4.1.2.3B. TS 29.228 6.1.2.1, 6.1.3.

---

[layout: two-columns]

## The same flow in the Lab

- The **Registrations** panel of the Lab (<http://localhost:8080>) lists each registered identity with its Contact, expiry and S-CSCF.
- Picking an identity draws the ladder of its last REGISTER. Picking an arrow shows the whole message and the clause that defines it.
- The ladder shows SIP and Cx. It does not show DNS. The Lab ties a Cx request to the REGISTER that the same node received just before, by inference.
- The Lab draws from the copies (HEP) that the nodes send it. It is not part of the IMS. A third-party REGISTER adds no row, and an RTR is not drawn.

[column]

| Tool | Good for |
| --- | --- |
| Wireshark | Every packet: DNS, TCP, the inside of each AVP. |
| Lab | One registration or call as a diagram, at once. |

> Book: [Refresh, deregistration, subscription and removal by the network](../../../docs/book/en/registration-lifecycle.md)
> Guide: [Watch the IMS in the Lab, "What each panel shows"](../../../docs/guide/en/lab.md#what-each-panel-shows) and ["How the ladder is built"](../../../docs/guide/en/lab.md#how-the-ladder-is-built)

---

## The code of the steps

```ims-scene
id: code
nodes: [ue, pcscf, dns, icscf, hss, scscf]
lead: Each node has a file with its steps, and functions that decide.
steps:
  - id: ue
    title: The REGISTER of the UE and its digest response (1, 7)
    focus: [ue]
    text: "`Register` in `internal/ue/ue.go`."
  - id: pcscf
    title: Path and DNS (2)
    focus: [pcscf, dns]
    text: "`register` and `pathURI` in `internal/pcscf/pcscf.go`, `Locate` in `internal/sipdns/locate.go`."
  - id: icscf
    title: UAR (3, 8)
    focus: [icscf]
    text: "`register` and `choose` in `internal/icscf/icscf.go`."
  - id: scscf
    title: MAR, 401, SAR, 200 OK (5, 6, 9, 10)
    focus: [scscf]
    text: "`register`, `challenge`, `check`, `accept` and `deregister` in `internal/scscf/scscf.go`."
  - id: hss
    title: The answers of the HSS
    focus: [hss]
    text: "`userAuthorization`, `multimediaAuth` and `serverAssignment` in `internal/hss/hss.go`."
  - id: regevent
    title: The reg event (11 to 14)
    focus: [scscf, pcscf, ue]
    text: "`internal/scscf/regevent.go`, `internal/pcscf/regevent.go`, `internal/ue/regevent.go`."
```

| Step | File to open | Functions that decide |
| --- | --- | --- |
| 1, 7. The REGISTER of the UE and its digest response | `internal/ue/ue.go` | `Register` |
| 2. Path and DNS | `internal/pcscf/pcscf.go`, `internal/sipdns/locate.go` | `register`, `pathURI`, `Locate` |
| 3, 8. UAR | `internal/icscf/icscf.go` | `register`, `choose` |
| 5, 6, 9, 10. MAR, 401, SAR, 200 OK | `internal/scscf/scscf.go` | `register`, `challenge`, `check`, `accept`, `deregister` |
| The answers of the HSS | `internal/hss/hss.go` | `userAuthorization`, `multimediaAuth`, `serverAssignment` |
| 11 to 14. The reg event | `internal/scscf/regevent.go`, `internal/pcscf/regevent.go`, `internal/ue/regevent.go` | `subscribe`, `notifyRegistration`, `endRegistration`, `subscribeRegEvent`, `SubscribeRegEvent` |

- The header names of the capture are in the code as they are. `Service-Route`, for example, is what `bindingResponse` of the S-CSCF puts in the 200 OK, and what `routeSet` of the UE makes its Route from.
- Each place where a procedure of the specification is simplified has a `SIMPLIFIED:` comment, and the guide's table of simplifications says the same.

> Guide: ["What this project simplifies"](../../../docs/guide/en/registration.md#what-this-project-simplifies)

---

## If you want to run it

This deck is written to be read. The steps to see the same flow in a capture of your own are in the guide.

- Starting the system and running a registration are in ["Run a registration"](../../../docs/guide/en/registration.md#run-a-registration). Opening the capture and the display filter are in ["Open the capture"](../../../docs/guide/en/registration.md#open-the-capture), and the whole numbered ladder is in ["The sequence"](../../../docs/guide/en/registration.md#the-sequence). Using Wireshark is in [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md).
- Cleaning up is in the guide's ["Clean up"](../../../docs/guide/en/registration.md#clean-up).

```sh
docker compose --profile ue --profile phones --profile volte down -v --remove-orphans
docker compose --profile ue down --rmi all -v --remove-orphans
```

The first line stops the nodes and the UEs and removes the volumes, but keeps the images, so the system of the call deck starts without building again. The second removes the images too. Both remove the stored data of the volumes with `-v`; the captures stay.

> The guide for calls is [Follow a voice call through the IMS](../../../docs/guide/en/call.md).
