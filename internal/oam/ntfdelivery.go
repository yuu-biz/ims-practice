package oam

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand/v2"
	"net/http"
	"slices"
	"sync"

	"github.com/yuu-biz/ims-practice/internal/jex"
	"github.com/yuu-biz/ims-practice/internal/metrics"
	"github.com/yuu-biz/ims-practice/internal/mns"
)

// The results of sending a notification to a recipient, which the
// metric oam_alarm_notifications_total counts.
const (
	notificationDelivered = "delivered"
	notificationFailed    = "failed"
)

// notificationMetrics counts the notifications that the OAM sends, by
// their type and whether the recipient took them.
type notificationMetrics struct {
	sent *metrics.Counter
}

func newNotificationMetrics(reg *metrics.Registry) (notificationMetrics, error) {
	types := make([]string, len(alarmNotificationTypes))
	for i, t := range alarmNotificationTypes {
		types[i] = string(t)
	}
	sent, err := reg.NewCounter("oam_alarm_notifications_total",
		"Alarm notifications that the OAM sent to the recipient of a subscription, by type and by whether the recipient answered with 204.",
		metrics.Label{Name: "notification_type", Values: types},
		metrics.Label{Name: "result", Values: []string{notificationDelivered, notificationFailed}})
	return notificationMetrics{sent: sent}, err
}

// recipient is a subscription with its filter read.
type recipient struct {
	sub    NtfSubscriptionControl
	filter *jex.Condition
}

// newRecipient is the recipient of sub, or nil with the reason when its
// filter cannot be read, which the check of the PUT keeps from
// happening.
func newRecipient(sub NtfSubscriptionControl) (*recipient, error) {
	r := &recipient{sub: sub}
	if f := sub.Attributes.NotificationFilter; f != "" {
		cond, err := jex.ParseCondition(f)
		if err != nil {
			return nil, err
		}
		r.filter = cond
	}
	return r, nil
}

// selects says whether the subscription is sent n: its notificationTypes
// name the type of n, or are absent, and its notificationFilter, if any,
// matches the body of n (TS 28.622 4.3.22.1). The scope of every
// subscription here is the whole SubNetwork, so it selects every
// notification.
func (r *recipient) selects(n alarmNotification) bool {
	types := r.sub.Attributes.NotificationTypes
	if types != nil && !slices.Contains(types, n.notificationType) {
		return false
	}
	return r.filter == nil || r.filter.Matches(n.document)
}

