package mns

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// The example bodies below are written from the schemas of the OpenAPI
// files that names_test.go names, with the values that the OAM server
// would write in Compose: the SubNetwork ims, the alarm list 1, and an
// S-CSCF that has lost its Diameter peer.

// alarmListGet is the answer to a GET of the alarm list.
const alarmListGet = `{
  "id": "1",
  "objectClass": "AlarmList",
  "objectInstance": "SubNetwork=ims,AlarmList=1",
  "attributes": {
    "operationalState": "ENABLED",
    "numOfAlarmRecords": 1,
    "lastModification": "2026-11-24T10:15:02Z",
    "alarmRecords": {
      "scscf.9f3c2a7b1d4e6058": {
        "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
        "notificationId": 4711,
        "alarmRaisedTime": "2026-11-24T10:15:02Z",
        "alarmChangedTime": "2026-11-24T10:16:40Z",
        "alarmType": "COMMUNICATIONS_ALARM",
        "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
        "specificProblem": "Diameter peer down",
        "perceivedSeverity": "MAJOR",
        "additionalText": "no answer to 3 DWR",
        "ackTime": "2026-11-24T10:17:00Z",
        "ackUserId": "noc-operator",
        "ackSystemId": "oss-1",
        "ackState": "ACKNOWLEDGED"
      }
    },
    "unreliableAlarmScope": "SubNetwork=ims,ManagedElement=pcscf"
  }
}`

const notifyNewAlarm = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
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
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
  "notificationId": 4712,
  "notificationType": "notifyChangedAlarmGeneral",
  "eventTime": "2026-11-24T10:16:40Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "specificProblem": "Diameter peer down",
  "perceivedSeverity": "CRITICAL",
  "additionalText": "no peer left for the realm"
}`

const notifyAckStateChanged = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
  "notificationId": 4713,
  "notificationType": "notifyAckStateChanged",
  "eventTime": "2026-11-24T10:17:00Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "specificProblem": "Diameter peer down",
  "perceivedSeverity": "CRITICAL",
  "ackState": "ACKNOWLEDGED",
  "ackUserId": "noc-operator",
  "ackSystemId": "oss-1"
}`

const notifyClearedAlarm = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
  "notificationId": 4714,
  "notificationType": "notifyClearedAlarm",
  "eventTime": "2026-11-24T10:20:31Z",
  "systemDN": "SubNetwork=ims",
  "alarmId": "scscf.9f3c2a7b1d4e6058",
  "alarmType": "COMMUNICATIONS_ALARM",
  "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
  "specificProblem": "Diameter peer down",
  "perceivedSeverity": "CLEARED"
}`

// notifyPotentialFaultyAlarmList: the OAM server has lost the P-CSCF.
const notifyPotentialFaultyAlarmList = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=pcscf",
  "notificationId": 90071992547409,
  "notificationType": "notifyPotentialFaultyAlarmList",
  "eventTime": "2026-11-24T10:21:00Z",
  "systemDN": "SubNetwork=ims",
  "reason": "serviceprovider-NE communication error"
}`

// notifyAlarmListRebuilt: the P-CSCF answers again.
const notifyAlarmListRebuilt = `{
  "href": "http://oam:8080/SubNetwork=ims/ManagedElement=pcscf",
  "notificationId": 90071992547410,
  "notificationType": "notifyAlarmListRebuilt",
  "eventTime": "2026-11-24T10:22:15Z",
  "systemDN": "SubNetwork=ims",
  "reason": "System-NE communication error",
  "alarmListAlignmentRequirement": "ALIGNMENT_REQUIRED"
}`

// subscriptionPut is the body of the PUT that creates a subscription.
const subscriptionPut = `{
  "id": "oss-1",
  "objectClass": "NtfSubscriptionControl",
  "objectInstance": "SubNetwork=ims,NtfSubscriptionControl=oss-1",
  "attributes": {
    "notificationRecipientAddress": "http://oss.example/notifications",
    "notificationTypes": ["notifyNewAlarm", "notifyClearedAlarm"],
    "scope": {"scopeType": "BASE_ALL"},
    "notificationFilter": "perceivedSeverity=\"CRITICAL\""
  }
}`

// errorGet is the answer to a GET with a query parameter of an unknown
// name (TS 32.158 6.6.5.2).
const errorGet = `{
  "type": "VALIDATION_ERROR",
  "reason": "QUERY_PARAM_NAMES_INVALID",
  "title": "The name of one or more query parameters is invalid.",
  "badQueryParams": ["scopeLevel"]
}`

