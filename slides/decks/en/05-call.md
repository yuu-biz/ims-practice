---
title: A voice call through the IMS
footer: ims-practice study session 5. Call
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: the caller, at: [0, 0]}
  - {id: agw, label: IMS-AGW, role: the media relay, at: [0, 1]}
  - {id: ue2, label: UE 2, role: the callee, at: [0, 2]}
  - {id: pcscf, label: P-CSCF, role: serves both UEs, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: hss, label: HSS, role: subscriber data, at: [4, 0]}
  - {id: scscf, label: S-CSCF, role: serves both sides, at: [3, 1]}
  - {id: mmtel, label: MMTel AS, role: supplementary services, at: [4, 1]}
  - {id: dns, label: DNS, role: ENUM records, at: [3, 2]}
events:
  # The caller's side (steps 1 to 3)
  - id: inv-ue1-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: "`tel:+1-202-555-0102` a number, not a place"
      - Route: "The P-CSCF, and the Service-Route (`sip:orig@scscf...`)"
      - Supported: "`100rel`, `timer`, `199`, `precondition`"
      - Session-Expires: 1800. UE 1 refreshes the session with an UPDATE
      - The SDP offer: UE 1's address and port. PCMU and telephone-event
      - "`a=curr` and `a=des`": Preconditions. UE 1's resources are there already
  - id: p-orig
    do: tells it is the caller's side
    at: pcscf
    fields:
      - The top Route: "Lacks the user part `term` of its Path"
      - The rest of Route: Checked against the Service-Route
  - id: ctx1-post
    msg: POST /contexts
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 1)
    fields:
      - Terminations: An access one (towards UE 1) and a core one (towards the network), with nowhere to send yet
  - id: ctx1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 1)
    fields:
      - What comes back: The ID of the context, and the addresses of its two terminations
  - id: set1-put
    msg: PUT .../terminations/access
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 1)
    fields:
      - Where it sends: The address in UE 1's SDP
  - id: set1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 1)
  - id: p-rewrite
    do: adds headers, rewrites the SDP
    at: pcscf
    fields:
      - P-Asserted-Identity: UE 1's default public identity
      - Record-Route: The P-CSCF. Later requests pass it
      - "`c=` and `m=` of the SDP": The core termination of context 1
  - id: inv-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    fields:
      - Route: "The Service-Route `sip:orig@scscf...`"
      - P-Asserted-Identity: UE 1's default public identity
      - "`c=` and `m=` of the SDP": The media relay, 172.28.0.50
  - id: s-orig
    do: serves the caller
    at: scscf
    fields:
      - The top Route: "Carries `orig`"
      - The served user: The one in P-Asserted-Identity. It checks that the user is registered
  - id: s-assert
    do: writes identities and service
    at: scscf
    fields:
      - P-Asserted-Identity: A second one is added, UE 1's tel URI
      - P-Asserted-Service: The ICSI of MMTel. P-Preferred-Service is gone
  - id: inv-s-as1
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (orig)
    fields:
      - Route: "The AS on top, then the S-CSCF with `odi`"
      - P-Served-User: "`sescase=orig`"
  - id: udr1
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    note: (UE 1)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: MMTEL-Services
  - id: uda1
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    note: (UE 1)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Content: UE 1's MMTelServices. The default settings change nothing
  - id: inv-as-s1
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (orig)
    fields:
      - Call-ID: A new one of the AS
      - Route: "The S-CSCF with `odi`"
  - id: enum-q
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (ENUM)
    fields:
      - Name asked for: "`2.0.1.0.5.5.5.2.0.2.1.e164.arpa` the number reversed"
  - id: enum-a
    msg: NAPTR
    from: dns
    to: scscf
    proto: DNS
    note: (ENUM answer)
    fields:
      - Service: "`E2U+sip`"
      - Regular expression: "Makes `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
  - id: s-uri
    do: makes the Request-URI a SIP URI
    at: scscf
    fields:
      - Before: "`tel:+1-202-555-0102`"
      - After: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
  # The entry point (steps 4 and 5)
  - id: loc-q
    msg: NAPTR, SRV
    from: scscf
    to: dns
    proto: DNS
    note: (finds the I-CSCF)
    fields:
      - Domain asked for: "`ims.mnc001.mcc001.3gppnetwork.org`"
  - id: loc-a
    msg: NAPTR, SRV
    from: dns
    to: scscf
    proto: DNS
    note: (answer)
    fields:
      - What it finds: As in registration, the I-CSCF, the entry point of the home network
  - id: inv-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    fields:
      - Request-URI: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
      - Via: "`SIP/2.0/TCP` the INVITE is over 1300 bytes"
  - id: lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    fields:
      - Public-Identity: "`sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`"
  - id: lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Server-Name: The S-CSCF where UE 2 is registered
  - id: inv-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    fields:
      - Route: The S-CSCF of the Server-Name of the LIA on top
      - Record-Route: The I-CSCF adds none
  # The callee's side (steps 6 and 7)
  - id: s-term
    do: serves the callee
    at: scscf
    fields:
      - The top Route: "No `orig`"
      - What it finds: UE 2's registration
      - P-Asserted-Service: Kept as it is
  - id: inv-s-as2
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (term)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: udr2
    msg: UDR
    from: mmtel
    to: hss
    proto: Sh
    note: (UE 2)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: MMTEL-Services
  - id: uda2
    msg: UDA
    from: hss
    to: mmtel
    proto: Sh
    note: (UE 2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Content: UE 2's MMTelServices
  - id: inv-as-s2
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (term)
    fields:
      - Call-ID: A new one of the AS
  - id: s-path
    do: routes towards UE 2
    at: scscf
    fields:
      - Record-Route: The S-CSCF
      - P-Called-Party-ID: The address that was called
      - Request-URI: The Contact that UE 2 registered
      - Route: The Path of UE 2's REGISTER
  - id: inv-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    fields:
      - Request-URI: The Contact that UE 2 registered
      - Route: "UE 2's Path (`sip:term@pcscf...`)"
  - id: p-term
    do: tells it is the callee's side
    at: pcscf
    fields:
      - The top Route: "The URI of its Path, with the user part `term`"
  - id: ctx2-post
    msg: POST /contexts
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 2)
    fields:
      - Terminations: An access one (towards UE 2) and a core one (towards the network), with nowhere to send yet
  - id: ctx2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 2)
    fields:
      - What comes back: The ID of the context, and the addresses of its two terminations
  - id: set2-put
    msg: PUT .../terminations/core
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 2)
    fields:
      - Where it sends: The address in the offer, the core termination of context 1
  - id: set2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 2)
  - id: inv-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    fields:
      - Sent to: The address UE 2's REGISTER came from, not the Contact
      - "`c=` and `m=` of the SDP": The access termination of context 2
  # The 183 (step 8)
  - id: r183-ue2-p
    msg: "183"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - The SDP answer: PCMU, and UE 2's address and port
      - Require: "`100rel` and `precondition`"
      - RSeq: The number of the reliable provisional response
      - Preconditions: "Both ends have their resources. No `a=conf`"
      - To tag, Contact, Record-Route: They make the early dialog
  - id: mod2-put
    msg: PUT .../terminations/access
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 2)
    fields:
      - Where it sends: The address of UE 2's answer
  - id: mod2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 2)
  - id: r183-p-s3
    msg: "183"
    from: pcscf
    to: scscf
    proto: SIP
    note: (dialog 3)
    fields:
      - "`c=` and `m=` of the SDP": The core termination of context 2
  - id: r183-s-as3
    msg: "183"
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 3)
  - id: r183-as-s2
    msg: "183"
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 2)
  - id: r183-s-i
    msg: "183"
    from: scscf
    to: icscf
    proto: SIP
    note: (dialog 2)
  - id: r183-i-s
    msg: "183"
    from: icscf
    to: scscf
    proto: SIP
    note: (dialog 2)
  - id: r183-s-as2
    msg: "183"
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 2)
  - id: r183-as-s1
    msg: "183"
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 1)
  - id: r183-s-p1
    msg: "183"
    from: scscf
    to: pcscf
    proto: SIP
    note: (dialog 1)
  - id: mod1-put
    msg: PUT .../terminations/core
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 1)
    fields:
      - Where it sends: The address of the answer, the core termination of context 2
  - id: mod1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 1)
  - id: r183-p-ue1
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (dialog 1)
    fields:
      - "`c=` and `m=` of the SDP": The access termination of context 1
      - Rewritten: Twice, by the P-CSCF
  # PRACK (step 9)
  - id: prack-ue1-p
    msg: PRACK
    from: ue1
    to: pcscf
    proto: SIP
    note: (dialog 1)
    fields:
      - RAck: The RSeq of the 183 and the CSeq of the INVITE
      - Route: The route set, built from Record-Route
  - id: prack-p-s1
    msg: PRACK
    from: pcscf
    to: scscf
    proto: SIP
    note: (dialog 1)
  - id: prack-s-as1
    msg: PRACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 1)
  - id: pok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (PRACK)
  - id: pok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (PRACK)
  - id: prack-as-s2
    msg: PRACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 2)
  - id: prack-s-as2
    msg: PRACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 2)
  - id: pok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (PRACK)
  - id: prack-as-s3
    msg: PRACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 3)
  - id: prack-s-p3
    msg: PRACK
    from: scscf
    to: pcscf
    proto: SIP
    note: (dialog 3)
  - id: prack-p-ue2
    msg: PRACK
    from: pcscf
    to: ue2
    proto: SIP
    note: (dialog 3)
  - id: pok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (PRACK)
  - id: pok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (PRACK)
  - id: pok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (PRACK)
  # 180, 200 OK and ACK (steps 10 and 11)
  - id: ue2-ring
    do: alerts its user
    at: ue2
    fields:
      - When: Every mandatory precondition is met
      - Here: Both UEs have their resources, so as soon as the 183 has its PRACK
  - id: r180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - Require: "`100rel` a reliable provisional response. It gets a PRACK too"
  - id: ok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (INVITE)
    fields:
      - SDP: None. The answer came in the reliable 183
      - Retransmission: Sent again, with growing intervals, until the ACK arrives
  - id: ok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (INVITE)
  - id: ack-as-s3
    msg: ACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 3)
  - id: ack-s-p3
    msg: ACK
    from: scscf
    to: pcscf
    proto: SIP
    note: (dialog 3)
  - id: ack-p-ue2
    msg: ACK
    from: pcscf
    to: ue2
    proto: SIP
    note: (dialog 3)
  - id: ok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (INVITE)
  - id: ok-i-s
    msg: 200 OK
    from: icscf
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (INVITE)
  - id: ack-as-s2
    msg: ACK
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 2)
  - id: ack-s-as2
    msg: ACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 2)
  - id: ok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (INVITE)
  - id: ok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (INVITE)
  - id: ok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (INVITE)
    fields:
      - Meaning: The dialog is confirmed
  - id: ack-ue1-p
    msg: ACK
    from: ue1
    to: pcscf
    proto: SIP
    note: (dialog 1)
    fields:
      - The way: Along the route set
      - Transaction: Not part of the INVITE transaction
  - id: ack-p-s1
    msg: ACK
    from: pcscf
    to: scscf
    proto: SIP
    note: (dialog 1)
  - id: ack-s-as1
    msg: ACK
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 1)
  # The voice (step 12)
  - id: rtp-ue1-agw
    msg: RTP
    from: ue1
    to: agw
    proto: RTP
    fields:
      - Interval and size: Every 20 ms, 160 bytes of G.711 μ-law
      - Destination: The access termination of context 1
      - Port: Sent from the port it receives on. Symmetric RTP
  - id: agw-relay
    do: passes two contexts
    at: agw
    fields:
      - The way: "Access (1), core (1), core (2), access (2)"
      - Context 1: Made by the P-CSCF on the caller's side
      - Context 2: Made by the P-CSCF on the callee's side
  - id: rtp-agw-ue2
    msg: RTP
    from: agw
    to: ue2
    proto: RTP
    fields:
      - Source: The access termination of context 2
  - id: rtp-ue2-agw
    msg: RTP
    from: ue2
    to: agw
    proto: RTP
  - id: agw-latch
    do: picks where to send (latching)
    at: agw
    fields:
      - Towards the UE: Where the first packet came from
      - Towards the network: Where the latest SDP says
  - id: rtp-agw-ue1
    msg: RTP
    from: agw
    to: ue1
    proto: RTP
  - id: rtcp-ue1-agw
    msg: RTCP
    from: ue1
    to: agw
    proto: RTCP
    fields:
      - Port: The port above the RTP port
      - Interval: At least every 5 seconds
      - Content: A sender report (a receiver report while the UE sends nothing) and an SDES with the CNAME
  # Hanging up (step 13)
  - id: bye-ue1-p
    msg: BYE
    from: ue1
    to: pcscf
    proto: SIP
    note: (dialog 1)
    fields:
      - Reason: '`RELEASE_CAUSE;cause=1;text="User ends call"`'
      - The way: Along the route set
  - id: bye-p-s1
    msg: BYE
    from: pcscf
    to: scscf
    proto: SIP
    note: (dialog 1)
  - id: bye-s-as1
    msg: BYE
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 1)
  - id: bok-as-s1
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-p1
    msg: 200 OK
    from: scscf
    to: pcscf
    proto: SIP
    note: (BYE)
  - id: bok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (BYE)
  - id: del1
    msg: "DELETE /contexts/{id}"
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 1)
  - id: del1-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 1)
  - id: bye-as-s2
    msg: BYE
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 2)
  - id: bye-s-as2
    msg: BYE
    from: scscf
    to: mmtel
    proto: SIP
    note: (dialog 2)
  - id: bok-as-s2
    msg: 200 OK
    from: mmtel
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-as2
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (BYE)
  - id: bye-as-s3
    msg: BYE
    from: mmtel
    to: scscf
    proto: SIP
    note: (dialog 3)
  - id: bye-s-p3
    msg: BYE
    from: scscf
    to: pcscf
    proto: SIP
    note: (dialog 3)
  - id: bye-p-ue2
    msg: BYE
    from: pcscf
    to: ue2
    proto: SIP
    note: (dialog 3)
  - id: bok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (BYE)
  - id: bok-p-s3
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (BYE)
  - id: bok-s-as3
    msg: 200 OK
    from: scscf
    to: mmtel
    proto: SIP
    note: (BYE)
  - id: del2
    msg: "DELETE /contexts/{id}"
    from: pcscf
    to: agw
    proto: HTTP
    note: (context 2)
  - id: del2-ok
    msg: 200 OK
    from: agw
    to: pcscf
    proto: HTTP
    note: (context 2)
  # Failures
  - id: r486-ue2-p
    msg: "486"
    from: ue2
    to: pcscf
    proto: SIP
    fields:
      - Source: UE 2, the node that made the response
  - id: lir-u
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (unregistered)
    fields:
      - Public-Identity: "`sip:+12025550103@ims.mnc001.mcc001.3gppnetwork.org`"
  - id: lia-u
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (unregistered)
    fields:
      - Experimental-Result: DIAMETER_UNREGISTERED_SERVICE (2003)
      - Server-Capabilities: The capabilities to pick an S-CSCF by
  - id: inv-i-s-u
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (unregistered)
    fields:
      - Sent to: The S-CSCF that the I-CSCF picks, as for a first registration
  - id: sar-u
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    note: (unregistered)
    fields:
      - Server-Assignment-Type: UNREGISTERED_USER (3)
  - id: saa-u
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    note: (unregistered)
    fields:
      - User-Data: "The profile, with the iFC that calls the MMTel AS for TERMINATING_UNREGISTERED"
  - id: inv-s-as-u
    msg: INVITE
    from: scscf
    to: mmtel
    proto: SIP
    note: (unregistered)
  - id: inv-as-s-u
    msg: INVITE
    from: mmtel
    to: scscf
    proto: SIP
    note: (unregistered)
    fields:
      - Content: No call forwarding, so it comes back unchanged
  - id: s-480
    do: has no registration to route to
    at: scscf
    fields:
      - Result: No service took the call, so 480
  - id: r480-s-as
    msg: "480"
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - Source: The S-CSCF. The AS copies it onto its own dialog
  - id: enum-q-x
    msg: NAPTR
    from: scscf
    to: dns
    proto: DNS
    note: (ENUM)
    fields:
      - Name asked for: "`8.9.1.0.5.5.5.2.0.2.1.e164.arpa`"
  - id: enum-a-x
    msg: NAPTR
    from: dns
    to: scscf
    proto: DNS
    note: (ENUM answer)
    fields:
      - Answer: No record
  - id: s-404
    do: cannot make a SIP URI
    at: scscf
    fields:
      - Result: There is no BGCF, so the S-CSCF answers 404
  - id: r404-s-as
    msg: "404"
    from: scscf
    to: mmtel
    proto: SIP
    fields:
      - Source: The S-CSCF. It answers the new INVITE of the AS
