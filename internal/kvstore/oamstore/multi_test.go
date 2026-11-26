package oamstore_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvjson"
	"github.com/yuu-biz/ims-practice/internal/kvstore/oamstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/storetest"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// The tests below run several OAM servers on one store, as several
// instances of the oam role run on one Valkey, and check what the
// instances do together: how they share the watches of the nodes, and
// that no event is lost when one of them stops.

// waitFor bounds every wait for what the instances do on their own
// goroutines.
const waitFor = 10 * time.Second

// node is a node that serves the maintenance API.
type node struct {
	name   string
	node   *maintenance.Node
	server *httptest.Server
}

func newNode(t *testing.T, name string) *node {
	t.Helper()
	n, err := maintenance.New(maintenance.Config{
		Node: name, Instance: name + "-0", LogLevel: new(slog.LevelVar),
		Functions:     []maintenance.Function{{Role: name, Users: func() int { return 0 }}},
		Configuration: func() any { return map[string]string{"node": name} },
	})
	if err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(n.Handler())
	t.Cleanup(s.Close)
	return &node{name: name, node: n, server: s}
}

func (n *node) raise(t *testing.T, peer maintenance.Peer) {
	t.Helper()
	err := n.node.RaiseAlarm(maintenance.Alarm{
		AlarmIdentity: maintenance.AlarmIdentity{Role: n.name, Peer: peer, AlarmType: maintenance.CommunicationsAlarm,
			ProbableCause: maintenance.CommunicationsSubsystemFailure, SpecificProblem: "Diameter peer down"},
		PerceivedSeverity: maintenance.Major,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// recipient keeps every notification it is sent, as it arrives, and
// answers each as answer says: with the status, at once when wait is
// nil, or once wait is closed. A request that ends before then gets no
// answer.
type recipient struct {
	server   *httptest.Server
	received chan map[string]any

	mu     sync.Mutex
	answer func(body map[string]any) (status int, wait <-chan struct{})
}

func newRecipient(t *testing.T) *recipient {
	t.Helper()
	r := &recipient{received: make(chan map[string]any, 100)}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		if err := json.Unmarshal(b, &body); err != nil {
			t.Errorf("a notification that is no JSON: %s", b)
		}
		r.received <- body
		r.mu.Lock()
		answer := r.answer
		r.mu.Unlock()
		status, wait := http.StatusNoContent, (<-chan struct{})(nil)
		if answer != nil {
			status, wait = answer(body)
		}
		if wait != nil {
			select {
			case <-wait:
			case <-req.Context().Done():
				return
			}
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recipient) setAnswer(answer func(map[string]any) (int, <-chan struct{})) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.answer = answer
}

func (r *recipient) next(t *testing.T, waitingFor string) map[string]any {
	t.Helper()
	select {
	case n := <-r.received:
		return n
	case <-time.After(waitFor):
		t.Fatalf("no notification within %v: waiting for %s", waitFor, waitingFor)
		return nil
	}
}

// link carries the requests of one instance to one node. A test can
// break it while the node and the links of the other instances work.
type link struct {
	server *httptest.Server
	broken atomic.Bool
}

func newLink(t *testing.T, n *node) *link {
	t.Helper()
	l := &link{}
	h := n.node.Handler()
	l.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !l.broken.Load() {
			h.ServeHTTP(w, req)
			return
		}
		// The connection closes with no answer.
		if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
			conn.Close()
		}
	}))
	t.Cleanup(l.server.Close)
	return l
}

// instance is one OAM instance on the shared store.
type instance struct {
	name   string
	server *oam.Server
	stop   context.CancelFunc
	done   chan struct{}
	// links are the links of the instance to each node, by its name.
	links map[string]*link
}

// cluster is the OAM instances on one store, and the nodes they watch.
type cluster struct {
	kv    kvstore.Store
	cfg   kvjson.Config
	nodes []*node
}

func (c *cluster) start(t *testing.T, name string) *instance {
	t.Helper()
	return c.startOn(t, name, c.kv)
}

