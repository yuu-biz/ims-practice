package sip

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// This file holds the session timer of RFC 4028 as pure logic: the
// Session-Expires and Min-SE header fields, what a UAC asks for, what a
// UAS answers, and the state both keep for a dialog. The session timer
// lets the UAs, and the proxies that keep call state, find out that a
// session has died without a BYE: one side refreshes the session with
// an UPDATE or a re-INVITE before the session interval is over, and
// whoever sees no refresh in time ends the call. The UA in ua.go runs
// the timers and sends the refreshes.

// MinSessionInterval is the smallest session interval, and the value of
// Min-SE where the header field is absent (RFC 4028 4, 5).
const MinSessionInterval = 90 * time.Second

// Refresher is the refresher parameter of Session-Expires: which side
// of the transaction that carries it refreshes the session. It names a
// role in that one transaction, the UAC or the UAS of the INVITE or
// UPDATE, not the caller or the callee: when the callee refreshes with
// an UPDATE, the callee is the UAC.
type Refresher string

const (
	RefresherUAC Refresher = "uac"
	RefresherUAS Refresher = "uas"
)

// SessionExpires is the value of the Session-Expires header field (RFC
// 4028 4).
//
//	Session-Expires: 1800;refresher=uac
type SessionExpires struct {
	// Interval is the session interval.
	Interval time.Duration
	// Refresher is "" when the value leaves the choice open.
	Refresher Refresher
}

func ParseSessionExpires(value string) (SessionExpires, error) {
	seconds, rest, _ := strings.Cut(value, ";")
	n, err := ParseDeltaSeconds(seconds)
	if err != nil {
		return SessionExpires{}, fmt.Errorf("sip: Session-Expires: %w", err)
	}
	se := SessionExpires{Interval: time.Duration(n) * time.Second}
	if r, ok := parseParams(rest).Get("refresher"); ok {
		switch se.Refresher = Refresher(strings.ToLower(r)); se.Refresher {
		case RefresherUAC, RefresherUAS:
		default:
			return SessionExpires{}, fmt.Errorf("sip: Session-Expires: unknown refresher %q", r)
		}
	}
	return se, nil
}

func (se SessionExpires) String() string {
	var params Params
	if se.Refresher != "" {
		params.Set("refresher", string(se.Refresher))
	}
	return deltaSeconds(se.Interval) + params.String()
}

// ParseMinSE reads the Min-SE header field (RFC 4028 5).
func ParseMinSE(value string) (time.Duration, error) {
	seconds, _, _ := strings.Cut(value, ";")
	n, err := ParseDeltaSeconds(seconds)
	if err != nil {
		return 0, fmt.Errorf("sip: Min-SE: %w", err)
	}
	return time.Duration(n) * time.Second, nil
}

func deltaSeconds(d time.Duration) string { return strconv.Itoa(int(d / time.Second)) }

// SessionTimerPolicy is what a UA wants of the session timer.
type SessionTimerPolicy struct {
	// Interval is the session interval that the UA asks for: in the
	// Session-Expires of its INVITE, and in its 2xx to an INVITE that
	// asked for none. RFC 4028 4 recommends 1800 seconds. With 0 the UA
	// asks for no session timer; it still lists the extension in
	// Supported and takes part when the peer or a proxy asks for one.
	Interval time.Duration
	// MinSE is the smallest session interval the UA accepts; a request
	// for less gets 422 (Session Interval Too Small). 0 means 90
	// seconds.
	MinSE time.Duration
}

func (p SessionTimerPolicy) minSE() time.Duration { return max(p.MinSE, MinSessionInterval) }

// SessionTimer is the session timer state of a dialog (RFC 4028 7.2,
// 9). The zero value is a dialog without a session timer.
type SessionTimer struct {
	// Interval is the session interval of the most recent 2xx to a
	// session refresh request. 0 means that the session does not
	// expire.
	Interval time.Duration
	// LocalRefresher tells that this UA refreshes the session. When it
	// is false, the peer does.
	LocalRefresher bool
	// MinSE is the largest Min-SE this UA has learned on the dialog,
	// from 422 responses and from the peer's session refresh requests
	// (RFC 4028 7.4). 0 means none.
	MinSE time.Duration
}

// Active reports whether the session expires unless it is refreshed.
func (t SessionTimer) Active() bool { return t.Interval > 0 }

// RefreshAfter is how long after the 2xx the refresher sends the next
// session refresh request: half the session interval (RFC 4028 7.2).
func (t SessionTimer) RefreshAfter() time.Duration { return t.Interval / 2 }

// ExpireAfter is how long after the 2xx a UA that has seen no refresh
// ends the session with a BYE: the session interval less 32 seconds or
// a third of the interval, whichever is smaller, so that the BYE goes
// out before the proxies on the way drop the session (RFC 4028 10).
func (t SessionTimer) ExpireAfter() time.Duration {
	return t.Interval - min(32*time.Second, t.Interval/3)
}

