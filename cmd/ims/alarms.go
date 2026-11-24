package main

import (
	"log/slog"

	"github.com/yuu-biz/ims-practice/internal/diameter"
	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// diameterPeerDown is the alarm that role raises while it cannot reach
// peer, one of the Diameter peers that it sends its requests to (TS
// 28.111 6.3, after ITU-T X.733 8.1.1 and 8.1.2):
//
//   - communications alarm, the type of a fault in conveying information
//     from one point to another;
//   - communications subsystem failure, the probable cause of a failure
//     of what carries the communications over the links (TS 28.111 Table
//     B.2, which gives it the communications alarm type);
//   - "Diameter peer down" as the specific problem, which tells the
//     alarm apart from another of the same cause, such as one about a
//     media relay.
//
// The alarmed object is the peer itself, not the group of peers that the
// role sends a request to: each peer that is gone has its own alarm,
// which names the instance to repair. Whether the role still has a peer
// left is its operational state, DISABLED while none of the group takes
// requests (TS 28.622 5.3.3).
func diameterPeerDown(role string, peer *diameter.Peer) maintenance.AlarmIdentity {
	return maintenance.AlarmIdentity{
		Role:            role,
		Peer:            maintenance.Peer(peer.Host()),
		AlarmType:       maintenance.CommunicationsAlarm,
		ProbableCause:   maintenance.CommunicationsSubsystemFailure,
		SpecificProblem: "Diameter peer down",
	}
}

// diameterPeerDownSeverity is MAJOR: the role has lost a peer and needs
// it back urgently (X.733 8.1.2.3), but a role with another instance of
// the peer still serves. A role with no instance left says so in its
// operational state.
const diameterPeerDownSeverity = maintenance.Major

// raiseDiameterPeerAlarms has role raise the alarm diameterPeerDown on
// node while it cannot reach one of peers, and clear it once the
// connection is open again. The Diameter layer decides when a peer is
// unreachable (diameter.Reachability): a connection that closes and
// opens again at the next attempt, Tc later, raises nothing. The alarm
// is the node's own, in the alarm list of its process, so the node needs
// nothing outside itself to raise it.
func raiseDiameterPeerAlarms(node *maintenance.Node, role string, peers []*diameter.Peer, log *slog.Logger) {
	for _, p := range peers {
		id := diameterPeerDown(role, p)
		p.WatchReachability(func(r diameter.Reachability) {
			var err error
			if r.Unreachable {
				err = node.RaiseAlarm(maintenance.Alarm{AlarmIdentity: id, PerceivedSeverity: diameterPeerDownSeverity, AdditionalText: r.Reason})
			} else {
				err = node.ClearAlarm(id)
			}
			if err != nil {
				log.Error("the alarm list refused the alarm of a Diameter peer", "role", role, "peer", p.Host(), "error", err.Error())
			}
		})
	}
}
