package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// count returns how many datagrams after the first skip summary
// matches want.
func count(t *testing.T, b *testbed, skip int, want string) int {
	t.Helper()
	n := 0
	for _, s := range summaries(t, b.net.datagrams()[skip:]) {
		if s == want {
			n++
		}
	}
	return n
}

func TestKeepAliveAuditsRootWhenIdle(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())
	// The reply to the second keep-alive is written after the
	// keep-alive, by another goroutine, so the wait is for both.
	b.until(t, "the keep-alive", 250*time.Millisecond, func() bool {
		return count(t, b, before, "mgc MEGACO/2 AuditValue=ROOT") == 2 && count(t, b, before, "mg MEGACO/2 Reply") == 2
	})
	// Two keep-alives 10 s apart, and nothing from the gateway but
	// the replies: its it/ito (27.5 s) never runs out.
	got := summaries(t, b.net.datagrams()[before:])
	want := []string{"mgc MEGACO/2 AuditValue=ROOT", "mg MEGACO/2 Reply", "mgc MEGACO/2 AuditValue=ROOT", "mg MEGACO/2 Reply"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages:\n%s", strings.Join(got, "\n"))
	}
}

func TestKeepAliveWaitsForIdleness(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())
	ctx := context.Background()
	// A call every 4 s keeps the association busy, so no keep-alive
	// is needed.
	for range 6 {
		r, err := b.controller.Reserve(ctx, Media{})
		if err != nil {
			t.Fatal(err)
		}
		if err := b.controller.Release(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		b.clock.Advance(4 * time.Second)
		time.Sleep(time.Millisecond)
	}
	if n := count(t, b, before, "mgc MEGACO/2 AuditValue=ROOT"); n != 0 {
		t.Errorf("%d keep-alives while calls went on", n)
	}
}

func TestLossOfTheGatewayAndItsRestart(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	old, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}

	// The gateway stops. The keep-alive gets no answer, and after
	// T-MAX the controller takes the gateway to have failed.
	b.gateway.Close()
	b.until(t, "the controller to give up on the gateway", 250*time.Millisecond, func() bool {
		return len(*b.controller.available.Load()) == 0
	})
	if _, err := b.controller.Reserve(ctx, Media{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Reserve without a gateway: %v", err)
	}
	if err := b.controller.Configure(ctx, old.ID, Access, Media{Address: mustAddrPort("192.0.2.30:49170")}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Configure on a failed gateway: %v", err)
	}

	// The gateway comes back with no contexts and registers with
	// Restart. Its context IDs start elsewhere.
	before := len(b.net.datagrams())
	b.relay = newFakeRelay()
	b.gateway = b.startGateway(t, b.relay, 200, 7001)
	b.waitRegistered(t)
	if n := count(t, b, before, "mg MEGACO/1 ServiceChange=ROOT"); n != 1 {
		t.Errorf("%d registrations", n)
	}
	r, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "200@agw1.ims.example.net" {
		t.Errorf("ID = %q", r.ID)
	}
	if err := b.controller.Configure(ctx, old.ID, Access, Media{Address: mustAddrPort("192.0.2.30:49170")}); !errors.Is(err, ErrNoContext) {
		t.Errorf("Configure of a context from before the restart: %v", err)
	}
}

func TestPendingRequestsFailWhenTheGatewayIsLost(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	r, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	b.net.setLose(func(d datagram) bool { return true })
	done := make(chan error, 1)
	go func() {
		err := b.controller.Release(ctx, r.ID)
		done <- err
	}()
	b.until(t, "the Release to fail", 250*time.Millisecond, func() bool { return len(done) == 1 })
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Errorf("Release: %v", err)
	}
}

func TestReRegistrationAfterTheNetworkFails(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	r, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	before := len(b.net.datagrams())

	// Nothing gets through. The controller gives up on the gateway;
	// the gateway's it/ito runs out, its Notify gets no answer, and it
	// tries to come back with Disconnected, which gets lost too.
	b.net.setLose(func(d datagram) bool { return true })
	b.until(t, "the gateway to lose the association", 250*time.Millisecond, func() bool {
		b.gateway.mu.Lock()
		defer b.gateway.mu.Unlock()
		return !b.gateway.registered
	})
	b.until(t, "a Disconnected", 250*time.Millisecond, func() bool {
		return sent(b.net.datagrams()[before:], "Disconnected")
	})
	if !sent(b.net.datagrams()[before:], "it/ito") {
		t.Error("the gateway sent no Notify of it/ito")
	}
	if len(*b.controller.available.Load()) != 0 {
		t.Error("the controller still takes the gateway")
	}

	// The network comes back. The next Disconnected gets through.
	b.net.setLose(nil)
	b.waitRegistered(t)
	got := summaries(t, b.net.datagrams()[before:])
	if !slices.Contains(got, "mg MEGACO/2 ServiceChange=ROOT") {
		t.Errorf("messages:\n%s", strings.Join(got, "\n"))
	}
	if slices.Contains(got, "mg MEGACO/1 ServiceChange=ROOT") {
		t.Error("the gateway registered with Restart, as if it had lost its contexts")
	}
	// The context from before is still there.
	if err := b.controller.Configure(ctx, r.ID, Access, Media{Address: mustAddrPort("192.0.2.30:49170")}); err != nil {
		t.Errorf("Configure after the re-registration: %v", err)
	}
}

