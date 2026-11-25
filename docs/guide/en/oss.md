# Hand the alarms to an OSS

[日本語](../ja/oss.md)

The nodes of this network keep their alarms in lists of their own, and the OAM gathers them, as [Read the alarms about the HSS](registration.md#read-the-alarms-about-the-hss) shows. The operator's network management, the OSS, does not read each node: it reads the alarms of the whole network from the OAM, acknowledges them there, and is told of each new one as it comes. This guide follows that interface. The OSS here is a small stand-in, the `oss` service, and everything between it and the OAM is HTTP in the clear, so the capture shows every request, every answer and every notification.

The guide follows these editions: TS 28.111 V20.0.0 (fault supervision), TS 28.532 V20.2.0 (the generic provisioning operations), TS 28.622 V20.4.0 (the subscriptions), TS 32.158 V20.1.0 (the REST rules of the management services), TS 32.161 V19.0.0 (Jex), TS 32.101 V19.0.0 and TS 32.102 V19.0.0 (the management architecture). The names of the paths and of the attributes are those of the OpenAPI files in the archives of those specifications: `TS28111_FaultNrm.yaml` and `TS28111_FaultNotifications.yaml` 19.3.0, `TS28532_ProvMnS.yaml` 19.2.0, `TS28623_SubscriptionControlNrm.yaml` 19.4.0 and `TS28623_ComDefs.yaml` 19.6.0. Clause numbers below refer to those editions.

## Before you start

- You have read [Follow a UE registering with the IMS](registration.md), up to [Take the HSS out of service](registration.md#take-the-hss-out-of-service). This guide raises the same alarm, "Diameter peer down", in the same way.
- The abbreviations are explained in the [glossary](../../glossary.md).

Start the network with the profile `oss`, which adds the stand-in at 172.28.0.66:

```sh
docker compose --profile oss up -d --build
```

Wait until the nodes have opened their Diameter connections to the HSS, as [the first commands](README.md#the-first-commands) say.

## The element manager and the network manager

TS 32.101 5.1.1 sorts the management interfaces of a network by what they join. Type 1 is between a network element (NE) and the element manager (EM) that manages it, and type 2 is between the element manager and the network manager (NM). Type 2 is called Itf-N, and it is the one that 3GPP mainly standardizes: its information model is defined apart from any protocol, so that one model can be carried by several (TS 32.102 7.3.2).

| Layer | In this network | Interface below it |
| --- | --- | --- |
| NM | The OSS, here the `oss` service | Itf-N, type 2: the 3GPP management services of this guide |
| EM | The OAM, the `oam` service | Type 1: the maintenance API of each node, this project's own (TS 32.600 4.2.1.3 leaves it to the product) |
| NE | The CSCFs, the HSS, the application servers and the media relays | |

In the newer framework of management services (TS 28.533), the OAM is the MnS producer of fault supervision and the OSS its MnS consumer. Fault supervision has no paths of its own: the alarm list is one class of the network resource model (NRM), and the OSS reads and changes it with the generic operations that read and change any managed object, the provisioning MnS (ProvMnS) of TS 28.532 (TS 28.111 6.2 and 6.6).

## The alarm list and its URI

The OAM manages one SubNetwork, `ims`, and offers one `AlarmList` below it, with the alarm records of every node. TS 28.111 6.11 gives that very case as its example: a subnetwork manager that may lose its connection to one of the elements it manages. The list is always there; an OSS can neither create nor delete it (TS 28.111 7.3.2.1), and its id is `1`.

Its URI is `http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1`, made of three parts (TS 32.158 4.4.2):

| Part | Here | What it is |
| --- | --- | --- |
| `{MnSRoot}` | `http://oam:8080/3GPPManagement` | The listener of the OAM, then the root path of the management services. `deploy/compose/oam.yaml` sets it in `mns_root`, beside the name of the SubNetwork in `subnetwork`; without the two, the OAM does not offer this interface. |
| The service and `{MnSVersion}` | `/ProvMnS/v19` | `v19` is `v` and the first number of the version of `TS28532_ProvMnS.yaml`, 19.2.0 (TS 32.158 4.6.2). |
| The object | `/SubNetwork=ims/AlarmList=1` | The Distinguished Name (DN) of the list, `SubNetwork=ims,AlarmList=1`, written as a path. |

The OAM serves it on the same listener as its own API, which is on no address of the host, so every command below runs inside a container of the network. The stand-in is one: `oss alarms`, `oss ack`, `oss subscribe` and `oss unsubscribe` send the requests of an OSS, with the defaults of this deployment, and read the answers strictly against the OpenAPI names.

Read the list:

```sh
docker compose exec oss oss alarms
```

```json
{
  "id": "1",
  "objectClass": "AlarmList",
  "objectInstance": "SubNetwork=ims,AlarmList=1",
  "attributes": {
    "operationalState": "ENABLED",
    "numOfAlarmRecords": 0,
    "lastModification": "2026-11-24T10:12:40Z",
    "alarmRecords": {}
  }
}
```

A node that started before the HSS listened may already hold a cleared record from its first attempt to connect; [raise an alarm](#raise-an-alarm-and-follow-it) below starts by acknowledging those.

An alarm record is keyed by the name of its node in the inventory of the OAM, a dot, and the id that the node gave it, such as `scscf.9f3c2a7b1d4e6058`. From that key the OAM knows which node to hand an acknowledgement to, without keeping anything. Its `objectInstance` starts with `SubNetwork=ims,ManagedElement=` and that name too, whatever the node calls itself: two nodes of one role that share a configuration file have one node name, but two names in the inventory.

## Subscribe to the notifications

An OSS subscribes by creating an `NtfSubscriptionControl` below the SubNetwork (TS 28.622 4.3.22.1), with the `createMOI` operation as a `PUT` (TS 28.532 12.1.1.1.2.1), and ends the subscription by deleting it.

```sh
docker compose exec oss oss subscribe
```

```http
PUT /3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1 HTTP/1.1
Host: oam:8080
Content-Type: application/json

{"id":"1","objectClass":"NtfSubscriptionControl","attributes":{"notificationRecipientAddress":"http://oss:8080/notifications"}}
```

The OAM answers `201 Created`, with the URI of the subscription in `Location` and the subscription in the body, which `oss subscribe` prints:

```json
{
  "id": "1",
  "objectClass": "NtfSubscriptionControl",
  "objectInstance": "SubNetwork=ims,NtfSubscriptionControl=1",
  "attributes": {
    "notificationRecipientAddress": "http://oss:8080/notifications"
  }
}
```

| Attribute | Here | What it says |
| --- | --- | --- |
| `notificationRecipientAddress` | `http://oss:8080/notifications` | Where the OAM posts each notification. `oss serve`, the command the `oss` service runs, takes them there. |
| `notificationTypes` | none, so all six below | The notifications to send. `oss subscribe -types notifyNewAlarm,notifyClearedAlarm` names some. |
| `notificationFilter` | none | A Jex conditions expression (TS 32.161 7.6) that a notification must meet, such as `contains(href,"ManagedElement=scscf") and perceivedSeverity="CRITICAL"`. `oss subscribe -filter` sets it. |
| `scope` | none | The objects whose notifications are sent; none means the whole SubNetwork. |

The OAM takes these four attributes and nothing more. A subscription with a name that the OpenAPI file has but the OAM does not take is refused with 500 and `SERVER_LIMITATION`: `HeartbeatControl`, which asks for heartbeat notifications, `VsDataContainer`, or a `scope` that selects with `dataNodeSelector`. The OAM understands such a request, but cannot carry it out (TS 32.158 6.6.4). A body that does not validate against the schema of the file is refused with 400 and `NEW_OBJECT_REPRESENTATION_INVALID` (6.6.4, 6.6.5.4). It is one that is no JSON; one with a name that the file does not have, such as `lastSequenceNo`, or with a name that differs from one of the file in case alone, such as `heartbeatControl`; one with `null` where the schema allows none, which is anywhere but an `id` and `vsData`; and one whose `HeartbeatControl`, `VsDataContainer` or `dataNodeSelector` does not have the form of its schema, such as a `HeartbeatControl` without `id`. When a body has a problem of each kind, the answer is the 400, which the OSS can mend: `SERVER_LIMITATION` is for a request that is well formed and understood (6.6.4).

A `PUT` of the same id again replaces the subscription and is answered `200 OK`. The subscription is kept by the OAM, not by the instance that took the request, so any instance of the OAM that watches a node sends its notifications. Read it back with `getMOIAttributes`, a `GET` of the same URI:

```sh
docker compose exec oss wget -qO- http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1
```

## The notifications and when each comes

The OAM watches each node: it asks the node's maintenance API for the changes of its alarm list, a request that waits up to five seconds for one, and turns each change into a notification. It posts the notification to every subscription that selects it, and the OSS answers `204 No Content` (TS 32.158 5.5.4). Any answer but a 2xx means that the OSS did not take the notification, a redirect too: the OAM does not follow one, as a client that follows a 301 or a 302 may send a GET in place of the POST, without the notification (RFC 9110 15.4.2, 15.4.3).

| Notification (TS 28.111) | When |
| --- | --- |
| `notifyNewAlarm` (8.2) | A record is added to the list: a node raised an alarm that it did not hold. |
| `notifyChangedAlarmGeneral` (8.4) | The severity, to a value other than `CLEARED`, or the additional text of a record changes. An alarm raised again while its cleared record is still in the list changes that record, so it comes as this notification, not as a new alarm (6.12, 6.14). |
| `notifyClearedAlarm` (8.3) | The severity of a record becomes `CLEARED`: the node that raised the alarm found the condition gone. |
| `notifyAckStateChanged` (8.8) | A record is acknowledged, or the acknowledgement is taken back. |
| `notifyPotentialFaultyAlarmList` (8.10) | A node stops answering the OAM, so its part of the list may be wrong. |
| `notifyAlarmListRebuilt` (8.5) | That part can be trusted again, but changes may have been missed: read it again. |

No notification says that a record left the list. An OSS knows it from the two notifications of a record that is cleared and acknowledged, in either order (TS 28.111 6.12).

Each notification begins with the same header (TS 28.532 11.0.2):

| Attribute | Example | What it is |
| --- | --- | --- |
| `href` | `http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org` | The object the notification is about, as its canonical URI: the host of the OAM, then the DN as a path (TS 32.158 4.2.4). |
| `notificationId` | `4711093825561044` | The same number as the `notificationId` of the record after the change, so that an OSS can match the two, and tell a notification it was sent twice from two changes. |
| `notificationType` | `notifyNewAlarm` | Which of the six. |
| `eventTime` | `2026-11-24T10:15:02Z` | When the change happened at the node. |
| `systemDN` | `SubNetwork=ims` | The DN of the object at the root of what the OAM manages. |

`oss serve` checks each notification against the OpenAPI names, keeps those it takes, and serves them back:

```sh
docker compose exec oss wget -qO- http://127.0.0.1:8080/received
docker compose exec oss wget -qO- 'http://127.0.0.1:8080/received?notification_type=notifyNewAlarm'
```

A body it refused, with the reason, is at `/refused`.

## Raise an alarm and follow it

1. Acknowledge the cleared records about the HSS that are left from the start, so that the alarm of the next step comes as a new one. Read them with a filter, a Jex conditions expression that selects records in the form of TS 28.111 A.1.4:

   ```sh
   docker compose exec oss oss alarms -filter \
     '/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="CLEARED"]'
   ```

   Then acknowledge each of them with `oss ack` and its id, as step 4 shows. One request carries the records of one node; a list that holds none needs nothing.

2. Shut the HSS down, with the request of [Take the HSS out of service](registration.md#take-the-hss-out-of-service):

   ```sh
   docker compose exec hss sh -c '
   body="{\"administrative_state\":\"SHUTTINGDOWN\",\"operator\":\"learner\"}"
   printf "PUT /maintenance/v1/functions/hss/administrative-state HTTP/1.0\r\nContent-Type: application/json\r\nContent-Length: ${#body}\r\n\r\n%s" "$body" |
     nc -w 5 127.0.0.1 8085'
   ```

   About 30 seconds later, Tc after the DPR, the five nodes that reach the HSS try to connect again, are refused, and each raises its alarm. Within a second the OSS has a `notifyNewAlarm` from each:

   ```json
   {"href":"http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
    "notificationId":4711093825561044,"notificationType":"notifyNewAlarm","eventTime":"2026-11-24T10:15:02Z","systemDN":"SubNetwork=ims",
    "alarmId":"scscf.9f3c2a7b1d4e6058","alarmType":"COMMUNICATIONS_ALARM","probableCause":"COMMUNICATIONS_SUBSYSTEM_FAILURE",
    "specificProblem":"Diameter peer down","perceivedSeverity":"MAJOR","additionalText":"no transport connection to the peer"}
   ```

   The later notifications of this alarm carry the same `alarmId`, which is what names the alarm in them (TS 28.111 6.12). This one relates that id to the four attributes that identify an alarm (6.14): the object, which `href` gives, `alarmType`, `probableCause` and `specificProblem`.

3. Read the alarms about the HSS:

   ```sh
   docker compose exec oss oss alarms -filter \
     '/AlarmList[id="1"]/attributes/alarmRecords/*[specificProblem="Diameter peer down" and perceivedSeverity="MAJOR"]'
   ```

   The filter goes in the query of a `GET`, percent-encoded (`GET /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1?filter=%2FAlarmList%5Bid%3D%221%22%5D...`). The answer holds the five records, each under the key of its `alarmId`:

   ```json
   "scscf.9f3c2a7b1d4e6058": {
     "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
     "notificationId": 4711093825561044,
     "alarmRaisedTime": "2026-11-24T10:15:02Z",
     "alarmType": "COMMUNICATIONS_ALARM",
     "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
     "specificProblem": "Diameter peer down",
     "perceivedSeverity": "MAJOR",
     "additionalText": "no transport connection to the peer",
     "ackState": "UNACKNOWLEDGED"
   }
   ```

   `numOfAlarmRecords` counts every record of the list, before the filter. A filter that selects nothing is no error: the answer is the list with no records.

4. Acknowledge the alarm of the S-CSCF, with the id from its notification:

   ```sh
   docker compose exec oss oss ack scscf.9f3c2a7b1d4e6058
   ```

   The acknowledgement is the `modifyMOIAttributes` operation as a JSON Merge Patch (RFC 7396, TS 28.532 12.1.1.1.4), one of the three forms of TS 28.111 A.1.4. The patch writes only the three attributes that an OSS may change (TS 28.111 7.3.1.2): `ackState`, `ackUserId`, who acknowledges, and `ackSystemId`, from which system.

   ```http
   PATCH /3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1 HTTP/1.1
   Host: oam:8080
   Content-Type: application/merge-patch+json

   {"id":"1","attributes":{"alarmRecords":{"scscf.9f3c2a7b1d4e6058":{"ackState":"ACKNOWLEDGED","ackUserId":"noc-operator","ackSystemId":"oss-1"}}}}
   ```

   The OAM hands the acknowledgement to the S-CSCF, which changes its record, and answers `204 No Content`. The change of the record is a change like any other, so the OSS also gets `notifyAckStateChanged`, with `ackState`, `ackUserId` and `ackSystemId`. The answer and the notification travel apart, and may come in either order. The alarm stays `MAJOR`: acknowledging says that someone is on it, not that it is over.

   `oss ack -unacknowledge` takes the acknowledgement back. A patch that names records of two nodes is refused with 500 and `SERVER_LIMITATION` before any node is asked, and an id that the list does not hold with 400 and `IE_NOT_FOUND`; the body of an error has the `type` and the `reason` of TS 32.158 6.6.

   Every change of the acknowledgement names who made it, as `notifyAckStateChanged` always carries `ackUserId` (TS 28.111 8.8.2): a patch without `ackUserId` is refused with 400 and `NEW_ATTRIBUTE_VALUE_INVALID`. The OAM screen acknowledges through the OAM's own API, which asks for the name of the operator in the same way. The OAM hands that acknowledgement on with the name as `ackUserId` and its own name as `ackSystemId`: the `node` of its configuration file, `oam` here. The OSS gets the same `notifyAckStateChanged`.

5. Unlock the HSS, with the same request and `UNLOCKED`. Each node connects again at its next attempt, up to Tc later, and the CEA clears its alarm: the OSS gets `notifyClearedAlarm` from each. The record of the S-CSCF, cleared and acknowledged, leaves the list (TS 28.111 6.14), and the OSS knows it from its two notifications. The four others stay, `CLEARED` and `UNACKNOWLEDGED`, until someone acknowledges them.

6. End the subscription:

   ```sh
   docker compose exec oss oss unsubscribe
   ```

   That is `deleteMOI`, `DELETE /3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1`, answered `204 No Content` (TS 28.532 12.1.1.1.5). The OAM sends nothing more to that address: acknowledge the four cleared records now, and no `notifyAckStateChanged` comes.

```text
OSS                                OAM                       S-CSCF                  HSS
 |  PUT NtfSubscriptionControl=1    |                           |                     |
 |--------------------------------->|                           |                     |
 |  201 Created                     |                           |                     |
 |<---------------------------------|                           |                     |
 |                                  |  GET alarm-events, waits  |                     |
 |                                  |-------------------------->|                     |
 |                                  |                           |  DPR, DPA           |
 |                                  |                           |<------------------->|
 |                                  |                           |  SYN, refused (Tc)  |
 |                                  |                           |-------------------->|
 |                                  |  200, the alarm was added |                     |
 |                                  |<--------------------------|                     |
 |  POST notifyNewAlarm             |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |  GET AlarmList=1?filter=         |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |  GET alarms, every node   |                     |
 |                                  |-------------------------->|                     |
 |                                  |  200                      |                     |
 |                                  |<--------------------------|                     |
 |  200 AlarmList                   |                           |                     |
 |<---------------------------------|                           |                     |
 |  PATCH AlarmList=1               |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |  PATCH alarms             |                     |
 |                                  |-------------------------->|                     |
 |                                  |  200                      |                     |
 |                                  |<--------------------------|                     |
 |  204                             |                           |                     |
 |<---------------------------------|                           |                     |
 |  POST notifyAckStateChanged      |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |                                  |                           |  CER, CEA (unlock)  |
 |                                  |                           |<------------------->|
 |  POST notifyClearedAlarm         |                           |                     |
 |<---------------------------------|                           |                     |
 |  204                             |                           |                     |
 |--------------------------------->|                           |                     |
 |  DELETE NtfSubscriptionControl=1 |                           |                     |
 |--------------------------------->|                           |                     |
 |  204                             |                           |                     |
 |<---------------------------------|                           |                     |
```

- The ladder has the S-CSCF; the I-CSCF, the IP-SM-GW, the MMTel AS and the Service Centre do the same, and each of their alarms gets its own notifications.
- Between the OAM and the S-CSCF is the maintenance API on port 8085, which Wireshark does not decode as HTTP by itself. Between the OSS and the OAM, both on port 8080, it decodes HTTP and the JSON bodies: `http && ip.addr == 172.28.0.66` shows the exchange, and `json.member_with_value == "notificationType:notifyNewAlarm"` the notifications of new alarms.
- The `GET` of the list asks every node of the inventory for its list before it answers; an acknowledgement goes to the one node that the key names.

## When a node does not answer: unreliableAlarmScope

The OAM keeps no copy of the alarms of a node. When a node does not answer, the OAM cannot say which alarms it holds, so it says that its part of the list is unreliable (TS 28.111 6.11). Pause the container of the MRFP, so that it stops answering without its process ending:

```sh
docker compose pause mrfp
```

The request of the OAM for the events of the MRFP runs out of time, within about ten seconds, and the OSS gets `notifyPotentialFaultyAlarmList`:

```json
{"href":"http://oam:8080/SubNetwork=ims/ManagedElement=mrfp","notificationId":2113906541120775,
 "notificationType":"notifyPotentialFaultyAlarmList","eventTime":"2026-11-24T10:20:31Z","systemDN":"SubNetwork=ims",
 "reason":"serviceprovider-NE communication error"}
```

Its `href` is the base of the part that may be wrong, here the whole of one node. The list says the same while the node is out of reach:

```sh
docker compose exec oss oss alarms
```

```json
"attributes": {
  "operationalState": "ENABLED",
  "numOfAlarmRecords": 0,
  "unreliableAlarmScope": "SubNetwork=ims,ManagedElement=mrfp",
  "alarmRecords": {}
}
```

The records of the MRFP are not in the answer at all: they are unknown, not absent. Let the MRFP go on again:

```sh
docker compose unpause mrfp
```

At its next answer the OSS gets `notifyAlarmListRebuilt` about the same `href`, with `reason` `System-NE communication error` and `alarmListAlignmentRequirement` `ALIGNMENT_REQUIRED`, and `unreliableAlarmScope` leaves the list. `ALIGNMENT_REQUIRED` tells the OSS that changes of that part were not notified (TS 28.111 8.5.2), so it reads the part again, with a `filter` on `contains(objectInstance,"ManagedElement=mrfp")`.

The OAM sends `notifyAlarmListRebuilt` with `ALIGNMENT_REQUIRED` in three more cases, each time because some changes were not notified:

| Case | `href` | `reason` |
| --- | --- | --- |
| The node started again, with a new alarm list | The node | `System restarts` |
| Changes of the node left its journal before the OAM read them, or its full list dropped a record | The node | `indeterminate` |
| A notification did not reach this OSS: no connection, no answer within five seconds, or an answer other than 2xx | The whole SubNetwork, `http://oam:8080/SubNetwork=ims`, sent to that subscription only | `indeterminate` |

The OAM does not send a notification again. From the notification that did not reach it, the OSS is sent nothing but that `notifyAlarmListRebuilt`, which the OAM tries every 30 seconds, whether the alarms change or not, until the OSS takes it. Then the notifications go to it again, and it reads the list again. If an alarm changes while that notification is on its way, though, the OSS may have read the list before the change, so the OAM sends it once more at its next try. An OSS that does not answer thus costs the OAM one wait of five seconds, not one for each notification, and holds up no other OSS.

When two nodes or more do not answer, `unreliableAlarmScope` is the whole SubNetwork, `SubNetwork=ims`, while each node still gets its own two notifications.

## Wireshark display filters

| Filter | What it finds |
| --- | --- |
| `http && ip.addr == 172.28.0.66` | Every exchange between the OSS and the OAM. |
| `http.request.method == "PUT" && http.request.uri contains "NtfSubscriptionControl="` | The subscription. Its answer carries `Location`. |
| `http.request.method == "DELETE" && http.request.uri contains "NtfSubscriptionControl="` | The end of the subscription. |
| `http.request.uri contains "AlarmList=1?filter="` | The reads of the list with a filter. |
| `json.key == "unreliableAlarmScope"` | The answers that hold an unreliable part of the list. |
| `http.request.method == "PATCH" && http.content_type == "application/merge-patch+json"` | The acknowledgements, as JSON Merge Patches. |
| `http.request.uri == "/notifications"` | The notifications that the OAM posts to the OSS. |
| `json.member_with_value == "notificationType:notifyNewAlarm"` | The new alarms. |
| `json.member_with_value == "notificationType:notifyAckStateChanged"` | The acknowledgements, as notified. |
| `json.member_with_value == "notificationType:notifyClearedAlarm"` | The alarms that were cleared. |
| `json.member_with_value == "notificationType:notifyPotentialFaultyAlarmList"` | A node that stopped answering. |
| `json.member_with_value == "notificationType:notifyAlarmListRebuilt"` | A part of the list to read again. |

`scripts/e2e-fm.sh` runs the steps of this guide, checks each exchange in the capture, and runs every filter of this table against that capture: a filter that finds no frame fails the run. It leaves the capture in `captures/` and the frames of the exchanges in `e2e-out/fm-ladder.txt`.

## What this project simplifies

| Where | What is left out | Why |
| --- | --- | --- |
| OAM, TS 28.111 6.11 and 6.12 | The `unreliableAlarmScope` of the alarm list that the OAM offers to an OSS names the one node that does not answer, or the whole `SubNetwork` when two or more do not. It never lists the nodes. | The OpenAPI file of the alarm list gives the attribute one DN, although TS 28.111 7.4.1 lets it hold several, and the whole list is one of the parts that 6.11 lets it name. |
| OAM, TS 32.158 6.1.2 and 6.2.2 | A read of that alarm list takes no `attributes` and no scope but `BASE_ONLY`, and its `fields` picks alarm records only. Any other is answered with 500 and `QUERY_PARAMS_TOO_COMPLEX`. | The alarm list contains no objects, so no scope reaches past the list itself, and the records are what an OSS reads from it. |
| OAM, TS 32.158 6.3.1 | A patch that acknowledges the alarm records of two or more nodes is refused with 500 and `SERVER_LIMITATION`, after the whole body is checked and before any node is asked, so that no list changes. | A patch is applied whole or not at all, and across nodes that would take a commit in two phases between them. An OSS sends one patch for each node instead. |
| OAM, TS 28.111 A.1.4 | Of the three forms of patch that acknowledge an alarm, only the JSON Merge Patch is taken. JSON Patch and the two forms of 3GPP are answered with 415. | TS 28.532 and TS 32.158 spell the media type of the 3GPP forms differently, and an acknowledgement needs no more than the Merge Patch. |
| OAM, TS 28.622 4.3.22.1 | A subscription of an OSS without `notificationTypes` is sent the six alarm notifications only: the OAM sends none of the configuration notifications of TS 28.532, so the creation and the deletion of a subscription are never notified. Its scope is the whole `SubNetwork`. | Fault supervision needs the alarm notifications alone, and an OSS that wants the alarms of some nodes only picks them with `notificationFilter`. |
| OAM, TS 28.111 7.3.2.1 and 8.5 | A record that the full alarm list of a node removed is told to the OSSs with `notifyAlarmListRebuilt` about that node, with `ALIGNMENT_REQUIRED` and `indeterminate`, although no list was rebuilt. | TS 28.111 lets a full list remove records and has no notification for it. This one says that some changes of the alarms were not notified, so the OSS reads that part of the list again rather than keep a record that the node no longer has. |
| OAM, TS 28.111 8.5.1 and 8.5.2 | A recipient that did not take a notification is sent nothing more until it takes a `notifyAlarmListRebuilt` of the whole list, with `ALIGNMENT_REQUIRED` and `indeterminate`, sent to its subscription alone. The notifications it missed are not sent again. | TS 28.111 sends that notification to every subscriber when a list has been rebuilt. Sending the missed notifications again would take the reliable notification transport of TS 28.532; the OSS reads the list instead, and an OSS that does not answer holds up neither the others nor the watch of a node. |

## Clean up

```sh
docker compose --profile ue --profile phones --profile volte --profile oss down -v --remove-orphans
```

This stops the nodes and the stand-in of the OSS and keeps the images, so the next guide starts without a new build. The `-v` deletes the volume of the OAM, and the subscriptions in it.
