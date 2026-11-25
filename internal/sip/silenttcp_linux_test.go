package sip

import (
	"errors"
	"net"
	"net/netip"
	"syscall"
	"testing"
	"time"
)

// silentTCPAddress is an address where a TCP connect gets no answer, as
// at a host that has gone: a listener with a backlog of zero, whose one
// place the test fills. The kernel then drops the SYN of every later
// connect, which waits until it times out.
func silentTCPAddress(t *testing.T) netip.AddrPort {
	t.Helper()
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Close(fd) })
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Addr: [4]byte{127, 0, 0, 1}}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 0); err != nil {
		t.Fatal(err)
	}
	sa, err := syscall.Getsockname(fd)
	if err != nil {
		t.Fatal(err)
	}
	in4 := sa.(*syscall.SockaddrInet4)
	addr := netip.AddrPortFrom(netip.AddrFrom4(in4.Addr), uint16(in4.Port))
	// Without SYN cookies even the first connect gets no answer, which
	// does as well.
	if c, err := net.DialTimeout("tcp", addr.String(), 200*time.Millisecond); err == nil {
		t.Cleanup(func() { c.Close() })
	}
	c, err := net.DialTimeout("tcp", addr.String(), 200*time.Millisecond)
	if err == nil {
		c.Close()
	}
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Skipf("a connect beyond the backlog did not wait until it timed out: %v", err)
	}
	return addr
}
