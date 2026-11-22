package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// stoppedListener is a listener that has gone, such as the pipe of a
// player that was closed.
type stoppedListener struct{ writes int }

func (s *stoppedListener) Write(b []byte) (int, error) {
	s.writes++
	return 0, errors.New("broken pipe")
}

// The two ways of hearing a call are apart: the pipe of -received-out
// going away leaves the player playing, and the call hands the frames
// on until nothing is listening at all.
func TestListenersKeepTheOthersWhenOneGoes(t *testing.T) {
	var logs safeBuffer
	pipe, player := &stoppedListener{}, &bytes.Buffer{}
	l := newListeners(logTo(&logs), &way{what: "on standard output", w: pipe}, &way{what: "on the player", w: player})

	if n, err := l.Write([]byte("packet")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; want the call to go on for the player", n, err)
	}
	if _, err := l.Write([]byte(" and more")); err != nil {
		t.Fatal(err)
	}
	if player.String() != "packet and more" {
		t.Errorf("the player heard %q", player.String())
	}
	if pipe.writes != 1 {
		t.Errorf("the pipe that failed was written to %d times; want once", pipe.writes)
	}
	if said := strings.Count(logs.String(), "on standard output"); said != 1 {
		t.Errorf("%d warnings for one listener that went: %s", said, logs.String())
	}

	// With the player gone as well, nothing is left to hear the call,
	// and the UE stops being handed what arrives.
	l.ways[1].w = &stoppedListener{}
	if _, err := l.Write([]byte("the last packet")); err == nil {
		t.Error("the stream goes on with nothing listening")
	}
}
