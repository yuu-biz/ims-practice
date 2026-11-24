package oam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// waitFor bounds every wait of these tests for something that the
// watchers do on their goroutines.
const waitFor = 10 * time.Second

// journalNode is a node whose process can start again with a new alarm
// list and journal, and whose journal can stop answering while the rest
// of its maintenance API answers.
type journalNode struct {
	name   string
	cfg    maintenance.Config
	server *httptest.Server

	mu         sync.Mutex
	node       *maintenance.Node
	eventsDown bool
}

func newJournalNode(t *testing.T, name string, cfg maintenance.Config) *journalNode {
	t.Helper()
	cfg.Node = name
	cfg.Instance = name + "-0"
	cfg.LogLevel = new(slog.LevelVar)
	cfg.Functions = []maintenance.Function{{Role: name, Users: func() int { return 0 }}}
	cfg.Configuration = func() any { return map[string]string{"node": name} }
	n := &journalNode{name: name, cfg: cfg}
	n.start(t)
	n.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		node, down := n.node, n.eventsDown
		n.mu.Unlock()
		if down && r.URL.Path == maintenance.AlarmEventsPath {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		node.Handler().ServeHTTP(w, r)
	}))
	t.Cleanup(n.server.Close)
	return n
}

// start starts the process of the node anew.
func (n *journalNode) start(t *testing.T) {
	t.Helper()
	node, err := maintenance.New(n.cfg)
	if err != nil {
		t.Fatal(err)
	}
	n.mu.Lock()
	n.node = node
	n.mu.Unlock()
}

func (n *journalNode) current() *maintenance.Node {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.node
}

func (n *journalNode) setEventsDown(down bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.eventsDown = down
}

func (n *journalNode) raise(t *testing.T, peer maintenance.Peer, s maintenance.PerceivedSeverity) {
	t.Helper()
	err := n.current().RaiseAlarm(maintenance.Alarm{AlarmIdentity: diameterPeerDown(n.name, peer), PerceivedSeverity: s, AdditionalText: "no answer to 3 DWR"})
	if err != nil {
		t.Fatal(err)
	}
}

func (n *journalNode) clear(t *testing.T, peer maintenance.Peer) {
	t.Helper()
	if err := n.current().ClearAlarm(diameterPeerDown(n.name, peer)); err != nil {
		t.Fatal(err)
	}
}

func (n *journalNode) endpoint() Endpoint {
	return Endpoint{Name: n.name, Roles: []string{n.name}, URL: n.server.URL}
}

// notification is a notification that the OSS stand-in received.
type notification struct {
	recipient string
	body      map[string]any
	raw       []byte
}

func (n notification) field(name string) string { return fmt.Sprint(n.body[name]) }

func (n notification) String() string { return n.field("notificationType") + " " + n.field("href") }

// ossStandIn is the recipient of the notifications of one or more
// subscriptions: it keeps what it receives, and answers with what
// answer says for the nth notification to a recipient.
type ossStandIn struct {
	server   *httptest.Server
	received chan notification

	mu     sync.Mutex
	count  map[string]int
	answer func(recipient string, n int) (status int, delay time.Duration)
}

func newOSSStandIn(t *testing.T) *ossStandIn {
	t.Helper()
	o := &ossStandIn{received: make(chan notification, 100), count: map[string]int{}}
	o.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || json.Unmarshal(b, &body) != nil {
			t.Errorf("the OSS received %s %s %q %s", r.Method, r.URL, r.Header.Get("Content-Type"), b)
		}
		o.mu.Lock()
		o.count[r.URL.Path]++
		k, answer := o.count[r.URL.Path], o.answer
		o.mu.Unlock()
		status, delay := http.StatusNoContent, time.Duration(0)
		if answer != nil {
			status, delay = answer(r.URL.Path, k)
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				// The OAM gave up: the notification was not taken.
				return
			}
		}
		if status == http.StatusNoContent {
			o.received <- notification{recipient: r.URL.Path, body: body, raw: b}
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(o.server.Close)
	return o
}

func (o *ossStandIn) setAnswer(answer func(recipient string, n int) (int, time.Duration)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.answer = answer
}

