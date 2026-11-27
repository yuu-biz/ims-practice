package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/yuu-biz/ims-practice/internal/sdp"
)

// playerName is the player that -play looks for. It comes with FFmpeg,
// which has decoders of its own for AMR and AMR-WB. This project ships
// no decoder and no player, and downloads neither: -play runs what the
// learner has already installed on the machine the UE runs on, and
// plays nothing when there is none.
const playerName = "ffplay"

// playerInstall is where FFmpeg is to be had, for the learner who has
// none.
const playerInstall = "https://ffmpeg.org/download.html"

// playerWaitLimit is how long the UE waits, once the call has ended,
// for the player to play out what it still holds. A test shortens it.
var playerWaitLimit = 10 * time.Second

// playerQueue is how many packets may wait for the player: a second of
// speech, at one 20 ms frame a packet. A player that falls further
// behind loses the packets that no longer fit, because a call runs to
// the clock of the network and waits for no listener.
const playerQueue = 50

// A playback writes what the call receives to the player, which it
// starts with the first packet: the codec of the call settles what the
// player has to be told. The packets go to the player from a goroutine
// of the playback's own, so that neither the receiving of RTP nor the
// end of the call ever waits for a player that has stopped reading.
//
// Every failure costs the learner the audio and nothing else: a player
// that never started, one that stopped, and one that cannot keep up are
// each said once, and the call goes on.
type playback struct {
	path  string
	codec func() (sdp.Codec, bool)
	log   *slog.Logger

	// What the player's own goroutines use: the packets waiting for it,
	// the end of the feeding, and the player's exit.
	frames chan []byte
	done   chan struct{}
	ended  chan error

	// mu guards the rest. It is never held while anything is written to
	// the player.
	mu       sync.Mutex
	cmd      *exec.Cmd
	in       io.WriteCloser
	stopped  bool // nothing more is written to the player
	closing  bool // the call ended, so the player is expected to stop
	reported bool // the learner has been told that the player stopped
	behind   bool // the learner has been told that packets are dropped
}

// newPlayback is the playback of -play, or nil when nothing will be
// played, which it says once. The player is looked up before the call,
// so that a -player that is not there is known by then.
func newPlayback(o options, log *slog.Logger, codec func() (sdp.Codec, bool)) *playback {
	if !o.play {
		return nil
	}
	name := o.player
	if name == "" {
		name = playerName
	}
	path, err := exec.LookPath(name)
	if err != nil {
		log.Warn("the call is not played: no player was found", "player", name,
			"hint", playerName+" comes with FFmpeg, which this project does not ship; install FFmpeg from "+
				playerInstall+", or give -player the path of "+playerName+" or of a program that takes its options")
		return nil
	}
	return &playback{path: path, codec: codec, log: log}
}

// Write hands the player what has arrived, and starts it with the first
// packet. The error is always nil: the call is not the playback's to
// end.
func (p *playback) Write(b []byte) (int, error) {
	p.mu.Lock()
	if !p.stopped && p.in == nil {
		if err := p.start(); err != nil {
			p.log.Warn("the call is not played: the player did not start", "player", p.path, "error", err.Error(),
				"hint", "give -player the path of a player, or install FFmpeg from "+playerInstall)
			p.stop()
		}
	}
	stopped, frames := p.stopped, p.frames
	p.mu.Unlock()
	if stopped {
		return len(b), nil
	}
	select {
	case frames <- bytes.Clone(b):
	default:
		p.fellBehind()
	}
	return len(b), nil
}

// fellBehind says once that the player is not keeping up with the call.
func (p *playback) fellBehind() {
	p.mu.Lock()
	say := !p.behind
	p.behind = true
	p.mu.Unlock()
	if say {
		p.log.Warn("some of the call is not played: the player is behind", "player", p.path, "packets_waiting", playerQueue)
	}
}

