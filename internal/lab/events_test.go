package lab

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/yuu-biz/ims-practice/internal/hep"
)

type sse struct {
	name string
	data string
}

func readEvent(t *testing.T, r *bufio.Reader) sse {
	t.Helper()
	var e sse
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading the event stream: %v", err)
		}
		line = strings.TrimSuffix(line, "\n")
		switch {
		case line == "" && e.name != "":
			return e
		case strings.HasPrefix(line, "event: "):
			e.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			e.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestEventsDeliverMessagesAndState(t *testing.T) {
	h := newHarness(t, Config{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.server.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	const id = "call-sse@172.28.0.100"
	h.capture(ue1, ue1, pcscf, hep.ProtocolTypeSIP, id, invite(id))
	r := bufio.NewReader(resp.Body)

	e := readEvent(t, r)
	var m Message
	if err := json.Unmarshal([]byte(e.data), &m); e.name != "message" || err != nil {
		t.Fatalf("first event %q: %s (%v)", e.name, e.data, err)
	}
	if m.ID != 1 || m.CallID != id || m.Summary != "INVITE "+callee || m.Direction != DirectionSend {
		t.Errorf("message event = %+v", m)
	}

	e = readEvent(t, r)
	var change struct {
		Call *Call `json:"call"`
	}
	if err := json.Unmarshal([]byte(e.data), &change); e.name != "state" || err != nil || change.Call == nil {
		t.Fatalf("second event %q: %s (%v)", e.name, e.data, err)
	}
	if change.Call.CallID != id || change.Call.State != CallCalling {
		t.Errorf("state event = %+v", change.Call)
	}
}
