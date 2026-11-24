package main

import (
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// decodeStrictly looks at every depth: into the attributes of a
// resource, the entries of a map and the items of a list.
func TestDecodeStrictlyChecksEveryName(t *testing.T) {
	const list = `{"id": "1", "attributes": {"operationalState": "ENABLED", "numOfAlarmRecords": 1,
	  "lastModification": "2026-11-24T10:15:02Z", "alarmRecords": {"scscf.1": {%s}}}}`
	const record = `"objectInstance": "SubNetwork=ims,ManagedElement=scscf", "notificationId": 1,
	  "alarmRaisedTime": "2026-11-24T10:15:02Z", "alarmType": "COMMUNICATIONS_ALARM",
	  "probableCause": "x", "perceivedSeverity": "MAJOR", "ackState": "UNACKNOWLEDGED"`
	var l mns.AlarmListSingle
	if err := decodeStrictly([]byte(strings.Replace(list, "%s", record, 1)), &l); err != nil {
		t.Fatal(err)
	}
	if l.Attributes.AlarmRecords["scscf.1"].PerceivedSeverity != mns.Major {
		t.Errorf("decoded %+v", l)
	}
	err := decodeStrictly([]byte(strings.Replace(list, "%s", record+`, "AckState": "ACKNOWLEDGED"`, 1)), &l)
	if err == nil || !strings.Contains(err.Error(), `"AckState" at /attributes/alarmRecords/scscf.1/`) {
		t.Errorf("a record with AckState: %v", err)
	}
	var s mns.NtfSubscriptionControlSingle
	err = decodeStrictly([]byte(`{"id": "1", "attributes": {"notificationRecipientAddress": "http://oss:8080/notifications", "scope": {"scopetype": "BASE_ALL"}}}`), &s)
	if err == nil || !strings.Contains(err.Error(), `"scopetype" at /attributes/scope/`) {
		t.Errorf("a scope with scopetype: %v", err)
	}
	for body, want := range map[string]string{
		`{"id": "1", "attributes": {"alarmRecords": {"scscf.1": null}}}`:                                                            "null at /attributes/alarmRecords/scscf.1",
		`{"id": "1", "attributes": {"notificationRecipientAddress": "http://oss:8080/notifications", "notificationTypes": [null]}}`: "null at /attributes/notificationTypes/0",
		`{"id": "1", "attributes": {"notificationRecipientAddress": "a", "notificationRecipientAddress": "b"}}`:                     `"notificationRecipientAddress" comes twice at /attributes/`,
		`{"id": "1", "objectClass": null}`: "null at /objectClass",
	} {
		var s mns.NtfSubscriptionControlSingle
		var l mns.AlarmListSingle
		var v any = &s
		if strings.Contains(body, "alarmRecords") {
			v = &l
		}
		if err := decodeStrictly([]byte(body), v); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error that says %q", body, err, want)
		}
	}
	if err := decodeStrictly([]byte(`{"id": 1}`), &s); err == nil {
		t.Error("an id that is a number decoded")
	}
}
