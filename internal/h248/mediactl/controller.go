package mediactl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// Controller is the controller side of Iq, the IMS-ALG in the P-CSCF.
// It implements Relay by sending the H.248 commands of TS 29.334
// 5.17.2 to the media gateways that have registered with it.
//
// Set up with MpProfile, it is the controller side of Mp instead, the
// MRFC: it implements Processor by sending the commands of TS 29.333
// 5.17.2 to the MRFPs that have registered with it, and hands the end
// of each announcement to ControllerConfig.Notify. One Controller
// speaks one profile, and accepts the gateways that name it.
//
// A Controller holds only its associations, one for each media gateway
// that registered with it; it keeps no record of the contexts. The ID
// of a Reservation names the context and the media gateway that holds
// it, as "<ContextID>@<MId>", so that every call for the context goes
// to that gateway. Several Controllers, one in each P-CSCF, can share
// nothing and run side by side; each sees the media gateways that
// registered with it, so that the P-CSCF can run as several instances.
type Controller struct {
	endpoint  *h248.Endpoint
	profile   profile
	clock     h248.Clock
	log       *slog.Logger
	keepAlive time.Duration
	mit       time.Duration
	timerX    time.Duration
	heartbeat func(ctx context.Context, id string) bool
	onNotify  func(Notification)

	ctx    context.Context
	cancel context.CancelFunc

	// associations holds an *association for each media gateway that
	// has registered, keyed by midKey of its MId.
	associations sync.Map
	// available lists the associations that take new contexts. It is
	// replaced as a whole when a gateway comes or goes, which is rare;
	// Reserve only reads it.
	available atomic.Pointer[[]*association]
	changing  sync.Mutex
	next      atomic.Uint64
}

// association is the control association with one media gateway, from
// its registration until it fails or the gateway registers again.
type association struct {
	key  string
	mid  h248.MId
	addr net.Addr
	// ctx ends with the association. The requests to the gateway run
	// under it, so that they fail at once when the association does.
	ctx    context.Context
	cancel context.CancelFunc

	mu sync.Mutex
	// activated is true once the gateway has accepted it/ito.
	activated bool
	// lastSent is when the controller last sent the gateway a
	// message. H.248.14 (03/2009) 6.6.2 has the controller keep the
	// gaps shorter than mit.
	lastSent time.Time
	timer    h248.Timer
}

// ControllerConfig sets up a Controller.
type ControllerConfig struct {
	// Profile is the profile that the Controller speaks: Profile, for
	// Iq, when left zero, or MpProfile.
	Profile h248.ServiceChangeProfile
	// Endpoint sets up the Endpoint that the Controller sends and
	// receives with. Its Handler must be nil: the Controller is the
	// handler. Its Version is replaced with ProtocolVersion.
	Endpoint h248.EndpointConfig
	// KeepAliveInterval is the longest time the controller lets pass
	// without a message to a gateway. When it has nothing else to
	// send, it sends AuditValue on ROOT with an empty Audit
	// descriptor, which also tells it whether the gateway still
	// answers (TS 29.334 5.12, Table 5.12.3; H.248.14 (03/2009) 6.6.2,
	// 6.6.3). Zero means 10 s.
	KeepAliveInterval time.Duration
	// InactivityTimer is the "maximum inactivity time" (mit) that the
	// controller sets on the it/ito event of each gateway (TS 29.334
	// 5.17.3.15). It must be longer than KeepAliveInterval. Zero means
	// 27.5 s: H.248.14 (03/2009) 6.6.5.1 suggests 110 % of T-MAX, and
	// the default T-MAX of h248.Timers is 25 s.
	InactivityTimer time.Duration
	// TimerX is the heartbeat period that the controller sets on the
	// hangterm/thb event of each termination (TS 29.334 5.17.2.2).
	// Zero means 30 minutes (see GatewayConfig.TimerX).
	TimerX time.Duration
	// TerminationHeartbeat tells whether the P-CSCF still uses the
	// context id, when a gateway reports the heartbeat of one of its
	// terminations (TS 29.334 5.17.2.6). When it returns false, the
	// controller answers with error 411 and releases the context, as
	// H.248.36 (03/2013) 5.6.2 asks of it.
	//
	// SIMPLIFIED: TS 29.334 5.17.2.6. A nil TerminationHeartbeat takes
	// every context to be in use, so a context that the P-CSCF has lost
	// track of stays in the gateway.
	//
	// For Mp, the question is the same, for the context of the
	// termination that reported (TS 29.333 5.17.2.32); when the answer
	// is false, the controller subtracts that termination.
	TerminationHeartbeat func(ctx context.Context, id string) bool
	// Notify receives, for Mp, the end of each announcement whose
	// Events descriptor asked for SignalCompleted: Announcement
	// Completed (TS 29.333 5.17.2.11). It is called in the goroutine
	// that answers the Notify, which waits for it to return, so it is
	// to hand the Notification on and return at once.
	//
	// An MRFP sends its Notifies to the one controller it has
	// registered with (H.248.1 (05/2002) 7.2.7, 11.2), which is the
	// only one that can have sent it the Events descriptor. So the
	// Notification reaches the MRFC instance whose Controller set the
	// event, unless the MRFP has registered with another one since.
	Notify func(Notification)
}

