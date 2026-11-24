package mns

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// The OpenAPI files that the lists below were written from, with the
// info.version of each. The files are those of the archives of TS
// 28.111 V20.0.0, TS 28.532 V20.2.0 and TS 28.623 V20.3.0 on the 3GPP
// file server. TestOpenAPIFiles compares the lists with downloaded
// copies of the files.
var fileVersions = map[string]string{
	faultNrm:    "19.3.0",
	provMnS:     "19.2.0",
	subscrNrm:   "19.4.0",
	comDefs:     "19.6.0",
	genericNrm:  "20.1.0",
	faultNotifs: "19.3.0",
}

const (
	faultNrm    = "TS28111_FaultNrm.yaml"
	faultNotifs = "TS28111_FaultNotifications.yaml"
	provMnS     = "TS28532_ProvMnS.yaml"
	subscrNrm   = "TS28623_SubscriptionControlNrm.yaml"
	comDefs     = "TS28623_ComDefs.yaml"
	genericNrm  = "TS28623_GenericNrm.yaml"
)

// Why a type leaves out an attribute of its schema. Each reason but
// the last ones matches a SIMPLIFIED: comment on the type.
const (
	notKept       = "the nodes do not keep it (SIMPLIFIED: on AlarmRecord)"
	correlation   = "alarm correlation is not built (SIMPLIFIED: in the package comment)"
	admc          = "no alarm is cleared by the consumer (SIMPLIFIED: in the package comment)"
	security      = "no security alarm is raised (SIMPLIFIED: in the package comment)"
	reliable      = "no reliable notification transport (SIMPLIFIED: on NotificationHeader)"
	vsData        = "no vendor-specific data (SIMPLIFIED: on Top)"
	heartbeat     = "no heartbeat (SIMPLIFIED: on NtfSubscriptionControlSingle)"
	locked        = "the list is never locked (SIMPLIFIED: on AlarmListAttributes)"
	oldValues     = "the old values are not kept (SIMPLIFIED: on NotifyChangedAlarmGeneral)"
	nodeSelector  = "subscriptions select by notificationFilter (SIMPLIFIED: on Scope)"
	firstProblem  = "the producer reports the first problem only, which TS 32.158 6.6.3.1 allows"
	noMultiStatus = "written only with 207, which one problem never needs (TS 32.158 6.6.3)"
	mergePatch3GP = "only for the 3GPP JSON Merge Patch, which is not accepted (TS 32.158 6.6.3.3)"
)

// schemaNames lists the properties of a schema in an OpenAPI file, in
// the order of the file, for the Go type that stands for it. Schema is
// a path below components/schemas: a schema name, then "oneOf/<n>" to
// take one alternative, or the name of a property whose own
// properties are meant. The properties of an allOf are those of its
// parts in order, the header first; those of a oneOf without an index
// are those of all its alternatives.
type schemaNames struct {
	v        any
	file     string
	schema   string
	names    []string
	required []string
	// leftOut are the names that the Go type does not have, and why.
	leftOut map[string]string
}

// notificationHeader are the properties of NotificationHeader of
// TS28623_ComDefs.yaml, which each notification starts with.
var notificationHeader = []string{"href", "notificationId", "notificationType", "eventTime", "systemDN", "sequenceNo", "subscriptionId"}

var headerRequired = []string{"href", "notificationId", "notificationType", "eventTime", "systemDN"}

var headerLeftOut = map[string]string{"sequenceNo": reliable, "subscriptionId": reliable}

func header(names ...string) []string { return slices.Concat(notificationHeader, names) }

func headerAnd(required ...string) []string { return slices.Concat(headerRequired, required) }

func leftOutAnd(m map[string]string) map[string]string {
	out := maps.Clone(headerLeftOut)
	maps.Copy(out, m)
	return out
}

