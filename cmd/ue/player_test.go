package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// standIn writes a program that takes the place of the player, so that
// the playback can be tried on a machine without FFmpeg. It records the
// arguments it was given in "args", and then runs body, in which HEARD
// stands for the file that holds what the player was fed.
func standIn(t *testing.T, body string) (path, args, heard string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in for the player is a shell script")
	}
	dir := t.TempDir()
	path, args, heard = filepath.Join(dir, "player"), filepath.Join(dir, "args"), filepath.Join(dir, "heard")
	script := "#!/bin/sh\nprintf '%s' \"$*\" > " + args + "\n" + strings.ReplaceAll(body, "HEARD", heard) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, args, heard
}

func logTo(buf *safeBuffer) *slog.Logger { return slog.New(slog.NewJSONHandler(buf, nil)) }

// safeBuffer holds the log while the playback's own goroutines, which
// feed the player and watch it, write to it.
type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

func (s *safeBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}

// shortPlayerWait keeps a test from waiting ten seconds for a player
// that will not end by itself.
func shortPlayerWait(t *testing.T) {
	old := playerWaitLimit
	playerWaitLimit = 200 * time.Millisecond
	t.Cleanup(func() { playerWaitLimit = old })
}

// read is what a file holds, and nothing for a file that is not there
// yet.
func read(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// until waits for the player to catch up with what it was given.
func until(t *testing.T, name, want string) {
	t.Helper()
	for range 500 {
		if read(t, name) == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the player heard %q; want %q", read(t, name), want)
}

// waitFor polls until the playback has noticed something, so that a
// test does not depend on how fast the player's own goroutines run.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for range 500 {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not happen", what)
}

// The player hears each packet while the call runs, not at the end of
// it. It is told what mu-law samples are, since a PCMU stream says
// nothing about itself, while an AMR or AMR-WB stream names its codec
// in the magic number.
func TestPlaybackFeedsThePlayerWhileTheCallRuns(t *testing.T) {
	for _, tc := range []struct {
		codec sdp.Codec
		args  string
	}{
		{sdp.VoLTECodecs()[0], "-nodisp -autoexit -loglevel error -i -"},
		{pcmuCodec, "-nodisp -autoexit -loglevel error -f mulaw -ar 8000 -ac 1 -i -"},
	} {
		player, args, heard := standIn(t, "exec cat > HEARD")
		var logs safeBuffer
		p := &playback{path: player, log: logTo(&logs), codec: func() (sdp.Codec, bool) { return tc.codec, true }}

		if n, err := p.Write([]byte("the first packet")); n != 16 || err != nil {
			t.Fatalf("%s: Write = %d, %v", tc.codec.Name, n, err)
		}
		until(t, heard, "the first packet")
		if got := read(t, args); got != tc.args {
			t.Errorf("%s: the player was run with %q; want %q", tc.codec.Name, got, tc.args)
		}
		if _, err := p.Write([]byte(" and the next")); err != nil {
			t.Fatal(err)
		}
		until(t, heard, "the first packet and the next")
		p.close()
		if !strings.Contains(logs.String(), "playing the call") || strings.Contains(logs.String(), "WARN") {
			t.Errorf("%s: the log says %s", tc.codec.Name, logs.String())
		}
	}
}

// A player that stops reading, while staying alive, must hold up
// neither the call nor its end: the packets that no longer fit are
// dropped, said once, and the UE does not wait for the player to take
// them.
func TestPlaybackDropsWhatAStalledPlayerCannotTake(t *testing.T) {
	shortPlayerWait(t)
	// The stand-in never reads its input and ends only when it is
	// stopped, as a player that has stalled does.
	player, _, _ := standIn(t, "exec sleep 60")
	var logs safeBuffer
	p := &playback{path: player, log: logTo(&logs), codec: func() (sdp.Codec, bool) { return pcmuCodec, true }}

	// More than the queue holds, and more than the pipe takes: every
	// write still returns at once.
	packet := bytes.Repeat([]byte("packet"), 400)
	for i := range playerQueue * 4 {
		done := make(chan struct{})
		go func() {
			defer close(done)
			if n, err := p.Write(packet); n != len(packet) || err != nil {
				t.Errorf("packet %d: Write = %d, %v; want the call to go on", i, n, err)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("packet %d: the call waited for the player", i)
		}
	}
	waitFor(t, "the dropped packets to be reported", func() bool {
		return strings.Contains(logs.String(), "the player is behind")
	})
	if dropped := strings.Count(logs.String(), "the player is behind"); dropped != 1 {
		t.Errorf("%d warnings for one player that fell behind: %s", dropped, logs.String())
	}

	// The end of the call waits for the player only as long as the
	// limit, and then stops it.
	ended := make(chan struct{})
	go func() { defer close(ended); p.close() }()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the call waited for the player")
	}
	if !strings.Contains(logs.String(), "the player was stopped") {
		t.Errorf("the log says %s", logs.String())
	}
}

// A player that fails where the call ends, as one with no audio device
// does, leaves a silent call behind, and the learner is told why.
func TestPlaybackSaysWhenThePlayerFailed(t *testing.T) {
	player, _, heard := standIn(t, "cat > HEARD; exit 2")
	var logs safeBuffer
	p := &playback{path: player, log: logTo(&logs), codec: func() (sdp.Codec, bool) { return pcmuCodec, true }}
	if _, err := p.Write([]byte("packet")); err != nil {
		t.Fatal(err)
	}
	until(t, heard, "packet")
	p.close()
	if !strings.Contains(logs.String(), "the player ended with an error") {
		t.Errorf("the log says %s", logs.String())
	}
}

// A learner without FFmpeg hears nothing and is told where to get it,
// once, before the call starts. A -player that is not there is found
// out at the same time.
func TestPlaybackSaysWhenThereIsNoPlayer(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var logs safeBuffer
	if p := newPlayback(options{play: true}, logTo(&logs), nil); p != nil {
		t.Error("a playback was made without a player")
	}
	said := logs.String()
	for _, want := range []string{"no player was found", playerName, playerInstall, "-player"} {
		if !strings.Contains(said, want) {
			t.Errorf("the warning does not name %s: %s", want, said)
		}
	}
	logs.Reset()
	if p := newPlayback(options{play: true, player: filepath.Join(t.TempDir(), "no-such-player")}, logTo(&logs), nil); p != nil {
		t.Error("a playback was made for a -player that is not there")
	}
	if !strings.Contains(logs.String(), "no player was found") {
		t.Errorf("a -player that is not there is not reported: %s", logs.String())
	}
	logs.Reset()
	if p := newPlayback(options{}, logTo(&logs), nil); p != nil || logs.Len() != 0 {
		t.Errorf("a run without -play makes a playback or says something: %s", logs.String())
	}
}

// A player that cannot be run costs the learner the audio and nothing
// else: the writes of the call still go through, and the reason is
// given once.
func TestPlaybackCarriesOnWhenThePlayerDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	// A file that is not a program: it is there, so it is found before
	// the call, and starting it fails.
	player := filepath.Join(dir, "player")
	if err := os.WriteFile(player, []byte("not a program"), 0o755); err != nil {
		t.Fatal(err)
	}
	var logs safeBuffer
	p := &playback{path: player, log: logTo(&logs), codec: func() (sdp.Codec, bool) { return pcmuCodec, true }}
	for range 3 {
		if n, err := p.Write([]byte("packet")); n != 6 || err != nil {
			t.Fatalf("Write = %d, %v; want the call to go on", n, err)
		}
	}
	if warnings := strings.Count(logs.String(), "the player did not start"); warnings != 1 {
		t.Errorf("%d warnings for one player that did not start: %s", warnings, logs.String())
	}
	p.close()
}

// A player that stops, as one the learner closes does and as one with
// no audio device to play on does, ends the playback and not the call.
// The UE notices the player itself, not only a write that fails, so
// that a short call says so too.
func TestPlaybackCarriesOnWhenThePlayerStops(t *testing.T) {
	player, _, _ := standIn(t, "exit 3")
	var logs safeBuffer
	p := &playback{path: player, log: logTo(&logs), codec: func() (sdp.Codec, bool) { return pcmuCodec, true }}
	if n, err := p.Write([]byte("packet")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; want the call to go on", n, err)
	}
	waitFor(t, "the player's exit to be noticed", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.stopped
	})
	if !strings.Contains(logs.String(), "the player stopped") {
		t.Fatalf("the playback did not say that the player stopped: %s", logs.String())
	}
	before := logs.Len()
	if n, err := p.Write([]byte("packet")); n != 6 || err != nil || logs.Len() != before {
		t.Errorf("after the player stopped: Write = %d, %v, log %s", n, err, logs.String())
	}
	p.close()
}

var pcmuCodec = sdp.Codec{PayloadType: sdp.PCMU, Name: sdp.NamePCMU, ClockRate: 8000}
