---
title: A short message through the IMS
footer: ims-practice study session 6. SMS
lang: en
format: visual
---

```ims-model
nodes:
  - {id: ue1, label: UE 1, role: the sender, at: [0, 0]}
  - {id: ue2, label: UE 2, role: the recipient, at: [0, 1]}
  - {id: pcscf, label: P-CSCF, role: the first hop, at: [1, 1]}
  - {id: icscf, label: I-CSCF, role: entry of the home network, at: [2, 0]}
  - {id: scscf, label: S-CSCF, role: evaluates the iFCs, at: [2, 1]}
  - {id: hss, label: HSS, role: subscriber data, at: [3, 0]}
  - {id: ipsmgw, label: IP-SM-GW, role: the AS for SMS, at: [3, 1]}
  - {id: sc, label: SC, role: holds and delivers, at: [4, 1]}
events:
  - id: r1-ue2-p
    msg: REGISTER
    from: ue2
    to: pcscf
    proto: SIP
    note: (2nd)
    fields:
      - Contact: "The feature tag `+g.3gpp.smsip`: this contact takes SMS over IP"
  - id: r1-p-i
    msg: REGISTER
    from: pcscf
    to: icscf
    proto: SIP
    note: (2nd)
  - id: r1-i-s
    msg: REGISTER
    from: icscf
    to: scscf
    proto: SIP
    note: (2nd)
  - id: r1ok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: r1ok-i-p
    msg: 200 OK
    from: icscf
    to: pcscf
    proto: SIP
  - id: r1ok-p-ue2
    msg: 200 OK
    from: pcscf
    to: ue2
    proto: SIP
  - id: scscf-ifc0
    do: matches the iFC of priority 0
    at: scscf
    fields:
      - Condition: Every REGISTER
      - Goes to: The IP-SM-GW, with the MSISDN as ServiceInfo
      - DefaultHandling: "SESSION_CONTINUED: the registration stays if the IP-SM-GW does not answer"
  - id: tp-register
    msg: REGISTER
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (third-party)
    fields:
      - Request-URI: "`sip:ipsmgw...:5060`, the ServerName of the iFC"
      - From, Contact: The S-CSCF
      - To: UE 2's public identity
      - Body: "`<service-info>` holds the MSISDN `12025550102`"
  - id: tp-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (third-party)
    fields:
      - Contact: Lists the binding, `expires=3600`
  - id: isub-ip-i
    msg: SUBSCRIBE
    from: ipsmgw
    to: icscf
    proto: SIP
    fields:
      - Event: "`reg`"
      - Expires: "3761: the 3600 s of the registration plus 161 s"
      - P-Asserted-Identity: The URI of the IP-SM-GW
  - id: isub-i-s
    msg: SUBSCRIBE
    from: icscf
    to: scscf
    proto: SIP
  - id: lir-r
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia-r
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2's S-CSCF
  - id: isubok-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
  - id: isubok-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
  - id: inotify
    msg: NOTIFY
    from: scscf
    to: ipsmgw
    proto: SIP
    fields:
      - Subscription-State: "`active`"
      - Body: reginfo, with `<unknown-param name="+g.3gpp.smsip">` in the contact
  - id: inotify-ok
    msg: 200 OK
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (NOTIFY)
  - id: ipsmgw-check
    do: looks for a contact that takes SMS
    at: ipsmgw
    fields:
      - Looks for: An active contact with `+g.3gpp.smsip`
      - If there is one: Registers itself in the HSS
      - If there is none: Sends no PUR
  - id: udr
    msg: UDR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - Data-Reference: IMSI (32)
  - id: uda
    msg: UDA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - User-Data: "UE 2's IMSI: SGd names a recipient by IMSI"
  - id: pur
    msg: PUR
    from: ipsmgw
    to: hss
    proto: Sh
    fields:
      - User-Identity: MSISDN `12025550102`
      - Data-Reference: SMSRegistrationInfo (24)
      - Sh-User-Data: "`12025550199` in `<IP-SM-GW-Number>`"
  - id: pua
    msg: PUA
    from: hss
    to: ipsmgw
    proto: Sh
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: m1-ue1-p
    msg: MESSAGE
    from: ue1
    to: pcscf
    proto: SIP
    note: (1st)
    fields:
      - Request-URI, To: "`tel:+12025550100`: the SC, not the recipient"
      - Content-Type: "`application/vnd.3gpp.sms`, a body of 47 octets"
      - Route: The P-CSCF and UE 1's Service-Route
  - id: m1-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (1st)
    fields:
      - P-Asserted-Identity: Put in by the P-CSCF
      - Record-Route: "None: no dialog follows"
  - id: m1-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (1st)
    fields:
      - Route: The IP-SM-GW on top, then the S-CSCF with `odi`
      - P-Asserted-Identity: UE 1's SIP URI and tel URI
  - id: scscf-ifc1
    do: matches the iFC of priority 1
    at: scscf
    fields:
      - Condition: A MESSAGE with an SMS body from a registered user
      - P-Asserted-Identity: "Adds UE 1's tel URI: the IP-SM-GW gives the SC a number"
      - P-Served-User: "`sescase=orig;regstate=reg`"
  - id: m1a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (1st)
    fields:
      - Meaning: The MESSAGE arrived. Nothing is delivered yet
  - id: m1a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (1st)
  - id: m1a-p-ue1
    msg: "202"
    from: pcscf
    to: ue1
    proto: SIP
    note: (1st)
  - id: ipsmgw-rp
    do: checks the RP-DATA
    at: ipsmgw
    fields:
      - Decoding: Does it decode?
      - RP-DA: Is it the SC's number, `12025550100`?
      - P-Asserted-Identity: Is there a tel URI?
      - If not: The submit report is an RP-ERROR
  - id: ofr
    msg: OFR
    from: ipsmgw
    to: sc
    proto: SGd
    fields:
      - SC-Address: "`12025550100`, the number the UE wrote in RP-DA"
      - User-Identifier: The sender's MSISDN, `12025550101`
      - SM-RP-UI: UE 1's SMS-SUBMIT as it is
      - Destination-Realm: "`ims.mnc001.mcc001.3gppnetwork.org`, with no Destination-Host"
  - id: srr1
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (on submit)
    fields:
      - MSISDN: The recipient's, `12025550102`
      - Asks: Is there a user with this number?
  - id: sra1
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (on submit)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - IP-SM-GW-Name: The IP-SM-GW of UE 2, from the registration of R5
      - User-Name: UE 2's IMSI
  - id: ofa
    msg: OFA
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - SM-RP-UI: SMS-SUBMIT-REPORT, with the SC's time stamp TP-SCTS
  - id: m2-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (2nd)
    fields:
      - Request-URI: UE 1's SIP URI, from P-Asserted-Identity of step 1
      - Accept-Contact: "`*;+g.3gpp.smsip;require;explicit`"
      - Request-Disposition: "`fork`"
      - In-Reply-To: The Call-ID of the MESSAGE of step 1
      - Body: RP-ACK with an SMS-SUBMIT-REPORT
  - id: m2-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (2nd)
  - id: m2-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (2nd)
  - id: m2-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (2nd)
  - id: lir-m2
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 1)
  - id: lia-m2
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 1)
    fields:
      - Server-Name: UE 1's S-CSCF
  - id: ue1-report
    do: matches the submit report
    at: ue1
    fields:
      - In-Reply-To: The Call-ID of its own short message
      - Records: "The SC accepted it: `\"msg\":\"submit report\"` in the log"
  - id: m2a-ue1-p
    msg: 200 OK
    from: ue1
    to: pcscf
    proto: SIP
    note: (2nd)
  - id: m2a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (2nd)
  - id: m2a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (2nd)
  - id: m2a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (2nd)
  - id: sc-make
    do: makes the SMS-DELIVER
    at: sc
    fields:
      - TP-OA: The sender's number, `12025550101`
      - TP-SCTS: When the SC took the message
      - TP-SRI: "Set: UE 1 asked for a status report"
      - Text: Unchanged
  - id: srr2
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (to deliver)
    fields:
      - MSISDN: The recipient's, `12025550102`
      - Asked by: The SMS-GMSC that delivers. The first came from the SMS-IWMSC that took it
  - id: sra2
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (to deliver)
    fields:
      - IP-SM-GW-Name: UE 2's IP-SM-GW again
      - User-Name: UE 2's IMSI
  - id: tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Destination-Host: The IP-SM-GW of the SRA. A TFR always names its node
      - User-Name: UE 2's IMSI, `001010000000002`
      - SC-Address: "`12025550100`, which becomes the RP-OA"
      - SM-RP-UI: SMS-DELIVER
  - id: ipsmgw-find
    do: finds the user by IMSI
    at: ipsmgw
    fields:
      - User-Name: Matched against the IMSI read in the UDA of R5
      - Recipient: "`tel:+12025550102`, the tel URI of the IMSI"
      - The delivery: Put in the shared `Store` before the MESSAGE goes
  - id: m3-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (3rd)
    fields:
      - Request-URI: "`tel:+12025550102`"
      - Request-Disposition: "`no-fork`"
      - P-Asserted-Identity: The IP-SM-GW, where the delivery report goes
      - Body: RP-DATA from the network with an SMS-DELIVER
  - id: m3-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (3rd)
  - id: m3-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (3rd)
  - id: m3-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (3rd)
  - id: lir-m3
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 2)
  - id: lia-m3
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 2)
    fields:
      - Server-Name: UE 2's S-CSCF
  - id: scscf-mt
    do: checks the terminating iFC and the contacts
    at: scscf
    fields:
      - Priority 2: "No match: P-Asserted-Identity is the IP-SM-GW"
      - Accept-Contact: UE 2's contact has `+g.3gpp.smsip` and stays
      - Request-Disposition: "`no-fork`: one contact only"
  - id: m3a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (3rd)
    fields:
      - Meaning: Only that the MESSAGE arrived
  - id: m3a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (3rd)
  - id: m3a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (3rd)
  - id: m3a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (3rd)
  - id: m4-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (4th)
    fields:
      - Request-URI: The IP-SM-GW, from P-Asserted-Identity of step 8
      - In-Reply-To: The Call-ID of the MESSAGE of step 8
      - Body: RP-ACK from the UE with an SMS-DELIVER-REPORT
  - id: m4-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (4th)
  - id: m4-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (4th)
  - id: scscf-dr
    do: matches UE 2's iFC of priority 1
    at: scscf
    fields:
      - Condition: A MESSAGE with an SMS body that UE 2 sends
      - Route: "As in step 3: the IP-SM-GW and `odi`"
  - id: m4a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (4th)
  - id: m4a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (4th)
  - id: m4a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (4th)
  - id: ipsmgw-dr
    do: finds the delivery and ends it
    at: ipsmgw
    fields:
      - In-Reply-To: Finds the delivery in the `Store` by the Call-ID of step 8
      - Which node: "Any IP-SM-GW: the state of the delivery is in the shared `Store`"
  - id: tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
      - Meaning: The short message arrived
  - id: sc-sr
    do: makes the status report
    at: sc
    fields:
      - To: The sender's number, `+12025550101`
      - TP-MR: The value of the SMS-SUBMIT
      - TP-ST: "0x00: the recipient received the message"
  - id: srr3
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (status report)
    fields:
      - MSISDN: The sender's, `12025550101`
  - id: sra3
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (status report)
    fields:
      - IP-SM-GW-Name: The IP-SM-GW of UE 1
      - User-Name: UE 1's IMSI
  - id: tfr3
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (status report)
    fields:
      - User-Name: UE 1's IMSI, `001010000000001`
      - SM-RP-UI: SMS-STATUS-REPORT
  - id: m5-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (5th)
    fields:
      - Request-URI: "`tel:+12025550101`"
      - Request-Disposition: "`no-fork`"
      - Body: RP-DATA with an SMS-STATUS-REPORT
      - TP-ST: 0x00
  - id: m5-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (5th)
  - id: m5-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (5th)
  - id: m5-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (5th)
  - id: lir-m5
    msg: LIR
    from: icscf
    to: hss
    proto: Cx
    note: (UE 1)
  - id: lia-m5
    msg: LIA
    from: hss
    to: icscf
    proto: Cx
    note: (UE 1)
  - id: m5a-ue1-p
    msg: 200 OK
    from: ue1
    to: pcscf
    proto: SIP
    note: (5th)
  - id: m5a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (5th)
  - id: m5a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (5th)
  - id: m5a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (5th)
  - id: m6-ue1-p
    msg: MESSAGE
    from: ue1
    to: pcscf
    proto: SIP
    note: (6th)
    fields:
      - In-Reply-To: The Call-ID of the 5th MESSAGE
      - Body: RP-ACK from the UE
  - id: m6-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (6th)
  - id: m6-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (6th)
  - id: m6a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (6th)
  - id: m6a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (6th)
  - id: m6a-p-ue1
    msg: "202"
    from: pcscf
    to: ue1
    proto: SIP
    note: (6th)
  - id: tfa3
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (status report)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: a1-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (A1)
    fields:
      - User-Name: UE 2's IMSI
  - id: a1-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (A1)
  - id: a1-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (A1)
  - id: a1-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (A1)
  - id: a1-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (A1)
  - id: a2-ue2-p
    msg: "480"
    from: ue2
    to: pcscf
    proto: SIP
    note: (A2)
    fields:
      - Meaning: "Temporarily Unavailable: reached, but cannot take it now"
      - delivery report: None
  - id: a2-p-s
    msg: "480"
    from: pcscf
    to: scscf
    proto: SIP
    note: (A2)
  - id: a2-s-i
    msg: "480"
    from: scscf
    to: icscf
    proto: SIP
    note: (A2)
  - id: a2-i-ip
    msg: "480"
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (A2)
  - id: a3-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (A3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_ABSENT_USER (5550)
  - id: ipsmgw-unri
    do: sets UNRI
    at: ipsmgw
    fields:
      - Input: UE 2's 480
      - UNRI: "Set for UE 2: only this node knows that the IMS failed"
  - id: a4-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (A4)
    fields:
      - SM-Delivery-Cause: ABSENT_USER
  - id: hss-absent
    do: puts the SC in the MWD
    at: hss
    fields:
      - MWD: The SC's address goes in
      - UNRI: "Set: no serving node in an SRA while it is"
  - id: a4-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (A4)
  - id: a5-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (A5)
    fields:
      - Content: Anything. Here, a short message of UE 2's own
  - id: a5-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (A5)
  - id: a5-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (A5)
  - id: a5a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (A5)
  - id: a5a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (A5)
  - id: a5a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (A5)
  - id: ipsmgw-present
    do: learns that UE 2 is back
    at: ipsmgw
    fields:
      - Input: A request of UE 2 arrived
      - UNRI: Clears it and tells the HSS
  - id: a6-rfsm
    msg: Ready-For-SM
    from: ipsmgw
    to: hss
    proto: S6c
    note: (A6)
    fields:
      - Command code: 16777214, this project's own
      - Alert-Reason: UE_PRESENT
      - User-Identifier: UE 2's MSISDN
      - IP-SM-GW-Number: The IP-SM-GW that tells
  - id: hss-present
    do: clears UNRI
    at: hss
    fields:
      - Checks: Is it the number of UE 2's IP-SM-GW?
      - UNRI: Cleared
      - Alerts: Every SC in the MWD
  - id: a6-ans
    msg: Ready-For-SM
    from: hss
    to: ipsmgw
    proto: S6c
    note: (answer)
  - id: a7-alr
    msg: ALR
    from: hss
    to: sc
    proto: S6c
    note: (A7)
    fields:
      - Destination-Host: The SC's name, found in `service_centres`
  - id: a7-ala
    msg: ALA
    from: sc
    to: hss
    proto: S6c
    note: (A7)
    fields:
      - Result: The address of the SC that answered leaves the MWD
  - id: sc-due
    do: delivers what it holds at once
    at: sc
    fields:
      - Trigger: The ALR
      - Retry: Does not wait for the `retry_delays` of an hour
  - id: a8-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (A8)
  - id: a8-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (A8)
    fields:
      - IP-SM-GW-Name: "There again: UNRI is clear"
      - MWD-Status: "Mostly 0: the SC is in the MWD, and no flag is set"
  - id: a8-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (A8)
    fields:
      - SM-RP-UI: The SMS-DELIVER that the SC held
  - id: a8-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (A8)
  - id: a8-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (A8)
  - id: a8-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (A8)
  - id: a8-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (A8)
  - id: a8a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (A8)
  - id: a8a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (A8)
  - id: a8a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (A8)
  - id: a8a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (A8)
  - id: a9-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (A9)
    fields:
      - Body: "RP-ACK: the delivery report"
  - id: a9-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (A9)
  - id: a9-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (A9)
  - id: a9a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (A9)
  - id: a9a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (A9)
  - id: a9a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (A9)
  - id: a9-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (A9)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: b1-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (B1)
  - id: b1-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (B1)
  - id: b1-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (B1)
  - id: b1-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (B1)
  - id: b1-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (B1)
  - id: b1a-ue2-p
    msg: 200 OK
    from: ue2
    to: pcscf
    proto: SIP
    note: (B1)
    fields:
      - Meaning: The short message got this far
  - id: b1a-p-s
    msg: 200 OK
    from: pcscf
    to: scscf
    proto: SIP
    note: (B1)
  - id: b1a-s-i
    msg: 200 OK
    from: scscf
    to: icscf
    proto: SIP
    note: (B1)
  - id: b1a-i-ip
    msg: 200 OK
    from: icscf
    to: ipsmgw
    proto: SIP
    note: (B1)
  - id: b2-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (B2)
    fields:
      - Body: RP-ERROR, RP-Cause 22 (memory capacity exceeded)
      - TP-FCS: "`0xD3`"
      - UE 2 now: Keeps nothing, and owes the network a notification
  - id: b2-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (B2)
  - id: b2-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (B2)
  - id: b2a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (B2)
  - id: b2a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (B2)
  - id: b2a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (B2)
  - id: b3-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (B3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555)
      - SM-Enumerated-Delivery-Failure-Cause: MEMORY_CAPACITY_EXCEEDED
  - id: b4-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (B4)
    fields:
      - SM-Delivery-Cause: UE_MEMORY_CAPACITY_EXCEEDED
  - id: hss-mcef
    do: sets MCEF
    at: hss
    fields:
      - MWD: The SC's address goes in
      - MCEF: Set
      - UNRI: "Not set: the UE is reachable and has no room"
  - id: b4-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (B4)
  - id: b5-ue2-p
    msg: MESSAGE
    from: ue2
    to: pcscf
    proto: SIP
    note: (B5)
    fields:
      - Body: "RP-SMMA: the notification of free memory"
      - Request-URI: The IP-SM-GW that brought the short message it could not keep
  - id: b5-p-s
    msg: MESSAGE
    from: pcscf
    to: scscf
    proto: SIP
    note: (B5)
  - id: b5-s-ip
    msg: MESSAGE
    from: scscf
    to: ipsmgw
    proto: SIP
    note: (B5)
  - id: b5a-ip-s
    msg: "202"
    from: ipsmgw
    to: scscf
    proto: SIP
    note: (B5)
    fields:
      - When: At once, before the HSS is told
  - id: b5a-s-p
    msg: "202"
    from: scscf
    to: pcscf
    proto: SIP
    note: (B5)
  - id: b5a-p-ue2
    msg: "202"
    from: pcscf
    to: ue2
    proto: SIP
    note: (B5)
  - id: b6-rfsm
    msg: Ready-For-SM
    from: ipsmgw
    to: hss
    proto: S6c
    note: (B6)
    fields:
      - Alert-Reason: UE_MEMORY_AVAILABLE
      - In MAP: READY_FOR_SM with the alert reason memoryAvailable
  - id: hss-mem
    do: clears MCEF and UNRI
    at: hss
    fields:
      - MCEF, UNRI: Cleared
      - Alerts: The SCs in the MWD
  - id: b6-ans
    msg: Ready-For-SM
    from: hss
    to: ipsmgw
    proto: S6c
    note: (answer)
  - id: b7-alr
    msg: ALR
    from: hss
    to: sc
    proto: S6c
    note: (B7)
  - id: b7-ala
    msg: ALA
    from: sc
    to: hss
    proto: S6c
    note: (B7)
  - id: b8-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (B8)
    fields:
      - Body: "RP-ACK: the answer to the notification of B5"
      - When: After the HSS has taken the notification
      - If the HSS cannot take it: RP-ERROR instead of RP-ACK
  - id: b8-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (B8)
  - id: b8-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (B8)
  - id: b8-p-ue2
    msg: MESSAGE
    from: pcscf
    to: ue2
    proto: SIP
    note: (B8)
  - id: d1-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (retry)
  - id: d1-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (retry)
    fields:
      - IP-SM-GW-Name: "There: a full memory sets no UNRI"
      - MWD-Status: Bit 2, MCEF-Set, is set. Bit 0 is clear
  - id: d2-tfr
    msg: TFR
    from: sc
    to: ipsmgw
    proto: SGd
    note: (D2)
  - id: d2-tfa
    msg: TFA
    from: ipsmgw
    to: sc
    proto: SGd
    note: (D2)
    fields:
      - Result-Code: DIAMETER_SUCCESS (2001)
  - id: sc-report
    do: reports the delivery to the HSS
    at: sc
    fields:
      - Input: MCEF-Set in the MWD-Status of the SRA
      - Decision: Reports the delivery once
  - id: d3-rdr
    msg: RDR
    from: sc
    to: hss
    proto: S6c
    note: (D3)
    fields:
      - SM-Delivery-Cause: SUCCESSFUL_TRANSFER
  - id: hss-success
    do: clears MCEF
    at: hss
    fields:
      - MCEF: Cleared
      - MWD: The SC's address goes; the other SCs left are alerted
  - id: d3-rda
    msg: RDA
    from: hss
    to: sc
    proto: S6c
    note: (D3)
  - id: f-ofr
    msg: OFR
    from: ipsmgw
    to: sc
    proto: SGd
  - id: f-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    fields:
      - MSISDN: "`12025550198`: no subscriber has it"
  - id: f-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    fields:
      - Experimental-Result: DIAMETER_ERROR_USER_UNKNOWN (5001)
  - id: f-ofa
    msg: OFA
    from: sc
    to: ipsmgw
    proto: SGd
    fields:
      - Experimental-Result: DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555)
      - SM-Enumerated-Delivery-Failure-Cause: INVALID_SME-ADDRESS
  - id: frep-ip-i
    msg: MESSAGE
    from: ipsmgw
    to: icscf
    proto: SIP
    note: (submit report)
    fields:
      - Body: RP-ERROR, RP-Cause 1 (unassigned number)
      - TP-FCS: 0xC3 (invalid SME address)
  - id: frep-i-s
    msg: MESSAGE
    from: icscf
    to: scscf
    proto: SIP
    note: (submit report)
  - id: frep-s-p
    msg: MESSAGE
    from: scscf
    to: pcscf
    proto: SIP
    note: (submit report)
  - id: frep-p-ue1
    msg: MESSAGE
    from: pcscf
    to: ue1
    proto: SIP
    note: (submit report)
  - id: g-srr
    msg: SRR
    from: sc
    to: hss
    proto: S6c
    note: (subscriber 3)
    fields:
      - MSISDN: "`12025550103`: subscriber 3 exists but is not registered"
  - id: g-sra
    msg: SRA
    from: hss
    to: sc
    proto: S6c
    note: (subscriber 3)
    fields:
      - Experimental-Result: DIAMETER_ERROR_ABSENT_USER (5550)
      - Why: No IP-SM-GW has registered for subscriber 3
  - id: hss-keep
    do: keeps the SC in the MWD
    at: hss
    fields:
      - MWD: The SC's address stays
      - Delivery: On the alert when subscriber 3 registers
