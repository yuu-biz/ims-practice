# 3GPP management services

[日本語](README.ja.md)

This package holds the JSON bodies with which an OSS reads and acknowledges the alarms of the network and is told of new ones, by the 3GPP management services (MnS). Fault supervision is TS 28.111, and the generic operations on the managed objects that carry it are the provisioning service (`ProvMnS`) of TS 28.532 over the HTTP rules of TS 32.158. The OSS reads the `AlarmList` of the `SubNetwork`, acknowledges an alarm record by patching it, and creates an `NtfSubscriptionControl` to be sent the alarm notifications.

The attribute names are those of the OpenAPI files of 3GPP. The files are not copied into the repository; the tests hold the names of each schema, written out from the files, and check each Go type against them. The package comment names each file and its version.

```sh
go test ./internal/mns
go doc ./internal/mns
```

## What this package simplifies

The code has a `SIMPLIFIED:` comment at each of these places.

| Where | What is left out | Why |
| --- | --- | --- |
| Package `mns`, TS 28.111 6.1 | Commenting alarms, alarm correlation and the alarm history, with their notifications and attributes; security alarms, the attributes of threshold alarms, and alarms that the MnS consumer clears. | The nodes raise and clear every alarm themselves, raise no threshold alarm, and never name a subscriber, whom a security alarm would name. The OpenAPI files have no alarm history. |
| `AlarmType`, TS 28.111 7.3.1.3, 8.2.2 | The five types of security alarms. | No node raises a security alarm. |
| `ProbableCause` and `SpecificProblem`, TS 28.111 7.4.1 | The integer form of each, whose meaning a vendor defines. | The nodes write both as strings. |
| `AlarmRecord`, TS 28.111 7.3.1.2 | The optional attributes that the nodes do not keep, such as `backedUpStatus`, `trendIndication` and `monitoredAttributes`, and those of the parts that the package leaves out. | The alarm list of each node keeps none of them. |
| `Top`, TS 28.622 4.3.29 | `VsDataContainer`, the vendor-specific data below an object. | No object here has any. |
| `AlarmListAttributes`, TS 28.111 7.3.2.2, 6.13 | `administrativeState`, which locks the list. | It is optional, and nothing here locks the list. |
| `NotificationHeader`, TS 28.532 11.0.2 | `sequenceNo` and `subscriptionId`. | An MnS producer carries them only with the reliable notification transport, which is not built. |
| `NotifyNewAlarm`, `NotifyClearedAlarm` and `NotifyChangedAlarmGeneral`, TS 28.111 8.2.2, 8.3.2, 8.4.2 | The parameters that carry the attributes `AlarmRecord` leaves out, and `changedAlarmAttributes`, the old values of a change. | The records do not have them. |
| `NtfSubscriptionControlSingle`, TS 28.622 4.3.21 | The `HeartbeatControl` that a subscription may contain. | Fault supervision does not need heartbeat notifications. |
| `Scope`, TS 28.622 4.3.23 | The `dataNodeSelector` form of the scope of a subscription. | A subscription here selects its notifications with `notificationFilter`. |
| `ScopeType`, TS 32.158 6.1.2 | `BASE_NTH_LEVEL` and `BASE_SUBTREE`. | The alarm list and the subscriptions contain no objects, so a scope is either the object alone or everything below the `SubNetwork`. |
