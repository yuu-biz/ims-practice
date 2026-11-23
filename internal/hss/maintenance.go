package hss

import (
	"log/slog"
	"sync/atomic"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// This file holds what the HSS tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// The CSCFs and the application servers reach the HSS over Diameter,
// not SIP, so an instance that goes out of service says so on its
// Diameter connections rather than in an answer to a request. Each peer
// gets a Disconnect-Peer-Request with Disconnect-Cause REBOOTING and
// the connection closes (RFC 6733 5.4, 5.4.3). The peer then sends this
// instance nothing, and a client that groups the instances of the HSS
// sends to the next one (diameter.PeerGroup). REBOOTING is the cause
// that lets the peer connect again, which an initiator does after Tc
// (RFC 6733 2.1), so the instance takes its peers back when the
// operator unlocks it.
//
// Answering every new request DIAMETER_TOO_BUSY instead would not do.
// RFC 6733 7.1.3 allows that answer only for a request that asks for
// one named server, and most of what reaches the HSS names none: the
// UAR, the LIR, the MAR, the SAR for an unregistered user and the Sh
// requests all carry a Destination-Realm and no Destination-Host, so
// any instance of the realm may serve them.
//
// While the instance is not UNLOCKED its node holds no listening socket
// for Diameter, so a peer that connects is refused by the transport and
// tries again after Tc. That is a departure from RFC 6733 2.1, which
// requires a node to be ready for connections on port 3868 at all times
// (a MUST), and it is deliberate: REBOOTING has already told the peer
// that this node is about to reboot (5.4.3), and a node that is
// rebooting holds no socket either, so a refused connection is what the
// peer is told to expect. An error CEA in its place has no Result-Code
// that says a node is out of service (5.3), and accepting the
// connection only to answer with another DPR repeats the whole exchange
// every Tc. diameter.Server.SetAccepting weighs the three in full.

// role is the name of the HSS in the configuration file, and the path
// segment of the maintenance API.
const role = "hss"

// Function is what the HSS tells the maintenance API of its node.
// server is the Diameter server of the node, the listener the peers of
// the HSS connect to.
//
// Its users are the Cx and Sh requests it is answering. A shut down
// that ends when the last of them has been answered sends its DPRs
// behind those answers. A lock does not wait, and nor does the deadline
// of a shut down: both close the connections with requests still in
// flight, and an answer that is still on its way out is then dropped
// rather than sent, so the peer that asked sees its connection close
// with no answer to that request. Its operational state is ENABLED
// while the node listens for Diameter connections, or is out of service
// and meant not to listen. It needs no peer of its own to answer, since
// it serves the subscribers from its Store; a Store it cannot read
// fails the one request rather than the role.
func (h *HSS) Function(server *diameter.Server) maintenance.Function {
	d := &drain{server: server, log: h.log}
	return maintenance.Function{
		Role:                       role,
		OperationalState:           d.operationalState,
		Users:                      func() int { return int(h.requests.Load()) },
		AdministrativeStateChanged: d.administrativeStateChanged,
	}
}

// Manage gives the HSS the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the HSS is UNLOCKED.
func (h *HSS) Manage(m *maintenance.ManagedFunction) { h.managed.Store(m) }

// requestAnswered counts the end of a Cx or Sh request, which may be
// the last that a shut down waits for. The node calls it once the
// answer is on its way to the peer (diameter.Node.AfterAnswer), so a
// shut down that ends here sends its DPR behind that answer and not
// in front of it.
func (h *HSS) requestAnswered() {
	h.requests.Add(-1)
	if m := h.managed.Load(); m != nil {
		m.UserQuit()
	}
}

// drain is what the HSS does to the Diameter connections of its node
// when the operator changes its administrative state.
type drain struct {
	server *diameter.Server
	log    *slog.Logger
	// cannotListen is set when the node failed to listen for Diameter
	// connections again after an unlock, so that no peer can reach the
	// HSS although the operator means it to serve.
	cannotListen atomic.Bool
}

// administrativeStateChanged takes the HSS out of service and brings it
// back. SHUTTINGDOWN and LOCKED both stop the node taking on a new
// peer, which is the X.731 7.1.3 difference between them: a
// SHUTTINGDOWN function serves the users it has, and a LOCKED one
// serves no one, so LOCKED also ends the connections it holds.
func (d *drain) administrativeStateChanged(from, to maintenance.AdministrativeState) {
	accepting := to == maintenance.Unlocked
	err := d.server.SetAccepting(accepting)
	d.cannotListen.Store(err != nil)
	if err != nil {
		// A bind that fails on an unlock leaves the HSS UNLOCKED and
		// DISABLED, and nothing here tries it again: the maintenance API
		// tells the role of a change of state, and a second unlock of a
		// function that is UNLOCKED already is no change. The operator
		// locks the HSS and unlocks it again, which is one transition each
		// and comes back through here.
		d.log.Error("cannot listen for Diameter connections again", "error", err.Error())
	}
	if to == maintenance.Locked {
		// Each peer gets a DPR with REBOOTING and the connection closes
		// once its DPA comes back or the Answer timer passes (RFC 6733
		// 5.4). This returns when the last one is closed, so the operator
		// learns that the HSS is LOCKED only once it really is.
		d.server.DisconnectPeers()
	}
}

func (d *drain) operationalState() maintenance.OperationalState {
	if d.cannotListen.Load() {
		return maintenance.Disabled
	}
	return maintenance.Enabled
}
