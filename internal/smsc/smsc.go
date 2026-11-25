// Package smsc is the Service Centre (SC) of TS 23.040: it takes short
// messages from UEs, holds them, and delivers them to their recipients,
// trying again until they arrive or their validity period ends (TS
// 23.040 6.1). The sc role of the ims command runs it. It speaks the
// Diameter interfaces of TS 29.338:
//
//   - SGd, as the SMS-IWMSC: a serving node (an MME, an SMSF or an
//     IP-SM-GW) brings a short message from a UE in an OFR, and the SC
//     answers with an OFA that carries the SMS-SUBMIT-REPORT
//     (MOForwardShortMessage);
//   - S6c, as the SMS-GMSC: the SC asks the HSS which node serves the
//     recipient (SRR), and, when the recipient is absent or its memory
//     is full, has the HSS keep the SC address in the message waiting
//     data (RDR). A delivery that succeeds while the HSS holds a flag
//     for the recipient it reports as well (RDR);
//   - SGd, as the SMS-GMSC: the SC forwards the SMS-DELIVER or the
//     SMS-STATUS-REPORT to the serving node in a TFR, and learns from
//     the TFA whether it arrived;
//   - S6c, as the SMS-IWMSC: the HSS alerts the SC when a user it holds
//     short messages for is reachable again (ALR, AlertServiceCentre).
//
// TS 23.040 lets the SC be one node with the SMS-GMSC and the SMS-IWMSC
// (4.1, Figure 4), and this package is all three.
//
// The package works on the decoded structs of package sgd and does not
// touch Diameter itself. The role that wires it passes each OFR and ALR
// that arrives to the method of the same name and sends back the answer
// it returns, and gives the SC an SGdClient and an S6cClient that send
// its requests. All the messages that the SC holds, and the state of
// their delivery, are in a Store, so that any number of SC instances can
// share them.
package smsc

import (
	"context"
	"crypto/rand"
	"github.com/yuu-biz/ims-practice/internal/diameter"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/maintenance"
	"github.com/yuu-biz/ims-practice/internal/sgd"
)

// SGdClient sends the requests of SGd that the SC originates. The SC
// fills each request except Session-Id, Origin-Host and Origin-Realm,
// which the client writes as its Diameter node names itself;
// DestinationHost and DestinationRealm name the serving node that the
// HSS gave, or another instance of it (forward). An error means that no
// answer came; diameter.ErrPeerUnavailable among them means that the
// request did not go out, because the client has no open connection to
// that node.
//
// ServingNodes are the Diameter identities of the serving nodes of a
// realm that the client can send a TFR to now.
type SGdClient interface {
	MTForwardShortMessage(ctx context.Context, req sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)
	ServingNodes(realm string) []string
}

// S6cClient sends the requests of S6c that the SC originates, filled in
// the same way as for SGdClient.
type S6cClient interface {
	SendRoutingInfoForSM(ctx context.Context, req sgd.SendRoutingInfoForSMRequest) (sgd.SendRoutingInfoForSMAnswer, error)
	ReportSMDeliveryStatus(ctx context.Context, req sgd.ReportSMDeliveryStatusRequest) (sgd.ReportSMDeliveryStatusAnswer, error)
}

// Clock tells the time. Tests give the SC one they move by hand.
type Clock interface {
	Now() time.Time
}

// SystemClock is the Clock of the operating system.
var SystemClock Clock = systemClock{}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// Config is how the SC names itself and how patient it is.
type Config struct {
	// Address is the E.164 number of the SC, digits only, such as
	// 12025550100: the RP-Destination Address that UEs send short
	// messages to, and the SC-Address of every request (TS 29.338
	// 6.3.3.2).
	Address string
	// HSSRealm is the Destination-Realm of the S6c requests. HSSHost is
	// their Destination-Host, or empty to let the Diameter routing find
	// the HSS of the user (TS 29.338 5.3.1.1.3).
	HSSRealm string
	HSSHost  string
	// RetrySchedule is how long the SC waits after an attempt that
	// failed for a reason that may pass: the n-th failure in a row waits
	// the n-th delay, and the last delay repeats. Empty means
	// DefaultRetrySchedule.
	RetrySchedule []time.Duration
	// DefaultValidityPeriod is how long the SC keeps a short message
	// whose SMS-SUBMIT has no TP-VP. Zero means 72 hours.
	DefaultValidityPeriod time.Duration
	// Lease is how long one instance may take to deliver one message
	// before another instance may take over. It has to outlast the
	// routing query and the TFR together, and an IP-SM-GW holds the TFA
	// until the UE has answered over SIP. Zero means 3 minutes.
	Lease time.Duration
	// PollInterval is how often Run looks for destinations that are due.
	// Zero means a second.
	PollInterval time.Duration
	// Deliveries is how many destinations one instance delivers to at
	// once, each with a lease, a goroutine and a request in flight. A
	// destination that is due while all of them are in use stays due in
	// the Store, for another instance, or for this one once a delivery
	// has ended. Zero means 100.
	Deliveries int
}

// DefaultRetrySchedule is the RetrySchedule when Config has none. TS
// 23.040 3.2.7 NOTE 5 leaves the autonomous retries of the SC to its
// operator.
var DefaultRetrySchedule = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

const (
	defaultValidityPeriod = 72 * time.Hour
	defaultLease          = 3 * time.Minute
	defaultPollInterval   = time.Second
	defaultDeliveries     = 100
)

