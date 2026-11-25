package maintenance

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// pcrfDown is the alarm of the test node's P-CSCF when its Rx peer, the
// PCRF, is down.
var pcrfDown = AlarmIdentity{
	Role:            "pcscf",
	Peer:            "pcrf.ims.mnc001.mcc001.3gppnetwork.org",
	AlarmType:       CommunicationsAlarm,
	ProbableCause:   CommunicationsSubsystemFailure,
	SpecificProblem: "Diameter peer down",
}

func mustRaise(t *testing.T, n *Node, a Alarm) {
	t.Helper()
	if err := n.RaiseAlarm(a); err != nil {
		t.Fatal(err)
	}
}

func mustClear(t *testing.T, n *Node, id AlarmIdentity) {
	t.Helper()
	if err := n.ClearAlarm(id); err != nil {
		t.Fatal(err)
	}
}

func alarms(t *testing.T, tn *testNode, f AlarmFilter) AlarmList {
	t.Helper()
	l, err := tn.client.Alarms(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRaiseUpdateAndClearAnAlarm(t *testing.T) {
	tn := newTestNode(t)
	raised := tn.clock.Now()
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major, AdditionalText: "no answer to 3 DWR"})
	l := alarms(t, tn, AlarmFilter{})
	if l.Node != "pcscf-1" || l.Instance != "pcscf-1-7d9f" || l.NumOfAlarmRecords != 1 || len(l.AlarmRecords) != 1 || !l.LastModification.Equal(raised) {
		t.Fatalf("list = %+v", l)
	}
	r := l.AlarmRecords[0]
	if r.ObjectInstance != "ManagedElement=pcscf-1,ManagedFunction=pcscf,Peer=pcrf.ims.mnc001.mcc001.3gppnetwork.org" ||
		r.Role != "pcscf" || r.Peer != pcrfDown.Peer || r.AlarmType != CommunicationsAlarm ||
		r.ProbableCause != CommunicationsSubsystemFailure || r.SpecificProblem != "Diameter peer down" ||
		r.PerceivedSeverity != Major || r.AckState != Unacknowledged || !r.AlarmRaisedTime.Equal(raised) ||
		r.AlarmChangedTime != nil || r.AlarmClearedTime != nil || r.AckTime != nil {
		t.Errorf("record = %+v", r)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(r.AlarmID) {
		t.Errorf("alarm id %q", r.AlarmID)
	}

	// The same alarm again changes nothing (TS 28.111 6.14): a role may
	// raise it at each failed attempt.
	tn.clock.Advance(time.Second)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major, AdditionalText: "no answer to 3 DWR"})
	if l := alarms(t, tn, AlarmFilter{}); l.NumOfAlarmRecords != 1 || l.AlarmRecords[0].AlarmChangedTime != nil || !l.LastModification.Equal(raised) {
		t.Errorf("after the same alarm: %+v", l)
	}

	// A new severity updates the record, which keeps its id.
	tn.clock.Advance(time.Second)
	changed := tn.clock.Now()
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Critical, AdditionalText: "no peer left"})
	l = alarms(t, tn, AlarmFilter{})
	if got := l.AlarmRecords[0]; l.NumOfAlarmRecords != 1 || got.AlarmID != r.AlarmID || got.PerceivedSeverity != Critical ||
		got.AdditionalText != "no peer left" || got.AlarmChangedTime == nil || !got.AlarmChangedTime.Equal(changed) || !got.AlarmRaisedTime.Equal(raised) {
		t.Errorf("after the update: %+v", got)
	}

	// Cleared, it stays in the list until acknowledged (6.14).
	tn.clock.Advance(time.Second)
	clearedAt := tn.clock.Now()
	mustClear(t, tn.node, pcrfDown)
	mustClear(t, tn.node, pcrfDown)
	l = alarms(t, tn, AlarmFilter{})
	if got := l.AlarmRecords[0]; got.PerceivedSeverity != Cleared || got.AlarmClearedTime == nil || !got.AlarmClearedTime.Equal(clearedAt) || !l.LastModification.Equal(clearedAt) {
		t.Errorf("after the clear: %+v", l)
	}
	notCleared := false
	if l := alarms(t, tn, AlarmFilter{Cleared: &notCleared}); l.NumOfAlarmRecords != 1 || len(l.AlarmRecords) != 0 {
		t.Errorf("cleared=false: %+v", l)
	}

	// Acknowledged, the cleared alarm leaves the list (7.3.1.1).
	tn.clock.Advance(time.Second)
	acked, err := tn.client.SetAckState(context.Background(), r.AlarmID, AckStateRequest{AckState: Acknowledged, Operator: "test-operator"})
	if err != nil {
		t.Fatal(err)
	}
	if acked.AckState != Acknowledged || acked.AckUserID != "test-operator" || acked.AckTime == nil || !acked.AckTime.Equal(tn.clock.Now()) ||
		!acked.AlarmChangedTime.Equal(clearedAt) {
		t.Errorf("acknowledged: %+v", acked)
	}
	if l := alarms(t, tn, AlarmFilter{}); l.NumOfAlarmRecords != 0 || len(l.AlarmRecords) != 0 || !l.LastModification.Equal(tn.clock.Now()) {
		t.Errorf("after the acknowledgement: %+v", l)
	}
	if !strings.Contains(tn.log.String(), `"msg":"alarm raised"`) || !strings.Contains(tn.log.String(), `"operator":"test-operator"`) {
		t.Errorf("log: %s", tn.log)
	}
}