// startOn starts the instance on kv, which stands between it and the
// store of the cluster.
func (c *cluster) startOn(t *testing.T, name string, kv kvstore.Store) *instance {
	t.Helper()
	// The instances renew every 100 ms a lease of 2 s, so that a loaded
	// machine does not let a watch run out by itself. A recipient that
	// missed a notification is sent the notifyAlarmListRebuilt it is owed
	// a second after, longer than the tests take to answer one that they
	// hold.
	store := oamstore.NewWithLease(kv, c.cfg, 2*time.Second)
	cfg := oam.Config{
		Name: "oam", SubNetwork: "ims", MnSRoot: "http://oam:8080/3GPPManagement",
		NtfSubscriptions: store, AlarmWatches: store, Instance: name,
		AlarmEventsWait: time.Second, WatchRenewal: 100 * time.Millisecond, NotificationTimeout: 2 * time.Second,
		MissedRetryInterval: time.Second,
	}
	links := map[string]*link{}
	for _, n := range c.nodes {
		links[n.name] = newLink(t, n)
		cfg.Nodes = append(cfg.Nodes, oam.Endpoint{Name: n.name, Roles: []string{n.name}, URL: links[n.name].server.URL})
	}
	s, err := oam.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	in := &instance{name: name, server: s, stop: cancel, done: make(chan struct{}), links: links}
	go func() {
		defer close(in.done)
		if err := s.RunNotifications(ctx); err != nil {
			t.Errorf("RunNotifications of %s: %v", name, err)
		}
	}()
	t.Cleanup(func() { in.halt(t) })
	return in
}

// halt stops the instance and waits until its watchers have ended.
func (in *instance) halt(t *testing.T) {
	in.stop()
	select {
	case <-in.done:
	case <-time.After(waitFor):
		t.Errorf("%s did not stop within %v", in.name, waitFor)
	}
}

