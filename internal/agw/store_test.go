package agw

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/rtptest"
)

func TestMemoryContextStore(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryContextStore()
	call := SavedContext{ID: "c2", Terminations: []SavedTermination{
		{ID: Access, LocalRTP: 20000, Remote: netip.MustParseAddrPort("192.0.2.10:49170"), Mode: SendReceive},
		{ID: Core, LocalRTP: 20002, Mode: SendReceive},
	}}
	conference := SavedContext{ID: "c1", Terminations: []SavedTermination{{ID: "t1", LocalRTP: 20004, Mode: ReceiveOnly}}}
	for _, c := range []SavedContext{call, conference} {
		if err := s.PutContext(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	// The store keeps its own copy: a later change by the relay goes
	// in only with the next PutContext.
	call.Terminations[1].Remote = netip.MustParseAddrPort("198.51.100.20:30000")

	got, err := s.Contexts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []SavedContext{conference, {ID: "c2", Terminations: []SavedTermination{
		{ID: Access, LocalRTP: 20000, Remote: netip.MustParseAddrPort("192.0.2.10:49170"), Mode: SendReceive},
		{ID: Core, LocalRTP: 20002, Mode: SendReceive},
	}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Contexts = %+v, want %+v", got, want)
	}

	if err := s.PutContext(ctx, call); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteContext(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteContext(ctx, "gone"); err != nil {
		t.Fatalf("DeleteContext of a context that is not there: %v", err)
	}
	got, _ = s.Contexts(ctx)
	if !reflect.DeepEqual(got, []SavedContext{call}) {
		t.Fatalf("Contexts after a replace and a delete = %+v, want %+v", got, []SavedContext{call})
	}
}

// relayOn is a relay on store with the ports of [minPort, maxPort], so
// that a test can create one again on the same ports and store, as a
// relay recreated under the same name is.
func relayOn(t *testing.T, minPort, maxPort uint16, store ContextStore) *Relay {
	t.Helper()
	r, err := NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, store, quiet())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	return r
}

func contextsOf(t *testing.T, s ContextStore) []SavedContext {
	t.Helper()
	saved, err := s.Contexts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

// brokenStore is a ContextStore that cannot be written, as the adapter
// of a relay is while the shared store cannot be reached. The fields
// are read by the retries of the relay, in a goroutine of their own.
type brokenStore struct {
	*MemoryContextStore
	// fail makes every PutContext fail while it is set, and deletes is
	// how many DeleteContexts still fail: it counts down, so that a test
	// can let a later attempt through.
	fail    atomic.Bool
	deletes atomic.Int32
}

var errBroken = errors.New("the store cannot be reached")

func (s *brokenStore) PutContext(ctx context.Context, c SavedContext) error {
	if s.fail.Load() {
		return errBroken
	}
	return s.MemoryContextStore.PutContext(ctx, c)
}

func (s *brokenStore) DeleteContext(ctx context.Context, id string) error {
	if s.deletes.Add(-1) >= 0 {
		return errBroken
	}
	return s.MemoryContextStore.DeleteContext(ctx, id)
}

// lateStore is a ContextStore whose write lands and is then reported as
// a failure, as the adapter of a relay does when the store answers
// after the timeout of the operation has run out.
type lateStore struct {
	*MemoryContextStore
	late atomic.Bool
}

func (s *lateStore) PutContext(ctx context.Context, c SavedContext) error {
	if err := s.MemoryContextStore.PutContext(ctx, c); err != nil {
		return err
	}
	if s.late.Load() {
		return errBroken
	}
	return nil
}

// blockingStore is a ContextStore whose write waits, as the adapter of
// a relay does while the store takes its time. It reports the context
// of each write that has begun on entered, and lets it finish when
// release is closed.
type blockingStore struct {
	*MemoryContextStore
	block   atomic.Bool
	entered chan string
	release chan struct{}
}

func (s *blockingStore) PutContext(ctx context.Context, c SavedContext) error {
	if s.block.Load() {
		s.entered <- c.ID
		<-s.release
	}
	return s.MemoryContextStore.PutContext(ctx, c)
}

// TestTheRelayStoresEveryContextItChanges follows one context of a call
// that becomes a conference: every operation of the control API leaves
// the store holding the ports, the addresses and the modes that the
// relay has.
func TestTheRelayStoresEveryContextItChanges(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)

	ue := netip.MustParseAddrPort("192.0.2.10:49170")
	c, err := r.Add(TerminationRequest{ID: Access, Remote: ue}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	want := []SavedContext{{ID: c.ID, Terminations: []SavedTermination{
		{ID: Access, LocalRTP: c.Terminations[Access].Local.Port(), Remote: ue, Mode: SendReceive},
		{ID: Core, LocalRTP: c.Terminations[Core].Local.Port(), Mode: SendReceive},
	}}}
	if got := contextsOf(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Add the store holds %+v, want %+v", got, want)
	}

	// The core termination learns where the far end receives.
	far := netip.MustParseAddrPort("198.51.100.20:30000")
	if _, err := r.Modify(c.ID, Core, TerminationRequest{Remote: far}); err != nil {
		t.Fatal(err)
	}
	want[0].Terminations[1].Remote = far
	if got := contextsOf(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Modify the store holds %+v, want %+v", got, want)
	}

	// A third party joins, and the context becomes a conference.
	third, err := r.AddTermination(c.ID, TerminationRequest{Mode: ReceiveOnly})
	if err != nil {
		t.Fatal(err)
	}
	want[0].Terminations = append(want[0].Terminations,
		SavedTermination{ID: third.ID, LocalRTP: third.Local.Port(), Mode: ReceiveOnly})
	if got := contextsOf(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after AddTermination the store holds %+v, want %+v", got, want)
	}

	if _, _, err := r.SubtractTermination(c.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	want[0].Terminations = want[0].Terminations[:2]
	if got := contextsOf(t, store); !reflect.DeepEqual(got, want) {
		t.Fatalf("after SubtractTermination the store holds %+v, want %+v", got, want)
	}

	if _, err := r.Subtract(c.ID); err != nil {
		t.Fatal(err)
	}
	if got := contextsOf(t, store); len(got) != 0 {
		t.Fatalf("after Subtract the store holds %+v, want nothing", got)
	}
}

// TestARecreatedRelayOpensThePortsOfItsContexts: the process of a relay
// that carries a call stops, and the relay is created again under the
// same name, on the same ports and the same store. It opens the ports of
// the context again, with the addresses and the modes of its
// terminations, and the media of the call goes on: neither party and
// neither controller has heard of the restart.
func TestARecreatedRelayOpensThePortsOfItsContexts(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 8)
	first := relayOn(t, minPort, maxPort, store)

	ue, network := udp(t), udp(t)
	c, err := first.Add(
		// The UE's SDP names an address it does not send from, as one
		// behind NAT does; the relay latches onto its first packet.
		TerminationRequest{ID: Access, Remote: netip.MustParseAddrPort("192.0.2.10:4000")},
		TerminationRequest{ID: Core, Remote: addr(network)})
	if err != nil {
		t.Fatal(err)
	}
	access, core := c.Terminations[Access].Local, c.Terminations[Core].Local
	ue.WriteToUDPAddrPort([]byte("before the restart"), access)
	if got, _ := read(t, network); !bytes.Equal(got, []byte("before the restart")) {
		t.Fatalf("the network side got %q before the restart", got)
	}
	// The latched address of the UE reaches the store with the next
	// change of the context, as the P-CSCF's Modify for the answer is.
	if _, err := first.Modify(c.ID, Core, TerminationRequest{Remote: addr(network)}); err != nil {
		t.Fatal(err)
	}

	// The process stops, with the context left in the store.
	first.Close()
	saved := contextsOf(t, store)
	want := []SavedContext{{ID: c.ID, Terminations: []SavedTermination{
		{ID: Access, LocalRTP: access.Port(), Remote: addr(ue), Mode: SendReceive},
		{ID: Core, LocalRTP: core.Port(), Remote: addr(network), Mode: SendReceive},
	}}}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("the store holds %+v, want %+v", saved, want)
	}

	again := relayOn(t, minPort, maxPort, store)
	contexts := again.Contexts()
	if len(contexts) != 1 || contexts[0].ID != c.ID || len(contexts[0].Terminations) != 2 {
		t.Fatalf("the relay created again holds %+v, want the context %s with its two terminations", contexts, c.ID)
	}
	for id, local := range map[TerminationID]netip.AddrPort{Access: access, Core: core} {
		if got := contexts[0].Terminations[id]; got.Local != local || got.Mode != SendReceive {
			t.Errorf("termination %s is %+v, want it on %v in mode %s", id, got, local, SendReceive)
		}
	}

	// The media goes on, both ways, without any word from a controller.
	network.WriteToUDPAddrPort([]byte("after the restart"), core)
	if got, src := read(t, ue); !bytes.Equal(got, []byte("after the restart")) || src != access {
		t.Errorf("the UE got %q from %v, want it from the access termination %v", got, src, access)
	}
	ue.WriteToUDPAddrPort([]byte("and back"), access)
	if got, src := read(t, network); !bytes.Equal(got, []byte("and back")) || src != core {
		t.Errorf("the network side got %q from %v, want it from the core termination %v", got, src, core)
	}

	// The controller, which heard nothing of the restart, subtracts the
	// context it knows, and that empties the store.
	if _, err := again.Subtract(c.ID); err != nil {
		t.Fatalf("the controller could not subtract the context after the restart: %v", err)
	}
	if got := contextsOf(t, store); len(got) != 0 {
		t.Errorf("the store holds %+v after the Subtract, want nothing", got)
	}
}

// TestARecreatedRelayGivesUpAContextWhosePortsAreTaken: a relay that
// cannot open the ports of a stored context starts without it and
// removes it from the store, rather than not starting at all.
func TestARecreatedRelayGivesUpAContextWhosePortsAreTaken(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 8)
	first := relayOn(t, minPort, maxPort, store)
	kept, err := first.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	lost, err := first.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	// Something else on the host holds the RTP port of one termination.
	port := lost.Terminations[Core].Local.Port()
	held, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(netip.AddrPortFrom(netip.IPv4Unspecified(), port)))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	again := relayOn(t, minPort, maxPort, store)
	contexts := again.Contexts()
	if len(contexts) != 1 || contexts[0].ID != kept.ID {
		t.Fatalf("the relay created again holds %+v, want the context %s only", contexts, kept.ID)
	}
	saved := contextsOf(t, store)
	if len(saved) != 1 || saved[0].ID != kept.ID {
		t.Errorf("the store holds %+v, want the context %s only", saved, kept.ID)
	}
}

// TestARelayWhoseStoreCannotBeReadDoesNotStart: without its contexts a
// relay would leave the calls of its earlier process without media, and
// answer their Subtract with "no such context", while holding their
// ports.
func TestARelayWhoseStoreCannotBeReadDoesNotStart(t *testing.T) {
	minPort, maxPort := rtptest.PortRange(t, 4)
	_, err := NewRelay(netip.MustParseAddr("127.0.0.1"), minPort, maxPort, nil, unreadableStore{}, quiet())
	if !errors.Is(err, errBroken) {
		t.Fatalf("NewRelay on a store that cannot be read: %v, want the error of the store", err)
	}
}

// unreadableStore is a ContextStore that cannot be read, as the adapter
// of a relay is while the shared store cannot be reached.
type unreadableStore struct{ ContextStore }

func (unreadableStore) Contexts(context.Context) ([]SavedContext, error) { return nil, errBroken }

// TestTheRelayGivesUpWhatItCannotStore: a context, or a termination of
// one, that the relay cannot write to the store is given up again, with
// its ports, rather than carrying media that no later process of the
// relay would know of.
func TestTheRelayGivesUpWhatItCannotStore(t *testing.T) {
	store := &brokenStore{MemoryContextStore: NewMemoryContextStore()}
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)
	free := r.ports.size()

	store.fail.Store(true)
	if _, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core}); !errors.Is(err, errBroken) {
		t.Fatalf("Add on a store that cannot be written: %v, want the error of the store", err)
	}
	if got := r.Contexts(); len(got) != 0 {
		t.Errorf("the relay holds %+v, want no context", got)
	}
	if got := r.ports.size(); got != free {
		t.Errorf("%d free ports, want %d: the ports of the context were not given back", got, free)
	}

	store.fail.Store(false)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	store.fail.Store(true)
	if _, err := r.AddTermination(c.ID, TerminationRequest{}); !errors.Is(err, errBroken) {
		t.Fatalf("AddTermination on a store that cannot be written: %v, want the error of the store", err)
	}
	if got := r.Contexts(); len(got) != 1 || len(got[0].Terminations) != 2 {
		t.Errorf("the relay holds %+v, want the context with its two terminations", got)
	}
	if got := r.ports.size(); got != free-2 {
		t.Errorf("%d free ports, want %d: the ports of the termination were not given back", got, free-2)
	}
}

