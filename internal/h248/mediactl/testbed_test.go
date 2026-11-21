package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// The addresses of the tests are documentation addresses (RFC 5737).
const (
	controllerAddr = "192.0.2.1:2944"
	gatewayAddr    = "192.0.2.2:2944"
)

var (
	controllerMId = h248.MId{DomainName: "pcscf1.ims.example.net"}
	gatewayMId    = h248.MId{DomainName: "agw1.ims.example.net"}
)

// fakeRelay is a Relay that keeps its contexts in a map. Its local
// addresses count up from 198.51.100.10:40000.
type fakeRelay struct {
	mu       sync.Mutex
	next     int
	contexts map[string]*fakeRelayContext
	reserved int
	released int
	// fail, when set, is returned by every call.
	fail error
}

// fakeRelayContext is what the relay was asked of a context: media is
// what each termination is to receive, local where it receives, and
// remote the last request that told it where to send.
type fakeRelayContext struct {
	media  map[agw.TerminationID]agw.Media
	local  map[agw.TerminationID]netip.AddrPort
	remote map[agw.TerminationID]agw.TerminationRequest
}

func newFakeRelay() *fakeRelay { return &fakeRelay{contexts: map[string]*fakeRelayContext{}} }

func (r *fakeRelay) Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return agw.Context{}, r.fail
	}
	r.next++
	r.reserved++
	id := fmt.Sprintf("c%d", r.next)
	addr := netip.MustParseAddr("198.51.100.10")
	port := uint16(40000 + 4*(r.next-1))
	c := &fakeRelayContext{
		media:  map[agw.TerminationID]agw.Media{},
		local:  map[agw.TerminationID]netip.AddrPort{agw.Access: netip.AddrPortFrom(addr, port), agw.Core: netip.AddrPortFrom(addr, port+2)},
		remote: map[agw.TerminationID]agw.TerminationRequest{},
	}
	out := agw.Context{ID: id, Terminations: map[agw.TerminationID]*agw.Termination{}}
	for _, t := range terminations {
		c.media[t.ID] = t.LocalMedia
		if t.Remote.IsValid() {
			c.remote[t.ID] = t
		}
		out.Terminations[t.ID] = &agw.Termination{ID: t.ID, Mode: t.Mode, Local: c.local[t.ID], Remote: t.Remote}
	}
	r.contexts[id] = c
	return out, nil
}

func (r *fakeRelay) Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	c, ok := r.contexts[id]
	if !ok {
		return agw.ErrNoContext
	}
	c.remote[termination] = change
	return nil
}

func (r *fakeRelay) Subtract(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	if _, ok := r.contexts[id]; !ok {
		return agw.ErrNoContext
	}
	delete(r.contexts, id)
	r.released++
	return nil
}

// reserve asks relay for a context as the P-CSCF does: an access and a
// core termination, each to receive media.
func reserve(ctx context.Context, relay Relay, media agw.Media) (agw.Context, error) {
	return relay.Add(ctx, agw.TerminationRequest{ID: agw.Access, LocalMedia: media}, agw.TerminationRequest{ID: agw.Core, LocalMedia: media})
}

func (r *fakeRelay) context(id string) (fakeRelayContext, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.contexts[id]
	if !ok {
		return fakeRelayContext{}, false
	}
	return *c, true
}

func (r *fakeRelay) counts() (reserved, released, open int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reserved, r.released, len(r.contexts)
}

func (r *fakeRelay) setFail(err error) {
	r.mu.Lock()
	r.fail = err
	r.mu.Unlock()
}

// testTimers are the default timers, except that a handler may send
// any number of Pendings. The fake clock may run ahead of a goroutine
// that carries out a request; the Pendings then keep the request
// alive, as they would for a slow handler, but error 506 would end it.
var testTimers = h248.Timers{PendingLimit: 1 << 20, PeerPendingLimit: 1 << 20}

// testbed is a controller and a gateway on a memNetwork.
type testbed struct {
	clock      *fakeClock
	net        *memNetwork
	controller *Controller
	gateway    *Gateway
	relay      *fakeRelay
}

func newTestbed(t *testing.T, config ControllerConfig, gateway ...func(*GatewayConfig)) *testbed {
	t.Helper()
	clock := newFakeClock()
	b := &testbed{clock: clock, net: newMemNetwork(clock), relay: newFakeRelay()}
	b.controller = b.startController(t, config, 1)
	b.gateway = b.startGateway(t, b.relay, 100, 5001, gateway...)
	return b
}

// startController starts a controller. Like a gateway, a controller
// that takes the place of another needs other transaction IDs.
func (b *testbed) startController(t *testing.T, config ControllerConfig, firstTransaction uint32) *Controller {
	t.Helper()
	config.Endpoint.MId = controllerMId
	config.Endpoint.Clock = b.clock
	config.Endpoint.FirstTransactionID = firstTransaction
	config.Endpoint.Timers = testTimers
	c, err := NewController(b.net.listen(controllerAddr), config)
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve()
	t.Cleanup(func() { c.Close() })
	return c
}

