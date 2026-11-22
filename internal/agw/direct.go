package agw

import "context"

// Direct drives a relay that runs in the same process, with the
// operations and the signatures of Client. A media node whose
// controller drives it over H.248 runs the media gateway side
// (internal/h248/mediactl) beside its relay, and that gateway carries
// out each command by calling a Direct; nothing of it goes over the
// control API, so such a node serves none.
//
// The ctx of each operation is ignored, because the operations of
// Relay take none: a deadline or a cancellation of the caller does not
// reach it. For a relay driven over H.248 that costs nothing, since it
// keeps its contexts in its own process and has no store to write to.
// A relay with a store would be another matter, and configuration
// refuses to put a gateway in front of one.
type Direct struct {
	relay *Relay
}

// NewDirect drives r.
func NewDirect(r *Relay) *Direct { return &Direct{relay: r} }

// Add creates a context with terminations.
func (d *Direct) Add(_ context.Context, terminations ...TerminationRequest) (Context, error) {
	return d.relay.Add(terminations...)
}

// Modify changes a termination. Like Client, it drops the termination
// that the relay answers with: no controller reads it.
func (d *Direct) Modify(_ context.Context, id string, tid TerminationID, change TerminationRequest) error {
	_, err := d.relay.Modify(id, tid, change)
	return err
}

// Subtract removes a context. Like Client, it drops the counts of what
// each termination carried, as the Subtract reply of Iq carries no
// statistics (TS 29.334 5.7.7, Table 5.7.7.2).
func (d *Direct) Subtract(_ context.Context, id string) error {
	_, err := d.relay.Subtract(id)
	return err
}
