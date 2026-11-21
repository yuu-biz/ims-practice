package kvlock_test

import (
	"context"
	"crypto/rand"
	"os"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
	"github.com/yuu-biz/ims-practice/internal/kvstore/kvlock"
)

// The benchmarks time what every registration and every call pays for
// each lock of a user: one Lock and its unlock. Uncontended takes the
// lock of a new user each time, and reports the median and the 99th
// percentile. TwoInstances has the goroutines of two instances take the
// lock of one user in turn, and reports the time per lock.
//
//	go test ./internal/kvstore/kvlock -run '^$' -bench . -benchtime 5000x
//
// With KVSTORE_VALKEY_ADDR set they run against that server too.

func BenchmarkMemory(b *testing.B) {
	s := kvstore.NewMemoryStore(nil)
	benchmarkLocker(b, func() kvstore.Store { return s })
}

func BenchmarkValkey(b *testing.B) {
	addr := os.Getenv("KVSTORE_VALKEY_ADDR")
	if addr == "" {
		b.Skip("KVSTORE_VALKEY_ADDR is not set")
	}
	benchmarkLocker(b, func() kvstore.Store {
		v := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
		b.Cleanup(v.Close)
		return v
	})
}

// benchmarkLocker runs the benchmarks on the store that newClient gives
// a client of, one for each instance.
func benchmarkLocker(b *testing.B, newClient func() kvstore.Store) {
	ctx := context.Background()
	prefix := "kvlockbench:" + rand.Text() + ":"
	newLocker := func() *kvlock.Locker {
		return kvlock.New(newClient(), kvlock.Config{Prefix: prefix, Role: "scscf"})
	}

	b.Run("Uncontended", func(b *testing.B) {
		l := newLocker()
		times := make([]time.Duration, 0, b.N)
		run, i := rand.Text(), 0
		for b.Loop() {
			user := run + strconv.Itoa(i) + "@ims.example"
			i++
			start := time.Now()
			unlock, err := l.Lock(ctx, user)
			if err != nil {
				b.Fatal(err)
			}
			unlock()
			times = append(times, time.Since(start))
		}
		slices.Sort(times)
		b.ReportMetric(float64(percentile(times, 50))/1e3, "p50-µs")
		b.ReportMetric(float64(percentile(times, 99))/1e3, "p99-µs")
	})

	b.Run("TwoInstances", func(b *testing.B) {
		lockers := []*kvlock.Locker{newLocker(), newLocker()}
		user := rand.Text() + "@ims.example"
		var next atomic.Int64
		b.RunParallel(func(pb *testing.PB) {
			l := lockers[next.Add(1)%2]
			for pb.Next() {
				unlock, err := l.Lock(ctx, user)
				if err != nil {
					b.Error(err)
					return
				}
				unlock()
			}
		})
	})
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[(len(sorted)-1)*p/100]
}
