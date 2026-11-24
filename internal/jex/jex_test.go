package jex

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// notification is an alarm notification as the OAM server sends one,
// written for these tests; the conditions below are the kind of
// notificationFilter an OSS sets.
const notification = `{
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
  "additionalText": "no answer to 3 DWR",
  "rootCauseIndicator": false,
  "backUpObject": null,
  "monitoredAttributes": {"retries": 3, "peers": ["hss-1", "hss-2"], "lastPeer": {"name": "hss-1"}}
}`

func decode(t *testing.T, s string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestMatches(t *testing.T) {
	doc := decode(t, notification)
	for _, c := range []struct {
		expr string
		want bool
	}{
		// Paths, relative and absolute; the root is the context node.
		{`perceivedSeverity="MAJOR"`, true},
		{`/perceivedSeverity="MAJOR"`, true},
		{`perceivedSeverity="CRITICAL"`, false},
		{`perceivedSeverity!="CRITICAL"`, true},
		{`monitoredAttributes/lastPeer/name="hss-1"`, true},
		{`*/lastPeer/name="hss-1"`, true},
		{`additionalText`, true},
		{`clearUserId`, false},
		// and binds more tightly than or; parentheses change that.
		{`notificationType="notifyNewAlarm" and perceivedSeverity="CRITICAL" or alarmType="COMMUNICATIONS_ALARM"`, true},
		{`notificationType="notifyNewAlarm" and (perceivedSeverity="CRITICAL" or alarmType="EQUIPMENT_ALARM")`, false},
		{`notificationType="notifyClearedAlarm" or perceivedSeverity="MAJOR" and alarmType="COMMUNICATIONS_ALARM"`, true},
		{`(notificationType="notifyClearedAlarm" or perceivedSeverity="MAJOR") and alarmType="EQUIPMENT_ALARM"`, false},
		// not and contains.
		{`not(perceivedSeverity="CRITICAL")`, true},
		{`not(perceivedSeverity="MAJOR" and not(alarmType="EQUIPMENT_ALARM"))`, false},
		{`contains(href,"ManagedElement=scscf") and perceivedSeverity="MAJOR"`, true},
		{`contains(href,"ManagedElement=pcscf")`, false},
		{`contains(specificProblem,"peer")`, true},
		{`specificProblem[contains(.,"Diameter")] and perceivedSeverity="MAJOR"`, true},
		{`specificProblem[contains(.,"DNS")]`, false},
		{`contains(monitoredAttributes,"3")`, false},
		// Numbers, booleans and null.
		{`notificationId=4711`, true},
		{`notificationId=4711.0`, true},
		{`notificationId=4.711e3`, true},
		{`notificationId>=4711 and notificationId<4712`, true},
		{`notificationId>4711`, false},
		{`notificationId<=-1`, false},
		{`monitoredAttributes/retries>2`, true},
		{`rootCauseIndicator=false`, true},
		{`rootCauseIndicator!=true`, true},
		{`backUpObject=null`, true},
		{`backUpObject`, true},
		// A value of another type is neither equal nor unequal (7.5).
		{`notificationId="4711"`, false},
		{`notificationId!="4711"`, false},
		{`perceivedSeverity!=1`, false},
		{`rootCauseIndicator="false"`, false},
		{`backUpObject!=null`, false},
		{`backUpObject!="x"`, false},
		// No node, several nodes, or an object: false (C.2).
		{`clearUserId!="x"`, false},
		{`monitoredAttributes/peers="hss-1"`, false},
		{`monitoredAttributes/peers!="hss-3"`, false},
		{`monitoredAttributes/peers`, true},
		{`monitoredAttributes/lastPeer="hss-1"`, false},
		{`monitoredAttributes/lastPeer!="hss-1"`, false},
		// . is the context node, here the root, which is an object.
		{`.`, true},
		{`.="x"`, false},
	} {
		cond, err := ParseCondition(c.expr)
		if err != nil {
			t.Errorf("ParseCondition(%s): %v", c.expr, err)
			continue
		}
		if got := cond.Matches(doc); got != c.want {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
		if cond.String() != c.expr {
			t.Errorf("String() = %s, want %s", cond, c.expr)
		}
	}
}

// TestPathsInsidePredicates: a predicate may test several steps below
// its node, or the root with an absolute path (7.5).
func TestPathsInsidePredicates(t *testing.T) {
	doc := decode(t, `{
	  "records": [
	    {"severity": "MAJOR", "peer": {"name": "hss-1"}},
	    {"severity": "MINOR", "peer": {"name": "hss-2"}}
	  ],
	  "watched": "hss-2"
	}`)
	for _, c := range []struct {
		expr string
		want bool
	}{
		{`records[peer/name="hss-2" and severity="MINOR"]`, true},
		{`records[peer/name="hss-2" and severity="MAJOR"]`, false},
		{`records[severity="MAJOR"]/peer/name="hss-1"`, true},
		{`records[/watched="hss-2"]/severity="MAJOR"`, false},
		{`records[/watched="hss-2" and severity="MAJOR"]/peer/name="hss-1"`, true},
		{`records/severity="MAJOR"`, false},
	} {
		cond, err := ParseCondition(c.expr)
		if err != nil {
			t.Errorf("ParseCondition(%s): %v", c.expr, err)
			continue
		}
		if got := cond.Matches(doc); got != c.want {
			t.Errorf("%s = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestSyntaxErrors(t *testing.T) {
	for _, c := range []struct {
		expr   string
		offset int
	}{
		{``, 0},
		{`="MAJOR"`, 0},
		{`perceivedSeverity = "MAJOR"`, 17},
		{`perceivedSeverity="MAJOR"  and alarmType="OTHER"`, 25},
		{`perceivedSeverity="MAJOR" AND alarmType="OTHER"`, 25},
		{`perceivedSeverity="MAJOR`, 18},
		{`perceivedSeverity='MAJOR'`, 18},
		{`perceivedSeverity=MAJOR`, 18},
		{`notificationId>"4711"`, 15},
		{`notificationId>true`, 15},
		{`notificationId=01`, 16},
		{`notificationId=1.`, 16},
		{`(perceivedSeverity="MAJOR"`, 26},
		{`not perceivedSeverity="MAJOR"`, 3},
		{`contains(href)`, 13},
		{`contains(href,ManagedElement)`, 14},
		{`1abc="x"`, 0},
		{`-abc="x"`, 0},
		{`a//b`, 2},
		{`/`, 1},
		{`a[b="x"`, 7},
		{`a[b="x"][c="y"]`, 8},
		{`.[b="x"]`, 2},
		{`a|b`, 1},
		{"a=\"x\"\nb=\"y\"", 5},
	} {
		_, err := ParseCondition(c.expr)
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("ParseCondition(%q) = %v, want a SyntaxError", c.expr, err)
			continue
		}
		if se.Offset != c.offset {
			t.Errorf("ParseCondition(%q): %v; want the offset %d", c.expr, err, c.offset)
		}
	}
}

// TestPositionsAreUnsupported: a position is in the grammar, but not
// evaluated.
func TestPositionsAreUnsupported(t *testing.T) {
	for _, expr := range []string{`0`, `monitoredAttributes/peers[1]="hss-2"`, `a[0]`} {
		_, err := ParseCondition(expr)
		var ue *UnsupportedError
		if !errors.As(err, &ue) {
			t.Errorf("ParseCondition(%q) = %v, want an UnsupportedError", expr, err)
		}
	}
}

// TestLimits: an expression nested too deeply, in any of the three
// ways, or one that is too long, is refused at once as unsupported,
// rather than taking the stack without bound. One at the limit is read.
func TestLimits(t *testing.T) {
	const levels = maxDepth + 1
	for name, c := range map[string]struct{ expr, what string }{
		"parentheses": {strings.Repeat("(", levels) + `a="x"` + strings.Repeat(")", levels), "nesting"},
		"not":         {strings.Repeat("not(", levels) + `a="x"` + strings.Repeat(")", levels), "nesting"},
		"predicates":  {strings.Repeat("a[", levels) + "b" + strings.Repeat("]", levels), "nesting"},
		"a million":   {strings.Repeat("(", 1_000_000) + `a="x"` + strings.Repeat(")", 1_000_000), "longer"},
		"long":        {`a="` + strings.Repeat("x", maxLength) + `"`, "longer"},
	} {
		_, err := ParseCondition(c.expr)
		var ue *UnsupportedError
		if !errors.As(err, &ue) || !strings.Contains(ue.What, c.what) {
			t.Errorf("%s: %.100v; want an UnsupportedError about %s", name, err, c.what)
		}
	}
	atLimit := strings.Repeat("(", maxDepth-1) + `a="x"` + strings.Repeat(")", maxDepth-1)
	cond, err := ParseCondition(atLimit)
	if err != nil {
		t.Fatalf("%d levels: %v", maxDepth, err)
	}
	if !cond.Matches(map[string]any{"a": "x"}) {
		t.Errorf("%d levels do not match", maxDepth)
	}
}

// TestDocument evaluates a condition against a Go value, as the OAM
// server does with a notification it is about to send.
func TestDocument(t *testing.T) {
	type header struct {
		NotificationId   int64  `json:"notificationId"`
		NotificationType string `json:"notificationType"`
	}
	type body struct {
		header
		PerceivedSeverity string `json:"perceivedSeverity"`
	}
	doc, err := Document(body{header{9007199254740991, "notifyNewAlarm"}, "CRITICAL"})
	if err != nil {
		t.Fatal(err)
	}
	cond, err := ParseCondition(`notificationType="notifyNewAlarm" and perceivedSeverity="CRITICAL" and notificationId=9007199254740991`)
	if err != nil {
		t.Fatal(err)
	}
	if !cond.Matches(doc) {
		t.Errorf("%s does not match %v", cond, doc)
	}
	for _, v := range []any{[]int{1}, "text", nil} {
		if _, err := Document(v); err == nil {
			t.Errorf("Document(%#v) succeeded", v)
		}
	}
}
