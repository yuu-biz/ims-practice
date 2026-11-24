package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
)

// fakeOAM answers the requests of the OSS as the northbound of the OAM
// does: the AlarmList=1 and the NtfSubscriptionControl objects of the
// SubNetwork ims, under {MnSRoot}/ProvMnS/v19.
type fakeOAM struct {
	t      *testing.T
	root   mns.MnSRoot
	server *httptest.Server

	mu            sync.Mutex
	subscriptions map[string]mns.NtfSubscriptionControlSingle
	records       map[string]mns.AlarmRecord
	// rewrite, when set, changes the answer to a GET of the alarm list,
	// for a test of an answer the OSS must refuse.
	rewrite func([]byte) []byte
	// patchAnswersList makes a JSON Merge Patch answer 200 with the
	// AlarmList, not 204.
	patchAnswersList bool
	// The last request.
	method, path, query, contentType string
	body                             []byte
}

func newFakeOAM(t *testing.T) *fakeOAM {
	t.Helper()
	f := &fakeOAM{t: t, subscriptions: map[string]mns.NtfSubscriptionControlSingle{}, records: map[string]mns.AlarmRecord{
		"scscf.9f3c2a7b1d4e6058": {
			ObjectInstance:    "SubNetwork=ims,ManagedElement=scscf,ManagedFunction=scscf,Peer=hss.ims.mnc001.mcc001.3gppnetwork.org",
			NotificationId:    4711,
			AlarmRaisedTime:   time.Date(2026, 11, 24, 10, 15, 2, 0, time.UTC),
			AlarmType:         mns.CommunicationsAlarm,
			ProbableCause:     "COMMUNICATIONS_SUBSYSTEM_FAILURE",
			SpecificProblem:   "Diameter peer down",
			PerceivedSeverity: mns.Major,
			AckState:          mns.Unacknowledged,
		},
	}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	root, err := mns.ParseMnSRoot(f.server.URL + mns.DefaultRoot)
	if err != nil {
		t.Fatal(err)
	}
	f.root = root
	return f
}

func (f *fakeOAM) consumer() consumer {
	return consumer{root: f.root, subNetwork: mns.SubNetworkDN("ims"), client: f.server.Client()}
}

func (f *fakeOAM) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.t.Error(err)
	}
	f.method, f.path, f.query, f.contentType, f.body = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Content-Type"), body
	dn, err := f.root.ParseResourcePath(r.URL.EscapedPath())
	if err != nil || len(dn) != 2 || dn[0] != (mns.RDN{ClassName: mns.ClassSubNetwork, Id: "ims"}) {
		http.NotFound(w, r)
		return
	}
	switch object := dn[1]; {
	case object == mns.RDN{ClassName: mns.ClassAlarmList, Id: "1"} && r.Method == http.MethodGet:
		f.getAlarmList(w)
	case object == mns.RDN{ClassName: mns.ClassAlarmList, Id: "1"} && r.Method == http.MethodPatch:
		f.patchAlarmList(w, r, body)
	case object.ClassName == mns.ClassNtfSubscriptionControl && r.Method == http.MethodPut:
		f.putSubscription(w, dn, body)
	case object.ClassName == mns.ClassNtfSubscriptionControl && r.Method == http.MethodDelete:
		delete(f.subscriptions, object.Id)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeOAM) getAlarmList(w http.ResponseWriter) {
	list := mns.AlarmListSingle{
		Top: mns.Top{Id: "1", ObjectClass: mns.ClassAlarmList, ObjectInstance: "SubNetwork=ims,AlarmList=1"},
		Attributes: &mns.AlarmListAttributes{
			OperationalState:  mns.Enabled,
			NumOfAlarmRecords: len(f.records),
			LastModification:  time.Date(2026, 11, 24, 10, 15, 2, 0, time.UTC),
			AlarmRecords:      maps.Clone(f.records),
		},
	}
	b, err := json.Marshal(list)
	if err != nil {
		f.t.Fatal(err)
	}
	if f.rewrite != nil {
		b = f.rewrite(b)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// withAttribute is a rewrite that adds the attribute to the list.
func withAttribute(attribute string) func([]byte) []byte {
	return func(b []byte) []byte {
		return bytes.Replace(b, []byte(`{"id":"1"`), []byte(`{"id":"1",`+attribute), 1)
	}
}

func (f *fakeOAM) patchAlarmList(w http.ResponseWriter, r *http.Request, body []byte) {
	if r.Header.Get("Content-Type") != mergePatch {
		w.Header().Set("Accept-Patch", mergePatch)
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	var patch ackPatch
	if err := decodeStrictly(body, &patch); err != nil || patch.Id != "1" {
		writeJSON(w, http.StatusBadRequest, mns.ErrorResponseDefault{Type: mns.ValidationError})
		return
	}
	for id, ack := range patch.Attributes.AlarmRecords {
		if _, ok := f.records[id]; !ok {
			writeJSON(w, http.StatusBadRequest, mns.ErrorResponseDefault{
				Type: mns.IENotFound, Reason: mns.AttributeNotFound, BadAttributes: []string{"#/attributes/alarmRecords/" + id},
			})
			return
		}
		if ack.AckUserId == "" {
			writeJSON(w, http.StatusBadRequest, mns.ErrorResponseDefault{Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid})
			return
		}
	}
	for id, ack := range patch.Attributes.AlarmRecords {
		record := f.records[id]
		record.AckState, record.AckUserId, record.AckSystemId = ack.AckState, ack.AckUserId, ack.AckSystemId
		f.records[id] = record
	}
	if f.patchAnswersList {
		f.getAlarmList(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeOAM) putSubscription(w http.ResponseWriter, dn mns.DN, body []byte) {
	var s mns.NtfSubscriptionControlSingle
	if err := decodeStrictly(body, &s); err != nil || s.Id != dn[1].Id || s.ObjectClass != mns.ClassNtfSubscriptionControl || s.Attributes == nil {
		writeJSON(w, http.StatusBadRequest, mns.ErrorResponseDefault{Type: mns.ValidationError})
		return
	}
	if !strings.HasPrefix(s.Attributes.NotificationRecipientAddress, "http") {
		writeJSON(w, http.StatusBadRequest, mns.ErrorResponseDefault{
			Type: mns.ValidationError, Reason: mns.NewAttributeValueInvalid,
			BadAttributes: []string{"#/attributes/notificationRecipientAddress"},
		})
		return
	}
	s.ObjectInstance = dn.String()
	held, replaced := f.subscriptions[s.Id]
	f.subscriptions[s.Id] = s
	switch {
	case !replaced:
		writeJSON(w, http.StatusCreated, s)
	case reflect.DeepEqual(held, s):
		// TS 32.158 5.1.2: the representation is not modified.
		w.WriteHeader(http.StatusNoContent)
	default:
		writeJSON(w, http.StatusOK, s)
	}
}

// sameJSON reports whether two JSON texts say the same.
func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("%s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	ja, _ := json.Marshal(x)
	jb, _ := json.Marshal(y)
	return bytes.Equal(ja, jb)
}

func TestSubscribeCreatesThenReplaces(t *testing.T) {
	oam := newFakeOAM(t)
	c := oam.consumer()
	attributes := mns.NtfSubscriptionControlAttributes{
		NotificationRecipientAddress: "http://oss:8080/notifications",
		NotificationTypes:            []mns.NotificationType{mns.NotifyNewAlarmType, mns.NotifyClearedAlarmType},
		NotificationFilter:           `perceivedSeverity="CRITICAL"`,
	}
	s, created, err := c.subscribe(t.Context(), "1", attributes)
	if err != nil || !created {
		t.Fatalf("created %v, %v", created, err)
	}
	if s.ObjectInstance != "SubNetwork=ims,NtfSubscriptionControl=1" || s.Attributes.NotificationRecipientAddress != attributes.NotificationRecipientAddress {
		t.Errorf("subscription %+v", s)
	}
	if oam.method != http.MethodPut || oam.path != "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=1" || oam.contentType != "application/json" {
		t.Errorf("request %s %s, %s", oam.method, oam.path, oam.contentType)
	}
	want := `{"id": "1", "objectClass": "NtfSubscriptionControl", "attributes": {"notificationRecipientAddress": "http://oss:8080/notifications",
	  "notificationTypes": ["notifyNewAlarm", "notifyClearedAlarm"], "notificationFilter": "perceivedSeverity=\"CRITICAL\""}}`
	if !sameJSON(t, oam.body, []byte(want)) {
		t.Errorf("body %s, want %s", oam.body, want)
	}
	// The same subscription again is not modified: 204, and the
	// subscription is the one sent.
	again, created, err := c.subscribe(t.Context(), "1", attributes)
	if err != nil || created || again.Id != "1" || again.Attributes.NotificationFilter != attributes.NotificationFilter {
		t.Errorf("the same PUT again: %+v, created %v, %v", again, created, err)
	}
	attributes.NotificationTypes = nil
	replaced, created, err := c.subscribe(t.Context(), "1", attributes)
	if err != nil || created || replaced.ObjectInstance != "SubNetwork=ims,NtfSubscriptionControl=1" || replaced.Attributes.NotificationTypes != nil {
		t.Errorf("a changed PUT: %+v, created %v, %v", replaced, created, err)
	}
}

func TestSubscribeSaysWhatTheOAMRefused(t *testing.T) {
	oam := newFakeOAM(t)
	_, _, err := oam.consumer().subscribe(t.Context(), "1", mns.NtfSubscriptionControlAttributes{NotificationRecipientAddress: "oss:8080"})
	if err == nil || !strings.Contains(err.Error(), "400 Bad Request") || !strings.Contains(err.Error(), `"NEW_ATTRIBUTE_VALUE_INVALID"`) {
		t.Errorf("error %v", err)
	}
}

func TestUnsubscribeDeletesTheSubscription(t *testing.T) {
	oam := newFakeOAM(t)
	c := oam.consumer()
	if _, _, err := c.subscribe(t.Context(), "oss-a", mns.NtfSubscriptionControlAttributes{NotificationRecipientAddress: "http://oss:8080/notifications"}); err != nil {
		t.Fatal(err)
	}
	if err := c.unsubscribe(t.Context(), "oss-a"); err != nil {
		t.Fatal(err)
	}
	if oam.method != http.MethodDelete || oam.path != "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=oss-a" || len(oam.body) != 0 {
		t.Errorf("request %s %s %q", oam.method, oam.path, oam.body)
	}
	if len(oam.subscriptions) != 0 {
		t.Errorf("subscriptions left: %v", oam.subscriptions)
	}
}

func TestReadAlarmList(t *testing.T) {
	oam := newFakeOAM(t)
	c := oam.consumer()
	list, err := c.readAlarmList(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if oam.method != http.MethodGet || oam.path != "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1" || oam.query != "" {
		t.Errorf("request %s %s?%s", oam.method, oam.path, oam.query)
	}
	if list.Attributes.NumOfAlarmRecords != 1 || list.Attributes.AlarmRecords["scscf.9f3c2a7b1d4e6058"].PerceivedSeverity != mns.Major {
		t.Errorf("list %+v", list.Attributes)
	}
	const filter = `/AlarmList[id="1"]/attributes/alarmRecords/*[perceivedSeverity="MAJOR"]`
	if _, err := c.readAlarmList(t.Context(), filter); err != nil {
		t.Fatal(err)
	}
	if want := "filter=%2FAlarmList%5Bid%3D%221%22%5D%2Fattributes%2FalarmRecords%2F%2A%5BperceivedSeverity%3D%22MAJOR%22%5D"; oam.query != want {
		t.Errorf("query %s, want %s", oam.query, want)
	}
}

// The answer must have the names of the OpenAPI files, spelt exactly.
func TestReadAlarmListRefusesAnUnknownAttribute(t *testing.T) {
	oam := newFakeOAM(t)
	for _, c := range []struct {
		rewrite func([]byte) []byte
		want    string
	}{
		{withAttribute(`"administrativeState":"UNLOCKED"`), `"administrativeState" at / is not an attribute that oss takes`},
		{withAttribute(`"ObjectClass":"AlarmList"`), `"ObjectClass" at / is not an attribute that oss takes`},
		{withAttribute(`"id":"1"`), `"id" comes twice`},
		{func(b []byte) []byte {
			return regexp.MustCompile(`"alarmRecords":\{.*\}\}\}`).ReplaceAll(b, []byte(`"alarmRecords":{"scscf.1":null}}}`))
		}, "null at /attributes/alarmRecords/scscf.1"},
	} {
		oam.rewrite = c.rewrite
		if _, err := oam.consumer().readAlarmList(t.Context(), ""); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("error %v, want one that says %s", err, c.want)
		}
	}
}

// An answer larger than oss reads is said to be so, not taken for a
// body that is no JSON.
func TestAnAnswerTooLargeToRead(t *testing.T) {
	oam := newFakeOAM(t)
	oam.rewrite = func(b []byte) []byte { return append(b, bytes.Repeat([]byte(" "), maxAnswer)...) }
	if _, err := oam.consumer().readAlarmList(t.Context(), ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("error %v", err)
	}
}

func TestAcknowledgeSendsAMergePatch(t *testing.T) {
	oam := newFakeOAM(t)
	c := oam.consumer()
	if err := c.acknowledge(t.Context(), []string{"scscf.9f3c2a7b1d4e6058"}, mns.Acknowledged, "noc-operator", "oss-1"); err != nil {
		t.Fatal(err)
	}
	if oam.method != http.MethodPatch || oam.path != "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1" || oam.contentType != "application/merge-patch+json" {
		t.Errorf("request %s %s, %s", oam.method, oam.path, oam.contentType)
	}
	want := `{"id": "1", "attributes": {"alarmRecords": {"scscf.9f3c2a7b1d4e6058": {
	  "ackState": "ACKNOWLEDGED", "ackUserId": "noc-operator", "ackSystemId": "oss-1"}}}}`
	if !sameJSON(t, oam.body, []byte(want)) {
		t.Errorf("body %s, want %s", oam.body, want)
	}
	if r := oam.records["scscf.9f3c2a7b1d4e6058"]; r.AckState != mns.Acknowledged || r.AckUserId != "noc-operator" {
		t.Errorf("record %+v", r)
	}
	if err := c.acknowledge(t.Context(), []string{"scscf.9f3c2a7b1d4e6058"}, mns.Unacknowledged, "noc-operator", ""); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(oam.body), "ackSystemId") {
		t.Errorf("an empty ackSystemId is written: %s", oam.body)
	}
}

// TS 32.158 6.3.2 lets the producer answer a JSON Merge Patch with 200
// and the whole resource, which must be the AlarmList.
func TestAcknowledgeTakesTheAlarmListInTheAnswer(t *testing.T) {
	oam := newFakeOAM(t)
	oam.patchAnswersList = true
	c := oam.consumer()
	if err := c.acknowledge(t.Context(), []string{"scscf.9f3c2a7b1d4e6058"}, mns.Acknowledged, "noc-operator", "oss-1"); err != nil {
		t.Fatal(err)
	}
	oam.rewrite = withAttribute(`"administrativeState":"UNLOCKED"`)
	if err := c.acknowledge(t.Context(), []string{"scscf.9f3c2a7b1d4e6058"}, mns.Acknowledged, "noc-operator", "oss-1"); err == nil || !strings.Contains(err.Error(), "administrativeState") {
		t.Errorf("an AlarmList with administrativeState: %v", err)
	}
}

// An error message shows the start of a long answer only.
func TestAnErrorShowsTheStartOfTheAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, mns.ErrorResponseDefault{Type: mns.ServerLimitation, Title: strings.Repeat("x", 10*maxErrorAnswer)})
	}))
	defer server.Close()
	root, err := mns.ParseMnSRoot(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := consumer{root: root, subNetwork: mns.SubNetworkDN("ims"), client: server.Client()}
	err = c.unsubscribe(t.Context(), "1")
	if err == nil || len(err.Error()) > 2*maxErrorAnswer || !strings.Contains(err.Error(), "SERVER_LIMITATION") {
		t.Errorf("error of %d bytes: %.200v", len(err.Error()), err)
	}
}

