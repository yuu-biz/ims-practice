package main

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// The player reading the pipe of "-received-out -" may close it in the
// middle of the call. The UE has a call to finish, so the write fails
// with an error instead of SIGPIPE ending the program.
func TestWritingToAClosedStandardOutputDoesNotEndTheUE(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGPIPE")
	}
	if os.Getenv("UE_BROKEN_PIPE_CHILD") == "1" {
		keepWritingOnABrokenPipe()
		for range 1000 {
			if _, err := os.Stdout.Write(make([]byte, 4096)); err != nil {
				os.Exit(9) // the write failed, as it should
			}
		}
		os.Exit(1) // no write failed: the pipe stayed open
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$")
	child.Env = append(os.Environ(), "UE_BROKEN_PIPE_CHILD=1")
	child.Stdout, child.Stderr = w, io.Discard
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	// Nothing reads the pipe any more, so the child's next write to
	// standard output is a broken pipe.
	w.Close()
	r.Close()
	err = child.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 9 {
		t.Fatalf("the UE ended with %v; want the write to fail (exit 9) rather than a signal to end the program", err)
	}
}