// subscribe puts the subscription oss-1 to r through the instance, and
// checks that it is answered want: 201 when it creates the
// subscription, 200 when it replaces it.
func (in *instance) subscribe(t *testing.T, r *recipient, want int) {
	t.Helper()
	h := in.server.MnSHandler()
	req := httptest.NewRequest(http.MethodPut, "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=oss-1",
		strings.NewReader(`{"id": "oss-1", "objectClass": "NtfSubscriptionControl", "attributes": {"notificationRecipientAddress": "`+r.server.URL+`/"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != want {
		t.Fatalf("PUT: %d %s, want %d", w.Code, w.Body, want)
	}
}

// unreliableScope is the unreliableAlarmScope of the alarm list that an
// OSS reads through the instance.
func (in *instance) unreliableScope(t *testing.T) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/AlarmList=1", nil)
	w := httptest.NewRecorder()
	in.server.MnSHandler().ServeHTTP(w, req)
	var list struct {
		Attributes struct {
			UnreliableAlarmScope string `json:"unreliableAlarmScope"`
		} `json:"attributes"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil {
		t.Fatalf("GET of the alarm list: %d %s", w.Code, w.Body)
	}
	return list.Attributes.UnreliableAlarmScope
}

// holders is the instance that holds the watch of each node.
func (c *cluster) holders(t *testing.T) map[string]string {
	t.Helper()
	client := kvjson.New(c.kv, c.cfg, "oam")
	holders := map[string]string{}
	for _, n := range c.nodes {
		var holder struct {
			Instance string `json:"instance"`
		}
		_, err := kvjson.Read(context.Background(), client, client.Key("watch-lease", n.name), &holder)
		if err == nil {
			holders[n.name] = holder.Instance
		}
	}
	return holders
}

// until waits until each node is held, and the instances hold as many
// as want says.
func (c *cluster) until(t *testing.T, want map[string]int) map[string]string {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for {
		holders := c.holders(t)
		count := map[string]int{}
		for _, h := range holders {
			count[h]++
		}
		ok := len(holders) == len(c.nodes)
		for name, n := range want {
			ok = ok && count[name] == n
		}
		if ok {
			return holders
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watches are held by %v within %v, want %v", holders, waitFor, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// held waits until the instances hold the watches of the nodes as want
// says, by the name of each node.
func (c *cluster) held(t *testing.T, want map[string]string) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for {
		holders := c.holders(t)
		if maps.Equal(holders, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the watches are held by %v within %v, want %v", holders, waitFor, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// watched waits until each node has a journal in its watch state, so
// that the events raised after it are sent.
func (c *cluster) watched(t *testing.T) {
	t.Helper()
	store := oamstore.New(c.kv, c.cfg)
	deadline := time.Now().Add(waitFor)
	for _, n := range c.nodes {
		for {
			st, err := store.AlarmWatchState(context.Background(), n.name)
			if err != nil {
				t.Fatal(err)
			}
			if st.JournalID != "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s is not watched within %v", n.name, waitFor)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// saved is the number of the last event of node that its watcher has
// saved it is done with.
func (c *cluster) saved(t *testing.T, node string) uint64 {
	t.Helper()
	st, err := oamstore.New(c.kv, c.cfg).AlarmWatchState(context.Background(), node)
	if err != nil {
		t.Fatal(err)
	}
	return st.LastNumber
}

// passed waits until the watcher of node has saved that it is done with
// an event after the number after: it has sent the notifications of
// the event, or passed over the recipients that are marked.
func (c *cluster) passed(t *testing.T, node string, after uint64) {
	t.Helper()
	deadline := time.Now().Add(waitFor)
	for c.saved(t, node) <= after {
		if time.Now().After(deadline) {
			t.Fatalf("the watcher of %s saved no event after %d within %v", node, after, waitFor)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// unmarked waits until no subscription is marked Missed.
func (c *cluster) unmarked(t *testing.T) {
	t.Helper()
	store := oamstore.New(c.kv, c.cfg)
	deadline := time.Now().Add(waitFor)
	for {
		subs, err := store.NtfSubscriptions(context.Background())
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

func newCluster(t *testing.T, backend storetest.Backend, nodes ...string) *cluster {
	c := &cluster{kv: backend.New(t), cfg: kvjson.Config{Prefix: storetest.Prefix()}}
	for _, name := range nodes {
		c.nodes = append(c.nodes, newNode(t, name))
	}
	return c
}

func TestTheInstancesShareTheWatches(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := newCluster(t, backend, "hss", "icscf", "scscf", "pcscf")
			first := c.start(t, "oam-1")
			c.until(t, map[string]int{"oam-1": 4})
			// A second instance gets half of the nodes, which the first
			// gives up.
			c.start(t, "oam-2")
			c.until(t, map[string]int{"oam-1": 2, "oam-2": 2})
			// When one stops, the other takes every node again.
			first.halt(t)
			c.until(t, map[string]int{"oam-2": 4})
		})
	}
}

func TestNoEventIsLostWhenAnInstanceStops(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := newCluster(t, backend, "icscf", "scscf")
			r := newRecipient(t)
			first := c.start(t, "oam-1")
			first.subscribe(t, r, http.StatusCreated)
			second := c.start(t, "oam-2")
			holders := c.until(t, map[string]int{"oam-1": 1, "oam-2": 1})
			c.watched(t)

			// The holder of scscf sends its new alarm, but stops before
			// the OSS answers, and so before it can save that it sent it.
			stopping, other := first, second
			if holders["scscf"] == "oam-2" {
				stopping, other = second, first
			}
			unanswered := make(chan struct{})
			r.setAnswer(func(body map[string]any) (int, <-chan struct{}) {
				if strings.Contains(body["href"].(string), "/ManagedElement=scscf/") {
					return http.StatusNoContent, unanswered
				}
				return http.StatusNoContent, nil
			})
			c.nodes[1].raise(t, "hss-1")
			sent := r.next(t, "the new alarm of scscf from its first holder")
			r.setAnswer(nil)
			stopping.halt(t)

			// The other instance takes the watch and sends the event
			// again, with the same notificationId, so that the OSS can
			// tell that it is the same notification.
			c.until(t, map[string]int{other.name: 2})
			again := r.next(t, "the new alarm of scscf from its next holder")
			if again["notificationId"] != sent["notificationId"] || again["alarmId"] != sent["alarmId"] || again["notificationType"] != "notifyNewAlarm" {
				t.Errorf("sent %v, then %v", sent, again)
			}
			// And it goes on with the events after it, of both nodes.
			c.nodes[1].raise(t, "hss-2")
			c.nodes[0].raise(t, "hss-1")
			got := map[string]bool{}
			for range 2 {
				n := r.next(t, "the alarms after the change of holder")
				got[n["href"].(string)] = true
			}
			for _, href := range []string{
				"http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss-2",
				"http://oam:8080/SubNetwork=ims/ManagedElement=icscf/ManagedFunction=icscf/Peer=hss-1",
			} {
				if !got[href] {
					t.Errorf("no notification of %s in %v", href, got)
				}
			}
		})
	}
}

// owedOnItsWay has the OSS r miss a notification, and then have the
// notifyAlarmListRebuilt that the miss owes it on its way: r holds its
// answer to it until release is closed, as an OSS that reads the alarm
// list before it answers. Two instances run, so that the one that sends
// the notifyAlarmListRebuilt need not be the one that watches the node;
// in is the one that the subscription was created through.
func owedOnItsWay(t *testing.T, backend storetest.Backend, r *recipient) (c *cluster, in *instance, release chan struct{}) {
	t.Helper()
	c = newCluster(t, backend, "scscf")
	in = c.start(t, "oam-1")
	in.subscribe(t, r, http.StatusCreated)
	c.start(t, "oam-2")
	c.until(t, nil)
	c.watched(t)

	release = make(chan struct{})
	var refused, held atomic.Bool
	r.setAnswer(func(body map[string]any) (int, <-chan struct{}) {
		switch body["notificationType"] {
		case "notifyNewAlarm":
			if refused.CompareAndSwap(false, true) {
				return http.StatusInternalServerError, nil
			}
		case "notifyAlarmListRebuilt":
			if held.CompareAndSwap(false, true) {
				return http.StatusNoContent, release
			}
		}
		return http.StatusNoContent, nil
	})
	before := c.saved(t, "scscf")
	c.nodes[0].raise(t, "hss-1")
	if n := r.next(t, "the alarm that the OSS refuses"); n["notificationType"] != "notifyNewAlarm" {
		t.Fatalf("got %v, want the alarm of hss-1", n)
	}
	c.passed(t, "scscf", before)
	if n := r.next(t, "the notifyAlarmListRebuilt that the miss owes"); n["notificationType"] != "notifyAlarmListRebuilt" {
		t.Fatalf("got %v, want the notifyAlarmListRebuilt that the miss owes", n)
	}
	return c, in, release
}

// TestAnAlarmRaisedWhileTheOwedNotificationIsOnItsWay: an alarm is
// raised after the OSS has read the list for the notifyAlarmListRebuilt
// that a miss owes it, and before the OSS answers it. The watcher passes
// the subscription over, as it is still marked; the alarm reaches the
// OSS all the same, by its notification or by another
// notifyAlarmListRebuilt.
func TestAnAlarmRaisedWhileTheOwedNotificationIsOnItsWay(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			r := newRecipient(t)
			c, _, release := owedOnItsWay(t, backend, r)
			before := c.saved(t, "scscf")
			c.nodes[0].raise(t, "hss-2")
			c.passed(t, "scscf", before)
			close(release)

			n := r.next(t, "the alarm of hss-2, or a notifyAlarmListRebuilt sent after it")
			switch {
			case n["notificationType"] == "notifyAlarmListRebuilt":
			case n["notificationType"] == "notifyNewAlarm" && strings.HasSuffix(n["href"].(string), "/Peer=hss-2"):
			default:
				t.Fatalf("got %v, want the alarm of hss-2 or a notifyAlarmListRebuilt", n)
			}
			// Once the OSS has taken that, the notifications go to it
			// again.
			c.unmarked(t)
			c.nodes[0].raise(t, "hss-3")
			if n := r.next(t, "the alarm after the owed notification"); n["notificationType"] != "notifyNewAlarm" || !strings.HasSuffix(n["href"].(string), "/Peer=hss-3") {
				t.Errorf("got %v, want the alarm of hss-3", n)
			}
		})
	}
}

// TestTheOwedNotificationGoesToTheNewRecipient: the OSS replaces its
// subscription with one to another address while the
// notifyAlarmListRebuilt that a miss owes is on its way to the old one.
// The answer of the old address does not clear the mark: the new one
// is sent the notifyAlarmListRebuilt too, and then the notifications.
func TestTheOwedNotificationGoesToTheNewRecipient(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			old, replacement := newRecipient(t), newRecipient(t)
			c, in, release := owedOnItsWay(t, backend, old)
			in.subscribe(t, replacement, http.StatusOK)
			close(release)

			if n := replacement.next(t, "the notifyAlarmListRebuilt that the miss owes, at the new address"); n["notificationType"] != "notifyAlarmListRebuilt" {
				t.Fatalf("got %v, want the notifyAlarmListRebuilt that the miss owes", n)
			}
			c.unmarked(t)
			c.nodes[0].raise(t, "hss-2")
			if n := replacement.next(t, "the alarm after the owed notification"); n["notificationType"] != "notifyNewAlarm" || !strings.HasSuffix(n["href"].(string), "/Peer=hss-2") {
				t.Errorf("got %v, want the alarm of hss-2", n)
			}
		})
	}
}

// TestAnInstanceThatCannotReachANodeHandsItsWatchOver: the link of the
// instance that watches a node to the node breaks, while the other
// instance still reaches it. The OSS hears that the node's part of the
// list may be faulty; then the other instance takes the watch, and the
// OSS hears that the part can be trusted again. The instance that
// cannot reach the node takes the watch of the other node in its
// place, and the OSS is sent the alarms of both nodes again.
func TestAnInstanceThatCannotReachANodeHandsItsWatchOver(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := newCluster(t, backend, "icscf", "scscf")
			r := newRecipient(t)
			first := c.start(t, "oam-1")
			first.subscribe(t, r, http.StatusCreated)
			second := c.start(t, "oam-2")
			holders := c.until(t, map[string]int{"oam-1": 1, "oam-2": 1})
			c.watched(t)
			cut, other := first, second
			if holders["scscf"] == "oam-2" {
				cut, other = second, first
			}

			cut.links["scscf"].broken.Store(true)
			if n := r.next(t, "notifyPotentialFaultyAlarmList"); n["notificationType"] != "notifyPotentialFaultyAlarmList" ||
				!strings.HasSuffix(n["href"].(string), "/ManagedElement=scscf") {
				t.Fatalf("got %v, want notifyPotentialFaultyAlarmList about scscf", n)
			}
			if n := r.next(t, "notifyAlarmListRebuilt from the instance that reaches scscf"); n["notificationType"] != "notifyAlarmListRebuilt" ||
				n["reason"] != "System-NE communication error" || !strings.HasSuffix(n["href"].(string), "/ManagedElement=scscf") {
				t.Fatalf("got %v, want notifyAlarmListRebuilt about scscf", n)
			}
			c.held(t, map[string]string{"scscf": other.name, "icscf": cut.name})
			deadline := time.Now().Add(waitFor)
			for scope := other.unreliableScope(t); scope != ""; scope = other.unreliableScope(t) {
				if time.Now().After(deadline) {
					t.Fatalf("the alarm list is still unreliable at %q %v after the watch was handed over", scope, waitFor)
				}
				time.Sleep(10 * time.Millisecond)
			}

			c.nodes[0].raise(t, "hss-1")
			c.nodes[1].raise(t, "hss-1")
			got := map[string]bool{}
			for range 2 {
				n := r.next(t, "the alarms after the watch was handed over")
				got[n["href"].(string)] = true
			}
			for _, href := range []string{
				"http://oam:8080/SubNetwork=ims/ManagedElement=icscf/ManagedFunction=icscf/Peer=hss-1",
				"http://oam:8080/SubNetwork=ims/ManagedElement=scscf/ManagedFunction=scscf/Peer=hss-1",
			} {
				if !got[href] {
					t.Errorf("no notification of %s in %v", href, got)
				}
			}
		})
	}
}

