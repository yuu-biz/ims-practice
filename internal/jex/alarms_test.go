package jex

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// alarmList is an AlarmList-Single as the OAM server answers a GET
// with, holding three records of two nodes.
const alarmList = `{
  "id": "1",
  "objectClass": "AlarmList",
  "objectInstance": "SubNetwork=ims,AlarmList=1",
  "attributes": {
    "operationalState": "ENABLED",
    "numOfAlarmRecords": 3,
    "lastModification": "2026-11-24T10:20:00Z",
    "alarmRecords": {
      "scscf.9f3c2a7b1d4e6058": {
        "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
        "notificationId": 4711,
        "alarmType": "COMMUNICATIONS_ALARM",
        "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
        "perceivedSeverity": "MAJOR",
        "ackState": "UNACKNOWLEDGED"
      },
      "scscf.0b1d2e3f40516273": {
        "objectInstance": "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf",
        "notificationId": 4800,
        "alarmType": "PROCESSING_ERROR_ALARM",
        "probableCause": "UNDERLYING_RESOURCE_UNAVAILABLE",
        "perceivedSeverity": "WARNING",
        "ackState": "ACKNOWLEDGED"
      },
      "icscf.5a6b7c8d9e0f1a2b": {
        "objectInstance": "SubNetwork=ims,ManagedElement=icscf,ManagedFunction=icscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
        "notificationId": 5120,
        "alarmType": "COMMUNICATIONS_ALARM",
        "probableCause": "COMMUNICATIONS_SUBSYSTEM_FAILURE",
        "perceivedSeverity": "CRITICAL",
        "ackState": "UNACKNOWLEDGED"
      }
    }
  }
}`

// TestSelectAlarmRecords uses selectors of the kinds that TS 28.111
// A.1.4 shows: by severity, by the alarmed object, and by the
// subtree of one node.
func TestSelectAlarmRecords(t *testing.T) {
	doc := decode(t, alarmList)
	for _, c := range []struct {
		expr string
		want []string
	}{
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR" or perceivedSeverity="CRITICAL"]`,
			[]string{"icscf.5a6b7c8d9e0f1a2b", "scscf.9f3c2a7b1d4e6058"}},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[objectInstance="SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf"]`,
			[]string{"scscf.0b1d2e3f40516273"}},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[contains(objectInstance,"SubNetwork=ims,ManagedElement=scscf,")]`,
			[]string{"scscf.0b1d2e3f40516273", "scscf.9f3c2a7b1d4e6058"}},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[ackState="UNACKNOWLEDGED" and not(alarmType="PROCESSING_ERROR_ALARM")]`,
			[]string{"icscf.5a6b7c8d9e0f1a2b", "scscf.9f3c2a7b1d4e6058"}},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[notificationId>=4800]`,
			[]string{"icscf.5a6b7c8d9e0f1a2b", "scscf.0b1d2e3f40516273"}},
		{`/AlarmList/attributes/alarmRecords/*`,
			[]string{"icscf.5a6b7c8d9e0f1a2b", "scscf.0b1d2e3f40516273", "scscf.9f3c2a7b1d4e6058"}},
		{`/AlarmList[id="1"]/attributes/alarmRecords/scscf.9f3c2a7b1d4e6058`,
			[]string{"scscf.9f3c2a7b1d4e6058"}},
		{`/AlarmList[id="1"]/attributes[numOfAlarmRecords>2]/alarmRecords/*[perceivedSeverity="CRITICAL"]`,
			[]string{"icscf.5a6b7c8d9e0f1a2b"}},
		// Another list, or no record that matches: nothing.
		{`/AlarmList[id="2"]/attributes/alarmRecords/*`, nil},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MINOR"]`, nil},
		{`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity=1]`, nil},
	} {
		s, err := ParseAlarmRecordSelector(c.expr)
		if err != nil {
			t.Errorf("ParseAlarmRecordSelector(%s): %v", c.expr, err)
			continue
		}
		if got := s.Select(doc); !slices.Equal(got, c.want) {
			t.Errorf("%s selects %q, want %q", c.expr, got, c.want)
		}
	}
}

