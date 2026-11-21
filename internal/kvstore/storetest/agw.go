package storetest

import (
	"context"
	"fmt"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/internal/agw"
)

// AGW runs the suite of agw.ContextStore. newStore returns the
// ContextStore of the relay instance named relay. Within one test, a
// second call with the same name stands for that relay created again at
// the same address: it must find what the first one saved. Different
// names are different relays on one store, and must not see each
// other's contexts.
//
// The other suites take a pair of Stores of one role; a ContextStore
// belongs to one relay instance, which alone owns its ports, so this
// suite takes the
// instance's name instead. agw.MemoryContextStore passes it when
// newStore gives the same value for the same name: a relay whose
// process lives on.
func AGW(t *testing.T, newStore func(t *testing.T, relay string) agw.ContextStore) {
	cases := []struct {
		name string
		run  func(t *testing.T, newStore func(t *testing.T, relay string) agw.ContextStore)
	}{
		{"PutReplaceDelete", agwPutReplaceDelete},
		{"NoContexts", agwNoContexts},
		{"RelaysApart", agwRelaysApart},
		{"RecreatedRelay", agwRecreatedRelay},
		{"ConcurrentPuts", agwConcurrentPuts},
		{"ConcurrentPutsAndDeletes", agwConcurrentPutsAndDeletes},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.run(t, newStore) })
	}
}

// relayCall is a context of one pass of a call through the IMS-AGW:
// the access side has learned the UE's address, the core side not yet.
func relayCall(id string, port uint16) agw.SavedContext {
	return agw.SavedContext{ID: id, Terminations: []agw.SavedTermination{
		{ID: agw.Access, LocalRTP: port, Remote: netip.MustParseAddrPort("192.0.2.10:49170"), Mode: agw.SendReceive},
		{ID: agw.Core, LocalRTP: port + 2, Mode: agw.SendReceive},
	}}
}

func contextsOf(t *testing.T, s agw.ContextStore) []agw.SavedContext {
	t.Helper()
	got, err := s.Contexts(context.Background())
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	return got
}

func putContext(t *testing.T, s agw.ContextStore, c agw.SavedContext) {
	t.Helper()
	if err := s.PutContext(context.Background(), c); err != nil {
		t.Fatalf("PutContext(%s): %v", c.ID, err)
	}
}

func deleteContext(t *testing.T, s agw.ContextStore, id string) {
	t.Helper()
	if err := s.DeleteContext(context.Background(), id); err != nil {
		t.Fatalf("DeleteContext(%s): %v", id, err)
	}
}

// sameContexts compares two lists of contexts. An empty list and nil are
// the same: both are a relay without contexts.
func sameContexts(got, want []agw.SavedContext) bool {
	return len(got) == 0 && len(want) == 0 || reflect.DeepEqual(got, want)
}

// agwPutReplaceDelete is internal/agw's TestMemoryContextStore, ported.
func agwPutReplaceDelete(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	s := newStore(t, "agw-1")
	call := relayCall("c2", 20000)
	conference := agw.SavedContext{ID: "c1", Terminations: []agw.SavedTermination{{ID: "t1", LocalRTP: 20004, Mode: agw.ReceiveOnly}}}
	putContext(t, s, call)
	putContext(t, s, conference)
	// The store keeps its own copy: a later change by the relay goes in
	// only with the next PutContext.
	call.Terminations[1].Remote = netip.MustParseAddrPort("198.51.100.20:30000")

	if got, want := contextsOf(t, s), []agw.SavedContext{conference, relayCall("c2", 20000)}; !sameContexts(got, want) {
		t.Fatalf("Contexts = %+v, want %+v", got, want)
	}

	putContext(t, s, call)
	deleteContext(t, s, "c1")
	deleteContext(t, s, "gone")
	if got, want := contextsOf(t, s), []agw.SavedContext{call}; !sameContexts(got, want) {
		t.Fatalf("Contexts after a replace and a delete = %+v, want %+v", got, want)
	}
	deleteContext(t, s, "c2")
	if got := contextsOf(t, s); len(got) != 0 {
		t.Fatalf("Contexts after the last delete = %+v, want none", got)
	}
}

func agwNoContexts(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	s := newStore(t, "agw-1")
	if got := contextsOf(t, s); len(got) != 0 {
		t.Fatalf("Contexts of a new relay = %+v, want none", got)
	}
	deleteContext(t, s, "c1")
}

