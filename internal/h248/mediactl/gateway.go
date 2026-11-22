package mediactl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// Gateway is the media gateway side of Iq, the IMS-AGW: it registers
// with a controller, turns the commands it receives into calls of a
// Relay, and reports the events of TS 29.334 5.17.2.6 and 5.17.3.16
// with Notify.
//
// Set up with a Processor in place of a Relay, it is the media gateway
// side of Mp, the MRFP: it registers with MpProfile, turns the commands
// of TS 29.333 5.17.2 into calls of the Processor, and reports the end
// of an announcement (5.17.2.11) and the termination heartbeat
// (5.17.2.32) with Notify.
//
// A Gateway is one media gateway with one controller. Its contexts
// live in its own memory, as the contexts of the media relay do, and
// are known to other nodes only by the context ID in the commands.
type Gateway struct {
	endpoint *h248.Endpoint
	profile  profile
	relay    Relay
	// processor is set in place of relay for Mp.
	processor Processor
	clock     h248.Clock
	log       *slog.Logger
	locate    func() (net.Addr, error)
	mwd       time.Duration
	timerX    time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	// nextContextID is the last context ID handed out.
	nextContextID atomic.Uint32
	// contexts holds a *gatewayContext for each context, keyed by its
	// h248.ContextID; for Mp, an *mpContext.
	contexts sync.Map
	// byProcessorContext holds each *mpContext again, keyed by the ID
	// that the Processor gave its context, so that SignalCompleted can
	// find it.
	byProcessorContext sync.Map

	// lost wakes the registration loop when the association with the
	// controller has failed.
	lost chan struct{}

	mu sync.Mutex
	// registered is true from the reply to the ServiceChange until
	// the association fails. Requests that come at other times are
	// refused with error 505 (H.248.1 (05/2002) 11.2).
	registered bool
	controller net.Addr
	inactivity inactivityTimer
}

// inactivityTimer is the event it/ito on ROOT, which the controller
// sets so that the gateway can tell when it has stopped hearing from
// the controller (H.248.14 (03/2009) 6.6.4).
type inactivityTimer struct {
	requestID uint32
	// mit is the "maximum inactivity time". Zero means that the event
	// is not set.
	mit   time.Duration
	timer h248.Timer
}

// gatewayContext is a context of the gateway and the context of the
// Relay that stands behind it.
type gatewayContext struct {
	id      h248.ContextID
	relayID string
	group   uint16

	mu        sync.Mutex
	released  bool
	heartbeat map[agw.TerminationID]*heartbeat
}

// heartbeat is the event hangterm/thb on one termination (H.248.36
// (03/2013) 5.2.1).
type heartbeat struct {
	requestID uint32
	timerX    time.Duration
	timer     h248.Timer
}

// GatewayConfig sets up a Gateway.
type GatewayConfig struct {
	// Endpoint sets up the Endpoint that the Gateway sends and
	// receives with. Its Handler must be nil: the Gateway is the
	// handler. Its Version is replaced with ProtocolVersion.
	Endpoint h248.EndpointConfig
	// LocateController returns the address of the controller to
	// register with. It is called before each registration that does
	// not resume an association, so that a name looked up in the DNS
	// may lead to another controller each time. The port is usually
	// h248.DefaultPort.
	LocateController func() (net.Addr, error)
	// Relay carries out the commands of Iq.
	Relay Relay
	// Processor, set in place of Relay, carries out the commands of Mp.
	// The Gateway then registers with MpProfile.
	Processor Processor
	// MaxWaitingDelay is the "maximum waiting delay" (MWD) of H.248.1
	// (05/2002) 9.2: before it registers, and after a registration
	// that failed, the gateway waits a random time between zero and
	// MWD, so that many gateways that start together do not all
	// register at once. The recommendation suggests far less than the
	// 600 s of a residential gateway for gateways with many
	// terminations; zero means 5 s, which keeps the start of the
	// network short.
	MaxWaitingDelay time.Duration
	// TimerX is the heartbeat period of hangterm/thb when the
	// controller sets the event without timerx. H.248.36 (03/2013)
	// 5.2.1.1.1 leaves the default to provisioning and asks for a
	// multiple of the call holding time. Zero means 30 minutes, ten
	// times the average call of 3 minutes that H.248.1 (05/2002) 9.2
	// reckons with.
	TimerX time.Duration
	// FirstContextID is the ID of the first context. Zero picks one at
	// random, so that the IDs after a restart do not repeat those that
	// the controller may still hold. The tests set it to get IDs they
	// can predict.
	FirstContextID uint32
}