```

---

# A voice call through the IMS

ims-practice study session 5 (a call through the media relay, with preconditions and the MMTel AS on its way)

---

[layout: summary]

## What you will know after this session

To begin: does UE 1 know the IP address of UE 2? It does not. It knows a telephone number.

- How a telephone number becomes a SIP address.
- How each CSCF finds the next one.
- How the two UEs agree on the media.
- How the voice flows through the media relay.

> Guide: [Follow a voice call through the IMS](../../../docs/guide/en/call.md) (`docs/guide/en/call.md`)
> The editions are those of Release 18, TS 24.229 V18.11.0 and TS 29.228 V18.0.0, and the RFCs named in each step.
> Transaction, dialog, SDP, PRACK and precondition are in the [glossary](../../../docs/glossary.md).

---

## What the call takes over from registration

A call uses three things that registration set up.

| Set up by registration | Kept by | Used in the call |
| --- | --- | --- |
| Service-Route | UE | To send the UE's requests to its S-CSCF. |
| Path | S-CSCF | To send requests for the UE to its P-CSCF. |
| IP association | P-CSCF | To check that a request comes from the registered UE. |

- Both UEs register before the call, as in the last session. Their REGISTERs open the capture.

> Guide: [registration, "10. The S-CSCF accepts: 200 OK"](../../../docs/guide/en/registration.md#10-the-s-cscf-accepts-200-ok)

---

## The nodes

```ims-scene
id: nodes
lead: One node acts in two positions, for the caller and for the callee. That is the key to reading this capture.
steps:
  - id: ues
    title: UE 1 (172.28.0.100) and UE 2 (172.28.0.101)
    focus: [ue1, ue2]
    text: |
      UE 1 is the caller, `+1-202-555-0101`. UE 2 is the callee, `+1-202-555-0102`.
      UE 1 does not know the address of UE 2. It knows only the number.
  - id: pcscf
    title: P-CSCF (172.28.0.40)
    focus: [pcscf]
    text: Serves both UEs. One call passes it twice, first on the caller's side, then on the callee's.
  - id: scscf
    title: S-CSCF (172.28.0.30)
    focus: [scscf]
    checkpoint: true
    text: It serves both UEs too, so it is both the caller's and the callee's S-CSCF.
  - id: icscf
    title: I-CSCF (172.28.0.20) and HSS (172.28.0.10)
    focus: [icscf, hss]
    text: The I-CSCF is the entry point of the callee's home network. It asks the HSS where the callee is.
  - id: mmtel
    title: MMTel AS (172.28.0.75)
    focus: [mmtel]
    text: |
      Takes the INVITE of the caller and of the callee, and sends each back.
      Here it changes nothing. The session on supplementary services shows what it does.
  - id: dns
    title: DNS (172.28.0.2)
    focus: [dns]
    checkpoint: true
    text: Holds the ENUM records that turn numbers into SIP URIs.
  - id: agw
    title: IMS-AGW (172.28.0.50)
    focus: [agw]
    text: |
      The media relay. It relays the RTP, and the P-CSCF controls it.
      The UEs never talk to each other directly.
