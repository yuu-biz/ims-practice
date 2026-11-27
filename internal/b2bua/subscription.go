package b2bua

import (
	"errors"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sip"
)

// A subscription that a SUBSCRIBE within the dialogs of a Call creates
// shares those dialogs with the session, whichever side subscribes (RFC
// 6665 4.5.2): a usage of each dialog, which a BYE does not end (RFC
// 5057 5.5). The Call keeps both legs for it after the session has
// ended, as each end does, so that the NOTIFY that ends the
// subscription still crosses. The subscription ends, and with it the
// usage of each leg (sip.CallDialog.AddSubscriptionUsage), once its
// final NOTIFY has a response, once one of its requests fails in a way
// that ends it (RFC 5057 4.2, 5.1, 5.2; RFC 6665 4.1.2.2, 4.2.2), or
// once it has expired and its last NOTIFY has had time to come (RFC
// 5057 4.2).

// relayedSubscription is one subscription that shares the dialogs of a
// Call.
type relayedSubscription struct {
	// ends end the usage of the subscription on the caller's leg and on
	// the callee's.
	ends []func()
	// stopExpiry stops the timer that ends the subscription after its
	// expiry, which each 2xx to a SUBSCRIBE and each NOTIFY of it sets
	// anew; run tells a timer that fires late that it has been replaced.
	stopExpiry func() bool
	run        uint64
}

// expiryGrace is how long the Call keeps a subscription past its
// expiry: the notifier ends one that expires with a NOTIFY (RFC 6665
// 4.2.1.4), which may take a transaction's time to cross.
var expiryGrace = 64 * sip.DefaultTimers().T1

// errRefused is what relayed gets for a request of a subscription that
// the role refused (Hooks.Request), and that got no response of the
// other leg.
var errRefused = errors.New("b2bua: the role refused the request")

// ofSubscription reports whether a request of method m belongs to a
// subscription, not to the session (RFC 5057 5.3).
func ofSubscription(m sip.Method) bool { return m == sip.SUBSCRIBE || m == sip.NOTIFY }

// outlivesSession reports whether req belongs to a subscription that
// the Call keeps both legs for, so that it waits for its turn even once
// the session has ended (fromCaller, fromCallee).
func (c *Call) outlivesSession(req *sip.Message) bool {
	if !ofSubscription(req.Method) {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.subscriptions[sip.EventKey(req.Get("Event"))]
	return ok
}

// addSubscription makes a SUBSCRIBE that creates a subscription within
// the dialogs a usage of both legs, before it goes on: the first NOTIFY
// may come before the 2xx. It reports whether the SUBSCRIBE created one
// (relayed then ends it again unless the SUBSCRIBE gets a 2xx), and
// whether to refuse it: once the session has ended, the dialogs take no
// new usage (RFC 5057 5.6). A refresh of a subscription that the Call
// has, or a SUBSCRIBE before the callee has answered, creates none.
func (c *Call) addSubscription(req *sip.Message) (created, refuse bool) {
	key := sip.EventKey(req.Get("Event"))
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.subscriptions[key]; ok || c.out == nil {
		return false, false
	}
	var ends []func()
	for _, l := range []leg{c.in, c.out} {
		end, err := l.AddSubscriptionUsage()
		if err != nil {
			for _, end := range ends {
				end()
			}
			return false, errors.Is(err, sip.ErrCallEnded)
		}
		ends = append(ends, end)
	}
	if c.subscriptions == nil {
		c.subscriptions = map[string]*relayedSubscription{}
	}
	sub := &relayedSubscription{ends: ends}
	c.subscriptions[key] = sub
	expires, err := sip.SubscribeExpires(req, 0)
	if err == nil && expires > 0 {
		c.expireSubscription(key, sub, expires)
	}
	return true, false
}

// relayed takes the final response of the other leg, or err, to req, a
// SUBSCRIBE or a NOTIFY that the Call has relayed: it ends the
// subscription when that ends it, and otherwise sets its expiry anew.
// created tells that req created the subscription (addSubscription).
func (c *Call) relayed(req *sip.Message, created bool, resp *sip.Message, err error) {
	key := sip.EventKey(req.Get("Event"))
	switch req.Method {
	case sip.SUBSCRIBE:
		switch {
		case err == nil && resp.StatusCode < 300:
			if expires, err := sip.SubscribeExpires(resp, 0); err == nil && expires > 0 {
				c.setExpiry(key, expires)
			}
		case created, refreshEnds(resp, err):
			c.endSubscription(key)
		}
	case sip.NOTIFY:
		state, perr := sip.ParseSubscriptionState(req.Get("Subscription-State"))
		switch {
		case perr == nil && state.State == sip.SubscriptionTerminated, sip.NotifyEndsSubscription(resp, err), errors.Is(err, sip.ErrCallEnded):
			// The notifier has ended the subscription, whatever became of
			// the NOTIFY that says so; or the subscriber has lost it, or
			// the leg toward it is gone.
			c.endSubscription(key)
		case perr == nil && state.Expires > 0:
			// RFC 6665 4.1.3: how long the subscription has left.
			c.setExpiry(key, state.Expires)
		}
	}
}

// refreshEnds reports whether the failure of a SUBSCRIBE that refreshes
// a subscription ends it: a response that says so (RFC 6665 4.1.2.2),
// none at all (RFC 5057 4.2), or a leg toward the notifier that is gone.
func refreshEnds(resp *sip.Message, err error) bool {
	if err != nil {
		return errors.Is(err, sip.ErrTimeout) || errors.Is(err, sip.ErrCallEnded)
	}
	return sip.EndsSubscription(resp.StatusCode)
}

// setExpiry has the subscription with key end after d and the grace for
// its last NOTIFY, in place of the expiry it had.
func (c *Call) setExpiry(key string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub, ok := c.subscriptions[key]; ok {
		c.expireSubscription(key, sub, d)
	}
}

// expireSubscription starts the timer that ends sub, the subscription
// with key, after d and the grace for its last NOTIFY. c.mu is held.
func (c *Call) expireSubscription(key string, sub *relayedSubscription, d time.Duration) {
	if sub.stopExpiry != nil {
		sub.stopExpiry()
	}
	sub.run++
	run := sub.run
	sub.stopExpiry = time.AfterFunc(d+expiryGrace, func() {
		c.mu.Lock()
		if c.subscriptions[key] != sub || sub.run != run {
			// A refresh or a NOTIFY has set the expiry anew meanwhile, or
			// the subscription has ended and another taken its Event.
			c.mu.Unlock()
			return
		}
		delete(c.subscriptions, key)
		c.mu.Unlock()
		c.log.Info("subscription within the dialogs expired without a NOTIFY that ends it")
		for _, end := range sub.ends {
			end()
		}
	}).Stop
}

// endSubscription ends the subscription with key on both legs. The
// dialog of a leg whose session has ended then ends with its last
// usage.
func (c *Call) endSubscription(key string) {
	c.mu.Lock()
	sub, ok := c.subscriptions[key]
	delete(c.subscriptions, key)
	c.mu.Unlock()
	if !ok {
		return
	}
	if sub.stopExpiry != nil {
		sub.stopExpiry()
	}
	for _, end := range sub.ends {
		end()
	}
}