// NewGateway makes a Gateway that sends and receives on conn. Call
// Serve to start it.
func NewGateway(conn net.PacketConn, config GatewayConfig) (*Gateway, error) {
	if config.Endpoint.Handler != nil {
		return nil, errors.New("mediactl: the Gateway handles the requests; leave Endpoint.Handler nil")
	}
	if (config.Relay == nil) == (config.Processor == nil) || config.LocateController == nil {
		return nil, errors.New("mediactl: a Gateway needs either a Relay or a Processor, and a way to locate its controller")
	}
	g := &Gateway{
		profile:   iqProfile,
		relay:     config.Relay,
		processor: config.Processor,
		clock:     config.Endpoint.Clock,
		log:       config.Endpoint.Logger,
		locate:    config.LocateController,
		mwd:       config.MaxWaitingDelay,
		timerX:    config.TimerX,
		lost:      make(chan struct{}, 1),
	}
	if g.processor != nil {
		g.profile = mpProfile
	}
	if g.clock == nil {
		g.clock = h248.SystemClock{}
	}
	if g.log == nil {
		g.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if g.mwd == 0 {
		g.mwd = 5 * time.Second
	}
	if g.timerX == 0 {
		g.timerX = 30 * time.Minute
	}
	first := config.FirstContextID
	for !validContextID(h248.ContextID(first)) {
		first = rand.Uint32()
	}
	g.nextContextID.Store(first - 1)

	endpointConfig := config.Endpoint
	endpointConfig.Version = ProtocolVersion
	endpointConfig.Handler = g.handle
	endpoint, err := h248.NewEndpoint(conn, endpointConfig)
	if err != nil {
		return nil, err
	}
	g.endpoint = endpoint
	g.ctx, g.cancel = context.WithCancel(context.Background())
	return g, nil
}

// validContextID tells whether id can name a context: it is none of the
// three values that H.248.1 (05/2002) 8.1.2 sets apart.
func validContextID(id h248.ContextID) bool {
	return id != h248.ContextNull && id != h248.ContextChoose && id != h248.ContextAll
}

// Serve reads messages and keeps the gateway registered until Close.
func (g *Gateway) Serve() error {
	served := make(chan error, 1)
	go func() { served <- g.endpoint.Serve() }()
	g.keepRegistered()
	return <-served
}

// Close stops the gateway. The contexts of the Relay stay as they are.
func (g *Gateway) Close() error {
	g.cancel()
	g.mu.Lock()
	g.inactivity.stop()
	g.mu.Unlock()
	return g.endpoint.Close()
}

// keepRegistered registers the gateway, and registers it again
// whenever the association fails, until the gateway is closed.
//
// The first registration is IMS-AGW Register (TS 29.334 5.17.3.5):
// ServiceChange Restart with 901 Cold Boot, since the relay has no
// contexts when the gateway starts (H.248.8 (03/2013) 5.2.2). After a
// failure, the gateway contacts the controller it had with Disconnected
// and 900 Service Restored, IMS-AGW Communication Up (TS 29.334
// 5.17.3.3, H.248.1 (05/2002) 11.5); its contexts are still there.
// A controller that does not accept the gateway may name another in
// the ServiceChangeMgcId of its reply, the Alternate MGC Id of TS
// 29.334 5.17.3.3 to 5.17.3.6. The gateway then sends the same
// ServiceChange there, and goes on so until a controller accepts it or
// one does not answer (H.248.1 (05/2002) 11.2). Every attempt that gets
// no acceptance is followed by a random wait (H.248.1 (05/2002) 9.2,
// 11.2).
//
// SIMPLIFIED: H.248.1 (05/2002) 11.2 and 11.5 have a gateway try an
// ordered list of secondary controllers, provisioned with it, when its
// controller does not answer. The list is optional in 11.2. This
// gateway is provisioned with one controller, as LocateController finds
// it, and after a failure starts again from there.
func (g *Gateway) keepRegistered() {
	method, reason := h248.Restart, "901 Cold Boot"
	var controller net.Addr
	wait := g.randomWait()
	for {
		if !g.sleep(wait) {
			return
		}
		wait = g.randomWait()
		if method == h248.Restart {
			addr, err := g.locate()
			if err != nil {
				g.log.Warn("mediactl: cannot locate the controller", "error", err)
				continue
			}
			controller = addr
		}
		accepted, next := g.register(controller, method, reason)
		for !accepted && next != nil {
			controller = next
			accepted, next = g.register(controller, method, reason)
		}
		if !accepted {
			continue
		}
		select {
		case <-g.lost:
		case <-g.ctx.Done():
			return
		}
		method, reason = h248.Disconnected, "900 Service Restored"
		wait = 0
	}
}

func (g *Gateway) randomWait() time.Duration {
	return rand.N(g.mwd + 1)
}

// sleep waits for d on the clock of the gateway. It reports false when
// the gateway was closed first.
func (g *Gateway) sleep(d time.Duration) bool {
	if d <= 0 {
		return g.ctx.Err() == nil
	}
	woken := make(chan struct{})
	t := g.clock.AfterFunc(d, func() { close(woken) })
	select {
	case <-woken:
		return true
	case <-g.ctx.Done():
		t.Stop()
		return false
	}
}

// register sends one ServiceChange on ROOT and reports whether the
// controller accepted it. A controller that sends the gateway on to
// another gives its address in next.
func (g *Gateway) register(controller net.Addr, method h248.ServiceChangeMethod, reason string) (accepted bool, next net.Addr) {
	parm := &h248.ServiceChangeParm{Method: method, Reason: reason}
	if method == h248.Restart {
		// TS 29.334 5.17.3.5 (Table 5.17.3.5.1) and 5.8.8 (Table
		// 5.8.8.7): the profile and the version go with a
		// registration; H.248.1 (05/2002) 7.2.8 adds the time stamp.
		profile := g.profile.name
		parm.Profile = &profile
		parm.Version = ProtocolVersion
		parm.TimeStamp = timeStamp(g.clock.Now())
	}
	g.log.Info("mediactl: registering", "controller", controller, "method", method, "reason", reason)
	reply, err := g.endpoint.Request(g.ctx, controller, []h248.ActionRequest{{
		ContextID: h248.ContextNull,
		Commands: []h248.CommandRequest{{
			Command:       h248.ServiceChange,
			TerminationID: h248.TerminationRoot,
			ServiceChange: parm,
		}},
	}})
	if err != nil {
		g.log.Warn("mediactl: the controller did not answer the ServiceChange", "controller", controller, "error", err)
		return false, nil
	}
	if err := rejection(reply); err != nil {
		g.log.Warn("mediactl: the controller refused the ServiceChange", "controller", controller, "error", err)
		return false, nil
	}
	if result := serviceChangeResult(reply); result != nil {
		switch {
		case result.MgcID != nil:
			// H.248.1 (05/2002) 11.2: a reply with ServiceChangeMgcId
			// does not accept the gateway, and names the controller to
			// contact instead.
			addr, err := g.controllerAddress(*result.MgcID)
			if err != nil {
				g.log.Warn("mediactl: cannot reach the controller that the reply names", "controller", controller, "error", err)
				return false, nil
			}
			g.log.Info("mediactl: the controller sends the gateway to another controller", "controller", controller, "next", addr)
			return false, addr
		case result.Profile != nil && !sameProfile(*result.Profile, g.profile.name):
			// H.248.1 (05/2002) 7.2.8: a profile in the reply is the
			// one the controller supports instead.
			g.log.Warn("mediactl: the controller supports another profile", "controller", controller,
				"profile", fmt.Sprintf("%s/%d", result.Profile.ProfileName, result.Profile.Version))
			return false, nil
		case result.Version != 0 && result.Version != ProtocolVersion:
			// H.248.1 (05/2002) 11.3: the gateway cannot use a lower
			// version than the profile requires.
			g.log.Warn("mediactl: the controller offers another version", "controller", controller, "version", result.Version)
			return false, nil
		}
	}
	g.mu.Lock()
	g.registered = true
	g.controller = controller
	g.mu.Unlock()
	g.log.Info("mediactl: registered", "controller", controller, "method", method)
	return true, nil
}

// controllerAddress is the UDP address of the controller that an MId
// names, at the default port when the MId has none (H.248.1 (05/2002)
// clause 9). A domain name is looked up in the DNS. A device name gives
// no address.
func (g *Gateway) controllerAddress(id h248.MId) (net.Addr, error) {
	port := id.PortNumber
	if port == 0 {
		port = h248.DefaultPort
	}
	addr := id.DomainAddress
	switch {
	case addr.IsValid():
	case id.DomainName != "":
		addrs, err := net.DefaultResolver.LookupNetIP(g.ctx, "ip", id.DomainName)
		if err != nil {
			return nil, err
		}
		addr = addrs[0]
	default:
		return nil, fmt.Errorf("the device name %q is not an address", id.DeviceName)
	}
	return net.UDPAddrFromAddrPort(netip.AddrPortFrom(addr.Unmap(), port)), nil
}

func sameProfile(a, b h248.ServiceChangeProfile) bool {
	return strings.EqualFold(a.ProfileName, b.ProfileName) && a.Version == b.Version
}

// serviceChangeResult returns the result parameters of the reply to a
// ServiceChange, or nil.
func serviceChangeResult(reply h248.TransactionReply) *h248.ServiceChangeResParm {
	for _, a := range reply.Actions {
		for _, c := range a.Commands {
			if c.Command == h248.ServiceChange {
				return c.ServiceChangeResult
			}
		}
	}
	return nil
}

// loseAssociation ends the association with the controller, so that
// the gateway registers again.
func (g *Gateway) loseAssociation(why string) {
	g.mu.Lock()
	was := g.registered
	g.registered = false
	g.inactivity.stop()
	g.inactivity = inactivityTimer{}
	g.mu.Unlock()
	if !was {
		return
	}
	g.log.Warn("mediactl: lost the association with the controller", "reason", why)
	select {
	case g.lost <- struct{}{}:
	default:
	}
}

// request sends a request to the controller. A reply is a message from
// the controller, so it restarts the inactivity timer.
func (g *Gateway) request(actions []h248.ActionRequest) (h248.TransactionReply, error) {
	g.mu.Lock()
	controller, registered := g.controller, g.registered
	g.mu.Unlock()
	if !registered {
		return h248.TransactionReply{}, ErrUnavailable
	}
	reply, err := g.endpoint.Request(g.ctx, controller, actions)
	if errors.Is(err, h248.ErrNoReply) {
		// H.248.1 (05/2002) Annex D.1.5 and H.248.14 (03/2009)
		// 6.6.4: a request without an answer means that the
		// controller has failed.
		g.loseAssociation("no answer to a request")
	}
	if err == nil {
		g.heardFromController()
	}
	return reply, err
}

// heardFromController restarts the inactivity timer, when it is set
// (H.248.14 (03/2009) 6.6.4).
func (g *Gateway) heardFromController() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inactivity.restart(g.clock, func() { go g.inactivityTimeout() })
}

