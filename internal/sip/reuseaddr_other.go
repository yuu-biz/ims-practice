//go:build !unix

package sip

// setReuseAddr does nothing where the option has another meaning, as it
// does on Windows. A protected client port there holds one TCP
// connection at a time, which is enough for a UE; the IPsec ESP that
// needs the port is set up on Linux.
func setReuseAddr(uintptr) error { return nil }
