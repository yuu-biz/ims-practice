package oam

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The paths and the names of the query parameters are written out here
// as TS28532_ProvMnS.yaml 19.2.0 and TS28111_FaultNrm.yaml 19.3.0 have
// them, rather than built with package mns, so that a change of either
// shows here: {MnSRoot}/ProvMnS/{MnSVersion}/{className}={id}, with
// v19 for the version 19.2.0 (TS 32.158 4.6.2) and the alarm list 1.
const (
	testMnSRoot   = "http://oam:8080/3GPPManagement"
	alarmListPath = "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1"
)

// mnsConfig is a Config with the 3GPP management services of the
// SubNetwork ims.
func mnsConfig() Config { return Config{SubNetwork: "ims", MnSRoot: testMnSRoot} }

// mnsAnswer is an answer of the 3GPP management services.
type mnsAnswer struct {
	status int
	header http.Header
	body   []byte
}

// mnsCall sends a request to the 3GPP management services of ts. A
// body is sent with contentType.
func (ts *testServer) mnsCall(t *testing.T, method, path, contentType, body string) mnsAnswer {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.server.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := ts.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) > 0 && resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("%s %s: a body with Content-Type %q", method, path, resp.Header.Get("Content-Type"))
	}
	return mnsAnswer{status: resp.StatusCode, header: resp.Header, body: b}
}

func (ts *testServer) alarmList(t *testing.T, query string) mns.AlarmListSingle {
	t.Helper()
	path := alarmListPath
	if query != "" {
		path += "?" + query
	}
	a := ts.mnsCall(t, "GET", path, "", "")
	if a.status != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, a.status, a.body)
	}
	return decodeAs[mns.AlarmListSingle](t, a.body)
}

// recordOf is the key of the record of the alarm of role and peer in
// the list of n, as the node's maintenance API reads it.
func recordOf(t *testing.T, inventoryName string, n *fakeNode, role string, peer maintenance.Peer) (string, maintenance.AlarmRecord) {
	t.Helper()
	for _, r := range n.node.Alarms(maintenance.AlarmFilter{}).AlarmRecords {
		if r.Role == role && r.Peer == peer {
			return inventoryName + "." + r.AlarmID, r
		}
	}
	t.Fatalf("no alarm of %s %s on %s", role, peer, n.name)
	return "", maintenance.AlarmRecord{}
}

const hssPeer = "hss.ims.mnc001.mcc001.3gppnetwork.org"

