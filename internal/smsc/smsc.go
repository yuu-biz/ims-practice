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
//     data (RDR);
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
// HSS gave. An error means that no answer came.
type SGdClient interface {
	MTForwardShortMessage(ctx context.Context, req sgd.MTForwardShortMessageRequest) (sgd.MTForwardShortMessageAnswer, error)
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
}

// DefaultRetrySchedule is the RetrySchedule when Config has none. TS
// 23.040 3.2.7 NOTE 5 leaves the autonomous retries of the SC to its
// operator.
var DefaultRetrySchedule = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

const (
	defaultValidityPeriod = 72 * time.Hour
	defaultLease          = 3 * time.Minute
	defaultPollInterval   = time.Second
)

// SMSC is the SC. Its methods may be called from many goroutines.
type SMSC struct {
	cfg   Config
	store Store
	sgd   SGdClient
	s6c   S6cClient
	clock Clock
	log   *slog.Logger
	// started counts the deliveries this instance began in the
	// background, for Wait.
	started sync.WaitGroup
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
	return &SMSC{cfg: cfg, store: store, sgd: sgdClient, s6c: s6cClient, clock: clock, log: log}
}

// Run delivers what is due, every PollInterval, until ctx ends: the
// retries of messages that could not be delivered, and the messages
// whose validity period ended. Every instance that shares the Store may
// run it; the leases keep them from delivering the same message.
func (c *SMSC) Run(ctx context.Context) {
	tick := time.NewTicker(c.cfg.PollInterval)
	defer tick.Stop()
	for {
		c.ProcessDue(ctx)
		select {
		case <-ctx.Done():
			c.Wait()
			return
		case <-tick.C:
		}
	}
}

// Wait blocks until the deliveries this instance began in the
// background, after an OFR or an ALR, have ended. A role calls it once
// its Diameter server has stopped passing requests in.
func (c *SMSC) Wait() { c.started.Wait() }

// s6cHeader is the routing part of a request to the HSS.
func (c *SMSC) s6cHeader() diameter.Header {
	return diameter.Header{DestinationHost: c.cfg.HSSHost, DestinationRealm: c.cfg.HSSRealm}
}

func newMessageID() string { return rand.Text() }
