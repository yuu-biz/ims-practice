package mediactl

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// stream is what the Media descriptor of an Add or a Modify asks of the
// single audio stream of a termination.
type stream struct {
	// local is the Local descriptor, or an audio stream with no format
	// when the command has none.
	local    sessionDescription
	hasLocal bool
	// remote is the Remote descriptor, or nil.
	remote *sessionDescription
	// mode is the stream mode that the LocalControl descriptor sets, or
	// empty when it sets none.
	mode h248.StreamMode
}

// checkStream reads the Media descriptor of an Add (add is true) or a
// Modify, and returns the error code for what this gateway does not
// support.
func checkStream(m *h248.MediaDescriptor, add bool) (stream, *h248.ErrorDescriptor) {
	return readStream(m, add, checkLocalControl)
}

// readStream reads the Media descriptor of an Add or a Modify, and
// checks its LocalControl descriptor with localControl, which differs
// between the profiles.
func readStream(m *h248.MediaDescriptor, add bool, localControl func(lc *h248.LocalControlDescriptor, add bool) *h248.ErrorDescriptor) (stream, *h248.ErrorDescriptor) {
	s := stream{local: sessionDescription{mediaType: mediaAudio, proto: protoRTPAVP, chooseAddress: true, choosePort: true}}
	var parms h248.StreamParms
	switch {
	case m == nil:
	case m.TerminationState != nil:
		// TS 29.334 5.7.1, Table 5.7.1.1: the ServiceState is used on
		// ROOT only.
		return s, errorDescriptor(501, "TerminationState on a termination other than ROOT")
	case m.OneStream != nil:
		parms = *m.OneStream
	case len(m.MultiStream) == 1 && m.MultiStream[0].StreamID == 1:
		parms = m.MultiStream[0].StreamParms
	case len(m.MultiStream) > 0:
		// The relay carries one audio stream for each termination.
		return s, errorDescriptor(515, fmt.Sprintf("stream %d", m.MultiStream[len(m.MultiStream)-1].StreamID))
	}
	if e := localControl(parms.LocalControl, add); e != nil {
		return s, e
	}
	if parms.LocalControl != nil {
		s.mode = parms.LocalControl.StreamMode
	}
	if parms.Local != nil {
		local, e := checkSessionDescription(parms.Local.SDP)
		if e != nil {
			return s, e
		}
		if !local.chooseAddress || !local.choosePort {
			return s, errorDescriptor(501, "the relay chooses the local address and port")
		}
		s.local, s.hasLocal = local, true
	}
	if parms.Remote != nil {
		remote, e := checkSessionDescription(parms.Remote.SDP)
		if e != nil {
			return s, e
		}
		if remote.chooseAddress || remote.choosePort || !remote.address.Addr().IsValid() {
			// H.248.8 (03/2013) 4.2.19: the Remote descriptor lacks
			// what the gateway needs to send.
			return s, errorDescriptor(441, "the Remote descriptor needs an address and a port")
		}
		s.remote = &remote
	}
	return s, nil
}

// checkLocalControl accepts the stream mode SendReceive only: the relay
// passes media both ways. The mode is Inactive unless a LocalControl
// descriptor sets it (H.248.1 (05/2002) 7.1.7), so an Add needs one.
// ReserveValue and ReserveGroup are accepted: the relay reserves the
// same ports whatever formats are offered.
func checkLocalControl(lc *h248.LocalControlDescriptor, add bool) *h248.ErrorDescriptor {
	mode := h248.StreamMode("")
	if lc != nil {
		mode = lc.StreamMode
	}
	if mode == "" && add {
		mode = h248.Inactive
	}
	if mode != "" && mode != h248.SendRecv {
		// H.248.8 (03/2013) 4.2.55.
		return errorDescriptor(517, fmt.Sprintf("stream 1 mode %s", mode))
	}
	if lc != nil {
		for _, p := range lc.PropertyParms {
			return unsupportedItem(Packages, p.Name, 450)
		}
	}
	return nil
}

func checkSessionDescription(text string) (sessionDescription, *h248.ErrorDescriptor) {
	d, err := parseSDP(text)
	switch {
	case err == errAlternatives:
		return d, errorDescriptor(501, err.Error())
	case err != nil:
		return d, errorDescriptor(449, err.Error())
	case d.mediaType != mediaAudio:
		// TS 29.334 5.15, Table 5.15.1.
		return d, errorDescriptor(515, "stream 1 media "+d.mediaType)
	case d.proto != protoRTPAVP:
		// TS 29.334 5.15, Table 5.15.2.
		return d, errorDescriptor(449, "m= proto "+d.proto)
	}
	return d, nil
}

