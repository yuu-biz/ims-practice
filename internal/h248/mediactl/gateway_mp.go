package mediactl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
	"github.com/yuu-biz/ims-practice/internal/h248"
)

// mpContext is a context of a Gateway that runs a Processor, and the
// context of the Processor that stands behind it.
type mpContext struct {
	id          h248.ContextID
	processorID string

	// mu guards what follows, and is held while the Processor carries
	// out a command on the context, so that the commands on one context
	// happen one after the other. It covers this context only.
	mu       sync.Mutex
	released bool
	// numbered is the last <n> of the termination IDs given out.
	numbered int
	terms    map[h248.TerminationID]*mpTermination
}

// mpTermination is a termination of an mpContext.
type mpTermination struct {
	id          h248.TerminationID
	processorID agw.TerminationID
	events      mpEvents
	heartbeat   *heartbeat
	// playing is the signal that plays, or empty. notifyOnTimeOut and
	// notifyOnInterrupt tell that the controller asked to hear when it
	// ends on its own, or when an event stops it (NotifyCompletion
	// with TimeOut and IntByEvent, H.248.1 (05/2002) 7.1.11).
	playing           h248.PkgdName
	notifyOnTimeOut   bool
	notifyOnInterrupt bool
}

// mpEvents is what an Events descriptor of Mp asks of a termination.
type mpEvents struct {
	requestID       uint32
	signalCompleted bool
	// heartbeat is timer X of hangterm/thb, or zero when the event is
	// not set. heartbeatKeepActive is its KeepActive flag.
	heartbeat           time.Duration
	heartbeatKeepActive bool
}

// mpSignal is what a Signals descriptor of Mp asks of a termination: an
// announcement to play, or, with a nil play, to stop what plays.
type mpSignal struct {
	name              h248.PkgdName
	play              *agw.Play
	notifyOnTimeOut   bool
	notifyOnInterrupt bool
}

// mpCommand is an Add or a Modify of Mp, read and checked.
type mpCommand struct {
	request h248.CommandRequest
	change  agw.TerminationRequest
	stream  stream
	// events and signal are nil when the command has no Events or
	// Signals descriptor.
	events *mpEvents
	signal *mpSignal
}

// mpAction carries out one action of Mp.
func (g *Gateway) mpAction(t *transaction, a h248.ActionRequest) (h248.ActionReply, bool) {
	if a.ContextRequest != nil && len(a.ContextRequest.TopologyReq) > 0 {
		// SIMPLIFIED: TS 29.333 5.7.8 (Table 5.7.8.1) allows the
		// topologies isolate, oneway and bothway. The media node sends
		// each termination the media of all the others, as the default
		// topology of H.248.1 (05/2002) 7.1.18 does, and has no other.
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(522, "")}, false
	}
	// SIMPLIFIED: TS 29.333 5.5 lets the MRFC give a context a priority
	// for MPS. The media node treats every context alike, so the
	// attribute is accepted and has no effect.
	switch a.ContextID {
	case h248.ContextNull:
		return g.rootAction(t, a)
	case h248.ContextChoose:
		return g.mpCreate(t, a)
	case h248.ContextAll:
		// SIMPLIFIED: TS 29.333 5.17.3.8 allows ALL as the context of an
		// AuditValue. As for Iq (see action), that would walk every
		// context of the gateway.
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(501, "ContextID ALL")}, false
	}
	v, found := g.contexts.Load(a.ContextID)
	if !found {
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(411, "")}, false
	}
	gc := v.(*mpContext)
	commands, ok := t.runCommands(a.ContextID, a.Commands, func(c h248.CommandRequest) []h248.CommandReply {
		return []h248.CommandReply{g.mpContextCommand(t.ctx, gc, c)}
	})
	return h248.ActionReply{ContextID: a.ContextID, Commands: commands}, ok
}

