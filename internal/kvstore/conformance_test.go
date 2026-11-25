package kvstore_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// harness is one store as two instances of a role see it.
type harness struct {
	// a and b share one store: the same MemoryStore, or two clients of
	// one server.
	a, b kvstore.Store
	// wait lets d pass for the time to live of the items: it moves the
	// clock of a MemoryStore, and sleeps for a server.
	wait func(d time.Duration)
	// prefix keeps the keys and indexes of one test apart from those of
	// the others, in a server that they share.
	prefix string
}

func (h harness) key(name string) string { return h.prefix + name }

// testConformance runs the tests that every Store must pass.
func testConformance(t *testing.T, newHarness func(t *testing.T) harness) {
	tests := []struct {
		name string
		run  func(t *testing.T, h harness)
	}{
		{"GetPutDelete", testGetPutDelete},
		{"TimeToLive", testTimeToLive},
		{"CompareAndSwap", testCompareAndSwap},
		{"CompareAndDelete", testCompareAndDelete},
		{"OnlyOneTakes", testOnlyOneTakes},
		{"ConcurrentCompareAndSwap", testConcurrentCompareAndSwap},
		{"TimeIndex", testTimeIndex},
		{"ClaimRunsOut", testClaimRunsOut},
		{"ScheduleEndsAClaim", testScheduleEndsAClaim},
		{"DueNoSoonerThanItsTime", testDueNoSoonerThanItsTime},
		{"CompetingClaims", testCompetingClaims},
		{"SortedIndex", testSortedIndex},
		{"SortedIndexPagesGoOn", testSortedIndexPagesGoOn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.run(t, newHarness(t)) })
	}
}

// uniquePrefix is a prefix for the keys of one test.
func uniquePrefix() string { return "kvstoretest:" + rand.Text() + ":" }

func testGetPutDelete(t *testing.T, h harness) {
	ctx := context.Background()
	k := h.key("registration")
	if _, err := h.a.Get(ctx, k); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatalf("Get of nothing: %v, want ErrNotFound", err)
	}
	value := []byte(`{"PrivateIdentity":"001010000000001@ims.example"}`)
	v1, err := h.a.Put(ctx, k, value, 0)
	if err != nil || v1 == "" {
		t.Fatalf("Put = %q, %v", v1, err)
	}
	value[0] = 'X' // the store keeps a copy
	got, err := h.b.Get(ctx, k)
	if err != nil || string(got.Value) != `{"PrivateIdentity":"001010000000001@ims.example"}` || got.Version != v1 {
		t.Fatalf("Get = %q %q, %v; want the value at version %q", got.Value, got.Version, err, v1)
	}
	v2, err := h.b.Put(ctx, k, []byte("second"), 0)
	if err != nil || v2 == v1 {
		t.Fatalf("second Put = %q, %v; want a new version", v2, err)
	}
	if err := h.a.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.Get(ctx, k); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatalf("Get after Delete: %v, want ErrNotFound", err)
	}
	if err := h.a.Delete(ctx, k); err != nil {
		t.Fatalf("Delete of nothing: %v", err)
	}
	// An empty value is a value.
	if _, err := h.a.Put(ctx, k, nil, 0); err != nil {
		t.Fatal(err)
	}
	if got, err := h.b.Get(ctx, k); err != nil || len(got.Value) != 0 {
		t.Fatalf("Get of an empty value = %q, %v", got.Value, err)
	}
}

