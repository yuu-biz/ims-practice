---
title: A short message through the IMS
footer: ims-practice study session 6. SMS
lang: en
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

## R1 to R5. The IP-SM-GW learns about the user

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

## 1 to 4. UE 1 submits the short message

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

## 5 to 7. The SC accepts, and the submit report goes back

| Look at | What to see |
| --- | --- |
| OFR and OFA | The IP-SM-GW hands the SMS-SUBMIT to the Service Centre over SGd, and the answer carries the submit report. |
| SRR and SRA | The Service Centre asks the HSS over S6c whether anyone has the recipient's number. |
| Request-URI of the IP-SM-GW's MESSAGE | UE 1's SIP URI. |
| Accept-Contact | `*;+g.3gpp.smsip;require;explicit`. |
| In-Reply-To | The Call-ID of the MESSAGE of step 1. |
| Body | RP-ACK with an SMS-SUBMIT-REPORT inside. |
| The way | In through the I-CSCF, which finds UE 1's S-CSCF with an LIR. |

The filter is `diameter.cmd.code in {8388645, 8388647} || sip.In-Reply-To`.

> Guide: [5](../../../docs/guide/en/sms.md#5-the-ip-sm-gw-hands-the-short-message-to-the-sc-ofr-srr-and-ofa) to [7](../../../docs/guide/en/sms.md#7-ue-1-answers-200). TS 24.341 5.3.3.4.3, 5.3.1.2. TS 29.328 7.6.2. TS 24.229 5.7.3.

---

## 8 to 10. The SC delivers

| Look at | What to see |
| --- | --- |
| TFR | The Service Centre forwards the SMS-DELIVER to the IP-SM-GW over SGd, naming the recipient by IMSI. |
| Request-URI | `tel:+12025550102`, the tel URI of that IMSI. |
| Request-Disposition | `no-fork`. |
| Body | RP-DATA from the network, with an SMS-DELIVER. TP-OA is the sender. |
| iFC of priority 2 | No match: P-Asserted-Identity is the IP-SM-GW. |
| What the S-CSCF does | Matches Accept-Contact against the Contact that UE 2 registered. |
| Answer of UE 2 | 200. It only says that the MESSAGE arrived. |

- A UE that did not register `+g.3gpp.smsip` does not get the MESSAGE: the S-CSCF answers 480. The feature tag of the registration is used here.
- Compare the SMS-SUBMIT with the SMS-DELIVER: TP-DA became TP-OA, and TP-SCTS is new.

> Guide: [8](../../../docs/guide/en/sms.md#8-the-sc-forwards-the-message-and-the-ip-sm-gw-delivers-it-tfr-and-sms-deliver) to [10](../../../docs/guide/en/sms.md#10-ue-2-answers-200). TS 24.341 5.3.3.4.2, 5.3.2.3. TS 24.229 5.4.3.3. RFC 3841 7.2.4, 9.1.

---

## 11 and 12. The delivery report

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

## 13 to 15. The status report

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

## A1 to A4. UE 2 takes no short message, and the SC holds it

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

## A5 to A9. UE 2 is back: Ready-For-SM, the alert and the resend

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

## B1 to B8. UE 2 has no memory left

`PUT /sms-memory` with `{"full": true}`, then a short message from UE 1.

| Step | What to see |
| --- | --- |
| B2 | 200, then a delivery report: an RP-ERROR with RP-Cause 22 (memory capacity exceeded), TP-FCS `0xD3`. |
| B3 | DIAMETER_ERROR_SM_DELIVERY_FAILURE (5555) in the TFA, with MEMORY_CAPACITY_EXCEEDED. |
| B4 | An RDR with UE_MEMORY_CAPACITY_EXCEEDED. The HSS sets MCEF. UNRI stays clear: the UE is reachable and has no room. |
| B5 | `{"full": false}`: UE 2 sends the notification it owes, an RP-SMMA in a MESSAGE. |
| B6, B7 | A Ready-For-SM with UE_MEMORY_AVAILABLE. The HSS clears MCEF and UNRI, and alerts the SCs of the MWD. |
| B8 | The RP-ACK for the notification goes to UE 2 only after the HSS has taken it. |

The filter is `diameter.Alert-Reason == 1`. The resend is the one of A8 and A9.

> Guide: ["UE 2 has no memory left"](../../../docs/guide/en/sms.md#ue-2-has-no-memory-left). TS 24.011 table 8.4 part 2, 7.3.2. TS 23.040 9.2.3.22, 3.2.6. TS 29.338 7.3.8. TS 23.204 6.6.

---

## D1 to D3. The retry, and the report of a delivery

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
