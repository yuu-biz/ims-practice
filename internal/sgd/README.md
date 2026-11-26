# SGd and S6c

[日本語](README.ja.md)

This package builds and reads the Diameter messages of TS 29.338 that move a short message between the SC and the nodes that serve a UE. On SGd these are MO-Forward-Short-Message (OFR/OFA) and MT-Forward-Short-Message (TFR/TFA). On S6c they are Send-Routing-Info-for-SM (SRR/SRA), Report-SM-Delivery-Status (RDR/RDA) and Alert-Service-Centre (ALR/ALA). S6c also carries Ready-For-SM, a command of this project's own with which the IP-SM-GW tells the HSS that a user can take short messages again; a network sends that notice in MAP-READY-FOR-SM (TS 29.002 12.4.1), which this project does not implement.

The Service Centre of package `smsc` works on the structs of this package. No node of this project runs either of them. The tests and the example build each message and read it back:

```sh
go test ./internal/sgd
go doc ./internal/sgd
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `sgd`, TS 29.338 5.3.2, 6.3.2 | The optional AVPs that no struct names. On SGd they serve priority services, SMS without MSISDN, the cell of the UE, the SGSN, machine-type communication, and a serving node that holds a message or asks the SC to send it again. On S6c they serve priority services, a user without MSISDN, the RP message type and the originating SME address, a second serving node, non-3GPP access, the VLR, and why the HSS has stored the user as absent. The package comment names each AVP. | The nodes that use this package send none of them and act on none of them. |
| `AlertServiceCentreRequest`, TS 29.338 5.3.2.5, 6.2.3 | Alerts from any node but the HSS, and the AVPs SMSMI-Correlation-ID, Maximum-UE-Availability-Time and SMS-GMSC-Alert-Event with Serving-Node. | They are for SMS without MSISDN and for UEs in a power saving mode, which an MME alerts directly on SGd. This project has no such UE. |
| `SRRFlags`, TS 29.338 Table 5.3.3.4/1 | The GPRS-Indicator bit, with which an SMS-GMSC asks for two serving nodes, and SM-RP-PRI, with which it asks for a delivery although the message waiting data already holds its address. Only Single-Attempt-Delivery is read. | The HSS of this project keeps one serving node and does not read the priority. |
| `MWDStatus`, TS 29.338 Table 5.3.3.8/1 | Bits 4 and 5, with which the HSS waits for a serving node of 5G, have no name. The HSS of this project sends the AVP only in a successful SRA, and not with DIAMETER_ERROR_ABSENT_USER. | The HSS names no serving node of 5G, and the SC reads the AVP only after a delivery that succeeded, which a successful SRA came before. |
| `SMDeliveryOutcome`, TS 29.338 5.3.3.14 | The outcomes of a delivery through an MSC, an SGSN or an SMSF for non-3GPP access. | This project has none of these nodes. |
| `ReportSMDeliveryStatusRequest`, TS 29.338 5.3.2.7 | Supported-Features, SMSMI-Correlation-ID, and the serving nodes through which the delivery failed. | S6c defines no feature for RDR. This project has no SMS without MSISDN, and its HSS keeps one serving node of each kind. |
| `ReportSMDeliveryStatusAnswer`, TS 29.338 5.3.2.8 | Supported-Features, and the serving nodes of the alert (Serving-Node, Additional-Serving-Node and the SMSF addresses), with which the HSS tells the SMS-GMSC to try again at once through a node it did not know of. | The HSS of this project alerts the SC with ALR instead. |
| `ServingNode`, TS 29.338 5.3.3.6 | The SGSN and the MSC, and the IP-SM-GW-SBI-Support-Indicator. | This project has no SGSN and no MSC, and reaches the IP-SM-GW over Diameter only. |
| `SMSF3GPPAddress`, TS 29.338 5.3.3.35, 5.3.3.36 | The SMSF-3GPP-SBI-Support-Indicator, and the whole SMSF-Non-3GPP-Address. | This project reaches the SMSF over Diameter only and has no non-3GPP access. |
| `ReadyForSMRequest`, TS 23.204 6.5b step 2d and 6.6 step 3, TS 29.002 12.4.1 | MAP-READY-FOR-SM, with which an IP-SM-GW tells the HLR that a user is reachable for short messages again or has memory for them again. Ready-For-SM, a command of this project's own on S6c with the experimental command code 16777214 (RFC 6733 11.2.1), carries the two notices instead. It names the user by MSISDN rather than by IMSI, gives the reason in the Alert-Reason AVP of TS 29.272 7.3.83 and the IP-SM-GW in IP-SM-GW-Number, and leaves out the Additional Alert Reason Indicator and the Maximum UE Availability Time. | This project does not implement MAP, and neither S6c nor Sh has a command for these notices. The experimental code keeps the command from passing for one of TS 29.338: Wireshark shows it as an unknown command, and an HSS without it answers DIAMETER_COMMAND_UNSUPPORTED. No form of such a command keeps to RFC 6733: a command added to S6c goes against 1.3.4, and a new application against 2.4, since IANA keeps no Application-Id for experiments; the experimental command code is the one that no real command can have. |
| `UserIdentifier`, TS 29.336 6.4.2 | The External-Identifier and the LMSI. | They belong to machine-type communication and to the VLR, which this project does not have. |
