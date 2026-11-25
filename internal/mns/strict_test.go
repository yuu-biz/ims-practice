package mns

import (
	"encoding/json"
	"strings"
	"testing"
)

// DecodeStrictly looks at every depth: into the attributes of a
// resource, the entries of a map and the items of a list.
func TestDecodeStrictlyChecksEveryName(t *testing.T) {
	const list = `{"id": "1", "attributes": {"operationalState": "ENABLED", "numOfAlarmRecords": 1,
	  "lastModification": "2026-11-24T10:15:02Z", "alarmRecords": {"scscf.1": {%s}}}}`
	const record = `"objectInstance": "SubNetwork=ims,ManagedElement=scscf", "notificationId": 1,
	  "alarmRaisedTime": "2026-11-24T10:15:02Z", "alarmType": "COMMUNICATIONS_ALARM",
	  "probableCause": "x", "perceivedSeverity": "MAJOR", "ackState": "UNACKNOWLEDGED"`
	var l AlarmListSingle
	if err := DecodeStrictly([]byte(strings.Replace(list, "%s", record, 1)), &l); err != nil {
		t.Fatal(err)
	}
	if l.Attributes.AlarmRecords["scscf.1"].PerceivedSeverity != Major {
		t.Errorf("decoded %+v", l)
	}
	err := DecodeStrictly([]byte(strings.Replace(list, "%s", record+`, "AckState": "ACKNOWLEDGED"`, 1)), &l)
	if err == nil || !strings.Contains(err.Error(), `"AckState" at /attributes/alarmRecords/scscf.1/`) {
		t.Errorf("a record with AckState: %v", err)
	}
	var s NtfSubscriptionControlSingle
	err = DecodeStrictly([]byte(`{"id": "1", "attributes": {"notificationRecipientAddress": "http://oss:8080/notifications", "scope": {"scopetype": "BASE_ALL"}}}`), &s)
	if err == nil || !strings.Contains(err.Error(), `"scopetype" at /attributes/scope/`) {
		t.Errorf("a scope with scopetype: %v", err)
	}
	for body, want := range map[string]string{
		`{"id": "1", "attributes": {"alarmRecords": {"scscf.1": null}}}`:                                                            "null at /attributes/alarmRecords/scscf.1",
		`{"id": "1", "attributes": {"notificationRecipientAddress": "http://oss:8080/notifications", "notificationTypes": [null]}}`: "null at /attributes/notificationTypes/0",
		`{"id": "1", "attributes": {"notificationRecipientAddress": "a", "notificationRecipientAddress": "b"}}`:                     `"notificationRecipientAddress" comes twice at /attributes/`,
		`{"id": "1", "objectClass": null}`: "null at /objectClass",
	} {
		var s NtfSubscriptionControlSingle
		var l AlarmListSingle
		var v any = &s
		if strings.Contains(body, "alarmRecords") {
			v = &l
		}
		if err := DecodeStrictly([]byte(body), v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error that says %q", body, err, want)
		}
	}
	if err := DecodeStrictly([]byte(`{"id": 1}`), &s); err == nil {
		t.Error("an id that is a number decoded")
	}
}

// The schema tag of a field says what the schema says of its attribute:
// the id of Top is required and may be null, and a json.RawMessage
// takes any value.
func TestDecodeStrictlyFollowsTheSchemaTag(t *testing.T) {
	var top Top
	if err := DecodeStrictly([]byte(`{"id": null, "objectClass": "AlarmList"}`), &top); err != nil || top.ObjectClass != "AlarmList" {
		t.Errorf("a null id: %v, %+v", err, top)
	}
	if err := DecodeStrictly([]byte(`{"objectClass": "AlarmList"}`), &top); err == nil || !strings.Contains(err.Error(), `no "id" at /`) {
		t.Errorf("no id: %v", err)
	}
	var s NtfSubscriptionControlSingle
	if err := DecodeStrictly([]byte(`{"id": "1", "attributes": null}`), &s); err == nil || !strings.Contains(err.Error(), "null at /attributes") {
		t.Errorf("null attributes: %v", err)
	}
	if err := DecodeStrictly([]byte(`null`), &s); err == nil {
		t.Error("a null body decoded")
	}
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := DecodeStrictly([]byte(`{"data": [null, {"Any": null}]}`), &raw); err != nil || string(raw.Data) != `[null, {"Any": null}]` {
		t.Errorf("a json.RawMessage: %v, %s", err, raw.Data)
	}
}