func TestAlarmListMergesTheListsOfEveryNode(t *testing.T) {
	// Two S-CSCF instances share one configuration file, so both call
	// themselves scscf; the inventory tells them apart.
	a := newFakeNode(t, "scscf", []string{"scscf"})
	b := newFakeNode(t, "scscf", []string{"scscf"})
	down := newFakeNode(t, "pcscf", []string{"pcscf"})
	raise(t, a, "scscf", hssPeer, maintenance.Critical)
	raise(t, b, "scscf", hssPeer, maintenance.Major)
	raise(t, b, "scscf", "hss-2", maintenance.Minor)
	down.server.Close()
	c := mnsConfig()
	c.Nodes = []Endpoint{
		{Name: "scscf-1", Roles: []string{"scscf"}, URL: a.server.URL},
		{Name: "scscf-2", Roles: []string{"scscf"}, URL: b.server.URL},
		{Name: "pcscf-1", Roles: []string{"pcscf"}, URL: down.server.URL},
	}
	ts := newTestServer(t, c)

	got := ts.alarmList(t, "")
	if got.Id != "1" || got.ObjectClass != "AlarmList" || got.ObjectInstance != "SubNetwork=ims,AlarmList=1" {
		t.Errorf("the identifiers: %+v", got.Top)
	}
	attrs := got.Attributes
	if attrs.OperationalState != mns.Enabled || attrs.NumOfAlarmRecords != 3 || len(attrs.AlarmRecords) != 3 {
		t.Fatalf("attributes: %+v", attrs)
	}
	// The node that did not answer is the unreliable part, and none of
	// its records is made up.
	if attrs.UnreliableAlarmScope != "SubNetwork=ims,ManagedElement=pcscf-1" {
		t.Errorf("unreliableAlarmScope = %q", attrs.UnreliableAlarmScope)
	}
	key, own := recordOf(t, "scscf-2", b, "scscf", hssPeer)
	r, ok := attrs.AlarmRecords[key]
	if !ok {
		t.Fatalf("no record %s in %v", key, slices.Sorted(maps.Keys(attrs.AlarmRecords)))
	}
	want := mns.AlarmRecord{
		ObjectInstance:    "SubNetwork=ims,ManagedElement=scscf-2,ManagedFunction=scscf,Peer=" + hssPeer,
		NotificationId:    own.NotificationID,
		AlarmRaisedTime:   own.AlarmRaisedTime,
		AlarmType:         mns.CommunicationsAlarm,
		ProbableCause:     "COMMUNICATIONS_SUBSYSTEM_FAILURE",
		SpecificProblem:   "Diameter peer down",
		PerceivedSeverity: mns.Major,
		AdditionalText:    "no answer to 3 DWR",
		AckState:          mns.Unacknowledged,
	}
	if !r.AlarmRaisedTime.Equal(want.AlarmRaisedTime) {
		t.Errorf("alarmRaisedTime %v, want %v", r.AlarmRaisedTime, want.AlarmRaisedTime)
	}
	r.AlarmRaisedTime = want.AlarmRaisedTime
	if r != want {
		t.Errorf("the record:\n got %+v\nwant %+v", r, want)
	}
	if k, _ := recordOf(t, "scscf-1", a, "scscf", hssPeer); attrs.AlarmRecords[k].ObjectInstance != "SubNetwork=ims,ManagedElement=scscf-1,ManagedFunction=scscf,Peer="+hssPeer {
		t.Errorf("the record of scscf-1: %+v", attrs.AlarmRecords[k])
	}
	newest := a.node.Alarms(maintenance.AlarmFilter{}).LastModification
	if other := b.node.Alarms(maintenance.AlarmFilter{}).LastModification; other.After(newest) {
		newest = other
	}
	if !attrs.LastModification.Equal(newest) {
		t.Errorf("lastModification %v, want the newest of the nodes, %v", attrs.LastModification, newest)
	}

	// The body has the names of the schema AlarmList-Single.
	raw := ts.mnsCall(t, "GET", alarmListPath, "", "")
	for _, name := range []string{`"id":"1"`, `"objectClass":"AlarmList"`, `"operationalState":"ENABLED"`, `"numOfAlarmRecords":3`, `"alarmRecords":{"scscf-1.`, `"unreliableAlarmScope"`} {
		if !strings.Contains(string(raw.body), name) {
			t.Errorf("no %s in %s", name, raw.body)
		}
	}
	if head := ts.mnsCall(t, "HEAD", alarmListPath, "", ""); head.status != http.StatusOK {
		t.Errorf("HEAD: %d", head.status)
	}
}

func TestUnreliableAlarmScope(t *testing.T) {
	for _, tt := range []struct {
		name string
		down []string
		want string
	}{
		{"every node answers", nil, ""},
		{"one does not", []string{"icscf"}, "SubNetwork=ims,ManagedElement=icscf"},
		{"two do not", []string{"icscf", "hss"}, "SubNetwork=ims"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var nodes []*fakeNode
			for _, name := range []string{"hss", "icscf", "scscf"} {
				n := newFakeNode(t, name, []string{name})
				if slices.Contains(tt.down, name) {
					n.server.Close()
				}
				nodes = append(nodes, n)
			}
			ts := newTestServer(t, mnsConfig(), nodes...)
			raw := ts.mnsCall(t, "GET", alarmListPath, "", "")
			got := decodeAs[mns.AlarmListSingle](t, raw.body)
			if got.Attributes.UnreliableAlarmScope != tt.want {
				t.Errorf("unreliableAlarmScope = %q, want %q", got.Attributes.UnreliableAlarmScope, tt.want)
			}
			if tt.want == "" && strings.Contains(string(raw.body), "unreliableAlarmScope") {
				t.Errorf("a reliable list writes the attribute: %s", raw.body)
			}
		})
	}
}

func TestAlarmListWithoutAnyNode(t *testing.T) {
	n := newFakeNode(t, "hss", []string{"hss"})
	n.server.Close()
	ts := newTestServer(t, mnsConfig(), n)
	got := ts.alarmList(t, "")
	if got.Attributes.UnreliableAlarmScope != "SubNetwork=ims,ManagedElement=hss" || got.Attributes.NumOfAlarmRecords != 0 ||
		!got.Attributes.LastModification.Equal(testTime) {
		t.Errorf("attributes: %+v", got.Attributes)
	}
}

