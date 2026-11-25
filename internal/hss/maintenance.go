package hss

import (
	"fmt"
	"slices"

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
	d := diameter.NewListenerDrain(server, role, h.log)
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if d.Reachable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(h.requests.Load()) },
		// SHUTTINGDOWN and LOCKED both stop the node taking on a new
		// peer, which is the X.731 7.1.3 difference between them: a
		// SHUTTINGDOWN function serves the users it has, and a LOCKED one
		// serves no one, so LOCKED also ends the connections it holds.
		AdministrativeStateChanged: func(_, to maintenance.AdministrativeState) {
			d.Change(to == maintenance.Unlocked, to == maintenance.Locked)
		},
	}
}

// Manage gives the HSS the administrative state that the maintenance
// API of its node keeps for it. Until then, and in a node without the
// API, the HSS is UNLOCKED.
func (h *HSS) Manage(m *maintenance.ManagedFunction) { h.managed.Store(m) }

// Settings are the parts of the HSS configuration that the maintenance
// API of its node changes while the HSS runs: which subscribers the
// network admits, and the service profiles of the subscriptions that
// name none of their own. An operator narrows a network to the users a
// feature is tried on, and gives a feature to the test users or to
// everyone, without restarting an instance and without the instances of
// the role disagreeing for as long as a restart takes.
//
// Each value is checked against what this HSS has, and the API sets
// none of a request's settings unless every one of them is good, so a
// request that names a policy or a profile the HSS does not have
// changes nothing. The criteria of a profile are not here: what an
// operator chooses is which profile a subscription gets, not what a
// profile holds. Nor are the subscribers, which the provisioning API
// changes one at a time.
//
// Narrowing the admission policy needs nothing else of the HSS: the
// registrations that are valid then stay as they are, and the requests
// that arrive from that moment on are refused (Admission).
//
// A change of either profile name gives another user profile to every
// subscription that the name decides for, so the HSS pushes the
// complete profile to the S-CSCF of each of those users whose profile
// an S-CSCF has, rather than leave it until the next registration (TS
// 29.228 6.6.1, pushChangedSettings). The subscriptions that name a
// profile of their own take neither name, and get no push from either.
func (h *HSS) Settings() []maintenance.Setting {
	const (
		admission   = role + ".admission"
		defaultName = role + ".default_service_profile"
		testUser    = role + ".test_user_service_profile"
	)
	// One group: a change that names several of them puts them in force
	// at once, so no request sees the policy of one change beside the
	// profile names of the one before it. Each set writes the pending
	// settings, which commitSettings publishes.
	group := maintenance.SettingsGroup{
		Begin:  h.beginSettings,
		Commit: func(uint64) error { h.commitSettings(); return nil },
	}
	return maintenance.Together(group,
		maintenance.NewSetting(admission,
			"which subscribers the network admits: all, or test_users for the subscriptions marked test_user",
			h.Admission,
			// Not ParseAdmission: that reads a configuration file, where
			// an absent policy means all, and a request that names the
			// setting means to name a policy.
			func(a Admission) error {
				if !slices.Contains(Admissions, a) {
					return fmt.Errorf("%q is none of %v", a, Admissions)
				}
				return nil
			},
			func(a Admission) { h.pending.admission = a }),
		maintenance.NewSetting(defaultName,
			"the service profile of a subscription that names none of its own",
			h.DefaultServiceProfile,
			func(name string) error { return h.config.CheckServiceProfileName(defaultName, name) },
			func(name string) { h.pending.defaultServiceProfile = name }),
		maintenance.NewSetting(testUser,
			"the service profile of a subscription marked test_user that names none of its own",
			h.TestUserServiceProfile,
			func(name string) error { return h.config.CheckServiceProfileName(testUser, name) },
			func(name string) { h.pending.testUserServiceProfile = name }),
	)
}

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