```

---

# A short message through the IMS

ims-practice study session 6 (SMS over IP: the IP-SM-GW, the SC, the three kinds of report, and holding and alerting)

---

[layout: summary]

## What you will know after this session

To begin: a short message goes to a telephone number. Who in the IMS finds the UE of that number?

- How the IP-SM-GW learns that a user can take short messages.
- How the S-CSCF hands a short message to the IP-SM-GW.
- How the message reaches the other UE.
- How each side learns what became of the message.
- How the SC holds a short message that the UE cannot take, and what makes it try again.
- How to read the body of a MESSAGE.

> Guide: [Follow a short message through the IMS](../../../docs/guide/en/sms.md) (`docs/guide/en/sms.md`)
> The editions are named at the top of the guide, among them TS 24.341 V19.0.0, TS 23.204 V19.1.0, TS 24.011 V20.0.0 and TS 23.040 V20.0.0.

---

## Where SMS over IP fits

However it travels, a short message is made of the same two layers.

```text
 SIP MESSAGE   Content-Type: application/vnd.3gpp.sms      TS 24.341
  +-- RP       RP-DATA, RP-ACK, RP-ERROR                   TS 24.011
       +-- TP  SMS-SUBMIT, SMS-DELIVER, SMS-STATUS-REPORT  TS 23.040
            +-- text   GSM 7 bit, UCS2                     TS 23.038
