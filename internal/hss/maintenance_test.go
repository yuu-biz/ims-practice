package hss

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/fiorix/go-diameter/v4/diam"
	"github.com/fiorix/go-diameter/v4/diam/avp"
	"github.com/fiorix/go-diameter/v4/diam/datatype"

	"github.com/yuu-biz/ims-practice/internal/cx"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/metrics"
)

// This file runs two instances of the HSS on their own Diameter nodes,
// as a network with two HSS nodes does, and takes one of them out of
// service through the maintenance API while a client sends Cx requests
// to the two of them as one group.

// drainTimers are far below the floor of RFC 3539 3.4.1 [1], which only
// the configuration file enforces, so that the test sees a reconnect
// after Tc without waiting 30 seconds for it.
var drainTimers = diameter.Timers{
	Watchdog:  2 * time.Second,
	Reconnect: 100 * time.Millisecond,
	Answer:    2 * time.Second,
}

// instance is one HSS with the Diameter node it answers on and the
// maintenance API of that node in front of it, wired as cmd/ims wires
// them.
type instance struct {
	hss    *HSS
	server *diameter.Server
	api    *maintenance.Node
}

func newInstance(t *testing.T, host string) *instance {
	t.Helper()
	memory, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"})
	if err != nil {
		t.Fatal(err)
	}
	return newInstanceOn(t, host, memory)
}

