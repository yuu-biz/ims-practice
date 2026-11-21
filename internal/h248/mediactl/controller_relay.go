package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// iqAdd carries out Reserve AGW Connection Point (TS 29.334 5.17.2.2)
// for the access and the core termination, as two Adds in the action
// that creates the context, and makes it Reserve and Configure AGW
// Connection Point (5.17.2.4) for a termination whose request has a
// remote address. The Local descriptors leave the address and the port
// to the gateway, which returns what it chose.
func (c *Controller) iqAdd(ctx context.Context, terminations []agw.TerminationRequest) (agw.Context, error) {
	asked := map[agw.TerminationID]agw.TerminationRequest{}
	var adds []h248.CommandRequest
	for _, t := range terminations {
		if t.ID != agw.Access && t.ID != agw.Core {
			return agw.Context{}, fmt.Errorf("mediactl: no termination %q on Iq", t.ID)
		}
		asked[t.ID] = t
		add, err := c.iqCommand(h248.Add, chooseTerminationID(t.ID), t)
		if err != nil {
			return agw.Context{}, err
		}
		adds = append(adds, add)
	}
	if len(terminations) != 2 || len(asked) != 2 {
		return agw.Context{}, errors.New("mediactl: a context of Iq has an access and a core termination")
	}
	a := c.pick()
	if a == nil {
		return agw.Context{}, ErrUnavailable
	}
	reply, err := c.request(ctx, a, []h248.ActionRequest{{ContextID: h248.ContextChoose, Commands: adds}})
	if err != nil {
		return agw.Context{}, err
	}
	if err := rejection(reply); err != nil {
		return agw.Context{}, err
	}
	if len(reply.Actions) != 1 || !validContextID(reply.Actions[0].ContextID) {
		return agw.Context{}, errMalformedReply
	}
	action := reply.Actions[0]
	out := agw.Context{ID: contextHandle(action.ContextID, a), Terminations: map[agw.TerminationID]*agw.Termination{}}
	for _, cmd := range action.Commands {
		t, ok := parseTerminationID(cmd.TerminationID)
		request, wanted := asked[t.side]
		// The profile has the gateway give both terminations the
		// context ID (see terminationID), so that the later commands
		// can name them from the ID of the context alone.
		if !ok || !wanted || cmd.TerminationID != terminationID(terminationGroup, t.side, action.ContextID) {
			return agw.Context{}, fmt.Errorf("%w: termination %s", errMalformedReply, cmd.TerminationID)
		}
		local, err := replyLocal(cmd)
		if err != nil {
			return agw.Context{}, err
		}
		out.Terminations[t.side] = added(t.side, local, request)
	}
	if len(out.Terminations) != 2 {
		return agw.Context{}, errMalformedReply
	}
	c.log.Info("mediactl: context reserved", "context", out.ID,
		"access", out.Terminations[agw.Access].Local.String(), "core", out.Terminations[agw.Core].Local.String())
	return out, nil
}

// iqCommand is the Add or the Modify of Iq for t. The stream mode is
// always set: without it an Add would leave the termination Inactive
// (H.248.1 (05/2002) 7.1.7), and the IMS-AGW passes media both ways, so
// a request without a mode gets SendReceive. The Events descriptor is
// that of terminationEvents. An Add carries the Local descriptor, and
// asks for latching with its signal, because a UE behind NAT sends from
// another address than its SDP shows; a request with a remote address
// carries the Remote descriptor too.
func (c *Controller) iqCommand(command h248.Command, id h248.TerminationID, t agw.TerminationRequest) (h248.CommandRequest, error) {
	if t.Signals != nil || t.Events != nil {
		return h248.CommandRequest{}, errors.New("mediactl: a termination of Iq plays no signal and reports the events of the profile only")
	}
	mode := t.Mode
	if mode == "" {
		mode = agw.SendReceive
	}
	m, ok := streamModeOf(mode)
	if !ok {
		return h248.CommandRequest{}, fmt.Errorf("mediactl: no mode %q", t.Mode)
	}
	parms := &h248.StreamParms{LocalControl: &h248.LocalControlDescriptor{StreamMode: m}}
	cmd := h248.CommandRequest{Command: command, TerminationID: id, Media: &h248.MediaDescriptor{OneStream: parms}, Events: c.terminationEvents()}
	switch {
	case command == h248.Add:
		parms.Local = &h248.LocalRemoteDescriptor{SDP: controllerSDP(netip.AddrPort{}, t.LocalMedia)}
		cmd.Signals = &h248.SignalsDescriptor{Signals: []h248.Signal{{
			SignalName: signalLatch,
			SigParList: []h248.SigParameter{{Name: paramNAPT, Value: h248.Value{Text: naptLatch}}},
		}}}
	case len(t.LocalMedia.Codecs) > 0 || t.LocalMedia.Bandwidth > 0:
		return h248.CommandRequest{}, errors.New("mediactl: Modify keeps the Local descriptor of a termination")
	}
	if t.Remote.IsValid() {
		parms.Remote = &h248.LocalRemoteDescriptor{SDP: controllerSDP(t.Remote, t.RemoteMedia)}
	}
	return cmd, nil
}