```

- The IMS carries it in the body of a SIP MESSAGE.
- It can also travel in NAS signaling. RP and TP are the same there. SIP only replaces the part that carries the message.
- This project carries short messages in SIP only. SMS over NAS (through the MSC, the MME or the SMSF) is not built, because the subject is the IMS.

> Guide: ["Where SMS over IP fits"](../../../docs/guide/en/sms.md#where-sms-over-ip-fits)

---

## The nodes

```ims-scene
id: nodes
lead: "Besides the CSCFs, which route SIP, two nodes speak to the SMS network: the IP-SM-GW and the SC."
steps:
  - id: ues
    title: UE 1 and UE 2 (172.28.0.100, 101)
    focus: [ue1, ue2]
    text: |
      UE 1 is the sender, `+1-202-555-0101`.
      UE 2 is the recipient, `+1-202-555-0102`.
  - id: cscfs
    title: The P-CSCF, the S-CSCF and the I-CSCF (40, 30, 20)
    focus: [pcscf, scscf, icscf]
    checkpoint: true
    text: |
      They relay the MESSAGEs. The S-CSCF evaluates the iFCs.
      The CSCFs only route SIP and know nothing of SMS.
  - id: ipsmgw
    title: The IP-SM-GW (172.28.0.70)
    focus: [ipsmgw]
    text: |
      The application server (AS) of SMS over IP. It speaks both SIP and the language of the SMS network.
      Its number is `+12025550199`.
  - id: sc
    title: The SC (172.28.0.71)
    focus: [sc]
    text: |
      The Service Centre. It holds each short message until it is delivered or its validity period ends.
      Its address is `+12025550100`. It speaks SGd with the IP-SM-GW.
  - id: hss
    title: The HSS (172.28.0.10)
    focus: [hss]
    text: It gives the iFCs over Cx, and answers the IP-SM-GW over Sh and the SC over S6c.
```

| Node | Address | What it does for SMS |
| --- | --- | --- |
| UE 1 | 172.28.0.100 | The sender, `+1-202-555-0101`. |
| UE 2 | 172.28.0.101 | The recipient, `+1-202-555-0102`. |
| P-CSCF, S-CSCF, I-CSCF | 172.28.0.40, 30, 20 | Relay the MESSAGEs. The S-CSCF evaluates the iFCs. |
| IP-SM-GW | 172.28.0.70 | The application server (AS) of SMS over IP. |
| SC | 172.28.0.71 | The Service Centre. Holds each short message until it is delivered or its validity period ends, and delivers it. |
| HSS | 172.28.0.10 | Gives the iFCs (Cx). Answers the IP-SM-GW (Sh) and the SC (S6c). |

- The SC address is `+12025550100`. The number of the IP-SM-GW is `+12025550199`.
- The CSCFs only route SIP. A short message needs a node that speaks both SIP and the language of the SMS network: the IP-SM-GW.
- The IP-SM-GW hands each short message to the SC over SGd, and the SC brings the ones for the UEs back over SGd. The SC asks the HSS over S6c which node serves a recipient. One node is the SMS-SC, the SMS-IWMSC and the SMS-GMSC.

> Guide: ["The nodes"](../../../docs/guide/en/sms.md#the-nodes) and ["Why an application server, and what the SC is"](../../../docs/guide/en/sms.md#why-an-application-server-and-what-the-sc-is). TS 24.341 5.3.3. TS 23.204 5.2.4. TS 23.040 4.1, 6.1.

---

## How the S-CSCF picks the IP-SM-GW

```ims-scene
id: ifc
lead: The S-CSCF knows nothing of SMS. It sends to the IP-SM-GW the requests that match the iFCs from the HSS.
steps:
  - id: register
    title: A REGISTER matches priority 0
    only: [r1-i-s, scscf-ifc0]
    focus: [scscf]
    text: Once a registration succeeds, the S-CSCF looks for the criteria that a REGISTER matches. Priority 0 matches every REGISTER.
  - id: third-party
    title: A third-party REGISTER goes to the IP-SM-GW
    show: [tp-register]
    checkpoint: true
    text: The body holds the MSISDN of the user, the ServiceInfo of the iFC.
  - id: submit
    title: The UE's MESSAGE matches priority 1
    only: [m1-p-s, scscf-ifc1]
    focus: [scscf]
    text: A MESSAGE from a registered user whose Content-Type is `application/vnd.3gpp.sms`.
  - id: submit-on
    title: The MESSAGE goes to the IP-SM-GW
    show: [m1-s-ip]
    checkpoint: true
    text: "It does not go to the SC of the Request-URI: the IP-SM-GW is put on top of Route."
  - id: deliver
    title: The IP-SM-GW's MESSAGE does not match priority 2
    only: [m3-i-s, scscf-mt]
    focus: [scscf]
    text: Priority 2 is for a MESSAGE to the user, unless P-Asserted-Identity is the IP-SM-GW.
  - id: deliver-on
    title: The MESSAGE goes on to UE 2
    show: [m3-s-p]
    text: The negated condition keeps the IP-SM-GW's own MESSAGE from coming back to it.
```

The S-CSCF knows nothing of SMS. The iFCs from the HSS say which request goes to which AS.

| Priority | Requests that match | Where they go |
| --- | --- | --- |
| 0 | Every REGISTER | The IP-SM-GW, with the MSISDN of the user. |
| 1 | A MESSAGE from a registered user whose Content-Type is `application/vnd.3gpp.sms` | The IP-SM-GW |
| 2 | The same MESSAGE to a registered user, unless the IP-SM-GW sent it | The IP-SM-GW |

- The iFCs are in `deploy/compose/hss.yaml` and reach the S-CSCF in the User-Data of the SAA at registration. The guide shows the YAML and the XML side by side.
- In the XML, ConditionTypeCNF says how the conditions combine: the SPTs of one Group are ORed, the Groups are ANDed.

> Guide: ["The filter criteria"](../../../docs/guide/en/sms.md#the-filter-criteria). TS 29.228 B.2.2, B.2.3, Annex E.

---

## A short message to a user: a negated condition

The criterion of priority 2 in `deploy/compose/hss.yaml` has four groups of conditions:

```text
     method is MESSAGE
 AND Content-Type is application/vnd.3gpp.sms
 AND session case is TERMINATING_REGISTERED
 AND NOT  P-Asserted-Identity is sip:ipsmgw.ims.mnc001.mcc001.3gppnetwork.org
```

- The IP-SM-GW puts its own URI in P-Asserted-Identity of every MESSAGE it sends to a user. The negated condition keeps those from coming back to it in a loop.
- What the criterion catches is a short message that arrives from another network. The IP-SM-GW of the user then delivers it.

> Guide: ["The filter criteria"](../../../docs/guide/en/sms.md#the-filter-criteria) and [Connect two IMS networks, "Short messages across"](../../../docs/guide/en/interconnect.md#short-messages-across). TS 23.218 5.2.3 NOTE 5. TS 23.204 6.4a.

---

## Run it

```sh
docker compose up -d --build

