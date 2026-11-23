package smsc

import (
	"context"
	"testing"
	"time"
)

// The cases of the Store itself, which MemoryStore and the adapter on
// kvstore must both pass, are in internal/kvstore/storetest. What is
// left here is what only MemoryStore can show: that it keeps no more in
// the process than it needs.

// Fictitious numbers, from +1 202 555 0100 to 0199.
const (
	alice = "12025550111"
	bob   = "12025550122"
	carol = "12025550133"
)

var t0 = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// TestMemoryStoreForgetsOldTimeStamps: the time stamp of a destination
// is kept only while a later message to it could take the same second,
// so that the store does not grow with every destination it has ever
// seen.
func TestMemoryStoreForgetsOldTimeStamps(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	at := t0.Add(400 * time.Millisecond)
	if _, err := s.ServiceCentreTimeStamp(ctx, bob, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ServiceCentreTimeStamp(ctx, carol, at); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ServiceCentreTimeStamp(ctx, bob, t0.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(s.timestamps) != 1 {
		t.Errorf("%d time stamps kept, want only the recent one", len(s.timestamps))
	}
}
