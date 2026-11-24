// Package mns holds the JSON types of the 3GPP management services
// that the OAM server offers northbound, to an OSS: the fault
// supervision of TS 28.111 (Release 20, V20.0.0) carried by the
// generic provisioning operations (ProvMnS) of TS 28.532 (Release 20,
// V20.2.0) over the REST solution set of TS 32.158 (Release 20,
// V20.1.0). The OAM server is an MnS producer and the OSS an MnS
// consumer (TS 28.533): the OSS reads the AlarmList of the SubNetwork,
// acknowledges alarm records by patching it, and creates an
// NtfSubscriptionControl to be sent the alarm notifications.
// The package serves no HTTP itself: the OAM server and a stand-in for
// an OSS in the tests both use it.
//
// The JSON types carry the attribute names of the OpenAPI files in the
// archives of those specifications on the 3GPP file server. A Go field
// is the attribute name with its first letter in upper case, so that
// "perceivedSeverity" in a capture is PerceivedSeverity here. The
// files, and the version in their info.version, are:
//
//	TS28111_FaultNrm.yaml                19.3.0  TS 28.111 V20.0.0  AlarmRecord, AlarmList-Single, the notifications
//	TS28111_FaultNotifications.yaml      19.3.0  TS 28.111 V20.0.0  the POST of a notification to its recipient
//	TS28532_ProvMnS.yaml                 19.2.0  TS 28.532 V20.2.0  Resource, Scope, the paths
//	TS28623_SubscriptionControlNrm.yaml  19.4.0  TS 28.623 V20.3.0  NtfSubscriptionControl-Single, Scope
//	TS28623_ComDefs.yaml                 19.6.0  TS 28.623 V20.3.0  NotificationHeader, the error responses
//	TS28623_GenericNrm.yaml              20.1.0  TS 28.623 V20.3.0  Top
//
// The files are 3GPP's and are not copied here. TestAttributeNames
// (names_test.go) compares the attributes of each type with a list of
// the names that the schema has in that file, written out by hand,
// and with the names that the type leaves out and why. The tests of
// types_test.go read example bodies written with those names and write
// them back.
//
// The objects are named by DNs that start at the SubNetwork, such as
// SubNetwork=ims,ManagedElement=scscf, with no DN prefix (DN). MnSRoot
// builds the URIs of TS 32.158 4.4.2 from them: the URI of a resource,
// {MnSRoot}/ProvMnS/v19/SubNetwork=ims/AlarmList=1, and the canonical
// URI that a notification names its object by,
// http://oam:8080/SubNetwork=ims/ManagedElement=scscf.
//
// What of the files the types cover:
//
//	AlarmList-Single, AlarmRecord                    AlarmListSingle, AlarmListAttributes, AlarmRecord
//	NotifyNewAlarm, NotifyChangedAlarmGeneral,
//	NotifyClearedAlarm, NotifyAckStateChanged,
//	NotifyPotentialFaultyAlarmList,
//	NotifyAlarmListRebuilt                           the types of the same names, with NotificationHeader
//	NtfSubscriptionControl-Single, Scope             NtfSubscriptionControlSingle, NtfSubscriptionControlAttributes, Scope
//	ErrorResponseGet, ErrorResponseDefault           the types of the same names
//	NotifyNewSecAlarm, NotifyChangedSecAlarmGeneral,
//	NotifyComments,
//	NotifyCorrelatedNotificationChanged,
//	HeartbeatControl-Single, ErrorResponsePatch,
//	the configuration notifications of ProvMnS       not built
//
// SIMPLIFIED: TS 28.111 V20.0.0 6.1. Of the optional solution
// components, commenting alarms (6.9), alarm correlation (6.10) and
// the alarm history (6.15) are left out, and so are their
// notifications and attributes; so are the security alarms (7.3.1.3),
// the attributes of threshold alarms and the clearing of alarms by the
// consumer (6.8). The alarms of the nodes need none of them: a node
// raises and clears every alarm itself, raises no threshold alarm, and
// names no subscriber, whom a security alarm would name as its service
// user. The OpenAPI files of the same archive have no AlarmHistory.
package mns