// TestAlarmRecordSelectorRefuses: an expression of another form is
// unsupported, whether it is a condition, a path elsewhere, or a part
// of Jex advanced; one that breaks the grammar is a syntax error.
func TestAlarmRecordSelectorRefuses(t *testing.T) {
	for _, expr := range []string{
		`perceivedSeverity="MAJOR"`,
		`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR"] and /AlarmList`,
		`/AlarmList[id="1"]/attributes/alarmRecords`,
		`/AlarmList[id="1"]/attributes/alarmRecords/*/perceivedSeverity`,
		`/AlarmList[id="1"]/attributes/numOfAlarmRecords`,
		`/AlarmList[id="1"]/attributes/alarmRecords/.`,
		`AlarmList[id="1"]/attributes/alarmRecords/*`,
		`/SubNetwork/AlarmList/attributes/alarmRecords/*`,
		`/AlarmList/attributes/alarmRecords/*[perceivedSeverity="MAJOR"] | /AlarmList/attributes/alarmRecords/*[perceivedSeverity="MINOR"]`,
		`/AlarmList/attributes/(alarmRecords|numOfAlarmRecords)`,
		`/`,
		`/AlarmList/attributes/alarmRecords/*[0]`,
		"/AlarmList/attributes/alarmRecords/*[" + strings.Repeat("(", maxDepth) + "a" + strings.Repeat(")", maxDepth) + "]",
	} {
		_, err := ParseAlarmRecordSelector(expr)
		var ue *UnsupportedError
		if !errors.As(err, &ue) {
			t.Errorf("ParseAlarmRecordSelector(%s) = %v, want an UnsupportedError", expr, err)
		}
	}
	for _, expr := range []string{
		``,
		`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity = "MAJOR"]`,
		`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR"`,
		`/AlarmList[id=1"]/attributes/alarmRecords/*`,
		// An alarmId that begins with a digit is no DataNodeName (A.4).
		`/AlarmList[id="1"]/attributes/alarmRecords/1abc`,
	} {
		_, err := ParseAlarmRecordSelector(expr)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("ParseAlarmRecordSelector(%s) = %v, want a SyntaxError", expr, err)
		}
	}
}

func TestParseAlarmRecordFields(t *testing.T) {
	for _, c := range []struct {
		fields string
		want   []string
	}{
		{`/attributes/alarmRecords/scscf.9f3c2a7b1d4e6058`, []string{"scscf.9f3c2a7b1d4e6058"}},
		{`/attributes/alarmRecords/(scscf.9f3c2a7b1d4e6058 | icscf.5a6b7c8d9e0f1a2b)`, []string{"scscf.9f3c2a7b1d4e6058", "icscf.5a6b7c8d9e0f1a2b"}},
		{`/attributes/alarmRecords/(scscf.9f3c2a7b1d4e6058|icscf.5a6b7c8d9e0f1a2b)`, []string{"scscf.9f3c2a7b1d4e6058", "icscf.5a6b7c8d9e0f1a2b"}},
		{`/attributes/alarmRecords/a,/attributes/alarmRecords/(b | c)`, []string{"a", "b", "c"}},
		{`/attributes/alarmRecords/a~1b~0c`, []string{"a/b~c"}},
		{`/attributes/alarmRecords/(a~1b | c)`, []string{"a~1b", "c"}},
		{`/attributes/alarmRecords/1abc`, []string{"1abc"}},
	} {
		got, err := ParseAlarmRecordFields(c.fields)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("ParseAlarmRecordFields(%s) = %q, %v; want %q", c.fields, got, err, c.want)
		}
	}
	for _, fields := range []string{
		`/attributes/numOfAlarmRecords`,
		`/attributes/alarmRecords/a/perceivedSeverity`,
		`/id`,
		`/attributes/alarmRecords/a,/attributes`,
		`/attributes/alarmRecords`,
		"/attributes/alarmRecords/" + strings.Repeat("a", maxLength),
	} {
		_, err := ParseAlarmRecordFields(fields)
		var ue *UnsupportedError
		if !errors.As(err, &ue) {
			t.Errorf("ParseAlarmRecordFields(%s) = %v, want an UnsupportedError", fields, err)
		}
	}
	for _, c := range []struct {
		fields string
		offset int
	}{
		{``, 0},
		{`attributes/alarmRecords/a`, 0},
		{`/attributes/alarmRecords/`, 25},
		{`/attributes/alarmRecords/(a | b`, 31},
		{`/attributes/alarmRecords/(a || b)`, 25},
		{`/attributes/alarmRecords/()`, 25},
		{`/attributes/alarmRecords/(a  |  b)`, 25},
		{`/attributes/alarmRecords/a,`, 27},
	} {
		_, err := ParseAlarmRecordFields(c.fields)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("ParseAlarmRecordFields(%s) = %v, want a SyntaxError", c.fields, err)
			continue
		}
		if se.Offset != c.offset {
			t.Errorf("ParseAlarmRecordFields(%s): %v; want the offset %d", c.fields, err, c.offset)
		}
	}
}
