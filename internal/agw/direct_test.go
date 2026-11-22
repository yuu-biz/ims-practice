package agw

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

// A Direct carries out the same three operations on the relay as a
// Client does over the control API, and passes the relay's errors on,
// so that a controller behind either one sees the same thing.
func TestDirectCarriesOutTheOperationsOnTheRelay(t *testing.T) {
	relay := newRelay(t, 4)
	defer relay.Close()
	direct := NewDirect(relay)
	ctx := context.Background()

	c, err := direct.Add(ctx, TerminationRequest{ID: Access}, TerminationRequest{ID: Core})
	if err != nil {
		t.Fatal(err)
	}
	access := c.Terminations[Access]
	if access == nil || !access.Local.IsValid() {
		t.Fatalf("the context %+v has no access termination with a local address", c)
	}

	ue := netip.MustParseAddrPort("192.0.2.30:49170")
	if err := direct.Modify(ctx, c.ID, Access, TerminationRequest{Remote: ue}); err != nil {
		t.Fatal(err)
	}
	stored := relay.Contexts()
	if len(stored) != 1 {
		t.Fatalf("the relay holds %d contexts, want 1", len(stored))
	}
	if got := stored[0].Terminations[Access]; got == nil || got.Remote != ue {
		t.Errorf("the access termination is %+v, want it to send to %v", got, ue)
	}

	if err := direct.Subtract(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if left := relay.Contexts(); len(left) != 0 {
		t.Errorf("the relay still holds %+v", left)
	}
	if err := direct.Subtract(ctx, c.ID); !errors.Is(err, ErrNoContext) {
		t.Errorf("a second Subtract: %v, want %v", err, ErrNoContext)
	}
	if err := direct.Modify(ctx, c.ID, Access, TerminationRequest{Remote: ue}); !errors.Is(err, ErrNoContext) {
		t.Errorf("a Modify of the released context: %v, want %v", err, ErrNoContext)
	}
}
