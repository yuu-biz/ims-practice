---
title: A UE registers with the IMS
footer: ims-practice study session 2. Registration
lang: en
---

# A UE registers with the IMS

ims-practice study session 2 (registration with SIP Digest, and the third-party REGISTER to the application server)

---

[layout: summary]

## What you will know after this session

To begin: what happens in the network between switching a phone on and its first call?

- The three things registration settles: where the phone is, who the user is, and the route of later requests.
- Why there are two REGISTERs.
- What the I-CSCF and the S-CSCF ask the HSS.
- Which headers of the 200 OK the later calls and short messages use.

> Guide: [Follow a UE registering with the IMS](../../../docs/guide/en/registration.md) (`docs/guide/en/registration.md`)
> The editions are those of Release 18: TS 24.229 V18.11.0, TS 29.228 V18.0.0, TS 29.229 V18.1.0.

---

## How the session goes

1. Start the system and run one registration.
2. Open the capture in Wireshark and apply the filter.
3. Look at the whole flow as a ladder.
4. Open the messages one by one and find the headers and AVPs that the slide lists.
5. Check the clause of the specification for that step in the guide.

> Starting the system and opening a capture are in the guide [Capture signaling and open it in Wireshark](../../../docs/guide/en/capture.md).

---

## The nodes

| Node | Address | What it does in registration |
| --- | --- | --- |
| UE simulator | 172.28.0.100 | Sends REGISTER and answers the challenge. |
| P-CSCF | 172.28.0.40 | The first hop of the UE. It adds Path. |
| I-CSCF | 172.28.0.20 | The entry point of the home network. It asks the HSS for the S-CSCF. |
| S-CSCF | 172.28.0.30 | The registrar. It authenticates and stores the registration. |
| HSS | 172.28.0.10 | The subscriber database. It answers over Cx. |
| DNS | 172.28.0.2 | Holds NAPTR, SRV and A of the home network domain. |

- The last number of each address tells the node in a capture: the HSS is 10, the I-CSCF 20, the S-CSCF 30, the P-CSCF 40.
- The UE is a softphone without a SIM. Its identities are configured in it, as an IMC holds them (TS 24.229 5.1.1.1B.1), so it uses the public identity it registers in its other requests too. That identity has the form of one made from an IMSI, but it is configured and not barred. A VoLTE phone registers a barred temporary identity instead; the VoLTE session shows it.