# First terminal: UE 2 registers and stays registered.
docker compose run --rm ue2 register \
  -impi 001010000000002@ims.mnc001.mcc001.3gppnetwork.org \
  -impu sip:+12025550102@ims.mnc001.mcc001.3gppnetwork.org \
  -password test-password-2

# Second terminal: UE 1 sends, and asks for a status report.
docker compose run --rm ue sms -status-report +12025550102 "Hello UE-B, this is UE-A."
```

- The flags come before the number and the text.
- UE 1 logs `"msg":"submit report"` and `"msg":"status report"`.
- UE 2 logs `"msg":"sms received"`.

> Guide: ["Send a short message"](../../../docs/guide/en/sms.md#send-a-short-message). The e2e script of SMS in `scripts/` does the same.

---

## Open the capture

```text
sip.CSeq.method in {"MESSAGE", "REGISTER"} || (sip && ip.addr == 172.28.0.70)
  || diameter.cmd.code in {302, 306, 307, 8388645, 8388646, 8388647, 8388648, 8388649, 16777214}
```

- It is one filter, on one line in Wireshark.
- The REGISTERs, the MESSAGEs and their responses, the SUBSCRIBEs and NOTIFYs of the IP-SM-GW, LIR, UDR and PUR stay, and so do the five commands of SGd and S6c and the Ready-For-SM of this project.
- Wireshark decodes the body of each MESSAGE by itself.
  - The Info column ends with the RP message, such as `(RP) RP-DATA (MS to Network)`.
  - The details have a "GSM A-I/F RP" tree and a "GSM SMS TPDU" tree.

> Guide: ["Open the capture"](../../../docs/guide/en/sms.md#open-the-capture). The ladder is under ["Sending and delivering"](../../../docs/guide/en/sms.md#sending-and-delivering-hello-ue-b-this-is-ue-a) in the guide.

---

## The shape of the whole: six MESSAGEs

| No. | Direction | Body | Steps |
| --- | --- | --- | --- |
| 1 | UE 1 to IP-SM-GW | RP-DATA with SMS-SUBMIT | 1 to 4 |
| 2 | IP-SM-GW to UE 1 | Submit report: RP-ACK with SMS-SUBMIT-REPORT | 6 and 7 |
| 3 | IP-SM-GW to UE 2 | RP-DATA with SMS-DELIVER | 8 to 10 |
| 4 | UE 2 to IP-SM-GW | Delivery report: RP-ACK with SMS-DELIVER-REPORT | 11 and 12 |
| 5 | IP-SM-GW to UE 1 | Status report: RP-DATA with SMS-STATUS-REPORT | 14 |
| 6 | UE 1 to IP-SM-GW | Delivery report: RP-ACK | 15 |

- Each of the six has a Call-ID of its own.
- The SIP responses (202 and 200) only say that a MESSAGE arrived. What became of the short message is in the reports.
- MESSAGEs 2 and 3 go out together, so they may come in either order in the capture. Without a request for a status report, 5 and 6 do not exist.

---

## The IP-SM-GW learns about the user (R1 to R5)

```ims-scene
id: register
lead: A third-party REGISTER does not say whether the UE takes SMS. The IP-SM-GW checks the reg event first, then writes itself into the HSS.
steps:
  - id: register
    title: UE 2 registers with the feature tag (R1)
    only: [r1-ue2-p, r1-p-i, r1-i-s]
    active: r1-ue2-p
    checkpoint: true
    text: |
      The registration is the one of the registration session. The first REGISTER and the 401 are left out.
      The one difference is `+g.3gpp.smsip` in Contact.
  - id: ok
    title: The 200 OK ends the registration
    show: [r1ok-s-i, r1ok-i-p, r1ok-p-ue2]
    detail: []
    checkpoint: true
    text: UE 2 is registered. What follows goes on without UE 2 waiting for it.
  - id: third-party
    title: The S-CSCF sends a third-party REGISTER (R2)
    only: [tp-register]
    checkpoint: true
    text: From and Contact are the S-CSCF, To is the user. `<service-info>` in the body holds the MSISDN.
  - id: third-party-ok
    title: The IP-SM-GW answers 200 OK
    show: [tp-ok]
    checkpoint: true
    text: The IP-SM-GW keeps the MSISDN, which names the user in R5.
  - id: subscribe
    title: The IP-SM-GW subscribes to the reg event (R3)
    only: [isub-ip-i, lir-r, lia-r, isub-i-s]
    active: isub-ip-i
    checkpoint: true
    text: The SUBSCRIBE goes to the entry of the home network, and the I-CSCF finds the S-CSCF with an LIR.
  - id: notify
    title: The S-CSCF reports the state in a NOTIFY (R4)
    hide: [lir-r, lia-r]
    show: [isubok-s-i, isubok-i-ip, inotify]
    active: inotify
    checkpoint: true
    text: The NOTIFY goes straight to the IP-SM-GW. The 200 OK to the SUBSCRIBE goes through the I-CSCF, so either may come first.
  - id: check
    title: It looks for a contact that takes SMS
    show: [inotify-ok, ipsmgw-check]
    active: ipsmgw-check
    focus: [ipsmgw]
    checkpoint: true
    text: The IP-SM-GW answers 200 OK and looks for an active contact with `+g.3gpp.smsip`.
  - id: udr
    title: "It reads the user's IMSI (R5: UDR and UDA)"
    only: [udr, uda]
    active: uda
    detail: [udr, uda]
    checkpoint: true
    text: A short message over SGd names its recipient by IMSI. The IMSI finds the user then.
  - id: pur
    title: "It registers itself in the HSS (R5: PUR and PUA)"
    only: [pur, pua]
    active: pur
    detail: [pur]
    text: Sh command 307. The HSS answers 2001 in the PUA and now knows which IP-SM-GW carries UE 2's short messages.
```

| Look at | What to see |
| --- | --- |
| Contact of the UE's REGISTER | The feature tag `+g.3gpp.smsip`. |
| REGISTER from the S-CSCF to the IP-SM-GW | A third-party REGISTER. `<service-info>` in the body holds the MSISDN. |
| SUBSCRIBE of the IP-SM-GW | Event is `reg`. It goes through the I-CSCF, which finds the S-CSCF with an LIR. |
| reginfo of the NOTIFY | `<unknown-param name="+g.3gpp.smsip">`. |
| PUR | Data-Reference is 24. `<IP-SM-GW-Number>` holds `12025550199`. |

The filter is `diameter.cmd.code == 307`.

- The third-party REGISTER alone does not say whether the UE can take short messages, so the IP-SM-GW subscribes to the reg event. For a UE that registers without `+g.3gpp.smsip`, no PUR is sent.

> Guide: ["Registration: the IP-SM-GW learns about the user"](../../../docs/guide/en/sms.md#registration-the-ip-sm-gw-learns-about-the-user). TS 24.341 5.3.2.2, 5.3.3.2. TS 24.229 5.4.1.7, 5.4.1.7A, 5.7.1.1. TS 29.328 6.1.2, 7.6.16.

---

## UE 1 submits the short message (1 to 4)

```ims-scene
id: submit
lead: UE 1 addresses the short message to the SC, not to the recipient. The iFC of the S-CSCF takes it to the IP-SM-GW.
steps:
  - id: send
    title: UE 1 sends the MESSAGE (1)
    only: [m1-ue1-p]
    checkpoint: true
    text: The Request-URI is the SC's number. The body is RP-DATA, and the SMS-SUBMIT inside has the recipient's number and the text.
  - id: pcscf
    title: The P-CSCF passes it on (2)
    show: [m1-p-s]
    text: |
      As for an INVITE, it puts in P-Asserted-Identity and follows the Service-Route.
      It adds no Record-Route, and no media relay is involved.
  - id: scscf
    title: The MESSAGE matches the iFC of priority 1 (3)
    show: [scscf-ifc1]
    focus: [scscf]
    zoom: [pcscf, scscf, ipsmgw]
    checkpoint: true
    text: The S-CSCF serves the sender. It adds UE 1's tel URI as a second P-Asserted-Identity.
  - id: ipsmgw
    title: The MESSAGE reaches the IP-SM-GW (3)
    show: [m1-s-ip]
    checkpoint: true
    text: The second Route is the S-CSCF itself with `odi`. An AS that sent the request back would let the S-CSCF go on with the next iFC. The IP-SM-GW does not.
  - id: accepted
    title: The IP-SM-GW accepts with 202 (4)
    show: [m1a-ip-s, m1a-s-p, m1a-p-ue1]
    active: m1a-ip-s
    text: |
      202, not 200: nothing has been delivered yet.
      What becomes of the short message is told by the submit report, not by the 202.