// next is the next notification that the OSS took, for any recipient.
func (o *ossStandIn) next(t *testing.T, waitingFor string) notification {
	t.Helper()
	select {
	case n := <-o.received:
		return n
	case <-time.After(waitFor):
		t.Fatalf("no notification within %v: waiting for %s", waitFor, waitingFor)
		return notification{}
	}
}

// none checks that the OSS has taken no notification beyond those read.
func (o *ossStandIn) none(t *testing.T) {
	t.Helper()
	select {
	case n := <-o.received:
		t.Errorf("an unexpected notification to %s: %s", n.recipient, n.raw)
	default:
	}
}

// notifier is an OAM that sends the notifications of its nodes, with the
// file stores and short times.
type notifier struct {
	*testServer
	watches  *FileAlarmWatchStore
	registry *metrics.Registry
	stop     func()
}

func newNotifier(t *testing.T, before func(*FileAlarmWatchStore), nodes ...*journalNode) *notifier {
	t.Helper()
	return startNotifier(t, t.TempDir(), before, nodes...)
}

// startNotifier starts an OAM whose stores are in dir. Its stop ends
// RunNotifications and waits for it; the cleanup of the test calls it
// too.
func startNotifier(t *testing.T, dir string, before func(*FileAlarmWatchStore), nodes ...*journalNode) *notifier {
	t.Helper()
	return startNotifierWith(t, dir, nil, before, nodes...)
}

// startNotifierWith is startNotifier with a configuration that configure
// changes, when it is not nil.
func startNotifierWith(t *testing.T, dir string, configure func(*Config), before func(*FileAlarmWatchStore), nodes ...*journalNode) *notifier {
	t.Helper()
	subs, err := NewFileNtfSubscriptionStore(filepath.Join(dir, "ntf-subscriptions.json"))
	if err != nil {
		t.Fatal(err)
	}
	watches, err := NewFileAlarmWatchStore(filepath.Join(dir, "alarm-watch"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if before != nil {
		before(watches)
	}
	c := mnsConfig()
	c.NtfSubscriptions, c.AlarmWatches = subs, watches
	c.AlarmEventsWait, c.WatchRenewal, c.NotificationTimeout = time.Second, 100*time.Millisecond, 300*time.Millisecond
	// A recipient that missed a notification is tried again at the next
	// one.
	c.MissedRetryInterval = time.Millisecond
	c.Metrics = metrics.NewRegistry()
	c.Now = time.Now
	for _, n := range nodes {
		c.Nodes = append(c.Nodes, n.endpoint())
	}
	if configure != nil {
		configure(&c)
	}
	n := &notifier{testServer: newTestServer(t, c), watches: watches, registry: c.Metrics}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- n.oam.RunNotifications(ctx) }()
	var once sync.Once
	n.stop = func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("RunNotifications: %v", err)
				}
			case <-time.After(waitFor):
				t.Errorf("RunNotifications did not return within %v of its end", waitFor)
			}
		})
	}
	t.Cleanup(n.stop)
	return n
}

// subscribe creates the subscription id, which sends to the path id of
// the OSS, with the attributes besides the recipient.
func (n *notifier) subscribe(t *testing.T, o *ossStandIn, id, attributes string) {
	t.Helper()
	if attributes != "" {
		attributes = ", " + attributes
	}
	body := `{"id": "` + id + `", "objectClass": "NtfSubscriptionControl", "attributes": {"notificationRecipientAddress": "` + o.server.URL + "/" + id + `"` + attributes + `}}`
	if a := n.mnsCall(t, "PUT", ntfSubscriptionPath+id, "application/json", body); a.status != http.StatusCreated {
		t.Fatalf("PUT %s: %d %s", id, a.status, a.body)
	}
}

