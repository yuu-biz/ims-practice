package mediactl

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// errNotMp is returned by AddTermination and SubtractTermination on a
// Controller that speaks Iq, and errNotIq by Subtract on one that
// speaks Mp.
var (
	errNotMp = errors.New("mediactl: the Controller speaks Iq; set ControllerConfig.Profile to MpProfile for Mp")
	errNotIq = errors.New("mediactl: the Controller speaks Mp, which subtracts one termination at a time (SubtractTermination)")
)

// Add creates a context with terminations, as the Adds of the action
// that creates it.
//
// On Iq, it is Reserve AGW Connection Point (TS 29.334 5.17.2.2) for an
// access and a core termination, named agw.Access and agw.Core.
//
// On Mp, it is Reserve IMS Resources (TS 29.333 5.17.2.2) for each
// termination. A termination with a remote address makes its Add
// Reserve and Configure IMS Resources (5.17.2.4) too, and one with a
// signal Start Announcement (5.17.2.9). The MRFP chooses the ID of each
// termination; the requests leave their ID empty.
//
// The Context names each termination as later calls are to name it,
// with the address on which it receives RTP; RTCP is on the port above
// (RFC 3550 11). Its ID means something only to the Controller that
// returned it.
func (c *Controller) Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error) {
	if c.profile.mp() {
		return c.mpAdd(ctx, terminations)
	}
	return c.iqAdd(ctx, terminations)
}

// Modify changes a termination: on Iq, Configure AGW Connection Point
// (TS 29.334 5.17.2.3); on Mp, Configure IMS Resources (TS 29.333
// 5.17.2.3), and with a signal Start Announcement (5.17.2.9) or, with
// an empty one, Stop Announcement (5.17.2.10). The Remote descriptor
// tells the termination where to send and in which form, as the SDP of
// the party it faces describes what that party receives; RTCP goes to
// the port above.
func (c *Controller) Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	if c.profile.mp() {
		return c.mpModify(ctx, id, termination, change)
	}
	return c.iqModify(ctx, id, termination, change)
}