> Guide: ["The nodes"](../../../docs/guide/en/registration.md#the-nodes), which also gives the identities and the password of the test user.

---

## Run it

```sh
docker compose up -d --build
docker compose logs scscf | grep I-Open        # are the Diameter connections open?
docker compose run --rm ue register --deregister-after 5s
```

After the start, wait until the I-CSCF and the S-CSCF have opened their Diameter connections to the HSS. A node that started before the HSS connects again after 30 seconds (Tc), so allow up to a minute.

Find these three lines in the log of the UE.

- `"msg":"registered"`, with the Service-Route and the associated identities.
- `"msg":"reg event"` with `"state":"active"`.
- `"msg":"deregistered"`.

> Guide: ["Run a registration"](../../../docs/guide/en/registration.md#run-a-registration)

---

## Open the capture

Open the newest file in `captures/` and use this display filter.

```text
sip || diameter.cmd.code >= 300 || dns.qry.type == 35 || dns.qry.type == 33
```

- SIP, Cx, and the NAPTR and SRV lookups stay.
- The Diameter watchdogs (DWR and DWA) go.
- **Statistics > Flow Graph** with **Limit to display filter** draws the ladder.
- The numbers 1 to 10 of the guide's ladder are the numbers of the next slides.

> Guide: ["Open the capture"](../../../docs/guide/en/registration.md#open-the-capture) and ["The sequence"](../../../docs/guide/en/registration.md#the-sequence), which has the full ladder.

---

## The shape of the whole

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

- The first REGISTER says who. The second proves it.
- The HSS is asked four times: two UARs, one MAR, one SAR. The full ladder of the guide has DNS as well.

---

## 1 and 2. The first REGISTER, and DNS finds the I-CSCF

| Look at | What to see |
| --- | --- |
| Request-URI of the UE's REGISTER | The home network domain, not a server. |
| Authorization | The private identity in `username`. `nonce` and `response` are empty. |
| Contact | A UUID as the user part, `+g.3gpp.smsip` and `expires=600000`. |
| Three DNS lookups | NAPTR, SRV, then A lead to the I-CSCF. |
| Headers the P-CSCF adds | Path, Require: path, P-Charging-Vector, P-Visited-Network-ID. |

The filter is `dns.qry.type == 35 || dns.qry.type == 33`.

- Compare the REGISTER before and after the P-CSCF to find what it adds. Path is what later brings an incoming call to the UE.

> Guide: [1](../../../docs/guide/en/registration.md#1-the-ue-sends-the-first-register) and [2](../../../docs/guide/en/registration.md#2-the-p-cscf-finds-the-i-cscf-and-forwards-the-register). TS 24.229 5.1.1.2.1, 5.1.1.2.3, 5.2.2.1. RFC 3263, RFC 3327, RFC 3581.

---

## 3 and 4. The I-CSCF asks the HSS (UAR and UAA)

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

## 5 and 6. The S-CSCF challenges (MAR, MAA, 401)

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

## 7 and 8. The second REGISTER

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

## 9 and 10. SAR, SAA and the 200 OK

| Look at | What to see |
| --- | --- |
| Server-Assignment-Type of the SAR | REGISTRATION (1). |
| User-Data of the SAA | The user profile, in XML, with the iFCs. |
| Path and Service-Route of the 200 OK | The routes of the requests that follow. |
| P-Associated-URI of the 200 OK | The identities registered together, without those that the HSS bars. The first is the default. |
| Contact of the 200 OK | `expires=3600`: the S-CSCF shortened it to its maximum. |
| Authentication-Info of the 200 OK | `rspauth`, the proof that the network knows H(A1) too. |

The filter is `diameter.cmd.code == 301`. Wireshark names User-Data `Cx-User-Data`.

- The UE uses Service-Route when it calls. Path is used when it is called. Both come back in the call.

> Guide: [9](../../../docs/guide/en/registration.md#9-the-s-cscf-checks-the-answer-and-tells-the-hss-sar-and-saa) and [10](../../../docs/guide/en/registration.md#10-the-s-cscf-accepts-200-ok). TS 24.229 5.4.1.2.2A, 5.4.1.2.2F. TS 29.228 Annex E. RFC 3608.

---

# The UE is registered

The second half follows what comes after the registration.

---

## 11 to 14. Subscribing to the registration state

- The P-CSCF and the UE ask the S-CSCF to report every change of the registration, with the reg event package.

| Look at | What to see |
| --- | --- |
| The SUBSCRIBE of the P-CSCF | Goes through the I-CSCF, which finds the S-CSCF with an LIR. |
| The SUBSCRIBE of the UE | Follows the Service-Route and skips the I-CSCF. |
| Body of the NOTIFY | `application/reginfo+xml`, with a `contact` per binding. |
| Subscription-State | `active;expires=...`. |

The filter is `sip.CSeq.method == "SUBSCRIBE" || sip.CSeq.method == "NOTIFY" || diameter.cmd.code == 302`.

> Guide: ["Subscribe to the registration state"](../../../docs/guide/en/registration.md#subscribe-to-the-registration-state). TS 24.229 5.2.3, 5.1.1.3, 5.4.2.1.1, 5.4.2.1.2. RFC 3680, RFC 6665. TS 29.228 6.1.4.

---

## 15 to 18. Registering at the application server too

- Following the iFCs of the profile, the S-CSCF sends a REGISTER of its own to the IP-SM-GW: a third-party REGISTER.

| Look at | What to see |
| --- | --- |
| The REGISTER of the S-CSCF | From and Contact are the S-CSCF, To is the user, the body holds the MSISDN. |
| SUBSCRIBE and NOTIFY of the IP-SM-GW | The IP-SM-GW subscribes to the reg event as well. |
| PUR and PUA | The IP-SM-GW registers its number in the HSS over Sh. |

- The UE does not wait for any of this: it already has its 200 OK. The SMS session reads this flow closely.

The filter is `diameter.cmd.code == 307`.

> Guide: ["Register the user at the application server (third-party REGISTER)"](../../../docs/guide/en/registration.md#register-the-user-at-the-application-server-third-party-register). TS 24.229 5.4.1.7, 5.4.1.7A. TS 24.341 5.3.3.2. TS 29.328 6.1.2.

---

## Deregistration

The UE sends a REGISTER whose Contact has `expires=0`.

| Look at | What to see |
| --- | --- |
| Parameter the P-CSCF adds | `integrity-protected="ip-assoc-yes"`. |
| User-Authorization-Type of the UAR | DE_REGISTRATION (1). |
| Answer of the S-CSCF | No 401: the P-CSCF found the IP association and added `ip-assoc-yes`. |
| Server-Assignment-Type of the SAR | USER_DEREGISTRATION (5). |
| The last NOTIFY | `terminated`, and the contact has `event="unregistered"`. |

- A registration that is not refreshed expires the same way: `event="expired"`, and a SAR with TIMEOUT_DEREGISTRATION. The same section of the guide has it.

> Guide: ["Deregistration"](../../../docs/guide/en/registration.md#deregistration). TS 24.229 5.1.1.6.1, 5.4.1.4.1, 5.4.2.1.2.

---

## Watching it fail

| Case | How to try it | What to see |
| --- | --- | --- |
| A wrong password | `ue register --password wrong-password` | The same up to step 8. The S-CSCF answers 403 and sends a SAR with AUTHENTICATION_FAILURE (9). |
| The HSS removes the subscription | Delete the subscription of a registered user in the Subscribers panel of the dashboard. | The HSS sends an RTR with Reason-Code PERMANENT_TERMINATION (0). The UE gets a NOTIFY with `event="rejected"`. |

- Put `docker compose run --rm` in front of the command.
- The filter for RTR and RTA is `diameter.cmd.code == 304`. The RTR is the first Cx request in these sessions that the HSS starts.
- The provisioning API of the HSS removes a subscription too; the guide has its table.

> Guide: ["A wrong password"](../../../docs/guide/en/registration.md#a-wrong-password) and ["Remove a subscription (deregistration by the HSS)"](../../../docs/guide/en/registration.md#remove-a-subscription-deregistration-by-the-hss). TS 24.229 5.4.1.2.3B. TS 29.228 6.1.2.1, 6.1.3.

---

[layout: two-columns]

## The same flow in the dashboard

- Open <http://localhost:8080> and pick an identity in the **Registrations** panel.
- The ladder of the last REGISTER appears. Pick an arrow to see the whole message and the clause that defines it.
- The ladder shows SIP and Cx. It does not show DNS.
- The dashboard draws from the copies (HEP) that the nodes send it. It is not part of the IMS.

[column]

| Tool | Good for |
| --- | --- |
| Wireshark | Every packet: DNS, TCP, the inside of each AVP. |
| Dashboard | One registration or call as a diagram, at once. |

> Guide: [Watch the IMS in the dashboard, "What each panel shows"](../../../docs/guide/en/dashboard.md#what-each-panel-shows) and ["How the ladder is built"](../../../docs/guide/en/dashboard.md#how-the-ladder-is-built)

---

## Check it in the code

| Step | File to open |
| --- | --- |
| 1, 7. The REGISTER of the UE and its digest response | `internal/ue/ue.go` |
| 2. Path and DNS | `internal/pcscf/pcscf.go`, `internal/sipdns/locate.go` |
| 3, 8. UAR | `internal/icscf/icscf.go` |
| 5, 6, 9, 10. MAR, 401, SAR, 200 OK | `internal/scscf/scscf.go` |
| The answers of the HSS | `internal/hss/hss.go` |
| 11 to 14. The reg event | `internal/scscf/regevent.go`, `internal/pcscf/regevent.go`, `internal/ue/regevent.go` |

- Search the code for a header name from the capture, such as `Service-Route`.
- Pick a row of the guide's table of simplifications and find its `SIMPLIFIED:` comment.

> Guide: ["What this project simplifies"](../../../docs/guide/en/registration.md#what-this-project-simplifies)

---

[layout: exercise]

## Looking back

1. Why are there two REGISTERs?
2. How often does the I-CSCF ask the HSS in one registration, and how do the answers differ?
3. Does the HSS give the password to the S-CSCF?
4. Who uses Path and who uses Service-Route of the 200 OK, and when?
5. Why is there no 401 when the UE deregisters?

---

[layout: answers]

## Looking back: answers

1. The network gives the challenge (the nonce) in answer to the first, and the UE answers it in the second.
2. Twice. First DIAMETER_FIRST_REGISTRATION with Server-Capabilities, then DIAMETER_SUBSEQUENT_REGISTRATION with Server-Name.
3. No. It gives H(A1).
4. The S-CSCF uses Path to send requests for the UE through its P-CSCF. The UE uses Service-Route to send its own requests to its S-CSCF.
5. The P-CSCF has an IP association for the UE and adds `ip-assoc-yes`.

---

## Next session

- Next is a call. It uses the Path, the Service-Route and the IP association that this session set up.
- Clean up with this command.

```sh
docker compose --profile ue down --rmi all -v --remove-orphans
```

> The guide of the next session is [Follow a voice call through the IMS](../../../docs/guide/en/call.md).
