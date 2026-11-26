# N5: Npcf_PolicyAuthorization

[日本語](README.ja.md)

This package is the Npcf_PolicyAuthorization service of TS 29.514: the N5 reference point between an AF, such as the P-CSCF, and the PCF of a 5GC. It is the 5GS counterpart of Rx (package `rx`). The AF creates an Individual Application Session Context with the service information of an IMS session, updates it when the SDP changes, and deletes it when the session ends; the PCF notifies the AF of events and asks it to end a context that is no longer valid (TS 29.514 4.2.1). The JSON types carry the attribute names of the OpenAPI file of TS 29.514 Annex A.2, so that `ascReqData` in a capture is `AscReqData` here.

No node of this project runs it: no binary in `cmd` builds it in. The PCF of the 5GC stub (package `fivegc`) serves it, and the tests show both sides:

```sh
go test ./internal/n5
go doc ./internal/n5
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `n5`, TS 29.514 Annex A.2 | The attributes that neither the P-CSCF procedures of Annex B nor the signalling path status of 4.2.6.7 use, such as those of traffic routing, TSC, QoS monitoring, sponsored data, MPS and MCPTT, and the operations that the package comment lists as not built. | The P-CSCF creates, updates and deletes a context and takes the notifications of its events, and uses nothing else. Decoding ignores attributes it does not know, as every SBI receiver must for an extensible API, so a fuller peer can still talk to this one. |
| `Client`, TS 29.513 8.4 | The BSF, which finds the PCF that serves the PDU session of a UE. The client sends to the one PCF it is given. | The 5GC stub has one PCF. |
| `NewHandler`, TS 29.514 5.3 | The GET of a context, the PUT and DELETE of the Events Subscription sub-resource (the Subscribe and Unsubscribe operations), and the PcscfRestoration custom operation. They are answered with 404 or 405. | The P-CSCF subscribes within Create and Update, and P-CSCF restoration is not in this project. |
| `AppSessionContextReqData`, TS 29.514 5.6.2.3 | `ueMac`, for Ethernet PDU sessions. | IMS runs over IP PDU sessions. |
| `AppSessionContextRespData`, TS 29.514 5.6.2.4 | `servAuthInfo`, `directNotifReports` and `ueIds`. | They serve background data transfer, QoS monitoring and the exposure of 5GS UE identities, which IMS does not use here. |
| The "Rm" types, TS 29.514 5.6.2.25 to 5.6.2.27 | The "Rm" variants are the same Go types as the plain ones, so an attribute cannot be null in a PATCH. An omitted attribute keeps its earlier value. | A JSON Merge Patch removes an attribute with null, and the P-CSCF never removes one. |
| `EventsSubscReqData`, TS 29.514 5.6.2.6 | The thresholds and parameters of usage, QoS monitoring and access network reports. | The package names only the events of resource allocation, which the IMS procedures use and which take none of them. |
| `EventsNotification`, TS 29.514 5.6.2.9 | The access network, location, charging, QoS monitoring and usage reports. | The package keeps only the reports of resource allocation, the events it names. |
| `AfEventSubscription`, TS 29.514 5.6.2.10 | `notifMethod`, `repPeriod` and `waitTime`. | Without `notifMethod` the PCF reports on event detection, which is what IMS needs. |
