package smsc

import (
	"sync"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
)

// This file holds what the SC tells the maintenance API of its node
// (internal/maintenance), and what it does while the operator has it
// shut down or locked.
//
// Everything the SC keeps past one request is in the Store: the short
// messages it holds, by recipient, and the time at which each recipient
// is due for its next attempt. Any instance takes a short message and
// any instance delivers it; the lease on a recipient (Lease) is the only
// thing that ties a piece of that work to one instance, and only while
// the instance attempts a delivery. So an instance that the operator
// takes out of service has to finish only the attempts it has begun,
// and every other piece of work goes to an instance in service through
// the Store.
//
// The serving nodes reach the SC over Diameter, and the SC cannot turn
// a new short message away in its answer. The OFR names no
// Destination-Host where the serving node knows only the realm of the
// SMS-IWMSC (TS 29.338 6.3.1.1), as the IP-SM-GW of this project does,
// and RFC 6733 7.1.3 allows DIAMETER_TOO_BUSY, the answer that sends a
// request to another node, only for a request that names one server.
// The failure causes of TS 29.338 Table 6.2.1.1/2 refuse the short
// message itself: "Service Centre congestion" would reach the UE as the
// failure of its message (TS 24.011 Table 8.4 part 1), and send it to
// no other instance. The node therefore takes the SC out of service on
// its Diameter listener, as it does the HSS (diameter.ListenerDrain,
// wired in cmd/ims): a shut down stops it accepting connections, and a
// lock ends those it has with a DPR, after which the serving node sends
// its OFRs to another instance.
//
// While it is not UNLOCKED, the SC goes on with:
//
//   - each OFR that still arrives on a connection it has. It holds the
//     short message in the Store and answers as ever, and leaves the
//     delivery to an instance in service: the short message is then the
//     responsibility of the SC as a whole (TS 23.040 6.1), not of this
//     instance;
//   - each ALR of the HSS. It is the end of the wait that an earlier
//     delivery began (TS 23.040 3.2.7), and all it asks is that the
//     queue of the user be due now, which the Store does for every
//     instance (AlertServiceCentre). Refusing it would leave the
//     address of the SC in the MWD and the short messages to the next
//     retry (TS 29.338 5.2.2.2);
//   - each delivery attempt of a lease it holds, until the TFA comes
//     back or no answer comes. It then releases the lease, and claims
//     neither the next message of that queue nor any other recipient,
//     so retries, expiries of the validity period and alerted queues
//     wait for an instance in service (ProcessDue, deliverSoon, serve).
//
// An SC that no other instance shares the Store with therefore holds
// what arrives until the operator unlocks it.
//
// A lock, or the deadline of a shut down, does not wait for the
// attempts in hand: the node ends the connections, and a TFR that is
// still waiting for its TFA gets no answer. The SC reads that as a
// system failure, which may pass (TS 23.040 Table 1), and keeps the
// short message for its next retry; the serving node may have delivered
// it already, and the recipient then gets it twice.

// role is the name of the SC in the configuration file, and the path
// segment of the maintenance API.
const role = "sc"

// Function is what the SC tells the maintenance API of its node. It
// cannot work while no instance of the HSS takes its S6c requests,
// which hssAvailable tells: without the HSS the SC can neither check
// the recipient of a short message it is handed, and refuses it with
// "Service Centre congestion", nor ask which node serves the recipient
// of one it holds.
//
// Its users are what this instance has in its hands and has not
// finished:
//
//   - each OFR and each ALR it is answering, which the node counts
//     (Answering) until the answer is on its way, so that the DPR of a
//     lock goes behind it;
//   - each recipient it holds a lease on, from the claim until the
//     lease is released: the routing query, the TFR to each serving node
//     the HSS named, and the report of a failure to the HSS. An IP-SM-GW
//     answers a TFR only once the UE has sent its delivery report or its
//     wait for one has run out, so one attempt can last a minute or
//     more.
//
// The short messages it holds in the Store are no users: any instance
// delivers them. A shut down with no lease in hand reaches LOCKED in
// the time an OFR takes. What else keeps it from LOCKED is a steady
// stream of OFRs on the connections it still has, which the
// timeout_seconds of the shut down bounds, as it does for the HSS.
func (c *SMSC) Function(hssAvailable func() bool) maintenance.Function {
	return maintenance.Function{
		Role: role,
		OperationalState: func() maintenance.OperationalState {
			if hssAvailable() {
				return maintenance.Enabled
			}
			return maintenance.Disabled
		},
		Users: func() int { return int(c.inHand.Load()) },
	}
}

// Manage gives the SC the administrative state that the maintenance API
// of its node keeps for it. Until then, and in a node without the API,
// the SC is UNLOCKED.
func (c *SMSC) Manage(f *maintenance.ManagedFunction) { c.managed.Store(f) }

// Answering counts a request that the SC is answering, an OFR or an
// ALR, as one of its users, until answered is called. The node calls
// answered once the answer is on its way to the peer
// (diameter.Node.AfterAnswer): a shut down that ends with this request
// then ends the connection behind the answer and not in front of it.
// answered may be called more than once; only the first call counts.
func (c *SMSC) Answering() (answered func()) {
	c.userArrived()
	var once sync.Once
	return func() { once.Do(c.userQuit) }
}

// unlocked reports whether the operator lets the SC take new work.
func (c *SMSC) unlocked() bool {
	f := c.managed.Load()
	return f == nil || f.AdministrativeState() == maintenance.Unlocked
}

// userArrived counts a piece of work that the SC has taken on.
func (c *SMSC) userArrived() { c.inHand.Add(1) }

// userQuit counts the end of one, which may be the last that a shut
// down waits for: the user quit event of X.731 7.1.3.4.
func (c *SMSC) userQuit() {
	c.inHand.Add(-1)
	if f := c.managed.Load(); f != nil {
		f.UserQuit()
	}
}