// start runs the player for the codec of the call. p.mu is held.
func (p *playback) start() error {
	codec, ok := p.codec()
	if !ok {
		return errors.New("the call has no audio stream")
	}
	cmd := exec.Command(p.path, playerArgs(codec)...)
	// Whatever the player says goes to standard error: standard output
	// may be carrying the stream of -received-out.
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		in.Close()
		return err
	}
	p.cmd, p.in = cmd, in
	p.frames, p.done, p.ended = make(chan []byte, playerQueue), make(chan struct{}), make(chan error, 1)
	go p.feed(in, p.frames, p.done)
	go p.watch(cmd, p.ended)
	p.log.Info("playing the call", "player", p.path, "codec", codec.Name)
	return nil
}

// feed writes the packets to the player, away from the goroutines of
// the call, and ends the playback when a write fails. It is the only
// writer of the pipe.
//
// cmd.Wait, in watch, runs beside these writes: it closes this end of
// the pipe once the player has exited, so a write that was in flight
// ends with an error on a closed file rather than with the broken pipe
// it would otherwise get. Both end the playback the same way.
func (p *playback) feed(in io.WriteCloser, frames <-chan []byte, done <-chan struct{}) {
	for {
		select {
		case b := <-frames:
			if _, err := in.Write(b); err != nil {
				if p.ending() {
					p.log.Warn("the rest of the call is not played: the player stopped reading",
						"player", p.path, "error", err.Error())
				}
				return
			}
		case <-done:
			return
		}
	}
}

// watch ends the playback when the player does, which is how a player
// the learner closes, or one with no audio device to play on, makes
// itself known, even while no write is waiting on it.
func (p *playback) watch(cmd *exec.Cmd, ended chan<- error) {
	err := cmd.Wait()
	if p.ending() {
		p.log.Warn("the rest of the call is not played: the player stopped", "player", p.path, "exit", exitOf(err))
	}
	ended <- err
}

// ending marks the playback over and tells whether the learner should
// hear why: not when the call is ending anyway, and not twice.
func (p *playback) ending() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	say := !p.closing && !p.stopped
	p.reported = p.reported || say
	p.stop()
	return say
}

func exitOf(err error) string {
	if err == nil {
		return "0"
	}
	return err.Error()
}

// playerArgs are what the player is given for the codec of the call.
// AMR and AMR-WB arrive in the storage format, whose magic number names
// the codec (RFC 4867 5.1), while PCMU is nothing but mu-law samples,
// so the player is told what they are.
func playerArgs(codec sdp.Codec) []string {
	args := []string{"-nodisp", "-autoexit", "-loglevel", "error"}
	if codec.Is(sdp.NamePCMU) {
		args = append(args, "-f", "mulaw", "-ar", strconv.Itoa(codec.ClockRate), "-ac", "1")
	}
	return append(args, "-i", "-")
}

// close ends the player's input and waits for it to play out what it
// holds, for as long as playerWaitLimit. Closing the pipe is what frees
// a feeder stuck in a write to a player that stopped reading, so the
// end of the call waits for the player's exit alone, and only as long
// as that limit.
func (p *playback) close() {
	p.mu.Lock()
	if p.closing {
		p.mu.Unlock()
		return
	}
	p.closing = true
	cmd, ended, done := p.cmd, p.ended, p.done
	p.stop()
	p.mu.Unlock()
	if done != nil {
		close(done)
	}
	if cmd == nil {
		return
	}
	select {
	case err := <-ended:
		// A player that failed, as one with no audio device to play on
		// does, leaves a silent call behind: say so, unless the learner
		// has heard about it already.
		p.mu.Lock()
		said := p.reported
		p.mu.Unlock()
		if err != nil && !said {
			p.log.Warn("the player ended with an error, so the call may not have been heard",
				"player", p.path, "exit", exitOf(err))
		}
	case <-time.After(playerWaitLimit):
		cmd.Process.Kill()
		<-ended
		p.log.Warn("the player was stopped: it had not finished", "player", p.path, "after", playerWaitLimit.String())
	}
}

// stop ends the playback without waiting for the player. Closing the
// pipe also ends the write of a feeder that the player has stopped
// reading. p.mu is held.
func (p *playback) stop() {
	p.stopped = true
	if p.in != nil {
		p.in.Close()
	}
}
