# The HTTP APIs

[日本語](../ja/apis.md)

Besides SIP and Diameter, the nodes of this project talk HTTP. Some of these APIs follow a 3GPP specification, which defines them in OpenAPI files, and a real network has them between its nodes. The others are this project's own: they operate the network or drive the learning tools, as the interfaces of a product of its own do. This page lists both, so that you can tell a standard API from one that only this project has.

## The APIs that follow a specification

Ut is XCAP, an XML API. For the APIs of the service based interface (SBI) below it, the OpenAPI file of the specification is the definition. The repository does not hold it: it comes in the archive of the specification on the 3GPP file server. The package comment of each package names the specification, its version, the OpenAPI file, and which operations of the file the package builds. The JSON types carry the attribute names of the file, and a test compares the names of each type with a list taken from the schema of the same name in the file.

| API | What uses it | Specification | Code |
| --- | --- | --- | --- |
| Ut, XCAP for the supplementary services | The UE simulator reads and changes its settings, and the MMTel AS serves them | TS 24.623 (XCAP, RFC 4825) | [`internal/xcap`](../../../internal/xcap/doc.go) |
| Npcf_PolicyAuthorization, the N5 reference point | The P-CSCF asks the PCF for the media of a call in 5GS, the counterpart of Rx | TS 29.514, `TS29514_Npcf_PolicyAuthorization.yaml` | Package `n5` |
| Nhss_imsUEContextManagement, Nhss_imsUEAuthentication, Nhss_imsSubscriberDataManagement, the N70 and N71 reference points | The I-CSCF and the S-CSCF ask the HSS over N70 instead of Cx, and an AS over N71 instead of Sh | TS 29.562, `TS29562_Nhss_imsUECM.yaml`, `TS29562_Nhss_imsUEAU.yaml`, `TS29562_Nhss_imsSDM.yaml` | Package `nhss` |
| What every SBI shares: HTTP/2, JSON and the error answers | Packages `n5` and `nhss` | TS 29.500, and ProblemDetails of TS 29.571 (`TS29571_CommonData.yaml`) | Package `sbi` |

Of these, only Ut runs in the Compose project. The other three are [code that no node runs](README.md#code-that-no-node-runs), so they do not appear in the capture. Their packages are under `internal`.

## This project's own APIs

3GPP leaves some interfaces to each product, such as the one between an element manager and a network element (TS 32.600 4.2.1.3), and the provisioning interface of an HSS. Others exist only because this is a lab: the control API of a test UE, or the Lab itself. For each of these APIs the repository has an OpenAPI 3.1 file next to the code, and the tests of the package send every request through a checker that compares the request and the answer with the file. A route, a field or a status code that the handler and the file do not share fails the tests, so the file says what the code does.

| API | Who serves it | OpenAPI file |
| --- | --- | --- |
| The maintenance API of a node: the state of its roles, the drain, the configuration, the level of the log and the alarm list | Each node, on its maintenance listener | [`internal/maintenance/openapi.yaml`](../../../internal/maintenance/openapi.yaml) |
| The OAM API: every node together, the configuration history, the subscriptions, the test users and the backups | The `oam` role | [`internal/oam/openapi.yaml`](../../../internal/oam/openapi.yaml) |
| The provisioning API of the HSS | The `hss` role, on its provisioning listener | [`internal/hss/openapi.yaml`](../../../internal/hss/openapi.yaml) |
| The Lab API: the messages, calls and registrations of the HEP copies, and the routes that the Lab passes on to the HSS, the EPC stub, the fault agent and the UE simulators | The `lab` role | [`internal/lab/openapi.yaml`](../../../internal/lab/openapi.yaml) |
| The control API of the UE simulator | `ue serve` | [`internal/ue/openapi.yaml`](../../../internal/ue/openapi.yaml) |
| The control API of the media relay, each call of which stands for an H.248 command | The IMS-AGW, the TrGW and the MRFP | [`internal/agw/openapi.yaml`](../../../internal/agw/openapi.yaml) |

The access stubs and the fault agent have APIs of their own too, described in the comments of their packages: the API of the EPC stub, whose requests are named after the NAS messages of TS 24.301 ([`internal/epc`](../../../internal/epc/http.go)), that of the 5GC stub, named after those of TS 24.501 (package `fivegc`, which no node runs), and that of the fault agent ([`internal/fault`](../../../internal/fault/http.go)).

Subscription management has a standard interface towards the OSS, but its information model has no attribute for the long-term key, OPc or the SIP Digest password of a subscription. [Follow a UE registering with the IMS](registration.md) tells more, and why the provisioning API of the HSS is this project's own.

## What the maintenance API leaves out

The alarm list of the maintenance API follows TS 28.111, with these parts left out. Each has a `SIMPLIFIED:` comment in package `maintenance`.

| Where | What is left out | Why |
| --- | --- | --- |
| Alarm list, TS 28.111 7.3.1.3 and 7.4.1 | The security alarm types. | A security alarm names its service user, who would be a subscriber. An alarm here never names one, so that the list does not grow with the subscribers. |
| Alarm list, TS 28.111 7.3.1.2 | Of the attributes of an alarm record, notificationId and most of the optional ones, such as backedUpStatus, trendIndication, the correlation attributes and the attributes of who cleared an alarm. alarmChangedTime, specificProblem, additionalText and the acknowledgement attributes are kept. | The node sends no notifications: the OAM server reads the list when it is asked. Every alarm here is cleared by the node that raised it. |
| Alarm list, TS 28.111 7.3.2.2 and 6.13 | The operationalState, the administrativeState and the unreliableAlarmScope of the list. | The list works whenever the node answers, so its operational state would always be `ENABLED`. `dropped_records` says instead when the list stopped holding every alarm. |
| Alarm list, TS 28.111 6.3 and 7.4.1 | `object_instance` only looks like a Distinguished Name. Its classes are the generic ManagedElement and ManagedFunction of TS 28.622, with Peer, which is no class of a network resource model, and the values are not escaped. | The node is no managed object of a network resource model here, and not every role has an IMS class of TS 28.705. |
| Alarm list, TS 28.111 6.14 | A cleared alarm leaves the list after a retention time, also when nobody has acknowledged it. | A list that no operator watches still forgets the faults that are over. |
