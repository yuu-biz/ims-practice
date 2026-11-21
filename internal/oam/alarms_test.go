package oam

import (
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

func raise(t *testing.T, n *fakeNode, role string, peer maintenance.Peer, s maintenance.PerceivedSeverity) {
	t.Helper()
	err := n.node.RaiseAlarm(maintenance.Alarm{
		AlarmIdentity:     diameterPeerDown(role, peer),
		PerceivedSeverity: s,
		AdditionalText:    "no answer to 3 DWR",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func diameterPeerDown(role string, peer maintenance.Peer) maintenance.AlarmIdentity {
	return maintenance.AlarmIdentity{
		Role:            role,
		Peer:            peer,
		AlarmType:       maintenance.CommunicationsAlarm,
		ProbableCause:   maintenance.CommunicationsSubsystemFailure,
		SpecificProblem: "Diameter peer down",
	}
}

func TestAlarmsOfEveryNodeMergedWithTheOneThatFailed(t *testing.T) {
	p1 := newFakeNode(t, "pcscf-1", []string{"pcscf"})
	p2 := newFakeNode(t, "pcscf-2", []string{"pcscf"})
	cscf := newFakeNode(t, "cscf-1", []string{"icscf", "scscf"})
	raise(t, p1, "pcscf", "pcrf.ims.mnc001.mcc001.3gppnetwork.org", maintenance.Major)
	raise(t, cscf, "scscf", "hss.ims.mnc001.mcc001.3gppnetwork.org", maintenance.Critical)
	raise(t, cscf, "icscf", "hss.ims.mnc001.mcc001.3gppnetwork.org", maintenance.Major)
	p2.server.Close()
	ts := newTestServer(t, Config{}, p1, p2, cscf)

	status, body := ts.call(t, "GET", "/oam/v1/alarms", "")
	if status != http.StatusOK {
		t.Fatalf("GET alarms: %d %s", status, body)
	}
	got := decodeAs[Alarms](t, body)
	if got.Succeeded != 2 || got.Failed != 1 || len(got.Nodes) != 3 {
		t.Fatalf("alarms: %s", body)
	}
	for i, want := range []string{"pcscf-1", "pcscf-2", "cscf-1"} {
		if got.Nodes[i].Node != want {
			t.Errorf("node %d is %s, want %s in the order of the inventory", i, got.Nodes[i].Node, want)
		}
	}
	// A node that does not answer is a failed entry, and no alarm is made
	// up for it.
	if r := got.Nodes[1]; r.Error == nil || r.Result != nil {
		t.Errorf("the closed node: %+v", r)
	}
	if r := got.Nodes[2].Result; r == nil || r.NumOfAlarmRecords != 2 || r.Listed != 2 || r.Instance != "cscf-1-0" {
		t.Errorf("cscf-1: %s", body)
	}
	var order []string
	for _, a := range got.Alarms {
		order = append(order, a.Node+" "+a.Role+" "+string(a.PerceivedSeverity))
	}
	// The most severe first; within a severity the newest first, and
	// for the same time the order of the inventory.
	if strings.Join(order, ", ") != "cscf-1 scscf CRITICAL, pcscf-1 pcscf MAJOR, cscf-1 icscf MAJOR" &&
		strings.Join(order, ", ") != "cscf-1 scscf CRITICAL, cscf-1 icscf MAJOR, pcscf-1 pcscf MAJOR" {
		t.Errorf("order: %q", order)
	}
	if a := got.Alarms[0]; a.ObjectInstance != "ManagedElement=cscf-1,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org" || a.AlarmID == "" {
		t.Errorf("the first alarm: %+v", a)
	}
	if !strings.Contains(string(body), `{"node":"cscf-1","alarm_id":`) {
		t.Errorf("a record does not carry its node: %s", body)
	}
}

func TestTheAlarmFilterGoesToEveryNode(t *testing.T) {
	var asked atomic.Int64
	a := newFakeNode(t, "scscf-1", []string{"scscf"})
	b := newFakeNode(t, "scscf-2", []string{"scscf"})
	for _, n := range []*fakeNode{a, b} {
		n.wrap = func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				asked.Add(1)
				h.ServeHTTP(w, r)
			})
		}
	}
	raise(t, a, "scscf", "hss-1", maintenance.Critical)
	raise(t, b, "scscf", "hss-1", maintenance.Minor)
	raise(t, b, "scscf", "hss-2", maintenance.Major)
	if err := b.node.ClearAlarm(diameterPeerDown("scscf", "hss-2")); err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, Config{}, a, b)

	_, body := ts.call(t, "GET", "/oam/v1/alarms?min_severity=MAJOR", "")
	got := decodeAs[Alarms](t, body)
	if len(got.Alarms) != 1 || got.Alarms[0].Node != "scscf-1" || got.Nodes[1].Result.NumOfAlarmRecords != 2 || got.Nodes[1].Result.Listed != 0 {
		t.Errorf("min_severity=MAJOR: %s", body)
	}
	_, body = ts.call(t, "GET", "/oam/v1/alarms?cleared=true", "")
	if got := decodeAs[Alarms](t, body); len(got.Alarms) != 1 || got.Alarms[0].Peer != "hss-2" || got.Alarms[0].PerceivedSeverity != maintenance.Cleared {
		t.Errorf("cleared=true: %s", body)
	}

	before := asked.Load()
	for _, query := range []string{"min_severity=LOW", "cleared=yes!", "node=scscf-1"} {
		status, body := ts.call(t, "GET", "/oam/v1/alarms?"+query, "")
		if status != http.StatusBadRequest || decodeAs[maintenance.ErrorBody](t, body).Error == "" {
			t.Errorf("?%s: %d %s", query, status, body)
		}
	}
	if asked.Load() != before {
		t.Error("a bad filter went to the nodes")
	}
}