// mpAdd carries out Reserve IMS Resources for each termination, with
// the Context ID and each Termination ID CHOOSE. The Local descriptor
// leaves the address and the port to the MRFP, which returns what it
// chose.
func (c *Controller) mpAdd(ctx context.Context, terminations []agw.TerminationRequest) (agw.Context, error) {
	if len(terminations) == 0 {
		return agw.Context{}, errors.New("mediactl: a context needs at least one termination")
	}
	var adds []h248.CommandRequest
	for _, t := range terminations {
		add, err := c.mpCommand(h248.Add, h248.TerminationChoose, t)
		if err != nil {
			return agw.Context{}, err
		}
		adds = append(adds, add)
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
	if len(reply.Actions) != 1 || !validContextID(reply.Actions[0].ContextID) || len(reply.Actions[0].Commands) != len(adds) {
		return agw.Context{}, errMalformedReply
	}
	action := reply.Actions[0]
	out := agw.Context{ID: contextHandle(action.ContextID, a), Terminations: map[agw.TerminationID]*agw.Termination{}}
	// Each reply answers the command at its place: a gateway carries out
	// the commands of an action one after the other (H.248.1 (05/2002)
	// clause 8).
	for i, cmd := range action.Commands {
		t, err := addedTermination(cmd, terminations[i])
		if err != nil {
			return agw.Context{}, err
		}
		out.Terminations[t.ID] = t
	}
	c.log.Info("mediactl: context created", "context", out.ID, "terminations", len(out.Terminations))
	return out, nil
}

// AddTermination carries out Reserve IMS Resources for a context that
// exists (TS 29.333 5.17.2.2, 5.17.2.30), as when a party joins a
// conference: one Add with the Context ID of the context and CHOOSE for
// the Termination ID. It is a procedure of Mp only.
func (c *Controller) AddTermination(ctx context.Context, id string, t agw.TerminationRequest) (agw.Termination, error) {
	if !c.profile.mp() {
		return agw.Termination{}, errNotMp
	}
	add, err := c.mpCommand(h248.Add, h248.TerminationChoose, t)
	if err != nil {
		return agw.Termination{}, err
	}
	reply, err := c.contextRequest(ctx, id, add)
	if err != nil {
		return agw.Termination{}, err
	}
	if len(reply.Actions) != 1 || len(reply.Actions[0].Commands) != 1 {
		return agw.Termination{}, errMalformedReply
	}
	out, err := addedTermination(reply.Actions[0].Commands[0], t)
	if err != nil {
		return agw.Termination{}, err
	}
	c.log.Info("mediactl: termination added", "context", id, "termination", string(out.ID))
	return *out, nil
}

// mpModify is the Modify of the termination.
func (c *Controller) mpModify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error {
	if len(change.LocalMedia.Codecs) > 0 || change.LocalMedia.Bandwidth > 0 {
		return errors.New("mediactl: Modify keeps the Local descriptor of a termination")
	}
	modify, err := c.mpCommand(h248.Modify, h248.TerminationID(termination), change)
	if err != nil {
		return err
	}
	_, err = c.contextRequest(ctx, id, modify)
	return err
}

// SubtractTermination carries out Release IMS Termination (TS 29.333
// 5.17.2.5): a Subtract of the termination, without an Audit
// descriptor, so that the MRFP reports the RTP packets it carried
// (rtp/ps and rtp/pr, H.248.1 (05/2002) E.12.4), which the Termination
// returns as PacketsSent and Packets. It is a procedure of Mp only.
//
// The last termination takes the context with it (H.248.1 (05/2002)
// 6.1.2), and the reply does not say whether that happened, so deleted
// is always false. A caller learns that the context is gone when a
// later call for it fails with agw.ErrNoContext.
func (c *Controller) SubtractTermination(ctx context.Context, id string, termination agw.TerminationID) (_ agw.Termination, deleted bool, _ error) {
	if !c.profile.mp() {
		return agw.Termination{}, false, errNotMp
	}
	reply, err := c.contextRequest(ctx, id, h248.CommandRequest{Command: h248.Subtract, TerminationID: h248.TerminationID(termination)})
	if err != nil {
		return agw.Termination{}, false, err
	}
	if len(reply.Actions) != 1 || len(reply.Actions[0].Commands) != 1 {
		return agw.Termination{}, false, errMalformedReply
	}
	out := agw.Termination{ID: termination}
	out.PacketsSent, out.Packets = readStatistics(reply.Actions[0].Commands[0].Statistics)
	c.log.Info("mediactl: termination subtracted", "context", id, "termination", string(termination),
		"packets_received", out.Packets, "packets_sent", out.PacketsSent)
	// SIMPLIFIED: H.248.1 (05/2002) 6.1.2. The last termination that is
	// subtracted takes the context with it, and deleted is to tell the
	// caller so. The Controller does not track how many terminations a
	// context has left, so deleted is always false, and the MRFC learns
	// that the context is gone only when its next AddTermination for it
	// fails with agw.ErrNoContext. The Subtract reply does not say, and
	// the Controller keeps no record of the contexts, so that several
	// Controllers can run side by side and share nothing.
	return out, false, nil
}

// readStatistics reads the RTP packets sent and received that mpSubtract
// writes. Other statistics are left out.
func readStatistics(d h248.StatisticsDescriptor) (sent, received uint64) {
	for _, p := range d {
		n, err := strconv.ParseFloat(p.StatValue.Text, 64)
		if err != nil || n < 0 {
			continue
		}
		// The statistics are of type double (H.248.1 (05/2002) E.12.4);
		// these hold whole numbers.
		v := uint64(n)
		switch h248.PkgdName(strings.ToLower(string(p.StatName))) {
		case statPacketsSent:
			sent = v
		case statPacketsReceived:
			received = v
		}
	}
	return sent, received
}

// contextRequest sends one command on the context that a Context ID of
// Add names, and returns the reply unless it is an error.
func (c *Controller) contextRequest(ctx context.Context, contextID string, cmd h248.CommandRequest) (h248.TransactionReply, error) {
	a, id, err := c.lookup(contextID)
	if err != nil {
		return h248.TransactionReply{}, err
	}
	reply, err := c.request(ctx, a, []h248.ActionRequest{{ContextID: id, Commands: []h248.CommandRequest{cmd}}})
	if err != nil {
		return reply, err
	}
	return reply, rejection(reply)
}

// mpCommand is the Add or the Modify of Mp for t. The LocalControl
// descriptor carries the mode: on Add always, with SendReceive when t
// has none, since H.248 would make the termination Inactive (H.248.1
// (05/2002) 7.1.7); on Modify only when t sets one, as a Modify without
// it keeps the mode. An Add always carries a Local descriptor and an
// Events descriptor with the termination heartbeat; a Modify carries an
// Events descriptor only when t sets one, since a new Events descriptor
// replaces the one before (H.248.1 (05/2002) 7.1.9).
func (c *Controller) mpCommand(command h248.Command, id h248.TerminationID, t agw.TerminationRequest) (h248.CommandRequest, error) {
	add := command == h248.Add
	if add && t.ID != "" {
		// TS 29.333 5.17.2.2, 5.17.2.9: a new termination is CHOOSE.
		return h248.CommandRequest{}, fmt.Errorf("mediactl: the MRFP chooses the termination ID, not %q", t.ID)
	}
	if t.Latch != "" {
		// The ipnapt package is not among MpPackages.
		return h248.CommandRequest{}, fmt.Errorf("mediactl: Mp asks for no latching, not %q", t.Latch)
	}
	parms := &h248.StreamParms{}
	mode := t.Mode
	if mode == "" && add {
		mode = agw.SendReceive
	}
	if mode != "" {
		m, ok := streamModeOf(mode)
		if !ok {
			return h248.CommandRequest{}, fmt.Errorf("mediactl: no mode %q", t.Mode)
		}
		parms.LocalControl = &h248.LocalControlDescriptor{StreamMode: m}
	}
	if add {
		parms.Local = &h248.LocalRemoteDescriptor{SDP: controllerSDP(netip.AddrPort{}, t.LocalMedia)}
	}
	if t.Remote.IsValid() {
		parms.Remote = &h248.LocalRemoteDescriptor{SDP: controllerSDP(t.Remote, t.RemoteMedia)}
	}
	cmd := h248.CommandRequest{Command: command, TerminationID: id}
	if parms.LocalControl != nil || parms.Remote != nil || add {
		cmd.Media = &h248.MediaDescriptor{OneStream: parms}
	}
	if t.Events != nil {
		for _, e := range t.Events.Detect {
			if e != agw.SignalCompleted {
				return h248.CommandRequest{}, fmt.Errorf("mediactl: no event %q", e)
			}
		}
	}
	if add || t.Events != nil {
		cmd.Events = c.mpEventsDescriptor(t.Events)
	}
	if t.Signals != nil {
		cmd.Signals = &h248.SignalsDescriptor{}
		if t.Signals.Play != nil {
			s, err := playSignal(*t.Signals.Play)
			if err != nil {
				return h248.CommandRequest{}, err
			}
			cmd.Signals.Signals = []h248.Signal{s}
		}
	}
	return cmd, nil
}

// addedTermination reads the termination ID and the address that the
// MRFP chose from the reply to the Add of request.
func addedTermination(cmd h248.CommandReply, request agw.TerminationRequest) (*agw.Termination, error) {
	if cmd.TerminationID == "" || strings.ContainsAny(string(cmd.TerminationID), "$*") {
		return nil, fmt.Errorf("%w: termination %q", errMalformedReply, cmd.TerminationID)
	}
	local, err := replyLocal(cmd)
	if err != nil {
		return nil, err
	}
	return added(agw.TerminationID(cmd.TerminationID), local, request), nil
}

// signalCompletion hands the g/sc of a Notify, Announcement Completed
// (TS 29.333 5.17.2.11), to ControllerConfig.Notify.
func (c *Controller) signalCompletion(a *association, contextID h248.ContextID, cmd h248.CommandRequest, ev h248.ObservedEvent) {
	n := Notification{
		Notify: agw.Notify{
			Context:     contextHandle(contextID, a),
			Termination: agw.TerminationID(cmd.TerminationID),
			RequestID:   cmd.ObservedEvents.RequestID,
			Event:       agw.SignalCompleted,
		},
		Detected: readTimeStamp(ev.TimeNotation),
	}
	for _, p := range ev.EventParList {
		switch {
		case strings.EqualFold(p.Name, paramSignalIdentity):
			n.Signal = p.Value.Text
		case strings.EqualFold(p.Name, paramTerminationMethod):
			n.Method = TerminationMethod(strings.ToUpper(p.Value.Text))
		}
	}
	c.log.Info("mediactl: a signal has ended", "context", n.Context, "termination", string(n.Termination),
		"signal", n.Signal, "method", string(n.Method), "request_id", n.RequestID)
	if c.onNotify != nil {
		c.onNotify(n)
	}
}

// readTimeStamp reads the time that timeStamp writes, or returns the
// zero time.
func readTimeStamp(t h248.TimeNotation) time.Time {
	if len(t.Time) != 8 {
		return time.Time{}
	}
	at, err := time.ParseInLocation("20060102150405", t.Date+t.Time[:6], time.UTC)
	if err != nil {
		return time.Time{}
	}
	var hundredths int
	fmt.Sscanf(t.Time[6:], "%02d", &hundredths)
	return at.Add(time.Duration(hundredths) * 10 * time.Millisecond)
}
