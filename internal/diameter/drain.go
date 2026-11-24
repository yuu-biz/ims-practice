package diameter

import (
	"log/slog"
	"sync/atomic"
)

// ListenerDrain is what a role that its peers reach on the listener of
// its node does to that listener when an operator takes the role out of
// service and brings it back. The peers of such a role open the
// connections, so the role cannot turn them away in an answer: a
// request that names no Destination-Host may not be answered
// DIAMETER_TOO_BUSY (RFC 6733 7.1.3). It turns them away on the
// transport instead. A role that takes no new users stops the server
// accepting connections (SetAccepting), and one that serves no one also
// ends the connections it has, each with a DPR whose Disconnect-Cause
// is REBOOTING (DisconnectPeers). The peer then sends the node nothing,
// and a client that groups the instances of the role sends to the next
// one (PeerGroup); REBOOTING lets it connect again after Tc (RFC 6733
// 2.1, 5.4.3), so the role takes its peers back once it accepts again.
//
// A node that runs two such roles has one listener for both, so taking
// either of them out of service takes the listener from both, and it
// comes back once both are back (Server.SetAccepting).
type ListenerDrain struct {
	server *Server
	role   string
	log    *slog.Logger
	// outOfService is set while the role has the server accept no
	// connection.
	outOfService atomic.Bool
}

// NewListenerDrain returns the drain of role, the name of the role in
// the configuration file, whose peers connect to server.
func NewListenerDrain(server *Server, role string, log *slog.Logger) *ListenerDrain {
	return &ListenerDrain{server: server, role: role, log: log}
}

// Change takes the role out of service or brings it back: accepting
// says whether it takes new peers, which a role in service does, and
// disconnect whether it also ends the connections it has, which a role
// that serves no one does.
//
// With disconnect, it returns once the last connection is closed, after
// its DPA or after the Answer timer (RFC 6733 5.4), so the operator
// learns that the role serves no one only once that is so.
func (d *ListenerDrain) Change(accepting, disconnect bool) {
	d.outOfService.Store(!accepting)
	if err := d.server.SetAccepting(d.role, accepting); err != nil {
		// A bind that fails when the role comes back leaves it in service
		// and unreachable, which Reachable says. The operator takes the
		// role out of service and back again, which tries once more.
		d.log.Error("cannot listen for Diameter connections again", "error", err.Error())
	}
	if disconnect {
		d.server.DisconnectPeers()
	}
}

// Reachable reports whether a new peer can reach the role as far as the
// operator means it to: a role in service is reachable while the server
// accepts, and is not when the node failed to listen again or another
// role of the node has the listener closed. A role out of service is
// meant to be unreachable, which is no fault of the role.
func (d *ListenerDrain) Reachable() bool { return d.outOfService.Load() || d.server.Accepting() }
