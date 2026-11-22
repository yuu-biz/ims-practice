package accessapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// ErrUENotReachable: the UE has no event stream open, so the network
// cannot send it a message. Send wraps it with the name of the stub,
// so errors.Is matches it.
var ErrUENotReachable = errors.New("the UE has no event stream open")

// Streams holds the event stream each UE has open. It stands for the
// UE's NAS signalling connection (over S1-MME or N2), and like that
// connection it lives in the instance the UE connected to.
type Streams struct {
	stub string // the name of the stub, which begins the error of Send
	mu   sync.Mutex
	open map[string]*stream // by IMSI
}

type stream struct {
	events chan event
	// closed is closed when a newer stream of the same UE replaces this
	// one.
	closed chan struct{}
}

type event struct {
	name MessageType
	data []byte
}

// streamBuffer bounds the messages waiting for a slow UE. A UE that
// falls this far behind is treated as not reachable.
const streamBuffer = 16

// NewStreams returns the event streams of the stub called stub, with no
// UE connected.
func NewStreams(stub string) *Streams { return &Streams{stub: stub, open: map[string]*stream{}} }

// connect opens the stream of a UE, in place of the one it had.
func (s *Streams) connect(imsi string) *stream {
	st := &stream{events: make(chan event, streamBuffer), closed: make(chan struct{})}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.open[imsi]; ok {
		close(old.closed)
	}
	s.open[imsi] = st
	return st
}

// disconnect forgets st, unless a newer stream has replaced it.
func (s *Streams) disconnect(imsi string, st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[imsi] == st {
		delete(s.open, imsi)
	}
}

// Send queues message on the UE's event stream as an event named name.
func (s *Streams) Send(imsi string, name MessageType, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.open[imsi]
	if !ok {
		return s.notReachable()
	}
	select {
	case st.events <- event{name, data}:
		return nil
	default:
		return s.notReachable()
	}
}

func (s *Streams) notReachable() error { return fmt.Errorf("%s: %w", s.stub, ErrUENotReachable) }

// Serve opens the event stream of the UE with imsi and writes the
// network's messages to it as Server-Sent Events: "event:" names the
// message, "data:" holds it as JSON. It returns when the UE goes away or
// a newer stream of the same UE replaces this one.
func (s *Streams) Serve(w http.ResponseWriter, r *http.Request, imsi string, log *slog.Logger) {
	st := s.connect(imsi)
	defer s.disconnect(imsi, st)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}
	log.Info("UE event stream open", "imsi", imsi, "remote_addr", r.RemoteAddr)
	defer log.Info("UE event stream closed", "imsi", imsi)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-st.closed:
			return
		case ev := <-st.events:
			// json.Marshal writes no newline, so the message fits on the one
			// data line of the event.
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
