package sip

import (
	"reflect"
	"testing"
	"time"
)

func TestSessionExpiresHeader(t *testing.T) {
	tests := []struct {
		in   string
		want SessionExpires
		out  string
	}{
		{"1800", SessionExpires{Interval: 30 * time.Minute}, "1800"},
		{"1800;refresher=uac", SessionExpires{Interval: 30 * time.Minute, Refresher: RefresherUAC}, "1800;refresher=uac"},
		{"90 ; Refresher=UAS;x=y", SessionExpires{Interval: 90 * time.Second, Refresher: RefresherUAS}, "90;refresher=uas"},
	}
	for _, tt := range tests {
		got, err := ParseSessionExpires(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseSessionExpires(%q) = %+v, %v; want %+v", tt.in, got, err, tt.want)
		}
		if s := got.String(); s != tt.out {
			t.Errorf("String() = %q, want %q", s, tt.out)
		}
	}
	for _, in := range []string{"", "soon", "1800;refresher=proxy"} {
		if _, err := ParseSessionExpires(in); err == nil {
			t.Errorf("ParseSessionExpires(%q) succeeded", in)
		}
	}
	if got, err := ParseMinSE("120;x=y"); err != nil || got != 2*time.Minute {
		t.Errorf("ParseMinSE = %v, %v; want 2m", got, err)
	}
	// RFC 4028 4: the compact form.
	m, err := Parse([]byte("UPDATE sip:a@example.com SIP/2.0\r\nx: 1800;refresher=uac\r\n\r\n"))
	if err != nil || m.Get("Session-Expires") != "1800;refresher=uac" {
		t.Errorf("compact form x read as %q, %v", m.Get("Session-Expires"), err)
	}
}

// sessionRefresh is a session refresh request with the given Supported
// and session timer header fields.
func sessionRefresh(supported, sessionExpires, minSE string) *Message {
	m := &Message{Method: INVITE, RequestURI: calleeURI}
	for name, value := range map[string]string{"Supported": supported, "Session-Expires": sessionExpires, "Min-SE": minSE} {
		if value != "" {
			m.Add(name, value)
		}
	}
	return m
}

func headerValues(headers []HeaderField) map[string]string {
	values := map[string]string{}
	for _, h := range headers {
		values[h.Name] = h.Value
	}
	return values
}

