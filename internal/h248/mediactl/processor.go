package mediactl

import (
	"context"
	"errors"
	"net/netip"
	"time"
)

// Processor is the set of operations with which an MRFC has an MRFP
// play announcements and mix conferences: the Mp reference point (TS
// 29.333), next to Relay for Iq. Each method is one call related
// procedure of Mp (TS 29.333 5.17.2), or several of them done in one
// command, as 5.17.2.1 (the NOTE of Table 5.17.2.1.1) allows:
//
//   - Add is Reserve IMS Resources (5.17.2.2) for each termination of a
//     new context. A termination with a remote address makes it Reserve
//     and Configure IMS Resources (5.17.2.4), and one with a signal
//     Start Announcement (5.17.2.9).
//   - AddTermination is the same for one termination of a context that
//     exists, as when a party joins a conference (5.17.2.30).
//   - Modify is Configure IMS Resources (5.17.2.3), and with a signal
//     Start Announcement or, with an empty one, Stop Announcement
//     (5.17.2.10).
//   - SubtractTermination is Release IMS Termination (5.17.2.5).
//
// The end of an announcement, Announcement Completed (5.17.2.11), goes
// the other way: the processor tells its Gateway with
// Gateway.SignalCompleted, and the Controller hands the Notify to the
// function of ControllerConfig.Notify.
//
// As with Relay, two kinds of value implement it. Controller does, by
// sending the H.248 commands to an MRFP that has registered with it.
// The processor given to a Gateway does, and the Gateway calls it to
// carry out the commands that a controller sends.
//
// A context holds any number of terminations (TS 29.333 5.4, Table
// 5.4.1): one for an announcement, three or more for a conference. The
// last termination that is subtracted takes the context with it
// (H.248.1 (05/2002) 6.1.2); a later call for the context fails with
// ErrNoContext.
type Processor interface {
	Add(ctx context.Context, terminations ...TerminationRequest) (Context, error)
	AddTermination(ctx context.Context, contextID string, t TerminationRequest) (Termination, error)
	Modify(ctx context.Context, contextID string, id TerminationID, change TerminationRequest) error
	SubtractTermination(ctx context.Context, contextID string, id TerminationID) (Statistics, error)
}

// Statistics is what a termination carried while it was in its
// context, as the Subtract reply of Mp reports it: the statistics of
// the rtp package (H.248.1 (05/2002) E.12.4), which count RTP only.
type Statistics struct {
	// PacketsSent and PacketsReceived count the RTP packets (rtp/ps,
	// rtp/pr).
	PacketsSent     uint64
	PacketsReceived uint64
}

// TerminationID names a termination within its context. Controller
// returns the H.248 termination ID; a processor behind a Gateway uses
// IDs of its own, which the Gateway maps.
type TerminationID string

// Mode is the stream mode of a termination, which H.248 carries in the
// LocalControl descriptor (TS 29.333 5.7.1.1, Table 5.7.1.1.2; H.248.1
// (05/2002) 7.1.7). Sending and receiving are seen from outside the
// context: a SendOnly termination passes media from the context to its
// remote party, a ReceiveOnly one from its remote party into the
// context. The values are those of the SDP direction attributes, as in
// the HTTP API of the media node.
type Mode string

const (
	SendReceive Mode = "sendrecv"
	SendOnly    Mode = "sendonly"
	ReceiveOnly Mode = "recvonly"
	Inactive    Mode = "inactive"
)

// TerminationRequest is what Add, AddTermination and Modify set on one
// termination. A field left at its zero value keeps what the
// termination has, with one exception: Mode.
type TerminationRequest struct {
	// Mode is the stream mode. Controller needs it on every request and
	// always writes it: H.248 makes a termination that an Add leaves
	// without a mode Inactive, where the HTTP API of the media node
	// makes it sendrecv. Gateway always sets it on Add, Inactive when
	// the command has none; on Modify, empty keeps the mode.
	Mode Mode
	// Local describes, on Add, the stream that the termination is to
	// receive: the formats of the m= line and the bandwidth, the Codec
	// List of TS 29.333 5.17.2.2. Its Address is left zero, for the
	// MRFP to choose. Modify does not change it.
	Local Media
	// Remote is where and in which form the termination sends, as the
	// SDP of the party it faces gives it: the Remote descriptor. RTCP
	// goes to the port above the address. A zero Address keeps what
	// the termination has.
	Remote Media
	// Signals, when not nil, replaces what the termination plays
	// (H.248.1 (05/2002) 7.1.11). An empty Signals stops it.
	Signals *Signals
	// Events, when not nil, replaces the events that the termination
	// reports (H.248.1 (05/2002) 7.1.9).
	Events *Events
}