// unsupportedItem is the error for a package item that this gateway
// does not realize: 440 when the package is not one of packages
// (H.248.8 (03/2013) 4.2.18), and otherwise code, which says that the
// package has no such property, event or signal.
func unsupportedItem(packages h248.PackagesDescriptor, name h248.PkgdName, code int) *h248.ErrorDescriptor {
	pkg, _, _ := strings.Cut(strings.ToLower(string(name)), "/")
	if !slices.ContainsFunc(packages, func(p h248.PackagesItem) bool { return p.PackageName == pkg }) {
		return errorDescriptor(440, pkg)
	}
	return errorDescriptor(code, string(name))
}

// terminationEvents is what an Events descriptor of a termination asks
// for.
type terminationEvents struct {
	requestID uint32
	// heartbeat is timer X of hangterm/thb, or zero when the event is
	// not set.
	heartbeat time.Duration
}

// checkTerminationEvents reads the Events descriptor of an Add or a
// Modify. The events of a termination are g/cause and hangterm/thb (TS
// 29.334 5.7.3, Table 5.7.3.1).
//
// SIMPLIFIED: TS 29.334 5.17.2.7 has the gateway report with g/cause
// a bearer that was released. The event is accepted and never
// reported: the relay keeps the ports of a context until Subtract, so
// no bearer is released before it.
func (g *Gateway) checkTerminationEvents(e *h248.EventsDescriptor) (terminationEvents, *h248.ErrorDescriptor) {
	var events terminationEvents
	if e == nil {
		return events, nil
	}
	events.requestID = e.RequestID
	for _, ev := range e.EventList {
		switch h248.PkgdName(strings.ToLower(string(ev.PkgdName))) {
		case eventHeartbeat:
			var e *h248.ErrorDescriptor
			if events.heartbeat, e = g.heartbeatPeriod(ev); e != nil {
				return events, e
			}
		case eventCause:
			// H.248.1 (05/2002) E.1.2: the event has no parameters in
			// an Events descriptor.
			if len(ev.EvParList) > 0 {
				return events, errorDescriptor(446, ev.EvParList[0].Name)
			}
		case eventInactivityTimeout:
			// H.248.14 (03/2009) 6: the package is used on ROOT only.
			return events, errorDescriptor(501, string(ev.PkgdName)+" on a termination other than ROOT")
		default:
			return events, unsupportedItem(Packages, ev.PkgdName, 451)
		}
	}
	return events, nil
}

// heartbeatPeriod is timer X of the event hangterm/thb: its parameter
// timerx in seconds, or TimerX of the gateway without one. 0 sends no
// heartbeat (H.248.36 (03/2013) 5.2.1.1.1).
func (g *Gateway) heartbeatPeriod(ev h248.RequestedEvent) (time.Duration, *h248.ErrorDescriptor) {
	period := g.timerX
	for _, p := range ev.EvParList {
		if !strings.EqualFold(p.Name, paramTimerX) {
			return 0, errorDescriptor(446, p.Name)
		}
		seconds, err := strconv.ParseUint(p.Value.Text, 10, 32)
		if err != nil || p.Value.Quoted || p.Sublist != nil {
			return 0, errorDescriptor(449, paramTimerX+" "+p.Value.Text)
		}
		period = time.Duration(seconds) * time.Second
	}
	return period, nil
}

// checkSignals reads the Signals descriptor of an Add (add is true) or
// a Modify, and returns the latching that it asks of the relay. The one
// signal of a termination is ipnapt/latch (TS 29.334 5.7.5, Table
// 5.7.5.1; H.248.37 (06/2008) 6.3.1), which the controller sends where
// it needs latching (TS 29.334 5.17.2.2 to 5.17.2.4), with any value of
// napt (Table 5.14.3.12.1). Without the signal a new termination sends
// to the address of its Remote descriptor (6.6.1). On Modify, no Signals
// descriptor keeps the latching, and one without the signal stops it
// and keeps the address latched (6.6.4).
func checkSignals(s *h248.SignalsDescriptor, add bool) (agw.Latch, *h248.ErrorDescriptor) {
	switch {
	case s == nil && add:
		return agw.NAPTOff, nil
	case s == nil:
		return "", nil
	case len(s.SeqSigLists) > 0:
		// TS 29.334 5.7.5, Table 5.7.5.2.
		return "", errorDescriptor(501, "signal lists")
	}
	latch := agw.StopLatching
	if add {
		latch = agw.NAPTOff
	}
	for _, sig := range s.Signals {
		if h248.PkgdName(strings.ToLower(string(sig.SignalName))) != signalLatch {
			return "", unsupportedItem(Packages, sig.SignalName, 452)
		}
		napt := ""
		for _, p := range sig.SigParList {
			if !strings.EqualFold(p.Name, paramNAPT) {
				return "", errorDescriptor(446, p.Name)
			}
			napt = p.Value.Text
		}
		if napt == "" {
			// H.248.37 (06/2008) 6.3.1.1.1: the parameter is not
			// optional.
			return "", errorDescriptor(457, paramNAPT)
		}
		var ok bool
		if latch, ok = latchOf(napt); !ok {
			return "", errorDescriptor(449, paramNAPT+" "+napt)
		}
	}
	return latch, nil
}