// Table 2 of RFC 4028 9, and the rules around it.
func TestUASAnswersTheSessionTimer(t *testing.T) {
	wants := SessionTimerPolicy{Interval: 30 * time.Minute}
	tests := []struct {
		name    string
		policy  SessionTimerPolicy
		before  SessionTimer
		req     *Message
		headers map[string]string
		after   SessionTimer
	}{
		{"the UAC asks and leaves the refresher open: the UAC refreshes",
			SessionTimerPolicy{}, SessionTimer{}, sessionRefresh("timer", "1800", ""),
			map[string]string{"Session-Expires": "1800;refresher=uac", "Require": "timer"},
			SessionTimer{Interval: 30 * time.Minute}},
		{"the UAC wants to refresh",
			SessionTimerPolicy{}, SessionTimer{}, sessionRefresh("timer", "1800;refresher=uac", ""),
			map[string]string{"Session-Expires": "1800;refresher=uac", "Require": "timer"},
			SessionTimer{Interval: 30 * time.Minute}},
		{"the UAC wants the UAS to refresh",
			SessionTimerPolicy{}, SessionTimer{}, sessionRefresh("timer", "1800;refresher=uas", ""),
			map[string]string{"Session-Expires": "1800;refresher=uas", "Require": "timer"},
			SessionTimer{Interval: 30 * time.Minute, LocalRefresher: true}},
		{"a proxy asked and the UAC does not support the extension: the UAS refreshes, without Require",
			SessionTimerPolicy{}, SessionTimer{}, sessionRefresh("100rel", "1800", ""),
			map[string]string{"Session-Expires": "1800;refresher=uas"},
			SessionTimer{Interval: 30 * time.Minute, LocalRefresher: true}},
		{"nobody asked, and the UAS wants a timer",
			wants, SessionTimer{}, sessionRefresh("timer", "", ""),
			map[string]string{"Session-Expires": "1800;refresher=uac", "Require": "timer"},
			SessionTimer{Interval: 30 * time.Minute}},
		{"nobody asked, the UAS wants a timer, and the request has a larger Min-SE",
			wants, SessionTimer{}, sessionRefresh("timer", "", "3600"),
			map[string]string{"Session-Expires": "3600;refresher=uac", "Require": "timer"},
			SessionTimer{Interval: time.Hour, MinSE: time.Hour}},
		{"nobody asked, and the UAS wants none: the session does not expire",
			SessionTimerPolicy{}, SessionTimer{}, sessionRefresh("timer", "", ""),
			map[string]string{}, SessionTimer{}},
		{"a refresh that leaves the refresher open keeps the UAS refreshing",
			SessionTimerPolicy{}, SessionTimer{Interval: 30 * time.Minute, LocalRefresher: true}, sessionRefresh("timer", "1800", ""),
			map[string]string{"Session-Expires": "1800;refresher=uas", "Require": "timer"},
			SessionTimer{Interval: 30 * time.Minute, LocalRefresher: true}},
		{"a refresh without Session-Expires turns the timer off",
			SessionTimerPolicy{}, SessionTimer{Interval: 30 * time.Minute, MinSE: 2 * time.Minute}, sessionRefresh("timer", "", ""),
			map[string]string{}, SessionTimer{MinSE: 2 * time.Minute}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.policy.Answer(tt.req, tt.before)
			if a.TooSmall != 0 {
				t.Fatalf("rejected with Min-SE %v", a.TooSmall)
			}
			if got := headerValues(a.Headers); !reflect.DeepEqual(got, tt.headers) {
				t.Errorf("2xx carries %v, want %v", got, tt.headers)
			}
			if a.Timer != tt.after {
				t.Errorf("state is %+v, want %+v", a.Timer, tt.after)
			}
		})
	}
}

// RFC 4028 9: an interval below the local minimum gets 422 with Min-SE,
// but only from a UAC that can act on it.
func TestUASRefusesASessionIntervalTooSmall(t *testing.T) {
	policy := SessionTimerPolicy{MinSE: 10 * time.Minute}
	before := SessionTimer{Interval: 30 * time.Minute}
	if a := policy.Answer(sessionRefresh("timer", "300", ""), before); a.TooSmall != 10*time.Minute || a.Timer != before {
		t.Errorf("answer %+v, want 422 with Min-SE 600 and the state unchanged", a)
	}
	if a := (SessionTimerPolicy{}).Answer(sessionRefresh("timer", "60", ""), SessionTimer{}); a.TooSmall != MinSessionInterval {
		t.Errorf("answer %+v to 60 s, want 422 with the minimum of 90 s", a)
	}
	a := policy.Answer(sessionRefresh("", "300", ""), SessionTimer{})
	if a.TooSmall != 0 || a.Timer.Interval != 5*time.Minute || !a.Timer.LocalRefresher {
		t.Errorf("answer %+v to a UAC without the extension, want the interval accepted and the UAS refreshing", a)
	}
}

