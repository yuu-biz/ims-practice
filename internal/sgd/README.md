# SGd and S6c

[日本語](README.ja.md)

This package builds and reads the Diameter messages of TS 29.338 that move a short message between the SC and the nodes that serve a UE. On SGd these are MO-Forward-Short-Message (OFR/OFA) and MT-Forward-Short-Message (TFR/TFA). On S6c they are Send-Routing-Info-for-SM (SRR/SRA), Report-SM-Delivery-Status (RDR/RDA) and Alert-Service-Centre (ALR/ALA).

The Service Centre of package `smsc` works on the structs of this package. No node of this project runs either of them. The tests and the example build each message and read it back:

```sh
go test ./internal/sgd
go doc ./internal/sgd
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `sgd`, TS 29.338 5.3.2, 6.3.2 | The optional AVPs that no struct names. On SGd they serve priority services, SMS without MSISDN, the cell of the UE, the SGSN, machine-type communication, and a serving node that holds a message or asks the SC to send it again. On S6c they serve priority services, a user without MSISDN, the RP message type and the originating SME address, a second serving node, non-3GPP access, the VLR, and what the HSS has stored about earlier deliveries that failed. The package comment names each AVP. | The nodes that use this package send none of them and act on none of them. |
| `AlertServiceCentreRequest`, TS 29.338 5.3.2.5, 6.2.3 | Alerts from any node but the HSS, and the AVPs SMSMI-Correlation-ID, Maximum-UE-Availability-Time and SMS-GMSC-Alert-Event with Serving-Node. | They are for SMS without MSISDN and for UEs in a power saving mode, which an MME alerts directly on SGd. This project has no such UE. |
| `SRRFlags`, TS 29.338 Table 5.3.3.4/1 | The GPRS-Indicator bit, with which an SMS-GMSC asks for two serving nodes, and SM-RP-PRI, with which it asks for a delivery although the message waiting data already holds its address. Only Single-Attempt-Delivery is read. | The HSS of this project keeps one serving node and does not read the priority. |
| `SMDeliveryOutcome`, TS 29.338 5.3.3.14 | The outcomes of a delivery through an MSC, an SGSN or an SMSF for non-3GPP access. | This project has none of these nodes. |
| `ReportSMDeliveryStatusRequest`, TS 29.338 5.3.2.7 | Supported-Features, SMSMI-Correlation-ID, and the serving nodes through which the delivery failed. | S6c defines no feature for RDR. This project has no SMS without MSISDN, and its HSS keeps one serving node of each kind. |
| `ReportSMDeliveryStatusAnswer`, TS 29.338 5.3.2.8 | Supported-Features, and the serving nodes of the alert (Serving-Node, Additional-Serving-Node and the SMSF addresses), with which the HSS tells the SMS-GMSC to try again at once through a node it did not know of. | The HSS of this project alerts the SC with ALR instead. |
| `ServingNode`, TS 29.338 5.3.3.6 | The SGSN and the MSC, and the IP-SM-GW-SBI-Support-Indicator. | This project has no SGSN and no MSC, and reaches the IP-SM-GW over Diameter only. |
| `SMSF3GPPAddress`, TS 29.338 5.3.3.35, 5.3.3.36 | The SMSF-3GPP-SBI-Support-Indicator, and the whole SMSF-Non-3GPP-Address. | This project reaches the SMSF over Diameter only and has no non-3GPP access. |
| `UserIdentifier`, TS 29.336 6.4.2 | The External-Identifier and the LMSI. | They belong to machine-type communication and to the VLR, which this project does not have. |