// mpCreate carries out the Adds of an action that creates a context:
// Reserve IMS Resources (TS 29.333 5.17.2.2), with Reserve and
// Configure IMS Resources (5.17.2.4) and Start Announcement (5.17.2.9)
// as each Add asks. The Processor makes the context with all its
// terminations at once, or fails as a whole.
func (g *Gateway) mpCreate(t *transaction, a h248.ActionRequest) (h248.ActionReply, bool) {
	ctx := t.ctx
	refuse := func(c h248.CommandRequest, e *h248.ErrorDescriptor) (h248.ActionReply, bool) {
		return h248.ActionReply{ContextID: a.ContextID, Commands: []h248.CommandReply{{Command: c.Command, TerminationID: c.TerminationID, Error: e}}}, false
	}
	if len(a.Commands) == 0 {
		return h248.ActionReply{ContextID: a.ContextID, Error: errorDescriptor(501, "a context to be created needs an Add")}, false
	}
	if t.stopped() {
		return refuse(a.Commands[0], errorDescriptor(506, ""))
	}
	var adds []mpCommand
	var requests []agw.TerminationRequest
	for i, c := range a.Commands {
		if c.Command != h248.Add {
			return refuse(c, errorDescriptor(501, fmt.Sprintf("%s in a context to be created", c.Command)))
		}
		add, e := g.checkMpCommand(c, true)
		if e != nil {
			return refuse(c, e)
		}
		add.change.ID = processorTerminationID(i + 1)
		adds = append(adds, add)
		requests = append(requests, add.change)
	}
	pc, err := g.processor.Add(ctx, requests...)
	for _, r := range requests {
		if err == nil && pc.Terminations[r.ID] == nil {
			err = fmt.Errorf("the processor made no termination %s", r.ID)
		}
	}
	if err != nil {
		g.log.Warn("mediactl: the processor could not make a context", "error", err)
		return refuse(adds[0].request, errorDescriptor(t.errorCode(processorErrorCode(err)), ""))
	}
	gc := &mpContext{processorID: pc.ID, terms: map[h248.TerminationID]*mpTermination{}}
	gc.mu.Lock()
	defer gc.mu.Unlock()
	g.claimContextID(func(id h248.ContextID) bool {
		gc.id = id
		_, loaded := g.contexts.LoadOrStore(id, gc)
		return !loaded
	})
	g.byProcessorContext.Store(gc.processorID, gc)
	g.log.Info("mediactl: context created", "context", gc.id.String(), "processor_context", gc.processorID)
	reply := h248.ActionReply{ContextID: gc.id}
	for _, add := range adds {
		reply.Commands = append(reply.Commands, g.addedTermination(gc, add, *pc.Terminations[add.change.ID]))
	}
	if i := t.overflow(reply); i >= 0 {
		// H.248.8 (03/2013) 4.2.66 and H.248.1 (05/2002) clause 8, as
		// for Iq (see reserve): the Processor made the context as a
		// whole, so it goes as a whole.
		for _, term := range gc.terms {
			if term.heartbeat != nil {
				term.heartbeat.timer.Stop()
			}
			if _, err := g.processor.SubtractTermination(context.WithoutCancel(ctx), gc.processorID, term.processorID); err != nil {
				g.log.Warn("mediactl: the processor could not subtract a termination", "context", gc.id.String(), "error", err)
			}
		}
		gc.released = true
		g.contexts.Delete(gc.id)
		g.byProcessorContext.Delete(gc.processorID)
		return refuse(adds[i].request, errorDescriptor(533, ""))
	}
	return reply, true
}

// mpContextCommand carries out a command on a context that exists.
func (g *Gateway) mpContextCommand(ctx context.Context, gc *mpContext, c h248.CommandRequest) h248.CommandReply {
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if gc.released {
		return failed(c, 411, "")
	}
	switch c.Command {
	case h248.Add:
		return g.mpAddTermination(ctx, gc, c)
	case h248.Modify:
		return g.mpModify(ctx, gc, c)
	case h248.Subtract:
		return g.mpSubtract(ctx, gc, c)
	}
	// SIMPLIFIED: TS 29.333 5.8.4 and 5.8.5 also use Move, and
	// AuditValue of the signals, events and statistics of a
	// termination. The MRFC of this project uses neither.
	return failed(c, 501, fmt.Sprintf("%s on a termination of a context", c.Command))
}

