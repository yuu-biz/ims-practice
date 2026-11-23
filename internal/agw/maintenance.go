package agw

import (
	"errors"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// This file holds what a media node tells the maintenance API of its
// node (internal/maintenance), and what it does while the operator has
// it shut down or locked.
//
// A context lives in the node that opened its ports: the controller
// holds the ID that this node gave it, and the RTP of the call flows
// through the sockets of this process. An instance that the operator
// takes out of service therefore goes on with the contexts it holds and
// takes no new one, which is the graceful method of TS 23.334 6.1.2 for
// an IMS-AGW and of TS 23.333 6.1.2 for an MRFP: the node is locked for
// new calls, and the connections it has are cleared as their calls are
// disconnected.
//
// While it is not UNLOCKED it goes on with:
//
//   - every operation on a context that exists: the Modify with which a
//     controller points a termination at the other party or starts an
//     announcement, the Subtract that releases a termination or the
//     whole context, and the AddTermination with which a party joins a
//     conference whose mix is in this node. The mix is in this node and
//     nowhere else, so an AddTermination refused here would take a
//     party out of a conference that is in progress, and it adds no
//     context: the node counts the contexts, and their number only
//     falls while it is shutting down;
//   - the events it reports to its controller, and the audits that read
//     its contexts and its announcements. They say what the node holds,
//     and the node is alive.
//
// It refuses Add, the operation that makes a context (TS 29.334
// 5.17.2.2, TS 29.333 5.17.2.2). Over the control API that is 503
// (Service Unavailable), and over H.248 the error 502, "Not ready",
// which is among the codes an IMS-AGW sends (TS 29.334 5.7.10, Table
// 5.7.10.2). The controller takes the next media node of
// its list, where it has one, exactly as it does for a node that does
// not answer or has no port left (internal/pcscf addContext,
// internal/mrfc candidates). The 503 carries no Retry-After: no
// controller of this project remembers that a node refused it, so
// nothing would read one.
//
// SIMPLIFIED: TS 29.334 5.17.3.2 and TS 23.333 8.30. The node tells no
// controller that it is going out of service. A real IMS-AGW or MRFP
// sends a ServiceChange on ROOT with the method GRACEFUL, and its
// controllers then establish no new connection on it until it comes
// back (TS 23.334 6.1.2, TS 23.333 6.1.2). Here each controller learns
// of the drain from the refusal of its next Add, and goes on asking
// this node for one call each until the operator unlocks it. The same
// holds for the H.248 side of Iq and Mp (internal/h248/mediactl), where
// the Controller takes a GRACEFUL ServiceChange from a gateway but no
// Gateway sends one.

// ErrOutOfService means that an operator has taken the media node out
// of service, so that it makes no new context.
var ErrOutOfService = errors.New("agw: the media node is out of service")

// Function is what a media node tells the maintenance API of its node,
// under the name that role gives its media role in the configuration
// file: "agw" for the IMS-AGW and "mrfp" for the MRFP. A node that has
// both runs a relay for each, and each has a Function of its own.
//
// It is ENABLED while the node runs. The relay holds no association
// with its controller that it could report the state of: over the
// control API each operation is a request of its own, and over H.248
// the association is in the Gateway beside it.
//
// Its users are the contexts this relay holds: one for each pass of a
// call through a P-CSCF, one for each announcement, and one for each
// conference, whatever the number of terminations in it. The contexts
// that an earlier process of this relay left in the store, and that it
// opened again when it started, are among them (restore), and so is
// an Add that is making its context (Relay.Add). A shut down
// waits for all of them, so that the operator learns from the state of
// the node when stopping it would cut no media: no RTP of a call, an
// announcement or a mix passes through this process any more. The
// controllers hold the state of the calls themselves and outlive the
// node.
func (r *Relay) Function(role string) maintenance.Function {
	return maintenance.Function{Role: role, Users: r.countContexts}
}

// Manage gives the relay the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the relay is UNLOCKED.
func (r *Relay) Manage(m *maintenance.ManagedFunction) { r.managed.Store(m) }

// unlocked reports whether the operator lets the relay make a context.
func (r *Relay) unlocked() bool {
	m := r.managed.Load()
	return m == nil || m.AdministrativeState() == maintenance.Unlocked
}

// countContexts counts the contexts of this process, which is what a
// shut down waits for, and the Adds that are making one (Relay.Add). It
// reads the length of the list that the relay keeps, and not the store:
// a count is asked for on every shut down and every state of the node,
// and a store outside the process would put a request to another node
// in the way of each of them. The contexts that this process opened
// again from the store when it started are in the list (restore), so
// they are counted.
func (r *Relay) countContexts() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.contexts) + int(r.adding.Load())
}

// contextGone tells the maintenance API that a context has been
// subtracted, or that an Add has failed, either of which may be the
// last one that a shut down waits for: the user quit event of X.731
// 7.1.3.4. The caller must hold neither
// Relay.mu nor the context's locks, since the API counts the contexts
// again.
func (r *Relay) contextGone() {
	if m := r.managed.Load(); m != nil {
		m.UserQuit()
	}
}