// TestTheWatchOfANodeThatNoInstanceReachesStays: no instance reaches the
// node. Each may try it, and then the last one to take the watch keeps
// it, rather than hand it round the instances while the node is away.
func TestTheWatchOfANodeThatNoInstanceReachesStays(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := newCluster(t, backend, "scscf")
			first := c.start(t, "oam-1")
			second := c.start(t, "oam-2")
			c.until(t, nil)
			c.watched(t)
			first.links["scscf"].broken.Store(true)
			second.links["scscf"].broken.Store(true)

			// A watch that went round would raise the generation at each
			// take. The node misses three requests, a second apart, before
			// its watcher hands it over, so the generation stays for twice
			// that once the watch has stopped moving.
			store := oamstore.New(c.kv, c.cfg)
			generation := func() uint64 {
				st, err := store.AlarmWatchState(context.Background(), "scscf")
				if err != nil {
					t.Fatal(err)
				}
				return st.Generation
			}
			// Both instances find that they cannot reach the node within a
			// few seconds; the deadline leaves twice waitFor for it, for a
			// loaded machine.
			const still = 6 * time.Second
			deadline := time.Now().Add(2*waitFor + still)
			for last, since := generation(), time.Now(); time.Since(since) < still; {
				if time.Now().After(deadline) {
					t.Fatalf("the generation of the watch still moved after %v, up to %d", 2*waitFor, last)
				}
				time.Sleep(100 * time.Millisecond)
				if g := generation(); g != last {
					last, since = g, time.Now()
				}
			}
			if holders := c.holders(t); holders["scscf"] == "" {
				t.Errorf("no instance holds the watch of scscf: %v", holders)
			}
		})
	}
}