// mpAddTermination adds a termination to a context that exists: Reserve
// IMS Resources with the Context ID of the context (TS 29.333 5.17.2.2,
// 5.17.2.9), as when a party joins a conference (5.17.2.30). gc.mu is
// held.
func (g *Gateway) mpAddTermination(ctx context.Context, gc *mpContext, c h248.CommandRequest) h248.CommandReply {
	add, e := g.checkMpCommand(c, true)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	add.change.ID = processorTerminationID(gc.numbered + 1)
	t, err := g.processor.AddTermination(ctx, gc.processorID, add.change)
	if err != nil {
		g.log.Warn("mediactl: the processor could not add a termination", "context", gc.id.String(), "error", err)
		return failed(c, processorErrorCode(err), "")
	}
	return g.addedTermination(gc, add, t)
}

// processorTerminationID is the ID that the Gateway asks its Processor
// to give the termination ip/<context>/<n>, so that it finds each
// termination in what Add returns.
func processorTerminationID(n int) agw.TerminationID {
	return agw.TerminationID("t" + strconv.Itoa(n))
}

// addedTermination records a termination that the Processor has added,
// and is the reply to its Add: the termination ID, and the Local
// descriptor with the address the Processor chose. gc.mu is held.
func (g *Gateway) addedTermination(gc *mpContext, add mpCommand, pt agw.Termination) h248.CommandReply {
	gc.numbered++
	t := &mpTermination{id: mpTerminationID(gc.id, gc.numbered), processorID: pt.ID}
	gc.terms[t.id] = t
	g.applied(gc, t, add)
	g.log.Info("mediactl: termination added", "context", gc.id.String(), "termination", string(t.id),
		"local", pt.Local.String(), "mode", string(add.change.Mode))
	return h248.CommandReply{
		Command:       h248.Add,
		TerminationID: t.id,
		Media: &h248.MediaDescriptor{OneStream: &h248.StreamParms{
			Local: &h248.LocalRemoteDescriptor{SDP: gatewayLocal(add.stream.local, pt.Local, gc.id)},
		}},
	}
}

// applied records the events and the signal of a command that the
// Processor has carried out on t. gc.mu is held.
func (g *Gateway) applied(gc *mpContext, t *mpTermination, c mpCommand) {
	if c.events != nil {
		t.events = *c.events
		g.setMpHeartbeat(gc, t)
	} else {
		g.restartMpHeartbeat(gc, t)
	}
	if c.signal != nil {
		t.playing, t.notifyOnTimeOut, t.notifyOnInterrupt = "", false, false
		if c.signal.play != nil {
			t.playing, t.notifyOnTimeOut, t.notifyOnInterrupt = c.signal.name, c.signal.notifyOnTimeOut, c.signal.notifyOnInterrupt
		}
	}
}

// mpTermination finds the termination of gc that a command names. It
// returns an error code when the ID names no termination of the context
// (H.248.8 (03/2013) 4.2.12 and 4.2.17). gc.mu is held.
func (g *Gateway) mpTermination(gc *mpContext, id h248.TerminationID) (*mpTermination, *h248.ErrorDescriptor) {
	key := h248.TerminationID(strings.ToLower(string(id)))
	if t, ok := gc.terms[key]; ok {
		return t, nil
	}
	if fields := strings.Split(string(key), "/"); len(fields) == 3 && fields[0] == "ip" {
		if n, err := strconv.ParseUint(fields[1], 10, 32); err == nil && h248.ContextID(n) != gc.id {
			if _, elsewhere := g.contexts.Load(h248.ContextID(n)); elsewhere {
				return nil, errorDescriptor(435, fields[1])
			}
		}
	}
	return nil, errorDescriptor(430, string(id))
}