// queryNodes is three nodes with four alarms, and the OAM, which counts
// the requests that reach the nodes.
func queryNodes(t *testing.T) (*testServer, *atomic.Int64, map[string]string) {
	t.Helper()
	var asked atomic.Int64
	count := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			asked.Add(1)
			h.ServeHTTP(w, r)
		})
	}
	icscf := newFakeNode(t, "icscf", []string{"icscf"})
	scscf := newFakeNode(t, "scscf", []string{"scscf"})
	pcscf := newFakeNode(t, "pcscf", []string{"pcscf"})
	raise(t, icscf, "icscf", hssPeer, maintenance.Major)
	raise(t, scscf, "scscf", hssPeer, maintenance.Critical)
	raise(t, scscf, "scscf", "hss-2", maintenance.Minor)
	raise(t, pcscf, "pcscf", "pcrf", maintenance.Warning)
	keys := map[string]string{}
	keys["icscf"], _ = recordOf(t, "icscf", icscf, "icscf", hssPeer)
	keys["scscf"], _ = recordOf(t, "scscf", scscf, "scscf", hssPeer)
	keys["scscf minor"], _ = recordOf(t, "scscf", scscf, "scscf", "hss-2")
	keys["pcscf"], _ = recordOf(t, "pcscf", pcscf, "pcscf", "pcrf")
	for _, n := range []*fakeNode{icscf, scscf, pcscf} {
		n.wrap = count
	}
	return newTestServer(t, mnsConfig(), icscf, scscf, pcscf), &asked, keys
}

func TestAlarmListQueries(t *testing.T) {
	ts, _, keys := queryNodes(t)
	records := `/AlarmList[id="1"]/attributes/alarmRecords/*`
	for _, tt := range []struct {
		name  string
		query url.Values
		want  []string
	}{
		{"no query", nil, []string{"icscf", "scscf", "scscf minor", "pcscf"}},
		{"filter of A.1.4", url.Values{"filter": {records + `[perceivedSeverity="MAJOR" or perceivedSeverity="CRITICAL"]`}}, []string{"icscf", "scscf"}},
		{"filter by the ManagedElement", url.Values{"filter": {records + `[contains(objectInstance,"ManagedElement=scscf,")]`}}, []string{"scscf", "scscf minor"}},
		{"dataNodeSelector", url.Values{"dataNodeSelector": {records + `[perceivedSeverity="WARNING"]`}}, []string{"pcscf"}},
		{"both select", url.Values{
			"filter":           {records + `[contains(objectInstance,"ManagedElement=scscf,")]`},
			"dataNodeSelector": {records + `[perceivedSeverity="MINOR"]`},
		}, []string{"scscf minor"}},
		{"another id of the list picks nothing", url.Values{"filter": {`/AlarmList[id="2"]/attributes/alarmRecords/*`}}, nil},
		{"scope BASE_ONLY", url.Values{"scope": {`{"scopeType":"BASE_ONLY"}`}}, []string{"icscf", "scscf", "scscf minor", "pcscf"}},
		{"scope with no type", url.Values{"scope": {`{}`}}, []string{"icscf", "scscf", "scscf minor", "pcscf"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := ts.alarmList(t, tt.query.Encode())
			var want []string
			for _, k := range tt.want {
				want = append(want, keys[k])
			}
			slices.Sort(want)
			if have := slices.Sorted(maps.Keys(got.Attributes.AlarmRecords)); !slices.Equal(have, want) {
				t.Errorf("records %v, want %v", have, want)
			}
			// The selection keeps the other attributes, and counts the
			// records before it.
			if got.Attributes.NumOfAlarmRecords != 4 || got.Attributes.OperationalState != mns.Enabled {
				t.Errorf("attributes: %+v", got.Attributes)
			}
		})
	}
}