func TestAnAcknowledgedAlarmLeavesWhenItClears(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Minor})
	id := alarms(t, tn, AlarmFilter{}).AlarmRecords[0].AlarmID
	ctx := context.Background()
	for _, s := range []AckState{Acknowledged, Acknowledged, Unacknowledged, Acknowledged} {
		r, err := tn.client.SetAckState(ctx, id, AckStateRequest{AckState: s, Operator: "alice"})
		if err != nil || r.AckState != s {
			t.Fatalf("set %s: %+v %v", s, r, err)
		}
	}
	// An alarm that is not cleared stays, acknowledged or not.
	if l := alarms(t, tn, AlarmFilter{}); l.NumOfAlarmRecords != 1 || l.AlarmRecords[0].AckState != Acknowledged {
		t.Fatalf("acknowledged: %+v", l)
	}
	mustClear(t, tn.node, pcrfDown)
	if l := alarms(t, tn, AlarmFilter{}); l.NumOfAlarmRecords != 0 {
		t.Errorf("an acknowledged alarm that cleared is still listed: %+v", l)
	}
	// Raised again, it is a new record, unacknowledged.
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Minor})
	if r := alarms(t, tn, AlarmFilter{}).AlarmRecords[0]; r.AlarmID == id || r.AckState != Unacknowledged {
		t.Errorf("raised again: %+v", r)
	}
}

func TestAClearedAlarmRaisedAgainIsTheSameRecord(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	first := alarms(t, tn, AlarmFilter{}).AlarmRecords[0]
	mustClear(t, tn.node, pcrfDown)
	tn.clock.Advance(time.Minute)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	r := alarms(t, tn, AlarmFilter{}).AlarmRecords[0]
	if r.AlarmID != first.AlarmID || r.PerceivedSeverity != Major || r.AlarmClearedTime != nil || !r.AlarmRaisedTime.Equal(first.AlarmRaisedTime) {
		t.Errorf("raised again: %+v", r)
	}
}

