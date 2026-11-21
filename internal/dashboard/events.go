package dashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// event is one Server-Sent Event: its name and its data, already JSON.
type event struct {
	name string
	data []byte
}

// subscriberQueue is how many events one browser may fall behind. A
// slower one misses events rather than hold up the collector; it can
// reload the lists.
const subscriberQueue = 256

// broker hands each event to every open /api/events stream.
type broker struct {
	mu          sync.Mutex
	subscribers map[chan event]struct{}
}

func newBroker() *broker { return &broker{subscribers: map[chan event]struct{}{}} }

func (b *broker) subscribe() (<-chan event, func()) {
	ch := make(chan event, subscriberQueue)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subscribers, ch)
		b.mu.Unlock()
	}
}

func (b *broker) publish(name string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subscribers {
		select {
		case ch <- event{name, data}:
		default:
		}
	}
}

func (d *Dashboard) streamEvents(w http.ResponseWriter, r *http.Request) {
	events, unsubscribe := d.events.subscribe()
	defer unsubscribe()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			// json.Marshal writes no newline, so the data fits on the one
			// data line of the event.
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.name, e.data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