func TestGatewayRegistersAgainWithARestartedController(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	before := len(b.net.datagrams())

	// The controller restarts and knows no gateway. When the it/ito of
	// the gateway runs out, its Notify gets error 507, and it comes
	// back with Disconnected.
	b.controller.Close()
	b.controller = b.startController(t, ControllerConfig{}, 3001)
	b.until(t, "the gateway to register again", 250*time.Millisecond, b.registered)
	eventually(t, "the reply to the Modify", func() bool { return len(summaries(t, b.net.datagrams()[before:])) >= 6 })
	got := summaries(t, b.net.datagrams()[before:])
	want := []string{
		"mg MEGACO/2 Notify=ROOT",
		"mgc MEGACO/2 Reply error 507",
		"mg MEGACO/2 ServiceChange=ROOT",
		"mgc MEGACO/2 Reply",
		"mgc MEGACO/2 Modify=ROOT",
		"mg MEGACO/2 Reply",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("messages:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestControllerRefusesAnotherProfile(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	reply, err := b.gateway.endpoint.Request(context.Background(), memAddr(controllerAddr), []h248.ActionRequest{{
		ContextID: h248.ContextNull,
		Commands: []h248.CommandRequest{{
			Command:       h248.ServiceChange,
			TerminationID: h248.TerminationRoot,
			ServiceChange: &h248.ServiceChangeParm{
				Method: h248.Restart, Reason: "901 Cold Boot", Version: 2,
				Profile: &h248.ServiceChangeProfile{ProfileName: "threegIq", Version: 7},
			},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	result := serviceChangeResult(reply)
	if result == nil || result.Profile == nil || *result.Profile != Profile {
		t.Errorf("reply %+v", reply)
	}
	if len(*b.controller.available.Load()) != 0 {
		t.Error("the controller took a gateway of another profile")
	}
}

func TestGatewayFollowsTheAlternateController(t *testing.T) {
	// The controller the gateway is provisioned with sends it on to
	// another, at 192.0.2.3, in the reply to its registration.
	const alternateAddr = "192.0.2.3:2944"
	clock := newFakeClock()
	b := &testbed{clock: clock, net: newMemNetwork(clock), relay: newFakeRelay()}
	alternate := h248.MId{DomainAddress: netip.MustParseAddr("192.0.2.3")}
	first, err := h248.NewEndpoint(b.net.listen(controllerAddr), h248.EndpointConfig{
		MId: controllerMId, Clock: clock, FirstTransactionID: 1, Timers: testTimers,
		Handler: func(ctx context.Context, r *h248.IncomingRequest) h248.TransactionReply {
			c := r.Actions[0].Commands[0]
			return h248.TransactionReply{Actions: []h248.ActionReply{{ContextID: h248.ContextNull, Commands: []h248.CommandReply{{
				Command: c.Command, TerminationID: c.TerminationID,
				ServiceChangeResult: &h248.ServiceChangeResParm{MgcID: &alternate},
			}}}}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go first.Serve()
	t.Cleanup(func() { first.Close() })
	c, err := NewController(b.net.listen(alternateAddr), ControllerConfig{Endpoint: h248.EndpointConfig{
		MId: h248.MId{DomainName: "pcscf2.ims.example.net"}, Clock: clock, FirstTransactionID: 1, Timers: testTimers,
	}})
	if err != nil {
		t.Fatal(err)
	}
	go c.Serve()
	t.Cleanup(func() { c.Close() })
	b.controller = c
	b.gateway = b.startGateway(t, b.relay, 100, 5001)

	// H.248.1 (05/2002) 11.2: the gateway sends the same ServiceChange
	// to the controller that the reply names, and registers there.
	b.waitRegistered(t)
	var registrations []string
	for _, d := range b.net.datagrams() {
		if d.from == gatewayAddr && strings.Contains(string(d.payload), "Method = Restart") {
			registrations = append(registrations, d.to)
		}
	}
	if len(registrations) != 2 || registrations[0] != controllerAddr || registrations[1] != alternateAddr {
		t.Errorf("registrations went to %v, want %s and then %s", registrations, controllerAddr, alternateAddr)
	}
}

func TestOutOfServiceGateway(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	r, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	outOfService := func(method h248.ServiceChangeMethod, reason string) {
		_, err := b.gateway.request([]h248.ActionRequest{{
			ContextID: h248.ContextNull,
			Commands: []h248.CommandRequest{{
				Command: h248.ServiceChange, TerminationID: h248.TerminationRoot,
				ServiceChange: &h248.ServiceChangeParm{Method: method, Reason: reason},
			}},
		}})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Graceful: no new contexts, the old ones go on.
	outOfService(h248.Graceful, "905 Termination Taken Out Of Service")
	if _, err := b.controller.Reserve(ctx, Media{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Reserve after Graceful: %v", err)
	}
	if err := b.controller.Configure(ctx, r.ID, Core, Media{Address: mustAddrPort("203.0.113.40:50000")}); err != nil {
		t.Errorf("Configure after Graceful: %v", err)
	}
	// Forced: the contexts are lost.
	outOfService(h248.Forced, "908 MG Impending Failure")
	if err := b.controller.Configure(ctx, r.ID, Core, Media{Address: mustAddrPort("203.0.113.40:50000")}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Configure after Forced: %v", err)
	}
}

func TestTerminationOutOfServiceClearsItsContext(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	r, err := b.controller.Reserve(context.Background(), Media{})
	if err != nil {
		t.Fatal(err)
	}
	_, id, err := b.controller.lookup(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := len(b.net.datagrams())
	// Termination Out Of Service (TS 29.334 5.17.3.19).
	reply, err := b.gateway.request([]h248.ActionRequest{{
		ContextID: id,
		Commands: []h248.CommandRequest{{
			Command: h248.ServiceChange, TerminationID: terminationID(1, Access, id),
			ServiceChange: &h248.ServiceChangeParm{Method: h248.Forced, Reason: "905 Termination Taken Out Of Service"},
		}},
	}})
	if err != nil || rejection(reply) != nil {
		t.Fatalf("reply %+v, %v", reply, err)
	}
	// H.248.1 (05/2002) 7.2.8: the controller clears the context.
	b.until(t, "the Subtract", 50*time.Millisecond, func() bool {
		_, released, _ := b.relay.counts()
		return released == 1
	})
	if !sent(b.net.datagrams()[before:], "Subtract = ") {
		t.Error("the controller sent no Subtract")
	}
}

func TestTerminationHeartbeat(t *testing.T) {
	var mu sync.Mutex
	inUse := map[string]bool{}
	b := newTestbed(t, ControllerConfig{
		TimerX: time.Minute,
		TerminationHeartbeat: func(ctx context.Context, id string) bool {
			mu.Lock()
			defer mu.Unlock()
			return inUse[id]
		},
	})
	b.waitRegistered(t)
	ctx := context.Background()
	kept, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	lost, err := b.controller.Reserve(ctx, Media{})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	inUse[kept.ID] = true
	mu.Unlock()
	before := len(b.net.datagrams())

	// After timer X, each termination reports its heartbeat. The
	// controller has no use for the second context and releases it.
	//
	// The four timers fall due at the same moment and each sends its
	// Notify from a goroutine of its own, so the order of the Notifies
	// is not fixed. The first Notify about the second context gets
	// error 411, and the Subtract that follows stops the heartbeat of
	// its other termination, which then may never be reported. The
	// controller writes the reply with error 411 and sends the
	// Subtract from different goroutines, so either may come first,
	// and the Notifies about the first context may still be on their
	// way when the relay has released the second. The wait is for
	// all that must be sent.
	always := []string{
		fmt.Sprintf("mg MEGACO/2 Notify=%s", terminationID(1, Access, 100)),
		fmt.Sprintf("mg MEGACO/2 Notify=%s", terminationID(1, Core, 100)),
		"mgc MEGACO/2 Reply error 411",
		"mgc MEGACO/2 Subtract=*",
	}
	oneOf := []string{
		fmt.Sprintf("mg MEGACO/2 Notify=%s", terminationID(1, Access, 101)),
		fmt.Sprintf("mg MEGACO/2 Notify=%s", terminationID(1, Core, 101)),
	}
	reported := func() bool {
		got := summaries(t, b.net.datagrams()[before:])
		for _, want := range always {
			if !slices.Contains(got, want) {
				return false
			}
		}
		return slices.ContainsFunc(oneOf, func(want string) bool { return slices.Contains(got, want) })
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("messages:\n%s", strings.Join(summaries(t, b.net.datagrams()[before:]), "\n"))
		}
	})
	b.until(t, "the hanging context to be released", 250*time.Millisecond, func() bool {
		_, released, _ := b.relay.counts()
		return released == 1 && reported()
	})
	if _, ok := b.relay.context("c1"); !ok {
		t.Error("the context in use was released")
	}
	if err := b.controller.Release(ctx, lost.ID); !errors.Is(err, ErrNoContext) {
		t.Errorf("Release of the released context: %v", err)
	}
}

func TestConcurrentContexts(t *testing.T) {
	b := newTestbed(t, ControllerConfig{})
	b.waitRegistered(t)
	ctx := context.Background()
	const calls = 50
	errs := make(chan error, calls)
	var wg sync.WaitGroup
	for i := range calls {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := b.controller.Reserve(ctx, Media{Codecs: nil})
			if err != nil {
				errs <- err
				return
			}
			remote := Media{Address: mustAddrPort(fmt.Sprintf("192.0.2.30:%d", 50000+2*i))}
			for _, side := range []Side{Access, Core} {
				if err := b.controller.Configure(ctx, r.ID, side, remote); err != nil {
					errs <- err
					return
				}
			}
			if err := b.controller.Release(ctx, r.ID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if reserved, released, open := b.relay.counts(); reserved != calls || released != calls || open != 0 {
		t.Errorf("reserved %d, released %d, open %d", reserved, released, open)
	}
}