// mpModify carries out Configure IMS Resources (TS 29.333 5.17.2.3),
// Start Announcement (5.17.2.9) and Stop Announcement (5.17.2.10).
// gc.mu is held.
func (g *Gateway) mpModify(ctx context.Context, gc *mpContext, c h248.CommandRequest) h248.CommandReply {
	t, e := g.mpTermination(gc, c.TerminationID)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	m, e := g.checkMpCommand(c, false)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	if err := g.processor.Modify(ctx, gc.processorID, t.processorID, m.change); err != nil {
		g.log.Warn("mediactl: the processor could not modify a termination", "context", gc.id.String(), "termination", string(t.id), "error", err)
		return failed(c, processorErrorCode(err), "")
	}
	g.applied(gc, t, m)
	g.log.Info("mediactl: termination modified", "context", gc.id.String(), "termination", string(t.id),
		"mode", string(m.change.Mode), "signal", string(t.playing))
	return h248.CommandReply{Command: c.Command, TerminationID: t.id}
}

// mpSubtract carries out Release IMS Termination (TS 29.333 5.17.2.5).
// The last termination takes the context with it (H.248.1 (05/2002)
// 6.1.2). The reply reports the packets the termination carried, the
// statistics of the rtp package that Mp lets a Subtract reply carry (TS
// 29.333 5.14.3.5, Table 5.14.3.5.1), unless an empty Audit descriptor
// asks for none (5.8.3). gc.mu is held.
//
// SIMPLIFIED: H.248.1 (05/2002) 7.2.3 lets a Subtract name its
// terminations with a wildcard. Release IMS Termination names one, and
// so does the MRFC of this project.
func (g *Gateway) mpSubtract(ctx context.Context, gc *mpContext, c h248.CommandRequest) h248.CommandReply {
	if strings.ContainsAny(string(c.TerminationID), "*$") {
		return failed(c, 501, "Subtract names one termination")
	}
	if c.Audit != nil && (len(c.Audit.AuditToken) > 0 || len(c.Audit.AuditPropertyToken) > 0) {
		return failed(c, 501, "an Audit descriptor that is not empty")
	}
	t, e := g.mpTermination(gc, c.TerminationID)
	if e != nil {
		return h248.CommandReply{Command: c.Command, TerminationID: c.TerminationID, Error: e}
	}
	stats, err := g.processor.SubtractTermination(ctx, gc.processorID, t.processorID)
	if err != nil && !errors.Is(err, agw.ErrNoTermination) {
		g.log.Warn("mediactl: the processor could not subtract a termination", "context", gc.id.String(), "termination", string(t.id), "error", err)
		return failed(c, processorErrorCode(err), "")
	}
	if t.heartbeat != nil {
		t.heartbeat.timer.Stop()
	}
	delete(gc.terms, t.id)
	if len(gc.terms) == 0 {
		gc.released = true
		g.contexts.Delete(gc.id)
		g.byProcessorContext.Delete(gc.processorID)
	}
	g.log.Info("mediactl: termination subtracted", "context", gc.id.String(), "termination", string(t.id),
		"context_deleted", gc.released, "packets_received", stats.PacketsReceived, "packets_sent", stats.PacketsSent)
	reply := h248.CommandReply{Command: c.Command, TerminationID: t.id}
	if c.Audit == nil {
		value := func(n uint64) h248.Value { return h248.Value{Text: strconv.FormatUint(n, 10)} }
		reply.Statistics = h248.StatisticsDescriptor{
			{StatName: statPacketsSent, StatValue: value(stats.PacketsSent)},
			{StatName: statPacketsReceived, StatValue: value(stats.PacketsReceived)},
		}
	}
	return reply
}

// processorErrorCode is the error code that stands for an error of the
// Processor.
func processorErrorCode(err error) int {
	switch {
	case errors.Is(err, agw.ErrNoContext):
		return 411
	case errors.Is(err, agw.ErrNoTermination):
		return 430
	case errors.Is(err, agw.ErrNoPort):
		return 510
	case errors.Is(err, agw.ErrUnknownAnnouncement):
		return 449
	}
	return 500
}

