package kvstore_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/kvstore"
)

// valkeyAddress is the address of a Valkey server for the tests, from
// KVSTORE_VALKEY_ADDR, such as 127.0.0.1:6379. Without one the tests
// against a server are skipped; the server is not part of the build.
func valkeyAddress(t *testing.T) string {
	addr := os.Getenv("KVSTORE_VALKEY_ADDR")
	if addr == "" {
		t.Skip("KVSTORE_VALKEY_ADDR is not set")
	}
	return addr
}

func TestValkey(t *testing.T) {
	addr := valkeyAddress(t)
	testConformance(t, func(t *testing.T) harness {
		a, b := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr}), kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
		t.Cleanup(a.Close)
		t.Cleanup(b.Close)
		return harness{a: a, b: b, wait: time.Sleep, prefix: uniquePrefix()}
	})
}

// TestValkeyAfterTheConnectionsAreKilled has the server close every
// connection of the client, as a restart of the server does. At most
// one operation fails, and the next one opens a new connection.
func TestValkeyAfterTheConnectionsAreKilled(t *testing.T) {
	addr := valkeyAddress(t)
	ctx := context.Background()
	// The connections of this store only: other packages share the
	// server while their tests run, and must keep theirs. Each one asks
	// the server for its ID when it opens, which the kill names.
	var mu sync.Mutex
	var own []string
	kvstore.SetDialValkey(t, func(ctx context.Context, addr string) (net.Conn, error) {
		var d net.Dialer
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		if _, err := conn.Write([]byte("*2\r\n$6\r\nCLIENT\r\n$2\r\nID\r\n")); err != nil {
			conn.Close()
			return nil, err
		}
		// The reply is one short line, read a byte at a time so that
		// nothing after it is taken from the store's reader.
		var line []byte
		b := make([]byte, 1)
		for len(line) == 0 || line[len(line)-1] != '\n' {
			if _, err := conn.Read(b); err != nil {
				conn.Close()
				return nil, err
			}
			line = append(line, b[0])
		}
		id, ok := strings.CutPrefix(strings.TrimSpace(string(line)), ":")
		if !ok {
			conn.Close()
			return nil, fmt.Errorf("CLIENT ID = %q", line)
		}
		mu.Lock()
		own = append(own, id)
		mu.Unlock()
		return conn, nil
	})
	s := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
	defer s.Close()
	k := uniquePrefix() + "item"
	if _, err := s.Put(ctx, k, []byte("1"), time.Minute); err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	mu.Lock()
	ids := slices.Clone(own)
	mu.Unlock()
	if len(ids) == 0 {
		t.Fatal("the store opened no connection")
	}
	n := 0
	for _, id := range ids {
		cmd := fmt.Sprintf("*4\r\n$6\r\nCLIENT\r\n$4\r\nKILL\r\n$2\r\nID\r\n$%d\r\n%s\r\n", len(id), id)
		if _, err := conn.Write([]byte(cmd)); err != nil {
			t.Fatal(err)
		}
		line, err := r.ReadString('\n')
		if err != nil || line[0] != ':' {
			t.Fatalf("CLIENT KILL ID %s = %q, %v", id, line, err)
		}
		if line != ":0\r\n" {
			n++
		}
	}
	if n == 0 {
		t.Fatal("CLIENT KILL ID killed none of the store's connections")
	}
	failures := 0
	for range 3 {
		if _, err := s.Get(ctx, k); err != nil {
			failures++
		}
	}
	if failures > 1 {
		t.Errorf("%d of 3 Gets failed after the kill, want at most 1", failures)
	}
}

// fakeServer accepts connections and hands each to serve.
func fakeServer(t *testing.T, serve func(net.Conn)) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				serve(conn)
			}()
		}
	}()
	return l.Addr().String()
}

// readCommand reads one command of the client, an array of bulk
// strings, and discards it.
func readCommand(r *bufio.Reader) error {
	var n int
	if _, err := fmt.Fscanf(r, "*%d\r\n", &n); err != nil {
		return err
	}
	for range n {
		var length int
		if _, err := fmt.Fscanf(r, "$%d\r\n", &length); err != nil {
			return err
		}
		if _, err := r.Discard(length + 2); err != nil {
			return err
		}
	}
	return nil
}

