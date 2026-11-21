package dashboard

import (
	"strings"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// CallState is where an INVITE session stands, as the messages of its
// Call-ID show it.
type CallState string

const (
	// CallCalling: the INVITE is out, and no provisional response that
	// creates an early dialog has come back.
	CallCalling CallState = "calling"
	// CallEarly: a provisional response with a To tag created an early
	// dialog (RFC 3261 12.1).
	CallEarly CallState = "early"
	// CallConfirmed: a 2xx confirmed the dialog (RFC 3261 13.2.2.4).
	CallConfirmed CallState = "confirmed"
	// CallTerminated: a BYE ended the session (RFC 3261 15).
	CallTerminated CallState = "terminated"
	// CallFailed: the INVITE got a 3xx to 6xx final response.
	CallFailed CallState = "failed"
	// CallCancelled: the INVITE got 487 after a CANCEL (RFC 3261 9.2).
	CallCancelled CallState = "cancelled"
)

// callEvent is what a message of the call means for its state.
type callEvent int

const (
	eventNone callEvent = iota
	// eventEarlyResponse is a 101-199 response to the INVITE with a To
	// tag. 100 Trying is hop by hop and creates no dialog.
	eventEarlyResponse
	eventSuccessResponse
	// eventRequestTerminated is 487, the answer to the INVITE once a
	// CANCEL has reached the callee.
	eventRequestTerminated
	eventFailureResponse
	eventBYE
)

// callTransitions holds every change of state. An event that a state
// does not list leaves the call as it is. That makes the table safe
// against the copies a call produces: each node captures each message
// once on receipt and once on sending, and the copies of different
// nodes may reach the dashboard out of order. A late 183 after the 200
// or a second copy of the 200 changes nothing.
var callTransitions = map[CallState]map[callEvent]CallState{
	CallCalling: {
		eventEarlyResponse:     CallEarly,
		eventSuccessResponse:   CallConfirmed,
		eventRequestTerminated: CallCancelled,
		eventFailureResponse:   CallFailed,
	},
	CallEarly: {
		eventEarlyResponse:     CallEarly,
		eventSuccessResponse:   CallConfirmed,
		eventRequestTerminated: CallCancelled,
		eventFailureResponse:   CallFailed,
		// RFC 3261 15: the caller may send BYE on an early dialog.
		eventBYE: CallTerminated,
	},
	CallConfirmed: {
		eventBYE: CallTerminated,
	},
}

func (s CallState) ended() bool {
	return s == CallTerminated || s == CallFailed || s == CallCancelled
}

// Call is one call: an INVITE session, keyed by its Call-ID, or the
// sessions that share an icid-value (see joinCall). Its fields other
// than Legs and Messages are those of the session that started first,
// the caller's.
type Call struct {
	CallID string `json:"call_id"`
	// Legs are the Call-IDs of the sessions of the call, one per dialog,
	// in the order they started; the first is CallID. A call that passes
	// routeing B2BUAs has a dialog between each two of the UEs and the
	// ASs on its way (TS 24.229 5.7.5).
	Legs  []string  `json:"legs"`
	From  string    `json:"from"`
	To    string    `json:"to"`
	State CallState `json:"state"`
	// Status is the latest response to the INVITE that moved the state,
	// such as 183, 200 or 486; null before the first one.
	Status   *int       `json:"status"`
	Started  time.Time  `json:"started"`
	Ended    *time.Time `json:"ended"`
	Messages int        `json:"messages"`
}

// newCall starts a call from its INVITE.
func newCall(invite *sip.Message, at time.Time) *Call {
	return &Call{
		CallID:  invite.Get("Call-ID"),
		From:    addressURI(invite.Get("From")),
		To:      addressURI(invite.Get("To")),
		State:   CallCalling,
		Started: at,
	}
}

// apply moves the session on m and reports whether it changed.
func (c *Call) apply(m *sip.Message, at time.Time) bool {
	next, ok := callTransitions[c.State][classify(m)]
	if !ok {
		return false
	}
	changed := next != c.State
	c.State = next
	if !m.IsRequest() && (c.Status == nil || *c.Status != m.StatusCode) {
		status := m.StatusCode
		c.Status = &status
		changed = true
	}
	if next.ended() && c.Ended == nil {
		c.Ended = &at
	}
	return changed
}

// classify maps a message of the call to its event. Only responses to
// INVITE count: a 200 to PRACK, CANCEL or BYE must not confirm a call.
func classify(m *sip.Message) callEvent {
	if m.IsRequest() {
		if m.Method == sip.BYE {
			return eventBYE
		}
		return eventNone
	}
	if cseqMethod(m) != sip.INVITE {
		return eventNone
	}
	switch code := m.StatusCode; {
	case code == 100:
		return eventNone
	case code < 200:
		if !hasToTag(m) {
			return eventNone
		}
		return eventEarlyResponse
	case code < 300:
		return eventSuccessResponse
	case code == 487:
		return eventRequestTerminated
	default:
		return eventFailureResponse
	}
}

func hasToTag(m *sip.Message) bool {
	to, err := sip.ParseNameAddr(m.Get("To"))
	if err != nil {
		return false
	}
	_, ok := to.Params.Get("tag")
	return ok
}

// addressURI is the URI of a From, To or Contact value, without the
// display name and the tag.
func addressURI(value string) string {
	na, err := sip.ParseNameAddr(value)
	if err != nil {
		return value
	}
	return na.URI
}

// icidValue is the icid-value of the P-Charging-Vector of m, the
// identifier of the call for charging (TS 24.229 7.2A.5), or "".
func icidValue(m *sip.Message) string {
	for _, p := range strings.Split(m.Get("P-Charging-Vector"), ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(p), "=")
		if strings.EqualFold(strings.TrimSpace(name), "icid-value") {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}
