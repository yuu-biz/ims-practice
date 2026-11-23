package diameter

import (
	"context"
	"time"
)

// This file is the per-request form of the Answer timer. RFC 6733 5.6.2
// has the timeout of a wait for an answer come from an
// application-defined timer, and the applications of one node do not
// agree on how long that is: the base protocol answers a CER or a DWR at
// once, while a serving node answers an SGd TFR only once the UE has
// sent its delivery report, which it waits for as long as TR1N (TS
// 24.011 6.2.2.2, TS 23.204 6.5 steps 4 and 6). TS 23.204 6.5a NOTE 1
// says as much: a short timer at the SMS-GMSC leaves the IP-SM-GW too
// little time, and is reconfigured to be longer.
//
// Timers.Answer is the timer of the node, and so of its base protocol
// exchanges. A role that sends a request whose answer takes longer puts
// its own timer in the context of that one request.

// answerTimeoutKey is the key of the per-request timeout in a context.
type answerTimeoutKey struct{}

// WithAnswerTimeout returns a context in which one request waits d for
// its answer, in place of the Answer timer of the node that sends it.
// Peer.Request and Server.Request honour it, and PeerGroup.Request
// gives it to all of its attempts together, as it does Timers.Answer. A
// d of zero or less, and a context without one, keep the node's timer.
//
// It does not lengthen a deadline that the caller's own context carries:
// a request that waits d still ends when that deadline passes.
func WithAnswerTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, answerTimeoutKey{}, d)
}

// answerTimeout is how long a request sent with ctx waits for its
// answer.
func answerTimeout(ctx context.Context, timers Timers) time.Duration {
	if d, ok := ctx.Value(answerTimeoutKey{}).(time.Duration); ok && d > 0 {
		return d
	}
	return timers.Answer
}
