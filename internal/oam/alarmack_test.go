package oam

import (
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// ackPatch is a JSON Merge Patch of the alarm list with the records,
// each written as the JSON object of its attributes.
func ackPatch(records map[string]string) string {
	var parts []string
	for _, key := range slices.Sorted(maps.Keys(records)) {
		parts = append(parts, `"`+key+`": `+records[key])
	}
	return `{"id": "1", "attributes": {"alarmRecords": {` + strings.Join(parts, ", ") + `}}}`
}

const acknowledge = `{"ackState": "ACKNOWLEDGED", "ackUserId": "noc-operator", "ackSystemId": "oss-1"}`

const mergePatchJSON = "application/merge-patch+json"

func TestPatchAcknowledgesAlarmRecords(t *testing.T) {
	ts, _, keys := queryNodes(t)
	a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{
		keys["scscf"]:       acknowledge,
		keys["scscf minor"]: `{"ackState": "ACKNOWLEDGED", "ackUserId": "noc-operator"}`,
	}))
	if a.status != http.StatusNoContent || len(a.body) != 0 {
		t.Fatalf("PATCH: %d %s", a.status, a.body)
	}
	got := ts.alarmList(t, "").Attributes.AlarmRecords
	r := got[keys["scscf"]]
	if r.AckState != mns.Acknowledged || r.AckUserId != "noc-operator" || r.AckSystemId != "oss-1" || r.AckTime == nil {
		t.Errorf("the acknowledged record: %+v", r)
	}
	if r := got[keys["scscf minor"]]; r.AckState != mns.Acknowledged || r.AckSystemId != "" {
		t.Errorf("the record without ackSystemId: %+v", r)
	}
	if r := got[keys["icscf"]]; r.AckState != mns.Unacknowledged {
		t.Errorf("a record of another node: %+v", r)
	}

	// Taking the acknowledgement back, with a parameter on the media
	// type.
	a = ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON+"; charset=utf-8", ackPatch(map[string]string{
		keys["scscf"]: `{"ackState": "UNACKNOWLEDGED", "ackUserId": "noc-operator"}`,
	}))
	if a.status != http.StatusNoContent {
		t.Fatalf("PATCH back: %d %s", a.status, a.body)
	}
	if r := ts.alarmList(t, "").Attributes.AlarmRecords[keys["scscf"]]; r.AckState != mns.Unacknowledged {
		t.Errorf("after taking it back: %+v", r)
	}
	// The identifiers may be written with the values they have, and a
	// patch that names no record changes nothing.
	for _, body := range []string{
		`{"id": "1", "objectClass": "AlarmList", "objectInstance": "SubNetwork=ims,AlarmList=1"}`,
		`{"id": "1", "attributes": {"alarmRecords": {}}}`,
	} {
		if a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, body); a.status != http.StatusNoContent {
			t.Errorf("PATCH %s: %d %s", body, a.status, a.body)
		}
	}
}

