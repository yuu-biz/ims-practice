package mediactl

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// handle carries out a transaction from the controller.
func (g *Gateway) handle(ctx context.Context, r *h248.IncomingRequest) h248.TransactionReply {
	g.mu.Lock()
	registered := g.registered
	g.mu.Unlock()
	t := &transaction{ctx: ctx, request: r}
	for _, a := range r.Actions {
		if !registered {
			// H.248.1 (05/2002) 11.2, H.248.8 (03/2013) 4.2.46: no
			// command is carried out before the ServiceChange that
			// registers the gateway has been answered.
			t.actions = append(t.actions, h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(505, "")})
			break
		}
		reply, ok := g.action(t, a)
		t.actions = append(t.actions, reply)
		if !ok {
			// H.248.1 (05/2002) 8.2.2: processing ends at the first
			// error, unless it is that of an optional command.
			break
		}
	}
	if registered {
		g.heardFromController()
	}
	return h248.TransactionReply{Actions: t.actions}
}

// action carries out one action. ok is false when it ended with an
// error.
func (g *Gateway) action(t *transaction, a h248.ActionRequest) (reply h248.ActionReply, ok bool) {
	if g.processor != nil {
		return g.mpAction(t, a)
	}
	if a.ContextRequest != nil && len(a.ContextRequest.TopologyReq) > 0 {
		// TS 29.334 5.7.9: Topology serves PS to CS access transfer
		// only, which this gateway does not support.
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(522, "")}, false
	}
	// SIMPLIFIED: TS 29.334 5.5 and 5.17.2.2 let the controller mark a
	// context as an emergency call or give it a priority. The relay
	// treats every context alike, so the attributes are accepted and
	// have no effect.
	switch a.ContextID {
	case h248.ContextNull:
		return g.rootAction(t, a)
	case h248.ContextChoose:
		return g.reserve(t, a)
	case h248.ContextAll:
		// SIMPLIFIED: TS 29.334 5.17.2.5 and 5.17.3.10 allow ALL as
		// the context of a Subtract and an AuditValue. That would walk
		// every context of the gateway; the controllers of this
		// project name one context at a time.
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(501, "ContextID ALL")}, false
	}
	v, found := g.contexts.Load(a.ContextID)
	if !found {
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(411, "")}, false
	}
	gc := v.(*gatewayContext)
	commands, ok := t.runCommands(a.ContextID, a.Commands, func(c h248.CommandRequest) []h248.CommandReply {
		return g.contextCommand(t.ctx, gc, c)
	})
	return h248.ActionReply{ContextID: a.ContextID, Commands: commands}, ok
}

// transaction is the reply to a transaction from the peer, as far as
// it has been carried out.
type transaction struct {
	ctx     context.Context
	request *h248.IncomingRequest
	// actions are the replies to the actions that have been carried
	// out.
	actions []h248.ActionReply
}

// overflow returns the index of the first command reply of a, the reply
// to the action being carried out, that does not fit in the message
// after the replies of the actions before it, or -1 when they all fit.
func (t *transaction) overflow(a h248.ActionReply) int {
	for i := range a.Commands {
		reply := h248.ActionReply{ContextID: a.ContextID, Commands: a.Commands[:i+1]}
		if !t.request.Fits(h248.TransactionReply{Actions: append(slices.Clip(t.actions), reply)}) {
			return i
		}
	}
	return -1
}

// stopped reports whether the Endpoint has sent as many Pendings for
// the transaction as it may. The command being carried out then takes
// error 506, and nothing after it is carried out (H.248.1 (05/2002)
// 8.2.3, H.248.8 (03/2013) 4.2.47).
func (t *transaction) stopped() bool {
	return errors.Is(context.Cause(t.ctx), h248.ErrPendingLimitReached)
}

// errorCode is the code for a command that failed with code: 506 when
// the transaction was stopped, since the command was then cut short.
func (t *transaction) errorCode(code int) int {
	if t.stopped() {
		return 506
	}
	return code
}