// errorPatch is the answer to a JSON Merge Patch that writes a
// read-only attribute (TS 32.158 6.6.5.3.2).
const errorPatch = `{
  "type": "MODIFICATION_NOT_ALLOWED",
  "reason": "ATTRIBUTE_NOT_WRITABLE",
  "title": "An attribute is not writable.",
  "badAttributes": ["#/attributes/alarmRecords/scscf.9f3c2a7b1d4e6058/perceivedSeverity"]
}`

// roundTrip decodes body into a value of type T, refusing an attribute
// that the type does not have, encodes it again and checks that the
// JSON says the same.
func roundTrip[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader([]byte(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	again, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var want, got any
	if err := json.Unmarshal([]byte(body), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(again, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round trip changed the body\nwant %s\n got %s", body, again)
	}
	return v
}

func TestRoundTrips(t *testing.T) {
	t.Run("alarm list", func(t *testing.T) {
		l := roundTrip[AlarmListSingle](t, alarmListGet)
		r := l.Attributes.AlarmRecords["scscf.9f3c2a7b1d4e6058"]
		if r.PerceivedSeverity != Major || r.AckState != Acknowledged || r.NotificationId != 4711 {
			t.Errorf("record = %+v", r)
		}
	})
	t.Run("notifyNewAlarm", func(t *testing.T) {
		n := roundTrip[NotifyNewAlarm](t, notifyNewAlarm)
		if n.NotificationType != NotifyNewAlarmType || n.AlarmType != CommunicationsAlarm {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("notifyChangedAlarmGeneral", func(t *testing.T) {
		n := roundTrip[NotifyChangedAlarmGeneral](t, notifyChangedAlarmGeneral)
		if n.NotificationType != NotifyChangedAlarmGeneralType || n.PerceivedSeverity != Critical {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("notifyAckStateChanged", func(t *testing.T) {
		n := roundTrip[NotifyAckStateChanged](t, notifyAckStateChanged)
		if n.NotificationType != NotifyAckStateChangedType || n.AckState != Acknowledged {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("notifyClearedAlarm", func(t *testing.T) {
		n := roundTrip[NotifyClearedAlarm](t, notifyClearedAlarm)
		if n.NotificationType != NotifyClearedAlarmType || n.PerceivedSeverity != Cleared {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("notifyPotentialFaultyAlarmList", func(t *testing.T) {
		n := roundTrip[NotifyPotentialFaultyAlarmList](t, notifyPotentialFaultyAlarmList)
		if n.NotificationType != NotifyPotentialFaultyAlarmListType || n.Reason != ReasonServiceProviderNECommunicationError {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("notifyAlarmListRebuilt", func(t *testing.T) {
		n := roundTrip[NotifyAlarmListRebuilt](t, notifyAlarmListRebuilt)
		if n.Reason != ReasonSystemNECommunicationError || n.AlarmListAlignmentRequirement != AlignmentRequired {
			t.Errorf("notification = %+v", n)
		}
	})
	t.Run("subscription", func(t *testing.T) {
		s := roundTrip[NtfSubscriptionControlSingle](t, subscriptionPut)
		if s.Attributes.Scope.ScopeType != BaseAll || len(s.Attributes.NotificationTypes) != 2 {
			t.Errorf("subscription = %+v", s.Attributes)
		}
	})
	t.Run("error of a GET", func(t *testing.T) {
		e := roundTrip[ErrorResponseGet](t, errorGet)
		if e.Type != ValidationError || e.Reason != QueryParamNamesInvalid {
			t.Errorf("error = %+v", e)
		}
	})
	t.Run("error of a patch", func(t *testing.T) {
		e := roundTrip[ErrorResponseDefault](t, errorPatch)
		if e.Type != ModificationNotAllowed || e.Reason != AttributeNotWritable {
			t.Errorf("error = %+v", e)
		}
	})
}

// TestMandatoryAttributesAreWritten checks that the attributes that
// TS 28.111 makes mandatory go out even when they are zero: an empty
// alarm list still says that it holds no records, and a record that
// has never been notified still has its notificationId.
func TestMandatoryAttributesAreWritten(t *testing.T) {
	b, err := json.Marshal(AlarmListSingle{Top: Top{Id: "1"}, Attributes: &AlarmListAttributes{}})
	if err != nil {
		t.Fatal(err)
	}
	var l struct {
		Attributes map[string]any `json:"attributes"`
	}
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"operationalState", "numOfAlarmRecords", "lastModification"} {
		if _, ok := l.Attributes[name]; !ok {
			t.Errorf("%s misses %q", b, name)
		}
	}
	b, err = json.Marshal(AlarmRecord{})
	if err != nil {
		t.Fatal(err)
	}
	var r map[string]any
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"objectInstance", "notificationId", "alarmRaisedTime", "alarmType", "probableCause", "perceivedSeverity", "ackState"} {
		if _, ok := r[name]; !ok {
			t.Errorf("%s misses %q", b, name)
		}
	}
}
