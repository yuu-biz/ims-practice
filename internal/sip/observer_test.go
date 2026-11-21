package sip

import (
	"bytes"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func listenObserved(t *testing.T) (*TransportLayer, <-chan Capture) {
	t.Helper()
	captures := make(chan Capture, 16)
	tr, err := ListenObserved("127.0.0.1:0", testLogger(t), func(c Capture) { captures <- c })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	return tr, captures
}

func nextCapture(t *testing.T, captures <-chan Capture) Capture {
	t.Helper()
	select {
	case c := <-captures:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("nothing observed")
		return Capture{}
	}
}

func checkCapture(t *testing.T, got Capture, transport Transport, sent bool, src, dst netip.AddrPort, wire []byte) {
	t.Helper()
	if got.Transport != transport || got.Sent != sent || got.Source != src || got.Destination != dst {
		t.Errorf("observed %s sent=%v %v -> %v, want %s sent=%v %v -> %v",
			got.Transport, got.Sent, got.Source, got.Destination, transport, sent, src, dst)
	}
	if !bytes.Equal(got.Wire, wire) {
		t.Errorf("observed wire bytes\n%q\nwant\n%q", got.Wire, wire)
	}
}

func receiveFrom(t *testing.T, tr *TransportLayer) *Message {
	t.Helper()
	done := make(chan *Message, 1)
	go func() {
		if m, _, err := tr.Receive(); err == nil {
			done <- m
		}
	}()
	select {
	case m := <-done:
		return m
	case <-time.After(2 * time.Second):
		t.Fatal("no message received")
		return nil
	}
}

// A received request is observed as it arrived, before the transport
// adds received and rport to its Via.
func TestObserverSeesReceivedUDPBytes(t *testing.T) {
	tr, captures := listenObserved(t)
	peer := rawSocket(t)
	// The extra space after the colon is kept on the wire but not by
	// Message.Bytes.
	wire := []byte(strings.Replace(requestWithVia("SIP/2.0/UDP 192.0.2.10:5060;branch=z9hG4bK1;rport"), "CSeq: ", "CSeq:  ", 1))
	if _, err := peer.WriteToUDPAddrPort(wire, tr.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	m := receiveFrom(t, tr)
	c := nextCapture(t, captures)
	checkCapture(t, c, UDP, false, addrOf(peer), tr.LocalAddr(), wire)
	if c.Message != m || !strings.Contains(m.Get("Via"), "received=") {
		t.Errorf("observed message %v, want the stamped request %v", c.Message, m)
	}
}

func TestObserverSeesSentUDPBytes(t *testing.T) {
	tr, captures := listenObserved(t)
	peer := rawSocket(t)
	req, err := Parse([]byte(requestWithVia("SIP/2.0/UDP 127.0.0.1:5060;branch=z9hG4bK2")))
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.SendRequest(req, Destination{Transport: UDP, Addr: addrOf(peer)}); err != nil {
		t.Fatal(err)
	}
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 65535)
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	checkCapture(t, nextCapture(t, captures), UDP, true, tr.LocalAddr(), addrOf(peer), buf[:n])
}

func TestObserverSeesTCPBytesBothWays(t *testing.T) {
	tr, captures := listenObserved(t)
	s := dial(t, tr.LocalAddr())
	local := s.conn.LocalAddr().(*net.TCPAddr).AddrPort()
	wire := []byte(strings.Replace(requestWithVia("SIP/2.0/TCP 127.0.0.1:5060;branch=z9hG4bK3"), "CSeq: ", "CSeq:  ", 1))
	// A keepalive before the request is not part of it (RFC 3261 7.5).
	if _, err := s.conn.Write(append([]byte("\r\n"), wire...)); err != nil {
		t.Fatal(err)
	}
	req := receiveFrom(t, tr)
	checkCapture(t, nextCapture(t, captures), TCP, false, local, tr.LocalAddr(), wire)

	resp := NewResponse(req, 200, "a1")
	if err := tr.SendResponse(resp, Destination{Transport: TCP, Addr: local}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.read(t, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	checkCapture(t, nextCapture(t, captures), TCP, true, tr.LocalAddr(), local, resp.Bytes())
}