// SMSC is the SC. Its methods may be called from many goroutines.
type SMSC struct {
	cfg   Config
	store Store
	sgd   SGdClient
	s6c   S6cClient
	clock Clock
	log   *slog.Logger
	// started counts the deliveries of this instance, from the
	// reservation before the claim to the end of the delivery, for
	// Wait. deliveries holds a token for each of them, up to
	// Config.Deliveries.
	started    sync.WaitGroup
	deliveries chan struct{}
	// life ends when this instance stops (Run). The attempts under way
	// run on it, so that the stop cuts them short, and no delivery
	// begins after it. mu keeps a delivery from beginning while the
	// instance stops, so that Wait sees every one that began.
	life context.Context
	end  context.CancelFunc
	mu   sync.Mutex
	// managed is the administrative state that the maintenance API of
	// the node keeps for the SC, or nil in a node without the API
	// (maintenance.go).
	managed atomic.Pointer[maintenance.ManagedFunction]
	// inHand counts the users of the SC: the requests it is answering
	// and the destinations it holds a lease on.
	inHand atomic.Int64
}

func New(cfg Config, store Store, sgdClient SGdClient, s6cClient S6cClient, clock Clock, log *slog.Logger) *SMSC {
	if len(cfg.RetrySchedule) == 0 {
		cfg.RetrySchedule = DefaultRetrySchedule
	}
	if cfg.DefaultValidityPeriod == 0 {
		cfg.DefaultValidityPeriod = defaultValidityPeriod
	}
	if cfg.Lease == 0 {
		cfg.Lease = defaultLease
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.Deliveries == 0 {
		cfg.Deliveries = defaultDeliveries
	}
	c := &SMSC{cfg: cfg, store: store, sgd: sgdClient, s6c: s6cClient, clock: clock, log: log,
		deliveries: make(chan struct{}, cfg.Deliveries)}
	c.life, c.end = context.WithCancel(context.Background())
	return c
}

// Run delivers what is due, every PollInterval, until ctx ends: the
// retries of messages that could not be delivered, and the messages
// whose validity period ended. Every instance that shares the Store may
// run it; the leases keep them from delivering the same message.
//
// The end of ctx stops this instance, and Run returns once the
// deliveries it has in hand have released their leases. An attempt
// under way, whether Run or an OFR or an ALR began it, is cut short:
// it is not counted, and its destination is due at once for an
// instance in service (attemptHead). A node therefore stops the SC
// before it closes the Diameter connections that the attempts send on.
// A TFR that the end of its connection cut would count as an attempt
// that failed, and its short message would wait for the next retry.
//
// The calls to the Store run on a context that the stop does not end:
// a claim or a release that the store made and this instance never
// heard of would leave the destination leased until the lease ran out.
func (c *SMSC) Run(ctx context.Context) {
	// The stop does not wait for the sweep: a sweep that is still
	// claiming would keep the attempts under way going, and begin new
	// ones, until it returned.
	context.AfterFunc(ctx, c.stop)
	tick := time.NewTicker(c.cfg.PollInterval)
	defer tick.Stop()
	for {
		c.ProcessDue()
		select {
		case <-ctx.Done():
			c.stop()
			c.Wait()
			return
		case <-tick.C:
		}
	}
}

// stop ends the life of this instance: the attempts under way are cut
// short, and no delivery begins after it.
func (c *SMSC) stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.end()
}

// stopped reports whether this instance has stopped.
func (c *SMSC) stopped() bool { return c.life.Err() != nil }

// reserve takes one of the deliveries that Config.Deliveries allows this
// instance, and counts it as a user of the SC, unless the operator has
// the SC out of service or this instance has stopped. It reports whether
// it did. The caller then claims a destination and delivers to it in
// the background, or gives the reservation back with unreserve once the
// claim has failed. Wait waits for the reservation as for the delivery.
func (c *SMSC) reserve() bool {
	// The user is counted before the administrative state is read,
	// which narrows the window in which a shut down counts no user and
	// the claim then follows. A claim that still slips through is an
	// attempt that the drain cuts, as the maintenance API serves any
	// session that comes in late.
	c.userArrived()
	if c.unlocked() && c.takeDelivery() {
		return true
	}
	c.userQuit()
	return false
}

// takeDelivery takes a token of deliveries for Wait to wait for, unless
// this instance has stopped or has none left.
func (c *SMSC) takeDelivery() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped() {
		return false
	}
	select {
	case c.deliveries <- struct{}{}:
		c.started.Add(1)
		return true
	default:
		return false
	}
}

// unreserve gives back what reserve took. The user quits first, so that
// a shut down which this delivery ends has taken effect once Wait
// returns.
func (c *SMSC) unreserve() {
	c.userQuit()
	<-c.deliveries
	c.started.Done()
}

// Wait blocks until the deliveries this instance began, in a sweep
// (ProcessDue) or after an OFR or an ALR, have ended.
func (c *SMSC) Wait() { c.started.Wait() }

// s6cHeader is the routing part of a request to the HSS.
func (c *SMSC) s6cHeader() diameter.Header {
	return diameter.Header{DestinationHost: c.cfg.HSSHost, DestinationRealm: c.cfg.HSSRealm}
}

func newMessageID() string { return rand.Text() }
