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
		"Alarm notifications that the OAM sent to the recipient of a subscription, by type and by whether the recipient answered with 2xx.",
		metrics.Label{Name: "notification_type", Values: types},
		metrics.Label{Name: "result", Values: []string{notificationDelivered, notificationFailed}})
	return notificationMetrics{sent: sent}, err
}

// recipient is a subscription as one round of sending has it: the
// subscription and its filter, read once for the round, with the mark
// of a miss as the round has changed it.
type recipient struct {
	mu     sync.Mutex
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

// notify sends each notification, in order, to every subscription that
// selects it. A notification goes to its recipients at once, at most
// NotificationConcurrency of them, each within NotificationTimeout, and
// the next one goes after the last of them has answered, so that each
// recipient gets the notifications of one node in the order of the
// node's journal. The subscriptions are read once, at the start.
//
// A recipient that does not take a notification is marked Missed, and
// from then on is sent nothing but the notifyAlarmListRebuilt of the
// whole list that the miss owes it, which asks the OSS to read the list
// again (sendOwed). So an OSS that has stopped answering costs the
// watcher of each node one NotificationTimeout, once, and not one for
// each notification: the others are not held up behind it, and the
// journals of the nodes do not run past their watchers. The mark is in
// the subscription store, so the watchers of every node and every
// instance skip the recipient alike.
//
// notify fails when it could not read the subscriptions, or could not
// write the mark of a miss: then some recipient has neither the
// notifications nor the mark that would have it read the list again.
// The watcher sends the notifications again, and saves nothing until
// they have gone (deliver); the recipients that took them get them
// twice, with the same notificationIds.
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
func (s *Server) notify(ctx context.Context, notifications ...alarmNotification) error {
	if len(notifications) == 0 {
		return nil
	}
	subs, err := s.ntfSubscriptions.NtfSubscriptions(ctx)
	if err != nil {
		return fmt.Errorf("the subscriptions could not be read: %w", err)
	}
	recipients := make([]*recipient, 0, len(subs))
	for _, sub := range subs {
		r, err := newRecipient(sub)
		if err != nil {
			s.log.Error("a subscription has a filter that cannot be read", "id", sub.ID, "error", err.Error())
			continue
		}
		recipients = append(recipients, r)
	}
	var (
		mu     sync.Mutex
		failed error
	)
	sem := make(chan struct{}, s.notificationConcurrency)
	for _, n := range notifications {
		var wg sync.WaitGroup
		for _, r := range recipients {
			r.mu.Lock()
			missed := r.sub.Missed
			r.mu.Unlock()
			if missed || !r.selects(n) {
				continue
			}
			wg.Go(func() {
				sem <- struct{}{}
				defer func() { <-sem }()
				// A POST that stopped because the watcher is stopping is
				// not the recipient's miss: the watcher saves nothing
				// then, and the next holder of the watch sends the
				// notification again.
				if s.post(ctx, r.sub, n) || ctx.Err() != nil {
					return
				}
				if err := s.markMissed(ctx, r); err != nil {
					mu.Lock()
					failed = err
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if failed != nil {
			return failed
		}
	}
	return nil
}

// markMissed records a miss of the recipient, in the store for every
// watcher and in the round for the rest of it. A subscription that is
// gone needs no mark.
func (s *Server) markMissed(ctx context.Context, r *recipient) error {
	retryAt := s.now().Add(s.missedRetryInterval)
	r.mu.Lock()
	r.sub.Missed, r.sub.RetryAt = true, retryAt
	r.sub.MissedEpoch++
	id := r.sub.ID
	r.mu.Unlock()
	err := s.ntfSubscriptions.MarkNtfSubscriptionMissed(ctx, id, retryAt)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("the mark of a missed notification to %s could not be written: %w", id, err)
	}
	return nil
}

// sendOwed sends the notifyAlarmListRebuilt that a miss owes each
// recipient whose turn has come, whether or not the alarms change. It
// runs at every round of RunNotifications, and for each subscription
// at most once every MissedRetryInterval over every watcher and
// instance: the one that takes the turn (ClaimNtfSubscriptionRetry)
// sends it, on a goroutine of its own, so that a recipient that still
// does not answer holds up nothing. Once the recipient has taken it,
// the mark is cleared, if no other miss has raised it since; the next
// notifications then go to the recipient again.
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
// took it: an answer of 2xx within NotificationTimeout. TS 32.158 5.5.4
// has the recipient answer 204.
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
	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.log.Warn("a notification was not delivered", "id", sub.ID, "notification_type", string(n.notificationType), "error", err.Error())
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxRequestBody))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		s.log.Warn("a notification was refused", "id", sub.ID, "notification_type", string(n.notificationType), "status", resp.StatusCode)
		return false
	}
	result = notificationDelivered
	return true
}

// newNotificationID is a random notificationId, in the 53 bits that a
// JSON number holds without loss.
func newNotificationID() int64 { return mathrand.Int64N(1<<53-1) + 1 }