// notify sends n to every subscription that selects it, at once, at most
// NotificationConcurrency of them, each within NotificationTimeout, and
// returns after the last of them has answered. The watcher sends the
// next notification of the node only then, so that each recipient gets
// the notifications of one node in the order of the node's journal. The
// subscriptions are read at the start.
//
// A recipient that does not take n is marked Missed, and from then on
// is sent nothing but the notifyAlarmListRebuilt of the whole list that
// the miss owes it, which asks the OSS to read the list again
// (sendOwed). So an OSS that has stopped answering costs the watcher of
// each node one NotificationTimeout, once, and not one for each
// notification: the others are not held up behind it, and the journals
// of the nodes do not run past their watchers. The mark is in the
// subscription store, so the watchers of every node and every instance
// skip the recipient alike. A skip while the turn of the mark is taken
// is recorded (markSkipped), so that the notifyAlarmListRebuilt on its
// way, which the OSS may have read the list for already, does not clear
// the mark when the OSS takes it, and the OSS is sent another.
//
// notify fails when it could not read the subscriptions, or could not
// write the mark of a miss or a skip: then some recipient has neither
// the notification nor the mark that would have it read the list
// again. The watcher sends the notification again, and saves nothing
// until it has gone (deliver). A recipient that took it is put in
// taken, and passed over when it is sent again, so that while the store
// cannot be written, the recipients that answer are not sent it at
// every try.
//
// The OSS that is sent a notification again after it missed one gets no
// copy of the ones it missed: TS 28.111 8.5.2 lets the producer say,
// with alarmListAlignmentRequirement, that notifications were not sent,
// and the OSS reads the list again (5, REQ-FM-MC-5).
//
// SIMPLIFIED: TS 28.111 V20.0.0 8.5.1 and 8.5.2. A recipient that missed
// a notification is sent a notifyAlarmListRebuilt of the whole list,
// with ALIGNMENT_REQUIRED and "indeterminate", although no list was
// rebuilt, and to that subscription alone, where 8.5.1 notifies every
// subscriber that a part of the list was rebuilt. The notifications
// that it missed are not sent again: that would take the reliable
// notification transport of TS 28.532, and the OSS is told instead to
// read the list, which 8.5.2 has the notification ask for when
// notifications were not sent.
func (s *Server) notify(ctx context.Context, taken deliveries, n alarmNotification) error {
	subs, err := s.ntfSubscriptions.NtfSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("the subscriptions could not be read: %w", err)
	}
	// mu guards taken and failed, which the POSTs write as they end.
	var (
		mu     sync.Mutex
		failed error
		wg     sync.WaitGroup
	)
	fail := func(err error) {
		mu.Lock()
		failed = err
		mu.Unlock()
	}
	sem := make(chan struct{}, s.notificationConcurrency)
	for _, sub := range subs {
		r, err := newRecipient(sub)
		if err != nil {
			s.log.Error("a subscription has a filter that cannot be read", "id", sub.ID, "error", err.Error())
			continue
		}
		if !r.selects(n) {
			continue
		}
		d := delivery{subscription: sub.ID, address: sub.Attributes.NotificationRecipientAddress}
		mu.Lock()
		done := taken[d]
		mu.Unlock()
		if done {
			continue
		}
		if sub.Missed {
			if err := s.markSkipped(ctx, sub); err != nil {
				fail(err)
			}
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			// A POST that stopped because the watcher is stopping is not
			// the recipient's miss: the watcher saves nothing then, and
			// the next holder of the watch sends the notification again.
			if s.post(ctx, sub, n) {
				mu.Lock()
				taken[d] = true
				mu.Unlock()
				return
			}
			if ctx.Err() != nil {
				return
			}
			if err := s.markMissed(ctx, sub.ID); err != nil {
				fail(err)
			}
		})
	}
	wg.Wait()
	return failed
}

// deliveries are the recipients that took the notification of one call
// of deliver.
type deliveries map[delivery]bool

// delivery is a subscription that took the notification at the
// address. A subscription that a PUT has moved to another address since
// has not taken it there.
type delivery struct {
	subscription string
	address      string
}

// markMissed records a miss of the subscription id in the store, for
// every watcher. A subscription that is gone needs no mark.
func (s *Server) markMissed(ctx context.Context, id string) error {
	err := s.ntfSubscriptions.MarkNtfSubscriptionMissed(ctx, id, s.now().Add(s.missedRetryInterval))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("the mark of a missed notification to %s could not be written: %w", id, err)
	}
	return nil
}

// markSkipped records, if need be, that the marked subscription sub was
// passed over for a notification. The alarm of the notification changed
// before notify read the subscriptions, so a notifyAlarmListRebuilt
// whose turn is taken after that read reaches the OSS after the change:
// the skip needs no record. One whose turn the read found taken may have
// reached the OSS before it, and the OSS read the list without it; then
// the store raises the epoch of the mark, so that the answer to that
// notification does not clear it (MarkNtfSubscriptionSkipped).
func (s *Server) markSkipped(ctx context.Context, sub NtfSubscriptionControl) error {
	if !sub.Claimed {
		return nil
	}
	err := s.ntfSubscriptions.MarkNtfSubscriptionSkipped(ctx, sub.ID, sub.MissedEpoch)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("the skip of a marked subscription %s could not be written: %w", sub.ID, err)
	}
	return nil
}