// runCommands carries out the commands of an action one after the
// other, as H.248.1 (05/2002) clause 8 and 8.2.2, as Corrigendum 1
// (03/2004) words clause 8, ask. A command on a wildcard has a reply
// for each termination it matched (7.2.3), even when some of them
// failed. A command that fails for any of its terminations ends the
// transaction, unless it is optional (the "O-" flag); ok is false
// then. A reply that would not fit in the message takes error 533 and
// ends the transaction even after an optional command (H.248.8
// (03/2013) 4.2.66). So does error 506, which the command takes when
// the Endpoint stops the transaction after its Pendings (see
// stopped).
func (t *transaction) runCommands(contextID h248.ContextID, commands []h248.CommandRequest, run func(c h248.CommandRequest) []h248.CommandReply) (replies []h248.CommandReply, ok bool) {
	replies = make([]h248.CommandReply, 0, len(commands))
	for _, c := range commands {
		if t.stopped() {
			return append(replies, failed(c, 506, "")), false
		}
		before := len(replies)
		replies = append(replies, run(c)...)
		if t.stopped() && slices.ContainsFunc(replies[before:], func(r h248.CommandReply) bool { return r.Error != nil }) {
			for i := before; i < len(replies); i++ {
				if replies[i].Error != nil {
					replies[i].Error = errorDescriptor(506, "")
				}
			}
			return replies, false
		}
		if i := t.overflow(h248.ActionReply{ContextID: contextID, Commands: replies}); i >= 0 {
			return append(replies[:i], failed(h248.CommandRequest{Command: c.Command, TerminationID: replies[i].TerminationID}, 533, "")), false
		}
		if !c.Optional && slices.ContainsFunc(replies[before:], func(r h248.CommandReply) bool { return r.Error != nil }) {
			return replies, false
		}
	}
	return replies, true
}

// failed is the reply of Command Rejected (TS 29.334 5.17.3.11).
func failed(c h248.CommandRequest, code int, detail string) h248.CommandReply {
	return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: errorDescriptor(code, detail)}
}

// reserveAdd is one Add of the action that reserves a context.
type reserveAdd struct {
	request h248.CommandRequest
	group   uint16
	side    agw.TerminationID
	stream  stream
	events  terminationEvents
	latch   agw.Latch
}

// reserve carries out Reserve AGW Connection Point (TS 29.334
// 5.17.2.2), or Reserve and Configure AGW Connection Point (5.17.2.4)
// when an Add carries a Remote descriptor.
//
// SIMPLIFIED: H.248.1 lets a controller add the terminations of a
// context one at a time. The relay makes a context with both of its
// terminations at once, so this gateway asks for both Adds, one for
// the access and one for the core termination, in the action that
// creates the context. A later Add to the context is refused with
// error 434, since the context holds the two terminations it can hold.
func (g *Gateway) reserve(t *transaction, a h248.ActionRequest) (h248.ActionReply, bool) {
	ctx := t.ctx
	refuse := func(c h248.CommandRequest, code int, detail string) (h248.ActionReply, bool) {
		return h248.ActionReply{ContextID: a.ContextID, Commands: []h248.CommandReply{failed(c, code, detail)}}, false
	}
	if t.stopped() && len(a.Commands) > 0 {
		return refuse(a.Commands[0], 506, "")
	}
	var adds []reserveAdd
	for _, c := range a.Commands {
		add, e := g.checkReserveAdd(c, adds)
		if e != nil {
			return refuse(c, e.ErrorCode, e.ErrorText)
		}
		adds = append(adds, add)
	}
	if len(adds) != 2 {
		c := h248.CommandRequest{Command: h248.Add, TerminationID: h248.TerminationChoose}
		if len(a.Commands) > 0 {
			c = a.Commands[0]
		}
		return refuse(c, 501, "the gateway reserves the access and the core termination together")
	}

	var requests []agw.TerminationRequest
	for _, add := range adds {
		mode, _ := modeOf(add.stream.mode)
		request := agw.TerminationRequest{ID: add.side, Mode: mode, LocalMedia: add.stream.local.media, Latch: add.latch}
		if add.stream.remote != nil {
			request.Remote, request.RemoteMedia = add.stream.remote.address, add.stream.remote.media
		}
		requests = append(requests, request)
	}
	rc, err := g.relay.Add(ctx, requests...)
	if err == nil && (rc.Terminations[agw.Access] == nil || rc.Terminations[agw.Core] == nil) {
		err = errors.New("the relay did not make both terminations")
	}
	if err != nil {
		g.log.Warn("mediactl: the relay could not reserve a context", "error", err)
		return refuse(adds[0].request, t.errorCode(relayErrorCode(err)), "")
	}
	gc := &gatewayContext{
		relayID:   rc.ID,
		group:     adds[0].group,
		heartbeat: map[agw.TerminationID]*heartbeat{},
	}
	g.storeContext(gc)
	g.log.Info("mediactl: context reserved", "context", gc.id.String(), "relay_context", gc.relayID)

	reply := h248.ActionReply{ContextID: gc.id}
	for _, add := range adds {
		g.setHeartbeat(gc, add.side, add.events)
		reply.Commands = append(reply.Commands, h248.CommandReply{
			Command:       h248.Add,
			TerminationID: terminationID(add.group, add.side, gc.id),
			Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
				Local: &h248.LocalRemoteDescriptor{SDP: gatewayLocal(add.stream.local, rc.Terminations[add.side].Local, gc.id)},
			}},
		})
	}
	if i := t.overflow(reply); i >= 0 {
		// H.248.8 (03/2013) 4.2.66, and H.248.1 (05/2002) clause 8,
		// which asks a gateway to undo, as far as it can, a command
		// that failed. The relay made both terminations at once, so
		// the context goes as a whole.
		g.release(context.WithoutCancel(ctx), gc)
		return refuse(adds[i].request, 533, "")
	}
	return reply, true
}

