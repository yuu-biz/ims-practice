package mns

import "time"

// The classes of the NRM that the DNs and the resources name (TS 28.111
// 7.3.2, TS 28.622 4.3.22). The naming attribute of a class is its
// name (TS 32.158 4.2.1.0).
const (
	ClassSubNetwork             = "SubNetwork"
	ClassManagedElement         = "ManagedElement"
	ClassAlarmList              = "AlarmList"
	ClassNtfSubscriptionControl = "NtfSubscriptionControl"
)

// AlarmType says roughly in which area an alarm arose (the alarmType
// attribute of TS 28.111 7.4.1; the schema AlarmType of
// TS28111_FaultNrm.yaml).
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.1.3 and 8.2.2. The five types of
// security alarms (INTEGRITY_VIOLATION, OPERATIONAL_VIOLATION,
// PHYSICAL_VIOLATION, SECURITY_SERVICE_OR_MECHANISM_VIOLATION,
// TIME_DOMAIN_VIOLATION) are left out, because no node raises a
// security alarm.
type AlarmType string

const (
	CommunicationsAlarm   AlarmType = "COMMUNICATIONS_ALARM"
	QualityOfServiceAlarm AlarmType = "QUALITY_OF_SERVICE_ALARM"
	ProcessingErrorAlarm  AlarmType = "PROCESSING_ERROR_ALARM"
	EquipmentAlarm        AlarmType = "EQUIPMENT_ALARM"
	EnvironmentalAlarm    AlarmType = "ENVIRONMENTAL_ALARM"
	OtherAlarm            AlarmType = "OTHER"
)

// ProbableCause qualifies an alarm beyond its type (TS 28.111 7.4.1),
// such as COMMUNICATIONS_SUBSYSTEM_FAILURE. The OpenAPI lists the
// causes of the informative Annex B of TS 28.111, and takes any other
// string as well.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.4.1. The schema ProbableCause of
// TS28111_FaultNrm.yaml also takes an integer, whose meaning a vendor
// defines. Only the string form is kept: the nodes write every cause as
// a string.
type ProbableCause string

// SpecificProblem refines the probable cause (TS 28.111 7.4.1), such
// as "Diameter peer down".
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.4.1. The schema SpecificProblem of
// TS28111_FaultNrm.yaml also takes an integer. Only the string form is
// kept, as for ProbableCause.
type SpecificProblem string

// PerceivedSeverity is how urgently an alarm needs the operator (TS
// 28.111 7.4.1; the schema PerceivedSeverity). CLEARED means that the
// condition of the alarm is gone.
type PerceivedSeverity string

const (
	Indeterminate PerceivedSeverity = "INDETERMINATE"
	Critical      PerceivedSeverity = "CRITICAL"
	Major         PerceivedSeverity = "MAJOR"
	Minor         PerceivedSeverity = "MINOR"
	Warning       PerceivedSeverity = "WARNING"
	Cleared       PerceivedSeverity = "CLEARED"
)

// AckState is whether a consumer has acknowledged an alarm (TS 28.111
// 6.7; the schema AckState). A new alarm record starts UNACKNOWLEDGED.
type AckState string

const (
	Acknowledged   AckState = "ACKNOWLEDGED"
	Unacknowledged AckState = "UNACKNOWLEDGED"
)

// OperationalState is the schema OperationalState of
// TS28623_ComDefs.yaml: whether an object, such as the alarm list,
// works.
type OperationalState string

const (
	Enabled  OperationalState = "ENABLED"
	Disabled OperationalState = "DISABLED"
)

// AlarmRecord is one alarm in the alarm list (TS 28.111 7.3.1; the
// schema AlarmRecord). The alarmId of 7.3.1.2 is not one of its
// attributes in the OpenAPI: it is the key of the record in
// AlarmListAttributes.AlarmRecords.
//
// The attributes that 7.3.1.2 makes mandatory, and ackState, which the
// acknowledgement makes mandatory (7.3.1.3), are always written; the
// OpenAPI marks none of them as required. alarmClearedTime is mandatory
// too, but has no value until the alarm is cleared.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.1.2. Of the optional attributes,
// backedUpStatus, backUpObject, trendIndication, thresholdinfo,
// stateChangeDefinition, monitoredAttributes, proposedRepairActions and
// additionalInformation are left out, with the attributes of the
// solution components that the package leaves out: correlatedNotifications
// and rootCauseIndicator (correlation), clearUserId, clearSystemId and
// clearingType (clearing of ADMC alarms), and serviceUser,
// serviceProvider and securityAlarmDetector (security alarms). The
// nodes keep none of them.
type AlarmRecord struct {
	// ObjectInstance is the DN of the alarmed object.
	ObjectInstance string `json:"objectInstance"`
	// NotificationId is the id of the last notification sent about the
	// alarm.
	NotificationId    int64             `json:"notificationId"`
	AlarmRaisedTime   time.Time         `json:"alarmRaisedTime"`
	AlarmChangedTime  *time.Time        `json:"alarmChangedTime,omitempty"`
	AlarmClearedTime  *time.Time        `json:"alarmClearedTime,omitempty"`
	AlarmType         AlarmType         `json:"alarmType"`
	ProbableCause     ProbableCause     `json:"probableCause"`
	SpecificProblem   SpecificProblem   `json:"specificProblem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceivedSeverity"`
	AdditionalText    string            `json:"additionalText,omitempty"`
	// AckTime, AckUserId and AckSystemId say when and by whom AckState
	// was last set; they have no value before.
	AckTime     *time.Time `json:"ackTime,omitempty"`
	AckUserId   string     `json:"ackUserId,omitempty"`
	AckSystemId string     `json:"ackSystemId,omitempty"`
	AckState    AckState   `json:"ackState"`
}