```

| Node | Address | What it does in the call |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The caller, `+1-202-555-0101`. |
| UE 2 | 172.28.0.101 | The callee, `+1-202-555-0102`. |
| P-CSCF | 172.28.0.40 | Serves both UEs. One call passes it twice. |
| S-CSCF | 172.28.0.30 | Both the caller's and the callee's S-CSCF. |
| I-CSCF | 172.28.0.20 | The entry point of the callee's home network. It asks the HSS where the callee is. |
| DNS | 172.28.0.2 | Holds the ENUM records that turn numbers into SIP URIs. |
| Media relay | 172.28.0.50 | Relays the RTP. The P-CSCF controls it. |
| MMTel AS | 172.28.0.75 | Takes the INVITE of the caller and of the callee, and sends each back. |

- One node acts in two positions, for the caller and for the callee: the key to reading this capture. In a real network the two sides are often other nodes, in other networks.
- Here the MMTel AS sends each INVITE back unchanged. The session on supplementary services shows what it does.
- The HSS (172.28.0.10) answers the I-CSCF's location query over Cx, and the MMTel AS's query for the service settings over Sh.

> Guide: ["The nodes"](../../../docs/guide/en/call.md#the-nodes)

---

## Run it

```sh
docker compose up -d --build

# First terminal: UE 2 waits for a call.
docker compose run --rm ue2 answer \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2 -wav-out /captures/ue2-heard.wav

# Second terminal: UE 1 calls the number, plays a tone for 3 seconds, hangs up.
docker compose run --rm ue call -tone 3s tel:+1-202-555-0102
```

- `captures/ue2-heard.wav` holds what UE 2 heard.
- The numbers are in the range 555-0100 to 555-0199, which the North American Numbering Plan keeps for fiction.

> Guide: ["Make a call"](../../../docs/guide/en/call.md#make-a-call)

---

## Open the capture

```text
sip || diameter.cmd.code == 302 || dns.qry.type == 35 || rtp || rtcp
```

- **Telephony > VoIP Calls** lists the call.
  - **Flow Sequence** draws the ladder.
  - **Play Streams** plays both directions.
- If the RTP shows up as plain UDP, enable **rtp_udp** under **Analyze > Enabled Protocols**.
- The numbers 1 to 13 of the guide's ladder are the numbers of the next slides.

> Guide: ["Open the capture"](../../../docs/guide/en/call.md#open-the-capture) and ["The sequence"](../../../docs/guide/en/call.md#the-sequence), which has the full ladder.

---

## The shape of the whole

```ims-scene
id: overview
view: ladder
nodes: [ue1, pcscf, scscf, mmtel, icscf, hss, ue2]
lead: The INVITE passes the S-CSCF twice, and visits the MMTel AS each time.
steps:
  - id: orig
    title: The INVITE reaches the caller's S-CSCF (1, 2)
    show: [inv-ue1-p, inv-p-s]
    detail: []
    text: UE 1 sends the INVITE to the P-CSCF, which sends it to the S-CSCF of the Service-Route.
  - id: as-orig
    title: The AS on the caller's side sends a new INVITE back (3)
    show: [inv-s-as1, inv-as-s1]
    active: inv-s-as1
    detail: []
    text: An iFC has the S-CSCF send the INVITE to the AS. The AS sends a new INVITE back, with a Call-ID of its own.
  - id: entry
    title: The entry point asks where the callee is (4, 5)
    show: [inv-s-i, lir, lia, inv-i-s]
    active: lir
    detail: []
    checkpoint: true
    text: |
      The S-CSCF turns the number into a SIP URI and sends the INVITE to the I-CSCF.
      The I-CSCF asks the HSS for the callee's S-CSCF, and the INVITE comes back to the same S-CSCF.
  - id: as-term
    title: The AS on the callee's side sends it back again (6)
    show: [inv-s-as2, inv-as-s2]
    active: inv-s-as2
    detail: []
    text: This time the S-CSCF serves the callee, and visits the same AS.
  - id: term
    title: The INVITE follows the Path to UE 2 (6, 7)
    show: [inv-s-p, inv-p-ue2]
    active: inv-p-ue2
    detail: []
    text: |
      The new INVITEs of the AS give one call three dialogs:
      UE 1 with the caller's AS (dialog 1), the two ASs (dialog 2), and the callee's AS with UE 2 (dialog 3).
