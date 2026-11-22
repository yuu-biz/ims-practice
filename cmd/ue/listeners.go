package main

import (
	"errors"
	"io"
	"log/slog"
)

// A listeners writes what the call receives to every way of hearing it,
// each on its own: the stream of -received-out and the player of -play
// are two listeners to one call, and one of them going away leaves the
// other listening. io.MultiWriter would stop at the first of them to
// fail.
//
// It gives up only once every listener has, and the caller then stops
// handing it what arrives.
type listeners struct {
	log  *slog.Logger
	ways []*way
}

// A way is one listener: what it is called, where it writes, and
// whether it is gone.
type way struct {
	what string
	w    io.Writer
	gone bool
}

func newListeners(log *slog.Logger, ways ...*way) *listeners {
	return &listeners{log: log, ways: ways}
}

// Write hands b to each listener that is still there, says once which
// one stops, and fails only when none is left.
func (l *listeners) Write(b []byte) (int, error) {
	left := 0
	for _, w := range l.ways {
		if w.gone {
			continue
		}
		if _, err := w.w.Write(b); err != nil {
			l.log.Warn("the call is no longer heard "+w.what, "error", err.Error())
			w.gone = true
			continue
		}
		left++
	}
	if left == 0 {
		return 0, errors.New("nothing is listening to the call any more")
	}
	return len(b), nil
}
