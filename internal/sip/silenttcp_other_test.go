//go:build !linux

package sip

import (
	"net/netip"
	"testing"
)

// silentTCPAddress needs a listener whose backlog the test sets, which
// the tests make with the system calls of Linux.
func silentTCPAddress(t *testing.T) netip.AddrPort {
	t.Skip("a TCP address that answers no connect is made on Linux only")
	return netip.AddrPort{}
}