// Top holds the attributes that every managed object has (TS 28.622
// 4.3.29; the schema Top of TS28623_GenericNrm.yaml). Id is the value
// of the naming attribute: "1" in AlarmList=1.
//
// SIMPLIFIED: TS 28.622 V20.4.0 4.3.29. VsDataContainer, which carries
// vendor-specific data below an object, is left out: no object here has
// any.
type Top struct {
	// The schema Top requires id, and lets it be null.
	Id          string `json:"id" schema:"required,nullable"`
	ObjectClass string `json:"objectClass,omitempty"`
	// ObjectInstance is the DN of the object.
	ObjectInstance string `json:"objectInstance,omitempty"`
}

// AlarmListSingle is one AlarmList (TS 28.111 7.3.2; the schema
// AlarmList-Single). It is the Resource (TS28532_ProvMnS.yaml) that a
// GET of the alarm list answers with and that a JSON Merge Patch
// changes.
type AlarmListSingle struct {
	Top
	Attributes *AlarmListAttributes `json:"attributes,omitempty"`
}

// AlarmListAttributes are the attributes of an AlarmList (TS 28.111
// 7.3.2.2). All but unreliableAlarmScope are mandatory in 7.3.2.2 and
// always written; the OpenAPI marks none as required.
//
// SIMPLIFIED: TS 28.111 V20.0.0 7.3.2.2 and 6.13. administrativeState,
// which locks the list, is left out: it is optional, and nothing here
// locks the list.
type AlarmListAttributes struct {
	OperationalState  OperationalState `json:"operationalState"`
	NumOfAlarmRecords int              `json:"numOfAlarmRecords"`
	LastModification  time.Time        `json:"lastModification"`
	// AlarmRecords is keyed by the alarmId of each record. An empty list
	// writes no attribute.
	AlarmRecords map[string]AlarmRecord `json:"alarmRecords,omitempty"`
	// UnreliableAlarmScope is the DN of the part of the list that may be
	// out of date (TS 28.111 6.11); empty when the whole list is
	// reliable. The OpenAPI gives it one DN, where 7.4.1 allows several.
	UnreliableAlarmScope string `json:"unreliableAlarmScope,omitempty"`
}

// NotificationType names the kind of a notification (TS 28.532 11.0.2;
// the schema NotificationType of TS28623_ComDefs.yaml). The values here
// are those of the schema AlarmNotificationTypes of
// TS28111_FaultNrm.yaml that the package has a body for.
type NotificationType string

const (
	NotifyNewAlarmType                 NotificationType = "notifyNewAlarm"
	NotifyChangedAlarmGeneralType      NotificationType = "notifyChangedAlarmGeneral"
	NotifyAckStateChangedType          NotificationType = "notifyAckStateChanged"
	NotifyClearedAlarmType             NotificationType = "notifyClearedAlarm"
	NotifyAlarmListRebuiltType         NotificationType = "notifyAlarmListRebuilt"
	NotifyPotentialFaultyAlarmListType NotificationType = "notifyPotentialFaultyAlarmList"
)

// NotificationHeader is what every notification carries (TS 28.532
// 11.0.2, 12.0.2.1; the schema NotificationHeader of
// TS28623_ComDefs.yaml). Href is the canonical URI (TS 32.158 4.2.4,
// MnSRoot.CanonicalURI) of the object the notification is about: it
// stands for the objectClass and objectInstance of TS 28.532 11.0.2
// (TS 28.111 A.1.2). SystemDN is the DN of the producer: the DN of the
// root object when there is no MnsAgent object. NotificationId tells
// the notifications of one object apart.
//
// SIMPLIFIED: TS 28.532 V20.2.0 11.0.2. sequenceNo and subscriptionId
// are left out: a producer carries them only when it supports the
// reliable notification transport, which is not built.
type NotificationHeader struct {
	Href             string           `json:"href"`
	NotificationId   int64            `json:"notificationId"`
	NotificationType NotificationType `json:"notificationType"`
	EventTime        time.Time        `json:"eventTime"`
	SystemDN         string           `json:"systemDN"`
}