func TestAcknowledgeAnAlarmOfOneNode(t *testing.T) {
	n := newFakeNode(t, "hss-1", []string{"hss"})
	other := newFakeNode(t, "hss-2", []string{"hss"})
	raise(t, n, "hss", "scscf-1", maintenance.Major)
	ts := newTestServer(t, Config{}, n, other)
	id := n.node.Alarms(maintenance.AlarmFilter{}).AlarmRecords[0].AlarmID

	status, body := ts.call(t, "PUT", "/oam/v1/nodes/hss-1/alarms/"+id+"/ack-state", `{"ack_state": "ACKNOWLEDGED", "operator": "test-operator"}`)
	got := decodeAs[FanOut[maintenance.AlarmRecord]](t, body)
	if status != http.StatusOK || got.Succeeded != 1 || len(got.Nodes) != 1 || got.Nodes[0].Result.AckState != maintenance.Acknowledged || got.Nodes[0].Result.AckUserID != "test-operator" {
		t.Fatalf("acknowledge: %d %s", status, body)
	}
	if r := n.node.Alarms(maintenance.AlarmFilter{}).AlarmRecords[0]; r.AckState != maintenance.Acknowledged {
		t.Errorf("the node's record: %+v", r)
	}
	if !strings.Contains(ts.log.String(), `"operator":"test-operator"`) {
		t.Errorf("the log does not name the operator: %s", ts.log)
	}

	// The alarm is hss-1's: on hss-2 the id is the node's 404.
	_, body = ts.call(t, "PUT", "/oam/v1/nodes/hss-2/alarms/"+id+"/ack-state", `{"ack_state": "ACKNOWLEDGED"}`)
	if got := decodeAs[FanOut[maintenance.AlarmRecord]](t, body); got.Failed != 1 || got.Nodes[0].Error.StatusCode != http.StatusNotFound {
		t.Errorf("another node: %s", body)
	}
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"PUT", "/oam/v1/nodes/hss-9/alarms/" + id + "/ack-state", `{"ack_state": "ACKNOWLEDGED"}`, http.StatusNotFound},
		{"PUT", "/oam/v1/nodes/hss-1/alarms/" + id + "/ack-state", `{"ack_state": "ACKNOWLEDGED", "force": true}`, http.StatusBadRequest},
		{"DELETE", "/oam/v1/alarms", "", http.StatusMethodNotAllowed},
	} {
		if status, body := ts.call(t, tc.method, tc.path, tc.body); status != tc.want {
			t.Errorf("%s %s: %d %s, want %d", tc.method, tc.path, status, body, tc.want)
		}
	}
}

func TestAlarmsThatMayHoldASecretAreNotPassedOn(t *testing.T) {
	good := newFakeNode(t, "hss-1", []string{"hss"})
	leaky := newFakeNode(t, "hss-2", []string{"hss"})
	raise(t, good, "hss", "scscf-1", maintenance.Minor)
	// A node of another build that does not check its alarm texts.
	leaky.wrap = func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{
				"node": "hss-2", "num_of_alarm_records": 1, "last_modification": testTime, "dropped_records": 0,
				"alarm_records": []map[string]any{{
					"alarm_id": "0011223344556677", "object_instance": "ManagedElement=hss-2", "alarm_raised_time": testTime,
					"alarm_type": "PROCESSING_ERROR_ALARM", "probable_cause": "SOFTWARE_ERROR", "perceived_severity": "MAJOR",
					"additional_text": "import failed for password=test-password", "ack_state": "UNACKNOWLEDGED",
				}},
			})
		})
	}
	ts := newTestServer(t, Config{}, good, leaky)

	_, body := ts.call(t, "GET", "/oam/v1/alarms", "")
	got := decodeAs[Alarms](t, body)
	if got.Succeeded != 1 || got.Failed != 1 || got.Nodes[1].Error == nil || len(got.Alarms) != 1 || got.Alarms[0].Node != "hss-1" {
		t.Errorf("alarms: %s", body)
	}
	if strings.Contains(string(body), "test-password") {
		t.Errorf("the answer holds a secret: %s", body)
	}
}