// A cleared alarm that nobody acknowledges is acknowledged by the node
// at the end of the retention, and leaves the list as an acknowledged
// cleared alarm does (TS 28.111 6.14, 7.3.1.1).
func TestTheNodeAcknowledgesAClearedAlarmAtTheEndOfItsRetention(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	mustClear(t, tn.node, pcrfDown)
	cleared := tn.clock.Now()
	tn.clock.Advance(DefaultClearedAlarmRetention - time.Second)
	l := alarms(t, tn, AlarmFilter{})
	if l.NumOfAlarmRecords != 1 || l.AlarmRecords[0].AckState != Unacknowledged {
		t.Fatalf("before the end of the retention: %+v", l)
	}
	tn.clock.Advance(time.Hour)
	l = alarms(t, tn, AlarmFilter{})
	if l.NumOfAlarmRecords != 0 || !l.LastModification.Equal(cleared.Add(DefaultClearedAlarmRetention)) {
		t.Errorf("after the retention: %+v", l)
	}
	for _, line := range strings.Split(tn.log.String(), "\n") {
		if strings.Contains(line, `"msg":"alarm acknowledgement changed"`) {
			if !strings.Contains(line, `"operator":"retention"`) || !strings.Contains(line, `"ack_system_id":"pcscf-1"`) {
				t.Errorf("the acknowledgement at the end of the retention: %s", line)
			}
			return
		}
	}
	t.Errorf("no acknowledgement in the log: %s", tn.log)
}

// A record whose acknowledgement an operator took back before it
// cleared is left to the retention like one that nobody acknowledged.
func TestTheRetentionAcknowledgesARecordTakenBackToUnacknowledged(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	id := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0].AlarmID
	if _, err := tn.node.SetAckState(id, AckStateRequest{AckState: Acknowledged, Operator: "alice"}); err != nil {
		t.Fatal(err)
	}
	if _, err := tn.node.SetAckState(id, AckStateRequest{AckState: Unacknowledged, Operator: "alice"}); err != nil {
		t.Fatal(err)
	}
	mustClear(t, tn.node, pcrfDown)
	tn.clock.Advance(DefaultClearedAlarmRetention)
	if l := tn.node.Alarms(AlarmFilter{}); l.NumOfAlarmRecords != 0 {
		t.Errorf("a cleared alarm taken back to UNACKNOWLEDGED outlived the retention: %+v", l)
	}
}

