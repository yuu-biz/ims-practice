package agw

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

// This file tries a media node while the operator has it out of service
// through the maintenance API of its node.

// mediaRole is the name of the media relay in the configuration file,
// which is the path segment of its maintenance API. A node with the
// mrfp role gives its relay that name instead (cmd/ims).
const mediaRole = "agw"

// managed gives r the maintenance API of a node, as the node of a media
// relay does, and returns the node.
func managed(t *testing.T, r *Relay) *maintenance.Node {
	t.Helper()
	node, err := maintenance.New(maintenance.Config{
		Node:          mediaRole,
		LogLevel:      new(slog.LevelVar),
		Functions:     []maintenance.Function{r.Function(mediaRole)},
		Configuration: func() any { return map[string]string{"node": mediaRole} },
	})
	if err != nil {
		t.Fatal(err)
	}
	r.Manage(node.ManagedFunction(mediaRole))
	return node
}

// relayState is the state of the relay in the maintenance API of node,
// with the count of its users, which the role must give.
func relayState(t *testing.T, node *maintenance.Node) (maintenance.FunctionState, int) {
	t.Helper()
	state, err := node.State()
	if err != nil {
		t.Fatal(err)
	}
	f := state.Functions[0]
	if f.Users == nil {
		t.Fatalf("the relay is %+v, want a count of its users", f)
	}
	return f, *f.Users
}

// setState makes the event that takes the relay to want, and returns
// its state after it.
func setState(t *testing.T, node *maintenance.Node, want maintenance.AdministrativeState) maintenance.FunctionState {
	t.Helper()
	f, err := node.SetAdministrativeState(mediaRole, maintenance.AdministrativeStateRequest{AdministrativeState: want, Operator: "test"})
	if err != nil {
		t.Fatalf("%s: %v", want, err)
	}
	return f
}

// TestAShutDownRelayMakesNoContext: a relay with no context is LOCKED
// as soon as the operator shuts it down, as a shut down leaves a
// function with no user (X.731 7.1.3.3), and it then gives no context.
// After the unlock it takes them again.
func TestAShutDownRelayMakesNoContext(t *testing.T) {
	r := newRelay(t, 4)
	node := managed(t, r)

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.Locked {
		t.Fatalf("the shut down left the relay %s, want LOCKED with no context", f.AdministrativeState)
	}
	if _, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core}); !errors.Is(err, ErrOutOfService) {
		t.Errorf("Add while locked = %v, want ErrOutOfService", err)
	}
	if cs := r.Contexts(); len(cs) != 0 {
		t.Errorf("the relay holds %v, want no context", cs)
	}

	setState(t, node, maintenance.Unlocked)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatalf("Add after the unlock: %v", err)
	}
	if _, users := relayState(t, node); users != 1 {
		t.Errorf("the relay counts %d users, want the one context it holds", users)
	}
	r.Subtract(c.ID)
}

// TestTheDrainWaitsForTheContextsOfTheRelay: a relay that holds a
// context stays SHUTTINGDOWN, goes on serving that context, and becomes
// LOCKED when the controller subtracts it.
func TestTheDrainWaitsForTheContextsOfTheRelay(t *testing.T) {
	r := newRelay(t, 8)
	node := managed(t, r)
	call, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	announcement, err := r.Add(TerminationRequest{ID: "t1"})
	if err != nil {
		t.Fatal(err)
	}

	f := setState(t, node, maintenance.ShuttingDown)
	if f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("the shut down left the relay %s, want SHUTTINGDOWN with two contexts", f.AdministrativeState)
	}
	if _, users := relayState(t, node); users != 2 {
		t.Errorf("the relay counts %d users, want its two contexts", users)
	}

	// The contexts it has go on: the controller points a termination at
	// the other party, and a party joins the context of a conference.
	if _, err := r.Modify(call.ID, Access, TerminationRequest{Mode: SendReceive, Remote: addr(udp(t))}); err != nil {
		t.Errorf("Modify of a context of a relay that is shutting down: %v", err)
	}
	if _, err := r.AddTermination(announcement.ID, TerminationRequest{ID: "t2"}); err != nil {
		t.Errorf("AddTermination to a context of a relay that is shutting down: %v", err)
	}
	if _, err := r.Add(TerminationRequest{ID: "t1"}); !errors.Is(err, ErrOutOfService) {
		t.Errorf("Add while shutting down = %v, want ErrOutOfService", err)
	}

	// The first context of the two leaves the relay shutting down, and
	// the last one locks it.
	if _, err := r.Subtract(call.ID); err != nil {
		t.Fatal(err)
	}
	f, users := relayState(t, node)
	if f.AdministrativeState != maintenance.ShuttingDown || users != 1 {
		t.Errorf("the relay is %s with %d users, want SHUTTINGDOWN with the context that is left", f.AdministrativeState, users)
	}
	if _, _, err := r.SubtractTermination(announcement.ID, "t1"); err != nil {
		t.Fatal(err)
	}
	f, users = relayState(t, node)
	if f.AdministrativeState != maintenance.ShuttingDown || users != 1 {
		t.Errorf("the relay is %s with %d users after one termination of two, want SHUTTINGDOWN with its context",
			f.AdministrativeState, users)
	}
	if _, deleted, err := r.SubtractTermination(announcement.ID, "t2"); err != nil || !deleted {
		t.Fatalf("the last termination left the context: deleted=%v, %v", deleted, err)
	}
	f, users = relayState(t, node)
	if f.AdministrativeState != maintenance.Locked || users != 0 {
		t.Errorf("the relay is %s with %d users, want LOCKED once its last context is subtracted", f.AdministrativeState, users)
	}
}

