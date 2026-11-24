package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestRunRefusesABadCommandLine(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for _, args := range [][]string{nil, {"listen"}, {"serve", "-port", "8080"}} {
		if err := run(t.Context(), log, args, io.Discard); !errors.Is(err, errUsage) {
			t.Errorf("oss %q: %v, want the usage", args, err)
		}
	}
	if err := run(t.Context(), log, []string{"serve", "0.0.0.0:8080"}, io.Discard); err == nil || !strings.Contains(err.Error(), "takes 0 arguments") {
		t.Errorf("oss serve with an argument: %v", err)
	}
	for _, args := range [][]string{{"-h"}, {"help"}, {"serve", "-h"}} {
		if err := run(t.Context(), log, args, io.Discard); !errors.Is(err, flag.ErrHelp) {
			t.Errorf("oss %q: %v, want flag.ErrHelp", args, err)
		}
	}
}

// serve stops, and says nothing went wrong, once its context ends.
func TestServeStopsWhenItsContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- serve(ctx, slog.New(slog.DiscardHandler), "127.0.0.1:0") }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Errorf("serve ended with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop within 10 s of the end of its context")
	}
	if err := serve(t.Context(), slog.New(slog.DiscardHandler), "127.0.0.1:no-port"); err == nil || !strings.Contains(err.Error(), "-listen") {
		t.Errorf("serve on a bad address: %v", err)
	}
}