// checkMpCommand reads an Add (add is true) or a Modify of Mp.
func (g *Gateway) checkMpCommand(c h248.CommandRequest, add bool) (mpCommand, *h248.ErrorDescriptor) {
	m := mpCommand{request: c}
	if add && c.TerminationID != h248.TerminationChoose {
		// TS 29.333 5.17.2.2, 5.17.2.9: a new termination is CHOOSE.
		return m, errorDescriptor(501, "Add takes CHOOSE as the termination ID")
	}
	if c.Audit != nil {
		return m, errorDescriptor(444, "Audit")
	}
	s, e := readStream(c.Media, add, checkMpLocalControl)
	if e == nil && !add && s.hasLocal {
		// SIMPLIFIED: TS 29.333 5.17.2.3 lets the MRFC change the local
		// resources of a termination. The media node keeps the ports it
		// chose until the termination is subtracted.
		e = errorDescriptor(501, "a Local descriptor in Modify")
	}
	if e != nil {
		return m, e
	}
	m.stream = s
	if s.mode == "" && add {
		// H.248.1 (05/2002) 7.1.7.
		s.mode = h248.Inactive
	}
	m.change.Mode, _ = modeOf(s.mode)
	m.change.LocalMedia = s.local.media
	if s.remote != nil {
		m.change.Remote, m.change.RemoteMedia = s.remote.address, s.remote.media
	}
	if c.Events != nil {
		events, e := g.checkMpEvents(c.Events)
		if e != nil {
			return m, e
		}
		m.events = &events
		m.change.Events = &agw.Events{RequestID: events.requestID}
		if events.signalCompleted {
			m.change.Events.Detect = []agw.EventName{agw.SignalCompleted}
		}
	}
	if c.Signals != nil {
		signal, e := checkMpSignals(c.Signals)
		if e != nil {
			return m, e
		}
		m.signal = &signal
		m.change.Signals = &agw.Signals{Play: signal.play}
	}
	return m, nil
}

// checkMpLocalControl accepts the four stream modes of Mp (TS 29.333
// 5.7.1.1, Table 5.7.1.1.2), and ReserveValue (Table 5.7.1.1.1): the
// media node takes any payload format it is offered.
func checkMpLocalControl(lc *h248.LocalControlDescriptor, add bool) *h248.ErrorDescriptor {
	if lc == nil {
		return nil
	}
	if _, ok := modeOf(lc.StreamMode); lc.StreamMode != "" && !ok {
		// H.248.8 (03/2013) 4.2.55: Loopback is not among them.
		return errorDescriptor(517, fmt.Sprintf("stream 1 mode %s", lc.StreamMode))
	}
	if lc.ReserveGroup != nil && *lc.ReserveGroup {
		// TS 29.333 5.7.1.1, Table 5.7.1.1.1: ReserveGroup is not used.
		return errorDescriptor(501, "ReserveGroup")
	}
	for _, p := range lc.PropertyParms {
		return unsupportedItem(MpPackages, p.Name, 450)
	}
	return nil
}

// checkMpEvents reads the Events descriptor of an Add or a Modify of
// Mp: the termination heartbeat hangterm/thb (TS 29.333 5.17.2.32), the
// end of a signal g/sc (5.17.2.11), and g/cause, which the media node
// accepts and never reports, as for Iq (see checkTerminationEvents).
// Mp uses KeepActive on events (5.7.2, Table 5.7.2.3); it matters for
// the heartbeat only, the one of them that can be detected while a
// signal plays (see sendMpHeartbeat).
func (g *Gateway) checkMpEvents(e *h248.EventsDescriptor) (mpEvents, *h248.ErrorDescriptor) {
	events := mpEvents{requestID: e.RequestID}
	for _, ev := range e.EventList {
		switch h248.PkgdName(strings.ToLower(string(ev.PkgdName))) {
		case eventHeartbeat:
			var e *h248.ErrorDescriptor
			if events.heartbeat, e = g.heartbeatPeriod(ev); e != nil {
				return events, e
			}
			events.heartbeatKeepActive = ev.KeepActive
		case eventSignalCompletion, eventCause:
			// H.248.1 (05/2002) E.1.2: no parameters in an Events
			// descriptor.
			if len(ev.EvParList) > 0 {
				return events, errorDescriptor(446, ev.EvParList[0].Name)
			}
			if h248.PkgdName(strings.ToLower(string(ev.PkgdName))) == eventSignalCompletion {
				events.signalCompleted = true
			}
		case eventInactivityTimeout:
			// TS 29.333 5.7.2, Table 5.7.2.1: it/* is set on ROOT.
			return events, errorDescriptor(501, string(ev.PkgdName)+" on a termination other than ROOT")
		default:
			return events, unsupportedItem(MpPackages, ev.PkgdName, 451)
		}
	}
	return events, nil
}