func TestAcknowledgeSaysWhatTheOAMRefused(t *testing.T) {
	oam := newFakeOAM(t)
	err := oam.consumer().acknowledge(t.Context(), []string{"scscf.0000000000000000"}, mns.Acknowledged, "noc-operator", "oss-1")
	if err == nil || !strings.Contains(err.Error(), `"IE_NOT_FOUND"`) {
		t.Errorf("error %v", err)
	}
}

// An answer that is not the error response of TS 32.158 6.6.3, such as
// the 404 of another server, is shown as it came, with what is wrong
// with it.
func TestAnAnswerThatIsNoErrorResponse(t *testing.T) {
	oam := newFakeOAM(t)
	c := oam.consumer()
	c.subNetwork = mns.SubNetworkDN("other")
	_, err := c.readAlarmList(t.Context(), "")
	if err == nil || !strings.Contains(err.Error(), "404 Not Found") || !strings.Contains(err.Error(), "not application/json") {
		t.Errorf("error %v", err)
	}
}

// The commands print what they read, and take their flags.
func TestCommandsAgainstTheOAM(t *testing.T) {
	oam := newFakeOAM(t)
	log := slog.New(slog.DiscardHandler)
	root := oam.root.String()
	var out bytes.Buffer
	if err := run(t.Context(), log, []string{"subscribe", "-mns-root", root, "-id", "oss-a", "-types", "notifyNewAlarm,notifyAckStateChanged"}, &out); err != nil {
		t.Fatal(err)
	}
	var s mns.NtfSubscriptionControlSingle
	if err := decodeStrictly(out.Bytes(), &s); err != nil || s.Id != "oss-a" || !slices.Equal(s.Attributes.NotificationTypes, []mns.NotificationType{mns.NotifyNewAlarmType, mns.NotifyAckStateChangedType}) {
		t.Errorf("subscribe printed %s (%v)", out.Bytes(), err)
	}
	if s.Attributes.NotificationRecipientAddress != "http://oss:8080/notifications" {
		t.Errorf("the recipient is %q", s.Attributes.NotificationRecipientAddress)
	}
	if err := run(t.Context(), log, []string{"subscribe", "-mns-root", root, "-types", "notifyNewSecAlarm"}, io.Discard); err == nil {
		t.Error("a subscription to notifyNewSecAlarm was sent")
	}

	out.Reset()
	if err := run(t.Context(), log, []string{"alarms", "-mns-root", root}, &out); err != nil {
		t.Fatal(err)
	}
	var list mns.AlarmListSingle
	if err := decodeStrictly(out.Bytes(), &list); err != nil || list.Attributes.NumOfAlarmRecords != 1 {
		t.Errorf("alarms printed %s (%v)", out.Bytes(), err)
	}

	if err := run(t.Context(), log, []string{"ack", "-mns-root", root, "-user", "alice", "scscf.9f3c2a7b1d4e6058"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if r := oam.records["scscf.9f3c2a7b1d4e6058"]; r.AckState != mns.Acknowledged || r.AckUserId != "alice" || r.AckSystemId != "oss-1" {
		t.Errorf("record %+v", r)
	}
	if err := run(t.Context(), log, []string{"ack", "-mns-root", root, "-unacknowledge", "scscf.9f3c2a7b1d4e6058"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if r := oam.records["scscf.9f3c2a7b1d4e6058"]; r.AckState != mns.Unacknowledged {
		t.Errorf("record %+v", r)
	}
	if err := run(t.Context(), log, []string{"ack", "-mns-root", root}, io.Discard); err == nil {
		t.Error("ack with no alarm record ran")
	}

	if err := run(t.Context(), log, []string{"unsubscribe", "-mns-root", root, "-id", "oss-a"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(oam.subscriptions) != 0 {
		t.Errorf("subscriptions left: %v", oam.subscriptions)
	}
	for _, args := range [][]string{
		{"alarms", "-mns-root", "oam:8080"},
		{"alarms", "-subnetwork", "a,b"},
	} {
		if err := run(t.Context(), log, args, io.Discard); err == nil {
			t.Errorf("oss %q ran", args)
		}
	}
}
