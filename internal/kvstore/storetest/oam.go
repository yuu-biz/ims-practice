package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/mns"
	"github.com/yuu-biz/ims-practice/internal/oam"
)

// OAMNtfSubscriptions runs the suite of oam.NtfSubscriptionStore.
func OAMNtfSubscriptions(t *testing.T, newPair func(t *testing.T) (oam.NtfSubscriptionStore, oam.NtfSubscriptionStore)) {
	run(t, newPair, []testCase[oam.NtfSubscriptionStore]{
		{"PutGetDelete", oamPutGetDelete},
		{"ReplaceKeepsOne", oamReplaceKeepsOne},
		{"TheBoundHolds", oamTheBoundHolds},
		{"TheBoundHoldsUnderConcurrentCreates", oamTheBoundHoldsConcurrently},
		{"ConcurrentPutsLoseNone", oamConcurrentPutsLoseNone},
		{"TheMissedMarkStaysWithAReplacement", oamMissedMark},
		{"AMissInBetweenKeepsTheMark", oamAMissInBetweenKeepsTheMark},
	})
}

func ntfSubscription(id, recipient string) oam.NtfSubscriptionControl {
	return oam.NtfSubscriptionControl{ID: id, Attributes: mns.NtfSubscriptionControlAttributes{
		NotificationRecipientAddress: recipient,
		NotificationTypes:            []mns.NotificationType{mns.NotifyNewAlarmType, mns.NotifyClearedAlarmType},
		Scope:                        &mns.Scope{ScopeType: mns.BaseAll},
		NotificationFilter:           `perceivedSeverity="CRITICAL"`,
	}}
}

func putNtf(t *testing.T, s oam.NtfSubscriptionStore, sub oam.NtfSubscriptionControl, max int) bool {
	t.Helper()
	created, err := s.PutNtfSubscription(context.Background(), sub, max)
	if err != nil {
		t.Errorf("PutNtfSubscription(%s): %v", sub.ID, err)
	}
	return created
}

func listNtf(t *testing.T, s oam.NtfSubscriptionStore) []oam.NtfSubscriptionControl {
	t.Helper()
	subs, err := s.NtfSubscriptions(context.Background())
	if err != nil {
		t.Fatalf("NtfSubscriptions: %v", err)
	}
	return subs
}

func oamPutGetDelete(t *testing.T, a, b oam.NtfSubscriptionStore) {
	ctx := context.Background()
	if subs := listNtf(t, b); len(subs) != 0 {
		t.Fatalf("a new store holds %+v", subs)
	}
	sub := ntfSubscription("oss-1", "http://oss:8080/notifications")
	if !putNtf(t, a, sub, 4) {
		t.Error("the first PutNtfSubscription did not create")
	}
	got, err := b.NtfSubscription(ctx, "oss-1")
	if err != nil || !sameJSON(t, got, sub) {
		t.Fatalf("NtfSubscription = %+v, %v; want %+v", got, err, sub)
	}
	if _, err := b.NtfSubscription(ctx, "oss-2"); !errors.Is(err, oam.ErrNotFound) {
		t.Errorf("NtfSubscription of none = %v, want ErrNotFound", err)
	}
	if err := b.DeleteNtfSubscription(ctx, "oss-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NtfSubscription(ctx, "oss-1"); !errors.Is(err, oam.ErrNotFound) {
		t.Errorf("NtfSubscription after the delete = %v, want ErrNotFound", err)
	}
	if err := a.DeleteNtfSubscription(ctx, "oss-1"); !errors.Is(err, oam.ErrNotFound) {
		t.Errorf("a second DeleteNtfSubscription = %v, want ErrNotFound", err)
	}
}

func oamReplaceKeepsOne(t *testing.T, a, b oam.NtfSubscriptionStore) {
	putNtf(t, a, ntfSubscription("oss-2", "http://oss-2:8080/"), 2)
	putNtf(t, a, ntfSubscription("oss-1", "http://oss:8080/a"), 2)
	replaced := ntfSubscription("oss-1", "http://oss:8080/b")
	// A replacement is no new subscription, so it is taken at the bound.
	if putNtf(t, b, replaced, 2) {
		t.Error("PutNtfSubscription of an existing id created one")
	}
	subs := listNtf(t, a)
	if len(subs) != 2 || subs[0].ID != "oss-1" || subs[1].ID != "oss-2" || !sameJSON(t, subs[0], replaced) {
		t.Errorf("NtfSubscriptions = %+v, want oss-1 replaced, then oss-2", subs)
	}
}

func oamTheBoundHolds(t *testing.T, a, b oam.NtfSubscriptionStore) {
	putNtf(t, a, ntfSubscription("oss-1", "http://oss:8080/"), 1)
	if _, err := b.PutNtfSubscription(context.Background(), ntfSubscription("oss-2", "http://oss:8080/"), 1); !errors.Is(err, oam.ErrTooManySubscriptions) {
		t.Errorf("a subscription beyond the bound: %v, want ErrTooManySubscriptions", err)
	}
	if subs := listNtf(t, a); len(subs) != 1 {
		t.Errorf("NtfSubscriptions = %+v", subs)
	}
}

