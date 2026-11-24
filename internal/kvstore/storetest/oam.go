package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

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
