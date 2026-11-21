package h248

import (
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

// fakeClock moves only when the test calls Advance. It runs the timers
// that fall due on the way, in the order of their times, in the
// goroutine of the test.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *fakeClock
	at    time.Time
	f     func()
	done  bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

// Advance moves the clock forward by d.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		c.timers = slices.DeleteFunc(c.timers, func(t *fakeTimer) bool { return t.done })
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.at.After(end) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = end
			c.mu.Unlock()
			return
		}
		next.done = true
		c.now = next.at
		c.mu.Unlock()
		next.f()
		c.mu.Lock()
	}
}

// memAddr is the address of a memConn. The tests use documentation
// addresses (RFC 5737) with the H.248 port.
type memAddr string

func (a memAddr) Network() string { return "udp" }
func (a memAddr) String() string  { return string(a) }

// datagram is one datagram that went over the memNetwork.
type datagram struct {
	from, to string
	at       time.Time
	payload  []byte
	lost     bool
}

// memNetwork carries datagrams between memConns. It keeps every
// datagram that was written, including the ones it lost.
type memNetwork struct {
	clock *fakeClock

	mu    sync.Mutex
	conns map[string]*memConn
	sent  []datagram
	// lose decides whether a datagram is lost. Nil loses none.
	lose func(d datagram) bool
}

func newMemNetwork(clock *fakeClock) *memNetwork {
	return &memNetwork{clock: clock, conns: map[string]*memConn{}}
}

func (n *memNetwork) listen(addr string) *memConn {
	c := &memConn{network: n, addr: memAddr(addr)}
	c.cond = sync.NewCond(&c.mu)
	n.mu.Lock()
	n.conns[addr] = c
	n.mu.Unlock()
	return c
}

func (n *memNetwork) setLose(lose func(d datagram) bool) {
	n.mu.Lock()
	n.lose = lose
	n.mu.Unlock()
}

// deliver puts the datagram into the queue of its receiver, unless it
// is lost.
func (n *memNetwork) deliver(d datagram) {
	d.at = n.clock.Now()
	n.mu.Lock()
	d.lost = n.lose != nil && n.lose(d)
	n.sent = append(n.sent, d)
	to := n.conns[d.to]
	n.mu.Unlock()
	if d.lost || to == nil {
		return
	}
	to.mu.Lock()
	to.queue = append(to.queue, d)
	to.cond.Signal()
	to.mu.Unlock()
}

// datagrams returns what has been written so far.
func (n *memNetwork) datagrams() []datagram {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.sent)
}

// settle waits until every memConn has an empty queue and a reader that
// waits for the next datagram. Since an Endpoint reads its datagrams
// one after another, everything delivered so far has then been
// handled.
func (n *memNetwork) settle(t *testing.T) {
	t.Helper()
	eventually(t, "the network to settle", func() bool {
		n.mu.Lock()
		conns := make([]*memConn, 0, len(n.conns))
		for _, c := range n.conns {
			conns = append(conns, c)
		}
		n.mu.Unlock()
		for _, c := range conns {
			c.mu.Lock()
			idle := c.closed || (len(c.queue) == 0 && c.reading)
			c.mu.Unlock()
			if !idle {
				return false
			}
		}
		return true
	})
}

// waitDatagrams waits until at least count datagrams have been written,
// and returns them.
func (n *memNetwork) waitDatagrams(t *testing.T, count int) []datagram {
	t.Helper()
	eventually(t, "datagrams to be written", func() bool { return len(n.datagrams()) >= count })
	return n.datagrams()
}

// memConn is a net.PacketConn on a memNetwork.
type memConn struct {
	network *memNetwork
	addr    memAddr

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []datagram
	reading bool
	closed  bool
}

func (c *memConn) ReadFrom(b []byte) (int, net.Addr, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.queue) == 0 && !c.closed {
		c.reading = true
		c.cond.Wait()
	}
	c.reading = false
	if c.closed {
		return 0, nil, net.ErrClosed
	}
	d := c.queue[0]
	c.queue = c.queue[1:]
	return copy(b, d.payload), memAddr(d.from), nil
}

func (c *memConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	c.network.deliver(datagram{from: string(c.addr), to: addr.String(), payload: slices.Clone(b)})
	return len(b), nil
}

func (c *memConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.cond.Broadcast()
	return nil
}

func (c *memConn) LocalAddr() net.Addr                { return c.addr }
func (c *memConn) SetDeadline(t time.Time) error      { return nil }
func (c *memConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *memConn) SetWriteDeadline(t time.Time) error { return nil }

// eventually waits, in real time, for goroutines of the Endpoint to get
// to a state. The timers of the Endpoint run on the fakeClock.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
