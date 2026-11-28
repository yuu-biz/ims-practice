---
title: Supplementary services with the MMTel AS
footer: ims-practice 7. Supplementary services
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: the caller, at: [0, 1]}
  - {id: ue2, label: UE 2, role: sets up its services, at: [0, 2]}
  - {id: ue3, label: UE 3, role: the forward target, at: [0, 3]}
  - {id: pcscf, label: P-CSCF, role: asserts and withholds, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: scscf, label: S-CSCF, role: sends to the AS by iFC, at: [3, 1]}
  - {id: hss, label: HSS, role: iFCs and user settings, at: [4, 1]}
  - {id: as1, label: MMTel AS, role: UE 1's services, at: [4, 0]}
  - {id: as2, label: MMTel AS, role: UE 2's services, at: [4, 2]}
  - {id: mrfc, label: MRFC, role: announcements, at: [5, 2]}
events:
  # Reference points
  - {id: ref-gm, rel: Gm, from: ue2, to: pcscf, proto: SIP}
  - {id: ref-mw, rel: Mw, from: pcscf, to: scscf, proto: SIP}
  - {id: ref-isc, rel: ISC, from: scscf, to: as2, proto: SIP}
  - {id: ref-cx, rel: Cx, from: scscf, to: hss, proto: Diameter}
  - {id: ref-sh, rel: Sh, from: as2, to: hss, proto: Diameter}
  - {id: ref-ut, rel: Ut, from: ue2, to: as2, proto: HTTP}
  - {id: ref-mr, rel: Mr', from: as2, to: mrfc, proto: SIP}
  # A plain call through the AS
  - id: c1-ue-p
    msg: INVITE
    from: ue1
    to: pcscf
    proto: SIP
    note: (C1)
    fields:
      - Request-URI: "`tel:+12025550102`"
  - id: c1-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (C1)
    fields:
      - P-Charging-Vector: "The P-CSCF puts in the `icid-value`"
  - id: scscf-ifc-orig
    do: matches the iFCs
    at: scscf
    fields:
      - Priorities 0 to 2: The criteria of SMS, REGISTER and MESSAGE. No match
      - Priority 10: INVITE and ORIGINATING_REGISTERED. A match
      - ServerName: "`sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060`"
      - DefaultHandling: SESSION_TERMINATED
  - id: scscf-default
    do: when the AS does not answer
    at: scscf
    fields:
      - Originating (priorities 10 and 13): SESSION_TERMINATED. The call ends
      - Terminating (priorities 11 and 12): SESSION_CONTINUED. It goes on without the AS
      - In exchange: Outgoing barring always holds. Incoming calls arrive without the barring of the AS
  - id: c1-s-as1
    msg: INVITE
    from: scscf
    to: as1
    proto: SIP
    note: (C1)
    fields:
      - P-Served-User: "`<sip:+12025550101@…>;sescase=orig;regstate=reg`"
      - Route: "`<sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>`"
  - id: as1-served
    do: picks the served user and case
    at: as1
    fields:
      - Served user: UE 1
      - Case: orig
      - Services it runs: OCB, OIR, TIP for the responses
  - id: udr1
    msg: UDR
    from: as1
    to: hss
    proto: Sh
    note: (UE 1)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: "`MMTEL-Services`"
  - id: uda1
    msg: UDA
    from: hss
    to: as1
    proto: Sh
    note: (UE 1)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Sh-User-Data: UE 1's MMTelServices document
  - id: c2-as1-s
    msg: INVITE
    from: as1
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - Call-ID: A new one, made by the AS
      - icid-value: The same as on C1
      - Route: "`<sip:scscf.…:5060;lr;odi=...>`"
  - id: scscf-odi
    do: goes on from the ODI
    at: scscf
    fields:
      - What it reads: "The `odi` in Route"
      - Where it goes on: After the criterion that matched (priority 10)
      - Result: No originating criterion is left. ENUM, then the I-CSCF
  - id: c2-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    note: (C2)
    fields:
      - Request-URI: "`sip:+12025550102@…` after ENUM"
  - id: lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2's S-CSCF
  - id: c2-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (C2)
  - id: c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;sescase=term;regstate=reg`"
      - The criterion that matched: Priority 11, INVITE and TERMINATING_REGISTERED
  - id: as2-served
    do: picks the served user and case
    at: as2
    fields:
      - Served user: UE 2
      - Case: term
      - Services it runs: ICB and ACR, CFU and CFNL, OIP and TIR, the leg to the user
      - By how the leg ends: CFB or CFNR
  - id: udr2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (UE 2)
    fields:
      - Data-Reference: RepositoryData (0)
      - Service-Indication: "`MMTEL-Services`"
  - id: uda2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (UE 2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Sh-User-Data: UE 2's MMTelServices document
  - id: c3-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
    fields:
      - Call-ID: Another new one
      - icid-value: The same as on C1 and C2
  - id: c3-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: c3-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - P-Charging-Vector: None. The P-CSCF removes it from what it sends to a UE
  - id: ok-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: pcscf-tip
    do: asserts who answered
    at: pcscf
    fields:
      - P-Asserted-Identity: The P-Called-Party-ID of the INVITE
      - Value: "`<sip:+12025550102@…>`"
  - id: ok-p-ue1
    msg: 200 OK
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - P-Asserted-Identity: "`<sip:+12025550102@…>`: this is TIP"
  # The document of the settings, and Ut
  - id: hss-doc
    do: holds a document per user
    at: hss
    fields:
      - Document: The simservs of XCAP
      - Kept as: "Sh repository data, Service-Indication `MMTEL-Services`"
      - First rules: "Those of CDIV and CB `rule-deactivated`, with no target"
  - id: as2-nocopy
    do: keeps no copy
    at: as2
    fields:
      - When it reads: With a UDR for each call
      - Why: Any instance of the AS can take any call
  - id: ut-get1
    msg: GET
    from: ue2
    to: as2
    proto: HTTP
    note: (1st)
    fields:
      - URI: The whole document. Its user part is the XUI
      - Authorization: None
  - id: as2-challenge
    do: makes the challenges
    at: as2
    fields:
      - What it asks the HSS: Nothing. No UDR
      - realm: The home domain, the same for every user
      - algorithm: SHA-256, then MD5
      - nonce: The same in both challenges of this 401
  - id: ut-401
    msg: "401"
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - WWW-Authenticate: "Two `Digest` challenges, SHA-256 and MD5"
  - id: ut-get2
    msg: GET
    from: ue2
    to: as2
    proto: HTTP
    note: (2nd)
    fields:
      - Authorization: "`Digest`, the answer to the first challenge"
  - id: ut-udr-cred
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (password)
    fields:
      - Service-Indication: "`Ut-Digest-Credential`"
  - id: ut-uda-cred
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (password)
    fields:
      - Content: HA1 values, not the password
  - id: ut-udr-doc
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (document)
    fields:
      - Service-Indication: "`MMTEL-Services`"
  - id: ut-uda-doc
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (document)
    fields:
      - Content: The document, with its Sequence Number 0
  - id: ut-200-get
    msg: 200 OK
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - ETag: "`\"0\"`"
      - Body: The whole document
  - id: ut-put
    msg: PUT
    from: ue2
    to: as2
    proto: HTTP
    fields:
      - URI: "A node selector after `~~`, pointing to the rule `cfu`"
      - If-Match: "`\"0\"`"
      - Content-Type: "`application/xcap-el+xml`"
      - Body: "The rule without `rule-deactivated`, with the target `tel:+12025550103`"
  - id: ut-udr-cred2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (again)
  - id: ut-uda-cred2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (again)
  - id: ut-udr-doc2
    msg: UDR
    from: as2
    to: hss
    proto: Sh
    note: (again)
  - id: ut-uda-doc2
    msg: UDA
    from: hss
    to: as2
    proto: Sh
    note: (again)
  - id: as2-replace
    do: replaces the rule and checks it
    at: as2
    fields:
      - If-Match: "Matches the `\"0\"` it read"
      - When it does not: 412, without a PUR
  - id: ut-pur
    msg: PUR
    from: as2
    to: hss
    proto: Sh
    fields:
      - Service-Indication: "`MMTEL-Services`"
      - Sh-User-Data: "`<SequenceNumber>1</SequenceNumber>` and the new document"
  - id: ut-pua
    msg: PUA
    from: hss
    to: as2
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - When it refuses: DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC
  - id: ut-200-put
    msg: 200 OK
    from: as2
    to: ue2
    proto: HTTP
    fields:
      - ETag: "`\"1\"`"
  # OIR
  - id: oir-c1-s-as1
    msg: INVITE
    from: scscf
    to: as1
    proto: SIP
    note: (C1)
    fields:
      - From: UE 1
      - P-Served-User: "`sescase=orig`"
  - id: as1-oir
    do: withholds the identity (OIR)
    at: as1
    fields:
      - Setting: "Restricted by default, by `ue xcap oir restricted`"
      - Privacy: "Adds `id`"
      - From: "Becomes `\"Anonymous\" <sip:anonymous@anonymous.invalid>`"
      - P-Asserted-Identity: Kept
  - id: oir-c2-as1-s
    msg: INVITE
    from: as1
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - From: "`\"Anonymous\" <sip:anonymous@anonymous.invalid>`"
      - Privacy: "`id`"
      - P-Asserted-Identity: "`<sip:+12025550101@…>`, kept inside the network"
  - id: oir-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: as2-oip
    do: presents the caller (OIP)
    at: as2
    fields:
      - OIP: UE 2 has it
      - "The `id` of Privacy": Kept, so that UE 2 learns the caller withheld
  - id: oir-c3-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
  - id: scscf-strip
    do: removes P-Asserted-Identity
    at: scscf
    fields:
      - Why: "The UE is outside the trust domain, and Privacy has `id`"
      - What goes: P-Asserted-Identity
  - id: oir-c3-s-p
    msg: INVITE
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: oir-c3-p-ue2
    msg: INVITE
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - From: "`\"Anonymous\" <sip:anonymous@anonymous.invalid>`"
      - Privacy: "`id`"
      - P-Asserted-Identity: None
  # CFU
  - id: cfu-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term;regstate=reg`"
  - id: as2-cfu
    do: diverts by CFU
    at: as2
    fields:
      - Rule: "`cfu`, without `rule-deactivated`"
      - Target: "`tel:+12025550103`"
      - When: As the INVITE arrives. UE 2 is not called
  - id: cfu-181
    msg: "181"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - P-Asserted-Identity: UE 2
      - History-Info: "The diversion so far; UE 3's entry has `Privacy=history`"
  - id: cfu-181-ue1
    msg: "181"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Meaning: Call Is Being Forwarded
  - id: cfu-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=302`"
      - History-Info: "Index 1 is UE 2; index 1.1 is UE 3, with `mp=1`"
  - id: scscf-cdiv
    do: sees the diversion
    at: scscf
    fields:
      - What it sees: The Request-URI of a request with the ODI changed
      - Criterion it matches: The ORIGINATING_CDIV of UE 2, the diverting user (priority 13)
  - id: cfu-c4-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C4)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;orig-cdiv;regstate=reg`"
  - id: as2-cdiv
    do: bars for the diverting user
    at: as2
    fields:
      - Served user: UE 2, the diverting user
      - Case: orig-cdiv
      - Services it runs: UE 2's OCB only
  - id: cfu-c5
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C5)
  - id: cfu-c6-p-ue3
    msg: INVITE
    from: pcscf
    to: ue3
    proto: SIP
    note: (C6)
    fields:
      - History-Info: Two entries, UE 2 and UE 3
  # CFB and CFNR
  - id: cfb-486-ue2-p
    msg: "486"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
    fields:
      - Meaning: Busy Here
  - id: cfb-486-p-s
    msg: "486"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfb-486-s-as2
    msg: "486"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
  - id: as2-cfb
    do: diverts on busy (CFB)
    at: as2
    fields:
      - Trigger: The 486 on C3
      - What it does: Acknowledges it, sends UE 1 a 181
      - Diversion: "`cause=486`"
      - UE 2's entry of History-Info: "`Reason=SIP%3Bcause%3D486...`"
  - id: cfnr-180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-180-p-s
    msg: "180"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfnr-180-s-as2
    msg: "180"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
    fields:
      - P-Asserted-Identity: UE 2
  - id: as2-timer
    do: starts the no reply timer
    at: as2
    fields:
      - Starts with: The first 180
      - Length: "UE 2's `NoReplyTimer`, 5 s in the e2e"
      - Without one: The operator's default of 20 s
  - id: cfnr-cancel-as2-s
    msg: CANCEL
    from: as2
    to: scscf
    proto: SIP
    note: (C3)
    fields:
      - Reason: "`SIP;cause=408;text=\"Request Timeout\"`"
      - When: with a 5 s timer, 5 s after the first 180
  - id: cfnr-cancel-s-p
    msg: CANCEL
    from: scscf
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-cancel-p-ue2
    msg: CANCEL
    from: pcscf
    to: ue2
    proto: SIP
    note: (C3)
    fields:
      - Reason: The same Reason; each CSCF copies it
  - id: cfnr-487-ue2-p
    msg: "487"
    from: ue2
    to: pcscf
    proto: SIP
    note: (C3)
  - id: cfnr-487-p-s
    msg: "487"
    from: pcscf
    to: scscf
    proto: SIP
    note: (C3)
  - id: cfnr-487-s-as2
    msg: "487"
    from: scscf
    to: as2
    proto: SIP
    note: (C3)
  - id: cfnr-199
    msg: "199"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - Meaning: Early Dialog Terminated
      - Reason: "`SIP;cause=408`"
  - id: cfnr-199-s-as1
    msg: "199"
    from: scscf
    to: as1
    proto: SIP
    note: (C2)
  - id: as1-keep
    do: keeps the 199 from UE 1
    at: as1
    fields:
      - UE 1's early dialog: Stays one
      - Result: UE 1 gets the 181 and UE 3's answers on it
  - id: cfnr-181
    msg: "181"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - History-Info: "UE 2, and UE 3 with `cause=408`"
  - id: cfnr-181-ue1
    msg: "181"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
  - id: cfnr-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=408`"
  # CFNL
  - id: cfnl-c2-s-i
    msg: INVITE
    from: scscf
    to: icscf
    proto: SIP
    note: (C2)
  - id: cfnl-lir
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (unregistered)
  - id: cfnl-lia
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (unregistered)
    fields:
      - Experimental-Result: DIAMETER_UNREGISTERED_SERVICE (2003)
      - Server-Capabilities: The capabilities of an S-CSCF, not a name
  - id: icscf-choose
    do: chooses the S-CSCF
    at: icscf
    fields:
      - Capabilities asked for: The Server-Capabilities of the LIA
      - Its choice: An S-CSCF that has them
  - id: cfnl-c2-i-s
    msg: INVITE
    from: icscf
    to: scscf
    proto: SIP
    note: (C2)
  - id: cfnl-sar
    msg: SAR
    from: scscf
    to: hss
    proto: Cx
    fields:
      - Server-Assignment-Type: UNREGISTERED_USER (3)
  - id: cfnl-saa
    msg: SAA
    from: hss
    to: scscf
    proto: Cx
    fields:
      - User-Data: UE 2's profile and iFCs
  - id: scscf-ifc-unreg
    do: the unregistered criterion
    at: scscf
    fields:
      - Priority 12: INVITE and TERMINATING_UNREGISTERED. A match
      - DefaultHandling: SESSION_CONTINUED
  - id: cfnl-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`<sip:+12025550102@…>;sescase=term;regstate=unreg`"
  - id: as2-cfnl
    do: diverts at once by CFNL
    at: as2
    fields:
      - Rule: "`cfnl`, with the condition `<not-registered/>`"
      - Diversion: "At once, `cause=404`"
      - Without CFNL: The INVITE goes back unchanged; the S-CSCF answers 480
  - id: cfnl-c4
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (C4)
    fields:
      - Request-URI: "`sip:+12025550103@…;user=phone;cause=404`"
  # Barring
  - id: bar-c2-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (C2)
    fields:
      - P-Served-User: "`sescase=term`"
  - id: as2-icb
    do: bars the incoming call (BAIC)
    at: as2
    fields:
      - Rule: "`baic`, switched on by UE 2 with `baic on`"
      - Order: Before any other service
      - Result: 603, after an announcement
  - id: bar-m-invite
    msg: INVITE
    from: as2
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - Request-URI: "`sip:annc@mrfc...;play=/provisioned/notice`"
      - SDP: None
  - id: bar-m-200
    msg: 200 OK
    from: mrfc
    to: as2
    proto: SIP
    note: (M)
    fields:
      - SDP: An offer, the MRFP 172.28.0.86, PCMU, sendonly
  - id: bar-183
    msg: "183"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - SDP: An answer that points to the MRFP
      - P-Early-Media: "`sendonly`"
      - Sent as: A reliable provisional response
  - id: bar-183-ue1
    msg: "183"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
  - id: bar-m-ack
    msg: ACK
    from: as2
    to: mrfc
    proto: SIP
    note: (M)
    fields:
      - SDP: An answer, the caller's media, recvonly
      - When: After the caller acknowledged the 183 with a PRACK
  - id: bar-m-bye
    msg: BYE
    from: mrfc
    to: as2
    proto: SIP
    note: (M)
    fields:
      - Meaning: The announcement has ended
  - id: bar-603
    msg: "603"
    from: as2
    to: scscf
    proto: SIP
    note: (C2)
    fields:
      - Reason: "`SIP;cause=603;text=\"Decline\"`"
  - id: bar-603-ue1
    msg: "603"
    from: pcscf
    to: ue1
    proto: SIP
    note: (C1)
    fields:
      - Reason: "`SIP;cause=603;text=\"Decline\"`"
  # CW and HOLD
  - id: cw-180-ue2-p
    msg: "180"
    from: ue2
    to: pcscf
    proto: SIP
    note: (2nd call)
    fields:
      - Alert-Info: "`<urn:alert:service:call-waiting>`"
  - id: cw-180-p-s
    msg: "180"
    from: pcscf
    to: scscf
    proto: SIP
    note: (2nd call)
  - id: cw-180-s-as2
    msg: "180"
    from: scscf
    to: as2
    proto: SIP
    note: (2nd call)
  - id: as2-cw
    do: decides how CW shows
    at: as2
    fields:
      - The operator's setting: "`calling-user-receives-notification-his-call-is-waiting` is `false`"
      - Result: The Alert-Info goes; the 180 goes on
  - id: cw-180-as2-s
    msg: "180"
    from: as2
    to: scscf
    proto: SIP
    note: (2nd call)
    fields:
      - Alert-Info: None
  - id: hold-ue2-p
    msg: INVITE
    from: ue2
    to: pcscf
    proto: SIP
    note: (hold)
    fields:
      - SDP: "`a=sendonly`"
  - id: hold-p-s
    msg: INVITE
    from: pcscf
    to: scscf
    proto: SIP
    note: (hold)
  - id: hold-s-as2
    msg: INVITE
    from: scscf
    to: as2
    proto: SIP
    note: (hold)
  - id: as2-hold
    do: passes it to the other leg
    at: as2
    fields:
      - A re-INVITE or UPDATE with SDP: Goes on to the other leg as it came
      - An UPDATE without a body: Ends at the AS; the session timer is per dialog
  - id: hold-as2-s
    msg: INVITE
    from: as2
    to: scscf
    proto: SIP
    note: (hold)
    fields:
      - SDP: "`a=sendonly`"
```

---

# Supplementary services with the MMTel AS

Supplementary services (the MMTel AS, its settings over Ut/XCAP, and the services in the capture)

---

[layout: summary]

## What this deck covers

To begin: you call a phone that forwards its calls, and another phone rings. Which node decides that? The callee's MMTel AS. The S-CSCF only hands the INVITE to the AS with an iFC; the AS reads the user's settings from the HSS and chooses the target.

- What a supplementary service is, and which node runs it.
- How the S-CSCF hands a call to the AS: iFCs, P-Served-User, ODI.
- How the AS, a B2BUA, turns one call into three dialogs.
- Where a user's settings are, and how the UE changes them over Ut with XCAP.
- What to look at in the capture for each service: identities, forwarding, barring.

> Book: [What application servers add](../../../docs/book/en/services.md), [iFC and the supplementary services of the MMTel AS](../../../docs/book/en/mmtel.md)
> Guide: [Supplementary services with the MMTel AS](../../../docs/guide/en/mmtel.md) (`docs/guide/en/mmtel.md`)
> The editions are named at the top of the guide, among them TS 24.229 V20.1.0, TS 24.604 V19.0.0, TS 24.607 V19.1.0, TS 24.611 V19.0.0, TS 24.623 V19.0.0, TS 29.364 V19.1.0 and GSMA IR.92 v24.0.

---

## Where supplementary services run

```ims-scene
id: where
nodes: [ue2, pcscf, scscf, hss, as2, mrfc]
lead: The CSCFs only route. The node that knows the user's settings and changes the call is an application server, the MMTel AS.
steps:
  - id: route
    title: The CSCFs route
    show: [ref-gm, ref-mw]
    focus: [ue2, pcscf, scscf]
    text: The UE's INVITE reaches the P-CSCF over Gm and the S-CSCF over Mw. Neither CSCF knows the user's settings.
  - id: isc
    title: The S-CSCF sends to the AS by iFC
    show: [ref-isc]
    focus: [scscf, as2]
    text: The SIP reference point between the S-CSCF and an AS is called ISC. The iFCs decide which requests go to the AS.
  - id: cx
    title: The iFCs come over Cx
    show: [ref-cx]
    focus: [scscf, hss]
    text: The SAA of registration brings the S-CSCF the profile, iFCs and all.
  - id: sh
    title: The AS reads the settings over Sh
    show: [ref-sh]
    focus: [as2, hss]
    checkpoint: true
    text: The user's settings are in the HSS. The AS reads them over Sh for each call.
  - id: ut
    title: The user changes them over Ut
    show: [ref-ut]
    focus: [ue2, as2]
    text: Ut is the HTTP interface between the UE and the AS. The `mmtel` role listens on SIP port 5060 for the AS and on HTTP port 80 for the XCAP server.
  - id: mrf
    title: The MRF plays the media
    show: [ref-mr]
    focus: [as2, mrfc]
    text: Announcements and conferences are played and mixed by the MRF. The AS asks the MRFC over Mr'.
```

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

```ims-scene
id: nodes
nodes: [ue1, ue2, ue3, pcscf, icscf, scscf, hss, as1, as2]
lead: There is one MMTel AS. The diagrams of this deck draw it twice, once for each user it serves.
steps:
  - id: ues
    title: UE 1, UE 2, UE 3
    focus: [ue1, ue2, ue3]
    text: |
      UE 1 (172.28.0.100) calls, and UE 2 (172.28.0.101) sets up its services.
      UE 3 (172.28.0.103) is where UE 2 forwards its calls.
  - id: cscfs
    title: P-CSCF, I-CSCF, S-CSCF
    focus: [pcscf, icscf, scscf]
    text: |
      The S-CSCF serves all three users, and its iFCs send every INVITE to the MMTel AS.
      The P-CSCF asserts for the network who called and who answered, and gives no UE an identity that is withheld.
  - id: as
    title: MMTel AS (172.28.0.75)
    focus: [as1, as2]
    checkpoint: true
    text: |
      The node of the `mmtel` role. The upper one runs UE 1's services, the lower one UE 2's: it is the same node.
      It listens as `mmtel.…` for SIP and as `xcap.…` for Ut.
  - id: hss
    title: HSS (172.28.0.10)
    focus: [hss]
    text: It hands out the iFCs over Cx, and each user's settings and Ut password over Sh.
```

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

```ims-scene
id: ifc
nodes: [ue1, pcscf, icscf, scscf, hss, as1, as2]
lead: The S-CSCF matches the INVITE against the iFCs and sends it to the AS of the criterion that matched. The criterion also says what happens when the AS does not answer.
steps:
  - id: arrive
    title: UE 1's INVITE reaches the S-CSCF
    only: [c1-ue-p, c1-p-s]
    active: c1-p-s
    text: UE 1 sends its INVITE along the Service-Route. The S-CSCF takes it as a call that UE 1 makes.
  - id: match
    title: The S-CSCF matches the iFCs
    show: [scscf-ifc-orig]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    text: It tries the criteria in the order of their priority. After the three of SMS, the one of priority 10 matches.
  - id: send
    title: It sends to the AS of that criterion
    show: [c1-s-as1]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: The ServerName of the criterion is where it goes. The S-CSCF adds P-Served-User, and a Route with the ODI.
  - id: default
    title: When the AS does not answer
    show: [scscf-default]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    text: An originating criterion ends the call; a terminating one goes on without the AS. Outgoing barring holds; incoming barring does not, while the AS is down.
```

| Priority | Matches | DefaultHandling |
| --- | --- | --- |
| 10 | INVITE, ORIGINATING_REGISTERED | SESSION_TERMINATED |
| 11 | INVITE, TERMINATING_REGISTERED | SESSION_CONTINUED |
| 12 | INVITE, TERMINATING_UNREGISTERED | SESSION_CONTINUED |
| 13 | INVITE, ORIGINATING_CDIV | SESSION_TERMINATED |

- Each sends to `sip:mmtel.ims.mnc001.mcc001.3gppnetwork.org:5060`.
- On the originating side the call ends when the AS does not answer, so that barring holds.
- They come after the three criteria of SMS, priorities 0 to 2. Each matches one session case only, so they never compete. The one for TERMINATING_UNREGISTERED is there for CFNL.

> Guide: ["The four filter criteria"](../../../docs/guide/en/mmtel.md#the-four-filter-criteria). TS 29.228 Annex E, B.2.3. TS 24.229 5.4.3.2 step 5, 5.4.3.3 step 4.

---

## Originating or terminating: P-Served-User

```ims-scene
id: sescase
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: One AS serves both ends of a call. The S-CSCF tells it in P-Served-User for whom, and in which case, each INVITE comes.
steps:
  - id: orig
    title: "orig: a call that the user makes"
    only: [c1-s-as1, as1-served]
    active: as1-served
    detail: [as1-served]
    zoom: [scscf, as1, as2]
    checkpoint: true
    text: An INVITE with `sescase=orig`. The AS runs UE 1's originating services.
  - id: term
    title: "term: a call to the user"
    only: [c2-s-as2, as2-served]
    active: as2-served
    detail: [as2-served]
    zoom: [scscf, as1, as2]
    checkpoint: true
    text: An INVITE with `sescase=term`. Barring comes first, then forwarding, then the identities.
  - id: unreg
    title: "regstate=unreg: a call to a user who is not registered"
    only: [cfnl-c2-s-as2]
    zoom: [scscf, as1, as2]
    text: A call to a user who has services for the unregistered state. The slide on CFNL comes back to this INVITE.
  - id: cdiv
    title: "orig-cdiv: a call that the AS diverted"
    only: [cfu-c4-s-as2, as2-cdiv]
    active: as2-cdiv
    detail: [cfu-c4-s-as2, as2-cdiv]
    zoom: [scscf, as1, as2]
    text: A diverted call gets the originating services of the diverting user. Only OCB runs.
```

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

```ims-scene
id: odi
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: The AS answers the INVITE as a UA and sends a new one back to the S-CSCF, with a Call-ID of its own. One call becomes three dialogs.
steps:
  - id: c1
    title: "C1: from UE 1 to UE 1's AS"
    only: [c1-ue-p, c1-p-s, c1-s-as1]
    text: The S-CSCF puts the AS on top of Route, and itself with the ODI below it.
  - id: c2
    title: "C2: the AS sends a new INVITE back"
    show: [c2-as1-s]
    zoom: [scscf, hss, as1]
    text: The AS takes its own Route off and sends along the rest. The INVITE comes back to the S-CSCF with the ODI.
  - id: odi
    title: The S-CSCF goes on from the ODI
    show: [scscf-odi]
    focus: [scscf]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: The S-CSCF goes on matching after the criterion that matched. The IP-SM-GW of the SMS deck sends nothing back; the MMTel AS does.
  - id: icscf
    title: The I-CSCF finds UE 2's S-CSCF
    only: [c2-s-i, lir, lia, c2-i-s]
    active: c2-i-s
    detail: [c2-s-i, lia]
    text: ENUM turns the number into a SIP URI, and the INVITE goes to the I-CSCF. The LIR belongs to dialog C2, because what the S-CSCF sends to the I-CSCF is the INVITE of the AS.
  - id: term
    title: A terminating criterion, and the AS again
    show: [c2-s-as2]
    checkpoint: true
    text: This time a terminating criterion of UE 2 matches, and the INVITE reaches UE 2's AS with `sescase=term`.
  - id: c3
    title: "C3: from UE 2's AS to UE 2"
    only: [c2-s-as2, c3-as2-s, c3-s-p, c3-p-ue2]
    active: c3-p-ue2
    detail: [c3-as2-s, c3-p-ue2]
    text: |
      Each of the three dialogs has its own Call-ID, tags and CSeq.
      A B2BUA and not a proxy: CFNR, a diversion that changes To, an OIR that makes From anonymous, the announcements and the conferences all need one.
  - id: whole
    title: One call passes the AS twice
    only: [c1-ue-p, c1-p-s, c1-s-as1, c2-as1-s, c2-s-i, c2-i-s, c2-s-as2, c3-as2-s, c3-s-p, c3-p-ue2]
    active: c3-p-ue2
    detail: []
    checkpoint: true
    text: The INVITE goes from the S-CSCF to the AS and back twice, for the originating services of UE 1 and the terminating services of UE 2.
```

```text
Route: <sip:mmtel.…:5060;lr>,<sip:scscf.…:5060;lr;odi=...>
```

```text
 UE 1 ... S-CSCF -- AS(UE 1) -- S-CSCF ... I-CSCF ... S-CSCF -- AS(UE 2) -- S-CSCF ... UE 2
 |<------ C1 ------>|<------------------ C2 ------------------->|<--------- C3 ---------->|
```

- The AS is a routeing B2BUA. It answers the INVITE as a UA and sends a new one, with a Call-ID of its own.
- The new INVITE comes back to the S-CSCF with the ODI, and the S-CSCF goes on with the next criterion. The IP-SM-GW of the SMS deck sends nothing back; the MMTel AS does.
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

The filter is `sip.P-Charging-Vector contains "icid-value=..."`, where `...` is the icid-value of the INVITE that UE 1's P-CSCF sends to the S-CSCF.

- The header stops at the P-CSCF. The UE sends none, and the P-CSCF removes it from every request and response it sends to a UE. The capture of the ims0 bridge has the legs to and from the UEs too, but the filter leaves them out.
- Wireshark's **VoIP Calls** lists each dialog as a call of its own, so one call looks like three at first sight.

> Guide: ["A B2BUA, not a proxy"](../../../docs/guide/en/mmtel.md#a-b2bua-not-a-proxy-one-call-three-dialogs). TS 24.229 5.7.5.1, 5.2.1. RFC 3326 2.

---

## Where the settings are: the simservs document

```ims-scene
id: document
nodes: [ue2, pcscf, scscf, hss, as2]
lead: A user's settings are one document, in the HSS. The AS reads it for each call and keeps no copy.
steps:
  - id: hss
    title: The HSS holds a document per user
    only: [hss-doc]
    focus: [hss]
    text: The simservs document of XCAP (TS 24.623, RFC 4825). It lives only in the HSS.
  - id: call
    title: Read for each incoming call
    only: [c2-s-as2, udr2, uda2]
    active: udr2
    detail: [udr2, uda2]
    zoom: [scscf, hss, as2]
    text: When the AS gets the incoming INVITE, it reads UE 2's document with a UDR (TS 29.364 7.2).
  - id: nocopy
    title: The AS keeps no copy
    show: [as2-nocopy]
    focus: [as2]
    zoom: [scscf, hss, as2]
    checkpoint: true
    text: So any instance of the AS can take any call.
  - id: ut
    title: The UE changes the same document over Ut
    only: [hss-doc, ut-get1]
    active: ut-get1
    text: The XCAP server is on the node of the AS too, and reads and writes the same document over Sh for each request. A UE adds no rule; it switches a service on by changing a rule that is there.
```

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

```ims-scene
id: ut
view: ladder
nodes: [ue2, as2, hss]
lead: For one change the UE sends a GET, a GET and a PUT. The AS reads the document from the HSS for each request, and shows its version as the ETag.
steps:
  - id: get1
    title: A GET without credentials
    show: [ut-get1]
    text: "`ue xcap set cfu on tel:+12025550103` first reads the whole document."
  - id: challenge
    title: The AS challenges (401)
    show: [as2-challenge, ut-401]
    active: as2-challenge
    detail: [as2-challenge, ut-401]
    checkpoint: true
    text: The AS authenticates a request before anything else. It answers without reading anything of the user, so the 401 is the same for every XUI.
  - id: get2
    title: It answers with Digest, and GETs again
    show: [ut-get2, ut-udr-cred, ut-uda-cred, ut-udr-doc, ut-uda-doc]
    active: ut-get2
    detail: [ut-get2, ut-udr-cred, ut-uda-cred]
    text: Only now does the AS read the Ut password; it checks the answer, then reads the document. Two UDRs.
  - id: etag
    title: The document comes back with its ETag
    show: [ut-200-get]
    checkpoint: true
    text: The ETag is the Sequence Number that the HSS checks on each update.
  - id: put
    title: A PUT of one rule
    show: [ut-put]
    text: The PUT carries in If-Match the ETag of the version it read.
  - id: reread
    title: The AS reads again and replaces the rule
    show: [ut-udr-cred2, ut-uda-cred2, ut-udr-doc2, ut-uda-doc2, as2-replace]
    active: as2-replace
    checkpoint: true
    text: It reads the credential and the document again. If the document changed in between, If-Match does not match, and the AS answers 412 without a PUR.
  - id: pur
    title: It writes with a PUR
    show: [ut-pur, ut-pua]
    active: ut-pur
    detail: [ut-pur, ut-pua]
    text: When another write gets in between the read and the PUR, the HSS refuses the PUR, and the AS answers 412 all the same.
  - id: done
    title: 200, with the new ETag
    show: [ut-200-put]
    text: Then the UE GETs the whole document again and prints it as settings.
```

What `ue xcap set cfu on tel:+12025550103` sends:

| Step | Request | The answer, and the Sh in between |
| --- | --- | --- |
| 1 | GET, no credentials | No UDR. 401 with two challenges, SHA-256 then MD5, the same for every user. |
| 2 | GET with `Authorization: Digest` | Two UDRs: the Ut password, then the document. 200 with `ETag: "0"`. |
| 3 | PUT of one rule, a node selector after `~~`, `If-Match: "0"` | A PUR with `<SequenceNumber>1</SequenceNumber>`. After the PUA, 200 with `ETag: "1"`. |
| 4 | GET | The document, printed as settings. |

- If the document changed in between, If-Match does not match and the AS answers 412 without a PUR. The ETag is the Sequence Number that the HSS checks on each update. When another write gets in between the read and the PUR, the HSS refuses the PUR with DIAMETER_ERROR_TRANSPARENT_DATA_OUT_OF_SYNC, and the AS answers 412 all the same.
- The filter is `http && ip.addr == 172.28.0.75`; `http.www_authenticate` finds the challenges, `http.request.method == "PUT"` the changes. The guide has the whole URI and body of the PUT.

> Guide: ["One change: CFU on"](../../../docs/guide/en/mmtel.md#one-change-cfu-on). RFC 7616 3.3. TS 24.623 5.2.3.2.1. RFC 4825 6, 7.11. TS 29.328 7.6.1, 6.1.2.1.

---

## The HTTP Digest challenge in the code

[internal/xcap/digest.go](../../../internal/xcap/digest.go)

```go
func (n nonces) challenge(h http.Header, realm string, offered []Algorithm, stale bool) {
	nonce := n.issue()
	for _, a := range offered {
		h.Add("WWW-Authenticate", sip.Challenge{
			Realm: realm, Nonce: nonce, Stale: stale, Algorithm: string(a), QOP: []string{qopAuth},
		}.String())
	}
}
```

- The first challenge offers `algorithms`, SHA-256 then MD5, in the home domain. It names no user, so the AS sends it before it reads anything of the user, and it is the same for every XUI. All the challenges share one nonce.
- After credentials that did not authenticate, `realm` and `offered` are those of the user's credential, the same unless the operator provisioned it in another realm or for MD5 alone.
- The credential is repository data in the HSS, with Service-Indication `Ut-Digest-Credential`. It holds HA1 values, not the password (the comment in `internal/xcap/credential.go`).
- WWW-Authenticate has the form of SIP Digest, so the code uses the `Challenge` of `internal/sip` as it is: it has the form of the 401 of the registration deck.

---

## A plain call through the AS, and TIP

```ims-scene
id: normal
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: With the default document the AS changes nothing. Still the INVITE passes an AS twice, and the 200 OK asserts who answered.
steps:
  - id: c1
    title: C1 reaches UE 1's AS
    only: [c1-ue-p, c1-p-s, c1-s-as1]
    active: c1-s-as1
    text: Look at P-Served-User with `sescase=orig`, and at the Route with `odi`.
  - id: udr
    title: The AS reads UE 1's settings (UDR)
    show: [udr1, uda1]
    active: udr1
    detail: [udr1, uda1]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: The document is the default one, so nothing about the call changes.
  - id: c2
    title: The AS sends the INVITE of C2
    only: [c2-as1-s]
    text: The INVITE of the AS has the same icid-value as C1.
  - id: lir
    title: The I-CSCF finds UE 2's S-CSCF with an LIR
    only: [c2-s-i, lir, lia, c2-i-s]
    active: lir
    text: The S-CSCF does ENUM and sends to the I-CSCF. This LIR belongs to dialog C2.
  - id: term
    title: The AS for the second time
    only: [c2-s-as2, udr2, uda2]
    active: c2-s-as2
    checkpoint: true
    text: This time it comes with `sescase=term`, and the AS reads UE 2's settings.
  - id: c3
    title: C3 reaches UE 2
    only: [c3-as2-s, c3-s-p, c3-p-ue2]
    active: c3-as2-s
    text: The 183, the PRACK and the 180 then go dialog by dialog, as in the call session.
  - id: answer
    title: The P-CSCF asserts who answered
    only: [ok-ue2-p, pcscf-tip]
    active: pcscf-tip
    text: UE 2's P-CSCF puts the P-Called-Party-ID of the INVITE into the P-Asserted-Identity of the 200 OK.
  - id: tip
    title: UE 1 learns who answered (TIP)
    hide: [ok-ue2-p]
    show: [ok-p-ue1]
    text: |
      The 200 OK goes back to UE 1 through both ASs; the diagram leaves out the hops in between.
      Every user has TIP by default.
```

| Look at | What to see |
| --- | --- |
| C1 INVITE from the S-CSCF to the AS | P-Served-User with `sescase=orig`, and a Route with `odi`. |
| The UDR of the AS | Data-Reference 0, Service-Indication `MMTEL-Services`. |
| The C2 INVITE of the AS | The same icid-value. ENUM and the LIR follow. |
| The second AS | Gets it with `sescase=term`, and sends C3 back. |
| 200 OK | UE 2 in P-Asserted-Identity: TIP. |

- With the default document the AS changes nothing, but every INVITE passes an AS twice. Every user has TIP by default: the P-CSCF puts P-Called-Party-ID into P-Asserted-Identity.
- The frame numbers in the guide come from one run of the script.

> Guide: ["A plain call through the AS"](../../../docs/guide/en/mmtel.md#a-plain-call-through-the-as). TS 24.229 5.4.3.2 step 5, 5.7.5.2.1, 5.2.6.4.4 step 1. TS 29.364 7.2.0. TS 24.608 4.5.2.4.

---

## OIR: the caller withholds its identity

```ims-scene
id: oir
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: Inside the network the identity stays asserted. It is withheld only from a UE, outside the trust domain.
steps:
  - id: restrict
    title: UE 1's AS withholds the identity
    only: [oir-c1-s-as1, as1-oir]
    active: as1-oir
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: "UE 1 set its default to restricted before it called. The AS makes From anonymous and adds `Privacy: id`."
  - id: c2
    title: P-Asserted-Identity stays inside the network
    show: [oir-c2-as1-s]
    zoom: [scscf, hss, as1]
    checkpoint: true
    text: The network still asserts the identity. The diagram leaves out the hops through the I-CSCF.
  - id: oip
    title: UE 2's AS runs OIP
    only: [oir-c2-s-as2, as2-oip]
    active: as2-oip
    text: UE 2 has OIP, so the AS keeps the `id`.
  - id: strip
    title: The S-CSCF removes P-Asserted-Identity
    show: [oir-c3-as2-s, scscf-strip]
    active: scscf-strip
    focus: [scscf]
    checkpoint: true
    text: The UE is outside the trust domain, and Privacy has `id`.
  - id: deliver
    title: The INVITE that reaches UE 2
    show: [oir-c3-s-p, oir-c3-p-ue2]
    active: oir-c3-p-ue2
    text: "It has the anonymous From and `Privacy: id`, and nothing that points to UE 1."
```

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

```ims-scene
id: cfu
view: ladder
nodes: [ue1, ue3, pcscf, scscf, as2]
lead: With CFU, UE 2's AS diverts the call as the INVITE arrives. UE 2's phone never rings.
steps:
  - id: arrive
    title: The call reaches UE 2's AS
    show: [cfu-c2-s-as2]
    text: UE 2 set CFU to UE 3 over Ut and stays registered. UE 1 calls UE 2.
  - id: decide
    title: The AS decides to divert by CFU
    show: [as2-cfu]
    focus: [as2]
    text: CFU comes right after barring. The INVITE never reaches UE 2.
  - id: notify
    title: A 181 tells the caller
    show: [cfu-181, cfu-181-ue1]
    active: cfu-181
    detail: [cfu-181, cfu-181-ue1]
    checkpoint: true
    text: The diagram leaves out the hops between UE 2's AS and UE 1. UE 3's entry has `Privacy=history`, because UE 2's AS does not know whether UE 3 lets its identity be shown.
  - id: c4
    title: The diverted INVITE (C4)
    show: [cfu-c4]
    active: cfu-c4
    text: The Request-URI carries the cause of the diversion (RFC 4458), and History-Info carries its history.
  - id: cdiv
    title: The S-CSCF sees the diversion
    show: [scscf-cdiv]
    focus: [scscf]
    text: The Request-URI of a request with the ODI changed, so it matches the ORIGINATING_CDIV criterion of UE 2, the diverting user.
  - id: ocb
    title: Barring for the diverting user
    show: [cfu-c4-s-as2, as2-cdiv]
    active: as2-cdiv
    detail: [cfu-c4-s-as2, as2-cdiv]
    checkpoint: true
    text: The INVITE comes back to the AS with `orig-cdiv`. UE 2's outgoing barring applies here, to the call to UE 3.
  - id: ue3
    title: The call reaches UE 3
    show: [cfu-c5, cfu-c6-p-ue3]
    active: cfu-c6-p-ue3
    text: The S-CSCF turns the number into UE 3's SIP URI with ENUM, and the call reaches UE 3 through the I-CSCF and UE 3's AS. The diagram leaves out the hops in between.
```

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

## CFB: the callee is busy

```ims-scene
id: cfb
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: With CFB the AS calls UE 2 first. When UE 2 answers that it is busy, the AS diverts.
steps:
  - id: busy
    title: UE 2 is busy (486)
    show: [cfb-486-ue2-p, cfb-486-p-s, cfb-486-s-as2]
    active: cfb-486-ue2-p
    checkpoint: true
    text: UE 2 answers the INVITE of dialog C3 with 486 Busy Here.
  - id: cfb
    title: The AS diverts on busy
    show: [as2-cfb]
    focus: [as2]
    text: The AS acknowledges the 486, sends UE 1 a 181 and diverts with `cause=486`. From the 181 on it goes as with CFU.
```

| Case | What to see |
| --- | --- |
| CFB | UE 2 answers C3 with 486. The AS acknowledges it, sends the 181, diverts with `cause=486`. |

> Guide: ["CFB"](../../../docs/guide/en/mmtel.md#cfb-the-callee-is-busy). TS 24.604 4.5.2.6.2.2 b, 4.5.2.6.3.

---

## CFNR: nobody answers

```ims-scene
id: cfnr
view: ladder
nodes: [ue1, ue2, pcscf, scscf, as1, as2]
lead: With CFNR the AS times the ringing of UE 2. When nobody answers in time, it stops the ringing and diverts.
steps:
  - id: ringing
    title: UE 2 rings (180)
    show: [cfnr-180-ue2-p, cfnr-180-p-s, cfnr-180-s-as2]
    active: cfnr-180-s-as2
    text: UE 2 has CFNR to UE 3, with a no reply timer of 5 seconds. It rings on and does not answer.
  - id: timer
    title: The timer starts with the first 180
    show: [as2-timer]
    focus: [as2]
    checkpoint: true
    text: A 180 from another branch of a fork does not start it again.
  - id: cancel
    title: The timer runs out, and a CANCEL
    show: [cfnr-cancel-as2-s, cfnr-cancel-s-p, cfnr-cancel-p-ue2]
    active: cfnr-cancel-as2-s
    detail: [cfnr-cancel-as2-s, cfnr-cancel-p-ue2]
    text: Each CSCF on the way copies the Reason into its own CANCEL, so UE 2 learns the cause too.
  - id: terminated
    title: UE 2 answers 487
    show: [cfnr-487-ue2-p, cfnr-487-p-s, cfnr-487-s-as2]
    active: cfnr-487-ue2-p
    checkpoint: true
    text: The leg to UE 2 ends here.
  - id: early
    title: A 199 ends UE 2's early dialog
    show: [cfnr-199, cfnr-199-s-as1, as1-keep]
    active: as1-keep
    detail: [cfnr-199, as1-keep]
    text: The 199 goes on C2 as far as UE 1's AS. The diagram leaves out the I-CSCF.
  - id: divert
    title: The 181, and a diversion with cause=408
    show: [cfnr-181, cfnr-181-ue1, cfnr-c4]
    active: cfnr-c4
    detail: [cfnr-181, cfnr-c4]
    text: From the 181 on it goes as with CFU, and the diagram leaves out the same hops. The CANCEL comes as long after the first 180 as the timer runs.
```

| Case | What to see |
| --- | --- |
| CFNR timer | Starts with the first 180. Its length is `NoReplyTimer`, 5 s in the e2e. |
| CFNR CANCEL | `Reason: SIP;cause=408;text="Request Timeout"`, copied by each CSCF. |
| 199 Early Dialog Terminated | Ends UE 2's early dialog on C2. It does not reach UE 1. |
| Then | The 181, and a diversion with `cause=408`. |

The filters are `sip.Method == "CANCEL" && sip.Reason` and `sip.Status-Code == 199`.

- Without a NoReplyTimer, the operator's default of 20 seconds applies. Either way, the time from the 180 to the CANCEL is the length of the timer: 10 s with `noreply 10`, 5 s with `noreply 5`.
- UE 1's AS keeps its one early dialog with UE 1, so UE 1 gets the 181 and UE 3's responses on the same early dialog.

> Guide: ["CFNR"](../../../docs/guide/en/mmtel.md#cfnr-nobody-answers). TS 24.604 4.5.2.6.3 item 2, 4.8.1. RFC 3326 2. RFC 6228.

---

## CFNL: the callee is not registered

```ims-scene
id: cfnl
nodes: [ue1, ue2, pcscf, icscf, scscf, hss, as1, as2]
lead: A call to UE 2 reaches the AS even while UE 2 is not registered, when it has services for that state. The AS diverts at once, without calling.
steps:
  - id: lir
    title: The LIR of the I-CSCF gets 2003
    only: [cfnl-c2-s-i, cfnl-lir, cfnl-lia]
    active: cfnl-lia
    zoom: [icscf, hss, scscf]
    text: UE 2 has CFNL to UE 3 and is not registered. The HSS answers with capabilities instead of the name of an S-CSCF.
  - id: choose
    title: The I-CSCF chooses an S-CSCF
    show: [icscf-choose, cfnl-c2-i-s]
    active: icscf-choose
    zoom: [icscf, hss, scscf]
    text: As after the first UAA of registration, it chooses an S-CSCF by the capabilities and sends the INVITE there.
  - id: sar
    title: The S-CSCF gets the profile (SAR)
    show: [cfnl-sar, cfnl-saa]
    active: cfnl-sar
    detail: [cfnl-sar, cfnl-saa]
    zoom: [icscf, hss, scscf]
    checkpoint: true
    text: A SAR of UNREGISTERED_USER. The SAA has UE 2's iFCs.
  - id: match
    title: The criterion for an unregistered callee matches
    only: [cfnl-c2-i-s, scscf-ifc-unreg]
    active: scscf-ifc-unreg
    focus: [scscf]
    text: It matches the criterion of TERMINATING_UNREGISTERED and sends to the AS.
  - id: to-as
    title: An INVITE with regstate=unreg reaches the AS
    show: [cfnl-c2-s-as2]
    checkpoint: true
    text: P-Served-User says that the call is to a user who is not registered.
  - id: divert
    title: The AS diverts at once
    only: [as2-cfnl, cfnl-c4]
    active: as2-cfnl
    detail: [as2-cfnl, cfnl-c4]
    text: The rule of CFNL has the condition `<not-registered/>`, so the AS diverts without calling UE 2.
```

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

```ims-scene
id: barring
view: ladder
nodes: [ue1, pcscf, scscf, as2, mrfc]
lead: Incoming barring is the first rule that UE 2's AS checks. Before it rejects the call, the caller hears an announcement.
steps:
  - id: arrive
    title: The call reaches UE 2's AS
    show: [bar-c2-s-as2]
    text: UE 2 has `baic on`. Up to here it goes as a plain call through the AS.
  - id: bar
    title: The AS bars the call
    show: [as2-icb]
    checkpoint: true
    text: It checks this before any other service. The call never reaches UE 2.
  - id: mrfc
    title: It asks the MRFC for the announcement
    show: [bar-m-invite, bar-m-200]
    active: bar-m-invite
    detail: [bar-m-invite, bar-m-200]
    text: An INVITE without SDP, whose Request-URI names what to play. The MRFC answers with SDP that points to the MRFP.
  - id: early
    title: A 183 points the caller's media to the MRFP
    show: [bar-183, bar-183-ue1]
    active: bar-183
    checkpoint: true
    text: The 183 is a reliable provisional response. The diagram leaves out the hops between UE 2's AS and the P-CSCF.
  - id: play
    title: The announcement plays
    show: [bar-m-ack]
    text: When the caller acknowledges the 183 with a PRACK, the AS sends the MRFC an ACK, and the MRFP starts the announcement.
  - id: decline
    title: The announcement ends, and a 603
    show: [bar-m-bye, bar-603, bar-603-ue1]
    active: bar-603
    text: The MRFC sends BYE, and the AS answers 603 with a Reason. With ACR it is 433. With BAOC, UE 1's AS answers 603, and there is no C2.
```

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

```ims-scene
id: cw-hold
view: ladder
nodes: [ue2, pcscf, scscf, as2]
lead: Call waiting is done by the UE; the AS only decides what the caller learns of it. The AS also passes the re-INVITE of a hold to the other leg as it came.
steps:
  - id: waiting
    title: UE 2 tells that a second call waits
    show: [cw-180-ue2-p, cw-180-p-s, cw-180-s-as2]
    active: cw-180-ue2-p
    checkpoint: true
    text: IR.92 asks for call waiting done by the UE, not the network (2.3.4). UE 2, in a call, answers the second call with a 180 that carries the call-waiting Alert-Info.
  - id: cw
    title: The AS takes the Alert-Info out
    show: [as2-cw, cw-180-as2-s]
    active: as2-cw
    detail: [as2-cw, cw-180-as2-s]
    checkpoint: true
    text: The AS passes the 180 on as it is, or takes the Alert-Info out by the operator's setting. The document of this network says that the caller does not learn of it.
  - id: hold
    title: UE 2 holds the first call
    show: [hold-ue2-p, hold-p-s, hold-s-as2]
    active: hold-ue2-p
    text: When `ue serve` gets `POST /answer`, it holds the first call and then takes the waiting one. The hold is a re-INVITE with `a=sendonly`.
  - id: pass
    title: The AS passes the re-INVITE on
    show: [as2-hold, hold-as2-s]
    active: as2-hold
    detail: [as2-hold, hold-as2-s]
    text: The AS passes the re-INVITE of the hold to the other leg as it came.
```

- IR.92 asks for call waiting done by the UE, not the network (2.3.4).
  - The callee's UE sends a 180 with `Alert-Info: <urn:alert:service:call-waiting>`.
  - The AS passes it on, or takes the Alert-Info out by the operator's setting.
- HOLD is a re-INVITE with `a=sendonly`. The AS passes it on to the other leg as it came.
- `ue serve` takes a second call while it has one: it rings with that Alert-Info, and `POST /answer` holds the first call and takes the waiting one. `scripts/e2e-mrf.sh` shows CW in the capture, and the in-process tests `TestCommunicationWaiting`, `TestHoldThroughTheIMS` and `TestServeTwoCalls` check it.

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

- Each item has a comment that starts with `SIMPLIFIED:` in the code. The items of the CSCFs and the HSS are in `internal/scscf`, `internal/icscf` and `internal/hss`.
- The nonce count is the price of running several instances: keeping the counts would need a Store that every instance shares.

> Guide: ["What this project simplifies"](../../../docs/guide/en/mmtel.md#what-this-project-simplifies). The table there gives the clause and the reason of each item.

---

## Where the code is

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

- The decisions are in `invokeApplicationServers` of `internal/scscf/services.go` (the iFCs in turn), `terminating` of `internal/mmtel/sessioncase.go` (the order of the services) and `change` of `internal/xcap/handler.go` (If-Match and 412).
- `internal/mmtel/network_test.go` runs every service through the whole IMS in one process. The cause values are constants in `internal/mmtel/diversion.go`; CFU gives the `cause=302` of the capture.

> Book: [iFC and the supplementary services of the MMTel AS](../../../docs/book/en/mmtel.md). Guide: ["Where the code is"](../../../docs/guide/en/mmtel.md#where-the-code-is)

---

## Run it yourself: calls and Ut

What follows is for readers who want to run the setup. Reading does not need it.

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

## Run it yourself: the capture and the Lab

```text
(sip && !(sip.CSeq.method in {"REGISTER", "SUBSCRIBE", "NOTIFY", "MESSAGE"})) || (http && ip.addr == 172.28.0.75) || diameter.cmd.code in {301, 302, 306, 307}
```

- It keeps the calls, Ut, the Sh reads and updates of the AS, and the LIRs and SARs. **Telephony > VoIP Calls** lists each dialog as a call of its own. The e2e script leaves every service in one capture.
- `docker compose --profile phones up -d --build` runs the Lab. In **Supplementary services (Ut/XCAP)** on the card of `phone2`, every service starts as `off`. **On** for **CFU (unconditional)** shows `CFU (unconditional): on to …`, and the ETag goes up by one.
- Do not run the `phones` and `ue` profiles at once: a subscriber would have two UEs.

> Guide: ["Open the capture"](../../../docs/guide/en/mmtel.md#open-the-capture), ["Drive the UE simulators" in Watch the IMS in the Lab](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## Run it yourself: tests and clean-up

```sh
go test -run 'TestCommunicationWaiting|TestHoldThroughTheIMS' -v ./internal/mmtel
go test -run TestServeTwoCalls -v ./internal/ue
grep -rn "SIMPLIFIED:" internal/mmtel internal/xcap internal/simservs internal/config/xcap.go cmd/ue/xcap.go
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```

- The first two lines are the tests that check CW and HOLD in an IMS in one process. The third lists the comments of the simplified places.
- The last line stops the `ue` and the `phones` profiles and deletes the images as well. The guide's "Clean up" stops every profile and keeps the images.

> The guides are listed in [docs/guide/](../../../docs/guide/).