func oamTheBoundHoldsConcurrently(t *testing.T, a, b oam.NtfSubscriptionStore) {
	const max = 3
	var created, refused atomic.Int32
	parallel(8, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		_, err := s.PutNtfSubscription(context.Background(), ntfSubscription(fmt.Sprintf("oss-%d", i), "http://oss:8080/"), max)
		switch {
		case err == nil:
			created.Add(1)
		case errors.Is(err, oam.ErrTooManySubscriptions):
			refused.Add(1)
		default:
			t.Errorf("PutNtfSubscription: %v", err)
		}
	})
	if created.Load() != max || refused.Load() != 8-max || len(listNtf(t, a)) != max {
		t.Errorf("%d created, %d refused, %d kept; want %d, %d, %d", created.Load(), refused.Load(), len(listNtf(t, a)), max, 8-max, max)
	}
}

func oamConcurrentPutsLoseNone(t *testing.T, a, b oam.NtfSubscriptionStore) {
	parallel(8, func(i int) {
		s := a
		if i%2 == 1 {
			s = b
		}
		putNtf(t, s, ntfSubscription(fmt.Sprintf("oss-%d", i), "http://oss:8080/"), 16)
	})
	if subs := listNtf(t, b); len(subs) != 8 {
		t.Errorf("%d subscriptions kept of 8", len(subs))
	}
}

func oamMissedMark(t *testing.T, a, b oam.NtfSubscriptionStore) {
	ctx := context.Background()
	now := time.Now().Round(time.Millisecond)
	putNtf(t, a, ntfSubscription("oss-1", "http://oss:8080/"), 4)
	if err := b.MarkNtfSubscriptionMissed(ctx, "oss-1", now); err != nil {
		t.Fatal(err)
	}
	// The OSS replaces its subscription, and is still owed a
	// notifyAlarmListRebuilt.
	putNtf(t, b, ntfSubscription("oss-1", "http://oss:8080/other"), 4)
	got, err := a.NtfSubscription(ctx, "oss-1")
	if err != nil || !got.Missed || got.MissedEpoch != 1 || !got.RetryAt.Equal(now) || got.Attributes.NotificationRecipientAddress != "http://oss:8080/other" {
		t.Errorf("after the replacement: %+v, %v", got, err)
	}
	if cleared, err := a.ClearNtfSubscriptionMissed(ctx, "oss-1", got.MissedEpoch); err != nil || !cleared {
		t.Fatalf("ClearNtfSubscriptionMissed = %v, %v", cleared, err)
	}
	if subs := listNtf(t, b); len(subs) != 1 || subs[0].Missed {
		t.Errorf("after the mark is cleared: %+v", subs)
	}
	if err := a.MarkNtfSubscriptionMissed(ctx, "oss-2", now); !errors.Is(err, oam.ErrNotFound) {
		t.Errorf("the mark of no subscription: %v, want ErrNotFound", err)
	}
}

// oamAMissInBetweenKeepsTheMark: one watcher sends the owed
// notifyAlarmListRebuilt for the miss it read, while another watcher
// misses again. The second miss keeps the mark, and its own turn.
func oamAMissInBetweenKeepsTheMark(t *testing.T, a, b oam.NtfSubscriptionStore) {
	ctx := context.Background()
	now := time.Now()
	putNtf(t, a, ntfSubscription("oss-1", "http://oss:8080/"), 4)
	if err := a.MarkNtfSubscriptionMissed(ctx, "oss-1", now); err != nil {
		t.Fatal(err)
	}
	read, err := b.NtfSubscription(ctx, "oss-1")
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := b.ClaimNtfSubscriptionRetry(ctx, "oss-1", read.MissedEpoch, now, now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("the first claim = %v, %v", claimed, err)
	}
	// Until next, no other watcher takes the turn of the same miss.
	if claimed, _ := a.ClaimNtfSubscriptionRetry(ctx, "oss-1", read.MissedEpoch, now.Add(time.Second), now.Add(time.Minute)); claimed {
		t.Error("a second watcher took the turn before its time")
	}
	if err := a.MarkNtfSubscriptionMissed(ctx, "oss-1", now); err != nil {
		t.Fatal(err)
	}
	if cleared, err := b.ClearNtfSubscriptionMissed(ctx, "oss-1", read.MissedEpoch); err != nil || cleared {
		t.Errorf("a clear at the epoch before the second miss = %v, %v; want the mark kept", cleared, err)
	}
	got, err := a.NtfSubscription(ctx, "oss-1")
	if err != nil || !got.Missed || got.MissedEpoch != read.MissedEpoch+1 {
		t.Fatalf("after the second miss: %+v, %v", got, err)
	}
	if claimed, err := a.ClaimNtfSubscriptionRetry(ctx, "oss-1", read.MissedEpoch, now, now); err != nil || claimed {
		t.Errorf("a claim at the old epoch = %v, %v", claimed, err)
	}
	if claimed, err := a.ClaimNtfSubscriptionRetry(ctx, "oss-1", got.MissedEpoch, now, now.Add(time.Minute)); err != nil || !claimed {
		t.Errorf("a claim at the new epoch = %v, %v", claimed, err)
	}
	if cleared, err := a.ClearNtfSubscriptionMissed(ctx, "oss-1", got.MissedEpoch); err != nil || !cleared {
		t.Errorf("a clear at the new epoch = %v, %v", cleared, err)
	}
}