// A condition that flaps updates one record; many conditions fill a
// bounded list, which drops a cleared record first, then the oldest
// (TS 28.111 7.3.2.1).
func TestTheAlarmListIsBounded(t *testing.T) {
	clock := newFakeClock()
	node, err := New(Config{
		Node:            "scscf-1",
		LogLevel:        new(slog.LevelVar),
		Functions:       []Function{{Role: "scscf"}},
		Configuration:   func() any { return struct{}{} },
		MaxAlarmRecords: 3,
		Clock:           clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	peer := func(i int) AlarmIdentity {
		id := pcrfDown
		id.Role = "scscf"
		id.Peer = Peer(fmt.Sprintf("hss-%d.ims.mnc001.mcc001.3gppnetwork.org", i))
		return id
	}
	for range 1000 {
		mustRaise(t, node, Alarm{AlarmIdentity: peer(1), PerceivedSeverity: Major})
		mustClear(t, node, peer(1))
	}
	if l := node.Alarms(AlarmFilter{}); l.NumOfAlarmRecords != 1 || l.DroppedRecords != 0 {
		t.Fatalf("a flapping alarm: %+v", l)
	}
	for i := 2; i <= 3; i++ {
		clock.Advance(time.Second)
		mustRaise(t, node, Alarm{AlarmIdentity: peer(i), PerceivedSeverity: Major})
	}
	// Full with peer 1 cleared and 2, 3 raised: peer 1 goes first,
	// although 2 is younger than it only by a second.
	clock.Advance(time.Second)
	mustRaise(t, node, Alarm{AlarmIdentity: peer(4), PerceivedSeverity: Major})
	l := node.Alarms(AlarmFilter{})
	if l.NumOfAlarmRecords != 3 || l.DroppedRecords != 1 || peersOf(l) != "4 3 2" {
		t.Fatalf("after the cleared one went: %+v", l)
	}
	// None cleared: the oldest goes.
	clock.Advance(time.Second)
	mustRaise(t, node, Alarm{AlarmIdentity: peer(5), PerceivedSeverity: Minor})
	l = node.Alarms(AlarmFilter{})
	if l.NumOfAlarmRecords != 3 || l.DroppedRecords != 2 || peersOf(l) != "4 3 5" {
		t.Errorf("after the oldest went: %+v", l)
	}
}

// peersOf is the number in the peer of each record, in the order of the
// list.
func peersOf(l AlarmList) string {
	var s []string
	for _, r := range l.AlarmRecords {
		s = append(s, strings.TrimSuffix(strings.TrimPrefix(string(r.Peer), "hss-"), ".ims.mnc001.mcc001.3gppnetwork.org"))
	}
	return strings.Join(s, " ")
}

func TestTheFiltersAndTheOrder(t *testing.T) {
	tn := newTestNode(t)
	raise := func(problem SpecificProblem, s PerceivedSeverity) {
		tn.clock.Advance(time.Second)
		id := AlarmIdentity{Role: "icscf", AlarmType: ProcessingErrorAlarm, ProbableCause: "SOFTWARE_ERROR", SpecificProblem: problem}
		mustRaise(t, tn.node, Alarm{AlarmIdentity: id, PerceivedSeverity: s})
	}
	raise("warning", Warning)
	raise("critical", Critical)
	raise("major old", Major)
	raise("indeterminate", Indeterminate)
	raise("minor", Minor)
	raise("major new", Major)
	raise("cleared", Major)
	mustClear(t, tn.node, AlarmIdentity{Role: "icscf", AlarmType: ProcessingErrorAlarm, ProbableCause: "SOFTWARE_ERROR", SpecificProblem: "cleared"})

	problems := func(l AlarmList) string {
		var s []string
		for _, r := range l.AlarmRecords {
			s = append(s, string(r.SpecificProblem))
		}
		return strings.Join(s, ", ")
	}
	yes, no := true, false
	for _, tc := range []struct {
		f    AlarmFilter
		want string
	}{
		{AlarmFilter{}, "critical, major new, major old, minor, warning, indeterminate, cleared"},
		{AlarmFilter{Cleared: &no}, "critical, major new, major old, minor, warning, indeterminate"},
		{AlarmFilter{Cleared: &yes}, "cleared"},
		{AlarmFilter{MinSeverity: Major}, "critical, major new, major old, indeterminate"},
		{AlarmFilter{MinSeverity: Warning}, "critical, major new, major old, minor, warning, indeterminate"},
		{AlarmFilter{MinSeverity: Critical, Cleared: &yes}, ""},
	} {
		l := alarms(t, tn, tc.f)
		if got := problems(l); got != tc.want || l.NumOfAlarmRecords != 7 {
			t.Errorf("%v: %q (%d in the list), want %q", tc.f.Query(), got, l.NumOfAlarmRecords, tc.want)
		}
	}

	for _, query := range []string{"min_severity=CLEARED", "min_severity=INDETERMINATE", "min_severity=major", "cleared=maybe", "severity=MAJOR", "cleared=true&cleared=false"} {
		rec := httptest.NewRecorder()
		tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AlarmsPath+"?"+query, nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("?%s = %d %s, want 400", query, rec.Code, rec.Body)
		}
	}
	q, _ := url.ParseQuery("cleared=false&min_severity=MINOR")
	if f, err := ParseAlarmFilter(q); err != nil || f.Query().Encode() != "cleared=false&min_severity=MINOR" {
		t.Errorf("round trip: %v %v", f, err)
	}
}

// The JSON names are part of the API: the OAM server and its screen read
// them.
func TestAlarmJSON(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major, AdditionalText: "no answer to 3 DWR"})
	id := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0].AlarmID
	tn.clock.Advance(time.Minute)
	acked, err := tn.node.SetAckState(id, AckStateRequest{AckState: Acknowledged, Operator: "test-operator", AckSystemID: "oam"})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, AlarmsPath, nil))
	want := `{"node":"pcscf-1","instance":"pcscf-1-7d9f","num_of_alarm_records":1,"last_modification":"2026-11-20T09:01:00Z","dropped_records":0,` +
		`"alarm_records":[{"alarm_id":"` + id + `","object_instance":"ManagedElement=pcscf-1,ManagedFunction=pcscf,Peer=pcrf.ims.mnc001.mcc001.3gppnetwork.org",` +
		`"role":"pcscf","peer":"pcrf.ims.mnc001.mcc001.3gppnetwork.org","notification_id":` + strconv.FormatInt(acked.NotificationID, 10) + `,"alarm_raised_time":"2026-11-20T09:00:00Z",` +
		`"alarm_type":"COMMUNICATIONS_ALARM","probable_cause":"COMMUNICATIONS_SUBSYSTEM_FAILURE","specific_problem":"Diameter peer down",` +
		`"perceived_severity":"MAJOR","additional_text":"no answer to 3 DWR","ack_state":"ACKNOWLEDGED","ack_time":"2026-11-20T09:01:00Z","ack_user_id":"test-operator","ack_system_id":"oam"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("GET alarms =\n%s\nwant\n%s", got, want)
	}
}

// Each change of a record gives it the id of a new notification (TS
// 28.111 7.4.1), and a change that changes nothing keeps the one it
// has. The acknowledgement keeps who changed it and through which system
// (7.3.1.3).
func TestEachChangeOfARecordHasANewNotificationID(t *testing.T) {
	tn := newTestNode(t)
	seen := map[int64]string{}
	notification := func(step string) AlarmRecord {
		t.Helper()
		l := tn.node.Alarms(AlarmFilter{})
		if len(l.AlarmRecords) != 1 {
			t.Fatalf("%s: %+v", step, l)
		}
		r := l.AlarmRecords[0]
		if r.NotificationID < 1 || r.NotificationID > maxNotificationID {
			t.Errorf("%s: notification id %d is out of 1 to 2^53 - 1", step, r.NotificationID)
		}
		if earlier, ok := seen[r.NotificationID]; ok {
			t.Errorf("%s: notification id %d again, first seen at %s", step, r.NotificationID, earlier)
		}
		seen[r.NotificationID] = step
		return r
	}
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	raised := notification("raised")
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	if r := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0]; r.NotificationID != raised.NotificationID {
		t.Errorf("raising the same alarm again changed the notification id")
	}
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Critical})
	notification("changed")
	if _, err := tn.node.SetAckState(raised.AlarmID, AckStateRequest{AckState: Acknowledged, Operator: "alice", AckSystemID: "oss-1"}); err != nil {
		t.Fatal(err)
	}
	if r := notification("acknowledged"); r.AckUserID != "alice" || r.AckSystemID != "oss-1" {
		t.Errorf("acknowledged: %+v", r)
	}
	if _, err := tn.node.SetAckState(raised.AlarmID, AckStateRequest{AckState: Unacknowledged, Operator: "bob"}); err != nil {
		t.Fatal(err)
	}
	if r := notification("unacknowledged"); r.AckUserID != "bob" || r.AckSystemID != "" {
		t.Errorf("the system is that of the last change, which named none: %+v", r)
	}
	mustClear(t, tn.node, pcrfDown)
	notification("cleared")
}

// One request acknowledges several alarms of the list: every one
// changes, or none does when one of them cannot.
func TestAcknowledgeSeveralAlarmsAtOnce(t *testing.T) {
	tn := newTestNode(t)
	ctx := context.Background()
	peer := func(i int) AlarmIdentity {
		id := pcrfDown
		id.Peer = Peer(fmt.Sprintf("pcrf-%d.ims.mnc001.mcc001.3gppnetwork.org", i))
		return id
	}
	ids := map[int]string{}
	for i := range 3 {
		mustRaise(t, tn.node, Alarm{AlarmIdentity: peer(i), PerceivedSeverity: Major})
		for _, r := range tn.node.Alarms(AlarmFilter{}).AlarmRecords {
			if r.Peer == peer(i).Peer {
				ids[i] = r.AlarmID
			}
		}
	}
	mustClear(t, tn.node, peer(2))
	before := tn.node.Alarms(AlarmFilter{})

	for _, tc := range []struct {
		name   string
		req    AckStatesRequest
		status int
		text   string
	}{
		{"an unknown alarm", AckStatesRequest{AckStates: map[string]AckStateRequest{
			ids[0]: {AckState: Acknowledged, Operator: "alice"}, "0011223344556677": {AckState: Acknowledged, Operator: "alice"}}}, http.StatusNotFound, "0011223344556677"},
		{"a state that there is not", AckStatesRequest{AckStates: map[string]AckStateRequest{
			ids[0]: {AckState: Acknowledged, Operator: "alice"}, ids[1]: {AckState: "SEEN", Operator: "alice"}}}, http.StatusBadRequest, ids[1]},
		{"an alarm without an operator", AckStatesRequest{AckStates: map[string]AckStateRequest{
			ids[0]: {AckState: Acknowledged, Operator: "alice"}, ids[1]: {AckState: Acknowledged}}}, http.StatusBadRequest, ids[1]},
		{"no alarm", AckStatesRequest{AckStates: map[string]AckStateRequest{}}, http.StatusBadRequest, "no alarm"},
	} {
		_, err := tn.client.SetAckStates(ctx, tc.req)
		wantAPIError(t, err, tc.status, tc.text)
		if after := tn.node.Alarms(AlarmFilter{}); !reflect.DeepEqual(after, before) {
			t.Errorf("%s changed the list:\n%+v\nwas\n%+v", tc.name, after, before)
		}
	}

	tn.clock.Advance(time.Second)
	change, err := tn.client.SetAckStates(ctx, AckStatesRequest{AckStates: map[string]AckStateRequest{
		ids[0]: {AckState: Acknowledged, Operator: "noc-operator", AckSystemID: "oss-1"},
		ids[2]: {AckState: Acknowledged, Operator: "noc-operator", AckSystemID: "oss-1"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(change.AlarmRecords) != 2 || !slices.IsSortedFunc(change.AlarmRecords, func(a, b AlarmRecord) int { return strings.Compare(a.AlarmID, b.AlarmID) }) {
		t.Fatalf("change: %+v", change)
	}
	for _, r := range change.AlarmRecords {
		if r.AckState != Acknowledged || r.AckUserID != "noc-operator" || r.AckSystemID != "oss-1" || r.AckTime == nil || !r.AckTime.Equal(tn.clock.Now()) {
			t.Errorf("acknowledged: %+v", r)
		}
	}
	// The cleared one has left the list; the other two stay.
	l := tn.node.Alarms(AlarmFilter{})
	if l.NumOfAlarmRecords != 2 {
		t.Fatalf("after the change: %+v", l)
	}
	for _, r := range l.AlarmRecords {
		if want := map[bool]AckState{true: Acknowledged, false: Unacknowledged}[r.AlarmID == ids[0]]; r.AckState != want {
			t.Errorf("%s: %s, want %s", r.Peer, r.AckState, want)
		}
	}
}

func TestAckStateErrors(t *testing.T) {
	tn := newTestNode(t)
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major})
	id := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0].AlarmID
	ctx := context.Background()
	_, err := tn.client.SetAckState(ctx, "0011223344556677", AckStateRequest{AckState: Acknowledged, Operator: "alice"})
	wantAPIError(t, err, http.StatusNotFound, "no such alarm")
	_, err = tn.client.SetAckState(ctx, id, AckStateRequest{AckState: "DONE", Operator: "alice"})
	wantAPIError(t, err, http.StatusBadRequest, "ack_state")
	// Each change names who made it, either way.
	for _, s := range []AckState{Acknowledged, Unacknowledged} {
		_, err = tn.client.SetAckState(ctx, id, AckStateRequest{AckState: s, AckSystemID: "oss-1"})
		wantAPIError(t, err, http.StatusBadRequest, "operator")
	}
	_, err = tn.client.SetAckStates(ctx, AckStatesRequest{AckStates: map[string]AckStateRequest{id: {AckState: Acknowledged}}})
	wantAPIError(t, err, http.StatusBadRequest, "operator")
	// A name of white space alone names nobody.
	_, err = tn.client.SetAckState(ctx, id, AckStateRequest{AckState: Acknowledged, Operator: " \t\u3000"})
	wantAPIError(t, err, http.StatusBadRequest, "operator")
	// The names are bounded, in characters, not bytes.
	long := strings.Repeat("ä", maxAckName)
	if _, err := tn.client.SetAckState(ctx, id, AckStateRequest{AckState: Unacknowledged, Operator: long, AckSystemID: long}); err != nil {
		t.Errorf("names of %d characters: %v", maxAckName, err)
	}
	_, err = tn.client.SetAckState(ctx, id, AckStateRequest{AckState: Acknowledged, Operator: long + "a"})
	wantAPIError(t, err, http.StatusBadRequest, "operator")
	_, err = tn.client.SetAckState(ctx, id, AckStateRequest{AckState: Acknowledged, Operator: "alice", AckSystemID: long + "a"})
	wantAPIError(t, err, http.StatusBadRequest, "ack_system_id")
	if r := tn.node.Alarms(AlarmFilter{}).AlarmRecords[0]; r.AckState != Unacknowledged {
		t.Errorf("a refused name changed the record: %+v", r)
	}
	rec := httptest.NewRecorder()
	tn.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, AckStatePath(id), strings.NewReader(`{"ack_state": "ACKNOWLEDGED", "ack_user_id": "alice"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an unknown field: %d %s", rec.Code, rec.Body)
	}
}