// TestWhatWasGivenUpLeavesTheStoreAsItWas: the write of a context that
// the controller is told it never got may have landed all the same, so
// what the relay gave up is taken out of the store, and a context that
// stays keeps the terminations it had.
func TestWhatWasGivenUpLeavesTheStoreAsItWas(t *testing.T) {
	store := &lateStore{MemoryContextStore: NewMemoryContextStore()}
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)

	store.late.Store(true)
	if _, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core}); !errors.Is(err, errBroken) {
		t.Fatalf("Add whose write failed after it landed: %v, want the error of the store", err)
	}
	if got := contextsOf(t, store); len(got) != 0 {
		t.Errorf("the store holds %+v, want nothing: the controller was told the Add failed", got)
	}

	store.late.Store(false)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	store.late.Store(true)
	if _, err := r.AddTermination(c.ID, TerminationRequest{}); !errors.Is(err, errBroken) {
		t.Fatalf("AddTermination whose write failed after it landed: %v, want the error of the store", err)
	}
	got := contextsOf(t, store)
	if len(got) != 1 || len(got[0].Terminations) != 2 {
		t.Errorf("the store holds %+v, want the context with the two terminations it had", got)
	}
}

// TestTheRelayTriesAgainToRemoveAContext: a removal the store turns
// down is tried again, so that the ports of a call that has ended are
// not opened again by the next process of the relay.
func TestTheRelayTriesAgainToRemoveAContext(t *testing.T) {
	store := &brokenStore{MemoryContextStore: NewMemoryContextStore()}
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	// The first removal fails, the one after it goes through.
	store.deletes.Store(1)
	if _, err := r.Subtract(c.ID); err != nil {
		t.Fatalf("Subtract whose removal the store turned down: %v, want the context given up all the same", err)
	}
	deadline := time.Now().Add(5 * forgetBackoff)
	for len(contextsOf(t, store)) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("the context %s is still in the store: the relay did not try the removal again", c.ID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheRelayGoesOnWhenAChangeCannotBeStored: a Modify and a Subtract
// are carried out and answered even when the store cannot be written.
// The change is in the media plane already, and a controller told that
// its Modify failed would end a call whose media flows.
func TestTheRelayGoesOnWhenAChangeCannotBeStored(t *testing.T) {
	store := &brokenStore{MemoryContextStore: NewMemoryContextStore()}
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)
	c, err := r.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}

	store.fail.Store(true)
	store.deletes.Store(1)
	far := netip.MustParseAddrPort("198.51.100.20:30000")
	if _, err := r.Modify(c.ID, Core, TerminationRequest{Remote: far}); err != nil {
		t.Errorf("Modify on a store that cannot be written: %v, want the change carried out", err)
	}
	if got := r.Contexts(); len(got) != 1 || got[0].Terminations[Core].Remote != far {
		t.Errorf("the relay holds %+v, want the core termination sending to %v", got, far)
	}
	if got := contextsOf(t, store); len(got) != 1 || got[0].Terminations[1].Remote.IsValid() {
		t.Errorf("the store holds %+v, want the context without the change the store refused", got)
	}
	if _, err := r.Subtract(c.ID); err != nil {
		t.Errorf("Subtract whose removal the store turned down: %v, want the context given up", err)
	}
	if got := r.Contexts(); len(got) != 0 {
		t.Errorf("the relay holds %+v after the Subtract, want no context", got)
	}
}

