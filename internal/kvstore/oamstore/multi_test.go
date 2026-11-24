package oamstore_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
// answers each with what hold says: at once, or only when the request
// ends.
type recipient struct {
	server   *httptest.Server
	received chan map[string]any

	mu   sync.Mutex
	hold func(body map[string]any) bool
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
		hold := r.hold
		r.mu.Unlock()
		if hold != nil && hold(body) {
			<-req.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *recipient) setHold(hold func(map[string]any) bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hold = hold
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

// instance is one OAM instance on the shared store.
type instance struct {
	name   string
	server *oam.Server
	stop   context.CancelFunc
	done   chan struct{}
}

// cluster is the OAM instances on one store, and the nodes they watch.
type cluster struct {
	kv    kvstore.Store
	cfg   kvjson.Config
	nodes []*node
}

func (c *cluster) start(t *testing.T, name string) *instance {
	t.Helper()
	// The instances renew every 100 ms a lease of 2 s, so that a loaded
	// machine does not let a watch run out by itself.
	store := oamstore.NewWithLease(c.kv, c.cfg, 2*time.Second)
	cfg := oam.Config{
		SubNetwork: "ims", MnSRoot: "http://oam:8080/3GPPManagement",
		NtfSubscriptions: store, AlarmWatches: store, Instance: name,
		AlarmEventsWait: time.Second, WatchRenewal: 100 * time.Millisecond, NotificationTimeout: 2 * time.Second,
	}
	for _, n := range c.nodes {
		cfg.Nodes = append(cfg.Nodes, oam.Endpoint{Name: n.name, Roles: []string{n.name}, URL: n.server.URL})
	}
	s, err := oam.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	in := &instance{name: name, server: s, stop: cancel, done: make(chan struct{})}
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

// subscribe creates a subscription through the instance.
func (in *instance) subscribe(t *testing.T, r *recipient) {
	t.Helper()
	h := in.server.MnSHandler()
	req := httptest.NewRequest(http.MethodPut, "/3GPPManagement/ProvMnS/v19/SubNetwork=ims/NtfSubscriptionControl=oss-1",
		strings.NewReader(`{"id": "oss-1", "objectClass": "NtfSubscriptionControl", "attributes": {"notificationRecipientAddress": "`+r.server.URL+`/"}}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("PUT: %d %s", w.Code, w.Body)
	}
}

// holders is the instance that holds the watch of each node.
func (c *cluster) holders(t *testing.T) map[string]string {
	t.Helper()
	client := kvjson.New(c.kv, c.cfg, "oam")
	holders := map[string]string{}
	for _, n := range c.nodes {
		var holder string
		_, err := kvjson.Read(context.Background(), client, client.Key("watch-lease", n.name), &holder)
		if err == nil {
			holders[n.name] = holder
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
			first.subscribe(t, r)
			second := c.start(t, "oam-2")
			holders := c.until(t, map[string]int{"oam-1": 1, "oam-2": 1})
			c.watched(t)

			// The holder of scscf sends its new alarm, but stops before
			// the OSS answers, and so before it can save that it sent it.
			stopping, other := first, second
			if holders["scscf"] == "oam-2" {
				stopping, other = second, first
			}
			r.setHold(func(body map[string]any) bool {
				return strings.Contains(body["href"].(string), "/ManagedElement=scscf/")
			})
			c.nodes[1].raise(t, "hss-1")
			sent := r.next(t, "the new alarm of scscf from its first holder")
			r.setHold(nil)
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