```

The INVITE passes the nodes in this order.

```ladder
UE 1, P-CSCF, S-CSCF, MMTel AS, I-CSCF, HSS, UE 2
UE 1 -> P-CSCF -> S-CSCF: INVITE (orig) // 1 - 2
S-CSCF -> MMTel AS: INVITE (orig) // 3
S-CSCF <- MMTel AS: new INVITE
S-CSCF -> I-CSCF: INVITE // 4
I-CSCF <-> HSS: LIR, LIA // 5
I-CSCF -> S-CSCF: INVITE (term) // 6
S-CSCF -> MMTel AS: INVITE (term)
S-CSCF <- MMTel AS: new INVITE
S-CSCF -> P-CSCF: INVITE (term) // 6
P-CSCF -> UE 2: INVITE (term) // 7
```

- `orig` is the originating position, the caller's side; `term` the terminating position, the callee's side.
- The AS is a routeing B2BUA (TS 24.229 5.7.5): its new INVITE has a Call-ID of its own, so one call has three dialogs. Session 7 shows how the AS makes them.

---

## The parts of the call

| Part | Steps | What happens |
| --- | --- | --- |
| The caller's side | 1 to 3 | The caller is asserted, the AS is passed, and the number becomes a SIP URI. |
| The entry point | 4 and 5 | The HSS names the S-CSCF that serves the callee. |
| The callee's side | 6 and 7 | The AS is passed, and the INVITE follows the Path to the callee's UE. |
| The answer | 8 to 11 | 183, PRACK, 180, PRACK, 200 OK, ACK. |
| Voice and hang-up | 12 and 13 | RTP through the relay. BYE ends the call. |

- Come back to this table at each new part: it is the map of the session.

---

## The INVITE, and the P-CSCF on the caller's side (1, 2)

```ims-scene
id: orig-pcscf
lead: The INVITE that leaves the P-CSCF names the media relay, not UE 1.
steps:
  - id: send
    title: UE 1 sends an INVITE to a number
    only: [inv-ue1-p]
    checkpoint: true
    text: |
      The Request-URI is only a number: UE 1 does not know where UE 2 is.
      Route names the P-CSCF and the Service-Route.
  - id: orig
    title: The P-CSCF tells it is the caller's side
    show: [p-orig]
    focus: [pcscf]
    text: The top Route lacks the `term` of its Path, so this pass is the caller's side.
  - id: context
    title: It asks the media relay for a context
    show: [ctx1-post]
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: |
      It asks for an access termination towards UE 1 and a core termination towards the network.
      This is not SIP: it is a small HTTP API of this project.
  - id: context-ok
    title: The media relay returns the context
    show: [ctx1-ok]
    zoom: [ue1, agw, ue2, pcscf]
    text: The relay answers with the addresses of the two terminations.
  - id: set
    title: It tells the access termination where to send
    show: [set1-put, set1-ok]
    hide: [ctx1-post, ctx1-ok]
    active: set1-put
    detail: [set1-put]
    zoom: [ue1, agw, ue2, pcscf]
    text: The POST that makes the context says nowhere to send. The address in UE 1's SDP comes in the PUT after it.
  - id: rewrite
    title: The P-CSCF adds headers and rewrites the SDP
    show: [p-rewrite]
    focus: [pcscf]
    checkpoint: true
    text: |
      The network trusts P-Asserted-Identity, not From. Whatever the UE writes in From does not matter.
      The SDP names the core termination. The network beyond sees no UE 1.
  - id: forward
    title: The INVITE reaches the S-CSCF
    show: [inv-p-s]
    text: The P-CSCF is in Record-Route now, so the later requests of the call pass it too.
```

| Look at | What to see |
| --- | --- |
| Request-URI | `tel:+1-202-555-0102`. The UE only dials a number. |
| Route | Two: the P-CSCF, and the Service-Route from registration. |
| Supported | `100rel`, `timer`, `199`, `precondition`. |
| Session-Expires | 1800. UE 1 refreshes the session with an UPDATE. |
| The SDP offer | UE 1's address and port. PCMU and telephone-event. |
| `a=curr` and `a=des` of the offer | Preconditions: UE 1's resources are there already. |
| Headers the P-CSCF adds | P-Asserted-Identity and Record-Route. |
| The SDP after the P-CSCF | `c=` and `m=` name the media relay. |

- The network trusts P-Asserted-Identity, not From. Whatever the UE writes in From does not matter.
- UE 1 has no access network that reserves resources, so its offer says that they are there already. A UE started with `-epc` is a VoLTE UE and offers AMR-WB and AMR before PCMU. Session 9, on VoLTE, follows that call and how it reserves its resources before the phone rings.
- The P-CSCF talks to the media relay over a small HTTP API of this project, which the capture shows as well. The standard protocol for the task is H.248.

> Guide: [1](../../../docs/guide/en/call.md#1-ue-1-sends-the-invite) and [2](../../../docs/guide/en/call.md#2-the-p-cscf-sends-it-to-the-callers-s-cscf). TS 24.229 5.1.3.1, 5.1.2A.1.1, 6.1.2, 5.2.6.2, 5.2.6.3.3. RFC 3262 4. RFC 3312. RFC 3264 5.1.

---

## The S-CSCF turns the number into a SIP URI (3)

```ims-scene
id: orig-scscf
lead: The S-CSCF passes the MMTel AS first, and only then turns the number into a SIP URI with ENUM in DNS.
steps:
  - id: arrive
    title: The S-CSCF serves the caller
    only: [inv-p-s, s-orig]
    focus: [scscf]
    text: The top Route carries `orig`, so the S-CSCF acts for the caller.
  - id: assert
    title: It writes identities and the service
    show: [s-assert]
    focus: [scscf]
    checkpoint: true
    text: It adds UE 1's tel URI as a second P-Asserted-Identity, and puts the ICSI of MMTel into P-Asserted-Service.
  - id: to-as
    title: An iFC sends the INVITE to the MMTel AS
    show: [inv-s-as1]
    zoom: [icscf, hss, scscf, mmtel]
    checkpoint: true
    text: It puts the AS on top of Route, then itself with an `odi`. The AS sends the INVITE back along that Route.
  - id: sh
    title: The AS reads the caller's services (UDR and UDA)
    show: [udr1, uda1]
    active: udr1
    detail: [udr1, uda1]
    zoom: [icscf, hss, scscf, mmtel]
    checkpoint: true
    text: The AS reads the user's service settings from the HSS over Sh. With the default settings nothing in the call changes.
  - id: back
    title: The AS sends a new INVITE back
    show: [inv-as-s1]
    zoom: [icscf, hss, scscf, mmtel]
    checkpoint: true
    text: The S-CSCF finds its `odi` in it and goes on with the next iFC.
  - id: enum
    title: The S-CSCF looks up ENUM
    only: [inv-as-s1, enum-q]
    active: enum-q
    zoom: [scscf, dns, mmtel]
    text: It asks for the NAPTR of a name made of the digits of the number, reversed, one label each.
  - id: enum-answer
    title: The NAPTR answer makes the SIP URI
    show: [enum-a]
    zoom: [scscf, dns, mmtel]
    checkpoint: true
    text: The service is `E2U+sip`. A regular expression makes the SIP URI from the number.
  - id: uri
    title: The Request-URI becomes a SIP URI
    show: [s-uri]
    focus: [scscf]
    zoom: [scscf, dns, mmtel]
    text: |
      This is the first change of the Request-URI of UE 1's INVITE.
      For a number with no record, the S-CSCF answers 404.