// RequestHeaders are the session timer header fields of a session
// refresh request that this UA sends on a dialog whose state is t: the
// initial INVITE, for which t is the zero value, or a later re-INVITE
// or UPDATE (RFC 4028 7.1, 7.4). The UA lists the timer option tag in
// Supported itself.
func (p SessionTimerPolicy) RequestHeaders(t SessionTimer) []HeaderField {
	var headers []HeaderField
	// 7.4: Min-SE MUST go out once the UA has learned one. 7.1: before
	// that it MAY, which a policy with a minimum of its own uses.
	minSE := max(t.MinSE, p.MinSE)
	if minSE > 0 {
		headers = append(headers, HeaderField{Name: "Min-SE", Value: deltaSeconds(max(minSE, MinSessionInterval))})
	}
	se := SessionExpires{Interval: p.Interval}
	if t.Active() {
		// 7.4: the current interval, and the roles as they are, so that
		// they do not change with every refresh. The sender of the
		// request is its UAC.
		se = SessionExpires{Interval: t.Interval, Refresher: RefresherUAS}
		if t.LocalRefresher {
			se.Refresher = RefresherUAC
		}
	}
	if se.Interval == 0 {
		return headers
	}
	// 7.1, 7.4: never less than Min-SE.
	se.Interval = max(se.Interval, minSE, MinSessionInterval)
	return append(headers, HeaderField{Name: "Session-Expires", Value: se.String()})
}

// SessionTimerAnswer is what a UAS does with a session refresh request
// (RFC 4028 9).
type SessionTimerAnswer struct {
	// TooSmall, when not 0, rejects the request with 422 (Session
	// Interval Too Small) and this value in Min-SE.
	TooSmall time.Duration
	// Headers are the session timer header fields of the 2xx:
	// Session-Expires and Require. They are empty when the session gets
	// no timer.
	Headers []HeaderField
	// Timer is the state of the dialog once the 2xx is sent.
	Timer SessionTimer
}

// Answer is RFC 4028 9 for req, an INVITE or UPDATE that this UA is
// about to accept on a dialog whose state is t.
func (p SessionTimerPolicy) Answer(req *Message, t SessionTimer) SessionTimerAnswer {
	next := SessionTimer{MinSE: t.MinSE}
	var requestMinSE time.Duration
	if v := req.Get("Min-SE"); v != "" {
		if m, err := ParseMinSE(v); err == nil {
			requestMinSE = m
			// 7.4: a Min-SE of the peer's request counts for the
			// refreshes this UA sends later.
			next.MinSE = max(next.MinSE, m)
		}
	}
	uacSupports := req.Supports(OptionTimer)
	se, err := ParseSessionExpires(req.Get("Session-Expires"))
	asked := err == nil
	switch {
	case asked && uacSupports && se.Interval < p.minSE():
		// A UAC that does not support the extension could not act on
		// the 422, so its request is accepted with the interval that a
		// proxy asked for.
		return SessionTimerAnswer{TooSmall: p.minSE(), Timer: t}
	case asked:
		// The UAS may shorten the interval but never lengthen it; this
		// one copies it.
		next.Interval = se.Interval
	case p.Interval > 0:
		// The request asked for no timer, and this UA wants one.
		next.Interval = max(p.Interval, requestMinSE, MinSessionInterval)
	default:
		// A 2xx without Session-Expires: the session does not expire.
		return SessionTimerAnswer{Timer: next}
	}

	// Table 2 of RFC 4028 9.
	refresher := RefresherUAS
	switch {
	case !uacSupports:
		// Only this UA can refresh.
	case se.Refresher != "":
		// The UAS cannot override the choice of the UAC.
		refresher = se.Refresher
	case t.Active() && t.LocalRefresher:
		// The choice is the UAS's. On a dialog that has a timer, the
		// roles stay as they are.
	default:
		// On a new session the UAC refreshes, which is also what IR.92
		// 2.2.8 asks of a UE that answers.
		refresher = RefresherUAC
	}
	next.LocalRefresher = refresher == RefresherUAS

	headers := []HeaderField{{Name: "Session-Expires", Value: SessionExpires{Interval: next.Interval, Refresher: refresher}.String()}}
	if uacSupports {
		// MUST when the UAC refreshes, since it has to read the
		// response to know that; SHOULD when the UAS does.
		headers = append(headers, HeaderField{Name: "Require", Value: OptionTimer})
	}
	return SessionTimerAnswer{Headers: headers, Timer: next}
}

// After2xx is the state of the dialog after resp, a 2xx to a session
// refresh request that this UA sent while the state was t (RFC 4028
// 7.2). A 2xx without Session-Expires turns the session timer off.
func (t SessionTimer) After2xx(resp *Message) SessionTimer {
	next := SessionTimer{MinSE: t.MinSE}
	se, err := ParseSessionExpires(resp.Get("Session-Expires"))
	if err != nil {
		return next
	}
	next.Interval = se.Interval
	// This UA was the UAC of the request.
	next.LocalRefresher = se.Refresher != RefresherUAS
	return next
}

// After422 is the state after a 422 (Session Interval Too Small) to a
// session refresh request: the Min-SE of the response joins what the
// UA has learned, for the request it sends next (RFC 4028 7.3, 7.4). It
// reports false when the 422 has no Min-SE to learn from, so that the
// UA does not send the same request again.
func (t SessionTimer) After422(resp *Message) (SessionTimer, bool) {
	minSE, err := ParseMinSE(resp.Get("Min-SE"))
	if err != nil || minSE <= t.MinSE {
		return t, false
	}
	t.MinSE = minSE
	return t, true
}