// NewController makes a Controller that sends and receives on conn.
// Call Serve to start it.
func NewController(conn net.PacketConn, config ControllerConfig) (*Controller, error) {
	if config.Endpoint.Handler != nil {
		return nil, errors.New("mediactl: the Controller handles the requests; leave Endpoint.Handler nil")
	}
	c := &Controller{
		profile:   iqProfile,
		onNotify:  config.Notify,
		clock:     config.Endpoint.Clock,
		log:       config.Endpoint.Logger,
		keepAlive: config.KeepAliveInterval,
		mit:       config.InactivityTimer,
		timerX:    config.TimerX,
		heartbeat: config.TerminationHeartbeat,
	}
	switch config.Profile {
	case h248.ServiceChangeProfile{}, Profile:
	case MpProfile:
		c.profile = mpProfile
	default:
		return nil, fmt.Errorf("mediactl: no profile %s/%d", config.Profile.ProfileName, config.Profile.Version)
	}
	if c.clock == nil {
		c.clock = h248.SystemClock{}
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.keepAlive == 0 {
		c.keepAlive = 10 * time.Second
	}
	if c.mit == 0 {
		c.mit = 27500 * time.Millisecond
	}
	if c.timerX == 0 {
		c.timerX = 30 * time.Minute
	}
	if c.mit <= c.keepAlive {
		return nil, fmt.Errorf("mediactl: the inactivity timer %v must be longer than the keep-alive interval %v", c.mit, c.keepAlive)
	}
	c.available.Store(&[]*association{})
	endpointConfig := config.Endpoint
	endpointConfig.Version = ProtocolVersion
	endpointConfig.Handler = c.handle
	endpoint, err := h248.NewEndpoint(conn, endpointConfig)
	if err != nil {
		return nil, err
	}
	c.endpoint = endpoint
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Serve reads messages until Close.
func (c *Controller) Serve() error { return c.endpoint.Serve() }

// Close ends every association and stops the controller.
func (c *Controller) Close() error {
	c.cancel()
	c.associations.Range(func(_, v any) bool {
		v.(*association).end()
		return true
	})
	return c.endpoint.Close()
}

// midKey is the text of an MId with the case folded: MIds do not
// depend on case (H.248.1 Annex B.2).
func midKey(id h248.MId) string {
	var text string
	switch {
	case id.DomainName != "":
		text = strings.ToLower(id.DomainName)
	case id.DomainAddress.IsValid():
		text = "[" + id.DomainAddress.String() + "]"
	default:
		return strings.ToLower(id.DeviceName)
	}
	if id.PortNumber != 0 {
		text += ":" + strconv.Itoa(int(id.PortNumber))
	}
	return text
}

// contextHandle is the ID of a Reservation.
func contextHandle(id h248.ContextID, a *association) string {
	return fmt.Sprintf("%d@%s", id, a.key)
}

// lookup finds the association and the context that a Reservation ID
// names.
func (c *Controller) lookup(handle string) (*association, h248.ContextID, error) {
	number, key, ok := strings.Cut(handle, "@")
	id, err := strconv.ParseUint(number, 10, 32)
	if !ok || err != nil || !validContextID(h248.ContextID(id)) {
		return nil, 0, fmt.Errorf("mediactl: %q is not the ID of a context of a Controller", handle)
	}
	v, found := c.associations.Load(key)
	if !found || v.(*association).ctx.Err() != nil {
		return nil, 0, fmt.Errorf("%w: %s", ErrUnavailable, key)
	}
	return v.(*association), h248.ContextID(id), nil
}

// pick chooses the gateway for a new context, taking the available
// gateways in turn.
func (c *Controller) pick() *association {
	list := *c.available.Load()
	if len(list) == 0 {
		return nil
	}
	return list[c.next.Add(1)%uint64(len(list))]
}

// setAvailable adds a to the gateways that take new contexts, or takes
// it out, along with any earlier association of the same gateway.
func (c *Controller) setAvailable(a *association, available bool) {
	c.changing.Lock()
	defer c.changing.Unlock()
	list := slices.DeleteFunc(slices.Clone(*c.available.Load()), func(o *association) bool { return o.key == a.key })
	if available {
		list = append(list, a)
	}
	c.available.Store(&list)
}

// request sends a request to the gateway of a. A gateway that does not
// answer is taken to have failed (H.248.1 (05/2002) Annex D.1.5), and
// the association ends.
func (c *Controller) request(ctx context.Context, a *association, actions []h248.ActionRequest) (h248.TransactionReply, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(a.ctx, cancel)
	defer stop()
	a.sent(c.clock.Now())
	reply, err := c.endpoint.Request(ctx, a.addr, actions)
	switch {
	case err == nil:
		return reply, nil
	case errors.Is(err, h248.ErrNoReply):
		c.lose(a, "no answer")
		return reply, fmt.Errorf("%w: %s: %w", ErrUnavailable, a.key, err)
	case a.ctx.Err() != nil:
		return reply, fmt.Errorf("%w: %s: the association ended", ErrUnavailable, a.key)
	}
	return reply, err
}

func (a *association) sent(at time.Time) {
	a.mu.Lock()
	a.lastSent = at
	a.mu.Unlock()
}

func (a *association) end() {
	a.cancel()
	a.mu.Lock()
	if a.timer != nil {
		a.timer.Stop()
	}
	a.mu.Unlock()
}

// lose ends the association: the gateway takes no new contexts, and
// the calls for its contexts fail with ErrUnavailable until it
// registers again.
func (c *Controller) lose(a *association, why string) {
	if a.ctx.Err() != nil {
		return
	}
	a.end()
	c.setAvailable(a, false)
	c.log.Warn("mediactl: the media gateway is unavailable", "mid", a.key, "addr", a.addr.String(), "reason", why)
}

// register records the association that a ServiceChange from a gateway
// starts. An association that the gateway had before ends; its
// requests fail.
func (c *Controller) register(r *h248.IncomingRequest, method h248.ServiceChangeMethod) {
	a := &association{key: midKey(r.From), mid: r.From, addr: r.Addr}
	a.ctx, a.cancel = context.WithCancel(c.ctx)
	if old, loaded := c.associations.Swap(a.key, a); loaded {
		old.(*association).end()
	}
	c.setAvailable(a, true)
	c.log.Info("mediactl: media gateway registered", "mid", a.key, "addr", a.addr.String(), "method", method)
	// The first keep-alive sets it/ito.
	a.mu.Lock()
	a.timer = c.clock.AfterFunc(activationDelay, func() { go c.keepAliveGateway(a) })
	a.mu.Unlock()
}

// activationDelay is how long after a registration the controller sets
// it/ito, and how long it waits to try again when the gateway refused
// it. A gateway refuses every command with error 505 until the reply
// to its ServiceChange has arrived (H.248.1 (05/2002) 11.2), and a
// command sent at once could overtake the reply.
const activationDelay = time.Second

// keepAliveGateway sets it/ito on a newly registered gateway
// (Inactivity Timeout - Activation, TS 29.334 5.17.3.15), and then sends
// an empty AuditValue on ROOT whenever KeepAliveInterval has passed
// without a message to the gateway.
func (c *Controller) keepAliveGateway(a *association) {
	if a.ctx.Err() != nil {
		return
	}
	a.mu.Lock()
	activated, idle := a.activated, c.clock.Now().Sub(a.lastSent)
	a.mu.Unlock()
	next := c.keepAlive
	switch {
	case !activated:
		reply, err := c.request(a.ctx, a, []h248.ActionRequest{{
			ContextID: h248.ContextNull,
			Commands: []h248.CommandRequest{{
				Command:       h248.Modify,
				TerminationID: h248.TerminationRoot,
				Events: &h248.EventsDescriptor{RequestID: requestIDRoot, EventList: []h248.RequestedEvent{{
					PkgdName: eventInactivityTimeout,
					EvParList: []h248.EventParameter{{
						Name:  paramMaximumInactivityTime,
						Value: h248.Value{Text: strconv.FormatInt(int64(c.mit/(10*time.Millisecond)), 10)},
					}},
				}}},
			}},
		}})
		if err != nil {
			return
		}
		if err := rejection(reply); err != nil {
			c.log.Warn("mediactl: the media gateway refused the inactivity timer", "mid", a.key, "error", err)
			next = activationDelay
			break
		}
		a.mu.Lock()
		a.activated = true
		a.mu.Unlock()
	case idle < c.keepAlive:
		next = c.keepAlive - idle
	default:
		reply, err := c.request(a.ctx, a, []h248.ActionRequest{{
			ContextID: h248.ContextNull,
			Commands: []h248.CommandRequest{{
				Command:       h248.AuditValue,
				TerminationID: h248.TerminationRoot,
				Audit:         &h248.AuditDescriptor{},
			}},
		}})
		if err != nil {
			return
		}
		if err := rejection(reply); err != nil {
			c.log.Warn("mediactl: the media gateway refused the audit", "mid", a.key, "error", err)
		}
	}
	a.mu.Lock()
	if a.ctx.Err() == nil {
		a.timer = c.clock.AfterFunc(next, func() { go c.keepAliveGateway(a) })
	}
	a.mu.Unlock()
}

// handle carries out a transaction from a gateway: its ServiceChanges
// and its Notifies.
func (c *Controller) handle(ctx context.Context, r *h248.IncomingRequest) h248.TransactionReply {
	t := &transaction{ctx: ctx, request: r}
	for _, act := range r.Actions {
		commands, ok := t.runCommands(act.ContextID, act.Commands, func(cmd h248.CommandRequest) []h248.CommandReply {
			switch cmd.Command {
			case h248.ServiceChange:
				return []h248.CommandReply{c.serviceChange(r, act.ContextID, cmd)}
			case h248.Notify:
				return []h248.CommandReply{c.notify(ctx, r, act.ContextID, cmd)}
			}
			return []h248.CommandReply{failed(cmd, 501, fmt.Sprintf("%s from a media gateway", cmd.Command))}
		})
		t.actions = append(t.actions, h248.ActionReply{ContextID: act.ContextID, Commands: commands})
		if !ok {
			// H.248.1 (05/2002) 8.2.2.
			break
		}
	}
	// The reply is a message to the gateway, which H.248.14 (03/2009)
	// 6.6.4 counts as a sign of life.
	if v, ok := c.associations.Load(midKey(r.From)); ok {
		v.(*association).sent(c.clock.Now())
	}
	return h248.TransactionReply{Actions: t.actions}
}

// terminationOutOfService handles a ServiceChange on terminations
// other than ROOT: Termination Out Of Service (TS 29.334 5.17.3.19),
// with the method Forced. H.248.1 (05/2002) 7.2.8 makes the controller
// responsible for clearing the context of a termination that was taken
// out of service by force, and asks for the termination to be
// subtracted at least. Once the report is acknowledged, the controller
// subtracts the context of Iq as a whole, as Release does, and the
// termination of Mp.
//
// SIMPLIFIED: TS 29.334 5.17.3.19 also allows ContextID ALL, and a
// wildcarded termination. The controller keeps no list of the contexts
// and terminations of a gateway, so it clears nothing for those, and
// for a wildcard on Mp; the caller learns of it when it next uses the
// context.
func (c *Controller) terminationOutOfService(r *h248.IncomingRequest, contextID h248.ContextID, cmd h248.CommandRequest) {
	key := midKey(r.From)
	c.log.Warn("mediactl: a termination went out of service", "mid", key, "context", contextID.String(),
		"termination", string(cmd.TerminationID), "method", cmd.ServiceChange.Method, "reason", cmd.ServiceChange.Reason)
	v, ok := c.associations.Load(key)
	if !ok || cmd.ServiceChange.Method != h248.Forced || !validContextID(contextID) {
		return
	}
	id := contextHandle(contextID, v.(*association))
	switch {
	case !c.profile.mp():
		go c.Release(c.ctx, id)
	case !strings.ContainsAny(string(cmd.TerminationID), "*$"):
		go c.SubtractTermination(c.ctx, id, TerminationID(cmd.TerminationID))
	}
}

// serviceChange answers a ServiceChange from a gateway (TS 29.334
// 5.17.3.2 to 5.17.3.6).
func (c *Controller) serviceChange(r *h248.IncomingRequest, contextID h248.ContextID, cmd h248.CommandRequest) h248.CommandReply {
	ack := h248.CommandReply{Command: cmd.Command, TerminationID: cmd.TerminationID}
	if contextID != h248.ContextNull || !strings.EqualFold(string(cmd.TerminationID), string(h248.TerminationRoot)) {
		c.terminationOutOfService(r, contextID, cmd)
		return ack
	}
	p := cmd.ServiceChange
	key := midKey(r.From)
	switch p.Method {
	case h248.Restart, h248.HandOff:
		// IMS-AGW Register, Restoration and Re-Register (TS 29.334
		// 5.17.3.4 to 5.17.3.6).
		if p.Profile == nil || !sameProfile(*p.Profile, c.profile.name) {
			// H.248.1 (05/2002) 7.2.8: the reply names the profile
			// that the controller supports instead.
			c.log.Warn("mediactl: refused a media gateway with another profile", "mid", key, "profile", p.Profile)
			profile := c.profile.name
			ack.ServiceChangeResult = &h248.ServiceChangeResParm{Profile: &profile}
			return ack
		}
		if p.Version != 0 && p.Version < ProtocolVersion {
			// H.248.1 (05/2002) 11.3, H.248.8 (03/2013) 4.2.5.
			return failed(cmd, 406, strconv.Itoa(p.Version))
		}
		c.register(r, p.Method)
		ack.ServiceChangeResult = &h248.ServiceChangeResParm{TimeStamp: timeStamp(c.clock.Now())}
		if p.Version > ProtocolVersion {
			// TS 29.334 5.17.3.5, Table 5.17.3.5.2, NOTE.
			ack.ServiceChangeResult.Version = ProtocolVersion
		}
	case h248.Disconnected:
		// IMS-AGW Communication Up (TS 29.334 5.17.3.3). The gateway
		// kept its contexts, so the IDs that the P-CSCF holds work
		// again. A controller that has restarted does not know the
		// gateway, and takes it as a new one: error 507 of H.248.8
		// (03/2013) 4.2.48 is not among the errors of TS 29.334
		// 5.7.10 (Table 5.7.10.1).
		//
		// SIMPLIFIED: H.248.1 (05/2002) 7.2.8 lets the controller
		// audit the gateway after a Disconnected, since its state may
		// have changed. This controller does not.
		c.register(r, p.Method)
	case h248.Forced:
		// IMS-AGW Out Of Service (TS 29.334 5.17.3.2): every context
		// of the gateway is lost (H.248.1 (05/2002) 7.2.8).
		if v, ok := c.associations.Load(key); ok {
			c.lose(v.(*association), "out of service: "+p.Reason)
		}
	case h248.Graceful:
		// The contexts stay; the gateway takes no new ones.
		if v, ok := c.associations.Load(key); ok {
			c.setAvailable(v.(*association), false)
			c.log.Warn("mediactl: the media gateway is going out of service", "mid", key, "reason", p.Reason)
		}
	default:
		return failed(cmd, 501, "ServiceChange "+string(p.Method))
	}
	return ack
}

// notify answers a Notify from a gateway: Termination Heartbeat
// Indication (TS 29.334 5.17.2.6), IP Bearer Released (5.17.2.7) and
// Inactivity Timeout - Indication (5.17.3.16).
//
// A gateway that the controller has no association with gets error 507
// "Unknown Control Association" (H.248.8 (03/2013) 4.2.48). The code is
// defined for the reply to a Disconnected, and is not among the errors
// of TS 29.334 5.7.10 (Table 5.7.10.1). It is sent here so that a
// gateway whose controller has restarted and forgotten it registers
// again: answered without an error, its it/ito would tell it that all
// is well.
func (c *Controller) notify(ctx context.Context, r *h248.IncomingRequest, contextID h248.ContextID, cmd h248.CommandRequest) h248.CommandReply {
	v, ok := c.associations.Load(midKey(r.From))
	if !ok || v.(*association).ctx.Err() != nil {
		return failed(cmd, 507, "")
	}
	a := v.(*association)
	// The events of a termination of Mp carry the RequestID that the
	// MRFC chose (see Events); those of Iq always carry
	// requestIDTermination.
	termination := cmd.ObservedEvents.RequestID == requestIDTermination || c.profile.mp()
	for _, ev := range cmd.ObservedEvents.ObservedEventList {
		name := h248.PkgdName(strings.ToLower(string(ev.EventName)))
		switch {
		case name == eventInactivityTimeout && cmd.ObservedEvents.RequestID == requestIDRoot:
			c.log.Info("mediactl: the media gateway checked the association", "mid", a.key)
		case name == eventSignalCompletion && c.profile.mp():
			c.signalCompletion(a, contextID, cmd, ev)
		case name == eventHeartbeat && termination:
			id := contextHandle(contextID, a)
			if c.heartbeat != nil && !c.heartbeat(ctx, id) {
				c.log.Warn("mediactl: releasing a context that is not in use", "context", id, "termination", string(cmd.TerminationID))
				if c.profile.mp() {
					go c.SubtractTermination(c.ctx, id, TerminationID(cmd.TerminationID))
				} else {
					go c.Release(c.ctx, id)
				}
				return failed(cmd, 411, "")
			}
		case name == eventCause && termination:
			// SIMPLIFIED: TS 29.334 5.17.2.7. The controller answers
			// IP Bearer Released as the clause asks (5.17.2.7.2), and
			// only logs it: Relay has no way to hand the event to its
			// caller, which owns the session. What the IMS-ALG then
			// does with the session is not part of the clause.
			c.log.Warn("mediactl: the media gateway released a bearer", "context", contextHandle(contextID, a),
				"termination", string(cmd.TerminationID))
		case name == eventInactivityTimeout || name == eventHeartbeat || name == eventCause:
			return failed(cmd, 458, strconv.FormatUint(uint64(cmd.ObservedEvents.RequestID), 10))
		default:
			return h248.CommandReply{Command: cmd.Command, TerminationID: cmd.TerminationID, Error: errorDescriptor(451, string(ev.EventName))}
		}
	}
	return h248.CommandReply{Command: cmd.Command, TerminationID: cmd.TerminationID}
}