func TestRaiseAlarmRefusesWhatCouldNameASubscriberOrHoldASecret(t *testing.T) {
	tn := newTestNode(t)
	with := func(f func(a *Alarm)) Alarm {
		a := Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major}
		f(&a)
		return a
	}
	for name, a := range map[string]Alarm{
		"a role the node does not run":    with(func(a *Alarm) { a.Role = "hss" }),
		"a SIP URI as the peer":           with(func(a *Alarm) { a.Peer = "sip:001010000000001@ims.mnc001.mcc001.3gppnetwork.org" }),
		"a private identity as the peer":  with(func(a *Alarm) { a.Peer = "001010000000001@ims.mnc001.mcc001.3gppnetwork.org" }),
		"an IMSI as the peer":             with(func(a *Alarm) { a.Peer = "001010000000001" }),
		"a label of digits":               with(func(a *Alarm) { a.Peer = "2025550123.example" }),
		"a peer that is no host":          with(func(a *Alarm) { a.Peer = "the PCRF" }),
		"a bad port":                      with(func(a *Alarm) { a.Peer = "pcrf:99999" }),
		"a tel URI as the problem":        with(func(a *Alarm) { a.SpecificProblem = "tel:+12025550123 rejected" }),
		"an E.164 number in the problem":  with(func(a *Alarm) { a.SpecificProblem = "no route to +12025550123" }),
		"an MSISDN in the text":           with(func(a *Alarm) { a.AdditionalText = "first failure for 12025550123" }),
		"a password in the text":          with(func(a *Alarm) { a.AdditionalText = "login failed with password=test-password" }),
		"a K in the text":                 with(func(a *Alarm) { a.AdditionalText = "k: 465b5ce8b199b49faa5f0a2ee238a6bc" }),
		"a key in hexadecimal":            with(func(a *Alarm) { a.AdditionalText = "bad 465b5ce8b199b49faa5f0a2ee238a6bc" }),
		"a text too long":                 with(func(a *Alarm) { a.AdditionalText = strings.Repeat("x", 513) }),
		"a problem too long":              with(func(a *Alarm) { a.SpecificProblem = SpecificProblem(strings.Repeat("x", 81)) }),
		"the severity CLEARED":            with(func(a *Alarm) { a.PerceivedSeverity = Cleared }),
		"no severity":                     with(func(a *Alarm) { a.PerceivedSeverity = "" }),
		"a security alarm type":           with(func(a *Alarm) { a.AlarmType = "INTEGRITY_VIOLATION" }),
		"no alarm type":                   with(func(a *Alarm) { a.AlarmType = "" }),
		"a probable cause in lower case":  with(func(a *Alarm) { a.ProbableCause = "link failure" }),
		"a probable cause with an number": with(func(a *Alarm) { a.ProbableCause = "IMSI_001010000000001" }),
	} {
		if err := tn.node.RaiseAlarm(a); !errors.Is(err, ErrInvalidAlarm) {
			t.Errorf("%s: %v", name, err)
		} else if strings.Contains(err.Error(), "test-password") || strings.Contains(err.Error(), "465b5ce8") {
			t.Errorf("%s: the error shows the secret: %v", name, err)
		}
	}
	if l := tn.node.Alarms(AlarmFilter{}); l.NumOfAlarmRecords != 0 {
		t.Errorf("a refused alarm is listed: %+v", l)
	}
	if err := tn.node.ClearAlarm(AlarmIdentity{Role: "hss", AlarmType: CommunicationsAlarm, ProbableCause: linkFailure}); !errors.Is(err, ErrInvalidAlarm) {
		t.Errorf("clear for a role the node does not run: %v", err)
	}

	for _, peer := range []Peer{"hss.ims.mnc001.mcc001.3gppnetwork.org", "hss:3868", "10.0.0.7:6379", "[fd00::7]:3868", "fd00::7", "agw-1"} {
		a := with(func(a *Alarm) {
			a.Peer = peer
			a.AdditionalText = "no answer within 5s at 2026-11-20T09:00:00Z, port 50051"
		})
		if err := tn.node.RaiseAlarm(a); err != nil {
			t.Errorf("peer %s: %v", peer, err)
		}
	}
	if err := tn.node.RaiseAlarm(Alarm{AlarmIdentity: AlarmIdentity{AlarmType: OtherAlarm, ProbableCause: "INDETERMINATE"}, PerceivedSeverity: Indeterminate}); err != nil {
		t.Errorf("an alarm of the node with the least fields: %v", err)
	}
}