// agwRelaysApart has two relays on one store, with a context ID in
// common, and a third whose name holds the separator of the keys.
func agwRelaysApart(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	one, two, three := newStore(t, "agw-1"), newStore(t, "agw-2"), newStore(t, "agw-1:context")
	putContext(t, one, relayCall("c1", 20000))
	putContext(t, one, relayCall("c2", 20004))
	putContext(t, two, relayCall("c1", 30000))
	putContext(t, three, relayCall("c1", 40000))

	deleteContext(t, two, "c2")
	if got, want := contextsOf(t, one), []agw.SavedContext{relayCall("c1", 20000), relayCall("c2", 20004)}; !sameContexts(got, want) {
		t.Errorf("Contexts of agw-1 = %+v, want %+v", got, want)
	}
	deleteContext(t, one, "c1")
	if got, want := contextsOf(t, two), []agw.SavedContext{relayCall("c1", 30000)}; !sameContexts(got, want) {
		t.Errorf("Contexts of agw-2 = %+v, want %+v", got, want)
	}
	if got, want := contextsOf(t, three), []agw.SavedContext{relayCall("c1", 40000)}; !sameContexts(got, want) {
		t.Errorf("Contexts of agw-1:context = %+v, want %+v", got, want)
	}
}

// agwRecreatedRelay has a relay add, change and subtract contexts, and
// a relay created again under its name restore what is left.
func agwRecreatedRelay(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	before := newStore(t, "agw-1")
	for i, id := range []string{"c1", "c2", "c3"} {
		putContext(t, before, relayCall(id, 20000+uint16(i)*4))
	}
	changed := relayCall("c2", 20004)
	changed.Terminations[1].Remote = netip.MustParseAddrPort("198.51.100.20:30000")
	changed.Terminations[1].Mode = agw.Inactive
	putContext(t, before, changed)
	deleteContext(t, before, "c1")

	after := newStore(t, "agw-1")
	if got, want := contextsOf(t, after), []agw.SavedContext{changed, relayCall("c3", 20008)}; !sameContexts(got, want) {
		t.Fatalf("Contexts of the recreated relay = %+v, want %+v", got, want)
	}
}

// agwConcurrentPuts has the calls of one relay add their contexts at
// once, through two handles of the relay's store: each of them must
// keep its entry in the index.
func agwConcurrentPuts(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	a, b := newStore(t, "agw-1"), newStore(t, "agw-1")
	const n = 20
	errs := make([]error, n)
	parallel(n, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		errs[i] = s.PutContext(context.Background(), relayCall(fmt.Sprintf("c%02d", i), 20000+uint16(i)*4))
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("PutContext of call %d: %v", i, err)
		}
	}
	var want []agw.SavedContext
	for i := range n {
		want = append(want, relayCall(fmt.Sprintf("c%02d", i), 20000+uint16(i)*4))
	}
	if got := contextsOf(t, newStore(t, "agw-1")); !sameContexts(got, want) {
		t.Fatalf("Contexts after %d concurrent puts = %d contexts %v, want %d", n, len(got), contextIDs(got), n)
	}
}

// agwConcurrentPutsAndDeletes ends half of the calls while new ones
// start.
func agwConcurrentPutsAndDeletes(t *testing.T, newStore func(*testing.T, string) agw.ContextStore) {
	s := newStore(t, "agw-1")
	const n = 10
	for i := range n {
		putContext(t, s, relayCall(fmt.Sprintf("old%02d", i), 20000+uint16(i)*4))
	}
	errs := make([]error, 2*n)
	parallel(2*n, func(i int) {
		if i < n {
			errs[i] = s.DeleteContext(context.Background(), fmt.Sprintf("old%02d", i))
		} else {
			errs[i] = s.PutContext(context.Background(), relayCall(fmt.Sprintf("new%02d", i-n), 30000+uint16(i)*4))
		}
	})
	for i, err := range errs {
		if err != nil {
			t.Fatalf("operation %d: %v", i, err)
		}
	}
	got := contextIDs(contextsOf(t, s))
	var want []string
	for i := range n {
		want = append(want, fmt.Sprintf("new%02d", i))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Contexts = %v, want %v", got, want)
	}
}

func contextIDs(cs []agw.SavedContext) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	slices.SortFunc(ids, strings.Compare)
	return ids
}
