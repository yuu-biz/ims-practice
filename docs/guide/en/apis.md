# The HTTP APIs

[日本語](../ja/apis.md)

Besides SIP and Diameter, the nodes of this project talk HTTP. Some of these APIs follow a 3GPP specification, which defines them in OpenAPI files, and a real network has them between its nodes. The others are this project's own: they operate the network or drive the learning tools, as the interfaces of a product of its own do. This page lists both, so that you can tell a standard API from one that only this project has.

## The APIs that follow a specification

Ut is XCAP, an XML API. For the other APIs below it, the OpenAPI file of the specification is the definition. The repository does not hold it: it comes in the archive of the specification on the 3GPP file server. The package comment of each package names the specification, its version, the OpenAPI file, and which operations of the file the package builds. The JSON types carry the attribute names of the file, and a test compares the names of each type with a list taken from the schema of the same name in the file.

| API | What uses it | Specification | Code |
| --- | --- | --- | --- |
| Ut, XCAP for the supplementary services | The UE simulator reads and changes its settings, and the MMTel AS serves them | TS 24.623 (XCAP, RFC 4825) | [`internal/xcap`](../../../internal/xcap/doc.go) |
| Npcf_PolicyAuthorization, the N5 reference point | The P-CSCF asks the PCF for the media of a call in 5GS, the counterpart of Rx | TS 29.514, `TS29514_Npcf_PolicyAuthorization.yaml` | Package `n5` |
| Nhss_imsUEContextManagement, Nhss_imsUEAuthentication, Nhss_imsSubscriberDataManagement, the N70 and N71 reference points | The I-CSCF and the S-CSCF ask the HSS over N70 instead of Cx, and an AS over N71 instead of Sh | TS 29.562, `TS29562_Nhss_imsUECM.yaml`, `TS29562_Nhss_imsUEAU.yaml`, `TS29562_Nhss_imsSDM.yaml` | Package `nhss` |
| What every SBI shares: HTTP/2, JSON and the error answers | Packages `n5` and `nhss` | TS 29.500, and ProblemDetails of TS 29.571 (`TS29571_CommonData.yaml`) | Package `sbi` |
| Fault supervision towards the OSS: the AlarmList of the SubNetwork, read and acknowledged with the generic provisioning operations (ProvMnS), and the subscriptions to its notifications (NtfSubscriptionControl) | The `oam` role serves it, and the OSS stand-in `oss` uses it ([Hand the alarms to an OSS](oss.md)) | TS 28.111 V20.0.0 and TS 28.532 V20.2.0, over the REST rules of TS 32.158 V20.1.0: `TS28111_FaultNrm.yaml` and `TS28111_FaultNotifications.yaml` 19.3.0, `TS28532_ProvMnS.yaml` 19.2.0, `TS28623_SubscriptionControlNrm.yaml` 19.4.0 | Package `mns`, and [`internal/oam`](../../../internal/oam/doc.go) |

Of these, Ut and fault supervision run in the Compose project. The other three are [code that no node runs](README.md#code-that-no-node-runs), so they do not appear in the capture. Their packages are under `internal`.

Fault supervision is the interface of the OAM towards the OSS, Itf-N. The OAM serves it beside its own API, under `{MnSRoot}`, which is `http://oam:8080/3GPPManagement` in the Compose project. The paths and the operations are these:

| Method and path | Operation (TS 28.532) | Answer |
| --- | --- | --- |
| `GET {MnSRoot}/ProvMnS/v19/SubNetwork=ims/AlarmList=1`, with `filter`, `dataNodeSelector`, `fields` or `scope` in the query | `getMOIAttributes` (12.1.1.1.3) | 200, the AlarmList with its alarm records |
| `PATCH {MnSRoot}/ProvMnS/v19/SubNetwork=ims/AlarmList=1`, `application/merge-patch+json` | `modifyMOIAttributes` (12.1.1.1.4): acknowledge, or take the acknowledgement back | 204 |
| `PUT {MnSRoot}/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl={id}` | `createMOI` (12.1.1.1.2.1): subscribe | 201 with `Location`, or 200 for a subscription it replaces |
| `GET {MnSRoot}/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl={id}` | `getMOIAttributes` | 200, the subscription |
| `DELETE {MnSRoot}/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl={id}` | `deleteMOI` (12.1.1.1.5): end the subscription | 204 |
| `POST` to the `notificationRecipientAddress` of a subscription, from the OAM | One of the six alarm notifications of TS 28.111 | 204 from the OSS (TS 32.158 5.5.4) |

An error has the body of TS 32.158 6.6, with its `type` and `reason`. Package `mns` holds the types, with the attribute names of the OpenAPI files, and the `oss` command uses the same types to check what the OAM sends.

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
| Alarm list, TS 28.111 7.3.1.2 | Most of the optional attributes of an alarm record, such as backedUpStatus, trendIndication, the correlation attributes and the attributes of who cleared an alarm. alarmChangedTime, specificProblem, additionalText and the acknowledgement attributes, `ackSystemId` among them, are kept, with the mandatory notificationId. | No role here has a value for them: every alarm here is cleared by the node that raised it, and no role relates one alarm to another. |
| Alarm list, TS 28.111 7.3.2.2 and 6.13 | The operationalState, the administrativeState and the unreliableAlarmScope of the list. | The list works whenever the node answers, so its operational state would always be `ENABLED`. `dropped_records` says instead when the list stopped holding every alarm. |
| Alarm list, TS 28.111 6.3 and 7.4.1 | `object_instance` only looks like a Distinguished Name. Its classes are the generic ManagedElement and ManagedFunction of TS 28.622, with Peer, which is no class of a network resource model, and the values are not escaped. | The node is no managed object of a network resource model here, and not every role has an IMS class of TS 28.705. |

A cleared alarm stays in the list until it is acknowledged (TS 28.111 6.14), and a record should not leave the list without an acknowledgement of an operator or a machine (6.7). The node is that machine when nobody acknowledges a cleared alarm within an hour: it acknowledges the alarm itself, with `ack_user_id` `retention` and its own name as `ack_system_id`, and the record leaves the list as any acknowledged cleared alarm does. Otherwise a list that no operator watches would keep the faults that are over until it is full. TS 28.111 asks a producer that takes no acknowledgements from outside to acknowledge alarms by itself (`REQ-FM-MC-12` in clause 5), and says nothing of one that does, so this acknowledgement is this project's own. The name `retention` tells it apart from that of an operator.