// TestRTPDoesNotWaitForTheStore: the packets of a context go on while a
// change of that same context waits for the store. The store is on the
// path of the controller, never of the media.
func TestRTPDoesNotWaitForTheStore(t *testing.T) {
	store := &blockingStore{MemoryContextStore: NewMemoryContextStore(),
		entered: make(chan string, 1), release: make(chan struct{})}
	minPort, maxPort := rtptest.PortRange(t, 8)
	r := relayOn(t, minPort, maxPort, store)
	ue, network := udp(t), udp(t)
	c, err := r.Add(TerminationRequest{ID: Access, Remote: addr(ue)}, TerminationRequest{ID: Core, Remote: addr(network)})
	if err != nil {
		t.Fatal(err)
	}
	access, core := c.Terminations[Access].Local, c.Terminations[Core].Local

	store.block.Store(true)
	modified := make(chan struct{})
	go func() {
		defer close(modified)
		r.Modify(c.ID, Core, TerminationRequest{Remote: addr(network)})
	}()
	// The write of the Modify has begun and has not come back.
	entered := <-store.entered
	defer func() {
		close(store.release)
		<-modified
	}()
	if entered != c.ID {
		t.Fatalf("the store was written for context %s, want %s", entered, c.ID)
	}

	ue.WriteToUDPAddrPort([]byte("while the store takes its time"), access)
	network.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1500)
	n, src, err := network.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatalf("nothing reached the network side within 2 s while the store had not answered the Modify: %v", err)
	}
	if got := string(buf[:n]); got != "while the store takes its time" || src != core {
		t.Errorf("the network side got %q from %v, want it from the core termination %v", got, src, core)
	}
}