// sendOwed sends the notifyAlarmListRebuilt that a miss owes each
// recipient whose turn has come, whether or not the alarms change. It
// runs at every round of RunNotifications, and for each subscription
// at most once every MissedRetryInterval over every watcher and
// instance: the one that takes the turn (ClaimNtfSubscriptionRetry)
// sends it, on a goroutine of its own, so that a recipient that still
// does not answer holds up nothing. It goes to the recipient of the
// copy read before the turn: the turn is taken at the epoch of that
// copy, and a PutNtfSubscription of a marked subscription raises the
// epoch. Once the recipient has taken it, the mark is cleared, if no
// miss, skip or replacement has raised its epoch since; the next
// notifications then go to the recipient again. Otherwise the mark
// stays, and the next turn sends another.
func (s *Server) sendOwed(ctx context.Context) {
	subs, err := s.ntfSubscriptions.NtfSubscriptions(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("the subscriptions could not be read for the notifications that misses owe", "error", err.Error())
		}
		return
	}
	now := s.now()
	for _, sub := range subs {
		if !sub.Missed || sub.RetryAt.After(now) {
			continue
		}
		r, err := newRecipient(sub)
		if err != nil {
			continue
		}
		claimed, err := s.ntfSubscriptions.ClaimNtfSubscriptionRetry(ctx, sub.ID, sub.MissedEpoch, now, now.Add(s.missedRetryInterval))
		if err != nil && !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
			s.log.Warn("the turn to send an owed notification could not be taken", "id", sub.ID, "error", err.Error())
		}
		if !claimed {
			continue
		}
		owed, err := s.mns.alarmListRebuilt(s.mns.subNetwork, mns.ReasonIndeterminate, newNotificationID(), now)
		if err != nil {
			continue
		}
		s.owed.Go(func() {
			// A subscription that does not select the notification, by
			// its notificationTypes or notificationFilter, has asked not
			// to be sent it. Its mark is cleared all the same, without
			// the OSS being told, as it would be for any notification it
			// does not select: the next notifications go to it again.
			if r.selects(owed) && !s.post(ctx, sub, owed) {
				return
			}
			if _, err := s.ntfSubscriptions.ClearNtfSubscriptionMissed(ctx, sub.ID, sub.MissedEpoch); err != nil && !errors.Is(err, ErrNotFound) && ctx.Err() == nil {
				s.log.Warn("the mark of a missed notification could not be cleared", "id", sub.ID, "error", err.Error())
			}
		})
	}
}

// post sends n to the recipient of sub, and says whether the recipient
// took it: an answer of 204 within NotificationTimeout. TS 32.158 5.5.4
// and TS28111_FaultNotifications.yaml have the recipient answer 204 to
// the POST at its address, and give no other success, so any other
// answer is a failure, another 2xx too. So is a redirect, which is not
// followed. A client that follows a 301 or a 302 may send a GET in place
// of the POST (RFC 9110 15.4.2, 15.4.3), without the notification. A 307
// or a 308 keeps the POST (15.4.8, 15.4.9), but sends the notification
// to an address that the subscription does not name, while TS 32.158
// 5.5.4 sends it to the sink that the subscriber gave (5.5.2); a
// recipient that moves replaces its subscription.
func (s *Server) post(ctx context.Context, sub NtfSubscriptionControl, n alarmNotification) bool {
	result := notificationFailed
	defer func() { s.notificationMetrics.sent.Inc(string(n.notificationType), result) }()
	b, err := json.Marshal(n.body)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, s.notificationTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Attributes.NotificationRecipientAddress, bytes.NewReader(b))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.notificationClient.Do(req)
	if err != nil {
		s.log.Warn("a notification was not delivered", "id", sub.ID, "notification_type", string(n.notificationType), "error", err.Error())
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRequestBody))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		s.log.Warn("a notification was refused", "id", sub.ID, "notification_type", string(n.notificationType), "status", resp.StatusCode)
		return false
	}
	result = notificationDelivered
	return true
}

// newNotificationID is a random notificationId, in the 53 bits that a
// JSON number holds without loss.
func newNotificationID() int64 { return mathrand.Int64N(1<<53-1) + 1 }