// linkFailure is a cause that Annex B lists and the package has
// no constant for: the role may use any cause in the spelling of Annex
// B.
const linkFailure ProbableCause = "LINK_FAILURE"

func TestCheckAlarmRecord(t *testing.T) {
	good := AlarmRecord{AlarmID: "0011223344556677", ObjectInstance: "ManagedElement=hss-1", AlarmType: CommunicationsAlarm, ProbableCause: linkFailure, PerceivedSeverity: Major, AckState: Unacknowledged, AdditionalText: "no answer"}
	if err := CheckAlarmRecord(good); err != nil {
		t.Errorf("a good record: %v", err)
	}
	for _, change := range []func(r *AlarmRecord){
		func(r *AlarmRecord) { r.AdditionalText = "opc=cd63cb71954a9f4e48a5994e37a02baf" },
		func(r *AlarmRecord) { r.ObjectInstance = "ManagedElement=hss-1,User=sip:alice@ims.example" },
		func(r *AlarmRecord) { r.SpecificProblem = "registration of 001010000000001" },
	} {
		r := good
		change(&r)
		if err := CheckAlarmRecord(r); err == nil {
			t.Errorf("%+v passed", r)
		}
	}
}

func TestNewChecksTheAlarmListBounds(t *testing.T) {
	for _, c := range []Config{{MaxAlarmRecords: -1}, {ClearedAlarmRetention: -time.Second}} {
		c.Node, c.LogLevel, c.Functions, c.Configuration = "n", new(slog.LevelVar), []Function{{Role: "pcscf"}}, func() any { return nil }
		if _, err := New(c); err == nil {
			t.Errorf("New took %+v", c)
		}
	}
}

func TestAlarmsUnderConcurrentUse(t *testing.T) {
	tn := newTestNode(t)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			id := pcrfDown
			id.Peer = Peer(fmt.Sprintf("pcrf-%d", i%3))
			for range 200 {
				_ = tn.node.RaiseAlarm(Alarm{AlarmIdentity: id, PerceivedSeverity: Major})
				l := tn.node.Alarms(AlarmFilter{})
				for _, r := range l.AlarmRecords {
					_, _ = tn.node.SetAckState(r.AlarmID, AckStateRequest{AckState: Acknowledged, Operator: "alice"})
				}
				_ = tn.node.ClearAlarm(id)
			}
		})
	}
	wg.Wait()
	if l := tn.node.Alarms(AlarmFilter{}); l.NumOfAlarmRecords > 3 {
		t.Errorf("three identities, %d records: %+v", l.NumOfAlarmRecords, l)
	}
}