func (g *Gateway) checkReserveAdd(c h248.CommandRequest, before []reserveAdd) (reserveAdd, *h248.ErrorDescriptor) {
	if c.Command != h248.Add {
		return reserveAdd{}, errorDescriptor(501, fmt.Sprintf("%s in a context to be created", c.Command))
	}
	t, ok := parseTerminationID(c.TerminationID)
	if !ok || (t.side != agw.Access && t.side != agw.Core) {
		return reserveAdd{}, errorDescriptor(431, "the gateway has ip/<group>/access/$ and ip/<group>/core/$")
	}
	if t.idText != choose {
		// TS 29.334 5.6.1.1.1, Table 5.6.1.1.1.1, NOTE 4.
		return reserveAdd{}, errorDescriptor(501, "Add takes CHOOSE as the termination ID")
	}
	if t.groupText == choose {
		t.group = terminationGroup
	}
	if slices.ContainsFunc(before, func(a reserveAdd) bool { return a.side == t.side }) {
		return reserveAdd{}, errorDescriptor(434, "")
	}
	if c.Audit != nil {
		return reserveAdd{}, errorDescriptor(444, "Audit")
	}
	s, e := checkStream(c.Media, true)
	if e != nil {
		return reserveAdd{}, e
	}
	events, e := g.checkTerminationEvents(c.Events)
	if e != nil {
		return reserveAdd{}, e
	}
	latch, e := checkSignals(c.Signals, true)
	if e != nil {
		return reserveAdd{}, e
	}
	return reserveAdd{request: c, group: t.group, side: t.side, stream: s, events: events, latch: latch}, nil
}

// relayErrorCode is the error code that stands for an error of the
// relay.
func relayErrorCode(err error) int {
	switch {
	case errors.Is(err, agw.ErrNoContext):
		return 411
	case errors.Is(err, agw.ErrNoPort):
		return 510
	case errors.Is(err, agw.ErrOutOfService):
		// "Not ready": an operator has taken the media node out of
		// service, so it makes no context until it is unlocked
		// (internal/agw maintenance.go). It is among the codes that an
		// IMS-AGW sends (TS 29.334 5.7.10, Table 5.7.10.2).
		return 502
	}
	return 500
}

// storeContext gives the context an ID that no other context of the
// gateway has, counting up from FirstContextID.
func (g *Gateway) storeContext(gc *gatewayContext) {
	g.claimContextID(func(id h248.ContextID) bool {
		gc.id = id
		_, loaded := g.contexts.LoadOrStore(id, gc)
		return !loaded
	})
}

// claimContextID offers claim the next context IDs in turn, until it
// takes one. claim stores the context under the ID and reports whether
// the ID was free.
func (g *Gateway) claimContextID(claim func(id h248.ContextID) bool) {
	for {
		id := h248.ContextID(g.nextContextID.Add(1))
		if validContextID(id) && claim(id) {
			return
		}
	}
}

// contextCommand carries out a command on a context that exists.
func (g *Gateway) contextCommand(ctx context.Context, gc *gatewayContext, c h248.CommandRequest) []h248.CommandReply {
	switch c.Command {
	case h248.Add:
		return []h248.CommandReply{failed(c, 434, "")}
	case h248.Modify:
		return []h248.CommandReply{g.modify(ctx, gc, c)}
	case h248.Subtract:
		return g.subtract(ctx, gc, c)
	}
	// SIMPLIFIED: TS 29.334 5.17.3.10 also lets the controller audit
	// the terminations of a context. The controllers of this project
	// audit ROOT only.
	return []h248.CommandReply{failed(c, 501, fmt.Sprintf("%s on a termination of a context", c.Command))}
}

