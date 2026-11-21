package kvstore_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// The benchmarks run the mixes of operations that one registration and
// one call make, as the roles make them, one after the other as a node
// does them, and report the time of a whole
// mix at the median and the 99th percentile.
//
//	go test ./internal/kvstore -run '^$' -bench . -benchtime 2000x
//
// With KVSTORE_VALKEY_ADDR set they run against that server too.

func BenchmarkMemory(b *testing.B) {
	benchmarkStore(b, kvstore.NewMemoryStore(nil))
}

func BenchmarkValkey(b *testing.B) {
	addr := valkeyAddressForBenchmark(b)
	s := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
	defer s.Close()
	benchmarkStore(b, s)
}

func valkeyAddressForBenchmark(b *testing.B) string {
	b.Helper()
	addr := os.Getenv("KVSTORE_VALKEY_ADDR")
	if addr == "" {
		b.Skip("KVSTORE_VALKEY_ADDR is not set")
	}
	return addr
}

func benchmarkStore(b *testing.B, s kvstore.Store) {
	ctx := context.Background()
	prefix := uniquePrefix()
	registration := make([]byte, 1800)
	dialog := make([]byte, 800)
	small := make([]byte, 60)

	flows := []struct {
		name string
		run  func(b *testing.B, user string)
	}{
		{"Get", func(b *testing.B, user string) {
			must(b, ignoreNotFound(s.Get(ctx, prefix+"fixed")))
		}},
		{"Put", func(b *testing.B, user string) {
			must(b, ignoreVersion(s.Put(ctx, prefix+"reg:"+user, registration, time.Hour)))
		}},
		{"CompareAndSwap", func(b *testing.B, user string) {
			must(b, ignoreVersion(s.CompareAndSwap(ctx, prefix+"new:"+user, "", registration, time.Hour)))
		}},
		{"ScheduleAndClaim", func(b *testing.B, user string) {
			ix := prefix + "expiry:" + user
			must(b, s.Schedule(ctx, ix, user, base))
			claims, err := s.ClaimDue(ctx, ix, base, time.Minute, 10)
			must(b, err)
			must(b, s.Done(ctx, claims[0]))
		}},
		// Registration: 12 reads, 8 writes, 1 swap, 1 delete, 4 changes
		// of a time index.
		{"Registration", func(b *testing.B, user string) {
			k := func(name string) string { return prefix + name + ":" + user }
			get := func(key string) kvstore.Item {
				item, err := s.Get(ctx, key)
				must(b, ignoreNotFound(item, err))
				return item
			}
			put := func(key string, value []byte, ttl time.Duration) {
				must(b, ignoreVersion(s.Put(ctx, key, value, ttl)))
			}
			// The REGISTER that gets the 401.
			get(k("challenge"))
			put(k("challenge"), small, 4*time.Minute)
			must(b, s.Schedule(ctx, prefix+"reg-await-auth", user, base))
			// The REGISTER that answers it.
			get(k("challenge"))
			must(b, s.Delete(ctx, k("challenge")))
			must(b, s.Unschedule(ctx, prefix+"reg-await-auth", user))
			get(k("registration"))
			for i := range 3 {
				put(k("impu"+strconv.Itoa(i)), small, 0)
			}
			put(k("registration"), registration, 0)
			must(b, s.Schedule(ctx, prefix+"registration-expiry", user, base))
			subs := get(k("subscriptions"))
			// The subscription of the P-CSCF, and the NOTIFYs.
			for range 2 {
				get(k("impu0"))
				get(k("registration"))
			}
			put(k("subscription"), dialog, 0)
			must(b, ignoreVersion(s.CompareAndSwap(ctx, k("subscriptions"), subs.Version, small, 0)))
			must(b, s.Schedule(ctx, prefix+"subscription-expiry", user, base))
			// The P-CSCF.
			for range 2 {
				get(k("association"))
				get(k("sa"))
			}
			put(k("association"), dialog, 0)
			put(k("contact"), small, 0)
		}},
		// Call: 41 reads, 2 writes, 8 swaps, 2 deletes that compare.
		{"Call", func(b *testing.B, user string) {
			k := func(name string) string { return prefix + name + ":" + user }
			get := func(key string) kvstore.Item {
				item, err := s.Get(ctx, key)
				must(b, ignoreNotFound(item, err))
				return item
			}
			for _, side := range []string{"caller", "callee"} {
				must(b, ignoreVersion(s.Put(ctx, k("dialog:"+side), dialog, 0)))
			}
			for i := range 31 {
				get(k("association" + strconv.Itoa(i%4)))
			}
			for i := range 8 {
				d := k("dialog:" + []string{"caller", "callee"}[i%2])
				item := get(d)
				must(b, ignoreVersion(s.CompareAndSwap(ctx, d, item.Version, dialog, 0)))
			}
			for _, side := range []string{"caller", "callee"} {
				item := get(k("dialog:" + side))
				must(b, s.CompareAndDelete(ctx, k("dialog:"+side), item.Version))
			}
		}},
	}
	if _, err := s.Put(ctx, prefix+"fixed", registration, time.Hour); err != nil {
		b.Fatal(err)
	}
	for _, f := range flows {
		b.Run(f.name, func(b *testing.B) {
			times := make([]time.Duration, 0, b.N)
			// Every run has users of its own, so that a new item is new.
			run, i := uniquePrefix(), 0
			for b.Loop() {
				user := run + strconv.Itoa(i)
				i++
				start := time.Now()
				f.run(b, user)
				times = append(times, time.Since(start))
			}
			slices.Sort(times)
			b.ReportMetric(float64(percentile(times, 50))/1e3, "p50-µs")
			b.ReportMetric(float64(percentile(times, 99))/1e3, "p99-µs")
		})
	}
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[(len(sorted)-1)*p/100]
}

func must(b *testing.B, err error) {
	if err != nil {
		b.Fatal(err)
	}
}

func ignoreNotFound(_ kvstore.Item, err error) error {
	if errors.Is(err, kvstore.ErrNotFound) {
		return nil
	}
	return err
}

func ignoreVersion(_ string, err error) error { return err }
