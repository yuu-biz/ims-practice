//go:build unix

package sip

import "syscall"

// setReuseAddr lets the socket share its local port with the
// connections that are open from it already, so that one protected
// client port can hold a TCP connection to every UE at once (TS 33.203
// 7.1 NOTE 6).
func setReuseAddr(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
}