// watching waits until the watcher of node has had its first answer, and
// so watches from the end of the node's journal.
func (n *notifier) watching(t *testing.T, node string) AlarmWatchState {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for {
		st, err := n.watches.AlarmWatchState(context.Background(), node)
		if err != nil {
			t.Fatal(err)
		}
		if st.JournalID != "" {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watcher of %s had no answer within %v", node, waitFor)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// strictly decodes the body of n into v, refusing a name that v lacks,
// so that the body has the names of the schema in package mns.
func strictly[T any](t *testing.T, n notification) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(n.raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("%s: %v", n.raw, err)
	}
	return v
}

func TestTheAlarmsOfANodeAreNotified(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	n.watching(t, "scscf")

	scscf.raise(t, hssPeer, maintenance.Major)
	_, own := recordOf(t, "scscf", &fakeNode{name: "scscf", node: scscf.current()}, "scscf", hssPeer)
	key := "scscf." + own.AlarmID
	href := "http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=" + hssPeer

	got := strictly[mns.NotifyNewAlarm](t, oss.next(t, "notifyNewAlarm"))
	want := mns.NotifyNewAlarm{
		NotificationHeader: mns.NotificationHeader{Href: href, NotificationId: own.NotificationID, NotificationType: mns.NotifyNewAlarmType,
			EventTime: own.AlarmRaisedTime, SystemDN: "SubNetwork=ims"},
		AlarmId: key, AlarmType: mns.CommunicationsAlarm, ProbableCause: "COMMUNICATIONS_SUBSYSTEM_FAILURE",
		SpecificProblem: "Diameter peer down", PerceivedSeverity: mns.Major, AdditionalText: "no answer to 3 DWR",
	}
	if !got.EventTime.Equal(want.EventTime) {
		t.Errorf("eventTime %v, want %v", got.EventTime, want.EventTime)
	}
	got.EventTime = want.EventTime
	if got != want {
		t.Errorf("notifyNewAlarm:\n got %+v\nwant %+v", got, want)
	}

	scscf.raise(t, hssPeer, maintenance.Critical)
	changed := strictly[mns.NotifyChangedAlarmGeneral](t, oss.next(t, "notifyChangedAlarmGeneral"))
	if changed.NotificationType != mns.NotifyChangedAlarmGeneralType || changed.PerceivedSeverity != mns.Critical || changed.AlarmId != key ||
		changed.NotificationId == own.NotificationID || changed.Href != href {
		t.Errorf("notifyChangedAlarmGeneral: %+v", changed)
	}

	if a := n.mnsCall(t, "PATCH", alarmListPath, mergePatchJSON, ackPatch(map[string]string{key: acknowledge})); a.status != http.StatusNoContent {
		t.Fatalf("PATCH: %d %s", a.status, a.body)
	}
	acked := strictly[mns.NotifyAckStateChanged](t, oss.next(t, "notifyAckStateChanged"))
	if acked.AckState != mns.Acknowledged || acked.AckUserId != "noc-operator" || acked.AckSystemId != "oss-1" || acked.PerceivedSeverity != mns.Critical {
		t.Errorf("notifyAckStateChanged: %+v", acked)
	}
	// The record of the list carries the id of its last notification.
	if r := n.alarmList(t, "").Attributes.AlarmRecords[key]; r.NotificationId != acked.NotificationId {
		t.Errorf("the record's notificationId %d, the notification's %d", r.NotificationId, acked.NotificationId)
	}

	scscf.clear(t, hssPeer)
	cleared := strictly[mns.NotifyClearedAlarm](t, oss.next(t, "notifyClearedAlarm"))
	if cleared.PerceivedSeverity != mns.Cleared || cleared.AlarmId != key || cleared.EventTime.IsZero() {
		t.Errorf("notifyClearedAlarm: %+v", cleared)
	}
	oss.none(t)

	n.counted(t, `oam_alarm_notifications_total{notification_type="notifyNewAlarm",result="delivered"} 1`,
		`oam_alarm_notifications_total{notification_type="notifyClearedAlarm",result="delivered"} 1`)
}

// counted waits until the metrics have each line. The OAM counts a
// notification once the recipient has answered, which may be after the
// recipient has passed the notification on.
func (n *notifier) counted(t *testing.T, lines ...string) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for {
		var text bytes.Buffer
		if err := n.registry.WriteText(&text); err != nil {
			t.Fatal(err)
		}
		var missing []string
		for _, line := range lines {
			if !strings.Contains(text.String(), line+"\n") {
				missing = append(missing, line)
			}
		}
		if len(missing) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no %q within %v in\n%s", missing, waitFor, text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestANotificationThatAStopCutShortIsSentAgain(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	dir := t.TempDir()
	n := startNotifier(t, dir, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	n.watching(t, "scscf")
	// The OSS takes so long over the first notification that the OAM
	// stops before it answers.
	arrived := make(chan struct{})
	var once sync.Once
	oss.setAnswer(func(_ string, k int) (int, time.Duration) {
		if k == 1 {
			once.Do(func() { close(arrived) })
			return http.StatusNoContent, waitFor
		}
		return http.StatusNoContent, 0
	})
	scscf.raise(t, hssPeer, maintenance.Major)
	select {
	case <-arrived:
	case <-time.After(waitFor):
		t.Fatalf("the notification did not reach the OSS within %v", waitFor)
	}
	n.stop()

	// The OAM that starts again on the same directory sends it again,
	// and the OSS is owed no notifyAlarmListRebuilt: it missed nothing.
	again := startNotifier(t, dir, nil, scscf)
	got := oss.next(t, "the notification that the stop cut short")
	if got.field("notificationType") != "notifyNewAlarm" {
		t.Errorf("got %s", got.raw)
	}
	again.stop()
	oss.none(t)
}

func TestSubscriptionsSelectTheirNotifications(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "cleared", `"notificationTypes": ["notifyClearedAlarm"]`)
	n.subscribe(t, oss, "critical", `"notificationFilter": "perceivedSeverity=\"CRITICAL\""`)
	n.subscribe(t, oss, "pcscf", `"notificationFilter": "contains(href,\"ManagedElement=pcscf\")"`)
	n.watching(t, "scscf")

	scscf.raise(t, "hss-1", maintenance.Major)
	scscf.raise(t, "hss-2", maintenance.Critical)
	scscf.clear(t, "hss-1")
	got := map[string][]string{}
	for range 2 {
		r := oss.next(t, "the notifications that the subscriptions select")
		got[r.recipient] = append(got[r.recipient], r.field("notificationType")+" "+r.field("perceivedSeverity"))
	}
	if strings.Join(got["/critical"], ",") != "notifyNewAlarm CRITICAL" || strings.Join(got["/cleared"], ",") != "notifyClearedAlarm CLEARED" {
		t.Errorf("got %v", got)
	}
	// The cleared alarm came last, and each notification goes to its
	// recipients before the next one: nothing more is on its way.
	oss.none(t)
}

func TestANodeThatStartsAgain(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	first := n.watching(t, "scscf")

	// The new process raises an alarm before the OAM finds that it is
	// new.
	scscf.start(t)
	scscf.raise(t, "hss-1", maintenance.Major)
	rebuilt := strictly[mns.NotifyAlarmListRebuilt](t, oss.next(t, "notifyAlarmListRebuilt"))
	if rebuilt.Href != "http://oam:8080/SubNetwork=ims/ManagedElement=scscf" || rebuilt.Reason != mns.ReasonSystemRestarts ||
		rebuilt.AlarmListAlignmentRequirement != mns.AlignmentRequired || rebuilt.SystemDN != "SubNetwork=ims" {
		t.Errorf("notifyAlarmListRebuilt: %+v", rebuilt)
	}
	// That alarm is in the list that the OSS reads again, and is not
	// notified after the notifyAlarmListRebuilt; the next alarm is.
	scscf.raise(t, "hss-2", maintenance.Major)
	if got := oss.next(t, "the next alarm of the new process"); got.field("notificationType") != "notifyNewAlarm" || !strings.HasSuffix(got.field("href"), "Peer=hss-2") {
		t.Errorf("got %s, want the new alarm of hss-2", got.raw)
	}
	if st := n.watching(t, "scscf"); st.JournalID == first.JournalID || st.Generation != first.Generation {
		t.Errorf("the state after the restart: %+v, before %+v", st, first)
	}
}

func TestANodeThatStopsAnswering(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	n.watching(t, "scscf")

	scscf.setEventsDown(true)
	faulty := strictly[mns.NotifyPotentialFaultyAlarmList](t, oss.next(t, "notifyPotentialFaultyAlarmList"))
	if faulty.Href != "http://oam:8080/SubNetwork=ims/ManagedElement=scscf" || faulty.Reason != mns.ReasonServiceProviderNECommunicationError ||
		faulty.NotificationType != mns.NotifyPotentialFaultyAlarmListType {
		t.Errorf("notifyPotentialFaultyAlarmList: %+v", faulty)
	}
	// The node answers a read of its list, but its watcher has found it
	// not to answer, so its part of the list is unreliable.
	if got := n.alarmList(t, "").Attributes.UnreliableAlarmScope; got != "SubNetwork=ims,ManagedElement=scscf" {
		t.Errorf("unreliableAlarmScope while the watcher cannot reach the node: %q", got)
	}

	scscf.setEventsDown(false)
	back := strictly[mns.NotifyAlarmListRebuilt](t, oss.next(t, "notifyAlarmListRebuilt"))
	if back.Reason != mns.ReasonSystemNECommunicationError || back.AlarmListAlignmentRequirement != mns.AlignmentRequired ||
		back.Href != faulty.Href || back.NotificationId == faulty.NotificationId {
		t.Errorf("notifyAlarmListRebuilt: %+v", back)
	}
	deadline := time.Now().Add(waitFor)
	for n.alarmList(t, "").Attributes.UnreliableAlarmScope != "" {
		if time.Now().After(deadline) {
			t.Fatalf("the list is still unreliable %v after the node came back", waitFor)
		}
		time.Sleep(10 * time.Millisecond)
	}
	oss.none(t)
}

func TestEventsLostFromTheJournal(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{MaxAlarmEvents: 2})
	for _, peer := range []maintenance.Peer{"hss-1", "hss-2", "hss-3", "hss-4"} {
		scscf.raise(t, peer, maintenance.Major)
	}
	journal := scscf.current().AlarmEvents(context.Background(), 0, 0).JournalID
	oss := newOSSStandIn(t)
	// An earlier watch sent the first event; the journal holds the last
	// two of four.
	n := newNotifier(t, func(w *FileAlarmWatchStore) {
		watch, _, err := w.Watch(context.Background(), "scscf", "earlier")
		if err != nil {
			t.Fatal(err)
		}
		st := watch.State()
		st.JournalID, st.LastNumber = journal, 1
		if err := watch.Save(context.Background(), st); err != nil {
			t.Fatal(err)
		}
		watch.Release(context.Background())
	}, scscf)
	n.subscribe(t, oss, "oss-1", "")

	rebuilt := strictly[mns.NotifyAlarmListRebuilt](t, oss.next(t, "notifyAlarmListRebuilt"))
	if rebuilt.Reason != mns.ReasonIndeterminate || rebuilt.Href != "http://oam:8080/SubNetwork=ims/ManagedElement=scscf" {
		t.Errorf("notifyAlarmListRebuilt: %+v", rebuilt)
	}
	for _, peer := range []string{"hss-3", "hss-4"} {
		if got := oss.next(t, "the events that the journal holds"); got.field("notificationType") != "notifyNewAlarm" || !strings.HasSuffix(got.field("href"), "Peer="+peer) {
			t.Errorf("got %s, want the new alarm of %s", got.raw, peer)
		}
	}
}

func TestARecordThatTheFullListDrops(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{MaxAlarmRecords: 1})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	n.watching(t, "scscf")

	scscf.raise(t, "hss-1", maintenance.Major)
	scscf.raise(t, "hss-2", maintenance.Major)
	var got []string
	for range 3 {
		r := oss.next(t, "the alarms and the record that was dropped")
		got = append(got, r.field("notificationType")+" "+r.field("reason")+r.field("perceivedSeverity"))
	}
	want := "notifyNewAlarm <nil>MAJOR,notifyAlarmListRebuilt indeterminate<nil>,notifyNewAlarm <nil>MAJOR"
	if strings.Join(got, ",") != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestARecipientThatMissedANotification(t *testing.T) {
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := newNotifier(t, nil, scscf)
	n.subscribe(t, oss, "oss-1", "")
	n.subscribe(t, oss, "oss-2", "")
	n.watching(t, "scscf")
	// oss-1 refuses its first notification, and oss-2 takes too long
	// over it.
	oss.setAnswer(func(recipient string, k int) (int, time.Duration) {
		switch {
		case recipient == "/oss-1" && k == 1:
			return http.StatusInternalServerError, 0
		case recipient == "/oss-2" && k == 1:
			return http.StatusNoContent, 2 * time.Second
		}
		return http.StatusNoContent, 0
	})

	// After the miss no alarm changes, and the notifyAlarmListRebuilt
	// that the miss owes each of them comes all the same, once its turn
	// has come and the recipient answers again.
	scscf.raise(t, "hss-1", maintenance.Major)
	got := map[string]string{}
	for range 2 {
		r := oss.next(t, "the notification that the miss owes")
		got[r.recipient] = r.field("notificationType") + " " + r.field("href")
	}
	for _, id := range []string{"/oss-1", "/oss-2"} {
		if want := "notifyAlarmListRebuilt http://oam:8080/SubNetwork=ims"; got[id] != want {
			t.Errorf("%s got %q, want %q", id, got[id], want)
		}
	}
	n.unmarked(t)
	// Once it is taken, the next ones go to them again.
	scscf.raise(t, "hss-3", maintenance.Major)
	for range 2 {
		r := oss.next(t, "the notification after the owed one")
		if r.field("notificationType") != "notifyNewAlarm" || !strings.HasSuffix(r.field("href"), "Peer=hss-3") {
			t.Errorf("%s got %s", r.recipient, r.raw)
		}
	}
	n.counted(t, `oam_alarm_notifications_total{notification_type="notifyNewAlarm",result="failed"} 2`)
}

// unmarked waits until no subscription is marked Missed.
func (n *notifier) unmarked(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for {
		subs, err := n.oam.ntfSubscriptions.NtfSubscriptions(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		marked := false
		for _, s := range subs {
			marked = marked || s.Missed
		}
		if !marked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the subscriptions are still marked after %v: %+v", waitFor, subs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestARecipientThatDoesNotAnswerHoldsUpNoOther: one OSS swallows every
// request. It costs the watcher one timeout, for the first
// notification, and not one for each: the other OSS gets the rest at
// once.
func TestARecipientThatDoesNotAnswerHoldsUpNoOther(t *testing.T) {
	const timeout = time.Second
	scscf := newJournalNode(t, "scscf", maintenance.Config{})
	oss := newOSSStandIn(t)
	n := startNotifierWith(t, t.TempDir(), func(c *Config) {
		c.NotificationTimeout = timeout
		c.MissedRetryInterval = time.Hour
	}, nil, scscf)
	n.subscribe(t, oss, "healthy", "")
	n.subscribe(t, oss, "silent", "")
	n.watching(t, "scscf")
	oss.setAnswer(func(recipient string, _ int) (int, time.Duration) {
		if recipient == "/silent" {
			return http.StatusNoContent, waitFor
		}
		return http.StatusNoContent, 0
	})
	const alarms = 6
	start := time.Now()
	for k := range alarms {
		scscf.raise(t, maintenance.Peer(fmt.Sprintf("hss-%d", k)), maintenance.Major)
	}
	for k := range alarms {
		r := oss.next(t, "the alarms at the recipient that answers")
		if r.recipient != "/healthy" || !strings.HasSuffix(r.field("href"), fmt.Sprintf("Peer=hss-%d", k)) {
			t.Errorf("got %s at %s, want the alarm of hss-%d", r.raw, r.recipient, k)
		}
	}
	// Held up behind the silent OSS, the six would take six timeouts.
	if elapsed := time.Since(start); elapsed > 3*timeout {
		t.Errorf("the recipient that answers got the six alarms in %v", elapsed)
	}
	oss.none(t)
}

func TestRunNotificationsNeedsItsStores(t *testing.T) {
	ts := newTestServer(t, mnsConfig(), newFakeNode(t, "scscf", []string{"scscf"}))
	if err := ts.oam.RunNotifications(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("RunNotifications without stores: %v", err)
	}
}

func TestPlanEvents(t *testing.T) {
	nb, err := newNorthbound("ims", testMnSRoot, []Endpoint{{Name: "scscf"}})
	if err != nil {
		t.Fatal(err)
	}
	now := testTime
	added := func(n uint64) maintenance.AlarmEvent {
		return maintenance.AlarmEvent{Number: n, EventType: maintenance.AlarmAdded, EventTime: now, AlarmRecord: maintenance.AlarmRecord{
			AlarmID: fmt.Sprintf("%016x", n), ObjectInstance: "ManagedElement=scscf,ManagedFunction=scscf", Role: "scscf",
			NotificationID: int64(n), AlarmRaisedTime: now, AlarmType: maintenance.CommunicationsAlarm,
			ProbableCause: maintenance.CommunicationsSubsystemFailure, PerceivedSeverity: maintenance.Major, AckState: maintenance.Unacknowledged}}
	}
	for _, tt := range []struct {
		name  string
		st    AlarmWatchState
		ev    maintenance.AlarmEvents
		want  []string
		final AlarmWatchState
	}{
		{"the first answer starts at the end of the journal",
			AlarmWatchState{Seed: "s"},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 2, Events: []maintenance.AlarmEvent{added(1), added(2)}},
			nil, AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 2}},
		{"events",
			AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 2},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 4, Events: []maintenance.AlarmEvent{added(3), added(4)}},
			[]string{"notifyNewAlarm", "notifyNewAlarm"}, AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 4}},
		{"another journal",
			AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 7, Unreachable: true, Transitions: 1},
			maintenance.AlarmEvents{JournalID: "j2", LastNumber: 9, Lost: true, Events: []maintenance.AlarmEvent{added(1)}},
			[]string{"notifyAlarmListRebuilt System restarts"}, AlarmWatchState{Seed: "s", JournalID: "j2", LastNumber: 9, Transitions: 2}},
		{"a state that was lost",
			AlarmWatchState{Seed: "s", Generation: 3, Lost: true},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 4, Events: []maintenance.AlarmEvent{added(3), added(4)}},
			[]string{"notifyAlarmListRebuilt indeterminate"}, AlarmWatchState{Seed: "s", Generation: 3, JournalID: "j1", LastNumber: 4}},
		{"back and lost",
			AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 1, Unreachable: true, Transitions: 1},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 5, Lost: true, Events: []maintenance.AlarmEvent{added(5)}},
			[]string{"notifyAlarmListRebuilt System-NE communication error", "notifyNewAlarm"}, AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 5, Transitions: 2}},
		{"lost",
			AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 1},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 5, Lost: true, Events: []maintenance.AlarmEvent{added(4), added(5)}},
			[]string{"notifyAlarmListRebuilt indeterminate", "notifyNewAlarm", "notifyNewAlarm"}, AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 5}},
		{"no event",
			AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 5},
			maintenance.AlarmEvents{JournalID: "j1", LastNumber: 5},
			nil, AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 5}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			steps := nb.planEvents("scscf", tt.st, tt.ev, now)
			var got []string
			for _, step := range steps {
				for _, n := range step.notifications {
					s := string(n.notificationType)
					if r, ok := n.body.(mns.NotifyAlarmListRebuilt); ok {
						s += " " + r.Reason
					}
					got = append(got, s)
				}
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("notifications %q, want %q", got, tt.want)
			}
			if final := steps[len(steps)-1].state; final != tt.final {
				t.Errorf("state %+v, want %+v", final, tt.final)
			}
		})
	}
	// The same reason for the same part of the list gets the same id from
	// any instance, and another reason another one.
	a := nb.planEvents("scscf", AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 1}, maintenance.AlarmEvents{JournalID: "j1", Lost: true}, now)
	b := nb.planEvents("scscf", AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 1, Generation: 2}, maintenance.AlarmEvents{JournalID: "j1", Lost: true}, now.Add(time.Second))
	c := nb.planEvents("scscf", AlarmWatchState{Seed: "s", JournalID: "j1", LastNumber: 2}, maintenance.AlarmEvents{JournalID: "j1", Lost: true}, now)
	id := func(steps []watchStep) int64 {
		return steps[0].notifications[0].body.(mns.NotifyAlarmListRebuilt).NotificationId
	}
	if id(a) != id(b) || id(a) == id(c) || id(a) <= 0 || id(a) >= 1<<53 {
		t.Errorf("the ids %d, %d, %d", id(a), id(b), id(c))
	}
}