func newInstanceOn(t *testing.T, host string, store Store) *instance {
	t.Helper()
	node := diameter.NewNode(diameter.Identity{OriginHost: host, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx}, drainTimers, quiet())
	h := mustHSS(Config{
		Realm:              realm,
		VisitedNetworks:    []string{realm},
		ServerCapabilities: cx.ServerCapabilities{MandatoryCapabilities: []uint32{1}},
	}, store, node)
	node.Handle(h.Handle)
	server, err := node.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Stop)
	api, err := maintenance.New(maintenance.Config{
		Node:          "hss",
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{h.Function(server)},
		Configuration: func() any { return struct{}{} },
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Manage(api.ManagedFunction(role))
	return &instance{hss: h, server: server, api: api}
}

// set makes the event that leads to state and returns the state of the
// HSS after it.
func (i *instance) set(t *testing.T, state maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := i.api.SetAdministrativeState(role, maintenance.AdministrativeStateRequest{AdministrativeState: state, Operator: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// until waits up to five seconds for ok, and fails with what the test
// was waiting for.
func until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waited in vain for %s", what)
}

// answeringHost is the Origin-Host of the instance that answered,
// which is how the test tells the two apart.
func answeringHost(t *testing.T, answer *diam.Message) string {
	t.Helper()
	a, err := answer.FindAVP(avp.OriginHost, 0)
	if err != nil {
		t.Fatalf("the answer carries no Origin-Host: %v", err)
	}
	return string(a.Data.(datatype.DiameterIdentity))
}

// userAuthorization is a UAR as the I-CSCF sends it: it names a realm
// and no host, so any instance of the HSS may answer it (TS 29.228
// 6.1.1).
func userAuthorization() *diam.Message {
	return cx.UserAuthorizationRequest{Header: hdr, UserName: impi, PublicIdentity: impu,
		VisitedNetworkIdentifier: realm, UserAuthorizationType: cx.Registration}.Message()
}

// TestPeerGroupMovesToTheSecondHSSWhenTheFirstIsDrained: the operator
// shuts one instance down, it sends its peers a DPR and closes, and the
// client's PeerGroup sends the next request to the other instance
// without the caller noticing. An unlock brings the first one back, and
// the group uses it again.
func TestPeerGroupMovesToTheSecondHSSWhenTheFirstIsDrained(t *testing.T) {
	first, second := "hss1."+realm, "hss2."+realm
	one, two := newInstance(t, first), newInstance(t, second)

	client := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx}, drainTimers, quiet())
	peerOne := client.Connect(diameter.PeerConfig{Address: one.server.Addr(), OriginHost: first})
	defer peerOne.Stop()
	peerTwo := client.Connect(diameter.PeerConfig{Address: two.server.Addr(), OriginHost: second})
	defer peerTwo.Stop()
	group, err := diameter.NewPeerGroup(metrics.NewRegistry(), "icscf", peerOne, peerTwo)
	if err != nil {
		t.Fatal(err)
	}
	until(t, "both connections to open", func() bool { return peerOne.Available() && peerTwo.Available() })

	ctx := context.Background()
	answer, err := group.Request(ctx, userAuthorization())
	if err != nil {
		t.Fatal(err)
	}
	if host := answeringHost(t, answer); host != first {
		t.Fatalf("the group asked %s, want the first instance %s", host, first)
	}

	// No request is in flight, so the shut down locks the instance at
	// once and the DPRs have gone out by the time it answers.
	if state := one.set(t, maintenance.ShuttingDown); state.AdministrativeState != maintenance.Locked {
		t.Fatalf("after the shut down the instance is %s, want LOCKED", state.AdministrativeState)
	}
	if peerOne.Available() {
		t.Error("the connection to the drained instance still takes requests")
	}
	if !peerTwo.Available() {
		t.Error("the drain of one instance closed the connection to the other")
	}

	answer, err = group.Request(ctx, userAuthorization())
	if err != nil {
		t.Fatal(err)
	}
	if host := answeringHost(t, answer); host != second {
		t.Fatalf("the group asked %s, want the second instance %s", host, second)
	}

	// REBOOTING invites the peer back (RFC 6733 5.4.3) and it tries
	// every Tc, but a locked instance holds no listening socket, so
	// several Tc later it is still out.
	time.Sleep(5 * drainTimers.Reconnect)
	if peerOne.Available() {
		t.Error("the locked instance took the peer back before the unlock")
	}

	// The unlock has the node listen again, and the peer gets in at its
	// next attempt.
	one.set(t, maintenance.Unlocked)
	until(t, "the connection to the unlocked instance to open again", peerOne.Available)
	answer, err = group.Request(ctx, userAuthorization())
	if err != nil {
		t.Fatal(err)
	}
	if host := answeringHost(t, answer); host != first {
		t.Fatalf("after the unlock the group asked %s, want the first instance %s", host, first)
	}
}

// slowStore holds the first read of a subscriber until the test lets
// it go, so that one Cx request is inside the HSS when the operator
// shuts it down.
type slowStore struct {
	Store
	once   sync.Once
	inside chan struct{}
	hold   chan struct{}
}

func (s *slowStore) SubscriberOf(ctx context.Context, publicID string) (Subscriber, bool, error) {
	s.once.Do(func() {
		close(s.inside)
		<-s.hold
	})
	return s.Store.SubscriberOf(ctx, publicID)
}

// TestShutDownWaitsForTheRequestsItTook: the HSS answers a request it
// took before the shut down, and only then does its connection close.
// RFC 6733 5.4 has the DPR say that the node is about to disconnect,
// not that it drops what it is holding.
func TestShutDownWaitsForTheRequestsItTook(t *testing.T) {
	memory, err := NewMemoryStore(Subscription{PrivateIdentity: impi, PublicIdentities: []string{impu}, Password: "test-password-1"})
	if err != nil {
		t.Fatal(err)
	}
	store := &slowStore{Store: memory, inside: make(chan struct{}), hold: make(chan struct{})}
	one := newInstanceOn(t, "hss1."+realm, store)

	client := diameter.NewNode(diameter.Identity{OriginHost: "icscf." + realm, OriginRealm: realm},
		[]uint32{diameter.ApplicationIDCx}, drainTimers, quiet())
	peer := client.Connect(diameter.PeerConfig{Address: one.server.Addr(), OriginHost: "hss1." + realm})
	defer peer.Stop()
	until(t, "the connection to open", peer.Available)

	answered := make(chan *diam.Message, 1)
	go func() {
		answer, err := peer.Request(context.Background(), userAuthorization())
		if err != nil {
			answered <- nil
			return
		}
		answered <- answer
	}()
	<-store.inside

	state := one.set(t, maintenance.ShuttingDown)
	if state.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("with a request in flight the instance is %s, want SHUTTINGDOWN", state.AdministrativeState)
	}
	if state.Users == nil || *state.Users != 1 {
		t.Fatalf("users = %v, want the one request in flight", state.Users)
	}

	close(store.hold)
	answer := <-answered
	if answer == nil {
		t.Fatal("the request the HSS took before the shut down got no answer")
	}
	if _, err := cx.ParseUserAuthorizationAnswer(answer); err != nil {
		t.Fatalf("the answer is not a UAA: %v", err)
	}
	until(t, "the instance to become LOCKED once its last request was answered", func() bool {
		return one.api.State().Functions[0].AdministrativeState == maintenance.Locked
	})
	until(t, "the drained instance to close its connection", func() bool { return !peer.Available() })
}