// TestTheControlAPIRefusesANewContextWithServiceUnavailable: over the
// control API, a media node out of service answers the Add of a new
// context with 503 and the error code out_of_service, and its client
// gives its caller ErrOutOfService, so that a controller takes the next
// media node of its list. Everything else it serves.
func TestTheControlAPIRefusesANewContextWithServiceUnavailable(t *testing.T) {
	r := newRelay(t, 4)
	node := managed(t, r)
	api := httptest.NewServer(checkedHandler(t, r))
	defer api.Close()
	client := NewClient(api.URL)
	ctx := context.Background()

	c, err := client.Add(ctx, TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	setState(t, node, maintenance.ShuttingDown)

	if _, err := client.Add(ctx, TerminationRequest{ID: "t1"}); !errors.Is(err, ErrOutOfService) {
		t.Errorf("Add over the control API = %v, want ErrOutOfService", err)
	}
	resp, err := http.Post(api.URL+"/contexts", "application/json", strings.NewReader(`{"terminations":[{"id":"t1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e apiError
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable || e.Error != "out_of_service" {
		t.Errorf("POST /contexts = %s with %+v, want 503 and out_of_service", resp.Status, e)
	}

	if err := client.Modify(ctx, c.ID, Access, TerminationRequest{Mode: Inactive}); err != nil {
		t.Errorf("Modify over the control API: %v", err)
	}
	if len(r.Contexts()) != 1 {
		t.Errorf("the relay holds %v, want the one context it had before the shut down", r.Contexts())
	}
	if err := client.Subtract(ctx, c.ID); err != nil {
		t.Errorf("Subtract over the control API: %v", err)
	}
	if f, users := relayState(t, node); f.AdministrativeState != maintenance.Locked || users != 0 {
		t.Errorf("the relay is %s with %d users, want LOCKED once the controller has subtracted its context",
			f.AdministrativeState, users)
	}
}

// TestTheContextsOfAnEarlierProcessCountAsUsers: the contexts that a
// relay opens again from its store when it starts are its users too, so
// a shut down of the node that was created again waits for the calls
// whose media it carries.
func TestTheContextsOfAnEarlierProcessCountAsUsers(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 4)
	first := relayOn(t, minPort, maxPort, store)
	if _, err := first.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	again := relayOn(t, minPort, maxPort, store)
	node := managed(t, again)
	if _, users := relayState(t, node); users != 1 {
		t.Fatalf("the relay counts %d users, want the context it opened again", users)
	}
	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Errorf("the shut down left the relay %s, want SHUTTINGDOWN with the context of the earlier process",
			f.AdministrativeState)
	}
	for _, c := range again.Contexts() {
		again.Subtract(c.ID)
	}
	if f, users := relayState(t, node); f.AdministrativeState != maintenance.Locked || users != 0 {
		t.Errorf("the relay is %s with %d users, want LOCKED once that context is subtracted", f.AdministrativeState, users)
	}
}

// slowStore is a store whose next write waits until the test lets it
// land, as a write to a store on another node waits for its answer.
type slowStore struct {
	*MemoryContextStore
	writing chan struct{}
	land    chan struct{}
}

func (s *slowStore) PutContext(ctx context.Context, c SavedContext) error {
	s.writing <- struct{}{}
	<-s.land
	return s.MemoryContextStore.PutContext(ctx, c)
}

// TestAShutDownCountsTheAddThatIsWritingItsContext: a shut down that
// comes while an Add is writing its context to the store finds the
// relay with a user, so the relay stays SHUTTINGDOWN, and is LOCKED
// only once the controller subtracts that context. A failed Add leaves
// no user behind.
func TestAShutDownCountsTheAddThatIsWritingItsContext(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 4)
	store := &slowStore{MemoryContextStore: NewMemoryContextStore(), writing: make(chan struct{}), land: make(chan struct{})}
	r := relayOn(t, minPort, maxPort, store)
	node := managed(t, r)

	type added struct {
		c   Context
		err error
	}
	done := make(chan added, 1)
	go func() {
		c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
		done <- added{c, err}
	}()
	select {
	case <-store.writing:
	case <-time.After(5 * time.Second):
		t.Fatal("the Add wrote no context to the store")
	}

	if f := setState(t, node, maintenance.ShuttingDown); f.AdministrativeState != maintenance.ShuttingDown {
		t.Fatalf("a shut down during the Add left the relay %s, want SHUTTINGDOWN", f.AdministrativeState)
	}
	close(store.land)
	var got added
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the Add did not return once its write landed")
	}
	if got.err != nil {
		t.Fatalf("the Add that began before the shut down: %v", got.err)
	}
	if f, users := relayState(t, node); f.AdministrativeState != maintenance.ShuttingDown || users != 1 {
		t.Errorf("the relay is %s with %d users, want SHUTTINGDOWN with the context of that Add", f.AdministrativeState, users)
	}
	if _, err := r.Subtract(got.c.ID); err != nil {
		t.Fatal(err)
	}
	if f, users := relayState(t, node); f.AdministrativeState != maintenance.Locked || users != 0 {
		t.Errorf("the relay is %s with %d users, want LOCKED once that context is subtracted", f.AdministrativeState, users)
	}

	setState(t, node, maintenance.Unlocked)
	if _, err := r.Add(); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("Add of no termination = %v, want ErrBadRequest", err)
	}
	if _, users := relayState(t, node); users != 0 {
		t.Errorf("a failed Add left the relay with %d users, want none", users)
	}
}