var schemas = []schemaNames{
	{
		v: AlarmRecord{}, file: faultNrm, schema: "AlarmRecord",
		names: []string{
			"objectInstance", "notificationId", "alarmRaisedTime", "alarmChangedTime", "alarmClearedTime",
			"alarmType", "probableCause", "specificProblem", "perceivedSeverity", "backedUpStatus",
			"backUpObject", "trendIndication", "thresholdinfo", "correlatedNotifications", "stateChangeDefinition",
			"monitoredAttributes", "proposedRepairActions", "additionalText", "additionalInformation", "rootCauseIndicator",
			"ackTime", "ackUserId", "ackSystemId", "ackState", "clearUserId",
			"clearSystemId", "serviceUser", "serviceProvider", "securityAlarmDetector", "clearingType",
		},
		leftOut: map[string]string{
			"backedUpStatus": notKept, "backUpObject": notKept, "trendIndication": notKept, "thresholdinfo": notKept,
			"stateChangeDefinition": notKept, "monitoredAttributes": notKept, "proposedRepairActions": notKept,
			"additionalInformation":   notKept,
			"correlatedNotifications": correlation, "rootCauseIndicator": correlation,
			"clearUserId": admc, "clearSystemId": admc, "clearingType": admc,
			"serviceUser": security, "serviceProvider": security, "securityAlarmDetector": security,
		},
	},
	{
		v: AlarmListSingle{}, file: faultNrm, schema: "AlarmList-Single",
		names:    []string{"id", "objectClass", "objectInstance", "VsDataContainer", "attributes"},
		required: []string{"id"},
		leftOut:  map[string]string{"VsDataContainer": vsData},
	},
	{
		v: AlarmListAttributes{}, file: faultNrm, schema: "AlarmList-Single/attributes",
		names:   []string{"administrativeState", "operationalState", "numOfAlarmRecords", "lastModification", "alarmRecords", "unreliableAlarmScope"},
		leftOut: map[string]string{"administrativeState": locked},
	},
	{
		v: Top{}, file: genericNrm, schema: "Top",
		names:    []string{"id", "objectClass", "objectInstance", "VsDataContainer"},
		required: []string{"id"},
		leftOut:  map[string]string{"VsDataContainer": vsData},
	},
	// The Resource of the ProvMnS operations, which each Single is.
	{
		v: AlarmListSingle{}, file: provMnS, schema: "Resource/oneOf/0",
		names:    []string{"id", "objectClass", "objectInstance", "attributes"},
		required: []string{"id"},
	},
	{
		v: NtfSubscriptionControlSingle{}, file: provMnS, schema: "Resource/oneOf/0",
		names:    []string{"id", "objectClass", "objectInstance", "attributes"},
		required: []string{"id"},
	},
	{
		v: NotificationHeader{}, file: comDefs, schema: "NotificationHeader",
		names: notificationHeader, required: headerRequired, leftOut: headerLeftOut,
	},
	{
		v: NotifyNewAlarm{}, file: faultNrm, schema: "NotifyNewAlarm",
		names: header(
			"alarmId", "alarmType", "probableCause", "specificProblem", "perceivedSeverity",
			"backedUpStatus", "backUpObject", "trendIndication", "thresholdInfo", "correlatedNotifications",
			"stateChangeDefinition", "monitoredAttributes", "proposedRepairActions", "additionalText", "additionalInformation",
			"rootCauseIndicator", "clearingType",
		),
		required: headerAnd("alarmId", "alarmType", "probableCause", "perceivedSeverity"),
		leftOut: leftOutAnd(map[string]string{
			"backedUpStatus": notKept, "backUpObject": notKept, "trendIndication": notKept, "thresholdInfo": notKept,
			"stateChangeDefinition": notKept, "monitoredAttributes": notKept, "proposedRepairActions": notKept,
			"additionalInformation":   notKept,
			"correlatedNotifications": correlation, "rootCauseIndicator": correlation, "clearingType": admc,
		}),
	},
	{
		v: NotifyClearedAlarm{}, file: faultNrm, schema: "NotifyClearedAlarm",
		names: header(
			"alarmId", "alarmType", "probableCause", "specificProblem", "perceivedSeverity",
			"correlatedNotifications", "clearUserId", "clearSystemId",
		),
		required: headerAnd("alarmId", "alarmType", "probableCause", "perceivedSeverity"),
		leftOut: leftOutAnd(map[string]string{
			"correlatedNotifications": correlation, "clearUserId": admc, "clearSystemId": admc,
		}),
	},
	{
		v: NotifyChangedAlarmGeneral{}, file: faultNrm, schema: "NotifyChangedAlarmGeneral",
		names: header(
			"alarmId", "alarmType", "probableCause", "specificProblem", "perceivedSeverity",
			"correlatedNotifications", "backedUpStatus", "backUpObject", "trendIndication", "thresholdInfo",
			"stateChangeDefinition", "monitoredAttributes", "proposedRepairActions", "additionalText", "additionalInformation",
			"rootCauseIndicator", "changedAlarmAttributes",
		),
		required: headerAnd("alarmId", "alarmType", "probableCause"),
		leftOut: leftOutAnd(map[string]string{
			"backedUpStatus": notKept, "backUpObject": notKept, "trendIndication": notKept, "thresholdInfo": notKept,
			"stateChangeDefinition": notKept, "monitoredAttributes": notKept, "proposedRepairActions": notKept,
			"additionalInformation":   notKept,
			"correlatedNotifications": correlation, "rootCauseIndicator": correlation,
			"changedAlarmAttributes": oldValues,
		}),
	},
	{
		v: NotifyAckStateChanged{}, file: faultNrm, schema: "NotifyAckStateChanged",
		names: header(
			"alarmId", "alarmType", "probableCause", "specificProblem", "perceivedSeverity",
			"ackState", "ackUserId", "ackSystemId",
		),
		required: headerAnd("alarmId", "alarmType", "probableCause", "perceivedSeverity", "ackState", "ackUserId"),
		leftOut:  headerLeftOut,
	},
	{
		v: NotifyPotentialFaultyAlarmList{}, file: faultNrm, schema: "NotifyPotentialFaultyAlarmList",
		names: header("reason"), required: headerAnd("reason"), leftOut: headerLeftOut,
	},
	{
		v: NotifyAlarmListRebuilt{}, file: faultNrm, schema: "NotifyAlarmListRebuilt",
		names: header("reason", "alarmListAlignmentRequirement"), required: headerAnd("reason"), leftOut: headerLeftOut,
	},
	{
		v: NtfSubscriptionControlSingle{}, file: subscrNrm, schema: "NtfSubscriptionControl-Single",
		names:    []string{"id", "objectClass", "objectInstance", "VsDataContainer", "attributes", "HeartbeatControl"},
		required: []string{"id"},
		leftOut:  map[string]string{"VsDataContainer": vsData, "HeartbeatControl": heartbeat},
	},
	{
		v: NtfSubscriptionControlAttributes{}, file: subscrNrm, schema: "NtfSubscriptionControl-Single/attributes",
		names: []string{"notificationRecipientAddress", "notificationTypes", "scope", "notificationFilter"},
	},
	{
		v: Scope{}, file: subscrNrm, schema: "Scope",
		names:   []string{"scopeType", "scopeLevel", "dataNodeSelector"},
		leftOut: map[string]string{"dataNodeSelector": nodeSelector},
	},
	{
		v: Scope{}, file: provMnS, schema: "Scope",
		names: []string{"scopeType", "scopeLevel"},
	},
	{
		v: ErrorResponseGet{}, file: comDefs, schema: "ErrorResponseGet",
		names:    []string{"status", "type", "reason", "title", "badQueryParams", "otherProblems"},
		required: []string{"type"},
		leftOut:  map[string]string{"status": noMultiStatus, "otherProblems": firstProblem},
	},
	{
		v: ErrorResponseDefault{}, file: comDefs, schema: "ErrorResponseDefault",
		names:    []string{"status", "type", "reason", "title", "badAttributes", "badObjects", "otherProblems"},
		required: []string{"type"},
		leftOut:  map[string]string{"status": noMultiStatus, "badObjects": mergePatch3GP, "otherProblems": firstProblem},
	},
	{
		v: ErrorResponse{}, file: comDefs, schema: "ErrorResponse",
		names: []string{"error"},
	},
	{
		v: ErrorResponseError{}, file: comDefs, schema: "ErrorResponse/error",
		names: []string{"errorInfo"},
	},
}