```

| Look at | What to see |
| --- | --- |
| P-Asserted-Identity | A second one is added: UE 1's tel URI. |
| P-Asserted-Service | The ICSI of MMTel. P-Preferred-Service is gone. |
| The INVITE to the AS | The AS on top of Route, then the S-CSCF with `odi`. `sescase=orig`. |
| The DNS lookup | NAPTR of `2.0.1.0.5.5.5.2.0.2.1.e164.arpa`, the number reversed. |
| The NAPTR answer | The service is `E2U+sip`. A regular expression makes the SIP URI. |
| Request-URI after the S-CSCF | `sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org`. |

The filter is `dns.qry.type == 35`. The records are in `deploy/compose/dns/e164.arpa.zone`.

- Build the ENUM domain by hand: reverse the digits, one label each.
- The AS reads the caller's services from the HSS over Sh. With the default settings it changes nothing and sends a new INVITE back. The S-CSCF finds its `odi` in it, goes on with the next iFC, and only then turns the number into a SIP URI.
- The UDR and UDA of the AS show with `diameter.cmd.code == 306`. The session on supplementary services reads them in detail.

> Guide: [3](../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri). TS 24.229 5.4.3.1, 5.4.3.2, 5.7.5. RFC 6050. RFC 6116.

---

## The I-CSCF and the LIR (4, 5)

```ims-scene
id: entry
lead: The I-CSCF asks the HSS for the callee's S-CSCF, and the INVITE comes back to the same S-CSCF.
steps:
  - id: lookup
    title: The S-CSCF looks for the I-CSCF
    only: [loc-q]
    zoom: [icscf, scscf, dns]
    text: As the P-CSCF did for the REGISTER, it looks up the domain of the Request-URI with NAPTR and SRV.
  - id: lookup-answer
    title: It finds the entry point of the home network
    show: [loc-a]
    zoom: [icscf, scscf, dns]
    checkpoint: true
    text: The answer is the address of the I-CSCF, the entry point of that domain.
  - id: forward
    title: The INVITE reaches the I-CSCF over TCP
    only: [inv-s-i]
    zoom: [icscf, scscf, dns]
    checkpoint: true
    text: The INVITE has grown at every hop and is over 1300 bytes now. From the S-CSCF on, the Via shows `SIP/2.0/TCP`.
  - id: lir
    title: The I-CSCF asks where the callee is (LIR)
    show: [lir]
    zoom: [icscf, hss, scscf]
    text: "Cx command 302. It asks what the UAR of registration asks: which S-CSCF serves the user."
  - id: lia
    title: The HSS names the S-CSCF (LIA)
    show: [lia]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: UE 2 is registered, so the HSS returns the Server-Name of the S-CSCF where UE 2 registered.
  - id: back
    title: The INVITE comes back to the same S-CSCF
    show: [inv-i-s]
    zoom: [icscf, hss, scscf]
    text: |
      The I-CSCF adds no Record-Route.
      So the later PRACK, ACK and BYE skip the I-CSCF.