// failingSubscriptions is a subscription store that fails every read
// of the subscriptions until a time, and the next writes of a mark, as
// many as the test says.
type failingSubscriptions struct {
	NtfSubscriptionStore
	readsUntil atomic.Int64
	marks      atomic.Int32
}

var errStore = errors.New("the store does not answer")

func (f *failingSubscriptions) NtfSubscriptions(ctx context.Context) ([]NtfSubscriptionControl, error) {
	if time.Now().UnixNano() < f.readsUntil.Load() {
		return nil, errStore
	}
	return f.NtfSubscriptionStore.NtfSubscriptions(ctx)
}

func (f *failingSubscriptions) MarkNtfSubscriptionMissed(ctx context.Context, id string, retryAt time.Time) error {
	if f.marks.Add(-1) >= 0 {
		return errStore
	}
	return f.NtfSubscriptionStore.MarkNtfSubscriptionMissed(ctx, id, retryAt)
}

// TestANotificationThatTheStoreKeptFromGoingIsSentAgain: when the store
// of the subscriptions fails, the watcher does not take the event for
// sent. It sends the notification again, after a wait, once the store
// answers, and only then moves on.
func TestANotificationThatTheStoreKeptFromGoingIsSentAgain(t *testing.T) {
	for _, tt := range []struct {
		name string
		fail func(*failingSubscriptions, *ossStandIn)
	}{
		{"the subscriptions cannot be read", func(f *failingSubscriptions, _ *ossStandIn) {
			f.readsUntil.Store(time.Now().Add(500 * time.Millisecond).UnixNano())
		}},
		{"the mark of a miss cannot be written", func(f *failingSubscriptions, oss *ossStandIn) {
			// The OSS refuses the first notification, and the mark that
			// would have it read the list again cannot be written.
			oss.setAnswer(func(_ string, k int) (int, time.Duration) {
				if k == 1 {
					return http.StatusInternalServerError, 0
				}
				return http.StatusNoContent, 0
			})
			f.marks.Store(1)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			scscf := newJournalNode(t, "scscf", maintenance.Config{})
			oss := newOSSStandIn(t)
			store := &failingSubscriptions{}
			n := startNotifierWith(t, t.TempDir(), func(c *Config) {
				store.NtfSubscriptionStore = c.NtfSubscriptions
				c.NtfSubscriptions = store
				c.MissedRetryInterval = time.Hour
			}, nil, scscf)
			n.subscribe(t, oss, "oss-1", "")
			n.watching(t, "scscf")
			tt.fail(store, oss)

			scscf.raise(t, "hss-1", maintenance.Major)
			got := oss.next(t, "the notification that the store kept from going")
			if got.field("notificationType") != "notifyNewAlarm" || !strings.HasSuffix(got.field("href"), "Peer=hss-1") {
				t.Errorf("got %s", got.raw)
			}
			// The journal moved on only after it: the next alarm comes
			// next.
			scscf.raise(t, "hss-2", maintenance.Major)
			if got := oss.next(t, "the next alarm"); !strings.HasSuffix(got.field("href"), "Peer=hss-2") {
				t.Errorf("got %s, want the alarm of hss-2", got.raw)
			}
			oss.none(t)
		})
	}
}

