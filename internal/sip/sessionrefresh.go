package sip

import (
	"errors"
	"slices"
	"time"
)

// This file runs the session timer of RFC 4028 in the UA: it starts the
// timers after each 2xx to a session refresh request, sends the
// refreshes, and ends a session that nobody refreshed. What the header
// fields say, and who refreshes, is decided in sessiontimer.go.

// ErrSessionExpired is why a call ends when no session refresh request
// got its 2xx before the session expired (RFC 4028 10).
var ErrSessionExpired = errors.New("sip: the session expired without a refresh")

// afterFunc starts a timer of the session timer (UAConfig.AfterFunc).
// Close waits for f once it has started, and f does not start after
// Close.
func (ua *UA) afterFunc(d time.Duration, f func()) (stop func() bool) {
	run := func() {
		if !ua.enter() {
			return
		}
		defer ua.running.Done()
		f()
	}
	if ua.cfg.AfterFunc != nil {
		return ua.cfg.AfterFunc(d, run)
	}
	return time.AfterFunc(d, run).Stop
}

// setSession makes t the session timer state of the dialog and starts
// its timers anew. The UA calls it for each 2xx to a session refresh
// request within the confirmed dialog, sent or received: the session
// expiration counts from that 2xx (RFC 4028 7.2, 9). c.mu is held.
func (d *CallDialog) setSession(t SessionTimer) {
	d.session = t
	d.stopSessionTimers()
	if !t.Active() || d.dialog.State != DialogConfirmed {
		return
	}
	// The run number tells a timer that fires late that it has been
	// replaced.
	d.sessionRun++
	run := d.sessionRun
	ua := d.c.ua
	ua.log.Info("sip session timer", "call_id", d.dialog.ID.CallID, "interval", t.Interval.String(), "local_refresher", t.LocalRefresher)
	if t.LocalRefresher {
		d.stopRefresh = ua.afterFunc(t.RefreshAfter(), func() { d.refreshSession(run) })
	}
	// RFC 4028 10 asks the side that does not refresh to end a session
	// that got no refresh. The refresher runs the same timer: when its
	// refreshes fail, its session is just as dead.
	d.stopExpire = ua.afterFunc(t.ExpireAfter(), func() { d.expireSession(run) })
}

// stopSessionTimers stops the timers of the session timer. c.mu is
// held.
func (d *CallDialog) stopSessionTimers() {
	for _, stop := range []*func() bool{&d.stopRefresh, &d.stopExpire} {
		if *stop != nil {
			(*stop)()
			*stop = nil
		}
	}
}

// refreshSession sends a session refresh request (RFC 4028 7.4, 10):
// an UPDATE without a session description when the peer allows UPDATE,
// and otherwise a re-INVITE that offers the session description this UA
// sent last, unchanged. The 2xx starts the timers anew. After a 422 the
// refresh goes out once more, with the Min-SE it learned; after a 491
// it waits out the glare (RFC 3261 14.1).
//
// SIMPLIFIED: RFC 4028 10. After any other failure the refresher SHOULD
// follow the rules of that status code and try again where it can, for
// instance with credentials after a 401. The UA only logs the failure,
// and the timer that ends the session then sends the BYE. No node of
// this project challenges a request within a dialog.
func (d *CallDialog) refreshSession(run uint64) {
	c := d.c
	for range 2 {
		c.mu.Lock()
		if run != d.sessionRun || d.dialog.State != DialogConfirmed {
			c.mu.Unlock()
			return
		}
		byUpdate := d.peerAllowsUpdate || len(d.localSDP.Content) == 0
		body := d.localSDP
		c.mu.Unlock()

		var resp *Message
		var err error
		if byUpdate {
			resp, err = d.Update(Body{}, c.ua.own(UPDATE, false, nil)...)
		} else {
			resp, err = d.ReInvite(body, c.ua.own(INVITE, false, nil)...)
		}
		switch {
		case err != nil:
			c.ua.log.Warn("session refresh failed", "error", err.Error())
			return
		case resp.StatusCode == 422:
			continue
		case resp.StatusCode == 491:
			c.ua.afterFunc(GlareDelay(d.in == nil), func() { d.refreshSession(run) })
			return
		case resp.StatusCode >= 300:
			c.ua.log.Warn("session refresh failed", "status", resp.StatusCode)
			return
		default:
			return
		}
	}
}

// expireSession ends a session that got no refresh in time with a BYE
// (RFC 4028 10).
func (d *CallDialog) expireSession(run uint64) {
	c := d.c
	c.mu.Lock()
	expired := run == d.sessionRun
	c.mu.Unlock()
	if !expired {
		return
	}
	if err := d.byeOwn(ErrSessionExpired); err != nil && !errors.Is(err, ErrCallEnded) {
		c.ua.log.Warn("BYE for an expired session failed", "error", err.Error())
	}
}

// sessionRequestHeaders are the session timer header fields of a
// session refresh request that this UA sends within the dialog. Only a
// confirmed dialog has a session timer. c.mu is held.
func (d *CallDialog) sessionRequestHeaders() []HeaderField {
	if d.dialog.State != DialogConfirmed {
		return nil
	}
	return d.c.ua.cfg.SessionTimer.RequestHeaders(d.session)
}

// sent notes body as the session description that this UA sent last.
// c.mu is held.
func (d *CallDialog) sent(body Body) {
	if len(body.Content) > 0 {
		d.localSDP = Body{Type: body.Type, Content: slices.Clone(body.Content)}
	}
}