// setHeartbeat sets the event hangterm/thb of a termination as the
// latest Events descriptor asks, replacing what was set before
// (H.248.1 (05/2002) 7.1.9).
func (g *Gateway) setHeartbeat(gc *gatewayContext, side agw.TerminationID, events terminationEvents) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if old := gc.heartbeat[side]; old != nil {
		old.timer.Stop()
		delete(gc.heartbeat, side)
	}
	if gc.released || events.heartbeat == 0 {
		return
	}
	gc.heartbeat[side] = &heartbeat{
		requestID: events.requestID,
		timerX:    events.heartbeat,
		timer:     g.clock.AfterFunc(events.heartbeat, func() { go g.sendHeartbeat(gc, side) }),
	}
}

// restartHeartbeat starts timer X again: any message about the
// termination resets it (H.248.36 (03/2013) 5.2.1.1.1).
func (g *Gateway) restartHeartbeat(gc *gatewayContext, side agw.TerminationID) {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	hb := gc.heartbeat[side]
	if gc.released || hb == nil {
		return
	}
	hb.timer.Stop()
	hb.timer = g.clock.AfterFunc(hb.timerX, func() { go g.sendHeartbeat(gc, side) })
}

// sendHeartbeat is Termination Heartbeat Indication (TS 29.334
// 5.17.2.6). The controller is to correct a mismatch it finds (H.248.36
// (03/2013) 5.6.2), so the gateway only reports the answer.
func (g *Gateway) sendHeartbeat(gc *gatewayContext, side agw.TerminationID) {
	gc.mu.Lock()
	hb := gc.heartbeat[side]
	gc.mu.Unlock()
	if hb == nil || g.ctx.Err() != nil {
		return
	}
	t := terminationID(gc.group, side, gc.id)
	reply, err := g.request([]h248.ActionRequest{{
		ContextID: gc.id,
		Commands:  []h248.CommandRequest{notifyRequest(t, hb.requestID, eventHeartbeat)},
	}})
	if err == nil {
		if err := rejection(reply); err != nil {
			g.log.Warn("mediactl: the controller has no record of the termination", "termination", string(t), "error", err)
		}
	}
	g.restartHeartbeat(gc, side)
}

// release releases the context in the relay and forgets it.
func (g *Gateway) release(ctx context.Context, gc *gatewayContext) error {
	gc.mu.Lock()
	if gc.released {
		gc.mu.Unlock()
		return agw.ErrNoContext
	}
	gc.released = true
	for side, hb := range gc.heartbeat {
		hb.timer.Stop()
		delete(gc.heartbeat, side)
	}
	gc.mu.Unlock()
	g.contexts.Delete(gc.id)
	if err := g.relay.Subtract(ctx, gc.relayID); err != nil {
		g.log.Warn("mediactl: the relay could not release a context", "context", gc.id.String(), "error", err)
		return err
	}
	g.log.Info("mediactl: context released", "context", gc.id.String(), "relay_context", gc.relayID)
	return nil
}

// rootAction carries out the commands on ROOT, in the NULL context.
func (g *Gateway) rootAction(t *transaction, a h248.ActionRequest) (h248.ActionReply, bool) {
	commands, ok := t.runCommands(a.ContextID, a.Commands, func(c h248.CommandRequest) []h248.CommandReply {
		if !strings.EqualFold(string(c.TerminationID), string(h248.TerminationRoot)) {
			// H.248.8 (03/2013) 4.2.12: the ephemeral terminations of
			// this gateway are never in the NULL context.
			return []h248.CommandReply{failed(c, 430, string(c.TerminationID))}
		}
		switch c.Command {
		case h248.AuditValue:
			return []h248.CommandReply{auditRoot(c, g.profile.packages)}
		case h248.Modify:
			return []h248.CommandReply{g.modifyRoot(c)}
		case h248.ServiceChange:
			return []h248.CommandReply{g.controllerServiceChange(c)}
		}
		return []h248.CommandReply{failed(c, 501, fmt.Sprintf("%s on ROOT", c.Command))}
	})
	return h248.ActionReply{ContextID: a.ContextID, Commands: commands}, ok
}

