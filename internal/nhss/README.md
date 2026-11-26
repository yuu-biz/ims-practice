# The service based interface of the HSS

[日本語](README.ja.md)

This package is the IMS part of the service based interface of the HSS, TS 29.562: Nhss_imsUEContextManagement (UECM), Nhss_imsUEAuthentication (UEAU) and Nhss_imsSubscriberDataManagement (SDM). The I-CSCF and the S-CSCF use them over N70 instead of Cx, and an AS such as the IP-SM-GW over N71 instead of Sh (TS 23.228 AA.1.2). The package comment maps each Cx and Sh procedure of this project onto the operations here, as TS 23.228 Tables AA.2.2.2-1 and AA.2.2.3-1 do.

No node of this project runs it: no binary in `cmd` builds it in. The tests show both sides:

```sh
go test ./internal/nhss
go doc ./internal/nhss
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `nhss`, TS 29.562 Annex A | The operations that the package comment does not list, among them restoration, IMS AS registration, the subscriptions to data changes (the counterpart of PPR/PPA) and the other SDM resources, and the attributes of the listed operations that their procedures do not need. | The Cx and Sh procedures that this project uses map onto the listed operations alone, and decoding ignores attributes it does not know, so a fuller peer can still talk to this one. |
| `ScscfRegistration`, TS 29.562 6.1.6.2.10 | The attributes of multiple registration, restoration, reselection, wildcards and loose routes. `scscfInstanceId`, which the table makes mandatory and the OpenAPI file does not, is sent only when the caller has one. | Without an NRF the S-CSCF has no NF instance ID. |
| `ImsProfileData`, TS 29.562 6.2.6.2.4 | Charging information, service level trace, priority levels and the limit of simultaneous registrations. | The S-CSCF of this project uses none of them. |
| `Ifcs`, TS 29.562 6.2.6.2.14 | `cscfFilterSetIdList`, the shared iFC sets. | The HSS stub sends complete criteria. |
| `IpSmGwAddress`, TS 29.562 6.2.6.2.44 | The Diameter identity and realm of the IP-SM-GW for S6c, its SBI support indication and its NF instance ID. | The SMS registration that the IP-SM-GW writes over Sh holds only its number. |
| `Client.GetProfileData`, TS 29.562 6.2.3.5.3.1 | The `dataset-names` and `supported-features` query parameters. The whole profile comes back. | The SAA that this operation stands for brings the whole user profile too. |
| `Client.GetMsisdns`, TS 29.562 6.2.3.3.3.1 | The `private-id` query parameter, which asks for the C-MSISDN of one of several private identities. | The Sh read that this operation stands for carries no private identity: the IP-SM-GW does not send one. |
| `SipAuthenticationInfoResult`, TS 29.562 6.3.6.2.3 | `lineIdentifierList` and `ipAddress`, for NBA and GIBA. | This project authenticates with IMS-AKA and SIP Digest only. |