func TestPatchRefusesWhatItCannotApply(t *testing.T) {
	ts, asked, keys := queryNodes(t)
	records := `#/attributes/alarmRecords/`
	scscf := keys["scscf"]
	for _, tt := range []struct {
		name   string
		body   string
		status int
		want   mns.ErrorResponseDefault
	}{
		{"no id", `{"attributes": {"alarmRecords": {"` + scscf + `": ` + acknowledge + `}}}`, 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, BadAttributes: []string{"#/id"}}},
		{"another id", `{"id": "2", "attributes": {}}`, 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeInvariant, BadAttributes: []string{"#/id"}}},
		{"an id that is a number", `{"id": 1}`, 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeInvariant, BadAttributes: []string{"#/id"}}},
		{"another objectInstance", `{"id": "1", "objectInstance": "SubNetwork=x,AlarmList=1"}`, 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeInvariant, BadAttributes: []string{"#/objectInstance"}}},
		{"no JSON object", `[{"op": "replace"}]`, 400, mns.ErrorResponseDefault{Type: mns.ValidationError}},
		{"two JSON values", `{"id": "1"} {}`, 400, mns.ErrorResponseDefault{Type: mns.ValidationError}},
		{"another attribute of the list", `{"id": "1", "attributes": {"operationalState": "DISABLED"}}`, 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{"#/attributes/operationalState"}}},
		{"every record removed", `{"id": "1", "attributes": {"alarmRecords": null}}`, 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{"#/attributes/alarmRecords"}}},
		{"a record removed", ackPatch(map[string]string{scscf: `null`}), 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{records + scscf}}},
		{"the severity", ackPatch(map[string]string{scscf: `{"ackState": "ACKNOWLEDGED", "ackUserId": "noc-operator", "perceivedSeverity": "CLEARED"}`}), 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{records + scscf + "/perceivedSeverity"}}},
		{"ackUserId removed", ackPatch(map[string]string{scscf: `{"ackState": "ACKNOWLEDGED", "ackUserId": null}`}), 403,
			mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{records + scscf + "/ackUserId"}}},
		{"no ackUserId", ackPatch(map[string]string{scscf: `{"ackState": "ACKNOWLEDGED"}`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf + "/ackUserId"}}},
		{"no ackState", ackPatch(map[string]string{scscf: `{"ackUserId": "noc-operator"}`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf + "/ackState"}}},
		{"an unknown ackState", ackPatch(map[string]string{scscf: `{"ackState": "SEEN", "ackUserId": "noc-operator"}`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf + "/ackState"}}},
		{"an ackUserId that is no string", ackPatch(map[string]string{scscf: `{"ackState": "ACKNOWLEDGED", "ackUserId": 7}`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf + "/ackUserId"}}},
		{"a record that is no object", ackPatch(map[string]string{scscf: `"ACKNOWLEDGED"`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf}}},
		{"a node that is not in the inventory", ackPatch(map[string]string{"mgcf.0123456789abcdef": acknowledge}), 400,
			mns.ErrorResponseDefault{Type: mns.IENotFound, Reason: mns.AttributeNotFound, BadAttributes: []string{records + "mgcf.0123456789abcdef"}}},
		{"a key without a node", ackPatch(map[string]string{"0123456789abcdef": acknowledge}), 400,
			mns.ErrorResponseDefault{Type: mns.IENotFound, Reason: mns.AttributeNotFound, BadAttributes: []string{records + "0123456789abcdef"}}},
		{"a key with a slash", ackPatch(map[string]string{"scscf/x": acknowledge}), 400,
			mns.ErrorResponseDefault{Type: mns.IENotFound, Reason: mns.AttributeNotFound, BadAttributes: []string{records + "scscf~1x"}}},
		// Every record is checked before the patch is found to span two
		// nodes, so a bad record is what is answered.
		{"two nodes with a bad record", ackPatch(map[string]string{keys["icscf"]: acknowledge, scscf: `{"ackState": "SEEN", "ackUserId": "x"}`}), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{records + scscf + "/ackState"}}},
		{"two nodes", ackPatch(map[string]string{keys["icscf"]: acknowledge, scscf: acknowledge}), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := asked.Load()
			a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, tt.body)
			if a.status != tt.status {
				t.Fatalf("%d %s, want %d", a.status, a.body, tt.status)
			}
			got := decodeAs[mns.ErrorResponseDefault](t, a.body)
			if got.Title == "" {
				t.Errorf("no title in %s", a.body)
			}
			got.Title = ""
			if got.Type != tt.want.Type || got.Reason != tt.want.Reason || !slices.Equal(got.BadAttributes, tt.want.BadAttributes) {
				t.Errorf("%+v, want %+v", got, tt.want)
			}
			if asked.Load() != before {
				t.Errorf("a refused patch reached the nodes")
			}
		})
	}
	for key, r := range ts.alarmList(t, "").Attributes.AlarmRecords {
		if r.AckState != mns.Unacknowledged {
			t.Errorf("a refused patch changed %s: %+v", key, r)
		}
	}
}

// TestPatchWithANameThatTheNodeRefuses: the node bounds the names of an
// acknowledgement, and the OAM, which leaves the bound to the node,
// answers its refusal as an invalid value, with no list changed.
func TestPatchWithANameThatTheNodeRefuses(t *testing.T) {
	ts, _, keys := queryNodes(t)
	long := strings.Repeat("x", 1000)
	for _, record := range []string{
		`{"ackState": "ACKNOWLEDGED", "ackUserId": "` + long + `"}`,
		`{"ackState": "ACKNOWLEDGED", "ackUserId": "noc-operator", "ackSystemId": "` + long + `"}`,
	} {
		a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{keys["scscf"]: record}))
		got := decodeAs[mns.ErrorResponseDefault](t, a.body)
		if a.status != http.StatusBadRequest || got.Type != mns.ValidationError || got.Reason != mns.NewAttributeValueInvalid {
			t.Errorf("a name of 1000 characters: %d %s", a.status, a.body)
		}
	}
	if r := ts.alarmList(t, "").Attributes.AlarmRecords[keys["scscf"]]; r.AckState != mns.Unacknowledged {
		t.Errorf("a refused patch changed %+v", r)
	}
}