func testTimeToLive(t *testing.T, h harness) {
	ctx := context.Background()
	short, kept, swapped := h.key("challenge"), h.key("rewritten"), h.key("swapped")
	if _, err := h.a.Put(ctx, short, []byte("1"), 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	// A write without a time to live keeps the item.
	if _, err := h.a.Put(ctx, kept, []byte("1"), 300*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.Put(ctx, kept, []byte("2"), 0); err != nil {
		t.Fatal(err)
	}
	// A swap sets the time to live anew.
	v, err := h.a.Put(ctx, swapped, []byte("1"), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.CompareAndSwap(ctx, swapped, v, []byte("2"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	h.wait(100 * time.Millisecond)
	if _, err := h.b.Get(ctx, short); err != nil {
		t.Fatalf("Get before the end of the time to live: %v", err)
	}
	h.wait(400 * time.Millisecond)
	if _, err := h.b.Get(ctx, short); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatalf("Get after the end of the time to live: %v, want ErrNotFound", err)
	}
	if got, err := h.b.Get(ctx, kept); err != nil || string(got.Value) != "2" {
		t.Fatalf("Get of the item written again without a time to live = %q, %v", got.Value, err)
	}
	if got, err := h.b.Get(ctx, swapped); err != nil || string(got.Value) != "2" {
		t.Fatalf("Get of the swapped item = %q, %v", got.Value, err)
	}
}

func testCompareAndSwap(t *testing.T, h harness) {
	ctx := context.Background()
	k := h.key("dialog")
	v1, err := h.a.CompareAndSwap(ctx, k, "", []byte("early"), 0)
	if err != nil {
		t.Fatalf("CompareAndSwap of a new item: %v", err)
	}
	if _, err := h.b.CompareAndSwap(ctx, k, "", []byte("other"), 0); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndSwap of a new item that exists: %v, want ErrConflict", err)
	}
	v2, err := h.b.CompareAndSwap(ctx, k, v1, []byte("confirmed"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.CompareAndSwap(ctx, k, v1, []byte("stale"), 0); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndSwap at an old version: %v, want ErrConflict", err)
	}
	if got, _ := h.a.Get(ctx, k); string(got.Value) != "confirmed" || got.Version != v2 {
		t.Fatalf("Get = %q %q; want what the successful swap wrote", got.Value, got.Version)
	}
	// A version never comes back: after a Delete and a new write, the
	// old one does not match.
	if err := h.a.Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.Put(ctx, k, []byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.b.CompareAndSwap(ctx, k, v2, []byte("stale"), 0); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndSwap at the version of a deleted item: %v, want ErrConflict", err)
	}
	if _, err := h.b.CompareAndSwap(ctx, h.key("missing"), v2, []byte("x"), 0); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndSwap of nothing at a version: %v, want ErrConflict", err)
	}
}

func testCompareAndDelete(t *testing.T, h harness) {
	ctx := context.Background()
	k := h.key("delivery")
	if err := h.a.CompareAndDelete(ctx, k, "anything"); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndDelete of nothing: %v, want ErrConflict", err)
	}
	v1, _ := h.a.Put(ctx, k, []byte("1"), 0)
	v2, _ := h.a.Put(ctx, k, []byte("2"), 0)
	if err := h.b.CompareAndDelete(ctx, k, v1); !errors.Is(err, kvstore.ErrConflict) {
		t.Fatalf("CompareAndDelete at an old version: %v, want ErrConflict", err)
	}
	if err := h.b.CompareAndDelete(ctx, k, v2); err != nil {
		t.Fatal(err)
	}
	if _, err := h.a.Get(ctx, k); !errors.Is(err, kvstore.ErrNotFound) {
		t.Fatalf("Get after CompareAndDelete: %v, want ErrNotFound", err)
	}
}

// testOnlyOneTakes is TakeDelivery of the IP-SM-GW: the delivery
// report and the timer, on two instances, end one delivery once.
func testOnlyOneTakes(t *testing.T, h harness) {
	ctx := context.Background()
	for i := range 20 {
		k := h.key("delivery:" + strconv.Itoa(i))
		if _, err := h.a.Put(ctx, k, []byte("pending"), 0); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var mu sync.Mutex
		taken := 0
		for _, s := range []kvstore.Store{h.a, h.b} {
			wg.Go(func() {
				item, err := s.Get(ctx, k)
				if err != nil {
					return
				}
				switch err := s.CompareAndDelete(ctx, k, item.Version); {
				case err == nil:
					mu.Lock()
					taken++
					mu.Unlock()
				case !errors.Is(err, kvstore.ErrConflict):
					t.Error(err)
				}
			})
		}
		wg.Wait()
		if taken != 1 {
			t.Fatalf("round %d: %d callers took the item, want 1", i, taken)
		}
	}
}

// testConcurrentCompareAndSwap has two instances update one item at
// once, as UpdateDialog of two P-CSCFs would. No update may get lost.
func testConcurrentCompareAndSwap(t *testing.T, h harness) {
	ctx := context.Background()
	k := h.key("counter")
	const workers, increments = 4, 50
	var wg sync.WaitGroup
	for _, s := range []kvstore.Store{h.a, h.b} {
		for range workers {
			wg.Go(func() {
				for range increments {
					if err := increment(ctx, s, k); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
	}
	wg.Wait()
	got, err := h.a.Get(ctx, k)
	if err != nil {
		t.Fatal(err)
	}
	if want := strconv.Itoa(2 * workers * increments); string(got.Value) != want {
		t.Errorf("counter = %s, want %s", got.Value, want)
	}
}

// increment reads, changes and writes back, and starts again when
// another writer came in between.
func increment(ctx context.Context, s kvstore.Store, k string) error {
	for {
		n, version := 0, ""
		item, err := s.Get(ctx, k)
		switch {
		case err == nil:
			if n, err = strconv.Atoi(string(item.Value)); err != nil {
				return err
			}
			version = item.Version
		case !errors.Is(err, kvstore.ErrNotFound):
			return err
		}
		_, err = s.CompareAndSwap(ctx, k, version, []byte(strconv.Itoa(n+1)), 0)
		if !errors.Is(err, kvstore.ErrConflict) {
			return err
		}
	}
}

// base is a time in the index, a whole millisecond as the index keeps.
var base = time.UnixMilli(1_790_000_000_000)

func testTimeIndex(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("registration-expiry")
	for member, at := range map[string]time.Duration{"c": 3 * time.Second, "a": time.Second, "b": 2 * time.Second, "d": 10 * time.Second} {
		if err := h.a.Schedule(ctx, ix, member, base.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if claims, err := h.b.ClaimDue(ctx, ix, base, time.Minute, 10); err != nil || len(claims) != 0 {
		t.Fatalf("ClaimDue before anything is due = %v, %v", claims, err)
	}
	now := base.Add(3 * time.Second)
	claims, err := h.b.ClaimDue(ctx, ix, now, time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []kvstore.Claim{
		{Index: ix, Member: "a", Due: base.Add(time.Second), Until: now.Add(time.Minute)},
		{Index: ix, Member: "b", Due: base.Add(2 * time.Second), Until: now.Add(time.Minute)},
	}
	if !equalClaims(claims, want) {
		t.Fatalf("ClaimDue = %v, want the two earliest, %v", claims, want)
	}
	rest, err := h.a.ClaimDue(ctx, ix, now, time.Minute, 10)
	if err != nil || len(rest) != 1 || rest[0].Member != "c" {
		t.Fatalf("second ClaimDue = %v, %v; want only c, as a and b are claimed and d is not due", rest, err)
	}
	if err := h.a.Done(ctx, claims[0]); err != nil {
		t.Fatal(err)
	}
	if err := h.b.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Fatalf("second Done: %v, want ErrClaimLost", err)
	}
	if err := h.a.Unschedule(ctx, ix, "b"); err != nil {
		t.Fatal(err)
	}
	if err := h.a.Done(ctx, claims[1]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Fatalf("Done after Unschedule: %v, want ErrClaimLost", err)
	}
	if err := h.a.Unschedule(ctx, ix, "nothing"); err != nil {
		t.Fatalf("Unschedule of nothing: %v", err)
	}
	if _, err := h.a.ClaimDue(ctx, ix, now, 0, 10); !errors.Is(err, kvstore.ErrShortLease) {
		t.Fatalf("ClaimDue without a lease: %v, want ErrShortLease", err)
	}
	// Only c, whose claim runs out at now plus a minute, and d are left.
	later, err := h.b.ClaimDue(ctx, ix, now.Add(time.Hour), time.Minute, 10)
	if err != nil || len(later) != 2 || later[0].Member != "d" || later[1].Member != "c" {
		t.Fatalf("ClaimDue an hour later = %v, %v; want d, then c whose claim ran out", later, err)
	}
}

func testClaimRunsOut(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("tr1n")
	if err := h.a.Schedule(ctx, ix, "call-1", base); err != nil {
		t.Fatal(err)
	}
	first, err := h.a.ClaimDue(ctx, ix, base, 5*time.Second, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("ClaimDue = %v, %v", first, err)
	}
	// The instance that claimed it stops. Until the lease runs out no
	// other one gets it; then the next one does.
	if again, _ := h.b.ClaimDue(ctx, ix, base.Add(4999*time.Millisecond), 5*time.Second, 1); len(again) != 0 {
		t.Fatalf("ClaimDue during the lease = %v, want nothing", again)
	}
	second, err := h.b.ClaimDue(ctx, ix, base.Add(5*time.Second), 5*time.Second, 1)
	if err != nil || len(second) != 1 || second[0].Member != "call-1" || !second[0].Due.Equal(base.Add(5*time.Second)) {
		t.Fatalf("ClaimDue after the lease = %v, %v; want call-1, due when the first lease ran out", second, err)
	}
	if err := h.a.Done(ctx, first[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Fatalf("Done of the claim that ran out: %v, want ErrClaimLost", err)
	}
	if err := h.b.Done(ctx, second[0]); err != nil {
		t.Fatal(err)
	}
}

// testScheduleEndsAClaim is a re-registration while another instance
// is about to end the registration: the expiry moves, and the claim on
// the old one ends.
func testScheduleEndsAClaim(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("registration-expiry")
	if err := h.a.Schedule(ctx, ix, "impi", base); err != nil {
		t.Fatal(err)
	}
	claims, err := h.a.ClaimDue(ctx, ix, base, time.Minute, 1)
	if err != nil || len(claims) != 1 {
		t.Fatalf("ClaimDue = %v, %v", claims, err)
	}
	if err := h.b.Schedule(ctx, ix, "impi", base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := h.a.Done(ctx, claims[0]); !errors.Is(err, kvstore.ErrClaimLost) {
		t.Fatalf("Done after a new Schedule: %v, want ErrClaimLost", err)
	}
	if got, _ := h.a.ClaimDue(ctx, ix, base.Add(2*time.Minute), time.Minute, 1); len(got) != 0 {
		t.Fatalf("ClaimDue before the new time = %v, want nothing", got)
	}
	if got, _ := h.a.ClaimDue(ctx, ix, base.Add(time.Hour), time.Minute, 1); len(got) != 1 {
		t.Fatalf("ClaimDue at the new time = %v, want impi", got)
	}
}

// testDueNoSoonerThanItsTime: a registration that ends within a
// millisecond is not due until that millisecond has passed. Claimed
// sooner, its handler would find it not yet expired and do nothing, and
// the Done after that would take the expiry out for good.
func testDueNoSoonerThanItsTime(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("registration-expiry")
	at := base.Add(900 * time.Microsecond)
	if err := h.a.Schedule(ctx, ix, "impi", at); err != nil {
		t.Fatal(err)
	}
	if claims, err := h.b.ClaimDue(ctx, ix, base.Add(100*time.Microsecond), time.Minute, 1); err != nil || len(claims) != 0 {
		t.Fatalf("ClaimDue 800µs before the member's time = %v, %v; want nothing", claims, err)
	}
	claims, err := h.b.ClaimDue(ctx, ix, base.Add(time.Millisecond), time.Minute, 1)
	if err != nil || len(claims) != 1 || claims[0].Due.Before(at) {
		t.Fatalf("ClaimDue once the member's time has passed = %v, %v; want impi, due no sooner than %v", claims, err, at)
	}

	// Done with a time that Schedule was given takes out the member that
	// is still there, as a claim that ended at that time would.
	if err := h.a.Schedule(ctx, ix, "impi", at); err != nil {
		t.Fatal(err)
	}
	if err := h.b.Done(ctx, kvstore.Claim{Index: ix, Member: "impi", Until: at}); err != nil {
		t.Fatalf("Done at the time of the Schedule: %v", err)
	}
	if got, _ := h.a.ClaimDue(ctx, ix, base.Add(time.Hour), time.Minute, 1); len(got) != 0 {
		t.Errorf("ClaimDue after Done = %v, want nothing", got)
	}
}

// testCompetingClaims has two instances, each with two workers, handle
// the expiries of one index. Each expiry must be handled once.
func testCompetingClaims(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("expiry")
	const members = 300
	for i := range members {
		if err := h.a.Schedule(ctx, ix, fmt.Sprintf("user-%03d", i), base.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	now := base.Add(time.Second)
	var mu sync.Mutex
	handled := map[string]int{}
	byInstance := map[string]int{}
	var wg sync.WaitGroup
	for name, s := range map[string]kvstore.Store{"a": h.a, "b": h.b} {
		for range 2 {
			wg.Go(func() {
				for {
					claims, err := s.ClaimDue(ctx, ix, now, time.Minute, 7)
					if err != nil {
						t.Error(err)
						return
					}
					if len(claims) == 0 {
						return
					}
					for _, c := range claims {
						mu.Lock()
						handled[c.Member]++
						byInstance[name]++
						mu.Unlock()
						if err := s.Done(ctx, c); err != nil {
							t.Errorf("Done of %s: %v", c.Member, err)
						}
					}
				}
			})
		}
	}
	wg.Wait()
	if len(handled) != members {
		t.Errorf("%d members handled, want %d", len(handled), members)
	}
	for m, n := range handled {
		if n != 1 {
			t.Errorf("%s handled %d times", m, n)
		}
	}
	t.Logf("handled by instance: %v", byInstance)
}

func members(t *testing.T, s kvstore.Store, index, after string, limit int) []string {
	t.Helper()
	got, err := s.MembersAfter(context.Background(), index, after, limit)
	if err != nil {
		t.Fatalf("MembersAfter(%s, %q, %d): %v", index, after, limit, err)
	}
	return got
}

func testSortedIndex(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("subscribers")
	if got := members(t, h.a, ix, "", 10); len(got) != 0 {
		t.Fatalf("an index that holds nothing = %v", got)
	}
	for _, member := range []string{"charlie", "alice", "bob", "alice"} {
		if err := h.a.AddToIndex(ctx, ix, member); err != nil {
			t.Fatal(err)
		}
	}
	if got := members(t, h.b, ix, "", 10); !slices.Equal(got, []string{"alice", "bob", "charlie"}) {
		t.Errorf("the whole index = %v, want each member once, in order", got)
	}
	if got := members(t, h.b, ix, "", 2); !slices.Equal(got, []string{"alice", "bob"}) {
		t.Errorf("the first page of two = %v", got)
	}
	if got := members(t, h.b, ix, "bob", 10); !slices.Equal(got, []string{"charlie"}) {
		t.Errorf("after bob = %v, want what comes after it and not bob itself", got)
	}
	if got := members(t, h.b, ix, "charlie", 10); len(got) != 0 {
		t.Errorf("after the last member = %v", got)
	}
	if got := members(t, h.b, ix, "", 0); len(got) != 0 {
		t.Errorf("a limit below one = %v", got)
	}
	if err := h.b.RemoveFromIndex(ctx, ix, "bob"); err != nil {
		t.Fatal(err)
	}
	if err := h.b.RemoveFromIndex(ctx, ix, "nobody"); err != nil {
		t.Fatalf("RemoveFromIndex of a member that is not there: %v", err)
	}
	if got := members(t, h.a, ix, "", 10); !slices.Equal(got, []string{"alice", "charlie"}) {
		t.Errorf("after RemoveFromIndex = %v", got)
	}
}

// A page goes on from where the one before it ended, even when that
// member has been taken out since: the operator who pages through the
// subscribers sees the rest of them.
func testSortedIndexPagesGoOn(t *testing.T, h harness) {
	ctx := context.Background()
	ix := h.key("subscribers-paged")
	var want []string
	for i := range 7 {
		member := fmt.Sprintf("impi-%02d", i)
		if err := h.a.AddToIndex(ctx, ix, member); err != nil {
			t.Fatal(err)
		}
		want = append(want, member)
	}
	var got []string
	after := ""
	for range len(want) {
		page := members(t, h.b, ix, after, 3)
		if len(page) == 0 {
			break
		}
		got = append(got, page...)
		after = page[len(page)-1]
		if err := h.a.RemoveFromIndex(ctx, ix, after); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("the pages held %v, want %v", got, want)
	}
}

func equalClaims(got, want []kvstore.Claim) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		g, w := got[i], want[i]
		if g.Index != w.Index || g.Member != w.Member || !g.Due.Equal(w.Due) || !g.Until.Equal(w.Until) {
			return false
		}
	}
	return true
}
