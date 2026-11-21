package epc

import (
	"encoding/json"
	"errors"
	"sync"
)

// ErrUENotReachable: the UE has no event stream open, so the network
// cannot send it a message.
var ErrUENotReachable = errors.New("epc: the UE has no event stream open")

// streams holds the event stream each UE has open. It stands for the S1
// connection over which the MME reaches a UE, and like that connection
// it lives in the instance the UE connected to.
type streams struct {
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

func newStreams() *streams { return &streams{open: map[string]*stream{}} }

// connect opens the stream of a UE, in place of the one it had.
func (s *streams) connect(imsi string) *stream {
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
func (s *streams) disconnect(imsi string, st *stream) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.open[imsi] == st {
		delete(s.open, imsi)
	}
}

func (s *streams) send(imsi string, name MessageType, message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.open[imsi]
	if !ok {
		return ErrUENotReachable
	}
	select {
	case st.events <- event{name, data}:
		return nil
	default:
		return ErrUENotReachable
	}
}