func (t *inactivityTimer) stop() {
	if t.timer != nil {
		t.timer.Stop()
	}
}

func (t *inactivityTimer) restart(clock h248.Clock, f func()) {
	t.stop()
	if t.mit > 0 {
		t.timer = clock.AfterFunc(t.mit, f)
	}
}

// inactivityTimeout is Inactivity Timeout - Indication (TS 29.334
// 5.17.3.16): the gateway has not heard from the controller for mit,
// and asks whether it is still there.
func (g *Gateway) inactivityTimeout() {
	g.mu.Lock()
	requestID, set := g.inactivity.requestID, g.inactivity.mit > 0
	g.mu.Unlock()
	if !set {
		return
	}
	reply, err := g.request([]h248.ActionRequest{{
		ContextID: h248.ContextNull,
		Commands:  []h248.CommandRequest{notifyRequest(h248.TerminationRoot, requestID, eventInactivityTimeout)},
	}})
	if err != nil {
		return
	}
	if err := rejection(reply); err != nil {
		// A controller that has restarted does not know the
		// association; see Controller.notify.
		g.loseAssociation(err.Error())
	}
}

func notifyRequest(t h248.TerminationID, requestID uint32, event h248.PkgdName) h248.CommandRequest {
	return h248.CommandRequest{
		Command:       h248.Notify,
		TerminationID: t,
		ObservedEvents: &h248.ObservedEventsDescriptor{
			RequestID:         requestID,
			ObservedEventList: []h248.ObservedEvent{{EventName: event}},
		},
	}
}
