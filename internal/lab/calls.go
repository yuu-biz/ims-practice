package lab

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

// callTransitions holds every change of state of one branch of a call
// (see Call.branches). An event that a state does not list leaves the
// branch as it is. That makes the table safe against the copies a call
// produces: each node captures each message once on receipt and once on
// sending, and the copies of different nodes may reach the Lab out of
// order. A late 183 after the 200 or a second copy of the 200 changes
// nothing.
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

	// branches holds each dialog that the INVITE created, by To tag. A
	// forking proxy sends the INVITE to several callees, and each answers
	// with a tag of its own, so one Call-ID has an early and a confirmed
	// dialog per branch (RFC 3261 12.1, 13.2.2.4). State and Status are
	// derived from them (see derive), so the 200 of one branch counts
	// whether or not another branch ended first.
	branches map[string]branch
	// moves counts the changes of branches, to tell which was last.
	moves int
}

// branch is one dialog of a call: where it stands, the latest response
// to the INVITE that moved it, and when it last moved.
type branch struct {
	state  CallState
	status int
	moved  int
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

// apply moves the branch that m belongs to, derives the state of the
// session from its branches, and reports whether the call changed.
func (c *Call) apply(m *sip.Message, at time.Time) bool {
	event := classify(m)
	tag, ok := c.branchOf(m, event)
	if !ok {
		return false
	}
	b, known := c.branches[tag]
	if !known {
		b.state = CallCalling
	}
	next, ok := callTransitions[b.state][event]
	if !ok {
		return false
	}
	if c.branches == nil {
		c.branches = map[string]branch{}
	}
	c.moves++
	b.state, b.moved = next, c.moves
	if !m.IsRequest() {
		b.status = m.StatusCode
	}
	c.branches[tag] = b

	changed := false
	state, status := derive(c.branches)
	if state != c.State {
		c.State, changed = state, true
	}
	if status != 0 && (c.Status == nil || *c.Status != status) {
		c.Status, changed = &status, true
	}
	if state.ended() {
		if c.Ended == nil {
			c.Ended = &at
		}
	} else if c.Ended != nil {
		// A branch that came later reopened the call.
		c.Ended, changed = nil, true
	}
	return changed
}

// branchOf is the To tag of the branch that m belongs to. A response
// carries it in To. A BYE carries it in To when the caller sends it and
// in From when the callee does, so it is the tag of a branch that the
// Lab knows.
func (c *Call) branchOf(m *sip.Message, event callEvent) (string, bool) {
	switch event {
	case eventNone:
		return "", false
	case eventBYE:
		for _, header := range []string{"To", "From"} {
			if tag, _ := tagOf(m, header); tag != "" {
				if _, ok := c.branches[tag]; ok {
					return tag, true
				}
			}
		}
		return "", false
	}
	tag, _ := tagOf(m, "To")
	return tag, true
}

// derive gives the state of the session and its status from its
// branches. One confirmed branch confirms the session, whatever order
// the branches ended in. Without one, a branch still in an early dialog
// keeps the session early. When every branch has ended, the session
// shows how: terminated if one of them was a call, failed if one of
// them got a failure response, and cancelled only if the caller
// withdrew from every one. The status is that of the branch that last
// moved into the state.
func derive(branches map[string]branch) (CallState, int) {
	for _, state := range []CallState{CallConfirmed, CallEarly, CallTerminated, CallFailed, CallCancelled} {
		var latest branch
		for _, b := range branches {
			if b.state == state && b.moved > latest.moved {
				latest = b
			}
		}
		if latest.moved > 0 {
			return state, latest.status
		}
	}
	return CallCalling, 0
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
	_, ok := tagOf(m, "To")
	return ok
}

// tagOf is the tag parameter of the From or To header of m.
func tagOf(m *sip.Message, header string) (string, bool) {
	na, err := sip.ParseNameAddr(m.Get(header))
	if err != nil {
		return "", false
	}
	return na.Params.Get("tag")
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
