// Package h248test holds a clock that moves only when a test says so
// and a network of datagrams in memory, on which the tests of package
// h248 and package mediactl run an Endpoint. The timers of the
// Endpoint then fire on the test's clock, and the test sees every
// datagram, including the ones the network lost.
//
// It imports nothing of package h248, so that the tests inside that
// package can import it.
package h248test

import (
	"net"
	"slices"
	"sync"
	"testing"
	"time"
)

// Clock moves only when the test calls Advance. It runs the timers
// that fall due on the way, in the order of their times, in the
// goroutine of the test.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
}

type timer struct {
	clock *Clock
	at    time.Time
	f     func()
	done  bool
}

func NewClock() *Clock {
	return &Clock{now: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc returns an h248.Timer, written out because this package
// does not import h248.
func (c *Clock) AfterFunc(d time.Duration, f func()) interface{ Stop() bool } {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *timer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	for {
		c.timers = slices.DeleteFunc(c.timers, func(t *timer) bool { return t.done })
		var next *timer
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

// Addr is the address of a Conn. The tests use documentation addresses
// (RFC 5737) with the H.248 port.
type Addr string

func (a Addr) Network() string { return "udp" }
func (a Addr) String() string  { return string(a) }

// Datagram is one datagram that went over the Network.
type Datagram struct {
	From, To string
	At       time.Time
	Payload  []byte
	Lost     bool
}

// Network carries datagrams between Conns. It keeps every datagram that
// was written, including the ones it lost.
type Network struct {
	clock *Clock

	mu    sync.Mutex
	conns map[string]*Conn
	sent  []Datagram
	// lose decides whether a datagram is lost. Nil loses none.
	lose func(d Datagram) bool
}

func NewNetwork(clock *Clock) *Network {
	return &Network{clock: clock, conns: map[string]*Conn{}}
}

func (n *Network) Listen(addr string) *Conn {
	c := &Conn{network: n, addr: Addr(addr)}
	c.cond = sync.NewCond(&c.mu)
	n.mu.Lock()
	n.conns[addr] = c
	n.mu.Unlock()
	return c
}

// SetLose sets what decides whether a datagram is lost. Nil loses none.
func (n *Network) SetLose(lose func(d Datagram) bool) {
	n.mu.Lock()
	n.lose = lose
	n.mu.Unlock()
}

// Deliver puts the datagram into the queue of its receiver, unless it
// is lost.
func (n *Network) Deliver(d Datagram) {
	d.At = n.clock.Now()
	n.mu.Lock()
	d.Lost = n.lose != nil && n.lose(d)
	n.sent = append(n.sent, d)
	to := n.conns[d.To]
	n.mu.Unlock()
	if d.Lost || to == nil {
		return
	}
	to.mu.Lock()
	to.queue = append(to.queue, d)
	to.cond.Signal()
	to.mu.Unlock()
}

// Datagrams returns what has been written so far.
func (n *Network) Datagrams() []Datagram {
	n.mu.Lock()
	defer n.mu.Unlock()
	return slices.Clone(n.sent)
}

// Settle waits until every Conn has an empty queue and a reader that
// waits for the next datagram. Since an Endpoint reads its datagrams
// one after another, everything delivered so far has then been
// handled.
func (n *Network) Settle(t *testing.T) {
	t.Helper()
	Eventually(t, "the network to settle", func() bool {
		n.mu.Lock()
		conns := make([]*Conn, 0, len(n.conns))
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

// WaitDatagrams waits until at least count datagrams have been written,
// and returns them.
func (n *Network) WaitDatagrams(t *testing.T, count int) []Datagram {
	t.Helper()
	Eventually(t, "datagrams to be written", func() bool { return len(n.Datagrams()) >= count })
	return n.Datagrams()
}

// Conn is a net.PacketConn on a Network.
type Conn struct {
	network *Network
	addr    Addr

	mu      sync.Mutex
	cond    *sync.Cond
	queue   []Datagram
	reading bool
	closed  bool
}

func (c *Conn) ReadFrom(b []byte) (int, net.Addr, error) {
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
	return copy(b, d.Payload), Addr(d.From), nil
}

func (c *Conn) WriteTo(b []byte, addr net.Addr) (int, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	c.network.Deliver(Datagram{From: string(c.addr), To: addr.String(), Payload: slices.Clone(b)})
	return len(b), nil
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	c.cond.Broadcast()
	return nil
}

func (c *Conn) LocalAddr() net.Addr                { return c.addr }
func (c *Conn) SetDeadline(t time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(t time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(t time.Time) error { return nil }

// Eventually waits, in real time, for goroutines of the Endpoint to get
// to a state. The timers of the Endpoint run on the Clock.
func Eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}