```

| Look at | What to see |
| --- | --- |
| Request-URI and To | `tel:+12025550100`: the SC, not the recipient. |
| Content-Type and body | `application/vnd.3gpp.sms`, 47 octets. |
| After the P-CSCF | P-Asserted-Identity is there. Record-Route is not. |
| Route after the S-CSCF | The IP-SM-GW on top, then the S-CSCF's own URI with `odi`. |
| P-Asserted-Identity after the S-CSCF | UE 1's tel URI is added. |
| Answer of the IP-SM-GW | 202 Accepted. |

- As with the INVITE of the call: Route and P-Asserted-Identity. Unlike it: no Record-Route, no media relay.
- 202, not 200: nothing has been delivered yet. The `odi` would let the S-CSCF go on with the next iFC if the AS sent the request back; the IP-SM-GW does not.

> Guide: [1](../../../docs/guide/en/sms.md#1-ue-1-submits-the-short-message) to [4](../../../docs/guide/en/sms.md#4-the-ip-sm-gw-accepts-202) and ["The original dialog identifier"](../../../docs/guide/en/sms.md#the-original-dialog-identifier). TS 24.341 5.3.1.2, 5.3.3.4.1. TS 24.229 5.2.6.3.7, 5.4.3.2, 5.4.3.4. RFC 3428 7.

---

## The SC accepts (5)

```ims-scene
id: sc-accept
lead: The IP-SM-GW hands the short message to the SC over SGd. The SC checks the recipient's number with the HSS before it takes it.
steps:
  - id: check
    title: The IP-SM-GW checks the RP-DATA
    only: [m1-s-ip, ipsmgw-rp]
    focus: [ipsmgw]
    checkpoint: true
    text: It checks after the 202. If any check fails, the submit report is an RP-ERROR.
  - id: ofr
    title: It hands it to the SC (OFR)
    only: [ofr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd command 8388645. SM-RP-UI holds UE 1's SMS-SUBMIT as it is.
  - id: srr
    title: The SC asks the HSS about the recipient's number (SRR)
    show: [srr1]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: S6c command 8388647. It checks that a user has the number.
  - id: sra
    title: The HSS names UE 2's IP-SM-GW (SRA)
    show: [sra1]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The IP-SM-GW registered by the PUR of R5. A number that no subscriber has gets DIAMETER_ERROR_USER_UNKNOWN here.
  - id: ofa
    title: The SC takes it (OFA)
    show: [ofa]
    zoom: [ipsmgw, sc, hss]
    text: The SC stamps it with TP-SCTS and holds it. From here on, the short message belongs to the SC.
```

| Look at | What to see |
| --- | --- |
| OFR and OFA | The IP-SM-GW hands the SMS-SUBMIT to the Service Centre over SGd, and the answer carries the submit report. |
| SRR and SRA | The Service Centre asks the HSS over S6c whether anyone has the recipient's number. |

The filter is `diameter.cmd.code in {8388645, 8388647} || sip.In-Reply-To`. It keeps the submit report of the next slide too.

- The IP-SM-GW hands the message to the SC as soon as it has sent the 202, so the OFR may come before the 202 in the capture.
- A number that no subscriber has gets DIAMETER_ERROR_USER_UNKNOWN in the SRA. The sender learns of it at once, without waiting for a status report ("When it fails").

> Guide: [4](../../../docs/guide/en/sms.md#4-the-ip-sm-gw-accepts-202) and [5](../../../docs/guide/en/sms.md#5-the-ip-sm-gw-hands-the-short-message-to-the-sc-ofr-srr-and-ofa). TS 23.204 6.3. TS 29.338 6.2.1. TS 23.040 8.2.2, 6.1.

---

## The submit report goes back to UE 1 (6, 7)

```ims-scene
id: submit-report
lead: The submit report is a new MESSAGE from the IP-SM-GW to UE 1. As a request from an AS to a user, it enters through the I-CSCF, the entry of the home network.
steps:
  - id: send
    title: The IP-SM-GW sends the submit report (6)
    only: [ofa, m2-ip-i]
    active: m2-ip-i
    text: The SMS-SUBMIT-REPORT of the OFA goes in an RP-ACK. In-Reply-To says which short message it answers.
  - id: lir
    title: The I-CSCF finds UE 1's S-CSCF (LIR and LIA)
    show: [lir-m2, lia-m2]
    active: lia-m2
    text: As for an incoming call. The I-CSCF adds no Record-Route.
  - id: deliver
    title: The submit report reaches UE 1
    hide: [ofa, lir-m2, lia-m2]
    show: [m2-i-s, m2-s-p, m2-p-ue1]
    active: m2-p-ue1
    detail: []
    checkpoint: true
    text: The S-CSCF treats it as a terminating request and follows UE 1's Path.
  - id: match
    title: UE 1 matches it
    show: [ue1-report]
    focus: [ue1]
    text: UE 1 kept the Call-ID of step 1. It compares In-Reply-To with it and records the message as accepted.
  - id: ok
    title: UE 1 answers 200 (7)
    show: [m2a-ue1-p, m2a-p-s, m2a-s-i, m2a-i-ip]
    active: m2a-ue1-p
    detail: []
    text: "The 200 goes back to the IP-SM-GW through the I-CSCF: a response follows the Via."
```

| Look at | What to see |
| --- | --- |
| Request-URI of the IP-SM-GW's MESSAGE | UE 1's SIP URI. |
| Accept-Contact | `*;+g.3gpp.smsip;require;explicit`. |
| In-Reply-To | The Call-ID of the MESSAGE of step 1. |
| Body | RP-ACK with an SMS-SUBMIT-REPORT inside. |
| The way | In through the I-CSCF, which finds UE 1's S-CSCF with an LIR. |

- The IP-SM-GW does not use the Route of the S-CSCF. It puts `Route: <sip:ims.mnc001.mcc001.3gppnetwork.org;lr>` on its own request and sends it to the entry of the home network.
- The submit report and the delivery of step 8 go out together, so their LIRs and answers mix in the capture.

> Guide: [6](../../../docs/guide/en/sms.md#6-the-submit-report-goes-back-to-ue-1) and [7](../../../docs/guide/en/sms.md#7-ue-1-answers-200). TS 24.341 5.3.3.4.3, 5.3.1.2. TS 24.229 5.7.3.

---

## The SC starts the delivery (8)

```ims-scene
id: sc-deliver
lead: Before it delivers, the SC asks the HSS again. The TFR names the recipient by IMSI.
steps:
  - id: make
    title: The SC makes the SMS-DELIVER
    only: [sc-make]
    focus: [sc]
    text: The sender's number goes in TP-OA, where the SMS-SUBMIT had TP-DA. TP-SCTS is new.
  - id: srr
    title: It asks as the SMS-GMSC that delivers (SRR)
    show: [srr2]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The first query was the SMS-IWMSC's. The user may have moved or gone away since.
  - id: sra
    title: The IP-SM-GW again (SRA)
    show: [sra2]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The SRA names UE 2's IP-SM-GW and gives UE 2's IMSI.
  - id: tfr
    title: The SC hands it to the IP-SM-GW in a TFR (8)
    show: [tfr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SGd command 8388646. This TFR gets no answer until the delivery is over.
  - id: find
    title: The IP-SM-GW finds UE 2 by IMSI
    show: [ipsmgw-find]
    focus: [ipsmgw]
    text: It uses the IMSI read in the UDA of R5. It puts the delivery in the shared `Store` before the MESSAGE goes.
```

| Look at | What to see |
| --- | --- |
| TFR | The Service Centre forwards the SMS-DELIVER to the IP-SM-GW over SGd, naming the recipient by IMSI. |

- Compare the SMS-SUBMIT with the SMS-DELIVER: TP-DA became TP-OA, and TP-SCTS is new.
- A TFR always names its node in Destination-Host: the IP-SM-GW of the SRA.

> Guide: [8](../../../docs/guide/en/sms.md#8-the-sc-forwards-the-message-and-the-ip-sm-gw-delivers-it-tfr-and-sms-deliver). TS 23.040 8.1.1. TS 29.338 6.2.2, 6.3.1.2.

---

## The MESSAGE reaches UE 2 (8 to 10)

```ims-scene
id: mt
lead: The MESSAGE to UE 2 also comes in through the I-CSCF. The S-CSCF picks the contact by the feature tag of the registration.
steps:
  - id: send
    title: The IP-SM-GW sends the MESSAGE (8)
    only: [m3-ip-i, lir-m3, lia-m3]
    active: m3-ip-i
    checkpoint: true
    text: It goes to UE 2's tel URI. The I-CSCF finds UE 2's S-CSCF with an LIR.
  - id: scscf
    title: The S-CSCF checks the criteria and the contacts (9)
    hide: [lir-m3, lia-m3]
    show: [m3-i-s, scscf-mt]
    active: scscf-mt
    focus: [scscf]
    text: |
      It matches Accept-Contact against the parameters of the contacts UE 2 registered.
      A UE that did not register `+g.3gpp.smsip` would not match, and the S-CSCF would answer 480.
  - id: ue2
    title: The MESSAGE reaches UE 2
    show: [m3-s-p, m3-p-ue2]
    active: m3-p-ue2
    detail: []
    text: The S-CSCF adds P-Called-Party-ID and follows UE 2's Path.
  - id: ok
    title: UE 2 answers 200 (10)
    show: [m3a-ue2-p, m3a-p-s, m3a-s-i, m3a-i-ip]
    active: m3a-ue2-p
    checkpoint: true
    text: The 200 only says that the MESSAGE arrived. Whether UE 2 took the short message, the delivery report says.
```

| Look at | What to see |
| --- | --- |
| Request-URI | `tel:+12025550102`, the tel URI of that IMSI. |
| Request-Disposition | `no-fork`. |
| Body | RP-DATA from the network, with an SMS-DELIVER. TP-OA is the sender. |
| iFC of priority 2 | No match: P-Asserted-Identity is the IP-SM-GW. |
| What the S-CSCF does | Matches Accept-Contact against the Contact that UE 2 registered. |
| Answer of UE 2 | 200. It only says that the MESSAGE arrived. |

- A UE that did not register `+g.3gpp.smsip` does not get the MESSAGE: the S-CSCF answers 480. The feature tag of the registration is used here.

> Guide: [8](../../../docs/guide/en/sms.md#8-the-sc-forwards-the-message-and-the-ip-sm-gw-delivers-it-tfr-and-sms-deliver) to [10](../../../docs/guide/en/sms.md#10-ue-2-answers-200), with the matching of the S-CSCF in [9](../../../docs/guide/en/sms.md#9-the-s-cscf-picks-the-contact-by-caller-preferences). TS 24.341 5.3.3.4.2, 5.3.2.3. TS 24.229 5.4.3.3. RFC 3841 7.2.4, 9.1.

---

## The delivery report (11, 12)

```ims-scene
id: delivery-report
lead: The delivery report is a MESSAGE that UE 2 originates. UE 2's own iFC takes it to the IP-SM-GW, which then answers the TFR.
steps:
  - id: send
    title: UE 2 sends the delivery report (11)
    only: [m4-ue2-p]
    text: It goes to the IP-SM-GW that was in P-Asserted-Identity of step 8.
  - id: scscf
    title: It matches UE 2's iFC (12)
    show: [m4-p-s, scscf-dr]
    active: scscf-dr
    focus: [scscf]
    text: "A MESSAGE with an SMS body that UE 2 sends: it matches the same criterion as in step 3."
  - id: ipsmgw
    title: The delivery report reaches the IP-SM-GW
    show: [m4-s-ip]
    detail: []
    checkpoint: true
    text: Route is the IP-SM-GW, then the S-CSCF with `odi`.
  - id: accepted
    title: The IP-SM-GW answers 202
    show: [m4a-ip-s, m4a-s-p, m4a-p-ue2]
    active: m4a-ip-s
    detail: []
    text: 202 Accepted, as in step 4.
  - id: find
    title: It finds the delivery and ends it
    show: [ipsmgw-dr]
    focus: [ipsmgw]
    text: It finds the delivery in the shared `Store` by the Call-ID of In-Reply-To. Whichever IP-SM-GW node gets the report can end the delivery.
  - id: tfa
    title: The TFR gets its answer (TFA)
    only: [tfr, ipsmgw-dr, tfa]
    active: tfa
    zoom: [ipsmgw, sc, hss]
    text: The answer to the TFR of step 8 comes only now. The TFR waits as long as the delivery takes, up to 66 seconds here.
```

| Look at | What to see |
| --- | --- |
| Request-URI of UE 2's MESSAGE | The IP-SM-GW, taken from P-Asserted-Identity of step 8. |
| In-Reply-To | The Call-ID of the MESSAGE of step 8. |
| Body | RP-ACK from the UE with an SMS-DELIVER-REPORT inside. |
| Route after the S-CSCF | As in step 3: the MESSAGE matches UE 2's own iFC. |
| Answer of the IP-SM-GW | 202 Accepted. |

- The delivery report is a MESSAGE that UE 2 originates, so UE 2's own iFC takes it to the IP-SM-GW.
- The state of a delivery is in the `Store` of the IP-SM-GW, so whichever IP-SM-GW gets the delivery report can finish the delivery.

> Guide: [11](../../../docs/guide/en/sms.md#11-ue-2-sends-the-delivery-report) and [12](../../../docs/guide/en/sms.md#12-the-delivery-report-reaches-the-ip-sm-gw-through-ue-2s-criteria). TS 24.341 5.3.2.4, 5.3.3.4.1. TS 23.204 6.5.

---

## The status report (13 to 15)

```ims-scene
id: status-report
lead: The SC delivers the status report like any short message, to the sender, UE 1.
steps:
  - id: make
    title: The SC makes the status report (13)
    only: [tfa, sc-sr]
    active: sc-sr
    focus: [sc]
    checkpoint: true
    text: UE 1 asked for it with TP-SRR in the SMS-SUBMIT. The SC makes it as soon as the TFA of step 12 is in.
  - id: srr
    title: It asks the HSS about the sender (SRR and SRA)
    only: [srr3, sra3]
    active: sra3
    zoom: [ipsmgw, sc, hss]
    detail: [srr3, sra3]
    checkpoint: true
    text: This time the recipient is UE 1. The HSS names UE 1's IP-SM-GW.
  - id: tfr
    title: It goes to the IP-SM-GW in a TFR (13)
    show: [tfr3]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: SM-RP-UI is the SMS-STATUS-REPORT. The recipient is UE 1's IMSI.
  - id: send
    title: The status report reaches UE 1 (14)
    only: [m5-ip-i, lir-m5, lia-m5, m5-i-s, m5-s-p, m5-p-ue1]
    active: m5-ip-i
    checkpoint: true
    text: |
      It goes to UE 1's tel URI, by the way of steps 8 to 10: the I-CSCF finds the S-CSCF with an LIR.
      TP-MR is the one of the SMS-SUBMIT, so UE 1 knows which message the report is about.
  - id: ok
    title: UE 1 answers 200
    hide: [lir-m5, lia-m5]
    show: [m5a-ue1-p, m5a-p-s, m5a-s-i, m5a-i-ip]
    active: m5a-ue1-p
    detail: []
    text: Here too, the 200 only says that the MESSAGE arrived.
  - id: report
    title: UE 1 sends a delivery report (15)
    only: [m6-ue1-p, m6-p-s, m6-s-ip]
    active: m6-ue1-p
    text: Like UE 2 in step 11, it says that the status report arrived. UE 1's own iFC takes it to the IP-SM-GW.
  - id: end
    title: The 202 and the TFA end the delivery
    show: [m6a-ip-s, m6a-s-p, m6a-p-ue1, tfa3]
    active: tfa3
    text: The IP-SM-GW answers 202, and tells the SC in a TFA that the status report was delivered.
```

- UE 1 asked for a status report with TP-SRR in the SMS-SUBMIT.

| Look at | What to see |
| --- | --- |
| SRR and TFR | Once the TFA of step 12 is in, the SC delivers the report like a short message, to the sender's number. |
| MESSAGE of the IP-SM-GW | To UE 1's tel URI, with an SMS-STATUS-REPORT inside. |
| TP-MR | The value of the SMS-SUBMIT. It says which message the report is about. |
| TP-ST | 0x00: the recipient received the message. |
| Delivery report of UE 1 | Says that the status report arrived. |

The filter is `gsm_sms.tp-mti == 2`.

- The submit report says that the SC accepted the message. The status report says that it reached the other side.

> Guide: ["The status report"](../../../docs/guide/en/sms.md#the-status-report). TS 24.341 5.3.3.4.4, 5.3.1.3. TS 23.040 9.2.3.15.

---

## UE 2 takes no short message, and the SC holds it (A1 to A4)

```ims-scene
id: absent
lead: When UE 2 refuses with 480, the SC keeps the short message. The HSS writes the SC's address into the MWD.
steps:
  - id: tfr
    title: The delivery starts as in step 8 (A1)
    only: [a1-tfr]
    zoom: [ipsmgw, sc, hss]
    text: "UE 2 is set to `{\"available\": false}` on its control API, and UE 1 sends a short message."
  - id: message
    title: The MESSAGE reaches UE 2
    show: [a1-ip-i, a1-i-s, a1-s-p, a1-p-ue2]
    active: a1-p-ue2
    detail: []
    text: By the way of step 8. The LIR of the I-CSCF is left out.
  - id: "480"
    title: UE 2 answers 480 (A2)
    show: [a2-ue2-p, a2-p-s, a2-s-i, a2-i-ip]
    active: a2-ue2-p
    checkpoint: true
    text: UE 2 was reached, but its state does not allow the request. No delivery report follows.
  - id: tfa
    title: The IP-SM-GW answers absent user (A3)
    only: [a1-tfr, a3-tfa]
    active: a3-tfa
    zoom: [ipsmgw, sc, hss]
    text: The 480 is mapped to the error of an absent subscriber, which the TFA carries as DIAMETER_ERROR_ABSENT_USER.
  - id: unri
    title: The IP-SM-GW sets UNRI
    show: [ipsmgw-unri]
    focus: [ipsmgw]
    zoom: [ipsmgw, sc, hss]
    text: "UE-Not-Reachable-for-IP: the flag that has it tell the HSS when UE 2 is back."
  - id: rdr
    title: "The SC reports to the HSS (A4: RDR)"
    show: [a4-rdr]
    zoom: [ipsmgw, sc, hss]
    text: S6c command 8388649, Report-SM-Delivery-Status.
  - id: hold
    title: The HSS puts the SC in the MWD
    show: [hss-absent, a4-rda]
    active: hss-absent
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: The HSS sets its UNRI too, and names no serving node in an SRA while it is set, so as not to send the SC to an IP-SM-GW that fails again.
```

`PUT /sms-availability` with `{"available": false}` on UE 2's control API, then a short message from UE 1.

```ladder
UE 2, IP-SM-GW, SC, HSS
SC -> IP-SM-GW: TFR // A1
IP-SM-GW -> UE 2: MESSAGE SMS-DELIVER // A1
UE 2 --> IP-SM-GW: 480 // A2
IP-SM-GW --> SC: TFA absent user // A3
SC <-> HSS: RDR, RDA // A4
```

| Step | What to see |
| --- | --- |
| A2 | 480 (Temporarily Unavailable): UE 2 was reached, but its state does not allow the request. No delivery report follows. |
| A3 | DIAMETER_ERROR_ABSENT_USER (5550) in the TFA. The IP-SM-GW raises its own UNRI for UE 2. |
| A4 | `SM-Delivery-Cause` ABSENT_USER. The HSS puts the SC's address in the MWD and sets UNRI, and names no serving node in an SRA while UNRI is set. |

> Guide: ["UE 2 takes no short message"](../../../docs/guide/en/sms.md#ue-2-takes-no-short-message). TS 24.341 5.3.3.4.2. TS 29.311 6.1.4.4.1. TS 29.338 7.3.3, 5.2.3, 5.2.1.3. TS 23.040 2.1.1, 3.2.8.

---

## The SC learns that UE 2 is back (A5 to A7)

```ims-scene
id: alert
lead: A request of UE 2 that reaches the IP-SM-GW says that UE 2 is back. The news reaches the SC through the HSS.
steps:
  - id: message
    title: UE 2 sends a request (A5)
    only: [a5-ue2-p, a5-p-s, a5-s-ip]
    active: a5-ue2-p
    text: "After `{\"available\": true}`. Any request does; here it is a short message of UE 2's own. Its OFR is left out."
  - id: accepted
    title: The IP-SM-GW answers 202
    show: [a5a-ip-s, a5a-s-p, a5a-p-ue2]
    active: a5a-ip-s
    detail: []
    text: It answers 202, as to every MESSAGE.
  - id: present
    title: It learns that UE 2 is back
    show: [ipsmgw-present]
    focus: [ipsmgw]
    checkpoint: true
    text: The IP-SM-GW still has the UNRI of A3. It clears it and tells the HSS.
  - id: ready
    title: "The IP-SM-GW tells the HSS (A6: Ready-For-SM)"
    only: [a6-rfsm]
    zoom: [ipsmgw, sc, hss]
    text: This project's own command. A network sends this news in READY_FOR_SM of MAP, which this project does not speak.
  - id: hss
    title: The HSS clears UNRI
    show: [hss-present]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: It acts only if the number is that of the IP-SM-GW UE 2 registered at.
  - id: alert
    title: "The HSS answers and alerts the SC (A7: ALR)"
    hide: [hss-present]
    show: [a6-ans, a7-alr]
    active: a7-alr
    zoom: [ipsmgw, sc, hss]
    text: The HSS answers without waiting for the ALR to finish, so the answer and the ALR come in either order.
```

```ladder
UE 2, IP-SM-GW, SC, HSS
UE 2 -> IP-SM-GW: MESSAGE // A5
IP-SM-GW <-> HSS: Ready-For-SM UE_PRESENT, answer // A6
SC <- HSS: ALR // A7
SC <-> HSS: SRR, SRA // A8
SC -> IP-SM-GW -> UE 2: TFR, MESSAGE SMS-DELIVER // A8
UE 2 -> IP-SM-GW: MESSAGE delivery report // A9
IP-SM-GW --> SC: TFA 2001 // A9
```

- **A5.** After `{"available": true}`, any request of UE 2 that reaches the IP-SM-GW says that the UE is there. A re-registration says the same (C1 to C4 of the guide).
- **A6.** Ready-For-SM is this project's own command, code 16777214. A network sends this notice in the READY_FOR_SM service of MAP, which this project does not speak. Wireshark names it `Unknown`.
- **A7.** The HSS clears UNRI and alerts every SC in the MWD. **A8.** The alert makes the queue due at once, ahead of the retry an hour later (`retry_delays` in `deploy/compose/sc.yaml`).

> Guide: ["UE 2 takes no short message"](../../../docs/guide/en/sms.md#ue-2-takes-no-short-message) and ["UE 2 only registers again"](../../../docs/guide/en/sms.md#ue-2-only-registers-again). TS 23.204 6.5b. TS 29.002 12.4.1. TS 29.338 5.2.2. TS 23.040 3.2.6.

---

## The SC sends it again (A8, A9)

```ims-scene
id: resend
lead: Alerted, the SC delivers at once, without waiting for the retry an hour later. This time UE 2 takes it.
steps:
  - id: due
    title: The SC makes what it holds due (A8)
    only: [a7-alr, sc-due]
    active: sc-due
    focus: [sc]
    zoom: [ipsmgw, sc, hss]
    text: It makes the queue of that number due at once, ahead of the retry interval.
  - id: srr
    title: The SC asks the HSS (SRR)
    hide: [a7-alr]
    show: [a8-srr]
    zoom: [ipsmgw, sc, hss]
    text: Unlike after A4, UNRI is now clear.
  - id: sra
    title: The IP-SM-GW is named again (SRA and ALA)
    show: [a8-sra, a7-ala]
    active: a8-sra
    zoom: [ipsmgw, sc, hss]
    text: The SC starts the delivery before it answers the ALR, and the HSS takes the SC out of the MWD only after the ALA, so MWD-Status in the SRA is mostly 0.
  - id: tfr
    title: It sends what it held in a TFR
    show: [a8-tfr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: From here on, as in steps 8 to 12.
  - id: deliver
    title: UE 2 takes it and answers 200
    only: [a8-tfr, a8-ip-i, a8-i-s, a8-s-p, a8-p-ue2, a8a-ue2-p, a8a-p-s, a8a-s-i, a8a-i-ip]
    active: a8a-ue2-p
    detail: []
    text: The MESSAGE comes in through the I-CSCF and reaches UE 2. The LIR is left out.
  - id: report
    title: The delivery report ends the delivery (A9)
    only: [a8-tfr, a9-ue2-p, a9-p-s, a9-s-ip, a9a-ip-s, a9a-s-p, a9a-p-ue2, a9-tfa]
    active: a9-tfa
    text: UE 2's delivery report reaches the IP-SM-GW, and the TFA says 2001.
```

| Look at | What to see |
| --- | --- |
| MWD-Status of the SRA | Mostly 0: the SC's address is in the MWD, and no flag is set. |
| TFA | 2001. As in step 12, it follows the delivery report. |

- No flag is set, so the SC does not report this delivery to the HSS. It reports one when a flag stays set (D1 to D3).
- If UE 2 sends nothing and only registers again, the IP-SM-GW sends the HSS the same news (C1 to C4 of the guide). The resend is the one of this slide.

> Guide: ["UE 2 takes no short message"](../../../docs/guide/en/sms.md#ue-2-takes-no-short-message) and ["UE 2 only registers again"](../../../docs/guide/en/sms.md#ue-2-only-registers-again). TS 29.338 5.3.3.8. TS 23.040 3.2.6.

---

## UE 2 has no memory left (B1 to B4)

```ims-scene
id: memory
lead: UE 2 with a full memory takes the MESSAGE, then refuses with an RP-ERROR. The HSS sets MCEF, not UNRI.
steps:
  - id: tfr
    title: The delivery starts (B1)
    only: [b1-tfr]
    zoom: [ipsmgw, sc, hss]
    text: "UE 2 is set to `{\"full\": true}` with `PUT /sms-memory`, and UE 1 sends a short message."
  - id: message
    title: UE 2 answers the MESSAGE with 200
    show: [b1-ip-i, b1-i-s, b1-s-p, b1-p-ue2, b1a-ue2-p, b1a-p-s, b1a-s-i, b1a-i-ip]
    active: b1a-ue2-p
    text: The short message gets as far as UE 2. The LIR is left out.
  - id: error
    title: The delivery report is an RP-ERROR (B2)
    only: [b1-tfr, b2-ue2-p, b2-p-s, b2-s-ip, b2a-ip-s, b2a-s-p, b2a-p-ue2]
    active: b2-ue2-p
    checkpoint: true
    text: UE 2 keeps nothing, and now owes the network a notification. The IP-SM-GW answers 202.
  - id: tfa
    title: The IP-SM-GW reports the failure (B3)
    only: [b1-tfr, b3-tfa]
    active: b3-tfa
    zoom: [ipsmgw, sc, hss]
    text: The TFA carries DIAMETER_ERROR_SM_DELIVERY_FAILURE with the cause MEMORY_CAPACITY_EXCEEDED.
  - id: rdr
    title: "The SC reports to the HSS (B4: RDR)"
    show: [b4-rdr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The cause is UE_MEMORY_CAPACITY_EXCEEDED.
  - id: hold
    title: The HSS sets MCEF
    show: [hss-mcef, b4-rda]
    active: hss-mcef
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: The UE is reachable and only has no room, so UNRI is not set.
```

`PUT /sms-memory` with `{"full": true}`, then a short message from UE 1.

| Step | What to see |
| --- | --- |
| B2 | 200, then a delivery report: an RP-ERROR with RP-Cause 22 (memory capacity exceeded), TP-FCS `0xD3`. |
| B3 | DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555) in the TFA, with MEMORY_CAPACITY_EXCEEDED. |
| B4 | An RDR with UE_MEMORY_CAPACITY_EXCEEDED. The HSS sets MCEF. UNRI stays clear: the UE is reachable and has no room. |

> Guide: ["UE 2 has no memory left"](../../../docs/guide/en/sms.md#ue-2-has-no-memory-left). TS 24.011 table 8.4 part 2. TS 23.040 9.2.3.22, 3.2.6. TS 29.338 7.3.8.

---

## The notification of free memory (B5 to B8)

```ims-scene
id: memory-back
lead: When UE 2 has room again, it says so itself. The RP-ACK comes back to UE 2 only after the HSS has taken the news.
steps:
  - id: smma
    title: UE 2 sends an RP-SMMA (B5)
    only: [b5-ue2-p, b5-p-s, b5-s-ip]
    active: b5-ue2-p
    text: "With `{\"full\": false}`, UE 2 sends the notification it owes, in a MESSAGE."
  - id: accepted
    title: The IP-SM-GW answers 202 at once
    show: [b5a-ip-s, b5a-s-p, b5a-p-ue2]
    active: b5a-ip-s
    checkpoint: true
    text: Before the news goes to the HSS. The RP-ACK goes in a MESSAGE of its own.
  - id: ready
    title: "The IP-SM-GW passes it to the HSS (B6: Ready-For-SM)"
    only: [b6-rfsm]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The same command of this project's own as in A6, with Alert-Reason UE_MEMORY_AVAILABLE.
  - id: hss
    title: The HSS clears MCEF and UNRI
    show: [hss-mem]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: It then alerts the SCs in the MWD.
  - id: alert
    title: The HSS answers and alerts the SC (B7)
    hide: [hss-mem]
    show: [b6-ans, b7-alr, b7-ala]
    active: b7-alr
    zoom: [ipsmgw, sc, hss]
    text: As in A7, the answer and the ALR come in either order.
  - id: ack
    title: The RP-ACK reaches UE 2 (B8)
    only: [b6-ans, b8-ip-i, b8-i-s, b8-s-p, b8-p-ue2]
    active: b8-ip-i
    text: |
      The HSS has taken it, so the IP-SM-GW sends the RP-ACK. The UE is never told that news arrived which did not.
      The resend is the one of A8 and A9.
```

| Step | What to see |
| --- | --- |
| B5 | `{"full": false}`: UE 2 sends the notification it owes, an RP-SMMA in a MESSAGE. |
| B6, B7 | A Ready-For-SM with UE_MEMORY_AVAILABLE. The HSS clears MCEF and UNRI, and alerts the SCs of the MWD. |
| B8 | The RP-ACK for the notification goes to UE 2 only after the HSS has taken it. |

The filter is `diameter.Alert-Reason == 1`. The resend is the one of A8 and A9.

- A notification that did not reach the HSS gets an RP-ERROR instead of the RP-ACK, with the cause set for each error of the HSS. The UE keeps the notification as one it still owes.

> Guide: ["UE 2 has no memory left"](../../../docs/guide/en/sms.md#ue-2-has-no-memory-left). TS 24.011 7.3.2. TS 23.204 6.6. TS 23.040 3.2.6, 11.2.

---

## The retry, and the report of a delivery (D1 to D3)

```ims-scene
id: retry
lead: When the RP-SMMA is lost, MCEF stays set. The retry of the SC sends the short message again, and the SC then reports the delivery to the HSS.
steps:
  - id: srr
    title: The SRR at the retry (D1)
    only: [d1-srr]
    zoom: [ipsmgw, sc, hss]
    text: After B1 to B4, UE 2 has room again, but its RP-SMMA was lost. The retry comes after the interval of `retry_delays`.
  - id: sra
    title: The SRA has MCEF-Set
    show: [d1-sra]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: It says that the HSS has MCEF set for the user.
  - id: tfr
    title: The SC sends a TFR (D2)
    show: [d2-tfr]
    zoom: [ipsmgw, sc, hss]
    text: The SIP beyond the IP-SM-GW is the one of A8 and A9, and is left out here.
  - id: tfa
    title: This time UE 2 takes it
    show: [d2-tfa]
    zoom: [ipsmgw, sc, hss]
    text: After the delivery report, the TFA says 2001.
  - id: decide
    title: The SC decides to report the delivery
    show: [sc-report]
    focus: [sc]
    zoom: [ipsmgw, sc, hss]
    text: The delivery succeeded while the HSS had a flag set, so the SC reports it.
  - id: rdr
    title: An RDR with SUCCESSFUL_TRANSFER (D3)
    show: [d3-rdr]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The SC sends this report once.
  - id: clear
    title: The HSS clears MCEF
    show: [hss-success, d3-rda]
    active: hss-success
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: It takes the SC's address out of the MWD. Without D3, MCEF would stay set.
```

An RP-SMMA can be lost. MCEF then stays set, no registration of UE 2 alerts the SC, and only the retry of the SC sends the short message again.

| Step | What to see |
| --- | --- |
| D1 | The SRR at the retry. The SRA names the IP-SM-GW, and its MWD-Status has bit 2, MCEF-Set. |
| D2 | The delivery of A8 and A9. The TFA says 2001. |
| D3 | An RDR with SUCCESSFUL_TRANSFER: the HSS had a flag set, so the SC reports the delivery. The HSS clears MCEF and takes the SC's address out of the MWD. |

- In this network the retry is an hour away. `TestARetryAfterAFullMemoryClearsTheFlag` in `internal/ipsmgw/chain_test.go` runs these steps with a retry after one second.
- After an alert the flags are already clear, so the SRA of A8 has no MCEF-Set and no such RDR follows.

> Guide: ["The notification of free memory is lost"](../../../docs/guide/en/sms.md#the-notification-of-free-memory-is-lost). TS 29.338 5.3.3.8, 5.2.3.1, 5.3.3.19. TS 23.040 10.1, 3.2.6.

---

## When it fails

```ims-scene
id: failures
lead: In both cases the SIP response is 202. The failure travels in the RP and TP layers, and the SC and the HSS decide it.
steps:
  - id: ofr
    title: The number has no subscriber
    only: [f-ofr]
    zoom: [ipsmgw, sc, hss]
    text: UE 1 sends to `+12025550198`. As in step 5, the IP-SM-GW hands it to the SC in an OFR.
  - id: srr
    title: The SC asks the HSS about the number
    show: [f-srr]
    zoom: [ipsmgw, sc, hss]
    text: The SRR of step 5.
  - id: sra
    title: The HSS answers USER_UNKNOWN
    show: [f-sra]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: No subscriber has the number.
  - id: ofa
    title: The SC refuses
    show: [f-ofa]
    zoom: [ipsmgw, sc, hss]
    checkpoint: true
    text: The OFA refuses the short message. No delivery is tried.
  - id: report
    title: The submit report is an RP-ERROR
    only: [f-ofa, frep-ip-i, frep-i-s, frep-s-p, frep-p-ue1]
    active: frep-ip-i
    checkpoint: true
    text: By the way of step 6. The sender learns of the failure at once, without waiting for a status report. The LIR is left out.
  - id: absent
    title: The recipient is not registered
    only: [g-srr, g-sra]
    active: g-sra
    zoom: [ipsmgw, sc, hss]
    text: Subscriber 3, `+12025550103`, exists, so the SC takes the message and the submit report is an RP-ACK. The SRR of the delivery gets 5550.
  - id: hold
    title: The HSS keeps the SC in the MWD
    show: [hss-keep]
    focus: [hss]
    zoom: [ipsmgw, sc, hss]
    text: The SC holds it. The alert of the HSS delivers it when subscriber 3 registers.
```

| Case | How to try it | What UE 1 gets |
| --- | --- | --- |
| The number has no subscriber | `ue sms +12025550198 "Nobody has this number."` | A submit report that is an RP-ERROR: RP-Cause 1, TP-FCS 0xC3. |
| The recipient is not registered | `ue sms -status-report +12025550103 "Are you there?"` | A submit report that is an RP-ACK, and then nothing yet: the SC holds the short message. |

- Put `docker compose run --rm` in front of the command.
- In both, the SIP response is 202: the failure travels in the RP and TP layers.
- In the first the SC refuses: the SRA of step 5 has DIAMETER_ERROR_USER_UNKNOWN (5001), and no delivery is tried.
- In the second the SC accepts. No IP-SM-GW has registered for subscriber 3, so each SRR of the SC gets DIAMETER_ERROR_ABSENT_USER (5550), and the HSS keeps the SC's address in the MWD. The alert of the HSS delivers it when subscriber 3 registers.

The filter is `gsm_a.rp.cause || gsm_sms.tp-fcs || diameter.cmd.code == 8388647`.

> Guide: ["When it fails"](../../../docs/guide/en/sms.md#when-it-fails). TS 24.011 Table 8.4. TS 23.040 9.2.3.22. TS 29.338 5.2.1.3.

---

## Read the body: the RP layer

This is the body of the MESSAGE of step 1.

```text
00 00 00 07 91 21 20 55 05 01 f0 23 21 00 0b 91 21 20 55 05 01 f2 00 00 19 c8 32 9b fd 06 55 8b 2d 21 0b
44 47 a7 e7 a0 f4 1c 54 2d b6 82 2e
```

| Octets | Field | What to see |
| --- | --- | --- |
| `00` | RP-MTI | RP-DATA from the UE to the network. |
| `07 91 21 20 55 05 01 f0` | RP-DA | 7 octets long. The digits are swapped in pairs. The SC's number. |
| `23` | Length of RP-User-Data | The 35 octets that follow are the TPDU. |

- Read it by hand first, then compare with the tree in Wireshark. Turn `21 20 55 05 01 f0` back into 12025550100.
- The Lab shows the same octets under **Body (hex)** in the details of the MESSAGE.

> Guide: ["The relay layer (RP)"](../../../docs/guide/en/sms.md#the-relay-layer-rp), which has the table of all fields. TS 24.011 7.3.1.2, 8.2.

---

## Read the body: the TP layer and the text

| Octets | Field | What to see |
| --- | --- | --- |
| `21` | First octet | TP-MTI 01: SMS-SUBMIT. TP-SRR 1: a status report is wanted. |
| `0b 91 21 20 55 05 01 f2` | TP-DA | The length is 11 digits. The recipient's number. |
| `00` | TP-DCS | The GSM 7 bit default alphabet. |
| `19` | TP-UDL | 25 septets, 25 characters. The packed text follows. |

- RP counts a length in octets, TP in digits: 7 and 11 for the same kind of number.
- TP-MTI 01 is SMS-SUBMIT from a UE, SMS-SUBMIT-REPORT from the network.
- Pack the first two characters, H and e, into `c8` and `32` with the figure of the guide. Japanese goes in UCS2, with TP-DCS `08`: send one from UE 2 and watch TP-DCS and TP-UDL change.

> Guide: ["The transfer layer (TP): SMS-SUBMIT"](../../../docs/guide/en/sms.md#the-transfer-layer-tp-sms-submit) and ["GSM 7 bit packing and UCS2"](../../../docs/guide/en/sms.md#gsm-7-bit-packing-and-ucs2). TS 23.040 9.2.2.2, 9.2.3. TS 23.038 6.1.2.1.1.

---

## Wireshark display filters

Before each filter, guess which of the six MESSAGEs stay.

| Filter | What it finds |
| --- | --- |
| `sip.Content-Type == "application/vnd.3gpp.sms"` | Every MESSAGE that carries a short message or a report. |
| `gsm_a.rp.msg_type == 0x00` | RP-DATA from a UE: the short messages that UEs sent. |
| `gsm_sms.tp-mti == 2` | SMS-STATUS-REPORT. |
| `gsm_sms.sms_text contains "Hello"` | The text, decoded from GSM 7 bit or UCS2. |
| `gsm_sms.tp-da == "12025550102"` | Short messages to that number. |
| `sip.In-Reply-To` | Submit reports and delivery reports. |
| `diameter.cmd.code == 306 \|\| diameter.cmd.code == 307` | The Sh messages: UDR, UDA, PUR, PUA. |
| `diameter.applicationId == 16777313` | SGd, between the IP-SM-GW and the SC. `16777312` is S6c. |
| `diameter.cmd.code == 16777214` | Ready-For-SM, this project's own command, and its answers. |

> Guide: ["Wireshark display filters"](../../../docs/guide/en/sms.md#wireshark-display-filters), and [the capture guide, "Display filters for SMS over IP"](../../../docs/guide/en/capture.md#display-filters-for-sms-over-ip)

---

## Sending from the Lab

```sh
docker compose --profile phones up -d --build
```

1. Fill in the SMS form in the card of `phone1`, tick **status report**, and press **Send SMS**.
2. Under **SMS sent**, `waiting for the submit report` turns into `accepted by the SC`.
3. **SMS received** in the card of `phone2` shows the sender's number and the text.
4. **ladder** in a row draws the ladder of that MESSAGE. Pick an arrow to see the RP and TP fields.

- The Lab draws one ladder per Call-ID, so the six MESSAGEs are six ladders.

> Guide: [Watch the IMS in the Lab, "Drive the UE simulators"](../../../docs/guide/en/lab.md#drive-the-ue-simulators)

---

## Check it in the code

| What you saw | Where to look |
| --- | --- |
| The body: RP, TP, text | `rp.go`, `tp.go`, `text.go` in `internal/sms` |
| The iFCs and their evaluation | `internal/ifc`, `internal/scscf/services.go` |
| The third-party REGISTER | `internal/scscf/thirdparty.go` |
| Matching Accept-Contact | `internal/scscf/callerprefs.go` |
| The IP-SM-GW | `message.go`, `deliver.go`, `regevent.go`, `store.go` in `internal/ipsmgw` |
| The Service Centre | `submit.go`, `deliver.go`, `alert.go`, `store.go` in `internal/smsc` |
| SGd and S6c | `internal/sgd`, `internal/hss/s6c.go` |
| The SC boundary, the SC stub | `internal/ipsmgw/servicecentre.go`, `internal/sc` |
| Sh | `internal/sh`, `internal/hss/sh.go` |
| The UE | `internal/ue/sms.go` |

- Find in `internal/sms/tp.go` the fields you read by hand. `internal/ipsmgw/network_test.go` runs the whole flow in one process.

> Guide: ["Where the code is"](../../../docs/guide/en/sms.md#where-the-code-is) and ["What this project simplifies"](../../../docs/guide/en/sms.md#what-this-project-simplifies)

---

[layout: exercise]

## Looking back

1. Whose address is the Request-URI of UE 1's MESSAGE?
2. How does the S-CSCF know to send that MESSAGE to the IP-SM-GW?
3. What does the 202 Accepted say, and what does the submit report say?
4. Where does a MESSAGE from the IP-SM-GW to UE 2 enter the network?
5. Whose iFC takes the delivery report to the IP-SM-GW?
6. Why is the length of RP-DA 7 and the length of TP-DA 11?
7. UE 2 cannot take a short message. Who keeps it, and what tells that node to try again?

---

[layout: answers]

## Looking back: answers

1. The SC's. A UE does not send a short message to the recipient directly.
2. The MESSAGE matches the criterion of priority 1 of the iFCs that came in the SAA at registration.
3. The 202 says that the IP-SM-GW got the MESSAGE. The submit report says whether the SC accepted the short message.
4. At the I-CSCF, which finds UE 2's S-CSCF with an LIR.
5. UE 2's. The delivery report is a MESSAGE that UE 2 originates.
6. RP counts octets and TP counts digits.
7. The SC keeps it, and the HSS keeps the SC's address in the MWD. The HSS alerts the SC with an ALR when the IP-SM-GW reports that UE 2 is back; without an alert, the retry of the SC tries again.

---

## Summing up

These six sessions cover the core of the IMS: registration, calls and short messages.

| Session | Subject | What to read |
| --- | --- | --- |
| 1 | Package layout | [README.md](../../../README.md) |
| 2 | The map of the network | [docs/guide/en/README.md](../../../docs/guide/en/README.md) |
| 3 | Captures and the Lab | [docs/guide/en/capture.md](../../../docs/guide/en/capture.md) |
| 4 | Registration | [docs/guide/en/registration.md](../../../docs/guide/en/registration.md) |
| 5 | Call | [docs/guide/en/call.md](../../../docs/guide/en/call.md) |
| 6 | SMS | [docs/guide/en/sms.md](../../../docs/guide/en/sms.md) |

- Next come the supplementary services of the MMTel AS ([mmtel.md](../../../docs/guide/en/mmtel.md)), then the VoLTE call, which builds on them ([volte.md](../../../docs/guide/en/volte.md)).

```sh
docker compose --profile ue --profile phones down --rmi all -v --remove-orphans
```