// startGateway starts a gateway. A gateway that takes the place of
// another needs other transaction IDs: the controller would answer
// the repetitions of the old requests from the replies it keeps. Each
// of change may change the configuration before the gateway starts.
func (b *testbed) startGateway(t *testing.T, relay Relay, firstContext, firstTransaction uint32, change ...func(*GatewayConfig)) *Gateway {
	t.Helper()
	config := GatewayConfig{
		Endpoint:         h248.EndpointConfig{MId: gatewayMId, Clock: b.clock, FirstTransactionID: firstTransaction, Timers: testTimers},
		LocateController: func() (net.Addr, error) { return memAddr(controllerAddr), nil },
		Relay:            relay,
		MaxWaitingDelay:  10 * time.Millisecond,
		FirstContextID:   firstContext,
	}
	for _, c := range change {
		c(&config)
	}
	g, err := NewGateway(b.net.listen(gatewayAddr), config)
	if err != nil {
		t.Fatal(err)
	}
	go g.Serve()
	t.Cleanup(func() { g.Close() })
	return g
}

// until moves the clock on by step until cond holds. After each step
// it waits for the datagrams to be read, and gives the goroutines that
// the timers start a moment of real time, so that the fake clock does
// not run far ahead of them.
//
// Before each step it also waits for the reply to every ServiceChange
// that has reached the controller. The controller starts the timer of
// the Modify that sets it/ito (activationDelay) while it carries out
// the ServiceChange, in a goroutine of its own, and writes the reply
// after that. Were the clock to pass activationDelay in between, the
// Modify would overtake the reply, and the gateway would refuse it
// with error 505.
func (b *testbed) until(t *testing.T, what string, step time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s at %v", what, b.clock.Now())
		}
		eventually(t, "the replies to the ServiceChanges", func() bool { return serviceChangesAnswered(t, b.net.datagrams()) })
		b.clock.Advance(step)
		b.net.settle(t)
		time.Sleep(time.Millisecond)
	}
}

// serviceChangesAnswered tells whether a controller has answered every
// ServiceChange from the gateway that was not lost.
func serviceChangesAnswered(t *testing.T, ds []datagram) bool {
	t.Helper()
	unanswered := map[uint32]bool{}
	for _, d := range ds {
		if d.lost {
			continue
		}
		m, err := h248.Parse(d.payload)
		if err != nil {
			t.Fatalf("datagram %q: %v", d.payload, err)
		}
		for _, tr := range m.Transactions {
			switch tr := tr.(type) {
			case h248.TransactionRequest:
				if d.from == gatewayAddr && tr.Actions[0].Commands[0].Command == h248.ServiceChange {
					unanswered[tr.TransactionID] = true
				}
			case h248.TransactionReply:
				if d.to == gatewayAddr {
					delete(unanswered, tr.TransactionID)
				}
			}
		}
	}
	return len(unanswered) == 0
}

// registered tells whether the gateway is registered and the
// controller takes it for new contexts, with it/ito set: the reply to
// the Modify that set it has arrived.
func (b *testbed) registered() bool {
	b.gateway.mu.Lock()
	gatewaySide := b.gateway.registered && b.gateway.inactivity.mit > 0
	b.gateway.mu.Unlock()
	available := *b.controller.available.Load()
	if !gatewaySide || len(available) != 1 {
		return false
	}
	a := available[0]
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activated
}

func (b *testbed) waitRegistered(t *testing.T) {
	t.Helper()
	b.until(t, "the registration", 50*time.Millisecond, b.registered)
}

// summary is a datagram as one line: who sent it, and the first
// command of each transaction, or the kind of transaction. key names
// the transactions in it, so that repetitions can be told apart.
func summary(t *testing.T, d datagram) (line, key string) {
	t.Helper()
	m, err := h248.Parse(d.payload)
	if err != nil {
		t.Fatalf("datagram %q: %v", d.payload, err)
	}
	from := "mgc"
	if d.from == gatewayAddr {
		from = "mg"
	}
	var parts, keys []string
	for _, tr := range m.Transactions {
		switch tr := tr.(type) {
		case h248.TransactionRequest:
			c := tr.Actions[0].Commands[0]
			parts = append(parts, fmt.Sprintf("%s=%s", c.Command, c.TerminationID))
			keys = append(keys, fmt.Sprintf("request %d", tr.TransactionID))
		case h248.TransactionReply:
			text := "Reply"
			if err := rejection(tr); err != nil {
				text += fmt.Sprintf(" error %d", err.(*CommandRejected).ErrorDescriptor.ErrorCode)
			}
			parts = append(parts, text)
			keys = append(keys, fmt.Sprintf("reply %d", tr.TransactionID))
		case h248.TransactionPending:
			parts = append(parts, "Pending")
		case h248.TransactionResponseAck:
			parts = append(parts, "Ack")
		}
	}
	return fmt.Sprintf("%s MEGACO/%d %s", from, m.Version, strings.Join(parts, ", ")),
		from + " " + strings.Join(keys, ", ")
}

// summaries summarizes the datagrams that were not lost. The
// procedures of the Endpoint (Pendings, acknowledgements and the
// repetitions of a request or a reply) depend on how fast the
// goroutines run against the fake clock, so they are left out.
func summaries(t *testing.T, ds []datagram) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for _, d := range ds {
		if d.lost {
			continue
		}
		line, key := summary(t, d)
		kind := line[strings.LastIndex(line, " ")+1:]
		if kind == "Pending" || kind == "Ack" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, line)
	}
	return out
}

// sent tells whether any datagram, lost or not, holds text.
func sent(ds []datagram, text string) bool {
	for _, d := range ds {
		if strings.Contains(string(d.payload), text) {
			return true
		}
	}
	return false
}

func errorIs(err, target error) bool { return errors.Is(err, target) }

func mustAddrPort(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }
