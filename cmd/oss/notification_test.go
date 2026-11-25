package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The bodies below are the six notifications that the OAM server of
// Compose sends about an S-CSCF that has lost its Diameter peer, written
// from the schemas of TS28111_FaultNrm.yaml.

const alarmHref = "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org"

const notifyNewAlarm = `{
  "href": "` + alarmHref + `",
  "notificationId": 4711,
  "notificationType": "notifyNewAlarm",
  "eventTime": "2026-11-24T10:15:02Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "specificProblem": "Diameter peer down",
  "perceivedSeverity": "MAJOR",
  "additionalText": "no answer to 3 DWR"
}`

const notifyChangedAlarmGeneral = `{
  "href": "` + alarmHref + `",
  "notificationId": 4712,
  "notificationType": "notifyChangedAlarmGeneral",
  "eventTime": "2026-11-24T10:16:40Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "perceivedSeverity": "CRITICAL",
  "additionalText": "no answer to 6 DWR"
}`

const notifyAckStateChanged = `{
  "href": "` + alarmHref + `",
  "notificationId": 4713,
  "notificationType": "notifyAckStateChanged",
  "eventTime": "2026-11-24T10:17:00Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "perceivedSeverity": "CRITICAL",
  "ackState": "ACKNOWLEDGED",
  "ackUserId": "noc-operator",
  "ackSystemId": "oss-1"
}`

const notifyClearedAlarm = `{
  "href": "` + alarmHref + `",
  "notificationId": 4714,
  "notificationType": "notifyClearedAlarm",
  "eventTime": "2026-11-24T10:20:31Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "perceivedSeverity": "CLEARED"
}`

const notifyPotentialFaultyAlarmList = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf",
  "notificationId": 8812,
  "notificationType": "notifyPotentialFaultyAlarmList",
  "eventTime": "2026-11-24T10:21:00Z",
  "systemDN": "SubNetwork=ims",
  "reason": "serviceprovider-NE communication error"
}`

const notifyAlarmListRebuilt = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf",
  "notificationId": 8813,
  "notificationType": "notifyAlarmListRebuilt",
  "eventTime": "2026-11-24T10:22:00Z",
  "systemDN": "SubNetwork=ims",
  "reason": "System-NE communication error",
  "alarmListAlignmentRequirement": "ALIGNMENT_REQUIRED"
}`

// edit returns body with its attribute name set to value, a JSON text,
// or removed when value is empty.
func edit(t *testing.T, body, name, value string) string {
	t.Helper()
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &attributes); err != nil {
		t.Fatal(err)
	}
	if value == "" {
		delete(attributes, name)
	} else {
		attributes[name] = json.RawMessage(value)
	}
	b, err := json.Marshal(attributes)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCheckNotificationTakesTheSixNotifications(t *testing.T) {
	for want, body := range map[mns.NotificationType]string{
		mns.NotifyNewAlarmType:                 notifyNewAlarm,
		mns.NotifyChangedAlarmGeneralType:      notifyChangedAlarmGeneral,
		mns.NotifyAckStateChangedType:          notifyAckStateChanged,
		mns.NotifyClearedAlarmType:             notifyClearedAlarm,
		mns.NotifyPotentialFaultyAlarmListType: notifyPotentialFaultyAlarmList,
		mns.NotifyAlarmListRebuiltType:         notifyAlarmListRebuilt,
	} {
		got, err := checkNotification([]byte(body))
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", want, got, err)
		}
	}
	// The optional attributes may be left out.
	for _, body := range []string{
		edit(t, edit(t, notifyNewAlarm, "specificProblem", ""), "additionalText", ""),
		edit(t, notifyAckStateChanged, "ackSystemId", ""),
		edit(t, notifyAlarmListRebuilt, "alarmListAlignmentRequirement", ""),
	} {
		if _, err := checkNotification([]byte(body)); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestCheckNotificationRefuses(t *testing.T) {
	for _, c := range []struct {
		name, body, reason string
	}{
		{"no JSON", `notifyNewAlarm`, "not a JSON object"},
		{"an array", `[` + notifyNewAlarm + `]`, "not a JSON object"},
		{"something after the object", notifyNewAlarm + `{}`, "not a JSON object"},
		{"no notificationType", edit(t, notifyNewAlarm, "notificationType", ""), "no notificationType"},
		{"a notificationType that is no string", edit(t, notifyNewAlarm, "notificationType", `1`), "no notificationType"},
		{"a notification of another management service", edit(t, notifyNewAlarm, "notificationType", `"notifyMOICreation"`), "not a notification of fault supervision"},
		{"a security alarm, which the OAM does not send", edit(t, notifyNewAlarm, "notificationType", `"notifyNewSecAlarm"`), "not a notification of fault supervision"},
		{"no href", edit(t, notifyNewAlarm, "href", ""), "no href"},
		{"no notificationId", edit(t, notifyNewAlarm, "notificationId", ""), "no notificationId"},
		{"no eventTime", edit(t, notifyClearedAlarm, "eventTime", ""), "no eventTime"},
		{"no systemDN", edit(t, notifyAlarmListRebuilt, "systemDN", ""), "no systemDN"},
		{"no alarmId", edit(t, notifyNewAlarm, "alarmId", ""), "no alarmId"},
		{"no perceivedSeverity in a change", edit(t, notifyChangedAlarmGeneral, "perceivedSeverity", ""), "no perceivedSeverity"},
		{"no ackUserId", edit(t, notifyAckStateChanged, "ackUserId", ""), "no ackUserId"},
		{"no reason", edit(t, notifyPotentialFaultyAlarmList, "reason", ""), "no reason"},
		{"null for a required attribute", edit(t, notifyNewAlarm, "probableCause", `null`), "null at /probableCause"},
		{"null for an optional attribute", edit(t, notifyNewAlarm, "specificProblem", `null`), "null at /specificProblem"},
		{"an attribute twice", strings.Replace(notifyNewAlarm, `"perceivedSeverity": "MAJOR"`, `"perceivedSeverity": "MAJOR", "perceivedSeverity": "CLEARED"`, 1), `"perceivedSeverity" comes twice`},
		{"a probableCause that is an integer", edit(t, notifyNewAlarm, "probableCause", `305`), "probableCause"},
		{"the subscriptionId of the reliable transport", edit(t, notifyNewAlarm, "subscriptionId", `"1"`), `"subscriptionId" at / is not an attribute of`},
		{"an optional attribute the records do not keep", edit(t, notifyNewAlarm, "backedUpStatus", `false`), `"backedUpStatus" at / is not an attribute of`},
		{"the alarmType of a security alarm", edit(t, notifyNewAlarm, "alarmType", `"INTEGRITY_VIOLATION"`), "alarmType is"},
		{"an empty alarmId", edit(t, notifyNewAlarm, "alarmId", `""`), "alarmId is empty"},
		{"an attribute of another body", edit(t, notifyNewAlarm, "reason", `"indeterminate"`), `"reason" at / is not an attribute of`},
		{"the body of another notificationType", edit(t, notifyAlarmListRebuilt, "notificationType", `"notifyPotentialFaultyAlarmList"`), `"alarmListAlignmentRequirement" at / is not an attribute of`},
		{"an attribute the OAM does not send", edit(t, notifyNewAlarm, "sequenceNo", `7`), `"sequenceNo" at / is not an attribute of`},
		{"a misspelt attribute", edit(t, notifyAckStateChanged, "ackSystemID", `"oss-1"`), `"ackSystemID" at / is not an attribute of`},
		{"a notificationId that is no integer", edit(t, notifyNewAlarm, "notificationId", `"4711"`), "notificationId"},
		{"an eventTime that is no date and time", edit(t, notifyNewAlarm, "eventTime", `"10:15"`), "parsing time"},
		{"an alarmType of no value", edit(t, notifyNewAlarm, "alarmType", `"communicationsAlarm"`), "alarmType is"},
		{"a perceivedSeverity of no value", edit(t, notifyNewAlarm, "perceivedSeverity", `"major"`), "perceivedSeverity is"},
		{"a cleared alarm that is not CLEARED", edit(t, notifyClearedAlarm, "perceivedSeverity", `"MAJOR"`), `perceivedSeverity is "MAJOR", not "CLEARED"`},
		{"a change to CLEARED", edit(t, notifyChangedAlarmGeneral, "perceivedSeverity", `"CLEARED"`), `perceivedSeverity is "CLEARED"`},
		{"an ackState of no value", edit(t, notifyAckStateChanged, "ackState", `"ACKED"`), "ackState is"},
		{"the camel case of TS 28.111 8.5.2", edit(t, notifyAlarmListRebuilt, "alarmListAlignmentRequirement", `"alignmentRequired"`), "alarmListAlignmentRequirement is"},
		{"an href that is no URI", edit(t, notifyNewAlarm, "href", `"http://oam:8080/%zz"`), "href"},
		{"an href that is a DN", edit(t, notifyNewAlarm, "href", `"SubNetwork=ims,ManagedElement=scscf"`), "not an http or https URI"},
		{"an href with a query", edit(t, notifyNewAlarm, "href", `"http://oam:8080/SubNetwork=ims?x=1"`), "more than a scheme"},
		{"an href that is the URI of a resource", edit(t, notifyNewAlarm, "href", `"http://oam:8080/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1"`), "href"},
		{"an href outside the systemDN", edit(t, notifyNewAlarm, "href", `"http://oam:8080/SubNetwork=other/ManagedElement=scscf"`), "not below the systemDN"},
		{"a systemDN that is no DN", edit(t, notifyNewAlarm, "systemDN", `"ims"`), "systemDN"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := checkNotification([]byte(c.body))
			if err == nil || !strings.Contains(err.Error(), c.reason) {
				t.Errorf("error %v, want one that says %q", err, c.reason)
			}
		})
	}
}