// TestAttributeNames checks that each type has, in the order of the
// file, exactly the properties of its schema that it does not leave
// out: a name spelt otherwise, or one that the schema does not have,
// fails, and so does one that is missing without a reason.
func TestAttributeNames(t *testing.T) {
	for _, s := range schemas {
		got := openapitest.JSONNames(s.v)
		for name := range s.leftOut {
			if !slices.Contains(s.names, name) {
				t.Errorf("%s of %s has no %q, which %T leaves out", s.schema, s.file, name, s.v)
			}
		}
		want := slices.DeleteFunc(slices.Clone(s.names), func(n string) bool { return s.leftOut[n] != "" })
		if !slices.Equal(got, want) {
			t.Errorf("%T has the attributes %q; %s of %s has %q, of which it leaves out %q", s.v, got, s.schema, s.file, s.names, slices.Sorted(maps.Keys(s.leftOut)))
		}
	}
}

// TestRequiredAttributes checks that a property the schema requires is
// always written: its field has neither omitempty nor omitzero.
func TestRequiredAttributes(t *testing.T) {
	for _, s := range schemas {
		options := jsonOptions(reflect.TypeOf(s.v))
		for _, name := range s.required {
			opts, ok := options[name]
			switch {
			case !ok:
				t.Errorf("%T has no %q, which %s of %s requires", s.v, name, s.schema, s.file)
			case strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero"):
				t.Errorf("%T may leave out %q, which %s of %s requires", s.v, name, s.schema, s.file)
			}
		}
	}
}

// jsonOptions maps the JSON name of each field of t, and of the
// structs it embeds, to the options of its tag.
func jsonOptions(t reflect.Type) map[string]string {
	out := map[string]string{}
	for f := range t.Fields() {
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" && f.Anonymous {
			maps.Copy(out, jsonOptions(f.Type))
			continue
		}
		out[name] = opts
	}
	return out
}

// enumValues lists the values of an enumeration in an OpenAPI file, in
// the order of the file, and the constants that stand for them.
type enumValues struct {
	file    string
	schema  string
	values  []string
	got     []string
	leftOut map[string]string
}