func TestAlarmListFields(t *testing.T) {
	ts, _, keys := queryNodes(t)
	for _, tt := range []struct {
		name   string
		query  url.Values
		want   []string
		absent []string
	}{
		{"one record", url.Values{"fields": {"/attributes/alarmRecords/" + keys["icscf"]}}, []string{"icscf"}, nil},
		{"a union of A.1.4", url.Values{"fields": {"/attributes/alarmRecords/(" + keys["icscf"] + " | " + keys["pcscf"] + ")"}}, []string{"icscf", "pcscf"}, nil},
		{"two pointers", url.Values{"fields": {"/attributes/alarmRecords/" + keys["scscf"] + ",/attributes/alarmRecords/" + keys["pcscf"]}}, []string{"scscf", "pcscf"}, nil},
		{"with a filter", url.Values{
			"fields": {"/attributes/alarmRecords/(" + keys["icscf"] + " | " + keys["pcscf"] + ")"},
			"filter": {`/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR"]`},
		}, []string{"icscf"}, nil},
		{"a record that is not there", url.Values{"fields": {"/attributes/alarmRecords/icscf.0000000000000000"}}, nil, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := ts.mnsCall(t, "GET", alarmListPath+"?"+tt.query.Encode(), "", "")
			if a.status != http.StatusOK {
				t.Fatalf("%d %s", a.status, a.body)
			}
			var got struct {
				mns.Top
				Attributes map[string]json.RawMessage `json:"attributes"`
			}
			if err := json.Unmarshal(a.body, &got); err != nil {
				t.Fatal(err)
			}
			if got.Id != "1" || got.ObjectInstance != "SubNetwork=ims,AlarmList=1" {
				t.Errorf("the identifiers: %s", a.body)
			}
			// fields drops every attribute that it does not name (TS
			// 32.158 6.2.3).
			for name := range got.Attributes {
				if name != "alarmRecords" {
					t.Errorf("attribute %s in %s", name, a.body)
				}
			}
			var records map[string]mns.AlarmRecord
			if raw, ok := got.Attributes["alarmRecords"]; ok {
				if err := json.Unmarshal(raw, &records); err != nil {
					t.Fatal(err)
				}
			}
			var want []string
			for _, k := range tt.want {
				want = append(want, keys[k])
			}
			slices.Sort(want)
			if have := slices.Sorted(maps.Keys(records)); !slices.Equal(have, want) {
				t.Errorf("records %v, want %v", have, want)
			}
		})
	}
}

func TestAlarmListRefusesQueries(t *testing.T) {
	ts, asked, _ := queryNodes(t)
	records := `/AlarmList[id="1"]/attributes/alarmRecords/*`
	for _, tt := range []struct {
		name   string
		query  string
		status int
		want   mns.ErrorResponseGet
	}{
		{"unknown names", url.Values{"scopeType": {"BASE_ONLY"}, "scopeLevel": {"1"}, "filter": {records}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamNamesInvalid, BadQueryParams: []string{"scopeLevel", "scopeType"}}},
		{"a filter that breaks the grammar", url.Values{"filter": {records + `[perceivedSeverity=]`}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"filter"}}},
		{"a dataNodeSelector that breaks the grammar", url.Values{"dataNodeSelector": {"/AlarmList["}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"dataNodeSelector"}}},
		{"fields that are no pointer", url.Values{"fields": {"alarmRecords"}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"fields"}}},
		{"a scope that is no JSON object", url.Values{"scope": {"BASE_ONLY"}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"scope"}}},
		{"a scope of no type", url.Values{"scope": {`{"scopeType":"BASE_EVERYTHING"}`}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"scope"}}},
		{"a filter twice", url.Values{"filter": {records, records}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"filter"}}},
		{"a malformed query", "filter=%zz", 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryMalformed}},
		{"a filter of another attribute", url.Values{"filter": {"/AlarmList/attributes/numOfAlarmRecords"}}.Encode(), 500,
			mns.ErrorResponseGet{Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"filter"}}},
		{"fields of another attribute", url.Values{"fields": {"/attributes/numOfAlarmRecords"}}.Encode(), 500,
			mns.ErrorResponseGet{Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"fields"}}},
		{"attributes", url.Values{"attributes": {"alarmRecords"}}.Encode(), 500,
			mns.ErrorResponseGet{Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"attributes"}}},
		{"scope BASE_ALL", url.Values{"scope": {`{"scopeType":"BASE_ALL"}`}}.Encode(), 500,
			mns.ErrorResponseGet{Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"scope"}}},
		{"scope BASE_SUBTREE", url.Values{"scope": {`{"scopeType":"BASE_SUBTREE","scopeLevel":2}`}}.Encode(), 500,
			mns.ErrorResponseGet{Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"scope"}}},
		// A value that the consumer can mend comes before one that the
		// producer cannot evaluate.
		{"400 before 500", url.Values{"attributes": {"alarmRecords"}, "fields": {"x"}}.Encode(), 400,
			mns.ErrorResponseGet{Type: mns.ValidationError, Reason: mns.QueryParamValuesInvalid, BadQueryParams: []string{"fields"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := asked.Load()
			a := ts.mnsCall(t, "GET", alarmListPath+"?"+tt.query, "", "")
			if a.status != tt.status {
				t.Fatalf("%d %s, want %d", a.status, a.body, tt.status)
			}
			got := decodeAs[mns.ErrorResponseGet](t, a.body)
			if got.Title == "" {
				t.Errorf("no title in %s", a.body)
			}
			got.Title = ""
			if got.Type != tt.want.Type || got.Reason != tt.want.Reason || !slices.Equal(got.BadQueryParams, tt.want.BadQueryParams) {
				t.Errorf("%+v, want %+v", got, tt.want)
			}
			// The query is checked before any node is asked.
			if asked.Load() != before {
				t.Errorf("a refused query reached the nodes")
			}
		})
	}
}