```

| Look at | What to see |
| --- | --- |
| DNS lookups of the S-CSCF | NAPTR and SRV as in registration. They give the I-CSCF. |
| Via after the S-CSCF | `SIP/2.0/TCP`: the INVITE has grown over 1300 bytes. |
| LIR | Carries the public identity of the callee. |
| LIA | DIAMETER_SUCCESS, and the Server-Name of the S-CSCF where UE 2 is registered. |
| The INVITE the I-CSCF forwards | That S-CSCF on top of Route. No Record-Route. |

The filter is `diameter.cmd.code == 302`.

- The I-CSCF adds no Record-Route, so the later PRACK, ACK and BYE skip it.
- The LIR asks what the UAR of registration asks: which S-CSCF serves the user.

> Guide: [4](../../../docs/guide/en/call.md#4-the-s-cscf-finds-the-callees-home-network) and [5](../../../docs/guide/en/call.md#5-the-i-cscf-asks-the-hss-where-the-callee-is-lir-and-lia). TS 24.229 5.3.2.1. TS 29.228 6.1.4. RFC 3261 18.1.1.

---

## The S-CSCF and the P-CSCF on the callee's side (6, 7)

```ims-scene
id: term
lead: The callee's S-CSCF follows the Path, and the P-CSCF makes a second context and delivers the INVITE to UE 2.
steps:
  - id: arrive
    title: The S-CSCF serves the callee
    only: [inv-i-s, s-term]
    focus: [scscf]
    text: The Route it got has no `orig`, so this time it acts for the callee.
  - id: to-as
    title: The callee's iFC sends the INVITE to the MMTel AS
    checkpoint: true
    show: [inv-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    text: To the same AS, now with `sescase=term`.
  - id: sh
    title: The AS reads the callee's services
    checkpoint: true
    show: [udr2, uda2]
    active: udr2
    detail: [udr2, uda2]
    zoom: [icscf, hss, scscf, mmtel]
    text: This time it reads the services of UE 2. Nothing changes here either.
  - id: back
    title: The AS sends a new INVITE back
    checkpoint: true
    show: [inv-as-s2]
    zoom: [icscf, hss, scscf, mmtel]
    text: This INVITE starts the dialog of the callee's AS with UE 2.
  - id: path
    title: The S-CSCF routes towards UE 2
    only: [inv-as-s2, s-path]
    focus: [scscf]
    text: |
      It sets the Request-URI to UE 2's Contact, and puts the Path of UE 2's REGISTER into Route.
      This is the second change of the Request-URI.
  - id: to-pcscf
    title: The INVITE reaches the P-CSCF of the Path
    checkpoint: true
    show: [inv-s-p]
    text: The Path names the P-CSCF that UE 2 registered through.
  - id: p-term
    title: The P-CSCF tells it is the callee's side
    checkpoint: true
    only: [inv-s-p, p-term]
    focus: [pcscf]
    text: The top Route is the URI of its Path, with the user part `term`, so this pass is the callee's side.
  - id: context
    title: It makes a second context
    checkpoint: true
    show: [ctx2-post, ctx2-ok]
    active: ctx2-post
    detail: [ctx2-post, ctx2-ok]
    zoom: [ue1, agw, ue2, pcscf]
    text: Each pass of the P-CSCF handles its own side only.
  - id: set
    title: It tells the core termination where to send
    show: [set2-put, set2-ok]
    active: set2-put
    detail: [set2-put]
    zoom: [ue1, agw, ue2, pcscf]
    text: It sends to the address in the offer, the core termination of context 1. This too comes in a PUT.
  - id: deliver
    title: The INVITE reaches UE 2
    show: [inv-p-ue2]
    zoom: [ue1, agw, ue2, pcscf]
    text: "It goes to the address that UE 2's REGISTER came from, not to the Contact: behind NAT only that address reaches the UE."
```

| Look at | What to see |
| --- | --- |
| Route as it reaches the S-CSCF | No `orig`: this time the S-CSCF serves the callee. |
| The INVITE to the AS | `sescase=term`. The AS sends a new INVITE back. |
| P-Called-Party-ID | The destination that was called. |
| Request-URI after the S-CSCF | The Contact that UE 2 registered. |
| Route after the S-CSCF | The Path of UE 2's REGISTER. |
| The SDP after the P-CSCF | Names the access termination of a second context. |
| Where the INVITE is sent | The address that UE 2's REGISTER came from. |

- The INVITE left the S-CSCF for the I-CSCF and comes back to the same S-CSCF.
- The P-CSCF knows it is on the callee's side: the top Route is the URI of its Path, with the user part `term`.
- It sends to the address of the REGISTER, not to the Contact: behind NAT only that address reaches the UE.

> Guide: [6](../../../docs/guide/en/call.md#6-the-callees-s-cscf-sends-the-invite-along-the-path) and [7](../../../docs/guide/en/call.md#7-the-p-cscf-delivers-the-invite-to-ue-2). TS 24.229 5.4.3.3, 5.2.6.4.3.

---

## UE 2 answers with a reliable 183 (8)

```ims-scene
id: answer
lead: The 183 goes back over the nodes of the INVITE, and each pass of the P-CSCF rewrites its SDP.
steps:
  - id: answer
    title: UE 2 answers with a 183
    only: [r183-ue2-p]
    checkpoint: true
    text: |
      A reliable provisional response, with the SDP answer and `Require: 100rel`.
      UE 2 sends the 183 again until a PRACK arrives.
  - id: relay
    title: The callee's P-CSCF tells the media relay of the answer
    show: [mod2-put, mod2-ok]
    active: mod2-put
    zoom: [ue1, agw, ue2, pcscf]
    text: It tells the termination that faces the answering side, the access one here, where UE 2 receives.
  - id: up
    title: The 183 reaches the callee's AS
    show: [r183-p-s3, r183-s-as3]
    active: r183-p-s3
    checkpoint: true
    text: The P-CSCF rewrites the SDP to name the core termination of context 2. The far end of dialog 3 is the callee's AS.
  - id: dialog2
    title: The AS copies the 183 onto the next dialog
    only: [r183-as-s2, r183-s-i]
    zoom: [icscf, hss, scscf, mmtel]
    active: r183-s-i
    detail: []
    text: A response follows the Via of the INVITE, so it passes the I-CSCF too.
  - id: dialog2-back
    title: The 183 reaches the caller's AS
    show: [r183-i-s, r183-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: r183-s-as2
    detail: []
    text: From the I-CSCF it goes back to the S-CSCF, its second pass there, and on to the caller's AS.
  - id: dialog1
    title: The 183 reaches the caller's P-CSCF
    only: [r183-as-s1, r183-s-p1]
    active: r183-s-p1
    detail: []
    text: The caller's AS copies the 183 onto dialog 1.
  - id: relay1
    title: The caller's P-CSCF tells the relay of the answer too
    show: [mod1-put, mod1-ok]
    active: mod1-put
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: This answer comes from the network side, so it tells the core termination where to send.
  - id: ue1
    title: The 183 reaches UE 1
    show: [r183-p-ue1]
    zoom: [ue1, agw, ue2, pcscf]
    text: UE 1 sees the access termination of its own context. Each UE talks only to the media relay.
```

| Look at | What to see |
| --- | --- |
| The SDP of the 183 | The answer: PCMU, UE 2's address and port. |
| Require and RSeq of the 183 | `100rel` and `precondition`: a reliable provisional response. |
| The preconditions of the 183 | Both ends have their resources. No `a=conf`. |
| To tag, Contact, Record-Route of the 183 | They make the early dialog. |
| The SDP of the 183 as UE 1 gets it | Rewritten twice by the P-CSCF. It names the relay. |

- The 183 goes back along the Via of the INVITE, so it passes the I-CSCF, which added no Record-Route. What skips the I-CSCF are the requests within the dialogs that follow.
- Before it rewrites the SDP, the P-CSCF tells the media relay with a `Modify` (an HTTP PUT) where the termination facing the answering side sends.

> Guide: [8](../../../docs/guide/en/call.md#8-ue-2-answers-the-offer-a-reliable-183). RFC 3264 6.1. RFC 3262 3. TS 24.229 5.1.4.1.

---

## UE 1 acknowledges the 183 with a PRACK (9)

```ims-scene
id: prack
lead: A PRACK is a request within a dialog. Each of the three dialogs has one PRACK and one 200.
steps:
  - id: dialog1
    title: UE 1's PRACK ends at the caller's AS
    only: [prack-ue1-p, prack-p-s1, prack-s-as1, pok-as-s1, pok-s-p1, pok-p-ue1]
    active: prack-ue1-p
    checkpoint: true
    text: |
      The PRACK follows the route set, through the P-CSCF and the S-CSCF.
      The AS at the far end of dialog 1 answers 200 at once.
  - id: dialog2
    title: The AS sends a PRACK of its own on the next dialog
    only: [prack-as-s2, prack-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: prack-s-as2
    detail: []
    text: The PRACK of dialog 2 goes from the caller's AS through the S-CSCF to the callee's AS. It skips the I-CSCF.
  - id: dialog2-ok
    title: The callee's AS answers the PRACK with 200
    show: [pok-as-s2, pok-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: pok-s-as2
    detail: []
    checkpoint: true
    text: The 200 goes back the same way to the caller's AS.
  - id: dialog3
    title: The PRACK reaches UE 2, which answers 200
    only: [prack-as-s3, prack-s-p3, prack-p-ue2, pok-ue2-p, pok-p-s3, pok-s-as3]
    active: prack-p-ue2
    detail: []
    text: |
      The callee's AS sends a PRACK on dialog 3.
      With the PRACK in, UE 2 stops sending the 183 again and answers 200.
```

| Look at | What to see |
| --- | --- |
| RAck of the PRACK | The RSeq of the 183 and the CSeq of the INVITE. |
| The way of the PRACK | It ends at the caller's AS, which sends it on in the next dialog. |

- A PRACK is a request within a dialog, so it ends at the far end of that dialog. The AS answers the PRACK it got with 200 at once and sends a PRACK of its own on the next dialog: one PRACK and one 200 for each of the three dialogs. None passes the I-CSCF, which added no Record-Route.
- The messages of two dialogs take ways of their own, so their order in the capture is not fixed.

> Guide: [9](../../../docs/guide/en/call.md#9-ue-1-acknowledges-the-183-prack). RFC 3262 3, 4. RFC 3261 12.2.1.1.

---

## 180, 200 OK and ACK (10, 11)

```ims-scene
id: confirm
lead: When UE 2 answers, the 200 OK goes back dialog by dialog, and the far end of each acknowledges it with an ACK.
steps:
  - id: ring
    title: UE 2 alerts its user and sends a 180
    only: [ue2-ring, r180-ue2-p]
    active: r180-ue2-p
    detail: [ue2-ring, r180-ue2-p]
    checkpoint: true
    text: |
      The 180 reaches UE 1 over the nodes of the 183, and gets a PRACK.
      A callee whose resources are not there yet sends no 180 until they are.
  - id: answer
    title: UE 2 answers, and the 200 OK reaches the callee's AS
    only: [ok-ue2-p, ok-p-s3, ok-s-as3]
    active: ok-ue2-p
    checkpoint: true
    text: The 200 OK carries no SDP. The answer came in the reliable 183.
  - id: ack3
    title: The callee's AS acknowledges at once
    show: [ack-as-s3, ack-s-p3, ack-p-ue2]
    active: ack-p-ue2
    detail: []
    checkpoint: true
    text: |
      The AS acknowledges UE 2's 200 on its own dialog at once.
      So the ACK to UE 2 can come before the 200 to UE 1.
  - id: dialog2
    title: The 200 OK of dialog 2 passes the I-CSCF
    only: [ok-as-s2, ok-s-i]
    zoom: [icscf, hss, scscf, mmtel]
    active: ok-s-i
    detail: []
    text: The 200 OK follows the Via, so it passes the I-CSCF as the 183 did.
  - id: dialog2-back
    title: The 200 OK reaches the caller's AS
    show: [ok-i-s, ok-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: ok-s-as2
    detail: []
    text: From the I-CSCF it goes back to the S-CSCF and on to the caller's AS.
  - id: dialog2-ack
    title: The caller's AS sends the ACK
    only: [ack-as-s2, ack-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: ack-s-as2
    detail: []
    checkpoint: true
    text: The ACK follows the route set, so it skips the I-CSCF.
  - id: dialog1
    title: The 200 OK reaches UE 1
    only: [ok-as-s1, ok-s-p1, ok-p-ue1]
    active: ok-p-ue1
    text: Dialog 1 is confirmed now.
  - id: ack1
    title: UE 1 sends the ACK
    show: [ack-ue1-p, ack-p-s1, ack-s-as1]
    active: ack-ue1-p
    text: The ACK follows the route set to the AS at the far end of dialog 1.
```

| Look at | What to see |
| --- | --- |
| 180 Ringing | Sent once the preconditions are met. It gets a PRACK too. |
| The 200 OK to the INVITE | The dialog is confirmed. |
| The SDP of the 200 OK | None. The answer came in the reliable 183. |
| ACK | Follows the route set. It is not part of the INVITE transaction. |

- UE 2 sends the 200 OK again, with growing intervals, until the ACK arrives.
- A callee whose resources are not there yet sends no 180 until they are. Both UEs here have theirs, so the 180 goes as soon as the 183 has its PRACK.
- The AS acknowledges UE 2's 200 on its own dialog at once, so the ACK to UE 2 can come before the 200 to UE 1.

> Guide: [10](../../../docs/guide/en/call.md#10-ue-2-rings-then-answers-180-and-200-ok) and [11](../../../docs/guide/en/call.md#11-ue-1-confirms-ack). RFC 3312 6. TS 24.229 5.1.4.1. IR.92 2.4.1. RFC 3261 13.3.1.4, 13.2.1, 13.2.2.4. RFC 3262 5.

---

## The voice flows through the relay (12)

```ims-scene
id: media
lead: The RTP flows only between a UE and the media relay, and passes two contexts inside the relay.
steps:
  - id: send
    title: UE 1 sends RTP to the media relay
    only: [rtp-ue1-agw]
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: It sends to the address it got in the 183, the access termination of context 1.
  - id: inside
    title: Inside the relay it passes two contexts
    show: [agw-relay]
    focus: [agw]
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: |
      One call has two contexts in the relay.
      The RTP crosses the relay twice, as it would cross the IMS-AGWs of two networks.
  - id: deliver
    title: The RTP reaches UE 2
    show: [rtp-agw-ue2]
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: The UEs never talk to each other directly. With the UEs behind two P-CSCFs, the call would look the same.
  - id: latch
    title: The relay picks where to send (latching)
    only: [rtp-ue2-agw, agw-latch]
    focus: [agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: |
      Towards the UE, it sends to where the first packet came from.
      A UE behind NAT, such as a softphone on the host, sends from an address its SDP cannot know.
  - id: back
    title: UE 2's voice reaches UE 1
    show: [rtp-agw-ue1]
    zoom: [ue1, agw, ue2, pcscf]
    text: Towards the network, it does not latch, and sends where the latest SDP says.
  - id: rtcp
    title: The RTCP passes the relay too
    only: [rtcp-ue1-agw]
    zoom: [ue1, agw, ue2, pcscf]
    text: The RTCP uses the port above the RTP port. At the end of the call an RTCP BYE goes out.
```

```text
UE 1 -> relay access (context 1) -> relay core (context 1)
     -> relay core (context 2) -> relay access (context 2) -> UE 2
```

| Look at | What to see |
| --- | --- |
| Interval and size of the RTP | Every 20 ms, 160 bytes of G.711 μ-law. |
| Source and destination of the RTP | Always a UE and the relay. The UEs never talk to each other. |
| Ports for sending and receiving | The same port: symmetric RTP. |
| RTCP | The port above the RTP port. At least every 5 seconds. |

- Towards the UE, the relay sends to where the first packet came from. This is called latching. Towards the network, it sends where the latest SDP says.

The filter is `rtp || rtcp`. **Play Streams** plays the voice.

> Guide: [12](../../../docs/guide/en/call.md#12-the-voice-flows-through-the-relay). RFC 3550 5.1, 6. RFC 3551 4.5.14. RFC 4961. IR.92 3.2.4.

---

## Two contexts, and latching (12)

- One call has two contexts in the relay: one from the pass of the P-CSCF on the caller's side, one from the pass on the callee's side. Each pass handles its own side only.
- So the RTP crosses the relay twice, as it would cross the IMS-AGWs of two networks, and the capture tells the two sides apart. With the UEs behind two P-CSCFs, the call would look the same.
- Latching is for UEs behind NAT: a softphone on the host sends from an address that its SDP cannot know. The P-CSCF and the IMS-AGW of a real network do the same.
- An RTCP compound packet holds a sender report (a receiver report while the UE sends nothing) and an SDES with the CNAME. An RTCP BYE ends the stream.

---

## Hanging up (13)

```ims-scene
id: bye
lead: The BYE goes dialog by dialog too, and the P-CSCF removes the context of its side when the 200 passes.
steps:
  - id: bye1
    title: UE 1 sends the BYE
    only: [bye-ue1-p, bye-p-s1, bye-s-as1]
    active: bye-ue1-p
    checkpoint: true
    text: The BYE is a request within the dialog, and follows the route set to the caller's AS.
  - id: ok1
    title: The caller's AS answers 200
    show: [bok-as-s1, bok-s-p1, bok-p-ue1]
    active: bok-s-p1
    detail: []
    text: The AS answers UE 1's BYE on its own dialog.
  - id: delete1
    title: The P-CSCF removes context 1
    show: [del1, del1-ok]
    active: del1
    zoom: [ue1, agw, ue2, pcscf]
    checkpoint: true
    text: |
      It removes it when the 200 passes. It waits for the answer because a BYE can be turned down.
      No SIP message shows the removal: the P-CSCF calls the API of the relay.
  - id: dialog2
    title: The AS sends a BYE of its own on the next dialog
    only: [bye-as-s2, bye-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: bye-s-as2
    detail: []
    text: The BYE of dialog 2 goes from the caller's AS through the S-CSCF to the callee's AS. The 200 to UE 1 and the BYE to UE 2 may come in either order.
  - id: dialog2-ok
    title: The callee's AS answers the BYE with 200
    show: [bok-as-s2, bok-s-as2]
    zoom: [icscf, hss, scscf, mmtel]
    active: bok-s-as2
    detail: []
    text: The 200 goes back the same way to the caller's AS.
  - id: dialog3
    title: The BYE reaches UE 2
    only: [bye-as-s3, bye-s-p3, bye-p-ue2]
    active: bye-p-ue2
    detail: []
    checkpoint: true
    text: The callee's AS sends a BYE on dialog 3.
  - id: ok3
    title: UE 2 sends the last 200
    only: [bok-ue2-p, bok-p-s3, bok-s-as3]
    active: bok-ue2-p
    detail: []
    text: The last 200 comes from UE 2.
  - id: delete2
    title: The P-CSCF removes context 2
    show: [del2, del2-ok]
    active: del2
    zoom: [ue1, agw, ue2, pcscf]
    text: The context of the pass on the callee's side goes too, when the 200 passes.
```

| Look at | What to see |
| --- | --- |
| BYE | A request within the dialog. It follows the route set. |
| Reason of the BYE | `RELEASE_CAUSE;cause=1`: why the call ended. |
| The 200 to the BYE | The far end of each dialog sends one. The last is UE 2's. |
| What the P-CSCF does | It removes the relay contexts when the 200 passes. |

- The P-CSCF waits for the answer because a BYE can be turned down.
- Removing the contexts shows in no SIP message: the P-CSCF calls the API of the relay, a small HTTP API of this project, so that the relay reads more easily than with a protocol as large as H.248.
- The AS answers UE 1's BYE on its own dialog and sends a BYE of its own on the next, so the 200 to UE 1 and the BYE to UE 2 may come in either order.

> Guide: [13](../../../docs/guide/en/call.md#13-ue-1-hangs-up-bye). TS 24.229 5.1.5. IR.92 2.2.4. RFC 3261 15.1.1, 15.1.2, 12.2.2.

---

## Watching it fail

```ims-scene
id: failures
lead: The node that made a response is the source of the first packet that carries it.
steps:
  - id: busy
    title: Busy. UE 2 turns the call down with 486
    checkpoint: true
    only: [r486-ue2-p]
    text: Start UE 2 with `-reject 486`, and the call ends with 486 Busy Here.
  - id: unregistered
    title: Not registered. The LIA says 2003
    checkpoint: true
    only: [lir-u, lia-u]
    active: lia-u
    detail: [lir-u, lia-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: Call `tel:+12025550103`, which does not register. An unregistered user still has an iFC that calls the MMTel AS.
  - id: select
    title: The I-CSCF picks an S-CSCF
    checkpoint: true
    show: [inv-i-s-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: Capabilities came back instead of a name, so the I-CSCF picks an S-CSCF as for a first registration.
  - id: profile
    title: The S-CSCF takes the profile (SAR and SAA)
    only: [sar-u, saa-u]
    active: sar-u
    detail: [sar-u, saa-u]
    zoom: [icscf, hss, scscf, mmtel]
    text: It takes the profile of the unregistered user from the HSS with a SAR of UNREGISTERED_USER.
  - id: as
    title: The AS sends the INVITE back unchanged
    checkpoint: true
    only: [inv-s-as-u, inv-as-s-u]
    active: inv-as-s-u
    zoom: [icscf, hss, scscf, mmtel]
    text: No call forwarding is set up, so the AS does not take the call.
  - id: no-contact
    title: The S-CSCF has no registration to route to
    only: [s-480]
    focus: [scscf]
    zoom: [icscf, hss, scscf, mmtel]
    text: The callee is not registered, so the S-CSCF cannot deliver the INVITE that the AS sent back. No service took the call.
  - id: r480
    title: The S-CSCF answers 480
    checkpoint: true
    show: [r480-s-as]
    zoom: [icscf, hss, scscf, mmtel]
    text: The first packet goes from the S-CSCF to the AS. The AS copies each response onto its own dialog, so look beyond the AS.
```

| Case | How to try it | How it ends | Who answers |
| --- | --- | --- | --- |
| Busy | Start UE 2 with `-reject 486`. | 486 Busy Here | UE 2 |
| Not registered | Call `tel:+12025550103`. | 480 Temporarily Unavailable | S-CSCF |
| Unknown number | Call `tel:+12025550198`. | 404 Not Found | S-CSCF |

- An unregistered user still has an iFC that calls the MMTel AS. The LIA says DIAMETER_UNREGISTERED_SERVICE (2003). The S-CSCF that the I-CSCF picks answers 480 once the AS sends the INVITE back.
- An unknown number has no ENUM record. This project has no BGCF, so the S-CSCF answers 404.
- Which node made a response: the source address of the first packet that carries it. The AS copies each response onto its own dialog, so look beyond the AS.

> Guide: ["Make a call"](../../../docs/guide/en/call.md#make-a-call), [3](../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri), [5](../../../docs/guide/en/call.md#5-the-i-cscf-asks-the-hss-where-the-callee-is-lir-and-lia). TS 29.228 6.1.4.1. TS 24.229 5.4.3.3.

---

## Watching it fail: an unknown number

```ims-scene
id: unknown-number
lead: When ENUM has no record for the number, the S-CSCF answers 404.
steps:
  - id: unknown
    title: Unknown number. ENUM has no record
    checkpoint: true
    only: [enum-q-x, enum-a-x]
    active: enum-a-x
    detail: [enum-q-x, enum-a-x]
    zoom: [scscf, dns, mmtel]
    text: Call `tel:+12025550198`. ENUM has no record for this number.
  - id: no-uri
    title: The S-CSCF cannot make a SIP URI
    only: [enum-a-x, s-404]
    focus: [scscf]
    zoom: [scscf, dns, mmtel]
    text: This project has no BGCF, so the call cannot go to the telephone network either.
  - id: r404
    title: The S-CSCF answers 404
    only: [r404-s-as]
    zoom: [scscf, dns, mmtel]
    text: The 404 answers the new INVITE of the AS. Here too the first packet comes from the S-CSCF.
```

- An unknown number has no ENUM record. This project has no BGCF, so the S-CSCF answers 404.

> Guide: [3](../../../docs/guide/en/call.md#3-the-s-cscf-turns-the-number-into-a-sip-uri). TS 24.229 5.4.3.2.

---

## Calling from the Lab

```sh
docker compose --profile phones up -d --build
```

1. Open <http://localhost:8080>. In the card of `phone2`, set the answer policy to `manual`.
2. In the card of `phone1`, press **Call**.
3. In the card of `phone2`, press **Answer**. Press **Hang up** in either card.
4. **ladder** next to the call draws its ladder, even while the call is up.

- Hanging up before the answer makes the caller send CANCEL, and the callee decline with 603.
- Do not run the `phones` and the `ue` profiles at once: two UEs of one subscriber overwrite each other's registration. The HTTP API that drives a UE is this project's own; none of it goes to the IMS.

> Guide: [Watch the IMS in the Lab, "Drive the UE simulators"](../../../docs/guide/en/lab.md#drive-the-ue-simulators), and [the call guide, "Drive a UE over HTTP"](../../../docs/guide/en/call.md#drive-a-ue-over-http)

---

## Check it in the code

| Step | File to open |
| --- | --- |
| 1, 8 to 13. The UE calls, answers, sends voice | `internal/ue/call.go`, `internal/ue/media.go` |
| 2, 7. The two passes of the P-CSCF, rewriting the SDP | `internal/pcscf/session.go` |
| 3, 6. The S-CSCF on the caller's and the callee's side | `internal/scscf/session.go` |
| 3, 6. The MMTel AS, a routeing B2BUA | `internal/mmtel/call.go` |
| 8 to 10. Preconditions | `internal/precondition` |
| 3. ENUM | `internal/sipdns/enum.go` |
| 5. LIR | `internal/icscf/session.go` |
| 12. Relaying and latching | `internal/agw/relay.go` |
| Dialogs and route sets | `internal/sip/dialog.go` |

- In `internal/scscf/session.go`, find the condition that tells originating from terminating.

> Guide: ["What this project simplifies"](../../../docs/guide/en/call.md#what-this-project-simplifies)

---

[layout: exercise]

## Looking back

1. How often does the Request-URI of UE 1's INVITE change before it reaches UE 2?
2. How does the S-CSCF tell whether it serves the caller or the callee?
3. Why is the I-CSCF not on the way of the PRACK and the BYE?
4. Whose address is in the SDP that UE 1 receives?
5. Which node answers 480, which answers 404, and on what grounds?
6. Why does UE 2's 200 OK carry no SDP?

---

[layout: answers]

## Looking back: answers

1. Twice. The S-CSCF turns the tel URI into a SIP URI, and the callee's S-CSCF sets UE 2's Contact.
2. By `orig` in the URI on top of Route.
3. It adds no Record-Route.
4. The media relay's: the access termination of UE 1's context.
5. The S-CSCF answers both. 480, because the LIA said DIAMETER_UNREGISTERED_SERVICE and the service of the unregistered user, the MMTel AS, did not take the call. 404, because ENUM has no record.
6. The answer came in the reliable 183 (RFC 3262 5).

---

## Next session

- Next is SMS. It uses registration, and the way the I-CSCF finds the S-CSCF, as they are.
- The iFCs that sent this call to the MMTel AS take the lead: they bring a short message to the IP-SM-GW.

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

> The guide of the next session is [Follow a short message through the IMS](../../../docs/guide/en/sms.md).
