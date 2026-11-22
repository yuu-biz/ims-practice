package mediactl

import (
	"context"
	"time"

	"github.com/yuu-biz/ims-practice/internal/agw"
)

// Processor is what a Gateway of Mp carries out the commands of its
// controller with: the media node in the role of the MRFP. Its
// operations and types are those with which the MRFC controls an MRFP,
// so that *agw.Client is one.
//
// A context holds any number of terminations (TS 29.333 5.4, Table
// 5.4.1): one for an announcement, three or more for a conference. Add
// fails as a whole. The Gateway names each termination that it asks
// for, so that it finds each one in what Add returns. The last
// termination that is subtracted takes the context with it (H.248.1
// (05/2002) 6.1.2); a later call for the context fails with
// agw.ErrNoContext.
//
// The end of an announcement goes the other way: the processor tells
// its Gateway with Gateway.SignalCompleted.
type Processor interface {
	Add(ctx context.Context, terminations ...agw.TerminationRequest) (agw.Context, error)
	AddTermination(ctx context.Context, id string, t agw.TerminationRequest) (agw.Termination, error)
	Modify(ctx context.Context, id string, termination agw.TerminationID, change agw.TerminationRequest) error
	SubtractTermination(ctx context.Context, id string, termination agw.TerminationID) (agw.Termination, bool, error)
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
// Controller hands it to ControllerConfig.Notify. Its agw.Notify names
// the termination as Add and AddTermination returned it, and the
// RequestID of the Events descriptor that asked for the event.
type Notification struct {
	agw.Notify
	// Signal is the signal that ended, such as an/apf, and Method how.
	Signal string
	Method TerminationMethod
	// Detected is when the MRFP detected the event, to a hundredth of a
	// second, or the zero time when the Notify does not say (TS 29.333
	// 5.7.7).
	Detected time.Time
}