func TestRESPServerThatDoesNotAnswer(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) { time.Sleep(5 * time.Second) })
	s := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr, Timeout: 100 * time.Millisecond})
	start := time.Now()
	_, err := s.Get(context.Background(), "k")
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get = %v, want a deadline error", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Get took %v, want about the timeout", elapsed)
	}
}

func TestRESPCancelledContext(t *testing.T) {
	addr := fakeServer(t, func(conn net.Conn) { time.Sleep(5 * time.Second) })
	s := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr, Timeout: 10 * time.Second})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	if _, err := s.Get(ctx, "k"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Get took %v after the cancel", elapsed)
	}
}

// TestRESPServerThatCloses closes each connection after one reply. The
// command on the connection that has been closed fails, and the one
// after it opens a new connection.
func TestRESPServerThatCloses(t *testing.T) {
	closed := make(chan struct{}, 1)
	addr := fakeServer(t, func(conn net.Conn) {
		r := bufio.NewReader(conn)
		if readCommand(r) == nil {
			conn.Write([]byte("*2\r\n$1\r\nv\r\n$7\r\nversion\r\n"))
		}
		conn.Close()
		select {
		case closed <- struct{}{}:
		default:
		}
	})
	s := kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr})
	ctx := context.Background()
	if item, err := s.Get(ctx, "k"); err != nil || string(item.Value) != "v" || item.Version != "version" {
		t.Fatalf("first Get = %+v, %v", item, err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the server did not close the connection of the first Get within 5 s")
	}
	if _, err := s.Get(ctx, "k"); err == nil {
		t.Fatal("Get on the closed connection did not fail")
	}
	if _, err := s.Get(ctx, "k"); err != nil {
		t.Fatalf("Get after the failure: %v, want it on a new connection", err)
	}
}

func TestRESPReplies(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		check func(t *testing.T, s *kvstore.Valkey)
	}{
		{"no item", "*2\r\n$-1\r\n$-1\r\n", func(t *testing.T, s *kvstore.Valkey) {
			if _, err := s.Get(context.Background(), "k"); !errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("Get = %v, want ErrNotFound", err)
			}
		}},
		{"error reply", "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n", func(t *testing.T, s *kvstore.Valkey) {
			if _, err := s.Get(context.Background(), "k"); err == nil || errors.Is(err, kvstore.ErrNotFound) {
				t.Errorf("Get = %v, want the error of the server", err)
			}
		}},
		{"not RESP", "hello\r\n", func(t *testing.T, s *kvstore.Valkey) {
			if _, err := s.Get(context.Background(), "k"); err == nil {
				t.Error("Get of a broken reply did not fail")
			}
		}},
		{"swap refused", ":0\r\n", func(t *testing.T, s *kvstore.Valkey) {
			if _, err := s.CompareAndSwap(context.Background(), "k", "v", nil, 0); !errors.Is(err, kvstore.ErrConflict) {
				t.Errorf("CompareAndSwap = %v, want ErrConflict", err)
			}
		}},
		{"claims", "*4\r\n$4\r\nimpi\r\n$13\r\n1790000000000\r\n$5\r\nimpi2\r\n$17\r\n1.790000000001e12\r\n", func(t *testing.T, s *kvstore.Valkey) {
			claims, err := s.ClaimDue(context.Background(), "ix", base, time.Minute, 10)
			if err != nil || len(claims) != 2 || claims[0].Member != "impi" || !claims[0].Due.Equal(base) ||
				!claims[1].Due.Equal(base.Add(time.Millisecond)) || !claims[1].Until.Equal(base.Add(time.Minute)) {
				t.Errorf("ClaimDue = %v, %v", claims, err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := fakeServer(t, func(conn net.Conn) {
				r := bufio.NewReader(conn)
				for readCommand(r) == nil {
					conn.Write([]byte(tt.reply))
				}
			})
			tt.check(t, kvstore.NewValkey(kvstore.ValkeyConfig{Address: addr}))
		})
	}
}