// NotifyNewAlarm reports a record added to the alarm list (TS 28.111
// 8.2). EventTime is the alarmRaisedTime of the record.
//
// SIMPLIFIED: TS 28.111 V20.0.0 8.2.2. The parameters that carry the
// attributes AlarmRecord leaves out are left out too.
type NotifyNewAlarm struct {
	NotificationHeader
	AlarmId           string            `json:"alarmId"`
	AlarmType         AlarmType         `json:"alarmType"`
	ProbableCause     ProbableCause     `json:"probableCause"`
	SpecificProblem   SpecificProblem   `json:"specificProblem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceivedSeverity"`
	AdditionalText    string            `json:"additionalText,omitempty"`
}

// NotifyClearedAlarm reports that the perceivedSeverity of a record
// became CLEARED (TS 28.111 8.3). EventTime is its alarmClearedTime.
//
// SIMPLIFIED: TS 28.111 V20.0.0 8.3.2. correlatedNotifications,
// clearUserId and clearSystemId are left out, as in AlarmRecord.
type NotifyClearedAlarm struct {
	NotificationHeader
	AlarmId           string            `json:"alarmId"`
	AlarmType         AlarmType         `json:"alarmType"`
	ProbableCause     ProbableCause     `json:"probableCause"`
	SpecificProblem   SpecificProblem   `json:"specificProblem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceivedSeverity"`
}

// NotifyChangedAlarmGeneral reports that the perceivedSeverity, other
// than to CLEARED, or another attribute of a record changed (TS 28.111
// 8.4). EventTime is its alarmChangedTime. perceivedSeverity is
// mandatory in 8.4.2 and always written, though the OpenAPI does not
// require it.
//
// SIMPLIFIED: TS 28.111 V20.0.0 8.4.2. The parameters that carry the
// attributes AlarmRecord leaves out are left out, and so is
// changedAlarmAttributes, the optional list of the old values.
type NotifyChangedAlarmGeneral struct {
	NotificationHeader
	AlarmId           string            `json:"alarmId"`
	AlarmType         AlarmType         `json:"alarmType"`
	ProbableCause     ProbableCause     `json:"probableCause"`
	SpecificProblem   SpecificProblem   `json:"specificProblem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceivedSeverity"`
	AdditionalText    string            `json:"additionalText,omitempty"`
}

// NotifyAckStateChanged reports that the ackState of a record changed
// (TS 28.111 8.8). EventTime is its ackTime.
type NotifyAckStateChanged struct {
	NotificationHeader
	AlarmId           string            `json:"alarmId"`
	AlarmType         AlarmType         `json:"alarmType"`
	ProbableCause     ProbableCause     `json:"probableCause"`
	SpecificProblem   SpecificProblem   `json:"specificProblem,omitempty"`
	PerceivedSeverity PerceivedSeverity `json:"perceivedSeverity"`
	AckState          AckState          `json:"ackState"`
	AckUserId         string            `json:"ackUserId"`
	AckSystemId       string            `json:"ackSystemId,omitempty"`
}

// NotifyPotentialFaultyAlarmList reports that a part of the alarm list
// may be out of date (TS 28.111 8.10). Href is the base object of that
// part, or the SystemDN for the whole list.
type NotifyPotentialFaultyAlarmList struct {
	NotificationHeader
	Reason string `json:"reason"`
}

// The reason of notifyPotentialFaultyAlarmList that TS 28.111 8.10.2
// lists for a producer that has lost a managed element.
const ReasonServiceProviderNECommunicationError = "serviceprovider-NE communication error"

// NotifyAlarmListRebuilt reports that a part of the alarm list has been
// rebuilt or can be trusted again (TS 28.111 8.5). Href is the base
// object of that part, or the SystemDN for the whole list.
type NotifyAlarmListRebuilt struct {
	NotificationHeader
	Reason                        string                        `json:"reason"`
	AlarmListAlignmentRequirement AlarmListAlignmentRequirement `json:"alarmListAlignmentRequirement,omitempty"`
}

// The reasons of notifyAlarmListRebuilt that TS 28.111 8.5.2 lists.
const (
	ReasonSystemNECommunicationError = "System-NE communication error"
	ReasonSystemRestarts             = "System restarts"
	ReasonIndeterminate              = "indeterminate"
)

// AlarmListAlignmentRequirement says whether the consumer should read
// the alarm list again, because the producer failed to send some
// notifications (TS 28.111 8.5.2). The spelling is that of the schema
// AlarmListAlignmentRequirement of TS28111_FaultNrm.yaml; 8.5.2 writes
// the values in camel case.
type AlarmListAlignmentRequirement string

const (
	AlignmentRequired    AlarmListAlignmentRequirement = "ALIGNMENT_REQUIRED"
	AlignmentNotRequired AlarmListAlignmentRequirement = "ALIGNMENT_NOT_REQUIRED"
)
