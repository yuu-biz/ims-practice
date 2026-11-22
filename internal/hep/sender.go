package hep

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// redialInterval is how often, at most, the sender looks the collector
// up again.
const redialInterval = 5 * time.Second

// dialFunc opens the connection to the collector. It resolves the name
// each time.
type dialFunc func(context.Context) (io.WriteCloser, error)

// Sender sends packets to a collector over UDP. A slow or absent collector
// must never delay SIP or Diameter, so Send never waits: when the queue is
// full it drops the packet and counts it.
type Sender struct {
	queue     chan []byte
	cancel    context.CancelFunc // stops a dial in progress
	stop      chan struct{}
	stopped   chan struct{}
	closeOnce sync.Once
	closeErr  error
	dropped   atomic.Uint64
}

// NewSender sends to the collector at address ("host:port") with at most
// queueSize packets waiting. Only a malformed address is an error. The
// collector, the Lab, is not on the path of any call and may
// start after the node, so a name that does not resolve yet is logged as
// a warning, and the copies are dropped until it does. The sender looks
// the name up again when a write fails, at most every redialInterval, so
// it also finds a collector that started later or moved.
func NewSender(address string, queueSize int, log *slog.Logger) (*Sender, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return nil, fmt.Errorf("port %q of %s is not a number", port, address)
	}
	var d net.Dialer
	dial := func(ctx context.Context) (io.WriteCloser, error) { return d.DialContext(ctx, "udp", address) }
	return newSender(dial, redialInterval, queueSize, log.With("collector", address)), nil
}

func newSender(dial dialFunc, interval time.Duration, queueSize int, log *slog.Logger) *Sender {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sender{
		queue:   make(chan []byte, queueSize),
		cancel:  cancel,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go s.write(ctx, dial, interval, log)
	return s
}

// Send queues p without waiting. It returns an error only when p cannot be
// encoded. After Close, Send delivers nothing.
func (s *Sender) Send(p Packet) error {
	b, err := p.Marshal()
	if err != nil {
		return err
	}
	select {
	case s.queue <- b:
	default:
		s.dropped.Add(1)
	}
	return nil
}

// Dropped is the number of packets dropped because the queue was full.
func (s *Sender) Dropped() uint64 {
	return s.dropped.Load()
}

// Close stops sending. Packets still in the queue are discarded.
func (s *Sender) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		close(s.stop)
		<-s.stopped
	})
	return s.closeErr
}

// write owns the connection: it dials, writes the queued packets, and
// dials again after a failure.
func (s *Sender) write(ctx context.Context, dial dialFunc, interval time.Duration, log *slog.Logger) {
	defer close(s.stopped)
	var conn io.WriteCloser
	var lastDial time.Time
	unresolved := false
	connect := func() {
		lastDial = time.Now()
		c, err := dial(ctx)
		switch {
		case err != nil && conn == nil && !unresolved:
			unresolved = true
			log.Warn("HEP collector not reachable; copies are dropped until it is", "error", err.Error())
		case err != nil:
		default:
			if conn != nil {
				conn.Close()
			}
			if unresolved {
				unresolved = false
				log.Info("HEP collector reachable")
			}
			conn = c
		}
	}
	connect()
	for {
		select {
		case b := <-s.queue:
			// When no collector listens, the host answers with ICMP port
			// unreachable and a later write on the connected socket fails.
			// The copy is lost either way and the next one may get through,
			// on this connection or on one to the address the name has now.
			if conn != nil {
				if _, err := conn.Write(b); err == nil {
					continue
				}
			}
			if time.Since(lastDial) >= interval {
				connect()
			}
		case <-s.stop:
			if conn != nil {
				s.closeErr = conn.Close()
			}
			return
		}
	}
}