func TestUACSessionTimerRequests(t *testing.T) {
	policy := SessionTimerPolicy{Interval: 30 * time.Minute}
	tests := []struct {
		name   string
		policy SessionTimerPolicy
		timer  SessionTimer
		want   map[string]string
	}{
		// RFC 4028 7.1: the refresher is left to the negotiation.
		{"initial INVITE", policy, SessionTimer{}, map[string]string{"Session-Expires": "1800"}},
		{"initial INVITE of a UA that asks for no timer", SessionTimerPolicy{}, SessionTimer{}, map[string]string{}},
		{"initial INVITE of a UA with a minimum of its own", SessionTimerPolicy{Interval: 30 * time.Minute, MinSE: 10 * time.Minute},
			SessionTimer{}, map[string]string{"Session-Expires": "1800", "Min-SE": "600"}},
		// RFC 4028 7.4.
		{"INVITE again after a 422", policy, SessionTimer{MinSE: time.Hour},
			map[string]string{"Session-Expires": "3600", "Min-SE": "3600"}},
		{"refresh by the refresher", policy, SessionTimer{Interval: 15 * time.Minute, LocalRefresher: true},
			map[string]string{"Session-Expires": "900;refresher=uac"}},
		{"request of the side that does not refresh", policy, SessionTimer{Interval: 15 * time.Minute},
			map[string]string{"Session-Expires": "900;refresher=uas"}},
		{"refresh after a 422 with a larger Min-SE", policy, SessionTimer{Interval: 15 * time.Minute, LocalRefresher: true, MinSE: 20 * time.Minute},
			map[string]string{"Session-Expires": "1200;refresher=uac", "Min-SE": "1200"}},
	}
	for _, tt := range tests {
		if got := headerValues(tt.policy.RequestHeaders(tt.timer)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: request carries %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestUACReadsTheSessionTimerOfThe2xx(t *testing.T) {
	ok := func(sessionExpires string) *Message {
		m := &Message{StatusCode: 200}
		if sessionExpires != "" {
			m.Add("Session-Expires", sessionExpires)
		}
		return m
	}
	before := SessionTimer{Interval: 10 * time.Minute, LocalRefresher: true, MinSE: 2 * time.Minute}
	tests := []struct {
		sessionExpires string
		want           SessionTimer
	}{
		// RFC 4028 7.2. The sender of the request was the UAC.
		{"1800;refresher=uac", SessionTimer{Interval: 30 * time.Minute, LocalRefresher: true, MinSE: 2 * time.Minute}},
		{"1800;refresher=uas", SessionTimer{Interval: 30 * time.Minute, MinSE: 2 * time.Minute}},
		{"", SessionTimer{MinSE: 2 * time.Minute}},
	}
	for _, tt := range tests {
		if got := before.After2xx(ok(tt.sessionExpires)); got != tt.want {
			t.Errorf("after a 2xx with Session-Expires %q: %+v, want %+v", tt.sessionExpires, got, tt.want)
		}
	}
}

func TestUACLearnsMinSEFromA422(t *testing.T) {
	tooSmall := &Message{StatusCode: 422}
	tooSmall.Add("Min-SE", "3600")
	learned, ok := SessionTimer{}.After422(tooSmall)
	if !ok || learned.MinSE != time.Hour {
		t.Fatalf("After422 = %+v, %v; want Min-SE 1h", learned, ok)
	}
	// The same Min-SE again teaches nothing, so the UA gives up instead
	// of sending the same request for ever (RFC 4028 10).
	if _, ok := learned.After422(tooSmall); ok {
		t.Error("a 422 with the Min-SE already learned asks for another try")
	}
	if _, ok := (SessionTimer{}).After422(&Message{StatusCode: 422}); ok {
		t.Error("a 422 without Min-SE asks for another try")
	}
}

func TestSessionTimerDurations(t *testing.T) {
	tests := []struct {
		interval, refresh, expire time.Duration
	}{
		// RFC 4028 7.2 and 10: refresh at half the interval; give up 32
		// s, or a third of the interval if that is less, before its end.
		{30 * time.Minute, 15 * time.Minute, 30*time.Minute - 32*time.Second},
		{90 * time.Second, 45 * time.Second, 60 * time.Second},
	}
	for _, tt := range tests {
		timer := SessionTimer{Interval: tt.interval}
		if !timer.Active() || timer.RefreshAfter() != tt.refresh || timer.ExpireAfter() != tt.expire {
			t.Errorf("interval %v: refresh after %v, expire after %v; want %v and %v",
				tt.interval, timer.RefreshAfter(), timer.ExpireAfter(), tt.refresh, tt.expire)
		}
	}
	if (SessionTimer{}).Active() {
		t.Error("a dialog without a session interval has an active timer")
	}
}