// Termination is a termination that Add or AddTermination made.
type Termination struct {
	ID TerminationID
	// Local is where the termination receives RTP: what the SDP that
	// the party it faces receives is to show. RTCP is on the port
	// above.
	Local netip.AddrPort
}

// Context is a context that Add made.
type Context struct {
	// ID names the context in the later calls. It means something only
	// to the Processor that returned it.
	ID string
	// Terminations are those of the request, in its order.
	Terminations []Termination
}

// Signals is the Signals descriptor of a termination: the media that
// the MRFP itself sends to the party of the termination. TS 29.333
// 5.7.4 (Table 5.7.4.7) plays one signal at a time.
type Signals struct {
	// Play plays a provisioned announcement: Start Announcement (TS
	// 29.333 5.17.2.9).
	Play *Play
}

// RepeatForever is the Repeat of an announcement that plays until its
// duration runs out or the controller stops it.
const RepeatForever = -1

// Play is an announcement to play, with the parameters of RFC 4240 3
// that shape it, as the MRFC reads them from the Request-URI.
type Play struct {
	// Announcement is the name of a provisioned announcement.
	Announcement string
	// Repeat is the number of plays in all: Announcement Cycles (TS
	// 29.333 5.17.2.9). Zero plays once.
	Repeat int
	// Delay is the silence between two plays, and Duration the most the
	// announcement may last; zero means no limit. H.248 counts both in
	// steps of 10 ms; Controller rounds up.
	Delay    time.Duration
	Duration time.Duration
}

// EventName names an event that a termination can report, as the
// package item that H.248 reports it with.
type EventName string

// SignalCompleted is the event g/sc, Signal Completion (H.248.1
// (05/2002) E.1.2): a signal has ended. It is the Announcement
// Completed of TS 29.333 5.17.2.11.
const SignalCompleted EventName = "g/sc"

// Events is the Events descriptor of a termination: the events to
// report, and the RequestID that each report carries back (H.248.1
// (05/2002) 7.1.9). Controller adds the termination heartbeat of TS
// 29.333 5.17.2.32 to every Events descriptor it writes, so the
// processor behind a Gateway sees only the events of Detect. A
// RequestID of 0 is written as 1: H.248.1 (05/2002) 7.2.7 leaves a
// Notify with RequestID 0 for further study.
type Events struct {
	RequestID uint32
	Detect    []EventName
}

func (e *Events) detects(name EventName) bool {
	if e == nil {
		return false
	}
	for _, d := range e.Detect {
		if d == name {
			return true
		}
	}
	return false
}

// TerminationMethod tells how a signal ended: the parameter Meth of
// g/sc (H.248.1 (05/2002) E.1.2).
type TerminationMethod string

const (
	// TimedOut: the signal timed out or otherwise completed on its own,
	// as an announcement does after its last cycle.
	TimedOut TerminationMethod = "TO"
	// InterruptedByEvent: an event stopped the signal.
	InterruptedByEvent TerminationMethod = "EV"
	// HaltedBySignals: a new Signals descriptor stopped it.
	HaltedBySignals TerminationMethod = "SD"
	// NotCompleted: it stopped, or never started, for another reason.
	NotCompleted TerminationMethod = "NC"
)

// Notification is a Notify from an MRFP, with the event of its
// ObservedEvents descriptor (H.248.1 (05/2002) 7.2.7, 7.1.17), as
// Controller hands it to ControllerConfig.Notify.
type Notification struct {
	// Context and Termination name the termination as Add and
	// AddTermination returned them.
	Context     string
	Termination TerminationID
	// RequestID is that of the Events descriptor that asked for the
	// event.
	RequestID uint32
	Event     EventName
	// Signal is the signal that ended, such as an/apf, and Method how.
	Signal string
	Method TerminationMethod
	// Detected is when the MRFP detected the event, to a hundredth of a
	// second, or the zero time when the Notify does not say (TS 29.333
	// 5.7.7).
	Detected time.Time
}

var (
	// ErrNoTermination means that the context has no termination of
	// that ID. A media gateway answers it with error 430 (H.248.8
	// (03/2013) 4.2.12). Controller returns it for 430 and 435, along
	// with ErrNoContext.
	ErrNoTermination = errors.New("mediactl: no such termination")
	// ErrUnknownAnnouncement means that the processor has no
	// announcement of that name. A media gateway answers it with error
	// 449, an unknown parameter value (TS 29.333 5.17.1).
	ErrUnknownAnnouncement = errors.New("mediactl: no such announcement")
)
