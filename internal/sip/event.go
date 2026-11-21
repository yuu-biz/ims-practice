package sip

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file holds the header fields of SIP events (RFC 6665): Event,
// which names the event package, and Subscription-State, which every
// NOTIFY carries. The subscription itself is a usage of a dialog; the
// subscriber's side is in subscription.go, the notifier's in
// notifier.go.

// SubState is the state of a subscription in the state machines of RFC
// 6665 4.1.2 and 4.2.2. Pending, active and terminated are also the
// values of Subscription-State.
type SubState string

const (
	// SubscriptionInit is a subscriber that has not sent its SUBSCRIBE.
	SubscriptionInit SubState = "init"
	// SubscriptionNotifyWait is a subscriber that has sent its SUBSCRIBE
	// and waits for the first NOTIFY.
	SubscriptionNotifyWait SubState = "notify_wait"
	// SubscriptionPending is a subscription that the notifier has
	// received but not yet authorized.
	SubscriptionPending SubState = "pending"
	SubscriptionActive  SubState = "active"
	// SubscriptionTerminated is a subscription that has ended. A
	// subscriber that wants the state again sends a new SUBSCRIBE
	// outside the dialog.
	SubscriptionTerminated SubState = "terminated"
)

// Reasons for terminated in Subscription-State (RFC 6665 4.1.3).
const (
	// ReasonDeactivated asks the subscriber to subscribe again at once.
	ReasonDeactivated = "deactivated"
	// ReasonTimeout ends a subscription that was not refreshed, or one
	// that the subscriber ended with Expires 0.
	ReasonTimeout = "timeout"
	// ReasonRejected ends a subscription that is no longer authorized.
	ReasonRejected = "rejected"
	// ReasonNoResource ends a subscription whose resource is gone. The
	// subscriber should not subscribe again.
	ReasonNoResource = "noresource"
)

// SubscriptionState is the value of the Subscription-State header
// field (RFC 6665 8.2.3).
type SubscriptionState struct {
	State SubState
	// Expires is the time left on an active or pending subscription.
	// The notifier MUST send it with those two states (4.2.2).
	Expires time.Duration
	// Reason says why a terminated subscription ended, such as
	// ReasonTimeout.
	Reason string
	// RetryAfter is how long to wait before subscribing again; zero
	// means that the header field has none.
	RetryAfter time.Duration
}

// ParseSubscriptionState reads Subscription-State. A value other than
// active, pending and terminated is an error: RFC 6665 4.1.3 gives an
// extension state no meaning this package knows.
func ParseSubscriptionState(value string) (SubscriptionState, error) {
	head, rest, _ := strings.Cut(value, ";")
	s := SubscriptionState{State: SubState(strings.ToLower(strings.TrimSpace(head)))}
	switch s.State {
	case SubscriptionActive, SubscriptionPending, SubscriptionTerminated:
	default:
		return SubscriptionState{}, fmt.Errorf("sip: unknown Subscription-State %q", value)
	}
	params := parseParams(rest)
	seconds := func(name string) (time.Duration, error) {
		v, ok := params.Get(name)
		if !ok {
			return 0, nil
		}
		n, err := ParseDeltaSeconds(v)
		return time.Duration(n) * time.Second, err
	}
	var err error
	if s.State != SubscriptionTerminated {
		// 4.1.3: expires has no meaning for terminated, and the
		// subscriber MUST ignore it.
		if s.Expires, err = seconds("expires"); err != nil {
			return SubscriptionState{}, fmt.Errorf("sip: Subscription-State expires: %w", err)
		}
	}
	if s.RetryAfter, err = seconds("retry-after"); err != nil {
		return SubscriptionState{}, fmt.Errorf("sip: Subscription-State retry-after: %w", err)
	}
	s.Reason, _ = params.Get("reason")
	return s, nil
}

func (s SubscriptionState) String() string {
	var params Params
	if s.State == SubscriptionTerminated {
		if s.Reason != "" {
			params.Set("reason", s.Reason)
		}
	} else {
		params.Set("expires", strconv.Itoa(int(s.Expires/time.Second)))
	}
	if s.RetryAfter > 0 {
		params.Set("retry-after", strconv.Itoa(int(s.RetryAfter/time.Second)))
	}
	return string(s.State) + params.String()
}

// sameEvent reports whether two Event values name the same event, as a
// NOTIFY must to match its SUBSCRIBE: the event type and the id
// parameter compare byte by byte, a value with id never matches one
// without, and no other parameter counts (RFC 6665 8.2.1).
func sameEvent(a, b string) bool {
	typeA, idA, hasA := splitEvent(a)
	typeB, idB, hasB := splitEvent(b)
	return typeA == typeB && hasA == hasB && idA == idB
}

func splitEvent(value string) (eventType, id string, hasID bool) {
	head, rest, _ := strings.Cut(value, ";")
	id, hasID = parseParams(rest).Get("id")
	return strings.TrimSpace(head), id, hasID
}

// EventType is the event type of an Event value without its
// parameters, such as "reg" (RFC 6665 8.2.1).
func EventType(value string) string {
	t, _, _ := splitEvent(value)
	return t
}
