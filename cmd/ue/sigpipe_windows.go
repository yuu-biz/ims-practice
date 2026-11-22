//go:build windows

package main

// keepWritingOnABrokenPipe has nothing to do on Windows, which has no
// SIGPIPE: a write to a pipe whose reader has gone fails with an error
// of its own, which the stream of the call handles as it does EPIPE.
func keepWritingOnABrokenPipe() {}
