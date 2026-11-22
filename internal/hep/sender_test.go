package hep

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSenderDeliversOverUDP(t *testing.T) {
	collector, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer collector.Close()
	s, err := NewSender(collector.LocalAddr().String(), 8, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Send(specExamplePacket); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, maxTotalLength)
	collector.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, _, err := collector.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(buf[:n])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, specExamplePacket) {
		t.Errorf("collector got %+v\nwant %+v", got, specExamplePacket)
	}
}

func TestSenderDeliversOnceCollectorStarts(t *testing.T) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := probe.LocalAddr().String()
	probe.Close()
	s, err := NewSender(address, 8, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	lost := specExamplePacket
	lost.CorrelationID = "before"
	for range 3 {
		if err := s.Send(lost); err != nil {
			t.Fatal(err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	collector, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Skipf("port %s was taken in between: %v", address, err)
	}
	defer collector.Close()
	later := specExamplePacket
	later.CorrelationID = "after"
	buf := make([]byte, maxTotalLength)
	for range 50 {
		if err := s.Send(later); err != nil {
			t.Fatal(err)
		}
		collector.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, _, err := collector.ReadFrom(buf)
		if err != nil {
			continue
		}
		if p, err := Unmarshal(buf[:n]); err == nil && p.CorrelationID == "after" {
			return
		}
	}
	t.Error("no packet reached the collector after it started")
}

// stalledConn stands in for a collector that does not keep up: every Write
// waits until release is closed.
type stalledConn struct {
	writing chan struct{}
	release chan struct{}

	mu      sync.Mutex
	written []string
}

func (c *stalledConn) Write(b []byte) (int, error) {
	select {
	case c.writing <- struct{}{}:
	default:
	}
	<-c.release
	p, err := Unmarshal(b)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.written = append(c.written, p.CorrelationID)
	c.mu.Unlock()
	return len(b), nil
}

func (c *stalledConn) Close() error { return nil }

func (c *stalledConn) correlationIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.written...)
}

func TestSendDropsWhenQueueIsFull(t *testing.T) {
	conn := &stalledConn{writing: make(chan struct{}, 1), release: make(chan struct{})}
	const queueSize = 2
	s := newSender(dialing(conn), time.Hour, queueSize, discard)
	defer s.Close()
	send := func(id string) {
		p := specExamplePacket
		p.CorrelationID = id
		if err := s.Send(p); err != nil {
			t.Error(err)
		}
	}

	send("in flight")
	<-conn.writing
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		send("queued 1")
		send("queued 2")
		send("dropped 1")
		send("dropped 2")
		send("dropped 3")
	}()
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		close(conn.release)
		t.Fatal("Send waited for the stalled collector")
	}
	if got := s.Dropped(); got != 3 {
		t.Errorf("Dropped() = %d, want 3", got)
	}

	close(conn.release)
	want := []string{"in flight", "queued 1", "queued 2"}
	deadline := time.Now().Add(5 * time.Second)
	for len(conn.correlationIDs()) < len(want) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := conn.correlationIDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("collector got %q, want %q", got, want)
	}
}

func TestSendAfterCloseIsHarmless(t *testing.T) {
	conn := &stalledConn{writing: make(chan struct{}, 1), release: make(chan struct{})}
	close(conn.release)
	s := newSender(dialing(conn), time.Hour, 1, discard)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := s.Send(specExamplePacket); err != nil {
			t.Fatal(err)
		}
	}
	if got := conn.correlationIDs(); len(got) != 0 {
		t.Errorf("collector got %q after Close", got)
	}
}

func TestSendRejectsPacketThatCannotBeEncoded(t *testing.T) {
	s := newSender(dialing(&stalledConn{writing: make(chan struct{}, 1), release: make(chan struct{})}), time.Hour, 1, discard)
	defer s.Close()
	p := specExamplePacket
	p.Source = netip.MustParseAddrPort("[2001:db8::1]:5060")
	if err := s.Send(p); err == nil {
		t.Error("Send accepted an IPv6 source")
	}
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func dialing(conn io.WriteCloser) dialFunc {
	return func(context.Context) (io.WriteCloser, error) { return conn, nil }
}

// recordingConn counts what is written to it, or fails every write.
type recordingConn struct {
	failing bool
	written atomic.Int32
}

func (c *recordingConn) Write(b []byte) (int, error) {
	if c.failing {
		return 0, errors.New("connection refused")
	}
	c.written.Add(1)
	return len(b), nil
}

func (c *recordingConn) Close() error { return nil }

// sendUntil sends a packet every few milliseconds until conn has one.
func sendUntil(t *testing.T, s *Sender, conn *recordingConn) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for conn.written.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no packet got through")
		}
		if err := s.Send(specExamplePacket); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A Lab that starts after the node: its name does not resolve at
// first. The sender warns once and finds it later.
func TestSenderFindsCollectorThatResolvesLater(t *testing.T) {
	var logs bytes.Buffer
	collector := &recordingConn{}
	var dials atomic.Int32
	dial := func(context.Context) (io.WriteCloser, error) {
		if dials.Add(1) <= 3 {
			return nil, errors.New("lookup lab: no such host")
		}
		return collector, nil
	}
	s := newSender(dial, 20*time.Millisecond, 8, slog.New(slog.NewTextHandler(&logs, nil)))
	sendUntil(t, s, collector)
	s.Close()
	text := logs.String()
	if n := strings.Count(text, "level=WARN"); n != 1 || !strings.Contains(text, "no such host") {
		t.Errorf("want one warning with the lookup error, got:\n%s", text)
	}
	if !strings.Contains(text, "level=INFO") {
		t.Errorf("no log line when the collector became reachable:\n%s", text)
	}
}

// A write that fails makes the sender look the name up again, which
// finds a collector that moved to another address.
func TestSenderRedialsAfterWriteFails(t *testing.T) {
	moved := &recordingConn{failing: true}
	now := &recordingConn{}
	var dials atomic.Int32
	dial := func(context.Context) (io.WriteCloser, error) {
		if dials.Add(1) == 1 {
			return moved, nil
		}
		return now, nil
	}
	s := newSender(dial, 20*time.Millisecond, 8, discard)
	defer s.Close()
	sendUntil(t, s, now)
}

func TestNewSenderTakesAnUnresolvableName(t *testing.T) {
	s, err := NewSender("collector.invalid:9060", 8, discard)
	if err != nil {
		t.Fatalf("NewSender: %v", err)
	}
	if err := s.Send(specExamplePacket); err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() {
		s.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the name lookup")
	}
}

func TestNewSenderRejectsMalformedAddress(t *testing.T) {
	for _, address := range []string{"lab", "lab:hep", "lab:70000"} {
		if _, err := NewSender(address, 8, discard); err == nil {
			t.Errorf("NewSender(%q) accepted it", address)
		}
	}
}
