# The 5GC stub

[日本語](README.ja.md)

This package is the stub of the 5GS access network: one node that stands for the AMF, the SMF and the UPF of a 5GC, with the PCF stub inside it. It is to 5GS what package `epc` is to EPS. A UE registers and sets up its PDU session over an HTTP and JSON API whose messages are named after the NAS messages of TS 24.501. The stub gives it its static IP address and the P-CSCF addresses, and keeps the QoS flow of the default QoS rule for SIP (5QI 5). The PCF serves the P-CSCF over N5 (package `n5`); when it authorises the media of a call, the stub adds a GBR QoS flow for voice (5QI 1) and tells the UE on its event stream.

No node of this project runs it: no binary in `cmd` builds it in. The tests and the example show it at work:

```sh
go test ./internal/fivegc
go doc ./internal/fivegc
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `fivegc`, TS 23.502 4.3.2.2.1 | The AMF, the SMF, the UPF and the PCF are one node, so N11, N4 and N7 are Go calls over one Store. There is no NRF and no UDM: the stub serves the subscribers of its configuration, and the AF is given the PCF's apiRoot in its configuration. There is no user plane: N3 and N6 are the one Docker network. | A learner of the IMS watches the signalling between the UE, the P-CSCF and the PCF, which these interfaces do not carry. |
| The UE API, TS 24.501 and TS 38.413 | NAS encoding, NGAP and the radio. The messages travel as HTTP and JSON, and the IMSI in the URL stands for the UE-associated connection on N2 and for the SUCI of REGISTRATION REQUEST. Security (5G-AKA, security mode control), the 5G-GUTI, network slicing (NSSAI and S-NSSAI), mobility and periodic registration updating, and the timers of the network side are left out. | A learner reads the procedures, not the encoding. The UE reaches the stub through its access network interface, so an implementation that speaks real NAS can stand in its place. |
| `RegistrationResult`, TS 24.501 9.11.3.6 | SMS over NAS. The "SMS over NAS transport allowed" bit is never set. | A UE of this stub then sends its short messages over IP through the IP-SM-GW. |
| `PDUSessionEstablishmentRequest`, TS 24.501 8.3.1 | The Integrity protection maximum data rate IE. | There is no user plane to protect. |
| `SystemInformation`, TS 38.331 | The broadcast of system information. The stub serves it at an HTTP path instead. | There is no radio to broadcast it on. |
| `Subscriber`, TS 24.501 6.2.4.2 | An address pool and the UPF. Each subscriber has a static address, which the SMF gives in the PDU address IE. | The UE's container has the same address on the Docker network, so its packets reach the IMS directly, with no tunnel and no network setting of the host. |
| `FiveGC.Register`, TS 23.502 4.2.2.2.2 | Authentication (5G-AKA with the AUSF and the UDM), NAS security, the UE context in the UDM, the 5G-GUTI, and with it REGISTRATION COMPLETE (TS 24.501 5.5.1.2.4). REGISTRATION REJECT is not sent: a request the stub cannot serve gets an error from the API. | The stub serves the IMSIs of its configuration. |
| `FiveGC.DeregistrationAccepted`, TS 24.501 5.5.2.3 | Timer T3522, and the deregistration request sent again when it expires. | The UE API runs over TCP, which loses no message. |
| `FiveGC.EstablishPDUSession`, TS 24.501 6.4.1 | More than one PDU session per UE, types other than IPv4, SSC modes other than 1, and a default DNN. PDU SESSION ESTABLISHMENT REJECT is not sent: a request the stub cannot serve gets an error from the API. | The UE must name the DNN of the configuration, as there is no subscription to take a default DNN from. |
| `FiveGC.PDUSessionReleaseCompleted`, TS 24.501 6.3.3.2 | The state PDU SESSION INACTIVE PENDING, and timer T3592. The network forgets the session when it sends PDU SESSION RELEASE COMMAND. | The UE API runs over TCP, which loses no message. |
| SMF and PCF, TS 29.512 | Npcf_SMPolicyControl (Create at establishment, UpdateNotify with PCC rules, Update with rule reports, Delete at release). The PCF and the SMF share the PDU session in the Store instead. | The stub has one PCF and one SMF, and N7 would add messages a learner of the IMS does not need, as Gx does in the EPC stub. |
| `sessionAMBR`, TS 23.501 5.7.2.7 | The subscribed Session-AMBR. Every session gets the same value, and the stub enforces no rate. | The stub has no UDM to take the subscribed value from. |
| `FiveGC.reconcileVoiceQoSFlow`, TS 24.501 6.3.2.2 | PDU SESSION MODIFICATION COMMAND when the rules change while the flow exists, and with it the wait for one command to be answered before the next, and timer T3591. The stub updates only its own record of the QoS rule and the QoS. | The stub enforces neither, and a UE only needs to know that the flow is there. |
| `voiceQoSFlow`, TS 23.501 5.7.1.2 | The QoS profile for the NG-RAN, with its ARP. The stub enforces neither rate. | There is no RAN to admit the flow. |
| PCF, TS 29.513 8.4 | The registration of the PDU sessions with a BSF (TS 29.521). The AF is given the PCF's apiRoot. | The stub has one PCF. |
| `pcf.Create`, TS 29.513 6.2 | IPv6 addresses. A context bound by an IPv6 address finds no PDU session. | The stub serves IPv4 PDU sessions only. |
| `pcf.Update`, TS 29.514 4.2.3.2 and RFC 7396 | The merge of a media component attribute by attribute. A media component in the patch replaces the one of the same number as a whole. | The P-CSCF of this project derives every component from the whole SDP each time. |
| `pcf.Delete`, TS 29.514 4.2.4.2 | The events asked for in the Delete. The answer is always 204. | Package `n5` names only the events of resource allocation, which have nothing left to report once the resources of the context are freed. |
| `pccRules`, TS 29.513 Table 7.3.3-1 | Media other than audio. Audio is authorised with 5QI 1, which the table gives audio when no operator policy applies, and which its NOTE 14 asks for IMS services where SRVCC is enabled. | The stub has no QoS flow for other media, and refuses them. |
| `maxDataRates`, TS 29.513 Table 7.3.3-1 | The branches the table leaves to operator policy, to specific algorithms for codecs and applications, and to a rate "as set by the operator". They give 0. | The stub has no operator policy to take them from. |
| `parseFDesc`, TS 29.214 5.3.8 | IPv6 addresses in a flow description. | The stub serves IPv4 PDU sessions only. |
| `packetFilter`, TS 24.501 9.11.4.13 | Filters for both directions, and the local address. Each flow becomes a filter of its own direction. | The UE has only the one address of the PDU session. |