// auditRoot answers Audit Value on ROOT (TS 29.334 5.17.3.10): an
// empty Audit descriptor, which the controller sends to check that the
// gateway answers (TS 29.334 5.12, Table 5.12.3), the packages, and
// the service state.
//
// SIMPLIFIED: TS 29.334 5.8.5 also lets the controller audit the
// properties of ROOT (root/*). Their values are the timers of the
// Endpoint, which are provisioned (5.14.3.2) and not reported here.
func auditRoot(c h248.CommandRequest, packages h248.PackagesDescriptor) h248.CommandReply {
	reply := h248.CommandReply{Command: h248.AuditValue, TerminationID: c.TerminationID}
	for _, token := range c.Audit.AuditToken {
		if token != h248.PackagesToken {
			return failed(c, 501, "Audit of "+string(token)+" on ROOT")
		}
		reply.Packages = packages
	}
	for _, item := range c.Audit.AuditPropertyToken {
		if !item.ServiceStates {
			return failed(c, 501, "Audit of "+string(item.PkgdName))
		}
		reply.Media = &h248.MediaDescriptor{TerminationState: &h248.TerminationStateDescriptor{ServiceState: h248.ServiceStateInService}}
	}
	return reply
}

// modifyRoot carries out Inactivity Timeout - Activation (TS 29.334
// 5.17.3.15): the controller sets it/ito on ROOT with the longest time
// it lets pass without a message. An Events descriptor without the
// event stops the timer.
func (g *Gateway) modifyRoot(c h248.CommandRequest) h248.CommandReply {
	if c.Media != nil || c.Signals != nil || c.Audit != nil {
		return failed(c, 501, "Modify of ROOT with other than an Events descriptor")
	}
	if c.Events == nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID}
	}
	timer := inactivityTimer{requestID: c.Events.RequestID}
	for _, ev := range c.Events.EventList {
		if name := h248.PkgdName(strings.ToLower(string(ev.PkgdName))); name != eventInactivityTimeout {
			if name == eventHeartbeat || name == eventCause {
				// TS 29.334 5.7.3, Table 5.7.3.1: not on ROOT.
				return failed(c, 501, string(ev.PkgdName)+" on ROOT")
			}
			return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: unsupportedItem(g.profile.packages, ev.PkgdName, 451)}
		}
		for _, p := range ev.EvParList {
			if !strings.EqualFold(p.Name, paramMaximumInactivityTime) {
				return failed(c, 446, p.Name)
			}
			steps, err := strconv.ParseUint(p.Value.Text, 10, 16)
			if err != nil || p.Value.Quoted {
				return failed(c, 449, paramMaximumInactivityTime+" "+p.Value.Text)
			}
			// H.248.14 (03/2009) 6.2.1.1.1: steps of 10 ms; 0 turns
			// the timer off.
			timer.mit = time.Duration(steps) * 10 * time.Millisecond
		}
		if len(ev.EvParList) == 0 {
			// H.248.14 (03/2009) 6.2.1.1.1 lets mit be left out only
			// when a default is provisioned, and this gateway has
			// none.
			return failed(c, 457, paramMaximumInactivityTime)
		}
	}
	g.mu.Lock()
	g.inactivity.stop()
	g.inactivity = timer
	g.mu.Unlock()
	g.log.Info("mediactl: inactivity timer set", "mit", timer.mit)
	return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID}
}

// controllerServiceChange answers a ServiceChange that the controller
// sends: IMS-ALG Restoration (TS 29.334 5.17.3.8) and IMS-ALG Out of
// Service (5.17.3.9). After the latter the gateway registers again, as
// after a failure of the controller.
//
// SIMPLIFIED: IMS-ALG Ordered Re-register (TS 29.334 5.17.3.7), a
// Handoff that sends the gateway to another controller, is refused
// with error 501. The controllers of this project do not send it.
func (g *Gateway) controllerServiceChange(c h248.CommandRequest) h248.CommandReply {
	switch c.ServiceChange.Method {
	case h248.Restart:
		g.log.Info("mediactl: the controller has restarted", "reason", c.ServiceChange.Reason)
	case h248.Forced, h248.Graceful:
		go g.loseAssociation("the controller goes out of service: " + c.ServiceChange.Reason)
	default:
		return failed(c, 501, "ServiceChange "+string(c.ServiceChange.Method))
	}
	return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID}
}