// checkMpSignals reads the Signals descriptor of an Add or a Modify of
// Mp: one announcement, an/apf or aasb/play, or none, which stops what
// plays (TS 29.333 5.17.2.10).
//
// SIMPLIFIED: TS 29.333 5.17.2.9 (NOTE 3) asks for signal lists, and
// 5.7.4 (Table 5.7.4.8) for KeepActive on signals. The media node plays
// one announcement at a time and starts it anew with each Signals
// descriptor, so both are refused.
func checkMpSignals(s *h248.SignalsDescriptor) (mpSignal, *h248.ErrorDescriptor) {
	switch {
	case len(s.SeqSigLists) > 0:
		return mpSignal{}, errorDescriptor(501, "signal lists")
	case len(s.Signals) == 0:
		return mpSignal{}, nil
	case len(s.Signals) > 1:
		// TS 29.333 5.7.4, Table 5.7.4.7.
		return mpSignal{}, errorDescriptor(501, "signals played at the same time")
	}
	sig := s.Signals[0]
	name := h248.PkgdName(strings.ToLower(string(sig.SignalName)))
	if name != signalAnnouncementPlay && name != signalAASPlay {
		return mpSignal{}, unsupportedItem(MpPackages, sig.SignalName, 452)
	}
	if sig.KeepActive {
		return mpSignal{}, errorDescriptor(501, "KeepActive on a signal")
	}
	if sig.StreamID > 1 {
		return mpSignal{}, errorDescriptor(515, fmt.Sprintf("stream %d", sig.StreamID))
	}
	play, e := readPlay(name, sig)
	if e != nil {
		return mpSignal{}, e
	}
	return mpSignal{
		name:              name,
		play:              &play,
		notifyOnTimeOut:   containsReason(sig.NotifyCompletion, h248.OnTimeOut),
		notifyOnInterrupt: containsReason(sig.NotifyCompletion, h248.OnInterruptByEvent),
	}, nil
}

func containsReason(reasons []h248.NotificationReason, r h248.NotificationReason) bool {
	for _, reason := range reasons {
		if strings.EqualFold(string(reason), string(r)) {
			return true
		}
	}
	return false
}

// readPlay reads the parameters of an/apf (H.248.7 (03/2004) 4.3.1) or
// aasb/play (H.248.9 (12/2009) 8.3.1), as playSignal writes them.
//
// SIMPLIFIED: H.248.7 (03/2004) 4.5 leaves the number of cycles and the
// duration that apply when the signal leaves them out to provisioning
// for each announcement. The media node has no such provisioning: it
// plays once, with no limit. The variant av is refused, since no
// variants are provisioned, and the direction must be external: the
// media node plays to the remote party of the termination only.
func readPlay(name h248.PkgdName, sig h248.Signal) (agw.Play, *h248.ErrorDescriptor) {
	var p agw.Play
	repeatParam := paramNumberOfCycles
	if name == signalAASPlay {
		repeatParam = paramIterations
	}
	for _, parm := range sig.SigParList {
		if parm.Sublist != nil {
			return p, errorDescriptor(449, parm.Name)
		}
		v := parm.Value.Text
		switch n := strings.ToLower(parm.Name); {
		case n == paramAnnouncementName && name == signalAnnouncementPlay:
			p.Announcement = v
		case n == paramAnnouncementName:
			announcement, ok := readSegmentSpecification(v)
			if !ok {
				return p, errorDescriptor(449, paramAnnouncementName+" "+v)
			}
			p.Announcement = announcement
		case n == strings.ToLower(repeatParam):
			plays, err := strconv.ParseUint(v, 10, 31)
			if err != nil {
				return p, errorDescriptor(449, parm.Name+" "+v)
			}
			p.Repeat = int(plays)
			if plays == 0 {
				p.Repeat = agw.RepeatForever
			}
		case n == paramInterval && name == signalAASPlay:
			steps, err := strconv.ParseUint(v, 10, 31)
			if err != nil {
				return p, errorDescriptor(449, parm.Name+" "+v)
			}
			p.DelayMS = int(steps) * 10
		case n == paramDirection:
			if !strings.EqualFold(v, directionExternal) {
				return p, errorDescriptor(449, parm.Name+" "+v)
			}
		case n == "av" && name == signalAnnouncementPlay:
			return p, errorDescriptor(449, parm.Name+" "+v)
		default:
			return p, errorDescriptor(446, parm.Name)
		}
	}
	if p.Announcement == "" {
		// H.248.7 (03/2004) 4.3.1, H.248.9 (12/2009) 8.3.1.1.1.
		return p, errorDescriptor(457, paramAnnouncementName)
	}
	switch {
	case sig.SigType == h248.OnOff:
		// The signal lasts until it is turned off (H.248.1 (05/2002)
		// 7.1.11), whatever its cycles and duration.
		p.Repeat, p.DurationMS = agw.RepeatForever, 0
	case sig.Duration != nil && (name == signalAnnouncementPlay || sig.SigType == h248.TimeOut):
		// aasb/play is Brief, which takes no duration, unless its type
		// is set to TimeOut (H.248.9 (12/2009) 8.5).
		p.DurationMS = int(*sig.Duration) * 10
	}
	return p, nil
}

