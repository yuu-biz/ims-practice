package oam

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// TestOpenAPI sends a request to each operation of openapi.yaml, with
// the answers that the description of the operation names, and checks
// that the handler takes exactly the methods of the document. The OAM
// operates a P-CSCF node and an HSS node, and the provisioning API of
// the HSS itself.
func TestOpenAPI(t *testing.T) {
	pcscf := newFakeNode(t, "pcscf-1", []string{"pcscf"}, retryAfter)
	raise(t, pcscf, "pcscf", "pcrf."+testRealm, maintenance.Major)
	hssNode := newHSSNode(t, true)
	history, err := NewFileChangeStore(filepath.Join(t.TempDir(), "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	backups, err := NewFileBackupStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, Config{
		HSSProvisioning: newRealHSS(t).URL,
		History:         history,
		Backups:         backups,
		Nodes:           []Endpoint{{Name: "hss-1", Roles: []string{"hss"}, URL: hssNode.server.URL}},
	}, pcscf)
	send := func(method, path, body string, status int) []byte {
		t.Helper()
		got, b := ts.call(t, method, path, body)
		if got != status {
			t.Errorf("%s %s %s = %d %s, want %d", method, path, body, got, b, status)
		}
		return b
	}
	// result is the result of the first node of a fan-out.
	result := func(b []byte) map[string]any {
		t.Helper()
		f := decodeAs[FanOut[map[string]any]](t, b)
		if len(f.Nodes) == 0 || f.Nodes[0].Result == nil {
			t.Fatalf("no result in %s", b)
		}
		return *f.Nodes[0].Result
	}
	const v1 = "/oam/v1/"
	impi := impiOf(1)
	record := `{"private_identity": "` + impi + `", "public_identities": ["sip:` + impi + `"], "password": "test-password"}`

	send("GET", v1+"inventory", "", 200)
	send("GET", v1+"state", "", 200)
	for _, scope := range []struct{ known, unknown string }{{"nodes/pcscf-1/", "nodes/mgcf-1/"}, {"roles/pcscf/", "roles/mgcf/"}} {
		send("GET", v1+scope.known+"state", "", 200)
		send("GET", v1+scope.unknown+"state", "", 404)
		send("GET", v1+scope.known+"log-level", "", 200)
		send("GET", v1+scope.unknown+"log-level", "", 404)
		send("PUT", v1+scope.known+"log-level", `{"level": "DEBUG", "operator": "test-operator"}`, 200)
		send("PUT", v1+scope.known+"log-level", `{"lvl": 1}`, 400)
		send("PUT", v1+scope.unknown+"log-level", `{"level": "DEBUG"}`, 404)
		send("GET", v1+scope.known+"config", "", 200)
		send("GET", v1+scope.unknown+"config", "", 404)
		send("PATCH", v1+scope.known+"config", `{"settings": {"`+retryAfter+`": 7}, "force": true}`, 400)
		send("PATCH", v1+scope.unknown+"config", `{"settings": {"`+retryAfter+`": 7}}`, 404)
	}
	send("PUT", v1+"nodes/pcscf-1/functions/pcscf/administrative-state", `{"administrative_state": "LOCKED"}`, 200)
	send("PUT", v1+"nodes/pcscf-1/functions/pcscf/administrative-state", `{"state": "LOCKED"}`, 400)
	send("PUT", v1+"nodes/mgcf-1/functions/mgcf/administrative-state", `{"administrative_state": "LOCKED"}`, 404)
	send("PUT", v1+"roles/pcscf/administrative-state", `{"administrative_state": "UNLOCKED", "operator": "test-operator"}`, 200)
	send("PUT", v1+"roles/pcscf/administrative-state", `{"state": "UNLOCKED"}`, 400)
	send("PUT", v1+"roles/mgcf/administrative-state", `{"administrative_state": "UNLOCKED"}`, 404)

	// The history: a change of the node, then one of its role that
	// touches the same setting, so that rolling the first back is in
	// conflict.
	first := result(send("PATCH", v1+"nodes/pcscf-1/config", `{"settings": {"`+retryAfter+`": 5}, "operator": "test-operator"}`, 200))
	second := result(send("PATCH", v1+"roles/pcscf/config", `{"settings": {"`+retryAfter+`": 6}}`, 200))
	send("GET", v1+"nodes/pcscf-1/config/history?limit=10", "", 200)
	send("GET", v1+"nodes/pcscf-1/config/history?limit=0", "", 400)
	send("GET", v1+"config/history/"+first["id"].(string), "", 200)
	send("GET", v1+"config/history/no-such-change", "", 404)
	send("POST", v1+"config/history/"+first["id"].(string)+"/rollback", "", 409)
	send("POST", v1+"config/history/"+first["id"].(string)+"/rollback", `{"force": true, "operator": "test-operator"}`, 200)
	send("POST", v1+"config/history/"+first["id"].(string)+"/rollback", `{"forced": true}`, 400)
	send("POST", v1+"config/history/no-such-change/rollback", "", 404)
	send("POST", v1+"config/change-sets/"+second["change_set"].(string)+"/rollback", `{"force": true}`, 200)
	send("POST", v1+"config/change-sets/"+second["change_set"].(string)+"/rollback", `{"forced": true}`, 400)
	send("POST", v1+"config/change-sets/no-such-set/rollback", "", 404)

	alarms := decodeAs[Alarms](t, send("GET", v1+"alarms?min_severity=MAJOR&cleared=false", "", 200))
	send("GET", v1+"alarms?cleared=maybe", "", 400)
	send("PUT", v1+"nodes/pcscf-1/alarms/"+alarms.Alarms[0].AlarmID+"/ack-state", `{"ack_state": "ACKNOWLEDGED", "operator": "test-operator"}`, 200)
	send("PUT", v1+"nodes/pcscf-1/alarms/"+alarms.Alarms[0].AlarmID+"/ack-state", `{"state": "ACKNOWLEDGED"}`, 400)
	send("PUT", v1+"nodes/mgcf-1/alarms/"+alarms.Alarms[0].AlarmID+"/ack-state", `{"ack_state": "ACKNOWLEDGED"}`, 404)

	send("PUT", v1+"subscriptions/"+impi+"?dry_run=true", record, 200)
	send("PUT", v1+"subscriptions/"+impi, record, 201)
	send("PUT", v1+"subscriptions/"+impi, record, 200)
	send("PUT", v1+"subscriptions/"+impi, `{"private_identity": "`+impi+`", "pin": "1234"}`, 400)
	send("PUT", v1+"subscriptions/"+impiOf(2), `{"private_identity": "`+impiOf(2)+`", "public_identities": ["sip:`+impi+`"], "password": "test-password"}`, 409)
	send("GET", v1+"subscriptions/"+impi, "", 200)
	send("GET", v1+"subscriptions/"+impiOf(9), "", 404)
	send("PUT", v1+"subscriptions/"+impi+"/customized-alerting-tone", `{"tone": "cat1", "active": true, "operator": "test-operator"}`, 200)
	send("PUT", v1+"subscriptions/"+impi+"/customized-alerting-tone", `{"tone": "cat-1"}`, 400)
	send("PUT", v1+"subscriptions/"+impiOf(9)+"/customized-alerting-tone", `{"tone": "cat1"}`, 404)
	send("PUT", v1+"subscriptions/"+impi+"/test-user", `{"test_user": true, "operator": "test-operator"}`, 200)
	send("PUT", v1+"subscriptions/"+impi+"/test-user", `{}`, 400)
	send("PUT", v1+"subscriptions/"+impiOf(9)+"/test-user", `{"test_user": true}`, 404)
	send("GET", v1+"subscriptions/"+impi+"/features/sms", "", 200)
	send("GET", v1+"subscriptions/"+impi+"/features/video", "", 404)
	send("GET", v1+"test-users", "", 200)
	send("PUT", v1+"test-users/service-profile", `{"service_profile": "trial", "operator": "test-operator"}`, 200)
	send("PUT", v1+"test-users/service-profile", `{"service_profile": "gold"}`, 400)
	send("PUT", v1+"test-users/admission", `{"admission": "all", "comment": "test"}`, 200)
	send("PUT", v1+"test-users/admission", `{"admission": "some"}`, 400)
	send("POST", v1+"provisioning?dry_run=true", record+"\n", 200)
	send("POST", v1+"provisioning?dry_run=maybe", record+"\n", 400)

	var manifest Manifest
	sc := bufio.NewScanner(bytes.NewReader(send("POST", v1+"backups", "", 200)))
	for sc.Scan() {
		var l StreamLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatal(err)
		}
		if l.Manifest != nil {
			manifest = *l.Manifest
		}
	}
	send("POST", v1+"backups", `{"label": "night"}`, 400)
	send("GET", v1+"backups", "", 200)
	send("GET", v1+"backups/"+manifest.ID, "", 200)
	send("GET", v1+"backups/no-such-backup", "", 404)
	send("POST", v1+"backups/"+manifest.ID+"/restore?dry_run=true", `{"operator": "test-operator"}`, 200)
	send("POST", v1+"backups/"+manifest.ID+"/restore?dry_run=maybe", "", 400)
	send("POST", v1+"backups/no-such-backup/restore", "", 404)

	send("DELETE", v1+"subscriptions/"+impi+"?dry_run=true", "", 200)
	send("DELETE", v1+"subscriptions/"+impi, "", 200)
	send("DELETE", v1+"subscriptions/"+impi, "", 404)
	send("DELETE", v1+"subscriptions/"+impi+"?dry_run=maybe", "", 400)
	send("GET", v1+"counters", "", http.StatusNotFound)
	send("DELETE", v1+"inventory", "", http.StatusMethodNotAllowed)

	ts.api.CheckRoutes(t, ts.oam.Handler())
	ts.api.CheckCovered(t)
}
