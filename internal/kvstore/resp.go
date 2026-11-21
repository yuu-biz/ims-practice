package kvstore

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

// This file is a client of RESP2, the protocol of Redis and Valkey
// (https://valkey.io/topics/protocol/). A command is an array of bulk
// strings; a reply is one value of the five types below. The client
// sends one command at a time on a connection and waits for its reply:
// it does not pipeline.

// respError is an error reply of the server, such as a WRONGTYPE or an
// error raised by a script. The connection stays usable.
type respError string

func (e respError) Error() string { return "kvstore: server: " + string(e) }

// A reply is one of:
//
//	string     a simple string, such as OK
//	respError  an error
//	int64      an integer
//	[]byte     a bulk string; nil for the null bulk string
//	[]any      an array of replies; nil for the null array

// writeCommand writes args as an array of bulk strings.
func writeCommand(w *bufio.Writer, args []string) error {
	fmt.Fprintf(w, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(w, "$%d\r\n%s\r\n", len(a), a)
	}
	return w.Flush()
}

// errProtocol is a reply that does not follow RESP2. The connection is
// lost: there is no telling where the next reply starts.
var errProtocol = errors.New("kvstore: reply does not follow RESP2")

// maxBulk is the longest bulk string or array that the client takes.
// The items of this project are a few kilobytes; a much longer length
// is a broken reply.
const maxBulk = 64 << 20

func readReply(r *bufio.Reader) (any, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) == 0 {
		return nil, errProtocol
	}
	kind, rest := line[0], string(line[1:])
	switch kind {
	case '+':
		return rest, nil
	case '-':
		return respError(rest), nil
	case ':':
		n, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return nil, errProtocol
		}
		return n, nil
	case '$':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 || n > maxBulk {
			return nil, errProtocol
		}
		if n == -1 {
			return []byte(nil), nil
		}
		b := make([]byte, n+2)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		if b[n] != '\r' || b[n+1] != '\n' {
			return nil, errProtocol
		}
		return b[:n], nil
	case '*':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 || n > maxBulk {
			return nil, errProtocol
		}
		if n == -1 {
			return []any(nil), nil
		}
		a := make([]any, 0, min(n, 1024))
		for range n {
			v, err := readReply(r)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		return a, nil
	}
	return nil, errProtocol
}

// readLine reads up to CRLF and returns the line without it.
func readLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, errProtocol
		}
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, errProtocol
	}
	return line[:len(line)-2], nil
}

// respConn is one connection to the server.
type respConn struct {
	conn net.Conn
	r    *bufio.Reader
	w    *bufio.Writer
}

// respClient sends commands on a pool of connections. A connection
// opens when a command finds none idle, and goes back to the pool after
// a reply. One that fails is closed with all the idle ones: after a
// failure of the connection, such as a restart of the server, the idle
// connections are most likely dead too, and the next command opens a
// fresh one instead of failing on each of them in turn.
type respClient struct {
	addr string
	// timeout bounds each command, from the dial to the end of the
	// reply, unless the context ends sooner.
	timeout time.Duration
	maxIdle int

	mu   sync.Mutex
	idle []*respConn
}

func newRESPClient(addr string, timeout time.Duration, maxIdle int) *respClient {
	return &respClient{addr: addr, timeout: timeout, maxIdle: maxIdle}
}

// do sends one command and returns its reply. An error reply of the
// server comes back as a respError error, with the connection kept.
//
// do never sends a command again on its own. When the connection fails
// after the command was written, the server may or may not have run it,
// and only the caller knows whether running it twice is harmless.
func (c *respClient) do(ctx context.Context, args ...string) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	rc, err := c.get(ctx)
	if err != nil {
		return nil, fmt.Errorf("kvstore: connect to %s: %w", c.addr, err)
	}
	deadline, _ := ctx.Deadline()
	rc.conn.SetDeadline(deadline)
	// A context that is cancelled before its deadline ends the wait at
	// once.
	stop := context.AfterFunc(ctx, func() { rc.conn.SetDeadline(time.Unix(1, 0)) })
	reply, err := exchange(rc, args)
	stopped := stop()
	if err != nil {
		rc.conn.Close()
		c.closeIdle()
		switch {
		case ctx.Err() != nil:
			err = fmt.Errorf("%w (%w)", ctx.Err(), err)
		case errors.Is(err, os.ErrDeadlineExceeded):
			// The deadline of the connection is that of ctx, and may
			// pass a moment before ctx itself reports it.
			err = fmt.Errorf("%w (%w)", context.DeadlineExceeded, err)
		}
		return nil, fmt.Errorf("kvstore: %s %s: %w", args[0], c.addr, err)
	}
	if stopped {
		c.put(rc)
	} else {
		// The reply came, but the deadline of the connection is spoilt.
		rc.conn.Close()
	}
	if e, ok := reply.(respError); ok {
		return nil, e
	}
	return reply, nil
}

func exchange(rc *respConn, args []string) (any, error) {
	if err := writeCommand(rc.w, args); err != nil {
		return nil, err
	}
	return readReply(rc.r)
}

func (c *respClient) get(ctx context.Context) (*respConn, error) {
	c.mu.Lock()
	if n := len(c.idle); n > 0 {
		rc := c.idle[n-1]
		c.idle = c.idle[:n-1]
		c.mu.Unlock()
		return rc, nil
	}
	c.mu.Unlock()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.addr)
	if err != nil {
		return nil, err
	}
	return &respConn{conn: conn, r: bufio.NewReader(conn), w: bufio.NewWriter(conn)}, nil
}

func (c *respClient) put(rc *respConn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.idle) >= c.maxIdle {
		rc.conn.Close()
		return
	}
	c.idle = append(c.idle, rc)
}

func (c *respClient) closeIdle() {
	c.mu.Lock()
	idle := c.idle
	c.idle = nil
	c.mu.Unlock()
	for _, rc := range idle {
		rc.conn.Close()
	}
}