// SignalCompleted tells the gateway that the signal of a termination
// has ended on its own, as an announcement does after its last cycle or
// when its duration has run out. The Processor calls it with its own
// IDs of the context and the termination. When the controller has
// asked to hear of it, the gateway sends Announcement Completed (TS
// 29.333 5.17.2.11): a Notify with g/sc, the signal, and the method TO
// (H.248.1 (05/2002) E.1.2), and the time it was detected (TS 29.333
// 5.7.7).
//
// It returns at once: the Notify goes out from a goroutine of its own,
// which waits for any command that the gateway is carrying out on the
// context.
//
// SIMPLIFIED: H.248.1 (05/2002) 7.1.11 and E.1.2 also report a signal
// that a new Signals descriptor halted (SD), or that stopped for
// another reason (NC), when the controller asks for it. The MRFC of
// this project stops an announcement only by subtracting its
// termination, so the gateway reports TO, and EV for a signal that
// the heartbeat stopped (see sendMpHeartbeat).
func (g *Gateway) SignalCompleted(contextID string, id agw.TerminationID) {
	detected := g.clock.Now()
	go g.signalCompleted(contextID, id, detected)
}

func (g *Gateway) signalCompleted(contextID string, id agw.TerminationID, detected time.Time) {
	v, ok := g.byProcessorContext.Load(contextID)
	if !ok {
		return
	}
	gc := v.(*mpContext)
	gc.mu.Lock()
	var t *mpTermination
	for _, candidate := range gc.terms {
		if candidate.processorID == id {
			t = candidate
		}
	}
	if t == nil || gc.released || t.playing == "" {
		gc.mu.Unlock()
		return
	}
	signal, report, requestID := t.playing, t.notifyOnTimeOut && t.events.signalCompleted, t.events.requestID
	t.playing, t.notifyOnTimeOut, t.notifyOnInterrupt = "", false, false
	gc.mu.Unlock()
	if report {
		g.notifySignalCompletion(gc, t, requestID, signal, TimedOut, detected)
	}
}

