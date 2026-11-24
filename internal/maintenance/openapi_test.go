package maintenance

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/openapitest"
)

// checkedHandler is the handler of n with every exchange checked
// against openapi.yaml, which the tests of the package go through.
func checkedHandler(t *testing.T, n *Node) http.Handler {
	t.Helper()
	return openapitest.Load(t, "openapi.yaml").Handler(t, n.Handler())
}

// TestOpenAPI sends a request to each operation of openapi.yaml, with
// the answers that the description of the operation names, and checks
// that the handler takes exactly the methods of the document.
func TestOpenAPI(t *testing.T) {
	tn := newTestNode(t)
	doc := openapitest.Load(t, "openapi.yaml")
	h := doc.Handler(t, tn.node.Handler())
	mustRaise(t, tn.node, Alarm{AlarmIdentity: pcrfDown, PerceivedSeverity: Major, AdditionalText: "no answer to 3 DWR"})
	id := alarms(t, tn, AlarmFilter{}).AlarmRecords[0].AlarmID

	for _, r := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", StatePath, "", 200},
		{"HEAD", StatePath, "", 200},
		{"PUT", AdministrativeStatePath("pcscf"), `{"administrative_state": "SHUTTINGDOWN", "timeout_seconds": 30, "operator": "alice"}`, 200},
		{"PUT", AdministrativeStatePath("pcscf"), `{"administrative_state": "LOCKED"}`, 200},
		{"PUT", AdministrativeStatePath("pcscf"), `{"administrative_state": "SHUTTINGDOWN"}`, 409},
		{"PUT", AdministrativeStatePath("pcscf"), `{"administrative_state": "UNLOCKED"}`, 200},
		{"PUT", AdministrativeStatePath("pcscf"), `{"administrative_state": "OPEN"}`, 400},
		{"PUT", AdministrativeStatePath("mgcf"), `{"administrative_state": "LOCKED"}`, 404},
		{"GET", ConfigurationPath, "", 200},
		{"PATCH", ConfigurationPath, `{"settings": {"pcscf.max_sessions": 50}, "operator": "alice", "comment": "night"}`, 200},
		{"PATCH", ConfigurationPath, `{"settings": {"pcscf.max_sessions": 50}}`, 200},
		{"PATCH", ConfigurationPath, `{"settings": {"pcscf.max_sessions": 0}}`, 400},
		{"PATCH", ConfigurationPath, `{"settings": {"pcscf.retry_after": 10}, "generation": 1}`, 409},
		{"GET", LogLevelPath, "", 200},
		{"PUT", LogLevelPath, `{"level": "DEBUG", "operator": "alice"}`, 200},
		{"PUT", LogLevelPath, `{"level": "LOUD"}`, 400},
		{"GET", AlarmsPath, "", 200},
		{"GET", AlarmsPath + "?cleared=false&min_severity=MINOR", "", 200},
		{"GET", AlarmsPath + "?cleared=true", "", 200},
		{"GET", AlarmsPath + "?severity=MAJOR", "", 400},
		{"PATCH", AlarmsPath, `{"ack_states": {"` + id + `": {"ack_state": "UNACKNOWLEDGED", "operator": "alice", "ack_system_id": "oss-1"}}}`, 200},
		{"PATCH", AlarmsPath, `{"ack_states": {"` + id + `": {"ack_state": "SEEN"}}}`, 400},
		{"PATCH", AlarmsPath, `{"ack_states": {"0000000000000000": {"ack_state": "ACKNOWLEDGED"}}}`, 404},
		{"PUT", AckStatePath(id), `{"ack_state": "ACKNOWLEDGED", "operator": "alice"}`, 200},
		{"PUT", AckStatePath(id), `{"ack_state": "SEEN"}`, 400},
		{"PUT", AckStatePath("0000000000000000"), `{"ack_state": "ACKNOWLEDGED"}`, 404},
		{"GET", PathPrefix + "counters", "", 404},
		{"DELETE", StatePath, "", 405},
	} {
		rec := httptest.NewRecorder()
		var body io.Reader
		if r.body != "" {
			body = strings.NewReader(r.body)
		}
		h.ServeHTTP(rec, httptest.NewRequest(r.method, r.path, body))
		if rec.Code != r.status {
			t.Errorf("%s %s %s = %d %s, want %d", r.method, r.path, r.body, rec.Code, rec.Body, r.status)
		}
	}
	doc.CheckRoutes(t, tn.node.Handler())
	doc.CheckCovered(t)
}
