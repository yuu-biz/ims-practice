package oam

import (
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The path of a subscription as TS28532_ProvMnS.yaml writes it, with the
// class of TS28623_SubscriptionControlNrm.yaml.
const ntfSubscriptionPath = "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl="

// subscriptionServer is an OAM with the 3GPP management services and
// the file store of subscriptions, bound to max.
func subscriptionServer(t *testing.T, max int) *testServer {
	t.Helper()
	store, err := NewFileNtfSubscriptionStore(filepath.Join(t.TempDir(), "ntf-subscriptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := mnsConfig()
	c.NtfSubscriptions, c.MaxNtfSubscriptions = store, max
	return newTestServer(t, c, newFakeNode(t, "scscf", []string{"scscf"}))
}

const subscriptionBody = `{
  "id": "oss-1",
  "objectClass": "NtfSubscriptionControl",
  "attributes": {
    "notificationRecipientAddress": "http://oss:8080/notifications",
    "notificationTypes": ["notifyNewAlarm", "notifyClearedAlarm"],
    "scope": {"scopeType": "BASE_ALL"},
    "notificationFilter": "contains(href,\"ManagedElement=scscf\") and perceivedSeverity=\"CRITICAL\""
  }
}`

func TestNtfSubscriptionPutGetDelete(t *testing.T) {
	ts := subscriptionServer(t, 0)
	path := ntfSubscriptionPath + "oss-1"
	a := ts.mnsCall(t, "PUT", path, "application/json", subscriptionBody)
	if a.status != http.StatusCreated {
		t.Fatalf("PUT: %d %s", a.status, a.body)
	}
	if loc := a.header.Get("Location"); loc != testMnSRoot+"/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=oss-1" {
		t.Errorf("Location: %q", loc)
	}
	created := decodeAs[mns.NtfSubscriptionControlSingle](t, a.body)
	if created.Id != "oss-1" || created.ObjectClass != "NtfSubscriptionControl" || created.ObjectInstance != "SubNetwork=ims,NtfSubscriptionControl=oss-1" ||
		created.Attributes == nil || created.Attributes.NotificationRecipientAddress != "http://oss:8080/notifications" ||
		!slices.Equal(created.Attributes.NotificationTypes, []mns.NotificationType{mns.NotifyNewAlarmType, mns.NotifyClearedAlarmType}) ||
		created.Attributes.Scope == nil || created.Attributes.Scope.ScopeType != mns.BaseAll || !strings.Contains(created.Attributes.NotificationFilter, "CRITICAL") {
		t.Errorf("the created subscription: %s", a.body)
	}
	got := ts.mnsCall(t, "GET", path, "", "")
	if got.status != http.StatusOK || string(got.body) != string(a.body) {
		t.Errorf("GET: %d %s, want the PUT's answer %s", got.status, got.body, a.body)
	}

	// The same id again replaces the subscription: 200, no Location.
	replace := `{"id": "oss-1", "objectClass": "NtfSubscriptionControl", "objectInstance": "SubNetwork=ims,NtfSubscriptionControl=oss-1",
	  "attributes": {"notificationRecipientAddress": "https://oss:8443/n"}}`
	a = ts.mnsCall(t, "PUT", path, "application/json", replace)
	if a.status != http.StatusOK || a.header.Get("Location") != "" {
		t.Fatalf("PUT again: %d %v %s", a.status, a.header, a.body)
	}
	if r := decodeAs[mns.NtfSubscriptionControlSingle](t, a.body); r.Attributes.NotificationTypes != nil || r.Attributes.Scope != nil ||
		r.Attributes.NotificationRecipientAddress != "https://oss:8443/n" {
		t.Errorf("a PUT replaces the whole subscription: %s", a.body)
	}

	if a := ts.mnsCall(t, "DELETE", path, "", ""); a.status != http.StatusNoContent || len(a.body) != 0 {
		t.Errorf("DELETE: %d %s", a.status, a.body)
	}
	for _, method := range []string{"GET", "DELETE"} {
		if a := ts.mnsCall(t, method, path, "", ""); a.status != http.StatusNotFound || len(a.body) != 0 {
			t.Errorf("%s after the DELETE: %d %s", method, a.status, a.body)
		}
	}
	if a := ts.mnsCall(t, "PATCH", path, "application/merge-patch+json", `{"id": "oss-1"}`); a.status != http.StatusMethodNotAllowed ||
		a.header.Get("Allow") != "DELETE, GET, HEAD, PUT" {
		t.Errorf("PATCH: %d %v", a.status, a.header)
	}
	// A subscription below another object is no object here.
	if a := ts.mnsCall(t, "GET", "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/ManagedElement=scscf/NtfSubscriptionControl=oss-1", "", ""); a.status != http.StatusNotFound {
		t.Errorf("a subscription below a ManagedElement: %d", a.status)
	}
}

func TestNtfSubscriptionRefusesWhatItCannotTake(t *testing.T) {
	ts := subscriptionServer(t, 0)
	body := func(old, new string) string {
		t.Helper()
		b := strings.Replace(subscriptionBody, old, new, 1)
		if b == subscriptionBody {
			t.Fatalf("%q is not in the body", old)
		}
		return b
	}
	for _, tt := range []struct {
		name   string
		id     string
		body   string
		status int
		want   mns.ErrorResponseDefault
	}{
		{"an unknown attribute", "oss-1", body(`"scope"`, `"lastSequenceNo": 3, "scope"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectRepresentationInvalid}},
		{"no JSON", "oss-1", "notificationRecipientAddress=http://oss/", 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectRepresentationInvalid}},
		{"another id", "oss-2", subscriptionBody, 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/id"}}},
		{"an id with a space", "oss%201", body(`"oss-1"`, `"oss 1"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/id"}}},
		{"no objectClass", "oss-1", body(`"objectClass": "NtfSubscriptionControl",`, ""), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectAttributeValueMissing, BadAttributes: []string{"#/objectClass"}}},
		{"another objectClass", "oss-1", body(`"NtfSubscriptionControl"`, `"HeartbeatControl"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectClassNameInvalid, BadAttributes: []string{"#/objectClass"}}},
		{"another objectInstance", "oss-1", body(`"objectClass"`, `"objectInstance": "SubNetwork=ims,NtfSubscriptionControl=oss-2", "objectClass"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/objectInstance"}}},
		{"no recipient", "oss-1", body(`"notificationRecipientAddress": "http://oss:8080/notifications",`, ""), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectAttributeValueMissing, BadAttributes: []string{"#/attributes/notificationRecipientAddress"}}},
		{"no attributes", "oss-1", `{"id": "oss-1", "objectClass": "NtfSubscriptionControl"}`, 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectAttributeValueMissing, BadAttributes: []string{"#/attributes/notificationRecipientAddress"}}},
		{"a recipient that is no http URL", "oss-1", body(`http://oss:8080/notifications`, `mailto:noc@oss`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationRecipientAddress"}}},
		{"a recipient with a fragment", "oss-1", body(`/notifications`, `/notifications#x`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationRecipientAddress"}}},
		{"a notification that the OAM does not send", "oss-1", body(`"notifyClearedAlarm"`, `"notifyMOICreation"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationTypes"}}},
		{"a notification twice", "oss-1", body(`"notifyClearedAlarm"`, `"notifyNewAlarm"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationTypes"}}},
		{"no notification", "oss-1", body(`["notifyNewAlarm", "notifyClearedAlarm"]`, `[]`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationTypes"}}},
		{"a scope of no type", "oss-1", body(`"BASE_ALL"`, `"BASE_SOME"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/scope"}}},
		{"a scope of the SubNetwork alone", "oss-1", body(`"BASE_ALL"`, `"BASE_ONLY"`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/attributes/scope"}}},
		{"a scope of some levels", "oss-1", body(`{"scopeType": "BASE_ALL"}`, `{"scopeType": "BASE_SUBTREE", "scopeLevel": 1}`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/attributes/scope"}}},
		{"a filter that breaks the grammar", "oss-1", body(`perceivedSeverity=\"CRITICAL\"`, `perceivedSeverity=`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationFilter"}}},
		{"a filter that the OAM does not evaluate", "oss-1", body(`perceivedSeverity=\"CRITICAL\"`, `alarmIds[0]=\"x\"`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/attributes/notificationFilter"}}},
		// The names of TS28623_SubscriptionControlNrm.yaml that the OAM
		// does not take are understood, and beyond what it does.
		{"a heartbeat", "oss-1", body(`"attributes"`, `"HeartbeatControl": {"id": "1", "attributes": {"heartbeatNtfPeriod": 60}}, "attributes"`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/HeartbeatControl"}}},
		{"vendor-specific data", "oss-1", body(`"attributes"`, `"VsDataContainer": [{"id": "1"}], "attributes"`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/VsDataContainer"}}},
		{"a scope that selects data nodes", "oss-1", body(`{"scopeType": "BASE_ALL"}`, `{"dataNodeSelector": "/SubNetwork/ManagedElement[id=\"scscf\"]"}`), 500,
			mns.ErrorResponseDefault{Type: mns.ServerLimitation, BadAttributes: []string{"#/attributes/scope/dataNodeSelector"}}},
		// A problem that the consumer can mend comes first (TS 32.158
		// 6.6.5.1).
		{"a heartbeat and an unknown attribute", "oss-1", body(`"attributes"`, `"HeartbeatControl": {"id": "1"}, "lastSequenceNo": 3, "attributes"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewObjectRepresentationInvalid}},
		{"a heartbeat and another id", "oss-2", body(`"attributes"`, `"HeartbeatControl": {"id": "1"}, "attributes"`), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/id"}}},
		{"a narrow scope and a filter that breaks the grammar", "oss-1",
			strings.Replace(body(`"BASE_ALL"`, `"BASE_ONLY"`), `perceivedSeverity=\"CRITICAL\"`, `perceivedSeverity=`, 1), 400,
			mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid, BadAttributes: []string{"#/attributes/notificationFilter"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := ts.mnsCall(t, "PUT", ntfSubscriptionPath+tt.id, "application/json", tt.body)
			if a.status != tt.status {
				t.Fatalf("%d %s, want %d", a.status, a.body, tt.status)
			}
			got := decodeAs[mns.ErrorResponseDefault](t, a.body)
			if got.Title == "" {
				t.Errorf("no title in %s", a.body)
			}
			if got.Type != tt.want.Type || got.Reason != tt.want.Reason || !slices.Equal(got.BadAttributes, tt.want.BadAttributes) {
				t.Errorf("%+v, want %+v", got, tt.want)
			}
		})
	}
	if subs, err := ts.oam.ntfSubscriptions.NtfSubscriptions(t.Context()); err != nil || len(subs) != 0 {
		t.Errorf("a refused PUT kept %+v, %v", subs, err)
	}
	for _, tt := range []struct{ contentType, query string }{{"application/xml", ""}, {"", ""}} {
		if a := ts.mnsCall(t, "PUT", ntfSubscriptionPath+"oss-1"+tt.query, tt.contentType, subscriptionBody); a.status != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: %d %s", tt.contentType, a.status, a.body)
		}
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if a := ts.mnsCall(t, method, ntfSubscriptionPath+"oss-1?x=1", "application/json", subscriptionBody); a.status != http.StatusBadRequest {
			t.Errorf("%s with a query: %d %s", method, a.status, a.body)
		}
	}
	for query, want := range map[string]mns.ErrorResponseGet{
		"fields=/attributes":   {Type: mns.ServerLimitation, Reason: mns.QueryParamsTooComplex, BadQueryParams: []string{"fields"}},
		"since=1&scope=%7B%7D": {Type: mns.ValidationError, Reason: mns.QueryParamNamesInvalid, BadQueryParams: []string{"since"}},
		"scope=%zz":            {Type: mns.ValidationError, Reason: mns.QueryMalformed},
	} {
		a := ts.mnsCall(t, "GET", ntfSubscriptionPath+"oss-1?"+query, "", "")
		got := decodeAs[mns.ErrorResponseGet](t, a.body)
		if a.status != statusOf[want.Type] || got.Type != want.Type || got.Reason != want.Reason || !slices.Equal(got.BadQueryParams, want.BadQueryParams) {
			t.Errorf("GET ?%s: %d %s, want %+v", query, a.status, a.body, want)
		}
	}
}

func TestNtfSubscriptionsAreBounded(t *testing.T) {
	ts := subscriptionServer(t, 2)
	put := func(id string) mnsAnswer {
		t.Helper()
		return ts.mnsCall(t, "PUT", ntfSubscriptionPath+id, "application/json", strings.Replace(subscriptionBody, `"oss-1"`, `"`+id+`"`, 1))
	}
	for _, id := range []string{"oss-1", "oss-2"} {
		if a := put(id); a.status != http.StatusCreated {
			t.Fatalf("PUT %s: %d %s", id, a.status, a.body)
		}
	}
	a := put("oss-3")
	if got := decodeAs[mns.ErrorResponseDefault](t, a.body); a.status != http.StatusInternalServerError || got.Type != mns.ServerLimitation {
		t.Errorf("a third subscription: %d %s", a.status, a.body)
	}
	// Replacing one is no new one.
	if a := put("oss-2"); a.status != http.StatusOK {
		t.Errorf("replacing at the bound: %d %s", a.status, a.body)
	}
}

func TestNtfSubscriptionWithoutAStore(t *testing.T) {
	ts := newTestServer(t, mnsConfig(), newFakeNode(t, "scscf", []string{"scscf"}))
	for _, method := range []string{"PUT", "GET", "DELETE"} {
		a := ts.mnsCall(t, method, ntfSubscriptionPath+"oss-1", "application/json", subscriptionBody)
		if got := decodeAs[mns.ErrorResponseDefault](t, a.body); a.status != http.StatusServiceUnavailable || got.Type != mns.ServiceDisabled {
			t.Errorf("%s: %d %s", method, a.status, a.body)
		}
	}
}