// notifySignalCompletion sends Announcement Completed (TS 29.333
// 5.17.2.11) for signal, which ended as method tells.
func (g *Gateway) notifySignalCompletion(gc *mpContext, t *mpTermination, requestID uint32, signal h248.PkgdName, method TerminationMethod, detected time.Time) {
	g.log.Info("mediactl: signal completed", "context", gc.id.String(), "termination", string(t.id), "signal", string(signal), "method", string(method))
	reply, err := g.request([]h248.ActionRequest{{
		ContextID: gc.id,
		Commands: []h248.CommandRequest{{
			Command:       h248.Notify,
			TerminationID: t.id,
			ObservedEvents: &h248.ObservedEventsDescriptor{
				RequestID: requestID,
				ObservedEventList: []h248.ObservedEvent{{
					EventName:    eventSignalCompletion,
					TimeNotation: timeStamp(detected),
					EventParList: []h248.EventParameter{
						// SigID is a string in the text encoding
						// (H.248.1 (05/2002) E.1.2).
						{Name: paramSignalIdentity, Value: h248.Value{Text: string(signal), Quoted: true}},
						{Name: paramTerminationMethod, Value: h248.Value{Text: string(method)}},
					},
				}},
			},
		}},
	}})
	if err == nil {
		if err := rejection(reply); err != nil {
			g.log.Warn("mediactl: the controller refused the end of a signal", "termination", string(t.id), "error", err)
		}
	}
}

// setMpHeartbeat sets hangterm/thb on t as its latest Events descriptor
// asks, replacing what was set before (H.248.1 (05/2002) 7.1.9). gc.mu
// is held.
func (g *Gateway) setMpHeartbeat(gc *mpContext, t *mpTermination) {
	if t.heartbeat != nil {
		t.heartbeat.timer.Stop()
		t.heartbeat = nil
	}
	if t.events.heartbeat == 0 {
		return
	}
	t.heartbeat = &heartbeat{
		requestID: t.events.requestID,
		timerX:    t.events.heartbeat,
		timer:     g.clock.AfterFunc(t.events.heartbeat, func() { go g.sendMpHeartbeat(gc, t) }),
	}
}

// restartMpHeartbeat starts timer X again: any message about the
// termination resets it (H.248.36 (03/2013) 5.2.1.1.1). gc.mu is held.
func (g *Gateway) restartMpHeartbeat(gc *mpContext, t *mpTermination) {
	hb := t.heartbeat
	if hb == nil {
		return
	}
	hb.timer.Stop()
	hb.timer = g.clock.AfterFunc(hb.timerX, func() { go g.sendMpHeartbeat(gc, t) })
}

// sendMpHeartbeat is Termination heartbeat indication (TS 29.333
// 5.17.2.32), as sendHeartbeat is for Iq. Unless the event is
// KeepActive, its detection stops the announcement that plays (H.248.1
// (05/2002) 7.1.9), which g/sc then reports with the method EV when
// the controller asked for it (7.1.11, E.1.2).
func (g *Gateway) sendMpHeartbeat(gc *mpContext, t *mpTermination) {
	detected := g.clock.Now()
	gc.mu.Lock()
	hb := t.heartbeat
	live := !gc.released && gc.terms[t.id] == t
	var interrupted h248.PkgdName
	report := false
	if hb != nil && live && t.playing != "" && !t.events.heartbeatKeepActive {
		if err := g.processor.Modify(g.ctx, gc.processorID, t.processorID, agw.TerminationRequest{Signals: &agw.Signals{}}); err != nil {
			g.log.Warn("mediactl: the processor could not stop a signal", "context", gc.id.String(), "termination", string(t.id), "error", err)
		} else {
			interrupted, report = t.playing, t.notifyOnInterrupt && t.events.signalCompleted
			t.playing, t.notifyOnTimeOut, t.notifyOnInterrupt = "", false, false
		}
	}
	requestID := t.events.requestID
	gc.mu.Unlock()
	if hb == nil || !live || g.ctx.Err() != nil {
		return
	}
	reply, err := g.request([]h248.ActionRequest{{
		ContextID: gc.id,
		Commands:  []h248.CommandRequest{notifyRequest(t.id, hb.requestID, eventHeartbeat)},
	}})
	if err == nil {
		if err := rejection(reply); err != nil {
			g.log.Warn("mediactl: the controller has no record of the termination", "termination", string(t.id), "error", err)
		}
	}
	if report {
		g.notifySignalCompletion(gc, t, requestID, interrupted, InterruptedByEvent, detected)
	}
	gc.mu.Lock()
	defer gc.mu.Unlock()
	if !gc.released && gc.terms[t.id] == t && t.heartbeat == hb {
		g.restartMpHeartbeat(gc, t)
	}
}