// added is the termination that an Add made at local, as the request
// asked for it.
func added(id agw.TerminationID, local netip.AddrPort, request agw.TerminationRequest) *agw.Termination {
	mode := request.Mode
	if mode == "" {
		mode = agw.SendReceive
	}
	return &agw.Termination{ID: id, Mode: mode, Local: local, Remote: request.Remote}
}

// terminationEvents is the Events descriptor of every Add and Modify.
// It replaces the one before (H.248.1 (05/2002) 7.1.9), so it always
// holds both events.
func (c *Controller) terminationEvents() *h248.EventsDescriptor {
	return &h248.EventsDescriptor{RequestID: requestIDTermination, EventList: []h248.RequestedEvent{
		{PkgdName: eventHeartbeat, EvParList: []h248.EventParameter{{
			Name:  paramTimerX,
			Value: h248.Value{Text: strconv.FormatInt(int64(c.timerX/time.Second), 10)},
		}}},
		{PkgdName: eventCause},
	}}
}

// replyLocal reads the address that the gateway chose from the Local
// descriptor of the reply to an Add.
func replyLocal(cmd h248.CommandReply) (netip.AddrPort, error) {
	var parms *h248.StreamParms
	switch {
	case cmd.Media == nil:
	case cmd.Media.OneStream != nil:
		parms = cmd.Media.OneStream
	case len(cmd.Media.MultiStream) == 1:
		parms = &cmd.Media.MultiStream[0].StreamParms
	}
	if parms == nil || parms.Local == nil {
		return netip.AddrPort{}, fmt.Errorf("%w: no Local descriptor for %s", errMalformedReply, cmd.TerminationID)
	}
	d, err := parseSDP(parms.Local.SDP)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("%w: %w", errMalformedReply, err)
	}
	if d.chooseAddress || d.choosePort || !d.address.Addr().IsValid() {
		return netip.AddrPort{}, fmt.Errorf("%w: the gateway chose no address for %s", errMalformedReply, cmd.TerminationID)
	}
	return d.address, nil
}

// iqModify carries out Configure AGW Connection Point (TS 29.334
// 5.17.2.3): a Modify of the termination with the Remote descriptor. A
// relay that latches on the first packet it receives (H.248.37 6.6.2)
// keeps the latched address.
func (c *Controller) iqModify(ctx context.Context, id string, side agw.TerminationID, change agw.TerminationRequest) error {
	if side != agw.Access && side != agw.Core {
		return fmt.Errorf("mediactl: no termination %q on Iq", side)
	}
	if !change.Remote.IsValid() {
		return errors.New("mediactl: Modify of Iq needs the remote address")
	}
	a, contextID, err := c.lookup(id)
	if err != nil {
		return err
	}
	modify, err := c.iqCommand(h248.Modify, terminationID(terminationGroup, side, contextID), change)
	if err != nil {
		return err
	}
	reply, err := c.request(ctx, a, []h248.ActionRequest{{ContextID: contextID, Commands: []h248.CommandRequest{modify}}})
	if err != nil {
		return err
	}
	return rejection(reply)
}

// Subtract carries out Release AGW Termination (TS 29.334 5.17.2.5)
// for both terminations of the context at once, with the wildcard "*".
// The reply carries no statistics: Iq reports none on Subtract (TS
// 29.334 5.7.7, Table 5.7.7.2).
func (c *Controller) Subtract(ctx context.Context, id string) error {
	if c.profile.mp() {
		return errNotIq
	}
	a, contextID, err := c.lookup(id)
	if err != nil {
		return err
	}
	reply, err := c.request(ctx, a, []h248.ActionRequest{{
		ContextID: contextID,
		Commands:  []h248.CommandRequest{{Command: h248.Subtract, TerminationID: h248.TerminationAll}},
	}})
	if err != nil {
		return err
	}
	if err := rejection(reply); err != nil {
		return err
	}
	c.log.Info("mediactl: context released", "context", id)
	return nil
}