// TestAWatcherWhoseSaveLostItsAnswerGoesOn: the save of the state after
// a notification reaches the store, but its answer does not reach the
// instance. The watcher saves again, finds the state its own, and goes
// on: the watch is not taken anew, and the OSS gets the next alarm next.
func TestAWatcherWhoseSaveLostItsAnswerGoesOn(t *testing.T) {
	for _, backend := range storetest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := newCluster(t, backend, "scscf")
			lossy := &losingAnswers{Store: c.kv}
			r := newRecipient(t)
			in := c.startOn(t, "oam-1", lossy)
			in.subscribe(t, r, http.StatusCreated)
			c.until(t, map[string]int{"oam-1": 1})
			c.watched(t)
			store := oamstore.New(c.kv, c.cfg)
			before, err := store.AlarmWatchState(context.Background(), "scscf")
			if err != nil {
				t.Fatal(err)
			}

			lossy.loseNext("watch-state")
			c.nodes[0].raise(t, "hss-1")
			if n := r.next(t, "the alarm of hss-1"); !strings.HasSuffix(n["href"].(string), "/Peer=hss-1") {
				t.Fatalf("got %v, want the alarm of hss-1", n)
			}
			c.passed(t, "scscf", before.LastNumber)
			c.nodes[0].raise(t, "hss-2")
			if n := r.next(t, "the alarm of hss-2"); !strings.HasSuffix(n["href"].(string), "/Peer=hss-2") {
				t.Fatalf("got %v, want the alarm of hss-2", n)
			}
			c.passed(t, "scscf", before.LastNumber+1)
			if after, err := store.AlarmWatchState(context.Background(), "scscf"); err != nil || after.Generation != before.Generation {
				t.Errorf("the generation went from %d to %d (%v): the watch was taken anew", before.Generation, after.Generation, err)
			}
		})
	}
}