func TestGiveUpCountsTheWatchesThatAreHeld(t *testing.T) {
	gone := make(chan struct{})
	close(gone)
	watching := map[string]chan struct{}{"hss": gone, "icscf": make(chan struct{}), "scscf": make(chan struct{})}
	var gave []string
	// One watcher is giving its watch up already, so of the two others,
	// one more goes for a share of one.
	held := giveUp(watching, 1, func(node string) { gave = append(gave, node) })
	open := 0
	for _, release := range watching {
		select {
		case <-release:
		default:
			open++
		}
	}
	if held != 1 || open != 1 || len(gave) != 1 || gave[0] == "hss" {
		t.Errorf("held %d, %d open, gave %v; want one held, one open and one given up", held, open, gave)
	}
	// Within the share, nothing more goes.
	if held := giveUp(watching, 1, func(node string) { t.Errorf("gave %s up within the share", node) }); held != 1 {
		t.Errorf("held %d within the share", held)
	}
}

// leasedWatches is a watch store whose watches last lease.
type leasedWatches struct {
	*FileAlarmWatchStore
	lease time.Duration
}

func (l leasedWatches) WatchLease() time.Duration { return l.lease }

func TestNewChecksTheRenewalAgainstTheLease(t *testing.T) {
	watches, err := NewFileAlarmWatchStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	c := mnsConfig()
	c.Nodes = []Endpoint{{Name: "hss", Roles: []string{"hss"}, URL: "http://hss:8085"}}
	c.AlarmWatches, c.WatchRenewal = leasedWatches{watches, 15 * time.Second}, 8*time.Second
	if _, err := New(c); err == nil {
		t.Error("New took a renewal of more than half the lease")
	}
	c.WatchRenewal = 5 * time.Second
	if _, err := New(c); err != nil {
		t.Errorf("New with a renewal of a third of the lease: %v", err)
	}
}
