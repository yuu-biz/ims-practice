//go:build !windows

package main

import (
	"os/signal"
	"syscall"
)

// keepWritingOnABrokenPipe keeps SIGPIPE from ending the UE when the
// player reading the pipe of "-received-out -" goes away. The Go
// runtime lets a SIGPIPE on standard output or standard error end the
// program unless the signal is handled, and the UE has a call to
// finish: with the signal ignored, the write fails with EPIPE instead,
// the stream of the call ends there, and the call goes on to its BYE.
func keepWritingOnABrokenPipe() { signal.Ignore(syscall.SIGPIPE) }
