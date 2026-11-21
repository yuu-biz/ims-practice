package agw

import (
	"context"
	"net/netip"
	"reflect"
	"testing"
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
