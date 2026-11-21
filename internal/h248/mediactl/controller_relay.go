package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"time"

	"github.com/yuu-biz/ims-practice/internal/h248"
)

// Reserve carries out Reserve AGW Connection Point (TS 29.334
// 5.17.2.2) for the access and the core termination, as two Adds in the
// action that creates the context. The Local descriptors leave the
// address and the port to the gateway, which returns what it chose.
func (c *Controller) Reserve(ctx context.Context, media Media) (Reservation, error) {
	if c.profile.mp() {
		return Reservation{}, errNotIq
	}
	a := c.pick()
	if a == nil {
		return Reservation{}, ErrUnavailable
	}
	media.Address = netip.AddrPort{}
	local := controllerSDP(media)
	reply, err := c.request(ctx, a, []h248.ActionRequest{{
		ContextID: h248.ContextChoose,
		Commands:  []h248.CommandRequest{c.reserveAdd(Access, local), c.reserveAdd(Core, local)},
	}})
	if err != nil {
		return Reservation{}, err
	}
	if err := rejection(reply); err != nil {
		return Reservation{}, err
	}
	if len(reply.Actions) != 1 || !validContextID(reply.Actions[0].ContextID) {
		return Reservation{}, errMalformedReply
	}
	action := reply.Actions[0]
	r := Reservation{ID: contextHandle(action.ContextID, a), Local: map[Side]netip.AddrPort{}}
	for _, cmd := range action.Commands {
		t, ok := parseTerminationID(cmd.TerminationID)
		// The profile has the gateway give both terminations the
		// context ID (see terminationID), so that the later commands
		// can name them from the Reservation ID alone.
		if !ok || cmd.TerminationID != terminationID(terminationGroup, t.side, action.ContextID) {
			return Reservation{}, fmt.Errorf("%w: termination %s", errMalformedReply, cmd.TerminationID)
		}
		local, err := replyLocal(cmd)
		if err != nil {
			return Reservation{}, err
		}
		r.Local[t.side] = local
	}
	if len(r.Local) != 2 {
		return Reservation{}, errMalformedReply
	}
	c.log.Info("mediactl: context reserved", "context", r.ID,
		"access", r.Local[Access].String(), "core", r.Local[Core].String())
	return r, nil
}

// reserveAdd is one Add of Reserve AGW Connection Point (TS 29.334
// 5.17.2.2, Table 5.17.2.2.1). The stream mode is set, since it is
// Inactive otherwise (H.248.1 (05/2002) 7.1.7). The events are the
// termination heartbeat, which the table always asks for, and the
// release of the bearer; the signal asks for latching, because a UE
// behind NAT sends from another address than its SDP shows.
func (c *Controller) reserveAdd(side Side, local string) h248.CommandRequest {
	return h248.CommandRequest{
		Command:       h248.Add,
		TerminationID: chooseTerminationID(side),
		Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
			LocalControl: &h248.LocalControlDescriptor{StreamMode: h248.SendRecv},
			Local:        &h248.LocalRemoteDescriptor{SDP: local},
		}},
		Events: c.terminationEvents(),
		Signals: &h248.SignalsDescriptor{Signals: []h248.Signal{{
			SignalName: signalLatch,
			SigParList: []h248.SigParameter{{Name: paramNAPT, Value: h248.Value{Text: naptLatch}}},
		}}},
	}
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
	if d.chooseAddress || d.choosePort || !d.media.Address.Addr().IsValid() {
		return netip.AddrPort{}, fmt.Errorf("%w: the gateway chose no address for %s", errMalformedReply, cmd.TerminationID)
	}
	return d.media.Address, nil
}

// Configure carries out Configure AGW Connection Point (TS 29.334
// 5.17.2.3): a Modify with the Remote descriptor.
func (c *Controller) Configure(ctx context.Context, id string, side Side, remote Media) error {
	if c.profile.mp() {
		return errNotIq
	}
	if side != Access && side != Core {
		return fmt.Errorf("mediactl: no termination %q", side)
	}
	if !remote.Address.IsValid() {
		return errors.New("mediactl: Configure needs the remote address")
	}
	a, contextID, err := c.lookup(id)
	if err != nil {
		return err
	}
	reply, err := c.request(ctx, a, []h248.ActionRequest{{
		ContextID: contextID,
		Commands: []h248.CommandRequest{{
			Command:       h248.Modify,
			TerminationID: terminationID(terminationGroup, side, contextID),
			Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
				LocalControl: &h248.LocalControlDescriptor{StreamMode: h248.SendRecv},
				Remote:       &h248.LocalRemoteDescriptor{SDP: controllerSDP(remote)},
			}},
			Events: c.terminationEvents(),
		}},
	}})
	if err != nil {
		return err
	}
	return rejection(reply)
}

// Release carries out Release AGW Termination (TS 29.334 5.17.2.5) for
// both terminations at once, with the wildcard "*". The reply carries
// no statistics: Iq reports none on Subtract (TS 29.334 5.7.7, Table
// 5.7.7.2).
func (c *Controller) Release(ctx context.Context, id string) error {
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