func TestPatchOfARecordThatIsGone(t *testing.T) {
	ts, _, keys := queryNodes(t)
	gone := "scscf.0123456789abcdef"
	a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{
		keys["scscf"]: acknowledge,
		gone:          acknowledge,
	}))
	if a.status != http.StatusBadRequest {
		t.Fatalf("%d %s", a.status, a.body)
	}
	got := decodeAs[mns.ErrorResponseDefault](t, a.body)
	if got.Type != mns.IENotFound || got.Reason != mns.AttributeNotFound || !slices.Equal(got.BadAttributes, []string{"#/attributes/alarmRecords/" + gone}) {
		t.Errorf("%+v", got)
	}
	// The node applies the patch whole or not at all.
	if r := ts.alarmList(t, "").Attributes.AlarmRecords[keys["scscf"]]; r.AckState != mns.Unacknowledged {
		t.Errorf("the record that is there was acknowledged: %+v", r)
	}
}

func TestPatchThatTheNodeDoesNotTake(t *testing.T) {
	for _, tt := range []struct {
		name   string
		answer func(w http.ResponseWriter)
		status int
		want   mns.ErrorResponseDefault
	}{
		{"the node refuses a value", func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"error": "invalid request: the operator is too long"}`)
		}, 400, mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid}},
		{"the node fails", func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusInternalServerError)
		}, 502, mns.ErrorResponseDefault{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n := newFakeNode(t, "scscf", []string{"scscf"})
			raise(t, n, "scscf", hssPeer, maintenance.Major)
			key, _ := recordOf(t, "scscf", n, "scscf", hssPeer)
			n.wrap = func(h http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodPatch {
						tt.answer(w)
						return
					}
					h.ServeHTTP(w, r)
				})
			}
			ts := newTestServer(t, mnsConfig(), n)
			a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{key: acknowledge}))
			if a.status != tt.status {
				t.Fatalf("%d %s, want %d", a.status, a.body, tt.status)
			}
			if tt.want.Type == "" {
				if len(a.body) != 0 {
					t.Errorf("a body: %s", a.body)
				}
				return
			}
			got := decodeAs[mns.ErrorResponseDefault](t, a.body)
			if got.Type != tt.want.Type || got.Reason != tt.want.Reason {
				t.Errorf("%+v, want %+v", got, tt.want)
			}
		})
	}
	n := newFakeNode(t, "scscf", []string{"scscf"})
	n.stop()
	ts := newTestServer(t, mnsConfig(), n)
	if a := ts.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{"scscf.0123456789abcdef": acknowledge})); a.status != http.StatusBadGateway || len(a.body) != 0 {
		t.Errorf("a node that does not answer: %d %s", a.status, a.body)
	}
}

func TestPatchTakesOnlyTheMergePatch(t *testing.T) {
	ts, asked, keys := queryNodes(t)
	body := ackPatch(map[string]string{keys["scscf"]: acknowledge})
	for _, contentType := range []string{"", "application/json", "application/json-patch+json", "application/3gpp-merge-patch+json", "application/vnd.3gpp.merge-patch+json", "application/3gpp-json-patch+json"} {
		a := ts.mnsCall(t, "PATCH", alarmListPath, contentType, body)
		if a.status != http.StatusUnsupportedMediaType || a.header.Get("Accept-Patch") != mergePatchJSON || len(a.body) != 0 {
			t.Errorf("Content-Type %q: %d %v %s", contentType, a.status, a.header, a.body)
		}
	}
	// The target of a patch has no query (TS 32.158 6.3.2).
	if a := ts.mnsCall(t, "PATCH", alarmListPath+"?fields=x", mergePatchJSON, body); a.status != http.StatusBadRequest ||
		decodeAs[mns.ErrorResponseDefault](t, a.body).Type != mns.ValidationError {
		t.Errorf("a patch with a query: %d %s", a.status, a.body)
	}
	if asked.Load() != 0 {
		t.Errorf("a refused patch reached the nodes")
	}
}