// termination finds the side of a context that a command names. It
// returns an error code when the ID names no termination of the
// context (H.248.8 (03/2013) 4.2.12 and 4.2.17).
func (g *Gateway) termination(gc *gatewayContext, id h248.TerminationID) (agw.TerminationID, *h248.ErrorDescriptor) {
	t, ok := parseTerminationID(id)
	if !ok || (t.side != agw.Access && t.side != agw.Core) || isWildcard(t.groupText) || isWildcard(t.idText) {
		return "", errorDescriptor(430, string(id))
	}
	if t.id != uint32(gc.id) {
		if _, elsewhere := g.contexts.Load(h248.ContextID(t.id)); elsewhere {
			return "", errorDescriptor(435, strconv.FormatUint(uint64(t.id), 10))
		}
		return "", errorDescriptor(430, string(id))
	}
	if t.group != gc.group {
		return "", errorDescriptor(430, string(id))
	}
	return t.side, nil
}

// modify carries out Configure AGW Connection Point (TS 29.334
// 5.17.2.3).
func (g *Gateway) modify(ctx context.Context, gc *gatewayContext, c h248.CommandRequest) h248.CommandReply {
	side, e := g.termination(gc, c.TerminationID)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	if c.Audit != nil {
		return failed(c, 444, "Audit")
	}
	s, e := checkStream(c.Media, false)
	if e == nil && s.hasLocal {
		// SIMPLIFIED: TS 29.334 5.17.2.3 lets the controller change
		// the local resources of a termination. The relay keeps the
		// ports it chose for the whole call.
		e = errorDescriptor(501, "a Local descriptor in Modify")
	}
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	var events *terminationEvents
	if c.Events != nil {
		ev, e := g.checkTerminationEvents(c.Events)
		if e != nil {
			return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
		}
		events = &ev
	}
	latch, e := checkSignals(c.Signals, false)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	if s.remote != nil || latch != "" {
		change := agw.TerminationRequest{Latch: latch}
		log := []any{"context", gc.id.String(), "side", side}
		if s.remote != nil {
			change.Remote, change.RemoteMedia = s.remote.address, s.remote.media
			log = append(log, "remote", s.remote.address.String())
		}
		if latch != "" {
			log = append(log, "latch", string(latch))
		}
		if err := g.relay.Modify(ctx, gc.relayID, side, change); err != nil {
			g.log.Warn("mediactl: the relay could not configure a termination", "context", gc.id.String(), "side", side, "error", err)
			return failed(c, relayErrorCode(err), "")
		}
		g.log.Info("mediactl: termination configured", log...)
	}
	if events != nil {
		g.setHeartbeat(gc, side, *events)
	} else {
		g.restartHeartbeat(gc, side)
	}
	return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID}
}

// subtract carries out Release AGW Termination (TS 29.334 5.17.2.5).
// The reply carries no statistics: Iq reports none on Subtract (TS
// 29.334 5.7.7, Table 5.7.7.2), and the descriptors of the reply are
// none or Error (Table 5.8.3.2). The Audit descriptor of the request,
// which Table 5.8.3.1 allows only empty, asks for nothing more.
//
// SIMPLIFIED: TS 29.334 5.17.2.5 lets the controller subtract one
// termination at a time. The relay releases a context as a whole, so
// this gateway asks for the wildcard "*", which subtracts both.
func (g *Gateway) subtract(ctx context.Context, gc *gatewayContext, c h248.CommandRequest) []h248.CommandReply {
	if c.TerminationID != h248.TerminationAll {
		return []h248.CommandReply{failed(c, 501, "Subtract takes the termination ID *")}
	}
	if c.Audit != nil && (len(c.Audit.AuditToken) > 0 || len(c.Audit.AuditPropertyToken) > 0) {
		return []h248.CommandReply{failed(c, 501, "an Audit descriptor that is not empty")}
	}
	if err := g.release(ctx, gc); err != nil {
		return []h248.CommandReply{failed(c, relayErrorCode(err), "")}
	}
	// The order of the replies to a wildcard is not specified (H.248.1
	// (05/2002) 7.2.3); access comes first.
	var replies []h248.CommandReply
	for _, side := range []agw.TerminationID{agw.Access, agw.Core} {
		replies = append(replies, h248.CommandReply{Command: h248.Subtract, TerminationID: terminationID(gc.group, side, gc.id)})
	}
	return replies
}