var enums = []enumValues{
	{
		file: faultNrm, schema: "AlarmType",
		values: []string{
			"COMMUNICATIONS_ALARM", "QUALITY_OF_SERVICE_ALARM", "PROCESSING_ERROR_ALARM", "EQUIPMENT_ALARM",
			"ENVIRONMENTAL_ALARM", "INTEGRITY_VIOLATION", "OPERATIONAL_VIOLATION", "PHYSICAL_VIOLATION",
			"SECURITY_SERVICE_OR_MECHANISM_VIOLATION", "TIME_DOMAIN_VIOLATION", "OTHER",
		},
		got: strs(CommunicationsAlarm, QualityOfServiceAlarm, ProcessingErrorAlarm, EquipmentAlarm, EnvironmentalAlarm, OtherAlarm),
		leftOut: map[string]string{
			"INTEGRITY_VIOLATION": security, "OPERATIONAL_VIOLATION": security, "PHYSICAL_VIOLATION": security,
			"SECURITY_SERVICE_OR_MECHANISM_VIOLATION": security, "TIME_DOMAIN_VIOLATION": security,
		},
	},
	{
		file: faultNrm, schema: "PerceivedSeverity",
		values: []string{"INDETERMINATE", "CRITICAL", "MAJOR", "MINOR", "WARNING", "CLEARED"},
		got:    strs(Indeterminate, Critical, Major, Minor, Warning, Cleared),
	},
	{
		file: faultNrm, schema: "AckState",
		values: []string{"ACKNOWLEDGED", "UNACKNOWLEDGED"},
		got:    strs(Acknowledged, Unacknowledged),
	},
	{
		file: faultNrm, schema: "AlarmNotificationTypes",
		values: []string{
			"notifyNewAlarm", "notifyChangedAlarmGeneral", "notifyAckStateChanged", "notifyCorrelatedNotificationChanged",
			"notifyComments", "notifyClearedAlarm", "notifyAlarmListRebuilt", "notifyPotentialFaultyAlarmList",
		},
		got: strs(NotifyNewAlarmType, NotifyChangedAlarmGeneralType, NotifyAckStateChangedType, NotifyClearedAlarmType,
			NotifyAlarmListRebuiltType, NotifyPotentialFaultyAlarmListType),
		leftOut: map[string]string{
			"notifyCorrelatedNotificationChanged": correlation,
			"notifyComments":                      "commenting alarms is not built (SIMPLIFIED: in the package comment)",
		},
	},
	{
		file: faultNrm, schema: "AlarmListAlignmentRequirement",
		values: []string{"ALIGNMENT_REQUIRED", "ALIGNMENT_NOT_REQUIRED"},
		got:    strs(AlignmentRequired, AlignmentNotRequired),
	},
	{
		file: comDefs, schema: "OperationalState",
		values: []string{"ENABLED", "DISABLED"},
		got:    strs(Enabled, Disabled),
	},
	{
		file: provMnS, schema: "ScopeType",
		values: []string{"BASE_ONLY", "BASE_NTH_LEVEL", "BASE_SUBTREE", "BASE_ALL"},
		got:    strs(BaseOnly, BaseAll),
		leftOut: map[string]string{
			"BASE_NTH_LEVEL": "no object contains another (SIMPLIFIED: on ScopeType)",
			"BASE_SUBTREE":   "no object contains another (SIMPLIFIED: on ScopeType)",
		},
	},
	{
		file: subscrNrm, schema: "Scope/oneOf/0/scopeType",
		values: []string{"BASE_ONLY", "BASE_ALL", "BASE_NTH_LEVEL", "BASE_SUBTREE"},
		got:    strs(BaseOnly, BaseAll),
		leftOut: map[string]string{
			"BASE_NTH_LEVEL": "no object contains another (SIMPLIFIED: on ScopeType)",
			"BASE_SUBTREE":   "no object contains another (SIMPLIFIED: on ScopeType)",
		},
	},
}

func strs[T ~string](values ...T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}

// TestEnumerations checks the constants against the values of each
// enumeration, as TestAttributeNames does the attributes.
func TestEnumerations(t *testing.T) {
	for _, e := range enums {
		for v := range e.leftOut {
			if !slices.Contains(e.values, v) {
				t.Errorf("%s of %s has no %q, which the constants leave out", e.schema, e.file, v)
			}
		}
		want := slices.DeleteFunc(slices.Clone(e.values), func(v string) bool { return e.leftOut[v] != "" })
		if !slices.Equal(e.got, want) {
			t.Errorf("the constants for %s are %q; %s has %q, of which they leave out %q", e.schema, e.got, e.file, e.values, slices.Sorted(maps.Keys(e.leftOut)))
		}
	}
}