func TestAlarmListPathsAndMethods(t *testing.T) {
	n := newFakeNode(t, "hss", []string{"hss"})
	ts := newTestServer(t, mnsConfig(), n)
	for _, tt := range []struct {
		method, path string
		status       int
		want         mns.ErrorResponseDefault
	}{
		{"PUT", alarmListPath, 403, mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.AttributeNotWritable, BadAttributes: []string{"#/attributes"}}},
		{"DELETE", alarmListPath, 403, mns.ErrorResponseDefault{Type: mns.ModificationNotAllowed, Reason: mns.ObjectDeletionNotAllowed}},
		{"POST", alarmListPath, 405, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=ims", 404, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=2", 404, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=other/AlarmList=1", 404, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/ManagedElement=hss/AlarmList=1", 404, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1/x", 404, mns.ErrorResponseDefault{}},
		{"GET", "/3GPPManagement/ProvMnS/v19/", 404, mns.ErrorResponseDefault{}},
	} {
		a := ts.mnsCall(t, tt.method, tt.path, "", "")
		if a.status != tt.status {
			t.Errorf("%s %s: %d %s, want %d", tt.method, tt.path, a.status, a.body, tt.status)
			continue
		}
		if tt.want.Type == "" {
			if len(a.body) != 0 {
				t.Errorf("%s %s: a body %s", tt.method, tt.path, a.body)
			}
			continue
		}
		got := decodeAs[mns.ErrorResponseDefault](t, a.body)
		got.Title = ""
		if got.Type != tt.want.Type || got.Reason != tt.want.Reason || !slices.Equal(got.BadAttributes, tt.want.BadAttributes) {
			t.Errorf("%s %s: %+v, want %+v", tt.method, tt.path, got, tt.want)
		}
	}
	if a := ts.mnsCall(t, "POST", alarmListPath, "", ""); a.header.Get("Allow") != "DELETE, GET, HEAD, PUT" {
		t.Errorf("Allow: %q", a.header.Get("Allow"))
	}
	// The ids are percent-encoded as a URI-LDN may have them.
	if a := ts.mnsCall(t, "GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=%69ms/AlarmList=1", "", ""); a.status != http.StatusOK {
		t.Errorf("an encoded id: %d", a.status)
	}
}

func TestNewChecksTheNamesOfTheManagementServices(t *testing.T) {
	nodes := []Endpoint{{Name: "hss", Roles: []string{"hss"}, URL: "http://hss:8085"}}
	for _, c := range []Config{
		{SubNetwork: "ims"},
		{MnSRoot: testMnSRoot},
		{SubNetwork: "ims,DC=x", MnSRoot: testMnSRoot},
		{SubNetwork: "ims/1", MnSRoot: testMnSRoot},
		{SubNetwork: "ims", MnSRoot: "oam:8080/3GPPManagement"},
		{SubNetwork: "ims", MnSRoot: testMnSRoot + "?x=1"},
	} {
		c.Nodes = nodes
		if _, err := New(c); err == nil {
			t.Errorf("New(%q, %q) took them", c.SubNetwork, c.MnSRoot)
		}
	}
	s, err := New(Config{Nodes: nodes})
	if err != nil || s.MnSHandler() != nil || s.MnSPathPrefix() != "" {
		t.Errorf("an OAM without the services: %v", err)
	}
	s, err = New(Config{Nodes: nodes, SubNetwork: "ims", MnSRoot: "http://oam:8080/mns/"})
	if err != nil || s.MnSPathPrefix() != "/mns/ProvMnS/v19/" {
		t.Errorf("the prefix of another root: %q, %v", s.MnSPathPrefix(), err)
	}
}