// TestANewContextAvoidsWhatTheRelayOpenedAgain: after a restart neither
// the ports, nor the context ID, nor the termination names that the
// relay opened again are handed out to a new context. A controller that
// still holds an ID of the earlier process must not reach a context
// that another call has taken.
func TestANewContextAvoidsWhatTheRelayOpenedAgain(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 12)
	first := relayOn(t, minPort, maxPort, store)
	old, err := first.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	// A termination whose name the relay chooses itself, as an MRFP
	// does for its conferences.
	joined, err := first.AddTermination(old.ID, TerminationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	first.Close()

	again := relayOn(t, minPort, maxPort, store)
	// The next ID the relay would hand out is the one of the context it
	// opened again.
	n, err := strconv.Atoi(strings.TrimPrefix(old.ID, "c"))
	if err != nil {
		t.Fatal(err)
	}
	again.nextID.Store(uint64(n) - 1)

	fresh, err := again.Add(TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ID == old.ID {
		t.Errorf("the new context took the ID %s of the context that was opened again", fresh.ID)
	}
	for _, side := range []TerminationID{Access, Core} {
		for _, taken := range old.Terminations {
			if fresh.Terminations[side].Local.Port() == taken.Local.Port() {
				t.Errorf("the %s termination took port %d, which was opened again", side, taken.Local.Port())
			}
		}
	}

	third, err := again.AddTermination(old.ID, TerminationRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if third.ID == joined.ID || third.ID == Access || third.ID == Core {
		t.Errorf("the new termination took the name %s of one that was opened again", third.ID)
	}
}

// TestARecreatedMRFPGoesOnMixingAConference: the contexts of the MRFP
// are those of the same relay, so a conference whose terminations were
// opened again is mixed on, and each party gets the mix without a word
// from the MRFC.
func TestARecreatedMRFPGoesOnMixingAConference(t *testing.T) {
	store := NewMemoryContextStore()
	minPort, maxPort := rtptest.PortRange(t, 20)
	first := relayOn(t, minPort, maxPort, store)
	a, b, c := udp(t), udp(t), udp(t)
	conference, err := first.Add(
		TerminationRequest{ID: "a", Remote: addr(a)},
		TerminationRequest{ID: "b", Remote: addr(b)},
		TerminationRequest{ID: "c", Remote: addr(c)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := readRTP(t, a, 2*time.Second); !ok {
		t.Fatal("the conference sends nothing before the restart")
	}
	first.Close()
	// What was on its way out when the process stopped is in the
	// socket of the party; the test reads what the relay created again
	// sends.
	rtptest.Queued(t, a)

	again := relayOn(t, minPort, maxPort, store)
	if got := again.Contexts(); len(got) != 1 || got[0].ID != conference.ID || len(got[0].Terminations) != 3 {
		t.Fatalf("the relay created again holds %+v, want the conference %s with its three terminations", got, conference.ID)
	}
	if _, ok := readRTP(t, a, 2*time.Second); !ok {
		t.Error("the conference that was opened again sends nothing: the mixer has no party")
	}
}
